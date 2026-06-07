
# Plan-Then-Execute Closeout

This dispatch is complete after exactly one terminal outcome:

1. `submit_plan` has been called successfully with an ordered, complete edit
   list.
2. `Decision: source-satisfied` names every required edit path and the observed
   current-state evidence showing the requested final state is already present.
3. A concrete `<<QUESTION>>:` blocker reports the exact missing fact or
   path-specific blocker.

After `submit_plan` returns, finalize with a short summary naming the planned
edits and stop. Do not loop into post-plan verification reads — the executor
phase will surface any per-edit failure on its own. Do not submit verification
or placeholder edits for source-satisfied work.
