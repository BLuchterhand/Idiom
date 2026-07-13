package autonomy

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Autonomy contracts", func() {
	It("builds a stable phase-contract summary", func() {
		contract, err := NewContract(ContractInput{
			Scope: ContractScope{
				InvocationKind:    InvocationPhase,
				Lane:              "implement",
				LaneRunID:         "lane-123",
				Phase:             "execute",
				PhaseRunID:        "phase-456",
				TaskFamily:        "implementation",
				RuntimeContract:   "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			Authority: []AuthoritySource{
				{Kind: "runtime_brief", Ref: "brief://lane-123", Freshness: FreshnessFresh},
				{Kind: "context_pack", Ref: "context_pack://c20be47003211dcd", EvidenceRef: "ev-context-pack", Freshness: FreshnessFresh},
			},
			PathRoles: []PathRole{
				{Role: PathRoleRequiredEdit, PathPattern: "internal/runtime/agent/runner.go", Freshness: FreshnessFresh, Reason: "required edit path from lane brief"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "docs/decisioning-north-star.md", Freshness: FreshnessFresh, Reason: "policy evidence only"},
				{Role: PathRoleProtected, PathPattern: ".git/**", Freshness: FreshnessFresh, Reason: "protected repository metadata"},
			},
			Capabilities: CapabilityPolicy{
				Allowed:          []Capability{CapabilityBoundedRead, CapabilitySourceEdit, CapabilityApplyPatch, CapabilityShellVerification},
				Forbidden:        []Capability{CapabilityExternalWrite, CapabilityMutatingShell},
				HiddenTools:      []string{"git_push"},
				AllowedEffects:   []SideEffectClass{SideEffectReadOnly, SideEffectAdditive},
				ForbiddenEffects: []SideEffectClass{SideEffectDestructive, SideEffectOpenWorld},
			},
			ToolPolicy: []ToolPolicyRule{
				{ToolPattern: "apply_patch", Decision: PolicyAllow, Capability: CapabilityApplyPatch, SideEffect: SideEffectAdditive, Source: "permission_profile"},
				{ToolPattern: "git push", Decision: PolicyDeny, Capability: CapabilityExternalWrite, SideEffect: SideEffectOpenWorld, Source: "protected_effect_policy", ReasonCode: "external_publish_not_in_contract"},
			},
			Obligations: []Obligation{
				{ObligationID: "obl-write-runner", Kind: ObligationWrite, Status: ObligationOpen, Owner: OwnerLane, TargetRef: "internal/runtime/agent/runner.go", RequiredBy: "required_edit_paths"},
				{ObligationID: "obl-verify", Kind: ObligationVerify, Status: ObligationOpen, Owner: OwnerHost, RequiredBy: "post_write_verification"},
				{ObligationID: "obl-final", Kind: ObligationFinalAnswer, Status: ObligationOpen, Owner: OwnerHost, RequiredBy: "completion_gate"},
			},
			Budget: BudgetState{
				MaxToolSteps:             12,
				UsedToolSteps:            2,
				MaxModelCalls:            4,
				MaxOperationalModelCalls: 8,
				UsedModelCalls:           1,
				MaxContextMessages:       20,
				MaxContextChars:          120000,
			},
			Verification: VerificationRequirement{
				Required:               true,
				Contract:               "tests_passed_or_failed_or_skipped_with_blocker",
				RequiredAfterWrite:     true,
				SourceAwareRequired:    true,
				AcceptedCommands:       []string{"go test ./internal/runtime/agent"},
				EvidenceRefs:           []string{"ev-context-pack"},
				SkippedRequiresBlocker: true,
			},
			Completion: CompletionRequirement{
				Required:               true,
				Criteria:               []string{"changed_paths_or_noop_reason", "verification_status"},
				BlockOnOpenObligations: true,
				AllowedNoopReasons:     []string{"required path already satisfies requested behavior"},
			},
			Readiness: ReadinessRequirement{
				ReadyToWrite:        true,
				OpenObligationRefs:  []string{"obl-write-runner", "obl-verify", "obl-final"},
				EvidenceRefs:        []string{"ev-context-pack"},
				FurtherReadsRequire: "one exact missing claim and one exact probe",
			},
			PolicyDecisions: []PolicyDecision{
				{
					DecisionID:       "decision-protect-git",
					Decision:         PolicyDeny,
					InvariantID:      "write.protected_path",
					ReasonCode:       "protected_path",
					Capability:       CapabilityExternalWrite,
					SideEffect:       SideEffectOpenWorld,
					PathRefs:         []string{".git/**"},
					ApprovalRequired: true,
					Source:           "path_role_policy",
				},
			},
			Evidence: []EvidenceRef{
				{EvidenceID: "ev-context-pack", Kind: "context_view", Path: ".idiom/comprehension/views/c20be47003211dcd.json", Handle: "context_pack://c20be47003211dcd", Freshness: FreshnessFresh, Confidence: "syntax"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(contract.SchemaVersion).To(Equal(SchemaVersion))
		Expect(contract.ContractID).To(HavePrefix("contract_"))

		reconstructed, err := NewContract(ContractInput{
			Scope:           contract.Scope,
			Authority:       contract.Authority,
			PathRoles:       contract.PathRoles,
			Capabilities:    contract.Capabilities,
			ToolPolicy:      contract.ToolPolicy,
			Obligations:     contract.Obligations,
			Budget:          contract.Budget,
			Verification:    contract.Verification,
			Completion:      contract.Completion,
			Readiness:       contract.Readiness,
			PolicyDecisions: contract.PolicyDecisions,
			Evidence:        contract.Evidence,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(reconstructed.ContractID).To(Equal(contract.ContractID))

		summary := contract.Summary()
		Expect(summary.Lane).To(Equal("implement"))
		Expect(summary.Phase).To(Equal("execute"))
		Expect(summary.TaskFamily).To(Equal("implementation"))
		Expect(summary.TargetRoleCounts).To(HaveKeyWithValue(string(PathRoleRequiredEdit), 1))
		Expect(summary.TargetRoleCounts).To(HaveKeyWithValue(string(PathRoleEvidenceReadonly), 1))
		Expect(summary.ObligationCounts).To(HaveKeyWithValue(string(ObligationOpen)+":"+string(ObligationVerify), 1))
		Expect(summary.ProtectedPathCount).To(Equal(1))
		Expect(summary.ToolPolicyCount).To(Equal(2))
		Expect(summary.PolicyDecisionCount).To(Equal(1))
		Expect(summary.EvidenceReferenceCount).To(Equal(1))
	})

	It("round-trips a read-only sidecar contract without strategy fields", func() {
		contract, err := NewContract(ContractInput{
			Scope: ContractScope{
				InvocationKind:    InvocationSidecarCall,
				TaskFamily:        "sidecar_discovery",
				RuntimeContract:   "grounded_evidence_summary",
				PermissionProfile: "read_only_workspace",
				Sidecar: &SidecarContext{
					Transport: "cli",
					Caller:    "codex",
					Profile:   "discovery",
					RequestID: "req-001",
					Tool:      "context_pack",
				},
				ToolCall: &ToolCallContext{
					ToolCallID: "tool-001",
					ToolName:   "context_pack",
					Capability: CapabilitySemanticDiscovery,
					SideEffect: SideEffectReadOnly,
				},
			},
			PathRoles: []PathRole{
				{Role: PathRoleEvidenceReadonly, PathPattern: "internal/capabilities/workspace/**", Freshness: FreshnessFresh, Reason: "discovery profile evidence"},
				{Role: PathRoleProtected, PathPattern: ".env", Freshness: FreshnessUnknown, Reason: "secret-bearing path"},
				{Role: PathRoleForbidden, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata must not be exposed through sidecar discovery"},
			},
			Capabilities: CapabilityPolicy{
				Allowed:          []Capability{CapabilitySemanticDiscovery, CapabilityBoundedRead, CapabilityLiteralSearch},
				Forbidden:        []Capability{CapabilitySourceEdit, CapabilityApplyPatch, CapabilityMutatingShell, CapabilityExternalWrite},
				AllowedEffects:   []SideEffectClass{SideEffectReadOnly},
				ForbiddenEffects: []SideEffectClass{SideEffectAdditive, SideEffectDestructive, SideEffectOpenWorld},
			},
			ToolPolicy: []ToolPolicyRule{
				{ToolPattern: "context_pack", Decision: PolicyAllow, Capability: CapabilitySemanticDiscovery, SideEffect: SideEffectReadOnly, Source: "sidecar_profile"},
				{ToolPattern: "apply_patch", Decision: PolicyDeny, Capability: CapabilityApplyPatch, SideEffect: SideEffectAdditive, Source: "sidecar_profile", ReasonCode: "read_only_contract"},
			},
			Obligations: []Obligation{
				{ObligationID: "obl-evidence", Kind: ObligationEvidence, Status: ObligationOpen, Owner: OwnerSidecar, RequiredBy: "sidecar_request", TargetRef: "context_pack"},
			},
			Verification: VerificationRequirement{
				Required: false,
				Contract: "not_required_for_read_only_sidecar",
			},
			Completion: CompletionRequirement{
				Required:               true,
				Criteria:               []string{"answer_addresses_sidecar_request", "inspected_paths_or_blocker"},
				BlockOnOpenObligations: true,
			},
			Readiness: ReadinessRequirement{
				ReadyToAnswer:       false,
				OpenObligationRefs:  []string{"obl-evidence"},
				FurtherReadsRequire: "a specific evidence gap in the sidecar request",
			},
			Evidence: []EvidenceRef{
				{EvidenceID: "ev-sidecar-request", Kind: "sidecar_request", Handle: "tool_call://tool-001", Freshness: FreshnessFresh, Confidence: "direct"},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		encoded, err := json.Marshal(contract)
		Expect(err).NotTo(HaveOccurred())

		jsonText := string(encoded)
		for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
			Expect(jsonText).NotTo(ContainSubstring(forbidden))
		}

		var roundTrip AutonomyContract

		Expect(json.Unmarshal(encoded, &roundTrip)).To(Succeed())
		Expect(roundTrip.Validate()).To(Succeed())
		Expect(roundTrip.Scope.Sidecar).NotTo(BeNil())
		Expect(roundTrip.Scope.Sidecar.Profile).To(Equal("discovery"))
		Expect(roundTrip.Scope.Sidecar.Tool).To(Equal("context_pack"))
		Expect(roundTrip.Capabilities.Allowed).NotTo(ContainElement(CapabilitySourceEdit))
		Expect(roundTrip.Capabilities.Forbidden).To(ContainElement(CapabilityExternalWrite))
	})

	It("copies caller-owned scope pointer contexts", func() {
		sidecar := &SidecarContext{
			Transport: " cli ",
			Caller:    " codex ",
			Profile:   " discovery ",
			RequestID: " req-001 ",
			Tool:      " context_pack ",
		}
		toolCall := &ToolCallContext{
			ToolCallID:      " tool-001 ",
			ToolName:        " context_pack ",
			Capability:      CapabilitySemanticDiscovery,
			SideEffect:      SideEffectReadOnly,
			ArgumentsDigest: " digest-before ",
		}

		contract, err := NewContract(ContractInput{
			Scope: ContractScope{
				InvocationKind:    InvocationSidecarCall,
				TaskFamily:        "sidecar_discovery",
				RuntimeContract:   "grounded_evidence_summary",
				PermissionProfile: "read_only_workspace",
				Sidecar:           sidecar,
				ToolCall:          toolCall,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(contract.Scope.Sidecar).NotTo(BeIdenticalTo(sidecar))
		Expect(contract.Scope.ToolCall).NotTo(BeIdenticalTo(toolCall))
		Expect(sidecar.Tool).To(Equal(" context_pack "))
		Expect(toolCall.ArgumentsDigest).To(Equal(" digest-before "))

		originalID := contract.ContractID

		sidecar.Tool = "apply_patch"
		sidecar.Profile = "write"
		toolCall.ToolName = "apply_patch"
		toolCall.SideEffect = SideEffectDestructive
		toolCall.ArgumentsDigest = "digest-after"

		Expect(contract.Scope.Sidecar.Tool).To(Equal("context_pack"))
		Expect(contract.Scope.Sidecar.Profile).To(Equal("discovery"))
		Expect(contract.Scope.ToolCall.ToolName).To(Equal("context_pack"))
		Expect(contract.Scope.ToolCall.SideEffect).To(Equal(SideEffectReadOnly))
		Expect(contract.Scope.ToolCall.ArgumentsDigest).To(Equal("digest-before"))
		Expect(contract.stableID()).To(Equal(originalID))
	})

	Describe("validation", func() {
		tests := []struct {
			name  string
			input ContractInput
			want  string
		}{
			{
				name:  "missing invocation kind",
				input: ContractInput{},
				want:  "scope.invocation_kind",
			},
			{
				name: "phase requires lane and phase",
				input: ContractInput{
					Scope: ContractScope{InvocationKind: InvocationPhase, Lane: "implement"},
				},
				want: "scope.lane and scope.phase",
			},
			{
				name: "sidecar requires sidecar context",
				input: ContractInput{
					Scope: ContractScope{InvocationKind: InvocationSidecarCall},
				},
				want: "scope.sidecar is required",
			},
			{
				name: "path role needs target",
				input: ContractInput{
					Scope:     ContractScope{InvocationKind: InvocationLane, Lane: "implement"},
					PathRoles: []PathRole{{Role: PathRoleRequiredEdit}},
				},
				want: "must identify a path_pattern",
			},
			{
				name: "negative budget rejected",
				input: ContractInput{
					Scope:  ContractScope{InvocationKind: InvocationLane, Lane: "implement"},
					Budget: BudgetState{MaxToolSteps: -1},
				},
				want: "budget.max_tool_steps",
			},
			{
				name: "obligation needs identity",
				input: ContractInput{
					Scope:       ContractScope{InvocationKind: InvocationLane, Lane: "implement"},
					Obligations: []Obligation{{Kind: ObligationVerify, Status: ObligationOpen}},
				},
				want: "obligation_id is required",
			},
		}

		for _, tt := range tests {
			tt := tt
			It("rejects "+tt.name, func() {
				_, err := NewContract(tt.input)
				Expect(err).To(MatchError(ContainSubstring(tt.want)))
			})
		}
	})

	It("covers client classifications and product signals in contract metadata", func() {
		contract, err := NewContract(ContractInput{
			Scope: ContractScope{
				InvocationKind:    InvocationSidecarCall,
				TaskFamily:        "sidecar_discovery",
				RuntimeContract:   "classification_coverage_fixture",
				PermissionProfile: "read_only_workspace",
				Sidecar: &SidecarContext{
					Transport: "cli",
					Caller:    "external-agent",
					Profile:   "discovery",
					RequestID: "classification-fixture",
					Tool:      "context_pack",
				},
			},
			PathRoles: []PathRole{
				{Role: PathRoleEvidenceReadonly, PathPattern: "finance/audit/context-pack.json", Reason: "finance audit evidence"},
				{Role: PathRoleForbidden, PathPattern: "healthcare/phi/**", Reason: "healthcare private data boundary"},
				{Role: PathRoleProtected, PathPattern: "public-sector/supply-chain/**", Reason: "public-sector protected evidence"},
				{Role: PathRoleRequiredEdit, PathPattern: "enterprise-saas/services/billing/retry.go", Reason: "enterprise SaaS merge-facing fixture"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "tooling-vendor/schema/idiom.tool.v1.json", Reason: "tooling vendor schema fixture"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "local-model/queue/readiness.json", Reason: "local model operations fixture"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "open-source/CONTRIBUTING.md", Reason: "open-source activation fixture"},
			},
			Capabilities: CapabilityPolicy{
				Allowed:          []Capability{CapabilitySemanticDiscovery, CapabilityBoundedRead},
				Forbidden:        []Capability{CapabilitySourceEdit, CapabilityExternalWrite},
				AllowedEffects:   []SideEffectClass{SideEffectReadOnly},
				ForbiddenEffects: []SideEffectClass{SideEffectDestructive, SideEffectOpenWorld},
			},
			Metadata: ContractMetadata{
				ClientClassifications: []ClientClassification{
					ClientFinance,
					ClientHealthcare,
					ClientPublicSector,
					ClientEnterpriseSaaS,
					ClientToolingVendor,
					ClientLocalModel,
					ClientOpenSource,
				},
				ProductSignals: []ProductSignal{
					ProductICP,
					ProductActivation,
					ProductRetainedUse,
					ProductIntegrationProof,
					ProductSecurityPrivacy,
					ProductCompetitiveProof,
					ProductClientDemoProof,
					ProductPackagingSupport,
					ProductLocalEvidenceExport,
				},
				Tags: []string{"p1.1", "client-classification-fixture"},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		for _, want := range allClientClassifications() {
			Expect(contract.Metadata.ClientClassifications).To(ContainElement(want))
		}

		for _, want := range allProductSignals() {
			Expect(contract.Metadata.ProductSignals).To(ContainElement(want))
		}

		Expect(len(contract.PathRoles)).To(BeNumerically(">=", len(allClientClassifications())))
	})
})

func containsCapability(values []Capability, want Capability) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func containsClientClassification(values []ClientClassification, want ClientClassification) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func containsProductSignal(values []ProductSignal, want ProductSignal) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func allClientClassifications() []ClientClassification {
	return []ClientClassification{
		ClientFinance,
		ClientHealthcare,
		ClientPublicSector,
		ClientEnterpriseSaaS,
		ClientToolingVendor,
		ClientLocalModel,
		ClientOpenSource,
	}
}

func allProductSignals() []ProductSignal {
	return []ProductSignal{
		ProductICP,
		ProductActivation,
		ProductRetainedUse,
		ProductIntegrationProof,
		ProductSecurityPrivacy,
		ProductCompetitiveProof,
		ProductClientDemoProof,
		ProductPackagingSupport,
		ProductLocalEvidenceExport,
	}
}
