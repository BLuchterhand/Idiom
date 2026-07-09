package planexecute

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/repository/comprehension"
)

func TestCollectPlanExecutePhaseEvidenceLoadsPacketAndExecutorChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/evidence\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"alpha\" }\n"), 0o644); err != nil {
		t.Fatalf("seed a.go: %v", err)
	}

	build, err := comprehension.Build(t.Context(), comprehension.BuildOptions{Root: root, Language: "go"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

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
		t.Fatalf("SaveView: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package pkg\n\nfunc A() string { return \"beta\" }\n"), 0o644); err != nil {
		t.Fatalf("seed post-execute a.go: %v", err)
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

	if evidence.SchemaVersion != PhaseEvidenceSchemaVersion {
		t.Fatalf("schema version = %q", evidence.SchemaVersion)
	}
	if evidence.Phase != PhaseEvidencePhaseReviewRepair {
		t.Fatalf("phase = %q, want %q", evidence.Phase, PhaseEvidencePhaseReviewRepair)
	}
	if evidence.Packet.Status != PhaseEvidencePacketStatusAvailableStale || evidence.Packet.PackID != key.ID() {
		t.Fatalf("expected stale graph-view packet %s, got %#v", key.ID(), evidence.Packet)
	}
	if evidence.ExecutorHandoff.PacketState != plancontract.ExecutorHandoffStateStale || evidence.ExecutorHandoff.Reason == "" {
		t.Fatalf("expected stale executor handoff evidence, got %#v", evidence.ExecutorHandoff)
	}
	if len(evidence.Packet.CandidateFiles) != 1 || evidence.Packet.CandidateFiles[0].Path != "a.go" {
		t.Fatalf("candidate files missing from phase evidence: %#v", evidence.Packet.CandidateFiles)
	}
	if len(evidence.SuggestedTests) != 1 || evidence.SuggestedTests[0].Command != "go test ./..." {
		t.Fatalf("suggested tests missing from phase evidence: %#v", evidence.SuggestedTests)
	}
	if len(evidence.SourceSanity) != 1 || evidence.SourceSanity[0].Status != "ok" {
		t.Fatalf("source sanity missing from phase evidence: %#v", evidence.SourceSanity)
	}
	if len(evidence.SubmittedPlan) != 1 || evidence.SubmittedPlan[0].Status != "applied" {
		t.Fatalf("submitted plan state should show applied edit, got %#v", evidence.SubmittedPlan)
	}
	if !slices.Contains(evidence.UnresolvedGaps, "reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("packet gap should remain visible in unresolved gaps, got %#v", evidence.UnresolvedGaps)
	}
	if slices.Contains(evidence.OpenObligations, "packet_gap:reason=stale_runtime_trace trace_id=t1") {
		t.Fatalf("informational packet gaps must not become repair obligations, got %#v", evidence.OpenObligations)
	}

	telemetry := phaseEvidenceTelemetry(evidence)
	candidatePaths, ok := telemetry["candidate_file_paths"].([]string)
	if !ok || !slices.Contains(candidatePaths, "a.go") {
		t.Fatalf("telemetry should include bounded candidate paths, got %#v", telemetry["candidate_file_paths"])
	}
	testCommands, ok := telemetry["suggested_test_commands"].([]string)
	if !ok || !slices.Contains(testCommands, "go test ./...") {
		t.Fatalf("telemetry should include suggested test commands, got %#v", telemetry["suggested_test_commands"])
	}
}

func TestLoadPlanExecutePacketEvidenceRejectsInvalidPackID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom", "comprehension", "views"), 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".idiom", "comprehension", "secret.json"), []byte(`{"pack_id":"secret"}`), 0o644); err != nil {
		t.Fatalf("seed escaped-looking artifact: %v", err)
	}

	got := loadPlanExecutePacketEvidence(root, "../secret")
	if got.Status != PhaseEvidencePacketStatusInvalidPackID {
		t.Fatalf("invalid pack ids must be rejected before cache-path fallback, got %#v", got)
	}
}
