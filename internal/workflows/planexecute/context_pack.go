package planexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
)

type PromptContextPackOptions struct {
	Mode              string
	Title             string
	Brief             string
	ProjectRoot       string
	RequiredEditPaths []string
	Limit             int
	BudgetTokens      int
	Timeout           time.Duration
}

type promptContextPackView struct {
	PackID           string                    `json:"pack_id"`
	Fresh            bool                      `json:"fresh"`
	Freshness        *promptContextFreshness   `json:"freshness"`
	CandidateFiles   []promptContextPackFile   `json:"candidate_files"`
	CandidateSymbols []promptContextPackSymbol `json:"candidate_symbols"`
	Tests            []promptContextPackTest   `json:"tests"`
	UnresolvedGaps   []string                  `json:"unresolved_gaps"`
}

type promptContextFreshness struct {
	Policy        string `json:"policy"`
	FreshForScope bool   `json:"fresh_for_scope"`
	GlobalFresh   bool   `json:"global_fresh"`
	Reason        string `json:"reason"`
}

type promptContextPackFile struct {
	Path       string  `json:"path"`
	Role       string  `json:"role"`
	Reason     string  `json:"reason"`
	Language   string  `json:"language"`
	Confidence string  `json:"confidence"`
	Score      float64 `json:"score"`
	Hub        bool    `json:"hub"`
}

type promptContextPackSymbol struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	Confidence    string `json:"confidence"`
	Reason        string `json:"reason"`
}

type promptContextPackTest struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Symbol  string `json:"symbol"`
}

var promptContextViewArtifactPath = regexp.MustCompile(`\.idiom/comprehension/views/[A-Za-z0-9._-]+\.json`)

func ComposePlannerPromptWithContextPack(brief string, contextPackSection string) string {
	body := strings.TrimSpace(brief)
	if !strings.Contains(body, pathparse.BriefBodyStartMarker) {
		body = pathparse.WrapBriefBody(body)
	}
	if strings.TrimSpace(contextPackSection) == "" {
		return body
	}
	return strings.TrimSpace(contextPackSection) + "\n\n" + body
}

func BuildPromptContextPackSection(cfg config.Config, opts PromptContextPackOptions, stderr io.Writer) string {
	brief := strings.TrimSpace(opts.Brief)
	if brief == "" || PromptAlreadyHasContextPack(brief) {
		return ""
	}
	wsCfg := cfg.Workspace
	wsCfg.Root = firstPromptContextNonEmpty(strings.TrimSpace(opts.ProjectRoot), strings.TrimSpace(wsCfg.Root), strings.TrimSpace(wsCfg.CWD), ".")
	if strings.TrimSpace(wsCfg.CWD) == "" {
		wsCfg.CWD = wsCfg.Root
	}
	wsCfg.EnableWrites = false

	limit := opts.Limit
	if limit <= 0 {
		limit = 8
	}
	budgetTokens := opts.BudgetTokens
	if budgetTokens <= 0 {
		budgetTokens = 1200
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result := executePromptContextPackWithFreshnessRecovery(ctx, workspace.NewManager(wsCfg), workspace.ToolRequest{
		Tool:              "context_pack",
		Task:              promptContextPackTask(opts, brief),
		Mode:              promptContextPackMode(opts.Mode),
		Limit:             limit,
		BudgetTokens:      budgetTokens,
		RequiredEditPaths: planExecuteUniqueRequiredPaths(opts.RequiredEditPaths),
	}, stderr)
	if strings.TrimSpace(result.Error) != "" {
		if stderr != nil {
			fmt.Fprintf(stderr, "plan-execute: context_pack skipped: %s\n", result.Error)
		}
		return RenderPromptContextDiscoveryFallbackSection(opts, result.Error)
	}
	section, err := RenderPromptContextPackSectionWithOptions(result, opts)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "plan-execute: context_pack skipped: %v\n", err)
		}
		return RenderPromptContextDiscoveryFallbackSection(opts, err.Error())
	}
	if strings.TrimSpace(section) != "" && stderr != nil {
		fmt.Fprintf(stderr, "plan-execute: context_pack preloaded (%s)\n", result.Summary)
	}
	return section
}

func executePromptContextPackWithFreshnessRecovery(ctx context.Context, mgr *workspace.Manager, req workspace.ToolRequest, stderr io.Writer) workspace.ToolResult {
	result := mgr.Execute(ctx, req)
	if !promptContextPackNeedsFreshnessRecovery(result) {
		return result
	}
	if stderr != nil {
		fmt.Fprintln(stderr, "context_pack freshness blocked; refreshing context status before retry")
	}
	refresh := workspace.ToolRequest{
		Tool:              "context_status",
		Refresh:           true,
		Limit:             50,
		FreshnessMaxFiles: 2000,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	}
	if strings.TrimSpace(req.Language) != "" {
		refresh.Language = req.Language
	}
	refreshResult := mgr.Execute(ctx, refresh)
	if stderr != nil {
		summary := strings.TrimSpace(refreshResult.Summary)
		if summary == "" {
			summary = strings.TrimSpace(refreshResult.Error)
		}
		if summary != "" {
			fmt.Fprintf(stderr, "context_status refresh before context_pack retry: %s\n", collapsePromptContextWhitespace(summary, 220))
		}
	}
	retry := mgr.Execute(ctx, req)
	if strings.TrimSpace(retry.Error) == "" || strings.TrimSpace(refreshResult.Error) == "" {
		return retry
	}
	return result
}

func promptContextPackNeedsFreshnessRecovery(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "freshness_blocker" {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	return strings.Contains(text, "freshness_blocker") ||
		strings.Contains(text, "stale_unbounded_graph") ||
		strings.Contains(text, "context freshness")
}

func RenderPromptContextPackSection(result workspace.ToolResult) (string, error) {
	return RenderPromptContextPackSectionWithOptions(result, PromptContextPackOptions{})
}

func PromptAlreadyHasContextPack(body string) bool {
	lower := strings.ToLower(body)
	trimmed := strings.TrimSpace(lower)
	return strings.Contains(lower, "\n## context pack") ||
		strings.Contains(lower, "\n## context packet") ||
		strings.Contains(lower, "\n# context pack") ||
		strings.Contains(lower, "\n# context packet") ||
		strings.Contains(lower, "\ncontext pack:") ||
		strings.Contains(lower, "\ncontext packet:") ||
		strings.HasPrefix(trimmed, "## context pack") ||
		strings.HasPrefix(trimmed, "# context pack") ||
		strings.HasPrefix(trimmed, "context pack:") ||
		strings.HasPrefix(trimmed, "context packet:")
}

func RenderPromptContextPackSectionWithOptions(result workspace.ToolResult, opts PromptContextPackOptions) (string, error) {
	var view promptContextPackView
	if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
		return "", fmt.Errorf("decode context_pack output: %w", err)
	}
	var b strings.Builder
	b.WriteString("## Context Pack\n\n")
	b.WriteString("Source: idiom `context_pack` automatic preload. Use this ranked packet before broad reads; treat it as cached context and verify exact anchors before source writes.\n\n")
	summary := promptContextViewArtifactPath.ReplaceAllString(strings.TrimSpace(result.Summary), "context view artifact")
	fmt.Fprintf(&b, "- Summary: %s\n", summary)
	if view.PackID != "" {
		fmt.Fprintf(&b, "- Pack: `%s`\n", view.PackID)
	}
	if view.Freshness != nil {
		fmt.Fprintf(&b, "- Freshness: policy=%s fresh_for_scope=%t global_fresh=%t", view.Freshness.Policy, view.Freshness.FreshForScope, view.Freshness.GlobalFresh)
		if view.Freshness.Reason != "" {
			fmt.Fprintf(&b, " reason=%s", view.Freshness.Reason)
		}
		b.WriteByte('\n')
	} else {
		fmt.Fprintf(&b, "- Fresh: %t\n", view.Fresh)
	}
	writePromptContextFiles(&b, view.CandidateFiles, 8)
	writePromptContextSymbols(&b, view.CandidateSymbols, view.CandidateFiles, 8)
	writePromptContextTests(&b, view.Tests, view.CandidateFiles, opts, 4)
	writePromptContextGaps(&b, view.UnresolvedGaps, 4)
	if !promptContextPackHasTargets(view) {
		b.WriteString("\n\n")
		b.WriteString(RenderPromptContextDiscoveryFallbackSection(PromptContextPackOptions{}, "context_pack returned no candidate files, symbols, or tests"))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func RenderPromptContextDiscoveryFallbackSection(opts PromptContextPackOptions, reason string) string {
	var b strings.Builder
	b.WriteString("## Context Discovery Fallback\n\n")
	b.WriteString("Source: idiom context preload fallback. The ranked context packet was unavailable or did not identify a useful target. Run this bounded discovery phase before the first edit-bearing action; do not repeat the same failed `context_pack` call.\n\n")
	if reason = strings.TrimSpace(reason); reason != "" {
		fmt.Fprintf(&b, "- Context packet status: unavailable (%s)\n", collapsePromptContextWhitespace(reason, 220))
	}
	if title := strings.TrimSpace(opts.Title); title != "" {
		fmt.Fprintf(&b, "- Objective: %s\n", collapsePromptContextWhitespace(title, 160))
	}
	if len(opts.RequiredEditPaths) > 0 {
		fmt.Fprintf(&b, "- Required edit paths from brief: %s\n", strings.Join(planExecuteUniqueRequiredPaths(opts.RequiredEditPaths), ", "))
	}
	b.WriteString("\n### Required Fallback Discovery Phase\n\n")
	b.WriteString("- Start with `search_files` for likely filenames, component names, package names, or UI/domain terms from the objective.\n")
	b.WriteString("- Then use `search_content` for stable labels, symbols, config keys, command names, or error text. If a literal phrase misses, broaden to nearby concepts instead of repeating it.\n")
	b.WriteString("- Read only the highest-confidence candidate file or symbol range with `read_file` or a language-aware symbol tool before the first `submit_plan` or write action.\n")
	b.WriteString("- If all bounded discovery misses, answer with `<<QUESTION>>:` and name the exact missing target evidence; do not invent an edit path.\n")
	return strings.TrimRight(b.String(), "\n")
}

func promptContextPackHasTargets(view promptContextPackView) bool {
	return len(view.CandidateFiles) > 0 || len(view.CandidateSymbols) > 0 || len(view.Tests) > 0
}

func promptContextPackMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "implement":
		return "implement"
	default:
		return "plan"
	}
}

func promptContextPackTask(opts PromptContextPackOptions, brief string) string {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = firstPromptContextMarkdownHeading(brief)
	}
	if title == "" {
		title = "tui plan-execute dispatch"
	}
	body := normalizePromptContextBody(brief)
	if len(body) > 8000 {
		body = body[:8000]
	}
	// Mode is passed separately via ToolRequest.Mode, so including
	// "lane: implement" in the task string only pollutes the ranking
	// with generic implementation-keyword matches, drowning out the
	// actual target files for the user request.
	//
	// Use title + body: title gives the ranker a focused anchor while
	// the body provides the full task context for keyword matching.
	return strings.TrimSpace(strings.Join([]string{
		title,
		body,
	}, "\n\n"))
}

func normalizePromptContextBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstPromptContextMarkdownHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func writePromptContextFiles(b *strings.Builder, files []promptContextPackFile, limit int) {
	if len(files) == 0 {
		return
	}
	var rendered int
	for i, file := range files {
		if i >= limit {
			break
		}
		if strings.EqualFold(strings.TrimSpace(file.Role), "test") {
			continue
		}
		if rendered == 0 {
			b.WriteString("\n### Candidate Files\n\n")
		}
		role := firstPromptContextNonEmpty(file.Role, "candidate")
		score := ""
		if file.Score > 0 {
			score = fmt.Sprintf(", score=%.2f", file.Score)
		}
		hub := ""
		if file.Hub {
			hub = ", hub"
		}
		fmt.Fprintf(b, "- `%s` (%s%s%s): %s\n", file.Path, role, score, hub, firstPromptContextNonEmpty(file.Reason, file.Confidence, file.Language))
		rendered++
	}
}

func writePromptContextSymbols(b *strings.Builder, symbols []promptContextPackSymbol, files []promptContextPackFile, limit int) {
	symbols = promptContextRelevantSymbols(symbols, files)
	if len(symbols) == 0 {
		return
	}
	b.WriteString("\n### Candidate Symbols\n\n")
	for i, sym := range symbols {
		if i >= limit {
			break
		}
		name := firstPromptContextNonEmpty(sym.QualifiedName, sym.ID)
		loc := sym.Path
		if sym.StartLine > 0 {
			loc = fmt.Sprintf("%s:%d", loc, sym.StartLine)
		}
		fmt.Fprintf(b, "- `%s` in `%s` (%s): %s\n", name, loc, firstPromptContextNonEmpty(sym.Kind, "symbol"), firstPromptContextNonEmpty(sym.Reason, sym.Confidence))
	}
}

func promptContextRelevantSymbols(symbols []promptContextPackSymbol, files []promptContextPackFile) []promptContextPackSymbol {
	sourceDirs := promptContextEditableSourceDirs(files)
	if len(sourceDirs) == 0 {
		return symbols
	}
	out := make([]promptContextPackSymbol, 0, len(symbols))
	for _, sym := range symbols {
		path := filepath.ToSlash(strings.TrimSpace(sym.Path))
		if path == "" {
			continue
		}
		if _, ok := sourceDirs[filepath.ToSlash(filepath.Dir(path))]; ok {
			out = append(out, sym)
		}
	}
	return out
}

func writePromptContextTests(b *strings.Builder, tests []promptContextPackTest, files []promptContextPackFile, opts PromptContextPackOptions, limit int) {
	tests = promptContextRelevantTests(tests, files, opts)
	if len(tests) == 0 {
		return
	}
	b.WriteString("\n### Candidate Tests\n\n")
	for i, test := range tests {
		if i >= limit {
			break
		}
		target := firstPromptContextNonEmpty(test.Symbol, test.Path)
		if target != "" {
			target = " - " + target
		}
		fmt.Fprintf(b, "- `%s`%s\n", test.Command, target)
	}
}

func promptContextRelevantTests(tests []promptContextPackTest, files []promptContextPackFile, opts PromptContextPackOptions) []promptContextPackTest {
	sourceDirs := promptContextEditableSourceDirs(files)
	out := promptContextSiblingTests(files, opts)
	seen := map[string]struct{}{}
	for _, test := range out {
		seen[promptContextTestKey(test)] = struct{}{}
	}
	for _, test := range tests {
		if !promptContextTestMatchesSourceDir(test, sourceDirs) {
			continue
		}
		key := promptContextTestKey(test)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, test)
	}
	return out
}

func promptContextEditableSourceDirs(files []promptContextPackFile) map[string]struct{} {
	dirs := map[string]struct{}{}
	for _, file := range files {
		if strings.EqualFold(strings.TrimSpace(file.Role), "test") {
			continue
		}
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		if path == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		if dir == "." || dir == "" {
			continue
		}
		dirs[dir] = struct{}{}
	}
	return dirs
}

func promptContextSiblingTests(files []promptContextPackFile, opts PromptContextPackOptions) []promptContextPackTest {
	root := firstPromptContextNonEmpty(opts.ProjectRoot, ".")
	var out []promptContextPackTest
	for _, file := range files {
		if strings.EqualFold(strings.TrimSpace(file.Role), "test") {
			continue
		}
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		if !strings.HasSuffix(strings.ToLower(path), ".go") || strings.HasSuffix(strings.ToLower(path), "_test.go") {
			continue
		}
		testPath := strings.TrimSuffix(path, ".go") + "_test.go"
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(testPath)))
		if err != nil || info.IsDir() {
			continue
		}
		out = append(out, promptContextPackTest{
			Command: "go test ./" + filepath.ToSlash(filepath.Dir(path)),
			Path:    testPath,
		})
	}
	return out
}

func promptContextTestMatchesSourceDir(test promptContextPackTest, sourceDirs map[string]struct{}) bool {
	if len(sourceDirs) == 0 {
		return false
	}
	path := filepath.ToSlash(strings.TrimSpace(test.Path))
	if path != "" {
		if _, ok := sourceDirs[filepath.ToSlash(filepath.Dir(path))]; ok {
			return true
		}
	}
	command := strings.TrimSpace(test.Command)
	for dir := range sourceDirs {
		if strings.Contains(command, "./"+dir) {
			return true
		}
	}
	return false
}

func promptContextTestKey(test promptContextPackTest) string {
	return strings.TrimSpace(test.Command) + "\x00" + filepath.ToSlash(strings.TrimSpace(test.Path)) + "\x00" + strings.TrimSpace(test.Symbol)
}

func writePromptContextGaps(b *strings.Builder, gaps []string, limit int) {
	if len(gaps) == 0 {
		return
	}
	b.WriteString("\n### Context Gaps\n\n")
	for i, gap := range gaps {
		if i >= limit {
			break
		}
		fmt.Fprintf(b, "- %s\n", gap)
	}
}

func firstPromptContextNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func collapsePromptContextWhitespace(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit > 0 && len(value) > limit {
		return strings.TrimSpace(value[:limit-1]) + "..."
	}
	return value
}
