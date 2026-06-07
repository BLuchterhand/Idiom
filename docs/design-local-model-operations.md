# Local Model Operations Design

Status: design proposal, drafted 2026-05-12

This is one pillar in the coordinated roadmap:
[idiom-design-roadmap.md](idiom-design-roadmap.md). It supplies the runtime
capacity, readiness, role routing, queue, and context-budget constraints that
all other pillars rely on when local or smaller models are in the loop.

This document defines Idiom's local-model operations pillar. It complements
[idiom-strategic-thesis.md](idiom-strategic-thesis.md), especially Phase 5:
make local-model operation a product strength. It also depends on the
planner-quality context design in
[local-model-planning-quality-design.md](local-model-planning-quality-design.md)
and the Qwen-specific backlog in
[archive/2026-05-historical/qwen-harness-optimizations.md](archive/2026-05-historical/qwen-harness-optimizations.md).

The thesis-level rule remains unchanged:

- The model owns strategy.
- The harness owns contracts.

Local-model operations is the pillar that makes that rule practical on machines
where inference is memory-bound, context is expensive, helper models can fail,
and small/local models need more structure than frontier cloud models.

## Executive Summary

Idiom should treat local inference as an operational system, not just a model
URL. The harness must know which backend process is expected to serve each role,
how much memory and context that role can consume, when a route-classifier or
executor is degraded, whether the queue daemon is protecting scarce GPU/Metal
resources, and which readiness checks prove that a lane can run now.

The desired product promise:

> A developer can run bounded coding-agent work on local Qwen-class models with
> predictable setup, explicit role routing, bounded concurrency, visible
> context budgets, actionable health checks, and logs that explain failures
> without hand-reconstructing every run.

Near-term design choices:

- Keep the current `llama.cpp`, MLX-LM, and Ollama-compatible posture, but
  normalize them behind a role-aware backend contract.
- Make queue admission a first-class readiness signal, not an optional footnote.
- Separate model roles: planner, executor/coder, route classifier, summarizer,
  verifier/critic, and embedding/ranker where applicable.
- Budget prompt context, output tokens, thinking traces, tool output, and KV
  cache as one capacity model.
- Prefer compact evidence packets and zoom handles over raw file flooding.
- Use telemetry, `harness_audit`, and `idiom doctor` to expose health, not to
  prescribe the model's next strategic action.

## Target User And System

Primary user:

- A developer running Idiom locally against one or more real repositories.
- The developer may also use Codex, Claude Code, OpenCode, Aider, or another
  agent, with Idiom acting as a local context/audit/verification sidecar.
- The developer values privacy, inspectability, offline or low-cloud operation,
  and reproducibility more than maximal frontier-model reasoning.

Primary machine shape:

- Apple Silicon with MLX-LM for the main local Qwen stack.
- Linux or macOS with `llama.cpp` / `llama-server` and GGUF models.
- Ollama-compatible environments used either directly or as a familiar local
  model manager.
- One machine may serve multiple repos and multiple agents concurrently.

Primary task shape:

- Bounded software-engineering work: plan, implement, verify, audit, explain,
  and debug failures.
- Multi-file tasks should already be decomposed into reasonable atoms. Current
  Qwen workflow notes identify small, single-purpose atoms as the dominant
  predictor of success; large briefs with many files or hundreds of lines are
  operationally different work, not just "more of the same."

System success is not "a local model always behaves like Opus." Success is that
Idiom makes local models safe and useful by owning setup, contracts, memory,
queues, routing, telemetry, and readiness.

## Current Idiom Baseline

Verified from the current repo docs and code:

- `README.md` documents three inference backend blocks:
  `llama_server`, `implementer`, and `route_classifier`.
- `internal/platform/config.Config` models those as `LlamaServer`, optional
  `Implementer`, and `RouteClassifier`.
- `.idiom/backends.json` declares role-aware backend profiles. The current
  self-hosted `qwen-pair` profile uses a Qwen 35B planner-classifier plus a
  Qwen3.6-27B executor, with a proxy on port `47680`.
- `internal/runtime/backends` owns `.idiom/backends.json`, role alias resolution,
  backend launch/stop/status, and an alias-routing proxy.
- `cmd/idiom/backend.go` exposes `idiom backend start|stop|status|list|proxy`.
- `cmd/idiom/queue.go` and `internal/runtime/runner` expose a per-user Unix-socket
  queue daemon with named pools, leases, waiting positions, status, ping,
  cancel, and idle flags.
- `runner.MaybeAcquire` is opportunistic: if the daemon is absent, lane entry
  points proceed ungated; if a daemon is reachable, they acquire the resolved
  pool.
- `internal/surfaces/tui/app/turn_planner.go` uses the route classifier only after
  deterministic routing is not decisive. It falls back to host routing if the
  classifier is disabled, unhealthy, times out, or returns invalid JSON.
- `internal/surfaces/tui/app/history.go` also uses the route-classifier client for history
  summarization and can lazily launch the configured helper server.
- `cmd/idiom/doctor.go` currently checks for backend binaries, model caches,
  project config, writable workspace, `python3`, and `git`.
- `internal/evidence/telemetry` writes local JSONL process telemetry under
  `logs/telemetry` by default and supports `IDIOM_TELEMETRY=0`.
- Conversation debug logs and `harness_audit` provide the richer behavior trail.

That baseline is already close to the target shape. The gap is making it
coherent enough that users and external agents can reason about local readiness
before they burn a 20-minute local run.

## External Basis

Local serving backends expose different controls, but they point to the same
operational truth: context, concurrency, and KV cache are scarce resources.

- `llama.cpp` is designed for local/cloud inference across many backends,
  including Apple Metal, CUDA, HIP, Vulkan, CPU, hybrid CPU/GPU, and integer
  quantization from roughly 1.5 to 8 bits. See
  <https://github.com/ggml-org/llama.cpp>.
- `llama-server` exposes server controls such as context size, chat templates,
  reasoning/thinking handling, and OpenAI-compatible function calling. See
  <https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md>.
- `llama-quantize` documents that quantization shrinks model size and can speed
  inference, but may reduce accuracy; it recommends importance-matrix-based
  quantization for quality. See
  <https://github.com/ggml-org/llama.cpp/blob/master/tools/quantize/README.md>.
- MLX-LM targets Apple Silicon, supports Hugging Face models, conversion,
  quantization, prompt caching, and a rotating fixed-size KV cache. Smaller
  `--max-kv-size` values use less memory with worse quality; larger values use
  more memory with better quality. See
  <https://github.com/ml-explore/mlx-lm#long-prompts-and-generations>.
- `mlx_lm.server` intentionally offers an OpenAI-like HTTP API, but its docs
  say it is not recommended for production because it only implements basic
  security checks. Idiom must therefore provide the operational wrapper, not
  treat the server as a complete production boundary. See
  <https://github.com/ml-explore/mlx-lm/blob/main/mlx_lm/SERVER.md>.
- Ollama documents that larger context lengths increase memory requirements,
  defaults context by VRAM tier, and recommends at least 64k tokens for agents
  and coding tools. See <https://docs.ollama.com/context-length>.
- Ollama's FAQ makes the concurrency hazard explicit: parallel requests multiply
  context memory, `OLLAMA_NUM_PARALLEL * OLLAMA_CONTEXT_LENGTH` drives RAM, and
  `OLLAMA_MAX_QUEUE` controls overload rejection. It also documents Flash
  Attention and KV-cache quantization tradeoffs. See
  <https://docs.ollama.com/faq>.
- Qwen3.6-35B-A3B's model card shows 262,144-token serving examples and
  separate tool-call parser guidance for SGLang/vLLM. See
  <https://huggingface.co/Qwen/Qwen3.6-35B-A3B>.
- Qwen3.6-27B is a 27B language model with native 262,144-token context,
  built-in MTP weights, and model-card guidance to keep at least 128k context
  for complex tasks when memory allows. See
  <https://huggingface.co/Qwen/Qwen3.6-27B>.
- "Lost in the Middle" shows that long-context models do not robustly use all
  positions in large prompts; relevant information in the middle can be used
  worse than information at the beginning or end. See
  <https://arxiv.org/abs/2307.03172>.
- SWE-agent shows that the agent-computer interface materially affects software
  engineering performance, including file editing, repository navigation, and
  test execution. See
  <https://proceedings.neurips.cc/paper_files/paper/2024/hash/5a7c947568c1b1328ccc5230172e1e7c-Abstract-Conference.html>.

Frontier coding tools also converge on host-owned operational contracts:

- Codex documents sandbox mode and approval policy as separate layers:
  technical access boundaries plus approval decisions. Codex CLI defaults
  include workspace-limited writes and no network access, with approvals for
  leaving the sandbox or using networked commands. See
  <https://developers.openai.com/codex/agent-approvals-security#sandbox-and-approvals>.
- Codex `config.toml` exposes `model_context_window`,
  `model_auto_compact_token_limit`, `tool_output_token_limit`,
  `background_terminal_max_timeout`, agent thread limits, sandbox settings,
  approval policies, and local provider support. See
  <https://developers.openai.com/codex/config-reference#configtoml>.
- Codex slash commands include `/compact`, `/status`, `/permissions`,
  `/plan`, `/review`, and `/ps`, which is a useful reference shape for making
  budgets, permissions, and background work visible. See
  <https://developers.openai.com/codex/cli/slash-commands#built-in-slash-commands>.
- Claude Code documents deny/ask/allow permission rules, deny-first
  precedence, hooks, working-directory access, and sandboxing as a complementary
  OS-level layer for Bash commands. See
  <https://code.claude.com/docs/en/permissions>.
- Claude Code cost guidance explicitly treats context size, compaction,
  subagent isolation, model choice, hooks, skills, and thinking-token budgets
  as operational cost controls. See <https://code.claude.com/docs/en/costs>.
- Claude Code's context-window docs show startup instructions, file reads,
  hooks, subagent summaries, and compaction as context lifecycle events. See
  <https://code.claude.com/docs/en/context-window>.
- OpenCode agents expose per-agent permissions with `ask`, `allow`, and `deny`
  for read, edit, grep, bash, task, external-directory, LSP, skills, web, and
  stuck-loop recovery. See <https://opencode.ai/docs/agents/>.

The design implication for Idiom: local operations needs a typed contract that
combines backend capacity, model role, queue admission, context budget,
permissions, and evidence readiness. Prompt reminders cannot replace that.

## Local And Smaller Model Constraints

Local models fail differently from frontier cloud agents. The harness should
assume these constraints and design for them directly.

### Model Behavior Constraints

- Weaker long-horizon state retention. The model may lose the thread after many
  tool calls even if the nominal context window is large.
- Higher prompt-bloat sensitivity. A 256k window does not mean all 256k tokens
  are equally usable, and long prompts increase prefill cost.
- Weaker self-critique. Qwen workflow notes record that local self-review misses
  issues a second agent or deterministic audit catches.
- Tool-call fragility. Local models are more likely to emit malformed JSON,
  extra prose, wrong field names, or tool calls after readiness.
- Over-exploration under uncertainty. When evidence is thin, local models can
  loop over reads/searches rather than naming one exact missing claim.
- Thinking/output tradeoff. Preserved reasoning can improve planning but
  consumes context; unbounded thinking can starve final structured output.

### Runtime Constraints

- Model weights, KV cache, prompt cache, and concurrent requests all compete for
  the same local memory pool.
- Context length is a memory allocation, not a free quality switch.
- Parallel lanes can OOM or wedge a backend even when each lane works alone.
- Backend readiness is not only "is port open"; role routing, model path,
  sampler settings, prompt-cache budget, and per-request capabilities also
  matter.
- Different backends expose incompatible knobs. `llama.cpp` has `--ctx-size`
  and KV cache controls; MLX-LM has prompt cache and rotating KV cache controls;
  Ollama wraps model and server settings differently.
- Local helper models can be slower or less reliable than deterministic routing.
  A route classifier must be optional and health-scored.

### Harness Response

- Use deterministic contracts for permissions, protected paths, required edit
  paths, schemas, readiness, verification, queue admission, and budgets.
- Use model-readable nudges for strategy only when a safe path remains open.
- Stop or degrade visibly when capacity is exhausted.
- Prefer a smaller, structured evidence packet over a larger raw prompt.
- Keep every local-model operational fact visible in telemetry and doctor
  output.

## Design Principles

1. Local capacity is a contract.
   If a lane can OOM the machine, the lane is not ready, even if the model URL
   responds.

2. Role routing must be explicit.
   Planner, executor, classifier, summarizer, and verifier are separate roles
   even when one physical model serves several of them.

3. Queueing is admission control, not convenience.
   The queue daemon protects local inference pools from oversubscription.

4. Context is a budgeted artifact.
   Prompt history, context packets, source excerpts, tool output, thinking, and
   output tokens all draw from one operational budget.

5. Health checks should name remedies.
   A failed doctor check should say what is wrong, which role is affected, and
   the exact next operator action.

6. Degraded mode is valid only when explicit.
   If the route classifier is down, use deterministic routing and log the
   degradation. If the queue daemon is absent, say the run is ungated.

7. Observability is part of the product.
   Logs should answer: what model served this role, what budget was active, why
   did the run stop, what queue slot was held, and which verification was
   accepted or missing?

## Backend Architecture

### Current Components

```text
.idiom/backends.json
  -> internal/runtime/backends.File/Profile/Model/Proxy
  -> idiom backend start|stop|status|proxy
  -> role-specific base URLs
  -> app lifecycle / lane entry points
  -> agent runner

config.json / .idiom/config.json
  -> llama_server
  -> implementer
  -> route_classifier
  -> chat and workspace budgets

idiom queue serve
  -> named pool caps
  -> MaybeAcquire from implement / plan-execute / queue run
```

The design should preserve this split:

- `config.json` describes baseline harness behavior and fallback servers.
- `.idiom/backends.json` describes local model stacks and role routing.
- `idiom backend` manages backend processes.
- `idiom queue` manages scarce local execution slots.
- `internal/surfaces/tui/app` resolves role-specific clients for the active run.
- `internal/runtime/agent` enforces runtime contracts and emits budget/stop telemetry.

### Backend Profile Contract

`backends.json` should become the canonical local operations manifest for model
roles. Each model entry should remain small and inspectable:

```json
{
  "alias": "qwen3.6-27b",
  "kind": "mlx",
  "binary": "/Users/ben/scripts/vmlx-idiom-launch.sh",
  "model_path": "/Users/ben/models/qwen3.6-27b-mlx-6bit",
  "host": "127.0.0.1",
  "port": 47783,
  "cache_size": 4,
  "cache_bytes": 12884901888,
  "max_tokens": 8192,
  "extra_args": [
    "--temp", "0.6",
    "--top-p", "0.95",
    "--top-k", "20",
    "--chat-template-args", "{\"enable_thinking\":true,\"preserve_thinking\":true}",
    "--prefill-step-size", "1024",
    "--max-prompt-tokens", "131072",
    "--use-paged-cache",
    "--enable-disk-cache",
    "--enable-pld",
    "--enable-jit"
  ],
  "roles": ["executor"],
  "queue_pool": "qwen-coder",
  "context_tokens": 262144,
  "recommended_context_tokens": 131072,
  "quantization": "mlx-6bit",
  "health": {
    "startup_timeout_seconds": 120,
    "request_timeout_seconds": 900
  }
}
```

New fields are intentionally descriptive. The current implementation can keep
using the existing fields first, but doctor/readiness should eventually report:

- alias
- role set
- backend kind
- model path or model id
- port/base URL
- model context capability
- configured working context
- output token cap
- prompt/KV cache budget
- acceleration settings: paged/prefix cache, PLD speculative acceleration,
  JIT, and whether explicit KV quantization is absent so vMLX can use its
  TurboQuant auto mode where compatible
- quantization family
- queue pool
- readiness probe outcome
- last known error or degraded mode

`qwen-pair` currently runs both local models through the vMLX wrapper. The
wrapper translates the common `mlx_lm.server` flags from `.idiom/backends.json`
to vMLX flags and appends Qwen reasoning/tool parser auto-detection. The profile
enables paged cache, disk cache, PLD speculative acceleration, JIT, 128k prompt
admission, and a smaller 1024-token prefill step to reduce Metal peak-memory
spikes on long prompts. It intentionally does not pass an explicit
`--kv-cache-quantization` value: vMLX's documented default is TurboQuant KV for
compatible models plus q4 stored-prefix fallback, while an explicit value opts
out of that calibrated auto path. MTP is present in the Qwen3.6 artifacts, but
the current MLX/vMLX server surface does not expose a Qwen3.6 MTP decoding flag;
enabling it would require a serving backend with Qwen's MTP speculative
configuration rather than a harmless config-file toggle.

### Backend Kind Adapter

The role-aware backend abstraction should hide backend-specific flags from
callers while preserving them for operators.

| Backend kind | Operational strengths | Harness responsibilities |
| --- | --- | --- |
| `mlx` | Apple Silicon throughput, MLX quantized models, prompt cache, request-level chat-template args in current Idiom usage. | Guard memory, expose cache budgets, report MLX server's non-production security note, validate model directory, detect unsupported flags. |
| `llama` | GGUF portability, explicit context size, broad hardware backends, KV cache quantization, reasoning/chat-template flags. | Normalize `--ctx-size`, KV cache type, reasoning parser, Jinja/tool-call support, PID/log lifecycle. |
| `ollama` | Familiar local model management, model preload/keepalive, app settings, server env knobs. | Treat context/concurrency/env settings as readiness inputs, detect overload/503, avoid hidden parallel-context multiplication. |
| `openai_compatible` | Any local or LAN endpoint implementing `/v1/chat/completions`. | Mark as externally managed; doctor can probe but should not start/stop. Require explicit permissions for non-loopback. |

Do not leak these choices into model-facing prompts. The model needs role and
capability facts; the operator needs backend knobs.

### Process Lifecycle

`idiom backend start` should remain conservative:

- Validate profile and role aliases before spawning.
- Refuse empty binary, empty model path for local model kinds, invalid port, or
  duplicate port in one profile.
- Start every declared model and report every failure, not first failure only.
- Write per-alias PID files under `.idiom/run`.
- Treat "port already in use" as a warning with enough data to identify the
  process, not as silent success.
- Probe both TCP readiness and a minimal model endpoint when the backend speaks
  an OpenAI-compatible API.
- Stamp backend start/status events into telemetry.

`idiom backend stop` should preserve the existing PID-file-first behavior:

- Prefer the recorded PID over `lsof` so a reused port does not kill an
  unrelated process.
- SIGTERM, wait, then SIGKILL only after re-checking liveness.
- Remove stale PID files.
- Report per-model outcomes.

`idiom backend proxy` should stay a role/alias router, not become a scheduler.
Scheduling belongs to the queue daemon.

## Model Roles

Local-model reliability improves when roles are explicit. One physical model may
serve multiple roles, but the contract should still name the role.

| Role | Primary responsibility | Recommended model shape | Failure mode | Degraded behavior |
| --- | --- | --- | --- | --- |
| Planner | Build strategy from packets, constraints, and evidence. | Strong reasoning local model, thinking enabled when available. | Overlong plan, unsupported claims, reasoning consumes output. | Smaller packet, lower output cap only if plan audit remains possible. |
| Executor / coder | Apply bounded edits and run verification. | Coding-tuned model, non-thinking or low-thinking, reliable tool calls. | Malformed tool args, broad reads, incomplete writes. | Narrow required paths, exact anchors, deterministic gate feedback. |
| Route classifier | Classify ambiguous turns before execution. | Small/fast JSON-only model or shared planner with low max tokens. | Timeout, invalid JSON, wrong implementation route. | Deterministic route fallback with telemetry event. |
| Summarizer | Compact history/log output into short evidence. | Small model or deterministic summarizer. | Drops key facts or invents. | Deterministic truncation with explicit uncertainty. |
| Verifier / critic | Review plan or patch against packet/test evidence. | Same or smaller local model behind deterministic audit. | False confidence. | Deterministic audit remains source of truth; model critique is advisory. |
| Embedding/ranker | Rank files, traces, and prior evidence. | Embedding model or deterministic graph ranker. | Hub inflation, stale facts. | Confidence tiers and freshness warnings. |

Role defaults should be resolved in this order:

1. Explicit proxy `default_<role>`.
2. First model tagged with that role.
3. Lone-model fallback for single-model profiles.
4. Executor-only legacy `coder` alias heuristic.
5. Fallback to `config.json` server block.

This matches the current `Profile.AliasForRole` direction while making the
operator-facing contract clearer.

## Queue And OOM Controls

### Problem

Local inference fails by saturation before it fails by clean errors. Two lanes
can each be valid in isolation and still crash or wedge the shared backend when
run together. Ollama's docs make the same issue explicit: parallel requests
multiply context memory, and queue length must be bounded
(<https://docs.ollama.com/faq>).

### Queue Contract

`idiom queue serve` should be the standard admission-control surface for local
model work:

- One daemon per user/machine.
- One Unix socket with `0600` permissions.
- Named pools for scarce resources, usually one per backend memory pool:
  `qwen-35b`, `qwen-27b`, `qwen-coder`, or `default`.
- Each pool has `max_in_flight`, waiting queue, in-flight list, and idle
  threshold.
- A lease is held by an open connection. Disconnect releases it.
- Waiting jobs can be canceled. In-flight jobs are not killed by the daemon;
  the holder process owns cancellation.
- Nested acquires are skipped when an ancestor already holds any pool, avoiding
  same-pool self-deadlock and cross-pool dining-philosopher deadlocks.

### Required Enhancements

Make queue health visible at lane start:

```json
{
  "queue": {
    "required": false,
    "socket": "~/.idiom/runner.sock",
    "reachable": true,
    "pool": "qwen-coder",
    "max_in_flight": 1,
    "position": 0,
    "lease_job_id": "j-1778620000000-1",
    "held_pools": ["qwen-coder"]
  }
}
```

Add pool capacity metadata:

- `memory_class`: `mlx-metal`, `cuda-vram`, `cpu-ram`, `ollama`, `remote`.
- `model_aliases`: list of aliases the pool protects.
- `max_context_tokens`: capacity assumption used by doctor.
- `max_parallel_requests`: expected backend parallelism.
- `idle_threshold_seconds`: existing idle flag threshold.
- `hard_required`: whether a lane must fail if the daemon is absent.

Queue readiness should have three modes:

- `ungated`: daemon missing; proceed with visible warning.
- `gated`: daemon reachable and lease acquired.
- `required_but_missing`: fail before starting inference.

Default developer runs can stay `ungated` on daemon absence for compatibility.
CI, eval, and multi-agent scripts should use required gating.

### OOM Prevention Rules

- Never infer safe concurrency from CPU count.
- Treat `context_tokens * parallel_requests` as a memory multiplier.
- Reserve memory for the OS, editor, shell, and tool subprocesses.
- Prefer one in-flight large planner per machine unless explicitly proven safe.
- Executor and planner pools should be separate only when they do not contend
  for the same physical memory pressure.
- If a nested command needs a different pool, wrap the outermost command in the
  pool used by the inner inference path or skip nested acquisition as current
  code does.
- Queue status should flag in-flight jobs older than the idle threshold because
  local-model wedges are often "still running" from the process table's point
  of view.

## Route Classifier Health

The route classifier is useful, but it must never become the hidden source of
truth for permissions or strategy.

Current behavior is aligned:

- Deterministic routing runs first.
- Decisive deterministic routes skip the classifier.
- Classifier calls have a short timeout.
- Invalid JSON or call failure falls back to deterministic routing.
- Host-side control rails still own final permissions and contracts.

Required health model:

```json
{
  "route_classifier": {
    "enabled": true,
    "always_on": true,
    "role_alias": "qwen3.6-35b",
    "base_url": "http://127.0.0.1:47780",
    "timeout_seconds": 20,
    "last_probe": "pass",
    "last_degraded_reason": "",
    "json_valid_rate_1h": 0.98,
    "fallback_rate_1h": 0.04,
    "p95_latency_ms_1h": 1800
  }
}
```

Classifier readiness checks:

- Endpoint reachable.
- Model responds within timeout.
- JSON-only probe parses into the route-plan schema.
- Required enum fields normalize successfully.
- Role alias matches the active backend profile.
- The classifier is not accidentally using the executor-only model unless the
  profile explicitly says so.

Classifier telemetry events:

- `route_classifier_probe`
- `route_classifier_call`
- `route_classifier_invalid_json`
- `route_classifier_timeout`
- `route_classifier_fallback`
- `route_classifier_disagreement` when deterministic and model routes conflict
  and correction logic chooses one.

Health policy:

- One failure: degrade for the current turn and log.
- Repeated failures in a session: suppress further classifier calls for that
  session and log `route_classifier_disabled_for_session`.
- Doctor failure: warn, not fail, unless a lane declares classifier-required.

## Prompt And Context Budget Strategy

Large local windows are useful, but they are not the product. Idiom's product is
budgeted evidence.

### Budget Surfaces

Track these categories per model call:

- System/developer/project instructions.
- Runtime brief and lane contract.
- User task.
- Conversation history.
- Context pack / planning packet.
- Exact source excerpts.
- Tool results.
- Diagnostics/test output.
- Preserved thinking traces.
- Reserved output tokens.
- Reserved thinking tokens if the backend supports a thinking budget.
- Tool schema overhead.

Expose the ledger in telemetry and optionally in `/context` or lane status:

```json
{
  "context_budget": {
    "model_alias": "qwen3.6-35b",
    "model_context_tokens": 262144,
    "configured_context_tokens": 65536,
    "estimated_input_tokens": 42000,
    "reserved_output_tokens": 12000,
    "reserved_reasoning_tokens": 8000,
    "tool_output_tokens": 6400,
    "risk": "moderate",
    "warnings": [
      "conversation history over 25 percent of budget",
      "two source excerpts overlap by more than 70 percent"
    ]
  }
}
```

### Budget Rules

- Treat 64k as a practical default coding-agent context target unless the
  backend profile proves more headroom and the task merits it. This matches
  Ollama's own coding-agent guidance for at least 64k
  (<https://docs.ollama.com/context-length>) while avoiding casual 256k dumps.
- Use `planning_v2` packet tiers: `brief`, `standard`, `deep`, `max-local`.
- Reserve output first. A plan that cannot emit `submit_plan` or an executor
  that cannot emit the final patch has already failed.
- For thinking models, make thinking preservation role-specific:
  - Planner: enable/preserve thinking when the task is complex and budget
    permits.
  - Executor: prefer non-thinking or reduced-thinking when structured tool
    calls and concise patches matter more.
  - Classifier: thinking off, low max tokens, strict JSON.
- Tool results must be summarized deterministically before entering long-lived
  history. Raw output can stay in logs and be zoomed when needed.
- Critical evidence should be placed near the beginning or end of the packet,
  not buried arbitrarily in the middle, because long-context retrieval is
  position-sensitive (<https://arxiv.org/abs/2307.03172>).
- Preserved thinking is not evidence. It can help model continuity, but
  readiness and audit must rely on source, tool output, tests, logs, and typed
  contracts.

### Compaction Policy

Compaction should be typed, not a prose-only summary:

- Keep current task, required paths, protected paths, verification obligations,
  changed files, open blockers, and final-answer contract.
- Keep source evidence as handles plus exact excerpts only where needed.
- Drop duplicate broad reads and stale search output.
- Preserve failed verification commands with exit status, timestamp, and git
  fingerprint.
- Mark claims as `verified`, `inferred`, `stale`, or `unknown`.

Claude Code and Codex both expose compaction/status controls as user-visible
operational surfaces (<https://code.claude.com/docs/en/context-window>,
<https://developers.openai.com/codex/cli/slash-commands#built-in-slash-commands>).
Idiom should do the same for local context pressure.

## Telemetry And Audit

Local operations needs two complementary evidence trails:

1. Process telemetry: small, redacted, always-on by default.
2. Conversation/debug logs: richer, redacted by default, full mode available
   for local debugging.

### Required Process Telemetry

Extend existing `logs/telemetry/*.jsonl` with:

- `backend_profile_resolved`
- `backend_start_attempt`
- `backend_ready`
- `backend_degraded`
- `backend_stop_attempt`
- `queue_probe`
- `queue_acquire_wait`
- `queue_acquire_granted`
- `queue_ungated`
- `route_classifier_probe`
- `route_classifier_fallback`
- `context_budget`
- `model_call_start`
- `model_call_end`
- `model_call_timeout`
- `budget_ledger`
- `doctor_check`
- `readiness_profile`

Fields should include:

- session id
- lane id
- backend profile
- role
- model alias
- backend kind
- base URL host/port, redacted if non-local
- queue pool and job id
- prompt/completion token counts when available
- max tokens and request timeout
- context budget estimate
- stop reason
- degraded reason
- version/build stamp

Do not log raw prompts, raw tool output, secrets, full file bodies, or command
payloads in process telemetry.

### Conversation Logs

Conversation logs should keep their existing role as the primary evidence trail
for behavior debugging. Local operations should add enough typed records that
`harness_audit` can answer:

- Did the route classifier run?
- Was the run gated by queue?
- Which backend role served each model call?
- Which context budget tier was used?
- Did compaction occur?
- Did a backend timeout, stream idle timeout, or OOM-like disconnect happen?
- Was verification accepted, blocked, or missing?
- Was final-answer readiness rejected?

### Audit Reports

Add a local-model operations purpose:

```bash
idiom tool harness_audit --input '{"purpose":"local_model_operations"}'
```

It should summarize:

- backend profile/schema drift
- missing model files
- queue daemon absent/reachable
- pool saturation and idle jobs
- classifier fallback rate
- model-call timeout rate
- average tokens per role
- context budget risk distribution
- OOM-like backend disconnects
- malformed tool-call rate by model alias
- verification outcomes after writes
- stop reasons by lane/model/profile

The audit should produce findings and evidence, not strategy prescriptions.

## Doctor And Readiness Checks

`idiom doctor` should grow from generic host checks into a lane-readiness
profile.

### Current Checks To Keep

- Supported backend runner on `PATH`.
- Local model files in known caches.
- Project config exists and is valid JSON.
- Workspace root is writable.
- `python3` availability.
- `git` availability.

### New Local-Model Checks

Backend profile:

- `.idiom/backends.json` exists, parses, and has a selected/default profile.
- Every model has alias, kind, binary, model path/model id, port, and roles.
- No duplicate aliases or duplicate ports within a profile.
- Every declared local model path is readable.
- Every local binary exists and is executable.
- Proxy defaults resolve to real aliases.
- Planner, executor, and classifier roles resolve for `plan-execute` and
  `implement`.

Backend process readiness:

- Role endpoints are reachable or explicitly externally managed.
- `/v1/models` or equivalent probe responds.
- A tiny chat completion succeeds for each role where safe.
- Configured request timeout is larger than expected local prefill time.
- Backend log file path is writable.

Queue readiness:

- Socket path is known.
- Daemon reachable or queue is explicitly optional.
- Required pools exist.
- Pool caps are sane for the active backend profile.
- Pool idle thresholds are configured.
- `queue status --json` includes no stale in-flight jobs past the threshold
  unless acknowledged.

Route classifier:

- Enabled/disabled state.
- Base URL resolves from backends profile when applicable.
- JSON probe passes.
- Timeout is short relative to main model request timeout.
- Fallback mode is recorded.

Context/model capacity:

- Configured context is no larger than the model/backend can support.
- Recommended working context is set.
- Output max tokens per lane are sufficient for planner/executor artifacts.
- Prompt/KV cache settings are visible.
- Quantization is declared or inferred from model path where possible.

Language/workspace readiness:

- `context_status` freshness.
- Key language backends available or reported as gaps.
- Required verification commands from config/project docs are executable-looking.
- `harness_audit` can read telemetry and conversation logs.

### Readiness Profiles

`idiom doctor --profile local-model-ops` should emit:

```json
{
  "status": "warn",
  "lanes": {
    "plan-execute": {
      "status": "pass",
      "planner": "qwen3.6-35b",
      "executor": "qwen3.6-27b",
      "queue": "gated",
      "classifier": "degraded"
    },
    "implement": {
      "status": "pass",
      "executor": "qwen3.6-27b",
      "queue": "gated"
    },
    "audit": {
      "status": "warn",
      "reason": "harness_audit can read logs but classifier health is degraded"
    }
  },
  "checks": [
    {
      "name": "queue pool qwen-coder",
      "status": "pass",
      "detail": "reachable, max_in_flight=1"
    },
    {
      "name": "route classifier JSON probe",
      "status": "warn",
      "detail": "timeout after 20s",
      "remedy": "run `idiom backend status --profile qwen-pair` and inspect logs/route-classifier.log"
    }
  ]
}
```

P6.3 readiness gates are explicit policy decisions over the underlying
readiness facts. The profile report distinguishes `pass`, `degraded`, and
`blocked` with machine-readable reasons and remedies:

```json
{
  "name": "implement",
  "state": "blocked",
  "enforcement": "report_only",
  "required_for_launch": true,
  "gates": [
    {
      "name": "backend_roles_resolved",
      "status": "blocked",
      "severity": "hard",
      "enforcement": "report_only",
      "required_for_launch": true,
      "evidence": "backend role config",
      "reason": ".idiom/backends.json not found",
      "remedy": "create .idiom/backends.json to make role readiness machine-readable"
    },
    {
      "name": "queue_admission_known",
      "status": "degraded",
      "severity": "soft",
      "enforcement": "report_only",
      "required_for_launch": false,
      "evidence": "queue admission",
      "reason": "queue daemon not reachable",
      "remedy": "start the daemon with `idiom queue serve` for live queue capacity evidence"
    }
  ],
  "hard_failures": [
    {
      "name": "backend_roles_resolved",
      "status": "blocked",
      "severity": "hard"
    }
  ],
  "soft_degradations": [
    {
      "name": "queue_admission_known",
      "status": "degraded",
      "severity": "soft"
    }
  ]
}
```

Policy is profile-shaped, not client-shaped:

- `backend_roles_resolved` is hard for `plan`, `implement`, `eval`, and
  `sidecar`, and soft for `discovery`.
- `queue_admission_known` is hard for `eval` because repeatable evaluation
  should not silently saturate local inference, and soft for `discovery`,
  `plan`, `implement`, and `sidecar` while current lane-start queue plumbing is
  opportunistic.
- `context_budget_valid` is hard for `plan`, `implement`, and `eval`, and soft
  for `discovery` and `sidecar`.
- `route_classifier_available_or_fallback_recorded` is soft for every profile;
  deterministic routing fallback remains visible instead of becoming a hidden
  strategy choice.

Until P6.4/P6.5 wire equivalent lane-start enforcement and parity tests, the
doctor policy stays `enforcement:"report_only"`. The harness reports unsafe or
degraded starts before launch, but P6.3 does not claim broad runtime blocking
that the lane runner cannot yet prove. Gate telemetry and exported evidence
must remain metadata-only: profile name, status, counts, queue/context/backend
states, and remedies, with no raw prompts, file bodies, secrets, or
customer-identifying payloads.

Human output should be the same information with exact remedy lines.

## Benchmarks

Local-model operations benchmarks should measure operational reliability, not
only final patch correctness.

### Fixed Smoke Suite

Run on every supported local backend profile:

- Direct grounded answer from a small context packet.
- Locate-only task.
- One-file docs edit.
- One-file Go edit with focused test.
- Planner/executor handoff using `planning_v2`.
- Route-classifier ambiguous follow-up.
- Long log summarization.
- Queue contention with two waiting jobs.

Metrics:

- wall time
- time to first token
- prefill tokens/sec if available
- decode tokens/sec if available
- prompt tokens
- completion tokens
- model calls
- tool calls
- malformed tool calls
- classifier fallback count
- queue wait time
- stop reason
- verification outcome
- max resident memory when available
- backend disconnect/OOM-like events

### Capacity Sweep

For each profile:

- Context sizes: 8k, 32k, 64k, 128k, 256k where supported.
- Queue caps: 1 and any explicitly supported higher cap.
- KV cache: backend-supported values, for example f16/q8/q4 for Ollama or
  llama.cpp where configured.
- Prompt cache on/off where supported.
- Thinking preserved/on/off by role.

Outcomes:

- pass/fail
- latency and throughput
- quality proxy from deterministic audit
- context-budget warnings
- OOM or timeout threshold
- recommended default for that machine

### Replay Suite

Use real logs:

- Successful bounded implementation runs.
- Failed route-classifier runs.
- Budget-exhausted runs.
- Queue deadlock/idle incidents.
- Backend timeout incidents.
- Large-atom Qwen wedges from the Qwen backlog.

Replay should compare:

- ungated vs gated
- no packet vs `context_pack`
- preserved thinking vs non-thinking role defaults
- current packet budget vs compacted packet budget
- classifier on vs deterministic fallback

Success is a measurable reduction in:

- broad reads before first edit
- repeated tool calls
- malformed tool calls
- unsupported plan claims
- stale facts
- route fallback surprises
- budget-exhausted endings
- backend/process wedges

## Rollout Plan

### Phase 0: Document And Align

- Land this design doc.
- Tag local-model operations as a thesis pillar in future work briefs.
- Keep existing behavior unchanged.

Exit criteria:

- The design is referenced by local-model backend/queue/classifier work.

### Phase 1: Readiness Profile

- Extend `idiom doctor` with `--profile local-model-ops`.
- Add role resolution checks against `.idiom/backends.json`.
- Add queue reachability/pool checks.
- Add classifier JSON probe.
- Add JSON schema for doctor output.

Exit criteria:

- A new machine can run one command and see which local-model lane is ready,
  degraded, or blocked.

### Phase 2: Telemetry And Audit

- Emit backend role, queue, classifier, and context-budget telemetry.
- Add `harness_audit --purpose local_model_operations`.
- Add audit findings for ungated runs, classifier fallback rate, idle queue
  jobs, and backend timeout/OOM-like disconnects.

Exit criteria:

- A failed local run can be classified without manually reading raw prose logs.

### Phase 3: Backend Manifest Hardening

- Add optional `queue_pool`, `context_tokens`, `recommended_context_tokens`,
  `quantization`, and health fields to `backends.json`.
- Validate duplicate ports/aliases and unresolved role defaults.
- Report endpoint model identity where backends expose it.
- Make externally managed endpoints explicit.

Exit criteria:

- Role routing, capacity assumptions, and operator remedies are visible before a
  lane starts.

### Phase 4: Budgeted Context Runtime

- Add per-call context budget ledger.
- Tie `planning_v2` packet tiers to model role/profile.
- Add warnings for excessive history, duplicate excerpts, stale summaries, and
  insufficient reserved output.
- Make compaction typed and auditable.

Exit criteria:

- Local runs can explain context pressure and stop before output starvation.

### Phase 5: Benchmark Harness

- Add local smoke suite.
- Add capacity sweeps.
- Add replay comparisons for real failure classes.
- Store results under a local, ignored artifact directory and summarize in
  telemetry/audit.

Exit criteria:

- Default profile changes are justified by benchmark deltas, not anecdotes.

### Phase 6: Optional Hard Gates

- Add config to require queue gating for specified lanes/profiles.
- Add config to fail when classifier is required but unhealthy.
- Add config to cap context by role and reject oversized packets before model
  call.

Exit criteria:

- CI/eval/multi-agent users can choose stricter operational contracts without
  breaking casual single-user local runs.

## Risks And Mitigations

| Risk | Mitigation |
| --- | --- |
| The harness starts micromanaging model strategy. | Keep operations checks about capacity, permissions, readiness, and evidence. Do not turn health checks into next-tool prescriptions. |
| Doctor becomes slow by running real completions for every model. | Use tiers: static checks by default, endpoint probe with `--deep`, tiny completion only with `--verify-model-call`. Cache recent healthy probes with timestamps. |
| Queue gating surprises single-user flows. | Keep opportunistic gating by default; require gating only by explicit config or CI/eval mode. |
| `backends.json` becomes too backend-specific. | Keep role/capacity fields generic; put backend-specific flags in `extra_args` and adapter-specific validation. |
| Quantization metadata becomes inaccurate. | Treat it as operator-declared or inferred with low confidence. Verify by backend/model metadata only when available. |
| Context budget estimates are approximate. | Mark estimates as estimates. Use actual token counts from model responses when available and feed them back into audit. |
| Classifier health causes false confidence. | Classifier never owns final contract. Invalid/missing classifier output always degrades to deterministic routing. |
| Benchmarks overfit to one machine. | Record machine/backend/profile metadata and compare within the same profile first. Use benchmarks for trend, not universal claims. |
| MLX-LM server security is overestimated. | Bind local-only, document that MLX-LM is not a production security boundary, and keep workspace permissions in Idiom. |
| More telemetry risks secret leakage. | Keep process telemetry redacted and summary-only; raw data stays in local debug logs under explicit mode. |

## Non-Goals

- Do not guarantee that local models match frontier cloud models on arbitrary
  reasoning.
- Do not build a hidden planner that chooses the model's implementation
  strategy.
- Do not require a queue daemon for every casual single-user run.
- Do not make MLX-LM, llama.cpp, or Ollama a security boundary.
- Do not chase every backend flag in the harness schema.
- Do not treat bigger context as a substitute for evidence selection.
- Do not store raw prompts, raw file bodies, or secrets in process telemetry.
- Do not solve model download, licensing, or hosted-model billing in this
  pillar.

## Acceptance Criteria

The local-model operations pillar is acceptable when:

- `idiom doctor --profile local-model-ops --json` reports lane readiness for
  `plan-execute`, `implement`, `audit`, and route classification with exact
  remedies.
- Backend profiles can declare planner, executor, and classifier roles without
  relying on alias heuristics.
- Every lane start emits the resolved backend profile, model alias, role, queue
  mode, request timeout, and context budget tier.
- Queue-managed lanes do not oversubscribe configured pools, and idle in-flight
  jobs are visible in `queue status --json`.
- Route classifier failures degrade to deterministic routing and are visible in
  telemetry/audit.
- Context budget warnings are emitted before oversized packets or history can
  starve output.
- `harness_audit --purpose local_model_operations` summarizes backend health,
  queue health, classifier health, context pressure, stop reasons, and
  verification outcomes from local logs.
- The local smoke benchmark records wall time, token counts, tool calls,
  classifier fallback, queue wait, stop reason, verification result, and
  backend disconnect/OOM-like signals.
- At least one replay suite shows fewer broad reads, repeated tool calls,
  malformed tool calls, or budget-exhausted endings after adopting the
  readiness/budget changes.
- The design remains north-star aligned: the model chooses strategy; Idiom
  enforces operational contracts and reports evidence.

## Open Questions

- Should queue gating become required by default for `plan-execute` when
  `.idiom/backends.json` declares more than one large local model?
- Should `idiom backend start` manage Ollama model preload/keepalive, or should
  Ollama remain externally managed with doctor-only probes?
- Should context budget defaults be per role in `backends.json`, per lane in
  `config.json`, or computed from both?
- Should classifier health be tracked per session only, or persisted across
  sessions to suppress repeated calls to a known-bad helper?
- Which memory signal is portable enough for doctor: process RSS, Metal memory,
  `ollama ps`, backend logs, or explicit operator-declared capacity?
- Should local benchmark results feed ranker/context-budget defaults
  automatically, or remain advisory until an operator accepts them?
