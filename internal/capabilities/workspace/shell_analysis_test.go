package workspace

import (
	"slices"
	"testing"

	"idiom/internal/contracts/autonomy"
)

func TestAnalyzeShellCommandClassifiesInspectionAndVerification(t *testing.T) {
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
			sideEffect:      autonomy.SideEffectUnknown,
		},
		{
			name:       "narrowing inspection",
			command:    "rg TODO internal",
			narrowing:  true,
			sideEffect: autonomy.SideEffectUnknown,
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
			name:       "mutating command is unknown",
			command:    "touch x",
			sideEffect: autonomy.SideEffectUnknown,
		},
		{
			name:             "pipe is control syntax",
			command:          "go test ./... | tee report.txt",
			buildOrTest:      true,
			verification:     ShellVerificationFullSuite,
			fullSuite:        true,
			hasControlSyntax: true,
			sideEffect:       autonomy.SideEffectUnknown,
		},
		{
			name:             "compound shell is control syntax",
			command:          "git status && rm -rf tmp",
			hasControlSyntax: true,
			sideEffect:       autonomy.SideEffectUnknown,
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
			name:         "eslint is generic verification",
			command:      "eslint src",
			verification: ShellVerificationGeneric,
			sideEffect:   autonomy.SideEffectUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeShellCommand(tt.command)
			if got.BroadInspection != tt.broadInspection {
				t.Fatalf("BroadInspection = %t, want %t", got.BroadInspection, tt.broadInspection)
			}
			if got.Narrowing != tt.narrowing {
				t.Fatalf("Narrowing = %t, want %t", got.Narrowing, tt.narrowing)
			}
			if got.BuildOrTest != tt.buildOrTest {
				t.Fatalf("BuildOrTest = %t, want %t", got.BuildOrTest, tt.buildOrTest)
			}
			if got.Verification != tt.verification {
				t.Fatalf("Verification = %q, want %q", got.Verification, tt.verification)
			}
			if got.FullSuite != tt.fullSuite {
				t.Fatalf("FullSuite = %t, want %t", got.FullSuite, tt.fullSuite)
			}
			if got.HasControlSyntax != tt.hasControlSyntax {
				t.Fatalf("HasControlSyntax = %t, want %t", got.HasControlSyntax, tt.hasControlSyntax)
			}
			if got.SideEffect != tt.sideEffect {
				t.Fatalf("SideEffect = %q, want %q", got.SideEffect, tt.sideEffect)
			}
		})
	}
}

func TestAnalyzeShellCommandDetectsBuildOrTestSegments(t *testing.T) {
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
			t.Fatalf("%q BuildOrTest = false, want true; analysis=%#v", command, got)
		}
	}

	for _, command := range []string{
		"git diff --check",
		"git status --short",
		"rg TODO internal",
		"python scripts/format.py",
	} {
		if got := AnalyzeShellCommand(command); got.BuildOrTest {
			t.Fatalf("%q BuildOrTest = true, want false; analysis=%#v", command, got)
		}
	}
}

func TestAnalyzeShellCommandDetectsVerificationFamilies(t *testing.T) {
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
		{command: "cargo test", want: ShellVerificationFullSuite, full: true},
		{command: "npm test", want: ShellVerificationFullSuite, full: true},
		{command: "npm run build", want: ShellVerificationBuild},
		{command: "python -m pytest", want: ShellVerificationTest},
		{command: "python3 tools/build.py", want: ShellVerificationBuild},
		{command: "make check", want: ShellVerificationCheck},
		{command: "bash -n scripts/run-harness.sh", want: ShellVerificationScriptSyntax},
		{command: "shellcheck scripts/run-harness.sh", want: ShellVerificationScriptSyntax},
		{command: "tsc --noEmit", want: ShellVerificationGeneric},
		{command: "ruff check .", want: ShellVerificationCheck},
		{command: "eslint src", want: ShellVerificationGeneric},
	}

	for _, tt := range tests {
		got := AnalyzeShellCommand(tt.command)
		if got.Verification != tt.want || got.FullSuite != tt.full {
			t.Fatalf("%q verification = %q full=%t, want %q full=%t; analysis=%#v", tt.command, got.Verification, got.FullSuite, tt.want, tt.full, got)
		}
	}
}

func TestWorkbranchChildShellPolicyProjectsAnalysisOwnedFamilies(t *testing.T) {
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
		if !slices.Contains(policy.AllowedPrefixes, prefix) {
			t.Fatalf("AllowedPrefixes missing %q: %#v", prefix, policy.AllowedPrefixes)
		}
	}

	for _, prefix := range []string{
		"git add", "git commit", "git reset", "git push", "gh pr create",
		"gh pr merge", "gh release", "git -C", "npm publish", "pnpm publish",
		"docker push", "kubectl",
	} {
		if !slices.Contains(policy.DeniedPrefixes, prefix) {
			t.Fatalf("DeniedPrefixes missing %q: %#v", prefix, policy.DeniedPrefixes)
		}
	}

	for _, fragment := range []string{"&&", ">", "$(", "`", "go env -w", "--output"} {
		if !slices.Contains(policy.DeniedFragments, fragment) {
			t.Fatalf("DeniedFragments missing %q: %#v", fragment, policy.DeniedFragments)
		}
	}
}

func TestReadOnlyChildShellPolicyIsWorkspaceOwned(t *testing.T) {
	policy := ReadOnlyChildShellPolicy()
	for _, prefix := range []string{
		"rg ", "sed -n", "find ", "git diff", "go env",
	} {
		if !slices.Contains(policy.AllowedPrefixes, prefix) {
			t.Fatalf("read-only prefixes missing %q: %#v", prefix, policy.AllowedPrefixes)
		}
	}

	for _, fragment := range []string{"&&", ">", "$(", "`", "go env -w", "--output"} {
		if !slices.Contains(policy.DeniedFragments, fragment) {
			t.Fatalf("read-only denied fragments missing %q: %#v", fragment, policy.DeniedFragments)
		}
	}
}
