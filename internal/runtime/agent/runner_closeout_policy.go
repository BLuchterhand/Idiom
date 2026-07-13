package agent

import (
	"fmt"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/taskledger"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

func maxToolStepsExhaustedReason() string {
	return "Maximum tool steps for this turn have been reached. Tools are now disabled. Respond with a concise, direct, text-only final answer synthesized from the evidence already gathered. If one exact claim remains unsupported, state that uncertainty after the supported answer with the smallest useful next action. Do not leave the response blank."
}

// implementationCompletionState is the single completion projection for a
// mutation run. Verification, phase selection, recovery, readiness, closeout,
// and final acceptance must agree on these open obligations.
type implementationCompletionState struct {
	ChangedPaths                    []string
	MissingRequiredPaths            []string
	MissingRequiredSnippetIDs       []string
	MissingRequiredSnippetPaths     []string
	UnresolvedRejectedMutationPaths []string
	OpenTaskLedgerObligationIDs     []string
	BlockedTaskLedgerItems          []implementationBlockedTaskLedgerItem
	VerificationPending             bool
	VerificationFailed              bool
	SemanticVerificationPending     bool
	VerificationBlockerSubjects     []implementationBlockerSubject
	TerminalDisposition             implementationTerminalDisposition
	SourceSatisfiedPaths            []string
}

type implementationBlockerSubject struct {
	Namespace workspace.TaskLedgerBlockerSubjectNamespace
	ID        string
}

type implementationTerminalDisposition string

const (
	implementationTerminalDispositionNone             implementationTerminalDisposition = ""
	implementationTerminalDispositionMutationComplete implementationTerminalDisposition = "mutation_complete"
	implementationTerminalDispositionPlanSubmitted    implementationTerminalDisposition = "plan_submitted"
	implementationTerminalDispositionSourceSatisfied  implementationTerminalDisposition = "source_satisfied"
	implementationTerminalDispositionExactBlocker     implementationTerminalDisposition = "exact_blocker"
)

type implementationBlockedTaskLedgerItem struct {
	ID               string
	Kind             workspace.TaskLedgerBlockerKind
	SubjectNamespace workspace.TaskLedgerBlockerSubjectNamespace
	SubjectID        string
	Reason           string
	Paths            []string
}

// deriveImplementationProgressState projects obligations that are available
// from the immutable brief and typed tool-result ledger alone. Callers making
// terminal decisions must pass the enriched run-owned snapshot instead.
func deriveImplementationProgressState(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) implementationCompletionState {
	changed := committedMutationEffectSetForResults(results)
	_, verificationFailed := failedVerificationAfterLatestWrite(results, brief)

	state := implementationCompletionState{
		ChangedPaths:                    uniqueSortedRunnerPaths(mapKeys(changed)),
		MissingRequiredPaths:            missingRequiredEditPaths(brief.RequiredEditPaths, changed),
		UnresolvedRejectedMutationPaths: unresolvedRejectedMutationPaths(results, brief.RequiredEditPaths),
		VerificationPending:             hasPendingVerificationAfterWrites(results, brief),
		VerificationFailed:              verificationFailed,
		SemanticVerificationPending:     sourceEditsNeedSemanticVerification(brief, results),
	}
	if planThenExecutePlannerBrief(brief) && successfulSubmitPlanResultCount(results) > 0 {
		state.TerminalDisposition = implementationTerminalDispositionPlanSubmitted
		state.MissingRequiredPaths = nil
	}
	state.VerificationBlockerSubjects = implementationVerificationBlockerSubjects(state, results, brief.VerificationPlan)

	return state
}

func (r *runExecution) implementationCompletionState(results []workspace.ToolResult) implementationCompletionState {
	if r == nil {
		return implementationCompletionState{}
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	state := deriveImplementationProgressState(brief, results)
	state.ChangedPaths = uniqueSortedRunnerPaths(r.state.committedMutationEffectIDs())
	state.UnresolvedRejectedMutationPaths = uniqueSortedRunnerPaths(textutil.Merge(
		unresolvedRejectedMutationPaths(results, nil),
		r.state.unresolvedMutationTargetIDs(),
	))
	ledger := r.postWriteVerificationLedger(results)
	state.VerificationPending = ledger.Pending()
	state.VerificationFailed = ledger.HasFailure
	state.SemanticVerificationPending = r.sourceEditsNeedSemanticVerification(results)
	if r.state.mutation.bound && state.TerminalDisposition != implementationTerminalDispositionPlanSubmitted {
		state.MissingRequiredPaths = r.state.missingRequiredMutationCoverageIDs()
	}
	if state.TerminalDisposition == implementationTerminalDispositionPlanSubmitted {
		state.MissingRequiredSnippetIDs = nil
	} else if r.runner == nil || r.runner.Workspace == nil || strings.TrimSpace(r.runner.Workspace.Root()) == "" {
		state.MissingRequiredSnippetIDs = requiredEditSnippetNames(brief)
	} else {
		state.MissingRequiredSnippetIDs = r.missingRequiredEditSnippets(results)
	}

	state.OpenTaskLedgerObligationIDs = taskLedgerActionObligationIDs(r.taskLedger, r.runner.TaskLedger)
	state.BlockedTaskLedgerItems = taskLedgerBlockedItemProjection(r.taskLedger)
	state.MissingRequiredSnippetPaths = r.requiredEditSnippetBlockerCandidatePaths(brief, state.MissingRequiredSnippetIDs)
	state.VerificationBlockerSubjects = implementationVerificationBlockerSubjects(state, results, brief.VerificationPlan)

	return state.withDerivedTerminalDisposition()
}

func implementationVerificationBlockerSubjects(state implementationCompletionState, results []workspace.ToolResult, verificationPlan []string) []implementationBlockerSubject {
	var subjects []implementationBlockerSubject
	if state.SemanticVerificationPending {
		subjects = append(subjects, implementationBlockerSubject{
			Namespace: workspace.TaskLedgerBlockerSubjectTool,
			ID:        workspace.ToolNameSourceSanity,
		})
	}
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if !workspace.AnalyzeVerificationResult(result).Attempted || strings.TrimSpace(result.Error) == "" {
			continue
		}
		if strings.TrimSpace(result.Tool) == workspace.ToolNameSourceSanity {
			subjects = append(subjects, implementationBlockerSubject{
				Namespace: workspace.TaskLedgerBlockerSubjectTool,
				ID:        workspace.ToolNameSourceSanity,
			})
		}
		if subject, ok := implementationVerificationCommandBlockerSubject(workspace.AnalyzeVerificationResult(result).Command); ok {
			subjects = appendImplementationBlockerSubject(subjects, subject)
		}
		break
	}
	if state.VerificationPending && len(subjects) == 0 {
		for _, command := range verificationPlan {
			if subject, ok := implementationVerificationCommandBlockerSubject(command); ok {
				subjects = appendImplementationBlockerSubject(subjects, subject)
				break
			}
		}
		if len(subjects) == 0 {
			subjects = append(subjects, implementationBlockerSubject{
				Namespace: workspace.TaskLedgerBlockerSubjectCommand,
				ID:        "executable:git",
			})
		}
	}
	return subjects
}

func implementationVerificationCommandBlockerSubject(command string) (implementationBlockerSubject, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return implementationBlockerSubject{}, false
	}
	executable := strings.TrimPrefix(fields[0], "./")
	if separator := strings.LastIndexAny(executable, "/\\"); separator >= 0 {
		executable = executable[separator+1:]
	}
	if executable == "" {
		return implementationBlockerSubject{}, false
	}
	return implementationBlockerSubject{
		Namespace: workspace.TaskLedgerBlockerSubjectCommand,
		ID:        "executable:" + executable,
	}, true
}

func appendImplementationBlockerSubject(subjects []implementationBlockerSubject, subject implementationBlockerSubject) []implementationBlockerSubject {
	for _, existing := range subjects {
		if existing == subject {
			return subjects
		}
	}
	return append(subjects, subject)
}

func (r *runExecution) requiredEditSnippetBlockerCandidatePaths(brief workerruntime.WorkerRuntimeBrief, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	missing := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		missing[id] = struct{}{}
	}
	if r == nil {
		return nil
	}
	requiredPaths := append([]string(nil), brief.RequiredEditPaths...)
	if r.state.mutation.bound {
		requiredPaths = r.requiredEditCoveragePaths()
	}
	candidateIDs := make(map[string]struct{})
	for _, path := range r.state.committedPostimageFileIDs() {
		candidateIDs[path] = struct{}{}
	}
	for _, path := range requiredPaths {
		if canonicalID := r.requiredEditSnippetCanonicalID(path); canonicalID != "" {
			candidateIDs[canonicalID] = struct{}{}
		}
	}
	var paths []string
	for i, snippet := range brief.RequiredEditSnippets {
		if _, ok := missing[requiredEditSnippetName(snippet, i)]; !ok {
			continue
		}
		if path := NormalizeRunnerPath(snippet.Path); path != "" {
			if canonicalID := r.requiredEditSnippetCanonicalID(path); canonicalID != "" {
				candidateIDs[canonicalID] = struct{}{}
			}
		}
		for _, candidate := range r.requiredEditSnippetSnapshotCandidates(snippet, requiredPaths, candidateIDs) {
			paths = append(paths, candidate.CanonicalID)
		}
	}
	return uniqueSortedRunnerPaths(paths)
}

func taskLedgerBlockedItemProjection(state *taskLedgerState) []implementationBlockedTaskLedgerItem {
	if state == nil {
		return nil
	}

	blocked := make([]implementationBlockedTaskLedgerItem, 0)
	for _, item := range state.itemsWithStatus(taskledger.StatusBlocked) {
		blocker := item.Blocker
		if blocker == nil {
			continue
		}
		blocked = append(blocked, implementationBlockedTaskLedgerItem{
			ID:               item.ID,
			Kind:             blocker.Kind,
			SubjectNamespace: blocker.Subject.Namespace,
			SubjectID:        blocker.Subject.ID,
			Reason:           blocker.Reason,
			Paths:            uniqueSortedRunnerPaths(item.Paths),
		})
	}

	return blocked
}

func requiredEditSnippetNames(brief workerruntime.WorkerRuntimeBrief) []string {
	names := make([]string, 0, len(brief.RequiredEditSnippets))
	for i, snippet := range brief.RequiredEditSnippets {
		names = append(names, requiredEditSnippetName(snippet, i))
	}
	return textutil.Unique(names)
}

func taskLedgerActionObligationIDs(state *taskLedgerState, policy TaskLedgerPolicy) []string {
	if policy.Disabled || (state != nil && state.policy.Disabled) {
		return nil
	}

	if state == nil {
		if policy.Required {
			return []string{"task_ledger:create"}
		}
		return nil
	}

	if state.policy.Required && !state.created {
		return []string{"task_ledger:create"}
	}
	if !state.created {
		return nil
	}

	return textutil.Merge(state.unfinishedItemIDs(), state.blockedItemIDs())
}

func (r *runExecution) currentCompletionGateSemanticObligations() ([]string, bool) {
	if r == nil {
		return nil, false
	}

	active := r.state.completionGateRecovery.ActiveViolation
	switch active.InvariantID {
	case completionGateInvariantMutationSnippets:
		return r.implementationCompletionState(r.state.collectedResults).MissingRequiredSnippetIDs, true
	case completionGateInvariantSilentTestRemoval:
		return r.removedTestDeclarations(r.state.collectedResults), true
	case completionGateInvariantTaskLedger:
		switch active.ViolationID {
		case "task_ledger_missing":
			if r.taskLedger != nil && r.taskLedger.created {
				return nil, true
			}
			return []string{"task_ledger:create"}, true
		case "task_ledger_blocked":
			if r.taskLedger == nil {
				return nil, true
			}
			return r.taskLedger.blockedItemIDs(), true
		default:
			return taskLedgerActionObligationIDs(r.taskLedger, r.runner.TaskLedger), true
		}
	default:
		return nil, false
	}
}

func (r *runExecution) observeCompletionGateProgress(req workspace.ToolRequest, result workspace.ToolResult) completionGateRecoveryProgress {
	if r == nil {
		return completionGateRecoveryProgress{}
	}

	progress := r.state.observeCompletionGateEvidenceProgress(req, result)
	if progress.MadeProgress() || strings.TrimSpace(result.Error) != "" {
		return progress
	}

	if pending, ok := r.currentCompletionGateSemanticObligations(); ok {
		return r.state.observeCompletionGateSemanticProgress(pending)
	}
	return completionGateRecoveryProgress{}
}

func (r *runExecution) emitCompletionGateRecoveryProgress(result workspace.ToolResult, progress completionGateRecoveryProgress) {
	if r == nil || !progress.MadeProgress() {
		return
	}

	content := fmt.Sprintf(
		"successful %s result advanced the active %s completion-gate violation; total recorded rejections remain %d",
		strings.TrimSpace(result.Tool),
		progress.AdvancedViolationClass,
		r.state.completionGateRecovery.TotalAttempts,
	)
	if len(progress.SatisfiedObligationIDs) > 0 {
		content = fmt.Sprintf(
			"completion-gate obligation(s) satisfied: %s; pending obligation(s): %s; total recorded rejections remain %d",
			strings.Join(progress.SatisfiedObligationIDs, ", "),
			textutil.FirstNonEmpty(strings.Join(progress.PendingObligationIDs, ", "), "none"),
			r.state.completionGateRecovery.TotalAttempts,
		)
	} else if len(progress.SatisfiedSourceBodyPaths) > 0 {
		content = fmt.Sprintf(
			"source-body evidence satisfied active completion-gate path(s): %s; pending path(s): %s; total recorded rejections remain %d",
			strings.Join(progress.SatisfiedSourceBodyPaths, ", "),
			textutil.FirstNonEmpty(strings.Join(progress.PendingSourceBodyPaths, ", "), "none"),
			r.state.completionGateRecovery.TotalAttempts,
		)
	}
	r.emitEvent(RunEvent{Role: "completion_gate_recovery_progress", Content: content})

	if r.state.completionGateRecovery.hasActiveViolation() {
		if prompt, ok := r.completionGateRecoveryPrompt(r.state.collectedResults); ok {
			r.state.setCompletionGateRecoveryMessage(prompt)
		}
	}
}

func (state implementationCompletionState) HasChanges() bool {
	return len(state.ChangedPaths) > 0
}

func (state implementationCompletionState) HasPendingRejectedMutation() bool {
	return len(state.UnresolvedRejectedMutationPaths) > 0
}

func (state implementationCompletionState) VerificationOpen() bool {
	return state.VerificationPending || state.VerificationFailed || state.SemanticVerificationPending
}

func (state implementationCompletionState) CloseoutReady() bool {
	switch state.TerminalDisposition {
	case implementationTerminalDispositionPlanSubmitted,
		implementationTerminalDispositionSourceSatisfied:
		return len(state.MissingRequiredPaths) == 0 &&
			len(state.MissingRequiredSnippetIDs) == 0 &&
			!state.HasPendingRejectedMutation() &&
			len(state.OpenTaskLedgerObligationIDs) == 0 &&
			!state.VerificationOpen()
	case implementationTerminalDispositionExactBlocker:
		return state.blockedTaskLedgerItemsCoverPaths(state.MissingRequiredPaths) &&
			(len(state.MissingRequiredSnippetIDs) == 0 ||
				(len(state.MissingRequiredSnippetPaths) > 0 && state.blockedTaskLedgerItemsCoverPaths(state.MissingRequiredSnippetPaths))) &&
			state.blockedTaskLedgerItemsCoverPaths(state.UnresolvedRejectedMutationPaths) &&
			state.onlyValidatedBlockedTaskLedgerObligationsOpen() &&
			(!state.VerificationOpen() || state.blockedTaskLedgerItemsCoverVerification())
	case implementationTerminalDispositionMutationComplete:
		return len(state.MissingRequiredPaths) == 0 &&
			len(state.MissingRequiredSnippetIDs) == 0 &&
			!state.HasPendingRejectedMutation() &&
			len(state.OpenTaskLedgerObligationIDs) == 0 &&
			!state.VerificationOpen()
	default:
		return false
	}
}

func (state implementationCompletionState) blockedTaskLedgerItemsCoverPaths(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	covered := make(map[string]struct{})
	for _, item := range state.BlockedTaskLedgerItems {
		if path := blockedTaskLedgerItemCanonicalPath(item); path != "" {
			covered[path] = struct{}{}
		}
	}
	for _, path := range paths {
		if _, ok := covered[NormalizeRunnerPath(path)]; !ok {
			return false
		}
	}
	return true
}

func blockedTaskLedgerItemCanonicalPath(item implementationBlockedTaskLedgerItem) string {
	if item.SubjectNamespace != workspace.TaskLedgerBlockerSubjectWorkspacePath {
		return ""
	}

	return NormalizeRunnerPath(item.SubjectID)
}

func (state implementationCompletionState) blockedTaskLedgerItemsCoverVerification() bool {
	if len(state.VerificationBlockerSubjects) == 0 {
		return false
	}

	for _, expected := range state.VerificationBlockerSubjects {
		covered := false
		for _, item := range state.BlockedTaskLedgerItems {
			if item.SubjectNamespace == expected.Namespace && item.SubjectID == expected.ID {
				covered = true
				break
			}
			if item.SubjectNamespace == workspace.TaskLedgerBlockerSubjectPolicy &&
				expected.Namespace == workspace.TaskLedgerBlockerSubjectTool &&
				strings.TrimPrefix(item.SubjectID, "tool_rule:") == expected.ID {

				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func (state implementationCompletionState) onlyValidatedBlockedTaskLedgerObligationsOpen() bool {
	if len(state.BlockedTaskLedgerItems) == 0 {
		return false
	}
	blocked := make(map[string]struct{}, len(state.BlockedTaskLedgerItems))
	for _, item := range state.BlockedTaskLedgerItems {
		blocked[item.ID] = struct{}{}
	}
	for _, obligation := range state.OpenTaskLedgerObligationIDs {
		if _, ok := blocked[obligation]; !ok {
			return false
		}
	}
	return len(state.OpenTaskLedgerObligationIDs) == len(blocked)
}

func (r *runExecution) implementationCompletionStateForTerminalAnswer(answer string, results []workspace.ToolResult) implementationCompletionState {
	state := r.implementationCompletionState(results)
	if r == nil {
		return state
	}
	if paths, ok := r.planThenExecuteAcceptedSourceSatisfiedPaths(answer, results); ok {
		state.TerminalDisposition = implementationTerminalDispositionSourceSatisfied
		state.SourceSatisfiedPaths = paths
		state.MissingRequiredPaths = nil
		return state
	}
	if taskLedgerExplicitBlockerAnswer(answer) && state.onlyValidatedBlockedTaskLedgerObligationsOpen() {
		state.TerminalDisposition = implementationTerminalDispositionExactBlocker
	}
	return state
}

func composeTypedTaskLedgerBlockerStatus(items []implementationBlockedTaskLedgerItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<<QUESTION>>: Task ledger is blocked by validated external constraints.\n\nBlocked items:\n")
	for _, item := range items {
		fmt.Fprintf(
			&b,
			"- id=%q; kind=%q; subject_namespace=%q; subject_id=%q; reason=%q; paths=%q\n",
			item.ID,
			item.Kind,
			item.SubjectNamespace,
			item.SubjectID,
			item.Reason,
			item.Paths,
		)
	}
	return strings.TrimSpace(b.String())
}

func (state implementationCompletionState) withDerivedTerminalDisposition() implementationCompletionState {
	if state.TerminalDisposition != implementationTerminalDispositionNone {
		return state
	}
	if state.HasChanges() &&
		len(state.MissingRequiredPaths) == 0 &&
		len(state.MissingRequiredSnippetIDs) == 0 &&
		!state.HasPendingRejectedMutation() &&
		len(state.OpenTaskLedgerObligationIDs) == 0 &&
		!state.VerificationOpen() {

		state.TerminalDisposition = implementationTerminalDispositionMutationComplete
	}

	return state
}

func implementationWriteCapabilityAvailable(tools []string) bool {
	for _, tool := range tools {
		if workspace.IsWorkspaceWriteTool(tool) {
			return true
		}
	}

	return false
}

type implementationCloseoutFacts struct {
	StopLabel           string
	ChangedPaths        []string
	RemainingPaths      []string
	MissingSnippets     []string
	RejectedWritePaths  []string
	OpenTaskLedgerItems []string
	BlockedTaskItems    []implementationBlockedTaskLedgerItem
	SourcedPaths        string
	InspectedEvidence   string
	RepeatedReadPaths   string
	VerificationPending bool
	VerificationFailed  bool
}

func (r *runExecution) implementationContractCloseoutBlocker(results []workspace.ToolResult, stopReason string) (string, bool) {
	if r == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return "", false
	}

	completionState := r.implementationCompletionState(results)
	if completionState.CloseoutReady() {
		return "", false
	}

	facts := implementationCloseoutFacts{
		StopLabel:           implementationCloseoutStopLabel(stopReason),
		ChangedPaths:        completionState.ChangedPaths,
		RemainingPaths:      completionState.MissingRequiredPaths,
		MissingSnippets:     completionState.MissingRequiredSnippetIDs,
		RejectedWritePaths:  completionState.UnresolvedRejectedMutationPaths,
		OpenTaskLedgerItems: completionState.OpenTaskLedgerObligationIDs,
		BlockedTaskItems:    completionState.BlockedTaskLedgerItems,
		SourcedPaths:        implementationTargetWriteReadySourcedPathSummary(results, 5),
		InspectedEvidence:   conversationInspectedPathSummary(results),
		RepeatedReadPaths:   implementationCloseoutRepeatedReadSummary(results, 5),
		VerificationPending: completionState.VerificationPending || completionState.SemanticVerificationPending,
		VerificationFailed:  completionState.VerificationFailed,
	}

	return composeImplementationCloseoutBlocker(facts), true
}

func implementationCloseoutStopLabel(stopReason string) string {
	switch strings.TrimSpace(stopReason) {
	case "implementation_tool_step_budget_blocked", "tool_step_budget_exhausted":
		return "implementation tool budget"
	case "model_call_budget_exhausted":
		return "implementation model-call budget"
	case "operational_model_call_limit_exceeded":
		return "implementation operational model-call limit"
	case "no_progress_budget_exhausted":
		return "implementation no-progress closeout"
	case "duration_budget_exhausted":
		return "implementation duration budget"
	default:
		return "implementation closeout"
	}
}

func composeImplementationCloseoutBlocker(facts implementationCloseoutFacts) string {
	var b strings.Builder

	label := strings.TrimSpace(facts.StopLabel)
	if label == "" {
		label = "implementation closeout"
	}

	b.WriteString("<<QUESTION>>: ")
	b.WriteString(label)
	b.WriteString(" reached before the implementation contract produced a successful workspace write, accepted no-op evidence, or exact blocker.")

	if len(facts.ChangedPaths) > 0 {
		b.WriteString("\n\nChanged paths:\n")
		for _, path := range facts.ChangedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n\nChanged paths: none\n")
	}

	b.WriteString("\nRemaining obligations:\n")
	if len(facts.ChangedPaths) == 0 {
		b.WriteString("- no successful write was recorded\n")
		b.WriteString("- no no-op evidence or exact blocker was produced\n")
	}

	if len(facts.RemainingPaths) > 0 {
		b.WriteString("- required edit path(s) not changed: ")
		b.WriteString(strings.Join(facts.RemainingPaths, ", "))
		b.WriteString("\n")
	}

	if len(facts.MissingSnippets) > 0 {
		b.WriteString("- required edit snippet(s) still missing: ")
		b.WriteString(strings.Join(facts.MissingSnippets, ", "))
		b.WriteString("\n")
	}

	if len(facts.RejectedWritePaths) > 0 {
		b.WriteString("- recoverable rejected mutation path(s) still unresolved: ")
		b.WriteString(strings.Join(facts.RejectedWritePaths, ", "))
		b.WriteString("\n")
	}

	if len(facts.OpenTaskLedgerItems) > 0 {
		b.WriteString("- task ledger obligation(s) still open: ")
		b.WriteString(strings.Join(facts.OpenTaskLedgerItems, ", "))
		b.WriteString("\n")
	}
	for _, item := range facts.BlockedTaskItems {
		fmt.Fprintf(
			&b,
			"- blocked task ledger item id=%q: kind=%q; subject_namespace=%q; subject_id=%q; reason=%q; paths=%q\n",
			item.ID,
			item.Kind,
			item.SubjectNamespace,
			item.SubjectID,
			item.Reason,
			item.Paths,
		)
	}

	if facts.VerificationPending {
		b.WriteString("- verification pending after recorded workspace write(s)\n")
	}
	if facts.VerificationFailed {
		b.WriteString("- verification failed after the latest workspace write\n")
	}

	if sourced := strings.TrimSpace(facts.SourcedPaths); sourced != "" {
		b.WriteString("\nSource-body evidence already gathered:\n- ")
		fmt.Fprintf(&b, "%q", sourced)
		b.WriteString("\n")
	}

	if repeated := strings.TrimSpace(facts.RepeatedReadPaths); repeated != "" {
		b.WriteString("\nRepeated allowed read evidence observed:\n- ")
		fmt.Fprintf(&b, "%q", repeated)
		b.WriteString("\n")
	}

	if inspected := strings.TrimSpace(facts.InspectedEvidence); inspected != "" {
		b.WriteString("\nLatest inspected evidence:\n- ")
		fmt.Fprintf(&b, "%q", inspected)
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func implementationCloseoutRepeatedReadSummary(results []workspace.ToolResult, limit int) string {
	counts := make(map[string]int)

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameReadFile, workspace.ToolNameCodeNavigate, workspace.ToolNameReadOnlyCapability:
		default:
			continue
		}

		for _, path := range newToolResultEvidenceRecord(result).ReadPaths() {
			path = NormalizeRunnerPath(path)
			if path == "" {
				continue
			}

			counts[path]++
		}
	}

	var repeated []string
	for path, count := range counts {
		if count > 1 {
			repeated = append(repeated, path)
		}
	}

	if len(repeated) == 0 {
		return ""
	}

	return summarizeRunnerPaths(uniqueSortedRunnerPaths(repeated), limit)
}

func (r *runExecution) requiredPathCompletionRecoveryRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	missing := r.remainingRequiredEditPaths(results)
	submitCorrection := r.planThenExecutePlannerPhase() && planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority())
	anchorRefreshState := PlanExecutePlanningState{}
	anchorRefresh := false

	if r.planThenExecutePlannerPhase() {
		anchorRefreshState = r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
		anchorRefresh = anchorRefreshState.Mode == PlanExecutePlanningModeAnchorRefresh && len(anchorRefreshState.RefreshPaths) > 0
	}

	if len(missing) == 0 {
		return workspace.ToolResult{}, false
	}

	if !anchorRefresh {
		completionState := r.implementationCompletionStateForTerminalAnswer(reply, results)
		if completionState.CloseoutReady() {
			switch completionState.TerminalDisposition {
			case implementationTerminalDispositionPlanSubmitted,
				implementationTerminalDispositionSourceSatisfied,
				implementationTerminalDispositionExactBlocker:
				return workspace.ToolResult{}, false
			}
		}
	}

	if r.planThenExecutePlannerPhase() {
		if anchorRefresh {
			return completionGateRejectionWithRecovery(
				"Completion gate rejected anchor-refresh final answer",
				"bounded source-body refresh remains required for rejected submit_plan anchor path(s): "+quotedRunnerStrings(anchorRefreshState.RefreshPaths)+". Final answers, `Decision: source-satisfied`, and `<<QUESTION>>:` do not satisfy this phase while a concrete refresh path is available. Issue exactly one bounded `read_file` or `code_navigate` source-body request for one refresh path before corrected `submit_plan`.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantPlanAnchorRefresh, completionGateViolationPlanAnchorRefresh, completionGateViolationSourceEvidence, anchorRefreshState.RefreshPaths),
			), true
		}

		correction := ""

		if rejected, _, ok := lastRejectedSubmitPlanCoverageResult(results); ok {
			correctionBrief := lastSubmitPlanRejectionBrief(rejected)
			if submitCorrection {
				correctionBrief = lastSubmitPlanCorrectionRejectionBrief(rejected)
			}

			correction = " Previous rejected submit_plan drafts do not count as coverage. " + correctionBrief + planThenExecuteSubmitPlanCorrectionInstructionForBrief(r.state.runtimeBrief, rejected)
		}

		if submitCorrection {
			return completionGateRejectionWithRecovery(
				"Completion gate rejected non-submit_plan correction",
				"required edit path(s) remain uncovered by corrected submit_plan: "+quotedRunnerStrings(missing)+"."+correction+" Submit-correction mode requires parser-readable submit_plan unless a validated typed blocker is already recorded. "+planThenExecuteSubmitPlanCoverageSentence()+" Do not answer `Decision: source-satisfied` and do not reply in prose.",
				newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "submit_plan_correction_pending", completionGateViolationPlanSubmission, missing),
			), true
		}

		return completionGateRejectionWithRecovery(
			"Completion gate rejected non-specific submit_plan blocker",
			"required edit path(s) remain uncovered by submit_plan: "+quotedRunnerStrings(missing)+"."+correction+" After completion-gate recovery, the required contract outcome is submit_plan ("+planThenExecuteSubmitPlanCoverageSentence()+"), `Decision: source-satisfied` with current-state evidence for every remaining required path, or a validated typed blocker recorded by the task ledger.",
			newCompletionGateRecoveryEnvelope(completionGateInvariantPlanSubmission, "submit_plan_missing", completionGateViolationPlanSubmission, missing),
		), true
	}

	return completionGateRejectionWithRecovery(
		"Completion gate rejected non-specific required-path blocker",
		"required edit path(s) remain unchanged: "+quotedRunnerStrings(missing)+". After completion-gate recovery, the required contract outcome is a concrete write or a validated typed blocker recorded by the task ledger.",
		newCompletionGateRecoveryEnvelope(completionGateInvariantMutationRequiredPaths, "required_edit_paths_remaining", completionGateViolationMutation, missing),
	), true
}

func (r *runExecution) requiredSourcePrerequisiteCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || questionProtocolAnswer(reply) {
		return workspace.ToolResult{}, false
	}

	pending := r.pendingRequiredSourceBodyPaths(results)
	if len(pending) == 0 {
		return workspace.ToolResult{}, false
	}

	result := sourcePrerequisiteRejection(
		"Final answer rejected before required source evidence",
		"required source-body evidence is still pending for: "+quotedRunnerStrings(pending)+". The response was not converted into a read. Issue a fresh bounded source-body request through the currently callable read surface, or answer <<QUESTION>> with the exact path-specific blocker.",
		pending,
	).toolResult(completionGateToolName)

	return annotateCompletionGateRecoveryEnvelope(
		result,
		newCompletionGateRecoveryEnvelope(
			completionGateInvariantSourcePrerequisite,
			"required_source_body_pending",
			completionGateViolationSourceEvidence,
			pending,
		),
	), true
}

func (r *runExecution) pendingRequiredSourceBodyPaths(results []workspace.ToolResult) []string {
	if r == nil || len(r.state.runtimeBrief.RequiredEditPaths) == 0 {
		return nil
	}

	if r.planThenExecutePlannerPhase() {
		if successfulSubmitPlanResultCount(results) > 0 {
			return nil
		}
		return uniqueSortedRunnerPaths(mapKeys(planThenExecuteUnreadRequiredPaths(r.state.runtimeBrief, results, r.planThenExecutePathAuthority())))
	}

	remaining := r.remainingRequiredEditPaths(results)
	if !runnerBriefRequiresMutation(r.state.runtimeBrief) || !scopedImplementationUsesBoundedSourceEdits(r.state.runtimeBrief, remaining) {
		return nil
	}

	projection := newToolResultEvidenceProjection(results)
	pending := make([]string, 0, len(remaining))
	for _, path := range remaining {
		path = NormalizeRunnerPath(path)
		if path == "" || writePathIsNonSource(path) || projection.SourceEvidenceForPath(path).Level == planThenExecuteEvidenceAnchorReady {
			continue
		}
		if r.runner.Workspace != nil && !r.scopedImplementationPathsRequireExistingSourceEvidence([]string{path}) {
			continue
		}
		pending = append(pending, path)
	}

	return uniqueSortedRunnerPaths(pending)
}

func (r *runExecution) completionGateRepeatedBlockerAnswer(result workspace.ToolResult, results []workspace.ToolResult) string {
	if r != nil && r.planThenExecutePlannerPhase() && planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		pending := r.implementationCompletionState(results).MissingRequiredPaths
		if envelope, ok := completionGateRecoveryEnvelopeFromResult(result); ok {
			pending = textutil.Merge(pending, envelope.PendingPaths)
		}
		if result.Facts != nil {
			pending = textutil.Merge(pending, result.Facts.DeferredPlanPaths)
		}

		var b strings.Builder
		b.WriteString("<<QUESTION>>: Plan-execute submit correction is still required.")
		b.WriteString("\n\nRequired action:\n- submit a fresh parser-readable `submit_plan` with real edits\n- place any path already at the requested final state in `source_satisfied_paths`")
		if len(pending) > 0 {
			b.WriteString("\n\nPending paths:\n")
			for _, path := range uniqueSortedRunnerPaths(pending) {
				b.WriteString("- ")
				b.WriteString(path)
				b.WriteString("\n")
			}
		}
		if code := strings.TrimSpace(result.Code); code != "" {
			b.WriteString("\nTyped rejection code: ")
			b.WriteString(code)
		}
		return strings.TrimSpace(b.String())
	}

	if r != nil && runnerBriefRequiresMutation(r.state.runtimeBrief) {
		if blocker, ok := r.implementationContractCloseoutBlocker(results, "completion_contract_incomplete"); ok {
			return blocker
		}
	}

	for index := len(results) - 1; index >= 0; index-- {
		denial := results[index]
		if denial.Facts == nil || denial.Facts.RejectionDisposition != toolfacts.ToolRejectionPolicyDenied {
			continue
		}
		code := strings.TrimSpace(denial.Code)
		if code != "permission_denied" && code != "approval_required" {
			continue
		}

		var b strings.Builder
		if code == "approval_required" {
			b.WriteString("<<QUESTION>>: Approval is required before the requested tool can run.")
		} else {
			b.WriteString("<<QUESTION>>: Permission policy denied the requested tool.")
		}
		if tool := strings.TrimSpace(denial.Tool); tool != "" {
			b.WriteString("\n\nDenied tool: `")
			b.WriteString(tool)
			b.WriteString("`")
		}
		if len(denial.Paths) > 0 && strings.TrimSpace(denial.Paths[0]) != "" {
			b.WriteString("\nDenied target: `")
			b.WriteString(strings.TrimSpace(denial.Paths[0]))
			b.WriteString("`")
		}
		b.WriteString("\nNo denied tool output was used as evidence.")
		return strings.TrimSpace(b.String())
	}

	var b strings.Builder
	b.WriteString("<<QUESTION>>: The run stopped before a supported final answer was produced.")
	if envelope, ok := completionGateRecoveryEnvelopeFromResult(result); ok && len(envelope.PendingPaths) > 0 {
		b.WriteString("\n\nPending evidence paths:\n")
		for _, path := range envelope.PendingPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	} else if paths := newToolResultEvidenceProjection(results).promptEvidencePaths(8); len(paths) > 0 {
		b.WriteString("\n\nInspected evidence paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (r *runExecution) planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult, nativeSubmitPlan bool) (string, bool) {
	if r == nil || lastResult == nil || !r.planThenExecutePlannerPhase() {
		return "", false
	}

	if strings.TrimSpace(req.Tool) != workspace.ToolNameSubmitPlan || !planThenExecuteSubmitCorrectionActive(r.state.runtimeBrief, results, r.planThenExecutePathAuthority()) {
		return "", false
	}

	if !planThenExecuteSubmitPlanRejectedForCorrection(req, *lastResult) {
		return "", false
	}

	state := r.derivePlanExecutePlanningState(results, PlanExecutePlanningTransport{TextualToolParser: true})
	if state.Mode == PlanExecutePlanningModeAnchorRefresh {
		return "", false
	}

	if nativeSubmitPlan {
		return planThenExecuteNativeSubmitPlanCorrectionPrompt(r.state.runtimeBrief, *lastResult), true
	}

	return RenderPlanExecuteRecoveryPlan(planThenExecuteRepeatedSubmitCorrectionRecoveryPlan(*lastResult)), true
}

func (r *runExecution) planThenExecuteRepeatedSubmitPlanCorrectionExhaustedRejection(req workspace.ToolRequest, lastResult *workspace.ToolResult, results []workspace.ToolResult) (string, string, bool) {
	if _, ok := r.planThenExecuteRepeatedSubmitPlanCorrectionPrompt(req, lastResult, results, false); !ok {
		return "", "", false
	}

	summary, errorText := planThenExecuteRepeatedCorrectionExhaustedRejection(*lastResult)
	return summary, errorText, true
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base string) string {
	return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce a visible parser-readable action now. Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request; submit_plan remains the terminal action when the evidence is sufficient.\n\n" + base
}

func planThenExecuteSubmitCorrectionHiddenOnlyVisiblePromptForState(base string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if planThenExecuteSubmitCorrectionBlockerAllowed(brief, results) {
		return "The previous retry produced hidden-only output with no parser-readable action. Hidden thinking is disabled for this retry; produce a visible parser-readable action now. Permission-authorized read/search/discovery tools remain callable for a fresh model-selected request; submit_plan or an exact path-specific `<<QUESTION>>:` blocker remains available when the evidence is sufficient.\n\n" + base
	}

	return planThenExecuteSubmitCorrectionHiddenOnlyVisiblePrompt(base)
}

func lowercaseFirstSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	first := value[0]
	if first >= 'A' && first <= 'Z' {
		return string(first+'a'-'A') + value[1:]
	}

	return value
}

func uppercaseFirstSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	first := value[0]
	if first >= 'a' && first <= 'z' {
		return string(first-'a'+'A') + value[1:]
	}

	return value
}

// verificationContractSkipsGate reports whether the brief's
// VerificationContract value declares the post-mutation verification
// gate inapplicable. The gate is the right shape for atoms that
// produce code requiring tests/builds to confirm correctness; it's
// the wrong shape for atoms that only edit prose, prompts, or other
// text where the file write IS the deliverable.
//
// Generic mechanism — task-family declarations choose the value:
//   - "tests_passed_or_failed_or_skipped_with_blocker": gate fires
//     (current default for code-producing families).
//   - "not_required_for_text_mutations": gate skips (text-only
//     families like docs_edit, prompt_edit_security).
//   - any "not_required_for_*_lane" suffix: gate skips (read-only
//     lanes, by definition).
//
// The host project picks the right verification_contract per family
// in its workerruntime catalog; the runner enforces consistently.
// Adding a new "no verification needed" task family is now a one-line
// catalog change instead of a runner patch.

func readOnlyExplanationGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	contract := strings.TrimSpace(brief.Contract)
	readOnlyPermission := workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile)
	family := strings.TrimSpace(brief.TaskFamily)

	if workerruntime.TaskFamilyIsPlanThenExecuteReview(family) {
		return false
	}

	switch contract {
	case string(workerruntime.ContractArchitecture):
		return readOnlyPermission || workerruntime.TaskFamilyIsArchitecture(family)
	case string(workerruntime.ContractReviewFindings):
		return readOnlyPermission ||
			family == "review" ||
			family == string(autonomy.TaskFamilySecurityReview) ||
			family == string(autonomy.TaskFamilyPerformanceReview)
	}

	if !readOnlyPermission {
		return false
	}

	switch family {
	case string(autonomy.TaskFamilyArchitecture), "review":
		return true
	default:
		return false
	}
}

func readOnlyExplanationMetaRejectionReason(reply string) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	for _, phrase := range []string{
		"i have sufficient evidence",
		"the strongest implementation evidence is in",
		"the inspected implementation evidence is enough",
		"the retained source context identifies",
		"the gathered evidence points to these implementation surfaces",
		"enough for a bounded answer",
		"implementation surface",
	} {
		if strings.Contains(lower, phrase) {
			return "the answer describes evidence sufficiency or implementation surface instead of explaining the concrete behavior from the inspected code. State the mechanism or findings directly."
		}
	}

	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 && !ReadOnlyExplanationHasMechanismVerb(lower) {
		return "the answer mostly inventories paths or scope and does not explain what the inspected code does. State the concrete behavior those files establish."
	}

	return ""
}

func architectureUnsupportedExactDetail(reply string, results []workspace.ToolResult) []string {
	exactSymbols := ExactEvidenceSymbolSet(results)
	if len(exactSymbols) == 0 {
		return nil
	}

	mentioned := ExplicitAnswerCodeSymbols(reply)
	if len(mentioned) == 0 {
		return nil
	}

	var unsupported []string

	for _, symbol := range mentioned {
		key := NormalizedAnswerSymbol(symbol)
		if key == "" {
			continue
		}

		if ArchitectureNonSourceSymbolTerm(key) {
			continue
		}

		if _, ok := exactSymbols[key]; ok {
			continue
		}

		if answerSymbolHasExplicitUncertainty(reply, symbol) {
			continue
		}

		unsupported = textutil.AppendUnique(unsupported, symbol)
	}

	return unsupported
}

func sourceBodyEvidenceSymbolCandidates(line string) []string {
	seen := map[string]struct{}{}

	var out []string

	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}

		key := NormalizedAnswerSymbol(raw)
		if key == "" {
			return
		}

		if _, ok := seen[key]; ok {
			return
		}

		seen[key] = struct{}{}

		out = append(out, raw)
	}

	if name := sourceBodyFunctionName(stripSourceEvidenceLinePrefix(strings.TrimSpace(line))); name != "" {
		add(name)
	}

	for _, match := range backtickedTokenPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range qualifiedIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, match := range callLikeIdentifierPattern.FindAllStringSubmatch(line, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	for _, token := range codeShapedTokenPattern.FindAllString(line, -1) {
		add(token)
	}

	for _, token := range codeShapedSnakeCasePattern.FindAllString(line, -1) {
		add(token)
	}

	for _, token := range strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		add(token)
	}

	return out
}

func delegatedReadOnlyLooksExportedTypeName(value string) bool {
	if len(value) < 2 {
		return false
	}

	first := rune(value[0])
	if !unicode.IsUpper(first) {
		return false
	}

	hasLower := false

	for _, r := range value[1:] {
		if unicode.IsLower(r) {
			hasLower = true
			continue
		}

		if unicode.IsDigit(r) {
			continue
		}

		return false
	}

	return hasLower
}

func answerSymbolHasExplicitUncertainty(reply string, symbol string) bool {
	window := answerSymbolEvidenceWindow(reply, symbol, 180)
	if window == "" {
		return false
	}

	return answerHasExplicitUncertainty(window)
}

func answerSymbolEvidenceWindow(reply string, symbol string, radius int) string {
	reply = strings.TrimSpace(reply)
	symbol = strings.TrimSpace(symbol)

	if reply == "" || symbol == "" {
		return ""
	}

	candidates := []string{symbol}
	if dot := strings.LastIndex(symbol, "."); dot >= 0 && dot+1 < len(symbol) {
		candidates = append(candidates, strings.TrimSpace(symbol[dot+1:]))
	}

	lowerReply := strings.ToLower(reply)
	idx := -1
	length := 0

	for _, candidate := range candidates {
		candidate = strings.Trim(candidate, "`'\"()[]{}<>,.:;!?")
		if candidate == "" {
			continue
		}

		found := strings.Index(lowerReply, strings.ToLower(candidate))
		if found >= 0 {
			idx = found
			length = len(candidate)

			break
		}
	}

	if idx < 0 {
		return ""
	}

	if sentence := answerSentenceContainingIndex(reply, idx, length); sentence != "" {
		return sentence
	}

	if radius <= 0 {
		radius = 120
	}

	start := idx - radius
	if start < 0 {
		start = 0
	}

	end := idx + length + radius
	if end > len(reply) {
		end = len(reply)
	}

	return reply[start:end]
}

func answerSentenceContainingIndex(text string, idx int, length int) string {
	if idx < 0 || idx >= len(text) {
		return ""
	}

	start := 0

	for i := idx - 1; i >= 0; i-- {
		switch text[i] {
		case '.', '!', '?', '\n':
			start = i + 1
			i = -1
		}
	}

	end := len(text)
	for i := idx + length; i < len(text); i++ {
		switch text[i] {
		case '.', '!', '?', '\n':
			end = i + 1
			i = len(text)
		}
	}

	return strings.TrimSpace(text[start:end])
}

func answerHasExplicitUncertainty(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))

	return ContainsAny(lower,
		"not directly inspected",
		"not directly read",
		"not confirmed",
		"not verified",
		"did not inspect",
		"didn't inspect",
		"uncertain",
		"may ",
		"might ",
		"appears ",
		"suggests ",
		"likely ",
		"seems ",
	)
}

func runnerBriefRequiresMutation(brief workerruntime.WorkerRuntimeBrief) bool {
	family := strings.ToLower(strings.TrimSpace(brief.TaskFamily))
	contract := strings.ToLower(strings.TrimSpace(brief.Contract))
	permission := strings.ToLower(strings.TrimSpace(brief.PermissionProfile))

	return strings.Contains(family, "implement") ||
		strings.Contains(family, "edit") ||
		strings.Contains(contract, "implementation") ||
		strings.Contains(permission, "write")
}

func writeReadyPacketReadContractActive(brief workerruntime.WorkerRuntimeBrief, mutationCommitted bool) bool {
	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if mutationCommitted {
		return false
	}

	return true
}

// briefHasUsableEditAnchors reports whether the brief carries inline
// edit anchors the model can actually act on without first reading
// source. Required edit paths alone don't qualify — they're a write
// allowlist, not evidence of where to put the change. Without anchors,
// the harness shouldn't claim packet-driven write-readiness, and the
// pre-write read guidance should match an abstract brief.
func briefHasUsableEditAnchors(brief workerruntime.WorkerRuntimeBrief) bool {
	if len(brief.RequiredEditSnippets) > 0 {
		return true
	}

	return exactAnchorImplementationActive(brief)
}

func implementationScopedEditSurfaceActive(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, missingRequiredPaths []string, mutationCommitted bool) bool {
	if planThenExecutePlannerBrief(brief) {
		return false
	}

	if !runnerBriefRequiresMutation(brief) || len(brief.RequiredEditPaths) == 0 {
		return false
	}

	if exactAnchorPreWriteSurfaceActive(brief, results, mutationCommitted) {
		return false
	}

	return len(missingRequiredPaths) != 0
}

// shouldForceAnswerForPostCompletionVerification returns true when the
// implementation contract is already fulfilled (all required edit paths
// have been written) and the agent is now thrashing on non-write tool
// calls to self-verify its work. Counts the current request if it is a
// non-write tool, then returns true when the running counter exceeds the
// configured cutoff. Write tools never count and reset the cutoff in
// effect because a write means the agent is actually doing more work, not
// just verifying.
func (r *runExecution) shouldForceAnswerForPostCompletionVerification(req workspace.ToolRequest, results []workspace.ToolResult, counter *int) bool {
	if r == nil || counter == nil {
		return false
	}

	if !runnerBriefRequiresMutation(r.state.runtimeBrief) || len(r.state.runtimeBrief.RequiredEditPaths) == 0 {
		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) {
		*counter = 0
		return false
	}

	if !r.implementationCompletionState(results).CloseoutReady() {
		*counter = 0
		return false
	}

	*counter++

	return *counter > postCompletionVerificationCutoff
}
