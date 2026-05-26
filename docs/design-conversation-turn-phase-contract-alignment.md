# Conversation Turn-Phase Contract Alignment

Status: design note for review, drafted 2026-05-25

## Problem

The generic scripted-contract smoke run now covers no-tool chat, source-backed
information, and read-only coding help. The tasks pass functionally, but the
live logs still report `turn_phase_contract_contradiction` findings during
contract replay for conversation turns that use textual tool transport.

The repeated contradiction shape is:

- the turn-phase contract advertises callable read/search tools
- the runtime call reports textual transport and expressible action classes that
  do not include those tool actions
- later `next_action_state` guidance can advertise read/search after the phase
  contract has marked those actions invalid

This is not a single prompt-string bug. It is a projection mismatch between the
model-facing protocol, runtime-call metadata, tool manifest visibility, and
audit replay. Treating it as another late nudge would weaken Idiom's contract
model.

## Current Narrow Fix

The implemented code change only addresses an immediate false closeout:
conversation evidence is not sufficient until every objective-named source file
has concrete source-body evidence. That preserves the existing loop-control
intent for ordinary direct answers while allowing a named multi-file read-only
question to gather the named evidence before final synthesis.

## Proposed Larger Direction

Define one canonical projection for conversation turns:

- `read_only_evidence_pending`: read/search actions are valid and textual tool
  syntax is expressible.
- `read_only_evidence_ready`: read/search actions are no longer valid; only a
  final answer or blocker is valid.
- runtime-call metadata, visible tool manifests, `next_action_state`, and audit
  replay must all derive from the same phase state.

The contract projector should not say textual transport cannot express a
read/search tool when the runner is actively accepting textual JSON tool
requests. Conversely, once the phase is answer-ready, no model-visible surface
should continue to advertise read/search as a valid next action.

## Review Questions

1. Should textual JSON tool requests be modeled as expressible read/search
   action classes in `runtime_call`, or should textual tool use be represented
   separately from action-class expressibility?
2. Which component should own the transition from evidence-pending to
   evidence-ready: `deriveTurnPhaseContract`, `next_action_state`, or a shared
   readiness projector used by both?
3. Should contract replay treat manifest-visible tools under textual transport
   as a contradiction, or only as a contradiction when those tools are visible
   after the phase marks the action class invalid?

## Acceptance Criteria

- The generic scripted-contract smoke suite remains green.
- `harness_audit` / contract replay no longer reports
  `turn_phase_contract_contradiction` for passing generic conversation turns.
- Existing loop-control regressions still pass: repeated broad discovery after
  bounded evidence is stopped, final tool-shaped answers are rejected, and
  normal no-tool chat remains tool-free.
- The fix updates tests at the projector/audit boundary, not only live scripts.
