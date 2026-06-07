package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/featureplanner"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/planexecute"
	"idiom/internal/recovery"
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
	m.logMessage("idiom.feature_planner.start", objective)
	if productBrief != nil {
		m.logMessage("idiom.feature_planner.product_context", fmt.Sprintf("effort_id=%s effort_root=%s", productBrief.EffortID, effortRoot))
		if productBrief.IsClosedSourceSatisfied() {
			return m.closeFeaturePlannerTUISourceSatisfied(state, *productBrief)
		}
	}

	writeEnvelope := featurePlannerProductWriteEnvelope(productBrief)
	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                objective,
		MaxPlanningIterations: 3,
		Now:                   time.Now().UTC(),
		ProductWorkflowBrief:  productBrief,
		ReconcileActiveRuns:   m.reconcileFeaturePlannerActivePlanExecuteRuns,
		WorkspaceRoot:         m.projectRoot(),
	}, func(prompt string, taskFamily string) (string, error) {
		if workerruntime.TaskFamilyIsFeaturePlannerImplementor(taskFamily) {
			return m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, writeEnvelope)
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
	m.logMessage(featurePlannerTUITerminalRole(result.State.Status), answer)
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
	m.logMessage(featurePlannerTUITerminalRole(state.Status), answer)
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

func (m *Model) runFeaturePlannerTUITurn(ctx context.Context, prompt string, taskFamily string) (string, error) {
	m.logMessage("feature_planner_prompt", prompt)
	opts := m.chatOptions()
	thinking := false
	opts.EnableThinking = &thinking
	opts.ResponseFormatJSON = workerruntime.TaskFamilyIsFeaturePlannerPlanner(taskFamily)
	opts.MaxTokens = 16384
	answer, err := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", err
	}
	m.logMessage("feature_planner_model", answer)
	return answer, nil
}

func (m *Model) runFeaturePlannerAtomPlanExecute(ctx context.Context, prompt string) (string, error) {
	return m.runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx, prompt, nil)
}

func (m *Model) runFeaturePlannerAtomPlanExecuteWithWriteEnvelope(ctx context.Context, prompt string, writeEnvelope []string) (string, error) {
	m.logFeaturePlannerPlanExecuteGitHistoryRequirement()

	priorSuppressTranscript := m.suppressPromptTranscript
	priorWriteEnvelope := append([]string(nil), m.planExecuteWriteEnvelope...)
	m.suppressPromptTranscript = true
	m.planExecuteWriteEnvelope = appendUniqueBriefStrings(nil, writeEnvelope...)
	defer func() {
		m.suppressPromptTranscript = priorSuppressTranscript
		m.planExecuteWriteEnvelope = priorWriteEnvelope
	}()

	answer, err := m.runPlanExecuteCommandOnce(ctx, prompt)
	if err != nil {
		if m.lastPlanExecuteRunResult == nil {
			return "", err
		}
		answer = err.Error()
	}
	completion, err := m.encodeFeaturePlannerPlanExecuteCompletion(answer)
	if err != nil {
		return "", err
	}
	m.logMessage(featurePlannerPlanExecuteCompletionLogType(completion), completion)
	return completion, nil
}

func featurePlannerProductWriteEnvelope(brief *featureplanner.ProductWorkflowBrief) []string {
	if brief == nil {
		return nil
	}
	var paths []string
	paths = appendUniqueBriefStrings(paths, brief.FilesToModify...)
	paths = appendUniqueBriefStrings(paths, brief.ContextEvidencePaths...)
	paths = appendUniqueBriefStrings(paths, brief.AuthorizedNewPackageRoots...)
	return paths
}

const featurePlannerPlanExecuteGitHistoryRequirementRole = "idiom.feature_planner.plan_execute_git_history_requirement"

func (m *Model) logFeaturePlannerPlanExecuteGitHistoryRequirement() {
	m.logMessage(featurePlannerPlanExecuteGitHistoryRequirementRole, featurePlannerPlanExecuteGitHistoryRequirementPayload())
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
	fields := featureplanner.PlanExecuteCompletionFields(summary)
	if m != nil {
		overlayFeaturePlannerPlanExecuteRunResult(fields, m.cfg.Workspace.Root, m.lastPlanExecuteRunResult)
	}
	fields["tool"] = featureplanner.ToolPlanExecuteComplete
	fields["lane_family"] = "plan-execute"
	if featureplanner.CompletionFieldString(fields, "status") == "" {
		fields["status"] = "ambiguous"
		fields["failure_kind"] = featureplanner.FirstNonEmptyString(featureplanner.CompletionFieldString(fields, "failure_kind"), "missing_nested_plan_execute_status")
		fields["failure_reason"] = featureplanner.FirstNonEmptyString(featureplanner.CompletionFieldString(fields, "failure_reason"), "nested plan-execute returned without typed completion status")
	}
	if featureplanner.CompletionFieldString(fields, "completion_source") == "" {
		fields["completion_source"] = "tui_plan_execute_returned"
	}
	fields["summary"] = featurePlannerPlanExecuteCompletionSummary(fields)
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
	overlayFeaturePlannerRecoverySidecarFields(fields, root, strings.TrimSpace(result.SessionID))
	if strings.TrimSpace(root) == "" || (strings.TrimSpace(result.SessionID) == "" && strings.TrimSpace(result.ResumeStatePath) == "") {
		return
	}
	stateID := strings.TrimSpace(result.SessionID)
	if stateID == "" {
		stateID = strings.TrimSuffix(filepath.Base(filepath.ToSlash(strings.TrimSpace(result.ResumeStatePath))), ".json")
	}
	state, statePath, found, err := planexecute.TryLoadProgressState(root, stateID)
	if err != nil || !found {
		return
	}
	progressFields := planexecute.ProgressStateEventFields(state, statePath)
	overlayFeaturePlannerPlanExecuteProgressFields(fields, progressFields)
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
	for _, key := range []string{"required_edit_paths", "allowed_edit_paths", "changed_paths"} {
		if value, ok := progressFields[key]; ok {
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
	body, err := json.Marshal(event)
	if err != nil {
		m.logMessage(event.Type, event.Message)
		return
	}
	m.logMessage(event.Type, string(body))
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
	var b strings.Builder
	fmt.Fprintf(&b, "feature-planner: %s\n", state.Status)
	if statePath != "" {
		fmt.Fprintf(&b, "state: %s\n", statePath)
	}
	if len(state.Atoms) > 0 {
		fmt.Fprintf(&b, "atoms: %d\n", len(state.Atoms))
		var atomIDs []string
		for _, atom := range state.Atoms {
			atomIDs = append(atomIDs, atom.ID)
		}
		fmt.Fprintf(&b, "atom_ids: %s\n", strings.Join(atomIDs, ", "))
	}
	if sddRefs := featureplanner.SDDRefsForSummary(state.Plan); len(sddRefs) > 0 {
		fmt.Fprintf(&b, "sdd_refs: %s\n", strings.Join(sddRefs, ", "))
	}
	if len(state.Plan.TDDContract.Obligations) > 0 {
		var tddIDs []string
		for _, obligation := range state.Plan.TDDContract.Obligations {
			tddIDs = append(tddIDs, obligation.ID)
		}
		fmt.Fprintf(&b, "tdd_obligations: %s\n", strings.Join(tddIDs, ", "))
	}
	if len(state.Blockers) > 0 {
		fmt.Fprintf(&b, "blockers: %s\n", strings.Join(state.Blockers, "; "))
	}
	if len(state.DerivedNextValidActions) > 0 {
		fmt.Fprintf(&b, "next: %s\n", strings.Join(state.DerivedNextValidActions, ", "))
	}
	return strings.TrimSpace(b.String())
}
