package comprehension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FreshnessPolicy string

const (
	FreshnessReportOnly  FreshnessPolicy = "report_only"
	FreshnessReadThrough FreshnessPolicy = "read_through"
	FreshnessRequire     FreshnessPolicy = "require_fresh"
	FreshnessAllowStale  FreshnessPolicy = "allow_stale"
)

const (
	FreshnessReasonBudgetExceeded            = "budget_exceeded"
	FreshnessReasonCollectorVersionMismatch  = "collector_version_mismatch"
	FreshnessReasonMissingIndex              = "missing_index"
	FreshnessReasonMissingScope              = "missing_scope"
	FreshnessReasonRefreshEstimateFailed     = "refresh_estimate_failed"
	FreshnessReasonRefreshFailed             = "refresh_failed"
	FreshnessReasonReferencedPathsUnverified = "referenced_paths_unverified"
	FreshnessReasonScopeNotIndexed           = "scope_not_indexed"
	FreshnessReasonStaleGraph                = "stale_graph"
	FreshnessReasonStaleScopeFiles           = "stale_scope_files"
	FreshnessReasonStaleUnboundedGraph       = "stale_unbounded_graph"
)

type FreshnessRequest struct {
	Root              string
	Operation         string
	Policy            FreshnessPolicy
	Path              string
	Handle            string
	Query             string
	ReferencedPaths   []string
	Language          string
	Mode              string
	MaxFiles          int
	MaxDuration       time.Duration
	ExactScopeRefresh bool
	PatchActiveStore  bool
	Enrichers         []GraphEnricher
	Status            *StatusReport
}

type FreshnessDecision struct {
	Policy           FreshnessPolicy `json:"policy"`
	Operation        string          `json:"operation,omitempty"`
	IndexID          string          `json:"index_id,omitempty"`
	StoreKind        string          `json:"store_kind,omitempty"`
	GeneratedAt      string          `json:"generated_at,omitempty"`
	FreshForScope    bool            `json:"fresh_for_scope"`
	GlobalFresh      bool            `json:"global_fresh"`
	Scope            string          `json:"scope,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	Blocker          string          `json:"blocker,omitempty"`
	BudgetExceeded   bool            `json:"budget_exceeded,omitempty"`
	Refresh          *RefreshReport  `json:"refresh,omitempty"`
	CheckedPaths     []string        `json:"checked_paths,omitempty"`
	CheckedPathCount int             `json:"checked_path_count"`
	StalePaths       []string        `json:"stale_paths,omitempty"`
	StalePathCount   int             `json:"stale_path_count"`
	MissingPaths     []string        `json:"missing_paths,omitempty"`
	MissingPathCount int             `json:"missing_path_count"`
}

type StaleEvidenceSafety struct {
	State            string          `json:"state"`
	Policy           FreshnessPolicy `json:"policy,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	FreshForScope    bool            `json:"fresh_for_scope"`
	GlobalFresh      bool            `json:"global_fresh"`
	StalePathCount   int             `json:"stale_path_count,omitempty"`
	MissingPathCount int             `json:"missing_path_count,omitempty"`
}

type FreshnessStats struct {
	ReadThroughAttempts   int64            `json:"read_through_attempts"`
	AllowStaleInvocations int64            `json:"allow_stale_invocations"`
	Blockers              int64            `json:"blockers"`
	BlockersByReason      map[string]int64 `json:"blockers_by_reason,omitempty"`
	RefreshFailures       int64            `json:"refresh_failures"`
	RootRebuilds          int64            `json:"root_rebuilds"`
}

func StaleEvidenceSafetyForFreshness(decision FreshnessDecision) StaleEvidenceSafety {
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = strings.TrimSpace(decision.Blocker)
	}
	if reason == "" && !decision.FreshForScope {
		reason = "freshness_blocker"
	}
	state := "fresh"
	if !decision.FreshForScope {
		switch decision.Policy {
		case FreshnessReportOnly:
			state = "reported"
		case FreshnessAllowStale:
			state = "allowed_stale"
		default:
			state = "blocked"
		}
	}
	return StaleEvidenceSafety{
		State:            state,
		Policy:           decision.Policy,
		Reason:           reason,
		FreshForScope:    decision.FreshForScope,
		GlobalFresh:      decision.GlobalFresh,
		StalePathCount:   decision.StalePathCount,
		MissingPathCount: decision.MissingPathCount,
	}
}

func StaleEvidenceSafetyForStatus(report StatusReport, freshness *FreshnessDecision) StaleEvidenceSafety {
	if freshness != nil {
		return StaleEvidenceSafetyForFreshness(*freshness)
	}
	if report.Fresh {
		return StaleEvidenceSafety{
			State:         "fresh",
			FreshForScope: true,
			GlobalFresh:   true,
		}
	}
	reason := FreshnessReasonStaleGraph
	if !report.Indexed {
		reason = FreshnessReasonMissingIndex
	} else if report.CollectorVersionMismatch {
		reason = FreshnessReasonCollectorVersionMismatch
	}
	return StaleEvidenceSafety{
		State:            "reported",
		Policy:           FreshnessReportOnly,
		Reason:           reason,
		FreshForScope:    false,
		GlobalFresh:      report.Fresh,
		StalePathCount:   report.StalePathCount,
		MissingPathCount: report.MissingFileCount,
	}
}

func StaleEvidenceBlocksUse(decision FreshnessDecision) bool {
	if decision.FreshForScope {
		return false
	}
	switch decision.Policy {
	case FreshnessReportOnly, FreshnessAllowStale:
		return false
	default:
		return true
	}
}

var (
	freshnessReadThroughAttempts   atomic.Int64
	freshnessAllowStaleInvocations atomic.Int64
	freshnessBlockers              atomic.Int64
	freshnessRefreshFailures       atomic.Int64
	freshnessRootRebuilds          atomic.Int64
	freshnessStatsMu               sync.Mutex
	freshnessBlockersByReason      = make(map[string]int64)
)

func FreshnessHealthStats() FreshnessStats {
	freshnessStatsMu.Lock()
	defer freshnessStatsMu.Unlock()
	blockersByReason := make(map[string]int64, len(freshnessBlockersByReason))
	for reason, count := range freshnessBlockersByReason {
		blockersByReason[reason] = count
	}
	return FreshnessStats{
		ReadThroughAttempts:   freshnessReadThroughAttempts.Load(),
		AllowStaleInvocations: freshnessAllowStaleInvocations.Load(),
		Blockers:              freshnessBlockers.Load(),
		BlockersByReason:      blockersByReason,
		RefreshFailures:       freshnessRefreshFailures.Load(),
		RootRebuilds:          freshnessRootRebuilds.Load(),
	}
}

func EnsureFresh(ctx context.Context, req FreshnessRequest) (decision FreshnessDecision, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := normalizeRoot(req.Root)
	if err != nil {
		return FreshnessDecision{}, err
	}
	policy := req.Policy
	if policy == "" {
		policy = FreshnessReadThrough
	}
	operation := strings.TrimSpace(req.Operation)
	scope := inferFreshnessScope(root, req)
	language := freshnessLanguage(req.Language)
	mode := freshnessIndexMode(req.Mode)
	decision = FreshnessDecision{
		Policy:    policy,
		Operation: operation,
		Scope:     scope,
	}
	recordFreshnessPolicy(policy)
	defer func() {
		recordFreshnessOutcome(decision)
	}()

	var status StatusReport
	if req.Status != nil {
		status = *req.Status
	} else {
		var err error
		status, err = statusBeforeFreshnessRefresh(root, scope)
		if err != nil {
			return decision, err
		}
	}
	decision.GlobalFresh = status.Fresh
	decision.CheckedPaths = freshnessCheckedPaths(req, scope)
	decision.StalePaths = freshnessRelevantPaths(status.StaleFiles, scope, decision.CheckedPaths)
	decision.MissingPaths = freshnessRelevantPaths(status.MissingFiles, scope, decision.CheckedPaths)
	decision.FreshForScope = freshnessScopeFresh(status, scope, decision.CheckedPaths)
	decision.Reason = freshnessReason(root, status, scope, decision.CheckedPaths)
	applyFreshnessStatusMetadata(&decision, status)
	if decision.Reason == "" {
		decision.FreshForScope = true
		return decision, nil
	}

	switch policy {
	case FreshnessReportOnly:
		return decision, nil
	case FreshnessAllowStale:
		decision.BudgetExceeded = freshnessBudgetExceeded(status, decision, req.MaxFiles)
		return decision, nil
	case FreshnessReadThrough, FreshnessRequire:
	default:
		return decision, fmt.Errorf("unknown freshness policy %q", policy)
	}

	refreshCtx := ctx
	var cancel context.CancelFunc
	if req.MaxDuration > 0 {
		refreshCtx, cancel = context.WithTimeout(ctx, req.MaxDuration)
		defer cancel()
	}
	refresh, err := runFreshnessRefresh(refreshCtx, root, req, status, decision, language, mode)
	if err != nil {
		return decision, err
	}
	decision.Refresh = refresh
	if refresh != nil {
		if refresh.Reason == FreshnessReasonBudgetExceeded {
			decision.Reason = refresh.Reason
		}
		if refresh.SkippedReason != "" {
			decision.Blocker = refresh.SkippedReason
		} else if refresh.Error != "" {
			decision.Blocker = FreshnessReasonRefreshFailed
		}
		if refresh.Reason == FreshnessReasonBudgetExceeded || refresh.SkippedReason == FreshnessReasonBudgetExceeded || strings.Contains(strings.ToLower(refresh.SkippedReason), "budget") || strings.Contains(strings.ToLower(refresh.SkippedReason), "cap") {
			decision.BudgetExceeded = true
		}
	}
	if refresh == nil || !refresh.Attempted || refresh.Error != "" {
		return decision, nil
	}

	afterScope := scope
	if refresh.Scope == "." {
		afterScope = ""
	}
	after, err := statusAfterFreshnessRefresh(root, afterScope, req.Operation, status)
	if err != nil {
		if refresh != nil {
			refresh.Error = err.Error()
		}
		return decision, nil
	}
	decision.GlobalFresh = after.Fresh
	decision.StalePaths = freshnessRelevantPaths(after.StaleFiles, scope, decision.CheckedPaths)
	decision.MissingPaths = freshnessRelevantPaths(after.MissingFiles, scope, decision.CheckedPaths)
	decision.FreshForScope = freshnessScopeFresh(after, scope, decision.CheckedPaths)
	applyFreshnessStatusMetadata(&decision, after)
	if !decision.FreshForScope && decision.Blocker == "" {
		decision.Blocker = freshnessBlocker(decision.Reason, scope, len(decision.StalePaths)+len(decision.MissingPaths), req.MaxFiles)
	}
	if decision.Blocker == FreshnessReasonBudgetExceeded {
		decision.Reason = FreshnessReasonBudgetExceeded
		decision.BudgetExceeded = true
	}
	return decision, nil
}

func statusBeforeFreshnessRefresh(root string, scope string) (StatusReport, error) {
	return Status(root)
}

func runFreshnessRefresh(ctx context.Context, root string, req FreshnessRequest, status StatusReport, decision FreshnessDecision, language string, mode string) (*RefreshReport, error) {
	if status.CollectorVersionMismatch {
		if freshnessOperationCanRebuildRoot(req.Operation) {
			return refreshFreshnessRoot(ctx, root, req, FreshnessReasonCollectorVersionMismatch, language, mode), nil
		}
		return skippedFreshnessRefresh(decision, language, FreshnessReasonCollectorVersionMismatch), nil
	}
	if decision.Scope != "" {
		return refreshFreshnessScope(ctx, root, req, decision.Scope, decision.Reason, language, mode), nil
	}
	if !status.Indexed {
		if freshnessOperationCanRebuildRoot(req.Operation) {
			return refreshFreshnessRoot(ctx, root, req, FreshnessReasonMissingIndex, language, mode), nil
		}
		return skippedFreshnessRefresh(decision, language, FreshnessReasonMissingIndex), nil
	}
	if len(decision.CheckedPaths) > 0 && decision.Reason == FreshnessReasonStaleScopeFiles {
		paths := append(append([]string(nil), decision.StalePaths...), decision.MissingPaths...)
		if len(paths) == 0 {
			return skippedFreshnessRefresh(decision, language, FreshnessReasonReferencedPathsUnverified), nil
		}
		if req.MaxFiles > 0 && len(paths) > req.MaxFiles {
			report := skippedFreshnessRefresh(decision, language, FreshnessReasonBudgetExceeded)
			report.Reason = FreshnessReasonBudgetExceeded
			report.StalePathCount = len(paths)
			report.MaxFiles = req.MaxFiles
			return report, nil
		}
		return refreshFreshnessFileSet(ctx, root, req, paths, FreshnessReasonStaleScopeFiles, language, mode), nil
	}
	paths := freshnessStatusPaths(status)
	if len(paths) == 0 {
		return skippedFreshnessRefresh(decision, language, FreshnessReasonMissingScope), nil
	}
	if req.MaxFiles > 0 && len(paths) > req.MaxFiles {
		report := skippedFreshnessRefresh(decision, language, FreshnessReasonBudgetExceeded)
		report.Reason = FreshnessReasonBudgetExceeded
		report.StalePathCount = len(paths)
		report.MaxFiles = req.MaxFiles
		return report, nil
	}
	if req.Operation != "context_status" {
		return refreshFreshnessRoot(ctx, root, req, FreshnessReasonStaleUnboundedGraph, language, mode), nil
	}
	return refreshFreshnessFileSet(ctx, root, req, paths, "small_stale_file_set", language, mode), nil
}

func freshnessOperationCanRebuildRoot(operation string) bool {
	switch operation {
	case "context_status", "context_query", "context_zoom", "context_pack", "context_impact", "discovery_query":
		return true
	default:
		return false
	}
}

func refreshFreshnessScope(ctx context.Context, root string, req FreshnessRequest, scope string, reason string, language string, mode string) *RefreshReport {
	report := &RefreshReport{
		Attempted: true,
		Scope:     scope,
		Language:  language,
		Reason:    reason,
	}
	build, err := Build(ctx, BuildOptions{
		Root:             root,
		Path:             scope,
		Language:         language,
		Mode:             mode,
		MaxFiles:         req.MaxFiles,
		ExactScope:       req.ExactScopeRefresh,
		PatchActiveStore: req.PatchActiveStore || req.ExactScopeRefresh,
		Enrichers:        req.Enrichers,
	})
	if err != nil {
		report.Error = err.Error()
		return report
	}
	applyBuildReportToRefresh(report, build)
	if after, err := StatusForScope(root, scope); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func refreshFreshnessRoot(ctx context.Context, root string, req FreshnessRequest, reason string, language string, mode string) *RefreshReport {
	if blocker := rootFreshnessBudgetBlocker(ctx, root, req, reason, language); blocker != nil {
		return blocker
	}
	freshnessRootRebuilds.Add(1)
	report := &RefreshReport{
		Attempted: true,
		Scope:     ".",
		Language:  language,
		Reason:    reason,
	}
	build, err := Build(ctx, BuildOptions{
		Root:      root,
		Path:      ".",
		Language:  language,
		Mode:      mode,
		Enrichers: req.Enrichers,
	})
	if err != nil {
		report.Error = err.Error()
		return report
	}
	applyBuildReportToRefresh(report, build)
	if after, err := Status(root); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func rootFreshnessBudgetBlocker(ctx context.Context, root string, req FreshnessRequest, reason string, language string) *RefreshReport {
	if req.MaxFiles <= 0 {
		return nil
	}
	count, overBudget, err := estimateRootRefreshSourceCount(ctx, root, language, req.MaxFiles)
	if err != nil {
		return &RefreshReport{
			Attempted:     false,
			Scope:         ".",
			Language:      language,
			Reason:        reason,
			SkippedReason: FreshnessReasonRefreshEstimateFailed,
			Error:         err.Error(),
		}
	}
	if !overBudget {
		return nil
	}
	return &RefreshReport{
		Attempted:        false,
		Scope:            ".",
		Language:         language,
		Reason:           FreshnessReasonBudgetExceeded,
		SkippedReason:    FreshnessReasonBudgetExceeded,
		SourceFileCount:  count,
		EstimatedMinimum: true,
		MaxFiles:         req.MaxFiles,
	}
}

func estimateRootRefreshSourceCount(ctx context.Context, root string, language string, maxFiles int) (count int, overBudget bool, err error) {
	if maxFiles <= 0 {
		return 0, false, nil
	}
	files, err := collectGenericSourceFiles(ctx, root, ".", language, maxFiles+1, map[string]struct{}{})
	if err != nil {
		return 0, false, err
	}
	return len(files), len(files) > maxFiles, nil
}

func recordFreshnessPolicy(policy FreshnessPolicy) {
	switch policy {
	case FreshnessReadThrough, FreshnessRequire:
		freshnessReadThroughAttempts.Add(1)
	case FreshnessAllowStale:
		freshnessAllowStaleInvocations.Add(1)
	}
}

func recordFreshnessOutcome(decision FreshnessDecision) {
	if decision.Policy == "" {
		return
	}
	if decision.Refresh != nil && decision.Refresh.Error != "" {
		freshnessRefreshFailures.Add(1)
	}
	if decision.FreshForScope {
		return
	}
	switch decision.Policy {
	case FreshnessReadThrough, FreshnessRequire:
	default:
		return
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "freshness_blocker"
	}
	freshnessBlockers.Add(1)
	freshnessStatsMu.Lock()
	freshnessBlockersByReason[reason]++
	freshnessStatsMu.Unlock()
}

func refreshFreshnessFileSet(ctx context.Context, root string, req FreshnessRequest, paths []string, reason string, language string, mode string) *RefreshReport {
	refreshScopes := freshnessRefreshScopes(paths, req.MaxFiles)
	report := &RefreshReport{
		Attempted:      true,
		Scope:          fmt.Sprintf("%d stale/missing file(s) across %d refresh scope(s)", len(paths), len(refreshScopes)),
		Language:       language,
		Reason:         reason,
		RefreshedFiles: append([]string(nil), paths...),
		RefreshScopes:  refreshScopes,
	}
	for _, path := range refreshScopes {
		pathLanguage := language
		if pathLanguage != "auto" && freshnessPathMissing(root, path) {
			pathLanguage = "auto"
		}
		build, err := Build(ctx, BuildOptions{
			Root:             root,
			Path:             path,
			Language:         pathLanguage,
			Mode:             mode,
			MaxFiles:         req.MaxFiles,
			ExactScope:       req.ExactScopeRefresh,
			PatchActiveStore: req.PatchActiveStore || req.ExactScopeRefresh,
			Enrichers:        req.Enrichers,
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
	if after, err := StatusForScope(root, commonFreshnessPathScope(paths)); err == nil {
		report.FreshAfter = after.Fresh
	}
	return report
}

func freshnessRefreshScopes(paths []string, maxFiles int) []string {
	paths = cleanFreshnessPaths(paths)
	if len(paths) <= 1 || maxFiles > 0 {
		return paths
	}
	byDir := make(map[string][]string)
	for _, path := range paths {
		dir := pathDirForRefresh(path)
		byDir[dir] = append(byDir[dir], path)
	}
	scopes := make([]string, 0, len(byDir))
	dirs := make([]string, 0, len(byDir))
	for dir, members := range byDir {
		if dir != "." {
			dirs = append(dirs, dir)
		}
		if dir != "." && len(members) > 1 {
			scopes = append(scopes, dir)
			continue
		}
		scopes = append(scopes, members...)
	}
	scopes = append(scopes, collapseHighFanoutFreshnessScopes(dirs)...)
	return removeCoveredFreshnessScopes(scopes)
}

func collapseHighFanoutFreshnessScopes(scopes []string) []string {
	const siblingCollapseThreshold = 8
	scopes = cleanFreshnessPaths(scopes)
	byParent := make(map[string][]string)
	for _, scope := range scopes {
		parent := pathDirForRefresh(scope)
		if parent == "." || parent == "" {
			continue
		}
		byParent[parent] = append(byParent[parent], scope)
	}
	collapsedParents := make(map[string]struct{})
	for parent, members := range byParent {
		if len(members) >= siblingCollapseThreshold {
			collapsedParents[parent] = struct{}{}
		}
	}
	if len(collapsedParents) == 0 {
		return nil
	}
	next := make([]string, 0, len(collapsedParents))
	for parent := range collapsedParents {
		next = append(next, parent)
	}
	return cleanFreshnessPaths(next)
}

func removeCoveredFreshnessScopes(scopes []string) []string {
	scopes = cleanFreshnessPaths(scopes)
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		covered := false
		for _, other := range scopes {
			if other == scope {
				continue
			}
			if pathInScope(scope, other) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out
}

func statusAfterFreshnessRefresh(root string, scope string, operation string, before StatusReport) (StatusReport, error) {
	if strings.TrimSpace(scope) != "" {
		scoped, err := StatusForScope(root, scope)
		if err != nil {
			return StatusReport{}, err
		}
		return combineStatusAfterScopedRefresh(before, scoped, scope), nil
	}
	return Status(root)
}

func combineStatusAfterScopedRefresh(before StatusReport, scoped StatusReport, scope string) StatusReport {
	if !before.Indexed {
		scoped.Fresh = false
		return scoped
	}
	combined := before
	combined.StaleFiles = replaceScopedStatusPaths(before.StaleFiles, scoped.StaleFiles, scope)
	combined.MissingFiles = replaceScopedStatusPaths(before.MissingFiles, scoped.MissingFiles, scope)
	combined.StaleSymbols = 0
	combined.StaleSymbolSamples = nil
	combined.Fresh = !combined.CollectorVersionMismatch && len(combined.StaleFiles) == 0 && len(combined.MissingFiles) == 0
	return combined
}

func replaceScopedStatusPaths(before []string, scoped []string, scope string) []string {
	seen := make(map[string]struct{}, len(before)+len(scoped))
	for _, path := range before {
		path = cleanFreshnessPath(path)
		if path == "" || pathInScope(path, scope) {
			continue
		}
		seen[path] = struct{}{}
	}
	for _, path := range scoped {
		path = cleanFreshnessPath(path)
		if path != "" {
			seen[path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func applyBuildReportToRefresh(report *RefreshReport, build BuildReport) {
	report.IndexID = build.IndexID
	report.FilesIndexed = build.FilesIndexed
	report.SymbolsIndexed = build.SymbolsIndexed
	report.EdgesIndexed = build.EdgesIndexed
	report.PartialRefreshReason = build.PartialRefreshReason
	report.FullRebuildReason = build.FullRebuildReason
	report.RefreshedFiles = build.RefreshedStaleFiles
}

func skippedFreshnessRefresh(decision FreshnessDecision, language string, blocker string) *RefreshReport {
	return &RefreshReport{
		Attempted:     false,
		Scope:         decision.Scope,
		Language:      language,
		Reason:        decision.Reason,
		SkippedReason: blocker,
	}
}

func inferFreshnessScope(root string, req FreshnessRequest) string {
	if scope := boundedFreshnessScope(req.Path); scope != "" {
		return scope
	}
	if scope := boundedFreshnessScope(pathFromFreshnessHandle(req.Handle)); scope != "" {
		return scope
	}
	paths := cleanFreshnessPaths(req.ReferencedPaths)
	if len(paths) == 1 {
		return paths[0]
	}
	if len(paths) > 1 {
		return commonFreshnessPathScope(paths)
	}
	if scope := existingQueryPathScope(root, req.Query); scope != "" {
		return scope
	}
	return ""
}

func freshnessCheckedPaths(req FreshnessRequest, scope string) []string {
	paths := cleanFreshnessPaths(req.ReferencedPaths)
	if len(paths) > 0 {
		return paths
	}
	if scope != "" {
		return []string{scope}
	}
	return nil
}

func freshnessReason(root string, status StatusReport, scope string, checkedPaths []string) string {
	if !status.Indexed {
		return FreshnessReasonMissingIndex
	}
	if status.CollectorVersionMismatch {
		return FreshnessReasonCollectorVersionMismatch
	}
	if scope != "" && len(checkedPaths) == 1 && checkedPaths[0] == scope && !freshnessGraphHasFileInScope(root, scope) {
		return FreshnessReasonScopeNotIndexed
	}
	if scope == "" && len(checkedPaths) == 0 {
		if !status.Fresh {
			return FreshnessReasonStaleUnboundedGraph
		}
		return ""
	}
	if len(freshnessRelevantPaths(status.StaleFiles, scope, checkedPaths)) > 0 || len(freshnessRelevantPaths(status.MissingFiles, scope, checkedPaths)) > 0 {
		return FreshnessReasonStaleScopeFiles
	}
	return ""
}

func freshnessScopeFresh(status StatusReport, scope string, checkedPaths []string) bool {
	if !status.Indexed || status.CollectorVersionMismatch {
		return false
	}
	if scope == "" && len(checkedPaths) == 0 {
		return status.Fresh
	}
	return len(freshnessRelevantPaths(status.StaleFiles, scope, checkedPaths)) == 0 && len(freshnessRelevantPaths(status.MissingFiles, scope, checkedPaths)) == 0
}

func freshnessRelevantPaths(paths []string, scope string, checkedPaths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, path := range paths {
		path = cleanFreshnessPath(path)
		if path == "" {
			continue
		}
		switch {
		case len(checkedPaths) > 0:
			for _, checked := range checkedPaths {
				if pathInScope(path, checked) || pathInScope(checked, path) {
					seen[path] = struct{}{}
					break
				}
			}
		case scope != "":
			if pathInScope(path, scope) {
				seen[path] = struct{}{}
			}
		default:
			seen[path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func freshnessStatusPaths(status StatusReport) []string {
	return cleanFreshnessPaths(append(append([]string(nil), status.StaleFiles...), status.MissingFiles...))
}

func freshnessBudgetExceeded(status StatusReport, decision FreshnessDecision, maxFiles int) bool {
	if maxFiles <= 0 {
		return false
	}
	pathCount := len(decision.StalePaths) + len(decision.MissingPaths)
	if decision.Scope == "" && len(decision.CheckedPaths) == 0 {
		pathCount = len(freshnessStatusPaths(status))
	}
	return pathCount > maxFiles
}

func cleanFreshnessPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if clean := cleanFreshnessPath(path); clean != "" {
			seen[clean] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func cleanFreshnessPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" || path == "." {
		return ""
	}
	return strings.TrimSuffix(path, "/")
}

func boundedFreshnessScope(path string) string {
	return cleanFreshnessPath(path)
}

func pathFromFreshnessHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	switch {
	case strings.HasPrefix(handle, "file://"):
		return strings.TrimPrefix(handle, "file://")
	case strings.HasPrefix(handle, "symbol://"):
		rest := strings.TrimPrefix(handle, "symbol://")
		if idx := strings.Index(rest, "#"); idx >= 0 {
			return rest[:idx]
		}
		return rest
	default:
		return ""
	}
}

func existingQueryPathScope(root string, query string) string {
	for _, token := range strings.FieldsFunc(query, func(r rune) bool {
		return r == '"' || r == '\'' || r == '`' || r == ',' || r == ';' || r == ':' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}'
	}) {
		scope := cleanFreshnessPath(token)
		if scope == "" || !strings.Contains(scope, "/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(scope))); err == nil {
			return scope
		}
	}
	return ""
}

func commonFreshnessPathScope(paths []string) string {
	common := ""
	for _, path := range paths {
		dir := pathDirForRefresh(path)
		if common == "" {
			common = dir
			continue
		}
		common = commonPathPrefix(common, dir)
	}
	if common == "." {
		return ""
	}
	return common
}

func freshnessGraphHasFileInScope(root string, scope string) bool {
	graph, err := LoadGraph(root)
	if err != nil {
		return false
	}
	for _, node := range graph.Nodes {
		if node.Kind == "file" && strings.TrimSpace(node.Path) != "" && pathInScope(node.Path, scope) {
			return true
		}
	}
	return false
}

func freshnessPathMissing(root string, path string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return os.IsNotExist(err)
}

func freshnessLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return "auto"
	}
	return language
}

func freshnessIndexMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick", "standard", "deep":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "standard"
	}
}

func freshnessBlocker(reason string, scope string, pathCount int, maxFiles int) string {
	switch reason {
	case FreshnessReasonBudgetExceeded:
		return FreshnessReasonBudgetExceeded
	case FreshnessReasonStaleUnboundedGraph:
		return FreshnessReasonStaleUnboundedGraph
	case FreshnessReasonCollectorVersionMismatch:
		return FreshnessReasonCollectorVersionMismatch
	case FreshnessReasonMissingIndex:
		return FreshnessReasonMissingIndex
	case FreshnessReasonScopeNotIndexed:
		return FreshnessReasonScopeNotIndexed
	default:
		if maxFiles > 0 && pathCount > maxFiles {
			return FreshnessReasonBudgetExceeded
		}
		return reason
	}
}
