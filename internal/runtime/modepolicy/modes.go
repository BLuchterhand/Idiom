package modepolicy

import (
	"strings"

	"idiom/internal/contracts/toolpolicy"
)

// Mode is a user-selectable agent profile. Each Mode bundles a system-prompt
// identity, a static permission ruleset, and a step cap.
type Mode struct {
	Name              string
	Description       string
	SystemPromptFiles []string
	PermissionRules   map[string][]toolpolicy.Rule
	StepCap           int
}

func BuildMode() Mode {
	return Mode{
		Name:              "build",
		Description:       "Full read/write/shell capability. The default for interactive development.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]toolpolicy.Rule{
			"shell": {
				{Pattern: "rm -rf /", Decision: toolpolicy.Deny},
				{Pattern: "rm -rf ~", Decision: toolpolicy.Deny},
				{Pattern: "rm -rf *", Decision: toolpolicy.Ask},
				{Pattern: "sudo *", Decision: toolpolicy.Ask},
			},
			"mcp:*": {{Pattern: "*", Decision: toolpolicy.Ask}},
		},
		StepCap: 0,
	}
}

func PlanMode() Mode {
	return Mode{
		Name:              "plan",
		Description:       "Read-only. Edits and mutating shell are denied. Use when you want the agent to think, not act.",
		SystemPromptFiles: []string{"prompts/base.md", "prompts/plan-mode.md"},
		PermissionRules: map[string][]toolpolicy.Rule{
			"edit_file":   {{Pattern: "**", Decision: toolpolicy.Deny}},
			"write_file":  {{Pattern: "**", Decision: toolpolicy.Deny}},
			"append_file": {{Pattern: "**", Decision: toolpolicy.Deny}},
			"apply_patch": {{Pattern: "**", Decision: toolpolicy.Deny}},
			"shell": {
				{Pattern: "git *", Decision: toolpolicy.Allow},
				{Pattern: "ls *", Decision: toolpolicy.Allow},
				{Pattern: "cat *", Decision: toolpolicy.Allow},
				{Pattern: "grep *", Decision: toolpolicy.Allow},
				{Pattern: "rg *", Decision: toolpolicy.Allow},
				{Pattern: "*", Decision: toolpolicy.Deny},
			},
			"mcp:*": {{Pattern: "*", Decision: toolpolicy.Allow}},
		},
		StepCap: 64,
	}
}

func GeneralMode() Mode {
	return Mode{
		Name:              "general",
		Description:       "Reads freely; writes require approval. Use when you aren't sure what the agent should be allowed to do.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]toolpolicy.Rule{
			"edit_file":   {{Pattern: "**", Decision: toolpolicy.Ask}},
			"write_file":  {{Pattern: "**", Decision: toolpolicy.Ask}},
			"append_file": {{Pattern: "**", Decision: toolpolicy.Ask}},
			"apply_patch": {{Pattern: "**", Decision: toolpolicy.Ask}},
			"shell": {
				{Pattern: "rm -rf *", Decision: toolpolicy.Deny},
				{Pattern: "sudo *", Decision: toolpolicy.Deny},
				{Pattern: "*", Decision: toolpolicy.Ask},
			},
			"mcp:*": {{Pattern: "*", Decision: toolpolicy.Ask}},
		},
		StepCap: 32,
	}
}

func DefaultModes() []Mode {
	return []Mode{BuildMode(), PlanMode(), GeneralMode()}
}

func ModeByName(name string) (Mode, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, mode := range DefaultModes() {
		if mode.Name == name {
			return mode, true
		}
	}

	return Mode{}, false
}

func ModeOrDefault(name string) Mode {
	if mode, ok := ModeByName(name); ok {
		return mode
	}

	return BuildMode()
}
