package agent

import (
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/workerruntime"
)

func rejectedFinalAnswerAcceptanceForTest(kind turnViolationKind, reason ...turnReasonCode) finalAnswerAcceptance {
	rejectionReason := turnReasonCode(kind)
	if len(reason) > 0 {
		rejectionReason = reason[0]
	}
	return rejectFinalAnswerAcceptance(newFinalAnswerAcceptance(), kind, rejectionReason)
}

func TestTurnObjectiveRelevanceDiagnosticIsAdvisoryOnly(t *testing.T) {
	diagnostic, ok := turnDiagnosticFromLegacyBlocker(
		"answer_objective_unmet:citation_mismatch",
		"objective relevance diagnostic",
		[]string{"internal/runtime/agent/evidence_readonly.go"},
	)
	if !ok {
		t.Fatal("expected objective relevance blocker to become a diagnostic")
	}
	if diagnostic.Kind != turnDiagnosticObjectiveRelevance {
		t.Fatalf("diagnostic kind = %q, want %q", diagnostic.Kind, turnDiagnosticObjectiveRelevance)
	}
	if diagnostic.ReasonCode != "answer_objective_unmet:citation_mismatch" {
		t.Fatalf("diagnostic reason code = %q", diagnostic.ReasonCode)
	}
	if _, ok := turnViolationFromLegacyBlocker("answer_objective_unmet:citation_mismatch", "objective relevance diagnostic", nil); ok {
		t.Fatal("objective relevance diagnostic must not become an enforceable violation")
	}

	assessment := turnContractAssessment{}.withDiagnostic(diagnostic)
	if turnContractAssessmentCapabilities(assessment).Retryable {
		t.Fatal("objective relevance diagnostic must not be retryable")
	}
	if turnContractAssessmentCapabilities(assessment).Recoverable {
		t.Fatal("objective relevance diagnostic must not be recoverable")
	}
	if len(assessment.Violations) > 0 {
		t.Fatalf("diagnostic-only assessment must not have violations: %#v", assessment)
	}
}

func TestTurnViolationCapabilitiesAreCentralized(t *testing.T) {
	tests := []struct {
		kind        turnViolationKind
		retryable   bool
		recoverable bool
	}{
		{kind: turnViolationPlanExecuteAnchorRefreshPending},
		{kind: turnViolationProtocolMetaFinalAnswer, retryable: true, recoverable: true},
		{kind: turnViolationUnexecutedToolRequestAnswer},
		{kind: turnViolationRequiredEditPathsRemaining},
		{kind: turnViolationRejectedMutationPathsRemaining},
		{kind: turnViolationUserVisibleBlocker},
		{kind: turnViolationReadOnlyProtocolBlocker, retryable: true},
		{kind: turnViolationNonAnswerProse, recoverable: true},
		{kind: turnViolationSourceGroundingUnmet, retryable: true},
		{kind: turnViolationRequiredEvidenceUnmet},
		{kind: turnViolationTransportProtocolOutput},
		{kind: turnViolationCompletionGateRepeatedRejection, recoverable: true},
		{kind: turnViolationCompletionGateRequiredSourceReadsRemainingRepeated},
		{kind: turnViolationCompletionGateSubmitPlanInvalidRepeated},
		{kind: turnViolationCompletionGateSubmitPlanMissingRepeated},
		{kind: turnViolationCompletionGateRequiredPathsRemainingRepeated},
		{kind: turnViolationCompletionGateRequiredSnippetsMissingRepeated},
		{kind: turnViolationCompletionGateLaneSanityRepeated},
		{kind: turnViolationCompletionGateVerificationRepeated},
		{kind: turnViolationToolAuthorizationDenied},
		{kind: turnViolationToolAuthorizationApprovalRequired},
		{kind: turnViolationPostWriteVerificationRequired},
		{kind: turnViolationPostWriteVerificationFailed},
		{kind: turnViolationValidationStatusContradictsEvidence, recoverable: true},
	}

	if len(tests) != len(turnViolationDefinitions) {
		t.Fatalf("capability matrix covers %d violation kinds, want %d", len(tests), len(turnViolationDefinitions))
	}
	for _, tt := range tests {
		acceptance := rejectedFinalAnswerAcceptanceForTest(tt.kind)
		capabilities := turnContractAssessmentCapabilities(finalAnswerAssessmentForAcceptance(acceptance))
		if got := capabilities.Retryable; got != tt.retryable {
			t.Fatalf("retryable(%q) = %v, want %v", tt.kind, got, tt.retryable)
		}
		if got := capabilities.Recoverable; got != tt.recoverable {
			t.Fatalf("recoverable(%q) = %v, want %v", tt.kind, got, tt.recoverable)
		}
	}
}

func TestTurnFinalAnswerRetryPolicyOwnsContextAwareRetries(t *testing.T) {
	leakedTemplate := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract:          string(workerruntime.ContractImplementationResult),
		PermissionProfile: "danger-full-access",
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse, turnReasonLeakedImplementationBlocker))
	if !leakedTemplate.Retryable || leakedTemplate.RequiresReadOnlyEvidence {
		t.Fatalf("leaked implementation blocker retry policy = %#v", leakedTemplate)
	}

	directSourceGrounding := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractDirectAnswer),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationSourceGroundingUnmet))
	if !directSourceGrounding.Retryable || !directSourceGrounding.RequiresReadOnlyEvidence {
		t.Fatalf("direct source-grounding retry policy = %#v", directSourceGrounding)
	}

	architectureNonAnswer := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractArchitecture),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse, turnReasonAnswerableEvidenceInsufficient))
	if !architectureNonAnswer.Retryable || !architectureNonAnswer.RequiresReadOnlyEvidence {
		t.Fatalf("architecture non-answer retry policy = %#v", architectureNonAnswer)
	}

	plainNonAnswer := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractDirectAnswer),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse))
	if plainNonAnswer.Retryable {
		t.Fatalf("plain non-answer retry policy should not retry: %#v", plainNonAnswer)
	}
}

func TestRawLegacyBlockerStringDoesNotDriveCapabilities(t *testing.T) {
	acceptance := finalAnswerAcceptanceWithPolicy(finalAnswerAcceptance{
		Decision:    finalAnswerAcceptanceRejected,
		BlockerKind: string(turnViolationSourceGroundingUnmet),
		Reason:      "source missing",
	})
	if turnFinalAnswerAcceptanceRetryable(acceptance) || turnFinalAnswerAcceptanceRecoverable(acceptance) {
		t.Fatalf("legacy blocker string without typed violation must not drive capabilities: %#v", acceptance)
	}
}

func TestRawLegacyReasonTextDoesNotDriveContextAwareRetry(t *testing.T) {
	acceptance := finalAnswerAcceptanceWithPolicy(finalAnswerAcceptance{
		Decision:    finalAnswerAcceptanceRejected,
		BlockerKind: string(turnViolationNonAnswerProse),
		Reason:      string(turnReasonLeakedImplementationBlocker),
	})

	policy := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract:          string(workerruntime.ContractImplementationResult),
		PermissionProfile: "danger-full-access",
	}, acceptance)
	if policy.Retryable {
		t.Fatalf("legacy reason text without typed violation must not drive retry: %#v", policy)
	}
}

func TestSourceGroundingRejectionContractClassifiesRenderedMessages(t *testing.T) {
	tests := []struct {
		name string
		text string
		kind sourceGroundingRejectionKind
		path string
	}{
		{
			name: "candidate only completion gate",
			text: sourceGroundingCandidateOnlyMessage([]string{"testdata/swe/debug-logs-weird-decisions-suite.json", "docs/configuration.md"}),
			kind: sourceGroundingRejectionCandidateOnly,
			path: "docs/configuration.md",
		},
		{
			name: "missing candidate citation",
			text: sourceGroundingMissingCandidateMessage([]string{"internal/runtime/agent/implementation_flow.go"}),
			kind: sourceGroundingRejectionMissingCandidate,
			path: "internal/runtime/agent/implementation_flow.go",
		},
		{
			name: "unsupported source detail",
			text: sourceGroundingUnsupportedDetailMessage([]string{"internal/runtime/agent/runner_core.go:11456"}),
			kind: sourceGroundingRejectionUnsupportedDetail,
			path: "internal/runtime/agent/runner_core.go:11456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejection, ok := classifySourceGroundingRejectionText(tt.text)
			if !ok {
				t.Fatalf("expected source-grounding classification for %q", tt.text)
			}
			if rejection.Kind != tt.kind {
				t.Fatalf("kind = %q, want %q", rejection.Kind, tt.kind)
			}
			if !slices.Contains(rejection.Paths, tt.path) {
				t.Fatalf("paths = %#v, want %q", rejection.Paths, tt.path)
			}
			if !isSourceGroundingError(tt.text) {
				t.Fatalf("isSourceGroundingError should accept rendered %s message", tt.name)
			}
		})
	}
}

func TestSourceGroundingRejectionContractSurvivesGateRetryPrefix(t *testing.T) {
	base := sourceGroundingCandidateOnlyMessage([]string{"testdata/swe/debug-logs-weird-decisions-suite.json", "docs/configuration.md"})
	escalated := escalateCompletionGateMessage(base, 1)

	rejection, ok := classifySourceGroundingRejectionText(escalated)
	if !ok {
		t.Fatalf("escalated candidate-only message should still classify:\n%s", escalated)
	}
	if rejection.Kind != sourceGroundingRejectionCandidateOnly {
		t.Fatalf("kind = %q, want %q", rejection.Kind, sourceGroundingRejectionCandidateOnly)
	}
	if !slices.Contains(rejection.Paths, "docs/configuration.md") {
		t.Fatalf("paths = %#v, want docs/configuration.md", rejection.Paths)
	}
	if strings.Contains(escalated, "use a different tool call") {
		t.Fatalf("candidate-only source-grounding retry must not use generic recovery text:\n%s", escalated)
	}
}

func TestTurnUnknownLegacyBlockerDoesNotBecomeTypedViolation(t *testing.T) {
	if _, ok := turnViolationFromLegacyBlocker("new_semantic_signal", "new signal", nil); ok {
		t.Fatal("unknown legacy blocker must not become an enforceable typed violation")
	}

	acceptance := rejectFinalAnswerAcceptanceFromLegacyBlocker(newFinalAnswerAcceptance(), "new_semantic_signal", "new signal")
	if acceptance.Decision != finalAnswerAcceptanceAccepted {
		t.Fatalf("unknown legacy blocker must not reject without typed mapping: %#v", acceptance)
	}
	if len(acceptance.ContractAssessment.Violations) > 0 || turnFinalAnswerAcceptanceRetryable(acceptance) || turnFinalAnswerAcceptanceRecoverable(acceptance) {
		t.Fatalf("unknown legacy blocker must not mint typed violation: %#v", acceptance.ContractAssessment)
	}
}

func TestTurnAssessmentEventFieldsProjectViolationMessagesOnly(t *testing.T) {
	assessment := turnContractAssessment{
		Diagnostics: []turnDiagnostic{{
			Kind:       turnDiagnosticObjectiveRelevance,
			ReasonCode: "answer_objective_unmet",
			Message:    "display-only diagnostic text",
		}},
		Violations: []turnViolation{{
			Kind:       turnViolationSourceGroundingUnmet,
			ReasonCode: turnReasonSourceGroundingUnmet,
			Message:    "display-only violation text",
		}},
	}

	fields := turnContractAssessmentEventFields(assessment)
	diagnostics := fields["contract_diagnostics"].([]map[string]any)
	violations := fields["contract_violations"].([]map[string]any)
	if _, ok := diagnostics[0]["message"]; ok {
		t.Fatalf("diagnostic event projection must not include display text: %#v", diagnostics[0])
	}
	if violations[0]["message"] != "display-only violation text" {
		t.Fatalf("violation event projection should include exact violation text: %#v", violations[0])
	}
}

func TestFinalAnswerAcceptanceExposesTypedAssessment(t *testing.T) {
	acceptance := rejectFinalAnswerAcceptanceWithMessage(finalAnswerAcceptance{
		EvidencePaths: []string{"internal/runtime/agent/runner_core.go"},
	}, turnViolationSourceGroundingUnmet, turnReasonSourceGroundingUnmet, "source path not backed by inspected evidence")

	if len(acceptance.ContractAssessment.Violations) == 0 {
		t.Fatalf("source grounding rejection should expose enforceable violation: %#v", acceptance.ContractAssessment)
	}
	violation := acceptance.ContractAssessment.Violations[0]
	if violation.Kind != turnViolationSourceGroundingUnmet {
		t.Fatalf("violation kind = %q, want %q", violation.Kind, turnViolationSourceGroundingUnmet)
	}
	if violation.PackID != autonomy.ContractPackFinalAnswerAcceptance {
		t.Fatalf("violation pack = %q, want %q", violation.PackID, autonomy.ContractPackFinalAnswerAcceptance)
	}
	if len(violation.Evidence) != 1 || violation.Evidence[0].Path != "internal/runtime/agent/runner_core.go" {
		t.Fatalf("unexpected evidence refs: %#v", violation.Evidence)
	}
}

func TestRejectFinalAnswerAcceptanceNormalizesTypedAssessment(t *testing.T) {
	acceptance := rejectFinalAnswerAcceptance(finalAnswerAcceptance{
		EvidencePaths: []string{"internal/runtime/agent/runner_core.go"},
	}, turnViolationSourceGroundingUnmet, turnReasonSourceGroundingUnmet)

	if acceptance.Decision != finalAnswerAcceptanceRejected ||
		acceptance.PolicyDecision != autonomy.PolicyReject ||
		acceptance.PackID != autonomy.ContractPackFinalAnswerAcceptance ||
		acceptance.InvariantID != finalAnswerPolicyInvariantAcceptance {
		t.Fatalf("reject helper must return normalized policy metadata: %#v", acceptance)
	}
	if !acceptance.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) ||
		acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) {
		t.Fatalf("reject helper must return typed violation only, got %#v", acceptance.ContractAssessment)
	}
}

func TestFinalAnswerAcceptancePreservesObjectiveDiagnosticAssessment(t *testing.T) {
	acceptance := finalAnswerAcceptanceWithDirectAnswerRelevance(newFinalAnswerAcceptance(), readOnlyDirectAnswerRelevance{
		BlockerKind:             "answer_objective_unmet:term_mismatch",
		Reason:                  "objective relevance diagnostic",
		RelevantSourceBodyPaths: []string{"internal/runtime/agent/evidence_readonly.go"},
	})
	acceptance.ContractAssessment = finalAnswerAssessmentForAcceptance(acceptance)

	if len(acceptance.ContractAssessment.Violations) > 0 {
		t.Fatalf("objective relevance diagnostic must not expose violations: %#v", acceptance.ContractAssessment)
	}
	if !acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance) {
		t.Fatalf("objective relevance diagnostic missing: %#v", acceptance.ContractAssessment)
	}
}

func TestFinalAnswerAcceptanceAcceptedOverrideClearsTypedViolations(t *testing.T) {
	rejected := rejectFinalAnswerAcceptanceWithMessage(newFinalAnswerAcceptance(), turnViolationSourceGroundingUnmet, turnReasonSourceGroundingUnmet, "source missing")
	if len(rejected.ContractAssessment.Violations) == 0 {
		t.Fatalf("test setup expected violation: %#v", rejected.ContractAssessment)
	}

	accepted := finalAnswerAcceptanceWithTerminalOverride(rejected, finalAnswerAcceptanceAccepted, "", "host_read_only_closeout")
	if len(accepted.ContractAssessment.Violations) > 0 {
		t.Fatalf("accepted override must clear stale typed violations: %#v", accepted.ContractAssessment)
	}
}

func TestFinalAnswerAcceptanceRejectedOverrideReplacesTypedViolations(t *testing.T) {
	rejected := rejectFinalAnswerAcceptanceWithMessage(newFinalAnswerAcceptance(), turnViolationSourceGroundingUnmet, turnReasonSourceGroundingUnmet, "source missing")
	if !rejected.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) {
		t.Fatalf("test setup expected source-grounding violation: %#v", rejected.ContractAssessment)
	}

	transport := finalAnswerAcceptanceWithTerminalOverride(
		rejected,
		finalAnswerAcceptanceRejected,
		string(turnViolationTransportProtocolOutput),
		"tool markup",
	)
	if transport.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet) ||
		!transport.ContractAssessment.HasViolation(turnViolationTransportProtocolOutput) ||
		len(transport.ContractAssessment.Violations) != 1 {
		t.Fatalf("rejected override must replace stale typed violations: %#v", transport.ContractAssessment)
	}
}

func TestTurnAssessmentFromToolAuthorizationPolicy(t *testing.T) {
	tests := []struct {
		name     string
		decision autonomy.ToolPolicyDecision
		reason   string
		want     turnViolationKind
	}{
		{name: "ask", decision: autonomy.PolicyAsk, reason: "write_approval_required", want: turnViolationToolAuthorizationApprovalRequired},
		{name: "reject", decision: autonomy.PolicyReject, reason: "current_tool_surface", want: turnViolationToolAuthorizationDenied},
		{name: "deny", decision: autonomy.PolicyDeny, reason: "protected_path", want: turnViolationToolAuthorizationDenied},
		{name: "allow", decision: autonomy.PolicyAllow, reason: "current_tool_surface"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := toolAuthorizationPolicyEvent(
				workspace.ToolRequest{Tool: workspace.ToolNameTask},
				tt.decision,
				tt.reason,
				"status",
				"class",
				"tool_authorization_policy",
				tt.decision == autonomy.PolicyAsk,
			)

			assessment := turnAssessmentFromToolAuthorizationPolicy(policy)
			if tt.want == "" {
				if len(assessment.Violations) > 0 {
					t.Fatalf("allow policy must not create violations: %#v", assessment)
				}
				return
			}
			if !assessment.HasViolation(tt.want) {
				t.Fatalf("policy should create %q violation, got %#v", tt.want, assessment)
			}
			violation := assessment.Violations[0]
			if violation.PackID != autonomy.ContractPackToolAuthorization ||
				violation.InvariantID != toolAuthorizationPolicyInvariant ||
				violation.ReasonCode != turnReasonCode(tt.reason) {
				t.Fatalf("unexpected authorization violation: %#v", violation)
			}
		})
	}
}

func TestTurnAssessmentFromVerificationPolicy(t *testing.T) {
	required := verificationPolicyDecision(autonomy.PolicyReject, "verification_required_after_writes", "pending")
	requiredAssessment := turnAssessmentFromVerificationPolicy(required)
	if !requiredAssessment.HasViolation(turnViolationPostWriteVerificationRequired) {
		t.Fatalf("verification required should create typed violation: %#v", requiredAssessment)
	}
	if violation := requiredAssessment.Violations[0]; violation.PackID != autonomy.ContractPackVerificationPostWrite ||
		violation.InvariantID != verificationPolicyInvariantPostWrite ||
		violation.ReasonCode != turnReasonCode(required.ReasonCode) {
		t.Fatalf("unexpected required verification violation: %#v", violation)
	}

	failed := verificationPolicyDecision(autonomy.PolicyReject, "verification_result_failed", "failed")
	failedAssessment := turnAssessmentFromVerificationPolicy(failed)
	if !failedAssessment.HasViolation(turnViolationPostWriteVerificationFailed) {
		t.Fatalf("failed verification should create typed violation: %#v", failedAssessment)
	}

	allowed := verificationPolicyDecision(autonomy.PolicyAllow, "verification_result_passed", "passed")
	if assessment := turnAssessmentFromVerificationPolicy(allowed); len(assessment.Violations) > 0 {
		t.Fatalf("allowed verification must not create violations: %#v", assessment)
	}
}

func TestTurnAssessmentFromVerificationDecision(t *testing.T) {
	required := VerificationDecision{
		Handled:     true,
		Decision:    autonomy.PolicyReject,
		ReasonCode:  "verification_required_after_writes",
		Status:      "pending",
		InvariantID: verificationPolicyInvariantPostWrite,
	}
	requiredAssessment := turnAssessmentFromVerificationDecision(required)
	if !requiredAssessment.HasViolation(turnViolationPostWriteVerificationRequired) {
		t.Fatalf("verification required should create typed violation: %#v", requiredAssessment)
	}

	failed := required
	failed.ReasonCode = "verification_result_failed"
	failed.Status = "failed"
	failedAssessment := turnAssessmentFromVerificationDecision(failed)
	if !failedAssessment.HasViolation(turnViolationPostWriteVerificationFailed) {
		t.Fatalf("failed verification should create typed violation: %#v", failedAssessment)
	}

	allowed := required
	allowed.Decision = autonomy.PolicyAllow
	allowed.Status = "passed"
	if assessment := turnAssessmentFromVerificationDecision(allowed); len(assessment.Violations) > 0 {
		t.Fatalf("allowed verification must not create violations: %#v", assessment)
	}
}
