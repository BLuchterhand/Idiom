package autonomy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewPhaseContractBuildsStableSummary(t *testing.T) {
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
			MaxToolSteps:       12,
			UsedToolSteps:      2,
			MaxModelCalls:      4,
			UsedModelCalls:     1,
			MaxContextMessages: 20,
			MaxContextChars:    120000,
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
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	if contract.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", contract.SchemaVersion, SchemaVersion)
	}
	if !strings.HasPrefix(contract.ContractID, "contract_") {
		t.Fatalf("contract id = %q, want contract_ prefix", contract.ContractID)
	}

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
	if err != nil {
		t.Fatalf("reconstructed contract returned error: %v", err)
	}
	if reconstructed.ContractID != contract.ContractID {
		t.Fatalf("contract id is not stable: got %q want %q", reconstructed.ContractID, contract.ContractID)
	}

	summary := contract.Summary()
	if summary.Lane != "implement" || summary.Phase != "execute" || summary.TaskFamily != "implementation" {
		t.Fatalf("summary identity mismatch: %#v", summary)
	}
	if summary.TargetRoleCounts[string(PathRoleRequiredEdit)] != 1 || summary.TargetRoleCounts[string(PathRoleEvidenceReadonly)] != 1 {
		t.Fatalf("unexpected path role counts: %#v", summary.TargetRoleCounts)
	}
	if summary.ObligationCounts[string(ObligationOpen)+":"+string(ObligationVerify)] != 1 {
		t.Fatalf("unexpected obligation counts: %#v", summary.ObligationCounts)
	}
	if summary.ProtectedPathCount != 1 || summary.ToolPolicyCount != 2 || summary.PolicyDecisionCount != 1 || summary.EvidenceReferenceCount != 1 {
		t.Fatalf("unexpected summary counts: %#v", summary)
	}
}

func TestSidecarReadOnlyContractRoundTripsWithoutStrategyFields(t *testing.T) {
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
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	jsonText := string(encoded)
	for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
		if strings.Contains(jsonText, forbidden) {
			t.Fatalf("contract JSON should not prescribe strategy field %q: %s", forbidden, jsonText)
		}
	}

	var roundTrip AutonomyContract
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal contract: %v", err)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatalf("round-tripped contract should validate: %v", err)
	}
	if roundTrip.Scope.Sidecar == nil || roundTrip.Scope.Sidecar.Profile != "discovery" || roundTrip.Scope.Sidecar.Tool != "context_pack" {
		t.Fatalf("round-tripped sidecar context mismatch: %#v", roundTrip.Scope.Sidecar)
	}
	if containsCapability(roundTrip.Capabilities.Allowed, CapabilitySourceEdit) {
		t.Fatalf("read-only sidecar contract allowed source edits: %#v", roundTrip.Capabilities.Allowed)
	}
	if !containsCapability(roundTrip.Capabilities.Forbidden, CapabilityExternalWrite) {
		t.Fatalf("read-only sidecar contract should forbid external writes: %#v", roundTrip.Capabilities.Forbidden)
	}
}

func TestNewContractCopiesScopePointerContexts(t *testing.T) {
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
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}
	if contract.Scope.Sidecar == sidecar {
		t.Fatalf("contract sidecar context aliases caller-owned pointer")
	}
	if contract.Scope.ToolCall == toolCall {
		t.Fatalf("contract tool call context aliases caller-owned pointer")
	}
	if sidecar.Tool != " context_pack " || toolCall.ArgumentsDigest != " digest-before " {
		t.Fatalf("caller-owned scope pointers were normalized through aliasing: sidecar=%#v tool_call=%#v", sidecar, toolCall)
	}

	originalID := contract.ContractID
	sidecar.Tool = "apply_patch"
	sidecar.Profile = "write"
	toolCall.ToolName = "apply_patch"
	toolCall.SideEffect = SideEffectDestructive
	toolCall.ArgumentsDigest = "digest-after"

	if contract.Scope.Sidecar.Tool != "context_pack" || contract.Scope.Sidecar.Profile != "discovery" {
		t.Fatalf("post-construction sidecar mutation changed contract body: %#v", contract.Scope.Sidecar)
	}
	if contract.Scope.ToolCall.ToolName != "context_pack" ||
		contract.Scope.ToolCall.SideEffect != SideEffectReadOnly ||
		contract.Scope.ToolCall.ArgumentsDigest != "digest-before" {
		t.Fatalf("post-construction tool call mutation changed contract body: %#v", contract.Scope.ToolCall)
	}
	if got := contract.stableID(); got != originalID {
		t.Fatalf("post-construction input mutation changed stable id: got %q want %q", got, originalID)
	}
}

func TestContractValidationRejectsInvalidShapes(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewContract(tt.input)
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestContractMetadataCoversClientClassificationAndProductSignals(t *testing.T) {
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
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	for _, want := range allClientClassifications() {
		if !containsClientClassification(contract.Metadata.ClientClassifications, want) {
			t.Fatalf("missing client classification %q in %#v", want, contract.Metadata.ClientClassifications)
		}
	}
	for _, want := range allProductSignals() {
		if !containsProductSignal(contract.Metadata.ProductSignals, want) {
			t.Fatalf("missing product signal %q in %#v", want, contract.Metadata.ProductSignals)
		}
	}
	if len(contract.PathRoles) < len(allClientClassifications()) {
		t.Fatalf("expected at least one path-role fixture per client class, got %#v", contract.PathRoles)
	}
}

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
