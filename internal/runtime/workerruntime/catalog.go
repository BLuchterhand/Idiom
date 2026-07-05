package workerruntime

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
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
	TaskFamily           autonomy.TaskFamilyID
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
			ToolProfile:          CapabilityConversationReadOnly,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), workspace.ToolNameTask, workspace.ToolNameShell), readOnlyDiscoveryToolNames()...),
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
			ToolProfile:          CapabilityArtifactDistiller,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), workspace.ToolNameTask, workspace.ToolNameTaskLedger, workspace.ToolNameShell, workspace.ToolNameSubmitPlan), readOnlyDiscoveryToolNames()...),
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
			ToolProfile:          CapabilityConversationReadOnly,
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
			ToolProfile:          CapabilityVerificationOnly,
			ForbiddenTools:       append(append([]string(nil), workspace.WorkspaceWriteToolNames()...), workspace.ToolNameTask),
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
			ToolProfile:          CapabilityLocateOnly,
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
			ToolProfile:          CapabilityGuardedWrite,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceTargetLocation, requiredEvidenceGitHistoryRationale, RequiredEvidenceChangedPaths, RequiredEvidenceVerificationStatus},
			RequiredTools:        []string{requiredToolGitHistoryLookup, "edit_file_or_apply_patch", "source_sanity_or_verification_shell"},
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
			ToolProfile:          CapabilityReadOnlyDiscovery,
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
			ToolProfile:          CapabilityReadOnlyDiscovery,
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
			ToolProfile:          CapabilityConversationReadOnly,
			ForbiddenTools:       append(append(workspace.WorkspaceWriteToolNames(), workspace.ToolNameTask, workspace.ToolNameShell, workspace.ToolNameSubmitPlan), readOnlyDiscoveryToolNames()...),
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
			ToolProfile:          CapabilityPlanSubmissionPlanner,
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
		// manifest when SetPlanThenExecutePlannerMode is active.
		// Verification is planner-owned (the executor phase
		// dispatches edits deterministically without re-running
		// tests), so the contract reflects "plan submitted, no per-
		// edit verification needed in this phase."
		return RuntimeContractSpec{
			TaskFamily:           autonomy.TaskFamilyPlanThenExecutePlanner,
			Contract:             ContractImplementationResult,
			PermissionProfile:    PermissionGuardedWorkspaceWrite,
			ToolProfile:          CapabilityPlanSubmissionPlanner,
			RequiredEvidence:     []EvidenceRequirementID{requiredEvidenceGitHistoryRationale, RequiredEvidencePlanSubmittedOrSourceSatisfied},
			RequiredTools:        []string{requiredToolGitHistoryLookup, workspace.ToolNameSubmitPlan},
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
			ToolProfile:          CapabilityPlanSubmissionReview,
			RequiredEvidence:     []EvidenceRequirementID{RequiredEvidenceVerificationChecklist, RequiredEvidenceDecisionPassOrCorrectivePlan},
			RequiredTools:        nil,
			ReadinessGate:        []ReadinessGateID{ReadinessVerificationChecklistCompleted, ReadinessDecisionPassOrCorrectivePlanOrBlocker},
			SearchPolicy:         []SearchPolicyID{SearchPolicyUsePhaseEvidenceFirst, SearchPolicyDoNotReSubmitAppliedPlanEdits, SearchPolicySubmitPlanOnlyForNewCorrectiveEdits, SearchPolicyCorrectiveSubmitPlanRequiresExecutableFinalEdits, SearchPolicyPassWhenOpenObligationsAreClosed},
			VerificationContract: VerificationNotRequiredReviewLane,
			StopConditions:       []StopConditionID{StopDecisionPassWithChecklist, StopSubmitPlanCorrectiveEditsOrBlocker, StopBlockedByMissingInformation},
			FinalCritic:          []FinalCriticID{FinalCriticAnswersActualUserRequest, FinalCriticVerificationChecklistPresent, FinalCriticDecisionReported},
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
			ToolProfile:          CapabilityConversationReadOnly,
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
		AllowedTools:         AllowedToolsForCapabilityProfile(allowedWorkspaceTools, spec.ToolProfile),
		ForbiddenTools:       append([]string(nil), spec.ForbiddenTools...),
		RequiredEvidence:     EvidenceRequirementStrings(spec.RequiredEvidence),
		RequiredTools:        append([]string(nil), spec.RequiredTools...),
		ReadinessGate:        ReadinessGateStrings(spec.ReadinessGate),
		SearchPolicy:         searchPolicyStrings(spec.SearchPolicy),
		VerificationContract: string(spec.VerificationContract),
		StopConditions:       stopConditionStrings(spec.StopConditions),
		FinalCritic:          finalCriticStrings(spec.FinalCritic),
	}
	if spec.ToolProfile == CapabilityPlanSubmissionPlanner || spec.ToolProfile == CapabilityPlanSubmissionReview {
		// The app often builds the brief before the workspace manager flips
		// into plan-execute mode, so the pre-mode workspace snapshot can lack
		// submit_plan even though the runner will enable it before execution.
		// Keep the contract-required tool in the brief allow-list so later
		// live-surface filtering does not remove the only valid planner write.
		brief.AllowedTools = mergeStrings(brief.AllowedTools, []string{workspace.ToolNameSubmitPlan})
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
		return filterAllowedTools(available, profile, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameReadFile)
	case CapabilityVerificationOnly:
		return filterAllowedTools(available, profile, workspace.ToolNameSourceSanity, workspace.ToolNameShell, workspace.ToolNameReadFile, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent)
	case CapabilityLocateOnly:
		return filterAllowedTools(available, profile, workspace.ToolNamePwd, workspace.ToolNameSearchFiles, workspace.ToolNameSearchContent, workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameContextQuery, workspace.ToolNameContextZoom)
	case CapabilityGuardedWrite, CapabilityIsolatedWrite:
		return filterAllowedTools(available, profile, guardedWriteToolNames()...)
	case CapabilityPlanSubmissionPlanner:
		return filterAllowedTools(available, profile, autonomy.PlanExecutePlannerToolNames()...)
	case CapabilityPlanSubmissionReview:
		return filterAllowedTools(available, profile, planSubmissionReviewToolNames()...)
	case CapabilityReadOnlyDiscovery:
		return filterAllowedTools(available, profile, readOnlyDiscoveryToolNames()...)
	case CapabilityConversationReadOnly:
		return filterAllowedTools(available, profile, workspace.RuntimeReadOnlyInvestigationToolNames()...)
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
	names := mergeStrings(workspace.RuntimeSourceInspectionToolNames(), []string{workspace.ToolNameReadOnlyCapability, workspace.ToolNameHarnessAudit})

	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())

	return mergeStrings(names, []string{workspace.ToolNameTask, "note"})
}

func planSubmissionReviewToolNames() []string {
	names := mergeStrings([]string{workspace.ToolNameReadFile, workspace.ToolNameFileStats, workspace.ToolNameCodeNavigate, workspace.ToolNameSourceSanity}, []string{workspace.ToolNameHarnessAudit, workspace.ToolNameHarnessFeedback})

	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())

	return mergeStrings(names, []string{workspace.ToolNameSubmitPlan})
}

func guardedWriteToolNames() []string {
	names := []string{workspace.ToolNameTaskLedger, workspace.ToolNameTask, workspace.ToolNamePwd}

	names = mergeStrings(names, workspace.RuntimeSourceInspectionToolNames())
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, []string{workspace.ToolNameGitStatus, workspace.ToolNameGitDiff})
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())
	names = mergeStrings(names, []string{workspace.ToolNameShell})
	names = mergeStrings(names, workspace.WorkspaceWriteToolNames())

	return mergeStrings(names, []string{workspace.ToolNameSubmitPlan})
}

func implementationScopedEditToolNames() []string {
	names := append([]string{}, workspace.WorkspaceWriteToolNames()...)

	names = mergeStrings(names, []string{workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameSubmitPlan})
	names = mergeStrings(names, workspace.RuntimeContextDiscoveryToolNames())
	names = mergeStrings(names, []string{workspace.ToolNameDependencyGraph, workspace.ToolNameCtagsSymbols})
	names = mergeStrings(names, workspace.RuntimeHistoryInspectionToolNames())
	names = mergeStrings(names, []string{workspace.ToolNameSourceSanity})

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
