package agent

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

const scopedImplementationLargeReadLineThreshold = 800

const scopedImplementationLargeReadByteThreshold = 96 * 1024

const scopedImplementationBoundedReadWindowLines = 180

const scopedImplementationBoundedReadLineCharCap = 400

const forceAnswerTailGatheredEvidence = "Write the normal final answer from the gathered evidence. If one exact claim remains unsupported, state that uncertainty after the supported answer. Do not emit XML tool-call tags, tool JSON, or tool-request syntax — write only the plain user-visible final answer."

type terminalAnswerOutcome string

const (
	terminalAnswerAccepted         terminalAnswerOutcome = "accepted"
	terminalAnswerBlocked          terminalAnswerOutcome = "blocked"
	terminalAnswerTransportBlocked terminalAnswerOutcome = "transport_protocol_blocked"
)

type terminalAnswerDecision struct {
	Outcome         terminalAnswerOutcome
	RawAnswer       string
	PresentedAnswer string
	StopReason      string
	Acceptance      finalAnswerAcceptance
	BlockerResult   workspace.ToolResult
	OverrideSource  string
	RetryExhausted  bool
}

func (r *runExecution) classifyTerminalAnswer(answer string, stopReason string, results []workspace.ToolResult) terminalAnswerDecision {
	stopReason = strings.TrimSpace(stopReason)
	if stopReason == "" {
		stopReason = "completed"
	}

	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	decision := terminalAnswerDecision{
		Outcome:         terminalAnswerAccepted,
		RawAnswer:       answer,
		PresentedAnswer: answer,
		StopReason:      stopReason,
		Acceptance:      acceptance,
	}

	if terminalAnswerContainsTransportProtocol(answer) {
		acceptance = finalAnswerAcceptanceWithTerminalOverride(
			acceptance,
			finalAnswerAcceptanceRejected,
			string(turnViolationTransportProtocolOutput),
			"the final answer contained tool-call transport markup instead of user-visible answer prose",
		)
		decision.Outcome = terminalAnswerTransportBlocked
		decision.PresentedAnswer = "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?"
		decision.StopReason = acceptance.BlockerKind
		decision.Acceptance = acceptance

		return decision
	}

	if acceptance.BlockerKind == "" {
		return decision
	}

	if recovered, recoveredAcceptance, ok := r.recoveredReadOnlyTerminalAnswer(answer, results, acceptance); ok {
		decision.Outcome = terminalAnswerAccepted
		decision.RawAnswer = recovered
		decision.PresentedAnswer = recovered
		decision.StopReason = "read_only_terminal_recovery"
		decision.Acceptance = recoveredAcceptance

		return decision
	}

	if _, ok := finalAnswerAcceptanceViolationKind(acceptance); !ok {
		return decision
	}

	decision.Outcome = terminalAnswerBlocked
	decision.StopReason = acceptance.BlockerKind
	if hostOwnedQuestionCloseoutStopReason(stopReason, answer) {
		decision.StopReason = stopReason
	}
	decision.RetryExhausted = turnFinalAnswerAcceptanceRetryable(acceptance)

	if !r.terminalQuestionAnswerCanRemain(answer, results) && r.terminalAnswerShouldOverrideRejectedRaw(answer, acceptance) {
		if blockerResult, ok := finalAnswerCompletionGateProjection(
			acceptance,
			"Final answer rejected by blocker check",
			r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, answer, results),
		); ok {
			decision.BlockerResult = blockerResult
			decision.PresentedAnswer = r.completionGateRepeatedBlockerAnswer(blockerResult, results)
			decision.OverrideSource = "finish"
		}
	}

	return decision
}

func (r *runExecution) terminalQuestionAnswerCanRemain(answer string, results []workspace.ToolResult) bool {
	if !questionProtocolAnswer(answer) {
		return false
	}
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return true
	}
	completion := r.implementationCompletionStateForTerminalAnswer(answer, results)
	return completion.TerminalDisposition == implementationTerminalDispositionExactBlocker && completion.CloseoutReady()
}

func hostOwnedQuestionCloseoutStopReason(stopReason string, answer string) bool {
	if !questionProtocolAnswer(answer) {
		return false
	}

	switch strings.TrimSpace(stopReason) {
	case "implementation_tool_step_budget_blocked",
		"tool_step_budget_exhausted",
		"model_call_budget_exhausted",
		"operational_model_call_limit_exceeded",
		"duration_budget_exhausted",
		"no_progress_budget_exhausted":
		return true
	default:
		return false
	}
}

func (r *runExecution) recoveredReadOnlyTerminalAnswer(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) (string, finalAnswerAcceptance, bool) {
	if r == nil {
		return "", finalAnswerAcceptance{}, false
	}
	if !turnFinalAnswerAcceptanceRecoverable(acceptance) {
		return "", finalAnswerAcceptance{}, false
	}

	return r.readOnlyTerminalRecoveryCandidate(acceptance, results)
}

func (r *runExecution) readOnlyTerminalRecoveryCandidate(acceptance finalAnswerAcceptance, results []workspace.ToolResult) (string, finalAnswerAcceptance, bool) {
	if r == nil {
		return "", finalAnswerAcceptance{}, false
	}

	// Recovery may relay an already completed child answer, but the harness
	// must not synthesize a new substantive answer from evidence after the
	// parent's model response was rejected.
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Tool) != workspace.ToolNameTask || strings.TrimSpace(result.Error) != "" {
			continue
		}
		candidate := strings.TrimSpace(result.Output)
		if candidate == "" {
			continue
		}
		recoveredAcceptance := r.evaluateFinalAnswerAcceptance(candidate, results)
		if recoveredAcceptance.BlockerKind == "" {
			return candidate, recoveredAcceptance, true
		}
	}

	return "", finalAnswerAcceptance{}, false
}

func terminalBlockedStatusShouldEmit(alreadyEmitted bool, lastStatus string, lastBlockerKind string, terminalBlockerKind string) bool {
	return !alreadyEmitted || strings.TrimSpace(lastStatus) != "blocked"
}

func (r *runExecution) terminalAnswerShouldOverrideRejectedRaw(answer string, acceptance finalAnswerAcceptance) bool {
	if acceptance.BlockerKind == "" {
		return false
	}

	if r != nil && strings.HasPrefix(r.agentName(), "child:") && !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return false
	}

	return acceptance.BlockerKind != string(turnViolationNonAnswerProse) ||
		looksLikeMetaIntentRestatement(answer) ||
		looksLikeGateSatisfactionMetaFinalAnswer(answer) ||
		looksLikeEmbeddedQuestionProtocolFinalAnswer(answer) ||
		looksLikeLowValueSourceInventoryNonAnswer(answer) ||
		finishShouldOverrideSourceEcho(answer)
}

func terminalAnswerContainsTransportProtocol(answer string) bool {
	return IsTextualToolCall(answer) ||
		strings.Contains(answer, "<n_tool") ||
		strings.Contains(answer, "<n_invoke") ||
		strings.Contains(answer, "<janus_tool") ||
		strings.Contains(answer, "<invoke name=") ||
		strings.Contains(answer, "DSML")
}

func (r *runExecution) decorateTerminalAnswerDecision(decision terminalAnswerDecision, results []workspace.ToolResult) string {
	if decision.Outcome == terminalAnswerTransportBlocked {
		return decision.PresentedAnswer
	}

	return r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     decision.PresentedAnswer,
		StopReason: decision.StopReason,
		Brief:      r.state.runtimeBrief,
		Results:    results,
		UserFacing: r.userFacingFinalAnswer(),
	})
}

func (r *runExecution) emitFinalAnswerTelemetry(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) {
	if r == nil || r.runner.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}

	lane := textutil.FirstNonEmpty(strings.TrimSpace(r.state.runtimeBrief.Lane), strings.TrimSpace(r.state.runtimeBrief.TaskFamily), r.agentName())
	contract := textutil.FirstNonEmpty(strings.TrimSpace(r.state.runtimeBrief.Contract), string(workerruntime.ContractDirectAnswer))
	evidencePaths := acceptance.EvidencePaths

	var presentationEvidencePaths []string

	if readOnlyDirectAnswerLane(r.state.runtimeBrief) && len(evidencePaths) == 0 {
		presentationEvidencePaths = newToolResultEvidenceProjection(results).readOnlyDirectAnswerPresentationEvidencePaths(r.state.runtimeBrief, 8)
	}

	addPresentationEvidencePaths := func(payload map[string]any) {
		if len(presentationEvidencePaths) > 0 {
			payload["presentation_evidence_paths"] = presentationEvidencePaths
		}
	}
	acceptanceFields := finalAnswerAcceptanceEventFields(acceptance)

	if acceptance.BlockerKind != "" {
		readiness := map[string]any{
			"lane":           lane,
			"ready":          false,
			"confidence":     "blocked",
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(readiness)

		for key, value := range acceptanceFields {
			readiness[key] = value
		}

		r.emitEvent(marshalRunEvent("readiness_check", readiness))

		critic := map[string]any{
			"contract":       contract,
			"passed":         false,
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(critic)

		for key, value := range acceptanceFields {
			critic[key] = value
		}

		r.emitEvent(marshalRunEvent("final_answer_critic", critic))

		rails := map[string]any{
			"lane":           lane,
			"task_family":    textutil.FirstNonEmpty(strings.TrimSpace(r.state.runtimeBrief.TaskFamily), contract),
			"contract":       contract,
			"passed":         false,
			"action":         "block",
			"reason":         acceptance.Reason,
			"blocker_kind":   acceptance.BlockerKind,
			"evidence_paths": evidencePaths,
		}
		addPresentationEvidencePaths(rails)

		for key, value := range acceptanceFields {
			rails[key] = value
		}

		r.emitEvent(marshalRunEvent("control_rails_enforcement", rails))

		return
	}

	confidence := "medium"

	if len(evidencePaths) >= 2 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "partial findings from this turn") {
		// Conversation lanes have no semantic relevance evaluation; the
		// harness can verify answer form but not answer quality. Confidence
		// must not claim "high" when relevance is unevaluated.
		if conversationLaneWithoutSemanticEval(r.state.runtimeBrief) {
			confidence = "medium"
		} else {
			confidence = "high"
		}
	}

	readiness := map[string]any{
		"lane":           lane,
		"ready":          true,
		"confidence":     confidence,
		"action":         "answer",
		"reason":         "answer satisfies lane contract and evidence gate",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(readiness)

	for key, value := range acceptanceFields {
		readiness[key] = value
	}

	r.emitEvent(marshalRunEvent("readiness_check", readiness))

	critic := map[string]any{
		"contract":       contract,
		"passed":         true,
		"action":         "accept",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(critic)

	for key, value := range acceptanceFields {
		critic[key] = value
	}

	r.emitEvent(marshalRunEvent("final_answer_critic", critic))

	rails := map[string]any{
		"lane":           lane,
		"task_family":    textutil.FirstNonEmpty(strings.TrimSpace(r.state.runtimeBrief.TaskFamily), contract),
		"contract":       contract,
		"passed":         true,
		"action":         "accept",
		"evidence_paths": evidencePaths,
	}
	addPresentationEvidencePaths(rails)

	for key, value := range acceptanceFields {
		rails[key] = value
	}

	r.emitEvent(marshalRunEvent("control_rails_enforcement", rails))
}

func (r *runExecution) emitFinalAnswerBlockerOverride(acceptance finalAnswerAcceptance, replacementStopReason string, source string) {
	if r == nil || r.runner.OnEvent == nil || acceptance.BlockerKind == "" {
		return
	}

	payload := map[string]any{
		"summary":                      "model final answer rejected and replaced with deterministic blocker narrative",
		"source":                       strings.TrimSpace(source),
		"original_acceptance_contract": acceptance.SchemaVersion,
		"original_acceptance_decision": string(acceptance.Decision),
		"original_blocker_kind":        strings.TrimSpace(acceptance.BlockerKind),
		"original_reason":              strings.TrimSpace(acceptance.Reason),
		"original_evidence_paths":      acceptance.EvidencePaths,
		"replacement_stop_reason":      strings.TrimSpace(replacementStopReason),
		"raw_answer_logged":            false,
	}
	if acceptance.ObjectiveRelevanceStatus != "" {
		payload["objective_relevance"] = acceptance.ObjectiveRelevanceStatus
	}

	if len(acceptance.RelevantSourceBodyPaths) > 0 {
		payload["relevant_source_body_paths"] = textutil.LimitSlice(acceptance.RelevantSourceBodyPaths, 8)
	}

	r.emitEvent(marshalRunEvent("final_answer_blocker_override", payload))
}

// requiredEditPathsProgressMessage returns a compact authoritative
// progress checklist that the runner injects into turnMessages after each
// successful write tool result on a brief that has required_edit_paths.
// The message gives the model running ground truth — paths actually
// changed (✓) and paths still pending (✗) — derived from
// the tool-result evidence projection rather than the model's conversation
// memory. Returns empty string when no checklist is appropriate (no
// required paths in the brief, write was unsuccessful, no state change
// since the last write, etc.).
//
// Why we inject only after a successful write rather than every turn:
// per-turn injection accumulates stale checklists in the conversation;
// after-write injection is exactly when the running state changes and
// the model needs the truth most.
func (r *runExecution) requiredEditPathsProgressMessage(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return ""
	}

	required := r.state.runtimeBrief.RequiredEditPaths
	if len(required) == 0 {
		return ""
	}

	if !workspaceWriteResultCommitted(req, result) {
		return ""
	}

	completion := r.implementationCompletionState(results)
	if len(completion.ChangedPaths) == 0 {
		return ""
	}

	var (
		done    []string
		pending []string
	)

	done = completion.ChangedPaths
	pending = completion.MissingRequiredPaths

	if len(done) == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString("Required edit paths progress (authoritative — derived from successful write tool results, not from model claims):\n")

	for _, p := range done {
		b.WriteString("  ✓ ")
		b.WriteString(p)
		b.WriteString("\n")
	}

	for _, p := range pending {
		b.WriteString("  ✗ ")
		b.WriteString(p)
		b.WriteString(" (pending)\n")
	}

	switch {
	case len(pending) == 0:
		b.WriteString("All required edit paths are written. Submit the standard final answer with the changed-paths summary now; do not call further tools beyond brief verification.")
	case len(pending) == 1:
		b.WriteString("One required edit path remains. The remaining-path write contract must target only that path, or report a concrete blocker for that path with <<QUESTION>>:.")
	default:
		b.WriteString(fmt.Sprintf("%d required edit paths remain. Continue with one remaining-path write contract per path; do not declare completion until each is written or report a per-path concrete blocker with <<QUESTION>>:.", len(pending)))
	}

	return b.String()
}

// finalAnswerBlockerErrorText returns the directional rejection message
// embedded in a synthesized completion_gate ToolResult when the runner
// overrides a fabricated final answer with a deterministic blocker
// narrative. It mirrors the existing mutation-completion gate text so the
// model gets the same shape of guidance it would have seen via a
// rejected-completion retry, plus an explicit note that its claimed
// changed-paths summary did not match reality.
func (r *runExecution) finalAnswerBlockerErrorText(kind string, reason string, answer string, results []workspace.ToolResult) string {
	kind = strings.TrimSpace(kind)
	reason = strings.TrimSpace(reason)
	if finalAnswerObjectiveRelevanceAdvisoryKind(kind) {
		return "objective relevance diagnostics are advisory. Accept the model's final answer unless a mechanical blocker such as missing source-body evidence, protocol markup, permissions, or required evidence applies."
	}

	switch kind {
	case string(turnViolationRequiredEditPathsRemaining):
		missing := r.implementationCompletionState(results).MissingRequiredPaths
		if r.planThenExecutePlannerPhase() {
			base := "the plan-execute planner contract is not fulfilled. The final answer claimed completion, but the harness has no successful submit_plan edit entry for the following required edit path(s): "
			if len(missing) > 0 {
				base += quotedRunnerStrings(missing)
			} else {
				base += "(see required_edit_paths in the brief)"
			}

			base += ". Submit a parser-readable submit_plan using edits[] for changed paths and top-level source_satisfied_paths for already-final paths, or report a concrete blocker per skipped path with <<QUESTION>>:."

			return base
		}

		base := "the implementation contract is not fulfilled. The final answer claimed completion, but the harness has no successful write tool result for the following required edit path(s): "
		if len(missing) > 0 {
			base += quotedRunnerStrings(missing)
		} else {
			base += "(see required_edit_paths in the brief)"
		}

		base += ". Satisfy the remaining-path write contract one path at a time, or report a concrete blocker per skipped path with <<QUESTION>>:."
		if unsupported := r.unsupportedClaimedChangedPaths(answer, results); len(unsupported) > 0 {
			base += " The previous answer also reported changed paths not backed by tool results: " + quotedRunnerStrings(unsupported) + " — do not include those in any future answer until they are actually written."
		}

		return base
	case string(turnViolationUserVisibleBlocker):
		return "the final answer was a <<QUESTION>>: blocker; surface the path-specific blocker reason for each unfinished required edit path."
	case string(turnViolationReadOnlyProtocolBlocker):
		return "the final answer exposed internal <<QUESTION>> blocker protocol in a read-only direct-answer lane. Answer in normal prose from gathered evidence, or state the exact unresolved claim and the smallest useful next probe without raw blocker markup."
	case string(turnViolationProtocolMetaFinalAnswer):
		if reason == string(turnReasonNonSelfContainedFinalAnswer) {
			return "the final answer referred to a hidden, prior, or rejected answer instead of standing on its own. Reply with self-contained user-visible prose from the gathered evidence; do not say \"the answer above\", \"previous answer\", or similar."
		}
		return "the final answer repeated internal validation status instead of answering the user. Answer in normal prose from the gathered evidence; omit validation status and internal protocol markers."
	case string(turnViolationUnexecutedToolRequestAnswer):
		transport := PlanExecutePlanningTransport{}
		if r != nil {
			transport = r.state.planningTransport()
		}
		return "the final answer was an unexecuted workspace-tool request payload. " + workspaceLookupTransportInstruction(recoveryToolTransportMode(transport)) + " Or answer in normal prose from the available evidence."
	case string(turnViolationNonAnswerProse):
		return "the final answer looks like transport-level noise (tool-call markup, echoed user question, or tool-failure language) rather than an actual answer. Reply with substantive prose that addresses the user's request, or use <<QUESTION>>: to state a concrete blocker."
	case string(turnViolationSourceGroundingUnmet):
		return reason
	case string(turnViolationRequiredEvidenceUnmet):
		if r != nil {
			missing := missingRequiredEvidence(r.state.runtimeBrief.RequiredEvidence, r.finalEvidenceProjection(answer, results))
			return "the answer does not satisfy the required evidence contract. Missing: " + strings.Join(missing, ", ") + ". Address each missing obligation in your answer, or use <<QUESTION>>: to state a concrete blocker."
		}

		return "the answer does not satisfy the required evidence contract. Address each missing obligation, or use <<QUESTION>>: to state a concrete blocker."
	case string(turnViolationValidationStatusContradictsEvidence):
		return reason
	case string(turnViolationRejectedMutationPathsRemaining):
		if r != nil {
			if missing := r.state.unresolvedMutationTargetIDs(); len(missing) > 0 {
				return "the implementation contract is not fulfilled. The model attempted workspace write(s) that were rejected and not later satisfied by successful writes: " + quotedRunnerStrings(missing) + ". Make those intended edits, or report a concrete no-op/blocker with <<QUESTION>>:."
			}
		}

		return "the implementation contract is not fulfilled. A rejected workspace write attempt remains unresolved; make the intended edit, or report a concrete no-op/blocker with <<QUESTION>>:."
	default:
		return "the final answer did not satisfy the active blocker check; continue with concrete writes or a per-path blocker reason."
	}
}

func (r *runExecution) finalAnswerBlockerKind(answer string, results []workspace.ToolResult) (string, string) {
	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	return acceptance.BlockerKind, acceptance.Reason
}

func (r *runExecution) decoratedReadOnlyReplyForCompletionGate(reply string, results []workspace.ToolResult) string {
	if r == nil || !r.userFacingFinalAnswer() || runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return reply
	}

	if looksLikeNonAnswerProse(reply) {
		return reply
	}

	if (!finalAnswerNeedsDecoration(reply) && !looksLikeGenericEvidenceComposerAnswer(reply)) ||
		looksLikeRawToolResultTranscript(reply) ||
		IsTextualToolCall(reply) ||
		looksLikeUnexecutedToolRequestAnswer(reply) {

		return reply
	}

	decorated := r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     reply,
		StopReason: "model_final",
		Brief:      r.state.runtimeBrief,
		Results:    results,
		UserFacing: true,
	})
	if strings.TrimSpace(decorated) == "" || strings.TrimSpace(decorated) == strings.TrimSpace(reply) {
		return reply
	}

	if finalAnswerNeedsDecoration(decorated) {
		return reply
	}

	return decorated
}
