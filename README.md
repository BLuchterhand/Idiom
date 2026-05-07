# Qwen Bubbles

A small Bubble Tea harness for running `Qwen3.6-35B-A3B` locally through `llama.cpp`.

## What it does

- launches or attaches to a local `llama-server`
- streams chat responses into a terminal UI
- saves transcripts to Markdown
- loads baseline behavior from prompt files and profiles every session
- gives Qwen scoped workspace access for reads, search, writes, and shell commands
- keeps the model and cache out of the `Colony` repo

## Layout

- `config.json`: runtime config
- `prompts/`: baseline prompts and behavior profiles
- `models/`: local GGUF weights
- `logs/llama-server.log`: server output
- `logs/conversations/`: per-session JSONL debug logs
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
  --no-tools \
  --validator 'markdownlint "$IDIOM_ARTIFACT"'
```

`artifact` is a generic drafting mode: idiom runs one model turn, writes the
final answer as the artifact, optionally fails if the model used tools, and
optionally runs a project-provided validator with `IDIOM_ARTIFACT` set to the
artifact path. Project-specific rules belong in the prompt and validator, not
in idiom core.

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

Avoid the repo-root `./idiom` binary for development runs after source changes.
It can lag behind the checked-out code and miss newer flags or subcommands. For
source-current runs, prefer `go run ./cmd/idiom <subcommand> <flags>`, or run
`make build` first and then use `bin/idiom` / `~/.local/bin/idiom`.

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
- The shipped config now uses `ctx_size: 196608`; preserved thinking becomes more useful as context grows, and Qwen officially recommends even larger windows for strongest long-horizon behavior.
- The harness now starts `llama-server` with `flash_attention`, `reasoning: auto`, and `reasoning_format: deepseek` by default, so visible responses stay clean while reasoning content remains structured on the server side.

## Inference backends

Each of the three server blocks (`llama_server`, `implementer`, `route_classifier`) accepts a `kind` field that selects the inference backend:

- `"kind": "llama"` (default) — launches `llama-server` from llama.cpp with the existing GGUF model path and llama-specific flags (`jinja`, `flash_attention`, `reasoning*`, `cache-type-*`, etc).
- `"kind": "mlx"` — launches `mlx_lm.server` from [mlx-lm](https://github.com/ml-explore/mlx-lm). On Apple Silicon this typically beats llama.cpp's Metal backend by ~15–30% throughput and ~10% memory. `model` becomes a Hugging Face ID (e.g. `unsloth/Qwen3.6-27B-UD-MLX-4bit`) or a local MLX directory — not a GGUF path. Llama-specific flags (`jinja`, `flash_attention`, `reasoning*`) are ignored, and `chat_template_kwargs` (`enable_thinking`, `preserve_thinking`) is omitted from requests since MLX doesn't understand it.

The blocks are independent, so mixed setups work — for example, main chat on MLX and the route classifier still on llama.cpp. Set `binary` to `mlx_lm.server` (install via `pip install mlx-lm`) and pass any additional flags via `extra_args`.

## Route Classifier

`route_classifier` runs a small structured classifier before execution so terse follow-ups and plan approvals can be reconciled against deterministic routing. It is enabled by default and returns JSON only; host-side control rails still own the final contract and tool permissions.

The app can auto-start the installed local Qwen 9B helper classifier when `auto_start_server` is true. The checked-in local config serves `/Users/ben/models/qwen35-9b/Qwen3.5-9B-Q8_0.gguf` on `http://127.0.0.1:11437` and writes logs to `logs/route-classifier.log`.

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
- `mkdir`, `edit_file`, `write_file`, and `append_file` are rejected
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

For C/C++ workspaces, the semantic tools are deterministic and genericized from Colony's own discovery stack: local symbol indexing, include-graph resolution, caller/callee tracing, body extraction, reverse include lookup, and blast-radius analysis. When `clangd` is installed, `cpp_workspace_symbol`, `cpp_definition`, `cpp_declaration`, `cpp_implementations`, `cpp_references`, and the call-tracing tools will prefer real LSP queries and fall back to the local C++ index when clangd cannot resolve the request from the available anchor. `cpp_diagnostics` will use `clangd --check` when `compile_commands.json` is available in the workspace.

Language `find_symbol` and `read_symbol` tools also accept declaration-shaped patterns such as `type Runner struct`, `class Engine`, `export interface Greeter`, `pub struct Worker`, or `public class Helper`; idiom normalizes them to symbol names before searching.

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

If the workspace contains repo-local discovery scripts, Qwen is now explicitly told to use them through the `shell` tool with their full native CLI flags.

For `Colony`, that means Qwen can use the same repo-local terminal commands Codex uses, including:

- `./scripts/lookup.sh`
- `./scripts/trace_code.sh`
- `./scripts/gdscript_trace.sh`
- `./scripts/boundary.sh`
- `python3 scripts/validate_discovery_tools.py`
- standard shell tools like `rg`, `awk`, `sed`, `git`, and `python3`

These are available capabilities, not defaults. The harness now biases Qwen toward generic workspace tools first, and the repo-local discovery scripts are meant to be used only when the user asks for them or when the repo workflow clearly calls for them.

This is parity for repo-local shell/discovery commands, not for Codex-only platform tools. Qwen still does not have native access to Codex features like web browsing, sub-agents, MCP connectors, or `apply_patch` semantics unless the harness implements equivalents itself.

### Recommended setup

- put stable global behavior in `prompts/base.md`
- put reusable modes in `prompts/profiles/*.md`
- point `project_root` at the repo you want the harness to reason about
- add a short repo-specific file such as `.qwen.md` in that repo

If `project_root` is set, the app auto-discovers these files in order:

- `.qwen.md`
- `QWEN.md`
- `AGENTS.md`
- `CLAUDE.md`

You can also add explicit paths with `project_prompt_files`.

## Notes

- The TUI stops the server on exit only if it started that server itself.
- The default config targets `models/qwen3.6-35b-a3b/Qwen3.6-35B-A3B-Q4_K_M.gguf`.
- If you want a different quantization later, change the `llama_server.model` path in `config.json`.
- Keep project prompt files short. A distilled `.qwen.md` is usually better than sending a large architecture manual every turn.
- `workspace.enable_writes` and `workspace.enable_shell` can be turned off if you want a read-only or no-exec mode.
