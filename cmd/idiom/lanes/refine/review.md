# META — Review brief (refine loop)

Read the drafted brief and find bugs. Use `write_file` once to
create `{{REVIEW_PATH}}` with findings. Final action MUST be the
write_file call.

## What to read (BOUNDED — total ≤5 reads)

1. The drafted brief: `{{DRAFT_PATH}}` (full read).
2-5. Source files cited in the brief (prioritize files with line
     anchors; bounded line ranges only — do NOT read whole files).

## What counts as a bug

- Hardcoded line numbers wrong (verify against the actual file).
- Proposed code won't compile (missing imports, wrong package,
  wrong function signature, scope errors).
- Edit-shape problems: append-to-end-of-existing-file is wedge-prone;
  prefer NEW FILE; substring anchors must be unique in the target.
- Acceptance commands that wouldn't actually trigger the behavior.
- Outer ```text fence wrapping the whole brief (silent parse break).
- Provenance / heuristic logic gaps.

## Output format (write to `{{REVIEW_PATH}}`)

```text
# Review of brief

## Bugs found

1. **<short title>**: <description>. <how to fix>.

## OK

- <thing the brief gets right>

## Verdict

<ship-as-is | fix-bugs-then-ship | reject-and-redraft>
```

If zero bugs found, write the file with "Bugs found: none" and
verdict "ship-as-is".

## Files To Modify

| File | Change |
|------|--------|
| `{{REVIEW_PATH}}` | NEW FILE: review of the drafted brief |

## Done when

`test -f {{REVIEW_PATH}}` returns 0.
