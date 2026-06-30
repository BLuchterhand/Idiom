package codingrules

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rulescontract "idiom/internal/contracts/codingrules"
)

func TestCheckJSONReportsWrapper(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"check", "--project-root", root, "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	var report rulescontract.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	if len(report.Findings) != 1 {
		t.Fatalf("findings = %#v, want one", report.Findings)
	}
	if report.Findings[0].RuleID != rulescontract.RuleNoPassThroughWrapper {
		t.Fatalf("finding = %#v", report.Findings[0])
	}
}

func TestCheckFailOnWarning(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "wrapper.go", `package sample

func target(v string) string { return v }

func Wrapper(v string) string {
	return target(v)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"check", "--project-root", root, "--fail-on", "warning"}, &stdout, &stderr)

	if code != 3 {
		t.Fatalf("exit code = %d, want 3; stdout = %s stderr = %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "go.no_pass_through_wrapper") {
		t.Fatalf("stdout did not include rule ID: %s", stdout.String())
	}
}

func TestCheckRejectsUnknownFailOn(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"check", "--project-root", t.TempDir(), "--fail-on", "always"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--fail-on") {
		t.Fatalf("stderr did not explain fail-on error: %s", stderr.String())
	}
}

func writeTestFile(t *testing.T, root string, name string, body string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
