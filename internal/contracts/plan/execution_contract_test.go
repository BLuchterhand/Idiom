package plan

import (
	"slices"
	"testing"
)

func TestPlanExecutionContractNormalizesStableIDAndHash(t *testing.T) {
	base := PlanExecutionContract{
		Revision:     "",
		ContractID:   "stale",
		ContractHash: "stale",
		PlanID:       " Feature Plan 42 ",
		Atoms: []AtomExecutionContract{{
			AtomTitle: "Wire contract",
			Scope: PlanScopeContract{
				RequiredCoveragePaths: []string{" ./pkg/b.go ", "pkg/a.go", "pkg\\a.go"},
				AllowedCandidatePaths: []string{"pkg/c.go", "pkg/a.go"},
				WriteAllowedPaths:     []string{"pkg/**", "pkg/a.go"},
				WriteAllowedExplicit:  true,
			},
			TDDObligations: []TDDObligation{{
				ID:                "TDD A",
				Command:           " go   test ./pkg ",
				TestArtifactPaths: []string{"pkg/feature_test.go", " ./pkg/feature_test.go "},
			}},
		}},
	}
	variant := PlanExecutionContract{
		Revision: PlanExecutionContractRevision,
		PlanID:   "feature-plan-42",
		Atoms: []AtomExecutionContract{{
			AtomTitle: "Wire contract",
			Scope: PlanScopeContract{
				RequiredCoveragePaths: []string{"pkg/a.go", "pkg/b.go"},
				AllowedCandidatePaths: []string{"pkg/a.go", "pkg/c.go"},
				WriteAllowedPaths:     []string{"pkg/a.go", "pkg/**"},
				WriteAllowedExplicit:  true,
			},
			TDDObligations: []TDDObligation{{
				ID:                "tdd-a",
				Command:           "go test ./pkg",
				TestArtifactPaths: []string{"pkg/feature_test.go"},
			}},
		}},
	}

	normalized := base.Normalized()
	normalizedVariant := variant.Normalized()

	if normalized.Revision != PlanExecutionContractRevision {
		t.Fatalf("revision = %q, want %q", normalized.Revision, PlanExecutionContractRevision)
	}

	if normalized.ContractID == "" || normalized.ContractHash == "" || normalized.ContractID == "stale" || normalized.ContractHash == "stale" {
		t.Fatalf("contract identity was not regenerated: id=%q hash=%q", normalized.ContractID, normalized.ContractHash)
	}

	if normalized.ContractID != normalizedVariant.ContractID || normalized.ContractHash != normalizedVariant.ContractHash {
		t.Fatalf("normalized variants produced different identities: %#v vs %#v", normalized, normalizedVariant)
	}

	atom := normalized.Atoms[0]
	if got, want := atom.Scope.RequiredCoveragePaths, []string{"pkg/a.go", "pkg/b.go"}; !slices.Equal(got, want) {
		t.Fatalf("required paths = %#v, want %#v", got, want)
	}

	if got, want := atom.Scope.AllowedCandidatePaths, []string{"pkg/c.go"}; !slices.Equal(got, want) {
		t.Fatalf("allowed paths = %#v, want %#v", got, want)
	}
}

func TestPlanExecutionContractCarriesApprovedPlanArtifactBinding(t *testing.T) {
	base := PlanExecutionContract{
		PlanID: "feature-plan-42",
		PlanArtifact: PlanArtifactBinding{
			ArtifactID:           " plan-artifact-1 ",
			Revision:             2,
			Digest:               " digest-1 ",
			WorkspaceFingerprint: " workspace-1 ",
			ApprovalEventID:      " approval-1 ",
			ApprovalStatus:       " Approved ",
			ApprovalMode:         " Auto ",
		},
		Atoms: []AtomExecutionContract{{
			AtomID: "atom-1",
			Scope:  PlanScopeContract{RequiredCoveragePaths: []string{"pkg/a.go"}},
		}},
	}
	variant := base
	variant.PlanArtifact.Digest = "digest-2"

	normalized := base.Normalized()
	if normalized.PlanArtifact.ArtifactID != "plan-artifact-1" ||
		normalized.PlanArtifact.ApprovalStatus != "approved" ||
		normalized.PlanArtifact.ApprovalMode != "auto" {
		t.Fatalf("plan artifact binding was not normalized: %+v", normalized.PlanArtifact)
	}

	if violations := ValidatePlanExecutionContractInput(normalized); len(violations) > 0 {
		t.Fatalf("approved plan artifact binding should validate: %+v", violations)
	}

	if normalized.ContractHash == variant.Normalized().ContractHash {
		t.Fatalf("plan artifact digest must contribute to contract hash")
	}
}

func TestPlanExecutionContractRejectsIncompleteApprovedPlanArtifactBinding(t *testing.T) {
	contract := PlanExecutionContract{
		PlanID: "feature-plan-42",
		PlanArtifact: PlanArtifactBinding{
			ArtifactID:      "plan-artifact-1",
			Revision:        1,
			ApprovalStatus:  "approved",
			ApprovalEventID: "approval-1",
		},
		Atoms: []AtomExecutionContract{{
			AtomID: "atom-1",
			Scope:  PlanScopeContract{RequiredCoveragePaths: []string{"pkg/a.go"}},
		}},
	}

	violations := ValidatePlanExecutionContractInput(contract)
	if !hasViolationCode(violations, "missing_plan_artifact_digest") ||
		!hasViolationCode(violations, "missing_plan_artifact_workspace_fingerprint") {
		t.Fatalf("expected artifact digest/workspace violations, got %+v", violations)
	}
}

func TestPlanExecutionContractNormalizesAuthoritySourceFacts(t *testing.T) {
	base := PlanExecutionContract{
		PlanID: "source-facts",
		AuthorityDeltas: []AuthorityDelta{{
			Kind:   AuthorityDeltaKindNarrowWriteEnvelope,
			Path:   "pkg/feature.go",
			Source: "accepted_source",
			SourceFacts: []SourceAuthorityFact{{
				Path:      "./pkg/source.go",
				Symbol:    " helper ",
				Signature: " void helper() ",
			}, {
				Path:      "pkg/source.go",
				Symbol:    "helper",
				Signature: "void helper()",
			}},
		}},
	}
	variant := PlanExecutionContract{
		PlanID: "source-facts",
		AuthorityDeltas: []AuthorityDelta{{
			Kind:   AuthorityDeltaKindNarrowWriteEnvelope,
			Path:   "pkg/feature.go",
			Source: "accepted_source",
			SourceFacts: []SourceAuthorityFact{{
				Path:      "pkg/source.go",
				Symbol:    "helper",
				Signature: "void helper()",
			}},
		}},
	}

	normalized := base.Normalized()
	normalizedVariant := variant.Normalized()

	if normalized.ContractHash != normalizedVariant.ContractHash {
		t.Fatalf("equivalent source facts changed contract hash: %q vs %q", normalized.ContractHash, normalizedVariant.ContractHash)
	}
	facts := normalized.AuthorityDeltas[0].SourceFacts
	if len(facts) != 1 || facts[0].Path != "pkg/source.go" || facts[0].Symbol != "helper" || facts[0].Signature != "void helper()" {
		t.Fatalf("source facts were not normalized/deduplicated: %+v", facts)
	}
}

func TestPlanExecutionContractRejectsInvalidAuthoritySourceFactPath(t *testing.T) {
	contract := PlanExecutionContract{
		PlanID: "source-facts",
		AuthorityDeltas: []AuthorityDelta{{
			Kind:   AuthorityDeltaKindNarrowWriteEnvelope,
			Path:   "pkg/feature.go",
			Source: "accepted_source",
			SourceFacts: []SourceAuthorityFact{{
				Path:      "../outside.go",
				Symbol:    "helper",
				Signature: "void helper()",
			}},
		}},
	}

	violations := ValidatePlanExecutionContractInput(contract)
	if !hasViolationCode(violations, "invalid_authority_delta_source_fact_path") {
		t.Fatalf("invalid source fact path should be rejected: %#v", violations)
	}
}

func TestAcceptanceObligationWithoutIDGetsEnforcedStableID(t *testing.T) {
	atom := AtomExecutionContract{
		AtomID: "atom-1",
		Scope: NewPlanScopeContract([]string{"pkg/feature.go"}, nil, nil).
			WithWriteAllowedPaths([]string{"pkg/feature.go"}),
		AcceptanceObligations: []AcceptanceEvidenceRequired{{
			Description: "Connected cistern water is consumed and water hauling is skipped.",
			Required:    true,
			Source:      "featureplanner.sdd",
		}},
		TerminalEvidence: StrictTerminalEvidenceRequirements(),
	}.Normalized()
	if len(atom.AcceptanceObligations) != 1 || atom.AcceptanceObligations[0].ID == "" {
		t.Fatalf("acceptance obligation should get stable ID: %#v", atom.AcceptanceObligations)
	}

	missing := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
		TerminalStatus:            "success",
		ChangedPaths:              []string{"pkg/feature.go"},
		RequiredPathCoveragePaths: []string{"pkg/feature.go"},
		SourceEvidence:            []SourceSatisfiedEvidenceRef{{Path: "pkg/feature.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:        true,
		VerificationPassed:        true,
		ReviewPassed:              true,
		RequiredTestResults:       nil,
		TDDCommandResults:         nil,
		TestArtifacts:             nil,
		AcceptanceEvidenceIDs:     nil,
	})
	if !hasViolationCode(missing, "acceptance_evidence_missing") {
		t.Fatalf("missing generated acceptance ID should be enforced, got %#v", missing)
	}

	accepted := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
		TerminalStatus:            "success",
		ChangedPaths:              []string{"pkg/feature.go"},
		RequiredPathCoveragePaths: []string{"pkg/feature.go"},
		SourceEvidence:            []SourceSatisfiedEvidenceRef{{Path: "pkg/feature.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:        true,
		VerificationPassed:        true,
		ReviewPassed:              true,
		AcceptanceEvidenceIDs:     []string{atom.AcceptanceObligations[0].ID},
	})
	if hasViolationCode(accepted, "acceptance_evidence_missing") {
		t.Fatalf("matching generated acceptance ID should satisfy obligation, got %#v", accepted)
	}
}

func TestAcceptanceEvidenceThresholdClaimsAreValidated(t *testing.T) {
	tests := []struct {
		name      string
		threshold string
		claim     AcceptanceEvidenceClaim
		wantCodes []string
	}{
		{
			name:      "missing claim",
			threshold: ">=80%",
			wantCodes: []string{"acceptance_evidence_claim_missing"},
		},
		{
			name:      "unparseable value",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "not-a-number", Met: true},
			wantCodes: []string{"acceptance_evidence_claim_unparseable"},
		},
		{
			name:      "unit mismatch",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80", Met: true},
			wantCodes: []string{"acceptance_evidence_threshold_unit_mismatch"},
		},
		{
			name:      "below threshold",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "79.9%", Met: false},
			wantCodes: []string{"acceptance_evidence_threshold_not_met"},
		},
		{
			name:      "at threshold",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80%", Met: true},
		},
		{
			name:      "above threshold",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "81.25%", Met: true},
		},
		{
			name:      "met true contradicts below threshold",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "72%", Met: true},
			wantCodes: []string{"acceptance_evidence_claim_met_mismatch", "acceptance_evidence_threshold_not_met"},
		},
		{
			name:      "met false contradicts passing threshold",
			threshold: ">=80%",
			claim:     AcceptanceEvidenceClaim{ID: "coverage-threshold", Value: "80%", Met: false},
			wantCodes: []string{"acceptance_evidence_claim_met_mismatch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atom := AtomExecutionContract{
				AtomID: "atom-1",
				Scope: NewPlanScopeContract([]string{"pkg/feature.go"}, nil, nil).
					WithWriteAllowedPaths([]string{"pkg/feature.go"}),
				AcceptanceObligations: []AcceptanceEvidenceRequired{{
					ID:        "coverage-threshold",
					Required:  true,
					Threshold: tt.threshold,
				}},
				TerminalEvidence: StrictTerminalEvidenceRequirements(),
			}.Normalized()

			evidence := validTerminalEvidence()
			evidence.ChangedPaths = []string{"pkg/feature.go"}
			evidence.RequiredPathCoveragePaths = []string{"pkg/feature.go"}
			evidence.SourceEvidence = []SourceSatisfiedEvidenceRef{{Path: "pkg/feature.go", Kind: "harness_observed_source"}}
			evidence.RequiredTestResults = nil
			evidence.TDDCommandResults = nil
			evidence.TestArtifacts = nil
			if tt.claim.ID != "" {
				evidence.AcceptanceEvidenceClaims = []AcceptanceEvidenceClaim{tt.claim}
			}

			violations := ValidateAtomTerminalEvidenceInput(atom, evidence)
			for _, code := range tt.wantCodes {
				if !hasViolationCode(violations, code) {
					t.Fatalf("missing violation %q in %#v", code, violations)
				}
			}
			if len(tt.wantCodes) == 0 && len(violations) > 0 {
				t.Fatalf("threshold claim should satisfy obligation, got %#v", violations)
			}
		})
	}
}

func TestPlanExecutionContractValidatesStructuredAuthorizedNewFiles(t *testing.T) {
	atom := AtomExecutionContract{
		AtomID: "new-file",
		Scope: NewPlanScopeContract([]string{"pkg/existing.go"}, nil, nil).
			WithWriteAllowedPaths([]string{"pkg/existing.go"}),
		AuthorizedNewFiles: []AuthorizedNewFile{{
			Path:   "pkg/generated.go",
			Reason: "typed implementation output",
			Source: "feature_planner",
		}},
		AuthorizedNewRoots: []AuthorizedNewRoot{{
			Root:   "pkg/generated",
			Reason: "typed generated package",
			Source: "feature_planner",
		}},
		TerminalEvidence: StrictTerminalEvidenceRequirements(),
	}

	accepted := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
		TerminalStatus:      "success",
		ChangedPaths:        []string{"pkg/existing.go", "pkg/generated.go", "pkg/generated/helper.go"},
		SourceEvidence:      []SourceSatisfiedEvidenceRef{{Path: "pkg/existing.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:  true,
		VerificationPassed:  true,
		ReviewPassed:        true,
		RequiredTestResults: nil,
	})
	if hasViolationCode(accepted, "changed_path_outside_write_envelope") {
		t.Fatalf("typed authorized new files/roots should be inside write authority: %#v", accepted)
	}

	rejected := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
		TerminalStatus:        "success",
		ChangedPaths:          []string{"pkg/invented.go"},
		SourceEvidence:        []SourceSatisfiedEvidenceRef{{Path: "pkg/existing.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:    true,
		VerificationPassed:    true,
		ReviewPassed:          true,
		RequiredTestResults:   nil,
		TDDCommandResults:     nil,
		TestArtifacts:         nil,
		AcceptanceEvidenceIDs: nil,
	})
	if !hasViolationCode(rejected, "changed_path_outside_write_envelope") {
		t.Fatalf("unstructured new path should be rejected, got %#v", rejected)
	}
}

func TestPlanExecutionContractRejectsRequiredPathDemotion(t *testing.T) {
	atom := AtomExecutionContract{
		AtomID: "demotion",
		Scope: PlanScopeContract{
			RequiredCoveragePaths: []string{"pkg/feature.go"},
			AllowedCandidatePaths: []string{"pkg/feature.go", "pkg/optional.go"},
		},
		AuthorityDeltas: []AuthorityDelta{{
			Kind: AuthorityDeltaKindDemoteRequiredPath,
			Path: "pkg/feature.go",
			From: "required",
			To:   "allowed",
		}},
	}

	violations := ValidateAtomExecutionContractInput(atom)
	if !hasViolationCode(violations, "required_path_demoted_to_allowed") {
		t.Fatalf("required path repeated as allowed should be rejected: %#v", violations)
	}

	if !hasViolationCode(violations, "required_path_demotion_rejected") {
		t.Fatalf("authority delta demoting required path should be rejected: %#v", violations)
	}
}

func TestPlanExecutionContractRejectsInvalidEditSetObligationPath(t *testing.T) {
	atom := AtomExecutionContract{
		AtomID: "invalid-edit-set-path",
		Scope: PlanScopeContract{
			RequiredCoveragePaths: []string{"pkg/feature.go"},
			EditSetObligations: EditSetObligations{
				{Kind: EditSetObligationKindImplementationEdit, Path: "/tmp/outside.go", Source: "fixture"},
			},
		},
	}

	violations := ValidateAtomExecutionContractInput(atom)
	if !hasViolationCode(violations, "invalid_edit_set_obligation_path") {
		t.Fatalf("invalid edit-set obligation path should be rejected before normalization drops it: %#v", violations)
	}
}

func TestAtomOpenEditSetObligationsDistinguishSourceEvidenceFromRequiredPaths(t *testing.T) {
	atom := AtomExecutionContract{
		AtomID: "source-distinction",
		Scope: NewPlanScopeContract(
			[]string{"pkg/feature.go"},
			[]string{"pkg/source_only_test.go"},
			nil,
		),
		SourceEvidenceObligations: []SourceEvidenceObligation{{
			ID:       "source-only",
			Path:     "pkg/source_only_test.go",
			Kind:     "harness_observed_source",
			Required: true,
		}},
	}

	open := atom.OpenEditSetObligations([]string{"pkg/feature.go"})
	if len(open) != 0 {
		t.Fatalf("source evidence path must not create a test edit obligation, got %#v", open)
	}

	open = atom.OpenEditSetObligations([]string{"pkg/source_only_test.go"})
	if len(open) != 1 || open[0].Kind != EditSetObligationKindImplementationEdit || open[0].Path != "pkg/feature.go" {
		t.Fatalf("required implementation path should remain the open obligation, got %#v", open)
	}
}

func TestTerminalEvidenceDetectsChangedTDDCommandAndArtifact(t *testing.T) {
	atom := tddAtom()
	evidence := validTerminalEvidence()

	evidence.TDDCommandResults = []TDDCommandEvidence{{
		ID:            "red-test",
		Command:       "go test ./internal/other",
		Passed:        true,
		ArtifactPaths: []string{"internal/plan/other_test.go"},
	}}
	evidence.TestArtifacts = []string{"internal/plan/other_test.go"}

	violations := ValidateAtomTerminalEvidenceInput(atom, evidence)
	if !hasViolationCode(violations, "tdd_command_changed") {
		t.Fatalf("changed TDD command was not rejected: %#v", violations)
	}

	if !hasViolationCode(violations, "required_test_artifact_missing") {
		t.Fatalf("missing required artifact was not rejected: %#v", violations)
	}

	if !hasViolationCode(violations, "tdd_test_artifact_changed") {
		t.Fatalf("changed TDD artifact was not rejected: %#v", violations)
	}
}

func TestTerminalEvidenceRejectsMissingTestArtifact(t *testing.T) {
	atom := tddAtom()
	evidence := validTerminalEvidence()

	evidence.TestArtifacts = nil
	evidence.TDDCommandResults[0].ArtifactPaths = nil

	violations := ValidateAtomTerminalEvidenceInput(atom, evidence)
	if !hasViolationCode(violations, "required_test_artifact_missing") {
		t.Fatalf("missing required TDD artifact should reject terminal success: %#v", violations)
	}
}

func TestTerminalSuccessWithoutEvidenceRejected(t *testing.T) {
	atom := tddAtom()

	violations := ValidateAtomTerminalEvidenceInput(atom, TerminalEvidenceInput{
		TerminalStatus: "success",
	})

	for _, code := range []string{
		"terminal_success_missing_evidence",
		"changed_path_coverage_missing",
		"required_path_coverage_missing",
		"required_source_evidence_missing",
		"source_sanity_failed_or_missing",
		"verification_failed_or_missing",
		"review_failed_or_missing",
		"required_test_missing",
		"required_tdd_command_missing",
	} {
		if !hasViolationCode(violations, code) {
			t.Fatalf("missing violation %q in %#v", code, violations)
		}
	}
}

func tddAtom() AtomExecutionContract {
	return AtomExecutionContract{
		AtomID: "tdd-atom",
		Scope: NewPlanScopeContract([]string{"internal/plan/execution.go"}, nil, nil).
			WithWriteAllowedPaths([]string{"internal/plan/execution.go", "internal/plan/execution_test.go"}),
		RequiredTests: []RequiredTest{{
			ID:      "go-test-plan",
			Command: "go test ./internal/contracts/plan",
		}},
		TDDObligations: []TDDObligation{{
			ID:                "red-test",
			Command:           "go test ./internal/contracts/plan -run TestContract",
			TestArtifactPaths: []string{"internal/plan/execution_test.go"},
		}},
		SourceEvidenceObligations: []SourceEvidenceObligation{{
			ID:       "source-read",
			Path:     "internal/plan/execution.go",
			Kind:     "harness_observed_source",
			Required: true,
		}},
		TerminalEvidence: StrictTerminalEvidenceRequirements(),
	}
}

func validTerminalEvidence() TerminalEvidenceInput {
	return TerminalEvidenceInput{
		TerminalStatus:            "success",
		ChangedPaths:              []string{"internal/plan/execution.go", "internal/plan/execution_test.go"},
		RequiredPathCoveragePaths: []string{"internal/plan/execution.go"},
		SourceEvidence:            []SourceSatisfiedEvidenceRef{{Path: "internal/plan/execution.go", Kind: "harness_observed_source"}},
		SourceSanityPassed:        true,
		VerificationPassed:        true,
		ReviewPassed:              true,
		RequiredTestResults:       []RequiredTestEvidence{{ID: "go-test-plan", Command: "go test ./internal/contracts/plan", Passed: true}},
		TDDCommandResults:         []TDDCommandEvidence{{ID: "red-test", Command: "go test ./internal/contracts/plan -run TestContract", Passed: true, ArtifactPaths: []string{"internal/plan/execution_test.go"}}},
		TestArtifacts:             []string{"internal/plan/execution_test.go"},
		AcceptanceEvidenceIDs:     nil,
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

func authorizedFilePaths(files []AuthorizedNewFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}

	slices.Sort(out)

	return out
}
