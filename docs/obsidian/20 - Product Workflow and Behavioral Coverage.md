---
status: current-with-open-work
tags:
  - idiom
  - product-workflow
  - tdd
  - coverage
---

# Product Workflow and Behavioral Coverage

This note is the current Obsidian summary for Idiom's upstream product workflow:
research evidence -> PRD -> ADR -> TDD obligations -> dispatch-ready brief.

It replaces the earlier proposal and roadmap split. The underlying design docs
remain the canonical long-form references:

- [design-prd-adr-brief-research-workflow.md](../design-prd-adr-brief-research-workflow.md)
- [design-tdd-coverage.md](../design-tdd-coverage.md)

## Current State

The workflow has deterministic headless CLI surfaces and durable fixtures. The
closed effort folder under
`docs/product-workflows/efforts/closed/prd-adr-brief-workflow/` is a status
bucket, not a claim that every target design* is complete.

Implemented surfaces include:

- `idiom product lint`
- `idiom product audit`
- `idiom product status`
- `idiom product efforts`
- `idiom product status-migration`
- `idiom product research`
- `idiom product prd --render-prompt`
- `idiom product adr --render-prompt`
- `idiom product tdd --plan-obligations`
- `idiom product tdd --evaluate-coverage`
- `idiom product brief --render`
- `idiom product feedback`

The shipped path is intentionally conservative. Product commands render,
validate, link, and summarize artifacts; they do not turn arbitrary product
intent into write authority by themselves. Dispatch still belongs to Idiom lanes
and their runtime contracts.

`*` marks roadmapped coverage, TUI, sidecar, or telemetry behavior.

## Product Pipeline

```mermaid
flowchart TD
    Intake["Objective, scope, constraints"] --> Research["Research profiles and run specs"]
    Research --> Evidence["Evidence ledger<br/>cards, conflicts, freshness"]
    Evidence --> PRD["PRD<br/>requirements, metrics, evidence refs"]
    PRD --> ADR["ADR set<br/>accepted decisions and supersession links"]
    ADR --> TDD["TDD obligations<br/>commands, owners, coverage tier"]
    TDD --> Coverage["Coverage gate<br/>deterministic thresholds"]
    Coverage --> Brief["Implementation brief<br/>files, behavior, constraints, tests"]
    Brief --> Lint["Brief lint and atom intake"]
    Lint --> Lane["implement or plan-execute lane"]
    Lane --> Feedback["Execution feedback and reconciliation"]
    Feedback -. updates .-> Evidence
    Feedback -. strengthens .-> TDD
```

The main principle is that a brief is the executable handoff, not the whole
product process. Research, requirement shaping, architecture decisions, and test
obligations live upstream so lane execution receives bounded facts instead of a
free-form product conversation.

## What The Harness Owns

| Stage | Harness-owned contract |
| --- | --- |
| Research | Profile shape, planned run specs, output schema, evidence merge, conflict handling |
| PRD | Parser, required metadata, evidence and TDD references, acceptance fields |
| ADR | Parser, Nygard-format structure, PRD/evidence/TDD links, supersession validation |
| TDD | Obligation ledger schema, deterministic obligation plan, command ownership |
| Coverage | Tiered gate evaluation and blocker reporting |
| Brief | Deterministic brief generation, required sections, files, tests, stop rules |
| Dispatch | Brief lint, atom intake, required edit paths, lane contracts |
| Feedback | Privacy-safe execution references, reconciliation, product metrics, ranker-learning evidence |

The model may draft PRD/ADR prose, synthesize examples, or propose risk ratings
when asked by an operator. Every generated artifact must re-enter deterministic
parse, audit, acceptance, coverage, and dispatch gates before it affects work.

## TDD Operator Process

The shipped TDD process is an obligation workflow, not a classic red-green UI
inside the TUI. The operator uses product commands to derive test obligations
from accepted PRD/ADR artifacts, evaluate whether those obligations are covered,
and block brief dispatch when coverage evidence is incomplete.

```mermaid
flowchart TD
    Accepted["Accepted PRD and ADR artifacts"]
    Plan["Plan TDD obligations<br/>idiom product tdd --plan-obligations"]
    Ledger["tdd.json<br/>obligations, owners, commands, coverage tier"]
    Evaluate["Evaluate coverage<br/>idiom product tdd --evaluate-coverage"]
    Gate{"Coverage gate passes?"}
    Brief["Render implementation brief<br/>idiom product brief --render"]
    Blocker["Typed blockers<br/>missing obligations or evidence"]
    Lane["Dispatch through implement<br/>or plan-execute lane"]
    Feedback["Execution feedback<br/>tests, blockers, reconciliation"]

    Accepted --> Plan --> Ledger --> Evaluate --> Gate
    Gate -- yes --> Brief --> Lane --> Feedback
    Gate -- no --> Blocker --> Ledger
    Feedback -. strengthens .-> Ledger
```

Current operator sequence:

1. Accept or update PRD/ADR artifacts through the product workflow.
2. Run `idiom product tdd --effort-root <path> --plan-obligations`.
3. Review `tdd.json` for obligation IDs, owners, commands, and coverage tier.
4. Run `idiom product tdd --effort-root <path> --evaluate-coverage --json`.
5. Resolve coverage blockers by adding or updating required tests, commands,
   evidence refs, or accepted artifact links.
6. Render the implementation brief only after the coverage gate passes:
   `idiom product brief --effort-root <path> --render`.
7. Dispatch the brief through `implement` or `plan-execute`.
8. Feed execution results back into product workflow feedback and future TDD
   obligations when tests, blockers, or recovery show missing coverage.

| TDD surface | Current role |
| --- | --- |
| `tdd.json` | Durable obligation ledger for the effort |
| `--plan-obligations` | Deterministically derives obligations from accepted PRD/ADR artifacts |
| `--evaluate-coverage` | Evaluates current fixture coverage and reports blockers |
| `brief --render` | Blocks when coverage-gate blockers remain |
| Lane verification | Proves required commands after implementation; command names alone are not proof |
| Product feedback | Records execution evidence that can strengthen later obligations |

The important distinction is between **obligation planning** and **verification
proof**. A command listed in `tdd.json` or a brief is pending work. It becomes
evidence only after a run records a passing result, and readiness remains open
until required test evidence is present or a typed blocker explains why it
cannot be produced.

## Behavioral Coverage

The target behavioral coverage design* is broader than the current shipped
gate. The long-form target* remains in
[design-tdd-coverage.md](../design-tdd-coverage.md).

```mermaid
flowchart TD
    ADR["Accepted ADR"] --> EARS["EARS requirements*"]
    EARS --> Examples["Example mapping*"]
    Examples --> BDD["BDD scenarios*"]
    BDD --> Contracts["Design by Contract*"]
    Contracts --> Failure["FMEA / HAZOP / FTA*"]
    Failure --> State["State model and decision tables*"]
    State --> Tests["Test obligations"]
    Tests --> Mutation["Mutation verification*"]
    Mutation --> Gate["Full deterministic coverage gate*"]
    Gate --> Brief["Dispatch-ready brief"]
```

Current implementation is narrower:

- TDD obligation types and ledger parsing exist.
- Deterministic TDD obligation synthesis from accepted PRD/ADR artifacts exists.
- A tiered coverage evaluator exists for current product-workflow fixtures.
- Brief generation blocks on coverage-gate blockers.

Remaining target work* includes EARS parsing*, example mapping*, BDD
validation*, contract schemas*, FMEA/HAZOP/FTA templates*, state-machine
checks*, decision table enumeration*, property-based obligations*, mutation
score calculation*, and the full nine-stage coverage gate*.

## Open Work*

High-signal remaining work*:

- Reuse product lint/audit traceability, acceptance, and supersession authority*
  inside the coverage gate, or run those validators as blocking preconditions.
- Harden status-migration apply temp-path writes against symlink surprises.*
- Add the durable two-ADR supersession fixture* and encode the
  index-vs-markdown divergence authority rule.
- Complete TUI continuation/handoff fixtures* for product workflow actions.
- Expose read-only product workflow status through `idiom tool` and MCP.*
- Add `tdd_eval` research profiles* with FMEA/HAZOP sources.
- Add runtime telemetry events* for research, evidence-card, PRD, ADR, TDD,
  coverage-gate, and dispatch-blocked transitions where durable reports are not
  enough.

## Operator Model*

`*` means this is the target operator model; shipped CLI status and read-only
TUI presenters cover only the implemented subset.

```mermaid
stateDiagram-v2
    direction TB

    [*] --> Draft
    Draft --> ResearchBlocked: missing evidence or conflict
    Draft --> PRDAudit: evidence ledger merged
    PRDAudit --> PRDBlocked: unsupported requirement
    PRDAudit --> ADRAudit: PRD accepted
    ADRAudit --> ADRBlocked: unresolved or invalid decision
    ADRAudit --> TDDAudit: ADRs accepted
    TDDAudit --> TDDBlocked: missing obligation
    TDDAudit --> CoverageGate: obligations ready
    CoverageGate --> CoverageBlocked: incomplete coverage
    CoverageGate --> BriefLint: coverage passes
    BriefLint --> BriefBlocked: malformed brief or weak scope
    BriefLint --> Intake: brief lint passes
    Intake --> IntakeBlocked: stale, dirty, terminal, or invalid scope
    Intake --> DispatchReady: dispatchable
    DispatchReady --> Lane: implement or plan-execute
    Lane --> Feedback: verified result or typed blocker
    Feedback --> [*]
```

The TUI should present this workflow through shared product status and recovery
view models. It should not infer progress from chat transcript text. If
workflow state, artifacts, lane history, plan-execute progress, or recovery
packets disagree, the right result is a reconciliation blocker.

## Related Notes

- [[01 - System Map]]
- [[02 - Deterministic Evidence-Based Development]]
- [[04 - Runtime Lanes]]
- [[08 - Planning Packets and Handoff]]
- [[09 - Verification Readiness and Completion]]
- [[16 - Operational Commands and Evidence Products]]
- [[19 - CLI TUI and Operator Surface]]
- [[21 - Recovery and Decomposition]]
