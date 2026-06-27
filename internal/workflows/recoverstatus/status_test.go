package recoverstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

func TestBuildAndFormatTextReportsRecoveryPacketStatus(t *testing.T) {
	root := t.TempDir()
	packet := recoverStatusTestPacket(t)

	packet.ProductLinks.PRDPaths = []string{"docs/prd.md"}
	packet.ResidualWork[0].EvidenceRefs = append(packet.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{
		Kind:    "legacy_failure",
		Path:    "internal/workflows/recoverstatus/status.go",
		Summary: "raw transcript details must not leak",
	})

	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	report, err := Build(root, "packet.json")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.Status != recoverycontract.PacketStatusReady || report.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("unexpected report status: %#v", report)
	}

	if report.WorkflowID != workflowstate.WorkflowRecovery ||
		report.WorkflowState != workflowstate.StateDecomposing ||
		report.WorkflowTerminalState != "" ||
		report.Dispatchable {

		t.Fatalf("unexpected workflow classification: %#v", report)
	}

	if report.PacketPath != "packet.json" || report.FailureID != packet.FailureID {
		t.Fatalf("unexpected packet identity: %#v", report)
	}

	if len(report.ResidualWork) != 1 || len(report.CandidateAtoms) != 1 {
		t.Fatalf("expected residual and atom: %#v", report)
	}

	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	if strings.Contains(string(body), "raw transcript") {
		t.Fatalf("report leaked evidence summary: %s", body)
	}

	text := FormatText(report)
	for _, want := range []string{
		"recover status: ready",
		"workflow_state: decomposing",
		"workflow_terminal_state: ",
		"dispatchable: false",
		"packet: packet.json",
		"failure_id: failure-status-test",
		"dirty_status: clean",
		"prd: docs/prd.md",
		"next_valid_action: review_decomposition",
		"- residual-001: Recover status test",
		"evidence_refs: required_path:internal/workflows/recoverstatus/status.go; required_test:go test ./internal/workflows/recoverstatus",
		"- recovery-001: Recover status test",
		"required_tests: go test ./internal/workflows/recoverstatus",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected text to contain %q, got:\n%s", want, text)
		}
	}

	for _, forbidden := range []string{"recover promote", "recover reconcile", "recovery_decompose", "raw transcript"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status text advertised or leaked %q:\n%s", forbidden, text)
		}
	}
}

func TestBuildUsesValidSiblingReconciliationForRemainingStatus(t *testing.T) {
	root := t.TempDir()
	packet := recoverStatusTestPacket(t)

	packet.ResidualWork = append(packet.ResidualWork, recoverycontract.ResidualWork{
		ID:     "residual-002",
		Title:  "Second residual",
		Reason: string(recoverycontract.FailureClassExecutorEdit),
		Paths:  []string{"internal/surfaces/tui/app/recover_status.go"},
	})
	packet.CandidateAtoms = append(packet.CandidateAtoms, recoverycontract.CandidateAtom{
		ID:              "recovery-002",
		Title:           "Second residual",
		RequiredPaths:   []string{"internal/surfaces/tui/app/recover_status.go"},
		RequiredTests:   []string{"go test ./internal/surfaces/tui/app"},
		Gates:           []string{recoverycontract.GateWorkspaceState},
		ResidualWorkIDs: []string{"residual-002"},
	})

	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "nested/packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recoverstatus",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	body, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "nested", "reconciliation.json"), append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write reconciliation: %v", err)
	}

	report, err := Build(root, "nested/packet.json")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.Status != recoverycontract.PacketStatusPartial || report.ReconciliationPath != "nested/reconciliation.json" {
		t.Fatalf("expected partial sibling reconciliation status, got %#v", report)
	}

	if report.WorkflowState != workflowstate.StateReconciling || report.WorkflowTerminalState != "" || report.Dispatchable {
		t.Fatalf("expected partial recovery report to remain nonterminal reconciling, got %#v", report)
	}

	if len(report.CandidateAtoms) != 1 || report.CandidateAtoms[0].ID != "recovery-002" {
		t.Fatalf("expected only remaining atom, got %#v", report.CandidateAtoms)
	}

	if len(report.ResidualWork) != 1 || report.ResidualWork[0].ID != "residual-002" {
		t.Fatalf("expected only remaining residual, got %#v", report.ResidualWork)
	}
}

func TestBuildReportsInvalidSiblingReconciliationAsBlocked(t *testing.T) {
	root := t.TempDir()
	packet := recoverStatusTestPacket(t)

	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "nested/packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recoverstatus",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	reconciliation.PacketDigest = "sha256:stale"

	body, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "nested", "reconciliation.json"), append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write reconciliation: %v", err)
	}

	report, err := Build(root, "nested/packet.json")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if report.Status != recoverycontract.PacketStatusBlocked || report.NextAction != recoverycontract.NextActionInspectFailure {
		t.Fatalf("expected blocked invalid reconciliation report, got %#v", report)
	}

	if report.WorkflowState != workflowstate.StateTerminal || report.WorkflowTerminalState != workflowstate.TerminalStateBlocked || report.Dispatchable {
		t.Fatalf("blocked reconciliation should not be dispatchable, got %#v", report)
	}

	if report.ReconciliationPath != "nested/reconciliation.json" || report.Reconciliation == nil {
		t.Fatalf("expected reconciliation identity to remain visible, got %#v", report)
	}

	foundDigestMismatch := false

	for _, blocker := range report.Blockers {
		if strings.Contains(blocker, "recovery_reconciliation_packet_digest_mismatch") {
			foundDigestMismatch = true
			break
		}
	}

	if !foundDigestMismatch {
		t.Fatalf("expected packet digest mismatch blocker, got %#v", report.Blockers)
	}
}

func TestBuildRejectsPacketOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()

	if _, _, err := recovery.WriteDecompositionPacket(outsideRoot, recoverStatusTestPacket(t), "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	_, err := Build(root, filepath.Join(outsideRoot, "packet.json"))
	if err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("expected outside packet path rejection, got %v", err)
	}
}

func TestBuildRejectsSymlinkedPacketPath(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePacket := filepath.Join(outsideRoot, "packet.json")

	if _, _, err := recovery.WriteDecompositionPacket(outsideRoot, recoverStatusTestPacket(t), "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	if err := os.Symlink(outsidePacket, filepath.Join(root, "packet.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := Build(root, "packet.json")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink packet path rejection, got %v", err)
	}
}

func TestBuildRejectsSymlinkedDefaultReconciliationPath(t *testing.T) {
	root := t.TempDir()
	packet := recoverStatusTestPacket(t)

	packet.FailureID = "failure-status-symlink-reconciliation"

	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	outsideRoot := t.TempDir()

	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		Now:              time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC),
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recoverstatus",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	outsideBody, err := json.MarshalIndent(reconciliation, "", "  ")
	if err != nil {
		t.Fatalf("marshal reconciliation: %v", err)
	}

	outsidePath := filepath.Join(outsideRoot, "reconciliation.json")
	if err := os.WriteFile(outsidePath, append(outsideBody, '\n'), 0o644); err != nil {
		t.Fatalf("write outside reconciliation: %v", err)
	}

	defaultPath := filepath.Join(root, ".idiom", "recovery", packet.FailureID, "reconciliation.json")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o755); err != nil {
		t.Fatalf("mkdir default reconciliation dir: %v", err)
	}

	if err := os.Symlink(outsidePath, defaultPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err = Build(root, "packet.json")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked reconciliation rejection, got %v", err)
	}
}

func recoverStatusTestPacket(t *testing.T) recoverycontract.DecompositionPacket {
	t.Helper()

	return recoverycontract.DecompositionPacket{
		SchemaVersion: recoverycontract.DecompositionPacketSchemaVersion,
		PacketID:      "packet-failure-status-test",
		FailureID:     "failure-status-test",
		CreatedAt:     time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		UpdatedAt:     time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		SourceRun: recoverycontract.SourceRun{
			Lane:              string(recoverycontract.SourceLaneImplement),
			SessionID:         "session-status-test",
			RequiredEditPaths: []string{"internal/workflows/recoverstatus/status.go"},
		},
		PartialState: recoverycontract.PartialState{
			DirtyStatus: recoverycontract.DirtyStatusClean,
			ScopeStatus: recoverycontract.ScopeStatusInScope,
		},
		Failure: recoverycontract.Failure{
			Class:   recoverycontract.FailureClassExecutorEdit,
			Summary: "recover status test failure",
		},
		ResidualWork: []recoverycontract.ResidualWork{{
			ID:     "residual-001",
			Title:  "Recover status test",
			Reason: string(recoverycontract.FailureClassExecutorEdit),
			Paths:  []string{"internal/workflows/recoverstatus/status.go"},
			EvidenceRefs: []recoverycontract.EvidenceRef{
				{Kind: "required_path", Path: "internal/workflows/recoverstatus/status.go"},
				{Kind: "required_test", ID: "go test ./internal/workflows/recoverstatus"},
			},
		}},
		CandidateAtoms: []recoverycontract.CandidateAtom{{
			ID:              "recovery-001",
			Title:           "Recover status test",
			RequiredPaths:   []string{"internal/workflows/recoverstatus/status.go"},
			RequiredTests:   []string{"go test ./internal/workflows/recoverstatus"},
			Gates:           []string{recoverycontract.GateWorkspaceState, recoverycontract.GateBriefLint},
			ResidualWorkIDs: []string{"residual-001"},
		}},
		NextAction: recoverycontract.NextActionReviewDecomposition,
	}
}
