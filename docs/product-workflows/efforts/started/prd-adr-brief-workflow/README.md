# PRD/ADR/Brief Workflow Effort

This effort is the durable implementation state for delivering
`docs/design-prd-adr-brief-research-workflow.md`.

## Current State

- Effort ID: `prd-adr-brief-workflow`
- Last completed atom: `impl-0021-phase-2-research-run-plan-from-profiles`
- Active atom: none planned; next action is to plan the next Phase 2 atom or advance phase
- Status bucket: `started`
- Current phase: `phase-2-research-profiles`
- Context pack: `2da56cd96a95bc0c`
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
- Helper-agent injection for Atom 15: at user request on
  2026-05-17T21:12:38Z, a side-conversation helper added
  `TDD-IMPL-0015-005` as a blocking closeout obligation for the automated
  changed-path/scope gate. This was not produced by the GLM implementor; the
  orchestrator should preserve it during Atom 15 reconciliation.

The workflow implementation remains in progress after Atom 21 closeout. These files are source-controlled
fixtures and resumable implementation state; shipped behavior is limited to the
committed product workflow validators plus lint and audit commands until later
atoms close.

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

Plan the next Phase 2 atom or advance phase. Profile storage, deterministic
research run planning, and evidence merge are in place; the next atom should
close the remaining design-doc gap before any later workflow is treated as
fully delivered.
