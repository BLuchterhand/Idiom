package comprehension

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ViewQueryInputs captures the request shape that, together with the
// active graph index_id, determines a Pack output. Slice fields are
// canonicalized (sorted, deduplicated) before hashing so equivalent
// requests produce identical IDs.
type ViewQueryInputs struct {
	Path                string   `json:"path,omitempty"`
	Scope               string   `json:"scope,omitempty"`
	Limit               int      `json:"limit,omitempty"`
	BudgetTokens        int      `json:"budget_tokens,omitempty"`
	RequiredTests       []string `json:"required_tests,omitempty"`
	RequiredEditPaths   []string `json:"required_edit_paths,omitempty"`
	ForbiddenPaths      []string `json:"forbidden_paths,omitempty"`
	Intent              string   `json:"intent,omitempty"`
	IncludeStaleRuntime bool     `json:"include_stale_runtime,omitempty"`
	// WorkspaceStateFingerprint is the composite cache key for
	// workspace-side state that affects Pack output but is not part of
	// the request inputs. Component fingerprints keep invalidation
	// attribution separate in the result contract.
	WorkspaceStateFingerprint string `json:"workspace_state_fingerprint,omitempty"`
	RankerWeightsFingerprint  string `json:"ranker_weights_fingerprint,omitempty"`
	RuntimeTracesFingerprint  string `json:"runtime_traces_fingerprint,omitempty"`
}

// ViewKey is the content-addressed identifier for a graph_views row.
// It captures every input that could change the packed output.
type ViewKey struct {
	IndexID string
	Task    string
	Mode    string
	Query   ViewQueryInputs
}

// ID returns the deterministic 16-character hex pack_id for this key.
// Identical inputs produce identical IDs across calls and processes;
// this is the content-addressed handle for graph_views rows.
func (k ViewKey) ID() string {
	canon := canonicalQueryJSON(k.Query)
	sum := sha256.Sum256([]byte(k.IndexID + "\x00" + k.Task + "\x00" + k.Mode + "\x00" + canon))
	return hex.EncodeToString(sum[:8])
}

// canonicalQueryJSON returns a stable JSON encoding of inputs that
// affect the pack output. Slice fields are sorted-and-deduplicated so
// callers don't have to worry about ordering.
func canonicalQueryJSON(q ViewQueryInputs) string {
	q.RequiredTests = canonicalStringSlice(q.RequiredTests)
	q.RequiredEditPaths = canonicalStringSlice(q.RequiredEditPaths)
	q.ForbiddenPaths = canonicalStringSlice(q.ForbiddenPaths)
	data, _ := json.Marshal(q)
	return string(data)
}

func canonicalStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Package-level cache hit/miss counters. Process-local; persistence is
// not required since harness_audit consumes the live process state.
var (
	viewCacheHits             atomic.Int64
	viewCacheMisses           atomic.Int64
	viewCacheWrites           atomic.Int64
	viewCacheFreshnessHits    atomic.Int64
	viewCacheStalePreventions atomic.Int64
	viewCacheStaleRowMisses   atomic.Int64
)

const viewFreshnessMaxFiles = 200

type cachedViewRow struct {
	OutputJSON                string
	IndexID                   string
	Task                      string
	Mode                      string
	QueryJSON                 string
	WorkspaceStateFingerprint string
	ReferencedPathsJSON       string
	SourceFingerprintsJSON    string
	Stale                     int
}

const (
	ViewLoadStateMissing = "missing"
	ViewLoadStateFresh   = "fresh"
	ViewLoadStateStale   = "stale"
)

// ViewLoadStatus is the bounded state of a graph_views lookup by pack_id.
// It lets callers distinguish true misses from stale or non-current rows
// without reaching through the cache abstraction.
type ViewLoadStatus struct {
	State          string
	Reason         string
	PackID         string
	RowIndexID     string
	CurrentIndexID string
}

// ViewCacheCounters returns the current process-local hit/miss/write
// counts for the graph_views cache. Used by harness_audit.
func ViewCacheCounters() (hits, misses, writes int64) {
	return viewCacheHits.Load(), viewCacheMisses.Load(), viewCacheWrites.Load()
}

func ViewFreshnessCounters() (freshnessHits, stalePreventions, staleRowMisses int64) {
	return viewCacheFreshnessHits.Load(), viewCacheStalePreventions.Load(), viewCacheStaleRowMisses.Load()
}

// ResetViewCacheCounters zeroes the counters. Test-only helper.
func ResetViewCacheCounters() {
	viewCacheHits.Store(0)
	viewCacheMisses.Store(0)
	viewCacheWrites.Store(0)
	viewCacheFreshnessHits.Store(0)
	viewCacheStalePreventions.Store(0)
	viewCacheStaleRowMisses.Store(0)
}

// LoadView returns the cached PackResult for the given key, or
// (PackResult{}, false, nil) on cache miss. Cache misses are not
// errors; only genuine I/O or decode failures return an error.
func LoadView(root string, key ViewKey) (PackResult, bool, error) {
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			viewCacheMisses.Add(1)
			return PackResult{}, false, nil
		}
		return PackResult{}, false, err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return PackResult{}, false, err
	}
	defer db.Close()
	if err := ensureGraphViewsTable(db); err != nil {
		return PackResult{}, false, err
	}
	row, err := loadCachedViewRow(db, key.ID())
	if errors.Is(err, sql.ErrNoRows) {
		recordStaleRowMiss(db, key.ID())
		viewCacheMisses.Add(1)
		return PackResult{}, false, nil
	}
	if err != nil {
		return PackResult{}, false, err
	}
	var result PackResult
	if err := json.Unmarshal([]byte(row.OutputJSON), &result); err != nil {
		return PackResult{}, false, fmt.Errorf("decode cached view %s: %w", key.ID(), err)
	}
	result, fresh, err := validateCachedViewFreshness(root, row, result, "load_view")
	if err != nil {
		return PackResult{}, false, err
	}
	if !fresh {
		viewCacheStalePreventions.Add(1)
		viewCacheMisses.Add(1)
		return PackResult{}, false, nil
	}
	viewCacheFreshnessHits.Add(1)
	viewCacheHits.Add(1)
	result.Cache = GraphViewCacheStateForKey(key, firstNonEmpty(result.StoreKind, result.Provenance.StoreKind), "hit", "fresh_cached_row")
	AttachPackResultContract(&result)
	return result, true, nil
}

// LoadViewByID fetches a cached PackResult by its content-addressed
// pack_id. Unlike LoadView, this skips key reconstruction — it's the
// executor entry point for rehydrating a planner's packet from the
// PlanArtifact.PackID field. Returns (PackResult{}, false, nil) on
// cache miss; non-nil error only on genuine I/O / decode failure.
func LoadViewByID(root string, packID string) (PackResult, bool, error) {
	result, status, err := LoadViewByIDStatus(root, packID)
	if err != nil {
		return PackResult{}, false, err
	}
	if status.State != ViewLoadStateFresh {
		return PackResult{}, false, nil
	}
	return result, true, nil
}

// LoadViewByIDStatus fetches a cached PackResult by pack_id and reports
// whether the graph_views row is fresh, stale, or absent. Stale rows are
// returned with Fresh=false so audit/phase handoff surfaces can report
// stale evidence without relying on legacy JSON view files.
func LoadViewByIDStatus(root string, packID string) (PackResult, ViewLoadStatus, error) {
	packID = strings.TrimSpace(packID)
	status := ViewLoadStatus{
		State:  ViewLoadStateMissing,
		Reason: "cache_miss",
		PackID: packID,
	}
	if packID == "" {
		status.Reason = "missing_packet_id"
		return PackResult{}, status, nil
	}
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			viewCacheMisses.Add(1)
			return PackResult{}, status, nil
		}
		return PackResult{}, status, err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return PackResult{}, status, err
	}
	defer db.Close()
	if err := ensureGraphViewsTable(db); err != nil {
		return PackResult{}, status, err
	}
	row, err := loadCachedViewRowAnyState(db, packID)
	if errors.Is(err, sql.ErrNoRows) {
		viewCacheMisses.Add(1)
		return PackResult{}, status, nil
	}
	if err != nil {
		return PackResult{}, status, err
	}
	var result PackResult
	if err := json.Unmarshal([]byte(row.OutputJSON), &result); err != nil {
		return PackResult{}, status, fmt.Errorf("decode cached view %s: %w", packID, err)
	}
	status.RowIndexID = firstNonEmpty(row.IndexID, result.IndexID)
	status.CurrentIndexID = cachedViewCurrentIndexID(root)
	if row.Stale != 0 {
		viewCacheStaleRowMisses.Add(1)
		viewCacheStalePreventions.Add(1)
		viewCacheMisses.Add(1)
		status.State = ViewLoadStateStale
		status.Reason = "stale_row"
		return staleCachedViewResult(result, status), status, nil
	}
	result, fresh, reason, err := validateCachedViewFreshnessStatus(root, row, result, "load_view_by_id")
	if err != nil {
		return PackResult{}, status, err
	}
	if !fresh {
		viewCacheStalePreventions.Add(1)
		viewCacheMisses.Add(1)
		status.State = ViewLoadStateStale
		status.Reason = cachedViewStaleReason(reason)
		return staleCachedViewResult(result, status), status, nil
	}
	viewCacheFreshnessHits.Add(1)
	viewCacheHits.Add(1)
	key := cachedViewKeyFromRow(row, packID)
	result.Cache = GraphViewCacheStateForKey(key, firstNonEmpty(result.StoreKind, result.Provenance.StoreKind), "hit", "fresh_cached_row")
	result.Cache.CacheKey = packID
	result.Cache.PackID = packID
	AttachPackResultContract(&result)
	status.State = ViewLoadStateFresh
	status.Reason = "fresh_cached_row"
	return result, status, nil
}

// SaveView inserts or replaces the graph_views row for the given key.
// The fingerprint column captures the content state (index_id plus
// referenced paths) so scoped refresh can mark stale by referenced-path
// comparison.
func SaveView(root string, key ViewKey, result PackResult) error {
	if err := saveViewOnce(root, key, result); err != nil {
		if !isSQLiteCorruptionError(err) {
			return err
		}
		if recoverErr := recoverSQLiteGraphStore(root); recoverErr != nil {
			return fmt.Errorf("recover SQLite graph store after graph_views write failure: %w (original error: %v)", recoverErr, err)
		}
		if retryErr := saveViewOnce(root, key, result); retryErr != nil {
			return fmt.Errorf("retry graph_views write after SQLite graph recovery: %w (original error: %v)", retryErr, err)
		}
	}
	viewCacheWrites.Add(1)
	return nil
}

func saveViewOnce(root string, key ViewKey, result PackResult) error {
	sqlitePath := sqliteGraphPath(root)
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := validateSQLiteIntegrity(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := createSQLiteSchema(tx); err != nil {
		return err
	}
	queryJSON := canonicalQueryJSON(key.Query)
	outputJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	references := referencedPaths(result)
	referencesJSON, err := json.Marshal(references)
	if err != nil {
		return err
	}
	sourceFingerprintsJSON, err := json.Marshal(sourceFingerprintsForPaths(root, references))
	if err != nil {
		return err
	}
	fingerprint := viewFingerprint(result)
	rootNodeID := strings.TrimSpace(key.Query.Path)
	resolution := strings.TrimSpace(key.Mode)
	if resolution == "" {
		resolution = "plan"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`INSERT INTO graph_views (
		id, root_node_id, resolution, query_json, output_json,
		fingerprint, stale, created_at, index_id, task, mode,
		workspace_state_fingerprint, referenced_paths_json, source_fingerprints_json
	) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		root_node_id = excluded.root_node_id,
		resolution   = excluded.resolution,
		query_json   = excluded.query_json,
		output_json  = excluded.output_json,
		fingerprint  = excluded.fingerprint,
		stale        = 0,
		created_at   = excluded.created_at,
		index_id     = excluded.index_id,
		task         = excluded.task,
		mode         = excluded.mode,
		workspace_state_fingerprint = excluded.workspace_state_fingerprint,
		referenced_paths_json       = excluded.referenced_paths_json,
		source_fingerprints_json    = excluded.source_fingerprints_json`,
		key.ID(),
		rootNodeID,
		resolution,
		queryJSON,
		string(outputJSON),
		fingerprint,
		now,
		key.IndexID,
		key.Task,
		key.Mode,
		key.Query.WorkspaceStateFingerprint,
		string(referencesJSON),
		string(sourceFingerprintsJSON),
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// viewFingerprint hashes the content state the view was generated
// against: the index_id plus the sorted set of repo-relative paths the
// pack output references.
func viewFingerprint(result PackResult) string {
	sum := sha256.New()
	sum.Write([]byte(result.IndexID))
	sum.Write([]byte("\x00"))
	for _, p := range referencedPaths(result) {
		sum.Write([]byte(p))
		sum.Write([]byte("\x00"))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// referencedPaths returns the sorted unique set of repo-relative paths
// the pack output mentions across candidate files, candidate symbols,
// tests, and evidence. Used by scoped invalidation.
func referencedPaths(result PackResult) []string {
	seen := make(map[string]struct{})
	add := func(p string) {
		p = sourceReferencePath(p)
		if p == "" {
			return
		}
		seen[p] = struct{}{}
	}
	for _, f := range result.CandidateFiles {
		add(f.Path)
	}
	for _, s := range result.CandidateSymbols {
		add(s.Path)
	}
	for _, t := range result.Tests {
		add(t.Path)
	}
	for _, e := range result.Evidence {
		switch e.Kind {
		case "context_file", "context_symbol", "runtime_anchor", "runtime_file":
			add(e.Path)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sourceReferencePath(path string) string {
	path = cleanFreshnessPath(path)
	if path == "" {
		return ""
	}
	for _, prefix := range []string{".idiom/", ".git/"} {
		if strings.HasPrefix(path, prefix) {
			return ""
		}
	}
	return path
}

func sourceFingerprintsForPaths(root string, paths []string) map[string]string {
	graph, err := LoadGraph(root)
	if err != nil {
		return map[string]string{}
	}
	fingerprints := fingerprintsByPath(graph.Nodes)
	out := make(map[string]string)
	for _, path := range cleanFreshnessPaths(paths) {
		if fingerprint := strings.TrimSpace(fingerprints[path]); fingerprint != "" {
			out[path] = fingerprint
		}
	}
	return out
}

func loadCachedViewRow(db *sql.DB, id string) (cachedViewRow, error) {
	var row cachedViewRow
	err := db.QueryRow(
		`SELECT output_json,
			COALESCE(index_id, ''),
			COALESCE(task, ''),
			COALESCE(mode, ''),
			COALESCE(query_json, ''),
			COALESCE(workspace_state_fingerprint, ''),
			COALESCE(referenced_paths_json, ''),
			COALESCE(source_fingerprints_json, ''),
			COALESCE(stale, 0)
		FROM graph_views WHERE id = ? AND stale = 0`,
		id,
	).Scan(
		&row.OutputJSON,
		&row.IndexID,
		&row.Task,
		&row.Mode,
		&row.QueryJSON,
		&row.WorkspaceStateFingerprint,
		&row.ReferencedPathsJSON,
		&row.SourceFingerprintsJSON,
		&row.Stale,
	)
	return row, err
}

func loadCachedViewRowAnyState(db *sql.DB, id string) (cachedViewRow, error) {
	var row cachedViewRow
	err := db.QueryRow(
		`SELECT output_json,
			COALESCE(index_id, ''),
			COALESCE(task, ''),
			COALESCE(mode, ''),
			COALESCE(query_json, ''),
			COALESCE(workspace_state_fingerprint, ''),
			COALESCE(referenced_paths_json, ''),
			COALESCE(source_fingerprints_json, ''),
			COALESCE(stale, 0)
		FROM graph_views WHERE id = ?`,
		id,
	).Scan(
		&row.OutputJSON,
		&row.IndexID,
		&row.Task,
		&row.Mode,
		&row.QueryJSON,
		&row.WorkspaceStateFingerprint,
		&row.ReferencedPathsJSON,
		&row.SourceFingerprintsJSON,
		&row.Stale,
	)
	return row, err
}

func cachedViewKeyFromRow(row cachedViewRow, packID string) ViewKey {
	var query ViewQueryInputs
	if strings.TrimSpace(row.QueryJSON) != "" {
		_ = json.Unmarshal([]byte(row.QueryJSON), &query)
	}
	if query.WorkspaceStateFingerprint == "" {
		query.WorkspaceStateFingerprint = row.WorkspaceStateFingerprint
	}
	key := ViewKey{
		IndexID: strings.TrimSpace(row.IndexID),
		Task:    strings.TrimSpace(row.Task),
		Mode:    strings.TrimSpace(row.Mode),
		Query:   query,
	}
	if key.ID() == strings.TrimSpace(packID) {
		return key
	}
	return key
}

func recordStaleRowMiss(db *sql.DB, id string) {
	var stale int
	if err := db.QueryRow(`SELECT stale FROM graph_views WHERE id = ?`, id).Scan(&stale); err == nil && stale != 0 {
		viewCacheStaleRowMisses.Add(1)
	}
}

func validateCachedViewFreshness(root string, row cachedViewRow, result PackResult, operation string) (PackResult, bool, error) {
	result, fresh, _, err := validateCachedViewFreshnessStatus(root, row, result, operation)
	return result, fresh, err
}

func validateCachedViewFreshnessStatus(root string, row cachedViewRow, result PackResult, operation string) (PackResult, bool, string, error) {
	references := cachedViewReferencedPaths(row, result)
	decision, err := EnsureFresh(context.Background(), FreshnessRequest{
		Root:              root,
		Operation:         operation,
		Policy:            FreshnessRequire,
		ReferencedPaths:   references,
		Mode:              firstNonEmpty(result.Mode, row.Mode),
		MaxFiles:          viewFreshnessMaxFiles,
		ExactScopeRefresh: true,
	})
	if err != nil {
		return PackResult{}, false, "", err
	}
	if !decision.FreshForScope {
		return result, false, firstNonEmpty(decision.Reason, decision.Blocker, FreshnessReasonStaleGraph), nil
	}
	if decision.Refresh != nil && decision.Refresh.Attempted {
		return result, false, "refreshed_referenced_paths", nil
	}
	if !cachedViewIndexMatchesCurrent(root, row, result) {
		return result, false, "non_current_index", nil
	}
	if !cachedViewFingerprintsMatch(root, row) {
		return result, false, "source_fingerprint_mismatch", nil
	}
	result.Freshness = &decision
	result.AutoRefresh = decision.Refresh
	result.Confidence = GraphResultConfidenceForFreshness(result.Fresh, &decision)
	result.Provenance = graphResultProvenanceFromFreshness(operation, decision, result.ArtifactPath)
	return result, true, "", nil
}

func cachedViewReferencedPaths(row cachedViewRow, result PackResult) []string {
	var references []string
	if strings.TrimSpace(row.ReferencedPathsJSON) != "" && json.Unmarshal([]byte(row.ReferencedPathsJSON), &references) == nil {
		references = filterSourceReferencePaths(references)
		if len(references) > 0 {
			return references
		}
	}
	return referencedPaths(result)
}

func filterSourceReferencePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path = sourceReferencePath(path); path != "" {
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

func cachedViewIndexMatchesCurrent(root string, row cachedViewRow, result PackResult) bool {
	rowIndexID := firstNonEmpty(row.IndexID, result.IndexID)
	if rowIndexID == "" {
		return false
	}
	return rowIndexID == cachedViewCurrentIndexID(root)
}

func cachedViewCurrentIndexID(root string) string {
	status, err := Status(root)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(status.IndexID)
}

func cachedViewFingerprintsMatch(root string, row cachedViewRow) bool {
	stored := cachedViewSourceFingerprints(row)
	if len(stored) == 0 {
		return true
	}
	current := sourceFingerprintsForPaths(root, mapKeys(stored))
	for path, fingerprint := range stored {
		if current[path] != fingerprint {
			return false
		}
	}
	return true
}

func staleCachedViewResult(result PackResult, status ViewLoadStatus) PackResult {
	reason := cachedViewStaleReason(status.Reason)
	result.PackID = firstNonEmpty(strings.TrimSpace(result.PackID), strings.TrimSpace(status.PackID))
	result.IndexID = firstNonEmpty(strings.TrimSpace(result.IndexID), strings.TrimSpace(status.RowIndexID))
	result.Fresh = false
	result.Confidence = GraphResultConfidence{Level: "low", Source: "graph_view_cache", Reason: reason}
	result.StaleEvidence = StaleEvidenceSafety{
		State:         "blocked",
		Policy:        FreshnessRequire,
		Reason:        reason,
		FreshForScope: false,
		GlobalFresh:   false,
	}
	result.Cache = GraphViewCacheState{
		State:     "stale",
		Source:    "graph_views",
		Reason:    reason,
		CacheKey:  strings.TrimSpace(status.PackID),
		PackID:    strings.TrimSpace(status.PackID),
		StoreKind: firstNonEmpty(strings.TrimSpace(result.StoreKind), strings.TrimSpace(result.Provenance.StoreKind)),
		Invalidation: GraphViewInvalidationSemantics{
			Policy:          "content_addressed_read_through",
			State:           "stale",
			ValidatesOnLoad: []string{"current_index_id", "referenced_source_fingerprints", "scoped_freshness"},
			StaleResult:     "cache_miss_recompute",
		},
	}
	AttachPackResultContract(&result)
	return result
}

func cachedViewStaleReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "stale_row",
		"non_current_index",
		"source_fingerprint_mismatch",
		"refreshed_referenced_paths",
		FreshnessReasonBudgetExceeded,
		FreshnessReasonCollectorVersionMismatch,
		FreshnessReasonMissingIndex,
		FreshnessReasonMissingScope,
		FreshnessReasonReferencedPathsUnverified,
		FreshnessReasonScopeNotIndexed,
		FreshnessReasonStaleGraph,
		FreshnessReasonStaleScopeFiles,
		FreshnessReasonStaleUnboundedGraph:
		return strings.TrimSpace(reason)
	default:
		return "stale_graph_view"
	}
}

func cachedViewSourceFingerprints(row cachedViewRow) map[string]string {
	if strings.TrimSpace(row.SourceFingerprintsJSON) == "" {
		return nil
	}
	var stored map[string]string
	if err := json.Unmarshal([]byte(row.SourceFingerprintsJSON), &stored); err != nil {
		return nil
	}
	out := make(map[string]string, len(stored))
	for path, fingerprint := range stored {
		path = sourceReferencePath(path)
		fingerprint = strings.TrimSpace(fingerprint)
		if path != "" && fingerprint != "" {
			out[path] = fingerprint
		}
	}
	return out
}

func mapKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// MarkViewsStale sets stale = 1 on every graph_views row whose output
// references one or more of the given paths. Used by scoped refresh so
// views aren't blanket-staled when only a subset of the graph changed.
// Returns the count of newly-stale rows.
func MarkViewsStale(root string, paths []string) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := ensureGraphViewsTable(db); err != nil {
		return 0, err
	}
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		pathSet[p] = struct{}{}
	}
	if len(pathSet) == 0 {
		return 0, nil
	}
	rows, err := db.Query(`SELECT id, output_json, COALESCE(referenced_paths_json, '') FROM graph_views WHERE stale = 0`)
	if err != nil {
		return 0, err
	}
	var staleIDs []string
	for rows.Next() {
		var id, output, referencesJSON string
		if err := rows.Scan(&id, &output, &referencesJSON); err != nil {
			rows.Close()
			return 0, err
		}
		references, ok := viewRowReferences(output, referencesJSON)
		if !ok {
			staleIDs = append(staleIDs, id)
			continue
		}
		for _, ref := range references {
			if _, hit := pathSet[ref]; hit {
				staleIDs = append(staleIDs, id)
				break
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(staleIDs) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stmt, err := tx.Prepare(`UPDATE graph_views SET stale = 1 WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, id := range staleIDs {
		if _, err := stmt.Exec(id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return len(staleIDs), nil
}

func viewRowReferences(outputJSON string, referencesJSON string) ([]string, bool) {
	var references []string
	if strings.TrimSpace(referencesJSON) != "" && json.Unmarshal([]byte(referencesJSON), &references) == nil {
		return filterSourceReferencePaths(references), true
	}
	var pr PackResult
	if err := json.Unmarshal([]byte(outputJSON), &pr); err != nil {
		return nil, false
	}
	return referencedPaths(pr), true
}

// MarkAllViewsStale flips every fresh row to stale. Used by full graph
// rebuilds where the entire content state has shifted underneath every
// cached view.
func MarkAllViewsStale(root string) (int, error) {
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := ensureGraphViewsTable(db); err != nil {
		return 0, err
	}
	res, err := db.Exec(`UPDATE graph_views SET stale = 1 WHERE stale = 0`)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// ViewStats aggregates graph_views table state for harness_audit.
type ViewStats struct {
	Rows                    int    `json:"rows"`
	Fresh                   int    `json:"fresh"`
	Stale                   int    `json:"stale"`
	NonCurrent              int    `json:"non_current"`
	CurrentIndexID          string `json:"current_index_id,omitempty"`
	BytesStored             int64  `json:"bytes_stored"`
	OldestCreated           string `json:"oldest_created,omitempty"`
	NewestCreated           string `json:"newest_created,omitempty"`
	WorkspaceStateKeyedRows int    `json:"workspace_state_keyed_rows"`
	ContractRows            int    `json:"contract_rows"`
	UnresolvedGapRows       int    `json:"unresolved_gap_rows"`
	Hits                    int64  `json:"cache_hits"`
	Misses                  int64  `json:"cache_misses"`
	Writes                  int64  `json:"cache_writes"`
	FreshnessHits           int64  `json:"freshness_hits"`
	StalePreventions        int64  `json:"stale_preventions"`
	StaleRowMisses          int64  `json:"stale_row_misses"`
}

// ReadViewStats returns aggregate counters over the graph_views table
// plus the in-process hit/miss counters. Returns zero ViewStats and no
// error when the SQLite file is absent.
func ReadViewStats(root string) (ViewStats, error) {
	hits, misses, writes := ViewCacheCounters()
	freshnessHits, stalePreventions, staleRowMisses := ViewFreshnessCounters()
	stats := ViewStats{
		Hits:             hits,
		Misses:           misses,
		Writes:           writes,
		FreshnessHits:    freshnessHits,
		StalePreventions: stalePreventions,
		StaleRowMisses:   staleRowMisses,
	}
	sqlitePath := sqliteGraphPath(root)
	if _, err := os.Stat(sqlitePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return stats, nil
		}
		return stats, err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return stats, err
	}
	defer db.Close()
	if err := ensureGraphViewsTable(db); err != nil {
		return stats, err
	}
	if meta, err := readSQLiteGraphMetadata(db); err == nil {
		stats.CurrentIndexID = strings.TrimSpace(meta["index_id"])
	}
	var oldest, newest sql.NullString
	err = db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN stale = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN stale = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(LENGTH(output_json)), 0),
		MIN(created_at),
		MAX(created_at),
		COALESCE(SUM(CASE WHEN COALESCE(workspace_state_fingerprint, '') <> '' THEN 1 ELSE 0 END), 0)
		FROM graph_views`).Scan(&stats.Rows, &stats.Fresh, &stats.Stale, &stats.BytesStored, &oldest, &newest, &stats.WorkspaceStateKeyedRows)
	if err != nil {
		return stats, err
	}
	if oldest.Valid {
		stats.OldestCreated = oldest.String
	}
	if newest.Valid {
		stats.NewestCreated = newest.String
	}
	if stats.CurrentIndexID != "" {
		if err := db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN COALESCE(index_id, '') <> ? THEN 1 ELSE 0 END), 0) FROM graph_views`, stats.CurrentIndexID).Scan(&stats.NonCurrent); err != nil {
			return stats, err
		}
	}
	contractRows, unresolvedGapRows, err := readViewContractStats(db)
	if err != nil {
		return stats, err
	}
	stats.ContractRows = contractRows
	stats.UnresolvedGapRows = unresolvedGapRows
	return stats, nil
}

func readViewContractStats(db *sql.DB) (contractRows int, unresolvedGapRows int, err error) {
	rows, err := db.Query(`SELECT output_json FROM graph_views`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var output string
		if err := rows.Scan(&output); err != nil {
			return 0, 0, err
		}
		var decoded struct {
			Contract             *GraphViewResultContract `json:"contract"`
			UnresolvedGapDetails []GraphUnresolvedGap     `json:"unresolved_gap_details"`
		}
		if err := json.Unmarshal([]byte(output), &decoded); err != nil {
			continue
		}
		if decoded.Contract != nil && decoded.Contract.SchemaVersion == GraphViewResultContractVersion {
			contractRows++
		}
		if len(decoded.UnresolvedGapDetails) > 0 {
			unresolvedGapRows++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return contractRows, unresolvedGapRows, nil
}

// ensureGraphViewsTable creates the graph_views table on a freshly-opened
// connection if it doesn't already exist. Lets the cache layer be useful
// even when no `context_index` build has ever run.
func ensureGraphViewsTable(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS graph_views (
		id TEXT PRIMARY KEY,
		root_node_id TEXT,
		resolution TEXT NOT NULL,
		query_json TEXT NOT NULL,
		output_json TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		stale INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		index_id TEXT,
		task TEXT,
		mode TEXT,
		workspace_state_fingerprint TEXT,
		referenced_paths_json TEXT,
		source_fingerprints_json TEXT
	)`); err != nil {
		return err
	}
	return ensureGraphViewsColumns(db)
}

type graphViewsSchemaTarget interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

func ensureGraphViewsColumns(target graphViewsSchemaTarget) error {
	rows, err := target.Query(`PRAGMA table_info(graph_views)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	existing := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{name: "index_id", sql: `ALTER TABLE graph_views ADD COLUMN index_id TEXT`},
		{name: "task", sql: `ALTER TABLE graph_views ADD COLUMN task TEXT`},
		{name: "mode", sql: `ALTER TABLE graph_views ADD COLUMN mode TEXT`},
		{name: "workspace_state_fingerprint", sql: `ALTER TABLE graph_views ADD COLUMN workspace_state_fingerprint TEXT`},
		{name: "referenced_paths_json", sql: `ALTER TABLE graph_views ADD COLUMN referenced_paths_json TEXT`},
		{name: "source_fingerprints_json", sql: `ALTER TABLE graph_views ADD COLUMN source_fingerprints_json TEXT`},
	} {
		if _, ok := existing[column.name]; ok {
			continue
		}
		if _, err := target.Exec(column.sql); err != nil {
			return err
		}
	}
	return nil
}

// ChangedPathsBetween returns the repo-relative paths whose file
// fingerprint differs between two graphs (or that exist in one but not
// the other). Used by scoped refresh to compute the invalidation set
// for graph_views.
func ChangedPathsBetween(previous Graph, current Graph) []string {
	prev := fingerprintsByPath(previous.Nodes)
	curr := fingerprintsByPath(current.Nodes)
	seen := make(map[string]struct{})
	for path, fp := range curr {
		if prev[path] != fp {
			seen[path] = struct{}{}
		}
	}
	for path := range prev {
		if _, stillThere := curr[path]; !stillThere {
			seen[path] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

type ComprehensionStateFingerprintSet struct {
	WorkspaceStateFingerprint string
	RankerWeightsFingerprint  string
	RuntimeTracesFingerprint  string
}

// ComprehensionStateFingerprint returns the composite cache fingerprint
// for workspace-side state that influences Pack output but is not part
// of the request inputs. Component fingerprints are available through
// ComprehensionStateFingerprints.
func ComprehensionStateFingerprint(root string) string {
	return ComprehensionStateFingerprints(root).WorkspaceStateFingerprint
}

func ComprehensionStateFingerprints(root string) ComprehensionStateFingerprintSet {
	ranker := rankerWeightsStateFingerprint(root)
	runtime := runtimeTracesStateFingerprint(root)
	return comprehensionStateFingerprintSet(ranker, runtime)
}

func comprehensionStateFingerprintSet(rankerFingerprint string, runtimeFingerprint string) ComprehensionStateFingerprintSet {
	rankerFingerprint = strings.TrimSpace(rankerFingerprint)
	runtimeFingerprint = strings.TrimSpace(runtimeFingerprint)
	return ComprehensionStateFingerprintSet{
		WorkspaceStateFingerprint: composeWorkspaceStateFingerprint(rankerFingerprint, runtimeFingerprint),
		RankerWeightsFingerprint:  rankerFingerprint,
		RuntimeTracesFingerprint:  runtimeFingerprint,
	}
}

func composeWorkspaceStateFingerprint(rankerFingerprint string, runtimeFingerprint string) string {
	rankerFingerprint = strings.TrimSpace(rankerFingerprint)
	runtimeFingerprint = strings.TrimSpace(runtimeFingerprint)
	if rankerFingerprint == "" && runtimeFingerprint == "" {
		return ""
	}
	h := sha256.New()
	if rankerFingerprint != "" {
		h.Write([]byte("ranker_weights\x00"))
		h.Write([]byte(rankerFingerprint))
		h.Write([]byte("\x00"))
	}
	if runtimeFingerprint != "" {
		h.Write([]byte("runtime_traces\x00"))
		h.Write([]byte(runtimeFingerprint))
		h.Write([]byte("\x00"))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func rankerWeightsStateFingerprint(root string) string {
	weightsPath := filepath.Join(root, ".idiom", "ranker", "weights.json")
	if data, err := os.ReadFile(weightsPath); err == nil {
		return shortSHA256Hex(data)
	}
	return ""
}

func shortSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func fingerprintsByPath(nodes []Node) map[string]string {
	out := make(map[string]string)
	for _, node := range nodes {
		if node.Kind != "file" || node.Path == "" {
			continue
		}
		out[node.Path] = node.Fingerprint
	}
	return out
}
