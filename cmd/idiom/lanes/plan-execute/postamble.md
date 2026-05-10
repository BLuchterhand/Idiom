
# Plan-Then-Execute Closeout

This dispatch is complete only after `submit_plan` has been called successfully
with an ordered, complete edit list, OR a concrete `<<QUESTION>>:` blocker is
reported with the exact missing fact. After `submit_plan` returns, finalize
with a short summary naming the planned edits and stop. Do not loop into
post-plan verification reads — the executor phase will surface any per-edit
failure on its own.
