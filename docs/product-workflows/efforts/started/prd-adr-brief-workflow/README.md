# PRD/ADR/Brief Workflow Effort

This effort is the durable implementation state for delivering
`docs/design-prd-adr-brief-research-workflow.md`.

## Current State

- Effort ID: `prd-adr-brief-workflow`
- Last completed atom: `impl-0031-phase-5-product-workflow-metrics`
- Active atom: none; next valid action is `plan_phase_5_obsidian_parity_check_atom`
- Status bucket: `started`
- Current phase: `phase-5-execution-feedback`
- Context pack: `dd185841859a40b8`
- Coverage tier: `unit`
- Blocker status: none open; fixture-status conflict resolved before Atom 8b
- Atom 9 report schema validation: all TDD obligations satisfied, product lint passes on 33 durable reports
- Atom 10 product audit command: all TDD obligations satisfied, 9 audit tests pass, product lint unchanged
- Atom 11 PRD audit hard checks: all TDD obligations satisfied; DS4-through-Codex
  CLI was tried twice and fell back to GLM after serious patch-quality issues.
  GLM repaired/completed the atom, and Codex review/verification passed.
- Atom 12 ADR audit hard checks: all TDD obligations satisfied. GLM implemented
  the shared parser/CLI change; Codex removed unrelated backend/config spillover
  before verification, then review/verification passed.
- Atom 13 TDD audit hard checks: all TDD obligations satisfied. GLM implemented
  deterministic PRD-to-TDD cross-link checks; Codex review/verification passed.
- Atom 14 product status view: all TDD obligations satisfied. GLM implemented
  the read-only `idiom product status` view, then completed targeted reviewer
  fixes for validated workflow-state parsing, audit-failure exit behavior, and
  closeout-stable durable fixture tests. Codex review/verification passed.
- Atom 15 workflow transition contract: all TDD obligations satisfied. GLM
  implemented pure `internal/productworkflow` transition/next-action contract
  logic, then completed targeted reviewer fixes for deterministic action order,
  status/state consistency blockers, and removal of the `init`-built transition
  map. Codex review/verification passed.
- Atom 16 scope closeout gate: all TDD obligations satisfied. GLM implemented
  pure changed-path/scope gate logic in new package files; Codex
  review/verification passed. The scope gate is not wired into CLI closeout yet.
- Atom 17 scope gate lint integration: all TDD obligations satisfied. GLM wired
  `EvaluateScopeGate` into product workflow lint/audit for active closeout
  implementation ledger changed paths; Codex required a correction pass for
  deterministic task ordering and explicit historical closed-atom handling, then
  review/verification passed.
- Atom 18 research profile storage: all TDD obligations satisfied. GLM implemented
  deterministic profile discovery under `.idiom/research/profiles`, typed findings
  for invalid/traversal/non-JSON entries, and a read-only `idiom product research`
  JSON surface. No committed .idiom state, no model execution, no merge/evidence
  surface changes. Codex review/verification passed.
- Atom 19: all TDD obligations satisfied. GLM implemented commit_hash ledger
  records and per-atom coverage validation; Codex review/verification passed.
  Each atom's changed_paths can now be validated against its specific commit diff
  when commit_hash is present, preventing piggybacking across atoms.
- Atom 20: all TDD obligations satisfied. Added the deterministic research evidence merge surface. This
  consumes typed researcher output cards, preserves conflicts, and exposes a
  `product research --merge-evidence --profile <profile-id-or-name>` CLI path
  without running models or committing `.idiom` runtime state. Single-profile
  efforts can omit `--profile`; multi-profile efforts must pass it so merge
  policy selection is explicit.
- Atom 21: all TDD obligations satisfied. Added the deterministic research run
  planning surface from stored profiles. This turns selected profile researcher
  rosters into stable run specs and output paths without running models, calling
  `idiom artifact`, writing researcher outputs, drafting PRDs, or writing the
  evidence ledger. The plan surface is mutually exclusive with evidence merge,
  requires explicit profile selection for multi-profile efforts, and fails closed
  on unsafe profile or researcher identifiers.
- Atom 22: all TDD obligations satisfied. Added role-specific research artifact
  prompt rendering for planned runs. This writes deterministic prompt files and
  emits `idiom artifact --no-tools` command recipes for each researcher without
  executing models, writing researcher outputs, merging evidence, or drafting
  PRDs. The render surface fails closed for missing profiles and unsafe prompt
  paths, blocks symlink prompt targets before partial writes, and leaves rendered
  prompt bodies out of durable reports and workflow state.
- Atom 23: all TDD obligations satisfied. Added deterministic PRD draft prompt
  rendering from the evidence ledger. The new `idiom product prd
  --render-prompt` surface writes a bounded PRD prompt file and emits an
  `idiom artifact --no-tools` command recipe without executing models, writing
  `prd.md`, changing ADRs, accepting material decisions, or mutating the
  evidence ledger. It fails closed on missing/invalid evidence, unsafe paths,
  prompt symlink targets, and symlinked prompt ancestors.
- Atom 24: all TDD obligations satisfied. Added deterministic ADR draft prompt
  rendering from the evidence ledger and parsed PRD context. The new
  `idiom product adr --render-prompt` surface writes a bounded ADR prompt file
  and emits an `idiom artifact --no-tools` command recipe without executing
  models, writing ADR markdown or the ADR index, accepting decisions, adding
  supersession links, or mutating PRD/evidence artifacts. It fails closed on
  missing/invalid evidence or PRD inputs, unsafe paths, prompt symlink targets,
  and symlinked prompt ancestors.
- Atom 25: all TDD obligations satisfied. Added deterministic PRD/ADR
  acceptance metadata validation and ADR supersession link checks. Accepted
  PRDs and ADRs now require explicit `accepted_at`, `accepted_by`, and
  `acceptance_profile`; draft/nonaccepted artifacts keep those fields optional.
  ADR markdown and `adrs/index.json` parse and validate `supersedes` and
  `superseded_by`, rejecting broken, duplicate, self-referential, and
  non-reciprocal links through product lint/audit. No model acceptance command,
  auto-accept path, promotion path, TUI/operator behavior, or execution path was
  added.
- Atom 26: all TDD obligations satisfied. Added the deterministic, plan-only
  `idiom product tdd --plan-obligations` surface from accepted PRD requirements
  and accepted ADRs. It emits JSON plan metadata and typed findings, preserves
  PRD/ADR/evidence/TDD references, maps the repository's coverage tiers to
  suggested obligation type and command, blocks when an accepted ADR cannot map
  a requirement, and does not overwrite `tdd.json`, generate briefs, evaluate
  the coverage gate, execute models, or dispatch work.
- Atom 27: all TDD obligations satisfied. Added deterministic
  `idiom product tdd --evaluate-coverage` evaluation over the accepted PRD,
  accepted ADRs, TDD ledger, implementation ledger, and traceability matrix. The
  gate reports tier-specific criteria and blockers, fails closed for unknown
  PRD/ADR TDD obligation references, scopes historical obligation authority to
  verified records for the current effort and PRD task, and does not generate
  briefs, mutate artifacts, execute models, run tests on behalf of the user, or
  dispatch work.
- Atom 28: all TDD obligations satisfied. Added deterministic
  `idiom product brief --render`, which consumes accepted PRD/ADR/TDD artifacts
  plus a passing coverage-gate evaluation, emits or writes a lint-verified
  implementation brief, reports brief-lint blockers before any dispatch-ready
  claim, covers JSON and human-readable output, keeps `--brief-output` under
  `briefs/`, and does not dispatch work or execute models.
- Atom 29 is planned as the first Phase 5 execution-feedback slice. It should
  add deterministic feedback linking from explicit lane/progress/evidence
  references into privacy-safe workflow summaries, preserving authority
  boundaries and avoiding lane dispatch, `.idiom` runtime mutation, evidence
  export writes, ranker-learning promotion, or raw transcript/file-body storage.
- Atom 29: all TDD obligations satisfied. Added deterministic
  `idiom product feedback`, which links explicit lane/progress/evidence
  references into privacy-safe execution feedback summaries, supports JSON and
  human-readable output, blocks unsafe reference paths and raw secret markers,
  preserves existing dispatch authority boundaries, and does not dispatch lanes,
  mutate `.idiom` runtime state, write evidence exports, or promote ranker
  learning.
- Atom 30: all TDD obligations satisfied. Added deterministic
  `idiom product feedback` reconciliation from passing feedback summaries into
  bounded workflow-state/evidence-link output. The command requires matching
  phase/state/revision context, blocks malformed or stale pass summaries,
  rejects direct `workflow_state.json` overwrites, writes requested reconciled
  state through a rooted effort filesystem boundary, and does not dispatch lanes,
  mutate `.idiom` runtime state, generate evidence exports, promote ranker
  learning, or store raw transcript/file bodies.
- Atom 31 is planned as the next Phase 5 execution-feedback slice. It should add
  deterministic product-workflow metrics to product audit or a dedicated product
  audit surface, reporting progress, gates, blockers, open obligations, reports,
  and bounded evidence-link counts without dispatching lanes, mutating `.idiom`
  runtime state, writing evidence exports, promoting ranker learning, or
  enforcing Obsidian parity.
- Atom 31: all TDD obligations satisfied. Added deterministic product-workflow
  metrics to `idiom product audit` and `idiom product status`, covering progress,
  gates, blockers, TDD, ledger, report, artifact, and evidence-link counts. The
  metrics path fails closed with privacy-safe relative artifact errors and keeps
  dispatch, `.idiom` runtime mutation, evidence export generation, ranker
  learning, and Obsidian parity enforcement outside the atom.
- Helper-agent injection for Atom 15: at user request on
  2026-05-17T21:12:38Z, a side-conversation helper added
  `TDD-IMPL-0015-005` as a blocking closeout obligation for the automated
  changed-path/scope gate. This was not produced by the GLM implementor; the
  orchestrator should preserve it during Atom 15 reconciliation.

The workflow implementation remains in progress after Atom 30 closeout. These
files are source-controlled fixtures and resumable implementation state; shipped
behavior includes deterministic product lint/audit/status, research planning and
prompt rendering, PRD/ADR prompt rendering, acceptance/supersession validation,
plan-only TDD obligation synthesis, deterministic coverage-gate evaluation,
lint-verified brief generation, read-only execution feedback linking, and
deterministic feedback reconciliation into bounded workflow-state/evidence-link
output, and product-workflow metrics in product audit/status. The next Phase 5
slice should add Obsidian parity checks to product lint/audit or closeout
reporting; operator surfaces remain later atoms.

## Implementation Guidelines

- Goal: deliver the full `docs/design-prd-adr-brief-research-workflow.md`
  design through durable atoms until the effort can be closed as complete.
- Codex orchestrates, verifies, reviews, investigates, records durable state, and
  commits. Use GPT Codex subagents for future implementation and investigation
  work; do not start new GLM/OpenCode implementors for this effort.
- Before implementation, the orchestrator and any subagent must use Idiom's
  discovery surface where it applies. Start with `idiom tool context_pack` for
  localization, then prefer `context_query`, `context_zoom`,
  `discovery_query`, `structural_search`, `dependency_graph`, or other
  Idiom tools before falling back to raw `rg`/file reads for literal or
  already-localized questions.
- Select the GPT Codex worker by atom complexity and record that choice in the
  ledger:
  - `C1`: `codex-gpt-worker-impl-XXXX`, model `gpt-5.3-codex-spark`,
    reasoning `high`, for small bounded edits.
  - `C2`: `codex-gpt-worker-impl-XXXX`, model `gpt-5.3-codex`, reasoning
    `medium` or `high`, for normal product-workflow code and fixture atoms.
  - `C3+`: `codex-gpt-worker-impl-XXXX`, model `gpt-5.4` or stronger,
    reasoning `high`, for cross-boundary harness, state-machine, audit, or
    multi-package changes.
- Reviewers remain `codex-gpt-review-impl-XXXX`, normally GPT-5.4 with medium
  reasoning unless the atom has `contract_high` blast radius.
- `impl-0008` is a controlled DS4 trial using `ds4/deepseek-v4-flash` through
  OpenCode attached-server execution. Record whether it is actually local if the
  run surfaces process/locality evidence; otherwise treat locality as unverified.
- DS4 trial result for `impl-0008`: the JSON/log run errored with
  `Tool call not allowed while generating summary: read`; the default-output
  retry hit the 10 minute cap with no persisted code changes. DS4 did surface a
  real status-schema conflict in reasoning, but it did not produce a patch.
- DS4 through Codex CLI remains historical evidence only for this effort unless
  explicitly re-authorized. Future planning should use GPT Codex workers instead
  of DS4 or GLM implementor lanes.
- DS4 Atom 11 result: direct Codex CLI passes produced partial patches, then the
  second pass introduced malformed Go, used `git checkout --`, and used ad hoc
  Python rewriting. This met the fallback threshold for serious quality issues;
  GLM repaired/finished the atom. This remains historical evidence only; do not
  use DS4 or GLM as future implementor lanes unless a later human instruction
  explicitly reopens that policy.
- Re-evaluate model stability after each atom and record the model, elapsed time,
  timeout status, and patch completeness in the implementation report.
- Keep GPT Codex worker briefs concise and patch-shaped: provide exact files,
  contract, negative cases, forbidden edits, tests, stop condition, and the
  context_pack id. Do not paste broad repo context already available through
  context_pack or durable docs unless it resolves a specific ambiguity.
- Parallelization policy: run read-only investigation, context gathering, and
  verification in parallel where possible. Run multiple implementors in parallel
  only when their write sets are disjoint; shared parser, validator, CLI,
  durable-state, or report files stay sequential.
- Parallelization should be planned deliberately at atom boundaries:
  - Always keep one orchestrator responsible for durable effort state
    (`workflow_state.json`, `events.jsonl`, `implementation_ledger.jsonl`,
    `tdd.json`, reports, and `docs/product-workflows/efforts/index.json`).
    Durable state remains sequential because event IDs, state revisions,
    checkpoint commits, and per-atom commit coverage are order-sensitive.
  - Parallelize read-only work aggressively: design-doc gap analysis, Idiom
    discovery queries, impact checks, test selection, review preparation,
    Obsidian parity review, and post-implementation code review can run while a
    worker owns a disjoint patch.
  - Prefer parallel implementation only for new-file pure logic with no shared
    CLI, validator, parser, or durable-state edits. Good candidates are isolated
    coverage-gate helpers, pure TDD synthesis helpers, or document-only parity
    updates, provided each worker has an explicit disjoint write set.
  - Do not run parallel implementors that both touch
    `cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`,
    `internal/productworkflow/validate.go`,
    `internal/productworkflow/validate_test.go`, parser/front-matter code,
    scope/commit coverage gates, or durable effort state.
  - For each parallel worker, record ownership, expected files, forbidden files,
    tests, and the integration order in the atom plan. Tell workers they are not
    alone in the codebase and must not revert or rewrite other workers' changes.
  - Integrate one worker at a time: inspect its diff, run focused tests for its
    slice, resolve conflicts locally, then run product lint/audit and full
    verification only after all slices for that atom are merged.
  - If parallel slices require the same shared file after discovery, collapse
    them back into a sequential atom rather than forcing a merge-heavy split.
- Keep implementor briefs patch-shaped, not task-shaped: name the exact
  decision, exact files, expected diff shape, forbidden choices, tests, and stop
  condition. Pre-resolve product/spec conflicts in Codex before handoff instead
  of asking the implementor to discover and decide them while coding.
- Prefer tiny implementation slices when a broader atom has coupled decisions.
  For example, migrate fixture data first, then add enum validation, then wire
  CLI failure behavior. Parallelize GPT Codex workers only when their write sets
  are disjoint; otherwise run sequential patch-shaped briefs and parallelize
  Codex-side context preparation, review, and verification.
- Prefer observable GPT Codex worker runs for future implementor attempts:
  record the worker id, model, reasoning effort, discovery tool evidence,
  changed paths, elapsed time, timeout status, and patch completeness in the
  implementation report.
- Parallelize independent Codex-side reads, reviews, context preparation, and
  verification where this does not duplicate or conflict with a running
  implementor. Preserve sequential atom closeout: each atom still requires its
  own verification, review, investigation, commit, and push before the next atom
  is treated as complete.
- Commit and push after every atom completes.
- Keep the Obsidian vault notes in parity with durable workflow reality. Before
  closing any atom that changes product-workflow status, implemented surfaces,
  phase completion, model policy, or remaining roadmap items, update
  `docs/obsidian/20 - PRD ADR Brief Research Workflow PROPOSAL.md` and
  `docs/obsidian/22 - Product Workflow ROADMAP.md` or record an explicit
  no-change rationale in the investigation report. Treat stale Obsidian status
  labels such as "NOT STARTED" as closeout drift for this effort.
- Use the product workflow CLI surfaces that are ready now during every
  remaining atom. Treat the TUI as not yet authoritative for this workflow until
  a later TUI/operator atom closes.
  - Run `idiom product status --effort-root <path> --json` before planning or
    resuming an atom to read the durable state, next actions, blockers, gates,
    and current phase.
  - Run `idiom product lint --effort-root <path> --json` and
    `idiom product audit --effort-root <path> --json` before atom closeout.
    These are the current workflow-quality gates, alongside focused tests,
    full-suite tests when warranted, JSON/JSONL validation, privacy scans, and
    `git diff --check`.
  - Run `idiom product research --project-root <repo> --json` when an atom needs
    to inspect project-owned research profiles under `.idiom/research/profiles`.
  - Run `idiom product research --project-root <repo> --plan-runs --profile <id>
    --json` when research is needed. The output is a deterministic run plan only;
    it does not execute models, call `idiom artifact`, write researcher outputs,
    draft PRDs, or write the evidence ledger.
  - When researcher output cards already exist, run
    `idiom product research --project-root <repo> --effort-root <effort>
    --merge-evidence --profile <id> --json` to merge them into the durable
    evidence ledger with conflict preservation.
  - Keep generated researcher outputs and model-execution mechanics out of the
    trusted workflow until a later atom explicitly implements and verifies that
    execution path.
- If a GPT Codex worker times out or produces no usable closeout, record a
  `fallback_exception` before orchestration-side code edits. Keep fallback edits
  inside the planned atom scope and add compensating verification.
- Do-now follow-up after Atom 14: plan Atom 15 as workflow hardening. It should
  make workflow-level audit/status failures visible so top-level failures cannot
  be hidden behind all-pass artifact sections, and it should add an automated
  changed-path/scope closeout gate that compares actual changed paths against
  the atom's expected and forbidden files before closeout. The scope gate is
  required because GLM has previously produced unrelated backend/config
  spillover that Codex had to remove manually.
- Atom 15 helper-injected closeout requirement: `TDD-IMPL-0015-005` now makes
  the changed-path/scope gate a blocking obligation before Atom 15 closeout.
  Treat it as user-directed side-thread guidance, not implementor-generated
  scope expansion.
- Coverage quality policy: line coverage is not sufficient for product workflow
  atoms. Each atom that adds or changes artifact parsing, audit logic, status
  output, brief generation, or closeout behavior must include focused unit tests
  for the exact contract, one realistic human-authored fixture variant when
  parsing markdown/YAML/JSON/JSONL or CLI-facing artifacts, at least one
  negative fixture-level CLI test for the new failure mode, a durable fixture
  pass test, and privacy/audit assertions when output includes reports, status
  summaries, telemetry, evidence, or implementation history.
- Parser and CLI tests must not only mirror the current fixture shape. Human
  authored variants should cover reasonable formatting variation such as
  reordered front matter, quoted strings or lists, multiline fields, comments,
  blank lines, heading casing or accepted aliases, and unknown extra fields that
  are either ignored or rejected explicitly.
- Negative CLI tests should assert exit code, JSON shape, typed finding or
  blocker code, and actionable non-JSON text when the command supports text
  output. When a broad end-to-end product surface is not implemented yet, add the
  smallest workflow-level test that exercises the user-visible command from an
  effort folder and checks the next valid action.

## Deferred Follow-ups

These are not blockers for Atom 14 or Atom 15, but future orchestrators should
preserve them until explicitly resolved:

- Parser hardening: current PRD/ADR parsing is intentionally string-shaped and
  fixture-oriented. Before accepting broader user-authored artifacts, add a
  hardening atom that uses a structured YAML/Markdown parser or otherwise
  tightens parsing around multiline fields, heading variants, quoted lists,
  comments, and casing.
- TDD reference authority: decide whether accepted PRD/ADR artifacts should cite
  only current `tdd.json` obligations or all workflow-known historical TDD IDs
  from the implementation ledger. Current audit behavior permits historical
  satisfied IDs to keep durable fixtures valid; this should become an explicit
  policy with tests.
- Coverage gate strictness: the coverage gate must reuse the same traceability,
  artifact-link, and acceptance validation authority as product lint/audit, or
  run those validations as a blocking precondition. Do not let the coverage gate
  become a weaker parallel checker that only verifies a subset of cross-links.
- Supersession fixture and divergence policy: add a durable fixture with at
  least two accepted ADRs in a supersession chain, and test that markdown front
  matter, ADR index entries, audit output, and workflow status agree. Define
  whether markdown or index metadata is authoritative when the two diverge, and
  fail closed until that policy is encoded.
- Obsidian parity gate: the manual parity rule above should become a lint/audit
  obligation before effort closeout. It must catch stale status labels or stale
  phase checkboxes in `docs/obsidian/20 - PRD ADR Brief Research Workflow
  PROPOSAL.md` and `docs/obsidian/22 - Product Workflow ROADMAP.md`, or require
  an explicit no-change rationale in the atom report.
- Human-readable CLI coverage: each new operator-facing product command needs
  tests for text output as well as JSON output, including actionable failure
  text, next valid action, and typed blocker/finding identity.
- Product completion roadmap: after Phase 1 hardening, continue the remaining
  design-doc surfaces rather than treating lint/audit as the product endpoint:
  research profiles, PRD/ADR generation, acceptance and supersession flow, brief
  generation/lint/intake, coverage gate, execution feedback, and operator/TUI
  surfaces.

## Resume Path

Start future sessions by reading:

1. `docs/product-workflows/efforts/index.json`
2. `workflow_state.json`
3. `implementation_ledger.jsonl`
4. `events.jsonl`
5. the latest files under `reports/`

Do not use raw chat transcript memory as the source of truth for this effort.

## Artifact Map

- `source_design_ref.json`: source design and roadmap references.
- `evidence/evidence.jsonl`: privacy-safe evidence cards.
- `evidence/traceability_matrix.json`: evidence-to-brief links.
- `prd.md`: fixture PRD for the Phase 0 substrate.
- `adrs/`: fixture ADR set.
- `tdd.json`: current atom TDD obligations.
- `briefs/`: dispatch-shaped fixture brief.
- `reports/`: implementation, review, and investigation reports.

## Next Valid Action

Plan the next Phase 5 Obsidian parity check atom. Keep the slice deterministic: detect stale proposal/roadmap status during product lint/audit or closeout reporting, or require an explicit no-change rationale, and keep lane dispatch, `.idiom` runtime mutation, evidence export generation, and ranker-learning promotion outside the atom.
