# Planner-Executor Delegation: Design Options

Captured 2026-05-09 during the Colony donkey-run debugging session, after the
verification-loop pattern surfaced as the dominant failure mode for 35B on
multi-file atoms (8-file DONKEY brief stalls at 4/8 edits, then loops on
post-edit verification reads).

## Problem shape

The 35B reasoning model can sequence 4 edits successfully, but after a
verification interruption it loses the active goal and never returns to writing
the remaining ✗ paths. Verification urge wins over the writing urge. The
harness already knows ground truth (`required_edit_paths_progress` lists ✓ for
written, ✗ for unwritten), but the model isn't converting ✗ into edit actions.

This is fundamentally a planner-executor coupling problem. One model is being
asked to:

1. Hold the multi-edit plan in working memory
2. Execute each edit
3. Verify each edit landed
4. Track progress
5. Decide when to stop

Reasoning models are good at (1) and (5); coder models are good at (2) and (3).
Forcing both roles into one 35B turn is what's currently failing.

## Three options, ordered by cost

## Shipping status (2026-05-09)

All three options shipped in a single push. The intercept landed first
because it was the smallest behavioral change; delegate_edit and
plan-execute followed because the user asked for the full menu.

| Option | Status | Tests |
|---|---|---|
| A — Harness intercept on ✓ paths | shipped | `internal/agent/runner_verification_intercept_test.go` |
| B — `delegate_edit` tool | shipped | `internal/agent/runner_delegate_edit_test.go` |
| C — Plan-then-execute mode | shipped | `internal/workspace/submit_plan_test.go`, `cmd/idiom/plan_execute_test.go` |

These three are not mutually exclusive: A applies to every implementation
lane; B is an opt-in tool the planner can call when it wants cognitive
separation per-edit; C is a different lane shape entirely
(`idiom plan-execute`).

### Option A — Harness intercept on ✓ paths (shipped 2026-05-09)

When the model calls `file_outline` / `read_symbol` / `read_file` on a path
already in the ✓ list, the harness returns a one-line redirect instead of the
real data:

> "Path already edited (✓). Do not re-verify — the planner runs builds. Next
> required path: `<X>`. Write it or stop with `<<QUESTION>>:`."

**Effort.** Hours. Single intercept in the runner's pre-dispatch hook;
required_edit_paths progress state is already tracked.

**Risk.** Low. Deterministic input validation. If a model legitimately wants to
recheck after a surprise diff, this blocks it — but that case is rare and
`<<QUESTION>>:` is the escape hatch.

**Upside.** Closes the verification-loop habit at the dispatch layer. Doesn't
help if the planner has other planning gaps unrelated to verification.

**When this is enough.** If 35B's only deficit is the verify-vs-write coupling,
this should be sufficient. Multi-edit sequencing is already proven (4/8 lands
without trouble); we just need to stop letting verification interrupt it.

### Option B — Tool-level delegation (`delegate_edit`)

Add a `delegate_edit` tool to the planner's manifest. The planner calls it
like any other tool with a structured edit spec:

```json
{
  "path": "native/src/core/constants/animals.h",
  "anchor_old_text": "...",
  "new_text": "...",
  "rationale": "add ANIMAL_SPACE_DONKEY constant"
}
```

Under the hood, the harness routes the body to a fresh 30B-coder sub-session
with a tight micro-prompt: "given this exact spec, write the edit." Returns
typed success/fail back to the planner.

**Effort.** Days. New tool definition, sub-session orchestration, error
propagation, prompt design for the 30B side, telemetry plumbing.

**Risk.** Medium.
- Latency: each delegation is a full prompt round-trip; 8 delegations could
  push runs from 2 to 15 minutes.
- Memory pressure: 35B and 30B both resident on Apple Silicon; concurrent
  active requests may queue at the proxy.
- Failure shape: 30B refusal or bad output needs a clear escalation path back
  to the planner.

**Upside.** Each edit runs in a fresh executor turn — no focus loss across N
edits. Planner's "verification" naturally becomes "did the executor report
success" rather than ad-hoc reads.

**When this is the right next step.** If Option A intercepts the verification
loop but the planner still drifts (e.g., loses the plan, skips paths, mis-orders
edits), the symptom is "planner can't sustain N steps" — delegation isolates
each step into its own context.

### Option C — Plan-then-execute (structured plan artifact)

Planner outputs a single structured plan artifact (e.g., `plan.json` with N
edit specs) as its sole deliverable. Harness reads the artifact and drives
30B-coder through each edit in a deterministic loop. Planner exits after
producing the plan and never sees individual edit results unless something
fails and re-planning is needed.

This is closer to the "implementation contract" framing already in
`idiom implement` — the planner produces an artifact, the harness enforces it.

**Effort.** Week+. Structured plan schema, artifact validator, harness driver
loop, retry policy, partial-failure handling, telemetry.

**Risk.** Higher.
- Plan artifact schema becomes a contract surface that must evolve with new
  edit shapes.
- Re-planning on partial failure is a non-trivial control problem.
- The model has to learn to produce the artifact reliably.

**Upside.** Cleanest separation of concerns. Matches the architecture that
frontier coding agents (Aider's architect mode, Cursor's composer) converged
on. Scales naturally to 20+ file atoms because the planner only thinks once.

**When this is worth doing.** Only after Option B proves the delegation pattern
works in practice, AND there's product pressure to scale beyond 8-10 file
atoms. Until then it's premature architecture.

## Staging

1. **Now:** Ship Option A. Watch one or two more Colony runs.
2. **If A is insufficient:** Build Option B. The 30B-coder is already running
   behind the same proxy via the `--coder` switch in
   `scripts/run_idiom_impl.sh`, so the routing infrastructure exists.
3. **If B works and product wants larger atoms:** Build Option C.

## What this is NOT

- Not a prompt fix. Telling a small model "don't verify" is a negative
  instruction about a behavior the model finds safe; small models routinely
  ignore those.
- Not a model upgrade. The point is to make 35B work, not to switch to a
  larger model that wouldn't need the harness compensation.
- Not "smarter verification." The whole verification urge needs to be
  redirected, not refined.
