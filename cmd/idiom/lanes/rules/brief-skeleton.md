# Brief Skeleton

A lane brief is the planner target packet. The format below is what
idiom's runner recognizes; sections beyond these are treated as
additional context. Brief authors (orchestrator, drafter, human) follow
this shape; executors treat what they receive as ground truth.

Required sections:

- **Title + Summary** — what's being changed and why, in 1-3 lines.
- **Files To Modify** — table or list of paths the lane is allowed to
  write. The runner recognizes "Files To Modify" and "Files You May
  Edit" (and near-equivalents) as `required_edit_paths`. Writes outside
  this list are rejected.
- **Verified Current State** — exact source snippets (with `file:line`
  references) that match the code today. These are the anchors the
  executor uses for `old_text` substrings or `start_line`/`end_line`
  ranges. If a section cannot be quoted exactly, do not fake an anchor
  — name the gap explicitly.
- **Required Edits** or **Implementation Shape** — what changes,
  edit-fact by edit-fact. Each Required Edit names ONE concrete change
  site (file + anchor + new text or behavior). Avoid bundling unrelated
  changes into one edit.
- **Acceptance** — what counts as done. Build and test commands listed
  here are planner-owned; idiom enforces that contract and the
  executor does not run them.

Recommended:

- **HARD PROHIBITIONS** — paths to leave alone, anti-patterns,
  side-quests the executor should refuse.
- **Decomposition notes** — if the brief intentionally splits work
  across multiple atoms, name the boundary so the executor doesn't
  pull in adjacent work.

Anchor discipline:

- Anchors are exact substrings of source text. Paraphrase and
  description are not anchors. "The function that handles X" is not an
  anchor; the literal source line is.
- Anchors must be unique within their file. If a snippet appears
  multiple times, extend the anchor with surrounding context until
  it's unique.
- Anchors that quote test names, doctest expectations, macro labels,
  or single known sections of test/spec files are exact-text questions.
  Confirm via one targeted exact-text read if needed, not via broad
  symbol queries.
