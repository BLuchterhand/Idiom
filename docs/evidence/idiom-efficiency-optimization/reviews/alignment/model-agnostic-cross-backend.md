[2m  ▎ thinking[0m
## Model-Agnostic / Cross-Backend Review — Final

I have evaluated the folded design as described against the core constraint: **does it unblock implementation without risking local (llama.cpp/Ollama) and non-DeepSeek API backends (OpenAI, Anthropic, etc.)?**

---

### Verified facts

1. **A1/A6 six-field prefetch contract** (path, line_range, content_hash, evidence_kind, policy, miss_reason) is backend-agnostic. All fields are data-level — no field assumes a particular API response shape, streaming mode, or structured-output capability. `content_hash` is computed client-side from stored content rather than returned by a backend. `evidence_kind` is a harness-level classification, not a backend-native enum. `miss_reason` is free-text, not an enumerated const set. **No backend-specific assumption.**

2. **A3 synthetic `reasoning_content` fixture** breaks the only hard backend asymmetry. Local models (llama.cpp, etc.) and non-DeepSeek API models (OpenAI, Anthropic) do not produce a DeepSeek-style `reasoning_content` field. By making this a fixture-level concern (synthetic/absent/empty when the backend doesn't produce it), the downstream consumer never blocks on backend capability. **The fixture layer fully insulates the contract.**

3. **A4 route-owned effective model identity** avoids inferring model name from backend response fields. No parsing per backend schema. Each route stores its own effective identity at config/init time. Telemetry consumes that stored identity. **Removes the single largest cross-backend fragility point.**

4. **A5 cache/cost session-summary fields always present with unknown/zero state** means local backends (no cache metrics, no cost reporting) produce valid output on day one. No backend is forced to implement a reporting contract it cannot satisfy. **Safe default eliminates backend-specific gate.**

5. **A2 one shared citation-authority helper** is a pure harness-level function. It receives structured evidence and applies the same rule set regardless of which backend produced the evidence tokens. **No backend dependency.**

6. **A6 surface inventory** explicitly defines the boundary that all backends implement against. A new backend need only match the surface, not the internal implementation.

7. **A1/A6 integration replay before completion** — if this replay exercises the contract through ≥2 backend types (local + at least one API backend), it will catch backend-specific regressions before landing. The design does not lock replay to DeepSeek only.

---

### Remaining blockers

**None at the contract/architecture level.** All six areas (A1–A6) have been revised to eliminate backend-specific assumptions. Specifically:

- The `reasoning_content` gap (the hardest cross-backend constraint) is properly handled by the synthetic fixture in A3.
- Model identity is route-owned, not backend-derived (A4).
- No enum or structured-output schema forces backend-specific serialization at the shared contract boundary.

The only residual risk is **verification scope**, not design:

- If the A1/A6 replay suite only tests against DeepSeek, a regression for local or OpenAI backends could slip through. This is an operations/testing concern, not a design blocker.
- If the A3 fixture defaults to "empty string" but downstream consumers treat a missing field as an error rather than an absence, that's a coding bug — caught by the replay suite if it includes a non-DeepSeek backend.

---

### Implementation-start recommendation

**Start with A6 (surface inventory + interface definition).** This was already the consensus recommendation from alignment reviewers and the cross-backend analysis confirms it: the surface contract is the fixed point that bounds all other work. Once A6 is defined:

1. Implement the A1 contract against the A6 surface (six-field prefetch data).
2. Implement the A3 fixture layer (synthetic reasoning_content) — trivially testable without any backend.
3. Implement A4 (route-owned model identity, wired to telemetry) — no backend dependency.
4. Implement A5 (cache/cost defaults) — no backend dependency.
5. Implement A2 (shared citation helper) — pure harness logic.
6. Wire integration replay targeting ≥2 backend types before calling A1/A6 complete.

---

### Decision: **PASS**

The folded design unblocks implementation across all backend types. Local and non-DeepSeek backends are not risked — they are explicitly protected by:

| Risk | Protection |
|---|---|
| `reasoning_content` missing | A3 synthetic fixture |
| Model identity inference per backend | A4 route-owned identity |
| Cache/cost fields absent for local | A5 unknown/zero defaults |
| Evidence structure varies by backend | A2 shared harness-level helper |
| Prefetch contract assumes API shape | A1/A6 six fields are all generic data; hashes computed client-side |

**Recommendation to the implementation team:** Write the integration replay test against a local backend (llama.cpp or Ollama) before the DeepSeek test. This guarantees the cross-background contract is exercised on day one of integration work, not retrofitted after DeepSeek is already passing.
  · 19503 tok · in 16980 (16640 cached / 340 new) · out 2523 (1433 reasoning) · ¥0.0057
