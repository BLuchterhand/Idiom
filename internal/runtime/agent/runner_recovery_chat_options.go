package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"slices"
	"strings"
)

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
		len(changedPathsFromToolResults(results)) == 0 &&
		concreteSourceEvidenceAlreadyRead(results) &&
		mutationNoWriteCompletionGateRejectionCount(results) >= 2 {

		opts = forceVisibleActionChatOptions(opts)
		opts = planExecuteSetExactMaxTokens(opts, 768)
		opts.ToolChoice = planThenExecuteTerminalAwareToolChoice(opts.ToolChoice)

		return opts
	}

	if runnerBriefRequiresMutation(brief) && len(brief.RequiredEditPaths) > 0 &&
		len(missingRequiredEditPaths(brief.RequiredEditPaths, changedPathsFromToolResults(results))) > 0 {

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
		} else if len(brief.RequiredEditPaths) == 0 && util.EqualTrimFold(opts.ToolChoice, "required") {
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

	changed := changedPathsFromToolResults(results)

	for _, path := range brief.RequiredEditPaths {
		normalized := normalizeRunnerPath(path)
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

	readEvidenceSeen := successfulToolSeen(results, workspace.ToolNameReadFile) || lastNonGateTool(results) == workspace.ToolNameReadFile || len(readPathsFromResults(results)) > 0
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

func (r *Runner) planThenExecuteSubmitPlanParseRecoveryPrompt(raw string, parseErr error, results []workspace.ToolResult) (string, bool) {
	plan, ok := r.planThenExecuteSubmitPlanParseRecoveryPlan(raw, parseErr, results)
	if !ok {
		return "", false
	}

	return RenderPlanExecuteRecoveryPlan(plan), true
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

func planThenExecuteReadyToSubmitChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteReadyToSubmitChatOptionsForBackend(opts, brief, results, true)
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
	required := nonEmptyRunnerStrings(brief.RequiredEditPaths, 12)

	var b strings.Builder

	b.WriteString("The previous native `submit_plan` tool call was rejected before it became a valid plan artifact. ")
	b.WriteString(lastSubmitPlanCorrectionRejectionBrief(result))

	if details := planThenExecuteNativeSubmitPlanCorrectionDetails(brief, result); details != "" {
		b.WriteString(details)
		b.WriteString(" ")
	}

	b.WriteString("Call native `submit_plan` again now. ")
	b.WriteString("The next tool call must be a corrected `submit_plan`. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
	b.WriteString(" Do not answer in prose, and do not call read/search/discovery, shell, verification, or per-edit write tools.")

	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}

	return b.String()
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

	var b strings.Builder

	b.WriteString("Native submit_plan tool-call transport failed before the model produced a tool call. For this one recovery retry, native tools are disabled and the textual tool parser is active.\n\n")
	b.WriteString("Emit exactly one visible JSON object for `submit_plan` now. Do not write prose before or after it. ")
	b.WriteString(planThenExecuteSubmitPlanCoverageSentence())

	if len(required) > 0 {
		b.WriteString("\n\nRequired edit paths: ")
		b.WriteString(strings.Join(required, ", "))
		b.WriteString(".")
	}

	return b.String(), true
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
		if surface.Kind == PlanExecuteRecoverySurfaceSubmitCorrection {
			opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan, workspace.ToolNameReadFile)
		} else {
			opts.Tools = filterChatOptionsTools(opts.Tools, workspace.ToolNameSubmitPlan)
		}
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

func planThenExecuteCandidateWriteOnlyRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecuteCandidateWriteOnlyRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 {
		return opts
	}

	if successfulSubmitPlanResultCount(results) > 0 {
		return opts
	}

	if len(readPathsFromResults(results)) == 0 && !successfulToolSeen(results, workspace.ToolNameReadFile) && lastNonGateTool(results) != workspace.ToolNameReadFile {
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

func planThenExecutePostReadinessRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts, brief, results, true)
}

func planThenExecutePostReadinessRecoveryChatOptionsForBackend(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, supportsNativeTools bool) llama.ChatOptions {
	if planThenExecuteUnscopedEmptySubmitPlanRejected(brief, results) {
		return planThenExecuteUnscopedEmptySubmitPlanRecoveryChatOptions(opts)
	}

	surface := DerivePlanExecuteRecoverySurface(brief, results, PlanExecutePlanningTransport{SupportsNativeTools: supportsNativeTools, TextualToolParser: !supportsNativeTools})
	if surface.Kind == PlanExecuteRecoverySurfaceAnchorRefresh {
		opts = forceVisibleActionChatOptions(opts)

		refreshTools := planThenExecuteAnchorRefreshToolNames()

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

		historyTools := planExecuteHistoryPendingToolNames()

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

	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}

	if len(refreshPaths) == 0 {
		return false
	}

	for _, path := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		normalized := normalizeRunnerPath(path)
		if _, ok := refreshPaths[normalized]; !ok {
			continue
		}

		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[normalized]; invalid {
			if !successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, normalized) {
				return true
			}

			continue
		}

		if !successfulSourceEvidencePathAfter(results, rejectedAt, normalized) {
			return true
		}
	}

	return false
}

func planThenExecuteRejectedPlanRefreshPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, rejected workspace.ToolResult, rejectedAt int) []string {
	refreshPaths := submitPlanInvalidAnchorPaths(rejected)
	for path := range submitPlanMissingSourceGroundingPaths(rejected) {
		refreshPaths[path] = struct{}{}
	}

	var out []string

	for _, rawPath := range planThenExecuteRefreshScopePathsForRejectedPlan(brief, results, refreshPaths) {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		if _, ok := refreshPaths[path]; !ok {
			continue
		}

		if _, invalid := submitPlanInvalidAnchorPaths(rejected)[path]; invalid {
			if successfulInvalidAnchorCorrectionEvidencePathAfter(results, rejectedAt, path) {
				continue
			}
		} else if successfulSourceEvidencePathAfter(results, rejectedAt, path) {
			continue
		}

		out = append(out, path)
	}

	return out
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
		normalized := normalizeRunnerPath(path)
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
		"Your next visible message must be exactly one parser-readable bounded source-body request for one of those paths. For symbol reads, use " + workspace.CodeNavigateCapabilityInstruction(workspace.CapReadSymbol) + ", a real path from the rejection/current evidence, and a real declaration pattern from current evidence. Or use `read_file` with explicit `start_line`/`end_line` only when that exact range comes from current source evidence.",
		"For large Go/source files, raw `read_file` windows are refresh evidence but are not enough to close an invalid-anchor correction; prefer the symbol/body route for the declaration you intend to edit.",
		"Do not call `submit_plan` again, answer `Decision: source-satisfied`, answer `<<QUESTION>>:`, or reply in prose until the bounded refresh returns.",
	}, "\n\n")
}

func planThenExecuteRequiredWriteOnlyRecoveryChatOptions(opts llama.ChatOptions, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) llama.ChatOptions {
	return planThenExecuteRequiredWriteOnlyRecoveryChatOptionsForBackend(opts, brief, results, true)
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

	if !(successfulToolSeen(results, workspace.ToolNameReadFile) || len(readPathsFromResults(results)) > 0 || lastNonGateTool(results) == workspace.ToolNameReadFile) {
		return false
	}

	return planThenExecuteCandidateScopeFullyRead(brief, results, 4)
}

func planThenExecuteOpenCandidateSourceCanSubmit(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if !planThenExecutePlannerBrief(brief) || len(brief.RequiredEditPaths) > 0 || len(normalizedRequiredEditPaths(brief.AllowedEditPaths)) > 0 {
		return false
	}

	return len(readPathsFromResults(results)) > 0
}

func planThenExecuteCandidateScopeFullyRead(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) bool {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return true
	}

	read := readPathsFromResults(results)
	if len(read) == 0 {
		return false
	}

	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; !ok {
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
	if strings.TrimSpace(current) == "" || util.EqualTrimFold(current, "required") {
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
