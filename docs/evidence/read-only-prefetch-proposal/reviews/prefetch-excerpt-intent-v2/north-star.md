[2m  ▎ thinking[0m
  · 16922 tok · in 16824 (16640 cached / 184 new) · out 98 (35 reasoning) · ¥0.0007
  -> read_file {"path": "docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md"}
[2m  ▎ thinking[0m
## APPROVE

This intent doc is well-aligned with north-star and actor-boundary principles. Specific confirmation of each key constraint:

| Constraint | Status | Notes |
|---|---|---|
| **Capped synthetic prefetch only** | ✅ | Explicitly scoped to `read_only_named_path_prefetch`; `read_file`, search tools, child-agent, routing, and gates are non-goals. |
| **Neutral footer, not strategy instruction** | ✅ | "neutral model-facing footer, not a strategy instruction" — says omitted ranges *can* be requested, not *should* be. This is factual tool-availability, not a prescriptive nudge. |
| **Normal `read_file` unchanged** | ✅ | Explicit in both contract and non-goals; acceptance test verifies full-range `read_file` still works after prefetch. |
| **Existing safeguards preserved** | ✅ | File-count, total-byte, generated/log, and binary checks kept; only replaces `too_large` skip with excerpt. |
| **Existing telemetry reused** | ✅ | `TruncatedFiles` field is already wired but inert; this makes it live without a new counter. |
| **No routing or gate changes** | ✅ | Explicit non-goal. |
| **Truncation metadata visible** | ✅ | Summary with original line/byte counts prevents silent partial-evidence assumption. |

One minor observation (not a blocker): the acceptance test "A runner-level truncated-prefetch fixture can answer without gratuitous follow-up when the excerpt is sufficient" tests a *desired operational outcome* rather than a harness invariant. The passive invariant is "prefetch never hides evidence; normal read_file is always available." The fixture test is fine as a smoke test but should not become a regression gate on model behavior — if the excerpt happens to be insufficient for a particular task, that's the model's call to follow up with `read_file`, not a harness failure. Consider wording it as a *capability demonstration* rather than a pass/fail acceptance criterion.

Proceed to implementation.
  · 19423 tok · in 18135 (17152 cached / 983 new) · out 1288 (857 reasoning) · ¥0.0039 · cache prefix changed: tools
