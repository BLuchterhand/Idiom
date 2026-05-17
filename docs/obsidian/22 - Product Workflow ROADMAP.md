---
status: roadmap
tags:
  - idiom
  - roadmap
  - todo
  - product-workflow
  - tdd-coverage
---

# ROADMAP: Product Workflow and TDD Coverage

> **Status: ROADMAP** — Tracks what is done and what remains for the upstream
> product pipeline and behavioral TDD coverage. Last audited 2026-05-15 against
> the codebase.
>
> Design docs:
> [design-prd-adr-brief-research-workflow.md](../design-prd-adr-brief-research-workflow.md),
> [design-tdd-coverage.md](../design-tdd-coverage.md)

## DONE: Seven Pillars

All seven architectural pillars have substantial, production-quality
implementations.

| Pillar | Design Doc | Status | Key Evidence |
| --- | --- | --- | --- |
| Contract-first autonomy | [design-contract-first-local-autonomy.md](../design-contract-first-local-autonomy.md) | **DONE** | `internal/autonomy/contract.go` — full `AutonomyContract` struct with obligations, path roles, gates; `audit.go` with obligation/gate/policy coverage |
| Persistent repository memory | [design-persistent-repository-memory.md](../design-persistent-repository-memory.md) | **DONE** | `internal/comprehension/` — 20+ files: graph SQLite, content-addressed caching, ranker learning, freshness, incremental rebuild |
| Capability-oriented tooling | [design-capability-oriented-tooling.md](../design-capability-oriented-tooling.md) | **DONE** | `internal/workspace/capabilities.go` — `ReadOnlyCapabilityRegistry`, language-aware adapter routing |
| Auditable planning/handoff | [design-auditable-planning-handoff.md](../design-auditable-planning-handoff.md) | **DONE** | `internal/architect/` — packet schemas, plan audit; `cmd/idiom/plan_execute.go`; `submit_plan` tool with artifact schema |
| Local model operations | [design-local-model-operations.md](../design-local-model-operations.md) | **DONE** | `internal/llama/` — client, server controller; `cmd/idiom/queue.go` — Unix socket queue daemon; multi-model support |
| Eval/audit/learning flywheel | [design-eval-audit-learning-flywheel.md](../design-eval-audit-learning-flywheel.md) | **DONE** | `internal/eval/` — 17 files: SWE-bench, localization benchmark, groundedness, ranker learning; `product_evidence.go`, `release_evidence.go` |
| Sidecar/substrate | [design-agent-substrate-sidecar.md](../design-agent-substrate-sidecar.md) | **DONE** | `internal/mcp/` — client, registry, HTTP transport, SSRF protection; `tool_sidecar_envelope.go`; autonomy sidecar integration |

## DONE: Existing Infrastructure Reused By Pipeline

These are complete and the product workflow composes them without modification.

| Capability | Status | Package |
| --- | --- | --- |
| `context_pack` / `context_query` / `context_zoom` | **DONE** | `internal/comprehension/` |
| `context_impact` / `context_trace_pack` | **DONE** | `internal/comprehension/` |
| `harness_audit` tool | **DONE** | `internal/workspace/harness_audit.go` |
| Architect packet generation | **DONE** | `internal/architect/packet.go` |
| Plan audit | **DONE** | `internal/architect/plan_audit.go` |
| Brief lint (preflight) | **DONE** | `cmd/idiom/brief-preflight.py`, `cmd/idiom/brief.go` |
| Atom intake | **DONE** | `cmd/idiom/atom.go` |
| Lane dispatch (`implement`, `plan-execute`) | **DONE** | `cmd/idiom/` |
| Product evidence export | **DONE** | `cmd/idiom/product_evidence.go` |
| Release evidence aggregation | **DONE** | `cmd/idiom/release_evidence.go` |
| Localization benchmark | **DONE** | `internal/eval/localization_benchmark.go` |
| SWE-bench eval infrastructure | **DONE** | `internal/eval/swe_suite.go` |

## NOT STARTED: PRD/ADR/Brief Workflow

The design doc exists at
[design-prd-adr-brief-research-workflow.md](../design-prd-adr-brief-research-workflow.md).
See [[20 - PRD ADR Brief Research Workflow PROPOSAL]] for the Obsidian summary.
Zero implementation code exists.

### TODO: Schemas and Types

- [ ] Evidence card schema struct and validator
- [ ] Evidence ledger (merge, dedup, conflict tracking)
- [ ] Research profile schema struct
- [ ] PRD schema struct with requirement shape
- [ ] ADR schema struct with Nygard-format fields
- [ ] TDD obligation ledger schema v1 (existing types)
- [ ] TDD obligation ledger schema v2 (with coverage metadata)
- [ ] Traceability matrix linking evidence → PRD → ADR → TDD → brief
- [ ] Artifact state machine enum (draft/audited/accepted/blocked/dispatched)

### TODO: Deterministic Audit Commands

- [ ] PRD audit: schema validation, evidence reference existence, metric
  presence, inference labeling, ADR contradiction detection, privacy scanning
- [ ] ADR audit: completeness checks, link validation, supersession chains,
  alternative presence, consequence fields
- [ ] TDD obligation audit: high-priority requirement coverage, ADR validation
  coverage, missing test detection, tautological assertion detection
- [ ] Coverage gate: 9-criterion threshold check (see below)
- [ ] Brief generation: deterministic template filling from PRD/ADR/TDD
  artifacts, section ordering, ID injection

### TODO: Research Infrastructure

- [ ] Researcher role configuration from profiles
- [ ] `tdd_eval` researcher role with FMEA/HAZOP evidence sources
- [ ] Deterministic evidence merge (dedup, freshness, conflict detection)
- [ ] Research profile storage under `.idiom/research/profiles/`

### TODO: CLI Commands

- [ ] `idiom product research` — run configured researchers
- [ ] `idiom product prd` — draft PRD from evidence ledger
- [ ] `idiom product adr` — draft ADRs from PRD
- [ ] `idiom product tdd` — build TDD obligations from PRD + ADRs
- [ ] `idiom product brief` — generate briefs from PRD + ADRs + TDD
- [ ] `idiom product audit` — audit entire product workflow directory

### TODO: Telemetry Events

- [ ] `research_packet_created`, `researcher_started`, `researcher_completed`
- [ ] `evidence_card_recorded`, `evidence_conflict_recorded`
- [ ] `prd_drafted`, `prd_audited`
- [ ] `adr_drafted`, `adr_accepted`
- [ ] `tdd_obligations_created`
- [ ] `coverage_gate_evaluated`
- [ ] `product_artifact_dispatch_ready`, `product_artifact_blocked`
- [ ] `execution_result_linked`

## NOT STARTED: Behavioral TDD Coverage Pipeline

The design doc exists at [design-tdd-coverage.md](../design-tdd-coverage.md).
See [[21 - Behavioral TDD Coverage PROPOSAL]] for the Obsidian summary.
Zero implementation code exists.

### TODO: Stage 1-4 (Requirements to Contracts)

- [ ] EARS template parser and validator
- [ ] EARS-to-BDD mapping (5 pattern templates)
- [ ] Example mapping schema (story → rules → examples → questions)
- [ ] BDD/Gherkin scenario validator
- [ ] Design by Contract schema (precondition/postcondition/invariant per port)
- [ ] BDD Then → postcondition binding validation
- [ ] BDD Given → precondition binding validation

### TODO: Stage 5 (Failure Mode Analysis — ~80% Deterministic)

- [ ] FMEA template: 5 failure classes, Severity/Probability/Detection scoring
- [ ] FMEA RPN calculator and threshold filter (RPN >= 21 → mandatory test)
- [ ] FMEA negative test obligation generator
- [ ] HAZOP guide-word checklist (11 guide words)
- [ ] HAZOP guide-word applicator per workflow step
- [ ] HAZOP severity filter (severity >= 3 → mandatory test)
- [ ] HAZOP deviation test obligation generator
- [ ] FTA tree schema (AND/OR gates, basic events)
- [ ] FTA leaf-to-obligation generator

### TODO: Stage 6-7 (State Machine and Test Suite)

- [ ] State machine model schema (states, transitions, guards, invariants)
- [ ] Reachability and deadlock checks
- [ ] Decision table enumerators (2^n boolean combinations)
- [ ] Boundary value generators (numeric bounds extraction and test values)
- [ ] PBT obligation schema (invariant, state machine, min cases, shrink)
- [ ] Decision table obligation schema
- [ ] Approval test fixture schema (transcript capture, structural comparison)

### TODO: Stage 8-9 (Mutation and Coverage Gate — 100% Deterministic)

- [ ] Mutation operator registry for contract fields
- [ ] Mutation test executor and score calculator
- [ ] Mutation score threshold gate (>= 80%)
- [ ] Coverage gate: 9-criterion deterministic checker
- [ ] Coverage gate event emission (`coverage_gate_evaluated`)
- [ ] Coverage gate block routing (which upstream stage to return to)

### TODO: Feedback Loop

- [ ] Post-mortem obligation template (incident → root cause → catching test)
- [ ] Incident-to-FMEA feedback (new failure mode from execution)
- [ ] Incident-to-HAZOP feedback (new deviation from execution)
- [ ] Coverage gate metric updates from execution outcomes
- [ ] Spec-compliance evaluation (independent of functional correctness)

## TODO: Implementation Phases

All implementation phases must be executed as long-lived implementation atoms.
Each atom requires a complexity-tier assessment, OpenCode GLM 5.1 as the
default implementation runtime, explicit fallback justification when another
implementor is used, one dedicated implementor sub-agent, one distinct dedicated
Codex/GPT reviewer sub-agent, required verification, and a post-effort
investigation report before closeout. Persist atom state in the effort folder so
work can resume across sessions without relying on raw transcript replay.

### TODO: Long-Lived Implementation Operating Model

- [ ] Implementation atom schema with `task_id`, phase, state, complexity tier,
  blast radius, context pack ID, expected files, forbidden files, TDD
  obligations, required tests, and blockers
- [ ] Implementor assignment fields: agent ID, runtime, model, OpenCode command,
  smoke-test status, and selection reason
- [ ] Reviewer assignment fields: distinct agent ID, GPT model, reasoning
  effort, selection reason, and reviewer independence check
- [ ] Complexity tiers `C0` through `C4` with economic model/effort defaults
  and upgrade/downgrade justification
- [ ] Repository OpenCode default points at `zai-coding-plan/glm-5.1`, with
  implementation dispatch invoking `opencode run --model
  zai-coding-plan/glm-5.1` or `scripts/run-opencode-glm.sh`
- [ ] Headless implementation smoke test uses the attached-server path:
  `scripts/run-opencode-glm.sh serve` plus `OPENCODE_SERVER_URL=...`
  `scripts/run-opencode-glm.sh run`
- [ ] Closeout blocker when the OpenCode GLM smoke test fails and no explicit
  fallback implementor exception is recorded
- [ ] Append-only `implementation_ledger.jsonl` in each effort folder
- [ ] Per-atom implementation, review, and post-effort investigation reports
- [ ] Resume validation that loads workflow state, implementation ledger, latest
  reports, event log, context freshness, and git status before choosing the next
  action
- [ ] Closeout blocker when model/effort selection is missing, implementor and
  reviewer are the same actor, required verification is missing, or the
  post-effort investigation is incomplete

### Phase 0: Design Fixtures (Current)

- [x] PRD/ADR/brief workflow design doc
- [x] Behavioral TDD coverage design doc
- [x] Obsidian PROPOSAL and ROADMAP notes
- [ ] Example PRD, ADR, TDD ledger, and brief fixture under `testdata`
- [ ] Manual walkthrough using current `context_pack`, `architect packet`,
  `artifact`, `brief lint`, `atom intake`
- [ ] Seed the implementation ledger with a `C0` documentation/fixture atom and
  persist implementor/reviewer assignments, reports, and post-effort
  investigation evidence

### Phase 1: Schemas and Deterministic Linters

- [ ] Evidence card, PRD, ADR, TDD obligation v1 structs
- [ ] Implementation atom, complexity tier, assignment, and investigation report
  structs
- [ ] PRD audit, ADR audit, TDD audit deterministic commands
- [ ] Artifact storage under `.idiom/product/`
- [ ] Tests for schema validation and gate logic
- [ ] Tests for assignment independence, missing model/effort selection,
  incomplete investigation report, and cross-session resume blockers

### Phase 2: Research Profiles

- [ ] Researcher role configuration from profiles
- [ ] `tdd_eval` role with FMEA/HAZOP templates
- [ ] Deterministic evidence merge
- [ ] Profile storage under `.idiom/research/profiles/`

### Phase 3: PRD and ADR Generation

- [ ] PRD prompt renderer from evidence ledger
- [ ] ADR prompt renderer from PRD
- [ ] Acceptance state and supersession links
- [ ] Human acceptance flow

### Phase 4: TDD Planner and Coverage Gate

- [ ] EARS template parser and validator
- [ ] FMEA template and RPN scorer
- [ ] HAZOP guide-word applicator
- [ ] Decision table and boundary value enumerators
- [ ] PBT obligation schema
- [ ] Coverage gate (9 criteria, 100% deterministic)
- [ ] Brief generator with coverage gate dependency

### Phase 5: Mutation Testing and Verification

- [ ] Mutation operator registry
- [ ] Mutation score calculator
- [ ] Mutation score gate (>= 80%)
- [ ] Spec-compliance evaluation

### Phase 6: Execution Feedback Loop

- [ ] Lane event → PRD/ADR/TDD obligation linking
- [ ] Post-mortem obligation auto-generation
- [ ] Incident → FMEA/HAZOP feedback
- [ ] Coverage metric updates from outcomes

### Phase 7: Sidecar and External Agent Use

- [ ] Read-only product workflow commands via `idiom tool` and MCP
- [ ] External agent consumption of PRD/ADR/brief artifacts
- [ ] Write authorization remains in existing lane contracts

## Deterministic vs LLM Architecture

~75% of the pipeline can be deterministic tooling. The LLM is needed at
roughly 5 narrow synthesis points. Every LLM output immediately re-enters
deterministic validation. The LLM never skips a gate.

```mermaid
flowchart LR
    subgraph Deterministic
        D1[Intake classify]
        D2[Packet build]
        D3[Evidence gather]
        D4[Evidence merge]
        D5[PRD audit]
        D6[ADR audit]
        D7[EARS validate]
        D8[FMEA enumerate]
        D9[HAZOP apply]
        D10[Decision tables]
        D11[Boundary values]
        D12[Coverage gate]
        D13[Brief lint]
        D14[Atom intake]
    end
    subgraph LLM Synthesis
        L1[Interpret evidence]
        L2[Draft PRD prose]
        L3[Draft ADR prose]
        L4[Rate FMEA severity]
        L5[Write brief guidance]
    end
    D1 --> D2 --> D3 --> L1 --> D4 --> L2 --> D5
    D5 --> L3 --> D6 --> D7 --> D8 --> L4 --> D9 --> D10 --> D11 --> D12
    D12 --> L5 --> D13 --> D14
```

## Related Notes

- [[01 - System Map]]
- [[02 - Deterministic Evidence-Based Development]]
- [[05 - Runner Contracts and Control Rails]]
- [[08 - Planning Packets and Handoff]]
- [[09 - Verification Readiness and Completion]]
- [[10 - Telemetry Audit Eval and Learning]]
- [[20 - PRD ADR Brief Research Workflow PROPOSAL]]
- [[21 - Behavioral TDD Coverage PROPOSAL]]
