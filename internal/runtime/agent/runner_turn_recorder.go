package agent

import (
	"encoding/json"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/failures"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type runnerTurnRecorder struct {
	runner                    *runExecution
	activeToolCall            *llama.ToolCall
	onToolResult              func(ToolResultEvent)
	declarationMovesForbidden bool
}

type runnerRecordOptions struct {
	TrackProgress           bool
	ResetRejected           bool
	CountRejected           bool
	IncrementNoProgress     bool
	IncrementRejectedStreak bool
	SetLastToolKey          bool
	SuppressModelMessage    bool
}

func (r runnerTurnRecorder) record(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	opts.SetLastToolKey = true
	r.recordWithOptions(step, req, result, reason, opts)
}

func (r runnerTurnRecorder) recordWithOptions(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string, opts runnerRecordOptions) {
	if r.runner == nil {
		return
	}
	state := &r.runner.state
	priorResults := r.snapshotResults()
	state.recordMutationCommitReceipt(result)
	result = resultWithRejectedRequestTarget(req, result)
	state.collectedResults = append(state.collectedResults, result)
	if strings.TrimSpace(result.Tool) != completionGateToolName &&
		(strings.TrimSpace(result.Error) == "" || workspaceWriteResultCommitted(req, result)) {
		progress := r.runner.observeCompletionGateProgress(req, result)
		r.runner.emitCompletionGateRecoveryProgress(result, progress)
	}
	if opts.SetLastToolKey {
		state.turn.lastToolKey = toolRequestKey(req)
	}

	resultCopy := result
	state.turn.lastToolResult = &resultCopy

	if opts.ResetRejected {
		state.lifecycle.progress.consecutiveRejected = 0
	}

	if opts.IncrementRejectedStreak {
		state.lifecycle.progress.consecutiveRejected++
	}

	if opts.CountRejected {
		state.metrics.RejectedActions++
	}

	if opts.TrackProgress {
		switch r.resultProgress(req, result, priorResults) {
		case runnerResultProgressMade:
			state.lifecycle.progress.consecutiveNoProgress = 0
			state.metrics.ProgressTurns++
		case runnerResultProgressStalled:
			state.lifecycle.progress.consecutiveNoProgress++
		}
	} else if opts.IncrementNoProgress {
		state.lifecycle.progress.consecutiveNoProgress++
	}

	if r.onToolResult != nil && r.runner != nil {
		r.onToolResult(ToolResultEvent{Step: step, Agent: r.runner.agentName(), Request: req, Result: result})
	}

	if !opts.SuppressModelMessage {
		modelResult := r.modelFacingToolResult(step, req, result, reason)

		state.turnMessages = append(state.turnMessages, toolResultMessage(r.activeToolCall, modelResult))
	}

	r.runner.emitNextActionState(step, reason, req, result, r.snapshotResults(), r.declarationMovesForbidden)

	if strings.TrimSpace(result.Error) != "" {
		results := r.snapshotResults()
		packet := buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(r.runner.agentName(), step, reason, req, result, results, r.declarationMovesForbidden, state.runtimeBrief, state.currentAllowedTools, state.planningTransport(), r.runner.implementationCompletionState(results), r.runner.planThenExecutePathAuthority(), r.runner.permissionAuthorizedInspection(r.runner.workspaceAdvertisedAllowedTools()))
		if len(packet.DoNotRepeat) > 0 || len(packet.RequiredNext) > 0 || strings.TrimSpace(packet.ResultError) != "" {
			if data, err := json.Marshal(packet); err == nil {
				r.runner.emitEvent(RunEvent{Role: "failure_feedback", Content: string(data)})
			}
		}
	}
}

type runnerResultProgress string

const (
	runnerResultProgressStalled runnerResultProgress = "stalled"
	runnerResultProgressMade    runnerResultProgress = "made"
	runnerResultProgressNeutral runnerResultProgress = "neutral"
)

func (r runnerTurnRecorder) resultProgress(req workspace.ToolRequest, result workspace.ToolResult, priorResults []workspace.ToolResult) runnerResultProgress {
	if toolResultMadeProgress(req, result) {
		return runnerResultProgressMade
	}

	return runnerResultProgressStalled
}

func resultWithRejectedRequestTarget(req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return result
	}

	disposition := toolfacts.ToolRejectionNotRetained
	if result.Facts != nil && result.Facts.RejectionDisposition != "" {
		disposition = result.Facts.RejectionDisposition
	} else if failures.CodeOf(result.Failure) == failures.PermissionDenied {
		disposition = toolfacts.ToolRejectionPolicyDenied
	}
	result = resultWithToolRejectionDisposition(result, disposition)
	if !disposition.CarriesRejectedWriteIntent() {
		result.Facts.RejectedWritePaths = nil
		return result
	}

	paths := canonicalPersistedRunnerPaths(result.Facts.RejectedWritePaths)
	if len(paths) == 0 {
		return result
	}
	facts := result.Facts
	facts.RejectedWritePaths = append([]string(nil), paths...)
	result.Facts = facts

	for _, path := range paths {
		if strings.Contains(result.Summary, path) || strings.Contains(result.Output, path) {
			continue
		}
		line := "Rejected write target: `" + path + "`"
		if strings.TrimSpace(result.Output) == "" {
			result.Output = line
		} else {
			result.Output = strings.TrimRight(result.Output, "\n") + "\n" + line
		}
	}

	return result
}

func (r *runExecution) bindRetainedMutationRejection(req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if r == nil || r.runner == nil || r.runner.Workspace == nil ||
		strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(req.Tool) ||
		result.Facts == nil || !result.Facts.RejectionDisposition.CarriesRejectedWriteIntent() {

		return result
	}
	result.Facts = result.Facts.Clone()
	if paths := canonicalPersistedRunnerPaths(result.Facts.RejectedWritePaths); len(paths) > 0 {
		result.Facts.RejectedWritePaths = paths
		return result
	}

	assessment, ok := r.mutationAdmissionForRequest(req)
	if !ok {
		result = resultWithToolRejectionDisposition(result, toolfacts.ToolRejectionNotRetained)
		result.Facts.RejectedWritePaths = nil
		return result
	}
	if assessment.Disposition != workspace.MutationAdmissionAdmittedWithoutApproval ||
		!assessment.Analysis.Mutation || len(assessment.Analysis.Targets) == 0 {
		result = resultWithToolRejectionDisposition(result, toolfacts.ToolRejectionNotRetained)
		result.Facts.RejectedWritePaths = nil
		return result
	}

	result.Facts.RejectedWritePaths = mutationRequestCanonicalTargetIDs(assessment.Analysis)
	return result
}

func resultWithToolRejectionDisposition(result workspace.ToolResult, disposition toolfacts.ToolRejectionDisposition) workspace.ToolResult {
	facts := result.Facts.Clone()
	if facts == nil {
		facts = &toolfacts.ToolResultFacts{}
	}
	facts.RejectionDisposition = disposition
	result.Facts = facts
	return result
}

func (r runnerTurnRecorder) modelFacingToolResult(step int, req workspace.ToolRequest, result workspace.ToolResult, reason string) workspace.ToolResult {
	if strings.TrimSpace(reason) == parserRecoveryRecordReason && r.activeToolCall != nil {
		if toolName := strings.TrimSpace(r.activeToolCall.Function.Name); toolName != "" {
			modelResult := result

			modelResult.Tool = toolName
			if r.runner != nil {
				modelResult.Error = modelFacingToolParseError(modelResult.Error, recoveryToolTransportMode(r.runner.state.planningTransport()))
			}
			return modelResult
		}
	}

	if r.runner == nil || strings.TrimSpace(result.Error) == "" || strings.TrimSpace(reason) != "tool_rejected" {
		return result
	}

	packet := buildRunnerFailureFeedbackPacketWithAllowedToolsAndTransport(
		r.runner.agentName(),
		step,
		reason,
		req,
		result,
		r.snapshotResults(),
		r.declarationMovesForbidden,
		r.runner.state.runtimeBrief,
		r.runner.state.currentAllowedTools,
		r.runner.state.planningTransport(),
		r.runner.implementationCompletionState(r.snapshotResults()),
		r.runner.planThenExecutePathAuthority(),
		r.runner.permissionAuthorizedInspection(r.runner.workspaceAdvertisedAllowedTools()),
	)
	feedback := renderModelFacingFailureFeedback(packet)

	if feedback == "" {
		return result
	}

	modelResult := result

	modelResult.Error = appendRecoveryCardToMessage(modelResult.Error, feedback)

	return modelResult
}

func renderModelFacingFailureFeedback(packet runnerFailureFeedbackPacket) string {
	var b strings.Builder

	b.WriteString("Recovery feedback")
	writeModelFacingFeedbackList(&b, "do_not_repeat", packet.DoNotRepeat, 5)
	writeModelFacingFeedbackList(&b, "required_next", packet.RequiredNext, 5)
	writeModelFacingFeedbackList(&b, "valid_next", packet.ValidNext, 5)
	writeModelFacingFeedbackList(&b, "evidence_status", packet.EvidenceStatus, 3)

	if card := strings.TrimSpace(packet.RecoveryCard); card != "" {
		b.WriteString("\n")
		b.WriteString(card)
	}

	feedback := strings.TrimSpace(b.String())
	if feedback == "Recovery feedback" {
		return ""
	}

	return feedback
}

func writeModelFacingFeedbackList(b *strings.Builder, label string, values []string, limit int) {
	values = nonEmptyRunnerStrings(values, limit)
	if len(values) == 0 {
		return
	}

	b.WriteString("\n")
	b.WriteString(label)
	b.WriteString(":")

	for _, value := range values {
		b.WriteString("\n- ")
		b.WriteString(value)
	}
}

func (r runnerTurnRecorder) snapshotResults() []workspace.ToolResult {
	if r.runner == nil {
		return nil
	}

	return r.runner.state.collectedResults
}

type recoveryNudgeInputs struct {
	req                workspace.ToolRequest
	results            []workspace.ToolResult
	brief              workerruntime.WorkerRuntimeBrief
	mutationAuthority  planThenExecutePathAuthority
	completion         *implementationCompletionState
	verificationLedger *postWriteVerificationLedger
}

// recoveryNudgeRule is a priority-ordered rejection-recovery nudge.
// Returns (nudge, true) when fired or ("", false) when the rule does
// not apply. The dispatcher tries rules in order; first match wins.
type recoveryNudgeRule func(in recoveryNudgeInputs) (string, bool)

// recoveryNudgeRules lists each rule in priority order. Order is
// load-bearing: a rule that fires returns before later rules get a
// chance, so insertion position matters. When adding a new rule,
// place it at the priority that matches the scenario it handles.
//
//  1. verification-only shell (read-only lanes need to run the check)
//  2. exact-anchor pre-write (write-ready packet, no more discovery)
//  3. missing required-edit paths (allowlist not yet covered)
//  4. pending verification after writes (writes need sanity)
//
// Strategy-specific recovery does not belong here. The model chooses among
// contract-valid read, search, and terminal actions after a rejection.
//
// When every priority rule declines, lastChanceRecoveryNudge produces
// the tool-shape-specific default.
var recoveryNudgeRules = []recoveryNudgeRule{
	verificationOnlyShellRecoveryNudge,
	exactAnchorPreWriteRecoveryNudge,
	missingRequiredEditPathsRecoveryNudge,
	pendingVerificationAfterWritesRecoveryNudge,
}

// buildRejectionRecoveryNudge gives the model one last closure-biased repair
// after repeated rejected tool calls. It states the contract and avoids
// prescribing a copy-paste tool sequence unless a hard invariant requires it.
// Behavior is decomposed into priority-ordered rules (recoveryNudgeRules);
// the first rule that fires returns its nudge. If none fire, the
// contract-shaped lastChanceRecoveryNudge always produces a default.
func (r *runExecution) buildRejectionRecoveryNudge(req workspace.ToolRequest, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}
	completion := r.implementationCompletionState(results)
	ledger := r.postWriteVerificationLedger(results)
	return buildRejectionRecoveryNudgeWithState(req, results, r.state.runtimeBrief, r.planThenExecutePathAuthority(), &completion, &ledger)
}

func buildRejectionRecoveryNudgeWithState(req workspace.ToolRequest, results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief, mutationAuthority planThenExecutePathAuthority, completion *implementationCompletionState, ledger *postWriteVerificationLedger) string {
	// Conversation lanes: the model owns readiness. Do not inject
	// nudges that declare evidence is sufficient or that the harness
	// will end the turn. The force-completion path is also gated in
	// rejectTool; this keeps the nudge text consistent.
	if LanePolicyForBrief(brief).skipRecoveryNudge {
		return ""
	}

	in := recoveryNudgeInputs{
		req:                req,
		results:            results,
		brief:              brief,
		mutationAuthority:  mutationAuthority,
		completion:         completion,
		verificationLedger: ledger,
	}
	for _, rule := range recoveryNudgeRules {
		if nudge, ok := rule(in); ok {
			return nudge
		}
	}

	return lastChanceRecoveryNudge(in)
}

func verificationOnlyShellRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !workerruntime.PermissionProfileIsReadOnlyVerificationShell(in.brief.PermissionProfile) {
		return "", false
	}

	cmd := "go test ./..."
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}

	var b strings.Builder

	b.WriteString("This is a verification-only turn; the requested check has not yet run. Run it (the configured plan starts with `")
	b.WriteString(cmd)
	b.WriteString("`), then answer with the observed pass/fail. If the check has already run, answer with that result.\n")

	return b.String(), true
}

func exactAnchorPreWriteRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	mutationCommitted := in.completion != nil && in.completion.HasChanges()
	if !exactAnchorPreWriteSurfaceActive(in.brief, in.results, mutationCommitted) {
		return "", false
	}

	target := "the supplied exact-anchor target"
	if paths := nonEmptyRunnerStrings(in.brief.RequiredEditPaths, 3); len(paths) > 0 {
		target = strings.Join(paths, ", ")
	} else if refs := nonEmptyRunnerStrings(in.brief.TargetRefs, 3); len(refs) > 0 {
		target = strings.Join(refs, ", ")
	}

	var b strings.Builder

	b.WriteString("This is an exact-anchor implementation pre-write turn. The target packet supplies current-state path and source evidence for the first write; a final answer still requires the write or an exact blocker.\n")
	b.WriteString("Exact-anchor write capability is required for ")
	b.WriteString(target)
	b.WriteString(". Permission-authorized inspection remains callable when the model needs another fact. If the supplied anchor is missing or stale, gather fresh lawful evidence or answer with <<QUESTION>> naming that exact anchor gap.\n")

	return b.String(), true
}

func missingRequiredEditPathsRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	if !runnerBriefRequiresMutation(in.brief) {
		return "", false
	}

	missing := missingRequiredEditPaths(in.brief.RequiredEditPaths, committedMutationEffectSetForResults(in.results))
	if in.completion != nil {
		missing = append([]string(nil), in.completion.MissingRequiredPaths...)
	}
	if len(missing) == 0 {
		return "", false
	}

	var b strings.Builder

	if strings.TrimSpace(in.brief.TaskFamily) == PlanThenExecutePlannerTaskFamily {
		b.WriteString("Recovery nudge — this plan-execute planner turn still has required edit path(s) without successful `submit_plan` coverage: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(".\n")

		if planThenExecuteSubmitCorrectionActive(in.brief, in.results, in.mutationAuthority) {
			if planThenExecuteSubmitCorrectionBlockerAllowed(in.brief, in.results) {
				b.WriteString("Do not answer with partial findings. A parser-readable `submit_plan` remains the required successful terminal outcome; permission-authorized inspection remains callable for fresh evidence. `<<QUESTION>>` is valid only with the exact path-specific missing body/anchor blocker after repeated rejected submit_plan attempts. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied` and do not repeat the rejected tool call.\n")
			} else {
				b.WriteString("Do not answer with partial findings. A parser-readable `submit_plan` remains the required terminal outcome; permission-authorized inspection remains callable for fresh evidence. ")
				b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
				b.WriteString(" Do not answer `Decision: source-satisfied`, do not answer <<QUESTION>>, and do not repeat the rejected tool call.\n")
			}

			return b.String(), true
		}

		b.WriteString("Do not answer with partial findings. Permission-authorized inspection remains callable while the terminal contract is open. Required terminal outcomes are `submit_plan` (")
		b.WriteString(planThenExecuteSubmitPlanCoverageSentence())
		b.WriteString("), `Decision: source-satisfied` with current-state evidence for every remaining path, or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

		return b.String(), true
	}

	b.WriteString("Recovery nudge — this implementation turn still has required edit path(s) without a successful write: ")
	b.WriteString(strings.Join(missing, ", "))
	b.WriteString(".\n")

	if scopedImplementationUsesBoundedSourceEdits(in.brief, missing) {
		b.WriteString("Do not answer with partial findings. Permission-authorized inspection remains callable when another fact is needed, but it does not resolve the remaining write obligation. For existing source/test required paths, the allowed write outcome is a bounded `edit_file` call using old_text/new_text or start_line/end_line/content, or an `apply_patch` envelope. Do not use `write_file` or `append_file` for existing source/test paths; reserve full-body writes for missing/new files and non-source deliverables. Otherwise answer with <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

		return b.String(), true
	}

	b.WriteString("Do not answer with partial findings. Permission-authorized inspection remains callable when another fact is needed, but it does not resolve the remaining write obligation. Required terminal outcomes are an allowed write or <<QUESTION>> naming the exact blocker for each remaining path. Do not repeat the rejected tool call.\n")

	return b.String(), true
}

func pendingVerificationAfterWritesRecoveryNudge(in recoveryNudgeInputs) (string, bool) {
	ledger := postWriteVerificationLedgerForResults(in.results, in.brief)
	if in.verificationLedger != nil {
		ledger = *in.verificationLedger
	}
	if ledger.Pending() && ledger.HasFailure {
		failed := ledger.LatestFailed
		return postWriteVerificationFailedRecoveryPrompt(failed, plannerOwnsVerification(in.brief)) + "\n", true
	}

	if !ledger.Pending() {
		return "", false
	}

	cmd := "git diff --check"
	if len(in.brief.VerificationPlan) > 0 && strings.TrimSpace(in.brief.VerificationPlan[0]) != "" {
		cmd = strings.TrimSpace(in.brief.VerificationPlan[0])
	}

	var b strings.Builder

	if plannerOwnsVerification(in.brief) {
		b.WriteString("This implementation turn has successful writes that are still pending lane sanity. Permission-authorized inspection and in-scope edits remain callable; any additional committed edit joins the final changed set and must pass the lane sanity contract before the final answer. Build/test verification is planner-owned for this route, so required evidence is compiler-backed source-sanity or language diagnostics for changed source files; for non-source edits one cheap non-build sanity command such as `")
		b.WriteString(cmd)
		b.WriteString("`. For source edits, `git diff --check` and generic source-sanity fallback are only patch hygiene. If source sanity is forbidden or impossible, answer with the changed paths and the concrete lane-sanity blocker.\n")

		return b.String(), true
	}

	b.WriteString("This implementation turn has successful writes that are still pending verification. Permission-authorized inspection and in-scope edits remain callable; any additional committed edit joins the final changed set and must be verified before the final answer. Required evidence is one focused language/project verification command or compiler-backed diagnostics. If a verification plan exists, start with `")
	b.WriteString(cmd)
	b.WriteString("`. If verification is forbidden or impossible, answer with the changed paths and the concrete verification blocker.\n")

	return b.String(), true
}

// lastChanceRecoveryNudge is the always-fires terminator. It produces
// a contract-shaped default when no priority rule fired.
func lastChanceRecoveryNudge(in recoveryNudgeInputs) string {
	return "Recovery nudge — the prior tool request was rejected by the active contract. " +
		"Do not repeat it unchanged. Continue with any currently callable action that satisfies an open contract obligation, " +
		"or answer if the terminal contract is already satisfied.\n"
}

// stripFallbackJSONArtifacts removes JSON-shaped object dumps from a tool
// result Output before it gets surfaced under "Verified context" in the
// forced-completion fallback. When a child lane's task result Output
// includes a discovery_query summary, the artifact metadata JSON often
// follows verbatim ({"id": "...", "purpose": "...", "layers_run": [...]
// }) and bleeds into the user-facing answer as opaque blocks. The
// fallback should show prose bullets; metadata blobs belong in the
// artifact file, not the answer. Generic across artifact JSON — any
// brace-delimited object whose first content line starts with a quoted
// key is treated as a metadata dump and dropped.
