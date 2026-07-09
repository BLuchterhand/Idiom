package planexecute

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/featureplanner"
	coreplanexecute "idiom/internal/workflows/planexecute"
)

func ReconcileActiveRuns(state *featureplanner.FeatureState, root string) []featureplanner.Event {
	if state == nil || len(state.ActiveRuns) == 0 || strings.TrimSpace(root) == "" {
		return nil
	}

	var events []featureplanner.Event

	for _, run := range append([]featureplanner.ActiveRun(nil), state.ActiveRuns...) {
		if strings.TrimSpace(run.LaneFamily) != "" && strings.TrimSpace(run.LaneFamily) != string(lanecontract.LanePlanExecute) {
			continue
		}

		atom := atomForActiveRun(state, run)
		if atom == nil {
			continue
		}

		payload, encodeErr, ok := findTerminalCompletionForActiveRun(root, run, *atom)
		if encodeErr != nil {
			events = append(events, featureplanner.Event{
				Type:      featureplannercontract.EventName(featureplannercontract.EventMalformedStructuredOutput),
				SessionID: state.SessionID,
				AtomID:    atom.ID,
				Message:   encodeErr.Error(),
			})
			continue
		}
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

func findTerminalCompletionForActiveRun(root string, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) (string, error, bool) {
	for _, sessionID := range progressSessionCandidates(run) {
		record, completionPath, found, err := coreplanexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, sessionID)
		if err == nil && found && terminalCompletionForActiveRun(record, run, atom) {
			payload, err := encodeTerminalCompletion(record, completionPath, run)
			return payload, err, err == nil
		}

		state, relPath, found, err := coreplanexecute.TryLoadProgressState(root, sessionID)
		if err == nil && found && terminalProgressForActiveRun(state, run, atom) {
			payload, err := encodeProgressCompletion(state, relPath, run)
			return payload, err, err == nil
		}
	}

	return "", nil, false
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
	record, ok := coreplanexecute.PlanExecuteTerminalCompletionRecordFromProgressState(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{})
	if !ok {
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

	recordPaths := uniqueStrings(nil, record.RequiredEditPaths...)

	recordPaths = uniqueStrings(recordPaths, record.AllowedEditPaths...)
	recordPaths = uniqueStrings(recordPaths, record.ChangedPaths...)
	recordPaths = uniqueStrings(recordPaths, record.SourceSatisfiedEvidencePaths...)

	return len(atomPaths) == 0 || stringSetsOverlap(atomPaths, recordPaths)
}

func terminalCompletionForActiveRun(record plancontract.PlanExecuteTerminalCompletionRecord, run featureplanner.ActiveRun, atom featureplanner.FeatureAtomState) bool {
	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)

	completedAt, ok := parseRequiredTime(record.TS)

	if !ok {
		return false
	}

	if startedAt := parseTime(run.StartedAt); !startedAt.IsZero() && completedAt.Before(startedAt.Add(-1*time.Second)) {
		return false
	}

	atomPaths := uniqueStrings(nil, atom.RequiredEditPaths...)

	atomPaths = uniqueStrings(atomPaths, atom.AllowedEditPaths...)

	recordPaths := uniqueStrings(nil, record.RequiredEditPaths...)

	recordPaths = uniqueStrings(recordPaths, record.AllowedEditPaths...)
	recordPaths = uniqueStrings(recordPaths, record.ChangedPaths...)
	recordPaths = uniqueStrings(recordPaths, record.SourceSatisfiedEvidencePaths...)

	return len(atomPaths) == 0 || stringSetsOverlap(atomPaths, recordPaths)
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

func parseRequiredTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}

	return parsed, true
}

func encodeTerminalCompletion(record plancontract.PlanExecuteTerminalCompletionRecord, completionPath string, run featureplanner.ActiveRun) (string, error) {
	fields := plancontract.PlanExecuteTerminalCompletionEventFields(record, completionPath)
	if fields == nil {
		fields = map[string]any{}
	}

	addActiveRunParentEvidence(fields, run)

	fields[featureplannercontract.PlanExecuteCompletionFieldTool] = featureplannercontract.ToolPlanExecuteComplete
	fields[featureplannercontract.PlanExecuteCompletionFieldLaneFamily] = string(lanecontract.LanePlanExecute)
	fields[featureplannercontract.PlanExecuteCompletionFieldCompletionSource] = string(featureplannercontract.PlanExecuteCompletionSourceDurableTerminal)
	markTrustedAcceptanceEvidence(fields)

	body, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("encode terminal completion payload: %w", err)
	}

	return string(body), nil
}

func encodeProgressCompletion(progress coreplanexecute.ProgressState, progressPath string, run featureplanner.ActiveRun) (string, error) {
	record, ok := coreplanexecute.PlanExecuteTerminalCompletionRecordFromProgressState(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
		ProgressPath: strings.TrimSpace(progressPath),
	})
	if !ok {
		fields := coreplanexecute.ProgressStateEventFields(progress, strings.TrimSpace(progressPath))
		if fields == nil {
			fields = map[string]any{}
		}

		return encodePlanExecuteCompletionFields(fields, progressPath, string(featureplannercontract.PlanExecuteCompletionSourceDurableProgress), run)
	}

	fields := plancontract.PlanExecuteTerminalCompletionEventFields(record, "")
	if fields == nil {
		fields = map[string]any{}
	}

	return encodePlanExecuteCompletionFields(fields, progressPath, string(featureplannercontract.PlanExecuteCompletionSourceDurableProgress), run)
}

func encodePlanExecuteCompletionFields(fields map[string]any, progressPath string, source string, run featureplanner.ActiveRun) (string, error) {
	if strings.TrimSpace(progressPath) == "" {
		progressPath = strings.TrimSpace(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldStatePath))
	}

	delete(fields, plancontract.PlanExecuteTerminalFieldStatePath)
	addActiveRunParentEvidence(fields, run)

	fields[featureplannercontract.PlanExecuteCompletionFieldTool] = featureplannercontract.ToolPlanExecuteComplete
	fields[featureplannercontract.PlanExecuteCompletionFieldLaneFamily] = string(lanecontract.LanePlanExecute)
	fields[featureplannercontract.PlanExecuteCompletionFieldCompletionSource] = source

	if strings.TrimSpace(progressPath) != "" {
		fields[plancontract.PlanExecuteTerminalFieldProgressPath] = strings.TrimSpace(progressPath)
	}

	markTrustedAcceptanceEvidence(fields)

	body, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("encode progress completion payload: %w", err)
	}

	return string(body), nil
}

func addActiveRunParentEvidence(fields map[string]any, run featureplanner.ActiveRun) {
	if strings.TrimSpace(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldContractID)) == "" && strings.TrimSpace(run.InvocationContractID) != "" {
		fields[plancontract.PlanExecuteTerminalFieldContractID] = strings.TrimSpace(run.InvocationContractID)
	}

	if strings.TrimSpace(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldSnapshotHash)) == "" && strings.TrimSpace(run.RuntimeEnvelopeID) != "" {
		fields[plancontract.PlanExecuteTerminalFieldSnapshotHash] = strings.TrimSpace(run.RuntimeEnvelopeID)
	}
}

func markTrustedAcceptanceEvidence(fields map[string]any) {
	if fields == nil {
		return
	}

	if plancontract.DecidePlanExecuteTerminalCompletionFields(fields, nil).Class != plancontract.PlanExecuteTerminalDecisionCompleted {
		delete(fields, featureplannercontract.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted)

		return
	}

	if _, ok := fields[plancontract.PlanExecuteTerminalFieldAcceptanceEvidenceIDs]; ok {
		featureplanner.TrustObservedPlanExecuteCompletionFields(fields)
	}
}

func stringSetsOverlap(left []string, right []string) bool {
	left = plancontract.UniqueScopePaths(left)
	right = plancontract.UniqueScopePaths(right)

	seen := make(map[string]struct{}, len(left))

	for _, value := range left {
		seen[value] = struct{}{}
	}

	for _, value := range right {
		if _, ok := seen[value]; ok {
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
