package productworkflow

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

func makeValidPRDDocument() PRDDocument {
	return PRDDocument{
		FrontMatter: PRDFrontMatter{
			SchemaVersion: "idiom.prd.v1",
			PRDID:         "PRD-001",
			Status:        string(ArtifactStateDraft),
			TaskID:        "task-001",
			CreatedAt:     "2026-06-17T00:00:00Z",
		},
		Requirements: []PRDRequirement{
			{
				ID:                   "REQ-001",
				Priority:             "P0",
				Statement:            "Users can log in with email.",
				EvidenceRefs:         []string{"E1"},
				TDDObligations:       []string{"T1"},
				OwnerAlignment:       "Product team",
				Testability:          "Login acceptance test",
				Metric:               "Login success rate > 99%",
				OpenQuestions:        []string{"SSO later?"},
				OpenQuestionsPresent: true,
			},
		},
	}
}

var _ = ginkgo.Describe("intake", func() {
	ginkgo.It("ValidatePRDIntakePass", func() {
		doc := makeValidPRDDocument()
		result := ValidatePRDIntake(doc)
		gomega.Expect(!result.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected passed, got failures: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingSchemaVersion", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.SchemaVersion = ""

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_schema_version")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_schema_version, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingPRDID", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.PRDID = ""

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_prd_id")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_prd_id, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingStatus", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.Status = ""

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_status")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_status, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeInvalidStatus", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.Status = "bogus"

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "invalid_status")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_status, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingTaskID", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.TaskID = ""

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_task_id")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_task_id, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingCreatedAt", func() {
		doc := makeValidPRDDocument()

		doc.FrontMatter.CreatedAt = ""

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_created_at")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_created_at, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeNoRequirements", func() {
		doc := makeValidPRDDocument()

		doc.Requirements = nil

		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "no_requirements")).To(gomega.BeFalse(), fmt.Sprintf("expected no_requirements, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeMissingRequirementFields", func() {
		tests := []struct {
			name  string
			mut   func(*PRDRequirement)
			codes []string
		}{
			{"missing_id", func(r *PRDRequirement) { r.ID = "" }, []string{"missing_requirement_id"}},
			{"missing_priority", func(r *PRDRequirement) { r.Priority = "" }, []string{"missing_requirement_priority"}},
			{"missing_statement", func(r *PRDRequirement) { r.Statement = "" }, []string{"missing_requirement_statement"}},
			{"missing_owner_alignment", func(r *PRDRequirement) { r.OwnerAlignment = "" }, []string{"missing_requirement_owner_alignment"}},
			{"missing_testability", func(r *PRDRequirement) { r.Testability = "" }, []string{"missing_requirement_testability"}},
			{"missing_metric", func(r *PRDRequirement) { r.Metric = "" }, []string{"missing_requirement_metric"}},
			{"missing_evidence_refs", func(r *PRDRequirement) { r.EvidenceRefs = nil }, []string{"missing_requirement_evidence_refs"}},
			{"missing_tdd_obligations", func(r *PRDRequirement) { r.TDDObligations = nil }, []string{"missing_requirement_tdd_obligations"}},
			{"missing_open_questions", func(r *PRDRequirement) { r.OpenQuestionsPresent = false }, []string{"missing_requirement_open_questions"}},
		}
		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				doc := makeValidPRDDocument()
				tt.mut(&doc.Requirements[0])

				result := ValidatePRDIntake(doc)
				gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))

				for _, code := range tt.codes {
					gomega.Expect(!hasCode(result, code)).To(gomega.BeFalse(), fmt.Sprintf("expected code %s, got: %+v", code, result.Failures))

				}
			})
		}
	})
	ginkgo.It("ValidatePRDIntakeMultipleRequirements", func() {
		doc := PRDDocument{
			FrontMatter: PRDFrontMatter{
				SchemaVersion: "idiom.prd.v1", PRDID: "PRD-001", Status: string(ArtifactStateDraft),
				TaskID: "task-001", CreatedAt: "2026-06-17T00:00:00Z",
			},
			Requirements: []PRDRequirement{
				{ID: "REQ-001", Priority: "P0", Statement: "First.", EvidenceRefs: []string{"E1"}, TDDObligations: []string{"T1"}, OwnerAlignment: "Team", Testability: "T", Metric: "M", OpenQuestionsPresent: true},
				{ID: "REQ-002", Priority: "", Statement: "Second.", EvidenceRefs: []string{"E2"}, TDDObligations: []string{"T2"}, OwnerAlignment: "Team", Testability: "T", Metric: "M", OpenQuestionsPresent: true},
				{ID: "REQ-003", Priority: "P2", Statement: "Third.", EvidenceRefs: []string{"E3"}, TDDObligations: []string{"T3"}, OwnerAlignment: "Team", Testability: "T", Metric: "M", OpenQuestionsPresent: true},
			},
		}
		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_requirement_priority")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_requirement_priority, got: %+v", result.Failures))

	})
	ginkgo.It("ValidatePRDIntakeCombinedFailures", func() {
		doc := PRDDocument{
			FrontMatter: PRDFrontMatter{
				// Missing prd_id AND created_at.
				SchemaVersion: "idiom.prd.v1", Status: string(ArtifactStateDraft),
				TaskID: "task-001", CreatedAt: "",
			},
			Requirements: []PRDRequirement{
				{ID: "REQ-001", Priority: "", Statement: "", EvidenceRefs: []string{"E1"},
					TDDObligations: []string{}, OwnerAlignment: "Team", Testability: "T",
					Metric: "M", OpenQuestionsPresent: false},
			},
		}
		result := ValidatePRDIntake(doc)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed with multiple failures"))
		gomega.Expect(len(result.Failures) < 2).To(gomega.BeFalse(), fmt.Sprintf("expected at least 2 failures, got %d: %+v", len(result.Failures), result.Failures))

		requiredCodes := []string{"missing_prd_id", "missing_created_at", "missing_requirement_priority",
			"missing_requirement_statement", "missing_requirement_tdd_obligations", "missing_requirement_open_questions"}
		for _, code := range requiredCodes {
			gomega.Expect(!hasCode(result, code)).To(gomega.BeFalse(), fmt.Sprintf("expected failure code %s not found in: %+v", code, result.Failures))

		}
	})
	ginkgo.It("ValidateADRIndexIntakePass", func() {
		idx := makeValidADRIndex()
		result := ValidateADRIndexIntake(idx)
		gomega.Expect(!result.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected passed, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIndexIntakeMissingSchemaVersion", func() {
		idx := makeValidADRIndex()

		idx.SchemaVersion = ""

		result := ValidateADRIndexIntake(idx)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_adr_index_schema_version")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_adr_index_schema_version, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIndexIntakeMissingEffortID", func() {
		idx := makeValidADRIndex()

		idx.EffortID = ""

		result := ValidateADRIndexIntake(idx)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "missing_adr_index_effort_id")).To(gomega.BeFalse(), fmt.Sprintf("expected missing_adr_index_effort_id, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIndexIntakeNoADRs", func() {
		idx := makeValidADRIndex()

		idx.ADRs = nil

		result := ValidateADRIndexIntake(idx)
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "no_adrs_in_index")).To(gomega.BeFalse(), fmt.Sprintf("expected no_adrs_in_index, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIndexIntakeMissingEntryFields", func() {
		tests := []struct {
			name  string
			mut   func(*ADRIndexEntry)
			codes []string
		}{
			{"missing_adr_id", func(e *ADRIndexEntry) { e.ADRID = "" }, []string{"missing_adr_index_entry_id"}},
			{"missing_status", func(e *ADRIndexEntry) { e.Status = "" }, []string{"missing_adr_index_entry_status"}},
			{"missing_path", func(e *ADRIndexEntry) { e.Path = "" }, []string{"missing_adr_index_entry_path"}},
			{"missing_prd_refs", func(e *ADRIndexEntry) { e.PRDRefs = nil }, []string{"missing_adr_index_entry_prd_refs"}},
		}
		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				idx := makeValidADRIndex()
				tt.mut(&idx.ADRs[0])

				result := ValidateADRIndexIntake(idx)
				gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))

				for _, code := range tt.codes {
					gomega.Expect(!hasCode(result, code)).To(gomega.BeFalse(), fmt.Sprintf("expected code %s, got: %+v", code, result.Failures))

				}
			})
		}
	})
	ginkgo.It("ValidateADRIntakePass", func() {
		doc := makeValidADRDocument()
		result := ValidateADRIntake(doc, "ADR-001.md")
		gomega.Expect(!result.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected passed, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIntakeMissingFrontMatter", func() {
		tests := []struct {
			name string
			mut  func(*ADRFrontMatter)
			code string
		}{
			{"schema_version", func(fm *ADRFrontMatter) { fm.SchemaVersion = "" }, "missing_adr_schema_version"},
			{"adr_id", func(fm *ADRFrontMatter) { fm.ADRID = "" }, "missing_adr_id"},
			{"status", func(fm *ADRFrontMatter) { fm.Status = "" }, "missing_adr_status"},
			{"prd_refs", func(fm *ADRFrontMatter) { fm.PRDRefs = nil }, "missing_adr_prd_refs"},
			{"evidence_refs", func(fm *ADRFrontMatter) { fm.EvidenceRefs = nil }, "missing_adr_evidence_refs"},
			{"tdd_obligations", func(fm *ADRFrontMatter) { fm.TDDObligations = nil }, "missing_adr_tdd_obligations"},
			{"coverage_tier", func(fm *ADRFrontMatter) { fm.CoverageTier = "" }, "missing_adr_coverage_tier"},
		}
		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				doc := makeValidADRDocument()
				tt.mut(&doc.FrontMatter)

				result := ValidateADRIntake(doc, "ADR-001.md")
				gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
				gomega.Expect(!hasCode(result, tt.code)).To(gomega.BeFalse(), fmt.Sprintf("expected %s, got: %+v", tt.code, result.Failures))

			})
		}
	})
	ginkgo.It("ValidateADRIntakeInvalidCoverageTier", func() {
		doc := makeValidADRDocument()

		doc.FrontMatter.CoverageTier = "bogus"

		result := ValidateADRIntake(doc, "ADR-001.md")
		gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
		gomega.Expect(!hasCode(result, "invalid_adr_coverage_tier")).To(gomega.BeFalse(), fmt.Sprintf("expected invalid_adr_coverage_tier, got: %+v", result.Failures))

	})
	ginkgo.It("ValidateADRIntakeMissingSections", func() {
		tests := []struct {
			name string
			mut  func(*ADRSections)
			code string
		}{
			{"Context", func(s *ADRSections) { s.HasContext = false }, "missing_adr_section_context"},
			{"Decision", func(s *ADRSections) { s.HasDecision = false }, "missing_adr_section_decision"},
			{"Alternatives Considered", func(s *ADRSections) { s.HasAlternativesConsidered = false }, "missing_adr_section_alternatives"},
			{"Evidence", func(s *ADRSections) { s.HasEvidence = false }, "missing_adr_section_evidence"},
			{"Consequences", func(s *ADRSections) { s.HasConsequences = false }, "missing_adr_section_consequences"},
			{"Compatibility", func(s *ADRSections) { s.HasCompatibilityAndMigration = false }, "missing_adr_section_compatibility"},
			{"TDD Obligations", func(s *ADRSections) { s.HasTDDObligations = false }, "missing_adr_section_tdd_obligations"},
			{"Rollback", func(s *ADRSections) { s.HasRollback = false }, "missing_adr_section_rollback"},
			{"Open Questions", func(s *ADRSections) { s.HasOpenQuestions = false }, "missing_adr_section_open_questions"},
		}
		for _, tt := range tests {
			ginkgo.By(tt.name, func() {
				doc := makeValidADRDocument()
				tt.mut(&doc.Sections)

				result := ValidateADRIntake(doc, "ADR-001.md")
				gomega.Expect(result.Passed).To(gomega.BeFalse(), fmt.Sprint("expected failed"))
				gomega.Expect(!hasCode(result, tt.code)).To(gomega.BeFalse(), fmt.Sprintf("expected %s, got: %+v", tt.code, result.Failures))

			})
		}
	})
})

func makeValidADRIndex() ADRIndex {
	return ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      "effort-001",
		ADRs: []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Choose Database", Status: string(ArtifactStateDraft), Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-06-17T00:00:00Z"},
		},
	}
}

func makeValidADRDocument() ADRDocument {
	return ADRDocument{
		FrontMatter: ADRFrontMatter{
			SchemaVersion: "idiom.adr.v1", ADRID: "ADR-001", Status: string(ArtifactStateDraft),
			PRDRefs: []string{"REQ-001"}, EvidenceRefs: []string{"E1"}, TDDObligations: []string{"T1"}, CoverageTier: "unit",
		},
		Sections: ADRSections{
			HasContext: true, HasDecision: true, HasAlternativesConsidered: true,
			HasEvidence: true, HasConsequences: true, HasCompatibilityAndMigration: true,
			HasTDDObligations: true, HasRollback: true, HasOpenQuestions: true,
		},
	}
}

func hasCode(result IntakeResult, code string) bool {
	for _, f := range result.Failures {
		if f.Code == code {
			return true
		}
	}

	return false
}
