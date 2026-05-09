package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/comprehension"
)

const (
	contextAutoRefreshMaxFiles       = 200
	contextStatusAutoRefreshMaxFiles = 25
)

func (m *Manager) contextIndex(ctx context.Context, req ToolRequest) ToolResult {
	report, err := comprehension.Build(ctx, comprehension.BuildOptions{
		Root:      m.cfg.Root,
		Path:      req.Path,
		Language:  req.Language,
		Mode:      req.Mode,
		MaxFiles:  req.Limit,
		Enrichers: m.contextGraphEnrichers(req.Mode),
	})
	if err != nil {
		return ToolResult{Tool: "context_index", Summary: "Context index build failed", Error: err.Error()}
	}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_index", Summary: "Context index report encode failed", Error: err.Error()}
	}
	return ToolResult{
		Tool:    "context_index",
		Summary: fmt.Sprintf("Indexed %d file(s), %d symbol(s), %d relationship(s), and %d enrichment report(s); artifact: %s", report.FilesIndexed, report.SymbolsIndexed, report.EdgesIndexed, len(report.CollectorReports), report.ArtifactPath),
		Output:  string(output),
	}
}

func (m *Manager) contextStatus(ctx context.Context, req ToolRequest) ToolResult {
	report, err := comprehension.Status(m.cfg.Root)
	if err != nil {
		return ToolResult{Tool: "context_status", Summary: "Context index status failed", Error: err.Error()}
	}
	var refresh *comprehension.RefreshReport
	if req.Refresh {
		refresh = m.contextStatusAutoRefresh(ctx, req, report)
		if refresh != nil && refresh.Attempted && refresh.Error == "" {
			refreshed, err := comprehension.Status(m.cfg.Root)
			if err != nil {
				refresh.Error = err.Error()
			} else {
				report = refreshed
			}
		}
		report.AutoRefresh = refresh
	}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_status", Summary: "Context status report encode failed", Error: err.Error()}
	}
	if !report.Indexed {
		return ToolResult{Tool: "context_status", Summary: "No context index is present" + contextRefreshSummary(refresh), Output: string(output)}
	}
	summary := fmt.Sprintf("Context index has %d file(s), %d symbol(s), and %d stale file(s)", report.FilesIndexed, report.SymbolsIndexed, len(report.StaleFiles)+len(report.MissingFiles))
	if report.CollectorVersionMismatch {
		summary = fmt.Sprintf("Context index collector versions are stale with %d file(s) and %d symbol(s)", report.FilesIndexed, report.SymbolsIndexed)
	}
	if report.Fresh {
		summary = fmt.Sprintf("Context index is fresh with %d file(s) and %d symbol(s)", report.FilesIndexed, report.SymbolsIndexed)
	}
	summary += contextRefreshSummary(refresh)
	return ToolResult{Tool: "context_status", Summary: summary, Output: string(output)}
}

func (m *Manager) contextQuery(ctx context.Context, req ToolRequest) ToolResult {
	refresh := m.contextAutoRefresh(ctx, req, "context_query")
	result, err := comprehension.Query(comprehension.QueryOptions{
		Root:            m.cfg.Root,
		Query:           req.Query,
		Path:            req.Path,
		Symbol:          firstNonEmptyString(req.Pattern, req.ID),
		Scope:           req.Workdir,
		Resolution:      req.Resolution,
		Limit:           req.Limit,
		IncludeEvidence: req.IncludeEvidence,
	})
	if err != nil {
		return ToolResult{Tool: "context_query", Summary: "Context query failed", Error: err.Error()}
	}
	result.AutoRefresh = refresh
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_query", Summary: "Context query report encode failed", Error: err.Error()}
	}
	gap := ""
	if len(result.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(result.UnresolvedGaps))
	}
	refreshSummary := contextRefreshSummary(refresh)
	return ToolResult{
		Tool:    "context_query",
		Summary: fmt.Sprintf("Context query returned %d node(s) and %d edge(s)%s%s", len(result.Nodes), len(result.Edges), gap, refreshSummary),
		Output:  string(output),
	}
}

func (m *Manager) contextZoom(req ToolRequest) ToolResult {
	handle := strings.TrimSpace(firstNonEmptyString(req.ID, req.Pattern, req.Path))
	result, err := comprehension.Zoom(comprehension.ZoomOptions{
		Root:          m.cfg.Root,
		Handle:        handle,
		Resolution:    req.Resolution,
		Limit:         req.Limit,
		IncludeSource: req.IncludeSource,
	})
	if err != nil {
		return ToolResult{Tool: "context_zoom", Summary: "Context zoom failed", Error: err.Error()}
	}
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_zoom", Summary: "Context zoom report encode failed", Error: err.Error()}
	}
	if result.Node == nil {
		return ToolResult{Tool: "context_zoom", Summary: "Context zoom found no node for handle", Output: string(output)}
	}
	return ToolResult{
		Tool:    "context_zoom",
		Summary: fmt.Sprintf("Context zoom returned %d related node(s) and %d edge(s)", len(result.RelatedNodes), len(result.Edges)),
		Output:  string(output),
	}
}

func (m *Manager) contextPack(ctx context.Context, req ToolRequest) ToolResult {
	task := strings.TrimSpace(firstNonEmptyString(req.Task, req.Query, req.Prompt, req.Content, req.Pattern))
	refresh := m.contextAutoRefresh(ctx, req, "context_pack")
	result, err := comprehension.Pack(comprehension.PackOptions{
		Root:           m.cfg.Root,
		Task:           task,
		Path:           req.Path,
		Scope:          req.Workdir,
		Mode:           req.Mode,
		Limit:          req.Limit,
		BudgetTokens:   req.BudgetTokens,
		RequiredTests:  req.RequiredTests,
		ForbiddenPaths: req.ForbiddenPaths,
	})
	if err != nil {
		return ToolResult{Tool: "context_pack", Summary: "Context pack generation failed", Error: err.Error()}
	}
	result.AutoRefresh = refresh
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_pack", Summary: "Context pack report encode failed", Error: err.Error()}
	}
	gap := ""
	if len(result.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(result.UnresolvedGaps))
	}
	refreshSummary := contextRefreshSummary(refresh)
	return ToolResult{
		Tool:    "context_pack",
		Summary: fmt.Sprintf("Context pack wrote %s with %d candidate file(s), %d symbol(s), and %d test(s)%s%s", result.ArtifactPath, len(result.CandidateFiles), len(result.CandidateSymbols), len(result.Tests), gap, refreshSummary),
		Output:  string(output),
	}
}

type contextImpactReport struct {
	Task              string                       `json:"task"`
	ArtifactPath      string                       `json:"artifact_path,omitempty"`
	IndexID           string                       `json:"index_id,omitempty"`
	Fresh             bool                         `json:"fresh"`
	GeneratedAt       string                       `json:"generated_at,omitempty"`
	BudgetTokens      int                          `json:"budget_tokens,omitempty"`
	ImpactScope       []contextImpactItem          `json:"impact_scope,omitempty"`
	Tests             []comprehension.PackTest     `json:"tests,omitempty"`
	Risks             []string                     `json:"risks,omitempty"`
	Unknowns          []string                     `json:"unknowns,omitempty"`
	UnresolvedGaps    []string                     `json:"unresolved_gaps,omitempty"`
	AutoRefresh       *comprehension.RefreshReport `json:"auto_refresh,omitempty"`
	ContextViewPath   string                       `json:"context_view_path,omitempty"`
	ContextViewHandle string                       `json:"context_view_handle,omitempty"`
}

type contextImpactItem struct {
	Handle      string                `json:"handle,omitempty"`
	Path        string                `json:"path,omitempty"`
	Symbol      string                `json:"symbol,omitempty"`
	Kind        string                `json:"kind"`
	Rank        float64               `json:"rank"`
	Signals     []contextImpactSignal `json:"signals,omitempty"`
	Confidence  string                `json:"confidence,omitempty"`
	Limitations []string              `json:"limitations,omitempty"`
}

type contextImpactSignal struct {
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight"`
	Detail string  `json:"detail,omitempty"`
}

func (m *Manager) contextImpact(ctx context.Context, req ToolRequest) ToolResult {
	task := strings.TrimSpace(firstNonEmptyString(req.Task, req.Query, req.Prompt, req.Content, req.Pattern))
	refresh := m.contextAutoRefresh(ctx, req, "context_impact")
	pack, err := comprehension.Pack(comprehension.PackOptions{
		Root:           m.cfg.Root,
		Task:           task,
		Path:           req.Path,
		Scope:          req.Workdir,
		Mode:           firstNonEmptyString(req.Mode, "plan"),
		Limit:          req.Limit,
		BudgetTokens:   req.BudgetTokens,
		RequiredTests:  req.RequiredTests,
		ForbiddenPaths: req.ForbiddenPaths,
	})
	if err != nil {
		return ToolResult{Tool: "context_impact", Summary: "Context impact ranking failed", Error: err.Error()}
	}
	pack.AutoRefresh = refresh
	report := contextImpactReport{
		Task:              pack.Task,
		ArtifactPath:      pack.ArtifactPath,
		IndexID:           pack.IndexID,
		Fresh:             pack.Fresh,
		GeneratedAt:       pack.GeneratedAt,
		BudgetTokens:      pack.BudgetTokens,
		ImpactScope:       contextImpactItems(pack, req.Limit),
		Tests:             append([]comprehension.PackTest(nil), pack.Tests...),
		Risks:             append([]string(nil), pack.Risks...),
		Unknowns:          append([]string(nil), pack.Unknowns...),
		UnresolvedGaps:    append([]string(nil), pack.UnresolvedGaps...),
		AutoRefresh:       refresh,
		ContextViewPath:   pack.ArtifactPath,
		ContextViewHandle: "context_pack://" + pack.PackID,
	}
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_impact", Summary: "Context impact report encode failed", Error: err.Error()}
	}
	gap := ""
	if len(report.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(report.UnresolvedGaps))
	}
	return ToolResult{
		Tool:    "context_impact",
		Summary: fmt.Sprintf("Context impact ranked %d item(s), %d test(s); context view: %s%s%s", len(report.ImpactScope), len(report.Tests), report.ContextViewPath, gap, contextRefreshSummary(refresh)),
		Output:  string(output),
	}
}

func contextImpactItems(pack comprehension.PackResult, limit int) []contextImpactItem {
	var items []contextImpactItem
	for _, file := range pack.CandidateFiles {
		rank := 0.74
		role := strings.ToLower(strings.TrimSpace(file.Role))
		if strings.Contains(role, "modify") || strings.Contains(role, "edit") {
			rank = 0.92
		}
		if strings.Contains(role, "test") {
			rank = 0.78
		}
		// Prefer the explicit score from the comprehension ranker when it
		// produced one; the role-based defaults remain as a fall-back for
		// callers that bypass the ranker.
		if file.Score > 0 {
			rank = file.Score
		}
		signals := []contextImpactSignal{{
			Kind:   "context_pack_candidate",
			Weight: 0.32,
			Detail: firstNonEmptyString(file.Reason, file.Role),
		}}
		if file.Score > 0 {
			signals = append(signals, contextImpactSignal{
				Kind:   "ranker_score",
				Weight: file.Score,
				Detail: "ranker_score",
			})
		}
		if file.Hub {
			signals = append(signals, contextImpactSignal{
				Kind:   "hub_demotion",
				Weight: 0.6,
				Detail: "fanout_or_name_hub",
			})
		}
		item := contextImpactItem{
			Handle:     firstNonEmptyString(firstString(file.Handles), "file://"+file.Path),
			Path:       file.Path,
			Kind:       "file",
			Rank:       rank,
			Confidence: firstNonEmptyString(file.Confidence, "heuristic"),
			Signals:    signals,
		}
		if file.Stale {
			item.Limitations = append(item.Limitations, "candidate was marked stale by context graph")
		}
		items = append(items, item)
	}
	for _, symbol := range pack.CandidateSymbols {
		item := contextImpactItem{
			Handle:     firstNonEmptyString(symbol.ID, "symbol://"+symbol.Path+"#"+symbol.QualifiedName),
			Path:       symbol.Path,
			Symbol:     symbol.QualifiedName,
			Kind:       firstNonEmptyString(symbol.Kind, "symbol"),
			Rank:       0.68,
			Confidence: firstNonEmptyString(symbol.Confidence, "heuristic"),
			Signals: []contextImpactSignal{{
				Kind:   "context_pack_symbol",
				Weight: 0.30,
				Detail: symbol.Reason,
			}},
		}
		if symbol.Stale {
			item.Limitations = append(item.Limitations, "symbol was marked stale by context graph")
		}
		items = append(items, item)
	}
	for _, test := range pack.Tests {
		handle := ""
		if test.Command != "" {
			handle = "test://" + contextImpactStableHandle(test.Command)
		} else if test.Path != "" {
			handle = "file://" + test.Path
		}
		items = append(items, contextImpactItem{
			Handle:     handle,
			Path:       test.Path,
			Symbol:     test.Symbol,
			Kind:       "test_command",
			Rank:       0.62,
			Confidence: "exact_tool_output",
			Signals: []contextImpactSignal{{
				Kind:   "validation_candidate",
				Weight: 0.25,
				Detail: firstNonEmptyString(test.Reason, test.Command),
			}},
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Rank == items[j].Rank {
			return items[i].Kind+"\x00"+items[i].Handle < items[j].Kind+"\x00"+items[j].Handle
		}
		return items[i].Rank > items[j].Rank
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func contextImpactStableHandle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unspecified"
	}
	sum := 0
	for _, ch := range value {
		sum = (sum*33 + int(ch)) & 0x7fffffff
	}
	return fmt.Sprintf("%08x", sum)
}

func (m *Manager) contextAutoRefresh(ctx context.Context, req ToolRequest, purpose string) *comprehension.RefreshReport {
	scope := boundedContextRefreshScope(req)
	status, statusErr := comprehension.Status(m.cfg.Root)
	if statusErr != nil {
		return &comprehension.RefreshReport{
			Attempted:     false,
			Scope:         scope,
			Language:      contextRefreshLanguage(req),
			Reason:        "status_error",
			SkippedReason: "context status failed",
			Error:         statusErr.Error(),
		}
	}
	reason := contextRefreshReason(m.cfg.Root, status, scope)
	if reason == "" {
		return nil
	}
	report := &comprehension.RefreshReport{
		Attempted: false,
		Scope:     scope,
		Language:  contextRefreshLanguage(req),
		Reason:    reason,
	}
	if scope == "" {
		report.SkippedReason = "automatic context refresh needs a bounded path scope; run context_index with path . for an intentional full rebuild"
		return report
	}
	report.Attempted = true
	build, err := comprehension.Build(ctx, comprehension.BuildOptions{
		Root:      m.cfg.Root,
		Path:      scope,
		Language:  report.Language,
		Mode:      contextRefreshIndexMode(req.Mode),
		MaxFiles:  contextAutoRefreshMaxFiles,
		Enrichers: m.contextGraphEnrichers(contextRefreshIndexMode(req.Mode)),
	})
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.IndexID = build.IndexID
	report.FilesIndexed = build.FilesIndexed
	report.SymbolsIndexed = build.SymbolsIndexed
	report.EdgesIndexed = build.EdgesIndexed
	report.PartialRefreshReason = build.PartialRefreshReason
	report.FullRebuildReason = build.FullRebuildReason
	report.RefreshedFiles = build.RefreshedStaleFiles
	if after, err := comprehension.Status(m.cfg.Root); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func (m *Manager) contextStatusAutoRefresh(ctx context.Context, req ToolRequest, status comprehension.StatusReport) *comprehension.RefreshReport {
	scope := boundedContextRefreshScope(req)
	if scope != "" {
		return m.contextAutoRefresh(ctx, req, "context_status")
	}
	if !status.Indexed {
		return m.contextRefreshRoot(ctx, req, "missing_index")
	}
	reason := contextRefreshReason(m.cfg.Root, status, "")
	if reason == "" {
		return nil
	}
	if reason == "collector_version_mismatch" {
		return m.contextRefreshRoot(ctx, req, reason)
	}
	paths := contextStatusRefreshPaths(status)
	report := &comprehension.RefreshReport{
		Attempted: false,
		Language:  contextRefreshLanguage(req),
		Reason:    reason,
	}
	if len(paths) == 0 {
		report.SkippedReason = "automatic context refresh needs a bounded path scope; run context_index with path . for an intentional full rebuild"
		return report
	}
	if len(paths) > contextStatusAutoRefreshMaxFiles {
		report.SkippedReason = fmt.Sprintf("automatic context status refresh found %d stale/missing file(s), above cap %d; provide a bounded path or run context_index with path . for an intentional full rebuild", len(paths), contextStatusAutoRefreshMaxFiles)
		return report
	}
	return m.contextRefreshFileSet(ctx, req, paths, "small_stale_file_set")
}

func (m *Manager) contextRefreshRoot(ctx context.Context, req ToolRequest, reason string) *comprehension.RefreshReport {
	report := &comprehension.RefreshReport{
		Attempted: true,
		Scope:     ".",
		Language:  contextRefreshLanguage(req),
		Reason:    reason,
	}
	mode := contextRefreshIndexMode(req.Mode)
	build, err := comprehension.Build(ctx, comprehension.BuildOptions{
		Root:      m.cfg.Root,
		Path:      ".",
		Language:  report.Language,
		Mode:      mode,
		Enrichers: m.contextGraphEnrichers(mode),
	})
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.IndexID = build.IndexID
	report.FilesIndexed = build.FilesIndexed
	report.SymbolsIndexed = build.SymbolsIndexed
	report.EdgesIndexed = build.EdgesIndexed
	report.PartialRefreshReason = build.PartialRefreshReason
	report.FullRebuildReason = build.FullRebuildReason
	report.RefreshedFiles = build.RefreshedStaleFiles
	if after, err := comprehension.Status(m.cfg.Root); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func (m *Manager) contextRefreshFileSet(ctx context.Context, req ToolRequest, paths []string, reason string) *comprehension.RefreshReport {
	report := &comprehension.RefreshReport{
		Attempted:      true,
		Scope:          fmt.Sprintf("%d stale/missing file(s)", len(paths)),
		Language:       contextRefreshLanguage(req),
		Reason:         reason,
		RefreshedFiles: append([]string(nil), paths...),
	}
	mode := contextRefreshIndexMode(req.Mode)
	for _, path := range paths {
		language := contextRefreshLanguageForPath(m.cfg.Root, path, req)
		build, err := comprehension.Build(ctx, comprehension.BuildOptions{
			Root:      m.cfg.Root,
			Path:      path,
			Language:  language,
			Mode:      mode,
			MaxFiles:  contextAutoRefreshMaxFiles,
			Enrichers: m.contextGraphEnrichers(mode),
		})
		if err != nil {
			report.Error = err.Error()
			return report
		}
		report.IndexID = build.IndexID
		report.FilesIndexed += build.FilesIndexed
		report.SymbolsIndexed += build.SymbolsIndexed
		report.EdgesIndexed += build.EdgesIndexed
		if report.PartialRefreshReason == "" {
			report.PartialRefreshReason = build.PartialRefreshReason
		}
		if report.FullRebuildReason == "" {
			report.FullRebuildReason = build.FullRebuildReason
		}
	}
	if after, err := comprehension.Status(m.cfg.Root); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func contextStatusRefreshPaths(status comprehension.StatusReport) []string {
	seen := make(map[string]struct{}, len(status.StaleFiles)+len(status.MissingFiles))
	for _, path := range append(append([]string(nil), status.StaleFiles...), status.MissingFiles...) {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" || path == "." {
			continue
		}
		seen[path] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func contextRefreshLanguageForPath(root string, path string, req ToolRequest) string {
	language := contextRefreshLanguage(req)
	if language == "auto" {
		return language
	}
	abs := filepath.Join(root, filepath.FromSlash(path))
	if _, err := os.Stat(abs); err != nil && os.IsNotExist(err) {
		return "auto"
	}
	return language
}

func boundedContextRefreshScope(req ToolRequest) string {
	scope := strings.TrimSpace(firstNonEmptyString(req.Path, req.Workdir))
	scope = filepath.ToSlash(filepath.Clean(scope))
	if scope == "." || scope == "" {
		return ""
	}
	return strings.TrimSuffix(scope, "/")
}

func contextRefreshLanguage(req ToolRequest) string {
	language := strings.TrimSpace(req.Language)
	if language == "" {
		return "auto"
	}
	return language
}

func contextRefreshIndexMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick", "standard", "deep":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "standard"
	}
}

func contextRefreshReason(root string, status comprehension.StatusReport, scope string) string {
	if !status.Indexed {
		return "missing_index"
	}
	if scope != "" && !contextGraphHasFileInScope(root, scope) {
		return "scope_not_indexed"
	}
	if status.CollectorVersionMismatch {
		return "collector_version_mismatch"
	}
	if scope == "" {
		if !status.Fresh {
			return "stale_unbounded_graph"
		}
		return ""
	}
	for _, path := range append(append([]string(nil), status.StaleFiles...), status.MissingFiles...) {
		if pathInContextToolScope(path, scope) {
			return "stale_scope_files"
		}
	}
	return ""
}

func contextGraphHasFileInScope(root string, scope string) bool {
	graph, err := comprehension.LoadGraph(root)
	if err != nil {
		return false
	}
	for _, node := range graph.Nodes {
		if node.Kind == "file" && pathInContextToolScope(node.Path, scope) {
			return true
		}
	}
	return false
}

func pathInContextToolScope(path string, scope string) bool {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	scope = filepath.ToSlash(filepath.Clean(strings.TrimSpace(scope)))
	if path == "" || scope == "" || scope == "." {
		return false
	}
	return path == scope || strings.HasPrefix(path, scope+"/")
}

func contextRefreshSummary(refresh *comprehension.RefreshReport) string {
	if refresh == nil {
		return ""
	}
	if refresh.Attempted {
		if refresh.Error != "" {
			return fmt.Sprintf("; auto-refresh attempted for %s but failed", refresh.Scope)
		}
		return fmt.Sprintf("; auto-refreshed %s (%d file(s))", refresh.Scope, refresh.FilesIndexed)
	}
	if refresh.SkippedReason != "" {
		return "; auto-refresh skipped"
	}
	return ""
}

func (m *Manager) contextTracePack(ctx context.Context, req ToolRequest) ToolResult {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return ToolResult{Tool: "context_trace_pack", Summary: "Context trace pack rejected", Error: "command is required"}
	}
	if !m.cfg.EnableShell {
		return ToolResult{Tool: "context_trace_pack", Summary: "Context trace pack rejected", Error: "shell execution is disabled"}
	}
	if m.Mode() == ModeExplore {
		if err := validateExploreShellCommand(command); err != nil {
			return ToolResult{
				Tool:    "context_trace_pack",
				Summary: "Context trace pack rejected",
				Error:   "explore mode only allows read-only inspection commands: " + err.Error(),
			}
		}
	}
	workdir, err := m.resolveWorkdir(req.Workdir)
	if err != nil {
		return ToolResult{Tool: "context_trace_pack", Summary: "Context trace pack failed to resolve workdir", Error: err.Error()}
	}
	stdout, stderr, execErr := m.runCommandDetailed(ctx, workdir, m.cfg.Shell, "-lc", command)
	exitCode := 0
	execError := ""
	if execErr != nil {
		execError = execErr.Error()
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	result, err := comprehension.Trace(comprehension.TraceOptions{
		Root:         m.cfg.Root,
		Command:      command,
		Path:         req.Path,
		Language:     req.Language,
		Stdout:       stdout,
		Stderr:       stderr,
		ExitCode:     exitCode,
		ExecError:    execError,
		Limit:        req.Limit,
		IncludeFiles: req.IncludeFiles,
	})
	if err != nil {
		return ToolResult{Tool: "context_trace_pack", Summary: "Context trace pack generation failed", Error: err.Error()}
	}
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_trace_pack", Summary: "Context trace pack report encode failed", Error: err.Error()}
	}
	status := "passed"
	if !result.Success {
		status = "failed"
	}
	gap := ""
	if len(result.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(result.UnresolvedGaps))
	}
	return ToolResult{
		Tool:    "context_trace_pack",
		Summary: fmt.Sprintf("Context trace pack wrote %s for %s command with %d event(s), %d executed file(s), and %d failing anchor(s)%s", result.ArtifactPath, status, len(result.Events), len(result.ExecutedFiles), len(result.FailingAnchors), gap),
		Output:  string(output),
	}
}
