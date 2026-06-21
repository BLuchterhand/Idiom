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

## 2026-06-21: Review Found Incomplete Headless CLI Contracts

Evidence:

- Manual review of `internal/surfaces/cli/headless/headless.go`
- Focused tests in `internal/surfaces/cli/headless/headless_test.go`
- No-model smoke checks with `/tmp/idiom-headless-review`

Failures:

- Invalid `--permission-profile` and malformed `--timeout` values were ignored
  instead of failing before model execution.
- `--output=json` emitted only the completion record, so automation callers did
  not receive the final answer on stdout.
- Completion-record save failures could leave a run with exit code 0.
- Lint, test, and commit commands were not bounded by the workspace command
  timeout.
- `idiom serve --once` was a test-only convenience that made smoke checks
  brittle and was not part of the documented user contract.

Broken invariant:

Headless flags and artifacts are runtime contracts. Invalid policy inputs,
missing answer transport, unreported artifact failure, and unbounded check
commands all make the automation surface less reliable than the TUI/harness
contract underneath it.

Implementation status:

- Fixed in this branch.
- Policy and timeout flags now fail fast.
- JSON stdout now includes the answer, while durable completion records keep
  prompt and answer hashes only.
- Completion-save failures now produce `terminal_class: artifact_failed` and
  a non-zero exit.
- Lint, test, and commit commands use the workspace command timeout.
- `serve --once` and its single-connection listener were removed; server health
  handlers are tested directly.

## 2026-06-21: Harness Audit Reported Existing Contract/Audit Failures

Evidence:

- Command: `/tmp/idiom-headless-review tool harness_audit --input
  '{"purpose":"behavior","limit":20}'`
- Audit summary: `findings=5`
- High-severity finding: `turn_phase_contract_contradiction`
  - Evidence: `5` current-log contradictions in the summary and `475`
    contract-coverage contradictions in the full audit payload.
  - Sample refs from the redacted audit: event ids
    `event_0aebbd720b5a45bd`, `event_0db2dd881f03e26e`,
    `event_1e524e5d1c10a42c`; contract id
    `contract_b905c71726ce02d3`.
- High-severity finding: `contract_event_privacy_forbidden_raw_content`
  - Evidence: `22` forbidden raw-content fields or value markers in autonomy
    event metadata.
  - Sample refs from the redacted audit: event ids
    `event_080f87db04afbb47`, `event_0dc716193517c0c0`,
    `event_112a59d8fb2db309`; contract ids
    `contract_0891fe13f3f34377`, `contract_4357424c89e61d27`.

Broken invariant:

Autonomy and contract-audit events must be internally coherent and
redaction-safe. A contract event that simultaneously advertises contradictory
turn-phase state is not a reliable audit source. Metadata carrying raw-content
markers violates the audit privacy boundary even when the output is otherwise
redacted.

Implementation proposal:

1. Add a redaction gate at autonomy event construction so metadata keys and
   values are scrubbed before events enter logs or audit state. The gate should
   reject or replace fields named like `raw`, `content`, `prompt`,
   `transcript`, `body`, `excerpt`, and known raw-content marker values unless
   the field is explicitly allowlisted as a count, hash, path, id, or boolean.
2. Add source-owner details to `harness_audit` findings: event id, contract id,
   event type, emitting package/function when known, and sanitized field path.
   The current high-severity findings are actionable as evidence of a class of
   failure, but not yet precise enough for a safe local patch.
3. Reconcile turn-phase surface emission so `contract_presented`,
   `model_exchange`, `readiness_state_updated`, and `turn_phase_surface_summary`
   derive terminal/readiness/action state from one typed projection. Add a test
   that a single synthetic turn cannot audit as both terminal-ready and
   action-required for the same contract id.
4. Add a focused audit regression fixture with one contradictory turn-phase
   event and one raw-content metadata event, then assert both are reported with
   sanitized, source-owner references.

Implementation status:

- Logged as follow-up work from the review.
- Not patched in this headless branch because the audit payload exposes only
  redacted ids for the high-severity findings; patching the suspected emitters
  without source-owner evidence would risk a symptom fix.
