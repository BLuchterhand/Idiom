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
		return RoleSpec{
			Name:                 RoleChildWrite,
			TaskFamily:           autonomy.TaskFamilyDelegatedIsolatedImpl,
			Contract:             ContractPatchSummary,
			PermissionProfile:    PermissionIsolatedWorkbranchWrite,
			AllowedTools:         workspace.ManifestToolsForSurfaceProfile(workspace.ToolSurfaceProfileIsolatedWrite),
			ForbiddenTools:       []string{"task", "mutating_parent_workspace", "git_push", "git_history_mutation", "git_index_mutation", "publish", "deploy"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus, RequiredEvidenceConfidence},
			ReadinessGate:        []ReadinessGateID{ReadinessPatchSummaryPresent, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeDelegatedScopeFirst, SearchPolicyAvoidParentWorkspaceAssumptions, SearchPolicyVerifyChangedSurface},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopPatchReadyForParentReview, StopBlockedByVerificationOrScope, StopBudgetExhaustedWithUncertainty},
		}
	case RoleChildReadOnly:
		return RoleSpec{
			Name:                 RoleChildReadOnly,
			TaskFamily:           autonomy.TaskFamilyDelegatedReadOnly,
			Contract:             ContractGroundedEvidence,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			AllowedTools:         workspace.ManifestToolsForSurfaceProfile(workspace.ToolSurfaceProfileDelegatedReadOnly),
			ForbiddenTools:       roleForbiddenWorkspaceWrites("mutating_shell", workspace.ToolNameTask),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceAnswerAddressesDelegatedQuestion, RequiredEvidenceConfidence},
			SearchPolicy:         []SearchPolicyID{SearchPolicyPreferDiscoveryQueryForCompositeQuestions, SearchPolicyPreferSymbolOrHandleProbesOverRawPathReads, SearchPolicyAnswerWhenEvidenceIsSufficient, SearchPolicyIfNotAnsweringNameOneExactMissingClaimAndProbe, SearchPolicyAvoidBroadLoops},
			VerificationContract: VerificationNotRequiredReadOnlyChild,
			StopConditions:       []StopConditionID{StopDelegatedQuestionAnswered, StopBlockedByMissingEvidenceOrPermission, StopBudgetExhaustedWithUncertainty},
		}
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
		return RoleSpec{
			Name:                 RoleImplement,
			TaskFamily:           autonomy.TaskFamilyImplementation,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			AllowedTools:         workspace.ManifestToolsForSurfaceProfile(workspace.ToolSurfaceProfileGuardedWrite),
			ForbiddenTools:       []string{"unbounded_write", "unapproved_protected_write"},
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceChangedFilesOrNoopReason, RequiredEvidenceVerificationResult},
			ReadinessGate:        []ReadinessGateID{ReadinessTargetPacketOrChangedFilesPresent, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeTargetPacketFirst, SearchPolicyTrustPreloadedCurrentStateAnchors, SearchPolicyWriteOnceEditLocationIsSupported, SearchPolicyAvoidBroadDiscoveryAfterTarget, SearchPolicyVerifyChangedSurface},
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
			ForbiddenTools:       roleForbiddenWorkspaceWrites(),
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUseWorkspaceToolsOnlyWhenNeeded},
			VerificationContract: VerificationNotRequiredConversation,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
		}
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
