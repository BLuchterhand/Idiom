package autonomy

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

const ReplayReportSchemaVersion = "idiom.autonomy.replay.v1"

const DefaultReplayMaxRepeatedGateDecisions = 3
const replayRedactedValue = "[redacted]"

type ReplayAtomKind string

const (
	ReplayAtomToolCalled         ReplayAtomKind = "tool_called"
	ReplayAtomPathWritten        ReplayAtomKind = "path_written"
	ReplayAtomChangedPath        ReplayAtomKind = "changed_path"
	ReplayAtomVerificationStatus ReplayAtomKind = "verification_status"
	ReplayAtomGateDecision       ReplayAtomKind = "gate_decision"
	ReplayAtomPackSeen           ReplayAtomKind = "pack_seen"
	ReplayAtomBudgetExhausted    ReplayAtomKind = "budget_exhausted"
	ReplayAtomObservedFact       ReplayAtomKind = "observed_fact"
)

type ReplayOptions struct {
	MaxRepeatedGateDecisions int
}

type ReplayReport struct {
	SchemaVersion         string            `json:"schema_version"`
	TotalEvents           int               `json:"total_events"`
	AtomCounts            map[string]int    `json:"atom_counts,omitempty"`
	PackCounts            map[string]int    `json:"pack_counts,omitempty"`
	EnforcementModeCounts map[string]int    `json:"enforcement_mode_counts,omitempty"`
	Atoms                 []ReplayAtom      `json:"atoms,omitempty"`
	Violations            []ReplayViolation `json:"violations,omitempty"`
}

type ReplayAtom struct {
	Kind       ReplayAtomKind `json:"kind"`
	EventID    string         `json:"event_id,omitempty"`
	Sequence   int64          `json:"sequence,omitempty"`
	ContractID string         `json:"contract_id,omitempty"`
	Gate       GateKind       `json:"gate,omitempty"`
	Decision   string         `json:"decision,omitempty"`
	Name       string         `json:"name,omitempty"`
	Value      string         `json:"value,omitempty"`
}

type ReplayViolation struct {
	ID         string `json:"id"`
	EventID    string `json:"event_id,omitempty"`
	Sequence   int64  `json:"sequence,omitempty"`
	ContractID string `json:"contract_id,omitempty"`
	Gate       string `json:"gate,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Reference  string `json:"reference,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
}

type replayState struct {
	report                ReplayReport
	maxRepeatedGateEvents int
	repeatedGateDecisions map[string]int
	lastWriteSequence     int64
	lastVerificationPass  int64
}

func ReplayEvents(events []Event, opts ReplayOptions) ReplayReport {
	state := replayState{
		report: ReplayReport{
			SchemaVersion:         ReplayReportSchemaVersion,
			AtomCounts:            map[string]int{},
			PackCounts:            map[string]int{},
			EnforcementModeCounts: map[string]int{},
		},
		maxRepeatedGateEvents: replayMaxRepeatedGateDecisions(opts),
		repeatedGateDecisions: map[string]int{},
	}

	ordered := append([]Event(nil), events...)
	slices.SortStableFunc(ordered, compareReplayEventOrder)
	state.report.TotalEvents = len(ordered)
	for _, event := range ordered {
		event = copyEvent(event)
		event.Normalize()
		state.replayEvent(event)
	}
	state.finish()
	return state.report
}

func replayMaxRepeatedGateDecisions(opts ReplayOptions) int {
	if opts.MaxRepeatedGateDecisions > 0 {
		return opts.MaxRepeatedGateDecisions
	}
	return DefaultReplayMaxRepeatedGateDecisions
}

func compareReplayEventOrder(a, b Event) int {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		if a.OccurredAt.IsZero() {
			return 1
		}
		if b.OccurredAt.IsZero() {
			return -1
		}
		if a.OccurredAt.Before(b.OccurredAt) {
			return -1
		}
		return 1
	}
	return cmp.Or(
		cmp.Compare(a.ContractID, b.ContractID),
		cmp.Compare(a.Sequence, b.Sequence),
		cmp.Compare(string(a.Type), string(b.Type)),
		cmp.Compare(a.EventID, b.EventID),
	)
}

func (s *replayState) replayEvent(event Event) {
	switch event.Type {
	case EventToolPolicyDecision, EventToolPolicyResult:
		if event.Policy != nil {
			s.replayPolicyEvent(event, *event.Policy)
		}
	case EventCompletionGateDecision:
		if event.Completion != nil {
			s.replayCompletionEvent(event, *event.Completion)
		}
	case EventGateDecision:
		if event.Gate != nil {
			s.replayGateEvent(event, *event.Gate)
		}
	}
}

func (s *replayState) replayPolicyEvent(event Event, policy ContractPolicyEvent) {
	if policy.ToolName != "" {
		s.addAtom(event, ReplayAtom{
			Kind: ReplayAtomToolCalled,
			Name: policy.ToolName,
		})
	}
	for _, path := range policy.ChangedPaths {
		s.addChangedPathAtoms(event, GateKind(""), policy.Decision, path, policyWritesPath(policy))
	}
}

func policyWritesPath(policy ContractPolicyEvent) bool {
	if policy.Decision != PolicyAllow && policy.Decision != PolicyObserve {
		return false
	}
	switch policy.Capability {
	case CapabilitySourceEdit, CapabilityApplyPatch, CapabilityMutatingShell, CapabilityExternalWrite:
		return true
	}
	return policy.SideEffect == SideEffectAdditive || policy.SideEffect == SideEffectDestructive
}

func (s *replayState) replayCompletionEvent(event Event, completion ContractCompletionEvent) {
	if completion.VerificationStatus != "" {
		s.addAtom(event, ReplayAtom{
			Kind:  ReplayAtomVerificationStatus,
			Gate:  GateCompletion,
			Value: completion.VerificationStatus,
		})
		if verificationStatusIsResolved(completion.VerificationStatus) {
			s.lastVerificationPass = event.Sequence
		}
	}
	for _, path := range completion.ChangedPaths {
		s.addChangedPathAtoms(event, GateCompletion, completion.Decision, path, false)
	}
	if completion.Decision == PolicyAllow && s.hasUnresolvedPostWriteVerification(completion.VerificationStatus, completion.RemainingObligations) {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_completion_unresolved_post_write_verification",
			Gate:      string(GateCompletion),
			Decision:  string(completion.Decision),
			Reference: "verification_status",
			Evidence:  "completion allowed after observed write changes without resolved post-write verification",
		})
	}
}

func (s *replayState) replayGateEvent(event Event, gate ContractGateEvent) {
	s.addAtom(event, ReplayAtom{
		Kind:     ReplayAtomGateDecision,
		Gate:     gate.Gate,
		Decision: string(gate.Decision),
		Value:    gate.ReasonCode,
	})
	if gate.ToolName != "" {
		s.addAtom(event, ReplayAtom{
			Kind: ReplayAtomToolCalled,
			Gate: gate.Gate,
			Name: gate.ToolName,
		})
	}
	if gate.PackID != "" {
		packID := replaySafeValue(string(gate.PackID))
		s.report.PackCounts[packID]++
		s.addAtom(event, ReplayAtom{
			Kind:  ReplayAtomPackSeen,
			Gate:  gate.Gate,
			Value: packID,
		})
	} else {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_gate_missing_pack_id",
			Gate:      string(gate.Gate),
			Decision:  string(gate.Decision),
			Reference: "pack_id",
			Evidence:  "gate decision did not identify the contract pack",
		})
	}
	if gate.EnforcementMode != "" {
		s.report.EnforcementModeCounts[replaySafeValue(string(gate.EnforcementMode))]++
	} else {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_gate_missing_enforcement_mode",
			Gate:      string(gate.Gate),
			Decision:  string(gate.Decision),
			Reference: "enforcement_mode",
			Evidence:  "gate decision did not identify observe/enforce mode",
		})
	}
	if len(gate.ObservedFacts) == 0 {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_gate_missing_observed_facts",
			Gate:      string(gate.Gate),
			Decision:  string(gate.Decision),
			Reference: "observed_facts",
			Evidence:  "gate decision had no bounded observed facts",
		})
	}
	for _, fact := range gate.ObservedFacts {
		s.addAtom(event, ReplayAtom{
			Kind:  ReplayAtomObservedFact,
			Gate:  gate.Gate,
			Value: string(fact),
		})
	}
	if gate.EnforcementMode == EnforcementModeEnforce && isBlockingGateDecision(gate.Decision) && len(gate.SafeAlternatives) == 0 {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_gate_missing_safe_alternatives",
			Gate:      string(gate.Gate),
			Decision:  string(gate.Decision),
			Reference: "safe_alternatives",
			Evidence:  "enforced blocking gate did not expose safe alternatives",
		})
	}
	s.checkRepeatedGateDecision(event, gate)
	for _, path := range gate.ChangedPaths {
		s.addChangedPathAtoms(event, gate.Gate, gate.Decision, path, gateWritesPath(gate))
	}
	if gate.VerificationStatus != "" {
		s.addAtom(event, ReplayAtom{
			Kind:  ReplayAtomVerificationStatus,
			Gate:  gate.Gate,
			Value: gate.VerificationStatus,
		})
		if gate.Gate == GateVerification && verificationStatusIsResolved(gate.VerificationStatus) {
			s.lastVerificationPass = event.Sequence
		}
	}
	if gate.Budget != nil && gate.Budget.Exhausted {
		s.addAtom(event, ReplayAtom{
			Kind:  ReplayAtomBudgetExhausted,
			Gate:  gate.Gate,
			Value: gate.Budget.ExhaustionReason,
		})
	}
	if gate.Gate == GateCompletion && gate.Decision == PolicyAllow && s.hasUnresolvedPostWriteVerification(gate.VerificationStatus, gate.OpenObligations) {
		s.addViolation(event, ReplayViolation{
			ID:        "replay_completion_unresolved_post_write_verification",
			Gate:      string(gate.Gate),
			Decision:  string(gate.Decision),
			Reference: "verification_status",
			Evidence:  "completion allowed after observed write changes without resolved post-write verification",
		})
	}
}

func (s *replayState) addChangedPathAtoms(event Event, gate GateKind, decision ToolPolicyDecision, path string, written bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	s.addAtom(event, ReplayAtom{
		Kind:     ReplayAtomChangedPath,
		Gate:     gate,
		Decision: string(decision),
		Value:    path,
	})
	if !written {
		return
	}
	s.addAtom(event, ReplayAtom{
		Kind:     ReplayAtomPathWritten,
		Gate:     gate,
		Decision: string(decision),
		Value:    path,
	})
	s.lastWriteSequence = event.Sequence
}

func gateWritesPath(gate ContractGateEvent) bool {
	return gate.Gate == GateWrite && (gate.Decision == PolicyAllow || gate.Decision == PolicyObserve)
}

func isBlockingGateDecision(decision ToolPolicyDecision) bool {
	return decision == PolicyReject || decision == PolicyDeny || decision == PolicyStop
}

func (s *replayState) checkRepeatedGateDecision(event Event, gate ContractGateEvent) {
	key := replayGateDecisionKey(gate)
	s.repeatedGateDecisions[key]++
	if s.repeatedGateDecisions[key] <= s.maxRepeatedGateEvents {
		return
	}
	s.addViolation(event, ReplayViolation{
		ID:        "replay_gate_repeated_decision",
		Gate:      string(gate.Gate),
		Decision:  string(gate.Decision),
		Reference: "gate_reason_decision",
		Evidence:  fmt.Sprintf("same gate/reason/decision repeated %d times; max is %d", s.repeatedGateDecisions[key], s.maxRepeatedGateEvents),
	})
}

func replayGateDecisionKey(gate ContractGateEvent) string {
	return strings.Join([]string{
		string(gate.Gate),
		gate.ReasonCode,
		string(gate.Decision),
	}, "\x00")
}

func (s *replayState) hasUnresolvedPostWriteVerification(status string, obligations []string) bool {
	if s.lastWriteSequence == 0 || s.lastVerificationPass > s.lastWriteSequence {
		return false
	}
	if verificationStatusIsResolved(status) {
		return false
	}
	if verificationStatusIsUnresolved(status) {
		return true
	}
	for _, obligation := range obligations {
		if strings.Contains(strings.ToLower(obligation), "verify") {
			return true
		}
	}
	return true
}

func verificationStatusIsResolved(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pass", "passed", "success", "succeeded", "ok", "clean", "verified", "resolved":
		return true
	default:
		return false
	}
}

func verificationStatusIsUnresolved(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "required", "missing", "unresolved", "open", "failed", "fail", "error", "skipped":
		return true
	default:
		return false
	}
}

func (s *replayState) addAtom(event Event, atom ReplayAtom) {
	atom.EventID = event.EventID
	atom.Sequence = event.Sequence
	atom.ContractID = event.ContractID
	if looksLikeRawContractEventContent(string(atom.Gate)) {
		atom.Gate = GateKind(replayRedactedValue)
	}
	atom.Decision = replaySafeValue(atom.Decision)
	atom.Name = replaySafeValue(atom.Name)
	atom.Value = replaySafeValue(atom.Value)
	s.report.Atoms = append(s.report.Atoms, atom)
	s.report.AtomCounts[string(atom.Kind)]++
}

func (s *replayState) addViolation(event Event, violation ReplayViolation) {
	violation.EventID = event.EventID
	violation.Sequence = event.Sequence
	violation.ContractID = event.ContractID
	violation.Gate = replaySafeValue(violation.Gate)
	violation.Decision = replaySafeValue(violation.Decision)
	violation.Reference = replaySafeValue(violation.Reference)
	violation.Evidence = replaySafeValue(violation.Evidence)
	s.report.Violations = append(s.report.Violations, violation)
}

func replaySafeValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if looksLikeRawContractEventContent(value) {
		return replayRedactedValue
	}
	return value
}

func (s *replayState) finish() {
	if len(s.report.AtomCounts) == 0 {
		s.report.AtomCounts = nil
	}
	if len(s.report.PackCounts) == 0 {
		s.report.PackCounts = nil
	}
	if len(s.report.EnforcementModeCounts) == 0 {
		s.report.EnforcementModeCounts = nil
	}
	if len(s.report.Atoms) == 0 {
		s.report.Atoms = nil
	}
	if len(s.report.Violations) == 0 {
		s.report.Violations = nil
	}
}
