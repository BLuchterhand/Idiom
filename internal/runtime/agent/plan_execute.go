package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func (r *Runner) implementationScopedDisallowedToolRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, reject bool) {
	if len(results) > 0 && strings.TrimSpace(results[len(results)-1].Code) == writeDescriptionMissingCode {
		return "", "", "", false
	}

	if r == nil || !implementationScopedEditSurfaceActive(r.RuntimeBrief, results) ||
		workspace.ImplementationScopedEditToolAllowedWithExternalReadOnlyDiscovery(req.Tool, r.workspaceToolSurfaceSnapshot().ExternalToolIsReadOnlyDiscovery) {

		return "", "", "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "requested tool"
	}

	summary = "Scoped implementation tool rejected"
	errorText = fmt.Sprintf("%s is outside the scoped required-path implementation surface for %s. The available contract outcomes are required-path write capability, semantic/context/declared project discovery for a named gap, a narrow raw read on a required path or evidence path, or <<QUESTION>> with the exact blocker.", tool, target)
	prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract menu is: required-path edit, context/language/declared read-only project discovery for the exact edit-safety fact, narrow raw read only for anchor confirmation on a required path or evidence path, or <<QUESTION>> with the exact blocker. Do not use task planning, shell, opaque external tools, or broad raw search until the required-path contract is resolved.", target)

	return summary, errorText, prompt, true
}

func (r *Runner) writeReadyPacketReadWithoutGapRecovery(req workspace.ToolRequest, results []workspace.ToolResult) (summary string, errorText string, prompt string, hideNonWrite bool, reject bool) {
	if r == nil || !postReadinessToolContractForBrief(r.RuntimeBrief, results, false, false).RequireSourceRefreshContract || !r.scopedReadClassTool(req) {
		return "", "", "", false, false
	}

	hideNonWrite = exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, results)

	missingGap := textutil.FirstNonEmpty(req.NamedGap, req.WhyNotAnswering) == ""
	// The read scope is the FULL required-edit-paths set plus explicit
	// packet evidence paths, not just the remaining edit paths. Once the
	// model has written one file in the brief, reads on that file are still
	// legitimate — the model often needs to reference its own header to write
	// the impl, verify a declaration it just landed, or mirror an existing
	// pattern from a sibling required file. Explicit evidence paths are also
	// legitimate read-only inputs (for example refine's previous draft/review
	// files). Tool-result evidence is intentionally not scope: context/query
	// packets can mention candidate or related files, and those candidates
	// must support reasoning without expanding the allowed read surface.
	readScope := implementationScopedExplicitReadScope(r.RuntimeBrief)
	outOfScope := implementationScopedEditSurfaceActive(r.RuntimeBrief, results) && !readRequestTargetsRequiredEditPath(req, readScope)

	if outOfScope && r.scopedImplementationDiscoveryLookupAllowed(req, readScope) {
		return "", "", "", false, false
	}

	// In-scope reads (target one of the brief's required_edit_paths) are
	// allowed without an explicit named_gap. The safety reason is
	// implicit — "verify the anchor on a path we're about to edit" — and
	// the schema describes named_gap as optional, so requiring it on
	// in-scope reads contradicted the surface the planner sees. Local
	// Qwen consistently calls code_navigate without named_gap and got
	// stuck; the previous behavior also rejected the asymmetric case
	// where read_file happened to include named_gap and code_navigate
	// didn't (Phase 6.5 / Phase 5b dispatch debugging, 2026-05-10).
	// Out-of-scope reads still get blocked regardless of named_gap —
	// that's the protection the gate is meant to provide.
	if !outOfScope {
		return "", "", "", false, false
	}

	scratchpadDeliverable := briefIsScratchpadDeliverableLane(r.RuntimeBrief)

	_ = missingGap // retained for the rejection-message switch below

	missing := r.remainingRequiredEditPaths(results)
	target := "the required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	summary = "Scoped implementation read rejected"

	switch {
	case missingGap && outOfScope:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must target a required edit path or explicit evidence path and include named_gap describing one exact edit-safety fact.", target)
	case missingGap:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. A read/search before resolving that contract must include named_gap describing one exact edit-safety fact.", target)
	default:
		errorText = fmt.Sprintf("the target packet, required_edit_paths, and evidence_paths establish the edit scope for %s. This read/search targets outside the required edit paths and explicit evidence paths.", target)
	}

	if scratchpadDeliverable {
		prompt = fmt.Sprintf("The implementation packet defines a non-source deliverable for %s, and the attempted read/search was outside the required or evidence paths. Treat this as scope evidence for the deliverable: record the missing or out-of-scope path in a blocker or scope-correction section instead of broadening discovery.", target)
	} else if hideNonWrite {
		prompt = fmt.Sprintf("The exact-anchor implementation packet is write-first for %s, and the attempted read/search did not satisfy the contract. Read/search tools are now hidden for this turn. The available contract outcomes are the smallest safe write to each remaining required path, or <<QUESTION>> naming a stale or missing supplied anchor.", target)
	} else {
		prompt = fmt.Sprintf("The implementation packet defines the edit scope for %s. The available contract outcomes are an allowed edit on a required path, semantic/context/declared project discovery for a named edit-safety gap, one narrow raw read inside a required path or explicit evidence path for anchor confirmation, or <<QUESTION>> naming the blocker when a remaining path cannot be edited safely from the packet plus scoped evidence.", target)
	}

	return summary, errorText, prompt, hideNonWrite, true
}

func implementationScopedExplicitReadScope(brief workerruntime.WorkerRuntimeBrief) []string {
	return textutil.Merge(brief.RequiredEditPaths, brief.EvidencePaths)
}

func (r *Runner) scratchpadDeliverableFailedEvidenceNudge(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) (string, bool) {
	if r == nil || !briefIsScratchpadDeliverableLane(r.RuntimeBrief) || strings.TrimSpace(result.Error) == "" {
		return "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || !r.scopedReadClassTool(req) || len(r.remainingRequiredEditPaths(results)) == 0 {
		return "", false
	}

	missing := r.remainingRequiredEditPaths(results)
	target := "the required deliverable"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(result.Error)
	if reason == "" {
		reason = strings.TrimSpace(result.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	return fmt.Sprintf("The optional evidence request for %s failed: %s. For this non-source deliverable, treat that as an evidence fact: record the missing, stale, ambiguous, or out-of-scope evidence in the deliverable itself rather than repairing the lookup through another raw or lower-level read. The next action should be either writing the deliverable with the evidence gap recorded, or answering <<QUESTION>> if the gap makes the deliverable impossible.", target, reason), true
}

func scratchpadDeliverableFailedEvidenceResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if !briefIsScratchpadDeliverableLane(brief) {
		return workspace.ToolResult{}, false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())) == 0 {
		return workspace.ToolResult{}, false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) == "" || workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		if strings.TrimSpace(result.Summary) == "Scratchpad evidence follow-up rejected" {
			continue
		}

		if workspace.IsReadTool(strings.TrimSpace(result.Tool)) {
			return result, true
		}
	}

	return workspace.ToolResult{}, false
}

func scratchpadDeliverableFailedEvidenceStatus(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	failed, ok := scratchpadDeliverableFailedEvidenceResult(brief, results)
	if !ok {
		return ""
	}

	target := "the required deliverable"
	if missing := missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet()); len(missing) > 0 {
		target = strings.Join(missing, ", ")
	} else if paths := nonEmptyRunnerStrings(brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	reason := strings.TrimSpace(failed.Error)
	if reason == "" {
		reason = strings.TrimSpace(failed.Summary)
	}

	if reason == "" {
		reason = "the optional evidence request failed"
	}

	tool := strings.TrimSpace(failed.Tool)
	if tool == "" {
		tool = "read/discovery tool"
	}

	return fmt.Sprintf("closed_negative_evidence: optional %s evidence for %s failed (%s); record this as an evidence gap in the non-source deliverable if the exact source fact remains unavailable", tool, target, textutil.Truncate(strings.TrimSpace(reason), 220))
}

func (r *Runner) scopedReadClassTool(req workspace.ToolRequest) bool {
	if workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return true
	}

	return r != nil && r.externalReadOnlyDiscoveryTool(req.Tool)
}

func (r *Runner) externalReadOnlyDiscoveryTool(tool string) bool {
	if r == nil || r.Workspace == nil {
		return false
	}

	return r.workspaceToolSurfaceSnapshot().ExternalToolIsReadOnlyDiscovery(tool)
}

func (r *Runner) scopedImplementationDiscoveryLookupAllowed(req workspace.ToolRequest, pathScope []string) bool {
	if !scopedImplementationDiscoveryPathAllowed(req, pathScope) {
		return false
	}

	if r != nil && r.externalReadOnlyDiscoveryTool(req.Tool) {
		return true
	}

	if scopedImplementationSymbolOwnerLookupAllowed(req) {
		return true
	}

	if !workspace.ImplementationScopedSemanticDiscoveryToolAllowed(req.Tool) {
		return false
	}

	if strings.TrimSpace(req.Tool) == workspace.ToolNameContextStatus {
		return true
	}

	target := strings.TrimSpace(textutil.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.Path, req.Command))
	if target == "" || len(target) < 3 {
		return false
	}

	profile := workspace.DiscoveryProfileForRequest(req)
	if profile.Scope == "broad" || profile.Cost == "high" {
		return false
	}

	return true
}

func (r *Runner) writeOnlyRecoveryToolIsWrite(tool string) bool {
	if workspace.IsWorkspaceWriteTool(tool) {
		return true
	}

	if r == nil || strings.TrimSpace(tool) != workspace.ToolNameSubmitPlan {
		return false
	}

	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func scopedImplementationDiscoveryPathAllowed(req workspace.ToolRequest, pathScope []string) bool {
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return true
	}

	normalized := NormalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	for _, scoped := range pathScope {
		scoped = NormalizeRunnerPath(scoped)
		if scoped == "" {
			continue
		}

		if normalized == scoped {
			return true
		}

		if !runnerPathLooksLikeFile(scoped) && strings.HasPrefix(normalized, scoped+"/") {
			return true
		}
	}

	return scopedImplementationPackageDiscoveryPathAllowed(req, normalized, pathScope)
}

func runnerPathLooksLikeFile(path string) bool {
	base := filepath.Base(strings.TrimSpace(filepath.ToSlash(path)))
	return strings.Contains(base, ".")
}

func scopedImplementationPackageDiscoveryPathAllowed(req workspace.ToolRequest, normalized string, pathScope []string) bool {
	normalized = NormalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(normalized)))
	if normalized == "" || normalized == "." || normalized == "/" || runnerPathLooksLikeFile(normalized) {
		return false
	}

	if !scopedImplementationDirectoryDiscoveryHasExactTarget(req) {
		return false
	}

	for _, scoped := range pathScope {
		scoped = NormalizeRunnerPath(filepath.ToSlash(strings.TrimSpace(scoped)))
		if scoped == "" || !runnerPathLooksLikeFile(scoped) {
			continue
		}

		parent := NormalizeRunnerPath(filepath.ToSlash(filepath.Dir(scoped)))
		if parent == "" || parent == "." || parent == "/" {
			continue
		}

		if normalized == parent {
			return true
		}
	}

	return false
}

func scopedImplementationDirectoryDiscoveryHasExactTarget(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(textutil.FirstNonEmpty(req.Pattern, req.Query, req.Task, req.ID, req.Handle, req.NamedGap))
	if target == "" || len(target) < 3 {
		return false
	}

	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}

	if strings.Contains(target, "$") {
		return false
	}

	return true
}

func scopedImplementationSymbolOwnerLookupAllowed(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability {
		capability := strings.ToLower(strings.TrimSpace(req.Capability))
		switch capability {
		case "find_symbol", "definition", "declaration", "workspace_symbol", "symbol", "symbol-lookup", "symbol_lookup":
		default:
			return false
		}

		return scopedImplementationLookupTargetAllowed(req)
	}

	_, suffix, ok := languagetools.ToolParts(tool)
	if !ok {
		return false
	}

	switch suffix {
	case "find_symbol", "find_function", "find_type", "find_const", "find_package",
		"definition", "declaration", "workspace_symbol", "read_symbol":
	default:
		return false
	}

	return scopedImplementationLookupTargetAllowed(req)
}

func scopedImplementationLookupTargetAllowed(req workspace.ToolRequest) bool {
	target := strings.TrimSpace(textutil.FirstNonEmpty(req.Pattern, req.Query, req.ID, req.Handle))
	if target == "" || len(target) < 3 {
		return false
	}

	if strings.ContainsAny(target, "*?[]{}|") {
		return false
	}

	return true
}

// writeOnlyRecoveryBlockerAnswer returns the <<QUESTION>> answer the runner
// emits when write-only recovery is active and the model tried a non-write
// tool that the bounded read allowance can no longer cover. Returns ok=false
// when the situation doesn't warrant a recovery blocker (e.g. read-only
// lane, no required edit paths remain).
func (r *Runner) writeOnlyRecoveryBlockerAnswer(results []workspace.ToolResult) (string, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and required edit path(s) remain unchanged: %s. Complete those edits from the current packet/evidence, or re-dispatch with a narrower anchor for each skipped path.", strings.Join(missing, ", ")), true
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return "", false
	}

	target := "the requested implementation target"
	if refs := nonEmptyRunnerStrings(r.RuntimeBrief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	return fmt.Sprintf("<<QUESTION>>: Write-only recovery is active and no edit has been attempted. Provide a narrower target packet or preloaded current-state anchor for %s; no workspace change was made.", target), true
}

func (r *Runner) writeOnlyRecoveryBlockerKind(results []workspace.ToolResult) string {
	if r != nil && len(r.remainingRequiredEditPaths(results)) > 0 {
		return "write_only_recovery_required_paths_remaining"
	}

	return "write_only_recovery_no_edit_attempted"
}

func (r *Runner) writeOnlyRecoveryNonWriteRepairPrompt(req workspace.ToolRequest, results []workspace.ToolResult) string {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested non-write tool"
	}

	lines := []string{
		fmt.Sprintf("%s was rejected because write-only recovery is active after repeated no-write completion attempts.", tool),
		"Do not read, search, navigate, inspect, summarize, or plan again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		lines = append(lines, "Required edit path(s) still unchanged: "+strings.Join(missing, ", ")+".")
	}

	lines = append(lines, "Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func (r *Runner) remainingRequiredEditPaths(results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	return missingRequiredEditPaths(r.requiredEditCoveragePaths(), newToolResultEvidenceProjection(results).ChangedPathSet())
}

func (r *Runner) requiredEditCoveragePaths() []string {
	if r == nil {
		return nil
	}

	required := r.RuntimeBrief.RequiredEditPaths
	if !r.planThenExecutePlanSubmissionPhase() {
		return required
	}

	root := ""
	if r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}

	return planThenExecuteRequiredCoveragePaths(required, root)
}

func contextPackUnavailableForRecovery(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return true
		}

		if result.Facts == nil || !result.Facts.HasDiscoveryTargets {
			return true
		}
	}

	return false
}

func (r *Runner) recoverContextPackFreshness(ctx context.Context, req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameContextPack || !contextPackResultNeedsFreshnessRecovery(result) {
		return result
	}

	r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_pack freshness blocked; refreshing context status before retry"})

	refresh := workspace.ToolRequest{
		Tool:              workspace.ToolNameContextStatus,
		Refresh:           true,
		Limit:             50,
		FreshnessMaxFiles: 2000,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	}
	if strings.TrimSpace(req.Language) != "" {
		refresh.Language = req.Language
	}

	refreshResult := r.Workspace.Execute(ctx, refresh)
	if summary := strings.TrimSpace(textutil.FirstNonEmpty(refreshResult.Summary, refreshResult.Error)); summary != "" {
		r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_status refresh result: " + textutil.Truncate(strings.TrimSpace(summary), 260)})
	}

	retry := r.Workspace.Execute(ctx, req)
	if strings.TrimSpace(retry.Error) == "" || strings.TrimSpace(refreshResult.Error) == "" {
		return retry
	}

	return result
}

func contextPackResultNeedsFreshnessRecovery(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "freshness_blocker" {
		return true
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))

	return strings.Contains(text, "freshness_blocker") ||
		strings.Contains(text, "stale_unbounded_graph") ||
		strings.Contains(text, "context freshness")
}

func contextPackSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
			return true
		}
	}

	return false
}

func concreteRecoveryEvidencePaths(results []workspace.ToolResult, max int) []string {
	filtered := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameContextPack, "completion_gate":
			continue
		default:
			filtered = append(filtered, result)
		}
	}

	return promptEvidencePathsFromToolResults(filtered, max)
}

func recoveryEvidencePathsForObjective(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	searchRanked := rankedSearchContentEvidencePaths(objective, results, max)
	if len(searchRanked) >= max {
		return searchRanked[:max]
	}

	seen := make(map[string]struct{}, len(searchRanked))
	paths := make([]string, 0, max)

	for _, path := range searchRanked {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range concreteRecoveryEvidencePaths(results, max*2) {
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		if len(paths) >= max {
			break
		}
	}

	return paths
}

func candidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := make(map[string]struct{}, max)
	paths := make([]string, 0, max)
	add := func(path string) {
		path = NormalizeRunnerPath(path)
		if path == "" {
			return
		}

		if !planThenExecuteRecoveryScopePathAllowed(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range brief.AllowedEditPaths {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range recoveryEvidencePathsForObjective(brief.Objective, results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range contextPackCandidatePathsFromResults(results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	return paths
}

func unreadCandidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := newToolResultEvidenceProjection(results).ReadPaths()
	for _, path := range candidates {
		if _, ok := read[NormalizeRunnerPath(path)]; ok {
			return nil
		}
	}

	unread := make([]string, 0, len(candidates))

	unread = append(unread, candidates...)

	return unread
}

func unreadCandidateRecoveryScopePathsForRecovery(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := newToolResultEvidenceProjection(results).ReadPaths()
	unread := make([]string, 0, len(candidates))

	for _, path := range candidates {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if candidateRecoveryScopePathHasMissingFileEvidence(normalized, results) {
			continue
		}

		if _, ok := read[normalized]; ok {
			continue
		}

		unread = append(unread, normalized)
	}

	return unread
}

func candidateRecoveryScopePathHasMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := NormalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	lowerPath := strings.ToLower(normalized)
	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.Contains(text, "file not found") ||
			strings.Contains(text, "no such file") ||
			strings.Contains(text, "cannot find the file") ||
			strings.Contains(text, "does not exist") {

			return true
		}
	}

	return false
}

func rankedSearchContentEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := recoveryObjectiveSignalTerms(objective)
	lowerObjective := strings.ToLower(objective)
	uiObjective := objectiveMentionsUI(lowerObjective)

	type scoredPath struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scoredPath{}
	order := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range strings.Split(result.Output, "\n") {
			path, text, ok := searchContentPathAndText(line)
			if !ok {
				continue
			}

			entry, exists := scores[path]
			if !exists {
				entry = &scoredPath{path: path, order: order}
				scores[path] = entry
				order++
			}

			entry.score += recoverySearchLineScore(path, text, terms, uiObjective)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	paths := make([]scoredPath, 0, len(scores))
	for _, entry := range scores {
		paths = append(paths, *entry)
	}

	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].score != paths[j].score {
			return paths[i].score > paths[j].score
		}

		return paths[i].order < paths[j].order
	})

	out := make([]string, 0, min(max, len(paths)))
	for _, entry := range paths {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func searchContentPathAndText(line string) (path string, text string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", "", false
	}

	before := strings.TrimSpace(line[:firstColon])
	fields := strings.Fields(before)

	if len(fields) == 0 {
		return "", "", false
	}

	path = fields[len(fields)-1]
	if !recoverySearchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")

	if secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return filepath.ToSlash(path), text, true
}

func recoverySearchLineScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "tool_schemas.go") || strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if strings.HasPrefix(lowerPath, "cmd/") && !ContainsAny(lowerText, "implementation", "core", "internal") {
		score -= 20
	}

	if slices.Contains(terms, "apply") && ContainsAny(lowerPath, "migration_apply", "_apply.", "/apply") {
		score += 60
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if ContainsAny(lowerPath, "render", "view", "model_state", "update_handlers") {
			score += 25
		}

		if ContainsAny(lowerPath, "frontier/", "comprehension/", "workspace/") {
			score -= 25
		}
	}

	return score
}

func recoveryObjectiveSignalTerms(objective string) []string {
	pattern := literalRecoverySearchPattern(objective)
	if pattern == "" {
		return nil
	}

	parts := strings.Split(pattern, "|")
	terms := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}

		terms = append(terms, strings.ReplaceAll(part, `\`, ""))
	}

	return terms
}

func objectiveMentionsUI(lowerObjective string) bool {
	return ContainsAny(lowerObjective, "tui", "ui", "banner", "header", "footer", "top-right", "top right", "window", "screen", "view", "render")
}

func (r *Runner) repeatedRangeLessReadFileRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return "", "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return "", "", false
	}

	if rangeLessReadFileWasRedirectedToOutline(path, results) && !newToolResultEvidenceProjection(results).HasSourceBody(path) {
		followup := r.repeatedRangeLessReadFileFollowup(path, results)

		return "Repeated range-less read_file redirected to bounded source request",
			fmt.Sprintf("A prior range-less `read_file` for `%s` was already redirected to outline evidence. Do not retry the same range-less read or dump the full file. Request bounded source-body evidence instead:\n\n```tool\n%s\n```", path, marshalToolRequestRaw(followup)),
			true
	}

	return "", "", false
}

func (r *Runner) mutationRejectedRetryWriteRecovery(decision runnerRequestBudgetDecision, req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, string, bool) {
	if r == nil || decision.ForceAnswerReason != "rejection_retry_blocked" {
		return workspace.ToolResult{}, "", false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return workspace.ToolResult{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Mutation write required after repeated rejected request",
		Error:   fmt.Sprintf("%s was already rejected and then repeated before any successful workspace write. The implementation contract is not satisfied by repeating rejected non-write requests.", tool),
	}
	prompt := result.Error + " Use the gathered source evidence to make the smallest parser-readable write now. If no write is valid, answer exactly `<<QUESTION>>:` with the concrete path-specific blocker or no-op evidence. Do not repeat the rejected non-write request."

	return result, prompt, true
}

func (r *Runner) mutationNoWriteLoopRecovery(req workspace.ToolRequest, results []workspace.ToolResult, identicalCallRun int) (workspace.ToolResult, string, bool) {
	if r == nil || identicalCallRun < 3 {
		return workspace.ToolResult{}, "", false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) || r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, "", false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) || len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return workspace.ToolResult{}, "", false
	}

	if !mutationNoWriteLoopEvidenceReady(r.RuntimeBrief, results) {
		return workspace.ToolResult{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	result := workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Mutation write required after repeated non-write request",
		Error:   fmt.Sprintf("%s was repeated %d times before any successful workspace write. The explicit implementation contract is not satisfied by repeating non-write requests.", tool, identicalCallRun),
	}
	prompt := result.Error + " Use the gathered source evidence to make the smallest parser-readable write now. If no write is valid, answer exactly `<<QUESTION>>:` with the concrete path-specific blocker or no-op evidence. Do not repeat the non-write request."

	return result, prompt, true
}

func mutationNoWriteLoopEvidenceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(brief.RequiredEditPaths) > 0 {
		return implementationRequiredPathAnchorsReady(brief, results)
	}

	for _, result := range results {
		if concreteSourceEvidenceResult(result) {
			return true
		}
	}

	return false
}

type planThenExecuteLoopBudgetRecovery struct {
	Prompt       string
	TerminalOnly bool
}

func (r *Runner) planThenExecuteLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}

	var unreadCandidatePaths []string

	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) == 0 {
		unreadCandidatePaths = unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 8)
	}

	noBodyMissRecovery := planThenExecuteNoBodyMissRecoveryApplies(results)
	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) &&
		!planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) &&
		(len(unreadCandidatePaths) == 0 || !noBodyMissRecovery) {

		return planThenExecuteLoopBudgetRecovery{}, false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated without advancing the planner contract", tool)
	}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Submit-correction mode is already ready; do not call more read, search, navigation, or verification tools.", tool, reason)
		outcome := "The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not report the loop-budget recovery itself as the blocker."
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
			outcome = "The next visible action must be a parser-readable `submit_plan`, unless no safe executable edit body can be produced from gathered evidence after repeated rejected submit_plan attempts; in that case answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, and do not report the loop-budget recovery itself as the blocker."
		}
		parts := []string{
			lead,
			outcome,
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
			"Fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
		}

		return planThenExecuteLoopBudgetRecovery{
			Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
			TerminalOnly: true,
		}, true
	}

	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) == 0 && noBodyMissRecovery {
		if unread := unreadCandidatePaths; len(unread) > 0 {
			read := sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths())
			lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that request.", tool, reason)
			parts := []string{
				lead,
				"Unscoped plan-execute candidate paths are not coverage obligations, but this planner still has unread candidate surfaces that may close a concrete edit-safety fact.",
				"Read candidate path(s) so far: " + textutil.FirstNonEmpty(strings.Join(read, ", "), "(none)"),
				"Unread candidate path(s): " + strings.Join(unread, ", "),
				"The next visible action must be exactly one parser-readable action: one bounded source-body read/search for a model-selected unread candidate path with a named edit-safety fact, a parser-readable `submit_plan` using the source evidence already gathered, `Decision: source-satisfied` with current-state evidence, or `<<QUESTION>>:` naming the exact blocker. If the prior symbol lookup missed before source-body evidence existed, a bounded read on that same candidate path is valid.",
				"Do not call the same tool with the same arguments again, do not run verification, and do not report the loop-budget recovery itself as the blocker.",
			}

			return planThenExecuteLoopBudgetRecovery{
				Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
				TerminalOnly: false,
			}, true
		}
	}

	lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. The planner already has source evidence; do not call more read, search, navigation, or verification tools.", tool, reason)
	parts := []string{
		lead,
		"The next visible action must be exactly one terminal outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing the current source path(s), or `<<QUESTION>>:` naming the exact blocker. Do not report the loop-budget recovery itself as the blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
		"If submitting a plan, fill the skeleton with real source text from the reads already in this turn. Do not copy placeholders, do not request another tool, and do not describe the plan in prose outside the parser-readable action.",
	}

	return planThenExecuteLoopBudgetRecovery{
		Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		TerminalOnly: true,
	}, true
}

func planThenExecuteNoBodyMissRecoveryApplies(results []workspace.ToolResult) bool {
	return len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 && planThenExecuteHasPriorReadClassFailure(results)
}

func (r *Runner) planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req workspace.ToolRequest, results []workspace.ToolResult, reason string, nativeSubmitPlan bool) (planThenExecuteLoopBudgetRecovery, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return planThenExecuteLoopBudgetRecovery{}, false
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) &&
		!planThenExecuteOpenCandidateSourceCanSubmit(r.RuntimeBrief, results) {

		return planThenExecuteLoopBudgetRecovery{}, false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = "the requested tool"
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("%s repeated after a loop-budget recovery", tool)
	}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. Submit-correction mode is already ready; repeating the same request is not progress.", tool, reason)
		outcome := "Read/search/navigation tools are disabled for this recovery turn. The next visible action must be exactly one parser-readable `submit_plan`. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied` and do not answer `<<QUESTION>>:`."
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
			outcome = "Read/search/navigation tools are disabled for this recovery turn. The next visible action must be a parser-readable `submit_plan`, unless no safe executable edit body can be produced from gathered evidence after repeated rejected submit_plan attempts; in that case answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`."
		}
		parts := []string{
			lead,
			outcome,
			planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
			"Fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
		}

		return planThenExecuteLoopBudgetRecovery{
			Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
			TerminalOnly: true,
		}, true
	}

	lead := fmt.Sprintf("The planner repeated %s after a loop-budget recovery: %s. The previous recovery already allowed either a different bounded read/search with a named edit-safety fact or a terminal planner outcome; repeating the same request is not progress.", tool, reason)
	parts := []string{
		lead,
		"Read/search/navigation tools are disabled for this recovery turn. The next visible action must be exactly one terminal planner outcome: a parser-readable `submit_plan`, `Decision: source-satisfied` citing current-state evidence, or `<<QUESTION>>:` naming the exact blocker.",
		planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(r.RuntimeBrief, results, nativeSubmitPlan),
		"If submitting a plan, fill the skeleton with real source text from the evidence already gathered. Do not request another tool, do not copy placeholders, and do not describe the plan in prose outside the parser-readable action.",
	}

	return planThenExecuteLoopBudgetRecovery{
		Prompt:       strings.Join(nonEmptyRunnerPromptParts(parts), "\n\n"),
		TerminalOnly: true,
	}, true
}

func (r *Runner) repeatedRangeLessReadFileRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || !rangeLessReadFileWasRedirectedToOutline(path, results) || newToolResultEvidenceProjection(results).HasSourceBody(path) {
		return workspace.ToolRequest{}, "", false
	}

	followup := r.repeatedRangeLessReadFileFollowup(path, results)
	if strings.TrimSpace(followup.Tool) == "" || normalizedToolRequestRaw(followup) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", false
	}

	if len(allowedTools) > 0 && !slices.Contains(allowedTools, followup.Tool) {
		return workspace.ToolRequest{}, "", false
	}

	return followup, marshalToolRequestRaw(followup), true
}

func (r *Runner) planThenExecuteCandidateRangeLessReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if len(planThenExecuteRequiredCoveragePaths(r.RuntimeBrief.RequiredEditPaths, "")) > 0 ||
		successfulSubmitPlanResultCount(results) > 0 ||
		len(planThenExecuteAnchorReadyPaths(r.RuntimeBrief, results)) == 0 {

		return workspace.ToolRequest{}, "", false
	}

	target := NormalizeRunnerPath(req.Path)
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, alreadyRead := newToolResultEvidenceProjection(results).ReadPaths()[target]; alreadyRead {
		return workspace.ToolRequest{}, "", false
	}

	candidateSet := make(map[string]struct{})
	for _, path := range candidateRecoveryScopePaths(r.RuntimeBrief, results, 8) {
		candidateSet[NormalizeRunnerPath(path)] = struct{}{}
	}

	if _, ok := candidateSet[target]; !ok {
		return workspace.ToolRequest{}, "", false
	}

	redirected := req

	redirected.Path = target
	redirected.StartLine = 1
	redirected.EndLine = 180

	if strings.TrimSpace(redirected.NamedGap) == "" {
		redirected.NamedGap = "bounded source window for unread candidate path"
	}

	if strings.TrimSpace(redirected.WhyNotAnswering) == "" {
		redirected.WhyNotAnswering = "need bounded source evidence before deciding whether this candidate path needs an edit"
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

func (r *Runner) planThenExecuteMalformedSymbolReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if !slices.Contains(allowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, ok := semanticlanguage.LanguageForFile(path); !ok {
		return workspace.ToolRequest{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	capability := strings.TrimSpace(req.Capability)

	if tool != workspace.ToolNameCodeNavigate && tool != workspace.ToolNameReadOnlyCapability &&
		!strings.HasSuffix(tool, "_read_symbol") && !strings.HasSuffix(tool, "_extract_body") {

		return workspace.ToolRequest{}, "", false
	}

	if capability != "" && capability != "read_symbol" && capability != "extract_body" &&
		!strings.HasSuffix(capability, "_read_symbol") && !strings.HasSuffix(capability, "_extract_body") {

		return workspace.ToolRequest{}, "", false
	}

	if strings.TrimSpace(req.Pattern) != "" {
		return workspace.ToolRequest{}, "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "bounded source window after malformed symbol read omitted a pattern",
		WhyNotAnswering: "need source-body evidence for the model-selected candidate path before submit_plan",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

func (r *Runner) planThenExecuteRepeatedSymbolReadFallbackRedirect(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 || len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if !slices.Contains(allowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || newToolResultEvidenceProjection(results).HasSourceBody(path) {
		return workspace.ToolRequest{}, "", false
	}

	if _, ok := semanticlanguage.LanguageForFile(path); !ok {
		return workspace.ToolRequest{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	capability := strings.TrimSpace(req.Capability)

	if tool != workspace.ToolNameCodeNavigate && tool != workspace.ToolNameReadOnlyCapability &&
		!strings.HasSuffix(tool, "_read_symbol") && !strings.HasSuffix(tool, "_extract_body") {

		return workspace.ToolRequest{}, "", false
	}

	if capability != "" && capability != "read_symbol" && capability != "extract_body" &&
		!strings.HasSuffix(capability, "_read_symbol") && !strings.HasSuffix(capability, "_extract_body") {

		return workspace.ToolRequest{}, "", false
	}

	if !planThenExecuteHasPriorReadClassFailure(results) {
		return workspace.ToolRequest{}, "", false
	}

	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "bounded source window after semantic symbol lookup failed",
		WhyNotAnswering: "need source-body evidence for the model-selected candidate path before submit_plan",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

func planThenExecuteHasPriorReadClassFailure(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "" {
			continue
		}

		if workerruntime.IsZeroHitSearchResult(result) {
			return true
		}

		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		lower := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if strings.Contains(lower, "zero-hit retry blocked") ||
			strings.Contains(lower, "no symbols matched") ||
			strings.Contains(lower, "no go symbols matched") ||
			strings.Contains(lower, "repeating it will not find new matches") {

			return true
		}
	}

	return false
}

func (r *Runner) planThenExecuteMissingTargetReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	// A missing target on an open-candidate read is a model contract failure, not
	// permission for the host to choose the path. Let the normal rejection/nudge
	// path ask for a concrete target instead.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) repeatedRangeLessReadFileFollowup(path string, results []workspace.ToolResult) workspace.ToolRequest {
	if r != nil {
		if req, ok := r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
			OutlineGap:       "source-body evidence for repeated range-less read_file",
			SymbolGap:        "source-body evidence for repeated range-less read_file",
			OutlineWhy:       "range-less read was redirected; need declarations before selecting source-body evidence",
			SymbolWhy:        "need current declaration body instead of another full-file read",
			RangeWhy:         "outline did not produce a readable symbol; need bounded source instead of another full-file read",
			FallbackRangeWhy: "need bounded source instead of another full-file read",
		}); ok {
			return req
		}
	}

	return workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "exact source fact still missing after prior full-file read",
		WhyNotAnswering: "need bounded source instead of repeating range-less read_file",
	}
}

func rangeLessReadFileWasRedirectedToOutline(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	lowerPath := strings.ToLower(path)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.TrimSpace(result.Tool) == workspace.ToolNameReadFile &&
			strings.Contains(text, "redirected") &&
			(strings.Contains(text, "file_outline") || strings.Contains(text, "outline")) {

			return true
		}

		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") {

			return true
		}
	}

	return false
}

func concreteSourceEvidenceResultMatchesPath(result workspace.ToolResult, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) {
		return false
	}

	for _, readPath := range ReadPathsFromToolResult(result) {
		if IsNonSourceEvidenceArtifactPath(readPath) {
			continue
		}

		if NormalizeRunnerPath(readPath) == path {
			return true
		}
	}

	return false
}

func fullSourceReadFileSeen(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if !readFileResultHasConcreteSourceBody(result) {
			continue
		}

		fact, ok := sourceBodyFactForPath(result, path)
		if !ok {
			continue
		}

		if sourceBodyFactCoversWholeFile(fact) {
			return true
		}

		if sourceBodyFactHasRange(fact) || fact.Truncated {
			continue
		}

		return true
	}

	return false
}

func implementationRequiredPathHiddenOnlyRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())) == 0 {
		return false
	}

	if !implementationRequiredPathAnchorsReady(brief, results) {
		return false
	}

	return strings.TrimSpace(reply.Content) == "" && strings.TrimSpace(reply.ReasoningContent) != ""
}

func mutationHiddenOnlyNoWriteTerminalRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return false
	}

	if !runnerBriefRequiresMutation(brief) {
		return false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 {
		return false
	}

	if implementationTargetWriteReadySurfaceActive(brief, results) {
		return false
	}

	if strings.TrimSpace(reply.Content) != "" || strings.TrimSpace(reply.ReasoningContent) == "" {
		return false
	}

	return concreteSourceEvidenceAlreadyRead(results)
}

func (r *Runner) unscopedNoWriteTerminalRecoveryReady(results []workspace.ToolResult) bool {
	if r == nil || !r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return false
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) > 0 || len(r.remainingRequiredEditPaths(results)) > 0 {
		return false
	}

	return concreteSourceEvidenceAlreadyRead(results) && mutationNoWriteCompletionGateRejectionCount(results) >= 2
}

func mutationNoWriteCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}

		if !strings.Contains(strings.ToLower(result.Summary), "mutation completion gate rejected final answer") {
			continue
		}

		if strings.Contains(strings.ToLower(result.Error), "no successful write tool result is present") {
			count++
		}
	}

	return count
}

func (r *Runner) unscopedNoWriteTerminalRecoveryPrompt(results []workspace.ToolResult) string {
	lines := []string{
		"The completion gate has now rejected multiple no-write final answers after source evidence was gathered.",
		"Keep using the active implementation strategy surface: parser-readable write when enough evidence exists, bounded read/search with `named_gap` for one exact missing edit-safety fact, or `<<QUESTION>>:` naming the exact blocker/no-op evidence.",
		"Do not repeat a prose final answer until a workspace write, no-op, or exact blocker has been recorded.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	lines = append(lines, "The model owns whether more evidence is needed before writing; the harness only requires the next visible action to satisfy the active parser-readable contract. Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func (r *Runner) implementationTargetWriteReadyTerminalRecoveryPrompt(results []workspace.ToolResult) string {
	lines := []string{
		"The final answer was rejected because the implementation lane is already target-write-ready and no workspace write has been recorded.",
		"Keep using the active target-write-ready strategy surface: parser-readable write when enough evidence exists, bounded read/search with `named_gap` for one exact missing edit-safety fact, or `<<QUESTION>>:` naming the exact blocker/no-op evidence.",
		"Do not repeat a prose final answer until a workspace write, no-op, or exact blocker has been recorded.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	lines = append(lines, "The model owns whether more evidence is needed before writing; the harness only requires the next visible action to satisfy the active parser-readable contract. Actions written only in hidden reasoning are ignored by the parser. Do not copy placeholders, schema examples, or prose as edit fields.")

	return strings.Join(lines, "\n\n")
}

func hiddenOnlyNoWriteTerminalRecoveryPrompt() string {
	return strings.Join([]string{
		"The previous retry produced hidden-only output after concrete source evidence was already read. Hidden thinking is disabled for this retry.",
		"Read/search/discovery tools are hidden for this turn. Do not inspect, search, plan, or summarize again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using the current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
		textualMutationWriteJSONInstruction(),
		"Choose the path and edit shape from the gathered evidence and active runtime contract. Do not copy placeholders, schema examples, or prose as edit fields.",
	}, "\n\n")
}

func reviewRepairCompletionGateRecoveryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response because it did not visibly close the review contract.",
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		lines = append(lines,
			"Phase Evidence still has failed obligations: "+strings.Join(failed, ", ")+". `Decision: pass` is invalid in this state.",
			"Your next visible response must be exactly one terminal review-repair outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
			"Corrective plans must be one parser-readable `submit_plan` object with a real `Verification Checklist:` rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact failed obligation.",
		)
	} else {
		lines = append(lines,
			"Your next visible response must be exactly one terminal review-repair outcome: `Decision: pass` with a `Verification Checklist`, a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact blocker.",
			"Pass responses start with `Decision: pass` followed by `Verification Checklist:` bullets. Corrective plans must be one parser-readable `submit_plan` object with a real checklist rationale and executable edits for allowed repair paths only. Blockers must start with `<<QUESTION>>:` and name the exact blocker.",
		)
	}

	lines = append(lines,
		"Do not add prose before or after the chosen terminal shape.",
		"Use the existing Phase Evidence Bundle, Diff Evidence, submitted_plan_state, source_sanity, verification entries, and inherited context packet as the evidence ledger. The closeout gate has fired, so do not read, search, refresh context, run commands, or ask for more inspection before choosing the terminal outcome.",
		"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract.",
	)

	return strings.Join(lines, "\n\n")
}

func reviewRepairSourceSanityRepairNeedsEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	failed := planThenExecuteReviewRepairFailedPhaseObligations(brief)
	hasSourceSanityFailure := false

	for _, obligation := range failed {
		obligation = strings.TrimSpace(obligation)
		if strings.HasPrefix(obligation, "source_sanity_failed:") || strings.HasPrefix(obligation, "source_sanity_error:") {
			hasSourceSanityFailure = true
			break
		}
	}

	if !hasSourceSanityFailure {
		return false
	}

	return reviewRepairCompletionGateRejectionCount(results) < 2
}

func reviewRepairCompletionGateRejectionCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != "completion_gate" {
			continue
		}

		if strings.HasPrefix(strings.TrimSpace(result.Summary), "Review-repair completion gate rejected") {
			count++
		}
	}

	return count
}

func reviewRepairSourceSanityRepairEvidencePrompt(brief workerruntime.WorkerRuntimeBrief) string {
	lines := []string{
		"The completion gate rejected the review-repair response, and Phase Evidence still has failed source_sanity obligations: " + strings.Join(planThenExecuteReviewRepairFailedPhaseObligations(brief), ", ") + ".",
		"Before choosing the terminal review-repair outcome, you may take one bounded source-read action only if it is needed to produce an executable corrective plan for the failed source_sanity diagnostics.",
		"Prefer the changed/allowed repair path first. If the compiler error names a referenced local type or symbol outside the repair path, read only the bounded source body needed to match that existing type shape.",
		"Do not run commands, do not broaden discovery, and do not inspect for confidence. After the bounded evidence returns, close with exactly one terminal outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
		"`Decision: pass` is invalid while source_sanity remains failed.",
	}

	return strings.Join(lines, "\n\n")
}

func reviewRepairTerminalOutcomeRequiredPrompt(brief workerruntime.WorkerRuntimeBrief, tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "that tool"
	}

	lines := []string{
		fmt.Sprintf("%s is not available after the review-repair closeout gate has fired.", tool),
	}
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		lines = append(lines,
			"Phase Evidence still has failed obligations: "+strings.Join(failed, ", ")+". `Decision: pass` is invalid in this state.",
			"Your next visible response must be exactly one terminal review-repair outcome: a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact failed obligation blocker.",
		)
	} else {
		lines = append(lines, "Your next visible response must be exactly one terminal review-repair outcome: `Decision: pass` with a `Verification Checklist`, a corrective `submit_plan` whose rationale starts with `Verification Checklist:`, or `<<QUESTION>>:` naming the exact blocker.")
	}

	lines = append(lines, "Do not read, search, refresh context, run commands, or add prose before or after the chosen terminal shape.")

	return strings.Join(lines, "\n\n")
}

func concreteSourceEvidenceAlreadyRead(results []workspace.ToolResult) bool {
	for _, result := range results {
		if concreteSourceEvidenceResult(result) {
			return true
		}
	}

	return false
}

func implementationTargetWriteReadySourcedPathSummary(results []workspace.ToolResult, max int) string {
	paths := newToolResultEvidenceProjection(results).SourceBodyPaths()
	if len(paths) == 0 {
		return ""
	}

	paths = uniqueSortedRunnerPaths(paths)
	if max <= 0 || max > len(paths) {
		max = len(paths)
	}

	parts := append([]string{}, paths[:max]...)
	if len(paths) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(paths)-max))
	}

	return strings.Join(parts, ", ")
}

func (r *Runner) markConcreteSourceEvidenceObserved(result workspace.ToolResult) {
	if r == nil || r.Workspace == nil || !concreteSourceEvidenceResult(result) {
		return
	}

	for _, path := range ReadPathsFromToolResult(result) {
		if IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		if !sourceEvidenceHasBody(planThenExecuteToolResultSourceEvidence(result, path)) {
			continue
		}

		r.Workspace.MarkPathObserved(path)
	}
}

func contextZoomResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	text := strings.Join([]string{result.Summary, result.Output}, "\n")
	if strings.TrimSpace(text) == "" {
		return false
	}

	hasSourcePath := false

	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		if IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		hasSourcePath = true

		break
	}

	if !hasSourcePath {
		return false
	}

	if strings.Contains(result.Output, "route_summary:") && RoutedSourceReadOutputHasBody(result.Output) {
		return true
	}

	return contextZoomOutputHasConcreteSourceBody(result.Output)
}

func contextZoomOutputHasConcreteSourceBody(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	var decoded any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		return false
	}

	return contextZoomJSONHasConcreteSourceBody(decoded)
}

func contextZoomJSONHasConcreteSourceBody(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		if list, ok := value.([]any); ok {
			for _, item := range list {
				if contextZoomJSONHasConcreteSourceBody(item) {
					return true
				}
			}
		}

		return false
	}

	if contextZoomJSONHasSourcePath(object) {
		if source, ok := object["source"].(string); ok && strings.TrimSpace(source) != "" {
			return true
		}
		if sourceBody, ok := object["source_body"].(string); ok && strings.TrimSpace(sourceBody) != "" {
			return true
		}
	}

	for _, nested := range object {
		if contextZoomJSONHasConcreteSourceBody(nested) {
			return true
		}
	}

	return false
}

func contextZoomJSONHasSourcePath(object map[string]any) bool {
	path, ok := object["path"].(string)
	if !ok {
		return false
	}

	path = NormalizeRunnerPath(path)

	return path != "" && !IsNonSourceEvidenceArtifactPath(path)
}

func readFileResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	for _, fact := range sourceBodyFactsFromResult(result) {
		path := NormalizeRunnerPath(fact.Path)
		if path != "" && !IsNonSourceEvidenceArtifactPath(path) {
			return true
		}
	}

	return false
}

func toolResultHasNonArtifactReadPath(result workspace.ToolResult) bool {
	for _, path := range ReadPathsFromToolResult(result) {
		if IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		return true
	}

	return false
}

func (r *Runner) mutationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return workspace.ToolResult{}, false
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}

	// Plan-then-execute planner phase: the planner's deliverable is a
	// successful submit_plan call, not file mutations. The executor
	// phase (run by the orchestrator AFTER this runner exits) is what
	// actually changes files. If submit_plan succeeded, the planner is
	// done — short-circuit the rest of the gate so we don't reject the
	// answer for "no successful write tool result".
	if r.planThenExecutePlannerPhase() && successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolResult{}, false
	}

	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if r.planThenExecutePlannerPhase() && len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {
		if r.planThenExecuteSourceSatisfiedAnswerCovers(reply, results) {
			return workspace.ToolResult{}, false
		}

		if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
			if requiredPathBlockerAnswerCovers(reply, missing) {
				return workspace.ToolResult{}, false
			}
		} else if planThenExecuteAnswerDeclaresTypedBlocker(reply) {
			return workspace.ToolResult{}, false
		}

		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Plan-execute completion gate rejected final answer",
			Error:   planThenExecuteMissingSubmitPlanError(r.RuntimeBrief.RequiredEditPaths),
		}, true
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {
		if unsupported := unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected unsupported change claim",
				Error:   "final answer reports changed paths not backed by successful write tool results: " + quotedRunnerStrings(unsupported) + ". Satisfy the missing write contract, or correct the final answer to a concrete blocker/no-op reason only.",
			}, true
		}

		if mutationReplyClaimsCompletedWorkspaceChange(reply) {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected unsupported change claim",
				Error:   "final answer claims the workspace change was completed, but no successful write tool result is present. Satisfy the missing write contract, or answer only with the concrete blocker/no-op evidence.",
			}, true
		}

		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet()); len(missing) > 0 {
			if requiredPathBlockerAnswerCovers(reply, missing) {
				return workspace.ToolResult{}, false
			}
		} else if mutationReplyReportsConcreteNoopOrBlocker(lowerReply) {
			return workspace.ToolResult{}, false
		}

		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "this task asks for a workspace change, but no successful write tool result is present. The contract outcomes are the smallest necessary edit, or a concrete blocker/no-op reason.",
		}, true
	}

	if unsupported := unsupportedClaimedChangedPaths(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "final answer reports changed paths not backed by successful write tool results: " + quotedRunnerStrings(unsupported) + ". Satisfy the missing write contract, or correct the changed-paths summary to only include recorded edits.",
		}, true
	}

	if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet()); len(missing) > 0 {
		if !requiredPathBlockerAnswerCovers(reply, missing) {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Mutation completion gate rejected final answer",
				Error:   "required edit path(s) were not changed: " + quotedRunnerStrings(missing) + ". Satisfy the required-path write contract, or state a concrete blocker/no-op reason for the skipped path(s).",
			}, true
		}
	}

	if removed := r.unreportedRemovedTestDeclarations(reply, results); len(removed) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected silent test removal",
			Error:   "git diff shows existing test declaration(s) removed without the final answer reporting them: " + quotedRunnerStrings(removed) + ". If the removal is intentional, report each removed test with verification status; otherwise restore the deleted test or fix the edit boundary.",
		}, true
	}

	if missing := r.missingRequiredEditSnippets(results); len(missing) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   "required edit snippet(s) are not present after the recorded edits: " + quotedRunnerStrings(missing) + ". Make the missing edit(s), or state a concrete blocker for each skipped required edit.",
		}, true
	}

	if workerruntime.VerificationContractSkipsGate(r.RuntimeBrief.VerificationContract) {
		return workspace.ToolResult{}, false
	}

	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) {
		errorText := "workspace changes are present, but the latest successful write is not followed by successful verification. Required evidence is a relevant language/project test/build/check result, compiler-backed diagnostics, or the concrete verification blocker."
		if plannerOwnsVerification(r.RuntimeBrief) {
			errorText = "workspace changes are present, but the latest successful write is not followed by lane sanity. This route delegates build/test verification to the planner; source edits require compiler-backed source-sanity evidence, and non-source edits may use git diff --check only as patch-hygiene evidence or state the concrete planner-owned verification blocker."
		}

		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   errorText,
		}, true
	}

	if sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {
		errorText := "source edits are present, but the available verification is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. Required evidence is a focused language-appropriate build/test/check command, compiler-backed diagnostics/source-sanity, or the concrete verification blocker; patch hygiene alone cannot certify source edits."
		if plannerOwnsVerification(r.RuntimeBrief) {
			errorText = "source edits are present, but the available lane sanity is only patch hygiene (`git diff --check`), generic source-sanity fallback, or no compiler-backed source-aware check. planner-owned verification lanes require compiler-backed source-sanity or language diagnostics for the changed source path. If source sanity is unavailable, answer with changed paths plus the concrete lane-sanity blocker. Do not claim patch hygiene certifies source edits."
		}

		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Mutation completion gate rejected final answer",
			Error:   errorText,
		}, true
	}

	if mutationReplyReportsConcreteNoopOrBlocker(lowerReply) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{}, false
}

func mutationReplyReportsConcreteNoopOrBlocker(lowerReply string) bool {
	lowerReply = strings.ToLower(strings.TrimSpace(lowerReply))
	if lowerReply == "" {
		return false
	}

	if ContainsAny(lowerReply,
		"<<question>>",
		"blocked",
		"blocker",
		"cannot",
		"can't",
		"could not",
		"unable",
		"permission",
		"approval",
		"no-op",
		"no op",
		"no change needed",
		"no changes needed",
		"nothing to change",
	) {

		return true
	}

	return ContainsAny(lowerReply, "already present", "already implemented", "already satisfied", "already up to date") &&
		ContainsAny(lowerReply, "no change", "no-op", "no op", "nothing to change")
}

func mutationReplyClaimsCompletedWorkspaceChange(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	if ContainsAny(lower, "already present", "already implemented", "already satisfied", "already up to date") &&
		ContainsAny(lower, "no change", "no-op", "no op", "nothing to change") {

		return false
	}

	if len(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(trimmed))) == 0 {
		for _, fragment := range pathparse.QuotedFragments(trimmed) {
			if _, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				return ContainsAny(lower,
					"has been edited",
					"have edited",
					"has been updated",
					"have updated",
					"updated so",
					"changed so",
					"implemented",
					"applied the edit",
					"made the edit",
					"source read confirms the edit",
				)
			}
		}

		return false
	}

	return ContainsAny(lower,
		"has been edited",
		"have edited",
		"has been updated",
		"have updated",
		"updated so",
		"changed so",
		"implemented",
		"applied the edit",
		"made the edit",
		"source read confirms the edit",
	)
}

const sourceGroundingAdditionalReadRecoveryRetries = 2

func (r *Runner) escalateCompletionGateResultMessage(result workspace.ToolResult, priorRejections int, results []workspace.ToolResult, allowedTools []string) string {
	_, sourceGrounding := sourceGroundingAssessmentFromResult(result)
	sourceReadRecoveryAvailable := sourceGrounding && r.sourceGroundingReadRecoveryEligible(allowedTools)
	retryLimit := r.completionGateRetryLimit(results)
	if sourceReadRecoveryAvailable {
		retryLimit = r.sourceGroundingCompletionGateRetryLimit(results)
	}
	return escalateCompletionGateMessageWithSourceRecovery(
		result.Error,
		priorRejections,
		sourceGrounding,
		sourceReadRecoveryAvailable,
		retryLimit,
	)
}

func escalateCompletionGateMessageWithSourceRecovery(base string, priorRejections int, sourceGrounding bool, sourceReadRecoveryAvailable bool, retryLimit int) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return base
	}

	switch {
	case priorRejections <= 0:
		return base
	case priorRejections == 1:
		if sourceGrounding {
			if !sourceReadRecoveryAvailable {
				return "[gate retry 2/3] " + trimmed +
					" — the gate already rejected one prior attempt with this error;" +
					" your next move MUST resolve the stated blocker (qualify unsupported" +
					" source citations as 'unverified' next to the claim, or state the" +
					" exact reason source-body evidence cannot be produced)."
			}
			if retryLimit <= 0 {
				retryLimit = 5
			}
			return fmt.Sprintf("[source evidence recovery 2/%d] ", retryLimit) + trimmed +
				" — the gate already rejected one prior attempt with this error;" +
				" your next move MUST resolve the stated blocker (call one bounded" +
				" `read_file` or `code_navigate` for each cited unread path, or" +
				" qualify each as 'unverified' next to the citation)."
		}
		return "[gate retry 2/3] " + trimmed +
			" — the gate already rejected one prior attempt with this error;" +
			" your next move MUST resolve the stated blocker (use a different tool" +
			" call or state a concrete reason this cannot proceed)."
	default:
		if sourceGrounding {
			if !sourceReadRecoveryAvailable {
				return "[gate retry 3/3 — strategy correction required] " + trimmed +
					" — repeated source-grounding rejections mean the active reply still cites" +
					" evidence the harness has not seen as source-body support. Qualify the" +
					" unsupported citation as 'unverified' next to the claim, or answer with" +
					" the exact blocker. Do not narrate; act."
			}
			attempt := priorRejections + 1
			if retryLimit <= 0 {
				retryLimit = 5
			}
			if attempt > retryLimit {
				attempt = retryLimit
			}
			return fmt.Sprintf("[source evidence recovery %d/%d] ", attempt, retryLimit) + trimmed +
				" — repeated source-grounding rejections mean the active reply still cites" +
				" evidence the harness has not seen as source-body support. Make one bounded" +
				" `read_file` or `code_navigate` call for a cited unread path, or qualify" +
				" that citation as 'unverified' next to the claim. Do not narrate; act."
		}
		return "[gate retry 3/3 — strategy correction required] " + trimmed +
			" — repeated rejections indicate the active reply does not satisfy" +
			" the contract. Either issue ONE concrete tool call that produces the" +
			" missing evidence, or write a final answer that explicitly names the" +
			" blocker. Do not narrate; act."
	}
}

func planThenExecuteMissingSubmitPlanError(requiredEditPaths []string) string {
	if len(requiredEditPaths) > 0 {
		return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names every required path and cites current-state evidence. Call `submit_plan`: " + planThenExecuteSubmitPlanCoverageSentence() + " Or answer `Decision: source-satisfied`, or answer with <<QUESTION>> naming the exact path-specific blocker."
	}

	return "plan-execute planner phase requires a successful submit_plan tool result before final answer, or a source-satisfied answer that names the relevant path(s) and cites current-state evidence. Call `submit_plan` for the needed candidate/allowed edit path(s), answer `Decision: source-satisfied`, or answer with `<<QUESTION>>:` naming the exact blocker."
}

func (r *Runner) completionGateRetryLimit(results []workspace.ToolResult) int {
	if r != nil && r.unscopedNoWriteCompletionRecoveryApplies(results) {
		return 2
	}

	if r != nil && planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return 2
	}

	if r != nil && r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		return 1
	}

	if r != nil && r.textualRequiredPathCompletionRecoveryApplies(results) {
		return 2
	}

	if r != nil && r.completionGateMutationRecoveryApplies(results) {
		if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
			sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {

			return 1
		}

		return 1
	}

	if r != nil && workerruntime.PermissionProfileIsReadOnlyWorkspace(r.RuntimeBrief.PermissionProfile) &&
		!runnerBriefRequiresMutation(r.RuntimeBrief) {
		return 3 // read-only lanes need more retries: the model must discover + read source evidence, and source-grounding retries consume attempts
	}

	return 2 // default retry limit; coupled to the "2/3" and "3/3" labels in escalateCompletionGateMessage
}

func (r *Runner) sourceGroundingCompletionGateRetryLimit(results []workspace.ToolResult) int {
	return r.completionGateRetryLimit(results) + sourceGroundingAdditionalReadRecoveryRetries
}

func (r *Runner) completionGateRetryLimitForResult(result workspace.ToolResult, results []workspace.ToolResult, allowedTools []string) int {
	if r.sourceGroundingReadRecoveryAvailable(result, allowedTools) {
		return r.sourceGroundingCompletionGateRetryLimit(results)
	}

	return r.completionGateRetryLimit(results)
}

func (r *Runner) sourceGroundingReadRecoveryAvailable(result workspace.ToolResult, allowedTools []string) bool {
	if r == nil {
		return false
	}
	if _, ok := sourceGroundingAssessmentFromResult(result); !ok {
		return false
	}

	return r.sourceGroundingReadRecoveryEligible(allowedTools)
}

func (r *Runner) sourceGroundingReadRecoveryEligible(allowedTools []string) bool {
	if r == nil {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {

		return false
	}

	tools := textutil.Unique(append(append([]string(nil), brief.AllowedTools...), allowedTools...))

	return sourceGroundingReadRecoveryToolsAvailable(tools)
}

func sourceGroundingReadRecoveryToolsAvailable(tools []string) bool {
	return toolAllowed(tools, workspace.ToolNameReadFile) ||
		toolAllowed(tools, workspace.ToolNameCodeNavigate)
}

func (r *Runner) retryableFinalAnswerAcceptanceRejection(acceptance finalAnswerAcceptance, results []workspace.ToolResult, priorGateRejections int, allowedTools []string) bool {
	if r == nil {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	violationKind, hasViolation := finalAnswerAcceptanceViolationKind(acceptance)

	retryLimit := r.completionGateRetryLimit(results)
	if hasViolation &&
		violationKind == turnViolationSourceGroundingUnmet &&
		r.sourceGroundingReadRecoveryEligible(allowedTools) {

		retryLimit = r.sourceGroundingCompletionGateRetryLimit(results)
	}

	if priorGateRejections >= retryLimit {
		return false
	}

	if hasViolation &&
		violationKind == turnViolationRequiredEvidenceUnmet &&
		runnerBriefRequiresMutation(brief) &&
		implementationTargetWriteReadySurfaceActive(brief, results) &&
		len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {

		return true
	}

	if r.planThenExecutePlannerPhase() && planThenExecuteAnchorRefreshFinalAnswerRejection(acceptance) {
		return r.planThenExecuteAnchorRefreshFinalAnswerPending(results)
	}

	retryPolicy := turnFinalAnswerRejectionRetryPolicyForBrief(brief, acceptance)
	if !retryPolicy.Retryable {
		return false
	}

	if !retryPolicy.RequiresReadOnlyEvidence {
		return true
	}

	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {

		return false
	}

	tools := textutil.Unique(append(append([]string(nil), brief.AllowedTools...), allowedTools...))

	return len(genericReadOnlyEvidenceTools(tools)) > 0
}

func (r *Runner) textualRequiredPathCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	return r != nil &&
		!r.supportsNativeTools() &&
		runnerBriefRequiresMutation(r.RuntimeBrief) &&
		len(r.remainingRequiredEditPaths(results)) > 0
}

func (r *Runner) unscopedNoWriteCompletionRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}

	return len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0
}

func (r *Runner) completionGateMutationRecoveryApplies(results []workspace.ToolResult) bool {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}

	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return true
	}

	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return true
	}

	if len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return true
	}

	return hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results)
}

func (r *Runner) completionGateRepeatedViolation(results []workspace.ToolResult) turnViolation {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRepeatedRejection)
	}

	if r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) != "" {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredSourceReadsRemainingRepeated)
		}

		if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateSubmitPlanInvalidRepeated)
		}

		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateSubmitPlanMissingRepeated)
	}

	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredPathsRemainingRepeated)
	}

	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRequiredSnippetsMissingRepeated)
	}

	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {

		if plannerOwnsVerification(r.RuntimeBrief) {
			return turnCompletionGateRepeatedViolation(turnViolationCompletionGateLaneSanityRepeated)
		}

		return turnCompletionGateRepeatedViolation(turnViolationCompletionGateVerificationRepeated)
	}

	return turnCompletionGateRepeatedViolation(turnViolationCompletionGateRepeatedRejection)
}

func (r *Runner) completionGateRecoveryPrompt(results []workspace.ToolResult) (prompt string, hideReads bool, ok bool) {
	if r == nil {
		return "", false, false
	}

	if prompt := r.readOnlyEvidenceCompletionRecoveryPrompt(results); prompt != "" {
		return prompt, false, true
	}

	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		if reviewRepairSourceSanityRepairNeedsEvidence(r.RuntimeBrief, results) {
			return reviewRepairSourceSanityRepairEvidencePrompt(r.RuntimeBrief), false, true
		}

		return reviewRepairCompletionGateRecoveryPrompt(r.RuntimeBrief), true, true
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "", false, false
	}

	if missing := r.remainingRequiredEditPaths(results); len(missing) > 0 {
		if r.planThenExecutePlannerPhase() {
			state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
			if state.Mode == PlanExecutePlanningModeAnchorRefresh && len(state.RefreshPaths) > 0 {
				refreshPaths := state.RefreshPaths
				target := "the rejected required path"

				if len(refreshPaths) > 0 {
					target = strings.Join(refreshPaths, ", ")
				}

				return strings.Join([]string{
					"Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=anchor_refresh callable_tools=read_file, code_navigate safe_alternatives=bounded_source_read.",
					"The completion gate rejected a final answer after `submit_plan` was rejected for stale or missing edit anchors.",
					"This is an anchor-refresh phase. Read/search/discovery is allowed only for one bounded source-body refresh on: " + target + ".",
					"Your next visible message must be exactly one parser-readable bounded `read_file` or `code_navigate` source-body request for one of those paths. Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or reply in prose until the bounded refresh returns.",
				}, "\n\n"), false, true
			}

			if nudge := planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results); nudge != "" {
				return strings.Join([]string{
					"The completion gate rejected a final answer before every required_edit_path produced source-body evidence.",
					"Do not draft `submit_plan` from outline/search hits. The next visible message must be exactly one parser-readable source-body tool call for the unread required path, or <<QUESTION>> with the exact path-specific blocker.",
					nudge,
				}, "\n\n"), false, true
			}

			if pending := planThenExecuteHistoryPendingPaths(r.RuntimeBrief, results, nil); len(pending) > 0 {
				return strings.Join([]string{
					"The completion gate rejected a final answer before required git-history evidence was produced for every required_edit_path.",
					"Source-body evidence is complete, but the planner is still in history-pending state. Do not call `submit_plan` yet and do not perform additional source reads.",
					"Your next visible message must be exactly one parser-readable `history_range` or `history_symbol` request for one pending path, or `<<QUESTION>>:` with the exact path-specific blocker.",
					"Pending git-history path(s): " + strings.Join(pending, ", "),
				}, "\n\n"), false, true
			}

			correction := planThenExecuteSubmitPlanVisibleJSONInstruction()

			if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
				correctionBrief := lastSubmitPlanRejectionBrief(rejected)
				if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
					correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
				}

				correction = correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.RuntimeBrief, rejected)
			}

			if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
				outcome := "Your next visible message must be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, and do not reply in prose."
				if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
					outcome = "Your next visible message must be a parser-readable `submit_plan` request, unless no safe executable edit body can be produced from gathered evidence after repeated rejected submit_plan attempts; in that case answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not answer `Decision: source-satisfied`, and do not reply in prose."
				}
				return fmt.Sprintf("Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=submit_ready hide_reads=true terminal_outcomes=submit_plan, Decision: source-satisfied, <<QUESTION>>:.\n\nThe completion gate rejected a final answer before the corrected plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits.", strings.Join(missing, ", "), outcome, correction), true, true
			}

			return fmt.Sprintf("Recovery decision: workflow_id=recovery state=inspecting terminal_state=recovery-open dispatchable=false reason=submit_ready hide_reads=true terminal_outcomes=submit_plan, Decision: source-satisfied, <<QUESTION>>:.\n\nThe completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. Your next visible message must be exactly one parser-readable action, not the same prose. Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. If the gathered evidence supports the requested changes, your next response must be a parser-readable `submit_plan` request, not prose. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. If a required missing source/test path has only behavior/helper requirements and no substantial inline body or explicit replacement shape, answer with <<QUESTION>> naming the missing body evidence. The other valid outcome is `Decision: source-satisfied` naming every path plus current-state evidence if the requested final state is already present. Do not call read_file, search_content, context_pack, context_query, code_navigate, or any other discovery tool.", strings.Join(missing, ", "), planThenExecuteSubmitPlanCoverageSentence(), correction), true, true
		}

		if briefIsScratchpadDeliverableLane(r.RuntimeBrief) {
			return fmt.Sprintf("The completion gate rejected a final answer while non-source deliverable path(s) remain unwritten: %s. The deliverable-write contract requires full corrected content with gathered evidence plus any missing, stale, ambiguous, or out-of-scope source fact recorded as `Source verification pending:` or `## Scope Correction Required`. Answer with <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty.", strings.Join(missing, ", ")), false, true
		}

		if exactAnchorPreWriteSurfaceActive(r.RuntimeBrief, results) {
			return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, true), true, true
		}

		return r.implementationRequiredPathCompletionRecoveryPrompt(missing, results, false), false, true
	}

	if missing := r.missingRequiredEditSnippets(results); len(missing) > 0 {
		return fmt.Sprintf("The completion gate rejected a final answer while required edit snippet(s) are still missing: %s. Read/search tools are hidden for this turn. The available contract outcomes are snippet-backed write capability or <<QUESTION>> naming the exact blocker for each missing snippet; do not perform additional discovery.", strings.Join(missing, ", ")), true, true
	}

	if r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) == 0 && successfulSubmitPlanResultCount(results) == 0 {
		gateRejected := completionGateResultSeen(results)
		gateRejections := completionGateResultCount(results)
		readPathSet := newToolResultEvidenceProjection(results).ReadPaths()
		readPaths := make([]string, 0, len(readPathSet))

		for path := range readPathSet {
			readPaths = append(readPaths, path)
		}

		sort.Strings(readPaths)

		candidatePaths := candidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
		for _, path := range readPaths {
			if !slices.Contains(candidatePaths, path) {
				candidatePaths = append([]string{path}, candidatePaths...)
			}
		}

		targetSummary := "No concrete candidate path has been read yet."
		nextAction := "If one edit-safety fact is missing, call one bounded read/search on the highest-confidence candidate path with a named missing edit-safety fact."
		hideReads := false

		if len(candidatePaths) > 0 {
			targetSummary = "Current candidate path evidence: " + strings.Join(candidatePaths, ", ")
			nextAction = fmt.Sprintf("If one edit-safety fact is missing, read the highest-confidence candidate path now: `read_file` on `%s` with a named missing edit-safety fact. Otherwise call `submit_plan`.", candidatePaths[0])
		}

		if len(readPaths) > 0 {
			targetSummary = "Read candidate path(s): " + strings.Join(readPaths, ", ")

			if gateRejected && gateRejections >= 2 {
				nextAction = "Read/search tools are hidden for this recovery turn because the planner already attempted to close after source evidence. Allowed/candidate paths are not coverage obligations. The next visible action must be one terminal planner outcome: call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
				hideReads = true
			} else if gateRejected {
				if unread := unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 8); len(unread) > 0 {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If the rejected answer was premature because one concrete edit-safety fact is still missing on another source/test surface, make exactly one bounded source read for a different unread candidate path with a named edit-safety fact: " + strings.Join(unread, ", ") + "."
				} else {
					nextAction = "Allowed/candidate paths are not coverage obligations. If the read source is enough, call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. If one concrete edit-safety fact is still missing, make exactly one bounded read/search request naming that missing fact instead of repeating the final-answer prose."
				}
			} else if unread := unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 4); len(unread) > 0 {
				nextAction = "Allowed/candidate paths are not coverage obligations. Call `submit_plan` using exact source anchors from the read output when enough evidence exists. If source-satisfied closeout depends on another candidate path, read one different unread candidate path with a bounded `read_file` range now: " + strings.Join(unread, ", ") + ". Do not reread a candidate path that already has source evidence."
			} else {
				nextAction = "Read/search tools are hidden for this recovery turn because the surfaced candidate source has already been read. Allowed/candidate paths are not coverage obligations. Call `submit_plan` using exact source anchors from the read output, answer `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker. Do not request another read."
				hideReads = true
			}
		}

		return strings.Join([]string{
			"The completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced an artifact for candidate/allowed scope. No required_edit_paths list is active, so choose the needed candidate path(s) from the objective and target refs instead of treating the allowlist as coverage.",
			targetSummary,
			"Your next visible message must be exactly one parser-readable action, not the same prose. Do not include analysis before or after the action.",
			"Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. The `submit_plan`, `Decision: source-satisfied`, `<<QUESTION>>:` outcome must appear in visible content or a native tool call.",
			"For textual tool transport, any bounded read/search/navigation action is one visible JSON object with a top-level `tool` field plus that tool's arguments, for example `" + workspace.CodeNavigateRequestExample(semanticlanguage.CapReadSymbol, "path/from/evidence.go", "SymbolFromEvidence") + "`. XML-like tags and key-value notes are not accepted tool calls unless they contain a parser-readable JSON object.",
			"For textual tool transport, a `submit_plan` action is one visible JSON object with top-level `tool`, `rationale`, and `edits` keys; `edits` must be a JSON array, not a quoted string.",
			"When submitting, each `submit_plan.edits[]` entry must use a real candidate path plus exactly one executable shape: exact `old_text`/`new_text`, replacement `start_line`/`end_line`/`content`, small inline insertion-only `insert_after_line`/`content`, or missing/new-file-only `replace_all:true`/`new_text`; include a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. For large additions, use line-range replacement of unchanged anchor line(s) plus inserted content. Edit bodies must be complete executable source text; do not copy schema examples, placeholders, ellipses, or prose as field values.",
			nextAction,
			"The only non-tool closeouts are a visible answer starting exactly with `Decision: source-satisfied` plus current-state evidence for the relevant path(s), or a visible `<<QUESTION>>:` naming the exact blocker. Do not call per-edit write tools.",
		}, "\n\n"), hideReads, true
	}

	if r.unscopedNoWriteCompletionRecoveryApplies(results) {
		if implementationTargetWriteReadySurfaceActive(r.RuntimeBrief, results) && len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 {
			return r.implementationTargetWriteReadyTerminalRecoveryPrompt(results), false, true
		}

		if r.unscopedNoWriteTerminalRecoveryReady(results) {
			return r.unscopedNoWriteTerminalRecoveryPrompt(results), false, true
		}

		candidatePaths := concreteRecoveryEvidencePaths(results, 4)
		writeStep := "Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`."

		if len(candidatePaths) > 0 {
			writeStep = fmt.Sprintf("Do not call a write tool until prior tool results contain source-body evidence for the concrete edit path and the exact replacement shape. Candidate path(s) with evidence so far: %s. When that evidence is present, emit one parser-readable write: `edit_file`, `write_file`, `append_file`, or `apply_patch`, using only real path and text values from the source evidence. Do not copy placeholder edit bodies or schema examples.", strings.Join(candidatePaths, ", "))
		}

		targetDiscoveryStep := "If the edit target is not identified, call `context_pack` for the user's objective rather than repeating a literal search that already missed. Use `mode:\"plan\"`, a small limit, and a task value copied from the actual user objective rather than a placeholder."
		if len(candidatePaths) > 0 {
			targetDiscoveryStep = fmt.Sprintf("Current evidence paths already identify candidate target(s): %s.\n\nIf one edit-safety fact is still missing, read the highest-confidence candidate instead of restarting discovery:\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"%s\",\"start_line\":1,\"end_line\":180,\"named_gap\":\"exact current source for the requested behavior\",\"why_not_answering\":\"need current source before the write\"}\n```\n\nDo not call `context_pack` again unless every listed candidate is proven irrelevant.", strings.Join(candidatePaths, ", "), candidatePaths[0])
		} else if contextPackUnavailableForRecovery(results) {
			targetDiscoveryStep = "The previous `context_pack` result failed or returned no usable target candidates, so do not call `context_pack` again in this recovery. Run a bounded fallback discovery phase instead: first `search_files` for likely filenames/components/domain terms, then `search_content` for stable labels, symbols, config keys, command names, or error text. If a literal phrase missed, broaden to nearby concepts. Read the highest-confidence candidate with `read_file` or a language-aware symbol tool before writing."
		} else if contextPackSeen(results) {
			targetDiscoveryStep = "The previous `context_pack` did not establish an exact edit target. Do not write or read a context-pack candidate unless it contains exact current-state evidence for the requested behavior. Run bounded fallback discovery instead: use `search_content` for stable domain terms, symbols, config keys, command names, or error text from the user's objective, then read the highest-confidence hit before writing."
		}

		return strings.Join([]string{
			"The completion gate rejected a final answer before any successful workspace write. No required_edit_paths list is active, so this is a target-selection failure, not a required-path coverage failure.",
			"Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.",
			writeStep,
			textualMutationEditFileJSONInstruction(),
			targetDiscoveryStep,
			"If one edit-safety fact is missing after a concrete candidate exists, call exactly one bounded discovery/read tool with `named_gap` and `why_not_answering` that names the fact needed before the write.",
			"The only non-tool closeout is `<<QUESTION>>:` naming the exact blocker, such as no resolvable edit target, missing permission, or missing body evidence. Do not repeat a final answer without a tool-backed write.",
		}, "\n\n"), false, true
	}

	if failed, ok := failedVerificationAfterLatestWrite(results, r.RuntimeBrief); ok {
		return postWriteVerificationFailedRecoveryPrompt(failed, plannerOwnsVerification(r.RuntimeBrief)), false, true
	}

	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {

		if plannerOwnsVerification(r.RuntimeBrief) {
			return postWriteVerificationPendingRecoveryPrompt(results, true), false, true
		}

		return postWriteVerificationPendingRecoveryPrompt(results, false), false, true
	}

	return "", false, false
}

func textualMutationWriteJSONInstruction() string {
	return "For textual tool transport, a write action is either one visible JSON object with a top-level `tool` field plus real source-backed arguments, or the Qwen key-value `tool:` format with the same real arguments. Function-call write syntax is not executable."
}

func textualMutationEditFileJSONInstruction() string {
	return textualMutationWriteJSONInstruction() + " For `edit_file`, include a real required path when one is active, exact `old_text` copied from source evidence, complete replacement `new_text`, and a one-sentence `description`. Do not copy placeholders, ellipses, schema examples, or prose as edit fields."
}

func (r *Runner) readOnlyEvidenceCompletionRecoveryPrompt(results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}

	// Source-grounding rejection: evidence was gathered but the answer cited
	// paths without body evidence. Guide the model to read those paths or
	// qualify the answer rather than searching for new targets. Check this
	// before the Readiness gate because even a nil/ready Readiness can
	// produce a source-grounding-unmet rejection on the answer text.
	if r.lastCompletionGateSourceGrounding.hasTypedSignal() && len(r.lastCompletionGateSourceGrounding.Paths) > 0 {
		return r.lastCompletionGateSourceGrounding.RecoveryPrompt(readOnlyEvidenceLedgerGuidanceForBrief(brief, results))
	}

	if brief.Readiness == nil || brief.Readiness.ReadyToAnswer {
		return ""
	}

	if len(genericReadOnlyEvidenceTools(brief.AllowedTools)) == 0 || genericReadOnlyAnswerEvidenceReady(brief, results) {
		return ""
	}

	targets := pathparse.MentionedWorkspacePaths(brief.Objective)
	if len(targets) == 0 {
		targets = append(targets, brief.TargetRefs...)
	}

	readPaths := newToolResultEvidenceProjection(results).ReadPaths()
	target := ""

	for _, rawTarget := range targets {
		candidate := NormalizeRunnerPath(rawTarget)
		if candidate == "" {
			continue
		}

		if _, alreadyRead := readPaths[candidate]; alreadyRead {
			continue
		}

		target = candidate

		break
	}

	if target != "" {
		return fmt.Sprintf("The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. The user named `%s`; make one parser-readable `read_file` request for that path now, then answer from the returned source. Do not edit files, run shell commands, or answer before the read result.\n\n```tool\n{\"tool\":\"read_file\",\"path\":\"%s\"}\n```", target, target)
	}

	if len(readPaths) > 0 {
		return "The completion gate rejected a read-only final answer because the gathered evidence does not yet include the codebase source needed to answer the explicit question. The named path(s) already read should not be reread. Make one targeted source search/read/navigation request for a different concrete code path tied to the missing claim, then answer from that evidence. Do not edit files or run shell commands."
	}

	return "The completion gate rejected a read-only final answer because no bounded workspace evidence has been gathered yet. Make one targeted read/search/discovery request tied to the explicit question, then answer from the returned evidence. Do not edit files, run shell commands, or answer before evidence."
}

func postWriteVerificationPendingRecoveryPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "The completion gate rejected a final answer because")
}

func postWriteVerificationRequiredPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "A workspace write completed and")
}

func postWriteVerificationPrompt(results []workspace.ToolResult, plannerOwned bool, intro string) string {
	paths := newToolResultEvidenceProjection(results).ChangedPathList()
	target := "changed source files"

	if len(paths) > 0 {
		target = strings.Join(paths, ", ")
	}

	scope := "post-write verification"
	requirement := "Required evidence is one focused language-appropriate verification command/tool for changed source files, compiler-backed diagnostics, or `<<QUESTION>>:` with the concrete verification blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."

	if plannerOwned {
		scope = "post-write lane sanity"
		requirement = "Required evidence is compiler-backed source-sanity or language diagnostics for changed source files, or `<<QUESTION>>:` with the concrete source-sanity blocker. Generic source-sanity fallback and `git diff --check` alone do not certify source edits."
	}

	action := "Call one parser-readable verification tool now."
	if len(paths) == 1 {
		action = "Choose the relevant language/project verification command or diagnostics route for this changed path and issue one parser-readable verification action now."
	}

	return strings.Join([]string{
		strings.TrimSpace(intro) + " " + scope + " is still unresolved for: " + target + ".",
		"Your next visible message must be one parser-readable verification action or `<<QUESTION>>:` blocker. Do not answer in prose, request more reads, or attempt additional edits in this verification phase.",
		requirement,
		action,
	}, "\n\n")
}

func (r *Runner) implementationRequiredPathCompletionRecoveryPrompt(missing []string, results []workspace.ToolResult, writeOnly bool) string {
	missing = nonEmptyRunnerStrings(missing, 12)

	target := "the remaining required edit path(s)"

	if len(missing) > 0 {
		target = strings.Join(missing, ", ")
	}

	var b strings.Builder

	if writeOnly {
		b.WriteString("The completion gate rejected a final answer while exact-anchor required edit path(s) remain unchanged: ")
	} else {
		b.WriteString("The completion gate rejected a final answer while required edit path(s) remain unchanged: ")
	}

	b.WriteString(target)
	b.WriteString(".\n\n")
	b.WriteString("Your next visible message must be exactly one parser-readable action, not prose and not an implementation summary. Do not include analysis before or after the action.\n\n")
	b.WriteString("Hidden-only reasoning is not completion evidence and may not be available to the tool parser. The contract outcome is a visible write, bounded read/discovery action, native tool call, or `<<QUESTION>>` blocker.\n\n")

	if scopedImplementationMissingPathsAreExistingSourceFiles(missing, r.Workspace) {
		b.WriteString("If the current evidence supports the requested change, call one bounded edit capability now. Use `apply_patch` for one coherent multi-file patch across required source/test paths, or `edit_file` with `old_text`+`new_text` / `start_line`+`end_line`+`content` for one remaining path. Do not use `write_file` or `append_file` for existing source/test edits in this recovery; `write_file` is a full-body write for missing/new files or non-source deliverables, and `append_file` is only for true end-of-file appends.\n\n")
	} else {
		b.WriteString("If the current evidence supports the requested change, call one write capability now for a remaining required path: `edit_file`, `write_file`, `append_file`, or `apply_patch`. Use real current text, complete replacement text, or a valid patch; do not copy placeholders or ellipses.\n\n")
	}

	unread := make([]string, 0, len(missing))
	for _, path := range missing {
		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		unread = append(unread, path)
	}

	if writeOnly {
		b.WriteString("Read/search/discovery tools are hidden for this turn because the packet already claimed exact write anchors. If an anchor is stale or missing, answer `<<QUESTION>>:` and name the stale or missing anchor for each blocked remaining path.\n")
	} else if len(unread) > 0 {
		path := unread[0]
		nextRead := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            path,
			StartLine:       1,
			EndLine:         220,
			NamedGap:        "exact current source needed before editing this required path",
			WhyNotAnswering: "need current source evidence before the write",
		}

		if recoveredReq, _, recovered := r.deterministicImplementationRequiredPathRead(results); recovered {
			nextRead = recoveredReq
		}

		b.WriteString("If one edit-safety fact is still missing, call exactly one bounded discovery/read action for a remaining required path or declared project symbol. Prefer the next unread source-body request now:\n\n")
		b.WriteString("```tool\n")
		b.WriteString(marshalToolRequestRaw(nextRead))
		b.WriteByte('\n')
		b.WriteString("```\n\n")
		b.WriteString("Do not perform broad discovery or task planning. A semantic/context lookup is valid only when it names the exact edit-safety fact needed before writing a remaining required path.\n")
	} else {
		b.WriteString("Every remaining required path already has source-body anchor evidence. Do not restart broad discovery or task planning. Call a write capability, or answer `<<QUESTION>>:` with the exact path-specific blocker that prevents the write.\n")

		if scopedImplementationMissingPathsAreExistingSourceFiles(missing, r.Workspace) {
			b.WriteString("\n" + textualMutationEditFileJSONInstruction() + "\n")
		}
	}

	b.WriteString("\nThe only non-tool closeout is `<<QUESTION>>:` with one concrete blocker reason for every remaining required path.")

	return b.String()
}

func scopedImplementationMissingPathsAreExistingSourceFiles(paths []string, manager *workspace.Manager) bool {
	if len(paths) == 0 || manager == nil {
		return false
	}

	for _, path := range paths {
		relPath := cleanRelativeRunnerPath(path)
		if relPath == "" || writePathIsNonSource(relPath) {
			return false
		}

		info, err := os.Stat(filepath.Join(manager.Root(), filepath.FromSlash(relPath)))
		if err != nil || info.IsDir() {
			return false
		}
	}

	return true
}

func postWriteVerificationFailedRecoveryPrompt(result workspace.ToolResult, plannerOwned bool) string {
	summary := strings.TrimSpace(result.Summary)
	detail := strings.TrimSpace(result.Error)

	if detail == "" {
		detail = strings.TrimSpace(result.Output)
	}

	if detail != "" {
		detail = textutil.Truncate(strings.TrimSpace(detail), 320)
	}

	var b strings.Builder

	if plannerOwned {
		b.WriteString("Post-write lane sanity failed after the required edits. Treat the failed sanity output as repair evidence, not as successful completion.\n\n")
	} else {
		b.WriteString("Post-write verification failed after the required edits. Treat the failed verification output as repair evidence, not as successful completion.\n\n")
	}

	if summary != "" {
		b.WriteString("Failed check: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}

	if detail != "" {
		b.WriteString("Failure detail: ")
		b.WriteString(detail)
		b.WriteString("\n")
	}

	b.WriteString("\nNext action must be one parser-readable action. If the failure names a changed or required path, line, symbol, or type error, repair the affected in-scope file with a write tool. If exactly one edit-safety fact is missing, use one narrow read/language/context lookup for that fact, then repair. Rerun verification only after a repair write or when the failure is clearly environmental. Answer `<<QUESTION>>:` only for an external, permission, or environment blocker that prevents repair.")

	return b.String()
}

func (r *Runner) prunePlanThenExecuteTerminalSurfaceHistory(messages *[]llama.ChatMessage, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	if droppedActions == 0 && droppedScaffolding == 0 {
		return
	}

	*messages = pruned

	r.emitEvent(marshalRunEvent("plan_then_execute_terminal_surface_history_pruned", map[string]any{
		"reason":                      strings.TrimSpace(reason),
		"dropped_tool_requests":       droppedActions,
		"dropped_surface_scaffolding": droppedScaffolding,
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalToolSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	surface := DerivePlanExecuteRecoverySurface(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
		r.refreshPlanThenExecuteAnchorRefreshSurface(messages, results, reason)

		return
	}

	if surface.Kind == PlanExecuteRecoverySurfaceSourceSatisfiedTerminal {
		r.refreshPlanThenExecuteTerminalOutcomeSurface(messages, results, reason)

		return
	}

	if surface.Kind != PlanExecuteRecoverySurfaceSubmitReady && surface.Kind != PlanExecuteRecoverySurfaceSubmitCorrection {
		r.prunePlanThenExecuteTerminalSurfaceHistory(messages, reason)

		return
	}

	submitPlanTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      submitPlanTools,
		ToolManifest:      workspace.BuildToolManifest(submitPlanTools),
		RequiredTools:     submitPlanTools,
		ReadinessGate:     append([]string(nil), terminalBrief.ReadinessGate...),
		SearchPolicy:      append([]string(nil), terminalBrief.SearchPolicy...),
		StopConditions:    append([]string(nil), terminalBrief.StopConditions...),
		FinalCritic:       append([]string(nil), terminalBrief.FinalCritic...),
		NextActions:       append([]string(nil), terminalBrief.RequiredNext...),
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	transport := "textual"
	surfaceTitle := "# Terminal Plan-Execute Textual Tool Surface"
	nativeTerminal := r.supportsTerminalSubmitPlanNativeTools(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	compactDropped := 0
	compactEvidenceMessages := 0

	if !nativeTerminal {
		pruned, compactDropped, compactEvidenceMessages = compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	}

	actionInstruction := r.textualToolProtocolPrompt(terminalBrief)
	submitPlanSkeleton := planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, nativeTerminal)
	renderedState := workerruntime.RenderRunState(terminalState)

	if nativeTerminal {
		transport = "native"
		surfaceTitle = "# Terminal Plan-Execute Native Tool Surface"
		terminalState.ReadinessGate = []string{"native_submit_plan_tool_call_required"}
		terminalState.StopConditions = []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"}
		terminalState.FinalCritic = []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		terminalState.NextActions = []string{"call native submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		renderedState = workerruntime.RenderStepInstruction(workerruntime.StepInstruction{
			CurrentState: terminalState,
			Directive:    "Use this current-state packet for the next action. Plan-execute source obligations are closed and this request exposes exactly one native tool: submit_plan. Call native submit_plan now. " + planThenExecuteSubmitPlanCoverageSentence() + " Do not read, search, shell, verify, use per-edit write tools, or answer in prose.",
		})
		actionInstruction = "Native `submit_plan` is the only exposed tool in this request. Use the `submit_plan` tool call now; do not answer in prose."
	}

	terminalPrompt := strings.Join([]string{
		surfaceTitle,
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `read_file`, `code_navigate`, search, shell, source-sanity, and per-edit write examples are stale because source obligations are closed.",
		"",
		renderedState,
		"",
		submitPlanSkeleton,
		"",
		actionInstruction,
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     transport,
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 submitPlanTools,
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalOutcomeSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_terminal_outcome",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      nil,
		ToolManifest:      nil,
		RequiredTools:     nil,
		ReadinessGate:     []string{"terminal_outcome_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness"},
		StopConditions:    []string{"decision_source_satisfied", "textual_submit_plan_returned_ok", "question_blocker_reported"},
		FinalCritic:       []string{"one_terminal_outcome_only", "per_path_current_state_evidence_for_source_satisfied"},
		NextActions: []string{
			"answer `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state",
			"or emit one visible textual submit_plan JSON object with real executable edits if changes are still needed",
			"or answer `<<QUESTION>>:` with the exact path-specific blocker",
		},
		InvalidActions: []string{
			"empty or source-satisfied submit_plan transport",
			"read/search/discovery after terminal readiness",
			"verification/build/test in planner phase",
			"per-edit write in planner phase",
			"prose-only answer without Decision/source evidence or blocker",
		},
		Readiness: terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Outcome Surface",
		"",
		"This replaces earlier runtime brief and tool protocol examples for this planner turn. Earlier `submit_plan`-only recovery text is stale because the previous `submit_plan` attempted a source-satisfied closeout through the wrong transport.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		"Next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required path already contains the requested final state.",
		"2. One visible textual `submit_plan` JSON object with real executable edits if changes are still needed. " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty or source-satisfied `submit_plan` object. Do not call read/search/discovery, shell, verification, or per-edit write tools.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "terminal_outcome",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 []string{},
	}))
}

func (r *Runner) refreshPlanThenExecuteTerminalTextualFallbackSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil || !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		return
	}

	submitPlanTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)
	terminalBrief := r.planThenExecuteTerminalToolBrief(results)
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	pruned, compactDropped, compactEvidenceMessages := compactPlanThenExecuteTerminalTextualMessages(pruned, terminalBrief, results)
	terminalState := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(terminalBrief.Lane), r.agentName()),
		TaskFamily:        terminalBrief.TaskFamily,
		Contract:          terminalBrief.Contract,
		Phase:             "plan_submit_ready_textual_fallback",
		Objective:         terminalBrief.Objective,
		Permission:        terminalBrief.PermissionProfile,
		TargetRefs:        append([]string(nil), terminalBrief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), terminalBrief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(terminalBrief, results, 6),
		Constraints:       append([]string(nil), terminalBrief.Constraints...),
		AllowedTools:      submitPlanTools,
		ToolManifest:      workspace.BuildToolManifest(submitPlanTools),
		RequiredTools:     submitPlanTools,
		ReadinessGate:     []string{"visible_textual_submit_plan_json_required"},
		SearchPolicy:      []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"},
		StopConditions:    []string{"submit_plan_returned_ok", "submit_plan_rejected_with_blocker"},
		FinalCritic:       []string{"submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"},
		NextActions:       []string{"emit exactly one visible submit_plan JSON object using edits[] for changed paths and source_satisfied_paths for already-final paths"},
		InvalidActions:    append([]string(nil), terminalBrief.InvalidActions...),
		Readiness:         terminalBrief.Readiness,
	}
	terminalPrompt := strings.Join([]string{
		"# Terminal Plan-Execute Textual Tool Surface",
		"",
		"Native tool-call transport failed to produce a required `submit_plan` call. Native tools are disabled for this retry; the only accepted action is one visible textual `submit_plan` JSON object.",
		"",
		workerruntime.RenderRunState(terminalState),
		"",
		planThenExecuteTerminalSubmitPlanSkeleton(terminalBrief, results, false),
		"",
		r.textualToolProtocolPrompt(terminalBrief),
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(terminalPrompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_textual_tool_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual_fallback",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"dropped_compact_messages":      compactDropped,
		"compact_evidence_messages":     compactEvidenceMessages,
		"allowed_tools":                 submitPlanTools,
	}))
}

func (r *Runner) refreshPlanThenExecuteAnchorRefreshSurface(messages *[]llama.ChatMessage, results []workspace.ToolResult, reason string) {
	if r == nil || !r.planThenExecutePlannerPhase() || messages == nil {
		return
	}

	stateProjection := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if stateProjection.Mode != PlanExecutePlanningModeAnchorRefresh || len(stateProjection.RefreshPaths) == 0 {
		return
	}

	refreshPaths := stateProjection.RefreshPaths
	pruned, droppedActions, droppedScaffolding := prunePlanThenExecuteTerminalTextualMessages(*messages)
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	anchorRefreshTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)
	state := workerruntime.RunState{
		Lane:              textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		TaskFamily:        brief.TaskFamily,
		Contract:          brief.Contract,
		Phase:             "plan_submit_anchor_refresh_required",
		Objective:         brief.Objective,
		Permission:        brief.PermissionProfile,
		TargetRefs:        append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths: append([]string(nil), brief.RequiredEditPaths...),
		EvidencePaths:     runnerEvidencePathsForBrief(brief, results, 6),
		Constraints:       append([]string(nil), brief.Constraints...),
		AllowedTools:      anchorRefreshTools,
		ToolManifest:      workspace.BuildToolManifest(anchorRefreshTools),
		RequiredTools:     anchorRefreshTools,
		ReadinessGate:     []string{"bounded_anchor_refresh_before_corrected_submit_plan"},
		SearchPolicy:      append([]string(nil), brief.SearchPolicy...),
		StopConditions:    []string{"bounded_source_refresh_returned"},
		FinalCritic:       []string{"refresh_targets_rejected_anchor_path", "no unrelated discovery"},
		NextActions:       append([]string(nil), brief.RequiredNext...),
		InvalidActions:    append([]string(nil), brief.InvalidActions...),
		Readiness:         brief.Readiness,
	}
	targetText := "the stale or missing required edit path named by the submit_plan rejection"

	if len(refreshPaths) > 0 {
		targetText = strings.Join(refreshPaths, ", ")
	}

	prompt := strings.Join([]string{
		"# Plan-Execute Anchor Refresh Surface",
		"",
		"A previous `submit_plan` was rejected because one or more edit anchors were stale or missing. This is a source-refresh phase, not a submit-plan phase.",
		"",
		workerruntime.RenderRunState(state),
		"",
		"Refresh only: " + targetText + ".",
		planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths),
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not emit `submit_plan` until this bounded refresh returns.",
		"After the refresh, use anchors copied from the refreshed source. Prefer `insert_after_line` plus `content` for insertions/appends; use `old_text` only when copied exactly from fresh source.",
	}, "\n")

	*messages = append(pruned, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(prompt)})
	r.emitEvent(marshalRunEvent("plan_then_execute_anchor_refresh_surface_refreshed", map[string]any{
		"reason":                        strings.TrimSpace(reason),
		"transport":                     "textual",
		"dropped_tool_request_messages": droppedActions,
		"dropped_scaffolding_messages":  droppedScaffolding,
		"allowed_tools":                 anchorRefreshTools,
		"refresh_paths":                 refreshPaths,
	}))
}

func (r *Runner) planThenExecuteTerminalToolBrief(results []workspace.ToolResult) workerruntime.WorkerRuntimeBrief {
	brief := runnerBriefWithPhaseHints(r.RuntimeBrief, results)
	if strings.TrimSpace(brief.Lane) == "" {
		brief.Lane = r.agentName()
	}

	submitPlanTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly)
	submitPlanDisallowedTools := workspace.PlanExecuteSubmitReadyDisallowedTools(brief.AllowedTools)
	brief.AllowedTools = submitPlanTools
	brief.RequiredTools = submitPlanTools
	brief.ForbiddenTools = textutil.Merge(brief.ForbiddenTools, submitPlanDisallowedTools)
	brief.ToolManifest = workspace.BuildToolManifest(submitPlanTools)
	brief.Constraints = textutil.Merge(brief.Constraints, []string{"plan_then_execute_submit_plan_ready", "required_edit_paths_define_write_scope", "planner_owned_verification"})
	brief.SearchPolicy = []string{"no read/search/discovery action is valid after submit_plan readiness before the first terminal outcome"}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		brief.Constraints = textutil.Merge(brief.Constraints, []string{"plan_then_execute_submit_correction_only"})
		brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths and source_satisfied_paths for already-final paths"}
		brief.InvalidActions = []string{"read/search/source-refresh after submit_plan correction is ready", "Decision: source-satisfied after an incomplete submit_plan", "<<QUESTION>> after source evidence is sufficient for correction", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
		brief.ReadinessGate = []string{"submit_plan_correction_required"}
		brief.StopConditions = []string{"submit_plan_returned_ok"}
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "edits_or_source_satisfied_paths_cover_required_paths"}
		if planThenExecuteSubmitCorrectionBlockerAllowed(r.RuntimeBrief, results) {
			brief.RequiredNext = []string{"corrected submit_plan using edits[] for changed paths, or <<QUESTION>> with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts"}
			brief.InvalidActions = []string{"read/search/source-refresh after submit_plan correction is ready", "Decision: source-satisfied after an incomplete submit_plan", "generic <<QUESTION>> without exact path-specific missing body/anchor blocker", "verify in planner phase", "per-edit write in planner phase", "prose-only final answer"}
			brief.ReadinessGate = []string{"submit_plan_correction_or_exact_body_anchor_blocker_required"}
			brief.StopConditions = []string{"submit_plan_returned_ok", "exact_missing_body_anchor_blocker_returned"}
			brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called_or_exact_body_anchor_blocker", "edits_or_source_satisfied_paths_cover_required_paths_unless_exact_blocker"}
		}
	} else {
		brief.ReadinessGate = textutil.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, brief.ReadinessGate)
		brief.StopConditions = textutil.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, brief.StopConditions)
		brief.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}
	}

	brief.Readiness = runnerReadinessState(r.agentName(), brief, results)

	return workerruntime.NormalizeRuntimeBrief(brief)
}

const planThenExecuteCurrentSubmitPlanSkeletonPrefix = "# Current Submit Plan Skeleton"

func planThenExecuteTerminalSubmitPlanSkeleton(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Use `Decision: source-satisfied` with current-state evidence, answer `<<QUESTION>>:` with the exact blocker, or perform one bounded read of a concrete candidate path before submitting a plan."
	}

	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}

	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

func planThenExecuteTerminalSubmitPlanSkeletonNoMoreTools(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, native bool) string {
	entries := planThenExecuteTerminalSubmitPlanSkeletonEntries(brief, results)
	if len(entries) == 0 {
		return "No concrete selected edit path is available for a safe `submit_plan` skeleton. Because the loop budget is exhausted, do not call more tools. Use `Decision: source-satisfied` with current-state evidence, or answer `<<QUESTION>>:` with the exact blocker."
	}

	if native {
		return planThenExecuteNativeSubmitPlanSkeleton(entries)
	}

	return planThenExecuteTextualSubmitPlanSkeleton(entries)
}

type planThenExecuteSubmitPlanSkeletonEntry struct {
	Path            string
	Evidence        []string
	EvidenceOmitted bool
	StartLine       int
	EndLine         int
	NewFile         bool
}

const planThenExecuteSubmitPlanSkeletonMaxEvidenceLines = 16

func planThenExecuteTerminalSubmitPlanSkeletonEntries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []planThenExecuteSubmitPlanSkeletonEntry {
	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{})
	paths := planThenExecuteSubmitPlanSkeletonPaths(state)
	evidenceByPath := planThenExecuteTerminalSubmitPlanEvidenceByPath(results, 2)
	entries := make([]planThenExecuteSubmitPlanSkeletonEntry, 0, len(paths))
	evidenceLinesRemaining := planThenExecuteSubmitPlanSkeletonMaxEvidenceLines

	for _, path := range paths {
		entry := planThenExecuteSubmitPlanSkeletonEntry{
			Path:      path,
			StartLine: 1,
			EndLine:   1,
			NewFile:   planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results),
		}
		for _, evidence := range evidenceByPath[NormalizeRunnerPath(path)] {
			if evidenceLinesRemaining > 0 {
				entry.Evidence = append(entry.Evidence, evidence.Summary)
				evidenceLinesRemaining--
			} else {
				entry.EvidenceOmitted = true
			}

			if entry.StartLine == 1 && entry.EndLine == 1 && evidence.StartLine > 0 && evidence.EndLine >= evidence.StartLine {
				entry.StartLine = evidence.StartLine
				entry.EndLine = evidence.EndLine
			}
		}

		entries = append(entries, entry)
	}

	return entries
}

type planThenExecuteTerminalSubmitPlanEvidence struct {
	Summary   string
	StartLine int
	EndLine   int
}

func planThenExecuteTerminalSubmitPlanEvidenceByPath(results []workspace.ToolResult, maxPerPath int) map[string][]planThenExecuteTerminalSubmitPlanEvidence {
	if maxPerPath <= 0 {
		maxPerPath = 2
	}

	out := map[string][]planThenExecuteTerminalSubmitPlanEvidence{}
	seen := map[string]map[string]struct{}{}
	add := func(path string, summary string, start int, end int) {
		path = NormalizeRunnerPath(path)
		summary = strings.TrimSpace(summary)

		if path == "" || summary == "" || len(out[path]) >= maxPerPath {
			return
		}

		if seen[path] == nil {
			seen[path] = map[string]struct{}{}
		}

		if _, ok := seen[path][summary]; ok {
			return
		}

		seen[path][summary] = struct{}{}

		out[path] = append(out[path], planThenExecuteTerminalSubmitPlanEvidence{
			Summary:   summary,
			StartLine: start,
			EndLine:   end,
		})
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		sourceFacts := sourceBodyFactsFromResult(result)
		if len(sourceFacts) == 0 {
			for _, path := range ReadPathsFromToolResult(result) {
				add(path, result.Summary, 0, 0)
			}
			continue
		}

		for _, fact := range sourceFacts {
			add(fact.Path, result.Summary, fact.StartLine, fact.EndLine)
		}
	}

	return out
}

func planThenExecuteNativeSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder

	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nNative `submit_plan` tool-argument shape. Fill these arguments in the tool call; do not answer with this template as prose.\n\n")
	b.WriteString("rationale: write one concrete sentence explaining why the edit set satisfies the request.\n")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n")
	b.WriteString("edits:\n")

	for _, entry := range entries {
		b.WriteString("- path: ")
		b.WriteString(strconv.Quote(entry.Path))
		b.WriteString("\n")

		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}

		b.WriteString("  purpose: write one concrete sentence for this path.\n")
		b.WriteString("  choose exactly one edit shape:\n")

		if entry.NewFile {
			b.WriteString("    new_file: replace_all=true + old_text=\"\" + new_text\n")
		} else {
			b.WriteString(fmt.Sprintf("    line_range_default: start_line=%d + end_line=%d + content (replacement only; safest for source/test edits and large additions)\n", entry.StartLine, entry.EndLine))
			b.WriteString("    substring_small_exact: old_text + new_text\n")
			b.WriteString("    insertion_small_only: insert_after_line + small inline content\n")
		}
	}

	b.WriteString("\nEdit shape semantics:\n")
	b.WriteString("- The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("- Line-range `content` replaces the inclusive current-source range from `start_line` through `end_line`; include the complete final replacement for that range.\n")
	b.WriteString("- `insert_after_line` inserts small inline non-empty `content` after an existing 1-based line in an existing file; it does not replace current lines, must not be combined with other edit shapes, and must not be used for large generated blocks.\n")
	b.WriteString("- For large additions inside existing files, use a line-range replacement covering a small current anchor range and include the unchanged anchor line(s) plus inserted content.\n")
	b.WriteString("- Substring `old_text` must be copied exactly from current source and match once; `new_text` is the complete final replacement.\n")
	b.WriteString("- Missing/new-file `new_text` is the complete final file body.\n")
	b.WriteString("- For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")

	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("- Additional evidence handles were omitted after the template evidence cap; use the current-source evidence already present in this turn.\n")
	}

	b.WriteString("- Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")

	return b.String()
}

func planThenExecuteTextualSubmitPlanSkeleton(entries []planThenExecuteSubmitPlanSkeletonEntry) string {
	var b strings.Builder

	b.WriteString(planThenExecuteCurrentSubmitPlanSkeletonPrefix)
	b.WriteString("\n\nParser-readable submit_plan contract. Emit exactly one visible JSON object with top-level `tool:\"submit_plan\"`, `rationale`, and an `edits` array. The JSON must contain real values only; do not copy placeholders, ellipses, or schema examples. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString("\n\n")

	for _, entry := range entries {
		b.WriteString("- required edit path: ")
		b.WriteString(entry.Path)
		b.WriteString("\n")

		for _, evidence := range entry.Evidence {
			b.WriteString("  evidence: ")
			b.WriteString(evidence)
			b.WriteString("\n")
		}

		if entry.NewFile {
			b.WriteString("  expected shape: replace_all true, empty old_text, complete final new_text file body, and a concrete purpose.\n")
		} else {
			startLine := entry.StartLine
			endLine := entry.EndLine

			if startLine <= 0 {
				startLine = 1
			}

			if endLine < startLine {
				endLine = startLine
			}

			b.WriteString("  default source range evidence: start_line ")
			b.WriteString(strconv.Itoa(startLine))
			b.WriteString(", end_line ")
			b.WriteString(strconv.Itoa(endLine))
			b.WriteString(". Use that range only if it is the exact replacement range; otherwise adjust from source evidence.\n")
			b.WriteString("  allowed shapes: start_line/end_line/content, old_text/new_text, insert_after_line/content for small insertions, or replace_all/new_text only for missing/new-file writes.\n")
		}
	}

	if planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries) {
		b.WriteString("\nAdditional evidence handles were omitted after the evidence cap; use the current-source evidence already present in this turn.\n\n")
	}

	b.WriteString("Edit shape choices: prefer `start_line` + `end_line` + `content` for source/test edits, multi-line changes, and large additions anchored to existing lines; use `old_text` + `new_text` for small exact replacements/deletions; use `insert_after_line` + small inline `content` only for small insertions/appends after an existing line; use `replace_all:true` + empty `old_text` + full `new_text` only for missing/new files. Existing source/test files must use bounded anchored edits, not whole-file `replace_all`.\n")
	b.WriteString("The shown source ranges are evidence handles, not proposed edits; adjust to the exact replacement range when needed.\n")
	b.WriteString("`insert_after_line` targets an existing 1-based line in an existing file, inserts small inline non-empty `content`, and must not be combined with other edit shapes or used with large content.\n")
	b.WriteString("For missing/new files, use only `replace_all:true` with empty `old_text` and a complete `new_text` file body.\n")
	b.WriteString("Line-range `content` replaces every current source line from `start_line` through `end_line`, inclusive. It is not a patch hunk; include the complete final replacement for that range.\n")
	b.WriteString("Do not copy placeholder values into edit bodies; fill `old_text`, `new_text`, or `content` only from source evidence and the final intended code.")

	return b.String()
}

func planThenExecuteSubmitPlanSkeletonEvidenceOmitted(entries []planThenExecuteSubmitPlanSkeletonEntry) bool {
	for _, entry := range entries {
		if entry.EvidenceOmitted {
			return true
		}
	}

	return false
}

const planThenExecuteTerminalSourceEvidencePrefix = "# Terminal Source Evidence"

func compactPlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) ([]llama.ChatMessage, int, int) {
	out := make([]llama.ChatMessage, 0, len(messages)+4)
	dropped := 0

	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		switch role {
		case "user":
			out = append(out, message)
		case "system":
			if planThenExecuteKeepTerminalCompactSystemMessage(message) {
				out = append(out, message)
			} else {
				dropped++
			}
		default:
			dropped++
		}
	}

	evidence := planThenExecuteTerminalSourceEvidenceMessages(brief, results)

	out = append(out, evidence...)

	return out, dropped, len(evidence)
}

func planThenExecuteKeepTerminalCompactSystemMessage(message llama.ChatMessage) bool {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}

	return strings.HasPrefix(content, "AUTHORITATIVE_CONSTRAINT:") ||
		strings.HasPrefix(content, planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
}

func planThenExecuteTerminalSourceEvidenceMessages(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []llama.ChatMessage {
	required := map[string]struct{}{}

	for _, path := range normalizedRequiredEditPaths(brief.RequiredEditPaths) {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			required[normalized] = struct{}{}
		}
	}

	messages := make([]llama.ChatMessage, 0, len(results))
	seen := map[string]struct{}{}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		paths := ReadPathsFromToolResult(result)

		paths = nonArtifactEvidencePaths(paths)

		if len(paths) == 0 {
			continue
		}

		if len(required) > 0 && !planThenExecuteEvidenceIntersectsRequiredPaths(paths, required) {
			continue
		}

		summary := strings.TrimSpace(result.Summary)
		output := strings.TrimSpace(result.Output)

		if summary == "" && output == "" {
			continue
		}

		key := strings.TrimSpace(result.Tool) + "\x00" + summary + "\x00" + output
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		var b strings.Builder

		b.WriteString(planThenExecuteTerminalSourceEvidencePrefix)
		b.WriteString("\n\n")

		if summary != "" {
			b.WriteString("summary: ")
			b.WriteString(summary)
			b.WriteString("\n")
		}

		if len(paths) > 0 {
			b.WriteString("paths: ")
			b.WriteString(strings.Join(normalizedRunnerPaths(paths), ", "))
			b.WriteString("\n")
		}

		if output != "" {
			b.WriteString("\n")
			b.WriteString(output)
		}

		messages = append(messages, llama.ChatMessage{Role: "system", Content: strings.TrimSpace(b.String())})
	}

	return messages
}

func planThenExecuteEvidenceIntersectsRequiredPaths(paths []string, required map[string]struct{}) bool {
	for _, path := range paths {
		if IsNonSourceEvidenceArtifactPath(path) {
			continue
		}

		if _, ok := required[NormalizeRunnerPath(path)]; ok {
			return true
		}
	}

	return false
}

func normalizedRunnerPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}

	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if IsNonSourceEvidenceArtifactPath(normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	return out
}

func prunePlanThenExecuteTerminalTextualMessages(messages []llama.ChatMessage) ([]llama.ChatMessage, int, int) {
	if len(messages) == 0 {
		return messages, 0, 0
	}

	out := make([]llama.ChatMessage, 0, len(messages))
	droppedActions := 0
	droppedScaffolding := 0

	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "tool" {
			// Keep tool results — they carry source-body evidence the
			// model needs for reasoning about edits. Without them, the
			// model sees "Read file X" in the summary but not the
			// actual content.
			out = append(out, message)
			continue
		}

		if planThenExecuteDropTextualToolRequestHistoryMessage(message) || strings.TrimSpace(message.Role) == "assistant" {
			droppedActions++
			continue
		}

		if planThenExecuteDropTerminalTextualScaffoldingMessage(message) {
			droppedScaffolding++
			continue
		}

		out = append(out, message)
	}

	if droppedActions == 0 && droppedScaffolding == 0 {
		return messages, 0, 0
	}

	return out, droppedActions, droppedScaffolding
}

func planThenExecuteDropTerminalTextualScaffoldingMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "system" {
		return false
	}

	content := strings.TrimSpace(message.Content)
	if content == "" {
		return false
	}

	return strings.HasPrefix(content, "# Textual Tool Call Protocol") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Textual Tool Surface") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Native Tool Surface") ||
		strings.HasPrefix(content, "# Terminal Plan-Execute Outcome Surface") ||
		strings.HasPrefix(content, planThenExecuteCurrentSubmitPlanSkeletonPrefix) ||
		strings.HasPrefix(content, "[frontier step instruction]") ||
		strings.HasPrefix(content, "[frontier runtime brief]") ||
		strings.HasPrefix(content, "REQUIRED SOURCE READ PENDING:") ||
		strings.HasPrefix(content, "PLANNER READINESS REACHED:") ||
		strings.HasPrefix(content, "SUBMIT_PLAN PARSE RECOVERY:") ||
		strings.HasPrefix(content, "Plan-execute submit_plan is now the only allowed planner action.") ||
		strings.HasPrefix(content, "The completion gate rejected") ||
		strings.HasPrefix(content, "The previous `submit_plan` was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "The previous native `submit_plan` tool call was rejected before it became a valid plan artifact.") ||
		strings.HasPrefix(content, "Native submit_plan tool-call transport failed before the model produced a tool call.") ||
		strings.HasPrefix(content, "The previous retry produced hidden-only output with no parser-readable action.") ||
		strings.HasPrefix(content, "Note: your `read_file` call was redirected") ||
		strings.HasPrefix(content, "Note: your `code_navigate` call was redirected")
}

func planThenExecuteDropTextualToolRequestHistoryMessage(message llama.ChatMessage) bool {
	if strings.TrimSpace(message.Role) != "assistant" || len(message.ToolCalls) > 0 {
		return false
	}

	content := strings.TrimSpace(message.Content)
	if content == "" || !strings.Contains(content, `"tool"`) {
		return false
	}

	req, _, ok, parseErr := workspace.ParseToolRequest(content)
	if ok {
		tool := strings.TrimSpace(req.Tool)
		return tool != workspace.ToolNameSubmitPlan
	}

	return parseErr != nil || strings.Contains(content, `"tool"`)
}

const planThenExecuteRejectedSubmitPlanDraftEvidencePrefix = "# Rejected Submit Plan Draft Evidence"

func replacePlanThenExecuteRejectedSubmitPlanDraftEvidence(messages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if messages == nil {
		return
	}

	prompt := planThenExecuteRejectedSubmitPlanDraftEvidence(req, result)
	if prompt == "" {
		return
	}

	out := make([]llama.ChatMessage, 0, len(*messages)+1)
	for _, message := range *messages {
		if strings.TrimSpace(message.Role) == "system" &&
			strings.HasPrefix(strings.TrimSpace(message.Content), planThenExecuteRejectedSubmitPlanDraftEvidencePrefix) {

			continue
		}

		out = append(out, message)
	}

	out = append(out, llama.ChatMessage{Role: "system", Content: prompt})
	*messages = out
}

func planThenExecuteRejectedSubmitPlanDraftEvidence(req workspace.ToolRequest, result workspace.ToolResult) string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return ""
	}

	invalidPaths := submitPlanInvalidAnchorPaths(result)
	missingPaths := submitPlanMissingRequiredPaths(result)

	var b strings.Builder

	b.WriteString(planThenExecuteRejectedSubmitPlanDraftEvidencePrefix)
	b.WriteString("\n\nFactual evidence from the previous `submit_plan` request. The harness rejected that request; no plan artifact was created.\n")

	if brief := strings.TrimSpace(lastSubmitPlanCorrectionRejectionBrief(result)); brief != "" {
		b.WriteString("\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}

	b.WriteString("\nDraft edit entries from the rejected request. Rejected body excerpts are not source evidence; correct them against the latest read_file/code_navigate source evidence before resubmitting.\n")

	for i, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			path = strings.TrimSpace(edit.Path)
		}

		if path == "" {
			path = fmt.Sprintf("edits[%d]", i)
		}

		b.WriteString(fmt.Sprintf("- edits[%d] path=%s", i, path))

		if edit.StartLine > 0 || edit.EndLine > 0 {
			b.WriteString(fmt.Sprintf(" lines=%d-%d", edit.StartLine, edit.EndLine))
		}

		if _, ok := invalidPaths[NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=invalid_anchor_or_syntax")
		}

		if _, ok := missingPaths[NormalizeRunnerPath(edit.Path)]; ok {
			b.WriteString(" status=missing_required_path")
		}

		if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
			b.WriteString(" purpose=")
			b.WriteString(textutil.Truncate(strings.TrimSpace(purpose), 180))
		}

		b.WriteString("\n")

		if planThenExecuteShouldIncludeRejectedDraftBody(edit, invalidPaths) {
			body := rejectedDraftEditBody(edit)
			if body != "" {
				b.WriteString("  rejected_body_excerpt_not_source_evidence:\n")

				for _, line := range strings.Split(textutil.Truncate(strings.TrimSpace(body), 2200), "\n") {
					b.WriteString("    ")
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
		}

		if b.Len() > 7000 {
			b.WriteString("- remaining rejected draft entries omitted after bounded evidence cap.\n")

			break
		}
	}

	return strings.TrimSpace(b.String())
}

func planThenExecuteShouldIncludeRejectedDraftBody(edit planartifact.PlanEdit, invalidPaths map[string]struct{}) bool {
	if len(invalidPaths) == 0 {
		return false
	}

	_, ok := invalidPaths[NormalizeRunnerPath(edit.Path)]

	return ok
}

func rejectedDraftEditBody(edit planartifact.PlanEdit) string {
	if strings.TrimSpace(edit.Content) != "" {
		return edit.Content
	}

	if strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}

	return ""
}

// PlanThenExecutePlannerTaskFamily is the runtime-brief task_family
// that marks the planner phase of `idiom plan-execute`. The runner
// uses it to forbid per-edit write tools (edit_file, write_file,
// append_file, apply_patch, delegate_edit) — the planner's only
// writes go through submit_plan. The executor phase, which runs in a
// separate process via the orchestrator in internal/surfaces/cli/planexecute/plan_execute.go,
// uses the workspace dispatch directly.
const PlanThenExecutePlannerTaskFamily = string(autonomy.TaskFamilyPlanThenExecutePlanner)

const planThenExecuteAnchorRefreshMaxTokens = 2048

// PlanThenExecuteReviewRepairTaskFamily marks the post-executor
// action-capable review phase. It shares the submit_plan surface for
// corrective edits, but unlike the planner phase it may also close with a
// direct `Decision: pass` answer when the evidence checklist is satisfied.
const PlanThenExecuteReviewRepairTaskFamily = string(autonomy.TaskFamilyPlanThenExecuteReview)

const planThenExecuteResultCandidateSourceReady = "plan_then_execute.candidate_source_ready"

// planThenExecutePlannerPhase reports whether the runner is currently
// servicing the planner half of plan-then-execute mode.
func (r *Runner) planThenExecutePlannerPhase() bool {
	if r == nil {
		return false
	}

	return planThenExecutePlannerBrief(r.RuntimeBrief)
}

func (r *Runner) planThenExecutePlanSubmissionPhase() bool {
	if r == nil {
		return false
	}

	switch strings.TrimSpace(r.RuntimeBrief.TaskFamily) {
	case PlanThenExecutePlannerTaskFamily, PlanThenExecuteReviewRepairTaskFamily:
		return true
	default:
		return false
	}
}

func planThenExecutePlannerBrief(brief workerruntime.WorkerRuntimeBrief) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecutePlannerTaskFamily
}

// preflightPlanThenExecuteWriteGuard rejects per-edit write tools
// during the planner phase of plan-then-execute mode and steers the
// model at submit_plan instead. The system addendum the orchestrator
// injects is a soft instruction; this guard is the hard one — even
// if the model ignores the prompt, the tool call is intercepted and
// converted into a directional rejection.
//
// Returns Intercepted=false (and an empty result) for any other lane
// shape, so the cost when the guard is inactive is a single string
// comparison.
func (r *Runner) preflightPlanThenExecuteWriteGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() {
		return verificationInterceptResult{}
	}

	tool := strings.TrimSpace(req.Tool)
	if !workspace.IsWorkspaceWriteTool(tool) {
		return verificationInterceptResult{}
	}

	directive := "plan-then-execute planner phase: per-edit write tools are unavailable. " +
		"Enumerate every change in a single submit_plan call and stop. " +
		"The orchestrator's executor phase will dispatch each edit deterministically after you submit the plan."

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected per-edit write",
			Error:   directive,
		},
		Reason: "plan-then-execute planner phase: write tool " + tool + " redirected to submit_plan",
	}
}

// preflightPlanThenExecuteSubmitPlanCoverageGuard validates the
// planner's structured plan against the same required_edit_paths
// contract that normal write tools obey. submit_plan is not a
// per-edit write, so the normal write-scope guard cannot see the
// paths it will later cause the executor to mutate. This guard keeps
// the invariant deterministic while still giving the planner a normal
// recoverable tool error it can fix with a new submit_plan call.
func (r *Runner) preflightPlanThenExecuteSubmitPlanCoverageGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}

	edits := planThenExecuteEffectiveSubmitPlanEdits(req)

	if len(edits) == 0 && workspace.SubmitPlanRequestClaimsSourceSatisfied(req) {
		return verificationInterceptResult{
			Intercepted: true,
			Result: workspace.ToolResult{
				Tool:    "submit_plan",
				Summary: "submit_plan rejected: source-satisfied terminal shape required",
				Error:   "submit_plan cannot carry a source-satisfied/no-op closeout. Answer `Decision: source-satisfied` with per-path current-state evidence, submit real edits, or answer <<QUESTION>> with the exact blocker.",
				Code:    workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode,
			},
			Reason: "plan-then-execute planner phase: source-satisfied closeout used submit_plan transport",
		}
	}

	sourceSatisfiedPaths := planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req)
	planned := planThenExecutePlannedSubmitPaths(edits, sourceSatisfiedPaths)
	decision := plancontract.ValidateCoverage(plancontract.CoverageInput{
		Scope:                   r.planThenExecuteRequiredEditScope(),
		Edits:                   planThenExecuteCoverageEdits(edits),
		SourceSatisfiedPaths:    sourceSatisfiedPaths,
		MissingRequiredSnippets: r.missingSubmitPlanRequiredEditSnippets(req, planned),
		InvalidAnchors:          r.invalidSubmitPlanAnchors(req),
		PathRoleCoverage:        planThenExecuteContractPathRoleCoverage(r.planThenExecuteSubmitPlanPathRoleCoverage(mapKeys(planned))),
	})

	if !decision.Rejected {
		return verificationInterceptResult{}
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: required edit path coverage mismatch",
			Output:  plancontract.CoverageDetailsJSON(decision.Details),
			Error:   decision.ErrorText(),
		},
		Reason: decision.Reason,
	}
}

// preflightPlanThenExecuteSubmitPlanGroundingGuard prevents planner atoms from
// turning guessed file paths into executor plans. Required and allowed paths
// scope the edit, but existing-file edits still need source-body evidence or an
// explicit new-file/body contract that makes the edit executable.
func (r *Runner) preflightPlanThenExecuteSubmitPlanGroundingGuard(req workspace.ToolRequest, results []workspace.ToolResult) verificationInterceptResult {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}

	if len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0 {
		return verificationInterceptResult{}
	}

	missing := r.ungroundedSubmitPlanEditPaths(req, results)
	if len(missing) == 0 {
		return verificationInterceptResult{}
	}

	sort.Strings(missing)

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    "submit_plan",
			Summary: "submit_plan rejected: edit paths lack source grounding",
			Error: "submit_plan edits for plan-execute planner atoms must be grounded before executor handoff; missing source-body evidence for " + quotedRunnerStrings(missing) + ". " +
				"Read each existing edit path with `read_file` or " + workspace.ToolNameCodeNavigate + " " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + ", then submit a corrected plan. If the feature is already present, answer `Decision: source-satisfied` with the observed current-state evidence; if no concrete path can be identified, answer `<<QUESTION>>:` with the exact blocker.",
		},
		Reason: "plan-then-execute planner phase: submit_plan lacked grounded edit paths",
	}
}

func (r *Runner) ungroundedSubmitPlanEditPaths(req workspace.ToolRequest, results []workspace.ToolResult) []string {
	edits := planThenExecuteEffectiveSubmitPlanEdits(req)
	if len(edits) == 0 {
		return []string{"edits[]"}
	}

	root := ""
	if r != nil && r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}

	missing := make([]string, 0, len(edits))
	seen := make(map[string]struct{}, len(edits))

	for i, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			path = plancontract.EditName(i, planThenExecuteCoverageEdit(edit))
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		if r.submitPlanEditPathGrounded(path, edit, root, results) {
			continue
		}

		missing = append(missing, path)
	}

	return missing
}

func (r *Runner) submitPlanEditPathGrounded(path string, edit planartifact.PlanEdit, root string, results []workspace.ToolResult) bool {
	return r.submitPlanEditPathGrounding(path, edit, root, results).Level != planThenExecuteEvidenceNone
}

func (r *Runner) submitPlanEditPathGrounding(path string, edit planartifact.PlanEdit, root string, results []workspace.ToolResult) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteSourceEvidence{}
	}

	if r != nil {
		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			return evidence
		}
	}

	if planThenExecuteEditHasExactAnchorContract(edit, root, path) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceExactAnchorContract,
			Source: "submit_plan",
			Detail: "exact current-source edit anchor",
		}
	}

	if planThenExecuteEditHasValidatedInsertionAnchor(edit, root, path, results) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceExactAnchorContract,
			Source: "submit_plan",
			Detail: "validated current-source insertion anchor",
		}
	}

	if planThenExecuteCompleteNewFileEdit(edit, root, path) {
		return planThenExecuteSourceEvidence{
			Path:   path,
			Level:  planThenExecuteEvidenceNewFileContract,
			Source: "submit_plan",
			Detail: "complete new-file body",
		}
	}

	return planThenExecuteSourceEvidence{Path: path}
}

func planThenExecuteEditHasValidatedInsertionAnchor(edit planartifact.PlanEdit, root string, path string, results []workspace.ToolResult) bool {
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" || strings.TrimSpace(root) == "" {
		return false
	}

	if edit.InsertAfterLine <= 0 || strings.TrimSpace(edit.Content) == "" {
		return false
	}

	if strings.TrimSpace(edit.OldText) != "" ||
		strings.TrimSpace(edit.NewText) != "" ||
		edit.StartLine > 0 ||
		edit.EndLine > 0 ||
		edit.ReplaceAll {

		return false
	}

	evidence := planThenExecuteRequiredSourceEvidenceForPath(workerruntime.WorkerRuntimeBrief{}, results, path)
	if planThenExecuteEvidenceRank(evidence.Level) < planThenExecuteEvidenceRank(planThenExecuteEvidencePathSeen) {
		return false
	}

	current := readPlanProjectionBase(root, path)
	if current == "" {
		return false
	}

	lines := strings.Split(current, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return edit.InsertAfterLine <= len(lines)
}

func planThenExecuteEditHasExactAnchorContract(edit planartifact.PlanEdit, root string, path string) bool {
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" || strings.TrimSpace(root) == "" {
		return false
	}

	current := readPlanProjectionBase(root, path)
	if current == "" {
		return false
	}

	if strings.TrimSpace(edit.OldText) != "" {
		return strings.Contains(current, edit.OldText)
	}

	if edit.StartLine > 0 || edit.EndLine > 0 {
		_, ok := applyPlanProjectionRange(current, edit.StartLine, edit.EndLine, edit.Content)
		return ok
	}

	return false
}

func planThenExecuteCompleteNewFileEdit(edit planartifact.PlanEdit, root string, path string) bool {
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" || strings.TrimSpace(root) == "" {
		return false
	}

	if !edit.ReplaceAll || strings.TrimSpace(edit.OldText) != "" || strings.TrimSpace(edit.NewText) == "" {
		return false
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
		return false
	} else {
		return os.IsNotExist(err)
	}
}

func (r *Runner) planThenExecuteRequiredEditScope() plancontract.SubmitPlanCoverageScope {
	root := ""
	if r != nil && r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}

	if r == nil {
		return plancontract.RequiredScope(nil, nil, planThenExecuteDirectoryScopePredicate(root))
	}

	return plancontract.RequiredScope(r.RuntimeBrief.RequiredEditPaths, r.RuntimeBrief.AllowedEditPaths, planThenExecuteDirectoryScopePredicate(root))
}

func planThenExecuteRequiredCoveragePaths(required []string, root string) []string {
	return plancontract.CoveragePaths(required, planThenExecuteDirectoryScopePredicate(root))
}

func normalizedRequiredEditPaths(required []string) []string {
	out := make([]string, 0, len(required))
	seen := make(map[string]struct{}, len(required))

	for _, raw := range required {
		path := NormalizeRunnerPath(raw)
		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		out = append(out, path)
		seen[path] = struct{}{}
	}

	return out
}

func planThenExecuteRequiredPathIsDirectoryScope(path string, all []string, root string) bool {
	path = pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(path))
	if path == "" {
		return false
	}

	if root != "" {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil && info.IsDir() {
			return true
		}
	}

	if pathparse.HasWorkspaceFileExtension(path) {
		return false
	}

	prefix := strings.TrimSuffix(path, "/") + "/"
	for _, other := range all {
		if other != path && strings.HasPrefix(other, prefix) {
			return true
		}
	}

	return false
}

func planThenExecuteDirectoryScopePredicate(root string) func(string) bool {
	return func(path string) bool {
		return planThenExecuteRequiredPathIsDirectoryScope(path, nil, root)
	}
}

func planThenExecuteSubmitPlanCoverageDetailsFromResult(result workspace.ToolResult) (plancontract.CoverageDetails, bool) {
	return plancontract.CoverageDetailsFromJSON(result.Output)
}

func planThenExecutePlannedSubmitPaths(edits []planartifact.PlanEdit, sourceSatisfiedPaths []string) map[string]struct{} {
	planned := make(map[string]struct{}, len(edits)+len(sourceSatisfiedPaths))
	for _, path := range sourceSatisfiedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			planned[normalized] = struct{}{}
		}
	}
	for _, edit := range edits {
		if path := NormalizeRunnerPath(edit.Path); path != "" {
			planned[path] = struct{}{}
		}
	}
	return planned
}

func planThenExecuteCoverageEdits(edits []planartifact.PlanEdit) []plancontract.SubmitPlanCoverageEdit {
	out := make([]plancontract.SubmitPlanCoverageEdit, 0, len(edits))
	for _, edit := range edits {
		out = append(out, planThenExecuteCoverageEdit(edit))
	}
	return out
}

func planThenExecuteCoverageEdit(edit planartifact.PlanEdit) plancontract.SubmitPlanCoverageEdit {
	return plancontract.SubmitPlanCoverageEdit{
		Path:            edit.Path,
		Purpose:         edit.Purpose,
		OldText:         edit.OldText,
		NewText:         edit.NewText,
		Content:         edit.Content,
		StartLine:       edit.StartLine,
		EndLine:         edit.EndLine,
		InsertAfterLine: edit.InsertAfterLine,
		ReplaceAll:      edit.ReplaceAll,
	}
}

func planThenExecuteContractPathRoleCoverage(coverage []autonomy.PlanScopePathRoleCoverage) []plancontract.PathRoleCoverage {
	out := make([]plancontract.PathRoleCoverage, 0, len(coverage))
	for _, item := range coverage {
		out = append(out, plancontract.PathRoleCoverage{
			Path:                       item.Path,
			OriginalPath:               item.OriginalPath,
			Roles:                      planThenExecutePathRoleNames(item.Roles),
			EffectiveRole:              string(item.EffectiveRole),
			EffectiveInvariantID:       string(item.EffectiveInvariantID),
			Decision:                   string(item.Decision),
			SatisfiesRequiredCoverage:  item.SatisfiesRequiredCoverage,
			WithinAllowedCandidatePath: item.WithinAllowedCandidatePath,
		})
	}
	return out
}

func planThenExecutePathRoleNames(roles []autonomy.PathRoleKind) []string {
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(string(role)) != "" {
			out = append(out, string(role))
		}
	}
	return out
}

func (r *Runner) planThenExecuteSubmitPlanPathRoleCoverage(candidatePaths []string) []autonomy.PlanScopePathRoleCoverage {
	if r == nil || len(candidatePaths) == 0 {
		return nil
	}
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      r.RuntimeBrief.RequiredEditPaths,
		AllowedCandidatePaths:  r.RuntimeBrief.AllowedEditPaths,
		CandidateCoveragePaths: candidatePaths,
		Source:                 "plan_execute_submit_plan_guard",
	})
	if err != nil {
		return nil
	}
	return append([]autonomy.PlanScopePathRoleCoverage(nil), coverage.CandidateCoverage...)
}

func (r *Runner) invalidSubmitPlanAnchors(req workspace.ToolRequest) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}

	var invalid []string

	contentCache := make(map[string]string)

	for _, edit := range planThenExecuteEffectiveSubmitPlanEdits(req) {
		path := pathparse.NormalizeWorkspaceRelativePath(NormalizeRunnerPath(edit.Path))
		if path == "" {
			continue
		}

		content, ok := contentCache[path]
		if !ok {
			content = readPlanProjectionBase(root, path)
			contentCache[path] = content
		}

		if edit.OldText != "" {
			if !strings.Contains(content, edit.OldText) {
				invalid = append(invalid, path+": old_text not found in current source")
			}

			continue
		}

		if edit.StartLine > 0 || edit.EndLine > 0 {
			if _, ok := applyPlanProjectionRange(content, edit.StartLine, edit.EndLine, edit.Content); !ok {
				invalid = append(invalid, path+": line range outside current source")
			}
		}
	}

	return invalid
}

func (r *Runner) missingSubmitPlanRequiredEditSnippets(req workspace.ToolRequest, planned map[string]struct{}) []string {
	if r == nil {
		return nil
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}

	root := ""
	if r.Workspace != nil {
		root = strings.TrimSpace(r.Workspace.Root())
	}

	projectedContent := projectedPlanContentByPath(root, planThenExecuteEffectiveSubmitPlanEdits(req))
	addSourceSatisfiedPlanProjectionBases(root, projectedContent, planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req))

	var missing []string

	for i, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, planned)
		if len(candidates) == 0 {
			candidates = mapKeys(planned)
		}

		if planThenExecuteRequiredSnippetCovered(candidates, snippet.Content, projectedContent) {
			continue
		}

		missing = append(missing, requiredEditSnippetName(snippet, i))
	}

	return missing
}

func projectedPlanContentByPath(root string, edits []planartifact.PlanEdit) map[string]string {
	out := make(map[string]string, len(edits))
	for _, edit := range edits {
		path := NormalizeRunnerPath(edit.Path)
		if path == "" {
			continue
		}

		content, loaded := out[path]
		if !loaded {
			content = readPlanProjectionBase(root, path)
		}

		out[path] = applyPlanProjectionEdit(content, edit)
	}

	return out
}

func addSourceSatisfiedPlanProjectionBases(root string, projectedContent map[string]string, paths []string) {
	if len(paths) == 0 {
		return
	}

	for _, rawPath := range paths {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, alreadyProjected := projectedContent[path]; alreadyProjected {
			continue
		}

		projectedContent[path] = readPlanProjectionBase(root, path)
	}
}

func planThenExecuteEffectiveSubmitPlanEdits(req workspace.ToolRequest) []planartifact.PlanEdit {
	if req.PlanArtifact != nil && len(req.PlanArtifact.Edits) > 0 {
		return append([]planartifact.PlanEdit(nil), req.PlanArtifact.Edits...)
	}

	return append([]planartifact.PlanEdit(nil), req.Edits...)
}

func planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req workspace.ToolRequest) []string {
	var paths []string

	if req.PlanArtifact != nil && len(req.PlanArtifact.Paths.SourceSatisfied) > 0 {
		paths = append(paths, req.PlanArtifact.Paths.SourceSatisfied...)
	}

	paths = append(paths, req.SourceSatisfiedPaths...)

	return paths
}

func (r *Runner) planThenExecuteAttachSubmitPlanSourceSatisfiedEvidence(req workspace.ToolRequest, results []workspace.ToolResult) workspace.ToolRequest {
	if r == nil || !r.planThenExecutePlanSubmissionPhase() || strings.TrimSpace(req.Tool) != "submit_plan" {
		return req
	}

	sourceSatisfiedPaths := plancontract.UniqueScopePaths(planThenExecuteEffectiveSubmitPlanSourceSatisfiedPaths(req))
	if len(sourceSatisfiedPaths) == 0 {
		return req
	}

	artifact := planartifact.PlanArtifact{}
	if req.PlanArtifact != nil {
		artifact = *req.PlanArtifact
	}

	if len(artifact.Edits) == 0 && len(req.Edits) > 0 {
		artifact.Edits = append([]planartifact.PlanEdit(nil), req.Edits...)
	}

	if strings.TrimSpace(artifact.Task.Summary) == "" {
		artifact.Task.Summary = textutil.FirstNonEmpty(strings.TrimSpace(req.Rationale), "legacy submit_plan request")
	}

	artifact.Contract.Kind = planartifact.ContractKind(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.Kind)), string(planartifact.ContractKindPlanThenExecute)))
	artifact.Contract.StrategyOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.StrategyOwner)), string(planartifact.OwnerModel)))
	artifact.Contract.ContractOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.ContractOwner)), string(planartifact.OwnerHarness)))
	artifact.Contract.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(artifact.Contract.PermissionProfile), planartifact.PermissionProfilePlanThenExecute)
	artifact.Contract.VerificationOwner = planartifact.Owner(textutil.FirstNonEmpty(strings.TrimSpace(string(artifact.Contract.VerificationOwner)), string(planartifact.OwnerPlanner)))
	artifact.Paths.SourceSatisfied = plancontract.UniqueScopePaths(append(artifact.Paths.SourceSatisfied, sourceSatisfiedPaths...))

	if len(artifact.Paths.Required) == 0 {
		artifact.Paths.Required = plancontract.UniqueScopePaths(r.RuntimeBrief.RequiredEditPaths)
	}

	if len(artifact.Contract.RequiredEditPaths) == 0 {
		artifact.Contract.RequiredEditPaths = append([]string(nil), artifact.Paths.Required...)
	}

	if len(artifact.VerificationObligations) == 0 {
		artifact.VerificationObligations = []planartifact.PlanArtifactVerificationObligation{{
			ID:       "verification-1",
			Kind:     planartifact.VerificationKindExecutorLaneSanity,
			Owner:    artifact.Contract.VerificationOwner,
			Status:   planartifact.VerificationStatusOpen,
			Required: true,
		}}
	}

	sourceSatisfiedSet := map[string]struct{}{}

	for _, path := range sourceSatisfiedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			sourceSatisfiedSet[normalized] = struct{}{}
		}
	}

	filteredEvidence := artifact.Evidence[:0]
	for _, evidence := range artifact.Evidence {
		path := NormalizeRunnerPath(evidence.Path)
		if _, sourceSatisfied := sourceSatisfiedSet[path]; sourceSatisfied {
			continue
		}

		filteredEvidence = append(filteredEvidence, evidence)
	}

	artifact.Evidence = filteredEvidence

	knownEvidence := map[string]struct{}{}

	for _, evidence := range artifact.Evidence {
		path := NormalizeRunnerPath(evidence.Path)
		kind := strings.TrimSpace(string(evidence.Kind))

		if path != "" && kind != "" {
			knownEvidence[path+"|"+kind] = struct{}{}
		}
	}

	for _, path := range sourceSatisfiedPaths {
		evidence := planThenExecuteObservedSourceEvidenceForPath(results, path)
		if evidence.Level != planThenExecuteEvidenceAnchorReady {
			continue
		}

		kind := string(planartifact.EvidenceKindHarnessObservedSource)
		key := NormalizeRunnerPath(path) + "|" + kind

		if _, ok := knownEvidence[key]; ok {
			continue
		}

		artifact.Evidence = append(artifact.Evidence, planartifact.PlanArtifactEvidence{
			Kind:       planartifact.EvidenceKind(kind),
			Path:       path,
			Summary:    textutil.FirstNonEmpty(strings.TrimSpace(evidence.Detail), "current-state source evidence") + " via " + textutil.FirstNonEmpty(strings.TrimSpace(evidence.Source), "source tool"),
			Confidence: "observed",
		})
		knownEvidence[key] = struct{}{}
	}

	req.PlanArtifact = &artifact

	return req
}

func planThenExecuteObservedSourceEvidenceForPath(results []workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	return newToolResultEvidenceProjection(results).SourceEvidenceForPath(path)
}

func readPlanProjectionBase(root string, path string) string {
	path = pathparse.NormalizeWorkspaceRelativePath(path)
	if strings.TrimSpace(root) == "" || path == "" {
		return ""
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return ""
	}

	return string(body)
}

func applyPlanProjectionEdit(current string, edit planartifact.PlanEdit) string {
	if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
		return edit.NewText
	}

	if strings.TrimSpace(edit.OldText) != "" {
		if strings.Contains(current, edit.OldText) {
			return strings.Replace(current, edit.OldText, edit.NewText, 1)
		}

		return current + "\n" + edit.NewText
	}

	if edit.StartLine > 0 || edit.EndLine > 0 {
		if projected, ok := applyPlanProjectionRange(current, edit.StartLine, edit.EndLine, edit.Content); ok {
			return projected
		}

		return current + "\n" + edit.Content
	}

	if strings.TrimSpace(edit.NewText) != "" {
		return current + "\n" + edit.NewText
	}

	if strings.TrimSpace(edit.Content) != "" {
		return current + "\n" + edit.Content
	}

	return current
}

func applyPlanProjectionRange(current string, startLine int, endLine int, replacement string) (string, bool) {
	if startLine < 1 || endLine < startLine {
		return "", false
	}

	lines := strings.SplitAfter(current, "\n")
	if current == "" {
		lines = nil
	}

	if endLine > len(lines) {
		return "", false
	}

	start := startLine - 1
	before := strings.Join(lines[:start], "")
	after := strings.Join(lines[endLine:], "")

	if replacement != "" && !strings.HasSuffix(replacement, "\n") && after != "" {
		replacement += "\n"
	}

	return before + replacement + after, true
}

func planThenExecuteRequiredSnippetCovered(candidates []string, snippet string, projectedContent map[string]string) bool {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return true
	}

	for _, candidate := range candidates {
		path := NormalizeRunnerPath(candidate)
		if path == "" {
			continue
		}

		if editSnippetMatchesContent(projectedContent[path], snippet) {
			return true
		}
	}

	return false
}

func planThenExecuteRequiredPathDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", "", false
	}

	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return "", "", false
	}

	if strings.TrimSpace(req.Tool) == "submit_plan" || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return "", "", false
	}

	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	if len(unread) == 0 {
		return "", "", false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)

	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok && target != "" {
		invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
		required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)

		if _, invalidAnchor := invalidAnchorPaths[target]; invalidAnchor {
			if _, inRequiredScope := required[target]; !inRequiredScope {
				next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
				nextInstruction := planThenExecuteSourceReadInstruction(next, results)

				return "Required edit path source read required",
					"the previous submit_plan named an invalid edit anchor outside required_edit_paths. Do not refresh out-of-scope anchors; " + nextInstruction + ". Then submit_plan, source-satisfied, or <<QUESTION>> with the exact blocker.",
					true
			}

			if submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
				planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
				!successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, target) {

				return "", "", false
			}

			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
			nextInstruction := planThenExecuteSourceReadInstruction(next, results)

			return "Bounded submit_plan anchor refresh required",
				"the previous submit_plan named an invalid edit anchor on " + target + ". " + planThenExecuteAnchorRefreshReadFallbackInstruction([]string{target}) + " Then submit a corrected plan after all required-path source evidence is present. Remaining unread required path also needs source evidence: " + nextInstruction + ".",
				true
		}
	}

	if target != "" {
		if _, ok := required[target]; ok {
			if _, missing := unread[target]; missing {
				return "", "", false
			}

			next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)

			return "Required edit path source read required",
				"this required path already has source evidence. Before more discovery on " + target + ", " + planThenExecuteSourceReadInstruction(next, results) + ". Then submit_plan, source-satisfied, or <<QUESTION>> with the exact blocker.",
				true
		}
	}

	next := firstPlanThenExecuteUnreadRequiredPath(brief.RequiredEditPaths, unread)
	if next == "" {
		return "", "", false
	}

	return "Required edit path source read required",
		"required_edit_paths define the write scope, but not every required path has source evidence yet. Do not use broad context/search discovery here. " + planThenExecuteSourceReadInstruction(next, results) + ". After all required paths are read once, submit_plan, source-satisfied, or <<QUESTION>> with the exact path-specific blocker.",
		true
}

func planThenExecuteSourceReadInstruction(path string, results []workspace.ToolResult) string {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return "read the next unread required_edit_path with source-body evidence"
	}

	if planThenExecutePathHasOutlineEvidence(path, results) {
		return "read source for " + path + " using a source-body route: " + workspace.CodeNavigateCapabilityInstruction(semanticlanguage.CapReadSymbol) + " for the relevant declaration, or `read_file` with `start_line`/`end_line` copied from the outline. Do not repeat whole-file `read_file`; it can redirect to an outline and still leave the path unread"
	}

	return "read source for " + path + " with `read_file`; if the file is large or redirects to an outline, follow with " + workspace.CodeNavigateCapabilityInstruction(semanticlanguage.CapReadSymbol) + " or bounded `read_file` using `start_line`/`end_line`"
}

func planThenExecutePathHasOutlineEvidence(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
		if !strings.Contains(text, strings.ToLower(path)) {
			continue
		}

		if strings.Contains(text, "file_outline") ||
			strings.Contains(text, "go_file_outline") ||
			strings.Contains(text, "redirected "+strings.ToLower(path)+" to code_navigate") ||
			strings.Contains(text, "redirected "+strings.ToLower(path)+" to go_file_outline") {

			return true
		}
	}

	return false
}

func planThenExecuteSourceBodyReadRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case "read_file":
		return req.StartLine > 0 || req.EndLine > 0
	case "code_navigate", "read_only_capability":
		capability := strings.TrimSpace(req.Capability)

		return workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapExtractBody)(capability) ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body")
	default:
		return strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body")
	}
}

func planThenExecuteAnchorRefreshRequestAllowed(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		return req.StartLine > 0 && req.EndLine >= req.StartLine && req.EndLine-req.StartLine+1 <= 400
	case workspace.ToolNameCodeNavigate:
		capability := strings.TrimSpace(req.Capability)

		return workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapReadSymbol)(capability) ||
			workspace.CodeNavigateCapabilityPredicate(semanticlanguage.CapExtractBody)(capability)
	default:
		return false
	}
}

func planThenExecuteInvalidAnchorRefreshRequestAllowed(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}

	if strings.TrimSpace(req.Tool) != "read_file" {
		return true
	}

	path = NormalizeRunnerPath(path)
	if path == "" || !strings.EqualFold(filepath.Ext(path), ".go") {
		return true
	}

	if planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) {
		return true
	}
	if planThenExecuteReadFileCoversRejectedLineRange(req, path, results) {
		return true
	}

	return !planThenExecuteLargePathSeen(path, results)
}

func planThenExecuteReadFileCoversRejectedSyntaxLine(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	line, ok := submitPlanSyntaxValidationLineForPath(rejected, path)
	if !ok {
		return false
	}

	return req.StartLine <= line && line <= req.EndLine
}

func planThenExecuteReadFileCoversRejectedLineRange(req workspace.ToolRequest, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != "read_file" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	ranges := submitPlanLineRangeInvalidAnchorRanges(rejected, path)
	for _, lineRange := range ranges {
		if req.StartLine <= lineRange.start && req.EndLine >= lineRange.end {
			return true
		}
	}

	return false
}

func submitPlanSyntaxValidationLineForPath(result workspace.ToolResult, path string) (int, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0, false
	}

	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	if !strings.Contains(strings.ToLower(text), "preflight syntax validation failed") {
		return 0, false
	}

	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(line), "preflight syntax validation failed") || !strings.Contains(line, path) {
			continue
		}

		idx := strings.Index(line, " at line ")
		if idx < 0 {
			continue
		}

		rest := strings.TrimSpace(line[idx+len(" at line "):])

		var digits strings.Builder

		for _, r := range rest {
			if r < '0' || r > '9' {
				break
			}

			digits.WriteRune(r)
		}

		if digits.Len() == 0 {
			continue
		}

		parsed, err := strconv.Atoi(digits.String())
		if err == nil && parsed > 0 {
			return parsed, true
		}
	}

	return 0, false
}

func planThenExecuteLargePathSeen(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		fact, ok := sourceBodyFactForPath(result, path)
		if ok && fact.TotalLines >= planThenExecuteLargeFileGenericHeaderMinTotalLines {
			return true
		}
	}

	return false
}

func planThenExecutePostReadinessDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return "", "", false
	}

	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return "", "", false
	}

	if strings.TrimSpace(req.Tool) == "submit_plan" || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return "", "", false
	}

	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
		target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
		required := normalizedRequiredEditPathSet(brief.RequiredEditPaths)
		_, inRequiredScope := required[target]

		if submitPlanRejectionHasInvalidAnchor(rejected) &&
			target != "" &&
			inRequiredScope &&
			submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
			planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
			!successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, target) {

			return "", "", false
		}

		if refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt); len(refreshPaths) > 0 {
			return "Bounded submit_plan anchor refresh required",
				"the previous `submit_plan` was rejected before it became a valid plan artifact, and the corrected plan still needs bounded source-body refresh for: " + strings.Join(nonEmptyRunnerStrings(refreshPaths, 4), ", ") + ". " + lastSubmitPlanRejectionBrief(rejected) + planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths) + " Do not call `submit_plan` until the remaining anchor refresh returns.",
				true
		}

		if pendingHistory := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pendingHistory) > 0 {
			if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
				return "", "", false
			}

			return "Git history evidence required",
				"the rejected `submit_plan` anchor refresh is complete, but git history evidence is still required before corrected submit_plan. Do not perform additional source reads or search. Call " + workspace.PlanExecuteHistoryPendingToolListForPrompt() + " for the intended logic in: " + strings.Join(nonEmptyRunnerStrings(pendingHistory, 4), ", ") + ".",
				true
		}

		refreshNote := planThenExecutePostSubmitRejectionRefreshNote(brief, results, rejected, rejectedAt)

		return "Corrected submit_plan required",
			"every required_edit_path has satisfied its source-body obligation, but the previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanCorrectionRejectionBrief(rejected) + planThenExecuteSubmitCorrectionOutcomeInstruction(brief, results) + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, rejected) + " " + refreshNote,
			true
	}

	if len(planThenExecuteUnreadRequiredPaths(brief, results)) > 0 {
		return "", "", false
	}

	if pendingHistory := planThenExecuteHistoryPendingPaths(brief, results, nil); len(pendingHistory) > 0 {
		if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
			return "", "", false
		}

		return "Git history evidence required",
			"every required_edit_path has satisfied its source-body obligation, but git history evidence is still required before submit_plan. Do not perform additional source reads or search. Call " + workspace.PlanExecuteHistoryPendingToolListForPrompt() + " for the intended logic in: " + strings.Join(nonEmptyRunnerStrings(pendingHistory, 4), ", ") + ", answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker.",
			true
	}

	if planThenExecutePostSourceHistoryRequestAllowed(req, brief, results) {
		return "", "", false
	}

	return "Plan submission required",
		"every required_edit_path has satisfied its source-body obligation. Do not use more read/search discovery. Submit one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence() + " Or answer `Decision: source-satisfied` with per-path current-state evidence, or answer <<QUESTION>> with the exact path-specific blocker. " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		true
}

func planThenExecutePostReadinessRejectionRequiresSubmitReadyTransition(summary string) bool {
	switch strings.TrimSpace(summary) {
	case "Plan submission required", "Corrected submit_plan required":
		return true
	default:
		return false
	}
}

func planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths []string) string {
	paths := nonEmptyRunnerStrings(refreshPaths, 1)
	if len(paths) == 0 {
		return "Use exactly one bounded `read_file` with `start_line`/`end_line` or `" + workspace.ToolNameCodeNavigate + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " for one refresh path."
	}

	path := NormalizeRunnerPath(paths[0])
	if path == "" {
		path = strings.TrimSpace(paths[0])
	}

	return "Use exactly one bounded source-body request for one refresh path. If no symbol or exact range is known yet, use deterministic fallback `read_file` for `" + path + "` with `start_line:1` and `end_line:120`; otherwise use `" + workspace.ToolNameCodeNavigate + "` with " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " for the relevant declaration."
}

func planThenExecutePostSourceHistoryRequestAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !slices.Contains([]string{workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol}, strings.TrimSpace(req.Tool)) {
		return false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}

	for _, pending := range planThenExecuteHistoryPendingPaths(brief, results, nil) {
		if NormalizeRunnerPath(pending) == target {
			return true
		}
	}

	return false
}

func planThenExecutePostSubmitRejectionRefreshNote(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	refreshable := make([]string, 0, 2)

	for _, rawPath := range planThenExecuteRefreshScopePaths(brief, results) {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, invalid := invalidAnchorPaths[path]; invalid && !successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, path) {
			refreshable = append(refreshable, path)
			continue
		}
	}

	if len(refreshable) == 0 {
		return "No further read/search/discovery action is currently allowed for this rejected plan; use the existing source-body evidence to submit corrected edits now."
	}

	return "The only remaining read exception is one bounded source-body refresh for: " + strings.Join(nonEmptyRunnerStrings(refreshable, 4), ", ") + ". For `" + workspace.ToolNameCodeNavigate + "`, include explicit " + workspace.CodeNavigateSourceBodyCapabilityPhrase() + " plus `pattern`. Otherwise do not call read/search/discovery before the corrected `submit_plan`."
}

func planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req workspace.ToolRequest, scopePaths []string, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}

	if !planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) {
		return false
	}

	if planThenExecuteAnchorRefreshReadRangeAlreadyTried(req, target, results[rejectedAt+1:]) {
		return false
	}

	scope := normalizedRequiredEditPathSet(scopePaths)
	if _, ok := scope[target]; !ok {
		return false
	}

	if _, invalidAnchor := submitPlanInvalidAnchorPaths(rejected)[target]; invalidAnchor {
		if successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, target) {
			return false
		}
	} else if successfulSourceEvidencePathAfter(results, rejectedAt, target) {
		return false
	}

	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)

	return submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target)
}

func planThenExecuteAnchorRefreshReadRangeAlreadyTried(req workspace.ToolRequest, target string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return false
	}

	target = NormalizeRunnerPath(target)
	if target == "" || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return false
	}

	for _, readRange := range readRangesForPathFromResults(results, target) {
		if planThenExecuteRequestedRangeCoveredByRead(req.StartLine, req.EndLine, readRange) {
			return true
		}
	}

	return false
}

func submitPlanRejectionHasInvalidAnchor(result workspace.ToolResult) bool {
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return len(details.InvalidAnchors) > 0
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error))

	return strings.Contains(text, "invalid edit anchor") ||
		strings.Contains(text, "old_text not found") ||
		strings.Contains(text, "whole-file replace_all edit would rewrite existing source file") ||
		strings.Contains(text, "would rewrite the entire existing source file") ||
		strings.Contains(text, "would collapse") ||
		strings.Contains(text, "starts a header guard before selecting") ||
		strings.Contains(text, "line range outside current source") ||
		strings.Contains(text, "preflight syntax validation failed")
}

func submitPlanInvalidAnchorPaths(result workspace.ToolResult) map[string]struct{} {
	out := submitPlanSyntaxValidationPaths(result)
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		for path := range submitPlanPathMapFromDiagnostics(details.InvalidAnchors, true) {
			out[path] = struct{}{}
		}

		return out
	}

	text := result.Summary + "\n" + result.Error
	for path := range submitPlanPathMapFromDiagnostics(backtickedSubmitPlanDiagnostics(text), true) {
		out[path] = struct{}{}
	}
	for path := range submitPlanLineRangeInvalidAnchorPaths(result) {
		out[path] = struct{}{}
	}
	for path := range submitPlanWholeFileExistingSourceRewritePaths(result) {
		out[path] = struct{}{}
	}

	return out
}

func submitPlanWholeFileExistingSourceRewritePaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	lower := strings.ToLower(text)
	replaceAllMarker := "whole-file replace_all edit would rewrite existing source file"
	lineRangeMarker := "line-range replacement for "
	lineRangeWholeBodyMarker := "would rewrite the entire existing source file"
	if !strings.Contains(lower, replaceAllMarker) && !(strings.Contains(lower, lineRangeMarker) && strings.Contains(lower, lineRangeWholeBodyMarker)) {
		return out
	}

	for _, line := range strings.Split(text, "\n") {
		lineLower := strings.ToLower(line)
		if idx := strings.Index(lineLower, replaceAllMarker); idx >= 0 {
			rawPath := strings.TrimSpace(line[idx+len(replaceAllMarker):])
			if cut := strings.IndexAny(rawPath, ";\n\r\t "); cut >= 0 {
				rawPath = rawPath[:cut]
			}
			path := normalizeSubmitPlanDiagnosticPath(rawPath)
			if path != "" {
				out[path] = struct{}{}
			}
		}

		if !strings.Contains(lineLower, lineRangeWholeBodyMarker) {
			continue
		}
		idx := strings.Index(lineLower, lineRangeMarker)
		if idx < 0 {
			continue
		}
		rawPath := strings.TrimSpace(line[idx+len(lineRangeMarker):])
		rawPathLower := strings.ToLower(rawPath)
		if cut := strings.Index(rawPathLower, " lines "); cut >= 0 {
			rawPath = rawPath[:cut]
		} else if cut := strings.IndexAny(rawPath, ";\n\r\t "); cut >= 0 {
			rawPath = rawPath[:cut]
		}
		path := normalizeSubmitPlanDiagnosticPath(rawPath)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}

	return out
}

func submitPlanLineRangeInvalidAnchorPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	lower := strings.ToLower(text)
	lineRangeMarker := "line-range replacement for "
	if !strings.Contains(lower, lineRangeMarker) {
		return out
	}

	for _, line := range strings.Split(text, "\n") {
		lineLower := strings.ToLower(line)
		if !strings.Contains(lineLower, "would rewrite the entire existing source file") &&
			!strings.Contains(lineLower, "would collapse") &&
			!strings.Contains(lineLower, "starts a header guard before selecting") &&
			!strings.Contains(lineLower, "old_text not found") &&
			!strings.Contains(lineLower, "invalid edit anchor") {
			continue
		}
		idx := strings.Index(lineLower, lineRangeMarker)
		if idx < 0 {
			continue
		}
		rawPath := strings.TrimSpace(line[idx+len(lineRangeMarker):])
		rawPathLower := strings.ToLower(rawPath)
		if cut := strings.Index(rawPathLower, " lines "); cut >= 0 {
			rawPath = rawPath[:cut]
		} else if cut := strings.IndexAny(rawPath, ";\n\r\t "); cut >= 0 {
			rawPath = rawPath[:cut]
		}
		path := normalizeSubmitPlanDiagnosticPath(rawPath)
		if path == "" {
			continue
		}
		out[path] = struct{}{}
	}

	return out
}

func submitPlanLineRangeInvalidAnchorRanges(result workspace.ToolResult, path string) []planThenExecuteLineRange {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return nil
	}

	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	lower := strings.ToLower(text)
	lineRangeMarker := "line-range replacement for "
	if !strings.Contains(lower, lineRangeMarker) {
		return nil
	}

	var ranges []planThenExecuteLineRange
	for _, line := range strings.Split(text, "\n") {
		lineLower := strings.ToLower(line)
		if !strings.Contains(lineLower, "would rewrite the entire existing source file") &&
			!strings.Contains(lineLower, "would collapse") &&
			!strings.Contains(lineLower, "starts a header guard before selecting") &&
			!strings.Contains(lineLower, "old_text not found") &&
			!strings.Contains(lineLower, "invalid edit anchor") {
			continue
		}

		gotPath, lineRange, ok := submitPlanLineRangeInvalidAnchorDiagnostic(line, lineRangeMarker)
		if !ok || gotPath != path {
			continue
		}

		ranges = append(ranges, lineRange)
	}

	return ranges
}

type planThenExecuteLineRange struct {
	start int
	end   int
}

func submitPlanLineRangeInvalidAnchorDiagnostic(line string, marker string) (string, planThenExecuteLineRange, bool) {
	lineLower := strings.ToLower(line)
	idx := strings.Index(lineLower, marker)
	if idx < 0 {
		return "", planThenExecuteLineRange{}, false
	}

	rawPath := strings.TrimSpace(line[idx+len(marker):])
	rawPathLower := strings.ToLower(rawPath)
	lineMarker := " lines "
	cut := strings.Index(rawPathLower, lineMarker)
	if cut < 0 {
		return "", planThenExecuteLineRange{}, false
	}

	path := normalizeSubmitPlanDiagnosticPath(rawPath[:cut])
	if path == "" {
		return "", planThenExecuteLineRange{}, false
	}

	afterLines := strings.TrimSpace(rawPath[cut+len(lineMarker):])
	fields := strings.Fields(afterLines)
	if len(fields) == 0 {
		return "", planThenExecuteLineRange{}, false
	}

	startText, endText, ok := strings.Cut(strings.Trim(fields[0], ".,;:)"), "-")
	if !ok {
		return "", planThenExecuteLineRange{}, false
	}

	start, startErr := strconv.Atoi(startText)
	end, endErr := strconv.Atoi(endText)
	if startErr != nil || endErr != nil || start <= 0 || end < start {
		return "", planThenExecuteLineRange{}, false
	}

	return path, planThenExecuteLineRange{start: start, end: end}, true
}

func normalizeSubmitPlanDiagnosticPath(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`\"'.,;:()[]{}")
	raw = filepath.ToSlash(raw)
	if raw == "" {
		return ""
	}
	if path := NormalizeRunnerPath(raw); path != "" && !pathparse.IsAbsolutePath(raw) {
		return path
	}

	for _, marker := range []string{
		"/internal/",
		"/cmd/",
		"/pkg/",
		"/native/",
		"/scripts/",
		"/tests/",
		"/docs/",
		"/.idiom/",
		"/.planning/",
	} {
		if idx := strings.Index(raw, marker); idx >= 0 {
			return NormalizeRunnerPath(raw[idx+1:])
		}
	}

	return ""
}

func submitPlanSyntaxValidationPaths(result workspace.ToolResult) map[string]struct{} {
	out := make(map[string]struct{})
	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")

	if !strings.Contains(strings.ToLower(text), "preflight syntax validation failed") {
		return out
	}

	for _, path := range pathparse.MentionedWorkspaceFiles(text) {
		path = NormalizeRunnerPath(path)
		if path == "" {
			continue
		}

		out[path] = struct{}{}
	}

	return out
}

func submitPlanMissingRequiredPaths(result workspace.ToolResult) map[string]struct{} {
	if details, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return submitPlanPathMapFromDiagnostics(details.MissingRequiredPaths, false)
	}

	text := result.Summary + "\n" + result.Error
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "missing required edit path")

	if idx < 0 {
		return nil
	}

	section := text[idx:]
	sectionLower := lower[idx:]

	for _, marker := range []string{"invalid edit anchor", "path(s) outside", "required edit snippet", ". submit", ". answer"} {
		if cut := strings.Index(sectionLower, marker); cut > 0 {
			section = section[:cut]
			sectionLower = sectionLower[:cut]
		}
	}

	return submitPlanPathMapFromDiagnostics(strings.Split(section, "`"), false)
}

func submitPlanMissingSourceGroundingPaths(result workspace.ToolResult) map[string]struct{} {
	if !planThenExecuteSubmitPlanGroundingRejectedResult(result) {
		return nil
	}

	text := result.Summary + "\n" + result.Error
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "missing source-body evidence for")

	if idx < 0 {
		return nil
	}

	section := text[idx:]
	sectionLower := lower[idx:]

	for _, marker := range []string{". read each", ". if the feature", ". if no concrete", ". submit"} {
		if cut := strings.Index(sectionLower, marker); cut > 0 {
			section = section[:cut]
			sectionLower = sectionLower[:cut]
		}
	}

	return submitPlanPathMapFromDiagnostics(backtickedSubmitPlanDiagnostics(section), false)
}

func backtickedSubmitPlanDiagnostics(text string) []string {
	parts := strings.Split(text, "`")
	if len(parts) < 3 {
		return nil
	}

	out := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		if segment := strings.TrimSpace(parts[i]); segment != "" {
			out = append(out, segment)
		}
	}

	return out
}

func submitPlanPathMapFromDiagnostics(segments []string, allowColon bool) map[string]struct{} {
	out := make(map[string]struct{})

	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}

		if strings.Contains(segment, ":") {
			if !allowColon {
				continue
			}

			segment = strings.SplitN(segment, ":", 2)[0]
		} else if allowColon {
			continue
		}

		path := NormalizeRunnerPath(segment)
		if planThenExecuteRecoveryScopePathAllowed(path) {
			out[path] = struct{}{}
		}
	}

	return out
}

func planThenExecuteRecoveryScopePathAllowed(path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || submitPlanDiagnosticProtocolToken(path) {
		return false
	}

	if pathparse.LooksLikeWorkspacePathToken(path) || pathparse.LooksLikeWorkspaceFilePath(path) {
		return true
	}

	base := filepath.Base(path)
	if strings.Contains(base, ".") && !strings.HasSuffix(base, ".") && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}

	if strings.HasPrefix(base, ".") && len(base) > 1 && !strings.ContainsAny(path, " \t\r\n") {
		return true
	}

	switch base {
	case "Brewfile", "Justfile", "Pipfile", "Rakefile", "Taskfile":
		return true
	default:
		return false
	}
}

func submitPlanDiagnosticProtocolToken(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	if lower == "" {
		return true
	}

	if !strings.ContainsAny(lower, "abcdefghijklmnopqrstuvwxyz0123456789.") {
		return true
	}

	if strings.Contains(lower, "<<question>>") || lower == "decision" || strings.HasPrefix(lower, "decision ") {
		return true
	}

	switch lower {
	case "read_file", workspace.ToolNameCodeNavigate, "read_symbol", "extract_body", "submit_plan":
		return true
	default:
		return false
	}
}

func submitPlanInvalidAnchorPathsAllowsRead(paths map[string]struct{}, path string) bool {
	if len(paths) == 0 {
		return false
	}

	_, ok := paths[NormalizeRunnerPath(path)]

	return ok
}

func successfulSourceEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for i := index + 1; i < len(results); i++ {
		if planThenExecuteEvidenceRank(planThenExecuteToolResultSourceEvidence(results[i], path).Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {
			return true
		}
	}

	return false
}

func successfulInvalidAnchorCorrectionEvidencePathAfter(results []workspace.ToolResult, index int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for i := index + 1; i < len(results); i++ {
		if invalidAnchorCorrectionEvidenceResult(results[i], path, results[:i+1]) {
			return true
		}
	}

	return false
}

func successfulInvalidAnchorCorrectionEvidenceForRejection(results []workspace.ToolResult, rejectedAt int, path string) bool {
	if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
		return true
	}

	return successfulInvalidAnchorCorrectionEvidenceAfterPriorRejection(results, rejectedAt, path)
}

func successfulInvalidAnchorCorrectionEvidenceAfterPriorRejection(results []workspace.ToolResult, rejectedAt int, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || rejectedAt <= 0 || rejectedAt > len(results) {
		return false
	}

	for i := rejectedAt - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}
		if strings.TrimSpace(result.Error) == "" {
			return false
		}
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) || !planThenExecuteSubmitPlanRejectedResult(result) {
			continue
		}
		if _, invalid := submitPlanInvalidAnchorPaths(result)[path]; !invalid {
			continue
		}
		for j := i + 1; j < rejectedAt; j++ {
			if invalidAnchorCompleteCorrectionEvidenceResult(results[j], path, results[:j+1]) {
				return true
			}
		}
	}

	return false
}

func invalidAnchorCompleteCorrectionEvidenceResult(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if readFileResultCoversRejectedLineRange(result, path, results) {
		return true
	}

	evidence := planThenExecuteToolResultSourceEvidence(result, path)
	if evidence.Level != planThenExecuteEvidenceAnchorReady {
		return false
	}

	if strings.TrimSpace(result.Tool) != "read_file" {
		return true
	}

	fact, ok := sourceBodyFactForPath(result, path)
	if !ok {
		return false
	}

	if sourceBodyFactCoversWholeFile(fact) {
		return true
	}

	req := workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: fact.StartLine,
		EndLine:   fact.EndLine,
	}

	return planThenExecuteReadFileCoversRejectedSyntaxLine(req, path, results) ||
		planThenExecuteReadFileCoversRejectedLineRange(req, path, results)
}

func invalidAnchorCorrectionEvidenceResult(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if readFileResultCoversRejectedLineRange(result, path, results) {
		return true
	}
	if readFileResultIsBoundedCppSourceWindow(result, path) {
		return true
	}

	evidence := planThenExecuteToolResultSourceEvidence(result, path)
	if evidence.Level != planThenExecuteEvidenceAnchorReady {
		return false
	}

	if strings.TrimSpace(result.Tool) != "read_file" {
		return true
	}

	fact, ok := sourceBodyFactForPath(result, path)
	if !ok {
		return false
	}

	if sourceBodyFactCoversWholeFile(fact) {
		return true
	}

	if planThenExecuteReadFileCoversRejectedSyntaxLine(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: fact.StartLine,
		EndLine:   fact.EndLine,
	}, path, results) {

		return true
	}
	if planThenExecuteReadFileCoversRejectedLineRange(workspace.ToolRequest{
		Tool:      "read_file",
		Path:      path,
		StartLine: fact.StartLine,
		EndLine:   fact.EndLine,
	}, path, results) {

		return true
	}

	return false
}

func readFileResultIsBoundedCppSourceWindow(result workspace.ToolResult, path string) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return false
	}
	if strings.TrimSpace(result.Output) == "" {
		return false
	}

	fact, ok := sourceBodyFactForPath(result, path)
	if !ok {
		return false
	}

	normalizedPath := strings.ToLower(NormalizeRunnerPath(path))
	if !(strings.HasSuffix(normalizedPath, ".cpp") ||
		strings.HasSuffix(normalizedPath, ".cc") ||
		strings.HasSuffix(normalizedPath, ".cxx")) {
		return false
	}
	if fact.StartLine > 400 {
		return false
	}

	return true
}

func readFileResultCoversRejectedLineRange(result workspace.ToolResult, path string, results []workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
		return false
	}

	fact, ok := sourceBodyFactForPath(result, path)
	if !ok {
		return false
	}

	return planThenExecuteReadFileCoversRejectedLineRange(workspace.ToolRequest{
		Tool:      workspace.ToolNameReadFile,
		Path:      path,
		StartLine: fact.StartLine,
		EndLine:   fact.EndLine,
	}, path, results)
}

func lastRejectedSubmitPlanCoverageResult(results []workspace.ToolResult) (workspace.ToolResult, int, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			continue
		}

		if planThenExecuteSubmitPlanRejectedResult(result) {
			return result, i, true
		}
	}

	return workspace.ToolResult{}, -1, false
}

func planThenExecuteSubmitPlanRejectedForCorrection(req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(req.Tool) == "submit_plan" &&
		planThenExecuteSubmitPlanRejectedResult(result) &&
		!planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
}

func planThenExecuteSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}

	if _, ok := planThenExecuteSubmitPlanCoverageDetailsFromResult(result); ok {
		return true
	}

	return strings.Contains(result.Summary, "submit_plan rejected") || strings.Contains(result.Error, "submit_plan must include")
}

func planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}

	if strings.TrimSpace(result.Code) == workspace.SubmitPlanSourceSatisfiedTerminalRequiredCode {
		return true
	}

	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error}, "\n"))

	return strings.Contains(text, "source-satisfied terminal shape required") ||
		strings.Contains(text, "source-satisfied rationale")
}

func planThenExecuteSourceSatisfiedTerminalRequiredPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	scopeLine := "Use the relevant selected edit paths from gathered current-source evidence."

	if len(required) > 0 {
		scopeLine = "Required path(s): " + strings.Join(required, ", ") + "."
	}

	return strings.Join([]string{
		"The previous `submit_plan` attempted a source-satisfied/no-op closeout using the wrong transport. That rejected tool call is not a valid plan artifact and is not an accepted source-satisfied terminal answer.",
		"",
		lastSubmitPlanRejectionBrief(result),
		scopeLine,
		"",
		"The next visible action must be exactly one terminal outcome:",
		"1. `Decision: source-satisfied` with per-path current-state evidence if every required or selected path already contains the requested final state.",
		"2. `submit_plan` with real executable edits for every required or selected path if changes are needed.",
		"3. `<<QUESTION>>:` with the exact path-specific blocker.",
		"",
		"Do not repeat an empty `submit_plan`, and do not treat the rejected tool call as plan coverage.",
	}, "\n")
}

func planThenExecuteSubmitPlanGroundingRejectedResult(result workspace.ToolResult) bool {
	return strings.TrimSpace(result.Tool) == "submit_plan" &&
		strings.Contains(strings.ToLower(result.Summary+"\n"+result.Error), "edit paths lack source grounding")
}

func submitPlanRejectionHasLargeInsertAfterLine(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))

	return strings.Contains(text, "insert_after_line content exceeds inline submit_plan limit") ||
		(strings.Contains(text, "insert_after_line") && strings.Contains(text, "content_ref is not supported"))
}

func submitPlanRejectionHasInvalidEditShape(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))

	return strings.Contains(text, "invalid_edit_shape") ||
		strings.Contains(text, "requires one edit shape") ||
		strings.Contains(text, "requires one executable edit shape") ||
		strings.Contains(text, "mutually exclusive") ||
		strings.Contains(text, "line-range edits require non-empty content") ||
		strings.Contains(text, "insert_after_line edits require non-empty content") ||
		strings.Contains(text, "expands the selected range") ||
		strings.Contains(text, "does not preserve any selected anchor line")
}

func lastSubmitPlanRejectionBrief(result workspace.ToolResult) string {
	text := strings.TrimSpace(result.Error)
	if text == "" {
		text = strings.TrimSpace(result.Summary)
	}

	if text == "" {
		return ""
	}

	if planThenExecuteSubmitPlanGroundingRejectedResult(result) {
		lower := strings.ToLower(text)
		for _, marker := range []string{". read each existing edit path", ". if the feature is already present", ". if no concrete path can be identified"} {
			if cut := strings.Index(lower, marker); cut > 0 {
				text = strings.TrimSpace(text[:cut]) + "."
				break
			}
		}
	}

	text = strings.Join(strings.Fields(text), " ")

	const limit = 720

	if len(text) > limit {
		const (
			head = 360
			tail = 300
		)

		text = strings.TrimSpace(text[:head]) + " ... " + strings.TrimSpace(text[len(text)-tail:])
	}

	return "Last submit_plan rejection: " + text + " "
}

func lastSubmitPlanCorrectionRejectionBrief(result workspace.ToolResult) string {
	return sanitizeSubmitCorrectionRejectionBrief(lastSubmitPlanRejectionBrief(result))
}

func sanitizeSubmitCorrectionRejectionBrief(text string) string {
	replacements := map[string]string{
		"answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied`, use `source_satisfied_paths` for mixed coverage, or submit real edits":                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "use `source_satisfied_paths` for already-final paths or submit real edits",
		"Answer `Decision: source-satisfied` with current-state evidence when no changes are needed":                                                     "Use `source_satisfied_paths` for already-final paths or submit real edits",
		"If every required path is already final, answer `Decision: source-satisfied` with per-path current-state evidence instead of using submit_plan": "Do not use terminal source-satisfied in submit-correction mode",
		"or answer `Decision: source-satisfied` if every required path is final":                                                                         "or use `source_satisfied_paths` for already-final paths",
		"answer `Decision: source-satisfied` if all paths are final":                                                                                     "use `source_satisfied_paths` for already-final paths",
		"if all paths are final answer `Decision: source-satisfied`; else":                                                                               "",
		"use `Decision: source-satisfied` only if all paths are final; else":                                                                             "",
	}
	for old, replacement := range replacements {
		text = strings.ReplaceAll(text, old, replacement)
	}

	return text
}

func planThenExecuteUnreadRequiredPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]struct{} {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	unread := make(map[string]struct{}, len(required))

	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}

		unread[path] = struct{}{}
	}

	return unread
}

func planThenExecutePendingRequiredSourceEvidence(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []planThenExecuteSourceEvidence {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	pending := make([]planThenExecuteSourceEvidence, 0, len(required))

	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		if !state.requiresSourceBodyRead() {
			continue
		}

		evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, path)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		if evidence.Path == "" {
			evidence.Path = path
		}

		if evidence.Level == planThenExecuteEvidenceNone {
			evidence.Level = planThenExecuteEvidenceNone
			evidence.Detail = "no source evidence"
		}

		pending = append(pending, evidence)
	}

	return pending
}

func planThenExecutePendingRequiredSourceEvidenceSummary(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) string {
	pending := planThenExecutePendingRequiredSourceEvidence(brief, results)
	if len(pending) == 0 {
		return ""
	}

	if max <= 0 || max > len(pending) {
		max = len(pending)
	}

	parts := make([]string, 0, max)
	for _, evidence := range pending[:max] {
		level := string(evidence.Level)
		if level == "" {
			level = "none"
		}

		detail := strings.TrimSpace(evidence.Detail)
		if detail != "" {
			parts = append(parts, evidence.Path+"("+level+": "+detail+")")
		} else {
			parts = append(parts, evidence.Path+"("+level+")")
		}
	}

	if len(pending) > max {
		parts = append(parts, fmt.Sprintf("+%d more", len(pending)-max))
	}

	return strings.Join(parts, ", ")
}

type planThenExecuteRequiredPathTypedState string

const (
	planThenExecuteRequiredPathExistingSourceBodyRequired planThenExecuteRequiredPathTypedState = "existing_source_body_required"
	planThenExecuteRequiredPathNewFileBodyRequired        planThenExecuteRequiredPathTypedState = "new_file_body_required"
	planThenExecuteRequiredPathSourceSatisfied            planThenExecuteRequiredPathTypedState = "source_satisfied"
	planThenExecuteRequiredPathStaleAnchor                planThenExecuteRequiredPathTypedState = "stale_anchor"
	planThenExecuteRequiredPathBlocked                    planThenExecuteRequiredPathTypedState = "blocked"
)

func (state planThenExecuteRequiredPathTypedState) requiresSourceBodyRead() bool {
	switch state {
	case planThenExecuteRequiredPathExistingSourceBodyRequired:
		return true
	default:
		return false
	}
}

func planThenExecuteRequiredPathState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, path string) planThenExecuteRequiredPathTypedState {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return planThenExecuteRequiredPathBlocked
	}

	if planThenExecuteRequiredPathHasExplicitNewFileIntent(brief, path) {
		return planThenExecuteRequiredPathNewFileBodyRequired
	}

	if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path, results) {
		return planThenExecuteRequiredPathNewFileBodyRequired
	}

	if planThenExecuteRequiredPathHasBlockerEvidence(path, results) {
		return planThenExecuteRequiredPathBlocked
	}

	if rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
		_, wholeFileRewrite := submitPlanWholeFileExistingSourceRewritePaths(rejected)[path]
		if _, stale := submitPlanInvalidAnchorPaths(rejected)[path]; stale && !wholeFileRewrite && !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
			return planThenExecuteRequiredPathStaleAnchor
		}
	}

	if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
		return planThenExecuteRequiredPathSourceSatisfied
	}

	return planThenExecuteRequiredPathExistingSourceBodyRequired
}

func planThenExecuteRequiredPathHasExplicitNewFileIntent(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	if planThenExecuteBriefAuthorizesNewFilePath(brief, path) {
		return true
	}

	text := strings.ToLower(planThenExecuteBriefNewFileIntentText(brief))
	if !strings.Contains(text, strings.ToLower(path)) {
		return false
	}

	lowerPath := strings.ToLower(path)
	for _, phrase := range []string{
		"create new file " + lowerPath,
		"create a new file " + lowerPath,
		"create a new file at " + lowerPath,
		"create a new file named " + lowerPath,
		"add new file " + lowerPath,
		"add a new file " + lowerPath,
		"add a new file at " + lowerPath,
		"add a new file named " + lowerPath,
		"write new file " + lowerPath,
		"write a new file " + lowerPath,
		"write a new file at " + lowerPath,
		"write a new file named " + lowerPath,
		"new file " + lowerPath,
		"new file at " + lowerPath,
		"new file named " + lowerPath,
		lowerPath + " as a new file",
		lowerPath + " is a new file",
		lowerPath + " will be a new file",
		lowerPath + " (new file",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	return false
}

func planThenExecuteBriefNewFileIntentText(brief workerruntime.WorkerRuntimeBrief) string {
	return strings.Join([]string{
		brief.Objective,
		brief.RequestedOutcome,
		brief.WriteIntent,
	}, "\n")
}

func planThenExecuteBriefAuthorizesNewFilePath(brief workerruntime.WorkerRuntimeBrief, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, authorized := range brief.AuthorizedNewFiles {
		if NormalizeRunnerPath(authorized) == path {
			return true
		}
	}

	return false
}

func planThenExecuteRequiredPathHasBlockerEvidence(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, result := range results {
		if planThenExecuteResultIsHarnessDiagnostic(result) {
			continue
		}

		if strings.TrimSpace(result.Tool) == "submit_plan" || strings.TrimSpace(result.Tool) == "completion_gate" {
			continue
		}

		text := strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output)
		if !strings.Contains(text, path) {
			continue
		}

		lower := strings.ToLower(text)
		if strings.Contains(lower, "<<question>>") ||
			strings.Contains(lower, "path-specific blocker") ||
			strings.Contains(lower, "blocked required") {

			return true
		}
	}

	return false
}

func planThenExecuteResultIsHarnessDiagnostic(result workspace.ToolResult) bool {
	tool := strings.ToLower(strings.TrimSpace(result.Tool))
	if strings.Contains(tool, "autonomy") ||
		strings.Contains(tool, "gate") ||
		strings.Contains(tool, "guard") {

		return true
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Error + "\n" + result.Output))
	if strings.Contains(text, `"safe_alternatives"`) ||
		strings.Contains(text, `"gate"`) ||
		strings.Contains(text, `"guard_friction"`) ||
		strings.Contains(text, "runner_completion_gate") ||
		strings.Contains(text, "runner_budget_gate") {

		return true
	}

	return false
}

func planThenExecuteRequiredPathStateSummaries(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	out := make([]string, 0, len(required))

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		out = append(out, normalized+":"+string(planThenExecuteRequiredPathState(brief, results, normalized)))
	}

	return out
}

func planThenExecuteToolResultSourceEvidence(result workspace.ToolResult, path string) planThenExecuteSourceEvidence {
	path = NormalizeRunnerPath(path)
	if path == "" || IsNonSourceEvidenceArtifactPath(path) || strings.TrimSpace(result.Error) != "" {
		return planThenExecuteSourceEvidence{}
	}

	text := strings.Join([]string{result.Summary, result.Output}, "\n")
	evidence := planThenExecuteSourceEvidence{Path: path}

	if strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Source = strings.TrimSpace(result.Tool)
		evidence.Detail = "path mentioned by tool result"
	}

	sourcePaths := ReadPathsFromToolResult(result)
	sourceMatched := false
	var sourceFact toolfacts.SourceBodyEvidence
	hasSourceFact := false

	for _, readPath := range sourcePaths {
		if IsNonSourceEvidenceArtifactPath(readPath) {
			continue
		}

		if NormalizeRunnerPath(readPath) == path {
			sourceMatched = true
			if fact, ok := sourceBodyFactForPath(result, path); ok {
				sourceFact = fact
				hasSourceFact = true
			}
			break
		}
	}

	if !sourceMatched {
		return evidence
	}

	tool := strings.TrimSpace(result.Tool)

	evidence.Source = tool
	evidence.Level = planThenExecuteEvidencePathSeen
	evidence.Detail = "typed path seen"

	if !hasSourceFact {
		if planThenExecuteCodeNavigateIsOutline(result) || strings.HasSuffix(tool, "_file_outline") {
			evidence.Detail = "file outline"
		}
		return evidence
	}

	evidence.Level, evidence.Detail = planThenExecuteReadFileEvidenceLevel(sourceFact, path)

	switch {
	case ReadStyleSourceBodyTool(tool):
		// Evidence level already comes from producer-owned source-body facts.
	case tool == "code_navigate" || tool == "read_only_capability":
		if planThenExecuteCodeNavigateIsOutline(result) {
			evidence.Level = planThenExecuteEvidencePathSeen
			evidence.Detail = "file outline"
		} else if sourceFact.Truncated {
			evidence.Level = planThenExecuteEvidenceBodyWindow
			evidence.Detail = "truncated routed source body"
		} else {
			evidence.Level = planThenExecuteEvidenceAnchorReady
			evidence.Detail = "routed source body"
		}
	case strings.HasSuffix(tool, "_file_outline"):
		evidence.Level = planThenExecuteEvidencePathSeen
		evidence.Detail = "file outline"
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		if sourceFact.Truncated {
			evidence.Level = planThenExecuteEvidenceBodyWindow
			evidence.Detail = "truncated symbol/body read"
		} else {
			evidence.Level = planThenExecuteEvidenceAnchorReady
			evidence.Detail = "symbol/body read"
		}
	}

	return evidence
}

// planThenExecuteCodeNavigateIsOutline detects whether a code_navigate /
// read_only_capability result contains file-outline content. It checks the
// output for structured outline markers (fn, struct, enum, impl, trait) and
// for capability: file_outline in the result summary/output. Returns true
// when the output is non-empty and contains outline-like content.
func planThenExecuteCodeNavigateIsOutline(result workspace.ToolResult) bool {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return false
	}
	summary := strings.ToLower(result.Summary)

	// If the tool was called with file_outline capability and output exists,
	// treat it as body-window evidence.
	if strings.Contains(summary, "outline") && !CandidateAnchorOnlyText(result.Output) {
		return true
	}

	// Check output for structured outline declarations common in Rust/Go/etc.
	lower := strings.ToLower(output)
	for _, marker := range []string{"\nfn ", "\nstruct ", "\nenum ", "\nimpl ", "\ntrait "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	// Check for explicit capability marker in output.
	if strings.Contains(lower, "capability: file_outline") {
		return true
	}

	return false
}

func planThenExecuteReadFileEvidenceLevel(fact toolfacts.SourceBodyEvidence, path string) (PlanThenExecuteSourceEvidenceLevel, string) {
	start, end := fact.StartLine, fact.EndLine
	total := fact.TotalLines
	hasRange := sourceBodyFactHasRange(fact)
	hasTotal := total > 0

	if fact.Truncated {
		return planThenExecuteEvidenceBodyWindow, "bounded body window"
	}

	if hasRange && hasTotal && end < total {
		if planThenExecuteGenericHeaderWindow(start, end, total, true) {
			return planThenExecuteEvidenceBodyWindow, "generic first-window read"
		}

		if planThenExecuteLargeGoRawRangeNeedsSymbolBody(path, total) {
			return planThenExecuteEvidenceBodyWindow, "large Go raw range; use symbol/body route"
		}

		// Bounded range: ≤120 lines and not starting at line 1 (header
		// window already excluded above) → precise anchor ready.
		// Also treat ranges that cover ≥90% of the file as anchor ready
		// (covers near-full-file reads without being strictly end >= total).
		if end-start+1 <= planThenExecuteBoundedReadMaxLines {
			return planThenExecuteEvidenceAnchorReady, "bounded source range"
		}
		if hasTotal && total > 0 && float64(end-start+1)/float64(total) >= 0.85 {
			return planThenExecuteEvidenceAnchorReady, "near-full-file bounded range"
		}

		return planThenExecuteEvidenceBodyWindow, "broad source range"
	}

	if hasRange && hasTotal && end >= total {
		return planThenExecuteEvidenceAnchorReady, "whole-file/current-state read"
	}

	if hasRange {
		if planThenExecuteGenericHeaderWindow(start, end, 0, false) {
			return planThenExecuteEvidenceBodyWindow, "generic first-window read without file-size evidence"
		}

		// Bounded range: ≤120 lines and not starting at line 1 → precise anchor ready.
		if end-start+1 <= planThenExecuteBoundedReadMaxLines {
			return planThenExecuteEvidenceAnchorReady, "bounded source range"
		}

		return planThenExecuteEvidenceBodyWindow, "broad source range"
	}

	return planThenExecuteEvidenceBodyWindow, "source body window"
}

const planThenExecuteGenericHeaderReadMaxLines = 220
const planThenExecuteLargeFileGenericHeaderReadMaxLines = 400
const planThenExecuteLargeFileGenericHeaderMinTotalLines = 1000
const planThenExecuteBoundedReadMaxLines = 120

func planThenExecuteLargeGoRawRangeNeedsSymbolBody(path string, total int) bool {
	return total >= planThenExecuteLargeFileGenericHeaderMinTotalLines &&
		strings.EqualFold(filepath.Ext(NormalizeRunnerPath(path)), ".go")
}

func planThenExecuteGenericHeaderWindow(start, end, total int, hasTotal bool) bool {
	if start != 1 || end < start {
		return false
	}

	if end <= planThenExecuteGenericHeaderReadMaxLines {
		return true
	}

	return hasTotal && total >= planThenExecuteLargeFileGenericHeaderMinTotalLines && end <= planThenExecuteLargeFileGenericHeaderReadMaxLines
}

func firstPlanThenExecuteUnreadRequiredPath(required []string, unread map[string]struct{}) string {
	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := unread[normalized]; ok {
			return normalized
		}
	}

	return ""
}

func planThenExecuteRequestTargetPath(req workspace.ToolRequest) string {
	for _, candidate := range []string{req.Path, req.Destination, req.Workdir} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && candidate != "." {
			return candidate
		}
	}

	if path := pathFromPlanThenExecuteHandle(req.Handle); path != "" {
		return path
	}

	if path := pathFromPlanThenExecuteHandle(req.ID); path != "" {
		return path
	}

	return ""
}

func pathFromPlanThenExecuteHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	switch {
	case strings.HasPrefix(handle, "file://"):
		return strings.TrimPrefix(handle, "file://")
	case strings.HasPrefix(handle, "symbol://"):
		rest := strings.TrimPrefix(handle, "symbol://")
		if path, _, ok := strings.Cut(rest, "#"); ok {
			return path
		}

		return rest
	default:
		return ""
	}
}

// preflightPlanThenExecutePlannerToolGuard keeps verification and shell
// affordances out of the planner phase. The planner can gather bounded
// source evidence and submit a plan, report source-satisfied evidence, or
// ask a blocker question; build/test/diff commands belong to the
// orchestrator/review side of the plan-execute workflow.
func (r *Runner) preflightPlanThenExecutePlannerToolGuard(req workspace.ToolRequest) verificationInterceptResult {
	if !r.planThenExecutePlannerPhase() {
		return verificationInterceptResult{}
	}

	tool := strings.TrimSpace(req.Tool)
	if !planThenExecutePlannerForbiddenTool(tool) {
		return verificationInterceptResult{}
	}

	return verificationInterceptResult{
		Intercepted: true,
		Result: workspace.ToolResult{
			Tool:    tool,
			Summary: "plan-then-execute planner phase rejected verification tool",
			Error: "plan-then-execute planner phase does not run shell, git, build, test, source-sanity, delegation, or task-ledger commands. " +
				"Use the current planner terminal contract instead; in submit-correction mode, emit corrected `submit_plan` only.",
		},
		Reason: "plan-then-execute planner phase: verification/shell tool " + tool + " is outside the planner contract",
	}
}

func (r *Runner) planThenExecuteClampedEOFReadRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != "read_file" {
		return "", "", false
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" || req.StartLine <= 0 {
		return "", "", false
	}

	totalLines, ok := priorClampedEOFReadLineCount(path, results)
	if !ok || req.StartLine <= totalLines {
		return "", "", false
	}

	return "Out-of-range read_file rejected",
		"previous read_file evidence already established that " + path + " has " + strconv.Itoa(totalLines) +
			" lines total, so start_line " + strconv.Itoa(req.StartLine) + " is beyond EOF and cannot add new evidence. " +
			"Do not request another out-of-range window. If the gathered source evidence is sufficient, call submit_plan. " +
			"If the source already satisfies the requested change, answer `Decision: source-satisfied` with per-path evidence. " +
			"Otherwise answer <<QUESTION>> and name the exact missing fact.",
		true
}

func (r *Runner) shouldHideToolAfterDiscoveryHardRedirect(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		return false
	}

	if tool == "read_file" && r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 {
		return false
	}

	return true
}

func priorClampedEOFReadLineCount(path string, results []workspace.ToolResult) (int, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0, false
	}

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != "read_file" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		total, ok := clampedEOFReadLineCountForPath(result.Summary, path)
		if ok {
			return total, true
		}
	}

	return 0, false
}

func clampedEOFReadLineCountForPath(summary string, path string) (int, bool) {
	summary = strings.TrimSpace(summary)
	path = NormalizeRunnerPath(path)

	const (
		prefix      = "Read "
		linesMarker = " lines "
		clampMarker = " was clamped to file bounds; file has "
	)

	if !strings.HasPrefix(summary, prefix) || !strings.Contains(summary, clampMarker) {
		return 0, false
	}

	rest := strings.TrimPrefix(summary, prefix)
	linesIdx := strings.Index(rest, linesMarker)

	if linesIdx < 0 {
		return 0, false
	}

	description := strings.TrimSpace(rest[:linesIdx])
	if !readFileSummaryDescriptionMatchesPath(description, path) {
		return 0, false
	}

	clampIdx := strings.Index(summary, clampMarker)
	if clampIdx < 0 {
		return 0, false
	}

	tail := strings.TrimSpace(summary[clampIdx+len(clampMarker):])
	fields := strings.Fields(tail)

	if len(fields) == 0 {
		return 0, false
	}

	total, err := strconv.Atoi(fields[0])
	if err != nil || total <= 0 {
		return 0, false
	}

	return total, true
}

func readFileSummaryDescriptionMatchesPath(description string, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	for _, candidate := range readFileSummaryPathCandidates(description) {
		if NormalizeRunnerPath(candidate) == path {
			return true
		}
	}

	return false
}

func readFileSummaryPathCandidates(description string) []string {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil
	}

	candidates := []string{description}
	fields := strings.Fields(description)

	if len(fields) > 1 && looksLikeWorkspacePathHandle(fields[0]) {
		candidates = append(candidates, strings.TrimSpace(strings.TrimPrefix(description, fields[0])))
	}

	return candidates
}

func looksLikeWorkspacePathHandle(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != 'f' {
		return false
	}

	for _, r := range text[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func (r *Runner) preflightPlanThenExecuteReviewRepairNoopSubmitPlanGuard(req workspace.ToolRequest) verificationInterceptResult {
	if r == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return verificationInterceptResult{}
	}

	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return verificationInterceptResult{}
	}

	edits := reviewRepairSubmitPlanEdits(req)
	if len(edits) == 0 || planEditsHaveNoConcretePath(edits) {
		return reviewRepairNoopSubmitPlanIntercept("no concrete corrective edit paths were provided")
	}

	if r.Workspace != nil && reviewRepairPlanEditsAlreadyApplied(r.Workspace.Root(), edits) {
		return reviewRepairNoopSubmitPlanIntercept("all submitted corrective edits already match the current workspace")
	}

	return verificationInterceptResult{}
}

const planThenExecuteReviewRepairMaxRawReadLines = 200

func (r *Runner) preflightPlanThenExecuteReviewRepairBroadReadGuard(req workspace.ToolRequest) verificationInterceptResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(r.RuntimeBrief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return verificationInterceptResult{}
	}

	if strings.TrimSpace(req.Tool) != "read_file" {
		return verificationInterceptResult{}
	}

	path := pathparse.NormalizeWorkspaceRelativePath(req.Path)
	if path == "" {
		return verificationInterceptResult{}
	}

	start, end := req.StartLine, req.EndLine
	if start > 0 && end > 0 && start > end {
		start, end = end, start
	}

	if start > 0 && end > 0 {
		if end-start+1 <= planThenExecuteReviewRepairMaxRawReadLines {
			return verificationInterceptResult{}
		}

		return reviewRepairBroadReadIntercept(path, fmt.Sprintf("requested %d lines", end-start+1))
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return reviewRepairBroadReadIntercept(path, "unbounded read_file request")
	}

	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return verificationInterceptResult{}
	}

	body, err := os.ReadFile(filepath.Join(root, clean))
	if err != nil {
		return verificationInterceptResult{}
	}

	lines := len(strings.Split(string(body), "\n"))
	if len(body) > 0 && body[len(body)-1] == '\n' {
		lines--
	}

	if lines <= planThenExecuteReviewRepairMaxRawReadLines {
		return verificationInterceptResult{}
	}

	return reviewRepairBroadReadIntercept(path, fmt.Sprintf("unbounded read_file would return %d lines", lines))
}

func reviewRepairBroadReadIntercept(path string, reason string) verificationInterceptResult {
	if strings.TrimSpace(reason) == "" {
		reason = "raw read is too broad for review-repair"
	}

	return verificationInterceptResult{
		Intercepted: true,
		Reason:      "review-repair broad read suppressed: " + reason,
		Result: workspace.ToolResult{
			Tool:    "read_file",
			Summary: "Review-repair broad read rejected",
			Error: strings.Join([]string{
				fmt.Sprintf("review-repair already has Phase Evidence, Diff Evidence, submitted_plan_state, and any inherited context packet; broad raw reads are not valid review evidence (%s).", reason),
				fmt.Sprintf("Use context_status refresh=true, context_query/context_zoom/code_navigate, or read_file with start_line/end_line covering at most %d lines for one named missing checklist fact in %s.", planThenExecuteReviewRepairMaxRawReadLines, path),
				"Then close with `Decision: pass` plus a `Verification Checklist`, a corrective `submit_plan`, or `<<QUESTION>>:`.",
			}, " "),
		},
	}
}

func reviewRepairNoopSubmitPlanIntercept(reason string) verificationInterceptResult {
	return verificationInterceptResult{
		Intercepted: true,
		Reason:      "review-repair submit_plan suppressed: " + reason,
		Result: workspace.ToolResult{
			Tool:    "review_repair_noop_plan",
			Summary: "review-repair submit_plan ignored: no new corrective plan",
			Output: strings.Join([]string{
				"The attempted review-repair submit_plan did not contain a new corrective edit.",
				"If the verification checklist passes, answer with `Decision: pass` and include the `Verification Checklist`.",
				"If a checklist item fails, call `submit_plan` only with concrete new corrective edits for the failing item.",
			}, " "),
		},
	}
}

func reviewRepairNoopSubmitPlanCloseoutRequired(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult) bool {
	return strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily &&
		strings.TrimSpace(req.Tool) == "submit_plan" &&
		strings.TrimSpace(result.Tool) == "review_repair_noop_plan" &&
		strings.TrimSpace(result.Error) == ""
}

func reviewRepairNoopSubmitPlanPassCloseout(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily ||
		strings.TrimSpace(req.Tool) != "submit_plan" ||
		len(reviewRepairSubmitPlanEdits(req)) > 0 {

		return "", false
	}

	rationale := strings.TrimSpace(req.Rationale)
	if rationale == "" ||
		!plancontract.ReviewRepairAnswerDeclaresPass(rationale) ||
		!plancontract.ReviewRepairTextHasChecklist(rationale) ||
		reviewRepairChecklistDeclaresFailingItem(rationale) {

		return "", false
	}

	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "", false
	}

	if _, ok := reviewRepairNoopSubmitPlanEvidencePassCloseout(brief); !ok {
		return "", false
	}

	return normalizeReviewRepairPassCloseout(rationale), true
}

type reviewRepairTerminalCloseout struct {
	Answer       string
	EventRole    string
	EventContent string
	StopReason   string
}

func reviewRepairTerminalCloseoutForRequest(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (reviewRepairTerminalCloseout, bool) {
	if answer, ok := reviewRepairNoopSubmitPlanPassCloseout(brief, req); ok {
		return reviewRepairTerminalCloseout{
			Answer:       answer,
			EventRole:    "plan_then_execute_review_repair_noop_pass",
			EventContent: "review-repair no-op submit_plan carried valid phase/pass evidence; closing with Decision: pass",
			StopReason:   "review_repair_noop_plan_pass",
		}, true
	}

	if answer, ok := reviewRepairTerminalProbePassCloseout(brief, req); ok {
		return reviewRepairTerminalCloseout{
			Answer:       answer,
			EventRole:    "plan_then_execute_review_repair_evidence_pass",
			EventContent: "review-repair requested an unnecessary hidden probe after phase evidence already satisfied pass; closing with Decision: pass",
			StopReason:   "review_repair_evidence_pass_after_hidden_probe",
		}, true
	}

	return reviewRepairTerminalCloseout{}, false
}

func reviewRepairTerminalProbePassCloseout(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecuteReviewRepairTaskFamily {
		return "", false
	}

	if !workspace.ToolSurfacePhaseAllowsTool(req.Tool, workspace.ToolSurfacePhasePlanExecuteReviewRepairTerminal) {
		return "", false
	}

	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "", false
	}

	return reviewRepairNoopSubmitPlanEvidencePassCloseout(brief)
}

func normalizeReviewRepairPassCloseout(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	kept := make([]string, 0, len(lines))

	for _, line := range lines {
		if textutil.EqualTrimFold(line, "Decision: pass") {
			continue
		}

		kept = append(kept, line)
	}

	body := strings.TrimSpace(strings.Join(kept, "\n"))
	if body == "" {
		return "Decision: pass"
	}

	return "Decision: pass\n\n" + body
}

func reviewRepairNoopSubmitPlanEvidencePassCloseout(brief workerruntime.WorkerRuntimeBrief) (string, bool) {
	evidence := strings.ToLower(reviewRepairTrustedPhaseEvidence(brief))
	if evidence == "" {
		return "", false
	}

	for _, required := range []string{
		"phase: review_repair",
		"submitted_plan_state:",
		"status=applied",
		"source_sanity:",
		"verification:",
		"patch_hygiene: status=passed",
	} {
		if !strings.Contains(evidence, required) {
			return "", false
		}
	}

	if !reviewRepairPhaseEvidenceSectionHasStatus(evidence, "source_sanity:", "status=ok") ||
		!reviewRepairPhaseEvidenceSectionHasStatus(evidence, "verification:", "status=ok") {

		return "", false
	}

	if reviewRepairPhaseEvidenceSectionHasEntries(evidence, "open_obligations:") {
		return "", false
	}

	if strings.Contains(evidence, "verification_failed:") ||
		strings.Contains(evidence, "verification_error:") ||
		strings.Contains(evidence, "source_sanity_failed:") ||
		strings.Contains(evidence, "source_sanity_error:") ||
		strings.Contains(evidence, "requested_test_edit_missing") ||
		strings.Contains(evidence, "requested_implementation_edit_missing") {

		return "", false
	}

	changed := nonEmptyRunnerStrings(brief.ChangedPaths, 6)
	if len(changed) == 0 {
		changed = nonEmptyRunnerStrings(brief.AllowedEditPaths, 6)
	}

	pathText := "the changed path(s)"
	if len(changed) > 0 {
		pathText = strings.Join(changed, ", ")
	}

	return strings.Join([]string{
		"Decision: pass",
		"",
		"Verification Checklist:",
		"- Required behavior: submitted_plan_state reports the planned edit applied for " + pathText + ".",
		"- Scope safety: Phase Evidence confines changed paths to the allowed review-repair scope.",
		"- Test or command evidence: Phase Evidence reports source_sanity status=ok, verification status=ok, and patch_hygiene status=passed.",
	}, "\n"), true
}

func reviewRepairPhaseEvidenceSectionHasStatus(evidence, sectionHeader, status string) bool {
	lines := strings.Split(evidence, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != sectionHeader {
			continue
		}

		for _, candidate := range lines[i+1:] {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" {
				continue
			}

			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				return false
			}

			if strings.Contains(trimmed, status) {
				return true
			}
		}
	}

	return false
}

func reviewRepairPhaseEvidenceSectionHasEntries(evidence, sectionHeader string) bool {
	lines := strings.Split(evidence, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != sectionHeader {
			continue
		}

		for _, candidate := range lines[i+1:] {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" {
				continue
			}

			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
				return false
			}

			return true
		}
	}

	return false
}

func reviewRepairSubmitPlanEdits(req workspace.ToolRequest) []planartifact.PlanEdit {
	if len(req.Edits) == 0 && req.PlanArtifact == nil {
		return nil
	}

	edits := append([]planartifact.PlanEdit(nil), req.Edits...)
	if req.PlanArtifact != nil {
		edits = append(edits, req.PlanArtifact.Edits...)
	}

	return edits
}

func reviewRepairChecklistDeclaresFailingItem(text string) bool {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if strings.HasPrefix(line, "-") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		}

		if strings.HasPrefix(line, "failing item:") {
			return true
		}
	}

	return false
}

func reviewRepairNoopSubmitPlanCloseoutPrompt(req workspace.ToolRequest, result workspace.ToolResult) string {
	var b strings.Builder

	b.WriteString("The review-repair `submit_plan` was ignored because it did not contain a new corrective edit. Tools are now disabled. ")
	b.WriteString("Use the review checklist and phase evidence already gathered to emit exactly one terminal response: ")
	b.WriteString("`Decision: pass` with the `Verification Checklist` if the checklist passes, or `<<QUESTION>>:` with the exact blocker if it does not. ")
	b.WriteString("Do not call `submit_plan` again unless there is a new concrete corrective edit, and do not narrate the ignored tool call as the outcome.")

	if rationale := strings.TrimSpace(req.Rationale); rationale != "" {
		b.WriteString("\n\nPrior checklist/rationale from the ignored request:\n")
		b.WriteString(rationale)
	}

	if output := strings.TrimSpace(result.Output); output != "" {
		b.WriteString("\n\nHarness note:\n")
		b.WriteString(output)
	}

	return b.String()
}

func reviewRepairNoopSubmitPlanCloseoutRejection(brief workerruntime.WorkerRuntimeBrief, answer string) (workspace.ToolResult, bool) {
	if planThenExecuteAnswerDeclaresTypedBlocker(answer) {
		return workspace.ToolResult{}, false
	}

	if plancontract.ReviewRepairAnswerDeclaresPass(answer) && plancontract.ReviewRepairTextHasChecklist(answer) {
		if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
			return workspace.ToolResult{
				Tool:    "completion_gate",
				Summary: "Review-repair completion gate rejected contradictory pass",
				Error:   "review-repair phase evidence still has failed obligations " + quotedRunnerStrings(failed) + "; choose a corrective submit_plan inside allowed paths or `<<QUESTION>>:` with the exact blocker instead of `Decision: pass`.",
			}, true
		}

		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    "completion_gate",
		Summary: "Review-repair completion gate rejected final answer",
		Error: strings.Join([]string{
			"review-repair must close with exactly one of:",
			"`Decision: pass` with a `Verification Checklist`,",
			"or `<<QUESTION>>:` naming the exact blocker after a no-op corrective plan.",
		}, " "),
	}, true
}

func reviewRepairNoopSubmitPlanDeterministicCloseout(brief workerruntime.WorkerRuntimeBrief, rejection workspace.ToolResult) string {
	if failed := planThenExecuteReviewRepairFailedPhaseObligations(brief); len(failed) > 0 {
		return "<<QUESTION>>: Review-repair phase evidence still has failed obligations: " + strings.Join(failed, ", ") + ". Submit a corrective plan inside the allowed repair paths or provide updated phase evidence before declaring pass."
	}

	if answer, ok := reviewRepairNoopSubmitPlanEvidencePassCloseout(brief); ok {
		return answer
	}

	errText := strings.TrimSpace(rejection.Error)
	if errText == "" {
		errText = "review-repair closeout did not produce `Decision: pass` with a `Verification Checklist` or a typed blocker"
	}

	return "<<QUESTION>>: " + errText
}

func planEditsHaveNoConcretePath(edits []planartifact.PlanEdit) bool {
	if len(edits) == 0 {
		return true
	}

	for _, edit := range edits {
		if strings.TrimSpace(edit.Path) != "" {
			return false
		}
	}

	return true
}

func reviewRepairPlanEditsAlreadyApplied(root string, edits []planartifact.PlanEdit) bool {
	root = strings.TrimSpace(root)
	if root == "" || len(edits) == 0 {
		return false
	}

	for _, edit := range edits {
		path := pathparse.NormalizeWorkspaceRelativePath(edit.Path)
		if path == "" {
			return false
		}

		bodyBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return false
		}

		if !reviewRepairPlanEditAlreadyApplied(string(bodyBytes), edit) {
			return false
		}
	}

	return true
}

func reviewRepairPlanEditAlreadyApplied(body string, edit planartifact.PlanEdit) bool {
	oldText := edit.OldText
	newText := edit.NewText

	switch {
	case edit.ReplaceAll && oldText == "" && newText != "":
		return body == newText
	case oldText != "" && newText != "":
		return !strings.Contains(body, oldText) && strings.Contains(body, newText)
	case oldText != "" && newText == "":
		return !strings.Contains(body, oldText)
	default:
		return false
	}
}

func planThenExecutePlannerForbiddenTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case "shell", "git_diff", "git_status", "source_sanity", "affected_suites", "context_trace_pack", "task", "task_ledger", "pwd":
		return true
	default:
		return false
	}
}

// planThenExecuteShortCircuitAnswer synthesizes the planner-phase
// final answer from a successful submit_plan tool result, letting the
// runner exit immediately without spending another model call on a
// summary that's redundant with the persisted plan artifact.
//
// Returns ("", false) when the short-circuit doesn't apply (wrong
// lane, no successful submit_plan, etc.). When it returns a non-empty
// answer, the runner should call finish(answer, nil,
// "plan_submitted_short_circuit", false).
//
// Why this exists: the planner's natural step-2 output is a short
// "Plan submitted with N edits across M files: ..." paragraph. That
// summary is already implicit in the plan artifact (the executor
// phase will dispatch each edit and emit per-edit JSON events with
// path + status). Asking the model to retype it costs ~4s and ~200
// tokens for zero new information.
func planThenExecuteShortCircuitAnswer(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest, result workspace.ToolResult) (string, bool) {
	taskFamily := strings.TrimSpace(brief.TaskFamily)
	if taskFamily != PlanThenExecutePlannerTaskFamily && taskFamily != PlanThenExecuteReviewRepairTaskFamily {
		return "", false
	}

	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return "", false
	}

	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return "", false
	}

	if strings.TrimSpace(result.Error) != "" {
		return "", false
	}

	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "Plan submitted; the executor phase will dispatch the planned edits."
	}

	body := []string{
		summary,
		"",
		"The plan artifact is on disk under .idiom/plans/. The orchestrator's executor phase will dispatch each edit deterministically and emit per-edit telemetry. No further planner action required.",
	}
	if details := strings.TrimSpace(result.Output); details != "" {
		body = append(body, "", details)
	}

	return strings.Join(body, "\n"), true
}

func planThenExecuteEmptySubmitPlanRejection(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}

	return strings.TrimSpace(result.Code) == workspace.SubmitPlanEmptyEditsCode
}

func planThenExecuteUnscopedEmptySubmitPlanRejected(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {

		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)

	return ok && planThenExecuteEmptySubmitPlanRejection(rejected)
}

func planThenExecuteUnscopedEmptySubmitPlanAttempt(brief workerruntime.WorkerRuntimeBrief, req workspace.ToolRequest) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		strings.TrimSpace(req.Tool) != "submit_plan" {

		return false
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {

		return false
	}

	return len(planThenExecuteEffectiveSubmitPlanEdits(req)) == 0
}

// successfulSubmitPlanResultCount returns the number of successful
// submit_plan tool results in the lane's recorded history. The
// completion gate uses this as the "the planner did its job" signal
// for plan-then-execute mode, replacing the usual changed-paths check
// (the planner's submit_plan call doesn't change files; the executor
// phase does, in a separate process).
func successfulSubmitPlanResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, r := range results {
		if strings.TrimSpace(r.Tool) != "submit_plan" {
			continue
		}

		if strings.TrimSpace(r.Error) != "" {
			continue
		}

		count++
	}

	return count
}

func (r *Runner) planThenExecuteSourceSatisfiedAnswerCovers(reply string, results []workspace.ToolResult) bool {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return false
	}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		return false
	}

	missing := missingRequiredEditPaths(r.requiredEditCoveragePaths(), newToolResultEvidenceProjection(results).ChangedPathSet())
	if len(missing) == 0 {
		missing = plancontract.SourceSatisfiedEvidencePaths(reply)
	}

	if !plancontract.SourceSatisfiedAnswerCoversPaths(reply, missing) {
		return false
	}

	if r.planThenExecuteRequiredEditSnippetsSourceSatisfied() {
		return true
	}

	// Evidence-ledger check: every required path the model claims as
	// source-satisfied must have a source-body read receipt in the
	// collected results. This prevents stale-evidence claims where
	// source-satisfied is asserted for a path whose content may have
	// changed since the last read.
	//
	// During the anchor-refresh phase, refresh-state paths must have
	// evidence from the current turn (results after SeedResults) to
	// prevent stale seed evidence from satisfying the check.
	if len(missing) > 0 {
		state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
		refreshCutoff := len(r.SeedResults)

		if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
			refreshCutoff = 0
		}

		if !requiredPathsHaveSourceBodyEvidence(missing, results, state.RefreshPaths, refreshCutoff) {
			return false
		}
	}

	return planThenExecuteSourceSatisfiedResultsCoverPaths(missing, results)
}

func planThenExecuteSubmitCorrectionActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	return DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true}).Mode == PlanExecutePlanningModeSubmitCorrection
}

func planThenExecuteSubmitCorrectionBlockerAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}
	if len(planThenExecuteRejectedPlanRefreshPathsNeedingRefreshBeforeBlocker(brief, results, rejected, rejectedAt)) > 0 {
		return false
	}

	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return false
	}

	return len(submitPlanRefreshPathCandidates(rejected)) > 0 || planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected)
}

func planThenExecuteRejectedPlanRefreshPathsNeedingRefreshBeforeBlocker(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	paths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	if len(paths) == 0 {
		return nil
	}

	wholeFileRewritePaths := submitPlanWholeFileExistingSourceRewritePaths(rejected)
	if len(wholeFileRewritePaths) == 0 {
		return paths
	}

	var out []string
	for _, path := range paths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}
		if _, wholeFileRewrite := wholeFileRewritePaths[normalized]; wholeFileRewrite {
			continue
		}
		out = append(out, normalized)
	}

	return uniqueSortedRunnerPaths(out)
}

func planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
	for _, marker := range []string{
		"missing body evidence",
		"missing body/anchor blocker",
		"no safe edit body",
		"would rewrite existing source file",
		"would rewrite the entire existing source file",
		"would collapse",
		"old_text not found",
		"starts a header guard before selecting",
		"invalid edit anchor",
		"unsupported proof claim",
		"no-op assertion",
		"submit a concrete state assertion",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}

func planThenExecuteSubmitCorrectionOutcomeInstruction(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "Submit a corrected parser-readable `submit_plan` request whose coverage includes every required_edit_path with valid executable edits from existing source-body evidence. If the available evidence still cannot support a safe executable edit body, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker. Do not answer `Decision: source-satisfied`, and do not call read/search/discovery in submit-correction mode."
	}

	return "Submit a corrected parser-readable `submit_plan` request whose coverage includes every required_edit_path with either valid executable edits from existing source-body evidence or `source_satisfied_paths` for paths already at final state. Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not call read/search/discovery in submit-correction mode."
}

func planThenExecuteSubmitCorrectionReadyNudgeOutcomeInstruction(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "Do not answer `Decision: source-satisfied`, do not read/search/discovery again, and do not describe the correction in prose outside the parser-readable action. If no safe executable edit body can be produced from the gathered evidence after these rejected submit_plan attempts, answer `<<QUESTION>>:` with the exact path-specific missing body/anchor blocker."
	}

	return "Do not answer `Decision: source-satisfied`, do not answer `<<QUESTION>>:`, do not read/search/discovery again, and do not describe the correction in prose outside the parser-readable action."
}

func planThenExecuteSourceSatisfiedResultsCoverPaths(requiredPaths []string, results []workspace.ToolResult) bool {
	if len(results) == 0 {
		return false
	}

	for _, path := range requiredPaths {
		if planThenExecuteRequiredSourceEvidenceForPath(workerruntime.WorkerRuntimeBrief{}, results, path).Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}

	return true
}

// requiredPathsHaveSourceBodyEvidence is the evidence-ledger check for the
// source-satisfied terminal outcome. It verifies that every required path has
// a source-body read receipt in the provided results, preventing stale-evidence
// claims where the model asserts source-satisfied for a path whose content may
// have changed since the last read.
//
// For freshness-sensitive contexts (anchor-refresh phase), pass a non-empty
// refreshPaths list with a resultCutoff index. Paths in refreshPaths must have
// evidence in results from index resultCutoff onward (i.e., produced during or
// after the refresh). A nil or empty refreshPaths list checks all results.
func requiredPathsHaveSourceBodyEvidence(requiredPaths []string, results []workspace.ToolResult, refreshPaths []string, resultCutoff int) bool {
	if len(requiredPaths) == 0 {
		return false
	}

	refreshSet := make(map[string]struct{}, len(refreshPaths))
	for _, p := range refreshPaths {
		refreshSet[NormalizeRunnerPath(p)] = struct{}{}
	}

	for _, path := range requiredPaths {
		normPath := NormalizeRunnerPath(path)
		scoped := results

		if _, needsFresh := refreshSet[normPath]; needsFresh && resultCutoff > 0 && resultCutoff < len(results) {
			scoped = results[resultCutoff:]
		}

		if planThenExecuteRequiredSourceEvidenceForPath(workerruntime.WorkerRuntimeBrief{}, scoped, normPath).Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}

	return true
}

func (r *Runner) planThenExecuteRequiredEditSnippetsSourceSatisfied() bool {
	if r == nil || r.Workspace == nil {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return false
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return false
	}

	contentCache := make(map[string]string)

	for _, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, nil)
		if len(candidates) == 0 {
			return false
		}

		if !requiredEditSnippetPresent(root, candidates, snippet.Content, contentCache) {
			return false
		}
	}

	return true
}

// planThenExecuteReadyToSubmitNudge returns a system-message string
// the runner injects into the next turn when the planner phase has
// anchor-ready source evidence for every required_edit_path and has not
// yet called submit_plan. The nudge converts the planner's verification urge
// into closeout pressure: instead of letting the model spend more
// reads narrowing anchors that already exist in the brief, it gets a
// directive saying the state is ready and the next action MUST be
// submit_plan or <<QUESTION>>.
//
// Returns "" when the nudge does not apply (wrong lane, plan already
// submitted, paths still unread, etc.). The runner emits the nudge
// at most once per turn-since-coverage; it is the caller's job to
// gate against re-injection on every subsequent tool result.
func planThenExecuteReadyToSubmitNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}

	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")

	if successfulSubmitPlanResultCount(results) > 0 {
		// Plan already submitted — the gate-exemption path will close
		// the runner cleanly; nothing to nudge.
		return ""
	}

	if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok && planThenExecuteSubmitCorrectionActive(brief, results) {
		return strings.Join([]string{
			"PLANNER SUBMIT-CORRECTION READY: every required_edit_path has source-body evidence, but the previous `submit_plan` was rejected before it became a valid plan artifact.",
			"",
			lastSubmitPlanCorrectionRejectionBrief(rejected),
			"The next visible action MUST be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
			planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief, rejected),
			"",
			planThenExecuteSubmitCorrectionReadyNudgeOutcomeInstruction(brief, results),
		}, "\n")
	}

	if len(required) == 0 {
		if len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 {
			return ""
		}

		state := PlanExecutePlanningState{
			EvidencePaths: sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths()),
		}
		paths := planThenExecuteSelectedEditPaths(brief, results, state)

		if len(paths) == 0 {
			return ""
		}

		observed := newToolResultEvidenceProjection(results).ReadPaths()

		var observedSelected []string

		for _, path := range paths {
			normalized := NormalizeRunnerPath(path)
			if _, ok := observed[normalized]; ok {
				observedSelected = append(observedSelected, normalized)
			}
		}

		if len(observedSelected) > 0 {
			paths = uniqueSortedRunnerPaths(observedSelected)
		}

		if len(planThenExecuteHistoryPendingPaths(brief, results, paths)) > 0 {
			return ""
		}

		unreadCandidateLine := "Allowed/candidate paths are not coverage obligations: do not read every allowed path by default."
		readActionLine := "  2. One bounded source-read/search for a different candidate path, but only when it names one concrete edit-safety fact still needed before the plan."
		finalLine := "Do not reread a candidate path that already has source-body evidence before the first valid `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:` blocker."

		return strings.Join([]string{
			"PLANNER READINESS REACHED: at least one selected candidate edit path has source evidence in this turn. This makes `submit_plan` available, but it does not prove every candidate path is complete.",
			"",
			"Selected candidate path(s): " + strings.Join(paths, ", "),
			"",
			unreadCandidateLine,
			"",
			"Next action must be exactly one parser-readable action:",
			"  1. `submit_plan` with only the edit(s) needed by the objective, using insertion, substring, replacement-only line range, or missing/new-file shape, plus a one-sentence purpose per edit.",
			readActionLine,
			"  3. `Decision: source-satisfied` naming the relevant candidate path(s) and observed current-state evidence when the requested final state is already present.",
			"  4. `<<QUESTION>>:` with the exact blocker that prevents drafting the plan.",
			"",
			"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
			"",
			finalLine,
		}, "\n")
	}

	var readyPaths []string

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
		state := planThenExecuteRequiredPathState(brief, results, normalized)

		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			readyPaths = append(readyPaths, normalized)
		default:
			return ""
		}
	}

	if len(readyPaths) == 0 {
		return ""
	}

	if len(planThenExecuteHistoryPendingPaths(brief, results, readyPaths)) > 0 {
		return ""
	}

	return strings.Join([]string{
		"PLANNER READINESS REACHED: every required_edit_path is ready for a terminal planner outcome; existing files have anchor-ready source evidence and missing/new files have explicit new-file evidence.",
		"",
		"Next visible action MUST be exactly one parser-readable `submit_plan` request. " + planThenExecuteSubmitPlanCoverageSentence(),
		"Use the smallest executable edit body that satisfies the objective. Prefer exact `old_text`/`new_text` for small unique replacements; use line-range `content` only for multi-line replacements or large insertions anchored to unchanged source lines. Do not replace whole functions or files when a smaller anchored edit is enough.",
		planThenExecuteSubmitPlanVisibleJSONInstruction() + " For a missing/new source or test file, the brief must provide a substantial inline body or explicit replacement edit shape.",
		"",
		"Narrow exceptions only:",
		"- Use `Decision: source-satisfied` only when every required path already contains the requested final state, and cite current-state evidence for every required path.",
		"- Use `<<QUESTION>>:` only when one exact path-specific blocker prevents drafting any valid `submit_plan` edit.",
		"",
		"Hidden reasoning is not executable evidence. The next action must be visible content or a native tool call.",
		"",
		"Default to submitting `submit_plan` now. Do not read/search/discovery again before the first valid `submit_plan` or narrow terminal exception.",
	}, "\n")
}

func planThenExecuteSubmitPlanOnlyReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")

	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if len(required) == 0 {
		if len(unreadCandidateRecoveryScopePathsForRecovery(brief, results, 8)) > 0 {
			return false
		}

		if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
			state := PlanExecutePlanningState{
				EvidencePaths: sortedRunnerPathKeys(newToolResultEvidenceProjection(results).ReadPaths()),
			}
			selected := planThenExecuteSelectedEditPaths(brief, results, state)

			return len(selected) > 0 && len(planThenExecuteHistoryPendingPaths(brief, results, selected)) == 0
		}

		ready := sortedRunnerPathKeys(planThenExecuteAnchorReadyPaths(brief, results))

		return len(ready) > 0 && len(planThenExecuteHistoryPendingPaths(brief, results, ready)) == 0
	}

	for _, path := range required {
		state := planThenExecuteRequiredPathState(brief, results, path)
		switch state {
		case planThenExecuteRequiredPathSourceSatisfied, planThenExecuteRequiredPathNewFileBodyRequired:
			continue
		default:
			return false
		}
	}

	return len(planThenExecuteHistoryPendingPaths(brief, results, required)) == 0
}

func planThenExecuteCandidateSourcePending(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}

	normalized := workerruntime.NormalizeRuntimeBrief(brief)
	if len(normalized.RequiredEditSnippets) > 0 {
		return false
	}

	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
		return true
	}

	if len(planThenExecuteAnchorReadyPaths(brief, results)) > 0 {
		return false
	}

	return true
}

const planThenExecuteContextDocReadCap = 5

var planThenExecuteRepoRootMarkdownFiles = map[string]struct{}{
	"readme.md":       {},
	"agents.md":       {},
	"contributing.md": {},
	"changelog.md":    {},
}

func isPlanExecuteContextDocPath(path string) bool {
	normalized := strings.ToLower(NormalizeRunnerPath(path))
	if normalized == "" {
		return false
	}

	if strings.HasPrefix(normalized, "docs/") && strings.HasSuffix(normalized, ".md") {
		return true
	}

	if !strings.Contains(normalized, "/") {
		if _, ok := planThenExecuteRepoRootMarkdownFiles[normalized]; ok {
			return true
		}
	}

	return false
}

func planExecuteContextDocReadCount(results []workspace.ToolResult) int {
	seen := make(map[string]struct{})
	for path := range newToolResultEvidenceProjection(results).ReadPaths() {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !isPlanExecuteContextDocPath(normalized) {
			continue
		}
		seen[strings.ToLower(normalized)] = struct{}{}
	}
	return len(seen)
}

func planThenExecuteCandidatePostReadinessDiscoveryRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, plancontract.PlanExecuteRejectionCode, bool) {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily ||
		len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 ||
		successfulSubmitPlanResultCount(results) > 0 ||
		!workspace.IsReadTool(strings.TrimSpace(req.Tool)) ||
		strings.TrimSpace(req.Tool) == "submit_plan" {

		return "", "", plancontract.PlanExecuteRejectionNone, false
	}

	anchorReady := planThenExecuteAnchorReadyPaths(brief, results)
	if len(anchorReady) == 0 {
		return "", "", plancontract.PlanExecuteRejectionNone, false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target != "" {
		if summary, errorText, reject := planThenExecutePostGateOutOfScopeCandidateReadRejection(target, brief, results); reject {
			return summary, errorText, plancontract.PlanExecuteRejectionPostGateReadOutsideCandidateScope, true
		}
	}

	if target != "" && isPlanExecuteContextDocPath(target) &&
		planExecuteContextDocReadCount(results) < planThenExecuteContextDocReadCap {
		return "", "", plancontract.PlanExecuteRejectionNone, false
	}

	allowedCandidateSet := normalizedRequiredEditPathSet(brief.AllowedEditPaths)
	if len(allowedCandidateSet) > 0 {
		if target == "" {
			return "Plan-execute candidate scope required",
				"plan-execute already has source evidence for an explicit allowed candidate path. Further discovery must target an exact allowed candidate file, call `submit_plan`, answer `Decision: source-satisfied`, or answer `<<QUESTION>>:` with the exact blocker; broad candidate discovery is no longer executable edit evidence.",
				plancontract.PlanExecuteRejectionCandidateScopeRequired,
				true
		}

		if _, allowed := allowedCandidateSet[target]; !allowed {
			candidates := sortedRunnerPathKeys(allowedCandidateSet)
			return "Plan-execute read outside allowed candidate scope",
				target + " is not an explicit allowed candidate path for this plan-execute planner turn. Use a bounded source-body read on one allowed candidate path if one concrete edit-safety fact is still missing (" + strings.Join(candidates, ", ") + "), or call `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:` with the exact blocker.",
				plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
				true
		}
	}

	if target != "" && planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief, results, target) && planThenExecuteCodeNavigateMissingAnchorCapability(req) {
		return "Anchor refresh capability required",
			"the previous `submit_plan` named an invalid edit anchor on " + target + ". `" + workspace.ToolNameCodeNavigate + "` anchor refresh requires an explicit source-body capability such as " + strings.Join(workspace.CodeNavigateSourceBodyCapabilityNames(), " or ") + " on the same path and concrete symbol/pattern. " + planThenExecuteAnchorRefreshReadFallbackInstruction([]string{target}),
			plancontract.PlanExecuteRejectionAnchorRefreshCapabilityRequired,
			true
	}

	if target != "" && planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req, brief, results) {
		return "", "", plancontract.PlanExecuteRejectionNone, false
	}

	if target != "" {
		if slices.Contains([]string{workspace.ToolNameHistoryRange, workspace.ToolNameHistorySymbol}, strings.TrimSpace(req.Tool)) && len(planThenExecuteHistoryPendingPaths(brief, results, []string{target})) > 0 {
			return "", "", plancontract.PlanExecuteRejectionNone, false
		}

		if planThenExecuteCandidateSymbolReadAllowed(req, results, target) {
			return "", "", plancontract.PlanExecuteRejectionNone, false
		}

		if planThenExecuteCandidateSamePathContinuationReadAllowed(req, results, target) {
			return "", "", plancontract.PlanExecuteRejectionNone, false
		}

		if _, alreadyReady := anchorReady[target]; !alreadyReady && planThenExecuteCandidateDifferentPathReadAllowed(req, brief, results, target) {
			return "", "", plancontract.PlanExecuteRejectionNone, false
		}

		if readyPath, alreadyReady := planThenExecuteReadyCandidateForRequestTarget(target, anchorReady); alreadyReady &&
			strings.TrimSpace(req.Tool) != "read_file" &&
			!planThenExecuteSourceBodyReadRequest(req) {

			return "Plan-execute candidate source already ready",
				readyPath + " already has source-body evidence for this planner turn. Non-body navigation/search on the same candidate or containing directory is not executable edit evidence. Use `submit_plan` with the gathered source evidence, use a bounded source-body continuation with a concrete named edit-safety gap, read a different candidate path only for one concrete missing edit-safety fact, answer `Decision: source-satisfied`, or answer `<<QUESTION>>:` with the exact blocker.",
				plancontract.PlanExecuteRejectionCandidateSourceAlreadyReady,
				true
		}
	}

	paths := make([]string, 0, len(anchorReady))
	for path := range anchorReady {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	return "", "", plancontract.PlanExecuteRejectionNone, false
}

func planThenExecuteReadyCandidateForRequestTarget(target string, anchorReady map[string]struct{}) (string, bool) {
	target = NormalizeRunnerPath(target)
	if target == "" {
		return "", false
	}

	if _, ok := anchorReady[target]; ok {
		return target, true
	}

	var matches []string
	targetPrefix := strings.TrimSuffix(target, "/") + "/"
	for path := range anchorReady {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if strings.HasPrefix(normalized, targetPrefix) {
			matches = append(matches, normalized)
		}
	}

	sort.Strings(matches)
	if len(matches) == 0 {
		return "", false
	}

	return matches[0], true
}

func planThenExecutePostGateOutOfScopeCandidateReadRejection(target string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, bool) {
	target = NormalizeRunnerPath(target)
	if target == "" || !completionGateResultSeen(results) {
		return "", "", false
	}

	if planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief, results, target) {
		return "", "", false
	}

	candidateSet := normalizedRequiredEditPathSet(brief.AllowedEditPaths)
	if len(candidateSet) == 0 {
		return "", "", false
	}

	if _, ok := candidateSet[target]; ok {
		return "", "", false
	}

	if _, ok := newToolResultEvidenceProjection(results).ReadPaths()[target]; ok {
		return "", "", false
	}

	candidates := sortedRunnerPathKeys(candidateSet)

	return "Plan-execute post-gate read outside candidate scope",
		"the completion gate already rejected a non-terminal planner answer. " + target + " is not an explicit allowed candidate path or prior evidence path for this plan-execute atom. Use a bounded read on one explicit allowed candidate path if one concrete edit-safety fact is still missing (" + strings.Join(candidates, ", ") + "), or call `submit_plan`, `Decision: source-satisfied`, or `<<QUESTION>>:` with the exact blocker.",
		true
}

func planThenExecuteCandidateLowValueAction(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, transport PlanExecutePlanningTransport) (string, string, bool) {
	state := DerivePlanExecutePlanningActionState(brief, results, transport, req)
	if len(state.LowValueActions) == 0 {
		return "", "", false
	}

	reason := state.LowValueActions[0]
	prompt := RenderPlanExecuteRecoveryPlan(planThenExecuteLowValueActionRecoveryPlan(state))

	return reason, prompt, true
}

func planThenExecuteCandidateDifferentPathReadAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, target string) bool {
	target = NormalizeRunnerPath(target)
	if target == "" {
		return false
	}

	for readPath := range newToolResultEvidenceProjection(results).ReadPaths() {
		if NormalizeRunnerPath(readPath) == target {
			return false
		}
	}

	candidateSet := make(map[string]struct{})
	for _, path := range candidateRecoveryScopePaths(brief, results, 8) {
		candidateSet[NormalizeRunnerPath(path)] = struct{}{}
	}

	if _, ok := candidateSet[target]; !ok {
		return false
	}

	return planThenExecuteAnchorRefreshRequestAllowed(req)
}

func planThenExecuteCandidateSymbolReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, target string) bool {
	target = NormalizeRunnerPath(target)
	if target == "" || strings.TrimSpace(req.Tool) != "code_navigate" || !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}

	if strings.TrimSpace(req.Pattern) == "" && strings.TrimSpace(req.Query) == "" {
		return false
	}

	if _, ok := newToolResultEvidenceProjection(results).ReadPaths()[target]; ok {
		return true
	}

	return false
}

func planThenExecuteCandidateSamePathContinuationReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, target string) bool {
	target = NormalizeRunnerPath(target)
	if target == "" || strings.TrimSpace(req.Tool) != "read_file" {
		return false
	}

	if req.StartLine <= 0 && req.EndLine <= 0 {
		for _, readRange := range readRangesForPathFromResults(results, target) {
			if readRange.total > 0 && readRange.total <= 400 && !(readRange.start <= 1 && readRange.end >= readRange.total) {
				return true
			}
		}

		return false
	}

	if !planThenExecuteAnchorRefreshRequestAllowed(req) {
		return false
	}

	for _, readRange := range readRangesForPathFromResults(results, target) {
		if planThenExecuteRequestedRangeCoveredByRead(req.StartLine, req.EndLine, readRange) {
			return false
		}
	}

	return true
}

func planThenExecuteRequestedRangeCoveredByRead(start int, end int, prior planThenExecuteReadRange) bool {
	if start <= 0 {
		start = 1
	}

	if prior.total > 0 {
		if end <= 0 || end > prior.total {
			end = prior.total
		}

		if start > prior.total {
			start = prior.total
		}
	}

	if end <= 0 || end < start {
		return false
	}

	return start >= prior.start && end <= prior.end
}

func planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}

	return planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req, candidateRecoveryScopePaths(brief, results, 8), results)
}

func planThenExecuteCandidateRejectedPlanSourceRefreshTarget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, target string) bool {
	target = NormalizeRunnerPath(target)
	if target == "" || len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 {
		return false
	}

	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if _, invalidAnchor := submitPlanInvalidAnchorPaths(rejected)[target]; !invalidAnchor {
		return false
	}

	scope := normalizedRequiredEditPathSet(candidateRecoveryScopePaths(brief, results, 8))

	_, ok = scope[target]

	return ok
}

func planThenExecuteCodeNavigateMissingAnchorCapability(req workspace.ToolRequest) bool {
	return strings.TrimSpace(req.Tool) == "code_navigate" &&
		strings.TrimSpace(req.Capability) == "" &&
		strings.TrimSpace(req.Pattern) != ""
}

func planThenExecuteSubmitPlanVisibleJSONInstruction() string {
	return "Textual-tool backends must emit exactly one visible JSON object with top-level `\"tool\":\"submit_plan\"` and top-level `edits` as a JSON array, not a quoted string. " + planThenExecuteSubmitPlanCoverageSentence() + " If a previous rejection says an edit would not change a path, move that path to top-level `source_satisfied_paths` only when current evidence shows it is already at final state; otherwise replace the no-op with a real edit. Every edit path must be a real required or selected path from gathered source evidence, and every edit body must be executable final source. Keep edit bodies as small as executable: prefer exact `old_text`/`new_text` for small unique replacements; use numeric `start_line`/`end_line` plus complete `content` for multi-line source/test edits and large additions anchored to unchanged source lines. For small insertions/appends, use `insert_after_line` plus inline non-empty `content` only after an existing line. Do not replace whole functions or files when a smaller anchored edit is enough."
}

func planThenExecuteSubmitPlanCoverageSentence() string {
	return "Cover each required edit path exactly once: put paths needing changes in `edits[]` with one real executable edit object, and put paths already at the requested final state in top-level `source_satisfied_paths`; never create an `edits[]` object whose only purpose is source-satisfied coverage."
}

func planThenExecuteSubmitPlanCorrectionInstructionForBrief(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string

	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}

	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "The rejected edit used an oversized or ref-backed `insert_after_line` body. Do not repeat `insert_after_line` for that edit, and do not use `content_ref` with `insert_after_line`. Emit exactly one visible JSON object with top-level `edits` as a JSON array, using `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range on the real edit path with the unchanged anchor line(s) plus inserted content from existing evidence.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "The rejected plan contained an invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each `edits[]` object must choose exactly one executable shape: `start_line`/`end_line`/non-empty `content` for a line-range replacement, exact `old_text`/`new_text`, small `insert_after_line`/inline non-empty `content`, or `replace_all:true` with full `new_text` for missing/new-file writes only. Existing source/test files must use bounded anchored edits. Do not combine shapes, do not use placeholders, and do not use content refs with `insert_after_line`.")
	} else {
		parts = append(parts, planThenExecuteSubmitPlanVisibleJSONInstruction())
	}

	return strings.Join(parts, " ")
}

func planThenExecuteNativeSubmitPlanCorrectionDetails(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	var parts []string

	if checklist := planThenExecuteSubmitPlanCorrectionChecklist(brief, result); checklist != "" {
		parts = append(parts, checklist)
	}

	if submitPlanRejectionHasLargeInsertAfterLine(result) {
		parts = append(parts, "do not repeat `insert_after_line` for the oversized/ref-backed edit. Use `start_line`/`end_line`/`content` for the large addition by replacing a small current-source anchor range with the unchanged anchor line(s) plus inserted content. `insert_after_line` must have small inline non-empty `content` and cannot use `content_ref`.")
	} else if submitPlanRejectionHasInvalidEditShape(result) {
		parts = append(parts, "Correct the invalid edit shape. "+planThenExecuteSubmitPlanCoverageSentence()+" Each edit must choose exactly one executable shape: line-range replacement, exact substring replacement, small inline insertion, or missing/new-file write. Existing source/test files must use bounded anchored edits. Do not combine shapes or use placeholders.")
	}

	return strings.Join(parts, " ")
}

func planThenExecuteSubmitPlanCorrectionChecklist(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)
	if len(required) == 0 {
		return ""
	}

	missing := nilIfEmptyRunnerPathSet(submitPlanMissingRequiredPaths(result))
	invalid := nilIfEmptyRunnerPathSet(submitPlanInvalidAnchorPaths(result))
	noop := nilIfEmptyRunnerPathSet(submitPlanNoOpEditPaths(result))
	unpathableNoop := submitPlanHasUnpathableNoOpEdit(result) && len(noop) == 0

	var b strings.Builder

	b.WriteString("Next submit_plan coverage checklist:")

	for _, rawPath := range required {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		b.WriteString(" ")
		b.WriteString(path)
		b.WriteString("=")

		switch {
		case noop != nil:
			if _, ok := noop[path]; ok {
				b.WriteString("use-source_satisfied_paths-if-current-state-is-final-or-submit-real-edit")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else if missing != nil {
				if _, ok := missing[path]; ok {
					b.WriteString("missing-entry")
				} else {
					b.WriteString("must-keep-valid-coverage")
				}
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case unpathableNoop:
			b.WriteString("must-have-real-edit-or-source_satisfied_paths-entry-no-noop")
		case missing != nil:
			if _, ok := missing[path]; ok {
				b.WriteString("missing-entry")
			} else if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		case invalid != nil:
			if _, ok := invalid[path]; ok {
				b.WriteString("invalid-anchor-or-syntax")
			} else {
				b.WriteString("must-keep-valid-coverage")
			}
		default:
			b.WriteString("must-have-valid-edit-or-source_satisfied_paths-entry")
		}

		b.WriteString(";")
	}

	return strings.TrimSpace(b.String())
}

func nilIfEmptyRunnerPathSet(paths map[string]struct{}) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}

	return paths
}

func submitPlanNoOpEditPaths(result workspace.ToolResult) map[string]struct{} {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return nil
	}

	text := strings.Join([]string{result.Summary, result.Error, result.Output}, "\n")
	if !strings.Contains(strings.ToLower(text), "edit would not change ") {
		return nil
	}

	paths := make(map[string]struct{})

	for _, line := range strings.Split(text, "\n") {
		remaining := line
		for {
			idx := strings.Index(strings.ToLower(remaining), "edit would not change ")
			if idx < 0 {
				break
			}

			after := remaining[idx+len("edit would not change "):]
			path := strings.TrimSpace(after)

			if cut := strings.Index(path, " after applying prior plan edits"); cut >= 0 {
				path = path[:cut]
			}

			if cut := strings.IndexAny(path, ";,`"); cut >= 0 {
				path = path[:cut]
			}

			if normalized := NormalizeRunnerPath(path); normalized != "" {
				paths[normalized] = struct{}{}
			}

			remaining = after
		}
	}

	if len(paths) == 0 {
		return nil
	}

	return paths
}

func submitPlanHasUnpathableNoOpEdit(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return false
	}

	text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))

	return strings.Contains(text, "old_text and new_text are identical")
}

func planThenExecuteUnreadRequiredSourceNudge(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return ""
	}

	if len(brief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 {
		return ""
	}

	unread := planThenExecuteUnreadRequiredPaths(brief, results)
	if len(unread) == 0 {
		return ""
	}

	next := firstPlanThenExecuteUnreadRequiredPath(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, ""), unread)
	if next == "" {
		return ""
	}

	if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(next, results) {
		return ""
	}

	evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, next)
	state := string(evidence.Level)

	if state == "" {
		state = "none"
	}

	detail := strings.TrimSpace(evidence.Detail)
	if detail != "" {
		state += " (" + detail + ")"
	}

	return strings.Join([]string{
		"REQUIRED SOURCE READ PENDING: a required_edit_path has not produced source-body evidence yet.",
		"",
		"Unread required path: " + next,
		"Current evidence state: " + state,
		"",
		"Next action MUST be exactly one visible source-body tool call for that path before `submit_plan`:",
		"  1. " + workspace.CodeNavigateCapabilityInstruction(semanticlanguage.CapReadSymbol) + " for the relevant declaration in " + next + ", or",
		"  2. `read_file` with explicit `start_line`/`end_line` copied from the outline/search hit.",
		"",
		"Outline and search hits do not satisfy this contract by themselves. Do not call `submit_plan` until every required_edit_path has source-body evidence. Hidden reasoning is not executable evidence.",
	}, "\n")
}

func planThenExecuteRequiredPathHasTerminalMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := NormalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, strings.ToLower(normalized)) {
			continue
		}

		if strings.Contains(text, "new file") ||
			strings.Contains(text, "does not exist yet") ||
			strings.Contains(text, "missing parent") {

			return true
		}
	}

	return false
}

func planThenExecuteAnchorReadyPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})

	for _, path := range planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "") {
		if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}

	if len(paths) > 0 {
		return paths
	}

	for path := range newToolResultEvidenceProjection(results).ReadPaths() {
		if planThenExecuteRequiredSourceEvidenceForPath(brief, results, path).Level == planThenExecuteEvidenceAnchorReady {
			paths[path] = struct{}{}
		}
	}

	return paths
}

type planThenExecuteReadRange struct {
	start int
	end   int
	total int
}

func readRangesForPathFromResults(results []workspace.ToolResult, target string) []planThenExecuteReadRange {
	target = NormalizeRunnerPath(target)
	if target == "" {
		return nil
	}

	ranges := []planThenExecuteReadRange{}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, fact := range sourceBodyFactsFromResult(result) {
			if NormalizeRunnerPath(fact.Path) != target {
				continue
			}

			if readRange, ok := sourceBodyReadRangeFromFact(fact); ok {
				ranges = append(ranges, readRange)
			}
		}
	}

	return ranges
}

const planThenExecuteRejectedSubmitPlanStreakThreshold = 3

func completionGateRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if briefIsScratchpadDeliverableLane(brief) {
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 16384
		}

		return opts
	}

	if strings.TrimSpace(brief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		opts = forceVisibleActionChatOptions(opts)
		if opts.MaxTokens <= 0 {
			opts.MaxTokens = 8192
		}

		return opts
	}

	if planThenExecutePlannerBrief(brief) {
		surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
		if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh || surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
			return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
		}

		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}

	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily &&
		runnerBriefRequiresMutation(brief) &&
		len(brief.RequiredEditPaths) == 0 &&
		len(newToolResultEvidenceProjection(results).ChangedPathSet()) == 0 &&
		concreteSourceEvidenceAlreadyRead(results) &&
		mutationNoWriteCompletionGateRejectionCount(results) >= 2 {

		opts = forceVisibleActionChatOptions(opts)
		opts = planExecuteSetExactMaxTokens(opts, 768)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

		return opts
	}

	if runnerBriefRequiresMutation(brief) && len(brief.RequiredEditPaths) > 0 &&
		len(missingRequiredEditPaths(brief.RequiredEditPaths, newToolResultEvidenceProjection(results).ChangedPathSet())) > 0 {

		opts = implementationRequiredPathForceVisibleActionChatOptions(opts)
		if !supportsNativeTools && implementationRequiredPathAnchorsReady(brief, results) {
			opts = forceVisibleActionChatOptions(opts)
		}

		limit := implementationRequiredPathRecoveryTokenLimit(brief, results)

		opts = planExecuteSetExactMaxTokens(opts, limit)

		return opts
	}

	limit := 1536
	if runnerBriefRequiresMutation(brief) {
		limit = 1024
		if len(brief.RequiredEditPaths) == 0 {
			limit = 2048
		}

		if len(brief.RequiredEditPaths) == 0 && lastNonGateTool(results) == workspace.ToolNameReadFile && successfulToolSeen(results, workspace.ToolNameReadFile) {
			// Keep the model's current thinking capability available for
			// the recovery response, but strip prior hidden reasoning from
			// the next request. For local Qwen backends, forcing thinking
			// off here can produce an empty visible response; preserving
			// current-response reasoning still lets parser-readable hidden
			// tool calls be salvaged.
			opts.PreserveThinking = false
			limit = 1024
		} else if len(brief.RequiredEditPaths) == 0 && textutil.EqualTrimFold(opts.ToolChoice, "required") {
			opts.ToolChoice = "auto"
		}
	}

	if opts.MaxTokens <= 0 || opts.MaxTokens > limit {
		opts.MaxTokens = limit
	}

	return opts
}

func implementationRequiredPathForceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

	return opts
}

func implementationRequiredPathAnchorsReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if len(brief.RequiredEditPaths) == 0 {
		return false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	for _, path := range brief.RequiredEditPaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := changed[normalized]; ok {
			continue
		}

		evidence := planThenExecuteRequiredSourceEvidenceForPath(brief, results, normalized)
		if evidence.Level != planThenExecuteEvidenceAnchorReady {
			return false
		}
	}

	return true
}

func implementationRequiredPathRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	if implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}

	return 4096
}

func implementationRequiredPathRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	var resultText strings.Builder

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			continue
		}

		resultText.WriteString(result.Summary)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Error)
		resultText.WriteByte('\n')
		resultText.WriteString(result.Output)
		resultText.WriteByte('\n')
	}

	return recoveryNeedsLargeBodyBudgetText(brief, resultText.String())
}

func planThenExecuteCandidatePostReadChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) {
		return opts
	}

	if planThenExecuteUnreadRequiredSourceNudge(brief, results) != "" {
		return planThenExecuteUnreadRequiredSourceChatOptions(opts, brief, results)
	}

	readEvidenceSeen := successfulToolSeen(results, workspace.ToolNameReadFile) || lastNonGateTool(results) == workspace.ToolNameReadFile || len(newToolResultEvidenceProjection(results).ReadPaths()) > 0
	if len(brief.RequiredEditPaths) > 0 {
		if !readEvidenceSeen {
			if opts.MaxTokens <= 0 {
				opts.MaxTokens = 16384
			}

			return opts
		}

		if !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
			opts = forceVisibleActionChatOptions(opts)
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

			return planExecuteCapMaxTokens(opts, 4096)
		}

		opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)

		limit := planThenExecuteRecoveryTokenLimit(brief, results)

		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			limit = 16384
		}

		if opts.MaxTokens <= 0 || opts.MaxTokens < limit || opts.MaxTokens > limit {
			opts.MaxTokens = limit
		}

		return opts
	}

	if readEvidenceSeen {
		opts = forceVisibleActionChatOptions(opts)
		if planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
			opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
			if supportsNativeTools {
				if planThenExecuteCandidateScopeFullyRead(brief, results, 4) && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
					opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
					opts.ToolChoice = "required"
				} else {
					opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
				}
			}
		} else {
			opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
		}
	}

	opts = planExecuteCapMaxTokens(opts, 4096)

	return opts
}

func planThenExecuteRecoveryNeedsLargeBodyBudget(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return recoveryNeedsLargeBodyBudgetText(brief, planThenExecuteRecoveryResultText(results))
}

func recoveryNeedsLargeBodyBudgetText(brief workerruntime.WorkerRuntimeBrief, resultText string) bool {
	text := strings.ToLower(strings.Join([]string{
		brief.Objective,
		strings.Join(brief.RequiredEditPaths, "\n"),
		resultText,
	}, "\n"))
	for _, phrase := range []string{"create new file", "add new file", "write new file", "whole-file", "replace_all", "full file body", "new file", "missing parent"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	for _, path := range brief.RequiredEditPaths {
		lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if lowerPath == "" {
			continue
		}

		base := filepath.Base(lowerPath)
		if strings.Contains(lowerPath, "/new/") || strings.HasPrefix(base, "new_") {
			return true
		}

		if strings.Contains(text, lowerPath) &&
			(strings.Contains(text, " create ") || strings.HasPrefix(text, "create ") ||
				strings.Contains(text, " add ") || strings.HasPrefix(text, "add ") ||
				strings.Contains(text, " write ") || strings.HasPrefix(text, "write ")) {

			return true
		}
	}

	return false
}

func planThenExecuteRecoveryResultText(results []workspace.ToolResult) string {
	var b strings.Builder

	for _, result := range results {
		b.WriteString(result.Summary)
		b.WriteByte('\n')
		b.WriteString(result.Error)
		b.WriteByte('\n')
		b.WriteString(result.Output)
		b.WriteByte('\n')
	}

	return b.String()
}

func (r *Runner) planThenExecuteSubmitPlanParseRecoveryPlan(raw string, parseErr error, results []workspace.ToolResult) (PlanExecuteRecoveryPlan, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || parseErr == nil {
		return PlanExecuteRecoveryPlan{}, false
	}

	stringifiedEdits := submitPlanEditsStringParseError(raw, parseErr)
	if !stringifiedEdits && !planThenExecuteSubmitPlanOnlyReady(r.RuntimeBrief, results) {
		return PlanExecuteRecoveryPlan{}, false
	}

	if planThenExecuteReadyToSubmitNudge(r.RuntimeBrief, results) == "" {
		return PlanExecuteRecoveryPlan{}, false
	}

	required := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 12)

	return planThenExecuteSubmitPlanParseRecoveryPlan(stringifiedEdits, required), true
}

func submitPlanEditsStringParseError(raw string, parseErr error) bool {
	if parseErr == nil {
		return false
	}

	lower := strings.ToLower(parseErr.Error() + "\n" + raw)

	return strings.Contains(lower, "toolrequest.edits") && strings.Contains(lower, "string")
}

func (r *Runner) emitPlanExecuteSubmitPlanParseRecoveryEvent(plan PlanExecuteRecoveryPlan, content string) {
	if r == nil || plan.Kind != PlanExecuteRecoveryKindSubmitPlanParse {
		return
	}

	r.emitEvent(RunEvent{Role: "plan_then_execute_submit_plan_parse_recovery", Content: strings.TrimSpace(content)})
}

func (r *Runner) emitPlanExecuteSubmitPlanParseRecoveryEventForParseFailure(raw string, parseErr error) {
	if r == nil || !r.planThenExecutePlannerPhase() || !submitPlanEditsStringParseError(raw, parseErr) {
		return
	}

	r.emitEvent(RunEvent{
		Role:    "plan_then_execute_submit_plan_parse_recovery",
		Content: "submit_plan parse recovery observed: edits was emitted as a quoted non-array string and did not count as a plan artifact",
	})
}

func planThenExecuteNativeSubmitPlanNudge(brief workerruntime.WorkerRuntimeBrief) string {
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)

	var b strings.Builder

	b.WriteString("PLANNER READINESS REACHED: every required_edit_path has anchor-ready source evidence in this turn.\n\n")
	b.WriteString("This backend now exposes only the native `submit_plan` tool for the terminal planner action. Call `submit_plan` now. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString(" Do not answer in prose.")

	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}

	return b.String()
}

func planThenExecuteNativeSubmitPlanCorrectionPrompt(brief workerruntime.WorkerRuntimeBrief, result workspace.ToolResult) string {
	return RenderPlanExecuteRecoveryPlan(planThenExecuteNativeSubmitPlanCorrectionRecoveryPlan(brief, result))
}

func (r *Runner) planThenExecuteNativeSubmitPlanNoCallFallbackPrompt(modelErr error, opts llama.ChatOptions, results []workspace.ToolResult, used int) (string, bool) {
	if r == nil || r.Client == nil || modelErr == nil || used > 0 || !r.planThenExecutePlannerPhase() {
		return "", false
	}

	if !planThenExecuteTerminalSubmitPlanAllowed(r.RuntimeBrief, results) {
		return "", false
	}

	detail := strings.TrimSpace(modelErr.Error())
	lower := strings.ToLower(detail)

	if !strings.Contains(lower, "tool_choice") || !strings.Contains(lower, "required") || !strings.Contains(lower, "did not produce any tool calls") {
		return "", false
	}

	required := nonEmptyRunnerStrings(r.RuntimeBrief.RequiredEditPaths, 12)

	return RenderPlanExecuteRecoveryPlan(planThenExecuteNativeNoCallFallbackRecoveryPlan(required)), true
}

func planThenExecuteReadyToSubmitChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh, PlanExecuteRecoverySurfaceHistoryPending:
		return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	case PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		return planThenExecuteSourceSatisfiedTerminalChatOptions(opts, brief, results)
	}

	if planThenExecuteReadyToSubmitNudge(brief, results) == "" {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}

	if len(brief.RequiredEditPaths) == 0 && !planThenExecuteTerminalSubmitPlanAllowed(brief, results) {
		return planThenExecuteCandidatePostReadChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}

	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
	}

	if supportsNativeTools && chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		opts.ToolChoice = "required"
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSourceSatisfiedTerminalChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts = forceVisibleActionChatOptions(opts)
	opts.Tools = nil
	opts.ToolChoice = ""
	opts.ForceNativeTools = false
	opts.ResponseFormatJSON = false

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSourceSatisfiedTerminalRequiredActive(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			return false
		}

		return planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result)
	}

	return false
}

func planThenExecuteTextualSubmitPlanFallbackChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, false)

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteUnreadRequiredSourceChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	if planThenExecuteUnreadRequiredSourceNudge(brief, results) == "" {
		return opts
	}

	opts = forceVisibleActionChatOptions(opts)
	if chatOptionsExposeAnyTool(opts, workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate) {
		opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate)
		opts.ToolChoice = "required"
	}

	opts = planExecuteCapMaxTokens(opts, 768)

	return opts
}

func planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 {
		return opts
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}

	if len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 && !successfulToolSeen(results, workspace.ToolNameReadFile) && lastNonGateTool(results) != workspace.ToolNameReadFile {
		return opts
	}

	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		return opts
	}

	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
		opts.ToolChoice = "required"
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteRecoveryTokenLimit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	requiredPathCount := len(normalizedRequiredEditPathSet(brief.RequiredEditPaths))
	if planThenExecuteRejectedPlanCorrectionOnly(brief, results) {
		if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
			return 16384
		}

		if requiredPathCount == 0 {
			return 16384
		}

		// Multi-file textual submit_plan output serializes full edit
		// bodies into one JSON object. Four-path source+test plans have
		// hit the 8192-token ceiling mid-JSON, so keep the larger budget
		// scoped to terminal plan synthesis/correction.
		if requiredPathCount >= 4 {
			return 16384
		}

		if requiredPathCount >= 3 {
			return 8192
		}

		return 1024
	}

	if planThenExecuteRecoveryNeedsLargeBodyBudget(brief, results) {
		return 16384
	}

	if lastNonGateTool(results) == workspace.ToolNameSubmitPlan {
		return 4096
	}

	if requiredPathCount >= 4 {
		return 16384
	}

	if requiredPathCount >= 3 {
		return 8192
	}

	return 4096
}

func planThenExecuteRejectedPlanCorrectionOnly(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if strings.TrimSpace(brief.TaskFamily) != PlanThenExecutePlannerTaskFamily {
		return false
	}

	if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); !ok {
		return false
	}

	if planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results) {
		return false
	}

	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	return !planThenExecuteRejectedPlanNeedsSourceRefresh(brief, results)
}

func planThenExecuteSourceSatisfiedRejectionAfterCoverageRejection(results []workspace.ToolResult) bool {
	lastCoverage := -1
	lastSourceSatisfied := -1

	for i, result := range results {
		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			lastSourceSatisfied = i
			continue
		}

		if planThenExecuteSubmitPlanRejectedResult(result) {
			lastCoverage = i
		}
	}

	return lastCoverage >= 0 && lastSourceSatisfied > lastCoverage
}

func planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts)
	}

	surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
	if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
		opts = forceVisibleActionChatOptions(opts)

		refreshTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteAnchorRefresh)

		if chatOptionsExposeAnyTool(opts, refreshTools...) {
			opts.Tools = filterChatOptionsTools(opts.Tools, refreshTools...)
			opts.ToolChoice = "required"
		} else {
			opts.Tools = nil
			opts.ToolChoice = ""
			opts.ForceNativeTools = false
			opts.ResponseFormatJSON = false
		}

		opts = planExecuteSetExactMaxTokens(opts, planThenExecuteAnchorRefreshMaxTokens)

		return opts
	}

	if surface.Kind == PlanExecuteRecoverySurfaceHistoryPending {
		opts = forceVisibleActionChatOptions(opts)

		historyTools := workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteHistoryPending)

		if chatOptionsExposeAnyTool(opts, historyTools...) {
			opts.Tools = filterChatOptionsTools(opts.Tools, historyTools...)
			opts.ToolChoice = "required"
		} else {
			opts.Tools = nil
			opts.ToolChoice = ""
			opts.ForceNativeTools = false
			opts.ResponseFormatJSON = false
		}

		opts = planExecuteSetExactMaxTokens(opts, 1024)

		return opts
	}

	if surface.Kind == PlanExecuteRecoverySurfaceSourceSatisfiedTerminal {
		return planThenExecuteSourceSatisfiedTerminalChatOptions(opts, brief, results)
	}

	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) == 0 {
		return planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
	}

	return planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, supportsNativeTools)
}

func planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	opts.ForceNativeTools = false
	opts.ResponseFormatJSON = false
	opts.EnableThinking = nil
	opts.ChatTemplateKwargs = false
	opts.PreserveThinking = false
	opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)
	opts = planExecuteCapMaxTokens(opts, 4096)

	return opts
}

func planThenExecuteRejectedPlanNeedsSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	return len(planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)) > 0
}

func planThenExecuteRejectedPlanRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	if paths := planThenExecuteRejectedPlanRefreshPathsForResult(brief, results, rejected, rejectedAt); len(paths) > 0 {
		return paths
	}

	if planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief, rejected, results) {
		return nil
	}

	return planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief, results)
}

func planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief workerruntime.WorkerRuntimeBrief, rejected workspace.ToolResult, results []workspace.ToolResult) bool {
	if !planThenExecutePathlessUnsafeSubmitPlanRejectionExhausted(rejected, results) {
		return false
	}

	return len(planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief, results)) == 0
}

func planThenExecutePathlessUnsafeSubmitPlanRejectionExhausted(rejected workspace.ToolResult, results []workspace.ToolResult) bool {
	if len(submitPlanRefreshPathCandidates(rejected)) > 0 {
		return false
	}
	if !planThenExecuteSubmitPlanMissingBodyOrUnsafeAnchor(rejected) {
		return false
	}

	return len(planThenExecuteRejectedSubmitPlanStreak(results)) >= planThenExecuteRejectedSubmitPlanStreakThreshold
}

func planThenExecuteLastRejectionSuppressesStreakRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	return planThenExecuteSuppressRejectedSubmitPlanStreakRefresh(brief, rejected, results)
}

func planThenExecuteRejectedPlanRefreshPathsForResult(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	refreshPaths := submitPlanRefreshPathCandidates(rejected)

	var out []string

	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, ok := refreshPaths[path]; !ok {
			continue
		}

		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}

		out = append(out, path)
	}

	return out
}

func submitPlanRefreshPathCandidates(rejected workspace.ToolResult) map[string]struct{} {
	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}

	return refreshPaths
}

func planThenExecuteRejectedSubmitPlanStreak(results []workspace.ToolResult) []int {
	var indices []int

	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSubmitPlan {
			continue
		}

		if strings.TrimSpace(result.Error) == "" {
			break
		}

		if planThenExecuteSourceSatisfiedSubmitPlanRejectedResult(result) {
			continue
		}

		if !planThenExecuteSubmitPlanRejectedResult(result) {
			continue
		}

		indices = append(indices, i)
	}

	slices.Reverse(indices)

	return indices
}

func planThenExecuteRejectedSubmitPlanStreakInvalidPaths(results []workspace.ToolResult) map[string]struct{} {
	out := map[string]struct{}{}
	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return out
	}

	for _, index := range streak {
		for path := range submitPlanRefreshPathCandidates(results[index]) {
			if normalized := NormalizeRunnerPath(path); normalized != "" {
				out[normalized] = struct{}{}
			}
		}
	}

	return out
}

func planThenExecuteRejectedSubmitPlanStreakRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	streak := planThenExecuteRejectedSubmitPlanStreak(results)
	if len(streak) < planThenExecuteRejectedSubmitPlanStreakThreshold {
		return nil
	}

	pathSet := map[string]struct{}{}
	for _, index := range streak {
		rejected := results[index]
		for _, path := range planThenExecuteRejectedPlanRefreshPathsForResult(brief, results, rejected, index) {
			if normalized := NormalizeRunnerPath(path); normalized != "" {
				pathSet[normalized] = struct{}{}
			}
		}
	}

	return sortedRunnerPathKeys(pathSet)
}

func planThenExecuteRefreshScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) []string {
	required := planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")
	if len(required) > 0 {
		return required
	}

	return candidateRecoveryScopePaths(brief, results, 16)
}

func planThenExecuteRefreshScopePathsForRejectedPlan(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, refreshPaths map[string]struct{}) []string {
	paths := planThenExecuteRefreshScopePaths(brief, results)
	if len(planThenExecuteRequiredCoveragePaths(brief.RequiredEditPaths, "")) > 0 || len(refreshPaths) == 0 {
		return paths
	}

	seen := normalizedRequiredEditPathSet(paths)

	for path := range refreshPaths {
		normalized := NormalizeRunnerPath(path)
		if !planThenExecuteRecoveryScopePathAllowed(normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	return paths
}

func planThenExecuteAnchorRefreshAfterRejectedSubmitPlanPrompt(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) string {
	refreshPaths := planThenExecuteRejectedPlanRefreshPaths(brief, results, rejected, rejectedAt)
	target := "the stale or missing required edit path named by the submit_plan rejection"

	if len(refreshPaths) > 0 {
		target = strings.Join(refreshPaths, ", ")
	}

	return strings.Join([]string{
		"The previous `submit_plan` was rejected before it became a valid plan artifact. " + lastSubmitPlanRejectionBrief(rejected),
		"This is an anchor-refresh phase, not a submit-plan phase. The corrected plan cannot be valid until source evidence is refreshed for: " + target + ".",
		"Your next visible message must be exactly one parser-readable bounded source-body request for one of those paths. " + planThenExecuteAnchorRefreshReadFallbackInstruction(refreshPaths),
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or reply in prose until the bounded refresh returns.",
	}, "\n\n")
}

func planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) == 0 {
		return opts
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}

	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		return opts
	}

	opts = planThenExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools {
		opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
		opts.ToolChoice = "required"
	}

	limit := planThenExecuteRecoveryTokenLimit(brief, results)

	opts = planExecuteSetExactMaxTokens(opts, limit)

	return opts
}

func planThenExecuteSubmitPlanTerminalChatOptions(opts llama.ChatOptions, supportsNativeTools bool) llama.ChatOptions {
	opts = planExecuteSubmitPlanTerminalChatOptions(opts, supportsNativeTools)
	if supportsNativeTools && !chatOptionsExposeTool(opts, workspace.ToolNameSubmitPlan) {
		opts.Tools = nil
		opts.ToolChoice = ""
		opts.ForceNativeTools = false
		opts.ResponseFormatJSON = true
	}

	return opts
}

func planThenExecuteTerminalSubmitPlanAllowed(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return false
	}

	if planThenExecuteSubmitPlanHistoryMissing(brief, results) {
		return false
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TerminalOnly: true})
	if !slices.Contains(state.TerminalOptions, workspace.ToolNameSubmitPlan) {
		return false
	}

	if state.Mode == PlanExecutePlanningModeSubmitReady || state.Mode == PlanExecutePlanningModeSubmitCorrection {
		return true
	}

	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		allowed := normalizedRequiredEditPaths(brief.AllowedEditPaths)
		return len(allowed) == 1 && len(state.SelectedEditPaths) > 0
	}

	if len(unreadCandidateRecoveryScopePathsForRecovery(brief, results, 8)) > 0 {
		return false
	}

	return len(state.SelectedEditPaths) > 0
}

func planThenExecuteTerminalSubmitPlanAllowedLegacy(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !planThenExecutePlannerBrief(brief) || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	if len(brief.RequiredEditPaths) > 0 {
		return planThenExecuteReadyToSubmitNudge(brief, results) != ""
	}

	if len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) == 0 {
		return false
	}

	if !(successfulToolSeen(results, workspace.ToolNameReadFile) || len(newToolResultEvidenceProjection(results).ReadPaths()) > 0 || lastNonGateTool(results) == workspace.ToolNameReadFile) {
		return false
	}

	return planThenExecuteCandidateScopeFullyRead(brief, results, 4)
}

func planThenExecuteOpenCandidateSourceCanSubmit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 || len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		return false
	}

	return len(newToolResultEvidenceProjection(results).ReadPaths()) > 0
}

func planThenExecuteCandidateScopeFullyRead(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) bool {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return true
	}

	read := newToolResultEvidenceProjection(results).ReadPaths()
	if len(read) == 0 {
		return false
	}

	for _, path := range candidates {
		if _, ok := read[NormalizeRunnerPath(path)]; !ok {
			return false
		}
	}

	return true
}

func forceVisibleActionChatOptions(opts llama.ChatOptions) llama.ChatOptions {
	thinkingOff := false

	opts.EnableThinking = &thinkingOff
	opts.ChatTemplateKwargs = true
	opts.PreserveThinking = false

	return opts
}

func planThenExecuteTerminalAwareToolChoice(current string) string {
	if strings.TrimSpace(current) == "" || textutil.EqualTrimFold(current, "required") {
		return "auto"
	}

	return current
}

func chatOptionsExposeTool(opts llama.ChatOptions, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	for _, tool := range opts.Tools {
		if strings.TrimSpace(tool.Function.Name) == name {
			return true
		}
	}

	return false
}

func chatOptionsExposeAnyTool(opts llama.ChatOptions, names ...string) bool {
	for _, name := range names {
		if chatOptionsExposeTool(opts, name) {
			return true
		}
	}

	return false
}

func filterChatOptionsTools(tools []llama.Tool, names ...string) []llama.Tool {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}

	filtered := make([]llama.Tool, 0, len(tools))
	for _, tool := range tools {
		if _, ok := allowed[strings.TrimSpace(tool.Function.Name)]; ok {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func (r *Runner) deterministicPlanExecuteRequiredSourceRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 || lastNonGateTool(results) == workspace.ToolNameSubmitPlan {
		return workspace.ToolRequest{}, "", false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	next := ""

	for _, path := range r.requiredEditCoveragePaths() {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := unread[normalized]; !ok {
			continue
		}

		if planThenExecuteRequiredPathHasTerminalMissingFileEvidence(normalized, results) || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicPlanExecuteRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}

			continue
		}

		next = normalized

		break
	}

	if next == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            next,
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "source-body evidence for required plan-execute edit path",
		WhyNotAnswering: "need current source before submit_plan can anchor the required edit",
	}

	return req, marshalToolRequestRaw(req), true
}

type planThenExecuteRequiredSourceSurfaceAction struct {
	Request  workspace.ToolRequest
	Raw      string
	Summary  string
	Error    string
	Redirect bool
}

func (r *Runner) planThenExecuteRequiredSourceSurfaceAction(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (planThenExecuteRequiredSourceSurfaceAction, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if len(unread) == 0 {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	if redirected, raw, ok := r.deterministicPlanExecuteRequiredSourceRead(results); ok &&
		slices.Contains(allowedTools, redirected.Tool) &&
		normalizedToolRequestRaw(redirected) != normalizedToolRequestRaw(req) {

		target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(redirected))
		if _, missing := unread[target]; missing {
			evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
			if planThenExecuteRequiredSourceRequestAdvances(redirected, target, evidence, results) {
				return planThenExecuteRequiredSourceSurfaceAction{
					Request:  redirected,
					Raw:      raw,
					Redirect: true,
				}, true
			}
		}
	}

	summary, errorText, reject := planThenExecuteRequiredPathDiscoveryRejection(req, r.RuntimeBrief, results)
	if !reject {
		return planThenExecuteRequiredSourceSurfaceAction{}, false
	}

	return planThenExecuteRequiredSourceSurfaceAction{
		Summary: summary,
		Error:   errorText,
	}, true
}

func (r *Runner) deterministicPlanExecuteRejectedRequiredSourceRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}

	if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) == "" {
		return workspace.ToolRequest{}, "", false
	}

	return r.deterministicPlanExecuteRequiredSourceRead(results)
}

func (r *Runner) deterministicImplementationRejectedRequiredPathRead(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolRequest{}, "", false
	}

	remaining := r.remainingRequiredEditPaths(results)
	if !scopedImplementationUsesBoundedSourceEdits(r.RuntimeBrief, remaining) {
		return workspace.ToolRequest{}, "", false
	}

	if implementationRequiredPathAnchorsReady(r.RuntimeBrief, results) {
		return workspace.ToolRequest{}, "", false
	}

	if !implementationRejectedTextCanAdvanceRequiredRead(trimmed) {
		return workspace.ToolRequest{}, "", false
	}

	return r.deterministicImplementationRequiredPathRead(results)
}

func implementationRejectedTextCanAdvanceRequiredRead(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) + " "

	return !ContainsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	)
}

func (r *Runner) deterministicImplementationRequiredPathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !runnerBriefRequiresMutation(r.RuntimeBrief) || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if len(r.remainingRequiredEditPaths(results)) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	changed := newToolResultEvidenceProjection(results).ChangedPathSet()

	for _, path := range r.requiredEditCoveragePaths() {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := changed[normalized]; ok {
			continue
		}

		evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, normalized)
		if evidence.Level == planThenExecuteEvidenceAnchorReady {
			continue
		}

		if evidence.Level != planThenExecuteEvidenceNone || planThenExecutePathHasAnyToolEvidence(normalized, results) {
			if req, ok := r.deterministicImplementationRequiredSourceFollowup(normalized, results); ok {
				return req, marshalToolRequestRaw(req), true
			}

			if alreadyReadPath(results, normalized) {
				continue
			}
		}

		req := workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            normalized,
			StartLine:       1,
			EndLine:         220,
			NamedGap:        "exact current source needed before editing this required path",
			WhyNotAnswering: "need current source evidence before the write",
		}

		return req, marshalToolRequestRaw(req), true
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicImplementationEvidencePathRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || r.planThenExecutePlannerPhase() || !r.unscopedNoWriteCompletionRecoveryApplies(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}

	seen := map[string]struct{}{}

	var paths []string

	for _, path := range r.RuntimeBrief.EvidencePaths {
		normalized := NormalizeRunnerPath(path)
		if normalized == "" || !recoverySearchPathLooksEditableSource(normalized) || alreadyReadPath(results, normalized) {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		paths = append(paths, normalized)
	}

	if len(paths) != 1 {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            paths[0],
		StartLine:       1,
		EndLine:         180,
		NamedGap:        "exact current source needed before editing the implementation evidence path",
		WhyNotAnswering: "need current source before the write",
	}

	return req, marshalToolRequestRaw(req), true
}

func (r *Runner) deterministicPlanExecuteRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required plan-execute edit path",
		SymbolGap:        "source-body evidence for required plan-execute edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before submit_plan can anchor the required edit",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before submit_plan can anchor the required edit",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before submit_plan can anchor the required edit",
	})
}

func (r *Runner) planThenExecuteRequiredSourceFollowupRedirect(results []workspace.ToolResult, req workspace.ToolRequest) (workspace.ToolRequest, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return workspace.ToolRequest{}, false
	}

	if successfulSubmitPlanResultCount(results) > 0 || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan || !workspace.IsReadTool(strings.TrimSpace(req.Tool)) {
		return workspace.ToolRequest{}, false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, false
	}

	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return workspace.ToolRequest{}, false
	}

	if planThenExecuteInvalidAnchorRefreshAllowed(req, target, results) {
		return workspace.ToolRequest{}, false
	}

	unread := planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)
	if _, missing := unread[target]; !missing {
		if len(unread) == 0 {
			return workspace.ToolRequest{}, false
		}

		redirected, _, ok := r.deterministicPlanExecuteRequiredSourceRead(results)
		if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
			return workspace.ToolRequest{}, false
		}

		return redirected, true
	}

	evidence := planThenExecuteRequiredSourceEvidenceForPath(r.RuntimeBrief, results, target)
	if evidence.Level == planThenExecuteEvidenceNone && !planThenExecutePathHasAnyToolEvidence(target, results) {
		return workspace.ToolRequest{}, false
	}

	if planThenExecuteRequiredSourceRequestAdvances(req, target, evidence, results) {
		return workspace.ToolRequest{}, false
	}

	redirected, ok := r.deterministicPlanExecuteRequiredSourceFollowup(target, results)
	if !ok || normalizedToolRequestRaw(redirected) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, false
	}

	return redirected, true
}

func (r *Runner) planThenExecuteAnchorRefreshWriteOnlyRejection(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() {
		return workspace.ToolResult{}, false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: "Bounded submit_plan anchor refresh required",
		Error:   planThenExecuteAnchorRefreshWriteOnlyError(strings.TrimSpace(req.Tool), state.RefreshPaths),
	}, true
}

func planThenExecuteInvalidAnchorRefreshAllowed(req workspace.ToolRequest, target string, results []workspace.ToolResult) bool {
	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return false
	}

	invalidAnchorPaths := submitPlanInvalidAnchorPaths(rejected)
	if _, invalidAnchor := invalidAnchorPaths[NormalizeRunnerPath(target)]; !invalidAnchor {
		return false
	}

	return submitPlanInvalidAnchorPathsAllowsRead(invalidAnchorPaths, target) &&
		planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) &&
		!successfulInvalidAnchorCorrectionEvidenceForRejection(results, rejectedAt, target)
}

func (r *Runner) planThenExecuteDirectSourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) (string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return "", false
	}

	if r.planThenExecuteReadySourceRefreshAllowed(req, results, used) {
		return "required path", true
	}
	if r.planThenExecuteActiveAnchorRefreshReadAllowed(req, results, used) {
		return "active anchor-refresh path", true
	}
	if r.planThenExecuteCandidateSourceRefreshAllowed(req, results, used) {
		return "candidate path", true
	}

	return "", false
}

func (r *Runner) planThenExecuteActiveAnchorRefreshReadAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || !slices.Contains(state.RefreshPaths, target) {
		return false
	}

	return planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req, state.RefreshPaths, results)
}

const planThenExecuteReadySourceRefreshLimit = 4

func (r *Runner) planThenExecuteReadySourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) == 0 || successfulSubmitPlanResultCount(results) > 0 || len(planThenExecuteUnreadRequiredPaths(r.RuntimeBrief, results)) > 0 {
		return false
	}

	if planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(r.RuntimeBrief, results) {
		return false
	}

	if !planThenExecuteRejectedPlanSourceRefreshAllowedForScope(req, r.requiredEditCoveragePaths(), results) {
		return false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return false
	}

	if _, ok := normalizedRequiredEditPathSet(r.RuntimeBrief.RequiredEditPaths)[target]; !ok {
		return false
	}

	if !planThenExecuteInvalidAnchorRefreshRequestAllowed(req, target, results) {
		return false
	}

	return true
}

func (r *Runner) planThenExecuteCandidateSourceRefreshAllowed(req workspace.ToolRequest, results []workspace.ToolResult, used int) bool {
	if r == nil || !r.planThenExecutePlannerPhase() || used >= planThenExecuteReadySourceRefreshLimit {
		return false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) > 0 || successfulSubmitPlanResultCount(results) > 0 {
		return false
	}

	return planThenExecuteCandidateRejectedPlanSourceRefreshAllowed(req, r.RuntimeBrief, results)
}

func (r *Runner) planThenExecuteRejectedPlanRefreshReadRedirect(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if r == nil || !r.planThenExecutePlannerPhase() || strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	target := NormalizeRunnerPath(planThenExecuteRequestTargetPath(req))
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}

	state := DerivePlanExecutePlanningState(r.RuntimeBrief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode != PlanExecutePlanningModeAnchorRefresh || len(state.RefreshPaths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	rejected, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)
	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	retargeted := false
	if !slices.Contains(state.RefreshPaths, target) {
		target = NormalizeRunnerPath(state.RefreshPaths[0])
		retargeted = true
	}

	line, ok := submitPlanSyntaxValidationLineForPath(rejected, target)
	if !retargeted && ok && req.StartLine <= line && line <= req.EndLine && planThenExecuteAnchorRefreshRequestAllowed(req) {
		return workspace.ToolRequest{}, "", false
	}

	if !retargeted &&
		!ok &&
		planThenExecuteAnchorRefreshRequestAllowed(req) &&
		!planThenExecuteAnchorRefreshReadRangeAlreadyTried(req, target, results[rejectedAt+1:]) {
		return workspace.ToolRequest{}, "", false
	}

	start, end := planThenExecuteAnchorRefreshFallbackRange(target, results, rejected)
	if ok {
		start = line - 20
		if start < 1 {
			start = 1
		}
		end = line + 60
	}
	redirected := workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            target,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        "refresh current source around rejected submit_plan syntax failure",
		WhyNotAnswering: "need current source around rejected plan line before corrected submit_plan",
	}

	return redirected, marshalToolRequestRaw(redirected), true
}

func planThenExecuteSubmitReadyParseFailureSeen(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	return planThenExecuteSubmitReadyParseFailureIndex(brief, results) >= 0
}

func planThenExecuteSubmitReadyParseFailureBlocksSourceRefresh(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	parseFailureAt := planThenExecuteSubmitReadyParseFailureIndex(brief, results)
	if parseFailureAt < 0 {
		return false
	}

	_, rejectedAt, ok := lastRejectedSubmitPlanCoverageResult(results)

	return !ok || rejectedAt < parseFailureAt
}

func planThenExecuteSubmitReadyParseFailureIndex(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) int {
	last := -1

	for i, result := range results {
		if strings.TrimSpace(result.Tool) != parserRecoveryToolName || strings.TrimSpace(result.Error) == "" {
			continue
		}

		if planThenExecuteSubmitPlanOnlyReady(brief, results[:i]) {
			last = i
		}
	}

	return last
}

func (r *Runner) planThenExecuteSubmitPlanOnlySurfaceRejection(req workspace.ToolRequest, results []workspace.ToolResult, allowedTools []string) (string, string, string, bool) {
	if r == nil || !planThenExecuteTerminalSubmitPlanSurfaceReady(r.RuntimeBrief, results) {
		return "", "", "", false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" || tool == workspace.ToolNameSubmitPlan {
		return "", "", "", false
	}

	if len(allowedTools) > 0 && !slices.Contains(allowedTools, workspace.ToolNameSubmitPlan) {
		return "", "", "", false
	}

	if planThenExecuteSubmitCorrectionActive(r.RuntimeBrief, results) {
		summary, errorText, prompt := planThenExecuteSubmitCorrectionWrongToolRejection(tool, planThenExecuteSubmitCorrectionRecoveryPlanForResults(r.RuntimeBrief, results, ""))
		return summary, errorText, prompt, true
	}

	prompt := strings.Join([]string{
		"Plan-execute submit_plan is now the only allowed planner action. Every required_edit_path already has anchor-ready source evidence, so read/search/source-refresh tools are closed for this planner turn.",
		"The next visible action must be a parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"Do not call `read_file`, `code_navigate`, search, shell, git, source-sanity, or per-edit write tools in this state. The rejected request will not become available again in this planner phase.",
		"Only use `Decision: source-satisfied` if every required path already contains the requested final state and you cite per-path current-state evidence. Only use `<<QUESTION>>:` if one exact path-specific blocker prevents drafting any `submit_plan` edit.",
	}, "\n")

	return "Plan-execute submit_plan required", fmt.Sprintf("%s is not available because the plan-execute planner is source-ready and exposes only `submit_plan`. Emit a parser-readable `submit_plan` JSON object now. %s Use `Decision: source-satisfied` or `<<QUESTION>>:` only for a true terminal exception; do not request another source read.", tool, planThenExecuteSubmitPlanCoverageSentence()), prompt, true
}

func planThenExecuteSubmitCorrectionRecoveryPlanForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, refreshNote string) PlanExecuteRecoveryPlan {
	rejected, _, _ := lastRejectedSubmitPlanCoverageResult(results)
	return planThenExecuteSubmitCorrectionRecoveryPlan(brief, rejected, refreshNote, planThenExecuteSubmitCorrectionBlockerAllowed(brief, results))
}

func planThenExecuteSubmitCorrectionWrongToolRejection(tool string, plan PlanExecuteRecoveryPlan) (string, string, string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "the requested tool"
	}

	errorSuffix := "No terminal source-satisfied/question/prose fallback is valid in this state."
	if slices.Contains(plan.TerminalOptions, "<<QUESTION>>:") {
		errorSuffix = "`<<QUESTION>>:` is valid only for the exact path-specific missing body/anchor blocker; no source-satisfied/prose fallback is valid in this state."
	}

	return "Corrected submit_plan required",
		fmt.Sprintf("%s is not available because plan-execute submit-correction mode exposes only `submit_plan`. Emit one parser-readable `submit_plan` JSON object now. %s %s", tool, planThenExecuteSubmitPlanCoverageSentence(), errorSuffix),
		RenderPlanExecuteRecoveryPlan(plan)
}

func planThenExecuteSubmitCorrectionCurrentSurfaceRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, string, string, bool) {
	if strings.TrimSpace(req.Tool) == "" || strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {
		return "", "", "", false
	}

	if !planThenExecuteSubmitCorrectionActive(brief, results) {
		return "", "", "", false
	}

	summary, errorText, prompt := planThenExecuteSubmitCorrectionWrongToolRejection(req.Tool, planThenExecuteSubmitCorrectionRecoveryPlanForResults(brief, results, ""))

	return summary, errorText, prompt, true
}

func planThenExecuteReadinessNudgedNonSubmitRejection(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, readinessNudged bool) (string, string, string, bool) {
	if !readinessNudged ||
		!planThenExecutePlannerBrief(brief) ||
		successfulSubmitPlanResultCount(results) > 0 ||
		strings.TrimSpace(req.Tool) == "" ||
		strings.TrimSpace(req.Tool) == workspace.ToolNameSubmitPlan {

		return "", "", "", false
	}

	if _, _, rejected := lastRejectedSubmitPlanCoverageResult(results); rejected {
		return "", "", "", false
	}

	tool := strings.TrimSpace(req.Tool)
	prompt := strings.Join([]string{
		"Plan-execute planner readiness was already reached before this request. The first terminal planner action is now required, so discovery, search, source refresh, verification, and per-edit write tools are closed until a `submit_plan` is attempted.",
		"The next visible action must be a parser-readable `submit_plan` JSON object. " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanVisibleJSONInstruction(),
		"Only use `Decision: source-satisfied` if the current source evidence proves every selected path already satisfies the requested final state. Only use `<<QUESTION>>:` if one exact path-specific blocker prevents drafting any plan edit.",
	}, "\n")

	return "Plan-execute submit_plan required",
		fmt.Sprintf("%s is not available after plan-execute readiness. The planner must attempt `submit_plan` before any more tool calls; use a terminal source-satisfied decision or exact blocker only if no executable edit can be drafted.", tool),
		prompt,
		true
}

func planThenExecuteTerminalSubmitPlanSurfaceReady(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if planThenExecuteSubmitPlanOnlyReady(brief, results) {
		return true
	}

	state := DerivePlanExecutePlanningState(brief, results, PlanExecutePlanningTransport{TextualToolParser: true})
	return state.Mode == PlanExecutePlanningModeSubmitReady || state.Mode == PlanExecutePlanningModeSubmitCorrection
}

func planThenExecuteRequiredSourceRequestAdvances(req workspace.ToolRequest, path string, evidence planThenExecuteSourceEvidence, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameReadFile:
		start, end := req.StartLine, req.EndLine
		if start <= 0 && end <= 0 {
			return evidence.Level == planThenExecuteEvidenceNone
		}

		if start <= 0 {
			start = 1
		}

		if end <= 0 || end < start {
			return false
		}

		if planThenExecuteReadRangeSeen(path, start, end, results) {
			return false
		}

		if planThenExecuteLargeGoRawRangeNeedsSymbolBody(path, planThenExecuteRequiredSourceReadTotal(path, results)) &&
			!planThenExecuteFileOutlineSeen(path, results) {

			return false
		}

		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines && planThenExecutePathHasOutlineEvidence(path, results) {
			return false
		}

		if start == 1 && end <= planThenExecuteGenericHeaderReadMaxLines &&
			planThenExecuteEvidenceRank(evidence.Level) >= planThenExecuteEvidenceRank(planThenExecuteEvidenceBodyWindow) {

			return false
		}

		return true
	case workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		capability := strings.TrimSpace(req.Capability)
		switch {
		case capability == "file_outline":
			return !planThenExecuteFileOutlineSeen(path, results)
		case capability == "read_symbol" || capability == "extract_body" ||
			strings.HasSuffix(capability, "_read_symbol") ||
			strings.HasSuffix(capability, "_extract_body"):
			symbol := strings.TrimSpace(req.Pattern)

			return symbol == "" || !planThenExecuteReadSymbolSeen(path, symbol, results)
		default:
			return false
		}
	default:
		tool := strings.TrimSpace(req.Tool)
		if strings.HasSuffix(tool, "_read_symbol") || strings.HasSuffix(tool, "_extract_body") {
			return true
		}

		return false
	}
}

func planThenExecuteRequiredSourceReadTotal(path string, results []workspace.ToolResult) int {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0
	}

	total := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if fact, ok := sourceBodyFactForPath(result, path); ok && fact.TotalLines > total {
			total = fact.TotalLines
		}
	}

	return total
}

func planThenExecuteReadRangeSeen(path string, start, end int, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" || start <= 0 || end < start {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		fact, ok := sourceBodyFactForPath(result, path)
		if !ok {
			continue
		}

		if fact.StartLine == start && fact.EndLine == end {
			return true
		}
	}

	return false
}

func (r *Runner) deterministicImplementationRequiredSourceFollowup(path string, results []workspace.ToolResult) (workspace.ToolRequest, bool) {
	return r.deterministicRequiredSourceFollowup(path, results, requiredSourceFollowupWording{
		OutlineGap:       "source-body line anchors for required implementation edit path",
		SymbolGap:        "source-body evidence for required implementation edit path",
		OutlineWhy:       "whole-file read redirected; need declarations before selecting a source-body read",
		SymbolWhy:        "need current declaration body before editing this required path",
		RangeWhy:         "outline did not produce a readable symbol; need bounded source before editing this required path",
		FallbackRangeWhy: "outline did not expose usable declarations; need bounded source before editing this required path",
	})
}

type requiredSourceFollowupWording struct {
	OutlineGap       string
	SymbolGap        string
	OutlineWhy       string
	SymbolWhy        string
	RangeWhy         string
	FallbackRangeWhy string
}

func (r *Runner) deterministicRequiredSourceFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return workspace.ToolRequest{}, false
	}

	if strings.TrimSpace(wording.OutlineGap) == "" {
		wording.OutlineGap = "source-body line anchors for required edit path"
	}

	if strings.TrimSpace(wording.SymbolGap) == "" {
		wording.SymbolGap = "source-body evidence for required edit path"
	}

	if !planThenExecutePathSupportsCodeNavigate(path) {
		return deterministicRequiredSourceRangeFollowup(path, results, wording)
	}

	if !planThenExecuteFileOutlineSeen(path, results) {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameCodeNavigate,
			Path:            path,
			Capability:      "file_outline",
			NamedGap:        wording.OutlineGap,
			WhyNotAnswering: wording.OutlineWhy,
		}, true
	}

	if symbol := planThenExecuteBestOutlineSymbol(path, r.RuntimeBrief.Objective, results); symbol != "" && !planThenExecuteReadSymbolSeen(path, symbol, results) {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameCodeNavigate,
			Path:            path,
			Capability:      "read_symbol",
			Pattern:         symbol,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.SymbolWhy,
		}, true
	}

	if start, end, ok := planThenExecuteBestOutlineRange(path, r.RuntimeBrief.Objective, results); ok {
		return workspace.ToolRequest{
			Tool:            workspace.ToolNameReadFile,
			Path:            path,
			StartLine:       start,
			EndLine:         end,
			NamedGap:        wording.SymbolGap,
			WhyNotAnswering: wording.RangeWhy,
		}, true
	}

	return deterministicRequiredSourceRangeFollowup(path, results, wording)
}

func deterministicRequiredSourceRangeFollowup(path string, results []workspace.ToolResult, wording requiredSourceFollowupWording) (workspace.ToolRequest, bool) {
	start, end, ok := planThenExecuteNextRequiredSourceReadRange(path, results)
	if !ok {
		return workspace.ToolRequest{}, false
	}

	return workspace.ToolRequest{
		Tool:            workspace.ToolNameReadFile,
		Path:            path,
		StartLine:       start,
		EndLine:         end,
		NamedGap:        wording.SymbolGap,
		WhyNotAnswering: wording.FallbackRangeWhy,
	}, true
}

func planThenExecuteNextRequiredSourceReadRange(path string, results []workspace.ToolResult) (int, int, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}

	maxEnd, total := 0, 0
	sawReadFileEvidence := false

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		fact, ok := sourceBodyFactForPath(result, path)
		if !ok {
			continue
		}

		sawReadFileEvidence = true

		if fact.StartLine > 0 && fact.EndLine >= fact.StartLine && fact.EndLine > maxEnd {
			maxEnd = fact.EndLine
		}

		if fact.TotalLines > total {
			total = fact.TotalLines
		}
	}

	if sawReadFileEvidence && maxEnd == 0 {
		return 0, 0, false
	}

	if !sawReadFileEvidence && alreadyReadPath(results, path) && !planThenExecuteFileOutlineSeen(path, results) {
		return 0, 0, false
	}

	start := 1
	if maxEnd > 0 {
		start = maxEnd + 1
	}

	if total > 0 && start > total {
		return 0, 0, false
	}

	end := start + 179
	if total > 0 && end > total {
		end = total
	}

	if start <= 0 || end < start {
		return 0, 0, false
	}

	return start, end, true
}

func planThenExecutePathSupportsCodeNavigate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".kts", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hxx":
		return true
	default:
		return false
	}
}

func planThenExecuteFileOutlineSeen(path string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	lowerPath := strings.ToLower(path)
	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Output)
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.Contains(text, "capability: file_outline") ||
			strings.Contains(text, "routed file_outline") ||
			strings.Contains(text, "top-level declaration") {

			return true
		}
	}

	return false
}

func planThenExecuteReadSymbolSeen(path, symbol string, results []workspace.ToolResult) bool {
	path = NormalizeRunnerPath(path)
	symbol = strings.ToLower(strings.TrimSpace(symbol))

	if path == "" || symbol == "" {
		return false
	}

	for _, result := range results {
		text := strings.ToLower(result.Summary + "\n" + result.Error + "\n" + result.Output)
		if strings.TrimSpace(result.Tool) == workspace.ToolNameCodeNavigate &&
			strings.Contains(text, "read_symbol") &&
			strings.Contains(text, strings.ToLower(path)) &&
			strings.Contains(text, symbol) {

			return true
		}
	}

	return false
}

func planThenExecuteBestOutlineSymbol(path, objective string, results []workspace.ToolResult) string {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return ""
	}

	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	best, bestScore := "", 0

	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}

		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}

			name := strings.TrimSpace(match[1])
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)

			if score > bestScore {
				best, bestScore = name, score
			}
		}

		for _, name := range planThenExecutePreviewDeclarationNames(text) {
			score := planThenExecuteSymbolScore(path, name, objectiveText, objectiveTokens)
			if score > bestScore {
				best, bestScore = name, score
			}
		}
	}

	return best
}

func planThenExecutePreviewDeclarationNames(text string) []string {
	var names []string

	seen := map[string]struct{}{}

	for _, match := range fileOutlinePreviewPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}

		for _, raw := range strings.Split(match[1], ",") {
			name := strings.TrimSpace(raw)
			if name == "" || strings.HasPrefix(name, "...") || strings.Contains(name, " more") {
				continue
			}

			if _, ok := seen[name]; ok {
				continue
			}

			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	return names
}

func planThenExecuteBestOutlineRange(path, objective string, results []workspace.ToolResult) (int, int, bool) {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return 0, 0, false
	}

	objectiveText := objective + " " + filepath.Base(path)
	objectiveTokens := planThenExecuteSymbolScoreTokens(objectiveText)
	bestStart, bestEnd, bestScore := 0, 0, 0

	for _, result := range results {
		text := result.Summary + "\n" + result.Output
		if strings.TrimSpace(result.Error) != "" || !strings.Contains(strings.ToLower(text), strings.ToLower(path)) {
			continue
		}

		for _, match := range fileOutlineDeclarationPattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 4 {
				continue
			}

			start, startErr := strconv.Atoi(match[2])
			end, endErr := strconv.Atoi(match[3])

			if startErr != nil || endErr != nil || start <= 0 || end < start {
				continue
			}

			score := planThenExecuteSymbolScore(path, strings.TrimSpace(match[1]), objectiveText, objectiveTokens)
			if score > bestScore {
				bestStart, bestEnd, bestScore = start, end, score
			}
		}
	}

	if bestScore <= 0 {
		return 0, 0, false
	}

	return bestStart, bestEnd, true
}

func planThenExecuteSymbolScore(path, name, objective string, objectiveTokens map[string]int) int {
	score := 0
	for token := range planThenExecuteSymbolScoreTokens(name) {
		score += objectiveTokens[token]
	}

	lowerName := strings.ToLower(name)
	lowerPath := strings.ToLower(filepath.ToSlash(path))

	if strings.HasSuffix(lowerPath, "_test.go") && strings.HasPrefix(lowerName, "test") {
		score += 4
	}

	if strings.HasPrefix(lowerPath, "cmd/") && strings.HasPrefix(lowerName, "run") {
		score += 4
	}

	if strings.Contains(lowerName, "validator") {
		score -= 3
	}

	return score
}

func planThenExecuteSymbolScoreTokens(text string) map[string]int {
	out := make(map[string]int)
	runes := []rune(text)

	var current strings.Builder

	flush := func() {
		token := strings.ToLower(strings.TrimSpace(current.String()))
		if len(token) >= 3 {
			out[token]++
		}

		current.Reset()
	}

	for i, r := range runes {
		if r == '_' || r == '-' || r == '/' || r == '.' || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()

			continue
		}

		if unicode.IsUpper(r) && current.Len() > 0 {
			prev := runes[i-1]

			var next rune

			if i+1 < len(runes) {
				next = runes[i+1]
			}

			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (next != 0 && unicode.IsLower(next)) {
				flush()
			}
		}

		current.WriteRune(unicode.ToLower(r))
	}

	flush()

	return out
}

func planThenExecutePathHasAnyToolEvidence(path string, results []workspace.ToolResult) bool {
	normalized := NormalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	_, ok := newToolResultEvidenceProjection(results).ReadPaths()[normalized]
	return ok
}

func (r *Runner) deterministicNoWriteRecoveryUnreadCandidateRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) {
		return workspace.ToolRequest{}, "", false
	}

	if r.planThenExecutePlannerPhase() {
		return workspace.ToolRequest{}, "", false
	}

	if successfulSubmitPlanResultCount(results) > 0 || len(newToolResultEvidenceProjection(results).ReadPaths()) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	if last := lastNonGateTool(results); last != workspace.ToolNameReadFile && last != workspace.ToolNameSearchContent && last != workspace.ToolNameCodeNavigate {
		return workspace.ToolRequest{}, "", false
	}

	paths := unreadCandidateRecoveryScopePaths(r.RuntimeBrief, results, 4)
	if completionGateResultSeen(results) {
		paths = unreadCandidateRecoveryScopePathsForRecovery(r.RuntimeBrief, results, 4)
	}

	if len(paths) == 0 {
		return workspace.ToolRequest{}, "", false
	}

	return noWriteRecoveryReadRequest(
		paths[0],
		"exact current code needed before writing the requested implementation change",
		"need current source before the write",
	)
}

func (r *Runner) deterministicPlanExecuteNonToolCandidateRecovery(reply string, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	// Open-candidate localization is strategic work, not a hard invariant.
	// Required edit paths and rejected stale anchors may still use deterministic
	// recovery, but open-ended plan-execute prose must be repaired by a
	// model-facing contract nudge/rejection instead of a host-chosen probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicPlanExecuteChainedSearchRecovery(req workspace.ToolRequest, results []workspace.ToolResult, executionState *runnerExecutionState) (workspace.ToolRequest, string, bool) {
	// Chained search is strategy, not a hard invariant. Exact-repeat and
	// no-progress guards still prevent loops; otherwise the model owns whether
	// a follow-up search or a source read is the better next probe.
	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoveryTargetRead(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || lastNonGateTool(results) != "" {
		return workspace.ToolRequest{}, "", false
	}

	for _, ref := range r.RuntimeBrief.TargetRefs {
		path := strings.TrimSpace(filepath.ToSlash(ref))
		if path == "" || !recoverySearchPathLooksEditableSource(path) || alreadyReadPath(results, path) {
			continue
		}

		return noWriteRecoveryReadRequest(
			path,
			"exact current code needed before submitting the requested plan",
			"need current source before submit_plan can anchor the edit",
		)
	}

	return workspace.ToolRequest{}, "", false
}

func (r *Runner) deterministicNoWriteRecoverySearch(results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if !r.noWriteRecoveryAllowed(results) || (lastNonGateTool(results) != workspace.ToolNameContextPack && !lastNonGateAttemptWasUnavailableContextPack(results)) {
		return workspace.ToolRequest{}, "", false
	}

	if len(concreteRecoveryEvidencePaths(results, 1)) > 0 || successfulToolSeen(results, workspace.ToolNameSearchContent) {
		return workspace.ToolRequest{}, "", false
	}

	pattern := literalRecoverySearchPattern(r.RuntimeBrief.Objective)
	if pattern == "" {
		return workspace.ToolRequest{}, "", false
	}

	req := workspace.ToolRequest{
		Tool:            workspace.ToolNameSearchContent,
		Path:            ".",
		Pattern:         pattern,
		NamedGap:        "locate exact current source for the requested behavior",
		WhyNotAnswering: "need a grounded source hit before reading or writing",
	}

	return req, marshalToolRequestRaw(req), true
}
