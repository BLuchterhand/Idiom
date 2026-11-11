---
name: orchestrator-workflow
description: |
  Plan and execute implementation work through bounded Idiom lanes. Use for
  greenfield or rearchitecture planning, implementation-plan reviews, atom
  decomposition, local Qwen or Codex worker dispatch, and final diff review.
  Require broad plans to define architecture, process topology, technology
  choices, testing systems, CI/release operations, and evidence-gated decisions
  before implementation atoms are dispatched.
---

# Orchestrator Workflow — Planner Plans, Bounded Workers Execute

This skill describes the canonical pattern for running an
implementation loop where a strong planner (Claude Opus, GPT, etc.)
authors the spec and a bounded executor (local Qwen via idiom, or a
cloud overflow) does the mechanical edits.

## Role Split

| Role | Typical model | Via | Responsibility |
|------|---------------|-----|----------------|
| **Planner** | Strong reasoner (Opus, GPT-5) | The orchestrator (Claude Code, Codex, etc.) | Decompose, write atom specs, review diffs, commit |
| **Researcher** | Same as planner, or smaller explorer | Local tools or a delegated subagent | Answer specific codebase questions before spec'ing |
| **Implementer** | Local Qwen first; cloud overflow second | `idiom lane plan-execute` | Plan + apply edits inside one bounded lane |
| **Reviewer** | Same as planner, or independent reviewer | Inline or delegated | Full-scope review of the produced diff |

The planner stays in one trajectory across the whole atom; the
executor is **disposable** — every dispatch is a fresh session, no
shared state across atoms. This keeps the planner's context clean
and lets the executor be replaced or scaled without redesigning the
loop.

## Pipeline (per atom)

```
1. ANALYZE   — Planner identifies the impact / blast radius
2. RESEARCH  — Planner (or delegated explorer) answers specific code questions
3. VERIFY    — Planner spot-checks key findings (1–2 targeted reads)
4. SPEC      — Planner writes the atom brief WITH test cases (tests are the spec)
5. IMPLEMENT — `idiom lane plan-execute --title <atom> <brief>`
6. ACCEPT    — Planner builds, runs tests, spot-checks, commits
7. REVIEW    — Per project policy: inline / delegated / skip based on complexity
```

Steps 1–4 and 6–7 stay with the planner. Step 5 is the executor's
sole responsibility — when a dispatch fails, the planner first
checks whether the brief was tight enough (anchor discipline), then
whether the harness has a real gap that should be fixed.

## Implementation-readiness gate

For a greenfield system, broad rearchitecture, or implementation-plan review,
complete this gate before decomposing work into atoms. Skip it for a narrow
change whose host architecture and delivery system are already established.

### Architecture and topology

State the chosen architecture rather than leaving it implied. Name the style
only when the design actually follows it—for example, a modular monolith with
hexagonal boundaries, not a list of fashionable patterns. Record:

- process, deployment, storage, worker, and external-system topology;
- module ownership, dependency direction, composition root, and public seams;
- data ownership, transaction boundaries, consistency, ordering, and recovery;
- trust, identity, secret, artifact, and external-effect boundaries;
- expected scale and the conditions that would justify extracting a service,
  adding remote workers, or changing the storage model.

Use a small context/container/component or deployment diagram when it clarifies
these connections. Do not choose microservices by default. Require a concrete
independent deployment, scaling, isolation, or ownership need before accepting
the operational cost of a network boundary.

### Technology baseline

For each material technology, record the concern, selected default, status,
alternatives considered, selection evidence, and decision deadline. Status is
one of `fixed`, `evidence_gated`, or `deferred`.

Cover at least:

- language/runtime and supported versions;
- metadata database, migrations, transactions, backup, and artifact storage;
- serialization, canonicalization, schemas, IDs, time, and compatibility;
- CLI, API transport, UI, component protocol, and SDK boundaries;
- configuration, credential storage, secret delivery, and identity provider;
- concurrency, queues, subprocess control, isolation, and network policy;
- logging, metrics, tracing, diagnostics, and support exports;
- build, packaging, installation, upgrade, signing, and release distribution.

Prefer the language standard library until a dependency removes demonstrated
complexity. A framework is an adapter choice, not the architecture. Pin tools
and dependencies that affect builds, persisted meaning, protocols, or evidence.

### Verification system

Name how each test layer runs; “add tests” is not an implementation plan. For
each layer, identify the runner or framework, fixture strategy, exact local
command, CI cadence, supported platforms, and retained failure evidence.

Cover the applicable layers:

- domain/unit and application-use-case tests;
- persistence, component, adapter, schema, and protocol contract tests;
- real-dependency integration tests and black-box CLI/API/browser end-to-end
  tests;
- fuzz or property tests for parsers, protocols, canonicalization, mappings,
  and state transitions;
- race/concurrency, crash/restart, failure-injection, security, migration, and
  compatibility tests;
- benchmarks, load tests, soak tests, and platform qualification.

Define the fake and mock policy. Prefer hand-written fakes at owned ports and
real infrastructure at integration boundaries. Avoid global coverage targets
that reward shallow assertions; require transition, rejection-path, and
invariant coverage at the owning boundary.

### Delivery and operations

Specify the CI platform and the PR, main/nightly, and release gates. Define how
the project produces reproducible artifacts, checks dependencies and licenses,
creates an SBOM, signs releases, tests installation and upgrades, verifies
backup/restore, and preserves diagnostics. Name configuration precedence,
operational health checks, rollback limits, and the first operator runbooks.

### Decision closure and connection review

Classify every unresolved choice as evidence-gated or deferred. Give an
evidence-gated choice an owner, alternatives, spike or test, deadline, and the
behavior when no option qualifies. Give deferred work an explicit product
boundary so it cannot become an accidental first-release dependency. Close any
choice that can change public syntax, persisted identity, transaction semantics,
or trust boundaries before implementation crosses that boundary.

Review every system connection using the same questions: who owns the contract,
what exact data or artifact crosses it, what authority travels with it, how it
fails or times out, how retry/recovery works, and which executable test proves
the behavior. A plan passes this gate only when an engineer can choose the next
atom without inventing architecture, tooling, or verification policy.

## Brief discipline

A good atom brief is the difference between a lane that converges
in one shot and a lane that exhausts its budget. The canonical
`brief-skeleton.md` rule (embedded in idiom) enumerates the
required sections; the most-overlooked are:

- **Verified Current State** — exact source snippets the executor
  uses as `old_text` anchors. Paraphrase ≠ anchor; "the function
  that handles X" is not an anchor; the literal source line is.
- **Files To Modify** — single-purpose decomposition; ≤8–10 edits
  per atom. The runner extracts this as `required_edit_paths`.
- **Implementation Shape** — for plan-execute, name the edit shape
  (substring / line-range / new-file) so the planner doesn't have
  to infer it.
- **Constraints, Tests, and Decisions** — cite the applicable architecture and
  technology choices, use their exact verification commands, and preserve any
  evidence-gated or deferred boundary. An atom must not silently decide a
  system-wide technology or cross a boundary the plan left closed.

`idiom brief lint <brief>` runs the canonical preflight on a brief
before dispatch; pre-merge CI hooks should run it too.

## When the dispatch fails

Three failure classes the lane wrapper surfaces (via the
`idiom.lane.degraded` JSON event):

- `read_only_lane` — the brief was routed to a read-only contract.
  Almost always an idiom-routing regression; report via
  `harness_feedback`, do NOT rewrite the brief.
- `no_edit_attempt` — the lane completed cleanly but never tried
  to edit. Either the brief lacked an actionable Required Edits
  section or the executor model couldn't anchor the edits.
- Non-zero subcommand exit — a real failure (build broken,
  process crash). The wrapper exit propagates.

The orchestrator's loop should subscribe to these signals and
react: typically retry with a tightened brief once, then escalate
to human review or fall back to a stronger executor.

## When the executor isn't suited

Local Qwen-coder-30B handles surgical edits to existing files
well. It struggles to author large new files from spec. The
emerging pattern:

- **Existing-file surgical edits** → dispatch to local Qwen.
- **New file authoring (>~50 lines)** → planner writes the file
  directly; executor only handles the dispatch-case wiring.
- **Multi-file refactors** → decompose into per-file atoms; each
  atom stays single-purpose.

This isn't a hard rule — it's the bias to start with. As local
models improve, more of the second category will move into Qwen's
column.

## Customize this skill

The canonical version (this file) is intentionally generic. A
project should overlay project-specific guidance — preferred test
runners, build commands, atom-decomposition heuristics for the
project's domain, escalation paths for failed dispatches — by
creating `<repo>/.claude/skills/orchestrator-workflow/local.md`
(or appending to this file). Idiom doesn't read these; they're
for the orchestrator's own reference.
