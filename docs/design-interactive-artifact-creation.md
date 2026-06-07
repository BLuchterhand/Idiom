# Interactive Artifact Creation Command

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

**Status: sparse design draft, created 2026-06-17. No implementation started.**

## Purpose

This feature advances two thesis pillars: **Contract-First Local Autonomy**
(approval gates as human-enforced contracts, phase transitions recorded in
`workflow_state.json`) and **Auditable Planning and Handoff** (artifacts
produced are lint-valid and feed into the existing feature-planner pipeline
with traceable provenance).

Define a new `idiom product create` (or similar) command that walks a human
operator through creating the upstream artifacts the product workflow and
feature-planner need — PRD, ADR set, and TDD ledger — starting from nothing
more than a feature description or problem statement.

Today each artifact has a deterministic renderer (`idiom product prd
--render-prompt`, `idiom product adr --render-prompt`, `idiom product tdd
--plan-obligations`), but each requires upstream artifacts that don't exist
when starting fresh. There is no "start here" flow.

## Problem

The current artifact-creation surface assumes the operator already has:

- `evidence/evidence.jsonl` (research evidence cards)
- `prd.md` (accepted PRD)
- `adrs/index.json` + `adrs/*.md` (accepted ADR set)
- `tdd.json` (TDD ledger with at least some obligations)

A new operator with a feature idea must either:

1. Manually write all these files in the right format, or
2. Run the headless renderers in the wrong order and get errors, or
3. Use `idiom artifact` with a big prompt to generate everything at once,
   bypassing the structured artifact pipeline entirely.

None of these are good. The workflow needs an interactive scaffold that
gathers information from the operator and produces valid, lintable artifacts
without requiring the operator to know the artifact schemas.

## Design Goals

1. **Single entrypoint.** One command (`idiom product create` or similar) that
   covers the full PRD → ADR → TDD creation flow. No chaining multiple
   commands in the right order.

2. **Interactive by default.** The command is a structured TUI
   conversation with the operator. Non-interactive scripting (`--from-prompt`)
   is deferred to a future phase.

3. **Progressive disclosure.** Ask for the minimum information needed at each
   step. Don't require the operator to specify ADR details before the PRD is
   sketched.

4. **Produces lint-valid artifacts.** Every artifact written must pass `idiom
   product lint` (or the relevant subset). The command should not produce
   artifacts that immediately fail validation.

5. **Preserves the existing renderers.** This command composes the existing
   deterministic renderers where possible, rather than duplicating their
   logic. For example, after the operator approves the PRD and ADR set, the
   command can call `idiom product tdd --plan-obligations` to seed the
   TDD ledger from the approved artifacts.

6. **Effort-folder setup.** Creates the full effort directory structure
   (`evidence/`, `adrs/`, `briefs/`, `reports/`, `workflow_state.json`,
   `tdd.json`) so the operator can immediately run `idiom product status`,
   `idiom product brief --render`, or `idiom feature-planner`.

7. **Evidence gathering optional.** The operator can supply evidence
   (research cards, codebase context) or skip it and mark requirements as
   unbacked inference.

## Approval Gates and Iteration Lanes

The human operator must be in charge of approving artifacts before they are
consumed downstream. Two approval gates gate the flow:

### Gate 1: PRD Approval

The PRD is the behavioral and product contract — it defines what to build
and why. Approval is human-initiated and validator-gated:

1. The operator decides the PRD is ready and initiates approval.
2. An intake validator checks every requirement against the PRD template.
   It verifies all required fields are present: `id`, `priority`, `statement`,
   `owner_alignment`, `testability`, `metric`, at least one `evidence_ref`,
   at least one `tdd_obligation`, and `open_questions`.
3. **Pass** → phase advances to ADR creation, approval marked in
   `workflow_state.json`.
4. **Fail** → the validator returns a failure class (e.g.,
   `missing_evidence_refs`, `missing_tdd_obligations`, `empty_priority`)
   naming the specific gap. The agent repairs the PRD in the iteration lane.
   The operator must re-initiate approval after repair.

The operator does not manually check every field — the validator does the
mechanical verification. The operator owns the decision to advance.

### Gate 2: ADR Approval

The ADR set is the architectural and development contract — it defines how to
build. Approval is human-initiated and validator-gated:

1. The operator decides the ADR set is ready and initiates approval.
2. An intake validator checks every ADR against the ADR template. It verifies
   all required sections are present: `## Context`, `## Decision`,
   `## Alternatives Considered`, `## Evidence`, `## Consequences`,
   `## Compatibility and Migration`, `## TDD Obligations`, `## Rollback`,
   `## Open Questions`.
3. **Pass** → phase advances to TDD synthesis, approval marked.
4. **Fail** → the validator returns a failure class naming the missing
   section(s). The agent repairs the ADR set in the iteration lane. The
   operator must re-initiate approval after repair.

As with the PRD gate, the validator handles mechanical completeness. The
operator owns the decision to advance.

### Separation of Concerns

- **PRD** = behavioral/product domain. Requirements, priorities, acceptance
  criteria, user-facing outcomes. The operator reviews this as a product
  owner / stakeholder.
- **ADR** = architectural/development domain. Technology choices, design
  patterns, trade-offs, consequences. The operator reviews this as a tech
  lead / architect.

The two gates ensure that product decisions are settled before architectural
work begins, and that architectural decisions are settled before test
obligations are synthesized.

### State Model

The command tracks its phase in `workflow_state.json` using the existing
state machine in `internal/workflows/productworkflow/transition.go`. The relevant
states and transitions are:

```
init → prd_drafting → prd_review ──approve──→ adr_drafting → adr_review ──approve──→ tdd_synthesis → complete
                ↑                    │                              ↑                    │
                └── request_changes ─┘                              └── request_changes ─┘
```

The existing transition engine (`EvaluateTransition`, `ApplyProductWorkflowTransition`)
already supports `human_verification` state with `approve_for_implementation`
and `request_changes` actions. The artifact-creation command can reuse this
machinery with new phase values (`prd_review`, `adr_review`) or map onto the
existing `human_verification` state with a phase discriminator.


### Intake Validator Schema

The intake validator wraps the existing `validatePRD` and `validateADR`
functions and returns structured failure classes the agent can target for
repair. It follows the `TransitionBlocker` pattern already established in
`internal/workflows/productworkflow/transition.go`.

**Result shape:**

```
type IntakeFailure struct {
    Code     string   `json:"code"`      // machine-readable failure class
    Message  string   `json:"message"`   // human-readable description
    Artifact string   `json:"artifact"`  // "prd", "adr", or "adr_index"
    Path     string   `json:"path"`      // effort-relative file path
    Fields   []string `json:"fields"`    // affected template fields for repair targeting
}

type IntakeResult struct {
    Passed   bool            `json:"passed"`
    Failures []IntakeFailure `json:"failures,omitempty"`
}
```

**Failure codes — PRD:**

| Code | Trigger | Repair hint |
|---|---|---|
| `missing_schema_version` | Front matter `schema_version` empty | Agent fills from context |
| `missing_prd_id` | Front matter `prd_id` empty | Agent derives from effort ID |
| `missing_status` | Front matter `status` empty | Agent sets to `draft` |
| `missing_task_id` | Front matter `task_id` empty | Agent derives from effort ID |
| `missing_created_at` | Front matter `created_at` empty | Agent fills current timestamp |
| `invalid_status` | `status` not a valid artifact state | Agent corrects to valid state |
| `no_requirements` | `requirements` array empty | Agent adds at least one requirement |
| `missing_requirement_id` | Requirement missing `id` | Agent assigns next ID |
| `missing_requirement_priority` | Requirement missing `priority` | Operator or agent assigns P0-P3 |
| `missing_requirement_statement` | Requirement missing `statement` | Agent writes from conversation context |
| `missing_requirement_owner_alignment` | Requirement missing `owner_alignment` | Agent prompts operator or infers |
| `missing_requirement_testability` | Requirement missing `testability` | Agent writes measurable criterion |
| `missing_requirement_metric` | Requirement missing `metric` | Agent writes success metric |
| `missing_requirement_evidence_refs` | Requirement has no `evidence_refs` | Agent adds `inference` ref or prompts for evidence |
| `missing_requirement_tdd_obligations` | Requirement has no `tdd_obligations` | Agent adds obligation reference |
| `missing_requirement_open_questions` | `open_questions` not present | Agent adds `open_questions: []` |

**Failure codes — ADR:**

| Code | Trigger | Repair hint |
|---|---|---|
| `missing_adr_schema_version` | Front matter `schema_version` empty | Agent fills from context |
| `missing_adr_id` | Front matter `adr_id` empty | Agent assigns next ID |
| `missing_adr_status` | Front matter `status` empty | Agent sets to `draft` |
| `missing_adr_prd_refs` | Front matter `prd_refs` empty | Agent links to PRD requirement IDs |
| `missing_adr_evidence_refs` | Front matter `evidence_refs` empty | Agent adds `inference` ref |
| `missing_adr_tdd_obligations` | Front matter `tdd_obligations` empty | Agent adds obligation reference |
| `missing_adr_coverage_tier` | Front matter `coverage_tier` empty | Agent sets to `unit` |
| `invalid_adr_coverage_tier` | `coverage_tier` not in allowed set | Agent corrects to `unit`/`integration`/`e2e` |
| `missing_adr_section_context` | `## Context` section empty | Agent fills from PRD context |
| `missing_adr_section_decision` | `## Decision` section empty | Agent states the chosen option |
| `missing_adr_section_alternatives` | `## Alternatives Considered` empty | Agent lists alternatives |
| `missing_adr_section_evidence` | `## Evidence` section empty | Agent cites evidence or marks inference |
| `missing_adr_section_consequences` | `## Consequences` section empty | Agent enumerates consequences |
| `missing_adr_section_compatibility` | `## Compatibility and Migration` empty | Agent addresses compatibility |
| `missing_adr_section_tdd_obligations` | `## TDD Obligations` section empty | Agent lists obligation references |
| `missing_adr_section_rollback` | `## Rollback` section empty | Agent describes rollback plan |
| `missing_adr_section_open_questions` | `## Open Questions` section empty | Agent adds `open_questions: []` |

**Failure codes — ADR Index:**

| Code | Trigger | Repair hint |
|---|---|---|
| `missing_adr_index_schema_version` | Index `schema_version` empty | Agent fills from context |
| `missing_adr_index_effort_id` | Index `effort_id` empty | Agent derives from effort ID |
| `no_adrs_in_index` | Index `adrs` array empty | Agent adds at least one ADR entry |
| `missing_adr_index_entry_id` | Index entry missing `adr_id` | Agent assigns next ID |
| `missing_adr_index_entry_status` | Index entry missing `status` | Agent sets to `draft` |
| `missing_adr_index_entry_path` | Index entry missing `path` | Agent derives from ADR filename |
| `missing_adr_index_entry_prd_refs` | Index entry missing `prd_refs` | Agent links to PRD requirement IDs |

**Auto-fill vs. operator-input.** Failures with "Agent fills from context" or
"Agent assigns" in the repair hint are self-healable — the agent can fix them
without re-prompting the operator. Failures like `missing_requirement_priority`
or `missing_requirement_owner_alignment` may need operator input. The agent
should attempt auto-fill first, then prompt the operator only when it lacks
enough context to fill the field confidently.

### Iteration Lane Design

Each iteration lane is a loop within the interactive session:

1. **PRD iteration lane:**
   - Agent writes every edit to `prd.md` immediately, loosely following the
     PRD template (see `internal/workflows/productworkflow/prd_draft_prompts.go`)
   - Operator freely adds, edits, or removes requirements
   - Operator can ask the agent to show the current PRD summary at any time
   - When the operator is satisfied, they initiate approval
   - The intake validator runs: checks that all template-required fields
     (`id`, `priority`, `statement`, `owner_alignment`, `testability`,
     `metric`, `evidence_refs`, `tdd_obligations`, `open_questions`) are
     present for every requirement
   - If the validator passes → phase advances to ADR, approval marked
   - If the validator fails → it returns a failure class (e.g.,
     `missing_evidence_refs`, `missing_tdd_obligations`, `empty_priority`)
     so the agent can repair the specific gap and the operator can
     re-initiate approval

2. **ADR iteration lane:**
   - Agent writes every edit to `adrs/*.md` and `adrs/index.json` immediately,
     loosely following the ADR template
   - Operator freely adds, edits, or removes ADRs
   - Operator can ask the agent to show the current ADR index at any time
   - When the operator is satisfied, they initiate approval
   - The intake validator runs: checks that all template-required sections
     (`## Context`, `## Decision`, `## Alternatives Considered`, `## Evidence`,
     `## Consequences`, `## Compatibility and Migration`, `## TDD Obligations`,
     `## Rollback`, `## Open Questions`) are present for every ADR
   - If the validator passes → phase advances to TDD synthesis, approval marked
   - If the validator fails → it returns a failure class so the agent can
     repair and the operator can re-initiate approval

Both lanes are designed to be conversational — the operator can iterate
freely within the lane without losing state. The session is not a rigid
linear form; it's a structured conversation bounded by the current phase.

## Non-Goals

- Do not build an autonomous product manager that approves its own PRDs.
- Do not replace the existing deterministic renderers (`--render-prompt`,
  `--plan-obligations`, `--evaluate-coverage`).
- Do not make the harness choose architecture or implementation strategy.
- Do not require web research for repository-local work.
- The creation scaffold does not hardcode model invocations. The agent
  in the TUI conversation loop makes model calls as part of normal
  operation. The scaffold provides templates and intake validation; the
  agent fills the templates.
- Do not build a general-purpose TUI wizard. The TUI continuation/handoff
  surface is a separate delivery tracked in the PRD/ADR/brief workflow
  effort's open blockers.

## Operator Flow (Sketch)

### Step 1: Initialize effort folder

```
idiom product create --effort-root docs/product-workflows/efforts/started/my-feature
```

Creates the directory structure and `workflow_state.json` in `init` phase.

### Step 2: Describe the feature

Command asks:

- What is the feature or change? (free-text)
- What problem does it solve? (free-text)
- Who is the target user or stakeholder? (free-text)
- What repository or codebase does this apply to? (path, default: `.`)

Writes these as initial evidence cards or a draft PRD preamble.

### Step 3: PRD iteration lane

Command enters the PRD drafting loop. It asks iteratively:

- Requirement N: one-sentence statement
- Priority: P0/P1/P2/P3
- Measurable outcome or acceptance criterion (optional)
- TDD obligation hint (optional — e.g., "unit test", "integration test",
  "manual verification")

The agent writes every edit to `prd.md` immediately. The operator can
freely add, edit, or remove requirements at any time — there is no "submit"
step. When the operator is satisfied, they approve, and the creation command
transitions to `prd_review` phase and presents the PRD for formal approval.

**Approval gate 1** (human-initiated, validator-gated):
1. Operator initiates approval.
2. Intake validator checks every requirement for template completeness.
3. Pass → advance to ADR phase, approval marked.
4. Fail → validator returns failure class, agent repairs, operator
   re-initiates approval.

### Step 4: ADR iteration lane

Command enters the ADR drafting loop. For each material architectural
decision implied by the PRD requirements, the command asks:

- Decision title (e.g., "Choose database backend")
- Options considered (free-text list)
- Chosen option
- Rationale
- Consequences

The agent writes every edit to `adrs/*.md` and `adrs/index.json` immediately.
The operator can freely add, edit, or remove ADRs at any time — there is no
"submit" step. When the operator is satisfied, they approve, and the creation
command transitions to `adr_review` phase and presents the ADR set for formal
approval.

**Approval gate 2** (human-initiated, validator-gated):
1. Operator initiates approval.
2. Intake validator checks every ADR for template completeness.
3. Pass → advance to TDD synthesis, approval marked.
4. Fail → validator returns failure class, agent repairs, operator
   re-initiates approval.

### Step 5: Synthesize TDD obligations

The command runs `idiom product tdd --plan-obligations --effort-root <path>`
(or its internal equivalent) to seed `tdd.json` from the approved PRD and ADRs.

The operator can review and edit the obligations before finalizing.

### Step 6: Set acceptance metadata

Command asks:

- Who is accepting these artifacts? (name/role)
- What profile or authority level?

Writes `accepted_at`, `accepted_by`, `acceptance_profile` to PRD and ADRs.
Transitions to `complete` phase.

### Step 7: Verify and summarize

Runs `idiom product lint --effort-root <path>` and reports any issues.
Prints a summary of what was created and what the next valid action is
(e.g., "run `idiom product brief --render` to generate implementation
briefs" or "run `idiom feature-planner --effort-root <path>` to plan atoms").

## Command Interface Sketch

```
idiom product create --effort-root <path> [flags]

Flags:
  --effort-root <path>       Required. Target effort folder (created if missing).
  --effort-id <id>           Optional effort ID (default: derived from folder name).
  --json                    Structured JSON output instead of interactive text.

Non-interactive flags (`--from-prompt`, `--from-prompt-file`, `--accept`) are
deferred — they belong to a future non-interactive mode (Phase 2), not the
TUI flow this design targets.
```

## Products Panel (TUI)

The idiom TUI gains a "Products" tab that surfaces the operator's active
product workflows. This panel is the discovery surface for efforts created
by `idiom product create` and the entry point for resuming in-progress
sessions.

### Discovery

The panel scans `docs/product-workflows/efforts/started/` (and any other
configured effort roots) for directories containing `workflow_state.json`.
Each valid effort directory becomes a selectable row in the panel.

### Status Display

Each row shows a compact summary derived from `workflow_state.json` and the
effort's artifacts:

- **Phase** — current phase (`prd_drafting`, `prd_review`, `adr_drafting`,
  `adr_review`, `tdd_synthesis`, `complete`), rendered as a status badge
- **Effort ID** — the directory name or `effort_id` from state
- **PRD title** — extracted from `prd.md` front matter if available,
  otherwise the effort ID
- **Requirement count** — number of requirements in `prd.md`
- **ADR count** — number of entries in `adrs/index.json`
- **Last action** — timestamp from the most recent phase transition

### Actions

Selecting an effort row resumes the session:

- If the effort is **in progress** (`prd_drafting` through `tdd_synthesis`):
  opens the `idiom product create` session at the current phase, continuing
  the interactive flow where the operator left off
- If the effort is **complete**: shows a read-only summary with links to run
  `idiom product status`, `idiom product lint`, or `idiom feature-planner`
- **New effort** action at the top of the panel: starts a fresh `idiom
  product create` session

The panel does not implement artifact editing, phase transitions, or model
calling — it delegates to `idiom product create` and the existing product
workflow commands for all actions.

### Interaction with Resume (Q4)

The panel is the primary resume surface. When an operator opens the Products
tab and selects an in-progress effort, the TUI spawns `idiom product create
--effort-root <path>` which reads the current phase from
`workflow_state.json` and resumes at that phase (see Q4).

## Artifacts Produced

| Artifact | Contents | Status |
|---|---|---|
| `workflow_state.json` | Phase `complete`, status `started` | Written |
| `prd.md` | Requirements with IDs, priorities, statuses (`accepted`) | Written |
| `adrs/index.json` | ADR index with `accepted` entries | Written |
| `adrs/ADR-*.md` | One file per decision | Written |
| `tdd.json` | Obligations from PRD/ADR synthesis | Written |
| `evidence/evidence.jsonl` | Optional, if operator supplied evidence | Written |
| `evidence/traceability_matrix.json` | Optional, if evidence supplied | Written |

## Integration with Existing Commands

After `idiom product create` completes, the operator can use any of these
without additional setup:

- `idiom product status --effort-root <path>`
- `idiom product lint --effort-root <path>`
- `idiom product audit --effort-root <path>`
- `idiom product brief --render --effort-root <path>`
- `idiom product tdd --evaluate-coverage --effort-root <path>`
- `idiom feature-planner --effort-root <path> --prompt "..."`

## Open Questions

1. **Conversation-first or operator-brings-requirements?** The existing
   `idiom product prd --render-prompt` flow feeds evidence cards to the model
   and the model synthesizes structured requirements. The creation command
   follows the same pattern: the operator describes the feature in
   conversation (Step 2), the agent produces structured
   requirements from that description, and the iteration lane lets the
   operator refine them. The agent is responsible for avoiding
   duplicates, detecting contradictions, and flagging ambiguities.

   **a. What validation belongs in the intake validator vs. the agent?**
   The intake validator checks mechanical completeness: are all
   template-required fields present? Are priorities valid? Do
   acceptance criteria match the required format? The agent owns
   deeper semantic quality: deduplication, contradiction detection,
   ambiguity flagging, and requirement-quality scoring.

2. **The ADR phase is required — every feature gets an ADR set.** The ADR
   is the bridge between product intent (PRD) and technical implementation.
   It's where the engineer ensures the PRD's behavioral requirements are
   implemented correctly in the target codebase, selecting the right
   patterns, libraries, and boundaries. The agent proposes initial
   ADRs from the PRD requirements; the ADR iteration lane is where the
   engineer adds domain-specific decisions the agent can't infer — auth
   strategy, compliance constraints, deployment topology, internal library
   policies — and removes or rewrites proposed ADRs that don't fit.

3. **What is the minimum viable PRD?** The minimum bar is not a
   requirement count — it's whether the operator has engaged with enough
   conversational prompts for the synthesizer to produce a lint-valid PRD.
   The CLI currently requires at least one `PRDRequirement` with `id`,
   `priority`, `statement`, `owner_alignment`, `testability`, `metric`,
   at least one `evidence_ref`, at least one `tdd_obligation`, and
   `open_questions` present (see `validatePRD` in
   `internal/workflows/productworkflow/validate.go`). The creation command's
   conversation (Step 2–3) is designed to elicit exactly these fields:
   "who is this for?" → owner_alignment, "how do you measure success?" →
   metric, "what does it need to be compatible with?" → evidence context.
   If the operator answers enough prompts, the synthesizer has what it
   needs. If they skip questions, the agent marks fields as
   `inference` or `pending` — and the drafting-phase lint rules may need
   to accept `inference`-level evidence where the accepted-phase rules
   require concrete sources.

4. **Should the command support resuming?** Yes — in initial scope
   (Phase 1). Re-running `idiom product create --effort-root <path>` on an
   existing effort reads the current phase from `workflow_state.json` and
   resumes from that phase rather than starting over. Phase transitions
   are durable. Since the agent writes artifact files immediately during
   iteration lanes (see Q9), all in-progress edits survive exit — resuming
   picks up with the current file contents. The only state lost on exit is
   conversational context (what the operator and agent were discussing
   immediately before exit). If the effort is already at `complete`, the
   command reports status and exits.

5. **How does this relate to the TUI continuation/handoff surface?**
   Resolved by the Products Panel (see above). The Products tab in the TUI
   is the discovery and resume surface for product workflows. It scans
   effort directories for `workflow_state.json`, displays phase and summary
   information, and delegates to `idiom product create` for session
   resumption. The TUI does not need a separate continuation surface for
   product workflows — the Products tab and `create` command together cover
   discovery, creation, resumption, and status viewing.

6. **Does the command need to support editing existing artifacts?**
   Create only. Re-entering via resume (re-running `idiom product create`
   on an in-progress effort) covers iteration and continuation. Post-
   acceptance edits to an `accepted` PRD or superseding an ADR are change-
   management operations — out of scope for the creation command. They
   belong in a separate effort lifecycle: start a new effort that
   references the superseded artifacts, or use a future `idiom product
   amend` command.

7. **What happens when the operator selects an existing effort?** In the
   TUI Products panel, selecting an existing effort resumes it at its
   current phase (see Q4). To start a fresh effort, the operator creates a
   new effort with a different name. The CLI `--force` flag is deferred —
   it's not relevant to the TUI flow this design targets.

8. **Should the approval gates use the existing transition engine?** Yes
   — map `prd_review` and `adr_review` onto the existing
   `human_verification` state with a phase discriminator. The creation
   command owns its own phase tracking (`prd_drafting`, `prd_review`,
   `adr_drafting`, `adr_review`) in `workflow_state.json`. It delegates to
   `EvaluateTransition` / `ApplyProductWorkflowTransition` only for the
   approval/rejection actions themselves (`approve_for_implementation`,
   `request_changes`). The transition engine doesn't need to know about
   creation-specific phases — it handles the approval event, and the
   creation command advances its own phase afterward. This avoids coupling
   the creation workflow's phase model to the atom-execution state
   machine.

9. **How does the iteration lane interact with the existing transition
   state machine?** The agent writes artifact files (`prd.md`, `adrs/*.md`,
   `adrs/index.json`) directly during the iteration lane — every edit is
   persisted immediately. The only state-machine transition is the
   approval gate: when the operator is satisfied with the current phase's
   artifacts, they approve, and the creation command writes a phase
   transition to `workflow_state.json` and advances to the next phase.
   There is no intermediate "submit" step within the lane. Approval means
   "I'm done iterating — proceed to the next phase," not "save my
   changes." The edits are already saved.

10. **Should the command support `--accept` (auto-approve)?** Deferred.
    `--accept` and `--from-prompt` are non-interactive CLI concerns
    (Phase 2). The TUI flow this design targets has the operator present at
    each approval gate. `--accept` can be reconsidered when Phase 2's
    non-interactive mode is designed.

## Implementation Phases (Speculative)

### Phase 1: Minimal scaffold with approval gates

- `idiom product create --effort-root <path>` with interactive Q&A
- PRD iteration lane: add/edit/remove requirements, agent writes immediately
- PRD approval gate: operator approves or requests changes
- ADR iteration lane: add/edit/remove ADRs, agent writes immediately
- ADR approval gate: approve or request changes
- Creates `workflow_state.json`, `prd.md`, `adrs/`, `adrs/index.json`
- Runs `idiom product tdd --plan-obligations` internally
- Runs `idiom product lint` and reports results

### Phase 2: Non-interactive mode (deferred)

- `--from-prompt` and `--from-prompt-file` flags for scripted use
- `--accept` flag to auto-approve both gates
- Parse a single description into structured requirements via heuristics
  (splitting on bullet points, numbered lists, etc.)
- Deferred until the TUI interactive flow is stable

### Phase 3: Evidence integration

- Optional evidence gathering step (codebase context, research cards)
- Writes `evidence/evidence.jsonl` and `evidence/traceability_matrix.json`

### Phase 4: Edit and resume

- Resume support via workflow state
- Edit existing artifacts (add requirement, add ADR, modify obligation)

## Related Documents

- [PRD To ADR To Brief Research Workflow](design-prd-adr-brief-research-workflow.md)
  — the upstream design this command feeds into
- [Feature Planner Orchestration](design-feature-planner-orchestration.md)
  — the downstream consumer of the artifacts this command produces
- [Feature Planner Product Workflow Integration](design-feature-planner-product-workflow-integration.md)
  — how feature-planner reads product workflow artifacts
- [TDD Coverage](design-tdd-coverage.md) — TDD obligation types and coverage tiers
