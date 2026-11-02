# Proposal: deterministic context-pack fallback for unreadable ranker state

Status: proposal only; not implemented.

Allow a caller to request context-pack ranking with deterministic baseline
weights when persisted ranker state cannot be read. The result would need to
identify the fallback in its provenance and unresolved gaps, while leaving the
malformed ranker files untouched.

This could keep read-only context discovery useful when optional learning state
is unavailable. The trade-off is that fallback can hide corrupt or unsafe
ranker state from a caller that otherwise expects the repository's persisted
ranking configuration to be authoritative. The current behavior fails closed
on malformed state; any proposal should preserve that default and make fallback
an explicit, reviewed contract.
