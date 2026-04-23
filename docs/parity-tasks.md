# OpenCode parity tasks

Two confirmed gaps remain after the Phase 1–6 alignment rewrite. Both are
verified by reading the source; neither is speculative.

---

## Gap 1 — DOOM_LOOP surfaces hard rejection, not approval

### Evidence
`runner.go` lines 390–401: when `identicalCallRun >= 3`, the runner calls
`rejectTool()` unconditionally — a hard rejection that feeds a blocker message
back to the model and terminates the turn. No approval gate is invoked.

Alignment doc §7 is explicit: the runner must *pause and ask the user*, not
silently reject. Quoting §7 directly:

> if the model emits three identical consecutive tool calls (same tool, same
> arguments), the runner halts the auto-execute path and surfaces an approval
> request to the user — blocking until `/approve` or `/deny` — **rather than
> silently rejecting the call**.

Silent rejection burns a model call and gives the user zero visibility; an
approval banner gives the user control.

### Fix

**File**: `internal/agent/runner.go`

Add a field to `Runner`:

```go
// DoomLoopGate, if non-nil, is called when the model emits three identical
// consecutive tool calls. Returning true approves the call (resets the streak
// and lets execution continue); returning false rejects it and terminates the
// turn. When nil, the runner falls back to a hard silent rejection.
DoomLoopGate func(req workspace.ToolRequest, count int) bool
```

In the doom-loop block, replace the unconditional `rejectTool()` pair with:
- If `DoomLoopGate != nil` and returns true → reset `identicalCallRun = 0`,
  fall through to normal execution.
- If `DoomLoopGate != nil` and returns false → call `rejectTool()`, return
  `finish(..., "doom_loop_guard", true)`.
- If `DoomLoopGate == nil` → existing hard-reject (no behavioral change for
  tests that don't wire the gate).

**File**: `internal/app/lifecycle.go`, `runAgentTurn()`

Wire the gate using the existing `requestApproval` mechanism:

```go
DoomLoopGate: func(req workspace.ToolRequest, count int) bool {
    return m.requestApproval(req, mode.Name)
},
```

### Verification
- The `Runner` struct has a `DoomLoopGate` field.
- `runAgentTurn()` sets it to a closure over `m.requestApproval`.
- When `DoomLoopGate == nil` (unit tests), behaviour is identical to before.
- `go test ./...` passes.

---

## Gap 2 — `workspace.Instructions()` still in system prompt

### Evidence
`lifecycle.go:systemPrompt()` lines 463–466:

```go
if m.workspace != nil {
    if instructions := strings.TrimSpace(m.workspace.Instructions()); instructions != "" {
        parts = append(parts, instructions)
    }
}
```

`workspace.Manager.Instructions()` returns workspace root path, CWD, and
behavioural rules (~10 lines). This is injected into every system prompt.

Alignment doc §5:

> Composition order … **Nothing else.**
> Specifically removed: `workspace.Manager.Instructions()` output.

The rules in `Instructions()` ("Do not invent file paths", "prefer edit_file",
write-policy note, discovery instructions) add tokens to every prompt. They
belong in tool descriptions or model training, not the system prompt.

### Fix

**File**: `internal/app/lifecycle.go`

Delete the `workspace.Instructions()` block from `systemPrompt()`.

**File**: `prompts/base.md`

Add one line to preserve the single most load-bearing rule — the one that
directly causes the list_files loop seen in the bad log:

```
- Discover real paths with list_files or search_files before using them; do not invent paths from memory.
```

All other rules from `Instructions()` are either implied by tool descriptions
or are redundant with the model's native instruction-following.

### Verification
- `systemPrompt()` no longer calls `m.workspace.Instructions()`.
- `prompts/base.md` contains the path-discovery rule.
- `go test ./...` passes.

---

## Regression contract

Both changes must leave the following intact:

- `/approve` and `/deny` still resolve pending approvals (approval.go path
  unchanged).
- `/mode build|plan|general` still swaps live.
- `go build ./... && go vet ./... && go test ./...` all green.
- Session JSONL logs still emit `tool_request` and `tool_result` events with
  correct shape.

---

## What success looks like

After both changes:
1. A session where the model loops on the same tool call produces an approval
   banner instead of a silent budget burn.
2. The first system message in a fresh session log is under ~500 tokens (base.md
   content only plus any mode overlay), with no workspace root/CWD text.
3. All tests pass.
