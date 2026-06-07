# OpenCode protocol alignment — verification contract

Target: idiom drives Qwen via the same wire protocol OpenCode uses when the backend can return structured tool-call failures safely — OpenAI function-calling over `/v1/chat/completions`, with native `tool_calls` and `role:"tool"` responses. Small models' native tool-call channel is the one their instruction tuning was primed on; wrapping tools in a text-based fenced-block protocol can degrade state tracking and produced the list_files loop we saw in `logs/conversations/20260421-092107.335.jsonl`.

Current MLX exception: `mlx_lm.server` Qwen tool parsers can terminate the HTTP request on malformed generated tool XML instead of returning a structured model response. For `kind:"mlx"`, idiom therefore omits native `tools` arrays and uses its JSON tool protocol with harness-side parsing, validation, and recovery. Llama-compatible backends remain the native tool-call target.

This document is the contract. When the implementation is complete, every row in the "Expected" column should be observable in a fresh session log.

---

## 1. Launch shape

**Expected**: `llama-server` starts with `--jinja` plus a Qwen chat template that supports tool-calling. Config exposes this.

Verification:
- `config.json` `llama_server.jinja: true` and `llama_server.chat_template` set (or a tool-call-aware template is resolved automatically from the model name).
- On startup, the managed `llama-server` process command line includes `--jinja` (visible via `ps` or the `logs/llama-server.log` startup banner).

## 2. Tool definitions on the wire

**Expected**: the request body posted to `POST /v1/chat/completions` contains a top-level `tools` array in OpenAI function-calling shape, and **no** in-prompt tool manifest text.

```json
{
  "model": "...",
  "messages": [
    {"role": "system", "content": "<persona prompt only — no tool protocol text>"},
    {"role": "user", "content": "..."}
  ],
  "tools": [
    {"type": "function", "function": {
      "name": "list_files",
      "description": "List files under a workspace path. Prefer search_* or read_file after the first overview.",
      "parameters": {"type": "object", "properties": {...}, "required": [...]}
    }},
    ...
  ],
  "tool_choice": "auto"
}
```

Verification:
- Decode one `llama-server` request (add temporary logging in `internal/runtime/llama/client.go`, or tcpdump the loopback socket). Confirm:
  - `tools` is present, length matches the tool manifest entries
  - `tool_choice` is `"auto"`
  - No system message contains the string `"\`\`\`tool"` or `"When you need a tool, respond with"`
  - No system message contains the tool-manifest table text produced by `RenderToolManifestInstructions`

## 3. Assistant tool-call parsing

**Expected**: the runner parses `choices[0].message.tool_calls[]` from the response, **not** regex-extracts fenced JSON from `message.content`.

Response shape from llama-server with jinja Qwen template:
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "",
      "tool_calls": [{
        "id": "c_abc123",
        "type": "function",
        "function": {"name": "list_files", "arguments": "{\"path\":\"internal\"}"}
      }]
    }
  }]
}
```

Verification:
- One recorded request in `logs/conversations/*.jsonl` where the `tool_request` event's `payload.raw` corresponds to `choices[0].message.tool_calls[0].function.arguments` verbatim (after JSON parse).
- `internal/runtime/agent/runner.go` no longer contains a `extractToolPayload` / fenced-block regex path in the request-ingestion code path. (Keeping it as a deprecation fallback is acceptable, but the primary path is `tool_calls`.)

## 4. Tool-result message shape

**Expected**: tool results are sent back to the model as `{"role":"tool","tool_call_id":"<id>","content":"<result text>"}` messages, one per tool call, with the `tool_call_id` matching the call they answer.

Verification:
- In a recorded session, each tool-call round-trip shows an assistant message with `tool_calls[<id>]` followed by a `role:"tool"` message carrying the same `tool_call_id`.
- No `role:"system"` message has content starting with `[workspace tool result]` — that formatting path is dead.
- `internal/capabilities/workspace/manager.go`'s `FormatToolResultForModel` is either deleted or only used for non-model-facing debug rendering.

## 5. System prompt shape

**Expected**: lean persona only. No tool-protocol instructions, no tool manifest, no static workspace directory listing.

Composition order (as built in `Model.systemPrompt()`):
1. `prompts/base.md` — persona + behavior ethos
2. `prompts/profiles/coding.md` — coding-specific ethos (only if profile active)
3. `Mode.SystemPromptFiles` — e.g. `prompts/plan-mode.md` for PlanMode
4. **Nothing else.**

Specifically *removed* from the system prompt:
- `workspace.Manager.Instructions()` output
- Fenced-block protocol description
- Tool manifest text
- `workspace.OverviewSystemPrompt()` directory snapshot

Verification:
- Count tokens in the first system message of a recorded session. Should be under ~2000 tokens total across the base.md + coding.md + mode files. (Prior runs were ~8000+ because of the manifest + overview.)
- Grep for `"When you need a tool"` in recorded logs — zero hits.
- Grep for the workspace overview's directory tree (`cmd/` / `internal/`) in recorded system messages — zero hits. It should appear only in tool results when the model calls `list_files` itself.

## 6. Dynamic AGENTS.md / CLAUDE.md attachment

**Expected**: after every `read_file` tool call, the runner walks up from the file's directory to the repo root, finds the nearest `AGENTS.md` (or `CLAUDE.md`), and attaches `"Instructions from <path>\n<file contents>"` as a one-shot system message — **once per file per session**.

Mirrors OpenCode's `packages/opencode/src/session/instruction.ts`.

Verification:
- Create `internal/runtime/agent/AGENTS.md` with a test marker string.
- Ask the agent to read `internal/runtime/agent/runner.go`.
- Recorded log shows a `role:"system"` message with content containing the marker, attached **once** even if the model reads multiple files in `internal/runtime/agent/`.
- If a second session reads `internal/surfaces/tui/app/model_state.go` and no `internal/surfaces/tui/app/AGENTS.md` exists, it walks up: finds `internal/runtime/agent/AGENTS.md`? No — it should find the nearest, climbing from `internal/surfaces/tui/app/` → `internal/` → repo root. If no `AGENTS.md` exists on the walk, nothing is attached.

## 7. DOOM_LOOP guard

**Expected**: if the model emits three identical consecutive tool calls (same tool, same arguments), the runner halts the auto-execute path and surfaces an approval request to the user — blocking until `/approve` or `/deny` — rather than silently rejecting the call.

Rationale: three identical calls is OpenCode's signal that the model has lost the thread. A pause-and-ask breaks the loop cleanly; silent rejection just burns budget.

Verification:
- Force a loop: mock a client that makes the model emit `list_files .` three times.
- On the fourth turn, an approval banner appears: `APPROVAL REQUIRED: model stuck in a 3-call loop on list_files. Continue? /approve or /deny.`
- `/deny` writes a fallback result and breaks the loop; the runner returns a concrete blocker message.
- `/approve` lets the fourth call through; if the loop continues, the guard fires again.

## 8. Initial-turn behavior (the one from the bad log)

User prompt: `"Can you identify an opportunity to make a small refactor in this codebase?"`

**Expected trace** (new log after alignment):
1. Model emits a first `tool_call` — typically `list_files .` or `glob internal/**/*.go` or similar
2. Model receives a `role:"tool"` response with the paths
3. Model **does not** repeat the same call. It narrows: `search_content TODO|FIXME`, or `read_file` on a specific candidate
4. After 2-5 tool calls, the model produces a concrete refactor suggestion citing files and lines
5. Final reply is on the order of 300-1500 tokens — a real answer, not a "partial findings, budget exhausted" apology
6. `reasoning_chars` stays in the hundreds throughout, not collapsing to 34

Verification:
- Run `go run ./cmd/idiom` with the same prompt.
- Inspect the newest `logs/conversations/*.jsonl`.
- All six bullets above are visible.

## 9. Regression backstops

**Expected**: these things that already work must not break.

- `/mode build|plan|general` still swaps live (rule 5 of system prompt still applies Mode files)
- `/approve` + `/deny` still resolve pending approvals
- `/undo` still restores the last pre-edit snapshot
- `/panel` still toggles the detail line
- `go build ./...` + `go vet ./...` + `go test ./...` all green
- Eval harness (`cmd/idiom-eval`, `cmd/idiom-swe-eval`) still runs (tools change protocol, but the session log shape for analysis is unchanged in fields that matter: `tool_request`, `tool_result`, `assistant`, `user` records all still present).

---

## What a success looks like, in one sentence

The on-the-wire request shape in a fresh session matches §2-4 exactly; the system prompt matches §5; the first real-task smoke test matches §8; and nothing in §9 regresses.
