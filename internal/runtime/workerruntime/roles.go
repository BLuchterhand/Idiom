package workerruntime

import (
	"strings"

	"idiom/internal/contracts/autonomy"
)

type RoleSpec struct {
	Name                 RoleName
	TaskFamily           autonomy.TaskFamilyID
	Contract             RuntimeContractID
	PermissionProfile    PermissionProfileID
	AllowedTools         []string
	ForbiddenTools       []string
	RequiredEvidence     []EvidenceRequirementID
	ReadinessGate        []ReadinessGateID
	SearchPolicy         []SearchPolicyID
	VerificationContract VerificationContractID
	StopConditions       []StopConditionID
}

func RoleSpecFor(name string) RoleSpec {
	switch roleName(name) {
	case RoleChildWrite:
		return RoleSpec{
			Name:                 RoleChildWrite,
			TaskFamily:           autonomy.TaskFamilyDelegatedIsolatedImpl,
			Contract:             ContractPatchSummary,
			PermissionProfile:    PermissionIsolatedWorkbranchWrite,
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityIsolatedWrite),
			ForbiddenTools:       []string{"task", "mutating_parent_workspace", "git_push", "git_history_mutation", "git_index_mutation", "publish", "deploy"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus, RequiredEvidenceConfidence},
			ReadinessGate:        []ReadinessGateID{ReadinessPatchSummaryPresent, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{"consume_delegated_scope_first", "avoid_parent_workspace_assumptions", "verify_changed_surface"},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopPatchReadyForParentReview, StopBlockedByVerificationOrScope, StopBudgetExhaustedWithUncertainty},
		}
	case RoleChildReadOnly:
		return RoleSpec{
			Name:                 RoleChildReadOnly,
			TaskFamily:           autonomy.TaskFamilyDelegatedReadOnly,
			Contract:             ContractGroundedEvidence,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			AllowedTools:         filterAllowedTools(ManifestToolsForCapabilityProfile(CapabilityReadOnlyDiscovery), CapabilityReadOnlyDiscovery, readOnlyChildAllowedTools()...),
			ForbiddenTools:       []string{"edit_file", "write_file", "append_file", "mkdir", "apply_patch", "go_move_declarations", "mutating_shell", "task"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceAnswerAddressesDelegatedQuestion, RequiredEvidenceConfidence},
			SearchPolicy:         []SearchPolicyID{"prefer_discovery_query_for_composite_questions", "prefer_symbol_or_handle_probes_over_raw_path_reads", "answer_when_evidence_is_sufficient", "if_not_answering_name_one_exact_missing_claim_and_probe", "avoid_broad_loops"},
			VerificationContract: VerificationNotRequiredReadOnlyChild,
			StopConditions:       []StopConditionID{StopDelegatedQuestionAnswered, StopBlockedByMissingEvidenceOrPermission, StopBudgetExhaustedWithUncertainty},
		}
	case RoleRepair:
		return RoleSpec{
			Name:                 RoleRepair,
			TaskFamily:           autonomy.TaskFamilyImplementationRepair,
			Contract:             ContractRepairSummary,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceFailedVerificationOutput, RequiredEvidenceChangedPathsOrNoopReasonRepair, RequiredEvidenceVerificationStatus},
			ReadinessGate:        []ReadinessGateID{ReadinessFailedOutputAddressed, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{"use_failed_output_first", "prefer_smallest_followup_edit", "avoid_scope_widening"},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopRepairVerified, StopRepairBlockedWithFailedOutput, StopBudgetExhaustedWithUncertainty},
		}
	case RoleImplement:
		return RoleSpec{
			Name:                 RoleImplement,
			TaskFamily:           autonomy.TaskFamilyImplementation,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			AllowedTools:         ManifestToolsForCapabilityProfile(CapabilityGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceChangedFilesOrNoopReason, RequiredEvidenceVerificationResult},
			ReadinessGate:        []ReadinessGateID{ReadinessTargetPacketOrChangedFilesPresent, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{"consume_target_packet_first", "trust_preloaded_current_state_anchors", "write_once_edit_location_is_supported", "avoid_broad_discovery_after_target", "verify_changed_surface"},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopImplementationVerifiedOrFailureReported, StopBlockedByMissingPermissionOrInformation, StopBudgetExhaustedWithUncertainty},
		}
	default:
		return RoleSpec{
			Name:                 RoleMain,
			TaskFamily:           autonomy.TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionConversationOnly,
			AllowedTools:         nil,
			ForbiddenTools:       []string{"mkdir", "edit_file", "write_file", "append_file", "apply_patch", "go_move_declarations"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{"use_workspace_tools_only_when_needed"},
			VerificationContract: VerificationNotRequiredConversation,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
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
		lane = string(spec.Name)
	}

	return NormalizeRuntimeBrief(WorkerRuntimeBrief{
		Lane:                 lane,
		TaskFamily:           string(spec.TaskFamily),
		Contract:             string(spec.Contract),
		Objective:            objective,
		PermissionProfile:    string(spec.PermissionProfile),
		AllowedTools:         append([]string(nil), spec.AllowedTools...),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     evidenceRequirementStrings(spec.RequiredEvidence),
		ReadinessGate:        readinessGateStrings(spec.ReadinessGate),
		SearchPolicy:         searchPolicyStrings(spec.SearchPolicy),
		VerificationContract: string(spec.VerificationContract),
		StopConditions:       stopConditionStrings(spec.StopConditions),
	})
}
