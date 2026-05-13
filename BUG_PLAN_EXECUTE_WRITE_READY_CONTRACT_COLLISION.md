# Bug: plan-execute rejects `submit_plan` when the packet is "write-ready"

**Date observed**: 2026-05-11 00:00 UTC
**idiom version**: dev / commit `098b6623718e62724b05910a862cf91f7be97734` (2026-05-10T23:01:42Z)
**Reporter**: Faultline orchestrator (Claude Code)
**Severity**: blocker — every well-anchored multi-file brief dispatched via `plan-execute` is rejected
**Related**: previous `BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md` (resolved); `FEATURE_REQUEST_BRIEF_LINT_HARDENING.md`

## Symptom

A `plan-execute` lane dispatched with a comprehensive brief (every Required Edit has exact `old_text`/`new_text` anchors, all include sites pre-anchored, no branching) calls `submit_plan` on its first turn and gets rejected with:

```
the target packet and required_edit_paths already establish write readiness
for the required edit path(s). submit_plan does not satisfy the pre-write
implementation contract; use an allowed write tool or answer with <<QUESTION>>
and the exact path-specific blocker.
```

The lane has no recovery: `plan-execute`'s planner-phase contract blocks every direct write tool (`edit_file`, `write_file`, `apply_patch`, `delegate_edit`, `mkdir`, `go_move_declarations`), so the model's only remaining options are to retry `submit_plan` (same rejection) or stop with `<<QUESTION>>:`. Neither produces edits.

## Reproducer

Project: Faultline (`/Users/ben/Documents/GitHub/Colony`), branch `phase8/idiom-migration`.

```bash
idiom queue run --pool qwen-35b --label "CET-3.2c-impl-falldetect-v2" -- \
  scripts/run_idiom_impl.sh --title CET-3.2c-impl-falldetect-v2 \
  /var/folders/h5/.../qwen_cet_atom_3.2c-impl-falldetect.md
```

`scripts/run_idiom_impl.sh` is the project's thin shim around `idiom lane plan-execute "$@"`. The brief (416 lines) is preserved at the path above; it has:

- 11 Required Edits across 3 files
- Every edit specifies `old_text`+`new_text` pairs of ≥ 2 lines of literal file content (no prose anchors)
- "Files to Modify" table with explicit allowlist (`required_edit_paths`)
- "Verified anchors" section preloading every external symbol the edits reference

The brief was specifically tightened (after a prior wedge against a vaguer version of the same atom) to satisfy what `qwen-brief-skeleton.md` Gates 5/6 now require: literal anchors, no branching, no discovery mandates.

## Trace

Wrapper output, lane phase:

```
[idiom queue qwen-35b] slot granted (job j-1778457516682-2) — running
idiom brief preflight: OK (0 file-allowlist row(s), 0 file(s))
{"type":"idiom.lane.start","mode":"plan-execute",...}
{"type":"idiom.plan_execute.queue_acquired","label":"plan-execute:ba34f29967ff017d",...}

step 1 tool=submit_plan
  → result error: "the target packet and required_edit_paths already
    establish write readiness for the required edit path(s).
    submit_plan does not satisfy the pre-write implementation contract;
    use an allowed write tool or answer with <<QUESTION>> and the
    exact path-specific blocker."

(model final message:)
  "The tool rejected `submit_plan` claiming the lane requires an
   'allowed write tool' instead, but the brief explicitly specifies
   `plan-execute` mode where `submit_plan` is the sole write mechanism
   and per-edit tools are blocked. This is a lane tooling
   misconfiguration — the lane is treating a plan-execute atom as a
   direct-implementation atom."

{"type":"idiom.plan_execute.plan_artifact_missing",
 "reason":"no plan artifacts under .idiom/plans written since ...
  with session_id=ba34f29967ff017d"}
```

## Root cause inference

Two contracts disagree:

1. **plan-execute lane contract** (per `cmd/idiom/plan_execute.go` and the lane preamble): `submit_plan` is the ONLY write tool the planner can call. All per-edit write tools are forbidden during the planner phase.
2. **Pre-write implementation contract** (apparently introduced in the recent build that removed the read budget): if the brief packet is comprehensive enough — `required_edit_paths` set, exact-anchor `old_text`/`new_text` per edit — the lane should bypass the planner phase and go straight to executor-driven writes via `edit_file` / `apply_patch`.

When both are active simultaneously, the planner phase rejects `submit_plan` (because contract #2 says the packet is write-ready), but the contract #1 lane preamble still has direct-write tools blocked. The model has no legal next action.

The model's diagnosis quoted above is essentially correct: "lane tooling misconfiguration".

## What I think the intended behavior is

Either:

- **(A)** A comprehensive packet should route to `implement` mode (write tools allowed) at dispatch time, not be intercepted during a `plan-execute` run. The decision belongs in route classification, not mid-lane.
- **(B)** `plan-execute` should always honor `submit_plan` as the legal write path, regardless of how comprehensive the packet looks. The "write-readiness" optimization is opt-out; today it's mandatory and clobbers the lane's actual contract.

(A) is cleaner; (B) is the minimal patch.

## What I tried before deciding to file

1. Re-drafted the brief twice already (initial → v2-with-prose-anchors-removed). v2 passed `idiom brief lint`, has all literal anchors, no branching. Same rejection at the contract collision — confirms it's not a brief defect.
2. Reviewed the lane preamble in the wrapper output — confirms `submit_plan` is in the `required_tools` list for plan-execute lanes and direct-write tools are in `forbidden_tools`.
3. Checked the recent build commit (`098b6623`) — has the read-budget removal but I haven't traced exactly which change introduced the write-readiness gate.

## Workarounds available to the consumer side

None that don't compromise correctness:

- Dispatch via `idiom lane implement` instead of `plan-execute`: re-introduces the multi-edit-pressure wedge mode for 12+ edit briefs (the very thing plan-execute was built for).
- Make the brief LESS comprehensive (remove some anchors, add prose) so idiom doesn't classify it as write-ready: actively bad; the previous wedge happened because the brief WAS too vague.
- Self-implement the atom: works but doesn't exercise the lane stack, defeating the point of the migration.

## Impact

Same impact class as the original queue deadlock: every well-formed `plan-execute` dispatch is blocked. With the brief-discipline improvements landing in Colony's rules and the upstream `idiom brief lint` feature request, briefs are MORE likely to trip this gate, not less. So the migration toward "comprehensive briefs + plan-execute" cannot ship until this is resolved.

## Suggested fix

If (B) above is the chosen path, the minimal change is: when a plan-execute lane fires the "pre-write implementation contract" check, AND the lane's `required_tools` includes `submit_plan`, treat `submit_plan` as a satisfying write tool rather than rejecting it.

If (A): move write-readiness detection out of the planner-phase runtime guard and into the lane router (`idiom lane plan-execute|implement` resolution), so a comprehensive brief auto-routes to `implement` at dispatch time and the planner-phase contract never fires.

Either way, the contract collision needs to be resolved before plan-execute briefs can ship.
