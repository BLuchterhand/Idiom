# P9.3 Release Gate

Gate date: 2026-05-14

Decision: no-go, needs evidence and closure. P9.3 does not support a release
readiness claim.

Artifact acceptance scope: this report can be accepted only as a fail-closed
no-go/pivot-scoped gate artifact for P9.3. Acceptance of the artifact means the
gate honestly records the current blockers, owners, evidence summaries,
reproducers, claim impacts, and next actions. It does not mean release readiness,
release approval, or closure of the underlying findings. The current
release gate status remains no-go/needs-evidence because open high findings,
open high/medium findings, partial release-bundle evidence, missing current
evidence, partial product evidence, and missing closure evidence all remain.

P9.3 passes only when both conditions are true:

- unresolved high contract/security regressions are zero
- every high/medium audit finding has owner, status, evidence, reproduce command,
  claim impact, and next action

Current evidence fails the first condition. A fresh audit-watch packet found open
high contract findings. Current release evidence also remains partial, so missing
evidence fails closed instead of being treated as readiness.

## Evidence Inputs

| Input | Command | Privacy-safe result |
| --- | --- | --- |
| P9.1 release bundle smoke | `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.3-smoke.json` | Wrote ignored local bundle `release-evidence-72e0530313501ba1`, `artifact_hash:72e0530313501ba1`, top-level `status:partial`. |
| Behavior harness audit | `idiom tool harness_audit --input '{"purpose":"behavior"}'` | Ran successfully; scanned 251 logs; reported 17 findings, including one high lifecycle finding and medium behavior/freshness findings. |
| Audit-watch ownership packet | `go run ./cmd/idiom audit watch --config config.json --once --review-existing --debounce 0 --output .idiom/audits/p9.3-watch` | Wrote ignored local packet `.idiom/audits/p9.3-watch/20260514-113603.718513000.json`; status `open`, promotion status `open`, 294 high/medium findings with owner/status/reproducer fields. |
| P9.2 decision | `docs/release-decision-p9.2.md` | Prior release decision was no-go until P9.3-P9.12 reviewed gates have evidence. |

Do not commit the ignored `.idiom/release` or `.idiom/audits` artifacts. They are
local evidence sources for reproduction; this document stores only summaries,
hashes, relative paths, statuses, and commands.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| High contract/security regressions | Zero unresolved high contract/security regressions | Fail: audit-watch reported 118 open high findings owned by `contract` in category `completion_contract`; no high `security` category was reported. | Blocks release. |
| High/medium finding inventory fields | Every high/medium finding has owner, status, evidence, reproduce command, claim impact, and next action | Partial for release readiness, acceptable only for this fail-closed artifact: audit-watch supplies owner surface, open status, bounded evidence summary, reproduce command, and promotion next action. Claim impact is documented in this report; closure evidence is still missing for open findings. | Blocks readiness claim until open high contract findings are closed or explicitly kept inside no-go/pivot scope. |
| P9.1 release bundle | Current release bundle is complete enough to support gate claims | Fail: bundle status is `partial`. | Blocks release. |
| Harness audit source | Current harness audit evidence exists and is privacy-safe | Partial: command ran locally; P9.1 bundle still reports current harness audit artifacts not supplied. | Needs evidence bundle input update before release review. |
| Product evidence export | Product evidence is complete enough to support release claims | Fail: release bundle reports product evidence `partial`; activation, retained-use, benchmark, and learning signals are missing. | Blocks broad product claims. |
| Sidecar/MCP integration proof | Integration proof paths remain present | Pass for this gate only: release bundle reports sidecar integration `pass`, including shell/CI, external coding-agent workflow, and MCP stdio proof. | Supports substrate direction but does not override P9.3 blockers. |
| Privacy posture | No raw prompts, raw protocol bodies, raw file bodies, absolute private paths, secrets, tokens, or credentials in the P9.3 report | Pass: this report contains only metadata summaries, relative paths, IDs/hashes, counts, statuses, and commands. Release bundle privacy check also reports `pass`. | Required invariant preserved. |

## Finding Inventory

The ignored audit-watch packet is the authoritative local source for per-instance
finding rows. It reported 141 high and 153 medium findings. Every row is open and
therefore needs closure evidence, a promoted regression/eval/audit/contract fix,
or a documented bounded non-goal before release.

| Severity | Category | Owner | Status | Evidence path/summary | Reproduce command | Claim impact | Next action |
| --- | --- | --- | --- | --- | --- | --- | --- |
| high | completion_contract | contract | open | `.idiom/audits/p9.3-watch/20260514-113603.718513000.json`; 118 open high completion-contract findings, including completion-gate and required-edit-path stop reasons. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | No release claim that harness completion contracts are regression-free; regulated and enterprise release claims remain no-go. | Promote each real recurring class to regression/eval/audit/contract fix, or document bounded non-goal, then attach before/after replay or bounded no-replay reason. |
| high | audit_coverage | audit_coverage | open | Same packet; 17 high audit-coverage findings, including incomplete implement lifecycle and high-risk stop-reason triggers. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | No claim that lifecycle/audit coverage is complete enough for release signoff. | Triage owner surface, close lifecycle gaps, and record replay evidence for each retained high finding. |
| high | telemetry_gap | telemetry | open | Same packet; failed implement-process trigger findings. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | No claim that telemetry closeout evidence is complete for high-risk runs. | Promote to telemetry/audit rule or bounded non-goal with replay evidence. |
| high | tool_loop | tool_schema | open | Same packet; exact-anchor prewrite hidden-tool repeat trigger. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | No claim that tool-loop control rails are regression-free. | Promote to a focused regression or contract fix and rerun audit-watch closure. |
| medium | context_staleness | context_freshness | open | Same packet; 98 context-staleness findings and freshness refresh failure evidence. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | Freshness evidence cannot support broad release confidence until medium rows are owned through closure or accepted no-go. | Promote repeated classes to freshness tests/audit rules or document bounded non-goals. |
| medium | telemetry_gap | telemetry | open | Same packet; behavior findings for redirect storm, zero-hit search, search truncation, doc wander, and named-gap read walk. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | No claim that behavior drift is below release threshold. | Convert recurring behavior classes to evals, audit rules, or contract changes with before/after evidence. |
| medium | tool_schema | tool_schema | open | Same packet; same-tool repeat and malformed-tool-input findings. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | Tool-surface reliability remains no-go for release until medium findings have closure evidence. | Add or update focused schema/tool-loop checks, or document bounded non-goal with reproducer. |
| medium | audit_coverage | audit_coverage | open | Same packet; 28 medium audit-coverage findings, including harness-feedback and implement-process-exit trigger findings. | `idiom tool harness_audit --input '{"purpose":"behavior","limit":50}'` | Audit ownership exists, but release cannot claim reviewed closure. | Triage whether each finding is real, then close with replay or bounded no-replay evidence. |

## P9.1/P9.2 Tieback

The fresh P9.1 smoke bundle matches the P9.2 no-go direction:

- release bundle status is `partial`
- current command evidence directory is unavailable
- required validation evidence is missing for context status, harness audits,
  representative SWE suite, Go tests, diff check, and product-evidence export
- audit/security status is `needs_evidence`
- current harness audit artifacts are not supplied to the release bundle
- product evidence export is `partial`
- sidecar integration proof is `pass`, including MCP stdio
- all seven client-readiness rows need validation records

The P9.3-specific blocker added by this gate is concrete: current audit-watch
evidence shows open high contract findings, so the release must remain no-go even
if sidecar integration evidence is present.

## Client-Classification Impact

| Client classification | P9.3 impact |
| --- | --- |
| finance | No-go. Regulated proof-only; open high contract findings and partial release evidence block governance, audit, and compliance-adjacent claims. |
| healthcare | No-go. Privacy-safe shape is preserved, but missing/partial audit closure and open high contract findings block healthcare release claims. |
| public-sector | No-go. Procurement/security review cannot rely on open high contract findings or partial current validation evidence. |
| enterprise-saas | No-go. Primary ICP claim remains blocked until high contract findings are closed or explicitly scoped out and product evidence is complete enough for review. |
| tooling-vendor | No-go for release; integration proof is promising, but open high/medium findings prevent conformance or embedding-confidence claims. |
| local-model | No-go for full release; local reproducer shape is present, but missing benchmark/local-ops and open findings limit the claim to internal evidence gathering. |
| open-source | No-go for release; transparent triage is available, but contributors need a closed or accepted finding set before readiness is claimed. |

## Required Closure

P9.3 can be reconsidered only after a reviewer can reproduce a fresh gate with:

```sh
go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.3-smoke.json
idiom tool harness_audit --input '{"purpose":"behavior"}'
go run ./cmd/idiom audit watch --config config.json --once --review-existing --debounce 0 --output .idiom/audits/p9.3-watch
```

The follow-up evidence must show:

- zero open high `completion_contract` or security findings, or a reviewed
  no-go/pivot scope that removes the release claim
- high/medium finding rows retain owner, status, evidence, reproduce command,
  claim impact, and next action
- closed findings include before/after replay evidence or a bounded no-replay
  reason accepted by the gate
- the P9.1 release bundle consumes current harness audit artifacts and required
  validation command evidence

P9.9 still owns the full security/privacy posture, threat model, data-flow map,
and control mapping signoff. This P9.3 report intentionally does not complete
that broader posture review.
