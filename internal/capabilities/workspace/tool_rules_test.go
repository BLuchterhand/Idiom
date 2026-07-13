package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/repository/semantic/languagetools"
)

var

// Fix 6 integration — Manager.EvaluateToolRule owns the workspace overlay:
// project config denies, active mode rules, protected write paths, and the
// legacy EnableWrites / EnableShell fallback.
_ = ginkgo.Describe("tool rules", func() {
	ginkgo.It("manager evaluate tool rule integrates config rules", func() {
		mgr := &Manager{}
		if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "x.go"}); got != toolpolicy.None {
			ginkgo.Fail(fmt.Sprintf("manager with no rules must return none, got %s", got))
		}
	})
})
var _ = ginkgo.Describe("tool policy input precomputes workspace targets", func() {

	cases := []struct {
		name             string
		request          ToolRequest
		wantCommand      string
		wantPathTarget   string
		wantPattern      string
		wantPatternKind  toolpolicy.PatternKind
		wantFallbackKeys []string
	}{
		{
			name:            "path rule target",
			request:         ToolRequest{Tool: "edit_file", Path: `docs\plan.md`},
			wantPathTarget:  "docs/plan.md",
			wantPattern:     "docs/plan.md",
			wantPatternKind: toolpolicy.PatternPath,
		},
		{
			name:            "destination path target",
			request:         ToolRequest{Tool: "go_move_declarations", Destination: `internal\moved.go`},
			wantPathTarget:  "internal/moved.go",
			wantPattern:     "internal/moved.go",
			wantPatternKind: toolpolicy.PatternPath,
		},
		{
			name:            "workdir path target",
			request:         ToolRequest{Tool: "external_read", Workdir: `tools\reader`},
			wantPathTarget:  "tools/reader",
			wantPattern:     "tools/reader",
			wantPatternKind: toolpolicy.PatternPath,
		},
		{
			name: "structured move path target",
			request: ToolRequest{
				Tool:  ToolNameGoMoveDeclarations,
				Moves: []GoDeclarationMove{{Destination: `internal\destination.go`}},
			},
			wantPathTarget:  "internal/destination.go",
			wantPattern:     "internal/destination.go",
			wantPatternKind: toolpolicy.PatternPath,
		},
		{
			name:            "shell command pattern target",
			request:         ToolRequest{Tool: ToolNameShell, Command: "  git status --short  ", Workdir: `repo\root`},
			wantCommand:     "git status --short",
			wantPathTarget:  "repo/root",
			wantPattern:     "git status --short",
			wantPatternKind: toolpolicy.PatternCommand,
		},
		{
			name:            "legacy bash command pattern target",
			request:         ToolRequest{Tool: "bash", Command: "  go test ./...  "},
			wantCommand:     "go test ./...",
			wantPattern:     "go test ./...",
			wantPatternKind: toolpolicy.PatternCommand,
		},
		{
			name:             "mcp target and fallback rule",
			request:          ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"go policy"}`)},
			wantPattern:      "go policy",
			wantPatternKind:  toolpolicy.PatternPath,
			wantFallbackKeys: []string{mcpToolPolicyFallbackRuleKey},
		},
	}

	for _, tc := range cases {
		ginkgo.It(tc.name, func() {
			got := ToolPolicyInput(tc.request)
			gomega.Expect(got.Command).To(gomega.Equal(tc.wantCommand), fmt.Sprintf("ToolPolicyInput(%#v) = %#v", tc.request, got))
			gomega.Expect(got.PathTarget).To(gomega.Equal(tc.wantPathTarget), fmt.Sprintf("ToolPolicyInput(%#v) = %#v", tc.request, got))
			gomega.Expect(got.PatternTarget).To(gomega.Equal(tc.wantPattern), fmt.Sprintf("ToolPolicyInput(%#v) = %#v", tc.request, got))
			gomega.Expect(got.PatternKind).To(gomega.Equal(tc.wantPatternKind), fmt.Sprintf("ToolPolicyInput(%#v) = %#v", tc.request, got))
			gomega.Expect(slices.Equal(got.FallbackRuleKeys, tc.wantFallbackKeys)).To(gomega.BeTrue(), fmt.Sprintf("fallback rule keys = %#v, want %#v", got.FallbackRuleKeys, tc.wantFallbackKeys))

		})
	}
})
var _ = ginkgo.Describe("tool rules", func() {
	ginkgo.It("manager evaluate configured tool rule matches shell command target", func() {
		mgr := NewManager(config.Workspace{
			ToolRules: map[string][]config.ToolRule{
				ToolNameShell: {
					{Pattern: "git push *", Decision: "deny"},
					{Pattern: "git *", Decision: "allow"},
				},
			},
		})

		if got := mgr.EvaluateConfiguredToolRule(ToolRequest{Tool: ToolNameShell, Command: "git push origin main"}); got.Decision != toolpolicy.Deny {
			ginkgo.Fail(fmt.Sprintf("git push decision = %s, want deny", got.Decision))
		}
		if got := mgr.EvaluateConfiguredToolRule(ToolRequest{Tool: ToolNameShell, Command: "git status --short"}); got.Decision != toolpolicy.Allow {
			ginkgo.Fail(fmt.Sprintf("git status decision = %s, want allow", got.Decision))
		}
	})
	ginkgo.It("manager evaluates every direct, aliased, and mixed write target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "secrets"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "docs"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "secrets", "token.txt"), []byte("old\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join("secrets", "token.txt"), filepath.Join(root, "cached-token.txt")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				ToolNameApplyPatch:         {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameGoMoveDeclarations: {{Pattern: "secrets/**", Decision: "deny"}},
			},
		})

		requests := []ToolRequest{
			{
				Tool:    ToolNameApplyPatch,
				Content: "*** Begin Patch\n*** Update File: secrets/token.txt\n@@\n-old\n+new\n*** End Patch",
			},
			{
				Tool:    ToolNameApplyPatch,
				Content: "*** Begin Patch\n*** Update File: cached-token.txt\n@@\n-old\n+new\n*** End Patch",
			},
			{
				Tool:    ToolNameApplyPatch,
				Content: "*** Begin Patch\n*** Add File: docs/public.txt\n+public\n*** Update File: cached-token.txt\n@@\n-old\n+new\n*** End Patch",
			},
			{
				Tool:        ToolNameGoMoveDeclarations,
				Path:        "secrets/token.txt",
				Destination: "docs/direct.go",
			},
			{
				Tool:        ToolNameGoMoveDeclarations,
				Path:        "docs/source.go",
				Destination: "docs/first.go",
				Moves: []GoDeclarationMove{
					{Destination: "docs/second.go"},
					{Destination: "cached-token.txt"},
				},
			},
		}

		wantTargets := []string{
			"secrets/token.txt",
			"cached-token.txt",
			"cached-token.txt",
			"secrets/token.txt",
			"cached-token.txt",
		}
		for index, request := range requests {
			decision := mgr.EvaluateConfiguredToolRule(request)
			gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Deny), "request was not denied: %#v", request)
			gomega.Expect(decision.Target).To(gomega.Equal(wantTargets[index]), "configured deny reported the wrong requested target: %#v", request)
		}

		directResult := mgr.Execute(context.Background(), requests[2])
		gomega.Expect(directResult.Error).To(gomega.ContainSubstring("cached-token.txt"))
		gomega.Expect(directResult.Error).NotTo(gomega.ContainSubstring("docs/public.txt"))

		aliasPatch := requests[1]
		askMgr := NewManager(config.Workspace{Root: root, CWD: root})
		askMgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameApplyPatch: {{Pattern: "secrets/**", Decision: toolpolicy.Ask}},
		})
		gomega.Expect(askMgr.EvaluateToolRule(aliasPatch)).To(gomega.Equal(toolpolicy.Ask))

		partialAllowMgr := NewManager(config.Workspace{Root: root, CWD: root})
		partialAllowMgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameGoMoveDeclarations: {{Pattern: "docs/first.go", Decision: toolpolicy.Allow}},
		})
		gomega.Expect(partialAllowMgr.EvaluateToolRule(requests[len(requests)-1])).To(gomega.Equal(toolpolicy.None))
	})
	ginkgo.It("manager applies flat permission policy to every compound write target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "docs"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "secrets"), 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		policy := toolpolicy.Policy{
			Default: toolpolicy.Allow,
			Rules: []toolpolicy.Rule{
				{Tool: ToolNameApplyPatch, PathPattern: "secrets/*", Decision: toolpolicy.Deny},
				{Tool: ToolNameGoMoveDeclarations, PathPattern: "secrets/*", Decision: toolpolicy.Deny},
			},
		}

		cases := []struct {
			name       string
			request    ToolRequest
			wantTarget string
		}{
			{
				name: "later direct patch target",
				request: ToolRequest{
					Tool:    ToolNameApplyPatch,
					Content: "*** Begin Patch\n*** Add File: docs/public.txt\n+public\n*** Add File: secrets/new-token.txt\n+secret\n*** End Patch",
				},
				wantTarget: "secrets/new-token.txt",
			},
			{
				name: "later structured move destination",
				request: ToolRequest{
					Tool:        ToolNameGoMoveDeclarations,
					Path:        "docs/source.go",
					Destination: "docs/first.go",
					Moves: []GoDeclarationMove{
						{Destination: "docs/second.go"},
						{Destination: "secrets/moved.go"},
					},
				},
				wantTarget: "secrets/moved.go",
			},
		}

		for _, tc := range cases {
			ginkgo.By(tc.name, func() {
				decision := mgr.EvaluatePermissionPolicy(policy, tc.request)
				gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Deny))
				gomega.Expect(decision.Target).To(gomega.Equal(tc.wantTarget))
				gomega.Expect(decision.Matched).To(gomega.BeTrue())
				gomega.Expect(decision.Rule.Decision).To(gomega.Equal(toolpolicy.Deny))
			})
		}
	})
	ginkgo.It("manager applies flat permission policy to canonical targets in compound writes", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "docs"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.MkdirAll(filepath.Join(root, "secrets"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "secrets", "token.txt"), []byte("old\n"), 0o600)).To(gomega.Succeed())
		if err := os.Symlink(filepath.Join("secrets", "token.txt"), filepath.Join(root, "z-cached-token.txt")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		policy := toolpolicy.Policy{
			Default: toolpolicy.Allow,
			Rules: []toolpolicy.Rule{{
				Tool:        ToolNameApplyPatch,
				PathPattern: "secrets/*",
				Decision:    toolpolicy.Deny,
			}},
		}
		request := ToolRequest{
			Tool:    ToolNameApplyPatch,
			Content: "*** Begin Patch\n*** Add File: docs/public.txt\n+public\n*** Update File: z-cached-token.txt\n@@\n-old\n+new\n*** End Patch",
		}

		decision := mgr.EvaluatePermissionPolicy(policy, request)
		gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Deny))
		gomega.Expect(decision.Target).To(gomega.Equal("z-cached-token.txt"))
		gomega.Expect(decision.Matched).To(gomega.BeTrue())
		gomega.Expect(decision.Rule.PathPattern).To(gomega.Equal("secrets/*"))
	})
	ginkgo.It("manager denies a compound write when only some targets are allowed", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		policy := toolpolicy.Policy{
			Default: toolpolicy.Deny,
			Rules: []toolpolicy.Rule{{
				Tool:        ToolNameApplyPatch,
				PathPattern: "docs/*",
				Decision:    toolpolicy.Allow,
			}},
		}
		request := ToolRequest{
			Tool:    ToolNameApplyPatch,
			Content: "*** Begin Patch\n*** Add File: docs/allowed.txt\n+allowed\n*** Add File: private/not-allowed.txt\n+blocked\n*** End Patch",
		}

		decision := mgr.EvaluatePermissionPolicy(policy, request)
		gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Deny))
		gomega.Expect(decision.Target).To(gomega.Equal("private/not-allowed.txt"))
		gomega.Expect(decision.Matched).To(gomega.BeFalse())
	})
	ginkgo.It("manager requires approval when any compound write target asks", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		policy := toolpolicy.Policy{
			Default: toolpolicy.Allow,
			Rules: []toolpolicy.Rule{{
				Tool:        ToolNameApplyPatch,
				PathPattern: "review/*",
				Decision:    toolpolicy.Ask,
			}},
		}
		request := ToolRequest{
			Tool:    ToolNameApplyPatch,
			Content: "*** Begin Patch\n*** Add File: docs/allowed.txt\n+allowed\n*** Add File: review/approval.txt\n+review\n*** End Patch",
		}

		decision := mgr.EvaluatePermissionPolicy(policy, request)
		gomega.Expect(decision.Decision).To(gomega.Equal(toolpolicy.Ask))
		gomega.Expect(decision.Target).To(gomega.Equal("review/approval.txt"))
		gomega.Expect(decision.Matched).To(gomega.BeTrue())
	})
	ginkgo.It("manager evaluate tool rule configured deny overrides mode allow", func() {
		mgr := NewManager(config.Workspace{
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: "secret.txt", Decision: "deny"},
				},
			},
		})
		mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			"read_file": {
				{Pattern: "*", Decision: toolpolicy.Allow},
			},
		})

		got := mgr.EvaluateToolRule(ToolRequest{Tool: "read_file", Path: "secret.txt"})
		gomega.Expect(got).To(gomega.Equal(toolpolicy.Deny), fmt.Sprintf("config deny must override mode allow, got %s", got))

	})
	ginkgo.It("manager protected write paths require approval in write mode", func() {
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir()})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})
		mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			"edit_file": {{Pattern: "**", Decision: toolpolicy.Allow}},
		})

		if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "prompts/base.md"}); got != toolpolicy.Ask {
			ginkgo.Fail(fmt.Sprintf("protected edit_file decision = %s, want ask", got))
		}

		if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "README.md"}); got != toolpolicy.Allow {
			ginkgo.Fail(fmt.Sprintf("unprotected edit_file decision = %s, want allow", got))
		}
	})
	ginkgo.It("manager protected write paths require approval for shell commands that are not proven read only", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "prompts"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "prompts", "base.md"), []byte("protected\n"), 0o644)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})
		mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameShell: {{Pattern: "*", Decision: toolpolicy.Allow}},
		})

		mutating := ToolRequest{Tool: ToolNameShell, Command: `printf changed > prompts/base.md`, Workdir: "."}
		gomega.Expect(mgr.EvaluateToolRule(mutating)).To(gomega.Equal(toolpolicy.Ask))

		inspection := ToolRequest{Tool: ToolNameShell, Command: `git diff -- prompts/base.md`, Workdir: "."}
		gomega.Expect(mgr.EvaluateToolRule(inspection)).To(gomega.Equal(toolpolicy.Allow))
	})
	ginkgo.It("manager protected write paths do not override mode deny", func() {
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir()})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})
		mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
			"edit_file": {{Pattern: "**", Decision: toolpolicy.Deny}},
		})

		if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "prompts/base.md"}); got != toolpolicy.Deny {
			ginkgo.Fail(fmt.Sprintf("protected edit_file under deny mode = %s, want deny", got))
		}
	})
	ginkgo.It("manager protected write paths cover apply patch targets", func() {
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir()})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})

		patch := "*** Begin Patch\n*** Update File: prompts/base.md\n@@\n-old\n+new\n*** End Patch\n"
		if got := mgr.EvaluateToolRule(ToolRequest{Tool: "apply_patch", Content: patch}); got != toolpolicy.Ask {
			ginkgo.Fail(fmt.Sprintf("protected apply_patch decision = %s, want ask", got))
		}
	})
	ginkgo.It("manager protected write paths resolve nested workdirs for every semantic write target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "prompts"), 0o755)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})

		requests := []ToolRequest{
			{Tool: ToolNameEditFile, Path: "base.md", Workdir: "prompts"},
			{
				Tool:    ToolNameApplyPatch,
				Workdir: "prompts",
				Content: "*** Begin Patch\n*** Update File: base.md\n@@\n-old\n+new\n*** End Patch",
			},
			{
				Tool:    ToolNameApplyPatch,
				Workdir: "prompts",
				Content: "*** Begin Patch\n*** Update File: source.md\n*** Move to: base.md\n@@\n-old\n+new\n*** End Patch",
			},
			{Tool: ToolNameGoMoveDeclarations, Path: "base.md", Destination: "destination.go", Workdir: "prompts"},
			{Tool: ToolNameGoMoveDeclarations, Path: "source.go", Destination: "base.md", Workdir: "prompts"},
			{
				Tool:        ToolNameGoMoveDeclarations,
				Path:        "source.go",
				Destination: "destination.go",
				Workdir:     "prompts",
				Moves:       []GoDeclarationMove{{Destination: "base.md"}},
			},
		}

		for _, request := range requests {
			gomega.Expect(mgr.EvaluateToolRule(request)).To(gomega.Equal(toolpolicy.Ask), "protected nested-workdir target was not recognized: %#v", request)
		}
	})
	ginkgo.It("manager protected write paths resolve symlink workdir aliases to their canonical target", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "prompts"), 0o755)).To(gomega.Succeed())
		if err := os.Symlink("prompts", filepath.Join(root, "prompt-alias")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		mgr.SetProtectedWritePaths([]string{"prompts/base.md"})

		request := ToolRequest{Tool: ToolNameEditFile, Path: "base.md", Workdir: "prompt-alias"}
		gomega.Expect(mgr.EvaluateToolRule(request)).To(gomega.Equal(toolpolicy.Ask))
	})
	ginkgo.It("manager execute denies configured read file rule", func() {
		dir := ginkgo.GinkgoT().TempDir()
		secret := "SECRET_SENTINEL_DO_NOT_LEAK"
		gomega.Expect(os.WriteFile(filepath.Join(dir, "deny.txt"), []byte(secret), 0o600)).To(gomega.Succeed())

		mgr := NewManager(config.Workspace{
			Root: dir,
			CWD:  dir,
			ToolRules: map[string][]config.ToolRule{
				"read_file": {
					{Pattern: "deny.txt", Decision: "deny"},
				},
			},
		})

		res := mgr.Execute(context.Background(), ToolRequest{Tool: "read_file", Path: "deny.txt"})
		gomega.Expect(res.Error).NotTo(gomega.Equal(""), fmt.Sprintf("expected deny error, got none: %+v", res))
		gomega.Expect(res.Summary).To(gomega.ContainSubstring("workspace tool_rules"), fmt.Sprintf("expected workspace tool_rules summary, got %q", res.Summary))
		gomega.Expect(res.Error).To(gomega.ContainSubstring("deny read_file"), fmt.Sprintf("expected read_file deny error, got %q", res.Error))
		gomega.Expect(res.Output+res.Summary+res.Error).NotTo(gomega.ContainSubstring(secret), fmt.Sprintf("deny result leaked file content: %+v", res))

	})
	ginkgo.It("code navigate route honors concrete language tool deny", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/routepolicy\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "pkg", "feature.go"), "package pkg\n\ntype Widget struct{}\n")

		mgr := NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				"go_definition": {
					{Pattern: "pkg/feature.go", Decision: "deny"},
				},
			},
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       "code_navigate",
			Capability: "definition",
			Path:       "pkg/feature.go",
			Pattern:    "Widget",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected routed go_definition deny, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("deny go_definition"), fmt.Sprintf("expected routed go_definition deny, got %#v", result))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("Code navigation routed definition via go_definition"), fmt.Sprintf("expected code_navigate wrapper summary, got %#v", result))

	})
	ginkgo.It("language search content port honors search content deny", func() {
		root := ginkgo.GinkgoT().TempDir()

		mgr := NewManager(config.Workspace{
			Root: root,
			CWD:  root,
			ToolRules: map[string][]config.ToolRule{
				"search_content": {
					{Pattern: ".", Decision: "deny"},
				},
			},
		})

		result := (languageToolHost{manager: mgr}).SearchContent(context.Background(), languagetools.SearchContentRequest{
			Path:    ".",
			Pattern: "anything",
		})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected search_content deny through reference fallback, got %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring("deny search_content"), fmt.Sprintf("expected search_content deny through reference fallback, got %#v", result))

	})
})
