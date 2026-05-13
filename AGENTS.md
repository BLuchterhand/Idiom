# Idiom AGENTS

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

This repository builds `idiom`, a local coding-agent harness around Qwen, `llama.cpp`, routing/control rails, workspace tools, and eval infrastructure.

## Start Here

- Read [README.md](README.md), [docs/idiom-strategic-thesis.md](docs/idiom-strategic-thesis.md), [docs/idiom-design-roadmap.md](docs/idiom-design-roadmap.md), [docs/decisioning-north-star.md](docs/decisioning-north-star.md), and [.codex/project-context.md](.codex/project-context.md) before changing agent behavior, prompt layers, routing, control rails, readiness gates, verification logic, or strategic direction.
- Always use the OpenAI developer documentation MCP server for OpenAI API, Codex, MCP, or ChatGPT-app questions without waiting for the user to ask explicitly.
- Keep terminology precise: the model owns strategy; the harness owns contracts.

## North-Star Rules

- Prefer nudges, redirects, and bounded recovery over hard-picking the model's next step.
- Force only real invariants: permissions, protected paths, protocol correctness, verification, completion/readiness contracts, repeated-loop breaks, and exhausted budgets.
- After the evidence is sufficient, answer or act. Continue only if you can name one exact missing claim and one exact probe that would resolve it.
- Keep prompts, recovery text, and answer-shaping generic and evidence-based. Do not hardcode one recurring scenario or topic template into reusable instructions.
- Evidence tools and summaries should report findings, paths, symbols, counts, and uncertainty. They should not prescribe the next tool call.
- All work and answers must be grounded in factual analysis from the codebase, tool output, tests, logs, or cited docs. Distinguish verified facts from inference, and do not present guesses as findings.

## Working Style In This Repo

- Do not guess paths. Discover them first.
- Prefer this repo's discovery surface before generic file search or ad hoc reads. Start with the most specific applicable discovery/semantic tool, then fall back to standard search/read tools only when the discovery path does not answer the question.
- The preferred order is: language-aware symbol/navigation tools, `discovery_query`, other structured discovery tools such as `structural_search`, `dependency_graph`, `history_*`, `blame_range`, `ctags_symbols`, `semgrep_scan`, or `joern_scan`, and only then generic `search_content` / `read_file` style inspection.
- External agents (opencode, Codex, Claude, etc.) can invoke idiom's workspace tools directly via the CLI without starting an inference session. Use `idiom tool <tool-name> --input '<json>'` or pipe JSON on stdin. Run `idiom tool help` for usage and `idiom tool list` for all available tools.
- Keep edits targeted. Preserve user changes and do not widen scope without cause.
- For non-trivial write work, keep a short explicit plan/checklist so the intended edits and verification stay visible.
- Treat prompt files, runner policy, control rails, and eval fixtures as one system. Changes in one layer often require updates or tests in another.
- If the available evidence is thin, say so and gather more. Do not smooth over uncertainty with confident prose.

### Behavior Debugging

For bugs involving agent behavior, readiness, tool loops, prompts, recovery, or context packets:

- Treat conversation logs, telemetry logs, and `harness_audit` as the primary evidence trail. Start from the newest relevant `.idiom/logs/conversations/*.jsonl` and `logs/telemetry/*.jsonl` records before changing prompts or rails.
- Treat `harness_audit` behavior output as local diagnostic evidence. Before storing, sharing, or committing audit artifacts, redact raw prompt, raw transcript, log excerpt, file-body, secret, and customer-identifying fields; keep aggregate counts, command IDs, paths, findings, and session IDs needed for reproduction.
- Do not treat contract audit `all_clear` as sufficient by itself. Check `contract_coverage.gate_coverage.open_obligation_refs`, unresolved obligations, invalid-event warnings, and sidecar/tool correlation findings before using audit output as proof.
- If the logs do not make the failure shape obvious, improve the structured logging or audit coverage as part of the fix. A harness behavior bug is not fully diagnosable until audit output can surface the same class of failure without hand-reconstructing it from raw prose.
- Trace behavioral authority end to end: static prompt stack, dynamic instruction attachment, runtime brief, tool manifest/schema, recovery nudges, gates, telemetry, and audit output.
- Search for authority leaks, not only direct wording. Distinguish instructions from evidence, telemetry, generated context, and source text.
- For required-path implementation failures, distinguish exact-anchor write-first contracts from abstract scope-ready packets. Check both what tells the model to write and which bounded read/search affordances remain visible.
- Prefer removing conflicting affordances or encoding a typed contract over adding another prompt reminder.
- Add regression tests for the absence of bad behavior, not only the presence of desired guidance.

### Discovery Tool CLI Reference

These tools are available both inside idiom agent sessions and via `idiom tool` from an external agent's shell:

| Tool | When to use | Example invocation |
|------|-------------|--------------------|
| `context_pack` | Get a ranked, cached packet of candidate files / symbols / tests for a task. **Default first call when localizing any change** — output is content-addressed and cached in `graph_views`, so identical task strings short-circuit re-discovery. | `idiom tool context_pack --input '{"task":"wire packet cache","mode":"plan","limit":8}'` |
| `context_query` | Graph-shaped questions ("what calls X", "what tests cover Y") without raw file flooding. | `idiom tool context_query --input '{"query":"callers of LoadView","resolution":"symbol"}'` |
| `context_zoom` | Drill from a packet handle (file/symbol/repo) to the next resolution level. | `idiom tool context_zoom --input '{"handle":"symbol://internal/comprehension/views.go#SaveView","resolution":"symbol"}'` |
| `context_status` | Index freshness, stale paths, collector coverage, available backends. | `idiom tool context_status` |
| `context_index` | Build or refresh the comprehension index. Use scoped `--path <subset>` for incremental refresh. | `idiom tool context_index --input '{"path":".","language":"go","mode":"standard"}'` |
| `context_impact` | Blast-radius and may-impact analysis using the context graph. | `idiom tool context_impact --input '{"path":"internal/comprehension/pack.go"}'` |
| `context_trace_pack` | Attach runtime evidence (stack frames, test JSON, coverage) to a packet. | `idiom tool context_trace_pack --input '{"command":"go test ./internal/comprehension/...","path":"."}'` |
| `discovery_query` | Broad impact, tracing, architecture, security, or history analysis. Orchestrates multiple sub-tools in one call; consults `context_pack` cache when fresh. | `idiom tool discovery_query --input '{"purpose":"impact_analysis","path":".","pattern":"funcName","language":"go"}'` |
| `structural_search` | AST-aware code pattern search (requires `ast-grep`). | `idiom tool structural_search --input '{"pattern":"fn $NAME($$$ARGS) { $$$BODY }","path":"src/","language":"rust"}'` |
| `dependency_graph` | Understand module/package dependency structure for Go, Rust, TypeScript, Python, or Java. | `idiom tool dependency_graph --input '{"path":".","language":"go"}'` |
| `history_range` | Git log for a specific line range in a file. | `idiom tool history_range --input '{"path":"main.go","start_line":10,"end_line":50}'` |
| `history_symbol` | Git log following a specific symbol across renames. | `idiom tool history_symbol --input '{"path":"manager.go","pattern":"Execute"}'` |
| `blame_range` | Git blame for a specific line range. | `idiom tool blame_range --input '{"path":"main.go","start_line":1,"end_line":80}'` |
| `ctags_symbols` | Symbol indexing across the codebase (requires `ctags`). | `idiom tool ctags_symbols --input '{"path":".","pattern":"Handler"}'` |
| `semgrep_scan` | Security and pattern scanning (requires `semgrep`). | `idiom tool semgrep_scan --input '{"path":".","pattern":"auto"}'` |
| `joern_scan` | Code property graph analysis (requires `joern-scan`). | `idiom tool joern_scan --input '{"path":"."}'` |
| `harness_audit` | Self-audit: tool surface, language coverage, backend availability, log behavior, ranker learning, and `graph_views` cache state. Run after substantive changes to verify the harness sees the new state. | `idiom tool harness_audit --input '{"purpose":"behavior"}'` |

When invoked via CLI, the tool result is printed as JSON with `Tool`, `Summary`, `Output`, and `Error` fields. Exit code is 0 on success, 1 if the tool reported an error.

### When to reach for `context_pack` (and the cache it populates)

`context_pack` is the right starting point for almost every "find the relevant code for this task" question. It returns a ranked candidate file/symbol/test list with provenance, and persists the result in the `graph_views` table inside `.idiom/comprehension/graph.sqlite` so the next call with the same task hits the cache instead of recomputing.

- **Lane default**: `idiom lane implement` and `idiom lane plan-execute`
  automatically attach a compact `context_pack` section before the brief body.
  Treat that packet as the first localization pass, then use language/project
  tools only for named gaps. Use `--no-context-pack` only when the caller
  already supplied an equivalent packet or is debugging context generation.
  Do not paste a second broad packet into the brief unless a human-readable
  artifact needs it; add only missing exact anchors, runtime evidence, or
  domain-specific facts.
  Refine scratchpad meta-prompts are excluded because refine already preloads
  phase-specific lint/source evidence and an extra repo map can distract the
  writer/reviewer phase.
- **Use the same task string across related calls** so identical requests reuse the cache. Don't append timestamps, session ids, or "round 2" suffixes.
- **The cache key is content-addressed** over `(graph index_id, task, mode, path, scope, limit, budget_tokens, required_tests, forbidden_paths, intent, include_stale_runtime, workspace_state_fingerprint, ranker_weights_fingerprint, runtime_traces_fingerprint)`. The aggregate `workspace_state_fingerprint` is the cache validity signal for workspace-side state; the ranker/runtime component fingerprints are also carried in the canonical query and result contract for attribution.
- **Known cache limitations** (see `docs/codebase-comprehension-memory-design.md` "Known cache limitations"):
  - Cache reuse is tied to content-addressed `graph.ID`. No-op rebuilds keep the same `index_id`; source or collector changes that alter graph content mint a different key or stale rows through referenced-path invalidation. Rows are not migrated between genuinely different graph IDs.
  - Cached rows still validate the current index, referenced source fingerprints, and scoped freshness on load. Failed validation is a miss/recompute, not a served stale result.
  - Runtime trace fingerprinting uses the bounded trace manifest when present. Legacy/no-manifest trace directories use a constant-work directory timestamp fallback until `context_trace_pack` writes the manifest; that fallback is coarse for hand-edited existing trace files.
- **Plan-then-execute**: the planner's `pack_id` round-trips through `submit_plan` → `PlanArtifact.PackID` → executor phase, which emits `executor_inherits_pack` (or `executor_pack_miss`) JSON events. Pass the `pack_id` through when chaining planner + executor so downstream consumers (UIs, debug tooling) can attach.

### Discovery hierarchy (this Claude Code session, you, an external agent)

When investigating this codebase, the right order is:

1. **`context_pack`** with the task string → ranked candidates, cached.
2. **Language-aware tools** (`go_find_symbol`, `code_navigate`, `cpp_*`, `python_*`, etc.) for symbol-shaped questions covered by the active language backend.
3. **`trace_code` / `lookup` / `api_access` / `struct_shape` / `fixture_inventory`** (under `discovery_query`) for graph-shaped or test-fixture questions.
4. **`structural_search`** for AST-aware patterns.
5. **`grep`/`rg`/`Read`** for literal strings, comments, doctest names, single-known-file scans, and anywhere the discovery surface doesn't cover the question.

If you find yourself ten greps deep without having tried `context_pack` once, you're holding the tool wrong. Inversely, don't use `context_pack` for "find the literal string `===END===`" — that's grep territory.

## Verification

- Default verification for code changes: `go test ./...`
- When a change is localized, run the narrowest relevant package tests first, then the full suite if the change affects shared runtime behavior, prompts/control rails, routing, or workspace/tooling contracts.
- If entrypoints or build wiring change, also run `make build` or `go build ./cmd/idiom`.
- If a change touches eval, routing, or discovery behavior, check the most relevant focused tests in addition to the full suite.

## Useful Commands

- `make run`
- `make build`
- `make test`
- `make coverage`
- `make eval LOG=logs/conversations/<session>.jsonl`
- `make swe-live-subset`

## Key Areas

- `cmd/idiom`: CLI entrypoint
- `internal/app`: TUI/session lifecycle
- `internal/agent`: runner, control rails, readiness, final answer shaping
- `internal/workspace`: tool surface and language-aware workspace operations
- `internal/frontier`: discovery/runtime helpers
- `prompts/`: baseline and mode/profile instructions
- `docs/`: design intent, north star, remediation, and parity plans

## Project Docs

- [.codex/project-context.md](.codex/project-context.md): Codex-focused project brief
- [docs/idiom-strategic-thesis.md](docs/idiom-strategic-thesis.md): project-defining differentiation thesis and roadmap
- [docs/decisioning-north-star.md](docs/decisioning-north-star.md): canonical strategy-vs-contract policy
- [docs/agent-autonomy-tracker.md](docs/agent-autonomy-tracker.md): completion, task-ledger, and workflow-state expectations
- [docs/north-star-remediation.md](docs/north-star-remediation.md): examples of repo language that should be generalized or softened
