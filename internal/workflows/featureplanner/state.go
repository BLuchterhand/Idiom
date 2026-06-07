package featureplanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validSessionID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func NewState(sessionID string, prompt string, promptFile string, maxPlanningIterations int, now time.Time) FeatureState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if maxPlanningIterations <= 0 {
		maxPlanningIterations = 3
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	state := FeatureState{
		SchemaVersion: SchemaVersion,
		SessionID:     strings.TrimSpace(sessionID),
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
		PromptSHA256:  promptSHA256(prompt),
		PromptFile:    strings.TrimSpace(promptFile),
		Status:        FeatureStatePlanning,
		PlanningLoop: PlanningLoop{
			Iteration:     0,
			MaxIterations: maxPlanningIterations,
			State:         FeatureStatePlanning,
		},
	}
	state.DerivedNextValidActions = DeriveNextValidActions(state)
	return state
}

func SaveState(root string, state *FeatureState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("feature planner state is nil")
	}
	if strings.TrimSpace(state.SessionID) == "" || !validSessionID.MatchString(state.SessionID) {
		return "", fmt.Errorf("invalid feature planner session id %q", state.SessionID)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = SchemaVersion
	}
	if state.SchemaVersion != SchemaVersion {
		return "", fmt.Errorf("unsupported feature planner schema %q", state.SchemaVersion)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	state.DerivedNextValidActions = DeriveNextValidActions(*state)
	if strings.TrimSpace(state.EventsPath) == "" {
		_, relEventsPath, err := eventLogPath(root, state.SessionID)
		if err != nil {
			return "", err
		}
		state.EventsPath = relEventsPath
	}
	if err := ValidateDurableState(*state); err != nil {
		return "", err
	}
	fullPath, relPath, err := statePath(root, state.SessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir feature planner state dir: %w", err)
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal feature planner state: %w", err)
	}
	tmpPath := fullPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write feature planner state: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("replace feature planner state: %w", err)
	}
	return relPath, nil
}

func LoadState(root string, sessionID string) (FeatureState, string, error) {
	state, relPath, found, err := TryLoadState(root, sessionID)
	if err != nil {
		return FeatureState{}, relPath, err
	}
	if !found {
		return FeatureState{}, relPath, fmt.Errorf("feature planner state %s does not exist", relPath)
	}
	return state, relPath, nil
}

func TryLoadState(root string, sessionID string) (FeatureState, string, bool, error) {
	fullPath, relPath, err := statePath(root, sessionID)
	if err != nil {
		return FeatureState{}, "", false, err
	}
	body, err := os.ReadFile(fullPath)
	if os.IsNotExist(err) {
		return FeatureState{}, relPath, false, nil
	}
	if err != nil {
		return FeatureState{}, relPath, false, fmt.Errorf("read feature planner state %s: %w", relPath, err)
	}
	var state FeatureState
	if err := json.Unmarshal(body, &state); err != nil {
		return FeatureState{}, relPath, false, fmt.Errorf("decode feature planner state %s: %w", relPath, err)
	}
	if state.SchemaVersion != "" && state.SchemaVersion != SchemaVersion {
		return FeatureState{}, relPath, false, fmt.Errorf("unsupported feature planner schema %q in %s", state.SchemaVersion, relPath)
	}
	if strings.TrimSpace(state.SessionID) == "" {
		state.SessionID = strings.TrimSpace(sessionID)
	}
	if !validSessionID.MatchString(state.SessionID) {
		return FeatureState{}, relPath, false, fmt.Errorf("invalid feature planner session id %q in %s", state.SessionID, relPath)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = SchemaVersion
	}
	state.DerivedNextValidActions = DeriveNextValidActions(state)
	if err := ValidateDurableState(state); err != nil {
		return FeatureState{}, relPath, false, err
	}
	return state, relPath, true, nil
}

func statePath(root string, sessionID string) (string, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", "", fmt.Errorf("invalid feature planner session id %q", sessionID)
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "feature-planner", sessionID, "state.json"))
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}

func EventLogPath(root string, sessionID string) (string, string, error) {
	return eventLogPath(root, sessionID)
}

func eventLogPath(root string, sessionID string) (string, string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", "", fmt.Errorf("invalid feature planner session id %q", sessionID)
	}
	relPath := filepath.ToSlash(filepath.Join(".idiom", "feature-planner", sessionID, "events.jsonl"))
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return filepath.Join(root, filepath.FromSlash(relPath)), relPath, nil
}

func promptSHA256(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func DeriveNextValidActions(state FeatureState) []string {
	switch state.Status {
	case FeatureStateDraftingPlan:
		return []string{"start_planning"}
	case FeatureStatePlanning:
		return []string{"continue_planning", "submit_feature_plan", "ask_planner_question", "mark_stuck"}
	case FeatureStatePlanningRepair:
		return []string{"repair_feature_plan", "continue_planning", "mark_stuck"}
	case FeatureStatePlanningQuestion:
		return []string{"answer_planner_question", "continue_planning_after_answer", "mark_stuck"}
	case FeatureStateDispatching:
		return []string{"dispatch_next_atom", "record_implementor_question", "resume_active_run"}
	case FeatureStateMalformedOutput:
		return []string{"repair_structured_output", "mark_stuck"}
	case FeatureStateStaleEditAnchor:
		return []string{"refresh_source_anchor", "repair_feature_plan", "mark_stuck"}
	case FeatureStateReady:
		return []string{"run-next", "refresh-context", "release-check"}
	case FeatureStateNeedsContextRefresh:
		return []string{"refresh-context"}
	case FeatureStateRecoveryOpen:
		return []string{"promote-recovery", "reconcile-recovery", "repair-plan"}
	case FeatureStateBlocked:
		return []string{"repair-state", "replan"}
	case FeatureStateReleaseReview:
		return []string{"release-check"}
	case FeatureStateSourceSatisfied:
		return []string{"verify_source_satisfied", "close_feature"}
	case FeatureStateVerifying:
		return []string{"run_verification", "request_repair", "submit_for_review"}
	case FeatureStateReviewing:
		return []string{"approve", "request_changes", "request_replan"}
	case FeatureStateComplete, FeatureStateFailed, FeatureStatePlanningStuck:
		return nil
	default:
		return []string{"inspect_state", "repair_feature_plan"}
	}
}
