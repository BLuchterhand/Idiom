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
- External agents (opencode, Codex, Claude, etc.) can invoke idiom's workspace tools directly via the CLI without starting an inference session. Use `idiom tool <tool-name> --input '<json>'` or pipe JSON on stdin. Run `idiom tool help` for usage and `idiom tool list` for all available tools.
- Keep edits targeted. Preserve user changes and do not widen scope without cause.
- For non-trivial write work, keep a short explicit plan/checklist so the intended edits and verification stay visible.
- Treat prompt files, runner policy, control rails, and eval fixtures as one system. Changes in one layer often require updates or tests in another.
- If the available evidence is thin, say so and gather more. Do not smooth over uncertainty with confident prose.

### Discovery Tool CLI Reference

These tools are available both inside idiom agent sessions and via `idiom tool` from an external agent's shell:

| Tool | When to use | Example invocation |
|------|-------------|--------------------|
| `discovery_query` | Broad impact, tracing, architecture, security, or history analysis. Orchestrates multiple sub-tools in one call. | `idiom tool discovery_query --input '{"purpose":"impact_analysis","path":".","pattern":"funcName","language":"go"}'` |
| `structural_search` | AST-aware code pattern search (requires `ast-grep`). | `idiom tool structural_search --input '{"pattern":"fn $NAME($$$ARGS) { $$$BODY }","path":"src/","language":"rust"}'` |
| `dependency_graph` | Understand module/package dependency structure for Go, Rust, TypeScript, Python, or Java. | `idiom tool dependency_graph --input '{"path":".","language":"go"}'` |
| `history_range` | Git log for a specific line range in a file. | `idiom tool history_range --input '{"path":"main.go","start_line":10,"end_line":50}'` |
| `history_symbol` | Git log following a specific symbol across renames. | `idiom tool history_symbol --input '{"path":"manager.go","pattern":"Execute"}'` |
| `blame_range` | Git blame for a specific line range. | `idiom tool blame_range --input '{"path":"main.go","start_line":1,"end_line":80}'` |
| `ctags_symbols` | Symbol indexing across the codebase (requires `ctags`). | `idiom tool ctags_symbols --input '{"path":".","pattern":"Handler"}'` |
| `semgrep_scan` | Security and pattern scanning (requires `semgrep`). | `idiom tool semgrep_scan --input '{"path":".","pattern":"auto"}'` |
| `joern_scan` | Code property graph analysis (requires `joern-scan`). | `idiom tool joern_scan --input '{"path":"."}'` |

When invoked via CLI, the tool result is printed as JSON with `Tool`, `Summary`, `Output`, and `Error` fields. Exit code is 0 on success, 1 if the tool reported an error.

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
