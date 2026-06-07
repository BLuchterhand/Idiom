package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sampleProfile builds a two-model profile with a configured proxy
// block — the canonical shape `idiom backend proxy` is built around.
// Tests reach for this when they want a representative routing
// table without retyping fields.
func sampleProfile(reasonerPort, coderPort, listen int) Profile {
	return Profile{
		Models: []Model{
			{Alias: "qwen3.6-35b", Binary: "mlx_lm.server", ModelPath: "/abs/qwen3.6-35b", Port: reasonerPort},
			{Alias: "qwen3-coder-30b", Binary: "mlx_lm.server", ModelPath: "/abs/qwen3-coder-30b", Port: coderPort},
		},
		Proxy: Proxy{Listen: listen, DefaultAlias: "qwen3.6-35b"},
	}
}

// TestBuildProxyConfigAcceptsValidProfile exercises the canonical
// happy path: a complete profile resolves into a routing table,
// preserves the configured listen/default values, and points each
// alias at 127.0.0.1:<port> with the absolute model path.
func TestBuildProxyConfigAcceptsValidProfile(t *testing.T) {
	cfg, err := BuildProxyConfig(sampleProfile(47780, 47781, 47680))
	if err != nil {
		t.Fatalf("BuildProxyConfig: %v", err)
	}
	if cfg.Listen != 47680 || cfg.DefaultAlias != "qwen3.6-35b" {
		t.Fatalf("listen/default mis-derived: %+v", cfg)
	}
	r, ok := cfg.Routes["qwen3-coder-30b"]
	if !ok {
		t.Fatalf("missing coder route in %v", cfg.Routes)
	}
	if r.BackendAddr != "127.0.0.1:47781" || r.AbsPath != "/abs/qwen3-coder-30b" {
		t.Fatalf("coder route shape wrong: %+v", r)
	}
}

// TestBuildProxyConfigRejectsEmptyModelPath makes the contract
// explicit: a model without model_path can't be proxied because
// there's nothing to rewrite the alias to. Silent dropping would
// surface as a confusing 404 at request time, so the configuration
// error has to be loud at startup.
func TestBuildProxyConfigRejectsEmptyModelPath(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "x", Binary: "x", Port: 1}}}
	_, err := BuildProxyConfig(prof)
	if err == nil || !strings.Contains(err.Error(), "model_path") {
		t.Fatalf("expected model_path error, got %v", err)
	}
}

// TestBuildProxyConfigRejectsUnknownDefaultAlias guards the proxy
// against a misconfigured default that names an alias the schema
// doesn't define. Without this check, every request without a
// `model` field would 404 — surfacing as "the proxy is broken"
// rather than "the schema is wrong."
func TestBuildProxyConfigRejectsUnknownDefaultAlias(t *testing.T) {
	prof := sampleProfile(1, 2, 3)
	prof.Proxy.DefaultAlias = "no-such-alias"
	_, err := BuildProxyConfig(prof)
	if err == nil || !strings.Contains(err.Error(), "default_alias") {
		t.Fatalf("expected default_alias error, got %v", err)
	}
}

// TestBuildProxyConfigRejectsInvalidPort closes the matching gap
// in BuildLaunchSpec's validation suite: a model with port <= 0
// can't be reached, so the proxy must reject it at config-build
// time rather than 502 every request later.
func TestBuildProxyConfigRejectsInvalidPort(t *testing.T) {
	prof := Profile{Models: []Model{{Alias: "x", Binary: "x", ModelPath: "/m", Port: 0}}}
	_, err := BuildProxyConfig(prof)
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("expected invalid-port error, got %v", err)
	}
}

// TestProxyMalformedJSONOnV1FallsBackToDefault verifies the
// graceful-degradation path called out in the Phase 4c review
// coverage notes: a /v1/ request with a non-JSON body still routes
// to the default backend instead of 400'ing. Matches the Python
// proxy's behavior; without the fallback, a typo'd curl invocation
// would surface as "the proxy is broken" rather than the operator
// realizing they sent bad JSON.
func TestProxyMalformedJSONOnV1FallsBackToDefault(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("not-json{"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 from default backend, got %d", w.Code)
	}
	if !strings.Contains(doer.gotURL, "47780") {
		t.Fatalf("malformed body should route to default: %s", doer.gotURL)
	}
}

// TestBuildProxyConfigRejectsDuplicateAlias guards against schema
// bugs where two models accidentally share an alias. The router is
// alias-keyed; allowing duplicates would silently use whichever the
// last loop iteration wrote, which is non-deterministic in spirit
// even if Go's range over slice is ordered.
func TestBuildProxyConfigRejectsDuplicateAlias(t *testing.T) {
	prof := Profile{Models: []Model{
		{Alias: "dup", Binary: "x", ModelPath: "/a", Port: 1},
		{Alias: "dup", Binary: "x", ModelPath: "/b", Port: 2},
	}}
	_, err := BuildProxyConfig(prof)
	if err == nil || !strings.Contains(err.Error(), "duplicate alias") {
		t.Fatalf("expected duplicate alias error, got %v", err)
	}
}

// stubDoer captures whatever request the handler forwarded so each
// test can assert on the rewritten body / chosen backend address.
type stubDoer struct {
	mu       sync.Mutex
	gotURL   string
	gotBody  []byte
	gotHdr   http.Header
	respFunc func(req *http.Request) (*http.Response, error)
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// req.Body is nil for body-less requests (GET /healthz, etc.) —
	// the production handler at proxy.go:181-184 leaves it unset in
	// that case. Mirror that here so the stub doesn't panic.
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	s.gotURL = req.URL.String()
	s.gotBody = body
	s.gotHdr = req.Header.Clone()
	if s.respFunc != nil {
		// Replace the consumed body so the user-supplied callback
		// sees the request as the production transport would.
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		return s.respFunc(req)
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// newHandlerWithStub builds a handler around the sample profile and
// the supplied Doer stub. Centralizing the wiring keeps the test
// bodies focused on the assertion they're making, not the boilerplate.
func newHandlerWithStub(t *testing.T, prof Profile, doer *stubDoer) http.Handler {
	t.Helper()
	cfg, err := BuildProxyConfig(prof)
	if err != nil {
		t.Fatalf("BuildProxyConfig: %v", err)
	}
	return NewProxyHandler(cfg, ProxyOptions{Doer: doer})
}

// TestProxyRewritesModelFieldToAbsolutePath verifies the core
// router contract: an alias like `qwen3-coder-30b` lands at the
// matching backend port, and the body forwarded upstream carries
// the absolute filesystem path mlx_lm.server expects.
func TestProxyRewritesModelFieldToAbsolutePath(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	body := `{"model":"qwen3-coder-30b","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(doer.gotURL, "127.0.0.1:47781") {
		t.Fatalf("forwarded URL should target the coder backend: %s", doer.gotURL)
	}
	var payload map[string]any
	if err := json.Unmarshal(doer.gotBody, &payload); err != nil {
		t.Fatalf("forwarded body not JSON: %v (body=%s)", err, doer.gotBody)
	}
	if payload["model"] != "/abs/qwen3-coder-30b" {
		t.Fatalf("model field should be rewritten to abs path; got %v", payload["model"])
	}
}

// TestProxyToleratesProviderPrefixedAlias ensures `mlx/qwen3.6-35b`
// (the OpenCode-style provider-tagged form) routes the same as the
// bare alias. Important because some clients always prefix.
func TestProxyToleratesProviderPrefixedAlias(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	body := `{"model":"mlx/qwen3-coder-30b","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(doer.gotURL, "47781") {
		t.Fatalf("provider-prefixed alias should still route to coder backend: %s", doer.gotURL)
	}
}

// TestProxyFallsBackToDefaultAliasOnUnknownModel verifies the
// configured fallback path: a request naming an alias the proxy
// doesn't know lands at the default route rather than 404'ing.
// The Python proxy worked this way and idiom invocations sometimes
// pass legacy aliases, so the safety net must stay.
func TestProxyFallsBackToDefaultAliasOnUnknownModel(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	body := `{"model":"some-unknown-alias"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected fallback to succeed, got %d", w.Code)
	}
	if !strings.Contains(doer.gotURL, "47780") {
		t.Fatalf("fallback should hit the default backend: %s", doer.gotURL)
	}
	var payload map[string]any
	_ = json.Unmarshal(doer.gotBody, &payload)
	if payload["model"] != "/abs/qwen3.6-35b" {
		t.Fatalf("model field should be rewritten to default's abs path; got %v", payload["model"])
	}
}

// TestProxyReturns404WhenNoDefaultAndUnknownAlias is the strict
// path: a profile without a default_alias must 404 unknown aliases
// instead of silently picking one. The error body lists the known
// aliases so the operator can correct the request.
func TestProxyReturns404WhenNoDefaultAndUnknownAlias(t *testing.T) {
	prof := sampleProfile(47780, 47781, 47680)
	prof.Proxy.DefaultAlias = ""
	doer := &stubDoer{}
	h := newHandlerWithStub(t, prof, doer)
	body := `{"model":"nope"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown alias with no default, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "qwen3.6-35b") {
		t.Fatalf("404 body should list known aliases: %s", w.Body.String())
	}
}

// TestProxyMergesMultipleSystemMessages exercises the chat-template
// compatibility shim: when idiom sends multiple system fragments,
// the proxy collapses them into a single leading system message so
// mlx_lm.server's Qwen template doesn't reject the request.
func TestProxyMergesMultipleSystemMessages(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	body := `{"model":"qwen3.6-35b","messages":[
		{"role":"system","content":"base rules"},
		{"role":"user","content":"q1"},
		{"role":"system","content":"lane preamble"}
	]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(doer.gotBody, &payload); err != nil {
		t.Fatalf("forwarded body not JSON: %v", err)
	}
	msgs, ok := payload["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected exactly 2 messages after merge, got %d (%v)", len(msgs), msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first message must be the merged system: %v", first)
	}
	if !strings.Contains(first["content"].(string), "base rules") || !strings.Contains(first["content"].(string), "lane preamble") {
		t.Fatalf("merged system must contain both fragments: %v", first["content"])
	}
}

// TestProxySingleLeadingSystemMessageIsNoOp guards the streaming
// hot path: a request with one system message already at index 0
// must not trigger a JSON re-marshal. We assert the bytes
// forwarded match the bytes received.
func TestProxySingleLeadingSystemMessageIsNoOp(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	body := `{"messages":[{"role":"system","content":"only"},{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if string(doer.gotBody) != body {
		t.Fatalf("body should pass through unchanged when no normalization needed:\n  got:  %s\n  want: %s", doer.gotBody, body)
	}
}

// TestProxyForwardsStreamingChunks verifies SSE pass-through: a
// backend that writes chunks separated by flushes must reach the
// client as separate writes (not coalesced). This is the
// load-bearing property for `stream: true` chat completions —
// without it, clients see all tokens at once at end-of-response.
func TestProxyForwardsStreamingChunks(t *testing.T) {
	chunks := []string{"data: token1\n\n", "data: token2\n\n", "data: [DONE]\n\n"}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	t.Cleanup(backend.Close)

	// Point the routing table at the test backend rather than the
	// canned 127.0.0.1:47781. Using a custom Doer that rewrites the
	// host on the way out keeps the rest of the routing logic
	// intact under test.
	prof := sampleProfile(47780, 47781, 47680)
	cfg, err := BuildProxyConfig(prof)
	if err != nil {
		t.Fatalf("BuildProxyConfig: %v", err)
	}
	doer := &redirectingDoer{target: backend.URL}
	h := NewProxyHandler(cfg, ProxyOptions{Doer: doer})

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen3.6-35b","stream":true,"messages":[]}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	got := w.Body.String()
	for _, c := range chunks {
		if !strings.Contains(got, c) {
			t.Fatalf("response body missing chunk %q; got: %q", c, got)
		}
	}
}

// redirectingDoer forwards every request to a fixed test server,
// regardless of the routed BackendAddr. It lets streaming/upstream
// tests reuse the canonical sample profile without binding to its
// fictional 47781 port.
type redirectingDoer struct{ target string }

func (d *redirectingDoer) Do(req *http.Request) (*http.Response, error) {
	out, err := http.NewRequestWithContext(req.Context(), req.Method, d.target+req.URL.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	return http.DefaultClient.Do(out)
}

// TestProxyReturns502OnBackendError verifies the failure surface:
// a transport error from Doer becomes a JSON 502 with a descriptive
// body. Important because clients distinguish 502 (backend down)
// from 5xx originating in the proxy itself.
func TestProxyReturns502OnBackendError(t *testing.T) {
	doer := &stubDoer{
		respFunc: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		},
	}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"qwen3.6-35b"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on backend error, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "backend unreachable") {
		t.Fatalf("error body should explain failure: %s", w.Body.String())
	}
}

// TestProxyNonV1PathRoutesToDefault verifies the side-channel
// contract: paths outside `/v1/` (think /metrics, /healthz, the
// mlx_lm.server admin surface) bypass the JSON-rewriting logic but
// still land at the default backend. Without this, health checks
// against the proxy would 404.
func TestProxyNonV1PathRoutesToDefault(t *testing.T) {
	doer := &stubDoer{}
	h := newHandlerWithStub(t, sampleProfile(47780, 47781, 47680), doer)
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 from default backend, got %d", w.Code)
	}
	if !strings.Contains(doer.gotURL, "47780") {
		t.Fatalf("non-/v1 should route to default: %s", doer.gotURL)
	}
}

// TestServeProxyBindsAndShutsDownGracefully exercises the listener
// glue end-to-end: ServeProxy serves, signals readiness, accepts a
// request, and exits cleanly when its context is canceled. Uses
// the ProxyOptions.Listener seam so we hand over a kernel-assigned
// port instead of bind-then-rebind, eliminating the race window
// that earlier versions of this test relied on.
func TestServeProxyBindsAndShutsDownGracefully(t *testing.T) {
	cfg, err := BuildProxyConfig(sampleProfile(47780, 47781, 0))
	if err != nil {
		t.Fatalf("BuildProxyConfig: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- ServeProxy(ctx, cfg, ProxyOptions{
			Doer:     &stubDoer{},
			Listener: listener,
		}, func(addr string) { addrCh <- addr })
	}()

	select {
	case addr := <-addrCh:
		if addr == "" {
			t.Fatal("ready callback fired with empty addr")
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Post("http://"+addr+"/v1/chat/completions",
			"application/json", strings.NewReader(`{"model":"qwen3.6-35b"}`))
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		_ = resp.Body.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("ServeProxy never invoked the ready callback")
	}

	cancel()
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("ServeProxy returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeProxy did not return after context cancel")
	}
}

// TestServeProxyRequiresListenPort guards the configuration shim:
// a config without Listen and no injected listener must surface a
// clear error rather than binding to port 0 (which would assign a
// random port the operator can't predict or reuse).
func TestServeProxyRequiresListenPort(t *testing.T) {
	cfg := ProxyConfig{Listen: 0, Routes: map[string]ProxyRoute{
		"x": {Alias: "x", BackendAddr: "127.0.0.1:1"},
	}}
	err := ServeProxy(context.Background(), cfg, ProxyOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("expected listen-port error, got %v", err)
	}
}
