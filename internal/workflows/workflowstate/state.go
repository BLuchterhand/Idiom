package workflowstate

import "strings"

type WorkflowID string

const (
	WorkflowDirectAnswer        WorkflowID = "direct_answer"
	WorkflowPlanExecutePlanner  WorkflowID = "plan_execute_planner"
	WorkflowPlanExecuteExecutor WorkflowID = "plan_execute_executor"
	WorkflowImplement           WorkflowID = "implement"
	WorkflowRecovery            WorkflowID = "recovery"
	WorkflowSidecarToolCall     WorkflowID = "sidecar_tool_call"
	WorkflowProductDispatch     WorkflowID = "product_workflow_dispatch"
	WorkflowAtomIntake          WorkflowID = "atom_intake"
	WorkflowPromptContextPacket WorkflowID = "prompt_context_packet"
)

type TerminalState string

const (
	TerminalStateComplete           TerminalState = "complete"
	TerminalStateSourceSatisfied    TerminalState = "source-satisfied"
	TerminalStateBlocked            TerminalState = "blocked"
	TerminalStateStaleNeedsRebase   TerminalState = "stale-needs-rebase"
	TerminalStateInvalidScope       TerminalState = "invalid-scope"
	TerminalStateMissingScope       TerminalState = "missing-scope"
	TerminalStateDirtyScope         TerminalState = "dirty-scope"
	TerminalStateVerificationFailed TerminalState = "verification-failed"
	TerminalStateRecoveryOpen       TerminalState = "recovery-open"
)

type Classification struct {
	WorkflowID    WorkflowID    `json:"workflow_id"`
	TerminalState TerminalState `json:"terminal_state"`
	Dispatchable  bool          `json:"dispatchable"`
	Terminal      bool          `json:"terminal"`
	Reason        string        `json:"reason,omitempty"`
	Blockers      []Blocker     `json:"blockers,omitempty"`
	Transitions   []Transition  `json:"transitions,omitempty"`
}

type Blocker struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type Transition struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
}

type PlanExecuteFailureKind string

const (
	PlanExecuteFailurePlannerBlocked                     PlanExecuteFailureKind = "planner_blocked"
	PlanExecuteFailurePlannerFailed                      PlanExecuteFailureKind = "planner_failed"
	PlanExecuteFailurePlanArtifactMissing                PlanExecuteFailureKind = "plan_artifact_missing"
	PlanExecuteFailurePlanArtifactInvalid                PlanExecuteFailureKind = "plan_artifact_invalid"
	PlanExecuteFailureSourceSanityFailed                 PlanExecuteFailureKind = "source_sanity_failed"
	PlanExecuteFailureSourceSatisfiedUnsupportedEvidence PlanExecuteFailureKind = "source_satisfied_unsupported_evidence"
	PlanExecuteFailureVerificationFailed                 PlanExecuteFailureKind = "verification_failed"
	PlanExecuteFailureMissingEditPathWithoutNewFileAuth  PlanExecuteFailureKind = "missing_edit_path_without_new_file_authorization"
	PlanExecuteFailureTerminalCompletionContractFailed   PlanExecuteFailureKind = "terminal_completion_contract_failed"
	PlanExecuteFailureExecutorFailed                     PlanExecuteFailureKind = "executor_failed"
	PlanExecuteFailureReviewRepairFailed                 PlanExecuteFailureKind = "review_repair_failed"
	PlanExecuteFailureReviewRepairBlocked                PlanExecuteFailureKind = "review_repair_blocked"
	PlanExecuteFailureReviewRepairDegraded               PlanExecuteFailureKind = "review_repair_degraded"
)

type ReviewStatus string

const (
	ReviewStatusPassed   ReviewStatus = "passed"
	ReviewStatusSkipped  ReviewStatus = "skipped"
	ReviewStatusBlocked  ReviewStatus = "blocked"
	ReviewStatusDegraded ReviewStatus = "degraded"
	ReviewStatusFailed   ReviewStatus = "failed"
)

type NextAction string

const (
	NextActionReviewDecomposition NextAction = "review_decomposition"
	NextActionAnswerBlocker       NextAction = "answer_blocker"
	NextActionRetryPlanExecute    NextAction = "retry_plan_execute"
	NextActionInspectFailure      NextAction = "inspect_failure"
	NextActionHumanIntervention   NextAction = "human_intervention"
	NextActionNone                NextAction = "none"
)

type PlanExecuteTerminalInput struct {
	Phase          string
	Status         string
	ReviewStatus   string
	ReviewStatusID ReviewStatus
	TerminalClass  string
	Outcome        string
	ExitCode       int
	FailureKind    string
	FailureKindID  PlanExecuteFailureKind
	FailureReason  string
	NextAction     string
	NextActionID   NextAction
}

func ClassifyPlanExecuteTerminal(input PlanExecuteTerminalInput) Classification {
	phase := token(input.Phase)
	status := token(input.Status)
	reviewStatus := firstNonEmpty(token(string(input.ReviewStatusID)), token(input.ReviewStatus))
	terminalClass := token(input.TerminalClass)
	outcome := token(input.Outcome)
	failureKind := firstNonEmpty(token(string(input.FailureKindID)), token(input.FailureKind))
	failureReason := strings.TrimSpace(input.FailureReason)
	nextAction := firstNonEmpty(token(string(input.NextActionID)), token(input.NextAction))
	if nextAction == token(string(NextActionNone)) {
		nextAction = ""
	}

	workflow := WorkflowPlanExecuteExecutor
	if phase == "planning" ||
		outcome == "source-satisfied" ||
		status == "source-satisfied" ||
		planExecutePlannerOwnsFailureKind(failureKind) {
		workflow = WorkflowPlanExecutePlanner
	}

	classification := Classification{
		WorkflowID:   workflow,
		Dispatchable: false,
		Terminal:     true,
		Reason:       firstNonEmpty(failureKind, outcome, status),
		Transitions: []Transition{{
			From: phase,
			To:   "terminal",
		}},
	}

	hardFailure := input.ExitCode != 0 || terminalClass == "failed" || strings.Contains(status, "failed")

	switch {
	case hardFailure:
		if state, ok := planExecuteTerminalStateForFailureKind(failureKind, failureReason); ok {
			classification.TerminalState = state
		} else {
			classification.TerminalState = TerminalStateBlocked
		}
	case outcome == "source-satisfied" || status == "source-satisfied":
		classification.TerminalState = TerminalStateSourceSatisfied
	case failureKind != "":
		if state, ok := planExecuteTerminalStateForFailureKind(failureKind, failureReason); ok {
			classification.TerminalState = state
		} else if terminalClass == "recovery-required" || reviewStatus == "blocked" || reviewStatus == "degraded" || nextAction != "" {
			classification.TerminalState = TerminalStateRecoveryOpen
		} else {
			classification.TerminalState = TerminalStateBlocked
		}
	case terminalClass == "recovery-required" || strings.Contains(reviewStatus, "blocked") || strings.Contains(reviewStatus, "degraded") || nextAction != "":
		classification.TerminalState = TerminalStateRecoveryOpen
	case status == "completed" || status == "complete" || phase == "complete" || terminalClass == "success":
		classification.TerminalState = TerminalStateComplete
	default:
		classification.TerminalState = TerminalStateBlocked
	}

	if classification.TerminalState != TerminalStateComplete && classification.TerminalState != TerminalStateSourceSatisfied {
		classification.Blockers = append(classification.Blockers, Blocker{Code: string(classification.TerminalState), Message: failureReason})
	}

	return Normalize(classification)
}

func planExecutePlannerOwnsFailureKind(kind string) bool {
	switch kind {
	case token(string(PlanExecuteFailurePlannerBlocked)),
		token(string(PlanExecuteFailurePlannerFailed)),
		token(string(PlanExecuteFailurePlanArtifactMissing)),
		token(string(PlanExecuteFailurePlanArtifactInvalid)),
		token(string(PlanExecuteFailureSourceSatisfiedUnsupportedEvidence)),
		token(string(PlanExecuteFailureMissingEditPathWithoutNewFileAuth)):
		return true
	default:
		return strings.Contains(kind, "planner") ||
			strings.Contains(kind, "plan-artifact") ||
			strings.Contains(kind, "source-satisfied") ||
			strings.Contains(kind, "missing-edit-path") ||
			strings.Contains(kind, "missing-scope") ||
			strings.Contains(kind, "invalid-scope") ||
			strings.Contains(kind, "scope-contract")
	}
}

func planExecuteTerminalStateForFailureKind(kind string, reason string) (TerminalState, bool) {
	reason = strings.TrimSpace(reason)
	switch kind {
	case token(string(PlanExecuteFailureMissingEditPathWithoutNewFileAuth)):
		return TerminalStateMissingScope, true
	case token(string(PlanExecuteFailureVerificationFailed)):
		return TerminalStateVerificationFailed, true
	case token(string(PlanExecuteFailureSourceSanityFailed)):
		return TerminalStateVerificationFailed, true
	}

	switch {
	case strings.Contains(kind, "missing-scope"):
		return TerminalStateMissingScope, true
	case strings.Contains(kind, "invalid-scope") || strings.Contains(kind, "scope-contract"):
		return TerminalStateInvalidScope, true
	case strings.Contains(kind, "stale") || strings.Contains(reason, "old_text not found"):
		return TerminalStateStaleNeedsRebase, true
	case strings.Contains(kind, "dirty") || strings.Contains(reason, "dirty"):
		return TerminalStateDirtyScope, true
	case strings.Contains(kind, "verification"):
		return TerminalStateVerificationFailed, true
	default:
		return "", false
	}
}

func Normalize(classification Classification) Classification {
	classification.Reason = strings.TrimSpace(classification.Reason)
	if classification.TerminalState == "" {
		classification.TerminalState = TerminalStateBlocked
	}
	if classification.WorkflowID == "" {
		classification.WorkflowID = WorkflowDirectAnswer
	}
	for i := range classification.Blockers {
		classification.Blockers[i].Code = token(classification.Blockers[i].Code)
		classification.Blockers[i].Message = strings.TrimSpace(classification.Blockers[i].Message)
	}
	for i := range classification.Transitions {
		classification.Transitions[i].From = token(classification.Transitions[i].From)
		classification.Transitions[i].To = token(classification.Transitions[i].To)
	}

	return classification
}

func token(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
