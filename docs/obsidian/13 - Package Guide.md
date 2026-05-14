---
tags:
  - idiom
  - package-guide
  - codebase-map
---

# Package Guide

This note gives a package-level map of the repo. Use it as an orientation aid,
not as a substitute for `context_pack` or source inspection.

## Commands

| Package | Purpose |
| --- | --- |
| `cmd/idiom` | Main CLI, TUI launch, tool commands, lanes, atom intake, local ops, artifact commands |
| `cmd/idiom-analyze` | Analysis command surface |
| `cmd/idiom-e2e` | End-to-end test command surface |
| `cmd/idiom-eval` | Evaluation command surface |
| `cmd/idiom-perf-eval` | Performance/eval command surface |
| `cmd/idiom-swe-eval` | SWE-style eval command surface |

Important `cmd/idiom` subcommands include lanes, tools, backend management,
queue, atom intake, brief lint, lane postflight, product-evidence export,
release evidence, audit watch, ranker commands, local-ops benchmark, and MCP
serve.

## Core Runtime Packages

| Package | Responsibility |
| --- | --- |
| `internal/agent` | Runner loop, autonomy contract, completion gate, task ledger, redirects, tool-use behavior |
| `internal/app` | Interactive TUI and session lifecycle |
| `internal/runner` | Process/runtime helpers around command execution |
| `internal/promptstack` | Prompt assembly and layering |
| `internal/frontier/workerruntime` | Worker runtime catalog, profiles, and capability surface |
| `internal/autonomy` | Autonomy contract structures and sidecar-facing behavior |

## Workspace And Tools

| Package | Responsibility |
| --- | --- |
| `internal/workspace` | Tool manager, dispatch, file operations, source sanity, context/audit tool entrypoints |
| `internal/semantic` | Semantic/language-aware support |
| `internal/discovery` | Discovery helpers and query orchestration |
| `internal/frontier` | Frontier/runtime helper functionality |
| `internal/frontier/pathparse` | Path parsing helpers |

## Repository Memory

| Package | Responsibility |
| --- | --- |
| `internal/comprehension` | Context graph, context packs, views, freshness, cache, impact, trace packs |
| `internal/discovery` | Discovery artifacts and higher-level codebase queries |

## Planning And Artifacts

| Package | Responsibility |
| --- | --- |
| `internal/architect` | Architect packets and planning-related evidence |
| `internal/projectartifacts` | Project artifact lifecycle and workflow state |
| `internal/postflight` | Postflight checks and release/workflow evidence |

## Model And Operations

| Package | Responsibility |
| --- | --- |
| `internal/backends` | Backend profiles, role aliases, launcher, status, and proxy management |
| `internal/llama` | llama.cpp/local model integration |
| `internal/config` | Project and runtime configuration |
| `internal/cmdutil` | Shared CLI utilities |
| `internal/autodetect` | Host, model, backend, and toolchain discovery helpers |

## Evidence And Diagnostics

| Package | Responsibility |
| --- | --- |
| `internal/telemetry` | Structured runtime events and telemetry plumbing |
| `internal/transcript` | Conversation transcript handling |
| `internal/eval` | Eval scoring and behavior-pattern detection |
| `internal/mcp` | Outbound MCP client and registry; inbound MCP server lives in `cmd/idiom/mcp.go` |

## Operational Evidence Commands

| Command family | Purpose |
| --- | --- |
| `idiom audit watch` | Poll telemetry/conversation logs, debounce high-signal events, and write review packets under `.idiom/audits/watch` |
| `idiom brief lint` | Pre-dispatch lint for implementation briefs, exact anchors, prompt-magic risks, and plan-execute edit shape |
| `idiom lane postflight` | QC pipeline for returned diffs |
| `idiom local-ops benchmark` | Local readiness, activation, smoke, capacity, and privacy-safe benchmark evidence |
| `idiom product-evidence export` | Metadata-only go/no-go evidence export for product decisions |
| `idiom release evidence` | Release evidence bundle comparing baseline/current command artifacts |
| `idiom ranker` | Manual ranker learning inspection and boost/penalize controls |

## Prompt And Docs Areas

| Path | Responsibility |
| --- | --- |
| `prompts/` | Baseline prompt layers |
| `prompts/profiles/` | Mode/profile-specific prompt layers |
| `docs/` | Design, roadmap, operations, architecture, audit, and workflow docs |
| `.codex/` | Codex-specific repo context and traps |

## Key Source Anchors

| Area | Files |
| --- | --- |
| Main CLI | `cmd/idiom/main.go`, `cmd/idiom/lane.go`, `cmd/idiom/plan_execute.go` |
| MCP and sidecar | `cmd/idiom/mcp.go`, `internal/mcp/client.go`, `internal/mcp/registry.go`, `internal/workspace/tool_schema_export.go` |
| Runner contracts | `internal/agent/runner.go`, `internal/agent/runner_autonomy.go`, `internal/agent/runner_task_ledger.go` |
| Workspace tools | `internal/workspace/manager.go`, `internal/workspace/tool_schemas.go`, `internal/workspace/context_tools.go`, `internal/workspace/source_sanity.go` |
| External project tools | `internal/workspace/external_tool.go`, `.idiom/external_tools/<name>.json` |
| Context memory | `internal/comprehension/graph.go`, `internal/comprehension/pack.go`, `internal/comprehension/views.go`, `internal/comprehension/freshness.go` |
| Prompt stack | `internal/promptstack`, `prompts/`, `prompts/profiles/` |
| Local operations | `cmd/idiom/doctor.go`, `cmd/idiom/backend.go`, `cmd/idiom/queue.go`, `internal/backends`, `internal/autodetect` |
| Audit/eval | `internal/eval`, `internal/telemetry`, `internal/workspace/harness_audit.go` |

## Change Localization Examples

| If changing... | Start with... |
| --- | --- |
| Completion readiness | `internal/agent`, runner tests, [[09 - Verification Readiness and Completion]] |
| Tool schema or dispatch | `internal/workspace`, tool tests, [[06 - Workspace Tools and Capability Routing]] |
| Context packet behavior | `internal/comprehension`, context tool tests, [[07 - Repository Memory and Context Graph]] |
| Plan-execute workflow | `cmd/idiom/plan_execute.go`, lane tests, [[04 - Runtime Lanes]] |
| Prompt behavior | `prompts/`, `internal/promptstack`, runner behavior tests |
| Audit classification | `internal/eval`, `harness_audit`, telemetry fixtures |
| Local backend readiness | `internal/backends`, `internal/autodetect`, `internal/llama`, local ops docs |

## Related Notes

- [[03 - Architecture Layers]]
- [[06 - Workspace Tools and Capability Routing]]
- [[11 - Local Model Operations]]
- [[19 - CLI TUI and Operator Surface]]

## Rule Of Thumb

If a behavior crosses packages, first identify the contract that connects them.
Most durable fixes belong at that contract boundary, not deep inside an adapter
or only in prompt prose.
