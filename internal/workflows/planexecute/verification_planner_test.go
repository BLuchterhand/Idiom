package planexecute

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	plancontract "idiom/internal/contracts/plan"
)

var _ = ginkgo.It("verification planner dedupe explicit and inferred commands", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeVerificationPlannerFile(root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(root, "pkg/feature/feature.go", "package feature\n")

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "After editing, run `go test ./pkg/feature`.",
		ChangedPaths:  []string{"pkg/feature/feature.go", "pkg/feature/feature_test.go"},
	})
	gomega.Expect(len(plan.Commands) != 1).To(gomega.BeFalse(), fmt.Sprintf("commands = %#v, want one deduped command", plan.Commands))

	if got := plan.Commands[0]; got.Command != "go test ./pkg/feature" || got.WorkingDir != "" || got.Source != plancontract.VerificationCommandSourcePrompt {
		ginkgo.Fail(fmt.Sprintf("unexpected command = %#v", got))
	}

	if got := plan.Commands[0].Argv; len(got) != 3 || got[0] != "go" || got[1] != "test" || got[2] != "./pkg/feature" {
		ginkgo.Fail(fmt.Sprintf("unexpected argv = %#v", got))
	}
	gomega.Expect(plan.Skips).To(gomega.HaveLen(0), fmt.Sprintf("unexpected skips/rejects: skips=%#v rejects=%#v", plan.Skips, plan.Rejects))
	gomega.Expect(plan.Rejects).To(gomega.HaveLen(0), fmt.Sprintf("unexpected skips/rejects: skips=%#v rejects=%#v", plan.Skips, plan.Rejects))
})

var _ = ginkgo.It("verification planner rejects unsafe prompt command", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeVerificationPlannerFile(root, "go.mod", "module example.com/root\n\ngo 1.22\n")

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "Required verification: run `go test ./pkg; rm -rf /`.",
	})
	gomega.Expect(len(plan.Commands) != 0).To(gomega.BeFalse(), fmt.Sprintf("unsafe prompt command must not be executable: %#v", plan.Commands))
	gomega.Expect(plan.Rejects).To(gomega.HaveLen(1), fmt.Sprintf("unsafe prompt command should be a structured reject, got %#v", plan.Rejects))
	gomega.Expect(plan.Rejects[0].Reason).To(gomega.Equal(plancontract.VerificationRejectUnsupportedCmd), fmt.Sprintf("unsafe prompt command should be a structured reject, got %#v", plan.Rejects))
})

var _ = ginkgo.It("verification planner accepts requested pytest commands", func() {
	root := ginkgo.GinkgoT().TempDir()

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt: strings.Join([]string{
			"Required verification command: run `pytest tests/test_query_handler.py` after edits.",
			"After implementation, run `python -m pytest tests/test_memory.py`.",
		}, "\n"),
	})
	gomega.Expect(len(plan.Commands) != 2).To(gomega.BeFalse(), fmt.Sprintf("commands = %#v, want requested pytest commands", plan.Commands))

	if got := plan.Commands[0]; got.Command != "pytest tests/test_query_handler.py" || got.Source != plancontract.VerificationCommandSourcePrompt {
		ginkgo.Fail(fmt.Sprintf("unexpected pytest command = %#v", got))
	}
	if got := plan.Commands[1]; got.Command != "python -m pytest tests/test_memory.py" || got.Source != plancontract.VerificationCommandSourcePrompt {
		ginkgo.Fail(fmt.Sprintf("unexpected python -m pytest command = %#v", got))
	}
	gomega.Expect(len(plan.Rejects) != 0).To(gomega.BeFalse(), fmt.Sprintf("safe pytest commands should not be rejected: %#v", plan.Rejects))
})

var _ = ginkgo.It("verification planner rejects unsafe pytest command", func() {
	root := ginkgo.GinkgoT().TempDir()

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot: root,
		Prompt:        "Required verification command: run `pytest tests/test_query_handler.py; rm -rf /` after edits.",
	})
	gomega.Expect(len(plan.Commands) != 0).To(gomega.BeFalse(), fmt.Sprintf("unsafe pytest command must not be executable: %#v", plan.Commands))
	gomega.Expect(plan.Rejects).To(gomega.HaveLen(1), fmt.Sprintf("unsafe pytest command should be a structured reject, got %#v", plan.Rejects))
	gomega.Expect(plan.Rejects[0].Reason).To(gomega.Equal(plancontract.VerificationRejectUnsupportedCmd), fmt.Sprintf("unsafe pytest command should be a structured reject, got %#v", plan.Rejects))
})

var _ = ginkgo.It("verification planner uses closest go module root", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeVerificationPlannerFile(root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(root, "pkg/root/root.go", "package root\n")
	writeVerificationPlannerFile(root, "nested/go.mod", "module example.com/nested\n\ngo 1.22\n")
	writeVerificationPlannerFile(root, "nested/pkg/feature/feature.go", "package feature\n")

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
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
		ginkgo.Fail(fmt.Sprintf("root command = %#v", command))
	}

	if command := got["nested/pkg/feature/feature.go"]; command.Command != "go test ./pkg/feature" || command.WorkingDir != "nested" {
		ginkgo.Fail(fmt.Sprintf("nested command = %#v", command))
	}
})

var _ = ginkgo.It("verification planner does not infer from go work alone", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeVerificationPlannerFile(root, "go.work", "go 1.22\n")
	writeVerificationPlannerFile(root, "pkg/feature/feature.go", "package feature\n")

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
		WorkspaceRoot: root,
		ChangedPaths:  []string{"pkg/feature/feature.go"},
	})
	gomega.Expect(len(plan.Commands) != 0).To(gomega.BeFalse(), fmt.Sprintf("go.work without a go.mod must not infer verification commands: %#v", plan.Commands))
	gomega.Expect(collectVerificationPlannerSkipReasons(plan.Skips).contains(plancontract.VerificationSkipNoGoModuleRoot)).To(gomega.BeTrue(), fmt.Sprintf("missing no-module skip, got %#v", plan.Skips))
})

var _ = ginkgo.It("verification planner skips unsafe and unsupported paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	writeVerificationPlannerFile(root, "go.mod", "module example.com/root\n\ngo 1.22\n")
	writeVerificationPlannerFile(root, "pkg/header.go", "// Code generated by fixture. DO NOT EDIT.\npackage pkg\n")

	plan := NewVerificationPlanner(root).Plan(VerificationPlannerInput{
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
			`C:\repo\abs.go`,
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
		gomega.Expect(reasons.contains(want)).To(gomega.BeTrue(), fmt.Sprintf("missing skip reason %q in %#v", want, plan.Skips))

	}
	gomega.Expect(plan.Commands).To(gomega.HaveLen(1), fmt.Sprintf("expected only safe explicit command, got %#v", plan.Commands))
	gomega.Expect(plan.Commands[0].Command).To(gomega.Equal("go test ./pkg"), fmt.Sprintf("expected only safe explicit command, got %#v", plan.Commands))
	gomega.Expect(plan.Rejects).To(gomega.HaveLen(1), fmt.Sprintf("expected structured reject for unsafe command, got %#v", plan.Rejects))
	gomega.Expect(plan.Rejects[0].Reason).To(gomega.Equal(plancontract.VerificationRejectUnsupportedCmd), fmt.Sprintf("expected structured reject for unsafe command, got %#v", plan.Rejects))
})

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

func writeVerificationPlannerFile(root string, rel string, body string) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write %s: %v", path, err))
	}
}
