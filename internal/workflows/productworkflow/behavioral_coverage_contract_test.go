package productworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBehavioralCoverageContractProjectsCompleteAcceptedCoverage(t *testing.T) {
	score := 91.0
	contract := BehavioralCoverageContract{
		EffortID:     "effort-test",
		TaskID:       "task-test",
		CoverageTier: "contract_high",
		EARS: []EARSRequirementCoverage{
			{
				ID:          "REQ-1",
				Pattern:     "event-driven",
				Statement:   "When login succeeds, the system shall create a session.",
				MappedRefs:  []string{"BDD-1"},
				Obligations: []string{"TDD-1"},
			},
		},
		ExampleRules: []ExampleRuleCoverage{
			{
				ID:            "RULE-1",
				RequirementID: "REQ-1",
				Rule:          "Accepted credentials create a session.",
				Examples: []ConcreteExampleCoverage{
					{ID: "EX-1", ConcreteDataRefs: []string{"fixtures/login_success.json"}},
					{ID: "EX-2", ConcreteDataRefs: []string{"fixtures/login_failure.json"}, Negative: true},
				},
			},
		},
		BDDScenarios: []BDDScenarioCoverage{
			{ID: "BDD-1", RequirementID: "REQ-1", Status: "accepted", Given: []string{"valid credentials exist"}, When: []string{"login succeeds"}, Then: []string{"session is created"}, ConcreteDataRefs: []string{"fixtures/login_success.json"}, Obligations: []string{"TDD-1"}},
			{ID: "BDD-2", Given: []string{"expired credentials exist"}, When: []string{"login is attempted"}, Then: []string{"session is rejected"}, ConcreteDataRefs: []string{"fixtures/login_failure.json"}},
			{ID: "BDD-WAIVED", Status: "waived"},
		},
		DesignContracts: []DesignContractCoverage{
			{ID: "DBC-1", Boundary: "auth/login", Preconditions: []string{"credentials parsed"}, Postconditions: []string{"session stored"}, Invariants: []string{"token owner unchanged"}, TestRefs: []string{"TestLoginContract"}, Obligations: []string{"TDD-1"}},
		},
		Invariants: []InvariantCoverage{
			{ID: "INV-1", Statement: "session token always belongs to authenticated user", PropertyBasedTestRefs: []string{"TestSessionTokenInvariant"}},
			{ID: "INV-NR", Status: "not_required"},
		},
		StateMachines: []StateMachineCoverage{
			{
				ID:     "SM-1",
				States: []string{"anonymous", "authenticated"},
				Transitions: []StateTransitionCoverage{
					{ID: "TR-1", From: "anonymous", To: "authenticated", Event: "login", TestRefs: []string{"TestLoginTransition"}},
				},
			},
		},
		FailureModes: []FailureModeCoverage{
			{ID: "FM-1", Mode: "expired session accepted", Mitigation: "reject expired token", RPN: 64, MitigationTestRefs: []string{"TestRejectsExpiredSession"}},
		},
		HAZOPDeviations: []HAZOPDeviationCoverage{
			{ID: "HAZOP-1", GuideWord: "Late", WorkflowStep: "session creation", Deviation: "session is written after expiration", Severity: 3, TestRefs: []string{"TestLateSessionRejected"}},
		},
		FaultTreeLeaves: []FaultTreeLeafCoverage{
			{ID: "FTA-1", TopEvent: "unauthorized session", Cause: "expired token accepted", TestRefs: []string{"TestExpiredTokenFaultTree"}},
		},
		DecisionTables: []DecisionTableCoverage{
			{ID: "DT-1", Conditions: []string{"password_valid", "token_expired"}, Rules: []DecisionTableRuleCoverage{{Conditions: map[string]string{"password_valid": "true", "token_expired": "false"}, Expected: "create_session"}}, TestRefs: []string{"TestLoginDecisionTable"}},
		},
		BoundaryValues: []BoundaryValueCoverage{
			{ID: "BV-1", Parameter: "session_ttl_seconds", Values: []string{"0", "1", "3600"}, TestRefs: []string{"TestSessionTTLBoundaries"}},
		},
		PostMortems: []PostMortemCoverage{
			{ID: "PM-1", Incident: "expired token regression", CatchingTestRefs: []string{"TestExpiredTokenRegression"}},
		},
		FormalModels: []FormalModelCoverage{
			{ID: "FORMAL-1", Model: "auth_session_state", VerificationRefs: []string{"alloy/auth_session.als"}},
		},
		FuzzTargets: []FuzzTargetCoverage{
			{ID: "FUZZ-1", Target: "ParseLoginToken", Generators: []string{"random token bytes"}, Invariants: []string{"never panics", "rejects malformed tokens"}, TestRefs: []string{"FuzzParseLoginToken"}, Obligations: []string{"TDD-1"}},
		},
		RAGEvals: []RAGEvaluationCoverage{
			{ID: "RAG-1", DatasetRefs: []string{"evals/login_retrieval.jsonl"}, Metrics: map[string]float64{"faithfulness": 0.98}, Thresholds: map[string]float64{"faithfulness": 0.95}, EvalRefs: []string{"TestLoginRetrievalEval"}, Obligations: []string{"TDD-1"}},
		},
		SecurityCases: []SecurityCaseCoverage{
			{ID: "SEC-1", Risk: "prompt injection bypasses auth policy", Attack: "malicious support note instructs auth bypass", Expected: "instruction is ignored", TestRefs: []string{"TestPromptInjectionAuthBypass"}, Obligations: []string{"TDD-1"}},
		},
		PrivacyControls: []PrivacyControlCoverage{
			{ID: "PRIV-1", DataClass: "user credentials", Policy: "no raw credential logging", RedactionRefs: []string{"redaction/auth_credentials"}, Retention: "not stored", TestRefs: []string{"TestCredentialRedaction"}, Obligations: []string{"TDD-1"}},
		},
		ProviderContracts: []ProviderContractCoverage{
			{ID: "PACT-1", Provider: "auth-tokenizer", Consumer: "login-flow", ContractRefs: []string{"contracts/auth_tokenizer.pact.json"}, TestRefs: []string{"TestAuthTokenizerContract"}, Obligations: []string{"TDD-1"}},
		},
		Observability: []ObservabilityCoverage{
			{ID: "OBS-1", Signal: "login_attempt_total", InstrumentedPath: "internal/auth/login.go", RequiredFields: []string{"result", "reason"}, TestRefs: []string{"TestLoginMetrics"}, Obligations: []string{"TDD-1"}},
		},
		PerformanceBounds: []PerformanceBoundCoverage{
			{ID: "PERF-1", Operation: "login", Bound: "p95 <= 250ms", MeasurementRefs: []string{"benchmarks/login_p95.json"}, Obligations: []string{"TDD-1"}},
		},
		E2EJourneys: []E2EJourneyCoverage{
			{ID: "E2E-1", Journey: "user signs in and reaches dashboard", Steps: []string{"submit credentials", "receive session", "load dashboard"}, ScriptRefs: []string{"scripts/e2e/login_dashboard.sh"}, Obligations: []string{"TDD-1"}},
		},
		TestObligations: []TestObligationCoverage{
			{ID: "TDD-1", Type: "contract", Behavior: "login creates a session", Owner: "productworkflow", Command: "go test ./internal/auth", RequiredBefore: "implementation"},
		},
		Verification: []VerificationGateCoverage{
			{ID: "VERIFY-1", Gate: "contract", Command: "go test ./internal/auth", Obligations: []string{"TDD-1"}},
		},
		Mutation: BehavioralMutationCoverage{
			Requirement: "required",
			MinScore:    80,
			Score:       &score,
		},
	}

	coverage, err := ProjectValidatedBehavioralCoverageContract(contract)
	if err != nil {
		t.Fatalf("expected valid contract, got %v", err)
	}

	if coverage.EARSTotal != 1 || coverage.EARSMapped != 1 {
		t.Fatalf("unexpected EARS projection: %#v", coverage)
	}
	if coverage.BDDTotal != 2 || coverage.BDDWithConcreteData != 2 {
		t.Fatalf("unexpected BDD projection: %#v", coverage)
	}
	if coverage.InvariantsTotal != 1 || coverage.InvariantsWithPBT != 1 {
		t.Fatalf("unexpected invariant projection: %#v", coverage)
	}
	if coverage.StateMachinesTotal != 1 || coverage.StateMachinesCovered != 1 {
		t.Fatalf("unexpected state-machine projection: %#v", coverage)
	}
	if coverage.FMEAModesTotal != 1 || coverage.FMEAModesMitigated != 1 {
		t.Fatalf("unexpected failure-mode projection: %#v", coverage)
	}
	if coverage.HAZOPDeviationsTotal != 1 || coverage.HAZOPDeviationsTested != 1 {
		t.Fatalf("unexpected HAZOP projection: %#v", coverage)
	}
	if coverage.DecisionTablesTotal != 1 || coverage.DecisionTablesEnumerated != 1 {
		t.Fatalf("unexpected decision-table projection: %#v", coverage)
	}
	if coverage.BoundaryValuesTotal != 1 || coverage.BoundaryValuesTested != 1 {
		t.Fatalf("unexpected boundary-value projection: %#v", coverage)
	}
	if coverage.FaultTreeLeavesTotal != 1 || coverage.FaultTreeLeavesTested != 1 {
		t.Fatalf("unexpected fault-tree projection: %#v", coverage)
	}
	if coverage.PostMortemIncidentsTotal != 1 || coverage.PostMortemCatchingTests != 1 {
		t.Fatalf("unexpected post-mortem projection: %#v", coverage)
	}
	if coverage.FormalModelsTotal != 1 || coverage.FormalModelsVerified != 1 {
		t.Fatalf("unexpected formal model projection: %#v", coverage)
	}
	if coverage.FuzzTargetsTotal != 1 || coverage.FuzzTargetsCovered != 1 {
		t.Fatalf("unexpected fuzz projection: %#v", coverage)
	}
	if coverage.RAGEvalsTotal != 1 || coverage.RAGEvalsMeasured != 1 {
		t.Fatalf("unexpected RAG eval projection: %#v", coverage)
	}
	if coverage.SecurityCasesTotal != 1 || coverage.SecurityCasesTested != 1 {
		t.Fatalf("unexpected security projection: %#v", coverage)
	}
	if coverage.PrivacyControlsTotal != 1 || coverage.PrivacyControlsTested != 1 {
		t.Fatalf("unexpected privacy projection: %#v", coverage)
	}
	if coverage.ProviderContractsTotal != 1 || coverage.ProviderContractsTested != 1 {
		t.Fatalf("unexpected provider contract projection: %#v", coverage)
	}
	if coverage.ObservabilityTotal != 1 || coverage.ObservabilityTested != 1 {
		t.Fatalf("unexpected observability projection: %#v", coverage)
	}
	if coverage.PerformanceBoundsTotal != 1 || coverage.PerformanceBoundsTested != 1 {
		t.Fatalf("unexpected performance projection: %#v", coverage)
	}
	if coverage.E2EJourneysTotal != 1 || coverage.E2EJourneysScripted != 1 {
		t.Fatalf("unexpected scripted e2e projection: %#v", coverage)
	}
	if !containsString(coverage.GatesPassed, "contract") {
		t.Fatalf("complete design contract should project contract gate passed: %#v", coverage)
	}
	if coverage.MutationMinScore != 80 || coverage.MutationScore == nil || *coverage.MutationScore != 91 {
		t.Fatalf("unexpected mutation projection: %#v", coverage)
	}
	if len(coverage.GatesBlocked) != 0 {
		t.Fatalf("passing contract projection should not block gates: %#v", coverage.GatesBlocked)
	}

	ledger := ProjectBehavioralCoverageContractLedger(contract)
	if ledger.EffortID != "effort-test" || ledger.TaskID != "task-test" || ledger.CoverageTier != "contract_high" {
		t.Fatalf("unexpected projected ledger identity: %#v", ledger)
	}
	if len(ledger.Obligations) != 1 || ledger.Obligations[0].ID != "TDD-1" || ledger.Obligations[0].Command == "" {
		t.Fatalf("unexpected projected obligations: %#v", ledger.Obligations)
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedBDDConcreteData(t *testing.T) {
	contract := BehavioralCoverageContract{
		BDDScenarios: []BDDScenarioCoverage{{ID: "BDD-1", Status: "accepted"}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted BDD scenario to fail")
	}
	if !strings.Contains(err.Error(), "bdd_scenarios[0] accepted coverage missing concrete") ||
		!strings.Contains(err.Error(), "bdd_scenarios[0] accepted coverage missing given") ||
		!strings.Contains(err.Error(), "bdd_scenarios[0] accepted coverage missing when") ||
		!strings.Contains(err.Error(), "bdd_scenarios[0] accepted coverage missing then") ||
		!strings.Contains(err.Error(), "bdd_scenarios[0] accepted coverage missing concrete_data_refs") {
		t.Fatalf("expected concrete data error, got %v", err)
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.BDDTotal != 1 || coverage.BDDWithConcreteData != 0 {
		t.Fatalf("projection should preserve incomplete BDD counters, got %#v", coverage)
	}
}

func TestBehavioralCoverageProjectionDoesNotPassContractGateForWaivedOnlyDesignContracts(t *testing.T) {
	contract := BehavioralCoverageContract{
		DesignContracts: []DesignContractCoverage{{
			ID:             "DBC-WAIVED",
			Status:         "waived",
			Boundary:       "external provider",
			Preconditions:  []string{"provider unavailable in fixture"},
			Postconditions: []string{"waiver documented"},
			Invariants:     []string{"no accepted handoff depends on provider"},
			TestRefs:       []string{"WAIVER-001"},
		}},
	}

	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		t.Fatalf("waived design contract should remain valid as a waiver artifact: %v", err)
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if containsString(coverage.GatesPassed, "contract") {
		t.Fatalf("waived-only design contracts must not pass contract gate: %#v", coverage)
	}
	if !containsString(coverage.GatesBlocked, "contract") {
		t.Fatalf("waived-only design contracts should leave contract gate blocked: %#v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedInvariant(t *testing.T) {
	contract := BehavioralCoverageContract{
		Invariants: []InvariantCoverage{{ID: "INV-1", Status: "accepted"}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted invariant to fail")
	}
	for _, want := range []string{
		"invariants[0] accepted coverage missing property_based_test_refs",
		"invariants[0] accepted coverage missing statement",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected invariant error containing %q, got %v", want, err)
		}
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.InvariantsTotal != 1 || coverage.InvariantsWithPBT != 0 {
		t.Fatalf("projection should preserve incomplete invariant counters, got %#v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedStateMachine(t *testing.T) {
	contract := BehavioralCoverageContract{
		StateMachines: []StateMachineCoverage{
			{
				ID:     "SM-1",
				States: []string{"open", "closed"},
				Transitions: []StateTransitionCoverage{
					{ID: "TR-1", From: "open", To: "closed", Event: "close"},
				},
			},
		},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted state machine to fail")
	}
	for _, want := range []string{
		"state_machines[0] accepted coverage missing covered transitions",
		"state_machines[0] transitions[0] missing test_refs",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected state-machine error containing %q, got %v", want, err)
		}
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.StateMachinesTotal != 1 || coverage.StateMachinesCovered != 0 {
		t.Fatalf("projection should preserve incomplete state-machine counters, got %#v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedFailureMode(t *testing.T) {
	contract := BehavioralCoverageContract{
		FailureModes: []FailureModeCoverage{{ID: "FM-1", Status: "accepted"}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted failure mode to fail")
	}
	for _, want := range []string{
		"failure_modes[0] accepted coverage missing mitigation_test_refs",
		"failure_modes[0] accepted coverage missing mode",
		"failure_modes[0] accepted coverage missing mitigation",
		"failure_modes[0] accepted coverage missing positive rpn",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected failure-mode error containing %q, got %v", want, err)
		}
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.FMEAModesTotal != 1 || coverage.FMEAModesMitigated != 0 {
		t.Fatalf("projection should preserve incomplete failure-mode counters, got %#v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedRiskAndModelDetails(t *testing.T) {
	contract := BehavioralCoverageContract{
		HAZOPDeviations: []HAZOPDeviationCoverage{{ID: "HAZOP-1", Status: "accepted", TestRefs: []string{"TestHAZOP"}}},
		FaultTreeLeaves: []FaultTreeLeafCoverage{{ID: "FTA-1", Status: "accepted", TestRefs: []string{"TestFaultTree"}}},
		PostMortems:     []PostMortemCoverage{{ID: "PM-1", Status: "accepted", CatchingTestRefs: []string{"TestRegression"}}},
		FormalModels:    []FormalModelCoverage{{ID: "FORMAL-1", Status: "accepted", VerificationRefs: []string{"model.tla"}}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted risk/model coverage to fail")
	}

	for _, want := range []string{
		"hazop_deviations[0] accepted coverage missing guide_word",
		"hazop_deviations[0] accepted coverage missing deviation",
		"hazop_deviations[0] accepted coverage missing workflow_step",
		"fault_tree_leaves[0] accepted coverage missing top_event",
		"fault_tree_leaves[0] accepted coverage missing cause",
		"post_mortems[0] accepted coverage missing incident",
		"formal_models[0] accepted coverage missing model",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestBehavioralCoverageContractRejectsIncompleteAcceptedProductionReadinessDetails(t *testing.T) {
	contract := BehavioralCoverageContract{
		FuzzTargets:       []FuzzTargetCoverage{{ID: "FUZZ-1", Status: "accepted", Target: "ParseToken"}},
		RAGEvals:          []RAGEvaluationCoverage{{ID: "RAG-1", Status: "accepted", DatasetRefs: []string{"evals/rag.jsonl"}}},
		SecurityCases:     []SecurityCaseCoverage{{ID: "SEC-1", Status: "accepted", Risk: "prompt injection"}},
		PrivacyControls:   []PrivacyControlCoverage{{ID: "PRIV-1", Status: "accepted", DataClass: "prompt traces"}},
		ProviderContracts: []ProviderContractCoverage{{ID: "PACT-1", Status: "accepted", Provider: "llm"}},
		Observability:     []ObservabilityCoverage{{ID: "OBS-1", Status: "accepted", Signal: "planner_turns"}},
		PerformanceBounds: []PerformanceBoundCoverage{{ID: "PERF-1", Status: "accepted", Operation: "planning"}},
		E2EJourneys:       []E2EJourneyCoverage{{ID: "E2E-1", Status: "accepted", Journey: "greenfield planning"}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected incomplete accepted production-readiness coverage to fail")
	}
	for _, want := range []string{
		"fuzz_targets[0] accepted coverage missing target, generators, invariants, or test_refs",
		"rag_evals[0] accepted coverage missing dataset_refs, metrics, thresholds, or eval_refs",
		"security_cases[0] accepted coverage missing risk, attack, expected outcome, or test_refs",
		"privacy_controls[0] accepted coverage missing data_class, policy, redaction_refs, retention, or test_refs",
		"provider_contracts[0] accepted coverage missing provider, consumer, contract_refs, or test_refs",
		"observability[0] accepted coverage missing signal, instrumented_path, required_fields, or test_refs",
		"performance_bounds[0] accepted coverage missing operation, bound, or measurement_refs",
		"e2e_journeys[0] accepted coverage missing journey, steps, or script_refs",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.FuzzTargetsTotal != 1 || coverage.FuzzTargetsCovered != 0 ||
		coverage.RAGEvalsTotal != 1 || coverage.RAGEvalsMeasured != 0 ||
		coverage.SecurityCasesTotal != 1 || coverage.SecurityCasesTested != 0 ||
		coverage.PrivacyControlsTotal != 1 || coverage.PrivacyControlsTested != 0 ||
		coverage.ProviderContractsTotal != 1 || coverage.ProviderContractsTested != 0 ||
		coverage.ObservabilityTotal != 1 || coverage.ObservabilityTested != 0 ||
		coverage.PerformanceBoundsTotal != 1 || coverage.PerformanceBoundsTested != 0 ||
		coverage.E2EJourneysTotal != 1 || coverage.E2EJourneysScripted != 0 {
		t.Fatalf("projection should preserve incomplete production-readiness counters, got %#v", coverage)
	}
}

func TestBehavioralCoverageContractValidatesSurfaceInventoryDecisions(t *testing.T) {
	contract := BehavioralCoverageContract{
		TestObligations: []TestObligationCoverage{{
			ID:       "TDD-1",
			Type:     "unit",
			Behavior: "domain behavior is covered",
			Command:  "go test ./...",
		}},
		SurfaceInventory: []BehavioralSurfaceCoverage{{
			ID:          "SURF-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "core requested behavior",
			Obligations: []string{"TDD-1"},
		}, {
			ID:        "SURF-API",
			Surface:   "api_transport",
			Status:    "not_required",
			Rationale: "no external transport boundary is changed",
		}, {
			ID:        "SURF-PRIVACY",
			Surface:   "privacy",
			Status:    "waived",
			Rationale: "no user data is processed in this increment",
		}},
	}

	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		t.Fatalf("valid surface inventory should pass: %v", err)
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if !containsString(coverage.GatesPassed, "surface_inventory") || containsString(coverage.GatesBlocked, "surface_inventory") {
		t.Fatalf("surface inventory should pass projection gate: %+v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsInScopeSurfaceWithoutObligation(t *testing.T) {
	contract := BehavioralCoverageContract{
		SurfaceInventory: []BehavioralSurfaceCoverage{{
			ID:           "SURF-PERSISTENCE",
			Surface:      "persistence",
			Status:       "in_scope",
			Rationale:    "durable behavior is requested",
			EvidenceRefs: []string{"docs/design.md#persistence"},
		}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil || !strings.Contains(err.Error(), "in-scope surface missing tdd_obligations") {
		t.Fatalf("expected in-scope surface without obligations to fail, got %v", err)
	}
}

func TestBehavioralCoverageContractRejectsNonRequiredSurfaceWithoutRationale(t *testing.T) {
	for _, status := range []string{"not_required", "waived"} {
		contract := BehavioralCoverageContract{
			SurfaceInventory: []BehavioralSurfaceCoverage{{
				ID:      "SURF-1",
				Surface: "api_transport",
				Status:  status,
			}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		if err == nil || !strings.Contains(err.Error(), status+" surface missing rationale") {
			t.Fatalf("expected %s surface without rationale to fail, got %v", status, err)
		}
	}
}

func TestBehavioralCoverageContractRejectsDuplicateAndConflictingSurfaceDecisions(t *testing.T) {
	contract := BehavioralCoverageContract{
		TestObligations: []TestObligationCoverage{{
			ID:       "TDD-1",
			Behavior: "domain behavior is covered",
			Command:  "go test ./...",
		}},
		SurfaceInventory: []BehavioralSurfaceCoverage{{
			ID:          "SURF-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "domain behavior is in scope",
			Obligations: []string{"TDD-1"},
		}, {
			ID:        "SURF-DOMAIN",
			Surface:   "api_transport",
			Status:    "not_required",
			Rationale: "duplicate id must not pass",
		}, {
			ID:        "SURF-DOMAIN-CONFLICT",
			Surface:   "domain_logic",
			Status:    "not_required",
			Rationale: "same surface cannot be both in scope and not required",
		}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected duplicate/conflicting surface decisions to fail")
	}
	for _, want := range []string{"duplicates id", "conflicts for surface"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected surface consistency error containing %q, got %v", want, err)
		}
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if !containsString(coverage.GatesBlocked, "surface_inventory") {
		t.Fatalf("conflicting surface inventory should block projection readiness: %+v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsUnknownSurfaceStatus(t *testing.T) {
	for _, status := range []string{"", "planned", "unknown_status"} {
		contract := BehavioralCoverageContract{
			SurfaceInventory: []BehavioralSurfaceCoverage{{
				ID:        "SURF-1",
				Surface:   "domain_logic",
				Status:    status,
				Rationale: "ambiguous status must not pass",
			}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		if err == nil {
			t.Fatalf("expected status %q to fail", status)
		}
	}
}

func TestBehavioralCoverageContractOpenQuestionSurfaceBlocksProjectionReadiness(t *testing.T) {
	contract := BehavioralCoverageContract{
		SurfaceInventory: []BehavioralSurfaceCoverage{{
			ID:           "SURF-API",
			Surface:      "api_transport",
			Status:       "open_question",
			Rationale:    "operator must choose CLI or HTTP boundary",
			QuestionRefs: []string{"q-api-boundary"},
		}},
	}

	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		t.Fatalf("open_question should be valid structured coverage state: %v", err)
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if !containsString(coverage.GatesBlocked, "surface_inventory") {
		t.Fatalf("open_question should block readiness projection: %+v", coverage)
	}
}

func TestBehavioralCoverageContractIgnoresWaivedAndNotRequiredProductionReadinessCounters(t *testing.T) {
	contract := BehavioralCoverageContract{
		FuzzTargets: []FuzzTargetCoverage{
			{ID: "FUZZ-WAIVED", Status: "waived"},
			{ID: "FUZZ-NR", Status: "not_required"},
		},
		SecurityCases: []SecurityCaseCoverage{
			{ID: "SEC-WAIVED", Status: "waived"},
			{ID: "SEC-NR", Status: "not_required"},
		},
		PrivacyControls: []PrivacyControlCoverage{
			{ID: "PRIV-WAIVED", Status: "waived"},
			{ID: "PRIV-NR", Status: "not_required"},
		},
	}

	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		t.Fatalf("waived/not_required production-readiness entries should validate without complete evidence, got %v", err)
	}

	coverage := ProjectBehavioralCoverageContract(contract)
	if coverage.FuzzTargetsTotal != 0 || coverage.SecurityCasesTotal != 0 || coverage.PrivacyControlsTotal != 0 {
		t.Fatalf("waived/not_required entries must not inflate coverage totals, got %#v", coverage)
	}
}

func TestBehavioralCoverageContractRejectsInvalidState(t *testing.T) {
	contract := BehavioralCoverageContract{
		BDDScenarios: []BDDScenarioCoverage{{ID: "BDD-1", Status: "planned", ConcreteDataRefs: []string{"fixture.json"}}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected invalid status to fail")
	}
	if !strings.Contains(err.Error(), `invalid status "planned"`) {
		t.Fatalf("expected invalid status error, got %v", err)
	}
}

func TestBehavioralCoverageContractRejectsAmbiguousRulesAndDanglingRefs(t *testing.T) {
	contract := BehavioralCoverageContract{
		EARS: []EARSRequirementCoverage{{
			ID:          "REQ-1",
			Statement:   "The system should respond appropriately.",
			MappedRefs:  []string{"BDD-MISSING"},
			Obligations: []string{"TDD-MISSING"},
		}},
		ExampleRules: []ExampleRuleCoverage{{
			ID:            "RULE-1",
			RequirementID: "REQ-MISSING",
			Rule:          "A rule with too little concrete coverage.",
			Examples: []ConcreteExampleCoverage{{
				ID:               "EX-1",
				ConcreteDataRefs: []string{"fixture.json"},
			}},
		}},
		BDDScenarios: []BDDScenarioCoverage{{
			ID:               "BDD-1",
			RequirementID:    "REQ-MISSING",
			RuleID:           "RULE-MISSING",
			Given:            []string{"a linked scenario exists"},
			When:             []string{"contract validates"},
			Then:             []string{"unknown refs fail"},
			ConcreteDataRefs: []string{"fixture.json"},
		}},
		TestObligations: []TestObligationCoverage{{
			ID:             "TDD-1",
			Type:           "contract",
			Behavior:       "known behavior",
			Command:        "go test ./...",
			RequiredBefore: "implementation",
		}},
	}

	err := ValidateBehavioralCoverageContract(contract)
	if err == nil {
		t.Fatal("expected ambiguous and dangling behavioral coverage to fail")
	}
	for _, want := range []string{
		"ambiguous statement language",
		`mapped_refs[0] references unknown coverage artifact or source ref "BDD-MISSING"`,
		"requires at least two concrete examples",
		`requirement_id references unknown coverage artifact "REQ-MISSING"`,
		`rule_id references unknown coverage artifact "RULE-MISSING"`,
		`references unknown obligation "TDD-MISSING"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %v", want, err)
		}
	}
}

func TestBehavioralCoverageContractRejectsRefsWhenAuthoritySetIsEmpty(t *testing.T) {
	obligationRef := BehavioralCoverageContract{
		EARS: []EARSRequirementCoverage{{
			ID:          "REQ-1",
			Statement:   "When behavior is accepted, the system shall require owned test obligations.",
			Obligations: []string{"TDD-MISSING"},
		}},
	}
	err := ValidateBehavioralCoverageContract(obligationRef)
	if err == nil || !strings.Contains(err.Error(), `references unknown obligation "TDD-MISSING"`) {
		t.Fatalf("expected missing obligation authority to fail, got %v", err)
	}

	requirementRef := BehavioralCoverageContract{
		ExampleRules: []ExampleRuleCoverage{{
			ID:            "RULE-1",
			RequirementID: "REQ-MISSING",
			Rule:          "Dangling requirement refs are invalid.",
			Examples: []ConcreteExampleCoverage{
				{ID: "EX-1", Description: "positive", ConcreteDataRefs: []string{"fixtures/positive.json"}},
				{ID: "EX-2", Description: "negative", ConcreteDataRefs: []string{"fixtures/negative.json"}, Negative: true},
			},
		}},
	}
	err = ValidateBehavioralCoverageContract(requirementRef)
	if err == nil || !strings.Contains(err.Error(), `requirement_id references unknown coverage artifact "REQ-MISSING"`) {
		t.Fatalf("expected missing requirement authority to fail, got %v", err)
	}

	ruleRef := BehavioralCoverageContract{
		EARS: []EARSRequirementCoverage{{
			ID:        "REQ-1",
			Statement: "When behavior is accepted, BDD refs must point at owned rules.",
		}},
		BDDScenarios: []BDDScenarioCoverage{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			RuleID:           "RULE-MISSING",
			Given:            []string{"accepted behavior"},
			When:             []string{"the contract validates"},
			Then:             []string{"dangling refs fail"},
			ConcreteDataRefs: []string{"fixtures/accepted.json"},
		}},
	}
	err = ValidateBehavioralCoverageContract(ruleRef)
	if err == nil || !strings.Contains(err.Error(), `rule_id references unknown coverage artifact "RULE-MISSING"`) {
		t.Fatalf("expected missing rule authority to fail, got %v", err)
	}
}

func TestTDDLedgerValidatesFullBehavioralCoverageProjection(t *testing.T) {
	score := 90.0
	contract := BehavioralCoverageContract{
		HAZOPDeviations: []HAZOPDeviationCoverage{{ID: "HAZOP-1", GuideWord: "No", WorkflowStep: "execution", Deviation: "step skipped", TestRefs: []string{"TestNoStep"}}},
		DecisionTables:  []DecisionTableCoverage{{ID: "DT-1", Conditions: []string{"enabled"}, Rules: []DecisionTableRuleCoverage{{Conditions: map[string]string{"enabled": "true"}, Expected: "run"}}, TestRefs: []string{"TestDecisionTable"}}},
		BoundaryValues:  []BoundaryValueCoverage{{ID: "BV-1", Parameter: "limit", Values: []string{"0", "1"}, TestRefs: []string{"TestLimitBoundary"}}},
		FaultTreeLeaves: []FaultTreeLeafCoverage{{ID: "FTA-1", TopEvent: "unsafe outcome", Cause: "unsafe leaf", TestRefs: []string{"TestFaultLeaf"}}},
		PostMortems:     []PostMortemCoverage{{ID: "PM-1", Incident: "past regression", CatchingTestRefs: []string{"TestRegression"}}},
		FormalModels:    []FormalModelCoverage{{ID: "FORMAL-1", Model: "protocol", VerificationRefs: []string{"model.tla"}}},
		FuzzTargets:     []FuzzTargetCoverage{{ID: "FUZZ-1", Target: "ParseInput", Generators: []string{"random bytes"}, Invariants: []string{"no panic"}, TestRefs: []string{"FuzzParseInput"}}},
		RAGEvals:        []RAGEvaluationCoverage{{ID: "RAG-1", DatasetRefs: []string{"evals/rag.jsonl"}, Metrics: map[string]float64{"faithfulness": 0.96}, Thresholds: map[string]float64{"faithfulness": 0.95}, EvalRefs: []string{"TestRAGEval"}}},
		SecurityCases:   []SecurityCaseCoverage{{ID: "SEC-1", Risk: "prompt injection", Attack: "ignore prior instructions", Expected: "ignored", TestRefs: []string{"TestPromptInjection"}}},
		PrivacyControls: []PrivacyControlCoverage{{ID: "PRIV-1", DataClass: "trace text", Policy: "redact secrets", RedactionRefs: []string{"redaction/traces"}, Retention: "30d", TestRefs: []string{"TestTraceRedaction"}}},
		ProviderContracts: []ProviderContractCoverage{{
			ID:           "PACT-1",
			Provider:     "fake-llm",
			Consumer:     "planner",
			ContractRefs: []string{"contracts/fake_llm.json"},
			TestRefs:     []string{"TestFakeProviderContract"},
		}},
		Observability:     []ObservabilityCoverage{{ID: "OBS-1", Signal: "planner_failures_total", InstrumentedPath: "internal/workflows/featureplanner", RequiredFields: []string{"reason"}, TestRefs: []string{"TestPlannerTelemetry"}}},
		PerformanceBounds: []PerformanceBoundCoverage{{ID: "PERF-1", Operation: "context render", Bound: "allocs stable", MeasurementRefs: []string{"bench/context_render.txt"}}},
		E2EJourneys:       []E2EJourneyCoverage{{ID: "E2E-1", Journey: "fake provider greenfield planning", Steps: []string{"start planner", "submit plan"}, ScriptRefs: []string{"scripts/e2e/fake_provider_feature_planner.sh"}}},
		Mutation:          BehavioralMutationCoverage{Requirement: "required", MinScore: 80, Score: &score},
	}
	coverage := ProjectBehavioralCoverageContract(contract)
	ledger := validBehavioralCoverageLedger(contract, coverage)

	if err := validateTDDLedger(ledger); err != nil {
		t.Fatalf("expected complete full projection to pass, got %v", err)
	}

	checks := []struct {
		name   string
		mutate func(*TDDCoverage)
		want   string
	}{
		{"hazop", func(c *TDDCoverage) { c.HAZOPDeviationsTested = 0 }, "hazop_deviations_tested"},
		{"decision table", func(c *TDDCoverage) { c.DecisionTablesEnumerated = 0 }, "decision_tables_enumerated"},
		{"boundary", func(c *TDDCoverage) { c.BoundaryValuesTested = 0 }, "boundary_values_tested"},
		{"fault tree", func(c *TDDCoverage) { c.FaultTreeLeavesTested = 0 }, "fault_tree_leaves_tested"},
		{"post mortem", func(c *TDDCoverage) { c.PostMortemCatchingTests = 0 }, "post_mortem_catching_tests"},
		{"formal", func(c *TDDCoverage) { c.FormalModelsVerified = 0 }, "formal_models_verified"},
		{"fuzz", func(c *TDDCoverage) { c.FuzzTargetsCovered = 0 }, "fuzz_targets_covered"},
		{"rag", func(c *TDDCoverage) { c.RAGEvalsMeasured = 0 }, "rag_evals_measured"},
		{"security", func(c *TDDCoverage) { c.SecurityCasesTested = 0 }, "security_cases_tested"},
		{"privacy", func(c *TDDCoverage) { c.PrivacyControlsTested = 0 }, "privacy_controls_tested"},
		{"provider", func(c *TDDCoverage) { c.ProviderContractsTested = 0 }, "provider_contracts_tested"},
		{"observability", func(c *TDDCoverage) { c.ObservabilityTested = 0 }, "observability_tested"},
		{"performance", func(c *TDDCoverage) { c.PerformanceBoundsTested = 0 }, "performance_bounds_tested"},
		{"scripted e2e", func(c *TDDCoverage) { c.E2EJourneysScripted = 0 }, "e2e_journeys_scripted"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			mismatched := ledger
			check.mutate(&mismatched.Coverage)
			err := validateTDDLedger(mismatched)
			if err == nil || !strings.Contains(err.Error(), check.want) {
				t.Fatalf("expected mismatch for %s, got %v", check.want, err)
			}
		})
	}
}

func TestProjectedBehavioralCoverageLedgerPassesCoverageGate(t *testing.T) {
	contract := BehavioralCoverageContract{
		EffortID:     "effort-test",
		TaskID:       "task-test",
		CoverageTier: "contract_high",
		EARS: []EARSRequirementCoverage{{
			ID:         "REQ-1",
			Statement:  "When login succeeds, the system shall create a session.",
			MappedRefs: []string{"BDD-1"},
		}},
		BDDScenarios: []BDDScenarioCoverage{{
			ID:               "BDD-1",
			RequirementID:    "REQ-1",
			Given:            []string{"accepted credentials"},
			When:             []string{"login succeeds"},
			Then:             []string{"session is created"},
			ConcreteDataRefs: []string{"fixtures/login.json"},
			Obligations:      []string{"TDD-1"},
		}},
		DesignContracts: []DesignContractCoverage{{
			ID:             "DBC-1",
			Boundary:       "auth/login",
			Preconditions:  []string{"credentials parsed"},
			Postconditions: []string{"session stored"},
			Invariants:     []string{"session owner stable"},
			TestRefs:       []string{"TestLoginContract"},
		}},
		Invariants: []InvariantCoverage{{
			ID:                    "INV-1",
			Statement:             "session owner remains stable",
			PropertyBasedTestRefs: []string{"TestSessionOwnerInvariant"},
		}},
		StateMachines: []StateMachineCoverage{{
			ID:     "SM-1",
			States: []string{"anonymous", "authenticated"},
			Transitions: []StateTransitionCoverage{{
				ID:       "TR-1",
				From:     "anonymous",
				To:       "authenticated",
				Event:    "login",
				TestRefs: []string{"TestLoginTransition"},
			}},
		}},
		TestObligations: []TestObligationCoverage{{
			ID:             "TDD-1",
			Type:           "contract",
			Behavior:       "login creates a session",
			Command:        "go test ./internal/auth",
			RequiredBefore: "implementation",
		}},
	}
	ledger := ProjectBehavioralCoverageContractLedger(contract)
	prd := PRDDocument{
		FrontMatter: PRDFrontMatter{Status: "accepted"},
		Requirements: []PRDRequirement{{
			ID:             "REQ-1",
			EvidenceRefs:   []string{"EV-1"},
			TDDObligations: []string{"TDD-1"},
		}},
	}
	adrIndex := ADRIndex{ADRs: []ADRIndexEntry{{ADRID: "ADR-1", Status: "accepted", Path: "ADR-1.md"}}}
	adrs := []ParsedADR{{
		Doc: ADRDocument{FrontMatter: ADRFrontMatter{ADRID: "ADR-1", Status: "accepted", EvidenceRefs: []string{"EV-1"}, TDDObligations: []string{"TDD-1"}}},
	}}
	traceability := TraceabilityMatrix{Matrix: []TraceabilityRow{{PRDRequirement: "REQ-1", ADR: "ADR-1"}}}
	eval := EvaluateCoverageGate(prd, adrIndex, adrs, ledger, traceability, map[string]bool{"TDD-1": true})

	if CoverageGateHasBlockers(eval) {
		t.Fatalf("projected accepted behavioral coverage should pass gate, got %#v", eval.Blockers)
	}
}

func TestBehavioralCoverageContractProjectsMutationRequirements(t *testing.T) {
	lowScore := 62.0

	tests := []struct {
		name             string
		mutation         BehavioralMutationCoverage
		wantMinScore     int
		wantScore        *float64
		wantBlockedToken string
		wantErrSubstring string
	}{
		{
			name: "required below score blocks legacy mutation gate",
			mutation: BehavioralMutationCoverage{
				Requirement: "required",
				MinScore:    80,
				Score:       &lowScore,
			},
			wantMinScore:     80,
			wantScore:        &lowScore,
			wantBlockedToken: "mutation",
		},
		{
			name: "waived projects unavailable mutation blocker",
			mutation: BehavioralMutationCoverage{
				Requirement:  "waived",
				MinScore:     80,
				WaiverReason: "mutation tool unavailable for generated target",
			},
			wantMinScore:     80,
			wantBlockedToken: "mutation_unavailable_tool",
		},
		{
			name: "not required leaves legacy mutation gate unconfigured",
			mutation: BehavioralMutationCoverage{
				Requirement: "not_required",
			},
		},
		{
			name: "required without score fails validation",
			mutation: BehavioralMutationCoverage{
				Requirement: "required",
				MinScore:    80,
			},
			wantMinScore:     80,
			wantBlockedToken: "mutation",
			wantErrSubstring: "mutation required score is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := BehavioralCoverageContract{Mutation: tt.mutation}
			err := ValidateBehavioralCoverageContract(contract)
			if tt.wantErrSubstring == "" && err != nil {
				t.Fatalf("expected valid mutation contract, got %v", err)
			}
			if tt.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected mutation validation error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstring) {
					t.Fatalf("expected mutation error containing %q, got %v", tt.wantErrSubstring, err)
				}
			}

			coverage := ProjectBehavioralCoverageContract(contract)
			if coverage.MutationMinScore != tt.wantMinScore {
				t.Fatalf("mutation_min_score = %d, want %d", coverage.MutationMinScore, tt.wantMinScore)
			}
			if tt.wantScore != nil {
				if coverage.MutationScore == nil || *coverage.MutationScore != *tt.wantScore {
					t.Fatalf("mutation_score = %v, want %v", coverage.MutationScore, *tt.wantScore)
				}
			}
			if tt.wantBlockedToken != "" && !containsString(coverage.GatesBlocked, tt.wantBlockedToken) {
				t.Fatalf("expected gates_blocked to contain %q, got %#v", tt.wantBlockedToken, coverage.GatesBlocked)
			}
			if tt.wantBlockedToken == "" && len(coverage.GatesBlocked) != 0 {
				t.Fatalf("expected no gates_blocked tokens, got %#v", coverage.GatesBlocked)
			}
		})
	}
}

func TestTDDLedgerValidatesBehavioralCoverageProjectionWhenPresent(t *testing.T) {
	score := 86.0
	contract := BehavioralCoverageContract{
		EARS: []EARSRequirementCoverage{{
			ID:         "REQ-1",
			Statement:  "When behavior is accepted, the system shall preserve coverage projection.",
			MappedRefs: []string{"BDD-1"},
		}},
		BDDScenarios: []BDDScenarioCoverage{{ID: "BDD-1", Given: []string{"accepted behavior"}, When: []string{"contract is projected"}, Then: []string{"coverage matches"}, ConcreteDataRefs: []string{"fixture.json"}}},
		Invariants:   []InvariantCoverage{{ID: "INV-1", Statement: "invariant", PropertyBasedTestRefs: []string{"TestInvariant"}}},
		StateMachines: []StateMachineCoverage{{
			ID:     "SM-1",
			States: []string{"draft", "published"},
			Transitions: []StateTransitionCoverage{
				{ID: "TR-1", From: "draft", To: "published", Event: "publish", TestRefs: []string{"TestPublishTransition"}},
			},
		}},
		FailureModes: []FailureModeCoverage{{ID: "FM-1", Mode: "projection drift", Mitigation: "validate behavioral projection", RPN: 48, MitigationTestRefs: []string{"TestFailureMode"}}},
		Mutation:     BehavioralMutationCoverage{Requirement: "required", MinScore: 80, Score: &score},
	}
	coverage := ProjectBehavioralCoverageContract(contract)

	ledger := validBehavioralCoverageLedger(contract, coverage)
	if err := validateTDDLedger(ledger); err != nil {
		t.Fatalf("expected projected behavioral coverage ledger to pass, got %v", err)
	}

	ledger.Coverage.BDDWithConcreteData = 0
	err := validateTDDLedger(ledger)
	if err == nil {
		t.Fatal("expected mismatched behavioral coverage projection to fail")
	}
	if !strings.Contains(err.Error(), "bdd_with_concrete_data") {
		t.Fatalf("expected BDD projection mismatch, got %v", err)
	}

	ledger = validBehavioralCoverageLedger(contract, coverage)
	ledger.Coverage.EARSMapped = 0
	err = validateTDDLedger(ledger)
	if err == nil {
		t.Fatal("expected mismatched EARS behavioral coverage projection to fail")
	}
	if !strings.Contains(err.Error(), "ears_mapped") {
		t.Fatalf("expected EARS projection mismatch, got %v", err)
	}
}

func TestPromoteBehavioralCoverageContractProjectsLedgerAndMergesObligations(t *testing.T) {
	contract := promotableBehavioralCoverageContract()
	ledger := TDDLedger{
		SchemaVersion: "idiom.tdd_obligations.v2",
		EffortID:      "effort-promote",
		TaskID:        "task-promote",
		CoverageTier:  "behavior_standard",
		Obligations: []TDDObligation{
			{ID: "TDD-KEEP", Type: "unit", Behavior: "keep existing obligation", Command: "go test ./keep", RequiredBefore: "implementation"},
			{ID: "TDD-BDD", Type: "unit", Behavior: "replace stale promoted obligation", Command: "go test ./old", RequiredBefore: "implementation"},
		},
	}

	promoted, err := PromoteBehavioralCoverageContract(ledger, contract)
	if err != nil {
		t.Fatalf("PromoteBehavioralCoverageContract: %v", err)
	}

	if promoted.BehavioralCoverage == nil {
		t.Fatal("expected behavioral coverage contract to be stamped into ledger")
	}
	if promoted.BehavioralCoverage.EffortID != "effort-promote" || promoted.BehavioralCoverage.TaskID != "task-promote" {
		t.Fatalf("expected identity from ledger, got effort=%q task=%q", promoted.BehavioralCoverage.EffortID, promoted.BehavioralCoverage.TaskID)
	}
	if promoted.Coverage.EARSTotal != 1 || promoted.Coverage.BDDTotal != 1 || !containsString(promoted.Coverage.GatesPassed, "contract") {
		t.Fatalf("expected projected coverage counters and contract gate, got %#v", promoted.Coverage)
	}
	if len(promoted.Obligations) != 2 {
		t.Fatalf("expected one existing and one promoted obligation, got %#v", promoted.Obligations)
	}
	if promoted.Obligations[0].ID != "TDD-KEEP" || promoted.Obligations[1].ID != "TDD-BDD" {
		t.Fatalf("expected deterministic obligation merge, got %#v", promoted.Obligations)
	}
	if promoted.Obligations[1].Command != "go test ./behavior" {
		t.Fatalf("expected promoted obligation to replace stale obligation, got %#v", promoted.Obligations[1])
	}
}

func TestAcceptedBehavioralCoverageProjectionFiltersNonAcceptedItems(t *testing.T) {
	contract := promotableBehavioralCoverageContract()
	contract.EARS = append(contract.EARS, EARSRequirementCoverage{
		ID:          "REQ-WAIVED",
		Status:      BehavioralCoverageStatusWaived,
		Statement:   "When waived behavior appears, it shall not become accepted context.",
		MappedRefs:  []string{"BDD-WAIVED"},
		Obligations: []string{"TDD-WAIVED"},
	})
	contract.ExampleRules = append(contract.ExampleRules, ExampleRuleCoverage{
		ID:            "RULE-WAIVED",
		RequirementID: "REQ-WAIVED",
		Rule:          "Waived coverage stays out of accepted context.",
		Examples: []ConcreteExampleCoverage{
			{ID: "EX-WAIVED-1", Description: "waived positive case", ConcreteDataRefs: []string{"fixtures/waived_positive.json"}},
			{ID: "EX-WAIVED-2", Description: "waived negative case", ConcreteDataRefs: []string{"fixtures/waived_negative.json"}, Negative: true},
		},
	})
	contract.BDDScenarios = append(contract.BDDScenarios,
		BDDScenarioCoverage{ID: "BDD-WAIVED", Status: BehavioralCoverageStatusWaived, RequirementID: "REQ-WAIVED", RuleID: "RULE-WAIVED", Given: []string{"waived"}, When: []string{"projected"}, Then: []string{"hidden"}, Obligations: []string{"TDD-WAIVED"}},
		BDDScenarioCoverage{ID: "BDD-NOT-REQUIRED", Status: BehavioralCoverageStatusNotRequired, Given: []string{"not required"}, When: []string{"projected"}, Then: []string{"hidden"}},
	)
	contract.TestObligations = append(contract.TestObligations, TestObligationCoverage{
		ID:             "TDD-WAIVED",
		Type:           "behavior",
		Behavior:       "waived behavior",
		Command:        "go test ./waived",
		RequiredBefore: "implementation",
	})
	contract.Verification = []VerificationGateCoverage{
		{ID: "VERIFY-ACCEPTED", Gate: "accepted", Command: "go test ./behavior", Obligations: []string{"TDD-BDD"}},
		{ID: "VERIFY-WAIVED", Gate: "waived", Command: "go test ./waived", Obligations: []string{"TDD-WAIVED"}},
	}
	ledger := ProjectBehavioralCoverageContractLedger(contract)
	digest := BehavioralCoverageContractDigest(*ledger.BehavioralCoverage)
	projection := ProjectAcceptedBehavioralCoverage(BriefGenerationInput{
		TDD:                ledger,
		PromotionEvents:    []BehavioralCoveragePromotionEvent{{Event: "behavioral_coverage_accepted", State: BehavioralCoveragePromotionStateAccepted, EventID: "evt-accepted", Digest: digest}},
		CoverageEvaluation: CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"},
	})
	if projection == nil {
		ref := implementationHandoffCoverageContract(BriefGenerationInput{
			TDD:                ledger,
			PromotionEvents:    []BehavioralCoveragePromotionEvent{{Event: "behavioral_coverage_accepted", State: BehavioralCoveragePromotionStateAccepted, EventID: "evt-accepted", Digest: digest}},
			CoverageEvaluation: CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"},
		})
		t.Fatalf("expected accepted projection, ref=%#v validate=%v projection=%v", ref, ValidateBehavioralCoverageContract(contract), ValidateBehavioralCoverageProjection(contract, ledger.Coverage))
	}
	if len(projection.Behavioral.BDDScenarios) != 1 || projection.Behavioral.BDDScenarios[0].ID != "BDD-BDD" {
		t.Fatalf("expected only accepted scenario in projection, got %#v", projection.Behavioral.BDDScenarios)
	}
	if projection.Projected.BDDTotal != 1 {
		t.Fatalf("expected projected accepted-only counters, got %#v", projection.Projected)
	}
	if len(projection.Behavioral.ExampleRules) != 1 || projection.Behavioral.ExampleRules[0].ID != "RULE-BDD" {
		t.Fatalf("expected only accepted-requirement rules in projection, got %#v", projection.Behavioral.ExampleRules)
	}
	if len(projection.Behavioral.TestObligations) != 1 || projection.Behavioral.TestObligations[0].ID != "TDD-BDD" {
		t.Fatalf("expected only accepted-obligation tests in projection, got %#v", projection.Behavioral.TestObligations)
	}
	if len(projection.Behavioral.Verification) != 1 || projection.Behavioral.Verification[0].ID != "VERIFY-ACCEPTED" {
		t.Fatalf("expected only accepted-obligation verification in projection, got %#v", projection.Behavioral.Verification)
	}
	if len(projection.Behavioral.SurfaceInventory) != 3 {
		t.Fatalf("expected closed surface decisions to remain in accepted projection, got %#v", projection.Behavioral.SurfaceInventory)
	}
	if projection.Behavioral.SurfaceInventory[0].ID != "SURF-BDD-DOMAIN" ||
		projection.Behavioral.SurfaceInventory[1].Status != BehavioralCoverageStatusNotRequired ||
		projection.Behavioral.SurfaceInventory[2].Status != BehavioralCoverageStatusWaived {
		t.Fatalf("accepted projection should preserve closed surface decisions, got %#v", projection.Behavioral.SurfaceInventory)
	}
}

func TestBehavioralCoveragePromotionLifecycleStatesAndSupersededDigest(t *testing.T) {
	first := promotableBehavioralCoverageContract()
	ledger := TDDLedger{EffortID: "effort-promote", TaskID: "task-promote", CoverageTier: "behavior_standard", Coverage: TDDCoverage{MutationMinScore: 80}}
	reviewed, err := ReviewBehavioralCoverageContractWithResult(ledger, first)
	if err != nil {
		t.Fatalf("ReviewBehavioralCoverageContractWithResult: %v", err)
	}
	if reviewed.State != BehavioralCoveragePromotionStateReviewed || reviewed.EventID == "" || reviewed.Digest == "" {
		t.Fatalf("expected reviewed lifecycle result, got %#v", reviewed)
	}
	accepted, err := PromoteBehavioralCoverageContractWithResult(ledger, first)
	if err != nil {
		t.Fatalf("PromoteBehavioralCoverageContractWithResult: %v", err)
	}
	second := promotableBehavioralCoverageContract()
	second.ContractID = "cov-promote-2"
	second.BDDScenarios[0].ID = "BDD-BDD-2"
	second.EARS[0].MappedRefs = []string{"BDD-BDD-2"}
	second.ExampleRules[0].ID = "RULE-BDD-2"
	second.BDDScenarios[0].RuleID = "RULE-BDD-2"
	superseding, err := PromoteBehavioralCoverageContractWithResult(accepted.Ledger, second)
	if err != nil {
		t.Fatalf("superseding promote: %v", err)
	}
	if superseding.State != BehavioralCoveragePromotionStateAccepted || superseding.SupersededDigest != accepted.Digest {
		t.Fatalf("expected accepted superseding result, got %#v previous=%s", superseding, accepted.Digest)
	}
}

func TestCommitBehavioralCoveragePromotionWritesPreparedThenCommittedAcceptedEvent(t *testing.T) {
	dir := t.TempDir()
	contract := promotableBehavioralCoverageContract()
	ledger := TDDLedger{EffortID: "effort-promote", TaskID: "task-promote", CoverageTier: "behavior_standard", Coverage: TDDCoverage{MutationMinScore: 80}}

	result, err := CommitBehavioralCoveragePromotion(dir, ledger, contract)
	if err != nil {
		t.Fatalf("CommitBehavioralCoveragePromotion: %v", err)
	}
	if result.State != BehavioralCoveragePromotionStateAccepted ||
		result.CommitStatus != BehavioralCoveragePromotionCommitStatusCommitted ||
		!result.LedgerCommitted ||
		result.EventID == "" ||
		result.Digest == "" {
		t.Fatalf("expected committed accepted promotion result, got %#v", result)
	}

	ledgerData, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written TDDLedger
	if err := json.Unmarshal(ledgerData, &written); err != nil {
		t.Fatalf("written tdd.json should decode: %v\n%s", err, string(ledgerData))
	}
	if written.BehavioralCoverage == nil || BehavioralCoverageContractDigest(*written.BehavioralCoverage) != result.Digest {
		t.Fatalf("written ledger should contain committed behavioral coverage, got %#v", written.BehavioralCoverage)
	}

	eventsData, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := ParseBehavioralCoveragePromotionEventsJSONL(eventsData)
	if err != nil {
		t.Fatalf("events should parse: %v\n%s", err, string(eventsData))
	}
	if len(events) != 2 {
		t.Fatalf("expected prepared and accepted events, got %#v", events)
	}
	if events[0].State != BehavioralCoveragePromotionStatePrepared || events[0].CommitStatus != BehavioralCoveragePromotionCommitStatusPrepared {
		t.Fatalf("first event should be prepared, got %#v", events[0])
	}
	if events[1].State != BehavioralCoveragePromotionStateAccepted || events[1].CommitStatus != BehavioralCoveragePromotionCommitStatusCommitted {
		t.Fatalf("second event should be committed accepted, got %#v", events[1])
	}
	if accepted := AcceptedBehavioralCoveragePromotionEvent(events, result.Digest); accepted == nil || accepted.EventID != result.EventID {
		t.Fatalf("accepted event lookup should ignore prepared event and return committed accepted event, got %#v", accepted)
	}
}

func validBehavioralCoverageLedger(contract BehavioralCoverageContract, coverage TDDCoverage) TDDLedger {
	return TDDLedger{
		SchemaVersion:      "idiom.tdd_obligations.v2",
		EffortID:           "effort-test",
		TaskID:             "task-test",
		CoverageTier:       "contract_high",
		Coverage:           coverage,
		BehavioralCoverage: &contract,
		Obligations: []TDDObligation{
			{
				ID:             "TDD-1",
				Type:           "contract",
				Behavior:       "test accepted behavioral coverage",
				Owner:          "productworkflow",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "close",
				Status:         "pending",
			},
		},
	}
}

func promotableBehavioralCoverageContract() BehavioralCoverageContract {
	return BehavioralCoverageContract{
		CoverageTier: "behavior_standard",
		EARS: []EARSRequirementCoverage{{
			ID:          "REQ-BDD",
			Pattern:     "event-driven",
			Statement:   "When the user approves a plan, the system shall persist behavioral coverage.",
			MappedRefs:  []string{"BDD-BDD"},
			Obligations: []string{"TDD-BDD"},
		}},
		ExampleRules: []ExampleRuleCoverage{{
			ID:            "RULE-BDD",
			RequirementID: "REQ-BDD",
			Rule:          "Approved coverage is projected into tdd.json.",
			Examples: []ConcreteExampleCoverage{
				{ID: "EX-BDD-1", Description: "coverage is accepted", ConcreteDataRefs: []string{"fixtures/coverage_accepted.json"}},
				{ID: "EX-BDD-2", Description: "coverage is missing", ConcreteDataRefs: []string{"fixtures/coverage_missing.json"}, Negative: true},
			},
		}},
		BDDScenarios: []BDDScenarioCoverage{{
			ID:               "BDD-BDD",
			RequirementID:    "REQ-BDD",
			RuleID:           "RULE-BDD",
			Given:            []string{"a valid behavioral coverage contract"},
			When:             []string{"the contract is promoted"},
			Then:             []string{"the TDD ledger contains projected coverage"},
			ConcreteDataRefs: []string{"fixtures/coverage_accepted.json"},
			Obligations:      []string{"TDD-BDD"},
		}},
		DesignContracts: []DesignContractCoverage{{
			ID:             "DBC-BDD",
			Boundary:       "productworkflow/tdd promotion",
			Preconditions:  []string{"contract validates"},
			Postconditions: []string{"ledger projection matches contract"},
			Invariants:     []string{"productworkflow owns accepted coverage projection"},
			TestRefs:       []string{"TestPromoteBehavioralCoverageContractProjectsLedgerAndMergesObligations"},
			Obligations:    []string{"TDD-BDD"},
		}},
		SurfaceInventory: []BehavioralSurfaceCoverage{{
			ID:          "SURF-BDD-DOMAIN",
			Surface:     "domain_logic",
			Status:      "in_scope",
			Rationale:   "coverage promotion is the requested behavior",
			Obligations: []string{"TDD-BDD"},
		}, {
			ID:        "SURF-BDD-TRANSPORT",
			Surface:   "api_transport",
			Status:    BehavioralCoverageStatusNotRequired,
			Rationale: "no external transport boundary changes in this product workflow contract",
		}, {
			ID:        "SURF-BDD-PRIVACY",
			Surface:   "privacy",
			Status:    BehavioralCoverageStatusWaived,
			Rationale: "fixture does not process user data",
		}},
		TestObligations: []TestObligationCoverage{{
			ID:             "TDD-BDD",
			Type:           "behavior",
			Behavior:       "promote accepted behavioral coverage into tdd.json",
			Owner:          "productworkflow",
			Command:        "go test ./behavior",
			RequiredBefore: "implementation",
		}},
	}
}
