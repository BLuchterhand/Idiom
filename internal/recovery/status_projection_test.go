package recovery

import "testing"

func TestProjectPacketStatusReportsBasePacketState(t *testing.T) {
	packet := recoveryStatusProjectionTestPacket()

	projection := ProjectPacketStatus(packet, nil)

	if projection.Status != PacketStatusReady || projection.NextAction != NextActionReviewDecomposition {
		t.Fatalf("unexpected projection: %#v", projection)
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
		Verification: ReconciliationVerification{
			Status: ReconciliationVerificationPass,
			Commands: []ReconciliationCommandResult{{
				Command:  "go test ./internal/recovery",
				Status:   ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	projection := ProjectPacketStatus(packet, &reconciliation)

	if projection.Status != PacketStatusPartial || projection.NextAction != NextActionReviewDecomposition {
		t.Fatalf("unexpected reconciled projection: %#v", projection)
	}
	if len(projection.CandidateAtoms) != 1 || projection.CandidateAtoms[0].ID != "recovery-002" {
		t.Fatalf("expected only remaining atom, got %#v", projection.CandidateAtoms)
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
		Verification: ReconciliationVerification{
			Status: ReconciliationVerificationPass,
			Commands: []ReconciliationCommandResult{{
				Command:  "go test ./internal/recovery",
				Status:   ReconciliationVerificationPass,
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

	if projection.Status != PacketStatusBlocked || projection.NextAction != NextActionInspectFailure {
		t.Fatalf("invalid reconciliation should block inspection, got %#v", projection)
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
	packet.PartialState.DirtyStatus = DirtyStatusDirtyOutScope
	reconciliation, err := BuildReconciliation(packet, ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001", "recovery-002"},
		Verification: ReconciliationVerification{
			Status: ReconciliationVerificationPass,
			Commands: []ReconciliationCommandResult{{
				Command:  "go test ./internal/recovery",
				Status:   ReconciliationVerificationPass,
				ExitCode: 0,
			}, {
				Command:  "go test ./internal/recoverstatus",
				Status:   ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}

	projection := ProjectPacketStatus(packet, &reconciliation)

	if projection.Status != PacketStatusRecovered || projection.NextAction != NextActionNone {
		t.Fatalf("expected recovered projection, got %#v", projection)
	}
	if len(projection.CandidateAtoms) != 0 || len(projection.ResidualWork) != 0 || len(projection.Blockers) != 0 {
		t.Fatalf("recovered projection should clear remaining work and blockers: %#v", projection)
	}
}

func recoveryStatusProjectionTestPacket() DecompositionPacket {
	return DecompositionPacket{
		SchemaVersion: DecompositionPacketSchemaVersion,
		PacketID:      "packet-status-projection",
		FailureID:     "failure-status-projection",
		PartialState: PartialState{
			DirtyStatus: DirtyStatusClean,
			ScopeStatus: ScopeStatusInScope,
		},
		ResidualWork: []ResidualWork{{
			ID:    "residual-001",
			Title: "First residual",
			Paths: []string{"internal/recovery/status_projection.go"},
		}, {
			ID:    "residual-002",
			Title: "Second residual",
			Paths: []string{"internal/recoverstatus/status.go"},
		}},
		CandidateAtoms: []CandidateAtom{{
			ID:              "recovery-001",
			Title:           "First residual",
			RequiredPaths:   []string{"internal/recovery/status_projection.go"},
			RequiredTests:   []string{"go test ./internal/recovery"},
			ResidualWorkIDs: []string{"residual-001"},
		}, {
			ID:              "recovery-002",
			Title:           "Second residual",
			RequiredPaths:   []string{"internal/recoverstatus/status.go"},
			RequiredTests:   []string{"go test ./internal/recoverstatus"},
			ResidualWorkIDs: []string{"residual-002"},
		}},
		NextAction: NextActionReviewDecomposition,
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
