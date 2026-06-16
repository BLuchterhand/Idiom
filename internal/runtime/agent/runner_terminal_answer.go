package agent

import (
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"strings"
)

func (r *Runner) classifyTerminalAnswer(answer string, stopReason string, results []workspace.ToolResult) terminalAnswerDecision {
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
		acceptance.Decision = finalAnswerAcceptanceRejected
		acceptance.BlockerKind = "transport_protocol_output"
		acceptance.Reason = "the final answer contained tool-call transport markup instead of user-visible answer prose"
		decision.Outcome = terminalAnswerTransportBlocked
		decision.PresentedAnswer = "I wasn't able to produce a clean answer from the evidence. Could you rephrase or narrow the question?"
		decision.StopReason = acceptance.BlockerKind
		decision.Acceptance = acceptance

		return decision
	}

	if r.hostOwnedReadOnlyCloseoutAccepted(answer, stopReason, results, acceptance) {
		acceptance.Decision = finalAnswerAcceptanceAccepted
		acceptance.BlockerKind = ""
		acceptance.Reason = "host_read_only_closeout"
		decision.Outcome = terminalAnswerAccepted
		decision.StopReason = stopReason
		decision.Acceptance = acceptance

		return decision
	}

	if acceptance.BlockerKind == "" {
		return decision
	}

	decision.Outcome = terminalAnswerBlocked
	decision.StopReason = acceptance.BlockerKind
	decision.RetryExhausted = retryableReadOnlyFinalAnswerBlockerKind(acceptance.BlockerKind)

	if !strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") && r.terminalAnswerShouldOverrideRejectedRaw(answer, acceptance) {
		blockerResult := workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Final answer rejected by blocker check",
			Error:   r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, answer, results),
		}

		decision.BlockerResult = blockerResult
		decision.PresentedAnswer = r.completionGateRepeatedBlockerAnswer(blockerResult, results)
		decision.OverrideSource = "finish"
	}

	return decision
}

func (r *Runner) hostOwnedReadOnlyCloseoutAccepted(answer string, stopReason string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) bool {
	if r == nil || strings.TrimSpace(stopReason) != "host_read_only_closeout" || acceptance.BlockerKind == "" {
		return false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !readOnlyDirectAnswerLane(brief) || runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(brief, results).AcceptsHostOwnedAnswer(r.agentName(), answer)
}

func terminalBlockedStatusShouldEmit(alreadyEmitted bool, lastStatus string, lastBlockerKind string, terminalBlockerKind string) bool {
	return !alreadyEmitted || strings.TrimSpace(lastStatus) != "blocked"
}

func (r *Runner) terminalAnswerShouldOverrideRejectedRaw(answer string, acceptance finalAnswerAcceptance) bool {
	if acceptance.BlockerKind == "" {
		return false
	}

	if r != nil && strings.HasPrefix(r.agentName(), "child:") && !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return false
	}

	return acceptance.BlockerKind != "non_answer_prose" ||
		looksLikeMetaIntentRestatement(answer) ||
		finishShouldOverrideSourceEcho(answer)
}

func terminalAnswerContainsTransportProtocol(answer string) bool {
	return looksLikeTextualToolCall(answer) ||
		strings.Contains(answer, "<n_tool") ||
		strings.Contains(answer, "<n_invoke") ||
		strings.Contains(answer, "<janus_tool") ||
		strings.Contains(answer, "<invoke name=") ||
		strings.Contains(answer, "DSML")
}

func (r *Runner) decorateTerminalAnswerDecision(decision terminalAnswerDecision, results []workspace.ToolResult) string {
	if decision.Outcome == terminalAnswerTransportBlocked {
		return decision.PresentedAnswer
	}

	return r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     decision.PresentedAnswer,
		StopReason: decision.StopReason,
		Brief:      r.RuntimeBrief,
		Results:    results,
		UserFacing: r.userFacingFinalAnswer(),
	})
}

func (r *Runner) emitAcceptedAnswerSignals(answer string, results []workspace.ToolResult) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}

	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	r.emitFinalAnswerTelemetry(answer, results, acceptance)

	if acceptance.BlockerKind != "" {
		r.emitCompletionStatus("blocked", acceptance.Reason, acceptance.BlockerKind, results, true)
	}
}

func (r *Runner) emitFinalAnswerTelemetry(answer string, results []workspace.ToolResult, acceptance finalAnswerAcceptance) {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(answer) == "" {
		return
	}

	lane := util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Lane), strings.TrimSpace(r.RuntimeBrief.TaskFamily), r.agentName())
	contract := util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.Contract), string(workerruntime.ContractDirectAnswer))
	evidencePaths := acceptance.EvidencePaths

	var presentationEvidencePaths []string

	if readOnlyDirectAnswerLane(r.RuntimeBrief) && len(evidencePaths) == 0 {
		presentationEvidencePaths = readOnlyDirectAnswerPresentationEvidencePaths(r.RuntimeBrief, results, 8)
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
			"task_family":    util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
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
		if conversationLaneWithoutSemanticEval(r.RuntimeBrief) {
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
		"task_family":    util.FirstNonEmpty(strings.TrimSpace(r.RuntimeBrief.TaskFamily), contract),
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

func (r *Runner) emitFinalAnswerBlockerOverride(acceptance finalAnswerAcceptance, replacementStopReason string, source string) {
	if r == nil || r.OnEvent == nil || acceptance.BlockerKind == "" {
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
		payload["relevant_source_body_paths"] = cappedStrings(acceptance.RelevantSourceBodyPaths, 8)
	}

	r.emitEvent(marshalRunEvent("final_answer_blocker_override", payload))
}

func (r *Runner) emitPostWriteVerificationTelemetry(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if r == nil || r.OnEvent == nil || strings.TrimSpace(result.Error) != "" || !resultCountsAsSuccessfulVerification(result) {
		return false
	}

	command := postWriteVerificationTelemetryCommand(req, result)
	if command == "" {
		return false
	}

	confidence := postWriteVerificationTelemetryConfidence(command)
	plan := map[string]any{
		"commands":          []string{command},
		"uses_full_suite":   postWriteVerificationUsesFullSuite(command),
		"coverage":          postWriteVerificationTelemetryCoverage(command),
		"confidence":        confidence,
		"risk_level":        postWriteVerificationTelemetryRisk(command),
		"expected_failures": postWriteVerificationExpectedFailures(command),
		"repair_policy":     "repair failing checks before widening scope",
	}
	r.emitEvent(marshalRunEvent("verification_plan", plan))

	feedback := map[string]any{
		"commands":               []string{command},
		"failed_commands":        []string{},
		"confidence":             confidence,
		"coverage":               plan["coverage"],
		"risk_level":             plan["risk_level"],
		"learned_recommendation": postWriteVerificationRecommendation(command),
	}
	r.emitEvent(marshalRunEvent("verification_feedback", feedback))

	return true
}

func postWriteVerificationTelemetryCommand(req workspace.ToolRequest, result workspace.ToolResult) string {
	if strings.TrimSpace(req.Tool) == workspace.ToolNameShell {
		if command := strings.TrimSpace(req.Command); command != "" {
			return command
		}

		if command := shellCommandFromToolResult(result); command != "" {
			return command
		}
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameShell {
		if command := shellCommandFromToolResult(result); command != "" {
			return command
		}
	}

	if strings.TrimSpace(req.Tool) == workspace.ToolNameSourceSanity && strings.TrimSpace(req.Path) != "" {
		return "source_sanity " + normalizeRunnerPath(req.Path)
	}

	if isDiagnosticsVerificationTool(req.Tool) {
		return strings.TrimSpace(req.Tool)
	}

	return ""
}

func postWriteVerificationUsesFullSuite(command string) bool {
	lower := strings.ToLower(command)

	return strings.Contains(lower, "go test ./...") ||
		strings.Contains(lower, "make test") ||
		strings.Contains(lower, "npm test") ||
		strings.Contains(lower, "pnpm test") ||
		strings.Contains(lower, "cargo test")
}

func postWriteVerificationTelemetryCoverage(command string) string {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck"):
		return "targeted script syntax verification"
	case postWriteVerificationUsesFullSuite(command):
		return "full-suite verification"
	case strings.Contains(lower, "test") || strings.Contains(lower, "build") || strings.Contains(lower, "check"):
		return "targeted project verification"
	default:
		return "targeted verification"
	}
}

func postWriteVerificationTelemetryConfidence(command string) string {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck") {
		return "low"
	}

	if postWriteVerificationUsesFullSuite(command) {
		return "high"
	}

	return "medium"
}

func postWriteVerificationTelemetryRisk(command string) string {
	if postWriteVerificationUsesFullSuite(command) {
		return "high"
	}

	return "low"
}

func postWriteVerificationExpectedFailures(command string) []string {
	lower := strings.ToLower(command)
	switch {
	case strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck"):
		return []string{"shell syntax errors"}
	case strings.Contains(lower, "test"):
		return []string{"test failures"}
	case strings.Contains(lower, "build"):
		return []string{"build failures"}
	case strings.Contains(lower, "check"):
		return []string{"check failures"}
	default:
		return []string{"verification failures"}
	}
}

func postWriteVerificationRecommendation(command string) string {
	lower := strings.ToLower(command)
	if strings.Contains(lower, "bash -n") || strings.Contains(lower, "sh -n") || strings.Contains(lower, "zsh -n") || strings.Contains(lower, "shellcheck") {
		return "syntax verification passed; run targeted tests when behavior changes beyond script parsing"
	}

	if postWriteVerificationUsesFullSuite(command) {
		return "record full-suite success for similar high-risk surfaces"
	}

	return "keep targeted tests close to the changed surface before widening scope"
}

// requiredEditPathsProgressMessage returns a compact authoritative
// progress checklist that the runner injects into turnMessages after each
// successful write tool result on a brief that has required_edit_paths.
// The message gives the model running ground truth — paths actually
// changed (✓) and paths still pending (✗) — derived from
// changedPathsFromToolResults rather than the model's conversation
// memory. Returns empty string when no checklist is appropriate (no
// required paths in the brief, write was unsuccessful, no state change
// since the last write, etc.).
//
// Why we inject only after a successful write rather than every turn:
// per-turn injection accumulates stale checklists in the conversation;
// after-write injection is exactly when the running state changes and
// the model needs the truth most.
func (r *Runner) requiredEditPathsProgressMessage(req workspace.ToolRequest, result workspace.ToolResult, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	if !runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}

	required := r.RuntimeBrief.RequiredEditPaths
	if len(required) == 0 {
		return ""
	}

	if !workspaceWriteResultCompleted(req, result) {
		return ""
	}

	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return ""
	}

	var (
		done    []string
		pending []string
	)

	for _, raw := range required {
		path := normalizeRunnerPath(raw)
		if path == "" {
			continue
		}

		if _, ok := changed[path]; ok {
			done = append(done, raw)
		} else {
			pending = append(pending, raw)
		}
	}

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
func (r *Runner) finalAnswerBlockerErrorText(kind string, reason string, answer string, results []workspace.ToolResult) string {
	switch kind {
	case "required_edit_paths_remaining":
		missing := missingRequiredEditPaths(r.RuntimeBrief.RequiredEditPaths, changedPathsFromToolResults(results))
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
		if unsupported := unsupportedClaimedChangedPaths(answer, results); len(unsupported) > 0 {
			base += " The previous answer also reported changed paths not backed by tool results: " + quotedRunnerStrings(unsupported) + " — do not include those in any future answer until they are actually written."
		}

		return base
	case "user_visible_blocker":
		return "the final answer was a <<QUESTION>>: blocker; surface the path-specific blocker reason for each unfinished required edit path."
	case "read_only_protocol_blocker":
		return "the final answer exposed internal <<QUESTION>> blocker protocol in a read-only direct-answer lane. Answer in normal prose from gathered evidence, or state the exact unresolved claim and the smallest useful next probe without raw blocker markup."
	case "unexecuted_tool_request_final_answer":
		return "the final answer was an unexecuted workspace-tool request payload. Issue the lookup as a parser-readable tool call with the `tool` field, or answer in normal prose from the available evidence."
	case "non_answer_prose":
		return "the final answer looks like transport-level noise (tool-call markup, echoed user question, or tool-failure language) rather than an actual answer. Reply with substantive prose that addresses the user's request, or use <<QUESTION>>: to state a concrete blocker."
	case "source_grounding_unmet":
		return reason
	case "answer_objective_unmet":
		return reason
	case "required_evidence_unmet":
		if r != nil {
			missing := missingRequiredEvidence(r.RuntimeBrief.RequiredEvidence, answer, results)
			return "the answer does not satisfy the required evidence contract. Missing: " + strings.Join(missing, ", ") + ". Address each missing obligation in your answer, or use <<QUESTION>>: to state a concrete blocker."
		}

		return "the answer does not satisfy the required evidence contract. Address each missing obligation, or use <<QUESTION>>: to state a concrete blocker."
	case "rejected_mutation_paths_remaining":
		var requiredPaths []string

		if r != nil {
			requiredPaths = r.RuntimeBrief.RequiredEditPaths
		}

		if missing := unresolvedRejectedMutationPaths(results, requiredPaths); len(missing) > 0 {
			return "the implementation contract is not fulfilled. The model attempted workspace write(s) that were rejected and not later satisfied by successful writes: " + quotedRunnerStrings(missing) + ". Make those intended edits, or report a concrete no-op/blocker with <<QUESTION>>:."
		}

		return "the implementation contract is not fulfilled. A rejected workspace write attempt remains unresolved; make the intended edit, or report a concrete no-op/blocker with <<QUESTION>>:."
	default:
		return "the final answer did not satisfy the active blocker check; continue with concrete writes or a per-path blocker reason."
	}
}

func (r *Runner) finalAnswerBlockerKind(answer string, results []workspace.ToolResult) (string, string) {
	acceptance := r.evaluateFinalAnswerAcceptance(answer, results)
	return acceptance.BlockerKind, acceptance.Reason
}

func (r *Runner) decoratedReadOnlyReplyForCompletionGate(reply string, results []workspace.ToolResult) string {
	if r == nil || !r.userFacingFinalAnswer() || runnerBriefRequiresMutation(r.RuntimeBrief) {
		return reply
	}

	if looksLikeNonAnswerProse(reply) {
		return reply
	}

	if (!finalAnswerNeedsDecoration(reply) && !looksLikeGenericEvidenceComposerAnswer(reply)) ||
		looksLikeRawToolResultTranscript(reply) ||
		looksLikeTextualToolCall(reply) ||
		looksLikeUnexecutedToolRequestAnswer(reply) {

		return reply
	}

	decorated := r.decorateFinalAnswer(FinalAnswerDecorateInput{
		Answer:     reply,
		StopReason: "model_final",
		Brief:      r.RuntimeBrief,
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

func acceptedAnswerEvidencePaths(answer string, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) []string {
	if runnerBriefRequiresMutation(brief) {
		if paths := acceptedImplementationEvidencePaths(results); len(paths) > 0 {
			return paths
		}
	}

	if readOnlyDirectAnswerAcceptanceApplies(brief) {
		return acceptedReadOnlyDirectAnswerEvidencePaths(answer, brief, results)
	}

	if readOnlyExplanationGateApplies(brief) {
		paths := mergeRunnerStateStrings(exactProbeEvidencePaths(results), pathparse.MentionedWorkspacePaths(answer))
		for _, packet := range readOnlyEvidencePacketsFromResults(results) {
			if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
				paths = mergeRunnerStateStrings(paths, packet.SourceBodyPaths, packet.InspectedPaths)
			}
		}

		paths = mergeRunnerStateStrings(paths, delegatedReadOnlyInspectedImplementationPaths(results))
		if len(paths) == 0 {
			paths = mergeRunnerStateStrings(paths, mentionedEvidencePathsFromToolResultsForObjective(results, brief.Objective, 24))
		}

		paths = prioritizedClosureEvidencePaths(paths, results, 8)
		paths = closurePromptEvidencePaths(paths, 8)

		if len(paths) > 0 {
			return paths
		}
	}

	paths := mergeRunnerStateStrings(pinnedPathsFromToolResults(results, 8), pathparse.MentionedWorkspacePaths(answer))
	for _, packet := range readOnlyEvidencePacketsFromResults(results) {
		if readOnlyEvidencePacketAnswerableForObjective(packet, brief.Objective) {
			paths = mergeRunnerStateStrings(paths, packet.SourceBodyPaths, packet.InspectedPaths)
		}
	}

	paths = delegatedDiscoveryAnswerEvidencePaths(brief.Objective, paths)

	return closurePromptEvidencePaths(paths, 8)
}

func acceptedImplementationEvidencePaths(results []workspace.ToolResult) []string {
	paths := changedPathListFromToolResults(results)

	paths = append(paths, directProbePathsFromToolResults(results, 8)...)

	return closurePromptEvidencePathsPreservingTests(paths, 8)
}

func changedPathListFromToolResults(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, rawPath := range changedPathsFromToolResult(result) {
			path := normalizeRunnerPath(rawPath)
			if path == "" {
				continue
			}

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return paths
}

func closurePromptEvidencePathsPreservingTests(paths []string, max int) []string {
	cleaned := make([]string, 0, len(paths))
	for _, path := range uniqueDelegatedDiscoveryPaths(paths) {
		path = normalizeDelegatedDiscoveryEvidencePath(path)
		switch {
		case path == "":
			continue
		case strings.HasPrefix(path, ".idiom/"), strings.HasPrefix(path, "idiom/"), strings.HasPrefix(path, ".codex/"), strings.HasPrefix(path, "codex/"), strings.HasPrefix(path, "."):
			continue
		case !delegatedDiscoveryLooksLikeEvidencePath(path):
			continue
		case !strings.Contains(path, "/") && !strings.HasSuffix(path, ".go"):
			continue
		}

		cleaned = append(cleaned, path)
	}

	return limitRunnerStrings(cleaned, max)
}

func exactProbeEvidencePaths(results []workspace.ToolResult) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0, 4)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_extract_body", "python_read_symbol", "ts_read_symbol", "rust_read_symbol", "java_read_symbol":
		default:
			continue
		}

		path, _ := extractExactProbeAnchor(result)

		path = normalizeDelegatedDiscoveryEvidencePath(path)

		if path == "" {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return preferNonTestEvidencePaths(paths)
}

func (r *Runner) agentName() string {
	if strings.TrimSpace(r.AgentName) == "" {
		return "main"
	}

	return r.AgentName
}

func (r *Runner) explorationDelegationNudge(readCallsSinceWrite int, results []workspace.ToolResult) string {
	if r == nil || r.Workspace == nil || r.DisableTaskTool || strings.HasPrefix(r.agentName(), "child:") || strings.HasPrefix(r.agentName(), "child_write:") {
		return ""
	}

	// The exploration delegation nudge uses mutation language
	// ("without a write") and assumes write-then-answer lanes.
	// The conversation lane has its own evidence path and should
	// not receive mutation-framed nudges.
	if r.lanePolicy().SkipExplorationDelegationNudge() {
		return ""
	}

	if runnerBriefRequiresMutation(r.RuntimeBrief) {
		return ""
	}

	allowedTools := r.advertisedAllowedTools(r.Workspace.AllowedTools())
	if !containsRunnerTool(allowedTools, workspace.ToolNameTask) {
		return ""
	}

	if len(changedPathsFromToolResults(results)) > 0 || hasSuccessfulTaskResult(results) {
		return ""
	}

	minEvidenceResults := 2
	threshold := 4

	if prefersEarlyReadOnlyDelegation(r.RuntimeBrief) {
		minEvidenceResults = 1
		threshold = 1
	}

	if workerruntime.RuntimeContractIsDirectAnswer(r.RuntimeBrief.Contract) {
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
		if util.EqualTrimFold(policy, "delegate_broad_discovery_when_needed") {
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

		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile,
			"go_read_symbol", "go_file_outline",
			"cpp_read_symbol", "cpp_file_outline",
			"python_read_symbol", "python_file_outline",
			"ts_read_symbol", "ts_file_outline",
			"rust_read_symbol", "rust_file_outline",
			"java_read_symbol", "java_file_outline":
			return true
		}
	}

	return false
}
