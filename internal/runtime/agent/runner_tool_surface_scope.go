package agent

import (
	"encoding/json"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"slices"
	"strings"
)

func hideImplementationWriteOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if implementationPreWriteToolAllowed(tool) {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func hideReviewRepairTerminalOnlyToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if strings.TrimSpace(tool) == workspace.ToolNameSubmitPlan {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func hideImplementationScopedToolsForTurn(hidden map[string]bool, owned map[string]bool, manager *workspace.Manager) {
	if hidden == nil || owned == nil || manager == nil {
		return
	}

	extReg := manager.ExternalToolsRegistry()
	for _, tool := range manager.AllowedTools() {
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) {
			continue
		}

		hidden[tool] = true
		owned[tool] = true
	}
}

func releaseHiddenTools(hidden map[string]bool, owned map[string]bool) {
	if hidden == nil || owned == nil {
		return
	}

	for tool := range owned {
		delete(hidden, tool)
		delete(owned, tool)
	}
}

func hideExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool, manager *workspace.Manager) {
	if hidden == nil || exactHidden == nil || manager == nil {
		return
	}

	for _, tool := range manager.AllowedTools() {
		if !implementationPreWriteToolAllowed(tool) {
			hidden[tool] = true
			exactHidden[tool] = true
		}
	}
}

func implementationPreWriteToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	// submit_plan is the planner-phase write for plan-then-execute
	// mode; without this exception the exact-anchor pre-write filter
	// hides it and the planner has nothing to call.
	if tool == workspace.ToolNameSubmitPlan {
		return true
	}

	return false
}

func implementationPreWriteAllowedTools(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationPreWriteToolAllowed(tool) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func implementationScopedEditToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	switch tool {
	case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate:
		return true
	case workspace.ToolNameSubmitPlan:
		// Plan-then-execute planner phase: submit_plan IS the planner's
		// write — it captures the structured plan the executor phase
		// will dispatch. Without this case the scoped-edit surface
		// filter strips it before the model ever sees it, leaving the
		// planner with only read tools and no way to finish.
		return true
	default:
		return implementationScopedSemanticDiscoveryToolAllowed(tool)
	}
}

func implementationScopedEditToolAllowedWithRegistry(tool string, extReg *workspace.ExternalToolRegistry) bool {
	tool = strings.TrimSpace(tool)
	if implementationScopedEditToolAllowed(tool) {
		return true
	}

	return extReg != nil && extReg.IsReadOnlyDiscovery(tool)
}

func implementationScopedEditAllowedToolsWithRegistry(tools []string, extReg *workspace.ExternalToolRegistry) []string {
	if len(tools) == 0 {
		return nil
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if implementationScopedEditToolAllowedWithRegistry(tool, extReg) && !slices.Contains(filtered, tool) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func implementationScopedSemanticDiscoveryToolAllowed(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	// Delegate explicit tool-name classification to the catalog profile.
	if len(workerruntime.AllowedToolsForCapabilityProfile([]string{tool}, workerruntime.CapabilityImplementationScopedEdit)) > 0 {
		return true
	}

	// Language-prefixed tools (go_find_symbol, cpp_definition, etc.) are
	// not in the explicit name list; classify via their discovery profile.
	profile := workspace.DiscoveryProfileForRequest(workspace.ToolRequest{Tool: tool})
	switch profile.Class {
	case "language_symbol", "language_relationship", "diagnostics":
		return true
	default:
		return false
	}
}

func releaseExactAnchorReadTools(hidden map[string]bool, exactHidden map[string]bool) {
	if hidden == nil || exactHidden == nil {
		return
	}

	for tool := range exactHidden {
		delete(hidden, tool)
		delete(exactHidden, tool)
	}
}

func workspaceWriteResultCompleted(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	return strings.TrimSpace(result.Error) == ""
}

func exactAnchorImplementationActive(brief workerruntime.WorkerRuntimeBrief) bool {
	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	return slices.Contains(brief.Constraints, "exact_anchor_implementation")
}

func requiredEditPathScopeRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) (summary string, errorText string, reject bool) {
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
	if len(required) == 0 || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return "", "", false
	}

	paths := writeRequestPaths(req)
	if len(paths) == 0 {
		return "", "", false
	}

	var outside []string

	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if strings.TrimSpace(req.Tool) == workspace.ToolNameMkdir && requiredEditPathAllowsMkdir(normalized, required) {
			continue
		}

		if _, ok := required[normalized]; !ok {
			outside = append(outside, normalized)
		}
	}

	if len(outside) == 0 {
		return "", "", false
	}

	return "Write outside required edit paths rejected",
		fmt.Sprintf("this implementation contract restricts writes to required_edit_paths %s; attempted write path(s) outside that scope: %s. Use an allowed path or report a concrete scope blocker.",
			quotedRunnerStrings(mapKeys(required)), quotedRunnerStrings(outside)),
		true
}

func writeRequestPaths(req workspace.ToolRequest) []string {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir:
		if strings.TrimSpace(req.Path) != "" {
			return []string{req.Path}
		}
	case workspace.ToolNameApplyPatch:
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}

		return workspace.PatchOpPaths(ops)
	case workspace.ToolNameGoMoveDeclarations:
		var paths []string

		if strings.TrimSpace(req.Path) != "" {
			paths = append(paths, req.Path)
		}

		if strings.TrimSpace(req.Destination) != "" {
			paths = append(paths, req.Destination)
		}

		for _, move := range req.Moves {
			if strings.TrimSpace(move.Destination) != "" {
				paths = append(paths, move.Destination)
			}
		}

		return paths
	}

	return nil
}

func normalizedRequiredEditPathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		normalized := normalizeRunnerPath(path)
		if normalized != "" {
			out[normalized] = struct{}{}
		}
	}

	return out
}

func readRequestTargetsRequiredEditPath(req workspace.ToolRequest, requiredPaths []string) bool {
	required := normalizedRequiredEditPathSet(requiredPaths)
	if len(required) == 0 {
		return false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return false
	}

	if _, ok := required[path]; ok {
		return true
	}

	return false
}

func scopedReadRecoveryRejectionKey(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	target := normalizeRunnerPath(util.FirstNonEmpty(req.Path, req.ID, req.Handle, req.Query, req.Pattern, req.Task, req.Command))

	if target == "" {
		target = "<empty-target>"
	}

	return tool + ":" + normalizeActionToken(target)
}

func requiredEditPathAllowsMkdir(dir string, required map[string]struct{}) bool {
	dir = strings.Trim(strings.TrimSpace(dir), "/")
	if dir == "" {
		return false
	}

	for path := range required {
		parent := strings.Trim(strings.TrimSpace(filepath.ToSlash(filepath.Dir(path))), "/")
		if parent == dir || strings.HasPrefix(parent, dir+"/") {
			return true
		}
	}

	return false
}

func markRuntimeBriefPathsObserved(manager *workspace.Manager, brief workerruntime.WorkerRuntimeBrief) {
	if manager == nil {
		return
	}

	candidates := append([]string{}, brief.TargetRefs...)

	candidates = append(candidates, brief.EvidencePaths...)
	candidates = append(candidates, brief.ChangedPaths...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.TargetPacket, "\n"))...)
	candidates = append(candidates, pathparse.MentionedWorkspacePaths(strings.Join(brief.UserBoundaries, "\n"))...)

	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\" ,.;:")
		if candidate == "" || !strings.Contains(candidate, ".") {
			continue
		}

		manager.MarkPathObserved(candidate)
	}
}

func nonEmptyRunnerStrings(values []string, max int) []string {
	if max <= 0 || len(values) == 0 {
		return nil
	}

	out := make([]string, 0, max)

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		out = append(out, trimmed)
		if len(out) >= max {
			return out
		}
	}

	return out
}

func hasSuccessfulVerificationResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if resultCountsAsSuccessfulVerification(result) {
			return true
		}
	}

	return false
}

func shellCommandFromToolResult(result workspace.ToolResult) string {
	for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "$ ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "$ "))
		}

		if line != "" {
			return ""
		}
	}

	return ""
}

func hasSuccessfulTaskResult(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func postChildReadBudgetForBrief(brief workerruntime.WorkerRuntimeBrief) int {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return 3
	}

	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return 0
	}

	if runnerBriefThoroughness(brief) == "standard" {
		return 1
	}

	return 2
}

func runnerBriefThoroughness(brief workerruntime.WorkerRuntimeBrief) string {
	for _, constraint := range brief.Constraints {
		key, value, ok := strings.Cut(strings.TrimSpace(constraint), "=")
		if !ok || strings.TrimSpace(key) != "thoroughness" {
			continue
		}

		return strings.ToLower(strings.TrimSpace(value))
	}

	return ""
}

func successfulReadClassResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		req := workspace.ToolRequest{Tool: strings.TrimSpace(result.Tool)}
		if isReadClassTool(req) {
			count++
		}
	}

	return count
}

func toolRequestKey(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Args is `json:"-"` on ToolRequest (it intentionally stays out of
	// log/event serialization), so it would NOT contribute to this key
	// otherwise. For MCP tools and external tools the typed fields are
	// empty and Args carries the entire request shape — without this
	// suffix two materially-different calls (e.g. `lookup` with
	// {"enum":"JobType"} vs {"enum":"ColonistState"}) would collapse to
	// the same key and trip the "replayed prior evidence" guard.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func normalizedToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(normalizeRunnerToolRequest(req))
	if err != nil {
		return ""
	}

	// Mirror the toolRequestKey suffix: Args is `json:"-"` on
	// ToolRequest so the marshal above misses it. The doom_loop_guard
	// and other fingerprint-based heuristics need to distinguish
	// external/MCP calls that carry their entire request shape in
	// Args; without this, three different lookup({enum:X}),
	// lookup({enum:Y}), lookup({enum:Z}) calls all collapse to one
	// fingerprint and trip the doom_loop_guard at threshold 3.
	if len(req.Args) > 0 {
		return string(data) + "|args=" + string(req.Args)
	}

	return string(data)
}

func doomLoopForcedCloseoutPrompt(req workspace.ToolRequest, identicalCallRun int) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the same tool"
	}

	return fmt.Sprintf("The exact same %s request was stopped after %d identical attempts. Tools are now disabled. Complete the active contract from the evidence already gathered. If the evidence is sufficient, answer with the required final decision or summary. If one concrete claim remains unsupported, answer with <<QUESTION>> and the exact unresolved blocker; do not request more tools.", tool, identicalCallRun)
}

func shouldRejectExactReplayBeforeLoopGate(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return true
	default:
		return false
	}
}

func normalizeRunnerToolRequest(req workspace.ToolRequest) workspace.ToolRequest {
	req.Path = normalizeRunnerPath(req.Path)
	req.Destination = normalizeRunnerPath(req.Destination)
	req.Workdir = normalizeRunnerPath(req.Workdir)
	req.NamedGap = ""
	req.WhyNotAnswering = ""

	for i := range req.Moves {
		req.Moves[i].Destination = normalizeRunnerPath(req.Moves[i].Destination)
	}

	return req
}

// truncForRetryReason trims a summary so the stored "why this was
// blocked" string stays small — model-facing text, not log-sized.
func truncForRetryReason(s string) string {
	s = strings.TrimSpace(s)

	const max = 160

	if len(s) <= max {
		return s
	}

	return s[:max-3] + "..."
}

// isContentInspectionTool reports whether a tool actually inspects
// content (as opposed to listing directory names). Used to reset the
// consecutive-list_files streak — running search_content, read_file, a
// go_* symbol tool, or a cpp_* symbol tool is exactly the narrowing
// behavior we want to reward.
func isContentInspectionTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameSearchContent, workspace.ToolNameSearchFiles, workspace.ToolNameRg, workspace.ToolNameReadFile,
		workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability,
		"go_file_outline", "go_read_symbol", "go_find_symbol", "go_find_type",
		"go_find_const", "go_workspace_symbol", "go_definition", "go_references",
		"go_implementations", "go_calls_from", "go_calls_into", "go_extract_body",
		"go_related_files", "go_find_package", "go_importers", "go_diagnostics",
		"cpp_file_outline", "cpp_find_symbol", "cpp_read_symbol", "cpp_workspace_symbol",
		"cpp_definition", "cpp_declaration", "cpp_implementations",
		"cpp_references", "cpp_calls_from", "cpp_calls_into", "cpp_extract_body",
		"cpp_related_files", "cpp_includes", "cpp_who_includes",
		"cpp_blast_radius", "cpp_dead_symbol", "cpp_diagnostics":
		return true
	}

	return false
}

// isReadClassTool reports whether a tool request is read-only / inspection.
// Used to drive the plan-mode read-budget nudge. Kept explicit (allow-list)
// so mutating tools can never be miscounted. Shell is excluded because it
// can mutate even when the command looks benign.
func isReadClassTool(req workspace.ToolRequest) bool {
	return workspace.IsReadTool(strings.TrimSpace(req.Tool))
}

const narrowToolFamilyNudgeAt = 5

// narrowToolFamily returns a stable family name for narrow/specific tools
// that the model might over-use in a loop. Returns "" for broad tools
// (discovery_query, context_query, harness_audit, etc.) so the repetition
// nudge resets when the model tries a broader approach.
func narrowToolFamily(tool string) string {
	tool = strings.TrimSpace(tool)
	switch tool {
	case workspace.ToolNameCodeNavigate,
		"go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		return "code_navigate"
	case workspace.ToolNameReadFile:
		return "read_file"
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return "search"
	case workspace.ToolNameShell:
		return "shell"
	default:
		return ""
	}
}

// narrowToolFamilyAutoRedirect reports whether a narrow tool family
// should be auto-redirected to a broader discovery tool after the
// repetition nudge fires. Only code_navigate enumeration benefits
// from this redirect; read_file and search are legitimate evidence
// tools that should never be redirected away.
func narrowToolFamilyAutoRedirect(family string) bool {
	return family == "code_navigate"
}

func toolActionClass(req workspace.ToolRequest) string {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case workspace.ToolNameListFiles:
		if workspace.IsScopedInspectionRequest(req) {
			return "list_files:" + normalizeActionToken(req.Path)
		}

		return "broad_inspection"
	case workspace.ToolNameShell:
		return shellActionClass(req.Command)
	case workspace.ToolNameReadFile:
		if target := util.FirstNonEmpty(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path)); target != "" {
			action := "read_file:" + normalizeActionToken(target)
			if strings.TrimSpace(req.ID) == "" && (req.StartLine > 0 || req.EndLine > 0) {
				action += fmt.Sprintf(":%d-%d", req.StartLine, req.EndLine)
			}

			return action
		}

		return workspace.ToolNameReadFile
	case workspace.ToolNameSourceSanity:
		return tool + ":" + normalizeActionToken(req.Path)
	case workspace.ToolNameCodeNavigate:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameReadOnlyCapability:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Query), strings.TrimSpace(req.Path), strings.TrimSpace(req.Language))
		return tool + ":" + normalizeActionToken(req.Capability) + ":" + normalizeActionToken(target)
	case workspace.ToolNameEditFile:
		shape := strings.TrimSpace(req.OldText) + "->" + strings.TrimSpace(req.NewText)
		if req.StartLine > 0 || req.EndLine > 0 || strings.TrimSpace(req.Content) != "" {
			shape = fmt.Sprintf("%d-%d:%s", req.StartLine, req.EndLine, strings.TrimSpace(req.Content))
		}

		return "edit_file:" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(shape)
	case workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameRg:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Glob)
	case workspace.ToolNameDiscoveryQuery:
		purpose := util.FirstNonEmpty(strings.TrimSpace(req.Purpose), strings.TrimSpace(req.Query))
		return tool + ":" + normalizeActionToken(purpose) + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameStructuralSearch:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameDependencyGraph:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Language)
	case workspace.ToolNameHistoryRange, workspace.ToolNameBlameRange:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(fmt.Sprintf("%d-%d", req.StartLine, req.EndLine))
	case workspace.ToolNameHistorySymbol, workspace.ToolNameCtagsSymbols, workspace.ToolNameSemgrepScan:
		return tool + ":" + normalizeActionToken(req.Path) + ":" + normalizeActionToken(req.Pattern)
	case workspace.ToolNameJoernScan:
		return tool + ":" + normalizeActionToken(req.Path)
	case "go_find_symbol", "go_find_type", "go_find_const", "go_file_outline", "go_read_symbol",
		"go_find_package", "go_related_files", "go_workspace_symbol",
		"go_definition", "go_references", "go_implementations",
		"go_calls_from", "go_calls_into", "go_extract_body", "go_importers", "go_diagnostics":
		target := util.FirstNonEmpty(strings.TrimSpace(req.Pattern), strings.TrimSpace(req.Path), strings.TrimSpace(req.ID))
		return tool + ":" + normalizeActionToken(target)
	case workspace.ToolNameGoMoveDeclarations:
		target := util.FirstNonEmpty(strings.TrimSpace(req.Path)+"->"+strings.TrimSpace(req.Destination), strings.TrimSpace(req.Pattern))
		if len(req.Moves) > 0 {
			target = strings.TrimSpace(req.Path) + "->batch"
		}

		return tool + ":" + normalizeActionToken(target)
	case workspace.ToolNameTaskLedger:
		return workspace.ToolNameTaskLedger
	default:
		return tool
	}
}

func isBootstrappedTargetReadReminder(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == workspace.ToolNameReadFile &&
		strings.TrimSpace(result.Summary) == "Implementation bootstrap already inspected this target" &&
		strings.TrimSpace(result.Error) != ""
}

func declarationMovesForbiddenByMessages(messages []llama.ChatMessage) bool {
	if len(messages) == 0 {
		return false
	}

	lastUser := -1

	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == "user" {
			lastUser = i
			break
		}
	}

	if lastUser < 0 {
		return false
	}

	for _, message := range messages[lastUser:] {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "system" {
			continue
		}

		if textForbidsDeclarationMoves(message.Content) {
			return true
		}
	}

	return false
}

func textForbidsDeclarationMoves(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))

	return containsAny(normalized,
		"do not move declarations",
		"do not move declaration",
		"don't move declarations",
		"no declaration moves",
		"without moving declarations",
		"do not create files or move declarations",
	)
}

func declarationMoveConstraintRejection(req workspace.ToolRequest, declarationMovesForbidden bool) (string, string, bool) {
	if !declarationMovesForbidden {
		return "", "", false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameGoMoveDeclarations:
		return "Declaration move rejected by task constraint",
			"the current task explicitly forbids moving declarations. Do not call go_move_declarations; allowed contract outcomes are narrow same-file read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	case "go_file_outline":
		return "Go outline rejected by task constraint",
			"the current task explicitly forbids declaration moves, and go_file_outline primarily suggests declaration-move batches. Do not call go_file_outline again; allowed contract outcomes are exact-line read, approved same-file edit, verification evidence, or a concrete blocker.",
			true
	default:
		return "", "", false
	}
}

func marshalToolRequestRaw(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return ""
	}

	return string(data)
}

func shellActionClass(command string) string {
	if isVerificationCommandText(command) {
		return "shell_verification:" + normalizeActionToken(command)
	}

	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return workspace.ToolNameShell
	}

	switch fields[0] {
	case "ls", "find", "tree":
		return "broad_inspection"
	case workspace.ToolNameRg, "grep", "awk", "sed", "cat", "head", "tail":
		return "shell_narrow:" + normalizeActionToken(command)
	default:
		return "shell:" + normalizeActionToken(fields[0])
	}
}

func normalizeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "_"
	}

	if len(value) > 96 {
		return value[:96]
	}

	return value
}

func containsRunnerTool(tools []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, tool := range tools {
		if strings.TrimSpace(tool) == target {
			return true
		}
	}

	return false
}

func runnerAllowsTool(tools []string, tool string) bool {
	return containsRunnerTool(tools, tool)
}

func runnerPlanExecuteTextualAllowedTools(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily == PlanThenExecuteReviewRepairTaskFamily {
		if !completionGateResultSeen(results) && len(planThenExecuteReviewRepairFailedPhaseObligations(brief)) == 0 {
			allowed := filterPhaseTools(brief.AllowedTools, workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameContextStatus, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameSubmitPlan)
			if len(allowed) > 0 {
				return allowed
			}

			return []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameContextStatus, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom, workspace.ToolNameSubmitPlan}
		}

		contract := deriveTurnPhaseContract(TurnPhaseContractInput{
			PhaseHint:        turnPhaseReviewRepairTerminal,
			Brief:            brief,
			Results:          results,
			BaseAllowedTools: brief.AllowedTools,
		})
		if len(contract.AllowedTools) > 0 {
			return contract.AllowedTools
		}

		return currentAllowedTools
	}

	if taskFamily != PlanThenExecutePlannerTaskFamily {
		if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
			return currentAllowedTools
		}

		return currentAllowedTools
	}

	state, contract, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if runnerPlanExecuteTextualContractDisablesTools(contract) {
		return nil
	}
	if allowed := runnerPlanExecuteTextualAllowedToolsFromProjection(state, contract, baseTools); len(allowed) > 0 {
		return allowed
	}

	if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
		return currentAllowedTools
	}

	if slices.Contains(state.TerminalOptions, workspace.ToolNameSubmitPlan) || planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return []string{workspace.ToolNameSubmitPlan}
	}

	return currentAllowedTools
}

func runnerPlanExecuteTextualAllowedToolsForRequest(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, req workspace.ToolRequest) []string {
	allowed := runnerPlanExecuteTextualAllowedTools(currentAllowedTools, brief, results)
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return allowed
	}

	if len(uniquePhaseStrings(allowed)) == 1 && runnerAllowsTool(allowed, workspace.ToolNameSubmitPlan) {
		return allowed
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || runnerAllowsTool(allowed, tool) {
		return allowed
	}

	state, contract, baseTools := runnerPlanExecuteTextualPlanningProjection(currentAllowedTools, brief, results)
	if runnerPlanExecuteTextualContractDisablesTools(contract) {
		return allowed
	}
	if !slices.Contains(state.ParserVisibleTools, tool) {
		return allowed
	}

	if len(baseTools) > 0 && !runnerAllowsTool(baseTools, tool) {
		return allowed
	}

	return uniquePhaseStrings(append(append([]string(nil), allowed...), tool))
}

func runnerPlanExecuteTextualContractDisablesTools(contract TurnPhaseContract) bool {
	return contract.PhaseID == turnPhaseSourceSatisfiedTerminal
}

func runnerPlanExecuteTextualPlanningProjection(currentAllowedTools []string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (PlanExecutePlanningState, TurnPhaseContract, []string) {
	baseTools := uniquePhaseStrings(append(append([]string(nil), brief.AllowedTools...), currentAllowedTools...))
	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		baseTools = uniquePhaseStrings(append(baseTools, workspace.ToolNameSubmitPlan))
	}
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
		baseTools = uniquePhaseStrings(append(baseTools, planThenExecuteAnchorRefreshToolNames()...))
	}
	contract := deriveTurnPhaseContract(TurnPhaseContractInput{
		Brief:             brief,
		Results:           results,
		BaseAllowedTools:  baseTools,
		TextualToolParser: true,
		ToolTransportMode: "textual",
	})

	return state, contract, baseTools
}

func runnerPlanExecuteTextualAllowedToolsFromProjection(state PlanExecutePlanningState, contract TurnPhaseContract, baseTools []string) []string {
	for _, tools := range [][]string{
		state.CallableTools,
		state.ParserVisibleTools,
		contract.ActionPolicy.CallableToolNames,
		contract.AllowedTools,
	} {
		allowed := runnerFilterPlanExecuteProjectionTools(baseTools, tools)
		if len(allowed) > 0 {
			return allowed
		}
	}

	return nil
}

func runnerFilterPlanExecuteProjectionTools(baseTools []string, projectionTools []string) []string {
	projectionTools = uniquePhaseStrings(projectionTools)
	if len(projectionTools) == 0 {
		return nil
	}

	if len(baseTools) == 0 {
		return projectionTools
	}

	return filterPhaseTools(baseTools, projectionTools...)
}
