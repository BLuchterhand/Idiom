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

func initRecoverTestGitRepo(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}
