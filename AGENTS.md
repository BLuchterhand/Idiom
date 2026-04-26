# qwen-bubbles AGENTS

This repository builds `idiom`, a local coding-agent harness around Qwen, `llama.cpp`, routing/control rails, workspace tools, and eval infrastructure.

## Start Here

- Read [README.md](README.md), [docs/decisioning-north-star.md](docs/decisioning-north-star.md), and [.codex/project-context.md](.codex/project-context.md) before changing agent behavior, prompt layers, routing, control rails, readiness gates, or verification logic.
- Always use the OpenAI developer documentation MCP server for OpenAI API, Codex, MCP, or ChatGPT-app questions without waiting for the user to ask explicitly.
- Keep terminology precise: the model owns strategy; the harness owns contracts.

## North-Star Rules

- Prefer nudges, redirects, and bounded recovery over hard-picking the model's next step.
- Force only real invariants: permissions, protected paths, protocol correctness, verification, completion/readiness contracts, repeated-loop breaks, and exhausted budgets.
- After the evidence is sufficient, answer or act. Continue only if you can name one exact missing claim and one exact probe that would resolve it.
- Keep prompts, recovery text, and answer-shaping generic and evidence-based. Do not hardcode one recurring scenario or topic template into reusable instructions.
- Evidence tools and summaries should report findings, paths, symbols, counts, and uncertainty. They should not prescribe the next tool call.
- All work and answers must be grounded in factual analysis from the codebase, tool output, tests, logs, or cited docs. Distinguish verified facts from inference, and do not present guesses as findings.

## Working Style In This Repo

- Do not guess paths. Discover them first.
- Prefer this repo's discovery surface before generic file search or ad hoc reads. Start with the most specific applicable discovery/semantic tool, then fall back to standard search/read tools only when the discovery path does not answer the question.
- The preferred order is: language-aware symbol/navigation tools, `discovery_query`, other structured discovery tools such as `structural_search`, `dependency_graph`, `history_*`, `blame_range`, `ctags_symbols`, `semgrep_scan`, or `joern_scan`, and only then generic `search_content` / `read_file` style inspection.
- Keep edits targeted. Preserve user changes and do not widen scope without cause.
- For non-trivial write work, keep a short explicit plan/checklist so the intended edits and verification stay visible.
- Treat prompt files, runner policy, control rails, and eval fixtures as one system. Changes in one layer often require updates or tests in another.
- If the available evidence is thin, say so and gather more. Do not smooth over uncertainty with confident prose.

## Verification

- Default verification for code changes: `go test ./...`
- When a change is localized, run the narrowest relevant package tests first, then the full suite if the change affects shared runtime behavior, prompts/control rails, routing, or workspace/tooling contracts.
- If entrypoints or build wiring change, also run `make build` or `go build ./cmd/idiom`.
- If a change touches eval, routing, or discovery behavior, check the most relevant focused tests in addition to the full suite.

## Useful Commands

- `make run`
- `make build`
- `make test`
- `make coverage`
- `make eval LOG=logs/conversations/<session>.jsonl`
- `make swe-live-subset`

## Key Areas

- `cmd/idiom`: CLI entrypoint
- `internal/app`: TUI/session lifecycle
- `internal/agent`: runner, control rails, readiness, final answer shaping
- `internal/workspace`: tool surface and language-aware workspace operations
- `internal/frontier`: discovery/runtime helpers
- `prompts/`: baseline and mode/profile instructions
- `docs/`: design intent, north star, remediation, and parity plans

## Project Docs

- [.codex/project-context.md](.codex/project-context.md): Codex-focused project brief
- [docs/decisioning-north-star.md](docs/decisioning-north-star.md): canonical strategy-vs-contract policy
- [docs/agent-autonomy-tracker.md](docs/agent-autonomy-tracker.md): completion, task-ledger, and workflow-state expectations
- [docs/north-star-remediation.md](docs/north-star-remediation.md): examples of repo language that should be generalized or softened
