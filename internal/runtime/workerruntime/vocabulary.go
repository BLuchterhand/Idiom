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

const (
	SearchPolicyAnswerFromVisibleContextWhenSufficient                SearchPolicyID = "answer_from_visible_context_when_sufficient"
	SearchPolicyForRepoSpecificQuestionsUseOneEfficientAllowedPath    SearchPolicyID = "for repo-specific questions use one efficient allowed context/search/read path"
	SearchPolicyPreferBoundedSourceReadsOverBroadScans                SearchPolicyID = "prefer bounded source reads over broad scans"
	SearchPolicyStopAfterEnoughEvidenceToAnswer                       SearchPolicyID = "stop_after_enough_evidence_to_answer"
	SearchPolicyRunRequestedCheck                                     SearchPolicyID = "run_requested_check"
	SearchPolicyCaptureFailureOutput                                  SearchPolicyID = "capture_failure_output"
	SearchPolicyDoNotEdit                                             SearchPolicyID = "do_not_edit"
	SearchPolicyPreferCodeNavigateOrContextQueryForSymbolLocation     SearchPolicyID = "prefer_code_navigate_or_context_query_for_symbol_location"
	SearchPolicyUseSearchContentAsFallbackForLiteralMatches           SearchPolicyID = "use_search_content_as_fallback_for_literal_matches"
	SearchPolicyStopWhenExactTargetIsSupported                        SearchPolicyID = "stop_when_exact_target_is_supported"
	SearchPolicyDelegateBroadDiscoveryWhenNeeded                      SearchPolicyID = "delegate_broad_discovery_when_needed"
	SearchPolicyLocateTargetBeforeEdit                                SearchPolicyID = "locate_target_before_edit"
	SearchPolicyHistoryRequiredBeforeWrites                           SearchPolicyID = "history_required_before_writes"
	SearchPolicyReadCommitDescriptionsForIntendedLogicBeforeWrite     SearchPolicyID = "read_commit_descriptions_for_intended_logic_before_write"
	SearchPolicyPreferDiscoveryQueryForCompositeReview                SearchPolicyID = "prefer_discovery_query_for_composite_review"
	SearchPolicyRankCandidates                                        SearchPolicyID = "rank_candidates"
	SearchPolicyAnswerWhenBoundedFindingOrNoFindingIsSupported        SearchPolicyID = "answer_when_bounded_finding_or_no_finding_is_supported"
	SearchPolicyIfContinuingNameOneExactMissingClaimAndProbe          SearchPolicyID = "if_continuing_name_one_exact_missing_claim_and_probe"
	SearchPolicyDeepenIfNoBoundedFinding                              SearchPolicyID = "deepen_if_no_bounded_finding"
	SearchPolicyUseStructuralSearchForSyntaxShapes                    SearchPolicyID = "use_structural_search_for_syntax_shapes"
	SearchPolicyUseDependencyGraphForBoundaryOrImportQuestions        SearchPolicyID = "use_dependency_graph_for_boundary_or_import_questions"
	SearchPolicyPreferDiscoveryQuerySecurityReviewBeforeRawScans      SearchPolicyID = "prefer_discovery_query_security_review_before_raw_scans"
	SearchPolicyNameHeavyScannerNeedInsteadOfDefaultSemgrepOrJoern    SearchPolicyID = "name_heavy_scanner_need_instead_of_default_semgrep_or_joern"
	SearchPolicyPreferDiscoveryQueryForArchitectureBoundary           SearchPolicyID = "prefer_discovery_query_for_architecture_boundary"
	SearchPolicyTraceBoundariesFromNamedComponents                    SearchPolicyID = "trace_boundaries_from_named_components"
	SearchPolicyAnswerWhenBoundaryEvidenceIsSufficient                SearchPolicyID = "answer_when_boundary_evidence_is_sufficient"
	SearchPolicyIfNotAnsweringNameOneExactMissingClaimAndProbe        SearchPolicyID = "if_not_answering_name_one_exact_missing_claim_and_probe"
	SearchPolicyUseDiscoveryQueryForBoundaryImportOrEvolutionQuestion SearchPolicyID = "use_discovery_query_for_boundary_import_or_evolution_questions"
	SearchPolicyNameHeavyGraphOrHistoryNeedIfUnresolved               SearchPolicyID = "name_heavy_graph_or_history_need_if_unresolved"
	SearchPolicyDispatchAtomThroughPlanExecute                        SearchPolicyID = "dispatch_atom_through_plan_execute"
	SearchPolicyPreserveRequiredEditPaths                             SearchPolicyID = "preserve_required_edit_paths"
	SearchPolicyReturnTypedPlanExecuteCompletion                      SearchPolicyID = "return_typed_plan_execute_completion"
	SearchPolicyConsumeTargetPacketFirst                              SearchPolicyID = "consume_target_packet_first"
	SearchPolicyTrustPreloadedCurrentStateAnchorsForScopeNotSource    SearchPolicyID = "trust_preloaded_current_state_anchors_for_scope_not_source_body"
	SearchPolicyRequiredExistingPathsNeedSourceBodyBeforeSubmitPlan   SearchPolicyID = "required_existing_paths_need_source_body_before_submit_plan"
	SearchPolicyHistoryRequiredBeforeSubmitPlan                       SearchPolicyID = "history_required_before_submit_plan"
	SearchPolicyReadCommitDescriptionsForLogicBeforeSubmitPlan        SearchPolicyID = "read_commit_descriptions_for_intended_logic_before_submit_plan"
	SearchPolicySubmitPlanWhenEditSetIsSupported                      SearchPolicyID = "submit_plan_when_edit_set_is_supported"
	SearchPolicySubmitPlanRequiresExecutableFinalEdits                SearchPolicyID = "submit_plan_requires_executable_final_edits"
	SearchPolicySourceSatisfiedWhenCurrentStateAlreadyMatches         SearchPolicyID = "source_satisfied_when_current_state_already_matches"
	SearchPolicyDoNotCallPerEditWrites                                SearchPolicyID = "do_not_call_per_edit_writes"
	SearchPolicyUsePhaseEvidenceFirst                                 SearchPolicyID = "use_phase_evidence_first"
	SearchPolicyDoNotReSubmitAppliedPlanEdits                         SearchPolicyID = "do_not_re_submit_applied_plan_edits"
	SearchPolicySubmitPlanOnlyForNewCorrectiveEdits                   SearchPolicyID = "submit_plan_only_for_new_corrective_edits"
	SearchPolicyCorrectiveSubmitPlanRequiresExecutableFinalEdits      SearchPolicyID = "corrective_submit_plan_requires_executable_final_edits"
	SearchPolicyPassWhenOpenObligationsAreClosed                      SearchPolicyID = "pass_when_open_obligations_are_closed"
	SearchPolicyTrustPreloadedCurrentStateAnchors                     SearchPolicyID = "trust_preloaded_current_state_anchors"
	SearchPolicyWriteOnceEditLocationIsSupported                      SearchPolicyID = "write_once_edit_location_is_supported"
	SearchPolicyPreferSmallBoundedEdits                               SearchPolicyID = "prefer_small_bounded_edits"
	SearchPolicyVerifyChangedSurface                                  SearchPolicyID = "verify_changed_surface"
	SearchPolicyConsumeDelegatedScopeFirst                            SearchPolicyID = "consume_delegated_scope_first"
	SearchPolicyAvoidParentWorkspaceAssumptions                       SearchPolicyID = "avoid_parent_workspace_assumptions"
	SearchPolicyPreferDiscoveryQueryForCompositeQuestions             SearchPolicyID = "prefer_discovery_query_for_composite_questions"
	SearchPolicyPreferSymbolOrHandleProbesOverRawPathReads            SearchPolicyID = "prefer_symbol_or_handle_probes_over_raw_path_reads"
	SearchPolicyAnswerWhenEvidenceIsSufficient                        SearchPolicyID = "answer_when_evidence_is_sufficient"
	SearchPolicyAvoidBroadLoops                                       SearchPolicyID = "avoid_broad_loops"
	SearchPolicyUseFailedOutputFirst                                  SearchPolicyID = "use_failed_output_first"
	SearchPolicyPreferSmallestFollowupEdit                            SearchPolicyID = "prefer_smallest_followup_edit"
	SearchPolicyAvoidScopeWidening                                    SearchPolicyID = "avoid_scope_widening"
	SearchPolicyAvoidBroadDiscoveryAfterTarget                        SearchPolicyID = "avoid_broad_discovery_after_target"
	SearchPolicyUseWorkspaceToolsOnlyWhenNeeded                       SearchPolicyID = "use_workspace_tools_only_when_needed"
)

const (
	FinalCriticArtifactBodyOnlyOrTypedBlocker                  FinalCriticID = "artifact_body_only_or_typed_blocker"
	FinalCriticNoToolCalls                                     FinalCriticID = "no_tool_calls"
	FinalCriticOutputsOnlyEditIntentOrTypedBlocker             FinalCriticID = "outputs_only_edit_intent_or_typed_blocker"
	FinalCriticNoWriteAuthority                                FinalCriticID = "no_write_authority"
	FinalCriticNoSubmitPlan                                    FinalCriticID = "no_submit_plan"
	FinalCriticAnswersActualUserRequest                        FinalCriticID = "answers_actual_user_request"
	FinalCriticWellFormattedInformativeOnTopic                 FinalCriticID = "well_formatted_informative_on_topic"
	FinalCriticCiteOrNameEvidenceWhenWorkspaceUsed             FinalCriticID = "cite_or_name_evidence_when_workspace_used"
	FinalCriticChangedPathsReported                            FinalCriticID = "changed_paths_reported"
	FinalCriticVerificationStatusReported                      FinalCriticID = "verification_status_reported"
	FinalCriticRequiredEvidenceCited                           FinalCriticID = "required_evidence_cited"
	FinalCriticFeaturePlanSchemaOrPlannerQuestion              FinalCriticID = "feature_plan_schema_or_planner_question"
	FinalCriticPlanExecuteCompletionReported                   FinalCriticID = "plan_execute_completion_reported"
	FinalCriticSubmitPlanCalled                                FinalCriticID = "submit_plan_called"
	FinalCriticSourceSatisfiedOrBlockerOnlyWithPerPathEvidence FinalCriticID = "source_satisfied_or_blocker_only_with_per_path_evidence"
	FinalCriticVerificationChecklistPresent                    FinalCriticID = "verification_checklist_present"
	FinalCriticDecisionReported                                FinalCriticID = "decision_reported"
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

func EvidenceRequirementStrings(values []EvidenceRequirementID) []string {
	if len(values) == 0 {
		return nil
	}

	return typedIDStrings(values)
}

func ReadinessGateStrings(values []ReadinessGateID) []string {
	if len(values) == 0 {
		return nil
	}

	return typedIDStrings(values)
}

func typedIDStrings[T ~string](values []T) []string {
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
