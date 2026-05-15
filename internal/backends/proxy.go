package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ProxyRoute pairs an alias with the backend address and absolute
// model path the request must be rewritten to. Built once per
// `idiom backend proxy` invocation from the loaded Profile; not
// modified after the server starts.
type ProxyRoute struct {
	Alias       string
	BackendAddr string // host:port for the backend mlx_lm.server
	AbsPath     string // absolute filesystem path or HF id mlx_lm.server expects
}

// ProxyConfig is the parsed table the proxy needs to route. Fields
// are derived from a Profile but kept independent so callers (CLI
// flags, tests) can override Listen/DefaultAlias without mutating
// the on-disk schema.
type ProxyConfig struct {
	Listen       int
	DefaultAlias string
	Routes       map[string]ProxyRoute
}

// BuildProxyConfig translates a Profile into the routing table the
// proxy serves. A model with no model_path is rejected — the proxy
// can't rewrite requests to a path the schema didn't supply, and
// silently dropping the route would surface as a confusing 404 at
// request time. Listen and DefaultAlias come straight from
// Profile.Proxy; CLI flags can override them after BuildProxyConfig
// returns.
func BuildProxyConfig(prof Profile) (ProxyConfig, error) {
	if len(prof.Models) == 0 {
		return ProxyConfig{}, errors.New("backends: profile has no models for proxy to route")
	}
	cfg := ProxyConfig{
		Listen:       prof.Proxy.Listen,
		DefaultAlias: strings.TrimSpace(prof.Proxy.DefaultAlias),
		Routes:       make(map[string]ProxyRoute, len(prof.Models)),
	}
	for _, m := range prof.Models {
		alias := strings.TrimSpace(m.Alias)
		if alias == "" {
			return ProxyConfig{}, errors.New("backends: model with empty alias cannot be routed")
		}
		path := expandHome(strings.TrimSpace(m.ModelPath))
		if path == "" {
			return ProxyConfig{}, fmt.Errorf("backends: model %q has empty model_path; proxy cannot rewrite without it", alias)
		}
		if m.Port <= 0 {
			return ProxyConfig{}, fmt.Errorf("backends: model %q has invalid port %d", alias, m.Port)
		}
		if _, dup := cfg.Routes[alias]; dup {
			return ProxyConfig{}, fmt.Errorf("backends: duplicate alias %q in profile", alias)
		}
		cfg.Routes[alias] = ProxyRoute{
			Alias:       alias,
			BackendAddr: net.JoinHostPort(modelHost(m), strconv.Itoa(m.Port)),
			AbsPath:     path,
		}
	}
	if cfg.DefaultAlias != "" {
		if _, ok := cfg.Routes[cfg.DefaultAlias]; !ok {
			return ProxyConfig{}, fmt.Errorf("backends: proxy default_alias %q is not a registered model", cfg.DefaultAlias)
		}
	}
	return cfg, nil
}

// ProxyOptions tunes the running proxy. Production callers pass a
// zero value and get sane defaults; tests inject Doer to skip a
// real network round-trip.
type ProxyOptions struct {
	// Doer is the HTTP client the proxy uses to forward requests.
	// Nil means "use the default streaming-friendly client." Tests
	// inject a stub that returns canned responses without dialing.
	Doer interface {
		Do(req *http.Request) (*http.Response, error)
	}
	// LogFunc receives one-line proxy events (route picks, errors).
	// Nil means "discard." The CLI wires this to stderr.
	LogFunc func(format string, args ...any)
	// Listener, when set, is what ServeProxy serves on instead of
	// binding cfg.Listen itself. Tests bind a kernel-assigned port
	// (`net.Listen("tcp", "127.0.0.1:0")`) and hand the listener
	// over so they don't race against re-binding the same port.
	// Production callers leave this nil and ServeProxy binds
	// cfg.Listen directly.
	Listener net.Listener
}

// NewProxyHandler returns the http.Handler that implements the
// alias router. Splitting handler-construction from server-running
// lets callers (CLI, tests) wire their own listener / shutdown
// behavior around the same routing logic.
func NewProxyHandler(cfg ProxyConfig, opts ProxyOptions) http.Handler {
	doer := opts.Doer
	if doer == nil {
		doer = defaultProxyClient()
	}
	logf := opts.LogFunc
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &proxyHandler{cfg: cfg, doer: doer, logf: logf}
}

// defaultProxyClient builds an http.Client tuned for streaming
// long-lived chat completion responses. mlx_lm.server can take
// minutes for a long generation, so the per-request timeout matches
// the Python proxy's 900s read window. We also disable response
// compression so we can stream the body straight through without
// a decode/re-encode round-trip.
func defaultProxyClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		MaxIdleConnsPerHost:   8,
		ResponseHeaderTimeout: 90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	return &http.Client{
		Timeout:   15 * time.Minute,
		Transport: transport,
	}
}

// proxyHandler routes one OpenAI-compatible request to the backend
// matching its `model` field, falling back to DefaultAlias when set.
// Mirrors the contract of Colony's mlx_alias_proxy.py so projects
// can drop the Python script without behavior change.
type proxyHandler struct {
	cfg  ProxyConfig
	doer interface {
		Do(req *http.Request) (*http.Response, error)
	}
	logf func(format string, args ...any)
}

// hopByHopHeaders are stripped from both the inbound request before
// forwarding and the backend response before relay. Content-Length
// is included because http.Client computes it for us; copying the
// inbound value would conflict with the rewritten body.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"content-length":      {},
	"host":                {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// maxRequestBodyBytes caps how much of the inbound body we'll read
// into memory. Chat completion requests are usually <100KB; this is
// a conservative ceiling that protects against a buggy or hostile
// client without rejecting anything realistic.
const maxRequestBodyBytes = 10 << 20

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Be defensive: although net/http server requests are documented
	// to have a non-nil Body, httptest.NewRequest paths and some
	// middleware can leave it nil, which would crash io.ReadAll.
	var body []byte
	if r.Body != nil {
		limited := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		var err error
		body, err = io.ReadAll(limited)
		if err != nil {
			h.writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		_ = r.Body.Close()
	}

	route, body := h.pickRoute(r.URL.Path, body)
	if route == nil {
		aliases := sortedAliases(h.cfg.Routes)
		h.logf("[proxy] 404 path=%s no matching route (known: %v)", r.URL.Path, aliases)
		h.writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no route for model; known aliases: %v", aliases))
		return
	}

	target := "http://" + route.BackendAddr + r.URL.RequestURI()
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	upstream, err := http.NewRequestWithContext(r.Context(), r.Method, target, reqBody)
	if err != nil {
		h.writeJSONError(w, http.StatusInternalServerError, "build upstream request: "+err.Error())
		return
	}
	for k, vs := range r.Header {
		if _, drop := hopByHopHeaders[strings.ToLower(k)]; drop {
			continue
		}
		for _, v := range vs {
			upstream.Header.Add(k, v)
		}
	}

	resp, err := h.doer.Do(upstream)
	if err != nil {
		h.logf("[proxy] 502 alias=%s backend=%s err=%v", route.Alias, route.BackendAddr, err)
		h.writeJSONError(w, http.StatusBadGateway, "backend unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if _, drop := hopByHopHeaders[strings.ToLower(k)]; drop {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream chunks straight through. Flushing after each read
	// preserves SSE chunk boundaries for `stream: true` chat
	// completion responses; a buffering write would coalesce events
	// and break clients that depend on per-token delivery.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8192)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				h.logf("[proxy] client write error alias=%s err=%v", route.Alias, werr)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr == io.EOF {
			return
		}
		if rerr != nil {
			h.logf("[proxy] backend read error alias=%s err=%v", route.Alias, rerr)
			return
		}
	}
}

// pickRoute decides which backend handles the request and (when
// applicable) rewrites the JSON body so `model` carries the absolute
// path mlx_lm.server expects. Non-/v1/ paths and non-JSON bodies
// route to DefaultAlias unchanged so health-check and metric
// endpoints still reach a backend.
func (h *proxyHandler) pickRoute(path string, body []byte) (*ProxyRoute, []byte) {
	if !strings.HasPrefix(path, "/v1/") || len(body) == 0 {
		return h.lookupDefault(), body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return h.lookupDefault(), body
	}

	alias, _ := payload["model"].(string)
	alias = strings.TrimSpace(alias)
	// Tolerate "mlx/qwen3.6-35b" style provider-prefixed names so
	// callers that pass a provider-tagged alias still land on the
	// right backend.
	if i := strings.Index(alias, "/"); i >= 0 {
		alias = alias[i+1:]
	}
	if alias == "" {
		alias = h.cfg.DefaultAlias
	}
	route, ok := h.cfg.Routes[alias]
	if !ok {
		if h.cfg.DefaultAlias == "" {
			return nil, body
		}
		def, defOK := h.cfg.Routes[h.cfg.DefaultAlias]
		if !defOK {
			return nil, body
		}
		// Operators debugging "why did my request go to the wrong
		// model" need to see the silent rewrite happen. One line
		// per fallback is enough — chatty enough to trace, quiet
		// enough not to dominate the log.
		h.logf("[proxy] alias=%q not registered; falling back to default=%q", alias, h.cfg.DefaultAlias)
		route = def
	}

	changed := false
	// Match Colony's mlx_alias_proxy.py: only rewrite `model` when
	// the caller actually supplied it. Adding a field the request
	// omitted could change semantics for callers that intentionally
	// rely on mlx_lm.server's default behavior.
	if _, hasModel := payload["model"]; hasModel {
		payload["model"] = route.AbsPath
		changed = true
	}
	if strings.HasPrefix(path, "/v1/chat/completions") {
		if normalizeChatPayload(payload) {
			changed = true
		}
	}
	if changed {
		if rewritten, err := json.Marshal(payload); err == nil {
			body = rewritten
		}
	}
	return &route, body
}

func (h *proxyHandler) lookupDefault() *ProxyRoute {
	if h.cfg.DefaultAlias == "" {
		return nil
	}
	r, ok := h.cfg.Routes[h.cfg.DefaultAlias]
	if !ok {
		return nil
	}
	return &r
}

// normalizeChatPayload merges multiple system messages into one
// leading entry. mlx_lm.server's Qwen chat template accepts at most
// one system message and requires it first; idiom legitimately sends
// multiple system fragments (base + lane preamble + project overlay),
// so the proxy collapses them so the backend doesn't reject the
// request.
//
// Returns true when the messages slice was modified; the caller
// re-marshals only when something changed.
func normalizeChatPayload(payload map[string]any) bool {
	raw, ok := payload["messages"].([]any)
	if !ok || len(raw) == 0 {
		return false
	}
	systems := make([]string, 0, 2)
	others := make([]any, 0, len(raw))
	for _, item := range raw {
		msg, ok := item.(map[string]any)
		if !ok {
			others = append(others, item)
			continue
		}
		role, _ := msg["role"].(string)
		if role == "system" {
			text := strings.TrimSpace(contentAsText(msg["content"]))
			if text != "" {
				systems = append(systems, text)
			}
			continue
		}
		others = append(others, item)
	}
	if len(systems) == 0 {
		return false
	}
	// Single system already at the head with intact content stays
	// as-is so steady-state streaming requests don't pay the
	// remarshal cost. Anything else (multiple systems, system not
	// at index 0, or content that needed trimming) gets rebuilt.
	if len(systems) == 1 {
		first, ok := raw[0].(map[string]any)
		if ok {
			if role, _ := first["role"].(string); role == "system" {
				if contentAsText(first["content"]) == systems[0] {
					return false
				}
			}
		}
	}
	merged := map[string]any{"role": "system", "content": strings.Join(systems, "\n\n")}
	normalized := make([]any, 0, len(others)+1)
	normalized = append(normalized, merged)
	normalized = append(normalized, others...)
	payload["messages"] = normalized
	return true
}

// contentAsText extracts a plain-text representation of an OpenAI
// message content field, which can be a string or a list of typed
// parts. Anything we can't read becomes the empty string so the
// caller drops it from the merged system message rather than
// emitting noise.
func contentAsText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for i, item := range c {
			switch part := item.(type) {
			case string:
				if i > 0 {
					b.WriteString("\n")
				}
				b.WriteString(part)
			case map[string]any:
				if text, ok := part["text"].(string); ok {
					if i > 0 {
						b.WriteString("\n")
					}
					b.WriteString(text)
				}
			}
		}
		return b.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", c)
	}
}

func sortedAliases(routes map[string]ProxyRoute) []string {
	out := make([]string, 0, len(routes))
	for a := range routes {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// writeJSONError writes a small JSON error body. We don't wire in a
// shared error type because the body shape only has to be readable
// by curl and the OpenAI client SDKs (which expect `{"error": ...}`).
func (h *proxyHandler) writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ServeProxy starts an http.Server bound to cfg.Listen and blocks
// until ctx is canceled. Returns the listener's address (so the CLI
// can log the resolved port) and an error from the underlying
// server. Splitting Serve from NewProxyHandler keeps the handler
// trivially testable without a real listener.
//
// `ready` is invoked once after the listener has bound but before
// Serve starts accepting requests; the CLI uses it to print the
// address line, and tests use it to know when it's safe to dial.
func ServeProxy(ctx context.Context, cfg ProxyConfig, opts ProxyOptions, ready func(addr string)) error {
	listener := opts.Listener
	if listener == nil {
		if cfg.Listen <= 0 {
			return errors.New("backends: proxy listen port is required")
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Listen))
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("backends: bind proxy port %d: %w", cfg.Listen, err)
		}
		listener = l
	}
	handler := NewProxyHandler(cfg, opts)
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	if ready != nil {
		ready(listener.Addr().String())
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
