package postflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoBuildPassesReportsCompileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gobuildpasses_test\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	src := "package main\n\nfunc main() { undefinedFunc() }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	diff := Diff{Files: []FileChange{{Path: "main.go"}}}
	findings, err := GoBuildPasses{}.Run(diff, RunContext{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatalf("expected at least one finding for broken Go file")
	}
	if findings[0].Check != "go_build_passes" {
		t.Fatalf("finding[0].Check = %q, want go_build_passes", findings[0].Check)
	}
	if findings[0].Severity != SeverityError {
		t.Fatalf("finding[0].Severity = %q, want error", findings[0].Severity)
	}
	if !strings.Contains(strings.ToLower(findings[0].Message), "undefined") {
		t.Fatalf("finding[0].Message = %q, expected to contain 'undefined'", findings[0].Message)
	}
}

func TestGoBuildPassesNoOpsForNonGoDiff(t *testing.T) {
	diff := Diff{Files: []FileChange{{Path: "README.md"}}}
	findings, err := GoBuildPasses{}.Run(diff, RunContext{ProjectRoot: "/tmp"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if findings != nil {
		t.Fatalf("expected nil findings for non-Go diff, got %v", findings)
	}
}
