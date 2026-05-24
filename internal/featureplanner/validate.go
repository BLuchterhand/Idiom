package featureplanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ParsedPlannerOutput struct {
	Kind     string
	Plan     FeaturePlan
	Question PlannerQuestion
	Raw      string
}

func ParsePlannerOutput(raw string) (ParsedPlannerOutput, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ParsedPlannerOutput{}, errors.New("planner output is empty")
	}
	if strings.HasPrefix(trimmed, "<<QUESTION>>:") {
		return ParsedPlannerOutput{
			Kind: ToolAskPlannerQuestion,
			Question: PlannerQuestion{
				QuestionSummary: strings.TrimSpace(strings.TrimPrefix(trimmed, "<<QUESTION>>:")),
				Status:          "open",
			},
			Raw: trimmed,
		}, nil
	}
	var envelope struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return ParsedPlannerOutput{}, fmt.Errorf("planner output must be JSON or <<QUESTION>>: %w", err)
	}
	switch strings.TrimSpace(envelope.Tool) {
	case ToolSubmitFeaturePlan:
		var plan FeaturePlan
		if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode submit_feature_plan: %w", err)
		}
		return ParsedPlannerOutput{Kind: ToolSubmitFeaturePlan, Plan: plan, Raw: trimmed}, nil
	case ToolAskPlannerQuestion:
		var question PlannerQuestion
		if err := json.Unmarshal([]byte(trimmed), &question); err != nil {
			return ParsedPlannerOutput{}, fmt.Errorf("decode ask_planner_question: %w", err)
		}
		question.Status = "open"
		return ParsedPlannerOutput{Kind: ToolAskPlannerQuestion, Question: question, Raw: trimmed}, nil
	default:
		return ParsedPlannerOutput{}, fmt.Errorf("unsupported feature planner tool %q", envelope.Tool)
	}
}

func ValidatePlan(plan FeaturePlan) error {
	var errs []error
	require(&errs, plan.Summary, "feature plan missing summary")
	require(&errs, plan.TopLevelScope.Objective, "feature plan missing top_level_scope.objective")
	if len(plan.TopLevelScope.SuccessCriteria) == 0 {
		errs = append(errs, errors.New("feature plan missing top_level_scope.success_criteria"))
	}
	if len(plan.SDDContract.Refs) == 0 && len(plan.SDDContract.Acceptance) == 0 {
		errs = append(errs, errors.New("feature plan missing SDD refs or acceptance criteria"))
	}
	require(&errs, plan.TDDContract.CoverageTier, "feature plan missing tdd_contract.coverage_tier")
	if !plan.TDDContract.RequiredBeforeImplementation {
		errs = append(errs, errors.New("feature plan tdd_contract.required_before_implementation must be true"))
	}
	if len(plan.TDDContract.Obligations) == 0 {
		errs = append(errs, errors.New("feature plan missing tdd_contract.obligations"))
	}
	obligations := map[string]TDDObligation{}
	for i, obligation := range plan.TDDContract.Obligations {
		prefix := fmt.Sprintf("tdd obligation %d", i)
		require(&errs, obligation.ID, prefix+" missing id")
		require(&errs, obligation.Behavior, prefix+" missing behavior")
		require(&errs, obligation.Command, prefix+" missing command")
		require(&errs, obligation.RequiredBefore, prefix+" missing required_before")
		if strings.TrimSpace(obligation.RequiredBefore) != "" && strings.TrimSpace(obligation.RequiredBefore) != "implementation" {
			errs = append(errs, fmt.Errorf("%s required_before must be implementation", prefix))
		}
		if id := strings.TrimSpace(obligation.ID); id != "" {
			obligations[id] = obligation
		}
	}
	if len(plan.Atoms) == 0 {
		errs = append(errs, errors.New("feature plan missing atoms"))
	}
	seenAtoms := map[string]bool{}
	for i, atom := range plan.Atoms {
		prefix := fmt.Sprintf("atom %d", i)
		require(&errs, atom.ID, prefix+" missing id")
		require(&errs, atom.Title, prefix+" missing title")
		require(&errs, atom.Summary, prefix+" missing summary")
		if id := strings.TrimSpace(atom.ID); id != "" {
			if seenAtoms[id] {
				errs = append(errs, fmt.Errorf("%s duplicate id %q", prefix, id))
			}
			seenAtoms[id] = true
		}
		if len(atom.RequiredEditPaths) == 0 && len(atom.AllowedEditPaths) == 0 && !atom.ImplementationRequired {
			errs = append(errs, fmt.Errorf("%s missing edit scope or source-satisfied marker", prefix))
		}
		if len(atom.SDDRefs) == 0 && len(plan.SDDContract.Refs) > 0 {
			errs = append(errs, fmt.Errorf("%s missing sdd_refs", prefix))
		}
		if !atom.TestFirst.BaselineRequired {
			errs = append(errs, fmt.Errorf("%s test_first.baseline_required must be true", prefix))
		}
		if len(atom.TestFirst.Commands) == 0 {
			errs = append(errs, fmt.Errorf("%s missing test_first.commands", prefix))
		}
		if len(atom.TestFirst.ObligationRefs) == 0 {
			errs = append(errs, fmt.Errorf("%s missing test_first.obligation_refs", prefix))
		}
		for _, ref := range atom.TestFirst.ObligationRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, ok := obligations[ref]; !ok {
				errs = append(errs, fmt.Errorf("%s references unknown TDD obligation %q", prefix, ref))
			}
		}
	}
	if err := validateAtomDependencies(plan.Atoms); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func ValidateDurablePlan(plan FeaturePlan) error {
	var errs []error
	if plan.SchemaVersion != "" && plan.SchemaVersion != FeaturePlanSchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported feature plan schema %q", plan.SchemaVersion))
	}
	if err := ValidatePlan(plan); err != nil {
		errs = append(errs, err)
	}
	if strings.TrimSpace(plan.FeatureID) == "" {
		errs = append(errs, errors.New("feature plan missing feature_id"))
	}
	if strings.TrimSpace(plan.SourceTask.Kind) == "" && strings.TrimSpace(plan.SourceTask.Summary) == "" {
		errs = append(errs, errors.New("feature plan missing source_task"))
	}
	for i, atom := range plan.Atoms {
		prefix := fmt.Sprintf("atom %d", i)
		if strings.TrimSpace(atom.SchemaVersion) != "" && atom.SchemaVersion != FeatureAtomSchemaVersion {
			errs = append(errs, fmt.Errorf("%s unsupported schema %q", prefix, atom.SchemaVersion))
		}
		if atom.FeatureID != "" && plan.FeatureID != "" && atom.FeatureID != plan.FeatureID {
			errs = append(errs, fmt.Errorf("%s feature_id %q does not match plan feature_id %q", prefix, atom.FeatureID, plan.FeatureID))
		}
		if missing := MissingStrictPreDispatchTDD(atom); len(missing) > 0 {
			errs = append(errs, fmt.Errorf("%s missing durable pre-dispatch TDD fields: %s", prefix, strings.Join(missing, ", ")))
		}
		if mismatches := MismatchedStrictPreDispatchTDD(atom); len(mismatches) > 0 {
			errs = append(errs, fmt.Errorf("%s mismatched durable pre-dispatch TDD fields: %s", prefix, strings.Join(mismatches, ", ")))
		}
	}
	return errors.Join(errs...)
}

func ValidateDurableState(state FeatureState) error {
	var errs []error
	if state.SchemaVersion != "" && state.SchemaVersion != SchemaVersion && state.SchemaVersion != FeatureStateSchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported feature state schema %q", state.SchemaVersion))
	}
	if strings.TrimSpace(state.FeatureID) == "" && strings.TrimSpace(state.SessionID) == "" {
		errs = append(errs, errors.New("feature state missing feature_id or session_id"))
	}
	if strings.TrimSpace(state.Status) == "" {
		errs = append(errs, errors.New("feature state missing status"))
	} else if !isValidFeatureState(state.Status) {
		errs = append(errs, fmt.Errorf("feature state has unknown status %q", state.Status))
	}
	if state.Revision < 0 {
		errs = append(errs, errors.New("feature state revision must be non-negative"))
	}
	if isTerminalFeatureState(state.Status) {
		if len(state.ActiveRuns) > 0 {
			errs = append(errs, errors.New("terminal feature state has active runs"))
		}
		if hasOpenQuestion(state.OpenQuestions) || len(state.PlanningLoop.OpenQuestionIDs) > 0 {
			errs = append(errs, errors.New("terminal feature state has open planner questions"))
		}
		if len(state.OpenRecoveryPackets) > 0 {
			errs = append(errs, errors.New("terminal feature state has open recovery packets"))
		}
	}
	for i, atom := range state.Atoms {
		if strings.TrimSpace(atom.State) == "" {
			errs = append(errs, fmt.Errorf("atom %d missing state", i))
		} else if !isValidAtomState(atom.State) {
			errs = append(errs, fmt.Errorf("atom %d has unknown state %q", i, atom.State))
		}
	}
	for i, question := range state.OpenQuestions {
		if err := ValidatePlannerQuestion(question); err != nil {
			errs = append(errs, fmt.Errorf("planner question %d invalid: %w", i, err))
		}
	}
	for i, run := range state.ActiveRuns {
		if strings.TrimSpace(run.RunID) == "" && strings.TrimSpace(run.LaneSessionID) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing run identity", i))
		}
		if strings.TrimSpace(run.AtomID) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing atom_id", i))
		}
		if strings.TrimSpace(run.State) == "" {
			errs = append(errs, fmt.Errorf("active run %d missing state", i))
		}
	}
	for i, evidence := range state.TDDPreExecutionEvidence {
		if strings.TrimSpace(evidence.EventID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing event_id", i))
		}
		if strings.TrimSpace(evidence.AtomID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing atom_id", i))
		}
		if strings.TrimSpace(evidence.SnapshotID) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing snapshot_id", i))
		}
		if strings.TrimSpace(evidence.SnapshotDigest) == "" {
			errs = append(errs, fmt.Errorf("tdd pre-execution evidence %d missing snapshot_digest", i))
		}
	}
	return errors.Join(errs...)
}

func ApplyPlannerOutput(state *FeatureState, parsed ParsedPlannerOutput, now time.Time) []Event {
	if state == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.PlanningLoop.Iteration++
	switch parsed.Kind {
	case ToolAskPlannerQuestion:
		question := normalizePlannerQuestion(parsed.Question, fmt.Sprintf("q-%03d", len(state.OpenQuestions)+1), "", now)
		if err := ValidatePlannerQuestion(question); err != nil {
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return []Event{{Type: "idiom.feature_planner.planner_question_rejected", SessionID: state.SessionID, Message: err.Error()}}
		}
		state.OpenQuestions = append(state.OpenQuestions, question)
		state.Status = FeatureStatePlanningQuestion
		state.PlanningLoop.State = FeatureStatePlanningQuestion
		return []Event{{Type: "idiom.feature_planner.planner_question_opened", SessionID: state.SessionID, Message: question.QuestionSummary}}
	case ToolSubmitFeaturePlan:
		hydrateDurablePlanDefaults(&parsed.Plan, state, now)
		if err := ValidateDurablePlan(parsed.Plan); err != nil {
			state.Plan = parsed.Plan
			state.Status = FeatureStatePlanningRepair
			state.PlanningLoop.State = FeatureStatePlanningRepair
			state.Blockers = append(state.Blockers, err.Error())
			return []Event{{Type: "idiom.feature_planner.tdd_pre_execution_blocked", SessionID: state.SessionID, Message: err.Error()}}
		}
		state.Plan = parsed.Plan
		state.Atoms = make([]FeatureAtomState, 0, len(parsed.Plan.Atoms))
		for _, atom := range parsed.Plan.Atoms {
			state.Atoms = append(state.Atoms, FeatureAtomState{
				FeatureAtomPlan: atom,
				State:           AtomStateQueued,
			})
		}
		state.Status = FeatureStateDispatching
		state.PlanningLoop.State = FeatureStateDispatching
		if len(state.Atoms) > 0 {
			state.Continuation.NextAtomID = state.Atoms[0].ID
			state.Continuation.ResumeAction = "dispatch_next_atom"
		}
		events := []Event{
			{Type: "idiom.feature_planner.plan_accepted", SessionID: state.SessionID, FeatureTo: FeatureStatePlanReview, Fields: map[string]any{"atom_count": len(state.Atoms)}},
			{Type: "idiom.feature_planner.plan_reviewed", SessionID: state.SessionID, FeatureFrom: FeatureStatePlanReview, FeatureTo: FeatureStateAwaitingApproval, Fields: map[string]any{"review": "durable_plan_contract_passed"}},
			{Type: "idiom.feature_planner.plan_approved", SessionID: state.SessionID, FeatureFrom: FeatureStateAwaitingApproval, FeatureTo: FeatureStateAtomizing, Fields: map[string]any{"approval": "harness_contract_approval"}},
			{Type: "idiom.feature_planner.atoms_compiled", SessionID: state.SessionID, FeatureFrom: FeatureStateAtomizing, FeatureTo: FeatureStateReady, Fields: map[string]any{"atom_count": len(state.Atoms)}},
		}
		events = append(events, recordTDDPreExecutionEvidence(state, now)...)
		events = append(events, Event{Type: "idiom.feature_planner.dispatch_ready", SessionID: state.SessionID, FeatureFrom: FeatureStateReady, FeatureTo: FeatureStateDispatching})
		return events
	default:
		state.Status = FeatureStateMalformedOutput
		state.PlanningLoop.State = FeatureStateMalformedOutput
		state.LastError = "unsupported parsed planner output"
		return []Event{{Type: "idiom.feature_planner.malformed_structured_output", SessionID: state.SessionID, Message: state.LastError}}
	}
}

func recordTDDPreExecutionEvidence(state *FeatureState, now time.Time) []Event {
	if state == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdAt := now.UTC().Format(time.RFC3339Nano)
	var events []Event
	for _, atom := range state.Atoms {
		evidence := tddEvidenceFromAtom(atom.FeatureAtomPlan, createdAt)
		if evidence.EventID == "" {
			continue
		}
		upsertTDDEvidence(state, evidence)
		events = append(events, Event{
			Type:      "idiom.feature_planner.tdd_pre_execution_checked",
			SessionID: state.SessionID,
			AtomID:    atom.ID,
			EventID:   evidence.EventID,
			Actor:     "harness",
			Fields: map[string]any{
				"obligation_refs":               evidence.ObligationRefs,
				"commands":                      evidence.Commands,
				"test_artifact_paths":           evidence.TestArtifactPaths,
				"expected_pre_execution_result": evidence.ExpectedPreExecutionResult,
				"coverage_gate_event_id":        evidence.CoverageGateEventID,
				"snapshot_id":                   evidence.SnapshotID,
				"snapshot_digest":               evidence.SnapshotDigest,
			},
		})
	}
	return events
}

func tddEvidenceFromAtom(atom FeatureAtomPlan, createdAt string) TDDPreExecutionEvidence {
	return TDDPreExecutionEvidence{
		EventID:                    strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID),
		AtomID:                     strings.TrimSpace(atom.ID),
		ObligationRefs:             normalizedStringList(atom.TestFirst.ObligationRefs),
		Commands:                   normalizedStringList(atom.TestFirst.Commands),
		TestArtifactPaths:          normalizedStringList(atom.TestFirst.TestArtifactPaths),
		ExpectedPreExecutionResult: strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult),
		CoverageGateEventID:        strings.TrimSpace(atom.TestFirst.CoverageGateEventID),
		SnapshotID:                 strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID),
		SnapshotDigest:             strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest),
		CreatedAt:                  createdAt,
	}
}

func upsertTDDEvidence(state *FeatureState, evidence TDDPreExecutionEvidence) {
	if state == nil || evidence.EventID == "" || evidence.AtomID == "" {
		return
	}
	for i := range state.TDDPreExecutionEvidence {
		existing := state.TDDPreExecutionEvidence[i]
		if existing.EventID == evidence.EventID && existing.AtomID == evidence.AtomID {
			state.TDDPreExecutionEvidence[i] = evidence
			return
		}
	}
	state.TDDPreExecutionEvidence = append(state.TDDPreExecutionEvidence, evidence)
}

func MarkPlanningStuck(state *FeatureState, reason string) {
	if state == nil {
		return
	}
	state.Status = FeatureStatePlanningStuck
	state.PlanningLoop.State = FeatureStatePlanningStuck
	state.PlanningLoop.StuckReason = strings.TrimSpace(reason)
	if state.PlanningLoop.StuckReason == "" {
		state.PlanningLoop.StuckReason = "planning loop exceeded maximum iterations"
	}
}

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}

func MissingStrictPreDispatchTDD(atom FeatureAtomPlan) []string {
	missing := MissingPreDispatchPlanTDD(atom)
	if len(atom.TestFirst.TestArtifactPaths) == 0 {
		missing = append(missing, "test_first.test_artifact_paths")
	}
	if len(atom.TestFirst.PreExecutionEventIDs) == 0 {
		missing = append(missing, "test_first.pre_execution_event_ids")
	}
	if strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult) == "" {
		missing = append(missing, "test_first.expected_pre_execution_result")
	}
	if strings.TrimSpace(atom.TestFirst.CoverageGateEventID) == "" {
		missing = append(missing, "test_first.coverage_gate_event_id")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotID) == "" {
		missing = append(missing, "planned_tdd_snapshot.snapshot_id")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.SnapshotDigest) == "" {
		missing = append(missing, "planned_tdd_snapshot.snapshot_digest")
	}
	if len(atom.PlannedTDDSnapshot.ObligationIDs) == 0 {
		missing = append(missing, "planned_tdd_snapshot.obligation_ids")
	}
	if len(atom.PlannedTDDSnapshot.TestCommands) == 0 {
		missing = append(missing, "planned_tdd_snapshot.test_commands")
	}
	if len(atom.PlannedTDDSnapshot.TestArtifactPaths) == 0 {
		missing = append(missing, "planned_tdd_snapshot.test_artifact_paths")
	}
	if len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) == 0 {
		missing = append(missing, "planned_tdd_snapshot.expected_pre_execution_results")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) == "" {
		missing = append(missing, "planned_tdd_snapshot.created_before_dispatch_event_id")
	}
	return missing
}

func MismatchedStrictPreDispatchTDD(atom FeatureAtomPlan) []string {
	var mismatches []string
	if len(atom.TestFirst.ObligationRefs) > 0 && len(atom.PlannedTDDSnapshot.ObligationIDs) > 0 &&
		!sameStringSet(atom.TestFirst.ObligationRefs, atom.PlannedTDDSnapshot.ObligationIDs) {
		mismatches = append(mismatches, "planned_tdd_snapshot.obligation_ids")
	}
	if len(atom.TestFirst.Commands) > 0 && len(atom.PlannedTDDSnapshot.TestCommands) > 0 &&
		!sameStringSet(atom.TestFirst.Commands, atom.PlannedTDDSnapshot.TestCommands) {
		mismatches = append(mismatches, "planned_tdd_snapshot.test_commands")
	}
	if len(atom.TestFirst.TestArtifactPaths) > 0 && len(atom.PlannedTDDSnapshot.TestArtifactPaths) > 0 &&
		!sameStringSet(atom.TestFirst.TestArtifactPaths, atom.PlannedTDDSnapshot.TestArtifactPaths) {
		mismatches = append(mismatches, "planned_tdd_snapshot.test_artifact_paths")
	}
	if strings.TrimSpace(atom.TestFirst.ExpectedPreExecutionResult) != "" &&
		len(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults) > 0 &&
		!containsString(atom.PlannedTDDSnapshot.ExpectedPreExecutionResults, atom.TestFirst.ExpectedPreExecutionResult) {
		mismatches = append(mismatches, "planned_tdd_snapshot.expected_pre_execution_results")
	}
	if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) != "" &&
		len(atom.TestFirst.PreExecutionEventIDs) > 0 &&
		!containsString(atom.TestFirst.PreExecutionEventIDs, atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) {
		mismatches = append(mismatches, "planned_tdd_snapshot.created_before_dispatch_event_id")
	}
	return mismatches
}

func ValidatePlannerQuestion(question PlannerQuestion) error {
	var errs []error
	require(&errs, question.ID, "planner question missing id")
	require(&errs, question.QuestionSummary, "planner question missing question_summary")
	require(&errs, question.QuestionKind, "planner question missing question_kind")
	require(&errs, question.BlockingClaim, "planner question missing blocking_claim")
	require(&errs, question.RequestedDecision, "planner question missing requested_decision")
	require(&errs, question.AuthorityRequired, "planner question missing authority_required")
	require(&errs, question.RequiredBefore, "planner question missing required_before")
	if question.QuestionKind != "" && !isValidQuestionKind(question.QuestionKind) {
		errs = append(errs, fmt.Errorf("planner question has unknown question_kind %q", question.QuestionKind))
	}
	if question.AuthorityRequired != "" && !isValidQuestionAuthority(question.AuthorityRequired) {
		errs = append(errs, fmt.Errorf("planner question has unknown authority_required %q", question.AuthorityRequired))
	}
	if question.Status != "" && question.Status != "open" && question.Status != "answered" && question.Status != "rejected" {
		errs = append(errs, fmt.Errorf("planner question has unknown status %q", question.Status))
	}
	return errors.Join(errs...)
}

func MissingPreDispatchPlanTDD(atom FeatureAtomPlan) []string {
	var missing []string
	if !atom.TestFirst.BaselineRequired {
		missing = append(missing, "test_first.baseline_required")
	}
	if len(atom.TestFirst.Commands) == 0 {
		missing = append(missing, "test_first.commands")
	}
	if len(atom.TestFirst.ObligationRefs) == 0 {
		missing = append(missing, "test_first.obligation_refs")
	}
	return missing
}

func isTerminalFeatureState(status string) bool {
	switch status {
	case FeatureStateReleased, FeatureStateClosedWithRisk, FeatureStateCanceled, FeatureStateComplete:
		return true
	default:
		return false
	}
}

func isValidFeatureState(status string) bool {
	switch status {
	case FeatureStateDraftingPlan, FeatureStatePlanning, FeatureStatePlanningRepair, FeatureStatePlanningQuestion,
		FeatureStatePlanningStuck, FeatureStatePlanReview, FeatureStateAwaitingApproval, FeatureStateAtomizing,
		FeatureStateReplanning, FeatureStateReady, FeatureStateDispatching, FeatureStateVerifying,
		FeatureStateReviewing, FeatureStateReconciling, FeatureStateNeedsContextRefresh,
		FeatureStateRecoveryOpen, FeatureStateBlocked, FeatureStateReleaseReview, FeatureStateReleased,
		FeatureStateClosedWithRisk, FeatureStateCanceled, FeatureStateInterrupted, FeatureStateComplete,
		FeatureStateFailed, FeatureStateSourceSatisfied, FeatureStateMalformedOutput, FeatureStateStaleEditAnchor:
		return true
	default:
		return false
	}
}

func isValidAtomState(status string) bool {
	switch status {
	case AtomStatePlanned, AtomStateQueued, AtomStateDispatchable, AtomStateDispatching, AtomStateImplementing,
		AtomStateQuestionForPlanner, AtomStateQuestionForOperator, AtomStateSourceSatisfied,
		AtomStateMalformedOutput, AtomStateStaleEditAnchor, AtomStateVerifying, AtomStateReviewing,
		AtomStateNeedsContextRefresh, AtomStateComplete, AtomStateStaleNeedsRebase,
		AtomStateBlockedDirtyFiles, AtomStateMissingScope, AtomStateInvalidScope, AtomStateFailedRefineDraft,
		AtomStateRecoveryOpen, AtomStateBlocked, AtomStateSkipped, AtomStateFailed, AtomStateCanceled,
		AtomStateInterrupted, AtomStateBlockedMissingTDD:
		return true
	default:
		return false
	}
}

func isValidQuestionKind(kind string) bool {
	switch kind {
	case "missing_spec", "conflicting_spec", "scope_split_needed", "test_gap", "stale_context", "unsafe_scope", "external_answer_required":
		return true
	default:
		return false
	}
}

func isValidQuestionAuthority(authority string) bool {
	switch authority {
	case "accepted_source", "operator_approval", "harness_policy", "fresh_context", "atom_local_evidence":
		return true
	default:
		return false
	}
}

func hasOpenQuestion(questions []PlannerQuestion) bool {
	for _, question := range questions {
		if question.Status == "open" {
			return true
		}
	}
	return false
}

func sameStringSet(left []string, right []string) bool {
	leftSet := normalizedStringSet(left)
	rightSet := normalizedStringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if !rightSet[value] {
			return false
		}
	}
	return true
}

func normalizedStringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func normalizedStringList(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsString(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func normalizePlannerQuestion(question PlannerQuestion, fallbackID string, atomID string, now time.Time) PlannerQuestion {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(question.ID) == "" {
		question.ID = strings.TrimSpace(question.QuestionID)
	}
	if strings.TrimSpace(question.ID) == "" {
		question.ID = fallbackID
	}
	if strings.TrimSpace(question.QuestionID) == "" {
		question.QuestionID = question.ID
	}
	if strings.TrimSpace(question.AtomID) == "" {
		question.AtomID = atomID
	}
	if strings.TrimSpace(question.QuestionKind) == "" {
		question.QuestionKind = "external_answer_required"
	}
	if strings.TrimSpace(question.BlockingClaim) == "" {
		question.BlockingClaim = question.QuestionSummary
	}
	if strings.TrimSpace(question.RequestedDecision) == "" {
		question.RequestedDecision = question.QuestionSummary
	}
	if strings.TrimSpace(question.AuthorityRequired) == "" {
		question.AuthorityRequired = "operator_approval"
	}
	if strings.TrimSpace(question.RequiredBefore) == "" {
		question.RequiredBefore = "plan_approval"
		if atomID != "" {
			question.RequiredBefore = "atom_dispatch"
		}
	}
	if strings.TrimSpace(question.Status) == "" {
		question.Status = "open"
	}
	if strings.TrimSpace(question.AskedAt) == "" {
		question.AskedAt = now.UTC().Format(time.RFC3339Nano)
	}
	return question
}

func hydrateDurablePlanDefaults(plan *FeaturePlan, state *FeatureState, now time.Time) {
	if plan == nil || state == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(plan.SchemaVersion) == "" {
		plan.SchemaVersion = FeaturePlanSchemaVersion
	}
	if strings.TrimSpace(plan.FeatureID) == "" {
		plan.FeatureID = firstNonEmpty(state.FeatureID, state.SessionID)
	}
	if strings.TrimSpace(plan.SourceTask.Kind) == "" {
		plan.SourceTask.Kind = "operator_prompt"
	}
	if strings.TrimSpace(plan.SourceTask.Summary) == "" {
		plan.SourceTask.Summary = state.PromptSHA256
	}
	for i := range plan.Atoms {
		atom := &plan.Atoms[i]
		if strings.TrimSpace(atom.SchemaVersion) == "" {
			atom.SchemaVersion = FeatureAtomSchemaVersion
		}
		if strings.TrimSpace(atom.FeatureID) == "" {
			atom.FeatureID = plan.FeatureID
		}
		if strings.TrimSpace(atom.AtomID) == "" {
			atom.AtomID = atom.ID
		}
		if strings.TrimSpace(atom.TestFirst.CoverageGateEventID) == "" && len(atom.TestFirst.PreExecutionEventIDs) > 0 {
			atom.TestFirst.CoverageGateEventID = atom.TestFirst.PreExecutionEventIDs[0]
		}
		if strings.TrimSpace(atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID) == "" && len(atom.TestFirst.PreExecutionEventIDs) > 0 {
			atom.PlannedTDDSnapshot.CreatedBeforeDispatchEventID = atom.TestFirst.PreExecutionEventIDs[0]
		}
		_ = now
	}
}

func validateAtomDependencies(atoms []FeatureAtomPlan) error {
	ids := map[string]bool{}
	depsByAtom := map[string][]string{}
	var errs []error
	for i, atom := range atoms {
		id := strings.TrimSpace(atom.ID)
		if id == "" {
			continue
		}
		ids[id] = true
		for _, dep := range atom.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				errs = append(errs, fmt.Errorf("atom %d has empty dependency", i))
				continue
			}
			if dep == id {
				errs = append(errs, fmt.Errorf("atom %d depends on itself %q", i, id))
				continue
			}
			depsByAtom[id] = append(depsByAtom[id], dep)
		}
	}
	for id, deps := range depsByAtom {
		for _, dep := range deps {
			if !ids[dep] {
				errs = append(errs, fmt.Errorf("atom %q depends on unknown atom %q", id, dep))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("atom dependency cycle includes %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range depsByAtom[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range ids {
		if err := visit(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
