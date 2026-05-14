---
tags:
  - idiom
  - glossary
---

# Glossary

## Agent Substrate

The reusable local infrastructure that gives coding agents repository memory,
tool schemas, contracts, verification, telemetry, and audit.

## Artifact

A structured output used by a workflow. Examples include atom specs,
`submit_plan` artifacts, architect packets, phase evidence, and review-repair
reports.

## Autonomy Contract

The runtime contract that describes model permissions, tool capabilities, path
roles, obligations, side effects, verification expectations, and readiness
criteria.

## Capability

A task-level operation the model wants, such as localizing a change, inspecting
a symbol, finding references, running source sanity, or auditing behavior.

## Capability Adapter

The concrete implementation of a capability. For example, a language server,
graph query, structural search, shell command, or generic fallback.

## Completion Gate

The deterministic check that decides whether a final answer is supported by
closed obligations, evidence, and verification.

## Context Graph

The persistent repository memory graph stored under `.idiom/comprehension`.
It records files, packages, symbols, imports, calls, tests, diagnostics,
freshness, views, and packet cache state.

## Context Packet

A bounded task-specific evidence envelope produced by `context_pack`. It can
include ranked files, symbols, tests, provenance, freshness, and a `pack_id`.

## Deterministic Evidence-Based Development

The working pattern of collecting evidence, encoding behavior in tests or
contracts, making scoped changes, verifying with source-aware checks, and
feeding results into docs, audit, evals, or memory.

## Evidence Envelope

A bounded artifact that carries facts, provenance, confidence, and uncertainty
without granting additional authority by itself.

## Freshness

The relationship between evidence and the current workspace state. Freshness
answers whether a packet, graph view, or runtime trace still reflects the files
and context being used.

## Harness

The deterministic system around the model: prompts, runner, tool manager,
contracts, lanes, repository memory, verification, telemetry, audit, and evals.

## Lane

A structured workflow from task brief to final result, source edits, artifact,
or blocker. Examples include `implement`, `plan-execute`, and `atom-spec`.

## Model Strategy

The model's responsibility to decide how to approach a task, what evidence is
needed, and how to reason about the result, within harness contracts.

## Packet ID

The identifier for a context packet. In plan-execute workflows it can round-trip
from planner to executor for traceable handoff.

## Path Role

The contract role assigned to a path, such as required edit path, protected
path, forbidden path, or evidence-only path.

## Readiness

Harness-computed observable state that determines whether work can finish.
Readiness considers obligations, edits, evidence, verification, blockers, and
final claims.

## Sidecar

The integration surface that lets external agents use Idiom's repository-aware
tools, schemas, memory, verification, and audit systems.

## Source Sanity

A source-aware diagnostics or syntax sanity route for checking code changes
beyond patch hygiene. It is separate from focused tests and builds.

## Tool Profile

The set of tools available to a model in a given mode or lane phase.

## Verification

Evidence that a claim has been checked. Verification can include tests, builds,
source sanity, runtime traces, audit, evals, and documented blockers.

## Related Notes

- [[00 - Home]]
- [[02 - Deterministic Evidence-Based Development]]
- [[09 - Verification Readiness and Completion]]
- [[13 - Package Guide]]
