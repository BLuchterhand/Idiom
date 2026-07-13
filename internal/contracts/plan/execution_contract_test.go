package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan execution contracts", func() {
	It("normalizes stable identity and scope content", func() {
		base := PlanExecutionContract{
			ContractID: "stale", ContractHash: "stale", PlanID: " Feature Plan 42 ",
			Atoms: []AtomExecutionContract{{
				AtomTitle: "Wire contract",
				Scope: PlanScopeContract{
					RequiredCoveragePaths: []string{" ./pkg/b.go ", "pkg/a.go", "pkg\\a.go"},
					AllowedCandidatePaths: []string{"pkg/c.go", "pkg/a.go"},
					WriteAllowedPaths:     []string{"pkg/**", "pkg/a.go"}, WriteAllowedExplicit: true,
				},
				TDDObligations: []TDDObligation{{
					ID: "TDD A", Command: " go   test ./pkg ",
					TestArtifactPaths: []string{"pkg/feature_test.go", " ./pkg/feature_test.go "},
				}},
			}},
		}
		variant := PlanExecutionContract{
			Revision: PlanExecutionContractRevision, PlanID: "feature-plan-42",
			Atoms: []AtomExecutionContract{{
				AtomTitle: "Wire contract",
				Scope: PlanScopeContract{
					RequiredCoveragePaths: []string{"pkg/a.go", "pkg/b.go"},
					AllowedCandidatePaths: []string{"pkg/a.go", "pkg/c.go"},
					WriteAllowedPaths:     []string{"pkg/a.go", "pkg/**"}, WriteAllowedExplicit: true,
				},
				TDDObligations: []TDDObligation{{
					ID: "tdd-a", Command: "go test ./pkg", TestArtifactPaths: []string{"pkg/feature_test.go"},
				}},
			}},
		}

		normalized := base.Normalized()
		normalizedVariant := variant.Normalized()
		Expect(normalized.Revision).To(Equal(PlanExecutionContractRevision))
		Expect(normalized.ContractID).NotTo(Or(BeEmpty(), Equal("stale")))
		Expect(normalized.ContractHash).NotTo(Or(BeEmpty(), Equal("stale")))
		Expect(normalized.ContractID).To(Equal(normalizedVariant.ContractID))
		Expect(normalized.ContractHash).To(Equal(normalizedVariant.ContractHash))
		Expect(normalized.Atoms[0].Scope.RequiredCoveragePaths).To(Equal([]string{"pkg/a.go", "pkg/b.go"}))
		Expect(normalized.Atoms[0].Scope.AllowedCandidatePaths).To(Equal([]string{"pkg/c.go"}))
	})

	It("binds approved plan-artifact identity into the contract hash", func() {
		base := PlanExecutionContract{
			PlanID: "feature-plan-42",
			PlanArtifact: PlanArtifactBinding{
				ArtifactID: " plan-artifact-1 ", Revision: 2, Digest: " digest-1 ",
				WorkspaceFingerprint: " workspace-1 ", ApprovalEventID: " approval-1 ",
				ApprovalStatus: " Approved ", ApprovalMode: " Auto ",
			},
			Atoms: []AtomExecutionContract{{AtomID: "atom-1", Scope: PlanScopeContract{RequiredCoveragePaths: []string{"pkg/a.go"}}}},
		}
		variant := base
		variant.PlanArtifact.Digest = "digest-2"
		normalized := base.Normalized()
		Expect(normalized.PlanArtifact.ArtifactID).To(Equal("plan-artifact-1"))
		Expect(normalized.PlanArtifact.ApprovalStatus).To(Equal("approved"))
		Expect(normalized.PlanArtifact.ApprovalMode).To(Equal("auto"))
		Expect(normalized.ContractHash).NotTo(Equal(variant.Normalized().ContractHash))
	})

	It("normalizes and deduplicates authority source facts", func() {
		base := PlanExecutionContract{PlanID: "source-facts", AuthorityDeltas: []AuthorityDelta{{
			Kind: AuthorityDeltaKindNarrowWriteEnvelope, Path: "pkg/feature.go", Source: "accepted_source",
			SourceFacts: []SourceAuthorityFact{
				{Path: "./pkg/source.go", Symbol: " helper ", Signature: " void helper() "},
				{Path: "pkg/source.go", Symbol: "helper", Signature: "void helper()"},
			},
		}}}
		variant := PlanExecutionContract{PlanID: "source-facts", AuthorityDeltas: []AuthorityDelta{{
			Kind: AuthorityDeltaKindNarrowWriteEnvelope, Path: "pkg/feature.go", Source: "accepted_source",
			SourceFacts: []SourceAuthorityFact{{Path: "pkg/source.go", Symbol: "helper", Signature: "void helper()"}},
		}}}
		normalized := base.Normalized()
		Expect(normalized.ContractHash).To(Equal(variant.Normalized().ContractHash))
		Expect(normalized.AuthorityDeltas[0].SourceFacts).To(Equal([]SourceAuthorityFact{{
			Path: "pkg/source.go", Symbol: "helper", Signature: "void helper()",
		}}))
	})

	It("generates and enforces a stable acceptance-obligation ID", func() {
		atom := AtomExecutionContract{
			AtomID: "atom-1",
			Scope:  NewPlanScopeContract([]string{"pkg/feature.go"}, nil, nil).WithWriteAllowedPaths([]string{"pkg/feature.go"}),
			AcceptanceObligations: []AcceptanceEvidenceRequired{{
				Description: "Connected cistern water is consumed and water hauling is skipped.", Required: true, Source: "featureplanner.sdd",
			}},
			TerminalEvidence: strictTerminalEvidenceRequirements(),
		}.Normalized()
		Expect(atom.AcceptanceObligations).To(HaveLen(1))
		Expect(atom.AcceptanceObligations[0].ID).NotTo(BeEmpty())

		evidence := TerminalEvidenceInput{
			TerminalStatus: "success", ChangedPaths: []string{"pkg/feature.go"}, RequiredPathCoveragePaths: []string{"pkg/feature.go"},
			SourceEvidence:     []SourceSatisfiedEvidenceRef{{Path: "pkg/feature.go", Kind: "harness_observed_source"}},
			SourceSanityPassed: true, VerificationPassed: true, ReviewPassed: true,
		}
		Expect(hasViolationCode(ValidateAtomTerminalEvidenceInput(atom, evidence), "acceptance_evidence_missing")).To(BeTrue())
		evidence.AcceptanceEvidenceIDs = []string{atom.AcceptanceObligations[0].ID}
		Expect(hasViolationCode(ValidateAtomTerminalEvidenceInput(atom, evidence), "acceptance_evidence_missing")).To(BeFalse())
	})

	DescribeTable("validates acceptance-evidence threshold claims",
		func(threshold string, claim AcceptanceEvidenceClaim, expectedCodes []string) {
			atom := thresholdAcceptanceAtom(threshold)
			evidence := validTerminalEvidence()
			evidence.ChangedPaths = []string{"pkg/feature.go"}
			evidence.RequiredPathCoveragePaths = []string{"pkg/feature.go"}
			evidence.SourceEvidence = []SourceSatisfiedEvidenceRef{{Path: "pkg/feature.go", Kind: "harness_observed_source"}}
			evidence.RequiredTestResults, evidence.TDDCommandResults, evidence.TestArtifacts = nil, nil, nil
			if claim.ID != "" {
				evidence.AcceptanceEvidenceClaims = []AcceptanceEvidenceClaim{claim}
			}
			violations := ValidateAtomTerminalEvidenceInput(atom, evidence)
			for _, code := range expectedCodes {
				Expect(hasViolationCode(violations, code)).To(BeTrue(), "missing violation %q", code)
			}
			if len(expectedCodes) == 0 {
				Expect(violations).To(BeEmpty())
			}
		},
		Entry("missing claim", ">=80%", AcceptanceEvidenceClaim{}, []string{"acceptance_evidence_claim_missing"}),
		Entry("unparseable value", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "not-a-number", Met: true}, []string{"acceptance_evidence_claim_unparseable"}),
		Entry("unit mismatch", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80", Met: true}, []string{"acceptance_evidence_threshold_unit_mismatch"}),
		Entry("below threshold", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "79.9%", Met: false}, []string{"acceptance_evidence_threshold_not_met"}),
		Entry("at threshold", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80%", Met: true}, nil),
		Entry("above threshold", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "81.25%", Met: true}, nil),
		Entry("true contradicts below", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "72%", Met: true}, []string{"acceptance_evidence_claim_met_mismatch", "acceptance_evidence_threshold_not_met"}),
		Entry("false contradicts passing", ">=80%", AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80%", Met: false}, []string{"acceptance_evidence_claim_met_mismatch"}),
	)

	It("accepts structured authorized new files and roots only", func() {
		atom := AtomExecutionContract{
			AtomID:             "new-file",
			Scope:              NewPlanScopeContract([]string{"pkg/existing.go"}, nil, nil).WithWriteAllowedPaths([]string{"pkg/existing.go"}),
			AuthorizedNewFiles: []AuthorizedNewFile{{Path: "pkg/generated.go", Reason: "typed implementation output", Source: "feature_planner"}},
			AuthorizedNewRoots: []AuthorizedNewRoot{{Root: "pkg/generated", Reason: "typed generated package", Source: "feature_planner"}},
			TerminalEvidence:   strictTerminalEvidenceRequirements(),
		}
		accepted := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
			TerminalStatus: "success", ChangedPaths: []string{"pkg/existing.go", "pkg/generated.go", "pkg/generated/helper.go"},
			SourceEvidence:     []SourceSatisfiedEvidenceRef{{Path: "pkg/existing.go", Kind: "harness_observed_source"}},
			SourceSanityPassed: true, VerificationPassed: true, ReviewPassed: true,
		})
		Expect(hasViolationCode(accepted, "changed_path_outside_write_envelope")).To(BeFalse())
		rejected := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
			TerminalStatus: "success", ChangedPaths: []string{"pkg/invented.go"},
			SourceEvidence:     []SourceSatisfiedEvidenceRef{{Path: "pkg/existing.go", Kind: "harness_observed_source"}},
			SourceSanityPassed: true, VerificationPassed: true, ReviewPassed: true,
		})
		Expect(hasViolationCode(rejected, "changed_path_outside_write_envelope")).To(BeTrue())
	})

	It("detects changed TDD commands and artifacts", func() {
		evidence := validTerminalEvidence()
		evidence.TDDCommandResults = []TDDCommandEvidence{{
			ID: "red-test", Command: "go test ./internal/other", Passed: true,
			ArtifactPaths: []string{"internal/plan/other_test.go"},
		}}
		evidence.TestArtifacts = []string{"internal/plan/other_test.go"}
		violations := ValidateAtomTerminalEvidenceInput(tddAtom(), evidence)
		expectViolationCodes(violations, "tdd_command_changed", "required_test_artifact_missing", "tdd_test_artifact_changed")
	})

	It("rejects a missing required test artifact", func() {
		evidence := validTerminalEvidence()
		evidence.TestArtifacts = nil
		evidence.TDDCommandResults[0].ArtifactPaths = nil
		Expect(hasViolationCode(ValidateAtomTerminalEvidenceInput(tddAtom(), evidence), "required_test_artifact_missing")).To(BeTrue())
	})

	It("rejects terminal success without required evidence", func() {
		violations := ValidateAtomTerminalEvidenceInput(tddAtom(), TerminalEvidenceInput{TerminalStatus: "success"})
		expectViolationCodes(violations,
			"terminal_success_missing_evidence", "changed_path_coverage_missing", "required_path_coverage_missing",
			"required_source_evidence_missing", "source_sanity_failed_or_missing", "verification_failed_or_missing",
			"review_failed_or_missing", "required_test_missing", "required_tdd_command_missing",
		)
	})
})

func strictTerminalEvidenceRequirements() TerminalEvidenceRequirements {
	return TerminalEvidenceRequirements{
		Explicit: true, RequireSourceEvidence: true, RequireChangedPathCoverage: true,
		RequireRequiredPathCoverage: true, RequireSourceSanity: true, RequireVerification: true,
		RequireReview: true, RequireRequiredTests: true, RequireTestArtifacts: true,
	}
}

func thresholdAcceptanceAtom(threshold string) AtomExecutionContract {
	return AtomExecutionContract{
		AtomID:                "atom-1",
		Scope:                 NewPlanScopeContract([]string{"pkg/feature.go"}, nil, nil).WithWriteAllowedPaths([]string{"pkg/feature.go"}),
		AcceptanceObligations: []AcceptanceEvidenceRequired{{ID: "coverage-threshold", Required: true, Threshold: threshold}},
		TerminalEvidence:      strictTerminalEvidenceRequirements(),
	}.Normalized()
}

func tddAtom() AtomExecutionContract {
	return AtomExecutionContract{
		AtomID: "tdd-atom",
		Scope: NewPlanScopeContract([]string{"internal/plan/execution.go"}, nil, nil).
			WithWriteAllowedPaths([]string{"internal/plan/execution.go", "internal/plan/execution_test.go"}),
		RequiredTests: []RequiredTest{{ID: "go-test-plan", Command: "go test ./internal/contracts/plan"}},
		TDDObligations: []TDDObligation{{
			ID: "red-test", Command: "go test ./internal/contracts/plan -run TestContract",
			TestArtifactPaths: []string{"internal/plan/execution_test.go"},
		}},
		SourceEvidenceObligations: []SourceEvidenceObligation{{
			ID: "source-read", Path: "internal/plan/execution.go", Kind: "harness_observed_source", Required: true,
		}},
		TerminalEvidence: strictTerminalEvidenceRequirements(),
	}
}

func validTerminalEvidence() TerminalEvidenceInput {
	return TerminalEvidenceInput{
		TerminalStatus:            "success",
		ChangedPaths:              []string{"internal/plan/execution.go", "internal/plan/execution_test.go"},
		RequiredPathCoveragePaths: []string{"internal/plan/execution.go"},
		SourceEvidence:            []SourceSatisfiedEvidenceRef{{Path: "internal/plan/execution.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:        true, VerificationPassed: true, ReviewPassed: true,
		RequiredTestResults: []RequiredTestEvidence{{ID: "go-test-plan", Command: "go test ./internal/contracts/plan", Passed: true}},
		TDDCommandResults: []TDDCommandEvidence{{
			ID: "red-test", Command: "go test ./internal/contracts/plan -run TestContract", Passed: true,
			ArtifactPaths: []string{"internal/plan/execution_test.go"},
		}},
		TestArtifacts: []string{"internal/plan/execution_test.go"},
	}
}

func expectViolationCodes(violations []PlanExecutionContractViolation, codes ...string) {
	GinkgoHelper()
	for _, code := range codes {
		Expect(hasViolationCode(violations, code)).To(BeTrue(), "missing violation %q", code)
	}
}

func hasViolationCode(violations []PlanExecutionContractViolation, code string) bool {
	for _, violation := range violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
