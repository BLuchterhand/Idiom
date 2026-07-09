package autonomy

import (
	"net/url"
	"slices"
	"strings"
)

const ObligationLedgerSchemaVersion = "idiom.autonomy.obligation_ledger.v1"

// ObligationLedger is a report-only normalized projection of obligation state.
// It does not enforce readiness or gate decisions; it gives those callers one
// structured shape for obligation statuses observed in contracts and events.
type ObligationLedger struct {
	SchemaVersion string                 `json:"schema_version"`
	Total         int                    `json:"total"`
	StatusCounts  map[string]int         `json:"status_counts,omitempty"`
	KindCounts    map[string]int         `json:"kind_counts,omitempty"`
	Open          []NormalizedObligation `json:"open,omitempty"`
	Resolved      []NormalizedObligation `json:"resolved,omitempty"`
	Waived        []NormalizedObligation `json:"waived,omitempty"`
	Blocked       []NormalizedObligation `json:"blocked,omitempty"`
	Stale         []NormalizedObligation `json:"stale,omitempty"`
	Superseded    []NormalizedObligation `json:"superseded,omitempty"`
	Entries       []NormalizedObligation `json:"entries,omitempty"`
}

type NormalizedObligation struct {
	Identity               string           `json:"identity"`
	ContractID             string           `json:"contract_id,omitempty"`
	ObligationID           string           `json:"obligation_id"`
	Kind                   ObligationKind   `json:"kind"`
	Status                 ObligationStatus `json:"status"`
	Owner                  ObligationOwner  `json:"owner,omitempty"`
	TargetRef              string           `json:"target_ref,omitempty"`
	RequiredBy             string           `json:"required_by,omitempty"`
	SourceEvent            string           `json:"source_event,omitempty"`
	EvidenceRef            string           `json:"evidence_ref,omitempty"`
	EvidenceEventID        string           `json:"evidence_event_id,omitempty"`
	EvidencePath           string           `json:"evidence_path,omitempty"`
	Command                string           `json:"command,omitempty"`
	Resolution             string           `json:"resolution,omitempty"`
	WaiverReason           string           `json:"waiver_reason,omitempty"`
	ApprovedBy             string           `json:"approved_by,omitempty"`
	FinalAnswerRequirement string           `json:"final_answer_requirement,omitempty"`
	FirstEventID           string           `json:"first_event_id,omitempty"`
	LastEventID            string           `json:"last_event_id,omitempty"`
	FirstSequence          int64            `json:"first_sequence,omitempty"`
	LastSequence           int64            `json:"last_sequence,omitempty"`
}

func NormalizeObligationLedger(events []Event) ObligationLedger {
	ledger := newObligationLedgerBuilder()

	ordered := append([]Event(nil), events...)
	slices.SortStableFunc(ordered, compareReplayEventOrder)

	for _, event := range ordered {
		event = copyEvent(event)
		event.Normalize()
		ledger.ApplyEvent(event)
	}

	return ledger.Report()
}

type obligationLedgerBuilder struct {
	entries map[obligationLedgerKey]NormalizedObligation
}

func newObligationLedgerBuilder() *obligationLedgerBuilder {
	return &obligationLedgerBuilder{entries: map[obligationLedgerKey]NormalizedObligation{}}
}

func (b *obligationLedgerBuilder) ApplyEvent(event Event) {
	if !isObligationEventType(event.Type) || event.Obligation == nil {
		return
	}

	obligation := *event.Obligation
	obligation.normalize()
	status := obligationStatusForEvent(event.Type, obligation.Status)
	if obligation.ObligationID == "" || obligation.Kind == "" || status == "" {
		return
	}

	entry := normalizedObligationFromEvent(event, obligation, status)
	b.upsert(entry)
}

func (b *obligationLedgerBuilder) Report() ObligationLedger {
	report := ObligationLedger{
		SchemaVersion: ObligationLedgerSchemaVersion,
		StatusCounts:  map[string]int{},
		KindCounts:    map[string]int{},
	}

	for _, identity := range sortedObligationLedgerIdentities(b.entries) {
		entry := b.entries[identity]
		report.Entries = append(report.Entries, entry)
		report.Total++
		incrementStringMap(&report.StatusCounts, string(entry.Status))
		incrementStringMap(&report.KindCounts, string(entry.Kind))

		switch entry.Status {
		case ObligationOpen:
			report.Open = append(report.Open, entry)
		case ObligationResolved:
			report.Resolved = append(report.Resolved, entry)
		case ObligationWaived:
			report.Waived = append(report.Waived, entry)
		case ObligationBlocked:
			report.Blocked = append(report.Blocked, entry)
		case ObligationStale:
			report.Stale = append(report.Stale, entry)
		case ObligationSuperseded:
			report.Superseded = append(report.Superseded, entry)
		}
	}

	if len(report.StatusCounts) == 0 {
		report.StatusCounts = nil
	}

	if len(report.KindCounts) == 0 {
		report.KindCounts = nil
	}

	return report
}

func (b *obligationLedgerBuilder) upsert(next NormalizedObligation) {
	key := obligationLedgerMapKey(next.ContractID, next.Kind, next.ObligationID)
	if key == (obligationLedgerKey{}) {
		return
	}

	current, ok := b.entries[key]
	if !ok {
		b.entries[key] = next
		return
	}

	next = carryForwardObligationFields(current, next)
	next.FirstEventID = firstNonEmptyEventValue(current.FirstEventID, next.FirstEventID)
	next.FirstSequence = firstNonZeroInt64(current.FirstSequence, next.FirstSequence)
	b.entries[key] = next
}

func carryForwardObligationFields(current NormalizedObligation, next NormalizedObligation) NormalizedObligation {
	next.Owner = firstNonEmptyObligationOwner(next.Owner, current.Owner)
	next.TargetRef = firstNonEmptyEventValue(next.TargetRef, current.TargetRef)
	next.RequiredBy = firstNonEmptyEventValue(next.RequiredBy, current.RequiredBy)
	next.SourceEvent = firstNonEmptyEventValue(next.SourceEvent, current.SourceEvent)
	next.EvidenceRef = firstNonEmptyEventValue(next.EvidenceRef, current.EvidenceRef)
	next.EvidenceEventID = firstNonEmptyEventValue(next.EvidenceEventID, current.EvidenceEventID)
	next.EvidencePath = firstNonEmptyEventValue(next.EvidencePath, current.EvidencePath)
	next.Command = firstNonEmptyEventValue(next.Command, current.Command)
	next.Resolution = firstNonEmptyEventValue(next.Resolution, current.Resolution)
	next.WaiverReason = firstNonEmptyEventValue(next.WaiverReason, current.WaiverReason)
	next.ApprovedBy = firstNonEmptyEventValue(next.ApprovedBy, current.ApprovedBy)
	next.FinalAnswerRequirement = firstNonEmptyEventValue(next.FinalAnswerRequirement, current.FinalAnswerRequirement)

	return next
}

func normalizedObligationFromEvent(event Event, obligation ContractObligationEvent, status ObligationStatus) NormalizedObligation {
	entry := NormalizedObligation{
		ContractID:             event.ContractID,
		ObligationID:           obligation.ObligationID,
		Kind:                   obligation.Kind,
		Status:                 status,
		Owner:                  obligation.Owner,
		TargetRef:              obligation.TargetRef,
		RequiredBy:             obligation.RequiredBy,
		SourceEvent:            obligation.SourceEvent,
		EvidenceEventID:        obligation.EvidenceEventID,
		EvidencePath:           obligation.EvidencePath,
		Command:                obligation.Command,
		Resolution:             obligation.Resolution,
		WaiverReason:           obligation.WaiverReason,
		ApprovedBy:             obligation.ApprovedBy,
		FinalAnswerRequirement: obligation.FinalAnswerRequirement,
		FirstEventID:           event.EventID,
		LastEventID:            event.EventID,
		FirstSequence:          event.Sequence,
		LastSequence:           event.Sequence,
	}
	entry.Identity = obligationLedgerIdentity(entry.ContractID, entry.Kind, entry.ObligationID)

	return entry
}

type obligationLedgerKey struct {
	contractID   string
	kind         ObligationKind
	obligationID string
}

func obligationLedgerMapKey(contractID string, kind ObligationKind, obligationID string) obligationLedgerKey {
	return obligationLedgerKey{
		contractID:   strings.TrimSpace(contractID),
		kind:         kind,
		obligationID: strings.TrimSpace(obligationID),
	}
}

func obligationLedgerIdentity(contractID string, kind ObligationKind, obligationID string) string {
	parts := []string{
		obligationLedgerIdentityPart(contractID),
		obligationLedgerIdentityPart(string(kind)),
		obligationLedgerIdentityPart(obligationID),
	}

	return strings.Join(parts, "/")
}

func obligationLedgerIdentityPart(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func obligationStatusForEvent(eventType EventType, payloadStatus ObligationStatus) ObligationStatus {
	switch eventType {
	case EventObligationOpened:
		return ObligationOpen
	case EventObligationResolved:
		return ObligationResolved
	case EventObligationWaived:
		return ObligationWaived
	case EventObligationBlocked:
		return ObligationBlocked
	case EventObligationStale:
		return ObligationStale
	case EventObligationSuperseded:
		return ObligationSuperseded
	default:
		return normalizeObligationStatus(payloadStatus)
	}
}

func normalizeObligationStatus(status ObligationStatus) ObligationStatus {
	status = ObligationStatus(strings.ToLower(strings.TrimSpace(string(status))))
	switch status {
	case ObligationOpen, ObligationResolved, ObligationWaived, ObligationBlocked, ObligationStale, ObligationSuperseded:
		return status
	default:
		return ""
	}
}

func isObligationEventType(eventType EventType) bool {
	return eventType == EventObligationOpened ||
		eventType == EventObligationResolved ||
		eventType == EventObligationWaived ||
		eventType == EventObligationBlocked ||
		eventType == EventObligationStale ||
		eventType == EventObligationSuperseded
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}

	return 0
}

func firstNonEmptyObligationOwner(values ...ObligationOwner) ObligationOwner {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func sortedObligationLedgerIdentities(entries map[obligationLedgerKey]NormalizedObligation) []obligationLedgerKey {
	identities := make([]obligationLedgerKey, 0, len(entries))
	for identity := range entries {
		identities = append(identities, identity)
	}

	slices.SortFunc(identities, func(a, b obligationLedgerKey) int {
		aEntry := entries[a]
		bEntry := entries[b]
		return strings.Compare(aEntry.Identity, bEntry.Identity)
	})

	return identities
}
