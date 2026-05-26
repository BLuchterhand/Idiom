package readiness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProfilesRejectsUnknownProfile(t *testing.T) {
	_, err := BuildProfiles(filepath.Join(t.TempDir(), "missing-config.json"), "unknown", nil)
	if err == nil {
		t.Fatal("expected unknown profile to fail")
	}
	if !IsProfileSelectionError(err) {
		t.Fatalf("expected profile selection error, got %T %v", err, err)
	}
}

func TestWorkspaceRootFromConfigResolvesRelativeRoot(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".idiom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"workspace":{"root":".."}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := WorkspaceRootFromConfig(configPath)
	if got != filepath.Clean(dir) {
		t.Fatalf("WorkspaceRootFromConfig() = %q, want %q", got, filepath.Clean(dir))
	}
}
