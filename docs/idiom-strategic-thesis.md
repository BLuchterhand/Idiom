# Idiom Strategic Thesis

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: project thesis, drafted 2026-05-12

This document answers one question: if Idiom resembles Aider on the surface,
what is the reason to keep building it?

The answer is not that Idiom should become another terminal pair programmer.
Aider already owns much of that shape. Idiom should continue only as a more
specific thing: a local, inspectable agent harness for bounded autonomous
software work, where repository context, permissions, verification, completion,
and improvement loops are host-owned contracts rather than prompt hopes.

## One-Sentence Thesis

Idiom is the local, evidence-first coding-agent harness for smaller and local
models: the model owns strategy, while the harness owns contracts, repository
memory, verification, auditability, and the learning loop that makes each run
more reliable than the last.

## Design Pillar Documents

The project-defining design set is indexed in
[idiom-design-roadmap.md](idiom-design-roadmap.md). The seven pillar docs are:

- [design-contract-first-local-autonomy.md](design-contract-first-local-autonomy.md)
- [design-persistent-repository-memory.md](design-persistent-repository-memory.md)
- [design-capability-oriented-tooling.md](design-capability-oriented-tooling.md)
- [design-auditable-planning-handoff.md](design-auditable-planning-handoff.md)
- [design-local-model-operations.md](design-local-model-operations.md)
- [design-eval-audit-learning-flywheel.md](design-eval-audit-learning-flywheel.md)
- [design-agent-substrate-sidecar.md](design-agent-substrate-sidecar.md)

## The Competitive Reality

Aider is not a weak baseline. It already provides the obvious terminal coding
surface:

- conversational code editing in a local git repository
- a repository map built from tree-sitter symbols and graph ranking
- support for many languages
- lint and test repair loops
- git integration
- ask/code/architect modes
- IDE comment workflows, images, web pages, and voice

External sources:

- Aider describes itself as terminal pair programming with cloud and local
  model support, repo mapping, git commits, IDE workflows, visual context, voice,
  and lint/test repair: <https://aider.chat/>
- Aider's repo map article explains that it uses tree-sitter to extract code
  structure and graph ranking to fit the most useful map content into a token
  budget: <https://aider.chat/2023/10/22/repomap.html>
- Aider's chat modes include ask, code, and architect/editor workflows:
  <https://aider.chat/docs/usage/modes.html>
- Aider's lint/test docs describe test commands and automatic repair after
  failing checks: <https://aider.chat/docs/usage/lint-test.html>
- Aider's supported-language docs show broad linter and repo-map coverage:
  <https://aider.chat/docs/languages.html>

The conclusion is blunt: "Aider, but local Qwen" is not enough. Aider can already
use local models. "Aider, but with a repo map" is not enough. Aider's repo map is
the mature reference point. "Aider, but with a few more tools" is not enough.
Tool count creates surface area, not differentiation.

Idiom's differential must be below the visible chat surface.

## Adjacent Frontier Signals

The larger coding-agent ecosystem is moving toward host-owned structure around
the model loop:

- Claude Code subagents preserve context, enforce tool restrictions, and split
  exploration from implementation. Its built-in Explore and Plan agents are
  read-only, while general-purpose agents can perform multi-step modification:
  <https://code.claude.com/docs/en/sub-agents>
- Claude Code hooks expose lifecycle events such as pre-tool, post-tool,
  subagent start/stop, task creation, and stop control. Agent hooks can inspect
  files and tool output before returning a structured decision:
  <https://code.claude.com/docs/en/hooks>
- OpenCode agents expose explicit permissions with ask/allow/deny semantics,
  per-agent permission overrides, subagent modes, and task permissions:
  <https://opencode.ai/docs/agents/>
- Codex CLI is a local terminal coding agent that can read, change, and run code
  in a selected directory and exposes approvals, subagents, review, MCP, web
  search, scripting, and cloud task integration in its current documentation:
  <https://developers.openai.com/codex/cli>
- SWE-agent's 2024 arXiv preprint argues that the agent-computer interface
  itself materially affects software-agent performance, including editing,
  repository navigation, tests, and command execution:
  <https://arxiv.org/abs/2405.15793>
- CodePlan's Microsoft Research publication page frames repository-level coding
  as a planning problem over dependency and may-impact analysis, with multi-step
  edits informed by repository context:
  <https://www.microsoft.com/en-us/research/publication/codeplan-repository-level-coding-using-llms-and-planning/>
- RepoGraph's OpenReview ICLR 2025 entry reports that repository-level graph
  structure improves software engineering agents across SWE-bench and
  CrossCodeEval-style tasks:
  <https://openreview.net/forum?id=dw9VUsSHGB>
- Codebase-Memory's 2026 arXiv preprint reports a persistent tree-sitter
  knowledge graph exposed over MCP, with much lower token and tool-call use than
  repeated file exploration while retaining most answer quality:
  <https://arxiv.org/abs/2603.27277>

The research and product direction agree on the important point: the model is
not the whole agent. The interface, permissions, context substrate, workflow
state, and evaluation loop are performance levers.

## Idiom's Claim

Idiom should not try to beat Aider as a polished terminal pair programmer in the
near term. Idiom should beat generic pair-programming harnesses on five sharper
claims.

### 1. Contract-First Local Autonomy

Detailed design: [design-contract-first-local-autonomy.md](design-contract-first-local-autonomy.md).

The core doctrine remains:

- The model owns strategy.
- The harness owns contracts.

That is already the canonical standard in
[decisioning-north-star.md](decisioning-north-star.md). The strategic step is to
turn that doctrine into the product boundary.

Contracts include:

- read/write permissions
- protected paths
- required edit paths
- target roles: editable, read-only evidence, analogy, forbidden
- post-write verification
- completion readiness
- task ledger state
- loop and budget fuses
- route-plan and workflow-state authority

The model can decide whether to inspect a symbol, write a patch, run a test, or
answer. The harness decides whether that action is allowed, whether evidence is
sufficient, and whether the run can honestly stop.

Differential: most tools expose permissions and approvals. Idiom's angle is to
make contracts task-shaped, inspectable, and replayable in local logs.

### 2. Persistent Repository Memory, Not Just Prompt Context

Detailed design: [design-persistent-repository-memory.md](design-persistent-repository-memory.md).

Aider's repo map is an efficient prompt context mechanism. Idiom's target should
be a persistent, evidence-linked repository memory:

- an index of files, packages, symbols, tests, imports, calls, diagnostics,
  runtime traces, and confidence metadata
- narrow views returned to the model at the resolution needed for the task
- packets that bind views to constraints, validation, freshness, and handoff
  contracts

This matches the design in
[codebase-comprehension-memory-design.md](codebase-comprehension-memory-design.md).
The important distinction is not "we also parse symbols." The distinction is:
Idiom should preserve and reuse repository facts as a local substrate across
turns, lanes, evals, and audits.

The strategic aim is to make `context_pack`, `context_query`, `context_zoom`,
`context_impact`, and `context_trace_pack` the default way a smaller/local model
orients itself without flooding the prompt.

Differential: Idiom's repository memory should be queryable, fresh, source-linked,
and usable by external agents through `idiom tool`, not merely injected into one
chat prompt.

### 3. Auditable Planning And Handoff

Detailed design: [design-auditable-planning-handoff.md](design-auditable-planning-handoff.md).

Architect packets already point in the right direction:

- deterministic context impact and context pack steps
- planning packets with contracts, candidate scope, freshness, language
  capabilities, handoff, and context budget
- deterministic plan audit
- packet comparison and replay comparison

See [architect-packets.md](architect-packets.md).

This should become one of the primary product claims: Idiom can externalize,
validate, and replay planning context before any model is asked to edit. That is
the difference between "the assistant thought about it" and "the host produced
an auditable planning envelope."

Differential: Idiom should make plan quality measurable before execution, not
only discover mistakes after a bad patch.

### 4. Local-Model Operations As A First-Class Constraint

Detailed design: [design-local-model-operations.md](design-local-model-operations.md).

Idiom exists because local and smaller models need more harness help than
frontier cloud models. The project should embrace that instead of treating it as
a handicap.

Local-model constraints include:

- limited editing reliability
- weaker long-horizon state retention
- higher sensitivity to prompt bloat
- higher risk of malformed tool calls
- expensive context if full prompts are repeatedly rebuilt
- local memory and concurrency limits
- separate route-classifier and implementer models

Idiom-specific capabilities that matter here:

- backend management for llama.cpp, MLX, and Ollama-compatible setups
- a local queue daemon for OOM-safe lane concurrency
- route classifier health and degraded-mode logging
- context compaction, budget ledgers, and stop accounting
- redacted local conversation logs and telemetry
- `harness_audit` and `harness_feedback` for self-diagnosis

Differential: Idiom should be the harness that lets a capable local model act
more like a frontier coding agent because the host supplies missing structure.

### 5. The Eval, Audit, And Learning Flywheel

Detailed design: [design-eval-audit-learning-flywheel.md](design-eval-audit-learning-flywheel.md).

The strongest long-term reason to continue Idiom is the improvement loop:

1. Run a lane.
2. Capture typed tool requests, tool results, route plans, workflow state,
   verification, stop reasons, and final answers.
3. Score behavior with evals and audits.
4. Feed localization and contract failures back into ranking, packet generation,
   route policy, prompts, or deterministic gates.
5. Re-run the same scenario and measure whether behavior improved.

Existing pieces:

- conversation JSONL logs
- local telemetry JSONL
- `idiom-eval`
- `idiom-swe-eval`
- `harness_audit`
- `harness_feedback`
- `context_pack` cache and ranker scores
- ranker learning from SWE groundedness and manual CLI nudges

The flywheel is not complete enough yet. The current behavior audit on
2026-05-12 still reports redirect storms, zero-hit searches, truncated searches,
same-tool repeats, malformed edit arguments, incomplete implement session
summaries, mostly stale context-cache rows, and idle/dormant ranker trigger
sites. Those are not just bugs; they are the project backlog.

Differential: Idiom should become the coding harness that can explain why it
failed and turn that explanation into a measurable improvement.

## What Idiom Is Not

Idiom should not compete on these axes:

- general-purpose polish versus Aider
- number of supported chat niceties
- broad language count as a headline
- automatic git commits as the core identity
- prompt files as the main extension mechanism
- "more tools" as a proxy for intelligence
- hidden orchestration that cannot be replayed from logs

When Aider already solves a user need well, Idiom should either integrate with
it, learn from it, or ignore that lane. Idiom should not chase surface parity
without a contract, memory, eval, or local-model reason.

## Product Definition

Idiom is a local agent runtime with these user-facing promises:

1. It can run a local or smaller model against a real repository without giving
   the model unbounded authority.
2. It can provide task-scoped repository evidence from a persistent graph rather
   than repeated raw file wandering.
3. It can prove what contract was active for a run: allowed tools, forbidden
   tools, editable paths, evidence paths, verification expectations, budget, and
   completion gate.
4. It can reject unsafe, incomplete, stale, or unverifiable work for concrete
   reasons.
5. It can replay, audit, and score behavior from logs.
6. It can improve localization and workflow decisions from eval and feedback
   signals.
7. It can expose the same capabilities to other agents through `idiom tool`,
   making Idiom useful as an agent substrate even when it is not the chat UI.

If these promises are not visible to users, the project has not differentiated.

## Roadmap

The roadmap is organized around making the thesis true in observable behavior.
Each phase should land with a doc update, focused tests, and at least one eval or
audit signal that proves the phase mattered.

### Phase 0: Make The Thesis Operational

Goal: stop treating this document as strategy prose and turn it into a work
filter.

Tasks:

- Link this thesis from `AGENTS.md` and `.codex/project-context.md`.
- Update active planning docs so new work maps to one or more thesis pillars:
  contract, memory, audit/eval, local-model operations, or typed workflow.
- Add a lightweight checklist to new feature briefs:
  - Which contract does this clarify or enforce?
  - Which evidence source proves it?
  - Which eval/audit signal should improve?
  - What Aider/frontier feature are we intentionally not copying?
- Triage existing backlog items into thesis-aligned and parity-chasing buckets.
- Stop adding prompt reminders when the failure should be represented as route
  metadata, tool schema, runtime brief, gate logic, or audit output.
- Treat prompt-derived authority as a bug unless it is compiled into a typed
  contract field. Required coverage, candidate scope, exact snippets,
  edit-kind obligations, recovery attempts, and resume state should be
  inspectable in logs without rereading prompt prose.

Acceptance:

- Every new harness feature can name the thesis pillar it advances.
- New prompt text must name the canonical owner it depends on or it does not
  land.
- Docs distinguish "competitive parity" from "strategic differential."

### Phase 1: Stabilize The Contract Surface

Goal: make the harness gates boring and explainable.

Tasks:

- Reduce completion-gate false positives and repeated rejection loops.
- Ensure every implement run emits a session summary and process closeout.
- Normalize stop reasons so `done`, `blocked`, `budget_exhausted`,
  `verification_failed`, and `contract_rejected` are distinct in logs.
- Strengthen malformed edit/tool argument feedback with schemas and examples
  rather than broad recovery prose.
- Make target-role enforcement visible in the dynamic tool-policy snapshot.
- Ensure final-answer readiness reports the exact missing contract when it
  rejects a run.
- Add regression tests for the top behavior-audit findings, starting with:
  redirect storms, same-tool repeats, malformed edit args, and final answers
  without post-write verification.

Metrics:

- `harness_audit` behavior findings for redirect storms and same-tool repeats
  decline by at least 50 percent on the newest 80 conversation logs.
- Incomplete implement session lifecycle findings go to zero.
- Every write-capable lane has a post-write verification status, even when the
  status is a concrete blocker.
- Rejection packets expose at least one safe next action or a clear blocker.

### Phase 2: Make Repository Memory Measurable

Goal: make `context_pack` and graph views demonstrably better than raw search
for localizing changes.

Tasks:

- Build a context-localization benchmark from existing logs:
  - prompt
  - actual changed paths
  - context_pack top-k candidates
  - broad reads/searches used before first edit
  - verification result
- Add an eval oracle for localization quality:
  - top-1, top-3, top-5 hit rate against changed files
  - false-positive hub inflation
  - missing required-test suggestion
  - stale-context prevention
- Wire ranker learning beyond manual/eval no-ops:
  - boost files that appear in successful verified edits
  - penalize files repeatedly surfaced before zero-hit or wrong-scope sessions
  - record why the update happened in `.idiom/ranker/learning_log.jsonl`
- Improve `context_query` and `context_zoom` so graph-shaped questions avoid
  full-file reads when symbol or relationship evidence is enough.
- Integrate `context_trace_pack` into packet generation for failing tests and
  runtime errors.
- Make cache freshness visible at the point of use, not only in audits.

Metrics:

- Top-5 localization hit rate improves on the captured benchmark.
- Average generic `read_file` plus `search_content` calls before first edit
  decreases.
- `context_pack` cache hit/freshness counters show real reuse during repeated
  plan/execute workflows.
- Stale runtime trace warnings are either resolved or intentionally ignored with
  a typed reason.

### Phase 3: Close The Eval And Audit Loop

Goal: make failures automatically become reviewable, reproducible work items.

Tasks:

- Promote `harness_audit` behavior findings into deterministic review packets
  under `.idiom/audits/watch`.
- Make `harness_feedback` categories actionable:
  - `tool_gap` routes to tool-schema or tool-result evidence work
  - `context_gap` routes to graph/view/ranker work
  - `verification_gap` routes to completion/verification contracts
  - `routing_gap` routes to classifier/control-plane work
  - `planning_quality` routes to packet, budget, or plan-audit work
- Add a command that converts a failed log into a candidate SWE scenario or
  focused regression brief.
- Track before/after metrics for each fixed audit cluster.
- Add an "agent improvement loop" document that explains how logs, evals,
  audit, ranker learning, and tests connect.

Metrics:

- At least one behavior-audit cluster per week is converted into a regression
  test, eval scenario, or deterministic audit rule.
- `harness_feedback` reports are grouped by component and trend over time.
- `idiom-swe-eval` includes representative scenarios for contract failures,
  localization failures, verification failures, and plan/build handoff failures.

### Phase 4: Collapse Tool Complexity Into Capabilities

Detailed design: [design-capability-oriented-tooling.md](design-capability-oriented-tooling.md).

Goal: keep the model reasoning in task terms instead of language-tool menus.

Tasks:

- Define a capability vocabulary:
  - inspect symbol
  - find definition
  - find references
  - explain dependency
  - check source sanity
  - trace runtime failure
  - localize change
  - verify edit
  - audit behavior
- Route capabilities to concrete tools based on path, language, backend
  availability, and freshness.
- Keep language-prefixed tools available as implementation details, but stop
  making prompts teach the model a language-specific strategy menu.
- Add tool-result fields that report precision tier, fallback used, confidence,
  and next safe capability choices.
- Update recovery packets to report violated contracts and available
  capabilities rather than prescribing one exact next tool.

Metrics:

- Tool manifest remains aligned with schemas, but prompt-visible tool guidance
  shrinks.
- Generic read/search share declines when language or graph capabilities are
  available.
- Behavior audit can distinguish "capability unavailable" from "model chose
  generic fallback."

### Phase 5: Make Local-Model Operation A Product Strength

Goal: make Idiom the reliable way to run local coding agents, not just a local
demo.

Tasks:

- Add a local-model readiness profile that reports:
  - backend binary
  - model path
  - context size
  - route classifier status
  - implementer status
  - queue pool
  - memory-risk estimate
  - known missing language backends
- Make `idiom doctor` include lane-readiness checks for plan-execute,
  implement, audit, and SWE eval.
- Add queue-aware lane examples and failure messages.
- Add a benchmark that records throughput, model-call count, tool-call count,
  verification rate, and memory pressure for a small fixed suite.
- Prefer compact structured evidence over prompt bloat for local models.

Metrics:

- New machine setup reaches a passing `idiom doctor` with actionable remedies.
- Queue-managed parallel lanes do not oversubscribe the configured backend pool.
- Local smoke suite tracks time, stop reason, verification, and memory-relevant
  counters across runs.

### Phase 6: Become An Agent Substrate

Detailed design: [design-agent-substrate-sidecar.md](design-agent-substrate-sidecar.md).

Goal: make Idiom useful even when another agent is the front end.

Tasks:

- Treat `idiom tool` as a stable external-agent API.
- Document contracts and JSON schemas for context, audit, verification, and
  plan-packet tools.
- Add examples for Codex, Claude Code, OpenCode, and Aider users invoking Idiom
  as a local context/audit/verifier sidecar.
- Expose machine-readable capability discovery:
  - available languages
  - active backends
  - known graph freshness
  - supported contracts
  - recommended verification commands
- Keep project-specific adapters under `.idiom/` while canonical behavior stays
  embedded in the binary.

Metrics:

- An external agent can call `idiom tool context_pack`, inspect the resulting
  candidate paths, run `context_zoom`, and then call `harness_audit` without
  starting an Idiom model session.
- Public docs show at least three sidecar workflows:
  - planning context sidecar
  - verification/audit sidecar
  - log-to-regression sidecar

## Immediate Next Steps

These are the next concrete pieces of work that should happen before expanding
the surface area.

1. Add this thesis to the required project reading path.
2. Run `harness_audit` before and after the next three harness changes and track
   whether the targeted finding actually changes.
3. Create a `context_pack` localization eval from 20 recent successful and
   failed implementation logs.
4. Wire the next non-manual weight-mutating ranker-learning trigger from
   verified edits or `harness_feedback`; recovery reconciliation is already
   evidence-only.
5. Fix incomplete implement session lifecycle logging.
6. Turn the top redirect-storm and same-tool-repeat patterns into regression
   tests.
7. Add a docs page for "Idiom as sidecar tools for other coding agents."
8. Add an audit-watch review packet example to the repo so the improvement loop
   has a concrete artifact shape.
9. Define the capability vocabulary and map it to existing language and graph
   tools.
10. Update feature-request templates or planning briefs to require thesis-pillar
    alignment and measurable acceptance.

## Decision Gates

Continuing Idiom is justified if the next work cycles make these claims more
true:

- local models complete bounded tasks with fewer broad reads/searches
- `context_pack` increasingly predicts the files that actually change
- completion gates reject bad endings without causing retry storms
- evals and audits catch recurring failures before hand inspection
- external agents can benefit from Idiom's context and audit tools
- setup and local-model operation become more reproducible

The project should reconsider or narrow scope if:

- most effort goes into matching Aider's UI or chat features
- repository memory does not outperform ordinary search in localization evals
- harness audit findings stay flat after targeted fixes
- prompt layers keep growing instead of contracts becoming clearer
- local-model support remains operationally fragile
- external agents cannot use Idiom tools without understanding Idiom internals

The pivot path is not failure. If Idiom cannot justify itself as a complete
agent harness, the most valuable subset is still a local context, audit,
verification, and eval sidecar for other agents.

## Strategic Slogan

Do not build a better Aider clone.

Build the local harness that makes smaller agents trustworthy: evidence in,
contracts enforced, verification recorded, failures audited, learning retained.
