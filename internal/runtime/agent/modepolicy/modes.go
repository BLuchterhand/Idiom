package modepolicy

import (
	"fmt"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
)

// Mode is a user-selectable agent profile. Each Mode bundles a system-prompt
// identity, a static permission ruleset, and a step cap.
type Mode struct {
	Name              string
	Description       string
	SystemPromptFiles []string
	PermissionRules   map[string][]workspace.ToolRule
	StepCap           int
}

func BuildMode() Mode {
	return Mode{
		Name:              "build",
		Description:       "Full read/write/shell capability. The default for interactive development.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]workspace.ToolRule{
			"shell": {
				{Pattern: "rm -rf /", Decision: workspace.ToolRuleDeny},
				{Pattern: "rm -rf ~", Decision: workspace.ToolRuleDeny},
				{Pattern: "rm -rf *", Decision: workspace.ToolRuleAsk},
				{Pattern: "sudo *", Decision: workspace.ToolRuleAsk},
			},
			"mcp:*": {{Pattern: "*", Decision: workspace.ToolRuleAsk}},
		},
		StepCap: 0,
	}
}

func PlanMode() Mode {
	return Mode{
		Name:              "plan",
		Description:       "Read-only. Edits and mutating shell are denied. Use when you want the agent to think, not act.",
		SystemPromptFiles: []string{"prompts/base.md", "prompts/plan-mode.md"},
		PermissionRules: map[string][]workspace.ToolRule{
			"edit_file":   {{Pattern: "**", Decision: workspace.ToolRuleDeny}},
			"write_file":  {{Pattern: "**", Decision: workspace.ToolRuleDeny}},
			"append_file": {{Pattern: "**", Decision: workspace.ToolRuleDeny}},
			"apply_patch": {{Pattern: "**", Decision: workspace.ToolRuleDeny}},
			"shell": {
				{Pattern: "git *", Decision: workspace.ToolRuleAllow},
				{Pattern: "ls *", Decision: workspace.ToolRuleAllow},
				{Pattern: "cat *", Decision: workspace.ToolRuleAllow},
				{Pattern: "grep *", Decision: workspace.ToolRuleAllow},
				{Pattern: "rg *", Decision: workspace.ToolRuleAllow},
				{Pattern: "*", Decision: workspace.ToolRuleDeny},
			},
			"mcp:*": {{Pattern: "*", Decision: workspace.ToolRuleAllow}},
		},
		StepCap: 64,
	}
}

func GeneralMode() Mode {
	return Mode{
		Name:              "general",
		Description:       "Reads freely; writes require approval. Use when you aren't sure what the agent should be allowed to do.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]workspace.ToolRule{
			"edit_file":   {{Pattern: "**", Decision: workspace.ToolRuleAsk}},
			"write_file":  {{Pattern: "**", Decision: workspace.ToolRuleAsk}},
			"append_file": {{Pattern: "**", Decision: workspace.ToolRuleAsk}},
			"apply_patch": {{Pattern: "**", Decision: workspace.ToolRuleAsk}},
			"shell": {
				{Pattern: "rm -rf *", Decision: workspace.ToolRuleDeny},
				{Pattern: "sudo *", Decision: workspace.ToolRuleDeny},
				{Pattern: "*", Decision: workspace.ToolRuleAsk},
			},
			"mcp:*": {{Pattern: "*", Decision: workspace.ToolRuleAsk}},
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

func ModeNames() []string {
	modes := DefaultModes()
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		names = append(names, mode.Name)
	}
	slices.Sort(names)
	return names
}

func (m Mode) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("agent mode name is required")
	}
	if m.StepCap < 0 {
		return fmt.Errorf("agent mode %q: step_cap must be non-negative", m.Name)
	}
	for tool, rules := range m.PermissionRules {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("agent mode %q: permission rule has empty tool name", m.Name)
		}
		for i, rule := range rules {
			switch rule.Decision {
			case workspace.ToolRuleAllow, workspace.ToolRuleAsk, workspace.ToolRuleDeny:
			case workspace.ToolRuleNone:
				return fmt.Errorf("agent mode %q: %s rule %d has empty decision", m.Name, tool, i)
			default:
				return fmt.Errorf("agent mode %q: %s rule %d has invalid decision %q", m.Name, tool, i, rule.Decision)
			}
		}
	}
	return nil
}
