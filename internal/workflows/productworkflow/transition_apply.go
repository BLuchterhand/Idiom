package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ProductWorkflowTransitionApplyOptions struct {
	EffortRoot            string
	Action                string
	Actor                 string
	UpdatedAt             string
	EventID               string
	ExpectedStateRevision int
}

type ProductWorkflowTransitionApplyResult struct {
	Status               string                             `json:"status"`
	EffortRoot           string                             `json:"effort_root"`
	Action               string                             `json:"action"`
	FromState            string                             `json:"from_state"`
	ToState              string                             `json:"to_state,omitempty"`
	UpdatedWorkflowState WorkflowState                      `json:"updated_workflow_state,omitempty"`
	Event                ProductWorkflowTransitionEvent     `json:"event,omitempty"`
	Blockers             []ProductWorkflowTransitionBlocker `json:"blockers"`
	NextValidAction      string                             `json:"next_valid_action"`
}

type ProductWorkflowTransitionBlocker struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type ProductWorkflowTransitionEvent struct {
	Event     string                               `json:"event"`
	EventID   string                               `json:"event_id"`
	EffortID  string                               `json:"effort_id"`
	TaskID    string                               `json:"task_id"`
	Timestamp string                               `json:"timestamp"`
	Source    string                               `json:"source"`
	Actor     string                               `json:"actor,omitempty"`
	Action    string                               `json:"action"`
	Details   ProductWorkflowTransitionEventDetail `json:"details"`
}

type ProductWorkflowTransitionEventDetail struct {
	FromState     string `json:"from_state"`
	ToState       string `json:"to_state"`
	StateRevision int    `json:"state_revision"`
}

func ApplyProductWorkflowTransition(options ProductWorkflowTransitionApplyOptions) ProductWorkflowTransitionApplyResult {
	result := ProductWorkflowTransitionApplyResult{
		Status:          "blocked",
		EffortRoot:      strings.TrimSpace(options.EffortRoot),
		Action:          strings.TrimSpace(options.Action),
		Blockers:        []ProductWorkflowTransitionBlocker{},
		NextValidAction: "resolve product workflow transition blockers before continuing",
	}
	if result.EffortRoot == "" {
		result.addBlocker("missing_effort_root", "", "effort root is required")
		return result
	}
	if result.Action == "" {
		result.addBlocker("missing_action", "", "transition action is required")
		return result
	}
	absRoot, err := filepath.Abs(result.EffortRoot)
	if err != nil {
		result.addBlocker("resolve_effort_root_error", "", err.Error())
		return result
	}
	result.EffortRoot = filepath.Clean(absRoot)

	statusView, workflowState, statusErr := LoadCheckedProductStatus(result.EffortRoot)
	if statusErr != nil {
		result.addBlocker("product_status_error", "workflow_state.json", statusErr.Error())
		return result
	}
	if workflowState == nil {
		result.addBlocker("missing_workflow_state", "workflow_state.json", "workflow_state.json not loaded")
		return result
	}
	result.FromState = workflowState.CurrentState
	operatorStatus := BuildProductOperatorStatusView(statusView, workflowState)
	for _, blocker := range operatorStatus.Blockers {
		result.addBlocker(blocker.Code, blocker.Source, blocker.Message)
	}
	if len(result.Blockers) > 0 {
		return result
	}
	if !stringSliceContains(workflowState.NextValidActions, result.Action) {
		result.addBlocker("action_not_listed", "workflow_state.next_valid_actions", fmt.Sprintf("action %q is not listed as a next_valid_action", result.Action))
		return result
	}

	transition := EvaluateTransition(TransitionInput{
		StatusBucket:    workflowState.StatusBucket,
		CurrentState:    workflowState.CurrentState,
		RequestedAction: result.Action,
		OpenBlockers:    workflowState.Blockers,
		OpenObligations: workflowState.OpenObligations,
		GateResults:     workflowState.Coverage.GateResults,
	})
	for _, blocker := range transition.Blockers {
		result.addBlocker(blocker.Code, "workflow_transition", blocker.Message)
	}
	if len(result.Blockers) > 0 {
		return result
	}
	if strings.TrimSpace(transition.ToState) == "" {
		result.addBlocker("empty_transition_target", "workflow_transition", fmt.Sprintf("action %q did not produce a target state", result.Action))
		return result
	}
	if options.ExpectedStateRevision > 0 && workflowState.StateRevision != options.ExpectedStateRevision {
		result.addBlocker("stale_workflow_state_revision", "workflow_state.json", fmt.Sprintf("workflow state revision %d does not match expected revision %d", workflowState.StateRevision, options.ExpectedStateRevision))
		return result
	}

	updatedAt := strings.TrimSpace(options.UpdatedAt)
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	eventID := strings.TrimSpace(options.EventID)
	if eventID == "" {
		eventID = "evt-product-transition-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	updated := *workflowState
	updated.CurrentState = transition.ToState
	updated.CurrentPhase = productWorkflowTransitionPhase(updated.CurrentPhase, transition.ToState)
	updated.UpdatedAt = updatedAt
	updated.StateRevision++
	updated.LastEventID = eventID
	updated.NextValidActions = transitionActionNames(nextActionsFor(transition.ToState, updated.Coverage.GateResults))

	event := ProductWorkflowTransitionEvent{
		Event:     "product_workflow_transition_applied",
		EventID:   eventID,
		EffortID:  updated.EffortID,
		TaskID:    updated.TaskID,
		Timestamp: updatedAt,
		Source:    "product_workflow_transition_apply",
		Actor:     strings.TrimSpace(options.Actor),
		Action:    result.Action,
		Details: ProductWorkflowTransitionEventDetail{
			FromState:     workflowState.CurrentState,
			ToState:       updated.CurrentState,
			StateRevision: updated.StateRevision,
		},
	}
	if err := writeProductWorkflowTransition(result.EffortRoot, updated, event); err != nil {
		result.addBlocker("transition_write_error", "workflow_state.json", err.Error())
		return result
	}
	result.Status = "applied"
	result.ToState = updated.CurrentState
	result.UpdatedWorkflowState = updated
	result.Event = event
	result.NextValidAction = firstTransitionAction(updated.NextValidActions)
	return result
}

func (r *ProductWorkflowTransitionApplyResult) addBlocker(code, path, message string) {
	r.Blockers = append(r.Blockers, ProductWorkflowTransitionBlocker{
		Code:    strings.TrimSpace(code),
		Path:    strings.TrimSpace(path),
		Message: strings.TrimSpace(message),
	})
}

func productWorkflowTransitionPhase(currentPhase, toState string) string {
	switch strings.TrimSpace(toState) {
	case "human_verification":
		return "phase-human-verification"
	case "implementing":
		return "phase-implementation"
	default:
		if strings.TrimSpace(currentPhase) != "" {
			return currentPhase
		}
		return "phase-" + strings.ReplaceAll(strings.TrimSpace(toState), "_", "-")
	}
}

func firstTransitionAction(actions []string) string {
	for _, action := range actions {
		if strings.TrimSpace(action) != "" {
			return strings.TrimSpace(action)
		}
	}
	return ""
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func writeProductWorkflowTransition(absRoot string, state WorkflowState, event ProductWorkflowTransitionEvent) error {
	workflowPath := filepath.Join(absRoot, "workflow_state.json")
	originalWorkflowBytes, err := os.ReadFile(workflowPath)
	if err != nil {
		return fmt.Errorf("read workflow_state.json: %w", err)
	}
	if err := rejectTransitionSymlink(workflowPath); err != nil {
		return err
	}
	workflowBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workflow_state.json: %w", err)
	}
	workflowBytes = append(workflowBytes, '\n')

	var eventsPath string
	var eventsBytes []byte
	var originalEventsBytes []byte
	eventsExisted := false
	if relEventsPath := strings.TrimSpace(state.ArtifactPaths["events"]); relEventsPath != "" {
		rel, ok := normalizedSafeEffortPath(relEventsPath)
		if !ok {
			return fmt.Errorf("workflow_state artifact_paths.events must be a safe relative path")
		}
		eventsPath = filepath.Join(absRoot, filepath.FromSlash(rel))
		if err := rejectTransitionSymlinkParent(absRoot, eventsPath); err != nil {
			return err
		}
		if err := rejectTransitionSymlink(eventsPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(eventsPath), 0o755); err != nil {
			return fmt.Errorf("prepare events parent: %w", err)
		}
		if data, err := os.ReadFile(eventsPath); err == nil {
			eventsBytes = data
			originalEventsBytes = append([]byte(nil), data...)
			eventsExisted = true
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("read events artifact: %w", err)
		}
		eventLine, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode transition event: %w", err)
		}
		if len(eventsBytes) > 0 && eventsBytes[len(eventsBytes)-1] != '\n' {
			eventsBytes = append(eventsBytes, '\n')
		}
		eventsBytes = append(eventsBytes, eventLine...)
		eventsBytes = append(eventsBytes, '\n')
	}

	workflowTemp := workflowPath + ".product-transition.tmp"
	if err := rejectTransitionTempPath(workflowTemp); err != nil {
		return err
	}
	if err := os.WriteFile(workflowTemp, workflowBytes, 0o644); err != nil {
		return fmt.Errorf("stage workflow_state.json: %w", err)
	}
	var eventsTemp string
	if eventsPath != "" {
		eventsTemp = eventsPath + ".product-transition.tmp"
		if err := rejectTransitionTempPath(eventsTemp); err != nil {
			_ = os.Remove(workflowTemp)
			return err
		}
		if err := os.WriteFile(eventsTemp, eventsBytes, 0o644); err != nil {
			_ = os.Remove(workflowTemp)
			return fmt.Errorf("stage events artifact: %w", err)
		}
	}
	if err := os.Rename(workflowTemp, workflowPath); err != nil {
		_ = os.Remove(workflowTemp)
		_ = os.Remove(eventsTemp)
		return fmt.Errorf("replace workflow_state.json: %w", err)
	}
	if eventsTemp != "" {
		if err := os.Rename(eventsTemp, eventsPath); err != nil {
			_ = os.Remove(eventsTemp)
			if rollbackErr := rollbackProductWorkflowTransition(workflowPath, originalWorkflowBytes, eventsPath, originalEventsBytes, eventsExisted); rollbackErr != nil {
				return fmt.Errorf("replace events artifact: %w (rollback error: %v)", err, rollbackErr)
			}
			return fmt.Errorf("replace events artifact: %w", err)
		}
	}
	return nil
}

func rejectTransitionTempPath(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", filepath.Base(path))
	}
	return fmt.Errorf("%s already exists", filepath.Base(path))
}

func rejectTransitionSymlinkParent(absRoot, targetPath string) error {
	rel, err := filepath.Rel(absRoot, filepath.Dir(targetPath))
	if err != nil || rel == "." {
		return err
	}
	current := absRoot
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not be a symlink", filepath.Base(current))
		}
	}
	return nil
}

func rollbackProductWorkflowTransition(workflowPath string, workflowBytes []byte, eventsPath string, eventsBytes []byte, eventsExisted bool) error {
	var errs []string
	if err := os.WriteFile(workflowPath, workflowBytes, 0o644); err != nil {
		errs = append(errs, fmt.Sprintf("restore workflow_state.json: %v", err))
	}
	if eventsPath != "" {
		if eventsExisted {
			if err := os.WriteFile(eventsPath, eventsBytes, 0o644); err != nil {
				errs = append(errs, fmt.Sprintf("restore events artifact: %v", err))
			}
		} else if err := os.Remove(eventsPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("remove created events artifact: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func rejectTransitionSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", filepath.Base(path))
	}
	return nil
}
