# PRD/ADR/Brief Workflow Effort

This effort is the durable implementation state for delivering
`docs/design-prd-adr-brief-research-workflow.md`.

## Current State

- Effort ID: `prd-adr-brief-workflow`
- Last completed atom: `impl-0018-phase-2-research-profile-storage`
- Active atom: `impl-0019-phase-2-atom-scoped-ledger-coverage-via-commit-hashes`
- Status bucket: `started`
- Current phase: `phase-2-research-profiles`
- Context pack: `1997ef6140265b3d`
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
- Atom 19 (planned): add commit_hash to implementation ledger records and per-atom
  coverage validation to prevent piggybacking. Each atom's changed_paths will be
  validated against its specific commit diff when commit_hash is present.
- Helper-agent injection for Atom 15: at user request on
  2026-05-17T21:12:38Z, a side-conversation helper added
  `TDD-IMPL-0015-005` as a blocking closeout obligation for the automated
  changed-path/scope gate. This was not produced by the GLM implementor; the
  orchestrator should preserve it during Atom 15 reconciliation.

The workflow implementation is in progress. These files are source-controlled
fixtures and resumable implementation state; shipped behavior is limited to the
committed product workflow validators plus lint and audit commands until later
atoms close.

## Implementation Guidelines

- Goal: deliver the full `docs/design-prd-adr-brief-research-workflow.md`
  design through durable atoms until the effort can be closed as complete.
- Codex orchestrates, verifies, reviews, investigates, records durable state, and
  commits. Dedicated implementor lanes make code changes by default; current
  eligible lanes are GLM through OpenCode and bounded DS4 trials through Codex
  CLI.
- Use OpenCode attached-server runs for implementor attempts, with a 15 minute
  cap before declaring timeout evidence:
  `OPENCODE_SERVER_URL=http://127.0.0.1:<port> ./scripts/run-opencode-glm.sh run --format default "<brief>"`.
- Select the implementor model from observed stability, not preference. As of
  `impl-0004`, GLM (`zai-coding-plan/glm-5.1`) is the most stable recorded
  choice because it produced partial patches on prior atoms; MiniMax 2.7
  highspeed (`minimax-coding-plan/MiniMax-M2.7-highspeed`) timed out after
  240 seconds with no output or code edits.
- `impl-0008` is a controlled DS4 trial using `ds4/deepseek-v4-flash` through
  OpenCode attached-server execution. Record whether it is actually local if the
  run surfaces process/locality evidence; otherwise treat locality as unverified.
- DS4 trial result for `impl-0008`: the JSON/log run errored with
  `Tool call not allowed while generating summary: read`; the default-output
  retry hit the 10 minute cap with no persisted code changes. DS4 did surface a
  real status-schema conflict in reasoning, but it did not produce a patch.
- DS4 through Codex CLI is now available for bounded implementor trials:
  `codex exec -p ds4 --cd /Users/ben/Documents/GitHub/idiom --sandbox danger-full-access -c approval_policy="never" --json "<brief>"`.
  A smoke test on 2026-05-17 returned `DS4_CODEX_READY` via JSONL in about 98
  seconds. Use it for the next bounded atom when the write set is clear, with a
  30 minute timeout. Prefer DS4 unless review finds serious quality issues,
  DS4 times out without a usable patch, or DS4 cannot run; GLM remains fallback.
- DS4 Atom 11 result: direct Codex CLI passes produced partial patches, then the
  second pass introduced malformed Go, used `git checkout --`, and used ad hoc
  Python rewriting. This met the fallback threshold for serious quality issues;
  GLM repaired/finished the atom. For the next atom, use the most stable lane for
  the write scope: DS4 may be retried on a small bounded atom only with a
  patch-shaped brief and 30 minute cap, while GLM should remain the fallback for
  shared parser/CLI code or any repeated DS4 edit-quality failure.
- Re-evaluate model stability after each atom and record the model, elapsed time,
  timeout status, and patch completeness in the implementation report.
- Codex-token conservation policy: preserve Codex for orchestration, review,
  verification, reconciliation, and small surgical fixes. Use GLM for code
  drafting on bounded atoms when its expected cleanup cost is lower than direct
  Codex implementation. If an atom is tiny, highly coupled to durable state, or
  GLM would require a long exploratory brief, Codex may implement directly only
  after recording the fallback/exception reason.
- Keep GLM briefs concise and patch-shaped to save Codex tokens: provide exact
  files, contract, negative cases, forbidden edits, and stop condition; do not
  paste broad repo context already available through context_pack or durable
  docs unless it resolves a specific ambiguity.
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
  CLI failure behavior. Parallelize GLM implementors only when their write sets
  are disjoint; otherwise run sequential patch-shaped briefs and parallelize
  Codex-side context preparation, review, and verification.
- Prefer observable OpenCode runs for future implementor attempts:
  `--format json --print-logs --log-level INFO` for raw event/log streaming, and
  `--thinking` only when the provider supports it and the output remains safe to
  exclude from durable reports.
- Parallelize independent Codex-side reads, reviews, context preparation, and
  verification where this does not duplicate or conflict with a running
  implementor. Preserve sequential atom closeout: each atom still requires its
  own verification, review, investigation, commit, and push before the next atom
  is treated as complete.
- Commit and push after every atom completes.
- If an implementor times out or produces no usable closeout, record a
  `fallback_exception` before any Codex code edits. Keep fallback edits inside
  the planned atom scope and add compensating verification.
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

Plan the next Phase 2 atom (research execution or model merge). The research
profile storage substrate is now in place; future atoms should add researcher
execution, evidence merge, conflict detection, and PRD synthesis using the
stored profiles. Codex owns durable closeout.
