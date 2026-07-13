package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/platform/config"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("recovery tools", func() {
	ginkgo.It("recovery tools inspect decompose and status", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-001",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		inspect := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_inspect", SessionID: "sid-001"})
		gomega.Expect(inspect.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_inspect returned error: %v\n%s", inspect.Error, inspect.Output))

		var inspectReport recoveryInspectToolReport
		gomega.Expect(json.Unmarshal([]byte(inspect.Output), &inspectReport)).To(gomega.Succeed())
		gomega.Expect(inspectReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))
		gomega.Expect(inspectReport.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassExecutorEdit)), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))
		gomega.Expect(inspectReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionReviewDecomposition)), fmt.Sprintf("unexpected next action: %#v", inspectReport))

		decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-001"})
		gomega.Expect(decompose.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_decompose returned error: %v\n%s", decompose.Error, decompose.Output))

		var decomposeReport recoveryDecomposeToolReport
		gomega.Expect(json.Unmarshal([]byte(decompose.Output), &decomposeReport)).To(gomega.Succeed())
		gomega.Expect(decomposeReport.PacketPath).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.PacketSHA256).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))

		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(decomposeReport.PacketPath))); err != nil {
			ginkgo.Fail(fmt.Sprintf("expected packet to be written: %v", err))
		}

		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-001"})
		gomega.Expect(status.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_status returned error: %v\n%s", status.Error, status.Output))

		var statusReport recoveryStatusToolReport
		gomega.Expect(json.Unmarshal([]byte(status.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Status).To(gomega.Equal("ready"), fmt.Sprintf("unexpected status report: %#v", statusReport))
		gomega.Expect(statusReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionReviewDecomposition)), fmt.Sprintf("unexpected status report: %#v", statusReport))
		gomega.Expect(statusReport.Packet).NotTo(gomega.BeNil(), fmt.Sprintf("status report missing residual work: %#v", statusReport.Packet))
		gomega.Expect(statusReport.Packet.ResidualWork).To(gomega.HaveLen(1), fmt.Sprintf("status report missing residual work: %#v", statusReport.Packet))

		for _, ref := range statusReport.Packet.ResidualWork[0].EvidenceRefs {
			gomega.Expect(ref.Summary).To(gomega.Equal(""), fmt.Sprintf("status packet residual refs must be metadata-only: %#v", statusReport.Packet.ResidualWork[0].EvidenceRefs))

		}

		for _, gate := range []string{recoverycontract.GateWorkspaceState, recoverycontract.GateBriefLint, recoverycontract.GateAtomIntake, recoverycontract.GateScope, recoverycontract.GateRequiredTests} {
			gomega.Expect(stringSliceContains(statusReport.Gates, gate)).To(gomega.BeTrue(), fmt.Sprintf("status report missing gate %q: %#v", gate, statusReport.Gates))

		}
	})
	ginkgo.It("recovery tools inspect decompose and status implement record", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WriteFailureRecord(root, recovery.FailureRecordInput{
			FailureID: recovery.ImplementFailureID("sid-implement"),
			SourceRun: recoverycontract.SourceRun{
				Lane:              string(recoverycontract.SourceLaneImplement),
				SessionID:         "sid-implement",
				RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
				AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
			},
			FailureClass:   recoverycontract.FailureClassExecutorEdit,
			FailureKind:    "implement_process_failed",
			FailureSummary: "implement process failed: exit_code=1",
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write implement failure record: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		inspect := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_inspect", SessionID: "sid-implement"})
		gomega.Expect(inspect.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_inspect returned error: %v\n%s", inspect.Error, inspect.Output))

		var inspectReport recoveryInspectToolReport
		gomega.Expect(json.Unmarshal([]byte(inspect.Output), &inspectReport)).To(gomega.Succeed())
		gomega.Expect(inspectReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))
		gomega.Expect(inspectReport.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassExecutorEdit)), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))

		decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-implement"})
		gomega.Expect(decompose.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_decompose returned error: %v\n%s", decompose.Error, decompose.Output))

		var decomposeReport recoveryDecomposeToolReport
		gomega.Expect(json.Unmarshal([]byte(decompose.Output), &decomposeReport)).To(gomega.Succeed())
		gomega.Expect(decomposeReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))

		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-implement"})
		gomega.Expect(status.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_status returned error: %v\n%s", status.Error, status.Output))

		var statusReport recoveryStatusToolReport
		gomega.Expect(json.Unmarshal([]byte(status.Output), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected status report: %#v", statusReport))
		gomega.Expect(statusReport.Status).To(gomega.Equal("ready"), fmt.Sprintf("unexpected status report: %#v", statusReport))

	})
	ginkgo.It("recovery decompose tool rejects symlinked output path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-symlink-output",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC()); err != nil {
			ginkgo.Fail(fmt.Sprintf("write failure record: %v", err))
		}

		outsideDir := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(root, ".idiom", "recovery", "link-out")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		decompose := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "recovery_decompose",
			SessionID:  "sid-symlink-output",
			OutputPath: ".idiom/recovery/link-out/packet.json",
		})
		gomega.Expect(decompose.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected symlinked output rejection, got error=%q output=%s", decompose.Error, decompose.Output))
		gomega.Expect(decompose.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("expected symlinked output rejection, got error=%q output=%s", decompose.Error, decompose.Output))

		if _, err := os.Stat(filepath.Join(outsideDir, "packet.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink target output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recovery decompose tool rejects symlinked default output path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-default-symlink-output",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		recoveryDir := filepath.Join(root, ".idiom", "recovery")
		outsideRecoveryDir := filepath.Join(ginkgo.GinkgoT().TempDir(), "recovery")
		gomega.Expect(os.Rename(recoveryDir, outsideRecoveryDir)).To(gomega.Succeed())

		if err := os.Symlink(outsideRecoveryDir, recoveryDir); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-default-symlink-output"})
		gomega.Expect(decompose.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected symlinked default output rejection, got error=%q output=%s", decompose.Error, decompose.Output))
		gomega.Expect(decompose.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("expected symlinked default output rejection, got error=%q output=%s", decompose.Error, decompose.Output))

		if _, err := os.Stat(filepath.Join(outsideRecoveryDir, record.FailureID, "packet.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink default target output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recovery decompose tool rejects control artifact output path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-control-artifact-output",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		decompose := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "recovery_decompose",
			SessionID:  "sid-control-artifact-output",
			OutputPath: filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "failure.json")),
		})
		gomega.Expect(decompose.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected control artifact rejection, got error=%q output=%s", decompose.Error, decompose.Output))
		gomega.Expect(decompose.Error).To(gomega.ContainSubstring("control artifact"), fmt.Sprintf("expected control artifact rejection, got error=%q output=%s", decompose.Error, decompose.Output))

		if _, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID("sid-control-artifact-output")); err != nil || !found {
			ginkgo.Fail(fmt.Sprintf("failure record should remain readable, found=%v err=%v", found, err))
		}
	})
	ginkgo.It("recovery decompose tool rejects preexisting temp symlink", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-temp-symlink-output",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		packetPath := filepath.Join(root, ".idiom", "recovery", record.FailureID, "packet.json")
		controlPath := filepath.Join(root, ".idiom", "recovery", record.FailureID, "failure.json")

		before, err := os.ReadFile(controlPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read failure record before: %v", err))

		if err := os.Symlink(controlPath, packetPath+".tmp"); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-temp-symlink-output"})
		gomega.Expect(decompose.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected temp symlink rejection, got error=%q output=%s", decompose.Error, decompose.Output))
		gomega.Expect(decompose.Error).To(gomega.ContainSubstring("write recovery decomposition packet"), fmt.Sprintf("expected temp symlink rejection, got error=%q output=%s", decompose.Error, decompose.Output))

		after, err := os.ReadFile(controlPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read failure record after: %v", err))
		gomega.Expect(string(after)).To(gomega.Equal(string(before)), fmt.Sprintf("failure record should not be overwritten through temp symlink"))

	})
	ginkgo.It("recovery status tool reflects reconciliation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-recovered",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write packet: %v", err))

		reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     recoveryToolTestVerification(packet, "recovery-001"),
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildReconciliation: %v", err))

		if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteReconciliation: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-recovered"})
		gomega.Expect(status.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_status returned error: %v\n%s", status.Error, status.Output))

		var report recoveryStatusToolReport
		gomega.Expect(json.Unmarshal([]byte(status.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal("recovered"), fmt.Sprintf("expected recovered status, got %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionNone)), fmt.Sprintf("expected recovered status, got %#v", report))
		gomega.Expect(report.Reconciliation).NotTo(gomega.BeNil(), fmt.Sprintf("expected recovered status, got %#v", report))
		gomega.Expect(report.CandidateAtoms).To(gomega.BeEmpty(), fmt.Sprintf("expected recovered status, got %#v", report))
		if report.Packet != nil {
			gomega.Expect(report.Packet.ResidualWork).To(gomega.BeEmpty(), fmt.Sprintf("recovered tool status should hide completed residual work: %#v", report.Packet.ResidualWork))
		}

	})
	ginkgo.It("recovery status tool scrubs persisted residual evidence summaries", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-summary-scrub",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write packet: %v", err))

		packet.ResidualWork[0].EvidenceRefs = append(packet.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{
			Kind:    "legacy_failure",
			Path:    "internal/surfaces/tui/app/foo.go",
			Summary: "raw_prompt: persisted legacy detail",
		})
		if _, _, err := recovery.WriteDecompositionPacket(root, packet, ""); err != nil {
			ginkgo.Fail(fmt.Sprintf("rewrite packet: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-summary-scrub"})
		gomega.Expect(status.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_status returned error: %v\n%s", status.Error, status.Output))
		gomega.Expect(status.Output).NotTo(gomega.ContainSubstring("raw_prompt"), fmt.Sprintf("status output leaked residual summary: %s", status.Output))
		gomega.Expect(status.Output).NotTo(gomega.ContainSubstring("persisted legacy detail"), fmt.Sprintf("status output leaked residual summary: %s", status.Output))

		var report recoveryStatusToolReport
		gomega.Expect(json.Unmarshal([]byte(status.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Packet).NotTo(gomega.BeNil(), fmt.Sprintf("status report missing residual work: %#v", report.Packet))
		gomega.Expect(report.Packet.ResidualWork).To(gomega.HaveLen(1), fmt.Sprintf("status report missing residual work: %#v", report.Packet))
		gomega.Expect(recoveryToolEvidenceRefsContain(report.Packet.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{Kind: "legacy_failure",
			Path: "internal/surfaces/tui/app/foo.go",
		},
		)).To(gomega.BeTrue(), fmt.Sprintf("expected sanitized legacy evidence ref: %#v", report.Packet.ResidualWork[0].EvidenceRefs))

	})
	ginkgo.It("recovery status tool blocks invalid reconciliation", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-invalid-reconciliation",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write packet: %v", err))

		verification := recoveryToolTestVerification(packet, "recovery-001")

		verification.Commands[0].ExitCode = 1

		reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification:     verification,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildReconciliation: %v", err))

		if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteReconciliation: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-invalid-reconciliation"})
		gomega.Expect(status.Error).To(gomega.Equal(""), fmt.Sprintf("recovery_status returned error: %v\n%s", status.Error, status.Output))

		var report recoveryStatusToolReport
		gomega.Expect(json.Unmarshal([]byte(status.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("expected invalid reconciliation blocker, got %#v", report))
		gomega.Expect(stringSliceContainsPrefix(report.Blockers, "recovery_reconciliation_missing_required_tests")).To(gomega.BeTrue(), fmt.Sprintf("expected invalid reconciliation blocker, got %#v", report))

	})
	ginkgo.It("recovery status tool rejects symlinked packet path", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryWorkspaceFile(root, "internal/surfaces/tui/app/foo.go", "package app\n")

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
			SessionID:         "sid-symlink-packet",
			Phase:             "executor",
			Status:            "failed",
			FailureKind:       "executor_failed",
			FailureReason:     "exact edit did not apply",
			RequiredEditPaths: []string{"internal/surfaces/tui/app/foo.go"},
			AllowedEditPaths:  []string{"internal/surfaces/tui/app/foo.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write failure record: %v", err))

		_, packetPath, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write packet: %v", err))

		linkPath := filepath.Join(root, ".idiom", "recovery", "packet-link.json")
		if err := os.Symlink(filepath.Join(root, filepath.FromSlash(packetPath)), linkPath); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlink unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", PacketPath: ".idiom/recovery/packet-link.json"})
		gomega.Expect(status.Error).NotTo(gomega.Equal(""), fmt.Sprintf("recovery_status should reject symlinked packet path, got output: %s", status.Output))
		gomega.Expect(status.Error).To(gomega.ContainSubstring("symlink"), fmt.Sprintf("expected symlink error, got %q", status.Error))

	})
	ginkgo.It("recovery tool profile parity", func() {
		for _, tool := range []string{"recovery_inspect", "recovery_status"} {
			if decision := ToolAllowedForProfile(tool, ToolProfileDiscovery); !decision.Allowed {
				ginkgo.Fail(fmt.Sprintf("%s should be allowed in discovery profile: %#v", tool, decision))
			}

			if decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery); !decision.Allowed {
				ginkgo.Fail(fmt.Sprintf("%s should be allowed in mcp_discovery profile: %#v", tool, decision))
			}
		}

		if decision := ToolAllowedForProfile("recovery_decompose", ToolProfileDiscovery); decision.Allowed || !strings.Contains(decision.Reason, "mutation") {
			ginkgo.Fail(fmt.Sprintf("recovery_decompose should be denied by discovery profile as a writer, got %#v", decision))
		}
	})
})

func recoveryToolTestVerification(packet recoverycontract.DecompositionPacket, atomIDs ...string) recoverycontract.ReconciliationVerification {
	completed := map[string]bool{}
	for _, id := range atomIDs {
		completed[id] = true
	}

	seen := map[string]bool{}

	var (
		required []string
		commands []recoverycontract.ReconciliationCommandResult
	)

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
			commands = append(commands, recoverycontract.ReconciliationCommandResult{
				Command:  test,
				Status:   recoverycontract.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}

	return recoverycontract.ReconciliationVerification{
		Status:        recoverycontract.ReconciliationVerificationPass,
		RequiredTests: required,
		Commands:      commands,
	}
}

func writeRecoveryWorkspaceFile(root string, rel string, body string) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func recoveryToolEvidenceRefsContain(refs []recoverycontract.EvidenceRef, want recoverycontract.EvidenceRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}

	return false
}

func stringSliceContainsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}

	return false
}
