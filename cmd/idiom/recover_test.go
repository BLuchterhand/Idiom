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

	"idiom/internal/comprehension"
	"idiom/internal/planexecute"
	"idiom/internal/recoverstatus"
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
	writeRecoverTestGoModule(t, root)
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
	if len(statusReport.ResidualWork) != 1 ||
		!recoveryEvidenceRefsContain(statusReport.ResidualWork[0].EvidenceRefs, recovery.EvidenceRef{Kind: "required_path", Path: "allowed.go"}) ||
		!recoveryEvidenceRefsContain(statusReport.ResidualWork[0].EvidenceRefs, recovery.EvidenceRef{Kind: "required_test", ID: "go test ./..."}) {
		t.Fatalf("expected residual evidence refs in status report: %#v", statusReport.ResidualWork)
	}

	statusOut.Reset()
	statusErr.Reset()
	code = runRecoverMain([]string{"status", "--config", configPath, "--packet", "packet.json"}, &statusOut, &statusErr)
	if code != 0 {
		t.Fatalf("recover status text exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String())
	}
	if !strings.Contains(statusOut.String(), "evidence_refs: required_path:allowed.go") ||
		!strings.Contains(statusOut.String(), "required_test:go test ./...") {
		t.Fatalf("expected residual evidence refs in status text, got %s", statusOut.String())
	}
	sharedStatusReport, err := recoverstatus.Build(root, "packet.json")
	if err != nil {
		t.Fatalf("shared recover status build: %v", err)
	}
	if statusOut.String() != recoverstatus.FormatText(sharedStatusReport) {
		t.Fatalf("CLI recover status text must match shared presenter.\nwant:\n%s\n\ngot:\n%s", recoverstatus.FormatText(sharedStatusReport), statusOut.String())
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

	var reconcileOut, reconcileErr bytes.Buffer
	code = runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", "recovery-001", "--json"}, &reconcileOut, &reconcileErr)
	if code != 0 {
		t.Fatalf("recover reconcile exit=%d stdout=%s stderr=%s", code, reconcileOut.String(), reconcileErr.String())
	}
	var reconcileReport recoverReconcileReport
	if err := json.Unmarshal(reconcileOut.Bytes(), &reconcileReport); err != nil {
		t.Fatalf("decode reconcile report: %v\n%s", err, reconcileOut.String())
	}
	if reconcileReport.Status != recovery.ReconciliationStatusRecovered || reconcileReport.NextAction != recovery.NextActionNone {
		t.Fatalf("unexpected reconcile report: %#v", reconcileReport)
	}
	if reconcileReport.RankerLearning == nil ||
		reconcileReport.RankerLearning.State != comprehension.LearningStateAccepted ||
		reconcileReport.RankerLearning.Source != comprehension.LearningSourceRecoveryReconciliation ||
		reconcileReport.RankerLearning.Reason != comprehension.LearningReasonVerifiedRecovery ||
		reconcileReport.RankerLearning.EvidenceKind != comprehension.LearningEvidenceKindRecoveryReconciliation {
		t.Fatalf("expected accepted recovery learning evidence, got %#v", reconcileReport.RankerLearning)
	}
	if reconcileReport.RankerLearning.Metrics["packet_quality"] != 1 ||
		reconcileReport.RankerLearning.Metrics["completed_atom_count"] != 1 {
		t.Fatalf("expected recovery learning metrics, got %#v", reconcileReport.RankerLearning.Metrics)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery learning evidence must not write weights.json, err=%v", err)
	}
	learningEntries, err := comprehension.ReadLearningLog(root)
	if err != nil {
		t.Fatalf("read learning log: %v", err)
	}
	learningSummary := comprehension.SummarizeLearningLog(learningEntries)
	if learningSummary.BySource[comprehension.LearningSourceRecoveryReconciliation] != 1 ||
		learningSummary.AcceptedReasonCounts[comprehension.LearningReasonVerifiedRecovery] != 1 {
		t.Fatalf("learning summary missing recovery reconciliation event: %#v", learningSummary)
	}
	if _, err := os.Stat(filepath.Join(root, reconcileReport.ReconciliationPath)); err != nil {
		t.Fatalf("reconciliation output missing: %v", err)
	}
	if reconcileReport.ReconciliationPath != "reconciliation.json" {
		t.Fatalf("expected sibling reconciliation output, got %#v", reconcileReport)
	}

	statusOut.Reset()
	statusErr.Reset()
	code = runRecoverMain([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &statusOut, &statusErr)
	if code != 0 {
		t.Fatalf("recover status after reconcile exit=%d stdout=%s stderr=%s", code, statusOut.String(), statusErr.String())
	}
	statusReport = recoverStatusReport{}
	if err := json.Unmarshal(statusOut.Bytes(), &statusReport); err != nil {
		t.Fatalf("decode reconciled status report: %v\n%s", err, statusOut.String())
	}
	if statusReport.Status != "recovered" || statusReport.NextAction != recovery.NextActionNone || len(statusReport.CandidateAtoms) != 0 || statusReport.Reconciliation == nil {
		t.Fatalf("expected recovered status after reconcile, got %#v", statusReport)
	}
	if len(statusReport.ResidualWork) != 0 {
		t.Fatalf("recovered status should not list remaining residual work: %#v", statusReport.ResidualWork)
	}
}

func TestRecoverStatusScrubsPersistedResidualEvidenceSummaries(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	packet := writeRecoverTestPacket(t, root, "recover-session-summary-scrub", "packet.json")
	packet.ResidualWork[0].EvidenceRefs = append(packet.ResidualWork[0].EvidenceRefs, recovery.EvidenceRef{
		Kind:    "legacy_failure",
		Path:    "allowed.go",
		Summary: "raw_prompt: persisted legacy detail",
	})
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("rewrite packet: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"status", "--config", configPath, "--packet", "packet.json", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recover status exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverStatusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, stdout.String())
	}
	if strings.Contains(stdout.String(), "raw_prompt") || strings.Contains(stdout.String(), "persisted legacy detail") {
		t.Fatalf("status JSON leaked residual evidence summary: %s", stdout.String())
	}
	if !recoveryEvidenceRefsContain(report.ResidualWork[0].EvidenceRefs, recovery.EvidenceRef{Kind: "legacy_failure", Path: "allowed.go"}) {
		t.Fatalf("expected sanitized legacy evidence ref in %#v", report.ResidualWork)
	}
}

func TestRecoverStatusRejectsPacketOutsideWorkspaceRoot(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	outsideRoot := t.TempDir()
	writeRecoverTestPacket(t, outsideRoot, "recover-session-outside-packet", "packet.json")

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"status", "--config", configPath, "--packet", filepath.Join(outsideRoot, "packet.json"), "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover status should reject outside packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverStatusReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "workspace root") || report.NextAction != recovery.NextActionInspectFailure {
		t.Fatalf("unexpected outside packet status report: %#v", report)
	}
}

func TestRecoverDecomposeRejectsOutsideOutputPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	writeRecoverFailedPlanExecuteRecord(t, root, "recover-session-decompose-outside", []string{"allowed.go"})

	outsidePath := filepath.Join(root, "..", "outside-packet.json")
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-outside", "--output", outsidePath, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "workspace root") || report.NextAction != recovery.NextActionInspectFailure {
		t.Fatalf("unexpected outside output report: %#v", report)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("outside output should not be written, stat err=%v", err)
	}
}

func TestRecoverDecomposeRejectsProtectedOutputPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	writeRecoverFailedPlanExecuteRecord(t, root, "recover-session-decompose-protected", []string{"allowed.go"})
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config before: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-protected", "--output", "config.json", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject protected output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "protected") || report.NextAction != recovery.NextActionInspectFailure {
		t.Fatalf("unexpected protected output report: %#v", report)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("protected config output should not be overwritten")
	}

	stdout.Reset()
	stderr.Reset()
	code = runRecoverMain([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-protected", "--output", "Config.JSON", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject case-variant protected output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	report = recoverDecomposeReport{}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode case-variant report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "protected") {
		t.Fatalf("unexpected case-variant protected output report: %#v", report)
	}
}

func TestRecoverDecomposeRejectsRecoveryControlArtifactOutput(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	sessionID := "recover-session-decompose-control-artifact"
	writeRecoverFailedPlanExecuteRecord(t, root, sessionID, []string{"allowed.go"})
	failureID := recovery.PlanExecuteFailureID(sessionID)

	outputPath := filepath.ToSlash(filepath.Join(".idiom", "recovery", failureID, "failure.json"))
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", sessionID, "--output", outputPath, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject control artifact output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "control artifact") {
		t.Fatalf("unexpected control artifact report: %#v", report)
	}
	if _, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, sessionID); err != nil || !found {
		t.Fatalf("failure record should remain readable, found=%v err=%v", found, err)
	}
}

func TestRecoverDecomposeRejectsSymlinkedOutputParent(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	writeRecoverFailedPlanExecuteRecord(t, root, "recover-session-decompose-symlink", []string{"allowed.go"})
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, "link-out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", "recover-session-decompose-symlink", "--output", "link-out/packet.json", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject symlinked output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "symlinked directories") {
		t.Fatalf("unexpected symlink output report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "packet.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink target output should not be written, stat err=%v", err)
	}
}

func TestRecoverDecomposeRejectsSymlinkedDefaultOutputParent(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	sessionID := "recover-session-decompose-default-symlink"
	writeRecoverFailedPlanExecuteRecord(t, root, sessionID, []string{"allowed.go"})
	failureID := recovery.PlanExecuteFailureID(sessionID)
	recoveryDir := filepath.Join(root, ".idiom", "recovery")
	outsideRecoveryDir := filepath.Join(t.TempDir(), "recovery")
	if err := os.Rename(recoveryDir, outsideRecoveryDir); err != nil {
		t.Fatalf("move recovery dir outside workspace: %v", err)
	}
	if err := os.Symlink(outsideRecoveryDir, recoveryDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", sessionID, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject symlinked default output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "symlinked directories") {
		t.Fatalf("unexpected symlink default output report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(outsideRecoveryDir, failureID, "packet.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink default target output should not be written, stat err=%v", err)
	}
}

func TestRecoverDecomposeRejectsPreexistingTempSymlink(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	sessionID := "recover-session-decompose-temp-symlink"
	writeRecoverFailedPlanExecuteRecord(t, root, sessionID, []string{"allowed.go"})
	failureID := recovery.PlanExecuteFailureID(sessionID)
	packetPath := filepath.Join(root, ".idiom", "recovery", failureID, "packet.json")
	controlPath := filepath.Join(root, ".idiom", "recovery", failureID, "failure.json")
	before, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read failure record before: %v", err)
	}
	if err := os.Symlink(controlPath, packetPath+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"decompose", "--config", configPath, "--session", sessionID, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover decompose should reject preexisting temp symlink, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverDecomposeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "write recovery decomposition packet") {
		t.Fatalf("unexpected temp symlink report: %#v", report)
	}
	after, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read failure record after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("failure record should not be overwritten through temp symlink")
	}
}

func TestRecoverReconcileMergesCompletedAtoms(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "recover-session-reconcile-merge",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"pkg/a/a.go", "pkg/b/b.go", "pkg/c/c.go", "pkg/d/d.go", "pkg/e/e.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	writeRecoverTestGoModule(t, root)
	for _, dir := range []string{"pkg/a", "pkg/b", "pkg/c", "pkg/d", "pkg/e"} {
		writeRecoverTestPackage(t, root, dir)
	}
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if len(packet.CandidateAtoms) < 2 {
		t.Fatalf("expected multiple atoms, got %#v", packet.CandidateAtoms)
	}
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[0].ID, "--output", "reconciliation.json", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("first reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[1].ID, "--output", "reconciliation.json", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverReconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if len(report.CompletedAtoms) != 2 || !recoverAtomIDsContain(report.CompletedAtoms, packet.CandidateAtoms[0].ID) || !recoverAtomIDsContain(report.CompletedAtoms, packet.CandidateAtoms[1].ID) {
		t.Fatalf("expected merged completed atoms, got %#v", report)
	}
}

func TestRecoveryReconciliationLearningRejectsRankerStatePaths(t *testing.T) {
	root := t.TempDir()
	packet := recovery.DecompositionPacket{
		SchemaVersion: recovery.DecompositionPacketSchemaVersion,
		FailureID:     "failure-ranker-path",
		PacketID:      "packet-failure-ranker-path",
		CandidateAtoms: []recovery.CandidateAtom{{
			ID:            "recovery-001",
			RequiredPaths: []string{".idiom\\ranker\\weights.json"},
			RequiredTests: []string{"go test ./cmd/idiom"},
		}},
	}
	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification: recovery.ReconciliationVerification{
			Status:        recovery.ReconciliationVerificationPass,
			RequiredTests: []string{"go test ./cmd/idiom"},
			Commands: []recovery.ReconciliationCommandResult{{
				Command:  "go test ./cmd/idiom",
				Status:   recovery.ReconciliationVerificationPass,
				ExitCode: 0,
			}},
		},
	})
	if err != nil {
		t.Fatalf("build reconciliation: %v", err)
	}
	report := recordRecoverReconciliationLearning(root, packet, reconciliation)
	if report.State != comprehension.LearningStateRejected || report.Reason != comprehension.LearningReasonNoValidFiles {
		t.Fatalf("ranker state paths should be rejected as learning files, got %#v", report)
	}
	if len(report.Files) != 0 {
		t.Fatalf("ranker state paths must not be retained in learning report: %#v", report.Files)
	}
}

func TestRecoverReconcileRejectsOutsideOutputPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "recover-session-reconcile-outside",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	outsidePath := filepath.Join(root, "..", "outside-reconciliation.json")
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", "recovery-001", "--output", outsidePath, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover reconcile should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverReconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "workspace root") || report.NextAction != recovery.NextActionInspectFailure {
		t.Fatalf("unexpected outside output report: %#v", report)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("outside output should not be written, stat err=%v", err)
	}
}

func TestRecoverReconcileRejectsSymlinkedOutputParent(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	writeRecoverTestGoModule(t, root)
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package recover_test\n"), 0o644); err != nil {
		t.Fatalf("write scoped source: %v", err)
	}
	packet := writeRecoverTestPacket(t, root, "recover-session-reconcile-symlink", "packet.json")
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, "link-out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "packet.json", "--completed", packet.CandidateAtoms[0].ID, "--output", "link-out/reconciliation.json", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover reconcile should reject symlinked output parent, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverReconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "symlinked directories") {
		t.Fatalf("unexpected symlink output report: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "reconciliation.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink target output should not be written, stat err=%v", err)
	}
}

func TestRecoverReconcileDefaultOutputAvoidsSymlinkedPacketDirectory(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	writeRecoverTestGoModule(t, root)
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package recover_test\n"), 0o644); err != nil {
		t.Fatalf("write scoped source: %v", err)
	}
	packet := writeRecoverTestPacket(t, root, "recover-session-reconcile-symlink-default", "")
	outsideDir := t.TempDir()
	if _, _, err := recovery.WriteDecompositionPacket(outsideDir, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket outside: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "link-packet")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"reconcile", "--config", configPath, "--packet", "link-packet/packet.json", "--completed", packet.CandidateAtoms[0].ID, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recover reconcile exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverReconcileReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	wantPrefix := filepath.ToSlash(filepath.Join(".idiom", "recovery", packet.FailureID))
	if !strings.HasPrefix(report.ReconciliationPath, wantPrefix) {
		t.Fatalf("expected fallback under .idiom/recovery, got %#v", report)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "reconciliation.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink packet dir reconciliation should not be written outside root, stat err=%v", err)
	}
}

func TestRecoverPromoteRequiresExplicitAllowDirtyForDirtyPacket(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644); err != nil {
		t.Fatalf("write scoped source: %v", err)
	}
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "recover-session-dirty",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
		ChangedPaths:      []string{"allowed.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	partial := recovery.PartialStateForDirtyPaths([]string{"allowed.go"}, record.PartialState.ChangedPaths, record.SourceRun.RequiredEditPaths, nil, 1)
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC(), PartialState: partial})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", "briefs/recovery-001.md", "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover promote should block dirty packet, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverPromoteReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode promote report: %v\n%s", err, stdout.String())
	}
	if report.Status != "blocked" || !recoverGateBlocked(report.Gates, recovery.GateWorkspaceState) {
		t.Fatalf("expected workspace-state block: %#v", report)
	}
	if _, err := os.Stat(filepath.Join(root, "briefs", "recovery-001.md")); !os.IsNotExist(err) {
		t.Fatalf("blocked promotion should not write output, stat err=%v", err)
	}
}

func TestRecoverPromoteRejectsOutsideOutputPath(t *testing.T) {
	configPath := writePlanExecuteConfig(t)
	root := filepath.Dir(configPath)
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package allowed\n"), 0o644); err != nil {
		t.Fatalf("write scoped source: %v", err)
	}
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         "recover-session-outside",
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if _, _, err := recovery.WriteDecompositionPacket(root, packet, "packet.json"); err != nil {
		t.Fatalf("WriteDecompositionPacket: %v", err)
	}
	outsidePath := filepath.Join(root, "..", "outside-recovery.md")
	var stdout, stderr bytes.Buffer
	code := runRecoverMain([]string{"promote", "--config", configPath, "--packet", "packet.json", "--atom", "recovery-001", "--output", outsidePath, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("recover promote should reject outside output, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var report recoverPromoteReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode promote report: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(report.Error, "workspace root") || report.NextAction != recovery.NextActionHumanIntervention {
		t.Fatalf("unexpected outside output report: %#v", report)
	}
	if _, err := os.Stat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("outside output should not be written, stat err=%v", err)
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

func recoverGateBlocked(gates []recoverGateReport, name string) bool {
	for _, gate := range gates {
		if gate.Name == name && gate.Status == "block" {
			return true
		}
	}
	return false
}

func recoverAtomIDsContain(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func recoveryEvidenceRefsContain(refs []recovery.EvidenceRef, want recovery.EvidenceRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

func writeRecoverTestGoModule(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module recover.test\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func writeRecoverTestPackage(t *testing.T, root string, dir string) {
	t.Helper()
	fullDir := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatalf("mkdir test package: %v", err)
	}
	pkg := filepath.Base(dir)
	body := []byte("package " + pkg + "\n\nfunc Value() int { return 1 }\n")
	if err := os.WriteFile(filepath.Join(fullDir, pkg+".go"), body, 0o644); err != nil {
		t.Fatalf("write test package: %v", err)
	}
}

func writeRecoverFailedPlanExecuteRecord(t *testing.T, root string, sessionID string, requiredPaths []string) {
	t.Helper()
	state := planexecute.NewProgressState(sessionID, "make a change", "", requiredPaths, nil, time.Now().UTC())
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
}

func writeRecoverTestPacket(t *testing.T, root string, sessionID string, outputPath string) recovery.DecompositionPacket {
	t.Helper()
	record, err := recovery.BuildPlanExecuteFailureRecord(recovery.PlanExecuteFailureInput{
		SessionID:         sessionID,
		FailureKind:       "executor_failed",
		FailureReason:     "edit failed",
		RequiredEditPaths: []string{"allowed.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("BuildPlanExecuteFailureRecord: %v", err)
	}
	packet, err := recovery.BuildDecompositionPacket(record, recovery.DecompositionOptions{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("BuildDecompositionPacket: %v", err)
	}
	if outputPath != "" {
		if _, _, err := recovery.WriteDecompositionPacket(root, packet, outputPath); err != nil {
			t.Fatalf("WriteDecompositionPacket: %v", err)
		}
	}
	return packet
}

func initRecoverTestGitRepo(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "init")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}
