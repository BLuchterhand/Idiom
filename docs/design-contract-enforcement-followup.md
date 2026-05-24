# Contract Enforcement Follow-Up Lessons

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: reviewed follow-up design backlog, drafted 2026-05-24

## Purpose

This document captures the broader lessons from Sponsio and the comparison to
Idiom. It is intentionally separate from
[design-contract-enforcement-immediate-stability.md](design-contract-enforcement-immediate-stability.md)
so the immediate stability effort can land without absorbing every product idea.

Assumption for this analysis: Sponsio is production-ready and Idiom is still
unstable.

## Product Comparison

Sponsio is a runtime policy engine for AI agents. Its core product is small and
sharp:

- users define or include contracts;
- contracts compile into deterministic checks over action trajectories;
- integrations intercept tool calls before side effects;
- observe mode records would-block events;
- enforce mode blocks or escalates violations;
- reports and benchmarks tell operators whether the contract set is useful.

Idiom is broader. It is a local coding-agent harness and sidecar:

- repository memory and freshness;
- context packets, semantic navigation, impact, and traces;
- typed lanes, planning handoff, executor contracts, verification, completion,
  recovery, and audit;
- local model operations and runtime envelopes;
- sidecar tools for other agents.

The lesson is not that Idiom should become Sponsio. The lesson is that Idiom's
contract layer needs a sharper enforcement kernel and rollout story, built on
the existing `EventGateDecision` / `ContractGateEvent` evidence surface rather
than a competing schema.

## Sponsio Capabilities Worth Learning From

### Small Atom Vocabulary

Sponsio's stability comes from reducing action history into simple atoms:

- tool called;
- cumulative count;
- argument contains regex;
- argument field contains regex;
- all argument paths stay inside prefixes;
- output contains regex;
- data contains or flows;
- prompt/response contains regex when that observation layer exists.

Idiom should eventually define its own atom vocabulary over runner and workspace
events. Candidate atoms:

- `tool_called(name)`
- `path_read(path)`
- `path_written(path)`
- `required_path_changed(path)`
- `source_body_observed(path)`
- `verification_passed(command)`
- `completion_blocked(reason)`
- `protocol_hash_present(hash)`
- `context_pack_fresh(pack_id)`
- `budget_exhausted(kind)`

These atoms should be facts for enforcement and replay. They should not replace
rich repository memory.

The immediate stability doc uses `observed_facts` as a bounded field on gate
decisions. This follow-up should promote those facts into a replayable atom
stream only after the immediate fields have enough production data to show which
facts are stable and useful.

### Temporal Contracts

Sponsio's useful deterministic patterns include:

- `must_precede(A, B)`
- `always_followed_by(A, B)`
- `rate_limit(A, N)`
- `bounded_retry(A, N)`
- `no_reversal(A, B)`
- `arg_blacklist(tool, field, patterns)`
- `scope_limit(tool, paths)`

Idiom equivalents could cover:

- source-body evidence must precede source edit;
- write must precede post-write verification;
- accepted verification must follow the relevant write;
- source-satisfied must not follow a changed required path without verification;
- repeated completion-gate rejection must stop or decompose;
- repeated identical discovery after answer-ready becomes guard friction.

### Contract Packs

Sponsio's contract packs are product packaging for known failure classes.

Potential Idiom packs:

- `coding/filesystem`: sensitive path denies, workspace scope, read-before-edit,
  whole-file write limits.
- `coding/shell`: destructive command patterns, privileged command approval,
  verification-only command allowlist.
- `coding/git`: force-push, branch mutation, commit-after-secret-read, dirty
  scope checks.
- `plan_execute/protocol`: planner/executor authority and terminal outcomes.
- `repo_memory/freshness`: stale packet use, stale trace use, cache validity.
- `verification/post_write`: mutation closeout and source-aware verification.
- `self_modify/guard_files`: prompts, runtime policy, telemetry, audit, and
  `.idiom` control artifacts.
- `sidecar/caller_contract`: external caller profile, path scope, freshness, and
  write disablement.

These should begin as first-party Go definitions, not a user-facing YAML DSL.
YAML can come later when the domain model is stable.

Before any user-facing pack format exists, each pack needs:

- a stable `pack_id`;
- included invariant IDs;
- allowed observation atoms;
- default enforcement mode;
- false-positive review notes;
- tests proving valid trajectories are not blocked.

### Observe-First Rollout

Sponsio makes observe mode the default adoption path. Idiom should apply this to
new gates and packs:

- hard gates remain hard where they already protect invariants;
- new detections start as `observe_only`;
- audit reports false positives, never-fired rules, and high-signal violations;
- only stable, low-noise rules graduate to enforcement.

### Anti-Tamper

Sponsio protects its own host plugin config from the guarded agent. Idiom needs
an equivalent for:

- prompt layers;
- runtime policy files;
- `.idiom` state that controls recovery, audit, ranker learning, or queueing;
- telemetry and audit disabling knobs;
- generated contract packs.

The first version should report observe-only except for already-protected paths.
Hard enforcement needs a clear operator-maintenance path.

Path taxonomy must be explicit. Prompt layers, runtime policy, audit disabling
knobs, and generated pack definitions are plausible guarded surfaces. `.idiom`
generated graph views, traces, refine drafts, and ordinary cache outputs are not
automatically protected just because they live under `.idiom`.

## What Idiom Should Not Copy Directly

### Natural-Language Contract Compilation

Natural-language-to-contract is useful for adoption, but it is not the first
stability lever. Idiom's current instability is more about scattered authority
and hard-to-compare gate evidence than lack of authoring ergonomics.

### Cloud Stochastic Atoms

LLM-judged atoms are valuable for semantic output quality, but they introduce
cost, latency, and another model dependency. Idiom should first make structural
contracts deterministic and replayable.

### External Framework Adapter Breadth

Sponsio integrates with many agent frameworks. Idiom's sidecar path should stay
focused on `idiom tool` and MCP until its own contract semantics are stable.

### Security Incident Marketing As Generic Policy

Incident packs are useful examples, but Idiom should avoid baking
scenario-specific narratives into core prompts or gates. Reusable packs must be
expressed as generic invariants.

## Follow-Up Roadmap

### Phase 1: Enforcement Kernel

Define a deterministic trace model over bounded runner/workspace events:

- action atoms;
- path atoms;
- verification atoms;
- completion atoms;
- budget and loop atoms.

Build a small evaluator for direct predicates and simple temporal patterns. This
can be table-driven Go before any LTL dependency exists.

The first kernel milestone should ingest the extended `ContractGateEvent`
payloads and produce replay decisions without invoking a model. It should also
correlate runner gate decisions with legacy surfaces, including
`completion_gate_decision` events and plan-execute JSON outcomes emitted from
`cmd/idiom/plan_execute.go`.

### Phase 2: Replay And Benchmarks

Create a trajectory benchmark independent of task success:

- known-bad tool sequences;
- valid coding trajectories;
- stale context scenarios;
- verification omission scenarios;
- self-modification attempts;
- source-satisfied/no-op cases.

Score allow/block/observe decisions, false positives, and missing envelopes.
Also score missing `pack_id`, missing `enforcement_mode`, unsafe
`observed_facts`, and missing `safe_alternatives` on rejected or stopped gates.

### Phase 3: Packs As Product Surface

Promote stable invariant groups into named packs with docs, tests, and
observability:

- pack source;
- included invariants;
- default mode;
- override story;
- false-positive notes;
- required observation atoms.

### Phase 4: Sidecar Enforcement

Apply the same packs to `idiom tool` and future MCP calls:

- discovery profile can observe freshness and scope violations;
- write profile can enforce path scope and protected paths;
- external callers can receive structured safe alternatives.

### Phase 5: Optional Formal Layer

If simple temporal patterns become too ad hoc, introduce a formal evaluator over
the same atoms. The stable boundary should be the atom stream and decision
envelope, not the choice of evaluator.

## Open Questions

- Which existing gate events have enough state to emit envelopes without broad
  refactoring?
- Which `.idiom` paths should be protected, observe-only, or explicitly
  operator-owned?
- Should pack definitions live in `internal/autonomy`, `internal/agent`, or a
  new package such as `internal/contractpacks`?
- What is the smallest trajectory fixture format that can replay gate decisions
  without invoking a model?
- How should ranker-learning evidence cite contract-pack outcomes without
  turning false positives into learned penalties?
- How should `cmd/idiom/plan_execute.go` JSON events correlate with autonomy
  `EventGateDecision` records without duplicating authority?

## Review Log

Initial reviewer bench completed on 2026-05-24:

- Reviewers aligned that broader Sponsio lessons belong here, not in the
  immediate stability patch.
- Follow-up scope should include deterministic atom streams, pack lifecycle,
  replay fixtures, sidecar enforcement, anti-tamper taxonomy, and plan-execute
  legacy event parity.
- Follow-up work should continue to avoid prompt reminders as the main
  enforcement mechanism and should preserve the model/harness authority split.

Second reviewer pass completed on 2026-05-24:

- Reviewers aligned that immediate implementation can proceed while this
  document retains broader lessons.
- Remaining follow-up emphasis: correlate `cmd/idiom/plan_execute.go` JSON
  outcomes with typed autonomy gate decisions, mature pack lifecycle, define
  self-modification operator paths before hard enforcement, and keep replay
  fixtures independent of model calls.
