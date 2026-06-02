package autonomy

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"idiom/internal/util"
)

const AuditReportSchemaVersion = "idiom.autonomy.audit.v1"

type EventAuditOptions struct {
	ExpectedContractIDs []string
}

type EventAuditReport struct {
	SchemaVersion          string                         `json:"schema_version"`
	TotalEvents            int                            `json:"total_events"`
	AllClear               bool                           `json:"all_clear"`
	Contracts              []ContractEventCoverage        `json:"contracts,omitempty"`
	LifecycleCoverage      LifecycleEventCoverage         `json:"lifecycle_coverage"`
	PolicyCoverage         PolicyEventCoverage            `json:"policy_coverage"`
	ObligationCoverage     ObligationEventCoverage        `json:"obligation_coverage"`
	GateCoverage           GateEventCoverage              `json:"gate_coverage"`
	GuardFrictionCoverage  GuardFrictionCoverage          `json:"guard_friction_coverage"`
	TurnPhaseCoverage      TurnPhaseEventCoverage         `json:"turn_phase_coverage"`
	ReferenceCoverage      ContractReferenceCoverage      `json:"reference_coverage"`
	SidecarToolCorrelation SidecarToolCorrelationCoverage `json:"sidecar_tool_correlation"`
	MetadataCoverage       ContractMetadataCoverage       `json:"metadata_coverage"`
	Findings               []EventAuditFinding            `json:"findings,omitempty"`
}

type ContractEventCoverage struct {
	ContractID             string         `json:"contract_id"`
	ParentID               string         `json:"parent_id,omitempty"`
	EventCount             int            `json:"event_count"`
	FirstSequence          int64          `json:"first_sequence,omitempty"`
	LastSequence           int64          `json:"last_sequence,omitempty"`
	LifecycleEvents        map[string]int `json:"lifecycle_events,omitempty"`
	PolicyEvents           map[string]int `json:"policy_events,omitempty"`
	PolicyDecisions        map[string]int `json:"policy_decisions,omitempty"`
	PolicyTools            map[string]int `json:"policy_tools,omitempty"`
	ObligationEvents       map[string]int `json:"obligation_events,omitempty"`
	ObligationStatusCounts map[string]int `json:"obligation_status_counts,omitempty"`
	ObligationKindCounts   map[string]int `json:"obligation_kind_counts,omitempty"`
	GateEvents             map[string]int `json:"gate_events,omitempty"`
	GateDecisionCounts     map[string]int `json:"gate_decision_counts,omitempty"`
	GateKindCounts         map[string]int `json:"gate_kind_counts,omitempty"`
	GuardFrictionCounts    map[string]int `json:"guard_friction_counts,omitempty"`
	OpenObligations        []string       `json:"open_obligations,omitempty"`
	ResolvedObligations    []string       `json:"resolved_obligations,omitempty"`
	WaivedObligations      []string       `json:"waived_obligations,omitempty"`
	SidecarRequestIDs      []string       `json:"sidecar_request_ids,omitempty"`
	ToolCallIDs            []string       `json:"tool_call_ids,omitempty"`
}

type LifecycleEventCoverage struct {
	Total                  int      `json:"total"`
	Compiled               int      `json:"compiled"`
	Presented              int      `json:"presented"`
	Recompiled             int      `json:"recompiled"`
	Stopped                int      `json:"stopped"`
	ContractsWithLifecycle []string `json:"contracts_with_lifecycle,omitempty"`
	LifecycleOnlyContracts []string `json:"lifecycle_only_contracts,omitempty"`
}

type PolicyEventCoverage struct {
	Total               int            `json:"total"`
	IntentClassified    int            `json:"intent_classified"`
	PathRolesResolved   int            `json:"path_roles_resolved"`
	DecisionEvents      int            `json:"decision_events"`
	ResultEvents        int            `json:"result_events"`
	DecisionCounts      map[string]int `json:"decision_counts,omitempty"`
	ToolCounts          map[string]int `json:"tool_counts,omitempty"`
	CapabilityCounts    map[string]int `json:"capability_counts,omitempty"`
	SideEffectCounts    map[string]int `json:"side_effect_counts,omitempty"`
	ContractsWithPolicy []string       `json:"contracts_with_policy,omitempty"`
}

type ObligationEventCoverage struct {
	Total                    int                  `json:"total"`
	Opened                   int                  `json:"opened"`
	Resolved                 int                  `json:"resolved"`
	Waived                   int                  `json:"waived"`
	KindCounts               map[string]int       `json:"kind_counts,omitempty"`
	StatusCounts             map[string]int       `json:"status_counts,omitempty"`
	OwnerCounts              map[string]int       `json:"owner_counts,omitempty"`
	ContractsWithObligations []string             `json:"contracts_with_obligations,omitempty"`
	OpenObligations          []ObligationAuditRef `json:"open_obligations,omitempty"`
	UnresolvedObligations    []ObligationAuditRef `json:"unresolved_obligations,omitempty"`
	UnmatchedObligations     []ObligationAuditRef `json:"unmatched_obligations,omitempty"`
}

type GateEventCoverage struct {
	Total                           int             `json:"total"`
	DecisionEvents                  int             `json:"decision_events"`
	KindCounts                      map[string]int  `json:"kind_counts,omitempty"`
	DecisionCounts                  map[string]int  `json:"decision_counts,omitempty"`
	DecisionCountsByPackID          map[string]int  `json:"decision_counts_by_pack_id,omitempty"`
	DecisionCountsByEnforcementMode map[string]int  `json:"decision_counts_by_enforcement_mode,omitempty"`
	ReasonCounts                    map[string]int  `json:"reason_counts,omitempty"`
	ToolCounts                      map[string]int  `json:"tool_counts,omitempty"`
	EventsWithObservedFacts         int             `json:"events_with_observed_facts"`
	EventsWithSafeAlternatives      int             `json:"events_with_safe_alternatives"`
	ContractsWithGateEvents         []string        `json:"contracts_with_gate_events,omitempty"`
	OpenObligationRefs              []string        `json:"open_obligation_refs,omitempty"`
	ResolvedObligationRefs          []string        `json:"resolved_obligation_refs,omitempty"`
	InconsistentOpenObligationRefs  []EventAuditRef `json:"inconsistent_open_obligation_refs,omitempty"`
	MissingPackIDRefs               []EventAuditRef `json:"missing_pack_id_refs,omitempty"`
	MissingEnforcementModeRefs      []EventAuditRef `json:"missing_enforcement_mode_refs,omitempty"`
	MissingSafeAlternativesRefs     []EventAuditRef `json:"missing_safe_alternatives_refs,omitempty"`
}

type GuardFrictionCoverage struct {
	Total                    int                                  `json:"guards_fired"`
	AvoidableFriction        int                                  `json:"avoidable_friction"`
	UnclassifiedFriction     int                                  `json:"unclassified_friction"`
	LaneCounts               map[string]int                       `json:"lane_counts,omitempty"`
	PhaseCounts              map[string]int                       `json:"phase_counts,omitempty"`
	AvoidabilityCounts       map[string]int                       `json:"avoidability_counts,omitempty"`
	FirstInvalidActionCounts map[string]int                       `json:"first_invalid_action_counts,omitempty"`
	RetryOutcomeCounts       map[string]int                       `json:"retry_outcome_counts,omitempty"`
	GuardSurfaceCounts       map[string]int                       `json:"guard_surface_counts,omitempty"`
	DecisionCounts           map[string]int                       `json:"decision_counts,omitempty"`
	TopContracts             []GuardFrictionContractCoverage      `json:"top_contracts,omitempty"`
	SafeAlternativeCoverage  GuardFrictionSafeAlternativeCoverage `json:"safe_alternative_coverage"`
	UnclassifiedRefs         []EventAuditRef                      `json:"unclassified_refs,omitempty"`
}

type GuardFrictionContractCoverage struct {
	ContractID           string `json:"contract_id"`
	Total                int    `json:"total"`
	AvoidableFriction    int    `json:"avoidable_friction"`
	UnclassifiedFriction int    `json:"unclassified_friction"`
}

type GuardFrictionSafeAlternativeCoverage struct {
	EventsWithSafeAlternatives    int            `json:"events_with_safe_alternatives"`
	EventsMissingSafeAlternatives int            `json:"events_missing_safe_alternatives"`
	SafeAlternativeCounts         map[string]int `json:"safe_alternative_counts,omitempty"`
	VisibleActionClassCounts      map[string]int `json:"visible_action_class_counts,omitempty"`
	VisibleToolCounts             map[string]int `json:"visible_tool_counts,omitempty"`
}

type TurnPhaseEventCoverage struct {
	Total                  int                      `json:"total"`
	ContractsWithTurnPhase []string                 `json:"contracts_with_turn_phase,omitempty"`
	PhaseCounts            map[string]int           `json:"phase_counts,omitempty"`
	SurfaceCounts          map[string]int           `json:"surface_counts,omitempty"`
	EnforcementModeCounts  map[string]int           `json:"enforcement_mode_counts,omitempty"`
	Contradictions         []TurnPhaseContradiction `json:"contradictions,omitempty"`
}

type TurnPhaseContradiction struct {
	EventID      string `json:"event_id,omitempty"`
	Type         string `json:"type,omitempty"`
	Sequence     int64  `json:"sequence,omitempty"`
	ContractID   string `json:"contract_id,omitempty"`
	PhaseID      string `json:"phase_id,omitempty"`
	ModelCallID  string `json:"model_call_id,omitempty"`
	ProtocolHash string `json:"protocol_hash,omitempty"`
	SurfaceID    string `json:"surface_id,omitempty"`
	SurfacePair  string `json:"surface_pair,omitempty"`
	InvariantID  string `json:"invariant_id,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	ActionClass  string `json:"action_class,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type ContractReferenceCoverage struct {
	MissingContractIDEvents     []EventAuditRef `json:"missing_contract_id_events,omitempty"`
	UnmatchedContractReferences []EventAuditRef `json:"unmatched_contract_references,omitempty"`
	StaleContractReferences     []EventAuditRef `json:"stale_contract_references,omitempty"`
	UnmatchedParentReferences   []EventAuditRef `json:"unmatched_parent_references,omitempty"`
	MissingExpectedContracts    []string        `json:"missing_expected_contracts,omitempty"`
}

type SidecarToolCorrelationCoverage struct {
	SidecarEventCount     int                           `json:"sidecar_event_count"`
	ToolCallEventCount    int                           `json:"tool_call_event_count"`
	CorrelatedEventCount  int                           `json:"correlated_event_count"`
	SidecarRequestIDs     []string                      `json:"sidecar_request_ids,omitempty"`
	ToolCallIDs           []string                      `json:"tool_call_ids,omitempty"`
	UnmatchedCorrelations []SidecarToolCorrelationIssue `json:"unmatched_correlations,omitempty"`
}

type ContractMetadataCoverage struct {
	EventsWithMetadata         int            `json:"events_with_metadata"`
	ClientClassificationCounts map[string]int `json:"client_classification_counts,omitempty"`
	ProductSignalCounts        map[string]int `json:"product_signal_counts,omitempty"`
	ClientClassifications      []string       `json:"client_classifications,omitempty"`
	ProductSignals             []string       `json:"product_signals,omitempty"`
}

type EventAuditRef struct {
	EventID    string `json:"event_id,omitempty"`
	Type       string `json:"type,omitempty"`
	Sequence   int64  `json:"sequence,omitempty"`
	ContractID string `json:"contract_id,omitempty"`
	Reference  string `json:"reference,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ObligationAuditRef struct {
	EventID      string `json:"event_id,omitempty"`
	Sequence     int64  `json:"sequence,omitempty"`
	ContractID   string `json:"contract_id,omitempty"`
	ObligationID string `json:"obligation_id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Status       string `json:"status,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type SidecarToolCorrelationIssue struct {
	EventID          string `json:"event_id,omitempty"`
	Type             string `json:"type,omitempty"`
	Sequence         int64  `json:"sequence,omitempty"`
	ContractID       string `json:"contract_id,omitempty"`
	SidecarRequestID string `json:"sidecar_request_id,omitempty"`
	SidecarTool      string `json:"sidecar_tool,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type EventAuditFinding struct {
	ID          string   `json:"id"`
	Severity    string   `json:"severity"`
	Evidence    string   `json:"evidence"`
	EventIDs    []string `json:"event_ids,omitempty"`
	ContractIDs []string `json:"contract_ids,omitempty"`
}

type eventAuditState struct {
	report                 EventAuditReport
	contracts              map[string]*ContractEventCoverage
	expectedContracts      map[string]struct{}
	lifecycleContracts     map[string]struct{}
	referencedContracts    map[string]struct{}
	staleAfterSequence     map[string]staleContractState
	openObligations        map[string]ObligationAuditRef
	sidecarRequestIDs      map[string]struct{}
	toolCallIDs            map[string]struct{}
	turnPhaseContracts     map[string]TurnPhaseContractEvent
	turnPhaseContractIDs   map[string]struct{}
	guardFrictionContracts map[string]*GuardFrictionContractCoverage
}

type staleContractState struct {
	Sequence int64
	Reason   string
}

func AuditEvents(events []Event, opts EventAuditOptions) EventAuditReport {
	state := eventAuditState{
		report: EventAuditReport{
			SchemaVersion: AuditReportSchemaVersion,
		},
		contracts:              make(map[string]*ContractEventCoverage),
		expectedContracts:      make(map[string]struct{}),
		lifecycleContracts:     make(map[string]struct{}),
		referencedContracts:    make(map[string]struct{}),
		staleAfterSequence:     make(map[string]staleContractState),
		openObligations:        make(map[string]ObligationAuditRef),
		sidecarRequestIDs:      make(map[string]struct{}),
		toolCallIDs:            make(map[string]struct{}),
		turnPhaseContracts:     make(map[string]TurnPhaseContractEvent),
		turnPhaseContractIDs:   make(map[string]struct{}),
		guardFrictionContracts: make(map[string]*GuardFrictionContractCoverage),
	}
	for _, contractID := range compactStrings(opts.ExpectedContractIDs) {
		state.expectedContracts[contractID] = struct{}{}
	}

	ordered := append([]Event(nil), events...)
	slices.SortStableFunc(ordered, compareEventAuditOrder)
	state.report.TotalEvents = len(ordered)
	for i := range ordered {
		event := copyEvent(ordered[i])
		rawGateFields := gateAuditExtendedFields(event.Gate)
		event.Normalize()
		state.addEvent(event, rawGateFields)
	}
	state.finish()
	return state.report
}

func compareEventAuditOrder(a, b Event) int {
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

func (s *eventAuditState) addEvent(event Event, rawGateFields gateAuditFieldSnapshot) {
	if event.ContractID == "" {
		s.report.ReferenceCoverage.MissingContractIDEvents = append(s.report.ReferenceCoverage.MissingContractIDEvents, auditRef(event, "", "event has no contract_id"))
	} else {
		s.referencedContracts[event.ContractID] = struct{}{}
		coverage := s.contractCoverage(event.ContractID)
		coverage.EventCount++
		coverage.ParentID = firstNonEmptyAuditValue(coverage.ParentID, event.ParentID)
		if coverage.FirstSequence == 0 || event.Sequence < coverage.FirstSequence {
			coverage.FirstSequence = event.Sequence
		}
		if event.Sequence > coverage.LastSequence {
			coverage.LastSequence = event.Sequence
		}
		if stale, ok := s.staleAfterSequence[event.ContractID]; ok && sequenceAfter(event.Sequence, stale.Sequence) {
			s.report.ReferenceCoverage.StaleContractReferences = append(s.report.ReferenceCoverage.StaleContractReferences, auditRef(event, event.ContractID, stale.Reason))
		}
	}
	s.addMetadata(event)
	s.addSidecarToolCorrelation(event)

	switch event.Type {
	case EventContractCompiled, EventContractPresented, EventContractRecompiled, EventContractStopped:
		s.addLifecycle(event)
	case EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult:
		s.addPolicy(event)
	case EventObligationOpened, EventObligationResolved, EventObligationWaived:
		s.addObligation(event)
	case EventGateDecision:
		s.addGate(event, rawGateFields)
	case EventGuardFriction:
		s.addGuardFriction(event)
	}
}

func (s *eventAuditState) addLifecycle(event Event) {
	s.report.LifecycleCoverage.Total++
	if event.ContractID != "" {
		s.lifecycleContracts[event.ContractID] = struct{}{}
		incrementContractMap(s.contractCoverage(event.ContractID), "lifecycle", string(event.Type))
	}
	if event.Lifecycle != nil && event.Lifecycle.Summary != nil && event.Lifecycle.Summary.ContractID != "" {
		s.lifecycleContracts[event.Lifecycle.Summary.ContractID] = struct{}{}
	}
	if event.Lifecycle != nil && event.Lifecycle.TurnPhaseContract != nil {
		s.addTurnPhaseContract(event, *event.Lifecycle.TurnPhaseContract)
		if event.Lifecycle.ModelProtocol != nil {
			s.addTurnPhaseSurfaceSummary(event, SurfaceSummaryFromModelProtocol(*event.Lifecycle.ModelProtocol, *event.Lifecycle.TurnPhaseContract))
		}
	}
	if event.Lifecycle != nil {
		for _, surface := range event.Lifecycle.SurfaceSummaries {
			s.addTurnPhaseSurfaceSummary(event, surface)
		}
	}
	switch event.Type {
	case EventContractCompiled:
		s.report.LifecycleCoverage.Compiled++
	case EventContractPresented:
		s.report.LifecycleCoverage.Presented++
	case EventContractRecompiled:
		s.report.LifecycleCoverage.Recompiled++
		if event.Lifecycle != nil {
			if event.Lifecycle.OldContractID != "" {
				if !s.contractKnown(event.Lifecycle.OldContractID) {
					s.report.ReferenceCoverage.UnmatchedContractReferences = append(s.report.ReferenceCoverage.UnmatchedContractReferences, auditRef(event, event.Lifecycle.OldContractID, "recompiled old_contract_id has no prior lifecycle or expected contract"))
				}
				s.staleAfterSequence[event.Lifecycle.OldContractID] = staleContractState{Sequence: event.Sequence, Reason: "contract superseded by recompile"}
			}
			if event.Lifecycle.NewContractID != "" {
				s.lifecycleContracts[event.Lifecycle.NewContractID] = struct{}{}
			}
		}
	case EventContractStopped:
		s.report.LifecycleCoverage.Stopped++
		if event.ContractID != "" {
			s.staleAfterSequence[event.ContractID] = staleContractState{Sequence: event.Sequence, Reason: "contract stopped"}
		}
	}
}

func (s *eventAuditState) addPolicy(event Event) {
	s.report.PolicyCoverage.Total++
	if event.ContractID != "" {
		incrementContractMap(s.contractCoverage(event.ContractID), "policy", string(event.Type))
	}
	switch event.Type {
	case EventToolIntentClassified:
		s.report.PolicyCoverage.IntentClassified++
	case EventPathRolesResolved:
		s.report.PolicyCoverage.PathRolesResolved++
	case EventToolPolicyDecision:
		s.report.PolicyCoverage.DecisionEvents++
	case EventToolPolicyResult:
		s.report.PolicyCoverage.ResultEvents++
	}
	if event.Policy == nil {
		return
	}
	s.addTurnPhasePolicyObservation(event)
	incrementStringMap(&s.report.PolicyCoverage.DecisionCounts, string(event.Policy.Decision))
	incrementStringMap(&s.report.PolicyCoverage.ToolCounts, event.Policy.ToolName)
	incrementStringMap(&s.report.PolicyCoverage.CapabilityCounts, string(event.Policy.Capability))
	incrementStringMap(&s.report.PolicyCoverage.SideEffectCounts, string(event.Policy.SideEffect))
	if event.ContractID != "" {
		coverage := s.contractCoverage(event.ContractID)
		incrementStringMap(&coverage.PolicyDecisions, string(event.Policy.Decision))
		incrementStringMap(&coverage.PolicyTools, event.Policy.ToolName)
	}
}

func (s *eventAuditState) addTurnPhaseContract(event Event, phase TurnPhaseContractEvent) {
	phase.normalize()
	if phase.PhaseID == "" {
		return
	}
	s.report.TurnPhaseCoverage.Total++
	s.turnPhaseContracts[event.ContractID] = phase
	if event.ContractID != "" {
		s.turnPhaseContractIDs[event.ContractID] = struct{}{}
	}
	incrementStringMap(&s.report.TurnPhaseCoverage.PhaseCounts, phase.PhaseID)
	incrementStringMap(&s.report.TurnPhaseCoverage.EnforcementModeCounts, phase.EnforcementMode)
	if stringSetContains(phase.ActionPolicy.AllowedActionClasses, "submit_plan") &&
		!stringSetContains(phase.RuntimeCall.ExpressibleActionClasses, "submit_plan") {
		s.addTurnPhaseContradiction(event, phase, "", "submit_plan", "phase allows submit_plan but runtime_call cannot express submit_plan")
	}
	if stringSetContains(phase.ActionPolicy.AllowedActionClasses, "final_answer") && turnPhaseEvidenceOpen(phase.EvidenceState) {
		s.addTurnPhaseContradiction(event, phase, "", "final_answer", "phase allows final_answer while evidence_state still has open acceptance, verification, recovery, or blocker evidence")
	}
}

func (s *eventAuditState) addTurnPhaseSurfaceSummary(event Event, surface TurnPhaseSurfaceSummaryEvent) {
	surface.normalize()
	if surface.SurfaceID == "" {
		return
	}
	incrementStringMap(&s.report.TurnPhaseCoverage.SurfaceCounts, surface.SurfaceID)
	phase, ok := s.turnPhaseContracts[event.ContractID]
	if !ok || phase.PhaseID == "" {
		return
	}
	for _, issue := range ReconcileTurnPhaseSurface(phase, surface) {
		issue.EventID = event.EventID
		issue.Type = string(event.Type)
		issue.Sequence = event.Sequence
		issue.ContractID = firstNonEmptyAuditValue(issue.ContractID, event.ContractID)
		s.report.TurnPhaseCoverage.Contradictions = append(s.report.TurnPhaseCoverage.Contradictions, issue)
	}
}

func (s *eventAuditState) addTurnPhasePolicyObservation(event Event) {
	if event.Policy == nil || event.ContractID == "" {
		return
	}
	phase, ok := s.turnPhaseContracts[event.ContractID]
	if !ok || phase.PhaseID == "" {
		return
	}
	tool := strings.TrimSpace(event.Policy.ToolName)
	if tool == "" {
		return
	}
	actionClass := turnPhaseActionClassForTool(tool)
	if actionClass != "" && stringSetContains(phase.ActionPolicy.InvalidActionClasses, actionClass) {
		s.addTurnPhaseContradiction(event, phase, tool, actionClass, "observed tool action class is invalid in latest turn_phase_contract")
		return
	}
	if len(phase.AllowedTools) == 0 && tool != "" && phase.RuntimeCall.ToolTransportMode != "textual" {
		s.addTurnPhaseContradiction(event, phase, tool, actionClass, "observed tool while latest turn_phase_contract exposes no manifest-visible tools")
		return
	}
	if len(phase.ActionPolicy.CallableToolNames) > 0 && !stringSetContains(phase.ActionPolicy.CallableToolNames, tool) {
		s.addTurnPhaseContradiction(event, phase, tool, actionClass, "observed tool is absent from latest turn_phase_contract callable_tool_names")
		return
	}
	if len(phase.AllowedTools) > 0 && !stringSetContains(phase.AllowedTools, tool) {
		s.addTurnPhaseContradiction(event, phase, tool, actionClass, "observed tool is absent from latest turn_phase_contract allowed_tools")
	}
}

func (s *eventAuditState) addTurnPhaseContradiction(event Event, phase TurnPhaseContractEvent, toolName string, actionClass string, reason string) {
	s.report.TurnPhaseCoverage.Contradictions = append(s.report.TurnPhaseCoverage.Contradictions, TurnPhaseContradiction{
		EventID:      event.EventID,
		Type:         string(event.Type),
		Sequence:     event.Sequence,
		ContractID:   event.ContractID,
		PhaseID:      phase.PhaseID,
		ModelCallID:  phase.ModelCallID,
		ProtocolHash: phase.ProtocolHash,
		SurfaceID:    "observed_tool_policy",
		SurfacePair:  turnPhaseSurfacePair("turn_phase_contract", "observed_tool_policy"),
		InvariantID:  turnPhaseInvariantIDForReason(reason),
		ToolName:     strings.TrimSpace(toolName),
		ActionClass:  strings.TrimSpace(actionClass),
		Reason:       strings.TrimSpace(reason),
	})
}

func (s *eventAuditState) addObligation(event Event) {
	s.report.ObligationCoverage.Total++
	if event.ContractID != "" {
		incrementContractMap(s.contractCoverage(event.ContractID), "obligation", string(event.Type))
	}
	switch event.Type {
	case EventObligationOpened:
		s.report.ObligationCoverage.Opened++
	case EventObligationResolved:
		s.report.ObligationCoverage.Resolved++
	case EventObligationWaived:
		s.report.ObligationCoverage.Waived++
	}
	if event.Obligation == nil {
		return
	}
	ref := ObligationAuditRef{
		EventID:      event.EventID,
		Sequence:     event.Sequence,
		ContractID:   event.ContractID,
		ObligationID: event.Obligation.ObligationID,
		Kind:         string(event.Obligation.Kind),
		Status:       string(event.Obligation.Status),
	}
	incrementStringMap(&s.report.ObligationCoverage.KindCounts, string(event.Obligation.Kind))
	incrementStringMap(&s.report.ObligationCoverage.StatusCounts, string(event.Obligation.Status))
	incrementStringMap(&s.report.ObligationCoverage.OwnerCounts, string(event.Obligation.Owner))
	if event.ContractID != "" {
		coverage := s.contractCoverage(event.ContractID)
		incrementStringMap(&coverage.ObligationStatusCounts, string(event.Obligation.Status))
		incrementStringMap(&coverage.ObligationKindCounts, string(event.Obligation.Kind))
		switch event.Type {
		case EventObligationOpened:
			coverage.OpenObligations = util.AppendUnique(coverage.OpenObligations, event.Obligation.ObligationID)
		case EventObligationResolved:
			coverage.ResolvedObligations = util.AppendUnique(coverage.ResolvedObligations, event.Obligation.ObligationID)
		case EventObligationWaived:
			coverage.WaivedObligations = util.AppendUnique(coverage.WaivedObligations, event.Obligation.ObligationID)
		}
	}
	key := obligationKey(event.ContractID, event.Obligation.ObligationID)
	switch event.Type {
	case EventObligationOpened:
		s.openObligations[key] = ref
	case EventObligationResolved, EventObligationWaived:
		if _, ok := s.openObligations[key]; !ok {
			ref.Reason = "obligation resolution has no matching opened obligation event"
			s.report.ObligationCoverage.UnmatchedObligations = append(s.report.ObligationCoverage.UnmatchedObligations, ref)
		}
		delete(s.openObligations, key)
		if event.ContractID != "" {
			coverage := s.contractCoverage(event.ContractID)
			coverage.OpenObligations = removeString(coverage.OpenObligations, event.Obligation.ObligationID)
		}
	}
}

func (s *eventAuditState) addGate(event Event, rawGateFields gateAuditFieldSnapshot) {
	s.report.GateCoverage.Total++
	if event.ContractID != "" {
		incrementContractMap(s.contractCoverage(event.ContractID), "gate", string(event.Type))
	}
	switch event.Type {
	case EventGateDecision:
		s.report.GateCoverage.DecisionEvents++
	}
	if event.Gate == nil {
		return
	}
	s.addGateProtocolHashParity(event)
	incrementStringMap(&s.report.GateCoverage.KindCounts, string(event.Gate.Gate))
	incrementStringMap(&s.report.GateCoverage.DecisionCounts, string(event.Gate.Decision))
	incrementStringMap(&s.report.GateCoverage.ReasonCounts, event.Gate.ReasonCode)
	incrementStringMap(&s.report.GateCoverage.ToolCounts, event.Gate.ToolName)
	extended := gateAuditExtendedFields(event.Gate)
	if rawGateFields.PackIDFieldPresent {
		if rawGateFields.PackID == "" {
			s.report.GateCoverage.MissingPackIDRefs = append(s.report.GateCoverage.MissingPackIDRefs, auditRef(event, "pack_id", "typed gate event missing pack_id"))
		}
	}
	incrementStringMap(&s.report.GateCoverage.DecisionCountsByPackID, extended.PackID)
	if rawGateFields.EnforcementModeFieldPresent {
		if rawGateFields.EnforcementMode == "" {
			s.report.GateCoverage.MissingEnforcementModeRefs = append(s.report.GateCoverage.MissingEnforcementModeRefs, auditRef(event, "enforcement_mode", "typed gate event missing enforcement_mode"))
		}
	}
	incrementStringMap(&s.report.GateCoverage.DecisionCountsByEnforcementMode, extended.EnforcementMode)
	if rawGateFields.ObservedFactsFieldPresent && len(rawGateFields.ObservedFacts) > 0 {
		s.report.GateCoverage.EventsWithObservedFacts++
	}
	if rawGateFields.SafeAlternativesFieldPresent && len(rawGateFields.SafeAlternatives) > 0 {
		s.report.GateCoverage.EventsWithSafeAlternatives++
	}
	if rawGateFields.SafeAlternativesFieldPresent && gateDecisionNeedsSafeAlternatives(event.Gate.Decision, extended.EnforcementMode) && len(rawGateFields.SafeAlternatives) == 0 {
		s.report.GateCoverage.MissingSafeAlternativesRefs = append(s.report.GateCoverage.MissingSafeAlternativesRefs, auditRef(event, "safe_alternatives", "enforced reject/deny/stop gate event missing safe_alternatives"))
	}
	s.report.GateCoverage.OpenObligationRefs = append(s.report.GateCoverage.OpenObligationRefs, event.Gate.OpenObligations...)
	s.report.GateCoverage.ResolvedObligationRefs = append(s.report.GateCoverage.ResolvedObligationRefs, event.Gate.ResolvedObligations...)
	for _, obligationID := range event.Gate.OpenObligations {
		if _, ok := s.openObligations[obligationKey(event.ContractID, obligationID)]; !ok {
			s.report.GateCoverage.InconsistentOpenObligationRefs = append(s.report.GateCoverage.InconsistentOpenObligationRefs, auditRef(event, obligationID, "gate reported open obligation that was not open at gate sequence"))
		}
	}
	if event.ContractID != "" {
		coverage := s.contractCoverage(event.ContractID)
		incrementStringMap(&coverage.GateDecisionCounts, string(event.Gate.Decision))
		incrementStringMap(&coverage.GateKindCounts, string(event.Gate.Gate))
	}
}

func (s *eventAuditState) addGateProtocolHashParity(event Event) {
	if event.Gate == nil || event.ContractID == "" {
		return
	}
	phase, ok := s.turnPhaseContracts[event.ContractID]
	if !ok || phase.PhaseID == "" {
		return
	}
	if event.Gate.Gate != GateCompletion {
		return
	}
	gateHash := strings.TrimSpace(event.Gate.ProtocolHash)
	phaseHash := strings.TrimSpace(phase.ProtocolHash)
	switch {
	case phaseHash != "" && gateHash != "" && gateHash != phaseHash:
		s.addTurnPhaseContradiction(event, phase, "", "", "completion gate protocol_hash differs from latest turn_phase_contract")
	}
	if n := len(s.report.TurnPhaseCoverage.Contradictions); n > 0 && strings.TrimSpace(s.report.TurnPhaseCoverage.Contradictions[n-1].EventID) == event.EventID {
		s.report.TurnPhaseCoverage.Contradictions[n-1].SurfaceID = "completion_gate"
		s.report.TurnPhaseCoverage.Contradictions[n-1].SurfacePair = turnPhaseSurfacePair("turn_phase_contract", "completion_gate")
		s.report.TurnPhaseCoverage.Contradictions[n-1].ProtocolHash = firstNonEmptyAuditValue(gateHash, phaseHash)
	}
}

func (s *eventAuditState) addGuardFriction(event Event) {
	s.report.GuardFrictionCoverage.Total++
	if event.ContractID != "" {
		incrementStringMap(&s.contractCoverage(event.ContractID).GuardFrictionCounts, string(EventGuardFriction))
	}
	if event.GuardFriction == nil {
		s.report.GuardFrictionCoverage.UnclassifiedFriction++
		s.report.GuardFrictionCoverage.UnclassifiedRefs = append(s.report.GuardFrictionCoverage.UnclassifiedRefs, auditRef(event, event.ContractID, "guard_friction payload missing"))
		return
	}
	friction := *event.GuardFriction
	friction.normalize()
	contractID := firstNonEmptyAuditValue(friction.ContractID, event.ContractID)
	incrementStringMap(&s.report.GuardFrictionCoverage.LaneCounts, firstNonEmptyAuditValue(friction.Lane, event.Correlation.Lane))
	incrementStringMap(&s.report.GuardFrictionCoverage.PhaseCounts, firstNonEmptyAuditValue(friction.PhaseID, friction.Phase, event.Correlation.Phase))
	incrementStringMap(&s.report.GuardFrictionCoverage.AvoidabilityCounts, string(friction.Avoidability))
	incrementStringMap(&s.report.GuardFrictionCoverage.FirstInvalidActionCounts, friction.FirstInvalidAction)
	incrementStringMap(&s.report.GuardFrictionCoverage.RetryOutcomeCounts, string(friction.RetryOutcome))
	incrementStringMap(&s.report.GuardFrictionCoverage.GuardSurfaceCounts, string(friction.GuardSurface))
	incrementStringMap(&s.report.GuardFrictionCoverage.DecisionCounts, string(friction.Decision))
	if len(friction.SafeAlternatives) > 0 {
		s.report.GuardFrictionCoverage.SafeAlternativeCoverage.EventsWithSafeAlternatives++
		for _, alternative := range friction.SafeAlternatives {
			incrementStringMap(&s.report.GuardFrictionCoverage.SafeAlternativeCoverage.SafeAlternativeCounts, alternative)
		}
	} else {
		s.report.GuardFrictionCoverage.SafeAlternativeCoverage.EventsMissingSafeAlternatives++
	}
	for _, actionClass := range friction.VisibleActionClasses {
		incrementStringMap(&s.report.GuardFrictionCoverage.SafeAlternativeCoverage.VisibleActionClassCounts, actionClass)
	}
	for _, tool := range friction.VisibleTools {
		incrementStringMap(&s.report.GuardFrictionCoverage.SafeAlternativeCoverage.VisibleToolCounts, tool)
	}
	contractCoverage := s.guardFrictionContractCoverage(contractID)
	contractCoverage.Total++
	if guardFrictionAvoidable(friction.Avoidability) {
		s.report.GuardFrictionCoverage.AvoidableFriction++
		contractCoverage.AvoidableFriction++
	}
	if guardFrictionUnclassified(event, friction) {
		s.report.GuardFrictionCoverage.UnclassifiedFriction++
		contractCoverage.UnclassifiedFriction++
		s.report.GuardFrictionCoverage.UnclassifiedRefs = append(s.report.GuardFrictionCoverage.UnclassifiedRefs, auditRef(event, contractID, "guard_friction lacks enough structured classification evidence"))
	}
}

func (s *eventAuditState) addMetadata(event Event) {
	if len(event.Metadata.ClientClassifications) == 0 && len(event.Metadata.ProductSignals) == 0 {
		return
	}
	s.report.MetadataCoverage.EventsWithMetadata++
	for _, classification := range event.Metadata.ClientClassifications {
		incrementStringMap(&s.report.MetadataCoverage.ClientClassificationCounts, string(classification))
	}
	for _, signal := range event.Metadata.ProductSignals {
		incrementStringMap(&s.report.MetadataCoverage.ProductSignalCounts, string(signal))
	}
}

func (s *eventAuditState) addSidecarToolCorrelation(event Event) {
	sidecarPresent := event.Correlation.SidecarRequestID != "" || event.Correlation.SidecarCaller != "" || event.Correlation.SidecarProfile != "" || event.Correlation.SidecarTool != ""
	toolCallID := event.Correlation.ToolCallID
	toolName := event.Correlation.ToolName
	if event.Policy != nil {
		toolCallID = firstNonEmptyAuditValue(toolCallID, event.Policy.ToolCallID)
		toolName = firstNonEmptyAuditValue(toolName, event.Policy.ToolName)
	}
	if sidecarPresent {
		s.report.SidecarToolCorrelation.SidecarEventCount++
		if event.Correlation.SidecarRequestID != "" {
			s.sidecarRequestIDs[event.Correlation.SidecarRequestID] = struct{}{}
			if event.ContractID != "" {
				coverage := s.contractCoverage(event.ContractID)
				coverage.SidecarRequestIDs = util.AppendUnique(coverage.SidecarRequestIDs, event.Correlation.SidecarRequestID)
			}
		}
	}
	if toolCallID != "" || toolName != "" {
		s.report.SidecarToolCorrelation.ToolCallEventCount++
		if toolCallID != "" {
			s.toolCallIDs[toolCallID] = struct{}{}
			if event.ContractID != "" {
				coverage := s.contractCoverage(event.ContractID)
				coverage.ToolCallIDs = util.AppendUnique(coverage.ToolCallIDs, toolCallID)
			}
		}
	}
	if sidecarPresent && toolCallID != "" {
		s.report.SidecarToolCorrelation.CorrelatedEventCount++
	}
	if !sidecarPresent {
		return
	}
	if toolCallID == "" && isPolicyToolEvent(event.Type) {
		s.addSidecarIssue(event, toolCallID, toolName, "sidecar policy event missing tool_call_id")
	}
	if event.Correlation.SidecarRequestID == "" && toolCallID != "" {
		s.addSidecarIssue(event, toolCallID, toolName, "tool call correlation missing sidecar_request_id")
	}
	if event.Correlation.SidecarTool != "" && toolName != "" && event.Correlation.SidecarTool != toolName {
		s.addSidecarIssue(event, toolCallID, toolName, "sidecar_tool does not match tool_name")
	}
	if event.Policy != nil {
		if event.Correlation.ToolCallID != "" && event.Policy.ToolCallID != "" && event.Correlation.ToolCallID != event.Policy.ToolCallID {
			s.addSidecarIssue(event, toolCallID, toolName, "correlation tool_call_id does not match policy tool_call_id")
		}
		if event.Correlation.ToolName != "" && event.Policy.ToolName != "" && event.Correlation.ToolName != event.Policy.ToolName {
			s.addSidecarIssue(event, toolCallID, toolName, "correlation tool_name does not match policy tool_name")
		}
	}
}

func (s *eventAuditState) addSidecarIssue(event Event, toolCallID, toolName, reason string) {
	s.report.SidecarToolCorrelation.UnmatchedCorrelations = append(s.report.SidecarToolCorrelation.UnmatchedCorrelations, SidecarToolCorrelationIssue{
		EventID:          event.EventID,
		Type:             string(event.Type),
		Sequence:         event.Sequence,
		ContractID:       event.ContractID,
		SidecarRequestID: event.Correlation.SidecarRequestID,
		SidecarTool:      event.Correlation.SidecarTool,
		ToolCallID:       toolCallID,
		ToolName:         toolName,
		Reason:           reason,
	})
}

func (s *eventAuditState) finish() {
	for contractID := range s.expectedContracts {
		if _, ok := s.referencedContracts[contractID]; !ok {
			s.report.ReferenceCoverage.MissingExpectedContracts = append(s.report.ReferenceCoverage.MissingExpectedContracts, contractID)
		}
	}
	for contractID := range s.referencedContracts {
		if contractID == "" || s.contractKnown(contractID) {
			continue
		}
		s.report.ReferenceCoverage.UnmatchedContractReferences = append(s.report.ReferenceCoverage.UnmatchedContractReferences, EventAuditRef{
			ContractID: contractID,
			Reference:  contractID,
			Reason:     "contract reference has no lifecycle event or expected contract",
		})
	}
	for _, coverage := range s.contracts {
		if coverage.ParentID != "" && !s.contractKnown(coverage.ParentID) {
			s.report.ReferenceCoverage.UnmatchedParentReferences = append(s.report.ReferenceCoverage.UnmatchedParentReferences, EventAuditRef{
				ContractID: coverage.ContractID,
				Reference:  coverage.ParentID,
				Reason:     "parent_id has no lifecycle event or expected contract",
			})
		}
		slices.Sort(coverage.OpenObligations)
		slices.Sort(coverage.ResolvedObligations)
		slices.Sort(coverage.WaivedObligations)
		slices.Sort(coverage.SidecarRequestIDs)
		slices.Sort(coverage.ToolCallIDs)
	}
	for _, ref := range s.openObligations {
		s.report.ObligationCoverage.OpenObligations = append(s.report.ObligationCoverage.OpenObligations, ref)
		ref.Reason = "obligation opened without resolved or waived event"
		s.report.ObligationCoverage.UnresolvedObligations = append(s.report.ObligationCoverage.UnresolvedObligations, ref)
	}
	s.report.LifecycleCoverage.ContractsWithLifecycle = sortedKeys(s.lifecycleContracts)
	s.report.PolicyCoverage.ContractsWithPolicy = s.contractsWithMap("policy")
	s.report.ObligationCoverage.ContractsWithObligations = s.contractsWithMap("obligation")
	s.report.GateCoverage.ContractsWithGateEvents = s.contractsWithMap("gate")
	s.report.LifecycleCoverage.LifecycleOnlyContracts = s.lifecycleOnlyContracts()
	s.report.SidecarToolCorrelation.SidecarRequestIDs = sortedKeys(s.sidecarRequestIDs)
	s.report.SidecarToolCorrelation.ToolCallIDs = sortedKeys(s.toolCallIDs)
	s.report.GateCoverage.OpenObligationRefs = compactStrings(s.report.GateCoverage.OpenObligationRefs)
	s.report.GateCoverage.ResolvedObligationRefs = compactStrings(s.report.GateCoverage.ResolvedObligationRefs)
	s.report.TurnPhaseCoverage.ContractsWithTurnPhase = sortedKeys(s.turnPhaseContractIDs)
	s.report.MetadataCoverage.ClientClassifications = sortedMapKeys(s.report.MetadataCoverage.ClientClassificationCounts)
	s.report.MetadataCoverage.ProductSignals = sortedMapKeys(s.report.MetadataCoverage.ProductSignalCounts)
	s.report.GuardFrictionCoverage.TopContracts = sortedGuardFrictionContractCoverages(s.guardFrictionContracts)
	s.report.Contracts = sortedContractCoverages(s.contracts)
	sortObligationRefs(s.report.ObligationCoverage.OpenObligations)
	sortObligationRefs(s.report.ObligationCoverage.UnresolvedObligations)
	sortObligationRefs(s.report.ObligationCoverage.UnmatchedObligations)
	sortAuditRefs(s.report.ReferenceCoverage.MissingContractIDEvents)
	sortAuditRefs(s.report.ReferenceCoverage.UnmatchedContractReferences)
	sortAuditRefs(s.report.ReferenceCoverage.StaleContractReferences)
	sortAuditRefs(s.report.ReferenceCoverage.UnmatchedParentReferences)
	sortAuditRefs(s.report.GateCoverage.InconsistentOpenObligationRefs)
	sortAuditRefs(s.report.GateCoverage.MissingPackIDRefs)
	sortAuditRefs(s.report.GateCoverage.MissingEnforcementModeRefs)
	sortAuditRefs(s.report.GateCoverage.MissingSafeAlternativesRefs)
	sortAuditRefs(s.report.GuardFrictionCoverage.UnclassifiedRefs)
	sortTurnPhaseContradictions(s.report.TurnPhaseCoverage.Contradictions)
	slices.Sort(s.report.ReferenceCoverage.MissingExpectedContracts)
	sortSidecarIssues(s.report.SidecarToolCorrelation.UnmatchedCorrelations)
	s.addFindings()
	s.report.AllClear = s.report.TotalEvents > 0 && len(s.report.Findings) == 0 && len(s.report.ObligationCoverage.UnresolvedObligations) == 0
}

func (s *eventAuditState) addFindings() {
	if refs := s.report.ReferenceCoverage.MissingContractIDEvents; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:       "contract_event_missing_contract_id",
			Severity: "high",
			Evidence: fmt.Sprintf("%d autonomy event(s) lack contract_id", len(refs)),
			EventIDs: eventIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.ReferenceCoverage.UnmatchedContractReferences; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_unmatched_contract_reference",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d contract reference(s) have no matching lifecycle event or expected contract", len(refs)),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.ReferenceCoverage.StaleContractReferences; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_stale_contract_reference",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d event(s) reference a stopped or superseded contract", len(refs)),
			EventIDs:    eventIDsFromAuditRefs(refs),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.ReferenceCoverage.UnmatchedParentReferences; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_unmatched_parent_reference",
			Severity:    "low",
			Evidence:    fmt.Sprintf("%d parent contract reference(s) have no matching lifecycle event or expected contract", len(refs)),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if missing := s.report.ReferenceCoverage.MissingExpectedContracts; len(missing) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_expected_contract_missing",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d expected contract(s) had no events", len(missing)),
			ContractIDs: append([]string(nil), missing...),
		})
	}
	if issues := s.report.SidecarToolCorrelation.UnmatchedCorrelations; len(issues) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_sidecar_tool_correlation_mismatch",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d sidecar/tool-call correlation issue(s)", len(issues)),
			EventIDs:    eventIDsFromSidecarIssues(issues),
			ContractIDs: contractIDsFromSidecarIssues(issues),
		})
	}
	if refs := s.report.GateCoverage.InconsistentOpenObligationRefs; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_gate_open_obligation_inconsistent",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d gate open-obligation reference(s) were not open at the gate sequence", len(refs)),
			EventIDs:    eventIDsFromAuditRefs(refs),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.GateCoverage.MissingPackIDRefs; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_gate_missing_pack_id",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d typed gate event(s) lack pack_id", len(refs)),
			EventIDs:    eventIDsFromAuditRefs(refs),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.GateCoverage.MissingEnforcementModeRefs; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_gate_missing_enforcement_mode",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d typed gate event(s) lack enforcement_mode", len(refs)),
			EventIDs:    eventIDsFromAuditRefs(refs),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.GateCoverage.MissingSafeAlternativesRefs; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_gate_missing_safe_alternatives",
			Severity:    "medium",
			Evidence:    fmt.Sprintf("%d enforced reject/deny/stop gate event(s) lack safe_alternatives", len(refs)),
			EventIDs:    eventIDsFromAuditRefs(refs),
			ContractIDs: contractIDsFromAuditRefs(refs),
		})
	}
	if refs := s.report.ObligationCoverage.UnmatchedObligations; len(refs) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "contract_event_unmatched_obligation_reference",
			Severity:    "low",
			Evidence:    fmt.Sprintf("%d obligation resolution event(s) had no matching opened event", len(refs)),
			EventIDs:    eventIDsFromObligationRefs(refs),
			ContractIDs: contractIDsFromObligationRefs(refs),
		})
	}
	if issues := s.report.TurnPhaseCoverage.Contradictions; len(issues) > 0 {
		s.report.Findings = append(s.report.Findings, EventAuditFinding{
			ID:          "turn_phase_contract_contradiction",
			Severity:    "high",
			Evidence:    fmt.Sprintf("%d turn phase contract contradiction(s)", len(issues)),
			EventIDs:    eventIDsFromTurnPhaseContradictions(issues),
			ContractIDs: contractIDsFromTurnPhaseContradictions(issues),
		})
	}
}

func (s *eventAuditState) contractCoverage(contractID string) *ContractEventCoverage {
	coverage, ok := s.contracts[contractID]
	if ok {
		return coverage
	}
	coverage = &ContractEventCoverage{ContractID: contractID}
	s.contracts[contractID] = coverage
	return coverage
}

func (s *eventAuditState) guardFrictionContractCoverage(contractID string) *GuardFrictionContractCoverage {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		contractID = "(missing)"
	}
	coverage, ok := s.guardFrictionContracts[contractID]
	if ok {
		return coverage
	}
	coverage = &GuardFrictionContractCoverage{ContractID: contractID}
	s.guardFrictionContracts[contractID] = coverage
	return coverage
}

func (s *eventAuditState) contractKnown(contractID string) bool {
	if contractID == "" {
		return false
	}
	if _, ok := s.lifecycleContracts[contractID]; ok {
		return true
	}
	if _, ok := s.expectedContracts[contractID]; ok {
		return true
	}
	return false
}

func (s *eventAuditState) contractsWithMap(kind string) []string {
	set := make(map[string]struct{})
	for contractID, coverage := range s.contracts {
		switch kind {
		case "policy":
			if len(coverage.PolicyEvents) > 0 {
				set[contractID] = struct{}{}
			}
		case "obligation":
			if len(coverage.ObligationEvents) > 0 {
				set[contractID] = struct{}{}
			}
		case "gate":
			if len(coverage.GateEvents) > 0 {
				set[contractID] = struct{}{}
			}
		case "guard_friction":
			if len(coverage.GuardFrictionCounts) > 0 {
				set[contractID] = struct{}{}
			}
		}
	}
	return sortedKeys(set)
}

func (s *eventAuditState) lifecycleOnlyContracts() []string {
	set := make(map[string]struct{})
	for contractID, coverage := range s.contracts {
		if len(coverage.LifecycleEvents) > 0 && len(coverage.PolicyEvents) == 0 && len(coverage.ObligationEvents) == 0 && len(coverage.GateEvents) == 0 && len(coverage.GuardFrictionCounts) == 0 {
			set[contractID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func incrementContractMap(coverage *ContractEventCoverage, kind string, key string) {
	switch kind {
	case "lifecycle":
		incrementStringMap(&coverage.LifecycleEvents, key)
	case "policy":
		incrementStringMap(&coverage.PolicyEvents, key)
	case "obligation":
		incrementStringMap(&coverage.ObligationEvents, key)
	case "gate":
		incrementStringMap(&coverage.GateEvents, key)
	}
}

func incrementStringMap(target *map[string]int, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if *target == nil {
		*target = make(map[string]int)
	}
	(*target)[key]++
}

func guardFrictionAvoidable(avoidability GuardFrictionAvoidability) bool {
	switch avoidability {
	case GuardFrictionAvoidabilityContractNotVisible,
		GuardFrictionAvoidabilityContradictoryContract,
		GuardFrictionAvoidabilityToolSurfaceGap,
		GuardFrictionAvoidabilityParserSurfaceGap,
		GuardFrictionAvoidabilityRuntimeStateGap,
		GuardFrictionAvoidabilityEvidenceTimingGap:
		return true
	default:
		return false
	}
}

func guardFrictionUnclassified(event Event, friction ContractGuardFrictionEvent) bool {
	return friction.Avoidability == "" ||
		friction.Avoidability == GuardFrictionAvoidabilityUnknown ||
		firstNonEmptyAuditValue(friction.ContractID, event.ContractID) == "" ||
		friction.InvariantID == "" ||
		friction.Decision == "" ||
		friction.GuardSurface == "" ||
		(friction.ModelContractVisible && friction.ProtocolHash == "")
}

type gateAuditFieldSnapshot struct {
	PackID                       string
	EnforcementMode              string
	ObservedFacts                []string
	SafeAlternatives             []string
	PackIDFieldPresent           bool
	EnforcementModeFieldPresent  bool
	ObservedFactsFieldPresent    bool
	SafeAlternativesFieldPresent bool
}

func gateAuditExtendedFields(gate *ContractGateEvent) gateAuditFieldSnapshot {
	if gate == nil {
		return gateAuditFieldSnapshot{}
	}
	value := reflect.ValueOf(gate)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return gateAuditFieldSnapshot{}
	}
	elem := value.Elem()
	return gateAuditFieldSnapshot{
		PackID:                       stringFieldByName(elem, "PackID"),
		EnforcementMode:              stringFieldByName(elem, "EnforcementMode"),
		ObservedFacts:                stringSliceFieldByName(elem, "ObservedFacts"),
		SafeAlternatives:             stringSliceFieldByName(elem, "SafeAlternatives"),
		PackIDFieldPresent:           elem.FieldByName("PackID").IsValid(),
		EnforcementModeFieldPresent:  elem.FieldByName("EnforcementMode").IsValid(),
		ObservedFactsFieldPresent:    elem.FieldByName("ObservedFacts").IsValid(),
		SafeAlternativesFieldPresent: elem.FieldByName("SafeAlternatives").IsValid(),
	}
}

func gateDecisionNeedsSafeAlternatives(decision ToolPolicyDecision, enforcementMode string) bool {
	if !strings.EqualFold(strings.TrimSpace(enforcementMode), "enforce") {
		return false
	}
	switch decision {
	case PolicyReject, PolicyDeny, PolicyStop:
		return true
	default:
		return false
	}
}

func stringFieldByName(value reflect.Value, fieldName string) string {
	field := value.FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(field.String())
}

func stringSliceFieldByName(value reflect.Value, fieldName string) []string {
	field := value.FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.Slice || field.Type().Elem().Kind() != reflect.String {
		return nil
	}
	values := make([]string, 0, field.Len())
	for i := 0; i < field.Len(); i++ {
		values = append(values, field.Index(i).String())
	}
	return compactStrings(values)
}

func auditRef(event Event, reference string, reason string) EventAuditRef {
	return EventAuditRef{
		EventID:    event.EventID,
		Type:       string(event.Type),
		Sequence:   event.Sequence,
		ContractID: event.ContractID,
		Reference:  strings.TrimSpace(reference),
		Reason:     reason,
	}
}

func obligationKey(contractID, obligationID string) string {
	return strings.TrimSpace(contractID) + "\x00" + strings.TrimSpace(obligationID)
}

func sequenceAfter(sequence, staleAfter int64) bool {
	if staleAfter <= 0 {
		return false
	}
	if sequence <= 0 {
		return true
	}
	return sequence > staleAfter
}

func isPolicyToolEvent(eventType EventType) bool {
	switch eventType {
	case EventToolIntentClassified, EventPathRolesResolved, EventToolPolicyDecision, EventToolPolicyResult:
		return true
	default:
		return false
	}
}

func turnPhaseActionClassForTool(tool string) string {
	switch strings.TrimSpace(tool) {
	case "read_file", "code_navigate", "go_read_symbol", "go_extract_body", "cpp_read_symbol", "cpp_definition":
		return "read"
	case "search_files", "search_content", "rg", "grep", "discovery_query", "context_pack", "context_query", "context_zoom":
		return "search"
	case "submit_plan":
		return "submit_plan"
	case "edit_file", "write_file", "append_file", "mkdir", "apply_patch":
		return "per_edit_write"
	case "shell", "source_sanity", "go_diagnostics", "cpp_diagnostics":
		return "verify"
	default:
		return ""
	}
}

func turnPhaseEvidenceOpen(state PhaseEvidenceStateEvent) bool {
	verificationStatus := strings.ToLower(strings.TrimSpace(state.VerificationStatus))
	return len(state.TDDObligationRefs) > 0 ||
		len(state.ResidualBlockers) > 0 ||
		len(state.RecoveryPacketRefs) > 0 ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "recovery_open") ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "incomplete") ||
		strings.EqualFold(strings.TrimSpace(state.ProductWorkflowState), "skipped") ||
		verificationStatus == "missing" ||
		verificationStatus == "failed" ||
		verificationStatus == "pre_write"
}

func stringSetContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func firstNonEmptyAuditValue(values ...string) string {
	return util.FirstNonEmpty(values...)
}

func removeString(values []string, remove string) []string {
	remove = strings.TrimSpace(remove)
	if remove == "" || len(values) == 0 {
		return values
	}
	out := values[:0]
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		if strings.TrimSpace(key) != "" {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

func sortedMapKeys(values map[string]int) []string {
	out := make([]string, 0, len(values))
	for key, count := range values {
		if strings.TrimSpace(key) != "" && count > 0 {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

func sortedContractCoverages(values map[string]*ContractEventCoverage) []ContractEventCoverage {
	out := make([]ContractEventCoverage, 0, len(values))
	for _, coverage := range values {
		out = append(out, *coverage)
	}
	slices.SortFunc(out, func(a, b ContractEventCoverage) int {
		return cmp.Compare(a.ContractID, b.ContractID)
	})
	return out
}

func sortedGuardFrictionContractCoverages(values map[string]*GuardFrictionContractCoverage) []GuardFrictionContractCoverage {
	out := make([]GuardFrictionContractCoverage, 0, len(values))
	for _, coverage := range values {
		out = append(out, *coverage)
	}
	slices.SortFunc(out, func(a, b GuardFrictionContractCoverage) int {
		if c := cmp.Compare(b.AvoidableFriction, a.AvoidableFriction); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Total, a.Total); c != 0 {
			return c
		}
		return cmp.Compare(a.ContractID, b.ContractID)
	})
	const maxTopGuardFrictionContracts = 10
	if len(out) > maxTopGuardFrictionContracts {
		out = out[:maxTopGuardFrictionContracts]
	}
	return out
}

func sortAuditRefs(refs []EventAuditRef) {
	slices.SortFunc(refs, func(a, b EventAuditRef) int {
		if c := cmp.Compare(a.Sequence, b.Sequence); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ContractID, b.ContractID); c != 0 {
			return c
		}
		return cmp.Compare(a.EventID, b.EventID)
	})
}

func sortObligationRefs(refs []ObligationAuditRef) {
	slices.SortFunc(refs, func(a, b ObligationAuditRef) int {
		if c := cmp.Compare(a.Sequence, b.Sequence); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ContractID, b.ContractID); c != 0 {
			return c
		}
		return cmp.Compare(a.ObligationID, b.ObligationID)
	})
}

func sortSidecarIssues(issues []SidecarToolCorrelationIssue) {
	slices.SortFunc(issues, func(a, b SidecarToolCorrelationIssue) int {
		if c := cmp.Compare(a.Sequence, b.Sequence); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ContractID, b.ContractID); c != 0 {
			return c
		}
		return cmp.Compare(a.EventID, b.EventID)
	})
}

func sortTurnPhaseContradictions(issues []TurnPhaseContradiction) {
	slices.SortFunc(issues, func(a, b TurnPhaseContradiction) int {
		if c := cmp.Compare(a.Sequence, b.Sequence); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ContractID, b.ContractID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.PhaseID, b.PhaseID); c != 0 {
			return c
		}
		return cmp.Compare(a.EventID, b.EventID)
	})
}

func eventIDsFromAuditRefs(refs []EventAuditRef) []string {
	set := make(map[string]struct{})
	for _, ref := range refs {
		if ref.EventID != "" {
			set[ref.EventID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func contractIDsFromAuditRefs(refs []EventAuditRef) []string {
	set := make(map[string]struct{})
	for _, ref := range refs {
		if ref.ContractID != "" {
			set[ref.ContractID] = struct{}{}
		}
		if ref.Reference != "" {
			set[ref.Reference] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func eventIDsFromSidecarIssues(issues []SidecarToolCorrelationIssue) []string {
	set := make(map[string]struct{})
	for _, issue := range issues {
		if issue.EventID != "" {
			set[issue.EventID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func contractIDsFromSidecarIssues(issues []SidecarToolCorrelationIssue) []string {
	set := make(map[string]struct{})
	for _, issue := range issues {
		if issue.ContractID != "" {
			set[issue.ContractID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func eventIDsFromTurnPhaseContradictions(issues []TurnPhaseContradiction) []string {
	set := make(map[string]struct{})
	for _, issue := range issues {
		if issue.EventID != "" {
			set[issue.EventID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func contractIDsFromTurnPhaseContradictions(issues []TurnPhaseContradiction) []string {
	set := make(map[string]struct{})
	for _, issue := range issues {
		if issue.ContractID != "" {
			set[issue.ContractID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func eventIDsFromObligationRefs(refs []ObligationAuditRef) []string {
	set := make(map[string]struct{})
	for _, ref := range refs {
		if ref.EventID != "" {
			set[ref.EventID] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func contractIDsFromObligationRefs(refs []ObligationAuditRef) []string {
	set := make(map[string]struct{})
	for _, ref := range refs {
		if ref.ContractID != "" {
			set[ref.ContractID] = struct{}{}
		}
	}
	return sortedKeys(set)
}
