[2m  ▎ thinking[0m
## Review: docs/design-direct-answer-efficiency.md

### Verified facts

1. **A1 and A6 share `internal/runtime/agent`** — both atoms touch the same package and are marked serialized/coordinated in the ledger, meaning their implementation order and contract boundaries are already recognised as coupled.
2. **Route telemetry has a role-alias mismatch** — benchmark logs show `deepseek-v4-pro` while the intended deployment was `deepseek-v4-flash`. The design doc proposes A4 effective model telemetry, but the evidence already surfaces a concrete authority leak: one layer (benchmark config → route resolution) stamped a different identity than the provider actually served.
3. **Session-summary gaps** — Reasonix output included `cache_hit_tokens`, `cache_miss_tokens`, `cost`, `currency`; Idiom trials lacked them entirely. A5 aims to add hosted cache/cost metrics, but this is a gap in the existing summary contract, not a novel feature.
4. **A2 touches `final_answer_acceptance`** — the citation classifier will read from or gate the final-answer acceptance boundary. This is the same package that enforces citation-validity contracts for source-backed output.
5. **A3 touches `internal/runtime/llama/client.go`** — DeepSeek `reasoning_content` protocol repair is local to one provider client, but the evidence does not say whether other provider clients have analogous content-prefix protocols that would need the same repair pattern.
6. **A5 touches usage/session summary metrics** — this overlaps with the existing summary surface that already lacked cost/cache fields. A5 is additive, not corrective to the missing fields.

### Inferences

- **A1 and A6 serialization is insufficiently scoped in the doc.** The ledger marks them "serialized/coordinated" but the design doc apparently does not define the exact handoff: A1 prefetches bounded reads to populate a cache/list; A6 provides the lightweight surface that consumes those reads. If A6 runs before A1's prefetch contract is sealed, it will fall back to unbounded reads or stale content. The missing boundary is: what is the exact data contract between A1's prefetch output and A6's read surface?
- **The role-alias mismatch is a route-authority bug that A4 partially addresses, but not at root.** A4 proposes "effective model telemetry" — stamping the model identity actually used into telemetry. The root problem is that the route resolution layer is allowed to substitute or alias the model identity *without* the telemetry layer detecting the mismatch. A4 fixes the *reporting* but not the *detection*: the harness should either reject a role alias that diverges from the configured model, or should atomically propagate the resolved identity back through the decision record.
- **The missing cache/cost fields in session summaries are a contract gap, not a missing feature.** A5 will add them, but the evidence shows Reasonix already emitted them — meaning the idiom session-summary contract simply never required them. A5 should be written as a contract fix (required field set) rather than an additive feature, so that any summary that omits them is a failed contract, not a silently missing row.
- **A2's citation classifier risks duplicating or conflicting with final-answer acceptance gates.** If the acceptance gate already validates citation-to-source-body correspondence, a classifier that re-checks the same invariant from a different prompt/embedding will either produce noisy overrides or require duplicate path-authority logic. The risk is *not* that both exist, but that they use different definitions of "valid citation."

### Root cause

**The design doc does not establish a single authority boundary for read-scope, citation validity, and model-identity telemetry.** Each atom is scoped to a package or surface, but the invariants that cut across atoms — *what counts as a bounded read*, *what counts as a verifiable citation*, and *what model identity the harness promises it used* — are left to each atom to interpret independently. This is the archetypal shared-boundary failure: three atoms (A1, A2, A6) independently decide what "evidence" means, and two atoms (A4, A5) independently decide what "usage" means, with no cross-atom contract to detect disagreement.

### Contributing factors

1. **A1/A6 serialization is recorded in the ledger but not in a cross-atom integration test plan.** The ledger says "serialized/coordinated" but the design doc should define the replay sequence and the invariant that A6's bounded reads are A1's prefetch output.
2. **A5 is framed as additive rather than corrective.** The existing session-summary contract already had a gap (no cost/cache fields); A5 should be framed as a contract repair so that future downstream consumers can rely on the field set.
3. **The role-alias mismatch existed before A4.** A4 is curative, not preventive. The deeper fix is to make the route layer *report* the resolved identity through the same channel the telemetry layer reads, so a mismatch is either impossible or detectable.
4. **A2's classifier has no documented relationship to the final-answer acceptance gate's citation checker.** If two independent checks exist in the same actor boundary, they will produce conflicting "valid citation" answers unless they share one path-authority implementation.

### Non-causes

- **A3 is not a root cause** — DeepSeek `reasoning_content` protocol repair is a single-provider, single-client fix with no shared-layer or cross-atom coupling. It is well-scoped and testable with a synthetic fixture (as the peer reviews already recommend).
- **The choice of DeepSeek-v4-pro vs flash is not an architecture bug** — it is a route-configuration error. The architecture should catch it (via A4 + route-identity propagation), but the bug itself is a config mismatch, not a design flaw.
- **Atom count is not a problem** — six atoms for efficiency is appropriate. The problem is the missing cross-atom contracts, not the number of work items.

### Fix recommendation

1. **Add an explicit A1↔A6 data contract to the design doc.** Define the exact shape of the prefetch output (path list, line ranges, content hash) and require A6 to consume that exact contract or produce a typed miss. Make the serialization order a runtime assertion, not a scheduling hint.

2. **Add a route-identity propagation requirement to A4.** Before A4 stamps effective model telemetry, the route layer must write the resolved model identity into a typed record that telemetry reads atomically. The benchmark logs show the mismatch happened because the identity was ambiguous at two points; require that ambiguity to be impossible after resolution.

3. **Reframe A5 as a contract repair, not an additive feature.** The session summary must have a `required_fields` set that includes `cache_hit_tokens`, `cache_miss_tokens`, `cost`, `currency`. Any summary missing them is a contract violation. This applies retroactively to existing summaries.

4. **Require A2 and the final-answer acceptance gate to share one citation-authority helper.** If both need to check that a citation (file:line) maps to a source-body read, they must call the same `ValidateCitation(path, line_range, content_hash)` function. Duplicate implementations will diverge.

5. **Add a synthetic reasoning_content fixture for A3** (already recommended by peer reviews). This is a concrete testability requirement: the client must handle a prefix like `reasoning_content\n...` without producing it in the final answer or mistaking it for answer content.

### Regression tests

- **A1/A6 integration replay:** a test that runs A1's prefetch with a known set of paths, then runs A6's read surface against the prefetch output and asserts it uses exactly those paths and ranges. A second scenario where A6 misses: assert it falls back to unbounded reads *or* produces a typed miss event (not silently falling back with no signal).
- **Route-identity mismatch detection:** a test that configures model X, injects a route alias that resolves to Y, and asserts that the telemetry record contains both the configured identity and the resolved identity as separate fields, and that the harness emits a warning-level event when they differ.
- **Session-summary required-fields contract:** a test that produces a session summary and asserts the required field set is present. Run against existing summarizers to detect the gap.
- **Citation-authority shared helper:** a test that passes a valid and invalid citation through both A2's classifier and the acceptance gate's checker, using the same helper, and asserts they agree.
- **A3 reasoning_content prefix:** feed the client a response with `reasoning_content\nactual_answer`, assert the final answer is `actual_answer` and no reasoning_content leaks to the acceptance gate.

### North-star risk

**Medium.** The design doc's atoms are individually north-star aligned (nudge, not hard-pick; bounded recovery; evidence-based action). The risk is that duplicated authority boundaries (A2/A6 citation validity, A1/A6 read scope, A4/A5 model identity) violate the north-star principle *"force only real invariants"* — if each atom enforces its own version of "evidence" and "identity," the harness will either produce redundant gates or contradict itself, which undermines trust in the substrate. The fix is to define those invariants once per concept and route all atoms through the same typed contract.

### Confidence

**High** for the A1/A6 coupling, the role-alias mismatch detection gap, and the session-summary contract gap — all are directly evidenced. **Moderate** for the A2/acceptance-gate duplication risk — it is logically necessary but we lack logs of an actual mismatch. **High** for the A3 testability gap (a concrete fixture recommendation).

### Decision

**PASS_WITH_RISKS**

The design is sound and each atom is scoped to a clear surface, but the cross-atom invariants for read authority, citation validity, and model identity are underspecified. The peer reviews already flagged the concrete missing pieces (A6 surface inventory, shared citation helper, A3 fixture, A5 contract repair). Accept the design with the conditions that:

1. The A1↔A6 data contract and serialization assertion are added before A6 implementation begins.
2. The A4 route-identity propagation requirement is added before A4 implementation.
3. A5 is reframed as a contract repair with a required-field assertion.
4. A2 and the acceptance gate share one citation-authority helper.
5. A3 includes a synthetic `reasoning_content` fixture as a pass/fail test.
  · 19699 tok · in 17228 (16640 cached / 588 new) · out 2471 (191 reasoning) · ¥0.0059
