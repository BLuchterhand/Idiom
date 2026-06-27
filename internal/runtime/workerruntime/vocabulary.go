package workerruntime

import (
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
)

type RoleName string
type RuntimeContractID string
type PermissionProfileID string
type EvidenceRequirementID string
type ReadinessGateID string
type SearchPolicyID string
type VerificationContractID string
type StopConditionID string
type FinalCriticID string

const (
	RoleChildWrite    RoleName = "child_write"
	RoleChildReadOnly RoleName = "child_read_only"
	RoleRepair        RoleName = "repair"
	RoleImplement     RoleName = "implement"
	RoleMain          RoleName = "main"
)

const (
	ContractArtifactFinalAnswer  RuntimeContractID = "artifact_final_answer"
	ContractEditIntentDistill    RuntimeContractID = "edit_intent_distillation"
	ContractDirectAnswer         RuntimeContractID = "direct_answer"
	ContractGroundedExplanation  RuntimeContractID = "grounded_explanation"
	ContractLocateTargets        RuntimeContractID = "locate_targets"
	ContractFeaturePlan          RuntimeContractID = "feature_plan"
	ContractImplementationResult RuntimeContractID = "implementation_result"
	ContractReviewFindings       RuntimeContractID = "review_findings"
	ContractArchitecture         RuntimeContractID = "architecture_assessment"
	ContractPatchSummary         RuntimeContractID = "patch_summary_with_verification"
	ContractGroundedEvidence     RuntimeContractID = "grounded_evidence_summary"
	ContractRepairSummary        RuntimeContractID = "repair_summary_with_verification"
)

const (
	PermissionConversationOnly          PermissionProfileID = "conversation_only"
	PermissionReadOnlyWorkspace         PermissionProfileID = "read_only_workspace"
	PermissionReadOnlyVerificationShell PermissionProfileID = "read_only_verification_shell"
	PermissionGuardedWorkspaceWrite     PermissionProfileID = "guarded_workspace_write"
	PermissionIsolatedWorkbranchWrite   PermissionProfileID = "isolated_workbranch_write"
	PermissionAnalysisReadOnlyThenWrite PermissionProfileID = "analysis_read_only_then_guarded_writes"
	PermissionPlanSubmissionReview      PermissionProfileID = "plan_submission_review"
)

const (
	RequiredEvidenceAnswerAddressesUserRequest       EvidenceRequirementID = "answer_addresses_user_request"
	RequiredEvidenceAnswerAddressesPrompt            EvidenceRequirementID = "answer_addresses_prompt"
	RequiredEvidenceArtifactBodyOrTypedBlocker       EvidenceRequirementID = "artifact_body_or_typed_blocker"
	RequiredEvidenceRawOutputParserFailure           EvidenceRequirementID = "raw_output_parser_failure"
	RequiredEvidenceActiveMutationContract           EvidenceRequirementID = "active_mutation_contract"
	RequiredEvidenceRequiredEditScope                EvidenceRequirementID = "required_edit_scope"
	RequiredEvidenceLogRecord                        EvidenceRequirementID = "log_record"
	RequiredEvidenceFailureOutput                    EvidenceRequirementID = "failure_output"
	RequiredEvidenceTimestampOrLatestSelection       EvidenceRequirementID = "timestamp_or_latest_selection"
	RequiredEvidenceVerificationCommand              EvidenceRequirementID = "verification_command"
	RequiredEvidencePassFailStatus                   EvidenceRequirementID = "pass_fail_status"
	RequiredEvidenceExactWorkspacePath               EvidenceRequirementID = "exact_workspace_path"
	RequiredEvidenceLineOrSnippet                    EvidenceRequirementID = "line_or_snippet"
	RequiredEvidenceTargetMatchReason                EvidenceRequirementID = "target_match_reason"
	RequiredEvidenceTargetLocation                   EvidenceRequirementID = "target_location"
	RequiredEvidenceGitHistoryRationale              EvidenceRequirementID = "git_history_rationale_for_intended_logic"
	RequiredEvidenceChangedPaths                     EvidenceRequirementID = "changed_paths"
	RequiredEvidenceChangedPathsOrNoopReason         EvidenceRequirementID = "changed_paths_or_noop_reason"
	RequiredEvidenceChangedFilesOrNoopReason         EvidenceRequirementID = "changed_files_or_noop_reason"
	RequiredEvidenceVerificationStatus               EvidenceRequirementID = "verification_status"
	RequiredEvidenceVerificationResult               EvidenceRequirementID = "verification_result"
	RequiredEvidenceInspectedPaths                   EvidenceRequirementID = "inspected_paths"
	RequiredEvidenceBoundedFindingsOrNoFinding       EvidenceRequirementID = "bounded_findings_or_no_finding"
	RequiredEvidenceArchitectureBoundaryEvidence     EvidenceRequirementID = "architecture_boundary_evidence"
	RequiredEvidenceConfidence                       EvidenceRequirementID = "confidence"
	RequiredEvidencePlanSubmittedOrSourceSatisfied   EvidenceRequirementID = "plan_submitted_or_source_satisfied_or_blocker"
	RequiredEvidenceFeaturePlanOrPlannerQuestion     EvidenceRequirementID = "feature_plan_or_planner_question"
	RequiredEvidencePlanExecuteCompletion            EvidenceRequirementID = "plan_execute_completion"
	RequiredEvidenceVerificationChecklist            EvidenceRequirementID = "verification_checklist"
	RequiredEvidenceDecisionPassOrCorrectivePlan     EvidenceRequirementID = "decision_pass_or_corrective_plan_or_blocker"
	RequiredEvidenceChangedPathsOrNoopReasonRepair   EvidenceRequirementID = "repair_changed_paths_or_blocker"
	RequiredEvidenceFailedVerificationOutput         EvidenceRequirementID = "failed_verification_output"
	RequiredEvidenceAnswerAddressesDelegatedQuestion EvidenceRequirementID = "answer_addresses_delegated_question"
)

const (
	ReadinessPatchSummaryPresent                      ReadinessGateID = "patch_summary_present"
	ReadinessChangedPathsReported                     ReadinessGateID = "changed_paths_reported"
	ReadinessVerificationStatusReported               ReadinessGateID = "verification_status_reported"
	ReadinessFailedOutputAddressed                    ReadinessGateID = "failed_output_addressed"
	ReadinessVerificationContractResolved             ReadinessGateID = "verification_contract_resolved"
	ReadinessTargetPacketOrChangedFilesPresent        ReadinessGateID = "target_packet_or_changed_files_present"
	ReadinessLatestLogSelected                        ReadinessGateID = "latest_log_selected"
	ReadinessFailureCauseSupportedByLog               ReadinessGateID = "failure_cause_supported_by_log"
	ReadinessLowConfidenceTriggersDeepening           ReadinessGateID = "low_confidence_triggers_deepening"
	ReadinessTargetLocationConfirmed                  ReadinessGateID = "target_location_confirmed"
	ReadinessInspectedPathsReported                   ReadinessGateID = "inspected_paths_reported"
	ReadinessBoundedFindingsOrNoFindingSupported      ReadinessGateID = "bounded_findings_or_no_finding_supported"
	ReadinessArchitectureBoundarySupportedOrUncertain ReadinessGateID = "architecture_boundary_evidence_supported_or_uncertainty_declared"
	ReadinessSubmitPlanOrSourceSatisfiedOrBlocker     ReadinessGateID = "submit_plan_or_source_satisfied_or_blocker_reported"
	ReadinessFeaturePlanOrPlannerQuestion             ReadinessGateID = "feature_plan_or_planner_question_reported"
	ReadinessPlanExecuteCompletion                    ReadinessGateID = "plan_execute_completion_reported"
	ReadinessVerificationChecklistCompleted           ReadinessGateID = "verification_checklist_completed"
	ReadinessDecisionPassOrCorrectivePlanOrBlocker    ReadinessGateID = "decision_pass_or_corrective_plan_or_blocker"
	ReadinessNativeSubmitPlanToolCallRequired         ReadinessGateID = "native_submit_plan_tool_call_required"
	ReadinessVisibleTextualSubmitPlanJSONRequired     ReadinessGateID = "visible_textual_submit_plan_json_required"
	ReadinessBoundedAnchorRefreshBeforeCorrectedPlan  ReadinessGateID = "bounded_anchor_refresh_before_corrected_submit_plan"
)

const (
	VerificationTestsPassedFailedSkipped VerificationContractID = "tests_passed_or_failed_or_skipped_with_blocker"
	VerificationNotRequiredArtifactFinal VerificationContractID = "not_required_for_artifact_final_answer"
	VerificationNotRequiredNonExecutable VerificationContractID = "not_required_for_non_executable_artifact"
	VerificationNotRequiredConversation  VerificationContractID = "not_required_for_conversation"
	VerificationNotRequiredReadOnlyLane  VerificationContractID = "not_required_for_read_only_lane"
	VerificationNotRequiredTextMutation  VerificationContractID = "not_required_for_text_mutations"
	VerificationNotRequiredReviewLane    VerificationContractID = "not_required_for_review_lane"
	VerificationNotRequiredReadOnlyChild VerificationContractID = "not_required_for_read_only_child"
)

const (
	StopArtifactBodyReturned                         StopConditionID = "artifact_body_returned"
	StopBlockedByMissingInformation                  StopConditionID = "blocked_by_missing_information"
	StopEditIntentOrTypedBlocker                     StopConditionID = "edit_intent_v1_or_typed_blocker"
	StopAnsweredUserRequest                          StopConditionID = "answered_user_request"
	StopLogCauseIdentifiedOrGapDeclared              StopConditionID = "log_cause_identified_or_log_gap_declared"
	StopRequiredEvidenceSatisfied                    StopConditionID = "required_evidence_satisfied"
	StopChangeVerified                               StopConditionID = "change_verified"
	StopBlockedWithFailedVerificationOrScope         StopConditionID = "blocked_with_failed_verification_or_scope"
	StopBudgetExhaustedWithUncertainty               StopConditionID = "budget_exhausted_with_uncertainty_declared"
	StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker StopConditionID = "submit_plan_returned_ok_or_source_satisfied_or_blocker_reported"
	StopFeaturePlanReturnedOrQuestion                StopConditionID = "feature_plan_returned_or_planner_question"
	StopPlanExecuteCompletionReturned                StopConditionID = "plan_execute_completion_returned"
	StopDecisionPassWithChecklist                    StopConditionID = "decision_pass_with_checklist"
	StopSubmitPlanCorrectiveEditsOrBlocker           StopConditionID = "submit_plan_corrective_edits_or_blocker"
	StopImplementationVerifiedOrFailureReported      StopConditionID = "implementation_verified_or_failure_reported"
	StopBlockedByMissingPermissionOrInformation      StopConditionID = "blocked_by_missing_permission_or_information"
	StopPatchReadyForParentReview                    StopConditionID = "patch_ready_for_parent_review"
	StopBlockedByVerificationOrScope                 StopConditionID = "blocked_by_verification_or_scope"
	StopDelegatedQuestionAnswered                    StopConditionID = "delegated_question_answered"
	StopBlockedByMissingEvidenceOrPermission         StopConditionID = "blocked_by_missing_evidence_or_permission"
	StopRepairVerified                               StopConditionID = "repair_verified"
	StopRepairBlockedWithFailedOutput                StopConditionID = "repair_blocked_with_failed_output"
)

func roleName(value string) RoleName {
	switch strings.TrimSpace(value) {
	case "child_write", "workbranch_write":
		return RoleChildWrite
	case "child_read_only", "delegated_read_only":
		return RoleChildReadOnly
	case "repair", "implement_repair":
		return RoleRepair
	case "implement":
		return RoleImplement
	default:
		return RoleMain
	}
}

func taskFamilyID(value string) autonomy.TaskFamilyID {
	return autonomy.TaskFamilyID(strings.TrimSpace(value))
}

func evidenceRequirementStrings(values []EvidenceRequirementID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func EvidenceRequirementStrings(values []EvidenceRequirementID) []string {
	return evidenceRequirementStrings(values)
}

func readinessGateStrings(values []ReadinessGateID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func ReadinessGateStrings(values []ReadinessGateID) []string {
	return readinessGateStrings(values)
}

func searchPolicyStrings(values []SearchPolicyID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func stopConditionStrings(values []StopConditionID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func finalCriticStrings(values []FinalCriticID) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(string(value)); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func PermissionProfileMatches(value string, profile PermissionProfileID) bool {
	return textutil.EqualTrimFold(value, string(profile))
}

func TaskFamilyMatches(value string, family autonomy.TaskFamilyID) bool {
	return textutil.EqualTrimFold(value, string(family))
}

func RuntimeContractMatches(value string, contract RuntimeContractID) bool {
	return textutil.EqualTrimFold(value, string(contract))
}

func TaskFamilyIsConversation(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyConversation)
}

func TaskFamilyIsPlanThenExecutePlanner(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyPlanThenExecutePlanner)
}

func TaskFamilyIsPlanThenExecuteReview(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyPlanThenExecuteReview)
}

func TaskFamilyIsFeaturePlannerPlanner(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyFeaturePlannerPlanner)
}

func TaskFamilyIsFeaturePlannerImplementor(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyFeaturePlannerImplementor)
}

func TaskFamilyIsArchitecture(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyArchitecture)
}

func TaskFamilyIsVerificationOnly(value string) bool {
	return TaskFamilyMatches(value, autonomy.TaskFamilyVerificationOnly)
}

func TaskFamilyRequiresInitialActionContract(value string) bool {
	switch strings.TrimSpace(value) {
	case string(autonomy.TaskFamilyImplementation),
		string(autonomy.TaskFamilyImplementationRepair),
		string(autonomy.TaskFamilyDelegatedIsolatedImpl),
		string(autonomy.TaskFamilyDocsEdit):
		return true
	default:
		return false
	}
}

func RuntimeContractRequiresInitialAction(value string) bool {
	switch strings.TrimSpace(value) {
	case string(ContractImplementationResult),
		string(ContractPatchSummary),
		string(ContractRepairSummary):
		return true
	default:
		return false
	}
}

func RuntimeContractIsDirectAnswer(value string) bool {
	return RuntimeContractMatches(value, ContractDirectAnswer)
}

func RuntimeContractIsArchitecture(value string) bool {
	return RuntimeContractMatches(value, ContractArchitecture)
}

func RuntimeContractIsReviewFindings(value string) bool {
	return RuntimeContractMatches(value, ContractReviewFindings)
}

func RuntimeContractIsArchitectureOrReview(value string) bool {
	return RuntimeContractIsArchitecture(value) || RuntimeContractIsReviewFindings(value)
}

func RuntimeContractIsConversationDiscovery(value string) bool {
	return RuntimeContractMatches(value, ContractDirectAnswer) ||
		RuntimeContractMatches(value, ContractGroundedEvidence)
}

func PermissionProfileIsReadOnlyWorkspace(value string) bool {
	return PermissionProfileMatches(value, PermissionReadOnlyWorkspace)
}

func PermissionProfileIsReadOnlyVerificationShell(value string) bool {
	return PermissionProfileMatches(value, PermissionReadOnlyVerificationShell)
}

func PermissionProfileIsConversationOnly(value string) bool {
	return PermissionProfileMatches(value, PermissionConversationOnly)
}

func PermissionProfileAllowsInitialActionContract(value string) bool {
	switch strings.TrimSpace(value) {
	case string(PermissionGuardedWorkspaceWrite),
		string(PermissionIsolatedWorkbranchWrite),
		string(PermissionAnalysisReadOnlyThenWrite):
		return true
	default:
		return false
	}
}

func ConversationDirectAnswerReadOnly(taskFamily string, contract string, permission string) bool {
	return TaskFamilyIsConversation(taskFamily) &&
		RuntimeContractIsDirectAnswer(contract) &&
		PermissionProfileIsReadOnlyWorkspace(permission)
}

func VerificationContractSkipsGate(contract string) bool {
	switch strings.TrimSpace(contract) {
	case string(VerificationNotRequiredArtifactFinal),
		string(VerificationNotRequiredNonExecutable),
		string(VerificationNotRequiredConversation),
		string(VerificationNotRequiredReadOnlyLane),
		string(VerificationNotRequiredTextMutation),
		string(VerificationNotRequiredReviewLane),
		string(VerificationNotRequiredReadOnlyChild):
		return true
	default:
		return false
	}
}

func EvidenceRequirementMatches(value string, requirement EvidenceRequirementID) bool {
	return strings.TrimSpace(value) == string(requirement)
}
