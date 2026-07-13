package planexecute

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/repository/comprehension"
)

var _ = ginkgo.It("collect plan execute phase evidence loads packet and executor checks", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/evidence\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed go.mod: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed a.go: %v", err))
	}

	build, err := comprehension.Build(ginkgo.GinkgoT().Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Build: %v", err))

	key := comprehension.ViewKey{IndexID: build.IndexID, Task: "phase evidence", Mode: "plan"}
	cached := comprehension.PackResult{
		PackID:         key.ID(),
		IndexID:        build.IndexID,
		Root:           root,
		Task:           "phase evidence",
		Mode:           "plan",
		Fresh:          true,
		CandidateFiles: []comprehension.PackFile{{Path: "a.go", Role: "editable", Reason: "matched task"}},
		CandidateSymbols: []comprehension.PackSymbol{
			{ID: "symbol://a.go#A", QualifiedName: "A", Kind: "function", Path: "a.go", Reason: "matched task"},
		},
		Tests:          []comprehension.PackTest{{Command: "go test ./...", Path: "a.go", Reason: "packet suggested test"}},
		UnresolvedGaps: []string{"reason=stale_runtime_trace trace_id=t1"},
	}

	if err := comprehension.SaveView(root, key, cached); err != nil {
		ginkgo.Fail(fmt.Sprintf("SaveView: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"beta\" }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed post-execute a.go: %v", err))
	}

	plan := planartifact.PlanArtifact{
		SchemaVersion: planartifact.SchemaVersion,
		PackID:        key.ID(),
		Edits: []planartifact.PlanEdit{
			{Path: "a.go", OldText: "alpha", NewText: "beta", Purpose: "change return marker"},
		},
	}
	evidence := collectPlanExecutePhaseEvidence(
		root,
		"review_repair",
		plan,
		[]string{"a.go"},
		[]string{"a.go"},
		[]string{"a.go"},
		ReviewRepairEvidence{DiffCheckExit: 0},
		ExecutorPhaseReport{SourceSanity: []plancontract.PhaseEvidenceCheck{{Kind: "source_sanity", Path: "a.go", Status: "ok", Summary: "Source sanity passed"}}},
		planExecuteTestDependencies().SourceSanityCovers,
	)
	gomega.Expect(evidence.SchemaVersion != PhaseEvidenceSchemaVersion).To(gomega.BeFalse(), fmt.Sprintf("schema version = %q", evidence.SchemaVersion))
	gomega.Expect(evidence.Phase != PhaseEvidencePhaseReviewRepair).To(gomega.BeFalse(), fmt.Sprintf("phase = %q, want %q", evidence.Phase, PhaseEvidencePhaseReviewRepair))
	gomega.Expect(evidence.Packet.Status).To(gomega.Equal(PhaseEvidencePacketStatusAvailableStale), fmt.Sprintf("expected stale graph-view packet %s, got %#v", key.ID(), evidence.Packet))
	gomega.Expect(evidence.Packet.PackID).To(gomega.Equal(key.ID()), fmt.Sprintf("expected stale graph-view packet %s, got %#v", key.ID(), evidence.Packet))
	gomega.Expect(evidence.ExecutorHandoff.PacketState).To(gomega.Equal(plancontract.ExecutorHandoffStateStale), fmt.Sprintf("expected stale executor handoff evidence, got %#v", evidence.ExecutorHandoff))
	gomega.Expect(evidence.ExecutorHandoff.Reason).NotTo(gomega.Equal(""), fmt.Sprintf("expected stale executor handoff evidence, got %#v", evidence.ExecutorHandoff))
	gomega.Expect(evidence.Packet.CandidateFiles).To(gomega.HaveLen(1), fmt.Sprintf("candidate files missing from phase evidence: %#v", evidence.Packet.CandidateFiles))
	gomega.Expect(evidence.Packet.CandidateFiles[0].Path).To(gomega.Equal("a.go"), fmt.Sprintf("candidate files missing from phase evidence: %#v", evidence.Packet.CandidateFiles))
	gomega.Expect(evidence.SuggestedTests).To(gomega.HaveLen(1), fmt.Sprintf("suggested tests missing from phase evidence: %#v", evidence.SuggestedTests))
	gomega.Expect(evidence.SuggestedTests[0].Command).To(gomega.Equal("go test ./..."), fmt.Sprintf("suggested tests missing from phase evidence: %#v", evidence.SuggestedTests))
	gomega.Expect(evidence.SourceSanity).To(gomega.HaveLen(1), fmt.Sprintf("source sanity missing from phase evidence: %#v", evidence.SourceSanity))
	gomega.Expect(evidence.SourceSanity[0].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusOK), fmt.Sprintf("source sanity missing from phase evidence: %#v", evidence.SourceSanity))
	gomega.Expect(evidence.SubmittedPlan).To(gomega.HaveLen(1), fmt.Sprintf("submitted plan state should show applied edit, got %#v", evidence.SubmittedPlan))
	gomega.Expect(evidence.SubmittedPlan[0].Status).To(gomega.Equal(plancontract.PhaseEvidenceStatusApplied), fmt.Sprintf("submitted plan state should show applied edit, got %#v", evidence.SubmittedPlan))
	gomega.Expect(slices.Contains(evidence.UnresolvedGaps, "reason=stale_runtime_trace trace_id=t1")).To(gomega.BeTrue(), fmt.Sprintf("packet gap should remain visible in unresolved gaps, got %#v", evidence.UnresolvedGaps))
	gomega.Expect(slices.Contains(evidence.OpenObligations, "packet_gap:reason=stale_runtime_trace trace_id=t1")).To(gomega.BeFalse(), fmt.Sprintf("informational packet gaps must not become repair obligations, got %#v", evidence.OpenObligations))

	telemetry := phaseEvidenceTelemetry(evidence)
	candidatePaths, ok := telemetry["candidate_file_paths"].([]string)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("telemetry should include bounded candidate paths, got %#v", telemetry["candidate_file_paths"]))
	gomega.Expect(candidatePaths).To(gomega.ContainElement("a.go"), fmt.Sprintf("telemetry should include bounded candidate paths, got %#v", telemetry["candidate_file_paths"]))

	testCommands, ok := telemetry["suggested_test_commands"].([]string)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("telemetry should include suggested test commands, got %#v", telemetry["suggested_test_commands"]))
	gomega.Expect(testCommands).To(gomega.ContainElement("go test ./..."), fmt.Sprintf("telemetry should include suggested test commands, got %#v", telemetry["suggested_test_commands"]))
})

var _ = ginkgo.It("load plan execute packet evidence rejects invalid pack id", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom", "comprehension", "views"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir views: %v", err))
	}

	if err := os.WriteFile(filepath.Join(root, ".idiom", "comprehension", "secret.json"), []byte(`{"pack_id":"secret"}`), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("seed escaped-looking artifact: %v", err))
	}

	got := loadPlanExecutePacketEvidence(root, "../secret")
	gomega.Expect(got.Status != PhaseEvidencePacketStatusInvalidPackID).To(gomega.BeFalse(), fmt.Sprintf("invalid pack ids must be rejected before cache-path fallback, got %#v", got))
})
