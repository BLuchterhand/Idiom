package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	localizationEvalSchema      = 1
	localizationEvalProdPath    = "."
	defaultLocalizationTimedRun = 3
	defaultIndexTimeout         = 40 * time.Minute
	defaultQueryTimeout         = 5 * time.Minute
	localizationEvalCacheFiles  = "graph.sqlite graph.sqlite.meta.json manifest.json"
	// localizationEvalRankedKept bounds the stored ranked list per case.
	localizationEvalRankedKept = 24
)

// LocalizationEvalRunOptions configures one exact-mode run.
type LocalizationEvalRunOptions struct {
	Repo          string
	IdiomBin      string
	CacheDir      string
	CacheMaxBytes int64
	Subset        string // "dev", "holdout" or "all"
	OnlyIDs       []string
	TimedCalls    int
	// NoSeed forces a cold index per case (the gating default: cold builds take
	// about 10 s here). A seeded index (incremental update of the nearest cached
	// tree) matched a cold one in the top 8 but differed at rank 13 in a probe,
	// so seeding is opt-in.
	NoSeed       bool
	IndexTimeout time.Duration
	QueryTimeout time.Duration
	Log          io.Writer
}

// LocalizationEvalCaseResult is the deterministic part of one case result plus
// the (non-gated) timing and provenance fields.
type LocalizationEvalCaseResult struct {
	ID            string                      `json:"id"`
	Split         string                      `json:"split"`
	Ranked        []string                    `json:"ranked"`
	Metrics8      LocalizationEvalCaseMetrics `json:"metrics_k8"`
	Metrics5      LocalizationEvalCaseMetrics `json:"metrics_k5"`
	Deterministic bool                        `json:"deterministic"`
	Fresh         bool                        `json:"index_fresh"`
	Error         string                      `json:"error,omitempty"`
	OutputBytes   int                         `json:"output_bytes"`
	LatencyMS     []int64                     `json:"latency_ms,omitempty"`
	IndexMS       int64                       `json:"index_ms,omitempty"`
	IndexSource   string                      `json:"index_source,omitempty"`
}

// LocalizationEvalRunResult is the output file of a run.
type LocalizationEvalRunResult struct {
	Schema           int                          `json:"schema"`
	Corpus           string                       `json:"corpus"`
	Mode             string                       `json:"mode"`
	CollectorVersion string                       `json:"collector_version"`
	Limit            string                       `json:"limit"`
	Cases            []LocalizationEvalCaseResult `json:"cases"`
	Summary          []LocalizationEvalSummary    `json:"summary"`
}

// RunLocalizationEval runs every selected case in exact mode.
func RunLocalizationEval(ctx context.Context, file LocalizationEvalCaseFile, opts LocalizationEvalRunOptions) (LocalizationEvalRunResult, error) {
	if opts.TimedCalls <= 0 {
		opts.TimedCalls = defaultLocalizationTimedRun
	}

	if opts.IndexTimeout <= 0 {
		opts.IndexTimeout = defaultIndexTimeout
	}

	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = defaultQueryTimeout
	}

	if opts.Log == nil {
		opts.Log = io.Discard
	}

	r := &localizationEvalRunner{file: file, opts: opts}
	result := LocalizationEvalRunResult{Schema: localizationEvalSchema, Corpus: file.Corpus, Mode: "exact", Limit: "production default (limit 8, pool 32 nodes)"}

	cases := selectLocalizationEvalCases(file.Cases, opts)
	for i, c := range cases {
		fmt.Fprintf(opts.Log, "[%d/%d] %s %s\n", i+1, len(cases), c.ID, c.Split)

		cr := r.runCase(ctx, c)
		if cr.Error != "" {
			fmt.Fprintf(opts.Log, "  error: %s\n", cr.Error)
		}

		result.Cases = append(result.Cases, cr)

		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}

	result.CollectorVersion = r.collector
	result.Summary = SummarizeLocalizationEval(result.Cases)

	return result, nil
}

func selectLocalizationEvalCases(all []LocalizationEvalCase, opts LocalizationEvalRunOptions) []LocalizationEvalCase {
	var out []LocalizationEvalCase

	for _, c := range all {
		if opts.Subset != "" && opts.Subset != "all" && c.Split != opts.Subset {
			continue
		}

		if len(opts.OnlyIDs) > 0 && !slices.Contains(opts.OnlyIDs, c.ID) {
			continue
		}

		out = append(out, c)
	}

	return out
}

type localizationEvalRunner struct {
	file      LocalizationEvalCaseFile
	opts      LocalizationEvalRunOptions
	collector string
}

func (r *localizationEvalRunner) runCase(ctx context.Context, c LocalizationEvalCase) LocalizationEvalCaseResult {
	cr := LocalizationEvalCaseResult{ID: c.ID, Split: c.Split, Deterministic: true}

	wt, err := os.MkdirTemp("", "idiom-loc-eval-")
	if err != nil {
		cr.Error = err.Error()
		return cr
	}

	defer os.RemoveAll(wt)

	if _, err := gitOut(r.opts.Repo, "worktree", "add", "--detach", "--force", wt, c.Base); err != nil {
		cr.Error = err.Error()
		return cr
	}

	defer func() { _, _ = gitOut(r.opts.Repo, "worktree", "remove", "--force", wt) }()

	if err := r.writeLocalizationConfig(wt); err != nil {
		cr.Error = err.Error()
		return cr
	}

	started := time.Now()
	if cr.IndexSource, err = r.prepareIndex(ctx, wt, c); err != nil {
		cr.Error = err.Error()
		return cr
	}

	cr.IndexMS = time.Since(started).Milliseconds()

	var first []string

	for call := 0; call <= r.opts.TimedCalls; call++ { // call 0 is the warm-up
		t0 := time.Now()

		files, bytesOut, fresh, err := r.query(ctx, wt, c.Query)
		if err != nil {
			cr.Error = err.Error()
			return cr
		}

		if call > 0 {
			cr.LatencyMS = append(cr.LatencyMS, time.Since(t0).Milliseconds())
		}

		if call == 0 {
			first, cr.OutputBytes, cr.Fresh = files, bytesOut, fresh
		} else if !slices.Equal(first, files) {
			cr.Deterministic = false
		}
	}

	cr.Ranked = first[:min(len(first), localizationEvalRankedKept)]
	cr.Metrics8, _ = ComputeLocalizationEvalMetrics(first, c.Gold, LocalizationEvalGatedK)
	cr.Metrics5, _ = ComputeLocalizationEvalMetrics(first, c.Gold, LocalizationEvalSecondaryK)

	return cr
}

func (r *localizationEvalRunner) writeLocalizationConfig(wt string) error {
	if r.file.Localization.Fingerprint() == "" {
		return nil
	}

	data, err := json.MarshalIndent(map[string]any{"localization": r.file.Localization}, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(wt, ".idiom", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}

// prepareIndex puts an index of the case's base tree in wt/.idiom/comprehension:
// from the cache when this exact tree and collector version are cached, else
// seeded from the nearest cached tree and refreshed incrementally, else cold.
func (r *localizationEvalRunner) prepareIndex(ctx context.Context, wt string, c LocalizationEvalCase) (string, error) {
	if r.collector == "" {
		version, err := r.collectorVersion(ctx, wt)
		if err != nil {
			return "", err
		}

		r.collector = version
	}

	tree, err := gitOut(r.opts.Repo, "rev-parse", c.Base+"^{tree}")
	if err != nil {
		return "", err
	}

	tree = strings.TrimSpace(tree)
	dest := filepath.Join(wt, ".idiom", "comprehension")
	entry := r.cacheEntry(tree)

	if _, err := os.Stat(entry); err == nil {
		touch(entry)
		return "cache", copyIndexFiles(entry, dest)
	}

	source := "cold"

	if near, ok := r.nearestCacheTree(tree); ok && !r.opts.NoSeed {
		if err := copyIndexFiles(r.cacheEntry(near), dest); err == nil {
			source = "seeded:" + near[:12]
		}
	}

	if _, err := r.runIdiom(ctx, wt, r.opts.IndexTimeout, "context_index", `{"path":".","mode":"standard"}`); err != nil {
		return source, fmt.Errorf("index: %w", err)
	}

	if err := copyIndexFiles(dest, entry); err != nil {
		return source, err
	}

	r.evictCache()

	return source, nil
}

func (r *localizationEvalRunner) collectorVersion(ctx context.Context, wt string) (string, error) {
	out, err := r.runIdiom(ctx, wt, r.opts.QueryTimeout, "context_status", `{"limit":1}`)
	if err != nil {
		return "", err
	}

	var status struct {
		CurrentCollectorVersion string `json:"current_collector_version"`
	}

	if err := json.Unmarshal([]byte(out), &status); err != nil || status.CurrentCollectorVersion == "" {
		return "", fmt.Errorf("context_status gave no collector version")
	}

	return status.CurrentCollectorVersion, nil
}

func (r *localizationEvalRunner) query(ctx context.Context, wt, task string) ([]string, int, bool, error) {
	input, _ := json.Marshal(map[string]any{"path": localizationEvalProdPath, "task": task})

	out, err := r.runIdiom(ctx, wt, r.opts.QueryTimeout, "context_impact", string(input))
	if err != nil {
		return nil, 0, false, err
	}

	var parsed struct {
		Fresh       bool `json:"fresh"`
		ImpactScope []struct {
			Path string `json:"path"`
		} `json:"impact_scope"`
	}

	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, 0, false, fmt.Errorf("context_impact output: %w", err)
	}

	seen := map[string]struct{}{}

	var files []string

	for _, item := range parsed.ImpactScope {
		if _, ok := seen[item.Path]; ok || item.Path == "" {
			continue
		}

		seen[item.Path] = struct{}{}

		files = append(files, item.Path)
	}

	return files, len(out), parsed.Fresh, nil
}

// runIdiom runs `idiom tool <tool> --input <json>` in dir and returns the
// tool's Output string. Process kill is by context only (exec guarantees a
// real positive pid).
func (r *localizationEvalRunner) runIdiom(ctx context.Context, dir string, timeout time.Duration, tool, input string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.opts.IdiomBin, "tool", tool, "--input", input)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("idiom tool %s: %w: %s", tool, err, truncate(stderr.String(), 300))
	}

	var envelope struct {
		Output string `json:"Output"`
		Error  string `json:"Error"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return "", fmt.Errorf("idiom tool %s: bad envelope: %w", tool, err)
	}

	if envelope.Error != "" {
		return "", fmt.Errorf("idiom tool %s: %s", tool, truncate(envelope.Error, 300))
	}

	return envelope.Output, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}

	return s
}

func (r *localizationEvalRunner) cacheEntry(tree string) string {
	key := LocalizationEvalTreeKey(tree, r.collector, r.file.Localization.Fingerprint())

	return filepath.Join(r.opts.CacheDir, r.collectorDir(), tree[:12]+"-"+key)
}

func (r *localizationEvalRunner) collectorDir() string {
	return LocalizationEvalTreeKey(r.collector, r.file.Localization.Fingerprint())
}

// nearestCacheTree returns the cached tree (same collector and config) with the
// fewest differing files to tree.
func (r *localizationEvalRunner) nearestCacheTree(tree string) (string, bool) {
	entries, _ := os.ReadDir(filepath.Join(r.opts.CacheDir, r.collectorDir()))
	best, bestDiff := "", -1

	for _, e := range entries {
		candidate, _, _ := strings.Cut(e.Name(), "-")
		full, err := r.resolveTree(candidate)

		if err != nil {
			continue
		}

		diff, err := gitOut(r.opts.Repo, "diff", "--name-only", full, tree)
		if err != nil {
			continue
		}

		if n := strings.Count(diff, "\n"); bestDiff < 0 || n < bestDiff {
			best, bestDiff = full, n
		}
	}

	return best, bestDiff >= 0
}

func (r *localizationEvalRunner) resolveTree(prefix string) (string, error) {
	out, err := gitOut(r.opts.Repo, "rev-parse", "--verify", prefix+"^{tree}")

	return strings.TrimSpace(out), err
}

func (r *localizationEvalRunner) evictCache() {
	if r.opts.CacheMaxBytes <= 0 {
		return
	}

	type entry struct {
		path string
		mod  time.Time
		size int64
	}

	var all []entry

	var total int64

	dirs, _ := os.ReadDir(r.opts.CacheDir)
	for _, d := range dirs {
		subs, _ := os.ReadDir(filepath.Join(r.opts.CacheDir, d.Name()))
		for _, s := range subs {
			p := filepath.Join(r.opts.CacheDir, d.Name(), s.Name())
			size, mod := dirSize(p)
			all = append(all, entry{p, mod, size})
			total += size
		}
	}

	slices.SortFunc(all, func(a, b entry) int { return a.mod.Compare(b.mod) })

	for _, e := range all {
		if total <= r.opts.CacheMaxBytes {
			return
		}

		if os.RemoveAll(e.path) == nil {
			total -= e.size
		}
	}
}

func dirSize(path string) (int64, time.Time) {
	var size int64

	var mod time.Time

	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		if info, err := d.Info(); err == nil {
			size += info.Size()
			if info.ModTime().After(mod) {
				mod = info.ModTime()
			}
		}

		return nil
	})

	return size, mod
}

func touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func copyIndexFiles(from, to string) error {
	if err := os.MkdirAll(to, 0o755); err != nil {
		return err
	}

	for _, name := range strings.Fields(localizationEvalCacheFiles) {
		data, err := os.ReadFile(filepath.Join(from, name))
		if err != nil {
			if os.IsNotExist(err) && name == "manifest.json" {
				continue
			}

			return err
		}

		if err := os.WriteFile(filepath.Join(to, name), data, 0o644); err != nil {
			return err
		}
	}

	return nil
}
