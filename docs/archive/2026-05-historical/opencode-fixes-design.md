# OpenCode-inspired fixes — design and acceptance criteria

Addresses two problem classes observed in
`logs/conversations/20260420-200831.527.jsonl`:

1. **Repeated tool failures on a hallucinated path.** The model retried
   `edit_file bubble/bubble.go` three times against a path that did not
   exist; the `rejection_recovery_nudge` shipped earlier caught it but is a
   last-resort patch, not a root-cause fix.
2. **Answer fabrication.** The model's final reply claimed it had extracted
   `formatValue` from `formatEntry` in `entry.go`, while no successful write
   ever landed; `budget_exhausted:false, stop_reason:"model_final"` and the
   critic accepted the reply.

Both are classes of bug that OpenCode sidesteps structurally rather than
through harness heuristics. Rest of this document specifies each fix, where
it lives, and what a test has to prove to call the fix shipped.

Scope: small, additive edits to existing tool surface + runner. No changes to
the agent.Runner control flow beyond where noted. Nothing here is gated by
the guarded-vs-auto write policy; these are capability improvements, not
safety rails.

---

## Fix 1 — Read-before-edit invariant

**Problem.** `edit_file` accepts a path argument and trusts the model to have
verified the file exists. Small models hallucinate paths, hit "no such file",
and retry the same hallucinated path.

**Design.** `workspace.Manager` gains a per-turn set of "files this session
has observed". Any tool result that successfully reads a file (`read_file`,
`search_content` with a path hit, `list_files` hitting a file row, shell
commands whose result enumerates paths) adds to the set. The `edit_file`
tool's PreExecute rejects when the target path is not in the set, with an
error telling the model to read the file first.

**Files.**
- `internal/capabilities/workspace/manager.go` — new `RecentlyObservedPaths` getter and
  internal bookkeeping; `MarkPathObserved(path)` called from inside
  `Execute` after successful reads.
- `internal/capabilities/workspace/tool_policy.go` (or nearest equivalent) — new
  `RequireReadBeforeEdit` PreExecute helper.
- Runner/implementation lane — wire the helper into `PreExecute`.

**Acceptance.**
- Unit test: manager records observed paths after a successful `read_file`;
  `HasObservedPath` returns true; after a miss (no such file), the path is
  *not* added.
- Unit test: `edit_file` against an un-observed path returns a structured
  error saying "first read_file this path".
- Unit test: `edit_file` against an observed path passes through.
- Integration test: runner loop where step 1 calls `edit_file` on a fresh
  path gets rejected with a readable error; step 2 reads the path; step 3
  edits successfully.

**Non-goals.** Does not constrain `write_file` (new-file creation — path by
definition not yet observed). Does not constrain `shell` write operations.

---

## Fix 2 — Fuzzy path suggestions on "not found"

**Problem.** Bare "no such file or directory" gives the model nothing to
recover with. OpenCode's file tools append a list of nearest matches.

**Design.** When a read or edit tool fails because the path doesn't exist,
decorate the error with up to three nearest real paths the workspace knows
about, ranked by case-insensitive Levenshtein distance with a small edit-
distance cap. Real paths come from the set the manager has already observed
via `list_files` or glob searches during the session (cheap) plus an
on-demand cheap filesystem walk scoped to the parent directory (bounded).

**Files.**
- `internal/capabilities/workspace/paths.go` — new `NearestKnownPaths(target string, k int) []string`.
- `internal/capabilities/workspace/manager.go` — in the path-not-found branches of
  `read_file` and `edit_file`, call the helper and append
  `\n\nDid you mean: A, B, C?` to the error when candidates are non-empty.

**Acceptance.**
- Unit test: `NearestKnownPaths("bubble/bubble.go", 3)` against a workspace
  containing `internal/bubble/bubble.go` returns that path as the top
  suggestion.
- Unit test: when the workspace is empty of candidates, the helper returns
  empty and the tool error is unchanged.
- Unit test: case-insensitivity — `BUBBLE.go` near `bubble.go`.
- Unit test: edit-distance cap rejects totally unrelated paths.
- Integration test: `edit_file` on a misspelled path returns an error whose
  text contains `Did you mean:`.

---

## Fix 3 — Fabrication detection

**Problem.** In the sample log, the final assistant reply named `entry.go`
and `formatValue` as having been edited; no successful edit ever occurred.
The existing final-answer critic accepted it.

**Design.** After the implement-lane runner returns, extract workspace-style
path tokens from the assistant's reply (regex for `[\w./]+\.(go|ts|js|py|md|json|yaml)`
and similar). For each extracted path, require it to appear either (a) in
`changedPaths`, (b) in any collected tool result's path metadata from the
turn, or (c) as an explicit negation ("did not edit X"). If an unexplained
path appears, inject a grounding correction — a system message reminding the
model what was actually edited, and request a revised summary. One retry
allowed, then fall through (never loop forever).

**Files.**
- `internal/surfaces/tui/app/implementation.go` — new helper
  `groundFinalAnswerAgainstEvidence(reply, changedPaths, collectedResults)`
  called inside `finalizeImplementation` before
  `applyControlRailsFinalAnswer`.
- `internal/frontier/` — pure helpers: path-token extractor + reconcile
  against an evidence set.

**Acceptance.**
- Unit test: extractor finds `entry.go`, `internal/surfaces/tui/app/app.go:42`,
  skips bare words `run`, `edit`, etc.
- Unit test: reconcile() returns "ungrounded" when reply names a path not
  in evidence and not negated.
- Unit test: reconcile() returns "grounded" when the only named paths are
  in `changedPaths`.
- Unit test: single-retry loop: first pass flagged, injects correction
  message, second pass either grounded (accept) or still ungrounded
  (accept anyway, stop_reason `grounding_retry_exhausted` logged).

**Non-goals.** Not guarding against hallucinated line numbers or content
(only path-level). Not running over the main lane — main is already
read-only-ish and its fabrications are rare.

---

## Fix 4 — Workspace overview in opening system context

**Problem.** Small models hallucinate paths partly because they go into the
turn blind: no sense of the project's top-level shape. AGENTS.md helps for
projects that ship one, but it's optional and often stale.

**Design.** At session start (first user message of a session), inject a
single compact `workspace_overview` system message: the top-level directory
listing (one level deep), counts of files per directory, and notable
dotfiles. Computed by the workspace manager via existing listing primitives.
Truncated to a hard cap.

**Files.**
- `internal/capabilities/workspace/overview.go` — new `OverviewSystemPrompt(root string) string`.
- `internal/surfaces/tui/app/prompt_routing.go` — `runStandardPromptOnce` injects the
  overview into `requestMessages` once per session (gated by a
  `workspaceOverviewInjected` flag on the Model).

**Acceptance.**
- Unit test: `OverviewSystemPrompt` run against a fixture repo returns a
  string containing the top-level dirs and respects the hard cap.
- Unit test: repeated calls to `runStandardPromptOnce` in one Model
  instance inject the overview exactly once.
- Integration test: first turn's message list contains exactly one
  `role=system` message with the overview; second turn's does not.

**Non-goals.** No hotspot ranking, no "recommended files", no
responsibility guessing. Just the terrain.

---

## Fix 5 — Summarization-based context compaction

**Problem.** When message history exceeds `max_history`, the harness hard-
truncates the oldest messages. Long-horizon intent (what the user originally
asked for, constraints they stated) silently disappears.

**Design.** Before truncating, if the session has more than a threshold of
messages (say, 30), fold the oldest messages into a single `session_digest`
system message via a model call. The digest uses a small, fixed-format
prompt: user goals, files read so far, edits applied, blockers, open
questions. The digest replaces the old messages; the recent N stay verbatim.

**Files.**
- `internal/surfaces/tui/app/model_state.go` — `maybeCompactHistory(history)` called from
  `requestMessages`. Uses `m.client` with a fixed small prompt.
- `internal/surfaces/tui/app/compaction.go` (new) — prompt template, regex-based fallback
  if the model call fails.

**Acceptance.**
- Unit test: `maybeCompactHistory` with < threshold messages returns input
  unchanged.
- Unit test: with > threshold messages, returns a history whose first
  message is a system `session_digest` and whose length equals threshold.
- Unit test: if the model call fails, the fallback regex summary still
  yields a digest containing the user's first prompt.
- Integration test against a fake server: a Model with 40+ history items
  calls the server once for the digest, then trims.

**Non-goals.** Not incremental compaction (single pass). Not token-aware
(uses message count as proxy). Not model-family aware.

---

## Fix 6 — Per-tool permission granularity

**Problem.** `enable_shell` / `enable_writes` are binary. Users either grant
everything or route through the approval flow even for innocuous commands.
OpenCode matches tool calls against glob rules: `allow | ask | deny`.

**Design.** New `ToolRules` config section:

```json
"tool_rules": {
  "shell": { "git *": "allow", "rm *": "ask", "*": "allow" },
  "edit_file": { "**/*.md": "allow", "**/secrets/**": "deny" },
  "write_file": { "**/secrets/**": "deny", "*": "ask" }
}
```

`ask` produces a write-approval proposal; `deny` produces a hard block;
`allow` passes through. Matching is first-match-wins in rule order. Missing
rules default to auto-allow (preserves current behavior when the section is
absent).

**Files.**
- `internal/platform/config/config.go` — new `ToolRules map[string][]ToolRule`.
- `internal/capabilities/workspace/tool_rules.go` (new) — `MatchToolRule(tool, req)
  string` returning `allow | ask | deny | ""`.
- `internal/capabilities/workspace/write_policy.go` or the main/implement guard —
  consult the matcher ahead of the existing binary switch.

**Acceptance.**
- Unit test: `shell: { "rm *": ask, "*": allow }` — `shell rm x` → ask,
  `shell ls` → allow.
- Unit test: `edit_file: { "**/*.md": allow, "**/secrets/**": deny }` —
  editing `README.md` → allow, editing `a/secrets/k.json` → deny.
- Unit test: absent rules → allow (behavior preservation).
- Integration test: a runner step hitting an `ask` rule produces a
  write-approval proposal event.

---

## Fix 7 — Real subagent spawning via the task tool

**Problem.** `tool_manifest.go:118` declares a `task` tool but nothing in
the runner actually spawns a subagent when it is called. Model calls to
`task` silently no-op.

**Design.** When `task` is invoked, the runner starts a child `Runner` with
a fresh `Workspace` copy and a seed prompt derived from the tool arguments
(objective, optional file hints, optional budget). The child runs its own
loop against the same model and returns its final answer as the tool
result. Isolated context — child messages do not pollute the parent's.

**Files.**
- `internal/runtime/agent/task_tool.go` (new) — `SpawnSubagent(parent *Runner,
  req workspace.ToolRequest) (workspace.ToolResult, error)`.
- `internal/runtime/agent/runner.go` — `task` tool dispatch at PreExecute time;
  budget-inherit with a fractional cap.
- `internal/capabilities/workspace/tool_manifest.go` — schema refinement: required
  fields `objective`, optional `files[]`, optional `max_steps`.

**Acceptance.**
- Unit test: fake-server fixture where parent's step 1 calls `task`; child
  answers; parent's step 2 sees the tool result and answers the user.
- Unit test: child's message history is isolated — parent's history never
  contains the child's intermediate tool calls.
- Unit test: child inherits a bounded subset of the parent's budget; hits
  its own `MaxToolSteps` without blowing the parent's.

**Non-goals.** No parallel subagents (one at a time). No cross-subagent
state. No approval-flow subagent (children inherit the parent's permission
policy).

---

## Fix 8 — Minimal MCP client

**Problem.** Zero MCP references in the codebase. OpenCode gets docs search,
issue trackers, browser control, DB tools, etc. through MCP servers. Local-
only Qwen benefits disproportionately because it can't rely on a provider-
side tool zoo.

**Design (minimal).** MCP over stdio transport only. Config lists servers
(name, command, args, env); at startup, the app spawns each, performs MCP
handshake, lists tools, and wires them into the workspace tool manifest
with a `mcp:<server>/` prefix. Tool invocations proxy JSON-RPC over stdio.

This is intentionally minimal: stdio only (not SSE/HTTP/WebSocket), tools
only (not resources or prompts), one-shot discovery (no dynamic refresh),
synchronous calls (no notifications).

**Files.**
- `internal/capabilities/mcp/client.go` (new) — JSON-RPC over stdio, `Initialize`,
  `ListTools`, `CallTool`.
- `internal/capabilities/mcp/registry.go` (new) — spawns servers from config, tracks
  lifecycles.
- `internal/platform/config/config.go` — new `MCP []MCPServer { Name, Command,
  Args, Env }`.
- `internal/capabilities/workspace/manager.go` — when executing a tool with an
  `mcp:` prefix, dispatch to the registry.

**Acceptance.**
- Unit test (no real server): fake MCP stdio with canned Initialize and
  tools/list responses — registry reports the tool, CallTool round-trips.
- Unit test: disabled/missing MCP section — no behavior change, no error.
- Integration test (optional, tagged): run against
  `npx @modelcontextprotocol/server-filesystem` if available on the
  machine; list tools; skip cleanly if not.

**Non-goals.** Resources, prompts, roots, sampling, subscriptions, SSE,
HTTP. Those can come later without changing this skeleton.

---

## Validation plan

Every fix above must ship together with:

1. `go build ./...` green.
2. `go test ./... -count=1` green.
3. `make swe-suite` (SWE e2e) green.
4. A dedicated test file section or test function name that maps 1:1 to
   the numbered fix ("Fix 3" in a comment or test name).

After implementation, the final step of this effort walks through every
numbered fix in this document and prints the matching file:line where the
corresponding test lives. That mapping is the "done" signal.

## What this document does NOT claim

- **That these fixes match what OpenCode does in every detail.** They take
  inspiration from OpenCode's user-observable contracts. Implementation
  details differ and are chosen for what fits idiom.
- **That small models will suddenly perform like Claude.** These changes
  remove classes of failure. They do not solve planning quality or code
  generation quality.
