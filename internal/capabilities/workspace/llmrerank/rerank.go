package llmrerank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"idiom/internal/runtime/runner"
)

const (
	// promptVersion changes when a prompt or the candidate text format changes,
	// so cached orders from the old format are not reused.
	promptVersion = "g885-v1"

	StatusOn        = "on"
	statusOffPrefix = "off: "
	cacheDirPerm    = 0o755
	cacheFilePerm   = 0o644
)

// Outcome is what Run decides. Order is always a permutation of the
// candidates: Idiom's own order when Status starts with "off".
type Outcome struct {
	Order  []int
	Status string
	Cached bool
}

// Reranked reports whether Order came from the scorers.
func (o Outcome) Reranked() bool { return o.Status == StatusOn }

type cacheRecord struct {
	Order     []int `json:"order"`
	LatencyMS int64 `json:"latency_ms"`
	InTokens  int   `json:"in_tokens"`
	OutTokens int   `json:"out_tokens"`
}

// logRecord is one line of the usage log. It carries no query or file text.
type logRecord struct {
	Key       string `json:"key"`
	Backends  string `json:"backends"`
	Status    string `json:"status"`
	Cached    bool   `json:"cached"`
	N         int    `json:"n"`
	LatencyMS int64  `json:"latency_ms"`
	InTokens  int    `json:"in_tokens"`
	OutTokens int    `json:"out_tokens"`
	EstIn     int    `json:"est_in_tokens,omitempty"`
	EstOut    int    `json:"est_out_tokens,omitempty"`
}

func identity(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}

	return order
}

func off(n int, reason string) Outcome {
	return Outcome{Order: identity(n), Status: statusOffPrefix + reason}
}

// Run reorders docs for query. It never fails: every error becomes Idiom's
// order with an "off: reason" status. The cache is keyed by query, candidates,
// backends and models, so a repeated eval costs nothing.
func Run(ctx context.Context, cfg Config, query string, docs []Doc) Outcome {
	n := len(docs)
	if !cfg.Enabled() || n < 2 {
		return Outcome{Order: identity(n)}
	}

	backends, err := buildBackends(cfg)
	if err != nil {
		return off(n, err.Error())
	}

	key := cacheKey(backends, cfg, query, docs)

	if cfg.Mode == ModeDry {
		return dryRun(cfg, backends, key, query, docs)
	}

	if rec, ok := readCache(cfg, key, n); ok {
		appendLog(cfg, logRecord{Key: key[:12], Backends: names(backends), Status: StatusOn, Cached: true, N: n, LatencyMS: rec.LatencyMS, InTokens: rec.InTokens, OutTokens: rec.OutTokens})

		return Outcome{Order: rec.Order, Status: StatusOn, Cached: true}
	}

	ctx, cancel := context.WithTimeout(ctx, timeoutFor(cfg, backends))
	defer cancel()

	started := time.Now()
	ranked, err := callBackends(ctx, cfg, backends, query, docs)
	latency := time.Since(started).Milliseconds()

	if err != nil {
		appendLog(cfg, logRecord{Key: key[:12], Backends: names(backends), Status: statusOffPrefix + err.Error(), N: n, LatencyMS: latency})

		return off(n, err.Error())
	}

	rec := cacheRecord{Order: fuse(ranked, n), LatencyMS: latency}
	for _, r := range ranked {
		rec.InTokens += r.InTokens
		rec.OutTokens += r.OutTokens
	}

	writeCache(cfg, key, rec)
	appendLog(cfg, logRecord{Key: key[:12], Backends: names(backends), Status: StatusOn, N: n, LatencyMS: latency, InTokens: rec.InTokens, OutTokens: rec.OutTokens})

	return Outcome{Order: rec.Order, Status: StatusOn}
}

func fuse(ranked []Ranked, n int) []int {
	if len(ranked) == 1 {
		return ranked[0].Order
	}

	orders := make([][]int, len(ranked))
	for i, r := range ranked {
		orders[i] = r.Order
	}

	return fuseRRF(orders, n)
}

// callBackends runs every backend concurrently inside one queue lease. Any
// failure fails the whole rerank, so an arm never silently runs on fewer
// channels than it names.
func callBackends(ctx context.Context, cfg Config, backends []Backend, query string, docs []Doc) ([]Ranked, error) {
	if cfg.Pool != "" {
		lease := runner.MaybeAcquire(ctx, cfg.QueueSocket, cfg.Pool, "llm-rerank", "idiom-rerank", nil)
		defer lease.Release()
	}

	ranked := make([]Ranked, len(backends))
	errs := make([]error, len(backends))

	var wg sync.WaitGroup

	for i, b := range backends {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ranked[i], errs[i] = b.Rank(ctx, query, docs)
		}()
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", backends[i].Name(), err)
		}

		if len(ranked[i].Order) != len(docs) {
			return nil, fmt.Errorf("%s: short order", backends[i].Name())
		}
	}

	return ranked, nil
}

func dryRun(cfg Config, backends []Backend, key, query string, docs []Doc) Outcome {
	for _, b := range backends {
		in, out := b.Estimate(query, docs)
		appendLog(cfg, logRecord{Key: key[:12], Backends: b.Name(), Status: "dry", N: len(docs), EstIn: in, EstOut: out})
	}

	return off(len(docs), "dry run")
}

func buildBackends(cfg Config) ([]Backend, error) {
	var out []Backend

	for _, name := range cfg.Backends {
		switch name {
		case BackendCross:
			out = append(out, crossBackend{cfg.CrossURL, cfg.CrossModel})
		case BackendEmbed:
			out = append(out, embedBackend{cfg.EmbedURL, cfg.EmbedModel})
		case BackendChat:
			out = append(out, chatBackend{cfg.ChatURL, cfg.ChatModel})
		case BackendAnthropic:
			out = append(out, anthropicBackend{cfg.AnthropicURL, cfg.AnthropicMod, cfg.KeyFile})
		default:
			return nil, fmt.Errorf("unknown backend %q", name)
		}
	}

	for _, b := range out {
		if err := checkBackendHost(cfg, b); err != nil {
			return nil, fmt.Errorf("%s: %w", b.Name(), err)
		}
	}

	return out, nil
}

// checkBackendHost keeps every backend but Anthropic on loopback.
func checkBackendHost(cfg Config, b Backend) error {
	switch v := b.(type) {
	case crossBackend:
		return checkPrivateHost(v.url, cfg.AllowRemote)
	case embedBackend:
		return checkPrivateHost(v.url, cfg.AllowRemote)
	case chatBackend:
		return checkPrivateHost(v.url, cfg.AllowRemote)
	}

	return nil
}

func timeoutFor(cfg Config, backends []Backend) time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}

	timeout := defaultLocalTimeout

	for _, b := range backends {
		if b.Name() == BackendChat || b.Name() == BackendAnthropic {
			timeout = defaultChatTimeout
		}
	}

	return timeout
}

func names(backends []Backend) string {
	s := ""

	for i, b := range backends {
		if i > 0 {
			s += "+"
		}

		s += b.Name()
	}

	return s
}

func cacheKey(backends []Backend, cfg Config, query string, docs []Doc) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00", promptVersion, cfg.PoolSize, query)

	for _, b := range backends {
		fmt.Fprintf(h, "%s=%s\x00", b.Name(), modelOf(cfg, b))
	}

	for _, d := range docs {
		fmt.Fprintf(h, "%s\x00%s\x00", d.Path, d.Text)
	}

	return hex.EncodeToString(h.Sum(nil))
}

func modelOf(cfg Config, b Backend) string {
	switch b.Name() {
	case BackendCross:
		return cfg.CrossModel
	case BackendEmbed:
		return cfg.EmbedModel
	case BackendChat:
		return cfg.ChatModel
	}

	return cfg.AnthropicMod
}

func cachePath(cfg Config, key string) string {
	return filepath.Join(cfg.CacheDir, key[:2], key+".json")
}

func readCache(cfg Config, key string, n int) (cacheRecord, bool) {
	if cfg.CacheDir == "" {
		return cacheRecord{}, false
	}

	data, err := os.ReadFile(cachePath(cfg, key))
	if err != nil {
		return cacheRecord{}, false
	}

	var rec cacheRecord
	if json.Unmarshal(data, &rec) != nil || len(rec.Order) != n {
		return cacheRecord{}, false
	}

	if order, ok := completeOrder(rec.Order, n); !ok || len(order) != n {
		return cacheRecord{}, false
	}

	return rec, true
}

func writeCache(cfg Config, key string, rec cacheRecord) {
	if cfg.CacheDir == "" {
		return
	}

	path := cachePath(cfg, key)
	if os.MkdirAll(filepath.Dir(path), cacheDirPerm) != nil {
		return
	}

	data, _ := json.Marshal(rec)
	_ = os.WriteFile(path, data, cacheFilePerm)
}

func appendLog(cfg Config, rec logRecord) {
	if cfg.LogPath == "" {
		return
	}

	data, _ := json.Marshal(rec)

	f, err := os.OpenFile(cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, cacheFilePerm)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.Write(append(data, '\n'))
}
