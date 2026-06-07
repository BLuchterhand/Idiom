# Live TODO Panel

Status: design draft, 2026-05-15

## Problem

The right-pane **Plan tab** already tracks planned work and completed writes,
but it is invisible by default and does not surface intent clearly enough to
serve as a live "to do" list during a session. The user must remember to open
the side panel (Ctrl+B), switch to the Plan tab (Ctrl+T), and mentally parse
a mix of headings, planned items, and unplanned writes to understand progress.

Two scenarios are underserved:

1. **Plan-execute sessions** — The planner knows every edit upfront via
   `submit_plan`. Those intents should appear as unchecked items the moment
   the plan is accepted, then check off one by one as the executor lands each
   write. Currently the plan tab is empty until the first write succeeds, at
   which point items appear already checked.
2. **Ad-hoc interactive sessions** — The model can call `task_ledger` to
   declare intent, but the panel is hidden so the user never sees the
   emerging plan. The user also has no manual way to add a TODO.

The existing infrastructure (task ledger, autodoc, diff tracking, checkbox
markdown) is sound. The gap is a **visibility and population** layer, not a
fundamental architectural change.

## Current State

### What exists

| Piece | Location | What it does |
|---|---|---|
| Plan tab | `internal/surfaces/tui/app/plan.go` | Right-pane tab rendering checkbox markdown |
| Autodoc | `internal/surfaces/tui/app/plan_autodoc.go` | Records write success/failure, attaches writes to planned ledger items |
| Task ledger | `internal/runtime/agent/runner_task_ledger.go` | Model-declared items with ID, Content, Status, Paths |
| Planned items | `plan_autodoc.go:467-487` | `### Planned` section, populated from `task_ledger` results |
| Unplanned writes | `plan_autodoc.go:279-304` | `### Unplanned` section, populated on write success |
| Diff overlay | `internal/surfaces/tui/app/plan_overlay.go` | Enter on checked item shows colored diff |
| Session headings | `plan_autodoc.go:330-364` | `## Session <id>` auto-appended per session |
| Tab navigation | `plan.go:235-289` | j/k cursor, Enter opens diff, Esc drops focus |

### What does NOT exist

- No auto-open or auto-switch to the Plan tab.
- No population from `submit_plan` results — only `task_ledger` feeds
  `### Planned`.
- No user-facing command to manually add a TODO item.
- Tab label is always "Plan" regardless of content or session type.
- No progress indicator (e.g. "3/7 done").

## Proposed Changes

### 1. Auto-show the panel when TODO content appears

**Trigger**: After any event that mutates the plan file (write success,
task_ledger update, or submit_plan population — see change 2), if the side
panel is closed, auto-open it and switch to the Plan tab.

**Why not always open**: An empty panel wastes horizontal space. Opening only
when there is content means the user sees the list exactly when it becomes
useful.

**Implementation sketch**:

- `planRefreshMsg` handler checks `m.planPanelAutoShowNeeded()`.
- New helper reads the plan file, returns true when there are checkbox lines.
- On true: set `m.historyPanelOpen = true`, `m.panelTab = panelTabPlan`.
- Emit once; do not re-trigger on subsequent refreshes if the panel is
  already open.

**For plan-execute sessions specifically**: The planner phase knows the task
family is `plan_then_execute_planner` (via `m.forcedTaskFamily`). When this
family is set and a `submit_plan` result lands (change 2), auto-open is
unconditional — the whole point of plan-execute is that the user should see
the plan being executed.

### 2. Populate unchecked items from `submit_plan` results

**Current flow**: The planner emits `submit_plan` with `PlanArtifact.Edits`.
The executor reads the artifact and dispatches each edit. The plan tab sees
nothing until a write lands — at which point it appears as a checked item
under `### Unplanned` (because no ledger item was created).

**Proposed flow**: Intercept `submit_plan` tool results in the `onToolResult`
callback. For each edit in the submitted plan, append an unchecked `[ ]` line
under a `### Plan` section (new, distinct from `### Planned` which is for
`task_ledger` items). Each line carries the edit's description, target path,
and a synthetic anchor so `attachWriteToPlannedItem` can check it off when
the matching executor write lands.

**Data path**:

```
onToolResult(req) where req.Tool == "submit_plan"
  → recordSubmittedPlan(req)
    → for each edit in req.PlanArtifact.Edits (or req.Edits):
        → append "- [ ] <description> (path) <!-- pid:<planEditID> -->" to plan file
        → register in m.planSubmittedEdits map[planEditID]PlanEdit
```

The existing `attachWriteToPlannedItem` matching logic (path match, heuristic
fallback) is reused — the `pid:` anchor serves the same role as `tid:` for
ledger items. When the executor write lands and `recordWriteSuccess` fires,
the matching `[ ]` flips to `[x]` and the diff anchor is appended.

**Why a new section (`### Plan`) instead of `### Planned`**: `### Planned` is
fed by `task_ledger`, which the planner phase does not have access to (the
tool is blocked during planning). Keeping the sections distinct preserves
clarity: `### Plan` = upfront edits from the plan-execute lane; `### Planned`
= model-declared tasks via `task_ledger`. They can coexist in the same file.

**Edit ID generation**: Each `PlanEdit` in the artifact already has a stable
index within the edits list. Use `fmt.Sprintf("pe-%d", idx)` as the synthetic
plan-edit ID. This is deterministic — the same plan always produces the same
IDs, which aids debugging and resumption.

### 3. Richer intent labels

**Current**: `planLineLabel()` (`plan_autodoc.go:309-324`) uses `req.Description`
with path fallback. For writes without a description, the label degrades to
`edit_file path/to/file`.

**Proposed**: When `req.Description` is empty, synthesize a label from
`toolRequestIntent()` (`lifecycle.go:717-779`), which already produces
human-readable intent strings per tool type (e.g. "apply the planned change
in path/to/file"). This applies to both the `### Unplanned` entries and the
`submit_plan` population.

**Scope**: Change is limited to `planLineLabel()` — add a fallback that calls
a shared intent-synthesis function. No structural changes to the autodoc
pipeline.

### 4. `/todo` slash command

**Purpose**: Let the user manually add an ad-hoc TODO item to the plan panel.

**Command**: `/todo <text>` — appends `- [ ] <text>` to the plan file under
the current session heading, then refreshes the plan viewport.

**Implementation**:

- Add `/todo` to the slash-command dispatch in `handleSlashCommand`.
- Call `ensurePlanPath()` + `ensurePlanSessionHeading()`, then append the
  line.
- Emit `planRefreshMsg` to trigger re-render.
- No anchor is attached (no diff to link to) — these are purely manual items
  that the user checks off by editing the plan file via `/plan`.

**Future extension**: A `/todo done <text>` variant could search for a
matching unchecked line and flip it. Not in scope for the initial
implementation — `/plan` opens `$EDITOR` for manual edits.

### 5. Tab label shows progress

**Current**: Tab always reads "Plan".

**Proposed**: When the plan file has checkbox items, show a progress count:

- `Plan · 3/7` — 3 checked, 7 total (plan-execute or any session with items)
- `Plan` — no items yet (current behavior)

**Implementation**: In `renderPanelTabs()`, call a helper that counts
checkbox items from `m.planContent` (already cached). The count is cheap
(a few regex matches) and only runs when the tab strip is rendered.

### 6. In-progress item styling

**Current**: Items are either `[ ]` (unchecked) or `[x]` (checked). There
is no visual distinction for "currently being worked on."

**Proposed**: When the model sets a `task_ledger` item to `in_progress`,
the plan line should render with a highlighted `[~]` or `[…]` indicator in
a distinct color (e.g. `colorAccent` / blue), so the user can see which
item the agent is actively pursuing.

**Implementation**:

- `plannedItemLine()` already switches on item status. Add `in_progress`
  case producing `- [~] <content> <!-- tid:... -->`.
- `renderPlanLine()` already pattern-matches on `[ ]` and `[x]`. Add a
  `[~]` case with a spinning or accent-colored indicator.
- When `recordTaskLedger` receives an `in_progress` status for an already-
  rendered item, update the plan file line in place (same mechanism as
  `attachWriteToPlannedItem`'s `[ ]` → `[x]` flip).

## File Change Summary

| File | Change |
|---|---|
| `internal/surfaces/tui/app/plan_autodoc.go` | Add `recordSubmittedPlan()`; extend `planLineLabel()` with intent fallback; extend `attachWriteToPlannedItem()` to match `pid:` anchors; add `in_progress` line rewrite |
| `internal/surfaces/tui/app/plan.go` | Add `planPanelAutoShowNeeded()`; update `renderPanelTabs()` for progress count; extend `renderPlanLine()` for `[~]` style |
| `internal/surfaces/tui/app/lifecycle.go` | Call `recordSubmittedPlan()` from `onToolResult` when `req.Tool == "submit_plan"`; call auto-show helper on `planRefreshMsg` |
| `internal/surfaces/tui/app/model_state.go` | Add `planSubmittedEdits map[string]workspace.PlanEdit` field |
| `internal/surfaces/tui/app/styles.go` | Add `planInProgressStyle` for `[~]` items |
| `internal/surfaces/tui/app/lifecycle.go` | Add `/todo` to slash-command dispatch |
| `internal/surfaces/tui/app/plan_autodoc_test.go` | Tests for `recordSubmittedPlan`, `pid:` anchor matching, in-progress rewrite |

## What This Does NOT Change

- **Planner tool access** — The planner phase still cannot call
  `task_ledger`. The new `submit_plan` interception is read-only from the
  planner's perspective; it reads the plan artifact the planner already
  produced.
- **Executor behavior** — The executor continues to dispatch edits
  deterministically. The only difference is the plan tab now shows progress.
- **Diff tracking** — Existing diff computation, storage, and overlay remain
  unchanged.
- **Plan file format** — Still markdown with checkboxes and HTML comment
  anchors. The new `### Plan` section and `pid:` anchors follow the same
  conventions as `### Planned` and `tid:`.
- **Non-plan-execute sessions** — The auto-show and submit_plan population
  only fire when the session has plan-execute context. Regular `implement`
  sessions continue to use `task_ledger` and `### Planned` as today.

## Open Questions

1. **Auto-open threshold**: Should the panel auto-open on the first
   `task_ledger` call in any session, or only in plan-execute sessions?
   Opening on any ledger call would surface the TODO list in regular
   `implement` sessions too, which may be desirable but could surprise users
   who don't use the side panel. **Proposal**: auto-open on first content in
   plan-execute sessions; in other sessions, show a footer hint ("plan · 3
   items — Ctrl+B to view") instead.

2. **Submitted-plan deduplication**: If the planner calls `submit_plan`
   multiple times (correction rounds), should the second call replace the
   `### Plan` section or append to it? **Proposal**: replace — clear the
   existing `### Plan` section and re-populate from the latest artifact.
   This matches the mental model of "the plan is the latest plan."

3. **Manual `/todo` items and diff linkage**: Currently there's no mechanism
   for a user-added TODO to auto-check when a write lands. This is fine —
   the user added it, the user checks it off via `/plan`. If we later want
   auto-checking, the user would need to specify a path in the `/todo`
   command (`/todo fix the handler -- internal/handler.go`). Not in scope.
