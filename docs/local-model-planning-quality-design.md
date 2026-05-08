# Local Model Planning Quality Design

## Objective

Make idiom strong enough that a local 35B-class long-context model can produce
frontier-quality technical plans on bounded software engineering tasks when the
needed facts are available locally.

This does not mean the harness should become the strategist. The quality target
is to reduce the model's uncertainty and context waste until the remaining work
is mostly reasoning over a compact, evidence-linked representation of the
codebase.

North star: the model owns strategy; the harness owns contracts. See
[decisioning-north-star.md](decisioning-north-star.md).

This is a follow-up to
[codebase-comprehension-memory-design.md](codebase-comprehension-memory-design.md).
That document defines the persistent multi-resolution repository graph. This
document defines the next layer: planner-quality context compilation, impact
ranking, plan critique, telemetry, and evaluation loops.

## Executive Summary

The core bet is plausible, but only with harness help. A Qwen3.6-35B-A3B style
local model has a large enough window for deep repository work: Qwen's official
model card documents native context up to 262,144 tokens and serving modes for
tool use. Frontier systems such as Claude Opus 4.6 now advertise 1M-token
context and stronger long-context planning, so raw window size is still a gap.
Research also shows that long-context models do not reliably use all positions
in a large prompt, especially facts buried in the middle.

The right response is not to dump more files into 256k. It is to make idiom a
planner-quality evidence compiler:

- Keep a fresh language-agnostic repository graph.
- Rank, budget, and package task-relevant evidence at multiple resolutions.
- Attach provenance, freshness, and uncertainty to every summarized claim.
- Give the model exact zoom handles instead of raw context floods.
- Audit the model's plan for unsupported claims and missing validation.
- Replay real runs and telemetry to improve tool affordances without hardcoding
  one project or one failure mode.

The result should be measured as planning utility, not code coverage:

- affected-scope recall
- unsupported-claim rate
- stale-fact rate
- validation precision
- read-loop/edit-readiness behavior
- successful implementation after plan handoff

## Current Idiom Baseline

Verified on 2026-05-07:

- `context_status --refresh=true` reported an active `sqlite-graph-v1` store,
  freshness after refresh, and a dependency-expanded auto-refresh from a
  16-file stale/missing set to a root graph rebuild.
- The active status view reported 214 files, 4,719 symbols, and 24,607 edges for
  the current graph view. The auto-refresh rebuild reported 2,764 files, 74,723
  symbols, and 389,380 edges.
- `harness_audit` reported a 124-tool surface with no manifest/schema drift.
- Available backends included `rg`, `git`, `ast-grep`, `ctags`, `semgrep`,
  `joern-scan`, `gopls`, `clangd`, and `context_graph_sqlite`.
- Missing optional backends included Python, TypeScript, Rust, and Java language
  servers on this machine. The harness correctly reports those as capability
  gaps instead of hard failures.

Existing capabilities already aligned with this design:

- Architect packets externalize planning context before plan, critique, or
  executor brief generation.
- `discovery_query` runs layered deterministic probes and persists evidence.
- The context graph stores repository facts and summaries.
- Language tools expose definitions, references, calls, diagnostics, and
  related-file probes where the backend supports them.
- `harness_feedback` gives agents a path to report capability gaps that normal
  telemetry may not infer.

The remaining gap is not another discovery tool. It is a higher-quality
selection and critique layer that turns these facts into planner-grade working
sets.

## Relationship To Existing Discovery Tools

This design supplements and packages the existing discovery surface. It does not
replace it.

| Layer | Existing role | Follow-up role |
| --- | --- | --- |
| Language tools | Precise live probes for definitions, references, calls, diagnostics, and symbols. | Continue to provide exact evidence and refresh stale graph facts. |
| `discovery_query` | Runs layered task-specific probes and persists evidence packets. | Becomes an evidence source and gap-filler for packet generation. |
| Context graph | Stores reusable repository facts, summaries, edges, freshness, and capability reports. | Becomes the substrate for ranking, zoom handles, budgeted views, and impact analysis. |
| Architect packets | Carry task, constraints, files, anchors, risks, tests, and context evidence. | Gain a planning V2 schema with ranked scope, freshness, confidence, budget, and audit contracts. |
| Plan audit | Currently limited to existing packet/lint contracts. | Checks model plans against packet evidence before implementation. |

The useful mental model is:

```text
language tools + discovery_query + runtime traces
  -> repository graph
  -> budgeted views
  -> planning packet
  -> model-authored plan
  -> deterministic plan audit and optional critique
```

The harness should decide whether facts are fresh, supported, well-formed, and
within contract. The model should still decide which evidence matters for the
task strategy.

## Research Basis

### Long Context Is Necessary But Not Sufficient

Qwen3.6-35B-A3B is a credible local foundation because its official model card
documents native 262,144-token context, tool-call serving guidance, and
long-text handling recommendations. Anthropic's Opus 4.6 announcement and
support docs describe 1M-token context and explicit improvements in long-context
coding and agentic planning. That frames the practical gap: local models can
carry a large task state, but frontier systems have more room and stronger
native retrieval/reasoning behavior.

The "Lost in the Middle" paper is the warning label. It found that model
performance can degrade when relevant information is placed in the middle of a
long context, even for models designed for long inputs. The implication for
idiom is direct: a 256k window is a scarce planning surface, not a warehouse.
Critical evidence should be selected, structured, deduplicated, and positioned
deliberately.

Sources:

- [Qwen/Qwen3.6-35B-A3B model card](https://huggingface.co/Qwen/Qwen3.6-35B-A3B)
- [Anthropic Claude Opus 4.6 announcement](https://www.anthropic.com/news/claude-opus-4-6)
- [Anthropic context window support note](https://support.claude.com/en/articles/8606395-how-large-is-the-claude-api-s-context-window)
- [Lost in the Middle: How Language Models Use Long Contexts](https://arxiv.org/abs/2307.03172)

### Retrieval Must Be Adaptive And Evidence-Aware

Classic Retrieval-Augmented Generation showed that combining parametric model
knowledge with external retrieved memory can improve factuality on knowledge
tasks. Self-RAG adds the important planner lesson: retrieval should be
conditioned on whether it is needed and whether retrieved passages are useful.
Adaptive-RAG makes the same point from a query-complexity angle: not every task
needs the same retrieval depth.

For idiom, this argues for a context compiler that can choose a budget tier and
surface evidence gaps without forcing the model's next step. The harness should
answer, "what reliable evidence is already available, what is stale, and what
resolution can be zoomed next?" It should not answer, "the model must call tool
X next."

Sources:

- [Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks](https://papers.neurips.cc/paper_files/paper/2020/hash/6b493230205f780e1bc26945df7481e5-Abstract.html)
- [Self-RAG: Learning to Retrieve, Generate, and Critique through Self-Reflection](https://arxiv.org/abs/2310.11511)
- [Adaptive-RAG: Learning to Adapt Retrieval-Augmented Large Language Models through Question Complexity](https://arxiv.org/abs/2403.14403)
- [From Local to Global: A Graph RAG Approach to Query-Focused Summarization](https://arxiv.org/abs/2404.16130)
- [Microsoft GraphRAG project](https://www.microsoft.com/en-us/research/project/graphrag/)

### Repository-Level Coding Needs Graphs, Impact, And Interface Design

CodePlan frames repository-level coding as planning over dependency analysis,
may-impact analysis, prior edits, and repository-derived context. RepoGraph
shows that a repository-level code graph can improve multiple AI software
engineering systems as a plug-in navigation layer. Aider's repo map shows a
practical version of the same idea: tree-sitter extracts symbols, graph ranking
selects relevant portions, and only the useful map enters the prompt.

SWE-agent shows that the agent-computer interface itself affects performance.
Agentless is also relevant because it demonstrates that localization and repair
can work well without a heavy autonomous agent loop. Together, these results
support idiom's north-star split: better tools, packets, and contracts can
improve model behavior without the harness taking over strategy.

Sources:

- [CodePlan: Repository-level Coding using LLMs and Planning](https://arxiv.org/abs/2309.12499)
- [RepoGraph: Enhancing AI Software Engineering with Repository-level Code Graph](https://proceedings.iclr.cc/paper_files/paper/2025/hash/4a4a3c197deac042461c677219efd36c-Abstract-Conference.html)
- [Aider repository maps](https://aider.chat/2023/10/22/repomap.html)
- [SWE-agent: Agent-Computer Interfaces Enable Automated Software Engineering](https://papers.nips.cc/paper_files/paper/2024/hash/5a7c947568c1b1328ccc5230172e1e7c-Abstract-Conference.html)
- [Agentless: Demystifying LLM-based Software Engineering Agents](https://arxiv.org/abs/2407.01489)

### Language-Agnostic Richness Requires Standard Integration Seams

LSP standardizes live language intelligence such as autocomplete, go to
definition, and find references. SCIP provides a language-agnostic index format
for definitions, references, and implementations. Kythe provides a
language-agnostic graph for build, compiler, semantic, cross-reference, and
cross-language information. Tree-sitter gives a practical incremental syntax
layer that is broad enough to cover many languages before precise semantic
backends are available. Joern's code property graph gives a deeper static
analysis path for AST, control-flow, and data-flow questions. Godot exposes a
GDScript language protocol surface, but its documentation marks the class as
experimental, so GDScript support should be treated as best-effort enrichment
with explicit confidence metadata.

The design implication: idiom should normalize every language into the same
graph vocabulary while preserving the source and confidence tier of each fact.
Go, C++, Python, TypeScript, Rust, Java, and GDScript should differ by
collector implementation, not by planner-facing concepts.

Sources:

- [Language Server Protocol](https://microsoft.github.io/language-server-protocol/)
- [SCIP Code Intelligence Protocol](https://github.com/scip-code/scip)
- [Sourcegraph precise code navigation](https://sourcegraph.com/docs/code-navigation/precise-code-navigation)
- [Kythe overview](https://kythe.io/docs/kythe-overview.html)
- [Tree-sitter](https://github.com/tree-sitter/tree-sitter)
- [Joern quickstart](https://docs.joern.io/quickstart/)
- [Godot GDScriptLanguageProtocol](https://docs.godotengine.org/en/latest/classes/class_gdscriptlanguageprotocol.html)

### Evaluation Must Reflect Real Software Work

SWE-bench frames software engineering evaluation around real issues, real
repositories, code edits, and test-based verification. That is closer to the
quality target than isolated code-generation benchmarks. However, idiom also
needs local replay suites because the harness is optimizing planning behavior,
not just final patch pass rates.

Source:

- [SWE-bench paper](https://openreview.net/pdf?id=VTF8yNQM66)

## Quality Target

Define "Opus-class planning utility" operationally rather than as a model
identity claim.

A local-model plan should be considered high quality when it:

- identifies the likely affected files, symbols, APIs, and tests
- separates verified facts from inference
- names the exact missing evidence needed before continuing
- explains relevant invariants and contracts without broad speculation
- proposes a bounded edit sequence that can be handed to an implementation lane
- avoids repeated read loops once evidence is sufficient
- avoids writing when protected-path, permission, or readiness contracts are not
  satisfied
- includes a validation plan tied to behavior, not reactive coverage
- reports harness/tooling gaps through telemetry when the tool surface was the
  blocker

The target is not that Qwen always matches Opus 4.6 on open-ended reasoning. The
target is that idiom can make bounded repository planning problems evidence-rich
enough that the remaining reasoning load fits the local model's strengths.

## Design Tenets

- Context packets are products, not dumps.
- The repository graph is a fact substrate, not a replacement for exact reads,
  language probes, tests, or runtime traces.
- Discovery tools remain the precision probes. The context layer packages,
  refreshes, ranks, and remembers their outputs.
- Summaries are typed observations with provenance and confidence, never silent
  facts.
- The harness may enforce contracts: freshness, protected paths, schemas,
  permissions, verification, loop breaks, and budget exhaustion.
- The harness should expose affordances: unresolved claims, stale scopes,
  candidate tests, related symbols, and available zoom handles.
- The harness should not hard-pick the model's next strategic step.
- Every feature must be language-agnostic at the planner API boundary.

## Proposed Capability Layers

### 1. Context Steward

Purpose: keep the 256k context window usable for planning instead of treating it
as an arbitrary file bucket.

Features:

- Add a deterministic `context_budget` planner used by packet generation and
  long-running sessions.
- Track token allocation by category: user task, rules, active plan, graph
  packet, source excerpts, tool results, conversation history, and reserved
  output/reasoning space.
- Detect duplicate evidence, oversized excerpts, stale summaries, and low
  evidence density.
- Support packet budget tiers: `brief`, `standard`, `deep`, and `max-local`.
- Emit budget health in audit telemetry.
- Prefer graph handles plus focused excerpts over full-file inclusion unless
  the file body is itself the evidence.

Planner-facing output should be advisory:

```json
{
  "budget_tier": "standard",
  "estimated_tokens": 42000,
  "reserved_output_tokens": 12000,
  "risk": "moderate",
  "warnings": [
    "two source excerpts overlap by 80%",
    "one summary is stale relative to dirty worktree"
  ]
}
```

Expected behavior tests:

- A packet with duplicate file excerpts is compacted or warned.
- A stale summary is labeled stale and excluded from high-confidence facts.
- A deep packet preserves exact evidence for required anchors before optional
  background summaries.

### 2. Planning Packet V2

Purpose: turn the graph and discovery outputs into a structured planning input
that is stable enough for small models and auditable enough for the harness.

Add a `planning_packet_v2` schema with these sections:

- `task`: user request, constraints, explicit scope, excluded scope
- `contracts`: protected paths, permissions, readiness gates, validation
  requirements
- `repository_view`: repo, subsystem, package, file, and symbol summaries
- `candidate_scope`: ranked files/symbols/tests with signal explanations
- `evidence`: exact excerpts, tool outputs, diagnostics, traces, and graph
  handles
- `freshness`: index id, git head, dirty fingerprint, stale nodes, refresh
  actions
- `unknowns`: exact unresolved claims and suggested probe categories
- `language_capabilities`: active precise backends and fallback tiers
- `handoff`: expected plan shape and implementation-lane readiness fields

Important distinction:

- `candidate_scope` may say why a file is relevant.
- It must not say "edit this file first" unless the user or a deterministic
  contract already made that mandatory.

Expected behavior tests:

- Required validation commands survive packet rendering.
- Stale graph facts appear under `freshness`, not as verified evidence.
- Missing language servers appear as capability gaps with fallback precision.
- A packet can be rendered without any model call.

### 3. Multi-Resolution Evidence Compiler

Purpose: compile graph nodes, summaries, exact source, and tool evidence into
task-shaped views.

Features:

- Build repository, subsystem, package, file, symbol, test, and trace views from
  the same graph.
- Give every view stable handles that allow zooming to exact source ranges.
- Preserve provenance for every summarized claim.
- Attach confidence tiers:
  - `exact_source`
  - `precise_backend`
  - `syntax_backend`
  - `heuristic`
  - `model_summary`
  - `stale`
- Allow deterministic summaries and optional model summary overlays to coexist.
- Add `what_this_proves` and `what_this_does_not_prove` fields to context views
  and discovery packets.

Expected behavior tests:

- File-level summaries do not claim behavior unsupported by symbols or exact
  excerpts.
- Model-generated summary overlays never replace deterministic source facts.
- A symbol handle remains resolvable after line drift when the symbol body
  fingerprint still matches.

### 4. Impact And Test Intelligence

Purpose: make planning packets good at answering "what could this change
affect?" without forcing the edit strategy.

Signals to combine:

- dependency edges
- imports/includes
- definitions and references
- call and type relationships
- tests and test names
- diagnostics
- history co-change
- ownership and directory boundaries
- runtime traces
- build manifests and generated-code mappings
- semantic text similarity as a fallback only

New planner-facing capability:

```json
{
  "ranked_scope": [
    {
      "path": "internal/workspace/context_tools.go",
      "rank": 0.93,
      "signals": ["defines requested tool", "referenced by tests", "dirty file"],
      "confidence": "precise_backend"
    }
  ],
  "candidate_tests": [
    {
      "command": "go test ./internal/workspace",
      "reason": "package owns changed context tool behavior"
    }
  ],
  "limitations": [
    "Python LSP unavailable; Python reference facts are syntax-tier only"
  ]
}
```

Expected behavior tests:

- A changed exported symbol ranks direct references and owning tests above
  unrelated lexical matches.
- Missing precise backends lower confidence but do not remove syntax-tier
  candidates.
- Generated files point back to their source mapping when available.

### 5. Plan Audit And Critique

Purpose: catch unsupported plans before they become wrong edits, while keeping
the model responsible for strategy.

Add deterministic `plan_audit` plus optional model-assisted critique.

Deterministic checks:

- every file in the plan exists or is explicitly new
- every cited symbol handle resolves or is marked unresolved
- every claimed test command is executable-looking and scoped
- every stale fact is either refreshed or labeled stale
- plan does not include protected paths without permission
- plan has an implementation-ready next atom when the task asks for edits
- plan names exact missing evidence when it refuses to proceed

Optional critique:

- asks a local or configured model to review the plan against the packet
- returns findings with evidence references
- does not rewrite the plan unless explicitly invoked in a separate artifact
  flow

Expected behavior tests:

- A plan citing a nonexistent file fails deterministic audit.
- A plan with no validation for a shared runtime change gets a warning.
- A plan that loops on "need to inspect more" without an exact missing claim
  gets a readiness warning.

### 6. Active Retrieval Affordances

Purpose: help the model ask better follow-up questions without scripting its
next move.

Current context and discovery tools should return:

- unresolved claims
- stale handles
- available zoom levels
- missing backend capabilities
- candidate probe categories
- evidence limits

Example:

```json
{
  "unresolved_claims": [
    {
      "claim": "whether Tool X writes audit telemetry",
      "known_evidence": ["tool manifest includes Tool X"],
      "missing_evidence": "call site or telemetry assertion",
      "probe_categories": ["references", "tests", "exact_read"]
    }
  ]
}
```

This is north-star aligned because the harness names gaps and probe categories,
not a mandatory next command.

Expected behavior tests:

- A stale symbol view exposes `refresh_available: true` and the stale reason.
- An unavailable backend is reported as capability metadata, not hidden.
- A packet with one unresolved claim names one exact missing claim.

### 7. Runtime Evidence Loop

Purpose: let tests, traces, diagnostics, and command output become graph
evidence instead of ephemeral transcript material.

Features:

- Index test commands, failing test names, stack traces, coverage targets, and
  diagnostics as graph nodes.
- Link runtime evidence to files, symbols, packages, and prior plans.
- Allow packets to include "last known failure" and "last known passing
  validation" evidence with timestamps and git fingerprints.
- Summarize command output deterministically first; optionally attach model
  overlays for long traces.

Expected behavior tests:

- A failing test trace links to its test file and likely production symbols when
  references are available.
- Old passing validation is labeled stale after relevant files change.
- Runtime summaries preserve exact failing assertion text.

### 8. Feedback And Telemetry Intelligence

Purpose: close the loop when agents find gaps that audit logs cannot infer.

Extend `harness_feedback` and audit analysis with typed planning-quality
events:

- `missing_affordance`
- `bad_scope_rank`
- `stale_fact`
- `backend_gap`
- `packet_bloat`
- `read_loop`
- `edit_readiness_failure`
- `unsupported_plan_claim`
- `validation_gap`
- `language_precision_gap`

Analyzer outputs should cluster feedback by generic failure mode, affected
tool/capability, language, and confidence. They should not produce project-
specific prompt patches as the primary recommendation.

Expected behavior tests:

- Feedback events survive ingestion into the audit analyzer.
- Analyzer can group read-loop reports by missing evidence affordance.
- Analyzer output distinguishes "model ignored evidence" from "harness did not
  expose evidence."

### 9. Language-Agnostic Enrichment Matrix

Purpose: keep Go, C++, GDScript, and future languages on one planner-facing
contract while preserving precision differences.

Planner-facing dimensions:

- syntax parse
- outline/symbols
- definitions
- references
- call hierarchy
- type hierarchy
- diagnostics
- dependency/build graph
- tests
- runtime traces
- generated-code/source mapping
- package/module metadata

Implementation tiers:

- Tier 0: file and text fallback
- Tier 1: syntax parser or ctags
- Tier 2: language server or compiler-backed queries
- Tier 3: repository index format such as SCIP or Kythe
- Tier 4: deeper static analysis such as Joern/CPG or language-specific
  analyzers

Required near-term hardening:

- Go: keep `gopls` as precise backend and ensure module/workspace warnings are
  visible in packets.
- C++: treat `compile_commands.json` discovery as a first-class precision
  boundary; use `clangd`/`scip-clang` where available; clearly fall back to
  syntax-tier facts otherwise.
- GDScript: index `.gd`, `.tscn`, `.tres`, project settings, autoloads, scenes,
  and signal connections where practical; use Godot's language server only as
  best-effort because the official API is marked experimental.
- Future languages: add collectors by implementing graph facts and capability
  reports, not by changing planner packet semantics.

Expected behavior tests:

- The same packet schema works for Go, C++, GDScript, and mixed-language repos.
- C++ packets report whether `compile_commands.json` was found.
- GDScript packets include scene/resource relationships when files are present.
- A language without LSP still produces useful syntax-tier context.

### 10. Planner Evaluation Harness

Purpose: measure whether the harness is making local models better planners.

Evaluation task types:

- scope localization
- impact analysis
- stale-symbol refresh
- test selection
- implementation plan generation
- plan critique
- read-loop avoidance
- handoff readiness
- multi-language repository navigation

Metrics:

- affected-file recall
- irrelevant-file rate
- symbol localization accuracy
- unsupported-claim rate
- stale-claim rate
- exact-gap quality
- validation command precision
- edit-readiness success
- tool-call efficiency
- implementation success after plan handoff
- user or reviewer correction rate

Suites:

- synthetic fixtures for deterministic graph expectations
- idiom replay logs
- Colony replay logs
- multi-language fixtures for Go, C++, Python, TypeScript, Rust, Java, and
  GDScript
- SWE-bench-style tasks where local execution is practical
- adversarial long-context tasks with important evidence placed at different
  packet positions

Expected behavior tests:

- Evaluations compare packet variants with the same model and task.
- A feature cannot pass only by increasing context size; it must improve one or
  more planning metrics.
- Regression reports identify whether failures came from retrieval, packet
  quality, model reasoning, tool execution, or validation.

## New Or Changed Tool Surface

The names below are proposed. They can be implemented as new tools or as
extensions to existing `context_*`, `architect`, and `harness_audit` surfaces if
that keeps the public API smaller.

| Capability | Purpose | Shape |
| --- | --- | --- |
| `context_budget` | Token and evidence-density health for current or proposed packet. | Deterministic report. |
| `context_impact` | Ranked files/symbols/tests with signal explanations. | Graph + tool evidence. |
| `context_zoom` | Resolve graph handles to exact source, summaries, traces, or related nodes. | Existing context tools may absorb this. |
| `architect packet --schema planning_v2` | Render planner-quality packet from task, graph view, and contracts. | Deterministic artifact. |
| `architect plan-audit` | Check a model plan against a packet. | Deterministic findings plus optional critique input. |
| `harness_audit --purpose planning_quality` | Analyze planner behavior, context budgets, feedback clusters, and stale facts. | Audit report. |
| `harness_feedback --kind <planning-quality-kind>` | Agent-reported harness/tooling gap. | Telemetry event. |

Design rule: expose fewer tools if the same contracts can be cleanly expressed
through existing names. The important part is the typed behavior, not the exact
CLI shape.

## Data Contracts

### Evidence Claim

```json
{
  "claim": "context_status auto-refreshed stale files",
  "claim_type": "tool_observation",
  "confidence": "exact_tool_output",
  "provenance": [
    {
      "tool": "context_status",
      "index_id": "0bcb37da910deaf5",
      "generated_at": "2026-05-07T18:55:14Z"
    }
  ],
  "freshness": {
    "fresh": true,
    "git_head": "adb592f5c4386533fb3523000a917a0619761b48",
    "dirty": true
  }
}
```

### Ranked Scope Item

```json
{
  "handle": "file://internal/workspace/context_tools.go",
  "path": "internal/workspace/context_tools.go",
  "kind": "file",
  "rank": 0.91,
  "signals": [
    {"kind": "defines_tool", "weight": 0.35},
    {"kind": "references_task_term", "weight": 0.18},
    {"kind": "tests_nearby", "weight": 0.16}
  ],
  "confidence": "precise_backend",
  "limitations": []
}
```

### Plan Audit Finding

```json
{
  "severity": "warning",
  "kind": "validation_gap",
  "message": "Plan changes shared workspace behavior but lists only a build command.",
  "evidence": ["packet://candidate_scope/internal/workspace"],
  "suggested_resolution": "Add a package-level behavior test or name why existing tests cover this path."
}
```

## Implementation Roadmap

### Phase 1: Planner Packet Quality

Deliverables:

- `planning_packet_v2` schema and renderer.
- Context budget report.
- Packet freshness and capability report hardening.
- Deterministic plan audit MVP.
- Focused tests for packet shape, stale facts, missing evidence, and validation
  readiness.

Exit criteria:

- A model can receive one packet that includes task, contracts, ranked scope,
  freshness, capability gaps, exact evidence, and validation candidates.
- A plan can be audited before implementation.

### Phase 2: Impact Ranking And Zoom

Deliverables:

- `context_impact` ranking over graph, history, tests, diagnostics, and runtime
  evidence.
- Stable zoom handles for file, symbol, test, trace, and summary nodes.
- Signal explanations and confidence tiers.
- C++ and GDScript fixture hardening.

Exit criteria:

- The model can start from a high-level task and quickly zoom into exact source
  without rediscovering the repository through repeated reads.
- Language precision differences are visible and generic.

### Phase 3: Runtime And Telemetry Loop

Deliverables:

- Runtime evidence graph nodes for tests, diagnostics, traces, and command
  results.
- `harness_feedback` planning-quality kinds.
- Audit analyzer ingestion and clustering.
- Replay reports for idiom and Colony logs.

Exit criteria:

- Read loops, stale facts, and missing affordances are visible as generic
  failure clusters.
- The analyzer can recommend tool/schema/evidence improvements without
  project-specific prompt patches.

### Phase 4: Local Model Optimization

Deliverables:

- Packet budget experiments for Qwen at realistic context sizes.
- Long-context placement tests to detect packet-position sensitivity.
- Optional model-assisted critique lane.
- Comparative evals against a frontier reference model on the same packet.

Exit criteria:

- Local-model planning improves on replay metrics without relying on larger
  prompts alone.
- The harness can identify which failures are retrieval, packet, model, tool,
  or validation failures.

## Non-Goals

- Do not guarantee local models equal frontier models on arbitrary open-ended
  reasoning.
- Do not build a hidden planner that selects the model's strategy.
- Do not use prompt phrases as substitutes for structured contracts.
- Do not make one project, one language, or one recurring failure mode the
  center of the reusable tool design.
- Do not treat LLM summaries as authoritative code facts.
- Do not require every language to have Tier 3 or Tier 4 enrichment before it is
  useful.

## Risks And Mitigations

| Risk | Mitigation |
| --- | --- |
| Context bloat returns under a new name. | Enforce budget reports, duplicate detection, and evidence-density metrics. |
| Harness starts prescribing strategy. | Keep outputs as evidence, gaps, ranked candidates, and contract checks. Avoid mandatory next-tool language. |
| Summary drift creates false confidence. | Attach provenance, confidence, freshness, and exact zoom handles to every summary. |
| Language support becomes uneven or special-cased. | Normalize facts into a shared graph vocabulary and expose precision tiers. |
| Evaluation overfits to Colony or idiom logs. | Mix replay logs with synthetic fixtures and external SWE-style tasks. |
| Optional backends make results non-reproducible. | Record backend availability and confidence in packets and eval output. |
| Plan critique becomes another model hallucination path. | Run deterministic audit first; require evidence-linked critique findings. |

## Open Questions

- Should optional plan critique run in the same model lane, a smaller local
  checker lane, or both behind a config flag?
- Should model-authored summary overlays ever be promoted automatically, or only
  stored as low-confidence overlays until validated by deterministic evidence?
- What default context budget should idiom reserve for output and reasoning when
  the configured local model exposes 196k, 256k, or 1M tokens?
- Should SCIP/Kythe import be a first-class graph source now, or a later bridge
  after the native graph contract stabilizes?
- What minimal GDScript scene/resource relation set gives enough planning value
  without pretending to be a full Godot semantic analyzer?

## Success Criteria

The work is complete enough when:

- `architect packet` can produce a planner packet that is fresh, budgeted,
  evidence-linked, and language-agnostic.
- `architect plan-audit` can reject or warn on unsupported claims, stale facts,
  missing validation, and non-resolving handles.
- `context_impact` or equivalent ranking improves affected-scope recall in
  replay evals.
- Audit telemetry can ingest agent-reported planning-quality failures and
  produce generic clusters.
- Go, C++, and GDScript all use the same planner packet shape with explicit
  precision tiers.
- Local Qwen planning improves on replay tasks by reducing read loops,
  unsupported claims, stale claims, and irrelevant file selection.

## Immediate Next Work

1. Add `planning_packet_v2` schema and renderer on top of the existing architect
   packet path.
2. Add deterministic `context_budget` reporting to packet generation.
3. Add `plan_audit` deterministic checks before optional model critique.
4. Add planning-quality feedback kinds and audit analyzer ingestion.
5. Add multi-language fixtures for Go, C++, and GDScript packet generation.
6. Build replay evals that compare current packets with V2 packets using the
   same local model and task.
