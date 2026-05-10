# Phase 4c Review Notes: `idiom backend proxy`

Date: 2026-05-10
Scope: Uncommitted working-tree changes implementing the alias-routing HTTP proxy

## Summary

Phase 4c implements `idiom backend proxy`, a Go reverse proxy that replaces Colony's `mlx_alias_proxy.py`. It listens on a single port, inspects each `/v1/*` request's `model` field to pick a backend, rewrites the model alias to an absolute filesystem path, merges multiple system messages into one (for Qwen template compatibility), and streams responses chunk-by-chunk.

Files: `internal/backends/proxy.go` (466 lines), `internal/backends/proxy_test.go` (466 lines), plus changes to `cmd/idiom/backend.go`, `cmd/idiom/backend_test.go`, `internal/backends/backends.go`, `docs/lane-architecture.md`, `docs/reusability-design.md`.

Build: clean. Full suite: one test panics (see BLOCKER below).

---

## BLOCKER: Nil-body panic in ServeHTTP

**Severity: BLOCKER (test panics, production GET requests will crash the handler)**

`TestProxyNonV1PathRoutesToDefault` panics with nil pointer dereference.

The test creates a GET request with no body:
```go
req := httptest.NewRequest("GET", "/healthz", nil)
```

`ServeHTTP` calls `io.ReadAll(r.Body)` on line 165 without guarding against `r.Body == nil`. `io.ReadAll(nil)` panics. This affects any real GET request to the proxy (health checks, metrics, etc.).

**Fix**: Add a nil guard before ReadAll:
```go
func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    var body []byte
    if r.Body != nil {
        var err error
        body, err = io.ReadAll(r.Body)
        if err != nil {
            h.writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
            return
        }
        _ = r.Body.Close()
    }
```

---

## Issues (by severity)

### HIGH: Streaming test uses real HTTP client

`TestProxyForwardsStreamingChunks` creates a real `httptest.Server` and uses `http.DefaultClient.Do()` via `redirectingDoer`. This makes a real network round-trip in a unit test, introducing flakiness under load. The rest of the test suite uses `stubDoer` for deterministic behavior. The streaming test should either:
- Use a stub that pipes chunks through an in-memory pipe (io.Pipe), or
- At minimum, document why a real round-trip is necessary here (it may be: testing the flush-to-SSE contract).

This is low urgency since the test passes reliably, but it breaks the "no real network in unit tests" convention.

### MEDIUM: No request size limit

`ServeHTTP` reads the entire request body into memory (`io.ReadAll`). A malicious or buggy client could send an enormous request. The Python proxy presumably had similar behavior (it's localhost-only), but a `http.MaxBytesReader` wrapper would be a cheap safety net:
```go
r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
```

### MEDIUM: No logging on fallback routing

When a request with an unknown alias falls back to DefaultAlias, no log line is emitted. The only log is on 404 (when there's no default). Operators debugging "why did my request go to the wrong model?" would benefit from a single log line like:
```
[proxy] alias="unknown-model" not found, falling back to default="qwen3.6-35b"
```

### LOW: Stale doc text

`docs/reusability-design.md` line 268 still says:
> `idiom backend proxy` is a Go port of `mlx_alias_proxy.py` (or shells out to it for now)

The parenthetical is outdated; Phase 4c is a full native port. Minor doc cleanup.

### LOW: expandHome reuse from launcher.go

`BuildProxyConfig` calls `expandHome(m.ModelPath)` on line 59, which lives in `launcher.go`. This is fine functionally (same package), but creates an implicit dependency: proxy.go requires launcher.go to compile. Currently they're always compiled together, so this is academic. If the package were ever split, `expandHome` should move to a shared file (e.g., `util.go`).

### LOW: provider prefix stripping is first-slash only

`pickRoute` strips the provider prefix by taking everything after the first `/`:
```go
if i := strings.Index(alias, "/"); i >= 0 {
    alias = alias[i+1:]
}
```
An alias like `a/b/c` becomes `b/c`, not `c`. This matches the comment ("mlx/qwen3.6-35b style provider-prefixed names"), and no real aliases contain slashes. Fine for now.

---

## Positive observations

- **Clean separation**: `NewProxyHandler` (handler) vs `ServeProxy` (listener+shutdown) vs `BuildProxyConfig` (config) is well-factored. Tests can exercise routing logic without a real listener.
- **Good test naming**: Every test has a comment explaining the contract it guards and why the failure mode matters.
- **System message merging**: `normalizeChatPayload` handles the single-system fast path correctly (avoids re-marshal when nothing changed).
- **contentAsText**: Handles both string and multi-part content types. The nil and default cases return empty string, which is the right behavior for the caller.
- **Graceful shutdown**: `ServeProxy` uses context cancellation with a 5-second shutdown timeout.
- **DI pattern**: `ProxyOptions.Doer` interface is the same DI pattern used throughout idiom. Consistent.
- **Flag override validation**: `--default` is validated against the resolved routes before the server starts. Prevents a runtime 404 loop.
- **Hop-by-hop header stripping**: Correct list, includes Content-Length (important since the body gets rewritten).

---

## Test coverage gaps

1. **No test for invalid port zero**: `BuildProxyConfig` rejects `m.Port <= 0`, but there's no negative test exercising this path directly.
2. **No test for non-JSON body on `/v1/` path**: `pickRoute` falls back to DefaultAlias when JSON parsing fails. This path would be exercised by `TestProxyNonV1PathRoutesToDefault` once the nil-body bug is fixed, but a dedicated test would be clearer.
3. **No concurrent request test**: The handler stores no mutable state, so concurrent safety should be fine, but a `go test -race` run with parallel requests would confirm.

---

## Files changed summary

| File | Lines | Change |
|------|-------|--------|
| `internal/backends/proxy.go` | +466 | New: core proxy implementation |
| `internal/backends/proxy_test.go` | +466 | New: comprehensive test coverage |
| `cmd/idiom/backend.go` | +61 | New: `runBackendProxy`, `backendServeProxy` var, proxy case in dispatch, usage text |
| `cmd/idiom/backend_test.go` | +164 | New: 5 proxy CLI tests + helpers |
| `internal/backends/backends.go` | +22 | New: `Proxy` struct fields (Listen, DefaultAlias, DefaultCoder), doc updates |
| `docs/lane-architecture.md` | ~8 | Updated: Phase 4c section |
| `docs/reusability-design.md` | ~3 | Updated: status line, phase table |

---

## Verdict

One blocker (nil-body panic) that must be fixed before commit. The rest is clean, well-structured code with good test coverage and consistent DI patterns. The proxy is a faithful port of the Python script with the right enhancements (flag overrides, graceful shutdown, structured errors).
