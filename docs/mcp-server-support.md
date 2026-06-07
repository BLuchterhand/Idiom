# MCP Server Support — Design Doc

Status: draft
Owner: tbd
Related: [internal/capabilities/mcp/client.go](../internal/capabilities/mcp/client.go), [internal/capabilities/workspace/tool_manifest.go](../internal/capabilities/workspace/tool_manifest.go), [archive/2026-05-historical/opencode-fixes-design.md](archive/2026-05-historical/opencode-fixes-design.md) §8

## Goals

1. Expose Idiom's workspace, language-semantic, discovery, audit, and harness tools as a **Model Context Protocol server** so external agents (Claude Code, Codex CLI, opencode, Continue, Cursor, custom MCP clients) can call them without embedding Idiom's Go runtime.
2. Reuse the existing `workspace.Manager.Execute` dispatch path so the model-facing surface, the `idiom tool` CLI, and the MCP server speak to the same tool implementations, security gates, and telemetry.
3. Ship two transports: **stdio** (default, spawn-per-client) and **streamable HTTP** (long-running daemon for shared/team use).
4. Provide a **profile-gated** tool surface so the same binary can serve a read-only discovery profile to one client and a full edit/shell profile to another.

## Non-Goals

- Proxying Idiom's MCP **client** registry through to MCP server clients (re-exposing mounted `mcp:<server>/<tool>` calls). Out of scope until single-side server support is stable.
- Implementing **sampling** (server-initiated LLM calls back to the client). Idiom's tools are deterministic; no tool needs the client's model.
- Full **OAuth 2.1** authorization server. HTTP transport ships with bearer-token auth only; OAuth is a follow-up if there's enterprise demand.
- Replacing the existing `idiom tool <name>` CLI. The CLI stays as the "one-shot subprocess" call shape for non-MCP integrators.

## Background

### What's already in the repo

Idiom is already an MCP **client**: [internal/capabilities/mcp/client.go](../internal/capabilities/mcp/client.go) implements JSON-RPC 2.0 over stdio + HTTP, [registry.go](../internal/capabilities/mcp/registry.go) namespaces remote tools as `mcp:<server>/<tool>`, and [internal/capabilities/workspace/manager.go:1158](../internal/capabilities/workspace/manager.go) dispatches calls when the prefix matches. Protocol revision negotiated is `2024-11-05` ([client.go:24](../internal/capabilities/mcp/client.go)).

The tool surface is concentrated in two places:

- [`workspace.Manager.Execute`](../internal/capabilities/workspace/manager.go) — single dispatch entrypoint for ~130 tools across workspace ops, language-semantic discovery (Go/C++/Python/TS/Rust/Java), structural and dependency analysis, history/blame, security scanners, harness audit/feedback, and write/shell. All gating (`EnableShell`, `EnableWrites`, `ToolRules`) is already applied here.
- [`workspace.BuildToolManifest`](../internal/capabilities/workspace/tool_manifest.go) — hand-written manifest of every tool with example request JSON and a one-line purpose. This is the source of truth for prompt rendering and `idiom tool list` output.

The closest existing analogue to an MCP `tools/call` is `idiom tool <name> --input '<json>'` ([internal/toolcmd/toolcmd.go](../internal/toolcmd/toolcmd.go), `RunWithTelemetry`). It loads workspace config, constructs a `ToolRequest`, runs `Manager.Execute`, serializes the `ToolResult` as JSON, and emits telemetry. The MCP server is essentially a long-running JSON-RPC frontend over the same path.

### What's missing

- No long-running HTTP/RPC endpoint Idiom exposes itself (the llama server controller manages a *child* process; that's the only "server" pattern in tree).
- No code path that speaks the JSON-RPC **server** half — `internal/capabilities/mcp/client.go` only writes requests and reads responses.
- No mechanism to convert manifest entries' example JSON into JSON-Schema. MCP `tools/list` requires `inputSchema` per tool.
- No explicit workspace path-containment check (paths are resolved relative to `workspace.Root`/`Cwd` but never validated as inside it). Acceptable for a local agent harness; **not acceptable** for an MCP server bound to `0.0.0.0`.

## Protocol Target

- **JSON-RPC 2.0** envelope, newline-delimited frames on stdio, JSON over HTTP/streamable for the HTTP transport.
- **Protocol revision**: negotiate `2025-06-18` (latest stable); fall back to `2024-11-05` if the client requests it (matches the existing client constant).
- **Transports**:
  - **stdio** — default. Client spawns `idiom mcp serve --stdio`, exchanges frames over the child's stdio. Inherits process-level trust (same UID).
  - **Streamable HTTP** — `idiom mcp serve --http :8765`. POST `/mcp` for request/response, optional GET upgrade for server-initiated streams (logging, progress). Bearer token from `--auth-token` / `IDIOM_MCP_TOKEN`.
  - **Not** the legacy HTTP+SSE transport. The 2025-06-18 spec deprecated it.
- **Capabilities advertised**:
  - `tools` — full tool surface (profile-gated).
  - `resources` — read-only views over discovery packets, audit reports, ranker state, debug logs (see §Resources).
  - `prompts` — `prompts/profiles/*.md` exposed as named prompt templates.
  - `logging` — Idiom telemetry events forwarded as `notifications/message` (rate-limited).
  - `roots` — accept client-declared roots and constrain `workspace.Root` to them.

## Architecture

### Package layout

```
internal/capabilities/mcp/
  client.go         (existing — unchanged)
  registry.go       (existing — unchanged)
  http.go, ssrf.go  (existing — unchanged)
  server/
    server.go       Server struct, capability advertisement, request router
    schema.go       Manifest -> inputSchema conversion
    transport.go    StdioServerTransport, HTTPServerTransport
    auth.go         Bearer-token middleware, IP allowlist
    resources.go    Resource provider (.idiom/, logs/, prompts/)
    prompts.go      Prompt provider (prompts/profiles/*.md)
    profile.go      Tool profile filtering (read-only / discovery / full)
    telemetry.go    Bridge from tool events -> MCP logging notifications

cmd/idiom/
  mcp_serve.go      Subcommand: idiom mcp serve [--stdio|--http addr]
```

The client side stays untouched. `internal/capabilities/mcp/server/` is independent and has no import dependency back into `internal/capabilities/mcp/client.go` — they share only the JSON-RPC envelope types, which we lift into a small `internal/capabilities/mcp/protocol` package as part of this work.

### Request lifecycle

```
client                     transport            server                  Manager
  |--- initialize -------->|--- decode -------->|
  |                        |                    |--- negotiate caps -->|
  |<-- result --------------|<-- encode ---------|
  |--- notifications/initialized ------------->|
  |--- tools/list -------->|                    |--- BuildToolManifest -->|
  |<-- tools[] -------------|<------ schema ---|
  |--- tools/call -------->|                    |
  |                        |                    |--- profile gate ----|
  |                        |                    |--- ToolRequest ---->|--- Execute
  |                        |                    |                    |    EnableWrites/Shell
  |                        |                    |                    |    ToolRules
  |                        |                    |<--- ToolResult ----|
  |                        |                    |--- telemetry log -->|
  |<-- result --------------|<-- content[] ------|
```

Crucially: the gate stack inside `Manager.Execute` runs unchanged. The MCP server does **not** add a parallel permission system. A profile filter runs *before* dispatch (so denied tools never appear in `tools/list`), but for any tool that is exposed, the same `EnableWrites` / `EnableShell` / `ToolRules` checks fire as for the model and the CLI.

### Concurrency model

- stdio: one client per process, requests serialized through a single dispatcher goroutine. Long-running tools (semgrep, joern, large `discovery_query`) honor request-level cancellation via the JSON-RPC `notifications/cancelled` message.
- HTTP: bounded worker pool (`--max-concurrent`, default 4) over a shared `Manager`. `Manager.Execute` is already safe for concurrent calls; tools that mutate workspace state (write tools, ranker updates) take per-tool locks at the manager level and we do not need a global lock at the server layer.

## Tool Surface Mapping

### From `BuildToolManifest` to MCP `tools/list`

Each `ToolManifestEntry` has:
- `Tool` string — becomes MCP `name`.
- `Purpose` string — becomes MCP `description` (one-liner).
- `Request` string — example JSON; the *source* for the schema, not the schema itself.

Two-step generation:

1. **Static codegen** (`go generate ./internal/capabilities/mcp/server/...`): parse each manifest entry's example JSON, infer field names + Go-type-mapped JSON types, emit a base JSON-Schema document per tool with `additionalProperties: false`.
2. **Hand-tuned overrides** (`internal/capabilities/mcp/server/schema_overrides.go`): per-tool patches for fields the example can't capture — enums (`purpose: "impact_analysis"|"function_tracing"|...`), required vs optional, descriptions copied from existing prompt text, and write-tool guard flags.

Generated schemas are checked into the repo so the runtime cost is just a `json.Unmarshal` per `tools/list`. Manifest changes that would alter schemas surface as a generated-file diff.

### Profiles

Tool exposure is gated by named profiles in config. Three ship by default:

| Profile         | Includes                                                                                          | Excludes                                              |
|-----------------|---------------------------------------------------------------------------------------------------|-------------------------------------------------------|
| `discovery`     | All read-only workspace + language-semantic + discovery_query + structural_search + history/blame | Write tools, shell, harness_feedback, semgrep/joern   |
| `discovery+sec` | `discovery` + semgrep_scan + joern_scan + dependency_graph                                        | Write tools, shell                                    |
| `full`          | Every tool the manifest exposes                                                                   | Nothing — caller must also set `EnableWrites`/`EnableShell` |

Profiles are filtering only: a tool that's allowed by the profile but blocked by `EnableShell=false` still fails at dispatch with the same error the CLI emits. Profiles never *grant* — they only narrow.

The default profile for stdio is `discovery`. The default for HTTP is `discovery` and the binary refuses to bind HTTP with `full` unless `--i-know-what-im-doing` or the equivalent config flag is set, with a startup log line naming the risk.

### Schema notes

- `path` fields advertise `format: "uri-reference"` and a description that names the workspace root. Servers reject absolute paths outside the active root **at the server layer**, before `Manager.resolvePath` runs, because the manager's behavior is "resolve relative to root" not "reject if outside" — a difference that matters once the call is coming from a non-trusted client over HTTP.
- The raw `Args json.RawMessage` field on `ToolRequest` ([manager.go:82](../internal/capabilities/workspace/manager.go)) stays in place — it's the carrier for tool-specific args that don't appear as named struct fields. The server unmarshals MCP `arguments` into a `map[string]any`, then re-marshals into `ToolRequest` with `Args` set to the original bytes.

## Resources

Resources are read-only URIs the client can list and fetch. Idiom has natural candidates:

| URI scheme                          | Source                                                | Use case                                                                              |
|-------------------------------------|-------------------------------------------------------|---------------------------------------------------------------------------------------|
| `idiom://discovery/queries/<id>`    | `.idiom/discovery/queries/*.json`                     | Inspect a prior `discovery_query` evidence packet without re-running it.              |
| `idiom://audits/watch/<id>`         | `.idiom/audits/watch/*.json`                          | Pull review packets emitted by `idiom audit watch`.                                   |
| `idiom://ranker/state`              | `.idiom/ranker/weights.json` + `learning_log.jsonl`   | Surface ranker learning state to a client building its own prioritization.            |
| `idiom://logs/conversations/<id>`   | `logs/conversations/<id>.jsonl`                       | Replay-grade access to prior session logs (subject to redaction).                     |
| `idiom://context/packs/<id>`        | Saved `context_pack` outputs                          | Reuse a context view across clients.                                                  |

Resources reuse the same path-containment rule as tools: the URI must resolve under `workspace.Root`. Subscriptions (`resources/subscribe`) for the audit watch directory are an obvious follow-up but not in v1.

## Prompts

`prompts/profiles/*.md` becomes the prompt catalog. Each file is exposed with its filename (sans `.md`) as the prompt name, the first heading as the description, and the body as the templated content. Argument extraction (e.g. `{{task}}`) is opt-in via a fenced YAML frontmatter block in the prompt file declaring the argument list — same pattern Cursor and Continue use.

This is mostly a freebie: the files exist, they're already curated for model behavior, and exposing them lets external agents inherit Idiom's prompt library without copying it.

## Security & Trust Model

### stdio

stdio inherits the spawning process's UID. We treat it the same as the existing `idiom tool` CLI: full workspace access, `EnableWrites`/`EnableShell` honored exactly as configured. No additional auth.

### HTTP

The HTTP transport is the dangerous one. Defaults:

- Bind to `127.0.0.1` only. Refuse `0.0.0.0` unless `--bind-public` is set.
- Bearer token required. `idiom mcp serve --http :8765 --auth-token-file ~/.idiom/mcp-token`. Tokens are compared with constant-time comparison.
- Per-token profile binding: `~/.idiom/mcp-tokens.json` maps tokens to `{profile, max_concurrent, rate_limit_qps}`. Allows handing one token to a CI runner (`discovery` profile, low rate) and another to a trusted local client (`full`, no limit).
- IP allowlist (`--allow-cidr`) for defense in depth.
- Path containment: the server validates every incoming path arg against `workspace.Root` *before* `Manager.Execute`. Symlinks resolved with `filepath.EvalSymlinks` and re-checked. Documented as the reason the existing implicit containment isn't sufficient at the MCP boundary.
- SSRF guards from [internal/capabilities/mcp/ssrf.go](../internal/capabilities/mcp/ssrf.go) reused for any tool that performs outbound HTTP (semgrep registry fetches, future web tools).
- Rate limiting per token, default 60 qps, burst 10.

### Roots

When the client advertises `roots`, the server clamps `workspace.Root` to the intersection. If a client root is outside the configured workspace, the call is rejected with a structured error rather than silently expanding. This means a misconfigured client can narrow the surface but never widen it.

### Tool-specific guards

- `shell`: requires `EnableShell=true` AND profile=`full`. Logs the full command line at INFO. Always emits a telemetry event with `discovery_class=shell` so audit can find unusual usage.
- `edit_file`/`write_file`/`apply_patch`: require `EnableWrites=true` AND the path to resolve under `workspace.Root`. Adds an `allowed_glob` config knob for HTTP clients (e.g. restrict to `**/*.md` for a docs-only token).
- `harness_feedback`: stays open even on `discovery`. It's append-only telemetry; useful for letting external agents report harness gaps back into Idiom's audit pipeline.

## Configuration

New top-level config block, additive:

```jsonc
{
  "mcp_server": {
    "enabled": false,
    "stdio": {
      "profile": "discovery"
    },
    "http": {
      "enabled": false,
      "addr": "127.0.0.1:8765",
      "tls": {
        "cert_file": "",
        "key_file": ""
      },
      "auth_token_file": "~/.idiom/mcp-token",
      "tokens_file": "~/.idiom/mcp-tokens.json",
      "allow_cidr": ["127.0.0.1/32"],
      "max_concurrent": 4,
      "rate_limit_qps": 60,
      "rate_limit_burst": 10,
      "default_profile": "discovery"
    },
    "resources": {
      "enable_discovery_packets": true,
      "enable_audit_watch": true,
      "enable_ranker_state": false,
      "enable_conversation_logs": false
    },
    "prompts": {
      "enable_profile_prompts": true
    },
    "logging": {
      "forward_telemetry": true,
      "rate_limit_per_minute": 600
    }
  }
}
```

CLI flags mirror the keys for one-off invocations:
```
idiom mcp serve --stdio --profile discovery
idiom mcp serve --http :8765 --profile discovery --auth-token-file ~/.idiom/mcp-token
idiom mcp serve --http :8765 --profile full --bind-public --i-know-what-im-doing
```

## Telemetry & Audit

Every MCP request flows through the same `LogDirectToolRequest`/`LogDirectToolResult` shape used by the CLI ([internal/toolcmd/toolcmd.go](../internal/toolcmd/toolcmd.go)) but with two added fields:

- `mcp.client_name` — from the `clientInfo` block in `initialize`.
- `mcp.transport` — `stdio` or `http`.

These are the *only* fields that distinguish MCP-driven tool calls from CLI-driven ones in `logs/telemetry`. `harness_audit` and `audit watch` consume the same files and pick up MCP traffic without changes.

For HTTP transport we add a thin per-token counter that `harness_audit` can summarize ("token `…3f9a` ran 412 calls today, 380 discovery_query, 12 read_file, 0 errors"). This is the basis for any future billing or quota model.

`logging` capability: telemetry events emit MCP `notifications/message` with severity mapped from the existing `severity` field on harness_feedback events and INFO for routine tool results. Rate-limited per the config knob to avoid overwhelming clients during an `idiom audit watch` burst.

## Implementation Plan

Phased so each step is independently shippable.

**Phase 1 — protocol + stdio (1-2 weeks).**
- Lift JSON-RPC envelope types into `internal/capabilities/mcp/protocol`.
- `internal/capabilities/mcp/server/server.go` with initialize/tools/list/tools/call. Tool dispatch through `Manager.Execute`. No resources, no prompts, no logging. Profile filter implemented but only the `discovery` profile shipped.
- Stdio transport.
- `idiom mcp serve --stdio` CLI.
- Schema codegen for the 30 most-used tools (read_file, search_files, search_content, list_files, file_stats, all `*_overview`/`*_find_symbol`/`*_read_symbol`/`*_references` per language, discovery_query, structural_search, dependency_graph, history_*, ctags_symbols, blame_range). Rest fall back to a permissive schema with `additionalProperties: true` until manually tuned.
- Tests: a Go-side fake client driving the server through stdin/stdout pipes, asserting `tools/list` shape and a few canonical `tools/call` round-trips.

**Phase 2 — full schema, profiles, write/shell gating (1 week).**
- Codegen + overrides for all 130 tools.
- `discovery+sec` and `full` profiles.
- Write/shell gating exercised via integration test: profile=`discovery` rejects `edit_file` from `tools/list`; profile=`full` + `EnableWrites=false` rejects at dispatch with an error matching the CLI's.
- Path-containment middleware. Tests covering symlink escape attempts.

**Phase 3 — HTTP transport (1 week).**
- Streamable HTTP per spec 2025-06-18.
- Bearer-token auth, per-token profiles, rate limiting, IP allowlist.
- Graceful shutdown, draining in-flight tool calls up to a deadline.
- TLS support (file-based certs; ACME later if anyone asks).

**Phase 4 — resources + prompts + logging notifications (1 week).**
- Resource provider for the four URI schemes.
- Prompt provider for `prompts/profiles/*.md`.
- `logging` capability + telemetry bridge.

**Phase 5 — observability + docs (3-5 days).**
- Per-token counters in `harness_audit`.
- README section + an MCP client config sample (`claude_desktop_config.json` snippet, opencode snippet).
- Conformance check against the public MCP test suite.

Total: ~5 weeks for one engineer; less if Phase 4 ships piecemeal.

## Open Questions

1. **Resource subscriptions.** The audit watch use case is the obvious one (notify clients when a new review packet lands), but `resources/subscribe` adds non-trivial state. Defer to a v1.1 unless an early adopter asks?
2. **Cancellation propagation into tools.** `Manager.Execute` honors context cancellation, but several language-semantic tools delegate to `gopls`/`clangd` over LSP and we don't currently propagate cancellation through that boundary cleanly. Worth auditing as part of Phase 1 or after.
3. **Multi-workspace.** Today `workspace.Root` is process-global. A long-running HTTP server with multiple clients pointing at different repos either needs (a) one server per workspace, (b) per-token workspace binding, or (c) request-scoped workspace selection. Recommendation: ship (a) for v1 — simple, matches stdio, and team deployments can run a server per repo behind a reverse proxy.
4. **Profile granularity.** Three profiles is the right v1 surface but power users will want per-tool toggles. The existing `workspace.ToolRules` map is the natural place; the question is whether to extend its semantics to also control `tools/list` visibility (currently it only gates dispatch).
5. **Sampling, after all?** Some Idiom flows (architect plan, brief lint) are model-dependent and currently call out to the configured llama/MLX server. An MCP client with its own model could in principle drive these via sampling. Not v1, but worth not painting ourselves out of.
6. **Prompt argument syntax.** Does the codebase already have a templating convention for the `.md` files in `prompts/profiles/`? If so, reuse it; if not, this design picks `{{name}}` arbitrarily and that should be confirmed before the prompt provider lands.

## Appendix A — Schema Mapping Worked Example

`go_read_symbol` manifest entry:

```go
{
  Tool: "go_read_symbol",
  Request: `{"tool":"go_read_symbol","pattern":"Name","path":"relative/file.go"}`,
  Purpose: "read a Go symbol body. ANCHOR FIRST via go_find_symbol/...",
}
```

Generated base schema:

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["pattern", "path"],
  "properties": {
    "pattern": { "type": "string", "description": "symbol name to read" },
    "path":    { "type": "string", "format": "uri-reference",
                 "description": "workspace-relative file path" }
  }
}
```

Hand-tuned override (in `schema_overrides.go`):

```go
"go_read_symbol": override{
  AddProperty("named_gap",      stringSchema("the unresolved claim this read should close")),
  AddProperty("why_not_answering", stringSchema("why current evidence is insufficient")),
  Description("Read a Go symbol body. Anchor first via go_find_symbol or a prior tool result."),
}
```

Final MCP `tools/list` entry:

```json
{
  "name": "go_read_symbol",
  "description": "Read a Go symbol body. Anchor first via go_find_symbol or a prior tool result.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["pattern", "path"],
    "properties": {
      "pattern":          { "type": "string", "description": "symbol name to read" },
      "path":             { "type": "string", "format": "uri-reference",
                            "description": "workspace-relative file path" },
      "named_gap":        { "type": "string", "description": "the unresolved claim this read should close" },
      "why_not_answering":{ "type": "string", "description": "why current evidence is insufficient" }
    }
  }
}
```

## Appendix B — Why Not Just Run `idiom tool` Per Call?

A subprocess-per-call approach (the MCP server shells out to `idiom tool <name>` for each `tools/call`) would be simpler but:

- 300-800 ms cold-start per call (Go binary load + workspace config + manifest build).
- Loses persistent caches: `gopls`/`clangd` LSP sessions, the Go AST index, ranker weights, the explore lane's repo-profile cache. These are precisely the assets that make Idiom's discovery layer fast.
- Can't honor MCP cancellation cleanly.
- Telemetry duplication: every call writes its own `logs/telemetry/idiom-<pid>.jsonl`.

Reusing the long-running `Manager` is the only path that preserves Idiom's actual value proposition.
