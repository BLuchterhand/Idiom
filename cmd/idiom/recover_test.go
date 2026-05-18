package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/planexecute"
	"idiom/internal/recovery"
)

func TestRunMainRecoverInspectReadsFailureRecord(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	state := planexecute.NewProgressState("recover-session-1", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = planexecute.RunFailurePlannerBlocked
	state.FailureReason = "<<QUESTION>>: answer needed before editing"
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("save progress: %v", err)
	}
	record, recordPath, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgress(state, statePath), time.Now().UTC())
	if err != nil {
		t.Fatalf("write recovery record: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"recover", "inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverInspectReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Status != "failed" || !report.Recorded {
		t.Fatalf("unexpected report status/recorded: %#v", report)
	}
	if report.FailureID != record.FailureID || report.RecordPath != recordPath {
		t.Fatalf("record identity mismatch: %#v want id=%s path=%s", report, record.FailureID, recordPath)
	}
	if report.FailureClass != recovery.FailureClassPlannerBlocked {
		t.Fatalf("failure class=%q", report.FailureClass)
	}
	if report.NextAction != recovery.NextActionAnswerBlocker {
		t.Fatalf("next action=%q", report.NextAction)
	}
}

func TestRunRecoverInspectComputesLegacyFailedProgressWithoutWriting(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	state := planexecute.NewProgressState("recover-session-2", "make a change", "", []string{"a.go"}, nil, time.Now().UTC())
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = "plan_artifact_missing"
	state.FailureReason = "no plan artifact"
	if _, err := planexecute.SaveProgressState(root, &state); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverInspectReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Recorded {
		t.Fatalf("legacy progress fallback must not write during inspect: %#v", report)
	}
	if report.FailureClass != recovery.FailureClassPlanMissing {
		t.Fatalf("failure class=%q", report.FailureClass)
	}
	if _, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, state.SessionID); err != nil || found {
		t.Fatalf("inspect should not persist fallback record, found=%v err=%v", found, err)
	}
}

func TestRunRecoverUnknownSubcommandExitsTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"wat"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRecoverTelemetryNameIncludesSubcommand(t *testing.T) {
	if got := commandTelemetryName([]string{"recover", "inspect"}); got != "recover:inspect" {
		t.Fatalf("telemetry command=%q", got)
	}
}

func TestRecoverInspectUsageErrorCanBeJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"inspect", "--json"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverInspectReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.Status != "error" || !strings.Contains(report.Error, "--session is required") {
		t.Fatalf("unexpected report: %#v", report)
	}
	if stderr.Len() != 0 {
		t.Fatalf("json usage errors should not also write stderr, got %s", stderr.String())
	}
}

func TestRunRecoverInspectClassifiesOutOfScopeDirtyWorkspace(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	initRecoverTestGitRepo(t, root)
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(".idiom/\nconfig.json\n"), 0o644); err != nil {
		t.Fatalf("write git exclude: %v", err)
	}
	state := planexecute.NewProgressState("recover-session-3", "make a change", "", []string{"allowed.go"}, nil, time.Now().UTC())
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "edit failed"
	state.ChangedPaths = []string{"allowed.go"}
	state.TotalEdits = 1
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgress(state, statePath), time.Now().UTC()); err != nil {
		t.Fatalf("write recovery record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unexpected.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"inspect", "--config", configPath, "--session", state.SessionID, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recover inspect exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverInspectReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.PartialState.DirtyStatus != recovery.DirtyStatusDirtyOutScope {
		t.Fatalf("dirty status=%q report=%#v", report.PartialState.DirtyStatus, report)
	}
	if report.PartialState.ScopeStatus != recovery.ScopeStatusOutOfScope {
		t.Fatalf("scope status=%q report=%#v", report.PartialState.ScopeStatus, report)
	}
	if report.NextAction != recovery.NextActionHumanIntervention {
		t.Fatalf("next action=%q", report.NextAction)
	}
}

func TestRecoverDecomposeStatusAndPromote(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644); err != nil {
		t.Fatalf("write scoped source: %v", err)
	}
	state := planexecute.NewProgressState("recover-session-4", "make a change", "", []string{"allowed.go"}, nil, time.Now().UTC())
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "edit failed"
	statePath, err := planexecute.SaveProgressState(root, &state)
	if err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, planexecute.RecoveryInputFromProgress(state, statePath), time.Now().UTC()); err != nil {
		t.Fatalf("write recovery record: %v", err)
	}

	var decomposeOut, decomposeErr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", state.SessionID, "--output", "packet.json", "--json"}, &decomposeOut, &decomposeErr)
	if code != 0 {
		t.Fatalf("recover decompose exit=%d stdout=%s stderr=%s", code, decomposeOut.String(), decomposeErr.String())
	}
	var decomposeReport recoverDecomposeReport
	if err := json.Unmarshal(decomposeOut.Bytes(), &decomposeReport); err != nil {
		t.Fatalf("decode decompose report: %v\n%s", err, decomposeOut.String())
	}
	if decomposeReport.Status != "ready" || decomposeReport.PacketPath != "packet.json" {
		t.Fatalf("unexpected decompose report: %#v", decomposeReport)
	}
	if len(decomposeReport.CandidateAtoms) != 1 || decomposeReport.CandidateAtoms[0].ID != "recovery-001" {
		t.Fatalf("unexpected candidate atoms: %#v", decomposeReport.CandidateAtoms)
	}

	var statusOut, statusErr bytes.Buffer
	code = runRecoverMain([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &statusOut, &statusErr)
	if code != 0 {
		t.Fatalf("recover status exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String())
	}
	var statusReport recoverStatusReport
	if err := json.Unmarshal(statusOut.Bytes(), &statusReport); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, statusOut.String())
	}
	if statusReport.Status != "ready" || statusReport.NextAction != recovery.NextActionReviewDecomposition {
		t.Fatalf("unexpected status report: %#v", statusReport)
	}

	var promoteOut, promoteErr bytes.Buffer
	code = runRecoverMain([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", "briefs/recovery-001.md", "--json"}, &promoteOut, &promoteErr)
	if code != 0 {
		t.Fatalf("recover promote exit=%d stdout=%s stderr=%s", code, promoteOut.String(), promoteErr.String())
	}
	var promoteReport recoverPromoteReport
	if err := json.Unmarshal(promoteOut.Bytes(), &promoteReport); err != nil {
		t.Fatalf("decode promote report: %v\n%s", err, promoteOut.String())
	}
	if promoteReport.Status != "promoted" || promoteReport.NextAction != "dispatch_atom" {
		t.Fatalf("unexpected promote report: %#v", promoteReport)
	}
	if _, err := os.Stat(filepath.Join(root, "briefs", "recovery-001.md")); err != nil {
		t.Fatalf("promotion output missing: %v", err)
	}
	for _, gate := range promoteReport.Gates {
		if gate.Status != "pass" {
			t.Fatalf("gate did not pass: %#v report=%#v", gate, promoteReport)
		}
	}
}

func TestSavePlanExecuteProgressStateWritesRecoveryDecompositionPacket(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	state := planexecute.NewProgressState("recover-session-5", "make a change", "", []string{"allowed.go"}, nil, time.Now().UTC())
	state.Status = planexecute.ProgressStatusFailed
	state.FailureKind = "executor_failed"
	state.FailureReason = "executor failed"
	var stdout bytes.Buffer
	if _, err := savePlanExecuteProgressState(root, &state, true, &stdout, "test_state_saved"); err != nil {
		t.Fatalf("savePlanExecuteProgressState: %v", err)
	}
	record, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, state.SessionID)
	if err != nil || !found {
		t.Fatalf("TryLoadPlanExecuteFailureRecord found=%v err=%v", found, err)
	}
	packetPath, _, err := recovery.DecompositionPacketPath(root, record.FailureID, "")
	if err != nil {
		t.Fatalf("DecompositionPacketPath: %v", err)
	}
	packet, _, found, err := recovery.TryLoadDecompositionPacket(packetPath)
	if err != nil || !found {
		t.Fatalf("TryLoadDecompositionPacket found=%v err=%v", found, err)
	}
	if packet.NextAction != recovery.NextActionReviewDecomposition || len(packet.CandidateAtoms) != 1 {
		t.Fatalf("unexpected packet: %#v", packet)
	}
	if !strings.Contains(stdout.String(), `"type":"idiom.plan_execute.recovery_decomposition_packet_written"`) {
		t.Fatalf("packet event missing from stdout=%s", stdout.String())
	}
}

func initRecoverTestGitRepo(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}
