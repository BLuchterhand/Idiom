# Debugging Reviewer Bench

Idiom behavior bugs are usually contract, tool-surface, prompt, runtime, or
acceptance failures that happen to surface in the same live run. Use this
reviewer bench to get targeted independent review without turning every fix into
a large committee.

## Reviewer Roles

| Reviewer | Use when | Primary questions |
| --- | --- | --- |
| Incident Root-Cause Reviewer | Any non-trivial live-run, TUI, CLI, lane, recovery, or completion failure. This is the default first reviewer. | What exactly happened, in what order, and what is the first broken invariant? Which claims are verified from logs/tests and which are inference? |
| Model Runtime / Config Reviewer | Hidden reasoning, missing visible tool calls, backend migrations, MLX/llama.cpp/vLLM behavior, tool-call transport, thinking flags, context window, temperature, token budget, or model alias changes are plausible contributors. | Did runtime configuration or backend capability make a capable model look worse than it is? Are native/textual tools, thinking, max tokens, and parser support aligned? |
| Harness / Go Reviewer | Runner state, gates, routing, queueing, session lifecycle, tool manifests, permissions, recovery, or Go control flow changed or appears implicated. | Is the state machine coherent? Are there duplicated authorities, stale state, concurrency or lifecycle bugs, false positives, or missing regression tests? |
| Prompt / Protocol Reviewer | The model emits prose, malformed tools, stale anchors, missing evidence, wrong terminal states, or repeatedly needs recovery nudges. | Does the model-facing protocol match the gate order and tool schema? Are prompts conflicting, over-specific, or relying on reminders where typed contracts should exist? |
| TDD / Acceptance Reviewer | The failure involves implementation correctness, acceptance criteria, product-workflow evidence, failing tests, or TDD artifacts. | Does the produced work satisfy the requested behavior and tests? If not, what failure packet should go back through the lane rather than being manually patched? |
| North-Star / Architecture Reviewer | The proposed fix changes broad harness behavior, weakens a gate, adds a new recovery path, changes lane semantics, or follows repeated failures. | Is this model-strategy vs harness-contract boundary correct? Does the change avoid scenario-specific hardcoding and compensating-fix chains? |

## Usage Rule

Use the smallest reviewer set that can challenge the likely failure mode.
Reviewers are read-only by default: they diagnose, challenge, and recommend.
Implementation remains a separate step unless a reviewer is explicitly assigned
an implementor role.

1. For every non-trivial behavior fix, send an Incident Root-Cause Reviewer.
2. Add exactly one specialist reviewer for the most likely class of failure:
   Model Runtime / Config, Harness / Go, Prompt / Protocol, or TDD /
   Acceptance.
3. Add a second specialist only when the root-cause evidence already spans two
   concrete domains, such as model tool-call transport plus prompt protocol, or
   runner state plus TDD acceptance semantics. Do not add a second specialist
   just because the failure is confusing.
4. Add the North-Star / Architecture Reviewer when the fix changes shared
   harness behavior, changes lane contracts, weakens/relocates a gate, or follows
   a repeated live-run failure.
5. Use the full bench only after two related harness-shaped failures or before a
   broad rearchitecture.
6. Do not dispatch every reviewer by default. If two reviewers identify the same
   root cause and the fix is local, implement and test that fix before expanding
   review.

## Specialist Selection

Use these triggers to pick the specialist reviewer.

| Failure signature | Specialist |
| --- | --- |
| Tool calls disappear, hidden reasoning dominates, backend changes, `tool_choice` errors, context/token issues, temperature or model alias drift | Model Runtime / Config Reviewer |
| State transitions conflict, gates disagree with prompts, recovery loops, stale session state, duplicated authority, false-positive tool rejection | Harness / Go Reviewer |
| The model sees the right tools but emits prose, malformed JSON/tool calls, stale anchors, missing evidence, or wrong terminal states | Prompt / Protocol Reviewer |
| Code or artifacts are produced but do not satisfy acceptance criteria, tests, product workflow evidence, or TDD obligations | TDD / Acceptance Reviewer |
| The proposed fix broadens policy, changes lane semantics, weakens a gate, or starts to look scenario-specific | North-Star / Architecture Reviewer |

## Required Reviewer Output

Every reviewer must use this shape:

```text
Verified facts:
Inferences:
Root cause:
Contributing factors:
Non-causes:
Fix recommendation:
Regression tests:
North-star risk:
Confidence:
```

Reviewers must cite local evidence: conversation JSONL, telemetry, audit output,
tests, source paths, or docs. They must distinguish verified facts from
inference and call out when evidence is thin.

## Guardrails

- Do not use reviewers to outvote hard invariants. If a gate is correct, fix the
  prompt, typed contract, or recovery state around it.
- Do not default to "model quality" before checking runtime configuration,
  tool-call transport, prompt/protocol conflicts, and harness state.
- Do not propose manual product-code fixes when the active goal is to test a TUI
  or lane workflow. Send product implementation defects back through the lane.
- Do not add prompt reminders until the relevant typed contract, tool surface,
  runtime brief, recovery state, and tests have been checked.
- Do not broaden the patch solely because multiple reviewers were used. Reviewer
  output should narrow the fix to the first broken invariant.
