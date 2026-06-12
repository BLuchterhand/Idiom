package workspace

import (
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace/toolpolicy"
	"idiom/internal/platform/config"
)

// Fix 6 (per-tool permission granularity) — OpenCode-style glob rules that
// upgrade the binary EnableWrites / EnableShell switches with per-pattern
// allow/ask/deny decisions. See docs/opencode-fixes-design.md §6.

// ToolRuleDecision is the outcome of a tool-rule lookup.
type ToolRuleDecision = toolpolicy.Decision

const (
	ToolRuleAllow = toolpolicy.Allow
	ToolRuleAsk   = toolpolicy.Ask
	ToolRuleDeny  = toolpolicy.Deny
	ToolRuleNone  = toolpolicy.None
)

// ToolRule is a single `pattern → decision` entry. Patterns are glob-style:
//   - for shell-class tools (shell, bash), the pattern is matched against
//     the command string with simple prefix-plus-wildcard semantics
//     ("git *" matches a command starting with "git ").
//   - for path-class tools (edit_file, write_file, append_file, read_file),
//     the pattern is matched against the request's path using `filepath.Match`
//     extended to accept `**` as a recursive glob.
type ToolRule = toolpolicy.Rule

// SetPermissionRules installs the active Mode's permission ruleset. Called
// at session startup (from New) and when the user switches Mode via
// /mode. Project-configured deny rules are still absolute; Mode rules are
// consulted only after a request fails to match a config deny.
func (m *Manager) SetPermissionRules(rules map[string][]ToolRule) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		m.permissionRules = nil
		return
	}

	copied := make(map[string][]ToolRule, len(rules))
	for tool, list := range rules {
		next := make([]ToolRule, len(list))
		copy(next, list)

		copied[tool] = next
	}

	m.permissionRules = copied
}

// SetProtectedWritePaths installs authority-bearing paths that require
// operator approval before mutation even in write-capable modes. It is used
// for active prompt sources: the model may propose changes, but the harness
// owns permission for mutating the instructions that shape future turns.
func (m *Manager) SetProtectedWritePaths(paths []string) {
	if m == nil {
		return
	}

	protected := make(map[string]struct{})
	for _, p := range paths {
		for _, key := range m.protectedPathKeys(p) {
			protected[key] = struct{}{}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(protected) == 0 {
		m.protectedWritePaths = nil
		return
	}

	m.protectedWritePaths = protected
}

// EvaluateConfiguredToolRule applies only the static
// config.Workspace.ToolRules layer. Callers use this for host-enforced
// absolute denies so no active Mode can override a project rule.
func (m *Manager) EvaluateConfiguredToolRule(req ToolRequest) ToolRuleDecision {
	if m == nil || len(m.cfg.ToolRules) == 0 {
		return ToolRuleNone
	}

	return MatchToolRule(strings.TrimSpace(req.Tool), req, convertConfiguredToolRules(m.cfg.ToolRules))
}

// EvaluateToolRule applies absolute project-configured deny rules first,
// then the active Mode's permission rules, then non-deny static
// config.Workspace.ToolRules as a fallback. Returns ToolRuleNone when
// neither layer matches.
func (m *Manager) EvaluateToolRule(req ToolRequest) ToolRuleDecision {
	if m == nil {
		return ToolRuleNone
	}

	tool := strings.TrimSpace(req.Tool)
	configDecision := m.EvaluateConfiguredToolRule(req)

	if configDecision == ToolRuleDeny {
		return ToolRuleDeny
	}

	m.mu.Lock()

	modeRules := m.permissionRules
	m.mu.Unlock()

	if len(modeRules) > 0 {
		if decision := MatchToolRule(tool, req, modeRules); decision != ToolRuleNone {
			if decision != ToolRuleDeny && m.protectedWritePathMatched(req) {
				return ToolRuleAsk
			}

			return decision
		}
	}

	if m.protectedWritePathMatched(req) {
		return ToolRuleAsk
	}

	if configDecision != ToolRuleNone {
		return configDecision
	}

	return ToolRuleNone
}

func (m *Manager) protectedWritePathMatched(req ToolRequest) bool {
	if m == nil || !IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	m.mu.Lock()
	protected := m.protectedWritePaths
	m.mu.Unlock()

	if len(protected) == 0 {
		return false
	}

	for _, path := range protectedWriteRequestPaths(req) {
		for _, key := range m.protectedPathKeys(path) {
			if _, ok := protected[key]; ok {
				return true
			}
		}
	}

	return false
}

func protectedWriteRequestPaths(req ToolRequest) []string {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case ToolNameApplyPatch:
		ops, err := ParsePatch(req.Content)
		if err != nil {
			return nil
		}

		paths := PatchOpPaths(ops)
		for _, op := range ops {
			if strings.TrimSpace(op.MoveTo) != "" {
				paths = append(paths, op.MoveTo)
			}
		}

		return paths
	default:
		if strings.TrimSpace(req.Path) == "" {
			return nil
		}

		return []string{req.Path}
	}
}

func (m *Manager) protectedPathKeys(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	cleaned := filepath.Clean(path)
	keys := []string{normalizePathKey(cleaned)}

	if filepath.IsAbs(cleaned) {
		if root := strings.TrimSpace(m.Root()); root != "" {
			if rel, err := filepath.Rel(root, cleaned); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				keys = append(keys, normalizePathKey(rel))
			}
		}
	} else if root := strings.TrimSpace(m.Root()); root != "" {
		keys = append(keys, normalizePathKey(filepath.Join(root, cleaned)))
	}

	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}

		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, key)
	}

	return out
}

func convertConfiguredToolRules(configRules map[string][]config.ToolRule) map[string][]ToolRule {
	if len(configRules) == 0 {
		return nil
	}

	convertedRules := make(map[string][]ToolRule, len(configRules))
	for name, rules := range configRules {
		converted := make([]ToolRule, 0, len(rules))
		for _, r := range rules {
			converted = append(converted, ToolRule{
				Pattern:  r.Pattern,
				Decision: ToolRuleDecision(strings.ToLower(strings.TrimSpace(r.Decision))),
			})
		}

		convertedRules[name] = converted
	}

	return convertedRules
}

// MatchToolRule evaluates the first rule whose pattern matches the request
// and returns its decision. A missing tool or empty rule set returns
// ToolRuleNone — callers should treat that as "defer to the legacy
// binary gate" so behavior is unchanged when the config omits this section.
//
// For `mcp:<server>/<tool>` calls there is a glob fallback: if no rules
// are registered under the literal qualified name, MatchToolRule retries
// against the catch-all key `mcp:*`. This lets users write a single
// "every MCP tool asks" rule without enumerating each server.
func MatchToolRule(tool string, req ToolRequest, rules map[string][]ToolRule) ToolRuleDecision {
	return toolpolicy.Match(tool, toolPolicyRequest(req), rules)
}

func extractToolRuleTarget(tool string, req ToolRequest) string {
	return toolpolicy.ExtractTarget(tool, toolPolicyRequest(req))
}

func toolPolicyRequest(req ToolRequest) toolpolicy.Request {
	return toolpolicy.Request{
		Command: req.Command,
		Path:    req.Path,
		Args:    req.Args,
	}
}
