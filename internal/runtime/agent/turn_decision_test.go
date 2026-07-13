package agent

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

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

var _ = ginkgo.It("TurnObjectiveRelevanceDiagnosticIsAdvisoryOnly", func() {
	diagnostic, ok := turnDiagnosticFromLegacyBlocker(
		"answer_objective_unmet:citation_mismatch",
		"objective relevance diagnostic",
		[]string{"internal/runtime/agent/evidence_readonly.go"},
	)
	gomega.Expect(ok).To(gomega.BeTrue(), "expected objective relevance blocker to become a diagnostic")
	gomega.Expect(diagnostic.Kind).To(gomega.Equal(turnDiagnosticObjectiveRelevance))
	gomega.Expect(diagnostic.ReasonCode).To(gomega.Equal(turnReasonCode("answer_objective_unmet:citation_mismatch")))
	_, violationOK := turnViolationFromLegacyBlocker("answer_objective_unmet:citation_mismatch", "objective relevance diagnostic", nil)
	gomega.Expect(violationOK).To(gomega.BeFalse(), "objective relevance diagnostic must not become an enforceable violation")

	assessment := turnContractAssessment{}.withDiagnostic(diagnostic)
	capabilities := turnContractAssessmentCapabilities(assessment)
	gomega.Expect(capabilities.Retryable).To(gomega.BeFalse(), "assessment=%#v", assessment)
	gomega.Expect(capabilities.Recoverable).To(gomega.BeFalse(), "assessment=%#v", assessment)
	gomega.Expect(assessment.Violations).To(gomega.BeEmpty(), "assessment=%#v", assessment)
})

var _ = ginkgo.It("TurnViolationCapabilitiesAreCentralized", func() {
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

	gomega.Expect(tests).To(gomega.HaveLen(len(turnViolationDefinitions)), "capability matrix must cover every violation kind")
	for _, tt := range tests {
		acceptance := rejectedFinalAnswerAcceptanceForTest(tt.kind)
		capabilities := turnContractAssessmentCapabilities(acceptance.ContractAssessment)
		gomega.Expect(capabilities.Retryable).To(gomega.Equal(tt.retryable), "violation kind %q", tt.kind)
		gomega.Expect(capabilities.Recoverable).To(gomega.Equal(tt.recoverable), "violation kind %q", tt.kind)
	}
})

var _ = ginkgo.It("TurnFinalAnswerRetryPolicyOwnsContextAwareRetries", func() {
	leakedTemplate := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract:          string(workerruntime.ContractImplementationResult),
		PermissionProfile: "danger-full-access",
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse, turnReasonLeakedImplementationBlocker))
	gomega.Expect(leakedTemplate.Retryable).To(gomega.BeTrue(), "policy=%#v", leakedTemplate)
	gomega.Expect(leakedTemplate.RequiresReadOnlyEvidence).To(gomega.BeFalse(), "policy=%#v", leakedTemplate)

	directSourceGrounding := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractDirectAnswer),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationSourceGroundingUnmet))
	gomega.Expect(directSourceGrounding.Retryable).To(gomega.BeTrue(), "policy=%#v", directSourceGrounding)
	gomega.Expect(directSourceGrounding.RequiresReadOnlyEvidence).To(gomega.BeTrue(), "policy=%#v", directSourceGrounding)

	architectureNonAnswer := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractArchitecture),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse, turnReasonAnswerableEvidenceInsufficient))
	gomega.Expect(architectureNonAnswer.Retryable).To(gomega.BeTrue(), "policy=%#v", architectureNonAnswer)
	gomega.Expect(architectureNonAnswer.RequiresReadOnlyEvidence).To(gomega.BeTrue(), "policy=%#v", architectureNonAnswer)

	plainNonAnswer := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractDirectAnswer),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse))
	gomega.Expect(plainNonAnswer.Retryable).To(gomega.BeTrue(), "direct answers must return non-answer prose to the model for a fresh authored answer: policy=%#v", plainNonAnswer)
	gomega.Expect(plainNonAnswer.RequiresReadOnlyEvidence).To(gomega.BeTrue(), "policy=%#v", plainNonAnswer)

	implementationNonAnswer := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract: string(workerruntime.ContractImplementationResult),
	}, rejectedFinalAnswerAcceptanceForTest(turnViolationNonAnswerProse))
	gomega.Expect(implementationNonAnswer.Retryable).To(gomega.BeFalse(), "the direct-answer exception must not broaden mutation-lane retries: policy=%#v", implementationNonAnswer)
})

var _ = ginkgo.It("RawLegacyBlockerStringDoesNotDriveCapabilities", func() {
	acceptance := finalAnswerAcceptanceWithPolicy(finalAnswerAcceptance{
		Decision:    finalAnswerAcceptanceRejected,
		BlockerKind: string(turnViolationSourceGroundingUnmet),
		Reason:      "source missing",
	})
	gomega.Expect(turnFinalAnswerAcceptanceRetryable(acceptance)).To(gomega.BeFalse(), "acceptance=%#v", acceptance)
	gomega.Expect(turnFinalAnswerAcceptanceRecoverable(acceptance)).To(gomega.BeFalse(), "acceptance=%#v", acceptance)
})

var _ = ginkgo.It("RawLegacyReasonTextDoesNotDriveContextAwareRetry", func() {
	acceptance := finalAnswerAcceptanceWithPolicy(finalAnswerAcceptance{
		Decision:    finalAnswerAcceptanceRejected,
		BlockerKind: string(turnViolationNonAnswerProse),
		Reason:      string(turnReasonLeakedImplementationBlocker),
	})

	policy := turnFinalAnswerRejectionRetryPolicyForBrief(workerruntime.WorkerRuntimeBrief{
		Contract:          string(workerruntime.ContractImplementationResult),
		PermissionProfile: "danger-full-access",
	}, acceptance)
	gomega.Expect(policy.Retryable).To(gomega.BeFalse(), "policy=%#v", policy)
})

var _ = ginkgo.It("SourceGroundingRejectionContractCarriesTypedPayload", func() {
	tests := []struct {
		name       string
		assessment sourceGroundingAssessment
		kind       sourceGroundingRejectionKind
		path       string
	}{
		{
			name:       "candidate only completion gate",
			assessment: sourceGroundingCandidateOnlyAssessment([]string{"testdata/swe/debug-logs-weird-decisions-suite.json", "docs/configuration.md"}),
			kind:       sourceGroundingRejectionCandidateOnly,
			path:       "docs/configuration.md",
		},
		{
			name:       "missing candidate citation",
			assessment: sourceGroundingMissingCandidateReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/implementation_flow.go"})),
			kind:       sourceGroundingRejectionMissingCandidate,
			path:       "internal/runtime/agent/implementation_flow.go",
		},
		{
			name:       "unsupported source detail",
			assessment: sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/runner_core.go:11456"})),
			kind:       sourceGroundingRejectionUnsupportedDetail,
			path:       "internal/runtime/agent/runner_core.go:11456",
		},
	}

	for _, tt := range tests {
		ginkgo.By(tt.name, func() {
			acceptance := rejectFinalAnswerAcceptanceWithSourceGrounding(newFinalAnswerAcceptance(), tt.assessment)
			result, ok := finalAnswerCompletionGateProjection(acceptance, "", acceptance.Reason)
			gomega.Expect(ok).To(gomega.BeTrue(), "expected completion-gate projection")

			rejection, ok := sourceGroundingAssessmentFromResult(result)
			gomega.Expect(ok).To(gomega.BeTrue(), "result=%#v", result)
			gomega.Expect(rejection.Kind).To(gomega.Equal(tt.kind), "rejection=%#v", rejection)
			paths := sourceReferencePaths(rejection.References)
			wantPath := sourceReferenceForPath(tt.path).Path
			gomega.Expect(paths).To(gomega.ContainElement(wantPath))
		})
	}
})

var _ = ginkgo.It("SourceGroundingRejectionEscalationUsesTypedSignal", func() {
	base := sourceGroundingCandidateOnlyAssessment([]string{"testdata/swe/debug-logs-weird-decisions-suite.json", "docs/configuration.md"}).Message()
	escalated := escalateCompletionGateMessageWithSourceRecovery(base, 1, true, true)

	gomega.Expect(escalated).To(gomega.ContainSubstring("[source evidence recovery attempt 2]"))
	gomega.Expect(escalated).NotTo(gomega.ContainSubstring("use a different tool call"))
})

var _ = ginkgo.It("TurnUnknownLegacyBlockerDoesNotBecomeTypedViolation", func() {
	_, ok := turnViolationFromLegacyBlocker("new_semantic_signal", "new signal", nil)
	gomega.Expect(ok).To(gomega.BeFalse(), "unknown legacy blocker must not become an enforceable typed violation")
})

var _ = ginkgo.It("TurnAssessmentEventFieldsProjectViolationMessagesOnly", func() {
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
	gomega.Expect(diagnostics[0]).NotTo(gomega.HaveKey("message"))
	gomega.Expect(violations[0]).To(gomega.HaveKeyWithValue("message", "display-only violation text"))
})

var _ = ginkgo.It("FinalAnswerAcceptanceExposesTypedAssessment", func() {
	acceptance := rejectFinalAnswerAcceptanceWithSourceGrounding(finalAnswerAcceptance{
		EvidencePaths: []string{
			" internal/runtime/agent/runner_core.go ",
			"internal/runtime/agent/source_grounding.go",
			"internal/runtime/agent/runner_core.go",
			"",
		},
	}, sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/source_grounding.go"})))

	violation, ok := turnContractAssessmentPrimaryViolation(acceptance.ContractAssessment)
	gomega.Expect(ok).To(gomega.BeTrue(), "assessment=%#v", acceptance.ContractAssessment)
	gomega.Expect(violation.Kind).To(gomega.Equal(turnViolationSourceGroundingUnmet))
	gomega.Expect(violation.PackID).To(gomega.Equal(autonomy.ContractPackFinalAnswerAcceptance))
	gomega.Expect(violation.EvidencePaths).To(gomega.Equal([]string{
		"internal/runtime/agent/runner_core.go",
		"internal/runtime/agent/source_grounding.go",
	}))
})

var _ = ginkgo.It("RejectFinalAnswerAcceptanceNormalizesTypedAssessment", func() {
	acceptance := rejectFinalAnswerAcceptance(finalAnswerAcceptance{
		EvidencePaths: []string{"internal/runtime/agent/runner_core.go"},
	}, turnViolationRequiredEvidenceUnmet, turnReasonRequiredEvidenceUnmet)

	gomega.Expect(acceptance.Decision).To(gomega.Equal(finalAnswerAcceptanceRejected), "acceptance=%#v", acceptance)
	gomega.Expect(acceptance.PolicyDecision).To(gomega.Equal(autonomy.PolicyReject), "acceptance=%#v", acceptance)
	gomega.Expect(acceptance.PackID).To(gomega.Equal(autonomy.ContractPackFinalAnswerAcceptance), "acceptance=%#v", acceptance)
	gomega.Expect(acceptance.InvariantID).To(gomega.Equal(finalAnswerPolicyInvariantAcceptance), "acceptance=%#v", acceptance)
	gomega.Expect(acceptance.ContractAssessment.HasViolation(turnViolationRequiredEvidenceUnmet)).To(gomega.BeTrue(), "assessment=%#v", acceptance.ContractAssessment)
	gomega.Expect(acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance)).To(gomega.BeFalse(), "assessment=%#v", acceptance.ContractAssessment)
})

var _ = ginkgo.It("FinalAnswerAcceptancePreservesObjectiveDiagnosticAssessment", func() {
	acceptance := finalAnswerAcceptanceWithDirectAnswerRelevance(newFinalAnswerAcceptance(), readOnlyDirectAnswerRelevance{
		BlockerKind:             "answer_objective_unmet:term_mismatch",
		Reason:                  "objective relevance diagnostic",
		RelevantSourceBodyPaths: []string{"internal/runtime/agent/evidence_readonly.go"},
	})

	gomega.Expect(acceptance.ContractAssessment.Violations).To(gomega.BeEmpty(), "assessment=%#v", acceptance.ContractAssessment)
	gomega.Expect(acceptance.ContractAssessment.HasDiagnostic(turnDiagnosticObjectiveRelevance)).To(gomega.BeTrue(), "assessment=%#v", acceptance.ContractAssessment)
})

var _ = ginkgo.It("FinalAnswerAcceptanceAcceptedOverrideClearsTypedViolations", func() {
	rejected := rejectFinalAnswerAcceptanceWithSourceGrounding(newFinalAnswerAcceptance(), sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/source_grounding.go"})))
	gomega.Expect(rejected.ContractAssessment.Violations).NotTo(gomega.BeEmpty(), "test setup expected violation")

	accepted := finalAnswerAcceptanceWithTerminalOverride(rejected, finalAnswerAcceptanceAccepted, "", "read_only_terminal_recovery")
	gomega.Expect(accepted.ContractAssessment.Violations).To(gomega.BeEmpty(), "assessment=%#v", accepted.ContractAssessment)
})

var _ = ginkgo.It("FinalAnswerAcceptanceRejectedOverrideReplacesTypedViolations", func() {
	rejected := rejectFinalAnswerAcceptanceWithSourceGrounding(newFinalAnswerAcceptance(), sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/agent/source_grounding.go"})))
	gomega.Expect(rejected.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet)).To(gomega.BeTrue(), "test setup assessment=%#v", rejected.ContractAssessment)

	transport := finalAnswerAcceptanceWithTerminalOverride(
		rejected,
		finalAnswerAcceptanceRejected,
		string(turnViolationTransportProtocolOutput),
		"tool markup",
	)
	gomega.Expect(transport.ContractAssessment.HasViolation(turnViolationSourceGroundingUnmet)).To(gomega.BeFalse(), "assessment=%#v", transport.ContractAssessment)
	gomega.Expect(transport.ContractAssessment.HasViolation(turnViolationTransportProtocolOutput)).To(gomega.BeTrue(), "assessment=%#v", transport.ContractAssessment)
	gomega.Expect(transport.ContractAssessment.Violations).To(gomega.HaveLen(1), "assessment=%#v", transport.ContractAssessment)
})
