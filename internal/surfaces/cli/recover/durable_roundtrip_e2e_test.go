package recover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	recoverycontract "idiom/internal/contracts/recovery"
	workflowstatecontract "idiom/internal/contracts/workflowstate"
	"idiom/internal/platform/config"
	"idiom/internal/platform/gitstate"
	"idiom/internal/runtime/runner"
	planexecutecli "idiom/internal/surfaces/cli/planexecute"
	cmdruntime "idiom/internal/surfaces/cli/runtime"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recoverstatus"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("durable plan-execute recovery", ginkgo.Label("e2e", "integration", "smoke"), func() {
	ginkgo.It("carries a real planner failure through repair and verified reconciliation", func() {
		const (
			sessionID           = "recovery-roundtrip-smoke"
			requiredPath        = "internal/target/target.go"
			verificationCommand = "go test ./internal/target"
			plannerFailure      = "planner transport unavailable during recovery smoke"
		)

		root, configPath := writeRecoveryRoundTripWorkspace()

		var (
			plannerCalls   int
			plannerPrompt  string
			plannerOptions cmdruntime.RunOptions
			planOut        bytes.Buffer
			planErr        bytes.Buffer
		)
		code := planexecutecli.RunMainWithTelemetry(
			[]string{
				"--config", configPath,
				"--session", sessionID,
				"--required-edit-path", requiredPath,
				"--prompt", "Change TargetValue to return 2 and satisfy the existing target package test.",
			},
			&planOut,
			&planErr,
			func(_ config.Config, prompt string, opts cmdruntime.RunOptions, _ io.Writer) (string, error) {
				plannerCalls++
				plannerPrompt = prompt
				plannerOptions = opts

				return "", errors.New(plannerFailure)
			},
			nil,
		)

		gomega.Expect(code).To(gomega.Equal(1), "plan-execute stdout=%s stderr=%s", planOut.String(), planErr.String())
		gomega.Expect(plannerCalls).To(gomega.Equal(1))
		gomega.Expect(plannerPrompt).To(gomega.ContainSubstring(requiredPath))
		gomega.Expect(plannerOptions.TaskFamily).To(gomega.Equal(string(autonomy.TaskFamilyPlanThenExecutePlanner)))
		gomega.Expect(plannerOptions.PlannerOwnedVerification).To(gomega.BeTrue())
		gomega.Expect(plannerOptions.ScopeContract.RequiredEditPaths()).To(gomega.Equal([]string{requiredPath}))

		state, statePath, found, err := planexecute.TryLoadProgressState(root, sessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(state.SessionID).To(gomega.Equal(sessionID))
		gomega.Expect(state.Phase).To(gomega.Equal(plancontract.PlanExecuteProgressPhasePlanning))
		gomega.Expect(state.Status).To(gomega.Equal(plancontract.PlanExecuteProgressStatusFailed))
		gomega.Expect(state.FailureKind).To(gomega.Equal(plancontract.PlanExecuteFailurePlannerFailed))
		gomega.Expect(state.FailureReason).To(gomega.ContainSubstring(plannerFailure))
		gomega.Expect(state.RequiredEditPaths).To(gomega.Equal([]string{requiredPath}))
		gomega.Expect(state.ChangedPaths).To(gomega.BeEmpty())

		record, recordPath, found, err := recovery.TryLoadFailureRecordForSession(root, sessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(record.FailureID).To(gomega.Equal(recovery.PlanExecuteFailureID(sessionID)))
		gomega.Expect(record.SourceRun.Lane).To(gomega.Equal(string(recoverycontract.SourceLanePlanExecute)))
		gomega.Expect(record.SourceRun.SessionID).To(gomega.Equal(sessionID))
		gomega.Expect(record.SourceRun.ProgressStatePath).To(gomega.Equal(statePath))
		gomega.Expect(record.SourceRun.RequiredEditPaths).To(gomega.Equal([]string{requiredPath}))
		gomega.Expect(record.SourceRun.AllowedEditPaths).To(gomega.BeEmpty())
		gomega.Expect(record.Failure.Class).To(gomega.Equal(recoverycontract.FailureClassPlannerFailed))
		gomega.Expect(record.Failure.Kind).To(gomega.Equal(string(plancontract.PlanExecuteFailurePlannerFailed)))
		gomega.Expect(record.Failure.Summary).To(gomega.ContainSubstring(plannerFailure))
		gomega.Expect(record.LockedContract).NotTo(gomega.BeNil())
		gomega.Expect(record.LockedContract.RequiredEditPaths).To(gomega.Equal([]string{requiredPath}))
		gomega.Expect(record.LockedContract.AllowedEditPaths).To(gomega.BeEmpty())

		terminal, terminalPath, found, err := planexecute.TryLoadPlanExecuteTerminalCompletionRecord(root, sessionID)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(terminal.SessionID).To(gomega.Equal(sessionID))
		gomega.Expect(terminal.Status).To(gomega.Equal(plancontract.PlanExecuteTerminalStatusFailed))
		gomega.Expect(terminal.TerminalClass).To(gomega.Equal(plancontract.PlanExecuteTerminalClassFailed))
		gomega.Expect(terminal.Outcome).To(gomega.Equal(plancontract.PlanExecuteTerminalOutcomeFailed))
		gomega.Expect(terminal.WorkflowID).To(gomega.Equal(workflowstatecontract.WorkflowPlanExecutePlanner))
		gomega.Expect(terminal.WorkflowTerminalState).To(gomega.Equal(workflowstatecontract.TerminalStateBlocked))
		gomega.Expect(terminal.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(terminal.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(terminal.RecordPath).To(gomega.Equal(recordPath))
		gomega.Expect(terminal.PacketPath).NotTo(gomega.BeEmpty())
		gomega.Expect(terminal.ProgressPath).To(gomega.Equal(statePath))
		gomega.Expect(terminal.RequiredPathCoverage).To(gomega.Equal([]plancontract.PlanExecuteRequiredPathCoverage{{
			Path:   requiredPath,
			Status: plancontract.PlanExecuteRequiredPathCoverageMissing,
		}}))

		packetPath := terminal.PacketPath
		initialPacket, loadedPacketPath, found, err := recovery.TryLoadSafeDecompositionPacket(root, filepath.Join(root, filepath.FromSlash(packetPath)))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(loadedPacketPath).To(gomega.Equal(packetPath))
		gomega.Expect(initialPacket.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(initialPacket.PacketID).NotTo(gomega.BeEmpty())
		gomega.Expect(initialPacket.SourceRun.ProgressStatePath).To(gomega.Equal(statePath))

		var inspectOut, inspectErr bytes.Buffer
		code = RunMainWithTelemetry(
			[]string{"inspect", "--config", configPath, "--session", sessionID, "--json"},
			&inspectOut,
			&inspectErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recover inspect stdout=%s stderr=%s", inspectOut.String(), inspectErr.String())

		var inspectReport recoverInspectReport
		gomega.Expect(json.Unmarshal(inspectOut.Bytes(), &inspectReport)).To(gomega.Succeed())
		gomega.Expect(inspectReport.Status).To(gomega.Equal(string(recoverycontract.ReportStatusFailed)))
		gomega.Expect(inspectReport.Recorded).To(gomega.BeTrue())
		gomega.Expect(inspectReport.SessionID).To(gomega.Equal(sessionID))
		gomega.Expect(inspectReport.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(inspectReport.RecordPath).To(gomega.Equal(recordPath))
		gomega.Expect(inspectReport.ProgressStatePath).To(gomega.Equal(statePath))
		gomega.Expect(inspectReport.TerminalCompletionPath).To(gomega.Equal(terminalPath))
		gomega.Expect(inspectReport.PartialState.DirtyStatus).To(gomega.Equal(recoverycontract.DirtyStatusClean))
		gomega.Expect(inspectReport.PartialState.ScopeStatus).To(gomega.Equal(recoverycontract.ScopeStatusNone))
		gomega.Expect(inspectReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionReviewDecomposition)))
		gomega.Expect(inspectReport.RequiredPathCoverage).To(gomega.Equal(terminal.RequiredPathCoverage))

		var decomposeOut, decomposeErr bytes.Buffer
		code = RunMainWithTelemetry(
			[]string{"decompose", "--config", configPath, "--session", sessionID, "--output", packetPath, "--json"},
			&decomposeOut,
			&decomposeErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recover decompose stdout=%s stderr=%s", decomposeOut.String(), decomposeErr.String())

		var decomposeReport recoverDecomposeReport
		gomega.Expect(json.Unmarshal(decomposeOut.Bytes(), &decomposeReport)).To(gomega.Succeed())
		gomega.Expect(decomposeReport.Status).To(gomega.Equal(string(recoverycontract.PacketStatusReady)))
		gomega.Expect(decomposeReport.SessionID).To(gomega.Equal(sessionID))
		gomega.Expect(decomposeReport.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(decomposeReport.PacketID).To(gomega.Equal(initialPacket.PacketID))
		gomega.Expect(decomposeReport.PacketPath).To(gomega.Equal(packetPath))
		gomega.Expect(decomposeReport.Packet).NotTo(gomega.BeNil())
		gomega.Expect(decomposeReport.Packet.PartialState.DirtyStatus).To(gomega.Equal(recoverycontract.DirtyStatusClean))
		gomega.Expect(decomposeReport.Packet.PartialState.ScopeStatus).To(gomega.Equal(recoverycontract.ScopeStatusNone))
		gomega.Expect(decomposeReport.CandidateAtoms).To(gomega.HaveLen(1))

		atom := decomposeReport.CandidateAtoms[0]
		expectedGates := []string{
			recoverycontract.GateWorkspaceState,
			recoverycontract.GateBriefLint,
			recoverycontract.GateAtomIntake,
			recoverycontract.GateScope,
			recoverycontract.GateRequiredTests,
		}
		gomega.Expect(atom.ID).NotTo(gomega.BeEmpty())
		gomega.Expect(atom.RequiredPaths).To(gomega.Equal([]string{requiredPath}))
		gomega.Expect(atom.ForbiddenPaths).To(gomega.BeEmpty())
		gomega.Expect(atom.RequiredTests).To(gomega.Equal([]string{verificationCommand}))
		gomega.Expect(atom.Gates).To(gomega.ConsistOf(expectedGates))
		gomega.Expect(atom.ResidualWorkIDs).To(gomega.HaveLen(1))

		var statusOut, statusErr bytes.Buffer
		code = RunMainWithTelemetry(
			[]string{"status", "--config", configPath, "--packet", packetPath, "--json"},
			&statusOut,
			&statusErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recover status stdout=%s stderr=%s", statusOut.String(), statusErr.String())

		var statusReport recoverstatus.Report
		gomega.Expect(json.Unmarshal(statusOut.Bytes(), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Status).To(gomega.Equal(recoverycontract.PacketStatusReady))
		gomega.Expect(statusReport.WorkflowID).To(gomega.Equal(workflowstatecontract.WorkflowRecovery))
		gomega.Expect(statusReport.WorkflowState).To(gomega.Equal(workflowstatecontract.StateDecomposing))
		gomega.Expect(statusReport.WorkflowTerminalState).To(gomega.BeEmpty())
		gomega.Expect(statusReport.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(statusReport.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(statusReport.PacketID).To(gomega.Equal(initialPacket.PacketID))
		gomega.Expect(statusReport.SourceSessionID).To(gomega.Equal(sessionID))
		gomega.Expect(statusReport.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition))
		gomega.Expect(statusReport.Gates).To(gomega.ConsistOf(expectedGates))
		gomega.Expect(statusReport.RequiredTests).To(gomega.Equal([]string{verificationCommand}))
		gomega.Expect(statusReport.CandidateAtoms).To(gomega.Equal([]recoverycontract.CandidateAtom{atom}))
		gomega.Expect(statusReport.ResidualWork).To(gomega.HaveLen(1))
		gomega.Expect(statusReport.ResidualWork[0].ID).To(gomega.Equal(atom.ResidualWorkIDs[0]))
		gomega.Expect(statusReport.ResidualWork[0].Paths).To(gomega.Equal([]string{requiredPath}))

		promotionPath := filepath.ToSlash(filepath.Join("briefs", atom.ID+".md"))
		var promoteOut, promoteErr bytes.Buffer
		code = RunMainWithTelemetry(
			[]string{"promote", "--config", configPath, "--packet", packetPath, "--atom", atom.ID, "--output", promotionPath, "--json"},
			&promoteOut,
			&promoteErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recover promote stdout=%s stderr=%s", promoteOut.String(), promoteErr.String())

		var promoteReport recoverPromoteReport
		gomega.Expect(json.Unmarshal(promoteOut.Bytes(), &promoteReport)).To(gomega.Succeed())
		gomega.Expect(promoteReport.Status).To(gomega.Equal(string(recoverycontract.ReportStatusPromoted)))
		gomega.Expect(promoteReport.PacketPath).To(gomega.Equal(packetPath))
		gomega.Expect(promoteReport.AtomID).To(gomega.Equal(atom.ID))
		gomega.Expect(promoteReport.OutputPath).To(gomega.Equal(promotionPath))
		gomega.Expect(promoteReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionNone)))
		gomega.Expect(promoteReport.PromotionNextAction).To(gomega.Equal(string(recoverycontract.PromotionNextActionDispatchAtom)))
		promotionGateNames := make([]string, 0, len(promoteReport.Gates))
		for _, gate := range promoteReport.Gates {
			promotionGateNames = append(promotionGateNames, gate.Name)
			gomega.Expect(gate.Status).To(gomega.Equal(recoverycontract.GateStatusPass), "promotion gate %#v", gate)
		}
		gomega.Expect(promotionGateNames).To(gomega.ConsistOf(expectedGates))

		briefBody, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(promotionPath)))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(briefBody)).To(gomega.ContainSubstring("Recovery Packet: `" + initialPacket.PacketID + "`"))
		gomega.Expect(string(briefBody)).To(gomega.ContainSubstring("Failure ID: `" + record.FailureID + "`"))
		gomega.Expect(string(briefBody)).To(gomega.ContainSubstring("`" + requiredPath + "`"))
		gomega.Expect(string(briefBody)).To(gomega.ContainSubstring("`" + verificationCommand + "` succeeds"))

		gomega.Expect(os.WriteFile(
			filepath.Join(root, filepath.FromSlash(requiredPath)),
			[]byte("package target\n\nfunc TargetValue() int { return 2 }\n"),
			0o644,
		)).To(gomega.Succeed())

		dirtyPaths, err := gitstate.DirtyPaths(context.Background(), root, gitstate.DirtyPathOptions{
			IgnorePrefixes: []string{".idiom/recovery/", ".idiom/plan-execute/"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(dirtyPaths).To(gomega.Equal([]string{requiredPath}))

		var reconcileOut, reconcileErr bytes.Buffer
		code = RunMainWithTelemetry(
			[]string{"reconcile", "--config", configPath, "--packet", packetPath, "--completed", atom.ID, "--json"},
			&reconcileOut,
			&reconcileErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recover reconcile stdout=%s stderr=%s", reconcileOut.String(), reconcileErr.String())

		var reconcileReport recoverReconcileReport
		gomega.Expect(json.Unmarshal(reconcileOut.Bytes(), &reconcileReport)).To(gomega.Succeed())
		gomega.Expect(reconcileReport.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusRecovered))
		gomega.Expect(reconcileReport.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(reconcileReport.PacketID).To(gomega.Equal(initialPacket.PacketID))
		gomega.Expect(reconcileReport.CompletedAtoms).To(gomega.Equal([]string{atom.ID}))
		gomega.Expect(reconcileReport.RemainingAtoms).To(gomega.BeEmpty())
		gomega.Expect(reconcileReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionNone)))
		gomega.Expect(reconcileReport.Reconciliation).NotTo(gomega.BeNil())
		gomega.Expect(reconcileReport.Reconciliation.PacketDigest).To(gomega.Equal(recovery.PacketDigest(*decomposeReport.Packet)))
		gomega.Expect(reconcileReport.Reconciliation.LockedContract).To(gomega.Equal(decomposeReport.Packet.LockedContract))
		gomega.Expect(reconcileReport.Reconciliation.Verification.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationPass))
		gomega.Expect(reconcileReport.Reconciliation.Verification.RequiredTests).To(gomega.Equal([]string{verificationCommand}))
		gomega.Expect(reconcileReport.Reconciliation.Verification.Commands).To(gomega.HaveLen(1))
		commandResult := reconcileReport.Reconciliation.Verification.Commands[0]
		gomega.Expect(commandResult.Command).To(gomega.Equal(verificationCommand))
		gomega.Expect(commandResult.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationPass))
		gomega.Expect(commandResult.ExitCode).To(gomega.Equal(0))
		gomega.Expect(commandResult.StartedAt).NotTo(gomega.BeEmpty())
		gomega.Expect(commandResult.CompletedAt).NotTo(gomega.BeEmpty())

		durableReconciliation, loadedReconciliationPath, found, err := recovery.TryLoadReconciliationForPacket(root, *decomposeReport.Packet)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(loadedReconciliationPath).To(gomega.Equal(reconcileReport.ReconciliationPath))
		gomega.Expect(durableReconciliation.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(durableReconciliation.PacketID).To(gomega.Equal(initialPacket.PacketID))
		gomega.Expect(durableReconciliation.PacketDigest).To(gomega.Equal(recovery.PacketDigest(*decomposeReport.Packet)))
		gomega.Expect(durableReconciliation.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusRecovered))
		gomega.Expect(durableReconciliation.NextAction).To(gomega.Equal(recoverycontract.NextActionNone))
		gomega.Expect(durableReconciliation.Verification).To(gomega.Equal(reconcileReport.Reconciliation.Verification))

		statusOut.Reset()
		statusErr.Reset()
		code = RunMainWithTelemetry(
			[]string{"status", "--config", configPath, "--packet", packetPath, "--json"},
			&statusOut,
			&statusErr,
			nil,
		)
		gomega.Expect(code).To(gomega.Equal(0), "recovered status stdout=%s stderr=%s", statusOut.String(), statusErr.String())

		statusReport = recoverstatus.Report{}
		gomega.Expect(json.Unmarshal(statusOut.Bytes(), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Status).To(gomega.Equal(recoverycontract.PacketStatusRecovered))
		gomega.Expect(statusReport.WorkflowID).To(gomega.Equal(workflowstatecontract.WorkflowRecovery))
		gomega.Expect(statusReport.WorkflowState).To(gomega.Equal(workflowstatecontract.StateTerminal))
		gomega.Expect(statusReport.WorkflowTerminalState).To(gomega.Equal(workflowstatecontract.TerminalStateComplete))
		gomega.Expect(statusReport.Dispatchable).To(gomega.BeFalse())
		gomega.Expect(statusReport.NextAction).To(gomega.Equal(recoverycontract.NextActionNone))
		gomega.Expect(statusReport.FailureID).To(gomega.Equal(record.FailureID))
		gomega.Expect(statusReport.PacketID).To(gomega.Equal(initialPacket.PacketID))
		gomega.Expect(statusReport.ReconciliationPath).To(gomega.Equal(reconcileReport.ReconciliationPath))
		gomega.Expect(statusReport.CompletedAtomCount).To(gomega.Equal(1))
		gomega.Expect(statusReport.RemainingAtomCount).To(gomega.Equal(0))
		gomega.Expect(statusReport.CandidateAtoms).To(gomega.BeEmpty())
		gomega.Expect(statusReport.ResidualWork).To(gomega.BeEmpty())
		gomega.Expect(statusReport.Blockers).To(gomega.BeEmpty())
		gomega.Expect(statusReport.Reconciliation).NotTo(gomega.BeNil())
		gomega.Expect(statusReport.Reconciliation.Verification.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationPass))
	})
})

func writeRecoveryRoundTripWorkspace() (string, string) {
	root := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().Setenv("IDIOM_QUEUE_POOL", "")
	ginkgo.GinkgoT().Setenv("IDIOM_QUEUE_SOCKET", "")
	ginkgo.GinkgoT().Setenv(runner.HeldPoolsEnv, "")

	targetDir := filepath.Join(root, "internal", "target")
	gomega.Expect(os.MkdirAll(targetDir, 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module recovery.roundtrip.test\n\ngo 1.25\n"),
		0o644,
	)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(targetDir, "target.go"),
		[]byte("package target\n\nfunc TargetValue() int { return 1 }\n"),
		0o644,
	)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(
		filepath.Join(targetDir, "target_test.go"),
		[]byte("package target\n\nimport \"testing\"\n\nfunc TestTargetValue(t *testing.T) {\n\tif got := TargetValue(); got != 2 {\n\t\tt.Fatalf(\"TargetValue() = %d, want 2\", got)\n\t}\n}\n"),
		0o644,
	)).To(gomega.Succeed())

	configPath := filepath.Join(root, "config.json")
	configBody := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(configPath, []byte(configBody), 0o644)).To(gomega.Succeed())

	initializeRecoveryRoundTripGitBaseline(root)

	return root, configPath
}

func initializeRecoveryRoundTripGitBaseline(root string) {
	isolatedGitEnvironment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") {
			continue
		}
		isolatedGitEnvironment = append(isolatedGitEnvironment, entry)
	}
	isolatedGitEnvironment = append(
		isolatedGitEnvironment,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)

	initCmd := exec.Command("git", "-C", root, "init", "--quiet")
	initCmd.Env = isolatedGitEnvironment
	output, err := initCmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git init: %s", strings.TrimSpace(string(output)))

	gomega.Expect(os.WriteFile(
		filepath.Join(root, ".git", "info", "exclude"),
		[]byte(".idiom/\nbriefs/\n"),
		0o644,
	)).To(gomega.Succeed())

	addCmd := exec.Command("git", "-C", root, "add", "--", "config.json", "go.mod", "internal/target/target.go", "internal/target/target_test.go")
	addCmd.Env = isolatedGitEnvironment
	output, err = addCmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git add: %s", strings.TrimSpace(string(output)))

	commitCmd := exec.Command(
		"git", "-C", root,
		"-c", "user.name=Recovery Smoke",
		"-c", "user.email=recovery-smoke@example.invalid",
		"commit", "--quiet", "-m", "recovery smoke baseline",
	)
	commitCmd.Env = isolatedGitEnvironment
	output, err = commitCmd.CombinedOutput()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "git commit: %s", strings.TrimSpace(string(output)))
}
