package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

type runnerRunLifecycleState struct {
	planExecute    runnerPlanExecuteLifecycleState
	closure        runnerClosureLifecycleState
	recovery       runnerRecoveryLifecycleState
	implementation runnerImplementationLifecycleState
	progress       runnerProgressLifecycleState
}

type runnerPlanExecuteLifecycleState struct {
	readinessNudged                     bool
	candidateReadinessNonSubmitTools    int
	correctionAfterRefreshNudged        bool
	unreadSourceNudgeKey                string
	nativeSubmitPlanNoCallFallbacks     int
	loopBudgetRecoveryUsed              bool
	reviewRepairTerminalRequiredRejects int
}

type runnerClosureLifecycleState struct {
	delegatedAnswerReadyNudged     bool
	delegatedPostReadyFollowups    int
	delegatedDiminishingNudgeStage int
	postChildFollowupReads         int
	postChildDiminishingNudgeStage int
	conversationEvidenceNudged     bool
	conversationFirstMilestone     int
	conversationNudgeInterval      int
}

type runnerRecoveryLifecycleState struct {
	rejectionNudgeUsed bool
}

type runnerImplementationLifecycleState struct {
	// postCompletionVerificationCalls bounds non-write verification after all
	// required edit paths have been written.
	postCompletionVerificationCalls int
}

type runnerProgressLifecycleState struct {
	replayedToolRequests   map[string]int
	rejectedActions        map[string]int
	consecutiveRejected    int
	transportRetries       int
	modelCapacityRetryUsed bool
	consecutiveNoProgress  int
	consecutiveNarrowCalls int
	lastNarrowFamily       string
	narrowFamilyNudgeFired bool
}

type completionGateViolationClass string

const (
	completionGateViolationContract        completionGateViolationClass = "completion_contract"
	completionGateViolationSourceGrounding completionGateViolationClass = "source_grounding"
	completionGateViolationSourceEvidence  completionGateViolationClass = "source_evidence"
	completionGateViolationMutation        completionGateViolationClass = "mutation"
	completionGateViolationVerification    completionGateViolationClass = "verification"
	completionGateViolationPlanSubmission  completionGateViolationClass = "plan_submission"
	completionGateViolationTaskLedger      completionGateViolationClass = "task_ledger"
)

type completionGateActiveViolation struct {
	InvariantID                  string
	ViolationID                  string
	Class                        completionGateViolationClass
	SourceGroundingKind          sourceGroundingRejectionKind
	PendingSourceBodyPaths       []string
	PendingSourceBodyReferences  []sourceReference
	PendingContractPaths         []string
	PendingObligationIDs         []string
	AcceptedVerificationCommands []string
	AttemptsSinceProgress        int
}

type completionGateRecoveryState struct {
	// TotalAttempts is observational: it records correction history for metrics
	// and audit, but does not cap contract recovery.
	TotalAttempts   int
	ActiveViolation completionGateActiveViolation
}

type completionGateRecoveryProgress struct {
	SatisfiedSourceBodyPaths []string
	PendingSourceBodyPaths   []string
	SatisfiedSourceBodyRefs  []sourceReference
	PendingSourceBodyRefs    []sourceReference
	SatisfiedContractPaths   []string
	PendingContractPaths     []string
	SatisfiedObligationIDs   []string
	PendingObligationIDs     []string
	AdvancedViolationClass   completionGateViolationClass
}

func (recovery completionGateRecoveryState) hasActiveViolation() bool {
	return recovery.ActiveViolation.InvariantID != ""
}

func (recovery completionGateRecoveryState) CanRetryActive(retryLimit int) bool {
	return recovery.hasActiveViolation() &&
		recovery.ActiveViolation.AttemptsSinceProgress <= retryLimit
}

func (recovery completionGateRecoveryState) ShouldAttachPrompt(priorAttempts int, canRetry bool) bool {
	if !canRetry {
		return false
	}
	return recovery.hasActiveViolation() &&
		(recovery.ActiveViolation.InvariantID != completionGateInvariantGeneric || priorAttempts == 0)
}

func (progress completionGateRecoveryProgress) MadeProgress() bool {
	return len(progress.SatisfiedSourceBodyPaths) > 0 ||
		len(progress.SatisfiedSourceBodyRefs) > 0 ||
		len(progress.SatisfiedObligationIDs) > 0 ||
		progress.AdvancedViolationClass != ""
}

func (recovery completionGateRecoveryState) PriorAttemptsFor(result workspace.ToolResult) int {
	next, _ := completionGateViolationFromResult(result)
	if !recovery.ActiveViolation.sameViolation(next) ||
		completionGateObligationsStrictlyContracted(recovery.ActiveViolation, next) {

		return 0
	}
	return recovery.ActiveViolation.AttemptsSinceProgress
}

func (recovery *completionGateRecoveryState) RecordRejection(result workspace.ToolResult) sourceGroundingAssessment {
	if recovery == nil {
		return sourceGroundingAssessment{}
	}

	recovery.TotalAttempts++
	next, assessment := completionGateViolationFromResult(result)
	next.AttemptsSinceProgress = 1

	if recovery.ActiveViolation.sameViolation(next) {
		if completionGateObligationsStrictlyContracted(recovery.ActiveViolation, next) {
			next.AttemptsSinceProgress = 1
		} else {
			next.AttemptsSinceProgress = recovery.ActiveViolation.AttemptsSinceProgress + 1
		}
	}
	recovery.ActiveViolation = next

	return assessment
}

func completionGateObligationsStrictlyContracted(previous completionGateActiveViolation, next completionGateActiveViolation) bool {
	previousObligations := previous.pendingObligations()
	nextObligations := next.pendingObligations()
	if len(previousObligations) == 0 || len(nextObligations) >= len(previousObligations) {
		return false
	}

	previousSet := make(map[string]struct{}, len(previousObligations))
	for _, obligation := range previousObligations {
		previousSet[obligation] = struct{}{}
	}
	for _, obligation := range nextObligations {
		if _, ok := previousSet[obligation]; !ok {
			return false
		}
	}
	return true
}

func (violation completionGateActiveViolation) pendingObligations() []string {
	if len(violation.PendingObligationIDs) > 0 {
		return normalizeCompletionGateObligationIDs(violation.PendingObligationIDs)
	}
	if len(violation.PendingSourceBodyPaths) > 0 {
		if len(violation.PendingSourceBodyReferences) > 0 {
			return completionGateSourceReferenceObligationKeys(violation.PendingSourceBodyReferences)
		}
		return uniqueSortedRunnerPaths(violation.PendingSourceBodyPaths)
	}
	return uniqueSortedRunnerPaths(violation.PendingContractPaths)
}

func completionGateViolationFromResult(result workspace.ToolResult) (completionGateActiveViolation, sourceGroundingAssessment) {
	if envelope, ok := completionGateRecoveryEnvelopeFromResult(result); ok {
		violation := completionGateActiveViolation{
			InvariantID:                  envelope.InvariantID,
			ViolationID:                  envelope.ViolationID,
			Class:                        envelope.RecoveryKind,
			PendingContractPaths:         append([]string(nil), envelope.PendingPaths...),
			PendingObligationIDs:         append([]string(nil), envelope.PendingObligationIDs...),
			AcceptedVerificationCommands: append([]string(nil), envelope.AcceptedVerificationCommands...),
		}
		if violation.Class == "" {
			violation.Class = completionGateViolationContract
		}
		if violation.Class == completionGateViolationSourceEvidence {
			violation.PendingSourceBodyPaths = append([]string(nil), envelope.PendingPaths...)
			violation.PendingContractPaths = nil
		}

		if assessment, ok := sourceGroundingAssessmentFromResult(result); ok {
			violation.Class = completionGateViolationSourceGrounding
			violation.SourceGroundingKind = assessment.Kind
			violation.PendingSourceBodyReferences = filterSourceReferences(assessment.References, func(reference sourceReference) bool {
				return reference.BodyReadable()
			})
			violation.PendingSourceBodyPaths = sourceReferencePaths(violation.PendingSourceBodyReferences)
			violation.PendingContractPaths = nil
			return violation, assessment
		}
		return violation, sourceGroundingAssessment{}
	}

	// CompletionGate is an extension point and may still return an unclassified
	// result. Keep that result retryable, but do not infer authority from prose.
	return completionGateActiveViolation{
		InvariantID: completionGateInvariantGeneric,
		ViolationID: completionGateViolationGeneric,
		Class:       completionGateViolationContract,
	}, sourceGroundingAssessment{}
}

func completionGateSourceReferenceObligationKeys(references []sourceReference) []string {
	keys := make([]string, 0, len(references))
	for _, reference := range normalizeSourceReferences(references) {
		key := string(reference.Kind) + ":" + reference.Path
		if reference.LineRange != nil {
			key += fmt.Sprintf(":%d-%d", reference.LineRange.Start, reference.LineRange.End)
		}
		keys = append(keys, key)
	}
	return keys
}

func (violation completionGateActiveViolation) sameViolation(other completionGateActiveViolation) bool {
	if violation.InvariantID != other.InvariantID ||
		violation.ViolationID != other.ViolationID ||
		violation.Class != other.Class {

		return false
	}
	return violation.Class != completionGateViolationSourceGrounding ||
		violation.SourceGroundingKind == other.SourceGroundingKind
}

func (recovery *completionGateRecoveryState) ObserveEvidenceProgress(
	req workspace.ToolRequest,
	result workspace.ToolResult,
	sourceWindowCurrent func(toolfacts.SourceWindowEvidence) bool,
) completionGateRecoveryProgress {
	if recovery == nil {
		return completionGateRecoveryProgress{}
	}

	switch recovery.ActiveViolation.Class {
	case completionGateViolationMutation:
		return recovery.observeMutationProgress(req, result)
	case completionGateViolationVerification:
		if strings.TrimSpace(result.Error) != "" {
			return completionGateRecoveryProgress{}
		}
		return recovery.observeVerificationProgress(result)
	case completionGateViolationPlanSubmission:
		if strings.TrimSpace(result.Error) != "" {
			return completionGateRecoveryProgress{}
		}
		if strings.TrimSpace(result.Tool) == workspace.ToolNameSubmitPlan {
			return recovery.resolveActiveViolation(completionGateViolationPlanSubmission)
		}
		return completionGateRecoveryProgress{}
	case completionGateViolationTaskLedger:
		return completionGateRecoveryProgress{}
	case completionGateViolationSourceGrounding, completionGateViolationSourceEvidence:
		if strings.TrimSpace(result.Error) != "" {
			return completionGateRecoveryProgress{}
		}
		return recovery.observeSourceBodyEvidence(result, sourceWindowCurrent)
	default:
		return completionGateRecoveryProgress{}
	}
}

type completionGateVerificationEvidence struct {
	Attempted    bool
	Successful   bool
	Relevant     bool
	ClearsAll    bool
	CoveredPaths []string
}

func completionGateVerificationEvidenceForResult(result workspace.ToolResult, pendingPaths []string, acceptedCommands []string) completionGateVerificationEvidence {
	pendingPaths = uniqueSortedRunnerPaths(pendingPaths)
	observation := workspace.AnalyzePostWriteVerificationResult(result, pendingPaths)
	evidence := completionGateVerificationEvidence{
		Attempted:    observation.Attempted,
		Successful:   observation.Successful,
		Relevant:     observation.RelevantToPendingWrites,
		CoveredPaths: intersectRunnerPaths(pendingPaths, observation.CoveredPendingPaths),
	}

	if completionGateVerificationCommandAuthorized(result, acceptedCommands) {
		passed := strings.TrimSpace(result.Tool) == workspace.ToolNameShell && strings.TrimSpace(result.Error) == ""
		evidence.Attempted = true
		evidence.Successful = passed
		evidence.Relevant = true
		evidence.ClearsAll = passed
		if passed {
			evidence.CoveredPaths = append([]string(nil), pendingPaths...)
		}
		return evidence
	}

	if !evidence.Successful {
		return evidence
	}
	evidence.ClearsAll = len(completionGateRemainingPaths(pendingPaths, evidence.CoveredPaths)) == 0
	return evidence
}

func completionGateVerificationCommandAuthorized(result workspace.ToolResult, acceptedCommands []string) bool {
	if result.Facts == nil {
		return false
	}
	command := strings.TrimSpace(result.Facts.ShellCommand)
	if command == "" {
		return false
	}
	if !slices.Contains(normalizeCompletionGateVerificationCommands(acceptedCommands), command) {
		return false
	}
	rawWorkdir := strings.TrimSpace(result.Facts.ShellWorkdir)
	if rawWorkdir == "" {
		return false
	}
	workdir := filepath.ToSlash(filepath.Clean(rawWorkdir))
	return workdir == "."
}

func (recovery *completionGateRecoveryState) observeVerificationProgress(result workspace.ToolResult) completionGateRecoveryProgress {
	pending := recovery.ActiveViolation.PendingContractPaths
	evidence := completionGateVerificationEvidenceForResult(
		result,
		pending,
		recovery.ActiveViolation.AcceptedVerificationCommands,
	)
	if !evidence.Successful || len(evidence.CoveredPaths) == 0 {
		return completionGateRecoveryProgress{}
	}

	remaining := completionGateRemainingPaths(pending, evidence.CoveredPaths)
	progress := completionGateRecoveryProgress{
		SatisfiedContractPaths: evidence.CoveredPaths,
		PendingContractPaths:   remaining,
		AdvancedViolationClass: completionGateViolationVerification,
	}
	if evidence.ClearsAll || len(remaining) == 0 {
		recovery.ActiveViolation = completionGateActiveViolation{}
		progress.PendingContractPaths = nil
		return progress
	}

	recovery.ActiveViolation.PendingContractPaths = remaining
	recovery.ActiveViolation.AttemptsSinceProgress = 0
	return progress
}

func (recovery *completionGateRecoveryState) observeMutationProgress(req workspace.ToolRequest, result workspace.ToolResult) completionGateRecoveryProgress {
	if len(recovery.ActiveViolation.PendingObligationIDs) > 0 {
		return completionGateRecoveryProgress{}
	}
	if !workspaceWriteResultCommitted(req, result) {
		return completionGateRecoveryProgress{}
	}
	return recovery.observeMutationProgressIDs(committedMutationEffectIDsForResult(result))
}

func (recovery *completionGateRecoveryState) observeMutationProgressIDs(changed []string) completionGateRecoveryProgress {
	if recovery == nil || len(recovery.ActiveViolation.PendingObligationIDs) > 0 {
		return completionGateRecoveryProgress{}
	}
	if len(changed) == 0 {
		return completionGateRecoveryProgress{}
	}
	pending := recovery.ActiveViolation.PendingContractPaths
	if len(pending) == 0 {
		return recovery.resolveActiveViolation(completionGateViolationMutation)
	}

	satisfied := intersectRunnerPaths(pending, changed)
	if len(satisfied) == 0 {
		return completionGateRecoveryProgress{}
	}
	remaining := completionGateRemainingPaths(pending, satisfied)
	progress := completionGateRecoveryProgress{
		SatisfiedContractPaths: satisfied,
		PendingContractPaths:   remaining,
		AdvancedViolationClass: completionGateViolationMutation,
	}
	if len(remaining) == 0 {
		recovery.ActiveViolation = completionGateActiveViolation{}
	} else {
		recovery.ActiveViolation.PendingContractPaths = remaining
		recovery.ActiveViolation.AttemptsSinceProgress = 0
	}
	return progress
}

func (recovery *completionGateRecoveryState) observeSemanticObligationProgress(currentPending []string) completionGateRecoveryProgress {
	if recovery == nil || len(recovery.ActiveViolation.PendingObligationIDs) == 0 {
		return completionGateRecoveryProgress{}
	}

	active := normalizeCompletionGateObligationIDs(recovery.ActiveViolation.PendingObligationIDs)
	currentSet := make(map[string]struct{}, len(currentPending))
	for _, id := range normalizeCompletionGateObligationIDs(currentPending) {
		currentSet[id] = struct{}{}
	}

	progress := completionGateRecoveryProgress{AdvancedViolationClass: recovery.ActiveViolation.Class}
	for _, id := range active {
		if _, pending := currentSet[id]; pending {
			progress.PendingObligationIDs = append(progress.PendingObligationIDs, id)
			continue
		}
		progress.SatisfiedObligationIDs = append(progress.SatisfiedObligationIDs, id)
	}
	if len(progress.SatisfiedObligationIDs) == 0 {
		return completionGateRecoveryProgress{}
	}

	if len(progress.PendingObligationIDs) == 0 {
		recovery.ActiveViolation = completionGateActiveViolation{}
		return progress
	}

	recovery.ActiveViolation.PendingObligationIDs = append([]string(nil), progress.PendingObligationIDs...)
	recovery.ActiveViolation.AttemptsSinceProgress = 0
	return progress
}

func completionGateRemainingPaths(pending []string, satisfied []string) []string {
	satisfiedSet := make(map[string]struct{}, len(satisfied))
	for _, path := range satisfied {
		satisfiedSet[NormalizeRunnerPath(path)] = struct{}{}
	}

	remaining := make([]string, 0, len(pending))
	for _, path := range pending {
		if _, ok := satisfiedSet[NormalizeRunnerPath(path)]; !ok {
			remaining = append(remaining, path)
		}
	}
	return remaining
}

func (recovery *completionGateRecoveryState) resolveActiveViolation(class completionGateViolationClass) completionGateRecoveryProgress {
	recovery.ActiveViolation = completionGateActiveViolation{}
	return completionGateRecoveryProgress{AdvancedViolationClass: class}
}

func (recovery *completionGateRecoveryState) observeSourceBodyEvidence(
	result workspace.ToolResult,
	sourceWindowCurrent func(toolfacts.SourceWindowEvidence) bool,
) completionGateRecoveryProgress {
	if len(recovery.ActiveViolation.PendingSourceBodyPaths) == 0 {
		return completionGateRecoveryProgress{}
	}
	activeClass := recovery.ActiveViolation.Class

	if len(recovery.ActiveViolation.PendingSourceBodyReferences) > 0 {
		progress := completionGateRecoveryProgress{}
		for _, reference := range recovery.ActiveViolation.PendingSourceBodyReferences {
			if completionGateSourceBodyReferenceSatisfied(result, reference, sourceWindowCurrent) {
				progress.SatisfiedSourceBodyRefs = append(progress.SatisfiedSourceBodyRefs, reference)
				continue
			}
			progress.PendingSourceBodyRefs = append(progress.PendingSourceBodyRefs, reference)
		}
		if !progress.MadeProgress() {
			return completionGateRecoveryProgress{}
		}
		progress.AdvancedViolationClass = activeClass
		progress.SatisfiedSourceBodyPaths = sourceReferencePaths(progress.SatisfiedSourceBodyRefs)
		progress.PendingSourceBodyPaths = sourceReferencePaths(progress.PendingSourceBodyRefs)

		recovery.ActiveViolation.PendingSourceBodyReferences = normalizeSourceReferences(progress.PendingSourceBodyRefs)
		recovery.ActiveViolation.PendingSourceBodyPaths = append([]string(nil), progress.PendingSourceBodyPaths...)
		recovery.ActiveViolation.AttemptsSinceProgress = 0
		if len(progress.PendingSourceBodyRefs) == 0 {
			recovery.ActiveViolation = completionGateActiveViolation{}
		}
		return progress
	}

	progress := completionGateRecoveryProgress{}
	for _, path := range recovery.ActiveViolation.PendingSourceBodyPaths {
		if completionGateSourceBodyPathSatisfied(result, path, sourceWindowCurrent) {
			progress.SatisfiedSourceBodyPaths = append(progress.SatisfiedSourceBodyPaths, path)
			continue
		}
		progress.PendingSourceBodyPaths = append(progress.PendingSourceBodyPaths, path)
	}
	if !progress.MadeProgress() {
		return completionGateRecoveryProgress{}
	}
	progress.AdvancedViolationClass = activeClass

	recovery.ActiveViolation.PendingSourceBodyPaths = append([]string(nil), progress.PendingSourceBodyPaths...)
	recovery.ActiveViolation.AttemptsSinceProgress = 0
	if len(progress.PendingSourceBodyPaths) == 0 {
		recovery.ActiveViolation = completionGateActiveViolation{}
	}

	return progress
}

func completionGateSourceBodyReferenceSatisfied(
	result workspace.ToolResult,
	reference sourceReference,
	sourceWindowCurrent func(toolfacts.SourceWindowEvidence) bool,
) bool {
	reference.Path = NormalizeRunnerPath(reference.Path)
	if reference.Path == "" || (reference.LineRange != nil && !reference.LineRange.Valid()) {
		return false
	}

	record := newToolResultEvidenceRecord(result)
	for _, evidence := range record.SourceWindowFacts(reference.Path) {
		if reference.LineRange != nil &&
			(evidence.StartLine > reference.LineRange.Start || evidence.EndLine < reference.LineRange.End) {

			continue
		}
		if sourceWindowCurrent != nil && sourceWindowCurrent(evidence) {
			return true
		}
	}

	if reference.LineRange != nil || reference.Kind != sourceReferenceExternalDocument {
		return false
	}
	projection := newToolResultEvidenceProjection([]workspace.ToolResult{result})
	return len(projection.ExternalDocumentBodyMatches(reference.Path)) > 0
}

func completionGateSourceBodyPathSatisfied(
	result workspace.ToolResult,
	path string,
	sourceWindowCurrent func(toolfacts.SourceWindowEvidence) bool,
) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	if completionGateSourceBodyReferenceSatisfied(result, sourceReference{Path: path}, sourceWindowCurrent) {
		return true
	}

	projection := newToolResultEvidenceProjection([]workspace.ToolResult{result})
	return len(projection.ExternalDocumentBodyMatches(path)) > 0
}

const completionGateRecoveryMessageTag = "[idiom completion-gate recovery state]"

type runnerTurnState struct {
	lastToolKey                  string
	lastToolResult               *workspace.ToolResult
	currentFingerprint           string
	mutationAdmissionFingerprint string
	mutationAdmission            workspace.MutationAdmissionAssessment
	mutationAdmissionBound       bool
}

func (state *runnerTurnState) bindRequest(req workspace.ToolRequest) string {
	if state == nil {
		return ""
	}

	state.currentFingerprint = toolRequestFingerprint(req)
	return state.currentFingerprint
}

func toolRequestFingerprint(req workspace.ToolRequest) string {
	return req.Tool + "|" + normalizedToolRequestRaw(req)
}

// beginTurnRequest binds one model-selected request to one non-executing
// workspace admission assessment. Every later prerequisite, retained-intent,
// and audit decision for this request consumes that same canonical analysis.
// Workspace.Execute still performs its own fresh authorization assessment.
func (r *runExecution) beginTurnRequest(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}
	r.state.turn.mutationAdmissionFingerprint = ""
	r.state.turn.mutationAdmission = workspace.MutationAdmissionAssessment{}
	r.state.turn.mutationAdmissionBound = false

	return r.ensureTurnRequest(req)
}

func (r *runExecution) ensureTurnRequest(req workspace.ToolRequest) string {
	if r == nil {
		return ""
	}
	fingerprint := r.state.turn.bindRequest(req)
	if r.state.turn.mutationAdmissionBound && r.state.turn.mutationAdmissionFingerprint == fingerprint {
		return fingerprint
	}

	r.state.turn.mutationAdmissionFingerprint = ""
	r.state.turn.mutationAdmission = workspace.MutationAdmissionAssessment{}
	r.state.turn.mutationAdmissionBound = false
	if r.runner == nil || r.runner.Workspace == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return fingerprint
	}

	r.state.turn.mutationAdmission = r.runner.Workspace.AssessMutationAdmission(r.state.ctx, req)
	r.state.turn.mutationAdmissionFingerprint = fingerprint
	r.state.turn.mutationAdmissionBound = true
	return fingerprint
}

func (r *runExecution) mutationAdmissionForRequest(req workspace.ToolRequest) (workspace.MutationAdmissionAssessment, bool) {
	if r == nil || !workspace.IsWorkspaceWriteTool(req.Tool) {
		return workspace.MutationAdmissionAssessment{}, false
	}
	fingerprint := r.ensureTurnRequest(req)
	if !r.state.turn.mutationAdmissionBound || r.state.turn.mutationAdmissionFingerprint != fingerprint {
		return workspace.MutationAdmissionAssessment{}, false
	}
	return r.state.turn.mutationAdmission, true
}

// runnerRunState owns mutable state shared by one run invocation, its turn
// controller, and extracted phase helpers. Helpers mutate this one value
// directly instead of synchronizing parallel loop-local copies.
type runnerRunState struct {
	ctx                 context.Context
	sourceWindowCurrent func(toolfacts.SourceWindowEvidence) bool
	policy              RunPolicy
	metrics             runnerMetrics
	runtimeBrief        workerruntime.WorkerRuntimeBrief
	mutation            runnerMutationState
	initializationErr   error

	opts                 llama.ChatOptions
	collectedResults     []workspace.ToolResult
	turnMessages         []llama.ChatMessage
	lastAssistantMessage llama.ChatMessage
	turn                 runnerTurnState

	budgetExhausted bool
	stopReason      string

	currentAllowedTools                  []string
	currentTransport                     PlanExecutePlanningTransport
	toolSurface                          ActiveSurface
	requiredPathCompletionRecoveryActive bool

	executionState               runnerExecutionState
	delegatedReadOnlyAnswerReady bool
	postChildPacketActive        bool
	directAnswerSurfaceEmitted   bool

	planThenExecuteNativeSubmitPlanDisabled bool

	autonomyRecorder                     *runnerAutonomyRecorder
	lastCompletionGateSourceGrounding    sourceGroundingAssessment
	completionGateRecovery               completionGateRecoveryState
	completionStatusEmitted              bool
	completionStatusLastStatus           string
	completionStatusLastBlockerKind      string
	implementationTargetWriteReadyNudges int
	acceptedAnswerSignalsEmitted         bool
	lifecycle                            runnerRunLifecycleState
}

func (r *runExecution) tryApplyPlanExecuteLoopBudgetRecovery(req workspace.ToolRequest, reason string) bool {
	if r == nil {
		return false
	}
	state := &r.state

	nativeSubmitPlan := chatOptionsExposeTool(state.opts, workspace.ToolNameSubmitPlan)
	var recovery planThenExecuteLoopBudgetRecovery
	var ok bool
	if state.lifecycle.planExecute.loopBudgetRecoveryUsed {
		recovery, ok = r.planThenExecuteIgnoredLoopBudgetRecoveryPrompt(req, state.collectedResults, reason, nativeSubmitPlan)
	} else {
		recovery, ok = r.planThenExecuteLoopBudgetRecoveryPrompt(req, state.collectedResults, reason, nativeSubmitPlan)
	}
	if !ok {
		return false
	}

	state.lifecycle.planExecute.loopBudgetRecoveryUsed = true
	state.opts = forceVisibleActionChatOptions(state.opts)
	state.syncCurrentAllowedToolsFromOptions()
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: recovery.Prompt})
	r.emitEvent(RunEvent{Role: "plan_then_execute_loop_budget_terminal_recovery", Content: recovery.Prompt})

	return true
}

// syncCurrentAllowedToolsFromOptions rebuilds currentAllowedTools from opts.Tools.
func (state *runnerRunState) syncCurrentAllowedToolsFromOptions() {
	if state == nil {
		return
	}
	state.currentAllowedTools = state.currentAllowedTools[:0]
	for _, tool := range state.opts.Tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name != "" {
			state.currentAllowedTools = append(state.currentAllowedTools, name)
		}
	}

}

func (state *runnerRunState) planningTransport() PlanExecutePlanningTransport {
	if state != nil && (state.currentTransport.SupportsNativeTools ||
		state.currentTransport.ForceNativeTools ||
		state.currentTransport.TextualToolParser ||
		state.currentTransport.ResponseFormatJSON ||
		state.currentTransport.TerminalOnly) {

		return state.currentTransport
	}
	return PlanExecutePlanningTransport{TextualToolParser: true}
}

func (state *runnerRunState) recordCompletionGateRejection(result workspace.ToolResult) {
	if state == nil {
		return
	}

	assessment := state.completionGateRecovery.RecordRejection(result)
	state.lastCompletionGateSourceGrounding = assessment
	state.clearCompletionGateRecoveryMessage()
}

func (state *runnerRunState) observeCompletionGateEvidenceProgress(req workspace.ToolRequest, result workspace.ToolResult) completionGateRecoveryProgress {
	if state == nil {
		return completionGateRecoveryProgress{}
	}

	progress := state.completionGateRecovery.ObserveEvidenceProgress(req, result, state.sourceWindowCurrent)
	if !progress.MadeProgress() {
		progress = state.observePathlessSourceEvidenceProgress(result)
	}
	if !progress.MadeProgress() {
		return progress
	}

	if len(progress.PendingSourceBodyPaths) == 0 {
		state.lastCompletionGateSourceGrounding = sourceGroundingAssessment{}
	} else {
		state.lastCompletionGateSourceGrounding = sourceGroundingAssessmentWithPendingBodyPaths(
			state.lastCompletionGateSourceGrounding,
			progress.PendingSourceBodyPaths,
		)
	}
	state.clearCompletionGateRecoveryMessage()

	return progress
}

func (state *runnerRunState) observeCompletionGateSemanticProgress(currentPending []string) completionGateRecoveryProgress {
	if state == nil {
		return completionGateRecoveryProgress{}
	}

	progress := state.completionGateRecovery.observeSemanticObligationProgress(currentPending)
	if !progress.MadeProgress() {
		return progress
	}

	state.clearCompletionGateRecoveryMessage()
	return progress
}

func (state *runnerRunState) observePathlessSourceEvidenceProgress(result workspace.ToolResult) completionGateRecoveryProgress {
	active := state.completionGateRecovery.ActiveViolation
	if active.Class != completionGateViolationSourceGrounding ||
		!slices.Equal(
			normalizeCompletionGateObligationIDs(active.PendingObligationIDs),
			[]string{completionGateObligationReadOnlyAnswerability},
		) ||
		strings.TrimSpace(result.Error) != "" {

		return completionGateRecoveryProgress{}
	}

	// runnerTurnRecorder appends the successful result before recovery observes
	// it. Comparing the ledger prefix with the full ledger makes readiness
	// advancement attributable to this result rather than to any earlier read.
	results := state.collectedResults
	if len(results) == 0 {
		return completionGateRecoveryProgress{}
	}
	priorResults := results[:len(results)-1]
	if genericReadOnlyEvidenceReady(state.runtimeBrief, priorResults) ||
		!genericReadOnlyEvidenceReady(state.runtimeBrief, results) {

		return completionGateRecoveryProgress{}
	}

	state.completionGateRecovery.ActiveViolation = completionGateActiveViolation{}
	return completionGateRecoveryProgress{
		SatisfiedObligationIDs: []string{completionGateObligationReadOnlyAnswerability},
		AdvancedViolationClass: completionGateViolationSourceGrounding,
	}
}

func sourceGroundingAssessmentWithPendingBodyPaths(assessment sourceGroundingAssessment, pending []string) sourceGroundingAssessment {
	pendingSet := make(map[string]struct{}, len(pending))
	for _, path := range pending {
		if path = NormalizeRunnerPath(path); path != "" {
			pendingSet[path] = struct{}{}
		}
	}

	references := filterSourceReferences(assessment.References, func(reference sourceReference) bool {
		if !reference.BodyReadable() {
			return true
		}
		_, ok := pendingSet[NormalizeRunnerPath(reference.Path)]
		return ok
	})

	return newSourceGroundingAssessment(assessment.Kind, references, assessment.Text)
}

func (state *runnerRunState) setCompletionGateRecoveryMessage(prompt string) string {
	if state == nil {
		return ""
	}

	state.clearCompletionGateRecoveryMessage()
	if !state.completionGateRecovery.hasActiveViolation() {
		return ""
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}

	message := completionGateRecoveryMessageTag + "\n" + prompt
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: message})
	return message
}

func (state *runnerRunState) clearCompletionGateRecoveryMessage() {
	if state == nil || len(state.turnMessages) == 0 {
		return
	}

	messages := state.turnMessages[:0]
	for _, message := range state.turnMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") &&
			strings.HasPrefix(strings.TrimSpace(message.Content), completionGateRecoveryMessageTag) {

			continue
		}
		messages = append(messages, message)
	}
	state.turnMessages = messages
}

// terminalSubmitPlanNativeTools reports whether native submit_plan is enabled.

func (r *runExecution) terminalSubmitPlanNativeTools() bool {
	state := &r.state
	return !state.planThenExecuteNativeSubmitPlanDisabled && r.supportsTerminalSubmitPlanNativeTools(state.collectedResults)
}

// syncPlanThenExecuteRecoveryAllowedTools records the callable surface already
// projected into the current chat options.
func (r *runExecution) syncPlanThenExecuteRecoveryAllowedTools() {
	state := &r.state
	state.syncCurrentAllowedToolsFromOptions()
}

// rebuildRunTools is the central tool-rebuild function. It projects the
// workspace-authorized surface through the active lane and permission profile,
// then writes state.opts.Tools and synchronizes request admission state.
func (r *runExecution) rebuildRunTools() {
	state := &r.state
	if r.runner.Workspace == nil {
		return
	}

	workspaceSurface := r.workspaceToolSurfaceSnapshot()
	allowed := r.workspaceAdvertisedAllowedTools()

	if workerruntime.PermissionProfileIsReadOnlyVerificationShell(r.state.runtimeBrief.PermissionProfile) && !state.executionState.verificationAttempted {
		filtered := make([]string, 0, 1)

		for _, t := range allowed {
			if t == workspace.ToolNameShell {
				filtered = append(filtered, t)
			}
		}

		allowed = filtered
	}

	if runnerBriefAllowsExternalToolAppend(r.state.runtimeBrief) {
		if externalNames := workspaceSurface.AvailableExternalToolNames(); len(externalNames) > 0 {
			seen := make(map[string]struct{}, len(allowed))
			for _, name := range allowed {
				seen[name] = struct{}{}
			}

			for _, name := range externalNames {
				if _, ok := seen[name]; !ok {
					allowed = append(allowed, name)
					seen[name] = struct{}{}
				}
			}
		}
	}

	if surface, ok := DirectAnswerSurfaceForBrief(r.state.runtimeBrief); ok {
		allowed = append([]string(nil), surface.ToolNames...)

		if !state.directAnswerSurfaceEmitted {
			EmitDirectAnswerSurfaceEvent(r.emitEvent, surface)

			state.directAnswerSurfaceEmitted = true
		}
	}

	if !activeSurfaceProvided(state.toolSurface) {
		state.toolSurface = NewActiveSurface(allowed, nil)
	} else {
		state.toolSurface.SetVisibleTools(allowed)
	}

	var tools []llama.Tool

	if len(allowed) > 0 {
		tools = buildLlamaTools(workspaceSurface.BuildToolDefinitions(allowed))
		if !workerruntime.RuntimeContractIsDirectAnswer(r.state.runtimeBrief.Contract) &&
			lanePolicyShouldHaveNoteTool(r.lanePolicy(), r.state.runtimeBrief.Contract) {

			tools = append(tools, noteToolDefinition())
			if !slices.Contains(r.state.runtimeBrief.AllowedTools, noteToolName) {
				r.state.runtimeBrief.AllowedTools = append(r.state.runtimeBrief.AllowedTools, noteToolName)
			}
		}
	}

	state.opts.Tools = tools
	state.syncCurrentAllowedToolsFromOptions()

	if len(state.opts.Tools) == 0 {
		state.opts.ToolChoice = ""
	} else if strings.TrimSpace(state.opts.ToolChoice) == "" {
		state.opts.ToolChoice = "auto"
	}
}

type runnerPlanExecuteCorrectionAfterRefreshDecision struct {
	prompt        string
	surfaceReason string
	eventRole     string
}

func (r *runExecution) derivePlanThenExecuteCorrectionAfterRefreshDecision(result workspace.ToolResult) (runnerPlanExecuteCorrectionAfterRefreshDecision, bool) {
	if r == nil {
		return runnerPlanExecuteCorrectionAfterRefreshDecision{}, false
	}
	state := &r.state
	if state.lifecycle.planExecute.correctionAfterRefreshNudged ||
		!r.planThenExecutePlannerPhase() ||
		!state.lifecycle.planExecute.readinessNudged ||
		strings.TrimSpace(result.Tool) == workspace.ToolNameSubmitPlan ||
		successfulSubmitPlanResultCount(state.collectedResults) > 0 {

		return runnerPlanExecuteCorrectionAfterRefreshDecision{}, false
	}

	recoveryState := r.derivePlanExecutePlanningState(state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true})
	rejectedResult, _, rejected := lastRejectedSubmitPlanCoverageResult(state.collectedResults)
	if !rejected ||
		recoveryState.Mode == PlanExecutePlanningModeAnchorRefresh ||
		len(planThenExecuteUnreadRequiredPaths(r.state.runtimeBrief, state.collectedResults, r.planThenExecutePathAuthority())) > 0 {

		return runnerPlanExecuteCorrectionAfterRefreshDecision{}, false
	}

	prompt := "The bounded source refresh after the rejected `submit_plan` is complete. " + planThenExecuteSubmitCorrectionOutcomeInstruction(r.state.runtimeBrief, state.collectedResults) + " " + planThenExecuteSubmitPlanCoverageSentence() + " " + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.state.runtimeBrief, rejectedResult)
	if r.terminalSubmitPlanNativeTools() {
		prompt = planThenExecuteNativeSubmitPlanCorrectionPrompt(r.state.runtimeBrief, rejectedResult)
	}

	return runnerPlanExecuteCorrectionAfterRefreshDecision{
		prompt:        prompt,
		surfaceReason: "submit_plan_after_refresh",
		eventRole:     "plan_then_execute_submit_plan_after_refresh",
	}, true
}

func (r *runExecution) applyPlanThenExecuteCorrectionAfterRefreshDecision(decision runnerPlanExecuteCorrectionAfterRefreshDecision) {
	state := &r.state
	state.lifecycle.planExecute.correctionAfterRefreshNudged = true
	r.refreshPlanThenExecuteTerminalSurface(decision.surfaceReason)
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: decision.prompt})
	r.rebuildRunTools()
	state.opts = planThenExecutePostReadinessRecoveryChatOptionsForBackend(state.opts, r.state.runtimeBrief, state.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())
	r.syncPlanThenExecuteRecoveryAllowedTools()
	r.emitEvent(RunEvent{Role: decision.eventRole, Content: decision.prompt})
}

// refreshPlanThenExecuteTerminalSurface refreshes the terminal surface.
func (r *runExecution) refreshPlanThenExecuteTerminalSurface(reason string) {
	state := &r.state
	surface := derivePlanExecuteRecoverySurface(r.state.runtimeBrief, state.collectedResults, PlanExecutePlanningTransport{TextualToolParser: true}, r.planThenExecutePathAuthority())
	switch surface.Kind {
	case PlanExecuteRecoverySurfaceAnchorRefresh:
		r.refreshPlanThenExecuteAnchorRefreshSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSourceSatisfiedTerminal:
		r.refreshPlanThenExecuteTerminalOutcomeSurface(&state.turnMessages, state.collectedResults, reason)

		return
	case PlanExecuteRecoverySurfaceSubmitReady, PlanExecuteRecoverySurfaceSubmitCorrection:
	default:
		r.prunePlanThenExecuteTerminalSurfaceHistory(&state.turnMessages, reason)

		return
	}

	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// forcePlanThenExecuteTerminalSurface unconditionally forces the terminal surface.
func (r *runExecution) forcePlanThenExecuteTerminalSurface(reason string) {
	state := &r.state
	if state.planThenExecuteNativeSubmitPlanDisabled {
		r.refreshPlanThenExecuteTerminalTextualFallbackSurface(&state.turnMessages, state.collectedResults, reason)

		return
	}

	r.refreshPlanThenExecuteTerminalToolSurface(&state.turnMessages, state.collectedResults, reason)
}

// transitionPlanThenExecuteSubmitReadyTerminal applies the full submit-ready
// runtime surface. Prompt-only transitions are unsafe here: once the model is
// told to call submit_plan, currentAllowedTools must agree even if stale
// read-only native tools are still present in chat options.
func (r *runExecution) transitionPlanThenExecuteSubmitReadyTerminal(prompt string, eventRole string, surfaceKey string) {
	state := &r.state
	r.refreshPlanThenExecuteTerminalSurface(surfaceKey)
	terminalBrief := r.planThenExecuteTerminalToolBrief(state.collectedResults)
	state.opts.Tools = nil
	if len(terminalBrief.AllowedTools) > 0 {
		state.opts.Tools = buildLlamaTools(r.workspaceToolSurfaceSnapshot().BuildToolDefinitions(terminalBrief.AllowedTools))
	}
	if len(state.opts.Tools) > 0 && strings.TrimSpace(state.opts.ToolChoice) == "" {
		state.opts.ToolChoice = "auto"
	}
	state.opts = planThenExecuteReadyToSubmitChatOptionsForBackend(state.opts, r.state.runtimeBrief, state.collectedResults, r.terminalSubmitPlanNativeTools(), r.planThenExecutePathAuthority())
	state.turnMessages = append(state.turnMessages, llama.ChatMessage{Role: "system", Content: prompt})
	r.syncPlanThenExecuteRecoveryAllowedTools()
	r.emitEvent(RunEvent{Role: eventRole, Content: prompt})
}

// emitStateCompletionStatus records completion status and delegates to Runner method.
func (r *runExecution) emitStateCompletionStatus(payload runnerCompletionStatus) {
	state := &r.state
	state.completionStatusEmitted = true
	state.completionStatusLastStatus = strings.TrimSpace(payload.Status)
	state.completionStatusLastBlockerKind = strings.TrimSpace(payload.BlockerKind)
	r.emitCompletionStatus(payload)
}

func (r *runExecution) emitStateCompletionStatusFromParams(status, reason, blockerKind string, results []workspace.ToolResult, userVisibleBlocker bool) {
	state := &r.state
	changedPaths := r.completionChangedPaths(results)
	payload := runnerCompletionStatus{
		Lane:          r.agentName(),
		TaskFamily:    strings.TrimSpace(r.state.runtimeBrief.TaskFamily),
		Contract:      strings.TrimSpace(r.state.runtimeBrief.Contract),
		Status:        strings.TrimSpace(status),
		Reason:        strings.TrimSpace(reason),
		BlockerKind:   strings.TrimSpace(blockerKind),
		RequiresEdit:  runnerBriefRequiresMutation(r.state.runtimeBrief),
		EditAttempted: len(changedPaths) > 0,
		ImplementationState: recoverycontract.ImplementationState{
			ChangedPaths: changedPaths,
		},
		UserVisibleBlocker: userVisibleBlocker,
	}
	r.attachImplementationLifecycleStatus(&payload, results, "", state.implementationTargetWriteReadyNudges, state.currentAllowedTools)
	r.emitStateCompletionStatus(payload)
}

// emitTerminalAnswerSignalsOnce emits final-answer signals exactly once.
func (r *runExecution) emitTerminalAnswerSignalsOnce(decision terminalAnswerDecision) {
	state := &r.state
	if state.acceptedAnswerSignalsEmitted || strings.TrimSpace(decision.RawAnswer) == "" {
		return
	}

	switch decision.Outcome {
	case terminalAnswerAccepted:
		if state.autonomyRecorder != nil {
			reasonCode := "final_answer_accepted"
			if r.planThenExecutePlannerPhase() && plancontract.SourceSatisfiedAnswerIntent(decision.RawAnswer) {
				reasonCode = "plan_source_satisfied_short_circuit"
			}

			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyAllow, reasonCode, state.collectedResults, state.completionGateRecovery.TotalAttempts)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)
	case terminalAnswerBlocked, terminalAnswerTransportBlocked:
		blockerKind := textutil.FirstNonEmpty(strings.TrimSpace(decision.Acceptance.BlockerKind), strings.TrimSpace(decision.StopReason))
		reason := strings.TrimSpace(decision.Acceptance.Reason)
		if hostOwnedQuestionCloseoutStopReason(state.stopReason, decision.RawAnswer) &&
			runnerBriefRequiresMutation(r.state.runtimeBrief) &&
			len(r.completionChangedPaths(state.collectedResults)) == 0 {

			blockerKind = strings.TrimSpace(state.stopReason)
			reason = blockerKind
		}
		if state.autonomyRecorder != nil {
			state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, blockerKind, state.collectedResults, state.completionGateRecovery.TotalAttempts)
		}

		r.emitFinalAnswerTelemetry(decision.RawAnswer, state.collectedResults, decision.Acceptance)

		if terminalBlockedStatusShouldEmit(state.completionStatusEmitted, state.completionStatusLastStatus, state.completionStatusLastBlockerKind, blockerKind) {
			// Construct the runnerCompletionStatus once, shared by the legacy event emission below.
			changedPaths := r.completionChangedPaths(state.collectedResults)
			completionPayload := runnerCompletionStatus{
				Lane:          r.agentName(),
				TaskFamily:    strings.TrimSpace(r.state.runtimeBrief.TaskFamily),
				Contract:      strings.TrimSpace(r.state.runtimeBrief.Contract),
				Status:        "blocked",
				Reason:        reason,
				BlockerKind:   strings.TrimSpace(blockerKind),
				RequiresEdit:  runnerBriefRequiresMutation(r.state.runtimeBrief),
				EditAttempted: len(changedPaths) > 0,
				ImplementationState: recoverycontract.ImplementationState{
					ChangedPaths: changedPaths,
				},
				UserVisibleBlocker: true,
			}
			r.attachImplementationLifecycleStatus(&completionPayload, state.collectedResults, state.stopReason, state.implementationTargetWriteReadyNudges, state.currentAllowedTools)
			r.emitStateCompletionStatus(completionPayload)
		}
	}

	state.acceptedAnswerSignalsEmitted = true
}

// finish is the terminal answer path for Run().
func (r *runExecution) finish(value string, finishErr error, reason string, exhausted bool) (string, error) {
	state := &r.state
	if strings.TrimSpace(reason) != "" {
		state.stopReason = reason
	}

	state.budgetExhausted = exhausted
	if finishErr == nil {
		terminalInput := value
		decision := r.classifyTerminalAnswer(value, state.stopReason, state.collectedResults)
		presentedBeforeDecoration := decision.PresentedAnswer

		decision.PresentedAnswer = r.decorateTerminalAnswerDecision(decision, state.collectedResults)

		if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(presentedBeforeDecoration) {
			r.emitFinalAnswerDecorationEvent(decision.StopReason, presentedBeforeDecoration, decision.PresentedAnswer)

			if strings.TrimSpace(r.state.lastAssistantMessage.Role) != "" || strings.TrimSpace(r.state.lastAssistantMessage.Content) != "" {
				r.state.lastAssistantMessage.Content = decision.PresentedAnswer
			}
		}

		if strings.TrimSpace(decision.PresentedAnswer) != strings.TrimSpace(terminalInput) &&
			(strings.TrimSpace(r.state.lastAssistantMessage.Role) != "" || strings.TrimSpace(r.state.lastAssistantMessage.Content) != "") {

			r.state.lastAssistantMessage.Content = decision.PresentedAnswer
		}

		value = decision.PresentedAnswer
		state.stopReason = decision.StopReason

		if decision.RetryExhausted || (decision.Outcome == terminalAnswerAccepted && decision.StopReason == "read_only_terminal_recovery") {
			state.budgetExhausted = false
		}

		if decision.OverrideSource != "" {
			r.emitFinalAnswerBlockerOverride(decision.Acceptance, decision.StopReason, decision.OverrideSource)
		}

		acceptedTerminalAnswer := ""
		if decision.Outcome == terminalAnswerAccepted {
			acceptedTerminalAnswer = decision.RawAnswer
		}
		r.emitImplementationLifecycleState(state.collectedResults, state.stopReason, state.implementationTargetWriteReadyNudges, state.currentAllowedTools, acceptedTerminalAnswer)
		r.emitTerminalAnswerSignalsOnce(decision)
	} else {
		r.emitImplementationLifecycleState(state.collectedResults, state.stopReason, state.implementationTargetWriteReadyNudges, state.currentAllowedTools, "")
	}

	return value, finishErr
}

// retryRejectedFinalAnswer handles rejected final answer with retry logic.
func (r *runExecution) retryRejectedFinalAnswer(controller *runnerTurnController, stepNumber int, activeToolCall *llama.ToolCall, assistantMessage llama.ChatMessage, acceptance finalAnswerAcceptance, result workspace.ToolResult) bool {
	state := &r.state
	priorGateRejections := state.completionGateRecovery.PriorAttemptsFor(result)
	priorTotalRejections := state.completionGateRecovery.TotalAttempts
	if !r.retryableFinalAnswerAcceptanceRejection(acceptance, state.collectedResults, state.currentAllowedTools) {
		return false
	}

	result.Error = r.escalateCompletionGateResultMessage(result, priorGateRejections, state.currentAllowedTools)

	if state.autonomyRecorder != nil {
		state.autonomyRecorder.emitCompletionGateAttempt(autonomy.PolicyReject, runnerAutonomyReasonCode(result.Summary), state.collectedResults, priorTotalRejections)
	}

	controller.rejectCompletion(stepNumber, activeToolCall, assistantMessage, result)
	retryLimit := r.completionGateActiveRetryLimit(result, state.collectedResults, state.currentAllowedTools)
	if !state.completionGateRecovery.CanRetryActive(retryLimit) {
		return false
	}
	if prompt, ok := r.completionGateRecoveryPrompt(state.collectedResults); ok {
		if card := r.renderRecoveryCardForRejectedActionWithTransport(workspace.ToolRequest{Tool: completionGateToolName}, result, state.collectedResults, state.currentAllowedTools, state.planningTransport()); card != "" {
			prompt = appendRecoveryCardToMessage(prompt, card)
		}

		state.setCompletionGateRecoveryMessage(prompt)
		r.emitEvent(RunEvent{Role: "completion_gate_recovery", Content: prompt})
	}

	return true
}
