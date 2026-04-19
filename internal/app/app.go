package app

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"qwen-bubbles/internal/semantic"
	"qwen-bubbles/internal/workspace"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type taskFileStamp struct {
	Size        int64 `json:"size"`
	ModTimeUnix int64 `json:"mod_time_unix"`
}

func symbolMatchFromGoSymbol(file *semantic.GoFile, symbol semantic.GoSymbol, score int) semantic.SymbolMatch {
	pkg := ""
	path := symbol.SourceFile
	if file != nil {
		pkg = file.Package
		if path == "" {
			path = file.Path
		}
	}
	return semantic.SymbolMatch{
		Path:      path,
		Package:   pkg,
		Name:      symbol.Name,
		Kind:      symbol.Kind,
		Receiver:  symbol.Receiver,
		Line:      symbol.Line,
		Column:    symbol.Column,
		StartLine: symbol.StartLine,
		EndLine:   symbol.EndLine,
		Score:     score,
	}
}

func looksLikeWorkspacePathToken(candidate string) bool {
	if candidate == "" || strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "/") {
		return false
	}
	if strings.ContainsAny(candidate, "*?") {
		return false
	}
	if strings.Contains(candidate, "/") {
		return strings.Contains(candidate, ".")
	}
	switch candidate {
	case "README.md", "go.mod", "go.sum", "config.json", "Makefile":
		return true
	}
	return hasWorkspaceFileExtension(candidate)
}

func hasWorkspaceFileExtension(candidate string) bool {
	switch filepath.Ext(candidate) {
	case ".go", ".md", ".json", ".yaml", ".yml", ".toml", ".txt":
		return true
	default:
		return false
	}
}

type locateOutcome struct {
	Literal    string
	Best       locateCandidate
	Alternates []locateCandidate
}

func marshalToolRequest(req workspace.ToolRequest) string {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf("{\"tool\":%q}", req.Tool)
	}
	return string(payload)
}

func primaryLocateLiteral(prompt string) string {
	for _, fragment := range quotedPromptFragments(prompt) {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" || looksLikeWorkspacePathToken(trimmed) {
			continue
		}
		return trimmed
	}

	lower := strings.ToLower(prompt)
	for _, marker := range []string{"shows ", "showing ", "says ", "text ", "label "} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		candidate := trimLocateLiteralTail(prompt[idx+len(marker):])
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func locateLiteralSearchPattern(literal string) string {
	return regexp.QuoteMeta(strings.TrimSpace(literal))
}

func trimLocateLiteralTail(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	stopWords := map[string]struct{}{
		"and":    {},
		"but":    {},
		"please": {},
		"so":     {},
		"that":   {},
		"which":  {},
	}
	parts := make([]string, 0, 4)
	for _, field := range strings.Fields(trimmed) {
		word := strings.Trim(field, "`'\"()[]{}<>,:;!?")
		if word == "" {
			continue
		}
		if _, stop := stopWords[strings.ToLower(word)]; stop {
			break
		}
		parts = append(parts, word)
		if len(parts) >= 4 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func locateSearchScopes(prompt string) []string {
	normalized := strings.ToLower(prompt)
	scopes := make([]string, 0, 5)
	seen := make(map[string]struct{})
	add := func(scope string) {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return
		}
		if _, ok := seen[scope]; ok {
			return
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}

	if containsAny(normalized, "bubbletea", "window", "header", "footer", "badge", "chat", "menu", "status", "banner") {
		add("internal/app")
	}
	for _, path := range promptMentionedWorkspacePaths(prompt) {
		add(path)
	}
	add("internal")
	add(".")
	return scopes
}

func looksLikeLocateMetaPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/logs/") ||
		strings.Contains(path, "/transcripts/") ||
		strings.Contains(path, "/.git/")
}

func looksLikeLocateMetaMatch(match string) bool {
	text := strings.ToLower(strings.TrimSpace(match))
	if containsAny(text, "workingframe", "func (m *model) workingdots", "strings.repeat") {
		return true
	}
	if strings.Contains(text, "workingstyle :=") && !strings.Contains(text, "qwen is working") {
		return true
	}
	return containsAny(
		text,
		"containsany(text",
		"strings.contains(text",
		"classifylocateregion",
		"scorelocatecandidate",
		"renderlocatesummary",
		"locatecandidate",
		"score +=",
		"separate similar match",
		"the top-of-window",
	)
}

func classifyLocateRegion(candidate locateCandidate) string {
	text := strings.ToLower(candidate.Path + "\n" + candidate.Match + "\n" + candidate.Snippet)
	switch {
	case containsAny(text, "header +=", "header :=", "workingstyle.render", "titlestyle.render", "serverstate :="):
		return "header"
	case containsAny(text, "footer :=", "footer +=", "helpstyle.render", "context:", "workspace:"):
		return "footer"
	case containsAny(text, "case \"assistant\"", "assistantstyle.render", "msg.role", "label = assistantstyle.render(\"qwen\")", "content = m.spinner.view()"):
		return "chat"
	case containsAny(text, "m.status =", "response complete.", "saving transcript.", "server ready on"):
		return "status"
	default:
		return ""
	}
}

func selectLocateAlternates(candidates []locateCandidate) []locateCandidate {
	if len(candidates) <= 1 {
		return nil
	}
	alternates := make([]locateCandidate, 0, 2)
	seenRegions := make(map[string]struct{})
	bestRegion := candidates[0].Region
	for _, candidate := range candidates[1:] {
		if len(alternates) >= 2 {
			break
		}
		if candidate.Region == "" || candidate.Region == bestRegion {
			continue
		}
		if _, ok := seenRegions[candidate.Region]; ok {
			continue
		}
		seenRegions[candidate.Region] = struct{}{}
		alternates = append(alternates, candidate)
	}
	return alternates
}

func firstSearchMatchLine(output string, path string) (int, bool) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, path+":") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		lineText := strings.TrimSpace(parts[1])
		fields := strings.Fields(lineText)
		if len(fields) == 0 {
			continue
		}
		if lineNumber, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			return lineNumber, true
		}
	}
	return 0, false
}

func extractReadFileLine(output string, targetLine int) (string, bool) {
	if targetLine <= 0 {
		return "", false
	}
	prefix := fmt.Sprintf("%d:", targetLine)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
	}
	return "", false
}

func looksLikeSingleLineConditionalWrapper(prevLine string, targetLine string, nextLine string) bool {
	prevLine = strings.TrimSpace(prevLine)
	targetLine = strings.TrimSpace(targetLine)
	nextLine = strings.TrimSpace(nextLine)
	if prevLine == "" || targetLine == "" || nextLine == "" {
		return false
	}
	return strings.HasPrefix(prevLine, "if ") &&
		strings.HasSuffix(prevLine, "{") &&
		nextLine == "}"
}

type unusedVariableDiagnostic struct {
	Path string
	Line int
	Name string
}

var unusedVariableDiagnosticPattern = regexp.MustCompile(`^(.+\.go):([0-9]+):([0-9]+): declared and not used: ([A-Za-z_][A-Za-z0-9_]*)$`)

func parseUnusedVariableDiagnostics(verify implementationVerification) []unusedVariableDiagnostic {
	seen := make(map[string]struct{})
	diagnostics := make([]unusedVariableDiagnostic, 0, 4)
	for _, result := range verify.results {
		for _, text := range []string{result.Output, result.Error} {
			for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				match := unusedVariableDiagnosticPattern.FindStringSubmatch(line)
				if len(match) != 5 {
					continue
				}
				lineNumber, err := strconv.Atoi(match[2])
				if err != nil || lineNumber <= 0 {
					continue
				}
				diag := unusedVariableDiagnostic{
					Path: normalizeGuardedPath(match[1]),
					Line: lineNumber,
					Name: strings.TrimSpace(match[4]),
				}
				key := fmt.Sprintf("%s:%d:%s", diag.Path, diag.Line, diag.Name)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				diagnostics = append(diagnostics, diag)
			}
		}
	}
	return diagnostics
}

func looksLikeUnusedVariableDeclaration(line string, name string) bool {
	line = strings.TrimSpace(line)
	name = strings.TrimSpace(name)
	if line == "" || name == "" {
		return false
	}
	return strings.HasPrefix(line, name+":=") ||
		strings.HasPrefix(line, name+" :=") ||
		strings.HasPrefix(line, "var "+name+" =")
}

func writeToolResultChanged(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if result.Error != "" {
		return false
	}
	switch req.Tool {
	case "edit_file", "write_file", "append_file", "mkdir", "go_move_declarations":
	default:
		return false
	}
	if strings.TrimSpace(req.Path) == "" && strings.TrimSpace(req.Destination) == "" {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Summary)), "no-op")
}

func prefixedCriticFindings(prefix string, findings []string) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		finding = strings.TrimSpace(finding)
		if finding != "" {
			out = append(out, prefix+": "+finding)
		}
	}
	return out
}

func directUISpacingReplacement(output string, startLine int, endLine int) (int, string, bool) {
	for line := startLine; line <= endLine; line++ {
		lineText, ok := extractReadFileLine(output, line)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(lineText)
		if !strings.HasPrefix(trimmed, "body :=") {
			continue
		}
		if strings.Contains(trimmed, ".MarginTop(") || strings.Contains(trimmed, ".PaddingTop(") {
			return 0, "", false
		}
		idx := strings.Index(lineText, "body :=")
		if idx < 0 {
			continue
		}
		indent := lineText[:idx]
		expr := strings.TrimSpace(strings.TrimPrefix(trimmed, "body :="))
		if expr == "" {
			continue
		}
		return line, indent + "body := lipgloss.NewStyle().MarginTop(1).Render(" + expr + ")\n", true
	}
	return 0, "", false
}

func replaceJSONFieldLine(lineText string, field string, desiredValue string) (string, bool) {
	pattern := regexp.MustCompile(`^(\s*"` + regexp.QuoteMeta(field) + `"\s*:\s*)([^,\n]+)(,?\s*)$`)
	matches := pattern.FindStringSubmatch(lineText)
	if len(matches) != 4 {
		return "", false
	}
	currentValue := strings.TrimSpace(matches[2])
	if currentValue == desiredValue {
		return "", false
	}
	return matches[1] + desiredValue + matches[3] + "\n", true
}

func extractFirstJSONObjectForPlan(text string) (string, bool) {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	if start < 0 {
		return "", false
	}
	text = text[start:]
	depth := 0
	inString := false
	escaped := false
	for i, r := range text {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '{':
			depth++
		case !inString && r == '}':
			depth--
			if depth == 0 {
				return text[:i+1], true
			}
		}
	}
	return "", false
}

func parseReadWindowSummary(summary string) (string, int, int, bool) {
	matches := regexp.MustCompile(`Read (?:f\d+\s+)?(.+?) lines (\d+)-(\d+)`).FindStringSubmatch(strings.TrimSpace(summary))
	if len(matches) != 4 {
		return "", 0, 0, false
	}
	startLine, err := strconv.Atoi(matches[2])
	if err != nil {
		return "", 0, 0, false
	}
	endLine, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", 0, 0, false
	}
	return normalizeGuardedPath(matches[1]), startLine, endLine, true
}

func cleanReadWindowSnippet(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
			continue
		}
		cleaned = append(cleaned, parts[1])
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func structuredReasonLooksStructural(reason string, strategyHints []string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	if containsAny(
		reason,
		"structur",
		"boundary",
		"boundaries",
		"responsibilit",
		"split",
		"extract",
		"separate",
		"move",
		"adapter",
		"interface",
		"ownership",
		"coupling",
		"orchestration",
	) {
		return true
	}
	for _, hint := range strategyHints {
		for _, token := range strings.Fields(strings.ToLower(hint)) {
			token = strings.Trim(token, "`'\"()[]{}<>,:;!?|")
			if len(token) < 6 {
				continue
			}
			if strings.Contains(reason, token) {
				return true
			}
		}
	}
	return false
}

func structuralPlanAllowsMultiFileStage(packet implementationTargetPacket, plan structuredExecutionPlan) bool {
	if !implementationTargetPacketNeedsStructuralPolicy(packet) {
		return true
	}
	if !structuralHintsAllowMultiFileStage(packet.StrategyHints) {
		return false
	}
	for _, op := range plan.Operations {
		if !structuredReasonAllowsMultiFileStage(op.Reason) {
			return false
		}
	}
	return true
}

func structuralHintsAllowMultiFileStage(hints []string) bool {
	for _, hint := range hints {
		normalized := strings.ToLower(hint)
		if containsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "between files") {
			return true
		}
	}
	return false
}

func structuredReasonAllowsMultiFileStage(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	return containsAny(normalized, "interface", "adapter", "move ", "moving ", "extract ", "extraction", "cross-file", "across files", "boundary")
}

func preflightResolvePath(mgr *workspace.Manager, path string, workdir string) (string, error) {
	base := mgr.CWD()
	if strings.TrimSpace(workdir) != "" {
		if filepath.IsAbs(workdir) {
			base = workdir
		} else {
			base = filepath.Join(base, workdir)
		}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(base, path)), nil
}

type preflightLineSpan struct {
	start int
	end   int
}

func preflightLineSpans(text string) []preflightLineSpan {
	if text == "" {
		return nil
	}
	spans := make([]preflightLineSpan, 0, strings.Count(text, "\n")+1)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		spans = append(spans, preflightLineSpan{start: start, end: i + 1})
		start = i + 1
	}
	if start <= len(text) {
		spans = append(spans, preflightLineSpan{start: start, end: len(text)})
	}
	return spans
}

func toolActionForStructuredPlan(req workspace.ToolRequest) string {
	switch req.Tool {
	case "edit_file":
		return "edit_file:" + strings.TrimSpace(req.Path)
	case "write_file":
		return "write_file:" + strings.TrimSpace(req.Path)
	case "append_file":
		return "append_file:" + strings.TrimSpace(req.Path)
	default:
		return req.Tool
	}
}

func enclosingSymbolAtLine(file *semantic.GoFile, line int) (semantic.GoSymbol, bool) {
	if file == nil || line <= 0 {
		return semantic.GoSymbol{}, false
	}
	var best semantic.GoSymbol
	bestSpan := 1 << 30
	found := false
	for _, symbol := range file.Symbols {
		if symbol.StartLine <= 0 || symbol.EndLine <= 0 {
			continue
		}
		if line < symbol.StartLine || line > symbol.EndLine {
			continue
		}
		span := symbol.EndLine - symbol.StartLine
		if !found || span < bestSpan {
			best = symbol
			bestSpan = span
			found = true
		}
	}
	return best, found
}

func limitedRelatedFiles(idx *semantic.GoIndex, path string, prompt string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	terms := []string{strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	if file := idx.FileByPath(path); file != nil {
		terms = append(terms, file.Package, file.PathDir())
	}
	terms = append(terms, approvalPromptTerms(prompt)...)
	return idx.RelevantFiles(terms, limit)
}

func packagePeerFiles(idx *semantic.GoIndex, pkgDir string, excludePath string, limit int) []string {
	if idx == nil || limit <= 0 {
		return nil
	}
	peers := make([]string, 0, limit)
	for _, file := range idx.Files {
		if file.Path == excludePath {
			continue
		}
		if file.PathDir() != pkgDir {
			continue
		}
		peers = append(peers, file.Path)
		if len(peers) >= limit {
			break
		}
	}
	return peers
}

func limitedResultLines(output string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	lines := make([]string, 0, limit)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func requestsShellOnlyMutation(normalized string) bool {
	return containsAny(
		normalized,
		"shell commands only",
		"using shell commands only",
		"use shell commands only",
		"via shell only",
		"through shell only",
	)
}

type searchMatch struct {
	Path string
	Line int
	Text string
}

func uncertaintyForStopReason(stopReason string) string {
	stopReason = strings.ToLower(strings.TrimSpace(stopReason))
	switch {
	case strings.Contains(stopReason, "fallback"), strings.Contains(stopReason, "exhausted"):
		return "state that bounded readiness budget was exhausted and identify unresolved gaps"
	case strings.Contains(stopReason, "no_usable"):
		return "state that no usable lane-conformant answer was produced"
	default:
		return ""
	}
}

func detectLanguageCapabilities(root string) []languageCapability {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	seenSignals := map[string]map[string]struct{}{
		"go":      {},
		"cpp":     {},
		"shell":   {},
		"config":  {},
		"docs":    {},
		"prompts": {},
		"tests":   {},
	}
	addSignal := func(key string, signal string) {
		if signal == "" {
			return
		}
		if _, ok := seenSignals[key]; !ok {
			seenSignals[key] = make(map[string]struct{})
		}
		seenSignals[key][signal] = struct{}{}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
		addSignal("go", "go.mod")
	}
	visited := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if visited > 500 {
			return filepath.SkipAll
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", "node_modules", "logs", "models", "tmp", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		ext := strings.ToLower(filepath.Ext(name))
		if strings.HasPrefix(rel, "prompts/") {
			addSignal("prompts", rel)
		}
		if strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") || strings.HasSuffix(name, "_test.go") {
			addSignal("tests", rel)
		}
		switch ext {
		case ".go":
			addSignal("go", rel)
		case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".hxx":
			addSignal("cpp", rel)
		case ".sh", ".bash", ".zsh":
			addSignal("shell", rel)
		case ".json", ".toml", ".yaml", ".yml":
			addSignal("config", rel)
		case ".md", ".mdx", ".rst":
			addSignal("docs", rel)
		}
		return nil
	})
	capabilities := make([]languageCapability, 0, 4)
	addCapability := func(key string, language string, tools []string) {
		signals := sortedStringKeys(seenSignals[key])
		if len(signals) == 0 {
			return
		}
		capabilities = append(capabilities, languageCapability{
			Language: language,
			Signals:  uniqueLimitedStrings(signals, 6),
			Tools:    append([]string(nil), tools...),
		})
	}
	addCapability("go", "go", []string{"go_overview", "go_find_symbol", "go_read_symbol", "go_diagnostics", "go_importers"})
	addCapability("cpp", "c/c++", []string{"cpp_overview", "cpp_find_symbol", "cpp_read_symbol", "clangd diagnostics"})
	addCapability("shell", "shell", []string{"search_content", "read_file", "shell syntax checks"})
	addCapability("config", "config", []string{"read_file", "structured JSON/TOML edits", "targeted validation"})
	addCapability("docs", "docs", []string{"search_content", "read_file", "markdown-aware edits"})
	addCapability("prompts", "prompts", []string{"read_file", "prompt profile inspection", "route contract validation"})
	addCapability("tests", "tests", []string{"go test", "shell test runners", "targeted regression checks"})
	return capabilities
}

func bootstrapRawToolRequest(req workspace.ToolRequest) string {
	data, err := json.Marshal(req)
	if err != nil {
		return req.Tool
	}
	return string(data)
}

func parseDiscoveredPaths(output string) (map[string]string, map[string]struct{}) {
	files := make(map[string]string)
	dirs := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		path, handle, isDir := parseDiscoveredPathLine(line)
		if path == "" {
			continue
		}
		if isDir {
			dirs[path] = struct{}{}
			continue
		}
		files[path] = handle
	}
	return files, dirs
}

func parseDiscoveredFileList(output string) []string {
	files, _ := parseDiscoveredPaths(output)
	return sortedStringMapKeys(files)
}

func parseDiscoveredPathLine(line string) (path string, handle string, isDir bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	if parts := strings.SplitN(line, " ", 2); len(parts) == 2 && looksLikeHandle(parts[0]) {
		handle = parts[0]
		path = strings.TrimSpace(parts[1])
	} else {
		path = line
	}

	if strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/"), handle, true
	}
	return path, handle, false
}

func firstHandleFromOutput(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		_, handle, isDir := parseDiscoveredPathLine(line)
		if handle != "" && !isDir {
			return handle
		}
	}
	return ""
}

func parseSearchMatchPaths(output string) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !looksLikeHandle(parts[0]) {
			continue
		}
		pathParts := strings.SplitN(parts[1], ":", 3)
		if len(pathParts) < 2 {
			continue
		}
		path := strings.TrimSpace(pathParts[0])
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func parseSearchMatches(output string) []searchMatch {
	matches := make([]searchMatch, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !looksLikeHandle(parts[0]) {
			continue
		}
		pathParts := strings.SplitN(parts[1], ":", 3)
		if len(pathParts) < 3 {
			continue
		}
		lineNumber, err := strconv.Atoi(strings.TrimSpace(pathParts[1]))
		if err != nil {
			continue
		}
		match := searchMatch{
			Path: strings.TrimSpace(pathParts[0]),
			Line: lineNumber,
			Text: strings.TrimSpace(pathParts[2]),
		}
		if match.Path == "" {
			continue
		}
		matches = append(matches, match)
	}
	return matches
}

func looksLikeHandle(text string) bool {
	if len(text) < 2 || text[0] != 'f' {
		return false
	}
	for _, ch := range text[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func hasDirectory(dirs map[string]struct{}, name string) bool {
	_, ok := dirs[name]
	return ok
}

func compactOutput(output string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

func longestSymbolInFile(file *semantic.GoFile) *semantic.GoSymbol {
	if file == nil {
		return nil
	}
	var best *semantic.GoSymbol
	bestSpan := 0
	for i := range file.Symbols {
		symbol := &file.Symbols[i]
		if symbol.Kind != "func" && symbol.Kind != "method" {
			continue
		}
		span := symbolSpan(symbol)
		if span > bestSpan {
			best = symbol
			bestSpan = span
		}
	}
	return best
}

func symbolSpan(symbol *semantic.GoSymbol) int {
	if symbol == nil || symbol.EndLine <= 0 || symbol.StartLine <= 0 || symbol.EndLine < symbol.StartLine {
		return 0
	}
	return symbol.EndLine - symbol.StartLine + 1
}

func exportedSymbolCountInFile(file *semantic.GoFile) int {
	if file == nil {
		return 0
	}
	count := 0
	for _, symbol := range file.Symbols {
		if symbol.Name == "" {
			continue
		}
		r := rune(symbol.Name[0])
		if r >= 'A' && r <= 'Z' {
			count++
		}
	}
	return count
}

func firstNonMetaHit(hits []searchMatch) *searchMatch {
	for i := range hits {
		if isMeaningfulReviewHit(hits[i]) {
			return &hits[i]
		}
	}
	return nil
}

func sortedStringKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uniqueLimitedStrings(values []string, limit int) []string {
	if limit == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
		if limit > 0 && len(unique) >= limit {
			break
		}
	}
	return unique
}

type effortPolicySnapshot struct {
	Lane                 string `json:"lane,omitempty"`
	Level                string `json:"level,omitempty"`
	MaxToolSteps         int    `json:"max_tool_steps,omitempty"`
	AnalysisToolBudget   int    `json:"analysis_tool_budget,omitempty"`
	ImplementToolBudget  int    `json:"implement_tool_budget,omitempty"`
	DeepeningPasses      int    `json:"deepening_passes,omitempty"`
	TimeBudgetSeconds    int    `json:"time_budget_seconds,omitempty"`
	MaxOutputTokens      int    `json:"max_output_tokens,omitempty"`
	ReasoningBudget      int    `json:"reasoning_budget,omitempty"`
	ConfidenceFloor      string `json:"confidence_floor,omitempty"`
	BudgetExhaustionMode string `json:"budget_exhaustion_mode,omitempty"`
}

type claimConfidenceSnapshot struct {
	Lane                  string   `json:"lane,omitempty"`
	Confidence            string   `json:"confidence,omitempty"`
	SupportedClaims       int      `json:"supported_claims,omitempty"`
	EvidencePaths         []string `json:"evidence_paths,omitempty"`
	UnresolvedUncertainty []string `json:"unresolved_uncertainty,omitempty"`
	Contradictions        []string `json:"contradictions,omitempty"`
	CalibrationBasis      string   `json:"calibration_basis,omitempty"`
}

type contextCompactionSnapshot struct {
	DroppedMessages  int      `json:"dropped_messages,omitempty"`
	RetainedMessages int      `json:"retained_messages,omitempty"`
	MaxHistory       int      `json:"max_history,omitempty"`
	PinnedPaths      []string `json:"pinned_paths,omitempty"`
	PinnedMemoryKind string   `json:"pinned_memory_kind,omitempty"`
	Strategy         string   `json:"strategy,omitempty"`
}

type toolFailureConfidenceSnapshot struct {
	Lane             string `json:"lane,omitempty"`
	Tool             string `json:"tool,omitempty"`
	Action           string `json:"action,omitempty"`
	ConfidenceImpact string `json:"confidence_impact,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

func buildEffortPolicySnapshot(plan promptExecutionPlan, maxToolSteps int, timeoutSeconds int, maxOutputTokens int, reasoningBudget int) effortPolicySnapshot {
	level := effortLevelForPlan(plan)
	policy := effortPolicySnapshot{
		Lane:                 string(plan.lane),
		Level:                level,
		MaxToolSteps:         maxToolSteps,
		TimeBudgetSeconds:    timeoutSeconds,
		MaxOutputTokens:      maxOutputTokens,
		ReasoningBudget:      reasoningBudget,
		ConfidenceFloor:      confidenceFloorForEffort(level),
		BudgetExhaustionMode: "answer_with_explicit_uncertainty_or_fallback",
	}
	switch plan.lane {
	case analysisLaneExplore, analysisLaneReview, analysisLaneArchitecture:
		policy.AnalysisToolBudget = exploreToolBudget(maxToolSteps)
		policy.DeepeningPasses = 2
	case analysisLaneImplement:
		policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
	case analysisLaneLocate:
		policy.AnalysisToolBudget = exploreToolBudget(maxToolSteps)
		if plan.followupLane == analysisLaneImplement {
			policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
		}
	default:
		policy.AnalysisToolBudget = maxToolSteps
	}
	if plan.followupLane == analysisLaneImplement && policy.ImplementToolBudget == 0 {
		policy.ImplementToolBudget = implementToolBudget(maxToolSteps)
	}
	return policy
}

func effortLevelForPlan(plan promptExecutionPlan) string {
	if plan.thoroughness != "" {
		return string(plan.thoroughness)
	}
	switch plan.lane {
	case analysisLaneImplement:
		return "bounded"
	case analysisLaneMain:
		return "standard"
	default:
		return "medium"
	}
}

func confidenceFloorForEffort(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "quick":
		return "low_with_explicit_uncertainty"
	case "thorough":
		return "high_or_exhausted_budget"
	case "bounded":
		return "verified_or_failed"
	default:
		return "medium"
	}
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func defaultExecutionPlanSummary(plan promptExecutionPlan) string {
	switch plan.lane {
	case analysisLaneReview:
		if routeContinuesToImplement(plan) {
			return "Reviewing bounded evidence before the implementation pass."
		}
		return "Reviewing bounded evidence and ranking likely findings."
	case analysisLaneArchitecture:
		if routeContinuesToImplement(plan) {
			return "Mapping structural boundaries before the implementation pass."
		}
		return "Mapping structural boundaries and ranking architecture hotspots."
	case analysisLaneExplore:
		if routeContinuesToImplement(plan) {
			return "Exploring the repository before the implementation pass."
		}
		return "Exploring the repository and gathering focused context."
	case analysisLaneLocate:
		return "Locating the exact code path before applying the requested edit."
	case analysisLaneImplement:
		return "Preparing a bounded implementation plan against the targeted files."
	default:
		return "Waiting for the response."
	}
}

func hasUILocationHint(normalized string) bool {
	return containsAny(
		normalized,
		"top",
		"header",
		"footer",
		"badge",
		"window",
		"chat",
		"menu",
		"status",
		"banner",
	)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func agentToolActionClass(req workspace.ToolRequest) string {
	if workspace.IsBroadInspectionRequest(req) {
		return "broad_inspection"
	}
	switch strings.TrimSpace(req.Tool) {
	case "read_file":
		return "read_file:" + firstNonEmptyString(strings.TrimSpace(req.ID), strings.TrimSpace(req.Path))
	case "edit_file":
		return "edit_file:" + strings.TrimSpace(req.Path)
	case "search_files", "search_content", "rg":
		return strings.TrimSpace(req.Tool) + ":" + strings.TrimSpace(req.Path)
	case "shell":
		fields := strings.Fields(strings.TrimSpace(req.Command))
		if len(fields) == 0 {
			return "shell"
		}
		return "shell:" + fields[0]
	default:
		return strings.TrimSpace(req.Tool)
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func looksLikeBareMarkerLiteral(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasSuffix(trimmed, ",") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, ","))
	}
	if len(trimmed) < 2 || trimmed[0] != '"' || trimmed[len(trimmed)-1] != '"' {
		return false
	}
	literal := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if literal == "" {
		return false
	}
	if strings.ContainsAny(literal, " \t") {
		return false
	}
	return containsAny(literal, "todo", "fixme", "xxx", "hack", "deprecated", "panic\\(", "bug", "regression", "cleanup", "error")
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func normalizeHistoryRole(role string) string {
	switch role {
	case "user", "assistant", "system":
		return role
	case "tool":
		return "system"
	default:
		return "system"
	}
}
