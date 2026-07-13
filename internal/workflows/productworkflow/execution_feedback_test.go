package productworkflow

import (
	"fmt"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("execution feedback", func() {
	ginkgo.It("ExecutionFeedbackPassesWithDeterministicOrdering", func() {
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
		gomega.Expect(out.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %q", out.Status))
		gomega.Expect(len(out.Blockers) != 0).To(gomega.BeFalse(), fmt.Sprintf("expected no blockers, got %#v", out.Blockers))
		gomega.Expect(out.ChangedPaths).To(gomega.HaveLen(2), fmt.Sprintf("expected sorted unique changed paths, got %#v", out.ChangedPaths))
		gomega.Expect(out.ChangedPaths[0]).To(gomega.Equal("a.go"), fmt.Sprintf("expected sorted unique changed paths, got %#v", out.ChangedPaths))
		gomega.Expect(out.ChangedPaths[1]).To(gomega.Equal("b.go"), fmt.Sprintf("expected sorted unique changed paths, got %#v", out.ChangedPaths))
		gomega.Expect(out.TDDObligations).To(gomega.HaveLen(2), fmt.Sprintf("expected sorted unique TDD refs, got %#v", out.TDDObligations))
		gomega.Expect(out.TDDObligations[0]).To(gomega.Equal("TDD-001"), fmt.Sprintf("expected sorted unique TDD refs, got %#v", out.TDDObligations))
		gomega.Expect(out.TDDObligations[1]).To(gomega.Equal("TDD-002"), fmt.Sprintf("expected sorted unique TDD refs, got %#v", out.TDDObligations))
		gomega.Expect(out.LaneHistory).To(gomega.HaveLen(1), fmt.Sprintf("expected lane history in summary, got %#v", out.LaneHistory))
		gomega.Expect(out.LaneHistory[0].Lane).To(gomega.Equal("implement"), fmt.Sprintf("expected lane history in summary, got %#v", out.LaneHistory))

	})
	ginkgo.It("ExecutionFeedbackBlocksOnVerificationProgressAndUnknownObligations", func() {
		out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
			WorkflowState: WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
			TDDLedger:     TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
			PlanProgress:  PlanExecuteProgressRef{Status: "blocked"},
			Verification:  VerificationRef{Status: "fail"},
			BriefPath:     "",
		})
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", out.Status))
		gomega.Expect(len(out.Blockers) < 3).To(gomega.BeFalse(), fmt.Sprintf("expected multiple blockers, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackBlocksUnsafeEvidenceRefs", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", out.Status))

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
		gomega.Expect(foundProduct).To(gomega.BeTrue(), fmt.Sprintf("expected unsafe evidence path blockers, got %#v", out.Blockers))
		gomega.Expect(foundRelease).To(gomega.BeTrue(), fmt.Sprintf("expected unsafe evidence path blockers, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackBlocksWhenObligationRefUnknown", func() {
		out := SummarizeExecutionFeedback(ExecutionFeedbackInput{
			WorkflowState:     WorkflowState{EffortID: "eff-1", TaskID: "task-1"},
			TDDLedger:         TDDLedger{Obligations: []TDDObligation{{ID: "TDD-001"}}},
			LaneHistory:       []ExecutionLaneEventRef{{Lane: "implement", Event: "closed", Status: "pass"}},
			PlanProgress:      PlanExecuteProgressRef{Status: "pass"},
			Verification:      VerificationRef{Status: "pass"},
			BriefPath:         "briefs/test.md",
			TDDObligationRefs: []string{"TDD-404"},
		})
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", out.Status))

		found := false

		for _, blocker := range out.Blockers {
			if blocker.Kind == "unknown_tdd_obligation" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected unknown_tdd_obligation blocker, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackBlocksMixedLaneHistoryWithFailure", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", out.Status))

		found := false

		for _, blocker := range out.Blockers {
			if blocker.Kind == "lane_history_not_pass" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected lane_history_not_pass blocker, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackBlocksOnPrivacyMarkers", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %q", out.Status))

		found := false

		for _, blocker := range out.Blockers {
			if blocker.Kind == "privacy_marker_detected" {
				found = true
				break
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected privacy_marker_detected blocker, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackReconciliationPassesAndLinksState", func() {
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
		gomega.Expect(out.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %#v", out))
		gomega.Expect(out.WorkflowState.ReconciliationMismatch).To(gomega.BeFalse(), fmt.Sprintf("expected reconciliation mismatch false"))
		gomega.Expect(out.WorkflowState.ArtifactPaths["execution_feedback_summary"] != "reports/execution_feedback_summary.json").To(gomega.BeFalse(), fmt.Sprintf("expected summary path link, got %#v", out.WorkflowState.ArtifactPaths))
		gomega.Expect(out.WorkflowState.ArtifactPaths["product_evidence"] != "product_evidence/export-1.json").To(gomega.BeFalse(), fmt.Sprintf("expected product evidence link, got %#v", out.WorkflowState.ArtifactPaths))
		gomega.Expect(out.EvidenceLinks.LinkedArtifactPathCount != 4).To(gomega.BeFalse(), fmt.Sprintf("expected linked path count 4, got %#v", out.EvidenceLinks))
		gomega.Expect(out.NextValidAction != "review reconciled workflow-state output and persist only after operator approval").To(gomega.BeFalse(), fmt.Sprintf("expected review-first next action, got %q", out.NextValidAction))

		foundGate := false

		for _, gate := range out.WorkflowState.Coverage.GateResults {
			if gate.Gate == "execution_feedback_reconciliation" && gate.Status == "pass" {
				foundGate = true
			}
		}
		gomega.Expect(!foundGate).To(gomega.BeFalse(), fmt.Sprintf("expected reconciliation gate result, got %#v", out.WorkflowState.Coverage.GateResults))

	})
	ginkgo.It("ExecutionFeedbackReconciliationRejectsMalformedPassSummary", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked malformed pass summary, got %#v", out))

		kinds := map[string]bool{}
		for _, blocker := range out.Blockers {
			kinds[blocker.Kind] = true
		}

		for _, want := range []string{"missing_lane_history", "plan_progress_not_pass", "plan_progress_phase_mismatch", "verification_not_pass", "missing_changed_paths", "missing_tdd_obligations"} {
			gomega.Expect(!kinds[want]).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %s, got %#v", want, out.Blockers))

		}
	})
	ginkgo.It("ExecutionFeedbackReconciliationRejectsMissingStateRevision", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked missing state revision, got %#v", out))

		found := false

		for _, blocker := range out.Blockers {
			if blocker.Kind == "missing_state_revision" {
				found = true
			}
		}
		gomega.Expect(!found).To(gomega.BeFalse(), fmt.Sprintf("expected missing_state_revision blocker, got %#v", out.Blockers))

	})
	ginkgo.It("ExecutionFeedbackReconciliationRejectsWrongPhaseState", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked wrong phase/state, got %#v", out))

		kinds := map[string]bool{}
		for _, blocker := range out.Blockers {
			kinds[blocker.Kind] = true
		}

		for _, want := range []string{"workflow_phase_mismatch", "plan_progress_phase_mismatch"} {
			gomega.Expect(!kinds[want]).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %s, got %#v", want, out.Blockers))

		}
	})
	ginkgo.It("ExecutionFeedbackReconciliationFailsClosed", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %#v", out))
		gomega.Expect(!out.WorkflowState.ReconciliationMismatch).To(gomega.BeFalse(), fmt.Sprintf("expected reconciliation mismatch true"))
		gomega.Expect(len(out.Blockers) == 0).To(gomega.BeFalse(), fmt.Sprintf("expected blockers"))

	})
	ginkgo.It("ExecutionFeedbackReconciliationRejectsUnknownTDDAndUnsafeChangedPath", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %#v", out))

	})
	ginkgo.It("ExecutionFeedbackReconciliationAllowsDispatchNamedPath", func() {
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
				ChangedPaths:    []string{"internal/surfaces/tui/app/implementation_dispatch.go"},
				Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
				TDDObligations:  []string{"TDD-001"},
			},
			ReconciledAt:         "2026-05-18T00:00:00Z",
			SummaryReferencePath: "reports/summary.json",
			ReconciledOutputPath: "reports/reconciled.json",
		})
		gomega.Expect(out.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected dispatch-named path to be treated as a path, got %#v", out.Blockers))

	})
	ginkgo.It("RankerLearningEvidenceAdapterPassesWithVerifiedExecutionFeedback", func() {
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
		gomega.Expect(out.Status != "pass").To(gomega.BeFalse(), fmt.Sprintf("expected pass, got %#v", out))
		gomega.Expect(out.Request == nil).To(gomega.BeFalse(), fmt.Sprintf("expected request, got %#v", out))
		gomega.Expect(out.Request.Source).To(gomega.Equal("harness_feedback"), fmt.Sprintf("expected review request shape, got %#v", out.Request))
		gomega.Expect(out.Request.Action).To(gomega.Equal("boost"), fmt.Sprintf("expected review request shape, got %#v", out.Request))
		gomega.Expect(out.Request.Verified).To(gomega.BeTrue(), fmt.Sprintf("expected review request shape, got %#v", out.Request))
		gomega.Expect(out.Request.TaskID).To(gomega.Equal("task-1"), fmt.Sprintf("expected deterministic request ids, got %#v", out.Request))
		gomega.Expect(out.Request.CorpusID).To(gomega.Equal("pack-1"), fmt.Sprintf("expected deterministic request ids, got %#v", out.Request))
		gomega.Expect(out.Request.ResultID).To(gomega.Equal("feedback_r5"), fmt.Sprintf("expected deterministic request ids, got %#v", out.Request))
		gomega.Expect(out.Request.Files).To(gomega.HaveLen(1), fmt.Sprintf("expected changed path files, got %#v", out.Request.Files))
		gomega.Expect(out.Request.Files[0]).To(gomega.Equal("cmd/idiom/product_workflow.go"), fmt.Sprintf("expected changed path files, got %#v", out.Request.Files))
		gomega.Expect(out.Request.Evidence.Kind).To(gomega.Equal("verified_feedback"), fmt.Sprintf("expected verified feedback evidence, got %#v", out.Request.Evidence))
		gomega.Expect(out.Request.Evidence.Verified).To(gomega.BeTrue(), fmt.Sprintf("expected verified feedback evidence, got %#v", out.Request.Evidence))
		gomega.Expect(out.Summary.ContextPackID).To(gomega.Equal("pack-1"), fmt.Sprintf("expected privacy-safe summary fields, got %#v", out.Summary))
		gomega.Expect(out.Summary.VerificationStatus).To(gomega.Equal("pass"), fmt.Sprintf("expected privacy-safe summary fields, got %#v", out.Summary))
		gomega.Expect(!strings.Contains(out.NextValidAction, "no ranker promotion was applied")).To(gomega.BeFalse(), fmt.Sprintf("expected bounded next action, got %q", out.NextValidAction))

	})
	ginkgo.It("RankerLearningEvidenceAdapterFailsClosed", func() {
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
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %#v", out))
		gomega.Expect(out.Request != nil).To(gomega.BeFalse(), fmt.Sprintf("blocked adapter must not return a request, got %#v", out.Request))

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
			gomega.Expect(!kinds[want]).To(gomega.BeFalse(), fmt.Sprintf("expected blocker %s, got %#v", want, out.Blockers))

		}
		gomega.Expect(strings.Contains(strings.ToLower(out.NextValidAction), "promot")).To(gomega.BeFalse(), fmt.Sprintf("blocked adapter next action must not imply promotion, got %q", out.NextValidAction))

	})
	ginkgo.It("RankerLearningEvidenceAdapterRejectsRankerStatePaths", func() {
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
				ChangedPaths:    []string{".idiom/ranker/weights.json"},
				Verification:    VerificationRef{Status: "pass", Commands: []string{"go test ./cmd/idiom"}},
				TDDObligations:  []string{"TDD-001"},
			},
		})
		gomega.Expect(out.Status != "blocked").To(gomega.BeFalse(), fmt.Sprintf("expected blocked, got %#v", out))
		gomega.Expect(out.Request != nil).To(gomega.BeFalse(), fmt.Sprintf("ranker state paths must not be emitted in a request, got %#v", out.Request))

		gomega.Expect(out.Blockers).To(gomega.ContainElement(gomega.SatisfyAll(
			gomega.HaveField("Kind", "unsupported_authority_reference"),
			gomega.HaveField("Message", gomega.ContainSubstring(".idiom/ranker")),
		)), fmt.Sprintf("expected ranker state path blocker, got %#v", out.Blockers))
	})
})
