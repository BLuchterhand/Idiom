# Prompt Decisioning Tree: idiom vs. OpenCode

A factually grounded comparison of how each project decides what prompt and
route to use for a turn.

> **April 2026 update.** idiom's runtime control plane was significantly
> simplified to mirror OpenCode's shape while keeping discovery tools as
> first-class. This doc reflects the post-simplification state. See
> "Simplification deltas" at the bottom for what was deleted.

Sources read:

- idiom: `internal/agent/modes.go`, `internal/promptstack/`,
  `internal/agent/runner_lane_policy.go`, `internal/agent/instructions.go`,
  `internal/app/turn_planner.go`, `internal/app/prompt_routing.go`
- OpenCode (`anomalyco/opencode`): `packages/opencode/src/agent/agent.ts`,
  `packages/opencode/src/session/system.ts`,
  `packages/opencode/src/session/prompt/*`

## OpenCode decisioning tree

```
incoming turn
│
├─ 1. AGENT (user-selected via Tab; build / plan / general)
│     ├─ build    → primary, broad permissions, default
│     ├─ plan     → primary, read-only (writes denied)
│     └─ general  → subagent only, invoked via @general
│
├─ 2. SYSTEM PROMPT (chosen by MODEL FINGERPRINT in session/system.ts)
│     ├─ gpt-4 / o1 / o3                  → beast.txt
│     ├─ codex variants                   → codex.txt
│     ├─ copilot gpt-5                    → copilot-gpt-5.txt
│     ├─ other gpt                        → gpt.txt
│     ├─ gemini                           → gemini.txt
│     ├─ claude                           → anthropic.txt
│     ├─ trinity / kimi                   → respective
│     └─ fallback                         → default.txt
│   plus mode-conditional fragments:
│     ├─ plan agent                       → plan.txt (+ plan-reminder-anthropic.txt)
│     ├─ build agent transition           → build-switch.txt
│     └─ step ceiling reminder            → max-steps.txt
│
├─ 3. ENVIRONMENT BLOCK (cwd, git status, platform, date)
│
├─ 4. SKILLS (gated by agent permissions)
│
└─ 5. INSTRUCTION ATTACHMENT (session/instruction.ts)
      on file touch → walk up dirs → nearest AGENTS.md / CLAUDE.md, attach once
```

## idiom decisioning tree (post-simplification)

```
incoming turn
│
├─ 1. MODE (user-selected at session start; OpenCode-shaped)
│     ├─ build    → full r/w/shell, deny "rm -rf /", ask on sudo, mcp:* ask
│     ├─ plan     → writes denied, shell allowlist (git/ls/cat/grep/rg), mcp:* allow
│     └─ general  → reads free, writes ask, shell ask, mcp:* ask
│
├─ 2. SYSTEM PROMPT (per-model fingerprint via promptstack.SelectModelPrompt)
│     ├─ qwen     → prompts/opencode/qwen.md       (full discovery directive)
│     ├─ claude   → prompts/opencode/anthropic.md
│     ├─ gpt/o1/o3 → prompts/opencode/gpt.md
│     ├─ gemini   → prompts/opencode/gemini.md
│     └─ default  → prompts/opencode/default.md
│   plus mode fragment via promptstack.SelectModeFragment:
│     ├─ plan     → prompts/opencode/plan.md
│     ├─ build    → prompts/opencode/build-switch.md
│     └─ general  → (no fragment)
│   Mode.WithModel(modelID) wires both into Mode.SystemPromptFiles at
│   session start, /mode switch, and child task spawn.
│
├─ 3. DETERMINISTIC LANE FALLBACK (turn_planner.go)
│     prompt-shape → family (debug_logs / verification_only / locate /
│     architecture / implementation / etc.) — no LLM classifier call.
│     The family gates two intent-based runtime contracts (below).
│
├─ 4. PROMPT STACK (promptstack/stack.go) — auxiliary tiers
│     ├─ inline:       chat.SystemPrompt
│     ├─ baseline:     legacy chat.SystemPromptFiles (still loadable)
│     ├─ profile:      <profiles_dir>/<name>.md
│     ├─ user_global:  ~/.config/idiom/{IDIOM,AGENTS,CLAUDE}.md
│     └─ project:      .qwen.md → QWEN.md → IDIOM.md → AGENTS.md → CLAUDE.md
│                       → .idiom/discovery/context.md
│
├─ 5. RUNTIME PREFLIGHT (runner_lane_policy.go) — minimal gate
│     ├─ conversation_only            → reject all tools
│     └─ read_only_verification_shell → force `go test`/`go build` first,
│                                       reject post-verification drift
│   (everything else passes through; the model's behavior is shaped
│   by the per-model system prompt, not by runtime redirects)
│
└─ 6. INSTRUCTION ATTACHMENT (instructions.go — same algorithm as OpenCode)
      file touch → walk up to nearest AGENTS.md / CLAUDE.md, at-most-once per session
```

## Substantive differences (still present)

| Dimension | OpenCode | idiom |
|---|---|---|
| **Prompt-by-model** | 13 prompt files keyed off model id | 5 prompt files (qwen / anthropic / gpt / gemini / default) |
| **Prompt-by-mode** | Light: only plan adds plan.txt | Mode fragment via SelectModeFragment, applied uniformly |
| **Tool decisioning** | LLM-driven with prompt hints + agent permission rules | Same — runtime preflight no longer rewrites tool calls |
| **Subagents** | `general` (`@general`) + hidden compaction/title/summary | `task` for delegation; child task brief drives a separate Mode |
| **Instructions attach** | session/instruction.ts walks up | instructions.go ports same algorithm |
| **Discovery toolset** | None (generic file/grep tools only) | First-class: `discovery_query`, `*_workspace_symbol`, `*_read_symbol`, `*_calls_*`, `structural_search`, `dependency_graph`, `history_*`, `blame_range`. Made privileged in every per-model prompt. |

## Simplification deltas (April 2026)

These pieces were deleted in the OpenCode-alignment pass:

- **LLM lane classifier** in `turn_planner.go`. The `classifyTurnRouteWithModel`
  function (a 256-token Qwen call to label each turn with a workflow
  family) and its `correctTurnRoutePlanForPrompt` post-correction were
  removed. The deterministic prompt-shape `fallbackTurnRoute` was
  authoritative in every corrective branch, so the model call only added
  latency, an extra failure mode, and a `route_classifier` server dependency.
- **Preflight redirect chain** in `runner_lane_policy.go` (~120 lines of
  body). The redirects covered: delegated_read_only bootstrap (multiple
  variants), secondary-evidence redirection, named-file-read redirection,
  search-hit-window redirection, integration pivots, post-readiness
  symbol-body upgrades, premature-target-read rejection, evidence-first
  read budgets, write-phase enforcement, debug-log phase scope,
  debug-log followups, and post-evidence drift. They were Qwen
  handholding written before the discovery-first system prompt existed;
  once `prompts/opencode/qwen.md` makes the contract explicit, the
  redirects become the wrong shape (the prompt asks the model to do X,
  the preflight rewrites it to Y).
- **Orphaned helpers** in `runner_lane_policy.go` (~700 lines): the
  helper functions that supported the deleted redirects
  (`delegatedReadOnly*`, `debugLog*`, `prematureTargetRead*`,
  `evidenceFirstTarget*`, etc.). Two utility helpers
  (`delegatedReadOnlyLiteralSymbolCandidate`, `appendUniqueSearchTerm`)
  were kept because `runner.go` and the answer-composer still use them.
- **Tests pinning deleted preflight behavior** in `runner_test.go` (~930 lines):
  31 test functions that called the deleted helpers directly or asserted
  on the redirected `applyLanePreflight` output were deleted along with
  their features.

What remained intact:

- `applyLanePreflight` itself (the function), reduced to two
  intent-based contracts: `conversation_only` (reject all tools) and
  `read_only_verification_shell` (force verification first). These match
  user-prompt shapes that already exist in idiom's deterministic fallback.
- `delegatedReadOnlyAnswerReadyAfterResult` and
  `readOnlyAnalysisAnswerReadyAfterResult` plus their dependencies. These
  are answer-readiness signals (when to stop and answer), not redirects.
  They drive the runner's loop-exit, not the model's tool choice.
- The `RouteClassifier` config block stays for **history summarization**
  (a separate use of a small secondary model), which idiom uses for
  context compaction. Lane-classification consumption is gone.

LOC impact (Go source, post-simplification):

- `runner_lane_policy.go`: 1034 → 302 (-732)
- `turn_planner.go`: 520 → 355 (-165)
- `runner_test.go`: 7687 → 6756 (-931)
- New: `prompts/opencode/*` (8 files, ~600 lines of prompt content)
- New: `promptstack/selector.go` + tests (+147 lines)
- New: `Mode.WithModel` in `agent/modes.go` (+30 lines, +tests)

## Summary

OpenCode shapes the turn almost entirely through the system prompt
(selected by model fingerprint, lightly modified by agent), then trusts
the LLM. idiom now does the same — with one difference. Its per-model
prompts make idiom's discovery toolset a first-class, privileged path
that the model is told to reach for before generic file/text tools. The
runtime preflight that used to enforce discovery-first via redirects
was deleted; the system prompt carries that contract instead.
