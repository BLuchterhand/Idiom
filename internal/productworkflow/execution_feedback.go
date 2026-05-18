package productworkflow

import (
	"fmt"
	"sort"
	"strings"
)

var executionFeedbackPrivacyMarkers = []string{
	"raw prompt",
	"raw prompts",
	"raw transcript",
	"raw transcripts",
	"file body",
	"file bodies",
	"secret",
	"secrets",
	"customer-identifying",
	"customer identifying",
	"customer data",
}

const executionFeedbackReconciliationPhase = "phase-5-execution-feedback"

func SummarizeExecutionFeedback(input ExecutionFeedbackInput) ExecutionFeedbackSummary {
	knownObligations := map[string]bool{}
	for _, obligation := range input.TDDLedger.Obligations {
		id := strings.TrimSpace(obligation.ID)
		if id == "" {
			continue
		}
		knownObligations[id] = true
	}

	out := ExecutionFeedbackSummary{
		Status:          "pass",
		EffortID:        strings.TrimSpace(input.WorkflowState.EffortID),
		TaskID:          strings.TrimSpace(input.WorkflowState.TaskID),
		StateRevision:   input.WorkflowState.StateRevision,
		CurrentPhase:    strings.TrimSpace(input.WorkflowState.CurrentPhase),
		CurrentState:    strings.TrimSpace(input.WorkflowState.CurrentState),
		LaneHistory:     normalizeExecutionLaneHistory(input.LaneHistory),
		PlanPhase:       strings.TrimSpace(input.PlanProgress.Phase),
		PlanState:       strings.TrimSpace(input.PlanProgress.State),
		PlanStatus:      strings.TrimSpace(input.PlanProgress.Status),
		ProductEvidence: EvidenceBundleRef{ID: strings.TrimSpace(input.ProductEvidence.ID), Path: strings.TrimSpace(input.ProductEvidence.Path)},
		ReleaseEvidence: EvidenceBundleRef{ID: strings.TrimSpace(input.ReleaseEvidence.ID), Path: strings.TrimSpace(input.ReleaseEvidence.Path)},
		BriefPath:       strings.TrimSpace(input.BriefPath),
		ChangedPaths:    stableUniqueStrings(input.ChangedPaths),
		Verification: VerificationRef{
			Status:   strings.TrimSpace(input.Verification.Status),
			Commands: stableUniqueStrings(input.Verification.Commands),
		},
		TDDObligations: stableUniqueStrings(input.TDDObligationRefs),
		Findings:       []ExecutionFeedbackFinding{},
		Blockers:       []ExecutionFeedbackBlocker{},
	}

	addFinding := func(kind, field, message, severity string) {
		out.Findings = append(out.Findings, ExecutionFeedbackFinding{
			Kind:     kind,
			Field:    field,
			Message:  message,
			Severity: severity,
		})
	}
	addBlocker := func(kind, message, next string) {
		out.Blockers = append(out.Blockers, ExecutionFeedbackBlocker{
			Kind:      kind,
			Message:   message,
			NextValid: next,
		})
	}
	checkPrivacyRef := func(field, value string) {
		if executionFeedbackContainsPrivacyMarker(value) {
			addBlocker(
				"privacy_marker_detected",
				fmt.Sprintf("%s contains forbidden privacy marker", field),
				"remove raw prompt/transcript/file-body/secret/customer-identifying content from references",
			)
		}
	}

	if !executionFeedbackPassingStatus(out.PlanStatus) {
		addBlocker(
			"plan_progress_not_pass",
			fmt.Sprintf("plan-execute progress status is %q", out.PlanStatus),
			"update plan-execute progress reference to a passing status before closeout",
		)
	}
	if !executionFeedbackPassingStatus(out.Verification.Status) {
		addBlocker(
			"verification_not_pass",
			fmt.Sprintf("verification status is %q", out.Verification.Status),
			"attach passing verification status and command references",
		)
	}
	for _, obligation := range out.TDDObligations {
		if !knownObligations[obligation] {
			addBlocker(
				"unknown_tdd_obligation",
				fmt.Sprintf("TDD obligation %q is not present in tdd.json", obligation),
				"align obligation references with declared tdd.json obligations",
			)
		}
	}
	if len(out.ChangedPaths) == 0 {
		addFinding("no_changed_paths", "changed_paths", "no changed paths were referenced", "warning")
	}
	if len(out.LaneHistory) == 0 {
		addBlocker(
			"missing_lane_history",
			"lane history reference is empty",
			"provide lane-history events showing phase execution transitions",
		)
	} else if failingLane, ok := executionFeedbackFirstNonPassingLane(out.LaneHistory); ok {
		addBlocker(
			"lane_history_not_pass",
			fmt.Sprintf("lane history event %q/%q has non-passing status %q", failingLane.Lane, failingLane.Event, failingLane.Status),
			"attach only passing or source-satisfied lane-history outcomes before feedback closeout",
		)
	} else if !executionFeedbackHasPassingLane(out.LaneHistory) {
		addBlocker(
			"lane_history_not_pass",
			"lane history has no passing execution event",
			"attach a lane-history event with pass, completed, success, or source-satisfied status",
		)
	}
	if strings.TrimSpace(out.BriefPath) == "" {
		addBlocker(
			"missing_brief_path",
			"brief path reference is empty",
			"provide a brief artifact path reference",
		)
	}
	for _, ref := range []struct {
		field string
		value EvidenceBundleRef
	}{
		{"product_evidence", out.ProductEvidence},
		{"release_evidence", out.ReleaseEvidence},
	} {
		if strings.TrimSpace(ref.value.ID) == "" {
			addBlocker("missing_"+ref.field+"_id", ref.field+" id reference is empty", "provide evidence artifact id references")
		}
		if !safeExecutionFeedbackArtifactPath(ref.value.Path) {
			addBlocker("unsafe_"+ref.field+"_path", ref.field+" path must be a safe relative artifact reference", "provide a safe relative evidence artifact path")
		}
	}
	if !safeExecutionFeedbackArtifactPath(out.BriefPath) {
		addBlocker("unsafe_brief_path", "brief path must be a safe relative artifact reference", "provide a safe relative brief path")
	}
	checkPrivacyRef("product_evidence.id", out.ProductEvidence.ID)
	checkPrivacyRef("product_evidence.path", out.ProductEvidence.Path)
	checkPrivacyRef("release_evidence.id", out.ReleaseEvidence.ID)
	checkPrivacyRef("release_evidence.path", out.ReleaseEvidence.Path)
	checkPrivacyRef("brief_path", out.BriefPath)
	for _, value := range out.ChangedPaths {
		checkPrivacyRef("changed_paths", value)
	}
	for _, value := range out.TDDObligations {
		checkPrivacyRef("tdd_obligations", value)
	}
	for _, value := range out.Verification.Commands {
		checkPrivacyRef("verification.commands", value)
	}

	sort.Slice(out.Findings, func(i, j int) bool {
		if out.Findings[i].Severity == out.Findings[j].Severity {
			if out.Findings[i].Field == out.Findings[j].Field {
				return out.Findings[i].Kind < out.Findings[j].Kind
			}
			return out.Findings[i].Field < out.Findings[j].Field
		}
		return out.Findings[i].Severity < out.Findings[j].Severity
	})
	sort.Slice(out.Blockers, func(i, j int) bool {
		if out.Blockers[i].Kind == out.Blockers[j].Kind {
			return out.Blockers[i].Message < out.Blockers[j].Message
		}
		return out.Blockers[i].Kind < out.Blockers[j].Kind
	})

	if len(out.Blockers) > 0 {
		out.Status = "blocked"
		out.NextValidAction = out.Blockers[0].NextValid
	} else {
		out.NextValidAction = "publish execution feedback summary as deterministic closeout evidence"
	}
	if out.ChangedPaths == nil {
		out.ChangedPaths = []string{}
	}
	if out.Verification.Commands == nil {
		out.Verification.Commands = []string{}
	}
	if out.TDDObligations == nil {
		out.TDDObligations = []string{}
	}
	return out
}

func executionFeedbackPassingStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "complete", "completed", "success", "source-satisfied":
		return true
	default:
		return false
	}
}

func executionFeedbackContainsPrivacyMarker(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return false
	}
	for _, marker := range executionFeedbackPrivacyMarkers {
		if strings.Contains(v, marker) {
			return true
		}
	}
	return false
}

func executionFeedbackHasPassingLane(events []ExecutionLaneEventRef) bool {
	for _, event := range events {
		if executionFeedbackPassingStatus(event.Status) {
			return true
		}
	}
	return false
}

func executionFeedbackFirstNonPassingLane(events []ExecutionLaneEventRef) (ExecutionLaneEventRef, bool) {
	for _, event := range events {
		if !executionFeedbackPassingStatus(event.Status) {
			return event, true
		}
	}
	return ExecutionLaneEventRef{}, false
}

func safeExecutionFeedbackArtifactPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "\\") {
		return false
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func normalizeExecutionLaneHistory(events []ExecutionLaneEventRef) []ExecutionLaneEventRef {
	if len(events) == 0 {
		return nil
	}
	result := make([]ExecutionLaneEventRef, 0, len(events))
	seen := map[string]bool{}
	for _, event := range events {
		normalized := ExecutionLaneEventRef{
			Lane:      strings.TrimSpace(event.Lane),
			Event:     strings.TrimSpace(event.Event),
			Status:    strings.TrimSpace(event.Status),
			Timestamp: strings.TrimSpace(event.Timestamp),
		}
		if normalized.Lane == "" && normalized.Event == "" && normalized.Status == "" {
			continue
		}
		key := normalized.Timestamp + "\x00" + normalized.Lane + "\x00" + normalized.Event + "\x00" + normalized.Status
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, normalized)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Timestamp != result[j].Timestamp {
			return result[i].Timestamp < result[j].Timestamp
		}
		if result[i].Lane != result[j].Lane {
			return result[i].Lane < result[j].Lane
		}
		if result[i].Event != result[j].Event {
			return result[i].Event < result[j].Event
		}
		return result[i].Status < result[j].Status
	})
	if len(result) == 0 {
		return nil
	}
	return result
}

func ReconcileExecutionFeedback(input ExecutionFeedbackReconciliationInput) ExecutionFeedbackReconciliationResult {
	state := input.WorkflowState
	summary := input.Summary
	knownObligations := map[string]bool{}
	for _, obligation := range input.KnownTDDObligations {
		id := strings.TrimSpace(obligation)
		if id == "" {
			continue
		}
		knownObligations[id] = true
	}
	result := ExecutionFeedbackReconciliationResult{
		Status:        "pass",
		WorkflowState: state,
		EvidenceLinks: ExecutionFeedbackEvidenceLinkSummary{
			ProductEvidenceID:        strings.TrimSpace(summary.ProductEvidence.ID),
			ReleaseEvidenceID:        strings.TrimSpace(summary.ReleaseEvidence.ID),
			ChangedPathCount:         len(summary.ChangedPaths),
			TDDObligationCount:       len(summary.TDDObligations),
			VerificationCommandCount: len(summary.Verification.Commands),
		},
		Findings: []ExecutionFeedbackFinding{},
		Blockers: []ExecutionFeedbackReconciliationBlocker{},
	}

	addBlocker := func(kind, message, next string) {
		result.Blockers = append(result.Blockers, ExecutionFeedbackReconciliationBlocker{
			Kind:      kind,
			Message:   message,
			NextValid: next,
		})
	}
	addFinding := func(kind, field, message, severity string) {
		result.Findings = append(result.Findings, ExecutionFeedbackFinding{
			Kind: kind, Field: field, Message: message, Severity: severity,
		})
	}
	checkPrivacy := func(field, value string) {
		if executionFeedbackContainsPrivacyMarker(value) {
			addBlocker(
				"privacy_marker_detected",
				fmt.Sprintf("%s contains forbidden privacy marker", field),
				"remove raw prompt/transcript/file-body/secret/customer-identifying content before reconciliation",
			)
		}
	}
	checkSafePath := func(field, value string) {
		if !safeExecutionFeedbackArtifactPath(value) {
			addBlocker(
				"unsafe_artifact_reference",
				fmt.Sprintf("%s must be a safe relative path", field),
				"replace unsafe artifact references with effort-relative non-traversal paths",
			)
		}
	}
	checkUnsupportedAuthority := func(field, value string) {
		v := strings.ToLower(strings.TrimSpace(value))
		if v == "" {
			return
		}
		unsupported := []string{
			"dispatch", "close effort", "ranker", "evidence export generation", "generate evidence export",
		}
		for _, marker := range unsupported {
			if strings.Contains(v, marker) {
				addBlocker(
					"unsupported_authority_reference",
					fmt.Sprintf("%s implies unsupported authority (%s)", field, marker),
					"remove dispatch/close/ranker/evidence-export-generation directives from reconciliation inputs",
				)
				return
			}
		}
	}

	if !executionFeedbackPassingStatus(summary.Status) {
		addBlocker("feedback_not_pass", fmt.Sprintf("feedback summary status is %q", summary.Status), "re-run `idiom product feedback` until the summary status is pass")
	}
	if len(summary.Blockers) > 0 {
		addBlocker("feedback_contains_blockers", "feedback summary includes blockers and cannot be reconciled", "resolve feedback blockers first, then rerun reconciliation")
	}
	if summary.StateRevision <= 0 {
		addBlocker(
			"missing_state_revision",
			"feedback summary is missing state_revision",
			"re-run `idiom product feedback` against the current workflow_state.json before reconciliation",
		)
	}
	if strings.TrimSpace(summary.EffortID) != strings.TrimSpace(state.EffortID) || strings.TrimSpace(summary.TaskID) != strings.TrimSpace(state.TaskID) {
		addBlocker(
			"effort_task_mismatch",
			fmt.Sprintf("summary effort/task (%s/%s) does not match workflow state (%s/%s)", summary.EffortID, summary.TaskID, state.EffortID, state.TaskID),
			"use a feedback summary generated from the same effort/task as workflow_state.json",
		)
	}
	if state.StateRevision > 0 && summary.StateRevision > 0 && summary.StateRevision != state.StateRevision {
		addBlocker(
			"state_revision_mismatch",
			fmt.Sprintf("summary state_revision %d does not match workflow_state state_revision %d", summary.StateRevision, state.StateRevision),
			"re-run `idiom product feedback` against the current workflow_state.json revision",
		)
	}
	if strings.TrimSpace(state.CurrentPhase) != executionFeedbackReconciliationPhase {
		addBlocker(
			"workflow_phase_mismatch",
			fmt.Sprintf("workflow_state current_phase %q is not %q", state.CurrentPhase, executionFeedbackReconciliationPhase),
			"only reconcile execution feedback while the workflow is in phase-5 execution feedback",
		)
	}
	if strings.TrimSpace(summary.CurrentPhase) != strings.TrimSpace(state.CurrentPhase) {
		addBlocker(
			"summary_phase_mismatch",
			fmt.Sprintf("summary current_phase %q does not match workflow_state current_phase %q", summary.CurrentPhase, state.CurrentPhase),
			"regenerate the feedback summary from the current workflow_state.json phase",
		)
	}
	if strings.TrimSpace(summary.CurrentState) != strings.TrimSpace(state.CurrentState) {
		addBlocker(
			"summary_state_mismatch",
			fmt.Sprintf("summary current_state %q does not match workflow_state current_state %q", summary.CurrentState, state.CurrentState),
			"regenerate the feedback summary from the current workflow_state.json state",
		)
	}
	if len(summary.LaneHistory) == 0 {
		addBlocker("missing_lane_history", "feedback summary is missing lane history", "re-run `idiom product feedback` with passing lane history before reconciliation")
	} else if failingLane, ok := executionFeedbackFirstNonPassingLane(summary.LaneHistory); ok {
		addBlocker(
			"lane_history_not_pass",
			fmt.Sprintf("lane history event %q/%q has non-passing status %q", failingLane.Lane, failingLane.Event, failingLane.Status),
			"attach only passing or source-satisfied lane-history outcomes before reconciliation",
		)
	} else if !executionFeedbackHasPassingLane(summary.LaneHistory) {
		addBlocker("lane_history_not_pass", "feedback summary has no passing lane event", "re-run `idiom product feedback` with passing lane history before reconciliation")
	}
	if !executionFeedbackPassingStatus(summary.PlanStatus) {
		addBlocker(
			"plan_progress_not_pass",
			fmt.Sprintf("feedback summary plan status is %q", summary.PlanStatus),
			"re-run `idiom product feedback` with passing plan progress before reconciliation",
		)
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(summary.PlanPhase)), "phase-5") || !strings.Contains(strings.ToLower(strings.TrimSpace(summary.PlanState)), "feedback") {
		addBlocker(
			"plan_progress_phase_mismatch",
			fmt.Sprintf("feedback summary plan progress %q/%q is not phase-5 feedback", summary.PlanPhase, summary.PlanState),
			"attach a phase-5 feedback plan-progress reference before reconciliation",
		)
	}
	if !executionFeedbackPassingStatus(summary.Verification.Status) {
		addBlocker(
			"verification_not_pass",
			fmt.Sprintf("feedback summary verification status is %q", summary.Verification.Status),
			"attach passing verification evidence before reconciliation",
		)
	}
	if len(summary.Verification.Commands) == 0 {
		addBlocker("missing_verification_commands", "feedback summary is missing verification commands", "attach bounded verification command references before reconciliation")
	}
	if strings.TrimSpace(summary.ProductEvidence.ID) == "" {
		addBlocker("missing_product_evidence_id", "feedback summary is missing product evidence id", "attach product evidence id before reconciliation")
	}
	if strings.TrimSpace(summary.ReleaseEvidence.ID) == "" {
		addBlocker("missing_release_evidence_id", "feedback summary is missing release evidence id", "attach release evidence id before reconciliation")
	}
	if len(summary.ChangedPaths) == 0 {
		addBlocker("missing_changed_paths", "feedback summary is missing changed paths", "attach bounded changed path references before reconciliation")
	}
	if len(summary.TDDObligations) == 0 {
		addBlocker("missing_tdd_obligations", "feedback summary is missing TDD obligation references", "attach TDD obligation references before reconciliation")
	}
	if strings.TrimSpace(input.ReconciledAt) == "" {
		addBlocker("missing_reconciled_at", "reconciled timestamp is required", "provide deterministic reconciliation timestamp metadata")
	}
	checkSafePath("summary_reference_path", input.SummaryReferencePath)
	checkSafePath("reconciled_output_path", input.ReconciledOutputPath)
	checkSafePath("product_evidence.path", summary.ProductEvidence.Path)
	checkSafePath("release_evidence.path", summary.ReleaseEvidence.Path)
	checkSafePath("brief_path", summary.BriefPath)
	for _, value := range summary.ChangedPaths {
		checkSafePath("changed_paths", value)
	}

	checkPrivacy("summary_reference_path", input.SummaryReferencePath)
	checkPrivacy("reconciled_output_path", input.ReconciledOutputPath)
	for _, value := range []string{
		summary.ProductEvidence.ID, summary.ProductEvidence.Path,
		summary.ReleaseEvidence.ID, summary.ReleaseEvidence.Path,
		summary.BriefPath, summary.NextValidAction, summary.Verification.Status,
		strings.Join(summary.Verification.Commands, "\n"),
		strings.Join(summary.ChangedPaths, "\n"),
		strings.Join(summary.TDDObligations, "\n"),
	} {
		checkPrivacy("summary", value)
	}
	checkUnsupportedAuthority("summary.next_valid_action", summary.NextValidAction)
	for _, obligation := range summary.TDDObligations {
		if strings.TrimSpace(obligation) == "" {
			continue
		}
		if !knownObligations[strings.TrimSpace(obligation)] {
			addBlocker(
				"unknown_tdd_obligation",
				fmt.Sprintf("TDD obligation %q is not present in tdd.json", obligation),
				"align feedback summary TDD obligation references with tdd.json",
			)
		}
	}

	if len(result.Blockers) > 0 {
		result.Status = "blocked"
		result.WorkflowState.ReconciliationMismatch = true
		sort.Slice(result.Blockers, func(i, j int) bool {
			if result.Blockers[i].Kind == result.Blockers[j].Kind {
				return result.Blockers[i].Message < result.Blockers[j].Message
			}
			return result.Blockers[i].Kind < result.Blockers[j].Kind
		})
		result.NextValidAction = result.Blockers[0].NextValid
		return result
	}

	if result.WorkflowState.ArtifactPaths == nil {
		result.WorkflowState.ArtifactPaths = map[string]string{}
	}
	result.WorkflowState.ArtifactPaths["execution_feedback_summary"] = strings.TrimSpace(input.SummaryReferencePath)
	result.WorkflowState.ArtifactPaths["brief"] = strings.TrimSpace(summary.BriefPath)
	result.WorkflowState.ArtifactPaths["product_evidence"] = strings.TrimSpace(summary.ProductEvidence.Path)
	result.WorkflowState.ArtifactPaths["release_evidence"] = strings.TrimSpace(summary.ReleaseEvidence.Path)
	result.EvidenceLinks.LinkedArtifactPathKeys = stableUniqueStrings([]string{
		"brief", "execution_feedback_summary", "product_evidence", "release_evidence",
	})
	result.EvidenceLinks.LinkedArtifactPathCount = len(result.EvidenceLinks.LinkedArtifactPathKeys)

	gateResults := make([]GateResult, 0, len(result.WorkflowState.Coverage.GateResults)+1)
	for _, gate := range result.WorkflowState.Coverage.GateResults {
		if strings.TrimSpace(strings.ToLower(gate.Gate)) == "execution_feedback_reconciliation" {
			continue
		}
		gateResults = append(gateResults, gate)
	}
	gateResults = append(gateResults, GateResult{
		Gate:      "execution_feedback_reconciliation",
		Status:    "pass",
		CheckedAt: strings.TrimSpace(input.ReconciledAt),
	})
	sort.Slice(gateResults, func(i, j int) bool {
		if gateResults[i].Gate != gateResults[j].Gate {
			return gateResults[i].Gate < gateResults[j].Gate
		}
		return gateResults[i].CheckedAt < gateResults[j].CheckedAt
	})
	result.WorkflowState.Coverage.GateResults = gateResults
	result.WorkflowState.Coverage.AdvisoryCriteria = stableUniqueStrings(append(
		append([]string{}, result.WorkflowState.Coverage.AdvisoryCriteria...),
		"execution feedback reconciliation linked deterministic evidence references",
	))
	if result.WorkflowState.Coverage.AdvisoryCriteria == nil {
		result.WorkflowState.Coverage.AdvisoryCriteria = []string{}
	}

	result.WorkflowState.LastReconciliationAt = strings.TrimSpace(input.ReconciledAt)
	result.WorkflowState.ReconciliationMismatch = false
	result.WorkflowState.NextValidActions = []string{
		"review reconciled workflow-state output and persist only after operator approval",
		"continue with remaining phase-5 metrics/operator workflow atoms",
	}
	result.NextValidAction = result.WorkflowState.NextValidActions[0]
	addFinding(
		"execution_result_linked",
		"artifact_paths",
		fmt.Sprintf("linked %d bounded execution feedback artifact paths", result.EvidenceLinks.LinkedArtifactPathCount),
		"info",
	)
	return result
}
