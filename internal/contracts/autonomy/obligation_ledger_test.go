package autonomy

import (
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Obligation ledger replay", func() {
	It("carries open and closed obligations from structured events", func() {
		contract := newEventTestPhaseContract()
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

		Expect(ledger.Total).To(Equal(2))
		Expect(ledger.Open).To(BeEmpty())
		Expect(ledger.Resolved).To(HaveLen(1))
		Expect(ledger.Waived).To(HaveLen(1))

		resolvedEntry := ledger.Resolved[0]
		Expect(resolvedEntry.Status).To(Equal(ObligationResolved))
		Expect(resolvedEntry.FirstEventID).To(Equal(opened.EventID))
		Expect(resolvedEntry.LastEventID).To(Equal(resolved.EventID))
		Expect(resolvedEntry.RequiredBy).To(Equal("post_write_verification"))
		Expect(resolvedEntry.EvidenceEventID).To(Equal("verification-pass"))
		Expect(resolvedEntry.Resolution).To(Equal("go test ./..."))
		Expect(ledger.Waived[0].Status).To(Equal(ObligationWaived))
		Expect(ledger.Waived[0].WaiverReason).To(Equal("source-satisfied closeout"))
		Expect(ledger.Waived[0].ApprovedBy).To(Equal("operator"))
	})

	It("keeps evidence, verification, and final-answer obligations distinct", func() {
		contract := newEventTestPhaseContract()
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

		Expect(ledger.Total).To(Equal(3))
		Expect(ledger.Open).To(HaveLen(2))
		Expect(ledger.Resolved).To(HaveLen(1))
		Expect(ledger.KindCounts).To(HaveKeyWithValue(string(ObligationEvidence), 1))
		Expect(ledger.KindCounts).To(HaveKeyWithValue(string(ObligationVerify), 1))
		Expect(ledger.KindCounts).To(HaveKeyWithValue(string(ObligationFinalAnswer), 1))
		Expect(normalizedObligationIDsContain(ledger.Open, "evidence", "shared-obligation")).To(BeTrue())
		Expect(normalizedObligationIDsContain(ledger.Open, "final_answer", "shared-obligation")).To(BeTrue())
		Expect(normalizedObligationIDsContain(ledger.Resolved, "verify", "shared-obligation")).To(BeTrue())
	})

	It("carries blocked, stale, superseded, and blocker states", func() {
		contract := newEventTestPhaseContract()
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

		Expect(ledger.Total).To(Equal(4))
		Expect(ledger.Blocked).To(HaveLen(1))
		Expect(ledger.Stale).To(HaveLen(1))
		Expect(ledger.Superseded).To(HaveLen(1))
		Expect(ledger.Open).To(HaveLen(1))
		Expect(ledger.Blocked[0].Status).To(Equal(ObligationBlocked))
		Expect(ledger.Blocked[0].FirstSequence).To(Equal(int64(1)))
		Expect(ledger.Blocked[0].LastSequence).To(Equal(int64(2)))
		Expect(ledger.Open[0].Kind).To(Equal(ObligationBlocker))
		Expect(ledger.KindCounts).To(HaveKeyWithValue(string(ObligationBlocker), 1))
		Expect(report.ReplayClassification.ObligationEvents).To(Equal(len(events)))
	})

	It("carries approval, budget, loop, and snippet obligation kinds", func() {
		contract := newEventTestPhaseContract()
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
			Expect(ledger.KindCounts).To(HaveKeyWithValue(string(kind), 1))
		}
		Expect(ledger.Total).To(Equal(4))
		Expect(ledger.Open).To(HaveLen(4))
	})

	It("escapes obligation identity components to avoid slash collisions", func() {
		contractA := newEventTestPhaseContract()
		contractA.ContractID = "contract/a"
		contractB := newEventTestPhaseContract()
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
		Expect(ledger.Total).To(Equal(2))
		Expect(ledger.Open).To(HaveLen(1))
		Expect(ledger.Resolved).To(HaveLen(1))
		Expect(ledger.Open[0].Identity).To(Equal("contract%2Fa/verify/b"))
		Expect(ledger.Resolved[0].Identity).To(Equal("contract/verify/a%2Fb"))
	})

	It("keeps the audit ledger consistent with obligation coverage", func() {
		contract := newEventTestPhaseContract()
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

		Expect(report.ObligationCoverage.Opened).To(Equal(2))
		Expect(report.ObligationCoverage.Resolved).To(Equal(1))
		Expect(report.ObligationCoverage.UnresolvedObligations).To(HaveLen(1))
		Expect(report.ObligationLedger.Open).To(HaveLen(1))
		Expect(report.ObligationLedger.Open[0].ObligationID).To(Equal("obl-verify"))
		Expect(report.ObligationLedger.Resolved).To(HaveLen(1))
		Expect(report.ObligationLedger.Resolved[0].ObligationID).To(Equal("obl-evidence"))
	})
})

func normalizedObligationIDsContain(entries []NormalizedObligation, kind string, obligationID string) bool {
	return slices.ContainsFunc(entries, func(entry NormalizedObligation) bool {
		return string(entry.Kind) == kind && entry.ObligationID == obligationID
	})
}
