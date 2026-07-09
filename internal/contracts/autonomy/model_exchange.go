package autonomy

import "strings"

const (
	ContractEnvelopeSchemaVersion     = "idiom.contract_envelope.v1"
	ContextPacketSchemaVersion        = "idiom.context_packet.v1"
	ModelResponseSchemaVersion        = "idiom.model_response.v1"
	PermissionSnapshotSchemaVersion   = "idiom.permission_snapshot.v1"
	ValidationSignalSchemaVersion     = "idiom.validation_signal.v1"
	FailureDeltaSchemaVersion         = "idiom.failure_delta.v1"
	TraceStorePolicySchemaVersion     = "idiom.trace_store_policy.v1"
	ReplayClassificationSchemaVersion = "idiom.replay_classification.v1"
	PhaseGatePolicySchemaVersion      = "idiom.phase_gate_policy.v1"
	RollbackControlSchemaVersion      = "idiom.rollback_control.v1"
)

type ModelResponseKind string

const (
	ModelResponseProposeAction     ModelResponseKind = "propose_action"
	ModelResponseRequestContext    ModelResponseKind = "request_context"
	ModelResponseChallengeContext  ModelResponseKind = "challenge_context"
	ModelResponseProposeValidation ModelResponseKind = "propose_validation"
	ModelResponseEscalate          ModelResponseKind = "escalate"
)

type ContextPacket struct {
	SchemaVersion    string   `json:"schema_version"`
	PacketID         string   `json:"packet_id,omitempty"`
	ContractID       string   `json:"contract_id,omitempty"`
	ProtocolHash     string   `json:"protocol_hash,omitempty"`
	SummaryRefs      []string `json:"summary_refs,omitempty"`
	TargetRefs       []string `json:"target_refs,omitempty"`
	PathRefs         []string `json:"path_refs,omitempty"`
	SymbolRefs       []string `json:"symbol_refs,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	ObligationRefs   []string `json:"obligation_refs,omitempty"`
	SchemaNames      []string `json:"schema_names,omitempty"`
	RedactionMarkers []string `json:"redaction_markers,omitempty"`
}

type ModelResponse struct {
	SchemaVersion      string              `json:"schema_version"`
	ResponseID         string              `json:"response_id,omitempty"`
	ContractID         string              `json:"contract_id"`
	ProtocolHash       string              `json:"protocol_hash"`
	Kind               ModelResponseKind   `json:"kind"`
	ProposedAction     *ProposedAction     `json:"proposed_action,omitempty"`
	ContextRequest     *ContextRequest     `json:"context_request,omitempty"`
	ContextChallenge   *ContextChallenge   `json:"context_challenge,omitempty"`
	ValidationProposal *ValidationProposal `json:"validation_proposal,omitempty"`
	Escalation         *Escalation         `json:"escalation,omitempty"`
}

type ProposedAction struct {
	ActionClass  string   `json:"action_class,omitempty"`
	ToolName     string   `json:"tool_name,omitempty"`
	TargetRefs   []string `json:"target_refs,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	ProposalRefs []string `json:"proposal_refs,omitempty"`
	ReasonCode   string   `json:"reason_code,omitempty"`
}

type ContextRequest struct {
	RequestRefs    []string `json:"request_refs,omitempty"`
	MissingRefs    []string `json:"missing_refs,omitempty"`
	ReasonCode     string   `json:"reason_code,omitempty"`
	RequestedShape string   `json:"requested_shape,omitempty"`
}

type ContextChallenge struct {
	ChallengeRefs []string `json:"challenge_refs,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	ReasonCode    string   `json:"reason_code,omitempty"`
	ProposedShape string   `json:"proposed_shape,omitempty"`
}

type ValidationProposal struct {
	ValidationRefs []string `json:"validation_refs,omitempty"`
	CommandRefs    []string `json:"command_refs,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	ReasonCode     string   `json:"reason_code,omitempty"`
}

type Escalation struct {
	EscalationRefs []string `json:"escalation_refs,omitempty"`
	BlockerRefs    []string `json:"blocker_refs,omitempty"`
	QuestionRefs   []string `json:"question_refs,omitempty"`
	ReasonCode     string   `json:"reason_code,omitempty"`
}

type PermissionSnapshot struct {
	SchemaVersion     string   `json:"schema_version"`
	SnapshotID        string   `json:"snapshot_id,omitempty"`
	ContractID        string   `json:"contract_id"`
	ProtocolHash      string   `json:"protocol_hash,omitempty"`
	PermissionProfile string   `json:"permission_profile,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	HiddenTools       []string `json:"hidden_tools,omitempty"`
	CapabilityRefs    []string `json:"capability_refs,omitempty"`
	PathRoleRefs      []string `json:"path_role_refs,omitempty"`
	CapturedBy        string   `json:"captured_by,omitempty"`
}

type ValidationSignal struct {
	SchemaVersion string   `json:"schema_version"`
	SignalID      string   `json:"signal_id,omitempty"`
	ContractID    string   `json:"contract_id"`
	ProtocolHash  string   `json:"protocol_hash,omitempty"`
	ValidationRef string   `json:"validation_ref,omitempty"`
	Status        string   `json:"status,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	CommandRefs   []string `json:"command_refs,omitempty"`
	CapturedBy    string   `json:"captured_by,omitempty"`
}

type FailureDelta struct {
	SchemaVersion string   `json:"schema_version"`
	DeltaID       string   `json:"delta_id,omitempty"`
	ContractID    string   `json:"contract_id"`
	ProtocolHash  string   `json:"protocol_hash,omitempty"`
	FailureClass  string   `json:"failure_class,omitempty"`
	BeforeRef     string   `json:"before_ref,omitempty"`
	AfterRef      string   `json:"after_ref,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	GeneratedBy   string   `json:"generated_by"`
}

type TraceStorePolicy struct {
	SchemaVersion   string   `json:"schema_version"`
	PolicyID        string   `json:"policy_id,omitempty"`
	ContractID      string   `json:"contract_id,omitempty"`
	ProtocolHash    string   `json:"protocol_hash,omitempty"`
	RetentionRef    string   `json:"retention_ref,omitempty"`
	RedactionRef    string   `json:"redaction_ref,omitempty"`
	RedactionStatus string   `json:"redaction_status,omitempty"`
	AllowedFields   []string `json:"allowed_fields,omitempty"`
	ForbiddenFields []string `json:"forbidden_fields,omitempty"`
	StoreRefs       []string `json:"store_refs,omitempty"`
	CapturedBy      string   `json:"captured_by,omitempty"`
}

type ReplayClassificationRecord struct {
	SchemaVersion    string   `json:"schema_version"`
	ClassificationID string   `json:"classification_id,omitempty"`
	ContractID       string   `json:"contract_id"`
	ProtocolHash     string   `json:"protocol_hash,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
	AtomRefs         []string `json:"atom_refs,omitempty"`
	ViolationRefs    []string `json:"violation_refs,omitempty"`
	ClassifiedBy     string   `json:"classified_by,omitempty"`
}

type PhaseGatePolicy struct {
	SchemaVersion         string   `json:"schema_version"`
	PolicyID              string   `json:"policy_id,omitempty"`
	ContractID            string   `json:"contract_id"`
	ProtocolHash          string   `json:"protocol_hash,omitempty"`
	PhaseID               string   `json:"phase_id,omitempty"`
	GateRefs              []string `json:"gate_refs,omitempty"`
	EnforcementMode       string   `json:"enforcement_mode,omitempty"`
	TerminalResponseKinds []string `json:"terminal_response_kinds,omitempty"`
	CapturedBy            string   `json:"captured_by,omitempty"`
}

type RollbackControl struct {
	SchemaVersion   string   `json:"schema_version"`
	ControlID       string   `json:"control_id,omitempty"`
	ContractID      string   `json:"contract_id"`
	ProtocolHash    string   `json:"protocol_hash,omitempty"`
	RollbackRef     string   `json:"rollback_ref,omitempty"`
	Decision        string   `json:"decision,omitempty"`
	OperatorRef     string   `json:"operator_ref,omitempty"`
	Source          string   `json:"source,omitempty"`
	PreviousMode    string   `json:"previous_mode,omitempty"`
	NewMode         string   `json:"new_mode,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	GuardRefs       []string `json:"guard_refs,omitempty"`
	AffectedPackIDs []string `json:"affected_pack_ids,omitempty"`
	AuthorizedBy    string   `json:"authorized_by,omitempty"`
}

type ContractModelExchangeEvent struct {
	Kind                 string                      `json:"kind"`
	ContractID           string                      `json:"contract_id,omitempty"`
	ProtocolHash         string                      `json:"protocol_hash,omitempty"`
	PhaseID              string                      `json:"phase_id,omitempty"`
	ModelCallID          string                      `json:"model_call_id,omitempty"`
	InvariantID          string                      `json:"invariant_id,omitempty"`
	SourceEventID        string                      `json:"source_event_id,omitempty"`
	SourceEventType      string                      `json:"source_event_type,omitempty"`
	ContextPacket        *ContextPacket              `json:"context_packet,omitempty"`
	PermissionSnapshot   *PermissionSnapshot         `json:"permission_snapshot,omitempty"`
	FailureDelta         *FailureDelta               `json:"failure_delta,omitempty"`
	ModelResponse        *ModelResponse              `json:"model_response,omitempty"`
	ValidationSignal     *ValidationSignal           `json:"validation_signal,omitempty"`
	TraceStorePolicy     *TraceStorePolicy           `json:"trace_store_policy,omitempty"`
	ReplayClassification *ReplayClassificationRecord `json:"replay_classification,omitempty"`
	PhaseGatePolicy      *PhaseGatePolicy            `json:"phase_gate_policy,omitempty"`
	RollbackControl      *RollbackControl            `json:"rollback_control,omitempty"`
}

type ModelExchangeValidationError struct {
	Problems []string
}

func (e ModelExchangeValidationError) Error() string {
	return "invalid model exchange: " + strings.Join(e.Problems, "; ")
}

func (p *ContextPacket) Normalize() {
	p.SchemaVersion = firstModelExchangeValue(p.SchemaVersion, ContextPacketSchemaVersion)
	p.PacketID = strings.TrimSpace(p.PacketID)
	p.ContractID = strings.TrimSpace(p.ContractID)
	p.ProtocolHash = strings.TrimSpace(p.ProtocolHash)
	p.SummaryRefs = compactStrings(p.SummaryRefs)
	p.TargetRefs = compactStrings(p.TargetRefs)
	p.PathRefs = compactStrings(p.PathRefs)
	p.SymbolRefs = compactStrings(p.SymbolRefs)
	p.EvidenceRefs = compactStrings(p.EvidenceRefs)
	p.ObligationRefs = compactStrings(p.ObligationRefs)
	p.SchemaNames = compactStrings(p.SchemaNames)
	p.RedactionMarkers = compactStrings(p.RedactionMarkers)
}

func (p ContextPacket) Validate() error {
	p.Normalize()

	var problems validationProblems

	validateModelExchangeSchemaID("context_packet.schema_version", p.SchemaVersion, ContextPacketSchemaVersion, &problems)
	validateOptionalModelExchangeContractRefs(p.ContractID, p.ProtocolHash, &problems)
	validateModelExchangeRedactionMarkers("context_packet", contextPacketValues(p), &problems)

	if problems.Any() {
		return ModelExchangeValidationError{Problems: problems.Messages()}
	}

	return nil
}

func (r *ModelResponse) Normalize() {
	r.SchemaVersion = firstModelExchangeValue(r.SchemaVersion, ModelResponseSchemaVersion)
	r.ResponseID = strings.TrimSpace(r.ResponseID)
	r.ContractID = strings.TrimSpace(r.ContractID)
	r.ProtocolHash = strings.TrimSpace(r.ProtocolHash)

	if r.ProposedAction != nil {
		r.ProposedAction.normalize()
	}

	if r.ContextRequest != nil {
		r.ContextRequest.normalize()
	}

	if r.ContextChallenge != nil {
		r.ContextChallenge.normalize()
	}

	if r.ValidationProposal != nil {
		r.ValidationProposal.normalize()
	}

	if r.Escalation != nil {
		r.Escalation.normalize()
	}
}

func (r ModelResponse) Validate() error {
	r.Normalize()

	var problems validationProblems

	validateModelExchangeSchemaID("model_response.schema_version", r.SchemaVersion, ModelResponseSchemaVersion, &problems)
	validateModelExchangeContractRefs(r.ContractID, r.ProtocolHash, &problems)
	validateModelResponsePayloadPresence(r, &problems)
	validateModelExchangeRedactionMarkers("model_response", modelResponseValues(r), &problems)

	if problems.Any() {
		return ModelExchangeValidationError{Problems: problems.Messages()}
	}

	return nil
}

func (s *PermissionSnapshot) Normalize() {
	s.SchemaVersion = firstModelExchangeValue(s.SchemaVersion, PermissionSnapshotSchemaVersion)
	s.SnapshotID = strings.TrimSpace(s.SnapshotID)
	s.ContractID = strings.TrimSpace(s.ContractID)
	s.ProtocolHash = strings.TrimSpace(s.ProtocolHash)
	s.PermissionProfile = strings.TrimSpace(s.PermissionProfile)
	s.AllowedTools = compactStrings(s.AllowedTools)
	s.HiddenTools = compactStrings(s.HiddenTools)
	s.CapabilityRefs = compactStrings(s.CapabilityRefs)
	s.PathRoleRefs = compactStrings(s.PathRoleRefs)
	s.CapturedBy = strings.TrimSpace(s.CapturedBy)
}

func (s PermissionSnapshot) Validate() error {
	s.Normalize()

	return validateHarnessAuthorityRecord(PermissionSnapshotSchemaVersion, s.SchemaVersion, s.ContractID, s.ProtocolHash, permissionSnapshotValues(s), "permission_snapshot.captured_by", s.CapturedBy)
}

func (s *ValidationSignal) Normalize() {
	s.SchemaVersion = firstModelExchangeValue(s.SchemaVersion, ValidationSignalSchemaVersion)
	s.SignalID = strings.TrimSpace(s.SignalID)
	s.ContractID = strings.TrimSpace(s.ContractID)
	s.ProtocolHash = strings.TrimSpace(s.ProtocolHash)
	s.ValidationRef = strings.TrimSpace(s.ValidationRef)
	s.Status = strings.TrimSpace(s.Status)
	s.EvidenceRefs = compactStrings(s.EvidenceRefs)
	s.CommandRefs = compactStrings(s.CommandRefs)
	s.CapturedBy = strings.TrimSpace(s.CapturedBy)
}

func (s ValidationSignal) Validate() error {
	s.Normalize()

	return validateHarnessAuthorityRecord(ValidationSignalSchemaVersion, s.SchemaVersion, s.ContractID, s.ProtocolHash, validationSignalValues(s), "validation_signal.captured_by", s.CapturedBy)
}

func (d *FailureDelta) Normalize() {
	d.SchemaVersion = firstModelExchangeValue(d.SchemaVersion, FailureDeltaSchemaVersion)
	d.DeltaID = strings.TrimSpace(d.DeltaID)
	d.ContractID = strings.TrimSpace(d.ContractID)
	d.ProtocolHash = strings.TrimSpace(d.ProtocolHash)
	d.FailureClass = strings.TrimSpace(d.FailureClass)
	d.BeforeRef = strings.TrimSpace(d.BeforeRef)
	d.AfterRef = strings.TrimSpace(d.AfterRef)
	d.EvidenceRefs = compactStrings(d.EvidenceRefs)
	d.GeneratedBy = strings.TrimSpace(d.GeneratedBy)
}

func (d FailureDelta) Validate() error {
	d.Normalize()

	return validateHarnessAuthorityRecord(FailureDeltaSchemaVersion, d.SchemaVersion, d.ContractID, d.ProtocolHash, failureDeltaValues(d), "failure_delta.generated_by", d.GeneratedBy)
}

func (p *TraceStorePolicy) Normalize() {
	p.SchemaVersion = firstModelExchangeValue(p.SchemaVersion, TraceStorePolicySchemaVersion)
	p.PolicyID = strings.TrimSpace(p.PolicyID)
	p.ContractID = strings.TrimSpace(p.ContractID)
	p.ProtocolHash = strings.TrimSpace(p.ProtocolHash)
	p.RetentionRef = strings.TrimSpace(p.RetentionRef)
	p.RedactionRef = strings.TrimSpace(p.RedactionRef)
	p.RedactionStatus = strings.TrimSpace(p.RedactionStatus)
	p.AllowedFields = compactStrings(p.AllowedFields)
	p.ForbiddenFields = compactStrings(p.ForbiddenFields)
	p.StoreRefs = compactStrings(p.StoreRefs)
	p.CapturedBy = strings.TrimSpace(p.CapturedBy)
}

func (p TraceStorePolicy) Validate() error {
	p.Normalize()

	return validateHarnessAuthorityRecord(TraceStorePolicySchemaVersion, p.SchemaVersion, p.ContractID, p.ProtocolHash, traceStorePolicyValues(p), "trace_store_policy.captured_by", p.CapturedBy)
}

func (c *ReplayClassificationRecord) Normalize() {
	c.SchemaVersion = firstModelExchangeValue(c.SchemaVersion, ReplayClassificationSchemaVersion)
	c.ClassificationID = strings.TrimSpace(c.ClassificationID)
	c.ContractID = strings.TrimSpace(c.ContractID)
	c.ProtocolHash = strings.TrimSpace(c.ProtocolHash)
	c.Outcome = strings.TrimSpace(c.Outcome)
	c.AtomRefs = compactStrings(c.AtomRefs)
	c.ViolationRefs = compactStrings(c.ViolationRefs)
	c.ClassifiedBy = strings.TrimSpace(c.ClassifiedBy)
}

func (c ReplayClassificationRecord) Validate() error {
	c.Normalize()

	return validateHarnessAuthorityRecord(ReplayClassificationSchemaVersion, c.SchemaVersion, c.ContractID, c.ProtocolHash, replayClassificationValues(c), "replay_classification.classified_by", c.ClassifiedBy)
}

func (p *PhaseGatePolicy) Normalize() {
	p.SchemaVersion = firstModelExchangeValue(p.SchemaVersion, PhaseGatePolicySchemaVersion)
	p.PolicyID = strings.TrimSpace(p.PolicyID)
	p.ContractID = strings.TrimSpace(p.ContractID)
	p.ProtocolHash = strings.TrimSpace(p.ProtocolHash)
	p.PhaseID = strings.TrimSpace(p.PhaseID)
	p.GateRefs = compactStrings(p.GateRefs)
	p.EnforcementMode = strings.TrimSpace(p.EnforcementMode)
	p.TerminalResponseKinds = compactStrings(p.TerminalResponseKinds)
	p.CapturedBy = strings.TrimSpace(p.CapturedBy)
}

func (p PhaseGatePolicy) Validate() error {
	p.Normalize()

	return validateHarnessAuthorityRecord(PhaseGatePolicySchemaVersion, p.SchemaVersion, p.ContractID, p.ProtocolHash, phaseGatePolicyValues(p), "phase_gate_policy.captured_by", p.CapturedBy)
}

func (c *RollbackControl) Normalize() {
	c.SchemaVersion = firstModelExchangeValue(c.SchemaVersion, RollbackControlSchemaVersion)
	c.ControlID = strings.TrimSpace(c.ControlID)
	c.ContractID = strings.TrimSpace(c.ContractID)
	c.ProtocolHash = strings.TrimSpace(c.ProtocolHash)
	c.RollbackRef = strings.TrimSpace(c.RollbackRef)
	c.Decision = strings.TrimSpace(c.Decision)
	c.OperatorRef = strings.TrimSpace(c.OperatorRef)
	c.Source = strings.TrimSpace(c.Source)
	c.PreviousMode = strings.TrimSpace(c.PreviousMode)
	c.NewMode = strings.TrimSpace(c.NewMode)
	c.Reason = strings.TrimSpace(c.Reason)
	c.GuardRefs = compactStrings(c.GuardRefs)
	c.AffectedPackIDs = compactStrings(c.AffectedPackIDs)
	c.AuthorizedBy = strings.TrimSpace(c.AuthorizedBy)
}

func (c RollbackControl) Validate() error {
	c.Normalize()

	return validateHarnessAuthorityRecord(RollbackControlSchemaVersion, c.SchemaVersion, c.ContractID, c.ProtocolHash, rollbackControlValues(c), "rollback_control.authorized_by", c.AuthorizedBy)
}

func (e *ContractModelExchangeEvent) Normalize() {
	e.Kind = strings.TrimSpace(e.Kind)
	e.ContractID = strings.TrimSpace(e.ContractID)
	e.ProtocolHash = strings.TrimSpace(e.ProtocolHash)
	e.PhaseID = strings.TrimSpace(e.PhaseID)
	e.ModelCallID = strings.TrimSpace(e.ModelCallID)
	e.InvariantID = strings.TrimSpace(e.InvariantID)
	e.SourceEventID = strings.TrimSpace(e.SourceEventID)
	e.SourceEventType = strings.TrimSpace(e.SourceEventType)

	if e.ContextPacket != nil {
		e.ContextPacket.Normalize()
	}

	if e.PermissionSnapshot != nil {
		e.PermissionSnapshot.Normalize()
	}

	if e.FailureDelta != nil {
		e.FailureDelta.Normalize()
	}

	if e.ModelResponse != nil {
		e.ModelResponse.Normalize()
	}

	if e.ValidationSignal != nil {
		e.ValidationSignal.Normalize()
	}

	if e.TraceStorePolicy != nil {
		e.TraceStorePolicy.Normalize()
	}

	if e.ReplayClassification != nil {
		e.ReplayClassification.Normalize()
	}

	if e.PhaseGatePolicy != nil {
		e.PhaseGatePolicy.Normalize()
	}

	if e.RollbackControl != nil {
		e.RollbackControl.Normalize()
	}
}

func (e ContractModelExchangeEvent) Validate() error {
	e.Normalize()

	var problems validationProblems

	e.validate("", &problems)

	if problems.Any() {
		return ModelExchangeValidationError{Problems: problems.Messages()}
	}

	return nil
}

func validateModelExchangeSchemaID(field, got, want string, problems *validationProblems) {
	if strings.TrimSpace(got) != want {
		problems.Addf("%s must be %s", field, want)
	}
}

func validateModelExchangeContractRefs(contractID, protocolHash string, problems *validationProblems) {
	if strings.TrimSpace(contractID) == "" {
		problems.Add("contract_id is required")
	}

	if strings.TrimSpace(protocolHash) == "" {
		problems.Add("protocol_hash is required")
	}

	validateOptionalModelExchangeContractRefs(contractID, protocolHash, problems)
}

func validateOptionalModelExchangeContractRefs(contractID, protocolHash string, problems *validationProblems) {
	if contractID != "" && !validContractLabel(contractID, 128) {
		problems.Add("contract_id must be a compact contract reference")
	}

	if protocolHash != "" && !validContractLabel(protocolHash, 128) {
		problems.Add("protocol_hash must be a compact protocol reference")
	}
}

func validateModelResponsePayloadPresence(response ModelResponse, problems *validationProblems) {
	payloads := 0
	if response.ProposedAction != nil {
		payloads++
	}

	if response.ContextRequest != nil {
		payloads++
	}

	if response.ContextChallenge != nil {
		payloads++
	}

	if response.ValidationProposal != nil {
		payloads++
	}

	if response.Escalation != nil {
		payloads++
	}

	if payloads != 1 {
		problems.Add("exactly one model response payload is required")
	}

	switch response.Kind {
	case ModelResponseProposeAction:
		if response.ProposedAction == nil {
			problems.Add("proposed_action payload is required for propose_action")
		}
	case ModelResponseRequestContext:
		if response.ContextRequest == nil {
			problems.Add("context_request payload is required for request_context")
		}
	case ModelResponseChallengeContext:
		if response.ContextChallenge == nil {
			problems.Add("context_challenge payload is required for challenge_context")
		}
	case ModelResponseProposeValidation:
		if response.ValidationProposal == nil {
			problems.Add("validation_proposal payload is required for propose_validation")
		}
	case ModelResponseEscalate:
		if response.Escalation == nil {
			problems.Add("escalation payload is required for escalate")
		}
	default:
		problems.Add("kind must be a known model-authored response variant")
	}
}

func validateModelExchangeRedactionMarkers(field string, values []string, problems *validationProblems) {
	for _, value := range values {
		if looksLikeRawGuardFrictionContent(value) {
			problems.Add(field + " must contain compact redaction-safe metadata only")
			return
		}
	}
}

func validateHarnessAuthorityRecord(wantSchema, gotSchema, contractID, protocolHash string, values []string, provenanceField, provenance string) error {
	var problems validationProblems

	validateModelExchangeSchemaID("schema_version", gotSchema, wantSchema, &problems)
	validateModelExchangeContractRefs(contractID, protocolHash, &problems)
	validateModelExchangeRedactionMarkers("harness authority record", values, &problems)

	if strings.TrimSpace(provenance) != "harness" {
		problems.Add(provenanceField + " must be harness")
	}

	if problems.Any() {
		return ModelExchangeValidationError{Problems: problems.Messages()}
	}

	return nil
}

func (e ContractModelExchangeEvent) validate(eventContractID string, problems *validationProblems) {
	if e.Kind == "" {
		problems.Add("model_exchange.kind is required")
	}

	validateModelExchangeContractRefs(e.ContractID, e.ProtocolHash, problems)

	if eventContractID != "" && e.ContractID != eventContractID {
		problems.Add("model_exchange.contract_id must match event contract_id")
	}

	payloads := 0
	if e.ContextPacket != nil {
		payloads++
	}

	if e.PermissionSnapshot != nil {
		payloads++
	}

	if e.FailureDelta != nil {
		payloads++
	}

	if e.ModelResponse != nil {
		payloads++
	}

	if e.ValidationSignal != nil {
		payloads++
	}

	if e.TraceStorePolicy != nil {
		payloads++
	}

	if e.ReplayClassification != nil {
		payloads++
	}

	if e.PhaseGatePolicy != nil {
		payloads++
	}

	if e.RollbackControl != nil {
		payloads++
	}

	if payloads != 1 {
		problems.Add("exactly one model_exchange payload is required")
	}

	e.validateNestedModelExchangeRefs(problems)
	e.validateNestedModelExchangePayload(problems)
}

func (e ContractModelExchangeEvent) validateNestedModelExchangeRefs(problems *validationProblems) {
	for _, refs := range []modelExchangeRefs{
		modelExchangeContextPacketRefs(e.ContextPacket),
		modelExchangePermissionSnapshotRefs(e.PermissionSnapshot),
		modelExchangeFailureDeltaRefs(e.FailureDelta),
		modelExchangeModelResponseRefs(e.ModelResponse),
		modelExchangeValidationSignalRefs(e.ValidationSignal),
		modelExchangeTraceStorePolicyRefs(e.TraceStorePolicy),
		modelExchangeReplayClassificationRefs(e.ReplayClassification),
		modelExchangePhaseGatePolicyRefs(e.PhaseGatePolicy),
		modelExchangeRollbackControlRefs(e.RollbackControl),
	} {
		if refs.name == "" {
			continue
		}

		if refs.contractID != "" && refs.contractID != e.ContractID {
			problems.Add(refs.name + ".contract_id must match model_exchange.contract_id")
		}

		if refs.protocolHash != "" && refs.protocolHash != e.ProtocolHash {
			problems.Add(refs.name + ".protocol_hash must match model_exchange.protocol_hash")
		}
	}
}

func (e ContractModelExchangeEvent) validateNestedModelExchangePayload(problems *validationProblems) {
	errs := []error{}
	if e.ContextPacket != nil {
		errs = append(errs, e.ContextPacket.Validate())
	}

	if e.PermissionSnapshot != nil {
		errs = append(errs, e.PermissionSnapshot.Validate())
	}

	if e.FailureDelta != nil {
		errs = append(errs, e.FailureDelta.Validate())
	}

	if e.ModelResponse != nil {
		errs = append(errs, e.ModelResponse.Validate())
	}

	if e.ValidationSignal != nil {
		errs = append(errs, e.ValidationSignal.Validate())
	}

	if e.TraceStorePolicy != nil {
		errs = append(errs, e.TraceStorePolicy.Validate())
	}

	if e.ReplayClassification != nil {
		errs = append(errs, e.ReplayClassification.Validate())
	}

	if e.PhaseGatePolicy != nil {
		errs = append(errs, e.PhaseGatePolicy.Validate())
	}

	if e.RollbackControl != nil {
		errs = append(errs, e.RollbackControl.Validate())
	}

	for _, err := range errs {
		problems.AddError(err)
	}
}

type modelExchangeRefs struct {
	name         string
	contractID   string
	protocolHash string
}

func modelExchangeContextPacketRefs(value *ContextPacket) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "context_packet", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangePermissionSnapshotRefs(value *PermissionSnapshot) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "permission_snapshot", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeFailureDeltaRefs(value *FailureDelta) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "failure_delta", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeModelResponseRefs(value *ModelResponse) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "model_response", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeValidationSignalRefs(value *ValidationSignal) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "validation_signal", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeTraceStorePolicyRefs(value *TraceStorePolicy) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "trace_store_policy", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeReplayClassificationRefs(value *ReplayClassificationRecord) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "replay_classification", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangePhaseGatePolicyRefs(value *PhaseGatePolicy) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "phase_gate_policy", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func modelExchangeRollbackControlRefs(value *RollbackControl) modelExchangeRefs {
	if value == nil {
		return modelExchangeRefs{}
	}

	return modelExchangeRefs{name: "rollback_control", contractID: value.ContractID, protocolHash: value.ProtocolHash}
}

func (p *ProposedAction) normalize() {
	p.ActionClass = strings.TrimSpace(p.ActionClass)
	p.ToolName = strings.TrimSpace(p.ToolName)
	p.TargetRefs = compactStrings(p.TargetRefs)
	p.EvidenceRefs = compactStrings(p.EvidenceRefs)
	p.ProposalRefs = compactStrings(p.ProposalRefs)
	p.ReasonCode = strings.TrimSpace(p.ReasonCode)
}

func (r *ContextRequest) normalize() {
	r.RequestRefs = compactStrings(r.RequestRefs)
	r.MissingRefs = compactStrings(r.MissingRefs)
	r.ReasonCode = strings.TrimSpace(r.ReasonCode)
	r.RequestedShape = strings.TrimSpace(r.RequestedShape)
}

func (c *ContextChallenge) normalize() {
	c.ChallengeRefs = compactStrings(c.ChallengeRefs)
	c.EvidenceRefs = compactStrings(c.EvidenceRefs)
	c.ReasonCode = strings.TrimSpace(c.ReasonCode)
	c.ProposedShape = strings.TrimSpace(c.ProposedShape)
}

func (p *ValidationProposal) normalize() {
	p.ValidationRefs = compactStrings(p.ValidationRefs)
	p.CommandRefs = compactStrings(p.CommandRefs)
	p.EvidenceRefs = compactStrings(p.EvidenceRefs)
	p.ReasonCode = strings.TrimSpace(p.ReasonCode)
}

func (e *Escalation) normalize() {
	e.EscalationRefs = compactStrings(e.EscalationRefs)
	e.BlockerRefs = compactStrings(e.BlockerRefs)
	e.QuestionRefs = compactStrings(e.QuestionRefs)
	e.ReasonCode = strings.TrimSpace(e.ReasonCode)
}

func contextPacketValues(p ContextPacket) []string {
	values := []string{p.SchemaVersion, p.PacketID, p.ContractID, p.ProtocolHash}

	values = append(values, p.SummaryRefs...)
	values = append(values, p.TargetRefs...)
	values = append(values, p.PathRefs...)
	values = append(values, p.SymbolRefs...)
	values = append(values, p.EvidenceRefs...)
	values = append(values, p.ObligationRefs...)
	values = append(values, p.SchemaNames...)
	values = append(values, p.RedactionMarkers...)

	return values
}

func modelResponseValues(r ModelResponse) []string {
	values := []string{r.SchemaVersion, r.ResponseID, r.ContractID, r.ProtocolHash, string(r.Kind)}
	if r.ProposedAction != nil {
		values = append(values, r.ProposedAction.ActionClass, r.ProposedAction.ToolName, r.ProposedAction.ReasonCode)
		values = append(values, r.ProposedAction.TargetRefs...)
		values = append(values, r.ProposedAction.EvidenceRefs...)
		values = append(values, r.ProposedAction.ProposalRefs...)
	}

	if r.ContextRequest != nil {
		values = append(values, r.ContextRequest.ReasonCode, r.ContextRequest.RequestedShape)
		values = append(values, r.ContextRequest.RequestRefs...)
		values = append(values, r.ContextRequest.MissingRefs...)
	}

	if r.ContextChallenge != nil {
		values = append(values, r.ContextChallenge.ReasonCode, r.ContextChallenge.ProposedShape)
		values = append(values, r.ContextChallenge.ChallengeRefs...)
		values = append(values, r.ContextChallenge.EvidenceRefs...)
	}

	if r.ValidationProposal != nil {
		values = append(values, r.ValidationProposal.ReasonCode)
		values = append(values, r.ValidationProposal.ValidationRefs...)
		values = append(values, r.ValidationProposal.CommandRefs...)
		values = append(values, r.ValidationProposal.EvidenceRefs...)
	}

	if r.Escalation != nil {
		values = append(values, r.Escalation.ReasonCode)
		values = append(values, r.Escalation.EscalationRefs...)
		values = append(values, r.Escalation.BlockerRefs...)
		values = append(values, r.Escalation.QuestionRefs...)
	}

	return values
}

func permissionSnapshotValues(s PermissionSnapshot) []string {
	values := []string{s.SchemaVersion, s.SnapshotID, s.ContractID, s.ProtocolHash, s.PermissionProfile, s.CapturedBy}

	values = append(values, s.AllowedTools...)
	values = append(values, s.HiddenTools...)
	values = append(values, s.CapabilityRefs...)
	values = append(values, s.PathRoleRefs...)

	return values
}

func validationSignalValues(s ValidationSignal) []string {
	values := []string{s.SchemaVersion, s.SignalID, s.ContractID, s.ProtocolHash, s.ValidationRef, s.Status, s.CapturedBy}

	values = append(values, s.EvidenceRefs...)
	values = append(values, s.CommandRefs...)

	return values
}

func failureDeltaValues(d FailureDelta) []string {
	values := []string{d.SchemaVersion, d.DeltaID, d.ContractID, d.ProtocolHash, d.FailureClass, d.BeforeRef, d.AfterRef, d.GeneratedBy}

	values = append(values, d.EvidenceRefs...)

	return values
}

func traceStorePolicyValues(p TraceStorePolicy) []string {
	values := []string{p.SchemaVersion, p.PolicyID, p.ContractID, p.ProtocolHash, p.RetentionRef, p.RedactionRef, p.RedactionStatus}

	values = append(values, p.AllowedFields...)
	values = append(values, p.StoreRefs...)
	values = append(values, p.CapturedBy)

	return values
}

func replayClassificationValues(c ReplayClassificationRecord) []string {
	values := []string{c.SchemaVersion, c.ClassificationID, c.ContractID, c.ProtocolHash, c.Outcome, c.ClassifiedBy}

	values = append(values, c.AtomRefs...)
	values = append(values, c.ViolationRefs...)

	return values
}

func phaseGatePolicyValues(p PhaseGatePolicy) []string {
	values := []string{p.SchemaVersion, p.PolicyID, p.ContractID, p.ProtocolHash, p.PhaseID, p.EnforcementMode}

	values = append(values, p.GateRefs...)
	values = append(values, p.TerminalResponseKinds...)
	values = append(values, p.CapturedBy)

	return values
}

func rollbackControlValues(c RollbackControl) []string {
	values := []string{c.SchemaVersion, c.ControlID, c.ContractID, c.ProtocolHash, c.RollbackRef, c.Decision, c.OperatorRef, c.Source, c.PreviousMode, c.NewMode, c.Reason, c.AuthorizedBy}

	values = append(values, c.GuardRefs...)
	values = append(values, c.AffectedPackIDs...)

	return values
}

func firstModelExchangeValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
