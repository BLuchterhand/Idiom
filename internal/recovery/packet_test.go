package recovery

import (
	"os"
	"path/filepath"
	"strings"
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

func TestWriteDecompositionPacketRejectsExistingTempSymlink(t *testing.T) {
	root := t.TempDir()
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-temp-symlink",
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
	fullPath, _, err := DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir packet dir: %v", err)
	}
	controlPath := filepath.Join(filepath.Dir(fullPath), "failure.json")
	before := []byte("failure record must not be overwritten\n")
	if err := os.WriteFile(controlPath, before, 0o600); err != nil {
		t.Fatalf("write control artifact: %v", err)
	}
	if err := os.Symlink(controlPath, fullPath+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, _, err := WriteDecompositionPacket(root, packet, ""); err == nil || !strings.Contains(err.Error(), "write recovery decomposition packet") {
		t.Fatalf("expected temp symlink write rejection, got %v", err)
	}
	after, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read control artifact: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("control artifact should not be overwritten")
	}
}

func TestBuildWriteAndLoadReconciliation(t *testing.T) {
	root := t.TempDir()
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/recovery/record.go", "internal/recovery/packet.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 12, 2, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	if reconciliation.Status != ReconciliationStatusRecovered || reconciliation.NextAction != NextActionNone {
		t.Fatalf("unexpected reconciliation: %#v", reconciliation)
	}
	if reconciliation.PacketDigest == "" || reconciliation.PacketDigest != PacketDigest(packet) {
		t.Fatalf("unexpected packet digest: %#v want %s", reconciliation, PacketDigest(packet))
	}
	if findings := ValidateReconciliation(packet, reconciliation); len(findings) != 0 {
		t.Fatalf("expected valid reconciliation, got %#v", findings)
	}
	if len(reconciliation.CompletedAtoms) != 1 || reconciliation.CompletedAtoms[0].ID != "recovery-001" {
		t.Fatalf("completed atoms=%#v", reconciliation.CompletedAtoms)
	}
	_, relPath, err := WriteReconciliation(root, reconciliation, "")
	if err != nil {
		t.Fatalf("WriteReconciliation: %v", err)
	}
	wantRel := filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "reconciliation.json"))
	if relPath != wantRel {
		t.Fatalf("rel path=%q want %q", relPath, wantRel)
	}
	loaded, _, found, err := TryLoadReconciliationForPacket(root, packet)
	if err != nil || !found {
		t.Fatalf("TryLoadReconciliationForPacket found=%v err=%v", found, err)
	}
	if loaded.Status != ReconciliationStatusRecovered || loaded.CompletedAtoms[0].ID != "recovery-001" {
		t.Fatalf("loaded reconciliation mismatch: %#v", loaded)
	}
}

func TestBuildReconciliationTracksRemainingAtoms(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-partial",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/recovery/record.go", "cmd/idiom/recover.go", "internal/app/model.go", "internal/workspace/tools.go", "README.md"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if len(packet.CandidateAtoms) < 2 {
		t.Fatalf("expected grouped candidate atoms, got %#v", packet.CandidateAtoms)
	}
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{packet.CandidateAtoms[0].ID},
		Verification:     reconciliationVerificationForPacket(packet, packet.CandidateAtoms[0].ID),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	if reconciliation.Status != ReconciliationStatusPartial || reconciliation.NextAction != NextActionReviewDecomposition {
		t.Fatalf("expected partial reconciliation, got %#v", reconciliation)
	}
	if len(reconciliation.RemainingAtoms) != len(packet.CandidateAtoms)-1 {
		t.Fatalf("remaining atoms=%#v packet=%#v", reconciliation.RemainingAtoms, packet.CandidateAtoms)
	}
}

func TestBuildReconciliationRejectsUnknownAtom(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-unknown",
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
	if _, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"missing"}}); err == nil {
		t.Fatal("expected unknown atom error")
	}
}

func TestValidateReconciliationRejectsStaleOrHandEditedArtifact(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-stale",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/recovery/record.go", "internal/recovery/packet.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     reconciliationVerificationForPacket(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	reconciliation.PacketDigest = ""
	findings := ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_packet_digest") {
		t.Fatalf("expected missing digest finding, got %#v", findings)
	}
	reconciliation.PacketDigest = "sha256:stale"
	findings = ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_packet_digest_mismatch") {
		t.Fatalf("expected digest mismatch finding, got %#v", findings)
	}
	reconciliation.PacketDigest = PacketDigest(packet)
	reconciliation.CompletedAtoms[0].RequiredPaths = []string{"different.go"}
	findings = ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_completed_atom_mismatch") {
		t.Fatalf("expected atom mismatch finding, got %#v", findings)
	}
}

func TestValidateReconciliationRequiresVerificationEvidence(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-unverified",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{CompletedAtomIDs: []string{"recovery-001"}})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	findings := ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_verification") {
		t.Fatalf("expected verification finding, got %#v", findings)
	}
}

func TestValidateReconciliationRejectsPassingStatusWithNonZeroExit(t *testing.T) {
	record, err := BuildPlanExecuteFailureRecord(PlanExecuteFailureInput{
		SessionID:         "session-reconcile-nonzero",
		FailureKind:       "executor_failed",
		RequiredEditPaths: []string{"internal/recovery/record.go"},
	}, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := BuildDecompositionPacket(record, DecompositionOptions{Now: time.Date(2026, 5, 18, 12, 1, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	verification := reconciliationVerificationForPacket(packet, "recovery-001")
	if len(verification.Commands) == 0 {
		t.Fatalf("expected required test command in fixture")
	}
	verification.Commands[0].ExitCode = 1
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     verification,
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	findings := ValidateReconciliation(packet, reconciliation)
	if !reconciliationFindingsContain(findings, "recovery_reconciliation_missing_required_tests") {
		t.Fatalf("expected required-test finding, got %#v", findings)
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

func reconciliationFindingsContain(findings []ReconciliationValidationFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func reconciliationVerificationForPacket(packet DecompositionPacket, atomIDs ...string) ReconciliationVerification {
	completed := map[string]bool{}
	for _, id := range atomIDs {
		completed[id] = true
	}
	seen := map[string]bool{}
	var commands []ReconciliationCommandResult
	var required []string
	for _, atom := range packet.CandidateAtoms {
		if !completed[atom.ID] {
			continue
		}
		for _, test := range atom.RequiredTests {
			if seen[test] {
				continue
			}
			seen[test] = true
			required = append(required, test)
			commands = append(commands, ReconciliationCommandResult{
				Command:  test,
				Status:   ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}
	return ReconciliationVerification{
		Status:        ReconciliationVerificationPass,
		RequiredTests: required,
		Commands:      commands,
	}
}
