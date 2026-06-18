package modepolicy

import (
	"fmt"
	"slices"
	"strings"
)

type ToolRuleDecision string

const (
	ToolRuleAllow ToolRuleDecision = "allow"
	ToolRuleAsk   ToolRuleDecision = "ask"
	ToolRuleDeny  ToolRuleDecision = "deny"
	ToolRuleNone  ToolRuleDecision = ""
)

type ToolRule struct {
	Pattern  string
	Decision ToolRuleDecision
}

// Mode is a user-selectable agent profile. Each Mode bundles a system-prompt
// identity, a static permission ruleset, and a step cap.
type Mode struct {
	Name              string
	Description       string
	SystemPromptFiles []string
	PermissionRules   map[string][]ToolRule
	StepCap           int
}

func BuildMode() Mode {
	return Mode{
		Name:              "build",
		Description:       "Full read/write/shell capability. The default for interactive development.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]ToolRule{
			"shell": {
				{Pattern: "rm -rf /", Decision: ToolRuleDeny},
				{Pattern: "rm -rf ~", Decision: ToolRuleDeny},
				{Pattern: "rm -rf *", Decision: ToolRuleAsk},
				{Pattern: "sudo *", Decision: ToolRuleAsk},
			},
			"mcp:*": {{Pattern: "*", Decision: ToolRuleAsk}},
		},
		StepCap: 0,
	}
}

func PlanMode() Mode {
	return Mode{
		Name:              "plan",
		Description:       "Read-only. Edits and mutating shell are denied. Use when you want the agent to think, not act.",
		SystemPromptFiles: []string{"prompts/base.md", "prompts/plan-mode.md"},
		PermissionRules: map[string][]ToolRule{
			"edit_file":   {{Pattern: "**", Decision: ToolRuleDeny}},
			"write_file":  {{Pattern: "**", Decision: ToolRuleDeny}},
			"append_file": {{Pattern: "**", Decision: ToolRuleDeny}},
			"apply_patch": {{Pattern: "**", Decision: ToolRuleDeny}},
			"shell": {
				{Pattern: "git *", Decision: ToolRuleAllow},
				{Pattern: "ls *", Decision: ToolRuleAllow},
				{Pattern: "cat *", Decision: ToolRuleAllow},
				{Pattern: "grep *", Decision: ToolRuleAllow},
				{Pattern: "rg *", Decision: ToolRuleAllow},
				{Pattern: "*", Decision: ToolRuleDeny},
			},
			"mcp:*": {{Pattern: "*", Decision: ToolRuleAllow}},
		},
		StepCap: 64,
	}
}

func GeneralMode() Mode {
	return Mode{
		Name:              "general",
		Description:       "Reads freely; writes require approval. Use when you aren't sure what the agent should be allowed to do.",
		SystemPromptFiles: []string{"prompts/base.md"},
		PermissionRules: map[string][]ToolRule{
			"edit_file":   {{Pattern: "**", Decision: ToolRuleAsk}},
			"write_file":  {{Pattern: "**", Decision: ToolRuleAsk}},
			"append_file": {{Pattern: "**", Decision: ToolRuleAsk}},
			"apply_patch": {{Pattern: "**", Decision: ToolRuleAsk}},
			"shell": {
				{Pattern: "rm -rf *", Decision: ToolRuleDeny},
				{Pattern: "sudo *", Decision: ToolRuleDeny},
				{Pattern: "*", Decision: ToolRuleAsk},
			},
			"mcp:*": {{Pattern: "*", Decision: ToolRuleAsk}},
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
			case ToolRuleAllow, ToolRuleAsk, ToolRuleDeny:
			case ToolRuleNone:
				return fmt.Errorf("agent mode %q: %s rule %d has empty decision", m.Name, tool, i)
			default:
				return fmt.Errorf("agent mode %q: %s rule %d has invalid decision %q", m.Name, tool, i, rule.Decision)
			}
		}
	}

	return nil
}
