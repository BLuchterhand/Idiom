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
		map[string]bool{"TDD-1": true},
	)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Findings)
	}
	if CoverageGateHasBlockers(eval) {
		t.Fatalf("expected no blockers, got %#v", eval.Blockers)
	}
}

func TestBuildCoverageGateEvaluatedEventReportsCoveragePercentages(t *testing.T) {
	mutation := 85.0
	event := BuildCoverageGateEvaluatedEvent(
		CoverageGateEvaluation{
			EffortID: "effort-1",
			TaskID:   "task-1",
			Status:   "pass",
		},
		TDDLedger{
			Coverage: TDDCoverage{
				EARSTotal:                4,
				EARSMapped:               2,
				BDDTotal:                 2,
				BDDWithConcreteData:      1,
				FMEAModesTotal:           4,
				FMEAModesMitigated:       3,
				HAZOPDeviationsTotal:     4,
				HAZOPDeviationsTested:    2,
				InvariantsTotal:          2,
				InvariantsWithPBT:        1,
				StateMachinesTotal:       4,
				StateMachinesCovered:     3,
				DecisionTablesTotal:      4,
				DecisionTablesEnumerated: 1,
				BoundaryValuesTotal:      5,
				BoundaryValuesTested:     4,
				FaultTreeLeavesTotal:     5,
				FaultTreeLeavesTested:    3,
				PostMortemIncidentsTotal: 5,
				PostMortemCatchingTests:  2,
				FormalModelsTotal:        2,
				FormalModelsVerified:     1,
				MutationScore:            &mutation,
			},
		},
	)

	if event.Event != "coverage_gate_evaluated" || event.EffortID != "effort-1" || event.TaskID != "task-1" || event.GateResult != "pass" {
		t.Fatalf("unexpected event identity: %#v", event)
	}
	assertCoveragePct(t, "ears", event.Coverage.EARSCoveragePct, 50)
	assertCoveragePct(t, "bdd", event.Coverage.BDDCoveragePct, 50)
	assertCoveragePct(t, "fmea", event.Coverage.FMEAMitigationPct, 75)
	assertCoveragePct(t, "hazop", event.Coverage.HAZOPTestedPct, 50)
	assertCoveragePct(t, "pbt", event.Coverage.InvariantPBTPct, 50)
	assertCoveragePct(t, "state_machine", event.Coverage.StateMachinePct, 75)
	assertCoveragePct(t, "decision_table", event.Coverage.DecisionTablePct, 25)
	assertCoveragePct(t, "boundary_value", event.Coverage.BoundaryValuePct, 80)
	assertCoveragePct(t, "fault_tree", event.Coverage.FaultTreeCoveragePct, 60)
	assertCoveragePct(t, "post_mortem", event.Coverage.PostMortemCoveragePct, 40)
	assertCoveragePct(t, "formal_verification", event.Coverage.FormalVerificationPct, 50)
	assertCoveragePct(t, "mutation", event.Coverage.MutationScore, 85)
	if len(event.BlockedReasons) != 0 {
		t.Fatalf("passing event should have no blocked reasons, got %#v", event.BlockedReasons)
	}
	if event.Waivers == nil {
		t.Fatal("waivers should be an empty list, not nil")
	}
}

func TestBuildCoverageGateEvaluatedEventReportsBlockedReasons(t *testing.T) {
	event := BuildCoverageGateEvaluatedEvent(
		CoverageGateEvaluation{
			Status: "blocked",
			Blockers: []CoverageGateBlocker{
				{Criterion: "mutation", Kind: "coverage_mutation_incomplete"},
				{Criterion: "authority_preflight", Kind: "parse_error"},
				{Criterion: "mutation", Kind: "coverage_mutation_incomplete"},
			},
		},
		TDDLedger{EffortID: "ledger-effort", TaskID: "ledger-task"},
	)

	if event.EffortID != "ledger-effort" || event.TaskID != "ledger-task" {
		t.Fatalf("expected ledger identity fallback, got %#v", event)
	}
	want := []string{"authority_preflight:parse_error", "mutation:coverage_mutation_incomplete"}
	if len(event.BlockedReasons) != len(want) {
		t.Fatalf("expected blocked reasons %#v, got %#v", want, event.BlockedReasons)
	}
	for i := range want {
		if event.BlockedReasons[i] != want[i] {
			t.Fatalf("expected blocked reasons %#v, got %#v", want, event.BlockedReasons)
		}
	}
}

func assertCoveragePct(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s coverage should be reported", name)
	}
	if *got != want {
		t.Fatalf("%s coverage = %v, want %v", name, *got, want)
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
		map[string]bool{"TDD-1": true},
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
		map[string]bool{"TDD-1": true},
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
		map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
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
		map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
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
		map[string]bool{"TDD-1": true, "TDD-2": true, "TDD-IMPLEMENTATION-LOCAL": true},
	)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Findings)
	}
}

func TestCoverageGateBlocksWhenTDDEvidenceMissing(t *testing.T) {
	// Create a scenario where TDD obligations exist but no verification records satisfy them.
	// knownTDDIDs is empty, so evaluateTDDEvidenceCoverage should return false.
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
			Obligations: []TDDObligation{
				{ID: "TDD-1"},
			},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
		// No knownTDDIDs provided, so TDD-1 is not satisfied.
	)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q: %#v", eval.Status, eval.Findings)
	}
	found := false
	for _, f := range eval.Findings {
		if f.Criterion == "tdd_evidence" && f.Kind == "coverage_tdd_evidence_missing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tdd_evidence finding, got: %#v", eval.Findings)
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

func TestCoverageGateAuthorityPreflightCarriesLintFindings(t *testing.T) {
	eval := CoverageGateEvaluation{Status: "pass"}
	eval = ApplyCoverageGateAuthorityPreflight(eval, []LintSummaryFinding{
		{Code: "adr_supersession_non_reciprocal", Path: "adrs/ADR-001-test.md", Message: "ADR-001 supersedes ADR-002 without reciprocal metadata"},
		{Code: "adr_index_frontmatter_mismatch", Path: "adrs/index.json", Message: "ADR index entry ADR-001 field prd_refs diverges from ADR markdown"},
		{Code: "obsidian_stale_status", Path: ObsidianProductWorkflowPath, Message: "not coverage-gate authority"},
	})
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", eval.Status)
	}
	foundAuthorityCriterion := false
	for _, criterion := range eval.Criteria {
		if criterion.Key == "authority_preflight" && criterion.Status == "blocked" {
			foundAuthorityCriterion = true
			break
		}
	}
	if !foundAuthorityCriterion {
		t.Fatalf("expected blocked authority_preflight criterion, got %#v", eval.Criteria)
	}
	if len(eval.Blockers) != 2 {
		t.Fatalf("expected only ADR authority blockers, got %#v", eval.Blockers)
	}
	kinds := map[string]bool{}
	for _, blocker := range eval.Blockers {
		kinds[blocker.Kind] = true
	}
	for _, want := range []string{"adr_supersession_non_reciprocal", "adr_index_frontmatter_mismatch"} {
		if !kinds[want] {
			t.Fatalf("expected authority blocker %q, got %#v", want, eval.Blockers)
		}
	}
}

func TestCoverageGateAuthorityFindingCoversCanonicalLintMatrix(t *testing.T) {
	authority := []LintSummaryFinding{
		{Code: "parse_error", Path: "prd.md"},
		{Code: "broken_link", Path: "prd.md"},
		{Code: "parse_error", Path: "adrs/ADR-001-test.md"},
		{Code: "broken_link", Path: "adrs/ADR-001-test.md"},
		{Code: "missing_file", Path: "evidence/traceability_matrix.json"},
		{Code: "parse_error", Path: "evidence/traceability_matrix.json"},
		{Code: "broken_link", Path: "evidence/traceability_matrix.json"},
		{Code: "parse_error", Path: "tdd.json"},
		{Code: "broken_link", Path: "tdd.json"},
		{Code: "adr_supersession_non_reciprocal", Path: "adrs/ADR-001-test.md"},
		{Code: "adr_index_frontmatter_mismatch", Path: "adrs/index.json"},
	}
	for _, finding := range authority {
		if !coverageGateAuthorityFinding(finding) {
			t.Fatalf("expected authority finding for %#v", finding)
		}
	}

	nonAuthority := []LintSummaryFinding{
		{Code: "obsidian_stale_status", Path: ObsidianProductWorkflowPath},
		{Code: "missing_file", Path: "prd.md"},
		{Code: "read_error", Path: "implementation_ledger.jsonl"},
	}
	for _, finding := range nonAuthority {
		if coverageGateAuthorityFinding(finding) {
			t.Fatalf("did not expect authority finding for %#v", finding)
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

func TestCoverageGateBlocksContractTiersWhenStateMachineInventoryMissing(t *testing.T) {
	for _, tier := range []string{"contract_high", "formal_optional"} {
		t.Run(tier, func(t *testing.T) {
			eval := coverageGateEvalWithStateMachineCoverage(tier, 0, 0)
			if eval.Status != "blocked" {
				t.Fatalf("expected blocked, got %q", eval.Status)
			}
			assertCoverageGateBlocker(t, eval, "state_machine", "coverage_state_machine_incomplete", "state_machines_total is zero")
		})
	}
}

func TestCoverageGateBlocksContractTiersWhenStateMachineCoveragePartial(t *testing.T) {
	eval := coverageGateEvalWithStateMachineCoverage("contract_high", 3, 2)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "state_machine", "coverage_state_machine_incomplete", "state_machines_covered == state_machines_total (2/3)")
}

func TestCoverageGateDoesNotRequireStateMachineCoverageBelowContractTiers(t *testing.T) {
	for _, tier := range []string{"documentation", "local_low", "unit", "behavior_standard"} {
		t.Run(tier, func(t *testing.T) {
			eval := coverageGateEvalWithStateMachineCoverage(tier, 0, 0)
			if eval.Status != "pass" {
				t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Blockers)
			}
			for _, blocker := range eval.Blockers {
				if blocker.Criterion == "state_machine" {
					t.Fatalf("expected no state_machine blocker in %s tier, got %#v", tier, eval.Blockers)
				}
			}
		})
	}
}

func TestCoverageGateBlocksContractTiersWhenBoundaryValueCoveragePartial(t *testing.T) {
	eval := coverageGateEvalWithBoundaryCoverage("contract_high", 4, 3, nil)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "boundary_value", "coverage_boundary_value_incomplete", "boundary_values_tested == boundary_values_total (3/4)")
}

func TestCoverageGateBlocksContractTiersWhenBoundaryValueGateConfiguredWithoutCounters(t *testing.T) {
	eval := coverageGateEvalWithBoundaryCoverage("contract_high", 0, 0, []string{"boundary_value"})
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "boundary_value", "coverage_boundary_value_incomplete", "boundary_values_total is zero")
}

func TestCoverageGateDoesNotRequireBoundaryValueCoverageWhenNoNumericBoundsDeclared(t *testing.T) {
	eval := coverageGateEvalWithBoundaryCoverage("contract_high", 0, 0, nil)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Blockers)
	}
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "boundary_value" {
			t.Fatalf("expected no boundary_value blocker, got %#v", eval.Blockers)
		}
	}
}

func TestCoverageGateBlocksContractTiersWhenFaultTreeCoveragePartial(t *testing.T) {
	eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 4, 3, nil)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "fault_tree", "coverage_fault_tree_incomplete", "fault_tree_leaves_tested == fault_tree_leaves_total (3/4)")
}

func TestCoverageGateBlocksContractTiersWhenFaultTreeGateConfiguredWithoutCounters(t *testing.T) {
	eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 0, 0, []string{"fault_tree"})
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "fault_tree", "coverage_fault_tree_incomplete", "fault_tree_leaves_total is zero")
}

func TestCoverageGateDoesNotRequireFaultTreeCoverageWhenNoFaultTreeDeclared(t *testing.T) {
	eval := coverageGateEvalWithFaultTreeCoverage("contract_high", 0, 0, nil)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Blockers)
	}
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "fault_tree" {
			t.Fatalf("expected no fault_tree blocker, got %#v", eval.Blockers)
		}
	}
}

func TestCoverageGateBlocksContractTiersWhenPostMortemCoveragePartial(t *testing.T) {
	eval := coverageGateEvalWithPostMortemCoverage("contract_high", 4, 3, nil)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "post_mortem", "coverage_post_mortem_incomplete", "post_mortem_catching_tests == post_mortem_incidents_total (3/4)")
}

func TestCoverageGateBlocksContractTiersWhenPostMortemGateConfiguredWithoutCounters(t *testing.T) {
	eval := coverageGateEvalWithPostMortemCoverage("contract_high", 0, 0, []string{"post_mortem"})
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "post_mortem", "coverage_post_mortem_incomplete", "post_mortem_incidents_total is zero")
}

func TestCoverageGateDoesNotRequirePostMortemCoverageWhenNoIncidentsDeclared(t *testing.T) {
	eval := coverageGateEvalWithPostMortemCoverage("contract_high", 0, 0, nil)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Blockers)
	}
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "post_mortem" {
			t.Fatalf("expected no post_mortem blocker, got %#v", eval.Blockers)
		}
	}
}

func TestCoverageGateBlocksFormalOptionalWhenFormalCoveragePartial(t *testing.T) {
	eval := coverageGateEvalWithFormalCoverage("formal_optional", 3, 2, nil)
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "formal_verification", "coverage_formal_verification_incomplete", "formal_models_verified == formal_models_total (2/3)")
}

func TestCoverageGateBlocksFormalOptionalWhenFormalGateConfiguredWithoutCounters(t *testing.T) {
	eval := coverageGateEvalWithFormalCoverage("formal_optional", 0, 0, []string{"formal_verification"})
	if eval.Status != "blocked" {
		t.Fatalf("expected blocked, got %q", eval.Status)
	}
	assertCoverageGateBlocker(t, eval, "formal_verification", "coverage_formal_verification_incomplete", "formal_models_total is zero")
}

func TestCoverageGateDoesNotRequireFormalCoverageForOrdinaryContractHigh(t *testing.T) {
	eval := coverageGateEvalWithFormalCoverage("contract_high", 0, 0, nil)
	if eval.Status != "pass" {
		t.Fatalf("expected pass, got %q: %#v", eval.Status, eval.Blockers)
	}
	for _, blocker := range eval.Blockers {
		if blocker.Criterion == "formal_verification" {
			t.Fatalf("expected no formal_verification blocker, got %#v", eval.Blockers)
		}
	}
}

func coverageGateEvalWithZeroInvariants(tier string) CoverageGateEvaluation {
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              []string{"contract"},
	})
}

func coverageGateEvalWithStateMachineCoverage(tier string, total int, covered int) CoverageGateEvaluation {
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       total,
		StateMachinesCovered:     covered,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              []string{"contract"},
	})
}

func coverageGateEvalWithBoundaryCoverage(tier string, total int, tested int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      total,
		BoundaryValuesTested:     tested,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithFaultTreeCoverage(tier string, total int, tested int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     total,
		FaultTreeLeavesTested:    tested,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithPostMortemCoverage(tier string, total int, catchingTests int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: total,
		PostMortemCatchingTests:  catchingTests,
		FormalModelsTotal:        1,
		FormalModelsVerified:     1,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithFormalCoverage(tier string, total int, verified int, gatesPassed []string) CoverageGateEvaluation {
	if len(gatesPassed) == 0 {
		gatesPassed = []string{"contract"}
	}
	return coverageGateEvalWithCoverage(tier, TDDCoverage{
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
		StateMachinesTotal:       1,
		StateMachinesCovered:     1,
		DecisionTablesTotal:      1,
		DecisionTablesEnumerated: 1,
		BoundaryValuesTotal:      1,
		BoundaryValuesTested:     1,
		FaultTreeLeavesTotal:     1,
		FaultTreeLeavesTested:    1,
		PostMortemIncidentsTotal: 1,
		PostMortemCatchingTests:  1,
		FormalModelsTotal:        total,
		FormalModelsVerified:     verified,
		GatesPassed:              gatesPassed,
	})
}

func coverageGateEvalWithCoverage(tier string, coverage TDDCoverage) CoverageGateEvaluation {
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
			Coverage:     coverage,
			Obligations:  []TDDObligation{{ID: "TDD-1"}},
		},
		TraceabilityMatrix{
			Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1", TDDObligation: "TDD-1"}},
		},
		map[string]bool{"TDD-1": true},
	)
}

func assertCoverageGateBlocker(t *testing.T, eval CoverageGateEvaluation, criterion string, kind string, messageSubstring string) {
	t.Helper()
	for _, blocker := range eval.Blockers {
		if blocker.Criterion != criterion || blocker.Kind != kind {
			continue
		}
		if messageSubstring != "" && !strings.Contains(blocker.Message, messageSubstring) {
			t.Fatalf("blocker message should contain %q, got: %s", messageSubstring, blocker.Message)
		}
		return
	}
	t.Fatalf("expected blocker criterion=%q kind=%q, got %#v", criterion, kind, eval.Blockers)
}
