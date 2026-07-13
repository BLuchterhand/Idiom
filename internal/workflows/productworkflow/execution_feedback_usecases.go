package productworkflow

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type ExecutionFeedbackArtifactsRequest struct {
	EffortRoot        string
	LaneHistoryPath   string
	PlanProgressPath  string
	VerificationPath  string
	ProductEvidence   EvidenceBundleRef
	ReleaseEvidence   EvidenceBundleRef
	ChangedPaths      []string
	BriefPath         string
	TDDObligationRefs []string
}

type RankerLearningFeedbackRequest struct {
	EffortRoot  string
	SummaryPath string
}

type RankerLearningFeedbackResult struct {
	SummaryReferencePath string
	Evidence             RankerLearningEvidenceResult
}

type ExecutionFeedbackReconciliationRequest struct {
	EffortRoot  string
	SummaryPath string
	OutputPath  string
}

type ExecutionFeedbackReconciliationUseCaseResult struct {
	SummaryReferencePath string
	OutputPath           string
	Reconciliation       ExecutionFeedbackReconciliationResult
}

func BuildExecutionFeedbackFromArtifacts(request ExecutionFeedbackArtifactsRequest) (ExecutionFeedbackSummary, error) {
	workflowState, err := loadExecutionFeedbackWorkflowState(request.EffortRoot)
	if err != nil {
		return ExecutionFeedbackSummary{}, err
	}

	var laneHistory []ExecutionLaneEventRef
	if err := readExecutionFeedbackJSON(request.EffortRoot, "lane history reference", request.LaneHistoryPath, &laneHistory, true); err != nil {
		return ExecutionFeedbackSummary{}, fmt.Errorf("lane history reference invalid: %w", err)
	}

	var planProgress PlanExecuteProgressRef
	if err := readExecutionFeedbackJSON(request.EffortRoot, "plan progress reference", request.PlanProgressPath, &planProgress, true); err != nil {
		return ExecutionFeedbackSummary{}, fmt.Errorf("plan progress reference invalid: %w", err)
	}

	var verification VerificationRef
	if err := readExecutionFeedbackJSON(request.EffortRoot, "verification reference", request.VerificationPath, &verification, true); err != nil {
		return ExecutionFeedbackSummary{}, fmt.Errorf("verification reference invalid: %w", err)
	}

	if marker := executionFeedbackPrivacyMarker(strings.Join(request.ChangedPaths, "\n")); marker != "" {
		return ExecutionFeedbackSummary{}, fmt.Errorf("changed paths contain forbidden privacy marker %q", marker)
	}
	if marker := executionFeedbackPrivacyMarker(strings.Join(request.TDDObligationRefs, "\n")); marker != "" {
		return ExecutionFeedbackSummary{}, fmt.Errorf("TDD obligation references contain forbidden privacy marker %q", marker)
	}
	if marker := executionFeedbackPrivacyMarker(strings.Join([]string{
		request.ProductEvidence.ID,
		request.ProductEvidence.Path,
		request.ReleaseEvidence.ID,
		request.ReleaseEvidence.Path,
		request.BriefPath,
		verification.Status,
		strings.Join(verification.Commands, "\n"),
	}, "\n")); marker != "" {
		return ExecutionFeedbackSummary{}, fmt.Errorf("references contain forbidden privacy marker %q", marker)
	}

	ledger, err := loadExecutionFeedbackTDDLedger(request.EffortRoot)
	if err != nil {
		return ExecutionFeedbackSummary{}, err
	}

	summary := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		EffortRoot:        request.EffortRoot,
		WorkflowState:     workflowState,
		TDDLedger:         ledger,
		LaneHistory:       laneHistory,
		PlanProgress:      planProgress,
		ProductEvidence:   EvidenceBundleRef{ID: strings.TrimSpace(request.ProductEvidence.ID), Path: strings.TrimSpace(request.ProductEvidence.Path)},
		ReleaseEvidence:   EvidenceBundleRef{ID: strings.TrimSpace(request.ReleaseEvidence.ID), Path: strings.TrimSpace(request.ReleaseEvidence.Path)},
		ChangedPaths:      request.ChangedPaths,
		Verification:      verification,
		BriefPath:         strings.TrimSpace(request.BriefPath),
		TDDObligationRefs: request.TDDObligationRefs,
	})

	return summary, nil
}

func BuildRankerLearningEvidenceFromFeedback(request RankerLearningFeedbackRequest) (RankerLearningFeedbackResult, error) {
	workflowState, err := loadExecutionFeedbackWorkflowState(request.EffortRoot)
	if err != nil {
		return RankerLearningFeedbackResult{}, err
	}

	summary, summaryRel, err := loadExecutionFeedbackSummary(request.EffortRoot, request.SummaryPath, "ranker-learning evidence summary")
	if err != nil {
		return RankerLearningFeedbackResult{}, err
	}

	ledger, err := loadExecutionFeedbackTDDLedger(request.EffortRoot)
	if err != nil {
		return RankerLearningFeedbackResult{}, err
	}

	evidence := AdaptExecutionFeedbackToRankerLearning(RankerLearningEvidenceInput{
		WorkflowState:        workflowState,
		KnownTDDObligations:  executionFeedbackTDDObligationIDs(ledger),
		Summary:              summary,
		SummaryReferencePath: filepath.ToSlash(summaryRel),
	})

	return RankerLearningFeedbackResult{
		SummaryReferencePath: filepath.ToSlash(summaryRel),
		Evidence:             evidence,
	}, nil
}

func ReconcileExecutionFeedbackFromArtifacts(request ExecutionFeedbackReconciliationRequest) (ExecutionFeedbackReconciliationUseCaseResult, error) {
	workflowState, err := loadExecutionFeedbackWorkflowState(request.EffortRoot)
	if err != nil {
		return ExecutionFeedbackReconciliationUseCaseResult{}, err
	}

	summary, summaryRel, err := loadExecutionFeedbackSummary(request.EffortRoot, request.SummaryPath, "reconcile summary")
	if err != nil {
		return ExecutionFeedbackReconciliationUseCaseResult{}, err
	}

	ledger, err := loadExecutionFeedbackTDDLedger(request.EffortRoot)
	if err != nil {
		return ExecutionFeedbackReconciliationUseCaseResult{}, err
	}

	outputRel, ok := cleanEffortRelativePath(request.OutputPath)
	if !ok {
		return ExecutionFeedbackReconciliationUseCaseResult{}, fmt.Errorf(
			"reconciled state output path %q must be effort-relative and must not contain path traversal",
			request.OutputPath,
		)
	}
	if _, err := resolveEffortArtifactPath(request.EffortRoot, outputRel, effortArtifactMayBeMissing); err != nil {
		return ExecutionFeedbackReconciliationUseCaseResult{}, fmt.Errorf("reconciled state output path invalid: %w", err)
	}
	outputKey := strings.ToLower(filepath.ToSlash(filepath.Clean(outputRel)))
	reservedOutputs := map[string]string{
		strings.ToLower(filepath.ToSlash(filepath.Clean(WorkflowStateFileName))): WorkflowStateFileName,
		strings.ToLower(filepath.ToSlash(filepath.Clean(summaryRel))):            filepath.ToSlash(summaryRel),
		strings.ToLower(filepath.ToSlash(filepath.Clean("tdd.json"))):            "tdd.json",
	}
	if reservedPath, reserved := reservedOutputs[outputKey]; reserved {
		return ExecutionFeedbackReconciliationUseCaseResult{}, fmt.Errorf(
			"reconciled state output must not overwrite authority input %s",
			reservedPath,
		)
	}

	reconciledAt := strings.TrimSpace(workflowState.UpdatedAt)
	if len(summary.LaneHistory) > 0 {
		if timestamp := strings.TrimSpace(summary.LaneHistory[len(summary.LaneHistory)-1].Timestamp); timestamp != "" {
			reconciledAt = timestamp
		}
	}

	reconciliation := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:        workflowState,
		Summary:              summary,
		ReconciledAt:         reconciledAt,
		SummaryReferencePath: filepath.ToSlash(summaryRel),
		ReconciledOutputPath: filepath.ToSlash(outputRel),
		KnownTDDObligations:  executionFeedbackTDDObligationIDs(ledger),
	})
	if reconciliation.Status == ProductWorkflowStatusPass {
		stateBytes, err := json.MarshalIndent(reconciliation.WorkflowState, "", "  ")
		if err != nil {
			return ExecutionFeedbackReconciliationUseCaseResult{}, fmt.Errorf("marshal reconciled workflow state: %w", err)
		}
		if err := writeEffortArtifactAtomic(request.EffortRoot, filepath.ToSlash(outputRel), append(stateBytes, '\n'), 0o644); err != nil {
			return ExecutionFeedbackReconciliationUseCaseResult{}, fmt.Errorf("write reconciled state output: %w", err)
		}
	}

	return ExecutionFeedbackReconciliationUseCaseResult{
		SummaryReferencePath: filepath.ToSlash(summaryRel),
		OutputPath:           filepath.ToSlash(outputRel),
		Reconciliation:       reconciliation,
	}, nil
}

func loadExecutionFeedbackWorkflowState(effortRoot string) (WorkflowState, error) {
	data, err := readExecutionFeedbackFile(effortRoot, WorkflowStateFileName, false, "workflow_state")
	if err != nil {
		return WorkflowState{}, fmt.Errorf("%s invalid: %w", WorkflowStateFileName, err)
	}

	parsed, err := ParseWorkflowStateJSON(data)
	if err != nil {
		return WorkflowState{}, fmt.Errorf("%s parse failed: %w", WorkflowStateFileName, err)
	}

	return parsed, nil
}

func loadExecutionFeedbackTDDLedger(effortRoot string) (TDDLedger, error) {
	data, err := readExecutionFeedbackFile(effortRoot, "tdd.json", false, "tdd")
	if err != nil {
		return TDDLedger{}, fmt.Errorf("tdd.json invalid: %w", err)
	}

	parsed, err := ParseTDDLedgerJSON(data)
	if err != nil {
		return TDDLedger{}, fmt.Errorf("tdd.json parse failed: %w", err)
	}

	return parsed, nil
}

func loadExecutionFeedbackSummary(effortRoot string, summaryPath string, label string) (ExecutionFeedbackSummary, string, error) {
	summaryRel, ok := cleanEffortRelativePath(summaryPath)
	if !ok {
		return ExecutionFeedbackSummary{}, "", fmt.Errorf(
			"%s path %q must be effort-relative and must not contain path traversal",
			label,
			summaryPath,
		)
	}

	data, err := readEffortArtifact(effortRoot, summaryRel)
	if err != nil {
		return ExecutionFeedbackSummary{}, "", fmt.Errorf("%s invalid: %w", label, err)
	}
	if marker := executionFeedbackPrivacyMarker(string(data)); marker != "" {
		return ExecutionFeedbackSummary{}, "", fmt.Errorf("%s contains forbidden privacy marker %q", label, marker)
	}

	summary, wrappedStatus, err := parseExecutionFeedbackSummaryDocument(data)
	if err != nil {
		return ExecutionFeedbackSummary{}, "", err
	}
	if wrappedStatus != "" && !executionFeedbackPassingStatus(wrappedStatus) {
		return ExecutionFeedbackSummary{}, "", fmt.Errorf("%s status must be pass, got %q", label, wrappedStatus)
	}

	return summary, summaryRel, nil
}

func parseExecutionFeedbackSummaryDocument(data []byte) (ExecutionFeedbackSummary, string, error) {
	var wrapped struct {
		Status  ProductWorkflowStatus    `json:"status"`
		Summary ExecutionFeedbackSummary `json:"summary"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && strings.TrimSpace(string(wrapped.Summary.Status)) != "" {
		return wrapped.Summary, strings.TrimSpace(string(wrapped.Status)), nil
	}

	var summary ExecutionFeedbackSummary
	if err := json.Unmarshal(data, &summary); err == nil && strings.TrimSpace(string(summary.Status)) != "" {
		return summary, "", nil
	}

	return ExecutionFeedbackSummary{}, "", fmt.Errorf("feedback summary must be a feedback summary or wrapped execution feedback result")
}

func readExecutionFeedbackJSON(effortRoot string, kind string, relPath string, target any, scanPrivacy bool) error {
	data, err := readExecutionFeedbackFile(effortRoot, relPath, scanPrivacy, kind)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, target)
}

func readExecutionFeedbackFile(effortRoot string, relPath string, scanPrivacy bool, kind string) ([]byte, error) {
	data, err := readEffortArtifact(effortRoot, relPath)
	if err != nil {
		return nil, err
	}
	if scanPrivacy {
		if marker := executionFeedbackPrivacyMarker(string(data)); marker != "" {
			return nil, fmt.Errorf("%s contains forbidden privacy marker %q", kind, marker)
		}
	}

	return data, nil
}

func executionFeedbackTDDObligationIDs(ledger TDDLedger) []string {
	ids := make([]string, 0, len(ledger.Obligations))
	for _, obligation := range ledger.Obligations {
		ids = append(ids, obligation.ID)
	}

	return ids
}

func executionFeedbackPrivacyMarker(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return ""
	}

	for _, marker := range executionFeedbackPrivacyMarkers {
		if strings.Contains(normalized, marker) {
			return marker
		}
	}

	return ""
}
