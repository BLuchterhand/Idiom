package recoverstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("Recovery status", func() {
	ginkgo.It("reports a ready decomposition packet without leaking evidence summaries", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := recoverStatusTestPacket()
		packet.ProductLinks.PRDPaths = []string{"docs/prd.md"}
		packet.ResidualWork[0].EvidenceRefs = append(packet.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{
			Kind:    "legacy_failure",
			Path:    "internal/workflows/recoverstatus/status.go",
			Summary: "raw transcript details must not leak",
		})

		_, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		report, err := Build(root, "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(report.Status).To(gomega.Equal(recoverycontract.PacketStatusReady))
		gomega.Expect(report.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition))
		gomega.Expect(report.WorkflowID).To(gomega.Equal(workflowstate.WorkflowRecovery))
		gomega.Expect(report.WorkflowState).To(gomega.Equal(workflowstate.StateDecomposing))
		gomega.Expect(report.WorkflowTerminalState).To(gomega.BeEmpty())
		gomega.Expect(report.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(report.PacketPath).To(gomega.Equal("packet.json"))
		gomega.Expect(report.FailureID).To(gomega.Equal(packet.FailureID))
		gomega.Expect(report.ResidualWork).To(gomega.HaveLen(1))
		gomega.Expect(report.CandidateAtoms).To(gomega.HaveLen(1))

		body, err := json.Marshal(report)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(body)).NotTo(gomega.ContainSubstring("raw transcript"))

		text := FormatText(report)
		gomega.Expect(text).To(gomega.And(
			gomega.ContainSubstring("recover status: ready"),
			gomega.ContainSubstring("workflow_state: decomposing"),
			gomega.ContainSubstring("workflow_terminal_state: "),
			gomega.ContainSubstring("dispatchable: false"),
			gomega.ContainSubstring("packet: packet.json"),
			gomega.ContainSubstring("failure_id: failure-status-test"),
			gomega.ContainSubstring("dirty_status: clean"),
			gomega.ContainSubstring("prd: docs/prd.md"),
			gomega.ContainSubstring("next_valid_action: review_decomposition"),
			gomega.ContainSubstring("- residual-001: Recover status test"),
			gomega.ContainSubstring("evidence_refs: required_path:internal/workflows/recoverstatus/status.go; required_test:go test ./internal/workflows/recoverstatus"),
			gomega.ContainSubstring("- recovery-001: Recover status test"),
			gomega.ContainSubstring("required_tests: go test ./internal/workflows/recoverstatus"),
		))
		forbidden := []string{"recover promote", "recover reconcile", "recovery_decompose", "raw transcript"}
		gomega.Expect(text).NotTo(gomega.Or(gomega.ContainSubstring(forbidden[0]), gomega.ContainSubstring(forbidden[1]), gomega.ContainSubstring(forbidden[2]), gomega.ContainSubstring(forbidden[3])))
	})

	ginkgo.It("uses a valid sibling reconciliation to report only remaining work", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := recoverStatusTestPacket()
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

		_, _, err := recovery.WriteDecompositionPacket(root, packet, "nested/packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		reconciliation := buildPassingReconciliation(packet)
		writeReconciliation(filepath.Join(root, "nested", "reconciliation.json"), reconciliation)

		report, err := Build(root, "nested/packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(report.Status).To(gomega.Equal(recoverycontract.PacketStatusPartial))
		gomega.Expect(report.ReconciliationPath).To(gomega.Equal("nested/reconciliation.json"))
		gomega.Expect(report.WorkflowState).To(gomega.Equal(workflowstate.StateReconciling))
		gomega.Expect(report.WorkflowTerminalState).To(gomega.BeEmpty())
		gomega.Expect(report.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(report.CandidateAtoms).To(gomega.ConsistOf(gomega.Satisfy(func(atom recoverycontract.CandidateAtom) bool {
			return atom.ID == "recovery-002"
		})))
		gomega.Expect(report.ResidualWork).To(gomega.ConsistOf(gomega.Satisfy(func(work recoverycontract.ResidualWork) bool {
			return work.ID == "residual-002"
		})))
	})

	ginkgo.It("reports an invalid sibling reconciliation as blocked", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := recoverStatusTestPacket()
		_, _, err := recovery.WriteDecompositionPacket(root, packet, "nested/packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		reconciliation := buildPassingReconciliation(packet)
		reconciliation.PacketDigest = "sha256:stale"
		writeReconciliation(filepath.Join(root, "nested", "reconciliation.json"), reconciliation)

		report, err := Build(root, "nested/packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(report.Status).To(gomega.Equal(recoverycontract.PacketStatusBlocked))
		gomega.Expect(report.NextAction).To(gomega.Equal(recoverycontract.NextActionInspectFailure))
		gomega.Expect(report.WorkflowState).To(gomega.Equal(workflowstate.StateTerminal))
		gomega.Expect(report.WorkflowTerminalState).To(gomega.Equal(workflowstate.TerminalStateBlocked))
		gomega.Expect(report.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(report.ReconciliationPath).To(gomega.Equal("nested/reconciliation.json"))
		gomega.Expect(report.Reconciliation).NotTo(gomega.BeNil())
		gomega.Expect(report.Blockers).To(gomega.ContainElement(gomega.ContainSubstring("recovery_reconciliation_packet_digest_mismatch")))
	})

	ginkgo.It("rejects a packet outside the workspace root", func() {
		root := ginkgo.GinkgoT().TempDir()
		outsideRoot := ginkgo.GinkgoT().TempDir()
		_, _, err := recovery.WriteDecompositionPacket(outsideRoot, recoverStatusTestPacket(), "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		_, err = Build(root, filepath.Join(outsideRoot, "packet.json"))
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("workspace root")))
	})

	ginkgo.It("rejects a symlinked packet path", func() {
		root := ginkgo.GinkgoT().TempDir()
		outsideRoot := ginkgo.GinkgoT().TempDir()
		outsidePacket := filepath.Join(outsideRoot, "packet.json")
		_, _, err := recovery.WriteDecompositionPacket(outsideRoot, recoverStatusTestPacket(), "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		if err := os.Symlink(outsidePacket, filepath.Join(root, "packet.json")); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		_, err = Build(root, "packet.json")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlink")))
	})

	ginkgo.It("rejects a symlinked default reconciliation path", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := recoverStatusTestPacket()
		packet.FailureID = "failure-status-symlink-reconciliation"
		_, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		outsideRoot := ginkgo.GinkgoT().TempDir()
		outsidePath := filepath.Join(outsideRoot, "reconciliation.json")
		writeReconciliation(outsidePath, buildPassingReconciliation(packet))
		defaultPath := filepath.Join(root, ".idiom", "recovery", packet.FailureID, "reconciliation.json")
		gomega.Expect(os.MkdirAll(filepath.Dir(defaultPath), 0o755)).To(gomega.Succeed())
		if err := os.Symlink(outsidePath, defaultPath); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}

		_, err = Build(root, "packet.json")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlink")))
	})
})

func buildPassingReconciliation(packet recoverycontract.DecompositionPacket) recoverycontract.Reconciliation {
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
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return reconciliation
}

func writeReconciliation(path string, reconciliation recoverycontract.Reconciliation) {
	body, err := json.MarshalIndent(reconciliation, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(path, append(body, '\n'), 0o644)).To(gomega.Succeed())
}

func recoverStatusTestPacket() recoverycontract.DecompositionPacket {
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
