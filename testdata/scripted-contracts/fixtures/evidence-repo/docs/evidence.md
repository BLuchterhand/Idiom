# Evidence Policy

Evidence is bounded information the harness can audit later: source-body reads,
log events, tool summaries, changed paths, verification commands, and explicit
uncertainty.

Git operations can be useful evidence when they answer a named question. History,
blame, and diff checks help reduce churn by showing why an intended change path
exists and whether the current patch touches the expected locations.

Git evidence must not become a hidden mutation gate. It should support a decision
with concrete paths and commands while keeping write permission, verification,
and final answer readiness as separate contracts.
