package modepolicy

import (
	"testing"

	"idiom/internal/contracts/toolpolicy"
)

func TestModeByNameRoundTrip(t *testing.T) {
	for _, wantMode := range DefaultModes() {
		mode, ok := ModeByName(wantMode.Name)
		if !ok {
			t.Errorf("ModeByName(%q) = !ok, want ok", wantMode.Name)

			continue
		}

		if mode.Name != wantMode.Name {
			t.Errorf("ModeByName(%q).Name = %q", wantMode.Name, mode.Name)
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
		want toolpolicy.Decision
	}{
		{"build", toolpolicy.Ask},
		{"plan", toolpolicy.Allow},
		{"general", toolpolicy.Ask},
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

			if len(rules) != 1 {
				t.Fatalf("mode %q: mcp:* rules = %#v, want one rule", tc.mode, rules)
			}
			if got := rules[0].Decision; got != tc.want {
				t.Fatalf("mode %q: mcp:* decision = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
