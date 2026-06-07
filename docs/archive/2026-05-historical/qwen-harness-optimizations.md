# Qwen Harness Optimizations — Comprehensive Backlog

**Status:** Living design doc. Captures the optimization surface for the
idiom-on-idiom Qwen workflow as of 2026-05-10.

**Question this answers:** "Where is the harness still limiting Qwen,
and what's the comprehensive list of things we could improve?"

## Background

We dispatch implementation work in this repo to local Qwen via the
five-step pipeline:

1. Claude writes a 1-paragraph spec.
2. Qwen drafts an implementation brief (`idiom lane refine` or `idiom
   lane implement`).
3. Qwen self-reviews the draft.
4. Claude spot-checks + patches issues.
5. Qwen ships the implementation (`idiom lane plan-execute`).

Today's session shipped 11 atoms (#47, #57, #58, #59, #60, #61, #62,
#63, #64, #65, #66, #67, #68, #69, #70, #71). Many wedges along the
way exposed harness friction. Web research on Qwen3's documented
quirks confirmed that *some* of our wedges are upstream model limits
(MLX 4-bit corruption on long structured output, weak self-critique,
endless reasoning loops in thinking mode), but *others* are harness
config that doesn't match Qwen3's recommended deployment shape (e.g.,
the `max_tokens=4096` cap was 8× below Qwen's recommended default).

This doc is the comprehensive backlog of harness optimizations
identified to date. Items marked **Shipped** are done; items marked
**Not shipped** are candidates.

## 1. Sampling & token plumbing

| Item | Status | Notes |
|---|---|---|
| Verify temp/top_p/top_k match Qwen3 quickstart | **Shipped #72** — 35B temp 1.0 → 0.6 (thinking-mode rec); coder unchanged (already correct) | |
| Per-lane `max_tokens` instead of one cap | Planner=16384 (#70 shipped); other lanes use default | Add `runOptions.MaxTokens` field, let refine draft/patch/review each pick |
| `presence_penalty=1.5` on retry after wedge | Qwen team's loop-mitigation recommendation | Wire as auto-bump on lane-replay |
| Audit hardcoded sampling fallbacks in agent runtime | Spot-check `internal/runtime/agent/` for any hardcoded temp/top_p that overrides backend config | Audit + remove |
| Planner `max_tokens` 4096 → 16384 | **Shipped #70** — directly addresses our most common wedge (submit_plan truncation on >300 line briefs) | |
| **Reduce 35B `ctx_size` from 262144 → 65536** | **Cannot ship via mlx_lm.server (#90)** — verified 2026-05-11 against installed 0.31.3 and upstream main: `mlx_lm.server` exposes 23 CLI flags, none is `--ctx-size`/`--context-length`/`--max-kv-size`. Context window is read from the model's tokenizer config at load time, not the CLI. Closest available levers: `--prompt-cache-bytes` (KV cache budget; dropped from 20GiB → 12GiB on both backends 2026-05-11 as Metal-pressure mitigation), `--prompt-cache-size`, `--max-tokens` (generation budget). Real fix needs either an upstream mlx-lm feature, switching the 35B to llama.cpp (where `--ctx-size` works), or editing the model's tokenizer config. | `cache_bytes` cut shipped; ctx_size flag plumbing parked pending upstream support |
| **6-bit 35B as fallback** | **Tried, incompatible** (#89) — `qwen3.6-35b-a3b-mlx-6bit` returned "System message must be at the beginning". Different chat template version | Need different 6-bit model or chat-template-args adaptation |

## 2. Refine loop quality

| Item | Status | Notes |
|---|---|---|
| Smarter bug-title parser | **Shipped #71** — filters "no fix needed" / "all X verified" / "no compile issue" | Could go further: severity-weighted bug count |
| Draft scratchpad artifact materialization | **Shipped 2026-05-15** — refine draft now routes through `idiom artifact --no-tools --mechanical-repair --output iter-N/draft.md` with `draft.artifact.jsonl`, `draft.report.json`, and typed `draft_state` metrics | Do not reintroduce `write_file` instructions in draft templates; use final-answer artifact closeout |
| Review meta-brief: separate `## Verifications` from `## Bugs found` | **Not shipped** — Qwen mis-formats verifications as bugs because the template only has one section | One-line template change in `cmd/idiom/lanes/refine/review.md` |
| Feed `idiom brief lint` output back to patcher as "Bug 0" | **Not shipped** — refine warns on lint failure but doesn't surface it to the next iter | Inject lint findings into the next patch meta-brief's "previous review" |
| Differentiate WARN vs FAIL in lint check at finalize | **Not shipped** — currently any non-zero exit is just "WARN... continuing" | Parse lint output, fail-loud on FAIL items, warn on WARN |
| Per-iteration max_tokens scaling | Later iters often need more headroom for larger patches | Trivial — pass `4096 + iter*2048` |
| Cross-session resume (`--resume`) | Deferred at #64 v1 | State directory shape already supports it; needs `--resume <title>` flag + iter-N enumeration |
| Confidence score in refine output | Deferred | Compute from "iters to converge" + "open bugs at finalize" |
| Best-draft tracking + no-improvement stop | **Shipped #66** | |
| Ship best, not latest, on max-iters exhaust | **Shipped #66** | |
| Brief lint runs on final draft before ship | **Shipped #66** (currently warns; see WARN/FAIL split above) | |

## 3. Lane shape & completion gates

| Item | Status | Notes |
|---|---|---|
| Lane-aware completion gate semantics | **Real bug observed today** — refine patch step writes a doc file, gate demands `source_sanity` that doesn't apply to docs | Separate "doc-write" gate that accepts `git diff --check` or path-suffix-based logic |
| Implement preamble "final action MUST be write_file" | **Shipped #60** — fixed implementation/review writer wedges; refine draft is now artifact-owned instead | Keep this out of draft artifact prompts |
| `source_sanity` recognizes more file types | Currently Go-only; markdown/YAML/JSON should have their own minimal sanity | Audit gate-input classification |
| Audit `--coder` flag vs plan-execute auto-route interactions | Make sure these don't fight | |
| Stale-brief gate (commit + mtime aware) | **Shipped #61, #62** | |

## 4. Brief authoring & decomposition

| Item | Status | Notes |
|---|---|---|
| Pre-dispatch atom-size estimator | **Not shipped** — would have caught today's task #64 (700 lines, 10 edits) before wedging | Compute estimated output tokens from brief, warn if > planner max_tokens / 2 |
| Auto-decomposition suggester | **Not shipped** — when atom violates ≤5 files/≤300 lines rule, suggest sub-atom split | Lint warning today; could become "here are 3 suggested sub-atom names" |
| Brief lint catches "all NEW FILE rows + inlined content" → suggest Claude-direct write | The "Brief-fully-inlines-final-content" trap entry exists in `qwen-known-traps.md` but lint doesn't detect it | Parser change |
| Brief lint catches language-tagged fence on content rows as FAIL not WARN | Today's task #68 brief had ` ```out.total++ ` fences — lint warned, didn't fail | Promote to FAIL |
| Spec quality gate before refine | If the 1-paragraph spec is too vague, refine commits to plausible-but-wrong interpretations (today's #68 provenance heuristic case) | "Spec lint" pass that checks for verifiable claims |
| Brief preflight NEW FILE row exemption | **Shipped #57** | |
| Brief preflight outer-fence detection | **Shipped #59** | |

## 5. Discovery / context efficiency

| Item | Status | Notes |
|---|---|---|
| Pre-load brief-cited file sections into prompt | Currently Qwen reads files itself; could pre-include the relevant ranges based on brief anchors | Significant prompt-construction work |
| Dedupe repeated reads within session | Lane re-reads same file at slightly different ranges — mostly harmless, sometimes wasteful | Read-cache layer |
| Better `code_navigate` defaults | Today's logs show 12+ `code_navigate` calls per wedge | Audit which calls returned useful info vs noise |
| Discovery tool descriptions match Qwen's expectations | Some tools have terse descriptions; clearer hints on when to use what could reduce thrashing | Audit `tool_schemas` |

## 6. Concurrency / queue / state

| Item | Status | Notes |
|---|---|---|
| Cross-pool deadlock fix | **Shipped #69** — extended reentrancy guard from same-pool to any-pool | |
| Daemon idle-detection: flag in-flight jobs with no MLX activity for >N min | **Not shipped** — would have caught today's deadlock in 5 min vs 16 | Daemon-side timer + status-payload field |
| `idiom queue cancel <jobid>` reaps grandchildren | Mentioned in queue help; verify it actually works | Bug observed today: killing parent left grandchild PID 68008 alive holding the slot |
| Stale-plan cleanup at session END too | Currently only at START (#58) | Trivial |
| Stale-refine state cleanup (>N days) | Currently `.idiom/refine/` grows forever | Same shape as #58 |
| Per-atom git worktree isolation | Currently atoms share working tree; concurrent atoms risk overlap | Bigger change; would need worktree-aware lane wrappers |
| Session-scoped plan artifact lookup | **Shipped #10, #61** — sessionID + sessionStart filters | |

## 7. Observability / debugging

| Item | Status | Notes |
|---|---|---|
| Per-iteration metrics in refine state dir (timestamps, token usage, dispatch durations) | **Not shipped** — would help calibrate where refine spends time | Add `iter-N/metrics.json` |
| Lane history records token usage per dispatch | **Not shipped** — `appendLaneHistory` doesn't capture this | Plumb from agent runtime |
| "Why did this wedge?" diagnostic CLI | **Not shipped** — manually grepping `/tmp/*.log` today | `idiom lane explain <session-id>` parses events, classifies wedge shape |
| Brief provenance tracking (refine vs claude-direct) | **Shipped #68** | |
| `idiom lane history --since 1h --failed` filters | Currently dumps everything | Trivial |
| `idiom lane history` summary output | **Shipped #63** | |

## 8. MLX runtime workarounds

| Item | Status | Notes |
|---|---|---|
| `--35b-6bit` auto-routing for large atoms | Already in `start_local_qwen.sh`; not auto-selected | Pre-dispatch heuristic: if expected output > 800 lines, suggest 6-bit |
| Forward `chat_template_kwargs` on MLX requests | **Shipped 2026-05-12** — installed `mlx_lm.server` 0.31.3 accepts request-level `chat_template_kwargs`; idiom now forwards Qwen thinking/preserve knobs only when a caller intentionally overrides server defaults | Enables one 35B server to serve thinking, direct/read-only, and refine no-thinking routes on current MLX |
| Drop duplicate 35B no-think backend | **Shipped 2026-05-12** — `idiom lane refine` now targets `qwen3.6-35b` with request-level `--no-thinking`, so qwen-pair no longer needs a separate `qwen3.6-35b-nothink` MLX process | Reclaims the duplicate 35B process RSS after restarting the backend profile |
| Disable MLX native tool parser | **Shipped 2026-05-14** — MLX Qwen3 Coder tool parsing threw Python exceptions on malformed multi-line generated tool arguments, causing client-side `EOF`; idiom now omits native `tools` arrays for MLX and keeps tool parsing/validation in the harness JSON protocol | Converts parser crashes into normal harness parse/recovery behavior instead of dropped sockets |
| Add Qwen3.6-27B planner profile | **Shipped 2026-05-12** — Idiom and Colony now both declare `qwen-27b-pair`: 27B-6bit planner/classifier on `:47783` plus the existing coder executor | Eval target for atom-spec / local orchestrator work without replacing the stable 35B profile |
| Multiple MLX instances per pool with different sampling configs | Heavy infrastructure | Speculative |
| KV-cache reuse across lanes for shared context | MLX supports prompt cache; we don't optimize for it | Investigate |

## 9. Workflow discipline / orchestrator-side

| Item | Status | Notes |
|---|---|---|
| `lane history` provenance ratio surfaces in session footer | "5/7 Claude-direct this session" creates discipline pressure | UI/wrapper change |
| Hard rule: if patching refine output > N lines, abort and re-spec | Today's recurring pattern: Claude keeps falling back to direct-write after refine produces ~70% | Discipline rule (memory entry), not code |
| Pre-dispatch brief preview ("here's what I'd send") for fast review | Currently the orchestrator reads the whole brief | `idiom brief preview <path>` summary command |
| `make build` instead of manual `go build -o ./idiom` | Today's lesson — manual build leaves canonical install at `~/.local/bin/idiom` stale | Discipline; ensure all callers use `make build` |

## 10. Tool-call format

| Item | Status | Notes |
|---|---|---|
| Reduce optional fields on `submit_plan` edits | Qwen3 has documented loops on multi-optional-param tool calls (llama.cpp #20164) — our `PlanEdit` struct has many optional fields | Make `purpose` required, etc. |
| Verify native parser is actually selected | Llama-compatible backends still use native OpenAI tool calls where reliable; MLX intentionally does not, because its current Qwen parser failure mode is an unstructured socket close | Check only for non-MLX backends |

## 11. Brief-drafter knowledge

| Item | Status | Notes |
|---|---|---|
| Upstream Qwen3 quirks documented in `qwen-known-traps.md` | **Shipped today** — new section linking 5 documented Qwen3 issues to wedge classes we've observed | |
| NEW FILE > append-to-existing-file rule | **Shipped** in `qwen-known-traps.md` | |
| Brief-fully-inlines-final-content → write directly | **Shipped** in `qwen-known-traps.md` | |

## Recommended next-3 to ship (highest immediate impact)

1. **Sampling param alignment with Qwen3 quickstart.** Backends.json has 35B at temp=1.0; Qwen3 docs recommend 0.6 in thinking mode. Single config edit + MLX restart, likely a noticeable quality bump on planning tasks.
2. **Lane-aware completion gates.** Today's refine patch step hit `source_sanity` false-positive on markdown writes — twice. The gate doesn't know "doc-write" is a different shape from "code-edit." Real wedge-cause we keep hitting.
3. **Daemon idle-detection.** Today's deadlock cost 16 minutes before manual diagnosis. A 5-minute flag in `queue status` would surface this class immediately.

## Findings reference

- **MLX vs llama.cpp on M5 Max:** stay on MLX. ~2-3× decode advantage on Qwen3-A3B MoE; ~2-2.5× prefill advantage. llama.cpp's `chat_template_kwargs` per-request support is its main edge but is buggy on Qwen3.5/3.6 anyway. See research summary in session log.
- **MLX caveat for `thinking_budget`:** Verified 2026-05-12 on installed `mlx_lm.server` 0.31.3 that request-level `chat_template_kwargs` are honored for `enable_thinking=false` on Qwen3.6-35B-A3B. `thinking_budget` still needs separate quality testing before becoming a default cap; don't impose a global thinking cap in `.idiom/backends.json` because it can hurt complex planning.
- **Refine no-thinking memory win:** Refine uses `--no-thinking` per request against the primary 35B backend. Do not reintroduce a dedicated `qwen3.6-35b-nothink` server unless a future MLX regression stops honoring request-level `chat_template_kwargs`.
- **Self-critique is a documented Qwen3 weakness.** Refine review will continue to miss ~30% of bugs Claude catches; the workflow's value comes from *combining* Qwen-review and Claude-spot-check (different blind spots), not from either alone.
- **Atom shape is the dominant predictor of success.** ≤5 files, ≤300 lines, NEW FILE > append, single-purpose actions: ships clean. >10 files / >700 lines: hits Qwen's actual ceiling (MLX 4-bit corruption + weak architectural synthesis); decompose or Claude-direct.

## Maintenance

This doc should be updated when:

- Any Tier-1 item ships (move to "Shipped" status, add commit reference).
- A new wedge class is identified that doesn't fit existing categories (add a new category).
- Upstream Qwen3 / MLX changes invalidate a documented quirk.
- The "recommended next-3" should be re-ranked after each shipping round.

Cross-references:
- `.claude/rules/qwen-known-traps.md` — runtime trap entries Qwen lanes need
- `.claude/rules/qwen-brief-skeleton.md` — brief authoring discipline
- `feedback_qwen_first_workflow.md` (Claude memory) — workflow standardization rules
