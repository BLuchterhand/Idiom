package recovery

import (
	"os"
	"path/filepath"
	"testing"

	recoverycontract "idiom/internal/contracts/recovery"
)

func TestSafeSiblingReconciliationPathAllowsRecoveryPacketSibling(t *testing.T) {
	root := t.TempDir()
	packetPath := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
	if err := os.MkdirAll(filepath.Dir(packetPath), 0o755); err != nil {
		t.Fatal(err)
	}

	fullPath, relPath, ok := SafeSiblingReconciliationPath(root, packetPath)
	if !ok {
		t.Fatal("expected sibling reconciliation path")
	}
	if relPath != ".idiom/recovery/failure-1/reconciliation.json" {
		t.Fatalf("relPath = %q", relPath)
	}
	if fullPath != filepath.Join(root, ".idiom", "recovery", "failure-1", "reconciliation.json") {
		t.Fatalf("fullPath = %q", fullPath)
	}
}

func TestSafeSiblingReconciliationPathRejectsProtectedSibling(t *testing.T) {
	root := t.TempDir()
	packetPath := filepath.Join(root, "logs", "packet.json")
	if err := os.MkdirAll(filepath.Dir(packetPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := SafeSiblingReconciliationPath(root, packetPath); ok {
		t.Fatal("expected protected logs sibling to be rejected")
	}
}

func TestSafeSiblingReconciliationPathRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	packetPath := filepath.Join(outside, "packet.json")

	if _, _, ok := SafeSiblingReconciliationPath(root, packetPath); ok {
		t.Fatal("expected outside-root packet to be rejected")
	}
}

func TestSafePacketPathRejectsSymlinkedPacket(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, _, err := SafePacketPath(root, link); err == nil {
		t.Fatal("expected symlinked packet path to be rejected")
	}
}

func TestTryLoadSafeDecompositionPacketRejectsOutsideRootBeforeRead(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsidePacket := filepath.Join(outside, "packet.json")
	if err := os.WriteFile(outsidePacket, []byte(`{"schema_version":"`+recoverycontract.DecompositionPacketSchemaVersion+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := TryLoadSafeDecompositionPacket(root, outsidePacket); err == nil {
		t.Fatal("expected outside packet to be rejected")
	}
}

func TestTryLoadSafeDecompositionPacketRejectsSymlinkedPacket(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"schema_version":"`+recoverycontract.DecompositionPacketSchemaVersion+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, _, _, err := TryLoadSafeDecompositionPacket(root, link); err == nil {
		t.Fatal("expected symlinked packet path to be rejected")
	}
}

func TestSafeDefaultReconciliationPathUsesRecoveryArtifactDir(t *testing.T) {
	root := t.TempDir()
	fullPath, relPath, err := SafeDefaultReconciliationPath(root, "failure-1")
	if err != nil {
		t.Fatal(err)
	}
	if relPath != ".idiom/recovery/failure-1/reconciliation.json" {
		t.Fatalf("relPath = %q", relPath)
	}
	if fullPath != filepath.Join(root, ".idiom", "recovery", "failure-1", "reconciliation.json") {
		t.Fatalf("fullPath = %q", fullPath)
	}
}

func TestSafeReconciliationOutputPathRejectsProtectedExplicitPath(t *testing.T) {
	root := t.TempDir()
	packetPath := filepath.Join(root, ".idiom", "recovery", "failure-1", "packet.json")
	if err := os.MkdirAll(filepath.Dir(packetPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, _, err := SafeReconciliationOutputPath(root, packetPath, "failure-1", "logs/reconciliation.json"); err == nil {
		t.Fatal("expected protected explicit reconciliation path to be rejected")
	}
}

func TestSafeDecompositionOutputPathOwnsRecoveryArtifactPolicy(t *testing.T) {
	root := t.TempDir()
	failureID := "failure-1"

	if err := os.MkdirAll(filepath.Join(root, ".idiom", "recovery", failureID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".idiom", "recovery", failureID, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	cases := []struct {
		name       string
		outputPath string
		wantRel    string
		wantErr    bool
	}{
		{name: "default", wantRel: ".idiom/recovery/failure-1/packet.json"},
		{name: "custom packet", outputPath: ".idiom/recovery/failure-1/custom-packet.json", wantRel: ".idiom/recovery/failure-1/custom-packet.json"},
		{name: "parent escape", outputPath: "../escape.json", wantErr: true},
		{name: "absolute outside", outputPath: filepath.Join(outside, "packet.json"), wantErr: true},
		{name: "git protected", outputPath: ".git/packet.json", wantErr: true},
		{name: "logs protected", outputPath: "logs/packet.json", wantErr: true},
		{name: "env protected", outputPath: ".env", wantErr: true},
		{name: "control failure", outputPath: ".idiom/recovery/failure-1/failure.json", wantErr: true},
		{name: "control reconciliation", outputPath: ".idiom/recovery/failure-1/reconciliation.json", wantErr: true},
		{name: "symlink ancestor", outputPath: ".idiom/recovery/failure-1/linked/packet.json", wantErr: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, relPath, err := SafeDecompositionOutputPath(root, failureID, tt.outputPath)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for output %q", tt.outputPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("SafeDecompositionOutputPath(%q): %v", tt.outputPath, err)
			}
			if relPath != tt.wantRel {
				t.Fatalf("relPath = %q, want %q", relPath, tt.wantRel)
			}
		})
	}
}
