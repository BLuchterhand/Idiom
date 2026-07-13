package recover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	recoverycontract "idiom/internal/contracts/recovery"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/repository/comprehension"
	"idiom/internal/workflows/planexecute"
	"idiom/internal/workflows/recoverstatus"
	"idiom/internal/workflows/recovery"
)

var _ = ginkgo.Describe("recovercmd", ginkgo.Label("integration"), func() {
	ginkgo.It("run main recover inspect reads failure record", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		state := planexecute.NewProgressState("recover-session-1", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = plancontract.PlanExecuteFailurePlannerBlocked
		state.FailureReason = "<<QUESTION>>: answer needed before editing"

		statePath, err := planexecute.SaveProgressState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

		record, recordPath, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write recovery record: %v", err))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal("failed"), fmt.Sprintf("unexpected report status/recorded: %#v", report))
		gomega.Expect(report.Recorded).To(gomega.BeTrue(), fmt.Sprintf("unexpected report status/recorded: %#v", report))
		gomega.Expect(report.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("record identity mismatch: %#v want id=%s path=%s", report, record.FailureID, recordPath))
		gomega.Expect(report.RecordPath).To(gomega.Equal(recordPath), fmt.Sprintf("record identity mismatch: %#v want id=%s path=%s", report, record.FailureID, recordPath))
		gomega.Expect(report.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassPlannerBlocked)), fmt.Sprintf("failure class=%q", report.FailureClass))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionAnswerBlocker)), fmt.Sprintf("next action=%q", report.NextAction))

	})
	ginkgo.It("run main recover show accepts failure id and prints terminal summary", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		state := planexecute.NewProgressState("recover-session-show", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = plancontract.PlanExecuteFailurePlannerBlocked
		state.FailureReason = "no launchable backend controller configured"

		statePath, err := planexecute.SaveProgressState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write recovery record: %v", err))

		terminalPath, err := planexecute.SavePlanExecuteTerminalCompletionRecord(root, plancontract.PlanExecuteTerminalCompletionRecord{
			SessionID:         state.SessionID,
			Status:            plancontract.PlanExecuteTerminalStatusFailed,
			FailureKind:       plancontract.PlanExecuteFailureKind(state.FailureKind),
			FailureReason:     state.FailureReason,
			FailureID:         record.FailureID,
			RecordPath:        ".idiom/recovery/" + record.FailureID + "/failure.json",
			PacketPath:        ".idiom/recovery/" + record.FailureID + "/packet.json",
			NextAction:        recoverycontract.NextActionAnswerBlocker,
			RequiredEditPaths: []string{"a.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save terminal completion: %v", err))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"show", "--config", configPath, record.FailureID}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover show exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		output := stdout.String()
		for _, want := range []string{
			"failure_reason: no launchable backend controller configured",
			"terminal_completion: " + terminalPath,
			"required_path_coverage:",
			"a.go: missing",
		} {
			gomega.Expect(output).To(gomega.ContainSubstring(want), fmt.Sprintf("recover show output missing %q:\n%s", want, output))

		}
	})
	ginkgo.It("run main recover show does not trust uncorrelated terminal completion", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		state := planexecute.NewProgressState("recover-session-show", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = plancontract.PlanExecuteFailurePlannerBlocked
		state.FailureReason = "durable failure record reason"

		statePath, err := planexecute.SaveProgressState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

		record, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write recovery record: %v", err))

		terminalPath, err := planexecute.SavePlanExecuteTerminalCompletionRecord(root, plancontract.PlanExecuteTerminalCompletionRecord{
			SessionID:         state.SessionID,
			Status:            plancontract.PlanExecuteTerminalStatusFailed,
			FailureKind:       plancontract.PlanExecuteFailureExecutorFailed,
			FailureReason:     "stale terminal reason",
			FailureID:         "different-failure-id",
			RecordPath:        ".idiom/recovery/different-failure-id/failure.json",
			NextAction:        recoverycontract.NextActionInspectFailure,
			RequiredEditPaths: []string{"a.go"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save terminal completion: %v", err))

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{"show", "--config", configPath, "--json", record.FailureID}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover show exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.TerminalCompletionPath).To(gomega.Equal(""), fmt.Sprintf("uncorrelated terminal path should not be trusted, got %q from %q", report.TerminalCompletionPath, terminalPath))
		gomega.Expect(report.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("uncorrelated terminal completion contaminated report: %#v", report))
		gomega.Expect(report.FailureReason).To(gomega.Equal("durable failure record reason"), fmt.Sprintf("uncorrelated terminal completion contaminated report: %#v", report))
		gomega.Expect(report.RequiredPathCoverage).To(gomega.BeEmpty(), fmt.Sprintf("uncorrelated terminal coverage must not be trusted: %#v", report.RequiredPathCoverage))

	})
	ginkgo.It("run recover inspect and decompose read implement failure record", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644)).To(gomega.Succeed())

		record, _, err := recovery.WriteFailureRecord(root, recovery.FailureRecordInput{
			FailureID: recovery.ImplementFailureID("implement-session-1"),
			SourceRun: recoverycontract.SourceRun{
				Lane:              string(recoverycontract.SourceLaneImplement),
				SessionID:         "implement-session-1",
				PromptFile:        "brief.md",
				RequiredEditPaths: []string{"allowed.go"},
			},
			FailureClass:   recoverycontract.FailureClassExecutorEdit,
			FailureKind:    "implement_process_failed",
			FailureSummary: "implement process failed: exit_code=1",
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write implement recovery record: %v", err))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"inspect", "--config", configPath, "--session", "implement-session-1", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var inspectReport recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &inspectReport)).To(gomega.Succeed())
		gomega.Expect(inspectReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))
		gomega.Expect(inspectReport.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassExecutorEdit)), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))
		gomega.Expect(inspectReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionReviewDecomposition)), fmt.Sprintf("unexpected inspect report: %#v", inspectReport))

		stdout.Reset()
		stderr.Reset()

		code = RunMainWithTelemetry([]string{"show", "--config", configPath, "--json", record.FailureID}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover show implement exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var showReport recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &showReport)).To(gomega.Succeed())
		gomega.Expect(showReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("show report loaded wrong implement failure: %#v", showReport))

		stdout.Reset()
		stderr.Reset()

		code = RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", "implement-session-1", "--output", "implement-packet.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover decompose exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var decomposeReport recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &decomposeReport)).To(gomega.Succeed())
		gomega.Expect(decomposeReport.FailureID).To(gomega.Equal(record.FailureID), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.Status).To(gomega.Equal("ready"), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))

	})
	ginkgo.It("run recover inspect computes legacy failed progress without writing", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		state := planexecute.NewProgressState("recover-session-2", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = "plan_artifact_missing"
		state.FailureReason = "no plan artifact"

		if _, err := planexecute.SaveProgressState(root, &state); err != nil {
			ginkgo.Fail(fmt.Sprintf("save progress: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Recorded).To(gomega.BeFalse(), fmt.Sprintf("legacy progress fallback must not write during inspect: %#v", report))
		gomega.Expect(report.FailureClass).To(gomega.Equal(string(recoverycontract.FailureClassPlanMissing)), fmt.Sprintf("failure class=%q", report.FailureClass))

		if _, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(state.SessionID)); err != nil || found {
			ginkgo.Fail(fmt.Sprintf("inspect should not persist fallback record, found=%v err=%v", found, err))
		}
	})
	ginkgo.DescribeTable("computes recovery-required progress without writing",
		func(status plancontract.PlanExecuteProgressStatus, failureKind plancontract.PlanExecuteFailureKind) {
			configPath := writePlanExecuteConfig()
			root := filepath.Dir(configPath)
			state := planexecute.NewProgressState("recover-"+string(status)+"-session", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())
			state.Phase = plancontract.PlanExecuteProgressPhaseReviewRepair
			state.Status = status
			state.FailureKind = failureKind
			state.FailureReason = "review repair " + string(status)
			_, err := planexecute.SaveProgressState(root, &state)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			var stdout, stderr bytes.Buffer
			code := RunMainWithTelemetry([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr, nil)
			gomega.Expect(code).To(gomega.Equal(0), "stdout=%s stderr=%s", stdout.String(), stderr.String())

			var report recoverInspectReport
			gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
			gomega.Expect(report.Recorded).To(gomega.BeFalse())
			gomega.Expect(report.NextAction).NotTo(gomega.Equal(string(recoverycontract.NextActionNone)))
			_, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(state.SessionID))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(found).To(gomega.BeFalse())
		},
		ginkgo.Entry("blocked", plancontract.PlanExecuteProgressStatusBlocked, plancontract.PlanExecuteFailureReviewRepairBlocked),
		ginkgo.Entry("degraded", plancontract.PlanExecuteProgressStatusDegraded, plancontract.PlanExecuteFailureReviewRepairDegraded),
	)
	ginkgo.It("run recover unknown subcommand exits two", func() {
		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"wat"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("unknown subcommand"), fmt.Sprintf("stderr=%s", stderr.String()))

	})
	ginkgo.It("recover inspect usage error can be json", func() {
		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"inspect", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal("error"), fmt.Sprintf("unexpected report: %#v", report))
		gomega.Expect(report.Error).To(gomega.ContainSubstring("--session is required"), fmt.Sprintf("unexpected report: %#v", report))
		gomega.Expect(stderr.Len()).To(gomega.Equal(0), fmt.Sprintf("json usage errors should not also write stderr, got %s", stderr.String()))

	})
	ginkgo.It("run recover inspect classifies out of scope dirty workspace", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		initRecoverTestGitRepo(root)
		gomega.Expect(os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(".idiom/\nconfig.json\n"), 0o644)).To(gomega.Succeed())

		state := planexecute.NewProgressState("recover-session-3", "make a change", "", []string{"allowed.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = "executor_failed"
		state.FailureReason = "edit failed"
		state.ChangedPaths = []string{"allowed.go"}
		state.TotalEdits = 1

		statePath, err := planexecute.SaveProgressState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

		if _, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC()); err != nil {
			ginkgo.Fail(fmt.Sprintf("write recovery record: %v", err))
		}
		gomega.Expect(os.WriteFile(filepath.Join(root, "unexpected.go"), []byte("package main\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverInspectReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.PartialState.DirtyStatus).To(gomega.Equal(recoverycontract.DirtyStatusDirtyOutScope), fmt.Sprintf("dirty status=%q report=%#v", report.PartialState.DirtyStatus, report))
		gomega.Expect(report.PartialState.ScopeStatus).To(gomega.Equal(recoverycontract.ScopeStatusOutOfScope), fmt.Sprintf("scope status=%q report=%#v", report.PartialState.ScopeStatus, report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionHumanIntervention)), fmt.Sprintf("next action=%q", report.NextAction))

	})
	ginkgo.It("recover decompose status and promote", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		writeRecoverTestGoModule(root)
		gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644)).To(gomega.Succeed())

		state := planexecute.NewProgressState("recover-session-4", "make a change", "", []string{"allowed.go"}, nil, time.Now().UTC())

		state.Status = plancontract.PlanExecuteProgressStatusFailed
		state.FailureKind = "executor_failed"
		state.FailureReason = "edit failed"

		statePath, err := planexecute.SaveProgressState(root, &state)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

		if _, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC()); err != nil {
			ginkgo.Fail(fmt.Sprintf("write recovery record: %v", err))
		}

		var decomposeOut, decomposeErr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", state.SessionID, "--output", "packet.json", "--json"}, &decomposeOut, &decomposeErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover decompose exit=%d stdout=%s stderr=%s", code, decomposeOut.String(), decomposeErr.String()))

		var decomposeReport recoverDecomposeReport
		gomega.Expect(json.Unmarshal(decomposeOut.Bytes(), &decomposeReport)).To(gomega.Succeed())
		gomega.Expect(decomposeReport.Status).To(gomega.Equal("ready"), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.PacketPath).To(gomega.Equal("packet.json"), fmt.Sprintf("unexpected decompose report: %#v", decomposeReport))
		gomega.Expect(decomposeReport.CandidateAtoms).To(gomega.HaveLen(1), fmt.Sprintf("unexpected candidate atoms: %#v", decomposeReport.CandidateAtoms))
		gomega.Expect(decomposeReport.CandidateAtoms[0].ID).To(gomega.Equal("recovery-001"), fmt.Sprintf("unexpected candidate atoms: %#v", decomposeReport.CandidateAtoms))

		var statusOut, statusErr bytes.Buffer

		code = RunMainWithTelemetry([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &statusOut, &statusErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover status exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String()))

		var statusReport recoverstatus.Report
		gomega.Expect(json.Unmarshal(statusOut.Bytes(), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Status).To(gomega.Equal(recoverycontract.PacketStatusReady), fmt.Sprintf("unexpected status report: %#v", statusReport))
		gomega.Expect(statusReport.NextAction).To(gomega.Equal(recoverycontract.NextActionReviewDecomposition), fmt.Sprintf("unexpected status report: %#v", statusReport))
		gomega.Expect(statusReport.ResidualWork).To(gomega.HaveLen(1), fmt.Sprintf("expected residual evidence refs in status report: %#v", statusReport.ResidualWork))
		gomega.Expect(recoveryEvidenceRefsContain(statusReport.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{Kind: "required_path", Path: "allowed.go"})).To(gomega.BeTrue(), fmt.Sprintf("expected residual evidence refs in status report: %#v", statusReport.ResidualWork))
		gomega.Expect(recoveryEvidenceRefsContain(statusReport.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{Kind: "required_test", ID: "go test ./..."})).To(gomega.BeTrue(), fmt.Sprintf("expected residual evidence refs in status report: %#v", statusReport.ResidualWork))

		statusOut.Reset()
		statusErr.Reset()

		code = RunMainWithTelemetry([]string{"status", "--config", configPath, "--packet", "packet.json"}, &statusOut, &statusErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover status text exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String()))
		gomega.Expect(statusOut.String()).To(gomega.ContainSubstring("evidence_refs: required_path:allowed.go"), fmt.Sprintf("expected residual evidence refs in status text, got %s", statusOut.String()))
		gomega.Expect(statusOut.String()).To(gomega.ContainSubstring("required_test:go test ./..."), fmt.Sprintf("expected residual evidence refs in status text, got %s", statusOut.String()))

		sharedStatusReport, err := recoverstatus.Build(root, "packet.json")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("shared recover status build: %v", err))
		gomega.Expect(statusOut.String()).To(gomega.Equal(recoverstatus.FormatText(sharedStatusReport)), fmt.Sprintf("CLI recover status text must match shared presenter.\nwant:\n%s\n\ngot:\n%s", recoverstatus.FormatText(sharedStatusReport), statusOut.String()))

		var promoteOut, promoteErr bytes.Buffer

		code = RunMainWithTelemetry([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", "briefs/recovery-001.md", "--json"}, &promoteOut, &promoteErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover promote exit=%d stdout=%s stderr=%s", code, promoteOut.String(), promoteErr.String()))

		var promoteReport recoverPromoteReport
		gomega.Expect(json.Unmarshal(promoteOut.Bytes(), &promoteReport)).To(gomega.Succeed())
		gomega.Expect(promoteReport.Status).To(gomega.Equal("promoted"), fmt.Sprintf("unexpected promote report: %#v", promoteReport))
		gomega.Expect(promoteReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionNone)), fmt.Sprintf("unexpected promote report: %#v", promoteReport))
		gomega.Expect(promoteReport.PromotionNextAction).To(gomega.Equal(string(recoverycontract.PromotionNextActionDispatchAtom)), fmt.Sprintf("unexpected promote report: %#v", promoteReport))

		if _, err := os.Stat(filepath.Join(root, "briefs", "recovery-001.md")); err != nil {
			ginkgo.Fail(fmt.Sprintf("promotion output missing: %v", err))
		}

		for _, gate := range promoteReport.Gates {
			gomega.Expect(gate.Status).To(gomega.Equal(recoverycontract.GateStatusPass), fmt.Sprintf("gate did not pass: %#v report=%#v", gate, promoteReport))

		}

		var reconcileOut, reconcileErr bytes.Buffer

		code = RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", "recovery-001", "--json"}, &reconcileOut, &reconcileErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover reconcile exit=%d stdout=%s stderr=%s", code, reconcileOut.String(), reconcileErr.String()))

		var reconcileReport recoverReconcileReport
		gomega.Expect(json.Unmarshal(reconcileOut.Bytes(), &reconcileReport)).To(gomega.Succeed())
		gomega.Expect(reconcileReport.Status).To(gomega.Equal(recoverycontract.ReconciliationStatusRecovered), fmt.Sprintf("unexpected reconcile report: %#v", reconcileReport))
		gomega.Expect(reconcileReport.NextAction).To(gomega.Equal(string(recoverycontract.NextActionNone)), fmt.Sprintf("unexpected reconcile report: %#v", reconcileReport))
		gomega.Expect(reconcileReport.RankerLearning).NotTo(gomega.BeNil(), fmt.Sprintf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning))
		gomega.Expect(reconcileReport.RankerLearning.State).To(gomega.Equal(comprehension.LearningStateAccepted), fmt.Sprintf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning))
		gomega.Expect(reconcileReport.RankerLearning.Source).To(gomega.Equal(comprehension.LearningSourceRecoveryReconciliation), fmt.Sprintf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning))
		gomega.Expect(reconcileReport.RankerLearning.Reason).To(gomega.Equal(comprehension.LearningReasonVerifiedRecovery), fmt.Sprintf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning))
		gomega.Expect(reconcileReport.RankerLearning.EvidenceKind).To(gomega.Equal(comprehension.LearningEvidenceKindRecoveryReconciliation), fmt.Sprintf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning))
		gomega.Expect(reconcileReport.RankerLearning.Metrics["packet_quality"]).To(gomega.Equal(float64(1)), fmt.Sprintf("expected recovery learning metrics, got %#v", reconcileReport.RankerLearning.Metrics))
		gomega.Expect(reconcileReport.RankerLearning.Metrics["completed_atom_count"]).To(gomega.Equal(float64(1)), fmt.Sprintf("expected recovery learning metrics, got %#v", reconcileReport.RankerLearning.Metrics))

		if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("recovery learning evidence must not write weights.json, err=%v", err))
		}

		learningEntries, err := comprehension.ReadLearningLog(ginkgo.GinkgoT().Context(), root)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read learning log: %v", err))

		learningSummary := comprehension.SummarizeLearningLog(learningEntries)
		gomega.Expect(learningSummary.BySource[comprehension.LearningSourceRecoveryReconciliation]).To(gomega.Equal(1), fmt.Sprintf("learning summary missing recovery reconciliation event: %#v", learningSummary))
		gomega.Expect(learningSummary.AcceptedReasonCounts[comprehension.LearningReasonVerifiedRecovery]).To(gomega.Equal(1), fmt.Sprintf("learning summary missing recovery reconciliation event: %#v", learningSummary))

		if _, err := os.Stat(filepath.Join(root, reconcileReport.ReconciliationPath)); err != nil {
			ginkgo.Fail(fmt.Sprintf("reconciliation output missing: %v", err))
		}
		gomega.Expect(reconcileReport.ReconciliationPath).To(gomega.Equal("reconciliation.json"), fmt.Sprintf("expected sibling reconciliation output, got %#v", reconcileReport))

		statusOut.Reset()
		statusErr.Reset()

		code = RunMainWithTelemetry([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &statusOut, &statusErr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover status after reconcile exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String()))

		statusReport = recoverstatus.Report{}
		gomega.Expect(json.Unmarshal(statusOut.Bytes(), &statusReport)).To(gomega.Succeed())
		gomega.Expect(statusReport.Status).To(gomega.Equal(recoverycontract.PacketStatusRecovered), fmt.Sprintf("expected recovered status after reconcile, got %#v", statusReport))
		gomega.Expect(statusReport.NextAction).To(gomega.Equal(recoverycontract.NextActionNone), fmt.Sprintf("expected recovered status after reconcile, got %#v", statusReport))
		gomega.Expect(statusReport.CandidateAtoms).To(gomega.HaveLen(0), fmt.Sprintf("expected recovered status after reconcile, got %#v", statusReport))
		gomega.Expect(statusReport.Reconciliation).NotTo(gomega.BeNil(), fmt.Sprintf("expected recovered status after reconcile, got %#v", statusReport))
		gomega.Expect(statusReport.ResidualWork).To(gomega.BeEmpty(), fmt.Sprintf("recovered status should not list remaining residual work: %#v", statusReport.ResidualWork))

	})
	ginkgo.It("recover status scrubs persisted residual evidence summaries", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		packet := writeRecoverTestPacket(root, "recover-session-summary-scrub", "packet.json")

		packet.ResidualWork[0].EvidenceRefs = append(packet.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{
			Kind:    "legacy_failure",
			Path:    "allowed.go",
			Summary: "raw_prompt: persisted legacy detail",
		})

		if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("rewrite packet: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("recover status exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverstatus.Report
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("raw_prompt"), fmt.Sprintf("status JSON leaked residual evidence summary: %s", stdout.String()))
		gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring("persisted legacy detail"), fmt.Sprintf("status JSON leaked residual evidence summary: %s", stdout.String()))
		gomega.Expect(recoveryEvidenceRefsContain(report.ResidualWork[0].EvidenceRefs, recoverycontract.EvidenceRef{Kind: "legacy_failure", Path: "allowed.go"})).To(gomega.BeTrue(), fmt.Sprintf("expected sanitized legacy evidence ref in %#v", report.ResidualWork))

	})
	ginkgo.It("recover status rejects packet outside workspace root", func() {
		configPath := writePlanExecuteConfig()
		outsideRoot := ginkgo.GinkgoT().TempDir()
		writeRecoverTestPacket(outsideRoot, "recover-session-outside-packet", "packet.json")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"status", "--config", configPath, "--packet", filepath.Join(outsideRoot, "packet.json"), "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover status should reject outside packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverstatus.Report
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside packet status report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(recoverycontract.NextActionInspectFailure), fmt.Sprintf("unexpected outside packet status report: %#v", report))

	})
	ginkgo.It("recover promote rejects packet outside workspace root", func() {
		configPath := writePlanExecuteConfig()
		outsideRoot := ginkgo.GinkgoT().TempDir()
		writeRecoverTestPacket(outsideRoot, "recover-session-outside-promote-packet", "packet.json")

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{
			"promote",
			"--config", configPath,
			"--packet", filepath.Join(outsideRoot, "packet.json"),
			"--atom", "recovery-001",
			"--output", "briefs/recovery-001.md",
			"--json",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover promote should reject outside packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverPromoteReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside packet promote report: %#v", report))

	})
	ginkgo.It("recover reconcile rejects packet outside workspace root", func() {
		configPath := writePlanExecuteConfig()
		outsideRoot := ginkgo.GinkgoT().TempDir()
		writeRecoverTestPacket(outsideRoot, "recover-session-outside-reconcile-packet", "packet.json")

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{
			"reconcile",
			"--config", configPath,
			"--packet", filepath.Join(outsideRoot, "packet.json"),
			"--completed", "recovery-001",
			"--json",
		}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover reconcile should reject outside packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverReconcileReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside packet reconcile report: %#v", report))

	})
	ginkgo.It("recover decompose rejects outside output path", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		writeRecoverFailedPlanExecuteRecord(root, "recover-session-decompose-outside", []string{"allowed.go"})

		outsidePath := filepath.Join(root, "..", "outside-packet.json")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-outside", "--output", outsidePath, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside output report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionInspectFailure)), fmt.Sprintf("unexpected outside output report: %#v", report))

		if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("outside output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover decompose rejects protected output path", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		writeRecoverFailedPlanExecuteRecord(root, "recover-session-decompose-protected", []string{"allowed.go"})

		before, err := os.ReadFile(configPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read config before: %v", err))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-protected", "--output", "config.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject protected output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("protected"), fmt.Sprintf("unexpected protected output report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionInspectFailure)), fmt.Sprintf("unexpected protected output report: %#v", report))

		after, err := os.ReadFile(configPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read config after: %v", err))
		gomega.Expect(string(after)).To(gomega.Equal(string(before)), fmt.Sprintf("protected config output should not be overwritten"))

		stdout.Reset()
		stderr.Reset()

		code = RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-protected", "--output", "Config.JSON", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject case-variant protected output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		report = recoverDecomposeReport{}
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("protected"), fmt.Sprintf("unexpected case-variant protected output report: %#v", report))

	})
	ginkgo.It("recover decompose rejects recovery control artifact output", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		sessionID := "recover-session-decompose-control-artifact"
		writeRecoverFailedPlanExecuteRecord(root, sessionID, []string{"allowed.go"})

		failureID := recovery.PlanExecuteFailureID(sessionID)

		outputPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "failure.json"))

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", sessionID, "--output", outputPath, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject control artifact output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("control artifact"), fmt.Sprintf("unexpected control artifact report: %#v", report))

		if _, _, found, err := recovery.TryLoadFailureRecord(root, recovery.PlanExecuteFailureID(sessionID)); err != nil || !found {
			ginkgo.Fail(fmt.Sprintf("failure record should remain readable, found=%v err=%v", found, err))
		}
	})
	ginkgo.It("recover decompose rejects symlinked output parent", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		writeRecoverFailedPlanExecuteRecord(root, "recover-session-decompose-symlink", []string{"allowed.go"})

		outsideDir := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(root, "link-out")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-symlink", "--output", "link-out/packet.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject symlinked output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("unexpected symlink output report: %#v", report))

		if _, err := os.Stat(filepath.Join(outsideDir, "packet.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink target output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover decompose rejects symlinked default output parent", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		sessionID := "recover-session-decompose-default-symlink"
		writeRecoverFailedPlanExecuteRecord(root, sessionID, []string{"allowed.go"})

		failureID := recovery.PlanExecuteFailureID(sessionID)
		recoveryDir := filepath.Join(root, ".idiom", "recovery")
		outsideRecoveryDir := filepath.Join(ginkgo.GinkgoT().TempDir(), "recovery")
		gomega.Expect(os.Rename(recoveryDir, outsideRecoveryDir)).To(gomega.Succeed())

		if err := os.Symlink(outsideRecoveryDir, recoveryDir); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", sessionID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject symlinked default output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("unexpected symlink default output report: %#v", report))

		if _, err := os.Stat(filepath.Join(outsideRecoveryDir, failureID, "packet.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink default target output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover decompose rejects preexisting temp symlink", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		sessionID := "recover-session-decompose-temp-symlink"
		writeRecoverFailedPlanExecuteRecord(root, sessionID, []string{"allowed.go"})

		failureID := recovery.PlanExecuteFailureID(sessionID)
		packetPath := filepath.Join(root, ".idiom", "recovery", failureID, "packet.json")
		controlPath := filepath.Join(root, ".idiom", "recovery", failureID, "failure.json")

		before, err := os.ReadFile(controlPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read failure record before: %v", err))

		if err := os.Symlink(controlPath, packetPath+".tmp"); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"decompose", "--config", configPath, "--session", sessionID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover decompose should reject preexisting temp symlink, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverDecomposeReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("write recovery decomposition packet"), fmt.Sprintf("unexpected temp symlink report: %#v", report))

		after, err := os.ReadFile(controlPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read failure record after: %v", err))
		gomega.Expect(string(after)).To(gomega.Equal(string(before)), fmt.Sprintf("failure record should not be overwritten through temp symlink"))

	})
	ginkgo.It("recover reconcile merges completed atoms", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)

		record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
			SessionID:         "recover-session-reconcile-merge",
			FailureKind:       "executor_failed",
			FailureReason:     "edit failed",
			RequiredEditPaths: []string{"pkg/a/a.go", "pkg/b/b.go", "pkg/c/c.go", "pkg/d/d.go", "pkg/e/e.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

		writeRecoverTestGoModule(root)

		for _, dir := range []string{"pkg/a", "pkg/b", "pkg/c", "pkg/d", "pkg/e"} {
			writeRecoverTestPackage(root, dir)
		}

		packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))
		gomega.Expect(len(packet.CandidateAtoms) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected multiple atoms, got %#v", packet.CandidateAtoms))

		if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[0].ID, "--output", "reconciliation.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("first reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		stdout.Reset()
		stderr.Reset()

		code = RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[1].ID, "--output", "reconciliation.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("second reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverReconcileReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.CompletedAtoms).To(gomega.HaveLen(2), fmt.Sprintf("expected merged completed atoms, got %#v", report))
		gomega.Expect(recoverAtomIDsContain(report.CompletedAtoms, packet.CandidateAtoms[0].ID)).To(gomega.BeTrue(), fmt.Sprintf("expected merged completed atoms, got %#v", report))
		gomega.Expect(recoverAtomIDsContain(report.CompletedAtoms, packet.CandidateAtoms[1].ID)).To(gomega.BeTrue(), fmt.Sprintf("expected merged completed atoms, got %#v", report))

	})
	ginkgo.It("run recovery verification command uses workspace root", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoverTestGoModule(root)
		writeRecoverTestPackage(root, "pkg/pass")

		result, err := runRecoveryVerificationCommand(root, "go test ./pkg/pass")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("verification command should pass from workspace root: %v", err))
		gomega.Expect(result.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationPass), fmt.Sprintf("unexpected pass result: %#v", result))
		gomega.Expect(result.ExitCode).To(gomega.Equal(0), fmt.Sprintf("unexpected pass result: %#v", result))
		gomega.Expect(result.StartedAt).NotTo(gomega.Equal(""), fmt.Sprintf("verification result should record timestamps: %#v", result))
		gomega.Expect(result.CompletedAt).NotTo(gomega.Equal(""), fmt.Sprintf("verification result should record timestamps: %#v", result))

	})
	ginkgo.It("run recovery verification command records failure exit code", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoverTestGoModule(root)
		writeRecoverFailingTestPackage(root, "pkg/fail")

		result, err := runRecoveryVerificationCommand(root, "go test ./pkg/fail")
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("failing verification command returned nil error"))
		gomega.Expect(result.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationFail), fmt.Sprintf("unexpected failure result: %#v err=%v", result, err))
		gomega.Expect(result.ExitCode).NotTo(gomega.Equal(0), fmt.Sprintf("unexpected failure result: %#v err=%v", result, err))
		gomega.Expect(result.StartedAt).NotTo(gomega.Equal(""), fmt.Sprintf("verification failure should record timestamps: %#v", result))
		gomega.Expect(result.CompletedAt).NotTo(gomega.Equal(""), fmt.Sprintf("verification failure should record timestamps: %#v", result))

	})
	ginkgo.It("run recovery verification command rejects unsupported command", func() {
		for _, tc := range []struct {
			command string
			error   string
		}{
			{command: "go env GOPATH", error: "unsupported recovery verification command"},
			{command: "go test", error: "must name a package pattern"},
		} {
			result, err := runRecoveryVerificationCommand(ginkgo.GinkgoT().TempDir(), tc.command)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("%s error = %v, want %q", tc.command, err, tc.error))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(tc.error), fmt.Sprintf("%s error = %v, want %q", tc.command, err, tc.error))
			gomega.Expect(result.Command).To(gomega.Equal(tc.command), fmt.Sprintf("unexpected unsupported-command result for %s: %#v", tc.command, result))
			gomega.Expect(result.Status).To(gomega.Equal(recoverycontract.ReconciliationVerificationFail), fmt.Sprintf("unexpected unsupported-command result for %s: %#v", tc.command, result))
			gomega.Expect(result.ExitCode).To(gomega.Equal(-1), fmt.Sprintf("unexpected unsupported-command result for %s: %#v", tc.command, result))
			gomega.Expect(result.CompletedAt).NotTo(gomega.Equal(""), fmt.Sprintf("unexpected unsupported-command result for %s: %#v", tc.command, result))

		}
	})
	ginkgo.It("run recovery brief lint gate maps lint report", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRecoveryBriefWorkspaceMarker(root)
		writeRecoveryFile(filepath.Join(root, "allowed.go"), "package allowed\n")

		passPath := writeRecoveryBrief(root, recoveryLintBriefBody("allowed.go"))
		pass := runRecoveryBriefLintGate(root, passPath)
		gomega.Expect(pass.Status).To(gomega.Equal(recoverycontract.GateStatusPass), fmt.Sprintf("unexpected passing lint gate: %#v", pass))
		gomega.Expect(pass.Summary).To(gomega.Equal("brief lint exit 0"), fmt.Sprintf("unexpected passing lint gate: %#v", pass))
		gomega.Expect(pass.Findings).To(gomega.HaveLen(0), fmt.Sprintf("unexpected passing lint gate: %#v", pass))

		blockPath := writeRecoveryBrief(root, "# Brief\n\n## Required Edits\n\nMissing file table.\n")
		block := runRecoveryBriefLintGate(root, blockPath)
		gomega.Expect(block.Status).To(gomega.Equal(recoverycontract.GateStatusBlock), fmt.Sprintf("unexpected blocking lint gate: %#v", block))
		gomega.Expect(block.Summary).To(gomega.HavePrefix("brief lint exit "), fmt.Sprintf("unexpected blocking lint gate: %#v", block))
		gomega.Expect(block.Findings).NotTo(gomega.HaveLen(0), fmt.Sprintf("unexpected blocking lint gate: %#v", block))

	})
	ginkgo.It("recovery reconciliation learning rejects ranker state paths", func() {
		root := ginkgo.GinkgoT().TempDir()
		packet := recoverycontract.DecompositionPacket{
			SchemaVersion: recoverycontract.DecompositionPacketSchemaVersion,
			FailureID:     "failure-ranker-path",
			PacketID:      "packet-failure-ranker-path",
			CandidateAtoms: []recoverycontract.CandidateAtom{{
				ID:            "recovery-001",
				RequiredPaths: []string{".idiom\\ranker\\weights.json"},
				RequiredTests: []string{"go test ./cmd/idiom"},
			}},
		}

		reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
			CompletedAtomIDs: []string{"recovery-001"},
			Verification: recoverycontract.ReconciliationVerification{
				Status:        recoverycontract.ReconciliationVerificationPass,
				RequiredTests: []string{"go test ./cmd/idiom"},
				Commands: []recoverycontract.ReconciliationCommandResult{{
					Command:  "go test ./cmd/idiom",
					Status:   recoverycontract.ReconciliationVerificationPass,
					ExitCode: 0,
				}},
			},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build reconciliation: %v", err))

		report := recordRecoverReconciliationLearning(root, packet, reconciliation)
		gomega.Expect(report.State).To(gomega.Equal(comprehension.LearningStateRejected), fmt.Sprintf("ranker state paths should be rejected as learning files, got %#v", report))
		gomega.Expect(report.Reason).To(gomega.Equal(comprehension.LearningReasonNoValidFiles), fmt.Sprintf("ranker state paths should be rejected as learning files, got %#v", report))
		gomega.Expect(report.Files).To(gomega.BeEmpty(), fmt.Sprintf("ranker state paths must not be retained in learning report: %#v", report.Files))

	})
	ginkgo.It("recover reconcile rejects outside output path", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)

		record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
			SessionID:         "recover-session-reconcile-outside",
			FailureKind:       "executor_failed",
			FailureReason:     "edit failed",
			RequiredEditPaths: []string{"allowed.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

		packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

		if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
		}

		outsidePath := filepath.Join(root, "..", "outside-reconciliation.json")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", "recovery-001", "--output", outsidePath, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover reconcile should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverReconcileReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside output report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionInspectFailure)), fmt.Sprintf("unexpected outside output report: %#v", report))

		if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("outside output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover reconcile rejects symlinked output parent", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		writeRecoverTestGoModule(root)
		gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package recover_test\n"), 0o644)).To(gomega.Succeed())

		packet := writeRecoverTestPacket(root, "recover-session-reconcile-symlink", "packet.json")
		outsideDir := ginkgo.GinkgoT().TempDir()

		if err := os.Symlink(outsideDir, filepath.Join(root, "link-out")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[0].ID, "--output", "link-out/reconciliation.json", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover reconcile should reject symlinked output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverReconcileReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("unexpected symlink output report: %#v", report))

		if _, err := os.Stat(filepath.Join(outsideDir, "reconciliation.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink target output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover reconcile rejects symlinked packet directory", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)

		packet := writeRecoverTestPacket(root, "recover-session-reconcile-symlink-default", "")
		outsideDir := ginkgo.GinkgoT().TempDir()

		if _, _, err := recovery.WriteDecompositionPacket(outsideDir, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket outside: %v", err))
		}

		if err := os.Symlink(outsideDir, filepath.Join(root, "link-packet")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"reconcile", "--config", configPath, "--packet", "link-packet/packet.json", "--completed", packet.CandidateAtoms[0].ID, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverReconcileReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlink"), fmt.Sprintf("expected symlink packet rejection, got %#v", report))

		if _, err := os.Stat(filepath.Join(outsideDir, "reconciliation.json")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink packet dir reconciliation should not be written outside root, stat err=%v", err))
		}
	})
	ginkgo.It("recover promote requires explicit allow dirty for dirty packet", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644)).To(gomega.Succeed())

		record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
			SessionID:         "recover-session-dirty",
			FailureKind:       "executor_failed",
			FailureReason:     "edit failed",
			RequiredEditPaths: []string{"allowed.go"},
			ChangedPaths:      []string{"allowed.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

		partial := recovery.PartialStateForDirtyPaths([]string{"allowed.go"}, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1)

		packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC(), PartialState: partial})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

		if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
		}

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", "briefs/recovery-001.md", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover promote should block dirty packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverPromoteReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Status).To(gomega.Equal("blocked"), fmt.Sprintf("expected workspace-state block: %#v", report))
		gomega.Expect(recoverGateBlocked(report.Gates, recoverycontract.GateWorkspaceState)).To(gomega.BeTrue(), fmt.Sprintf("expected workspace-state block: %#v", report))

		if _, err := os.Stat(filepath.Join(root, "briefs", "recovery-001.md")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("blocked promotion should not write output, stat err=%v", err))
		}
	})
	ginkgo.It("recover promote rejects outside output path", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644)).To(gomega.Succeed())

		record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
			SessionID:         "recover-session-outside",
			FailureKind:       "executor_failed",
			FailureReason:     "edit failed",
			RequiredEditPaths: []string{"allowed.go"},
		}, time.Now().UTC())
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

		packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

		if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
		}

		outsidePath := filepath.Join(root, "..", "outside-recovery.md")

		var stdout, stderr bytes.Buffer

		code := RunMainWithTelemetry([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", outsidePath, "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover promote should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverPromoteReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("workspace root"), fmt.Sprintf("unexpected outside output report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionHumanIntervention)), fmt.Sprintf("unexpected outside output report: %#v", report))

		if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("outside output should not be written, stat err=%v", err))
		}
	})
	ginkgo.It("recover promote rejects symlinked output parent", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		packet := writePromotableRecoverPacket(root, "recover-session-promote-symlink-parent")

		outsideDir := ginkgo.GinkgoT().TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(root, "brief-link")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", packet.CandidateAtoms[0].ID, "--output", "brief-link/recovery.md", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover promote should reject symlinked parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverPromoteReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlinked directories"), fmt.Sprintf("unexpected symlink parent report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionHumanIntervention)), fmt.Sprintf("unexpected symlink parent report: %#v", report))

		if _, err := os.Stat(filepath.Join(outsideDir, "recovery.md")); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink parent output should not be written outside root, stat err=%v", err))
		}
	})
	ginkgo.It("recover promote rejects symlinked output file", func() {
		configPath := writePlanExecuteConfig()
		root := filepath.Dir(configPath)
		packet := writePromotableRecoverPacket(root, "recover-session-promote-symlink-file")

		outsidePath := filepath.Join(ginkgo.GinkgoT().TempDir(), "outside.md")
		gomega.Expect(os.MkdirAll(filepath.Join(root, "briefs"), 0o755)).To(gomega.Succeed())

		if err := os.Symlink(outsidePath, filepath.Join(root, "briefs", "recovery.md")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		var stdout, stderr bytes.Buffer
		code := RunMainWithTelemetry([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", packet.CandidateAtoms[0].ID, "--output", "briefs/recovery.md", "--json"}, &stdout, &stderr, nil)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("recover promote should reject symlinked output file, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

		var report recoverPromoteReport
		gomega.Expect(json.Unmarshal(stdout.Bytes(), &report)).To(gomega.Succeed())
		gomega.Expect(report.Error).To(gomega.ContainSubstring("symlink"), fmt.Sprintf("unexpected symlink target report: %#v", report))
		gomega.Expect(report.NextAction).To(gomega.Equal(string(recoverycontract.NextActionHumanIntervention)), fmt.Sprintf("unexpected symlink target report: %#v", report))

		if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
			ginkgo.Fail(fmt.Sprintf("symlink target output should not be written outside root, stat err=%v", err))
		}
	})
})

func recoverGateBlocked(gates []recoverGateReport, name string) bool {
	for _, gate := range gates {
		if gate.Name == name && gate.Status == recoverycontract.GateStatusBlock {
			return true
		}
	}

	return false
}

func writePromotableRecoverPacket(root string, sessionID string) recoverycontract.DecompositionPacket {
	gomega.Expect(os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644)).To(gomega.Succeed())

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		FailureKind:       string(plancontract.PlanExecuteFailureExecutorFailed),
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
	}, time.Now().UTC())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
	}

	return packet
}

func recoverAtomIDsContain(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}

	return false
}

func recoveryEvidenceRefsContain(refs []recoverycontract.EvidenceRef, want recoverycontract.EvidenceRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}

	return false
}

func writeRecoverTestGoModule(root string) {
	gomega.Expect(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module recover.test\n\ngo 1.25\n"), 0o644)).To(gomega.Succeed())

}

func writeRecoverTestPackage(root string, dir string) {

	fullDir := filepath.Join(root, filepath.FromSlash(dir))
	gomega.Expect(os.MkdirAll(fullDir, 0o755)).To(gomega.Succeed())

	pkg := filepath.Base(dir)
	body := []byte("package " + pkg + "\n\nfunc Value() int { return 1 }\n")
	gomega.Expect(os.WriteFile(filepath.Join(fullDir, pkg+".go"), body, 0o644)).To(gomega.Succeed())

}

func writeRecoverFailingTestPackage(root string, dir string) {

	fullDir := filepath.Join(root, filepath.FromSlash(dir))
	gomega.Expect(os.MkdirAll(fullDir, 0o755)).To(gomega.Succeed())

	pkg := filepath.Base(dir)
	body := []byte("package " + pkg + "\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"expected failure\") }\n")
	gomega.Expect(os.WriteFile(filepath.Join(fullDir, pkg+"_test.go"), body, 0o644)).To(gomega.Succeed())

}

func writeRecoveryBriefWorkspaceMarker(root string) {
	gomega.Expect(os.Mkdir(filepath.Join(root, ".git"), 0o755)).To(gomega.Succeed())

}

func writeRecoveryFile(path string, content string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())

}

func writeRecoveryBrief(root string, body string) string {

	path := filepath.Join(root, "brief.md")
	writeRecoveryFile(path, body)

	return path
}

func recoveryLintBriefBody(path string) string {
	return "# Brief\n\n" +
		"## Files to Modify (EXHAUSTIVE allowlist)\n\n" +
		"| File | Change |\n" +
		"|------|--------|\n" +
		"| `" + path + "` | MODIFY existing package file |\n\n" +
		"## Verified Anchors\n\n" +
		"Confirmed `" + path + ":1` currently declares package allowed.\n\n" +
		"## Required Edits\n\n" +
		"Apply the edit named in the file table.\n\n" +
		"## Acceptance\n\n" +
		"- `go build ./...` succeeds.\n"
}

func writeRecoverFailedPlanExecuteRecord(root string, sessionID string, requiredPaths []string) {

	state := planexecute.NewProgressState(sessionID, "make a change", "", requiredPaths, nil, time.Now().UTC())

	state.Status = plancontract.PlanExecuteProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "edit failed"

	statePath, err := planexecute.SaveProgressState(root, &state)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("save progress: %v", err))

	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgressWithInvariants(state, statePath, nil), time.Now().UTC()); err != nil {
		ginkgo.Fail(fmt.Sprintf("write recovery record: %v", err))
	}
}

func writeRecoverTestPacket(root string, sessionID string, outputPath string) recoverycontract.DecompositionPacket {

	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
	}, time.Now().UTC())
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildPlanExecuteFailureRecord: %v", err))

	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildDecompositionPacket: %v", err))

	if outputPath != "" {
		if _, _, err := recovery.WriteDecompositionPacket(root, packet, outputPath); err != nil {
			ginkgo.Fail(fmt.Sprintf("WriteDecompositionPacket: %v", err))
		}
	}

	return packet
}

func initRecoverTestGitRepo(root string) {

	cmd := exec.Command("git", "-C", root, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		ginkgo.Fail(fmt.Sprintf("git init: %v\n%s", err, out))
	}
}

func writePlanExecuteConfig() string {

	root := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().
		Setenv("IDIOM_QUEUE_SOCKET", filepath.Join(root, "missing-runner.sock"))

	path := filepath.Join(root, "config.json")
	body := fmt.Sprintf(`{"workspace":{"root":%q,"cwd":%q}}`, root, root)
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())

	return path
}
