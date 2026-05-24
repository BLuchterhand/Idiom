## Frontier Routing Implementation Plan

### Goal

Move prompt routing from a narrow heuristic lane picker into a small explicit routing state machine with hard transitions, observable reasons, and lane-specific policies.

### Status

Current implementation status:

- Phase 1 complete
- Phase 2 complete for runtime logging, execution-panel visibility, and answer-contract observability
- Phase 3 complete for prompt-plan follow-up transitions across `locate`, `explore`, and `review`
- Phase 4 complete: deterministic fallback transitions now cover implement guard redirects, explore fallback summaries, review inconclusive stops, and architecture hotspot synthesis
- Phase 5 complete: lane contracts are encoded as typed policies and rendered into lane prompts
- Phase 6 complete: `architecture` is now a first-class lane with explicit routing, prompts, phase cards, and fallback synthesis
- Phase 7 complete for `review`, `architecture`, and `explore`: summary retry requirements, log keys, acceptance checks, and fallback selection are now lane-aware
- Phase 8 complete: structural hotspot ranking now uses deterministic semantic signals including funcs/methods, types, imports, local package imports, fan-in/fan-out, importer pressure, package span, entrypoint concentration, naming-based responsibility clusters, mixed-surface heuristics, and prompt relevance
- Phase 11 complete: `idiom-eval` scenarios can now assert route lane, follow-up lane, route reason, answer contract, and thoroughness from debug logs
- Phase 12 complete: deterministic evidence can be normalized into a host-side evidence ledger, rendered in explore, review, and architecture fallback summaries, and used for non-fallback answer grounding
- Phase 13 complete: analysis lanes can emit evidence-grounded replan recommendations, `idiom-eval` can assert them, and `/replan` can execute the last recommendation on user request
- Phase 14 complete: deterministic language capability registry identifies Go, C/C++, shell, config, docs, prompts, and tests, injects tool capabilities into repository profiles, drives architecture fallback wording, is exposed through `idiom-eval`, and has language-support route coverage
- Phase 15 complete: implementation verification plans now expose risk level, expected failure modes, and repair policy in the execution panel
- Phase 16 complete: verification plans are logged and replay-testable through `idiom-eval` scenario assertions
- Phase 17 complete: final implementation summaries now include verification risk, expected failure modes, and repair policy
- Phase 18 complete: guarded write approval proposals now include verification risk, expected failure modes, and repair policy before the user grants scope
- Phase 19 complete: analysis summary rejections are now observable in `idiom-eval` metrics and enforceable through scenario budgets
- Phase 20 complete: a representative SWE prompt suite now tests routing, follow-up legality, route reasons, answer contracts, and relevant thoroughness across common software engineering tasks
- Phase 21 complete: representative SWE task suites can now score captured logs with task-specific oracles across route, verification, security, failure, multi-turn, tool-budget, and duration-budget expectations
- Phase 22 complete: SWE suites now carry an explicit coverage contract so required task families, risk tags, and minimum task counts cannot silently drift out of the frontier corpus
- Phase 23 complete: SWE suites now run static oracle-quality linting so read-only, security, direct implementation, multi-turn, failure-repair, and tool-error tasks cannot carry weak or unsafe expectations
- Phase 24 complete: `idiom-swe-eval` can now run suite-definition-only gates for fast CI validation of coverage and lint without requiring captured task logs
- Phase 25 complete: coverage contracts now gate behavioral breadth across route lanes, follow-up transitions, answer contracts, verification outcomes, and oracle assertion capabilities
- Phase 26 complete: captured-log SWE reports now aggregate failures by family, initial lane, answer contract, verification expectation, and oracle assertion for faster frontier regression triage
- Phase 27 complete: suite coverage now gates fixture-backed and derived task-shape representation, and suite-only validation enforces relative, existing fixture directories
- Phase 28 complete: coverage contracts now gate route reasons and explicit thoroughness levels, and result summaries bucket failures by route intent
- Phase 29 complete: coverage contracts now gate built-in scenario assertion features so evaluator paths for tools, forbidden tools, phases, evidence, verification plans, approvals, and budget assertions stay exercised
- Phase 30 complete: coverage contracts now gate concrete required tools, forbidden tools, changed-file surfaces, and evidence-path surfaces so labels cannot substitute for asserted artifact coverage
- Phase 31 complete: representative coverage now includes advanced scenario assertions for final-answer constraints, replans, broad-tool budgets, and language capability expectations
- Phase 32 complete: readiness gates are observable through evaluator metrics and representative suite coverage
- Phase 33 complete: readiness failures can trigger bounded deterministic deepening before an answer is accepted as final
- Phase 34 complete: final implementation answers pass through a host-side critic that repairs missing changed-file, verification, risk, and failed-verification disclosures
- Phase 35 complete: route plans now emit explicit effort policy snapshots covering tool budgets, deepening passes, timeout/output budgets, confidence floor, and exhaustion behavior
- Phase 36 complete for architecture-derived refactors and general task memory: resumable refactor stages plus investigation/partial-repair memory now emit structured `long_horizon_state` records, evaluator assertions, suite coverage gates for `persist`/`resume`/`clear`, memory-kind gates, stale target invalidation, and result buckets
- Phase 37 complete for negative-evidence coverage: the representative suite now includes no-match locate, no-bounded-finding review, and false-positive architecture tasks, no-change lint requirements, coverage gates, and result buckets
- Phase 38 complete: analysis runs now emit actual budget ledgers with used budget, evidence gain, stop reason, and user-visible uncertainty policy
- Phase 39 complete: deterministic deepening now emits marginal evidence deltas
- Phase 40 complete: readiness decisions now emit claim-confidence calibration snapshots
- Phase 41 complete: context truncation now emits compaction records with pinned task/refactor evidence
- Phase 42 complete: final-answer critics now cover analysis lane summaries, not only implementation summaries
- Phase 43 complete: prompt routing now emits route-ambiguity and tie-break records
- Phase 44 complete: captured-log SWE regression mode is tracked as the live behavioral regression path
- Phase 45 complete: implementation verification now emits verification-feedback records for future escalation decisions
- Phase 46 complete: failed deterministic tools now emit tool-failure confidence degradation records
- Phase 47 complete: effort policy, budget ledger, evidence delta, readiness, and claim confidence now form an auditable stop-control chain
- routing and transition tests added and passing in the full repository test suite
- architecture prompts that explicitly ask for analysis followed by changes now route as `architecture -> implement`
- architecture summaries now enrich the implementation target packet with hotspot file paths and structural strategy hints before editing begins
- architecture-driven structured execution now has host-enforced policy for bounded op count and structural rationale
- architecture-driven multi-file edits are now stage-gated: first-stage plans prefer one primary file and require explicit cross-file hints plus operation reasons before touching multiple files
- stage journal metadata, post-verification next-stage guidance, durable workspace-scoped resume state, safe resume handling, and execution-panel refactor status are now implemented for architecture-derived refactors
- route plans now carry typed answer contracts, and both route logs and the execution panel expose the imposed answer shape
- deterministic locate summaries now render and validate the `locate_targets` contract before feeding implementation

### Why

The current system mostly answers one question: "which lane should this prompt start in?"

That is not enough for frontier-grade agent behavior. We also need to answer:

- why this route was chosen
- what follow-up state is allowed after analysis
- which transitions are legal
- which transitions are blocked by product policy rather than model preference
- how to observe and test routing decisions directly
- what answer shape is valid for each task class
- when high-level structural advice is acceptable without a bounded defect

### Gap From Latest Failure

The latest failure mode was not lack of evidence. It was task-type confusion.

Observed problem:

- a broad architecture/structure prompt was routed into `review`
- the review lane was optimized for bounded findings and concrete defects
- the synthesizer rejected a useful structural summary because it did not fit the review output contract
- the user received an underpowered answer despite the system already having enough evidence to produce a strong architecture assessment

Root cause:

- the current plan distinguishes route state, but not analysis intent strongly enough
- `review` and `architecture/structure analysis` are materially different task families
- output acceptance rules are too narrow and biased toward finding-oriented summaries

Frontier implication:

- a frontier router must classify not only the lane, but also the answer contract
- structural analysis must be a first-class route with its own evidence standards and synthesis rules

### Target architecture

Use a hybrid router:

1. Hard routing envelope

- Explicit route states: `main`, `locate`, `explore`, `review`, `architecture`, `implement`
- Explicit follow-up transitions:
  - `locate -> implement` for mixed locate/edit requests
  - `explore -> implement` for investigate-then-fix requests
  - `review -> implement` only for bounded actionable findings
  - `architecture -> implement` only when the user explicitly asks to apply the structural change
  - all other transitions terminate back to user-facing summary
- Lane-specific permissions and budgets remain logically imposed

2. Model-driven execution inside each lane

- Once routed, the model still decides local tool usage and reasoning steps
- Deterministic shortcuts and guards remain available where they improve reliability

3. Intent-specific answer contracts

- The router must bind each request to a lane and an answer contract
- The synthesis layer must validate answers against the contract for that lane
- Review-style “one concrete finding” rules must not apply to architecture summaries

### Task Taxonomy

The router should classify prompts into these user-intent families:

1. Direct conversation

- simple question already answerable from current context
- route: `main`
- expected output: direct answer

2. Locate

- identify where code, text, UI, or behavior lives
- route: `locate`
- expected output: concrete target path(s), snippet(s), rationale

3. Review

- ask for bugs, regressions, correctness risks, code review, vulnerabilities
- route: `review`
- expected output: bounded findings ordered by severity

4. Architecture / structure analysis

- ask for organization, modularity, maintainability, layering, package boundaries, structural optimization, decomposition, ownership, codebase shape
- route: `architecture`
- expected output: structural observations, top opportunities, suggested boundaries, confidence

5. Explore

- broad understanding, unfamiliar codebase walkthrough, concept tracing, mechanism explanation
- route: `explore`
- expected output: grounded summary of relevant code paths and relationships

6. Implement

- direct request to modify code
- route: `implement`
- expected output: bounded change plus verification

### Routing Rules For Architecture Prompts

Prompts mentioning terms like these should strongly bias toward `architecture` rather than `review`:

- `architecture`
- `structure`
- `structural`
- `organization`
- `organize`
- `modularity`
- `modular`
- `boundaries`
- `split`
- `decompose`
- `separate responsibilities`
- `maintainability`
- `cleanup`
- `refactor this codebase`
- `optimize the codebase structure`
- `improve package layout`
- `improve ownership`

Additional heuristics:

- if the prompt asks “what optimizations can we make to this codebase’s structure”, never route to `review`
- if the prompt asks for “review for bugs / regressions / risks”, never route to `architecture`
- if the prompt requests both structure analysis and code changes, route to `architecture` with `followup=implement`

### Lane Contracts

Each lane needs a distinct contract.

#### Review

Allowed evidence style:

- concrete bug, risk, regression, missing test, unsafe behavior

Required synthesis shape:

- findings first
- severity ordering
- file references
- brief residual risk / uncertainty

Disallowed synthesis shape:

- vague “maybe this file is large”
- repo-wide architecture advice without bounded defect

#### Architecture

Allowed evidence style:

- large files
- mixed responsibilities
- overloaded packages
- weak boundaries
- duplicated orchestration
- excessive type/function concentration
- poor layering
- language/server/tooling coupling inside generic managers

Required synthesis shape:

- what is structurally sound
- top 3 to 5 optimization opportunities
- why each opportunity matters
- recommended refactor direction
- confidence and what needs deeper validation

Allowed without line-specific defects:

- yes, when grounded in package roles, semantic density, entrypoint concentration, file size, and responsibility mixing

Disallowed rejection reason:

- “missing bounded finding” should never reject an architecture answer

#### Explore

Required synthesis shape:

- concise grounded explanation
- major files/packages involved
- what is verified vs inferred

#### Locate

Required synthesis shape:

- concrete file path(s), lines, snippet(s), best target

#### Implement

Required synthesis shape:

- changed files
- outcome
- verification

### Evidence Thresholds

Review lane threshold:

- requires bounded evidence for at least one concrete finding

Architecture lane threshold:

- any two of the following are sufficient to answer:
  - semantic hot path / very large function surface
  - many types/functions in one file or package
  - multiple unrelated responsibilities in a single module
  - generic manager owning language-specific adapters
  - top-level layout is sound but internal boundaries are weak

This threshold is intentionally lower than review because the user is asking for structural advice, not a defect proof.

### Synthesis Acceptance Rules

Replace the current single rejection logic with task-aware acceptance.

Review summary should be rejected when:

- it lacks a concrete grounded finding
- it has only vague ranking language

Architecture summary should be rejected only when:

- it lacks grounded repository evidence entirely
- it gives generic advice not tied to this codebase

Architecture summary should be accepted when:

- it cites concrete hotspots or package roles
- it proposes plausible structural decompositions
- it distinguishes verified evidence from inference

### Deterministic Architecture Fallback

If the architecture lane cannot produce a final answer cleanly, the host should synthesize a fallback summary from gathered evidence rather than degrade to a review-style “no finding” message.

Fallback structure:

- repository shape
- structural hotspots
- top opportunities
- recommended next validation step

This is different from review fallback, which should prefer “no bounded finding”.

### Phase 1: explicit prompt route plan

Implement a typed route plan object that captures:

- initial lane
- follow-up lane
- routing reason
- requested thoroughness

This replaces a partially implicit `continueAfterAnalysis` boolean with a real transition plan.

Deliverables:

- typed routing reason enum
- typed follow-up transition field
- explicit route selection function with ordered precedence
- tests for mixed locate/edit, direct implement, architecture/explore, and default main routing

Implementation status:

- complete
- route selection now uses a typed `promptExecutionPlan` with explicit lane, follow-up lane, route reason, thoroughness, and answer contract
- route reasons are explicit enums for direct conversation, locate, explore, review, architecture, and direct implementation requests
- route precedence covers mixed locate/edit, investigate/edit, review/edit, architecture/refactor, verification-only, and default direct-answer prompts
- routing tests protect mixed locate/edit, direct implementation, architecture/explore, review, verification, and default main behavior

### Phase 2: route observability

Add direct observability for routing decisions.

Deliverables:

- log the selected route plan and reason
- surface route reason in trace/debug output
- add transcript evidence for route selection where appropriate
- log the selected answer contract as part of the route plan

Implementation status:

- complete
- route plans include typed answer contracts for direct answers, locate targets, grounded explanations, review findings, architecture assessments, and implementation results
- route-plan logs render `contract=...`
- execution-panel metadata renders the active answer contract alongside lane, follow-up, and route reason

### Phase 3: lane transition guardrails

Move major post-analysis jumps behind explicit transition rules instead of ad hoc booleans.

Deliverables:

- `locate` follow-up restricted to `implement`
- `explore` follow-up restricted to `implement` or summary
- `review` follow-up restricted to `implement` only for bounded findings
- `architecture` follow-up restricted to `implement` only when the user explicitly asks to apply the structural refactor
- no hidden cross-lane jumps

Implementation status:

- complete
- analysis transitions are represented by explicit transition helpers rather than hidden booleans
- `locate`, `explore`, `review`, and `architecture` handoffs are constrained to legal follow-up lanes before implementation starts
- architecture-to-implement handoff carries source-lane metadata and structural hints rather than falling through a review contract
- transition tests cover legal follow-up behavior and reject hidden cross-lane jumps

### Phase 4: deterministic fallback transitions

When a routed lane fails in a known way, apply bounded deterministic fallback before giving up.

Examples:

- `implement` with target packet + broad discovery request -> redirect to scoped target inspection
- `explore` after broad overview -> redirect to narrowing tools
- `review` with weak evidence -> terminate with explicit uncertainty instead of broadening indefinitely
- `architecture` with enough grounded hotspot evidence -> synthesize structural summary even if the model fails to format it cleanly

Implementation status:

- complete
- implement lanes redirect broad discovery back to scoped target inspection when a target packet exists
- explore lanes reject ungrounded or tool-formatted summaries and fall back to deterministic bootstrap evidence
- review lanes stop before editing when no bounded actionable finding is confirmed
- architecture lanes synthesize grounded hotspot summaries from deterministic structural evidence

### Phase 5: policy-first lane contracts

Define each lane as a contract:

- allowed tools
- forbidden tools
- expected outputs
- budget
- fallback behavior
- synthesis acceptance rules

This should be encoded both in prompts and in host-side logic.

Implementation status:

- complete
- each lane has a typed contract policy with expected outputs, forbidden behavior, and fallback policy
- explore, review, architecture, and implement prompts render their lane contract before task-specific rules
- deterministic locate summaries render the locate contract and are host-validated before they are accepted as implementation context
- host-side route plans and execution panels carry the same answer-contract identity used by prompt policy

### Phase 6: First-Class Architecture Lane

Implement `architecture` as a first-class route and analysis lane.

Deliverables:

- new `analysisLaneArchitecture`
- new route reason such as `architecture_request`
- architecture task prompt
- architecture summary acceptance rules
- architecture fallback synthesizer
- architecture-specific status and phase messages

Implementation status:

- complete
- routed prompts now use `analysisLaneArchitecture` and `promptRouteReasonArchitectureRequest`
- the architecture lane has dedicated system/task prompts, deterministic bootstrap evidence, and architecture-specific phase messaging
- architecture summaries can now feed bounded implementation targeting by contributing hotspot paths and strategy hints to the target packet
- architecture-derived target packets now carry source-lane metadata and structural hints into the implementation planner

### Phase 7: Task-Aware Summary Validation

Replace a single review-oriented summary validator with lane-aware validators.

Deliverables:

- `review` validator
- `architecture` validator
- `explore` validator
- route-aware fallback summary selection

Implementation status:

- complete
- `review`, `architecture`, and `explore` now use lane-aware retry instructions, log keys, acceptance checks, and fallback selection
- grounding still requires exact hotspot/candidate paths
- `explore` summaries now require grounded evidence and verified-vs-inferred language; invalid explore responses fall back to a deterministic summary from bootstrap evidence

### Phase 8: Structural Heuristic Board

Add deterministic structural signals to support architecture answers.

Signals:

- functions per file
- types per file
- package fan-in / fan-out where cheaply available
- manager/god-object patterns
- entrypoint concentration
- mixed responsibility heuristics by naming and tool ownership

Implementation status:

- complete
- the architecture lane now ranks deterministic hotspots from semantic signals:
  - funcs/methods per file
  - types per file
  - imports per file
  - local package imports
  - explicit fan-out through local imports
  - explicit fan-in through workspace importer files
  - workspace importer pressure
  - package span
  - entrypoint concentration
  - naming-based mixed-responsibility clusters from paths, imports, and symbols
  - mixed orchestration/integration surface heuristics
  - prompt relevance

### Phase 9: Structural Execution Policy

Harden architecture-to-implement execution with host-side constraints so structural refactors do not depend only on model obedience.

Deliverables:

- architecture-aware structured plan rules
- host-side validation for operation count and structural rationale
- architecture-aware target packet metadata for the implementation planner

Implementation status:

- complete for bounded structural edits
- architecture-derived structured plans now:
  - carry source-lane and strategy hints into the target packet
  - face tighter op-count limits
  - must include structural reasons aligned with the requested boundary change
  - render a first-stage structural refactor hint in the implementation target packet
  - reject multi-file first-stage edits unless the structural hints and operation reasons explicitly justify a cross-file boundary move

### Phase 10: Resumable Refactor Stages

Persist structural refactor stage state across turns so larger refactors can proceed as a sequence of verified bounded stages.

Deliverables:

- stage journal for architecture-derived refactors
- explicit next-stage recommendation after verification
- user-visible stage status in the execution panel
- safe resume command or prompt handling for the next stage

Implementation status:

- complete
- implemented:
  - stage journal metadata for the last bounded architecture-derived refactor
  - explicit next-stage recommendation after verification
  - user-visible refactor stage status in the execution panel
  - safe in-memory resume command and prompt handling for the next bounded stage
  - architecture-summary stage numbering so resumed target packets stay stage-aware
  - durable stage state beyond the current in-memory last-change journal
  - workspace-scoped resume reload after restart
  - cleanup of non-resumable or rolled-back stage records

### Completed Implementation Slices

Implemented slices:

1. typed route plan with explicit lane, follow-up, reason, thoroughness, and answer contract
2. route-plan logging and execution-panel visibility
3. explicit transition helpers for analysis-to-implementation handoffs
4. deterministic fallback transitions for implementation, explore, review, and architecture lanes
5. first-class architecture routing and synthesis
6. lane-aware summary validators for review, architecture, explore, and locate
7. deterministic architecture fallback synthesis
8. resumable architecture-derived structural refactor stages
9. route-and-contract log evals for replaying prior failures and protecting routing behavior

### Phase 11: Route And Contract Eval Harness

Convert routing quality into replayable scenarios so prior failures and future regressions can be tested outside the interactive app.

Deliverables:

- parse `route_plan` debug-log records
- expose route plans in eval metrics and CLI output
- let scenarios require initial lane, follow-up lane, route reason, answer contract, and thoroughness
- include tests for passing and failing route-plan expectations
- include suggested scenario generation from observed route plans

Implementation status:

- complete
- `internal/eval` now records route-plan snapshots from `route_plan` messages
- `Scenario.require_route_plan` can assert lane, follow-up, reason, contract, and thoroughness
- `BuildSuggestedScenario` captures the latest observed route plan
- `idiom-eval` prints the latest route plan in human-readable metrics
- focused eval tests cover route extraction, route mismatch reporting, route expectation passing, and suggested scenario generation

### Phase 12: Evidence Ledger And Provenance

Normalize deterministic lane evidence into a compact ledger so summaries can distinguish verified facts from inferred gaps.

Deliverables:

- collect verified paths from search, read, list, and semantic tool results
- record tool coverage and tool errors
- record inferred gaps that still need targeted validation
- render the ledger into fallback summaries
- attach ledger metadata to lane summaries and eval reports

Implementation status:

- complete
- implemented:
  - `evidenceLedger` with verified paths, tools, errors, and inferred gaps
  - deterministic ledger construction from `workspace.ToolResult` values
  - evidence-ledger rendering in explore fallback summaries
  - evidence-ledger rendering in review fallback summaries
  - evidence-ledger rendering in architecture fallback summaries
  - deterministic candidate and hotspot paths folded into ledger provenance
  - `idiom-eval` evidence-path metrics extracted from debug-log tool summaries
  - scenario assertions for required evidence paths
  - suggested scenario generation for observed evidence paths
  - non-fallback explore/review/architecture answer acceptance now uses ledger-derived verified paths for grounding
  - tests for ledger extraction/rendering and fallback summary provenance
  - tests for ledger-grounded non-fallback answer acceptance

### Phase 13: Mid-Run Replanning

Detect when evidence gathered by one lane actually satisfies a different lane contract, then surface a legal replan recommendation instead of silently forcing the original route.

Deliverables:

- detect cross-contract analysis outputs after lane completion
- require grounded paths before recommending a replan
- surface the recommended lane and reason in the final summary
- log replan recommendations for eval and replay
- support explicit command-based resume into the recommended lane

Implementation status:

- complete
- implemented:
  - deterministic replan recommendation helper for review, explore, and architecture summaries
  - grounded-path requirement before any replan recommendation is emitted
  - evidence-ledger paths can satisfy the grounding requirement when the summary has the right lane shape but omits explicit paths
  - summary rendering for recommended next lane and reason
  - debug-log `replan_recommendation` messages when recommendations are emitted
  - `idiom-eval` metrics and scenario assertions for replan recommendations
  - `/replan` command to explicitly execute the last recorded recommendation
  - replan prompt construction that carries the original request and prior evidence summary
  - tests for structural review-to-architecture recommendation, ungrounded rejection, and evidence-ledger grounding

### Phase 14: Language Capability Registry

Make language/tool support explicit so answers about Go, C/C++, shell, config, and future languages are grounded in detected project capabilities.

Deliverables:

- detect language families from repo files and module markers
- map each language family to available semantic or validation tools
- inject capability registry into repository profiles used by analysis lanes
- expose registry in evidence/debug output
- route language-specific questions through capability-aware prompts and fallbacks

Implementation status:

- complete
- implemented:
  - deterministic detection for Go, C/C++, shell, config, docs, prompt assets, and test surfaces
  - tool capability mapping for Go semantic tools, C/C++ semantic tools, shell checks, config validation, docs edits, prompt profile inspection, and regression checks
  - repository-profile injection for explore, review, and architecture bootstraps
  - `idiom-eval` language capability metrics extracted from debug-log repository profiles
  - scenario assertions for required language, tool, and detection signal capabilities
  - suggested scenario generation for observed language capability registries
  - language-aware architecture fallback wording for Go, C/C++, docs, and prompt-system surfaces
  - tests for registry detection/rendering and profile injection
  - tests for eval parsing, matching, failure reporting, and suggested language capability scenarios
  - tests for language-aware architecture fallback wording
  - route tests for language-support and capability-organization questions

### Phase 15: Verification Planner Upgrade

Make implementation verification plans describe not only which commands to run, but also the risk profile, likely failure modes, and repair policy required before success can be claimed.

Deliverables:

- classify changed-path risk as low, medium, high, or unknown
- enumerate expected failure modes based on changed surfaces
- distinguish targeted, importer, full-suite, syntax-only, compile-only, and unknown verification confidence
- render verification risk and repair policy in the execution panel
- test planner behavior across local Go package, core runtime, shell script, and docs-only edits

Implementation status:

- complete
- implemented:
  - `verificationPlan` risk metadata: `RiskLevel`, `ExpectedFailures`, and `RepairPolicy`
  - path-aware risk classification for Go, shell, docs, prompt/config, and core app surfaces
  - expected-failure inference for compile errors, package regressions, routing/prompt regressions, shell syntax errors, and integration regressions
  - repair-policy summaries tied to full-suite, targeted/importer, medium-confidence, smoke, or unavailable coverage
  - execution-panel rendering for risk, expected failures, and repair policy
  - tests for local Go package targeting, core runtime full-suite escalation, shell syntax/full-suite escalation, docs-only low-risk smoke coverage, and panel rendering

### Phase 16: Verification Contract Eval Harness

Make verification quality observable and replayable so implementation runs can be tested against the planned risk contract, not only against whether a shell command happened to pass.

Deliverables:

- log the computed verification plan before checks run
- parse verification-plan log records in `idiom-eval`
- expose latest verification-plan metadata in CLI metrics
- let scenarios assert coverage, confidence, risk level, full-suite escalation, required commands, and expected failure modes
- include suggested scenario generation from observed verification plans

Implementation status:

- complete
- implemented:
  - `verification_plan` debug-log messages with commands, reasons, full-suite flag, coverage, confidence, risk, expected failures, and repair policy
  - `VerificationPlanSnapshot` metrics in `internal/eval`
  - `Scenario.require_verification_plan` assertions
  - human-readable `idiom-eval` verification-plan output
  - suggested scenarios that capture the latest observed verification plan
  - tests for parsing, metrics extraction, mismatch reporting, and suggested scenario generation

### Phase 17: Verification-Aware Final Summaries

Make implementation closeout answers carry the same verification risk contract as the execution panel and debug logs so the user sees what was validated and what failure modes the run was designed to catch.

Deliverables:

- carry verification risk metadata through `implementationVerification`
- render risk level in final implementation summaries
- render expected failure modes in final implementation summaries
- render repair policy in final implementation summaries
- keep per-command pass/fail output unchanged

Implementation status:

- complete
- implemented:
  - `implementationVerification` now carries risk, expected failures, and repair policy from the verification plan
  - `composeImplementationSummary` renders confidence, risk, expected failure modes, repair policy, and command outcomes together
  - tests cover final summary rendering for high-risk verification contracts

### Phase 18: Verification-Aware Write Approval

Make pre-write approval packets carry the same verification risk contract as execution and final summaries so the user can approve scope with an explicit view of likely failure modes and repair expectations.

Deliverables:

- carry verification risk metadata into write approval proposals
- render expected failure modes in approval summaries
- render repair policy in approval summaries
- preserve existing impact, blast-radius, deep-analysis, and verification-command guidance

Implementation status:

- complete
- implemented:
  - write approval proposals now store verification risk, expected failures, and repair policy
  - approval rendering includes a `Verification risk contract` section
  - protected-path approval tests assert risk contract rendering and shell failure-mode coverage

### Phase 19: Summary Acceptance Eval Metrics

Make malformed analysis-lane outputs replay-testable by surfacing summary rejection events in eval metrics and scenario budgets.

Deliverables:

- count analysis summary rejections from debug logs
- record the lanes that rejected summaries
- print summary rejection counts in human-readable `idiom-eval` output
- let scenarios cap acceptable summary rejections
- include suggested scenario generation from observed rejection counts

Implementation status:

- complete
- implemented:
  - `Metrics.summary_rejections` and `summary_rejection_lanes`
  - `Scenario.max_summary_rejections`
  - summary rejection extraction from lane summary log messages
  - CLI output for rejection counts and lanes
  - tests for metrics extraction, mismatch reporting, and suggested scenario generation

### Phase 20: Representative SWE Prompt Suite

Generalize routing coverage beyond isolated regression cases by maintaining a named suite of common software engineering prompts.

Deliverables:

- document the representative SWE task taxonomy
- cover direct questions, verification-only requests, locate, locate-then-edit, explore-then-implement, review, review-then-fix, security/performance audits, cleanup, architecture, architecture-then-implement, and direct edits across code/tests/config/scripts/docs/prompts/shell
- assert lane, follow-up lane, route reason, answer contract, and thoroughness when relevant
- use the suite to expose and fix classifier gaps

Implementation status:

- complete
- implemented:
  - `docs/representative-swe-prompt-suite.md`
  - `TestRepresentativeSWEPromptSuiteRoutesTypicalTasks`
  - classifier support for `then update` / `and update` implementation follow-ups
  - classifier support for `investigation` phrasing as an exploration signal
  - full-suite regression coverage through `go test ./...`

### Phase 21: Executable SWE Task Eval Harness

Turn representative SWE prompts into a replayable task corpus that can evaluate complete captured agent runs, not only route classification.

Deliverables:

- task suite schema with embedded `idiom-eval` scenarios
- SWE-specific oracle layer for changed files, forbidden files, approvals, shell use, errors, turns, tool budgets, and duration budgets
- representative suite fixture under `testdata/swe`
- `idiom-swe-eval` CLI for one-log-per-task scoring
- starter adversarial and failure fixtures for prompt-injection and broken-Go repair tasks
- docs and tests for the suite loader, oracle scoring, missing-log failures, and family coverage

Implementation status:

- complete
- implemented:
  - `internal/eval` SWE suite loader, validator, evaluator, reports, and task oracle checks
  - `Metrics.duration_millis` from timestamped JSONL records
  - `cmd/idiom-swe-eval` human-readable and JSON reporting
  - `testdata/swe/representative-suite.json` spanning common SWE task families
  - fixture repositories for adversarial README content and Go compile failure repair
  - `docs/swe-eval-suite.md`
  - Makefile entrypoint through `make swe-eval LOGS=/path/to/logs`

### Phase 22: SWE Suite Coverage Contracts

Make corpus breadth testable as a first-class harness property. A frontier harness should fail when the task set loses security, recovery, multi-turn, or implementation-family representation, even if the remaining logs pass.

Deliverables:

- suite-level coverage contract for minimum task count
- required family coverage
- required tag coverage
- per-family minimum counts
- coverage report in library and CLI output
- validation failure when a suite violates its own coverage contract
- tests for missing families, missing tags, underrepresented families, and representative fixture existence

Implementation status:

- complete
- implemented:
  - `SWECoverageContract` and `SWECoverageReport`
  - `AnalyzeSWESuiteCoverage`
  - coverage gating in `ValidateSWESuite` and `EvaluateSWESuite`
  - CLI coverage summary in `idiom-swe-eval`
  - representative-suite coverage contract in `testdata/swe/representative-suite.json`
  - tests proving coverage failures are surfaced separately from task-log failures

### Phase 23: SWE Suite Oracle Linting

Prevent weak task definitions from producing misleading green evals. Frontier harnesses need static checks that the oracle itself is strong enough for the task family before any model run is trusted.

Deliverables:

- lint `read-only` tasks for no-change requirements
- lint security/adversarial tasks for write forbids or no-change requirements
- lint direct implementation tasks for changed-file or approval assertions
- lint multi-turn tasks for minimum turn assertions
- lint failure-repair tasks for verification expectations
- lint tool-error tasks for error budgets
- surface lint failures in validation, JSON reports, and CLI output
- tests for weak-oracle detection

Implementation status:

- complete
- implemented:
  - `LintSWESuite`
  - validation failure for lint findings
  - `SWESuiteReport.lint_findings`
  - CLI lint summary
  - representative suite lint coverage
  - regression tests for unsafe or under-specified task definitions

### Phase 24: Suite-Only Eval Mode

Separate corpus-definition quality from captured-run quality so CI can quickly reject weak task suites before expensive or environment-dependent model runs.

Deliverables:

- load suite JSON without immediately enforcing validation
- evaluate suite definition coverage and lint without task logs
- expose a `idiom-swe-eval -suite-only` mode
- preserve normal captured-log mode validation before log scoring
- tests proving definition-only reports do not require logs

Implementation status:

- complete
- implemented:
  - `ReadSWESuite` for decode-only loading
  - `EvaluateSWESuiteDefinition`
  - `idiom-swe-eval -suite-only`
  - docs for suite-only validation
  - regression coverage for log-free suite definition evaluation

### Phase 25: Behavioral Coverage Dimensions

Extend corpus coverage beyond labels. A frontier suite should prove that the harness exercises route behavior, answer contracts, verification outcomes, and oracle capability types, not only task names.

Deliverables:

- required initial-lane coverage
- required follow-up lane coverage
- required answer-contract coverage
- required verification outcome coverage, including expected failures
- required oracle-assertion coverage
- representative failing-verification read-only task
- CLI coverage counts for behavioral dimensions
- tests for missing behavioral coverage dimensions

Implementation status:

- complete
- implemented:
  - coverage maps for initial lanes, follow-up lanes, answer contracts, verification states, and oracle assertions
  - coverage contract fields for each behavioral dimension
  - `verification-only-report-failure` representative task over the seeded compile-failure fixture
  - stricter default suite contract requiring `none`, `passed`, and `failed` verification states
  - required oracle assertion coverage for no-change, changed-file, forbidden-file, budget, approval, shell, summary-rejection, and verification-plan checks
  - CLI coverage summary for all behavioral maps

### Phase 26: Result Coverage Breakdown

Make failed captured-log suites diagnosable by coverage dimension. Frontier harnesses should tell whether the regression is concentrated in architecture, review, failed verification reporting, or a specific oracle type.

Deliverables:

- aggregate pass/fail totals
- missing-log and load-error counts
- pass/fail buckets by task family
- pass/fail buckets by initial lane
- pass/fail buckets by answer contract
- pass/fail buckets by verification expectation
- pass/fail buckets by oracle assertion type
- concise CLI failure bucket output and full JSON summary

Implementation status:

- complete
- implemented:
  - `SWEResultSummary`
  - `SWEResultBucket`
  - result aggregation in captured-log evaluation
  - definition-only summary for coverage/lint status
  - CLI result line and failed-family / failed-lane summaries
  - regression tests for missing-log and load-error summary accounting

### Phase 27: Fixture And Task-Shape Coverage

Make adversarial, failure, protected approval, multi-turn, rollback, and fixture-backed task structures first-class coverage dimensions. Frontier harnesses should fail when the corpus keeps labels but loses the structural cases that actually exercise agent behavior.

Deliverables:

- required fixture coverage
- required derived task-shape coverage
- fixture path syntax linting
- suite-only fixture existence validation
- result breakdown by fixture and task shape
- tests for missing fixture coverage, missing task-shape coverage, invalid fixture paths, and missing fixture directories

Implementation status:

- complete
- implemented:
  - `required_fixtures` and `required_task_shapes` coverage contract fields
  - `fixtures` and `task_shapes` coverage maps
  - derived task shapes for read-only, fixture-backed, multi-turn, protected approval, direct implementation, analysis-then-implementation, verification-only, expected failure, failure repair, tool-error recovery, rollback, security/adversarial, and surface-specific edits
  - `ValidateSWESuiteFixtures`
  - `EvaluateSWESuiteDefinitionWithFixtures`
  - CLI suite-only fixture checks
  - result buckets by fixture and task shape
  - representative suite contract requiring adversarial and compile-failure fixtures

### Phase 28: Route Intent Coverage

Cover why a route was chosen, not just which lane it entered. Frontier behavior depends on route reasons and thoroughness contracts because they drive prompt policy, evidence depth, and follow-up expectations.

Deliverables:

- required route-reason coverage
- required thoroughness coverage
- coverage maps for route reasons and thoroughness
- result breakdown by route reason
- CLI coverage counts for route reasons and thoroughness
- tests for missing route-reason and thoroughness coverage

Implementation status:

- complete
- implemented:
  - `required_route_reasons`
  - `required_thoroughness`
  - `route_reasons` and `thoroughness` coverage maps
  - result buckets by route reason
  - representative suite requirements for direct conversation, locate, explore, review, architecture, direct implementation, medium, and thorough coverage

### Phase 29: Scenario Assertion Feature Coverage

Cover the evaluator's own assertion surface. A frontier suite should fail if common scenario assertions disappear from the representative corpus, because unexercised assertion paths silently decay.

Deliverables:

- required scenario assertion coverage
- scenario assertion coverage map
- result breakdown by scenario assertion type
- CLI coverage count for scenario assertions
- default suite requirements for prompt, tool, forbidden-tool, phase, route-plan, repeated-dead-end, summary-rejection, verification, verification-plan, changed-file, evidence-path, and approval assertions
- tests for missing scenario assertion coverage

Implementation status:

- complete
- implemented:
  - `required_scenario_assertions`
  - `scenario_assertions` coverage map
  - derived scenario assertion extraction from suite tasks
  - result buckets by scenario assertion type
  - representative suite contract covering the active scenario assertion surface

### Phase 30: Tool And Artifact Surface Coverage

Make the suite prove it asserts concrete tools and files, not only high-level task labels. Frontier evals should fail if shell verification, forbidden write tools, or code/docs/config/script/test/prompt surfaces disappear from the corpus.

Deliverables:

- required concrete tool coverage
- required forbidden-tool coverage
- required changed-file surface coverage
- required evidence-path surface coverage
- result buckets by required tool and file surface
- CLI counts for tool and artifact coverage maps
- tests for missing tool, forbidden-tool, file-surface, and evidence-surface coverage

Implementation status:

- complete
- implemented:
  - `required_tools`
  - `required_forbidden_tools`
  - `required_file_surfaces`
  - `required_evidence_surfaces`
  - `tools`, `forbidden_tools`, `file_surfaces`, and `evidence_surfaces` coverage maps
  - file-surface derivation for Go, docs, config, script, test, prompt, and other paths
  - result buckets by required tool and file surface
  - representative suite requirements for shell, write-tool forbids, Go/docs/config/script/test/prompt changed-file surfaces, and Go evidence surfaces

### Phase 31: Advanced Scenario Assertion Coverage

Close the remaining evaluator-surface gap by requiring assertions that constrain final answer content, safe replanning, broad tool requests, and language capability discovery.

Deliverables:

- final assistant required-text coverage
- final assistant forbidden-text coverage
- replan assertion coverage
- broad-tool budget coverage
- language capability assertion coverage
- representative suite tasks exercising each advanced assertion type

Implementation status:

- complete
- implemented:
  - direct answer task now asserts required and forbidden final-answer text
  - security review task now asserts a zero broad-tool budget
  - direct Go edit task now asserts Go language capability discovery
  - tool-error recovery task now asserts an explore-to-implement replan recommendation
  - default coverage contract requires `final_assistant_contains`, `forbid_final_assistant_contains`, `replan`, `broad_tool_budget`, and `language_capabilities`

### Phase 32: Readiness Gates And Adaptive Search Depth

The previous plan implied this through thoroughness, verification, and summary rejection budgets, but it did not make response readiness first-class. That is a gap. Frontier behavior needs an explicit stop policy: the answer is ready only when the task contract, evidence state, uncertainty level, and verification state are coherent.

Deliverables:

- structured readiness checks in debug logs
- evaluator assertions for readiness lane, ready/not-ready state, confidence, action, reason text, and evidence-path count
- suite coverage for readiness assertions and readiness actions
- runtime rejection of low-confidence analysis summaries even when they mention a valid path
- adaptive route thoroughness for prompts that explicitly complain about early stopping or ask for longer / frontier-grade search
- retry prompts that define readiness instead of letting the model decide from vibes
- tests for low-confidence readiness rejection, readiness parsing/evaluation, suite readiness coverage, and no-early-stop thoroughness escalation

Implementation status:

- complete
- implemented:
  - `readiness_check` debug-log messages for explore, review, and architecture summaries
  - readiness snapshot parsing in `internal/eval`
  - `require_readiness` scenario assertions
  - `required_readiness_actions` suite coverage
  - low-confidence grounded summaries now fail the analysis readiness gate and trigger retry/fallback
  - prompts containing signals like longer search, not confident, do not stop, frontier-grade, whole codebase, and comprehensive now escalate to thorough search
  - representative suite includes readiness coverage on an architecture task

### Phase 33: Multi-Pass Deepening Loop

Readiness gates prevent bad finalization, but they do not yet guarantee additional search after a low-confidence model response. Frontier-grade search should deepen deterministically when the readiness gate fails and useful search budget remains.

Deliverables:

- lane-specific deepening planner that selects the next search/read action from missing evidence
- explicit search budget ledger by lane and thoroughness
- automatic retry with additional deterministic evidence before asking the model again
- stop conditions based on marginal evidence gain, not elapsed patience
- debug-log records for deepening attempts, skipped deepening, and exhausted budget
- evaluator assertions for required deepening attempts and exhausted-budget honesty
- representative prompts for "not confident, keep searching" and "confidence still low after budget exhausted"

Implementation status:

- complete
- implemented:
  - `completeAnalysisSummary` now accepts a lane-specific deterministic deepener
  - review readiness failures trigger up to two bounded deeper passes with wider reads and targeted searches over candidate paths before fallback
  - architecture readiness failures trigger up to two bounded deeper passes with wider hotspot reads and related-file lookup before fallback
  - `deepening_attempt` debug-log messages record lane, attempted/exhausted state, action, reason, and evidence paths
  - evaluator support for `require_deepening`
  - suite coverage support for `required_deepening_actions`
  - representative suite task for readiness-driven review deepening
  - CLI output and result buckets for deepening diagnostics

### Phase 34: Answer Critic And Contract-Level Self-Check

A frontier harness should run a deterministic critic over final answers before presenting them. This should be separate from model self-reflection: the model can propose an answer, but the harness decides whether the answer satisfies the contract.

Deliverables:

- final-answer critic per answer contract
- explicit checks for unsupported claims, missing changed-file summary, missing verification summary, and overconfident language after failed verification
- critic-triggered repair loop for implementation summaries
- debug-log records for critic pass/fail and repair attempts
- evaluator assertions for final-answer critic outcomes

Implementation status:

- complete
- implemented:
  - deterministic implementation-summary critic with contract, pass/fail, action, repair state, and findings
  - final answer checks for changed-file sections, exact changed paths, verification sections, verification commands, verification risk, and overconfident passed-verification claims after failed checks
  - critic-triggered deterministic repair that rebuilds unsafe implementation summaries from changed paths, verification results, and refactor-stage state
  - `final_answer_critic` debug-log messages
  - evaluator support for `require_final_answer_critic`
  - suite coverage support for `required_final_critic_actions`
  - CLI reporting and result buckets for final critic actions
  - representative suite coverage for accepted implementation final answers
  - unit tests for critic rejection and repair of unsafe model prose

### Phase 35: Budget, Effort, And Uncertainty Policy

Frontier agents do not search forever. They stop when the marginal value of more work is lower than the cost or when the user-imposed budget is exhausted. The harness needs an explicit policy layer instead of hard-coded scattered limits.

Deliverables:

- unified effort policy for quick, medium, thorough, and exhaustive work
- token/tool/time budget accounting in debug logs and eval metrics
- confidence calibration rules tied to evidence count, verification result, and unresolved uncertainty
- user-visible statement when budget, not confidence, caused stopping
- tests for budget exhaustion, budget-respecting short tasks, and thorough tasks that use deeper budgets

Implementation status:

- complete
- implemented:
  - explicit `effort_policy` debug-log messages for every prompt plan
  - unified policy snapshot covering lane, effort level, configured tool budget, analysis budget, implementation budget, deepening pass budget, request time budget, output-token budget, reasoning budget, confidence floor, and budget-exhaustion behavior
  - evaluator support for `require_effort_policy`
  - suite coverage support for `required_effort_levels`
  - CLI reporting and result buckets for effort levels
  - representative suite coverage for medium and thorough effort policies
  - tests for effort-policy derivation, parsing, matching, and suggested-scenario generation

### Phase 36: Long-Horizon Task Memory And Resumption

Some SWE tasks require multiple coherent passes. The harness has initial refactor-stage persistence, but the implementation plan should explicitly cover long-horizon memory for investigations, partial verification, and unresolved evidence gaps.

Deliverables:

- persisted task state for open evidence gaps, attempted searches, chosen targets, verification history, and next actions
- safe resume prompts that do not redo completed broad search
- stale-state invalidation after edits or external file changes
- evaluator scenarios for interrupted investigation, resumed repair, and resumed refactor

Implementation status:

- complete for architecture-derived long-horizon refactors and general persisted investigation/repair memory
- implemented:
  - durable workspace-scoped structural-refactor stage state with changed files, remaining targets, strategy, verification status, and next action
  - safe resume prompts that preserve completed stage context and target the next bounded file instead of repeating broad architecture discovery
  - stale/non-resumable stage cleanup after failed or completed refactor stages
  - structured `long_horizon_state` debug logs for `persist`, `resume`, and `clear` lifecycle actions
  - `idiom-eval` metrics and scenario assertions for long-horizon state action, source lane, stage, focus path, remaining targets, next-step content, and resumability
  - SWE suite coverage gates and result buckets for `persist`, `resume`, and `clear` long-horizon actions
  - durable workspace-scoped `task-memory.json` for general investigations and partial repairs
  - persisted open evidence gaps, attempted searches, chosen targets, verification history, and next actions
  - safe `/resume task` handling and natural-language resume detection that resumes from saved evidence instead of repeating broad search
  - stale-state invalidation when saved target files change outside the persisted task state
  - failed-verification repair memory after bounded implementations that cannot be repaired in the current turn
  - evaluator fields for memory kind, open gaps, attempted searches, chosen targets, verification history, and next actions
  - SWE suite coverage gates for `investigation` and `partial_repair` memory kinds

### Phase 37: Adversarial And Negative-Evidence Corpus

The current suite covers prompt injection and tool-error recovery, but frontier parity needs negative-evidence tasks where the right behavior is to say "not found" after enough search, without hallucinating a target.

Deliverables:

- representative no-match locate tasks
- no-bug-found review tasks
- false-positive architecture and performance prompts
- assertions that final answers include searched locations and honest uncertainty
- oracle checks that no files changed when evidence did not justify a change

Implementation status:

- complete for suite/harness coverage
- implemented:
  - representative no-match locate task for a nonexistent UI component
  - representative no-bounded-finding review task for a named nonexistent performance regression
  - representative false-positive architecture task for a nonexistent package-boundary target
  - static lint requiring `negative-evidence` tasks to forbid changed files through the oracle
  - coverage contract fields for required negative-evidence outcomes
  - derived `negative_evidence` task-shape coverage
  - captured-log result buckets for negative-evidence failures
  - documentation for negative-evidence coverage and lint expectations

### Phase 38: Budget Ledger And Stop Reasons

Frontier behavior needs actual budget accounting, not just declared effort. Every analysis run should expose model calls, deepening passes, tool results, evidence gain, stop reason, and whether the stop was confidence-driven or budget-driven.

Implementation status:

- complete
- implemented:
  - `budget_ledger` debug-log records for analysis lanes
  - used model-call, deepening-pass, tool-result, new-evidence, total-evidence, stop-reason, and budget-exhaustion fields
  - evaluator assertions through `require_budget_ledger`
  - representative suite coverage through `budget_ledger` scenario assertions

### Phase 39: Marginal Evidence Delta Controller

Deepening should continue only while it adds useful evidence. The harness now records evidence deltas from deterministic deepening passes so stop policy can distinguish high-value, low-value, and blocked searches.

Implementation status:

- complete
- implemented:
  - `evidence_delta` debug-log records for each deterministic deepening pass
  - new-path, total-path, tool-result, successful-tool, failed-tool, and marginal-value fields
  - evaluator assertions through `require_evidence_delta`
  - representative suite coverage through `evidence_delta` scenario assertions

### Phase 40: Claim-Level Confidence Calibration

Readiness confidence must be tied to evidence and unresolved uncertainty, not model tone. The harness now emits claim-confidence snapshots from readiness decisions.

Implementation status:

- complete
- implemented:
  - `claim_confidence` debug-log records derived from readiness gates
  - confidence, supported-claim count, evidence paths, unresolved uncertainty, contradictions, and calibration basis fields
  - evaluator assertions through `require_claim_confidence`
  - representative suite coverage through `claim_confidence` scenario assertions

### Phase 41: Adaptive Session Context And Pinned Evidence

Long sessions need compaction that preserves task state. Raw history truncation is not enough.

Implementation status:

- complete
- implemented:
  - `context_compaction` debug-log records when `MaxHistory` truncates chat history
  - retained/dropped message counts, pinned paths, pinned memory kind, and compaction strategy fields
  - pinned paths sourced from execution summaries, task memory, and resumable refactor targets
  - evaluator assertions through `require_context_compaction`
  - representative suite coverage through `context_compaction` scenario assertions

### Phase 42: Contract Critics Beyond Implementation

Final-answer criticism now covers analysis lanes, not only implementation summaries.

Implementation status:

- complete
- implemented:
  - analysis final-answer critic for locate, review, architecture, and explore summaries
  - contract-aware checks for empty answers, missing evidence paths, locate no-match honesty, review bounded-finding framing, and architecture structural framing
  - shared `final_answer_critic` observability for accepted and warned analysis answers

### Phase 43: Route Ambiguity And Tie-Break Auditing

Ambiguous prompts should not silently fall through route precedence.

Implementation status:

- complete
- implemented:
  - `route_ambiguity` debug-log records for every prompt plan
  - selected lane, route reason, detected route signals, ambiguity flag, and host tie-break explanation fields
  - evaluator assertions through `require_route_ambiguity`
  - representative suite coverage for an architecture-plus-implementation ambiguous prompt

### Phase 44: Captured-Log Regression Mode

Definition-only suite validation is not enough. The existing `idiom-swe-eval` captured-log mode remains the canonical live regression path: missing logs fail, load errors fail, and result buckets identify which behavioral surface regressed.

Implementation status:

- complete
- implemented:
  - captured-log evaluation through `idiom-swe-eval -logs`
  - per-task load-error and missing-log failures
  - result buckets for route, readiness, deepening, critic, effort, long-horizon, negative-evidence, tool, and file-surface dimensions

### Phase 45: Verification Feedback Memory

Verification plans should produce feedback for future escalation decisions.

Implementation status:

- complete
- implemented:
  - `verification_feedback` debug-log records after implementation verification
  - risk, coverage, confidence, commands, failed commands, and learned recommendation fields
  - evaluator assertions through `require_verification_feedback`
  - representative suite coverage for syntax-only verification feedback

### Phase 46: Tool-Failure-Aware Confidence

Tool failures should degrade confidence unless alternate evidence covers the same gap.

Implementation status:

- complete
- implemented:
  - `tool_failure_confidence` debug-log records for failed deterministic deepening tools
  - lane, tool, action class, confidence impact, and reason fields
  - evaluator assertions through `require_tool_failure_confidence`
  - representative suite coverage for tool-error recovery confidence degradation

### Phase 47: Adaptive Effort Integration

The effort policy, budget ledger, evidence delta, readiness gate, and claim-confidence snapshot now form one auditable stop-control chain.

Implementation status:

- complete
- implemented:
  - effort declaration through `effort_policy`
  - actual budget use through `budget_ledger`
  - marginal gain through `evidence_delta`
  - answer readiness through `readiness_check`
  - calibrated answer confidence through `claim_confidence`
  - user-visible uncertainty policy in budget-ledger stop records

### Phase 48: Deterministic Control Rails

The frontier behavior gap is no longer just route quality. The harness needs an explicit deterministic scaffold that makes unsupported claims, unsafe writes, weak stop conditions, and missing verification visible and testable.

Implementation status:

- complete
- implemented:
  - dedicated follow-up tracker in `docs/deterministic-control-rails-tracker.md`
  - `control_rails` debug-log records for every prompt plan
  - lane, follow-up lane, answer contract, task family, permission profile, required evidence, required conceptual tools, forbidden tool classes, readiness gates, search policy, verification contract, stop conditions, final-critic checks, and failure-to-eval capture policy
  - log/failure-inspection prompts route to read-only exploration instead of direct conversation
  - verification-only prompts receive a read-only shell-verification rail profile instead of generic main-lane shell denial
  - forbidden tool classes are enforced through control-rail pre-execution checks
  - final answers emit `control_rails_enforcement` records and are deterministically repaired when evidence, changed-file/no-op, or verification contracts are missing
  - evaluator assertions through `require_control_rails`
  - suggested scenario generation from observed control rails
  - CLI metric display for the latest control-rails snapshot
  - SWE suite scenario assertion coverage for `control_rails`
  - regression tests for debug-log rails, implementation rails, eval parsing/matching, suggested scenarios, and CLI output

### Testing suite

The routing/testing baseline should cover:

- prompt classification into `main`, `locate`, `explore`, `review`, `architecture`, and `implement`
- representative SWE prompt taxonomy across common task families
- executable SWE task corpus scoring against captured logs with task-specific oracles
- suite coverage contracts for required SWE families and risk/capability tags
- behavioral coverage contracts for lanes, transitions, answer contracts, verification states, and oracle assertion types
- fixture and task-shape coverage contracts
- route intent coverage for route reasons and thoroughness
- scenario assertion feature coverage
- tool and artifact surface coverage
- advanced scenario assertion coverage for final answer constraints, replans, broad-tool budgets, and language capabilities
- readiness gates, readiness actions, confidence calibration, and no-early-stop thoroughness escalation
- multi-pass deepening after failed readiness when budget remains
- final-answer critic outcomes and critic-triggered repair loops
- unified budget/effort policy metrics
- actual budget ledger stop reasons
- marginal evidence deltas
- claim-confidence calibration
- route ambiguity tie-break records
- context compaction with pinned evidence
- verification feedback records
- tool-failure confidence degradation
- deterministic control-rails contracts and task-family preconditions
- long-horizon task memory and resumable investigations
- negative-evidence tasks where the correct answer is "not found" or "not enough evidence"
- captured-log result summaries by coverage dimension
- static suite linting for weak or unsafe task oracles
- suite-definition-only validation without captured logs
- route reasons for each classified prompt family
- answer contracts for each classified prompt family
- route-and-contract eval scenarios from debug logs
- explicit follow-up transition rules after analysis lanes
- execution panel metadata for lane, follow-up lane, route reason, and answer contract
- deterministic guard redirects when an implementation target packet is ignored
- deterministic fallback summaries for explore and architecture
- evidence-ledger provenance in fallback summaries
- safe replan recommendations from cross-contract evidence
- eval assertions for expected replan recommendations
- language capability registry detection and profile injection
- eval assertions for expected language capability registries
- verification planner risk metadata and repair policy rendering
- eval assertions for expected verification plan risk and coverage
- final implementation summaries include verification risk and repair policy
- write approval proposals include verification risk and repair policy
- eval budgets for analysis summary rejections
- locate summary contract validation before implementation handoff
- resumable architecture-derived refactor stage persistence and safe resume handling
- full package regression coverage via `go test ./...`

Additional frontier-grade tests:

- architecture prompt must not route to `review`
- architecture summary with grounded hotspots must be accepted
- review summary with only hotspot rankings must be rejected
- architecture fallback synthesis must produce useful output when the model emits empty or malformed final text
- mixed “analyze structure then apply” prompt must route to `architecture -> implement`
- prompts using “bugs”, “regressions”, or “vulnerabilities” must still prefer `review`
