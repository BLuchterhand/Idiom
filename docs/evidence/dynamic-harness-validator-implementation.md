# Dynamic Harness Validator Implementation Evidence

Status: first atom implemented and reviewer-approved

Source design:

- `docs/dynamic-harness-validator-design.md`
- `docs/obsidian/05 - Runner Contracts and Control Rails.md`

## Reviewer Findings Folded In

The full reviewer bench found that a runner-local prose recovery was not enough
to satisfy the design. The implemented first atom therefore records a typed
`dynamic_validator_decision`, renders the model-facing card from that decision,
and covers both corrected-write and repeated-detour outcomes in unit and
scripted contract tests.

Accepted staged risks:

- Runtime/model-call correlation remains a later audit atom. The validator does
  not choose backend, parser, thinking, or token policy.
- Scoped-read and completion-gate consolidation remain later atoms; the
  existing runner gates still enforce those paths.

## Atom Ledger

| Atom | State | Scope | Evidence | Tests |
| --- | --- | --- | --- | --- |
| `dynamic-validator-001` | implemented | Missing-description repair for in-scope required-path writes | `context_pack:cb7a08ab254200dc`, reviewer bench feedback, live log `logs/conversations/20260524-104058.895.jsonl` | `go test ./internal/runtime/agent -run 'DynamicValidator\|WriteDescription\|WriteReadyPacketRepeatedEmptyPathReadReturnsTypedBlocker' -count=1`, `make scripted-contract-suite`, `make scripted-contracts` |

## Implemented Contract

- Missing `description` on an in-scope required-path write emits
  `dynamic_validator_decision` with status `force_write_or_block`, invariant
  `write.description.required`, recovery id `write_description_repair`, missing
  item `write_description_missing`, valid actions
  `same_write_with_description` / `typed_blocker`, invalid non-write actions,
  and hidden tool class `non_write_tools`.
- The rendered validator card is compact and non-copyable.
- The corrected same write executes through the existing workspace guard path.
- Repeated non-write detours close with blocker kind
  `write_description_repair_detour_repeated`.
- Validator events do not contain raw edit payloads or file bodies.

## Coverage

Unit and runner coverage:

- `internal/runtime/agent/dynamic_validator_test.go`
- `internal/runtime/agent/write_description_guard_test.go`
- `internal/runtime/agent/runner_test.go`

Scripted/live coverage:

- `testdata/scripted-contracts/scripts/scripted-implement-missing-description-repair.json`
- `testdata/scripted-contracts/scripts/scripted-implement-missing-description-detour-blocker.json`

Docs refreshed:

- `docs/dynamic-harness-validator-design.md`
- `docs/obsidian/05 - Runner Contracts and Control Rails.md`
- `docs/obsidian-export/docs/obsidian/05 - Runner Contracts and Control Rails.md`

## Verification

Completed on 2026-05-24:

- `go test ./internal/runtime/agent -run 'DynamicValidator|WriteDescription|WriteReadyPacketRepeatedEmptyPathReadReturnsTypedBlocker' -count=1`
- `go test ./internal/runtime/agent ./internal/surfaces/tui/app ./internal/workflows/planexecute ./cmd/idiom-swe-eval -count=1`
- `make scripted-contract-suite`
- `make scripted-contracts`
- `go test ./...`
- `git diff --check`
- `idiom tool harness_audit --input '{"purpose":"behavior"}'`

`make scripted-contracts` passed all 28 live scripted tasks. The new validator
coverage includes corrected missing-description repair and repeated non-write
detour closure with `write_description_repair_detour_repeated`.

Audit note: `harness_audit` completed and reported existing historical log
findings, including implementation session lifecycle gaps and turn-phase
contract contradictions across capped historical logs. Those are not introduced
by this atom and remain separate audit/remediation work.
