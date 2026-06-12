package agent

import (
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestDefaultModesValidate(t *testing.T) {
	for _, mode := range DefaultModes() {
		if err := mode.Validate(); err != nil {
			t.Errorf("mode %q failed validation: %v", mode.Name, err)
		}
	}
}

func TestModeByNameRoundTrip(t *testing.T) {
	for _, name := range ModeNames() {
		mode, ok := ModeByName(name)
		if !ok {
			t.Errorf("ModeByName(%q) = !ok, want ok", name)

			continue
		}

		if mode.Name != name {
			t.Errorf("ModeByName(%q).Name = %q", name, mode.Name)
		}
	}

	if _, ok := ModeByName("not-a-mode"); ok {
		t.Error("ModeByName(unknown) should be !ok")
	}
}

func TestModeOrDefaultFallsBackToBuild(t *testing.T) {
	if got := ModeOrDefault(""); got.Name != "build" {
		t.Errorf("ModeOrDefault empty = %q, want build", got.Name)
	}

	if got := ModeOrDefault("plan"); got.Name != "plan" {
		t.Errorf("ModeOrDefault plan = %q, want plan", got.Name)
	}

	if got := ModeOrDefault("nonsense"); got.Name != "build" {
		t.Errorf("ModeOrDefault nonsense = %q, want build fallback", got.Name)
	}
}

// TestModeMCPDefaults pins each mode's default decision for `mcp:*`
// against decision 3 in docs/web-search-tool-design.md §6 W5: plan
// allows external reads, build/general ask. Regressions here would
// either silently expose the model to free egress (plan miswired) or
// burden every general-mode interaction with a confirmation prompt for
// every MCP tool call.
func TestModeMCPDefaults(t *testing.T) {
	cases := []struct {
		mode string
		want workspace.ToolRuleDecision
	}{
		{"build", workspace.ToolRuleAsk},
		{"plan", workspace.ToolRuleAllow},
		{"general", workspace.ToolRuleAsk},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			mode, ok := ModeByName(tc.mode)
			if !ok {
				t.Fatalf("ModeByName(%q) failed", tc.mode)
			}

			rules, ok := mode.PermissionRules["mcp:*"]
			if !ok {
				t.Fatalf("mode %q is missing mcp:* permission rule", tc.mode)
			}

			req := workspace.ToolRequest{Tool: "mcp:exa/web_search_exa", Args: []byte(`{"query":"hello"}`)}
			got := workspace.MatchToolRule(req.Tool, req, map[string][]workspace.ToolRule{"mcp:*": rules})

			if got != tc.want {
				t.Fatalf("mode %q: mcp:* decision = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
