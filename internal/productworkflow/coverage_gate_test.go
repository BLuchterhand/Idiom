package productworkflow

import (
	"strings"
	"testing"
)

func TestCoverageGatePassesBehaviorStandardWithCompleteCoverage(t *testing.T) {
	mutation := 90.0
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "behavior_standard",
			Coverage: TDDCoverage{
				EARSTotal:           2,
				EARSMapped:          2,
				BDDTotal:            2,
				BDDWithConcreteData: 2,
				GatesPassed:         []string{"contract"},
				MutationScore:       &mutation,
				MutationMinScore:    80,
			},
			Obligations: []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Findings)
	}
	if CoverageGateHasBlockers(eval) {
		t.Fatalf("expected no blockers, got %#v", eval.Blockers)
	}
}

func TestCoverageGateBlocksContractHighWhenMutationBelowMin(t *testing.T) {
	mutation := 50.0
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "contract_high",
			Coverage: TDDCoverage{
				EARSTotal:                1,
				EARSMapped:               1,
				BDDTotal:                 1,
				BDDWithConcreteData:      1,
				FMEAModesTotal:           1,
				FMEAModesMitigated:       1,
				HAZOPDeviationsTotal:     1,
				HAZOPDeviationsTested:    1,
				InvariantsTotal:          1,
				InvariantsWithPBT:        1,
				DecisionTablesTotal:      1,
				DecisionTablesEnumerated: 1,
				GatesPassed:              []string{"contract"},
				MutationScore:            &mutation,
				MutationMinScore:         80,
			},
			Obligations: []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	foundMutation := false
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "mutation" {
			foundMutation = true
			break
		}
	}
	if !foundMutation {
		t.Fatalf("expected mutation blocker, got %#v", eval.Blockers)
	}
}

func TestCoverageGateBlocksWhenEvidenceAcceptanceCoverageMissing(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{Matrix: []TraceabilityRow{}},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	found := false
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "evidence_acceptance" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected evidence_acceptance blocker, got %#v", eval.Blockers)
	}
}

func TestCoverageGateBlocksWhenPRDOrADRReferencesUnknownTDDObligation(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			RelPath: "adrs/ADR-1.md",
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-MISSING"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
		},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
}

func TestCoverageGateDoesNotBlockWhenNonMustPRDReferencesUnknownTDDObligation(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
		},
	)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q", eval.Status)
	}
	for _, blocker := range eval.Blockers {
		if blocker.Kind == "coverage_prd_tdd_obligation_unknown" {
			t.Fatalf("non-must PRD unknown TDD ref must not produce PRD TDD blocker: %#v", eval.Blockers)
		}
	}
}

func TestCoverageGateBlocksWhenMustPRDReferencesUnknownTDDObligation(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "must", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-MISSING"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-MISSING"}},
		},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	found := false
	for _, blocker := range eval.Blockers {
		if blocker.Kind == "coverage_prd_tdd_obligation_unknown" &&
			strings.Contains(blocker.Message, "REQ-1") &&
			strings.Contains(blocker.Message, "TDD-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected typed PRD TDD blocker naming requirement and obligation, got %#v", eval.Blockers)
	}
	found = false
	for _, finding := range eval.Findings {
		if finding.Kind == "coverage_prd_tdd_obligation_unknown" &&
			strings.Contains(finding.Message, "REQ-1") &&
			strings.Contains(finding.Message, "TDD-MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected typed PRD TDD finding naming requirement and obligation, got %#v", eval.Findings)
	}
}

func TestCoverageGateBlocksWhenAcceptedADRReferencesUnknownTDDObligation(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-MISSING"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}

	foundBlocker := false
	foundFinding := false
	for _, blocker := range eval.Blockers {
		if blocker.Kind == "coverage_adr_tdd_obligation_unknown" {
			foundBlocker = true
			if !strings.Contains(blocker.Message, "ADR-1") || !strings.Contains(blocker.Message, "TDD-MISSING") {
				t.Fatalf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message)
			}
			break
		}
	}
	if !foundBlocker {
		t.Fatalf("expected typed ADR TDD blocker, got %#v", eval.Blockers)
	}

	for _, finding := range eval.Findings {
		if finding.Kind == "coverage_adr_tdd_obligation_unknown" {
			foundFinding = true
			if !strings.Contains(finding.Message, "ADR-1") || !strings.Contains(finding.Message, "TDD-MISSING") {
				t.Fatalf("finding message should contain ADR and TDD ID, got: %s", finding.Message)
			}
			break
		}
	}
	if !foundFinding {
		t.Fatalf("expected typed ADR TDD finding, got %#v", eval.Findings)
	}
}

// TestCoverageGateRegressionForADRWithoutRelPath tests that ADRs without RelPath
// but with ADRID still generate typed coverage blockers/findings with both IDs in the message.
func TestCoverageGateRegressionForADRWithoutRelPath(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", Priority: "should", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-MISSING"},
				},
				// Note: No RelPath field (this would be the regression case)
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}

	foundBlocker := false
	foundFinding := false
	for _, blocker := range eval.Blockers {
		if blocker.Kind == "coverage_adr_tdd_obligation_unknown" {
			foundBlocker = true
			// Verify both ADR ID and TDD ID are in the message
			if !strings.Contains(blocker.Message, "ADR-1") || !strings.Contains(blocker.Message, "TDD-MISSING") {
				t.Fatalf("blocker message should contain ADR and TDD ID, got: %s", blocker.Message)
			}
			break
		}
	}
	if !foundBlocker {
		t.Fatalf("expected typed ADR TDD blocker, got %#v", eval.Blockers)
	}

	for _, finding := range eval.Findings {
		if finding.Kind == "coverage_adr_tdd_obligation_unknown" {
			foundFinding = true
			// Verify both ADR ID and TDD ID are in the message
			if !strings.Contains(finding.Message, "ADR-1") || !strings.Contains(finding.Message, "TDD-MISSING") {
				t.Fatalf("finding message should contain ADR and TDD ID, got: %s", finding.Message)
			}
			break
		}
	}
	if !foundFinding {
		t.Fatalf("expected typed ADR TDD finding, got %#v", eval.Findings)
	}
}

func TestCoverageGateEvidenceAcceptanceDoesNotRequireEveryTDDObligationTrace(t *testing.T) {
	eval := EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1", "TDD-2"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:          "ADR-1",
					Status:         "accepted",
					EvidenceRefs:   []string{"EV-1"},
					TDDObligations: []string{"TDD-1", "TDD-2"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: "unit",
			Coverage:     TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{
				{ID: "TDD-1"},
				{ID: "TDD-2"},
				{ID: "TDD-IMPLEMENTATION-LOCAL"},
			},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Findings)
	}
}

func TestBuildKnownTDDObligationIDsScopesHistoricalRecords(t *testing.T) {
	ids := BuildKnownTDDObligationIDs(
		TDDLedger{
			EffortID:    "effort",
			Obligations: []TDDObligation{{ID: "TDD-CURRENT"}},
		},
		[]ImplementationLedgerRecord{
			{RecordType: "verification_completed", EffortID: "effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-HIST"}},
			{RecordType: "verification_completed", EffortID: "effort", TaskID: "other-task", TDDObligationsSatisfied: []string{"TDD-OTHER"}},
			{RecordType: "implementation_completed", EffortID: "effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-WRONG-RECORD"}},
			{RecordType: "verification_completed", EffortID: "other-effort", TaskID: "artifact-task", TDDObligationsSatisfied: []string{"TDD-WRONG-EFFORT"}},
		},
		"artifact-task",
	)
	for _, want := range []string{"TDD-CURRENT", "TDD-HIST"} {
		if !ids[want] {
			t.Fatalf("expected known ID %q in %#v", want, ids)
		}
	}
	for _, unexpected := range []string{"TDD-OTHER", "TDD-WRONG-RECORD", "TDD-WRONG-EFFORT"} {
		if ids[unexpected] {
			t.Fatalf("did not expect known ID %q in %#v", unexpected, ids)
		}
	}
}

func TestCoverageGateBlocksContractTiersWhenPBTInvariantTotalIsZero(t *testing.T) {
	for _, tier := range []string{"contract_high", "formal_optional"} {
		t.Run(tier, func(t *testing.T) {
			eval := coverageGateEvalWithZeroInvariants(tier)
			if eval.Status != "blocked" {
				t.Fatalf("expected blocked, got %q", eval.Status)
			}

			foundPBT := false
			for _, blocker := range eval.Blockers {
				if blocker.Criterion == "pbt" {
					foundPBT = true
					if !strings.Contains(blocker.Message, "invariants_total is zero") {
						t.Fatalf("blocker message should mention invariants_total is zero, got: %s", blocker.Message)
					}
					break
				}
			}
			if !foundPBT {
				t.Fatalf("expected pbt blocker, got %#v", eval.Blockers)
			}
		})
	}
}

func TestCoverageGateDoesNotBlockLowerTiersWhenPBTInvariantTotalIsZero(t *testing.T) {
	for _, tier := range []string{"documentation", "local_low", "unit", "behavior_standard"} {
		t.Run(tier, func(t *testing.T) {
			eval := coverageGateEvalWithZeroInvariants(tier)
			if eval.Status != "pass" {
				t.Fatalf("expected pass, got %q", eval.Status)
			}

			for _, blocker := range eval.Blockers {
				if blocker.Criterion == "pbt" {
					t.Fatalf("expected no pbt blocker in %s tier, got %#v", tier, eval.Blockers)
				}
			}
		})
	}
}

func coverageGateEvalWithZeroInvariants(tier string) CoverageGateEvaluation {
	return EvaluateCoverageGate(
		PRDDocument{
			FrontMatter: PRDFrontMatter{Status: "accepted"},
			Requirements: []PRDRequirement{
				{ID: "REQ-1", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}},
			},
		},
		ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1"}}},
		[]ParsedADR{{
			Doc: ADRDocument{
				FrontMatter: ADRFrontMatter{
					ADRID:        "ADR-1",
					Status:       "accepted",
					EvidenceRefs: []string{"EV-1"},
				},
			},
		}},
		TDDLedger{
			CoverageTier: tier,
			Coverage: TDDCoverage{
				EARSTotal:                1,
				EARSMapped:               1,
				BDDTotal:                 1,
				BDDWithConcreteData:      1,
				FMEAModesTotal:           1,
				FMEAModesMitigated:       1,
				HAZOPDeviationsTotal:     1,
				HAZOPDeviationsTested:    1,
				InvariantsTotal:          0,
				InvariantsWithPBT:        0,
				DecisionTablesTotal:      1,
				DecisionTablesEnumerated: 1,
				GatesPassed:              []string{"contract"},
			},
			Obligations: []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
	)
}
