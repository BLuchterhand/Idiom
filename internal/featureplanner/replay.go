package featureplanner

import (
	"fmt"
	"strings"
)

type ReplayBundle struct {
	State  FeatureState `json:"state"`
	Events []Event      `json:"events"`
}

func Replay(initial FeatureState, events []Event) (ReplayBundle, error) {
	state := initial
	seenIdempotency := map[string]bool{}
	for _, event := range events {
		if event.IdempotencyKey != "" {
			if seenIdempotency[event.IdempotencyKey] {
				continue
			}
			seenIdempotency[event.IdempotencyKey] = true
		}
		if err := validateReplayEvent(state, event); err != nil {
			return ReplayBundle{}, err
		}
		if event.SessionID != "" && state.SessionID != "" && event.SessionID != state.SessionID {
			return ReplayBundle{}, fmt.Errorf("feature event session_id %q does not match state session_id %q", event.SessionID, state.SessionID)
		}
		if event.FeatureRevisionAfter > 0 {
			state.Revision = event.FeatureRevisionAfter
		}
		switch event.Type {
		case "idiom.feature_planner.planner_question_opened", "idiom.feature_planner.implementor_question_opened":
			state.Status = FeatureStatePlanningQuestion
			state.PlanningLoop.State = FeatureStatePlanningQuestion
		case "idiom.feature_planner.plan_accepted", "idiom.feature_planner.atom_dispatched":
			if state.Status != FeatureStatePlanningQuestion {
				state.Status = FeatureStateDispatching
			}
		case "idiom.feature_planner.tdd_pre_execution_checked":
			upsertTDDEvidence(&state, replayTDDEvidence(event))
		case "idiom.feature_planner.atom_completed":
			markReplayAtomState(&state, event.AtomID, AtomStateComplete)
		case "idiom.feature_planner.atom_source_satisfied":
			markReplayAtomState(&state, event.AtomID, AtomStateSourceSatisfied)
		case "idiom.feature_planner.product_source_satisfied":
			state.Status = FeatureStateSourceSatisfied
			state.PlanningLoop.State = FeatureStateSourceSatisfied
			state.Blockers = nil
			state.OpenQuestions = nil
			state.PlanningLoop.OpenQuestionIDs = nil
			state.OpenRecoveryPackets = nil
		case "idiom.feature.atom_started":
			state.Status = firstNonEmpty(event.FeatureTo, FeatureStateDispatching)
			markReplayAtomState(&state, event.AtomID, firstNonEmpty(event.AtomTo, AtomStateDispatching))
		case "idiom.feature.atom_progressed", "idiom.feature.atom_reviewed", "idiom.feature.atom_verified":
			state.Status = firstNonEmpty(event.FeatureTo, state.Status)
			markReplayAtomState(&state, event.AtomID, event.AtomTo)
		case "idiom.feature.planner_question_opened":
			state.Status = firstNonEmpty(event.FeatureTo, FeatureStatePlanningRepair)
			markReplayAtomState(&state, event.AtomID, firstNonEmpty(event.AtomTo, AtomStateQuestionForPlanner))
		case "idiom.feature_planner.tdd_pre_execution_blocked":
			state.Status = FeatureStatePlanningRepair
			markReplayAtomState(&state, event.AtomID, AtomStateBlockedMissingTDD)
		case "idiom.feature_planner.malformed_structured_output":
			state.Status = FeatureStateMalformedOutput
		case "idiom.feature_planner.planning_stuck":
			state.Status = FeatureStatePlanningStuck
		}
		state.Status = firstNonEmpty(event.FeatureTo, state.Status)
		for _, delta := range event.AtomDeltas {
			switch delta.Op {
			case "update", "":
				markReplayAtomState(&state, delta.AtomID, delta.AtomTo)
			}
		}
		applyActiveRunDeltas(&state, event)
		applyLeaseDeltas(&state, event)
		if state.Status == FeatureStateDispatching && allAtomsTerminal(state.Atoms) && len(state.ActiveRuns) == 0 {
			state.Status = FeatureStateComplete
		}
	}
	state.DerivedNextValidActions = DeriveNextValidActions(state)
	return ReplayBundle{State: state, Events: events}, nil
}

func replayTDDEvidence(event Event) TDDPreExecutionEvidence {
	fields := event.Fields
	return TDDPreExecutionEvidence{
		EventID:                    strings.TrimSpace(event.EventID),
		AtomID:                     strings.TrimSpace(event.AtomID),
		ObligationRefs:             replayStringSlice(fields["obligation_refs"]),
		Commands:                   replayStringSlice(fields["commands"]),
		TestArtifactPaths:          replayStringSlice(fields["test_artifact_paths"]),
		ExpectedPreExecutionResult: replayStringField(fields["expected_pre_execution_result"]),
		CoverageGateEventID:        replayStringField(fields["coverage_gate_event_id"]),
		SnapshotID:                 replayStringField(fields["snapshot_id"]),
		SnapshotDigest:             replayStringField(fields["snapshot_digest"]),
		CreatedAt:                  event.CreatedAt,
	}
}

func replayStringField(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func replayStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return normalizedStringList(typed)
	case []any:
		var out []string
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return normalizedStringList(out)
	default:
		return nil
	}
}

func validateReplayEvent(state FeatureState, event Event) error {
	if event.SchemaVersion != "" && event.SchemaVersion != FeatureEventSchemaVersion {
		return fmt.Errorf("unsupported feature event schema %q", event.SchemaVersion)
	}
	if event.FeatureID != "" && state.FeatureID != "" && event.FeatureID != state.FeatureID {
		return fmt.Errorf("feature event feature_id %q does not match state feature_id %q", event.FeatureID, state.FeatureID)
	}
	if event.FeatureRevisionBefore > 0 || event.FeatureRevisionAfter > 0 {
		if event.FeatureRevisionBefore != state.Revision {
			return ReplayError{Message: fmt.Sprintf("event %q expected feature_revision_before %d got folded revision %d", event.EventID, event.FeatureRevisionBefore, state.Revision)}
		}
		if event.FeatureRevisionAfter != event.FeatureRevisionBefore+1 {
			return ReplayError{Message: fmt.Sprintf("event %q feature_revision_after must increment by one", event.EventID)}
		}
	}
	if event.FeatureFrom != "" && event.FeatureFrom != state.Status {
		return ReplayError{Message: fmt.Sprintf("event %q expected feature_from %q got folded state %q", event.EventID, event.FeatureFrom, state.Status)}
	}
	if len(event.AtomDeltas) > 0 && event.AtomID != "" {
		first := event.AtomDeltas[0]
		if first.AtomID != event.AtomID || first.AtomFrom != event.AtomFrom || first.AtomTo != event.AtomTo {
			return ReplayError{Message: fmt.Sprintf("event %q singular atom fields do not match first atom delta", event.EventID)}
		}
	}
	if event.AtomID != "" && len(event.AtomDeltas) == 0 {
		current, found := replayAtomState(state, event.AtomID)
		if !found {
			return ReplayError{Message: fmt.Sprintf("event %q references unknown atom %q", event.EventID, event.AtomID)}
		}
		if event.AtomFrom != "" && current != event.AtomFrom {
			return ReplayError{Message: fmt.Sprintf("event %q expected atom %q from %q got folded state %q", event.EventID, event.AtomID, event.AtomFrom, current)}
		}
		if event.AtomTo != "" && !isValidAtomState(event.AtomTo) {
			return ReplayError{Message: fmt.Sprintf("event %q has invalid atom_to %q", event.EventID, event.AtomTo)}
		}
	}
	for _, delta := range event.AtomDeltas {
		if strings.TrimSpace(delta.AtomID) == "" {
			return ReplayError{Message: fmt.Sprintf("event %q has atom delta with empty atom_id", event.EventID)}
		}
		current, found := replayAtomState(state, delta.AtomID)
		if !found {
			return ReplayError{Message: fmt.Sprintf("event %q references unknown atom %q", event.EventID, delta.AtomID)}
		}
		if delta.AtomFrom != "" && current != delta.AtomFrom {
			return ReplayError{Message: fmt.Sprintf("event %q expected atom %q from %q got folded state %q", event.EventID, delta.AtomID, delta.AtomFrom, current)}
		}
		if delta.AtomTo != "" && !isValidAtomState(delta.AtomTo) {
			return ReplayError{Message: fmt.Sprintf("event %q has invalid atom_to %q", event.EventID, delta.AtomTo)}
		}
	}
	if event.Type == "idiom.feature_planner.tdd_pre_execution_checked" {
		if err := validateReplayTDDEvidence(replayTDDEvidence(event)); err != nil {
			return ReplayError{Message: fmt.Sprintf("event %q invalid pre-execution TDD evidence: %v", event.EventID, err)}
		}
	}
	return nil
}

func replayAtomState(state FeatureState, atomID string) (string, bool) {
	for _, atom := range state.Atoms {
		if atom.ID == atomID {
			return atom.State, true
		}
	}
	return "", false
}

func validateReplayTDDEvidence(evidence TDDPreExecutionEvidence) error {
	var missing []string
	if strings.TrimSpace(evidence.EventID) == "" {
		missing = append(missing, "event_id")
	}
	if strings.TrimSpace(evidence.AtomID) == "" {
		missing = append(missing, "atom_id")
	}
	if len(evidence.ObligationRefs) == 0 {
		missing = append(missing, "obligation_refs")
	}
	if len(evidence.Commands) == 0 {
		missing = append(missing, "commands")
	}
	if len(evidence.TestArtifactPaths) == 0 {
		missing = append(missing, "test_artifact_paths")
	}
	if strings.TrimSpace(evidence.ExpectedPreExecutionResult) == "" {
		missing = append(missing, "expected_pre_execution_result")
	}
	if strings.TrimSpace(evidence.CoverageGateEventID) == "" {
		missing = append(missing, "coverage_gate_event_id")
	}
	if strings.TrimSpace(evidence.SnapshotID) == "" {
		missing = append(missing, "snapshot_id")
	}
	if strings.TrimSpace(evidence.SnapshotDigest) == "" {
		missing = append(missing, "snapshot_digest")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func markReplayAtomState(state *FeatureState, atomID string, next string) {
	if state == nil || atomID == "" {
		return
	}
	for i := range state.Atoms {
		if state.Atoms[i].ID == atomID {
			if next == "" {
				return
			}
			state.Atoms[i].State = next
			return
		}
	}
}

func applyActiveRunDeltas(state *FeatureState, event Event) {
	for _, delta := range event.ActiveRunDeltas {
		switch delta.Op {
		case "insert":
			state.ActiveRuns = append(state.ActiveRuns, ActiveRun{
				RunID:                   firstNonEmpty(delta.LaneSessionID, delta.AtomID),
				AtomID:                  delta.AtomID,
				LaneSessionID:           delta.LaneSessionID,
				LaneFamily:              delta.LaneFamily,
				InvocationContractID:    delta.InvocationContractID,
				RuntimeEnvelopeID:       delta.RuntimeEnvelopeID,
				PlanExecuteProgressPath: delta.ProgressPath,
				ProgressRevision:        delta.AfterProgressRevision,
				ProgressSHA256:          delta.AfterProgressSHA256,
				ResultSource:            delta.ResultSource,
				State:                   firstNonEmpty(delta.AfterState, event.AtomTo),
				StartedAt:               delta.StartedAt,
				HeartbeatAt:             delta.HeartbeatAt,
				HeartbeatSource:         delta.HeartbeatSource,
				EvidenceDeltaSinceEvent: append([]string(nil), delta.EvidenceDeltaSinceEvent...),
			})
		case "remove", "fold_terminal":
			removeActiveRun(state, delta.AtomID, delta.LaneSessionID)
		case "heartbeat", "classify":
			updateActiveRun(state, delta)
		}
	}
}

func applyLeaseDeltas(state *FeatureState, event Event) {
	for _, delta := range event.LeaseDeltas {
		switch delta.Op {
		case "acquire":
			state.Leases = append(state.Leases, Lease{
				LeaseID:         delta.LeaseID,
				LeaseKind:       delta.LeaseKind,
				Scope:           delta.Scope,
				OwnerFeatureID:  delta.OwnerFeatureID,
				OwnerAtomID:     delta.OwnerAtomID,
				OwnerSession:    delta.OwnerSession,
				FencingRevision: delta.AfterFencingRevision,
				AcquiredAt:      delta.AcquiredAt,
				ExpiresAt:       delta.ExpiresAt,
				State:           "held",
			})
		case "release", "expire":
			for i := range state.Leases {
				if state.Leases[i].LeaseID == delta.LeaseID {
					state.Leases[i].State = delta.ResultingState
					state.Leases[i].ReleasedAt = delta.ReleasedAt
					state.Leases[i].ReleaseReason = delta.ReleaseReason
				}
			}
		}
	}
}

func removeActiveRun(state *FeatureState, atomID, sessionID string) {
	for i := range state.ActiveRuns {
		if state.ActiveRuns[i].AtomID == atomID && (sessionID == "" || state.ActiveRuns[i].LaneSessionID == sessionID || state.ActiveRuns[i].RunID == sessionID) {
			state.ActiveRuns = append(state.ActiveRuns[:i], state.ActiveRuns[i+1:]...)
			return
		}
	}
}

func updateActiveRun(state *FeatureState, delta ActiveRunDelta) {
	for i := range state.ActiveRuns {
		if state.ActiveRuns[i].AtomID != delta.AtomID {
			continue
		}
		if delta.LaneSessionID != "" && state.ActiveRuns[i].LaneSessionID != "" && state.ActiveRuns[i].LaneSessionID != delta.LaneSessionID {
			continue
		}
		if delta.AfterState != "" {
			state.ActiveRuns[i].State = delta.AfterState
		}
		if delta.AfterProgressRevision != 0 {
			state.ActiveRuns[i].ProgressRevision = delta.AfterProgressRevision
		}
		if delta.AfterProgressSHA256 != "" {
			state.ActiveRuns[i].ProgressSHA256 = delta.AfterProgressSHA256
		}
		if delta.HeartbeatAt != "" {
			state.ActiveRuns[i].HeartbeatAt = delta.HeartbeatAt
		}
		return
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
