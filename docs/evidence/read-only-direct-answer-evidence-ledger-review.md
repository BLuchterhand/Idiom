# Read-Only Direct-Answer Evidence Ledger Review

## Failure Evidence

- Failure log: `logs/conversations/20260604-043943.474.jsonl`.
- Final stop: `answer_objective_unmet`.
- Run shape: 26 model calls, 23 tool results, 5 rejected actions.
- Completion gate rejected the final answer twice; the second rejection still reported no objective-relevant evidence paths.
- Initial context packet was advisory but weak for the user objective: `.idiom/comprehension/views/521a634b50481d7c.json` ranked harness/dispatch files and did not by itself establish TUI up/down message navigation behavior.
- Later source reads reached objective-relevant UI files, including `internal/app/update_handlers.go` and `internal/app/rendering.go`, but final-answer readiness did not canonicalize those observed reads into answerable evidence.

## Approved Design

- Treat `context_pack` as localization/advisory evidence only.
- Make `readOnlyAnswerabilityLedger` the canonical source for read-only direct-answer readiness.
- Allow source-body and diagnostic observed evidence to support final claims when it directly addresses the user objective.
- Do not promote citation/path presence alone into answerability.
- Do not add a prompt, path, sanitizer, or scenario-specific special case.

## Reviewer Approvals

- Incident Root-Cause: approved if the fix repairs the broken invariant at the ledger/readiness boundary rather than making the logged scenario pass by exception.
- Harness/Go/Test Oracle: approved if tests prove multi-source ledger evidence, direct-answer readiness, and presentation behavior without relying on absence-only assertions.
- North-Star/Architecture/de-drift: approved if the ledger remains generic, evidence-led, and advisory-pack aware without giving the harness strategy ownership.

## Implementation Intents

- Add ledger multi-source support for source reads and diagnostic observations.
- Select the direct-answer readiness and presentation path from ledger-backed objective evidence.
- Add regressions for weak context-pack plus later source-read answerability, citation-alone non-promotion, and final-answer presentation using ledger evidence.
