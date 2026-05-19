# Architect Packets

Architect packets externalize planning context before a model writes a plan,
critique, or executor brief. The goal is to move reusable capability into
Agent Harness's harness contracts while keeping project-specific knowledge in a local
profile.

This mirrors proven patterns from other agent tools:

- separate reasoning from editing
- give agents concise repository context instead of raw file floods
- enforce readiness gates before dispatch
- keep traces and packets auditable

## Flow

```bash
harness context impact \
  --task-file /tmp/task.md \
  --output /tmp/impact.json

harness context pack \
  --task-file /tmp/task.md \
  --output /tmp/context-view.json

harness architect packet \
  --task-file /tmp/task.md \
  --schema planning_v2 \
  --budget-tier standard \
  --profile demo \
  --context-view /tmp/context-view.json \
  --file src/feature.cpp:modify:"feature logic" \
  --test "go test ./...:harness regression" \
  --constraint "Stop with <<QUESTION>> if a required symbol is missing." \
  --output /tmp/packet.json

harness architect lint --packet /tmp/packet.json

harness architect budget --packet /tmp/packet.json

harness architect plan --packet /tmp/packet.json \
  --output /tmp/plan.prompt.md

harness artifact \
  --prompt-file /tmp/plan.prompt.md \
  --output /tmp/plan.md \
  --json-out /tmp/plan.events.jsonl \
  --report-out /tmp/plan.report.json \
  --mechanical-repair \
  --no-tools

harness architect plan-audit \
  --packet /tmp/packet.json \
  --plan-file /tmp/plan.md

harness architect critique \
  --packet /tmp/packet.json \
  --prior-file /tmp/plan.md \
  --output /tmp/critique.prompt.md

harness architect packet-compare \
  --base /tmp/packet-v1.json \
  --candidate /tmp/packet.json

harness architect replay-compare \
  --base /tmp/packet-v1.json \
  --candidate /tmp/packet.json \
  --base-plan /tmp/plan-v1.md \
  --candidate-plan /tmp/plan.md
```

`context impact`, `context pack`, `packet`, `lint`, `budget`,
`packet-compare`, `replay-compare`, and `plan-audit` are deterministic. They do
not require a model. `--context-view` accepts a saved `harness context pack` JSON
view and records it as `context_view` evidence. `plan`, `critique`, and `brief`
also only render prompts; use `harness artifact` or another model runner to
generate the actual plan, critique, or brief.

When `artifact` is used as the model runner, prefer `--json-out` plus
`--report-out`. The JSONL file preserves the model event stream, while the
report is the compact dispatch artifact: it records the no-tools contract,
prompt and answer hashes, model, validator exit, tool-use detection,
deterministic repair source, and `dispatch_ready`. Use `--mechanical-repair`
for Markdown plan/spec artifacts so one accidental outer fence is unwrapped
deterministically before validation.

`harness context pack` results are cached under
`.harness/comprehension/graph.sqlite` in the `graph_views` table, keyed by
content fingerprint (active graph `index_id` + canonical task/mode/query
inputs). Re-running `context pack` with identical inputs returns the cached
view without recomputation; scoped `context build --refresh` only invalidates
the rows whose referenced paths changed. The `views/<pack_id>.json` files
under `.harness/comprehension/` are human-readable mirrors of the SQLite rows
for inspection — they are not the source of truth.

`--schema planning_v2` keeps the v1 packet fields and adds planner-quality
sections: `contracts`, `repository_view`, `candidate_scope`, `freshness`,
`language_capabilities`, `handoff`, and `context_budget`. This schema is
intended for plan generation and plan audit. The default remains v1 for
existing callers.

`plan-audit` is deterministic. It checks a candidate plan against the packet
for missing file references, validation commands that cite missing workspace
paths, unresolved placeholder tokens such as `<path>`, unresolved handles,
protected-path edits, stale context use, validation gaps, and vague "inspect
more" deferrals.

`budget` evaluates the same packet across `brief`, `standard`, `deep`, and
`max-local` budget tiers and emits placement probes for long-context
sensitivity. `packet-compare` reports deterministic schema/packet deltas between
variants. `replay-compare` audits model-produced plans from base and candidate
packets so V2 planning changes can be compared on the same task and model.

## Profile

By default, Agent Harness looks for:

- `.harness/architect/profile.json`
- `.harness/architecture/profile.json`

You can also pass `--profile <path>` or `--profile <name>`, where names resolve
under `.harness/architect/profiles/<name>.json`.

Example:

```json
{
  "name": "demo",
  "description": "Repository planning adapter",
  "rules": [
    {
      "label": "project rules",
      "path": "AGENTS.md",
      "required": true,
      "max_bytes": 24000
    }
  ],
  "collectors": [
    {
      "name": "symbol trace",
      "command": "./scripts/trace_symbols.sh",
      "args": ["--help"],
      "timeout_seconds": 10
    }
  ],
  "gates": [
    {"name": "exact_tests_required", "required": true},
    {"name": "file_allowlist_required", "required": true},
    {"name": "question_stop_required", "required": true},
    {"name": "evidence_required", "required": true}
  ],
  "constraints": [
    "The model owns strategy; the harness owns contracts.",
    "Stop with <<QUESTION>> if a required symbol, file, or test fact is missing."
  ],
  "roles": [
    {"name": "planner", "description": "normalizes the packet into atoms"},
    {"name": "critic", "description": "finds missing evidence and unsafe scope"},
    {"name": "executor", "description": "implements only the approved atom"}
  ]
}
```

Collectors are not run unless `--run-collectors` is provided. Without that flag,
the packet records them as pending evidence so the planner can see what the
profile would collect without executing project commands.

## Built-In Gates

- `exact_tests_required`: packet must list at least one exact validation command.
- `file_allowlist_required`: packet must list at least one file entry.
- `question_stop_required`: packet constraints or evidence must include
  `<<QUESTION>>`. `local-model_question_stop_required` is accepted as a compatibility
  alias for existing local profiles.
- `evidence_required`: packet must include rule, collector, or context-view
  evidence.
- `anchors_required`: packet must include at least one verified anchor.
- `context_view_required`: packet must include at least one context-view
  evidence entry.
- `fresh_context_required` or `context_freshness_required`: context-view
  evidence must parse as JSON and report `"fresh": true`.

Context views that are stale, malformed, missing freshness metadata, or carrying
unresolved gaps produce warnings by default. They become errors only when a
required freshness/context gate asks the harness to enforce that invariant.

Unknown required gates are warnings. That keeps project-specific gates visible
without pretending Agent Harness can validate rules it does not understand.
