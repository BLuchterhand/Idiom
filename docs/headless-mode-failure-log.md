# Headless Mode Failure Log

This log captures idiom failures observed while implementing headless parity.
Each entry names the evidence, the harness boundary that owns the issue, and
the implementation status.

## 2026-06-21: Headless Planning Timed Out Without a Terminal Artifact

Evidence:

- Session `20260621-061400.458`
- Command shape: `./idiom -prompt <headless parity implementation planning prompt> -json`
- Result: the external supervisor timed out after 180 seconds. The run produced
  useful localization events but did not return a final answer or a typed
  terminal artifact to the caller.

Broken invariant:

Headless automation needs a durable terminal artifact that reports whether the
run completed, failed, degraded, or timed out. The existing root `--prompt`
path emits runtime JSONL events but does not give automation callers a stable
completion record.

Implementation status:

- Fixed for the new `idiom run` surface in this branch.
- `idiom run` writes `.idiom/runs/<run-id>.completion.json` using
  `schema_version: idiom.headless.run_completion.v1`.
- The record includes exit code, terminal class, policy overrides, checks, and
  artifact paths.
- `--fail-on-degraded` lets CI reject requests that asked for a parity feature
  that is only recorded, not enforced.

Remaining follow-up:

Legacy `idiom -prompt ... -json` remains a compatibility path. New automation
should use `idiom run`; if the legacy path stays supported for CI, it should
delegate to the same completion-record writer instead of maintaining a separate
contract.
