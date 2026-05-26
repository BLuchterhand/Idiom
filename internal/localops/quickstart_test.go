package localops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/workspace"
)

func TestQuickstartPacketEvidenceRequiresStableIdentifiers(t *testing.T) {
	packet := PacketEvidence(workspace.ToolResult{}, map[string]any{
		"pack_id":         "pack-1",
		"index_id":        "index-1",
		"fresh":           true,
		"candidate_files": []any{map[string]any{"path": "main.go"}},
	}, "localize quickstart")
	if packet.Status != "pass" || packet.PacketID != "pack-1" || packet.FreshnessState != "fresh" {
		t.Fatalf("expected passing packet evidence, got %#v", packet)
	}

	blocked := PacketEvidence(workspace.ToolResult{}, map[string]any{}, "localize quickstart")
	if blocked.Status != "blocked" || blocked.BlockerReason == "" {
		t.Fatalf("expected missing packet fields to block, got %#v", blocked)
	}
}

func TestQuickstartCleanupOnlyRemovesContextViewArtifacts(t *testing.T) {
	root := t.TempDir()
	viewDir := filepath.Join(root, ".idiom", "comprehension", "views")
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatalf("stage view dir: %v", err)
	}
	viewPath := filepath.Join(viewDir, "pack.json")
	if err := os.WriteFile(viewPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write view artifact: %v", err)
	}

	cleanup := QuickstartCleanupEvidence(viewPath, "remove-smoke-artifacts", "pack-1", root, true)
	if cleanup.Status != "pass" || cleanup.RemovedArtifactCount != 1 {
		t.Fatalf("expected removable view artifact cleanup, got %#v", cleanup)
	}
	if _, err := os.Stat(viewPath); !os.IsNotExist(err) {
		t.Fatalf("expected view artifact removed, stat err=%v", err)
	}

	outside := QuickstartCleanupEvidence(filepath.Join(root, "README.md"), "remove-smoke-artifacts", "pack-1", root, true)
	if outside.Status != "blocked" || outside.BlockerReason == "" {
		t.Fatalf("expected non-view artifact to block cleanup, got %#v", outside)
	}
}

func TestQuickstartCleanupReportOnlyRetainsLocalState(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".idiom", "comprehension")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("stage state dir: %v", err)
	}
	cleanup := QuickstartCleanupEvidence("", "report-only", "pack-1", root, true)
	if cleanup.Status != "retained_by_policy" || cleanup.ExitStatus != "not_run" || cleanup.RetainedStatePolicy == "" {
		t.Fatalf("expected report-only cleanup to retain state, got %#v", cleanup)
	}
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("report-only cleanup should preserve comprehension state: %v", err)
	}
}

func TestSuggestedVerificationCommandsAreSanitizedAndBounded(t *testing.T) {
	commands := SuggestedVerificationCommands(map[string]any{
		"tests": []any{
			map[string]any{"command": "go test ./..."},
			map[string]any{"command": "API_TOKEN=abc go test ./internal/localops"},
			map[string]any{"command": "go test ./cmd/idiom"},
			map[string]any{"command": "go test ./internal/agent"},
		},
	})
	if len(commands) != 3 {
		t.Fatalf("expected first three commands only, got %#v", commands)
	}
	if commands[0] != "go test ./..." || commands[1] != "[redacted] go test ./internal/localops" {
		t.Fatalf("unexpected sanitized commands: %#v", commands)
	}
	if VerificationLabel(commands[0]) != "go-test" {
		t.Fatalf("expected go-test label for %q", commands[0])
	}
}

func TestFinalizeQuickstartProductEvidenceBlocksForbiddenContent(t *testing.T) {
	smoke := QuickstartSmoke{
		Status: "pass",
		ProductEvidenceSafeOutput: ActivationProductEvidence{
			Status:                 "pending",
			ForbiddenFieldCheck:    "pending",
			RedactionStatus:        "pending",
			P7_11Linkage:           "deferred_to_p7.11_export",
			P9_6Linkage:            "activation_report_input",
			HostedTelemetryDefault: false,
		},
		FirstUsefulPacket: ActivationPacketEvidence{
			Task: ActivationTaskEvidence{RedactionStatus: "hashed"},
		},
		PrivacyRedactionStatus: ActivationPrivacyEvidence{
			Status:              "pass",
			ForbiddenFieldCheck: "pass",
		},
	}
	FinalizeQuickstartProductEvidence(&smoke)
	if smoke.ProductEvidenceSafeOutput.Status != "pass" || smoke.ProductEvidenceSafeOutput.ArtifactHash == "" {
		t.Fatalf("expected safe product evidence to pass, got %#v", smoke.ProductEvidenceSafeOutput)
	}
	if want, err := quickstartProductEvidenceHash(smoke); err != nil || smoke.ProductEvidenceSafeOutput.ArtifactHash != want {
		t.Fatalf("safe product evidence hash should describe final body, got=%q want=%q err=%v", smoke.ProductEvidenceSafeOutput.ArtifactHash, want, err)
	}

	smoke.FirstUsefulPacket.PacketID = "/Users/ben/private-repo"
	FinalizeQuickstartProductEvidence(&smoke)
	if smoke.Status != "blocked" || smoke.ProductEvidenceSafeOutput.ForbiddenFieldCheck == "pass" {
		t.Fatalf("expected forbidden evidence to block, got %#v", smoke)
	}
	if want, err := quickstartProductEvidenceHash(smoke); err != nil || smoke.ProductEvidenceSafeOutput.ArtifactHash != want {
		t.Fatalf("blocked product evidence hash should describe final body, got=%q want=%q err=%v", smoke.ProductEvidenceSafeOutput.ArtifactHash, want, err)
	}
	body, err := json.Marshal(smoke)
	if err != nil {
		t.Fatalf("marshal smoke: %v", err)
	}
	if strings.Contains(string(body), "/Users/ben/private-repo") {
		t.Fatalf("blocked quickstart evidence should redact forbidden value: %s", string(body))
	}
}
