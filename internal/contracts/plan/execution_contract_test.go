package plancontract

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
