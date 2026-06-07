package planexecute

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/featureplanner"
	coreplanexecute "idiom/internal/planexecute"
)

func ReconcileActiveRuns(state *featureplanner.FeatureState, root string) []featureplanner.Event {
	if state == nil || len(state.ActiveRuns) == 0 || strings.TrimSpace(root) == "" {
		return nil
	}
	var events []featureplanner.Event
	for _, run := range append([]featureplanner.ActiveRun(nil), state.ActiveRuns...) {
		if strings.TrimSpace(run.LaneFamily) != "" && strings.TrimSpace(run.LaneFamily) != "plan-execute" {
			continue
		}
		atom := atomForActiveRun(state, run)
		if atom == nil {
			continue
		}
		payload, ok := findTerminalCompletionForActiveRun(root, run, *atom)
		if !ok {
			continue
		}
		events = append(events, featureplanner.ApplyImplementorOutput(state, atom, payload)...)
	}
	return events
}

func atomForActiveRun(state *featureplanner.FeatureState, run featureplanner.ActiveRun) *featureplanner.FeatureAtomState {
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

func findTerminalCompletionForActiveRun(root string, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) (string, bool) {
	for _, sessionID := range progressSessionCandidates(run) {
		record, completionPath, found, err := coreplanexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, sessionID)
		if err == nil && found && terminalCompletionForActiveRun(record, run, atom) {
			return encodeTerminalCompletion(record, completionPath), true
		}
		state, relPath, found, err := coreplanexecute.TryLoadProgressState(root, sessionID)
		if err == nil && found && terminalProgressForActiveRun(state, run, atom) {
			return encodeProgressCompletion(state, relPath), true
		}
	}
	return "", false
}

func progressSessionCandidates(run featureplanner.ActiveRun) []string {
	var candidates []string
	if sessionID := sessionIDFromProgressPath(run.PlanExecuteProgressPath); sessionID != "" {
		candidates = append(candidates, sessionID)
	}
	candidates = append(candidates, strings.TrimSpace(run.LaneSessionID), strings.TrimSpace(run.RunID))
	return uniqueStrings(nil, candidates...)
}

func sessionIDFromProgressPath(path string) string {
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

func terminalProgressForActiveRun(progress coreplanexecute.ProgressState, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) bool {
	if !terminalProgressStatus(progress) {
		return false
	}
	if startedAt := parseTime(run.StartedAt); !startedAt.IsZero() {
		updatedAt := parseTime(progress.UpdatedAt)
		createdAt := parseTime(progress.CreatedAt)
		if !updatedAt.IsZero() && updatedAt.Before(startedAt.Add(-1*time.Second)) {
			return false
		}
		if !createdAt.IsZero() && createdAt.Before(startedAt.Add(-1*time.Second)) {
			return false
		}
	}
	atomPaths := uniqueStrings(nil, atom.RequiredEditPaths...)
	atomPaths = uniqueStrings(atomPaths, atom.AllowedEditPaths...)
	progressPaths := uniqueStrings(nil, progress.RequiredEditPaths...)
	progressPaths = uniqueStrings(progressPaths, progress.AllowedEditPaths...)
	progressPaths = uniqueStrings(progressPaths, progress.ChangedPaths...)
	return len(atomPaths) == 0 || stringSetsOverlap(atomPaths, progressPaths)
}

func terminalCompletionForActiveRun(record coreplanexecute.PlanExecuteTerminalCompletionRecord, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) bool {
	if !terminalCompletionClass(record.TerminalClass) {
		return false
	}
	if startedAt := parseTime(run.StartedAt); !startedAt.IsZero() {
		updatedAt := parseTime(record.TS)
		if !updatedAt.IsZero() && updatedAt.Before(startedAt.Add(-1*time.Second)) {
			return false
		}
	}
	atomPaths := uniqueStrings(nil, atom.RequiredEditPaths...)
	atomPaths = uniqueStrings(atomPaths, atom.AllowedEditPaths...)
	recordPaths := uniqueStrings(nil, record.RequiredEditPaths...)
	recordPaths = uniqueStrings(recordPaths, record.AllowedEditPaths...)
	recordPaths = uniqueStrings(recordPaths, record.ChangedPaths...)
	recordPaths = uniqueStrings(recordPaths, record.SourceSatisfiedEvidencePaths...)
	return len(atomPaths) == 0 || stringSetsOverlap(atomPaths, recordPaths)
}

func terminalCompletionClass(class coreplanexecute.PlanExecuteTerminalClass) bool {
	switch class {
	case coreplanexecute.PlanExecuteTerminalClassSuccess, coreplanexecute.PlanExecuteTerminalClassRecoveryRequired, coreplanexecute.PlanExecuteTerminalClassFailed:
		return true
	default:
		return false
	}
}

func terminalProgressStatus(progress coreplanexecute.ProgressState) bool {
	switch strings.TrimSpace(progress.Status) {
	case coreplanexecute.ProgressStatusCompleted, coreplanexecute.ProgressStatusFailed, coreplanexecute.ProgressStatusDegraded, coreplanexecute.ProgressStatusBlocked, coreplanexecute.ProgressStatusSkipped:
		return true
	default:
		return false
	}
}

func parseTime(value string) time.Time {
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

func encodeTerminalCompletion(record coreplanexecute.PlanExecuteTerminalCompletionRecord, completionPath string) string {
	fields := coreplanexecute.PlanExecuteTerminalCompletionEventFields(record, completionPath)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["tool"] = featureplanner.ToolPlanExecuteComplete
	fields["lane_family"] = "plan-execute"
	fields["completion_source"] = "durable_plan_execute_terminal_completion"
	body, _ := json.Marshal(fields)
	return string(body)
}

func encodeProgressCompletion(progress coreplanexecute.ProgressState, progressPath string) string {
	fields := coreplanexecute.ProgressStateEventFields(progress, strings.TrimSpace(progressPath))
	if fields == nil {
		fields = map[string]any{}
	}
	if strings.TrimSpace(progressPath) == "" {
		progressPath = strings.TrimSpace(featureplanner.CompletionFieldString(fields, "state_path"))
	}
	delete(fields, "state_path")
	fields["tool"] = featureplanner.ToolPlanExecuteComplete
	fields["lane_family"] = "plan-execute"
	fields["completion_source"] = "durable_plan_execute_progress"
	if strings.TrimSpace(progressPath) != "" {
		fields["progress_path"] = strings.TrimSpace(progressPath)
	}
	if featureplanner.CompletionFieldString(fields, "status") == "" {
		fields["status"] = strings.TrimSpace(progress.Status)
	}
	if featureplanner.CompletionFieldString(fields, "phase") == "" {
		fields["phase"] = strings.TrimSpace(progress.Phase)
	}
	body, _ := json.Marshal(fields)
	return string(body)
}

func stringSetsOverlap(left []string, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		if value = normalizePath(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range right {
		if _, ok := seen[normalizePath(value)]; ok {
			return true
		}
	}
	return false
}

func uniqueStrings(base []string, values ...string) []string {
	out := append([]string(nil), base...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizePath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	path = strings.TrimPrefix(path, "./")
	return path
}
