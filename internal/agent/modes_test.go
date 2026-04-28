package agent

import (
	"strings"
	"testing"

	"idiom/internal/workspace"
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

func TestWithModelRewritesSystemPromptFilesByModelAndMode(t *testing.T) {
	tests := []struct {
		name       string
		modeName   string
		modelID    string
		wantBase   string
		wantFrag   string
		wantNoFrag bool
	}{
		{"qwen + build", "build", "qwen3.6-35b", "qwen.md", "build-switch.md", false},
		{"qwen + plan", "plan", "qwen3.6-35b", "qwen.md", "plan.md", false},
		{"qwen + general (no fragment)", "general", "qwen3.6-35b", "qwen.md", "", true},
		{"claude + plan → anthropic plan reminder", "plan", "claude-opus-4-5", "anthropic.md", "plan-reminder-anthropic.md", false},
		{"gpt-4o → beast + build", "build", "gpt-4o", "beast.md", "build-switch.md", false},
		{"gpt-3.5 → gpt + build", "build", "gpt-3.5-turbo", "gpt.md", "build-switch.md", false},
		{"gemini + plan", "plan", "gemini-2.0-pro", "gemini.md", "plan.md", false},
		{"unknown + build → default base", "build", "some-future-model", "default.md", "build-switch.md", false},
		{"empty model id → default base", "build", "", "default.md", "build-switch.md", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mode, ok := ModeByName(tc.modeName)
			if !ok {
				t.Fatalf("ModeByName(%q) not found", tc.modeName)
			}
			got := mode.WithModel(tc.modelID)
			if got.Name != tc.modeName {
				t.Fatalf("WithModel preserved Name = %q, want %q", got.Name, tc.modeName)
			}
			if len(got.SystemPromptFiles) == 0 {
				t.Fatalf("WithModel returned empty SystemPromptFiles")
			}
			if !strings.HasSuffix(got.SystemPromptFiles[0], tc.wantBase) {
				t.Fatalf("base prompt = %q, want suffix %q", got.SystemPromptFiles[0], tc.wantBase)
			}
			if tc.wantNoFrag {
				if len(got.SystemPromptFiles) != 1 {
					t.Fatalf("expected only base prompt for general mode, got %v", got.SystemPromptFiles)
				}
				return
			}
			if len(got.SystemPromptFiles) < 2 {
				t.Fatalf("expected base + fragment, got %v", got.SystemPromptFiles)
			}
			if !strings.HasSuffix(got.SystemPromptFiles[1], tc.wantFrag) {
				t.Fatalf("fragment = %q, want suffix %q", got.SystemPromptFiles[1], tc.wantFrag)
			}
		})
	}
}

func TestWithModelPreservesPermissionsAndStepCap(t *testing.T) {
	original := PlanMode()
	rewritten := original.WithModel("qwen3.6-35b")
	if rewritten.StepCap != original.StepCap {
		t.Fatalf("StepCap drifted: got %d, want %d", rewritten.StepCap, original.StepCap)
	}
	if len(rewritten.PermissionRules) != len(original.PermissionRules) {
		t.Fatalf("permission rule count drifted: got %d, want %d", len(rewritten.PermissionRules), len(original.PermissionRules))
	}
	for tool := range original.PermissionRules {
		if _, ok := rewritten.PermissionRules[tool]; !ok {
			t.Fatalf("permission rule for %q missing after WithModel", tool)
		}
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
