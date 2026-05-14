# P9.2 Release Decision

Decision date: 2026-05-14

Decision: continue implementation and evidence gathering. This is not a go-to-release
decision. The current release claim is no-go until the P9.3-P9.12 gates below
produce reviewed evidence.

Evidence source: a fresh P9.1 smoke bundle generated with
`go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.2-smoke.json`.
The ignored generated bundle reported
`bundle_id:release-evidence-72e0530313501ba1`,
`artifact_hash:72e0530313501ba1`, and top-level `status:partial`.

## Rationale

Idiom should continue because the available evidence still supports the north-star
direction: a local-first coding-agent control plane/substrate with privacy-safe
evidence, reviewable gates, and integration confidence. The current evidence does
not support a release readiness claim because the generated P9.1 bundle is partial
and explicitly needs more evidence.

Measured criteria from the P9.1/P0-P8 evidence:

| Criterion | Current evidence | Decision impact |
| --- | --- | --- |
| Release bundle status | P9.2 smoke bundle status is `partial`; baseline evidence is `present`; current command evidence is `missing`; command and artifact deltas are `needs_evidence`. | No release claim. Continue only to close evidence gaps. |
| Sidecar integration proof | P9.1 bundle marks sidecar integration `pass` for shell/CI script, external coding-agent workflow, and MCP stdio paths. | Supports continuing the substrate thesis. Does not override missing release gates. |
| MCP proof | MCP stdio path is `pass` with a replay fixture reference and P9.8 readiness is `evidence_present`. | Keep MCP in scope as integration evidence, pending P9.8 review/signoff. |
| Client classification readiness | All seven classifications are `needs_evidence` because validation records are missing. | No client-row release claims. Each row remains limited or deferred as listed below. |
| Benchmark, local-ops, and competitive evidence | Benchmark/local-ops section is `needs_evidence`; local-ops report is `not_available`; no competitive benchmark artifact was supplied. | No productivity, competitive, local-ops reliability, or broad performance claim. |
| Audit and security posture | Audit/security status is `needs_evidence`; current harness audit artifacts were not supplied. Product security/privacy and audit gates are present in product evidence, but not enough for release signoff. | P9.3 and P9.9 remain blocking gates. |
| Validation records | Required current validation command evidence is missing for context status, harness audits, SWE representative suite, `go test ./...`, `go test ./cmd/idiom -count=1`, `git diff --check`, and product-evidence export. | Release review must be reopened after validation evidence exists. |
| Product evidence export | Product evidence status is `partial`; signal completeness is `partial`; export id is `product-evidence-b7698265520e86eb`. | Product evidence can inform gaps, but cannot justify release or commercial claims. |
| Privacy posture | Bundle privacy is metadata-only; forbidden-field check is `pass`; raw prompts, raw protocol bodies, raw file bodies, absolute private paths, and secrets are all reported absent. | Privacy-safe evidence shape is acceptable for continuing. Maintain this as a hard release invariant. |

## Client Classification Stance

| Classification | Current stance |
| --- | --- |
| `finance` | Regulated proof-only. No finance compliance, FINRA-specific, retention, or formal attestation claim until P9.3/P9.9 evidence and reviewed controls exist. |
| `healthcare` | Regulated proof-only. No HIPAA, PHI-processing, or healthcare compliance claim; demos must stay non-sensitive and metadata-only. |
| `public-sector` | Regulated proof-only. No FedRAMP, DoD, NIST certification, offline procurement, fleet-admin, or managed support claim. |
| `enterprise-saas` | Primary paid beachhead remains the working hypothesis, but buyer-value, retained-use, paid/internal-platform signal, and validation records are missing. |
| `tooling-vendor` | Secondary integration path. CLI/schema/MCP evidence supports exploration, but partner packaging, SDKs, conformance, and custom embedding remain out of release scope. |
| `local-model` | Secondary enabling segment. Current evidence supports readiness/privacy shape only; no model-quality, capacity, backend reliability, or tuning-support claim. |
| `open-source` | Adoption wedge and proof pool. Activation evidence can support docs/demo confidence, but it does not count as paid enterprise-SaaS demand. |

## Required Follow-Up Gates

Release claims remain blocked until these gates have reviewed evidence:

| Gate | Required before release claim |
| --- | --- |
| P9.3 | No unresolved high contract/security regressions; all high/medium audit findings have owner, status, evidence, and reproduce commands. |
| P9.4 | Complete client-classification example coverage and reviewer signoff that examples use generic primitives, not vertical-specific branches. |
| P9.5 | Review-protocol compliance for every implemented ledger task. |
| P9.6 | Activation/onboarding evidence with measured install-to-first-value, cleanup path, docs gaps, blocker reasons, and privacy-safe output. |
| P9.7 | Design-partner validation, retained-use evidence, and at least one credible paid or internal-platform signal for the primary ICP. |
| P9.8 | Reviewed proof for shell/CI, external coding-agent workflow, and MCP stdio using shared profile/schema/freshness/audit contracts. |
| P9.9 | Reviewed security/privacy posture, threat model, data-flow map, redaction/retention, protected paths, side effects, and control mapping. |
| P9.10 | Competitive benchmark coverage or documented infeasibility for each external baseline, with unsupported claims removed. |
| P9.11 | Runnable client demo transcripts and honest MVP/final-state limitations for every classification. |
| P9.12 | Packaging, pricing, support, admin, and ICP thesis tied to product evidence, with a final continue/pivot/no-go decision. |

## Blockers

The current release no-go is caused by these unresolved blockers:

- Current command evidence directory is missing from the P9.1 bundle input.
- Required validation command evidence is missing for context status, harness
  audits, SWE representative suite, Go tests, diff hygiene, and product-evidence
  export.
- Current harness audit artifacts are not supplied.
- Local-ops benchmark evidence is not supplied.
- Competitive benchmark artifact is not supplied; P9.10 owns the external-baseline
  claim gate.
- Product evidence export is partial.
- All seven client classifications are missing validation records.

## Pivot Or Rollback Path

If P9.3-P9.12 evidence remains missing after the next release review, pivot from
commercial release claims to a CLI-only/internal preview of the sidecar substrate:

- Keep the sidecar-only substrate scope: local CLI, metadata-only evidence,
  sidecar recipes, context/freshness/audit primitives, and MCP proof where
  reviewed.
- Defer regulated claims for finance, healthcare, and public-sector until control
  evidence and support posture pass P9.3/P9.9.
- Defer competitive claims until P9.10 benchmark coverage or documented
  infeasibility exists.
- Defer paid enterprise-SaaS packaging until P9.7/P9.12 produce buyer or
  internal-platform evidence.
- If enterprise-SaaS buyer evidence stays weak but integration evidence remains
  strong, evaluate a named tooling-vendor-led pivot in P9.12. If local readiness
  evidence is the strongest signal, evaluate a named local-model-led preview.

This preserves the sales thesis as an evidence-first substrate while avoiding a
hardcoded model strategy, vertical-specific behavior, or unsupported release
claim.
