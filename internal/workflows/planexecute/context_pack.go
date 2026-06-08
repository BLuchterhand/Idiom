package planexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/config"
	"idiom/internal/repository/comprehension"
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

// promptContextPackHasTargets checks whether a PackResult contains at least
// one candidate file, symbol, or test.
func promptContextPackHasTargets(result comprehension.PackResult) bool {
	return len(result.CandidateFiles) > 0 || len(result.CandidateSymbols) > 0 || len(result.Tests) > 0
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
	var packResult comprehension.PackResult
	if err := json.Unmarshal([]byte(result.Output), &packResult); err != nil {
		return "", fmt.Errorf("decode context_pack output: %w", err)
	}
	// Reuse the pre-computed CompactProjection from Pack() when
	// available; fall back to building a fresh projection for
	// pre-Atom-5 cached packs that lack the field.
	projection := packResult.CompactProjection
	if projection.PackID == "" {
		projection = comprehension.NewCompactPackProjection(packResult)
	}
	budgetTokens := opts.BudgetTokens
	if budgetTokens <= 0 {
		budgetTokens = 1200
	}
	compactText, projWithOmitted := comprehension.RenderCompactPackProjection(projection, budgetTokens)

	var b strings.Builder
	b.WriteString("## Context Pack\n\n")
	b.WriteString("Source: idiom `context_pack` automatic preload. Use this ranked packet before broad reads; treat it as cached context and verify exact anchors before source writes.\n\n")
	summary := promptContextViewArtifactPath.ReplaceAllString(strings.TrimSpace(result.Summary), "context view artifact")
	fmt.Fprintf(&b, "- Summary: %s\n", summary)

	if compactText != "" {
		b.WriteString("\n")
		b.WriteString(compactText)
		b.WriteString("\n")
	}

	// Surface omitted counts when budget was enforced.
	if projWithOmitted.OmittedFiles > 0 || projWithOmitted.OmittedSymbols > 0 || projWithOmitted.OmittedTests > 0 {
		b.WriteString("\n_Note: budget cap applied — some candidates omitted from this view. Use `context_pack` directly for the full result._\n")
	}

	if !promptContextPackHasTargets(packResult) {
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
