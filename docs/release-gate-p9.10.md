# P9.10 Competitive Benchmark Release Gate

Gate date: 2026-05-14

Decision: no-go, needs external-baseline evidence. P9.10 does not support a
competitive readiness, productivity, or superiority claim for the current
repository evidence.

Artifact acceptance scope: this report can be accepted only as a privacy-safe,
fail-closed release-gate artifact. It records the current machine-readable
P9.10 status, required external baselines, coverage counts, blockers, evidence
IDs, and claim impact. It does not update the implementation tracker, does not
commit generated `.idiom` artifacts, and does not approve release.

P9.10 passes only when every required external baseline from the P4.6
localization competitive-condition contract is either measured fairly or has
documented infeasibility with verification method, blocker reason, artifact and
command evidence, and claim impact. Local Idiom-only metrics such as
`context_pack`, raw search/read, and generated Idiom discovery reports can
support benchmark context, but they cannot satisfy this gate by themselves.

## Evidence Inputs

| Input | Evidence summary | Gate effect |
| --- | --- | --- |
| P4.6 localization corpus | `testdata/localization/recent-implementation-corpus.json` is loaded as the default competitive evidence source. It is privacy-reviewed and metadata-only, but the current corpus has no measured or documented-infeasible evidence for the required external baselines. | Supplies current fail-closed P9.10 counts. |
| Required external baselines | `aider_repo_map` and `external_agent_native` are derived from the localization competitive baseline definitions. | Both are required before P9.10 can pass. |
| Product evidence export | `idiom product-evidence export --project-root .` reports P9.10 `needs_evidence`, required external baselines `2`, external baseline rows `2`, covered external baselines `0`, measured external baselines `0`, documented-infeasible external baselines `0`, missing external baselines `0`, malformed baseline rows `0`, self-referential baselines `0`, and evidence ID `p4.6_localization_competitive_benchmark`. | Product evidence exposes explicit zero-valued competitive counts without overclaiming. |
| Release evidence bundle | `idiom release evidence --project-root . --output .idiom/release/p9.10-smoke.json` reports matching P9.10 `needs_evidence`, counts, blockers, evidence IDs, and claim impact in `release_readiness` and `benchmark_local_ops.competitive_coverage`. | Release evidence agrees with product evidence and remains partial/no-go. |
| Privacy posture | Generated summaries store IDs, counts, statuses, and bounded blocker strings only. | No raw prompts, file bodies, private paths or URLs, secrets, regulated personal data, or partner-identifying details are included. |

Generated `/tmp` and `.idiom/release` outputs are local reproduction artifacts.
This document stores only command labels, statuses, counts, evidence IDs, and
bounded blocker semantics.

## Gate Result

| Check | Required pass condition | Current status | Gate effect |
| --- | --- | --- | --- |
| Required external baseline coverage | Every required external baseline is measured or documented infeasible. | Fail closed: `covered_external_baselines=0` of `required_external_baselines=2`. | Blocks competitive readiness claims. |
| Aider-style repo-map baseline | Measured result or documented infeasibility with command ID, artifact, verification method, blocker reason, and claim impact. | Needs evidence: no measured result or documented infeasibility. | Blocks P9.10. |
| External-agent native baseline | Measured result or documented infeasibility with command ID, artifact, verification method, blocker reason, and claim impact. | Needs evidence: no measured result or documented infeasibility. | Blocks P9.10. |
| Self-referential evidence | Idiom-only local metrics cannot satisfy the gate by themselves. | Enforced by tests and gate logic. | Prevents local-only benchmark overclaiming. |
| Malformed or incomplete rows | Missing baseline, unsupported classification, missing command/artifact/verification method, stale/no-go/incomplete status, or missing claim impact fails closed. | Enforced by tests and structured blockers. | Keeps evidence reviewable and non-ambiguous. |
| Product/release parity | Product and release evidence must expose matching P9.10 status, counts, blockers, and evidence IDs. | Pass for current generated smokes: both report `needs_evidence` with matching counts and blockers. | Reviewers can consume either machine-readable surface. |

Current P9.10 classification: **NO-GO for competitive benchmark readiness**.

## Claim Impact

Allowed claim: Idiom now has a machine-readable P9.10 gate that fails closed on
missing, malformed, self-referential, stale, no-go, or incomplete competitive
baseline evidence.

Disallowed claim: P9.10 does not prove competitive superiority, productivity
lift, Aider parity, external-agent parity, enterprise SaaS readiness, tooling
vendor readiness, open-source adoption readiness, regulated readiness, paid
success, or release GO.

## Residual Blockers

- `aider_repo_map` has no measured result or documented infeasibility in the
  current P4.6 localization evidence.
- `external_agent_native` has no measured result or documented infeasibility in
  the current P4.6 localization evidence.
- No competitive claim should be made until both required external baselines are
  covered or have documented infeasibility with claim impact.

## Reproduction Commands

```sh
go test ./cmd/idiom -run 'TestP910|TestP46|TestP711|TestRunReleaseEvidence|TestRunProductEvidence' -count=1
go test ./internal/eval -run 'Test.*Competitive|Test.*Localization|Test.*Benchmark' -count=1
go test ./cmd/idiom ./internal/eval -count=1
go test ./cmd/idiom -count=1

jq empty testdata/localization/recent-implementation-corpus.json

go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.10-product-evidence.json
jq -r '.release_gate_linkage[] | select(.gate=="P9.10") | [.gate,.status,.evidence,((.evidence_ids // [])|join(",")),((.blockers // [])|length|tostring)] | @tsv' /tmp/idiom-p9.10-product-evidence.json

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.10-smoke.json
jq -r '.release_readiness[] | select(.gate=="P9.10") | [.gate,.status,.evidence,((.evidence_ids // [])|join(",")),((.blockers // [])|length|tostring)] | @tsv' .idiom/release/p9.10-smoke.json
jq -r '.benchmark_local_ops.competitive_coverage | [.gate,.status,.evidence,((.evidence_ids // [])|join(",")),((.blockers // [])|length|tostring)] | @tsv' .idiom/release/p9.10-smoke.json

git diff --check
```

Expected current result: product and release evidence both report P9.10
`needs_evidence`; counts show two required external baselines, two external
baseline rows from the default corpus, and explicit zero-valued covered,
measured, documented-infeasible, missing-baseline, malformed-row, and
self-referential counts; blockers identify missing measured or
documented-infeasible evidence for `aider_repo_map` and
`external_agent_native`; evidence IDs include
`p4.6_localization_competitive_benchmark`; forbidden-field checks remain safe;
and the overall release status remains partial/no-go until later gates close.
