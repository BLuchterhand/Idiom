# Eval, Audit, Feedback, And Ranker-Learning Flywheel

Status: design draft
Date: 2026-05-12

This is one pillar in the coordinated roadmap:
[idiom-design-roadmap.md](idiom-design-roadmap.md). It consumes the traces from
contracts, memory, capabilities, handoff, local-model operations, and sidecar
calls, then turns failures into measurable improvements.

## Objective

Idiom should turn every meaningful agent run into evidence that can improve the
harness. The flywheel is:

1. Capture structured traces from local runs, replay runs, live evals, audit
   watch, and feedback reports.
2. Score those traces with deterministic regression checks and benchmark
   metrics.
3. Promote failures into durable scenarios or SWE-style tasks.
4. Feed verified context-quality signals into the codebase ranker.
5. Re-run the same scenarios to prove the harness improved without narrowing
   the model's strategy.

This design follows Idiom's north-star split: the model owns strategy; the
harness owns contracts, permissions, evidence, verification, completion gates,
and repeatable measurement. The loop should preserve that split. A failure in
tool contracts, stale context, verification, audit coverage, or rank ranking
should become typed evidence and a regression. It should not become a
scenario-specific prompt reminder unless the root cause is actually prompt
authority.

The target outcome is a local harness that can answer four questions from the
same evidence trail:

- Did the task succeed?
- Did the harness preserve the required contracts while the model worked?
- Did the context and ranker put useful code in front of the model?
- Did a new failure become a regression, an audit rule, a feedback item, or an
  explicit non-goal?

## External Evidence

SWE-bench established the basic external shape for coding-agent evaluation:
agents are evaluated against real GitHub issues, repository state, generated
patches, and test outcomes rather than only textual answers
(https://www.swebench.com/, https://github.com/SWE-bench/SWE-bench).
That gives Idiom a useful outcome metric family: resolved rate, patch
applicability, test pass rate, and repository-level correctness.

SWE-agent adds a second lesson: the "agent-computer interface" and trajectory
matter, not just the final patch. Its 2024 arXiv preprint emphasizes that tool
design and interaction protocol substantially affect SWE-bench performance
(https://arxiv.org/abs/2405.15793). The SWE-agent inspector also treats
trajectories as first-class artifacts for debugging runs
(https://swe-agent.com/latest/usage/inspector/). Idiom should therefore score
trajectory quality, control-rail behavior, tool-loop failures, and auditability
alongside task outcome.

OpenAI's agent-evaluation guidance uses traces first for debugging and then
datasets plus graders for repeatable eval runs. Traces capture model calls,
tool calls, guardrails, handoffs, and custom spans; evals then use datasets and
graders to score behavior at scale
(https://developers.openai.com/api/docs/guides/agent-evals,
https://developers.openai.com/api/docs/guides/agents/integrations-observability#tracing,
https://developers.openai.com/api/docs/guides/evaluation-getting-started).
This maps directly to Idiom's JSONL logs, scenario graders, SWE suites, and
audit-watch packets.

Codex exposes operational hooks and telemetry surfaces that reinforce the same
pattern. Its configuration includes local log directories, history settings,
OpenTelemetry exporters, sandbox and approval policies, and feature flags for
hooks and memories (https://developers.openai.com/codex/config-reference).
Codex hooks can observe or influence lifecycle events such as `SessionStart`,
`UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, and
`Stop`, with a `transcript_path` available to hook handlers
(https://developers.openai.com/codex/hooks). Idiom's equivalent should remain
local-first and deterministic, but the event boundaries are useful external
confirmation.

Claude Code exposes a similar hook model with JSON input, transcript paths,
tool hooks, stop hooks, compact hooks, and session lifecycle hooks
(https://code.claude.com/docs/en/hooks). OpenCode exposes per-agent
permissions and event-capable plugins
(https://opencode.ai/docs/agents/, https://opencode.ai/docs/plugins/). These
systems converge on the same harness design: durable transcripts, typed
lifecycle events, permission policy, and extension points for validation or
logging.

Codebase-Memory is the relevant research anchor for Idiom's context memory and
ranker metrics. Its 2026 arXiv preprint describes a persistent tree-sitter
knowledge graph exposed through MCP to reduce repeated repository exploration
while preserving code quality (https://arxiv.org/abs/2603.27277). Idiom should measure not only
whether tasks pass, but whether `context_pack`, graph cache, freshness policy,
and learned ranking reduce unnecessary reads and tool calls while keeping edit
targets in the top-ranked context.

## Current Idiom Capabilities

This section is based on the current repository docs and code inspection on
2026-05-12.

### Conversation Logs

Idiom records canonical conversation JSONL under `.idiom/logs/conversations`
and also reads legacy or alternate locations such as `logs/conversations`.
Records include prompt/context bundles, user and assistant messages, tool
requests, tool results, child tool calls, errors, server state, and transcript
metadata. Tool request records preserve step, lane, broad/narrow state,
discovery class, scope, and cost where available.

These logs are the primary evidence trail for behavior bugs involving routing,
readiness, prompt layers, recovery, control rails, and completion decisions.
They are also the input for replay scenarios and generated regression suites.

### Telemetry

Process telemetry is written as local JSONL with schema
`idiom.telemetry.v1`, normally under `logs/telemetry`. It records lifecycle
events such as `session_start`, `process_start`, arbitrary harness events, and
`process_exit`. The telemetry recorder redacts prompt-like fields and does not
store raw tool output by default.

Current telemetry streams include TUI, prompt, server, artifact, implement,
architect, and `idiom tool` records. Fields cover timing, output sizes,
discovery metadata, errors, context fields, feedback fields, and process
lifecycle. Telemetry can be redirected with `IDIOM_TELEMETRY_DIR` and disabled
with `IDIOM_TELEMETRY=0`.

### Replay Eval

`cmd/idiom-eval` evaluates a captured log against a scenario. Scenarios can
assert prompt matchers, required and forbidden final-answer content, required
and forbidden tools, required discovery tools or discovery classes, phase
events, route plans and replans, verification state, changed files, evidence
paths, approval behavior, readiness, deepening, final critic, effort, workflow,
budget ledger, claim confidence, context compaction, route ambiguity,
verification feedback, tool-failure confidence, and control rails.

`idiom-eval` can also generate a starter scenario from a log. That generated
scenario is useful triage material, but it should not be treated as a finished
oracle until a human or deterministic reducer has strengthened the assertions.

### SWE Eval Suite

`cmd/idiom-swe-eval` scores a corpus of task logs and suite oracles. The suite
schema supports task families, tags, fixtures, lanes, follow-up route reasons,
contracts, thoroughness levels, verification expectations, scenario
assertions, readiness/deepening/final-critic expectations, effort and long
horizon metadata, negative evidence, tool surfaces, forbidden tool surfaces,
file surfaces, and evidence surfaces.

Oracles can assert changed files, forbidden changed files, file content,
maximum tool requests, duration and error budgets, minimum turns, no approval,
no shell, no summary rejection, groundedness, and generic phrase forbids. The
suite linter verifies coverage and catches inconsistent task definitions.

On 2026-05-12, the representative suite check:

```sh
go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json
```

passed suite validation for 44 tasks. That run validates suite structure and
coverage, not live task success.

### Performance Eval

`cmd/idiom-perf-eval` analyzes one or more logs for anti-patterns, tool mix,
context stats, and feedback stats. It reports repeated tool calls, zero-hit
searches, redirect storms, context packet usage, freshness signals, candidate
file counts, feedback categories, and aggregate behavior across logs.

This is the right bridge between raw traces and benchmark reporting: it turns
unstructured behavioral smell into measurable trend lines.

### Harness Audit

`harness_audit` is a read-only workspace tool with purposes such as
`tool_surface`, `behavior`, and `planning_quality`. The current report includes
findings, tool surface summaries, language tooling, backend availability, log
behavior, planning quality, ranker learning, context cache health, freshness
health, improvement questions, and consolidation candidates.

A behavior audit run on 2026-05-12 scanned 71 selected logs and reported:

- 8 findings.
- Medium findings for redirect storms and zero-hit searches.
- Low findings for same-tool repeats, truncated search content, and a tool
  error streak.
- A high finding for an incomplete implement session lifecycle.
- An info finding that ranker learning is idle.
- An info finding that most `graph_views` cache rows are stale.

The same audit reported no existing `.idiom/ranker/weights.json` or ranker
learning log and listed `eval`, `cli`, `verified_edit`, `harness_feedback`, and
`audit_watch` as currently dormant learning sources.

### Harness Feedback

`harness_feedback` records structured observations about harness gaps. Current
categories include tool gaps, prompt gaps, workflow gaps, context gaps, stale
context, language support, routing gaps, verification gaps, telemetry gaps,
audit gaps, planning quality, packet quality, impact ranking, plan audit gaps,
budget gaps, replay/eval gaps, missing affordances, bad scope ranking, stale
facts, backend gaps, packet bloat, read loops, edit-readiness failures,
unsupported plan claims, validation gaps, language precision gaps, and other
gaps.

Feedback records include expected behavior, observed behavior, workaround,
impact, evidence, confidence, severity, suggested fix, path, and task id. They
are already structured enough to become both audit inputs and ranker-learning
candidate inputs, provided the promotion step remains guarded.

### Audit Watch

`idiom audit watch` polls conversation and telemetry JSONL locations, debounces
completed implement sessions and high-value trigger events, runs behavior
audits for triggered paths, and writes review packets under
`.idiom/audits/watch`.

Current triggers include implement process exit, `harness_feedback`,
completion-gate records, high-risk stop reasons, malformed tool input, blocked
completion statuses, and stale context events. High-risk stop reasons include
repeated completion-gate rejection, required paths remaining, retry blocked,
source edit verification required, exhausted tool-step budgets, write-only
recovery failures, exact-anchor prewrite hiding, and repeated exact-anchor
write failures.

### Context Pack Cache And Freshness

`context_pack` creates ranked packets of candidate files, symbols, tests, and
evidence. It stores packet views in `.idiom/comprehension/graph.sqlite` in the
`graph_views` table. The cache key is content-addressed over graph index id,
task, mode, path, scope, limit, token budget, required tests, forbidden paths,
and intent. The cache is bypassed when freshness checks say the working tree is
not fresh.

Current known limitations are documented in the repo: graph ids include build
time, ranker weight changes and runtime trace changes are not part of the cache
key, and edited-but-unrefreshed working trees recompute instead of trusting the
cache. `harness_audit` exposes cache rows, fresh/stale rows, hit/miss counts,
stale preventions, and freshness telemetry.

### Ranker Learning

`context_pack` and `context_impact` use learnable per-workspace multipliers in
`.idiom/ranker/weights.json`. The implementation starts paths at a baseline
weight, applies bounded boosts or penalties, decays existing weights, clamps to
minimum and maximum weights, and appends learning events to
`.idiom/ranker/learning_log.jsonl`.

Current wired learning paths include SWE groundedness and manual CLI commands:

- SWE groundedness score `>= 0.75` boosts oracle context files.
- Score `< 0.40` penalizes oracle context files.
- Scores in the middle band produce no weight change.
- `idiom ranker boost`, `idiom ranker penalize`, `idiom ranker reset`, and
  `idiom ranker show` provide manual control.

The implementation already defines dormant or future source labels for
`verified_edit`, `harness_feedback`, and `audit_watch`. The flywheel should
activate those sources only through deterministic adapters with evidence and
rollback visibility.

## Target Feedback Loop

The desired loop is intentionally boring and repeatable:

1. A user, lane, replay, live eval, or external harness run starts.
2. The runner emits conversation records, telemetry events, tool events,
   verification events, budget records, context records, and final status.
3. `audit watch` or an explicit audit command groups relevant logs into a
   review packet with trigger reasons and behavior findings.
4. Replay eval or SWE eval scores the same evidence against existing
   scenarios, or generates a candidate regression from a new failure.
5. A triage step assigns each real failure to one owner surface: contract,
   control rail, tool schema, readiness, verification, audit coverage,
   telemetry, context freshness, ranker ranking, prompt authority, or explicit
   non-goal.
6. The owner surface is changed with the narrowest fix that addresses the
   evidence.
7. If context ranking was part of the failure or success, a deterministic
   learning adapter emits a bounded ranker-learning event.
8. The same captured scenario, the relevant SWE task bucket, and a focused
   audit are re-run.
9. The packet is closed with before/after evidence, or left open with a
   concrete missing probe.

Every feedback edge should preserve provenance. No learning update should exist
without source, task id or session id, action, files, score or reason, and a
linkable evidence path. No regression should exist without a source log,
fixture or repository state, expected changed-file policy, and clear oracle
intent.

## Data Schema And Events

The current schemas are spread across conversation logs, telemetry, replay
scenarios, SWE suites, audit reports, feedback records, and ranker logs. The
flywheel does not need one mega-schema. It needs stable IDs and event fields
that let these records join cleanly.

### Shared Event Envelope

New or revised events should use this shared envelope where practical:

```json
{
  "schema": "idiom.event.v1",
  "timestamp": "2026-05-12T00:00:00Z",
  "workspace_root": "/Users/ben/Documents/GitHub/idiom",
  "session_id": "local-session-id",
  "task_id": "optional-suite-or-regression-id",
  "run_id": "optional-live-run-id",
  "source": "conversation|telemetry|eval|swe_eval|audit|audit_watch|feedback|ranker",
  "event_type": "tool_request|tool_result|verification|audit_finding|learning_update",
  "lane": "implement|plan_execute|architect|refine|unknown",
  "step": "optional-runner-step",
  "record_id": "stable-record-id",
  "parent_record_id": "optional-parent-record-id"
}
```

The envelope is not a replacement for existing records. It is the join contract
for future records and summaries.

### Tool And Trajectory Events

Tool events should preserve the current fields and normalize these keys:

```json
{
  "tool_name": "context_pack",
  "tool_namespace": "workspace",
  "tool_request_id": "req-123",
  "discovery_class": "context_pack|search|read|edit|shell|verification|other",
  "scope": "repo|path|symbol|log|external",
  "cost_class": "low|medium|high",
  "input_summary": "redacted deterministic summary",
  "output_summary": "counts, paths, symbols, tests, uncertainty",
  "status": "ok|error|rejected|blocked|timeout",
  "error_class": "optional-normalized-class",
  "duration_ms": 123
}
```

Output summaries should report facts, paths, symbols, counts, uncertainty, and
bounded excerpts. They should not prescribe the next tool call.

### Contract And Gate Events

Control rails and gates should emit enough evidence to debug authority leaks:

```json
{
  "event_type": "contract_check",
  "contract": "permission|protected_path|verification|completion|readiness|budget|freshness",
  "decision": "allow|nudge|block|repair|stop",
  "reason": "required_edit_paths_remaining",
  "evidence_paths": ["internal/agent/readiness.go"],
  "missing_claim": "optional exact missing claim",
  "next_probe": "optional exact probe if one exists",
  "budget_remaining": 4
}
```

This event shape directly supports replay assertions for completion gates,
readiness, deepening, verification feedback, budget ledger, and final critic.

### Audit Finding Events

Audit findings should be promotable into regressions and dashboards:

```json
{
  "event_type": "audit_finding",
  "finding_id": "behavior_zero_hit_search",
  "severity": "high|medium|low|info",
  "category": "behavior|planning_quality|context_cache|freshness|ranker_learning|tool_surface",
  "source_logs": ["logs/conversations/example.jsonl"],
  "matched_records": 11,
  "summary": "Zero-hit searches repeated across three logs",
  "owner_surface": "tooling|audit|ranker|prompt|contract|docs|non_goal",
  "promotion_status": "open|promoted|fixed|declined",
  "regression_task_id": "optional",
  "learning_candidate_id": "optional"
}
```

`owner_surface` and `promotion_status` are the important additions. They turn
audit from a report into a work queue.

### Ranker Learning Events

The current learning log is close to the target. The event should remain small
and append-only:

```json
{
  "timestamp": "2026-05-12T00:00:00Z",
  "source": "eval|cli|verified_edit|harness_feedback|audit_watch",
  "action": "boost|penalize|no_op|reset",
  "score": 0.82,
  "files": ["internal/workspace/harness_audit.go"],
  "task_id": "swe-task-or-session-id",
  "evidence": ["logs/conversations/example.jsonl"],
  "reason": "groundedness_high",
  "applied": true,
  "weight_before": 1.0,
  "weight_after": 1.15
}
```

Only relative repository paths should be accepted. Absolute paths, escaping
paths, generated artifacts outside the repository, and paths from failed
oracles should be ignored unless the action is an explicitly justified penalty.

### SWE Task Metadata

Generated regression tasks should keep the existing suite shape and add only
minimal provenance:

```json
{
  "id": "regression-required-paths-remaining-20260512",
  "family": "failure_repair",
  "tags": ["audit_watch", "completion_gate"],
  "source_log": "logs/conversations/example.jsonl",
  "source_audit_packet": ".idiom/audits/watch/example.json",
  "created_from": "audit_finding:required_edit_paths_remaining",
  "prompt": "...",
  "scenario": { "path": "testdata/swe/scenarios/example.json" },
  "oracle": { "no_changed_files": false }
}
```

The provenance fields make generated tasks reviewable without requiring every
consumer to parse the original audit packet.

## Ranker Learning

Ranker learning should optimize context ordering, not decide what the model is
allowed to do. Permissions, protected paths, required paths, and verification
contracts stay outside the ranker.

### Existing Mechanics

Current learned weights are local to the workspace:

- Storage: `.idiom/ranker/weights.json`.
- Log: `.idiom/ranker/learning_log.jsonl`.
- Default weight: `1.0`.
- Weight decay: applied before each update.
- Boost and penalty: bounded increments.
- Clamp: minimum and maximum weights prevent runaway drift.
- Invalid paths: ignored if absolute or outside the repository.

The current SWE groundedness adapter is intentionally conservative: high
groundedness boosts, low groundedness penalizes, and the middle band records no
change.

### Learning Sources

The target source list is:

- `eval`: already wired through SWE groundedness. This remains the most
  trustworthy automatic source because it is tied to an oracle.
- `cli`: already wired for explicit human operations.
- `verified_edit`: proposed. Boost files that were present in the packet and
  then edited or cited in a passing verification run. Penalize surfaced files
  only when a deterministic check proves they were misleading, stale, or
  unrelated.
- `harness_feedback`: proposed. Promote feedback categories such as
  `packet_quality`, `impact_ranking`, `bad_scope_rank`, `stale_context`, and
  `packet_bloat` into candidate ranker events only when severity and evidence
  are present.
- `audit_watch`: proposed. Convert repeated audit findings about context
  misses, stale packet use, bad rank order, or low groundedness into candidate
  learning events. Audit watch should generate candidates; a deterministic
  adapter decides whether to apply them.

### Promotion Rules

Automatic ranker updates require:

- A source label.
- A task id, session id, or audit packet id.
- One or more repository-relative files.
- A score or normalized reason.
- A pass/fail context. Passing task signals may boost. Failing task signals may
  penalize only when the oracle isolates context ranking as the failure cause.
- A learning log entry whether the action is applied or skipped.

Updates should be skipped when:

- The task failed for a non-context reason.
- The evidence names only directories or generated files.
- The files are outside the repository or protected by policy.
- The source is model prose without deterministic support.
- The same source and task id already produced an equivalent event.

### Ranker Metrics

The ranker should be evaluated with retrieval metrics in addition to task
outcome:

- `hit_at_1`, `hit_at_3`, `hit_at_5`, and `hit_at_10` for oracle context files.
- Mean reciprocal rank of first relevant edit or evidence file.
- Edit-target recall in `context_pack` candidate files.
- Evidence-file recall in `context_pack` candidate files.
- First relevant rank before and after learning.
- Packet token budget per relevant file.
- Stale candidate rate.
- No-op learning rate for middle-band groundedness.
- Drift alerts when one path family dominates learned weights.

These metrics align Idiom with Codebase-Memory-style goals: less repeated
exploration and lower token/tool cost without losing relevant context.

## Audit-Watch Packets

Audit watch should produce review packets that are useful to humans, eval
generators, and future automation. The packet is not a fix by itself. It is a
deterministic summary of why a run deserves review.

Target packet fields:

```json
{
  "schema": "idiom.audit_watch_packet.v1",
  "generated_at": "2026-05-12T00:00:00Z",
  "workspace_root": "/Users/ben/Documents/GitHub/idiom",
  "trigger_files": ["logs/telemetry/implement.jsonl"],
  "trigger_reasons": ["completion_gate", "harness_feedback"],
  "sessions": ["session-id"],
  "audits": ["embedded-or-linked-harness-audit-report"],
  "findings": ["behavior_zero_hit_search"],
  "top_failure_class": "completion_contract|tool_loop|context_staleness|ranker_miss|verification_gap",
  "candidate_regressions": ["regression-task-id"],
  "candidate_learning_events": ["learning-candidate-id"],
  "owner_surface": "contract|tooling|ranker|audit|telemetry|prompt|docs|non_goal",
  "status": "open|promoted|fixed|declined",
  "reproduce": {
    "audit": "idiom tool harness_audit --input '{\"purpose\":\"behavior\"}'",
    "eval": "go run ./cmd/idiom-eval -log ... -scenario ..."
  }
}
```

Audit watch should continue to debounce noisy events. It should prefer stable
trigger identities over raw line offsets so a log append does not create a new
packet for the same completed session. It should never mutate ranker weights or
suite files directly; it should create candidates with enough evidence for the
promotion rules above.

## Log-To-Regression Workflow

The regression workflow should be explicit enough that a future agent can
follow it without reinterpreting the failure from scratch.

1. Identify the source failure.
   Start from a high or medium audit finding, a failed SWE task, a failed
   replay scenario, or a structured `harness_feedback` record.

2. Select the canonical evidence.
   Pick the smallest log or packet that reproduces the behavior. Prefer
   conversation logs for agent behavior, telemetry logs for lifecycle gaps, and
   audit-watch packets for grouped trigger behavior.

3. Generate a starter scenario.
   Use `idiom-eval -write-scenario` for a single log or
   `idiom-swe-eval -write-failing-scenarios` for suite failures. Treat the
   generated file as a draft.

4. Strengthen the oracle.
   Add required and forbidden tools, expected route or control-rail events,
   changed-file policy, approval expectations, verification expectations,
   groundedness context, max tool or error budgets, and final-answer
   constraints. Remove incidental assertions that would overfit to a harmless
   run detail.

5. Add suite metadata.
   Assign family, tags, lane, contracts, verification expectation, file
   surfaces, evidence surfaces, and negative-evidence flags. The suite linter
   should explain what coverage bucket the task contributes to.

6. Reproduce the failure.
   Run `go run ./cmd/idiom-eval ...` or
   `go run ./cmd/idiom-swe-eval ... -logs ...` and confirm the scenario fails
   before the fix when possible. For live-only failures, record the reason a
   captured replay is insufficient.

7. Fix the owner surface.
   Change the contract, gate, tool schema, telemetry, audit rule, freshness
   policy, ranker adapter, or prompt authority that actually caused the
   failure. Prefer removing conflicting affordances or encoding typed contracts
   over adding prompt reminders.

8. Re-run focused and aggregate checks.
   Run the focused replay or SWE task, the relevant suite bucket, and a focused
   `harness_audit`. For shared behavior, run the representative suite and the
   default project verification.

9. Promote ranker learning if justified.
   Apply a ranker update only after the scenario outcome and context evidence
   identify a context-ranking signal. Record skipped candidates too.

10. Close the packet.
    Mark the audit-watch packet or feedback item as `fixed`, `promoted`, or
    `declined` with a reason and links to the scenario, code change, and
    before/after command results.

## Benchmark Metrics

Idiom needs benchmark metrics at several layers because a local harness can
fail while the final patch still happens to pass, or succeed operationally
while the patch is wrong.

### Outcome Metrics

- Task pass rate.
- SWE-style resolved rate.
- Test pass/fail count.
- Patch applicability.
- Required changed-file satisfaction.
- Forbidden changed-file violations.
- Required approval or no-approval satisfaction.
- Final-answer required and forbidden content satisfaction.

### Trajectory And Contract Metrics

- Route plan and replan correctness.
- Control-rail event presence.
- Completion-gate rejection counts and reasons.
- Readiness, deepening, and final-critic checks.
- Verification-plan and verification-feedback quality.
- Budget-ledger state.
- Claim-confidence events.
- Context-compaction events.
- Tool-failure confidence events.
- Assistant messages per user turn.

### Efficiency Metrics

- Total tool requests.
- Heavy discovery tool count.
- Broad-search count.
- Zero-hit search count.
- Repeated same-tool calls.
- Redirect storm count.
- Tool error streak count.
- Duration.
- Prompt and context token estimates where available.
- Repeated read rate for already-known files.

### Context And Ranker Metrics

- `context_pack` presence and packet id.
- Candidate file count, symbol count, and test count.
- Edit-target recall at k.
- Evidence-file recall at k.
- First relevant rank.
- Groundedness score.
- Stale packet prevention count.
- Cache hit, miss, stale row, and stale-prevention counts.
- Freshness refresh attempts and blockers.
- Learning updates by source and action.
- Ranker no-op rate.
- Weight drift concentration by path prefix.

### Audit And Feedback Metrics

- Audit findings by severity and category.
- Time from finding to promoted regression.
- Time from finding to fixed or declined.
- Feedback records by category and severity.
- Audit-watch trigger precision: packets that become regressions or accepted
  fixes divided by total packets.
- Audit-watch trigger recall: known regression failures that had a matching
  packet.
- Incomplete session lifecycle count.
- Dormant learning-source count.

### Suite And Corpus Metrics

- Suite lint status.
- Task count by family, tag, lane, route reason, contract, and verification
  expectation.
- Scenario assertion coverage.
- Oracle assertion coverage.
- Captured-log load failures.
- Missing log count.
- Live-vs-captured drift.
- Bucket pass rates by family and assertion type.

## Implementation Plan

### Phase 0: Baseline Inventory

Status: already started.

- Keep this design doc as the shared target.
- Preserve the current representative suite-only check as the baseline.
- Record the current behavior-audit snapshot: 71 logs scanned, 8 findings,
  ranker learning idle, and most graph-view cache rows stale.

### Phase 1: Stable Event Joins

- Normalize shared IDs across conversation logs, telemetry, audit packets,
  eval results, feedback records, and ranker logs.
- Add or document `session_id`, `task_id`, `run_id`, `record_id`, and
  `source_log` fields where they are missing.
- Keep redaction behavior local-first and conservative.
- Add tests that generated summaries do not include raw prompt bodies or raw
  tool output where telemetry promises redaction.

### Phase 2: Audit-Watch Packet Upgrade

- Version audit-watch packets as `idiom.audit_watch_packet.v1`.
- Add owner surface, promotion status, candidate regression IDs, candidate
  learning event IDs, and reproduce commands.
- Keep packet generation read-only.
- Add focused tests for trigger dedupe, malformed input triggers,
  completion-gate triggers, stale-context triggers, and feedback triggers.

### Phase 3: Regression Promotion Path

- Make log-to-regression promotion a documented command path.
- Ensure generated scenarios include source log provenance.
- Add suite-lint rules for generated regressions: source log required, family
  required, owner surface required, and at least one strong oracle assertion.
- Ensure `idiom-swe-eval -write-failing-scenarios` preserves enough oracle
  context to become reviewable without raw transcript spelunking.

### Phase 4: Ranker Learning Sources

- Keep `eval` and `cli` as the first-class wired sources.
- Add `verified_edit` learning only after passing verification and changed-file
  evidence are available.
- Add `harness_feedback` candidate learning for packet quality and impact
  ranking categories.
- Add `audit_watch` candidate learning for repeated context-ranking findings.
- Record no-op and skipped learning events so audit can distinguish dormant,
  healthy, and blocked sources.
- Add drift checks to `harness_audit` for path-prefix concentration and stale
  learned weights.

### Phase 5: Unified Benchmark Report

- Combine `idiom-swe-eval`, `idiom-perf-eval`, and `harness_audit` output into
  one report shape for local runs.
- Include outcome, trajectory, efficiency, context/ranker, audit/feedback, and
  suite/corpus metrics.
- Preserve raw JSON output for automation and a concise text summary for local
  terminal use.
- Add baseline comparison fields so a run can say which metrics improved,
  regressed, or lacked comparable data.

### Phase 6: CI And Release Gates

- Run suite-only validation on representative suites as a cheap default.
- Run captured regression logs when they are available and deterministic.
- Keep live SWE subsets opt-in or scheduled because local models and live
  repositories can be expensive and nondeterministic.
- Require focused evals for changes to prompts, runner policy, control rails,
  readiness gates, verification logic, discovery behavior, audit watch, or
  ranker learning.

### Phase 7: Closure Discipline

- Require every high or medium audit finding to have one of:
  `promoted`, `fixed`, `declined`, or `open with exact missing probe`.
- Link fixes to the source log, regression task, audit packet, and verification
  command.
- Track repeated declined findings as possible non-goals or noisy audit rules.

## Risks And Non-Goals

### Risks

- Bad oracles can teach the ranker the wrong lesson.
- Benchmarks can be gamed if trajectory assertions become too scenario-specific.
- Live evals can be flaky because local models, repository state, and tool
  availability vary.
- Logs can accidentally preserve sensitive data if redaction contracts regress.
- Ranker learning can overfit to recently edited files or popular path
  prefixes.
- Audit watch can create review fatigue if trigger precision is low.
- Generated scenarios can preserve incidental behavior and make useful harness
  improvements look like regressions.
- Cache freshness and ranker weights can interact in confusing ways if cache
  keys do not account for weight-changing events.

### Non-Goals

- Idiom is not trying to replace SWE-bench or become a public leaderboard.
- The flywheel does not train the underlying model.
- The ranker does not grant permissions, choose protected paths, or bypass
  verification.
- Audit watch does not auto-edit code, auto-commit changes, or directly mutate
  ranker weights.
- Regression generation does not remove the need to review oracles.
- The benchmark report is not a final-answer quality grader for arbitrary prose.
- The design does not require uploading local transcripts to a hosted service.

## Acceptance Criteria

The flywheel is working when these criteria are true:

- Every high or medium `harness_audit` finding can be mapped to an owner
  surface, promotion status, and source evidence.
- Audit-watch packets include trigger files, trigger reasons, findings,
  candidate regressions, candidate learning events, owner surface, status, and
  reproduce commands.
- Generated regression tasks include source log provenance and pass suite lint
  only when they have meaningful oracle assertions.
- Replay and SWE eval reports expose outcome, trajectory, efficiency,
  context/ranker, audit/feedback, and suite/corpus metrics.
- Ranker learning events always include source, action, files, task or session
  id, reason or score, applied/skipped state, and evidence.
- `harness_audit` can distinguish wired, dormant, healthy, skipped, and blocked
  ranker-learning sources.
- Context metrics report edit-target recall, evidence-file recall, first
  relevant rank, stale candidate rate, cache hit/miss/stale counts, and
  freshness blockers.
- A failing captured log can be promoted into a regression, fail before the
  fix, pass after the fix, and remain attached to the audit packet that found
  it.
- Suite-only validation remains cheap enough to run by default.
- Live evals remain opt-in or scheduled and publish enough metadata to compare
  live-vs-captured drift.
- No new feedback loop stores raw secrets or raw prompt bodies outside the
  existing conversation log contract.
- The system improves harness contracts and context ranking without hardcoding
  the model's next strategic step for a recurring scenario.
