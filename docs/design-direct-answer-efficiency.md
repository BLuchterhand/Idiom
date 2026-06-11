# Direct-Answer Efficiency Optimization

**North Star: AI coding agents are becoming common, but teams do not yet trust
their context, decisions, or outputs. Idiom sells the missing substrate: repo
memory, freshness, contracts, audit, and evals.**

Status: draft for reviewer bench, created 2026-06-11

## Purpose

This design defines the optimization program that followed a small hosted
DeepSeek Flash comparison between Reasonix and Idiom. The benchmark was not a
product-quality eval suite, but it surfaced actionable harness behavior in
Idiom's read-only direct-answer path:

- avoidable search before reading an exact user-named path;
- occasional full-file reads for simple file-scoped questions;
- high prompt-token floor for tiny read-only answers;
- completion-gate false positives when command examples or placeholders look
  like unsupported citations;
- DeepSeek hosted-chat history/protocol drift around `reasoning_content`;
- route telemetry showing stale role aliases during model override runs;
- weaker hosted cost/cache observability than Reasonix.

The goal is to reduce latency, token use, and diagnostic ambiguity without
weakening the contract-first harness boundary.

## Evidence Snapshot

Benchmark artifact: `.idiom/benchmark/20260611-122151`

Clean task:

```text
Read README.md and answer in exactly one concise sentence under 35 words: what is Idiom? End with Source: README.md.
```

Both tools used the hosted DeepSeek API with model id `deepseek-v4-flash`.

Median clean-pass results:

| Tool | Wall time | Prompt tokens | Completion tokens | Total tokens | Tool calls |
| --- | ---: | ---: | ---: | ---: | ---: |
| Reasonix | 4.02s | 34,880 | 145 | 35,009 | 2 |
| Idiom | 5.66s | 29,577 | 158 | 29,735 | 2 |

Observed Idiom behavior:

- Trial 1 directly read `README.md` but read the full 1,025-line file.
- Trials 2 and 3 searched for `README.md`, then read only lines 1-30 or 1-40.
- The route decision event reported role aliases as `deepseek-v4-pro` even
  though the active benchmark config targeted `deepseek-v4-flash`.
- An earlier comparable task produced blocked/synthetic final answers because
  source-grounding acceptance treated command/example paths such as
  `path/to/brief.md` and `views/<pack_id>.json` as unsupported source
  citations.
- A failed trial against DeepSeek returned: `The reasoning_content in the
  thinking mode must be passed back to the API.`

Context packet for implementation planning:

- `context_pack` id: `edc474559a0d4803`
- artifact: `.idiom/comprehension/views/edc474559a0d4803.json`
- freshness: fresh after read-through refresh on 2026-06-11
- likely implementation areas named by the packet include:
  `internal/runtime/agent/final_answer_acceptance.go`,
  `internal/runtime/agent/discovery_redirect.go`,
  `internal/surfaces/tui/app/prompt_routing.go`,
  `internal/capabilities/workspace/language_tools_adapter.go`,
  `internal/runtime/llama/client.go`, and telemetry-related surfaces.

## Design Principles

1. **Preserve strategy ownership.** The model still decides how to synthesize
   the answer. The harness may pre-resolve exact evidence the user explicitly
   named, but it should not write the answer.
2. **Make deterministic state cheap before model dispatch.** If the user says
   "Read README.md", path existence and a bounded initial read are host-owned
   setup, not a strategy question.
3. **Keep gates as invariants, not friction.** Citation and grounding gates
   should reject unsupported evidence claims, not command literals or examples
   that are not being cited as evidence.
4. **Measure the real cost surface.** Hosted cache and cost metrics should be
   visible beside total prompt/completion tokens so benchmark conclusions match
   paid-token behavior.
5. **Do not make prompt text authority.** Optimizations must live in typed
   routing/runtime state, tool request policy, telemetry, or acceptance logic,
   not in additional reminders.

## Proposed Changes

### 0. Lightweight Direct-Answer Surface Inventory

Before exact-path prefetch or bounded reads are implemented, direct-answer
mode needs a concrete runtime surface inventory. This is the foundation that
the other direct-answer optimizations consume.

For `direct_answer` + read-only workspace tasks, the lightweight surface must:

- keep read-only investigation tools available: `read_file`, `search_files`,
  `search_content`, directory/file statistics where already allowed, and
  language/context navigation capabilities already exposed for read-only
  evidence;
- remove write and mutation tools from the model-facing manifest:
  `write_file`, `edit_file`, `submit_plan`, plan-execute executor surfaces,
  and mutation/review-repair terminal protocol;
- remove planner/executor/reviewer role protocol text from direct-answer
  model-facing instructions;
- keep normal uncertainty prose as a valid user-facing terminal state when
  evidence is insufficient;
- emit a surface/version identifier in telemetry so audit can distinguish the
  lightweight direct-answer surface from build-mode direct-answer fallback.

Non-trivial read-only investigation must remain possible. The required
negative oracle is a task that cannot be answered from an exact path alone and
requires search followed by a bounded source read.

Implementation order: this inventory and its tests must land before exact-path
prefetch. A prefetch packet must be consumed by this surface, not define a
parallel model-facing protocol.

Required enforcement test: if the lightweight surface inventory is missing,
stale, or does not advertise support for `source_body_prefetch`, exact-path
prefetch must emit a typed disabled/miss state and normal model-led
investigation must remain available. A1 must not run against an unknown A6
surface.

### 1. Exact-Path Direct-Answer Prefetch

When a read-only direct-answer request names one or more exact workspace paths
that exist, the harness should attach a bounded source-body evidence packet
before the first model call.

Initial supported shape:

- task family: `conversation` or `direct_answer`;
- permission profile: read-only workspace;
- user text includes explicit file path mentions;
- every prefetch target resolves inside the workspace root;
- the path is a regular file, not a directory or generated benchmark artifact;
- the file is under a size/line threshold or can be safely range-bounded.

The packet should be recorded as normal source-body evidence, not as a hidden
answer. The model receives evidence and can answer, ask for a narrow follow-up,
or inspect more source if the bounded slice is insufficient.

A1 and A6 share one data contract:

- `path`: workspace-relative canonical path;
- `line_range`: inclusive bounded range when the file is range-read;
- `content_hash`: hash of the exact source-body slice supplied to the model;
- `evidence_kind`: `source_body_prefetch`;
- `policy`: bounded-read policy name and version;
- `miss_reason`: typed reason when no prefetch is attached.

The lightweight direct-answer surface must either consume this contract or emit
a typed miss. It must not silently reinterpret prefetch evidence as generic
prompt text.

Contract negative fixtures are required before A1 implementation:

- empty or outside-root `path`;
- `line_range` outside actual file bounds or with start greater than end;
- `content_hash` present together with a non-empty `miss_reason`;
- `evidence_kind` other than `source_body_prefetch`;
- `policy` not recognized by the direct-answer bounded-read policy registry;
- `miss_reason` absent when no content is attached.

Negative cases:

- do not prefetch paths in mutation lanes;
- do not prefetch ambiguous file-name searches when multiple current files
  match;
- do not prefetch paths inside generated logs, benchmark outputs, or
  audit-only evidence folders unless the user explicitly asks about those
  artifacts;
- do not bypass protected-path or tool-rule checks.

Success metric: exact-path direct-answer tasks should normally complete in one
model call plus zero or one tool call equivalent, with no search-before-read
when the path exists.

### 2. Bounded Read Defaults For Direct Answers

For direct-answer prefetch and model-requested `read_file` calls in simple
file-scoped Q&A, default to bounded ranges instead of full-file reads when the
question does not require whole-file inspection.

Initial policy:

- when the prompt asks for summary/definition/quick fact and names a doc file,
  read the first 60-100 lines;
- when the prompt names an anchor string, use search or bounded surrounding
  lines;
- when the prompt asks for "entire file", "all references", "full contents",
  line-count-sensitive analysis, or broad architecture review, keep full-file
  reads available;
- if a bounded read does not answer the question, the model may request another
  bounded slice.

This belongs in the read/direct-answer evidence path, not in generic
`read_file` semantics for every lane. Implementation should prefer a small,
typed `DirectAnswerEvidencePolicy` over ad hoc string checks scattered across
the runner.

Success metric: simple file-scoped direct answers should not read full
multi-hundred-line docs by default.

### 3. Lightweight Direct-Answer Runtime Surface

The clean benchmark still spent roughly 30k total tokens for a one-sentence
answer. Some of that is unavoidable project instruction context, but direct
answers do not need the same visible surface as build-mode implementation.

Introduce a lightweight direct-answer turn surface:

- read-only tools only;
- no writer/edit/plan-execute protocol text;
- no mutation-lane terminal states;
- compact completion contract focused on accepted evidence and normal
  uncertainty prose;
- route/model/runtime telemetry still emitted.

This should be a compiled runtime surface or model-facing protocol projection,
not a prompt-only profile. It must remain auditable through the same contract
ids and protocol hashes used by the existing runner.

This section is intentionally sequenced after the inventory above in the
document, but implementation-wise it is Atom A6 and must run before Atom A1.
The surface inventory is the dispatch gate for A6.

Success metric: direct-answer prompt-token floor decreases on a fixed benchmark
without increasing unsupported answers or blocker leaks.

### 4. Source-Grounding Citation Classifier

The final-answer acceptance gate should distinguish:

- evidence citations: paths the answer presents as inspected source authority;
- command examples: literal path arguments inside shell commands or usage
  examples;
- placeholders: `path/to/brief.md`, `<pack_id>`, `{name}`, and similar template
  paths;
- historical/log-only paths: benchmark artifacts or transcripts being discussed
  as logs, not source evidence.

Only evidence citations should be required to match accepted source-body
evidence. Command/example placeholders should not make an otherwise grounded
answer fail.

The canonical owner should be the read-only final-answer acceptance path, with a
shared helper used by completion status, fallback composition, and telemetry.
Do not add a local "ignore this string" list to one presentation layer.

The shared helper must be the only citation-authority implementation used by
A2 and by the existing final-answer acceptance gate. It should classify
path-like mentions and validate accepted evidence through one typed result so
acceptance, completion status, fallback synthesis, and telemetry cannot
disagree about the same answer.

Success metric: answers citing `README.md` while quoting
`idiom lane plan-execute --title my-task path/to/brief.md` pass when
`README.md` was accepted source evidence; answers that falsely cite
uninspected source paths still fail.

### 5. Hosted DeepSeek Reasoning-Content Protocol Repair

DeepSeek hosted-chat history can reject follow-up requests when prior assistant
messages contain hidden reasoning that is not replayed with the required
`reasoning_content` field.

Implementation should classify the backend capability and repair history at
the OpenAI-compatible client boundary:

- preserve `reasoning_content` when the provider requires it for thinking-mode
  continuity;
- or reliably disable thinking and avoid sending thinking-mode history for
  direct-answer Flash runs;
- log the effective thinking policy, backend, model id, and history repair
  decision in bounded telemetry.

Required fixture: a synthetic hosted DeepSeek chat response/history fixture
must reproduce the `reasoning_content` continuity requirement without relying
on a live API call. The existing local/non-DeepSeek behavior that strips hidden
reasoning from replayed content must remain covered as a separate negative
case.

The fixture must be table-driven and cover at least: omitted field, empty
string, normal string, multiline content, special characters, truncated content,
and streamed-vs-non-streamed response shape when the client supports both.

This must be model-agnostic at the shared client layer: DeepSeek-specific repair
should not break local textual-tool backends or other native tool-call hosted
providers.

Success metric: the observed DeepSeek `reasoning_content` invalid request does
not recur in a replay fixture, and non-DeepSeek backend tests remain unchanged.

### 6. Effective Model Attribution In Route Telemetry

Route and session telemetry must report the effective model used for the call,
not a stale default profile alias.

Current benchmark evidence showed route aliases for `deepseek-v4-pro` while the
client model was `deepseek-v4-flash`. This weakens audit and benchmark trust.

Implementation should record both:

- selected/default role alias from project backend profile, when present;
- effective client model id after `-model`, lane, config, or role override.

The route layer must own the resolved identity record. Telemetry should consume
that typed resolved identity instead of independently re-deriving the model id.
If the selected profile alias and effective model id differ, the event should
make that difference explicit rather than overwriting either value.

Success metric: benchmark JSONL can unambiguously answer which model every
model call used.

### 7. Hosted Cache, Miss, And Cost Metrics

Reasonix exposed `cache_hit_tokens`, `cache_miss_tokens`, and cost. Idiom
currently reports prompt/completion/total tokens but not hosted cache/cost
fields.

Extend the hosted usage schema to capture provider-specific cache fields when
present, without making them mandatory:

- prompt/cache hit tokens;
- prompt/cache miss tokens;
- reasoning tokens if exposed;
- provider currency/cost fields when emitted by the response or computed by a
  configured rate table;
- per-call and session-summary aggregation.

Session summaries should always expose the metric field names with a canonical
state when the provider does not supply values:

- numeric count fields use `0` plus a sibling `*_known:false` boolean when
  absence matters for interpretation;
- cost uses `cost_known:false`, `cost:0`, and `currency:""` when unavailable;
- provider-supplied values set the matching `*_known:true` fields.

Unknown provider fields should be preserved only as bounded structured metrics,
not raw response bodies.

Success metric: hosted DeepSeek benchmark summaries can compare total tokens,
cache-adjusted tokens, and cost.

## Dispatchable Atom Plan

The effort should be implemented as separate atoms after full reviewer-bench
alignment:

| Atom | Scope | Expected files | Required tests |
| --- | --- | --- | --- |
| A6 lightweight direct-answer surface | protocol/tool-surface slimming | runtime surface/protocol projection/prompt selection tests | focused direct-answer runtime tests plus benchmark replay |
| A1 exact-path prefetch and bounded read policy | direct-answer evidence setup | runner/direct-answer policy files, read-only answerability tests | `go test ./internal/runtime/agent -run 'DirectAnswer|ReadOnly|Evidence'` |
| A2 citation classifier | final-answer acceptance and path authority classification | `internal/runtime/agent/final_answer_acceptance.go`, related tests | `go test ./internal/runtime/agent -run 'FinalAnswer|Evidence|Citation|Ground'` |
| A3 hosted DeepSeek protocol repair | hosted chat client/history handling | `internal/runtime/llama/client.go`, client tests | `go test ./internal/runtime/llama` |
| A4 effective model telemetry | route/model-call/session telemetry | route/runtime telemetry files and tests | focused route/runtime telemetry tests |
| A5 hosted cache/cost metrics | usage schema and summary aggregation | llama usage structs, runner metrics, CLI explain/postflight if applicable | `go test ./internal/runtime/llama ./internal/runtime/agent ./internal/surfaces/cli/lane` |

Parallelization:

- A6 must run first and produce the concrete surface inventory and protocol
  tests.
- A1 runs after A6 and must consume the A1/A6 data contract above.
- A2, A3, A4, and A5 can start in parallel after A6 is reviewed if expected
  files are confirmed disjoint after localization.
- A4 and A5 may share session-summary fixtures; if they touch the same
  aggregation code, serialize the smaller telemetry patch first.
- Final integration must run after all atoms because metrics, prefetch, and
  acceptance interact in the same benchmark.

## Verification Plan

Focused tests:

- exact-path direct answer reads `README.md` without search-first behavior;
- bounded read default avoids full-file source-body reads for simple summary
  prompts;
- missing or excluded exact paths produce a typed prefetch miss and do not
  block normal model-led investigation;
- A6's lightweight surface supports a non-trivial read-only investigation
  sequence such as search followed by bounded source read;
- A1/A6 contract negative fixtures reject malformed prefetch packets;
- A1 is disabled with a typed miss when the A6 surface inventory is stale or
  absent;
- command placeholders in inspected docs are not treated as unsupported source
  citations;
- false source citations remain blocked;
- DeepSeek hosted-chat response with `reasoning_content` can be replayed or
  safely stripped according to the effective thinking policy;
- DeepSeek `reasoning_content` fixtures cover omitted, empty, normal,
  multiline, special-character, truncated, and streamed/non-streamed cases;
- route telemetry reports effective model id separately from profile alias;
- hosted usage fields aggregate cache hit/miss and cost when present.
- session summaries include cache/cost field names with unknown/zero values
  when a provider does not expose them.
- a cross-condition regression suite runs the direct-answer replay, citation
  fixture, DeepSeek protocol fixture, effective-model telemetry fixture, and
  cache/cost summary fixture together before final integration review.

Behavior replay:

- rerun the clean benchmark under hosted DeepSeek Flash;
- rerun the earlier placeholder-command benchmark that cited the
  `plan-execute` command from `README.md`;
- compare wall time, prompt tokens, total tokens, tool calls, and cache/cost
  metrics against `.idiom/benchmark/20260611-122151`.

Broad verification:

- `go test ./internal/runtime/llama`
- `go test ./internal/runtime/agent`
- `go test ./internal/surfaces/cli/lane`
- `go test ./...` before final review if shared runner, telemetry, or prompt
  surfaces change.

## Risks And Non-Goals

Risks:

- over-eager prefetch could hide missing evidence or read generated artifacts
  the user did not intend;
- bounded reads could under-read files whose first lines are misleading;
- direct-answer surface slimming could accidentally remove a capability needed
  for legitimate read-only investigation;
- DeepSeek-specific protocol handling could leak provider assumptions into the
  shared client;
- cache/cost metrics may be provider-specific and incomplete.

Mitigations:

- keep prefetch eligibility exact-path and read-only only;
- let the model request additional bounded reads after prefetch;
- keep full direct-answer tooling available when the question is broad;
- isolate provider-specific protocol decisions behind backend capability checks;
- treat cache/cost fields as optional metrics with explicit unknown state.

Non-goals:

- weaken final-answer evidence gates;
- synthesize answers in the harness;
- make direct-answer routing depend on magic prompt phrases;
- replace repository-memory/context-pack workflows for broad code questions;
- optimize for one benchmark at the expense of mutation, review, or planning
  lanes.

## Reviewer Questions

Full-bench review should answer:

- Is exact-path prefetch a host-owned setup step or an overreach into model
  strategy?
- Does bounded-read policy have a clear owner and negative oracle?
- Does the citation classifier avoid duplicating path-authority logic?
- Is the DeepSeek protocol repair located at the right backend boundary?
- Does telemetry distinguish selected role alias from effective model id?
- Are cache/cost metrics privacy-safe and provider-agnostic enough?
- Are atoms disjoint enough for parallel implementation after review?
