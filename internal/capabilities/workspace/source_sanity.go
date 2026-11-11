package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/discovery"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const (
	defaultSourceSanityLimit  = 24
	sourceSanityMethodGeneric = "generic_source_sanity"
)

type sourceSanityOutcome struct {
	Path   string
	Method string
	Detail string
	Error  string
}

func (m *Manager) sourceSanity(ctx context.Context, req ToolRequest) ToolResult {
	attemptedPaths := m.sourceSanityRequestScope(req)
	targets, err := m.sourceSanityTargets(req)
	if err != nil {
		return ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity unavailable", Error: err.Error(), Paths: attemptedPaths}
	}

	if len(targets) == 0 {
		return ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity unavailable", Error: "no source files available for source sanity", Paths: attemptedPaths}
	}
	attemptedPaths = make([]string, 0, len(targets))
	for _, target := range targets {
		attemptedPaths = textutil.AppendUnique(attemptedPaths, filepath.ToSlash(m.pathAccess.DisplayPath(target)))
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultSourceSanityLimit
	}

	if len(targets) > limit {
		rel := make([]string, 0, min(limit, len(targets)))
		for _, target := range targets[:min(limit, len(targets))] {
			rel = append(rel, filepath.ToSlash(m.pathAccess.DisplayPath(target)))
		}

		return ToolResult{
			Tool:    ToolNameSourceSanity,
			Summary: "Source sanity needs narrower scope",
			Error:   fmt.Sprintf("source_sanity matched %d source files; pass a narrower path or set limit explicitly. First matches: %s", len(targets), strings.Join(rel, ", ")),
			Paths:   attemptedPaths,
		}
	}

	outcomes := make([]sourceSanityOutcome, 0, len(targets))
	for _, target := range targets {
		outcomes = append(outcomes, m.sourceSanityOne(ctx, target, req.Workdir))
	}

	failures := make([]string, 0)
	outputLines := make([]string, 0, len(outcomes))
	diagnosticPaths := make([]string, 0, len(outcomes))
	fallbacks := 0

	for _, outcome := range outcomes {
		if strings.TrimSpace(outcome.Path) != "" && outcome.Method != sourceSanityMethodGeneric {
			diagnosticPaths = textutil.AppendUnique(diagnosticPaths, outcome.Path)
		}

		line := fmt.Sprintf("%s: %s", outcome.Path, outcome.Detail)
		if outcome.Method != "" {
			line = fmt.Sprintf("%s: %s via %s", outcome.Path, outcome.Detail, outcome.Method)
		}

		outputLines = append(outputLines, line)

		if outcome.Method == sourceSanityMethodGeneric {
			fallbacks++
		}

		if outcome.Error != "" {
			failures = append(failures, fmt.Sprintf("%s: %s", outcome.Path, outcome.Error))
		}
	}

	var facts *toolfacts.ToolResultFacts
	if len(diagnosticPaths) > 0 {
		facts = &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: diagnosticPaths}
	}

	if len(failures) > 0 {
		return ToolResult{
			Tool:    ToolNameSourceSanity,
			Summary: fmt.Sprintf("Source sanity failed for %d of %d file(s)", len(failures), len(outcomes)),
			Output:  strings.Join(outputLines, "\n"),
			Error:   strings.Join(failures, "\n"),
			Facts:   facts,
			Paths:   attemptedPaths,
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
		Tool:    ToolNameSourceSanity,
		Summary: summary,
		Output:  strings.Join(outputLines, "\n"),
		Facts:   facts,
		Paths:   attemptedPaths,
	}
}

func (m *Manager) sourceSanityRequestScope(req ToolRequest) []string {
	if abs, err := m.pathAccess.ResolvePath(req.Path, req.Workdir); err == nil {
		if display := normalizeVerificationScope(m.pathAccess.DisplayPath(abs)); display != "" {
			return []string{display}
		}
	}

	path := normalizeVerificationScope(req.Path)
	if path == "" {
		return nil
	}
	workdir := normalizeVerificationScope(req.Workdir)
	if workdir == "" || workdir == "." {
		return []string{path}
	}
	return []string{normalizeVerificationScope(filepath.ToSlash(filepath.Join(workdir, path)))}
}

func (m *Manager) sourceSanityTargets(req ToolRequest) ([]string, error) {
	if strings.TrimSpace(req.Path) == "" {
		return nil, fmt.Errorf("%s requires path", ToolNameSourceSanity)
	}

	target, err := m.pathAccess.ResolvePath(req.Path, req.Workdir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		if sourcekind.CoversSourceSanity(target) {
			return []string{target}, nil
		}

		return nil, fmt.Errorf("%s is not a supported source file", m.pathAccess.DisplayPath(target))
	}

	var targets []string

	err = safepath.WalkDirUnderRoot(m.cfg.Root, target, "source sanity discovery", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if path != target && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasPrefix(filepath.Base(path), ".") || discovery.ShouldSkipRelFile(filepath.ToSlash(m.pathAccess.DisplayPath(path))) {
			return nil
		}

		if sourcekind.CoversSourceSanity(path) {
			targets = append(targets, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(targets, func(i, j int) bool {
		return m.pathAccess.DisplayPath(targets[i]) < m.pathAccess.DisplayPath(targets[j])
	})

	return targets, nil
}

func (m *Manager) sourceSanityOne(ctx context.Context, absPath string, workdir string) sourceSanityOutcome {
	rel := filepath.ToSlash(m.pathAccess.DisplayPath(absPath))
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
				Error:  textutil.FirstNonEmpty(strings.TrimSpace(result.Error), strings.TrimSpace(result.Output), strings.TrimSpace(result.Summary)),
			}
		}

		fallback, err := genericSourceSanity(m.cfg.Root, absPath)
		if err != nil {
			return sourceSanityOutcome{
				Path:   rel,
				Method: sourceSanityMethodGeneric,
				Detail: fmt.Sprintf("%s; %s", diagnosticsUnavailableDetail(result), fallback),
				Error:  err.Error(),
			}
		}

		if sourcekind.RequiresConcreteSourceSanityDiagnostics(rel) {
			return sourceSanityOutcome{
				Path:   rel,
				Method: sourceSanityMethodGeneric,
				Detail: fmt.Sprintf("%s; %s", diagnosticsUnavailableDetail(result), fallback),
				Error:  "concrete C/C++ diagnostics are unavailable; generic delimiter sanity is advisory and insufficient for terminal source_sanity",
			}
		}

		return sourceSanityOutcome{
			Path:   rel,
			Method: sourceSanityMethodGeneric,
			Detail: fmt.Sprintf("%s; %s", diagnosticsUnavailableDetail(result), fallback),
		}
	}

	fallback, err := genericSourceSanity(m.cfg.Root, absPath)
	if err != nil {
		return sourceSanityOutcome{
			Path:   rel,
			Method: sourceSanityMethodGeneric,
			Detail: fallback,
			Error:  err.Error(),
		}
	}

	return sourceSanityOutcome{
		Path:   rel,
		Method: sourceSanityMethodGeneric,
		Detail: fallback,
	}
}

func sourceSanityDiagnosticToolForPath(path string) string {
	lang, ok := semanticlanguage.LanguageForFile(path)
	if !ok {
		return ""
	}

	return lang.Tool(semanticlanguage.CapDiagnostics)
}

func diagnosticsUnavailable(result ToolResult) bool {
	goDiagnosticText := strings.ToLower(result.Output + "\n" + result.Error)
	if result.Tool == ToolNameGoDiagnostics &&
		strings.Contains(goDiagnosticText, "go test compile failed") &&
		!strings.Contains(goDiagnosticText, "go.mod file not found") {

		return false
	}

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
	detail := textutil.FirstNonEmpty(strings.TrimSpace(result.Output), strings.TrimSpace(result.Summary))

	detail = strings.ReplaceAll(detail, "\r\n", "\n")
	detail = strings.TrimSpace(strings.Split(detail, "\n")[0])

	if detail == "" {
		return "completed"
	}

	return detail
}

func genericSourceSanity(root string, absPath string) (string, error) {
	data, err := safepath.ReadFileUnderRoot(root, absPath, "source file", safepath.Options{})
	if err != nil {
		return "generic source sanity could not read file", err
	}

	if !utf8.Valid(data) {
		return "generic source sanity failed", fmt.Errorf("file is not valid UTF-8")
	}

	if issue := genericDelimiterSanity(filepath.Ext(absPath), string(data)); issue != "" {
		return "generic source sanity failed", fmt.Errorf("%w: %s", ErrSourceSanityFailed, issue)
	}

	if strings.EqualFold(filepath.Ext(absPath), ".gd") {
		if issue := gdscriptStructuralSanity(string(data)); issue != "" {
			return "generic source sanity failed", fmt.Errorf("%w: %s", ErrSourceSanityFailed, issue)
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
