package workspace

import "testing"

func TestNormalizeGraphOutputPathRejectsBackslashTraversal(t *testing.T) {
	root := t.TempDir()
	if got := normalizeGraphOutputPath(`..\secret.go`, root); got != "" {
		t.Fatalf("expected backslash traversal to be rejected, got %q", got)
	}
	if got := normalizeGraphOutputPath(`internal\capabilities\workspace\manager.go`, root); got != "internal/capabilities/workspace/manager.go" {
		t.Fatalf("expected backslash path to normalize, got %q", got)
	}
}
