package workerruntime

import (
	"strings"

	"idiom/internal/workspace"
)

const (
	requiredEvidenceGitHistoryRationale = "git_history_rationale_for_intended_logic"
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
	TaskFamily           string
	Contract             string
	PermissionProfile    string
	ToolProfile          ToolCapabilityProfile
	ForbiddenTools       []string
	RequiredEvidence     []string
	RequiredTools        []string
	ReadinessGate        []string
	SearchPolicy         []string
	VerificationContract string
	StopConditions       []string
	FinalCritic          []string
}

const (
	ArtifactFinalAnswerTaskFamily = "artifact_final_answer"
	ArtifactDistillerTaskFamily   = "artifact_distiller"
)

func RuntimeContractForTaskFamily(family string) RuntimeContractSpec {
	switch strings.TrimSpace(family) {
	case ArtifactFinalAnswerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           ArtifactFinalAnswerTaskFamily,
			Contract:             "artifact_final_answer",
			PermissionProfile:    "conversation_only",
			ToolProfile:          CapabilityConversationReadOnly,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), "task", "shell"), readOnlyDiscoveryToolNames()...),
			RequiredEvidence:     []string{"artifact_body_or_typed_blocker"},
			VerificationContract: "not_required_for_artifact_final_answer",
			StopConditions:       []string{"artifact_body_returned", "blocked_by_missing_information"},
			FinalCritic:          []string{"artifact_body_only_or_typed_blocker", "no_tool_calls"},
		}
	case ArtifactDistillerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           ArtifactDistillerTaskFamily,
			Contract:             "edit_intent_distillation",
			PermissionProfile:    "conversation_only",
			ToolProfile:          CapabilityArtifactDistiller,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), "task", "task_ledger", "shell", "submit_plan"), readOnlyDiscoveryToolNames()...),
			RequiredEvidence:     []string{"raw_output_parser_failure", "active_mutation_contract", "required_edit_scope"},
			VerificationContract: "not_required_for_non_executable_artifact",
			StopConditions:       []string{"edit_intent_v1_or_typed_blocker"},
			FinalCritic:          []string{"outputs_only_edit_intent_or_typed_blocker", "no_tool_calls", "no_write_authority", "no_submit_plan"},
		}
	case "conversation", "direct_question":
		return RuntimeContractSpec{
			TaskFamily:           "conversation",
			Contract:             "direct_answer",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityConversationReadOnly,
			RequiredEvidence:     []string{"answer_addresses_user_request"},
			SearchPolicy:         []string{"answer_from_visible_context_when_sufficient", "for repo-specific questions use one efficient context/search/read path", "prefer context_pack for initial workspace-localization before ad-hoc search/read", "prefer bounded source reads over broad scans", "stop_after_enough_evidence_to_answer"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"answered_user_request", "blocked_by_missing_information"},
			FinalCritic:          []string{"answers_actual_user_request", "well_formatted_informative_on_topic", "cite_or_name_evidence_when_workspace_used"},
		}
	case "debug_logs":
		return RuntimeContractSpec{
			TaskFamily:           "debug_logs",
			Contract:             "grounded_explanation",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityDebugLogInvestigator,
			ForbiddenTools:       append([]string(nil), workspace.WorkspaceWriteToolNames()...),
			RequiredEvidence:     []string{"log_record", "failure_output", "timestamp_or_latest_selection"},
			RequiredTools:        []string{"log_inspection"},
			ReadinessGate:        []string{"latest_log_selected", "failure_cause_supported_by_log", "low_confidence_triggers_deepening"},
			SearchPolicy:         []string{"start_with_most_recent_log", "extract_failure_window"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"log_cause_identified_or_log_gap_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "verification_only":
		return RuntimeContractSpec{
			TaskFamily:           "verification_only",
			Contract:             "direct_answer",
			PermissionProfile:    "read_only_verification_shell",
			ToolProfile:          CapabilityVerificationOnly,
			ForbiddenTools:       append(append([]string(nil), workspace.WorkspaceWriteToolNames()...), "task"),
			RequiredEvidence:     []string{"verification_command", "pass_fail_status"},
			RequiredTools:        []string{"shell"},
			ReadinessGate:        []string{"verification_status_reported"},
			SearchPolicy:         []string{"run_requested_check", "capture_failure_output", "do_not_edit"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied"},
			FinalCritic:          []string{"answers_actual_user_request"},
		}
	case "locate":
		return RuntimeContractSpec{
			TaskFamily:           "locate",
			Contract:             "locate_targets",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityLocateOnly,
			RequiredEvidence:     []string{"exact_workspace_path", "line_or_snippet", "target_match_reason"},
			SearchPolicy:         []string{"prefer_code_navigate_or_context_query_for_symbol_location", "use_search_content_as_fallback_for_literal_matches", "stop_when_exact_target_is_supported"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "blocked_by_missing_information"},
		}
	case "locate_then_implement", "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return RuntimeContractSpec{
			TaskFamily:           strings.TrimSpace(family),
			Contract:             "implementation_result",
			PermissionProfile:    "analysis_read_only_then_guarded_writes",
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []string{"target_location", requiredEvidenceGitHistoryRationale, "changed_paths", "verification_status"},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
			ReadinessGate:        []string{"target_location_confirmed", "changed_paths_reported", "verification_status_reported"},
			SearchPolicy:         []string{"delegate_broad_discovery_when_needed", "locate_target_before_edit", "history_required_before_writes", "read_commit_descriptions_for_intended_logic_before_write", "verify_changed_surface"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			StopConditions:       []string{"change_verified", "blocked_with_failed_verification_or_scope"},
			FinalCritic:          []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	case "security_review", "performance_review":
		searchPolicy := []string{"prefer_discovery_query_for_composite_review", "rank_candidates", "answer_when_bounded_finding_or_no_finding_is_supported", "if_continuing_name_one_exact_missing_claim_and_probe", "deepen_if_no_bounded_finding", "use_structural_search_for_syntax_shapes", "use_dependency_graph_for_boundary_or_import_questions"}
		if strings.TrimSpace(family) == "security_review" {
			searchPolicy = append(searchPolicy, "prefer_discovery_query_security_review_before_raw_scans", "name_heavy_scanner_need_instead_of_default_semgrep_or_joern")
		}
		return RuntimeContractSpec{
			TaskFamily:           strings.TrimSpace(family),
			Contract:             "review_findings",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []string{"inspected_paths", "bounded_findings_or_no_finding", "confidence"},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"},
			SearchPolicy:         searchPolicy,
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "budget_exhausted_with_uncertainty_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
		}
	case "architecture":
		return RuntimeContractSpec{
			TaskFamily:           "architecture",
			Contract:             "architecture_assessment",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityReadOnlyDiscovery,
			RequiredEvidence:     []string{"inspected_paths", "architecture_boundary_evidence", "confidence"},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"},
			SearchPolicy:         []string{"delegate_broad_discovery_when_needed", "prefer_discovery_query_for_architecture_boundary", "trace_boundaries_from_named_components", "answer_when_boundary_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "use_discovery_query_for_boundary_import_or_evolution_questions", "name_heavy_graph_or_history_need_if_unresolved"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"required_evidence_satisfied", "budget_exhausted_with_uncertainty_declared"},
			FinalCritic:          []string{"answers_actual_user_request", "required_evidence_cited"},
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
			TaskFamily:           "plan_then_execute_planner",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			ToolProfile:          CapabilityPlanSubmissionPlanner,
			RequiredEvidence:     []string{requiredEvidenceGitHistoryRationale, "plan_submitted_or_source_satisfied_or_blocker"},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "submit_plan"},
			ReadinessGate:        []string{"submit_plan_or_source_satisfied_or_blocker_reported"},
			SearchPolicy:         []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "history_required_before_submit_plan", "read_commit_descriptions_for_intended_logic_before_submit_plan", "submit_plan_when_edit_set_is_supported", "submit_plan_requires_executable_final_edits", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"},
			VerificationContract: "not_required_for_text_mutations",
			StopConditions:       []string{"submit_plan_returned_ok_or_source_satisfied_or_blocker_reported", "blocked_by_missing_permission_or_information"},
			FinalCritic:          []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"},
		}
	case "plan_then_execute_review_repair":
		// Action-capable review for plan-execute. This lane receives
		// executor diff/evidence and may submit a corrective plan, but
		// a clean review closes with a direct checklist-backed pass. Keep
		// it review-shaped so generic mutation gates do not force a
		// submit_plan when no repair is needed.
		return RuntimeContractSpec{
			TaskFamily:           "plan_then_execute_review_repair",
			Contract:             "review_findings",
			PermissionProfile:    "plan_submission_review",
			ToolProfile:          CapabilityPlanSubmissionReview,
			RequiredEvidence:     []string{"verification_checklist", "decision_pass_or_corrective_plan_or_blocker"},
			RequiredTools:        nil,
			ReadinessGate:        []string{"verification_checklist_completed", "decision_pass_or_corrective_plan_or_blocker"},
			SearchPolicy:         []string{"use_phase_evidence_first", "do_not_re_submit_applied_plan_edits", "submit_plan_only_for_new_corrective_edits", "corrective_submit_plan_requires_executable_final_edits", "pass_when_open_obligations_are_closed"},
			VerificationContract: "not_required_for_review_lane",
			StopConditions:       []string{"decision_pass_with_checklist", "submit_plan_corrective_edits_or_blocker", "blocked_by_missing_information"},
			FinalCritic:          []string{"answers_actual_user_request", "verification_checklist_present", "decision_reported"},
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
		verificationContract := "tests_passed_or_failed_or_skipped_with_blocker"
		switch family {
		case "docs_edit", "prompt_edit_security":
			verificationContract = "not_required_for_text_mutations"
		}
		return RuntimeContractSpec{
			TaskFamily:           family,
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []string{requiredEvidenceGitHistoryRationale, "changed_paths_or_noop_reason", "verification_status"},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
			ReadinessGate:        []string{"changed_paths_reported", "verification_contract_resolved"},
			SearchPolicy:         []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "history_required_before_writes", "read_commit_descriptions_for_intended_logic_before_write", "write_once_edit_location_is_supported", "prefer_small_bounded_edits", "verify_changed_surface"},
			VerificationContract: verificationContract,
			StopConditions:       []string{"implementation_verified_or_failure_reported", "blocked_by_missing_permission_or_information"},
			FinalCritic:          []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"},
		}
	default:
		return RuntimeContractSpec{
			TaskFamily:           "conversation",
			Contract:             "direct_answer",
			PermissionProfile:    "read_only_workspace",
			ToolProfile:          CapabilityConversationReadOnly,
			RequiredEvidence:     []string{"answer_addresses_user_request"},
			VerificationContract: "not_required_for_read_only_lane",
			StopConditions:       []string{"answered_user_request", "blocked_by_missing_information"},
			FinalCritic:          []string{"answers_actual_user_request", "well_formatted_informative_on_topic"},
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
		TaskFamily:           spec.TaskFamily,
		Contract:             spec.Contract,
		PermissionProfile:    spec.PermissionProfile,
		AllowedTools:         AllowedToolsForCapabilityProfile(allowedWorkspaceTools, spec.ToolProfile),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     append([]string(nil), spec.RequiredEvidence...),
		RequiredTools:        append([]string(nil), spec.RequiredTools...),
		ReadinessGate:        append([]string(nil), spec.ReadinessGate...),
		SearchPolicy:         append([]string(nil), spec.SearchPolicy...),
		VerificationContract: spec.VerificationContract,
		StopConditions:       append([]string(nil), spec.StopConditions...),
		FinalCritic:          append([]string(nil), spec.FinalCritic...),
	}
	if spec.ToolProfile == CapabilityPlanSubmissionPlanner || spec.ToolProfile == CapabilityPlanSubmissionReview {
		// The app often builds the brief before the workspace manager flips
		// into plan-execute mode, so the pre-mode workspace snapshot can lack
		// submit_plan even though the runner will enable it before execution.
		// Keep the contract-required tool in the brief allow-list so later
		// live-surface filtering does not remove the only valid planner write.
		brief.AllowedTools = mergeStrings(brief.AllowedTools, []string{"submit_plan"})
	}
	if spec.PermissionProfile == "conversation_only" {
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
	if spec.PermissionProfile == "read_only_workspace" {
		brief.ForbiddenTools = mergeStrings(brief.ForbiddenTools, []string{"mutating_shell"})
	}
	if spec.PermissionProfile == "read_only_verification_shell" {
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
	return []string{
		"search_files",
		"search_content",
		"read_file",
		"file_stats",
		"code_navigate",
		"read_only_capability",
		"source_sanity",
		"harness_audit",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"task",
		"note",
	}
}

func planSubmissionReviewToolNames() []string {
	return []string{
		"read_file",
		"file_stats",
		"code_navigate",
		"source_sanity",
		"harness_audit",
		"harness_feedback",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"history_range",
		"history_symbol",
		"blame_range",
		"submit_plan",
	}
}

func planSubmissionPlannerToolNames() []string {
	return []string{
		"search_files",
		"search_content",
		"read_file",
		"file_stats",
		"code_navigate",
		"harness_feedback",
		"recovery_inspect",
		"recovery_status",
		"product_efforts",
		"product_status",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"history_range",
		"history_symbol",
		"blame_range",
		"submit_plan",
	}
}

func guardedWriteToolNames() []string {
	return []string{
		"task_ledger",
		"task",
		"pwd",
		"search_files",
		"search_content",
		"read_file",
		"file_stats",
		"code_navigate",
		"source_sanity",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"git_status",
		"git_diff",
		"history_range",
		"history_symbol",
		"blame_range",
		"shell",
		"mkdir",
		"edit_file",
		"write_file",
		"append_file",
		"apply_patch",
		"delegate_edit",
		"submit_plan",
		"go_move_declarations",
	}
}

func implementationScopedEditToolNames() []string {
	names := append([]string{}, workspace.WorkspaceWriteToolNames()...)
	names = append(names,
		"read_file",
		"code_navigate",
		"submit_plan",
		"context_status",
		"context_query",
		"context_zoom",
		"context_pack",
		"context_impact",
		"discovery_query",
		"structural_search",
		"dependency_graph",
		"ctags_symbols",
		"history_range",
		"history_symbol",
		"blame_range",
		"source_sanity",
	)
	return names
}

func selectAllowedTools(available []string, allowWrites bool, allowShell bool, allowTask bool) []string {
	if len(available) == 0 {
		return nil
	}
	var out []string
	for _, tool := range available {
		switch {
		case workspace.IsWorkspaceWriteTool(tool):
			if allowWrites {
				out = append(out, tool)
			}
		case tool == "shell":
			if allowShell {
				out = append(out, tool)
			}
		case tool == "task", tool == "task_ledger":
			if allowTask {
				out = append(out, tool)
			}
		default:
			out = append(out, tool)
		}
	}
	return out
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
