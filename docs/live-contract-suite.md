# Live Contract Suite

The live contract suite is the expensive, model-backed smoke bed for harness
contracts that unit tests cannot prove. It runs the built `idiom` binary against
the configured local model stack, on a frozen fixture repository or archived Git
commit, and rolls the workspace back after every task.

Run the static suite lint:

```bash
make live-contract-suite
```

Run the live suite:

```bash
make live-contracts
```

Run the deterministic e2e suite with a scripted fake model:

```bash
make scripted-contracts
```

`make scripted-contracts` uses the same frozen workspace, generated-log scoring,
and rollback oracle as `make live-contracts`, but starts a tiny local
OpenAI-compatible fake model for each task. This is the default regression bed
for lane contracts, malformed tool-call repair, rejection handling, and rollback
proofs that do not require real model judgment. Keep live-model cases for the
smaller set of prompts where the actual local model's strategy and prompt
following are the behavior under test.

`make live-contracts` depends on `make build`, then invokes
`scripts/live-contract-runner.sh` through `cmd/idiom-swe-eval`. Each task gets a
fresh temporary workspace initialized as a Git repository. Most tasks copy a
small fixture directory such as `testdata/live-contracts/fixtures/frozen-label-repo`.
Regression tasks that need a real historical repo state can instead set
`fixture_git_ref` to a commit hash; the runner uses `git archive` from the suite
repository, so uncommitted local files and ignored state are not copied. The
runner records the baseline tree, runs the built binary from `~/.idiom/bin/idiom`
unless `IDIOM_BIN` is set, and always executes:

```bash
git reset --hard HEAD
git clean -fdx
```

The generated JSONL log includes:

- `idiom.live_contract.workspace_prepared`: fixture path or Git ref, temporary
  workspace, source commit/tree, baseline tree, and rollback strategy.
- `idiom.live_contract.resolved_config`: redacted runtime/config evidence for
  the temporary run, including model path existence, tool-log directories, MCP
  enablement, and route-classifier wiring.
- `idiom.live_contract.rollback`: files dirty before rollback, files dirty
  after rollback, baseline tree, post-reset tree, and rollback status.

The evaluator treats `changed_before_reset` from the rollback record as
observed changed-file evidence. That lets implementation tasks assert real
workspace mutation even though the runner restores the fixture before exiting.
Tasks can also require `require_clean_rollback`; that asserts exactly one
rollback record with `status:"clean"`, no dirty files after reset, and matching
baseline/post-reset Git trees.

Useful knobs:

- `IDIOM_CONFIG=/path/to/config.json`: base config with local backend settings.
- `IDIOM_BIN=/path/to/idiom`: built binary to test.
- `LIVE_CONTRACT_SUITE=...`: alternate suite file.
- `LIVE_CONTRACT_LOGS=...`: generated JSONL output directory.
- `IDIOM_SWE_TASK_TIMEOUT=180`: override the per-task watchdog seconds. Without
  this override, task timeout is derived from `oracle.max_duration_millis` plus a
  small teardown buffer, so larger plan-execute contracts can opt into longer
  live runs.
- `IDIOM_LIVE_ENABLE_ROUTE_CLASSIFIER=1`: use the configured classifier server
  instead of routing classifier prompts through the already-running planner
  endpoint.
- `IDIOM_LIVE_ENABLE_MCP=1`: keep configured MCP connectors enabled. They are
  disabled by default so local contract tests do not depend on external tools.

Keep this suite small. Add new cases only for contracts that need a real model,
real prompt stack, real binary wiring, and rollback proof.

## Scripted Contract Suite

Scripted contracts live under `testdata/scripted-contracts`. Each task can set
`runner_mode` to choose an explicit binary entrypoint (`implement` or
`plan-execute`) instead of relying on natural-language lane activation. The
matching fake-model script is `testdata/scripted-contracts/scripts/<task-id>.json`
unless `IDIOM_SCRIPTED_MODEL_SCRIPT` or `IDIOM_SCRIPTED_MODEL_DIR` overrides it.

The scripted server supports `/v1/models` and `/v1/chat/completions`, returning
one scripted assistant response per model call. Responses may be plain textual
tool JSON, native `tool_calls`, or final assistant content. The runner records
`idiom.scripted_model.requests` in the task log so the evaluator can assert the
fake model was actually exercised.

## Contract Authority Migration Coverage

For contract-authority migration work, use scripted contracts before live smoke:
scripted replay should prove `ToolIntent`, typed route/runtime policy, path-role
decisions, obligation closure, typed recovery, completion gates, rollback, and
audit reconstruction. Live contracts should then cover only the smaller set of
model-dependent behaviors: local-model prompt following, terminal plan pressure,
executor handoff, verification behavior, resolved runtime policy, and rollback
under the real binary/model wiring.

Do not weaken implementation-required live fixtures to accept typed blockers.
Add a separate blocker or recovery fixture when typed-blocker quality is the
behavior under test.
