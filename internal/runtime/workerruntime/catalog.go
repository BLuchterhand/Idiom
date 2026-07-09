package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
)

type RuntimeContractSpec struct {
	TaskFamily           autonomy.TaskFamilyID
	Contract             RuntimeContractID
	route                runtimeRouteSpec
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

type runtimeRouteSpec struct {
	InitialLane           string
	FollowupLane          string
	DefaultReason         string
	MutationCapable       bool
	EvidenceFirstMutation bool
	PlanSubmission        bool
	DirectAnswerReadOnly  bool
	VerificationOwner     RuntimeVerificationOwner
}

const (
	ArtifactFinalAnswerTaskFamily = string(autonomy.TaskFamilyArtifactFinalAnswer)
	ArtifactDistillerTaskFamily   = string(autonomy.TaskFamilyArtifactDistiller)
)

func RuntimeContractForTaskFamily(family string) RuntimeContractSpec {
	spec, ok := runtimeContractSpecForTaskFamily(family)
	if ok {
		return spec
	}

	return runtimeFallbackContractSpec()
}

func runtimeContractSpecForRouteFamily(family string) (RuntimeContractSpec, bool) {
	switch strings.TrimSpace(family) {
	case "plan_execute", "plan_execute_planner", "plan_then_execute":
		return runtimeContractSpecForTaskFamily(string(autonomy.TaskFamilyPlanThenExecutePlanner))
	case "multi_turn_state":
		spec, ok := runtimeContractSpecForTaskFamily(string(autonomy.TaskFamilyConversation))
		if !ok {
			return RuntimeContractSpec{}, false
		}
		spec.route.DefaultReason = "context_followup"
		return spec, true
	default:
		return runtimeContractSpecForTaskFamily(family)
	}
}

func runtimeContractSpecForTaskFamily(family string) (RuntimeContractSpec, bool) {
	family = strings.TrimSpace(family)
	switch family {
	case ArtifactFinalAnswerTaskFamily:
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyArtifactFinalAnswer,
			Contract:             ContractArtifactFinalAnswer,
			route:                runtimeRouteSpec{InitialLane: "main", DefaultReason: "artifact_final_answer"},
			PermissionProfile:    PermissionConversationOnly,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceArtifactBodyOrTypedBlocker},
			VerificationContract: VerificationNotRequiredArtifactFinal,
			StopConditions:       []StopConditionID{StopArtifactBodyReturned, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticArtifactBodyOnlyOrTypedBlocker, FinalCriticNoToolCalls},
		}, true
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
		}, true
	case string(autonomy.TaskFamilyConversation), string(autonomy.TaskFamilyDirectQuestion), string(autonomy.TaskFamilyDebugLogs):
		routeReason := "conversation"
		if family == string(autonomy.TaskFamilyDebugLogs) {
			routeReason = "legacy_debug_logs_collapsed_to_conversation"
		}
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyConversation,
			Contract:             ContractDirectAnswer,
			route:                runtimeRouteSpec{InitialLane: "main", DefaultReason: routeReason, DirectAnswerReadOnly: true},
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileConversationReadOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceAnswerAddressesUserRequest},
			SearchPolicy:         []SearchPolicyID{SearchPolicyAnswerFromVisibleContextWhenSufficient, SearchPolicyForRepoSpecificQuestionsUseOneEfficientAllowedPath, SearchPolicyPreferBoundedSourceReadsOverBroadScans, SearchPolicyStopAfterEnoughEvidenceToAnswer},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopAnsweredUserRequest, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticWellFormattedInformativeOnTopic, FinalCriticCiteOrNameEvidenceWhenWorkspaceUsed},
		}, true
	case "verification_only":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyVerificationOnly,
			Contract:             ContractDirectAnswer,
			route:                runtimeRouteSpec{InitialLane: "main", DefaultReason: "verification_request", DirectAnswerReadOnly: true},
			PermissionProfile:    PermissionReadOnlyVerificationShell,
			ToolProfile:          workspace.ToolSurfaceProfileVerificationOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationCommand, RequiredEvidencePassFailStatus},
			RequiredTools:        []string{workspace.ToolNameShell},
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyRunRequestedCheck, SearchPolicyCaptureFailureOutput, SearchPolicyDoNotEdit},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest},
		}, true
	case "locate":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyLocate,
			Contract:             ContractLocateTargets,
			route:                runtimeRouteSpec{InitialLane: "locate", DefaultReason: "locate_request"},
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileLocateOnly,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceExactWorkspacePath, RequiredEvidenceLineOrSnippet, RequiredEvidenceTargetMatchReason},
			SearchPolicy:         []SearchPolicyID{SearchPolicyPreferCodeNavigateOrContextQueryForSymbolLocation, SearchPolicyUseSearchContentAsFallbackForLiteralMatches, SearchPolicyStopWhenExactTargetIsSupported},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBlockedByMissingInformation},
		}, true
	case "locate_then_implement", "explore_then_implement", "investigate_then_implement", "review_then_implement", "architecture_then_implement":
		route := runtimeRouteSpec{InitialLane: "locate", FollowupLane: "implement", DefaultReason: "locate_request", MutationCapable: true, VerificationOwner: RuntimeVerificationOwnerExecutor}
		switch family {
		case "explore_then_implement", "investigate_then_implement":
			route = runtimeRouteSpec{InitialLane: "explore", FollowupLane: "implement", DefaultReason: "explore_request", MutationCapable: true, EvidenceFirstMutation: true, VerificationOwner: RuntimeVerificationOwnerExecutor}
		case "review_then_implement":
			route = runtimeRouteSpec{InitialLane: "review", FollowupLane: "implement", DefaultReason: "review_request", MutationCapable: true, EvidenceFirstMutation: true, VerificationOwner: RuntimeVerificationOwnerExecutor}
		case "architecture_then_implement":
			route = runtimeRouteSpec{InitialLane: "architecture", FollowupLane: "implement", DefaultReason: "architecture_request", MutationCapable: true, EvidenceFirstMutation: true, VerificationOwner: RuntimeVerificationOwnerExecutor}
		}
		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractImplementationResult,
			route:                route,
			PermissionProfile:    PermissionAnalysisReadOnlyThenWrite,
			ToolProfile:          workspace.ToolSurfaceProfileGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceTargetLocation, RequiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPaths, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell},
			ReadinessGate:        []ReadinessGateID{ReadinessTargetLocationConfirmed, ReadinessChangedPathsReported, ReadinessVerificationStatusReported},
			SearchPolicy:         []SearchPolicyID{SearchPolicyDelegateBroadDiscoveryWhenNeeded, SearchPolicyLocateTargetBeforeEdit, SearchPolicyHistoryRequiredBeforeWrites, SearchPolicyReadCommitDescriptionsForIntendedLogicBeforeWrite, SearchPolicyVerifyChangedSurface},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopChangeVerified, StopBlockedWithFailedVerificationOrScope},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticChangedPathsReported, FinalCriticVerificationStatusReported},
		}, true
	case "security_review", "performance_review":
		searchPolicy := []SearchPolicyID{SearchPolicyPreferDiscoveryQueryForCompositeReview, SearchPolicyRankCandidates, SearchPolicyAnswerWhenBoundedFindingOrNoFindingIsSupported, SearchPolicyIfContinuingNameOneExactMissingClaimAndProbe, SearchPolicyDeepenIfNoBoundedFinding, SearchPolicyUseStructuralSearchForSyntaxShapes, SearchPolicyUseDependencyGraphForBoundaryOrImportQuestions}
		if family == "security_review" {
			searchPolicy = append(searchPolicy, SearchPolicyPreferDiscoveryQuerySecurityReviewBeforeRawScans, SearchPolicyNameHeavyScannerNeedInsteadOfDefaultSemgrepOrJoern)
		}

		return RuntimeContractSpec{
			TaskFamily:           taskFamilyID(family),
			Contract:             ContractReviewFindings,
			route:                runtimeRouteSpec{InitialLane: "review", DefaultReason: family},
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceBoundedFindingsOrNoFinding, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessBoundedFindingsOrNoFindingSupported},
			SearchPolicy:         searchPolicy,
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticRequiredEvidenceCited},
		}, true
	case "architecture":
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyArchitecture,
			Contract:             ContractArchitecture,
			route:                runtimeRouteSpec{InitialLane: "architecture", DefaultReason: "architecture_request"},
			PermissionProfile:    PermissionReadOnlyWorkspace,
			ToolProfile:          workspace.ToolSurfaceProfileReadOnlyDiscovery,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceInspectedPaths, RequiredEvidenceArchitectureBoundaryEvidence, RequiredEvidenceConfidence},
			RequiredTools:        []string{"workspace_inspection"},
			ReadinessGate:        []ReadinessGateID{ReadinessInspectedPathsReported, ReadinessArchitectureBoundarySupportedOrUncertain},
			SearchPolicy:         []SearchPolicyID{SearchPolicyDelegateBroadDiscoveryWhenNeeded, SearchPolicyPreferDiscoveryQueryForArchitectureBoundary, SearchPolicyTraceBoundariesFromNamedComponents, SearchPolicyAnswerWhenBoundaryEvidenceIsSufficient, SearchPolicyIfNotAnsweringNameOneExactMissingClaimAndProbe, SearchPolicyUseDiscoveryQueryForBoundaryImportOrEvolutionQuestion, SearchPolicyNameHeavyGraphOrHistoryNeedIfUnresolved},
			VerificationContract: VerificationNotRequiredReadOnlyLane,
			StopConditions:       []StopConditionID{StopRequiredEvidenceSatisfied, StopBudgetExhaustedWithUncertainty},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticRequiredEvidenceCited},
		}, true
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
		}, true
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
		}, true
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
			route:                runtimeRouteSpec{InitialLane: "implement", DefaultReason: string(autonomy.TaskFamilyPlanThenExecutePlanner), MutationCapable: true, PlanSubmission: true, VerificationOwner: RuntimeVerificationOwnerPlannerOwned},
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          workspace.ToolSurfaceProfilePlanSubmissionPlanner,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceGitHistoryRationale, RequiredEvidencePlanSubmittedOrSourceSatisfied},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.ToolNameSubmitPlan},
			ReadinessGate:        []ReadinessGateID{ReadinessSubmitPlanOrSourceSatisfiedOrBlocker},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeTargetPacketFirst, SearchPolicyTrustPreloadedCurrentStateAnchorsForScopeNotSource, SearchPolicyRequiredExistingPathsNeedSourceBodyBeforeSubmitPlan, SearchPolicyHistoryRequiredBeforeSubmitPlan, SearchPolicyReadCommitDescriptionsForLogicBeforeSubmitPlan, SearchPolicySubmitPlanWhenEditSetIsSupported, SearchPolicySubmitPlanRequiresExecutableFinalEdits, SearchPolicySourceSatisfiedWhenCurrentStateAlreadyMatches, SearchPolicyDoNotCallPerEditWrites},
			VerificationContract: VerificationNotRequiredTextMutation,
			StopConditions:       []StopConditionID{StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticSubmitPlanCalled, FinalCriticSourceSatisfiedOrBlockerOnlyWithPerPathEvidence},
		}, true
	case string(autonomy.TaskFamilyPlanThenExecuteReview):
		// Action-capable review for plan-execute. This lane receives
		// executor diff/evidence and may submit a corrective plan, but
		// a clean review closes with a direct checklist-backed pass. Keep
		// it review-shaped so generic mutation gates do not force a
		// submit_plan when no repair is needed.
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyPlanThenExecuteReview,
			Contract:             ContractReviewFindings,
			route:                runtimeRouteSpec{InitialLane: "review", DefaultReason: string(autonomy.TaskFamilyPlanThenExecuteReview), PlanSubmission: true, VerificationOwner: RuntimeVerificationOwnerPlannerOwned},
			PermissionProfile:    PermissionPlanSubmissionReview,
			ToolProfile:          workspace.ToolSurfaceProfilePlanSubmissionReview,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationChecklist, RequiredEvidenceDecisionPassOrCorrectivePlan},
			RequiredTools:        nil,
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationChecklistCompleted, ReadinessDecisionPassOrCorrectivePlanOrBlocker},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUsePhaseEvidenceFirst, SearchPolicyDoNotReSubmitAppliedPlanEdits, SearchPolicySubmitPlanOnlyForNewCorrectiveEdits, SearchPolicyCorrectiveSubmitPlanRequiresExecutableFinalEdits, SearchPolicyPassWhenOpenObligationsAreClosed},
			VerificationContract: VerificationNotRequiredReviewLane,
			StopConditions:       []StopConditionID{StopDecisionPassWithChecklist, StopSubmitPlanCorrectiveEditsOrBlocker, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticVerificationChecklistPresent, FinalCriticDecisionReported},
		}, true
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
		}, true
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
		}, true
	case string(autonomy.TaskFamilyImplementationRepair):
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyImplementationRepair,
			Contract:             ContractRepairSummary,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          workspace.ToolSurfaceProfileGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceFailedVerificationOutput, RequiredEvidenceChangedPathsOrNoopReasonRepair, RequiredEvidenceVerificationStatus},
			ReadinessGate:        []ReadinessGateID{ReadinessFailedOutputAddressed, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUseFailedOutputFirst, SearchPolicyPreferSmallestFollowupEdit, SearchPolicyAvoidScopeWidening},
			VerificationContract: VerificationTestsPassedFailedSkipped,
			StopConditions:       []StopConditionID{StopRepairVerified, StopRepairBlockedWithFailedOutput, StopBudgetExhaustedWithUncertainty},
		}, true
	case "implementation", "test_authoring", "config_edit", "script_edit", "gdscript_edit", "docs_edit", "prompt_edit_security", "failure_injection":
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
			route:                runtimeRouteSpec{InitialLane: "implement", DefaultReason: "direct_implementation", MutationCapable: true, VerificationOwner: RuntimeVerificationOwnerExecutor},
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          workspace.ToolSurfaceProfileGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPathsOrNoopReason, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{workspace.RequiredToolGitHistoryLookup, workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell},
			ReadinessGate:        []ReadinessGateID{ReadinessChangedPathsReported, ReadinessVerificationContractResolved},
			SearchPolicy:         []SearchPolicyID{SearchPolicyConsumeTargetPacketFirst, SearchPolicyTrustPreloadedCurrentStateAnchors, SearchPolicyHistoryRequiredBeforeWrites, SearchPolicyReadCommitDescriptionsForIntendedLogicBeforeWrite, SearchPolicyWriteOnceEditLocationIsSupported, SearchPolicyPreferSmallBoundedEdits, SearchPolicyVerifyChangedSurface},
			VerificationContract: verificationContract,
			StopConditions:       []StopConditionID{StopImplementationVerifiedOrFailureReported, StopBlockedByMissingPermissionOrInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticChangedPathsReported, FinalCriticVerificationStatusReported},
		}, true
	default:
		return RuntimeContractSpec{}, false
	}
}

func runtimeFallbackContractSpec() RuntimeContractSpec {
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

// WorkspaceToolSurfaceOverrideProfileForTaskFamily reports the runtime-contract
// tool profile that should override the interactive workspace surface. Write
// profiles intentionally return false so callers keep the workspace default.
func WorkspaceToolSurfaceOverrideProfileForTaskFamily(family string) (workspace.ToolSurfaceProfile, bool) {
	if strings.TrimSpace(family) == "" {
		return "", false
	}

	profile := RuntimeContractForTaskFamily(family).ToolProfile
	switch profile {
	case workspace.ToolSurfaceProfileConversationReadOnly,
		workspace.ToolSurfaceProfileReadOnlyDiscovery,
		workspace.ToolSurfaceProfileVerificationOnly,
		workspace.ToolSurfaceProfileLocateOnly,
		workspace.ToolSurfaceProfilePlanSubmissionPlanner,
		workspace.ToolSurfaceProfilePlanSubmissionReview:
		return profile, true
	default:
		return "", false
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
