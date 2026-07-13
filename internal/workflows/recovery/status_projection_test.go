package recovery

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
)

var _ = ginkgo.Describe("Recovery packet status projection", func() {
	ginkgo.It("reports the base packet as nonterminal decomposition work", func() {
		projection := ProjectPacketStatus(recoveryStatusProjectionTestPacket(), nil)

		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Status":                gomega.Equal(recoverycontract.PacketStatusReady),
			"NextAction":            gomega.Equal(recoverycontract.NextActionReviewDecomposition),
			"WorkflowID":            gomega.Equal(workflowstate.WorkflowRecovery),
			"WorkflowState":         gomega.Equal(workflowstate.StateDecomposing),
			"WorkflowTerminalState": gomega.BeEmpty(),
			"Dispatchable":          gomega.BeFalse(),
			"Blockers":              gomega.BeEmpty(),
			"CandidateAtoms":        gomega.HaveLen(2),
			"ResidualWork":          gomega.HaveLen(2),
		}))
	})

	ginkgo.It("projects only work remaining after a partial reconciliation", func() {
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		projection := ProjectPacketStatus(packet, &reconciliation)
		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Status":                 gomega.Equal(recoverycontract.PacketStatusPartial),
			"NextAction":             gomega.Equal(recoverycontract.NextActionReviewDecomposition),
			"WorkflowState":          gomega.Equal(workflowstate.StateReconciling),
			"WorkflowTerminalState":  gomega.BeEmpty(),
			"Dispatchable":           gomega.BeFalse(),
			"ReconciliationFindings": gomega.BeEmpty(),
			"Blockers":               gomega.BeEmpty(),
		}))
		gomega.Expect(projection.CandidateAtoms).To(gomega.ConsistOf(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"ID": gomega.Equal("recovery-002"),
		})))
		gomega.Expect(projection.ResidualWork).To(gomega.ConsistOf(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"ID": gomega.Equal("residual-002"),
		})))
	})

	ginkgo.It("blocks an invalid reconciliation and exposes its findings", func() {
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		reconciliation.PacketDigest = "sha256:stale"
		reconciliation.Blockers = []string{"operator review required"}

		projection := ProjectPacketStatus(packet, &reconciliation)
		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Status":                 gomega.Equal(recoverycontract.PacketStatusBlocked),
			"NextAction":             gomega.Equal(recoverycontract.NextActionInspectFailure),
			"WorkflowState":          gomega.Equal(workflowstate.StateTerminal),
			"WorkflowTerminalState":  gomega.Equal(workflowstate.TerminalStateBlocked),
			"Dispatchable":           gomega.BeFalse(),
			"ReconciliationFindings": gomega.Not(gomega.BeEmpty()),
		}))
		gomega.Expect(projection.Blockers).To(gomega.And(
			gomega.ContainElement(gomega.HavePrefix("recovery_reconciliation_packet_digest_mismatch:")),
			gomega.ContainElement("operator review required"),
		))
	})

	ginkgo.It("clears recovered work and prior packet blockers", func() {
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
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		projection := ProjectPacketStatus(packet, &reconciliation)
		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Status":                gomega.Equal(recoverycontract.PacketStatusRecovered),
			"NextAction":            gomega.Equal(recoverycontract.NextActionNone),
			"WorkflowState":         gomega.Equal(workflowstate.StateTerminal),
			"WorkflowTerminalState": gomega.Equal(workflowstate.TerminalStateComplete),
			"Dispatchable":          gomega.BeFalse(),
			"CandidateAtoms":        gomega.BeEmpty(),
			"ResidualWork":          gomega.BeEmpty(),
			"Blockers":              gomega.BeEmpty(),
		}))
	})

	ginkgo.It("reports dirty out-of-scope work as a terminal dirty-scope state", func() {
		packet := recoveryStatusProjectionTestPacket()
		packet.PartialState.DirtyStatus = recoverycontract.DirtyStatusDirtyOutScope

		projection := ProjectPacketStatus(packet, nil)
		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"WorkflowState":         gomega.Equal(workflowstate.StateTerminal),
			"WorkflowTerminalState": gomega.Equal(workflowstate.TerminalStateDirtyScope),
			"Dispatchable":          gomega.BeFalse(),
		}))
	})

	ginkgo.It("records a workflow projection validation error in a blocked projection", func() {
		projection := blockedPacketStatusProjection(recoverycontract.PacketStatusProjection{
			Status:        recoverycontract.PacketStatusReady,
			WorkflowID:    workflowstate.WorkflowRecovery,
			WorkflowState: workflowstate.StateDecomposing,
			Dispatchable:  true,
		}, workflowstate.ValidateStateProjection(workflowstate.WorkflowRecovery, workflowstate.StateDecomposing, "", true))

		gomega.Expect(projection).To(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Status":                gomega.Equal(recoverycontract.PacketStatusBlocked),
			"WorkflowState":         gomega.Equal(workflowstate.StateTerminal),
			"WorkflowTerminalState": gomega.Equal(workflowstate.TerminalStateBlocked),
			"Dispatchable":          gomega.BeFalse(),
			"NextAction":            gomega.Equal(recoverycontract.NextActionInspectFailure),
		}))
		gomega.Expect(projection.Blockers).To(gomega.ContainElement(gomega.HavePrefix("workflow projection invalid:")))
	})
})

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
