[2m  ▎ thinking[0m
I'll review each specified file to assess whether the revised proposal is implementable as a localized Harness/Go change without duplicated authority or mutation-lane leakage.
  · 17197 tok · in 16991 (13312 cached / 3679 new) · out 206 (43 reasoning) · ¥0.0044
  -> read_file {"path": "docs/proposal-read-only-named-path-evidence-prefetch.md"}
  -> read_file {"path": "docs/debugging-reviewer-bench.md"}
  -> read_file {"path": "docs/decisioning-north-star.md"}
[2m  ▎ thinking[0m
Now let me read the implementation files.
  · 28357 tok · in 28117 (13312 cached / 14805 new) · out 240 (17 reasoning) · ¥0.0156 · cache prefix changed: tools
  -> read_file {"path": "internal/runtime/agent/final_answer_acceptance.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_answerability.go"}
  -> read_file {"path": "internal/runtime/agent/read_only_evidence_packet.go"}
  -> read_file {"path": "internal/runtime/agent/direct_answer_surface.go"}
  -> read_file {"path": "internal/capabilities/workspace/manager.go"}
  · tool output truncated: 15361 of 48129 bytes elided
  · tool output truncated: 40672 of 73440 bytes elided
[2m  ▎ thinking[0m
Let me check the existing path extraction utility and the brief's evidence path tracking.
  · 62062 tok · in 61774 (28288 cached / 33486 new) · out 288 (65 reasoning) · ¥0.0346
  -> explore {"task": "In the idiom codebase at /Users/ben/Documents/GitHub/idiom, find the pathparse.MentionedWorkspacePaths functio...
  -> grep {"pattern": "pathparse\\.MentionedWorkspacePaths", "path": "/Users/ben/Documents/GitHub/idiom"}
  -> mcp__codegraph__files {"path": "/Users/ben/Documents/GitHub/idiom"}
  -> mcp__codegraph__search {"query": "MentionedWorkspacePaths", "projectPath": "/Users/ben/Documents/GitHub/idiom"}
  -> mcp__codegraph__search {"query": "readOnlyObjectiveEvidencePaths", "projectPath": "/Users/ben/Documents/GitHub/idiom"}
  -> mcp__codegraph__context {"projectPath": "/Users/ben/Documents/GitHub/idiom", "task": "pathparse.MentionedWorkspacePaths function implementation ...
  -> mcp__codegraph__node {"projectPath": "/Users/ben/Documents/GitHub/idiom", "symbol": "MentionedWorkspacePaths", "includeCode": true}
  -> mcp__codegraph__node {"projectPath": "/Users/ben/Documents/GitHub/idiom", "symbol": "readOnlyObjectiveEvidencePaths", "includeCode": true}
  -> mcp__codegraph__explore {"projectPath": "/Users/ben/Documents/GitHub/idiom", "query": "pathparse.go pathparse ScopeToBriefBody mentionedWorkspac...
  -> mcp__codegraph__callers {"projectPath": "/Users/ben/Documents/GitHub/idiom", "symbol": "readOnlyAnswerabilityForResults"}
  -> mcp__codegraph__search {"projectPath": "/Users/ben/Documents/GitHub/idiom", "query": "final_answer_acceptance"}
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/final_answer_acceptance.go"}
  -> mcp__codegraph__node {"includeCode": true, "projectPath": "/Users/ben/Documents/GitHub/idiom", "symbol": "readOnlyDirectAnswerCandidateOnlyLe...
  -> read_file {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent/read_only_answerability.go", "offset": 40, "limit": 2...
  -> mcp__codegraph__node {"includeCode": true, "projectPath": "/Users/ben/Documents/GitHub/idiom", "symbol": "acceptedAnswerEvidencePaths"}
  ⊘ explore sub-agent: paused after 5 tool-call rounds (agent.max_steps) — the work so far is saved; send another message to continue, or set max_steps higher or to 0 for no limit
[2m  ▎ thinking[0m
Now let me look at the existing path extraction utility to understand the reuse surface:
  · 70943 tok · in 68686 (61952 cached / 6734 new) · out 2257 (2190 reasoning) · ¥0.0125
  -> glob {"pattern": "internal/contracts/pathparse/*.go"}
[2m  ▎ thinking[0m
  · 71090 tok · in 71002 (68608 cached / 2394 new) · out 88 (34 reasoning) · ¥0.0039
  -> read_file {"path": "internal/contracts/pathparse/pathparse.go"}
[2m  ▎ thinking[0m
Let me also check where the initial context is assembled — the hook point for prefetch injection:
  · 76276 tok · in 76121 (71040 cached / 5081 new) · out 155 (39 reasoning) · ¥0.0068
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent", "pattern": "func.*prepareInitialContext|func.*build...
[2m  ▎ thinking[0m
  · 76389 tok · in 76290 (76160 cached / 130 new) · out 99 (13 reasoning) · ¥0.0019
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent", "pattern": "buildMessages|buildRequest|initialConte...
[2m  ▎ thinking[0m
  · 76519 tok · in 76403 (76288 cached / 115 new) · out 116 (29 reasoning) · ¥0.0019
  -> grep {"path": "/Users/ben/Documents/GitHub/idiom/internal/runtime/agent", "pattern": "chatMessages|systemMessages|runLoop|Run...
[2m  ▎ thinking[0m
I have all the evidence I need. Here is my review.

---

## Harness / Go Reviewer: Proposal Assessment

**Proposal:** `docs/proposal-read-only-named-path-evidence-prefetch.md`

---

### Verified facts

1. **`pathparse.MentionedWorkspacePaths` already extracts file paths from user objectives.** It is called by `readOnlyObjectiveEvidencePaths` (read_only_answerability.go:1032-1041), which feeds `NamedSourcePaths` and `MissingNamedSourcePaths` into the answerability ledger. The function does deterministic syntactic cleanup (backtick/quote stripping, line-suffix stripping via `StripPathLocationSuffix`) and rejects globs, symbols, and non-workspace file extensions. (pathparse.go:384-425, pathparse.go:441-453)

2. **A surface-gate for prefetch already exists but is intentionally inactive.** `direct_answer_surface.go` defines `DirectAnswerSurfaceCapSourceBodyPrefetch`, `DirectAnswerSurfaceVersion = "2026-06-11"`, and `DirectAnswerSurfacePrefetchEligibility()` which checks version + capability advertisement. The capability list in `directAnswerSurfaceCapabilities()` does not yet include `source_body_prefetch`. The comment reads "A1 must remain disabled until it is added here and the version is bumped." (direct_answer_surface.go:33-36, 131-137)

3. **`readOnlyDirectAnswerLane` is a well-tested, narrow route predicate.** It gates on `PermissionProfileIsReadOnlyWorkspace` + `readOnlyDirectAnswerTaskFamily` + `RuntimeContractIsDirectAnswer`. Mutation lanes have their own orthogonal check (`runnerBriefRequiresMutation`). There is no code path where both predicates are true simultaneously for the same brief. (read_only_answerability.go:138-148, final_answer_acceptance.go:67, 77)

4. **`readOnlyEvidenceItem` already has a `Source` field** (`ReadOnlyEvidenceFinding.Source`), and the evidence ledger tracks provenance through `readOnlyEvidenceItem.Kind`. Adding a new evidential `Kind` constant like `readOnlyEvidencePrefetch` fits the existing pattern. (read_only_evidence_packet.go:17-21, read_only_answerability.go:12-21)

5. **`final_answer_acceptance.go` processes evidence through `acceptedAnswerEvidencePaths` and `readOnlyDirectAnswerRelevance`, both of which consume `results []workspace.ToolResult`.** Prefetched content would need to enter the acceptance pipeline via the same `results` parameter — either as synthetic `ToolResult` entries or through a separate path into the `EvidencePaths` accumulation. There is currently no non-tool-result evidence path into acceptance. (final_answer_acceptance.go:31-41, 180-187)

6. **The workspace `Manager.readFile` performs the full path resolution, protected-path check, symlink escape check, size cap, and read permission check internally.** There is no public `resolveSafeReadPath(path) error` method that performs the policy checks without also reading the full content. The proposal's "policy adapter" would need to extract these checks into a reusable predicate. (manager.go — readFile implementation inferred from path-check patterns, not shown in full)

7. **The benchmark cited in the proposal (44.47s vs 19.43s) has a known confounding factor**: the log at `.idiom/benchmark/prompts/base.md` was missing. The proposal correctly flags this as "a confounding setup issue" before treating the delta as stable evidence. (proposal.md:27-29)

---

### Inferences

1. **The path extractor component is primarily a wrapper around `pathparse.NormalizeWorkspacePathToken` + `pathparse.StripPathLocationSuffix` + existence-on-disk check.** Most of the described behavior (syntactic cleanup, line-suffix stripping, backtick/quote stripping, glob/symbol rejection) already exists in the pathparse package. The extractor's marginal new work is: existence-on-disk check, line-suffix pattern support for `#L<N>` and `#L<N>-L<M>` (note: `:N` and `:N-M` are already handled by `StripPathLocationSuffix`), and the explicit caps/safety rules.

2. **The policy adapter is the riskiest new component.** It needs to extract the path-resolution and permission-checking logic from `Manager.readFile` (or the underlying path-cleaning helper) into a separately callable predicate. If done as a `Manager.SafeReadPath(path) (string, error)` method that returns the resolved, permission-checked canonical path without reading file content, it maps naturally to existing private logic. If done as a separate ad hoc check outside the Manager, it risks diverging from actual workspace read policy.

3. **Evidence ledger integration is the most consequential design choice.** There are two approaches:
   - **Synthetic ToolResult**: Wrap prefetched file bytes into `workspace.ToolResult{Tool: "prefetch", Summary: "...", Output: "..."}` entries. These flow through the existing `readOnlyAnswerabilityForResults` path without new acceptance-layer code. The `source:"prefetch"` attribution goes into `ReadOnlyEvidenceFinding.Source`. This is the simpler path.
   - **Separate evidence path**: Add a `PrefetchEvidencePaths` field to `finalAnswerAcceptance` that merges into `EvidencePaths` during acceptance evaluation. This is cleaner isolation but adds another merge operation in an already complex function.

4. **The proposal's deduplication rule** ("final-answer acceptance should see one evidence path while audit preserves both sources") is already how the ledger works: `SourceBodyPaths` is a set (via `mergeRunnerStateStrings`), and model re-reads of the same path produce no-op merges in the ledger. The `readOnlyEvidenceItem` list preserves both sources with distinct `Source` values. Prefetch + model-read of the same path would produce two items (one `source:"prefetch"`, one `source:"read_file"`) in `Items[]` but one entry in `SourceBodyPaths`.

5. **The proposed default of 6 files and bounded bytes** is conservative given that the benchmark's issue was ~71k characters of tool output. 6 files of ~8k each = ~48k characters, which is within range of the original problem without ballooning.

---

### Root cause

The first broken invariant for this proposal is:

> **When a read-only prompt explicitly names local source or design files, the harness should be able to inject known, permission-safe source evidence before the first model turn without deciding the model's answer or strategy.**

The benchmark showed that the existing architecture forced the model to spend one hosted-model round trip per read/outline/search step when the user had already named the exact candidate files. This is not a model-strategy failure — it is a transport architecture gap where the harness defers all evidence gathering to model-directed tool calls even when the candidate set is already known at prompt time.

---

### Contributing factors

- The benchmark had a missing `base.md` prompt, which inflated Id-iom latency relative to a better-configured run. This is a setup issue, not a design flaw, but it means the raw latency delta is unreliable as a proof of concept.
- `pathparse.MentionedWorkspacePaths` is already called in `readOnlyObjectiveEvidencePaths` and feeds the ledger's `NamedSourcePaths` — but currently only for path accounting, not for content injection. The mechanism to turn extracted paths into injected evidence does not exist.
- `DirectAnswerSurfacePrefetchEligibility` and its version gate exist but are wired to an empty capability list. The surface change is a one-line addition (`DirectAnswerSurfaceCapSourceBodyPrefetch` into the capability list) plus a version bump.

---

### Non-causes

- **Not a model refusal, classification error, permission failure, or completion-gate loop.** The proposal explicitly rules out these failure classes with benchmark evidence. (proposal.md:7-9)
- **Not a general answer-readiness or model-quality failure.** The model successfully gathered evidence in the benchmark; it just took too many serial tool calls to assemble it. The proposal targets transport efficiency, not answer quality.
- **Not a prompt-surface or protocol problem.** The model correctly chose `read_file`/`code_navigate`/`search_content` tools. The issue is that the harness had no mechanism to short-circuit the evidence transport given the user-provided file names.

---

### Fix recommendation

**Approve with changes.** The proposal is sound, route-local, and north-star aligned. The following four implementation cautions should be addressed:

1. **Extract a reusable `resolveSafeReadPath` method on `Manager`** (or equivalent in the workspace package) that checks path resolution, protected-path policy, symlink safety, file size, and binary content — all without reading the full file content into a tool result. The prefetcher calls this method for each candidate path; only paths that pass become prefetch content. This is the single most important implementation detail because ad hoc path validation outside the Manager is how policy drift starts.

2. **Prefer synthetic `workspace.ToolResult` entries for prefetched content over a separate evidence path.** Emit one `workspace.ToolResult` per prefetched file with `Tool: "prefetch"` (or a dedicated tool name). The existing `readOnlyAnswerabilityForResults` in `read_only_answerability.go` already classifies `concreteSourceEvidenceResult` results into `readOnlyEvidenceSourceBody` items. Either add `"prefetch"` to the concrete-source-evidence set, or handle it in a dedicated case alongside the existing `tool == "task"` packet path. This avoids adding a new merge operation in `final_answer_acceptance.go`.

3. **Activate the surface gate in the same commit that adds the prefetch behavior.** Add `DirectAnswerSurfaceCapSourceBodyPrefetch` to `directAnswerSurfaceCapabilities()` and bump `DirectAnswerSurfaceVersion`. Without this, `DirectAnswerSurfacePrefetchEligibility` returns `false`, and the proposal's A1 gate never activates. The prefetch tests should verify that `DirectAnswerSurfacePrefetchEligibility` becomes `true` when the capability is advertised.

4. **Reduce the default cap from 6 to 4 files in the first implementation.** The proposal says "Maximum files: 6 by default". Starting at 4 files limits prompt-ballooning risk while still covering the common case (1-3 files to inspect). This default can be raised to 6 after benchmark evidence shows it is safe. This is a conservative recommendation, not a required change.

---

### Regression tests

The test plan in the proposal at lines 179-209 is comprehensive. I recommend the following additions:

- **Policy parity test**: Verify that a path the workspace `Manager.readFile` would reject (protected path, symlink escape, binary, too-large, outside-root) is also rejected by the prefetcher's `resolveSafeReadPath` check, and vice versa. This prevents the policy adapter from diverging from actual workspace read policy.
- **Acceptance pipeline test**: Verify that a prefetched path (via synthetic `ToolResult`) passes through `acceptedAnswerEvidencePaths` and is counted in the `EvidencePaths` field of a successful acceptance. This catches regressions if the `concreteSourceEvidenceResult` classification changes.
- **Mutation-lane isolation test**: Construct a mutation-lane brief that happens to reference the same files as a read-only prefetch candidate. Verify the prefetch packet is never constructed or injected. The existing `readOnlyDirectAnswerLane` gate is the invariant; the test should verify the entire injection point is skipped.
- **Dedup + audit attribution test**: Prefetch path X, then have the model call `read_file` on path X. Verify that `EvidencePaths` has one entry and that the evidence `Items` list has two entries with distinct `Source` values (`"prefetch"` and `"read_file"`).

---

### North-star risk

**Low.** The proposal is cleanly aligned with the model-strategy / harness-contract boundary:

- The harness prefetches only exact named paths that exist on disk. It does not decide which paths are relevant to the answer.
- The model retains full authority to ask for more evidence, disregard prefetched content, or answer with uncertainty.
- Prefetched evidence is recorded with `source:"prefetch"` so audit can distinguish host-injected evidence from model-led reads.
- The proposal explicitly excludes direct-answer decisioning ("no forced 'answer now' gate"), tool-surface changes, and policy relaxation.

The only north-star risk I see is **scope creep through the policy adapter**: if the `resolveSafeReadPath` method grows affordances for path expansion, glob resolution, or content-type inference beyond what `readFile` does, it becomes a separate permission surface that could drift from workspace policy. The fix is to co-locate it with `readFile` or derive it from the same path-checking primitives, and to test policy parity (as recommended above).

---

### Confidence

**High (85%).** The proposal is well-scoped, identifies the correct invariant, cites benchmark evidence with appropriate caveats, names the exact files and ownership boundaries, includes explicit drift checks, and the existing codebase already has most of the scaffolding in place (path extraction, surface gate, evidence ledger, evidence-item provenance tracking). The two risk areas (policy adapter extraction, evidence ledger integration) have clear resolution paths.

The 15% uncertainty comes from: (a) the benchmark confounding factor means the latency improvement is not yet proven; the real gain could be smaller than the 44.47s → 19.43s delta; (b) the `readOnlyAnswerabilityForResults` function is already large (~170 lines) and complex; adding prefetch processing there needs care to avoid making the ledger's internal logic harder to reason about.

---

### Final verdict: **approve-with-changes**

The proposal is implementable as a localized Harness/Go change. It does not duplicate authority or leak into mutation lanes, provided these four implementation requirements are met:

1. **Extract `resolveSafeReadPath` on `Manager`** — do not write ad hoc path validation.
2. **Use synthetic `workspace.ToolResult`** for prefetch content — do not add a new evidence pipeline to `final_answer_acceptance.go`.
3. **Activate `source_body_prefetch` in the surface capability list** in the same commit.
4. **Add the three recommended regression tests** (policy parity, acceptance pipeline, mutation-lane isolation).
  · 90194 tok · in 86437 (76416 cached / 10021 new) · out 3757 (630 reasoning) · ¥0.0191
