package brief
import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunBriefPreview(t *testing.T) {
	tmpDir := t.TempDir()
	briefPath := filepath.Join(tmpDir, "test-brief.md")
	briefContent := `# Test Brief

## Problem

A test problem.

## Files To Modify

| File | Change |
|------|--------|
| "a.go" | change a |
| "b.go" | change b |

## Verified Current State

Some state.

## Required Behavior

1. Step one.
2. Step two.

## Acceptance

test done.
`
	if err := os.WriteFile(briefPath, []byte(briefContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := runBriefPreview([]string{briefPath}, &stdout, &stderr, nil)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d: %s", exit, stderr.String())
	}
	out := stdout.String()
	if !bytes.Contains([]byte(out), []byte("Files To Modify: 2")) {
		t.Errorf("expected 'Files To Modify: 2' in output, got:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("Lint: OK")) {
		t.Errorf("expected 'Lint: OK' in output, got:\n%s", out)
	}
}
