package toolpolicy

import (
	"encoding/json"
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

const (
	toolNameShell = "shell"
	toolNameBash  = "bash"
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

// Request is the subset of a workspace tool request needed to evaluate
// permission-rule targets. It keeps rule matching independent from the
// full workspace tool DTO.
type Request struct {
	Tool        string
	Command     string
	Path        string
	Destination string
	Workdir     string
	Moves       []Move
	Args        json.RawMessage
}

// Move is the subset of a structured move request needed for permission
// target selection.
type Move struct {
	Destination string
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
// explicit deny tails, such as shell redirection denials, stronger than earlier
// broad allow rules.
func (p Policy) Decide(req Request) DecisionResult {
	decision := p.Default
	if decision == None {
		decision = Allow
	}

	result := DecisionResult{
		Tool:     strings.TrimSpace(req.Tool),
		Target:   Target(req),
		Decision: decision,
	}

	for _, rule := range p.Rules {
		if rule.Decision == None || !rule.Matches(req) {
			continue
		}

		result.Decision = rule.Decision
		result.Rule = rule
		result.Matched = true
	}

	return result
}

// Matches reports whether a flat policy rule applies to the request.
func (r Rule) Matches(req Request) bool {
	tool := strings.TrimSpace(req.Tool)
	if r.Tool != "" && r.Tool != "*" && r.Tool != tool {
		return false
	}

	command := strings.TrimSpace(req.Command)
	if r.CommandPrefix != "" && !CommandMatchesPrefix(command, r.CommandPrefix) {
		return false
	}

	if r.CommandContains != "" && !strings.Contains(command, r.CommandContains) {
		return false
	}

	if r.PathPattern != "" {
		target := Target(req)
		if target == "" {
			return false
		}

		ok, err := path.Match(r.PathPattern, target)
		if err != nil || !ok {
			return false
		}
	}

	if r.Pattern != "" {
		return matchPattern(tool, r.Pattern, ExtractTarget(tool, req))
	}

	return true
}

// Match evaluates the first rule whose pattern matches the request and returns
// its decision. Missing tools or empty rule sets return None.
func Match(tool string, req Request, rules map[string][]Rule) Decision {
	if len(rules) == 0 {
		return None
	}

	if decision := matchByKey(tool, req, rules, tool); decision != None {
		return decision
	}

	if strings.HasPrefix(tool, "mcp:") && tool != "mcp:*" {
		if decision := matchByKey(tool, req, rules, "mcp:*"); decision != None {
			return decision
		}
	}

	return None
}

func matchByKey(tool string, req Request, rules map[string][]Rule, key string) Decision {
	toolRules, ok := rules[key]
	if !ok || len(toolRules) == 0 {
		return None
	}

	target := ExtractTarget(tool, req)
	for _, rule := range toolRules {
		if matchPattern(tool, rule.Pattern, target) {
			if rule.Decision == None {
				return Allow
			}

			return rule.Decision
		}
	}

	return None
}

// ExtractTarget returns the request field a rule should match for a tool.
func ExtractTarget(tool string, req Request) string {
	tool = strings.TrimSpace(tool)
	switch {
	case tool == toolNameShell || tool == toolNameBash:
		return strings.TrimSpace(req.Command)
	case strings.HasPrefix(tool, "mcp:"):
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

// Target returns the main path-like request target for permission snapshots and
// flat policy path rules.
func Target(req Request) string {
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

func matchPattern(tool string, pattern string, target string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}

	if target == "" {
		return false
	}

	switch tool {
	case toolNameShell, toolNameBash:
		return matchShell(pattern, target)
	default:
		return matchPath(pattern, target)
	}
}

func matchShell(pattern string, command string) bool {
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

// CommandMatchesPrefix matches a command prefix on shell-token boundaries.
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
