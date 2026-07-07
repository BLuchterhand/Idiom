package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

// Fix 6 integration — Manager.EvaluateToolRule owns the workspace overlay:
// project config denies, active mode rules, protected write paths, and the
// legacy EnableWrites / EnableShell fallback.
func TestManagerEvaluateToolRuleIntegratesConfigRules(t *testing.T) {
	mgr := &Manager{}
	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "x.go"}); got != toolpolicy.None {
		t.Fatalf("manager with no rules must return none, got %s", got)
	}
}

func TestManagerEvaluateToolRuleConfiguredDenyOverridesModeAllow(t *testing.T) {
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
	if got != toolpolicy.Deny {
		t.Fatalf("config deny must override mode allow, got %s", got)
	}
}

func TestManagerProtectedWritePathsRequireApprovalInWriteMode(t *testing.T) {
	mgr := NewManager(config.Workspace{Root: t.TempDir()})
	mgr.SetProtectedWritePaths([]string{"prompts/base.md"})
	mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
		"edit_file": {{Pattern: "**", Decision: toolpolicy.Allow}},
	})

	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "prompts/base.md"}); got != toolpolicy.Ask {
		t.Fatalf("protected edit_file decision = %s, want ask", got)
	}

	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "README.md"}); got != toolpolicy.Allow {
		t.Fatalf("unprotected edit_file decision = %s, want allow", got)
	}
}

func TestManagerProtectedWritePathsDoNotOverrideModeDeny(t *testing.T) {
	mgr := NewManager(config.Workspace{Root: t.TempDir()})
	mgr.SetProtectedWritePaths([]string{"prompts/base.md"})
	mgr.SetPermissionRules(map[string][]toolpolicy.Rule{
		"edit_file": {{Pattern: "**", Decision: toolpolicy.Deny}},
	})

	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "prompts/base.md"}); got != toolpolicy.Deny {
		t.Fatalf("protected edit_file under deny mode = %s, want deny", got)
	}
}

func TestManagerProtectedWritePathsCoverApplyPatchTargets(t *testing.T) {
	mgr := NewManager(config.Workspace{Root: t.TempDir()})
	mgr.SetProtectedWritePaths([]string{"prompts/base.md"})

	patch := "*** Begin Patch\n*** Update File: prompts/base.md\n@@\n-old\n+new\n*** End Patch\n"
	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "apply_patch", Content: patch}); got != toolpolicy.Ask {
		t.Fatalf("protected apply_patch decision = %s, want ask", got)
	}
}

func TestManagerExecuteDeniesConfiguredReadFileRule(t *testing.T) {
	dir := t.TempDir()
	secret := "SECRET_SENTINEL_DO_NOT_LEAK"

	if err := os.WriteFile(filepath.Join(dir, "deny.txt"), []byte(secret), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

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
	if res.Error == "" {
		t.Fatalf("expected deny error, got none: %+v", res)
	}

	if !strings.Contains(res.Summary, "workspace tool_rules") {
		t.Fatalf("expected workspace tool_rules summary, got %q", res.Summary)
	}

	if !strings.Contains(res.Error, "deny read_file") {
		t.Fatalf("expected read_file deny error, got %q", res.Error)
	}

	if strings.Contains(res.Output+res.Summary+res.Error, secret) {
		t.Fatalf("deny result leaked file content: %+v", res)
	}
}
