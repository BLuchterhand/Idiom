package autonomy

import (
	"slices"
	"testing"
	"time"
)

func TestNormalizeContractObligationLedgerStableIdentityAndStatus(t *testing.T) {
	contract := AutonomyContract{
		ContractID: "contract-ledger",
		Obligations: []Obligation{
			{
				ObligationID: " obl-verify ",
				Kind:         ObligationVerify,
				Status:       ObligationStatus(" RESOLVED "),
				Owner:        OwnerHost,
				RequiredBy:   " post_write_verification ",
				EvidenceRef:  " verification-event ",
			},
		},
	}

	ledger := NormalizeContractObligationLedger(contract)

	if ledger.Total != 1 || len(ledger.Resolved) != 1 || len(ledger.Open) != 0 || len(ledger.Waived) != 0 {
		t.Fatalf("expected one resolved obligation, got %#v", ledger)
	}

	entry := ledger.Resolved[0]
	if entry.Identity != "contract-ledger/verify/obl-verify" {
		t.Fatalf("expected stable contract/kind/id identity, got %q", entry.Identity)
	}

	if entry.Status != ObligationResolved || entry.RequiredBy != "post_write_verification" || entry.EvidenceRef != "verification-event" {
		t.Fatalf("expected normalized status and fields, got %#v", entry)
	}
}

func TestReplayEventsCarriesOpenClosedObligationLedgerFromStructuredEvents(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)

	opened := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, start, 1))
	resolved := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "raw-resolved-status-mismatch",
		Type:          EventObligationResolved,
		OccurredAt:    start.Add(time.Second),
		Sequence:      2,
		ContractID:    contract.ContractID,
		Obligation: &ContractObligationEvent{
			ObligationID:    "obl-verify",
			Kind:            ObligationVerify,
			Status:          ObligationOpen,
			Owner:           OwnerHost,
			Resolution:      "go test ./...",
			EvidenceEventID: "verification-pass",
		},
	}
	waived := Event{
		SchemaVersion: EventSchemaVersion,
		EventID:       "raw-waived-status-mismatch",
		Type:          EventObligationWaived,
		OccurredAt:    start.Add(2 * time.Second),
		Sequence:      3,
		ContractID:    contract.ContractID,
		Obligation: &ContractObligationEvent{
			ObligationID: "obl-final",
			Kind:         ObligationFinalAnswer,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "completion_gate",
			WaiverReason: "source-satisfied closeout",
			ApprovedBy:   "operator",
		},
	}

	report := ReplayEvents([]Event{waived, resolved, opened}, ReplayOptions{})
	ledger := report.ObligationLedger

	if ledger.Total != 2 || len(ledger.Open) != 0 || len(ledger.Resolved) != 1 || len(ledger.Waived) != 1 {
		t.Fatalf("expected replay ledger to close resolved/waived obligations, got %#v", ledger)
	}

	resolvedEntry := ledger.Resolved[0]
	if resolvedEntry.Status != ObligationResolved || resolvedEntry.FirstEventID != opened.EventID || resolvedEntry.LastEventID != resolved.EventID {
		t.Fatalf("expected event type to normalize resolved status and preserve event span, got %#v", resolvedEntry)
	}

	if resolvedEntry.RequiredBy != "post_write_verification" || resolvedEntry.EvidenceEventID != "verification-pass" {
		t.Fatalf("expected sparse resolution event to carry forward open details, got %#v", resolvedEntry)
	}

	if resolvedEntry.Resolution != "go test ./..." {
		t.Fatalf("expected resolution detail to survive replay ledger, got %#v", resolvedEntry)
	}

	if ledger.Waived[0].Status != ObligationWaived || ledger.Waived[0].WaiverReason != "source-satisfied closeout" || ledger.Waived[0].ApprovedBy != "operator" {
		t.Fatalf("expected waived obligation in replay ledger, got %#v", ledger.Waived[0])
	}
}

func TestObligationLedgerDoesNotConflateEvidenceVerificationAndFinalAnswer(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 6, 18, 14, 30, 0, 0, time.UTC)

	events := []Event{
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "shared-obligation",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "source_evidence",
		}, start, 1)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "shared-obligation",
			Kind:         ObligationVerify,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "post_write_verification",
		}, start.Add(time.Second), 2)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID:           "shared-obligation",
			Kind:                   ObligationFinalAnswer,
			Status:                 ObligationOpen,
			Owner:                  OwnerHost,
			RequiredBy:             "completion_gate",
			FinalAnswerRequirement: "summarize changed files and verification",
		}, start.Add(2*time.Second), 3)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
			ObligationID: "shared-obligation",
			Kind:         ObligationVerify,
			Status:       ObligationResolved,
			Owner:        OwnerHost,
			RequiredBy:   "post_write_verification",
		}, start.Add(3*time.Second), 4)),
	}

	ledger := NormalizeObligationLedger(events)

	if ledger.Total != 3 || len(ledger.Open) != 2 || len(ledger.Resolved) != 1 {
		t.Fatalf("expected same obligation_id to remain distinct by kind, got %#v", ledger)
	}

	if ledger.KindCounts[string(ObligationEvidence)] != 1 ||
		ledger.KindCounts[string(ObligationVerify)] != 1 ||
		ledger.KindCounts[string(ObligationFinalAnswer)] != 1 {

		t.Fatalf("expected separate kind counts, got %#v", ledger.KindCounts)
	}

	if !normalizedObligationIDsContain(ledger.Open, "evidence", "shared-obligation") ||
		!normalizedObligationIDsContain(ledger.Open, "final_answer", "shared-obligation") ||
		!normalizedObligationIDsContain(ledger.Resolved, "verify", "shared-obligation") {

		t.Fatalf("expected open evidence/final-answer and resolved verification entries, got %#v", ledger)
	}
}

func TestObligationLedgerCarriesBlockedStaleSupersededAndBlockerKinds(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 6, 18, 15, 30, 0, 0, time.UTC)

	events := []Event{
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "source-body",
			Kind:         ObligationEvidence,
			Status:       ObligationOpen,
			Owner:        OwnerPlanner,
			RequiredBy:   "source_body_obligations",
		}, start, 1)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationBlocked, contract, Obligation{
			ObligationID: "source-body",
			Kind:         ObligationEvidence,
			Status:       ObligationBlocked,
			Owner:        OwnerPlanner,
			RequiredBy:   "source_body_obligations",
		}, start.Add(time.Second), 2)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationStale, contract, Obligation{
			ObligationID: "anchor",
			Kind:         ObligationEvidence,
			Status:       ObligationStale,
			Owner:        OwnerHost,
			RequiredBy:   "stale_anchor_policy",
		}, start.Add(2*time.Second), 3)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationSuperseded, contract, Obligation{
			ObligationID: "old-loop",
			Kind:         ObligationLoop,
			Status:       ObligationSuperseded,
			Owner:        OwnerHost,
			RequiredBy:   "loop_budget",
		}, start.Add(3*time.Second), 4)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "terminal-blocker",
			Kind:         ObligationBlocker,
			Status:       ObligationOpen,
			Owner:        OwnerPlanner,
			RequiredBy:   "terminal_blocker",
		}, start.Add(4*time.Second), 5)),
	}

	report := ReplayEvents(events, ReplayOptions{})
	ledger := report.ObligationLedger

	if ledger.Total != 4 || len(ledger.Blocked) != 1 || len(ledger.Stale) != 1 || len(ledger.Superseded) != 1 || len(ledger.Open) != 1 {
		t.Fatalf("expected blocked/stale/superseded/open entries, got %#v", ledger)
	}

	if ledger.Blocked[0].Status != ObligationBlocked || ledger.Blocked[0].FirstSequence != 1 || ledger.Blocked[0].LastSequence != 2 {
		t.Fatalf("expected blocked source-body obligation to carry event span, got %#v", ledger.Blocked[0])
	}

	if ledger.Open[0].Kind != ObligationBlocker || ledger.KindCounts[string(ObligationBlocker)] != 1 {
		t.Fatalf("expected typed blocker obligation kind in ledger, got %#v", ledger)
	}

	if report.ReplayClassification.ObligationEvents != len(events) {
		t.Fatalf("replay classification obligation events = %d, want %d", report.ReplayClassification.ObligationEvents, len(events))
	}
}

func TestObligationLedgerCarriesApprovalBudgetLoopAndSnippetCoverageKinds(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 6, 18, 15, 45, 0, 0, time.UTC)

	events := []Event{
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "approval-write",
			Kind:         ObligationApproval,
			Status:       ObligationOpen,
			Owner:        OwnerUser,
			TargetRef:    "internal/runtime/agent/runner.go",
			RequiredBy:   "write_approval",
		}, start, 1)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "loop-budget",
			Kind:         ObligationLoop,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "loop_budget",
		}, start.Add(time.Second), 2)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "tool-budget",
			Kind:         ObligationBudget,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
			RequiredBy:   "tool_budget",
		}, start.Add(2*time.Second), 3)),
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
			ObligationID: "snippet-final-state",
			Kind:         ObligationSnippet,
			Status:       ObligationOpen,
			Owner:        OwnerPlanner,
			TargetRef:    "snippet-a",
			RequiredBy:   "required_edit_snippets",
		}, start.Add(3*time.Second), 4)),
	}

	ledger := NormalizeObligationLedger(events)
	for _, kind := range []ObligationKind{ObligationApproval, ObligationLoop, ObligationBudget, ObligationSnippet} {
		if ledger.KindCounts[string(kind)] != 1 {
			t.Fatalf("expected one %s obligation, got %#v", kind, ledger.KindCounts)
		}
	}

	if ledger.Total != 4 || len(ledger.Open) != 4 {
		t.Fatalf("expected four open typed obligations, got %#v", ledger)
	}
}

func TestObligationLedgerEscapesIdentityAndAvoidsSlashCollisions(t *testing.T) {
	contractA := newEventTestPhaseContract(t)
	contractA.ContractID = "contract/a"
	contractB := newEventTestPhaseContract(t)
	contractB.ContractID = "contract"
	start := time.Date(2026, 6, 18, 14, 45, 0, 0, time.UTC)

	events := []Event{
		mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contractA, Obligation{
			ObligationID: "b",
			Kind:         ObligationVerify,
			Status:       ObligationOpen,
			Owner:        OwnerHost,
		}, start, 1)),
		{
			SchemaVersion: EventSchemaVersion,
			EventID:       "raw-resolved-slash-id",
			Type:          EventObligationResolved,
			OccurredAt:    start.Add(time.Second),
			Sequence:      2,
			ContractID:    contractB.ContractID,
			Obligation: &ContractObligationEvent{
				ObligationID: "a/b",
				Kind:         ObligationVerify,
				Status:       ObligationResolved,
				Owner:        OwnerHost,
				Resolution:   "distinct slash-bearing obligation",
			},
		},
	}

	ledger := NormalizeObligationLedger(events)
	if ledger.Total != 2 || len(ledger.Open) != 1 || len(ledger.Resolved) != 1 {
		t.Fatalf("slash-bearing contract/id values must not collide, got %#v", ledger)
	}

	if ledger.Open[0].Identity != "contract%2Fa/verify/b" {
		t.Fatalf("expected escaped contract id in display identity, got %q", ledger.Open[0].Identity)
	}
	if ledger.Resolved[0].Identity != "contract/verify/a%2Fb" {
		t.Fatalf("expected escaped obligation id in display identity, got %q", ledger.Resolved[0].Identity)
	}
}

func TestAuditEventsObligationLedgerMatchesExistingCoverage(t *testing.T) {
	contract := newEventTestPhaseContract(t)
	start := time.Date(2026, 6, 18, 15, 0, 0, 0, time.UTC)

	openVerify := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-verify",
		Kind:         ObligationVerify,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "post_write_verification",
	}, start, 1))
	openEvidence := mustAutonomyEvent(NewObligationStateEvent(EventObligationOpened, contract, Obligation{
		ObligationID: "obl-evidence",
		Kind:         ObligationEvidence,
		Status:       ObligationOpen,
		Owner:        OwnerHost,
		RequiredBy:   "source_evidence",
	}, start.Add(time.Second), 2))
	resolveEvidence := mustAutonomyEvent(NewObligationStateEvent(EventObligationResolved, contract, Obligation{
		ObligationID: "obl-evidence",
		Kind:         ObligationEvidence,
		Status:       ObligationResolved,
		Owner:        OwnerHost,
		RequiredBy:   "source_evidence",
	}, start.Add(2*time.Second), 3))

	report := AuditEvents([]Event{resolveEvidence, openVerify, openEvidence}, EventAuditOptions{})

	if report.ObligationCoverage.Opened != 2 || report.ObligationCoverage.Resolved != 1 || len(report.ObligationCoverage.UnresolvedObligations) != 1 {
		t.Fatalf("expected existing audit coverage to remain intact, got %#v", report.ObligationCoverage)
	}

	if len(report.ObligationLedger.Open) != 1 || report.ObligationLedger.Open[0].ObligationID != "obl-verify" {
		t.Fatalf("expected audit ledger open obligations to match coverage, got %#v", report.ObligationLedger)
	}

	if len(report.ObligationLedger.Resolved) != 1 || report.ObligationLedger.Resolved[0].ObligationID != "obl-evidence" {
		t.Fatalf("expected audit ledger resolved obligations to match coverage, got %#v", report.ObligationLedger)
	}
}

func normalizedObligationIDsContain(entries []NormalizedObligation, kind string, obligationID string) bool {
	return slices.ContainsFunc(entries, func(entry NormalizedObligation) bool {
		return string(entry.Kind) == kind && entry.ObligationID == obligationID
	})
}
