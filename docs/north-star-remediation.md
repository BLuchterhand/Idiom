# North-Star Remediation Patch

Generated 2026-04-25. Scope: clear, fundamental misalignments with `docs/decisioning-north-star.md`. Each item lists the current text, the proposed replacement, and the principle being restored. Borderline phrasing (numeric thresholds in nudges, "prefer" guidance, etc.) is intentionally excluded.

**Excluded by user direction:** `tool_schemas.go` descriptions for `discovery_query`, `ast-grep`, and `ctags` (structured-search contract is intentional).

---

## 1. `prompts/base.md` — hardcoded one-scenario rule (GGUF)

**Why:** North-star §Repo Policy: "do not hardcode strategy around one recurring scenario or question class; if a rule is real, express it in generic evidence/contract terms rather than domain-specific terms." A GGUF-only line is the domain-specific shape. The underlying rule — "binary or structured-format artifacts need format-aware inspection" — is generic and worth keeping; just lift it out of one file format.

**File:** `prompts/base.md:13`

**Current:**
```
- To inspect GGUF model metadata, do not grep the raw binary — use `strings path.gguf | grep -a <key>` or a Python `gguf` package dump. GGUF keys are length-prefixed, not line-oriented.
```

**Proposed:** generalize to a format-agnostic rule:
```
- For binary or structured-format files (model weights, archives, databases, compiled artifacts), grep and line-oriented tools will not work — use a format-aware reader for that file type. If you are not sure what format a file is, identify it before trying to inspect its contents.
```

The rule is now generic; the GGUF-specific recipe, if worth preserving anywhere, becomes an eval fixture or — better — lives next to the GGUF-handling code as an `AGENTS.md` snippet picked up by the existing instruction attacher.

---

## 2. `prompts/base.md` — topic-template tool sequences

**Why:** North-star §Repo Policy: "treat search policies and examples as ranked guidance, not mandatory scripts." The numbered recipes for *Investigation*, *Locate a symbol*, and *Verification after edits* are exactly the topic-template shape the doc warns against.

**File:** `prompts/base.md:29-43`

**Current:**
```
Preferred tool sequences (follow one of these shapes; do not chain list_files or guess filenames):

Investigation — "how does X work?" / "where is Y?":
  1. For supported code languages, start with one structured probe: `*_workspace_symbol`, `*_file_outline`, call/reference tools, or one `discovery_query` for broad architecture / impact / tracing questions.
  2. Read one exact symbol body, discovered handle, or narrow line range from the best candidate. Use `search_content` only when semantic tools or discovered handles do not land.
  3. Answer, citing file:line. Continue only if one exact missing claim remains and you can name the exact probe that would resolve it.

Locate a symbol by name:
  1. `go_workspace_symbol` (or the language equivalent) with the symbol name. Returns file + line + kind in one call.
  2. `go_read_symbol` with the symbol name and the returned path to inspect the body.
  3. Skip step 1 if you already know the path — call `go_read_symbol` directly.

Verification after edits:
  1. `shell` with `go test ./path/to/package` (NOT `cat`/`grep`/`find`/`sed`). Scope tight — one package, not the whole repo.
  2. If failing, `read_file` on the failing test or source to diagnose.
```

**Proposed:** Replace the entire section with a generic guidance paragraph:

```
General shape (guidance, not a script):
- Start with one structured probe (`*_workspace_symbol`, `*_file_outline`, references/calls, or one `discovery_query` for impact/architecture/tracing questions).
- Read the exact symbol body or narrow line range from the best candidate; fall back to `search_content` only when semantic tools do not land.
- Answer with file:line citations once the evidence is sufficient.
- Verify writes with the narrowest test scope that covers the change.
```

The "Locate a symbol" recipe is already covered by the general shape; the "Verification" recipe duplicates the existing nudge to verify after edits.

---

## 3. `prompts/base.md` — `task_ledger` as forced pre-step

**Why:** "Before edits: ... call `task_ledger`" forces a tool sequence ahead of any write. North-star: prefer nudges; force only invariants. Whether the ledger exists at the time of a write is a useful contract for the *plan panel* (a UI surface), not a strategy the model must satisfy in lockstep.

**File:** `prompts/base.md:16`

**Current:**
```
- Before edits: when you determine write work is needed, call `task_ledger` with one item per intended change and list the files in each item's `paths`. This drives the plan panel and lets completed writes check off their planned item in place; file touches without a matching item land in an "Unplanned" section — a signal the triage was incomplete.
```

**Proposed:**
```
- The `task_ledger` tool drives the plan panel: when write work is non-trivial, use it to list one item per intended change with the files under `paths`. Writes without a matching ledger item show up under "Unplanned" — a signal that triage was incomplete, not a hard error.
```

This keeps the *what* and *why* without prescribing call-order.

---

## 4. `internal/runtime/agent/runner.go:582-585` — "must narrow next" forced strategy

**Why:** North-star §Examples (bad): "Only tool X remains available when the model could still safely answer or use a narrow follow-up." This rejects a broad inspection and lists four specific tools the model "must" use.

**File:** `internal/runtime/agent/runner.go:582-585`

**Current:**
```go
errorText := "you already have a broad workspace overview. Next use search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
if r.Workspace.IsExploreMode() {
    errorText = "you already have a broad workspace overview. In explore mode you must narrow next with search_files, search_content, read_file on a discovered handle, or a targeted shell search/read command before doing more broad inspection."
}
```

**Proposed:**
```go
errorText := "you already have a broad workspace overview. Either answer from the evidence already gathered, or take one narrower next step (a discovered handle, a single named symbol, or a focused search) before any further broad inspection."
```

Drop the explore-mode branch. Same rule applies to both modes; the difference was only the word "must."

---

## 5. `internal/runtime/agent/runner.go:551-557` — list_files hide + ranked alternatives

**Why:** Hiding `list_files` after repeated chained calls is a legitimate loop-break contract. The wording prescribes three ranked alternatives, which is strategy.

**File:** `internal/runtime/agent/runner.go:551-557`

**Current:**
```go
fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files has been removed from your available tools for the remainder of this turn. Use search_content with keywords from the user's question, read_file on a path you already discovered, or a language-specific symbol tool (go_workspace_symbol, go_references) instead.", executionState.consecutiveListFiles),
```

**Proposed:**
```go
fmt.Sprintf("you have run %d list_files calls in a row without inspecting any content. list_files is hidden for the rest of this turn. Take one narrower next step on a path you already discovered, or answer with the evidence already gathered.", executionState.consecutiveListFiles),
```

Keep the loop-break (invariant), drop the ranked tool list (strategy).

---

## 6. `internal/runtime/agent/runner.go:5607-5644` — "Pick ONE" recovery menu

**Why:** North-star §Examples (good): "after readiness, ask the model to name one exact missing claim and one exact probe that would resolve it; do not hard-pick the probe for the model." The "Pick ONE of the concrete next steps below (you can copy-paste the JSON)" framing — and the embedded `Do not call pwd, ls, find, search_files, search_content, or read_file` lines — directly violate this.

**File:** `internal/runtime/agent/runner.go:5607-5644`

**Current (`buildLastChanceRecoveryNudge` opening):**
```go
b.WriteString("Recovery nudge — this is your last chance before the harness ends the turn. Pick ONE of the concrete next steps below (you can copy-paste the JSON). Do NOT repeat the rejected tool call.\n\n")
```

**Proposed:**
```go
b.WriteString("Recovery nudge — last chance before the harness ends the turn. Either answer with the evidence already gathered, or take one narrow next step that names the exact missing claim it is closing. Do not repeat the rejected tool call.\n\n")
```

**Current (verification-only branch, lines 5614-5620):**
```go
b.WriteString("This is a verification-only turn. Do not explore first.\n")
b.WriteString("Run the requested check now:\n")
b.WriteString("  {\"tool\":\"shell\",\"command\":\"")
b.WriteString(strings.ReplaceAll(cmd, "\"", "\\\""))
b.WriteString("\",\"workdir\":\".\"}\n")
b.WriteString("If that command already ran, answer with the observed pass/fail result instead of calling another tool.\n")
b.WriteString("\nDo not call `pwd`, `ls`, `find`, `search_files`, `search_content`, or `read_file` before the verification command.\n")
return b.String()
```

**Proposed:**
```go
b.WriteString("This is a verification-only turn. Do not explore first — run the requested check.\n")
b.WriteString("The configured verification command is: ")
b.WriteString(cmd)
b.WriteString("\n")
b.WriteString("If that command has already run, answer with the observed pass/fail result.\n")
return b.String()
```

Verification-only is a real task contract — "run the requested check before answering" can stay firm. Two specific things go: (a) the JSON copy-paste tool template, which is hard-picking the probe and is exactly what north-star §Examples warns against; (b) the enumerated `Do not call pwd, ls, find, search_files, search_content, read_file` list, which is the bad-list anti-pattern (the verification-only permission profile already enforces tool boundaries — the prose is redundant force, and any new tool added later silently escapes the list). Keeping "Do not explore first" preserves the contract without prescribing the recovery shape.

**Current (debug-log branch, lines 5644-onwards — partial):**
```go
b.WriteString("Do not switch back to source files, broad workspace discovery, or another raw read_file window of the same log.\n")
```

**Proposed:** drop this line entirely. The `debugLogPhaseScopeRejection` contract already blocks the unwanted shapes; the prose is duplicative force.

---

## 7. `internal/runtime/agent/runner_lane_policy.go:130-137` — `debug_logs_tail_answer_forced`

**Why:** Domain-keyed forced answer for one scenario (debug-log lane after a blind reread). North-star §Repo Policy: "do not hardcode strategy around one recurring scenario… if a rule is real, express it in generic evidence/contract terms." The underlying contract is "evidence sufficient — answer now"; the lane-specific name and prompt encode strategy in the wrong layer.

**File:** `internal/runtime/agent/runner_lane_policy.go:130-137`

**Current:**
```go
if summary == "Blind log reread rejected" {
    decision.RejectSummary = summary
    decision.RejectError = errorText
    decision.ForceAnswer = &runnerForcedAnswer{
        Reason: "debug_logs_tail_answer_forced",
        Prompt: "You already have the most recent log tail. Do not request any more tools. Answer directly from that log evidence: identify the weird decisions visible in the logs, say whether the harness can be made smarter, and name the most relevant generic improvement or the remaining uncertainty.",
    }
    return decision
}
```

**Proposed:**
```go
if summary == "Blind log reread rejected" {
    decision.RejectSummary = summary
    decision.RejectError = errorText
    decision.ForceAnswer = &runnerForcedAnswer{
        Reason: "evidence_sufficient_after_blind_reread",
        Prompt: "Evidence is sufficient. Answer from what you already have: state the observation, the inferred cause if any, and the remaining uncertainty. If a specific probe would still materially change the answer, name the exact missing claim instead of calling another tool.",
    }
    return decision
}
```

Generic reason key, generic prompt. The split is deliberate: the **trigger** can stay domain-specific because "blind log reread" is a real, named shape that only fires inside a `debug_logs` lane — that's a contract observation, not strategy hardcoding. The **forced-answer reason and prompt** are what need to be generic, because the rule the contract enforces ("evidence is sufficient, answer now") is generic and applies in any lane where the same shape arises. The same fix should be considered anywhere else a force is keyed by lane name rather than by evidence shape.

---

## 8. `internal/capabilities/workspace/manager.go:1090-1091` — "Do NOT retry" directive in evidence payload

**Why:** North-star: "evidence tools should describe findings, counts, paths, handles, and symbols without prescribing the next tool call." Telling the model not to retry is a control-flow rule that belongs in the runner's repeated-call detector, not in a `list_files` error string.

**File:** `internal/capabilities/workspace/manager.go:1090-1091`

**Current:**
```go
if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
    errMsg = fmt.Sprintf("directory %q does not exist in this workspace. Do NOT retry with children of this path (e.g. %q/foo.go) — they cannot exist either. Check the Did-you-mean suggestions below or ask the user for the real location. (os: %s)", req.Path, strings.TrimRight(req.Path, "/"), err.Error())
}
```

**Proposed:**
```go
if os.IsNotExist(err) && strings.TrimSpace(req.Path) != "" {
    errMsg = fmt.Sprintf("directory %q does not exist in this workspace. (os: %s)", req.Path, err.Error())
}
```

The Did-you-mean suggestions are already attached as `result.Output` below — they speak for themselves. If looping on hallucinated children turns out to be a real failure mode, fix it in the loop-break logic, not in the message.

---

## 9. `internal/capabilities/workspace/manager.go:3171-3177` — read-before-edit recovery prescription

**Why:** Read-before-edit is a legitimate invariant. The recovery prescription ("Call read_file on this path or use search_content / search_files / list_files") is strategy in an evidence payload.

**File:** `internal/capabilities/workspace/manager.go:3171-3177`

**Current:**
```go
if !m.HasObservedPath(m.displayPath(path)) && !m.HasObservedPath(req.Path) {
    return ToolResult{
        Tool:    "edit_file",
        Summary: "Edit request rejected",
        Error: fmt.Sprintf("edit_file requires you to have read %s first in this session. Call read_file on this path (or use search_content / search_files / list_files to confirm it exists), then retry the edit.",
            m.displayPath(path)),
    }
}
```

**Proposed:**
```go
if !m.HasObservedPath(m.displayPath(path)) && !m.HasObservedPath(req.Path) {
    return ToolResult{
        Tool:    "edit_file",
        Summary: "Edit request rejected",
        Error:   fmt.Sprintf("edit_file requires you to have observed %s in this session before editing it.", m.displayPath(path)),
    }
}
```

State the contract; let the model choose how to satisfy it.

---

## 10. `internal/capabilities/workspace/manager.go:3279` — start_line invariant + recovery prescription

**Why:** The invariant (`start_line >= 1`) is correct. The "If you don't know the line number, read the file first or use old_text/new_text instead" tail prescribes recovery.

**File:** `internal/capabilities/workspace/manager.go:3279`

**Current:**
```go
return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "start_line must be >= 1 (line numbers are 1-indexed). If you don't know the line number, read the file first or use old_text/new_text instead."}
```

**Proposed:**
```go
return ToolResult{Tool: "edit_file", Summary: "Edit request rejected", Error: "start_line must be >= 1 (line numbers are 1-indexed)."}
```

The tool description for `edit_file` already documents `old_text/new_text` as an alternative shape — repeating it in the error is unnecessary.

---

## 11. `internal/capabilities/workspace/tool_schemas.go:84-89` — `task` description prescribes strategy

**Why:** North-star: "evidence tools should describe findings… without prescribing the next tool call. Strategy guidance belongs in runtime briefs and control rails, not in evidence summaries or result payloads." The `task` tool description carries a strategy rule ("use a read_only explorer first") that belongs in a runtime brief.

**File:** `internal/capabilities/workspace/tool_schemas.go:84-89`

**Current:**
```go
{
    Name:        "task",
    Description: "Delegate a bounded evidence-gathering question to a sub-agent. For broad review, performance, security, architecture, or large search tasks, use a read_only explorer first and have it return inspected paths plus concise findings.",
    ...
},
```

**Proposed:**
```go
{
    Name:        "task",
    Description: "Delegate a bounded question to a sub-agent. Read-only sub-agents return inspected paths and concise findings; write-mode sub-agents may also make changes within their scope.",
    ...
},
```

The "use a read_only explorer first" steering, if it is real policy, should live in the runtime brief alongside the broad-evidence-delegation nudges (`runner_request_budget.go`).

---

## 12. `internal/capabilities/workspace/tool_manifest.go:368` — Python-specific shell fallback recipe

**Why:** Domain-specific rule baked into a per-language semantic-tool brief. If "fall back to shell when language server is missing" is a real general rule, it should be expressed once across all languages, not as a hardcoded recipe for Python only.

**File:** `internal/capabilities/workspace/tool_manifest.go:368`

**Current:**
```go
if hasPython {
    lines = append(lines,
        "Python semantic-tool rules:",
        "- Prefer Python semantic tools over raw shell/grep for module, symbol, entrypoint, reference, and diagnostics questions.",
        "- Semantic lookups return paths, symbol kinds, and handles for follow-up inspection.",
        "- python_diagnostics requires pyright or pylsp; if neither is installed, fall back to shell (`python -m py_compile`, `ruff check`) explicitly.",
    )
}
```

**Proposed:** drop the third bullet from the static manifest. The right shape is to detect the capability gap at runtime and report it neutrally inside the tool result when `python_diagnostics` is actually called without a backing language server — something like:

```
python_diagnostics: no language server available (pyright and pylsp not installed). Diagnostics not produced.
```

That is a **factual capability report** in an evidence payload, which is north-star aligned. It surfaces only when relevant, lets the model decide what (if anything) to do next, and avoids baking a Python-only recipe into every turn. If the same pattern exists for other languages, apply the same shape there — runtime-detected, neutral capability gap, no prescribed shell fallback.

---

## 13. `internal/runtime/agent/runner_request_budget.go:149,157,207,271,279` — "Do not" framing

**Why:** Several rejection messages stack "Do not X. Do not Y." prescriptions where the underlying contract is the same "answer now or one targeted probe for a named gap." The contract is correct; the phrasing leans force where nudge would carry the same weight.

**File:** `internal/runtime/agent/runner_request_budget.go`

**Current (line 149, 157, 271, 279 — representative sample):**
```go
RejectError: "this turn has already been nudged to either delegate one focused read_only task, answer from the evidence already gathered, or continue with a materially narrower follow-up. Do not keep spending broad inspection calls.",
...
RejectError: "the read_only child task already returned the broad evidence packet. Do not restart broad discovery in the parent. Continue from the child result with one targeted read/search for a named gap, make the required change, or answer with the current evidence.",
...
RejectError: fmt.Sprintf("you already revisited %s multiple times after the child packet. Answer now, or inspect a different evidence path for a named gap.", label),
...
Nudge: fmt.Sprintf("You already inspected %s after the child packet. Unless one specific missing line remains, answer now or inspect a different evidence path. Do not keep rereading the same file family.", label),
```

**Proposed (apply consistently):**

Line 149:
```go
RejectError: "broad inspection has already been nudged on this turn. Answer from the evidence gathered, or take one narrower next step naming the exact missing claim it closes.",
```

Line 157:
```go
RejectError: "the read_only child task returned the broad evidence packet. Continue from that packet with one targeted read or search for a named gap, make the required change, or answer with the current evidence.",
```

Line 271:
```go
RejectError: fmt.Sprintf("you already revisited %s multiple times after the child packet. Answer now, or take one narrow probe on a different evidence path that names the exact missing claim.", label),
```

Line 279:
```go
Nudge: fmt.Sprintf("you already inspected %s after the child packet. Either answer now, or take one narrow probe that names the exact missing claim — same path is fine if you can name what new line you expect.", label),
```

Pattern: drop "Do not X" prefixes; use the canonical "answer now or one exact probe for a named gap" wording from the north-star §Examples.

---

## 14. `internal/runtime/agent/runner_task_ledger.go:90,100` — fallback menus after readiness *(lower priority)*

**Severity note:** This is a *cleanup* item, not a fundamental misalignment. The completion gate is a legitimate contract surface, the menus are short and not topic-specific, and the cost of leaving them is much smaller than the cost of leaving items 6 and 7. Land items 6 and 7 first; fold this into a later phrasing pass once the higher-impact patterns have settled.

**Why:** Three-option fallback menus at completion-gate violations. North-star §Examples (good): "after readiness, ask the model to name one exact missing claim and one exact probe that would resolve it; do not hard-pick the probe for the model… later recovery and failure prompts should stay closure-biased too."

**File:** `internal/runtime/agent/runner_task_ledger.go:90,100`

**Current (line 90):**
```go
Error: "this is complex multi-step work, but no task_ledger has been created. Create a task ledger, continue the work, or answer with an explicit blocker/no-op reason.",
```

**Proposed:**
```go
Error: "this is complex multi-step work and no task_ledger exists. Create a ledger or, if you are not yet acting on a write, answer with an explicit blocker or no-op reason.",
```

**Current (line 100):**
```go
Error: "task ledger still has unfinished required work: " + strings.Join(unfinished, "; ") + ". Continue with the next tool call, update the ledger with a concrete blocker, or if the user asked only for a plan, reply with an explicit planning-exit phrase (for example \"plan ready - awaiting approval\").",
```

**Proposed:**
```go
Error: "task ledger still has unfinished required work: " + strings.Join(unfinished, "; ") + ". Continue the work, mark the ledger item with a concrete blocker, or — if the user asked only for a plan — finish with an explicit planning-exit phrase (e.g. \"plan ready - awaiting approval\").",
```

The change is small but real: the original frames the three options as a menu the model picks from; the proposal frames them as the natural shapes of "actually finish or honestly stop."

---

## 15. Contract selection through prompt magic *(new, 2026-05-05)*

**Why:** North-star §Core Split: "the model owns strategy; the harness owns
contracts." A prompt phrase such as `Contract: implementation_result`,
`Task family: implementation`, or `first action must be edit_file` is not a
contract surface; it is strategy text being parsed as if it were structured
state. That is brittle and creates wrapper/router coupling that agents cannot
reason about.

**Bad shape:**

```text
Generic prompt runner -> route classifier -> prompt contains magic phrase ->
host upgrades route
```

**Good shape:**

```text
Dedicated command or typed run option -> runtime brief contract -> model sees a
normal strategy brief
```

**Remediation rule:** When a caller already knows the task contract, carry it
through structured run metadata, a typed route plan, a role, or a command mode.
For implementation atoms, use `idiom implement` so the harness sets
`TaskFamily: implementation` directly. If generic routing misclassifies a write
task, fix the structured routing path or wrapper command, not the atom wording.

**Regression check:** Add tests that the structured path bypasses the route
classifier and emits the expected runtime brief. Avoid tests that require magic
prompt phrases to trigger a contract.

---

## Out-of-scope (intentionally left alone)

- **`tool_schemas.go:130` (discovery_query)** — user-confirmed: discovery tools have an intentional hard requirement. The "Use when X instead of choosing tools ad hoc" framing is contract guidance, not strategy steering. The load-bearing enforcement should live in `discovery_redirect.go`; verify that side fires for the listed intents.
- **`tool_schemas.go:138, 178` (ast-grep, ctags)** — same family as `discovery_query`. Structured-search-first is endorsed by north-star §Repo Policy.
- **`tool_manifest.go` "Prefer X over Y" lines** — explicitly endorsed style ("treat search policies and examples as ranked guidance").
- **`prompts/base.md:10` ("Aim for ≤5 tool calls per sub-question")** — soft nudge, numeric threshold but not force; left for the user to tune separately.
- **`prompts/plan-mode.md:7` ("Do not keep searching unless…")** — closure-biased, on the harsh side of the line; phrasing tweak only, not fundamental.
- **`internal/runtime/agent/discovery_redirect.go`** — already matches the canonical good example ("Use the structured tool instead; if it returns nothing, retry the original request").
- **`internal/frontier/`** — clean per audit; closure directive at `runtime.go:606-607` is the model the rest of the codebase should converge toward.

---

## Suggested rollout order

Sorted by severity / blast-radius rather than by file:

1. **Highest-impact strategy violations** — items 6 and 7. Recovery menu with copy-paste JSON and the lane-named forced answer are the two most direct violations of north-star §Examples ("do not hard-pick the probe for the model"; "do not hardcode strategy around one recurring scenario"). Both have test coverage in `runner_test.go` / `runner_request_budget_test.go` — expect string assertions to update.
2. **Static prompts** — items 1, 2, 3. No test impact, immediate effect on every turn, easy to review in isolation.
3. **Forced strategy in runner** — items 4, 5, 13. Localized to `runner.go` and `runner_request_budget.go`; phrasing-only changes that need fixture updates.
4. **Strategy in evidence payloads** — items 8, 9, 10, 11. Touch `manager.go` and `tool_schemas.go`. Grep for the exact error strings before editing — several tests assert on them verbatim.
5. **Capability-gap reporting refactor** — item 12. Pair the static-line removal with a runtime detection path inside `python_tools.go` so the tool itself emits the neutral capability message; do not land the manifest deletion before the runtime side exists, or the model will lose the signal entirely.
6. **Phrasing cleanup** — item 14. Cosmetic relative to the rest; land last or fold into another pass.

Each item is independently revertible. Pick whichever subset is most defensible to land first; items 6, 7, and 1 together would be the single highest-value cluster.
