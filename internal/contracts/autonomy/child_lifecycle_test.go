package autonomy

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Child lifecycle contracts", func() {
	It("requires child contracts to name their parent and lane-phase scope", func() {
		contract, err := NewContract(ContractInput{
			ParentID: "contract-parent",
			Scope: ContractScope{
				InvocationKind: InvocationChild,
				Lane:           "architecture",
				Phase:          "read_only_evidence",
				TaskFamily:     "delegated_read_only",
			},
			Capabilities: CapabilityPolicy{
				Allowed:        []Capability{CapabilitySemanticDiscovery, CapabilityBoundedRead},
				AllowedEffects: []SideEffectClass{SideEffectReadOnly},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(contract.Scope.InvocationKind).To(Equal(InvocationChild))
		Expect(contract.ParentID).To(Equal("contract-parent"))

		_, err = NewContract(ContractInput{
			Scope: ContractScope{
				InvocationKind: InvocationChild,
				Lane:           "architecture",
				Phase:          "read_only_evidence",
			},
		})
		Expect(err).To(MatchError(ContainSubstring("parent_id is required for child contracts")))
	})

	It("validates the parent-child runtime envelope and blocking capacity", func() {
		record := parentChildInvocationFixture()
		Expect(record.Validate()).To(Succeed())

		record.RuntimeEnvelope.CapacityAdmission.State = CapacityAdmissionUnavailable
		Expect(record.Validate()).To(MatchError(ContainSubstring("state must be acquired or degraded for blocking dispatch")))

		record = parentChildInvocationFixture()
		record.RuntimeEnvelope.RoleResolution.OK = false
		record.RuntimeEnvelope.RoleResolution.Source = RoleResolutionSourceRoleTag
		Expect(record.Validate()).To(MatchError(ContainSubstring("source must be unresolved when ok is false")))
	})

	DescribeTable("validates child lifecycle enum values",
		func(valid bool) {
			Expect(valid).To(BeTrue())
		},
		Entry("workspace main", validChildWorkspaceMode(ChildWorkspaceMain)),
		Entry("workspace invalid", !validChildWorkspaceMode(ChildWorkspaceMode("sidecar"))),
		Entry("role source proxy", validRoleResolutionSource(RoleResolutionSourceProxyDefault)),
		Entry("role source invalid", !validRoleResolutionSource(RoleResolutionSource("manual"))),
		Entry("capacity state acquired", validCapacityAdmissionState(CapacityAdmissionAcquired)),
		Entry("capacity state invalid", !validCapacityAdmissionState(CapacityAdmissionState("pending"))),
		Entry("capacity enforcement blocking", validCapacityAdmissionEnforcement(CapacityAdmissionBlocking)),
		Entry("capacity enforcement invalid", !validCapacityAdmissionEnforcement(CapacityAdmissionEnforcement("manual"))),
		Entry("transport native", validChildTransportMode(ChildTransportNative)),
		Entry("transport invalid", !validChildTransportMode(ChildTransportMode("email"))),
		Entry("observed deterministic", validChildObservedTransport(ChildObservedTransportDeterministic)),
		Entry("observed invalid", !validChildObservedTransport(ChildObservedTransport("proxy"))),
		Entry("normalizer normalized", validChildNormalizerStatus(ChildNormalizerNormalized)),
		Entry("normalizer invalid", !validChildNormalizerStatus(ChildNormalizerStatus("skipped"))),
		Entry("result candidate patch", validChildResultKind(ChildResultCandidatePatch)),
		Entry("result invalid", !validChildResultKind(ChildResultKind("summary"))),
		Entry("terminal completed", validChildTerminalStatus(ChildTerminalCompleted)),
		Entry("terminal invalid", !validChildTerminalStatus(ChildTerminalStatus("queued"))),
		Entry("objective accepted", validChildObjectiveAlignment(ChildObjectiveAccepted)),
		Entry("objective invalid", !validChildObjectiveAlignment(ChildObjectiveAlignment("partial"))),
		Entry("execution route child", validChildExecutionRoute(ChildRouteChildModel)),
		Entry("execution route invalid", !validChildExecutionRoute(ChildExecutionRoute("worker"))),
		Entry("path gate allow", validChildPathGateDecision(ChildPathGateAllow)),
		Entry("path gate invalid", !validChildPathGateDecision(ChildPathGateDecision("ask"))),
		Entry("acceptance accepted", validChildAcceptanceDecision(ChildAcceptanceAccepted)),
		Entry("acceptance invalid", !validChildAcceptanceDecision(ChildAcceptanceDecision("deferred"))),
		Entry("alignment evaluator host", validAlignmentEvaluator(AlignmentEvaluatorHostContract)),
		Entry("alignment evaluator invalid", !validAlignmentEvaluator(AlignmentEvaluator("parent"))),
		Entry("verification accepted", validVerificationAcceptanceDecision(VerificationAcceptanceAccepted)),
		Entry("verification invalid", !validVerificationAcceptanceDecision(VerificationAcceptanceDecision("deferred"))),
	)

	It("requires candidate-patch terminals to reference a patch artifact", func() {
		record := ChildTerminalRecord{
			TerminalID:         "terminal-1",
			InvocationID:       "child-1",
			ChildContractID:    "contract-child",
			ResultKind:         ChildResultCandidatePatch,
			Status:             ChildTerminalCompleted,
			ObjectiveAlignment: ChildObjectiveNotApplicable,
			ParseStatus: ChildParseStatus{
				TransportObserved: ChildObservedTransportTextual,
				NormalizerStatus:  ChildNormalizerNormalized,
			},
		}

		Expect(record.Validate()).To(MatchError(ContainSubstring("artifact_refs are required")))
		record.ArtifactRefs = []string{"patch://patch-1"}
		Expect(record.Validate()).To(Succeed())
	})

	It("requires candidate patches to carry a digest and changed paths", func() {
		artifact := CandidatePatchArtifact{
			PatchID:         "patch-1",
			InvocationID:    "child-1",
			ChildContractID: "contract-child",
			BaseSHA:         "abc123",
			ChangedPaths:    []string{"internal/contracts/autonomy/child_lifecycle.go"},
			DiffStat:        PatchDiffStat{Files: 1, Insertions: 12},
			PatchPath:       ".idiom/patches/patch-1.diff",
			PatchDigest:     "sha256:abcd",
			CreatedAt:       time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC),
		}

		Expect(artifact.Validate()).To(Succeed())
		artifact.PatchDigest = ""
		Expect(artifact.Validate()).To(MatchError(ContainSubstring("patch_digest is required")))
	})

	It("requires applied-parent-edit terminals to carry execution metadata", func() {
		record := ChildTerminalRecord{
			TerminalID:         "terminal-1",
			InvocationID:       "child-1",
			ChildContractID:    "contract-child",
			ResultKind:         ChildResultAppliedParentEdit,
			Status:             ChildTerminalCompleted,
			ObjectiveAlignment: ChildObjectiveNotApplicable,
			ParseStatus: ChildParseStatus{
				TransportObserved: ChildObservedTransportDeterministic,
				NormalizerStatus:  ChildNormalizerNotRequired,
			},
		}

		Expect(record.Validate()).To(MatchError(ContainSubstring("execution_metadata requires route")))
		record.ExecutionMetadata = ChildExecutionMetadata{
			Route:                      ChildRouteDeterministicDirect,
			ParentPlanAuthorityRef:     "plan://accepted-plan",
			PathGateRef:                "event://path-gate",
			PathGateDecision:           ChildPathGateAllow,
			VerificationObligationRefs: []string{"obl-verify"},
		}
		Expect(record.Validate()).To(Succeed())

		record.ParseStatus = ChildParseStatus{}
		Expect(record.Validate()).To(MatchError(ContainSubstring("parse_status.transport_observed is required for applied_parent_edit")))
		record.ParseStatus = ChildParseStatus{
			TransportObserved: ChildObservedTransportDeterministic,
			NormalizerStatus:  ChildNormalizerNotRequired,
		}
		record.ExecutionMetadata.PathGateDecision = ChildPathGateDecision("ask")
		Expect(record.Validate()).To(MatchError(ContainSubstring("path_gate_decision is invalid")))
	})

	It("requires parse-failed terminals to describe the parse failure", func() {
		record := ChildTerminalRecord{
			TerminalID:         "terminal-parse",
			InvocationID:       "child-1",
			ChildContractID:    "contract-child",
			ResultKind:         ChildResultEvidencePacket,
			Status:             ChildTerminalParseFailed,
			ObjectiveAlignment: ChildObjectiveWeak,
		}

		Expect(record.Validate()).To(MatchError(ContainSubstring("parser_failure_class is required")))
		record.ParseStatus = ChildParseStatus{
			TransportObserved:  ChildObservedTransportTextual,
			NormalizerStatus:   ChildNormalizerFailed,
			ParserFailureClass: "invalid_json",
			RawOutputRedacted:  true,
		}
		Expect(record.Validate()).To(Succeed())
	})

	It("requires accepted output alignment while allowing non-authoritative records", func() {
		accepted := childAcceptanceFixture()
		Expect(accepted.Validate()).To(Succeed())

		accepted.AlignmentBasis.AcceptedSourceBodyRefs = nil
		Expect(accepted.Validate()).To(MatchError(ContainSubstring("accepted_source_body_refs are required")))

		accepted = childAcceptanceFixture()
		accepted.ReasonCode = "accepted_evidence"
		accepted.AlignmentBasis = ChildAlignmentBasis{}
		Expect(accepted.Validate()).To(MatchError(ContainSubstring("delegated_objective_ref is required")))

		accepted = childAcceptanceFixture()
		accepted.ReasonCode = "non_authoritative"
		accepted.AlignmentBasis = ChildAlignmentBasis{}
		Expect(accepted.Validate()).To(Succeed())

		appliedEdit := ChildAcceptanceRecord{
			AcceptanceID:     "accept-delegate",
			ParentContractID: "contract-parent",
			ChildContractID:  "contract-child",
			InvocationID:     "child-1",
			TerminalID:       "terminal-1",
			ResultKind:       ChildResultAppliedParentEdit,
			Decision:         ChildAcceptanceRecordedOnly,
			ReasonCode:       "direct_dispatch",
		}
		Expect(appliedEdit.Validate()).To(MatchError(ContainSubstring("applied_parent_edit acceptance requires route")))
	})

	It("requires accepted child output to retain replayable references", func() {
		accepted := childAcceptanceFixture()
		accepted.AcceptedEvidenceRefs = nil
		accepted.AcceptedPatchRefs = nil
		accepted.VerificationObligationRefs = nil
		Expect(accepted.Validate()).To(MatchError(ContainSubstring("accepted child output requires")))
	})

	It("requires verification acceptance to reference obligations and a successful command", func() {
		record := VerificationAcceptanceRecord{
			VerificationAcceptanceID: "verify-accept-1",
			ParentContractID:         "contract-parent",
			ObligationRefs:           []string{"obl-verify"},
			CommandRef:               "command://go-test-runtime-agent",
			CommandFingerprint:       "sha256:cmd",
			WorkspaceRef:             "workspace://main",
			BaseSHA:                  "abc123",
			HeadSHA:                  "def456",
			ExitStatus:               0,
			ResultRef:                "result://go-test-runtime-agent",
			Decision:                 VerificationAcceptanceAccepted,
			ReasonCode:               "obligation_matched",
		}

		Expect(record.Validate()).To(Succeed())
		record.ObligationRefs = nil
		Expect(record.Validate()).To(MatchError(ContainSubstring("obligation_refs are required")))

		record.ObligationRefs = []string{"obl-verify"}
		record.ExitStatus = 1
		Expect(record.Validate()).To(MatchError(ContainSubstring("accepted verification requires exit_status 0")))
	})
})

func parentChildInvocationFixture() ParentChildInvocationRecord {
	return ParentChildInvocationRecord{
		InvocationID:     "child-1",
		ParentContractID: "contract-parent",
		ChildContractID:  "contract-child",
		Role:             "source_investigator",
		Lane:             "architecture",
		Phase:            "read_only_evidence",
		WorkspaceMode:    ChildWorkspaceNoWrite,
		PackID:           "pack-1",
		ContractSummary: ChildContractSummary{
			PathRoleDigest:   "sha256:path",
			ToolPolicyDigest: "sha256:tool",
			ObligationDigest: "sha256:obl",
		},
		RuntimeEnvelope: ChildRuntimeEnvelope{
			BackendKind: "deepseek",
			ModelAlias:  "deepseek-flash",
			ModelRole:   "source_investigator",
			RoleResolution: ChildRoleResolution{
				RequestedRole: "source_investigator",
				ResolvedRole:  "source_investigator",
				Alias:         "deepseek-flash",
				Source:        RoleResolutionSourceRoleTag,
				OK:            true,
			},
			CapacityAdmission: ChildCapacityAdmission{
				State:       CapacityAdmissionAcquired,
				Pool:        "external_client",
				ModelAlias:  "deepseek-flash",
				Enforcement: CapacityAdmissionBlocking,
			},
			ToolTransportMode:   ChildTransportNative,
			ParserTransportMode: ChildTransportTextual,
			ParserContract:      "read_only_evidence_packet",
			MaxTokens:           4096,
			ContextBudget: ChildContextBudget{
				MaxContextMessages: 8,
				MaxContextChars:    64000,
			},
			ToolSchemaHash:   "sha256:tools",
			RequestTimeoutMS: 75000,
		},
		CreatedAt: time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC),
	}
}

func childAcceptanceFixture() ChildAcceptanceRecord {
	return ChildAcceptanceRecord{
		AcceptanceID:               "accept-1",
		ParentContractID:           "contract-parent",
		ChildContractID:            "contract-child",
		InvocationID:               "child-1",
		TerminalID:                 "terminal-1",
		ResultKind:                 ChildResultEvidencePacket,
		Decision:                   ChildAcceptanceAccepted,
		RuntimeEnvelopeRef:         "runtime://child-1",
		PackID:                     "pack-1",
		PackFreshness:              FreshnessFresh,
		AcceptedEvidenceRefs:       []string{"source://internal/runtime/agent/runner_child_tasks.go"},
		VerificationObligationRefs: []string{"obl-evidence"},
		AlignmentBasis: ChildAlignmentBasis{
			DelegatedObjectiveRef:  "objective://child-1",
			EffectiveScopeRefs:     []string{"scope://internal/runtime/agent"},
			AcceptedSourceBodyRefs: []string{"source://internal/runtime/agent/runner_child_tasks.go"},
			CandidateOnlyRefs:      []string{"context_pack://pack-1"},
			Evaluator:              AlignmentEvaluatorHostContract,
		},
		ReasonCode: "objective_aligned",
	}
}
