package workspace

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

const (
	legacyToolPolicyCommandNameBash = "bash"
	mcpToolPolicyFallbackRuleKey    = "mcp:*"
)

// ToolRuleDecision is the workspace-owned result of evaluating map-based tool
// rules. Target is the caller-requested path that produced the aggregate
// decision; command and MCP targets use a correlation-safe display label.
type ToolRuleDecision struct {
	Tool     string
	Target   string
	Decision toolpolicy.Decision
}

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
// absolute denies so no active Mode can override a project rule. Compound
// requests retain the requested target that caused the aggregate decision so
// enforcement and audit evidence describe the same path.
func (m *Manager) EvaluateConfiguredToolRule(req ToolRequest) ToolRuleDecision {
	if m == nil || len(m.cfg.ToolRules) == 0 {
		return ToolRuleDecision{}
	}

	return m.evaluateToolRules(req, config.ToolPolicyRules(m.cfg.ToolRules))
}

// EvaluateToolRule applies absolute project-configured deny rules first,
// then the active Mode's permission rules, then non-deny static
// config.Workspace.ToolRules as a fallback. Returns toolpolicy.None when
// neither layer matches.
func (m *Manager) EvaluateToolRule(req ToolRequest) toolpolicy.Decision {
	if m == nil {
		return toolpolicy.None
	}

	configDecision := m.EvaluateConfiguredToolRule(req)

	if configDecision.Decision == toolpolicy.Deny {
		return toolpolicy.Deny
	}

	m.mu.Lock()

	modeRules := m.permissionRules
	m.mu.Unlock()

	if len(modeRules) > 0 {
		if decision := m.evaluateToolRules(req, modeRules); decision.Decision != toolpolicy.None {
			if decision.Decision != toolpolicy.Deny && m.protectedWritePathMatched(req) {
				return toolpolicy.Ask
			}

			return decision.Decision
		}
	}

	if m.protectedWritePathMatched(req) {
		return toolpolicy.Ask
	}

	if configDecision.Decision != toolpolicy.None {
		return configDecision.Decision
	}

	return toolpolicy.None
}

// EvaluatePermissionPolicy applies a flat permission policy to every semantic
// path in a request. Canonical workspace targets may make a decision more
// restrictive, but the returned target remains the path the caller requested.
// This keeps compound writes and symlink aliases from bypassing a path rule
// while leaving the pure toolpolicy matcher independent of workspace details.
func (m *Manager) EvaluatePermissionPolicy(policy toolpolicy.Policy, req ToolRequest) toolpolicy.DecisionResult {
	input := ToolPolicyInput(req)
	if input.PatternKind != toolpolicy.PatternPath || strings.HasPrefix(input.Tool, "mcp:") {
		return policy.Decide(input)
	}

	references := toolPolicyPathReferences(req)
	if len(references) == 0 {
		return policy.Decide(input)
	}

	var aggregate toolpolicy.DecisionResult
	hasDecision := false
	consider := func(candidate toolpolicy.DecisionResult, requestedTarget string) bool {
		candidate.Target = requestedTarget
		if !hasDecision || permissionDecisionPriority(candidate.Decision) > permissionDecisionPriority(aggregate.Decision) {
			aggregate = candidate
			hasDecision = true
		}

		return aggregate.Decision == toolpolicy.Deny
	}

	for _, reference := range references {
		requestedTarget := strings.ReplaceAll(strings.TrimSpace(reference.path), "\\", "/")
		requestedInput := input
		requestedInput.PathTarget = requestedTarget
		requestedInput.PatternTarget = requestedTarget
		if consider(policy.Decide(requestedInput), requestedTarget) {
			return aggregate
		}

		if m == nil {
			continue
		}

		for _, canonicalTarget := range m.pathAccess.canonicalWorkspacePathKeys(reference.path, reference.workdir) {
			canonicalInput := requestedInput
			canonicalInput.PathTarget = canonicalTarget
			canonicalInput.PatternTarget = canonicalTarget
			canonicalDecision := policy.Decide(canonicalInput)
			if !canonicalDecision.Matched || canonicalDecision.Decision == toolpolicy.Allow {
				continue
			}
			if consider(canonicalDecision, requestedTarget) {
				return aggregate
			}
		}
	}

	return aggregate
}

func permissionDecisionPriority(decision toolpolicy.Decision) int {
	switch decision {
	case toolpolicy.Deny:
		return 3
	case toolpolicy.Ask:
		return 2
	case toolpolicy.Allow:
		return 1
	default:
		return 0
	}
}

// evaluateToolRules preserves the request-facing rule result while making
// canonical workspace targets participate in restrictive decisions. A
// symlink alias may be safe to read, but it must not turn a protected target
// into an unprotected matcher key. Canonical allows are deliberately ignored:
// an alias cannot gain authority that its requested path did not have.
func (m *Manager) evaluateToolRules(req ToolRequest, rules map[string][]toolpolicy.Rule) ToolRuleDecision {
	input := ToolPolicyInput(req)
	if input.PatternKind != toolpolicy.PatternPath || strings.HasPrefix(input.Tool, "mcp:") {
		decision := toolpolicy.Match(input, rules)
		if decision == toolpolicy.None {
			return ToolRuleDecision{}
		}

		return ToolRuleDecision{Tool: input.Tool, Target: toolPolicyDisplayTarget(req), Decision: decision}
	}

	references := toolPolicyPathReferences(req)
	if len(references) == 0 {
		decision := toolpolicy.Match(input, rules)
		if decision == toolpolicy.None {
			return ToolRuleDecision{}
		}

		return ToolRuleDecision{Tool: input.Tool, Target: toolPolicyDisplayTarget(req), Decision: decision}
	}

	allRequestedTargetsAllowed := true
	firstAllowedTarget := ""
	overall := ToolRuleDecision{Tool: input.Tool}
	for _, reference := range references {
		requestedTarget := strings.ReplaceAll(strings.TrimSpace(reference.path), "\\", "/")
		requestedInput := input
		requestedInput.PathTarget = requestedTarget
		requestedInput.PatternTarget = requestedTarget
		targetDecision := toolpolicy.Match(requestedInput, rules)
		if targetDecision == toolpolicy.Deny {
			return ToolRuleDecision{Tool: input.Tool, Target: requestedTarget, Decision: toolpolicy.Deny}
		}

		for _, canonicalTarget := range m.pathAccess.canonicalWorkspacePathKeys(reference.path, reference.workdir) {
			canonicalInput := requestedInput
			canonicalInput.PathTarget = canonicalTarget
			canonicalInput.PatternTarget = canonicalTarget
			switch toolpolicy.Match(canonicalInput, rules) {
			case toolpolicy.Deny:
				return ToolRuleDecision{Tool: input.Tool, Target: requestedTarget, Decision: toolpolicy.Deny}
			case toolpolicy.Ask:
				targetDecision = toolpolicy.Ask
			}
		}

		switch targetDecision {
		case toolpolicy.Deny:
			return ToolRuleDecision{Tool: input.Tool, Target: requestedTarget, Decision: toolpolicy.Deny}
		case toolpolicy.Ask:
			if overall.Decision != toolpolicy.Ask {
				overall.Target = requestedTarget
			}
			overall.Decision = toolpolicy.Ask
			allRequestedTargetsAllowed = false
		case toolpolicy.Allow:
			// An allow applies only to this requested target. Every target in a
			// multi-path operation must be allowed before the request gains allow
			// authority from this rule layer.
			if firstAllowedTarget == "" {
				firstAllowedTarget = requestedTarget
			}
		default:
			allRequestedTargetsAllowed = false
		}
	}

	if overall.Decision == toolpolicy.Ask {
		return overall
	}
	if allRequestedTargetsAllowed {
		return ToolRuleDecision{Tool: input.Tool, Target: firstAllowedTarget, Decision: toolpolicy.Allow}
	}

	return ToolRuleDecision{}
}

func (m *Manager) protectedWritePathMatched(req ToolRequest) bool {
	if m == nil {
		return false
	}

	m.mu.Lock()
	protected := m.protectedWritePaths
	m.mu.Unlock()

	if len(protected) == 0 {
		return false
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameShell || tool == legacyToolPolicyCommandNameBash || tool == ToolNameContextTracePack {
		// An arbitrary command can mutate any workspace path, and a partial
		// command parser cannot prove its target set. Preserve read-only
		// inspection without allowing unknown or mutating commands to bypass the
		// authority that protects active prompt sources.
		return AnalyzeShellCommand(req.Command).SideEffect != autonomy.SideEffectReadOnly
	}
	if !IsWorkspaceWriteTool(tool) {
		return false
	}

	for _, reference := range toolPolicyPathReferences(req) {
		for _, key := range m.pathAccess.canonicalWorkspacePathKeys(reference.path, reference.workdir) {
			if _, ok := protected[key]; ok {
				return true
			}
		}
	}

	return false
}

// ToolPolicyInput translates the workspace-owned ToolRequest into the
// semantic values consumed by the pure permission-rule matcher.
func ToolPolicyInput(req ToolRequest) toolpolicy.Input {
	tool := strings.TrimSpace(req.Tool)
	input := toolpolicy.Input{
		Tool:          tool,
		Command:       strings.TrimSpace(req.Command),
		PathTarget:    toolPolicyPathTarget(req),
		PatternKind:   toolpolicy.PatternPath,
		PatternTarget: toolPolicyPathTarget(req),
	}

	switch {
	case tool == ToolNameShell || tool == legacyToolPolicyCommandNameBash:
		input.PatternKind = toolpolicy.PatternCommand
		input.PatternTarget = input.Command
	case strings.HasPrefix(tool, "mcp:"):
		input.PatternTarget = mcpToolPolicyTarget(req.Args)
		if tool != mcpToolPolicyFallbackRuleKey {
			input.FallbackRuleKeys = []string{mcpToolPolicyFallbackRuleKey}
		}
	}

	return input
}

// toolPolicyDisplayTarget returns a correlation-safe description for policy
// errors and events. Raw commands and MCP arguments remain matcher-only data.
func toolPolicyDisplayTarget(req ToolRequest) string {
	input := ToolPolicyInput(req)
	tool := strings.TrimSpace(input.Tool)

	switch {
	case input.PatternKind == toolpolicy.PatternCommand:
		return toolPolicySensitiveTargetLabel("shell command", input.PatternTarget)
	case strings.HasPrefix(tool, "mcp:"):
		return toolPolicySensitiveTargetLabel("MCP request", input.PatternTarget)
	case strings.TrimSpace(input.PathTarget) != "":
		return input.PathTarget
	case strings.TrimSpace(input.PatternTarget) != "":
		return input.PatternTarget
	default:
		return "<request>"
	}
}

func toolPolicySensitiveTargetLabel(label string, target string) string {
	label = strings.TrimSpace(label)
	if strings.TrimSpace(target) == "" {
		return label
	}

	digest := sha256.Sum256([]byte(target))

	return fmt.Sprintf("%s [sha256:%x]", label, digest[:8])
}

func toolPolicyPathTarget(req ToolRequest) string {
	for _, value := range []string{req.Path, req.Destination, req.Workdir} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return strings.ReplaceAll(trimmed, "\\", "/")
		}
	}
	if len(req.Moves) > 0 {
		return strings.ReplaceAll(strings.TrimSpace(req.Moves[0].Destination), "\\", "/")
	}

	return ""
}

type toolPolicyPathReference struct {
	path    string
	workdir string
}

func toolPolicyPathReferences(req ToolRequest) []toolPolicyPathReference {
	var paths []string
	analysis, err := analyzeMutationRequestShape(req)
	if err == nil && analysis.Mutation {
		for _, target := range analysis.Targets {
			paths = append(paths, target.RequestedPath)
		}
	} else {
		paths = append(paths, req.Path, req.Destination)
		for _, move := range req.Moves {
			paths = append(paths, move.Destination)
		}
	}

	references := make([]toolPolicyPathReference, 0, len(paths)+1)
	seen := make(map[string]struct{}, len(paths)+1)
	add := func(path string, workdir string) {
		path = strings.TrimSpace(path)
		workdir = strings.TrimSpace(workdir)
		if path == "" {
			return
		}

		key := normalizePathKey(path) + "\x00" + normalizePathKey(workdir)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		references = append(references, toolPolicyPathReference{path: path, workdir: workdir})
	}

	for _, path := range paths {
		add(path, req.Workdir)
	}
	if len(references) == 0 && strings.TrimSpace(req.Workdir) != "" {
		add(req.Workdir, "")
	}

	return references
}

func mcpToolPolicyTarget(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}

	if query := toolPolicyJSONStringField(fields["query"]); query != "" {
		return query
	}

	return toolPolicyJSONStringField(fields["url"])
}

func toolPolicyJSONStringField(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		return ""
	}

	return result
}
