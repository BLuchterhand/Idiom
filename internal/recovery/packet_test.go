package recovery

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBuildDecompositionPacketCreatesCandidateAtom(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-4",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"internal/recovery/record.go", "internal/recovery/packet.go"},
		ChangedPaths:      []string{"internal/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{
		Now:          time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC),
		PartialState: PartialStateForDirtyPaths(nil, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1),
	})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if packet.SchemaVersion != DecompositionPacketSchemaVersion {
		t.Fatalf("schema=%q", packet.SchemaVersion)
	}
	if packet.NextAction != NextActionReviewDecomposition {
		t.Fatalf("next action=%q", packet.NextAction)
	}
	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}
	atom := packet.CandidateAtoms[0]
	if atom.ID != "recovery-001" || atom.Complexity != "C1" {
		t.Fatalf("candidate atom=%#v", atom)
	}
	if !stringSlicesEqual(atom.Gates, []string{GateWorkspaceState, GateBriefLint, GateAtomIntake, GateScope, GateRequiredTests}) {
		t.Fatalf("gates=%#v", atom.Gates)
	}
	if !stringSlicesEqual(atom.RequiredTests, []string{"go test ./internal/recovery"}) {
		t.Fatalf("required tests=%#v", atom.RequiredTests)
	}
}

func TestBuildDecompositionPacketUsesDeepestGoPackageForRequiredTests(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-nested",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/frontier/workerruntime/runtime.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if len(packet.CandidateAtoms) != 1 {
		t.Fatalf("candidate atoms=%#v", packet.CandidateAtoms)
	}
	if !stringSlicesEqual(packet.CandidateAtoms[0].RequiredTests, []string{"go test ./internal/frontier/workerruntime"}) {
		t.Fatalf("required tests=%#v", packet.CandidateAtoms[0].RequiredTests)
	}
}

func TestWriteAndLoadDecompositionPacket(t *testing.T) {
	root := t.TempDir()
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-5",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"a.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	fullPath, relPath, err := WriteDecompositionPacket(root, packet, "")
	if err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	wantRel := filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "packet.json"))
	if relPath != wantRel {
		t.Fatalf("rel path=%q want %q", relPath, wantRel)
	}
	loaded, _, found, err := TryLoadDecompositionPacket(fullPath)
	if err != nil || !found {
		t.Fatalf("TryLoadDecompositionPacket found=%v err=%v", found, err)
	}
	if loaded.PacketID != packet.PacketID || loaded.FailureID != packet.FailureID {
		t.Fatalf("loaded packet mismatch: %#v", loaded)
	}
}

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
