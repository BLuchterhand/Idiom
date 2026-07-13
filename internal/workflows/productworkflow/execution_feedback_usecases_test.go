package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("execution feedback usecases", func() {
	ginkgo.It("ExecutionFeedbackArtifactUseCasesLoadAdaptAndReconcile", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeExecutionFeedbackUseCaseJSON(root, WorkflowStateFileName, WorkflowState{
			SchemaVersion:    "idiom.product_workflow_state.v1.test",
			EffortID:         "eff-1",
			TaskID:           "task-1",
			Title:            "Feedback use case",
			UpdatedAt:        "2026-05-18T00:00:00Z",
			StateRevision:    1,
			StatusBucket:     WorkflowStatusBucketStarted,
			CurrentPhase:     executionFeedbackReconciliationPhase,
			CurrentState:     WorkflowStatePlanned,
			ArtifactPaths:    map[string]string{"events": "events.jsonl"},
			Context:          WorkflowContext{ContextPackID: "pack-1", FreshnessState: "current"},
			Coverage:         CoverageState{CoverageTier: "unit"},
			Privacy:          PrivacyState{RedactionStatus: "applied"},
			FolderPath:       "docs/eff-1",
			NextValidActions: []string{"review feedback"},
			DurabilityLevel:  "source_controlled",
		})
		writeExecutionFeedbackUseCaseJSON(root, "tdd.json", TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2.test",
			EffortID:      "eff-1",
			TaskID:        "task-1",
			CoverageTier:  "unit",
			Coverage:      TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{{
				ID: "TDD-001", Type: "acceptance", Behavior: "feedback is reconciled",
				Owner: "execution_feedback_usecases_test.go", Command: "go test ./...",
				RequiredBefore: "close", Status: "pending",
			}},
		})
		writeExecutionFeedbackUseCaseJSON(root, "references/lane_history.json", []ExecutionLaneEventRef{{
			Lane: "implement", Event: "closed", Status: "pass", Timestamp: "2026-05-18T00:00:00Z",
		}})
		writeExecutionFeedbackUseCaseJSON(root, "references/plan_progress.json", PlanExecuteProgressRef{
			Phase: "phase-5", State: "feedback", Status: "completed",
		})
		writeExecutionFeedbackUseCaseJSON(root, "references/verification.json", VerificationRef{
			Status: "pass", Commands: []string{"go test ./..."},
		})

		summary, err := BuildExecutionFeedbackFromArtifacts(ExecutionFeedbackArtifactsRequest{
			EffortRoot:        root,
			LaneHistoryPath:   "references/lane_history.json",
			PlanProgressPath:  "references/plan_progress.json",
			VerificationPath:  "references/verification.json",
			ProductEvidence:   EvidenceBundleRef{ID: "pe-1", Path: "product_evidence/export.json"},
			ReleaseEvidence:   EvidenceBundleRef{ID: "re-1", Path: "release_evidence/bundle.json"},
			ChangedPaths:      []string{"internal/workflows/productworkflow/execution_feedback.go"},
			BriefPath:         "briefs/task-1.md",
			TDDObligationRefs: []string{"TDD-001"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build feedback: %v", err))
		gomega.Expect(summary.Status != ProductWorkflowStatusPass).To(gomega.BeFalse(), fmt.Sprintf("expected passing feedback summary, got %#v", summary))

		writeExecutionFeedbackUseCaseJSON(root, "references/feedback_summary.json", summary)
		adapted, err := BuildRankerLearningEvidenceFromFeedback(RankerLearningFeedbackRequest{
			EffortRoot:  root,
			SummaryPath: "references/feedback_summary.json",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("adapt feedback: %v", err))
		gomega.Expect(adapted.Evidence.Status).To(gomega.Equal(ProductWorkflowStatusPass), fmt.Sprintf("expected bounded passing ranker evidence, got %#v", adapted.Evidence))
		gomega.Expect(adapted.Evidence.Request).NotTo(gomega.BeNil(), fmt.Sprintf("expected bounded passing ranker evidence, got %#v", adapted.Evidence))

		reconciled, err := ReconcileExecutionFeedbackFromArtifacts(ExecutionFeedbackReconciliationRequest{
			EffortRoot:  root,
			SummaryPath: "references/feedback_summary.json",
			OutputPath:  "reports/workflow_state.reconciled.json",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("reconcile feedback: %v", err))
		gomega.Expect(reconciled.Reconciliation.Status != ProductWorkflowStatusPass).To(gomega.BeFalse(), fmt.Sprintf("expected passing reconciliation, got %#v", reconciled.Reconciliation))
		{

			_, err := os.Stat(filepath.Join(root, "reports", "workflow_state.reconciled.json"))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected reconciled state output: %v", err))
		}

	})
	ginkgo.It("BuildExecutionFeedbackFromArtifactsRejectsTraversalReference", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeExecutionFeedbackUseCaseJSON(root, WorkflowStateFileName, WorkflowState{
			SchemaVersion: "test", EffortID: "eff-1", TaskID: "task-1", Title: "Feedback",
			StatusBucket: WorkflowStatusBucketStarted, CurrentPhase: "phase-5", CurrentState: WorkflowStateImplementing,
			ArtifactPaths: map[string]string{"events": "events.jsonl"}, Context: WorkflowContext{ContextPackID: "pack"},
			Coverage: CoverageState{CoverageTier: "unit"}, Privacy: PrivacyState{RedactionStatus: "applied"},
			FolderPath: "docs/eff-1", NextValidActions: []string{"continue"}, DurabilityLevel: "source_controlled",
		})

		_, err := BuildExecutionFeedbackFromArtifacts(ExecutionFeedbackArtifactsRequest{
			EffortRoot:      root,
			LaneHistoryPath: "../lane_history.json",
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("must be effort-relative")))
	})
})

func writeExecutionFeedbackUseCaseJSON(root string, relPath string, value any) {
	ginkgo.GinkgoHelper()

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal %s: %v", relPath, err))

	path := filepath.Join(root, relPath)
	{
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir %s: %v", relPath, err))
	}
	{

		err := os.WriteFile(path, append(data, '\n'), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write %s: %v", relPath, err))
	}

}
