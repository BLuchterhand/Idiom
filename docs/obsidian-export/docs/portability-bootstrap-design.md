# Portability Bootstrap Design

Status: design guidance

This document defines the greenfield portability layer for Agent Harness. It
captures the reusable mechanics that let a new project adopt the harness without
copying repository-specific scripts, prompts, release notes, or migration
history.

The goal is a small, stable surface:

- a project-local `.harness/` directory for configuration and overrides
- canonical embedded resources supplied by the harness binary
- explicit extension points for project-specific tools, prompts, and policies
- install, doctor, lane, backend, and sidecar commands that behave the same
  across repositories

## Design Principles

1. Canonical behavior lives in the harness, not in copied project scripts.
2. Project-specific behavior is expressed as data: configuration, prompt
   overlays, tool manifests, and policy files.
3. Defaults should be useful, but every default should be inspectable and
   overridable through a typed contract.
4. A fresh project should be able to initialize, diagnose, run a stub lane, and
   inspect evidence before any real model call is trusted.
5. The portability layer must reuse the same contracts, telemetry, schemas,
   audit, context graph, and sidecar surfaces as normal lane execution.

## Project Layout Contract

A greenfield project should converge on this shape:

```text
.harness/
  harness.toml
  backends.json
  prompts/
    profiles/
    lanes/
  external_tools/
  policies/
  state/
```

`harness.toml` is the project entrypoint. It declares the project name, default
lanes, enabled capabilities, protected paths, test commands, freshness policy,
and sidecar profiles.

`backends.json` declares local and remote inference backends by role rather than
by one-off script name. The harness resolves roles such as planner, coder,
reviewer, verifier, summarizer, and sidecar-discovery into concrete backend
profiles.

`prompts/` contains only overlays and project-specific profile text. Canonical
lane framing, recovery language, readiness gates, and completion criteria are
embedded in the harness and versioned.

`external_tools/` contains typed manifests for project tools. These tools may
expose domain-specific discovery or verification, but they must return evidence
and uncertainty rather than prescribing model strategy.

`policies/` contains local policy overlays: approval rules, protected paths,
network policy, test requirements, artifact retention, and redaction rules.

`state/` is owned by the harness. It stores local runtime state, indexes,
durable packets, audit records, and resumable workflow state.

## Command Surface

The portability command surface should be intentionally small:

| Command | Purpose | Contract |
| --- | --- | --- |
| `harness init` | Create the project-local `.harness/` skeleton. | Never overwrite project files without an explicit migration plan. |
| `harness init --auto` | Detect language, package manager, test commands, and candidate backends. | Mark inferred values as inferred until accepted. |
| `harness doctor` | Diagnose backend, config, schema, index, tool, permission, and test readiness. | Return structured checks with severity and remediation hints. |
| `harness backend start|stop|status` | Manage configured local inference backends. | Use role/pool configuration; expose ports, health, and queue state. |
| `harness lane <mode>` | Run a configured lane with canonical framing plus project overlays. | Compile and emit a lane contract before any mutation. |
| `harness lane postflight` | Run quality checks over the lane diff and evidence bundle. | Produce typed findings and block completion claims on failed invariants. |
| `harness tool <name>` | Invoke sidecar tools from another LLM agent or script. | Use the same schemas, permissions, freshness, telemetry, and audit contracts as first-party lanes. |
| `harness artifact` | Create or inspect durable plans, briefs, packets, and evidence products. | Preserve provenance and make supersession explicit. |

## Feature Interactions

Portability is not a standalone feature. It is the packaging layer that binds
the rest of the architecture:

- Contract-first autonomy supplies the authority model for `init`, `lane`,
  `tool`, and `postflight`.
- Persistent repository memory supplies the context graph, freshness checks,
  cached packets, and trace-backed evidence available in every project.
- Capability-oriented tooling lets project-specific tools plug in without
  changing the model-facing vocabulary.
- Auditable planning and handoff define which artifacts may authorize edits,
  verification, and continuation.
- Local-model operations provide role routing, backend lifecycle, queue
  admission, degraded mode, and context budget enforcement.
- Eval/audit/learning converts install checks, lane traces, tool calls, and
  recovery events into regression protection.
- The sidecar surface gives LLM agents the same evidence and contract
  primitives without running the full harness loop.

## Greenfield Deliverables

A portability-ready build should deliver:

- an embedded default configuration schema with generated documentation
- `harness init` templates for the supported project families
- `harness init --auto` detection for language, package manager, test command,
  backend availability, and existing agent rules
- `harness doctor` checks for configuration, backend health, model availability,
  context index state, external tool manifests, sidecar schemas, redaction
  policy, and verification commands
- role-based backend routing with explicit pool, context-window, thinking-mode,
  concurrency, and degraded-mode fields
- canonical lane resources embedded in the harness binary
- project prompt overlays that merge without modifying canonical prompts
- tool manifest validation and schema drift detection
- a stub-model lane fixture that proves the install can execute a complete
  contract, telemetry, audit, and postflight loop without a real model
- release artifacts for the intended distribution channels
- a minimal sidecar smoke test for `harness tool list`, schema export, and one
  read-only evidence tool

## Testing Expectations

Portability should be tested as a product surface, not only as helpers:

- Golden tests for generated `.harness/` skeletons and overlay merge order.
- Table tests for autodetection across empty repos, common language repos,
  mixed-language repos, missing toolchains, and invalid config.
- Contract tests for every CLI command envelope: JSON output, exit codes,
  stderr policy, schema version, and redaction behavior.
- Stub backend integration tests that run lane, postflight, audit, and sidecar
  flows without a real model.
- Doctor fixture tests for passing, warning, degraded, and blocking states.
- Backward-compatible schema tests for config, backend profiles, tool
  manifests, packet records, and telemetry events.
- Negative tests that prove project overlays cannot disable hard invariants
  such as protected paths, verification gates, redaction, and audit event
  emission.

## Completion Criteria

The portability layer is complete when a new repository can:

1. install the harness
2. run `harness init --auto`
3. inspect and accept inferred configuration
4. run `harness doctor` with clear readiness output
5. execute a stub lane that emits contract, telemetry, packet, audit, and
   postflight artifacts
6. expose at least one read-only sidecar tool to an LLM-agent caller
7. run the relevant tests without requiring repository-specific wrapper scripts

The resulting project should feel local and configurable, but the architecture
should remain harness-owned: contracts, schemas, telemetry, audit, freshness,
and verification are shared infrastructure.
