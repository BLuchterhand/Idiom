## Representative SWE Prompt Suite

This suite defines the baseline task taxonomy that `idiom` should route correctly for general software engineering work.

The executable source of truth is `TestRepresentativeSWEPromptSuiteRoutesTypicalTasks` in `internal/app/swe_prompt_suite_test.go`.

### Covered Task Families

- Direct technical questions: answer in the main lane without forcing analysis.
- Verification-only requests: stay in the main lane so the agent can run/report checks without implying an edit.
- Locate-only requests: route to `locate` with the `locate_targets` contract.
- Locate-then-edit requests: route to `locate -> implement`.
- Explore-only and investigate-then-update requests: route to `explore`, with `implement` follow-up when explicitly requested.
- Review/audit tasks: route to `review` with bounded findings.
- Review-then-fix tasks: route to `review -> implement` only when the prompt asks to apply a bounded issue.
- Architecture and language-capability questions: route to `architecture`.
- Architecture-then-implement tasks: route to `architecture -> implement`.
- Direct implementation tasks: route to `implement` for code, tests, config, scripts, docs, prompt files, and shell-only mutations.

### Current Representative Prompts

- `What does the debug log redacted mode do?`
- `Run the Go test suite and report any failures.`
- `Find where the "Qwen is working" banner is rendered.`
- `Find where the "Qwen is working" banner is rendered and remove it.`
- `Explore the routing flow in this codebase and then update docs/frontier-routing-implementation-plan.md with what you find.`
- `Do a thorough investigation of the guarded write flow in this codebase, then update the implementation if a gap is confirmed.`
- `Review this codebase for regressions and apply the fix if you find a bounded issue.`
- `Audit this repo for security vulnerabilities.`
- `Review this codebase for performance regressions.`
- `Identify one code smell in this codebase.`
- `Analyze package boundaries and ownership in this codebase.`
- `Which languages does this harness support, and how extensible is that architecture?`
- `Analyze this codebase's structure, identify the best package boundary split, and then implement it.`
- `Update the code in internal/app/app.go to improve the execution panel label.`
- `Add regression tests in internal/app/write_guard_test.go for route plan rendering.`
- `Update config.example.json so max_tool_steps is 16.`
- `Edit scripts/run-harness.sh to print the selected profile before launching.`
- `Update README.md with a short troubleshooting note for debug logs.`
- `Edit prompts/base.md to clarify the answer style guidance.`
- `Create a new file scratch-note.txt containing hello by using shell commands only.`

### Quality Bar

Every prompt in the suite asserts:

- initial lane
- legal follow-up lane
- route reason
- answer contract
- thoroughness when relevant

The suite is intended to grow whenever a new common SWE task type is discovered or a routing failure appears in logs.
