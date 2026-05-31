package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/featureplanner"
	"idiom/internal/llama"
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
	sessionID := m.featurePlannerSessionID()
	state := featureplanner.NewState(sessionID, objective, "", 3, time.Now().UTC())
	operatorCommand := "/feature-planner " + objective
	m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: operatorCommand})
	m.logMessage("user", operatorCommand)
	m.logMessage("route_plan", "initial=feature-planner reason=typed_feature_planner_command route_source=typed_command")
	m.logMessage("idiom.feature_planner.start", objective)

	implementorBlocked := false
	result, err := featureplanner.Run(state, featureplanner.RunOptions{
		Prompt:                objective,
		MaxPlanningIterations: 3,
		Now:                   time.Now().UTC(),
	}, func(prompt string, taskFamily string) (string, error) {
		if taskFamily == "feature_planner_implementor" {
			if implementorBlocked {
				return encodeFeaturePlannerSiblingDispatchPaused(), nil
			}
			answer, runErr := m.runFeaturePlannerAtomPlanExecute(ctx, prompt)
			if runErr != nil || featurePlannerPlanExecuteOutputBlocks(answer) {
				implementorBlocked = true
			}
			return answer, runErr
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

func (m *Model) runFeaturePlannerTUITurn(ctx context.Context, prompt string, taskFamily string) (string, error) {
	m.logMessage("feature_planner_prompt", prompt)
	opts := m.chatOptions()
	thinking := false
	opts.EnableThinking = &thinking
	opts.ResponseFormatJSON = taskFamily == "feature_planner_planner"
	opts.MaxTokens = 16384
	answer, err := m.client.CompleteChat(ctx, []llama.ChatMessage{{Role: "user", Content: prompt}}, opts)
	if err != nil {
		return "", err
	}
	m.logMessage("feature_planner_model", answer)
	return answer, nil
}

func (m *Model) runFeaturePlannerAtomPlanExecute(ctx context.Context, prompt string) (string, error) {
	if featureplanner.PlanExecuteGitHistoryRequirementPresent(prompt) {
		if body, err := json.Marshal(featureplanner.PlanExecuteGitHistoryRequirementFields()); err == nil {
			m.logMessage(featureplanner.EventPlanExecuteGitHistoryRequirement, string(body))
		}
	}
	priorSuppressTranscript := m.suppressPromptTranscript
	m.suppressPromptTranscript = true
	defer func() { m.suppressPromptTranscript = priorSuppressTranscript }()

	answer, err := m.runPlanExecuteCommandOnce(ctx, prompt)
	if err != nil {
		if isRetryableModelTransportFailure(err) {
			body, _ := json.Marshal(map[string]any{
				"tool":             featureplanner.ToolQuestionForPlanner,
				"lane_family":      "plan-execute",
				"question_summary": "retryable model transport failure; previous product artifacts are preserved and the same atom can be retried",
				"blocking_claim":   err.Error(),
			})
			m.logMessage("idiom.plan_execute.blocked", string(body))
			return string(body), nil
		}
		return "", err
	}
	if planExecuteAnswerIsBlocked(answer) {
		summary := planExecuteBlockedQuestionSummary(answer)
		body, _ := json.Marshal(map[string]any{
			"tool":             featureplanner.ToolQuestionForPlanner,
			"lane_family":      "plan-execute",
			"question_summary": summary,
			"blocking_claim":   "plan-execute did not complete the atom implementation contract",
		})
		m.logMessage("idiom.plan_execute.blocked", string(body))
		return string(body), nil
	}
	if body, err := json.Marshal(map[string]any{
		"tool":          featureplanner.ToolPlanExecuteComplete,
		"lane_family":   "plan-execute",
		"progress_path": planExecuteProgressStatePath(answer),
		"summary":       planExecuteCompletionSummary(answer),
	}); err == nil {
		m.logMessage("idiom.plan_execute.completed", string(body))
		return string(body), nil
	}
	return encodeFeaturePlannerPlanExecuteCompletion(answer)
}

func planExecuteBlockedQuestionSummary(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:"))
	}
	var fields []string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "session_id:") ||
			strings.HasPrefix(lower, "progress_state:") ||
			strings.HasPrefix(lower, "failure_kind:") ||
			strings.HasPrefix(lower, "required_edit_paths:") ||
			strings.HasPrefix(lower, "reason:") ||
			strings.HasPrefix(lower, "plan-execute:") {
			fields = append(fields, line)
		}
	}
	if len(fields) > 0 {
		return strings.Join(fields, "; ")
	}
	return trimmed
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

func planExecuteCompletionSummary(answer string) string {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return "plan-execute completed"
	}
	var lines []string
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "progress_state:") ||
			strings.HasPrefix(lower, "session_id:") {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= 3 {
			break
		}
	}
	if len(lines) == 0 {
		return "plan-execute completed"
	}
	return strings.Join(lines, " ")
}

func planExecuteProgressStatePath(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "progress_state:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func encodeFeaturePlannerPlanExecuteCompletion(summary string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"tool":        featureplanner.ToolPlanExecuteComplete,
		"lane_family": "plan-execute",
		"summary":     strings.TrimSpace(summary),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
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
	sddRefs := append([]string{}, state.Plan.SDDContract.Refs...)
	sddRefs = append(sddRefs, state.Plan.SDDContract.PRDRequirementRefs...)
	sddRefs = append(sddRefs, state.Plan.SDDContract.ADRRefs...)
	sddRefs = appendUniqueBriefStrings(nil, sddRefs...)
	if len(sddRefs) > 0 {
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
