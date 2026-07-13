package toolpolicy

import (
	"path"
	"regexp"
	"strings"
)

// Decision is the outcome of a tool-rule lookup.
type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
	None  Decision = ""
)

// Rule is a single `pattern -> decision` entry.
type Rule struct {
	Tool            string   `json:"tool,omitempty"`
	Pattern         string   `json:"pattern,omitempty"`
	PathPattern     string   `json:"path_pattern,omitempty"`
	CommandPrefix   string   `json:"command_prefix,omitempty"`
	CommandContains string   `json:"command_contains,omitempty"`
	Decision        Decision `json:"decision"`
}

// PatternKind selects the matching semantics for a precomputed pattern target.
// Tool adapters own the mapping from concrete request shapes to these semantic
// kinds; the policy matcher does not know transport-specific tool names.
type PatternKind string

const (
	PatternPath    PatternKind = "path"
	PatternCommand PatternKind = "command"
)

// Input contains only the semantic values consumed by permission rules.
// Adapters must extract these values from their transport or tool request
// before invoking the pure matcher.
type Input struct {
	Tool string
	// Command may contain secrets and is matcher-only input. It must not be
	// rendered in errors, events, or logs.
	Command    string
	PathTarget string
	// PatternTarget may contain sensitive command, query, or URL text. It is
	// matcher-only input and must not be rendered in errors, events, or logs.
	PatternTarget    string
	PatternKind      PatternKind
	FallbackRuleKeys []string
}

// Policy is a flat permission policy used by child runners and other
// execution contexts whose rules must support deny-over-allow ordering.
// Map-based tool rules keep first-match behavior through Match.
type Policy struct {
	Default Decision
	Rules   []Rule
}

// DecisionResult is the result of evaluating a flat Policy.
type DecisionResult struct {
	Tool     string
	Target   string
	Decision Decision
	Rule     Rule
	Matched  bool
}

// Decide evaluates a flat policy with last-match-wins semantics. This keeps
// explicit deny tails, such as command redirection denials, stronger than earlier
// broad allow rules.
func (p Policy) Decide(input Input) DecisionResult {
	decision := p.Default
	if decision == None {
		decision = Allow
	}

	result := DecisionResult{
		Tool:     strings.TrimSpace(input.Tool),
		Target:   strings.TrimSpace(input.PathTarget),
		Decision: decision,
	}

	for _, rule := range p.Rules {
		if rule.Decision == None || !rule.Matches(input) {
			continue
		}

		result.Decision = rule.Decision
		result.Rule = rule
		result.Matched = true
	}

	return result
}

// Matches reports whether a flat policy rule applies to the semantic input.
func (r Rule) Matches(input Input) bool {
	tool := strings.TrimSpace(input.Tool)
	if r.Tool != "" && r.Tool != "*" && r.Tool != tool {
		return false
	}

	command := strings.TrimSpace(input.Command)
	if r.CommandPrefix != "" && !CommandMatchesPrefix(command, r.CommandPrefix) {
		return false
	}

	if r.CommandContains != "" && !strings.Contains(command, r.CommandContains) {
		return false
	}

	if r.PathPattern != "" {
		target := strings.TrimSpace(input.PathTarget)
		if target == "" {
			return false
		}

		ok, err := path.Match(r.PathPattern, target)
		if err != nil || !ok {
			return false
		}
	}

	if r.Pattern != "" {
		return matchPattern(input.PatternKind, r.Pattern, input.PatternTarget)
	}

	return true
}

// Match evaluates the first rule whose pattern matches the semantic input and returns
// its decision. Missing tools or empty rule sets return None.
func Match(input Input, rules map[string][]Rule) Decision {
	if len(rules) == 0 {
		return None
	}

	tool := strings.TrimSpace(input.Tool)
	keys := make([]string, 0, 1+len(input.FallbackRuleKeys))
	keys = append(keys, tool)
	keys = append(keys, input.FallbackRuleKeys...)

	for _, key := range keys {
		if decision := matchByKey(input, rules, strings.TrimSpace(key)); decision != None {
			return decision
		}
	}

	return None
}

func matchByKey(input Input, rules map[string][]Rule, key string) Decision {
	toolRules, ok := rules[key]
	if !ok || len(toolRules) == 0 {
		return None
	}

	for _, rule := range toolRules {
		if matchPattern(input.PatternKind, rule.Pattern, input.PatternTarget) {
			if rule.Decision == None {
				return Allow
			}

			return rule.Decision
		}
	}

	return None
}

func matchPattern(kind PatternKind, pattern string, target string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}

	if target == "" {
		return false
	}

	if kind == PatternCommand {
		return matchCommand(pattern, target)
	}

	return matchPath(pattern, target)
}

func matchCommand(pattern string, command string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimRight(strings.TrimSuffix(pattern, "*"), " ")
		if prefix == "" {
			return true
		}

		if command == prefix {
			return true
		}

		return strings.HasPrefix(command, prefix+" ")
	}

	return command == pattern
}

// CommandMatchesPrefix matches a command prefix on token boundaries.
func CommandMatchesPrefix(command string, prefix string) bool {
	if prefix == "" {
		return true
	}

	if strings.HasSuffix(prefix, " ") || strings.HasSuffix(prefix, "\t") {
		return strings.HasPrefix(command, prefix)
	}

	if command == prefix {
		return true
	}

	if !strings.HasPrefix(command, prefix) {
		return false
	}

	if len(command) <= len(prefix) {
		return true
	}

	return command[len(prefix)] == ' ' || command[len(prefix)] == '\t'
}

func matchPath(pattern string, path string) bool {
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

func globToRegex(pattern string) string {
	var b strings.Builder

	i := 0

	for i < len(pattern) {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
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
