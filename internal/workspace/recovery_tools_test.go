package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/config"
	"idiom/internal/recovery"
)

func TestRecoveryToolsInspectDecomposeAndStatus(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-001",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	inspect := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_inspect", SessionID: "sid-001"})
	if inspect.Error != "" {
		t.Fatalf("recovery_inspect returned error: %v\n%s", inspect.Error, inspect.Output)
	}
	var inspectReport recoveryInspectToolReport
	if err := json.Unmarshal([]byte(inspect.Output), &inspectReport); err != nil {
		t.Fatalf("decode inspect report: %v\n%s", err, inspect.Output)
	}
	if inspectReport.FailureID != record.FailureID || inspectReport.FailureClass != recovery.FailureClassExecutorEdit {
		t.Fatalf("unexpected inspect report: %#v", inspectReport)
	}
	if inspectReport.NextAction != recovery.NextActionReviewDecomposition {
		t.Fatalf("unexpected next action: %#v", inspectReport)
	}

	decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-001"})
	if decompose.Error != "" {
		t.Fatalf("recovery_decompose returned error: %v\n%s", decompose.Error, decompose.Output)
	}
	var decomposeReport recoveryDecomposeToolReport
	if err := json.Unmarshal([]byte(decompose.Output), &decomposeReport); err != nil {
		t.Fatalf("decode decompose report: %v\n%s", err, decompose.Output)
	}
	if decomposeReport.PacketPath == "" || decomposeReport.PacketSHA256 == "" || len(decomposeReport.CandidateAtoms) != 1 {
		t.Fatalf("unexpected decompose report: %#v", decomposeReport)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(decomposeReport.PacketPath))); err != nil {
		t.Fatalf("expected packet to be written: %v", err)
	}

	status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-001"})
	if status.Error != "" {
		t.Fatalf("recovery_status returned error: %v\n%s", status.Error, status.Output)
	}
	var statusReport recoveryStatusToolReport
	if err := json.Unmarshal([]byte(status.Output), &statusReport); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, status.Output)
	}
	if statusReport.Status != "ready" || statusReport.NextAction != recovery.NextActionReviewDecomposition {
		t.Fatalf("unexpected status report: %#v", statusReport)
	}
	for _, gate := range []string{recovery.GateWorkspaceState, recovery.GateBriefLint, recovery.GateAtomIntake, recovery.GateScope, recovery.GateRequiredTests} {
		if !stringSliceContains(statusReport.Gates, gate) {
			t.Fatalf("status report missing gate %q: %#v", gate, statusReport.Gates)
		}
	}
}

func TestRecoveryDecomposeToolRejectsSymlinkedOutputPath(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	if _, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-symlink-output",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, ".idiom", "recovery", "link-out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	decompose := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "recovery_decompose",
		SessionID:  "sid-symlink-output",
		OutputPath: ".idiom/recovery/link-out/packet.json",
	})
	if decompose.Error == "" || !strings.Contains(decompose.Error, "symlinked directories") {
		t.Fatalf("expected symlinked output rejection, got error=%q output=%s", decompose.Error, decompose.Output)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "packet.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink target output should not be written, stat err=%v", err)
	}
}

func TestRecoveryDecomposeToolRejectsSymlinkedDefaultOutputPath(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-default-symlink-output",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	recoveryDir := filepath.Join(root, ".idiom", "recovery")
	outsideRecoveryDir := filepath.Join(t.TempDir(), "recovery")
	if err := os.Rename(recoveryDir, outsideRecoveryDir); err != nil {
		t.Fatalf("move recovery dir outside workspace: %v", err)
	}
	if err := os.Symlink(outsideRecoveryDir, recoveryDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-default-symlink-output"})
	if decompose.Error == "" || !strings.Contains(decompose.Error, "symlinked directories") {
		t.Fatalf("expected symlinked default output rejection, got error=%q output=%s", decompose.Error, decompose.Output)
	}
	if _, err := os.Stat(filepath.Join(outsideRecoveryDir, record.FailureID, "packet.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink default target output should not be written, stat err=%v", err)
	}
}

func TestRecoveryDecomposeToolRejectsControlArtifactOutputPath(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-control-artifact-output",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	decompose := mgr.Execute(context.Background(), ToolRequest{
		Tool:       "recovery_decompose",
		SessionID:  "sid-control-artifact-output",
		OutputPath: filepath.ToSlash(filepath.Join(".idiom", "recovery", record.FailureID, "failure.json")),
	})
	if decompose.Error == "" || !strings.Contains(decompose.Error, "control artifact") {
		t.Fatalf("expected control artifact rejection, got error=%q output=%s", decompose.Error, decompose.Output)
	}
	if _, _, found, err := recovery.TryLoadPlanExecuteFailureRecord(root, "sid-control-artifact-output"); err != nil || !found {
		t.Fatalf("failure record should remain readable, found=%v err=%v", found, err)
	}
}

func TestRecoveryDecomposeToolRejectsPreexistingTempSymlink(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-temp-symlink-output",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	packetPath := filepath.Join(root, ".idiom", "recovery", record.FailureID, "packet.json")
	controlPath := filepath.Join(root, ".idiom", "recovery", record.FailureID, "failure.json")
	before, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read failure record before: %v", err)
	}
	if err := os.Symlink(controlPath, packetPath+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	decompose := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_decompose", SessionID: "sid-temp-symlink-output"})
	if decompose.Error == "" || !strings.Contains(decompose.Error, "write recovery decomposition packet") {
		t.Fatalf("expected temp symlink rejection, got error=%q output=%s", decompose.Error, decompose.Output)
	}
	after, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatalf("read failure record after: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("failure record should not be overwritten through temp symlink")
	}
}

func TestRecoveryStatusToolReflectsReconciliation(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-recovered",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
	if err != nil {
		t.Fatalf("write packet: %v", err)
	}
	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     recoveryToolTestVerification(packet, "recovery-001"),
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
		t.Fatalf("WriteReconciliation: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-recovered"})
	if status.Error != "" {
		t.Fatalf("recovery_status returned error: %v\n%s", status.Error, status.Output)
	}
	var report recoveryStatusToolReport
	if err := json.Unmarshal([]byte(status.Output), &report); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, status.Output)
	}
	if report.Status != "recovered" || report.NextAction != recovery.NextActionNone || report.Reconciliation == nil || len(report.CandidateAtoms) != 0 {
		t.Fatalf("expected recovered status, got %#v", report)
	}
}

func TestRecoveryStatusToolBlocksInvalidReconciliation(t *testing.T) {
	root := t.TempDir()
	writeRecoveryWorkspaceFile(t, root, "internal/app/foo.go", "package app\n")
	record, _, err := recovery.WritePlanExecuteFailureRecord(root, recovery.PlanExecuteFailureInput{
		SessionID:         "sid-invalid-reconciliation",
		Phase:             "executor",
		Status:            "failed",
		FailureKind:       "executor_failed",
		FailureReason:     "exact edit did not apply",
		RequiredEditPaths: []string{"internal/app/foo.go"},
		AllowedEditPaths:  []string{"internal/app/foo.go"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("write failure record: %v", err)
	}
	packet, _, err := recovery.WriteDecompositionPacketForFailureRecord(root, record, time.Now().UTC())
	if err != nil {
		t.Fatalf("write packet: %v", err)
	}
	verification := recoveryToolTestVerification(packet, "recovery-001")
	verification.Commands[0].ExitCode = 1
	reconciliation, err := recovery.BuildReconciliation(packet, recovery.ReconciliationOptions{
		CompletedAtomIDs: []string{"recovery-001"},
		Verification:     verification,
	})
	if err != nil {
		t.Fatalf("BuildReconciliation: %v", err)
	}
	if _, _, err := recovery.WriteReconciliation(root, reconciliation, ""); err != nil {
		t.Fatalf("WriteReconciliation: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	status := mgr.Execute(context.Background(), ToolRequest{Tool: "recovery_status", SessionID: "sid-invalid-reconciliation"})
	if status.Error != "" {
		t.Fatalf("recovery_status returned error: %v\n%s", status.Error, status.Output)
	}
	var report recoveryStatusToolReport
	if err := json.Unmarshal([]byte(status.Output), &report); err != nil {
		t.Fatalf("decode status report: %v\n%s", err, status.Output)
	}
	if report.Status != "blocked" || !stringSliceContainsPrefix(report.Blockers, "recovery_reconciliation_missing_required_tests") {
		t.Fatalf("expected invalid reconciliation blocker, got %#v", report)
	}
}

func TestRecoveryToolProfileParity(t *testing.T) {
	for _, tool := range []string{"recovery_inspect", "recovery_status"} {
		if decision := ToolAllowedForProfile(tool, ToolProfileDiscovery); !decision.Allowed {
			t.Fatalf("%s should be allowed in discovery profile: %#v", tool, decision)
		}
		if decision := ToolAllowedForProfile(tool, ToolProfileMCPDiscovery); !decision.Allowed {
			t.Fatalf("%s should be allowed in mcp_discovery profile: %#v", tool, decision)
		}
	}
	if decision := ToolAllowedForProfile("recovery_decompose", ToolProfileDiscovery); decision.Allowed || !strings.Contains(decision.Reason, "mutation") {
		t.Fatalf("recovery_decompose should be denied by discovery profile as a writer, got %#v", decision)
	}
}

func recoveryToolTestVerification(packet recovery.DecompositionPacket, atomIDs ...string) recovery.ReconciliationVerification {
	completed := map[string]bool{}
	for _, id := range atomIDs {
		completed[id] = true
	}
	seen := map[string]bool{}
	var required []string
	var commands []recovery.ReconciliationCommandResult
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
			commands = append(commands, recovery.ReconciliationCommandResult{
				Command:  test,
				Status:   recovery.ReconciliationVerificationPass,
				ExitCode: 0,
			})
		}
	}
	return recovery.ReconciliationVerification{
		Status:        recovery.ReconciliationVerificationPass,
		RequiredTests: required,
		Commands:      commands,
	}
}

func writeRecoveryWorkspaceFile(t *testing.T, root string, rel string, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
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
