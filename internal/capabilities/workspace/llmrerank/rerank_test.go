package llmrerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testDocs(n int) []Doc {
	docs := make([]Doc, n)
	for i := range docs {
		docs[i] = Doc{Path: "f" + string(rune('a'+i)) + ".go", Text: "f" + string(rune('a'+i)) + ".go - purpose"}
	}

	return docs
}

// crossServer scores candidate i with scores[i].
func crossServer(t *testing.T, scores []float64, calls *atomic.Int32) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		type res struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		}

		out := make([]res, len(scores))
		for i, s := range scores {
			out[i] = res{i, s}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"results": out, "usage": map[string]int{"prompt_tokens": 100}})
	}))
}

func cfgFor(url string, backends ...string) Config {
	return Config{Mode: ModeOn, Backends: backends, PoolSize: 30, CrossURL: url, CrossModel: "m", EmbedURL: url, EmbedModel: "e", ChatURL: url, ChatModel: "c", Timeout: 2 * time.Second}
}

func TestCrossEncoderReordersAndCaches(t *testing.T) {
	var calls atomic.Int32

	srv := crossServer(t, []float64{0.1, 0.9, 0.5}, &calls)
	defer srv.Close()

	cfg := cfgFor(srv.URL, BackendCross)
	cfg.CacheDir = t.TempDir()
	cfg.LogPath = filepath.Join(t.TempDir(), "log.jsonl")

	first := Run(context.Background(), cfg, "q", testDocs(3))
	if !first.Reranked() || !slices.Equal(first.Order, []int{1, 2, 0}) {
		t.Fatalf("first = %+v", first)
	}

	srv.Close() // a cache hit must not need the server

	second := Run(context.Background(), cfg, "q", testDocs(3))
	if !second.Cached || !slices.Equal(second.Order, first.Order) || calls.Load() != 1 {
		t.Fatalf("second = %+v, calls %d", second, calls.Load())
	}

	log, _ := os.ReadFile(cfg.LogPath)
	if strings.Contains(string(log), "\"q\"") || strings.Count(string(log), "\n") != 2 {
		t.Fatalf("log should hold two rows and no query: %s", log)
	}
}

func TestEmbedderFusedWithCrossByRRF(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rerank", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
			{"index": 0, "relevance_score": 0.2}, {"index": 1, "relevance_score": 0.9}, {"index": 2, "relevance_score": 0.5}}})
	})
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		// query, then candidates 0..2: embed order is 2, 0, 1
		vecs := [][]float64{{1, 0}, {1, 1}, {0, 1}, {1, 0.1}}

		data := make([]map[string]any, len(vecs))
		for i, v := range vecs {
			data[i] = map[string]any{"index": i, "embedding": v}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := Run(context.Background(), cfgFor(srv.URL, BackendCross, BackendEmbed), "q", testDocs(3))
	// cross order 1,2,0 and embed order 2,0,1: candidate 2 is top-two in both and wins.
	if !got.Reranked() || got.Order[0] != 2 {
		t.Fatalf("fused = %+v", got)
	}
}

func TestFallbackWhenBackendDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	got := Run(context.Background(), cfgFor(url, BackendCross), "q", testDocs(4))
	if got.Reranked() || !strings.HasPrefix(got.Status, "off: ") || !slices.Equal(got.Order, []int{0, 1, 2, 3}) {
		t.Fatalf("down server = %+v", got)
	}
}

func TestFallbackOnTimeoutAndErrors(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()

	cfg := cfgFor(slow.URL, BackendCross)
	cfg.Timeout = 100 * time.Millisecond

	started := time.Now()
	if got := Run(context.Background(), cfg, "q", testDocs(3)); got.Reranked() || time.Since(started) > time.Second {
		t.Fatalf("timeout = %+v after %v", got, time.Since(started))
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
	defer broken.Close()

	if got := Run(context.Background(), cfgFor(broken.URL, BackendCross), "q", testDocs(3)); got.Reranked() {
		t.Fatalf("500 = %+v", got)
	}

	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer garbage.Close()

	if got := Run(context.Background(), cfgFor(garbage.URL, BackendCross), "q", testDocs(3)); got.Reranked() {
		t.Fatalf("garbage = %+v", got)
	}
}

func TestDegenerateScoresFallBack(t *testing.T) {
	var calls atomic.Int32

	srv := crossServer(t, []float64{0, 0, 0}, &calls)
	defer srv.Close()

	got := Run(context.Background(), cfgFor(srv.URL, BackendCross), "q", testDocs(3))
	if got.Reranked() || !strings.Contains(got.Status, "degenerate") {
		t.Fatalf("flat scores = %+v", got)
	}
}

func TestChatListwiseParsesAndFallsBack(t *testing.T) {
	reply := "<think>hmm [9]</think>\nSure: [3, 1]"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": reply}}},
			"usage":   map[string]int{"prompt_tokens": 50, "completion_tokens": 5}})
	}))
	defer srv.Close()

	got := Run(context.Background(), cfgFor(srv.URL, BackendChat), "q", testDocs(4))
	if !got.Reranked() || !slices.Equal(got.Order, []int{2, 0, 1, 3}) {
		t.Fatalf("chat = %+v", got)
	}

	reply = "I cannot rank these."

	if got := Run(context.Background(), cfgFor(srv.URL, BackendChat), "q", testDocs(4)); got.Reranked() || !slices.Equal(got.Order, []int{0, 1, 2, 3}) {
		t.Fatalf("parse failure = %+v", got)
	}
}

func TestAnthropicBackendSendsKeyOnlyInHeader(t *testing.T) {
	var gotKey, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		buf := new(strings.Builder)

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		data, _ := json.Marshal(body)
		buf.Write(data)
		gotBody = buf.String()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "[2,1]"}},
			"usage":   map[string]int{"input_tokens": 400, "output_tokens": 6}})
	}))
	defer srv.Close()

	keyFile := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(keyFile, []byte("sk-test-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := cfgFor("", BackendAnthropic)
	cfg.AnthropicURL, cfg.AnthropicMod, cfg.KeyFile = srv.URL, "claude-haiku-5-5", keyFile
	cfg.LogPath = filepath.Join(t.TempDir(), "log.jsonl")

	got := Run(context.Background(), cfg, "q", testDocs(2))
	if !got.Reranked() || !slices.Equal(got.Order, []int{1, 0}) || gotKey != "sk-test-secret" {
		t.Fatalf("anthropic = %+v key %q", got, gotKey)
	}

	log, _ := os.ReadFile(cfg.LogPath)
	if strings.Contains(gotBody, "sk-test-secret") || strings.Contains(string(log), "sk-test-secret") {
		t.Fatal("key leaked into body or log")
	}

	cfg.KeyFile = filepath.Join(t.TempDir(), "missing")
	if got := Run(context.Background(), cfg, "q", testDocs(2)); got.Reranked() || strings.Contains(got.Status, "missing") {
		t.Fatalf("missing key = %+v", got)
	}
}

func TestDryRunCallsNothingAndLogsEstimates(t *testing.T) {
	var calls atomic.Int32

	srv := crossServer(t, []float64{1, 2}, &calls)
	defer srv.Close()

	cfg := cfgFor(srv.URL, BackendCross, BackendChat)
	cfg.Mode = ModeDry
	cfg.LogPath = filepath.Join(t.TempDir(), "log.jsonl")

	got := Run(context.Background(), cfg, "q", testDocs(2))
	if got.Reranked() || calls.Load() != 0 || got.Status != "off: dry run" {
		t.Fatalf("dry = %+v calls %d", got, calls.Load())
	}

	log, _ := os.ReadFile(cfg.LogPath)
	if strings.Count(string(log), "est_in_tokens") != 2 {
		t.Fatalf("dry log = %s", log)
	}
}

func TestRemoteHostRefusedUnlessAllowed(t *testing.T) {
	cfg := cfgFor("http://example.com:8181", BackendCross)
	if got := Run(context.Background(), cfg, "q", testDocs(2)); got.Reranked() || !strings.Contains(got.Status, "loopback or tailnet") {
		t.Fatalf("remote = %+v", got)
	}
}

func TestOffByDefault(t *testing.T) {
	cfg := FromEnv(func(string) string { return "" })
	if cfg.Enabled() {
		t.Fatal("enabled by default")
	}

	if got := Run(context.Background(), cfg, "q", testDocs(3)); got.Status != "" || len(got.Order) != 3 {
		t.Fatalf("off = %+v", got)
	}
}

func TestParseListwise(t *testing.T) {
	cases := []struct {
		text string
		want []int
		ok   bool
	}{
		{"[2,1,3]", []int{1, 0, 2}, true},
		{"```json\n[3]\n```", []int{2, 0, 1}, true},
		{"[7, 2, 2]", []int{1, 0, 2}, true}, // out-of-range and repeats dropped
		{"[]", nil, false},
		{"none", nil, false},
	}

	for _, c := range cases {
		got, err := parseListwise(c.text, 3)
		if (err == nil) != c.ok || (c.ok && !slices.Equal(got, c.want)) {
			t.Errorf("%q -> %v, %v", c.text, got, err)
		}
	}
}

func TestLoadDocsFromCodeMap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "engineering")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	codeMap := "# Code map\n\n## core/sim\n- tick.cpp (10) Runs the tick\n- tick.h (5) Tick API | :3 class Tick, :9 fn run_tick\n- bare.cpp (4)\n"
	if err := os.WriteFile(filepath.Join(dir, "code-map.md"), []byte(codeMap), 0o644); err != nil {
		t.Fatal(err)
	}

	docs := LoadDocs(root, defaultCodeMapPath, []string{"core/sim/tick.h", "core/sim/bare.cpp", "other/x.go"})
	want := []string{"core/sim/tick.h - Tick API [Tick, run_tick]", "core/sim/bare.cpp", "other/x.go"}

	for i, w := range want {
		if docs[i].Text != w {
			t.Errorf("doc %d = %q, want %q", i, docs[i].Text, w)
		}
	}
}

func TestPrivateHostRule(t *testing.T) {
	for host, ok := range map[string]bool{
		"http://127.0.0.1:1": true, "http://localhost:1": true, "http://[::1]:1": true,
		"http://100.64.0.1:1": true, "http://100.127.255.254:1": true,
		"http://100.128.0.1:1": false, "http://8.8.8.8:1": false, "http://example.com": false,
	} {
		if err := checkPrivateHost(host, false); (err == nil) != ok {
			t.Errorf("%s: err %v, want ok=%v", host, err, ok)
		}
	}
}
