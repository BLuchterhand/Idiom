# Codex Setup

This directory holds repo-local Codex guidance for `idiom`.

## Files

- `../AGENTS.md`: the primary instruction file Codex auto-loads for this repository
- `project-context.md`: distilled repo context, north-star guidance, commands, and doc map for Codex sessions
- `common-traps.md`: recurring prompt, contract, evidence, audit, and process traps to check before changing Idiom behavior

## Notes

- The OpenAI developer docs MCP server is expected for OpenAI/Codex/API questions and should be available globally as `openaiDeveloperDocs`.
- Keep durable instructions in `AGENTS.md`. Use this directory for project-specific supporting context that is useful to reference but should not all live in the top-level instruction file.
- The repo already has assistant-oriented prompt material in `prompts/` and `.claude/`; this `.codex` setup translates the durable parts of that guidance into Codex-native project instructions rather than duplicating every prompt verbatim.
