package planexecute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	featureplannercontract "idiom/internal/contracts/featureplanner"
	lanecontract "idiom/internal/contracts/lane"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/workflows/featureplanner"
	coreplanexecute "idiom/internal/workflows/planexecute"
)

var _ = ginkgo.Describe("Plan-execute reconciliation adapter", func() {
	ginkgo.It("folds a bound terminal completion", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		progress.Review.Status = plancontract.PlanExecuteReviewStatusCompleted
		progress.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "ok",
		}}

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
			CompletedAt:  time.Now().UTC(),
		})

		_, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateComplete))
		gomega.Expect(state.ActiveRuns).To(gomega.BeEmpty())
		gomega.Expect(events).To(gomega.HaveExactElements(
			gomega.HaveField("Type", "idiom.feature_planner.atom_completed"),
			gomega.HaveField("Type", "idiom.feature_planner.release_blocked"),
		))
	})

	ginkgo.It("overlays active parent authority while preserving divergent terminal scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		run := state.ActiveRuns[0]
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		progress.ScopeContractID = "child-scope-contract"
		progress.ScopeContractHash = "child-scope-hash"
		markProgressTerminalEvidenceForTest(&progress)

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
			CompletedAt:  time.Now().UTC(),
		})

		completionPath, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		payload, err := encodeTerminalCompletion(record, completionPath, run)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		fields := decodeCompletionPayloadForTest(payload)
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldContractID)).To(gomega.Equal(run.InvocationContractID))
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldSnapshotHash)).To(gomega.Equal(run.RuntimeEnvelopeID))
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldScopeContractID)).To(gomega.Equal("child-scope-contract"))
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldScopeContractHash)).To(gomega.Equal("child-scope-hash"))

		ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateComplete))
		gomega.Expect(state.ActiveRuns).To(gomega.BeEmpty())
	})

	ginkgo.It("does not trust acceptance evidence from a failed terminal completion", func() {
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		run := state.ActiveRuns[0]
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusFailed
		progress.ChangedPaths = []string{"pkg/feature.go"}

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath:          ".idiom/plan-execute/sessions/atom-1-run-1.json",
			AcceptanceEvidenceIDs: []string{"req-1"},
			CompletedAt:           time.Now().UTC(),
		})

		record.TerminalClass = plancontract.PlanExecuteTerminalClassFailed
		record.Outcome = plancontract.PlanExecuteTerminalOutcomeFailed

		payload, err := encodeTerminalCompletion(record, ".idiom/plan-execute/completions/atom-1-run-1.json", run)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		fields := decodeCompletionPayloadForTest(payload)
		gomega.Expect(fields[featureplannercontract.PlanExecuteCompletionFieldAcceptanceEvidenceTrusted] == true).To(gomega.BeFalse())
	})

	ginkgo.It("overlays active parent authority while preserving divergent progress scope", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		run := state.ActiveRuns[0]
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		progress.ScopeContractID = "child-progress-scope-contract"
		progress.ScopeContractHash = "child-progress-scope-hash"
		markProgressTerminalEvidenceForTest(&progress)

		_, err := coreplanexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		payload, err := encodeProgressCompletion(progress, ".idiom/plan-execute/sessions/atom-1-run-1.json", run)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		fields := decodeCompletionPayloadForTest(payload)
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldContractID)).To(gomega.Equal(run.InvocationContractID))
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldSnapshotHash)).To(gomega.Equal(run.RuntimeEnvelopeID))
		gomega.Expect(featureplanner.CompletionFieldString(fields, plancontract.PlanExecuteTerminalFieldScopeContractID)).To(gomega.Equal("child-progress-scope-contract"))

		ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateComplete))
		gomega.Expect(state.ActiveRuns).To(gomega.BeEmpty())
	})

	ginkgo.It("keeps an executor-completed checkpoint active until terminal completion", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "plans/atom-1.json", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseExecutor
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.PlanPath = "plans/atom-1.json"
		progress.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		progress.TotalEdits = 1
		progress.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "ok",
		}}

		_, err := coreplanexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).NotTo(gomega.Equal(featureplanner.AtomStateComplete), "events: %+v", events)
		gomega.Expect(state.ActiveRuns).To(gomega.HaveLen(1))
	})

	ginkgo.It("keeps a repairable executor failure active", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "plans/atom-1.json", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseExecutor
		progress.Status = plancontract.PlanExecuteProgressStatusFailed
		progress.PlanPath = "plans/atom-1.json"
		progress.FailureKind = plancontract.PlanExecuteFailureSourceSanityFailed
		progress.Executor.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		progress.TotalEdits = 1
		progress.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
			Kind:   "source_sanity",
			Path:   "pkg/feature.go",
			Status: "failed",
		}}

		_, err := coreplanexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).NotTo(gomega.Equal(featureplanner.AtomStateComplete), "events: %+v", events)
		gomega.Expect(state.ActiveRuns).To(gomega.HaveLen(1))
	})

	ginkgo.DescribeTable("rejecting terminal sidecars with an unusable timestamp",
		func(mutateTS func(*plancontract.PlanExecuteTerminalCompletionRecord)) {
			root := ginkgo.GinkgoT().TempDir()
			state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
			progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

			progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
			progress.Status = plancontract.PlanExecuteProgressStatusCompleted
			progress.ChangedPaths = []string{"pkg/feature.go"}
			markProgressTerminalEvidenceForTest(&progress)

			record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
				ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
				CompletedAt:  time.Now().UTC(),
			})
			mutateTS(&record)

			_, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			events := ReconcileActiveRuns(&state, root)

			gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateImplementing))
			gomega.Expect(state.ActiveRuns).To(gomega.HaveLen(1))
			gomega.Expect(events).To(gomega.BeEmpty())
		},
		ginkgo.Entry("missing timestamp", func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
			record.TS = ""
		}),
		ginkgo.Entry("unparseable timestamp", func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
			record.TS = "not-a-timestamp"
		}),
		ginkgo.Entry("stale timestamp", func(record *plancontract.PlanExecuteTerminalCompletionRecord) {
			record.TS = time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)
		}),
	)

	ginkgo.It("preserves explicit wrong parent authority for rejection", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		markProgressTerminalEvidenceForTest(&progress)

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
			CompletedAt:  time.Now().UTC(),
		})

		completionPath, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		overlayTerminalCompletionJSONForTest(root, completionPath, map[string]any{
			"contract_id": "wrong-parent-contract",
		})

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateRecoveryOpen))
		gomega.Expect(events).To(gomega.ConsistOf(gomega.HaveField("Type", "idiom.feature_planner.plan_execute_completion_rejected")))
		gomega.Expect(state.ActiveRuns).To(gomega.BeEmpty())
	})

	ginkgo.It("rejects a wrong legacy contract hash after authority overlay", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("atom-1-run-1", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted
		progress.ChangedPaths = []string{"pkg/feature.go"}
		markProgressTerminalEvidenceForTest(&progress)

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/atom-1-run-1.json",
			CompletedAt:  time.Now().UTC(),
		})

		record.ContractHash = "wrong-legacy-snapshot"

		_, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateRecoveryOpen))
		gomega.Expect(events).To(gomega.ConsistOf(gomega.And(
			gomega.HaveField("Type", "idiom.feature_planner.plan_execute_completion_rejected"),
			gomega.HaveField("Message", gomega.ContainSubstring("conflicts with the active plan contract snapshot")),
		)))
	})

	ginkgo.It("ignores an unbound terminal completion with overlapping paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("other-run", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted

		record := coreplanexecute.NewPlanExecuteTerminalCompletionRecord(progress, coreplanexecute.PlanExecuteTerminalCompletionInput{
			ProgressPath: ".idiom/plan-execute/sessions/other-run.json",
			CompletedAt:  time.Now().UTC(),
		})

		_, err := coreplanexecute.SavePlanExecuteTerminalCompletionRecord(root, record)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateImplementing))
		gomega.Expect(state.ActiveRuns).To(gomega.HaveLen(1))
		gomega.Expect(events).To(gomega.BeEmpty())
	})

	ginkgo.It("ignores unbound terminal progress with overlapping paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		state := featureStateWithActivePlanExecuteRunForTest("atom-1-run-1")
		progress := coreplanexecute.NewProgressState("generated-session", "prompt", "", []string{"pkg/feature.go"}, nil, time.Now().UTC())

		progress.Phase = plancontract.PlanExecuteProgressPhaseComplete
		progress.Status = plancontract.PlanExecuteProgressStatusCompleted

		_, err := coreplanexecute.SaveProgressState(root, &progress)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := ReconcileActiveRuns(&state, root)

		gomega.Expect(state.Atoms[0].State).To(gomega.Equal(featureplanner.AtomStateImplementing))
		gomega.Expect(state.ActiveRuns).To(gomega.HaveLen(1))
		gomega.Expect(events).To(gomega.BeEmpty())
	})

	ginkgo.It("normalizes canonical scope paths without accepting escapes", func() {
		gomega.Expect(stringSetsOverlap([]string{`pkg\feature.go`}, []string{"./pkg/feature.go"})).To(gomega.BeTrue())
		gomega.Expect(stringSetsOverlap([]string{"../pkg/feature.go"}, []string{"pkg/feature.go"})).To(gomega.BeFalse())
	})
})

func featureStateWithActivePlanExecuteRunForTest(runID string) featureplanner.FeatureState {
	started := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	plan := featureplanner.FeaturePlan{
		FeatureID: "feature-session",
		Atoms: []featureplanner.FeatureAtomPlan{{
			ID:                "atom-1",
			RequiredEditPaths: []string{"pkg/feature.go"},
		}},
	}
	contract := featureplanner.StampPlanAuthorityContract(&plan, nil)
	atomContract, _ := contractAtomForTest(contract, "atom-1")

	return featureplanner.FeatureState{
		SessionID: "feature-session",
		Status:    featureplanner.FeatureStateDispatching,
		Plan:      plan,
		Atoms: []featureplanner.FeatureAtomState{{
			FeatureAtomPlan: plan.Atoms[0],
			State:           featureplanner.AtomStateImplementing,
			LastRunID:       runID,
		}},
		ActiveRuns: []featureplanner.ActiveRun{{
			RunID:                runID,
			AtomID:               "atom-1",
			LaneSessionID:        runID,
			LaneFamily:           string(lanecontract.LanePlanExecute),
			InvocationContractID: atomContract.Scope.ContractID,
			RuntimeEnvelopeID:    contract.ContractHash,
			State:                featureplanner.ActiveRunStateRunning,
			StartedAt:            started,
		}},
	}
}

func contractAtomForTest(contract plancontract.PlanExecutionContract, atomID string) (plancontract.AtomExecutionContract, bool) {
	for _, atom := range contract.Atoms {
		if atom.AtomID == atomID {
			return atom, true
		}
	}

	return plancontract.AtomExecutionContract{}, false
}

func markProgressTerminalEvidenceForTest(progress *coreplanexecute.ProgressState) {
	progress.Review.Status = plancontract.PlanExecuteReviewStatusCompleted
	progress.Executor.SourceSanity = []plancontract.PhaseEvidenceCheck{{
		Kind:   "source_sanity",
		Path:   "pkg/feature.go",
		Status: "ok",
	}}
}

func decodeCompletionPayloadForTest(payload string) map[string]any {
	var fields map[string]any
	gomega.Expect(json.Unmarshal([]byte(payload), &fields)).To(gomega.Succeed(), "payload: %s", payload)

	return fields
}

func overlayTerminalCompletionJSONForTest(root string, relPath string, overlay map[string]any) {
	path := filepath.Join(root, filepath.FromSlash(relPath))

	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	var fields map[string]any
	gomega.Expect(json.Unmarshal(body, &fields)).To(gomega.Succeed())

	for key, value := range overlay {
		fields[key] = value
	}

	body, err = json.MarshalIndent(fields, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(path, append(body, '\n'), 0o600)).To(gomega.Succeed())
}
