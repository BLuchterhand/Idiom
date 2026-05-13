# Open-Source Harness Lessons for Idiom

This note captures what idiom should adopt from mature open-source coding-agent
harnesses. The goal is not to switch harnesses. The goal is to copy the pieces
that address failure modes we have observed in idiom's own logs.

## Current Evidence

The immediate failure class is now visible in audit output:

- Colony retry25/26 showed a refine patch phase that read the previous draft
  and review, then returned the corrected draft as prose instead of calling
  `write_file`.
- The completion gate correctly rejected the no-write answer.
- Recovery still made read/discovery look like a viable next step, so the model
  reopened source discovery (`read_file`, `code_navigate`, `ast_grep`,
  `lookup`) instead of writing the scratchpad deliverable.
- `harness_audit` initially missed the class because it scanned recent
  telemetry logs but not the relevant `.idiom/logs/conversations/*.jsonl`
  files. After audit expansion, the same failed run is surfaced as
  `completion_gate_reopened_discovery`.
- Colony retry27 showed the newer contract improving behavior: the draft phase
  wrote the requested scratchpad file directly, and the review phase recovered
  from a rejected extra lookup by writing the review.
- Colony retry30 showed a second closeout edge: the patch phase read exactly
  the required scratchpad inputs, then emitted the full corrected markdown
  deliverable as final-answer text instead of calling `write_file`. The
  runner now treats that as a recoverable interface mismatch only when the
  target is a single new markdown deliverable with a recognized refine shape;
  it records the recovery through a synthetic `write_file` result so
  completion gates, logs, and audits see an actual changed path.

That evidence points to three generic needs:

1. Writer phases must be real write contracts, not prose instructions.
2. Phase boundaries must stop discovery from leaking into closeout.
3. Logs must be sufficient to diagnose the behavior class without manually
   reconstructing one raw JSONL transcript.

## Adopt From Aider: Architect/Editor Split

Aider separates reasoning from editing. In architect mode, one model describes
the solution and a separate editor step converts that solution into a concrete
edit format. Aider also documents model-specific edit formats (`diff`, `whole`,
`editor-diff`, `editor-whole`) and notes that editor formats are narrower than
the general problem-solving prompt.

Source:

- <https://aider.chat/docs/more/edit-formats.html>
- <https://aider.chat/docs/usage/modes.html>
- <https://aider.chat/2024/09/26/architect.html>

### What We Want

Use a constrained editor phase for idiom refine patch/closeout work. Once the
review has identified what must change, the model should not be acting as a
general investigator. It should be in a narrow writer state:

- input: previous draft, review, lint findings, preloaded source evidence
- output: one `write_file` for the scratchpad deliverable, or `<<QUESTION>>`
  with a concrete blocker
- forbidden after closeout readiness: new source discovery, optional lookup
  repair, broad planning, prose-only final answer

### What The Change Looks Like

Short term:

- Keep `write_file` as the concrete edit action for non-source scratchpad
  deliverables.
- Treat refine scratchpad draft/review/patch phases as writer/reviewer
  phases, not source-discovery phases: they may read required scratchpad
  inputs, then must write the scratchpad deliverable or record missing source
  facts inside it as `Source verification pending:` / `## Scope Correction
  Required`.
- After a no-write completion-gate rejection, reject non-write tools for that
  scratchpad deliverable until the file is written or the model reports a
  blocker.
- Keep uncertainty inside the artifact using `Source verification pending:` or
  `## Scope Correction Required`, instead of reopening discovery.
- If the model already generated the full single-file markdown deliverable as
  final-answer text, recover by writing that artifact through `write_file` and
  recording the result. Do not salvage `<<QUESTION>>` blockers, existing files,
  source files, or markdown that does not match a known deliverable shape.

Medium term:

- Add an explicit `scratchpad_editor` lane or runtime phase.
- Give it a smaller tool manifest: `read_file` only for required scratchpad
  inputs before closeout, then `write_file` only after closeout readiness.
- Teach refine to hand the editor a structured patch packet rather than a
  general implementation brief.

Expected benefit:

- Fewer prose-only deliverables.
- Fewer post-gate read loops.
- Fewer false failsoft exits where a valid scratchpad artifact existed but was
  stranded in the wrong channel.
- Lower prompt volume in refine patch phases because the model no longer keeps
  re-deriving source facts after the review has already named the required
  changes.

Why it is likely effective:

- The observed failure is not lack of knowledge; the model had the previous
  draft, review, lint findings, and preloaded source anchors. It failed at
  action selection. A narrower editor phase attacks that exact problem.
- Aider's design is explicitly motivated by models that reason well but fail to
  emit reliable file edits.

## Adopt From Aider: Ranked Repo Map, But Keep Freshness Auditable

Aider sends a concise, ranked repository map that includes important files,
symbols, signatures, and critical definition lines. It adjusts the map to fit a
token budget and uses graph ranking to choose relevant context.

Source:

- <https://aider.chat/docs/repomap.html>

### What We Want

Idiom's context graph and `context_pack` are already close to this. The next
step is to make context packets behave more like ranked evidence packets and
less like optional search results:

- rank file/symbol candidates by task relevance
- include exact anchors where available
- report freshness and stale/missing evidence in the packet
- keep `harness_audit` able to summarize freshness health from both in-process
  counters and persisted logs

### What The Change Looks Like

- Continue using `context_pack` before broad reads.
- `idiom lane implement` and `idiom lane plan-execute` now preload a compact
  `## Context Pack` section by default. The packet is best-effort: generation
  failures are logged to stderr and the lane still runs from the original
  brief. Refine scratchpad meta-prompts are excluded because they already carry
  phase-specific lint/source evidence; adding a generic repo map there reopens
  the exact "discovery instead of closeout" failure mode. Use
  `--no-context-pack` only when a caller has already supplied an equivalent
  context packet or is debugging packet generation itself.
- Prefer semantic/context/language/project discovery before raw `read_file`.
- Preserve source rows in `harness_audit` so we know which log families were
  scanned and whether they were capped.
- Add log-derived freshness fields to audit output so a standalone audit run
  does not misleadingly report zero freshness activity just because its
  in-process counters are empty.

Expected benefit:

- Less token waste from repeated file reads.
- Better evidence quality for write-first packets.
- Easier diagnosis when cached context is stale or too broad.

Why it is likely effective:

- Colony and idiom logs repeatedly show generic reads/searches dominating tool
  mix when the harness leaves raw discovery attractive.
- A ranked map/context packet gives the model the high-signal handles without
  making it enumerate the repo.

## Adopt From SWE-agent: Treat The Interface As The Product

SWE-agent's core contribution is not just prompting; it frames the
agent-computer interface as a first-class object. Tool documentation,
commands, edit affordances, and trajectories are part of the agent's effective
capability. SWE-agent also stores trajectories and provides CLI commands for
replay/statistics.

Source:

- <https://swe-agent.com/latest/usage/cli/>
- <https://github.com/SWE-agent/SWE-agent/blob/main/docs/usage/trajectories.md>
- <https://arxiv.org/abs/2405.15793>

### What We Want

Idiom should continue moving rules out of duplicated prose and into explicit
runtime contracts:

- required edit paths
- required evidence
- scoped tool surfaces
- completion gates
- recovery gates
- audit-visible stop reasons

### What The Change Looks Like

- Runtime state should say what is valid next, not just what the model should
  prefer.
- Gate failures should emit structured events that explain the contract breach.
- Recovery should be an interface transition. Example: after a non-source
  deliverable fails completion because no write occurred, the interface changes
  to "write this artifact or report the blocker."
- Audit should have named anti-patterns for each important interface failure,
  such as `completion_gate_reopened_discovery` and
  `required_paths_blocked_without_write`.

Expected benefit:

- Fewer prompt patches.
- Easier debugging of authority conflicts.
- Behavior changes that can be validated by logs rather than by reading prompts
  and hoping the model obeys.

Why it is likely effective:

- The closeout bug persisted across prompt wording changes but became directly
  measurable and actionable once the harness had a named audit pattern and a
  runtime guard.

## Adopt From OpenHands: Typed Event Streams And Persisted State

OpenHands persists complete conversation state, including a message/event log,
agent configuration, execution state, tool outputs, statistics, workspace
context, skills, and custom agent state. Its event files are explicitly
trajectory data split into granular records.

Source:

- <https://docs.openhands.dev/sdk/guides/convo-persistence>
- <https://arxiv.org/abs/2407.16741>

### What We Want

Idiom logs should be central to behavior repair. Conversation logs and
telemetry logs should not be side effects. They should be the source of truth
for:

- which phase the runner was in
- what contract was active
- which tools were visible
- which action the model requested
- whether the action was accepted, rejected, rewritten, or hidden
- why the run stopped
- whether required paths were written
- whether verification was satisfied, skipped, or blocked

### What The Change Looks Like

- Expand typed events around phase transitions, recovery transitions, tool
  visibility changes, completion-gate rejections, and blocked closeouts.
- Keep `harness_audit` coverage aligned with all log families:
  `.idiom/logs/conversations`, `logs/conversations`, `.idiom/logs/e2e`,
  `logs/e2e`, and `logs/telemetry`.
- Make `audit watch` use the same default log families as `harness_audit`.
- Add trigger reasons for blocked `completion_status` and required-path stop
  reasons.

Expected benefit:

- Faster diagnosis of future loops.
- Less reliance on manual transcript archaeology.
- Better regression tests because every repair has a log-level symptom.

Why it is likely effective:

- The closeout bug became obvious once we scanned the right conversation log.
  Before that, the audit layer underreported the failure because the relevant
  log family was out of scope.

## Adopt From Agentless-Style Pipelines: Hard Phase Separation

Agentless-style SWE workflows use explicit phases: localization, repair, and
validation. That is less flexible than a free-running agent, but it limits
phase bleed.

Source:

- <https://app.argminai.com/arxiv-dashboard/papers/2407.01489v2>

### What We Want

Refine should not behave like a single general-purpose implementation loop.
It should be a pipeline:

1. draft from spec and known gaps
2. review the draft
3. patch the draft from review/lint evidence
4. accept only if lint/review pass
5. hand off to implementation

Each phase has different valid tools and different stop conditions.

### What The Change Looks Like

- Draft phase: write scratchpad draft from spec; record missing source truth as
  pending evidence.
- Review phase: read draft, optionally spot-check named facts, write review.
- Patch phase: read draft and review, write corrected draft; if source truth is
  still missing, record it in the draft rather than reopening broad discovery.
- Implementation phase: write actual source files using required edit paths and
  verified anchors.

Expected benefit:

- Less role confusion.
- Smaller prompts.
- More predictable recovery because the runner can know what "done" means for
  the current phase.

Why it is likely effective:

- The same Qwen model performed correctly in retry27's draft/review phases when
  the phase contract was narrow enough. The failure appeared when patch closeout
  still allowed too many action interpretations after rejection.

## Adopt From Observability Tools: Queryable Audit Trail

Tools such as Gryph, AgentReplay, and AgentLogs focus on making agent actions
queryable and reviewable. They do not solve idiom's action-selection problem by
themselves, but they reinforce the operational requirement: every read, write,
tool call, command, and policy decision should be visible enough to audit.

Sources:

- <https://safedep.io/gryph-ai-agent-audit-trail>
- <https://agentreplay.dev/>
- <https://agentlogs.ai/>

### What We Want

Make idiom's own audit layer good enough that we do not need a separate
observability wrapper for normal development.

### What The Change Looks Like

- Queryable anti-patterns and top offenders in `harness_audit`.
- `audit watch` triggers on high-risk stops, completion gates,
  `harness_feedback`, malformed tool inputs, stale-context signs, and blocked
  completion statuses.
- Review artifacts from `audit watch` that embed the relevant `harness_audit`
  report.

Expected benefit:

- Less missed regression risk.
- Better future optimization work because token waste and loop classes are
  measurable.

Why it is likely effective:

- The latest fix was driven by audit output that named the exact failure class.
  That is the workflow we want by default.

## Recommended Idiom Roadmap

### Immediate

- Keep the scratchpad closeout guard: after a no-write completion-gate rejection
  for a non-source deliverable, reject non-write tools and require `write_file`
  or `<<QUESTION>>`.
- Keep automatic lane context-pack preloading for implementation routes. This
  is the Aider-style repo-map equivalent: the runner sees ranked files,
  symbols, tests, and freshness before the model starts raw discovery.
- Keep draft/patch refine phases on a large enough output budget to emit the
  whole corrected artifact, not only analysis prose.
- Keep expanded audit coverage across conversation, telemetry, and e2e logs.
- Keep log-derived freshness health in `harness_audit`.
- Keep `audit watch` defaults aligned with `harness_audit` defaults.

### Near Term

- Introduce explicit refine sub-phases in runtime state:
  `scratchpad_draft`, `scratchpad_review`, `scratchpad_patch`,
  `scratchpad_accept`.
- Give each sub-phase a small, explicit tool manifest.
- Add phase-specific anti-patterns:
  `scratchpad_patch_optional_lookup_repair`,
  `review_phase_reopened_source_without_named_gap`,
  `draft_phase_source_discovery`.
- Add a compact "phase replay" command that prints only phase changes,
  model choices, tool results, gate decisions, and stop reason.

### Later

- Build an Aider-style editor lane for source edits:
  planner/reasoner produces an edit packet; editor emits only a structured edit
  artifact.
- Add structured edit formats for local models that struggle with free-form
  tool calls.
- Add trajectory-level evaluation fixtures from real idiom failures, so every
  harness fix can be replayed against the exact failure shape.

## Non-Goals

- Do not add arbitrary hard read budgets. The desired boundary is phase and
  evidence readiness, not a numeric read cap.
- Do not solve action-selection failures with more duplicate prose.
- Do not make raw source reads impossible. They remain valid when the active
  phase has a named source fact that needs exact confirmation.
- Do not let audit become a passive dashboard only. Audit findings should feed
  directly into repair work and regression tests.
