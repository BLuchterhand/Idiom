package comprehension

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ViewQueryInputs captures the request shape that, together with the
// active graph index_id, determines a Pack output. Slice fields are
// canonicalized (sorted, deduplicated) before hashing so equivalent
// requests produce identical IDs.
type ViewQueryInputs struct {
	Path           string   `json:"path,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	BudgetTokens   int      `json:"budget_tokens,omitempty"`
	RequiredTests  []string `json:"required_tests,omitempty"`
	ForbiddenPaths []string `json:"forbidden_paths,omitempty"`
	Intent         string   `json:"intent,omitempty"`
	// WorkspaceStateFingerprint captures workspace-side state that
	// affects Pack output but is not part of the request inputs:
	// the .idiom/ranker/weights.json file (ranker multipliers) and
	// the .idiom/comprehension/traces/ directory (runtime overlay).
	// Computed by ComprehensionStateFingerprint at Pack time so a
	// weight bump or new trace artifact busts the cache without
	// waiting for an index rebuild.
	WorkspaceStateFingerprint string `json:"workspace_state_fingerprint,omitempty"`
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
	viewCacheHits   atomic.Int64
	viewCacheMisses atomic.Int64
	viewCacheWrites atomic.Int64
)

// ViewCacheCounters returns the current process-local hit/miss/write
// counts for the graph_views cache. Used by harness_audit.
func ViewCacheCounters() (hits, misses, writes int64) {
	return viewCacheHits.Load(), viewCacheMisses.Load(), viewCacheWrites.Load()
}

// ResetViewCacheCounters zeroes the counters. Test-only helper.
func ResetViewCacheCounters() {
	viewCacheHits.Store(0)
	viewCacheMisses.Store(0)
	viewCacheWrites.Store(0)
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
	var outputJSON string
	err = db.QueryRow(
		`SELECT output_json FROM graph_views WHERE id = ? AND stale = 0`,
		key.ID(),
	).Scan(&outputJSON)
	if errors.Is(err, sql.ErrNoRows) {
		viewCacheMisses.Add(1)
		return PackResult{}, false, nil
	}
	if err != nil {
		return PackResult{}, false, err
	}
	var result PackResult
	if err := json.Unmarshal([]byte(outputJSON), &result); err != nil {
		return PackResult{}, false, fmt.Errorf("decode cached view %s: %w", key.ID(), err)
	}
	viewCacheHits.Add(1)
	return result, true, nil
}

// LoadViewByID fetches a cached PackResult by its content-addressed
// pack_id. Unlike LoadView, this skips key reconstruction — it's the
// executor entry point for rehydrating a planner's packet from the
// PlanArtifact.PackID field. Returns (PackResult{}, false, nil) on
// cache miss; non-nil error only on genuine I/O / decode failure.
func LoadViewByID(root string, packID string) (PackResult, bool, error) {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return PackResult{}, false, nil
	}
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
	var outputJSON string
	err = db.QueryRow(
		`SELECT output_json FROM graph_views WHERE id = ? AND stale = 0`,
		packID,
	).Scan(&outputJSON)
	if errors.Is(err, sql.ErrNoRows) {
		viewCacheMisses.Add(1)
		return PackResult{}, false, nil
	}
	if err != nil {
		return PackResult{}, false, err
	}
	var result PackResult
	if err := json.Unmarshal([]byte(outputJSON), &result); err != nil {
		return PackResult{}, false, fmt.Errorf("decode cached view %s: %w", packID, err)
	}
	viewCacheHits.Add(1)
	return result, true, nil
}

// SaveView inserts or replaces the graph_views row for the given key.
// The fingerprint column captures the content state (index_id plus
// referenced paths) so scoped refresh can mark stale by referenced-path
// comparison.
func SaveView(root string, key ViewKey, result PackResult) error {
	sqlitePath := sqliteGraphPath(root)
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return err
	}
	db, err := openSQLite(sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()
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
	fingerprint := viewFingerprint(result)
	rootNodeID := strings.TrimSpace(key.Query.Path)
	resolution := strings.TrimSpace(key.Mode)
	if resolution == "" {
		resolution = "plan"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`INSERT INTO graph_views (
		id, root_node_id, resolution, query_json, output_json,
		fingerprint, stale, created_at
	) VALUES (?, ?, ?, ?, ?, ?, 0, ?)
	ON CONFLICT(id) DO UPDATE SET
		root_node_id = excluded.root_node_id,
		resolution   = excluded.resolution,
		query_json   = excluded.query_json,
		output_json  = excluded.output_json,
		fingerprint  = excluded.fingerprint,
		stale        = 0,
		created_at   = excluded.created_at`,
		key.ID(),
		rootNodeID,
		resolution,
		queryJSON,
		string(outputJSON),
		fingerprint,
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	viewCacheWrites.Add(1)
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
		p = strings.TrimSpace(p)
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
		add(e.Path)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
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
	rows, err := db.Query(`SELECT id, output_json FROM graph_views WHERE stale = 0`)
	if err != nil {
		return 0, err
	}
	var staleIDs []string
	for rows.Next() {
		var id, output string
		if err := rows.Scan(&id, &output); err != nil {
			rows.Close()
			return 0, err
		}
		var pr PackResult
		if err := json.Unmarshal([]byte(output), &pr); err != nil {
			// Undecodable rows can't be served; treat as stale.
			staleIDs = append(staleIDs, id)
			continue
		}
		for _, ref := range referencedPaths(pr) {
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
	Rows          int    `json:"rows"`
	Fresh         int    `json:"fresh"`
	Stale         int    `json:"stale"`
	BytesStored   int64  `json:"bytes_stored"`
	OldestCreated string `json:"oldest_created,omitempty"`
	NewestCreated string `json:"newest_created,omitempty"`
	Hits          int64  `json:"cache_hits"`
	Misses        int64  `json:"cache_misses"`
	Writes        int64  `json:"cache_writes"`
}

// ReadViewStats returns aggregate counters over the graph_views table
// plus the in-process hit/miss counters. Returns zero ViewStats and no
// error when the SQLite file is absent.
func ReadViewStats(root string) (ViewStats, error) {
	hits, misses, writes := ViewCacheCounters()
	stats := ViewStats{Hits: hits, Misses: misses, Writes: writes}
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
	var oldest, newest sql.NullString
	err = db.QueryRow(`SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN stale = 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN stale = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(LENGTH(output_json)), 0),
		MIN(created_at),
		MAX(created_at)
		FROM graph_views`).Scan(&stats.Rows, &stats.Fresh, &stats.Stale, &stats.BytesStored, &oldest, &newest)
	if err != nil {
		return stats, err
	}
	if oldest.Valid {
		stats.OldestCreated = oldest.String
	}
	if newest.Valid {
		stats.NewestCreated = newest.String
	}
	return stats, nil
}

// ensureGraphViewsTable creates the graph_views table on a freshly-opened
// connection if it doesn't already exist. Lets the cache layer be useful
// even when no `context_index` build has ever run.
func ensureGraphViewsTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS graph_views (
		id TEXT PRIMARY KEY,
		root_node_id TEXT,
		resolution TEXT NOT NULL,
		query_json TEXT NOT NULL,
		output_json TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		stale INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	)`)
	return err
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

// ComprehensionStateFingerprint returns a short hex fingerprint over
// workspace-side state that influences Pack output but isn't part of
// the request inputs: the ranker weights file and the runtime traces
// directory. Cheap to compute (one os.Stat per item, no content read).
// Returned empty string when neither item exists; that's the baseline
// state and produces a stable empty fingerprint.
func ComprehensionStateFingerprint(root string) string {
	weightsPath := filepath.Join(root, ".idiom", "ranker", "weights.json")
	tracesDir := filepath.Join(root, ".idiom", "comprehension", "traces")
	h := sha256.New()
	if info, err := os.Stat(weightsPath); err == nil {
		h.Write([]byte("weights\x00"))
		h.Write([]byte(info.ModTime().UTC().Format(time.RFC3339Nano)))
		h.Write([]byte("\x00"))
		h.Write([]byte(strconv.FormatInt(info.Size(), 10)))
		h.Write([]byte("\x00"))
	}
	if info, err := os.Stat(tracesDir); err == nil {
		h.Write([]byte("traces_dir\x00"))
		h.Write([]byte(info.ModTime().UTC().Format(time.RFC3339Nano)))
		h.Write([]byte("\x00"))
	}
	if entries, err := os.ReadDir(tracesDir); err == nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte("\x00"))
	}
}
sum := h.Sum(nil)
if len(sum) == 0 {
	return ""
}
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
