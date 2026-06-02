# Contract Capability Architecture Overhaul Implementation Evidence

Status: phase 1 report-only authority pilot and supporting consolidations
implemented locally, created 2026-05-24, updated 2026-06-02

This evidence note tracks implementation slices for
[Contract And Capability Architecture Overhaul](../design-contract-capability-architecture-overhaul.md).
It records durable implementation facts only; raw prompts, raw transcripts, and
file bodies are intentionally excluded.

## Scope

Current implementation scope covers Phase 1 report-only contract authority
work plus supporting consolidation needed to reduce duplicated planner
semantics. It makes contract compilation observable and, for the covered
plan-execute planner path, lets the runner autonomy recorder consume the
compiled contract identity without graduating enforcement beyond report-only.

Out of scope for this evidence note:

- broad runner enforcement outside the plan-execute planner pilot
- lane-start runtime launch blocking
- broad path-normalization or path-role resolver migration
- hiding or removing direct workspace/language tools behind a capability facade
- changing scripted-contract fixtures owned by another dirty worktree change
- changing unrelated scripted-contract edits already present in the worktree

## Atom Ledger

| Atom | State | Scope | Implementor evidence | Reviewer evidence | Verification |
| --- | --- | --- | --- | --- | --- |
| `unit-001` | reviewed, repaired | passive plan-execute planner contract compiler in `internal/contractcompile` | implementor reported new pure package with compiler, runtime envelope, protocol projection, normalization, validation, and tests | focused North-Star/Architecture, Harness/Go, Runtime/Config, Prompt/Protocol, and TDD/Acceptance reviews; findings repaired in-package | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...`; `git diff --check` |
| `unit-002` | reviewed | design-doc implementation workflow and docs/Obsidian links | implementor reported docs-only operating rule plus roadmap and Obsidian links | documentation/workflow review found no blocking issues | `git diff --check`; placeholder-marker scan returned no matches in touched docs |
| `unit-003` | reviewed | paired runtime degradation decisions inside `internal/contractcompile` | implemented as repair to Unit 1 runtime review | final full-bench review found no blocking issues | `go test -count=1 ./internal/contractcompile ./internal/autonomy ./internal/frontier/workerruntime`; `go test ./...` |
| `unit-004` | committed | local planner tool-vocabulary alignment across compiler and runner protocol projection | commit `c3623830` consolidated the plan-execute planner vocabulary used by `internal/contractcompile` and `internal/agent` | prior consolidation review found the compiler vocabulary had to match the real planner surface before runner integration | focused contractcompile/agent/workerruntime/workspace tests; `go test ./...`; `git diff --check` |
| `unit-005` | implemented locally | pure prompt edit-scope parsing extracted into `internal/plancontract` | implementor reported `planexecute` compatibility wrappers with parser behavior preserved | Reviewer B marked parser extraction GO and broad path normalization NO-GO | `go test ./internal/plancontract ./internal/planexecute ./internal/app` |
| `unit-006` | implemented locally | canonical plan-execute planner tool metadata in `internal/autonomy` | implementor reported compiler, workspace manifest filtering, and workerruntime planner profile consuming shared metadata | Reviewer B marked inward metadata GO before runner classification | `go test ./internal/autonomy ./internal/contractcompile ./internal/workspace ./internal/frontier/workerruntime` |
| `unit-007` | implemented locally | runner autonomy planner classification delegates to shared planner metadata | implementor reported planner-only capability/side-effect classification from autonomy metadata with legacy fallback for non-planner paths | Reviewer B marked this GO only after Unit 006 landed | `go test ./internal/agent -run 'Autonomy|PlanExecute|PhaseContract|RuntimeCall'` |
| `unit-008` | implemented locally | non-enforcing runtime readiness schema around `report_only`, `allowed_degraded`, and `launch_blocked` | implementor reported typed readiness projection while preserving report-only lane behavior | Reviewer C marked schema consolidation GO and lane-start enforcement NO-GO | `go test ./internal/readiness ./internal/cmd/lane ./internal/cmd/doctor ./internal/localops ./internal/lanehistory` |
| `unit-009` | implemented locally | plan-execute planner runner authority pilot consumes `contractcompile` output | implementor reported planner recorder construction using `CompilePlanExecutePlanner`, with non-planner paths unchanged | Reviewer A marked compiler-to-runner authority GO for a covered pilot and no broad enforcement | focused contractcompile/agent/workerruntime tests; `go test ./internal/agent ./internal/contractcompile ./internal/frontier/workerruntime`; `git diff --check` |
| `unit-010` | implemented locally | audit coverage for readiness and completion contract events | implementor reported `AuditEvents` now counts `readiness_state_updated` and `completion_gate_decision` and flags completion allow with remaining obligations | Reviewer C marked replay/audit coverage GO after authority events and release-gate enforcement NO-GO | `go test ./internal/autonomy`; `git diff --check -- internal/autonomy/audit.go internal/autonomy/audit_test.go` |

## Atom Records

The compact ledger above is backed by these workflow fields. Each atom used the
current branch state plus the design and reviewer evidence as its source packet;
no atom was dispatched with overlapping write scope.

| Atom | Phase | Tier | Context evidence | Expected files | Forbidden files | Blast radius | TDD obligations | Required tests | Audit signal | Rollback note | Implementor evidence | Reviewer evidence | Investigation path |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `unit-001` | Phase 1 contract compilation | `C1` | `context_pack` evidence for contract compilation and source inspection; design reviewer report | `internal/contractcompile/*.go`, `internal/contractcompile/*_test.go` | `internal/agent/**`, `internal/app/**`, `cmd/**`, `internal/workspace/**`, `testdata/scripted-contracts/**` | passive package-only compiler foundation; no runner wiring | stable ids, actor boundary, required-edit/source-body distinction, action-class validation, planner-forbidden tool validation, import boundaries | focused compiler/autonomy/runtime packages; full Go suite before commit | passive contract compiler should not change live `harness_audit` behavior counts | remove `internal/contractcompile` and docs references; no runner wiring to unwind | subagent Unit 1 report, summarized in this file | focused reviewer reports, summarized in this file and final bench rows | this file |
| `unit-002` | Design-doc implementation workflow | `C0` | design-doc workflow request and docs/Obsidian source notes | `docs/design-doc-implementation-workflow.md`, `docs/idiom-design-roadmap.md`, source Obsidian notes | generated `docs/obsidian-export/**`, source code, unrelated testdata | docs-only operating rule and links | workflow no-go gates, atom fields, parallelization rule, docs freshness rule | `git diff --check`; placeholder-marker scan | no audit signal expected for docs-only atom | remove workflow doc and roadmap/Obsidian links | subagent Unit 2 report, summarized in this file | documentation/workflow reviewer report, summarized in this file | this file |
| `unit-003` | Phase 1 runtime envelope hardening | `C1` | runtime/config reviewer finding on degraded invariant fallback pairing | `internal/contractcompile/types.go`, `internal/contractcompile/compile.go`, `internal/contractcompile/normalize.go`, `internal/contractcompile/compile_test.go` | runner/app/workspace/cmd/testdata/docs outside this evidence note | passive runtime-envelope fields only; no live behavior change | explicit degradation fallback association, legacy positional pairing, no fallback authorization without fallback | focused compiler/autonomy/runtime packages; full Go suite before commit | runtime envelope becomes more replayable; no live behavior count expected to move | remove `DegradationDecision` fields and tests if later superseded by typed autonomy schema | subagent Unit 3 report, summarized in this file | focused runtime/architecture reviewer report, summarized in this file | this file |
| `unit-004` | Phase 1 tool vocabulary alignment | `C1` | runner/compiler planner vocabulary drift risk from Unit 1 residuals | `internal/contractcompile/**`, `internal/agent/**`, planner vocabulary tests | broad workspace/runtime readiness, path parsing, lane launch enforcement | aligns local planner vocabulary before active authority wiring | real manifest vocabulary acceptance and forbidden planner tool rejection | focused compiler/agent/runtime/workspace packages; full suite | reduces tool-surface contradiction risk before runner wiring | revert commit `c3623830` if superseded by a stronger metadata package | prior Codex implementation summary | prior focused review and local verification | commit `c3623830` |
| `unit-005` | Prompt scope parser extraction | `C1` | Reviewer B GO packet; duplicated required/allowed edit-scope parsing in `internal/planexecute/prompt.go` | `internal/plancontract/prompt_scope.go`, parser tests, `internal/planexecute/prompt.go` wrappers | runner gates, contractcompile, readiness, workspace manifests | pure parser move with compatibility wrappers; no behavior expansion | required/allowed separation, file-only required filtering, inline/structured/heading parity | `go test ./internal/plancontract ./internal/planexecute ./internal/app` | no live audit signal expected; reduces parser authority duplication | restore parser helpers to `planexecute` if wrapper parity breaks | Implementor A report | Reviewer B GO, broad path normalization NO-GO | this file |
| `unit-006` | Planner metadata authority | `C1` | Reviewer B GO packet; compiler/workspace/workerruntime planner lists duplicated | `internal/autonomy/planner_tool_metadata.go`, contractcompile/workspace/workerruntime consumers and tests | runner classification, readiness, prompt parsing | moves planner tool metadata inward without importing workspace into compiler | shared allowed/rejected planner tools, side effects, defensive-copy metadata | `go test ./internal/autonomy ./internal/contractcompile ./internal/workspace ./internal/frontier/workerruntime` | capability/tool coverage should cite one metadata source | restore local lists only if inward metadata creates import cycle; no cycle observed | Implementor B report | Reviewer B GO; runner classification held until after this unit | this file |
| `unit-007` | Runner planner classification delegation | `C1` | Reviewer B conditional GO after Unit 006 | `internal/agent/runner_autonomy.go`, runner autonomy tests | non-planner fallback behavior, workspace manifests, readiness | planner-only capability and side-effect classification reads shared metadata; non-planner fallback remains | every shared planner metadata entry maps in runner autonomy; representative policy events | `go test ./internal/agent -run 'Autonomy|PlanExecute|PhaseContract|RuntimeCall'` | policy events become more comparable with compiler metadata | remove planner metadata lookup and keep fallback switch if planner classification regresses | Implementor C report | Reviewer B GO after Unit 006 | this file |
| `unit-008` | Runtime readiness schema | `C1` | Reviewer C GO packet; readiness states existed as telemetry but not typed runtime decision schema | `internal/readiness/readiness.go`, readiness tests | lane-start enforcement, contractcompile enforcement mode, runner dispatch | typed report-only readiness projection with `allowed_degraded`/`launch_blocked` vocabulary; no blocking behavior | missing backend role, invalid context budget, queue unavailable/degraded, report-only preservation | readiness, lane, doctor, localops, and lanehistory focused tests | future runtime readiness audit has stable fields; no launch gate expected to move | remove `RuntimeReadinessReport` projection if later replaced by autonomy runtime schema | Implementor D report | Reviewer C GO for schema and NO-GO for launch blocking | this file |
| `unit-009` | Planner authority pilot | `C2` | Reviewer A GO packet; runner recorder built a parallel contract while protocol projection used compiler | `internal/agent/model_protocol_projection.go`, `internal/agent/runner_autonomy.go`, focused agent tests | non-planner runner paths, lane-start enforcement, phase/source-body gating ownership | covered plan-execute planner recorder uses `CompilePlanExecutePlanner`; gates remain report-only | contract compiled/presented/model protocol/completion gate share compiled contract id and protocol hash | focused compiler/agent/runtime tests; `go test ./internal/agent ./internal/contractcompile ./internal/frontier/workerruntime`; `go test ./...`; `git diff --check` | covered planner events can be audited by compiled contract id | fall back to legacy runner contract construction for planner if compiler input builder regresses | Implementor E report | Reviewer A GO with report-only/no broad rollout constraints | this file |
| `unit-010` | Readiness/completion audit coverage | `C1` | Reviewer C GO packet; `AuditEvents` did not first-class count readiness/completion events | `internal/autonomy/audit.go`, audit tests | harness audit summary strings, release gates, runner event generation | audit consumes existing structured readiness/completion events; no new event stream | coverage counts and completion allow with remaining obligations finding | `go test ./internal/autonomy`; diff check on touched audit files | audit reports readiness/completion coverage by contract | remove new coverage fields if later replaced by replay report fusion | Implementor F report | Reviewer C GO and release-gate NO-GO | this file |

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

Residual risk status after the 2026-06-02 batch:

- Resolved for the covered planner path: planner tool vocabulary now comes from
  `internal/autonomy` metadata and is consumed by compiler, workspace manifest,
  workerruntime planner profile, and runner autonomy classification.
- Still open: runtime readiness has typed report-only decisions, but launch
  blocking and broader enforcement remain intentionally disabled.
- Still open: the covered planner recorder consumes the compiled contract
  identity, but non-planner runner paths still use legacy runner contract
  construction.
- Still open: path-role authority is only partially consolidated through prompt
  parser extraction. Broad path normalization and resolver migration remain
  future work.

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
go test ./internal/plancontract ./internal/planexecute ./internal/app
go test ./internal/autonomy ./internal/contractcompile ./internal/workspace ./internal/frontier/workerruntime
go test ./internal/agent -run 'Autonomy|PlanExecute|PhaseContract|RuntimeCall'
go test ./internal/readiness ./internal/cmd/lane ./internal/cmd/doctor ./internal/localops ./internal/lanehistory
go test ./internal/agent ./internal/contractcompile ./internal/frontier/workerruntime
go test ./...
git diff --check
idiom tool harness_audit --input '{"purpose":"behavior"}'
```

Focused package tests, full Go suite, diff check, and harness audit passed for
this batch. The latest harness audit completed with `logs=252`,
`contract_events=5146`, `gate_decisions=455`, `findings=9`,
`historical_findings=3`, and `questions=9`. It still reports existing
behavior/audit signals, including `turn_phase_contract_contradiction=48`;
those remaining signals are outside this report-only planner authority pilot
and are targets for later migration phases.

## Docs Freshness

Updated documentation:

- `docs/design-contract-capability-architecture-overhaul.md`
- `docs/evidence/contract-capability-architecture-overhaul-implementation.md`

The design-doc implementation workflow and Obsidian freshness links were part
of the earlier documentation atom. This batch updates only the architecture
design and implementation evidence notes.

## Initial Full-Bench Review

The initial passive compiler deliverable was reviewed using all six roles in
`docs/debugging-reviewer-bench.md`.

| Reviewer | Finding |
| --- | --- |
| Incident Root-Cause | No blocking issue. The first broken invariant remains `contract.active_authority`; the initial slice made it observable through a passive compiler and did not claim enforcement migration. |
| Model Runtime / Config | No blocking issue. Runtime transport, parser contract, native tool policy, response format, thinking budget, token budget, queue state, enforcement mode, degraded invariants, and paired fallback decisions are modeled separately. |
| Harness / Go | No blocking issue. The new compiler package is pure, has boundary tests, and was not wired into runner behavior in the initial slice. |
| Prompt / Protocol | Final review found transport projection and free-form safe-alternative risks; both were repaired by adding explicit transport/runtime protocol fields and rejecting `required_next` in this passive compiler. |
| TDD / Acceptance | Final review found planner-forbidden tool and atom-record gaps; both were repaired by planner tool validation and the atom records above. Regression tests cover stable ids, actor-boundary separation, required-edit/source-body distinction, unknown tool rejection, planner-forbidden tool rejection, action-class validation, report-only provenance, runtime budget clamps, degradation fallback pairing, and import boundaries. |
| North-Star / Architecture | No blocking issue. The change keeps strategy with the model and contracts with the harness; it adds typed evidence without adding scenario-specific prompt pressure or live enforcement. |

## 2026-06-02 Reviewer Alignment

The current batch used focused reviewer alignment before implementation:

- Root-cause/North-Star review marked the plan-execute planner compiler-to-runner
  authority pilot, path parser consolidation, planner capability metadata, and
  replay/audit coverage as GO. It marked standalone runner splitting and
  lane-start runtime blocking as NO-GO.
- Harness/Go review marked prompt parser extraction, inward planner metadata,
  and runner classification delegation as GO. It marked broad path
  normalization, phase/source-body gate migration, and compiler imports of
  workspace/workerruntime/app/planexecute as NO-GO.
- Runtime/Audit review marked typed readiness schema and readiness/completion
  audit coverage as GO. It marked launch enforcement and contract replay as a
  sole release gate as NO-GO.

Current accepted residual risks:

- Enforcement remains report-only. Later migration atoms must prove the covered
  path through replay/audit before broad runner enforcement or lane-start
  blocking graduates.
- The planner recorder startup compile is phase-light; per-turn protocol
  projection supplies exact phase/runtime details while citing the recorder's
  compiled contract identity.
- The `harness_audit` compact summary does not yet surface the new
  readiness/completion coverage fields directly.
- The design-doc implementation workflow is prose. A later atom can make it an
  executable template or command if the process becomes frequent enough to need
  executable enforcement.
