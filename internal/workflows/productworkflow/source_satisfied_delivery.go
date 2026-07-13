package productworkflow

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	plancontract "idiom/internal/contracts/plan"
)

type SourceSatisfiedDeliveryRequest struct {
	EffortRoot       string
	ExpectedEffortID string
	ExpectedTaskID   string
	Completion       plancontract.PlanExecuteTerminalCompletionRecord
}

// RecordSourceSatisfiedDelivery records the evidence-backed delivery outcome
// after artifact creation has reached its independent terminal phase.
func RecordSourceSatisfiedDelivery(request SourceSatisfiedDeliveryRequest) (WorkflowState, error) {
	expectedEffortID := strings.TrimSpace(request.ExpectedEffortID)
	expectedTaskID := strings.TrimSpace(request.ExpectedTaskID)
	if expectedEffortID == "" || expectedTaskID == "" {
		return WorkflowState{}, fmt.Errorf("source-satisfied delivery requires expected effort_id and task_id")
	}
	completion, err := validateSourceSatisfiedTerminalCompletion(request.Completion)
	if err != nil {
		return WorkflowState{}, err
	}

	lease, err := openProductWorkflowMutationLease(request.EffortRoot)
	if err != nil {
		return WorkflowState{}, fmt.Errorf("acquire source-satisfied delivery lease: %w", err)
	}
	defer lease.Close()

	state, phase, err := inspectWorkflowAuthority(lease.root)
	if err != nil {
		return WorkflowState{}, fmt.Errorf("inspect source-satisfied delivery workflow authority: %w", err)
	}
	if phase != PhaseComplete {
		return WorkflowState{}, fmt.Errorf("source-satisfied delivery requires workflow phase %q, got %q", PhaseComplete, phase)
	}
	if strings.TrimSpace(state.EffortID) != expectedEffortID || strings.TrimSpace(state.TaskID) != expectedTaskID {
		return WorkflowState{}, fmt.Errorf(
			"source-satisfied delivery effort/task mismatch: expected %s/%s, found %s/%s",
			expectedEffortID,
			expectedTaskID,
			strings.TrimSpace(state.EffortID),
			strings.TrimSpace(state.TaskID),
		)
	}
	closeout, err := sourceSatisfiedDeliveryCloseout(completion, state.EffortID, state.TaskID)
	if err != nil {
		return WorkflowState{}, err
	}
	eventRevision := state.StateRevision + 1
	if state.SourceSatisfiedDelivery != nil {
		eventRevision = state.StateRevision
	}
	event := sourceSatisfiedDeliveryEvent(state, closeout, eventRevision)
	if state.SourceSatisfiedDelivery != nil {
		if sourceSatisfiedDeliveryCloseoutsEqual(*state.SourceSatisfiedDelivery, closeout) {
			eventsPath, pathErr := productWorkflowEventsPath(state)
			if pathErr != nil {
				return WorkflowState{}, pathErr
			}
			exists, _, inspectErr := inspectProductWorkflowTransitionEvent(lease.root, eventsPath, event)
			if inspectErr != nil {
				return WorkflowState{}, fmt.Errorf("verify recorded source-satisfied event: %w", inspectErr)
			}
			if !exists {
				return WorkflowState{}, fmt.Errorf(
					"source-satisfied delivery state references missing event_id %q",
					event.EventID,
				)
			}

			return state, nil
		}

		return WorkflowState{}, fmt.Errorf(
			"source-satisfied delivery already records completion %q",
			strings.TrimSpace(state.SourceSatisfiedDelivery.CompletionDigest),
		)
	}

	state.SourceSatisfiedDelivery = &closeout
	state.UpdatedAt = closeout.RecordedAt
	state.StateRevision++
	state.LastEventID = event.EventID
	if err := validateWorkflowState(state); err != nil {
		return WorkflowState{}, fmt.Errorf("validate source-satisfied workflow state: %w", err)
	}
	if _, err := validateCreationWorkflowAuthority(state); err != nil {
		return WorkflowState{}, fmt.Errorf("validate source-satisfied creation authority: %w", err)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return WorkflowState{}, fmt.Errorf("validate source-satisfied delivery effort root: %w", err)
	}
	if err := writeProductWorkflowTransition(lease, state, event); err != nil {
		return WorkflowState{}, fmt.Errorf("record source-satisfied delivery: %w", err)
	}

	return state, nil
}

func validateSourceSatisfiedTerminalCompletion(
	record plancontract.PlanExecuteTerminalCompletionRecord,
) (plancontract.PlanExecuteTerminalCompletionRecord, error) {
	record = plancontract.NormalizePlanExecuteTerminalCompletionRecord(record)
	if err := plancontract.ValidatePlanExecuteTerminalCompletionRecord(record); err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf("validate source-satisfied terminal completion: %w", err)
	}
	if strings.TrimSpace(record.SessionID) == "" {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf("source-satisfied terminal completion requires session_id")
	}
	if record.TS != strings.TrimSpace(record.TS) {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf("source-satisfied terminal completion timestamp must be normalized")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.TS); err != nil {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf("source-satisfied terminal completion requires a valid timestamp: %w", err)
	}
	terminalClass, outcome := plancontract.ClassifyPlanExecuteTerminalCompletion(record)
	if terminalClass != plancontract.PlanExecuteTerminalClassSuccess || outcome != plancontract.PlanExecuteTerminalOutcomeSourceSatisfied {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf(
			"source-satisfied delivery requires success/source_satisfied terminal authority, got %s/%s",
			terminalClass,
			outcome,
		)
	}
	if len(record.ValidationFailures) > 0 {
		return plancontract.PlanExecuteTerminalCompletionRecord{}, fmt.Errorf(
			"source-satisfied terminal completion evidence failed validation: %s",
			strings.Join(record.ValidationFailures, ", "),
		)
	}

	return record, nil
}

func sourceSatisfiedDeliveryCloseout(
	completion plancontract.PlanExecuteTerminalCompletionRecord,
	effortID string,
	taskID string,
) (SourceSatisfiedDeliveryCloseout, error) {
	closeout := SourceSatisfiedDeliveryCloseout{
		EffortID:      strings.TrimSpace(effortID),
		TaskID:        strings.TrimSpace(taskID),
		Outcome:       plancontract.PlanExecuteTerminalOutcomeSourceSatisfied,
		SessionID:     strings.TrimSpace(completion.SessionID),
		EvidencePaths: append([]string(nil), completion.SourceSatisfiedEvidencePaths...),
		Evidence:      append([]plancontract.SourceSatisfiedEvidenceRef(nil), completion.SourceSatisfiedEvidence...),
		RecordedAt:    completion.TS,
	}
	completionDigest, err := sourceSatisfiedDeliveryCompletionDigest(closeout)
	if err != nil {
		return SourceSatisfiedDeliveryCloseout{}, err
	}
	closeout.CompletionDigest = completionDigest
	if err := ValidateSourceSatisfiedDeliveryCloseout(closeout); err != nil {
		return SourceSatisfiedDeliveryCloseout{}, err
	}

	return closeout, nil
}

// ValidateSourceSatisfiedDeliveryCloseout verifies the durable, current-state
// evidence projection before another workflow treats it as terminal authority.
func ValidateSourceSatisfiedDeliveryCloseout(closeout SourceSatisfiedDeliveryCloseout) error {
	if strings.TrimSpace(closeout.EffortID) == "" || strings.TrimSpace(closeout.TaskID) == "" ||
		closeout.EffortID != strings.TrimSpace(closeout.EffortID) || closeout.TaskID != strings.TrimSpace(closeout.TaskID) {
		return fmt.Errorf("source-satisfied delivery effort_id and task_id must be normalized and non-empty")
	}
	if closeout.Outcome != plancontract.PlanExecuteTerminalOutcomeSourceSatisfied {
		return fmt.Errorf("source-satisfied delivery has invalid outcome %q", closeout.Outcome)
	}
	if strings.TrimSpace(closeout.SessionID) == "" || closeout.SessionID != strings.TrimSpace(closeout.SessionID) {
		return fmt.Errorf("source-satisfied delivery session_id must be normalized and non-empty")
	}
	digest := strings.TrimPrefix(strings.TrimSpace(closeout.CompletionDigest), "sha256:")
	if !validWorkflowArtifactDigest(digest) {
		return fmt.Errorf("source-satisfied delivery has invalid completion_digest")
	}
	paths := plancontract.UniqueScopePaths(closeout.EvidencePaths)
	if len(paths) == 0 || !slices.Equal(paths, closeout.EvidencePaths) {
		return fmt.Errorf("source-satisfied delivery evidence_paths must be normalized, unique, and non-empty")
	}
	evidence := plancontract.NormalizeSourceSatisfiedEvidenceRefs(closeout.Evidence)
	if len(evidence) == 0 || !slices.Equal(evidence, closeout.Evidence) {
		return fmt.Errorf("source-satisfied delivery evidence must be normalized, unique, and non-empty")
	}
	if violations := plancontract.ValidateSourceSatisfiedPathCoverage(plancontract.SourceSatisfiedPathCoverageInput{
		RequiredPaths:        paths,
		SourceSatisfiedPaths: paths,
		Evidence:             evidence,
		RequireEvidence:      true,
	}); len(violations) > 0 {
		return fmt.Errorf("source-satisfied delivery evidence is not current-state grounded: %s:%s", violations[0].Code, violations[0].Path)
	}
	recordedAt := strings.TrimSpace(closeout.RecordedAt)
	if recordedAt == "" || closeout.RecordedAt != recordedAt {
		return fmt.Errorf("source-satisfied delivery recorded_at must be normalized and non-empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, recordedAt); err != nil {
		return fmt.Errorf("source-satisfied delivery has invalid recorded_at: %w", err)
	}
	expectedDigest, err := sourceSatisfiedDeliveryCompletionDigest(closeout)
	if err != nil {
		return err
	}
	if closeout.CompletionDigest != expectedDigest {
		return fmt.Errorf("source-satisfied delivery completion_digest does not match its durable authority")
	}

	return nil
}

func sourceSatisfiedDeliveryCompletionDigest(closeout SourceSatisfiedDeliveryCloseout) (string, error) {
	authority := struct {
		EffortID      string                                    `json:"effort_id"`
		TaskID        string                                    `json:"task_id"`
		Outcome       plancontract.PlanExecuteTerminalOutcome   `json:"outcome"`
		SessionID     string                                    `json:"session_id"`
		EvidencePaths []string                                  `json:"evidence_paths"`
		Evidence      []plancontract.SourceSatisfiedEvidenceRef `json:"evidence"`
		RecordedAt    string                                    `json:"recorded_at"`
	}{
		EffortID:      closeout.EffortID,
		TaskID:        closeout.TaskID,
		Outcome:       closeout.Outcome,
		SessionID:     closeout.SessionID,
		EvidencePaths: closeout.EvidencePaths,
		Evidence:      closeout.Evidence,
		RecordedAt:    closeout.RecordedAt,
	}
	body, err := json.Marshal(authority)
	if err != nil {
		return "", fmt.Errorf("encode source-satisfied delivery authority: %w", err)
	}

	return fmt.Sprintf("sha256:%x", sha256.Sum256(body)), nil
}

func sourceSatisfiedDeliveryCloseoutsEqual(left, right SourceSatisfiedDeliveryCloseout) bool {
	return left.EffortID == right.EffortID &&
		left.TaskID == right.TaskID &&
		left.Outcome == right.Outcome &&
		left.SessionID == right.SessionID &&
		left.CompletionDigest == right.CompletionDigest &&
		slices.Equal(left.EvidencePaths, right.EvidencePaths) &&
		slices.Equal(left.Evidence, right.Evidence) &&
		left.RecordedAt == right.RecordedAt
}

func sourceSatisfiedDeliveryEvent(
	state WorkflowState,
	closeout SourceSatisfiedDeliveryCloseout,
	stateRevision int,
) ProductWorkflowTransitionEvent {
	return ProductWorkflowTransitionEvent{
		Event:     string(ProductWorkflowEventSourceSatisfiedRecorded),
		EventID:   sourceSatisfiedDeliveryEventID(closeout.CompletionDigest),
		EffortID:  strings.TrimSpace(state.EffortID),
		TaskID:    strings.TrimSpace(state.TaskID),
		Timestamp: closeout.RecordedAt,
		Source:    string(ProductWorkflowEventSourceSatisfiedDelivery),
		Action:    "record_source_satisfied_delivery",
		Details: ProductWorkflowTransitionEventDetail{
			FromState:        strings.TrimSpace(state.CurrentState),
			ToState:          strings.TrimSpace(state.CurrentState),
			StateRevision:    stateRevision,
			Outcome:          string(closeout.Outcome),
			SessionID:        closeout.SessionID,
			CompletionDigest: closeout.CompletionDigest,
		},
	}
}

func sourceSatisfiedDeliveryEventID(completionDigest string) string {
	digest := strings.TrimPrefix(strings.TrimSpace(completionDigest), "sha256:")

	return "evt-product-source-satisfied-" + digest
}
