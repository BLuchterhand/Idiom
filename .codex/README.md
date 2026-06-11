# Codex Setup

This directory holds repo-local Codex guidance for `idiom`.

## Files

- `../AGENTS.md`: the primary instruction file Codex auto-loads for this repository
- `project-context.md`: distilled repo context, north-star guidance, commands, and doc map for Codex sessions
- `common-traps.md`: recurring prompt, contract, evidence, audit, and process traps to check before changing Idiom behavior
- `agents/*.toml`: project-scoped Codex subagents with Idiom-specific reasoning-effort defaults

## Notes

- The OpenAI developer docs MCP server is expected for OpenAI/Codex/API questions and should be available globally as `openaiDeveloperDocs`.
- Keep durable instructions in `AGENTS.md`. Use this directory for project-specific supporting context that is useful to reference but should not all live in the top-level instruction file.
- The repo already has assistant-oriented prompt material in `prompts/` and `.claude/`; this `.codex` setup translates the durable parts of that guidance into Codex-native project instructions rather than duplicating every prompt verbatim.

## Subagent Reasoning Effort

Default to medium reasoning for ordinary Idiom implementation work. Select a
specialist agent when the task shape is clearer:

- `idiom-explorer`: low effort, read-only exploration, localization, log triage, and evidence summaries.
- `idiom-worker`: medium effort, localized implementation, documentation updates, and focused refactors.
- `idiom-reviewer`: high effort, review for correctness, contracts, regressions, security, architecture boundaries, and missing tests.
- `idiom-root-cause`: high effort, behavior debugging across prompts, control rails, readiness, recovery, telemetry, and evals.
- `idiom-strategist`: xhigh effort, rare cross-cutting design, migration, or incident-analysis decisions where wrong conclusions are costly.

Effort follows contract risk, not task size. Small prompt, gate, runtime, or
readiness changes usually deserve high effort; large mechanical scans usually
deserve low or medium effort.
