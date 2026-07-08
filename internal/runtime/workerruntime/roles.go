package workerruntime

import (
	"idiom/internal/capabilities/workspace"
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
		return roleSpecFromRuntimeContract(RoleChildWrite, RuntimeContractForTaskFamily(string(autonomy.TaskFamilyDelegatedIsolatedImpl)), []string{"task", "mutating_parent_workspace", "git_push", "git_history_mutation", "git_index_mutation", "publish", "deploy"})
	case RoleChildReadOnly:
		return roleSpecFromRuntimeContract(RoleChildReadOnly, RuntimeContractForTaskFamily(string(autonomy.TaskFamilyDelegatedReadOnly)), roleForbiddenWorkspaceWrites("mutating_shell", workspace.ToolNameTask))
	case RoleRepair:
		return RoleSpec{
			Name:                 RoleRepair,
			TaskFamily:           autonomy.TaskFamilyImplementationRepair,
			Contract:             ContractRepairSummary,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			AllowedTools:         workspace.ManifestToolsForSurfaceProfile(workspace.ToolSurfaceProfileGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceFailedVerificationOutput, RequiredEvidenceChangedPathsOrNoopReasonRepair, RequiredEvidenceVerificationStatus},
			ReadinessGate:        []ReadinessGateID{ReadinessFailedOutputAddressed, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUseFailedOutputFirst, SearchPolicyPreferSmallestFollowupEdit, SearchPolicyAvoidScopeWidening},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopRepairVerified, StopRepairBlockedWithFailedOutput, StopBudgetExhaustedWithUncertainty},
		}
	case RoleImplement:
		return roleSpecFromRuntimeContract(RoleImplement, RuntimeContractForTaskFamily(string(autonomy.TaskFamilyImplementation)), []string{"unbounded_write", "unapproved_protected_write"})
	default:
		return RoleSpec{
			Name:                 RoleMain,
			TaskFamily:           autonomy.TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionConversationOnly,
			AllowedTools:         nil,
			ForbiddenTools:       roleForbiddenWorkspaceWrites(),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUseWorkspaceToolsOnlyWhenNeeded},
			VerificationContract: VerificationNotRequiredConversation,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
		}
	}
}

func roleSpecFromRuntimeContract(name RoleName, spec RuntimeContractSpec, forbiddenTools []string) RoleSpec {
	return RoleSpec{
		Name:                 name,
		TaskFamily:           spec.TaskFamily,
		Contract:             spec.Contract,
		PermissionProfile:    spec.PermissionProfile,
		AllowedTools:         workspace.ManifestToolsForSurfaceProfile(spec.ToolProfile),
		ForbiddenTools:       append([]string(nil), forbiddenTools...),
		RequiredEvidence:     append([]EvidenceRequirementID(nil), spec.RequiredEvidence...),
		ReadinessGate:        append([]ReadinessGateID(nil), spec.ReadinessGate...),
		SearchPolicy:         append([]SearchPolicyID(nil), spec.SearchPolicy...),
		VerificationContract: spec.VerificationContract,
		StopConditions:       append([]StopConditionID(nil), spec.StopConditions...),
	}
}

func roleForbiddenWorkspaceWrites(extra ...string) []string {
	tools := workspace.WorkspaceWriteToolNames()
	return append(tools, extra...)
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
		RequiredEvidence:     EvidenceRequirementStrings(spec.RequiredEvidence),
		ReadinessGate:        ReadinessGateStrings(spec.ReadinessGate),
		SearchPolicy:         typedIDStrings(spec.SearchPolicy),
		VerificationContract: string(spec.VerificationContract),
		StopConditions:       typedIDStrings(spec.StopConditions),
	})
}
