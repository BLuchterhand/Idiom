package planexecute

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	plancontract "idiom/internal/contracts/plan"
)

func TestVerificationPlannerDedupeExplicitAndInferredCommands(t *testing.T) {
	root := t.TempDir()
	writeVerificationPlannerFile(t, root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(t, root, "pkg/feature/feature.go", "package feature\n")

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "After editing, run `go test ./pkg/feature`.",
		ChangedPaths:  []string{"pkg/feature/feature.go", "pkg/feature/feature_test.go"},
	})

	if len(plan.Commands) != 1 {
		t.Fatalf("commands = %#v, want one deduped command", plan.Commands)
	}

	if got := plan.Commands[0]; got.Command != "go test ./pkg/feature" || got.WorkingDir != "" || got.Source != plancontract.VerificationCommandSourcePrompt {
		t.Fatalf("unexpected command = %#v", got)
	}

	if got := plan.Commands[0].Argv; len(got) != 3 || got[0] != "go" || got[1] != "test" || got[2] != "./pkg/feature" {
		t.Fatalf("unexpected argv = %#v", got)
	}

	if len(plan.Skips) != 0 || len(plan.Rejects) != 0 {
		t.Fatalf("unexpected skips/rejects: skips=%#v rejects=%#v", plan.Skips, plan.Rejects)
	}
}

func TestVerificationPlannerRejectsUnsafePromptCommand(t *testing.T) {
	root := t.TempDir()
	writeVerificationPlannerFile(t, root, "go.mod", "module example.com/root\n\ngo 1.22\n")

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "Required verification: run `go test ./pkg; rm -rf /`.",
	})

	if len(plan.Commands) != 0 {
		t.Fatalf("unsafe prompt command must not be executable: %#v", plan.Commands)
	}

	if len(plan.Rejects) != 1 || plan.Rejects[0].Reason != plancontract.VerificationRejectUnsupportedCmd {
		t.Fatalf("unsafe prompt command should be a structured reject, got %#v", plan.Rejects)
	}
}

func TestVerificationPlannerAcceptsRequestedPytestCommands(t *testing.T) {
	root := t.TempDir()

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt: strings.Join([]string{
			"Required verification command: run `pytest tests/test_query_handler.py` after edits.",
			"After implementation, run `python -m pytest tests/test_memory.py`.",
		}, "\n"),
	})

	if len(plan.Commands) != 2 {
		t.Fatalf("commands = %#v, want requested pytest commands", plan.Commands)
	}

	if got := plan.Commands[0]; got.Command != "pytest tests/test_query_handler.py" || got.Source != plancontract.VerificationCommandSourcePrompt {
		t.Fatalf("unexpected pytest command = %#v", got)
	}
	if got := plan.Commands[1]; got.Command != "python -m pytest tests/test_memory.py" || got.Source != plancontract.VerificationCommandSourcePrompt {
		t.Fatalf("unexpected python -m pytest command = %#v", got)
	}
	if len(plan.Rejects) != 0 {
		t.Fatalf("safe pytest commands should not be rejected: %#v", plan.Rejects)
	}
}

func TestVerificationPlannerRejectsUnsafePytestCommand(t *testing.T) {
	root := t.TempDir()

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "Required verification command: run `pytest tests/test_query_handler.py; rm -rf /` after edits.",
	})

	if len(plan.Commands) != 0 {
		t.Fatalf("unsafe pytest command must not be executable: %#v", plan.Commands)
	}
	if len(plan.Rejects) != 1 || plan.Rejects[0].Reason != plancontract.VerificationRejectUnsupportedCmd {
		t.Fatalf("unsafe pytest command should be a structured reject, got %#v", plan.Rejects)
	}
}

func TestVerificationPlannerUsesClosestGoModuleRoot(t *testing.T) {
	root := t.TempDir()
	writeVerificationPlannerFile(t, root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(t, root, "pkg/root/root.go", "package root\n")
	writeVerificationPlannerFile(t, root, "nested/go.mod", "module example.com/nested\n\ngo 1.22\n")
	writeVerificationPlannerFile(t, root, "nested/pkg/feature/feature.go", "package feature\n")

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		ChangedPaths: []string{
			"pkg/root/root.go",
			"nested/pkg/feature/feature.go",
		},
	})

	got := map[string]plancontract.VerificationCommand{}
	for _, command := range plan.Commands {
		got[command.Path] = command
	}

	if command := got["pkg/root/root.go"]; command.Command != "go test ./pkg/root" || command.WorkingDir != "" {
		t.Fatalf("root command = %#v", command)
	}

	if command := got["nested/pkg/feature/feature.go"]; command.Command != "go test ./pkg/feature" || command.WorkingDir != "nested" {
		t.Fatalf("nested command = %#v", command)
	}
}

func TestVerificationPlannerDoesNotInferFromGoWorkAlone(t *testing.T) {
	root := t.TempDir()
	writeVerificationPlannerFile(t, root, "go.work", "go 1.22\n")
	writeVerificationPlannerFile(t, root, "pkg/feature/feature.go", "package feature\n")

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		ChangedPaths:  []string{"pkg/feature/feature.go"},
	})

	if len(plan.Commands) != 0 {
		t.Fatalf("go.work without a go.mod must not infer verification commands: %#v", plan.Commands)
	}

	if !collectVerificationPlannerSkipReasons(plan.Skips).contains(plancontract.VerificationSkipNoGoModuleRoot) {
		t.Fatalf("missing no-module skip, got %#v", plan.Skips)
	}
}

func TestVerificationPlannerSkipsUnsafeAndUnsupportedPaths(t *testing.T) {
	root := t.TempDir()
	writeVerificationPlannerFile(t, root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(t, root, "pkg/header.go", "// Code generated by fixture. DO NOT EDIT.\npackage pkg\n")

	plan := PlanVerification(VerificationPlannerInput{
		WorkspaceRoot: root,
		ExplicitCommands: []string{
			"go test ./pkg",
			"go test ./pkg; rm -rf /",
		},
		ChangedPaths: []string{
			"vendor/pkg/a.go",
			"generated/a.go",
			"pkg/header.go",
			filepath.Join(root, "abs.go"),
			"../outside.go",
			"pkg//bad.go",
			"README.md",
		},
	})

	reasons := collectVerificationPlannerSkipReasons(plan.Skips)
	for _, want := range []string{
		plancontract.VerificationSkipVendorPath,
		plancontract.VerificationSkipGeneratedPath,
		plancontract.VerificationSkipGeneratedFile,
		plancontract.VerificationSkipAbsolutePath,
		plancontract.VerificationSkipParentTraversal,
		plancontract.VerificationSkipEmptySegment,
		plancontract.VerificationSkipNonGoPath,
	} {
		if !reasons.contains(want) {
			t.Fatalf("missing skip reason %q in %#v", want, plan.Skips)
		}
	}

	if len(plan.Commands) != 1 || plan.Commands[0].Command != "go test ./pkg" {
		t.Fatalf("expected only safe explicit command, got %#v", plan.Commands)
	}

	if len(plan.Rejects) != 1 || plan.Rejects[0].Reason != plancontract.VerificationRejectUnsupportedCmd {
		t.Fatalf("expected structured reject for unsafe command, got %#v", plan.Rejects)
	}
}

type verificationPlannerSkipReasons []string

func (reasons verificationPlannerSkipReasons) contains(reason string) bool {
	return slices.Contains(reasons, reason)
}

func collectVerificationPlannerSkipReasons(skips []plancontract.VerificationSkip) verificationPlannerSkipReasons {
	reasons := make(verificationPlannerSkipReasons, 0, len(skips))
	for _, skip := range skips {
		reasons = append(reasons, skip.Reason)
	}

	return reasons
}

func writeVerificationPlannerFile(t *testing.T, root string, rel string, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
