# Contract Capability Architecture Overhaul Implementation Evidence

Status: initial atoms implemented and final-reviewed, created 2026-05-24

This evidence note tracks implementation slices for
[Contract And Capability Architecture Overhaul](../design-contract-capability-architecture-overhaul.md).
It records durable implementation facts only; raw prompts, raw transcripts, and
file bodies are intentionally excluded.

## Scope

Current implementation scope is Phase 1 foundation work only. It makes contract
compilation observable and testable before any runner behavior changes.

Out of scope for this evidence note:

- switching runner gates to consume the new compiler
- changing workspace adapter policy
- changing scripted-contract fixtures owned by another dirty worktree change
- changing unrelated `internal/agent/runner*.go` or scripted-contract edits
  already present in the worktree

## Atom Ledger

| Atom | State | Scope | Implementor evidence | Reviewer evidence | Verification |
| --- | --- | --- | --- | --- | --- |
| `unit-001` | reviewed, repaired | passive plan-execute planner contract compiler in `internal/contractcompile` | implementor reported new pure package with compiler, runtime envelope, protocol projection, normalization, and tests | focused North-Star/Architecture, Harness/Go, Runtime/Config, Prompt/Protocol, and TDD/Acceptance reviews; findings repaired in-package | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...`; `git diff --check` |
| `unit-002` | reviewed | design-doc implementation workflow and docs/Obsidian links | implementor reported docs-only operating rule plus roadmap and Obsidian links | documentation/workflow review found no blocking issues | `git diff --check`; placeholder-marker scan returned no matches in touched docs |
| `unit-003` | reviewed | paired runtime degradation decisions inside `internal/contractcompile` | implemented as repair to Unit 1 runtime review | final full-bench review found no blocking issues | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...` |

## Unit 1 Findings And Repairs

Focused reviewers found and the implementation repaired these compiler issues:

- Required edit paths originally became planner-owned write obligations. They
  now compile as open planner evidence obligations required by
  `submit_plan_required_edit_coverage`.
- Required edit paths and source-body obligations were conflated by defaults.
  They are now distinct: required edit paths are coverage scope, source-body
  obligations are explicit evidence requirements.
- The model protocol projection now includes required edit coverage, path-role
  summary, verification obligations, and safe alternatives in addition to
  visible tools and action classes.
- Callable and invalid action classes are canonicalized and overlap-checked.
- Unknown tools are rejected across allowed, forbidden, required, and
  protocol-visible tool sets.
- Read-only profiles reject mutating tools in allowed, required, and
  protocol-visible tool lists.
- The passive compiler rejects non-`report_only` enforcement mode.
- Planner capability policy no longer grants additive side-effect authority
  just because required or allowed edit paths exist.

Residual risk carried forward:

- The current tool vocabulary is a local compiler mapping. Before runner
  integration, compare it against the real workspace tool catalog or manifest.
- Runtime policy values remain mostly string fields. This is acceptable for a
  passive compiler slice but should tighten before enforcement.

## Unit 2 Workflow Rule

[Design Doc Implementation Workflow](../design-doc-implementation-workflow.md)
was added as a durable operating rule for broad design-doc implementation. It
requires:

- full pre-implementation reviewer bench for broad architecture, harness, lane,
  prompt/protocol, audit, and local-model changes
- durable reviewer evidence
- atomization before implementation
- current context evidence and no-go gates per atom
- safe parallelism only across disjoint write scopes
- independent per-atom implementor and reviewer passes
- roadmap and Obsidian freshness checks
- final full-bench review before commit and push

## Current Verification

Latest local checks:

```text
go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime
go test ./...
git diff --check
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

The focused tests, full Go suite, and diff check passed after the latest
repairs. `harness_audit` completed with `logs=251`, `contract_events=4360`,
`findings=15`, and `questions=11`; the remaining findings are pre-existing
behavior/audit signals that the broader architecture design targets but this
passive slice does not claim to fix.

## Docs Freshness

Updated documentation:

- `docs/design-contract-capability-architecture-overhaul.md`
- `docs/design-doc-implementation-workflow.md`
- `docs/idiom-design-roadmap.md`
- `docs/obsidian/03 - Architecture Layers.md`
- `docs/obsidian/04 - Runtime Lanes.md`
- `docs/obsidian/22 - Ground-Up Implementation Plan.md`

The Obsidian notes now mention the design-doc implementation workflow and the
current report-only `internal/contractcompile` foundation. Generated
`docs/obsidian-export` copies were not edited directly.

## Final Full-Bench Review

Final review was run against the assembled deliverable using all six roles in
`docs/debugging-reviewer-bench.md`.

| Reviewer | Finding |
| --- | --- |
| Incident Root-Cause | No blocking issue. The first broken invariant remains `contract.active_authority`; this slice makes it observable through a passive compiler and does not claim enforcement migration. |
| Model Runtime / Config | No blocking issue. Runtime transport, parser contract, native tool policy, response format, thinking budget, token budget, queue state, enforcement mode, degraded invariants, and paired fallback decisions are modeled separately. |
| Harness / Go | No blocking issue. The new compiler package is pure, has boundary tests, and is not wired into runner behavior in this slice. |
| Prompt / Protocol | No blocking issue. The model protocol projection includes visible tools, callable and invalid action classes, terminal shapes, source-body obligations, required edit coverage, path-role summary, verification obligations, safe alternatives, and a protocol hash. |
| TDD / Acceptance | No blocking issue. Regression tests cover stable ids, actor-boundary separation, required-edit/source-body distinction, unknown tool rejection, action-class validation, report-only provenance, runtime budget clamps, degradation fallback pairing, and import boundaries. |
| North-Star / Architecture | No blocking issue. The change keeps strategy with the model and contracts with the harness; it adds typed evidence without adding scenario-specific prompt pressure or live enforcement. |

Accepted residual risks:

- The compiler is passive and report-only. Later migration atoms must wire
  contract ids, protocol hashes, policy decisions, and obligation updates into
  runner events before audit coverage metrics should be expected to improve.
- The design-doc implementation workflow is prose. A later atom can make it an
  executable template or command if the process becomes frequent enough to need
  executable enforcement.
