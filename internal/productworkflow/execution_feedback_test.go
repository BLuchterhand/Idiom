package productworkflow

import (
	"strings"
	"testing"
)

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

func TestExecutionFeedbackReconciliationPassesAndLinksState(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState: WorkflowState{
			EffortID:      "eff-1",
			TaskID:        "task-1",
			StateRevision: 5,
			CurrentPhase:  "phase-5-execution-feedback",
			CurrentState:  "planned",
			ArtifactPaths: map[string]string{
				"events": "events.jsonl",
			},
			Coverage: CoverageState{
				GateResults: []GateResult{{Gate: "coverage", Status: "pass", CheckedAt: "2026-05-17T00:00:00Z"}},
			},
		},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "planned",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "completed",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"cmd/idiom/product_workflow.go"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-001"},
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/execution_feedback_summary.json",
		ReconciledOutputPath: "reports/workflow_state.reconciled.json",
	})
	if out.Status != "pass" {
		t.Fatalf("expected pass, got %#v", out)
	}
	if out.WorkflowState.ReconciliationMismatch {
		t.Fatalf("expected reconciliation mismatch false")
	}
	if out.WorkflowState.ArtifactPaths["execution_feedback_summary"] != "reports/execution_feedback_summary.json" {
		t.Fatalf("expected summary path link, got %#v", out.WorkflowState.ArtifactPaths)
	}
	if out.WorkflowState.ArtifactPaths["product_evidence"] != "product_evidence/export-1.json" {
		t.Fatalf("expected product evidence link, got %#v", out.WorkflowState.ArtifactPaths)
	}
	if out.EvidenceLinks.LinkedArtifactPathCount != 4 {
		t.Fatalf("expected linked path count 4, got %#v", out.EvidenceLinks)
	}
	if out.NextValidAction != "review reconciled workflow-state output and persist only after operator approval" {
		t.Fatalf("expected review-first next action, got %q", out.NextValidAction)
	}
	foundGate := false
	for _, gate := range out.WorkflowState.Coverage.GateResults {
		if gate.Gate == "execution_feedback_reconciliation" && gate.Status == "pass" {
			foundGate = true
		}
	}
	if !foundGate {
		t.Fatalf("expected reconciliation gate result, got %#v", out.WorkflowState.Coverage.GateResults)
	}
}

func TestExecutionFeedbackReconciliationRejectsMalformedPassSummary(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-5-execution-feedback", CurrentState: "feedback"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked malformed pass summary, got %#v", out)
	}
	kinds := map[string]bool{}
	for _, blocker := range out.Blockers {
		kinds[blocker.Kind] = true
	}
	for _, want := range []string{"missing_lane_history", "plan_progress_not_pass", "plan_progress_phase_mismatch", "verification_not_pass", "missing_changed_paths", "missing_tdd_obligations"} {
		if !kinds[want] {
			t.Fatalf("expected blocker %s, got %#v", want, out.Blockers)
		}
	}
}

func TestExecutionFeedbackReconciliationRejectsMissingStateRevision(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-5-execution-feedback", CurrentState: "planned"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "planned",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "completed",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"cmd/idiom/product_workflow.go"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-001"},
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked missing state revision, got %#v", out)
	}
	found := false
	for _, blocker := range out.Blockers {
		if blocker.Kind == "missing_state_revision" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing_state_revision blocker, got %#v", out.Blockers)
	}
}

func TestExecutionFeedbackReconciliationRejectsWrongPhaseState(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-4-brief-generation", CurrentState: "planned"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-4-brief-generation",
			CurrentState:    "planned",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-4",
			PlanState:       "brief",
			PlanStatus:      "completed",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"cmd/idiom/product_workflow.go"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-001"},
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked wrong phase/state, got %#v", out)
	}
	kinds := map[string]bool{}
	for _, blocker := range out.Blockers {
		kinds[blocker.Kind] = true
	}
	for _, want := range []string{"workflow_phase_mismatch", "plan_progress_phase_mismatch"} {
		if !kinds[want] {
			t.Fatalf("expected blocker %s, got %#v", want, out.Blockers)
		}
	}
}

func TestExecutionFeedbackReconciliationFailsClosed(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-5-execution-feedback", CurrentState: "feedback"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "blocked",
			EffortID:        "eff-2",
			TaskID:          "task-2",
			StateRevision:   4,
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "../escape.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			Verification:    VerificationRef{Status: "pass"},
			TDDObligations:  []string{"TDD-001"},
			NextValidAction: "dispatch lane now",
			Blockers:        []ExecutionFeedbackBlocker{{Kind: "x", Message: "y", NextValid: "z"}},
		},
		ReconciledAt:         "",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %#v", out)
	}
	if !out.WorkflowState.ReconciliationMismatch {
		t.Fatalf("expected reconciliation mismatch true")
	}
	if len(out.Blockers) == 0 {
		t.Fatalf("expected blockers")
	}
}

func TestExecutionFeedbackReconciliationRejectsUnknownTDDAndUnsafeChangedPath(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-5-execution-feedback", CurrentState: "planned"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "planned",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "completed",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"../bad"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-404"},
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %#v", out)
	}
}

func TestExecutionFeedbackReconciliationAllowsDispatchNamedPath(t *testing.T) {
	out := ReconcileExecutionFeedback(ExecutionFeedbackReconciliationInput{
		WorkflowState:       WorkflowState{EffortID: "eff-1", TaskID: "task-1", StateRevision: 5, CurrentPhase: "phase-5-execution-feedback", CurrentState: "feedback"},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "feedback",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "pass",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"internal/app/implementation_dispatch.go"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-001"},
		},
		ReconciledAt:         "2026-05-18T00:00:00Z",
		SummaryReferencePath: "reports/summary.json",
		ReconciledOutputPath: "reports/reconciled.json",
	})
	if out.Status != "pass" {
		t.Fatalf("expected dispatch-named path to be treated as a path, got %#v", out.Blockers)
	}
}

func TestRankerLearningEvidenceAdapterPassesWithVerifiedExecutionFeedback(t *testing.T) {
	out := AdaptExecutionFeedbackToRankerLearning(RankerLearningEvidenceInput{
		WorkflowState: WorkflowState{
			EffortID:     "eff-1",
			TaskID:       "task-1",
			CurrentPhase: "phase-5-execution-feedback",
			CurrentState: "planned",
			Context: WorkflowContext{
				ContextPackID:  "pack-1",
				FreshnessState: "current",
			},
			Privacy: PrivacyState{
				RedactionStatus: "applied",
			},
		},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "pass",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "planned",
			LaneHistory:     []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "completed",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"cmd/idiom/product_workflow.go"},
			Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
			TDDObligations:  []string{"TDD-001"},
		},
	})
	if out.Status != "pass" {
		t.Fatalf("expected pass, got %#v", out)
	}
	if out.Request == nil {
		t.Fatalf("expected request, got %#v", out)
	}
	if out.Request.Source != "harness_feedback" || out.Request.Action != "boost" || !out.Request.Verified {
		t.Fatalf("expected review request shape, got %#v", out.Request)
	}
	if out.Request.TaskID != "task-1" || out.Request.CorpusID != "pack-1" || out.Request.ResultID != "feedback_r5" {
		t.Fatalf("expected deterministic request ids, got %#v", out.Request)
	}
	if len(out.Request.Files) != 1 || out.Request.Files[0] != "cmd/idiom/product_workflow.go" {
		t.Fatalf("expected changed path files, got %#v", out.Request.Files)
	}
	if out.Request.Evidence.Kind != "verified_feedback" || !out.Request.Evidence.Verified {
		t.Fatalf("expected verified feedback evidence, got %#v", out.Request.Evidence)
	}
	if out.Summary.ContextPackID != "pack-1" || out.Summary.VerificationStatus != "pass" {
		t.Fatalf("expected privacy-safe summary fields, got %#v", out.Summary)
	}
	if !strings.Contains(out.NextValidAction, "no ranker promotion was applied") {
		t.Fatalf("expected bounded next action, got %q", out.NextValidAction)
	}
}

func TestRankerLearningEvidenceAdapterFailsClosed(t *testing.T) {
	out := AdaptExecutionFeedbackToRankerLearning(RankerLearningEvidenceInput{
		WorkflowState: WorkflowState{
			EffortID:     "eff-1",
			TaskID:       "task-1",
			CurrentPhase: "phase-5-execution-feedback",
			CurrentState: "planned",
			Privacy: PrivacyState{
				RedactionStatus: "applied",
			},
		},
		KnownTDDObligations: []string{"TDD-001"},
		Summary: ExecutionFeedbackSummary{
			Status:          "blocked",
			EffortID:        "eff-1",
			TaskID:          "task-1",
			StateRevision:   5,
			CurrentPhase:    "phase-5-execution-feedback",
			CurrentState:    "planned",
			PlanPhase:       "phase-5",
			PlanState:       "feedback",
			PlanStatus:      "blocked",
			ProductEvidence: EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export-1.json"},
			ReleaseEvidence: EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle-1.json"},
			BriefPath:       "briefs/test.md",
			ChangedPaths:    []string{"../bad", "contains raw transcript"},
			Verification:    VerificationRef{Status: "fail"},
			TDDObligations:  []string{"TDD-404"},
			Blockers:        []ExecutionFeedbackBlocker{{Kind: "verification_not_pass", Message: "blocked", NextValid: "repair"}},
		},
	})
	if out.Status != "blocked" {
		t.Fatalf("expected blocked, got %#v", out)
	}
	if out.Request != nil {
		t.Fatalf("blocked adapter must not return a request, got %#v", out.Request)
	}
	kinds := map[string]bool{}
	for _, blocker := range out.Blockers {
		kinds[blocker.Kind] = true
	}
	for _, want := range []string{
		"feedback_not_pass",
		"feedback_contains_blockers",
		"missing_context_pack_id",
		"missing_context_freshness",
		"plan_progress_not_pass",
		"verification_not_pass",
		"missing_verification_commands",
		"unsafe_artifact_reference",
		"unknown_tdd_obligation",
		"privacy_marker_detected",
	} {
		if !kinds[want] {
			t.Fatalf("expected blocker %s, got %#v", want, out.Blockers)
		}
	}
	if strings.Contains(strings.ToLower(out.NextValidAction), "promot") {
		t.Fatalf("blocked adapter next action must not imply promotion, got %q", out.NextValidAction)
	}
}
