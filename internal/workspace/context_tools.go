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
