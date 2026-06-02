package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/comprehension"
	"idiom/internal/failures"
	"idiom/internal/util"
)

const (
	defaultContextStatusOutputLimit              = 50
	defaultContextStatusUnboundedRefreshMaxFiles = 200
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
	report, err := contextStatusReport(m.cfg.Root, req)
	if err != nil {
		return ToolResult{Tool: "context_status", Summary: "Context index status failed", Error: err.Error()}
	}
	var refresh *comprehension.RefreshReport
	var freshness *comprehension.FreshnessDecision
	if req.Refresh {
		decision := m.contextStatusFreshness(ctx, req, report)
		freshness = &decision
		refresh = decision.Refresh
		if refresh != nil && refresh.Attempted && refresh.Error == "" {
			refreshed, err := contextStatusReport(m.cfg.Root, req)
			if err != nil {
				refresh.Error = err.Error()
			} else {
				report = refreshed
			}
		}
		report.AutoRefresh = refresh
		report.Freshness = freshness
		report = comprehension.StatusReportWithMetadata(report, "context_status", freshness)
	} else {
		policy := contextFreshnessPolicy(req, comprehension.FreshnessReportOnly)
		decision, err := comprehension.EnsureFresh(ctx, comprehension.FreshnessRequest{
			Root:      m.cfg.Root,
			Operation: "context_status",
			Policy:    policy,
			Path:      util.FirstNonEmpty(req.Path, req.Workdir),
			Query:     util.FirstNonEmpty(req.Query, req.Task, req.Prompt, req.Content, req.Pattern),
			Language:  contextRefreshLanguage(req),
			Mode:      req.Mode,
			MaxFiles:  req.FreshnessMaxFiles,
			Status:    &report,
		})
		if err != nil {
			return ToolResult{Tool: "context_status", Summary: "Context index status freshness report failed", Error: err.Error()}
		}
		if boundedContextRefreshScope(req) != "" {
			decision.GlobalFresh = false
		}
		report.Freshness = &decision
		report = comprehension.StatusReportWithMetadata(report, "context_status", &decision)
	}
	report = limitContextStatusReport(report, contextStatusOutputLimit(req))
	output, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_status", Summary: "Context status report encode failed", Error: err.Error()}
	}
	if !report.Indexed {
		return ToolResult{Tool: "context_status", Summary: "No context index is present" + contextRefreshSummary(refresh), Output: string(output)}
	}
	summary := fmt.Sprintf("Context index has %d file(s), %d symbol(s), and %d stale file(s)", report.FilesIndexed, report.SymbolsIndexed, contextStatusStaleSummaryCount(report))
	if report.CollectorVersionMismatch {
		summary = fmt.Sprintf("Context index collector versions are stale with %d file(s) and %d symbol(s)", report.FilesIndexed, report.SymbolsIndexed)
	}
	if report.Fresh {
		summary = fmt.Sprintf("Context index is fresh with %d file(s) and %d symbol(s)", report.FilesIndexed, report.SymbolsIndexed)
	}
	summary += contextRefreshSummary(refresh)
	return ToolResult{Tool: "context_status", Summary: summary, Output: string(output)}
}

func contextStatusReport(root string, req ToolRequest) (comprehension.StatusReport, error) {
	if scope := boundedContextRefreshScope(req); scope != "" {
		return comprehension.StatusForScope(root, scope)
	}
	return comprehension.Status(root)
}

func (m *Manager) contextQuery(ctx context.Context, req ToolRequest) ToolResult {
	freshness, err := m.contextReadFreshness(ctx, req, "context_query")
	if err != nil {
		return ToolResult{Tool: "context_query", Summary: "Context query freshness check failed", Error: err.Error()}
	}
	if comprehension.StaleEvidenceBlocksUse(freshness) {
		return contextFreshnessBlockerResult("context_query", freshness)
	}
	result, err := comprehension.Query(comprehension.QueryOptions{
		Root:            m.cfg.Root,
		Query:           req.Query,
		Path:            req.Path,
		Symbol:          util.FirstNonEmpty(req.Pattern, req.ID),
		Scope:           req.Workdir,
		Resolution:      req.Resolution,
		Limit:           req.Limit,
		IncludeEvidence: req.IncludeEvidence,
	})
	if err != nil {
		return ToolResult{Tool: "context_query", Summary: "Context query failed", Error: err.Error()}
	}
	result.AutoRefresh = freshness.Refresh
	result.Freshness = &freshness
	result.Confidence = comprehension.GraphResultConfidenceForFreshness(result.Fresh, &freshness)
	result.StaleEvidence = comprehension.StaleEvidenceSafetyForFreshness(freshness)
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_query", Summary: "Context query report encode failed", Error: err.Error()}
	}
	gap := ""
	if len(result.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(result.UnresolvedGaps))
	}
	refreshSummary := contextRefreshSummary(freshness.Refresh)
	return ToolResult{
		Tool:    "context_query",
		Summary: fmt.Sprintf("Context query returned %d node(s) and %d edge(s)%s%s", len(result.Nodes), len(result.Edges), gap, refreshSummary),
		Output:  string(output),
	}
}

func (m *Manager) contextZoom(ctx context.Context, req ToolRequest) ToolResult {
	handle := strings.TrimSpace(util.FirstNonEmpty(req.ID, req.Handle, req.Pattern, req.Path))
	freshness, err := m.contextReadFreshness(ctx, ToolRequest{
		Tool:              req.Tool,
		ID:                handle,
		Handle:            handle,
		Pattern:           req.Pattern,
		Path:              req.Path,
		Language:          req.Language,
		Mode:              req.Mode,
		Resolution:        req.Resolution,
		FreshnessPolicy:   req.FreshnessPolicy,
		FreshnessMaxFiles: req.FreshnessMaxFiles,
	}, "context_zoom")
	if err != nil {
		return ToolResult{Tool: "context_zoom", Summary: "Context zoom freshness check failed", Error: err.Error()}
	}
	if comprehension.StaleEvidenceBlocksUse(freshness) {
		return contextFreshnessBlockerResult("context_zoom", freshness)
	}
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
	result.Freshness = &freshness
	result.Confidence = comprehension.GraphResultConfidenceForFreshness(result.Fresh, &freshness)
	result.StaleEvidence = comprehension.StaleEvidenceSafetyForFreshness(freshness)
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
	task := strings.TrimSpace(util.FirstNonEmpty(req.Task, req.Query, req.Prompt, req.Content, req.Pattern))
	freshness, err := m.contextReadFreshness(ctx, req, "context_pack")
	if err != nil {
		return ToolResult{Tool: "context_pack", Summary: "Context pack freshness check failed", Error: err.Error()}
	}
	if comprehension.StaleEvidenceBlocksUse(freshness) {
		return contextFreshnessBlockerResult("context_pack", freshness)
	}
	result, err := comprehension.Pack(comprehension.PackOptions{
		Root:              m.cfg.Root,
		Task:              task,
		Path:              req.Path,
		Scope:             req.Workdir,
		Mode:              req.Mode,
		Limit:             req.Limit,
		BudgetTokens:      req.BudgetTokens,
		RequiredTests:     req.RequiredTests,
		RequiredEditPaths: req.RequiredEditPaths,
		ForbiddenPaths:    req.ForbiddenPaths,
		Freshness:         &freshness,
	})
	if err != nil {
		return ToolResult{Tool: "context_pack", Summary: "Context pack generation failed", Error: err.Error()}
	}
	result.AutoRefresh = freshness.Refresh
	result.Freshness = &freshness
	result.Confidence = comprehension.GraphResultConfidenceForFreshness(result.Fresh, &freshness)
	result.StaleEvidence = comprehension.StaleEvidenceSafetyForFreshness(freshness)
	result.Provenance.Operation = "context_pack"
	if result.Provenance.Source == "" {
		result.Provenance.Source = "context_graph"
	}
	if result.Provenance.ArtifactPath == "" {
		result.Provenance.ArtifactPath = result.ArtifactPath
	}
	if result.Provenance.IndexID == "" {
		result.Provenance.IndexID = freshness.IndexID
		result.Provenance.StoreKind = freshness.StoreKind
		result.Provenance.GeneratedAt = freshness.GeneratedAt
	}
	comprehension.AttachPackResultContract(&result)
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolResult{Tool: "context_pack", Summary: "Context pack report encode failed", Error: err.Error()}
	}
	gap := ""
	if len(result.UnresolvedGaps) > 0 {
		gap = fmt.Sprintf("; %d gap(s)", len(result.UnresolvedGaps))
	}
	refreshSummary := contextRefreshSummary(freshness.Refresh)
	return ToolResult{
		Tool:    "context_pack",
		Summary: fmt.Sprintf("Context pack wrote %s with %d candidate file(s), %d symbol(s), and %d test(s)%s%s", result.ArtifactPath, len(result.CandidateFiles), len(result.CandidateSymbols), len(result.Tests), gap, refreshSummary),
		Output:  string(output),
	}
}

type contextImpactReport struct {
	Task              string                              `json:"task"`
	ArtifactPath      string                              `json:"artifact_path,omitempty"`
	IndexID           string                              `json:"index_id,omitempty"`
	StoreKind         string                              `json:"store_kind,omitempty"`
	Fresh             bool                                `json:"fresh"`
	Freshness         *comprehension.FreshnessDecision    `json:"freshness,omitempty"`
	Confidence        comprehension.GraphResultConfidence `json:"confidence"`
	Provenance        comprehension.GraphResultProvenance `json:"provenance"`
	GeneratedAt       string                              `json:"generated_at,omitempty"`
	BudgetTokens      int                                 `json:"budget_tokens,omitempty"`
	ImpactScope       []contextImpactItem                 `json:"impact_scope,omitempty"`
	Tests             []comprehension.PackTest            `json:"tests,omitempty"`
	Risks             []string                            `json:"risks,omitempty"`
	Unknowns          []string                            `json:"unknowns,omitempty"`
	UnresolvedGaps    []string                            `json:"unresolved_gaps,omitempty"`
	AutoRefresh       *comprehension.RefreshReport        `json:"auto_refresh,omitempty"`
	StaleEvidence     comprehension.StaleEvidenceSafety   `json:"stale_evidence"`
	ContextViewPath   string                              `json:"context_view_path,omitempty"`
	ContextViewHandle string                              `json:"context_view_handle,omitempty"`
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
	task := m.contextImpactTask(req)
	freshness, err := m.contextReadFreshness(ctx, req, "context_impact")
	if err != nil {
		return ToolResult{Tool: "context_impact", Summary: "Context impact freshness check failed", Error: err.Error()}
	}
	if comprehension.StaleEvidenceBlocksUse(freshness) {
		return contextFreshnessBlockerResult("context_impact", freshness)
	}
	pack, err := comprehension.Pack(comprehension.PackOptions{
		Root:              m.cfg.Root,
		Task:              task,
		Path:              req.Path,
		Scope:             req.Workdir,
		Mode:              util.FirstNonEmpty(req.Mode, "plan"),
		Limit:             req.Limit,
		BudgetTokens:      req.BudgetTokens,
		RequiredTests:     req.RequiredTests,
		RequiredEditPaths: req.RequiredEditPaths,
		ForbiddenPaths:    req.ForbiddenPaths,
		Freshness:         &freshness,
	})
	if err != nil {
		return ToolResult{Tool: "context_impact", Summary: "Context impact ranking failed", Error: err.Error()}
	}
	pack.AutoRefresh = freshness.Refresh
	pack.Freshness = &freshness
	pack.StaleEvidence = comprehension.StaleEvidenceSafetyForFreshness(freshness)
	provenance := pack.Provenance
	provenance.Operation = "context_impact"
	report := contextImpactReport{
		Task:              pack.Task,
		ArtifactPath:      pack.ArtifactPath,
		IndexID:           pack.IndexID,
		StoreKind:         pack.StoreKind,
		Fresh:             pack.Fresh,
		Freshness:         &freshness,
		Confidence:        comprehension.GraphResultConfidenceForFreshness(pack.Fresh, &freshness),
		Provenance:        provenance,
		GeneratedAt:       pack.GeneratedAt,
		BudgetTokens:      pack.BudgetTokens,
		ImpactScope:       contextImpactItems(pack, req.Limit),
		Tests:             append([]comprehension.PackTest(nil), pack.Tests...),
		Risks:             append([]string(nil), pack.Risks...),
		Unknowns:          append([]string(nil), pack.Unknowns...),
		UnresolvedGaps:    append([]string(nil), pack.UnresolvedGaps...),
		AutoRefresh:       freshness.Refresh,
		StaleEvidence:     comprehension.StaleEvidenceSafetyForFreshness(freshness),
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
		Summary: fmt.Sprintf("Context impact ranked %d item(s), %d test(s); context view: %s%s%s", len(report.ImpactScope), len(report.Tests), report.ContextViewPath, gap, contextRefreshSummary(freshness.Refresh)),
		Output:  string(output),
	}
}

func (m *Manager) contextImpactTask(req ToolRequest) string {
	if task := strings.TrimSpace(util.FirstNonEmpty(req.Task, req.Query, req.Prompt, req.Content, req.Pattern)); task != "" {
		return task
	}
	path := normalizeGraphOutputPath(req.Path, m.cfg.Root)
	if path == "" {
		return ""
	}
	return "Graph-backed impact evidence for " + path
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
			Detail: util.FirstNonEmpty(file.Reason, file.Role),
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
			Handle:     util.FirstNonEmpty(firstString(file.Handles), "file://"+file.Path),
			Path:       file.Path,
			Kind:       "file",
			Rank:       rank,
			Confidence: util.FirstNonEmpty(file.Confidence, "heuristic"),
			Signals:    signals,
		}
		if file.Stale {
			item.Limitations = append(item.Limitations, "candidate was marked stale by context graph")
		}
		items = append(items, item)
	}
	for _, symbol := range pack.CandidateSymbols {
		item := contextImpactItem{
			Handle:     util.FirstNonEmpty(symbol.ID, "symbol://"+symbol.Path+"#"+symbol.QualifiedName),
			Path:       symbol.Path,
			Symbol:     symbol.QualifiedName,
			Kind:       util.FirstNonEmpty(symbol.Kind, "symbol"),
			Rank:       0.68,
			Confidence: util.FirstNonEmpty(symbol.Confidence, "heuristic"),
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
				Detail: util.FirstNonEmpty(test.Reason, test.Command),
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

func contextFreshnessPolicy(req ToolRequest, fallback comprehension.FreshnessPolicy) comprehension.FreshnessPolicy {
	switch strings.ToLower(strings.TrimSpace(req.FreshnessPolicy)) {
	case "":
		return fallback
	case string(comprehension.FreshnessReportOnly):
		return comprehension.FreshnessReportOnly
	case string(comprehension.FreshnessReadThrough):
		return comprehension.FreshnessReadThrough
	case string(comprehension.FreshnessRequire):
		return comprehension.FreshnessRequire
	case string(comprehension.FreshnessAllowStale):
		return comprehension.FreshnessAllowStale
	default:
		return fallback
	}
}

func (m *Manager) contextReadFreshness(ctx context.Context, req ToolRequest, operation string) (comprehension.FreshnessDecision, error) {
	return comprehension.EnsureFresh(ctx, comprehension.FreshnessRequest{
		Root:             m.cfg.Root,
		Operation:        operation,
		Policy:           contextFreshnessPolicy(req, comprehension.FreshnessReadThrough),
		Path:             util.FirstNonEmpty(req.Path, req.Workdir),
		Handle:           util.FirstNonEmpty(req.ID, req.Handle, req.Pattern),
		Query:            util.FirstNonEmpty(req.Query, req.Task, req.Prompt, req.Content, req.Pattern),
		ReferencedPaths:  req.RequiredEditPaths,
		Language:         contextRefreshLanguage(req),
		Mode:             req.Mode,
		MaxFiles:         req.FreshnessMaxFiles,
		PatchActiveStore: true,
		Enrichers:        m.contextGraphEnrichers(contextRefreshIndexMode(req.Mode)),
	})
}

func (m *Manager) contextStatusFreshness(ctx context.Context, req ToolRequest, status comprehension.StatusReport) comprehension.FreshnessDecision {
	freshnessStatus := status
	decision, err := comprehension.EnsureFresh(ctx, comprehension.FreshnessRequest{
		Root:              m.cfg.Root,
		Operation:         "context_status",
		Policy:            contextFreshnessPolicy(req, comprehension.FreshnessReadThrough),
		Path:              util.FirstNonEmpty(req.Path, req.Workdir),
		Query:             util.FirstNonEmpty(req.Query, req.Task, req.Prompt, req.Content, req.Pattern),
		ReferencedPaths:   req.RequiredEditPaths,
		Language:          contextRefreshLanguage(req),
		Mode:              req.Mode,
		MaxFiles:          contextStatusRefreshMaxFiles(req, status),
		ExactScopeRefresh: contextStatusExactScopeRefresh(req, status),
		PatchActiveStore:  true,
		Enrichers:         m.contextGraphEnrichers(contextRefreshIndexMode(req.Mode)),
		Status:            &freshnessStatus,
	})
	if err != nil {
		refresh := &comprehension.RefreshReport{
			Attempted:     false,
			Scope:         boundedContextRefreshScope(req),
			Language:      contextRefreshLanguage(req),
			Reason:        "status_error",
			SkippedReason: "status_error",
			Error:         err.Error(),
		}
		return comprehension.FreshnessDecision{
			Policy:        comprehension.FreshnessReadThrough,
			Operation:     "context_status",
			FreshForScope: false,
			GlobalFresh:   status.Fresh,
			Scope:         refresh.Scope,
			Reason:        refresh.Reason,
			Blocker:       refresh.SkippedReason,
			Refresh:       refresh,
		}
	}
	if boundedContextRefreshScope(req) != "" {
		decision.GlobalFresh = false
	}
	return decision
}

func (m *Manager) contextStatusAutoRefresh(ctx context.Context, req ToolRequest, status comprehension.StatusReport) *comprehension.RefreshReport {
	decision := m.contextStatusFreshness(ctx, req, status)
	return decision.Refresh
}

func contextStatusRefreshMaxFiles(req ToolRequest, status comprehension.StatusReport) int {
	if boundedContextRefreshScope(req) != "" || len(req.RequiredEditPaths) > 0 {
		return 0
	}
	if req.FreshnessMaxFiles > 0 {
		return req.FreshnessMaxFiles
	}
	if req.Limit > 0 {
		return req.Limit
	}
	if status.Indexed && !status.Fresh && !status.CollectorVersionMismatch {
		return defaultContextStatusUnboundedRefreshMaxFiles
	}
	return 0
}

func contextStatusExactScopeRefresh(req ToolRequest, status comprehension.StatusReport) bool {
	return boundedContextRefreshScope(req) == "" && len(req.RequiredEditPaths) == 0 && status.Indexed && !status.CollectorVersionMismatch
}

func contextStatusStaleSummaryCount(report comprehension.StatusReport) int {
	if report.StaleFileCount > 0 || report.MissingFileCount > 0 {
		return report.StaleFileCount + report.MissingFileCount
	}
	return len(report.StaleFiles) + len(report.MissingFiles)
}

func contextStatusOutputLimit(req ToolRequest) int {
	if req.Limit > 0 {
		return req.Limit
	}
	return defaultContextStatusOutputLimit
}

func limitContextStatusReport(report comprehension.StatusReport, limit int) comprehension.StatusReport {
	if limit <= 0 {
		return report
	}
	report.StaleFiles = limitStringSlice(report.StaleFiles, limit)
	report.MissingFiles = limitStringSlice(report.MissingFiles, limit)
	report.StaleSymbolSamples = limitNodeSummarySlice(report.StaleSymbolSamples, limit)
	if report.AutoRefresh != nil {
		refresh := *report.AutoRefresh
		refresh.RefreshedFiles = limitStringSlice(refresh.RefreshedFiles, limit)
		report.AutoRefresh = &refresh
	}
	if report.Freshness != nil {
		freshness := *report.Freshness
		freshness.CheckedPaths = limitStringSlice(freshness.CheckedPaths, limit)
		freshness.StalePaths = limitStringSlice(freshness.StalePaths, limit)
		freshness.MissingPaths = limitStringSlice(freshness.MissingPaths, limit)
		if freshness.Refresh != nil {
			refresh := *freshness.Refresh
			refresh.RefreshedFiles = limitStringSlice(refresh.RefreshedFiles, limit)
			freshness.Refresh = &refresh
		}
		report.Freshness = &freshness
	}
	return report
}

func limitStringSlice(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return append([]string(nil), values[:limit]...)
}

func limitNodeSummarySlice(values []comprehension.NodeSummary, limit int) []comprehension.NodeSummary {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return append([]comprehension.NodeSummary(nil), values[:limit]...)
}

func contextFreshnessBlockerResult(tool string, freshness comprehension.FreshnessDecision) ToolResult {
	output, err := json.MarshalIndent(struct {
		Error         string                            `json:"error"`
		Freshness     comprehension.FreshnessDecision   `json:"freshness"`
		StaleEvidence comprehension.StaleEvidenceSafety `json:"stale_evidence"`
	}{
		Error:         "freshness_blocker",
		Freshness:     freshness,
		StaleEvidence: comprehension.StaleEvidenceSafetyForFreshness(freshness),
	}, "", "  ")
	if err != nil {
		return toolInternalFailureResult(tool, "Context freshness blocker encode failed", err)
	}
	reason := strings.TrimSpace(freshness.Reason)
	if reason == "" {
		reason = "freshness_blocker"
	}
	return ToolResult{
		Tool:    tool,
		Summary: fmt.Sprintf("%s blocked by context freshness (%s)", tool, reason),
		Output:  string(output),
		Error:   "freshness_blocker",
		Failure: failures.New(
			failures.FailedPrecondition,
			reasonContextFreshnessBlocker,
			fmt.Sprintf("%s blocked by context freshness (%s)", tool, reason),
		),
	}
}

func boundedContextRefreshScope(req ToolRequest) string {
	scope := strings.TrimSpace(util.FirstNonEmpty(req.Path, req.Workdir))
	scope = filepath.ToSlash(filepath.Clean(scope))
	if scope == "." || scope == "" {
		return ""
	}
	return strings.TrimSuffix(scope, "/")
}

func contextRefreshLanguage(req ToolRequest) string {
	language := strings.TrimSpace(req.Language)
	if language != "" && !strings.EqualFold(language, "auto") {
		return language
	}
	if support, ok := LanguageForFile(boundedContextRefreshScope(req)); ok {
		return support.Name
	}
	return "auto"
}

func contextRefreshIndexMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick", "standard", "deep":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "standard"
	}
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
