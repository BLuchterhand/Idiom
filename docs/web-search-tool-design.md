# Web search via MCP — design and acceptance criteria

Adds web search to the `idiom` harness by finishing the MCP wiring already
scaffolded under [internal/mcp](internal/mcp/) and adding HTTP/SSE
transport so the existing registry can talk to hosted MCP servers like
Exa's `https://mcp.exa.ai/mcp`. Web search then ships as **a config
entry**, not new tool code.

This replaces the v1 of this doc that proposed a parallel
`internal/web/exa.go` HTTP client. That design duplicated infrastructure
we already have. The right shape is: bridge MCP through, and the Exa
endpoint becomes one line of JSON.

Status: **landed — W1–W6 implemented as 10-step plan in §11**.

Test surface: 5 mcp packages green —
- `internal/mcp` (12 HTTPTransport + 18 SSRF/redaction tests + existing stdio)
- `internal/config` (10 MCPServer/Workspace tests)
- `internal/workspace` (10 fan-in + dispatch + permission-target tests)
- `internal/agent` (5 parseToolCall + 3 mode-default + lane-classifier tests)
- `internal/app` (4 setupMCPRegistry tests)

To enable: copy the `mcp` block from `config.example.json` into your
`config.json` (drop the leading underscore). No code changes required.

---

## 1. Goals

1. **Web search.** Let the model search the public web via Exa AI's
   hosted MCP endpoint, the same backend OpenCode uses.
2. **Finish the MCP integration.** [internal/mcp](internal/mcp/) was
   landed as Fix 8 in [docs/opencode-fixes-design.md](docs/opencode-fixes-design.md)
   but is not actually wired into the runner. Any user MCP server
   configured in `config.json` today is silently ignored. This change
   fixes that on the way through.
3. **OpenCode-shaped surface.** Same backend (Exa MCP), same gating
   philosophy (user picks Mode, Mode picks rule), same per-call
   permission model — extended to all MCP-discovered tools, not just
   web search.
4. **Add hosted MCP support.** [internal/mcp/client.go:1-5](internal/mcp/client.go:1)
   explicitly says "No SSE/HTTP transport". This change adds it. Once
   in, every future hosted MCP (GitHub's, Linear's, Sentry's, …) plugs
   in as a config entry.

## 2. Non-goals

1. **A bespoke Exa client.** No `internal/web/exa.go`. Everything
   speaks MCP.
2. **A bespoke `web_fetch` Go tool.** OpenCode's `webfetch` is a
   direct HTTP fetcher with HTML→Markdown built into the agent.
   Decision 5 routes us around it: we configure
   [`@modelcontextprotocol/server-fetch`](https://github.com/modelcontextprotocol/servers/tree/main/src/fetch)
   instead — same shape as Exa, zero new Go code, identical
   permission story. See §13 for the trade-offs we accepted.
3. **A second search provider.** Exa-only today. Provider abstraction
   only when there's a second concrete provider to abstract over.
4. **Long-lived streaming SSE / MCP Streamable HTTP (spec 2025-03-26).**
   Exa's endpoint uses the simpler "POST → SSE-formatted response body"
   shape. Full bidirectional streaming MCP-over-HTTP can wait until a
   server requires it.
5. **Per-session $ budget caps on Exa calls.** Useful, but separable.
   Track as W7 in §14.

## 3. How OpenCode does this

`websearch` in OpenCode is one screen of code
(`packages/opencode/src/tool/websearch.ts`, 72 LOC) that wraps a one-
file MCP-over-HTTP-SSE client (`packages/opencode/src/tool/mcp-exa.ts`,
78 LOC). The Exa client:

```ts
const URL = process.env.EXA_API_KEY
  ? `https://mcp.exa.ai/mcp?exaApiKey=${encodeURIComponent(process.env.EXA_API_KEY)}`
  : "https://mcp.exa.ai/mcp"
```

POSTs a JSON-RPC 2.0 `tools/call` request, reads the response body as
SSE-formatted text, walks lines for `data: <json>` frames, returns
`result.content[0].text`. Single attempt, 25 s timeout, no retry, no
caching, no fallback (`mcp-exa.ts:74`).

The `EXA_API_KEY` env var is **optional** — Exa's hosted MCP works
unauthenticated. OpenCode's docs make that explicit
(`packages/web/src/content/docs/tools.mdx:281`):

> No API key is required — the tool connects directly to Exa AI's
> hosted MCP service without authentication.

`websearch` is gated twice (`packages/opencode/src/tool/registry.ts:280`):

```ts
if (tool.id === WebSearchTool.id) {
  return input.providerID === ProviderID.opencode || Flag.OPENCODE_ENABLE_EXA
}
```

So it's invisible to the model unless the user opts in by env var or by
using OpenCode's hosted LLM provider — *and* every call still goes
through the permission `ask()` flow.

The exposed parameters are the ones Exa accepts:

| Field | Type | Default |
| --- | --- | --- |
| `query` | string | required |
| `numResults` | number | 8 |
| `livecrawl` | `"fallback" \| "preferred"` | `fallback` |
| `type` | `"auto" \| "fast" \| "deep"` | `auto` |
| `contextMaxCharacters` | number | 10 000 |

Exposed tools: `web_search_exa`, `get_code_context_exa`. We get both
from the same MCP server — Exa's "code context" search is what
OpenCode renders as their `codesearch` tool.

The model-facing description text is `websearch.txt` with a
`{{year}}`-substituted line that explicitly anchors the model to the
current year for time-sensitive queries. We mirror this.

---

## 4. Current state of `internal/mcp`

The package is complete enough to be useful, but unused.

What exists:

- `Transport` interface with `Send / Receive / Close`
  ([client.go:60](internal/mcp/client.go:60)).
- `Client` speaking JSON-RPC 2.0 with `Initialize / ListTools /
  CallTool` ([client.go:68](internal/mcp/client.go:68)).
- `StdioTransport` for stdio servers
  ([client.go:247](internal/mcp/client.go:247)).
- `Registry` that spawns servers, namespaces tools as
  `mcp:<server>/<tool>`, dispatches `Call`
  ([registry.go:22](internal/mcp/registry.go:22)).
- `RegisterExisting` for non-stdio transports — the hook this design
  uses ([registry.go:78](internal/mcp/registry.go:78)).

What's missing:

- **No HTTP/SSE transport.** `client.go:3` says so.
- **No bridge to the model.** `Registry.Tools()` exists, but
  [internal/workspace/tool_schemas.go:73](internal/workspace/tool_schemas.go:73)'s
  `baseToolDefinitions` is hand-rolled and ignorant of MCP.
- **No bridge to dispatch.** [internal/workspace/manager.go:359](internal/workspace/manager.go:359)'s
  `Manager.Execute` switch has no case for `mcp:*` tool names.
- **No boot wiring.** [cmd/idiom/main.go](cmd/idiom/main.go) does not
  construct a registry from `cfg.MCP` or hand it to the workspace.
- **No permission integration.** [internal/agent/modes.go](internal/agent/modes.go)
  has no rules for MCP tools; [internal/workspace/tool_rules.go:114](internal/workspace/tool_rules.go:114)'s
  `extractToolRuleTarget` doesn't know how to extract a target from
  an MCP tool call's args.

Each of those is a small, additive edit.

---

## 5. Integration map

| Concern | File | Site |
| --- | --- | --- |
| HTTP/SSE transport | `internal/mcp/http.go` (new) | new file |
| Server config schema | [internal/config/config.go:71](internal/config/config.go:71) | `MCPServer` struct |
| Registry → boot | [cmd/idiom/main.go](cmd/idiom/main.go) | spawn from `cfg.MCP`, hand to manager |
| Registry → workspace | [internal/workspace/manager.go](internal/workspace/manager.go) | new `Manager.SetMCPRegistry` + dispatch case |
| Tool list to model | [internal/workspace/tool_schemas.go:22](internal/workspace/tool_schemas.go:22) | extend `BuildToolDefinitions` |
| Allow-list | [internal/workspace/tool_manifest.go:25](internal/workspace/tool_manifest.go:25) | extend `ToolManifestTools` |
| Permission rules | [internal/agent/modes.go](internal/agent/modes.go) | per-Mode rules for `mcp:*` |
| Rule target extraction | [internal/workspace/tool_rules.go:114](internal/workspace/tool_rules.go:114) | `extractToolRuleTarget` |
| Lane policy | [internal/agent/runner_lane_policy.go:38](internal/agent/runner_lane_policy.go:38) | negative-list additions |

---

## 6. The fixes

### Fix W1 — HTTP/SSE transport in `internal/mcp`

**Problem.** [internal/mcp/client.go:1-5](internal/mcp/client.go:1)
explicitly carves out HTTP transport as out-of-scope. Hosted MCP servers
(Exa, GitHub's, Linear's) all expose their endpoints over HTTP, not
stdio. Without HTTP transport we can't talk to any of them.

**Design.** Implement the `Transport` interface against an HTTP
endpoint. Each MCP request becomes one HTTP POST whose body is the
JSON-RPC frame. The response is read as SSE-formatted text — lines
prefixed with `data: ` are JSON-RPC reply frames; everything else
(including blank lines, `event:` lines, and comment lines) is
ignored. Frames are pushed into an in-memory channel; `Receive`
returns the next one.

This shape exactly matches what Exa's endpoint produces. It is *not*
the full bidirectional MCP Streamable HTTP transport (spec 2025-03-26)
— that adds long-lived SSE for server-pushed notifications, which Exa
does not use. We pin a tag on the package comment and revisit if a
target server requires it.

```go
// internal/mcp/http.go (sketch — not final)

type HTTPTransport struct {
    url     string
    client  *http.Client
    headers http.Header

    inbox  chan []byte
    closed chan struct{}
    once   sync.Once
}

func StartHTTP(ctx context.Context, url string, headers http.Header, timeout time.Duration) (*HTTPTransport, error)

func (t *HTTPTransport) Send(ctx context.Context, payload []byte) error {
    // Validate URL via SSRF guard (W5).
    // POST payload as application/json, Accept: application/json, text/event-stream.
    // In a goroutine, scan the response body line-by-line; for each line
    // starting with "data: ", push the JSON suffix into t.inbox.
    // On EOF or error, the goroutine exits without closing inbox.
}

func (t *HTTPTransport) Receive(ctx context.Context) ([]byte, error) {
    select {
    case frame := <-t.inbox:
        return frame, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    case <-t.closed:
        return nil, io.EOF
    }
}

func (t *HTTPTransport) Close() error
```

The `Client.call` loop in [client.go:128](internal/mcp/client.go:128)
is request/reply and demuxes by JSON-RPC `id`, so multiple concurrent
Sends from one Client (rare for a tool call, but possible) are safe —
frames may arrive in any order; the Client's `pending` map keyed by
`id` reorders them.

**Files.**

- `internal/mcp/http.go` (new) — ~120 LOC: `HTTPTransport`, `StartHTTP`,
  SSE-line scanner, header support for `Authorization` / custom auth.
- `internal/mcp/http_test.go` (new) — `httptest.Server` returning
  scripted SSE bodies, asserts request shape, response decoding,
  timeout, malformed-body resilience (drop and continue).
- [internal/mcp/client.go:1-5](internal/mcp/client.go:1) — update the
  package doc comment to drop "No SSE/HTTP transport".

**Acceptance.**

- `httptest.Server` returns
  `data: {"jsonrpc":"2.0","id":1,"result":{...}}\n\n`; `Client.CallTool`
  returns the parsed result.
- Multi-frame body — server writes a comment line, an empty line, then
  the data frame; result still parses.
- Body with extra `data:` frames after the matching reply — extra
  frames sit in `inbox` until `Close` drains them, no panic.
- Server returns HTTP 401 — `Send` returns an error, no goroutine
  leak (verified via `goleak` or a sentinel).
- Server hangs past `cfg.TimeoutSeconds` — `Send` returns context
  deadline; `inbox` channel is closed cleanly.
- Server returns malformed JSON in a `data:` line — frame is dropped
  silently (matches stdio behavior in [client.go:107-111](internal/mcp/client.go:107)).
- SSRF guard: `StartHTTP` with `url=http://127.0.0.1/...` returns an
  error before any socket is opened (see W5).

**Non-goals.** No reconnection logic — Exa's endpoint completes one
exchange per POST. No shared connection pool — Go's default
`http.Transport` already keeps connections warm across calls to the
same host.

---

### Fix W2 — Extend `MCPServer` config to support both transports

**Problem.** [internal/config/config.go:71-77](internal/config/config.go:71)
defines `MCPServer` with `Command / Args / Env`. There's no field for a
hosted endpoint URL.

**Design.** Add a `URL` field. If set, the registry uses HTTP
transport; if empty, it uses stdio (existing behavior). One optional
`Headers map[string]string` for static auth (Bearer tokens, custom
headers).

```go
// internal/config/config.go (delta)

type MCPServer struct {
    Name    string            `json:"name"`
    Command string            `json:"command,omitempty"` // stdio
    Args    []string          `json:"args,omitempty"`    // stdio
    Env     []string          `json:"env,omitempty"`     // stdio
    URL     string            `json:"url,omitempty"`     // http
    Headers map[string]string `json:"headers,omitempty"` // http (optional)
}
```

Validation: exactly one of `Command` or `URL` must be set per server;
both unset or both set is a config error reported at startup.

**Files.**

- [internal/config/config.go:71-77](internal/config/config.go:71) — add fields.
- [internal/config/config_test.go](internal/config/config_test.go) —
  round-trip tests for both shapes; both-set and both-empty fail
  validation.

**Acceptance.**

- `config.json` containing
  `{"mcp":[{"name":"exa","url":"https://mcp.exa.ai/mcp"}]}`
  parses, validates, and produces an `MCPServer` with `URL` set and
  `Command` empty.
- `{"mcp":[{"name":"x","command":"foo","url":"https://..."}]}` fails
  validation with a clear message.
- Headers map round-trips.

**Non-goals.** No env-var interpolation in URL/Headers (yet) — users
who need a secret in a header can use `${env:NAME}` syntax in a later
W6 if and when the need surfaces.

---

### Fix W3 — Boot the registry from config and hand it to the workspace

**Problem.** Even with HTTP transport, the registry is unused. Nothing
in [cmd/idiom/main.go](cmd/idiom/main.go) constructs one.

**Design.** At startup, after the config loads and before the runner
starts:

1. Construct `mcp.NewRegistry()`.
2. For each entry in `cfg.MCP`:
   - If `URL != ""`: `mcp.StartHTTP(ctx, entry.URL, entry.Headers, timeout)` →
     `mcp.NewClient(transport)` → `registry.RegisterExisting(ctx, name, client)`.
   - Else: `registry.Spawn(ctx, mcp.ServerConfig{...})`.
3. Hand the registry to the workspace manager via
   `manager.SetMCPRegistry(registry)`.
4. Defer `registry.Close()` on shutdown.

If a single server fails to register, log and continue — the rest of
the agent should still work, the same forgiving stance the existing
`Spawn` already documents.

**Files.**

- [cmd/idiom/main.go](cmd/idiom/main.go) — new `setupMCPRegistry(ctx, cfg)`
  helper, called once per session.
- [internal/workspace/manager.go](internal/workspace/manager.go) — new
  field `mcp *mcp.Registry`, new method `SetMCPRegistry(*mcp.Registry)`.
- [internal/agent/runner.go:128](internal/agent/runner.go:128) — no
  change at the call site; the runner already pulls tool definitions
  from the workspace, which now includes MCP tools (W4).

**Acceptance.**

- Boot test: spin up a fake MCP server (httptest), point a config at
  it, observe that `Manager.MCPRegistry().Tools()` lists the server's
  tools.
- Misconfig test: a config with one valid server and one invalid (URL
  resolves to nothing) produces logs about the failure, but the valid
  one still registers.
- Shutdown test: `Close` is called on the registry; HTTP transport
  goroutine exits within 2 s.

**Non-goals.** No hot-reload of MCP config — startup-only. The user
re-runs `idiom` to pick up new servers. (OpenCode does the same.)

---

### Fix W4 — Surface MCP tools to the model and dispatch them

**Problem.** Even with the registry populated, the model never sees the
tools and the workspace switch can't dispatch them.

**Design.** Three small changes:

1. **Tool definitions sent to the LLM** —
   [internal/workspace/tool_schemas.go:22](internal/workspace/tool_schemas.go:22)'s
   `BuildToolDefinitions` becomes a method on `Manager` (or accepts an
   optional registry argument). After emitting `baseToolDefinitions()`
   it appends one `ToolDefinition` per registered MCP tool:
   - `Name`: `mcp:exa/web_search_exa` (the qualified name, used as-is
     in the tool spec sent to the model).
   - `Description`: the upstream tool's description, prefixed with
     `[mcp:exa] ` so the model and the user can tell where it comes
     from. The Exa server publishes good descriptions; we don't rewrite
     them.
   - `ParametersJSON`: the upstream `inputSchema` (already JSON), passed
     through unchanged.

2. **Allow-list** —
   [internal/workspace/tool_manifest.go:25](internal/workspace/tool_manifest.go:25)'s
   `ToolManifestTools` adds every registered MCP tool name to the
   returned slice (when a registry is attached). This keeps the
   `BuildToolDefinitions` allow-list path consistent.

3. **Dispatch** —
   [internal/workspace/manager.go:366](internal/workspace/manager.go:366)
   adds a default branch that recognizes the `mcp:` prefix:

   ```go
   default:
       if strings.HasPrefix(tool, "mcp:") && m.mcp != nil {
           return m.executeMCP(ctx, req)
       }
       return ToolResult{Tool: tool, Error: "unknown tool"}
   ```

   `executeMCP` reconstructs the tool's argument JSON from the
   `ToolRequest` (see "Argument bridging" below), calls
   `m.mcp.Call(ctx, req.Tool, args)`, joins all returned text-content
   parts into `Output`, and returns a `ToolResult` with `Tool: req.Tool`.

**Argument bridging.** `ToolRequest` is a flat struct with hard-coded
field names — `Query`, `Path`, `Pattern`, etc. MCP tools have
arbitrary JSON-Schema arguments. Two paths:

- **A. Add a generic `Args json.RawMessage` field to `ToolRequest`.**
  When the tool name starts with `mcp:`, the agent layer marshals the
  model's tool-call arguments into `Args` verbatim instead of mapping
  them to typed fields. `executeMCP` passes `Args` straight through to
  `mcp.Registry.Call`.
- **B. Keep the typed mapping, but special-case at the agent layer.**
  Less clean; `parseToolCall` would need a dual code path.

Recommend **A**. It's a single new field on `ToolRequest`, and it
isolates the "MCP tools are different" knowledge in one place
([internal/agent/tools.go](internal/agent/tools.go)'s `parseToolCall`).

**Files.**

- [internal/workspace/manager.go:69](internal/workspace/manager.go:69)
  (`ToolRequest`) — add `Args json.RawMessage`.
- [internal/workspace/manager.go](internal/workspace/manager.go) — new
  `(*Manager).executeMCP` method (~30 LOC).
- [internal/workspace/tool_schemas.go:22](internal/workspace/tool_schemas.go:22)
  — extend `BuildToolDefinitions` to fan in MCP tools, and convert it
  into a function that takes a registry (or accept it via a
  `Manager` method wrapping the existing function).
- [internal/workspace/tool_manifest.go:25](internal/workspace/tool_manifest.go:25)
  — extend `ToolManifestTools` similarly.
- [internal/agent/tools.go](internal/agent/tools.go) (`parseToolCall`)
  — when `tool_call.Function.Name` starts with `mcp:`, route the raw
  argument JSON into `req.Args`; do not attempt typed-field mapping.
- [internal/agent/tools.go](internal/agent/tools.go)
  (`toolResultMessage` / `FormatToolResultForModel`) — confirm MCP
  outputs render the same way generic outputs do (they will — the
  formatter is by-result, not by-tool).

**Acceptance.**

- Unit (workspace): with a mock registry exposing one tool, the slice
  returned by `BuildToolDefinitions(nil)` contains an entry with
  `Name == "mcp:fake/echo"` and the upstream description.
- Unit (workspace): `Manager.Execute` with `req.Tool = "mcp:fake/echo"`
  and `req.Args = `{"text":"hi"}`` produces a `ToolResult` whose
  `Output` contains `"hi"`.
- Unit (agent): `parseToolCall` on a tool-call named `mcp:exa/web_search_exa`
  with the example argument JSON populates `req.Args` byte-for-byte
  identical to the LLM-provided arguments.
- Integration: end-to-end with a httptest MCP server, the runner
  drives one turn that calls `mcp:fake/echo` and returns the echoed
  string in the assistant final message.

**Non-goals.** No streaming partial results. No structured
`metadata` extraction from MCP responses (Exa returns one text block
today, future tools may not).

---

### Fix W5 — Permission and lane integration for `mcp:*`

**Problem.** Once MCP tools are visible to the model, they bypass our
existing per-Mode permission rules unless we add specific entries.

**Design.** Two layers:

1. **Per-Mode rules** —
   [internal/agent/modes.go](internal/agent/modes.go) — add an
   `mcp:*` entry to each Mode:
   - **PlanMode**:
     `"mcp:*": [{Pattern: "*", Decision: ToolRuleAllow}]` — plan mode
     is read-only; MCP tools are external reads.
   - **BuildMode**:
     `"mcp:*": [{Pattern: "*", Decision: ToolRuleAsk}]` — network
     egress + cost; user confirms once and stickies via the existing
     "always" flow at the prompt layer.
   - **GeneralMode**:
     `"mcp:*": [{Pattern: "*", Decision: ToolRuleAsk}]`.

   Users can scope further in their `config.json`:
   ```json
   {
     "tool_rules": {
       "mcp:exa/web_search_exa": [
         { "pattern": "*", "decision": "allow" }
       ]
     }
   }
   ```

   This requires
   [internal/workspace/tool_rules.go:94-112](internal/workspace/tool_rules.go:94)'s
   `MatchToolRule` to look up by exact tool name first, then fall back
   to a glob match against `mcp:*`. The existing `MatchToolRule`
   already does map-keyed first-match-wins, so the change is in the
   evaluator: try the literal tool name, then try `mcp:*`.

2. **Rule target extraction** —
   [internal/workspace/tool_rules.go:114-121](internal/workspace/tool_rules.go:114)
   currently extracts `req.Command` for shell tools and `req.Path`
   otherwise. For MCP tools, neither field is meaningful. Extend:
   ```go
   func extractToolRuleTarget(tool string, req ToolRequest) string {
       switch {
       case tool == "shell" || tool == "bash":
           return strings.TrimSpace(req.Command)
       case strings.HasPrefix(tool, "mcp:"):
           // Best-effort: the model's `query` or `url` is the most
           // human-meaningful pattern target.
           if q := stringFromArgs(req.Args, "query"); q != "" {
               return q
           }
           if u := stringFromArgs(req.Args, "url"); u != "" {
               return u
           }
           return ""
       default:
           return strings.TrimSpace(req.Path)
       }
   }
   ```
   Where `stringFromArgs` is a tiny helper that decodes one top-level
   string field from `req.Args` without unmarshaling the whole blob.

3. **Lane policy** —
   [internal/agent/runner_lane_policy.go:38](internal/agent/runner_lane_policy.go:38).
   The existing universal rejections (`conversation_only`,
   `read_only_verification_shell`) already cover MCP tools — they're
   not verification-class and not allowed in conversation-only.
   `IsNarrowingRequest` and `ShouldClearBroadInspectionLock` in
   [internal/workspace/manager.go:913-988](internal/workspace/manager.go:913)
   should return `false` for any `mcp:*` tool — external retrieval is
   not workspace narrowing. One-line additions to both switches.
4. **Post-readiness gap-naming.** Do not require `named_gap` /
   `why_not_answering` from MCP tool calls.
   [internal/agent/tools.go:62](internal/agent/tools.go:62)'s
   `applyPostReadinessSchemaRequirements` short-circuits on
   `isPostReadinessGapTool` — leave that check returning `false` for
   `mcp:*`, which it does today (it's an explicit allow-list of
   workspace tools).

**Files.**

- [internal/agent/modes.go](internal/agent/modes.go) — three Mode
  constructors gain `mcp:*` rules.
- [internal/workspace/tool_rules.go:94-121](internal/workspace/tool_rules.go:94)
  — extract for `mcp:*`, plus the tool-name → glob fallback in
  `MatchToolRule`.
- [internal/workspace/manager.go:913-988](internal/workspace/manager.go:913)
  — extend the negative cases.
- Tests in
  [internal/workspace/tool_rules_test.go](internal/workspace/tool_rules_test.go)
  and [internal/agent/runner_lane_policy.go](internal/agent/runner_lane_policy.go)
  (or its test sibling) for the new behavior.

**Acceptance.**

- PlanMode + `req.Tool="mcp:exa/web_search_exa"` →
  `EvaluateToolRule` returns `ToolRuleAllow`.
- BuildMode + same → `ToolRuleAsk`.
- BuildMode + user config rule
  `"mcp:exa/web_search_exa": [{"pattern":"go 1.24*","decision":"allow"}]` +
  `req.Args={"query":"go 1.24 release notes"}` → `ToolRuleAllow`.
- Same setup, query `"unrelated"` → falls back through `mcp:*` glob
  → `ToolRuleAsk`.
- `IsNarrowingRequest(req{Tool:"mcp:exa/web_search_exa"})` → `false`.
- `applyLanePreflight` in `conversation_only` lane rejects an
  `mcp:exa/web_search_exa` call with the existing rejection text.

---

### Fix W6 — SSRF guard on the HTTP transport

**Problem.** A model that can call `mcp.exa.ai/mcp` can in principle
also be tricked into pointing the harness at any HTTP URL — but only
if a user adds it to `cfg.MCP`. The model never picks the URL itself
(unlike `web_fetch`, which is the higher-risk feature we are *not*
shipping in this change). Still: prompt-injected web content could
suggest a config edit, and a typo in a config file shouldn't let the
agent hit cloud-metadata.

**Design.** When `StartHTTP` opens its first connection, validate the
URL through a small `internal/mcp/ssrf.go` helper:

1. Scheme: `http` or `https` only.
2. Host class: resolve once; reject loopback, link-local (`169.254.0.0/16`,
   `fe80::/10`), RFC 1918 private, unique-local IPv6 (`fc00::/7`),
   multicast, unspecified.
3. Optional `Web.AllowPrivateNetwork bool` (in the `Workspace` block,
   not on individual MCP entries) — a single CLI-only escape hatch
   for users with corp intranet MCP servers. Off by default.

The DNS resolution is one-shot at startup. We do *not* re-pin the IP
at request time (the existing `Client` only ever talks to the URL it
was started with, so DNS rebind is not a relevant attack here — the
host is fixed in config, not chosen by the model).

If a user ever wants to register an MCP server at
`http://localhost:8765` (a local stdio-bridge or a dev helper), they
flip the override or — better — register that server via stdio.

**Files.**

- `internal/mcp/ssrf.go` (new, ~60 LOC).
- `internal/mcp/ssrf_test.go` (new) — table-driven over the host
  classes.
- [internal/config/config.go:150](internal/config/config.go:150)
  (`Workspace`) — new `AllowPrivateNetwork bool` (default false).

**Acceptance.**

- Table-driven test rejects `http://127.0.0.1`, `http://localhost`,
  `http://169.254.169.254`, `http://10.0.0.1`, `http://192.168.1.1`,
  `http://[::1]`, `http://[fc00::1]`.
- Allows `https://mcp.exa.ai/mcp` and `https://api.openai.com`.
- With `AllowPrivateNetwork: true`, all the rejected cases pass.

**Non-goals.** No outbound proxy support
(`HTTPS_PROXY` / `NO_PROXY`). Std lib `http.DefaultTransport` already
honors these; no extra code needed unless it doesn't work in practice.

---

## 7. Once W1–W6 land, web search and web fetch are config

```jsonc
// config.json — one entry, two tools, no new Go code
{
  "mcp": [
    {
      "name": "exa",
      "url": "https://mcp.exa.ai/mcp",
      "timeout_seconds": 25
      // optional: "headers": {"X-Api-Key": "..."}  if Exa adds auth later
    }
  ]
}
```

The live smoke test (`IDIOM_LIVE_MCP=1 go test -tags=integration ./internal/app/`)
confirms Exa's endpoint exposes **two** tools today:

- `mcp:exa/web_search_exa` — search-the-web (the headline feature)
- `mcp:exa/web_fetch_exa` — URL-as-clean-markdown (covers the
  webfetch use case for free)

Original §13 plan was to register a separate community MCP fetch
server alongside Exa. Empirically, Exa already provides both — the
fetch entry is unnecessary. See §13 for the updated stance.

With `EXA_API_KEY` set in the environment, the user can pass it
through via the URL query string in the same shape OpenCode uses:

```jsonc
{
  "mcp": [
    {
      "name": "exa",
      "url": "https://mcp.exa.ai/mcp?exaApiKey=${env:EXA_API_KEY}"
    }
  ]
}
```

(Env-var interpolation in the URL is **not part of this change** —
deferred to W8 in §14. For now, users who want auth set it in a
header.)

Tools the model sees once Exa registers:

- `mcp:exa/web_search_exa` — web search (the headline feature).
- `mcp:exa/get_code_context_exa` — Exa Code, OpenCode's `codesearch`.

In Mode terms, both inherit the `mcp:*` rule from §6 W5.

---

## 8. Description text and prompt seed

The Exa MCP server publishes its own tool descriptions through
`tools/list`. Those land verbatim in the LLM-facing tool spec via
W4(1). Two adjustments at the harness layer:

1. **Provenance prefix.** Every MCP-sourced description gets
   `[mcp:exa] ` prepended in `BuildToolDefinitions`. This costs a
   handful of tokens and pays them back when the model is choosing
   between local and external tools — it can see at a glance which
   ones leave the workstation.
2. **Year anchor.** OpenCode injects `{{year}}` into `websearch.txt`
   so the model knows what "current" means. Exa's MCP server *also*
   includes this guidance in its own description (we'd see when the
   Bridge enumerates it). If it doesn't, we can re-inject the year as
   a one-line system-prompt addendum from
   [prompts/base.md](prompts/base.md) — but verify the upstream
   description first to avoid duplicate guidance.

A one-paragraph seed for [prompts/base.md](prompts/base.md):

> When the user asks about events, releases, or facts later than your
> training cutoff, or when local discovery cannot answer the question,
> consider `mcp:exa/web_search_exa`. Use local discovery first when
> the answer is in the workspace. External calls cost the user money
> and time; do not use them for questions you can already answer.

---

## 9. Logging

[internal/app/event_stream.go:88-123](internal/app/event_stream.go:88)
already records `tool_request` and `tool_result` for every dispatched
tool. MCP tools land in this same path because they go through
`Manager.Execute`. Two specifics:

1. **Args logging.** When `req.Tool` is `mcp:*`, we log
   `req.Args` (raw JSON) in the `tool_request` event. That's the
   model's full intent and is the right level of fidelity for replay
   and eval. The existing schema doesn't have a slot for raw args; we
   add `args` (string, optional, JSON-encoded) when present.
2. **Secret redaction.** If a user has put an API key in the URL
   query string of an MCP entry, the harness must never log the URL.
   Only the server *name* is in tool calls (e.g. `mcp:exa/...`), not
   the URL — so this is naturally safe. Add a guard in `StartHTTP`'s
   error wrapping: never include the raw URL in error messages, only
   the host. Helper: `mcp.RedactURL(string) string` strips the query
   string before formatting.

---

## 10. Testing strategy

| Layer | Approach |
| --- | --- |
| HTTP transport | `httptest.Server` returning canned SSE bodies; covers happy path, partial frames, malformed JSON, timeout, 4xx/5xx, redirects. |
| SSRF guard | Pure unit, table-driven over the host classes. |
| Registry boot | Boot test with `httptest.Server` standing in as the MCP endpoint; assert `Tools()` contains the expected entries. |
| Workspace dispatch | Inject a fake `mcp.Registry` into the manager via `SetMCPRegistry`; assert the dispatch case fires for `mcp:*`. |
| Permission rules | Extend [internal/workspace/tool_rules_test.go](internal/workspace/tool_rules_test.go) with cases for `mcp:exa/...` matching, glob-fallback, and arg-target extraction. |
| Lane policy | Add `mcp:*` cases to the existing lane-policy tests. |
| End-to-end | One `internal/eval/swe_suite.go` scenario: prompt asking for a recent fact, mocked Exa MCP returning a fixed result string, assert the runner calls the tool and incorporates the answer. |
| Live smoke | Optional, gated by `IDIOM_LIVE_MCP=1`; hits real `mcp.exa.ai/mcp` once with a benign query. Not in CI. |

---

## 11. Build / migration plan

Each step is independently testable, shippable, and dark-launchable
behind the absence of any `cfg.MCP` entry.

1. **Step 1 — `internal/mcp/ssrf.go` + tests.**
   Pure helper, no integration. ~80 LOC.
2. **Step 2 — `internal/mcp/http.go` + `HTTPTransport` tests.**
   ~150 LOC source + ~200 LOC tests. Verifies against `httptest`. The
   stdio path is unchanged.
3. **Step 3 — `MCPServer` config extension** (`URL`, `Headers`) +
   validation + tests.
4. **Step 4 — Boot the registry from `cfg.MCP`** in
   [cmd/idiom/main.go](cmd/idiom/main.go); add
   `Manager.SetMCPRegistry`; tear down on shutdown. With `cfg.MCP`
   empty (the default), behavior is unchanged.
5. **Step 5 — `ToolRequest.Args` field** + agent-layer route in
   `parseToolCall` for `mcp:*` calls. No tools registered yet, so no
   visible change.
6. **Step 6 — `BuildToolDefinitions` / `ToolManifestTools` fan-in.**
   With no MCP server registered, output is identical to today.
7. **Step 7 — `Manager.Execute` `mcp:*` case** + permission rule
   extensions in [tool_rules.go:114](internal/workspace/tool_rules.go:114).
   Still dark — nothing registers an MCP server.
8. **Step 8 — Mode rules.** Add `mcp:*` entries to all three Modes.
   Still dark.
9. **Step 9 — Lane policy negative-list extensions.** Still dark.
10. **Step 10 — Light it up.** Add Exa to `config.example.json` (not
    `config.json` — that's the user's). Update docs. Add the eval
    scenario.

Steps 1–9 land net-zero behavior change. Step 10 is where the user
actually opts in.

Estimated total: ~400 LOC source + ~500 LOC tests, spread across
~10 small PRs.

---

## 12. Risk and rollback

| Risk | Likelihood | Mitigation |
| --- | --- | --- |
| Exa endpoint changes shape | Low | Single transport file, easy to update. SSE parser is generic. |
| Goroutine leak in HTTP transport on error | Medium | Explicit `closed` channel + `goleak` test in CI. |
| MCP server announces a tool whose name collides with a built-in | Low | Mandatory `mcp:` prefix on the tool name precludes this — `mcp:` is reserved. |
| MCP server returns megabytes of content | Medium | Existing per-tool truncation in [internal/agent/tools.go](internal/agent/tools.go) `FormatToolResultForModel` already caps. Add a cap inside `executeMCP` matching `Workspace.MaxOutputBytes`. |
| Prompt injection via fetched MCP content | Medium | Tool result is wrapped in the same `<tool_output>` framing as every other tool. Optional: prepend `[untrusted: external]` to MCP outputs. |
| Cost surprise from Exa | Medium | "ask" default in BuildMode; per-turn ceiling proposal in W7. |
| User pastes API key into `config.json` and commits it | Low | Document `${env:NAME}` interpolation as the supported pattern (W8). `config.json` is already in `.gitignore`. |

**Rollback path.** Remove the Exa entry from `cfg.MCP`. The tools
disappear from `BuildToolDefinitions`; the model can't call them.

---

## 13. `web_fetch` — covered by Exa, no separate server needed

Original plan (decision 5): register a stdio community MCP fetch
server (`@modelcontextprotocol/server-fetch`) alongside Exa.

What we actually found at integration-test time:

1. The npm package `@modelcontextprotocol/server-fetch` doesn't exist —
   the reference fetch server is a **Python** package (`mcp-server-fetch`
   on PyPI), not a Node one.
2. **Exa's hosted MCP endpoint already exposes a fetch tool**:
   `mcp:exa/web_fetch_exa`, described upstream as "Read a webpage's
   full content as clean markdown. Use after web_search_exa…".

So the right move is just **don't register a separate fetch server**.
Exa's single `cfg.MCP` entry gives us search + fetch with one HTTP
endpoint, no Node runtime required, no second SSRF surface, and the
same per-Mode rules apply uniformly.

OpenCode's standalone Go-internal `webfetch` (with HTML→Markdown,
Cloudflare retry, image-as-attachment) is genuinely a different
capability, but neither qwen-bubbles nor Exa's fetch tool is
attempting to replicate every feature. If the eval evidence later
shows Exa's fetch is inadequate (paywalls, JS-rendered content,
non-HTML), W11 in §15 covers shipping a Go fetcher then.

What we get for free:

- HTML→Markdown conversion (handled by the upstream server).
- A single `fetch` tool with `url`, `max_length`, `start_index`,
  `raw` parameters.
- `robots.txt` honored by default.
- The same per-Mode permission rules from §6 W5 (`mcp:fetch/*` keyed
  the same as Exa).
- Stdio transport — no dependency on W1.

What we accept by going this route:

- A Node runtime on the user's machine (`npx` invocation). Acceptable
  given that OpenCode itself ships as a Node package; users already
  have it.
- The upstream server's choice of dependencies and update cadence. We
  pin to a known-good version in [config.example.json](config.example.json)
  and revisit if breakage hits the eval suite.
- Less control over Cloudflare-bot-detection retry behavior. The
  upstream server's behavior is good enough for the workloads we
  expect; we pull our own fetcher only if the eval evidence demands
  it.

This design is reversible — if we later find the community server
inadequate, we can ship a Go fetcher as a sibling MCP server (or
in-process tool) without touching anything else this change adds.

---

## 14. Decisions — resolved

| # | Decision | Choice |
| --- | --- | --- |
| 1 | Path B (wire MCP through; search via config) | **Confirmed** |
| 2 | Argument bridging | **Add `ToolRequest.Args json.RawMessage`** |
| 3 | PlanMode default for `mcp:*` | **`allow`** |
| 4 | SSRF guard | **On by default + `AllowPrivateNetwork` escape hatch** |
| 5 | `web_fetch` capability | **Via a community MCP fetch server** (e.g. `@modelcontextprotocol/server-fetch`); no new Go code |
| 6 | DNS resolution in SSRF guard | **One-shot at `StartHTTP`**, no per-request re-pin |
| 7 | `{{year}}` injection | **Decide after first live look** at Exa's `tools/list` output — skip if upstream description already anchors the year |

Decision 5 changes the shape of §13 — `web_fetch` is no longer
deferred; it joins the same `cfg.MCP` array as Exa. See the updated
§7 example.

---

## 15. Out-of-scope follow-ups

Track separately, do not bundle:

- **W7 — Per-session $ budget cap.** Counter on Exa-class calls,
  hard reject past the cap, surfaced in TUI.
- **W8 — `${env:NAME}` interpolation in config strings.** Useful
  beyond MCP — applies to LLM API keys, log paths, etc.
- **W9 — Streamable HTTP MCP transport (spec 2025-03-26).** Required
  if we ever target a server that pushes long-lived events.
- **W10 — Hot-reload of `cfg.MCP`.** OpenCode doesn't do this; we
  shouldn't until pain shows up.
- **W11 — Bespoke Go web fetcher.** Only if the community MCP fetch
  server proves inadequate in the eval suite (see §13).
- **W12 — Citation extraction.** Parse Exa's text response for URLs
  and attach as structured citations the TUI can render clickable.

---

## 16. References

- OpenCode `websearch` source: `packages/opencode/src/tool/websearch.ts`
- OpenCode Exa MCP client: `packages/opencode/src/tool/mcp-exa.ts`
- OpenCode tool registry / gating: `packages/opencode/src/tool/registry.ts:280`
- OpenCode permission evaluator: `packages/opencode/src/permission/evaluate.ts`
- OpenCode public docs: `packages/web/src/content/docs/tools.mdx:255-285`
- OpenCode env flag: `packages/core/src/flag/flag.ts:67`
- qwen-bubbles MCP scaffolding: [internal/mcp/client.go](internal/mcp/client.go), [internal/mcp/registry.go](internal/mcp/registry.go)
- qwen-bubbles tool schemas: [internal/workspace/tool_schemas.go:22](internal/workspace/tool_schemas.go:22)
- qwen-bubbles dispatch: [internal/workspace/manager.go:359](internal/workspace/manager.go:359)
- qwen-bubbles Mode rules: [internal/agent/modes.go](internal/agent/modes.go)
- qwen-bubbles tool-rule evaluator: [internal/workspace/tool_rules.go](internal/workspace/tool_rules.go)
- qwen-bubbles lane policy: [internal/agent/runner_lane_policy.go](internal/agent/runner_lane_policy.go)
- Precedent design-doc style: [docs/opencode-fixes-design.md](docs/opencode-fixes-design.md)
- Original Fix 8 (the MCP scaffold this completes): [docs/opencode-fixes-design.md](docs/opencode-fixes-design.md) §8
