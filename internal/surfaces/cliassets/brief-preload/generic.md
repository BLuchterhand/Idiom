## Atom-Spec Drafting Preload: generic

- Produce a planner-owned implementation atom spec, not implementation code.
- Required sections for source-touching atoms: Files To Modify, Files to Read First, Verified Current State, Changes, Required Edits or Required Behavior, Constraints, Tests, Acceptance, Decisions.
- Files To Modify must be an exhaustive allowlist table with one discrete action per row. Do not combine actions or specify overlapping line ranges for the same file.
- Files to Read First must cite concrete repository paths with line anchors, not vague systems or "definitions".
- Verified Current State must inline the load-bearing current source facts, exact anchors, or tool output needed to decide whether the atom is stale.
- Required Edits must be unconditional and should include exact old/new anchors or unified diffs when the edit shape is known.
- Changes must be concrete enough for an implementation lane to act without discovering missing API names.
- Tests must be exact commands the planner can run after accepting the diff.
- Leave no placeholders such as `...`, `TBD`, or `<fill me>`.
