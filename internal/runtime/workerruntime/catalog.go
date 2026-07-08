package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
)

type RuntimeContractSpec struct {
	TaskFamily           autonomy.TaskFamilyID
	Contract             RuntimeContractID
	PermissionProfile    PermissionProfileID
	ToolProfile          workspace.ToolSurfaceProfile
	RequiredEvidence     []EvidenceRequirementID
	RequiredTools        []string
	ReadinessGate        []ReadinessGateID
	SearchPolicy         []SearchPolicyID
	VerificationContract VerificationContractID
	StopConditions       []StopConditionID
	FinalCritic          []FinalCriticID
}

const (
	ArtifactFinalAnswerTaskFamily = string(autonomy.TaskFamilyArtifactFinalAnswer)
	ArtifactDistillerTaskFamily   = string(autonomy.TaskFamilyArtifactDistiller)
)

func RuntimeContractForTaskFamily(family string) RuntimeContractSpec {
	switch strings.TrimSpace(family) {
	case ArtifactFinalAnswerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyArtifactFinalAnswer,
			Contract:             ContractArtifactFinalAnswer,
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceArtifactBodyOrTypedBlocker},
			VerificationContract: VerificationNotRequiredArtifactFinal,
			StopConditions:       []StopConditionID{StopArtifactBodyReturned, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticArtifactBodyOnlyOrTypedBlocker, FinalCriticNoToolCalls},
		}
	case ArtifactDistillerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyArtifactDistiller,
			Contract:             ContractEditIntentDistill,
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          workspace.ToolSurfaceProfileArtifactDistiller,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceRawOutputParserFailure, RequiredEvidenceActiveMutationContract, RequiredEvidenceRequiredEditScope},
			VerificationContract: VerificationNotRequiredNonExecutable,
			StopConditions:       []StopConditionID{StopEditIntentOrTypedBlocker},
			FinalCritic:          []FinalCriticID{FinalCriticOutputsOnlyEditIntentOrTypedBlocker, FinalCriticNoToolCalls, FinalCriticNoWriteAuthority, FinalCriticNoSubmitPlan},
		}
	case string(autonomy.TaskFamilyConversation), string(autonomy.TaskFamilyDirectQuestion), string(autonomy.TaskFamilyDebugLogs):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{SearchPolicyAnswerFromVisibleContextWhenSufficient, SearchPolicyForRepoSpecificQuestionsUseOneEfficientAllowedPath, SearchPolicyPreferBoundedSourceReadsOverBroadScans, SearchPolicyStopAfterEnoughEvidenceToAnswer},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticWellFormattedInformativeOnTopic, FinalCriticCiteOrNameEvidenceWhenWorkspaceUsed},
		}
	case "verification_only":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyVerificationOnly,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyVerificationShell,
			ToolProfile:          workspace.ToolSurfaceProfileVerificationOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationCommand, RequiredEvidencePassFailStatus},
			RequiredTools:        []string{workspace.ToolNameShell},
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyRunRequestedCheck, SearchPolicyCaptureFailureOutput, SearchPolicyDoNotEdit},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest},
		}
	case "locate":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyLocate,
			Contract:             ContractLocateTargets,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileLocateOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceExactWorkspacePath, RequiredEvidenceLineOrSnippet, RequiredEvidenceTargetMatchReason},
			SearchPolicy:         []SearchPolicyID{SearchPolicyPreferCodeNavigateOrContextQueryForSymbolLocation, SearchPolicyUseSearchContentAsFallbackForLiteralMatches, SearchPolicyStopWhenExactTargetIsSupported},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBlockedByMissingInformation},
		}
	case "locate_then_implement", "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionAnalysisReadOnlyThenWrite,
			ToolProfile:          workspace.ToolSurfaceProfileGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceTargetLocation, RequiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPaths, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell},
			ReadinessGate:        []ReadinessGateID{ReadinessTargetLocationConfirmed, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyDelegateBroadDiscoveryWhenNeeded, SearchPolicyLocateTargetBeforeEdit, SearchPolicyHistoryRequiredBeforeWrites, SearchPolicyReadCommitDescriptionsForIntendedLogicBeforeWrite, SearchPolicyVerifyChangedSurface},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopChangeVerified, StopBlockedWithFailedVerificationOrScope},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticChangedPathsReported, FinalCriticVerificationStatusReported},
		}
	case "security_review", "performance_review":
		searchPolicy := []SearchPolicyID{SearchPolicyPreferDiscoveryQueryForCompositeReview, SearchPolicyRankCandidates, SearchPolicyAnswerWhenBoundedFindingOrNoFindingIsSupported, SearchPolicyIfContinuingNameOneExactMissingClaimAndProbe, SearchPolicyDeepenIfNoBoundedFinding, SearchPolicyUseStructuralSearchForSyntaxShapes, SearchPolicyUseDependencyGraphForBoundaryOrImportQuestions}
		if strings.TrimSpace(family) == "security_review" {
			searchPolicy = append(searchPolicy, SearchPolicyPreferDiscoveryQuerySecurityReviewBeforeRawScans, SearchPolicyNameHeavyScannerNeedInsteadOfDefaultSemgrepOrJoern)
		}

		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractReviewFindings,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceBoundedFindingsOrNoFinding, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessBoundedFindingsOrNoFindingSupported},
			SearchPolicy:         searchPolicy,
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticRequiredEvidenceCited},
		}
	case "architecture":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyArchitecture,
			Contract:             ContractArchitecture,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceArchitectureBoundaryEvidence, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessArchitectureBoundarySupportedOrUncertain},
			SearchPolicy:         []SearchPolicyID{SearchPolicyDelegateBroadDiscoveryWhenNeeded, SearchPolicyPreferDiscoveryQueryForArchitectureBoundary, SearchPolicyTraceBoundariesFromNamedComponents, SearchPolicyAnswerWhenBoundaryEvidenceIsSufficient, SearchPolicyIfNotAnsweringNameOneExactMissingClaimAndProbe, SearchPolicyUseDiscoveryQueryForBoundaryImportOrEvolutionQuestion, SearchPolicyNameHeavyGraphOrHistoryNeedIfUnresolved},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticRequiredEvidenceCited},
		}
	case string(autonomy.TaskFamilyFeaturePlannerPlanner):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyFeaturePlannerPlanner,
			Contract:             ContractFeaturePlan,
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceFeaturePlanOrPlannerQuestion},
			ReadinessGate:        []ReadinessGateID{ReadinessFeaturePlanOrPlannerQuestion},
			VerificationContract: VerificationNotRequiredNonExecutable,
			StopConditions:       []StopConditionID{StopFeaturePlanReturnedOrQuestion, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticFeaturePlanSchemaOrPlannerQuestion, FinalCriticNoToolCalls},
		}
	case string(autonomy.TaskFamilyFeaturePlannerImplementor):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyFeaturePlannerImplementor,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          workspace.ToolSurfaceProfilePlanSubmissionPlanner,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidencePlanExecuteCompletion},
			ReadinessGate:        []ReadinessGateID{ReadinessPlanExecuteCompletion},
			SearchPolicy:         []SearchPolicyID{SearchPolicyDispatchAtomThroughPlanExecute, SearchPolicyPreserveRequiredEditPaths, SearchPolicyReturnTypedPlanExecuteCompletion},
			VerificationContract: VerificationNotRequiredTextMutation,
			StopConditions:       []StopConditionID{StopPlanExecuteCompletionReturned, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{FinalCriticPlanExecuteCompletionReported},
		}
	case string(autonomy.TaskFamilyPlanThenExecutePlanner):
		// Planner phase of `idiom plan-execute`. The model has read
		// access plus the submit_plan tool; per-edit writes are
		// runtime-rejected by the runner's plan-then-execute write
		// guard, and the workspace manager hides them from the
		// manifest when the workspace tool surface's plan-execute mode is active.
		// Verification is planner-owned (the executor phase
		// dispatches edits deterministically without re-running
		// tests), so the contract reflects "plan submitted, no per-
		// edit verification needed in this phase."
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyPlanThenExecutePlanner,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          workspace.ToolSurfaceProfilePlanSubmissionPlanner,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceGitHistoryRationale, RequiredEvidencePlanSubmittedOrSourceSatisfied},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.ToolNameSubmitPlan},
			ReadinessGate:        []ReadinessGateID{ReadinessSubmitPlanOrSourceSatisfiedOrBlocker},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeTargetPacketFirst, SearchPolicyTrustPreloadedCurrentStateAnchorsForScopeNotSource, SearchPolicyRequiredExistingPathsNeedSourceBodyBeforeSubmitPlan, SearchPolicyHistoryRequiredBeforeSubmitPlan, SearchPolicyReadCommitDescriptionsForLogicBeforeSubmitPlan, SearchPolicySubmitPlanWhenEditSetIsSupported, SearchPolicySubmitPlanRequiresExecutableFinalEdits, SearchPolicySourceSatisfiedWhenCurrentStateAlreadyMatches, SearchPolicyDoNotCallPerEditWrites},
			VerificationContract: VerificationNotRequiredTextMutation,
			StopConditions:       []StopConditionID{StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticSubmitPlanCalled, FinalCriticSourceSatisfiedOrBlockerOnlyWithPerPathEvidence},
		}
	case string(autonomy.TaskFamilyPlanThenExecuteReview):
		// Action-capable review for plan-execute. This lane receives
		// executor diff/evidence and may submit a corrective plan, but
		// a clean review closes with a direct checklist-backed pass. Keep
		// it review-shaped so generic mutation gates do not force a
		// submit_plan when no repair is needed.
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyPlanThenExecuteReview,
			Contract:             ContractReviewFindings,
			PermissionProfile:    PermissionPlanSubmissionReview,
			ToolProfile:          workspace.ToolSurfaceProfilePlanSubmissionReview,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationChecklist, RequiredEvidenceDecisionPassOrCorrectivePlan},
			RequiredTools:        nil,
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationChecklistCompleted, ReadinessDecisionPassOrCorrectivePlanOrBlocker},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUsePhaseEvidenceFirst, SearchPolicyDoNotReSubmitAppliedPlanEdits, SearchPolicySubmitPlanOnlyForNewCorrectiveEdits, SearchPolicyCorrectiveSubmitPlanRequiresExecutableFinalEdits, SearchPolicyPassWhenOpenObligationsAreClosed},
			VerificationContract: VerificationNotRequiredReviewLane,
			StopConditions:       []StopConditionID{StopDecisionPassWithChecklist, StopSubmitPlanCorrectiveEditsOrBlocker, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticVerificationChecklistPresent, FinalCriticDecisionReported},
		}
	case string(autonomy.TaskFamilyDelegatedIsolatedImpl):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyDelegatedIsolatedImpl,
			Contract:             ContractPatchSummary,
			PermissionProfile:    PermissionIsolatedWorkbranchWrite,
			ToolProfile:          workspace.ToolSurfaceProfileIsolatedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus, RequiredEvidenceConfidence},
			ReadinessGate:        []ReadinessGateID{ReadinessPatchSummaryPresent, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeDelegatedScopeFirst, SearchPolicyAvoidParentWorkspaceAssumptions, SearchPolicyVerifyChangedSurface},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopPatchReadyForParentReview, StopBlockedByVerificationOrScope, StopBudgetExhaustedWithUncertainty},
		}
	case string(autonomy.TaskFamilyDelegatedReadOnly):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyDelegatedReadOnly,
			Contract:             ContractGroundedEvidence,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileDelegatedReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceAnswerAddressesDelegatedQuestion, RequiredEvidenceConfidence},
			SearchPolicy:         []SearchPolicyID{SearchPolicyPreferDiscoveryQueryForCompositeQuestions, SearchPolicyPreferSymbolOrHandleProbesOverRawPathReads, SearchPolicyAnswerWhenEvidenceIsSufficient, SearchPolicyIfNotAnsweringNameOneExactMissingClaimAndProbe, SearchPolicyAvoidBroadLoops},
			VerificationContract: VerificationNotRequiredReadOnlyChild,
			StopConditions:       []StopConditionID{StopDelegatedQuestionAnswered, StopBlockedByMissingEvidenceOrPermission, StopBudgetExhaustedWithUncertainty},
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
			ToolProfile:          workspace.ToolSurfaceProfileGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell},
			ReadinessGate:        []ReadinessGateID{ReadinessChangedPathsReported, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeTargetPacketFirst, SearchPolicyTrustPreloadedCurrentStateAnchors, SearchPolicyHistoryRequiredBeforeWrites, SearchPolicyReadCommitDescriptionsForIntendedLogicBeforeWrite, SearchPolicyWriteOnceEditLocationIsSupported, SearchPolicyPreferSmallBoundedEdits, SearchPolicyVerifyChangedSurface},
			VerificationContract: verificationContract,
			StopConditions:       []StopConditionID{StopImplementationVerifiedOrFailureReported, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticChangedPathsReported, FinalCriticVerificationStatusReported},
		}
	default:
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticWellFormattedInformativeOnTopic},
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
		PermissionProfile:    string(spec.PermissionProfile),
		AllowedTools:         workspace.AllowedToolsForSurfaceProfile(allowedWorkspaceTools, spec.ToolProfile),
		RequiredEvidence:     EvidenceRequirementStrings(spec.RequiredEvidence),
		RequiredTools:        append([]string(nil), spec.RequiredTools...),
		ReadinessGate:        ReadinessGateStrings(spec.ReadinessGate),
		SearchPolicy:         typedIDStrings(spec.SearchPolicy),
		VerificationContract: string(spec.VerificationContract),
		StopConditions:       typedIDStrings(spec.StopConditions),
		FinalCritic:          typedIDStrings(spec.FinalCritic),
	}
	if spec.PermissionProfile == PermissionConversationOnly {
		// Conversation mode is intentionally tool-free. Codebase questions
		// should route to locate, architecture, review, or implementation
		// families before any workspace tool is advertised.
		brief.AllowedTools = nil
		brief.ToolManifest = nil
	}

	if spec.ToolProfile == workspace.ToolSurfaceProfileArtifactDistiller {
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
