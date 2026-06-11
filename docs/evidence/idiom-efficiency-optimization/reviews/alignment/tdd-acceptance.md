[2m  ▎ thinking[0m
**Verified facts:**

1. **A1/A6 negative fixtures** now have concrete test cases enumerated: empty path, outside-root path, invalid line_range, content_hash + miss_reason conflict, invalid evidence_kind, unknown policy, missing miss_reason — covering structural validation (path, format), semantic conflict detection, enum/oneOf boundaries, and required-field enforcement.

2. **A6 stale/missing inventory → A1 prefetch** ordering is now encoded as a behavioral contract: stale/missing inventory disables A1 prefetch with a typed miss, not silent fallthrough or a generic error. This makes the ordering testable as a state-based invariant (inventory state → prefetch eligibility) rather than relying on fragile call-sequence assertions.

3. **A3 reasoning_content fixtures** are table-driven and cover the full surface: omitted (field absent), empty (field present but empty), normal, multiline, special characters, truncated, streamed (per-chunk), and non-streamed (single payload). This is sufficient breadth.

4. **A5 sentinel state** is now concrete: numeric `0` plus `*_known` booleans and explicit `cost_known`/`currency` fields. The sentinel is typed and assertable — no reliance on nil/absent checks or silent zero-value assumptions.

5. **Cross-condition regression suite** covers direct-answer replay, citation fixture, DeepSeek fixture, effective-model telemetry, and cache/cost summary — a representative cross-section that exercises routing, metadata, and telemetry aggregation paths beyond the per-condition unit tests.

**Remaining blockers:** None.

**Implementation-start recommendation:** Proceed with implementation. The design resolves every prior blocker with concrete, testable additions. Ensure the "typed miss" for A1 prefetch when A6 inventory is stale/missing has an explicit schema or enum value in the contract (not just described in prose) so the ordering fixture has a concrete assertion target.

**Decision: PASS**
  · 18532 tok · in 16987 (16640 cached / 347 new) · out 1545 (1134 reasoning) · ¥0.0038
