package autonomy

import (
	"strings"
	"testing"
	"time"
)

func TestChildContractRequiresParentAndLanePhaseScope(t *testing.T) {
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
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	if contract.Scope.InvocationKind != InvocationChild || contract.ParentID != "contract-parent" {
		t.Fatalf("child contract identity mismatch: %#v", contract)
	}

	_, err = NewContract(ContractInput{
		Scope: ContractScope{
			InvocationKind: InvocationChild,
			Lane:           "architecture",
			Phase:          "read_only_evidence",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "parent_id is required for child contracts") {
		t.Fatalf("expected missing parent_id error, got %v", err)
	}
}

func TestParentChildInvocationRecordValidatesRuntimeEnvelopeAndCapacity(t *testing.T) {
	record := parentChildInvocationFixture()

	if err := record.Validate(); err != nil {
		t.Fatalf("valid invocation record rejected: %v", err)
	}

	record.RuntimeEnvelope.CapacityAdmission.State = CapacityAdmissionUnavailable
	err := record.Validate()
	if err == nil || !strings.Contains(err.Error(), "state must be acquired or degraded for blocking dispatch") {
		t.Fatalf("expected blocking capacity validation error, got %v", err)
	}

	record = parentChildInvocationFixture()
	record.RuntimeEnvelope.RoleResolution.OK = false
	record.RuntimeEnvelope.RoleResolution.Source = RoleResolutionSourceRoleTag
	err = record.Validate()
	if err == nil || !strings.Contains(err.Error(), "source must be unresolved when ok is false") {
		t.Fatalf("expected unresolved role validation error, got %v", err)
	}
}

func TestChildLifecycleEnumValidators(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "workspace main", ok: validChildWorkspaceMode(ChildWorkspaceMain)},
		{name: "workspace invalid", ok: !validChildWorkspaceMode(ChildWorkspaceMode("sidecar"))},
		{name: "role source proxy", ok: validRoleResolutionSource(RoleResolutionSourceProxyDefault)},
		{name: "role source invalid", ok: !validRoleResolutionSource(RoleResolutionSource("manual"))},
		{name: "capacity state acquired", ok: validCapacityAdmissionState(CapacityAdmissionAcquired)},
		{name: "capacity state invalid", ok: !validCapacityAdmissionState(CapacityAdmissionState("pending"))},
		{name: "capacity enforcement blocking", ok: validCapacityAdmissionEnforcement(CapacityAdmissionBlocking)},
		{name: "capacity enforcement invalid", ok: !validCapacityAdmissionEnforcement(CapacityAdmissionEnforcement("manual"))},
		{name: "transport native", ok: validChildTransportMode(ChildTransportNative)},
		{name: "transport invalid", ok: !validChildTransportMode(ChildTransportMode("email"))},
		{name: "observed deterministic", ok: validChildObservedTransport(ChildObservedTransportDeterministic)},
		{name: "observed invalid", ok: !validChildObservedTransport(ChildObservedTransport("proxy"))},
		{name: "normalizer normalized", ok: validChildNormalizerStatus(ChildNormalizerNormalized)},
		{name: "normalizer invalid", ok: !validChildNormalizerStatus(ChildNormalizerStatus("skipped"))},
		{name: "result candidate patch", ok: validChildResultKind(ChildResultCandidatePatch)},
		{name: "result invalid", ok: !validChildResultKind(ChildResultKind("summary"))},
		{name: "terminal completed", ok: validChildTerminalStatus(ChildTerminalCompleted)},
		{name: "terminal invalid", ok: !validChildTerminalStatus(ChildTerminalStatus("queued"))},
		{name: "objective accepted", ok: validChildObjectiveAlignment(ChildObjectiveAccepted)},
		{name: "objective invalid", ok: !validChildObjectiveAlignment(ChildObjectiveAlignment("partial"))},
		{name: "execution route child", ok: validChildExecutionRoute(ChildRouteChildModel)},
		{name: "execution route invalid", ok: !validChildExecutionRoute(ChildExecutionRoute("worker"))},
		{name: "path gate allow", ok: validChildPathGateDecision(ChildPathGateAllow)},
		{name: "path gate invalid", ok: !validChildPathGateDecision(ChildPathGateDecision("ask"))},
		{name: "acceptance accepted", ok: validChildAcceptanceDecision(ChildAcceptanceAccepted)},
		{name: "acceptance invalid", ok: !validChildAcceptanceDecision(ChildAcceptanceDecision("deferred"))},
		{name: "alignment evaluator host", ok: validAlignmentEvaluator(AlignmentEvaluatorHostContract)},
		{name: "alignment evaluator invalid", ok: !validAlignmentEvaluator(AlignmentEvaluator("parent"))},
		{name: "verification accepted", ok: validVerificationAcceptanceDecision(VerificationAcceptanceAccepted)},
		{name: "verification invalid", ok: !validVerificationAcceptanceDecision(VerificationAcceptanceDecision("deferred"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatal("validator result changed")
			}
		})
	}
}

func TestChildTerminalRequiresCandidatePatchArtifactRef(t *testing.T) {
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

	err := record.Validate()
	if err == nil || !strings.Contains(err.Error(), "artifact_refs are required") {
		t.Fatalf("expected missing artifact ref error, got %v", err)
	}

	record.ArtifactRefs = []string{"patch://patch-1"}
	if err := record.Validate(); err != nil {
		t.Fatalf("terminal with patch artifact ref rejected: %v", err)
	}
}

func TestCandidatePatchArtifactRequiresDigestAndChangedPaths(t *testing.T) {
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

	if err := artifact.Validate(); err != nil {
		t.Fatalf("valid candidate patch artifact rejected: %v", err)
	}

	artifact.PatchDigest = ""
	err := artifact.Validate()
	if err == nil || !strings.Contains(err.Error(), "patch_digest is required") {
		t.Fatalf("expected missing patch_digest error, got %v", err)
	}
}

func TestAppliedParentEditTerminalRequiresExecutionMetadata(t *testing.T) {
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

	err := record.Validate()
	if err == nil || !strings.Contains(err.Error(), "execution_metadata requires route") {
		t.Fatalf("expected execution metadata error, got %v", err)
	}

	record.ExecutionMetadata = ChildExecutionMetadata{
		Route:                      ChildRouteDeterministicDirect,
		ParentPlanAuthorityRef:     "plan://accepted-plan",
		PathGateRef:                "event://path-gate",
		PathGateDecision:           ChildPathGateAllow,
		VerificationObligationRefs: []string{"obl-verify"},
	}

	if err := record.Validate(); err != nil {
		t.Fatalf("applied parent edit terminal rejected: %v", err)
	}

	record.ParseStatus = ChildParseStatus{}
	err = record.Validate()
	if err == nil || !strings.Contains(err.Error(), "parse_status.transport_observed is required for applied_parent_edit") {
		t.Fatalf("expected applied parent edit parse status error, got %v", err)
	}

	record.ParseStatus = ChildParseStatus{
		TransportObserved: ChildObservedTransportDeterministic,
		NormalizerStatus:  ChildNormalizerNotRequired,
	}

	record.ExecutionMetadata.PathGateDecision = ChildPathGateDecision("ask")
	err = record.Validate()
	if err == nil || !strings.Contains(err.Error(), "path_gate_decision is invalid") {
		t.Fatalf("expected invalid path gate decision error, got %v", err)
	}
}

func TestParseFailedTerminalRequiresParseFailureDetails(t *testing.T) {
	record := ChildTerminalRecord{
		TerminalID:         "terminal-parse",
		InvocationID:       "child-1",
		ChildContractID:    "contract-child",
		ResultKind:         ChildResultEvidencePacket,
		Status:             ChildTerminalParseFailed,
		ObjectiveAlignment: ChildObjectiveWeak,
	}

	err := record.Validate()
	if err == nil || !strings.Contains(err.Error(), "parser_failure_class is required") {
		t.Fatalf("expected parse failure detail error, got %v", err)
	}

	record.ParseStatus = ChildParseStatus{
		TransportObserved:  ChildObservedTransportTextual,
		NormalizerStatus:   ChildNormalizerFailed,
		ParserFailureClass: "invalid_json",
		RawOutputRedacted:  true,
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("parse_failed terminal with details rejected: %v", err)
	}
}

func TestChildAcceptanceRequiresObjectiveAlignmentBasisAndDelegateEditRefs(t *testing.T) {
	accepted := childAcceptanceFixture()
	if err := accepted.Validate(); err != nil {
		t.Fatalf("valid child acceptance rejected: %v", err)
	}

	accepted.AlignmentBasis.AcceptedSourceBodyRefs = nil
	err := accepted.Validate()
	if err == nil || !strings.Contains(err.Error(), "accepted_source_body_refs are required") {
		t.Fatalf("expected missing source-body alignment basis error, got %v", err)
	}

	accepted = childAcceptanceFixture()
	accepted.ReasonCode = "accepted_evidence"
	accepted.AlignmentBasis = ChildAlignmentBasis{}
	err = accepted.Validate()
	if err == nil || !strings.Contains(err.Error(), "delegated_objective_ref is required") {
		t.Fatalf("expected accepted evidence alignment basis error, got %v", err)
	}

	accepted = childAcceptanceFixture()
	accepted.ReasonCode = "non_authoritative"
	accepted.AlignmentBasis = ChildAlignmentBasis{}
	if err := accepted.Validate(); err != nil {
		t.Fatalf("non-authoritative accepted record should not require objective alignment basis: %v", err)
	}

	delegateEdit := ChildAcceptanceRecord{
		AcceptanceID:     "accept-delegate",
		ParentContractID: "contract-parent",
		ChildContractID:  "contract-child",
		InvocationID:     "child-1",
		TerminalID:       "terminal-1",
		ResultKind:       ChildResultAppliedParentEdit,
		Decision:         ChildAcceptanceRecordedOnly,
		ReasonCode:       "direct_dispatch",
	}
	err = delegateEdit.Validate()
	if err == nil || !strings.Contains(err.Error(), "applied_parent_edit acceptance requires route") {
		t.Fatalf("expected delegate_edit metadata error, got %v", err)
	}
}

func TestAcceptedChildAcceptanceRequiresReplayableRefs(t *testing.T) {
	accepted := childAcceptanceFixture()
	accepted.AcceptedEvidenceRefs = nil
	accepted.AcceptedPatchRefs = nil
	accepted.VerificationObligationRefs = nil

	err := accepted.Validate()
	if err == nil || !strings.Contains(err.Error(), "accepted child output requires") {
		t.Fatalf("expected accepted refs validation error, got %v", err)
	}
}

func TestVerificationAcceptanceRecordRequiresCommandAndObligationRefs(t *testing.T) {
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

	if err := record.Validate(); err != nil {
		t.Fatalf("valid verification acceptance rejected: %v", err)
	}

	record.ObligationRefs = nil
	err := record.Validate()
	if err == nil || !strings.Contains(err.Error(), "obligation_refs are required") {
		t.Fatalf("expected missing obligation refs error, got %v", err)
	}

	record = VerificationAcceptanceRecord{
		VerificationAcceptanceID: "verify-accept-1",
		ParentContractID:         "contract-parent",
		ObligationRefs:           []string{"obl-verify"},
		CommandRef:               "command://go-test-runtime-agent",
		CommandFingerprint:       "sha256:cmd",
		WorkspaceRef:             "workspace://main",
		BaseSHA:                  "abc123",
		HeadSHA:                  "def456",
		ExitStatus:               1,
		ResultRef:                "result://go-test-runtime-agent",
		Decision:                 VerificationAcceptanceAccepted,
		ReasonCode:               "obligation_matched",
	}
	err = record.Validate()
	if err == nil || !strings.Contains(err.Error(), "accepted verification requires exit_status 0") {
		t.Fatalf("expected accepted failed-exit validation error, got %v", err)
	}
}

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
