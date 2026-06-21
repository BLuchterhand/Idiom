# Idiom AGENTS

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

This repository builds `idiom`, a local coding-agent harness around Qwen, `llama.cpp`, routing/control rails, workspace tools, and eval infrastructure.

## Start Here

- Read [README.md](README.md), [docs/idiom-strategic-thesis.md](docs/idiom-strategic-thesis.md), [docs/idiom-design-roadmap.md](docs/idiom-design-roadmap.md), [docs/decisioning-north-star.md](docs/decisioning-north-star.md), [.codex/project-context.md](.codex/project-context.md), and [.codex/common-traps.md](.codex/common-traps.md) before changing agent behavior, prompt layers, routing, control rails, readiness gates, verification logic, or strategic direction.
- Before changing harness behavior, prompt contracts, queueing, atom intake, audit, or lane orchestration, also read [docs/idiom-maintenance-best-practices.md](docs/idiom-maintenance-best-practices.md).
- Always use the OpenAI developer documentation MCP server for OpenAI API, Codex, MCP, or ChatGPT-app questions without waiting for the user to ask explicitly.
- Keep terminology precise: the model owns strategy; the harness owns contracts.

## North-Star Rules

- Prefer nudges, redirects, and bounded recovery over hard-picking the model's next step.
- Force only real invariants: permissions, protected paths, protocol correctness, verification after writes, final-answer acceptance, repeated-loop breaks, and exhausted budgets in lanes with concrete deliverables.
- In read-only strategy lanes, do not force answer synthesis, no-tools closure, or answer-only surfaces merely because the harness thinks evidence is sufficient. If the model asks for a relevant read/search/data source, honor it unless a real invariant rejects it, and make any rejection name the invariant and the exact unsupported/repeated/prohibited action.
- After the evidence is sufficient, answer or act. Continue only if you can name one exact missing claim and one exact probe that would resolve it.
- Keep prompts, recovery text, and answer-shaping generic and evidence-based. Do not hardcode one recurring scenario or topic template into reusable instructions.
- Evidence tools and summaries should report findings, paths, symbols, counts, and uncertainty. They should not prescribe the next tool call.
- All work and answers must be grounded in factual analysis from the codebase, tool output, tests, logs, or cited docs. Distinguish verified facts from inference, and do not present guesses as findings.

### Behavior-Fix Decision Procedure

Before changing prompts, routing, readiness, recovery, delegated discovery, or
final-answer shaping, classify the fix using this checklist:

1. **Harness-owned invariant:** Is the harness enforcing permissions,
   protected paths, tool/protocol shape, evidence freshness, source-body
   support, final-answer acceptance, verification, loop bounds, or bounded
   deliverable budget exhaustion? If not, the proposed fix is probably
   model-strategy steering. Read-only "readiness" is observable evidence state,
   not authority to force an answer.
2. **Model-owned strategy:** Does the change hard-pick the next probe, answer
   outline, file path, answer timing, no-tools mode, or synthesis for a topic
   when multiple valid strategies remain? If yes, redesign it as a typed
   contract, evidence predicate, or advisory nudge, or remove it.
3. **Exact anchors vs composite scope:** If the model or user has an exact
   source file/symbol/body anchor, preserve that narrow path by default.
   Redirect to broad/composite discovery only when the question is inherently
   cross-cutting or the exact anchor has been proven off-objective.
4. **Evidence integrity:** Do not accept candidate paths, listings, summaries,
   or off-objective packets as source-body evidence. Promote a packet only when
   its findings support the user's objective and name source-owner evidence
   where the contract requires source ownership.
5. **Generic ownership:** A special case is allowed only when it names a real
   domain boundary and is covered by positive and negative tests. Prefer shared
   predicates such as objective relevance, source ownership, exact-anchor
   preservation, and composite-scope classification over topic templates.

If the patch cannot pass this checklist in writing, do not land it. Gather more
log evidence or move the fix to the contract boundary that actually owns the
failure.

## Engineering Quality Baseline

For code changes, reviews, refactors, and tests, apply the local
`unified-software-engineering` rule set as a binding repo baseline even when
external rulebook files are not loaded. Use specialized rulebooks as reference
material when they materially change a task, but do not rely on them as the only
source of quality policy.

For implementation, refactoring, design review, and rearchitecture decisions,
also use [docs/software-engineering-ruleset.md](docs/software-engineering-ruleset.md)
as the repository's practical ruleset for choosing paradigms, architectures,
patterns, algorithms, data structures, quality practices, and operational
tradeoffs.

- Optimize for code a senior team can maintain for years: clear names, cohesive
  functions, shallow control flow, explicit behavior, and actionable errors.
- Keep business logic, infrastructure, persistence, API, and presentation
  concerns separated unless the existing architecture intentionally combines
  them.
- Prefer local, boring, explicit code over clever compression or premature
  abstraction.
- Duplicate once when the abstraction is unclear; abstract when the third use
  proves the shared concept.
- Tests should specify behavior, cover meaningful edge and failure cases, and
  avoid brittle implementation coupling.
- Before presenting code, self-review the diff for naming, complexity,
  duplication, edge cases, boundary leaks, dead code, and missing verification.

## Working Style In This Repo

- Do not guess paths. Discover them first.
- Default to an idiom-offloaded work loop: use the actual `idiom` binary and
  CLI/context tools to research, debug, and gather evidence; synthesize and
  verify the proposed approach outside idiom; then route actual repository
  changes back through `idiom plan-execute` or `idiom lane plan-execute` by
  default. Prefer the TUI/live operator path when the behavior under test is
  user-facing, and monitor long-running CLI/TUI work by polling slowly and
  reading only small bounded log or telemetry chunks.
- Do not classify a long-running `idiom` command, lane, TUI session, or worker
  as a harness failure merely because it is taking a while. This is a large
  repository; reads, writes, discovery, review, and verification can legitimately
  run for a long time. Treat a run as failed only when it emits a typed failure,
  exits non-zero, records a blocker, or shows a repeated no-progress condition
  across slow bounded polls with evidence. While it is still running, poll
  process status, structured status artifacts, or small log/telemetry tails at a
  low cadence instead of streaming output or imposing a new timeout.
- For local harness or subagent delegation, use a context-efficient supervisor
  loop by default. Launch each worker with stdout/stderr redirected to a run log,
  record the PID and expected artifact paths, and poll only process status or a
  small structured status file while it runs. Do not stream or repeatedly read
  live progress output into the orchestrator context. Require delegated workers
  to write bounded artifacts such as `status.json`, `final.md`, `summary.json`,
  and `blockers.json` when the harness supports it. On success, read the final
  summary and inspect the resulting diff or declared artifacts; on failure, read
  only a bounded log tail plus blocker artifacts needed to decide the next
  orchestration step. Keep research, implementation, and review inside the
  delegated harness when the user requested that actor boundary.
- When the user explicitly asks Codex to use subagents or parallel agents in
  this repo, choose from `.codex/agents/*.toml` by task risk: `idiom-explorer`
  for low-effort read-only exploration, localization, log triage, and evidence
  summaries; `idiom-worker` for medium-effort localized implementation,
  documentation updates, and focused refactors; `idiom-reviewer` for
  high-effort correctness, contract, regression, security, architecture, and
  test-gap review; `idiom-root-cause` for high-effort behavior debugging across
  prompts, control rails, readiness, recovery, telemetry, and evals; and
  `idiom-strategist` for rare xhigh-effort cross-cutting design, migration, or
  incident-analysis decisions. Effort follows contract risk, not task size:
  small prompt, gate, runtime, or readiness changes usually deserve high effort,
  while large mechanical scans usually deserve low or medium effort.
- If the issue is in idiom itself, keep the research and fix inside idiom unless
  that bug blocks idiom from proceeding. When idiom is blocked, record the
  session/failure id and blocker, then use the smallest external fallback needed
  to continue.
- For behavior-debugging and broad harness investigations, run through the
  actual `idiom` binary first, preferably the durable headless CLI such as
  `idiom run --prompt '<investigation>' --json`. Use legacy `idiom -prompt
  '<investigation>' -json` only for compatibility checks. Treat the
  binary's route, tool sequence, answer, and log as the first diagnostic
  artifact before reaching for lower-level workspace tools or raw shell search.
  For simple literal lookups or already-localized source review, use the
  narrowest exact tool.
- If an `idiom` binary investigation returns unhelpful, stale, misrouted, or
  garbage output, first decide whether that is an Idiom bug, coverage gap, or
  missing contract that should be fixed for future runs. Use the run's
  conversation log, route events, tool results, and stop reason as evidence. If
  the binary result is merely incomplete but not a harness bug, proceed with the
  normal debugging path and record that distinction.
- After the binary has localized the question or proved unhelpful, prefer this
  repo's discovery surface before generic file search or ad hoc reads. Start
  with the most specific applicable discovery/semantic tool, then fall back to
  standard search/read tools only when the discovery path does not answer the
  question.
- The preferred post-binary order is: language-aware symbol/navigation tools,
  `discovery_query`, other structured discovery tools such as
  `structural_search`, `dependency_graph`, `history_*`, `blame_range`,
  `ctags_symbols`, `semgrep_scan`, or `joern_scan`, and only then generic
  `search_content` / `read_file` style inspection.
- External agents (opencode, Codex, Claude, etc.) can invoke idiom's workspace tools directly via the CLI without starting an inference session. Use `idiom tool <tool-name> --input '<json>'` or pipe JSON on stdin. Run `idiom tool help` for usage and `idiom tool list` for all available tools.
- Keep edits targeted. Preserve user changes and do not widen scope without cause.
- Before changing or reverting established behavior, inspect relevant git history
  with `history_range`, `history_symbol`, `blame_range`, or focused `git log`
  when the prior rationale is not obvious. Treat commit messages as evidence of
  why a decision exists, not as unquestionable authority.
- Before removing behavior from a recent broad commit, build a retention table:
  changed area, failure it addressed, current tests/logs that still depend on
  it, keep/remove decision, and focused verification. Do not broad-revert first
  and discover dependent live contracts afterward.
- Before sending or committing a behavior fix, step back and state the deepest
  generic root cause supported by the logs. Validate that hypothesis with the
  debugging reviewer bench, then implement the smallest contract-level fix that
  addresses that root cause. Do not land symptom patches or lane-specific
  workarounds while a broader harness contract explains the failure.
- For every bug, perform a root-cause checkpoint before editing: name the first
  broken invariant, the boundary that owns it, the evidence proving that
  boundary failed, and the broader class of future failures the fix should
  prevent. A narrow code patch is acceptable only after this checkpoint shows
  the invariant is truly local; otherwise prefer the deeper contract, tool,
  readiness, acceptance, or architecture repair even when a smaller patch would
  make the immediate test pass.
- Treat repeated small fixes in the same area as evidence of a missing
  abstraction or contract. Pause, review the churn, and consolidate around the
  shared concept before adding another guard, string check, prompt nudge, or
  eval expectation.
- Strongly avoid bespoke implementations of shared logic. If a fix needs a new
  branch, string matcher, lane-specific path, scenario-specific prompt, or
  one-off helper, first identify the shared invariant and the boundary that
  should own it. Prefer a typed contract, reusable predicate, common adapter,
  or existing abstraction over duplicating behavior in a local patch. A special
  case is acceptable only when the domain boundary is real, explicitly named,
  covered by tests, and reviewed as intentionally isolated rather than a
  shortcut around the common mechanism.
- For non-trivial write work, keep a short explicit plan/checklist so the intended edits and verification stay visible.
- When committing, make the commit body useful future evidence: explain the
  problem or failure that forced the change, the chosen contract/design, any
  meaningful alternatives or reversions avoided, and the verification run. Do
  not leave future agents guessing whether a later edit is progress or churn.
- Treat prompt files, runner policy, control rails, and eval fixtures as one system. Changes in one layer often require updates or tests in another.
- If the available evidence is thin, say so and gather more. Do not smooth over uncertainty with confident prose.

### Behavior Debugging

For bugs involving agent behavior, readiness, tool loops, prompts, recovery, or context packets:

- Start with an actual `idiom` binary investigation whenever the binary can run:
  ask it to inspect the newest relevant logs and produce the evidence trail. If
  that investigation misroutes, loops, fabricates, or fails to use available
  evidence, treat the failed investigation itself as behavior-debugging evidence
  and consider whether the harness should be fixed before bypassing it.
- Treat conversation logs, telemetry logs, and `harness_audit` as the source of truth for what happened. Start from the newest relevant user-level `~/.idiom/logs/conversations/*.jsonl` records, then check repo-local `.idiom/logs/conversations/*.jsonl`, `logs/conversations/*.jsonl`, and `logs/telemetry/*.jsonl` records before changing prompts, rails, gates, or tests. The `idiom -prompt ... -json` path normally writes conversation logs under `~/.idiom/logs/conversations`, even when the workspace has its own `.idiom/` directory. Do not rely on memory, summaries, or the user's paraphrase of the run when logs are available.
- Record the evidence trail in your diagnosis: exact session/log path(s), relevant stop reason(s), rejected tool/error counts, completion-gate or recovery events, and the tool sequence that proves the failure shape. If you cannot name those facts yet, keep inspecting logs instead of patching.
- Compare the newest failure against prior similar logs before choosing a fix. A recurring behavior bug should be classified from log patterns across attempts, not from the single most recent transcript alone.
- When an investigation starts to chase prompt wording, a lane name, a single
  path, or a repeated tool call, pause and reclassify the failure at the deepest
  contract boundary that explains the evidence. Prefer fixes to runtime
  contracts, tool affordances, completion/readiness gates, or final-answer
  transport over scenario-specific prompt nudges.
- Never treat "make this failing scenario pass" as the root cause. The required
  diagnosis is the reusable invariant failure that made the scenario possible,
  such as stale authority, missing source-body evidence, mismatched tool schema,
  premature readiness, weak final-answer acceptance, or an incoherent actor
  boundary.
- Before proposing a fix, explicitly list tempting smaller patches and why they
  are insufficient or genuinely enough. If the answer is "because it satisfies
  the current assertion," the analysis is incomplete.
- Treat `harness_audit` behavior output as local diagnostic evidence. Before storing, sharing, or committing audit artifacts, redact raw prompt, raw transcript, log excerpt, file-body, secret, and customer-identifying fields; keep aggregate counts, command IDs, paths, findings, and session IDs needed for reproduction.
- Do not treat contract audit `all_clear` as sufficient by itself. Check `contract_coverage.gate_coverage.open_obligation_refs`, unresolved obligations, invalid-event warnings, and sidecar/tool correlation findings before using audit output as proof.
- If the logs do not make the failure shape obvious, improve the structured logging or audit coverage as part of the fix. A harness behavior bug is not fully diagnosable until conversation/telemetry logs and audit output can surface the same class of failure without hand-reconstructing raw prose.
- Trace behavioral authority end to end: static prompt stack, dynamic instruction attachment, runtime brief, tool manifest/schema, recovery nudges, gates, telemetry, and audit output.
- When repeated recovery nudges are needed, inspect the first model-facing lead-in before adding another nudge. The initial prompt must state the phase's executable protocol, valid terminal outcomes, evidence prerequisites, and tool/schema shape in the same order the harness gates enforce them.
- Keep actor and contract names stable across prompts, gates, logs, and UI. A plan-execute planner does not "implement"; it produces a `submit_plan` artifact, a `Decision: source-satisfied` closeout, or a typed blocker. The executor implements accepted plan edits. Rejected `submit_plan` calls are invalid attempts, not successful plan artifacts.
- Separate similarly named obligations in prompt and gate language: `required_edit_paths` are mutation scope, source-body obligations are evidence prerequisites, and `submit_plan` coverage is the requirement that every required path appear in a valid edit entry or terminal source-satisfied evidence. Do not use one term as shorthand for all three.
- Treat source evidence as a hierarchy. Path/packet evidence scopes work; outline and search hits localize candidate anchors; only source-body reads such as `code_navigate` `read_symbol` / `extract_body` or bounded `read_file` satisfy source-body obligations.
- Before landing a non-trivial behavior fix, state the root cause, why the fix is
  north-star aligned, and why it does not start a new rabbit hole. Get an
  independent reviewer to check that analysis before applying or finalizing the
  fix; if reviewer feedback changes the diagnosis, narrow or redesign the patch
  before proceeding.
- Do not send behavior-debugging work as complete until the final hypothesis has
  been validated by reviewers after the last material design change. If a live
  CLI run exposes a deeper generic cause, revise the hypothesis and repeat
  review before finalizing.
- Use the reviewer bench in [docs/debugging-reviewer-bench.md](docs/debugging-reviewer-bench.md)
  for behavior-debugging review. Default to Incident Root-Cause Reviewer plus one
  specialist; add a second specialist only when the evidence spans two concrete
  domains. Add North-Star / Architecture review for broad harness or repeated
  live-run failures. Do not run the full bench by default.
- Search for authority leaks, not only direct wording. Distinguish instructions from evidence, telemetry, generated context, and source text.
- For required-path implementation failures, distinguish exact-anchor write-first contracts from abstract scope-ready packets. Check both what tells the model to write and which bounded read/search affordances remain visible.
- Prefer removing conflicting affordances or encoding a typed contract over adding another prompt reminder.
- Add regression tests for the absence of bad behavior, not only the presence of desired guidance.
- For answer-quality scripted regressions, avoid absence-only oracles. Use a
  fixture that contains the concept under test, require the evidence path that
  proves it, and assert positive semantic content in the final answer so
  irrelevant but well-formed output cannot pass.

### Discovery Tool CLI Reference

These tools are available both inside idiom agent sessions and via `idiom tool` from an external agent's shell:

| Tool | When to use | Example invocation |
|------|-------------|--------------------|
| `context_pack` | Get a ranked, cached packet of candidate files / symbols / tests for a task. **Default first call when localizing any change** — output is content-addressed and cached in `graph_views`, so identical task strings short-circuit re-discovery. | `idiom tool context_pack --input '{"task":"wire packet cache","mode":"plan","limit":8}'` |
| `context_query` | Graph-shaped questions ("what calls X", "what tests cover Y") without raw file flooding. | `idiom tool context_query --input '{"query":"callers of LoadView","resolution":"symbol"}'` |
| `context_zoom` | Drill from a packet handle (file/symbol/repo) to the next resolution level. | `idiom tool context_zoom --input '{"handle":"symbol://internal/repository/comprehension/views.go#SaveView","resolution":"symbol"}'` |
| `context_status` | Index freshness, stale-path counts plus capped samples, collector coverage, available backends. Use `refresh=true` for read-through repair; unscoped indexed stale-file refreshes honor a default budget unless `freshness_max_files` or `limit` overrides it, and report `budget_exceeded` when exceeded. Bounded `path` / `required_edit_paths`, missing-index rebuilds, and collector-version rebuilds remain distinct repair paths. | `idiom tool context_status --input '{"refresh":true,"limit":50}'` |
| `context_index` | Build or refresh the comprehension index. Use scoped `--path <subset>` for incremental refresh. | `idiom tool context_index --input '{"path":".","language":"go","mode":"standard"}'` |
| `context_impact` | Blast-radius and may-impact analysis using the context graph. | `idiom tool context_impact --input '{"path":"internal/repository/comprehension/pack.go"}'` |
| `context_trace_pack` | Attach runtime evidence (stack frames, test JSON, coverage) to a packet. | `idiom tool context_trace_pack --input '{"command":"go test ./internal/repository/comprehension/...","path":"."}'` |
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

1. **Actual `idiom` binary for behavior debugging or broad harness
   investigations** with the investigation prompt, preferably
   `idiom run --prompt '<question>' --json`. Use legacy `idiom -prompt
   '<question>' -json` only for compatibility checks. Inspect its route,
   tools, answer, and log before using lower-level tools. For simple literal
   lookups or already-localized source review, skip directly to the narrow exact
   tool.
2. **`context_pack`** with the same stable task string → ranked candidates,
   cached, especially when the binary points at a broad code area or when you
   need a direct CLI follow-up.
3. **Language-aware tools** (`go_find_symbol`, `code_navigate`, `cpp_*`, `python_*`, etc.) for symbol-shaped questions covered by the active language backend.
4. **`trace_code` / `lookup` / `api_access` / `struct_shape` / `fixture_inventory`** (under `discovery_query`) for graph-shaped or test-fixture questions.
5. **`structural_search`** for AST-aware patterns.
6. **`grep`/`rg`/`Read`** for literal strings, comments, doctest names, single-known-file scans, and anywhere the discovery surface doesn't cover the question.

If you are debugging behavior or doing broad harness investigation and find
yourself ten greps deep without having tried the actual `idiom` binary once,
you're holding the tool wrong. Inversely, once the binary has localized a
literal or single-known-file check, use the simplest exact follow-up instead of
forcing another broad packet.

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
- `internal/surfaces/tui/app`: TUI/session lifecycle
- `internal/runtime/agent`: runner, control rails, readiness, final answer shaping
- `internal/capabilities/workspace`: tool surface and language-aware workspace operations
- `internal/frontier`: discovery/runtime helpers
- `prompts/`: baseline and mode/profile instructions
- `docs/`: design intent, north star, remediation, and parity plans

## Hosted DeepSeek Implementation Routing

The repository default backend profile is `deepseek-v4-flash`, a hosted
non-local DeepSeek profile declared in `.idiom/backends.json`. Prefer the
default profile for implementation-heavy atoms when the user asks for a
non-local implementor and no more specific backend is required.

Brief lint is local (Python preflight) and does not route through any model
backend:

```bash
idiom brief lint path/to/brief.md
idiom brief lint --json path/to/brief.md
```

### Prerequisites

- The hosted DeepSeek API credentials required by the active backend profile
  must be set in the shell.
- No local backend process is needed for hosted DeepSeek profiles.

### Smoke testing

After any change to the profile, backends.json, or prompt stack, verify the
end-to-end path:

1. **Backend health**: `idiom backend status --profile deepseek-v4-flash` —
   expect the hosted DeepSeek backend to report UP.
2. **Brief lint** (no model needed): write a minimal brief and run
   `idiom brief lint <path>`.
3. **Plan-execute dry run**: invoke `idiom lane plan-execute --backend-profile
   deepseek-v4-flash --json` with a trivial one-edit brief and confirm the
   planner phase completes and the executor phase dispatches against the hosted
   DeepSeek backend.

## Project Docs

- [.codex/project-context.md](.codex/project-context.md): Codex-focused project brief
- [docs/idiom-strategic-thesis.md](docs/idiom-strategic-thesis.md): project-defining differentiation thesis and roadmap
- [docs/decisioning-north-star.md](docs/decisioning-north-star.md): canonical strategy-vs-contract policy
- [docs/agent-autonomy-tracker.md](docs/agent-autonomy-tracker.md): completion, task-ledger, and workflow-state expectations
- [docs/north-star-remediation.md](docs/north-star-remediation.md): examples of repo language that should be generalized or softened
