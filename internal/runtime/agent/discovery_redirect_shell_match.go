package agent

import (
	"regexp"
	"strconv"
	"strings"

	"idiom/internal/capabilities/workspace"
)

type shellSedMatch struct {
	path       string
	start, end int
}

// reShellSedRange matches the common `sed -n 'A,Bp' FILE` shape. The
// quoted address form `'1,20p'` is the only flag the logs observed -- other
// sed invocations (in-place edits, script form) fall through and execute.
var reShellSedRange = regexp.MustCompile(`^sed\s+-n\s+['"]?(\d+),(\d+)p['"]?\s+(\S+)\s*$`)

func matchShellSedRange(cmd string) *shellSedMatch {
	m := reShellSedRange.FindStringSubmatch(cmd)
	if m == nil {
		return nil
	}

	start, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}

	end, err := strconv.Atoi(m[2])
	if err != nil || end < start {
		return nil
	}

	return &shellSedMatch{path: m[3], start: start, end: end}
}

type shellGrepMatch struct {
	pattern string
	path    string
	glob    string
}

func matchShellWCStats(cmd string) string {
	if strings.ContainsAny(cmd, "$`") || shellCommandHasControlOperator(cmd) {
		return ""
	}

	fields := shellCommandFields(cmd)
	if len(fields) < 2 || fields[0] != "wc" {
		return ""
	}

	pathArg := ""

	for i := 1; i < len(fields); i++ {
		field := fields[i]
		if field == "--" {
			if i+1 >= len(fields) || pathArg != "" {
				return ""
			}

			pathArg = fields[i+1]
			i++

			continue
		}

		if strings.HasPrefix(field, "-") {
			if !isSupportedWCFlag(field) {
				return ""
			}

			continue
		}

		if pathArg != "" {
			return ""
		}

		pathArg = field
	}

	return stripShellQuotes(pathArg)
}

func isSupportedWCFlag(flag string) bool {
	if flag == "" || flag[0] != '-' {
		return false
	}

	switch flag {
	case "-l", "-c", "-m", "-w":
		return true
	}

	if strings.HasPrefix(flag, "--") {
		switch flag {
		case "--lines", "--bytes", "--chars", "--words":
			return true
		default:
			return false
		}
	}

	for _, r := range strings.TrimPrefix(flag, "-") {
		switch r {
		case 'l', 'c', 'm', 'w':
		default:
			return false
		}
	}

	return true
}

var reShellGrepIdentToken = regexp.MustCompile(`^[A-Za-z_][\w:]*$`)

// matchShellGrepIdentifier accepts `grep [-flags] PATTERN [FILE]` (or `rg`
// in place of `grep`) where PATTERN, stripped of optional single/double
// quotes, is a bare identifier. Pipes, redirections, and regex metachars
// anywhere in the line disqualify the whole match -- we only rewrite the
// unambiguous symbol-lookup shape.
func matchShellGrepIdentifier(cmd string) *shellGrepMatch {
	if strings.ContainsAny(cmd, "$`") || shellCommandHasControlOperator(cmd) {
		return nil
	}

	fields := shellCommandFields(cmd)
	if len(fields) < 2 {
		return nil
	}

	if fields[0] != "grep" && fields[0] != "rg" {
		return nil
	}

	i := 1
	// Skip flag tokens. Accept single-letter combos (-rn) and long
	// flags (--include=foo). Capture include/glob flags because they
	// are the only language signal in common unscoped grep/rg searches.
	glob := ""

	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
		// -E, -P, -F change pattern semantics -- bail. The caller's
		// pattern might be valid identifier in basic grep but a regex
		// in -E grep, so we don't rewrite those.
		if fields[i] == "-E" || fields[i] == "-P" || fields[i] == "-F" ||
			fields[i] == "--extended-regexp" || fields[i] == "--perl-regexp" {

			return nil
		}

		if strings.HasPrefix(fields[i], "--include=") {
			glob = stripShellQuotes(strings.TrimPrefix(fields[i], "--include="))
		} else if fields[i] == "-g" && i+1 < len(fields) {
			i++

			glob = stripShellQuotes(fields[i])
		}

		i++
	}

	if i >= len(fields) {
		return nil
	}

	pattern, ok := shellGrepSymbolPattern(stripShellQuotes(fields[i]))
	if !ok {
		return nil
	}

	i++

	path := ""

	for i < len(fields) {
		token := fields[i]
		if strings.HasPrefix(token, "--include=") {
			glob = stripShellQuotes(strings.TrimPrefix(token, "--include="))
			i++

			continue
		}

		if token == "-g" && i+1 < len(fields) {
			i++

			glob = stripShellQuotes(fields[i])
			i++

			continue
		}

		if strings.HasPrefix(token, "-") {
			i++
			continue
		}

		if path != "" {
			return nil
		}

		candidate := stripShellQuotes(token)
		if strings.ContainsAny(candidate, "*?[]{}()") {
			return nil
		}

		path = candidate
		i++
	}

	return &shellGrepMatch{pattern: pattern, path: path, glob: glob}
}

func shellCommandHasControlOperator(cmd string) bool {
	var quote rune

	escaped := false

	for _, r := range cmd {
		if escaped {
			escaped = false
			continue
		}

		if r == '\\' {
			if quote != '\'' {
				escaped = true
			}

			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
			}

			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if strings.ContainsRune("|><;&", r) {
			return true
		}
	}

	return false
}

func shellCommandFields(cmd string) []string {
	var (
		fields []string
		b      strings.Builder
		quote  rune
	)

	escaped := false

	for _, r := range cmd {
		if escaped {
			b.WriteRune(r)

			escaped = false

			continue
		}

		if r == '\\' {
			if quote == '\'' {
				b.WriteRune(r)
			} else {
				escaped = true
			}

			continue
		}

		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}

			continue
		}

		if r == '\'' || r == '"' {
			quote = r
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if b.Len() > 0 {
				fields = append(fields, b.String())
				b.Reset()
			}

			continue
		}

		b.WriteRune(r)
	}

	if escaped {
		b.WriteRune('\\')
	}

	if b.Len() > 0 {
		fields = append(fields, b.String())
	}

	return fields
}

func shellGrepSymbolPattern(pattern string) (string, bool) {
	pattern = strings.TrimSpace(pattern)
	if reShellGrepIdentToken.MatchString(pattern) {
		return pattern, true
	}

	if strings.ContainsAny(pattern, "*+?[]{}()^$") {
		return "", false
	}

	if first, ok := firstIdentifierAlternative(pattern); ok {
		return first, true
	}

	if ident, ok := extractUnambiguousSyntaxIdentifier(pattern); ok {
		return ident, true
	}

	return "", false
}

func firstIdentifierAlternative(pattern string) (string, bool) {
	normalized := strings.ReplaceAll(pattern, `\|`, "|")
	if !strings.Contains(normalized, "|") {
		return "", false
	}

	parts := strings.Split(normalized, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !looksLikeIdentifier(part) {
			return "", false
		}
	}

	first := strings.TrimSpace(parts[0])

	return first, first != ""
}

func extractUnambiguousSyntaxIdentifier(pattern string) (string, bool) {
	var found string

	for _, extractors := range syntaxExtractors {
		for _, re := range extractors {
			m := re.FindStringSubmatch(pattern)
			if len(m) < 2 || m[1] == "" {
				continue
			}

			if found != "" && found != m[1] {
				return "", false
			}

			found = m[1]
		}
	}

	return found, found != ""
}

func languageForShellGrep(match *shellGrepMatch, ws *workspace.Manager) (workspace.LanguageSupport, bool) {
	if match == nil || ws == nil {
		return workspace.LanguageSupport{}, false
	}

	if lang, ok := languageFromConcretePath(match.path); ok {
		return lang, true
	}

	if lang, ok := languageFromGlob(match.glob); ok {
		return lang, true
	}

	active := ws.ActiveLanguages()
	if len(active) == 1 {
		return active[0], true
	}

	return workspace.LanguageSupport{}, false
}

// reShellHeadTail matches `head -N FILE` / `tail -N FILE` / `head -n N FILE`.
// Only the simple single-file form redirects; multi-file or piped variants
// fall through to normal execution.
var reShellHeadTail = regexp.MustCompile(`^(head|tail)\s+(?:-n\s+)?-?(\d+)\s+(\S+)\s*$`)

type shellHeadTailMatch struct {
	path       string
	start, end int
}

// matchShellHeadTailFile rewrites `head -N FILE` -> read_file lines 1..N
// and `tail -N FILE` -> read_file requesting a wide-enough trailing
// window. For tail we use start_line=1 end_line=0 (open-ended) because
// without reading the file we don't know the line count; read_file's
// own clamping logic will handle it.
func matchShellHeadTailFile(cmd string) *shellHeadTailMatch {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return nil
	}

	m := reShellHeadTail.FindStringSubmatch(cmd)
	if m == nil {
		return nil
	}

	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return nil
	}

	path := m[3]
	if strings.ContainsAny(path, "*?[]{}()") {
		return nil
	}

	switch m[1] {
	case "head":
		return &shellHeadTailMatch{path: path, start: 1, end: n}
	case "tail":
		// For tail we don't know total lines; point the model at a
		// whole-file read_file so the workspace layer can choose the
		// right log/source behavior.
		return &shellHeadTailMatch{path: path, start: 1, end: 0}
	}

	return nil
}

// matchShellCatWholeFile rewrites `cat FILE` (single file, no flags,
// no pipe) -> read_file on that path. Returns the file path when matched,
// empty string otherwise.
func matchShellCatWholeFile(cmd string) string {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return ""
	}

	fields := strings.Fields(cmd)
	if len(fields) != 2 || fields[0] != "cat" {
		return ""
	}

	path := fields[1]
	if strings.HasPrefix(path, "-") {
		return ""
	}

	if strings.ContainsAny(path, "*?[]{}()") {
		return ""
	}

	return path
}

// reShellFindName matches the common `find PATH -name PATTERN` shape.
// Go's regexp (RE2) doesn't support backreferences, so we match with
// liberal quote handling and strip quotes from the captured pattern.
// Any additional predicates (-type, -path, -exec, -mtime, etc.) or
// pipe/redirection disqualify the match so we don't silently rewrite
// complex finds.
var reShellFindName = regexp.MustCompile(`^find\s+\S+\s+-name\s+(\S+)\s*$`)

// matchShellFindName rewrites `find PATH -name PATTERN` -> search_files
// with the same pattern. Returns the pattern when matched, empty string
// otherwise.
func matchShellFindName(cmd string) string {
	if strings.ContainsAny(cmd, "|><$`;&") {
		return ""
	}

	m := reShellFindName.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}

	pattern := stripShellQuotes(m[1])
	if pattern == "" {
		return ""
	}

	return pattern
}

func stripShellQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}

	return s
}
