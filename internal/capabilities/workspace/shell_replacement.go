package workspace

import (
	"regexp"
	"strconv"
	"strings"

	semanticlanguage "idiom/internal/repository/semantic/language"
)

type ShellReplacementKind string

const (
	ShellReplacementSedReadFile      ShellReplacementKind = "sed_read_file"
	ShellReplacementHeadTailReadFile ShellReplacementKind = "headtail_read_file"
	ShellReplacementCatReadFile      ShellReplacementKind = "cat_read_file"
	ShellReplacementFindSearchFiles  ShellReplacementKind = "find_search_files"
	ShellReplacementSedWCFileStats   ShellReplacementKind = "sed_wc_file_stats"
	ShellReplacementWCFileStats      ShellReplacementKind = "file_stats"
	ShellReplacementSymbolSearch     ShellReplacementKind = "symbol_search"
)

type ShellReplacementCandidate struct {
	Kind       ShellReplacementKind
	Capability string
	Request    ToolRequest
	Symbol     ShellSymbolSearch
}

type ShellSymbolSearch struct {
	Pattern string
	Path    string
	Glob    string
}

func shellReplacementCandidate(command string) *ShellReplacementCandidate {
	if match := matchShellSedRange(command); match != nil {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementSedReadFile,
			Capability: ToolNameReadFile,
			Request: ToolRequest{
				Tool:      ToolNameReadFile,
				Path:      match.path,
				StartLine: match.start,
				EndLine:   match.end,
			},
		}
	}

	if match := matchShellHeadTailFile(command); match != nil {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementHeadTailReadFile,
			Capability: ToolNameReadFile,
			Request: ToolRequest{
				Tool:      ToolNameReadFile,
				Path:      match.path,
				StartLine: match.start,
				EndLine:   match.end,
			},
		}
	}

	if path := matchShellCatWholeFile(command); path != "" {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementCatReadFile,
			Capability: ToolNameReadFile,
			Request:    ToolRequest{Tool: ToolNameReadFile, Path: path},
		}
	}

	if pattern := matchShellFindName(command); pattern != "" {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementFindSearchFiles,
			Capability: ToolNameSearchFiles,
			Request:    ToolRequest{Tool: ToolNameSearchFiles, Pattern: pattern},
		}
	}

	if match := matchShellSedWCStats(command); match != nil {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementSedWCFileStats,
			Capability: ToolNameFileStats,
			Request: ToolRequest{
				Tool:      ToolNameFileStats,
				Path:      match.path,
				StartLine: match.start,
				EndLine:   match.end,
			},
		}
	}

	if path := matchShellWCStats(command); path != "" {
		return &ShellReplacementCandidate{
			Kind:       ShellReplacementWCFileStats,
			Capability: ToolNameFileStats,
			Request:    ToolRequest{Tool: ToolNameFileStats, Path: path},
		}
	}

	if match := matchShellGrepIdentifier(command); match != nil {
		capability := string(semanticlanguage.CapWorkspaceSymbol)
		if match.path != "" {
			capability = string(semanticlanguage.CapReferences)
		}

		return &ShellReplacementCandidate{
			Kind:       ShellReplacementSymbolSearch,
			Capability: capability,
			Symbol: ShellSymbolSearch{
				Path:    match.path,
				Pattern: match.pattern,
				Glob:    match.glob,
			},
		}
	}

	return nil
}

type shellSedMatch struct {
	path       string
	start, end int
}

var reShellSedRange = regexp.MustCompile(`^sed\s+-n\s+['"]?(\d+),(\d+)p['"]?\s+(\S+)\s*$`)

var reShellSedWCStats = regexp.MustCompile(`^sed\s+-n\s+['"]?(\d+)(?:,(\d+))?p['"]?\s+(\S+)\s*\|\s*wc\s+-c\s*$`)

func matchShellSedRange(command string) *shellSedMatch {
	m := reShellSedRange.FindStringSubmatch(command)
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

func matchShellSedWCStats(command string) *shellSedMatch {
	if strings.ContainsAny(command, "$`") {
		return nil
	}

	m := reShellSedWCStats.FindStringSubmatch(command)
	if m == nil {
		return nil
	}

	start, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}

	end := start
	if strings.TrimSpace(m[2]) != "" {
		parsedEnd, err := strconv.Atoi(m[2])
		if err != nil || parsedEnd < start {
			return nil
		}
		end = parsedEnd
	}

	return &shellSedMatch{path: m[3], start: start, end: end}
}

type shellGrepMatch struct {
	pattern string
	path    string
	glob    string
}

func matchShellWCStats(command string) string {
	if strings.ContainsAny(command, "$`") || shellCommandHasControlSyntax(command) {
		return ""
	}

	fields := shellCommandFields(command)
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

func matchShellGrepIdentifier(command string) *shellGrepMatch {
	if strings.ContainsAny(command, "$`") || shellCommandHasControlSyntax(command) {
		return nil
	}

	fields := shellCommandFields(command)
	if len(fields) < 2 {
		return nil
	}

	if fields[0] != "grep" && fields[0] != "rg" {
		return nil
	}

	i := 1
	glob := ""
	for i < len(fields) && strings.HasPrefix(fields[i], "-") {
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

func shellCommandFields(command string) []string {
	var (
		fields []string
		b      strings.Builder
		quote  rune
	)

	escaped := false
	for _, r := range command {
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

	if ident, ok := SyntaxIdentifierFromAnyLanguage(pattern); ok {
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
		if !LooksLikeIdentifier(part) {
			return "", false
		}
	}

	first := strings.TrimSpace(parts[0])
	return first, first != ""
}

var reShellHeadTail = regexp.MustCompile(`^(head|tail)\s+(?:-n\s+)?-?(\d+)\s+(\S+)\s*$`)

type shellHeadTailMatch struct {
	path       string
	start, end int
}

func matchShellHeadTailFile(command string) *shellHeadTailMatch {
	if strings.ContainsAny(command, "|><$`;&") {
		return nil
	}

	m := reShellHeadTail.FindStringSubmatch(command)
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
		return &shellHeadTailMatch{path: path, start: 1, end: 0}
	default:
		return nil
	}
}

func matchShellCatWholeFile(command string) string {
	if strings.ContainsAny(command, "|><$`;&") {
		return ""
	}

	fields := shellCommandFields(command)
	if len(fields) != 2 || fields[0] != "cat" {
		return ""
	}

	path := fields[1]
	if strings.HasPrefix(path, "-") || strings.ContainsAny(path, "*?[]{}()") {
		return ""
	}

	return path
}

var reShellFindName = regexp.MustCompile(`^find\s+\S+\s+-name\s+(\S+)\s*$`)

func matchShellFindName(command string) string {
	if strings.ContainsAny(command, "|><$`;&") {
		return ""
	}

	m := reShellFindName.FindStringSubmatch(command)
	if m == nil {
		return ""
	}

	return stripShellQuotes(m[1])
}

func stripShellQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}

	return s
}
