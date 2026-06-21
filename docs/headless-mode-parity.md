# Headless Mode Parity

This note records the headless harness controls added for parity with common
agent runners such as Codex non-interactive mode, Claude Code headless mode,
OpenCode, Aider, and SWE-agent.

## Implemented Surface

`idiom run` is the stable automation entry point for a single headless turn.
It wraps the existing non-interactive prompt runner instead of introducing a
second agent loop. It accepts prompts from `--prompt`, `--prompt-file`,
positional text, `-`, or piped stdin. Output modes are `text`, `json`, and
`jsonl`; `--json` is an alias for `--output=jsonl`.

Each run writes a terminal record under `.idiom/runs/<run-id>.completion.json`.
The record uses `schema_version: idiom.headless.run_completion.v1` and includes
the run id, prompt and answer hashes, output mode, exit code, terminal class,
policy overrides, checks, and artifact paths. `--summary-out` writes the same
record to a caller-selected path. The durable record does not persist the raw
prompt or answer. `--output=json` includes the final answer on stdout for the
calling process. `--patch-out` writes `git diff --binary`.

Headless JSONL events now include
`schema_version: idiom.headless.event.v1`. The event stream remains the existing
runtime stream; the version marker gives callers a compatibility anchor without
changing the model/tool protocol.

## Policy Controls

`idiom run` accepts per-run policy flags:

- `--permission-profile read-only` disables workspace writes.
- `--permission-profile write` enables workspace writes.
- `--allow-tool` and `--deny-tool` project into `workspace.tool_rules`.
- `--allow-write` and `--deny-path` project into write-tool path rules.
- `--max-tool-steps` and `--timeout` project into the loaded chat config.
- `--ephemeral` disables durable save/debug log directories for the run.

`--output-schema` requires the final answer to contain a JSON object whose
`schema_version` matches the requested value. `--approval-policy never` is
accepted for headless runs. Other approval policies are recorded as degraded
intent for now because they need a non-interactive approval transport.
`--fail-on-degraded` lets CI reject those requests instead of silently accepting
a weaker contract.

## Inspection Surface

`idiom session` adds automation-friendly session handling:

- `list` summarizes known conversation logs.
- `inspect` reports basic event counts and the last event type.
- `export` writes a sanitized record list that keeps event metadata and
  summaries but drops raw prompt, transcript, and file-body content.
- `replay` emits recorded user prompts as a dry replay plan.

`idiom contract inspect|obligations|children` reports the active workspace
contract surface from config and, when a session id is supplied, attaches a
plan-execute terminal completion record if one exists.

`idiom decision why` scans a session log for route, decision, policy, and
rejection events and returns the selected event. This is intentionally a
diagnostic read model; it does not infer a decision that was not logged.

`idiom serve` exposes a minimal HTTP health/status server:

- `GET /healthz`
- `GET /v1/status`

This is not a replacement for MCP discovery or the TUI. It establishes a
separate headless server surface that can grow into a run API without mixing
agent execution with tool discovery.

## Remaining Gaps

The remaining parity gaps are enforcement depth, not command discovery:

- `--approval-policy` needs a typed non-interactive approval transport.
- `idiom serve` should add an authenticated `/v1/run` endpoint before it is
  treated as a full remote agent server.
- `session replay` is dry-run only; executing replay should require explicit
  policy and output destinations.

These gaps should be implemented at the CLI/runtime contract boundary. They
should not be patched through prompt wording.
