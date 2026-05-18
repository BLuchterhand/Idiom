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
	hasPrivacyMarker := func(value string) bool {
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
	checkPrivacyRef := func(field, value string) {
		if hasPrivacyMarker(value) {
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
