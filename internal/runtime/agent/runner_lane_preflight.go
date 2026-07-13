package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/workerruntime"
)

func readOnlyInvestigationStrategyPrompt(allowedTools []string) string {
	broadOrientation := "Start with an allowed context or search tool that matches the missing evidence; one well-scoped call is usually better than enumerating symbols one by one."
	if toolAllowed(allowedTools, workspace.ToolNameContextPack) && toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		broadOrientation = "Start with context_pack or context_query for a ranked map of relevant files and symbols. One call replaces 20+ individual symbol lookups."
	} else if toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		broadOrientation = "Start with context_query for graph-shaped or symbol-relationship questions, or use search_content for exact text. One well-scoped call is usually better than enumerating symbols one by one."
	} else if toolAllowed(allowedTools, workspace.ToolNameSearchContent) {
		broadOrientation = "Start with search_content or search_files for stable names, labels, symbols, or error text, then read only the highest-confidence result."
	}

	bundledProbe := "Use the broadest visible read-only tool that matches the named gap, then follow with targeted reads only where needed."
	if toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery) {
		bundledProbe = "Use discovery_query with purpose=\"architecture_trace\" or \"impact_analysis\" to orchestrate multiple probes at once. One call replaces 10+ separate searches."
	}

	switchText := "If you are reading symbols one by one, pause and use a broader visible context or search tool instead."
	switch {
	case toolAllowed(allowedTools, workspace.ToolNameContextQuery) && toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery):
		switchText = "If you are reading symbols one by one, pause and use context_query or discovery_query instead."
	case toolAllowed(allowedTools, workspace.ToolNameContextQuery):
		switchText = "If you are reading symbols one by one, pause and use context_query, code_navigate, or search_content for the named gap instead."
	case toolAllowed(allowedTools, workspace.ToolNameSearchContent):
		switchText = "If you are reading symbols one by one, pause and use search_content or search_files for a stable term before the next targeted read."
	}

	return "## Investigation Strategy\n\n" +
		"For read-only investigation questions, an efficient approach:\n\n" +
		"1. Broad orientation (1 call): " + broadOrientation + "\n" +
		"2. Bundled probe (1 call): " + bundledProbe + "\n" +
		"3. Targeted reads (2-3 calls): Read only the 1-3 key files or symbols identified above. Do not enumerate every symbol in a file.\n" +
		"4. Answer: Once the evidence answers the question, stop. You do not need to exhaustively verify every related function.\n\n" +
		switchText
}

func narrowToolFamilyRepetitionNudgePrompt(narrowFamily string, consecutiveCalls int, allowedTools []string) string {
	options := readOnlyStrategyShiftOptions(allowedTools)
	if options == "" {
		options = "switch to a different visible read-only tool that directly matches the remaining evidence gap"
	}

	return fmt.Sprintf(
		"You have called %s %d times consecutively. If you still lack the evidence to answer, switch strategy: %s. Repeated narrow symbol lookups rarely uncover the high-level answer faster than one broader visible probe.",
		narrowFamily, consecutiveCalls, options,
	)
}

func readOnlyStrategyShiftOptions(allowedTools []string) string {
	var options []string
	if toolAllowed(allowedTools, workspace.ToolNameContextQuery) {
		options = append(options, "use `context_query` for a graph-shaped question")
	}
	if toolAllowed(allowedTools, workspace.ToolNameDiscoveryQuery) {
		options = append(options, "use `discovery_query` for a bundled layered probe")
	}
	if toolAllowed(allowedTools, workspace.ToolNameHarnessAudit) {
		options = append(options, "use `harness_audit` for harness self-inspection")
	}
	if toolAllowed(allowedTools, workspace.ToolNameContextPack) {
		options = append(options, "use `context_pack` for cached workspace localization")
	}
	if toolAllowed(allowedTools, workspace.ToolNameSearchContent) {
		options = append(options, "use `search_content` for a stable term or error text")
	}

	return joinStrategyOptions(options, "or")
}

func toolAllowed(allowedTools []string, tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	for _, allowed := range allowedTools {
		if strings.TrimSpace(allowed) == tool {
			return true
		}
	}

	return false
}

func joinStrategyOptions(options []string, conjunction string) string {
	switch len(options) {
	case 0:
		return ""
	case 1:
		return options[0]
	case 2:
		return options[0] + " " + conjunction + " " + options[1]
	default:
		return strings.Join(options[:len(options)-1], ", ") + ", " + conjunction + " " + options[len(options)-1]
	}
}

type runnerLanePreflightInput struct {
	Request               workspace.ToolRequest
	Raw                   string
	Results               []workspace.ToolResult
	VerificationAttempted bool
}

type runnerLanePreflightDecision struct {
	Request           workspace.ToolRequest
	Raw               string
	RejectSummary     string
	RejectError       string
	RejectStopReason  string
	RejectDisposition toolfacts.ToolRejectionDisposition
	RejectFacts       *toolfacts.ToolResultFacts
	ForceAnswer       *runnerForcedAnswer
}

type runnerForcedAnswer struct {
	Reason string
	Prompt string
}

type delegatedDiscoveryStopWordUse int

const delegatedDiscoveryStopWordSymbol delegatedDiscoveryStopWordUse = iota

func delegatedDiscoveryStopWord(lower string, use delegatedDiscoveryStopWordUse) bool {
	switch strings.TrimSpace(lower) {
	case "",
		"agent", "codebase", "architecture", "configuration", "config", "configs", "prompt", "prompts", "main",
		"files", "file", "directory", "directories", "entry", "entrypoints", "line", "lines",
		"harness", "implementation", "source", "truth", "source-of-truth", "source_of_truth", "before", "tests", "test", "readme", "docs", "start",
		"internal", "workspace", "named", "scope", "question", "questions", "direct", "directly", "delegated", "repository", "relative",
		"evidence", "sufficient", "inspected", "guidance", "prefer", "targeted", "symbol", "symbols", "bounded", "read", "reads", "overview", "overviews",
		"continue", "readiness", "claim", "claims", "probe", "probes", "resolve", "materially", "incomplete":
		return true
	}

	if use == delegatedDiscoveryStopWordSymbol {
		switch strings.TrimSpace(lower) {
		case "subagent", "subagents", "delegate", "task", "runner", "package", "import", "const", "type", "func", "return",
			"use", "uses", "using", "utilize", "utilizes", "utilise", "utilises", "make", "makes":
			return true
		}
	}

	return false
}
func (r *runExecution) applyLanePreflight(input runnerLanePreflightInput) runnerLanePreflightDecision {
	decision := runnerLanePreflightDecision{
		Request: input.Request,
		Raw:     input.Raw,
	}

	if workerruntime.PermissionProfileIsConversationOnly(r.state.runtimeBrief.PermissionProfile) {
		// Project-defined external tools are allowed even in conversation
		// mode — they're opaque shell invocations the host project
		// registered specifically so the agent could use them. Built-in
		// workspace tools (edit, shell, discovery_query, etc.) remain
		// rejected because conversation_only's contract is "no workspace
		// state change, no idiom-internal exploration." Externals
		// satisfy that contract by being host-controlled.
		toolName := strings.TrimSpace(decision.Request.Tool)
		isExternal := false

		isExternal = r.workspaceToolSurfaceSnapshot().HasExternalTool(toolName)

		if !isExternal {
			decision.RejectSummary = "Tool request rejected by conversation-only contract"
			decision.RejectError = "this turn is conversation-only. Do not call built-in tools; project-defined external tools (registered via workspace.external_tools) are allowed if any are configured. Otherwise answer directly from the prompt and prior visible context."
			decision.RejectStopReason = "conversation_only_tool_rejected"

			return decision
		}
	}

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.state.runtimeBrief.PermissionProfile) &&
		!workspace.AnalyzeVerificationRequest(decision.Request).Attempted {

		if !input.VerificationAttempted {
			decision.RejectSummary = "Verification-first contract rejected non-verification action"
			decision.RejectError = "this turn is verification-only. The requested action was not replaced or executed. Issue a fresh verification command with shell (for example go test ./..., go build ./..., npm test, or the seeded failing command), then inspect files only if the output needs explanation."
			decision.RejectStopReason = "verification_first_contract_exhausted"
			decision.RejectDisposition = toolfacts.ToolRejectionDeferredByPhase

			return decision
		} else {
			decision.RejectSummary = "Verification-only contract rejected post-verification drift"
			decision.RejectError = "this turn is verification-only and a verification command has already run. Report the pass/fail result directly unless another verification command is strictly necessary."
			decision.RejectStopReason = "verification_only_post_check_drift"

			return decision
		}
	}

	return decision
}

func conversationEvidenceSatisfied(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !readOnlyDirectAnswerLane(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results).Answerable()
}

// conversationInspectedPathSummary returns a compact summary of the
// most recent read/navigation results. It reports evidence without
// declaring readiness — the model owns the decision of when evidence
// is sufficient.
func conversationInspectedPathSummary(results []workspace.ToolResult) string {
	var inspected []string

	for i := len(results) - 1; i >= 0 && len(inspected) < 5; i-- {
		r := results[i]
		if strings.TrimSpace(r.Error) != "" {
			continue
		}

		tool := strings.TrimSpace(r.Tool)
		if tool == workspace.ToolNameContextPack || tool == workspace.ToolNameTask {
			continue
		}

		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			continue
		}

		inspected = append(inspected, summary)
	}

	if len(inspected) == 0 {
		return ""
	}

	// Reverse to show oldest first
	for i, j := 0, len(inspected)-1; i < j; i, j = i+1, j-1 {
		inspected[i], inspected[j] = inspected[j], inspected[i]
	}

	return strings.Join(inspected, "; ")
}

// conversationMilestones derives context-window-aware timing for
// conversation-lane auto-findings capture and call-count awareness nudges.
// Returns (firstMilestone, nudgeInterval) in tool calls.
//
// The derivation mirrors compactionParams in internal/surfaces/tui/app/compaction.go:
// firstMilestone is 25% of the compaction threshold in tool calls,
// nudgeInterval is 35%. Both are floored at current hardcoded defaults
// (15 and 20) so small models keep the existing schedule; large models
// get proportionally later milestones to match their larger context windows.
func conversationMilestones(ctxSize int) (firstMilestone int, nudgeInterval int) {
	if ctxSize <= 0 {
		ctxSize = 65536
	}

	const estTokensPerMsg = 125

	thresholdMsgs := ctxSize * 40 / 100 / estTokensPerMsg

	if thresholdMsgs > 200 {
		thresholdMsgs = 200
	}

	maxToolCalls := thresholdMsgs / 2

	firstMilestone = max(15, maxToolCalls*25/100)
	nudgeInterval = max(20, maxToolCalls*35/100)

	return firstMilestone, nudgeInterval
}

func (r *runExecution) conversationEvidenceAvailableForObjective(results []workspace.ToolResult) bool {
	if r == nil || runnerBriefRequiresMutation(r.state.runtimeBrief) || runnerVerificationRequired(r.state.runtimeBrief) {
		return false
	}

	return readOnlyClosureDecisionForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(r.state.runtimeBrief), results).Answerable()
}

func mainReadOnlyObjectiveShouldDelegate(objective string) bool {
	lower := strings.ToLower(delegatedDisplayQuestion(objective))
	if lower == "" {
		return false
	}

	if mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower) {
		return false
	}

	return ContainsAny(lower,
		"architecture", "boundary", "boundaries", "flow", "handoff", "lifecycle", "orchestration",
		"how does", "how this", "explain how", "trace", "call", "calls", "invoked", "invoke",
		"subagent", "sub-agent", "sub agent", "child task", "child runner",
		"discovery tool", "discovery tools", "tool surface", "increase", "improve", "improvement", "usefulness", "what can we add",
	)
}

func mainReadOnlyObjectiveIsConcreteFeatureQuestion(lower string) bool {
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return false
	}

	if strings.Contains(lower, "what can we add") || strings.Contains(lower, "improve") || strings.Contains(lower, "usefulness") {
		return false
	}

	if strings.HasPrefix(lower, "what does ") || strings.HasPrefix(lower, "where is ") || strings.HasPrefix(lower, "where does ") {
		return true
	}

	return ContainsAny(lower, " mode do", " feature do")
}

func mainReadOnlyDelegationBootstrapEvidenceSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		tool := strings.TrimSpace(result.Tool)
		if tool == "" || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if tool == workspace.ToolNameTask {
			return true
		}

		if workspace.IsReadTool(strings.TrimSpace(tool)) && strings.TrimSpace(result.Output) != "" {
			return true
		}
	}

	return false
}

func shouldDelegateMainReadOnlyBootstrapRequest(req workspace.ToolRequest) bool {
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameContextPack, workspace.ToolNameContextQuery, workspace.ToolNameContextStatus, workspace.ToolNameDiscoveryQuery, workspace.ToolNameHarnessAudit:
		return true
	case workspace.ToolNameListFiles:
		return true
	case workspace.ToolNameSearchFiles:
		pattern := strings.TrimSpace(req.Pattern)
		return pattern == "" || pattern == "*" || pattern == "*.go" || strings.HasPrefix(pattern, "*")
	case workspace.ToolNameSearchContent:
		path := strings.TrimSpace(req.Path)
		return path == "" || path == "." || !pathparse.HasWorkspaceFileExtension(path)
	default:
		return workspace.IsBroadInspectionRequest(req)
	}
}

func delegatedReadOnlyChildMechanismQuestion(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(delegatedDisplayQuestion(objective)), " "))
	return ContainsAny(lower,
		"subagent",
		"sub-agent",
		"sub agent",
		"child-agent",
		"child agent",
		"child task",
		"task tool",
		"delegation",
	)
}

func delegatedReadOnlyLooksCodeShapedSymbol(value string) bool {
	if strings.Contains(value, "_") {
		return true
	}

	hasLower := false

	for i, r := range value {
		switch {
		case unicode.IsDigit(r):
			return true
		case unicode.IsLower(r):
			hasLower = true
		case i > 0 && unicode.IsUpper(r) && hasLower:
			return true
		}
	}

	return false
}

func (r *runExecution) delegatedReadOnlyRequestPath(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}

	if path := strings.TrimSpace(req.Path); path != "" {
		return filepath.ToSlash(path)
	}

	if r.runner.Workspace == nil || strings.TrimSpace(req.ID) == "" {
		return ""
	}

	path, err := r.runner.Workspace.ResolveHandlePath(req.ID)
	if err != nil {
		return ""
	}

	return filepath.ToSlash(path)
}

func (r *runExecution) delegatedReadOnlyAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}

	if !strings.HasPrefix(r.agentName(), "child:") || strings.TrimSpace(r.state.runtimeBrief.TaskFamily) != "delegated_read_only" {
		return false
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) || runnerVerificationRequired(r.state.runtimeBrief) {
		return false
	}

	tool := strings.TrimSpace(textutil.FirstNonEmpty(result.Tool, req.Tool))
	closure := readOnlyClosureDecisionForResults(r.state.runtimeBrief, results)

	switch {
	case tool == workspace.ToolNameDiscoveryQuery:
		if !delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
			return false
		}

		// A composite discovery packet provides a codebase map but not
		// source content. Require at least one source body read before
		// declaring readiness, otherwise the model is blocked from
		// reading the files the discovery query surfaced.
		if !closure.Answerable() {
			return false
		}
	case workspace.IsReadTool(strings.TrimSpace(tool)):
		if !closure.Answerable() {
			return false
		}
	default:
		return false
	}

	return true
}

func (r *runExecution) readOnlyAnalysisAnswerReadyAfterResult(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) bool {
	_ = req

	if r == nil || strings.TrimSpace(result.Error) != "" {
		return false
	}

	if strings.TrimSpace(r.state.runtimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		return false
	}

	if strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return false
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) || runnerVerificationRequired(r.state.runtimeBrief) {
		return false
	}

	if r.conversationEvidenceAvailableForObjective(results) && !readOnlyExplanationGateApplies(r.state.runtimeBrief) {
		return true
	}

	if !readOnlyExplanationGateApplies(r.state.runtimeBrief) {
		return false
	}

	exactProbes := readOnlyAnalysisExactProbeCount(results)
	evidencePaths := newToolResultEvidenceProjection(results).promptEvidencePaths(6)

	if delegatedReadOnlyHasCompositeDiscoveryPacket(results) {
		closure := readOnlyClosureDecisionForResults(r.state.runtimeBrief, results)
		return exactProbes >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.state.runtimeBrief, closure.Evidence)
	}

	closure := readOnlyClosureDecisionForResults(r.state.runtimeBrief, results)

	return exactProbes >= 2 && len(evidencePaths) >= 1 && closure.Answerable() && readOnlyAnalysisHasObjectiveRelevantSourceBody(r.state.runtimeBrief, closure.Evidence)
}

func readOnlyAnalysisHasObjectiveRelevantSourceBody(brief workerruntime.WorkerRuntimeBrief, evidence readOnlyAnswerabilityLedger) bool {
	sourceBodies := readOnlyPathSet(evidence.SourceBodyPaths)
	for _, support := range evidence.SourceBodyObjectiveTerms {
		path := NormalizeRunnerPath(support.Path)
		if path == "" || len(support.Terms) == 0 {
			continue
		}

		if _, ok := sourceBodies[path]; ok {
			return true
		}
	}

	for _, path := range textutil.Merge(evidence.SourceBodyObjectivePaths, evidence.SourceBodyPaths) {
		if implementationSourcePathOverlapsObjective(path, brief.Objective) ||
			sourceEvidencePathSharesObjectiveTerm(path, brief.Objective) {

			return true
		}
	}

	return false
}

func readOnlyAnalysisExactProbeCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if newToolResultEvidenceRecord(result).candidateAnchorOnly {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile:
			if newToolResultEvidenceRecord(result).GeneratedArtifactOnlyRead() {
				continue
			}

			if span := toolResultReadSpan(result); span > 0 && span <= 120 {
				count++
			}
		case workspace.ToolNameCodeNavigate:
			if len(newToolResultEvidenceRecord(result).SourceWindowPaths()) > 0 {
				count++
			}
		default:
			if !languagetools.ToolHasAnyCapability(
				result.Tool,
				semanticlanguage.CapReadSymbol,
				semanticlanguage.CapExtractBody,
				semanticlanguage.CapCallsFrom,
				semanticlanguage.CapCallsInto,
				semanticlanguage.CapReferences,
				semanticlanguage.CapDefinition,
				semanticlanguage.CapWorkspaceSymbol,
			) {
				continue
			}
			count++
		}
	}

	return count
}

func toolResultReadSpan(result workspace.ToolResult) int {
	match := regexp.MustCompile(`lines (\d+)-(\d+)`).FindStringSubmatch(result.Summary)
	if len(match) != 3 {
		return 0
	}

	start, errStart := strconv.Atoi(match[1])
	end, errEnd := strconv.Atoi(match[2])

	if errStart != nil || errEnd != nil || end < start {
		return 0
	}

	return end - start + 1
}

func delegatedReadOnlyHasCompositeDiscoveryPacket(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == workspace.ToolNameDiscoveryQuery &&
			strings.TrimSpace(result.Error) == "" &&
			strings.Contains(result.Output, `"artifact_path"`) &&
			strings.Contains(result.Output, `"evidence"`) {

			return true
		}
	}

	return false
}

func delegatedReadOnlyHasBodyRead(results []workspace.ToolResult, wantPath string) bool {
	wantPath = filepath.ToSlash(strings.TrimSpace(wantPath))
	if wantPath == "" {
		return false
	}

	for _, path := range newToolResultEvidenceProjection(results).delegatedReadOnlyInspectedImplementationPaths() {
		if path == wantPath {
			return true
		}
	}

	return false
}

func delegatedReadOnlySiblingImplementationPath(results []workspace.ToolResult, currentPath string) string {
	currentPath = filepath.ToSlash(strings.TrimSpace(currentPath))
	if currentPath == "" {
		return ""
	}

	candidates := make([]string, 0, 6)
	dir := filepath.ToSlash(filepath.Dir(currentPath))
	base := strings.TrimSuffix(filepath.Base(currentPath), filepath.Ext(currentPath))
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '_' || r == '-' })

	for shrink := len(parts) - 1; shrink >= 1; shrink-- {
		candidate := filepath.ToSlash(filepath.Join(dir, strings.Join(parts[:shrink], "_")+".go"))
		if candidate != currentPath {
			candidates = append(candidates, candidate)
		}
	}

	for _, path := range newToolResultEvidenceProjection(results).pinnedPaths(32) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" || path == currentPath || strings.HasSuffix(path, "_test.go") || !strings.HasSuffix(path, ".go") {
			continue
		}

		if filepath.ToSlash(filepath.Dir(path)) != dir {
			continue
		}

		candidates = append(candidates, path)
	}

	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}

		seen[candidate] = struct{}{}
		for _, result := range results {
			if toolResultMentionsPath(result, candidate) {
				return candidate
			}
		}
	}

	return ""
}

func delegatedReadOnlySearchHitWindow(results []workspace.ToolResult, path string) (int, int, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return 0, 0, false
	}

	pattern := regexp.MustCompile(regexp.QuoteMeta(path) + `:(\d+):`)

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Output) == "" {
			continue
		}

		matches := pattern.FindStringSubmatch(result.Output)
		if len(matches) != 2 {
			continue
		}

		line, err := strconv.Atoi(matches[1])
		if err != nil || line <= 0 {
			continue
		}

		start := line - 20
		if start < 1 {
			start = 1
		}

		return start, line + 80, true
	}

	return 0, 0, false
}

func toolResultMentionsPath(result workspace.ToolResult, path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}

	for _, field := range []string{result.Summary, result.Output, result.Error} {
		if strings.Contains(filepath.ToSlash(field), path) {
			return true
		}
	}

	return false
}

func (r *runExecution) explorationDelegationNudge(readCallsSinceWrite int, results []workspace.ToolResult) string {
	if r == nil || r.runner.Workspace == nil || r.runner.DisableTaskTool || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return ""
	}

	// The exploration delegation nudge uses mutation language
	// ("without a write") and assumes write-then-answer lanes.
	// The conversation lane has its own evidence path and should
	// not receive mutation-framed nudges.
	if r.lanePolicy().skipExplorationDelegationNudge {
		return ""
	}

	if runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return ""
	}

	allowedTools := r.workspaceAdvertisedAllowedTools()
	if !slices.Contains(allowedTools, workspace.ToolNameTask) {
		return ""
	}

	if hasSuccessfulTaskResult(results) {
		return ""
	}

	minEvidenceResults := 2
	threshold := 4

	if prefersEarlyReadOnlyDelegation(r.state.runtimeBrief) {
		minEvidenceResults = 1
		threshold = 1
	}

	if workerruntime.RuntimeContractIsDirectAnswer(r.state.runtimeBrief.Contract) {
		if hasConcreteReadOnlyInspectionEvidence(results) {
			return ""
		}

		minEvidenceResults = 2
		threshold = 2
	}

	if successfulReadClassResultCount(results) < minEvidenceResults {
		return ""
	}

	if readCallsSinceWrite < threshold {
		return ""
	}

	return fmt.Sprintf("You have already used %d read/search steps in the main loop without a write. A final answer is available if the current evidence already supports a concrete answer. Only if one broad missing claim genuinely remains and a bounded read_only child would close it faster than more local probing, you may delegate one bounded read_only task with the task tool.", readCallsSinceWrite)
}

func prefersEarlyReadOnlyDelegation(brief workerruntime.WorkerRuntimeBrief) bool {
	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return false
	}

	for _, policy := range brief.SearchPolicy {
		if textutil.EqualTrimFold(policy, "delegate_broad_discovery_when_needed") {
			return true
		}
	}

	return false
}

func hasConcreteReadOnlyInspectionEvidence(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		if workspace.IsSourceBodyInspectionTool(result.Tool) {
			return true
		}
	}

	return false
}
