package autonomy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContractEnvelopeCarriesCompactAllowlistedContextPacket(t *testing.T) {
	envelope := ContractEnvelope{
		ContractID:   "contract_abc123",
		ProtocolHash: "protocol_def456",
		SchemaNames: []string{
			ModelResponseSchemaVersion,
			ContextPacketSchemaVersion,
			ModelResponseSchemaVersion,
		},
		ContextPacket: &ContextPacket{
			PacketID:         "packet_001",
			ContractID:       "contract_abc123",
			ProtocolHash:     "protocol_def456",
			TargetRefs:       []string{"pack:c90ecdd88a13bbe5"},
			PathRefs:         []string{"internal/autonomy/model_exchange.go"},
			EvidenceRefs:     []string{"evidence:source_body"},
			ObligationRefs:   []string{"obl-source"},
			SchemaNames:      []string{ContextPacketSchemaVersion},
			RedactionMarkers: []string{"metadata_only", "prompt_omitted", "transcript_omitted", "file_content_omitted"},
		},
		ProtocolShape: ProtocolShape{
			SchemaNames:           []string{ModelResponseSchemaVersion, "submit_plan"},
			CallableActionClasses: []string{"read", "submit_plan"},
			TerminalResponseKinds: []string{"propose_action", "request_context"},
			VisibleTools:          []string{"read_file"},
			CallableTools:         []string{"read_file"},
		},
	}

	if err := envelope.Validate(); err != nil {
		t.Fatalf("envelope should validate: %v", err)
	}
	envelope.Normalize()
	if envelope.SchemaVersion != ContractEnvelopeSchemaVersion {
		t.Fatalf("Normalize should default schema version, got %q", envelope.SchemaVersion)
	}

	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	for _, forbidden := range []string{"raw_prompt", "raw_transcript", "file_body", "secret="} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("envelope leaked forbidden marker %q: %s", forbidden, encoded)
		}
	}
}

func TestModelResponseVariantsRequireMatchingSinglePayload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response ModelResponse
	}{
		{
			name: "propose action",
			response: ModelResponse{
				ContractID:     "contract_abc123",
				ProtocolHash:   "protocol_def456",
				Kind:           ModelResponseProposeAction,
				ProposedAction: &ProposedAction{ActionClass: "read", TargetRefs: []string{"path:internal/autonomy/model_exchange.go"}},
			},
		},
		{
			name: "request context",
			response: ModelResponse{
				ContractID:     "contract_abc123",
				ProtocolHash:   "protocol_def456",
				Kind:           ModelResponseRequestContext,
				ContextRequest: &ContextRequest{MissingRefs: []string{"source_body:runner"}, ReasonCode: "missing_source_body"},
			},
		},
		{
			name: "challenge context",
			response: ModelResponse{
				ContractID:       "contract_abc123",
				ProtocolHash:     "protocol_def456",
				Kind:             ModelResponseChallengeContext,
				ContextChallenge: &ContextChallenge{ChallengeRefs: []string{"packet:stale"}, ReasonCode: "stale_anchor"},
			},
		},
		{
			name: "propose validation",
			response: ModelResponse{
				ContractID:         "contract_abc123",
				ProtocolHash:       "protocol_def456",
				Kind:               ModelResponseProposeValidation,
				ValidationProposal: &ValidationProposal{ValidationRefs: []string{"go-test-contractcompile"}, CommandRefs: []string{"cmd:focused-tests"}},
			},
		},
		{
			name: "escalate",
			response: ModelResponse{
				ContractID:   "contract_abc123",
				ProtocolHash: "protocol_def456",
				Kind:         ModelResponseEscalate,
				Escalation:   &Escalation{BlockerRefs: []string{"blocked:missing_permission"}, ReasonCode: "permission_boundary"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.response.Validate(); err != nil {
				t.Fatalf("response should validate: %v", err)
			}
		})
	}

	mismatched := ModelResponse{
		ContractID:     "contract_abc123",
		ProtocolHash:   "protocol_def456",
		Kind:           ModelResponseRequestContext,
		ProposedAction: &ProposedAction{ActionClass: "read"},
	}
	if err := mismatched.Validate(); err == nil || !strings.Contains(err.Error(), "context_request payload is required") {
		t.Fatalf("expected matching payload validation error, got %v", err)
	}

	tooMany := ModelResponse{
		ContractID:     "contract_abc123",
		ProtocolHash:   "protocol_def456",
		Kind:           ModelResponseProposeAction,
		ProposedAction: &ProposedAction{ActionClass: "read"},
		ContextRequest: &ContextRequest{MissingRefs: []string{"source_body"}},
	}
	if err := tooMany.Validate(); err == nil || !strings.Contains(err.Error(), "exactly one model response payload") {
		t.Fatalf("expected single-payload validation error, got %v", err)
	}
}

func TestModelExchangeValidationRejectsRawMarkersAndRequiresHarnessFailureDelta(t *testing.T) {
	packet := ContextPacket{
		PacketID:     "packet_001",
		ContractID:   "contract_abc123",
		ProtocolHash: "protocol_def456",
		PathRefs:     []string{"file_body: package main"},
	}
	if err := packet.Validate(); err == nil || !strings.Contains(err.Error(), "redaction-safe metadata") {
		t.Fatalf("expected raw marker validation error, got %v", err)
	}

	delta := FailureDelta{
		ContractID:   "contract_abc123",
		ProtocolHash: "protocol_def456",
		FailureClass: "parser_malformed",
		GeneratedBy:  "model",
	}
	if err := delta.Validate(); err == nil || !strings.Contains(err.Error(), "generated_by must be harness") {
		t.Fatalf("expected harness-generated validation error, got %v", err)
	}

	delta.GeneratedBy = "harness"
	if err := delta.Validate(); err != nil {
		t.Fatalf("harness-generated failure delta should validate: %v", err)
	}

	for _, authority := range []interface{ Validate() error }{
		PermissionSnapshot{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", AllowedTools: []string{"read_file"}, CapturedBy: "harness"},
		ValidationSignal{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Status: "passed", CapturedBy: "harness"},
		TraceStorePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", RedactionRef: "redaction:metadata_only", CapturedBy: "harness"},
		ReplayClassificationRecord{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Outcome: "no_violation", ClassifiedBy: "harness"},
		PhaseGatePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", PhaseID: "planner", GateRefs: []string{"completion_gate"}, CapturedBy: "harness"},
		RollbackControl{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Decision: "not_required", AuthorizedBy: "harness"},
	} {
		if err := authority.Validate(); err != nil {
			t.Fatalf("harness authority record should validate: %T: %v", authority, err)
		}
	}
}

func TestHarnessOwnedAuthorityRecordsRejectMissingOrModelProvenance(t *testing.T) {
	tests := []struct {
		name        string
		missing     interface{ Validate() error }
		modelAuth   interface{ Validate() error }
		wantMessage string
	}{
		{
			name:        "permission snapshot",
			missing:     PermissionSnapshot{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", AllowedTools: []string{"read_file"}},
			modelAuth:   PermissionSnapshot{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", AllowedTools: []string{"read_file"}, CapturedBy: "model"},
			wantMessage: "permission_snapshot.captured_by must be harness",
		},
		{
			name:        "validation signal",
			missing:     ValidationSignal{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Status: "passed"},
			modelAuth:   ValidationSignal{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Status: "passed", CapturedBy: "model"},
			wantMessage: "validation_signal.captured_by must be harness",
		},
		{
			name:        "failure delta",
			missing:     FailureDelta{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", FailureClass: "parser_malformed"},
			modelAuth:   FailureDelta{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", FailureClass: "parser_malformed", GeneratedBy: "model"},
			wantMessage: "failure_delta.generated_by must be harness",
		},
		{
			name:        "trace store policy",
			missing:     TraceStorePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", RedactionRef: "redaction:metadata_only"},
			modelAuth:   TraceStorePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", RedactionRef: "redaction:metadata_only", CapturedBy: "model"},
			wantMessage: "trace_store_policy.captured_by must be harness",
		},
		{
			name:        "replay classification",
			missing:     ReplayClassificationRecord{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Outcome: "full"},
			modelAuth:   ReplayClassificationRecord{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Outcome: "full", ClassifiedBy: "model"},
			wantMessage: "replay_classification.classified_by must be harness",
		},
		{
			name:        "phase gate policy",
			missing:     PhaseGatePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", PhaseID: "planner", GateRefs: []string{"completion_gate"}},
			modelAuth:   PhaseGatePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", PhaseID: "planner", GateRefs: []string{"completion_gate"}, CapturedBy: "model"},
			wantMessage: "phase_gate_policy.captured_by must be harness",
		},
		{
			name:        "rollback control",
			missing:     RollbackControl{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Decision: "not_required"},
			modelAuth:   RollbackControl{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Decision: "not_required", AuthorizedBy: "model"},
			wantMessage: "rollback_control.authorized_by must be harness",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name+" missing", func(t *testing.T) {
			if err := tc.missing.Validate(); err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("expected missing provenance error %q, got %v", tc.wantMessage, err)
			}
		})
		t.Run(tc.name+" model", func(t *testing.T) {
			if err := tc.modelAuth.Validate(); err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("expected model provenance error %q, got %v", tc.wantMessage, err)
			}
		})
	}
}

func TestHarnessOwnedAuthorityRecordsRequireProtocolProvenance(t *testing.T) {
	snapshot := PermissionSnapshot{
		ContractID:   "contract_abc123",
		AllowedTools: []string{"read_file"},
		CapturedBy:   "harness",
	}
	if err := snapshot.Validate(); err == nil || !strings.Contains(err.Error(), "protocol_hash is required") {
		t.Fatalf("expected protocol provenance error, got %v", err)
	}
}
