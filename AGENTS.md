# Idiom AGENTS

## North Star

Idiom is a local coding-agent harness. The model owns strategy; the harness owns contracts, evidence, permissions, audit, and verification.

## Start Here

- Read [README.md](README.md), [docs/README.md](docs/README.md), and
  [docs/architecture.md](docs/architecture.md) before changing runtime,
  prompts, routing, control rails, readiness gates, workspace tools, or release
  behavior.
- Read [docs/security-privacy-posture.md](docs/security-privacy-posture.md)
  before changing logging, telemetry, audit output, tool execution, config, or
  secret handling.
- Keep terminology precise: strategy belongs to the model; enforceable
  contracts belong to the harness.

## Engineering Baseline

- Prefer clear, explicit Go over clever compression.
- Keep business logic, infrastructure, persistence, CLI, TUI, and prompt
  concerns separated unless an existing package intentionally combines them.
- Avoid pass-through wrappers and one-off helpers. Abstract only when a shared
  concept has proved itself.
- Use constants, typed enums, structs, and narrow interfaces where they encode
  real contracts. Do not add ceremony for simple values.
- Tests should specify behavior and meaningful edge cases, not implementation
  trivia.
- Before finishing a change, review the diff for naming, complexity,
  duplication, boundary leaks, dead code, hidden assumptions, and missing
  verification.

## Harness Rules

- Force only real invariants: permissions, protected paths, protocol shape,
  evidence freshness, source-body support, verification after writes,
  final-answer acceptance, loop bounds, and exhausted deliverable budgets.
- Do not hard-pick the model's next reasoning step when multiple valid
  strategies remain. Prefer typed contracts, evidence predicates, and advisory
  nudges.
- Evidence tools should report findings, paths, symbols, counts, and
  uncertainty. They should not prescribe the next tool call.
- All findings and answers must be grounded in code, tests, logs, tool output,
  or cited documentation. Mark inference as inference.

## Workflow

- Do not guess paths. Discover them first.
- For behavior debugging or broad harness investigations, start with the actual
  `idiom` binary when it can run, then inspect logs, telemetry, and audit
  evidence before patching prompts or gates.
- For local source investigation, prefer the narrowest effective tool:
  language-aware navigation, `idiom tool context_pack`, structured discovery,
  then `rg` or direct file reads.
- Keep edits targeted and preserve user changes.
- Before removing established behavior, inspect relevant history when the
  rationale is not obvious.
- Treat repeated local fixes in the same area as a signal for a missing shared
  contract or abstraction.

## Verification

- Default code verification: `go test ./...`.
- For shared runtime, prompt, control-rail, routing, workspace-tool, installer,
  or release changes, also run the relevant focused package tests and build the
  CLI with `go build ./cmd/idiom`.
- For release cleanup, run a secret scan and inspect any remaining findings as
  either true positives to purge or explicit test fixtures to allowlist.
