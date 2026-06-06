package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/featureplanner"
	"idiom/internal/planexecute"
)

func (m *Model) reconcileFeaturePlannerActivePlanExecuteRuns(state *featureplanner.FeatureState) []featureplanner.Event {
	if m == nil || state == nil || len(state.ActiveRuns) == 0 {
		return nil
	}
	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		root = strings.TrimSpace(m.cfg.Workspace.Root)
	}
	if root == "" {
		return nil
	}
	var events []featureplanner.Event
	for _, run := range append([]featureplanner.ActiveRun(nil), state.ActiveRuns...) {
		if strings.TrimSpace(run.LaneFamily) != "" && strings.TrimSpace(run.LaneFamily) != "plan-execute" {
			continue
		}
		atom := featurePlannerAtomForActiveRun(state, run)
		if atom == nil {
			continue
		}
		progress, progressPath, ok := findTerminalPlanExecuteProgressForActiveRun(root, run, *atom)
		if !ok {
			continue
		}
		payload := encodePlanExecuteProgressCompletion(progress, progressPath)
		events = append(events, featureplanner.ApplyImplementorOutput(state, atom, payload)...)
	}
	return events
}

func featurePlannerAtomForActiveRun(state *featureplanner.FeatureState, run featureplanner.ActiveRun) *featureplanner.FeatureAtomState {
	if state == nil {
		return nil
	}
	for i := range state.Atoms {
		atom := &state.Atoms[i]
		if strings.TrimSpace(atom.ID) == strings.TrimSpace(run.AtomID) {
			return atom
		}
	}
	return nil
}

func findTerminalPlanExecuteProgressForActiveRun(root string, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) (planexecute.ProgressState, string, bool) {
	for _, sessionID := range planExecuteProgressSessionCandidates(run) {
		state, relPath, found, err := planexecute.TryLoadProgressState(root, sessionID)
		if err == nil && found && terminalPlanExecuteProgressForActiveRun(state, run, atom) {
			return state, relPath, true
		}
	}
	latestState, latestPath, latestTime, foundLatest := planexecute.ProgressState{}, "", time.Time{}, false
	for _, candidate := range listPlanExecuteProgressStates(root) {
		state, relPath, found, err := planexecute.TryLoadProgressState(root, candidate)
		if err != nil || !found || !terminalPlanExecuteProgressForActiveRun(state, run, atom) {
			continue
		}
		updated := parsePlanExecuteProgressTime(state.UpdatedAt)
		if !foundLatest || updated.After(latestTime) {
			latestState, latestPath, latestTime, foundLatest = state, relPath, updated, true
		}
	}
	return latestState, latestPath, foundLatest
}

func planExecuteProgressSessionCandidates(run featureplanner.ActiveRun) []string {
	var candidates []string
	if sessionID := sessionIDFromPlanExecuteProgressPath(run.PlanExecuteProgressPath); sessionID != "" {
		candidates = append(candidates, sessionID)
	}
	candidates = append(candidates, strings.TrimSpace(run.LaneSessionID), strings.TrimSpace(run.RunID))
	return appendUniqueBriefStrings(nil, candidates...)
}

func sessionIDFromPlanExecuteProgressPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "" || base == "state" {
		return ""
	}
	return base
}

func listPlanExecuteProgressStates(root string) []string {
	dir := filepath.Join(strings.TrimSpace(root), ".idiom", "plan-execute", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var sessions []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if sessionID != "" {
			sessions = append(sessions, sessionID)
		}
	}
	return sessions
}

func terminalPlanExecuteProgressForActiveRun(progress planexecute.ProgressState, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) bool {
	if !terminalPlanExecuteProgressStatus(progress) {
		return false
	}
	if startedAt := parsePlanExecuteProgressTime(run.StartedAt); !startedAt.IsZero() {
		updatedAt := parsePlanExecuteProgressTime(progress.UpdatedAt)
		createdAt := parsePlanExecuteProgressTime(progress.CreatedAt)
		if !updatedAt.IsZero() && updatedAt.Before(startedAt.Add(-1*time.Second)) {
			return false
		}
		if !createdAt.IsZero() && createdAt.Before(startedAt.Add(-1*time.Second)) {
			return false
		}
	}
	atomPaths := appendUniqueBriefStrings(nil, atom.RequiredEditPaths...)
	atomPaths = appendUniqueBriefStrings(atomPaths, atom.AllowedEditPaths...)
	progressPaths := appendUniqueBriefStrings(nil, progress.RequiredEditPaths...)
	progressPaths = appendUniqueBriefStrings(progressPaths, progress.AllowedEditPaths...)
	progressPaths = appendUniqueBriefStrings(progressPaths, progress.ChangedPaths...)
	return len(atomPaths) == 0 || appStringSetsOverlap(atomPaths, progressPaths)
}

func terminalPlanExecuteProgressStatus(progress planexecute.ProgressState) bool {
	switch strings.TrimSpace(progress.Status) {
	case planexecute.ProgressStatusCompleted, planexecute.ProgressStatusFailed, planexecute.ProgressStatusDegraded, planexecute.ProgressStatusSkipped:
		return true
	default:
		return false
	}
}

func parsePlanExecuteProgressTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func encodePlanExecuteProgressCompletion(progress planexecute.ProgressState, progressPath string) string {
	fields := map[string]any{
		"tool":             featureplanner.ToolPlanExecuteComplete,
		"lane_family":      "plan-execute",
		"status":           strings.TrimSpace(progress.Status),
		"phase":            strings.TrimSpace(progress.Phase),
		"completion_source": "durable_plan_execute_progress",
		"progress_path":     strings.TrimSpace(progressPath),
	}
	if strings.TrimSpace(progress.Review.Status) != "" {
		fields["review_status"] = strings.TrimSpace(progress.Review.Status)
	}
	if strings.TrimSpace(progress.FailureKind) != "" {
		fields["failure_kind"] = strings.TrimSpace(progress.FailureKind)
	}
	if strings.TrimSpace(progress.FailureReason) != "" {
		fields["failure_reason"] = strings.TrimSpace(progress.FailureReason)
	}
	body, _ := json.Marshal(fields)
	return string(body)
}

func appStringSetsOverlap(left []string, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value = normalizeAppPath(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range right {
		if _, ok := seen[normalizeAppPath(value)]; ok {
			return true
		}
	}
	return false
}

func normalizeAppPath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	path = strings.TrimPrefix(path, "./")
	return path
}
