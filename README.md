# Idiom

A local coding-agent harness for running local and hosted models through
`llama.cpp`, MLX, or OpenAI-compatible APIs, with structured CLI subcommands
for plan-execute / implement / atom-spec lanes plus a Bubble Tea terminal UI.

Idiom is being built and optimized around the Qwen 3.6 family of models,
though it has also been tested successfully against GLM 5.1 and Minimax m2.7
models. It is meant to be an agnostic harness for local and hosted models, but
it has not yet been broadly validated across providers.

CURRENT STATE: Idiom is used primarily as a headless runner. The product
workflow has deterministic headless CLI surfaces under `idiom product`, but the
PRD/ADR/brief design still has open post-closeout blockers before it is fully
delivered. The TUI is being brought up to speed and should not yet be treated as
authoritative for complex product-workflow continuation.

## What it does

- launches or attaches to local `llama-server` / `mlx_lm.server` backends, or
  calls a hosted backend
- can stream chat responses into a terminal UI
- saves transcripts to Markdown
- loads baseline behavior from prompt files and profiles every session
- gives the model scoped workspace access for reads, search, writes, and shell commands
- keeps model weights, caches, and harness logs out of the target repository

## Architecture at a glance

Idiom's core split is simple: the model owns strategy; the harness owns
contracts, evidence, and verification. The runtime surfaces that split through
typed lanes, repository memory, capability-oriented tools, and audit output.

For a connected architecture and operations knowledge base, open
[`docs/obsidian`](docs/obsidian/README.md) as an Obsidian vault. It explains the
deterministic evidence-based development pattern, runtime lanes, repository
memory, tool contracts, verification, audit, evals, and package ownership.

## Install from source (current path)

Use this path when working from this repository or before a binary release
exists. The release installer needs a published GitHub Release asset; until a
`v*` tag has been pushed and the release workflow has uploaded binaries, build
from a clone.

On macOS with Homebrew, run the master installer to install the required local
toolchain, workspace discovery backends, the source-built `idiom` binary, and
the per-user queue daemon:

```bash
./scripts/install-requirements.sh
```

The default install covers Go, ripgrep, ast-grep, Universal Ctags, Semgrep,
Joern, and `gopls`. It runs `make build`, which compiles the canonical
source-build binary at `~/.idiom/bin/idiom`, then refreshes `bin/idiom`,
repo-root `./idiom`, and `~/.local/bin/idiom` as symlinks to that file.

To install optional precision backends for other language families, run:

```bash
./scripts/install-requirements.sh --all
```

Useful installer flags:

- `--no-build` — install/check the toolchain without running `make build`.
- `--no-audit` — skip the final `harness_audit` tool-surface check.
- `--no-queue` — skip the macOS launchd queue daemon install.

If you already have the prerequisites and only need the binary:

```bash
make build
```

On Linux or other Unix-like systems, `scripts/install-requirements.sh` exits by
design because it is macOS/Homebrew-specific. Install the Go version declared in
`go.mod` and any discovery CLIs you need, then run `make build` or:

```bash
go build -o bin/idiom ./cmd/idiom
```

## Release install (after a tagged release)

`install.sh` downloads a release archive from GitHub Releases. This command
will fail until a release tag exists and `.github/workflows/release.yml` has
uploaded assets for that tag.

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh
```

That fetches the latest release for your OS/arch (macOS or Linux,
`amd64` or `arm64`), verifies the SHA256 checksum, and drops the
binary in `~/.local/bin/idiom` (or `/usr/local/bin/idiom` if that's
writable without sudo). Add `~/.local/bin` to your shell's `$PATH` if
you don't already have it.

Override knobs (env vars): `IDIOM_VERSION=v0.1.0` to pin an existing release,
`IDIOM_INSTALL_DIR=~/bin` to change the destination, and `IDIOM_REPO=owner/fork`
to install from a fork.

## Quick start

Verify the host is ready:

```bash
idiom doctor
```

`idiom doctor` checks for a backend runner (`mlx_lm.server`,
`llama-server`, or `ollama` on `$PATH`), local model files in
`~/.cache/huggingface`/`~/models`/`~/.ollama/models`, project config,
and supporting tools (`python3`, `git`). It also reports generic
readiness profiles for `discovery`, `plan`, `implement`, `eval`, and
`sidecar` workflows. Each profile includes machine-readable readiness
gates with `pass`, `degraded`, or `blocked` status, hard failures, soft
degradations, launch-required flags, and exact remedy lines. P6 doctor
gates are `enforcement:"report_only"` until lane-start enforcement is
wired through the runner; `--json` emits the structured checks and gate
policy for CI or local evidence export. Use `--profile <name>` to limit
the readiness section while keeping the legacy host checks.

Run a lane against a brief:

```bash
idiom lane plan-execute --title my-task path/to/brief.md
```

`idiom lane` is the recommended entry — it composes the prompt
overlay, auto-routes plan-execute to the configured executor model,
preloads a compact ranked `context_pack` for implement / plan-execute
lanes, and forwards JSON events. Internal refine scratchpad meta-prompts
keep their phase-specific evidence instead of receiving an extra repo map.
The first refine draft is materialized through `idiom artifact --no-tools`
so the model returns content and the harness writes `.idiom/refine/.../draft.md`;
later review/patch phases keep their narrower scratchpad writer contracts.
Pass `--no-context-pack` only when a caller has already supplied an
equivalent packet. See
[docs/lane-architecture.md](docs/lane-architecture.md) for the lane
shapes (`plan-execute`, `implement`, `atom-spec`) and brief format.

Other useful subcommands:

- `idiom version` (or `idiom --version`) — print binary version,
  commit, build date, Go runtime, and host os/arch. The same fields
  are also stamped into telemetry's `session_start` / `process_start`
  records, so log readers can attribute behavior to a specific build
  without scraping the binary.
- `idiom backend start | stop | status | list | proxy` — manage local
  LLM runners.
- `idiom recover inspect --session <id> [--json]` — inspect the recovery
  failure record and classified partial state for a failed `plan-execute`
  session.
- `idiom recover status --packet <packet.json> [--json]` — inspect candidate
  recovery atoms, product links, gates, and the next valid action from a
  decomposition packet.
- `idiom recover decompose --session <id> [--output <packet.json>] [--json]`
  and `idiom recover promote --packet <packet.json> --atom <id> --output
  <brief.md>` — rebuild a recovery packet or promote one candidate atom into a
  normal gated brief. Failed plan-execute runs automatically write both
  `.idiom/recovery/<failure-id>/failure.json` and
  `.idiom/recovery/<failure-id>/packet.json`; promotion still does not dispatch
  follow-up work.
- `idiom tool schemas` — export built-in workspace tool schemas plus
  profile, side-effect, and policy metadata as JSON.
- `idiom tool <name> --input '<json>'` — run one workspace tool and print JSON
  with the legacy top-level `Tool`/`Summary`/`Output`/`Error` fields plus a
  stable `sidecar` envelope (`schema_version:"idiom.tool.v1"`) carrying
  profile, allowlisted short opaque caller metadata, contract metadata,
  freshness, warnings, audit IDs, structured output, and typed errors for
  integrations.
  Recovery state is available through `recovery_inspect` and `recovery_status`
  in the discovery profile; `recovery_decompose` writes local recovery artifacts
  and therefore requires the default profile. `recovery_promote` remains a
  first-party `idiom recover` command. Product operator status also surfaces
  recovery packets referenced from `workflow_state.artifact_paths` and blocks
  normal workflow dispatch while a recovery packet is open.
  See [docs/sidecar-recipes.md](docs/sidecar-recipes.md) for external caller
  recipes covering shell/CI scripts, coding-agent harnesses, local/private-code
  users, and the replayed discovery-profile smoke transcript.
- `idiom atom intake --spec <atom.md> [--project-root <root>] [--json]` —
  classify an atom/spec before dispatch. Intake fails closed for complete,
  source-satisfied, stale accepted-result, failed-refine best drafts, dirty
  scoped-path, or unscoped artifacts, and rejects paths outside the workspace
  root, so queue producers and lane wrappers do not spend tokens on
  non-dispatchable work.
- `idiom queue serve | run | status | ping | cancel` — local job queue
  daemon for gating concurrent lanes/agents against an OOM-safe cap;
  see [Local job queue](#local-job-queue) below.
- `idiom local-ops benchmark --json` — local smoke/capacity/replay
  benchmark evidence for readiness profiles. It reports pass/degraded/blocked
  prerequisites, bounded capacity assumptions, replay-log signals when supplied,
  and metadata-only privacy guarantees without requiring a live model in the
  default smoke.
- `idiom local-ops benchmark --json --quickstart-smoke` — CLI-only
  quickstart activation smoke. It records setup/source-build readiness, first
  useful `context_pack`, freshness, `harness_audit`, suggested verification,
  privacy-safe product-evidence fields, docs gap/failure reason, deterministic
  time-to-first-value fields, and cleanup evidence without requiring a live
  model or network. Use `--quickstart-cleanup remove-local-state` only on a
  clean fixture when you want the smoke to remove generated local context state.
- `idiom product-evidence export --project-root .` — local P7.11 go/no-go
  evidence export. It summarizes activation, retained use, integration,
  benchmark, audit, learning, release-gate linkage, and client-classification
  coverage using IDs, hashes, counts, and statuses instead of prompt bodies,
  file bodies, private paths, or secrets. See
  [docs/product-evidence-export.md](docs/product-evidence-export.md).
- `idiom release evidence --project-root . --output .idiom/release/bundle.json` —
  P9.1 release evidence bundle. It compares baseline/current command artifacts
  and summarizes validation, audit/security, local-ops/benchmark, sidecar
  integration, product-evidence export, and all seven client classifications
  with metadata-only IDs, hashes, relative paths, statuses, and blockers.
- `idiom mcp serve --stdio --profile discovery` — experimental MCP stdio
  sidecar. It serves `initialize`, `tools/list`, `tools/call`, `roots/list`,
  `resources/list`, and metadata-only `resources/read` for schema-derived graph
  discovery tools, using the same discovery profile request filtering as
  `idiom tool --profile discovery`. Resource file bodies are not returned yet;
  resource paths are root-clamped and protected locations are denied. Telemetry
  parity and full resource/freshness parity remain later P8 work.
- `idiom brief lint <brief.md>` — pre-flight lint on an
  implementation brief.
- `idiom lane postflight --diff <patch>` — QC pipeline against a
  returned diff.

## Layout

- `.idiom/config.json`: self-hosted lane/doctor config for this repo
- `config.example.json`: portable llama.cpp starter config for new environments
- `config.json`: root direct-run config for this checkout; do not treat it as
  the portable template
- `prompts/`: baseline prompts and behavior profiles
- `logs/`: server output, per-session JSONL debug logs, and telemetry
- `transcripts/`: saved chats

## Run

```bash
./scripts/run-harness.sh
```

If you want the server without the TUI:

```bash
./scripts/run-server.sh
```

Headless one-shot prompt with structured JSONL events on stdout:

```bash
./idiom --prompt "summarize internal/agent/runner.go" --json
```

Generate a standalone artifact while keeping the event stream separate:

```bash
./idiom artifact \
  --prompt-file /tmp/spec-draft-prompt.md \
  --output /tmp/spec.md \
  --json-out /tmp/spec-events.jsonl \
  --report-out /tmp/spec.report.json \
  --no-tools \
  --mechanical-repair \
  --validator 'markdownlint "$IDIOM_ARTIFACT"'
```

`artifact` is a generic drafting mode: idiom runs one model turn, writes the
final answer as the artifact, optionally fails if the model used tools, writes
raw JSONL events, and can write a provenance/readiness report. The report
records the prompt and answer hashes, model, output paths, no-tools contract,
tool-use detection, validator exit, deterministic repair source, and
`dispatch_ready`. `--mechanical-repair` is intentionally narrow: it unwraps one
accidental outer Markdown fence before writing/validation and records that in
the report. Project-specific rules belong in the prompt and validator, not in
idiom core.

Externalize planning context before asking a model to architect, critique, or
brief a feature:

```bash
./idiom context impact --task-file /tmp/task.md --output /tmp/impact.json
./idiom context pack --task-file /tmp/task.md --output /tmp/context-view.json
./idiom architect packet --task-file /tmp/task.md --schema planning_v2 --context-view /tmp/context-view.json --output /tmp/packet.json
./idiom architect lint --packet /tmp/packet.json
./idiom architect budget --packet /tmp/packet.json
./idiom architect plan --packet /tmp/packet.json --output /tmp/plan.prompt.md
./idiom artifact \
  --prompt-file /tmp/plan.prompt.md \
  --output /tmp/plan.md \
  --json-out /tmp/plan.events.jsonl \
  --report-out /tmp/plan.report.json \
  --no-tools \
  --mechanical-repair
./idiom architect plan-audit --packet /tmp/packet.json --plan-file /tmp/plan.md
```

See [docs/architect-packets.md](docs/architect-packets.md). Idiom owns the
packet schema, prompt rendering, and gate contracts; project profiles own local
rules, collectors, tests, and domain traps.
Use `--schema planning_v2` when you want ranked scope, freshness, language
capability, handoff, and context budget sections for planner-quality review.
Use `architect packet-compare` and `architect replay-compare` to compare v1 and
V2 packet/plan variants from the same task and model run.
`architect plan-audit` also checks unresolved placeholder tokens such as
`<path>` and validation commands that cite missing workspace paths, so packet
plans fail before a lane inherits fake commands or ambiguous follow-up work.

List or resume prior sessions (sessions are the JSONL files in `chat.debug_log_dir`):

```bash
./idiom --list-sessions
./idiom --prompt "what did we conclude?" --resume 20260418-055543.504
```

`--resume` only replays the prior conversation's user/assistant messages into the new run's history. Per-workspace plan/workflow/task-memory state still auto-resumes from its own persisted store on Model construction.

Or use `make`:

```bash
make run
make server
make build
make eval LOG=logs/conversations/20260418-055543.504.jsonl
```

`make build` compiles the single source-build binary at `~/.idiom/bin/idiom`,
then refreshes `bin/idiom`, repo-root `./idiom`, and `~/.local/bin/idiom` as
symlinks to that canonical file. Shells with `~/.local/bin` on `PATH`, direct
repo invocations, and the queue daemon therefore use the same rebuilt binary.

For source-current runs, prefer `go run ./cmd/idiom <subcommand> <flags>` or
run `make build` first and then use any of those symlinked entrypoints.

MCP stdio discovery smoke:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context_status","arguments":{"limit":1}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{}}' \
  '{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"idiom://workspace/file/.git/config"}}' \
  | go run ./cmd/idiom mcp serve --stdio --profile discovery \
      --caller-id mcp-smoke --request-id req-smoke --trace-id trace:mcp-smoke
```

The `resources/read` example above should return a JSON-RPC denial, not file
contents. MCP resources are metadata-only. MCP `tools/call` responses and local
telemetry use the shared sidecar caller vocabulary with
`transport:"mcp_stdio"` instead of CLI transport. The complete replayable MCP
integration proof lives in
`testdata/sidecar/mcp-stdio-integration-proof.json`.

## In-app controls

- `Enter`: send prompt
- `Ctrl+S`: save transcript
- `Ctrl+L`: clear chat
- `Ctrl+R`: restart `llama-server`
- `Ctrl+C`: quit
- `/help`: show slash commands

## Slash commands

- `/save`
- `/clear`
- `/system <prompt>`
- `/temp <value>`
- `/preset [list|name]`
- `/trace [compact|detail]`
- `/context`
- `/workspace`
- `/pwd`
- `/debuglog [mode redacted|full]`
- `/mode [normal|explore [quick|medium|thorough]]`
- `/profile list`
- `/profile add <name>`
- `/profile remove <name>`
- `/server start`
- `/server stop`
- `/server restart`
- `/server status`
- `/help`

## Qwen 3.6 Tuning

`idiom` now exposes the full Qwen 3.6 sampling surface through both config and the OpenAI-compatible request body:

- `temperature`
- `top_p`
- `top_k`
- `min_p`
- `presence_penalty`
- `repetition_penalty`
- `enable_thinking`
- `preserve_thinking`

The bundled defaults target coding work on Qwen3.6 with thinking enabled:

- `preset: qwen3-coding-preserve`
- `temperature: 0.6`
- `top_p: 0.95`
- `top_k: 20`
- `min_p: 0.0`
- `presence_penalty: 0.0`
- `repetition_penalty: 1.0`
- `enable_thinking: true`
- `preserve_thinking: true`

Built-in presets:

- `qwen3-coding`
- `qwen3-coding-preserve`
- `qwen3-general`
- `qwen3-direct`
- `qwen3-direct-reasoning`

Use `/preset list` to see the preset names and `/preset <name>` to switch live inside the TUI.

Notes:

- If you hand-tune the numeric sampling fields in `config.json`, set `preset: "custom"` so startup does not re-apply a built-in preset over your manual values.
- `qwen3-coding-preserve` is the shipped coding default for long sessions; it consumes more context because historical thinking traces are retained. Use `qwen3-coding` when you want the same sampling settings without retained thinking.
- The shipped config now uses `ctx_size: 262144` for Qwen3.6-35B-A3B. Preserved thinking becomes more useful as context grows, and Qwen recommends large windows for strongest long-horizon behavior.
- The harness now starts `llama-server` with `flash_attention`, `reasoning: auto`, and `reasoning_format: deepseek` by default, so visible responses stay clean while reasoning content remains structured on the server side.

## Inference backends

Each of the three server blocks (`llama_server`, `implementer`,
`route_classifier`) accepts a `kind` field that selects the inference backend.
Local, harness-launchable backends are:

- `"kind": "llama"` (default) — launches `llama-server` from llama.cpp with the existing GGUF model path and llama-specific flags (`jinja`, `flash_attention`, `reasoning*`, `cache-type-*`, etc).
- `"kind": "mlx"` — launches `mlx_lm.server` from [mlx-lm](https://github.com/ml-explore/mlx-lm). On Apple Silicon this typically beats llama.cpp's Metal backend by ~15–30% throughput and ~10% memory. `model` becomes a Hugging Face ID (e.g. `Qwen/Qwen3.6-35B-A3B`) or a local MLX directory — not a GGUF path. Llama-specific flags (`jinja`, `flash_attention`, `reasoning*`) are ignored. Current `mlx_lm.server` builds honor per-request `chat_template_kwargs`, so idiom can intentionally switch `enable_thinking` / `preserve_thinking` for routes that need a different mode; keep server defaults in `extra_args` for fallback behavior, for example `--chat-template-args '{"enable_thinking":true,"preserve_thinking":true}'`. Idiom does not send OpenAI native `tools` arrays to MLX backends; current MLX Qwen tool parsers can terminate the HTTP request on malformed generated tool XML, so MLX lanes use idiom's JSON tool protocol and harness-side validation instead.

Hosted, client-only backends are `"openai_responses"`, `"codex"`, `"zai"`,
and `"minimax"`. They do not start a local process and cannot be used with
`--server-only`; set `model`, optional `base_url`, and optional `api_key_env`.
Default API key env vars are `OPENAI_API_KEY` for OpenAI/Codex,
`ZAI_API_KEY`, and `MINIMAX_API_KEY`.

The blocks are independent, so mixed setups work — for example, main chat on
MLX and the route classifier still on llama.cpp, or a hosted planner with a
local implementer. Set `binary` to `mlx_lm.server` for MLX (install via
`pip install mlx-lm`) and pass any additional flags via `extra_args`.

## Local job queue

`idiom queue` is a small daemon that gates concurrent work against a configurable per-pool cap. It exists so multiple projects on the same machine can run lanes/agents in parallel without crashing or OOM'ing the local LLM backend(s) — each project asks the daemon for a slot, blocks until one is free, runs its command, and the slot releases when the command exits. Anyone with the socket path can also see the live queue.

Start the daemon (foreground; one per machine):

```bash
idiom queue serve
```

By default it listens on `$XDG_RUNTIME_DIR/idiom-runner.sock` (or `$TMPDIR/idiom-runner.sock` on macOS) and reads `~/.idiom/runner.json` for pool config. Override either with `--socket` / `--config` or with `IDIOM_RUNNER_SOCKET` / `IDIOM_RUNNER_CONFIG`.

Wrap any command in a slot acquisition:

```bash
# In any project:
idiom queue run --pool qwen-35b --label "VL-13.4 lane" -- ./scripts/run_idiom_impl.sh ...
```

`queue run` blocks until a slot in the named pool is free, then execs the command. Closing the wrapper (clean exit, ctrl-C, or crash) releases the slot. Exit code mirrors the wrapped command, so this composes with shell-level error handling.

Inspect the queue from anywhere:

```bash
idiom queue status        # human-readable table
idiom queue status --json # for scripts and dashboards
idiom queue ping          # daemon liveness + uptime
idiom queue cancel <id>   # remove a waiting job (in-flight jobs aren't killed; ctrl-C the holder)
```

Config file (`~/.idiom/runner.json`) — omit it and you get a single `default` pool with `max_in_flight=1`:

```json
{
  "pools": {
    "default":    {"max_in_flight": 1},
    "qwen-35b":   {"max_in_flight": 1},
    "qwen-27b":   {"max_in_flight": 1},
    "qwen-coder": {"max_in_flight": 1}
  }
}
```

Pools are independent caps, so `qwen-35b`, `qwen-27b`, and `qwen-coder` can each run one lane simultaneously without contending. Acquiring an unknown pool is rejected — pools must exist in the daemon's config before any project can submit to them.

## Route Classifier

`route_classifier` runs a small structured classifier before execution so terse follow-ups and plan approvals can be reconciled against deterministic routing. It is enabled by default and returns JSON only; host-side control rails still own the final contract and tool permissions.

The app can auto-start a configured small helper classifier when
`auto_start_server` is true. The checked-in config uses a repo-relative model
path, serves it on `http://127.0.0.1:11437`, and writes logs to
`logs/route-classifier.log`; override `server_model`, `base_url`, or the make
variables for your local model layout.

On first use, the harness checks that `server_model` is readable before spawning the helper. If the model path or binary is wrong, it reports a classifier-health error and falls back to host routing instead of blocking the conversation. The classifier prompt is intentionally schema-first and JSON-only, with examples for log inspection, log-fix requests, plan-only requests, durable-plan approvals, and user boundaries.

You can also run the classifier manually with:

```bash
make classifier-server
```

Override any manual-server setting inline, for example `make classifier-server CLASSIFIER_PORT=11438 CLASSIFIER_MODEL=/path/to/model.gguf`.

- `enabled`: turns classifier calls on or off.
- `base_url`: optional OpenAI-compatible endpoint for a separate small model server. Empty uses the main chat endpoint.
- `model`: optional model id for a smaller served model, such as a 7-9B instruct model. Empty uses the active chat model.
- `always_on`: runs classification on every prompt instead of only ambiguous prompts.
- `max_tokens`: output cap for the classifier JSON.
- `timeout_seconds`: short timeout so routing does not block the main response path.
- `auto_start_server`: starts a local classifier server on demand when `base_url` is unavailable.
- `server_model`: local GGUF model path used only by auto-start.
- `start_timeout_seconds`: startup fuse for the local classifier process.
- `binary`, `host`, `port`, `ctx_size`, `threads`, `gpu_layers`, `log_file`, and `extra_args`: local classifier server launch settings.

## Baseline Behavior

The harness builds a hidden `system` prompt on every request from four layers:

1. `chat.system_prompt`
2. `chat.system_prompt_files`
3. `chat.default_profiles` loaded from `chat.profiles_dir`
4. project prompt files from `chat.project_root`

The current active prompt sources are shown in the TUI footer as `context: ...`.

## Debug Logging

Every TUI session now writes a timestamped JSONL debug log in `chat.debug_log_dir`, which defaults to `logs/conversations`.

The logger has two modes:

- `redacted`: default. Hides the full hidden prompt, rewrites workspace paths to workspace-relative form, and masks likely secrets plus large tool payload bodies.
- `full`: keeps the complete hidden prompt and raw tool payloads for deep local debugging.

Each record includes a timestamp and event type. The log captures:

- the hidden prompt/context bundle active for the session
- workspace mode and explore thoroughness changes
- user, assistant, and tool messages
- raw tool requests with step numbers, agent lane, broad-vs-narrow classification, and discovery balance metadata (`discovery_class`, `discovery_scope`, `discovery_cost`)
- tool results with step numbers, agent lane, summaries, errors, and the same discovery balance metadata when applicable
- delegated child tool requests/results as typed records, so live-suite scoring sees the actual discovery tools used inside `task`
- server state changes
- chat/runtime/context errors
- saved transcript paths

Use `/debuglog` in the TUI to show the current session log path, and `/debuglog mode redacted` or `/debuglog mode full` to switch the active mode for the running session.

The default TUI trace view is now compact. Use `/trace detail` when you want raw tool chatter in the visible transcript, or `/trace compact` to collapse it back into a short activity summary in the footer.

## Local Telemetry

Every `idiom` process also writes a local JSONL telemetry file under
`logs/telemetry` by default. This covers TUI launches, `--prompt`,
`--server-only`, `artifact`, `implement`, `architect`, and standalone
`idiom tool` calls without changing stdout contracts. Tool telemetry records
request/result summaries, timing, output sizes, discovery class/scope/cost,
errors, and structured context/feedback fields for tools that emit them; it
does not persist raw tool output. Prompt-like CLI arguments and tool payload
bodies are redacted to length plus digest.

Set `IDIOM_TELEMETRY_DIR=/path/to/logs` to move these files, or
`IDIOM_TELEMETRY=0` to disable the process-level telemetry. Model sessions
continue to write the richer debug logs in `logs/conversations`, and
`harness_audit` behavior scans include both `logs/conversations` and
`logs/telemetry` by default.

## Harness Audit

`harness_audit` is a read-only workspace tool for auditing idiom itself. It
reports evidence about the exported tool surface, language capability wiring,
backend availability, and recent JSONL behavior logs. It is meant to identify
future harness/tooling improvements, not to replace Go or language-specific
code search.

```bash
./idiom tool harness_audit --input '{"purpose":"all","path":"logs/conversations","limit":25}'
```

Use `purpose:"tool_surface"` for static manifest/schema/backend checks, or
`purpose:"behavior"` to scan behavior logs for efficiency and control-rail
signals such as repeated rejected calls, zero-hit searches, or generic-read
overuse. When autonomy contract events are present in the scanned JSONL logs,
the same passive audit output includes `contract_coverage` for lifecycle,
policy, obligation, reference, sidecar/tool-call, and product metadata coverage.
Agents can add task-grounded harness/tool/prompt observations with
`harness_feedback`; behavior audits aggregate those events by category,
component, confidence, and severity.

```bash
./idiom tool harness_feedback --input '{"category":"tool_gap","component":"context_pack","expected":"packet names the deciding relationship","observed":"packet named candidate files but omitted the edge needed to choose","workaround":"used language references before editing","impact":"extra read loop","evidence":["logs/conversations/session.jsonl step 8"],"confidence":"verified","severity":"medium"}'
```

For autonomous local monitoring, run `idiom audit watch`. The watcher polls
telemetry and conversation JSONL streams, debounces high-signal trigger events
such as completed `implement` processes, completion-gate records, high-risk stop
reasons, malformed tool input, stale-context reports, recovery record/packet
events, and `harness_feedback`, then writes deterministic review packets under
`.idiom/audits/watch` with bounded recovery summaries when applicable.

```bash
./idiom audit watch --config config.json
./idiom audit watch --config config.json --once --review-existing --debounce 0
```

The first long-running invocation creates a baseline by default; pass
`--review-existing` when you want the initial scan to review existing logs.

## Brief Pre-Flight Lint

`idiom brief lint <brief.md>` runs a deterministic pre-dispatch lint on an
implementation brief before handing it to `idiom implement`. The lint covers
single-purpose decomposition, prompt-magic contract triggers, content fence
safety, blockquoted code, test-anchor preload presence, verified-current-state
presence, exact-anchor specificity for every required edit path, and
plan-execute edit-shape readiness.

```bash
./idiom brief lint path/to/brief.md
```

Exit codes: `0` OK (warnings still printed), `1` lint failure, `2` usage
error. The implementation is a Python script embedded in the binary and
extracted to a temp dir on each invocation; `python3` must be on `PATH`.

The exact-anchor check is the strongest signal — every required edit path
must carry at least one `<path>:<line>` reference, an explicit `line N` /
`lines N-M` mention, or a quoted unique snippet from the file. Relational
anchors ("near other small predicates", "find the closing brace") force the
lane to spend reads locating the insertion site and frequently exhaust the
pre-write read budget.

For plan-execute briefs, a line reference is not enough when the Required
Edits/Behavior section asks the planner to run a mutating command or append /
insert from prose. Inline an executable shape instead:
`old_text`/`new_text`, `start_line`/`end_line`+`content`, or whole-file
replacement content. Command-generated files should be generated before
dispatch and handed to the lane as exact edits.

This was originally Colony's `scripts/qwen_brief_preflight.py`; the canonical
implementation now lives with idiom so any project dispatching multi-file
briefs through `idiom implement` can run the same lint pre-flight.

## Queue Daemon

`idiom queue serve` runs a per-user Unix-socket daemon that gates concurrent
`idiom plan-execute` and `idiom implement` dispatches against a small set of
named pools. Without it, two simultaneous lanes can both hit the same MLX
backend and crash it under Metal command-buffer pressure; with it, the second
lane blocks until the first releases.

The macOS install path provisions it automatically via a per-user launchd
agent:

```bash
./scripts/install-requirements.sh             # full install (queue daemon included)
./scripts/install-requirements.sh --no-queue  # skip the daemon
./scripts/install-queue-daemon.sh             # install / re-bootstrap just the daemon
./scripts/install-queue-daemon.sh --reload    # restart after binary change
./scripts/install-queue-daemon.sh --uninstall # remove the agent + plist
make test-install-queue-daemon                # 5-test smoke check
```

Defaults the installer writes:

- Config: `~/.idiom/runner.json` (planner pools `qwen-35b` / `qwen-27b` and
  executor pool `qwen-coder`, each `max_in_flight=1`, matching the local Qwen
  backend profiles)
- Launchd agent: `~/Library/LaunchAgents/com.idiom.queue.plist`
- Binary: `~/.idiom/bin/idiom` (the canonical source-build binary; `make build`
  refreshes this file and points `bin/idiom`, repo-root `./idiom`, and
  `~/.local/bin/idiom` at it. Launchd-spawned processes hang in dyld when their
  executable resolves through TCC-protected paths like `~/Documents/`, so the
  real binary lives in unrestricted user space)
- Socket: `~/.idiom/runner.sock` (stable across shell / launchd / SSH sessions)
- Logs: `~/.idiom/logs/queue-daemon.{log,err}`

Once installed, `idiom plan-execute` and `idiom implement` automatically dial
the daemon via `runner.MaybeAcquire`, choose a pool by model alias (`*coder*`
→ `qwen-coder`; `*27b*` → `qwen-27b`; `*35b*` / `*planner*` → `qwen-35b`),
and wait for a lease before starting the planner phase. When the daemon isn't
running, both commands proceed un-gated — the gate is opportunistic, not
required.

Tune pool caps after install by editing `~/.idiom/runner.json` and running
`scripts/install-queue-daemon.sh --reload`. Status: `idiom queue status`.

## Context-Pack Cache

`context_pack` results are persisted in the `graph_views` table inside
`.idiom/comprehension/graph.sqlite`, keyed by content fingerprint (active
graph `index_id` + canonical task/mode/query inputs). Identical requests are
served from the cache instead of being recomputed; the planner's `pack_id`
travels through `submit_plan` so the executor phase can rehydrate the same
view via `comprehension.LoadViewByID(root, pack_id)`.

Invalidation is path-aware. Scoped `context build --refresh` marks stale
only the rows whose referenced files changed; full rebuilds blanket-stale
the table. `idiom tool harness_audit --input '{"purpose":"behavior"}'`
surfaces row count, fresh/stale split, bytes stored, and session-local
hit/miss counters under `context_cache`.

The `views/<pack_id>.json` files under `.idiom/comprehension/views/` are
human-readable mirrors of the SQLite rows for inspection — the SQLite row
is the source of truth.

Graph-backed tool outputs (`context_status`, `context_query`, `context_zoom`,
`context_pack`, and `context_impact`) include structured freshness,
confidence, and provenance fields so sidecar callers can distinguish fresh
graph evidence, scoped-fresh evidence, stale paths, and the backing index
without parsing prose summaries.

## Ranker Learning

`context_pack` and `context_impact` rank candidate files using intent-aware
path/graph signals plus a per-workspace learnable multiplier persisted under
`.idiom/ranker/weights.json`. Each candidate file carries a `score` and a
`hub` flag in the packet output; consumers should treat the score as the
primary ordering and `hub: true` as "imported by many things, demoted unless
the task names it."

Eval runs feed the learning loop automatically when an `SWEOracle.Groundedness`
oracle is configured: scores >= 0.75 boost the oracle's `context_files`,
scores < 0.40 penalize them, and the noisy middle band records a no-op so the
audit trail still reflects that the eval ran. Every update writes to
`.idiom/ranker/learning_log.jsonl` under `source: "eval"`.

For manual nudges, the `idiom ranker` CLI is the escape hatch:

```bash
./idiom ranker boost    internal/agent/runner.go internal/eval/swe_suite.go
./idiom ranker penalize --note "stale heuristic" docs/old.md
./idiom ranker show
./idiom ranker show --json
./idiom ranker reset
```

Each CLI invocation records the action in the same learning log under
`source: "cli"`. `harness_audit` exposes the merged state through its
`ranker_learning` block, listing wired vs dormant trigger sites and
per-source recommendations for further automation.

## Replay And Eval

`idiom` now includes a scored log-evaluation CLI:

```bash
go run ./cmd/idiom-eval -log logs/conversations/20260418-055543.504.jsonl
```

That prints a quick summary of:

- the last user prompt
- tool counts and broad-search counts
- repeated dead-end counts
- verification status
- changed files when they are present in the final assistant summary

You can also derive a starter scenario file from a captured run:

```bash
go run ./cmd/idiom-eval \
  -log logs/conversations/20260418-055543.504.jsonl \
  -write-scenario /tmp/banner-padding.json
```

And score a later run against that scenario:

```bash
go run ./cmd/idiom-eval \
  -log logs/conversations/20260418-071727.566.jsonl \
  -scenario /tmp/banner-padding.json
```

Scenario files are JSON and can assert things like:

- expected prompt matchers
- required phases
- maximum broad-search count
- maximum repeated dead-end count
- expected verification state
- required changed files
- whether guarded approval should have been required

For a corpus-level SWE task gate, use `idiom-swe-eval`:

```bash
go run ./cmd/idiom-swe-eval \
  -suite testdata/swe/representative-suite.json \
  -logs /path/to/task-logs
```

The log directory should contain one `<task-id>.jsonl` debug log per task in the suite. See `docs/swe-eval-suite.md` for the default task families and oracle schema.

To generate and score those logs from real headless conversations, run:

```bash
make swe-live
```

The live runner executes each task's prompt or multi-turn sequence in an isolated temporary workspace, then scores the produced debug log with the same deterministic SWE oracles.
The SWE report prints per-task discovery-class counts from typed tool request records, which makes broad delegation, text search, file reads, language-symbol tools, AST search, dependency graphs, history, and security scans visible as separate balance buckets.

For faster iteration against a small checked-in subset, including the subagent explanation benchmark, run:

```bash
make swe-live-subset
```

## Explore Mode

Use `/mode explore` to switch the main agent into a Claude-style read-only investigation mode.

The harness also has a lightweight explore subagent:

- for investigation-only prompts such as open-ended bug hunts, the harness now routes the turn into a separate read-only explore lane with its own workspace state and smaller tool budget
- for prompts that ask Qwen to investigate before editing, the explore lane runs first, returns a compact summary, and only that summary is handed to the writable main agent
- the explore lane's tool chatter stays out of the visible transcript, but it is still recorded in the debug log under the `explore` agent lane

The harness also has a dedicated review lane for open-ended audit prompts:

- prompts like `find one bug`, `identify a code smell`, `audit this repo`, or `review this codebase` now route into a deterministic read-only review path instead of the generic tool loop
- the review lane gathers candidate files first, ranks them, reads bounded evidence, and asks the model for one concrete finding instead of letting it freestyle broad repo exploration
- if the prompt explicitly asks to fix or apply the result, only the review summary is handed off to the writable main agent

You can also set the investigation depth:

- `/mode explore quick`
- `/mode explore medium`
- `/mode explore thorough`
- `/mode normal`

In explore mode:

- Qwen is restricted to read-only workspace tools
- `mkdir`, `edit_file`, `write_file`, `append_file`, `apply_patch`, and `go_move_declarations` are rejected
- `shell` is limited to read-only inspection commands such as `ls`, `find`, `rg`, `grep`, `awk`, `sed`, `cat`, `head`, `tail`, `wc`, `file`, and read-only `git` commands
- after one broad orientation step, the runner pushes Qwen to narrow with `search_files`, `search_content`, or `read_file` instead of continuing to survey the repo
- the same “narrow after the first broad step” rule now applies in normal mode too, so repeated `list_files` / `ls` / `find` loops get rejected before they burn the whole tool budget
- the explore subagent is bounded separately from the main agent, so exploratory dead-ends do not consume the full main tool budget
- the harness now keeps a session-local repository profile cache, so later explore turns can reuse discovered files and repo shape instead of cold-starting from another top-level listing
- bootstrap is deterministic and layered: source-first overview, key root-file reads, likely entrypoint reads, then prompt-directed content search before the model is allowed to improvise
- for Go repos, the explore lane now builds a local AST-backed semantic index so bootstrap can reason from packages, entrypoints, functions, methods, and types instead of relying only on filename and grep heuristics
- `quick`, `medium`, and `thorough` now change how many bootstrap reads, search paths, and follow-up file reads the explore lane performs
- successful main-agent writes clear the cached repo profile automatically so later exploration does not rely on stale structure

## Workspace Access

The harness now gives Qwen a tool loop for workspace operations. When needed, the model can ask the harness to:

- print the working directory with `pwd`
- list files with `list_files`
- read files with `read_file`
- count files, bytes, and lines with `file_stats` instead of shell `wc`/`find` pipelines
- search with `rg`
- run deterministic purpose-built discovery with `discovery_query` (`impact_analysis`, `function_tracing`, `architecture_boundary`, `security_review`, or `history_context`)
- search code structurally with `structural_search` (ast-grep when installed)
- inspect module/import dependencies with `dependency_graph`
- inspect code history with `history_range`, `history_symbol`, and `blame_range`
- use fallback symbol discovery with `ctags_symbols` (Universal Ctags when installed)
- run optional security/dataflow discovery with `semgrep_scan` and `joern_scan` when those CLIs are installed
- inspect Go repos semantically with `go_overview`, `go_entrypoints`, `go_find_symbol`, `go_find_type`, `go_find_const`, `go_read_symbol`, `go_find_package`, `go_related_files`, `go_definition`, `go_references`, `go_implementations`, `go_calls_from`, `go_calls_into`, `go_extract_body`, `go_importers`, `go_diagnostics`, and `go_workspace_symbol`
- inspect C/C++ repos semantically with `cpp_overview`, `cpp_entrypoints`, `cpp_find_symbol`, `cpp_read_symbol`, `cpp_related_files`, `cpp_definition`, `cpp_declaration`, `cpp_implementations`, `cpp_references`, `cpp_calls_from`, `cpp_calls_into`, `cpp_extract_body`, `cpp_includes`, `cpp_who_includes`, `cpp_blast_radius`, `cpp_dead_symbol`, `cpp_diagnostics`, and `cpp_workspace_symbol`
- create directories with `mkdir`
- make targeted in-place edits with `edit_file`
- overwrite or append files with `write_file` and `append_file`
- apply multi-file unified-diff patches with `apply_patch` (Codex-style `*** Begin Patch / *** Update File / @@ / *** End Patch` envelope; supports `*** Add File`, `*** Update File` with optional `*** Move to:`, and `*** Delete File`)
- inspect git state with `git_status` and `git_diff` (read-only; `git_diff` accepts an optional `path`)
- run shell commands with `shell`

Shell access is intentionally powerful: it runs with the same OS permissions as the app process. The configured workspace is the default cwd, not a hard sandbox.

For Go workspaces, the semantic tools are preferred over raw grep/listing for package and symbol discovery. The local AST index covers overview, symbols, entrypoints, related files, importer lookup, and fallback call analysis. `go_workspace_symbol`, `go_definition`, `go_references`, `go_implementations`, `go_calls_from`, `go_calls_into`, and `go_diagnostics` will use `gopls` when it is installed and fall back to the local Go index or `go build` where possible.

For C/C++ workspaces, the semantic tools provide generic local symbol indexing,
include-graph resolution, caller/callee tracing, body extraction, reverse
include lookup, and blast-radius analysis. When `clangd` is installed,
`cpp_workspace_symbol`, `cpp_definition`, `cpp_declaration`,
`cpp_implementations`, `cpp_references`, and the call-tracing tools will prefer
real LSP queries and fall back to the local C++ index when clangd cannot resolve
the request from the available anchor. `cpp_diagnostics` will use
`clangd --check` when `compile_commands.json` is available in the workspace.

Language `find_symbol` and `read_symbol` tools also accept declaration-shaped patterns such as `type Runner struct`, `class Engine`, `export interface Greeter`, `export declare class ExternalClient`, `pub struct Worker`, or `public class Helper`; idiom normalizes them to symbol names before searching.

### Workspace defaults

- if `workspace.root` is empty, the app defaults it to the directory you launched the harness from
- the bundled scripts preserve that launch directory even though they `cd` into the harness repo first
- if `workspace.cwd` is empty, it defaults to `workspace.root`

### Tool behavior

- main-agent tool activity is shown in the transcript as `Tool` entries
- explore-subagent tool activity stays off the visible transcript and is logged only to the debug JSONL
- tool failures are fed back to the model and also surfaced in the UI error banner
- the current workspace summary is shown in the footer as `workspace: ...`
- file exploration is discovery-first and deterministic: `search_files`, `search_content`, and `list_files` return stable file handles like `f12`
- `read_file` should use those handles; raw path reads are rejected until that exact path has already appeared in a prior tool result
- `discovery_query` writes a compact summary to the tool result and persists the full layered evidence packet under `.idiom/discovery/queries/<id>.json` in the target project; git workspaces are updated via `.git/info/exclude` so these local harness artifacts stay out of status

### Repo-local discovery tool parity

If the workspace contains repo-local discovery scripts, expose them through
`workspace.external_tools` or `workspace.external_tools_dir` when they are part
of that project's workflow. Idiom does not assume project script names or
repository layouts; generic workspace tools remain the default discovery path.

Repo-local discovery commands are available capabilities, not defaults. The
harness biases Qwen toward generic workspace tools first, and project-specific
commands are meant to be used only when the user asks for them or when the repo
workflow clearly calls for them.

This is parity for repo-local shell/discovery commands, not for Codex-only platform tools. Qwen still does not have native access to Codex features like web browsing, sub-agents, MCP connectors, or `apply_patch` semantics unless the harness implements equivalents itself.

### Recommended setup

- put stable global behavior in `prompts/base.md`
- put reusable modes in `prompts/profiles/*.md`
- point `project_root` at the repo you want the harness to reason about
- add a short repo-specific file such as `.qwen.md` in that repo

If `project_root` is set, the app auto-discovers these files in order:

- `.qwen.md`
- `QWEN.md`
- `IDIOM.md`
- `AGENTS.md`

Use `project_prompt_candidates` to replace that auto-discovery list. Set it to
`[]` when you want a compact prompt surface made only from
`project_prompt_files`. You can also add explicit paths with
`project_prompt_files`. Add `CLAUDE.md` to `project_prompt_candidates`
explicitly if a project wants Claude-specific guidance reused. Generated
discovery context such as
`.idiom/discovery/context.md` is not auto-loaded as prompt behavior; use the
context/discovery tools for it, or add it explicitly when you want an
evidence-only prompt section.

## Notes

- The TUI stops the server on exit only if it started that server itself.
- `config.example.json` shows a portable llama.cpp GGUF setup. For a new
  environment, copy it to the config path you plan to use (`config.json` for
  direct `./idiom -config config.json` runs, or `.idiom/config.json` for the
  lane/doctor defaults) and adjust `llama_server.model` plus related model
  paths. In shared repos, keep private model paths and API-key choices out of
  committed config.
- Keep project prompt files short. A distilled `.qwen.md` is usually better than sending a large architecture manual every turn.
- `workspace.enable_writes` and `workspace.enable_shell` can be turned off if you want a read-only or no-exec mode.
