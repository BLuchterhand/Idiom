package root

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunMainRoutesAtomCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	specPath := filepath.Join(root, "spec.md")
	if err := os.WriteFile(specPath, []byte("# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runMain([]string{"atom", "intake", "--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected routed atom intake exit 0, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
}
