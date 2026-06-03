package workerruntime

import (
	"strings"

	"idiom/internal/autonomy"
	"idiom/internal/workspace"
)

const (
	requiredEvidenceGitHistoryRationale = RequiredEvidenceGitHistoryRationale
	requiredToolGitHistoryLookup        = "history_range_or_history_symbol"
)

type ToolCapabilityProfile string

const (
	CapabilityConversationReadOnly     ToolCapabilityProfile = "conversation_read_only"
	CapabilityReadOnlyDiscovery        ToolCapabilityProfile = "read_only_discovery"
	CapabilityDebugLogInvestigator     ToolCapabilityProfile = "debug_log_investigator"
	CapabilityVerificationOnly         ToolCapabilityProfile = "verification_only"
	CapabilityLocateOnly               ToolCapabilityProfile = "locate_only"
	CapabilityPlanSubmissionPlanner    ToolCapabilityProfile = "plan_submission_planner"
	CapabilityPlanSubmissionReview     ToolCapabilityProfile = "plan_submission_review"
	CapabilityArtifactDistiller        ToolCapabilityProfile = "artifact_distiller"
	CapabilityGuardedWrite             ToolCapabilityProfile = "guarded_write"
	CapabilityIsolatedWrite            ToolCapabilityProfile = "isolated_write"
	CapabilityImplementationScopedEdit ToolCapabilityProfile = "implementation_scoped_edit"
)

type RuntimeContractSpec struct {
	TaskFamily           TaskFamilyID
	Contract             RuntimeContractID
	PermissionProfile    PermissionProfileID
	ToolProfile          ToolCapabilityProfile
	ForbiddenTools       []string
	RequiredEvidence     []EvidenceRequirementID
	RequiredTools        []string
	ReadinessGate        []ReadinessGateID
	SearchPolicy         []SearchPolicyID
	VerificationContract VerificationContractID
	StopConditions       []StopConditionID
	FinalCritic          []FinalCriticID
}

const (
	ArtifactFinalAnswerTaskFamily = string(TaskFamilyArtifactFinalAnswer)
	ArtifactDistillerTaskFamily   = string(TaskFamilyArtifactDistiller)
)

func RuntimeContractForTaskFamily(family string) RuntimeContractSpec {
	switch strings.TrimSpace(family) {
	case ArtifactFinalAnswerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyArtifactFinalAnswer,
			Contract:             ContractArtifactFinalAnswer,
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          CapabilityConversationReadOnly,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), "task", "shell"), readOnlyDiscoveryToolNames()...),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceArtifactBodyOrTypedBlocker},
			VerificationContract: VerificationNotRequiredArtifactFinal,
			StopConditions:       []StopConditionID{StopArtifactBodyReturned, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{"artifact_body_only_or_typed_blocker", "no_tool_calls"},
		}
	case ArtifactDistillerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyArtifactDistiller,
			Contract:             ContractEditIntentDistill,
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          CapabilityArtifactDistiller,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), "task", "task_ledger", "shell", "submit_plan"), readOnlyDiscoveryToolNames()...),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceRawOutputParserFailure, RequiredEvidenceActiveMutationContract, RequiredEvidenceRequiredEditScope},
			VerificationContract: VerificationNotRequiredNonExecutable,
			StopConditions:       []StopConditionID{StopEditIntentOrTypedBlocker},
			FinalCritic:          []FinalCriticID{"outputs_only_edit_intent_or_typed_blocker", "no_tool_calls", "no_write_authority", "no_submit_plan"},
		}
	case "conversation", "direct_question":
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{"answer_from_visible_context_when_sufficient", "for repo-specific questions use one efficient context/search/read path", "prefer context_pack for initial workspace-localization before ad-hoc search/read", "prefer bounded source reads over broad scans", "stop_after_enough_evidence_to_answer"},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "well_formatted_informative_on_topic", "cite_or_name_evidence_when_workspace_used"},
		}
	case "debug_logs":
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyDebugLogs,
			Contract:             ContractGroundedExplanation,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityDebugLogInvestigator,
			ForbiddenTools:       append([]string(nil), workspace.WorkspaceWriteToolNames()...),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceLogRecord, RequiredEvidenceFailureOutput, RequiredEvidenceTimestampOrLatestSelection},
			RequiredTools:        []string{"log_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessLatestLogSelected, ReadinessFailureCauseSupportedByLog, ReadinessLowConfidenceTriggersDeepening},
			SearchPolicy:         []SearchPolicyID{"start_with_most_recent_log", "extract_failure_window"},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopLogCauseIdentifiedOrGapDeclared},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "verification_only":
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyVerificationOnly,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyVerificationShell,
			ToolProfile:          CapabilityVerificationOnly,
			ForbiddenTools:       append(append([]string(nil), workspace.WorkspaceWriteToolNames()...), "task"),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationCommand, RequiredEvidencePassFailStatus},
			RequiredTools:        []string{"shell"},
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{"run_requested_check", "capture_failure_output", "do_not_edit"},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request"},
		}
	case "locate":
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyLocate,
			Contract:             ContractLocateTargets,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityLocateOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceExactWorkspacePath, RequiredEvidenceLineOrSnippet, RequiredEvidenceTargetMatchReason},
			SearchPolicy:         []SearchPolicyID{"prefer_code_navigate_or_context_query_for_symbol_location", "use_search_content_as_fallback_for_literal_matches", "stop_when_exact_target_is_supported"},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBlockedByMissingInformation},
		}
	case "locate_then_implement", "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionAnalysisReadOnlyThenWrite,
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceTargetLocation, requiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPaths, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
			ReadinessGate:        []ReadinessGateID{ReadinessTargetLocationConfirmed, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{"delegate_broad_discovery_when_needed", "locate_target_before_edit", "history_required_before_writes", "read_commit_descriptions_for_intended_logic_before_write", "verify_changed_surface"},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopChangeVerified, StopBlockedWithFailedVerificationOrScope},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	case "security_review", "performance_review":
		searchPolicy := []SearchPolicyID{"prefer_discovery_query_for_composite_review", "rank_candidates", "answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe", "deepen_if_no_bounded_finding", "use_structural_search_for_syntax_shapes", "use_dependency_graph_for_boundary_or_import_questions"}
		if strings.TrimSpace(family) == "security_review" {
			searchPolicy = append(searchPolicy, "prefer_discovery_query_security_review_before_raw_scans", "name_heavy_scanner_need_instead_of_default_semgrep_or_joern")
		}
		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractReviewFindings,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceBoundedFindingsOrNoFinding, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessBoundedFindingsOrNoFindingSupported},
			SearchPolicy:         searchPolicy,
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "architecture":
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyArchitecture,
			Contract:             ContractArchitecture,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceArchitectureBoundaryEvidence, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessArchitectureBoundarySupportedOrUncertain},
			SearchPolicy:         []SearchPolicyID{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components", "answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "use_discovery_query_for_boundary_import_or_evolution_questions", "name_heavy_graph_or_history_need_if_unresolved"},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "plan_then_execute_planner":
		// Planner phase of `idiom plan-execute`. The model has read
		// access plus the submit_plan tool; per-edit writes are
		// runtime-rejected by the runner's plan-then-execute write
		// guard, and the workspace manager hides them from the
		// manifest when SetPlanThenExecutePlannerMode is active.
		// Verification is planner-owned (the executor phase
		// dispatches edits deterministically without re-running
		// tests), so the contract reflects "plan submitted, no per-
		// edit verification needed in this phase."
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyPlanThenExecutePlanner,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          CapabilityPlanSubmissionPlanner,
			RequiredEvidence:     []EvidenceRequirementID{requiredEvidenceGitHistoryRationale, RequiredEvidencePlanSubmittedOrSourceSatisfied},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "submit_plan"},
			ReadinessGate:        []ReadinessGateID{ReadinessSubmitPlanOrSourceSatisfiedOrBlocker},
			SearchPolicy:         []SearchPolicyID{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "history_required_before_submit_plan", "read_commit_descriptions_for_intended_logic_before_submit_plan", "submit_plan_when_edit_set_is_supported", "submit_plan_requires_executable_final_edits", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"},
			VerificationContract: VerificationNotRequiredTextMutation,
			StopConditions:       []StopConditionID{StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"},
		}
	case "plan_then_execute_review_repair":
		// Action-capable review for plan-execute. This lane receives
		// executor diff/evidence and may submit a corrective plan, but
		// a clean review closes with a direct checklist-backed pass. Keep
		// it review-shaped so generic mutation gates do not force a
		// submit_plan when no repair is needed.
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyPlanThenExecuteReview,
			Contract:             ContractReviewFindings,
			PermissionProfile:    PermissionPlanSubmissionReview,
			ToolProfile:          CapabilityPlanSubmissionReview,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationChecklist, RequiredEvidenceDecisionPassOrCorrectivePlan},
			RequiredTools:        nil,
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationChecklistCompleted, ReadinessDecisionPassOrCorrectivePlanOrBlocker},
			SearchPolicy:         []SearchPolicyID{"use_phase_evidence_first", "do_not_re_submit_applied_plan_edits", "submit_plan_only_for_new_corrective_edits", "corrective_submit_plan_requires_executable_final_edits", "pass_when_open_obligations_are_closed"},
			VerificationContract: VerificationNotRequiredReviewLane,
			StopConditions:       []StopConditionID{StopDecisionPassWithChecklist, StopSubmitPlanCorrectiveEditsOrBlocker, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "verification_checklist_present", "decision_reported"},
		}
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
		family = strings.TrimSpace(family)
		// Verification contract differs by family: code-producing
		// families (implementation, test_authoring, config_edit,
		// script_edit, gdscript_edit, failure_injection) need
		// tests/builds run after the mutation. Families whose edited
		// file is the practical deliverable for this atom (docs_edit,
		// prompt_edit_security) opt out of the generic post-mutation
		// gate. The runner reads VerificationContract to decide whether
		// the gate fires; the prefix "not_required_for_*" short-circuits
		// it.
		verificationContract := VerificationTestsPassedFailedSkipped
		switch family {
		case "docs_edit", "prompt_edit_security":
			verificationContract = VerificationNotRequiredTextMutation
		}
		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{requiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
			ReadinessGate:        []ReadinessGateID{ReadinessChangedPathsReported, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "history_required_before_writes", "read_commit_descriptions_for_intended_logic_before_write", "write_once_edit_location_is_supported", "prefer_small_bounded_edits", "verify_changed_surface"},
			VerificationContract: verificationContract,
			StopConditions:       []StopConditionID{StopImplementationVerifiedOrFailureReported, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	default:
		return RuntimeContractSpec{
			TaskFamily:           TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          CapabilityConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{"answers_actual_user_request", "well_formatted_informative_on_topic"},
		}
	}
}

func RuntimeBriefForTaskFamily(family string, lane string, allowedWorkspaceTools []string) WorkerRuntimeBrief {
	spec := RuntimeContractForTaskFamily(family)
	if strings.TrimSpace(lane) == "" {
		lane = "main"
	}
	brief := WorkerRuntimeBrief{
		Lane:                 strings.TrimSpace(lane),
		TaskFamily:           string(spec.TaskFamily),
		Contract:             string(spec.Contract),
		PermissionProfile:    permissionProfileString(spec.PermissionProfile),
		AllowedTools:         AllowedToolsForCapabilityProfile(allowedWorkspaceTools, spec.ToolProfile),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     evidenceRequirementStrings(spec.RequiredEvidence),
		RequiredTools:        append([]string(nil), spec.RequiredTools...),
		ReadinessGate:        readinessGateStrings(spec.ReadinessGate),
		SearchPolicy:         searchPolicyStrings(spec.SearchPolicy),
		VerificationContract: verificationContractString(spec.VerificationContract),
		StopConditions:       stopConditionStrings(spec.StopConditions),
		FinalCritic:          finalCriticStrings(spec.FinalCritic),
	}
	if spec.ToolProfile == CapabilityPlanSubmissionPlanner || spec.ToolProfile == CapabilityPlanSubmissionReview {
		// The app often builds the brief before the workspace manager flips
		// into plan-execute mode, so the pre-mode workspace snapshot can lack
		// submit_plan even though the runner will enable it before execution.
		// Keep the contract-required tool in the brief allow-list so later
		// live-surface filtering does not remove the only valid planner write.
		brief.AllowedTools = mergeStrings(brief.AllowedTools, []string{"submit_plan"})
	}
	if spec.PermissionProfile == PermissionConversationOnly {
		// Conversation mode is intentionally tool-free. Codebase questions
		// should route to locate, architecture, review, or implementation
		// families before any workspace tool is advertised.
		brief.AllowedTools = nil
		brief.ToolManifest = nil
	}
	if spec.ToolProfile == CapabilityArtifactDistiller {
		brief.AllowedTools = nil
		brief.RequiredTools = nil
		brief.ToolManifest = nil
	}
	if spec.PermissionProfile == PermissionReadOnlyWorkspace {
		brief.ForbiddenTools = mergeStrings(brief.ForbiddenTools, []string{"mutating_shell"})
	}
	if spec.PermissionProfile == PermissionReadOnlyVerificationShell {
		brief.ForbiddenTools = mergeStrings(brief.ForbiddenTools, []string{"mutating_shell"})
	}
	return NormalizeRuntimeBrief(brief)
}

func ManifestToolsForCapabilityProfile(profile ToolCapabilityProfile) []string {
	available := workspace.ToolManifestTools(toolManifestOptionsForProfile(profile))
	return AllowedToolsForCapabilityProfile(available, profile)
}

func AllowedToolsForCapabilityProfile(available []string, profile ToolCapabilityProfile) []string {
	if len(available) == 0 {
		return nil
	}
	switch profile {
	case CapabilityArtifactDistiller:
		return nil
	case CapabilityDebugLogInvestigator:
		return filterAllowedTools(available, profile, "search_files", "search_content", "read_file")
	case CapabilityVerificationOnly:
		return filterAllowedTools(available, profile, "source_sanity", "shell", "read_file", "search_files", "search_content")
	case CapabilityLocateOnly:
		return filterAllowedTools(available, profile, "pwd", "search_files", "search_content", "read_file", "code_navigate", "context_query", "context_zoom")
	case CapabilityGuardedWrite, CapabilityIsolatedWrite:
		return filterAllowedTools(available, profile, guardedWriteToolNames()...)
	case CapabilityPlanSubmissionPlanner:
		return filterAllowedTools(available, profile, planSubmissionPlannerToolNames()...)
	case CapabilityPlanSubmissionReview:
		return filterAllowedTools(available, profile, planSubmissionReviewToolNames()...)
	case CapabilityReadOnlyDiscovery:
		return filterAllowedTools(available, profile, readOnlyDiscoveryToolNames()...)
	case CapabilityConversationReadOnly:
		return filterAllowedTools(available, profile, readOnlyDiscoveryToolNames()...)
	case CapabilityImplementationScopedEdit:
		return filterAllowedTools(available, profile, implementationScopedEditToolNames()...)
	default:
		return append([]string(nil), available...)
	}
}

func toolManifestOptionsForProfile(profile ToolCapabilityProfile) workspace.ToolManifestToolOptions {
	opts := workspace.ToolManifestToolOptions{
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}
	switch profile {
	case CapabilityGuardedWrite, CapabilityIsolatedWrite:
		opts.IncludeTaskLedger = true
		opts.IncludeTask = true
		opts.IncludeWriteTools = true
	case CapabilityPlanSubmissionPlanner:
		opts.IncludePlanThenExecute = true
		opts.PlanThenExecutePlannerSurface = true
	case CapabilityPlanSubmissionReview:
		opts.IncludePlanThenExecute = true
	case CapabilityReadOnlyDiscovery, CapabilityConversationReadOnly:
		opts.IncludeTask = true
	case CapabilityVerificationOnly, CapabilityLocateOnly, CapabilityDebugLogInvestigator, CapabilityArtifactDistiller:
		// no extra options
	case CapabilityImplementationScopedEdit:
		opts.IncludeWriteTools = true
	}
	return opts
}

func readOnlyDiscoveryToolNames() []string {
	names := mergeStrings(workspace.RuntimeSourceInspectionToolNames(), []string{"read_only_capability", "harness_audit"})
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	return mergeStrings(names, []string{"task", "note"})
}

func planSubmissionReviewToolNames() []string {
	names := mergeStrings([]string{"read_file", "file_stats", "code_navigate", "source_sanity"}, []string{"harness_audit", "harness_feedback"})
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())
	return mergeStrings(names, []string{"submit_plan"})
}

func planSubmissionPlannerToolNames() []string {
	return autonomy.PlanExecutePlannerToolNames()
}

func guardedWriteToolNames() []string {
	names := []string{"task_ledger", "task", "pwd"}
	names = mergeStrings(names, workspace.RuntimeSourceInspectionToolNames())
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, []string{"git_status", "git_diff"})
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())
	names = mergeStrings(names, []string{"shell"})
	names = mergeStrings(names, workspace.WorkspaceWriteToolNames())
	return mergeStrings(names, []string{"submit_plan"})
}

func implementationScopedEditToolNames() []string {
	names := append([]string{}, workspace.WorkspaceWriteToolNames()...)
	names = mergeStrings(names, []string{"read_file", "code_navigate", "submit_plan"})
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, []string{"dependency_graph", "ctags_symbols"})
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())
	names = mergeStrings(names, []string{"source_sanity"})
	return names
}

func filterAllowedTools(available []string, profile ToolCapabilityProfile, names ...string) []string {
	if len(available) == 0 || len(names) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	var out []string
	for _, tool := range available {
		trimmed := strings.TrimSpace(tool)
		if _, ok := allowed[trimmed]; ok {
			out = append(out, tool)
			continue
		}
		// External/MCP tools are opaque to idiom, so only broad discovery,
		// planning, and implementation profiles may inherit them by
		// default. Bounded review/verification profiles use exact
		// allow-lists so a project-defined helper cannot widen the phase
		// contract or distract the model from the supplied evidence.
		if capabilityProfileAllowsExternalTools(profile) && isExternalToolName(trimmed) {
			out = append(out, tool)
		}
	}
	return out
}

func capabilityProfileAllowsExternalTools(profile ToolCapabilityProfile) bool {
	switch profile {
	case CapabilityDebugLogInvestigator, CapabilityVerificationOnly, CapabilityPlanSubmissionReview, CapabilityArtifactDistiller:
		return false
	case CapabilityImplementationScopedEdit:
		return false
	default:
		return true
	}
}

// isExternalToolName reports whether a tool name belongs to a host-project
// external tool or an MCP-discovered tool, as opposed to a built-in
// idiom tool. Built-ins are enumerated in the workspace's tool manifest;
// anything that isn't a built-in (and isn't reserved by the runner) is
// treated as a registered external/MCP tool the project owns.
//
// Implementation: built-ins follow a small set of stable name patterns
// (`go_*`, `python_*`, `ts_*`, `rust_*`, `java_*`, `cpp_*`, plus the
// hardcoded utility names like `pwd`, `read_file`, `discovery_query`,
// etc.). MCP tools always carry the `mcp:` prefix. Anything that doesn't
// match either is assumed to be a project external.
func isExternalToolName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "mcp:") {
		return true
	}
	// rg is a search_content alias; it is not a separate manifest entry
	// but must still be recognized as built-in.
	if name == "rg" {
		return false
	}
	if workspace.IsBuiltInToolName(name) {
		return false
	}
	return true
}
