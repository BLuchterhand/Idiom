package postflight

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Project postflight checks", func() {
	Describe("loading", func() {
		It("returns no checks for a project without check definitions", func() {
			checks, err := LoadProjectChecks(GinkgoT().TempDir())
			Expect(err).NotTo(HaveOccurred())
			Expect(checks).To(BeEmpty())
		})

		It("parses a shell rule", func() {
			root := GinkgoT().TempDir()
			writeProjectCheck(root, "lint.json", `{"name": "lint", "kind": "shell", "command": "true"}`)

			checks, err := LoadProjectChecks(root)
			Expect(err).NotTo(HaveOccurred())
			Expect(checks).To(HaveLen(1))
			Expect(checks[0].Name()).To(Equal("lint"))
		})

		It("uses the filename when a rule omits its name", func() {
			root := GinkgoT().TempDir()
			writeProjectCheck(root, "filename-fallback.json", `{"kind": "shell", "command": "true"}`)

			checks, err := LoadProjectChecks(root)
			Expect(err).NotTo(HaveOccurred())
			Expect(checks).To(HaveLen(1))
			Expect(checks[0].Name()).To(Equal("filename-fallback"))
		})

		It("rejects malformed JSON", func() {
			root := GinkgoT().TempDir()
			writeProjectCheck(root, "broken.json", `{"name":`)
			_, err := LoadProjectChecks(root)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("shell execution", func() {
		It("passes when the command exits zero", func() {
			requirePOSIX("relies on POSIX true")
			check := &projectCheckRunner{spec: ProjectCheck{Name: "always-ok", Kind: "shell", Command: "true"}}

			findings, err := check.Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(BeEmpty())
		})

		It("reports a non-zero exit as an error by default", func() {
			requirePOSIX("relies on POSIX false")
			check := &projectCheckRunner{spec: ProjectCheck{Name: "always-fail", Kind: "shell", Command: "false"}}

			findings, err := check.Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Message).To(ContainSubstring("exit"))
		})

		It("reports a non-zero exit as a warning when configured", func() {
			requirePOSIX("relies on POSIX false")
			failOnNonZero := false
			check := &projectCheckRunner{spec: ProjectCheck{
				Name: "advisory", Kind: "shell", Command: "false", FailOnNonZero: &failOnNonZero,
			}}

			findings, err := check.Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityWarning))
		})

		It("runs from the project root with substituted arguments", func() {
			root := GinkgoT().TempDir()
			GinkgoT().Setenv("GO_WANT_POSTFLIGHT_PROJECT_HELPER", "1")
			check := helperProjectCheck("cwd-and-args", "{project_root}", "{paths}")
			diff := Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}}

			findings, err := check.Run(diff, RunContext{ProjectRoot: root})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Message).To(And(
				ContainSubstring("cwd="+root),
				ContainSubstring("arg="+root),
				ContainSubstring("arg=a.go b.go"),
			))
		})

		It("includes stdout, stderr, and exit status in a failure", func() {
			GinkgoT().Setenv("GO_WANT_POSTFLIGHT_PROJECT_HELPER", "1")
			findings, err := helperProjectCheck("combined-output").Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Message).To(And(
				ContainSubstring("stdout marker"),
				ContainSubstring("stderr marker"),
				ContainSubstring("exit 7"),
			))
		})

		It("bounds combined command output", func() {
			GinkgoT().Setenv("GO_WANT_POSTFLIGHT_PROJECT_HELPER", "1")
			findings, err := helperProjectCheck("chatty").Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Message).To(ContainSubstring("... (truncated; rerun the check for full output)"))
			Expect(len(findings[0].Message)).To(BeNumerically("<=", shellOutputCap+128))
		})

		It("reports an empty command as a configuration error", func() {
			check := &projectCheckRunner{spec: ProjectCheck{Name: "broken", Kind: "shell"}}
			findings, err := check.Run(Diff{}, RunContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(findings).To(HaveLen(1))
			Expect(findings[0].Severity).To(Equal(SeverityError))
			Expect(findings[0].Message).To(ContainSubstring("empty"))
		})
	})

	It("reports the reserved ast_grep kind as a warning", func() {
		check := &projectCheckRunner{spec: ProjectCheck{Name: "future", Kind: "ast_grep"}}
		findings, err := check.Run(Diff{}, RunContext{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Severity).To(Equal(SeverityWarning))
	})

	It("reports an unknown kind as a configuration error", func() {
		check := &projectCheckRunner{spec: ProjectCheck{Name: "typo", Kind: "shel"}}
		findings, err := check.Run(Diff{}, RunContext{})
		Expect(err).NotTo(HaveOccurred())
		Expect(findings).To(HaveLen(1))
		Expect(findings[0].Severity).To(Equal(SeverityError))
	})

	It("substitutes known shell argument placeholders", func() {
		args := substituteShellArgs(
			[]string{"--root", "{project_root}", "--paths", "{paths}", "--unknown", "{not_a_placeholder}"},
			RunContext{ProjectRoot: "/proj"},
			Diff{Files: []FileChange{{Path: "a.go"}, {Path: "b.go"}}},
		)
		Expect(args).To(Equal([]string{
			"--root", "/proj", "--paths", "a.go b.go", "--unknown", "{not_a_placeholder}",
		}))
	})

	It("aggregates findings and computes a failed result", Label("development-scenario", "integration", "contract"), func() {
		diff := Diff{Files: []FileChange{
			{Path: "a.go", AddedLines: []AddedLine{{Line: 2, Body: "// <<QUESTION>>: marker"}}},
			{Path: "b.go"},
		}}
		result := Run(diff, RunContext{
			ProjectRoot: GinkgoT().TempDir(), RequiredEditPaths: []string{"a.go"},
		}, GenericChecks())

		Expect(result.Pass).To(BeFalse())
		checks := make([]string, 0, len(result.Findings))
		for _, finding := range result.Findings {
			checks = append(checks, finding.Check)
		}
		Expect(checks).To(And(
			ContainElement("unresolved_question_marker"),
			ContainElement("file_allowlist_mismatch"),
		))
	})

	It("filters checks by name", func() {
		checks := GenericChecks()
		filtered := FilterChecks(checks, []string{"narrating_test_comments"})
		Expect(filtered).To(HaveLen(len(checks) - 1))
		for _, check := range filtered {
			Expect(check.Name()).NotTo(Equal("narrating_test_comments"))
		}
	})

	It("records check execution errors as findings", func() {
		controller := gomock.NewController(GinkgoT())
		check := NewMockCheck(controller)
		check.EXPECT().Run(Diff{}, RunContext{}).Return(nil, &runtimeError{msg: "synthetic"}).Times(1)
		check.EXPECT().Name().Return("errors_always").Times(1)

		result := Run(Diff{}, RunContext{}, []Check{check})
		Expect(result.Pass).To(BeFalse())
		Expect(result.Findings).To(HaveLen(1))
		Expect(result.Findings[0].Severity).To(Equal(SeverityError))
	})
})

func TestPostflightProjectCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_POSTFLIGHT_PROJECT_HELPER") != "1" {
		return
	}

	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}

	args := os.Args[separator+1:]
	switch args[0] {
	case "cwd-and-args":
		workingDirectory, _ := os.Getwd()
		fmt.Println("cwd=" + workingDirectory)
		for _, arg := range args[1:] {
			fmt.Println("arg=" + arg)
		}
		os.Exit(3)
	case "combined-output":
		fmt.Fprintln(os.Stdout, "stdout marker")
		fmt.Fprintln(os.Stderr, "stderr marker")
		os.Exit(7)
	case "chatty":
		fmt.Fprintln(os.Stdout, strings.Repeat("x", shellOutputCap+256))
		os.Exit(1)
	}

	os.Exit(2)
}

type runtimeError struct{ msg string }

func (r *runtimeError) Error() string { return r.msg }

func writeProjectCheck(root, name, body string) {
	GinkgoHelper()
	directory := filepath.Join(root, ".idiom", "postflight", "checks")
	Expect(os.MkdirAll(directory, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, name), []byte(body), 0o644)).To(Succeed())
}

func helperProjectCheck(scenario string, arguments ...string) *projectCheckRunner {
	args := []string{"-test.run=TestPostflightProjectCommandHelperProcess", "--", scenario}
	args = append(args, arguments...)
	return &projectCheckRunner{spec: ProjectCheck{
		Name: scenario, Kind: "shell", Command: os.Args[0], Args: args,
	}}
}

func requirePOSIX(reason string) {
	GinkgoHelper()
	if runtime.GOOS == "windows" {
		Skip(reason)
	}
}
