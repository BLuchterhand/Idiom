package autonomy

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model exchange contracts", func() {
	Describe("model response payloads", func() {
		validResponses := []struct {
			name     string
			response ModelResponse
		}{
			{
				name: "propose action",
				response: ModelResponse{
					ContractID:     "contract_abc123",
					ProtocolHash:   "protocol_def456",
					Kind:           ModelResponseProposeAction,
					ProposedAction: &ProposedAction{ActionClass: "read", TargetRefs: []string{"path:internal/contracts/autonomy/model_exchange.go"}},
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
		}
		for _, tc := range validResponses {
			tc := tc
			It("accepts a matching single payload for "+tc.name, func() {
				Expect(tc.response.Validate()).To(Succeed())
			})
		}

		It("requires the payload matching the response kind", func() {
			response := ModelResponse{
				ContractID:     "contract_abc123",
				ProtocolHash:   "protocol_def456",
				Kind:           ModelResponseRequestContext,
				ProposedAction: &ProposedAction{ActionClass: "read"},
			}
			Expect(response.Validate()).To(MatchError(ContainSubstring("context_request payload is required")))
		})

		It("rejects multiple response payloads", func() {
			response := ModelResponse{
				ContractID:     "contract_abc123",
				ProtocolHash:   "protocol_def456",
				Kind:           ModelResponseProposeAction,
				ProposedAction: &ProposedAction{ActionClass: "read"},
				ContextRequest: &ContextRequest{MissingRefs: []string{"source_body"}},
			}
			Expect(response.Validate()).To(MatchError(ContainSubstring("exactly one model response payload")))
		})
	})

	It("rejects raw context markers and requires harness-generated failure deltas", func() {
		packet := ContextPacket{
			PacketID:     "packet_001",
			ContractID:   "contract_abc123",
			ProtocolHash: "protocol_def456",
			PathRefs:     []string{"file_body: package main"},
		}
		Expect(packet.Validate()).To(MatchError(ContainSubstring("redaction-safe metadata")))

		delta := FailureDelta{
			ContractID:   "contract_abc123",
			ProtocolHash: "protocol_def456",
			FailureClass: "parser_malformed",
			GeneratedBy:  "model",
		}
		Expect(delta.Validate()).To(MatchError(ContainSubstring("generated_by must be harness")))
		delta.GeneratedBy = "harness"
		Expect(delta.Validate()).To(Succeed())

		for _, authority := range []interface{ Validate() error }{
			PermissionSnapshot{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", AllowedTools: []string{"read_file"}, CapturedBy: "harness"},
			ValidationSignal{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Status: "passed", CapturedBy: "harness"},
			TraceStorePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", RedactionRef: "redaction:metadata_only", CapturedBy: "harness"},
			ReplayClassificationRecord{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Outcome: "no_violation", ClassifiedBy: "harness"},
			PhaseGatePolicy{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", PhaseID: "planner", GateRefs: []string{"completion_gate"}, CapturedBy: "harness"},
			RollbackControl{ContractID: "contract_abc123", ProtocolHash: "protocol_def456", Decision: "not_required", AuthorizedBy: "harness"},
		} {
			Expect(authority.Validate()).To(Succeed(), "authority type %T", authority)
		}
	})

	Describe("harness-owned authority provenance", func() {
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
			tc := tc
			Context(tc.name, func() {
				It("rejects missing provenance", func() {
					Expect(tc.missing.Validate()).To(MatchError(ContainSubstring(tc.wantMessage)))
				})
				It("rejects model provenance", func() {
					Expect(tc.modelAuth.Validate()).To(MatchError(ContainSubstring(tc.wantMessage)))
				})
			})
		}
	})

	It("requires protocol provenance for harness-owned authority records", func() {
		snapshot := PermissionSnapshot{
			ContractID:   "contract_abc123",
			AllowedTools: []string{"read_file"},
			CapturedBy:   "harness",
		}
		Expect(snapshot.Validate()).To(MatchError(ContainSubstring("protocol_hash is required")))
	})
})
