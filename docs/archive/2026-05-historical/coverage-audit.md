## Coverage Audit

This document records the coverage review prompted by the 2026-04-18 runtime failure:

```text
Chat failed: unsupported explore thoroughness ""
```

### Why That Escaped

The route tests asserted lane, follow-up lane, reason, and answer contract for representative prompts. They only asserted thoroughness when a test case explicitly set an expected value.

Architecture and some review prompts route into subagents but did not set `thoroughness` in the test cases, so the route plan could carry an empty thoroughness value without failing tests. There was also no route-to-subagent setup test proving every analysis lane enters subagent launch with a valid thoroughness.

The fix adds both:

- route classification now defaults `explore`, `review`, and `architecture` lanes to the workspace thoroughness, normally `medium`
- `runPromptPlan` normalizes analysis-lane thoroughness defensively before launching subagents
- regression tests require representative architecture/review prompts to carry medium thoroughness
- a direct normalization test covers all analysis lanes

### Coverage Command

Use:

```bash
make coverage
```

For function-level detail:

```bash
make coverage-report
```

### Current Package Coverage

Measured with `go test ./... -cover` after the added tests:

| Package | Coverage | Risk Notes |
| --- | ---: | --- |
| `internal/evidence/eval` | 83.6% | Good coverage for replay metrics, SWE suite contracts, oracle lint, and suite-only validation. |
| `internal/runtime/promptstack` | 82.6% | Good coverage for prompt composition, dedupe, missing required files, and profile listing. |
| `internal/repository/semantic` | 76.5% | Go and C/C++ semantic index/query paths now both have representative parser/query coverage. |
| `internal/platform/config` | 64.9% | Covers defaulting, path resolution, validation, and preset cloning. |
| `internal/evidence/transcript` | 54.3% | Covers session/message/phase logging and redaction; some artifact/server/context logging remains lower risk. |
| `internal/surfaces/tui/app` | 43.3% | Large runtime package. Core routing, guards, verification planning, approval rendering, summaries, and deterministic fallback paths are covered; full TUI/runtime loops remain integration-heavy. |
| `internal/runtime/agent` | 33.0% | Now covers dead-end action keys, shell classes, fallback synthesis, and output compaction. Full runner loop still needs an interface seam for deterministic fake clients. |
| `internal/capabilities/workspace` | 33.1% | Core parser/safety helpers, discovered-path read guard, basic read/write execution, explore write rejection, path traversal rejection, and C++ helpers are covered; shell execution and Go workspace tool paths need more fixture-backed tests. |
| `internal/runtime/llama` | 25.8% | Covers chat request payloads, response parsing, HTTP errors, and retry classification. Server process lifecycle remains integration-heavy. |
| `cmd/*` | 0.0% | Thin entrypoints; helper extraction would make CLI output paths easier to test. |

### Likely Bug Hiding Places

- `internal/surfaces/tui/app.runPromptPlan` and subagent launch paths: route plans cross into workspace mode, prompt construction, debug logging, and model calls. Bugs here look like the latest failure.
- `internal/runtime/agent.Run`: the loop is still coupled to concrete `llama.Client`, making full dead-end and retry behavior hard to test without refactoring to an interface.
- `internal/capabilities/workspace.Execute`: path resolution, discovered-handle enforcement, write behavior, shell validation, and Go tool dispatch need more fixture-backed tests.
- `internal/runtime/llama.StreamChat`: streaming parser and error handling are lower covered than non-streaming chat.
- `cmd/qwen-*`: current entrypoints are not structured for unit tests; extract CLI runners if command behavior starts carrying more logic.

### Added Tests

This audit added coverage for:

- config defaulting, path resolution, validation, and preset pointer cloning
- prompt stack composition, optional project prompts, dedupe, missing required prompts, and sorted profile listing
- workspace tool request parsing, malformed requests, shell classification, explore shell validation, and broad-inspection lock clearing
- workspace execution for write/read flows, discovered-path read guard, explore-mode write rejection, path traversal rejection, and unsupported tools
- agent repeated-action keying, fallback answer synthesis, and output compaction
- llama non-streaming chat request payloads, response parsing, HTTP/empty-choice errors, and loading-model retry classification
- Go semantic indexing, package matching, symbol lookup, call hierarchy, default-file ranking, and relevant-file ranking

### Remaining Coverage Work

The next highest-value coverage improvements are:

- introduce a small `chatCompleter` interface so `internal/runtime/agent.Run` can be tested end-to-end without a real HTTP client
- add fixture-backed tests for `workspace.Execute` covering read handles, path suggestions, guarded writes, shell safety, and Go tool dispatch
- extract CLI command bodies into testable `run(args, stdout, stderr)` functions
- add `httptest` coverage for `StreamChat`, including chunk errors and `[DONE]`
