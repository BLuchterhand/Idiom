## Executable SWE Eval Suite

`qwen-swe-eval` scores a corpus of captured `qwen-bubbles` debug logs against task-specific SWE oracles.

This is the executable layer above the route-only representative prompt suite:

- `docs/representative-swe-prompt-suite.md` defines the routing taxonomy.
- `testdata/swe/representative-suite.json` defines task-level oracles.
- `cmd/qwen-swe-eval` scores one log per task.

### Run

```bash
go run ./cmd/qwen-swe-eval \
  -suite testdata/swe/representative-suite.json \
  -logs /path/to/task-logs
```

The log directory must contain one JSONL log per task:

```text
/path/to/task-logs/direct-debuglog-question.jsonl
/path/to/task-logs/verification-only-go-test.jsonl
/path/to/task-logs/locate-banner-rendering.jsonl
```

JSON output is available with:

```bash
go run ./cmd/qwen-swe-eval \
  -suite testdata/swe/representative-suite.json \
  -logs /path/to/task-logs \
  -json
```

Validate only the suite definition, coverage contract, and lint rules with:

```bash
go run ./cmd/qwen-swe-eval \
  -suite testdata/swe/representative-suite.json \
  -suite-only
```

The same gate is available through `make swe-suite`.

### Task Schema

Each task has:

- `id`: stable log filename stem and report key.
- `family`: SWE task family, such as `review_then_implement`.
- `prompt`: representative user request.
- `turns`: optional multi-turn conversation prompts.
- `tags`: searchable labels for filtering and coverage review.
- `fixture`: optional fixture workspace path for future live runners.
- `scenario`: standard `qwen-eval` scenario assertions.
- `oracle`: additional SWE-specific quality gates.

The suite can also define a `coverage` contract:

- `min_tasks`: minimum number of tasks in the suite.
- `required_families`: task families that must be represented.
- `required_tags`: capability or risk tags that must be represented.
- `required_fixtures`: fixture workspaces that must be represented.
- `required_task_shapes`: derived task structures that must be represented, such as `fixture_backed`, `multi_turn`, `protected_approval`, `expected_failure`, and `analysis_then_implementation`.
- `required_initial_lanes`: starting route lanes that must be represented.
- `required_followup_lanes`: planned transition lanes that must be represented.
- `required_route_reasons`: route intent labels that must be represented.
- `required_contracts`: answer contracts that must be represented.
- `required_thoroughness`: explicit thoroughness levels that must be represented.
- `required_verification`: verification outcomes such as `none`, `passed`, and `failed`.
- `required_scenario_assertions`: built-in scenario assertion types that must be exercised.
- New frontier-control scenario assertions include `budget_ledger`, `evidence_delta`, `claim_confidence`, `context_compaction`, `route_ambiguity`, `verification_feedback`, `tool_failure_confidence`, and `control_rails`.
- `required_oracle_assertions`: oracle assertion capabilities that must be exercised.
- `required_readiness_actions`: readiness outcomes that must be represented, such as `answer`, `retry`, and `fallback`.
- `required_deepening_actions`: deterministic deepening outcomes that must be represented, such as `retry_with_evidence`.
- `required_final_critic_actions`: final-answer critic outcomes that must be represented, such as `accept` and `repair`.
- `required_effort_levels`: effort-policy levels that must be represented, such as `medium` and `thorough`.
- `required_long_horizon_actions`: persisted task-memory outcomes that must be represented, such as `persist`, `resume`, and `clear`.
- `required_long_horizon_kinds`: persisted memory kinds that must be represented, such as `investigation` and `partial_repair`.
- `required_negative_evidence`: negative-evidence outcomes that must be represented, such as `no_match`, `no_bounded_finding`, and `false_positive`.
- `required_tools`: concrete required tools that must be represented.
- `required_forbidden_tools`: concrete forbidden tools that must be represented.
- `required_file_surfaces`: changed-file surfaces that must be represented, such as `go`, `docs`, `config`, `script`, `test`, and `prompt`.
- `required_evidence_surfaces`: evidence-path surfaces that must be represented.
- `min_tasks_per_family`: per-family minimum counts for areas that need multiple cases.

Coverage failures make the suite fail even before individual log outcomes are considered. This prevents a reduced corpus from silently looking healthy.

### Suite Linting

`qwen-swe-eval` also runs static lint checks against the suite definition:

- fixture paths must be relative and stay under the suite directory.
- suite-only validation checks that referenced fixture directories exist.
- `read-only` tasks must require no changed files.
- security or adversarial tasks must forbid writes or require no changed files.
- direct implementation tasks must require changed files or explicit approval.
- multi-turn tasks must require enough user turns.
- failure-repair tasks must assert a verification outcome.
- tool-error tasks must bound errors.
- negative-evidence tasks must require no changed files.

Lint failures indicate a weak or unsafe oracle, not an agent failure. Fix the suite definition before treating task scores as meaningful.

### Result Summaries

Captured-log runs include aggregate result buckets:

- overall passed, failed, missing-log, and load-error counts
- pass/fail buckets by task family
- pass/fail buckets by initial lane
- pass/fail buckets by route reason
- pass/fail buckets by answer contract
- pass/fail buckets by verification expectation
- pass/fail buckets by scenario assertion type
- pass/fail buckets by oracle assertion type
- readiness coverage counts for scenarios that assert `require_readiness`
- pass/fail buckets by readiness, deepening, final-answer critic action, effort level, long-horizon action, and negative-evidence outcome
- pass/fail buckets by required tool
- pass/fail buckets by changed-file surface

The human-readable CLI prints the most important failure buckets; JSON output includes the full `summary` object for dashboards and CI annotations.

### Oracle Coverage

The current oracle layer can assert:

- no changed files for read-only tasks
- required and forbidden changed files
- max tool requests
- max timestamped duration
- max errors
- minimum user and assistant turns
- no approval requirement
- no shell usage
- no summary rejections
- required summary rejection lanes

The standard scenario layer already covers:

- prompt matchers
- final answer required/forbidden text
- required/forbidden tools
- phases
- route plan
- replan recommendation
- broad tool request budget
- repeated dead-end budget
- summary rejection budget
- verification state
- verification plan
- changed files
- evidence paths
- language capabilities
- approval requirement

### Representative Families

The default suite covers:

- direct questions
- verification-only requests
- locate
- locate-then-implement
- explore-then-implement
- investigation-then-implement
- review-then-implement
- security review
- performance review
- architecture
- architecture-then-implement
- direct implementation
- test authoring
- config edits
- script edits
- docs edits
- protected prompt edits
- failure repair
- tool-error recovery
- multi-turn undo/rollback

### Current Limitation

This scorer still evaluates logs through deterministic oracles, and it can now generate those logs first with `qwen-swe-eval -run-live -runner-command ...`. The live runner command is intentionally externalized so local model/TUI launch details stay configurable while the suite schema and oracle layer remain deterministic.
