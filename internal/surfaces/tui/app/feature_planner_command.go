package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/sessionid"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/featureplannerruntime"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recovery"
)

func (m *Model) runFeaturePlannerCommandOnce(ctx context.Context, objective string) (string, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("feature-planner objective is required")
	}

	m.emitJSONStart(objective)

	if err := m.ensureServerReady(ctx); err != nil {
		m.emitJSONError("server_not_ready", err.Error())

		return "", err
	}

	productBrief, effortRoot, err := m.loadFeaturePlannerProductContext(objective)
	if err != nil {
		m.emitJSONError("product_context_unavailable", err.Error())

		return "", err
	}

	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, objective, "", 3, time.Now().UTC())

	if productBrief != nil {
		state.EffortID = productBrief.EffortID
		state.EffortRoot = effortRoot
		m.featurePlannerEffortRoot = effortRoot
	}

	operatorCommand := "/feature-planner " + objective

	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: operatorCommand})
	m.logMessage("user", operatorCommand)
	m.logMessage("route_plan", "initial=feature-planner reason=typed_feature_planner_command route_source=typed_command")
	m.logFeaturePlannerInternalEvent(featureplannercontract.EventName(featureplannercontract.EventStart), objective)

	if productBrief != nil {
		m.logFeaturePlannerInternalEvent(featureplannercontract.EventName(featureplannercontract.EventProductContext), fmt.Sprintf("effort_id=%s effort_root=%s", productBrief.EffortID, effortRoot))

		if productBrief.IsClosedSourceSatisfied() {
			return m.closeFeaturePlannerTUISourceSatisfied(state, *productBrief)
		}
	}

	writeEnvelope := featureplanner.ProductWriteEnvelopePaths(productBrief)
	writeEnvelopeExplicit := productBrief != nil

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                objective,
		MaxPlanningIterations: 3,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  productBrief,
		ReconcileActiveRuns:   m.reconcileFeaturePlannerActivePlanExecuteRuns,
		WorkspaceRoot:         m.projectRoot(),
	}, func(prompt string, taskFamily string) (string, error) {
		if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
			return m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, writeEnvelope, writeEnvelopeExplicit)
		}

		return m.runFeaturePlannerTUITurn(ctx, prompt, taskFamily)
	})
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	for _, event := range result.Events {
		m.logFeaturePlannerEvent(event)
	}

	eventsPath, err := featureplanner.AppendEvents(m.projectRoot(), result.State.SessionID, result.Events)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	result.State.EventsPath = eventsPath

	statePath, err := m.saveFeaturePlannerStateAndPlanTab(&result.State)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	answer := formatFeaturePlannerTUIAnswerForRoot(result.State, statePath, m.projectRoot())
	m.logFeaturePlannerInternalEvent(featurePlannerTUITerminalRole(result.State.Status), answer)
	m.updateDirectFeaturePlannerSession(result.State)

	return m.finishPromptOnceReply(answer), nil
}

func (m *Model) closeFeaturePlannerTUISourceSatisfied(state featureplanner.FeatureState, brief featureplanner.ProductWorkflowBrief) (string, error) {
	state, event := featureplanner.ProductSourceSatisfiedCloseout(state, brief, brief.EffortID)
	m.logFeaturePlannerEvent(event)

	eventsPath, err := featureplanner.AppendEvents(m.projectRoot(), state.SessionID, []featureplanner.Event{event})
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	state.EventsPath = eventsPath

	statePath, err := m.saveFeaturePlannerStateAndPlanTab(&state)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	answer := formatFeaturePlannerTUIAnswerForRoot(state, statePath, m.projectRoot())
	m.logFeaturePlannerInternalEvent(featurePlannerTUITerminalRole(state.Status), answer)
	m.updateDirectFeaturePlannerSession(state)

	return m.finishPromptOnceReply(answer), nil
}

func (m *Model) loadFeaturePlannerProductContext(objective string) (*featureplanner.ProductWorkflowBrief, string, error) {
	effortRoot, found := featurePlannerEffortRootFromObjective(objective)
	if !found {
		effortRoot = strings.TrimSpace(m.featurePlannerEffortRoot)
		found = effortRoot != ""
	}

	if !found {
		return nil, "", nil
	}

	effortRoot = m.resolveFeaturePlannerEffortRoot(effortRoot)

	brief, err := loadDirectFeaturePlannerProductBrief(effortRoot)
	if err != nil {
		return nil, effortRoot, fmt.Errorf("load product workflow implementation handoff from %s: %w", effortRoot, err)
	}

	return brief, effortRoot, nil
}

func (m *Model) resolveFeaturePlannerEffortRoot(effortRoot string) string {
	effortRoot = cleanFeaturePlannerEffortPathToken(effortRoot)
	if filepath.IsAbs(effortRoot) {
		return filepath.Clean(effortRoot)
	}

	if root := strings.TrimSpace(m.projectRoot()); root != "" {
		return filepath.Clean(filepath.Join(root, effortRoot))
	}

	return filepath.Clean(effortRoot)
}

func featurePlannerEffortRootFromObjective(objective string) (string, bool) {
	fields := strings.Fields(objective)
	for idx, field := range fields {
		if field != "--effort-root" {
			continue
		}

		if idx+1 >= len(fields) {
			return "", false
		}

		candidate := cleanFeaturePlannerEffortPathToken(fields[idx+1])

		return candidate, candidate != ""
	}

	for _, field := range fields {
		candidate := cleanFeaturePlannerEffortPathToken(field)
		if featurePlannerLooksLikeProductEffortRoot(candidate) {
			return candidate, true
		}
	}

	return "", false
}

func cleanFeaturePlannerEffortPathToken(token string) string {
	return strings.Trim(strings.TrimSpace(token), "`'\".,;:()[]{}<>")
}

func featurePlannerLooksLikeProductEffortRoot(path string) bool {
	path = filepath.ToSlash(path)
	for _, prefix := range []string{
		"docs/product-workflows/efforts/started/",
		"docs/product-workflows/efforts/proposed/",
		"docs/product-workflows/efforts/closed/",
	} {
		if strings.Contains(path, prefix) {
			return true
		}
	}

	return false
}

func (m *Model) sendFeaturePlannerCommandAsync(objective string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(objective)
	m.beginAgentHistoryTurn()

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := m.runFeaturePlannerCommandOnce(ctx, objective)
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) sendDirectFeaturePlannerAnswerAsync(answer string) {
	m.streaming = true
	m.workingFrame = 0
	m.currentTurnBusyStatus = busyStatusForPrompt(answer)
	m.beginAgentHistoryTurn()

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := m.runDirectFeaturePlannerAnswerOnce(ctx, answer)
		m.events <- streamDoneMsg{err: err}
	}()
}

func (m *Model) runDirectFeaturePlannerAnswerOnce(ctx context.Context, answer string) (string, error) {
	sessionID := strings.TrimSpace(m.directFeaturePlannerSessionID)
	if sessionID == "" {
		return "", fmt.Errorf("no active direct feature-planner session is waiting for an answer")
	}

	state, _, err := featureplanner.LoadState(m.projectRoot(), sessionID)
	if err != nil {
		return "", err
	}

	productBrief, effortRoot, err := m.productBriefForFeaturePlannerState(state)
	if err != nil {
		return "", err
	}

	writeEnvelope := featureplanner.ProductWriteEnvelopePaths(productBrief)
	writeEnvelopeExplicit := productBrief != nil
	resumePlanRevision := directFeaturePlannerPlanRevisionBypassesQuestionAuthority(state, answer)

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                directFeaturePlannerResumePrompt(state, answer),
		MaxPlanningIterations: 3,
		ResumeAnswer:          answer,
		ResumePlanRevision:    resumePlanRevision,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  productBrief,
		ReconcileActiveRuns:   m.reconcileFeaturePlannerActivePlanExecuteRuns,
		WorkspaceRoot:         m.projectRoot(),
	}, func(prompt string, taskFamily string) (string, error) {
		if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
			return m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, writeEnvelope, writeEnvelopeExplicit)
		}

		return m.runFeaturePlannerTUITurn(ctx, prompt, taskFamily)
	})
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	for _, event := range result.Events {
		m.logFeaturePlannerEvent(event)
	}

	eventsPath, err := featureplanner.AppendEvents(m.projectRoot(), result.State.SessionID, result.Events)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}
	result.State.EventsPath = eventsPath

	statePath, err := m.saveFeaturePlannerStateAndPlanTab(&result.State)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	if effortRoot != "" {
		m.featurePlannerEffortRoot = effortRoot
	}

	reply := formatFeaturePlannerTUIAnswerForRoot(result.State, statePath, m.projectRoot())
	m.logFeaturePlannerInternalEvent(featurePlannerTUITerminalRole(result.State.Status), reply)
	m.updateDirectFeaturePlannerSession(result.State)

	return m.finishPromptOnceReply(reply), nil
}

func directFeaturePlannerResumePrompt(state featureplanner.FeatureState, answer string) string {
	prompt := ""
	if summary := strings.TrimSpace(state.PromptSummary); summary != "" {
		prompt = summary
	} else if summary := strings.TrimSpace(state.Plan.SourceTask.Summary); summary != "" {
		prompt = summary
	} else if summary := strings.TrimSpace(state.Plan.Summary); summary != "" {
		prompt = summary
	} else {
		prompt = strings.TrimSpace(answer)
	}

	if strings.TrimSpace(answer) != "" && directFeaturePlannerStateNeedsRepairInstruction(state) {
		return strings.TrimSpace(prompt + "\n\nOperator repair instruction:\n" + strings.TrimSpace(answer))
	}

	if strings.TrimSpace(answer) != "" && directFeaturePlannerInputLooksPlanRevision(answer) {
		return strings.TrimSpace(prompt + "\n\nOperator plan revision instruction:\n" + strings.TrimSpace(answer))
	}

	return prompt
}

func directFeaturePlannerPlanRevisionBypassesQuestionAuthority(state featureplanner.FeatureState, answer string) bool {
	if !directFeaturePlannerInputLooksPlanRevision(answer) {
		return false
	}

	for _, question := range openDirectFeaturePlannerQuestions(state) {
		authority := strings.TrimSpace(question.AuthorityRequired)
		if authority != "" && authority != "operator_approval" {
			return true
		}
	}

	return false
}

func (m *Model) productBriefForFeaturePlannerState(state featureplanner.FeatureState) (*featureplanner.ProductWorkflowBrief, string, error) {
	effortRoot := strings.TrimSpace(state.EffortRoot)
	if effortRoot == "" {
		return nil, "", nil
	}

	effortRoot = m.resolveFeaturePlannerEffortRoot(effortRoot)
	brief, err := loadDirectFeaturePlannerProductBrief(effortRoot)
	if err != nil {
		return nil, effortRoot, err
	}

	return brief, effortRoot, nil
}

func (m *Model) directFeaturePlannerHasOpenQuestion() bool {
	sessionID := strings.TrimSpace(m.directFeaturePlannerSessionID)
	if sessionID == "" {
		return false
	}

	state, _, err := featureplanner.LoadState(m.projectRoot(), sessionID)
	if err != nil {
		return false
	}

	return featureplanner.CountOpenPlannerQuestions(state) > 0
}

func (m *Model) directFeaturePlannerAcceptsOperatorInput() bool {
	sessionID := strings.TrimSpace(m.directFeaturePlannerSessionID)
	if sessionID == "" {
		return false
	}

	state, _, err := featureplanner.LoadState(m.projectRoot(), sessionID)
	if err != nil {
		return false
	}

	return featurePlannerStateAcceptsDirectOperatorInput(state)
}

func (m *Model) directFeaturePlannerShouldConsumeOperatorInput(input string) bool {
	sessionID := strings.TrimSpace(m.directFeaturePlannerSessionID)
	if sessionID == "" || strings.TrimSpace(input) == "" {
		return false
	}

	state, _, err := featureplanner.LoadState(m.projectRoot(), sessionID)
	if err != nil {
		return false
	}

	if directFeaturePlannerStateNeedsRepairInstruction(state) {
		return directFeaturePlannerInputLooksPlanRevision(input) || featurePlannerGenericAnswerLooksIntentional(input)
	}

	openQuestions := openDirectFeaturePlannerQuestions(state)
	if len(openQuestions) == 0 {
		return false
	}

	if directFeaturePlannerInputLooksPlanRevision(input) {
		return true
	}

	for _, question := range openQuestions {
		if directFeaturePlannerQuestionInputLooksAnswer(question, input) {
			return true
		}
	}

	return false
}

func openDirectFeaturePlannerQuestions(state featureplanner.FeatureState) []featureplanner.PlannerQuestion {
	out := make([]featureplanner.PlannerQuestion, 0, len(state.OpenQuestions))
	for _, question := range state.OpenQuestions {
		if featurePlannerQuestionOpen(question) {
			out = append(out, question)
		}
	}

	return out
}

func directFeaturePlannerQuestionInputLooksAnswer(question featureplanner.PlannerQuestion, input string) bool {
	switch strings.TrimSpace(question.AuthorityRequired) {
	case "operator_approval":
		return featurePlannerOperatorReplyLooksIntentional(input)
	case "accepted_source":
		return featurePlannerStructuredAnswerLooksIntentional(input)
	default:
		return featurePlannerGenericAnswerLooksIntentional(input)
	}
}

type featurePlannerOperatorInputIntent string

const (
	featurePlannerOperatorInputNone     featurePlannerOperatorInputIntent = ""
	featurePlannerOperatorInputDecision featurePlannerOperatorInputIntent = "decision"
	featurePlannerOperatorInputRevision featurePlannerOperatorInputIntent = "revision"
)

func directFeaturePlannerInputLooksPlanRevision(input string) bool {
	return classifyFeaturePlannerOperatorInput(input) == featurePlannerOperatorInputRevision
}

func classifyFeaturePlannerOperatorInput(input string) featurePlannerOperatorInputIntent {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return featurePlannerOperatorInputNone
	}

	normalized := featurePlannerNormalizedOperatorInput(trimmed)
	if normalized == "" {
		return featurePlannerOperatorInputNone
	}

	if featurePlannerOperatorInputHasRevisionIntent(normalized) {
		return featurePlannerOperatorInputRevision
	}

	for _, prefix := range []string{
		"approve",
		"approved",
		"authorize",
		"authorized",
		"yes",
		"yep",
		"ok",
		"okay",
		"go ahead",
		"proceed",
		"continue",
		"reject",
		"rejected",
		"deny",
		"denied",
		"no",
		"nope",
		"create",
		"do not",
		"don t",
		"dont",
		"don't",
	} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") {
			return featurePlannerOperatorInputDecision
		}
	}

	for _, phrase := range []string{
		" i approve ",
		" i authorize ",
		" you may ",
		" ok to ",
		" okay to ",
		" create these ",
		" create the ",
	} {
		if strings.Contains(" "+normalized+" ", phrase) {
			return featurePlannerOperatorInputDecision
		}
	}

	return featurePlannerOperatorInputNone
}

func featurePlannerOperatorInputHasRevisionIntent(normalized string) bool {
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return false
	}

	for _, prefix := range []string{
		"please revise",
		"revise",
		"update the plan",
		"change",
		"make it",
		"make the plan",
		"adjust",
		"add",
		"scope",
		"expand",
		"flesh out",
		"include",
		"narrow",
		"instead",
	} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") {
			return true
		}
	}

	for _, phrase := range []string{
		" revise the ",
		" update plan ",
		" update the ",
		" update this plan ",
		" change the ",
		" adjust the ",
		" add more detail ",
		" add more details ",
		" add more info ",
		" add more information ",
		" add detail ",
		" add details ",
		" add info ",
		" add information ",
		" flesh out the plan ",
		" include this in the plan ",
		" include that in the plan ",
		" update scope ",
		" revise scope ",
		" revise the scope ",
		" change the scope ",
		" make this ",
		" make it ",
		" instead ",
	} {
		if strings.Contains(" "+normalized+" ", phrase) {
			return true
		}
	}

	return false
}

func featurePlannerGenericAnswerLooksIntentional(input string) bool {
	input = strings.TrimSpace(input)
	if input == "" || strings.Contains(input, "?") {
		return false
	}

	return true
}

func featurePlannerStructuredAnswerLooksIntentional(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || strings.Contains(trimmed, "?") {
		return false
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return true
	}

	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"answer_source_kind", "evidence_refs", "source_facts"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

func featurePlannerOperatorReplyLooksIntentional(input string) bool {
	return classifyFeaturePlannerOperatorInput(input) != featurePlannerOperatorInputNone
}

func featurePlannerNormalizedOperatorInput(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	return strings.Join(fields, " ")
}

func directFeaturePlannerStateNeedsRepairInstruction(state featureplanner.FeatureState) bool {
	switch state.Status {
	case featureplanner.FeatureStatePlanningRepair, featureplanner.FeatureStateStaleEditAnchor:
		return true
	default:
		return false
	}
}

func featurePlannerStateAcceptsDirectOperatorInput(state featureplanner.FeatureState) bool {
	return featureplanner.CountOpenPlannerQuestions(state) > 0 || directFeaturePlannerStateNeedsRepairInstruction(state)
}

func (m *Model) updateDirectFeaturePlannerSession(state featureplanner.FeatureState) {
	switch state.Status {
	case featureplanner.FeatureStatePlanningQuestion, featureplanner.FeatureStatePlanningRepair, featureplanner.FeatureStateStaleEditAnchor:
		m.directFeaturePlannerSessionID = strings.TrimSpace(state.SessionID)
	default:
		m.directFeaturePlannerSessionID = ""
	}
}

func (m *Model) runFeaturePlannerTUITurn(ctx context.Context, prompt string, taskFamily string) (string, error) {
	return featureplannerruntime.CompleteModelTurn(ctx, m.client, prompt, m.chatOptions(), taskFamily, m.logFeaturePlannerInternalEvent)
}

type featurePlannerAtomPlanExecuteRunner func(context.Context, string) (string, error)

func (m *Model) runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx context.Context, prompt string, writeEnvelope []string, writeEnvelopeExplicit bool) (string, error) {
	return m.runFeaturePlannerAtomPlanExecuteWithRunner(ctx, prompt, writeEnvelope, writeEnvelopeExplicit, m.runPlanExecuteCommandOnce)
}

func (m *Model) runFeaturePlannerAtomPlanExecuteWithRunner(ctx context.Context, prompt string, writeEnvelope []string, writeEnvelopeExplicit bool, runOnce featurePlannerAtomPlanExecuteRunner) (string, error) {
	m.logFeaturePlannerPlanExecuteGitHistoryRequirement()

	priorSuppressTranscript := m.suppressPromptTranscript
	priorWriteEnvelope := append([]string(nil), m.planExecuteWriteEnvelope...)
	priorWriteEnvelopeExplicit := m.planExecuteWriteEnvelopeExplicit

	m.suppressPromptTranscript = true
	m.planExecuteWriteEnvelope = appendUniqueBriefStrings(nil, writeEnvelope...)
	m.planExecuteWriteEnvelopeExplicit = writeEnvelopeExplicit

	defer func() {
		m.suppressPromptTranscript = priorSuppressTranscript
		m.planExecuteWriteEnvelope = priorWriteEnvelope
		m.planExecuteWriteEnvelopeExplicit = priorWriteEnvelopeExplicit
	}()

	answer, err := runOnce(ctx, prompt)
	if err != nil {
		if m.lastPlanExecuteRunResult == nil {
			return "", err
		}

		answer = err.Error()
	}

	summary := m.featurePlannerPlanExecuteCompletionSummary(answer)
	if retryPrompt, ok := featureplanner.PlanExecuteRecoveryRetryPromptForSummary(prompt, summary); ok {
		answer, err = runOnce(ctx, retryPrompt)
		if err != nil {
			if m.lastPlanExecuteRunResult == nil {
				return "", err
			}

			answer = err.Error()
		}

		summary = m.featurePlannerPlanExecuteCompletionSummary(answer)
	}

	completion, err := encodeFeaturePlannerPlanExecuteCompletionSummary(summary)
	if err != nil {
		return "", err
	}

	m.logFeaturePlannerInternalEvent(string(featurePlannerPlanExecuteCompletionLogType(completion)), completion)

	return completion, nil
}

var featurePlannerPlanExecuteGitHistoryRequirementRole = featureplannercontract.EventName(featureplannercontract.EventPlanExecuteGitHistoryRequirement)

func (m *Model) logFeaturePlannerPlanExecuteGitHistoryRequirement() {
	m.logFeaturePlannerInternalEvent(featurePlannerPlanExecuteGitHistoryRequirementRole, featurePlannerPlanExecuteGitHistoryRequirementPayload())
}

func featurePlannerPlanExecuteGitHistoryRequirementPayload() string {
	body, _ := json.Marshal(map[string]any{
		featureplannercontract.PlanExecuteCompletionFieldTool:       string(featureplannercontract.ToolGitHistoryRequirement),
		featureplannercontract.PlanExecuteCompletionFieldLaneFamily: string(lanecontract.LanePlanExecute),
		"commit_descriptions_required":                              true,
		"evidence_tools":                                            []string{"history_range", "history_symbol"},
	})

	return string(body)
}

func planExecuteAnswerIsBlocked(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return true
	}

	for _, marker := range []string{
		"plan-execute: recovery-open",
		"plan-execute: recovery_open",
		"plan-execute: failed",
		"feature-planner: recovery-open",
		"contract blocked",
		"could not produce a plan artifact",
		"required_edit_paths_remaining",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	return false
}

func (m *Model) encodeFeaturePlannerPlanExecuteCompletion(summary string) (string, error) {
	return encodeFeaturePlannerPlanExecuteCompletionSummary(m.featurePlannerPlanExecuteCompletionSummary(summary))
}

func (m *Model) featurePlannerPlanExecuteCompletionSummary(text string) featureplanner.PlanExecuteCompletionSummary {
	summary := featureplanner.PlanExecuteCompletionSummaryFromOutput(text)
	if m != nil {
		summary = overlayFeaturePlannerPlanExecuteRunResult(summary, m.cfg.Workspace.Root, m.lastPlanExecuteRunResult)
	}

	summary.Tool = string(featureplannercontract.ToolPlanExecuteComplete)
	summary.LaneFamily = string(lanecontract.LanePlanExecute)

	if strings.TrimSpace(summary.Status) == "" {
		summary.Status = featureplanner.PlanExecuteCompletionStatusAmbiguous
		summary.FailureKind = textutil.FirstNonEmpty(summary.FailureKind, featureplanner.PlanExecuteCompletionMissingStatusFailureKind)
		summary.FailureReason = textutil.FirstNonEmpty(summary.FailureReason, featureplanner.PlanExecuteCompletionMissingStatusFailureReason)
	}

	if strings.TrimSpace(summary.CompletionSource) == "" {
		summary.CompletionSource = string(featureplannercontract.PlanExecuteCompletionSourceTUIReturned)
	}

	summary.Summary = featurePlannerPlanExecuteCompletionText(summary)
	summary = featureplanner.TrustObservedPlanExecuteCompletionSummary(summary)

	return summary
}

func encodeFeaturePlannerPlanExecuteCompletionSummary(summary featureplanner.PlanExecuteCompletionSummary) (string, error) {
	fields := summary.FeaturePlannerPayloadFields()

	body, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func featurePlannerPlanExecuteCompletionText(summary featureplanner.PlanExecuteCompletionSummary) string {
	var parts []string

	for _, item := range []struct {
		key   string
		value string
	}{
		{plancontract.PlanExecuteTerminalFieldStatus, summary.Status},
		{plancontract.PlanExecuteTerminalFieldPhase, summary.Phase},
		{plancontract.PlanExecuteTerminalFieldReviewStatus, summary.ReviewStatus},
		{plancontract.PlanExecuteTerminalFieldFailureKind, summary.FailureKind},
		{plancontract.PlanExecuteTerminalFieldProgressPath, summary.ProgressPath},
	} {
		if value := strings.TrimSpace(item.value); value != "" {
			parts = append(parts, item.key+"="+value)
		}
	}

	if len(parts) == 0 {
		return "plan-execute returned without typed terminal fields"
	}

	return "plan-execute " + strings.Join(parts, " ")
}

func encodeFeaturePlannerSiblingDispatchPaused() string {
	body, _ := json.Marshal(map[string]any{
		"tool": featureplannercontract.ToolQuestionForPlanner,
		featureplannercontract.PlanExecuteCompletionFieldLaneFamily: string(lanecontract.LanePlanExecute),
		"question_summary": "sibling atom dispatch paused because an earlier implementor child opened a blocker in this feature-planner run",
		"blocking_claim":   "resolve the first blocked child session before dispatching additional sibling atoms",
	})

	return string(body)
}

func featurePlannerPlanExecuteOutputBlocks(answer string) bool {
	var envelope struct {
		Tool string `json:"tool"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(answer)), &envelope); err == nil && strings.TrimSpace(envelope.Tool) == string(featureplannercontract.ToolQuestionForPlanner) {
		return true
	}

	return planExecuteAnswerIsBlocked(answer)
}

func featurePlannerPlanExecuteCompletionLogType(payload string) featureplannercontract.NestedPlanExecuteCompletionEventName {
	var fields map[string]any

	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &fields); err != nil {
		return featureplannercontract.NestedPlanExecuteCompletionEventBlocked
	}

	return featureplanner.PlanExecuteCompletionEventTypeForFields(fields)
}

func overlayFeaturePlannerPlanExecuteRunResult(summary featureplanner.PlanExecuteCompletionSummary, root string, result *planexecute.RunResult) featureplanner.PlanExecuteCompletionSummary {
	if result == nil {
		return summary
	}

	if strings.TrimSpace(result.ResumeStatePath) != "" && strings.TrimSpace(summary.ProgressPath) == "" {
		summary.ProgressPath = strings.TrimSpace(result.ResumeStatePath)
	}

	if strings.TrimSpace(result.FailureKind) != "" && strings.TrimSpace(summary.FailureKind) == "" {
		summary.FailureKind = strings.TrimSpace(result.FailureKind)
	}

	if strings.TrimSpace(result.FailureReason) != "" && strings.TrimSpace(summary.FailureReason) == "" {
		summary.FailureReason = strings.TrimSpace(result.FailureReason)
	}

	if result.ExitCode != 0 {
		if summary.ExitCode == 0 {
			summary.ExitCode = result.ExitCode
		}
		if strings.TrimSpace(summary.Status) == "" {
			summary.Status = "failed"
		}
	}

	if result.Completion != nil {
		return overlayFeaturePlannerPlanExecuteTerminalSummary(summary, plancontract.PlanExecuteTerminalCompletionEventFields(*result.Completion, result.CompletionPath))
	}

	summary = overlayFeaturePlannerRecoverySidecarSummary(summary, root, strings.TrimSpace(result.SessionID))

	if strings.TrimSpace(root) == "" || (strings.TrimSpace(result.SessionID) == "" && strings.TrimSpace(result.ResumeStatePath) == "") {
		return summary
	}

	stateID := strings.TrimSpace(result.SessionID)
	if stateID == "" {
		stateID = strings.TrimSuffix(filepath.Base(filepath.ToSlash(strings.TrimSpace(result.ResumeStatePath))), ".json")
	}

	if record, completionPath, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, stateID); err == nil && found {
		summary = overlayFeaturePlannerPlanExecuteTerminalSummary(summary, plancontract.PlanExecuteTerminalCompletionEventFields(record, completionPath))
		summary = overlayFeaturePlannerRecoverySidecarSummary(summary, root, stateID)

		return summary
	}

	state, statePath, found, err := planexecute.TryLoadProgressState(root, stateID)
	if err != nil || !found {
		return summary
	}

	if record, ok := planexecute.PlanExecuteTerminalCompletionRecordFromProgressState(state, planexecute.PlanExecuteTerminalCompletionInput{ProgressPath: statePath}); ok {
		summary = overlayFeaturePlannerPlanExecuteTerminalSummary(summary, plancontract.PlanExecuteTerminalCompletionEventFields(record, ""))
	} else {
		progressFields := planexecute.ProgressStateEventFields(state, statePath)
		if result.ExitCode != 0 {
			progressFields[plancontract.PlanExecuteTerminalFieldExitCode] = result.ExitCode
		}
		summary = featureplanner.MergePlanExecuteCompletionSummaries(summary, featureplanner.PlanExecuteCompletionSummaryFromFields(progressFields))
	}

	return overlayFeaturePlannerRecoverySidecarSummary(summary, root, stateID)
}

func overlayFeaturePlannerRecoverySidecarSummary(summary featureplanner.PlanExecuteCompletionSummary, root string, sessionID string) featureplanner.PlanExecuteCompletionSummary {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(sessionID) == "" {
		return summary
	}

	record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
	if err != nil || !found {
		return summary
	}

	if strings.TrimSpace(record.FailureID) != "" && strings.TrimSpace(summary.FailureID) == "" {
		summary.FailureID = strings.TrimSpace(record.FailureID)
	}

	if strings.TrimSpace(recordPath) != "" && strings.TrimSpace(summary.RecordPath) == "" {
		summary.RecordPath = strings.TrimSpace(recordPath)
	}

	packetPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", strings.TrimSpace(record.FailureID), "packet.json"))
	if strings.TrimSpace(record.FailureID) != "" && strings.TrimSpace(summary.PacketPath) == "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packetPath))); err == nil {
			summary.PacketPath = packetPath
		}
	}

	if strings.TrimSpace(string(record.NextAction)) != "" && strings.TrimSpace(summary.NextAction) == "" {
		summary.NextAction = strings.TrimSpace(string(record.NextAction))
	}

	return summary
}

func overlayFeaturePlannerPlanExecuteTerminalSummary(summary featureplanner.PlanExecuteCompletionSummary, terminalFields map[string]any) featureplanner.PlanExecuteCompletionSummary {
	return featureplanner.MergePlanExecuteCompletionSummaries(summary, featureplanner.PlanExecuteCompletionSummaryFromFields(terminalFields))
}

func (m *Model) featurePlannerSessionID() string {
	stamp := time.Now().UTC().Format("20060102-150405000000")

	if m.debugLogPath != "" {
		base := filepath.Base(m.debugLogPath)
		ext := filepath.Ext(base)
		if sessionID, ok := sessionid.Normalize(strings.TrimSuffix(base, ext)); ok {
			candidate := sessionID + "-feature-planner-" + stamp
			if sessionid.Valid(candidate) {
				return candidate
			}
		}
	}

	return "tui-feature-planner-" + stamp
}

func (m *Model) logFeaturePlannerEvent(event featureplanner.Event) {
	event = sanitizeFeaturePlannerEventForConversation(event)

	body, err := json.Marshal(event)
	if err != nil {
		m.logFeaturePlannerInternalEvent(event.Type, event.Message)

		return
	}

	m.logFeaturePlannerInternalEvent(event.Type, string(body))
}

func (m *Model) logFeaturePlannerInternalEvent(role, content string) {
	if m == nil || m.debugLogger == nil {
		return
	}

	_ = m.debugLogger.LogRunEvent(strings.TrimSpace(role), content)
}

func sanitizeFeaturePlannerEventForConversation(event featureplanner.Event) featureplanner.Event {
	if !featurePlannerEventCarriesInternalDiagnostic(event.Type) {
		return event
	}

	originalFieldCount := len(event.Fields)
	event.Fields = featurePlannerConversationDiagnosticFields(event.Fields)
	omittedFieldCount := originalFieldCount - len(event.Fields)
	if strings.TrimSpace(event.Message) != "" {
		event.Fields["diagnostic_chars"] = len(event.Message)
		event.Message = "internal feature-planner diagnostic omitted from conversation; inspect the saved state or event log for full details"
	}
	if omittedFieldCount > 0 {
		event.Fields["diagnostic_fields_omitted"] = omittedFieldCount
	}

	return event
}

func featurePlannerEventCarriesInternalDiagnostic(eventType string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	if eventType == "" {
		return false
	}

	return strings.Contains(eventType, "_blocked") ||
		strings.Contains(eventType, "_rejected") ||
		strings.Contains(eventType, "malformed_structured_output")
}

func featurePlannerConversationDiagnosticFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		if featurePlannerConversationDiagnosticFieldAllowed(key) {
			out[key] = value
		}
	}

	return out
}

func featurePlannerConversationDiagnosticFieldAllowed(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "candidate_id", "rejected_at", "rejected_plan_sha256", "submitted_atom_count":
		return true
	default:
		return false
	}
}

func featurePlannerTUITerminalRole(status string) string {
	switch status {
	case featureplanner.FeatureStateComplete:
		return featureplannercontract.EventName(featureplannercontract.EventComplete)
	case featureplanner.FeatureStateRecoveryOpen:
		return featureplannercontract.EventName(featureplannercontract.EventRecoveryOpen)
	case featureplanner.FeatureStatePlanningQuestion:
		return featureplannercontract.EventName(featureplannercontract.EventQuestionOpen)
	case featureplanner.FeatureStatePlanningStuck:
		return featureplannercontract.EventName(featureplannercontract.EventPlanningStuck)
	case featureplanner.FeatureStateFailed:
		return featureplannercontract.EventName(featureplannercontract.EventFailed)
	default:
		return featureplannercontract.EventName(featureplannercontract.EventTerminal)
	}
}

func formatFeaturePlannerTUIAnswer(state featureplanner.FeatureState, statePath string) string {
	return formatFeaturePlannerTUIAnswerForRoot(state, statePath, "")
}

func formatFeaturePlannerTUIAnswerForRoot(state featureplanner.FeatureState, statePath string, workspaceRoot string) string {
	statePath = featurePlannerTUIDisplayPath(workspaceRoot, statePath)

	if answer, ok := formatFeaturePlannerTUIOperatorQuestion(state, statePath); ok {
		return answer
	}

	return featureplanner.FormatUserFacingStateSummary(state, statePath)
}

func featurePlannerTUIDisplayPath(workspaceRoot string, path string) string {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	path = strings.TrimSpace(path)
	if workspaceRoot == "" || path == "" || !filepath.IsAbs(path) {
		return path
	}

	return featurePlannerRelPath(workspaceRoot, path)
}

func formatFeaturePlannerTUIOperatorQuestion(state featureplanner.FeatureState, statePath string) (string, bool) {
	if featureplanner.CountOpenPlannerQuestions(state) == 0 {
		return "", false
	}

	explanation := featurePlannerApprovalExplanation(state)

	for _, question := range state.OpenQuestions {
		request, ok := greenfieldScopeAuthorizationFromPlannerQuestion(question, statePath)
		if !ok {
			continue
		}

		request.PlanPath = strings.TrimSpace(state.PlanPath)
		request.Explanation = explanation

		return renderGreenfieldWriteApproval(request), true
	}

	for _, question := range state.OpenQuestions {
		if featurePlannerTUIShouldRenderQuestion(question) {
			return renderFeaturePlannerQuestion(question, statePath), true
		}
	}

	return "", false
}

func featurePlannerTUIShouldRenderQuestion(question featureplanner.PlannerQuestion) bool {
	if !featurePlannerQuestionOpen(question) {
		return false
	}

	authority := strings.TrimSpace(question.AuthorityRequired)
	if authority == "" || authority == "operator_approval" {
		return true
	}

	return authority == "accepted_source" &&
		featurePlannerQuestionNeedsField(question, "source_localization") &&
		question.UserFacing
}

func featurePlannerApprovalExplanation(state featureplanner.FeatureState) string {
	plan, _, ok := featurePlannerPlanTabCandidate(state)
	if !ok {
		return ""
	}

	var b strings.Builder
	b.WriteString("What this proposed plan entails:\n")
	if objective := strings.TrimSpace(firstNonEmptyFeaturePlannerPlanText(plan.TopLevelScope.Objective, plan.Summary)); objective != "" {
		fmt.Fprintf(&b, "- Objective: %s\n", featurePlannerInlineMarkdown(objective))
	}
	if summary := strings.TrimSpace(plan.Summary); summary != "" && summary != strings.TrimSpace(plan.TopLevelScope.Objective) {
		fmt.Fprintf(&b, "- Summary: %s\n", featurePlannerInlineMarkdown(summary))
	}
	if criteria := featurePlannerUniqueStrings(plan.TopLevelScope.SuccessCriteria); len(criteria) > 0 {
		fmt.Fprintf(&b, "- Success criteria: %s\n", strings.Join(criteria, "; "))
	}
	if atomSummaries := featurePlannerApprovalAtomSummaries(plan.Atoms, 4); len(atomSummaries) > 0 {
		fmt.Fprintf(&b, "- Work breakdown: %s\n", strings.Join(atomSummaries, "; "))
	}
	if commands := featurePlannerApprovalVerificationCommands(plan, 0); len(commands) > 0 {
		fmt.Fprintf(&b, "- Proposed verification: %s\n", strings.Join(commands, "; "))
	}
	if featurePlannerApprovalQuestionMentionsEmptyWorkspace(state) {
		b.WriteString("- These are proposed files to create in the empty workspace, not existing source files.\n")
	} else {
		b.WriteString("- These are proposed missing paths to create only after approval; they are not authorized workspace edits yet.\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func featurePlannerApprovalQuestionMentionsEmptyWorkspace(state featureplanner.FeatureState) bool {
	for _, question := range state.OpenQuestions {
		if !featurePlannerQuestionOpen(question) {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(question.QuestionSummary + " " + question.BlockingClaim + " " + question.RequestedDecision))
		if strings.Contains(text, "empty workspace") || strings.Contains(text, "no project files") || strings.Contains(text, "greenfield") {
			return true
		}
	}

	return false
}

func featurePlannerApprovalAtomSummaries(atoms []featureplanner.FeatureAtomPlan, limit int) []string {
	var out []string
	for _, atom := range atoms {
		title := firstNonEmptyFeaturePlannerPlanText(atom.Title, atom.ID, atom.AtomID)
		if title == "" {
			continue
		}
		if summary := strings.TrimSpace(atom.Summary); summary != "" {
			title = title + " (" + featurePlannerInlineMarkdown(summary) + ")"
		}
		out = append(out, title)
		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func featurePlannerApprovalVerificationCommands(plan featureplanner.FeaturePlan, limit int) []string {
	var values []string
	for _, obligation := range plan.TDDContract.Obligations {
		if command := strings.TrimSpace(obligation.Command); command != "" {
			values = append(values, command)
		}
	}
	for _, atom := range plan.Atoms {
		values = append(values, atom.TestFirst.Commands...)
		values = append(values, atom.RequiredTests...)
	}

	values = featurePlannerUniqueStrings(values)
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}
