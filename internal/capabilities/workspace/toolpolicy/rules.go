package toolpolicy

import (
	"encoding/json"
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
	Pattern  string   `json:"pattern"`
	Decision Decision `json:"decision"`
}

// Request is the subset of a workspace tool request needed to evaluate
// permission-rule targets. It keeps rule matching independent from the
// full workspace tool DTO.
type Request struct {
	Command string
	Path    string
	Args    json.RawMessage
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
	switch {
	case tool == "shell" || tool == "bash":
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
	case "shell", "bash":
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
