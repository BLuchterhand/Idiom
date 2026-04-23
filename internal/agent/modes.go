package agent

import (
	"fmt"
	"sort"
	"strings"

	"idiom/internal/workspace"
)

// Mode is a user-selectable agent profile, OpenCode-shaped. Each Mode
// bundles a system-prompt identity, a static permission ruleset, and a
// step cap. The user picks a Mode at session start via config or CLI —
// the harness never classifies the user's prompt to pick one on their
// behalf. Modes are the single legitimate place for deterministic
// per-agent control: permissions and budgets are static config the user
// chose, not derived from interpreting their prompt.
type Mode struct {
	Name              string
	Description       string
	SystemPromptFiles []string
	PermissionRules   map[string][]workspace.ToolRule
	StepCap           int
}

// BuildMode is the default: full tool capability with a light deny-list
// for known-dangerous shell patterns. Matches OpenCode's `build` agent.
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
		},
		StepCap: 0,
	}
}

// PlanMode is read-only: edits and shell mutations are denied. The user
// picks this when they want reasoning + exploration without risk of
// changes. Matches OpenCode's `plan` agent.
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
		},
		StepCap: 64,
	}
}

// GeneralMode is a middle ground: reads and safe shell are allowed, but
// writes require confirmation. A conservative default for ambiguous
// sessions.
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
		},
		StepCap: 32,
	}
}

// DefaultModes returns the built-in Modes in a stable order.
func DefaultModes() []Mode {
	return []Mode{BuildMode(), PlanMode(), GeneralMode()}
}

// ModeByName looks up a built-in Mode. Unknown names return ok=false —
// callers should surface an error rather than silently falling back so
// the user sees that their selection was rejected.
func ModeByName(name string) (Mode, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, mode := range DefaultModes() {
		if mode.Name == name {
			return mode, true
		}
	}
	return Mode{}, false
}

// ModeOrDefault resolves a name, falling back to BuildMode. Used at
// startup where an empty config should yield sane behavior.
func ModeOrDefault(name string) Mode {
	if mode, ok := ModeByName(name); ok {
		return mode
	}
	return BuildMode()
}

// ModeNames returns the sorted list of built-in Mode names, suitable
// for --help output and validation.
func ModeNames() []string {
	modes := DefaultModes()
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		names = append(names, mode.Name)
	}
	sort.Strings(names)
	return names
}

// ModePolicy returns tool-economy nudge defaults for the given Mode. These
// are soft budgets — they inject a one-shot system message when crossed,
// never hard-stop a turn. Callers layer this with their own MaxContextChars
// etc. by copying the returned value and overriding fields.
//
// Defaults:
//   - FingerprintNudgeAt: 3 — mirrors the DoomLoopGate threshold but
//     catches non-consecutive repeats.
//   - SynthesisNudgeAt: 10 — fires on chatty turns; typical happy paths
//     finish in 3–6 calls, so 10 indicates plateau risk.
//   - ReadBudgetSoft: 8 in plan mode, 0 elsewhere. Plan mode output should
//     be a plan, not another round of reads.
func ModePolicy(mode Mode) RunPolicy {
	policy := RunPolicy{
		FingerprintNudgeAt: 3,
		SynthesisNudgeAt:   10,
	}
	if strings.EqualFold(mode.Name, "plan") {
		policy.ReadBudgetSoft = 8
	}
	return policy
}

// Validate checks a Mode for obvious construction errors. Called at
// startup so bad config fails loud instead of producing confusing
// runtime behavior.
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
