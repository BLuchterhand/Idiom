package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"idiom/internal/discovery"
)

const defaultSourceSanityLimit = 24

type sourceSanityOutcome struct {
	Path   string
	Method string
	Detail string
	Error  string
}

func (m *Manager) sourceSanity(ctx context.Context, req ToolRequest) ToolResult {
	targets, err := m.sourceSanityTargets(req)
	if err != nil {
		return ToolResult{Tool: "source_sanity", Summary: "Source sanity unavailable", Error: err.Error()}
	}
	if len(targets) == 0 {
		return ToolResult{Tool: "source_sanity", Summary: "Source sanity unavailable", Error: "no source files available for source sanity"}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultSourceSanityLimit
	}
	if len(targets) > limit {
		rel := make([]string, 0, minInt(limit, len(targets)))
		for _, target := range targets[:minInt(limit, len(targets))] {
			rel = append(rel, filepath.ToSlash(m.displayPath(target)))
		}
		return ToolResult{
			Tool:    "source_sanity",
			Summary: "Source sanity needs narrower scope",
			Error:   fmt.Sprintf("source_sanity matched %d source files; pass a narrower path or set limit explicitly. First matches: %s", len(targets), strings.Join(rel, ", ")),
		}
	}

	outcomes := make([]sourceSanityOutcome, 0, len(targets))
	for _, target := range targets {
		outcomes = append(outcomes, m.sourceSanityOne(ctx, target, req.Workdir))
	}

	failures := make([]string, 0)
	outputLines := make([]string, 0, len(outcomes))
	fallbacks := 0
	for _, outcome := range outcomes {
		line := fmt.Sprintf("%s: %s", outcome.Path, outcome.Detail)
		if outcome.Method != "" {
			line = fmt.Sprintf("%s: %s via %s", outcome.Path, outcome.Detail, outcome.Method)
		}
		outputLines = append(outputLines, line)
		if outcome.Method == "generic_source_sanity" {
			fallbacks++
		}
		if outcome.Error != "" {
			failures = append(failures, fmt.Sprintf("%s: %s", outcome.Path, outcome.Error))
		}
	}

	if len(failures) > 0 {
		return ToolResult{
			Tool:    "source_sanity",
			Summary: fmt.Sprintf("Source sanity failed for %d of %d file(s)", len(failures), len(outcomes)),
			Output:  strings.Join(outputLines, "\n"),
			Error:   strings.Join(failures, "\n"),
		}
	}

	summary := fmt.Sprintf("Source sanity passed for %d file(s)", len(outcomes))
	if fallbacks > 0 {
		summary += fmt.Sprintf(" (%d generic fallback)", fallbacks)
		if fallbacks != 1 {
			summary += "s"
		}
	}
	return ToolResult{
		Tool:    "source_sanity",
		Summary: summary,
		Output:  strings.Join(outputLines, "\n"),
	}
}

func (m *Manager) sourceSanityTargets(req ToolRequest) ([]string, error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, fmt.Errorf("source_sanity requires path")
	}
	target, err := m.resolvePath(req.Path, req.Workdir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if sourceSanityCoversPath(target) {
			return []string{target}, nil
		}
		return nil, fmt.Errorf("%s is not a supported source file", m.displayPath(target))
	}

	var targets []string
	err = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != target && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), ".") || discovery.ShouldSkipRelFile(filepath.ToSlash(m.displayPath(path))) {
			return nil
		}
		if sourceSanityCoversPath(path) {
			targets = append(targets, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool {
		return m.displayPath(targets[i]) < m.displayPath(targets[j])
	})
	return targets, nil
}

func (m *Manager) sourceSanityOne(ctx context.Context, absPath string, workdir string) sourceSanityOutcome {
	rel := filepath.ToSlash(m.displayPath(absPath))
	if tool := sourceSanityDiagnosticToolForPath(rel); tool != "" {
		result := m.Execute(ctx, ToolRequest{Tool: tool, Path: rel, Workdir: workdir})
		if strings.TrimSpace(result.Error) == "" && !diagnosticsUnavailable(result) {
			return sourceSanityOutcome{
				Path:   rel,
				Method: tool,
				Detail: oneLineSourceSanityDetail(result),
			}
		}
		if !diagnosticsUnavailable(result) {
			return sourceSanityOutcome{
				Path:   rel,
				Method: tool,
				Detail: oneLineSourceSanityDetail(result),
				Error:  firstNonEmptyString(strings.TrimSpace(result.Error), strings.TrimSpace(result.Output), strings.TrimSpace(result.Summary)),
			}
		}
		fallback, err := genericSourceSanity(absPath)
		if err != nil {
			return sourceSanityOutcome{
				Path:   rel,
				Method: "generic_source_sanity",
				Detail: fmt.Sprintf("%s; %s", diagnosticsUnavailableDetail(result), fallback),
				Error:  err.Error(),
			}
		}
		return sourceSanityOutcome{
			Path:   rel,
			Method: "generic_source_sanity",
			Detail: fmt.Sprintf("%s; %s", diagnosticsUnavailableDetail(result), fallback),
		}
	}

	fallback, err := genericSourceSanity(absPath)
	if err != nil {
		return sourceSanityOutcome{
			Path:   rel,
			Method: "generic_source_sanity",
			Detail: fallback,
			Error:  err.Error(),
		}
	}
	return sourceSanityOutcome{
		Path:   rel,
		Method: "generic_source_sanity",
		Detail: fallback,
	}
}

func sourceSanityDiagnosticToolForPath(path string) string {
	lang, ok := LanguageForFile(path)
	if !ok {
		return ""
	}
	return lang.Tool(CapDiagnostics)
}

func sourceSanityCoversPath(path string) bool {
	if _, ok := LanguageForFile(path); ok {
		return true
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".gd":
		return true
	default:
		return false
	}
}

// SourceSanityCoversPath reports whether source_sanity has a concrete
// diagnostic route or generic fallback for path. Plan/executor gates use this
// to avoid treating prose/config edits as source edits.
func SourceSanityCoversPath(path string) bool {
	return sourceSanityCoversPath(path)
}

func diagnosticsUnavailable(result ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + " " + result.Error))
	if text == "" {
		return false
	}
	return strings.Contains(text, "unavailable") ||
		strings.Contains(text, "not installed") ||
		strings.Contains(text, "not found") ||
		strings.Contains(text, "no ") && strings.Contains(text, " files available for diagnostics")
}

func diagnosticsUnavailableDetail(result ToolResult) string {
	detail := strings.TrimSpace(result.Error)
	if detail == "" {
		detail = strings.TrimSpace(result.Summary)
	}
	if detail == "" {
		return "language diagnostics unavailable"
	}
	return fmt.Sprintf("%s unavailable: %s", strings.TrimSpace(result.Tool), detail)
}

func oneLineSourceSanityDetail(result ToolResult) string {
	detail := firstNonEmptyString(strings.TrimSpace(result.Output), strings.TrimSpace(result.Summary))
	detail = strings.ReplaceAll(detail, "\r\n", "\n")
	detail = strings.TrimSpace(strings.Split(detail, "\n")[0])
	if detail == "" {
		return "completed"
	}
	return detail
}

func genericSourceSanity(absPath string) (string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "generic source sanity could not read file", err
	}
	if !utf8.Valid(data) {
		return "generic source sanity failed", fmt.Errorf("file is not valid UTF-8")
	}
	if issue := genericDelimiterSanity(filepath.Ext(absPath), string(data)); issue != "" {
		return "generic source sanity failed", fmt.Errorf("%s", issue)
	}
	if strings.EqualFold(filepath.Ext(absPath), ".gd") {
		if issue := gdscriptStructuralSanity(string(data)); issue != "" {
			return "generic source sanity failed", fmt.Errorf("%s", issue)
		}
	}
	return "generic delimiter sanity passed", nil
}

func gdscriptStructuralSanity(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !gdscriptNeedsIndentedBody(line) {
			continue
		}
		parentIndent := leadingIndentColumns(line)
		foundBody := false
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || strings.HasPrefix(next, "#") {
				continue
			}
			if leadingIndentColumns(lines[j]) <= parentIndent {
				return fmt.Sprintf("line %d: GDScript function declaration has no indented body before line %d", i+1, j+1)
			}
			foundBody = true
			break
		}
		if !foundBody {
			return fmt.Sprintf("line %d: GDScript function declaration has no body", i+1)
		}
	}
	return ""
}

func gdscriptNeedsIndentedBody(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "func ") && !strings.HasPrefix(trimmed, "static func ") {
		return false
	}
	withoutComment := strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
	return strings.HasSuffix(withoutComment, ":")
}

func leadingIndentColumns(line string) int {
	cols := 0
	for _, r := range line {
		switch r {
		case ' ':
			cols++
		case '\t':
			cols += 4
		default:
			return cols
		}
	}
	return cols
}

type delimiterFrame struct {
	r    rune
	line int
	col  int
}

func genericDelimiterSanity(ext string, text string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	runes := []rune(text)
	var stack []delimiterFrame
	line, col := 1, 0
	inLineComment := false
	inBlockComment := false
	var quote rune
	tripleQuote := false
	escaped := false

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\n' {
			line++
			col = 0
			inLineComment = false
			if quote != 0 && !tripleQuote && quote != '`' {
				escaped = false
			}
			continue
		}
		col++

		if inLineComment {
			continue
		}
		if inBlockComment {
			if r == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				i++
				col++
				inBlockComment = false
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' && quote != '`' {
				escaped = true
				continue
			}
			if tripleQuote {
				if r == quote && i+2 < len(runes) && runes[i+1] == quote && runes[i+2] == quote {
					i += 2
					col += 2
					quote = 0
					tripleQuote = false
				}
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}

		if r == '/' && i+1 < len(runes) {
			switch runes[i+1] {
			case '/':
				inLineComment = true
				i++
				col++
				continue
			case '*':
				inBlockComment = true
				i++
				col++
				continue
			}
		}
		if r == '#' && hashStartsLineComment(ext) {
			inLineComment = true
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			if (ext == ".py" || ext == ".pyi" || ext == ".gd") && r != '`' &&
				i+2 < len(runes) && runes[i+1] == r && runes[i+2] == r {
				tripleQuote = true
				i += 2
				col += 2
			}
			continue
		}

		switch r {
		case '(', '[', '{':
			stack = append(stack, delimiterFrame{r: r, line: line, col: col})
		case ')', ']', '}':
			if len(stack) == 0 {
				return fmt.Sprintf("unmatched %q at line %d column %d", r, line, col)
			}
			last := stack[len(stack)-1]
			if !delimitersMatch(last.r, r) {
				return fmt.Sprintf("mismatched %q at line %d column %d; opened %q at line %d column %d", r, line, col, last.r, last.line, last.col)
			}
			stack = stack[:len(stack)-1]
		}
	}

	if inBlockComment {
		return "unterminated block comment"
	}
	if quote != 0 {
		return fmt.Sprintf("unterminated %q string", quote)
	}
	if len(stack) > 0 {
		last := stack[len(stack)-1]
		return fmt.Sprintf("unclosed %q from line %d column %d", last.r, last.line, last.col)
	}
	return ""
}

func hashStartsLineComment(ext string) bool {
	switch ext {
	case ".py", ".pyi", ".gd":
		return true
	default:
		return false
	}
}

func delimitersMatch(open rune, close rune) bool {
	switch open {
	case '(':
		return close == ')'
	case '[':
		return close == ']'
	case '{':
		return close == '}'
	default:
		return false
	}
}
