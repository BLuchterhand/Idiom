# Project Context

## What This Repo Is

`idiom` is a local coding-agent harness for Qwen models running through `llama.cpp` or MLX. The repo combines:

- a terminal UI and session lifecycle
- routing and control-rail logic
- workspace and semantic tooling
- eval/replay infrastructure
- prompt layers that steer the local model

The main goal is frontier-style autonomy with explicit, observable host contracts.

## North Star

This repo's canonical policy is [docs/decisioning-north-star.md](../docs/decisioning-north-star.md).

The durable rules to preserve are:

- Model owns strategy.
- Harness owns contracts.
- Prefer nudges over forced strategy when a safe, contract-compliant path is still open.
- Force only invariants: permissions, safety boundaries, protocol correctness, verification, completion/readiness, loop breaks, and post-budget cutoffs.
- After readiness, either answer now or take one exact probe for one exact missing claim.
- Reusable instructions should stay generic and evidence-shaped, not scenario-shaped.

If a proposed prompt, policy, or recovery string feels like it is choosing the model's next move rather than protecting an invariant, it is probably misaligned.

## Repo-Specific Engineering Expectations

- Do not guess file paths.
- Prefer this repo's discovery tooling before generic search. Use the most specific applicable tool first: language-aware symbol/navigation tools, `discovery_query`, then other structured discovery tools such as `structural_search`, `dependency_graph`, `history_range`, `history_symbol`, `blame_range`, `ctags_symbols`, `semgrep_scan`, or `joern_scan`. Fall back to raw text search or broad file reads only when the discovery path does not land.
- Treat zero-result searches as evidence, not as permission to invent a new path.
- Keep tool and prompt guidance generic; do not encode topic-specific recipes unless they are truly format- or contract-driven.
- Treat summaries and evidence packets as observations only. Strategy guidance belongs in prompts/control rails, not inside tool-result payloads.
- When touching prompts, runner policies, final-answer shaping, or discovery behavior, check nearby tests and docs for parity.
- Ground all conclusions in factual analysis. Answers should be traceable to code, tests, logs, discovery output, or cited documentation, and should clearly separate verified behavior from inference or open uncertainty.

## Verification Expectations

Use the narrowest meaningful verification first, then broaden when the blast radius justifies it.

Common commands:

```bash
make build
make test
go test ./...
go test ./internal/agent/...
go test ./internal/workspace/...
go test ./internal/app/...
```

Use the full `go test ./...` suite by default for harness changes. Targeted package tests are appropriate as the first pass while iterating.

For replay/eval work:

```bash
make eval LOG=logs/conversations/<session>.jsonl
make swe-live-subset
```

## High-Value Files And Directories

- `README.md`: runtime, modes, tooling, and workflow overview
- `Makefile`: canonical run/build/test/eval commands
- `prompts/base.md`: baseline model behavior rules
- `prompts/profiles/coding.md`: code-session guidance
- `prompts/plan-mode.md`: read-only planning guidance
- `internal/agent/`: runner, permissions, readiness, ledger/workflow handling
- `internal/workspace/`: discovery and semantic tool implementations
- `internal/app/`: TUI lifecycle and plan/history UI
- `docs/decisioning-north-star.md`: strategy/contract split
- `docs/agent-autonomy-tracker.md`: completion and workflow-state design
- `docs/north-star-remediation.md`: examples of instructions that should be softened or generalized

## Change Patterns

When working in this repo, expect cross-cutting changes:

- Prompt changes often need runner/test updates.
- Runner/control-rail changes often need doc updates.
- Tooling changes often need both semantic-tool tests and higher-level agent behavior tests.
- Changes that affect completion, recovery, or routing should be checked against the north-star language before they land.

## Evidence Discipline

- Use discovery output to narrow the search space before reading files broadly.
- Quote or summarize only what the evidence supports.
- If two sources conflict, call out the conflict instead of forcing a neat conclusion.
- If a claim is inferred rather than directly observed, label it as an inference and explain what evidence supports it.

## OpenAI / Codex Questions

For OpenAI API, Codex, MCP, or ChatGPT-app questions:

- use the `openaiDeveloperDocs` MCP server first
- fall back to official OpenAI docs only if MCP is unavailable or insufficient

That instruction is durable because the repo contains Codex-related workflow and prompt material, and stale advice here is costly.
