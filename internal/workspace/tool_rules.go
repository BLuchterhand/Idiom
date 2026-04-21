package workspace

import (
	"regexp"
	"strings"
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
// /mode. Mode rules are consulted first; the Workspace config's ToolRules
// block remains a fallback for tools the Mode doesn't cover.
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

// EvaluateToolRule applies the Mode's permission rules first, then falls
// back to the static config.Workspace.ToolRules. Returns ToolRuleNone when
// neither layer matches.
func (m *Manager) EvaluateToolRule(req ToolRequest) ToolRuleDecision {
	if m == nil {
		return ToolRuleNone
	}
	tool := strings.TrimSpace(req.Tool)
	m.mu.Lock()
	modeRules := m.permissionRules
	m.mu.Unlock()
	if len(modeRules) > 0 {
		if decision := MatchToolRule(tool, req, modeRules); decision != ToolRuleNone {
			return decision
		}
	}
	if len(m.cfg.ToolRules) == 0 {
		return ToolRuleNone
	}
	fallback := make(map[string][]ToolRule, len(m.cfg.ToolRules))
	for name, configRules := range m.cfg.ToolRules {
		converted := make([]ToolRule, 0, len(configRules))
		for _, r := range configRules {
			converted = append(converted, ToolRule{
				Pattern:  r.Pattern,
				Decision: ToolRuleDecision(strings.ToLower(strings.TrimSpace(r.Decision))),
			})
		}
		fallback[name] = converted
	}
	return MatchToolRule(tool, req, fallback)
}

// MatchToolRule evaluates the first rule whose pattern matches the request
// and returns its decision. A missing tool or empty rule set returns
// ToolRuleNone — callers should treat that as "defer to the legacy
// binary gate" so behavior is unchanged when the config omits this section.
func MatchToolRule(tool string, req ToolRequest, rules map[string][]ToolRule) ToolRuleDecision {
	if len(rules) == 0 {
		return ToolRuleNone
	}
	toolRules, ok := rules[tool]
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
	switch tool {
	case "shell", "bash":
		return strings.TrimSpace(req.Command)
	default:
		return strings.TrimSpace(req.Path)
	}
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
