package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

// Fix 6 (per-tool permission granularity) — exercises docs/opencode-fixes-design.md §6.

func TestMatchToolRuleShellPatterns(t *testing.T) {
	rules := map[string][]ToolRule{
		"shell": {
			{Pattern: "rm *", Decision: ToolRuleAsk},
			{Pattern: "git *", Decision: ToolRuleAllow},
			{Pattern: "*", Decision: ToolRuleAllow},
		},
	}
	cases := []struct {
		command string
		want    ToolRuleDecision
	}{
		{"rm -rf /tmp/x", ToolRuleAsk},
		{"git status", ToolRuleAllow},
		{"ls -la", ToolRuleAllow},
	}
	for _, c := range cases {
		got := MatchToolRule("shell", ToolRequest{Tool: "shell", Command: c.command}, rules)
		if got != c.want {
			t.Fatalf("command=%q want=%s got=%s", c.command, c.want, got)
		}
	}
}

func TestMatchToolRulePathPatternsWithRecursiveGlob(t *testing.T) {
	rules := map[string][]ToolRule{
		"edit_file": {
			{Pattern: "**/secrets/**", Decision: ToolRuleDeny},
			{Pattern: "**/*.md", Decision: ToolRuleAllow},
			{Pattern: "*", Decision: ToolRuleAsk},
		},
	}
	cases := []struct {
		path string
		want ToolRuleDecision
	}{
		{"README.md", ToolRuleAllow},
		{"docs/plan.md", ToolRuleAllow},
		{"app/secrets/keys.json", ToolRuleDeny},
		{"internal/secrets/token.txt", ToolRuleDeny},
		{"internal/app/app.go", ToolRuleAsk},
	}
	for _, c := range cases {
		got := MatchToolRule("edit_file", ToolRequest{Tool: "edit_file", Path: c.path}, rules)
		if got != c.want {
			t.Fatalf("path=%q want=%s got=%s", c.path, c.want, got)
		}
	}
}

func TestMatchToolRuleAbsentRulesReturnsNone(t *testing.T) {
	if got := MatchToolRule("edit_file", ToolRequest{Tool: "edit_file", Path: "app.go"}, nil); got != ToolRuleNone {
		t.Fatalf("empty rules should return none, got %s", got)
	}
	rules := map[string][]ToolRule{
		"shell": {{Pattern: "*", Decision: ToolRuleAllow}},
	}
	if got := MatchToolRule("edit_file", ToolRequest{Tool: "edit_file", Path: "app.go"}, rules); got != ToolRuleNone {
		t.Fatalf("unconfigured tool should return none, got %s", got)
	}
}

func TestMatchToolRuleFirstMatchWins(t *testing.T) {
	rules := map[string][]ToolRule{
		"edit_file": {
			{Pattern: "internal/app/*.go", Decision: ToolRuleAllow},
			{Pattern: "**/app.go", Decision: ToolRuleDeny},
		},
	}
	got := MatchToolRule("edit_file", ToolRequest{Tool: "edit_file", Path: "internal/app/app.go"}, rules)
	if got != ToolRuleAllow {
		t.Fatalf("first-match-wins broken: got %s", got)
	}
}

// Fix 6 integration — the Manager's EvaluateToolRule wrapper converts
// config.ToolRule entries and applies the same glob semantics as the pure
// matcher above. This is the surface that app-layer code (lifecycle.go,
// implementation.go) consults before falling through to the binary
// EnableWrites / EnableShell gates.
func TestManagerEvaluateToolRuleIntegratesConfigRules(t *testing.T) {
	mgr := &Manager{}
	if got := mgr.EvaluateToolRule(ToolRequest{Tool: "edit_file", Path: "x.go"}); got != ToolRuleNone {
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
	mgr.SetPermissionRules(map[string][]ToolRule{
		"read_file": {
			{Pattern: "*", Decision: ToolRuleAllow},
		},
	})

	got := mgr.EvaluateToolRule(ToolRequest{Tool: "read_file", Path: "secret.txt"})
	if got != ToolRuleDeny {
		t.Fatalf("config deny must override mode allow, got %s", got)
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
