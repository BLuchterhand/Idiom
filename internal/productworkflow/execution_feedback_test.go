package productworkflow

import "testing"

func TestExecutionFeedbackPassesWithDeterministicOrdering(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState: WorkflowState{
			EffortID:     "eff-1",
			TaskID:       "task-1",
			CurrentPhase: "phase-5",
			CurrentState: "execution_feedback",
		},
		TDDLedger: TDDLedger{
			Obligations: []TDDObligation{{ID: "TDD-002"}, {ID: "TDD-001"}},
		},
		LaneHistory: []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
		PlanProgress: PlanExecuteProgressRef{
			Phase:  "phase-5",
			State:  "feedback",
			Status: "completed",
		},
		ProductEvidence:   EvidenceBundleRef{ID: "pe-1", Path: "product-evidence/export.json"},
		ReleaseEvidence:   EvidenceBundleRef{ID: "re-1", Path: "release-evidence/bundle.json"},
		ChangedPaths:      []string{"b.go", "a.go", "a.go"},
		Verification:      VerificationRef{Status: "pass", Commands: []string{"go test ./...", "go test ./..."}},
		BriefPath:         "briefs/test.md",
		TDDObligationRefs: []string{"TDD-002", "TDD-001", "TDD-001"},
	})
	if out.Status != "pass" {
		t.Fatalf("expected pass, got %q", out.Status)
	}
	if len(out.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %#v", out.Blockers)
	}
	if len(out.ChangedPaths) != 2 || out.ChangedPaths[0] != "a.go" || out.ChangedPaths[1] != "b.go" {
		t.Fatalf("expected sorted unique changed paths, got %#v", out.ChangedPaths)
	}
	if len(out.TDDObligations) != 2 || out.TDDObligations[0] != "TDD-001" || out.TDDObligations[1] != "TDD-002" {
		t.Fatalf("expected sorted unique TDD refs, got %#v", out.TDDObligations)
	}
	if len(out.LaneHistory) != 1 || out.LaneHistory[0].Lane != "implement" {
		t.Fatalf("expected lane history in summary, got %#v", out.LaneHistory)
	}
}

func TestExecutionFeedbackBlocksOnVerificationProgressAndUnknownObligations(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState: WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
		TDDLedger:     TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
		PlanProgress:  PlanExecuteProgressRef{Status: "blocked"},
		Verification:  VerificationRef{Status: "fail"},
		BriefPath:     "",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	if len(out.Blockers) < 3 {
		t.Fatalf("expected multiple blockers, got %#v", out.Blockers)
	}
}

func TestExecutionFeedbackBlocksUnsafeEvidenceRefs(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState:     WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
		TDDLedger:         TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
		LaneHistory:       []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "completed"}},
		PlanProgress:      PlanExecuteProgressRef{Status: "completed"},
		Verification:      VerificationRef{Status: "passed"},
		ProductEvidence:   EvidenceBundleRef{ID: "pe-1", Path: "../product.json"},
		ReleaseEvidence:   EvidenceBundleRef{ID: "re-1", Path: "/tmp/release.json"},
		BriefPath:         "briefs/test.md",
		TDDObligationRefs: []string{"TDD-001"},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	foundProduct := false
	foundRelease := false
	for _, blocker := range out.Blockers {
		if blocker.Kind == "unsafe_product_evidence_path" {
			foundProduct = true
		}
		if blocker.Kind == "unsafe_release_evidence_path" {
			foundRelease = true
		}
	}
	if !foundProduct || !foundRelease {
		t.Fatalf("expected unsafe evidence path blockers, got %#v", out.Blockers)
	}
}

func TestExecutionFeedbackBlocksWhenObligationRefUnknown(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState:     WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
		TDDLedger:         TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
		LaneHistory:       []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
		PlanProgress:      PlanExecuteProgressRef{Status: "pass"},
		Verification:      VerificationRef{Status: "pass"},
		BriefPath:         "briefs/test.md",
		TDDObligationRefs: []string{"TDD-404"},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Blockers {
		if blocker.Kind == "unknown_tdd_obligation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unknown_tdd_obligation blocker, got %#v", out.Blockers)
	}
}

func TestExecutionFeedbackBlocksMixedLaneHistoryWithFailure(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState: WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
		TDDLedger:     TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
		LaneHistory: []ExecutionLaneEventRef{
			{Lane: "implement", Event: "closed", Status: "pass"},
			{Lane: "verify", Event: "closed", Status: "fail"},
		},
		PlanProgress:      PlanExecuteProgressRef{Status: "pass"},
		Verification:      VerificationRef{Status: "pass"},
		BriefPath:         "briefs/test.md",
		ProductEvidence:   EvidenceBundleRef{ID: "pe-1", Path: "product-evidence/export.json"},
		ReleaseEvidence:   EvidenceBundleRef{ID: "re-1", Path: "release-evidence/bundle.json"},
		ChangedPaths:      []string{"cmd/idiom/product_workflow.go"},
		TDDObligationRefs: []string{"TDD-001"},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Blockers {
		if blocker.Kind == "lane_history_not_pass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lane_history_not_pass blocker, got %#v", out.Blockers)
	}
}

func TestExecutionFeedbackBlocksOnPrivacyMarkers(t *testing.T) {
	out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
		WorkflowState: WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
		TDDLedger:     TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
		LaneHistory:   []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
		PlanProgress:  PlanExecuteProgressRef{Status: "pass"},
		Verification:  VerificationRef{Status: "pass", Commands: []string{"contains raw transcript"}},
		BriefPath:     "briefs/test.md",
		ProductEvidence: EvidenceBundleRef{
			ID:   "pe-1",
			Path: "customer-identifying/export.json",
		},
		TDDObligationRefs: []string{"TDD-001"},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", out.Status)
	}
	found := false
	for _, blocker := range out.Blockers {
		if blocker.Kind == "privacy_marker_detected" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected privacy_marker_detected blocker, got %#v", out.Blockers)
	}
}
