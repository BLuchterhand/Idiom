package productworkflow

import "fmt"

// IntakeFailure reports a single validator failure with enough detail for an
// agent to target a repair.
type IntakeFailure struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Artifact string   `json:"artifact"`
	Path     string   `json:"path"`
	Fields   []string `json:"fields"`
}

// IntakeResult wraps the pass/fail outcome of an intake validation pass.
type IntakeResult struct {
	Passed   bool            `json:"passed"`
	Failures []IntakeFailure `json:"failures,omitempty"`
}

// ValidatePRDIntake checks a PRDDocument against the intake-template
// requirements. This implements the same field-level checks as validatePRD
// but returns failure codes usable for targeted repair by an agent in the
// creation iteration lane.
func ValidatePRDIntake(doc PRDDocument) IntakeResult {
	var failures []IntakeFailure
	fm := doc.FrontMatter

	if fm.SchemaVersion == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_schema_version", Message: "PRD front matter missing schema_version",
			Artifact: "prd", Path: "prd.md", Fields: []string{"schema_version"},
		})
	}
	if fm.PRDID == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_prd_id", Message: "PRD front matter missing prd_id",
			Artifact: "prd", Path: "prd.md", Fields: []string{"prd_id"},
		})
	}
	if fm.Status == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_status", Message: "PRD front matter missing status",
			Artifact: "prd", Path: "prd.md", Fields: []string{"status"},
		})
	} else if err := ValidateArtifactState(fm.Status); err != nil {
		failures = append(failures, IntakeFailure{
			Code: "invalid_status", Message: "PRD front matter: " + err.Error(),
			Artifact: "prd", Path: "prd.md", Fields: []string{"status"},
		})
	}
	if fm.TaskID == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_task_id", Message: "PRD front matter missing task_id",
			Artifact: "prd", Path: "prd.md", Fields: []string{"task_id"},
		})
	}
	if fm.CreatedAt == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_created_at", Message: "PRD front matter missing created_at",
			Artifact: "prd", Path: "prd.md", Fields: []string{"created_at"},
		})
	}

	if len(doc.Requirements) == 0 {
		failures = append(failures, IntakeFailure{
			Code: "no_requirements", Message: "PRD has no requirements",
			Artifact: "prd", Path: "prd.md", Fields: []string{"requirements"},
		})
	}

	for i, req := range doc.Requirements {
		prefix := "PRD requirement " + req.ID
		if req.ID == "" {
			prefix = "PRD requirement " + fmt.Sprintf("%d", i)
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_id", Message: prefix + " missing id",
				Artifact: "prd", Path: "prd.md", Fields: []string{"id"},
			})
		}
		if req.Priority == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_priority", Message: prefix + " missing priority",
				Artifact: "prd", Path: "prd.md", Fields: []string{"priority"},
			})
		}
		if req.Statement == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_statement", Message: prefix + " missing statement",
				Artifact: "prd", Path: "prd.md", Fields: []string{"statement"},
			})
		}
		if req.OwnerAlignment == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_owner_alignment", Message: prefix + " missing owner_alignment",
				Artifact: "prd", Path: "prd.md", Fields: []string{"owner_alignment"},
			})
		}
		if req.Testability == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_testability", Message: prefix + " missing testability",
				Artifact: "prd", Path: "prd.md", Fields: []string{"testability"},
			})
		}
		if req.Metric == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_metric", Message: prefix + " missing metric",
				Artifact: "prd", Path: "prd.md", Fields: []string{"metric"},
			})
		}
		if len(req.EvidenceRefs) == 0 {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_evidence_refs", Message: prefix + " missing evidence_refs",
				Artifact: "prd", Path: "prd.md", Fields: []string{"evidence_refs"},
			})
		}
		if len(req.TDDObligations) == 0 {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_tdd_obligations", Message: prefix + " missing tdd_obligations",
				Artifact: "prd", Path: "prd.md", Fields: []string{"tdd_obligations"},
			})
		}
		if !req.OpenQuestionsPresent {
			failures = append(failures, IntakeFailure{
				Code: "missing_requirement_open_questions", Message: prefix + " missing open_questions",
				Artifact: "prd", Path: "prd.md", Fields: []string{"open_questions"},
			})
		}
	}

	return IntakeResult{Passed: len(failures) == 0, Failures: failures}
}

// ValidateADRIndexIntake checks an ADRIndex against the intake-template
// requirements.
func ValidateADRIndexIntake(idx ADRIndex) IntakeResult {
	var failures []IntakeFailure

	if idx.SchemaVersion == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_index_schema_version", Message: "ADR index missing schema_version",
			Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"schema_version"},
		})
	}
	if idx.EffortID == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_index_effort_id", Message: "ADR index missing effort_id",
			Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"effort_id"},
		})
	}
	if len(idx.ADRs) == 0 {
		failures = append(failures, IntakeFailure{
			Code: "no_adrs_in_index", Message: "ADR index has no ADR entries",
			Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"adrs"},
		})
	}
	for i, entry := range idx.ADRs {
		prefix := "ADR index entry " + entry.ADRID
		if entry.ADRID == "" {
			prefix = "ADR index entry " + fmt.Sprintf("%d", i)
			failures = append(failures, IntakeFailure{
				Code: "missing_adr_index_entry_id", Message: prefix + " missing adr_id",
				Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"adr_id"},
			})
		}
		if entry.Status == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_adr_index_entry_status", Message: prefix + " missing status",
				Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"status"},
			})
		}
		if entry.Path == "" {
			failures = append(failures, IntakeFailure{
				Code: "missing_adr_index_entry_path", Message: prefix + " missing path",
				Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"path"},
			})
		}
		if len(entry.PRDRefs) == 0 {
			failures = append(failures, IntakeFailure{
				Code: "missing_adr_index_entry_prd_refs", Message: prefix + " missing prd_refs",
				Artifact: "adr_index", Path: "adrs/index.json", Fields: []string{"prd_refs"},
			})
		}
	}

	return IntakeResult{Passed: len(failures) == 0, Failures: failures}
}

// ValidateADRIntake checks an ADRDocument against the intake-template
// requirements. This implements the same field-level checks as validateADR
// but returns failure codes usable for targeted repair.
func ValidateADRIntake(doc ADRDocument, adrPath string) IntakeResult {
	var failures []IntakeFailure
	fm := doc.FrontMatter

	if fm.SchemaVersion == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_schema_version", Message: "ADR front matter missing schema_version",
			Artifact: "adr", Path: adrPath, Fields: []string{"schema_version"},
		})
	}
	if fm.ADRID == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_id", Message: "ADR front matter missing adr_id",
			Artifact: "adr", Path: adrPath, Fields: []string{"adr_id"},
		})
	}
	if fm.Status == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_status", Message: "ADR front matter missing status",
			Artifact: "adr", Path: adrPath, Fields: []string{"status"},
		})
	}
	if len(fm.PRDRefs) == 0 {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_prd_refs", Message: "ADR front matter missing prd_refs",
			Artifact: "adr", Path: adrPath, Fields: []string{"prd_refs"},
		})
	}
	if len(fm.EvidenceRefs) == 0 {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_evidence_refs", Message: "ADR front matter missing evidence_refs",
			Artifact: "adr", Path: adrPath, Fields: []string{"evidence_refs"},
		})
	}
	if len(fm.TDDObligations) == 0 {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_tdd_obligations", Message: "ADR front matter missing tdd_obligations",
			Artifact: "adr", Path: adrPath, Fields: []string{"tdd_obligations"},
		})
	}
	if fm.CoverageTier == "" {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_coverage_tier", Message: "ADR front matter missing coverage_tier",
			Artifact: "adr", Path: adrPath, Fields: []string{"coverage_tier"},
		})
	} else if !allowedCoverageTiers[fm.CoverageTier] {
		failures = append(failures, IntakeFailure{
			Code: "invalid_adr_coverage_tier", Message: "ADR front matter invalid coverage_tier " + fm.CoverageTier,
			Artifact: "adr", Path: adrPath, Fields: []string{"coverage_tier"},
		})
	}

	sections := doc.Sections
	if !sections.HasContext {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_context", Message: "ADR body missing ## Context section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Context"},
		})
	}
	if !sections.HasDecision {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_decision", Message: "ADR body missing ## Decision section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Decision"},
		})
	}
	if !sections.HasAlternativesConsidered {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_alternatives", Message: "ADR body missing ## Alternatives Considered section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Alternatives Considered"},
		})
	}
	if !sections.HasEvidence {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_evidence", Message: "ADR body missing ## Evidence section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Evidence"},
		})
	}
	if !sections.HasConsequences {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_consequences", Message: "ADR body missing ## Consequences section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Consequences"},
		})
	}
	if !sections.HasCompatibilityAndMigration {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_compatibility", Message: "ADR body missing ## Compatibility and Migration section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Compatibility and Migration"},
		})
	}
	if !sections.HasTDDObligations {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_tdd_obligations", Message: "ADR body missing ## TDD Obligations section",
			Artifact: "adr", Path: adrPath, Fields: []string{"TDD Obligations"},
		})
	}
	if !sections.HasRollback {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_rollback", Message: "ADR body missing ## Rollback section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Rollback"},
		})
	}
	if !sections.HasOpenQuestions {
		failures = append(failures, IntakeFailure{
			Code: "missing_adr_section_open_questions", Message: "ADR body missing ## Open Questions section",
			Artifact: "adr", Path: adrPath, Fields: []string{"Open Questions"},
		})
	}

	return IntakeResult{Passed: len(failures) == 0, Failures: failures}
}
