# Live Contract Suite

The live contract suite is the expensive, model-backed smoke bed for harness
contracts that unit tests cannot prove. It runs the built `idiom` binary against
the configured local model stack, on a frozen fixture repository, and rolls the
fixture back after every task.

Run the static suite lint:

```bash
make live-contract-suite
```

Run the live suite:

```bash
make live-contracts
```

`make live-contracts` depends on `make build`, then invokes
`scripts/live-contract-runner.sh` through `cmd/idiom-swe-eval`. Each task gets a
fresh temporary copy of `testdata/live-contracts/fixtures/frozen-label-repo`,
initialized as a Git repository. The runner records the baseline tree, runs the
built binary from `~/.idiom/bin/idiom` unless `IDIOM_BIN` is set, and always
executes:

```bash
git reset --hard HEAD
git clean -fdx
```

The generated JSONL log includes:

- `idiom.live_contract.workspace_prepared`: fixture path, temporary workspace,
  baseline tree, and rollback strategy.
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
- `IDIOM_SWE_TASK_TIMEOUT=180`: per-task watchdog seconds.
- `IDIOM_LIVE_ENABLE_ROUTE_CLASSIFIER=1`: use the configured classifier server
  instead of routing classifier prompts through the already-running planner
  endpoint.
- `IDIOM_LIVE_ENABLE_MCP=1`: keep configured MCP connectors enabled. They are
  disabled by default so local contract tests do not depend on external tools.

Keep this suite small. Add new cases only for contracts that need a real model,
real prompt stack, real binary wiring, and rollback proof.
