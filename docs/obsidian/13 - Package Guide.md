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
| `cmd/idiom` | Thin CLI entrypoint (`main.go`); root command wiring lives in `internal/surfaces/cli/root` |
| `cmd/idiom-analyze` | Thin entrypoint; analysis impl in `internal/surfaces/cli/analyze` |
| `cmd/idiom-e2e` | Thin entrypoint; e2e impl in `internal/surfaces/cli/e2e` |
| `cmd/idiom-eval` | Thin entrypoint; eval impl in `internal/surfaces/cli/eval/` |
| `cmd/idiom-perf-eval` | Thin entrypoint; perf-eval impl in `internal/surfaces/cli/perfeval` |
| `cmd/idiom-swe-eval` | Thin entrypoint; swe-eval impl in `internal/surfaces/cli/sweeval` |

Important CLI subcommands live under `internal/surfaces/cli/`: lanes
(`lane`), tools (`tool`), backend management (`backend`), queue (`queue`), atom
intake (`atom`), brief lint (`brief`), plan-execute (`planexecute`), product
workflow and evidence commands (`product`), release evidence
(`internal/evidence/productevidence`), audit watch (`audit`), ranker commands
(`ranker`), local-ops benchmark (`localops`), MCP serve (`mcp`), doctor
(`doctor`), probe (`probe`), architect (`architect`), refine (`refine`), and
init (`init`).

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
| `internal/frontier` | Frontier/runtime helper functionality |
| `internal/contracts/pathparse` | Path parsing helpers |

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
| `internal/cmd/config` | Shared CLI configuration |
| `internal/platform/util` | Shared utility helpers: string, exec, IO, JSON, and generic pointer functions |
| `internal/platform/autodetect` | Host, model, backend, and toolchain discovery helpers |

## Evidence And Diagnostics

| Package | Responsibility |
| --- | --- |
| `internal/evidence/telemetry` | Structured runtime events and telemetry plumbing |
| `internal/evidence/transcript` | Conversation transcript handling |
| `internal/evidence/eval` | Eval scoring and behavior-pattern detection |
| `internal/capabilities/mcp` | Outbound MCP client and registry; inbound MCP server lives in `internal/surfaces/cli/mcp` |

## Operational Evidence Commands

| Command family (package) | Purpose |
| --- | --- |
| `internal/auditcmd` (`idiom audit watch`) | Poll telemetry/conversation logs, debounce high-signal events, and write review packets under `.idiom/audits/watch` |
| `internal/briefcmd` (`idiom brief lint`) | Pre-dispatch lint for implementation briefs, exact anchors, prompt-magic risks, and plan-execute edit shape |
| `internal/workflows/postflight` (`idiom lane postflight`) | QC pipeline for returned diffs |
| `internal/surfaces/cli/localops` (`idiom local-ops benchmark`) | Local readiness, activation, smoke, capacity, and privacy-safe benchmark evidence |
| `internal/evidence/productevidence` (`idiom product-evidence export`) | Metadata-only go/no-go evidence export for product decisions |
| `internal/evidence/productevidence` (`idiom release evidence`) | Release evidence bundle comparing baseline/current command artifacts |
| `internal/surfaces/cli/ranker` (`idiom ranker`) | Manual ranker learning inspection, boost/penalize controls, and rollback |

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
| Main CLI | `cmd/idiom/main.go`, `internal/surfaces/cli/root`, `internal/lanecmd`, `internal/surfaces/cli/planexecute`, `internal/implementcmd`, `internal/toolcmd` |
| MCP and sidecar | `internal/surfaces/cli/mcp`, `internal/capabilities/mcp/client.go`, `internal/capabilities/mcp/registry.go`, `internal/capabilities/workspace/tool_schema_export.go` |
| Runner contracts | `internal/runtime/agent/runner.go`, `internal/runtime/agent/runner_autonomy.go`, `internal/runtime/agent/runner_task_ledger.go` |
| Workspace tools | `internal/capabilities/workspace/manager.go`, `internal/capabilities/workspace/tool_schemas.go`, `internal/capabilities/workspace/context_tools.go`, `internal/capabilities/workspace/source_sanity.go` |
| External project tools | `internal/capabilities/workspace/external_tool.go`, `.idiom/external_tools/<name>.json` |
| Context memory | `internal/repository/comprehension/graph.go`, `internal/repository/comprehension/pack.go`, `internal/repository/comprehension/views.go`, `internal/repository/comprehension/freshness.go` |
| Prompt stack | `internal/runtime/promptstack`, `prompts/`, `prompts/profiles/` |
| Local operations | `internal/surfaces/cli/doctor`, `internal/surfaces/cli/backend`, `internal/surfaces/cli/queue`, `internal/surfaces/cli/localops`, `internal/runtime/backends`, `internal/platform/autodetect` |
| Audit/eval | `internal/evidence/eval`, `internal/evidence/telemetry`, `internal/capabilities/workspace/harness_audit.go` |

## Change Localization Examples

| If changing... | Start with... |
| --- | --- |
| Completion readiness | `internal/runtime/agent`, runner tests, [[09 - Verification Readiness and Completion]] |
| Tool schema or dispatch | `internal/capabilities/workspace`, tool tests, [[06 - Workspace Tools and Capability Routing]] |
| Context packet behavior | `internal/repository/comprehension`, context tool tests, [[07 - Repository Memory and Context Graph]] |
| Plan-execute workflow | `internal/surfaces/cli/planexecute`, lane tests, [[04 - Runtime Lanes]] |
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
| Sidecar and external integrations | [[23 - Major Systems Architectural Onboarding#Sidecar And External Integrations]] |
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
