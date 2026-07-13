package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type runnerTurnController struct {
	runner        *runExecution
	requestBudget *runnerRequestBudgetState
	recorder      *runnerTurnRecorder
}

type runnerToolRejection struct {
	Summary     string
	Error       string
	Disposition toolfacts.ToolRejectionDisposition
	Facts       *toolfacts.ToolResultFacts
}

func (rejection runnerToolRejection) toolResult(tool string) workspace.ToolResult {
	return resultWithToolRejectionDisposition(workspace.ToolResult{
		Tool:    strings.TrimSpace(tool),
		Summary: rejection.Summary,
		Error:   rejection.Error,
		Facts:   rejection.Facts.Clone(),
	}, rejection.Disposition)
}

func (c *runnerTurnController) rejectTool(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, rejection runnerToolRejection) (string, error, bool) {
	if c == nil || c.runner == nil || c.recorder == nil {
		return "", nil, false
	}
	state := &c.runner.state

	result := rejection.toolResult(req.Tool)
	result = c.runner.bindRetainedMutationRejection(req, result)
	currentFingerprint := c.runner.ensureTurnRequest(req)
	if c.requestBudget != nil && strings.TrimSpace(currentFingerprint) != "" {
		c.requestBudget.recordRejectedFingerprint(currentFingerprint, req, result)
	}

	countAsDeadEnd := result.Facts == nil || !result.Facts.RejectionDisposition.CarriesRejectedWriteIntent()
	if countAsDeadEnd {
		if action := toolActionClass(req); action != "" && state.lifecycle.progress.rejectedActions != nil {
			state.lifecycle.progress.rejectedActions[action]++
		}
		state.lifecycle.progress.consecutiveRejected++
	}

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, "tool_rejected", runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})

	if state.lifecycle.progress.consecutiveRejected >= 2 {
		if !state.lifecycle.recovery.rejectionNudgeUsed {
			if nudge := c.runner.buildRejectionRecoveryNudge(req, c.snapshotResults()); nudge != "" {
				state.lifecycle.recovery.rejectionNudgeUsed = true
				state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: nudge})

				c.runner.emitEvent(RunEvent{Role: "rejection_recovery_nudge", Content: nudge})

				state.lifecycle.progress.consecutiveRejected = 0

				return "", nil, false
			}
		}

		// Conversation lanes: the "last chance before harness ends"
		// force-complete contradicts the investigation directive.
		// Instead of force-completing, let the model continue.
		if c.runner != nil && !c.runner.lanePolicy().forceAnswerAfterRejection {
			state.lifecycle.progress.consecutiveRejected = 0
			return "", nil, false
		}

		answer, err := c.completeWithoutMoreToolsWithDefaultCloseout()

		return answer, err, true
	}

	return "", nil, false
}

func (c *runnerTurnController) rejectCompletion(step int, activeToolCall *llama.ToolCall, reply llama.ChatMessage, result workspace.ToolResult) {
	if c == nil || c.runner == nil || c.recorder == nil {
		return
	}
	state := &c.runner.state

	state.recordCompletionGateRejection(result)
	c.runner.emitCompletionGateRecoveryState()
	state.metrics.CompletionGateRejections++

	historyReply := reply
	if !c.currentOpts().PreserveThinking {
		historyReply.ReasoningContent = ""
	}

	if !completionGateRejectedReplyHiddenFromHistory(c.runner, result) {
		state.turnMessages = append(state.turnMessages, completionGateRejectedHistoryMessage(c.runner, historyReply, result, c.snapshotResults()))
	}

	c.runner.emitThinkingTrace(step, "completion_gate_reply_preserved", historyReply, c.currentOpts(), true)

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, workspace.ToolRequest{Tool: completionGateToolName}, result, "completion_gate_rejected", runnerRecordOptions{
		IncrementNoProgress: true,
		SetLastToolKey:      false,
	})
}

func completionGateRejectedReplyHiddenFromHistory(r *runExecution, result workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Tool) != completionGateToolName {
		return false
	}

	return readOnlyDirectAnswerLane(r.state.runtimeBrief) && !runnerBriefRequiresMutation(r.state.runtimeBrief)
}

func completionGateRejectedHistoryMessage(r *runExecution, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) llama.ChatMessage {
	message := llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          reply.Content,
		ReasoningContent: reply.ReasoningContent,
	}
	if !shouldCompactRejectedCompletionReply(r, reply, result, results) {
		return message
	}

	content := strings.TrimSpace(reply.Content)

	message.Content = fmt.Sprintf(
		"[omitted rejected final answer: %d chars. The completion gate rejected it before required workspace changes were recorded: %s. Next action must satisfy the gate with an allowed tool call or a concrete blocker. Preview: %s]",
		len(content),
		textutil.Truncate(strings.TrimSpace(result.Error), 240),
		textutil.Truncate(strings.TrimSpace(content), 500),
	)
	message.ReasoningContent = ""

	return message
}

func shouldCompactRejectedCompletionReply(r *runExecution, reply llama.ChatMessage, result workspace.ToolResult, results []workspace.ToolResult) bool {
	if r == nil || strings.TrimSpace(result.Tool) != completionGateToolName {
		return false
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) || len(r.state.runtimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if r.implementationCompletionState(results).HasChanges() {
		return false
	}

	content := strings.TrimSpace(reply.Content)
	if len(content) < 4000 {
		return false
	}

	return strings.TrimSpace(result.Error) != ""
}

func (c *runnerTurnController) recordParseFailure(step int, activeToolCall *llama.ToolCall, raw string, parseErr error, transport string, parserFailureClass autonomy.ParserFailureClass, suppressModelMessage bool) {
	if c == nil || c.recorder == nil || parseErr == nil {
		return
	}

	decision := malformedToolParseFailureDecision(raw, parseErr, transport, parserFailureClass)

	c.recorder.activeToolCall = activeToolCall

	if c.runner != nil && c.runner.state.autonomyRecorder != nil {
		c.runner.state.autonomyRecorder.emitPolicyResult(decision.Request, decision.Policy)
	}

	c.recorder.recordWithOptions(step, decision.Request, decision.Result, parserRecoveryRecordReason, runnerRecordOptions{
		CountRejected:        true,
		SetLastToolKey:       false,
		SuppressModelMessage: suppressModelMessage,
	})
}

const (
	parserRecoveryToolName         = "tool_parse"
	parserRecoveryRecordReason     = "tool_parse_rejected"
	parserRecoverySummaryMalformed = "Malformed tool request rejected"
	parserRecoverySummaryUnsafe    = "Unsafe tool-shaped request rejected"
)

type malformedToolPolicyDecision struct {
	Request workspace.ToolRequest
	Result  workspace.ToolResult
	Policy  runnerAutonomyPolicyEventInput
}

func (r *runExecution) malformedToolRecoveryPrompt(raw string, parseErr error, results []workspace.ToolResult) string {
	if r == nil || parseErr == nil {
		return ""
	}
	state := &r.state

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var lines []string

	if IsTextualToolCall(raw) {
		lines = append(lines,
			"The last response was rejected by the tool parser because it used XML/HTML/DSML-style tool-call markup instead of the active tool transport.",
			malformedToolRecoveryActionInstruction(recoveryToolTransportMode(state.planningTransport())),
		)
	} else {
		lines = append(lines,
			"The last response was rejected by the tool parser because it was not one valid tool call.",
			malformedToolRecoveryActionInstruction(recoveryToolTransportMode(state.planningTransport())),
		)
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		if ledger := readOnlyEvidenceLedgerGuidanceForBrief(brief, results); ledger != "" {
			lines = append(lines, ledger)
		}
		if state.lastCompletionGateSourceGrounding.hasTypedSignal() {
			if recovery := state.lastCompletionGateSourceGrounding.RecoveryPrompt(""); recovery != "" {
				lines = append(lines, "The source-grounding recovery contract remains active:\n"+recovery)
			}
		}
	}

	return strings.Join(lines, "\n\n")
}

func (r *runExecution) unsafeToolShapeRecoveryPrompt(normalization toolRequestSyntaxNormalization, results []workspace.ToolResult) string {
	if r == nil || strings.TrimSpace(normalization.EventOutcome) != toolRequestNormalizerOutcomeUnsafeToInfer {
		return ""
	}
	state := &r.state

	lines := []string{
		"The last response looked like tool-shaped arguments, but the harness cannot safely infer that into an executable tool call.",
	}

	if implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0) {
		lines = append(lines, "Do not put tool arguments in answer prose. The active request is the workspace write follow-up; prose answers to the earlier identification question do not satisfy this phase. Use the active native tool-call transport for one callable write or "+implementationTargetWriteReadySourceProbeToolsPhrase+" tool, or answer <<QUESTION>> with the exact blocker/no-op evidence.")
		lines = append(lines, "The implementation lane is target-write-ready. "+implementationTargetWriteReadyRecoveryInstruction(results))
	} else {
		lines = append(lines, malformedToolRecoveryActionInstruction(recoveryToolTransportMode(state.planningTransport())))
	}

	return strings.Join(lines, "\n\n")
}

func (r *runExecution) ambiguousModelOutputRecoveryPrompt(normalization toolRequestSyntaxNormalization, results []workspace.ToolResult) string {
	if r == nil || strings.TrimSpace(normalization.EventOutcome) != toolRequestNormalizerOutcomeUnsafeToInfer {
		return ""
	}

	if implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0) {
		lines := []string{
			"The last response was prose, not an implementation action. The harness did not infer or execute any write from it.",
			"The active request is the workspace write follow-up; do not answer the earlier identification question again. Prose is invalid in target-write-ready unless it is the exact <<QUESTION>> blocker/no-op shape.",
			"The implementation lane is target-write-ready. " + implementationTargetWriteReadyRecoveryInstruction(results),
			"If the previous text was intended as an edit, issue it through one active write tool call. If it was intended as a blocker or no-op, answer <<QUESTION>> with the exact blocker/no-op evidence. If one edit-safety fact is still missing, take one bounded " + implementationTargetWriteReadySourceProbeToolsPhrase + " probe with `named_gap` for that exact fact.",
		}
		return strings.Join(lines, "\n\n")
	}

	lines := []string{
		"The last response was not an executable tool call and was not accepted as a final answer. The harness did not infer or execute any action from it.",
	}

	return strings.Join(lines, "\n\n")
}

func (r *runExecution) unsafeToolShapeRecoveryBeforeCompletionGateActive(results []workspace.ToolResult) bool {
	if r == nil {
		return false
	}

	return implementationTargetWriteReadySurfaceActive(r.state.runtimeBrief, results, len(r.completionChangedPaths(results)) > 0)
}

func malformedToolParseFailureDecision(raw string, parseErr error, transport string, parserFailureClass autonomy.ParserFailureClass) malformedToolPolicyDecision {
	policyTransport, parserFailureClass := parserPolicyMetadata(raw, parseErr, transport, parserFailureClass)

	return malformedToolPolicyDecision{
		Request: workspace.ToolRequest{Tool: parserRecoveryToolName},
		Result: workspace.ToolResult{
			Tool:    parserRecoveryToolName,
			Summary: parserRecoverySummaryMalformed,
			Error:   toolParseErrorFeedback(raw, parseErr, ""),
		},
		Policy: runnerAutonomyPolicyEventInput{
			Decision:      autonomy.PolicyReject,
			PackID:        autonomy.ContractPackParserToolRequest,
			InvariantID:   malformedToolPolicyInvariantToolRequest,
			ReasonCode:    "malformed_tool_request",
			Transport:     policyTransport,
			ParserFailure: parserFailureClass,
			Result:        "rejected",
			ErrorClass:    parserRecoveryToolName,
			Source:        "malformed_tool_policy",
		},
	}
}

func unsafeToolShapeDecision(normalization toolRequestSyntaxNormalization, targetWriteReady bool) malformedToolPolicyDecision {
	transport, parserFailureClass := parserPolicyMetadata(normalization.Raw, nil, normalization.EventTransport, autonomy.ParserFailureUnsupportedTransport)
	if parserFailureClass == "" || parserFailureClass == autonomy.ParserFailureNone {
		parserFailureClass = autonomy.ParserFailureUnsupportedTransport
	}

	return malformedToolPolicyDecision{
		Request: workspace.ToolRequest{Tool: parserRecoveryToolName},
		Result: workspace.ToolResult{
			Tool:    parserRecoveryToolName,
			Summary: parserRecoverySummaryUnsafe,
			Error:   unsafeToolShapeErrorFeedback(transport, targetWriteReady),
		},
		Policy: runnerAutonomyPolicyEventInput{
			Decision:      autonomy.PolicyReject,
			PackID:        autonomy.ContractPackParserToolRequest,
			InvariantID:   malformedToolPolicyInvariantToolRequest,
			ReasonCode:    "unsafe_tool_shape",
			Transport:     transport,
			ParserFailure: parserFailureClass,
			Result:        "rejected",
			ErrorClass:    parserRecoveryToolName,
			Source:        "unsafe_tool_shape_policy",
		},
	}
}

func malformedToolRecoveryActionInstruction(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return "Do not emit tags such as `<n_tool_calls>`, `<invoke>`, `<janus_tool>`, or textual tool JSON as answer prose. If a tool is useful, use the active native tool-call transport exposed by this runtime; otherwise answer in plain user-visible prose."
	}
	return "If a tool is useful, emit exactly one parser-readable tool call through the active tool transport; otherwise answer in plain user-visible prose."
}

func workspaceLookupTransportInstruction(transportMode string) string {
	if strings.EqualFold(strings.TrimSpace(transportMode), workerruntime.RecoveryToolTransportNative) {
		return "If a workspace lookup is needed, use the active native tool-call transport and wait for the result."
	}
	return "If a workspace lookup is needed, issue it as a parser-readable tool call with the `tool` field and wait for the result."
}

func toolParseErrorFeedback(raw string, parseErr error, transportMode string) string {
	if parseErr == nil {
		return modelFacingToolParseError("Emit exactly one valid tool call or answer normally.", transportMode)
	}

	message := parseErr.Error()
	lower := strings.ToLower(message + "\n" + raw)

	if strings.Contains(lower, "toolrequest.edits") && strings.Contains(lower, "string") {
		return message + ". submit_plan.edits was emitted as a quoted string. Emit `edits` as a real JSON array value, not a string. Each edit object must use a real path and exactly one executable edit shape: insert_after_line/content for small inline insertions, old_text/new_text for exact replacements or deletions, start_line/end_line/content for an inclusive current-source range, or replace_all with full new_text for missing/new files only. Existing source/test files must use bounded anchored edits. For native tool-call backends, pass the submit_plan arguments object directly with `rationale` and an `edits` array. Edit bodies must be complete executable source text, not placeholders or prose."
	}

	if IsTextualToolCall(raw) {
		return modelFacingToolParseError(message+". The payload looks like XML/HTML/DSML-style tool-call markup. Do not emit tags such as <n_tool_calls>, <invoke>, or <janus_tool>. Use the runtime's active tool-call mechanism for tools, or answer in plain prose.", transportMode)
	}

	return modelFacingToolParseError(message+". Emit exactly one valid tool call or answer normally.", transportMode)
}

func unsafeToolShapeErrorFeedback(transport autonomy.ToolTransport, targetWriteReady bool) string {
	if targetWriteReady {
		return modelFacingToolParseError("The payload was tool-shaped, but it was not an executable tool call. Use the active native tool-call transport for one callable write/read/search tool, or answer <<QUESTION>> with the exact blocker/no-op evidence. Prose final answers cannot satisfy the current target-write-ready phase.", workerruntime.RecoveryToolTransportNative)
	}

	switch transport {
	case autonomy.ToolTransportNativeCall:
		return modelFacingToolParseError("The payload was tool-shaped, but it was not an executable native tool call. Use the active native tool-call transport for tools, or answer in plain prose.", workerruntime.RecoveryToolTransportNative)
	default:
		return modelFacingToolParseError("The payload was tool-shaped, but it was unsafe to infer as a tool call. Emit exactly one valid tool call through the active transport, or answer in plain prose.", "")
	}
}

func (c *runnerTurnController) recordUnsafeToolShape(step int, activeToolCall *llama.ToolCall, normalization toolRequestSyntaxNormalization, suppressModelMessage bool) {
	if c == nil || c.recorder == nil {
		return
	}

	decision := unsafeToolShapeDecision(normalization, true)

	c.recorder.activeToolCall = activeToolCall

	if c.runner != nil && c.runner.state.autonomyRecorder != nil {
		c.runner.state.autonomyRecorder.emitPolicyResult(decision.Request, decision.Policy)
	}

	c.recorder.recordWithOptions(step, decision.Request, decision.Result, parserRecoveryRecordReason, runnerRecordOptions{
		CountRejected:        true,
		SetLastToolKey:       false,
		SuppressModelMessage: suppressModelMessage,
	})
}

func (c *runnerTurnController) recordRejectedResult(step int, activeToolCall *llama.ToolCall, req workspace.ToolRequest, result workspace.ToolResult, reason string) {
	if c == nil || c.recorder == nil {
		return
	}

	if c.runner != nil {
		result = c.runner.bindRetainedMutationRejection(req, result)
	}

	if c.requestBudget != nil && c.runner != nil {
		currentFingerprint := c.runner.ensureTurnRequest(req)
		if strings.TrimSpace(currentFingerprint) != "" {
			c.requestBudget.recordRejectedFingerprint(currentFingerprint, req, result)
		}
	}

	c.recorder.activeToolCall = activeToolCall
	c.recorder.recordWithOptions(step, req, result, reason, runnerRecordOptions{
		CountRejected:  true,
		SetLastToolKey: true,
	})
}

func (c *runnerTurnController) snapshotResults() []workspace.ToolResult {
	if c == nil || c.runner == nil {
		return nil
	}

	return c.runner.state.collectedResults
}

func (c *runnerTurnController) currentMessages() []llama.ChatMessage {
	if c == nil || c.runner == nil {
		return nil
	}

	return c.runner.state.turnMessages
}

func (c *runnerTurnController) currentOpts() llama.ChatOptions {
	if c == nil || c.runner == nil {
		return llama.ChatOptions{}
	}

	return c.runner.state.opts
}

func (c *runnerTurnController) completeWithoutMoreToolsWithDefaultCloseout() (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	state := &c.runner.state

	mode := c.noToolsCloseoutMode()
	reason := "Tool budget is exhausted. Tools are now disabled."
	if mode == noToolsCloseoutDeterministicRequired && state.policy.shouldStopForOperationalModelCalls(state.metrics.ModelCalls) {
		reason = "Operational model-call limit is exhausted. Tools are now disabled."
	}
	if blocker, ok := c.incompleteMutationCloseoutBlocker(reason); ok {
		return blocker, nil
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(state.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), reason, &state.metrics, mode)
}

func (c *runnerTurnController) completeWithoutMoreToolsWithReason(prompt string) (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	state := &c.runner.state
	if blocker, ok := c.incompleteMutationCloseoutBlocker(prompt); ok {
		return blocker, nil
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(state.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), prompt, &state.metrics, c.noToolsCloseoutMode())
}

func (c *runnerTurnController) completeWithoutMoreToolsWithReasonMode(prompt string, mode noToolsCloseoutMode) (string, error) {
	if c == nil || c.runner == nil {
		return "", nil
	}
	if mode == "" {
		mode = c.noToolsCloseoutMode()
	}
	state := &c.runner.state
	if blocker, ok := c.incompleteMutationCloseoutBlocker(prompt); ok {
		return blocker, nil
	}

	return c.runner.completeWithoutMoreToolsWithReasonMode(state.ctx, c.currentMessages(), c.currentOpts(), c.snapshotResults(), prompt, &state.metrics, mode)
}

func (c *runnerTurnController) incompleteMutationCloseoutBlocker(stopReason string) (string, bool) {
	if c == nil || c.runner == nil || !runnerBriefRequiresMutation(c.runner.state.runtimeBrief) {
		return "", false
	}

	completionState := c.runner.implementationCompletionState(c.snapshotResults())
	if completionState.CloseoutReady() {
		return "", false
	}

	return c.runner.implementationContractCloseoutBlocker(c.snapshotResults(), stopReason)
}

func (c *runnerTurnController) noToolsCloseoutMode() noToolsCloseoutMode {
	if c == nil || c.runner == nil {
		return noToolsCloseoutModelAllowed
	}

	if c.runner.state.policy.shouldStopForOperationalModelCalls(c.runner.state.metrics.ModelCalls) {
		return noToolsCloseoutDeterministicRequired
	}

	return noToolsCloseoutModelAllowed
}

const (
	ReasonCompletionGateRejected failures.Reason = 2001 + iota
	ReasonAgentTextualToolCall
	ReasonAgentToolRequest
	ReasonAgentEmptyResponse
)

var (
	ErrCompletionGateRejected = failures.New(failures.FailedPrecondition, ReasonCompletionGateRejected, "completion gate rejected final answer")
	ErrAgentTextualToolCall   = failures.New(failures.FailedPrecondition, ReasonAgentTextualToolCall, "agent emitted textual tool call markup instead of answer")
	ErrAgentToolRequest       = failures.New(failures.FailedPrecondition, ReasonAgentToolRequest, "agent emitted a tool request instead of answer")
	ErrAgentEmptyResponse     = failures.New(failures.FailedPrecondition, ReasonAgentEmptyResponse, "agent returned an empty response")
)
