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
			name:        "go test is read only verification",
			command:     "go test ./...",
			buildOrTest: true,
			sideEffect:  autonomy.SideEffectReadOnly,
		},
		{
			name:       "git status is read only verification",
			command:    "git status --short",
			sideEffect: autonomy.SideEffectReadOnly,
		},
		{
			name:       "git diff check is read only verification",
			command:    "git diff --check -- README.md",
			sideEffect: autonomy.SideEffectReadOnly,
		},
		{
			name:       "git diff output flag is unknown",
			command:    "git diff --output=report.txt",
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
			name:        "quoted pipe is not control syntax",
			command:     "go test ./internal/runtime/agent -run 'TestA|TestB'",
			buildOrTest: true,
			sideEffect:  autonomy.SideEffectReadOnly,
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

func TestShellCommandFromToolResultExtractsWorkspaceShellPrefix(t *testing.T) {
	tests := []struct {
		name   string
		result ToolResult
		want   string
	}{
		{
			name:   "shell output prefix",
			result: ToolResult{Tool: ToolNameShell, Output: "$ go test ./...\nok"},
			want:   "go test ./...",
		},
		{
			name:   "blank lines before shell prefix",
			result: ToolResult{Tool: ToolNameShell, Output: "\n\n  $ git diff --check\n"},
			want:   "git diff --check",
		},
		{
			name:   "unstructured output before prefix is not shell command",
			result: ToolResult{Tool: ToolNameShell, Output: "ok\n$ go test ./..."},
		},
		{
			name:   "plain command text is not shell command result shape",
			result: ToolResult{Tool: ToolNameShell, Output: "go test ./..."},
		},
		{
			name:   "empty output",
			result: ToolResult{Tool: ToolNameShell},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShellCommandFromToolResult(tt.result); got != tt.want {
				t.Fatalf("ShellCommandFromToolResult() = %q, want %q", got, tt.want)
			}
		})
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

	for _, fragment := range []string{"&&", ">", "$(", "`", "go env -w", "--output="} {
		if !slices.Contains(policy.DeniedFragments, fragment) {
			t.Fatalf("DeniedFragments missing %q: %#v", fragment, policy.DeniedFragments)
		}
	}
}

func TestReadOnlyChildShellAllowedPrefixesAreWorkspaceOwned(t *testing.T) {
	prefixes := ReadOnlyChildShellAllowedPrefixes()
	for _, prefix := range []string{
		"rg ", "sed -n", "find ", "git diff", "go env",
	} {
		if !slices.Contains(prefixes, prefix) {
			t.Fatalf("read-only prefixes missing %q: %#v", prefix, prefixes)
		}
	}
}
