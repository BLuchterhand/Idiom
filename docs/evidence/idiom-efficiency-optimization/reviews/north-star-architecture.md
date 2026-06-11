[2m  ▎ thinking[0m
  · 17171 tok · in 16933 (16640 cached / 293 new) · out 238 (104 reasoning) · ¥0.0011
  -> read_file {"path": "docs/design-direct-answer-efficiency.md"}
  -> read_file {"path": "docs/evidence/idiom-efficiency-optimization/ledger.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
[2m  ▎ thinking[0m
  · 26289 tok · in 26153 (17152 cached / 9001 new) · out 136 (14 reasoning) · ¥0.0096 · cache prefix changed: tools
  -> ls {"path": ".idiom/benchmark/20260611-122151"}
  -> grep {"path": "docs/design-direct-answer-efficiency.md", "pattern": "benchmark|artifact|trial|Reasonix|Idiom"}
[2m  ▎ thinking[0m
  · 27738 tok · in 27567 (26240 cached / 1327 new) · out 171 (24 reasoning) · ¥0.0022
  -> read_file {"path": ".idiom/benchmark/20260611-122151/task.txt"}
  -> ls {"path": ".idiom/benchmark/20260611-122151/idiom"}
  -> ls {"path": ".idiom/benchmark/20260611-122151/reasonix"}
[2m  ▎ thinking[0m
  · 28296 tok · in 27962 (27648 cached / 314 new) · out 334 (20 reasoning) · ¥0.0015
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-2.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/stdout-3.jsonl"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/idiom/time-3.txt"}
[2m  ▎ thinking[0m
  · 32167 tok · in 31685 (28288 cached / 3397 new) · out 482 (21 reasoning) · ¥0.0049
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/stdout-3.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-1.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-2.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/metrics-3.json"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-1.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-2.txt"}
  -> read_file {"path": ".idiom/benchmark/20260611-122151/reasonix/time-3.txt"}
[2m  ▎ thinking[0m
  · 33658 tok · in 33504 (32128 cached / 1376 new) · out 154 (16 reasoning) · ¥0.0023
  -> read_file {"path": ".idiom/comprehension/views/edc474559a0d4803.json"}
  -> grep {"path": "docs/design-direct-answer-efficiency.md", "pattern": "A1|A2|A3|A4|A5|A6|atom|parallel|serial"}
  · tool output truncated: 52332 of 85100 bytes elided
[2m  ▎ thinking[0m
---

# North-Star / Architecture Review: Direct-Answer Efficiency Optimization

## Verified facts

1. **Benchmark task** (`.idiom/benchmark/20260611-122151/task.txt`): "Read README.md and answer in exactly one concise sentence under 35 words: what is Idiom? End with Source: README.md." Both tools used hosted DeepSeek API `deepseek-v4-flash`.

2. **Idiom trial 1** (`stdout-1.jsonl`, `duration_ms=4649`): Model directly called `read_file` on `README.md` lines 1-1025 (full file). No search step. One tool call. `prompt_tokens=33827`, `total_tokens=33912`.

3. **Idiom trials 2-3** (`stdout-2.jsonl`, `stdout-3.jsonl`): Model called `search_files` for `readme.md` first (found 18 matches), then `read_file` lines 1-30 and 1-40 respectively. Two tool calls each. `prompt_tokens≈29500`, `total_tokens≈29600-29700`.

4. **Route telemetry (all 3 trials)**: `writer_route_decision` records `classifier_model`, `executor_model`, `planner_model` as `"deepseek-v4-pro"` — not the effective `deepseek-v4-flash` used by the benchmark config.

5. **Reasonix trials** (`metrics-1.json`, `metrics-2.json`, `metrics-3.json`): All opened `README.md` with bounded reads (limits 50, 60, 80). All exposed `cache_hit_tokens` (range 12k-33k), `cache_miss_tokens` (range 821-10,816), and cost (`¥0.0019` to `¥0.0116`). Two steps each (bounded read → answer synthesis). Idiom exposes none of these fields.

6. **Context packet** (`edc474559a0d4803.json`): `localization_confidence: "high"`, identifies 6 evidence paths including `final_answer_acceptance.go`, `discovery_redirect.go`, `prompt_routing.go`, `language_tools_adapter.go`, `pack_builders.go`, `read_symbol_candidates.go`. Index fresh after read-through refresh (871 files, 20k+ symbols).

7. **Design principles** (docs §2): Five principles — preserve strategy ownership, deterministic state before dispatch, gates as invariants, measure real cost surface, no prompt text authority. All explicitly stated before any proposed change.

8. **Atom plan** (docs §Dispatchable Atom Plan + ledger): 6 atoms (A1-A6), with parallelization plan, forbidden files, blast radii, and expected implementation boundaries documented per atom.

## Inferences

1. **Idiom's search-before-read is not deterministic.** Trial 1 read `README.md` directly (no search); trials 2 and 3 searched first. Same harness, same prompt. This means the behavior is model-strategy-dependent, not a harness invariant. The model decides whether to search or read; a harness prefetch would make this host-owned instead of model-strategy-dependent — which is exactly the architecture shift the design proposes.

2. **Reasonix's cost advantage is primarily from DeepSeek prompt caching, not superior architecture.** Reasonix trial 3 achieved `cache_hit_tokens=33,920` / `cache_miss_tokens=821` — a 97.6% cache hit rate — bringing cost to `¥0.0019`. That's DeepSeek's cached prompt prefix across repeated runs of the same task, not an architectural difference. Idiom cannot capture this advantage until it (a) reports cache metrics (A5), and (b) stabilizes its prompt prefix to make caching effective.

3. **The "full file read" in trial 1 is not inherently a bug.** `README.md` is a ~1k-line file whose first ~30 lines contain the answer. Reading the full file consumed ~23k extra prompt tokens but required only one tool call. The bounded read in trials 2-3 used fewer prompt tokens (29.5k vs 33.8k) but required two tool calls (search + read). There is a genuine latency-vs-tokens tradeoff here that the design does not fully acknowledge.

4. **The model-telemetry issue (deepseek-v4-pro vs deepseek-v4-flash) is a stale-alias propagation bug, not a routing bug.** The route correctly identified `task_family=conversation`, `writer_mode=read_only`, and `reason_codes=["route.read_only_question"]`. The role aliases came from the profile config, not from the actual model override. This is an attribution-transport issue, not a misrouting issue.

## Root cause (architecture level)

**The harness has no typed distinction between "evidence setup" (host-owned, deterministic) and "evidence strategy" (model-owned, heuristic)** for read-only direct-answer tasks. Both path resolution and answer synthesis flow through the same model-facing tool loop, forcing the model to decide whether to search first, read bounded or full, and whether to cite what it finds. Reasonix externalizes path resolution to a fixed prefetch step; Idiom leaves it to the model's tool-selection heuristic. The architectural fix — a typed `DirectAnswerEvidencePolicy` that resolves exact-named paths before model dispatch — is correctly scoped in the design (A1) and properly subordinated to the evidence pipeline rather than the answer pipeline.

## Contributing factors

1. **No exact-path detection before model dispatch.** The harness sees "Read README.md" as a natural-language instruction, not a structured request that names a workspace path. The routing layer classifies it as `direct_answer` / `conversation` but does not parse the explicit path mention.

2. **Model is shown full tool surface for all task families.** Direct-answer sessions see the same search/read/write/edit/plan tool repertoire as mutation sessions. A6 would fix this by projecting a read-only surface.

3. **Final-answer acceptance treats all path-looking strings equally.** The gate cannot distinguish `README.md` (legitimate citation) from `path/to/brief.md` (command example). A2 fixes this.

4. **No hosted cache/cost telemetry means optimization decisions are blind.** The benchmark can't tell whether the 30k prompt tokens are from instruction overhead, uncompressed evidence, or cache-miss rate. A5 fixes this.

5. **Route telemetry propagates profile aliases instead of effective model id.** A4 fixes this.

## Non-causes

1. **Not an agent-quality problem.** All three Idiom trials produced correct, well-grounded answers citing `README.md`. The issue is efficiency, not correctness.

2. **Not a prompt-wording problem.** The design correctly avoids adding another reminder to existing prompt files (Principle 5). The fix is in typed runtime state and tool policy, not prompt text.

3. **Not a context-packet quality problem.** The context packet (`edc474559a0d4803`) has `localization_confidence: "high"` and correctly identifies the relevant implementation files.

4. **Not a search-tool problem.** `search_files` working correctly (found 18 matches) is evidence the tool is fine — the model choosing to call it when the file name is already known is the inefficiency.

5. **Not a routing problem.** The route correctly identified read-only, conversation, direct-answer contract. The routing is sound; the evidence setup before model dispatch is what's missing.

## Fix recommendation

**Approach**: Implement as specified in the design's atom plan. The architecture boundaries are correctly drawn. Specific recommendations:

1. **Implement A6 (lightweight surface) before A1 (prefetch).** The design says they should be serialized but doesn't say which goes first. A6 defines the compact direct-answer surface that A1's prefetch operates within. A6 first, then A1. Update the ledger's serialization note accordingly.

2. **A1 must go through the evidence pipeline, not the system-prompt pipeline.** The design says "recorded as normal source-body evidence, not as a hidden answer" — this is the critical architecture invariant. Enforce it with a typed contract: the prefetch result must use the same `EvidencePacket` / `AcceptedSourceBody` schema as a tool-read result. If an implementation shortcuts this by stuffing content into the system prompt as pre-answered text, it violates the north-star boundary.

3. **A6 should publish a concrete surface inventory before coding.** The design says "read-only tools only; no writer/edit/plan-execute protocol text; no mutation-lane terminal states." This should be compiled into a documented allowlist/denylist of tools, prompt sections, and terminal states before implementation. A test should assert the inventory matches expectations.

4. **A2 citation classifier must be a single shared helper.** The design already says this — "Do not add a local 'ignore this string' list to one presentation layer." Strongly enforce this in review. The function should live in `internal/runtime/agent/` (or a shared `internal/contracts/evidence` package) and be imported by acceptance, telemetry, and fallback composition — not duplicated.

5. **A5 should not store raw provider response bodies.** The design says "Unknown provider fields should be preserved only as bounded structured metrics, not raw response bodies." Add an explicit test that raw provider JSON is never persisted in telemetry or metrics storage.

6. **Serialization order**: A6 → A1 (serial, in that order). A2, A3, A4, A5 in parallel after A6 is drafted (they touch disjoint files per the ledger's `forbidden_files`). Final integration replay after all atoms.

## Regression tests

| What to test | How | Current baseline |
|---|---|---|
| Exact-path direct answer doesn't search before read | `read_file` appears without prior `search_files` in conversation log | Present in `.idiom/benchmark/20260611-122151/idiom/stdout-1.jsonl` |
| Bounded read default avoids full-file reads for simple prompts | First `read_file` for `README.md` has `limit ≤ 100` | Trials 2-3 show this already; trial 1 shows full-file risk |
| Command placeholders don't fail accepted evidence | Benchmark with `path/to/brief.md` in output passes | Identified as earlier failure (design line 53-56) |
| False source citations still blocked | Uninspected `internal/runtime/agent/runner.go` cited in answer → still fails | Baseline: must fail |
| `reasoning_content` protocol error does not recur | Replay fixture with DeepSeek thinking-mode response | Reported at design line 57-58 |
| Route telemetry shows `deepseek-v4-flash` as effective model | `writer_route_decision` or model-call event contains `effective_model: "deepseek-v4-flash"` | Current: `deepseek-v4-pro` (all 3 trials) |
| Hosted cache/cost metrics present in session summary when provider supplies them | `cache_hit_tokens`, `cache_miss_tokens`, cost fields in JSONL | Absent in all 3 Idiom trials; present in Reasonix `metrics-*.json` |
| A6 surface excludes mutation tools | Tool manifest for direct-answer session has no `write_file`, `edit_file`, `plan-execute`, `plan_scope` | Current: mutation surface is visible (design §3) |
| Full `go test ./...` passes | No regressions from runtime/agent, llama, surfaces changes | Run after integration |

## North-star risk

**Assessment: Low risk, well-mitigated.**

The design is north-star aligned. Specific checks:

| Risk | Threshold | Verdict |
|---|---|---|
| **Strategy vs contract boundary** | Harness may prefetch evidence but must not write the answer | **Clear**. Design Principle 1: "The model still decides how to synthesize the answer." Prefetch goes through evidence pipeline, not answer pipeline. |
| **Gate weakening** | Citation classifier must not let false citations through | **Safe**. Design says "command/example placeholders should not make an otherwise grounded answer fail" AND "answers that falsely cite uninspected source paths still fail." Both are required. |
| **Scenario-specific hardcoding** | No magic strings, path lists, or prompt phrases | **Clean**. All changes in typed routing state, tool policy, telemetry, or acceptance logic (Principle 5). |
| **Compensating-fix chains** | Each atom fixes a distinct boundary | **Good**. A1 (evidence setup), A2 (evidence acceptance), A3 (client protocol), A4 (telemetry), A5 (metrics), A6 (surface) — all different harness boundaries. |
| **Local-model operations compatibility** | Changes must not break llama.cpp / MLX / textual-tool backends | **Adequate**. A3 has a capability check guard. A5 makes cache/cost fields optional. A1 prefetch is tool-agnostic (file read). No change depends on hosted-only features. |

**One significant risk**: **A6 surface slimming has the most architectural blast radius** (touches `prompts/` and `internal/runtime/agent/**`). If the lightweight surface removes a tool that a legitimate read-only investigation needs (e.g., `search_files` for exploring a new codebase, `code_navigate` for symbol-level reading), the user will hit a "tool not available" error in a scenario that shouldn't be restricted. The design acknowledges this in risks (line 321-322) but should produce a **concrete surface inventory** before implementation: exactly which tools, prompt sections, and terminal states are removed, and which remain. This inventory should be reviewed by the full bench before coding starts.

## Confidence

**High** for the architecture alignment assessment. The evidence is concrete (3 JSONL trial transcripts, 3 Reasonix metric files, task definition, route events), the design principles are explicit and north-star consistent, and the atom boundaries are well-documented. Confidence is **Moderate** for the implementation-level claim that all atoms are genuinely disjoint — the expected files listed in the ledger for A1, A2, and A6 overlap around `internal/runtime/agent/`, and A6's `expected_files: ["internal/runtime/agent/**"]` is deliberately broad. The ledger's `forbidden_files` constraints provide some protection but the actual separation depends on localized implementation choices not yet documented.

---

## Decision: PASS_WITH_RISKS

**Conditions for passing to BLOCKED** (none met):

- Design violates north-star strategy-vs-contract boundary: **No**. Prefetch is correctly scoped as evidence, not answer.
- Design weakens invariant gates: **No**. Citation classifier makes gates smarter, not weaker.
- Design introduces scenario-specific hardcoding: **No**. All changes in typed runtime state, tool policy, telemetry.
- Design cannot work on local/textual-tool backends: **No**. Prefetch and bounded reads are tool-agnostic.

**Conditions for upgrading to PASS** (both should be satisfied before implementation):

1. **A6 publishes a surface inventory** (exact tool allowlist/denylist, prompt section diffs, terminal state removals) before implementation begins.
2. **Ledger updated** to specify A6 → A1 serialization order (not just "serialized or coordinated").

**Carried forward as architecture obligations** (must be enforced during review):

- Prefetch goes through evidence pipeline, not system prompt. Test asserts same schema as tool-read evidence.
- Citation classifier is a single shared helper, not duplicated across acceptance/telemetry/composition.
- A6 surface inventory must not silently remove `search_files`, `code_navigate`, `grep`, or `structural_search` — legitimate read-only investigation tools.
- A5 never stores raw provider response bodies.
- A3 protocol repair is gated by backend capability check; non-DeepSeek tests are unchanged.
  · 49527 tok · in 43947 (33536 cached / 10411 new) · out 5580 (2117 reasoning) · ¥0.0222
