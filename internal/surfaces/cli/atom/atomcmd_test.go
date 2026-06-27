package atom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/workflows/atomintake"
)

func TestRunAtomIntakeMainEmitsWorkflowReportJSON(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: planned\n\n## Scope\n\n- `foo.go`\n")
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := RunIntakeMain([]string{"--spec", specPath, "--project-root", root, "--json"}, &stdout, &stderr, atomintake.Dependencies{})
	if code != 0 {
		t.Fatalf("RunIntakeMain code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}

	var report atomintake.IntakeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	if report.State != atomintakecontract.StateDispatchable || !report.Dispatchable {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestRunAtomIntakeMainTextOutputUsesWorkflowReport(t *testing.T) {
	root := t.TempDir()
	specPath := writeAtomSpec(t, root, "spec.md", "# Atom\n\nStatus: accepted\n")

	var stdout, stderr bytes.Buffer
	code := RunIntakeMain([]string{"--spec", specPath, "--project-root", root}, &stdout, &stderr, atomintake.Dependencies{})
	if code == 0 {
		t.Fatalf("accepted spec should block dispatch")
	}

	out := stdout.String()
	if !strings.Contains(out, "state: complete") || !strings.Contains(out, "dispatchable: false") {
		t.Fatalf("text report missing state/dispatchable fields: %s", out)
	}
}

func writeAtomSpec(t *testing.T, root string, name string, body string) string {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	return path
}
