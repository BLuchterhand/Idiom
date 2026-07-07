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

func TestPolicyDecideLastMatchWins(t *testing.T) {
	policy := Policy{
		Default: Deny,
		Rules: []Rule{
			{Tool: "shell", CommandPrefix: "git diff", Decision: Allow},
			{Tool: "shell", CommandContains: "--output", Decision: Deny},
		},
	}

	for _, command := range []string{"git diff --output=/tmp/patch", "git diff --output /tmp/patch"} {
		got := policy.Decide(Request{Tool: "shell", Command: command})
		if got.Decision != Deny || !got.Matched || got.Rule.CommandContains != "--output" {
			t.Fatalf("deny tail should override earlier allow for %q, got %#v", command, got)
		}
	}
}

func TestPolicyCommandPrefixMatchesTokenBoundary(t *testing.T) {
	policy := Policy{
		Default: Deny,
		Rules: []Rule{
			{Tool: "shell", CommandPrefix: "go test", Decision: Allow},
		},
	}

	if got := policy.Decide(Request{Tool: "shell", Command: "go test ./..."}); got.Decision != Allow {
		t.Fatalf("go test command should match prefix, got %#v", got)
	}

	if got := policy.Decide(Request{Tool: "shell", Command: "go testdata"}); got.Decision != Deny {
		t.Fatalf("prefix must respect token boundary, got %#v", got)
	}
}

func TestPolicyPathPatternUsesRequestTarget(t *testing.T) {
	policy := Policy{
		Default: Allow,
		Rules: []Rule{
			{Tool: "edit_file", PathPattern: "README.md", Decision: Deny},
		},
	}

	got := policy.Decide(Request{Tool: "edit_file", Path: "README.md"})
	if got.Decision != Deny || got.Target != "README.md" {
		t.Fatalf("path pattern should match request target, got %#v", got)
	}
}
