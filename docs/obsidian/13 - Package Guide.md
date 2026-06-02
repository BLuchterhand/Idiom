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
| `cmd/idiom` | Thin CLI entrypoint (`main.go`); all subcommand impls live in `internal/*cmd/` |
| `cmd/idiom-analyze` | Thin entrypoint; analysis impl in `internal/analyzecmd/` |
| `cmd/idiom-e2e` | Thin entrypoint; e2e impl in `internal/e2ecmd/` |
| `cmd/idiom-eval` | Thin entrypoint; eval impl in `internal/evalcmd/` |
| `cmd/idiom-perf-eval` | Thin entrypoint; perf-eval impl in `internal/perfevalcmd/` |
| `cmd/idiom-swe-eval` | Thin entrypoint; swe-eval impl in `internal/sweevalcmd/` |

Important CLI subcommands live in `internal/*cmd/` packages:
lanes (`internal/lanecmd`), tools (`internal/toolcmd`), backend management
(`internal/backendcmd`), queue (`internal/queuecmd`), atom intake
(`internal/atomcmd`), brief lint (`internal/briefcmd`), lane postflight
(`internal/postflight`), product-evidence export (`internal/productevidence`),
release evidence (`internal/productevidence`), audit watch (`internal/auditcmd`),
ranker commands (`internal/rankercmd`), local-ops benchmark (`internal/localopscmd`),
MCP serve (`internal/mcpcmd`), doctor (`internal/doctorcmd`), probe
(`internal/probecmd`), architect (`internal/architectcmd`), refine
(`internal/refinecmd`), and init (`internal/initcmd`).

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
| `internal/cmd/config` | Shared CLI configuration |
| `internal/util` | Shared utility helpers: string, exec, IO, JSON, and generic pointer functions |
| `internal/autodetect` | Host, model, backend, and toolchain discovery helpers |

## Evidence And Diagnostics

| Package | Responsibility |
| --- | --- |
| `internal/telemetry` | Structured runtime events and telemetry plumbing |
| `internal/transcript` | Conversation transcript handling |
| `internal/eval` | Eval scoring and behavior-pattern detection |
| `internal/mcp` | Outbound MCP client and registry; inbound MCP server lives in `internal/mcpcmd` |

## Operational Evidence Commands

| Command family (package) | Purpose |
| --- | --- |
| `internal/auditcmd` (`idiom audit watch`) | Poll telemetry/conversation logs, debounce high-signal events, and write review packets under `.idiom/audits/watch` |
| `internal/briefcmd` (`idiom brief lint`) | Pre-dispatch lint for implementation briefs, exact anchors, prompt-magic risks, and plan-execute edit shape |
| `internal/postflight` (`idiom lane postflight`) | QC pipeline for returned diffs |
| `internal/localopscmd` (`idiom local-ops benchmark`) | Local readiness, activation, smoke, capacity, and privacy-safe benchmark evidence |
| `internal/productevidence` (`idiom product-evidence export`) | Metadata-only go/no-go evidence export for product decisions |
| `internal/productevidence` (`idiom release evidence`) | Release evidence bundle comparing baseline/current command artifacts |
| `internal/rankercmd` (`idiom ranker`) | Manual ranker learning inspection, boost/penalize controls, and rollback |

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
| Main CLI | `cmd/idiom/main.go`, `internal/rootcmd`, `internal/lanecmd`, `internal/planexecutecmd`, `internal/implementcmd`, `internal/toolcmd` |
| MCP and sidecar | `internal/mcpcmd`, `internal/mcp/client.go`, `internal/mcp/registry.go`, `internal/workspace/tool_schema_export.go` |
| Runner contracts | `internal/agent/runner.go`, `internal/agent/runner_autonomy.go`, `internal/agent/runner_task_ledger.go` |
| Workspace tools | `internal/workspace/manager.go`, `internal/workspace/tool_schemas.go`, `internal/workspace/context_tools.go`, `internal/workspace/source_sanity.go` |
| External project tools | `internal/workspace/external_tool.go`, `.idiom/external_tools/<name>.json` |
| Context memory | `internal/comprehension/graph.go`, `internal/comprehension/pack.go`, `internal/comprehension/views.go`, `internal/comprehension/freshness.go` |
| Prompt stack | `internal/promptstack`, `prompts/`, `prompts/profiles/` |
| Local operations | `internal/doctorcmd`, `internal/backendcmd`, `internal/queuecmd`, `internal/localopscmd`, `internal/backends`, `internal/autodetect` |
| Audit/eval | `internal/eval`, `internal/telemetry`, `internal/workspace/harness_audit.go` |

## Change Localization Examples

| If changing... | Start with... |
| --- | --- |
| Completion readiness | `internal/agent`, runner tests, [[09 - Verification Readiness and Completion]] |
| Tool schema or dispatch | `internal/workspace`, tool tests, [[06 - Workspace Tools and Capability Routing]] |
| Context packet behavior | `internal/comprehension`, context tool tests, [[07 - Repository Memory and Context Graph]] |
| Plan-execute workflow | `internal/planexecutecmd`, lane tests, [[04 - Runtime Lanes]] |
| Prompt behavior | `prompts/`, `internal/promptstack`, runner behavior tests |
| Audit classification | `internal/eval`, `harness_audit`, telemetry fixtures |
| Local backend readiness | `internal/backends`, `internal/autodetect`, `internal/llama`, local ops docs |

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
