---
tags:
  - harness
  - package-guide
  - codebase-map
---

# Package Guide

This note gives a package-level map of the target codebase. Use it as an orientation aid,
not as a substitute for `context_pack` or source inspection.

## Commands

| Package | Purpose |
| --- | --- |
| `cmd/harness` | Main CLI, TUI launch, tool commands, lanes, atom intake, local ops, artifact commands |
| `cmd/harness-analyze` | Analysis command surface |
| `cmd/harness-e2e` | End-to-end test command surface |
| `cmd/harness-eval` | Evaluation command surface |
| `cmd/harness-perf-eval` | Performance/eval command surface |
| `cmd/harness-swe-eval` | SWE-style eval command surface |

Important `cmd/harness` subcommands include lanes, tools, backend management,
queue, atom intake, brief lint, lane postflight, product-evidence export,
release evidence, audit watch, ranker commands, local-ops benchmark, and MCP
serve.

## Core Runtime Packages

| Package | Responsibility |
| --- | --- |
| `internal/runtime/agent` | Runner loop, autonomy contract, completion gate, task ledger, redirects, tool-use behavior |
| `internal/surfaces/tui/app` | Interactive TUI and session lifecycle |
| `internal/runtime/runner` | Process/runtime helpers around command execution |
| `internal/runtime/promptstack` | Prompt assembly and layering |
| `internal/runtime/workerruntime` | Worker runtime catalog, profiles, and capability surface |
| `internal/contracts/autonomy` | Autonomy contract structures and sidecar-facing behavior |

## Workspace And Tools

| Package | Responsibility |
| --- | --- |
| `internal/capabilities/workspace` | Tool manager, dispatch, file operations, source sanity, context/audit tool entrypoints |
| `internal/repository/semantic` | Semantic/language-aware support |
| `internal/repository/discovery` | Discovery helpers and query orchestration |
| `internal/runtime` | Runtime helper functionality |
| `internal/runtime/pathparse` | Path parsing helpers |

## Repository Memory

| Package | Responsibility |
| --- | --- |
| `internal/repository/comprehension` | Context graph, context packs, views, freshness, cache, impact, trace packs |
| `internal/repository/discovery` | Discovery artifacts and higher-level codebase queries |

## Planning And Artifacts

| Package | Responsibility |
| --- | --- |
| `internal/workflows/architect` | Architect packets and planning-related evidence |
| `internal/workflows/projectartifacts` | Project artifact lifecycle and workflow state |
| `internal/workflows/postflight` | Postflight checks and release/workflow evidence |

## Model And Operations

| Package | Responsibility |
| --- | --- |
| `internal/runtime/backends` | Backend profiles, role aliases, launcher, status, and proxy management |
| `internal/runtime/llama` | llama.cpp/local model integration |
| `internal/platform/config` | Project and runtime configuration |
| `internal/cmdutil` | Shared CLI utilities |
| `internal/platform/util` | Shared utility helpers: string, exec, IO, JSON, and generic pointer functions |
| `internal/platform/autodetect` | Host, model, backend, and toolchain discovery helpers |

## Evidence And Diagnostics

| Package | Responsibility |
| --- | --- |
| `internal/evidence/telemetry` | Structured runtime events and telemetry plumbing |
| `internal/evidence/transcript` | Conversation transcript handling |
| `internal/evidence/eval` | Eval scoring and behavior-pattern detection |
| `internal/capabilities/mcp` | Outbound MCP client and registry; inbound MCP server lives in `cmd/harness/mcp.go` |

## Operational Evidence Commands

| Command family | Purpose |
| --- | --- |
| `harness audit watch` | Poll telemetry/conversation logs, debounce high-signal events, and write review packets under `.harness/audits/watch` |
| `harness brief lint` | Pre-dispatch lint for implementation briefs, exact anchors, prompt-magic risks, and plan-execute edit shape |
| `harness lane postflight` | QC pipeline for returned diffs |
| `harness local-ops benchmark` | Local readiness, activation, smoke, capacity, and privacy-safe benchmark evidence |
| `harness product-evidence export` | Metadata-only go/no-go evidence export for product decisions |
| `harness release evidence` | Release evidence bundle comparing baseline/active command artifacts |
| `harness ranker` | Manual ranker learning inspection, boost/penalize controls, and rollback |

## Prompt And Docs Areas

| Path | Responsibility |
| --- | --- |
| `prompts/` | Baseline prompt layers |
| `prompts/profiles/` | Mode/profile-specific prompt layers |
| `docs/` | Design, design map, operations, architecture, audit, and workflow docs |
| `.agent-config/` | LLM-agent-specific project context and traps |

## Key Source Anchors

| Area | Files |
| --- | --- |
| Main CLI | `cmd/harness/main.go`, `cmd/harness/lane.go`, `cmd/harness/plan_execute.go` |
| MCP and sidecar | `cmd/harness/mcp.go`, `internal/capabilities/mcp/client.go`, `internal/capabilities/mcp/registry.go`, `internal/capabilities/workspace/tool_schema_export.go` |
| Runner contracts | `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_autonomy.go`, `internal/runtime/agent/runner_task_ledger.go` |
| Workspace tools | `internal/capabilities/workspace/manager.go`, `internal/capabilities/workspace/tool_schemas.go`, `internal/capabilities/workspace/context_tools.go`, `internal/capabilities/workspace/source_sanity.go` |
| External project tools | `internal/capabilities/workspace/external_tool.go`, `.harness/external_tools/<name>.json` |
| Context memory | `internal/repository/comprehension/graph.go`, `internal/repository/comprehension/pack.go`, `internal/repository/comprehension/views.go`, `internal/repository/comprehension/freshness.go` |
| Prompt stack | `internal/runtime/promptstack`, `prompts/`, `prompts/profiles/` |
| Local operations | `cmd/harness/doctor.go`, `cmd/harness/backend.go`, `cmd/harness/queue.go`, `internal/runtime/backends`, `internal/platform/autodetect` |
| Audit/eval | `internal/evidence/eval`, `internal/evidence/telemetry`, `internal/capabilities/workspace/harness_audit.go` |

## Change Localization Examples

| If changing... | Start with... |
| --- | --- |
| Completion readiness | `internal/runtime/agent`, runner tests, [[09 - Verification Readiness and Completion]] |
| Tool schema or dispatch | `internal/capabilities/workspace`, tool tests, [[06 - Workspace Tools and Capability Routing]] |
| Context packet behavior | `internal/repository/comprehension`, context tool tests, [[07 - Repository Memory and Context Graph]] |
| Plan-execute workflow | `cmd/harness/plan_execute.go`, lane tests, [[04 - Runtime Lanes]] |
| Prompt behavior | `prompts/`, `internal/runtime/promptstack`, runner behavior tests |
| Audit classification | `internal/evidence/eval`, `harness_audit`, telemetry fixtures |
| Local backend readiness | `internal/runtime/backends`, `internal/platform/autodetect`, `internal/runtime/llama`, local ops docs |

## Onboarding By System

For subsystem entry paths, read [[23 - Major Systems Architectural Onboarding]]
before editing. It connects the package map in this note to ownership rules,
verification expectations, and the friction signals that usually mean a change
belongs at a boundary.

| System | Start with |
| --- | --- |
| CLI and lanes | [[23 - Major Systems Architectural Onboarding#CLI And Lanes]] |
| TUI/session lifecycle | [[23 - Major Systems Architectural Onboarding#TUI And Session Lifecycle]] |
| Runner and contracts | [[23 - Major Systems Architectural Onboarding#Runner And Contracts]] |
| Workspace tools and capability routing | [[23 - Major Systems Architectural Onboarding#Workspace Tools And Capability Routing]] |
| Repository memory and context graph | [[23 - Major Systems Architectural Onboarding#Repository Memory And Context Graph]] |
| Planning and handoff | [[23 - Major Systems Architectural Onboarding#Planning And Handoff]] |
| Verification readiness and completion | [[23 - Major Systems Architectural Onboarding#Verification Readiness And Completion]] |
| Telemetry, audit, eval, and learning | [[23 - Major Systems Architectural Onboarding#Telemetry Audit Eval And Learning]] |
| Local model operations | [[23 - Major Systems Architectural Onboarding#Local Model Operations]] |
| Sidecar and LLM agent integrations | [[23 - Major Systems Architectural Onboarding#Sidecar And LLM Agent Integrations]] |
| Product workflow and behavioral coverage | [[23 - Major Systems Architectural Onboarding#Product Workflow And Behavioral Coverage]] |
| Recovery and decomposition | [[23 - Major Systems Architectural Onboarding#Recovery And Decomposition]] |

## Related Notes

- [[03 - Architecture Layers]]
- [[06 - Workspace Tools and Capability Routing]]
- [[11 - Local Model Operations]]
- [[19 - CLI TUI and Operator Surface]]
- [[23 - Major Systems Architectural Onboarding]]

## Rule Of Thumb

If a behavior crosses packages, first identify the contract that connects them.
Most durable fixes belong at that contract boundary, not deep inside an adapter
or only in prompt prose.
