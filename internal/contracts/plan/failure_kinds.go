package plan

import "strings"

// PlanExecuteFailureKind is the stable contract vocabulary for plan-execute
// terminal and recovery failure classifications.
type PlanExecuteFailureKind string

const (
	// PlanExecuteFailurePlannerBlocked means the planner reached a typed blocker instead of a plan artifact.
	PlanExecuteFailurePlannerBlocked PlanExecuteFailureKind = "planner_blocked"
	// PlanExecuteFailurePlannerFailed means planner execution failed before producing a valid terminal artifact.
	PlanExecuteFailurePlannerFailed PlanExecuteFailureKind = "planner_failed"
	// PlanExecuteFailurePlanArtifactMissing means no accepted plan artifact was available for execution.
	PlanExecuteFailurePlanArtifactMissing PlanExecuteFailureKind = "plan_artifact_missing"
	// PlanExecuteFailurePlanArtifactInvalid means the submitted plan artifact failed contract validation.
	PlanExecuteFailurePlanArtifactInvalid PlanExecuteFailureKind = "plan_artifact_invalid"
	// PlanExecuteFailureSourceSanityFailed means source sanity checks failed after edits or terminal synthesis.
	PlanExecuteFailureSourceSanityFailed PlanExecuteFailureKind = "source_sanity_failed"
	// PlanExecuteFailureSourceSatisfiedUnsupportedEvidence means a source-satisfied terminal used unsupported evidence.
	PlanExecuteFailureSourceSatisfiedUnsupportedEvidence PlanExecuteFailureKind = "source_satisfied_unsupported_evidence"
	// PlanExecuteFailureVerificationFailed means required verification did not pass.
	PlanExecuteFailureVerificationFailed PlanExecuteFailureKind = "verification_failed"
	// PlanExecuteFailureMissingRequestedTestEdit means a requested test-edit obligation was not covered.
	PlanExecuteFailureMissingRequestedTestEdit PlanExecuteFailureKind = "missing_requested_test_edit"
	// PlanExecuteFailureMissingRequestedImplementationEdit means a requested implementation-edit obligation was not covered.
	PlanExecuteFailureMissingRequestedImplementationEdit PlanExecuteFailureKind = "missing_requested_implementation_edit"
	// PlanExecuteFailureMissingEditPathWithoutNewFileAuth means the plan introduced an unauthorized new edit path.
	PlanExecuteFailureMissingEditPathWithoutNewFileAuth PlanExecuteFailureKind = "missing_edit_path_without_new_file_authorization"
	// PlanExecuteFailurePlanApprovalDenied means human approval for planned edits was denied or unavailable.
	PlanExecuteFailurePlanApprovalDenied PlanExecuteFailureKind = "plan_approval_denied"
	// PlanExecuteFailureTerminalCompletionContractFailed means the terminal completion record failed invariants.
	PlanExecuteFailureTerminalCompletionContractFailed PlanExecuteFailureKind = "terminal_completion_contract_failed"
	// PlanExecuteFailureTerminalCompletionSaveFailed means the harness could not persist the terminal completion.
	PlanExecuteFailureTerminalCompletionSaveFailed PlanExecuteFailureKind = "terminal_completion_save_failed"
	// PlanExecuteFailureJSONEventWriteFailed means the harness could not write a required machine-readable event.
	PlanExecuteFailureJSONEventWriteFailed PlanExecuteFailureKind = "json_event_write_failed"
	// PlanExecuteFailureExecutorFailed means execution failed outside a more specific typed failure.
	PlanExecuteFailureExecutorFailed PlanExecuteFailureKind = "executor_failed"
	// PlanExecuteFailureModelError means nested plan-execute stopped because the model backend returned model_error.
	PlanExecuteFailureModelError PlanExecuteFailureKind = "model_error"
	// PlanExecuteFailureMissingNestedStatus means a nested plan-execute run returned without typed terminal status.
	PlanExecuteFailureMissingNestedStatus PlanExecuteFailureKind = "missing_nested_plan_execute_status"
	// PlanExecuteFailureReviewModelError means review degraded because the review model failed unexpectedly.
	PlanExecuteFailureReviewModelError PlanExecuteFailureKind = "review_model_error"
	// PlanExecuteFailureReviewRepairFailed means the review-repair phase completed with failed repair evidence.
	PlanExecuteFailureReviewRepairFailed PlanExecuteFailureKind = "review_repair_failed"
	// PlanExecuteFailureReviewRepairBlocked means the review-repair phase reached a typed blocker.
	PlanExecuteFailureReviewRepairBlocked PlanExecuteFailureKind = "review_repair_blocked"
	// PlanExecuteFailureReviewRepairDegraded means review-repair could not run with full review confidence.
	PlanExecuteFailureReviewRepairDegraded PlanExecuteFailureKind = "review_repair_degraded"
)

// ParsePlanExecuteFailureKind parses exact contract values plus legacy
// hyphenated spellings used by older terminal records.
func ParsePlanExecuteFailureKind(kind string) (PlanExecuteFailureKind, bool) {
	value := PlanExecuteFailureKind(strings.TrimSpace(kind))
	for _, known := range PlanExecuteFailureKinds() {
		if value == known || canonicalPlanExecuteVocabularyToken(string(value)) == canonicalPlanExecuteVocabularyToken(string(known)) {
			return known, true
		}
	}

	return "", false
}

func PlanExecuteFailureKindEditSetObligation(kind PlanExecuteFailureKind) (EditSetObligationKind, bool) {
	switch kind {
	case PlanExecuteFailureMissingRequestedTestEdit:
		return EditSetObligationKindTestEdit, true
	case PlanExecuteFailureMissingRequestedImplementationEdit:
		return EditSetObligationKindImplementationEdit, true
	default:
		return "", false
	}
}

func PlanExecuteFailureKinds() []PlanExecuteFailureKind {
	return []PlanExecuteFailureKind{
		PlanExecuteFailurePlannerBlocked,
		PlanExecuteFailurePlannerFailed,
		PlanExecuteFailurePlanArtifactMissing,
		PlanExecuteFailurePlanArtifactInvalid,
		PlanExecuteFailureSourceSanityFailed,
		PlanExecuteFailureSourceSatisfiedUnsupportedEvidence,
		PlanExecuteFailureVerificationFailed,
		PlanExecuteFailureMissingRequestedTestEdit,
		PlanExecuteFailureMissingRequestedImplementationEdit,
		PlanExecuteFailureMissingEditPathWithoutNewFileAuth,
		PlanExecuteFailurePlanApprovalDenied,
		PlanExecuteFailureTerminalCompletionContractFailed,
		PlanExecuteFailureTerminalCompletionSaveFailed,
		PlanExecuteFailureJSONEventWriteFailed,
		PlanExecuteFailureExecutorFailed,
		PlanExecuteFailureModelError,
		PlanExecuteFailureMissingNestedStatus,
		PlanExecuteFailureReviewModelError,
		PlanExecuteFailureReviewRepairFailed,
		PlanExecuteFailureReviewRepairBlocked,
		PlanExecuteFailureReviewRepairDegraded,
	}
}

func canonicalPlanExecuteVocabularyToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}
