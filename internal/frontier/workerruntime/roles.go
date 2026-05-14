package workerruntime

import "strings"

type RoleSpec struct {
	Name                 string
	TaskFamily           string
	Contract             string
	PermissionProfile    string
	AllowedTools         []string
	ForbiddenTools       []string
	RequiredEvidence     []string
	ReadinessGate        []string
	SearchPolicy         []string
	VerificationContract string
	StopConditions       []string
}

func RoleSpecFor(name string) RoleSpec {
	switch name {
	case "child_write", "workbranch_write":
		return RoleSpec{
			Name:                 "child_write",
			TaskFamily:           "delegated_isolated_implementation",
			Contract:             "patch_summary_with_verification",
			PermissionProfile:    "isolated_workbranch_write",
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityIsolatedWrite),
			ForbiddenTools:       []string{"task", "mutating_parent_workspace", "git_push", "git_history_mutation", "git_index_mutation", "publish", "deploy"},
			RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status", "confidence"},
			ReadinessGate:        []string{"patch_summary_present", "changed_paths_reported", "verification_status_reported"},
			SearchPolicy:         []string{"consume_delegated_scope_first", "avoid_parent_workspace_assumptions", "verify_changed_surface"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			StopConditions:       []string{"patch_ready_for_parent_review", "blocked_by_verification_or_scope", "budget_exhausted_with_uncertainty_declared"},
		}
	case "child_read_only", "delegated_read_only":
		return RoleSpec{
			Name:                 "child_read_only",
			TaskFamily:           "delegated_read_only",
			Contract:             "grounded_evidence_summary",
			PermissionProfile:    "read_only_workspace",
			AllowedTools:         filterAllowedTools(ManifestToolsForCapabilityProfile(CapabilityConversationReadOnly), CapabilityConversationReadOnly, readOnlyChildAllowedTools()...),
			ForbiddenTools:       []string{"edit_file", "write_file", "append_file", "mkdir", "apply_patch", "go_move_declarations", "mutating_shell", "task"},
			RequiredEvidence:     []string{"inspected_paths", "answer_addresses_delegated_question", "confidence"},
			SearchPolicy:         []string{"prefer_discovery_query_for_composite_questions", "prefer_symbol_or_handle_probes_over_raw_path_reads", "answer_when_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "avoid_broad_loops"},
			VerificationContract: "not_required_for_read_only_child",
			StopConditions:       []string{"delegated_question_answered", "blocked_by_missing_evidence_or_permission", "budget_exhausted_with_uncertainty_declared"},
		}
	case "repair", "implement_repair":
		return RoleSpec{
			Name:                 "repair",
			TaskFamily:           "implementation_repair",
			Contract:             "repair_summary_with_verification",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []string{"failed_verification_output", "repair_changed_paths_or_blocker", "verification_status"},
			ReadinessGate:        []string{"failed_output_addressed", "verification_contract_resolved"},
			SearchPolicy:         []string{"use_failed_output_first", "prefer_smallest_followup_edit", "avoid_scope_widening"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			StopConditions:       []string{"repair_verified", "repair_blocked_with_failed_output", "budget_exhausted_with_uncertainty_declared"},
		}
	case "implement":
		return RoleSpec{
			Name:                 "implement",
			TaskFamily:           "implementation",
			Contract:             "implementation_result",
			PermissionProfile:    "guarded_workspace_write",
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []string{"changed_files_or_noop_reason", "verification_result"},
			ReadinessGate:        []string{"target_packet_or_changed_files_present", "verification_contract_resolved"},
			SearchPolicy:         []string{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported", "avoid_broad_discovery_after_target", "verify_changed_surface"},
			VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			StopConditions:       []string{"implementation_verified_or_failure_reported", "blocked_by_missing_permission_or_information", "budget_exhausted_with_uncertainty_declared"},
		}
	default:
		return RoleSpec{
			Name:                 "main",
			TaskFamily:           "conversation",
			Contract:             "direct_answer",
			PermissionProfile:    "conversation_only",
			AllowedTools:         nil,
			ForbiddenTools:       []string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "go_move_declarations"},
			RequiredEvidence:     []string{"answer_addresses_user_request"},
			SearchPolicy:         []string{"use_workspace_tools_only_when_needed"},
			VerificationContract: "not_required_for_conversation",
			StopConditions:       []string{"answered_user_request", "blocked_by_missing_information"},
		}
	}
}

func readOnlyChildAllowedTools() []string {
	tools := readOnlyDiscoveryToolNames()
	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		switch strings.TrimSpace(tool) {
		case "task", "list_files":
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func RuntimeBriefForRole(role string, lane string, objective string) WorkerRuntimeBrief {
	spec := RoleSpecFor(role)
	if lane == "" {
		lane = spec.Name
	}
	return NormalizeRuntimeBrief(WorkerRuntimeBrief{
		Lane:                 lane,
		TaskFamily:           spec.TaskFamily,
		Contract:             spec.Contract,
		Objective:            objective,
		PermissionProfile:    spec.PermissionProfile,
		AllowedTools:         append([]string(nil), spec.AllowedTools...),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     append([]string(nil), spec.RequiredEvidence...),
		ReadinessGate:        append([]string(nil), spec.ReadinessGate...),
		SearchPolicy:         append([]string(nil), spec.SearchPolicy...),
		VerificationContract: spec.VerificationContract,
		StopConditions:       append([]string(nil), spec.StopConditions...),
	})
}
