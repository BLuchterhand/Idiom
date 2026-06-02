package workerruntime

import "strings"

type RoleName string
type TaskFamilyID string
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
	TaskFamilyArtifactFinalAnswer       TaskFamilyID = "artifact_final_answer"
	TaskFamilyArtifactDistiller         TaskFamilyID = "artifact_distiller"
	TaskFamilyConversation              TaskFamilyID = "conversation"
	TaskFamilyDebugLogs                 TaskFamilyID = "debug_logs"
	TaskFamilyVerificationOnly          TaskFamilyID = "verification_only"
	TaskFamilyLocate                    TaskFamilyID = "locate"
	TaskFamilyLocateThenImplement       TaskFamilyID = "locate_then_implement"
	TaskFamilyExploreThenImplement      TaskFamilyID = "explore_then_implement"
	TaskFamilyInvestigateThenImplement  TaskFamilyID = "investigate_then_implement"
	TaskFamilyReviewThenImplement       TaskFamilyID = "review_then_implement"
	TaskFamilyArchitectureThenImplement TaskFamilyID = "architecture_then_implement"
	TaskFamilySecurityReview            TaskFamilyID = "security_review"
	TaskFamilyPerformanceReview         TaskFamilyID = "performance_review"
	TaskFamilyArchitecture              TaskFamilyID = "architecture"
	TaskFamilyPlanThenExecutePlanner    TaskFamilyID = "plan_then_execute_planner"
	TaskFamilyPlanThenExecuteReview     TaskFamilyID = "plan_then_execute_review_repair"
	TaskFamilyImplementation            TaskFamilyID = "implementation"
	TaskFamilyTestAuthoring             TaskFamilyID = "test_authoring"
	TaskFamilyConfigEdit                TaskFamilyID = "config_edit"
	TaskFamilyScriptEdit                TaskFamilyID = "script_edit"
	TaskFamilyGDScriptEdit              TaskFamilyID = "gdscript_edit"
	TaskFamilyDocsEdit                  TaskFamilyID = "docs_edit"
	TaskFamilyPromptEditSecurity        TaskFamilyID = "prompt_edit_security"
	TaskFamilyFailureInjection          TaskFamilyID = "failure_injection"
	TaskFamilyDirectQuestion            TaskFamilyID = "direct_question"
	TaskFamilyDirectAnswer              TaskFamilyID = "direct_answer"
	TaskFamilyDelegatedIsolatedImpl     TaskFamilyID = "delegated_isolated_implementation"
	TaskFamilyDelegatedReadOnly         TaskFamilyID = "delegated_read_only"
	TaskFamilyImplementationRepair      TaskFamilyID = "implementation_repair"
)

const (
	ContractArtifactFinalAnswer  RuntimeContractID = "artifact_final_answer"
	ContractEditIntentDistill    RuntimeContractID = "edit_intent_distillation"
	ContractDirectAnswer         RuntimeContractID = "direct_answer"
	ContractGroundedExplanation  RuntimeContractID = "grounded_explanation"
	ContractLocateTargets        RuntimeContractID = "locate_targets"
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

func taskFamilyID(value string) TaskFamilyID {
	return TaskFamilyID(strings.TrimSpace(value))
}

func permissionProfileString(profile PermissionProfileID) string {
	return string(profile)
}

func verificationContractString(contract VerificationContractID) string {
	return string(contract)
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
	return strings.EqualFold(strings.TrimSpace(value), string(profile))
}

func TaskFamilyMatches(value string, family TaskFamilyID) bool {
	return strings.EqualFold(strings.TrimSpace(value), string(family))
}

func RuntimeContractMatches(value string, contract RuntimeContractID) bool {
	return strings.EqualFold(strings.TrimSpace(value), string(contract))
}

func TaskFamilyIsConversation(value string) bool {
	return TaskFamilyMatches(value, TaskFamilyConversation)
}

func TaskFamilyIsPlanThenExecutePlanner(value string) bool {
	return TaskFamilyMatches(value, TaskFamilyPlanThenExecutePlanner)
}

func TaskFamilyIsPlanThenExecuteReview(value string) bool {
	return TaskFamilyMatches(value, TaskFamilyPlanThenExecuteReview)
}

func TaskFamilyIsArchitecture(value string) bool {
	return TaskFamilyMatches(value, TaskFamilyArchitecture)
}

func TaskFamilyIsVerificationOnly(value string) bool {
	return TaskFamilyMatches(value, TaskFamilyVerificationOnly)
}

func TaskFamilyRequiresInitialActionContract(value string) bool {
	switch strings.TrimSpace(value) {
	case string(TaskFamilyImplementation),
		string(TaskFamilyImplementationRepair),
		string(TaskFamilyDelegatedIsolatedImpl),
		string(TaskFamilyDocsEdit):
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
