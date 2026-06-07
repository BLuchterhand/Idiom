package toolpolicy

import "testing"

func TestMatchShellPatterns(t *testing.T) {
	rules := map[string][]Rule{
		"shell": {
			{Pattern: "rm *", Decision: Ask},
			{Pattern: "git *", Decision: Allow},
			{Pattern: "*", Decision: Allow},
		},
	}
	cases := []struct {
		command string
		want    Decision
	}{
		{"rm -rf /tmp/x", Ask},
		{"git status", Allow},
		{"ls -la", Allow},
	}
	for _, c := range cases {
		got := Match("shell", Request{Command: c.command}, rules)
		if got != c.want {
			t.Fatalf("command=%q want=%s got=%s", c.command, c.want, got)
		}
	}
}

func TestMatchPathPatternsWithRecursiveGlob(t *testing.T) {
	rules := map[string][]Rule{
		"edit_file": {
			{Pattern: "**/secrets/**", Decision: Deny},
			{Pattern: "**/*.md", Decision: Allow},
			{Pattern: "*", Decision: Ask},
		},
	}
	cases := []struct {
		path string
		want Decision
	}{
		{"README.md", Allow},
		{"docs/plan.md", Allow},
		{"app/secrets/keys.json", Deny},
		{"internal/secrets/token.txt", Deny},
		{"internal/surfaces/tui/app/app.go", Ask},
	}
	for _, c := range cases {
		got := Match("edit_file", Request{Path: c.path}, rules)
		if got != c.want {
			t.Fatalf("path=%q want=%s got=%s", c.path, c.want, got)
		}
	}
}

func TestMatchAbsentRulesReturnsNone(t *testing.T) {
	if got := Match("edit_file", Request{Path: "app.go"}, nil); got != None {
		t.Fatalf("empty rules should return none, got %s", got)
	}
	rules := map[string][]Rule{
		"shell": {{Pattern: "*", Decision: Allow}},
	}
	if got := Match("edit_file", Request{Path: "app.go"}, rules); got != None {
		t.Fatalf("unconfigured tool should return none, got %s", got)
	}
}

func TestMatchFirstMatchWins(t *testing.T) {
	rules := map[string][]Rule{
		"edit_file": {
			{Pattern: "internal/surfaces/tui/app/*.go", Decision: Allow},
			{Pattern: "**/app.go", Decision: Deny},
		},
	}
	got := Match("edit_file", Request{Path: "internal/surfaces/tui/app/app.go"}, rules)
	if got != Allow {
		t.Fatalf("first-match-wins broken: got %s", got)
	}
}
