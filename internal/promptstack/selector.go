package promptstack

import (
	"path/filepath"
	"strings"
)

// OpenCodePromptDir is the directory under the project root that holds
// the per-model and per-mode prompt files. Mirrors OpenCode's
// packages/opencode/src/session/prompt/ layout, with idiom tool names
// substituted for OpenCode's tool names and a discovery-first directive
// added near the top of each base prompt.
const OpenCodePromptDir = "prompts/opencode"

// SelectModelPrompt returns the relative path to the per-model base
// prompt file for the given model identifier. Mirrors OpenCode's
// session/system.ts provider() function: the model id is matched against
// substring fingerprints, and the first match wins. An unknown model id
// falls back to the generic default.md.
//
// Substring matching (not exact equality) is intentional. Real-world
// model ids look like "models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf"
// (gguf path), "claude-opus-4-5", "gpt-4o", "github/copilot-gpt-5", or
// "gemini-2.0-pro" — we want all of those to route correctly.
//
// Family routing follows OpenCode order-of-precedence so more-specific
// fingerprints (copilot, codex, beast) win over their generic siblings
// (gpt). Idiom adds qwen as the leading family because Qwen is the
// default local backend; OpenCode itself does not ship a Qwen prompt,
// so prompts/opencode/qwen.md is idiom-specific (still in the same
// shape as OpenCode's per-model bases).
func SelectModelPrompt(modelID string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case id == "":
		return promptPath("default.md")
	case strings.Contains(id, "qwen"):
		return promptPath("qwen.md")
	case strings.Contains(id, "claude"), strings.Contains(id, "anthropic"):
		return promptPath("anthropic.md")
	case strings.Contains(id, "gemini"):
		return promptPath("gemini.md")
	case strings.Contains(id, "kimi"):
		return promptPath("kimi.md")
	case strings.Contains(id, "trinity"):
		return promptPath("trinity.md")
	case strings.Contains(id, "copilot"):
		return promptPath("copilot-gpt-5.md")
	case strings.Contains(id, "codex"):
		return promptPath("codex.md")
	case strings.Contains(id, "gpt-4"), strings.Contains(id, "gpt-5"),
		strings.Contains(id, "o1"), strings.Contains(id, "o3"),
		strings.Contains(id, "beast"):
		// OpenCode routes gpt-4 / o1 / o3 to beast.txt — the autonomous,
		// long-context variant. Keep that mapping for parity.
		return promptPath("beast.md")
	case strings.Contains(id, "gpt"):
		return promptPath("gpt.md")
	default:
		return promptPath("default.md")
	}
}

// SelectModeFragment returns the relative path to the mode-specific
// fragment that should follow the per-model base prompt, or empty if the
// mode contributes no fragment. Mirrors OpenCode's plan.txt /
// build-switch.txt overlay: the base prompt is full content; the mode
// fragment is a short reminder appended on top.
//
// Plan mode resolves to plan.md by default, or plan-reminder-anthropic.md
// when the active model is from the Anthropic family. OpenCode's
// session/system.ts uses the same model-aware split because the
// Anthropic plan reminder includes the enhanced multi-phase planning
// workflow (Explore → Plan → Synthesis → Final → Exit) that is too
// dense for smaller models.
//
// Mode names are case-insensitive. Unknown names return empty (no
// fragment) rather than an error so callers can defer mode validation
// to the agent layer.
func SelectModeFragment(modeName string) string {
	return SelectModeFragmentForModel(modeName, "")
}

// SelectModeFragmentForModel is the model-aware variant. Plan mode
// returns plan-reminder-anthropic.md for Anthropic family models so the
// model-specific phased workflow is attached. Other modes ignore the
// model id.
func SelectModeFragmentForModel(modeName, modelID string) string {
	switch strings.ToLower(strings.TrimSpace(modeName)) {
	case "plan":
		id := strings.ToLower(strings.TrimSpace(modelID))
		if strings.Contains(id, "claude") || strings.Contains(id, "anthropic") {
			return promptPath("plan-reminder-anthropic.md")
		}
		return promptPath("plan.md")
	case "build":
		return promptPath("build-switch.md")
	default:
		return ""
	}
}

// MaxStepsFragmentPath returns the path to the step-ceiling reminder
// that callers should append once the per-turn tool budget is exhausted.
// The agent layer decides when to apply it; this helper just centralizes
// the path so the fragment moves with the rest of the prompt set.
func MaxStepsFragmentPath() string {
	return promptPath("max-steps.md")
}

func promptPath(name string) string {
	return filepath.ToSlash(filepath.Join(OpenCodePromptDir, name))
}
