package root

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMainHeadlessInvalidSessionRejectsBeforeModel(t *testing.T) {
	root := t.TempDir()
	configPath := writeMainConfigRoot(t, root)

	var stdout, stderr bytes.Buffer
	code := RunMain([]string{
		"run",
		"--config", configPath,
		"--prompt", "should not reach model",
		"--session", "../escape",
	}, &stdout, &stderr, Options{})

	if code != 2 {
		t.Fatalf("exit = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid run id") {
		t.Fatalf("stderr should explain invalid run id, got %q", stderr.String())
	}
	if matches, _ := filepath.Glob(filepath.Join(root, ".idiom", "runs", "*.completion.json")); len(matches) != 0 {
		t.Fatalf("invalid session should not write completion artifacts: %#v", matches)
	}
}
