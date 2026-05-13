# Planner-Executor Coupling: Three Options

idiom now ships three independent mechanisms to break the planner's
verification loop and reduce focus loss across multi-edit briefs. They
are not mutually exclusive. Pick by need:

| Need | Use |
|---|---|
| You want every implementation lane to stop falling into post-edit verification spirals | **Option A (always on)** |
| You want the planner to formally hand single edits to a separate executor model | **Option B — `delegate_edit` tool** |
| You want a clean planner / executor split where the planner enumerates ALL edits up front | **Option C — `idiom plan-execute`** |

Background on the failure mode this targets is in
[`docs/planner-executor-delegation-design.md`](docs/planner-executor-delegation-design.md).

---

## Option A — Verification intercept on ✓ paths (always on)

### What it does

After the planner has written a required path successfully, the runner
intercepts any subsequent read-class call against that path **when the
preceding tool was also a read** (the verification-spree pattern) and
replaces the result with a one-line redirect that names the next ✗
path:

```
read_file on "native/src/core/animal_types.h" suppressed: that path is
already on the ✓ list of required_edit_paths and the planner runs builds
— do not re-verify completed edits. Next required path (✗ pending):
native/src/core/constants/animals.h. Write it with edit_file or
apply_patch, or stop with <<QUESTION>>: naming the concrete blocker for
that path. 4 required path(s) remain in total: ...
```

The model sees the redirect as the tool result and (in practice)
issues an `edit_file` against the named path on its next turn instead
of stacking another verification read.

### When it fires

All four conditions must hold:

1. The brief carries `required_edit_paths` (i.e. the lane is an
   implementation contract, not a free-form research turn).
2. At least one required path is still ✗ pending.
3. The request is one of the verification-shaped read tools:
   `read_file`; `code_navigate` with `capability=file_outline` or
   `capability=read_symbol`; or any direct `*_file_outline` /
   `*_read_symbol` for cpp/go/python/ts/rust/java.
4. The immediately preceding recorded tool result was ALSO a
   read-class tool. The first read after a write is left through
   because `write a → read a → write b` (mirror existing structure)
   is a legitimate prep pattern.

### When it stays out of your way

- `search_content` and `search_files` are exempt — they're how you
  legitimately verify a literal expression inside a file.
- `code_navigate` with `find_symbol`, `references`, `definition`,
  `calls_from`, `calls_into`, `implementations`, `related_files` is
  exempt — those are real lookups, not verification.
- Reads of paths that aren't in `required_edit_paths` are exempt —
  cross-file context is fine.
- Once every required path is ✓, the guard stands down so closeout
  reads are not blocked.

### How to use

Nothing. It's wired into `idiom implement` (and any lane that uses
the runner with a brief that names `required_edit_paths`). To verify
it fired, look for `verification_intercept_already_edited` events in
the conversation log.

### Files

- `internal/agent/runner_verification_intercept.go`
- `internal/agent/runner_verification_intercept_test.go`
- Wired in `internal/agent/runner.go` before the apply_patch preflight.

---

## Option B — `delegate_edit` tool

### What it does

Adds a tool the planner can call instead of `edit_file` /
`apply_patch`. The runner takes the spec, sends it to the configured
executor model (`Runner.ImplementerClient`, sourced from the executor
role in `.idiom/backends.json` or the `Implementer` block of
`config.json`), parses the executor's reply for an `edit_file` /
`apply_patch` JSON tool call, and dispatches that against the
workspace. The planner sees a normal tool result; the executor never
sees the planner's context.

If the executor is unconfigured, errors out, or returns an
unparseable reply, the runner falls back to dispatching the planner's
spec directly. The lane never stalls.

### When to use it

When the planner has decided exactly what to change and you want it
to keep its context focused on planning the **next** path instead of
re-reading what it just wrote. The cognitive separation is the
benefit; the executor's actual edit is usually a rubber stamp.

### Tool shape

The model emits a tool call shaped like this:

```json
{
  "tool": "delegate_edit",
  "path": "native/src/core/animal_types.h",
  "old_text": "    CAVE_BEETLE,\n    COUNT",
  "new_text": "    CAVE_BEETLE,\n    DONKEY,\n    COUNT",
  "purpose": "append DONKEY before COUNT to preserve existing ordinals"
}
```

Two valid edit shapes (mirror `edit_file`):

- **Substring**: `old_text` + `new_text` (+ optional `replace_all`).
- **Range**: `start_line` + `end_line` + `content`.

### Configuration

`delegate_edit` uses `Runner.ImplementerClient`, which the harness
resolves from one of two sources at boot:

**Preferred: `.idiom/backends.json`** with an executor-role tag.
Either tag a model with `"roles": ["executor"]`, set
`proxy.default_executor` (or the legacy `proxy.default_coder`), or
leave a one-model profile untagged and let the lone-model fallback
serve every role. See `backends.example.json` at the repo root for
copy-paste-able single-model, planner+executor split, and
proxy-fronted variants.

```jsonc
// One model that handles every role (planner, executor, classifier).
// Single-model setups: .idiom/backends.json
{
  "profiles": {
    "local": {
      "models": [
        {"alias": "qwen3-coder-30b", "binary": "mlx_lm.server",
         "model_path": "/path/to/qwen3-coder-30b", "port": 47780}
      ]
    }
  }
}
```

```jsonc
// Two-model split: dedicated planner + executor.
{
  "profiles": {
    "local": {
      "models": [
        {"alias": "qwen3.6-35b",      "model_path": "...", "port": 47780,
         "roles": ["planner"]},
        {"alias": "qwen3-coder-30b",  "model_path": "...", "port": 47781,
         "roles": ["executor"]}
      ]
    }
  }
}
```

**Fallback: `config.json`** `Implementer` block (and `LlamaServer` for
the planner, `RouteClassifier` for the classifier). Still supported
unchanged — when no backends.json declares the role, the harness
reads these fields exactly as it always has:

```json
{
  "llama_server": { ... },
  "implementer": {
    "kind": "mlx",
    "host": "127.0.0.1",
    "port": 47781,
    "model": "/path/to/qwen3-coder-30b"
  }
}
```

If neither source declares an executor, `delegate_edit` falls back to
direct dispatch — still useful as a "this is delegated work, don't
verify" mental hook for the planner. The runner deliberately does NOT
fall back to the planner's own client; that would defeat the
context-separation goal.

### Telemetry events

| Event | Meaning |
|---|---|
| `delegate_edit_executor_call` | executor produced an edit_file/apply_patch call |
| `delegate_edit_executor_error` | executor HTTP / chat call failed; falling back |
| `delegate_edit_parse_error` | executor reply wasn't a valid JSON tool call; falling back |
| `delegate_edit_direct_dispatch` | bypassed the executor, dispatching planner spec directly |

### Counts as a real edit

`delegate_edit` results carry the underlying `Edited <path>` summary
through `wrapDelegateEditResult`, and `changedPathsFromToolResult` has
a `case "delegate_edit"` branch so successful delegations register on
`required_edit_paths_progress` and the ✓ list. Without that the
planner would never see its delegated edits as done.

### Files

- `internal/agent/runner_delegate_edit.go` — runner intercept,
  prompt builder, reply parser, fallback path, wrap.
- `internal/agent/runner_delegate_edit_test.go` — 14 tests covering
  validation, parsing, executor success path, fallback paths, and
  the changedPaths integration.
- Tool manifest entry: `internal/workspace/tool_manifest.go`.
- OpenAI schema: `internal/workspace/tool_schemas.go`.
- Runner intercept wired in `internal/agent/runner.go` next to the
  `task` intercept.

### How to use

If the brief's tool surface includes write tools, the model sees
`delegate_edit` automatically. To recommend it explicitly in a brief:

> Prefer `delegate_edit` for individual file changes when you have a
> precise spec; use `apply_patch` for tightly-coupled multi-file
> changes that must land together.

---

## Option C — `idiom plan-execute` (plan-then-execute lane)

### What it does

A new CLI subcommand that runs the implementation in two phases:

1. **Planner phase** — the model runs against the brief with all
   per-edit write tools FORBIDDEN (`edit_file`, `write_file`,
   `append_file`, `apply_patch`, `delegate_edit`, `mkdir`,
   `go_move_declarations` — every one of those returns a directional
   redirect at the runner). The only "write" the planner can do is
   call `submit_plan` with the full ordered edit list. After that,
   the planner finalizes and exits. If bounded source evidence shows
   the requested final state is already present in every required path,
   the planner may instead close with `Decision: source-satisfied`
   naming each path and the observed evidence; the executor is skipped.
2. **Executor phase** — the orchestrator (in `cmd/idiom`) reads the
   plan artifact written by `submit_plan` and dispatches each edit in
   order through the workspace. Before dispatch, it validates scope,
   stale anchors, ambiguous anchors, completed artifact overwrites, and
   unsupported proof claims such as `assert true` repairs or comments
   admitting the behavior was not actually inspected. After dispatch, it
   runs `source_sanity` on changed source files. Failure of any contract
   aborts the phase and surfaces the failing path. The executor report
   retains source-sanity evidence for the next phase.
3. **Review-repair phase** — the orchestrator assembles an
   `idiom.phase_evidence.v1` bundle for the reviewer. The bundle
   includes changed/required/allowed paths, planner `pack_id` status,
   packet candidate files/symbols/suggested tests, packet gaps, executor
   source-sanity results, patch hygiene, and open obligations. The
   reviewer sees this before deciding to pass, repair, or block. The
   `review_repair_start` audit event also carries bounded evidence
   details so later diagnosis can inspect the packet, test, sanity, and
   obligation state without replaying broad discovery.

The model only thinks once — about WHAT to change, end-to-end —
and never has to hold "what's done vs. what's left" in working memory
across N edits.

### How to use

```bash
idiom plan-execute --prompt-file path/to/brief.md
# or
idiom plan-execute --prompt "Add DONKEY animal across the codebase ..."
# or with positional prompt:
idiom plan-execute "Add DONKEY animal across the codebase ..."
```

Flags (mirror `idiom implement`):

| Flag | Default | Meaning |
|---|---|---|
| `--config` | `config.json` | path to config file |
| `--prompt` | | inline prompt |
| `--prompt-file` | | read prompt from a file |
| `--model` | | override `llama_server.model` |
| `--resume` | | resume a prior session |
| `--json` | `false` | emit structured JSONL events to stdout |

### What gets written

- `<repo_root>/.idiom/plans/<timestamp>.plan.json` — timestamped
  plan artifact (canonical source of truth).
- `<repo_root>/.idiom/plans/last.plan.json` — rolling pointer to the
  most recent plan, useful for `cat` / scripting.

Plan artifact shape:

```json
{
  "schema_version": "idiom.plan.v1",
  "created": "2026-05-09T20:11:20.123456789Z",
  "rationale": "free-text paragraph: why this plan, in this order",
  "pack_id": "3c965b3c8fb10ccb",
  "edits": [
    {
      "path": "native/src/core/animal_types.h",
      "old_text": "    CAVE_BEETLE,\n    COUNT",
      "new_text": "    CAVE_BEETLE,\n    DONKEY,\n    COUNT",
      "purpose": "append DONKEY before COUNT to preserve existing ordinals"
    },
    {
      "path": "native/src/core/constants/animals.h",
      "start_line": 246,
      "end_line": 246,
      "content": "inline constexpr int ANIMAL_SPACE_DONKEY = 7;\n",
      "purpose": "add DONKEY to space-per-animal table"
    }
  ]
}
```

`pack_id` is optional. When the planner used `context_pack` to
localize the work, passing the result's `pack_id` back through
`submit_plan` lets the executor rehydrate the same context view from
the `graph_views` cache via `comprehension.LoadViewByID(root,
pack_id)` instead of re-discovering the same files. Older artifacts
without a `pack_id` remain readable; the field is additive and
schema_version stays at `idiom.plan.v1`.

When a `pack_id` is present, review-repair receives the cached packet
summary as phase evidence. Missing `pack_id` is reported as evidence
state, not a hard failure; structured packet gaps from an available
packet become checklist obligations. If a supplied `pack_id` cannot be
loaded, that packet miss/error is also an open obligation for the reviewer
to resolve with the checklist, a narrow repair, or a blocker.

### Stdout shape (non-`--json` mode)

```
<planner phase output, including the planner's final answer>

  edit 1/8 native/src/core/animal_types.h ... ok
  edit 2/8 native/src/core/constants/animals.h ... ok
  ...
  edit 8/8 native/tests/test_combat.cpp ... ok
  source sanity native/src/core/animal_types.h ... ok
  source sanity native/tests/test_combat.cpp ... ok

--- plan-then-execute summary ---
executor: 8 edits, 8 succeeded, 0 failed
```

On failure:

```
  edit 1/8 native/src/core/animal_types.h ... ok
  edit 2/8 native/src/core/constants/animals.h ... FAILED: edit_file ...
... aborted on edit 2 (native/src/core/constants/animals.h): edit_file ...
```

### When the planner phase tries to call a per-edit write

The runner returns a directional rejection that names `submit_plan`
and the executor phase. This is enforced at the runner's pre-dispatch
hook (`preflightPlanThenExecuteWriteGuard`), so the system addendum
is backed by hard runtime enforcement rather than soft prompting.

Planner-phase shell, git, build/test, and source-sanity commands are
also rejected. Those checks belong to the deterministic executor or
review-repair phases, not to the edit-planning phase.

### Failure recovery

If the planner reports `Decision: source-satisfied` with every required
path named, the orchestrator treats the run as a clean no-op and exits
zero. This is the stale-spec path: the correct response is to update the
tracker/spec, not to force a stale `submit_plan`.

If the planner produces no plan (e.g., stops with `<<QUESTION>>:` for
a blocker), the orchestrator prints the planner's final answer and
exits non-zero. To re-plan, fix the blocker and re-run.

If the executor phase fails on preflight, an edit, unsupported proof
evidence, or post-write `source_sanity`, the orchestrator prints the
specific failure and exits non-zero. The plan artifact remains under
`.idiom/plans/` for inspection. To resume after a fix, re-run the
planner so it computes a fresh plan against the current workspace.

### Files

- `internal/workspace/submit_plan.go` — `submit_plan` workspace
  tool, `PlanEdit` / `PlanArtifact` types, `LoadPlanArtifact`,
  validation, schema versioning.
- `internal/workspace/submit_plan_test.go` — 5 tests.
- `cmd/idiom/plan_execute.go` — CLI subcommand, planner-phase
  system addendum, latest-artifact lookup, executor phase dispatch,
  post-write source sanity, and action-capable review-repair gate.
- `cmd/idiom/plan_execute_evidence.go` — phase evidence bundle for
  packet, diff, source-sanity, suggested-test, and open-obligation
  handoff into review-repair.
- `cmd/idiom/plan_execute_test.go` — focused plan-execute contract
  tests.
- `internal/agent/runner_plan_then_execute_guard.go` — hard runtime
  enforcement of "no per-edit writes or planner-phase verification
  commands", plus source-satisfied closeout recognition.
- `internal/agent/runner_plan_then_execute_guard_test.go` — focused
  tests for planner-phase submit, source-satisfied, and tool guards.

---

## Choosing between the options

- **A is free**: it fires automatically on any implementation lane.
  No action required.
- **B is opt-in per call**: the planner chooses to call
  `delegate_edit` instead of `edit_file`. Useful when you want the
  cognitive break without changing the lane shape. Configure
  `Implementer` in `config.json` to point at a separate executor
  model; otherwise it falls back to direct dispatch.
- **C is a different lane shape**: the planner physically can't emit
  a per-edit write — it MUST produce a `submit_plan`, a source-satisfied
  closeout, or a path-specific blocker. Strongest separation; best for
  multi-file atoms (8+ files) where focus loss is the dominant failure
  mode.

You can run **A + C** together (always on + plan lane) — they don't
interact. **B** is mostly redundant under C (the executor phase is
already a deterministic loop, no need for per-edit model handoff),
but B is independently useful inside `idiom implement`.

---

## Rolling out

1. **Day one**: A is automatic for every existing lane. Watch
   conversation logs for `verification_intercept_already_edited`
   events and confirm the model pivots to a write on the next turn.
2. **Once stable**: recommend `delegate_edit` in implementation
   briefs and declare an executor-role backend (either via
   `roles: ["executor"]` in `.idiom/backends.json` or by setting the
   `Implementer` block in `config.json`).
3. **For larger atoms (8+ files)**: switch to `idiom plan-execute`.
   Rewrite the brief if necessary so it's planner-friendly (the
   planner never edits — it must enumerate every change).

If a lane misbehaves with one option, it can fall back to the
previous option without code changes; both are still wired in.
