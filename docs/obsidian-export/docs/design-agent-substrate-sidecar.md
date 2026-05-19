# Agent Harness As LLM Agent Substrate

Status: design guidance

Related docs:

- [harness-design-map.md](harness-design-map.md)
- [harness-strategic-thesis.md](harness-strategic-thesis.md)
- [decisioning-north-star.md](decisioning-north-star.md)
- [design-contract-first-local-autonomy.md](design-contract-first-local-autonomy.md)
- [design-persistent-repository-memory.md](design-persistent-repository-memory.md)
- [design-capability-oriented-tooling.md](design-capability-oriented-tooling.md)
- [design-auditable-planning-handoff.md](design-auditable-planning-handoff.md)
- [design-local-model-operations.md](design-local-model-operations.md)
- [design-eval-audit-learning-flywheel.md](design-eval-audit-learning-flywheel.md)
- [lane-architecture.md](lane-architecture.md)
- [portability-bootstrap-design.md](portability-bootstrap-design.md)
- MCP support design
- [codebase-comprehension-memory-design.md](codebase-comprehension-memory-design.md)
- [architect-packets.md](architect-packets.md)
- Autonomy tracker

## Executive Thesis

Agent Harness should become the reusable substrate that makes LLM agents more
reliable without trying to replace their chat, planning, or editing surfaces.
LLM agents, scripts, CI jobs, and IDE agents should be able to call Agent
Harness for repository facts, scoped context packets, semantic navigation,
verification evidence, audit packets, and policy-shaped workspace operations
through stable CLI and MCP transports.

The product boundary stays the same as the project thesis:

- The LLM agent owns strategy.
- The harness owns contracts.

As a substrate, Agent Harness does not tell an LLM agent which next step to take.
It answers capability-oriented questions with evidence: findings, paths,
symbols, counts, freshness, diagnostics, verification output, uncertainty, and
contract state. The LLM agent decides whether to inspect more, edit, run a
test, ask the user, or stop. Agent Harness decides whether the requested action is
inside the declared permission, freshness, path, protocol, verification, and
budget contracts.

This is differentiated from building another terminal pair programmer. LLM
agents already cover the mature terminal-editing shape: repository maps, chat
modes, scripting, lint/test repair, permissions, subagents, hooks, MCP,
non-interactive modes, and many model backends. Agent Harness's sharper role is
below those visible surfaces: persistent repository memory,
capability-oriented tools, contract-first autonomy, and an
eval/audit/learning flywheel that any LLM agent can reuse.

## Why This Is Differentiated

Most coding agents bundle a model, a chat interface, a filesystem editor, a
shell, and a small amount of repository context. That makes them useful, but it
also means every agent has to rediscover the same repository facts and rebuild
the same safety discipline in its own prompt and tool loop.

Agent Harness should instead be the reusable substrate under multiple agents:

- Repository memory: persistent graph facts, summaries, runtime traces, ranker
  weights, cache rows, and evidence packets survive across sessions and
  callers.
- Capability-oriented tooling: callers ask for source sanity, impact, callers,
  definitions, context packs, history, blame, dependency shape, or verification
  evidence without choosing a language-specific implementation as strategy.
- Contract-first autonomy: permissions, protected paths, required paths,
  verification requirements, freshness state, and completion/readiness are
  typed host contracts, not repeated prompt wishes.
- Audit and replay: tool calls, cache hits, stale rows, backend availability,
  verification, permission failures, and loop behavior can be summarized by
  `harness_audit` and turned into improvements.
- LLM-agent neutrality: LLM agents and shell scripts can call the same substrate
  without adopting Agent Harness's TUI or runner.

The research direction supports this boundary. SWE-agent's 2024 arXiv preprint
argues that agent-computer interface design changes software-agent performance,
including editing, navigation, and test execution
(https://arxiv.org/abs/2405.15793). CodePlan's Microsoft Research publication
page frames repository-level coding as a planning problem over dependency and
may-impact analysis rather than a single prompt over a flat file set
(https://www.microsoft.com/en-us/research/publication/codeplan-repository-level-coding-using-llms-and-planning/).
RepoGraph's 2024 arXiv preprint reports that repository-level graph structure
can improve agents across SWE-bench-style systems
(https://arxiv.org/abs/2410.14684). Codebase-Memory's 2026 arXiv preprint
reports a persistent Tree-Sitter knowledge graph exposed by MCP with lower token
and tool-call cost than repeated file exploration while retaining most answer
quality (https://arxiv.org/abs/2603.27277). SWE-chat's 2026 arXiv preprint trace
study is a warning that real coding-agent usage needs evidence from natural
workflows, not only curated benchmarks: it reports frequent user pushback and
only partial survival of agent-produced code into commits
(https://arxiv.org/abs/2604.20779).

## Target Users And Workflows

The first target user is a developer or platform team that wants LLM agents to
share a stronger substrate than raw grep, shell, and repeated context
explanation. They should be able to install Agent Harness once per workspace,
index the repository, and give any approved LLM agent a small set of
high-value commands or MCP tools.

Primary workflows:

- "Before editing, localize the task": an LLM agent calls
  `context_pack`, `context_query`, `context_zoom`, or `context_impact` to get a
  scoped evidence packet instead of wandering through files.
- "Use semantic tools from any LLM agent": an LLM agent can call Agent Harness
  as a tool server for Go, C/C++, Python, TypeScript, Rust, Java, generic ctags,
  structural search, dependency graphs, history, blame, Semgrep, and Joern.
- "Attach proof to a plan": a planner calls Agent Harness to produce a context packet,
  freshness report, candidate scope, verification command list, and handoff
  contract before asking any model to edit.
- "Run safe automation": CI or local scripts call `harness tool` with explicit JSON
  and check exit codes, without opening a chat session or local LLM.
- "Audit the agent substrate": after a run, a human or agent calls
  `harness_audit` to see tool-surface drift, backend gaps, cache state, stale
  traces, and behavior findings.
- "Use Agent Harness without Agent Harness's model loop": an LLM agent keeps its own strategy
  loop while Agent Harness supplies repository memory and deterministic contracts.

Secondary users include platform engineers who want a local policy and evidence
service for multiple LLM agents, teams evaluating agents on shared repos, and
agent authors who want repository graph tools over a stable CLI or MCP boundary.

## Sidecar Surface

The sidecar surface should expose `harness tool <tool-name> --input '<json>'`
plus stdin JSON for CLI callers, and an MCP server for clients that prefer tool
discovery and JSON-RPC lifecycle negotiation. `harness_audit` should verify the
manifest/schema/tool-surface relationship, and `context_status` should report
index freshness, language coverage, graph size, stale paths, and backend
availability without leaking raw source.

The active tool families are:

- State and filesystem: `task_ledger`, `task`, `pwd`, `list_files`,
  `search_files`, `search_content`, `read_file`, `file_stats`.
- Context graph and memory: `context_index`, `context_status`,
  `context_query`, `context_zoom`, `context_pack`, `context_impact`,
  `context_trace_pack`, `discovery_query`.
- Language and structure: `code_navigate`, `source_sanity`,
  `structural_search`, `dependency_graph`, `ctags_symbols`, and language
  families for Go, C/C++, Python, TypeScript/JavaScript, Rust, and Java.
- History and security: `history_range`, `history_symbol`, `blame_range`,
  `semgrep_scan`, `joern_scan`.
- Audit and learning: `harness_audit`, `harness_feedback`.
- Git and execution: `git_status`, `git_diff`, `shell`.
- Writes: `mkdir`, `edit_file`, `write_file`, `append_file`, `apply_patch`,
  `delegate_edit`.

The important sidecar claim is not the raw number of tools. It is that these
tools can be called outside an Agent Harness inference session and still reuse the same
workspace manager, manifest, schemas, telemetry, cache, and permission gates
documented in [lane-architecture.md](lane-architecture.md) and
the MCP support design.

### Tool Result Discipline

Every evidence tool should keep a narrow role:

- Report what was found.
- Include paths, symbols, counts, command output, freshness, and uncertainty.
- Distinguish exact facts from fallbacks and inferred relationships.
- Avoid prescribing the caller's next tool call.

This preserves the north-star split. An LLM agent can ask "what calls this"
or "what tests cover this" and receive evidence. It should not receive "now run
tool X" unless a hard contract is being enforced or a command failed because a
specific typed precondition was missing.

## Stable Sidecar API Contracts

The stable contract should be defined at the capability/envelope layer, not at
the incidental CLI formatting layer.

### Request Contract

All sidecar transports should support this conceptual request:

```json
{
  "api_version": "harness.tool.v1",
  "tool": "context_pack",
  "arguments": {
    "task": "localize auth retry handling",
    "mode": "plan",
    "limit": 8
  },
  "workspace": {
    "root": "/abs/path/to/repo",
    "cwd": "/abs/path/to/repo"
  },
  "contract": {
    "profile": "discovery",
    "allowed_paths": ["."],
    "forbidden_paths": [".env", ".git"],
    "required_freshness": "read_through"
  },
  "trace": {
    "caller": "llm-agent",
    "session_id": "optional-agent-session-id",
    "correlation_id": "optional-call-id"
  }
}
```

The CLI can continue to accept the compact form:

```bash
harness tool context_pack --input '{"task":"localize auth retry handling","mode":"plan","limit":8}'
```

The stable internal interpretation should be:

- `tool`: required stable tool name.
- `arguments`: tool-specific JSON object validated against the generated schema.
- `workspace`: optional override, never allowed to widen configured containment.
- `contract`: optional narrowing layer for profiles, path scope, freshness,
  write enablement, shell enablement, and timeout.
- `trace`: caller metadata for audit and cache attribution.

### Response Contract

The active CLI prints a JSON object with `Tool`, `Summary`, `Output`, and
`Error`. That can remain as the compatibility envelope. The next stable
contract should add a structured v1 envelope that preserves machine-readable
fields without requiring callers to parse `Output` strings:

```json
{
  "api_version": "harness.tool.v1",
  "tool": "context_pack",
  "ok": true,
  "summary": "Context pack wrote .harness/comprehension/views/abc.json",
  "output": {
    "pack_id": "abc",
    "fresh": true,
    "candidate_files": []
  },
  "diagnostics": [],
  "freshness": {
    "fresh_for_scope": true,
    "global_fresh": true,
    "index_id": "eedd97186d64ffe4"
  },
  "uncertainty": [],
  "audit": {
    "correlation_id": "optional-call-id",
    "telemetry_path": "logs/telemetry/..."
  }
}
```

Errors should be structured by class:

- `validation_error`: malformed JSON, schema mismatch, unknown enum, missing
  required field.
- `permission_denied`: profile, path, shell, write, network, or protected-path
  contract violation.
- `freshness_blocked`: caller required fresh evidence but the index could not be
  refreshed.
- `backend_unavailable`: semantic backend missing and no acceptable fallback was
  allowed.
- `tool_failed`: tool ran but returned failure evidence.
- `internal_error`: unexpected host failure.

Validation and permission errors are contract results, not model strategy
feedback. They should say what invariant blocked the call and which safe
alternatives remain in capability terms, such as "discovery profile allows
`context_pack`, `context_query`, and read-only semantic tools; write tools are
not exposed."

## JSON Schema And Versioning Plan

Agent Harness should publish schemas for both the tool envelope and every tool's
arguments.

Versioning rules:

- Use `harness.tool.v1` for the stable sidecar envelope.
- Use non-public schema IDs such as `urn:harness:schema:tools:context_pack.v1`;
  do not publish placeholder `https://` URLs that look externally resolvable.
- Backward-compatible additions are allowed only for optional fields.
- Removing fields, changing field meaning, tightening required fields, or
  changing output type requires a new major version.
- Tool names are stable. If a tool is superseded, keep an alias with a
  deprecation field until the replacement has a documented migration window.
- Every response includes `api_version`, `tool`, `ok`, `summary`, and either
  `output` or `error`.
- Every schema declares `additionalProperties: false` unless the tool explicitly
  needs an open metadata map.
- Every field that carries a path states whether it is repo-relative or
  absolute, and path arguments are normalized and checked against workspace
  containment before dispatch.

The source of truth should converge on the existing manifest/schema/audit path.
`harness_audit` detects manifest/schema drift. The design map should
graduate that into generated schemas checked into the project, a `harness tool
schema <name>` command, and an MCP `tools/list` implementation using the same
schema documents.

## MCP Server Options

MCP is the natural second transport because LLM agents,
editors, and custom clients understand MCP-style tool discovery. The
MCP specification defines JSON-RPC 2.0, lifecycle negotiation, and server
features such as tools, resources, and prompts
(https://modelcontextprotocol.io/specification/). Its
standard transports are stdio and Streamable HTTP
(https://modelcontextprotocol.io/specification/).

Agent Harness should define both options in the MCP support design:

- `harness mcp serve --stdio --profile discovery`: default local client mode.
  The client launches Agent Harness as a subprocess. This mirrors the trust model of
  `harness tool`.
- `harness mcp serve --http 127.0.0.1:8765 --profile discovery`: local daemon mode
  for clients that prefer Streamable HTTP or need a shared process.
- `harness mcp serve --http 127.0.0.1:8765 --profile full`: opt-in trusted local
  mode only, with write/shell gates still enforced by workspace config.

MCP feature mapping:

- Tools: expose profile-filtered Agent Harness tools from the same manifest and schemas
  as `harness tool`.
- Resources: expose read-only URIs for context packs, audit reports, ranker
  state, selected logs, and discovery artifacts.
- Prompts: expose reusable prompt templates only when they are generic and
  contract-aligned. Do not make prompts the primary integration contract.
- Logging: forward telemetry summaries where clients support MCP logging.
- Roots: clamp the active workspace to the client-declared root intersection.

For Streamable HTTP, the MCP spec warns servers to validate `Origin`, bind local
servers to localhost, and use authentication to reduce DNS rebinding and local
server abuse (https://modelcontextprotocol.io/specification/).
Those requirements should be first-class acceptance criteria, not follow-up
hardening.

## LLM Agent Integration Examples

### Configured LLM Agents

Many LLM agents support project and user configuration, MCP server entries,
approval policies, sandbox modes, permission profiles, and non-interactive
execution for scripts or CI. Agent Harness should integrate with those surfaces
through stable files, CLI envelopes, MCP tools, and least-privilege profiles
rather than through model-specific prompt conventions.

Direct CLI sidecar use:

```bash
harness tool context_pack --input '{"task":"find the auth retry code","mode":"plan","limit":8}' \
  > /tmp/harness-context.json

agent exec --sandbox workspace-write --json - <<'PROMPT'
Use /tmp/harness-context.json as repository evidence.
Implement the minimal auth retry fix. Do not rediscover broadly unless the
packet names a specific gap that blocks the edit.
PROMPT
```

Future MCP config sketch:

```toml
[mcp_servers.harness]
command = "harness"
args = ["mcp", "serve", "--stdio", "--profile", "discovery"]
cwd = "/path/to/repo"
required = true
enabled_tools = ["context_pack", "context_query", "context_zoom", "context_impact", "source_sanity", "harness_audit"]
tool_timeout_sec = 60
```

LLM-agent security guidance is compatible with Agent Harness's sidecar model:
agent runs should keep sandbox and approval controls active, use
least-privilege profiles, and reserve broad filesystem authority for controlled
environments. Agent Harness should not ask users to bypass those controls. It
should work under read-only and workspace-write profiles and report permission
denials clearly.

### Hook-Based LLM Agents

Hook-based LLM agents can carry tool restrictions, model settings, MCP
servers, hooks, isolation, memory, and permission-mode fields. Their hooks
often include lifecycle points such as `PreToolUse`, `PostToolUse`,
`SubagentStart`, `SubagentStop`, `Stop`, `PreCompact`, `PostCompact`, and MCP
elicitation events.

Direct hook sidecar use:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "harness tool context_pack --input \"{\\\"task\\\":\\\"$PROMPT\\\",\\\"mode\\\":\\\"plan\\\",\\\"limit\\\":8}\""
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "harness tool source_sanity --input '{\"path\":\".\"}'"
          }
        ]
      }
    ]
  }
}
```

The production version should avoid fragile shell interpolation and use a small
wrapper script that reads an LLM agent's hook JSON from stdin. The design principle is
still useful: the LLM agent owns delegation and planning; Agent Harness supplies context and
verification evidence at hook boundaries.

Future MCP use should expose Agent Harness tools as `mcp__harness__context_pack`,
`mcp__harness__context_query`, and similar names. Hook matchers can use the
client's MCP tool naming convention when the LLM agent exposes one.

### Profile-Based LLM Agents

Profile-based LLM agents can specify prompts, models, step limits,
disabled agents, and permissions. Permission values should map cleanly to
`ask`, `allow`, and `deny`; permission keys gate actions such as read, edit,
shell, task delegation, web search, LSP, and external-directory access.

Recommended pattern:

```json
{
  "agent": {
    "planner": {
      "prompt": "{file:./prompts/planner.txt}",
      "permission": {
        "read": "allow",
        "grep": "allow",
        "bash": "ask",
        "edit": "deny",
        "external_directory": "deny"
      }
    }
  }
}
```

The planner prompt can direct an LLM agent to call:

```bash
harness tool context_pack --input '{"task":"<task>","mode":"plan","limit":8}'
harness tool context_impact --input '{"path":"<path>"}'
harness tool harness_audit --input '{"purpose":"tool_surface"}'
```

The durable integration should be MCP with a discovery profile for planning and
a separate full profile only for trusted edit agents.

### Repo-Map LLM Agents

An LLM agent's repo map sends symbols and key definitions to the model and
ranks the most relevant portions into the token budget. Scriptable agents may
also accept a task message, apply edits, and exit.

Agent Harness should not try to replace an LLM agent's pair-programming loop.
The sidecar use is to provide fresher repository memory, safer contracts,
verification evidence, and audit output that the agent can consume.
One useful sidecar pattern is precomputed, auditable context:

```bash
harness tool context_pack --input '{"task":"add JSON export for invoice totals","mode":"plan","limit":10}' \
  > /tmp/harness-pack.json

llm-agent-cli --message "Use /tmp/harness-pack.json as sidecar evidence. Make the minimal change and run the listed verification." \
  path/to/invoice.py path/to/tests.py
```

Longer term, if an LLM agent or an LLM-agent wrapper consumes MCP tools, Agent Harness's
`context_pack` and `context_query` should complement the repo map with
persistent cache state, freshness, audit, and graph-backed query endpoints.

### Manual Scripts And CI

`harness tool` should remain the lowest-friction integration:

```bash
set -euo pipefail

TASK_FILE="${1:?task file required}"
TASK="$(cat "$TASK_FILE")"

PACK="$(harness tool context_pack --input "{\"task\":$(jq -Rs . <<<"$TASK"),\"mode\":\"plan\",\"limit\":8}")"
echo "$PACK" > .harness/tmp/context-pack.json

harness tool harness_audit --input '{"purpose":"tool_surface"}' > .harness/tmp/audit.json
```

This path is important for CI, local evaluation, and agents that cannot or
should not run an MCP client.

## Security And Permissions Model

The sidecar boundary increases the need for explicit permissions because
multiple agents may call the same binary. Agent Harness should use one permission model
across `harness tool`, MCP stdio, MCP HTTP, local lanes, and future SDKs.

Profiles:

- `discovery`: read-only repository facts, context graph tools, semantic
  queries, source sanity, history, blame, and audit. No writes, no shell by
  default, no network-bearing tools unless explicitly allowed.
- `discovery+sec`: `discovery` plus `semgrep_scan`, `joern_scan`, and other
  heavier security analyzers.
- `verify`: read-only plus configured test/build commands through a constrained
  command allowlist.
- `write`: edit tools allowed inside declared path scope, shell still blocked
  unless separately allowed.
- `full`: all configured tools exposed, still constrained by workspace root,
  protected paths, write enablement, shell enablement, and tool rules.

Invariants:

- Workspace path containment is checked before tool dispatch.
- Protected paths such as `.git`, secrets, configured deny globs, and project
  policy paths are read/write denied according to profile.
- HTTP MCP binds to localhost by default, requires authentication, validates
  `Origin`, and rejects public bind without an explicit danger flag.
- Tool schemas are enforced before execution.
- Tool results distinguish permission denial from tool failure.
- Profile filters narrow the surface but never grant authority that workspace
  config denies.
- Writes carry changed path evidence.
- Verification evidence must occur after relevant writes if a caller asks Agent Harness
  to judge completion.

This mirrors the LLM-agent security posture instead of fighting it: sandbox mode and
approval policy define technical boundaries for model-generated commands, while
Agent Harness profiles define which sidecar capabilities are visible and callable.

## Observability And Audit

Every sidecar call should be attributable:

- caller kind: `cli`, `mcp_stdio`, `mcp_http`, `lane`, `script`.
- caller name: a configured identifier such as `llm-agent-cli`,
  `hooked-agent`, `profile-agent`, `script`, `manual`, or a caller-supplied
  value.
- workspace root and git HEAD.
- tool name, schema version, profile, duration, success/failure, error class.
- paths read, paths written, commands run, backend used, fallback used.
- cache hit/miss/write/stale result for graph-backed tools.
- freshness policy and freshness result.
- redaction status for prompts, args, command output, and logs.

The first consumer is `harness_audit`. It should summarize:

- tool manifest/schema drift.
- backend availability gaps.
- cache row counts and stale/fresh split.
- repeated zero-result searches and low-value loops when visible in logs.
- permission denials and their profile/path causes.
- write/verify ordering failures.
- LLM-agent caller mix and high-volume tools.

The goal is not surveillance for its own sake. It is the eval/audit/learning
flywheel from [harness-strategic-thesis.md](harness-strategic-thesis.md): run an
agent, capture typed evidence, score failures, improve ranking or contracts,
and replay the scenario.

## Cache And Freshness Interaction

The sidecar is valuable only if callers can trust freshness. Agent Harness's
`context_pack` persists graph views in `.harness/comprehension/graph.sqlite`
and human-readable exports under `.harness/comprehension/views/`. The cache key is
content-addressed over graph index ID plus task, mode, path, scope, limit,
budget, tests, forbidden paths, and intent, as documented in the project
AGENTS.md. `context_status` reports the active index, collector version,
languages, storage backends, and freshness state.

Sidecar freshness contract:

- `report_only`: return evidence and freshness metadata even when stale.
- `read_through`: refresh the relevant scope before answering when possible.
- `require_fresh`: fail with `freshness_blocked` if fresh evidence cannot be
  produced.
- `no_refresh`: use the active graph only, useful for deterministic replay.

Every graph-backed response should include:

- `index_id`
- `pack_id` or query result ID when applicable
- `fresh_for_scope`
- `global_fresh`
- `refresh_attempted`
- `stale_paths`
- `fallbacks_used`

Known cache limitations must stay visible to LLM-agent callers: full rebuilds can
mint a new `index_id`, ranker weight changes and new runtime traces may require
an index refresh to reshape outputs, and stale working trees should bypass or
refresh cache rows rather than silently serving old context.

## Compatibility Matrix

| Caller | CLI Transport | MCP Transport | Best default profile | Main workflow | Notes |
| --- | --- | --- | --- | --- | --- |
| CLI-first LLM agent | Yes, shell command from interactive or non-interactive execution | Yes, if the agent supports configured MCP servers and tool allow/deny lists | `discovery` for planning, `verify` for checks | Preload context packets, query graph, audit after edits | Keep LLM-agent sandbox/approval controls enabled; mark Agent Harness as required only when missing evidence should fail the run. |
| Hook-capable LLM agent | Yes, hook wrapper scripts | Yes, when hook matchers can target MCP tool names | `discovery` or `verify` | Hook-time context injection, post-edit source sanity, subagent context | Use wrapper scripts for hook JSON; avoid brittle prompt interpolation. |
| Profile-based LLM agent | Yes, shell tools or project scripts | Depends on the active caller environment | `discovery` | Planner agents call context and audit tools under ask/allow permissions | Align LLM-agent permission keys with Agent Harness profile gates. |
| Repo-map LLM agent | Yes, shell pre-step or wrapper around a task message | Wrapper-dependent | `discovery` | Precompute context packet before the edit session | Complements repo maps; does not replace an LLM agent's editing loop. |
| Manual scripts | Yes, primary interface | Optional | `discovery`, `verify`, or `write` | CI preflight, context exports, audit snapshots | Must remain stable even if MCP is not installed. |
| Agent Harness lanes | Native internal dispatch | Not needed | lane-owned | Local model autonomy | Lanes remain a first-party consumer of the same substrate. |

## Implementation Design Map

### Phase 0: Document And Freeze The Contract

Acceptance criteria:

- This document is linked from the strategic thesis, MCP server design, and
  reusability docs.
- `harness tool help`, `harness tool list`, and docs agree on the sidecar purpose.
- The active CLI envelope is documented as compatibility behavior.
- A new `harness.tool.v1` envelope is designed but not required by callers.

### Phase 1: Schema Export For Existing CLI

Acceptance criteria:

- `harness tool schema <tool>` prints the tool argument JSON Schema.
- `harness tool schema --all` exports a deterministic schema bundle.
- `harness_audit` fails or warns on manifest/schema drift with file-level
  evidence.
- At least `context_pack`, `context_query`, `context_zoom`, `context_status`,
  `context_impact`, `source_sanity`, and `harness_audit` have hand-reviewed
  schemas.

### Phase 2: Structured v1 CLI Envelope

Acceptance criteria:

- `harness tool <name> --format v1` emits the v1 response envelope.
- Existing `Tool`/`Summary`/`Output`/`Error` output remains default for one
  release.
- Error classes are stable and covered by tests.
- Freshness metadata is structured for all graph-backed tools.
- Golden tests cover success, validation error, permission denial, stale graph,
  and backend fallback.

### Phase 3: Read-Only MCP Stdio Server

Acceptance criteria:

- `harness mcp serve --stdio --profile discovery` implements initialize,
  `tools/list`, and `tools/call`.
- Tool list is generated from the same manifest/schema source as the CLI.
- Calls dispatch through the same workspace manager as `harness tool`.
- No write or shell tools are exposed in `discovery`.
- LLM agents can list and call `context_pack` in local smoke tests.

### Phase 4: MCP Resources And Audit

Acceptance criteria:

- Resources expose context packs, selected audit reports, and freshness state.
- `harness_audit` includes MCP caller attribution.
- MCP calls produce telemetry equivalent to CLI direct tool calls.
- Resource reads honor workspace containment and redaction settings.

### Phase 5: Streamable HTTP Server

Acceptance criteria:

- `harness mcp serve --http 127.0.0.1:8765 --profile discovery` implements
  Streamable HTTP according to MCP transport requirements.
- HTTP validates `Origin`, requires bearer-token auth, binds localhost by
  default, and rejects public bind without an explicit danger flag.
- Per-token profile, rate limit, and max concurrency settings exist.
- Integration tests cover invalid origin, missing token, profile denial, and
  concurrent read-only calls.

### Phase 6: Write And Verify Profiles

Acceptance criteria:

- `write` and `verify` profiles are available but not default.
- Write tools require configured write enablement and path scope.
- Verification commands use a constrained allowlist and produce post-write
  evidence.
- Audit reports write/verify ordering and changed paths.

### Phase 7: LLM Agent Recipes

Acceptance criteria:

- Docs include working LLM-agent and shell examples.
- Examples use least-privilege profiles.
- A smoke script validates `harness tool` examples without requiring external
  agent credentials.
- MCP examples are tested where local clients are available and clearly marked
  otherwise.

### Phase 8: Learning Flywheel Integration

Acceptance criteria:

- `harness_feedback` accepts LLM-agent caller metadata.
- Cache misses, stale packets, and repeated failed lookups can affect ranker or
  audit recommendations through an explicit, reviewable path.
- Evals can replay sidecar traces without requiring the original LLM agent.
- Audit output distinguishes model strategy failure from substrate contract
  failure.

## Non-Goals

- Do not build another general terminal pair programmer.
- Do not replace LLM agents chat loops.
- Do not make prompt text the integration API.
- Do not expose every internal lane or runner control as an integration tool.
- Do not proxy arbitrary MCP servers through Agent Harness before Agent Harness's own server
  surface is stable.
- Do not grant write or shell authority through MCP by default.
- Do not hide stale evidence behind friendly summaries.
- Do not report strategy recommendations from evidence tools.

## Risks

- Contract drift: CLI, MCP, internal lanes, and docs may diverge unless schema
  generation and `harness_audit` remain authoritative.
- Tool sprawl: exposing many tools can recreate the same confusion Agent Harness is
  trying to solve. Profiles and capability docs must keep the first surface
  small.
- Security expansion: HTTP MCP with write/shell profiles can become a local
  privilege boundary. Localhost binding, auth, origin checks, path containment,
  and conservative defaults are mandatory.
- Stale confidence: persistent memory can become worse than grep if freshness is
  unclear. Every response needs freshness and fallback metadata.
- Caller mismatch: each LLM agent has its own permission semantics. Agent Harness
  must narrow its own surface rather than assuming the caller's sandbox is
  sufficient.
- Audit volume: high-volume sidecar use can produce noisy telemetry. Redaction,
  retention, sampling, and summaries must be designed early.
- Strategy leakage: tool results may accidentally prescribe next actions. This
  would violate the model/harness split and make Agent Harness harder to reuse.

## Open Questions

- Should `harness.tool.v1` replace the active CLI envelope by default after one
  release, or remain opt-in indefinitely for compatibility?
- Should `source_sanity` be the canonical verification abstraction for external
  agents, or should `verify` become a separate sidecar family with explicit
  command allowlists?
- How should path scopes be represented across CLI, MCP roots, LLM agent
  permissions, LLM agent hooks, and LLM agent permissions without inventing separate
  policy languages?
- Which resources are safe to expose by default over MCP: context packs only,
  or also redacted conversation logs and ranker state?
- Should ranker learning consume LLM-agent traces automatically, or only
  after `harness_feedback` marks them as trusted?
- How should sidecar traces be retained when multiple LLM agents work in
  the same dirty worktree?
- What is the minimum stable discovery profile for first release: seven context
  tools only, or the full read-only semantic matrix?
- Should Agent Harness support a small SDK after MCP, or is CLI plus MCP sufficient?

## Cross-Link Expectations

This document should become the sidecar/substrate pillar and cross-link to:

- Persistent repository memory in
  [design-persistent-repository-memory.md](design-persistent-repository-memory.md):
  context graph, graph views, traces, ranker state, cache limitations.
- Capability-oriented tooling in
  [design-capability-oriented-tooling.md](design-capability-oriented-tooling.md):
  models ask for
  capabilities; the harness resolves backends and reports evidence.
- Contract-first local autonomy in
  [design-contract-first-local-autonomy.md](design-contract-first-local-autonomy.md):
  model strategy stays
  separate from host contracts.
- Auditable planning and handoff in
  [design-auditable-planning-handoff.md](design-auditable-planning-handoff.md):
  LLM-agent plans should carry packet IDs, path roles, and verification
  obligations when they cross into execution.
- Local-model operations in
  [design-local-model-operations.md](design-local-model-operations.md): sidecar
  clients need readiness, queue, backend, and degraded-mode evidence.
- Eval/audit/learning flywheel in
  [design-eval-audit-learning-flywheel.md](design-eval-audit-learning-flywheel.md):
  sidecar traces must be replayable evidence, not unstructured logs.
- Reusable lane/project seams in [portability-bootstrap-design.md](portability-bootstrap-design.md):
  project-specific tooling stays project-specific while Agent Harness owns the generic
  sidecar contract.
- MCP server mechanics in the MCP support design: MCP is
  a transport over the same workspace manager, manifest, schemas, permissions,
  telemetry, and audit.

The north-star sentence for every future edit is: LLM agents bring
strategy; Agent Harness supplies local repository evidence and enforces typed contracts.
