package workspace

import (
	"encoding/json"
	"regexp"
	"strings"

	"idiom/internal/config"
)

// Fix 6 (per-tool permission granularity) — OpenCode-style glob rules that
// upgrade the binary EnableWrites / EnableShell switches with per-pattern
// allow/ask/deny decisions. See docs/opencode-fixes-design.md §6.

// ToolRuleDecision is the outcome of a tool-rule lookup.
type ToolRuleDecision string

const (
	ToolRuleAllow ToolRuleDecision = "allow"
	ToolRuleAsk   ToolRuleDecision = "ask"
	ToolRuleDeny  ToolRuleDecision = "deny"
	ToolRuleNone  ToolRuleDecision = ""
)

// ToolRule is a single `pattern → decision` entry. Patterns are glob-style:
//   - for shell-class tools (shell, bash), the pattern is matched against
//     the command string with simple prefix-plus-wildcard semantics
//     ("git *" matches a command starting with "git ").
//   - for path-class tools (edit_file, write_file, append_file, read_file),
//     the pattern is matched against the request's path using `filepath.Match`
//     extended to accept `**` as a recursive glob.
type ToolRule struct {
	Pattern  string           `json:"pattern"`
	Decision ToolRuleDecision `json:"decision"`
}

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
			return decision
		}
	}
	if configDecision != ToolRuleNone {
		return configDecision
	}
	return ToolRuleNone
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
	if len(rules) == 0 {
		return ToolRuleNone
	}
	if decision := matchToolRuleByKey(tool, req, rules, tool); decision != ToolRuleNone {
		return decision
	}
	if strings.HasPrefix(tool, "mcp:") && tool != "mcp:*" {
		if decision := matchToolRuleByKey(tool, req, rules, "mcp:*"); decision != ToolRuleNone {
			return decision
		}
	}
	return ToolRuleNone
}

// matchToolRuleByKey looks up rules under the given key and applies the
// pattern matcher. tool is still the original tool name — used by the
// pattern matcher to pick shell vs. path semantics.
func matchToolRuleByKey(tool string, req ToolRequest, rules map[string][]ToolRule, key string) ToolRuleDecision {
	toolRules, ok := rules[key]
	if !ok || len(toolRules) == 0 {
		return ToolRuleNone
	}
	target := extractToolRuleTarget(tool, req)
	for _, rule := range toolRules {
		if matchToolRulePattern(tool, rule.Pattern, target) {
			if rule.Decision == ToolRuleNone {
				return ToolRuleAllow
			}
			return rule.Decision
		}
	}
	return ToolRuleNone
}

func extractToolRuleTarget(tool string, req ToolRequest) string {
	switch {
	case tool == "shell" || tool == "bash":
		return strings.TrimSpace(req.Command)
	case strings.HasPrefix(tool, "mcp:"):
		// MCP tools have arbitrary args; we cannot type-match. Use the
		// most human-meaningful field present — `query` for search-shaped
		// tools, `url` for fetch-shaped ones — so users can still write
		// patterns like "https://github.com/*" or "go 1.24*".
		if q := stringFieldFromRawJSON(req.Args, "query"); q != "" {
			return q
		}
		if u := stringFieldFromRawJSON(req.Args, "url"); u != "" {
			return u
		}
		return ""
	default:
		return strings.TrimSpace(req.Path)
	}
}

// stringFieldFromRawJSON extracts one top-level string field from raw
// JSON bytes without unmarshaling the whole blob into a typed struct.
// Returns "" when the field is missing, non-string, or the bytes are
// not a JSON object.
func stringFieldFromRawJSON(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	rawVal, ok := probe[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(rawVal, &s); err != nil {
		return ""
	}
	return s
}

func matchToolRulePattern(tool string, pattern string, target string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	if target == "" {
		return false
	}
	switch tool {
	case "shell", "bash":
		return matchShellRule(pattern, target)
	default:
		return matchPathRule(pattern, target)
	}
}

// matchShellRule implements the simple "git *" / "rm -rf *" style prefix
// match. A trailing "*" means "anything after this prefix".
func matchShellRule(pattern string, command string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimRight(strings.TrimSuffix(pattern, "*"), " ")
		if prefix == "" {
			return true
		}
		if command == prefix {
			return true
		}
		if strings.HasPrefix(command, prefix+" ") {
			return true
		}
		return false
	}
	return command == pattern
}

// matchPathRule supports `**` (any depth of path segments) and `*` (any
// characters within a single segment) on path patterns. Matching is
// case-insensitive and separator-normalized to forward slashes.
func matchPathRule(pattern string, path string) bool {
	pat := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(pattern), "\\", "/"))
	tgt := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	if pat == "" {
		return false
	}
	regex := "^" + globToRegex(pat) + "$"
	compiled, err := regexp.Compile(regex)
	if err != nil {
		return false
	}
	return compiled.MatchString(tgt)
}

// globToRegex converts a shell-style glob with `**` / `*` into an anchored
// regex body. `**/` (or standalone `**`) matches any depth including zero;
// `*` matches anything that isn't a path separator; `?` matches one non-
// separator char; any other character is escaped literally.
func globToRegex(pattern string) string {
	var b strings.Builder
	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// Handle "**/" and plain "**".
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString(`(?:.*/)?`)
					i += 3
					continue
				}
				b.WriteString(`.*`)
				i += 2
				continue
			}
			b.WriteString(`[^/]*`)
			i++
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return b.String()
}
