# Architecture

Idiom separates model strategy from harness authority.

The model decides how to reason about the task. The harness enforces the
contracts around that work: tool schemas, permissions, protected paths,
evidence freshness, lane boundaries, verification, and final-answer acceptance.

## Main Components

| Area | Responsibility |
| --- | --- |
| `cmd/idiom` | CLI entrypoint and command wiring. |
| `internal/runtime` | Agent runner, backend routing, readiness, rails, and final-answer handling. |
| `internal/contracts` | Shared policy, plan, permission, and evidence contracts. |
| `internal/capabilities/workspace` | Workspace tools, sidecar execution, and tool policy. |
| `internal/repository` | Repository comprehension, context packets, and graph-backed memory. |
| `internal/workflows` | Implementation planning, execution, recovery, and product workflows. |
| `internal/evidence` | Telemetry, audit records, evaluation, and evidence projections. |
| `internal/platform` | Configuration, filesystem and process adapters. |
| `internal/surfaces/cli` | CLI surfaces for run, lanes, audit, queue, MCP, and tools. |
| `internal/surfaces/tui` | Interactive terminal UI. |
| `prompts/` | Baseline prompt stack and mode/profile instructions. |
| `.idiom/` | Project-local harness config with placeholders only. |

## Runtime Flow

```mermaid
flowchart LR
  User["User or script"] --> CLI["CLI/TUI surface"]
  CLI --> Runtime["Agent runtime"]
  Runtime --> Backend["Model backend"]
  Runtime --> Tools["Workspace tools"]
  Tools --> Repo["Repository evidence"]
  Runtime --> Audit["Telemetry and audit"]
```

The key boundary is that tools and audit evidence inform the model, while the
harness remains responsible for enforcing what the model may do.

## Ownership and Lifecycle

`Runner` holds shared runtime dependencies. A `runExecution` owns invocation
state and snapshots mutable inputs. Turn state belongs to that invocation;
approval bindings and cancellation must not leak into a later run. The TUI
uses a detached `promptExecutionSession` and receives worker events rather than
letting workers mutate the live UI model.

Workspace owns tool classification, permissions, and execution. CLI and MCP
translate requests into that boundary. Repository adapters supply facts;
runtime evidence projections interpret source bodies, candidate paths, and
verification for the active objective. Display text is not evidence authority.

Workflow persistence owns durable state, leases, journals, and recovery. Surface
packages present workflow results and collect operator decisions. Move a rule
to its owning package when callers duplicate its meaning; avoid compatibility
wrappers that leave a second authority in place.

## Security Boundary

Workspace policy governs admitted tool calls. The process still has the host
user's permissions, and shell commands can execute repository code. The optional
[OS sandbox adapter](design-os-sandbox.md) is a process API; normal CLI runs do
not automatically use it. Hosted backends and external tools receive the data
sent to them. See the [security posture](security-privacy-posture.md) for tested
controls and limits.

## Coding Rule Contracts

Coding rules are project-local harness contracts for recurring source patterns.
The policy file names rules, languages, severities, enforcement levels, and
documented suppressions. The analyzer produces typed findings with paths,
symbols, hashes, and evidence facts instead of source snippets, so the model can
respond to idiom violations without the harness prescribing a next reasoning
step.

The default posture is advisory. A repository can promote a rule to blocking
when the rule represents a genuine invariant; lane postflight then treats
blocking findings as failed verification.

## Design Rules

- Keep prompt guidance, runtime gates, telemetry, and tests aligned.
- Represent real protocols as typed contracts rather than string conventions.
- Prefer reusable predicates and contract checks over scenario-specific prompt
  nudges.
- Keep generated local evidence out of source control unless it is a deliberate
  sanitized fixture.
