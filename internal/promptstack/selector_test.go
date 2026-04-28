package promptstack

import (
	"strings"
	"testing"
)

func TestSelectModelPromptRoutesByFingerprint(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		want    string
	}{
		{"empty falls back to default", "", "default.md"},
		{"unknown falls back to default", "some-future-model-2099", "default.md"},
		{"qwen gguf path", "models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf", "qwen.md"},
		{"qwen lowercase", "qwen2.5-coder", "qwen.md"},
		{"qwen mlx hf id", "unsloth/Qwen3.6-27B-UD-MLX-4bit", "qwen.md"},
		{"claude opus", "claude-opus-4-5", "anthropic.md"},
		{"anthropic in id", "anthropic/claude-3.5-sonnet", "anthropic.md"},
		{"gemini pro", "gemini-2.0-pro", "gemini.md"},
		{"kimi", "moonshot-kimi-k2", "kimi.md"},
		{"trinity", "trinity-mini", "trinity.md"},
		{"copilot beats gpt prefix", "github/copilot-gpt-5", "copilot-gpt-5.md"},
		{"codex beats gpt", "openai/codex-1", "codex.md"},
		{"gpt-4o → beast", "gpt-4o", "beast.md"},
		{"gpt-5 → beast", "gpt-5", "beast.md"},
		{"openai o1 → beast", "o1-preview", "beast.md"},
		{"openai o3 → beast", "o3-mini", "beast.md"},
		{"gpt-3.5 → gpt", "gpt-3.5-turbo", "gpt.md"},
		{"plain gpt", "gpt-something", "gpt.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectModelPrompt(tc.modelID)
			if !strings.HasSuffix(got, tc.want) {
				t.Fatalf("SelectModelPrompt(%q) = %q, want suffix %q", tc.modelID, got, tc.want)
			}
			if !strings.HasPrefix(got, OpenCodePromptDir+"/") {
				t.Fatalf("SelectModelPrompt(%q) = %q, want prefix %q", tc.modelID, got, OpenCodePromptDir+"/")
			}
		})
	}
}

func TestSelectModeFragmentReturnsExpectedPath(t *testing.T) {
	tests := []struct {
		mode string
		want string
	}{
		{"plan", "plan.md"},
		{"PLAN", "plan.md"},
		{"build", "build-switch.md"},
		{"Build ", "build-switch.md"},
		{"general", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			got := SelectModeFragment(tc.mode)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("SelectModeFragment(%q) = %q, want empty", tc.mode, got)
				}
				return
			}
			if !strings.HasSuffix(got, tc.want) {
				t.Fatalf("SelectModeFragment(%q) = %q, want suffix %q", tc.mode, got, tc.want)
			}
			if !strings.HasPrefix(got, OpenCodePromptDir+"/") {
				t.Fatalf("SelectModeFragment(%q) = %q, want prefix %q", tc.mode, got, OpenCodePromptDir+"/")
			}
		})
	}
}

func TestSelectModeFragmentForModelSwapsAnthropicPlanReminder(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		modelID string
		want    string
	}{
		{"plan + claude → anthropic plan reminder", "plan", "claude-opus-4-5", "plan-reminder-anthropic.md"},
		{"plan + anthropic in id", "plan", "anthropic/claude-3.5-sonnet", "plan-reminder-anthropic.md"},
		{"plan + qwen → generic plan", "plan", "qwen3.6-35b", "plan.md"},
		{"plan + gpt → generic plan", "plan", "gpt-4o", "plan.md"},
		{"plan + empty → generic plan", "plan", "", "plan.md"},
		{"build + claude → build-switch (model ignored)", "build", "claude-opus-4-5", "build-switch.md"},
		{"general + anything → empty", "general", "claude-opus-4-5", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectModeFragmentForModel(tc.mode, tc.modelID)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("SelectModeFragmentForModel(%q, %q) = %q, want empty", tc.mode, tc.modelID, got)
				}
				return
			}
			if !strings.HasSuffix(got, tc.want) {
				t.Fatalf("SelectModeFragmentForModel(%q, %q) = %q, want suffix %q", tc.mode, tc.modelID, got, tc.want)
			}
		})
	}
}

func TestMaxStepsFragmentPathStableLocation(t *testing.T) {
	got := MaxStepsFragmentPath()
	if !strings.HasSuffix(got, "max-steps.md") {
		t.Fatalf("MaxStepsFragmentPath() = %q, want suffix max-steps.md", got)
	}
	if !strings.HasPrefix(got, OpenCodePromptDir+"/") {
		t.Fatalf("MaxStepsFragmentPath() = %q, want prefix %q", got, OpenCodePromptDir+"/")
	}
}
