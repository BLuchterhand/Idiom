# P9.12 Packaging, Pricing, Support, Admin, And ICP Gate

Decision: NO-GO for commercial or production claims.

P9.12 now consumes the structured P0.11 thesis in
`docs/packaging-support-thesis.json`. The thesis records the primary ICP,
pricing hypothesis, support boundary, admin non-goals, enterprise-surface
limits, local evidence export stance, all seven classification packaging
stances, and an explicit `continue`, `pivot`, or `no_go` decision.

Current claim impact:

- Enterprise SaaS remains the primary MVP buyer.
- Open-source/local-model remain adoption and enablement wedges.
- Tooling-vendor remains integration-relevant.
- Finance, healthcare, and public-sector remain proof-only or deferred.
- Commercial and production claims remain blocked until P9.7 demand evidence
  and the other prior release gates are supplied with evidence IDs.

Required reproduction:

```bash
go run ./cmd/idiom product-evidence export --project-root . >/tmp/idiom-p9.12-product-evidence.json
go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.12-release.json
```

Review fields:

- `release_gate_linkage[] | select(.gate=="P9.12")`
- `release_readiness[] | select(.gate=="P9.12")`
- `counts.classification_stances_covered`
- `counts.unsupported_admin_request_count`
- `counts.pricing_signal_quality_hypothesis`
- `counts.decision_no_go`
- `blockers`
- `evidence_ids`
