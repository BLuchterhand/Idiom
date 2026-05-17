package planexecute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/workspace"
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
		timeout = 45 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result := workspace.NewManager(wsCfg).Execute(ctx, workspace.ToolRequest{
		Tool:              "context_pack",
		Task:              promptContextPackTask(opts, brief),
		Mode:              promptContextPackMode(opts.Mode),
		Limit:             limit,
		BudgetTokens:      budgetTokens,
		RequiredEditPaths: planExecuteUniqueRequiredPaths(opts.RequiredEditPaths),
	})
	if strings.TrimSpace(result.Error) != "" {
		if stderr != nil {
			fmt.Fprintf(stderr, "plan-execute: context_pack skipped: %s\n", result.Error)
		}
		return ""
	}
	section, err := RenderPromptContextPackSection(result)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "plan-execute: context_pack skipped: %v\n", err)
		}
		return ""
	}
	if strings.TrimSpace(section) != "" && stderr != nil {
		fmt.Fprintf(stderr, "plan-execute: context_pack preloaded (%s)\n", result.Summary)
	}
	return section
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

func RenderPromptContextPackSection(result workspace.ToolResult) (string, error) {
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
	writePromptContextSymbols(&b, view.CandidateSymbols, 8)
	writePromptContextTests(&b, view.Tests, 4)
	writePromptContextGaps(&b, view.UnresolvedGaps, 4)
	return strings.TrimRight(b.String(), "\n"), nil
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
	return strings.TrimSpace(strings.Join([]string{
		"lane: " + firstPromptContextNonEmpty(strings.TrimSpace(opts.Mode), "plan-execute"),
		"title: " + title,
		"brief:",
		body,
	}, "\n"))
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
	b.WriteString("\n### Candidate Files\n\n")
	for i, file := range files {
		if i >= limit {
			break
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
	}
}

func writePromptContextSymbols(b *strings.Builder, symbols []promptContextPackSymbol, limit int) {
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

func writePromptContextTests(b *strings.Builder, tests []promptContextPackTest, limit int) {
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
