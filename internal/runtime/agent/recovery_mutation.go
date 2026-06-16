package agent

import (
	"context"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
		readPath := normalizeRunnerPath(agentutil.ExtractReadPathFromSummary(result.Summary))
		return readPath == path && !isNonSourceEvidenceArtifactPath(readPath)
	}

	for _, readPath := range agentutil.ReadPathsFromToolResult(result) {
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

		readPath := normalizeRunnerPath(agentutil.ExtractReadPathFromSummary(result.Summary))
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

	for _, path := range agentutil.ReadPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
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

	return agentutil.RoutedSourceReadOutputHasBody(result.Output) ||
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

	for _, path := range agentutil.ReadPathsFromToolResult(result) {
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
	for _, path := range agentutil.ReadPathsFromToolResult(result) {
		if isNonSourceEvidenceArtifactPath(path) {
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
	if r.planThenExecutePlannerPhase() && len(changedPathsFromToolResults(results)) == 0 {
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

	if len(changedPathsFromToolResults(results)) == 0 {
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

		if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
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

	if missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results)); len(missing) > 0 {
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

	if verificationContractSkipsGate(r.RuntimeBrief.VerificationContract) {
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

// refineReasoningShape captures one recognized markdown structural
// shape the auto-salvage can recover. anchor is the marker we search
// for as the "primary" structural section start; corroborator is a
// second marker that must ALSO appear so we don't extract on a
// passing mention. Each shape's primary anchor defines where the
// extracted block begins (we walk back from its last occurrence).
type refineReasoningShape struct {
	name         string
	primary      string
	corroborator string
}

var refineReasoningShapes = []refineReasoningShape{
	// Refine review deliverable: structured bug list + verdict.
	{name: "review", primary: "## Bugs found", corroborator: "## Verdict"},
	// Refine markdown-deliverable mode: the draft carries the target
	// file's content in a dedicated inline-content section.
	{name: "markdown-deliverable", primary: "## Inline content", corroborator: "## Deliverable"},
	// Refine draft / patch deliverable: implementation brief layout
	// (the canonical brief skeleton sections). Required Behavior +
	// Files To Modify both appear in every well-formed refine draft;
	// requiring both lets us reject thinking chatter that mentions a
	// section heading in passing.
	{name: "draft", primary: "## Files To Modify", corroborator: "## Required Behavior"},
	// Lowercase variant — the draft template uses "## Files to
	// Modify" (small 't') in some renderings.
	{name: "draft-alt", primary: "## Files to Modify", corroborator: "## Required Behavior"},
	// Some project and refine templates use "Required Edits" for the
	// concrete implementation section. Treat that as the same
	// corrected-brief shape so a prose preface does not strand a full
	// markdown deliverable in the final answer.
	{name: "draft-required-edits", primary: "## Files To Modify", corroborator: "## Required Edits"},
	{name: "draft-required-edits-alt", primary: "## Files to Modify", corroborator: "## Required Edits"},
}

// extractStructuredMarkdownFromReasoning recovers a refine-shaped
// markdown deliverable (review or draft/patch) from inside a model's
// reasoning content, used by tryAutoSalvageMarkdownDeliverable as
// a fallback when the visible reply is empty. Returns "" when no
// safe extraction is possible — the auto-salvage caller then skips
// and the completion gate rejects normally.
//
// Safety preconditions (all must hold):
//   - reasoning >= 200 chars (some signal there)
//   - reasoning matches one of refineReasoningShapes (primary +
//     corroborator both present)
//   - extracted block is >= 100 chars
//
// Extraction strategy: find the LAST occurrence of the matched
// shape's primary anchor and take from there to the end (walking
// back through any preceding `# Title`). Last-occurrence is
// deliberate — the model's thinking often iterates a few times
// before settling on the final structure; the last occurrence is
// closest to the model's final intent.
func extractStructuredMarkdownFromReasoning(reasoning string) string {
	r := strings.TrimSpace(reasoning)
	if len(r) < 200 {
		return ""
	}

	var matched refineReasoningShape

	for _, shape := range refineReasoningShapes {
		if strings.Contains(r, shape.primary) && strings.Contains(r, shape.corroborator) {
			matched = shape
			break
		}
	}

	if matched.primary == "" {
		return ""
	}

	primaryIdx := strings.LastIndex(r, matched.primary)
	if primaryIdx < 0 {
		return ""
	}

	startIdx := primaryIdx
	if nl := strings.LastIndex(r[:primaryIdx], "\n"); nl >= 0 {
		startIdx = nl + 1
	}

	// Walk back to a preceding `# Title` if one is directly above
	// (within 400 chars). Conservative cap — beyond that we'd risk
	// grabbing arbitrary thinking.
	lookbackStart := startIdx - 400
	if lookbackStart < 0 {
		lookbackStart = 0
	}

	preceding := r[lookbackStart:startIdx]
	if hdr := strings.LastIndex(preceding, "\n# "); hdr >= 0 {
		startIdx = lookbackStart + hdr + 1
	}

	candidate := strings.TrimSpace(r[startIdx:])
	if len(candidate) < 100 {
		return ""
	}

	if reasoningCandidateLooksLikeUnfilledTemplate(candidate) {
		return ""
	}

	return candidate
}

// reasoningCandidateLooksLikeUnfilledTemplate rejects salvaged
// content that's the meta-brief's skeleton placeholder itself —
// observed during refine v10 (#97): Coder-30B emitted 0 visible
// content and 69KB of reasoning content that turned out to be the
// model "thinking about" the template (copy-pasting `<RELATIVE_PATH>`
// and `<numbered step...>` markers). The salvage matched the
// structural anchors and would have shipped that garbage as a draft.
//
// Markers checked are the exact placeholder strings the refine
// templates ship. If two or more appear in the candidate, treat it
// as model-didn't-actually-fill-it and refuse to salvage.
func reasoningCandidateLooksLikeUnfilledTemplate(candidate string) bool {
	markers := []string{
		"RELATIVE_PATH",
		"<numbered step",
		"<workspace/relative",
		"<bracket placeholder>",
		"<short title>",
		"<description>",
		"<how to fix>",
		"<one or two sentences",
		"<quote relevant existing code",
		"<shell command that verifies",
	}
	hits := 0

	for _, m := range markers {
		if strings.Contains(candidate, m) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}

	return false
}

// extractStructuredMarkdownFromReply recovers a full markdown
// deliverable from visible final-answer text. Unlike reasoning
// salvage, visible reply salvage may walk back to a much earlier H1:
// long refine drafts often place `## Files To Modify` near the end of
// the document, well beyond the conservative reasoning lookback.
func extractStructuredMarkdownFromReply(reply string) string {
	r := strings.TrimSpace(reply)
	if len(r) < 100 || strings.HasPrefix(r, "<<QUESTION>>") {
		return ""
	}

	if recoverableMarkdownDeliverableShape(r) && markdownBodyStartsAsDeliverable(r) && !reasoningCandidateLooksLikeUnfilledTemplate(r) {
		return r
	}

	var matched refineReasoningShape

	for _, shape := range refineReasoningShapes {
		if strings.Contains(r, shape.primary) && strings.Contains(r, shape.corroborator) {
			matched = shape
			break
		}
	}

	if matched.primary == "" {
		return ""
	}

	primaryIdx := strings.LastIndex(r, matched.primary)
	if primaryIdx < 0 {
		return ""
	}

	startIdx := primaryIdx
	if strings.HasPrefix(r, "# ") {
		startIdx = 0
	} else if hdr := strings.LastIndex(r[:primaryIdx], "\n# "); hdr >= 0 {
		startIdx = hdr + 1
	} else if nl := strings.LastIndex(r[:primaryIdx], "\n"); nl >= 0 {
		startIdx = nl + 1
	}

	candidate := strings.TrimSpace(r[startIdx:])
	if len(candidate) < 100 || !recoverableMarkdownDeliverableShape(candidate) {
		return ""
	}

	if reasoningCandidateLooksLikeUnfilledTemplate(candidate) {
		return ""
	}

	return candidate
}

func recoverableMarkdownDeliverableShape(body string) bool {
	body = strings.TrimSpace(body)
	if len(body) < 100 {
		return false
	}

	for _, shape := range refineReasoningShapes {
		if strings.Contains(body, shape.primary) && strings.Contains(body, shape.corroborator) {
			return true
		}
	}

	return false
}

// autoSalvageMarkdownDeliverable handles a specific Qwen-at-low-temp
// failure mode: the deliverable is a single new .md file (refine
// review/draft/patch lanes), the model wrote the entire markdown
// content into its final-answer text instead of calling write_file,
// and the completion gate is about to reject for "no successful write."
//
// When the brief has exactly one required edit path that ends in .md,
// the path doesn't yet exist on disk, and the reply is a recognizable
// refine-shaped markdown payload, we write the reply to that path on
// behalf of the model and return the synthetic write_file result so
// the main runner can record it as normal completion evidence.
//
// Returns false (no salvage) when the criteria don't match — caller
// proceeds with the normal rejection. Salvage failures (file write
// errors) also return false; the original gate rejection wins.
func (r *Runner) autoSalvageMarkdownDeliverable(reply string) (workspace.ToolRequest, workspace.ToolResult, bool) {
	if r == nil || r.Workspace == nil {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	if strings.TrimSpace(r.RuntimeBrief.TaskFamily) == PlanThenExecuteReviewRepairTaskFamily {
		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	if len(r.RuntimeBrief.RequiredEditPaths) != 1 {
		if len(r.RuntimeBrief.RequiredEditPaths) > 0 {
			r.emitAutoSalvageEvent("skip: required_edit_paths count=%d, want 1", len(r.RuntimeBrief.RequiredEditPaths))
		}

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	path := strings.TrimSpace(r.RuntimeBrief.RequiredEditPaths[0])
	if path == "" || !strings.HasSuffix(strings.ToLower(path), ".md") {
		r.emitAutoSalvageEvent("skip: path %q is not a .md file", path)

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	cleanPath := filepath.ToSlash(filepath.Clean(path))
	if filepath.IsAbs(path) || cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		r.emitAutoSalvageEvent("skip: path %q is not a safe relative path", path)

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	if root := strings.TrimSpace(r.Workspace.Root()); root != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(cleanPath))); err == nil {
			r.emitAutoSalvageEvent("skip: path %q already exists", path)

			return workspace.ToolRequest{}, workspace.ToolResult{}, false
		} else if !os.IsNotExist(err) {
			r.emitAutoSalvageEvent("skip: cannot stat %q: %v", path, err)

			return workspace.ToolRequest{}, workspace.ToolResult{}, false
		}
	}

	body := extractStructuredMarkdownFromReply(reply)
	source := "reply"

	if body != "" && strings.TrimSpace(body) != strings.TrimSpace(reply) {
		source = "reply-structured"

		r.emitAutoSalvageEvent("extracted structured markdown from reply prose (%d chars)", len(body))
	}

	// Fallback path (#95): Qwen3 thinking mode sometimes burns the
	// entire generation budget inside the <think> block and exits
	// with empty visible content even when the thinking-text contains
	// a structured review. Recover by extracting from
	// LastAssistantMessage.ReasoningContent — bounded to the
	// review-template structural markers so we don't ship random
	// reasoning artifacts.
	if len(body) < 100 {
		if salvaged := extractStructuredMarkdownFromReasoning(r.LastAssistantMessage.ReasoningContent); salvaged != "" {
			body = salvaged
			source = "reasoning"

			r.emitAutoSalvageEvent("recovering from reasoning content (reply was %d chars, reasoning yielded %d)", len(strings.TrimSpace(reply)), len(body))
		}
	}

	if len(body) < 100 {
		r.emitAutoSalvageEvent("skip: body too short (%d chars, source=%s)", len(body), source)

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	if !recoverableMarkdownDeliverableShape(body) {
		r.emitAutoSalvageEvent("skip: body is not a recognized markdown deliverable shape (first 100 chars: %q)", body[:min(100, len(body))])

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	ctx := context.Background()
	req := workspace.ToolRequest{
		Tool:        workspace.ToolNameWriteFile,
		Path:        path,
		Content:     body,
		Description: "auto-salvage: model put deliverable in final-answer text instead of calling write_file",
	}
	result := r.Workspace.Execute(ctx, req)

	if strings.TrimSpace(result.Error) != "" {
		r.emitAutoSalvageEvent("FAILED: write_file %s: %s", path, result.Error)

		return workspace.ToolRequest{}, workspace.ToolResult{}, false
	}

	r.emitAutoSalvageEvent("SUCCESS: wrote %s (%d bytes)", path, len(body))

	return req, result, true
}

func (r *Runner) emitAutoSalvageEvent(format string, args ...any) {
	if r == nil {
		return
	}

	r.emitEvent(RunEvent{Role: "auto_salvage", Content: fmt.Sprintf(format, args...)})
}

// tryAutoSalvageMarkdownDeliverable is retained for focused unit tests
// and callers that only care whether the recovery wrote a file. The
// main runner uses autoSalvageMarkdownDeliverable so the synthetic
// write_file result is recorded in collectedResults.
func (r *Runner) tryAutoSalvageMarkdownDeliverable(reply string) bool {
	_, _, ok := r.autoSalvageMarkdownDeliverable(reply)
	return ok
}

func markdownBodyStartsAsDeliverable(body string) bool {
	body = strings.TrimSpace(body)
	return strings.HasPrefix(body, "# ") || strings.HasPrefix(body, "## ")
}

func mutationReplyReportsConcreteNoopOrBlocker(lowerReply string) bool {
	lowerReply = strings.ToLower(strings.TrimSpace(lowerReply))
	if lowerReply == "" {
		return false
	}

	if containsAny(lowerReply,
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

	return containsAny(lowerReply, "already present", "already implemented", "already satisfied", "already up to date") &&
		containsAny(lowerReply, "no change", "no-op", "no op", "nothing to change")
}

func mutationReplyClaimsCompletedWorkspaceChange(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return false
	}

	lower := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	if containsAny(lower, "already present", "already implemented", "already satisfied", "already up to date") &&
		containsAny(lower, "no change", "no-op", "no op", "nothing to change") {

		return false
	}

	if len(pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(trimmed))) == 0 {
		for _, fragment := range pathparse.QuotedFragments(trimmed) {
			if _, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				return containsAny(lower,
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

	return containsAny(lower,
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

// escalateCompletionGateMessage prefixes the gate's existing error
// text with progressively-firmer instructions so the model gets
// clearer guidance on each retry instead of the same message N times.
// priorRejections is the number of completion-gate rejections this
// turn BEFORE the current one (so 0 = first rejection). Generic — no
// task-family or project assumptions.
//
// Tier 0 (first rejection): pass through unchanged. Most rejections
//
//	resolve on first retry; extra prefix would be noise.
//
// Tier 1 (second rejection): mark it explicitly + remind the model
//
//	the gate already fired once and the next move must address the
//	stated blocker.
//
// Tier 2 (third rejection, last before tools disable): demand a
//
//	concrete tool call by name OR an explicit blocker statement.
func escalateCompletionGateMessage(base string, priorRejections int) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return base
	}

	// Detect source-grounding rejection patterns.  When the gate says the
	// model cited paths without source-body evidence, the fix is to rewrite
	// the answer text — not to call more tools (the model already has the
	// needed evidence in its context).
	hasUnsupportedPaths := strings.Contains(trimmed, "not backed by a source-body read")
	hasNonExistentPaths := strings.Contains(trimmed, "not read as source-body evidence")
	isSourceGrounding := hasUnsupportedPaths || hasNonExistentPaths

	switch {
	case priorRejections <= 0:
		return base
	case priorRejections == 1:
		if isSourceGrounding {
			return "[gate retry 2/3] " + trimmed +
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
		if isSourceGrounding {
			return "[gate retry 3/3 — last attempt before tools disable] " + trimmed +
				" — repeated rejections indicate the active reply does not satisfy" +
				" the contract. Make one bounded `read_file` or `code_navigate` call" +
				" for each cited unread path, or qualify each as 'unverified' next to" +
				" the citation. Do not narrate; act."
		}
		return "[gate retry 3/3 — last attempt before tools disable] " + trimmed +
			" — repeated rejections indicate the active reply does not satisfy" +
			" the contract. Either issue ONE concrete tool call that produces the" +
			" missing evidence, or write a final answer that explicitly names the" +
			" blocker. Do not narrate; act."
	}
}

// isSourceGroundingError reports whether a completion gate error text is a
// source-grounding rejection (the model cited paths without reading them).
// Used to trigger non-read tool hiding on retry.
func isSourceGroundingError(errText string) bool {
	trimmed := strings.TrimSpace(errText)
	return strings.Contains(trimmed, "not backed by a source-body read") ||
		strings.Contains(trimmed, "not read as source-body evidence")
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

			if completionGateRejectionBeforeLatestWrite(results) {
				return 2
			}

			return 1
		}

		return 1
	}

	return 2 // default retry limit; coupled to the "2/3" and "3/3" labels in escalateCompletionGateMessage
}

func (r *Runner) retryableFinalAnswerAcceptanceRejection(acceptance finalAnswerAcceptance, results []workspace.ToolResult, priorGateRejections int, allowedTools []string) bool {
	if r == nil {
		return false
	}

	if priorGateRejections >= r.completionGateRetryLimit(results) {
		return false
	}

	if r.planThenExecutePlannerPhase() && planThenExecuteAnchorRefreshFinalAnswerRejection(acceptance) {
		return r.planThenExecuteAnchorRefreshFinalAnswerPending(results)
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) ||
		len(brief.RequiredEditPaths) > 0 ||
		runnerBriefRequiresMutation(brief) {

		return false
	}

	if !retryableReadOnlyFinalAnswerRejectionForBrief(brief, acceptance) {
		return false
	}

	tools := uniquePhaseStrings(append(append([]string(nil), brief.AllowedTools...), allowedTools...))

	return len(genericReadOnlyEvidenceTools(tools)) > 0
}

func retryableReadOnlyFinalAnswerRejectionForBrief(brief workerruntime.WorkerRuntimeBrief, acceptance finalAnswerAcceptance) bool {
	if workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {
		return retryableReadOnlyFinalAnswerBlockerKind(acceptance.BlockerKind)
	}

	if !workerruntime.RuntimeContractIsArchitectureOrReview(brief.Contract) {
		return false
	}

	return strings.TrimSpace(acceptance.BlockerKind) == "non_answer_prose" &&
		containsAny(acceptance.Reason, "insufficient_evidence_answer", "answerable_evidence_reported_as_insufficient")
}

func retryableReadOnlyFinalAnswerBlockerKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "source_grounding_unmet", "answer_objective_unmet", "read_only_protocol_blocker":
		return true
	default:
		return false
	}
}

func completionGateRejectionBeforeLatestWrite(results []workspace.ToolResult) bool {
	latestWrite := -1

	for i, result := range results {
		if strings.TrimSpace(result.Error) == "" && workspace.IsWorkspaceWriteTool(result.Tool) {
			latestWrite = i
		}
	}

	if latestWrite <= 0 {
		return false
	}

	for _, result := range results[:latestWrite] {
		if strings.TrimSpace(result.Tool) == "completion_gate" {
			return true
		}
	}

	return false
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

	return len(changedPathsFromToolResults(results)) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0
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

	if len(changedPathsFromToolResults(results)) == 0 && len(r.RuntimeBrief.RequiredEditPaths) == 0 {
		return true
	}

	return hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results)
}

func (r *Runner) completionGateRepeatedBlockerKind(results []workspace.ToolResult) string {
	if r == nil || !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return "completion_gate_repeated_rejection"
	}

	if r.planThenExecutePlannerPhase() && len(r.RuntimeBrief.RequiredEditPaths) > 0 && successfulSubmitPlanResultCount(results) == 0 {
		if planThenExecuteUnreadRequiredSourceNudge(r.RuntimeBrief, results) != "" {
			return "completion_gate_required_source_reads_remaining_repeated"
		}

		if _, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			return "completion_gate_submit_plan_invalid_repeated"
		}

		return "completion_gate_submit_plan_missing_repeated"
	}

	if len(r.remainingRequiredEditPaths(results)) > 0 {
		return "completion_gate_required_paths_remaining_repeated"
	}

	if len(r.missingRequiredEditSnippets(results)) > 0 {
		return "completion_gate_required_snippets_missing_repeated"
	}

	if hasPendingVerificationAfterWrites(results, r.RuntimeBrief) ||
		sourceEditsNeedSemanticVerification(r.RuntimeBrief, results) {

		if plannerOwnsVerification(r.RuntimeBrief) {
			return "completion_gate_lane_sanity_repeated"
		}

		return "completion_gate_verification_repeated"
	}

	return "completion_gate_repeated_rejection"
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
				return fmt.Sprintf("The completion gate rejected a final answer before the corrected plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits.", strings.Join(missing, ", "), outcome, correction), true, true
			}

			return fmt.Sprintf("The completion gate rejected a final answer before the plan-submission contract (`submit_plan`) produced a valid artifact for required edit path(s): %s. Previous rejected submit_plan drafts do not count as coverage. Every required path already has source-body evidence, so read/search/discovery tools are hidden now and must not be called. Your next visible message must be exactly one parser-readable action, not the same prose. Actions written only in hidden reasoning are ignored by the tool parser and do not satisfy this contract. If the gathered evidence supports the requested changes, your next response must be a parser-readable `submit_plan` request, not prose. %s %s For missing/new files use explicit replacement edit shapes: `replace_all:true` with empty `old_text`, `new_text` as the full file body, and a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. If a required missing source/test path has only behavior/helper requirements and no substantial inline body or explicit replacement shape, answer with <<QUESTION>> naming the missing body evidence. The other valid outcome is `Decision: source-satisfied` naming every path plus current-state evidence if the requested final state is already present. Do not call read_file, search_content, context_pack, context_query, code_navigate, or any other discovery tool.", strings.Join(missing, ", "), planThenExecuteSubmitPlanCoverageSentence(), correction), true, true
		}

		if briefIsScratchpadDeliverableLane(r.RuntimeBrief) {
			return fmt.Sprintf("The completion gate rejected a final answer while non-source deliverable path(s) remain unwritten: %s. The deliverable-write contract requires full corrected content with gathered evidence plus any missing, stale, ambiguous, or out-of-scope source fact recorded as `Source verification pending:` or `## Scope Correction Required`. Answer with <<QUESTION>> only if the deliverable cannot be written from the gathered evidence plus recorded uncertainty.", strings.Join(missing, ", ")), false, true
		}

		if implementationPreWriteToolSurfaceActive(r.RuntimeBrief, results) {
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
		readPathSet := readPathsFromResults(results)
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
			"For textual tool transport, any bounded read/search/navigation action is one visible JSON object with a top-level `tool` field plus that tool's arguments, for example `" + workspace.CodeNavigateRequestExample(workspace.CapReadSymbol, "path/from/evidence.go", "SymbolFromEvidence") + "`. XML-like tags and key-value notes are not accepted tool calls unless they contain a parser-readable JSON object.",
			"For textual tool transport, a `submit_plan` action is one visible JSON object with top-level `tool`, `rationale`, and `edits` keys; `edits` must be a JSON array, not a quoted string.",
			"When submitting, each `submit_plan.edits[]` entry must use a real candidate path plus exactly one executable shape: exact `old_text`/`new_text`, replacement `start_line`/`end_line`/`content`, small inline insertion-only `insert_after_line`/`content`, or missing/new-file-only `replace_all:true`/`new_text`; include a one-sentence `purpose`. Existing source/test files must use bounded anchored edits. For large additions, use line-range replacement of unchanged anchor line(s) plus inserted content. Edit bodies must be complete executable source text; do not copy schema examples, placeholders, ellipses, or prose as field values.",
			nextAction,
			"The only non-tool closeouts are a visible answer starting exactly with `Decision: source-satisfied` plus current-state evidence for the relevant path(s), or a visible `<<QUESTION>>:` naming the exact blocker. Do not call per-edit write tools.",
		}, "\n\n"), hideReads, true
	}

	if r.unscopedNoWriteCompletionRecoveryApplies(results) {
		if r.unscopedNoWriteTerminalRecoveryReady(results) {
			return r.unscopedNoWriteTerminalRecoveryPrompt(results), true, true
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
	if r.lastCompletionGateError != "" {
		if paths := readOnlySourceGroundingUnsupportedPathsFromError(r.lastCompletionGateError); len(paths) > 0 {
			return fmt.Sprintf(
				"The completion gate rejected the answer because source paths were cited without body evidence: %s. Call one bounded `read_file` or `code_navigate` for the first unsupported path, read its actual body, then cite that evidence. If the path was from search or graph results and does not need a full read, qualify it as `unverified` next to the citation — do not just remove it from the answer without replacing it with supported evidence. No new discovery or search is needed.",
				strings.Join(paths, ", "),
			)
		}
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

	readPaths := readPathsFromResults(results)
	target := ""

	for _, rawTarget := range targets {
		candidate := normalizeRunnerPath(rawTarget)
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

// readOnlySourceGroundingUnsupportedPathsFromError extracts the unsupported
// path names from a source-grounding rejection error message. The error format
// comes from sourceGroundingRejectionReason and reads:
//
//	"the answer cites source path/line/symbol detail not backed by a
//	 source-body read: PATH1, PATH2. Use one exact read..."
//
// or from architectureUnsupportedExactDetail:
//
//	"the answer names additional implementation detail PATH1, PATH2
//	 without an exact inspected probe..."
func readOnlySourceGroundingUnsupportedPathsFromError(errText string) []string {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		return nil
	}

	// Find the colon that separates the message from the path list.
	_, after, found := strings.Cut(errText, ":")
	if !found {
		return nil
	}

	after = strings.TrimSpace(after)
	if after == "" {
		return nil
	}

	var paths []string
	for _, token := range strings.Split(after, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		// Stop at the first sentence boundary.
		if idx := strings.Index(token, ". "); idx > 0 {
			token = token[:idx]
		} else if strings.HasSuffix(token, ".") {
			token = strings.TrimSuffix(token, ".")
		}
		token = strings.TrimSpace(token)
		if token != "" {
			paths = append(paths, token)
		}
	}

	return paths
}

func postWriteVerificationPendingRecoveryPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "The completion gate rejected a final answer because")
}

func postWriteVerificationRequiredPrompt(results []workspace.ToolResult, plannerOwned bool) string {
	return postWriteVerificationPrompt(results, plannerOwned, "A workspace write completed and")
}

func postWriteVerificationPrompt(results []workspace.ToolResult, plannerOwned bool, intro string) string {
	paths := changedPathListFromToolResults(results)
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
		detail = util.Truncate(strings.TrimSpace(detail), 320)
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
