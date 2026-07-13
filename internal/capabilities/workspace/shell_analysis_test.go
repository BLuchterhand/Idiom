package workspace

import (
	"fmt"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
)

var _ = ginkgo.Describe("analyze shell command classifies inspection and verification", func() {

	tests := []struct {
		name             string
		command          string
		broadInspection  bool
		narrowing        bool
		buildOrTest      bool
		verification     ShellVerificationFamily
		fullSuite        bool
		hasControlSyntax bool
		sideEffect       autonomy.SideEffectClass
	}{
		{
			name:            "broad inspection",
			command:         "find . -maxdepth 2",
			broadInspection: true,
			sideEffect:      autonomy.SideEffectReadOnly,
		},
		{
			name:       "narrowing inspection",
			command:    "rg TODO internal",
			narrowing:  true,
			sideEffect: autonomy.SideEffectReadOnly,
		},
		{
			name:         "go test is read only full suite verification",
			command:      "go test ./...",
			buildOrTest:  true,
			verification: ShellVerificationFullSuite,
			fullSuite:    true,
			sideEffect:   autonomy.SideEffectReadOnly,
		},
		{
			name:       "git status is read only verification",
			command:    "git status --short",
			sideEffect: autonomy.SideEffectReadOnly,
		},
		{
			name:         "git diff check is read only verification",
			command:      "git diff --check -- README.md",
			verification: ShellVerificationCheck,
			sideEffect:   autonomy.SideEffectReadOnly,
		},
		{
			name:       "git diff output flag is unknown",
			command:    "git diff --output=report.txt",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:       "git diff separated output flag is unknown",
			command:    "git diff --output report.txt",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:       "prefix is not enough",
			command:    "git statusx",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:       "mutating command is additive",
			command:    "touch x",
			sideEffect: autonomy.SideEffectAdditive,
		},
		{
			name:             "pipe is control syntax",
			command:          "go test ./... | head",
			buildOrTest:      true,
			hasControlSyntax: true,
			sideEffect:       autonomy.SideEffectUnknown,
		},
		{
			name:             "compound shell is control syntax",
			command:          "git status && rm -rf tmp",
			hasControlSyntax: true,
			sideEffect:       autonomy.SideEffectDestructive,
		},
		{
			name:             "redirection is control syntax",
			command:          "git diff > report.txt",
			hasControlSyntax: true,
			sideEffect:       autonomy.SideEffectUnknown,
		},
		{
			name:         "quoted pipe is not control syntax",
			command:      "go test ./internal/runtime/agent -run 'TestA|TestB'",
			buildOrTest:  true,
			verification: ShellVerificationTest,
			sideEffect:   autonomy.SideEffectReadOnly,
		},
		{
			name:         "bash syntax check is script verification",
			command:      "bash -n scripts/run-harness.sh",
			verification: ShellVerificationScriptSyntax,
			sideEffect:   autonomy.SideEffectUnknown,
		},
		{
			name:         "shellcheck is script verification",
			command:      "shellcheck scripts/run-harness.sh",
			verification: ShellVerificationScriptSyntax,
			sideEffect:   autonomy.SideEffectUnknown,
		},
		{
			name:         "typescript compiler is generic verification",
			command:      "tsc --noEmit",
			verification: ShellVerificationGeneric,
			sideEffect:   autonomy.SideEffectUnknown,
		},
		{
			name:         "ruff check is check verification",
			command:      "ruff check .",
			verification: ShellVerificationCheck,
			sideEffect:   autonomy.SideEffectUnknown,
		},
		{
			name:         "flagged Python unittest is read only verification",
			command:      "python3 -B -m unittest",
			buildOrTest:  true,
			verification: ShellVerificationFullSuite,
			fullSuite:    true,
			sideEffect:   autonomy.SideEffectReadOnly,
		},
		{
			name:         "flagged Python build remains mutation unknown",
			command:      "python3 -B -m build",
			buildOrTest:  true,
			verification: ShellVerificationBuild,
			sideEffect:   autonomy.SideEffectUnknown,
		},
		{
			name:       "Python command argv cannot impersonate verification",
			command:    `python3 -c "print('ready')" -m unittest`,
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:       "Python script argv cannot impersonate verification",
			command:    "python3 scripts/format.py -m unittest",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:       "Python version argv cannot impersonate read only verification",
			command:    "python3 -V -m unittest",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:         "eslint is generic verification",
			command:      "eslint src",
			verification: ShellVerificationGeneric,
			sideEffect:   autonomy.SideEffectUnknown,
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			got := AnalyzeShellCommand(tt.command)
			gomega.Expect(got.BroadInspection).To(gomega.Equal(tt.broadInspection), fmt.Sprintf("BroadInspection = %t, want %t", got.BroadInspection, tt.broadInspection))
			gomega.Expect(got.Narrowing).To(gomega.Equal(tt.narrowing), fmt.Sprintf("Narrowing = %t, want %t", got.Narrowing, tt.narrowing))
			gomega.Expect(got.BuildOrTest).To(gomega.Equal(tt.buildOrTest), fmt.Sprintf("BuildOrTest = %t, want %t", got.BuildOrTest, tt.buildOrTest))
			gomega.Expect(got.Verification).To(gomega.Equal(tt.verification), fmt.Sprintf("Verification = %q, want %q", got.Verification, tt.verification))
			gomega.Expect(got.FullSuite).To(gomega.Equal(tt.fullSuite), fmt.Sprintf("FullSuite = %t, want %t", got.FullSuite, tt.fullSuite))
			gomega.Expect(got.HasControlSyntax).To(gomega.Equal(tt.hasControlSyntax), fmt.Sprintf("HasControlSyntax = %t, want %t", got.HasControlSyntax, tt.hasControlSyntax))
			gomega.Expect(got.SideEffect).To(gomega.Equal(tt.sideEffect), fmt.Sprintf("SideEffect = %q, want %q", got.SideEffect, tt.sideEffect))

		})
	}
})
var _ = ginkgo.Describe("shell analysis", func() {
	ginkgo.It("analyze shell command detects build or test segments", func() {
		buildOrTest := []string{
			"cd native && scons -j1 -Q 2>&1 | head -20",
			"go test ./...",
			"cargo check",
			"npm run build",
			"python -m pytest",
			"python3 -m unittest",
			"python3 scripts/run_tests.py --build-only",
			"python3 tools/build.py",
			"make test",
		}

		for _, command := range buildOrTest {
			if got := AnalyzeShellCommand(command); !got.BuildOrTest {
				ginkgo.Fail(fmt.Sprintf("%q BuildOrTest = false, want true; analysis=%#v", command, got))
			}
		}

		for _, command := range []string{
			"git diff --check",
			"git status --short",
			"rg TODO internal",
			"python scripts/format.py",
		} {
			if got := AnalyzeShellCommand(command); got.BuildOrTest {
				ginkgo.Fail(fmt.Sprintf("%q BuildOrTest = true, want false; analysis=%#v", command, got))
			}
		}
	})
	ginkgo.It("analyze shell command detects verification families", func() {
		tests := []struct {
			command string
			want    ShellVerificationFamily
			full    bool
		}{
			{command: "go test ./...", want: ShellVerificationFullSuite, full: true},
			{command: "go test ./internal/runtime/agent", want: ShellVerificationTest},
			{command: "go build ./cmd/idiom", want: ShellVerificationBuild},
			{command: "git diff --check -- README.md", want: ShellVerificationCheck},
			{command: "cargo check", want: ShellVerificationCheck},
			{command: "cargo test", want: ShellVerificationTest},
			{command: "npm test", want: ShellVerificationTest},
			{command: "make test", want: ShellVerificationTest},
			{command: "npm run build", want: ShellVerificationBuild},
			{command: "python -m pytest", want: ShellVerificationFullSuite, full: true},
			{command: "python3 -B -m unittest", want: ShellVerificationFullSuite, full: true},
			{command: "python3 -I -W ignore -X dev -m pytest", want: ShellVerificationFullSuite, full: true},
			{command: "python3 -B -m build", want: ShellVerificationBuild},
			{command: `python3 -c "print('-m unittest')" -m unittest`, want: ShellVerificationNone},
			{command: "python3 scripts/format.py -m unittest", want: ShellVerificationNone},
			{command: "python3 --future-option -m unittest", want: ShellVerificationNone},
			{command: "python3 --version -m unittest", want: ShellVerificationNone},
			{command: "python3 tools/build.py", want: ShellVerificationBuild},
			{command: "make check", want: ShellVerificationCheck},
			{command: "bash -n scripts/run-harness.sh", want: ShellVerificationScriptSyntax},
			{command: "shellcheck scripts/run-harness.sh", want: ShellVerificationScriptSyntax},
			{command: "tsc --noEmit", want: ShellVerificationGeneric},
			{command: "ruff check .", want: ShellVerificationCheck},
			{command: "eslint src", want: ShellVerificationGeneric},
			{command: "printf 'go test ./...'", want: ShellVerificationNone},
			{command: "go test -run 'go test ./...' ./internal/unrelated", want: ShellVerificationTest},
			{command: "go test -run TestAgent ./...", want: ShellVerificationTest},
			{command: "go test -short ./...", want: ShellVerificationTest},
			{command: "go test -n ./...", want: ShellVerificationTest},
			{command: "go test -exec=true ./...", want: ShellVerificationTest},
			{command: "go build -n ./cmd/idiom", want: ShellVerificationBuild},
			{command: "go vet -n ./...", want: ShellVerificationGeneric},
			{command: "go test -c ./...", want: ShellVerificationTest},
			{command: "go test -count=0 ./...", want: ShellVerificationTest},
			{command: "go test -future-filter ./...", want: ShellVerificationNone},
			{command: "go test ./internal/unrelated # ./...", want: ShellVerificationNone},
			{command: "go test -run '#' ./internal/unrelated", want: ShellVerificationTest},
			{command: "go test ./... && printf done", want: ShellVerificationNone},
			{command: "go test ./... | head", want: ShellVerificationNone},
			{command: "go test ./internal/runtime/agent | head -n 20", want: ShellVerificationNone},
			{command: "go test ./... | tee report.txt", want: ShellVerificationNone},
			{command: "go test ./... > report.txt", want: ShellVerificationNone},
			{command: "go test ./... | head && printf done", want: ShellVerificationNone},
			{command: "go test ./... | head | rm -rf tmp", want: ShellVerificationNone},
			{command: "go test ./... || head", want: ShellVerificationNone},
		}

		for _, tt := range tests {
			got := AnalyzeShellCommand(tt.command)
			gomega.Expect(got.Verification).To(gomega.Equal(tt.want), fmt.Sprintf("%q verification = %q full=%t, want %q full=%t; analysis=%#v", tt.command, got.Verification, got.FullSuite, tt.want, tt.full, got))
			gomega.Expect(got.FullSuite).To(gomega.Equal(tt.full), fmt.Sprintf("%q verification = %q full=%t, want %q full=%t; analysis=%#v", tt.command, got.Verification, got.FullSuite, tt.want, tt.full, got))

		}
	})
})
var _ = ginkgo.Describe("tool request action class uses shell verification facts", func() {

	tests := []struct {
		name    string
		command string
		want    autonomy.ActionClass
	}{
		{name: "verification", command: "go test ./internal/capabilities/workspace", want: autonomy.ActionClassVerify},
		{name: "flagged Python verification", command: "python3 -B -m unittest", want: autonomy.ActionClassVerify},
		{name: "read only inspection", command: "git status --short", want: autonomy.ActionClassRead},
		{name: "write capable git diff is only a probe", command: "git diff --output=report.txt", want: autonomy.ActionClassGenericProbe},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			req := ToolRequest{Tool: ToolNameShell, Command: tt.command}
			if got := ToolRequestActionClass(req); got != tt.want {
				ginkgo.Fail(fmt.Sprintf("ToolRequestActionClass(%#v) = %q, want %q", req, got, tt.want))
			}
		})
	}
})
var _ = ginkgo.Describe("shell verification execution", func() {
	ginkgo.It("rejects statically non-executing Go test commands", func() {
		for _, command := range []string{"go test -c ./...", "go test -count=0 ./...", "go test -count 0 ./...", "go test -list TestX ./...", "go test -n ./...", "go test -exec=true ./...", "go test -exec /usr/bin/true ./...", "go build -n ./cmd/idiom", "go vet -n ./..."} {
			gomega.Expect(zeroSelectedTestFailureReason(command, "")).NotTo(gomega.BeEmpty(), fmt.Sprintf("command %q must not produce successful test evidence", command))
		}
		gomega.Expect(zeroSelectedTestFailureReason("go test -count=1 ./...", "ok")).To(gomega.BeEmpty())
		gomega.Expect(zeroSelectedTestFailureReason("go test ./pkg", "testing: warning: no tests to run\nPASS")).NotTo(gomega.BeEmpty())
		gomega.Expect(zeroSelectedTestFailureReason("go test ./...", "?\tpkg\t[no test files]\nok\tother\t0.1s")).To(gomega.BeEmpty())
	})
})
var _ = ginkgo.Describe("shell analysis", func() {
	ginkgo.It("workbranch child shell policy projects analysis owned families", func() {
		policy := WorkbranchChildShellPolicy([]string{
			"git_push",
			"git_history_mutation",
			"git_index_mutation",
			"publish",
			"deploy",
		})

		for _, prefix := range []string{
			"go test", "go list", "git status", "git diff", "git log", "git show",
			"git grep", "pwd", "ls ", "rg ", "grep ", "cat ", "head ", "tail ", "wc ",
		} {
			gomega.Expect(slices.Contains(policy.AllowedPrefixes, prefix)).To(gomega.BeTrue(), fmt.Sprintf("AllowedPrefixes missing %q: %#v", prefix, policy.AllowedPrefixes))

		}

		for _, prefix := range []string{
			"git add", "git commit", "git reset", "git push", "gh pr create",
			"gh pr merge", "gh release", "git -C", "npm publish", "pnpm publish",
			"docker push", "kubectl",
		} {
			gomega.Expect(slices.Contains(policy.DeniedPrefixes, prefix)).To(gomega.BeTrue(), fmt.Sprintf("DeniedPrefixes missing %q: %#v", prefix, policy.DeniedPrefixes))

		}

		for _, fragment := range []string{"&&", ">", "$(", "`", "go env -w", "--output"} {
			gomega.Expect(slices.Contains(policy.DeniedFragments, fragment)).To(gomega.BeTrue(), fmt.Sprintf("DeniedFragments missing %q: %#v", fragment, policy.DeniedFragments))

		}
	})
	ginkgo.It("read only child shell policy is workspace owned", func() {
		policy := ReadOnlyChildShellPolicy()
		for _, prefix := range []string{
			"rg ", "sed -n", "find ", "git diff", "go env",
		} {
			gomega.Expect(slices.Contains(policy.AllowedPrefixes, prefix)).To(gomega.BeTrue(), fmt.Sprintf("read-only prefixes missing %q: %#v", prefix, policy.AllowedPrefixes))

		}

		for _, fragment := range []string{"&&", ">", "$(", "`", "go env -w", "--output"} {
			gomega.Expect(slices.Contains(policy.DeniedFragments, fragment)).To(gomega.BeTrue(), fmt.Sprintf("read-only denied fragments missing %q: %#v", fragment, policy.DeniedFragments))

		}
	})
})
