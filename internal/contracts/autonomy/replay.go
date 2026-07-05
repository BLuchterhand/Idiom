package autonomy

import (
	"cmp"
	"fmt"
	"reflect"
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
	SchemaVersion         string                     `json:"schema_version"`
	TotalEvents           int                        `json:"total_events"`
	AtomCounts            map[string]int             `json:"atom_counts,omitempty"`
	PackCounts            map[string]int             `json:"pack_counts,omitempty"`
	EnforcementModeCounts map[string]int             `json:"enforcement_mode_counts,omitempty"`
	ReplayClassification  ReplayClassificationReport `json:"replay_classification"`
	ReplayMetrics         ReplayMetrics              `json:"replay_metrics"`
	PrivacyChecks         ReplayPrivacyChecks        `json:"privacy_checks"`
	ObligationLedger      ObligationLedger           `json:"obligation_ledger"`
	Atoms                 []ReplayAtom               `json:"atoms,omitempty"`
	Violations            []ReplayViolation          `json:"violations,omitempty"`
}

type ReplayClassification string

const (
	ReplayClassificationFull          ReplayClassification = "full"
	ReplayClassificationRedacted      ReplayClassification = "redacted"
	ReplayClassificationPartial       ReplayClassification = "partial"
	ReplayClassificationNonReplayable ReplayClassification = "non_replayable"
)

type ReplayClassificationReport struct {
	Classification               ReplayClassification `json:"classification"`
	StructuredEvents             int                  `json:"structured_events"`
	EventsWithRequiredPayload    int                  `json:"events_with_required_payload"`
	MissingRequiredPayloadEvents int                  `json:"missing_required_payload_events"`
	DecisionEvents               int                  `json:"decision_events"`
	GateDecisionEvents           int                  `json:"gate_decision_events"`
	EventsWithHashesOrRefs       int                  `json:"events_with_hashes_or_refs"`
	ObligationEvents             int                  `json:"obligation_events"`
	VerificationEvents           int                  `json:"verification_events"`
	TracePolicyEvents            int                  `json:"trace_policy_events"`
	RedactedEvents               int                  `json:"redacted_events"`
	MissingSignalCounts          map[string]int       `json:"missing_signal_counts,omitempty"`
	Reasons                      []string             `json:"reasons,omitempty"`
}

type ReplayMetrics struct {
	RepeatedSameToolDecisions      int            `json:"repeated_same_tool_decisions"`
	RepeatedSameGateDecisions      int            `json:"repeated_same_gate_decisions"`
	RedirectStorms                 int            `json:"redirect_storms"`
	BroadRediscoveryAfterReadiness int            `json:"broad_rediscovery_after_readiness"`
	ZeroHitSearchStreaks           int            `json:"zero_hit_search_streaks"`
	MaxZeroHitSearchStreak         int            `json:"max_zero_hit_search_streak"`
	CandidatePathWaste             int            `json:"candidate_path_waste"`
	MissingRequiredPayloadCounts   map[string]int `json:"missing_required_payload_counts,omitempty"`
	PhaseGatePassCounts            map[string]int `json:"phase_gate_pass_counts,omitempty"`
	PhaseGateFailCounts            map[string]int `json:"phase_gate_fail_counts,omitempty"`
}

type ReplayPrivacyChecks struct {
	ForbiddenFieldCount   int                `json:"forbidden_field_count"`
	FieldCounts           map[string]int     `json:"field_counts,omitempty"`
	RedactionStatusCounts map[string]int     `json:"redaction_status_counts,omitempty"`
	Refs                  []ReplayPrivacyRef `json:"refs,omitempty"`
}

type ReplayPrivacyRef struct {
	EventID    string `json:"event_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Sequence   int64  `json:"sequence,omitempty"`
	ContractID string `json:"contract_id,omitempty"`
	Field      string `json:"field,omitempty"`
	Reason     string `json:"reason,omitempty"`
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

	state.report.ReplayClassification, state.report.ReplayMetrics, state.report.PrivacyChecks = replayIntentForEvents(ordered)
	state.report.ObligationLedger = NormalizeObligationLedger(ordered)
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
	case "pass", "passed", "success", "succeeded", "ok", "clean", "verified", "resolved", "observed", "not_required":
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

	if looksLikeRawGuardFrictionContent(string(atom.Gate)) {
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

	if looksLikeRawGuardFrictionContent(value) {
		return replayRedactedValue
	}

	return value
}

func (s *replayState) finish() {
	finishReplayIntentReport(&s.report.ReplayClassification, &s.report.ReplayMetrics, &s.report.PrivacyChecks)

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

func replayIntentForEvents(events []Event) (ReplayClassificationReport, ReplayMetrics, ReplayPrivacyChecks) {
	state := replayIntentState{
		classification: ReplayClassificationReport{
			MissingSignalCounts: map[string]int{},
		},
		metrics: ReplayMetrics{
			MissingRequiredPayloadCounts: map[string]int{},
			PhaseGatePassCounts:          map[string]int{},
			PhaseGateFailCounts:          map[string]int{},
		},
		privacy: ReplayPrivacyChecks{
			FieldCounts:           map[string]int{},
			RedactionStatusCounts: map[string]int{},
		},
		toolDecisionCounts: map[string]int{},
		gateDecisionCounts: map[string]int{},
	}

	ordered := append([]Event(nil), events...)
	slices.SortStableFunc(ordered, compareReplayEventOrder)

	for _, event := range ordered {
		event = copyEvent(event)
		event.Normalize()
		state.addEvent(event)
	}

	state.finish()

	return state.classification, state.metrics, state.privacy
}

type replayIntentState struct {
	classification     ReplayClassificationReport
	metrics            ReplayMetrics
	privacy            ReplayPrivacyChecks
	readySeen          bool
	zeroHitStreak      int
	redirectStreak     int
	toolDecisionCounts map[string]int
	gateDecisionCounts map[string]int
}

func (s *replayIntentState) addEvent(event Event) {
	s.classification.StructuredEvents++

	if payloadName, ok := requiredReplayPayloadName(event.Type); ok {
		if eventHasReplayRequiredPayload(event, payloadName) {
			s.classification.EventsWithRequiredPayload++
		} else {
			s.classification.MissingRequiredPayloadEvents++

			s.metrics.MissingRequiredPayloadCounts[payloadName]++
		}
	}

	if eventHasReplayHashesOrRefs(event) {
		s.classification.EventsWithHashesOrRefs++
	}

	if eventHasReplayVerification(event) {
		s.classification.VerificationEvents++
	}

	if eventHasReplayTracePolicy(event) {
		s.classification.TracePolicyEvents++
	}

	if eventHasReplayRedactionState(event) {
		s.classification.RedactedEvents++
	}

	if event.ModelExchange != nil && event.ModelExchange.TraceStorePolicy != nil {
		incrementStringMap(&s.privacy.RedactionStatusCounts, event.ModelExchange.TraceStorePolicy.RedactionStatus)
	}

	s.addPrivacyRefs(replayPrivacyRefsForEvent(event))

	switch event.Type {
	case EventToolPolicyDecision, EventToolPolicyResult, EventToolIntentClassified, EventPathRolesResolved:
		if event.Policy != nil {
			s.addPolicyMetrics(event, *event.Policy)
		}
	case EventObligationOpened, EventObligationResolved, EventObligationWaived, EventObligationBlocked, EventObligationStale, EventObligationSuperseded:
		s.classification.ObligationEvents++
	case EventReadinessStateUpdated:
		if event.Readiness != nil && (event.Readiness.ReadyToAnswer || event.Readiness.ReadyToWrite || event.Readiness.ReadyToVerify) {
			s.readySeen = true
		}
	case EventCompletionGateDecision:
		if event.Completion != nil {
			s.classification.DecisionEvents++
			if eventHasReplayVerification(event) {
				s.classification.VerificationEvents++
			}
		}
	case EventGateDecision:
		if event.Gate != nil {
			s.addGateMetrics(event, *event.Gate)
		}
	case EventGuardFriction:
		if event.GuardFriction != nil && guardFrictionRedirectLike(*event.GuardFriction) {
			s.redirectStreak++
			if s.redirectStreak == 3 {
				s.metrics.RedirectStorms++
			}
		} else {
			s.redirectStreak = 0
		}
	}
}

func (s *replayIntentState) addPolicyMetrics(event Event, policy ContractPolicyEvent) {
	if event.Type == EventToolPolicyDecision || event.Type == EventToolPolicyResult {
		s.classification.DecisionEvents++
	}

	key := replayPolicyDecisionMetricKey(policy)
	if key != "" {
		s.toolDecisionCounts[key]++
		if s.toolDecisionCounts[key] > 1 {
			s.metrics.RepeatedSameToolDecisions++
		}
	}

	if s.readySeen && replayBroadRediscoveryTool(policy.ToolName, policy.Capability) {
		s.metrics.BroadRediscoveryAfterReadiness++
	}

	if replayZeroHitSearchPolicy(policy) {
		s.zeroHitStreak++
		if s.zeroHitStreak == 2 {
			s.metrics.ZeroHitSearchStreaks++
		}

		if s.zeroHitStreak > s.metrics.MaxZeroHitSearchStreak {
			s.metrics.MaxZeroHitSearchStreak = s.zeroHitStreak
		}
	} else if replaySearchTool(policy.ToolName, policy.Capability) {
		s.zeroHitStreak = 0
	}

	if replayCandidatePathWaste(policy) {
		s.metrics.CandidatePathWaste++
	}
}

func (s *replayIntentState) addGateMetrics(event Event, gate ContractGateEvent) {
	s.classification.DecisionEvents++

	s.classification.GateDecisionEvents++

	if len(gate.ObligationRefs)+len(gate.OpenObligations)+len(gate.ResolvedObligations) > 0 {
		s.classification.ObligationEvents++
	}

	key := replayGateDecisionKey(gate)
	if key != "" {
		s.gateDecisionCounts[key]++
		if s.gateDecisionCounts[key] > 1 {
			s.metrics.RepeatedSameGateDecisions++
		}
	}

	phaseKey := replayPhaseGateKey(event, gate)
	if replayGatePasses(gate) {
		incrementStringMap(&s.metrics.PhaseGatePassCounts, phaseKey)
	} else {
		incrementStringMap(&s.metrics.PhaseGateFailCounts, phaseKey)
	}

	if isBlockingGateDecision(gate.Decision) || gate.Decision == PolicyNudge {
		s.redirectStreak++
		if s.redirectStreak == 3 {
			s.metrics.RedirectStorms++
		}
	} else {
		s.redirectStreak = 0
	}
}

func (s *replayIntentState) addPrivacyRefs(refs []ReplayPrivacyRef) {
	for _, ref := range refs {
		s.privacy.Refs = append(s.privacy.Refs, ref)
		s.privacy.ForbiddenFieldCount++
		incrementStringMap(&s.privacy.FieldCounts, ref.Field)
	}
}

func (s *replayIntentState) finish() {
	if s.classification.StructuredEvents == 0 {
		s.classification.Classification = ReplayClassificationNonReplayable
		s.classification.Reasons = append(s.classification.Reasons, "no_events")
		finishReplayIntentReport(&s.classification, &s.metrics, &s.privacy)

		return
	}

	for _, signal := range replayRequiredClassificationSignals(&s.classification) {
		s.classification.MissingSignalCounts[signal]++
	}

	switch {
	case s.classification.EventsWithRequiredPayload == 0 || s.classification.DecisionEvents == 0:
		s.classification.Classification = ReplayClassificationNonReplayable
	case s.privacy.ForbiddenFieldCount > 0:
		s.classification.Classification = ReplayClassificationNonReplayable
	case s.classification.MissingRequiredPayloadEvents > 0 || len(s.classification.MissingSignalCounts) > 0:
		s.classification.Classification = ReplayClassificationPartial
	case s.classification.RedactedEvents > 0:
		s.classification.Classification = ReplayClassificationRedacted
	default:
		s.classification.Classification = ReplayClassificationFull
	}

	if s.classification.MissingRequiredPayloadEvents > 0 {
		s.classification.Reasons = append(s.classification.Reasons, "missing_required_payloads")
	}

	if s.privacy.ForbiddenFieldCount > 0 {
		s.classification.Reasons = append(s.classification.Reasons, "privacy_forbidden_raw_content")
	}

	for _, key := range sortedMapKeys(s.classification.MissingSignalCounts) {
		s.classification.Reasons = append(s.classification.Reasons, "missing_"+key)
	}

	finishReplayIntentReport(&s.classification, &s.metrics, &s.privacy)
}

func finishReplayIntentReport(classification *ReplayClassificationReport, metrics *ReplayMetrics, privacy *ReplayPrivacyChecks) {
	if classification != nil {
		if len(classification.MissingSignalCounts) == 0 {
			classification.MissingSignalCounts = nil
		}

		if len(classification.Reasons) == 0 {
			classification.Reasons = nil
		}
	}

	if metrics != nil {
		if len(metrics.MissingRequiredPayloadCounts) == 0 {
			metrics.MissingRequiredPayloadCounts = nil
		}

		if len(metrics.PhaseGatePassCounts) == 0 {
			metrics.PhaseGatePassCounts = nil
		}

		if len(metrics.PhaseGateFailCounts) == 0 {
			metrics.PhaseGateFailCounts = nil
		}
	}

	if privacy != nil {
		if len(privacy.FieldCounts) == 0 {
			privacy.FieldCounts = nil
		}

		if len(privacy.RedactionStatusCounts) == 0 {
			privacy.RedactionStatusCounts = nil
		}

		if len(privacy.Refs) == 0 {
			privacy.Refs = nil
		}
	}
}

func replayRequiredClassificationSignals(report *ReplayClassificationReport) []string {
	var missing []string

	if report.GateDecisionEvents == 0 {
		missing = append(missing, "gate_decisions")
	}

	if report.EventsWithHashesOrRefs == 0 {
		missing = append(missing, "hashes_or_refs")
	}

	if report.ObligationEvents == 0 {
		missing = append(missing, "obligations")
	}

	if report.VerificationEvents == 0 {
		missing = append(missing, "verification_status")
	}

	if report.TracePolicyEvents == 0 {
		missing = append(missing, "trace_policy")
	}

	return missing
}

func requiredReplayPayloadName(eventType EventType) (string, bool) {
	switch eventType {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped:
		return "lifecycle", true
	case EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult:
		return "policy", true
	case EventObligationOpened, EventObligationResolved, EventObligationWaived, EventObligationBlocked, EventObligationStale, EventObligationSuperseded:
		return "obligation", true
	case EventReadinessStateUpdated:
		return "readiness", true
	case EventCompletionGateDecision:
		return "completion", true
	case EventGateDecision:
		return "gate", true
	case EventGuardFriction:
		return "guard_friction", true
	case EventModelExchange:
		return "model_exchange", true
	default:
		return "", false
	}
}

func eventHasReplayRequiredPayload(event Event, payloadName string) bool {
	switch payloadName {
	case "lifecycle":
		return event.Lifecycle != nil
	case "policy":
		return event.Policy != nil
	case "obligation":
		return event.Obligation != nil
	case "readiness":
		return event.Readiness != nil
	case "completion":
		return event.Completion != nil
	case "gate":
		return event.Gate != nil
	case "guard_friction":
		return event.GuardFriction != nil
	case "model_exchange":
		return event.ModelExchange != nil
	default:
		return false
	}
}

func eventHasReplayHashesOrRefs(event Event) bool {
	if len(event.EvidenceRefs) > 0 || event.ParentID != "" || event.Correlation.SidecarRequestID != "" || event.Correlation.ToolCallID != "" {
		return true
	}

	if event.Lifecycle != nil {
		return event.Lifecycle.RailsHash != "" || event.Lifecycle.ProtocolHash != "" ||
			len(event.Lifecycle.RequiredEvidence) > 0 || len(event.Lifecycle.RemainingObligations) > 0 ||
			(event.Lifecycle.ModelProtocol != nil && event.Lifecycle.ModelProtocol.ProtocolHash != "") ||
			(event.Lifecycle.TurnPhaseContract != nil && event.Lifecycle.TurnPhaseContract.ProtocolHash != "")
	}

	if event.Policy != nil {
		return event.Policy.ToolCallID != "" || event.Policy.ArgumentsDigest != "" || len(event.Policy.PathRoles) > 0 ||
			len(event.Policy.EvidencePaths) > 0 || len(event.Policy.ObligationsOpened) > 0 || len(event.Policy.ObligationsResolved) > 0
	}

	if event.Obligation != nil {
		return event.Obligation.SourceEvent != "" || event.Obligation.TargetRef != "" || event.Obligation.EvidenceEventID != "" ||
			event.Obligation.EvidencePath != ""
	}

	if event.Readiness != nil {
		return len(event.Readiness.EvidenceRefs) > 0 || len(event.Readiness.OpenObligations) > 0
	}

	if event.Completion != nil {
		return event.Completion.ProtocolHash != "" || len(event.Completion.RemainingObligations) > 0
	}

	if event.Gate != nil {
		return event.Gate.ProtocolHash != "" || event.Gate.ToolCallID != "" || event.Gate.LoopFingerprintDigest != "" ||
			len(event.Gate.TargetRefs) > 0 || len(event.Gate.ObligationRefs) > 0 || len(event.Gate.OpenObligations) > 0 ||
			len(event.Gate.ResolvedObligations) > 0
	}

	if event.GuardFriction != nil {
		return event.GuardFriction.ProtocolHash != "" || event.GuardFriction.SourceEventID != "" || event.GuardFriction.ObligationID != "" ||
			len(event.GuardFriction.EvidenceRefs) > 0
	}

	if event.ModelExchange != nil {
		return event.ModelExchange.ProtocolHash != "" || event.ModelExchange.SourceEventID != "" ||
			event.ModelExchange.InvariantID != "" || event.ModelExchange.ContextPacket != nil ||
			event.ModelExchange.TraceStorePolicy != nil
	}

	return false
}

func eventHasReplayVerification(event Event) bool {
	if event.Completion != nil && event.Completion.VerificationStatus != "" {
		return true
	}

	if event.Gate != nil && event.Gate.VerificationStatus != "" {
		return true
	}

	if event.Readiness != nil && event.Readiness.ReadyToVerify {
		return true
	}

	if event.Obligation != nil && event.Obligation.Kind == ObligationVerify {
		return true
	}

	if event.ModelExchange != nil && event.ModelExchange.ValidationSignal != nil {
		return true
	}

	return false
}

func eventHasReplayTracePolicy(event Event) bool {
	return event.ModelExchange != nil && event.ModelExchange.TraceStorePolicy != nil
}

func eventHasReplayRedactionState(event Event) bool {
	return replayEventContainsString(event, func(value string) bool {
		return strings.Contains(strings.ToLower(strings.TrimSpace(value)), "redacted")
	})
}

func replayEventContainsString(event Event, predicate func(string) bool) bool {
	var walk func(reflect.Value) bool

	walk = func(value reflect.Value) bool {
		if !value.IsValid() {
			return false
		}

		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return false
			}

			return walk(value.Elem())
		}

		switch value.Kind() {
		case reflect.String:
			return predicate(value.String())
		case reflect.Struct:
			if value.Type().PkgPath() == "time" {
				return false
			}

			for i := 0; i < value.NumField(); i++ {
				if walk(value.Field(i)) {
					return true
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				if walk(value.Index(i)) {
					return true
				}
			}
		}

		return false
	}

	return walk(reflect.ValueOf(event))
}

func replayPrivacyRefsForEvent(event Event) []ReplayPrivacyRef {
	var (
		refs []ReplayPrivacyRef
		walk func(reflect.Value, string)
	)

	walk = func(value reflect.Value, path string) {
		if !value.IsValid() {
			return
		}

		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return
			}

			walk(value.Elem(), path)

			return
		}

		switch value.Kind() {
		case reflect.String:
			if reason := replayPrivacyStringReason(path, value.String()); reason != "" {
				refs = append(refs, replayPrivacyRef(event, path, reason))
			}
		case reflect.Struct:
			if value.Type().PkgPath() == "time" {
				return
			}

			for i := 0; i < value.NumField(); i++ {
				field := value.Type().Field(i)
				if field.PkgPath != "" {
					continue
				}

				name := replayJSONFieldName(field)
				if name == "-" {
					continue
				}

				fieldPath := name
				if path != "" {
					fieldPath = path + "." + name
				}

				if reason := replayPrivacyFieldNameReason(name); reason != "" {
					refs = append(refs, replayPrivacyRef(event, fieldPath, reason))
				}

				walk(value.Field(i), fieldPath)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				walk(value.Index(i), path)
			}
		}
	}
	walk(reflect.ValueOf(event), "")

	return refs
}

func replayPrivacyRef(event Event, field, reason string) ReplayPrivacyRef {
	return ReplayPrivacyRef{
		EventID:    event.EventID,
		Type:       string(event.Type),
		Sequence:   event.Sequence,
		ContractID: event.ContractID,
		Field:      strings.TrimSpace(field),
		Reason:     reason,
	}
}

func replayJSONFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}

	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return field.Name
	}

	return name
}

func replayPrivacyFieldNameReason(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "raw_prompt", "raw_transcript", "prompt_body", "transcript_body", "file_body", "old_text", "new_text", "long_excerpt", "customer_id", "customer_identifier":
		return "forbidden raw-content field"
	default:
		return ""
	}
}

func replayPrivacyStringReason(path, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if strings.Contains(strings.ToLower(strings.TrimSpace(path)), "forbidden_fields") {
		return ""
	}

	lower := strings.ToLower(value)
	if len(value) > 500 {
		return "long excerpt-like value"
	}

	if looksLikeRawGuardFrictionContent(value) {
		return "raw content marker"
	}

	for _, marker := range []string{"token=", "bearer ", "customer-", "customer_", "customer ", "secrets/", "/secrets/", "\\secrets\\"} {
		if strings.Contains(lower, marker) {
			return "secret or customer identifier marker"
		}
	}

	return ""
}

func replayPolicyDecisionMetricKey(policy ContractPolicyEvent) string {
	if policy.ToolName == "" || policy.Decision == "" {
		return ""
	}

	return strings.Join([]string{policy.ToolName, string(policy.Decision), policy.ReasonCode}, "\x00")
}

func replayBroadRediscoveryTool(tool string, capability Capability) bool {
	tool = strings.TrimSpace(tool)
	switch tool {
	case "context_pack", "discovery_query", "search_files", "list_files":
		return true
	case "search_content":
		return capability == CapabilityLiteralSearch || capability == CapabilityUnboundedRead || capability == ""
	default:
		return capability == CapabilitySemanticDiscovery || capability == CapabilityLiteralSearch || capability == CapabilityUnboundedRead
	}
}

func replaySearchTool(tool string, capability Capability) bool {
	switch strings.TrimSpace(tool) {
	case "search_content", "search_files", "context_query", "context_pack", "discovery_query", "rg", "grep":
		return true
	default:
		return capability == CapabilityLiteralSearch || capability == CapabilitySemanticDiscovery
	}
}

func replayZeroHitSearchPolicy(policy ContractPolicyEvent) bool {
	if !replaySearchTool(policy.ToolName, policy.Capability) {
		return false
	}

	text := strings.ToLower(strings.Join([]string{policy.Result, policy.ErrorClass, policy.ArgumentsSummary, policy.ReasonCode}, " "))
	for _, marker := range []string{"zero hits", "0 hits", "0 results", "no results", "no matches", "empty result"} {
		if strings.Contains(text, marker) {
			return true
		}
	}

	return false
}

func replayCandidatePathWaste(policy ContractPolicyEvent) bool {
	text := strings.ToLower(strings.Join([]string{policy.Result, policy.ErrorClass, policy.ArgumentsSummary, policy.ReasonCode}, " "))
	if strings.Contains(text, "candidate_path_waste") || strings.Contains(text, "candidate path waste") {
		return true
	}

	if len(policy.PathRoles) == 0 {
		return false
	}

	for _, match := range policy.PathRoles {
		switch match.Role {
		case PathRoleRequiredEdit, PathRoleAllowedEdit, PathRoleEvidenceReadonly, PathRoleGeneratedArtifact, PathRoleVerificationOut:
			return false
		}
	}

	return true
}

func replayPhaseGateKey(event Event, gate ContractGateEvent) string {
	phase := firstNonEmptyReplayValue(event.Correlation.Phase, event.Correlation.PhaseRunID, event.Correlation.Lane, "unknown")
	return phase + "/" + string(gate.Gate)
}

func replayGatePasses(gate ContractGateEvent) bool {
	return gate.Decision == PolicyAllow || gate.Decision == PolicyObserve
}

func guardFrictionRedirectLike(friction ContractGuardFrictionEvent) bool {
	return friction.Decision == GuardFrictionDecisionNudge || friction.Decision == GuardFrictionDecisionReject ||
		friction.Decision == GuardFrictionDecisionRecover || friction.RetryOutcome == GuardFrictionRetrySameFailure
}

func firstNonEmptyReplayValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
