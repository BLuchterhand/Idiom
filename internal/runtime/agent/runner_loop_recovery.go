package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func (r *Runner) repeatedRangeLessReadFileRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}

	if len(changedPathsFromToolResults(results)) > 0 {
		return "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return "", "", false
	}

	if rangeLessReadFileWasRedirectedToOutline(path, results) && !concreteSourceEvidenceReadForPath(path, results) {
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

	if workspace.IsWorkspaceWriteTool(req.Tool) || len(changedPathsFromToolResults(results)) > 0 {
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

	if workspace.IsWorkspaceWriteTool(req.Tool) || len(changedPathsFromToolResults(results)) > 0 {
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
			read := sortedRunnerPathKeys(readPathsFromResults(results))
			lead := fmt.Sprintf("The repeated %s request hit the loop budget: %s. Do not repeat that request.", tool, reason)
			parts := []string{
				lead,
				"Unscoped plan-execute candidate paths are not coverage obligations, but this planner still has unread candidate surfaces that may close a concrete edit-safety fact.",
				"Read candidate path(s) so far: " + util.FirstNonEmpty(strings.Join(read, ", "), "(none)"),
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
	return len(readPathsFromResults(results)) == 0 && planThenExecuteHasPriorReadClassFailure(results)
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

	if len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !rangeLessReadFileWasRedirectedToOutline(path, results) || concreteSourceEvidenceReadForPath(path, results) {
		return workspace.ToolRequest{}, "", false
	}

	followup := r.repeatedRangeLessReadFileFollowup(path, results)
	if strings.TrimSpace(followup.Tool) == "" || normalizedToolRequestRaw(followup) == normalizedToolRequestRaw(req) {
		return workspace.ToolRequest{}, "", false
	}

	if len(allowedTools) > 0 && !runnerAllowsTool(allowedTools, followup.Tool) {
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

	target := normalizeRunnerPath(req.Path)
	if target == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, alreadyRead := readPathsFromResults(results)[target]; alreadyRead {
		return workspace.ToolRequest{}, "", false
	}

	candidateSet := make(map[string]struct{})
	for _, path := range candidateRecoveryScopePaths(r.RuntimeBrief, results, 8) {
		candidateSet[normalizeRunnerPath(path)] = struct{}{}
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

	if !runnerAllowsTool(allowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" {
		return workspace.ToolRequest{}, "", false
	}

	if _, ok := workspace.LanguageForFile(path); !ok {
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

	if strings.TrimSpace(req.Pattern) != "" || strings.TrimSpace(req.Symbol) != "" || strings.TrimSpace(req.Query) != "" {
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
	if r == nil || !r.planThenExecutePlannerPhase() || successfulSubmitPlanResultCount(results) > 0 || len(changedPathsFromToolResults(results)) > 0 {
		return workspace.ToolRequest{}, "", false
	}

	if !runnerAllowsTool(allowedTools, workspace.ToolNameReadFile) {
		return workspace.ToolRequest{}, "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || concreteSourceEvidenceReadForPath(path, results) {
		return workspace.ToolRequest{}, "", false
	}

	if _, ok := workspace.LanguageForFile(path); !ok {
		return workspace.ToolRequest{}, "", false
	}

	tool := strings.TrimSpace(req.Tool)
	capability := strings.TrimSpace(req.Capability)

	if capability == "" {
		capability = strings.TrimSpace(req.Operation)
	}

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
	path = normalizeRunnerPath(path)
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

func concreteSourceEvidenceReadForPath(path string, results []workspace.ToolResult) bool {
	return sourceEvidenceBodyReadForPath(path, results)
}

func concreteSourceEvidenceResultMatchesPath(result workspace.ToolResult, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || isNonSourceEvidenceArtifactPath(path) {
		return false
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameReadFile {
		readPath := normalizeRunnerPath(extractReadPathFromSummary(result.Summary))
		return readPath == path && !isNonSourceEvidenceArtifactPath(readPath)
	}

	for _, readPath := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(readPath) {
			continue
		}

		if normalizeRunnerPath(readPath) == path {
			return true
		}
	}

	return false
}

func fullSourceReadFileSeen(path string, results []workspace.ToolResult) bool {
	path = normalizeRunnerPath(path)
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

		readPath := normalizeRunnerPath(extractReadPathFromSummary(result.Summary))
		if readPath != path {
			continue
		}

		text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
		start, end, hasRange := planThenExecuteReadSummaryLineRange(result.Summary)
		total, hasTotal := planThenExecuteReadSummaryTotalLines(result.Summary)

		if hasRange && hasTotal && start == 1 && end >= total {
			return true
		}

		if hasRange || strings.Contains(text, "output was capped") || strings.Contains(text, "full range-less read omitted") || strings.Contains(text, "bounded large required-path source") {
			continue
		}

		return true
	}

	return false
}

func completionGateRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return completionGateRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func implementationRequiredPathHiddenOnlyRecoveryStall(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, reply llama.ChatMessage, priorGateRejections int, supportsNativeTools bool) bool {
	if supportsNativeTools || priorGateRejections == 0 {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) == 0 {
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

	if len(changedPathsFromToolResults(results)) > 0 {
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

	if len(changedPathsFromToolResults(results)) > 0 || len(r.remainingRequiredEditPaths(results)) > 0 {
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
		"Read/search/discovery tools are hidden for this turn. Do not inspect, search, navigate, plan, or summarize again.",
		"Your next visible message must be exactly one terminal action: a parser-readable write tool call using current source evidence, or `<<QUESTION>>:` naming the exact blocker that prevents a write.",
		textualMutationWriteJSONInstruction(),
	}
	if sourced := implementationTargetWriteReadySourcedPathSummary(results, 4); sourced != "" {
		lines = append(lines, "Already-read source-body evidence is available for: "+sourced+".")
	}

	lines = append(lines, "Choose the path and edit shape from gathered evidence and the active runtime contract. Actions written only in hidden reasoning are ignored by the parser.")

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

func concreteSourceEvidencePaths(results []workspace.ToolResult) []string {
	return sourceEvidenceBodyPaths(results)
}

func implementationTargetWriteReadySourcedPathSummary(results []workspace.ToolResult, max int) string {
	paths := concreteSourceEvidencePaths(results)
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

	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}

		if !sourceEvidenceHasBody(planThenExecuteToolResultSourceEvidence(result, path)) {
			continue
		}

		r.Workspace.MarkPathObserved(path)
	}
}

func concreteSourceEvidenceResult(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" {
		return false
	}

	if candidateAnchorOnlyResult(result) {
		return false
	}

	tool := strings.TrimSpace(result.Tool)
	switch {
	case readStyleSourceBodyTool(tool):
		return readFileResultHasConcreteSourceBody(result)
	case tool == workspace.ToolNameContextZoom:
		return contextZoomResultHasConcreteSourceBody(result)
	case tool == workspace.ToolNameCodeNavigate || tool == workspace.ToolNameReadOnlyCapability:
		return routedSourceReadOutputHasBody(result.Output) && toolResultHasNonArtifactReadPath(result)
	case strings.HasSuffix(tool, "_read_symbol"), strings.HasSuffix(tool, "_extract_body"):
		return toolResultHasNonArtifactReadPath(result)
	default:
		return false
	}
}

func readStyleSourceBodyTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile, workspace.ToolNamePrefetch:
		return true
	default:
		return false
	}
}

func contextZoomResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	text := strings.Join([]string{result.Summary, result.Output}, "\n")
	if strings.TrimSpace(text) == "" {
		return false
	}

	hasSourcePath := false

	for _, path := range pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(text)) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}

		hasSourcePath = true

		break
	}

	if !hasSourcePath {
		return false
	}

	lower := strings.ToLower(text)

	return routedSourceReadOutputHasBody(result.Output) ||
		strings.Contains(lower, `"source"`) ||
		strings.Contains(lower, "source body") ||
		strings.Contains(lower, "include_source")
}

func readFileResultHasConcreteSourceBody(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.Join([]string{result.Summary, result.Output}, "\n"))
	if strings.TrimSpace(result.Summary) == "" || !strings.Contains(text, "read ") {
		return false
	}

	hasSourcePath := false

	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			return false
		}

		hasSourcePath = true
	}

	if !hasSourcePath {
		return false
	}

	if strings.Contains(text, "redirected") ||
		strings.Contains(text, "file_outline") ||
		strings.Contains(text, "capability: outline") ||
		strings.Contains(text, "candidate_request") {

		return false
	}

	return true
}

func toolResultHasNonArtifactReadPath(result workspace.ToolResult) bool {
	for _, path := range readPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
			continue
		}

		return true
	}

	return false
}

func readFileResultOnlyMentionsDiagnosticLogArtifacts(result workspace.ToolResult) bool {
	paths := readPathsFromToolResult(result)
	if len(paths) == 0 {
		return false
	}

	for _, path := range paths {
		if !isNonSourceEvidenceArtifactPath(path) {
			return false
		}
	}

	return true
}
