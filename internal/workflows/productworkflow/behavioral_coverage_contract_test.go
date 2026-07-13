package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("behavioral coverage contract", func() {
	ginkgo.It("BehavioralCoverageContractProjectsCompleteAcceptedCoverage", func() {
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
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid contract, got %v", err))
		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.EARSTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected EARS projection: %#v", coverage))
		gomega.Expect(coverage.EARSMapped).To(gomega.Equal(1), fmt.Sprintf("unexpected EARS projection: %#v", coverage))
		gomega.Expect(coverage.BDDTotal).To(gomega.Equal(2), fmt.Sprintf("unexpected BDD projection: %#v", coverage))
		gomega.Expect(coverage.BDDWithConcreteData).To(gomega.Equal(2), fmt.Sprintf("unexpected BDD projection: %#v", coverage))
		gomega.Expect(coverage.InvariantsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected invariant projection: %#v", coverage))
		gomega.Expect(coverage.InvariantsWithPBT).To(gomega.Equal(1), fmt.Sprintf("unexpected invariant projection: %#v", coverage))
		gomega.Expect(coverage.StateMachinesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected state-machine projection: %#v", coverage))
		gomega.Expect(coverage.StateMachinesCovered).To(gomega.Equal(1), fmt.Sprintf("unexpected state-machine projection: %#v", coverage))
		gomega.Expect(coverage.FMEAModesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected failure-mode projection: %#v", coverage))
		gomega.Expect(coverage.FMEAModesMitigated).To(gomega.Equal(1), fmt.Sprintf("unexpected failure-mode projection: %#v", coverage))
		gomega.Expect(coverage.HAZOPDeviationsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected HAZOP projection: %#v", coverage))
		gomega.Expect(coverage.HAZOPDeviationsTested).To(gomega.Equal(1), fmt.Sprintf("unexpected HAZOP projection: %#v", coverage))
		gomega.Expect(coverage.DecisionTablesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected decision-table projection: %#v", coverage))
		gomega.Expect(coverage.DecisionTablesEnumerated).To(gomega.Equal(1), fmt.Sprintf("unexpected decision-table projection: %#v", coverage))
		gomega.Expect(coverage.BoundaryValuesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected boundary-value projection: %#v", coverage))
		gomega.Expect(coverage.BoundaryValuesTested).To(gomega.Equal(1), fmt.Sprintf("unexpected boundary-value projection: %#v", coverage))
		gomega.Expect(coverage.FaultTreeLeavesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected fault-tree projection: %#v", coverage))
		gomega.Expect(coverage.FaultTreeLeavesTested).To(gomega.Equal(1), fmt.Sprintf("unexpected fault-tree projection: %#v", coverage))
		gomega.Expect(coverage.PostMortemIncidentsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected post-mortem projection: %#v", coverage))
		gomega.Expect(coverage.PostMortemCatchingTests).To(gomega.Equal(1), fmt.Sprintf("unexpected post-mortem projection: %#v", coverage))
		gomega.Expect(coverage.FormalModelsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected formal model projection: %#v", coverage))
		gomega.Expect(coverage.FormalModelsVerified).To(gomega.Equal(1), fmt.Sprintf("unexpected formal model projection: %#v", coverage))
		gomega.Expect(coverage.FuzzTargetsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected fuzz projection: %#v", coverage))
		gomega.Expect(coverage.FuzzTargetsCovered).To(gomega.Equal(1), fmt.Sprintf("unexpected fuzz projection: %#v", coverage))
		gomega.Expect(coverage.RAGEvalsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected RAG eval projection: %#v", coverage))
		gomega.Expect(coverage.RAGEvalsMeasured).To(gomega.Equal(1), fmt.Sprintf("unexpected RAG eval projection: %#v", coverage))
		gomega.Expect(coverage.SecurityCasesTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected security projection: %#v", coverage))
		gomega.Expect(coverage.SecurityCasesTested).To(gomega.Equal(1), fmt.Sprintf("unexpected security projection: %#v", coverage))
		gomega.Expect(coverage.PrivacyControlsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected privacy projection: %#v", coverage))
		gomega.Expect(coverage.PrivacyControlsTested).To(gomega.Equal(1), fmt.Sprintf("unexpected privacy projection: %#v", coverage))
		gomega.Expect(coverage.ProviderContractsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected provider contract projection: %#v", coverage))
		gomega.Expect(coverage.ProviderContractsTested).To(gomega.Equal(1), fmt.Sprintf("unexpected provider contract projection: %#v", coverage))
		gomega.Expect(coverage.ObservabilityTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected observability projection: %#v", coverage))
		gomega.Expect(coverage.ObservabilityTested).To(gomega.Equal(1), fmt.Sprintf("unexpected observability projection: %#v", coverage))
		gomega.Expect(coverage.PerformanceBoundsTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected performance projection: %#v", coverage))
		gomega.Expect(coverage.PerformanceBoundsTested).To(gomega.Equal(1), fmt.Sprintf("unexpected performance projection: %#v", coverage))
		gomega.Expect(coverage.E2EJourneysTotal).To(gomega.Equal(1), fmt.Sprintf("unexpected scripted e2e projection: %#v", coverage))
		gomega.Expect(coverage.E2EJourneysScripted).To(gomega.Equal(1), fmt.Sprintf("unexpected scripted e2e projection: %#v", coverage))
		gomega.Expect(!containsString(coverage.GatesPassed, "contract")).To(gomega.BeFalse(), fmt.Sprintf("complete design contract should project contract gate passed: %#v", coverage))
		gomega.Expect(coverage.MutationMinScore).To(gomega.Equal(80), fmt.Sprintf("unexpected mutation projection: %#v", coverage))
		gomega.Expect(coverage.MutationScore).NotTo(gomega.BeNil(), fmt.Sprintf("unexpected mutation projection: %#v", coverage))
		gomega.Expect(*coverage.MutationScore).To(gomega.Equal(91.0), fmt.Sprintf("unexpected mutation projection: %#v", coverage))
		gomega.Expect(len(coverage.GatesBlocked) != 0).To(gomega.BeFalse(), fmt.Sprintf("passing contract projection should not block gates: %#v", coverage.GatesBlocked))

		ledger := validBehavioralCoverageLedger(contract, coverage)
		gomega.Expect(ledger.EffortID).To(gomega.Equal("effort-test"), fmt.Sprintf("unexpected projected ledger identity: %#v", ledger))
		gomega.Expect(ledger.TaskID).To(gomega.Equal("task-test"), fmt.Sprintf("unexpected projected ledger identity: %#v", ledger))
		gomega.Expect(ledger.CoverageTier).To(gomega.Equal("contract_high"), fmt.Sprintf("unexpected projected ledger identity: %#v", ledger))
		gomega.Expect(ledger.Obligations).To(gomega.HaveLen(1), fmt.Sprintf("unexpected projected obligations: %#v", ledger.Obligations))
		gomega.Expect(ledger.Obligations[0].ID).To(gomega.Equal("TDD-1"), fmt.Sprintf("unexpected projected obligations: %#v", ledger.Obligations))
		gomega.Expect(ledger.Obligations[0].Command).NotTo(gomega.BeEmpty(), fmt.Sprintf("unexpected projected obligations: %#v", ledger.Obligations))

	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedBDDConcreteData", func() {
		contract := BehavioralCoverageContract{
			BDDScenarios: []BDDScenarioCoverage{{ID: "BDD-1", Status: "accepted"}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted BDD scenario to fail")
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("bdd_scenarios[0] accepted coverage missing concrete"), fmt.Sprintf("expected concrete data error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("bdd_scenarios[0] accepted coverage missing given"), fmt.Sprintf("expected concrete data error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("bdd_scenarios[0] accepted coverage missing when"), fmt.Sprintf("expected concrete data error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("bdd_scenarios[0] accepted coverage missing then"), fmt.Sprintf("expected concrete data error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("bdd_scenarios[0] accepted coverage missing concrete_data_refs"), fmt.Sprintf("expected concrete data error, got %v", err))

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.BDDTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete BDD counters, got %#v", coverage))
		gomega.Expect(coverage.BDDWithConcreteData).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete BDD counters, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageProjectionDoesNotPassContractGateForWaivedOnlyDesignContracts", func() {
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
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("waived design contract should remain valid as a waiver artifact: %v", err))
		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(containsString(coverage.GatesPassed, "contract")).To(gomega.BeFalse(), fmt.Sprintf("waived-only design contracts must not pass contract gate: %#v", coverage))
		gomega.Expect(!containsString(coverage.GatesBlocked, "contract")).To(gomega.BeFalse(), fmt.Sprintf("waived-only design contracts should leave contract gate blocked: %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedInvariant", func() {
		contract := BehavioralCoverageContract{
			Invariants: []InvariantCoverage{{ID: "INV-1", Status: "accepted"}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted invariant to fail")

		for _, want := range []string{
			"invariants[0] accepted coverage missing property_based_test_refs",
			"invariants[0] accepted coverage missing statement",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected invariant error containing %q, got %v", want, err))

		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.InvariantsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete invariant counters, got %#v", coverage))
		gomega.Expect(coverage.InvariantsWithPBT).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete invariant counters, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedStateMachine", func() {
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
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted state machine to fail")

		for _, want := range []string{
			"state_machines[0] accepted coverage missing covered transitions",
			"state_machines[0] transitions[0] missing test_refs",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected state-machine error containing %q, got %v", want, err))

		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.StateMachinesTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete state-machine counters, got %#v", coverage))
		gomega.Expect(coverage.StateMachinesCovered).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete state-machine counters, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedFailureMode", func() {
		contract := BehavioralCoverageContract{
			FailureModes: []FailureModeCoverage{{ID: "FM-1", Status: "accepted"}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted failure mode to fail")

		for _, want := range []string{
			"failure_modes[0] accepted coverage missing mitigation_test_refs",
			"failure_modes[0] accepted coverage missing mode",
			"failure_modes[0] accepted coverage missing mitigation",
			"failure_modes[0] accepted coverage missing positive rpn",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected failure-mode error containing %q, got %v", want, err))

		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.FMEAModesTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete failure-mode counters, got %#v", coverage))
		gomega.Expect(coverage.FMEAModesMitigated).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete failure-mode counters, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedRiskAndModelDetails", func() {
		contract := BehavioralCoverageContract{
			HAZOPDeviations: []HAZOPDeviationCoverage{{ID: "HAZOP-1", Status: "accepted", TestRefs: []string{"TestHAZOP"}}},
			FaultTreeLeaves: []FaultTreeLeafCoverage{{ID: "FTA-1", Status: "accepted", TestRefs: []string{"TestFaultTree"}}},
			PostMortems:     []PostMortemCoverage{{ID: "PM-1", Status: "accepted", CatchingTestRefs: []string{"TestRegression"}}},
			FormalModels:    []FormalModelCoverage{{ID: "FORMAL-1", Status: "accepted", VerificationRefs: []string{"model.tla"}}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted risk/model coverage to fail")

		for _, want := range []string{
			"hazop_deviations[0] accepted coverage missing guide_word",
			"hazop_deviations[0] accepted coverage missing deviation",
			"hazop_deviations[0] accepted coverage missing workflow_step",
			"fault_tree_leaves[0] accepted coverage missing top_event",
			"fault_tree_leaves[0] accepted coverage missing cause",
			"post_mortems[0] accepted coverage missing incident",
			"formal_models[0] accepted coverage missing model",
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("BehavioralCoverageContractRejectsIncompleteAcceptedProductionReadinessDetails", func() {
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
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected incomplete accepted production-readiness coverage to fail")

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
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.FuzzTargetsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.FuzzTargetsCovered).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.RAGEvalsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.RAGEvalsMeasured).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.SecurityCasesTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.SecurityCasesTested).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.PrivacyControlsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.PrivacyControlsTested).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.ProviderContractsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.ProviderContractsTested).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.ObservabilityTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.ObservabilityTested).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.PerformanceBoundsTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.PerformanceBoundsTested).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.E2EJourneysTotal).To(gomega.Equal(1), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))
		gomega.Expect(coverage.E2EJourneysScripted).To(gomega.Equal(0), fmt.Sprintf("projection should preserve incomplete production-readiness counters, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractValidatesSurfaceInventoryDecisions", func() {
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
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("valid surface inventory should pass: %v", err))
		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(containsString(coverage.GatesPassed, "surface_inventory")).To(gomega.BeTrue(), fmt.Sprintf("surface inventory should pass projection gate: %+v", coverage))
		gomega.Expect(containsString(coverage.GatesBlocked, "surface_inventory")).To(gomega.BeFalse(), fmt.Sprintf("surface inventory should pass projection gate: %+v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsInScopeSurfaceWithoutObligation", func() {
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
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected in-scope surface without obligations to fail, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("in-scope surface missing tdd_obligations"), fmt.Sprintf("expected in-scope surface without obligations to fail, got %v", err))

	})
	ginkgo.It("BehavioralCoverageContractRejectsNonRequiredSurfaceWithoutRationale", func() {
		for _, status := range []string{"not_required", "waived"} {
			contract := BehavioralCoverageContract{
				SurfaceInventory: []BehavioralSurfaceCoverage{{
					ID:      "SURF-1",
					Surface: "api_transport",
					Status:  status,
				}},
			}

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected %s surface without rationale to fail, got %v", status, err))
			gomega.Expect(err.Error()).To(gomega.ContainSubstring(status+" surface missing rationale"), fmt.Sprintf("expected %s surface without rationale to fail, got %v", status, err))

		}
	})
	ginkgo.It("BehavioralCoverageContractRejectsDuplicateAndConflictingSurfaceDecisions", func() {
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
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected duplicate/conflicting surface decisions to fail")

		for _, want := range []string{"duplicates id", "conflicts for surface"} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected surface consistency error containing %q, got %v", want, err))

		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(!containsString(coverage.GatesBlocked, "surface_inventory")).To(gomega.BeFalse(), fmt.Sprintf("conflicting surface inventory should block projection readiness: %+v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsUnknownSurfaceStatus", func() {
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
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("expected status %q to fail", status))

		}
	})
	ginkgo.It("BehavioralCoverageContractOpenQuestionSurfaceBlocksProjectionReadiness", func() {
		contract := BehavioralCoverageContract{
			SurfaceInventory: []BehavioralSurfaceCoverage{{
				ID:           "SURF-API",
				Surface:      "api_transport",
				Status:       "open_question",
				Rationale:    "operator must choose CLI or HTTP boundary",
				QuestionRefs: []string{"q-api-boundary"},
			}},
		}
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("open_question should be valid structured coverage state: %v", err))
		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(!containsString(coverage.GatesBlocked, "surface_inventory")).To(gomega.BeFalse(), fmt.Sprintf("open_question should block readiness projection: %+v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractIgnoresWaivedAndNotRequiredProductionReadinessCounters", func() {
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
		{

			err := ValidateBehavioralCoverageContract(contract)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("waived/not_required production-readiness entries should validate without complete evidence, got %v", err))
		}

		coverage := ProjectBehavioralCoverageContract(contract)
		gomega.Expect(coverage.FuzzTargetsTotal).To(gomega.Equal(0), fmt.Sprintf("waived/not_required entries must not inflate coverage totals, got %#v", coverage))
		gomega.Expect(coverage.SecurityCasesTotal).To(gomega.Equal(0), fmt.Sprintf("waived/not_required entries must not inflate coverage totals, got %#v", coverage))
		gomega.Expect(coverage.PrivacyControlsTotal).To(gomega.Equal(0), fmt.Sprintf("waived/not_required entries must not inflate coverage totals, got %#v", coverage))

	})
	ginkgo.It("BehavioralCoverageContractRejectsInvalidState", func() {
		contract := BehavioralCoverageContract{
			BDDScenarios: []BDDScenarioCoverage{{ID: "BDD-1", Status: "planned", ConcreteDataRefs: []string{"fixture.json"}}},
		}

		err := ValidateBehavioralCoverageContract(contract)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected invalid status to fail")
		gomega.Expect(!strings.Contains(err.Error(), `invalid status "planned"`)).To(gomega.BeFalse(), fmt.Sprintf("expected invalid status error, got %v", err))

	})
	ginkgo.It("BehavioralCoverageContractRejectsAmbiguousRulesAndDanglingRefs", func() {
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
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected ambiguous and dangling behavioral coverage to fail")

		for _, want := range []string{
			"ambiguous statement language",
			`mapped_refs[0] references unknown coverage artifact or source ref "BDD-MISSING"`,
			"requires at least two concrete examples",
			`requirement_id references unknown coverage artifact "REQ-MISSING"`,
			`rule_id references unknown coverage artifact "RULE-MISSING"`,
			`references unknown obligation "TDD-MISSING"`,
		} {
			gomega.Expect(!strings.Contains(err.Error(), want)).To(gomega.BeFalse(), fmt.Sprintf("expected error containing %q, got %v", want, err))

		}
	})
	ginkgo.It("BehavioralCoverageContractRejectsRefsWhenAuthoritySetIsEmpty", func() {
		obligationRef := BehavioralCoverageContract{
			EARS: []EARSRequirementCoverage{{
				ID:          "REQ-1",
				Statement:   "When behavior is accepted, the system shall require owned test obligations.",
				Obligations: []string{"TDD-MISSING"},
			}},
		}
		err := ValidateBehavioralCoverageContract(obligationRef)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing obligation authority to fail, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`references unknown obligation "TDD-MISSING"`), fmt.Sprintf("expected missing obligation authority to fail, got %v", err))

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
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing requirement authority to fail, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`requirement_id references unknown coverage artifact "REQ-MISSING"`), fmt.Sprintf("expected missing requirement authority to fail, got %v", err))

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
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing rule authority to fail, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`rule_id references unknown coverage artifact "RULE-MISSING"`), fmt.Sprintf("expected missing rule authority to fail, got %v", err))

	})
	ginkgo.It("TDDLedgerValidatesFullBehavioralCoverageProjection", func() {
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
		{

			err := validateTDDLedger(ledger)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected complete full projection to pass, got %v", err))
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
			ginkgo.By(check.name, func() {
				mismatched := ledger
				check.mutate(&mismatched.Coverage)
				err := validateTDDLedger(mismatched)
				gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected mismatch for %s, got %v", check.want, err))
				gomega.Expect(err.Error()).To(gomega.ContainSubstring(check.want), fmt.Sprintf("expected mismatch for %s, got %v", check.want, err))

			})
		}
	})
	ginkgo.It("ProjectedBehavioralCoverageLedgerPassesCoverageGate", func() {
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
		ledger := validBehavioralCoverageLedger(contract, ProjectBehavioralCoverageContract(contract))
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
		gomega.Expect(CoverageGateHasBlockers(eval)).To(gomega.BeFalse(), fmt.Sprintf("projected accepted behavioral coverage should pass gate, got %#v", eval.Blockers))

	})
	ginkgo.It("BehavioralCoverageContractProjectsMutationRequirements", func() {
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
			ginkgo.By(tt.name, func() {
				contract := BehavioralCoverageContract{Mutation: tt.mutation}
				err := ValidateBehavioralCoverageContract(contract)
				if tt.wantErrSubstring == "" {
					gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected valid mutation contract, got %v", err))
				} else {
					gomega.Expect(err).To(gomega.HaveOccurred(), "expected mutation validation error")
					gomega.Expect(err.Error()).To(gomega.ContainSubstring(tt.wantErrSubstring), fmt.Sprintf("expected mutation error containing %q, got %v", tt.wantErrSubstring, err))
				}

				coverage := ProjectBehavioralCoverageContract(contract)
				gomega.Expect(coverage.MutationMinScore != tt.wantMinScore).To(gomega.BeFalse(), fmt.Sprintf("mutation_min_score = %d, want %d", coverage.MutationMinScore, tt.wantMinScore))

				if tt.wantScore != nil {
					gomega.Expect(coverage.MutationScore).NotTo(gomega.BeNil(), fmt.Sprintf("mutation_score = %v, want %v", coverage.MutationScore, *tt.wantScore))
					gomega.Expect(*coverage.MutationScore).To(gomega.Equal(*tt.wantScore), fmt.Sprintf("mutation_score = %v, want %v", coverage.MutationScore, *tt.wantScore))

				}
				if tt.wantBlockedToken == "" {
					gomega.Expect(coverage.GatesBlocked).To(gomega.BeEmpty(), fmt.Sprintf("expected no gates_blocked tokens, got %#v", coverage.GatesBlocked))
				} else {
					gomega.Expect(coverage.GatesBlocked).To(gomega.ContainElement(tt.wantBlockedToken), fmt.Sprintf("expected gates_blocked to contain %q, got %#v", tt.wantBlockedToken, coverage.GatesBlocked))
				}

			})
		}
	})
	ginkgo.It("TDDLedgerValidatesBehavioralCoverageProjectionWhenPresent", func() {
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
		{
			err := validateTDDLedger(ledger)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected projected behavioral coverage ledger to pass, got %v", err))
		}

		ledger.Coverage.BDDWithConcreteData = 0
		err := validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected mismatched behavioral coverage projection to fail")
		gomega.Expect(!strings.Contains(err.Error(), "bdd_with_concrete_data")).To(gomega.BeFalse(), fmt.Sprintf("expected BDD projection mismatch, got %v", err))

		ledger = validBehavioralCoverageLedger(contract, coverage)
		ledger.Coverage.EARSMapped = 0
		err = validateTDDLedger(ledger)
		gomega.Expect(err).To(gomega.HaveOccurred(), "expected mismatched EARS behavioral coverage projection to fail")
		gomega.Expect(!strings.Contains(err.Error(), "ears_mapped")).To(gomega.BeFalse(), fmt.Sprintf("expected EARS projection mismatch, got %v", err))

	})
	ginkgo.It("PrepareBehavioralCoveragePromotionProjectsLedgerAndMergesObligations", func() {
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

		result, err := prepareBehavioralCoveragePromotion(behavioralCoverageWorkflowState(ledger), ledger, contract)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prepareBehavioralCoveragePromotion: %v", err))
		gomega.Expect(result.State).To(gomega.Equal(BehavioralCoveragePromotionStatePrepared), fmt.Sprintf("expected prepared promotion result, got %#v", result))
		gomega.Expect(result.CommitStatus).To(gomega.Equal(BehavioralCoveragePromotionCommitStatusWritePending), fmt.Sprintf("expected accepted pending promotion result, got %#v", result))
		gomega.Expect(result.Digest).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected accepted pending promotion result, got %#v", result))

		promoted := result.Ledger
		gomega.Expect(promoted.BehavioralCoverage).NotTo(gomega.BeNil(), "expected behavioral coverage contract to be stamped into ledger")
		gomega.Expect(promoted.BehavioralCoverage.EffortID).To(gomega.Equal("effort-promote"), fmt.Sprintf("expected identity from ledger, got effort=%q task=%q", promoted.BehavioralCoverage.EffortID, promoted.BehavioralCoverage.TaskID))
		gomega.Expect(promoted.BehavioralCoverage.TaskID).To(gomega.Equal("task-promote"), fmt.Sprintf("expected identity from ledger, got effort=%q task=%q", promoted.BehavioralCoverage.EffortID, promoted.BehavioralCoverage.TaskID))
		gomega.Expect(promoted.Coverage.EARSTotal).To(gomega.Equal(1), fmt.Sprintf("expected projected coverage counters and contract gate, got %#v", promoted.Coverage))
		gomega.Expect(promoted.Coverage.BDDTotal).To(gomega.Equal(1), fmt.Sprintf("expected projected coverage counters and contract gate, got %#v", promoted.Coverage))
		gomega.Expect(containsString(promoted.Coverage.GatesPassed, "contract")).To(gomega.BeTrue(), fmt.Sprintf("expected projected coverage counters and contract gate, got %#v", promoted.Coverage))
		gomega.Expect(len(promoted.Obligations) != 2).To(gomega.BeFalse(), fmt.Sprintf("expected one existing and one promoted obligation, got %#v", promoted.Obligations))
		gomega.Expect(promoted.Obligations[0].ID).To(gomega.Equal("TDD-KEEP"), fmt.Sprintf("expected deterministic obligation merge, got %#v", promoted.Obligations))
		gomega.Expect(promoted.Obligations[1].ID).To(gomega.Equal("TDD-BDD"), fmt.Sprintf("expected deterministic obligation merge, got %#v", promoted.Obligations))
		gomega.Expect(promoted.Obligations[1].Command != "go test ./behavior").To(gomega.BeFalse(), fmt.Sprintf("expected promoted obligation to replace stale obligation, got %#v", promoted.Obligations[1]))

	})
	ginkgo.It("AcceptedBehavioralCoverageProjectionFiltersNonAcceptedItems", func() {
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
		baseLedger := TDDLedger{
			EffortID:     "effort-promote",
			TaskID:       "task-promote",
			CoverageTier: "behavior_standard",
		}
		result, err := prepareBehavioralCoveragePromotion(behavioralCoverageWorkflowState(baseLedger), baseLedger, contract)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prepareBehavioralCoveragePromotion: %v", err))

		ledger := result.Ledger
		digest := BehavioralCoverageContractDigest(*ledger.BehavioralCoverage)
		projection := ProjectAcceptedBehavioralCoverage(BriefGenerationInput{
			TDD:                ledger,
			PromotionEvents:    []BehavioralCoveragePromotionEvent{{Event: "behavioral_coverage_accepted", State: BehavioralCoveragePromotionStateAccepted, EventID: "evt-accepted", Digest: digest}},
			CoverageEvaluation: CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"},
		})
		ref := implementationHandoffCoverageContract(BriefGenerationInput{
			TDD:                ledger,
			PromotionEvents:    []BehavioralCoveragePromotionEvent{{Event: "behavioral_coverage_accepted", State: BehavioralCoveragePromotionStateAccepted, EventID: "evt-accepted", Digest: digest}},
			CoverageEvaluation: CoverageGateEvaluation{Status: ProductWorkflowStatusPass, CoverageTier: "behavior_standard"},
		})
		gomega.Expect(projection).NotTo(gomega.BeNil(), fmt.Sprintf("expected accepted projection, ref=%#v validate=%v projection=%v", ref, ValidateBehavioralCoverageContract(contract), ValidateBehavioralCoverageProjection(contract, ledger.Coverage)))
		gomega.Expect(projection.Behavioral.BDDScenarios).To(gomega.HaveLen(1), fmt.Sprintf("expected only accepted scenario in projection, got %#v", projection.Behavioral.BDDScenarios))
		gomega.Expect(projection.Behavioral.BDDScenarios[0].ID).To(gomega.Equal("BDD-BDD"), fmt.Sprintf("expected only accepted scenario in projection, got %#v", projection.Behavioral.BDDScenarios))
		gomega.Expect(projection.Projected.BDDTotal != 1).To(gomega.BeFalse(), fmt.Sprintf("expected projected accepted-only counters, got %#v", projection.Projected))
		gomega.Expect(projection.Behavioral.ExampleRules).To(gomega.HaveLen(1), fmt.Sprintf("expected only accepted-requirement rules in projection, got %#v", projection.Behavioral.ExampleRules))
		gomega.Expect(projection.Behavioral.ExampleRules[0].ID).To(gomega.Equal("RULE-BDD"), fmt.Sprintf("expected only accepted-requirement rules in projection, got %#v", projection.Behavioral.ExampleRules))
		gomega.Expect(projection.Behavioral.TestObligations).To(gomega.HaveLen(1), fmt.Sprintf("expected only accepted-obligation tests in projection, got %#v", projection.Behavioral.TestObligations))
		gomega.Expect(projection.Behavioral.TestObligations[0].ID).To(gomega.Equal("TDD-BDD"), fmt.Sprintf("expected only accepted-obligation tests in projection, got %#v", projection.Behavioral.TestObligations))
		gomega.Expect(projection.Behavioral.Verification).To(gomega.HaveLen(1), fmt.Sprintf("expected only accepted-obligation verification in projection, got %#v", projection.Behavioral.Verification))
		gomega.Expect(projection.Behavioral.Verification[0].ID).To(gomega.Equal("VERIFY-ACCEPTED"), fmt.Sprintf("expected only accepted-obligation verification in projection, got %#v", projection.Behavioral.Verification))
		gomega.Expect(len(projection.Behavioral.SurfaceInventory) != 3).To(gomega.BeFalse(), fmt.Sprintf("expected closed surface decisions to remain in accepted projection, got %#v", projection.Behavioral.SurfaceInventory))
		gomega.Expect(projection.Behavioral.SurfaceInventory[0].ID).To(gomega.Equal("SURF-BDD-DOMAIN"), fmt.Sprintf("accepted projection should preserve closed surface decisions, got %#v", projection.Behavioral.SurfaceInventory))
		gomega.Expect(projection.Behavioral.SurfaceInventory[1].Status).To(gomega.Equal(BehavioralCoverageStatusNotRequired), fmt.Sprintf("accepted projection should preserve closed surface decisions, got %#v", projection.Behavioral.SurfaceInventory))
		gomega.Expect(projection.Behavioral.SurfaceInventory[2].Status).To(gomega.Equal(BehavioralCoverageStatusWaived), fmt.Sprintf("accepted projection should preserve closed surface decisions, got %#v", projection.Behavioral.SurfaceInventory))

	})
	ginkgo.It("BehavioralCoveragePromotionPreviewStatesAndSupersededDigest", func() {
		first := promotableBehavioralCoverageContract()
		ledger := TDDLedger{EffortID: "effort-promote", TaskID: "task-promote", CoverageTier: "behavior_standard", Coverage: TDDCoverage{MutationMinScore: 80}}
		state := behavioralCoverageWorkflowState(ledger)
		reviewed, err := reviewBehavioralCoverageContractWithResult(state, ledger, first)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("reviewBehavioralCoverageContractWithResult: %v", err))
		gomega.Expect(reviewed.State).To(gomega.Equal(BehavioralCoveragePromotionStateReviewed), fmt.Sprintf("expected reviewed lifecycle result, got %#v", reviewed))
		gomega.Expect(reviewed.EventID).To(gomega.BeEmpty(), fmt.Sprintf("pure review must not mint commit provenance, got %#v", reviewed))
		gomega.Expect(reviewed.Digest).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected reviewed lifecycle result, got %#v", reviewed))

		prepared, err := prepareBehavioralCoveragePromotion(state, ledger, first)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("prepareBehavioralCoveragePromotion: %v", err))
		gomega.Expect(prepared.State).To(gomega.Equal(BehavioralCoveragePromotionStatePrepared))

		second := promotableBehavioralCoverageContract()
		second.ContractID = "cov-promote-2"
		second.BDDScenarios[0].ID = "BDD-BDD-2"
		second.EARS[0].MappedRefs = []string{"BDD-BDD-2"}
		second.ExampleRules[0].ID = "RULE-BDD-2"
		second.BDDScenarios[0].RuleID = "RULE-BDD-2"
		superseding, err := prepareBehavioralCoveragePromotion(state, prepared.Ledger, second)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("superseding prepare: %v", err))
		gomega.Expect(superseding.State).To(gomega.Equal(BehavioralCoveragePromotionStatePrepared), fmt.Sprintf("expected prepared superseding result, got %#v previous=%s", superseding, prepared.Digest))
		gomega.Expect(superseding.SupersededDigest).To(gomega.Equal(prepared.Digest), fmt.Sprintf("expected prepared superseding result, got %#v previous=%s", superseding, prepared.Digest))

	})
	ginkgo.It("PromoteBehavioralCoverageFromEffortWritesOnlyCommittedAcceptedEvent", func() {
		dir := ginkgo.GinkgoT().TempDir()
		contract := promotableBehavioralCoverageContract()
		mutationScore := 80.0
		contract.Mutation = BehavioralMutationCoverage{
			Requirement: BehavioralMutationRequirementRequired,
			MinScore:    80,
			Score:       &mutationScore,
		}
		contract.TestObligations[0].Type = "contract"
		ledger := TDDLedger{
			SchemaVersion: "idiom.tdd_obligations.v2",
			EffortID:      "effort-promote",
			TaskID:        "effort-promote",
			CoverageTier:  "unit",
			Coverage:      TDDCoverage{MutationMinScore: 80},
			Obligations: []TDDObligation{{
				ID:             "TDD-BASE",
				Type:           "contract",
				Behavior:       "preserve the existing product-workflow obligation",
				Owner:          "productworkflow",
				Command:        "go test ./internal/workflows/productworkflow",
				RequiredBefore: "implementation",
				Status:         "pending",
			}},
		}

		contract.CoverageTier = "unit"
		writeBehavioralCoveragePromotionEffort(dir, ledger, contract)

		review := PromoteBehavioralCoverageFromEffort(BehavioralCoveragePromotionRequest{
			EffortRoot:   dir,
			ContractPath: "behavioral-coverage.json",
			Write:        true,
		})
		gomega.Expect(review.Status).To(gomega.Equal(ProductWorkflowStatusPass), "findings=%+v", review.Findings)
		result := review.Promotion
		gomega.Expect(result.State).To(gomega.Equal(BehavioralCoveragePromotionStateAccepted), fmt.Sprintf("expected committed accepted promotion result, got %#v", result))
		gomega.Expect(result.CommitStatus).To(gomega.Equal(BehavioralCoveragePromotionCommitStatusCommitted), fmt.Sprintf("expected committed accepted promotion result, got %#v", result))
		gomega.Expect(result.LedgerCommitted).To(gomega.BeTrue(), fmt.Sprintf("expected committed accepted promotion result, got %#v", result))
		gomega.Expect(result.EventID).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected committed accepted promotion result, got %#v", result))
		gomega.Expect(result.Digest).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected committed accepted promotion result, got %#v", result))

		ledgerData, err := os.ReadFile(filepath.Join(dir, "tdd.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))

		var written TDDLedger
		{
			err := json.Unmarshal(ledgerData, &written)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("written tdd.json should decode: %v\n%s", err, string(ledgerData)))
		}
		gomega.Expect(written.BehavioralCoverage).NotTo(gomega.BeNil(), fmt.Sprintf("written ledger should contain committed behavioral coverage, got %#v", written.BehavioralCoverage))
		gomega.Expect(BehavioralCoverageContractDigest(*written.BehavioralCoverage)).To(gomega.Equal(result.Digest), fmt.Sprintf("written ledger should contain committed behavioral coverage, got %#v", written.BehavioralCoverage))

		eventsData, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprint(err))

		events, err := ParseBehavioralCoveragePromotionEventsJSONL(eventsData)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("events should parse: %v\n%s", err, string(eventsData)))
		gomega.Expect(events).To(gomega.HaveLen(1), fmt.Sprintf("expected only the transaction's committed event, got %#v", events))
		gomega.Expect(events[0].State).To(gomega.Equal(BehavioralCoveragePromotionStateAccepted), fmt.Sprintf("event should be committed accepted, got %#v", events[0]))
		gomega.Expect(events[0].CommitStatus).To(gomega.Equal(BehavioralCoveragePromotionCommitStatusCommitted), fmt.Sprintf("event should be committed accepted, got %#v", events[0]))
		{

			accepted := AcceptedBehavioralCoveragePromotionEvent(events, result.Digest)
			gomega.Expect(accepted).NotTo(gomega.BeNil(), fmt.Sprintf("accepted event lookup should return the committed accepted event, got %#v", accepted))
			gomega.Expect(accepted.EventID).To(gomega.Equal(result.EventID), fmt.Sprintf("accepted event lookup should return the committed accepted event, got %#v", accepted))
		}

	})
})

func behavioralCoverageWorkflowState(ledger TDDLedger) WorkflowState {
	return WorkflowState{
		EffortID: ledger.EffortID,
		TaskID:   ledger.TaskID,
		Coverage: CoverageState{CoverageTier: ledger.CoverageTier},
	}
}

func writeBehavioralCoveragePromotionEffort(
	root string,
	ledger TDDLedger,
	contract BehavioralCoverageContract,
) {
	ginkgo.GinkgoHelper()

	_, err := InitializeEffort(CreateEffortOptions{EffortRoot: root, EffortID: ledger.EffortID})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	advanceTestEffortToADRDrafting(root)

	adr := validADRDoc()
	entry := ADRIndexEntry{
		ADRID:        adr.FrontMatter.ADRID,
		Title:        "Behavioral coverage promotion",
		Status:       string(ArtifactStateDraft),
		Path:         "ADR-001.md",
		CoverageTier: adr.FrontMatter.CoverageTier,
		PRDRefs:      append([]string(nil), adr.FrontMatter.PRDRefs...),
		CreatedAt:    "2026-06-17T00:00:00Z",
	}
	persisted := persistInitialTestADRDrafts(root, []ADRDocument{adr}, []ADRIndexEntry{entry})
	gomega.Expect(persisted.Documents).To(gomega.HaveLen(1))
	gomega.Expect(persisted.IndexEntries).To(gomega.HaveLen(1))

	result, err := SubmitADRsForReview(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhaseADRReview))
	result, intakes, err := ApproveADRGate(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.CurrentPhase).To(gomega.Equal(PhaseTDDSynthesis))
	for _, intake := range intakes {
		gomega.Expect(intake.Passed).To(gomega.BeTrue(), "failures=%+v", intake.Failures)
	}

	lease, err := openProductWorkflowMutationLease(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	generation, _, workflowState, err := inspectTDDGeneration(lease.root)
	lease.Close()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(workflowState.EffortID).To(gomega.Equal(ledger.EffortID))
	gomega.Expect(workflowState.TaskID).To(gomega.Equal(ledger.TaskID))

	ledgerBody, err := json.MarshalIndent(ledger, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(commitSynthesizedTDDLedger(root, generation, append(ledgerBody, '\n'))).To(gomega.Succeed())

	contractBody, err := json.MarshalIndent(contract, "", "  ")
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(writeEffortArtifactAtomic(root, "behavioral-coverage.json", append(contractBody, '\n'), 0o644)).To(gomega.Succeed())
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
			TestRefs:       []string{"PrepareBehavioralCoveragePromotionProjectsLedgerAndMergesObligations"},
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
