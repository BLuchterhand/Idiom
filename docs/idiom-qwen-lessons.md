# Idiom/Qwen Lessons

This log captures operational lessons from running local Qwen through idiom.
Keep entries short and biased toward harness changes that improve correctness.

## 2026-05-05 — implementation contracts must be structured

VL 9.2d exposed a bad interaction between a generic prompt wrapper and the
route classifier: an implementation atom with acceptance commands was routed as
read-only verification or evidence-first analysis, even though the caller
already knew it needed a write-capable lane.

Do not repair this by adding prompt phrases like `Contract:
implementation_result`, `Task family: implementation`, or `first action must
be edit_file`. That makes strategy text carry a harness contract.

Use the structured path instead:

- call `idiom implement` for implementation atoms
- carry `TaskFamily: implementation` through run options
- build the runtime brief from that task family without asking the route
  classifier first
- keep the atom brief as a normal spec with allowlisted files, verified anchors,
  required behavior, and post-edit validation

If the structured path still fails to edit, fix idiom's routing, runtime brief,
tool permissions, write guards, or completion gate before falling back to another
implementer.
