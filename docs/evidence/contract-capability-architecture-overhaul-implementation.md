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
| `unit-001` | reviewed, repaired | passive plan-execute planner contract compiler in `internal/contractcompile` | implementor reported new pure package with compiler, runtime envelope, protocol projection, normalization, validation, and tests | focused North-Star/Architecture, Harness/Go, Runtime/Config, Prompt/Protocol, and TDD/Acceptance reviews; findings repaired in-package | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...`; `git diff --check` |
| `unit-002` | reviewed | design-doc implementation workflow and docs/Obsidian links | implementor reported docs-only operating rule plus roadmap and Obsidian links | documentation/workflow review found no blocking issues | `git diff --check`; placeholder-marker scan returned no matches in touched docs |
| `unit-003` | reviewed | paired runtime degradation decisions inside `internal/contractcompile` | implemented as repair to Unit 1 runtime review | final full-bench review found no blocking issues | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...` |

## Atom Records

The compact ledger above is backed by these workflow fields. Each atom used the
current branch state plus the design and reviewer evidence as its source packet;
no atom was dispatched with overlapping write scope.

| Atom | Phase | Tier | Context evidence | Expected files | Forbidden files | Blast radius | TDD obligations | Required tests | Audit signal | Rollback note | Implementor evidence | Reviewer evidence | Investigation path |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `unit-001` | Phase 1 contract compilation | `C1` | `context_pack` evidence for contract compilation and source inspection; design reviewer report | `internal/contractcompile/*.go`, `internal/contractcompile/*_test.go` | `internal/agent/**`, `internal/app/**`, `cmd/**`, `internal/workspace/**`, `testdata/scripted-contracts/**` | passive package-only compiler foundation; no runner wiring | stable ids, actor boundary, required-edit/source-body distinction, action-class validation, planner-forbidden tool validation, import boundaries | focused compiler/autonomy/runtime packages; full Go suite before commit | passive contract compiler should not change live `harness_audit` behavior counts | remove `internal/contractcompile` and docs references; no runner wiring to unwind | subagent Unit 1 report, summarized in this file | focused reviewer reports, summarized in this file and final bench rows | this file |
| `unit-002` | Design-doc implementation workflow | `C0` | design-doc workflow request and docs/Obsidian source notes | `docs/design-doc-implementation-workflow.md`, `docs/idiom-design-roadmap.md`, source Obsidian notes | generated `docs/obsidian-export/**`, source code, unrelated testdata | docs-only operating rule and links | workflow no-go gates, atom fields, parallelization rule, docs freshness rule | `git diff --check`; placeholder-marker scan | no audit signal expected for docs-only atom | remove workflow doc and roadmap/Obsidian links | subagent Unit 2 report, summarized in this file | documentation/workflow reviewer report, summarized in this file | this file |
| `unit-003` | Phase 1 runtime envelope hardening | `C1` | runtime/config reviewer finding on degraded invariant fallback pairing | `internal/contractcompile/types.go`, `internal/contractcompile/compile.go`, `internal/contractcompile/normalize.go`, `internal/contractcompile/compile_test.go` | runner/app/workspace/cmd/testdata/docs outside this evidence note | passive runtime-envelope fields only; no live behavior change | explicit degradation fallback association, legacy positional pairing, no fallback authorization without fallback | focused compiler/autonomy/runtime packages; full Go suite before commit | runtime envelope becomes more replayable; no live behavior count expected to move | remove `DegradationDecision` fields and tests if later superseded by typed autonomy schema | subagent Unit 3 report, summarized in this file | focused runtime/architecture reviewer report, summarized in this file | this file |

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
- The planner compiler rejects planner-forbidden shell, verification,
  delegation, and mutating tools even when the surrounding workspace permission
  profile allows writes.
- Allowed, required, visible, and forbidden tool sets cannot contradict each
  other, including through different tool names that map to the same capability.
- Protocol hashes now cover transport, parser contract, native-tool policy,
  tool choice, response format, required tools, readiness gates, constraints,
  and invalid actions.
- Free-form `required_next` directives are rejected instead of being absorbed
  into hashed safe alternatives.

Residual risk carried forward:

- The current tool vocabulary is a local compiler mapping. Before runner
  integration, compare it against the real workspace tool catalog or manifest.
- Runtime policy values remain mostly string fields. This is acceptable for a
  passive compiler slice but should tighten before enforcement.
- The compiler uses a local list of planner-forbidden tools. Before runner
  integration, compare that list against the workspace manifest and phase
  contract tool surface.

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
`findings=15`, and `questions=11`; those remaining behavior/audit signals are
outside this passive compiler slice and are targets for later migration phases.

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
| Prompt / Protocol | Final review found transport projection and free-form safe-alternative risks; both were repaired by adding explicit transport/runtime protocol fields and rejecting `required_next` in this passive compiler. |
| TDD / Acceptance | Final review found planner-forbidden tool and atom-record gaps; both were repaired by planner tool validation and the atom records above. Regression tests cover stable ids, actor-boundary separation, required-edit/source-body distinction, unknown tool rejection, planner-forbidden tool rejection, action-class validation, report-only provenance, runtime budget clamps, degradation fallback pairing, and import boundaries. |
| North-Star / Architecture | No blocking issue. The change keeps strategy with the model and contracts with the harness; it adds typed evidence without adding scenario-specific prompt pressure or live enforcement. |

Accepted residual risks:

- The compiler is passive and report-only. Later migration atoms must wire
  contract ids, protocol hashes, policy decisions, and obligation updates into
  runner events before audit coverage metrics should be expected to improve.
- The design-doc implementation workflow is prose. A later atom can make it an
  executable template or command if the process becomes frequent enough to need
  executable enforcement.
