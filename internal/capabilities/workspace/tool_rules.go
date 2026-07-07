package workspace

import (
	"strings"

	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

// Fix 6 (per-tool permission granularity) — OpenCode-style glob rules that
// upgrade the binary EnableWrites / EnableShell switches with per-pattern
// allow/ask/deny decisions. See docs/opencode-fixes-design.md §6.

// SetPermissionRules installs the active Mode's permission ruleset. Called
// at session startup (from New) and when the user switches Mode via
// /mode. Project-configured deny rules are still absolute; Mode rules are
// consulted only after a request fails to match a config deny.
func (m *Manager) SetPermissionRules(rules map[string][]toolpolicy.Rule) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(rules) == 0 {
		m.permissionRules = nil
		return
	}

	copied := make(map[string][]toolpolicy.Rule, len(rules))
	for tool, list := range rules {
		next := make([]toolpolicy.Rule, len(list))
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
		for _, key := range m.pathAccess.ProtectedPathKeys(p) {
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
func (m *Manager) EvaluateConfiguredToolRule(req ToolRequest) toolpolicy.Decision {
	if m == nil || len(m.cfg.ToolRules) == 0 {
		return toolpolicy.None
	}

	return toolpolicy.Match(strings.TrimSpace(req.Tool), toolPolicyRequest(req), config.ToolPolicyRules(m.cfg.ToolRules))
}

// EvaluateToolRule applies absolute project-configured deny rules first,
// then the active Mode's permission rules, then non-deny static
// config.Workspace.ToolRules as a fallback. Returns toolpolicy.None when
// neither layer matches.
func (m *Manager) EvaluateToolRule(req ToolRequest) toolpolicy.Decision {
	if m == nil {
		return toolpolicy.None
	}

	tool := strings.TrimSpace(req.Tool)
	configDecision := m.EvaluateConfiguredToolRule(req)

	if configDecision == toolpolicy.Deny {
		return toolpolicy.Deny
	}

	m.mu.Lock()

	modeRules := m.permissionRules
	m.mu.Unlock()

	if len(modeRules) > 0 {
		if decision := toolpolicy.Match(tool, toolPolicyRequest(req), modeRules); decision != toolpolicy.None {
			if decision != toolpolicy.Deny && m.protectedWritePathMatched(req) {
				return toolpolicy.Ask
			}

			return decision
		}
	}

	if m.protectedWritePathMatched(req) {
		return toolpolicy.Ask
	}

	if configDecision != toolpolicy.None {
		return configDecision
	}

	return toolpolicy.None
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
		for _, key := range m.pathAccess.ProtectedPathKeys(path) {
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
		ops, err := patching.Parse(req.Content)
		if err != nil {
			return nil
		}

		paths := patching.OpPaths(ops)
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

func extractToolRuleTarget(tool string, req ToolRequest) string {
	return toolpolicy.ExtractTarget(tool, toolPolicyRequest(req))
}

func toolPolicyRequest(req ToolRequest) toolpolicy.Request {
	return toolpolicy.Request{
		Tool:        req.Tool,
		Command:     req.Command,
		Path:        req.Path,
		Destination: req.Destination,
		Workdir:     req.Workdir,
		Moves:       toolPolicyMoves(req.Moves),
		Args:        req.Args,
	}
}

func toolPolicyMoves(moves []GoDeclarationMove) []toolpolicy.Move {
	if len(moves) == 0 {
		return nil
	}

	out := make([]toolpolicy.Move, 0, len(moves))
	for _, move := range moves {
		out = append(out, toolpolicy.Move{Destination: move.Destination})
	}

	return out
}
