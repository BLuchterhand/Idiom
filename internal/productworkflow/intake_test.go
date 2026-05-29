package productworkflow

import "testing"

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

func TestValidatePRDIntakePass(t *testing.T) {
	doc := makeValidPRDDocument()
	result := ValidatePRDIntake(doc)
	if !result.Passed {
		t.Fatalf("expected passed, got failures: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingSchemaVersion(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.SchemaVersion = ""
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_schema_version") {
		t.Fatalf("expected missing_schema_version, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingPRDID(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.PRDID = ""
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_prd_id") {
		t.Fatalf("expected missing_prd_id, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingStatus(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.Status = ""
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_status") {
		t.Fatalf("expected missing_status, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeInvalidStatus(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.Status = "bogus"
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "invalid_status") {
		t.Fatalf("expected invalid_status, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingTaskID(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.TaskID = ""
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_task_id") {
		t.Fatalf("expected missing_task_id, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingCreatedAt(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.FrontMatter.CreatedAt = ""
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_created_at") {
		t.Fatalf("expected missing_created_at, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeNoRequirements(t *testing.T) {
	doc := makeValidPRDDocument()
	doc.Requirements = nil
	result := ValidatePRDIntake(doc)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "no_requirements") {
		t.Fatalf("expected no_requirements, got: %+v", result.Failures)
	}
}

func TestValidatePRDIntakeMissingRequirementFields(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			doc := makeValidPRDDocument()
			tt.mut(&doc.Requirements[0])
			result := ValidatePRDIntake(doc)
			if result.Passed {
				t.Fatal("expected failed")
			}
			for _, code := range tt.codes {
				if !hasCode(result, code) {
					t.Errorf("expected code %s, got: %+v", code, result.Failures)
				}
			}
		})
	}
}

func TestValidatePRDIntakeMultipleRequirements(t *testing.T) {
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
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_requirement_priority") {
		t.Fatalf("expected missing_requirement_priority, got: %+v", result.Failures)
	}
}


func TestValidatePRDIntakeCombinedFailures(t *testing.T) {
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
	if result.Passed {
		t.Fatal("expected failed with multiple failures")
	}
	if len(result.Failures) < 2 {
		t.Fatalf("expected at least 2 failures, got %d: %+v", len(result.Failures), result.Failures)
	}
	requiredCodes := []string{"missing_prd_id", "missing_created_at", "missing_requirement_priority",
		"missing_requirement_statement", "missing_requirement_tdd_obligations", "missing_requirement_open_questions"}
	for _, code := range requiredCodes {
		if !hasCode(result, code) {
			t.Errorf("expected failure code %s not found in: %+v", code, result.Failures)
		}
	}
}

func makeValidADRIndex() ADRIndex {
	return ADRIndex{
		SchemaVersion: "idiom.adr_index.v1",
		EffortID:      "effort-001",
		ADRs: []ADRIndexEntry{
			{ADRID: "ADR-001", Title: "Choose Database", Status: string(ArtifactStateDraft), Path: "ADR-001.md", CoverageTier: "unit", PRDRefs: []string{"REQ-001"}, CreatedAt: "2026-06-17T00:00:00Z"},
		},
	}
}

func TestValidateADRIndexIntakePass(t *testing.T) {
	idx := makeValidADRIndex()
	result := ValidateADRIndexIntake(idx)
	if !result.Passed {
		t.Fatalf("expected passed, got: %+v", result.Failures)
	}
}

func TestValidateADRIndexIntakeMissingSchemaVersion(t *testing.T) {
	idx := makeValidADRIndex()
	idx.SchemaVersion = ""
	result := ValidateADRIndexIntake(idx)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_adr_index_schema_version") {
		t.Fatalf("expected missing_adr_index_schema_version, got: %+v", result.Failures)
	}
}

func TestValidateADRIndexIntakeMissingEffortID(t *testing.T) {
	idx := makeValidADRIndex()
	idx.EffortID = ""
	result := ValidateADRIndexIntake(idx)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "missing_adr_index_effort_id") {
		t.Fatalf("expected missing_adr_index_effort_id, got: %+v", result.Failures)
	}
}

func TestValidateADRIndexIntakeNoADRs(t *testing.T) {
	idx := makeValidADRIndex()
	idx.ADRs = nil
	result := ValidateADRIndexIntake(idx)
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "no_adrs_in_index") {
		t.Fatalf("expected no_adrs_in_index, got: %+v", result.Failures)
	}
}

func TestValidateADRIndexIntakeMissingEntryFields(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			idx := makeValidADRIndex()
			tt.mut(&idx.ADRs[0])
			result := ValidateADRIndexIntake(idx)
			if result.Passed {
				t.Fatal("expected failed")
			}
			for _, code := range tt.codes {
				if !hasCode(result, code) {
					t.Errorf("expected code %s, got: %+v", code, result.Failures)
				}
			}
		})
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

func TestValidateADRIntakePass(t *testing.T) {
	doc := makeValidADRDocument()
	result := ValidateADRIntake(doc, "ADR-001.md")
	if !result.Passed {
		t.Fatalf("expected passed, got: %+v", result.Failures)
	}
}

func TestValidateADRIntakeMissingFrontMatter(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			doc := makeValidADRDocument()
			tt.mut(&doc.FrontMatter)
			result := ValidateADRIntake(doc, "ADR-001.md")
			if result.Passed {
				t.Fatal("expected failed")
			}
			if !hasCode(result, tt.code) {
				t.Fatalf("expected %s, got: %+v", tt.code, result.Failures)
			}
		})
	}
}

func TestValidateADRIntakeInvalidCoverageTier(t *testing.T) {
	doc := makeValidADRDocument()
	doc.FrontMatter.CoverageTier = "bogus"
	result := ValidateADRIntake(doc, "ADR-001.md")
	if result.Passed {
		t.Fatal("expected failed")
	}
	if !hasCode(result, "invalid_adr_coverage_tier") {
		t.Fatalf("expected invalid_adr_coverage_tier, got: %+v", result.Failures)
	}
}

func TestValidateADRIntakeMissingSections(t *testing.T) {
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
		t.Run(tt.name, func(t *testing.T) {
			doc := makeValidADRDocument()
			tt.mut(&doc.Sections)
			result := ValidateADRIntake(doc, "ADR-001.md")
			if result.Passed {
				t.Fatal("expected failed")
			}
			if !hasCode(result, tt.code) {
				t.Fatalf("expected %s, got: %+v", tt.code, result.Failures)
			}
		})
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

