package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/platform/util"
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
	m.logFeaturePlannerInternalEvent("idiom.feature_planner.start", objective)

	if productBrief != nil {
		m.logFeaturePlannerInternalEvent("idiom.feature_planner.product_context", fmt.Sprintf("effort_id=%s effort_root=%s", productBrief.EffortID, effortRoot))

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

	statePath, err := featureplanner.SaveState(m.projectRoot(), &result.State)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	answer := formatFeaturePlannerTUIAnswer(result.State, statePath)
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

	statePath, err := featureplanner.SaveState(m.projectRoot(), &state)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	answer := formatFeaturePlannerTUIAnswer(state, statePath)
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

	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                directFeaturePlannerResumePrompt(state, answer),
		MaxPlanningIterations: 3,
		ResumeAnswer:          answer,
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

	statePath, err := featureplanner.SaveState(m.projectRoot(), &result.State)
	if err != nil {
		m.emitJSONError("run_failed", err.Error())

		return "", err
	}

	if effortRoot != "" {
		m.featurePlannerEffortRoot = effortRoot
	}

	reply := formatFeaturePlannerTUIAnswer(result.State, statePath)
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

	return prompt
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

	return featureplanner.CountOpenPlannerQuestions(state) > 0 || directFeaturePlannerStateNeedsRepairInstruction(state)
}

func directFeaturePlannerStateNeedsRepairInstruction(state featureplanner.FeatureState) bool {
	switch state.Status {
	case featureplanner.FeatureStatePlanningRepair, featureplanner.FeatureStateStaleEditAnchor:
		return true
	default:
		return false
	}
}

func (m *Model) updateDirectFeaturePlannerSession(state featureplanner.FeatureState) {
	switch state.Status {
	case featureplanner.FeatureStatePlanningQuestion, featureplanner.FeatureStatePlanningRepair, featureplanner.FeatureStateStaleEditAnchor, featureplanner.FeatureStateDispatching:
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

	fields := m.featurePlannerPlanExecuteCompletionFields(answer)
	if retryPrompt, ok := featureplanner.PlanExecuteRecoveryRetryPrompt(prompt, fields); ok {
		answer, err = runOnce(ctx, retryPrompt)
		if err != nil {
			if m.lastPlanExecuteRunResult == nil {
				return "", err
			}

			answer = err.Error()
		}

		fields = m.featurePlannerPlanExecuteCompletionFields(answer)
	}

	completion, err := encodeFeaturePlannerPlanExecuteCompletionFields(fields)
	if err != nil {
		return "", err
	}

	m.logFeaturePlannerInternalEvent(featurePlannerPlanExecuteCompletionLogType(completion), completion)

	return completion, nil
}

const featurePlannerPlanExecuteGitHistoryRequirementRole = "idiom.feature_planner.plan_execute_git_history_requirement"

func (m *Model) logFeaturePlannerPlanExecuteGitHistoryRequirement() {
	m.logFeaturePlannerInternalEvent(featurePlannerPlanExecuteGitHistoryRequirementRole, featurePlannerPlanExecuteGitHistoryRequirementPayload())
}

func featurePlannerPlanExecuteGitHistoryRequirementPayload() string {
	body, _ := json.Marshal(map[string]any{
		"tool":                         "git_history_requirement",
		"lane_family":                  "plan-execute",
		"commit_descriptions_required": true,
		"evidence_tools":               []string{"history_range", "history_symbol"},
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
	return encodeFeaturePlannerPlanExecuteCompletionFields(m.featurePlannerPlanExecuteCompletionFields(summary))
}

func (m *Model) featurePlannerPlanExecuteCompletionFields(summary string) map[string]any {
	fields := featureplanner.PlanExecuteCompletionFields(summary)
	if m != nil {
		overlayFeaturePlannerPlanExecuteRunResult(fields, m.cfg.Workspace.Root, m.lastPlanExecuteRunResult)
	}

	fields["tool"] = featureplanner.ToolPlanExecuteComplete
	fields["lane_family"] = "plan-execute"

	if featureplanner.CompletionFieldString(fields, "status") == "" {
		fields["status"] = "ambiguous"
		fields["failure_kind"] = util.FirstNonEmpty(featureplanner.CompletionFieldString(fields, "failure_kind"), "missing_nested_plan_execute_status")
		fields["failure_reason"] = util.FirstNonEmpty(featureplanner.CompletionFieldString(fields, "failure_reason"), "nested plan-execute returned without typed completion status")
	}

	if featureplanner.CompletionFieldString(fields, "completion_source") == "" {
		fields["completion_source"] = "tui_plan_execute_returned"
	}

	fields["summary"] = featurePlannerPlanExecuteCompletionSummary(fields)
	featureplanner.TrustObservedPlanExecuteCompletionFields(fields)

	return fields
}

func encodeFeaturePlannerPlanExecuteCompletionFields(fields map[string]any) (string, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func featurePlannerPlanExecuteCompletionSummary(fields map[string]any) string {
	var parts []string

	for _, key := range []string{"status", "phase", "review_status", "failure_kind", "progress_path"} {
		if value := featureplanner.CompletionFieldString(fields, key); value != "" {
			parts = append(parts, key+"="+value)
		}
	}

	if len(parts) == 0 {
		return "plan-execute returned without typed terminal fields"
	}

	return "plan-execute " + strings.Join(parts, " ")
}

func encodeFeaturePlannerSiblingDispatchPaused() string {
	body, _ := json.Marshal(map[string]any{
		"tool":             featureplanner.ToolQuestionForPlanner,
		"lane_family":      "plan-execute",
		"question_summary": "sibling atom dispatch paused because an earlier implementor child opened a blocker in this feature-planner run",
		"blocking_claim":   "resolve the first blocked child session before dispatching additional sibling atoms",
	})

	return string(body)
}

func featurePlannerPlanExecuteOutputBlocks(answer string) bool {
	var envelope struct {
		Tool string `json:"tool"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(answer)), &envelope); err == nil && strings.TrimSpace(envelope.Tool) == featureplanner.ToolQuestionForPlanner {
		return true
	}

	return planExecuteAnswerIsBlocked(answer)
}

func featurePlannerPlanExecuteCompletionLogType(payload string) string {
	var fields map[string]any

	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &fields); err != nil {
		return "idiom.plan_execute.blocked"
	}

	if featurePlannerPlanExecuteLogStringField(fields, "tool") != featureplanner.ToolPlanExecuteComplete {
		return "idiom.plan_execute.blocked"
	}

	switch featureplanner.PlanExecuteCompletionTerminalClass(fields) {
	case featureplanner.PlanExecuteTerminalCompleted:
		return "idiom.plan_execute.completed"
	default:
		return "idiom.plan_execute.blocked"
	}
}

func featurePlannerPlanExecuteLogStringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprint(value))
}

func overlayFeaturePlannerPlanExecuteRunResult(fields map[string]any, root string, result *planexecute.RunResult) {
	if fields == nil || result == nil {
		return
	}

	if strings.TrimSpace(result.ResumeStatePath) != "" && featureplanner.CompletionFieldString(fields, "progress_path") == "" {
		fields["progress_path"] = strings.TrimSpace(result.ResumeStatePath)
	}

	if strings.TrimSpace(result.FailureKind) != "" && featureplanner.CompletionFieldString(fields, "failure_kind") == "" {
		fields["failure_kind"] = strings.TrimSpace(result.FailureKind)
	}

	if strings.TrimSpace(result.FailureReason) != "" && featureplanner.CompletionFieldString(fields, "failure_reason") == "" {
		fields["failure_reason"] = strings.TrimSpace(result.FailureReason)
	}

	if result.ExitCode != 0 && featureplanner.CompletionFieldString(fields, "status") == "" {
		fields["status"] = "failed"
	}

	if result.Completion != nil {
		overlayFeaturePlannerPlanExecuteTerminalFields(fields, planexecute.PlanExecuteTerminalCompletionEventFields(*result.Completion, result.CompletionPath))

		return
	}

	overlayFeaturePlannerRecoverySidecarFields(fields, root, strings.TrimSpace(result.SessionID))

	if strings.TrimSpace(root) == "" || (strings.TrimSpace(result.SessionID) == "" && strings.TrimSpace(result.ResumeStatePath) == "") {
		return
	}

	stateID := strings.TrimSpace(result.SessionID)
	if stateID == "" {
		stateID = strings.TrimSuffix(filepath.Base(filepath.ToSlash(strings.TrimSpace(result.ResumeStatePath))), ".json")
	}

	if record, completionPath, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, stateID); err == nil && found {
		overlayFeaturePlannerPlanExecuteTerminalFields(fields, planexecute.PlanExecuteTerminalCompletionEventFields(record, completionPath))
		overlayFeaturePlannerRecoverySidecarFields(fields, root, stateID)

		return
	}

	state, statePath, found, err := planexecute.TryLoadProgressState(root, stateID)
	if err != nil || !found {
		return
	}

	if record, ok := planexecute.PlanExecuteTerminalCompletionRecordFromProgressState(state, planexecute.PlanExecuteTerminalCompletionInput{ProgressPath: statePath}); ok {
		overlayFeaturePlannerPlanExecuteTerminalFields(fields, planexecute.PlanExecuteTerminalCompletionEventFields(record, ""))
	} else {
		progressFields := planexecute.ProgressStateEventFields(state, statePath)
		overlayFeaturePlannerPlanExecuteProgressFields(fields, progressFields)
	}

	overlayFeaturePlannerRecoverySidecarFields(fields, root, stateID)
}

func overlayFeaturePlannerRecoverySidecarFields(fields map[string]any, root string, sessionID string) {
	if fields == nil || strings.TrimSpace(root) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}

	record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
	if err != nil || !found {
		return
	}

	if strings.TrimSpace(record.FailureID) != "" && featureplanner.CompletionFieldString(fields, "failure_id") == "" {
		fields["failure_id"] = strings.TrimSpace(record.FailureID)
	}

	if strings.TrimSpace(recordPath) != "" && featureplanner.CompletionFieldString(fields, "record_path") == "" {
		fields["record_path"] = strings.TrimSpace(recordPath)
	}

	packetPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", strings.TrimSpace(record.FailureID), "packet.json"))
	if strings.TrimSpace(record.FailureID) != "" && featureplanner.CompletionFieldString(fields, "packet_path") == "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(packetPath))); err == nil {
			fields["packet_path"] = packetPath
		}
	}

	if strings.TrimSpace(record.NextAction) != "" && featureplanner.CompletionFieldString(fields, "next_valid_action") == "" {
		fields["next_valid_action"] = strings.TrimSpace(record.NextAction)
	}
}

func overlayFeaturePlannerPlanExecuteProgressFields(fields map[string]any, progressFields map[string]any) {
	featureplanner.OverlayPlanExecuteCompletionFields(fields, progressFields)

	for _, key := range []string{"required_edit_paths", "allowed_edit_paths", "changed_paths", "source_sanity"} {
		if value, ok := progressFields[key]; ok {
			fields[key] = value
		}
	}
}

func overlayFeaturePlannerPlanExecuteTerminalFields(fields map[string]any, terminalFields map[string]any) {
	featureplanner.OverlayPlanExecuteCompletionFields(fields, terminalFields)

	for _, key := range []string{"required_edit_paths", "allowed_edit_paths", "write_allowed_paths", "changed_paths", "source_sanity", "source_satisfied_evidence_paths", "source_satisfied_evidence", "contract_hash", "completion_path"} {
		if value, ok := terminalFields[key]; ok {
			fields[key] = value
		}
	}
}

func (m *Model) featurePlannerSessionID() string {
	stamp := time.Now().UTC().Format("20060102-150405.000000")

	if m.debugLogPath != "" {
		base := filepath.Base(m.debugLogPath)
		ext := filepath.Ext(base)
		sessionID := strings.TrimSuffix(base, ext)

		if sessionID != "" {
			return sessionID + "-feature-planner-" + stamp
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
		return "idiom.feature_planner.complete"
	case featureplanner.FeatureStateRecoveryOpen:
		return "idiom.feature_planner.recovery_open"
	case featureplanner.FeatureStatePlanningQuestion:
		return "idiom.feature_planner.question_open"
	case featureplanner.FeatureStatePlanningStuck:
		return "idiom.feature_planner.planning_stuck"
	case featureplanner.FeatureStateFailed:
		return "idiom.feature_planner.failed"
	default:
		return "idiom.feature_planner.terminal"
	}
}

func formatFeaturePlannerTUIAnswer(state featureplanner.FeatureState, statePath string) string {
	if answer, ok := formatFeaturePlannerTUIOperatorQuestion(state, statePath); ok {
		return answer
	}

	return featureplanner.FormatStateSummary(state, statePath)
}

func formatFeaturePlannerTUIOperatorQuestion(state featureplanner.FeatureState, statePath string) (string, bool) {
	if featureplanner.CountOpenPlannerQuestions(state) == 0 {
		return "", false
	}

	for _, question := range state.OpenQuestions {
		request, ok := greenfieldScopeAuthorizationFromPlannerQuestion(question, statePath)
		if !ok {
			continue
		}

		return renderGreenfieldWriteApproval(request), true
	}

	return "", false
}
