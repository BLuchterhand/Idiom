package recovery

import (
	"testing"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
)

func TestProjectPacketStatusReportsBasePacketState(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()

	projection := ProjectPacketStatus(packet, nil)

	if projection.Status != recoverycontract.PacketStatusReady || projection.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("unexpected projection: %#v", projection)
	}

	if projection.WorkflowID != workflowstate.WorkflowRecovery ||
		projection.WorkflowState != workflowstate.StateDecomposing ||
		projection.WorkflowTerminalState != "" ||
		projection.Dispatchable {

		t.Fatalf("ready packet should be a nonterminal decomposing workflow state, got %#v", projection)
	}

	if len(projection.Blockers) != 0 {
		t.Fatalf("ready packet should not have blockers: %#v", projection.Blockers)
	}

	if len(projection.CandidateAtoms) != 2 || len(projection.ResidualWork) != 2 {
		t.Fatalf("projection dropped packet work: %#v", projection)
	}
}

func TestProjectPacketStatusAppliesPartialReconciliationRemainingWork(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recovery",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	projection := ProjectPacketStatus(packet, &reconciliation)

	if projection.Status != recoverycontract.PacketStatusPartial || projection.NextAction != recoverycontract.NextActionReviewDecomposition {
		t.Fatalf("unexpected reconciled projection: %#v", projection)
	}

	if len(projection.CandidateAtoms) != 1 || projection.CandidateAtoms[0].ID != "recovery-002" {
		t.Fatalf("expected only remaining atom, got %#v", projection.CandidateAtoms)
	}

	if projection.WorkflowState != workflowstate.StateReconciling || projection.WorkflowTerminalState != "" || projection.Dispatchable {
		t.Fatalf("partial recovery should remain nonterminal reconciling, got %#v", projection)
	}

	if len(projection.ResidualWork) != 1 || projection.ResidualWork[0].ID != "residual-002" {
		t.Fatalf("expected only remaining residual, got %#v", projection.ResidualWork)
	}

	if len(projection.ReconciliationFindings) != 0 || len(projection.Blockers) != 0 {
		t.Fatalf("valid partial reconciliation should not add findings/blockers: %#v", projection)
	}
}

func TestProjectPacketStatusBlocksInvalidReconciliation(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recovery",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	reconciliation.PacketDigest = "sha256:stale"
	reconciliation.Blockers = []string{"operator review required"}

	projection := ProjectPacketStatus(packet, &reconciliation)

	if projection.Status != recoverycontract.PacketStatusBlocked || projection.NextAction != recoverycontract.NextActionInspectFailure {
		t.Fatalf("invalid reconciliation should block inspection, got %#v", projection)
	}

	if projection.WorkflowState != workflowstate.StateTerminal || projection.WorkflowTerminalState != workflowstate.TerminalStateBlocked || projection.Dispatchable {
		t.Fatalf("invalid reconciliation should not be dispatchable, got %#v", projection)
	}

	if len(projection.ReconciliationFindings) == 0 {
		t.Fatalf("expected reconciliation validation findings")
	}

	if !statusProjectionBlockersContain(projection.Blockers, "recovery_reconciliation_packet_digest_mismatch:") ||
		!statusProjectionBlockersContain(projection.Blockers, "operator review required") {

		t.Fatalf("expected validation and reconciliation blockers, got %#v", projection.Blockers)
	}
}

func TestProjectPacketStatusClearsRecoveredBlockers(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()

	packet.PartialState.DirtyStatus = recoverycontract.DirtyStatusDirtyOutScope

	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001", "recovery-002"},
		Verification: recoverycontract.ReconciliationVerification{
			Status: recoverycontract.ReconciliationVerificationPass,
			Commands: []recoverycontract.ReconciliationCommandResult{{
				Command:  "go test ./internal/workflows/recovery",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}, {
				Command:  "go test ./internal/workflows/recoverstatus",
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	projection := ProjectPacketStatus(packet, &reconciliation)

	if projection.Status != recoverycontract.PacketStatusRecovered || projection.NextAction != recoverycontract.NextActionNone {
		t.Fatalf("expected recovered projection, got %#v", projection)
	}

	if projection.WorkflowState != workflowstate.StateTerminal || projection.WorkflowTerminalState != workflowstate.TerminalStateComplete || projection.Dispatchable {
		t.Fatalf("recovered packet should be complete and not dispatchable, got %#v", projection)
	}

	if len(projection.CandidateAtoms) != 0 || len(projection.ResidualWork) != 0 || len(projection.Blockers) != 0 {
		t.Fatalf("recovered projection should clear remaining work and blockers: %#v", projection)
	}
}

func TestProjectPacketStatusReportsDirtyScope(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()
	packet.PartialState.DirtyStatus = recoverycontract.DirtyStatusDirtyOutScope

	projection := ProjectPacketStatus(packet, nil)

	if projection.WorkflowState != workflowstate.StateTerminal || projection.WorkflowTerminalState != workflowstate.TerminalStateDirtyScope || projection.Dispatchable {
		t.Fatalf("dirty out-of-scope packet should be dirty-scope and not dispatchable, got %#v", projection)
	}
}

func TestBlockedPacketStatusProjectionRecordsProjectionValidationError(t *testing.T) {
	projection := blockedPacketStatusProjection(recoverycontract.PacketStatusProjection{
		Status:        recoverycontract.PacketStatusReady,
		WorkflowID:    workflowstate.WorkflowRecovery,
		WorkflowState: workflowstate.StateDecomposing,
		Dispatchable:  true,
	}, workflowstate.ValidateStateProjection(workflowstate.WorkflowRecovery, workflowstate.StateDecomposing, "", true))

	if projection.Status != recoverycontract.PacketStatusBlocked ||
		projection.WorkflowState != workflowstate.StateTerminal ||
		projection.WorkflowTerminalState != workflowstate.TerminalStateBlocked ||
		projection.Dispatchable ||
		projection.NextAction != recoverycontract.NextActionInspectFailure {

		t.Fatalf("invalid projection should become blocked inspect projection, got %#v", projection)
	}

	if !statusProjectionBlockersContain(projection.Blockers, "workflow projection invalid:") {
		t.Fatalf("projection validation error missing from blockers: %#v", projection.Blockers)
	}
}

func recoveryStatusProjectionTestPacket() recoverycontract.DecompositionPacket {
	return recoverycontract.DecompositionPacket{
		SchemaVersion: recoverycontract.DecompositionPacketSchemaVersion,
		PacketID:      "packet-status-projection",
		FailureID:     "failure-status-projection",
		PartialState: recoverycontract.PartialState{
			DirtyStatus: recoverycontract.DirtyStatusClean,
			ScopeStatus: recoverycontract.ScopeStatusInScope,
		},
		ResidualWork: []recoverycontract.ResidualWork{{
			ID:    "residual-001",
			Title: "First residual",
			Paths: []string{"internal/workflows/recovery/status_projection.go"},
		}, {
			ID:    "residual-002",
			Title: "Second residual",
			Paths: []string{"internal/workflows/recoverstatus/status.go"},
		}},
		CandidateAtoms: []recoverycontract.CandidateAtom{{
			ID:              "recovery-001",
			Title:           "First residual",
			RequiredPaths:   []string{"internal/workflows/recovery/status_projection.go"},
			RequiredTests:   []string{"go test ./internal/workflows/recovery"},
			ResidualWorkIDs: []string{"residual-001"},
		}, {
			ID:              "recovery-002",
			Title:           "Second residual",
			RequiredPaths:   []string{"internal/workflows/recoverstatus/status.go"},
			RequiredTests:   []string{"go test ./internal/workflows/recoverstatus"},
			ResidualWorkIDs: []string{"residual-002"},
		}},
		NextAction: recoverycontract.NextActionReviewDecomposition,
	}
}

func statusProjectionBlockersContain(blockers []string, want string) bool {
	for _, blocker := range blockers {
		if len(blocker) >= len(want) && blocker[:len(want)] == want {
			return true
		}
	}

	return false
}
