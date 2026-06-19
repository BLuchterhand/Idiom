# Rearchitecture Hosted Lane Smoke Brief

Status: planned

## Scope

- `docs/rearchitecture-smoke-target.md`

## Reference Context

- `docs/rearchitecture-smoke-brief.md`
- `docs/idiom-rearchitecture-tracker.md`
- `docs/rearchitecture-phase0-evidence.md`

## Files To Modify

| File | Change |
| --- | --- |
| `docs/rearchitecture-smoke-target.md` | MODIFY line 3 smoke marker only. |

## Verified Current State

- `docs/rearchitecture-smoke-target.md:3` currently reads `Status: planned`.

## Required Behavior

Use this brief only to verify hosted DeepSeek V4 Flash lane dispatch, telemetry
attribution, protocol projection, accepted docs-only write execution, and
terminal state.

In `docs/rearchitecture-smoke-target.md`, replace line 3:

old_text:

```text
Status: planned
```

new_text:

```text
Status: smoke-planned
```

## Acceptance

- The run records `deepseek-v4-flash` backend/model attribution for planner,
  executor, and classifier roles.
- The run records parser mode, contract id, protocol hash, and terminal state.
- The standard smoke command copies
  `docs/rearchitecture-smoke-target.template.md` over
  `docs/rearchitecture-smoke-target.md` before and after the run, so the smoke
  is rerunnable before or after this tracker is committed.
- The run changes no runtime source, contract source, prompts, CLI/TUI code, or
  backend configuration.
