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
