package productworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	BehavioralCoverageStatusAccepted    = "accepted"
	BehavioralCoverageStatusWaived      = "waived"
	BehavioralCoverageStatusNotRequired = "not_required"

	BehavioralCoveragePromotionStateProposed   = "proposed"
	BehavioralCoveragePromotionStatePrepared   = "prepared"
	BehavioralCoveragePromotionStateReviewed   = "reviewed"
	BehavioralCoveragePromotionStateAccepted   = "accepted"
	BehavioralCoveragePromotionStateRejected   = "rejected"
	BehavioralCoveragePromotionStateSuperseded = "superseded"

	BehavioralCoveragePromotionCommitStatusNotWritten   = "not_written"
	BehavioralCoveragePromotionCommitStatusWritePending = "write_pending"
	BehavioralCoveragePromotionCommitStatusPrepared     = "prepared"
	BehavioralCoveragePromotionCommitStatusCommitted    = "committed"
	BehavioralCoveragePromotionCommitStatusBlocked      = "blocked"

	BehavioralCoveragePromotionAuditStatusNotAttempted = "not_attempted"
	BehavioralCoveragePromotionAuditStatusRecorded     = "recorded"
	BehavioralCoveragePromotionAuditStatusDegraded     = "degraded"

	BehavioralMutationRequirementRequired    = "required"
	BehavioralMutationRequirementWaived      = "waived"
	BehavioralMutationRequirementNotRequired = "not_required"
)

type AcceptedBehavioralCoverageProjection struct {
	Contract   ImplementationHandoffCoverageContract
	Behavioral BehavioralCoverageContract
	Projected  TDDCoverage
	Status     string
	Authority  string
}

type BehavioralCoveragePromotionResult struct {
	State            string
	CommitStatus     string
	LedgerCommitted  bool
	CommitWarning    string
	AuditStatus      string
	AuditError       string
	EventID          string
	Digest           string
	SupersededDigest string
	Ledger           TDDLedger
}

type BehavioralCoverageContract struct {
	SchemaVersion     string                       `json:"schema_version,omitempty"`
	ContractID        string                       `json:"contract_id,omitempty"`
	EffortID          string                       `json:"effort_id,omitempty"`
	TaskID            string                       `json:"task_id,omitempty"`
	CoverageTier      string                       `json:"coverage_tier,omitempty"`
	EventID           string                       `json:"event_id,omitempty"`
	PromotionState    string                       `json:"promotion_state,omitempty"`
	SourceRefs        []string                     `json:"source_refs,omitempty"`
	RiskClass         string                       `json:"risk_class,omitempty"`
	EARS              []EARSRequirementCoverage    `json:"ears,omitempty"`
	ExampleRules      []ExampleRuleCoverage        `json:"example_rules,omitempty"`
	BDDScenarios      []BDDScenarioCoverage        `json:"bdd_scenarios,omitempty"`
	DesignContracts   []DesignContractCoverage     `json:"design_contracts,omitempty"`
	Invariants        []InvariantCoverage          `json:"invariants,omitempty"`
	StateMachines     []StateMachineCoverage       `json:"state_machines,omitempty"`
	FailureModes      []FailureModeCoverage        `json:"failure_modes,omitempty"`
	HAZOPDeviations   []HAZOPDeviationCoverage     `json:"hazop_deviations,omitempty"`
	FaultTreeLeaves   []FaultTreeLeafCoverage      `json:"fault_tree_leaves,omitempty"`
	DecisionTables    []DecisionTableCoverage      `json:"decision_tables,omitempty"`
	BoundaryValues    []BoundaryValueCoverage      `json:"boundary_values,omitempty"`
	PostMortems       []PostMortemCoverage         `json:"post_mortems,omitempty"`
	FormalModels      []FormalModelCoverage        `json:"formal_models,omitempty"`
	FuzzTargets       []FuzzTargetCoverage         `json:"fuzz_targets,omitempty"`
	RAGEvals          []RAGEvaluationCoverage      `json:"rag_evals,omitempty"`
	SecurityCases     []SecurityCaseCoverage       `json:"security_cases,omitempty"`
	PrivacyControls   []PrivacyControlCoverage     `json:"privacy_controls,omitempty"`
	ProviderContracts []ProviderContractCoverage   `json:"provider_contracts,omitempty"`
	Observability     []ObservabilityCoverage      `json:"observability,omitempty"`
	PerformanceBounds []PerformanceBoundCoverage   `json:"performance_bounds,omitempty"`
	E2EJourneys       []E2EJourneyCoverage         `json:"e2e_journeys,omitempty"`
	SurfaceInventory  []BehavioralSurfaceCoverage  `json:"surface_inventory,omitempty"`
	TestObligations   []TestObligationCoverage     `json:"test_obligations,omitempty"`
	Verification      []VerificationGateCoverage   `json:"verification,omitempty"`
	Mutation          BehavioralMutationCoverage   `json:"mutation,omitempty"`
	Feedback          []BehavioralFeedbackCoverage `json:"feedback,omitempty"`
	Waivers           []BehavioralCoverageWaiver   `json:"waivers,omitempty"`
	OpenQuestions     []BehavioralCoverageQuestion `json:"open_questions,omitempty"`
}

type EARSRequirementCoverage struct {
	ID          string   `json:"id"`
	Pattern     string   `json:"pattern,omitempty"`
	Statement   string   `json:"statement"`
	Status      string   `json:"status,omitempty"`
	MappedRefs  []string `json:"mapped_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type ExampleRuleCoverage struct {
	ID            string                    `json:"id"`
	RequirementID string                    `json:"requirement_id,omitempty"`
	Rule          string                    `json:"rule"`
	Examples      []ConcreteExampleCoverage `json:"examples,omitempty"`
	Questions     []string                  `json:"questions,omitempty"`
}

type ConcreteExampleCoverage struct {
	ID               string   `json:"id"`
	Description      string   `json:"description,omitempty"`
	ConcreteDataRefs []string `json:"concrete_data_refs,omitempty"`
	Negative         bool     `json:"negative,omitempty"`
}

type BDDScenarioCoverage struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	RequirementID    string   `json:"requirement_id,omitempty"`
	RuleID           string   `json:"rule_id,omitempty"`
	Given            []string `json:"given,omitempty"`
	When             []string `json:"when,omitempty"`
	Then             []string `json:"then,omitempty"`
	ConcreteDataRefs []string `json:"concrete_data_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type DesignContractCoverage struct {
	ID             string   `json:"id"`
	Status         string   `json:"status,omitempty"`
	Boundary       string   `json:"boundary,omitempty"`
	Preconditions  []string `json:"preconditions,omitempty"`
	Postconditions []string `json:"postconditions,omitempty"`
	Invariants     []string `json:"invariants,omitempty"`
	TestRefs       []string `json:"test_refs,omitempty"`
	Obligations    []string `json:"tdd_obligations,omitempty"`
}

type InvariantCoverage struct {
	ID                    string   `json:"id"`
	Status                string   `json:"status,omitempty"`
	Statement             string   `json:"statement,omitempty"`
	PropertyBasedTestRefs []string `json:"property_based_test_refs,omitempty"`
	Obligations           []string `json:"tdd_obligations,omitempty"`
}

type StateMachineCoverage struct {
	ID          string                    `json:"id"`
	Status      string                    `json:"status,omitempty"`
	States      []string                  `json:"states,omitempty"`
	Transitions []StateTransitionCoverage `json:"transitions,omitempty"`
}

type StateTransitionCoverage struct {
	ID       string   `json:"id"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Event    string   `json:"event,omitempty"`
	TestRefs []string `json:"test_refs,omitempty"`
}

type FailureModeCoverage struct {
	ID                 string   `json:"id"`
	Status             string   `json:"status,omitempty"`
	Mode               string   `json:"mode,omitempty"`
	Mitigation         string   `json:"mitigation,omitempty"`
	RPN                int      `json:"rpn,omitempty"`
	MitigationTestRefs []string `json:"mitigation_test_refs,omitempty"`
	Obligations        []string `json:"tdd_obligations,omitempty"`
}

type HAZOPDeviationCoverage struct {
	ID           string   `json:"id"`
	Status       string   `json:"status,omitempty"`
	GuideWord    string   `json:"guide_word,omitempty"`
	WorkflowStep string   `json:"workflow_step,omitempty"`
	Deviation    string   `json:"deviation,omitempty"`
	Severity     int      `json:"severity,omitempty"`
	TestRefs     []string `json:"test_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
}

type FaultTreeLeafCoverage struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	TopEvent    string   `json:"top_event,omitempty"`
	Cause       string   `json:"cause,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type DecisionTableCoverage struct {
	ID          string                      `json:"id"`
	Status      string                      `json:"status,omitempty"`
	Conditions  []string                    `json:"conditions,omitempty"`
	Rules       []DecisionTableRuleCoverage `json:"rules,omitempty"`
	TestRefs    []string                    `json:"test_refs,omitempty"`
	Obligations []string                    `json:"tdd_obligations,omitempty"`
}

type DecisionTableRuleCoverage struct {
	ID         string            `json:"id,omitempty"`
	Conditions map[string]string `json:"conditions,omitempty"`
	Expected   string            `json:"expected,omitempty"`
}

type BoundaryValueCoverage struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Parameter   string   `json:"parameter,omitempty"`
	Classes     []string `json:"classes,omitempty"`
	Values      []string `json:"values,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type PostMortemCoverage struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	Incident         string   `json:"incident,omitempty"`
	CatchingTestRefs []string `json:"catching_test_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type FormalModelCoverage struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	Model            string   `json:"model,omitempty"`
	VerificationRefs []string `json:"verification_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type FuzzTargetCoverage struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Target      string   `json:"target,omitempty"`
	Generators  []string `json:"generators,omitempty"`
	Invariants  []string `json:"invariants,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type RAGEvaluationCoverage struct {
	ID          string             `json:"id"`
	Status      string             `json:"status,omitempty"`
	DatasetRefs []string           `json:"dataset_refs,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	Thresholds  map[string]float64 `json:"thresholds,omitempty"`
	EvalRefs    []string           `json:"eval_refs,omitempty"`
	Obligations []string           `json:"tdd_obligations,omitempty"`
}

type SecurityCaseCoverage struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Attack      string   `json:"attack,omitempty"`
	Expected    string   `json:"expected,omitempty"`
	TestRefs    []string `json:"test_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type PrivacyControlCoverage struct {
	ID            string   `json:"id"`
	Status        string   `json:"status,omitempty"`
	DataClass     string   `json:"data_class,omitempty"`
	Policy        string   `json:"policy,omitempty"`
	RedactionRefs []string `json:"redaction_refs,omitempty"`
	Retention     string   `json:"retention,omitempty"`
	TestRefs      []string `json:"test_refs,omitempty"`
	Obligations   []string `json:"tdd_obligations,omitempty"`
}

type ProviderContractCoverage struct {
	ID           string   `json:"id"`
	Status       string   `json:"status,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Consumer     string   `json:"consumer,omitempty"`
	ContractRefs []string `json:"contract_refs,omitempty"`
	TestRefs     []string `json:"test_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
}

type ObservabilityCoverage struct {
	ID               string   `json:"id"`
	Status           string   `json:"status,omitempty"`
	Signal           string   `json:"signal,omitempty"`
	InstrumentedPath string   `json:"instrumented_path,omitempty"`
	RequiredFields   []string `json:"required_fields,omitempty"`
	TestRefs         []string `json:"test_refs,omitempty"`
	Obligations      []string `json:"tdd_obligations,omitempty"`
}

type PerformanceBoundCoverage struct {
	ID              string   `json:"id"`
	Status          string   `json:"status,omitempty"`
	Operation       string   `json:"operation,omitempty"`
	Bound           string   `json:"bound,omitempty"`
	MeasurementRefs []string `json:"measurement_refs,omitempty"`
	Obligations     []string `json:"tdd_obligations,omitempty"`
}

type E2EJourneyCoverage struct {
	ID          string   `json:"id"`
	Status      string   `json:"status,omitempty"`
	Journey     string   `json:"journey,omitempty"`
	Steps       []string `json:"steps,omitempty"`
	ScriptRefs  []string `json:"script_refs,omitempty"`
	Obligations []string `json:"tdd_obligations,omitempty"`
}

type BehavioralSurfaceCoverage struct {
	ID           string   `json:"id"`
	Surface      string   `json:"surface"`
	Status       string   `json:"status"`
	Rationale    string   `json:"rationale,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Obligations  []string `json:"tdd_obligations,omitempty"`
	QuestionRefs []string `json:"question_refs,omitempty"`
}

type TestObligationCoverage struct {
	ID             string `json:"id"`
	Type           string `json:"type,omitempty"`
	Behavior       string `json:"behavior"`
	Owner          string `json:"owner,omitempty"`
	Command        string `json:"command"`
	RequiredBefore string `json:"required_before,omitempty"`
}

type VerificationGateCoverage struct {
	ID          string   `json:"id"`
	Gate        string   `json:"gate"`
	Command     string   `json:"command,omitempty"`
	Obligations []string `json:"obligations,omitempty"`
}

type BehavioralMutationCoverage struct {
	Requirement  string   `json:"requirement,omitempty"`
	MinScore     int      `json:"min_score,omitempty"`
	Score        *float64 `json:"score,omitempty"`
	WaiverReason string   `json:"waiver_reason,omitempty"`
}

type BehavioralCoverageWaiver struct {
	ID        string `json:"id"`
	Technique string `json:"technique"`
	Reason    string `json:"reason"`
}

type BehavioralFeedbackCoverage struct {
	ID             string   `json:"id"`
	Outcome        string   `json:"outcome,omitempty"`
	ObligationRefs []string `json:"obligation_refs,omitempty"`
}

type BehavioralCoverageQuestion struct {
	ID         string `json:"id"`
	Question   string `json:"question"`
	Status     string `json:"status,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

func ValidateBehavioralCoverageContract(contract BehavioralCoverageContract) error {
	var errs []error
	errs = append(errs, validateBehavioralCoverageIdentifierUniqueness(contract)...)
	switch strings.TrimSpace(contract.PromotionState) {
	case "":
	case BehavioralCoveragePromotionStateAccepted:
		if strings.TrimSpace(contract.EventID) == "" {
			errs = append(errs, errors.New("accepted behavioral coverage promotion is missing event_id"))
		}
	default:
		errs = append(errs, fmt.Errorf("behavioral coverage promotion_state %q is unsupported", contract.PromotionState))
	}
	obligationIDs := behavioralCoverageObligationIDs(contract.TestObligations)
	coverageIDs := behavioralCoverageArtifactIDs(contract)
	requirementIDs := behavioralCoverageEARSRequirementIDs(contract.EARS)
	ruleIDs := behavioralCoverageExampleRuleIDs(contract.ExampleRules)

	for i, requirement := range contract.EARS {
		prefix := fmt.Sprintf("behavioral coverage ears[%d]", i)
		errs = append(errs, validateEARSRequirementCoverage(prefix, requirement)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, requirement.Obligations, obligationIDs)...)
		errs = append(errs, validateBehavioralCoverageMappedRefs(prefix, requirement.MappedRefs, coverageIDs)...)
	}

	for i, rule := range contract.ExampleRules {
		prefix := fmt.Sprintf("behavioral coverage example_rules[%d]", i)
		errs = append(errs, validateExampleRuleCoverage(prefix, rule)...)
		errs = append(errs, validateKnownBehavioralCoverageRef(prefix, "requirement_id", rule.RequirementID, requirementIDs)...)
	}

	for i, scenario := range contract.BDDScenarios {
		prefix := fmt.Sprintf("behavioral coverage bdd_scenarios[%d]", i)
		errs = append(errs, validateBDDScenarioCoverage(prefix, scenario)...)
		errs = append(errs, validateKnownBehavioralCoverageRef(prefix, "requirement_id", scenario.RequirementID, requirementIDs)...)
		errs = append(errs, validateKnownBehavioralCoverageRef(prefix, "rule_id", scenario.RuleID, ruleIDs)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, scenario.Obligations, obligationIDs)...)
	}

	for i, coverage := range contract.DesignContracts {
		prefix := fmt.Sprintf("behavioral coverage design_contracts[%d]", i)
		errs = append(errs, validateDesignContractCoverage(prefix, coverage)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, coverage.Obligations, obligationIDs)...)
	}

	for i, invariant := range contract.Invariants {
		prefix := fmt.Sprintf("behavioral coverage invariants[%d]", i)
		errs = append(errs, validateBehavioralCoverageItem(prefix, invariant.ID, invariant.Status, len(nonEmptyStrings(invariant.PropertyBasedTestRefs)) > 0, "missing property_based_test_refs")...)
		if normalizeBehavioralCoverageStatus(invariant.Status) == BehavioralCoverageStatusAccepted && strings.TrimSpace(invariant.Statement) == "" {
			errs = append(errs, fmt.Errorf("%s accepted coverage missing statement", prefix))
		}
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, invariant.Obligations, obligationIDs)...)
	}

	for i, machine := range contract.StateMachines {
		prefix := fmt.Sprintf("behavioral coverage state_machines[%d]", i)
		errs = append(errs, validateStateMachineCoverage(prefix, machine)...)
	}

	for i, mode := range contract.FailureModes {
		prefix := fmt.Sprintf("behavioral coverage failure_modes[%d]", i)
		errs = append(errs, validateBehavioralCoverageItem(prefix, mode.ID, mode.Status, len(nonEmptyStrings(mode.MitigationTestRefs)) > 0, "missing mitigation_test_refs")...)
		if normalizeBehavioralCoverageStatus(mode.Status) == BehavioralCoverageStatusAccepted {
			if strings.TrimSpace(mode.Mode) == "" {
				errs = append(errs, fmt.Errorf("%s accepted coverage missing mode", prefix))
			}
			if strings.TrimSpace(mode.Mitigation) == "" {
				errs = append(errs, fmt.Errorf("%s accepted coverage missing mitigation", prefix))
			}
			if mode.RPN <= 0 {
				errs = append(errs, fmt.Errorf("%s accepted coverage missing positive rpn", prefix))
			}
		}
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, mode.Obligations, obligationIDs)...)
	}

	for i, deviation := range contract.HAZOPDeviations {
		prefix := fmt.Sprintf("behavioral coverage hazop_deviations[%d]", i)
		errs = append(errs, validateHAZOPDeviationCoverage(prefix, deviation)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, deviation.Obligations, obligationIDs)...)
	}

	for i, leaf := range contract.FaultTreeLeaves {
		prefix := fmt.Sprintf("behavioral coverage fault_tree_leaves[%d]", i)
		errs = append(errs, validateFaultTreeLeafCoverage(prefix, leaf)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, leaf.Obligations, obligationIDs)...)
	}

	for i, table := range contract.DecisionTables {
		prefix := fmt.Sprintf("behavioral coverage decision_tables[%d]", i)
		errs = append(errs, validateDecisionTableCoverage(prefix, table)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, table.Obligations, obligationIDs)...)
	}

	for i, boundary := range contract.BoundaryValues {
		prefix := fmt.Sprintf("behavioral coverage boundary_values[%d]", i)
		errs = append(errs, validateBoundaryValueCoverage(prefix, boundary)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, boundary.Obligations, obligationIDs)...)
	}

	for i, incident := range contract.PostMortems {
		prefix := fmt.Sprintf("behavioral coverage post_mortems[%d]", i)
		errs = append(errs, validatePostMortemCoverage(prefix, incident)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, incident.Obligations, obligationIDs)...)
	}

	for i, model := range contract.FormalModels {
		prefix := fmt.Sprintf("behavioral coverage formal_models[%d]", i)
		errs = append(errs, validateFormalModelCoverage(prefix, model)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, model.Obligations, obligationIDs)...)
	}

	for i, target := range contract.FuzzTargets {
		prefix := fmt.Sprintf("behavioral coverage fuzz_targets[%d]", i)
		errs = append(errs, validateFuzzTargetCoverage(prefix, target)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, target.Obligations, obligationIDs)...)
	}

	for i, eval := range contract.RAGEvals {
		prefix := fmt.Sprintf("behavioral coverage rag_evals[%d]", i)
		errs = append(errs, validateRAGEvaluationCoverage(prefix, eval)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, eval.Obligations, obligationIDs)...)
	}

	for i, securityCase := range contract.SecurityCases {
		prefix := fmt.Sprintf("behavioral coverage security_cases[%d]", i)
		errs = append(errs, validateSecurityCaseCoverage(prefix, securityCase)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, securityCase.Obligations, obligationIDs)...)
	}

	for i, privacy := range contract.PrivacyControls {
		prefix := fmt.Sprintf("behavioral coverage privacy_controls[%d]", i)
		errs = append(errs, validatePrivacyControlCoverage(prefix, privacy)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, privacy.Obligations, obligationIDs)...)
	}

	for i, provider := range contract.ProviderContracts {
		prefix := fmt.Sprintf("behavioral coverage provider_contracts[%d]", i)
		errs = append(errs, validateProviderContractCoverage(prefix, provider)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, provider.Obligations, obligationIDs)...)
	}

	for i, signal := range contract.Observability {
		prefix := fmt.Sprintf("behavioral coverage observability[%d]", i)
		errs = append(errs, validateObservabilityCoverage(prefix, signal)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, signal.Obligations, obligationIDs)...)
	}

	for i, bound := range contract.PerformanceBounds {
		prefix := fmt.Sprintf("behavioral coverage performance_bounds[%d]", i)
		errs = append(errs, validatePerformanceBoundCoverage(prefix, bound)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, bound.Obligations, obligationIDs)...)
	}

	for i, journey := range contract.E2EJourneys {
		prefix := fmt.Sprintf("behavioral coverage e2e_journeys[%d]", i)
		errs = append(errs, validateE2EJourneyCoverage(prefix, journey)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, journey.Obligations, obligationIDs)...)
	}
	errs = append(errs, validateBehavioralSurfaceInventoryConsistency(contract.SurfaceInventory)...)
	for i, surface := range contract.SurfaceInventory {
		prefix := fmt.Sprintf("behavioral coverage surface_inventory[%d]", i)
		errs = append(errs, validateBehavioralSurfaceCoverage(prefix, surface)...)
		errs = append(errs, validateKnownBehavioralObligationRefs(prefix, surface.Obligations, obligationIDs)...)
	}

	for i, obligation := range contract.TestObligations {
		prefix := fmt.Sprintf("behavioral coverage test_obligations[%d]", i)
		if strings.TrimSpace(obligation.ID) == "" {
			errs = append(errs, fmt.Errorf("%s missing id", prefix))
		}
		if strings.TrimSpace(obligation.Behavior) == "" {
			errs = append(errs, fmt.Errorf("%s missing behavior", prefix))
		}
		if strings.TrimSpace(obligation.Command) == "" {
			errs = append(errs, fmt.Errorf("%s missing command", prefix))
		}
	}

	errs = append(errs, validateBehavioralMutationCoverage(contract.Mutation)...)
	for i, question := range contract.OpenQuestions {
		prefix := fmt.Sprintf("behavioral coverage open_questions[%d]", i)
		errs = append(errs, validateBehavioralCoverageQuestion(prefix, question)...)
	}

	return errors.Join(errs...)
}

func ProjectBehavioralCoverageContract(contract BehavioralCoverageContract) TDDCoverage {
	coverage := TDDCoverage{}

	for _, requirement := range contract.EARS {
		if !BehavioralCoverageStatusIsAccepted(requirement.Status) {
			continue
		}
		coverage.EARSTotal++
		if len(nonEmptyStrings(requirement.MappedRefs)) > 0 || len(nonEmptyStrings(requirement.Obligations)) > 0 || bddScenarioCoversRequirement(contract.BDDScenarios, requirement.ID) {
			coverage.EARSMapped++
		}
	}

	for _, scenario := range contract.BDDScenarios {
		if !BehavioralCoverageStatusIsAccepted(scenario.Status) {
			continue
		}
		coverage.BDDTotal++
		if len(nonEmptyStrings(scenario.ConcreteDataRefs)) > 0 {
			coverage.BDDWithConcreteData++
		}
	}

	if len(contract.DesignContracts) > 0 {
		if allDesignContractsComplete(contract.DesignContracts) {
			coverage.GatesPassed = appendUniqueString(coverage.GatesPassed, "contract")
		} else {
			coverage.GatesBlocked = appendUniqueString(coverage.GatesBlocked, "contract")
		}
	}

	for _, invariant := range contract.Invariants {
		if !BehavioralCoverageStatusIsAccepted(invariant.Status) {
			continue
		}
		coverage.InvariantsTotal++
		if len(nonEmptyStrings(invariant.PropertyBasedTestRefs)) > 0 {
			coverage.InvariantsWithPBT++
		}
	}

	for _, machine := range contract.StateMachines {
		if !BehavioralCoverageStatusIsAccepted(machine.Status) {
			continue
		}
		coverage.StateMachinesTotal++
		if stateMachineCoverageComplete(machine) {
			coverage.StateMachinesCovered++
		}
	}

	for _, mode := range contract.FailureModes {
		if !BehavioralCoverageStatusIsAccepted(mode.Status) {
			continue
		}
		coverage.FMEAModesTotal++
		if len(nonEmptyStrings(mode.MitigationTestRefs)) > 0 {
			coverage.FMEAModesMitigated++
		}
	}

	for _, deviation := range contract.HAZOPDeviations {
		if !BehavioralCoverageStatusIsAccepted(deviation.Status) {
			continue
		}
		coverage.HAZOPDeviationsTotal++
		if len(nonEmptyStrings(deviation.TestRefs)) > 0 {
			coverage.HAZOPDeviationsTested++
		}
	}

	for _, table := range contract.DecisionTables {
		if !BehavioralCoverageStatusIsAccepted(table.Status) {
			continue
		}
		coverage.DecisionTablesTotal++
		if decisionTableCoverageComplete(table) {
			coverage.DecisionTablesEnumerated++
		}
	}

	for _, boundary := range contract.BoundaryValues {
		if !BehavioralCoverageStatusIsAccepted(boundary.Status) {
			continue
		}
		coverage.BoundaryValuesTotal++
		if len(nonEmptyStrings(boundary.Values)) > 0 && len(nonEmptyStrings(boundary.TestRefs)) > 0 {
			coverage.BoundaryValuesTested++
		}
	}

	for _, leaf := range contract.FaultTreeLeaves {
		if !BehavioralCoverageStatusIsAccepted(leaf.Status) {
			continue
		}
		coverage.FaultTreeLeavesTotal++
		if len(nonEmptyStrings(leaf.TestRefs)) > 0 {
			coverage.FaultTreeLeavesTested++
		}
	}

	for _, incident := range contract.PostMortems {
		if !BehavioralCoverageStatusIsAccepted(incident.Status) {
			continue
		}
		coverage.PostMortemIncidentsTotal++
		if len(nonEmptyStrings(incident.CatchingTestRefs)) > 0 {
			coverage.PostMortemCatchingTests++
		}
	}

	for _, model := range contract.FormalModels {
		if !BehavioralCoverageStatusIsAccepted(model.Status) {
			continue
		}
		coverage.FormalModelsTotal++
		if len(nonEmptyStrings(model.VerificationRefs)) > 0 {
			coverage.FormalModelsVerified++
		}
	}

	for _, target := range contract.FuzzTargets {
		if !BehavioralCoverageStatusIsAccepted(target.Status) {
			continue
		}
		coverage.FuzzTargetsTotal++
		if fuzzTargetCoverageComplete(target) {
			coverage.FuzzTargetsCovered++
		}
	}

	for _, eval := range contract.RAGEvals {
		if !BehavioralCoverageStatusIsAccepted(eval.Status) {
			continue
		}
		coverage.RAGEvalsTotal++
		if ragEvaluationCoverageComplete(eval) {
			coverage.RAGEvalsMeasured++
		}
	}

	for _, securityCase := range contract.SecurityCases {
		if !BehavioralCoverageStatusIsAccepted(securityCase.Status) {
			continue
		}
		coverage.SecurityCasesTotal++
		if securityCaseCoverageComplete(securityCase) {
			coverage.SecurityCasesTested++
		}
	}

	for _, privacy := range contract.PrivacyControls {
		if !BehavioralCoverageStatusIsAccepted(privacy.Status) {
			continue
		}
		coverage.PrivacyControlsTotal++
		if privacyControlCoverageComplete(privacy) {
			coverage.PrivacyControlsTested++
		}
	}

	for _, provider := range contract.ProviderContracts {
		if !BehavioralCoverageStatusIsAccepted(provider.Status) {
			continue
		}
		coverage.ProviderContractsTotal++
		if providerContractCoverageComplete(provider) {
			coverage.ProviderContractsTested++
		}
	}

	for _, signal := range contract.Observability {
		if !BehavioralCoverageStatusIsAccepted(signal.Status) {
			continue
		}
		coverage.ObservabilityTotal++
		if observabilityCoverageComplete(signal) {
			coverage.ObservabilityTested++
		}
	}

	for _, bound := range contract.PerformanceBounds {
		if !BehavioralCoverageStatusIsAccepted(bound.Status) {
			continue
		}
		coverage.PerformanceBoundsTotal++
		if performanceBoundCoverageComplete(bound) {
			coverage.PerformanceBoundsTested++
		}
	}

	for _, journey := range contract.E2EJourneys {
		if !BehavioralCoverageStatusIsAccepted(journey.Status) {
			continue
		}
		coverage.E2EJourneysTotal++
		if e2eJourneyCoverageComplete(journey) {
			coverage.E2EJourneysScripted++
		}
	}
	if len(contract.SurfaceInventory) > 0 {
		if allBehavioralSurfacesDecided(contract.SurfaceInventory) {
			coverage.GatesPassed = appendUniqueString(coverage.GatesPassed, "surface_inventory")
		} else {
			coverage.GatesBlocked = appendUniqueString(coverage.GatesBlocked, "surface_inventory")
		}
	}

	projectMutationCoverage(contract.Mutation, &coverage)
	coverage.GatesPassed = uniqueSortedStrings(coverage.GatesPassed)
	coverage.GatesBlocked = uniqueSortedStrings(coverage.GatesBlocked)

	return coverage
}

func ProjectAcceptedBehavioralCoverage(input BriefGenerationInput) *AcceptedBehavioralCoverageProjection {
	if input.TDD.BehavioralCoverage == nil {
		return nil
	}
	ref := implementationHandoffCoverageContract(input)
	if ref == nil || !implementationHandoffAcceptedCoverageStatus(ref.Status) {
		return nil
	}
	accepted := AcceptedOnlyBehavioralCoverageContract(*input.TDD.BehavioralCoverage)

	return &AcceptedBehavioralCoverageProjection{
		Contract:   *ref,
		Behavioral: accepted,
		Projected:  ProjectBehavioralCoverageContract(accepted),
		Status:     ref.Status,
		Authority:  "product_workflow",
	}
}

func AcceptedOnlyBehavioralCoverageContract(contract BehavioralCoverageContract) BehavioralCoverageContract {
	out := contract
	acceptedRequirementIDs := map[string]bool{}
	out.EARS = nil
	for _, item := range contract.EARS {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.EARS = append(out.EARS, item)
			if id := strings.TrimSpace(item.ID); id != "" {
				acceptedRequirementIDs[id] = true
			}
		}
	}
	acceptedRuleIDs := map[string]bool{}
	out.ExampleRules = nil
	for _, item := range contract.ExampleRules {
		requirementID := strings.TrimSpace(item.RequirementID)
		if requirementID != "" && !acceptedRequirementIDs[requirementID] {
			continue
		}
		out.ExampleRules = append(out.ExampleRules, item)
		if id := strings.TrimSpace(item.ID); id != "" {
			acceptedRuleIDs[id] = true
		}
	}
	acceptedObligationIDs := map[string]bool{}
	addObligations := func(refs []string) {
		for _, ref := range refs {
			if id := strings.TrimSpace(ref); id != "" {
				acceptedObligationIDs[id] = true
			}
		}
	}
	for _, item := range out.EARS {
		addObligations(item.Obligations)
	}
	out.BDDScenarios = nil
	for _, item := range contract.BDDScenarios {
		requirementID := strings.TrimSpace(item.RequirementID)
		ruleID := strings.TrimSpace(item.RuleID)
		if !BehavioralCoverageStatusIsAccepted(item.Status) ||
			(requirementID != "" && !acceptedRequirementIDs[requirementID]) ||
			(ruleID != "" && !acceptedRuleIDs[ruleID]) {
			continue
		}
		out.BDDScenarios = append(out.BDDScenarios, item)
		addObligations(item.Obligations)
	}
	out.DesignContracts = nil
	for _, item := range contract.DesignContracts {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.DesignContracts = append(out.DesignContracts, item)
			addObligations(item.Obligations)
		}
	}
	out.Invariants = nil
	for _, item := range contract.Invariants {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.Invariants = append(out.Invariants, item)
			addObligations(item.Obligations)
		}
	}
	out.StateMachines = nil
	for _, item := range contract.StateMachines {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.StateMachines = append(out.StateMachines, item)
		}
	}
	out.FailureModes = nil
	for _, item := range contract.FailureModes {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.FailureModes = append(out.FailureModes, item)
			addObligations(item.Obligations)
		}
	}
	out.HAZOPDeviations = nil
	for _, item := range contract.HAZOPDeviations {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.HAZOPDeviations = append(out.HAZOPDeviations, item)
			addObligations(item.Obligations)
		}
	}
	out.FaultTreeLeaves = nil
	for _, item := range contract.FaultTreeLeaves {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.FaultTreeLeaves = append(out.FaultTreeLeaves, item)
			addObligations(item.Obligations)
		}
	}
	out.DecisionTables = nil
	for _, item := range contract.DecisionTables {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.DecisionTables = append(out.DecisionTables, item)
			addObligations(item.Obligations)
		}
	}
	out.BoundaryValues = nil
	for _, item := range contract.BoundaryValues {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.BoundaryValues = append(out.BoundaryValues, item)
			addObligations(item.Obligations)
		}
	}
	out.PostMortems = nil
	for _, item := range contract.PostMortems {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.PostMortems = append(out.PostMortems, item)
			addObligations(item.Obligations)
		}
	}
	out.FormalModels = nil
	for _, item := range contract.FormalModels {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.FormalModels = append(out.FormalModels, item)
			addObligations(item.Obligations)
		}
	}
	out.FuzzTargets = nil
	for _, item := range contract.FuzzTargets {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.FuzzTargets = append(out.FuzzTargets, item)
			addObligations(item.Obligations)
		}
	}
	out.RAGEvals = nil
	for _, item := range contract.RAGEvals {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.RAGEvals = append(out.RAGEvals, item)
			addObligations(item.Obligations)
		}
	}
	out.SecurityCases = nil
	for _, item := range contract.SecurityCases {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.SecurityCases = append(out.SecurityCases, item)
			addObligations(item.Obligations)
		}
	}
	out.PrivacyControls = nil
	for _, item := range contract.PrivacyControls {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.PrivacyControls = append(out.PrivacyControls, item)
			addObligations(item.Obligations)
		}
	}
	out.ProviderContracts = nil
	for _, item := range contract.ProviderContracts {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.ProviderContracts = append(out.ProviderContracts, item)
			addObligations(item.Obligations)
		}
	}
	out.Observability = nil
	for _, item := range contract.Observability {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.Observability = append(out.Observability, item)
			addObligations(item.Obligations)
		}
	}
	out.PerformanceBounds = nil
	for _, item := range contract.PerformanceBounds {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.PerformanceBounds = append(out.PerformanceBounds, item)
			addObligations(item.Obligations)
		}
	}
	out.E2EJourneys = nil
	for _, item := range contract.E2EJourneys {
		if BehavioralCoverageStatusIsAccepted(item.Status) {
			out.E2EJourneys = append(out.E2EJourneys, item)
			addObligations(item.Obligations)
		}
	}
	out.SurfaceInventory = nil
	for _, item := range contract.SurfaceInventory {
		if BehavioralCoverageSurfaceStatusIsClosedDecision(item.Status) {
			out.SurfaceInventory = append(out.SurfaceInventory, item)
			if BehavioralCoverageSurfaceStatusIsAccepted(item.Status) {
				addObligations(item.Obligations)
			}
		}
	}
	out.Verification = nil
	for _, item := range contract.Verification {
		refs := nonEmptyStrings(item.Obligations)
		if len(refs) == 0 || behavioralCoverageAnyRefAccepted(refs, acceptedObligationIDs) {
			out.Verification = append(out.Verification, item)
		}
	}
	out.TestObligations = nil
	for _, item := range contract.TestObligations {
		id := strings.TrimSpace(item.ID)
		if id != "" && acceptedObligationIDs[id] {
			out.TestObligations = append(out.TestObligations, item)
		}
	}

	return out
}

func behavioralCoverageAnyRefAccepted(refs []string, accepted map[string]bool) bool {
	for _, ref := range refs {
		if accepted[strings.TrimSpace(ref)] {
			return true
		}
	}

	return false
}

func reviewBehavioralCoverageContractWithResult(
	state WorkflowState,
	ledger TDDLedger,
	contract BehavioralCoverageContract,
) (BehavioralCoveragePromotionResult, error) {
	result, err := behavioralCoveragePromotionProjection(state, ledger, contract, BehavioralCoveragePromotionStateReviewed)
	if err != nil {
		return result, err
	}
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusNotWritten
	result.AuditStatus = BehavioralCoveragePromotionAuditStatusNotAttempted
	return result, nil
}

func prepareBehavioralCoveragePromotion(
	state WorkflowState,
	ledger TDDLedger,
	contract BehavioralCoverageContract,
) (BehavioralCoveragePromotionResult, error) {
	result, err := behavioralCoveragePromotionProjection(state, ledger, contract, BehavioralCoveragePromotionStatePrepared)
	if err != nil {
		return result, err
	}
	result.CommitStatus = BehavioralCoveragePromotionCommitStatusWritePending
	result.AuditStatus = BehavioralCoveragePromotionAuditStatusNotAttempted
	return result, nil
}

func behavioralCoveragePromotionProjection(
	state WorkflowState,
	ledger TDDLedger,
	contract BehavioralCoverageContract,
	promotionState string,
) (BehavioralCoveragePromotionResult, error) {
	contract, err := validateTDDContractAuthority(state, ledger, contract)
	if err != nil {
		return BehavioralCoveragePromotionResult{
			State:        BehavioralCoveragePromotionStateRejected,
			CommitStatus: BehavioralCoveragePromotionCommitStatusBlocked,
			AuditStatus:  BehavioralCoveragePromotionAuditStatusNotAttempted,
		}, err
	}
	if err := ValidateBehavioralCoverageContract(contract); err != nil {
		return BehavioralCoveragePromotionResult{
			State:        BehavioralCoveragePromotionStateRejected,
			CommitStatus: BehavioralCoveragePromotionCommitStatusBlocked,
			AuditStatus:  BehavioralCoveragePromotionAuditStatusNotAttempted,
		}, err
	}

	projected := ProjectBehavioralCoverageContract(contract)
	var supersededDigest string
	priorPromotedObligationIDs := map[string]bool{}
	if ledger.BehavioralCoverage != nil {
		supersededDigest = BehavioralCoverageContractDigest(*ledger.BehavioralCoverage)
		priorPromotedObligationIDs = behavioralCoverageObligationIDs(ledger.BehavioralCoverage.TestObligations)
	}
	digest := BehavioralCoverageContractDigest(contract)
	if digest == "" {
		return BehavioralCoveragePromotionResult{
			State:        BehavioralCoveragePromotionStateRejected,
			CommitStatus: BehavioralCoveragePromotionCommitStatusBlocked,
			AuditStatus:  BehavioralCoveragePromotionAuditStatusNotAttempted,
		}, errors.New("behavioral coverage contract digest could not be computed")
	}
	ledger.SchemaVersion = firstNonEmptyStr(ledger.SchemaVersion, "v1")
	ledger.Coverage = projected
	ledger.BehavioralCoverage = &contract
	ledger.Obligations = mergePromotedBehavioralObligations(
		ledger.Obligations,
		priorPromotedObligationIDs,
		projectBehavioralTestObligations(contract.TestObligations),
	)

	if supersededDigest == digest {
		supersededDigest = ""
	}
	return BehavioralCoveragePromotionResult{
		State:            promotionState,
		Digest:           digest,
		SupersededDigest: supersededDigest,
		Ledger:           ledger,
	}, nil
}

func behavioralCoveragePromotionEventID(digest string) string {
	digest = strings.TrimPrefix(strings.TrimSpace(digest), "sha256:")
	if len(digest) > 16 {
		digest = digest[:16]
	}
	if digest == "" {
		return ""
	}
	return "coverage_promoted_" + digest
}

func mergePromotedBehavioralObligations(
	existing []TDDObligation,
	priorPromotedIDs map[string]bool,
	promoted []TDDObligation,
) []TDDObligation {
	promotedIDs := tddObligationIDs(promoted)

	out := make([]TDDObligation, 0, len(existing)+len(promoted))
	for _, obligation := range existing {
		id := strings.TrimSpace(obligation.ID)
		if id != "" && (priorPromotedIDs[id] || promotedIDs[id]) {
			continue
		}
		out = append(out, obligation)
	}
	out = append(out, promoted...)

	return out
}

func tddObligationIDs(obligations []TDDObligation) map[string]bool {
	ids := make(map[string]bool, len(obligations))
	for _, obligation := range obligations {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			ids[id] = true
		}
	}

	return ids
}

func ValidateBehavioralCoverageProjection(contract BehavioralCoverageContract, coverage TDDCoverage) error {
	projected := ProjectBehavioralCoverageContract(contract)
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"ears_total", coverage.EARSTotal, projected.EARSTotal},
		{"ears_mapped", coverage.EARSMapped, projected.EARSMapped},
		{"bdd_total", coverage.BDDTotal, projected.BDDTotal},
		{"bdd_with_concrete_data", coverage.BDDWithConcreteData, projected.BDDWithConcreteData},
		{"invariants_total", coverage.InvariantsTotal, projected.InvariantsTotal},
		{"invariants_with_pbt", coverage.InvariantsWithPBT, projected.InvariantsWithPBT},
		{"state_machines_total", coverage.StateMachinesTotal, projected.StateMachinesTotal},
		{"state_machines_covered", coverage.StateMachinesCovered, projected.StateMachinesCovered},
		{"fmea_modes_total", coverage.FMEAModesTotal, projected.FMEAModesTotal},
		{"fmea_modes_mitigated", coverage.FMEAModesMitigated, projected.FMEAModesMitigated},
		{"hazop_deviations_total", coverage.HAZOPDeviationsTotal, projected.HAZOPDeviationsTotal},
		{"hazop_deviations_tested", coverage.HAZOPDeviationsTested, projected.HAZOPDeviationsTested},
		{"decision_tables_total", coverage.DecisionTablesTotal, projected.DecisionTablesTotal},
		{"decision_tables_enumerated", coverage.DecisionTablesEnumerated, projected.DecisionTablesEnumerated},
		{"boundary_values_total", coverage.BoundaryValuesTotal, projected.BoundaryValuesTotal},
		{"boundary_values_tested", coverage.BoundaryValuesTested, projected.BoundaryValuesTested},
		{"fault_tree_leaves_total", coverage.FaultTreeLeavesTotal, projected.FaultTreeLeavesTotal},
		{"fault_tree_leaves_tested", coverage.FaultTreeLeavesTested, projected.FaultTreeLeavesTested},
		{"post_mortem_incidents_total", coverage.PostMortemIncidentsTotal, projected.PostMortemIncidentsTotal},
		{"post_mortem_catching_tests", coverage.PostMortemCatchingTests, projected.PostMortemCatchingTests},
		{"formal_models_total", coverage.FormalModelsTotal, projected.FormalModelsTotal},
		{"formal_models_verified", coverage.FormalModelsVerified, projected.FormalModelsVerified},
		{"fuzz_targets_total", coverage.FuzzTargetsTotal, projected.FuzzTargetsTotal},
		{"fuzz_targets_covered", coverage.FuzzTargetsCovered, projected.FuzzTargetsCovered},
		{"rag_evals_total", coverage.RAGEvalsTotal, projected.RAGEvalsTotal},
		{"rag_evals_measured", coverage.RAGEvalsMeasured, projected.RAGEvalsMeasured},
		{"security_cases_total", coverage.SecurityCasesTotal, projected.SecurityCasesTotal},
		{"security_cases_tested", coverage.SecurityCasesTested, projected.SecurityCasesTested},
		{"privacy_controls_total", coverage.PrivacyControlsTotal, projected.PrivacyControlsTotal},
		{"privacy_controls_tested", coverage.PrivacyControlsTested, projected.PrivacyControlsTested},
		{"provider_contracts_total", coverage.ProviderContractsTotal, projected.ProviderContractsTotal},
		{"provider_contracts_tested", coverage.ProviderContractsTested, projected.ProviderContractsTested},
		{"observability_total", coverage.ObservabilityTotal, projected.ObservabilityTotal},
		{"observability_tested", coverage.ObservabilityTested, projected.ObservabilityTested},
		{"performance_bounds_total", coverage.PerformanceBoundsTotal, projected.PerformanceBoundsTotal},
		{"performance_bounds_tested", coverage.PerformanceBoundsTested, projected.PerformanceBoundsTested},
		{"e2e_journeys_total", coverage.E2EJourneysTotal, projected.E2EJourneysTotal},
		{"e2e_journeys_scripted", coverage.E2EJourneysScripted, projected.E2EJourneysScripted},
	}

	var errs []error
	for _, check := range checks {
		if check.got != check.want {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.%s (%d) does not match behavioral coverage projection (%d)", check.name, check.got, check.want))
		}
	}

	requirement := normalizeBehavioralMutationRequirement(contract.Mutation.Requirement)
	if requirement != "" {
		if coverage.MutationMinScore != projected.MutationMinScore {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.mutation_min_score (%d) does not match behavioral coverage projection (%d)", coverage.MutationMinScore, projected.MutationMinScore))
		}
		if !sameOptionalFloat64(coverage.MutationScore, projected.MutationScore) {
			errs = append(errs, errors.New("tdd ledger coverage.mutation_score does not match behavioral coverage projection"))
		}
		for _, gate := range projected.GatesBlocked {
			if !containsString(coverage.GatesBlocked, gate) {
				errs = append(errs, fmt.Errorf("tdd ledger coverage.gates_blocked missing behavioral coverage projection %q", gate))
			}
		}
	}
	for _, gate := range projected.GatesPassed {
		if !containsString(coverage.GatesPassed, gate) {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.gates_passed missing behavioral coverage projection %q", gate))
		}
	}
	for _, gate := range projected.GatesBlocked {
		if !containsString(coverage.GatesBlocked, gate) {
			errs = append(errs, fmt.Errorf("tdd ledger coverage.gates_blocked missing behavioral coverage projection %q", gate))
		}
	}

	return errors.Join(errs...)
}

func BehavioralCoverageContractDigest(contract BehavioralCoverageContract) string {
	// EventID is promotion provenance, not contract content. Excluding it keeps
	// the digest stable when the owner records the generated event identity in
	// the authoritative TDD ledger.
	contract.EventID = ""
	contract.PromotionState = ""
	data, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func legacyBehavioralCoverageContractDigest(contract BehavioralCoverageContract) string {
	// Before promotion provenance moved into the ledger authority, EventID was
	// part of the contract digest and PromotionState did not exist. Retain this
	// projection only for matching already-written audit events.
	contract.PromotionState = ""
	data, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateEARSRequirementCoverage(prefix string, requirement EARSRequirementCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, requirement.ID, requirement.Status, len(nonEmptyStrings(requirement.MappedRefs)) > 0 || len(nonEmptyStrings(requirement.Obligations)) > 0, "missing mapped_refs or tdd_obligations")
	if strings.TrimSpace(requirement.Statement) == "" {
		errs = append(errs, fmt.Errorf("%s missing statement", prefix))
	}
	if containsAmbiguousEARSRequirementLanguage(requirement.Statement) {
		errs = append(errs, fmt.Errorf("%s ambiguous statement language", prefix))
	}

	return errs
}

func validateExampleRuleCoverage(prefix string, rule ExampleRuleCoverage) []error {
	var errs []error
	if strings.TrimSpace(rule.ID) == "" {
		errs = append(errs, fmt.Errorf("%s missing id", prefix))
	}
	if strings.TrimSpace(rule.Rule) == "" {
		errs = append(errs, fmt.Errorf("%s missing rule", prefix))
	}
	if len(rule.Examples) < 2 {
		errs = append(errs, fmt.Errorf("%s requires at least two concrete examples", prefix))
	}
	for i, example := range rule.Examples {
		examplePrefix := fmt.Sprintf("%s examples[%d]", prefix, i)
		if strings.TrimSpace(example.ID) == "" {
			errs = append(errs, fmt.Errorf("%s missing id", examplePrefix))
		}
		if len(nonEmptyStrings(example.ConcreteDataRefs)) == 0 {
			errs = append(errs, fmt.Errorf("%s missing concrete_data_refs", examplePrefix))
		}
	}

	return errs
}

func validateBDDScenarioCoverage(prefix string, scenario BDDScenarioCoverage) []error {
	complete := len(nonEmptyStrings(scenario.Given)) > 0 &&
		len(nonEmptyStrings(scenario.When)) > 0 &&
		len(nonEmptyStrings(scenario.Then)) > 0 &&
		len(nonEmptyStrings(scenario.ConcreteDataRefs)) > 0
	errs := validateBehavioralCoverageItem(prefix, scenario.ID, scenario.Status, complete, "missing concrete given/when/then data or concrete_data_refs")
	if normalizeBehavioralCoverageStatus(scenario.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	if len(nonEmptyStrings(scenario.Given)) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing given", prefix))
	}
	if len(nonEmptyStrings(scenario.When)) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing when", prefix))
	}
	if len(nonEmptyStrings(scenario.Then)) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing then", prefix))
	}
	if len(nonEmptyStrings(scenario.ConcreteDataRefs)) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing concrete_data_refs", prefix))
	}
	return errs
}

func validateDesignContractCoverage(prefix string, coverage DesignContractCoverage) []error {
	complete := len(nonEmptyStrings(coverage.Preconditions)) > 0 &&
		len(nonEmptyStrings(coverage.Postconditions)) > 0 &&
		len(nonEmptyStrings(coverage.Invariants)) > 0 &&
		len(nonEmptyStrings(coverage.TestRefs)) > 0
	errs := validateBehavioralCoverageItem(prefix, coverage.ID, coverage.Status, complete, "missing preconditions, postconditions, invariants, or test_refs")
	if normalizeBehavioralCoverageStatus(coverage.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	if strings.TrimSpace(coverage.Boundary) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing boundary", prefix))
	}
	return errs
}

func validateHAZOPDeviationCoverage(prefix string, deviation HAZOPDeviationCoverage) []error {
	errs := validateSimpleTestedCoverage(prefix, deviation.ID, deviation.Status, deviation.TestRefs, "missing test_refs")
	if normalizeBehavioralCoverageStatus(deviation.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	if strings.TrimSpace(deviation.GuideWord) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing guide_word", prefix))
	}
	if strings.TrimSpace(deviation.Deviation) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing deviation", prefix))
	}
	if strings.TrimSpace(deviation.WorkflowStep) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing workflow_step", prefix))
	}
	return errs
}

func validateDecisionTableCoverage(prefix string, table DecisionTableCoverage) []error {
	complete := decisionTableCoverageComplete(table)
	errs := validateBehavioralCoverageItem(prefix, table.ID, table.Status, complete, "missing conditions, rules, expected outcomes, or test_refs")
	if normalizeBehavioralCoverageStatus(table.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	for i, rule := range table.Rules {
		rulePrefix := fmt.Sprintf("%s rules[%d]", prefix, i)
		if len(rule.Conditions) == 0 {
			errs = append(errs, fmt.Errorf("%s missing conditions", rulePrefix))
		}
		if strings.TrimSpace(rule.Expected) == "" {
			errs = append(errs, fmt.Errorf("%s missing expected", rulePrefix))
		}
	}
	return errs
}

func validateBoundaryValueCoverage(prefix string, boundary BoundaryValueCoverage) []error {
	complete := len(nonEmptyStrings(boundary.Values)) > 0 && len(nonEmptyStrings(boundary.TestRefs)) > 0
	errs := validateBehavioralCoverageItem(prefix, boundary.ID, boundary.Status, complete, "missing values or test_refs")
	if normalizeBehavioralCoverageStatus(boundary.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	if strings.TrimSpace(boundary.Parameter) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing parameter", prefix))
	}
	return errs
}

func validateSimpleTestedCoverage(prefix string, id string, status string, testRefs []string, missingDetail string) []error {
	return validateBehavioralCoverageItem(prefix, id, status, len(nonEmptyStrings(testRefs)) > 0, missingDetail)
}

func validateFaultTreeLeafCoverage(prefix string, leaf FaultTreeLeafCoverage) []error {
	errs := validateSimpleTestedCoverage(prefix, leaf.ID, leaf.Status, leaf.TestRefs, "missing test_refs")
	if normalizeBehavioralCoverageStatus(leaf.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	if strings.TrimSpace(leaf.TopEvent) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing top_event", prefix))
	}
	if strings.TrimSpace(leaf.Cause) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing cause", prefix))
	}
	return errs
}

func validatePostMortemCoverage(prefix string, incident PostMortemCoverage) []error {
	errs := validateSimpleTestedCoverage(prefix, incident.ID, incident.Status, incident.CatchingTestRefs, "missing catching_test_refs")
	if normalizeBehavioralCoverageStatus(incident.Status) == BehavioralCoverageStatusAccepted && strings.TrimSpace(incident.Incident) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing incident", prefix))
	}
	return errs
}

func validateFormalModelCoverage(prefix string, model FormalModelCoverage) []error {
	errs := validateSimpleTestedCoverage(prefix, model.ID, model.Status, model.VerificationRefs, "missing verification_refs")
	if normalizeBehavioralCoverageStatus(model.Status) == BehavioralCoverageStatusAccepted && strings.TrimSpace(model.Model) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing model", prefix))
	}
	return errs
}

func validateFuzzTargetCoverage(prefix string, target FuzzTargetCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, target.ID, target.Status, fuzzTargetCoverageComplete(target), "missing target, generators, invariants, or test_refs")
	if normalizeBehavioralCoverageStatus(target.Status) == BehavioralCoverageStatusAccepted && strings.TrimSpace(target.Target) == "" {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing target", prefix))
	}
	return errs
}

func validateRAGEvaluationCoverage(prefix string, eval RAGEvaluationCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, eval.ID, eval.Status, ragEvaluationCoverageComplete(eval), "missing dataset_refs, metrics, thresholds, or eval_refs")
	if normalizeBehavioralCoverageStatus(eval.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}
	for metric, threshold := range eval.Thresholds {
		if strings.TrimSpace(metric) == "" {
			errs = append(errs, fmt.Errorf("%s threshold metric name must not be blank", prefix))
			continue
		}
		if threshold <= 0 {
			errs = append(errs, fmt.Errorf("%s threshold %q must be positive", prefix, metric))
		}
	}
	return errs
}

func validateSecurityCaseCoverage(prefix string, securityCase SecurityCaseCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, securityCase.ID, securityCase.Status, securityCaseCoverageComplete(securityCase), "missing risk, attack, expected outcome, or test_refs")
	return errs
}

func validatePrivacyControlCoverage(prefix string, privacy PrivacyControlCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, privacy.ID, privacy.Status, privacyControlCoverageComplete(privacy), "missing data_class, policy, redaction_refs, retention, or test_refs")
	return errs
}

func validateProviderContractCoverage(prefix string, provider ProviderContractCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, provider.ID, provider.Status, providerContractCoverageComplete(provider), "missing provider, consumer, contract_refs, or test_refs")
	return errs
}

func validateObservabilityCoverage(prefix string, signal ObservabilityCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, signal.ID, signal.Status, observabilityCoverageComplete(signal), "missing signal, instrumented_path, required_fields, or test_refs")
	return errs
}

func validatePerformanceBoundCoverage(prefix string, bound PerformanceBoundCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, bound.ID, bound.Status, performanceBoundCoverageComplete(bound), "missing operation, bound, or measurement_refs")
	return errs
}

func validateE2EJourneyCoverage(prefix string, journey E2EJourneyCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, journey.ID, journey.Status, e2eJourneyCoverageComplete(journey), "missing journey, steps, or script_refs")
	return errs
}

func validateBehavioralSurfaceCoverage(prefix string, surface BehavioralSurfaceCoverage) []error {
	var errs []error
	if strings.TrimSpace(surface.ID) == "" {
		errs = append(errs, fmt.Errorf("%s missing id", prefix))
	}
	if strings.TrimSpace(surface.Surface) == "" {
		errs = append(errs, fmt.Errorf("%s missing surface", prefix))
	}
	status := NormalizeBehavioralSurfaceStatus(surface.Status)
	if status == "" {
		errs = append(errs, fmt.Errorf("%s missing status", prefix))
	}
	switch status {
	case BehavioralCoverageStatusAccepted:
		if len(nonEmptyStrings(surface.Obligations)) == 0 {
			errs = append(errs, fmt.Errorf("%s in-scope surface missing tdd_obligations", prefix))
		}
	case BehavioralCoverageStatusWaived, BehavioralCoverageStatusNotRequired:
		if strings.TrimSpace(surface.Rationale) == "" {
			errs = append(errs, fmt.Errorf("%s %s surface missing rationale", prefix, status))
		}
	case "open_question":
		if strings.TrimSpace(surface.Rationale) == "" && len(nonEmptyStrings(surface.QuestionRefs)) == 0 {
			errs = append(errs, fmt.Errorf("%s open_question surface missing rationale or question_refs", prefix))
		}
	default:
		errs = append(errs, fmt.Errorf("%s unsupported status %q", prefix, surface.Status))
	}
	return errs
}

func validateBehavioralSurfaceInventoryConsistency(surfaces []BehavioralSurfaceCoverage) []error {
	var errs []error
	ids := map[string]int{}
	surfaceStatuses := map[string]string{}
	for i, surface := range surfaces {
		id := strings.TrimSpace(surface.ID)
		if id != "" {
			if first, ok := ids[id]; ok {
				errs = append(errs, fmt.Errorf("behavioral coverage surface_inventory[%d] duplicates id %q first declared at surface_inventory[%d]", i, id, first))
			} else {
				ids[id] = i
			}
		}
		name := strings.TrimSpace(strings.ToLower(surface.Surface))
		if name == "" {
			continue
		}
		status := NormalizeBehavioralSurfaceStatus(surface.Status)
		if firstStatus, ok := surfaceStatuses[name]; ok && firstStatus != status {
			errs = append(errs, fmt.Errorf("behavioral coverage surface_inventory[%d] conflicts for surface %q: status %q differs from prior status %q", i, surface.Surface, surface.Status, firstStatus))
			continue
		}
		surfaceStatuses[name] = status
	}
	return errs
}

func validateBehavioralCoverageQuestion(prefix string, question BehavioralCoverageQuestion) []error {
	var errs []error
	if strings.TrimSpace(question.ID) == "" {
		errs = append(errs, fmt.Errorf("%s missing id", prefix))
	}
	if strings.TrimSpace(question.Question) == "" {
		errs = append(errs, fmt.Errorf("%s missing question", prefix))
	}
	status := strings.TrimSpace(strings.ToLower(question.Status))
	if status == "" {
		status = "resolved"
	}
	if status != "resolved" && status != "waived" {
		errs = append(errs, fmt.Errorf("%s unresolved question status %q", prefix, question.Status))
	}
	if status == "resolved" && strings.TrimSpace(question.Resolution) == "" {
		errs = append(errs, fmt.Errorf("%s resolved question missing resolution", prefix))
	}
	return errs
}

func validateKnownBehavioralObligationRefs(prefix string, refs []string, obligationIDs map[string]bool) []error {
	if len(refs) == 0 {
		return nil
	}
	var errs []error
	for i, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if !obligationIDs[ref] {
			errs = append(errs, fmt.Errorf("%s tdd_obligations[%d] references unknown obligation %q", prefix, i, ref))
		}
	}
	return errs
}

func validateBehavioralCoverageMappedRefs(prefix string, refs []string, known map[string]bool) []error {
	if len(refs) == 0 {
		return nil
	}
	var errs []error
	for i, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if !known[ref] {
			errs = append(errs, fmt.Errorf("%s mapped_refs[%d] references unknown coverage artifact or source ref %q", prefix, i, ref))
		}
	}
	return errs
}

func validateKnownBehavioralCoverageRef(prefix string, field string, ref string, known map[string]bool) []error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	if known[ref] {
		return nil
	}
	return []error{fmt.Errorf("%s %s references unknown coverage artifact %q", prefix, field, ref)}
}

func behavioralCoverageObligationIDs(obligations []TestObligationCoverage) map[string]bool {
	out := map[string]bool{}
	for _, obligation := range obligations {
		if id := strings.TrimSpace(obligation.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func behavioralCoverageEARSRequirementIDs(requirements []EARSRequirementCoverage) map[string]bool {
	out := map[string]bool{}
	for _, requirement := range requirements {
		if id := strings.TrimSpace(requirement.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func behavioralCoverageExampleRuleIDs(rules []ExampleRuleCoverage) map[string]bool {
	out := map[string]bool{}
	for _, rule := range rules {
		if id := strings.TrimSpace(rule.ID); id != "" {
			out[id] = true
		}
	}
	return out
}

func behavioralCoverageArtifactIDs(contract BehavioralCoverageContract) map[string]bool {
	out := map[string]bool{}
	add := func(id string) {
		if id = strings.TrimSpace(id); id != "" {
			out[id] = true
		}
	}

	for _, ref := range contract.SourceRefs {
		add(ref)
	}
	for _, rule := range contract.ExampleRules {
		add(rule.ID)
	}
	for _, scenario := range contract.BDDScenarios {
		add(scenario.ID)
	}
	for _, coverage := range contract.DesignContracts {
		add(coverage.ID)
	}
	for _, invariant := range contract.Invariants {
		add(invariant.ID)
	}
	for _, machine := range contract.StateMachines {
		add(machine.ID)
		for _, transition := range machine.Transitions {
			add(transition.ID)
		}
	}
	for _, mode := range contract.FailureModes {
		add(mode.ID)
	}
	for _, deviation := range contract.HAZOPDeviations {
		add(deviation.ID)
	}
	for _, leaf := range contract.FaultTreeLeaves {
		add(leaf.ID)
	}
	for _, table := range contract.DecisionTables {
		add(table.ID)
		for _, rule := range table.Rules {
			add(rule.ID)
		}
	}
	for _, boundary := range contract.BoundaryValues {
		add(boundary.ID)
	}
	for _, incident := range contract.PostMortems {
		add(incident.ID)
	}
	for _, model := range contract.FormalModels {
		add(model.ID)
	}
	for _, target := range contract.FuzzTargets {
		add(target.ID)
	}
	for _, eval := range contract.RAGEvals {
		add(eval.ID)
	}
	for _, securityCase := range contract.SecurityCases {
		add(securityCase.ID)
	}
	for _, privacy := range contract.PrivacyControls {
		add(privacy.ID)
	}
	for _, provider := range contract.ProviderContracts {
		add(provider.ID)
	}
	for _, signal := range contract.Observability {
		add(signal.ID)
	}
	for _, bound := range contract.PerformanceBounds {
		add(bound.ID)
	}
	for _, journey := range contract.E2EJourneys {
		add(journey.ID)
	}

	return out
}

func validateBehavioralCoverageIdentifierUniqueness(contract BehavioralCoverageContract) []error {
	var errs []error
	global := map[string]string{}
	addGlobal := func(id, location string) {
		if err := recordUniqueBehavioralCoverageID(global, id, location); err != nil {
			errs = append(errs, err)
		}
	}
	for index, ref := range contract.SourceRefs {
		addGlobal(ref, fmt.Sprintf("source_refs[%d]", index))
	}
	for index, item := range contract.ExampleRules {
		addGlobal(item.ID, fmt.Sprintf("example_rules[%d]", index))
		examples := map[string]string{}
		for exampleIndex, example := range item.Examples {
			if err := recordUniqueBehavioralCoverageID(examples, example.ID, fmt.Sprintf("example_rules[%d].examples[%d]", index, exampleIndex)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for index, item := range contract.BDDScenarios {
		addGlobal(item.ID, fmt.Sprintf("bdd_scenarios[%d]", index))
	}
	for index, item := range contract.DesignContracts {
		addGlobal(item.ID, fmt.Sprintf("design_contracts[%d]", index))
	}
	for index, item := range contract.Invariants {
		addGlobal(item.ID, fmt.Sprintf("invariants[%d]", index))
	}
	for index, item := range contract.StateMachines {
		addGlobal(item.ID, fmt.Sprintf("state_machines[%d]", index))
		for transitionIndex, transition := range item.Transitions {
			addGlobal(transition.ID, fmt.Sprintf("state_machines[%d].transitions[%d]", index, transitionIndex))
		}
	}
	for index, item := range contract.FailureModes {
		addGlobal(item.ID, fmt.Sprintf("failure_modes[%d]", index))
	}
	for index, item := range contract.HAZOPDeviations {
		addGlobal(item.ID, fmt.Sprintf("hazop_deviations[%d]", index))
	}
	for index, item := range contract.FaultTreeLeaves {
		addGlobal(item.ID, fmt.Sprintf("fault_tree_leaves[%d]", index))
	}
	for index, item := range contract.DecisionTables {
		addGlobal(item.ID, fmt.Sprintf("decision_tables[%d]", index))
		for ruleIndex, rule := range item.Rules {
			addGlobal(rule.ID, fmt.Sprintf("decision_tables[%d].rules[%d]", index, ruleIndex))
		}
	}
	for index, item := range contract.BoundaryValues {
		addGlobal(item.ID, fmt.Sprintf("boundary_values[%d]", index))
	}
	for index, item := range contract.PostMortems {
		addGlobal(item.ID, fmt.Sprintf("post_mortems[%d]", index))
	}
	for index, item := range contract.FormalModels {
		addGlobal(item.ID, fmt.Sprintf("formal_models[%d]", index))
	}
	for index, item := range contract.FuzzTargets {
		addGlobal(item.ID, fmt.Sprintf("fuzz_targets[%d]", index))
	}
	for index, item := range contract.RAGEvals {
		addGlobal(item.ID, fmt.Sprintf("rag_evals[%d]", index))
	}
	for index, item := range contract.SecurityCases {
		addGlobal(item.ID, fmt.Sprintf("security_cases[%d]", index))
	}
	for index, item := range contract.PrivacyControls {
		addGlobal(item.ID, fmt.Sprintf("privacy_controls[%d]", index))
	}
	for index, item := range contract.ProviderContracts {
		addGlobal(item.ID, fmt.Sprintf("provider_contracts[%d]", index))
	}
	for index, item := range contract.Observability {
		addGlobal(item.ID, fmt.Sprintf("observability[%d]", index))
	}
	for index, item := range contract.PerformanceBounds {
		addGlobal(item.ID, fmt.Sprintf("performance_bounds[%d]", index))
	}
	for index, item := range contract.E2EJourneys {
		addGlobal(item.ID, fmt.Sprintf("e2e_journeys[%d]", index))
	}

	localNamespaces := []struct {
		name string
		ids  []string
	}{
		{name: "ears", ids: behavioralCoverageEARSIDValues(contract.EARS)},
		{name: "test_obligations", ids: behavioralCoverageTestObligationIDValues(contract.TestObligations)},
		{name: "verification", ids: behavioralCoverageVerificationIDValues(contract.Verification)},
		{name: "waivers", ids: behavioralCoverageWaiverIDValues(contract.Waivers)},
		{name: "feedback", ids: behavioralCoverageFeedbackIDValues(contract.Feedback)},
		{name: "open_questions", ids: behavioralCoverageQuestionIDValues(contract.OpenQuestions)},
	}
	for _, namespace := range localNamespaces {
		seen := map[string]string{}
		for index, id := range namespace.ids {
			if err := recordUniqueBehavioralCoverageID(seen, id, fmt.Sprintf("%s[%d]", namespace.name, index)); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errs
}

func recordUniqueBehavioralCoverageID(seen map[string]string, id, location string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if first, ok := seen[id]; ok {
		return fmt.Errorf("behavioral coverage %s duplicates id %q first declared at %s", location, id, first)
	}
	seen[id] = location

	return nil
}

func behavioralCoverageEARSIDValues(items []EARSRequirementCoverage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func behavioralCoverageTestObligationIDValues(items []TestObligationCoverage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func behavioralCoverageVerificationIDValues(items []VerificationGateCoverage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func behavioralCoverageWaiverIDValues(items []BehavioralCoverageWaiver) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func behavioralCoverageFeedbackIDValues(items []BehavioralFeedbackCoverage) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func behavioralCoverageQuestionIDValues(items []BehavioralCoverageQuestion) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func containsAmbiguousEARSRequirementLanguage(statement string) bool {
	lower := strings.ToLower(statement)
	for _, token := range []string{" should ", " might ", " may ", " appropriately", " as needed", " maybe "} {
		if strings.Contains(" "+lower+" ", token) {
			return true
		}
	}
	return false
}

func projectBehavioralTestObligations(obligations []TestObligationCoverage) []TDDObligation {
	out := make([]TDDObligation, 0, len(obligations))
	for _, obligation := range obligations {
		out = append(out, TDDObligation{
			ID:             strings.TrimSpace(obligation.ID),
			Type:           strings.TrimSpace(obligation.Type),
			Behavior:       strings.TrimSpace(obligation.Behavior),
			Owner:          strings.TrimSpace(obligation.Owner),
			Command:        strings.TrimSpace(obligation.Command),
			RequiredBefore: strings.TrimSpace(obligation.RequiredBefore),
			Status:         "planned",
		})
	}

	return out
}

func bddScenarioCoversRequirement(scenarios []BDDScenarioCoverage, requirementID string) bool {
	requirementID = strings.TrimSpace(requirementID)
	if requirementID == "" {
		return false
	}
	for _, scenario := range scenarios {
		if BehavioralCoverageStatusIsAccepted(scenario.Status) && strings.TrimSpace(scenario.RequirementID) == requirementID {
			return true
		}
	}

	return false
}

func allDesignContractsComplete(contracts []DesignContractCoverage) bool {
	accepted := 0
	for _, contract := range contracts {
		if !BehavioralCoverageStatusIsAccepted(contract.Status) {
			continue
		}
		accepted++
		if len(nonEmptyStrings(contract.Preconditions)) == 0 ||
			len(nonEmptyStrings(contract.Postconditions)) == 0 ||
			len(nonEmptyStrings(contract.Invariants)) == 0 ||
			len(nonEmptyStrings(contract.TestRefs)) == 0 {
			return false
		}
	}
	return accepted > 0
}

func decisionTableCoverageComplete(table DecisionTableCoverage) bool {
	if len(nonEmptyStrings(table.Conditions)) == 0 || len(table.Rules) == 0 || len(nonEmptyStrings(table.TestRefs)) == 0 {
		return false
	}
	for _, rule := range table.Rules {
		if len(rule.Conditions) == 0 || strings.TrimSpace(rule.Expected) == "" {
			return false
		}
	}
	return true
}

func fuzzTargetCoverageComplete(target FuzzTargetCoverage) bool {
	return strings.TrimSpace(target.Target) != "" &&
		len(nonEmptyStrings(target.Generators)) > 0 &&
		len(nonEmptyStrings(target.Invariants)) > 0 &&
		len(nonEmptyStrings(target.TestRefs)) > 0
}

func ragEvaluationCoverageComplete(eval RAGEvaluationCoverage) bool {
	return len(nonEmptyStrings(eval.DatasetRefs)) > 0 &&
		len(eval.Metrics) > 0 &&
		len(eval.Thresholds) > 0 &&
		len(nonEmptyStrings(eval.EvalRefs)) > 0
}

func securityCaseCoverageComplete(securityCase SecurityCaseCoverage) bool {
	return strings.TrimSpace(securityCase.Risk) != "" &&
		strings.TrimSpace(securityCase.Attack) != "" &&
		strings.TrimSpace(securityCase.Expected) != "" &&
		len(nonEmptyStrings(securityCase.TestRefs)) > 0
}

func privacyControlCoverageComplete(privacy PrivacyControlCoverage) bool {
	return strings.TrimSpace(privacy.DataClass) != "" &&
		strings.TrimSpace(privacy.Policy) != "" &&
		len(nonEmptyStrings(privacy.RedactionRefs)) > 0 &&
		strings.TrimSpace(privacy.Retention) != "" &&
		len(nonEmptyStrings(privacy.TestRefs)) > 0
}

func providerContractCoverageComplete(provider ProviderContractCoverage) bool {
	return strings.TrimSpace(provider.Provider) != "" &&
		strings.TrimSpace(provider.Consumer) != "" &&
		len(nonEmptyStrings(provider.ContractRefs)) > 0 &&
		len(nonEmptyStrings(provider.TestRefs)) > 0
}

func observabilityCoverageComplete(signal ObservabilityCoverage) bool {
	return strings.TrimSpace(signal.Signal) != "" &&
		strings.TrimSpace(signal.InstrumentedPath) != "" &&
		len(nonEmptyStrings(signal.RequiredFields)) > 0 &&
		len(nonEmptyStrings(signal.TestRefs)) > 0
}

func performanceBoundCoverageComplete(bound PerformanceBoundCoverage) bool {
	return strings.TrimSpace(bound.Operation) != "" &&
		strings.TrimSpace(bound.Bound) != "" &&
		len(nonEmptyStrings(bound.MeasurementRefs)) > 0
}

func e2eJourneyCoverageComplete(journey E2EJourneyCoverage) bool {
	return strings.TrimSpace(journey.Journey) != "" &&
		len(nonEmptyStrings(journey.Steps)) > 0 &&
		len(nonEmptyStrings(journey.ScriptRefs)) > 0
}

func allBehavioralSurfacesDecided(surfaces []BehavioralSurfaceCoverage) bool {
	if len(validateBehavioralSurfaceInventoryConsistency(surfaces)) > 0 {
		return false
	}
	for _, surface := range surfaces {
		status := NormalizeBehavioralSurfaceStatus(surface.Status)
		if status == "" || status == "open_question" {
			return false
		}
		if status == BehavioralCoverageStatusAccepted && len(nonEmptyStrings(surface.Obligations)) == 0 {
			return false
		}
	}
	return true
}

func validateBehavioralCoverageItem(prefix string, id string, status string, complete bool, missingDetail string) []error {
	var errs []error

	if strings.TrimSpace(id) == "" {
		errs = append(errs, fmt.Errorf("%s missing id", prefix))
	}

	normalizedStatus := normalizeBehavioralCoverageStatus(status)
	if !validBehavioralCoverageStatus(normalizedStatus) {
		errs = append(errs, fmt.Errorf("%s invalid status %q", prefix, status))
		return errs
	}

	if normalizedStatus == BehavioralCoverageStatusAccepted && !complete {
		errs = append(errs, fmt.Errorf("%s accepted coverage %s", prefix, missingDetail))
	}

	return errs
}

func validateStateMachineCoverage(prefix string, machine StateMachineCoverage) []error {
	errs := validateBehavioralCoverageItem(prefix, machine.ID, machine.Status, stateMachineCoverageComplete(machine), "missing covered transitions")
	if normalizeBehavioralCoverageStatus(machine.Status) != BehavioralCoverageStatusAccepted {
		return errs
	}

	if len(nonEmptyStrings(machine.States)) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing states", prefix))
	}

	if len(machine.Transitions) == 0 {
		errs = append(errs, fmt.Errorf("%s accepted coverage missing transitions", prefix))
	}

	for i, transition := range machine.Transitions {
		transitionPrefix := fmt.Sprintf("%s transitions[%d]", prefix, i)
		if strings.TrimSpace(transition.ID) == "" {
			errs = append(errs, fmt.Errorf("%s missing id", transitionPrefix))
		}
		if strings.TrimSpace(transition.From) == "" {
			errs = append(errs, fmt.Errorf("%s missing from", transitionPrefix))
		}
		if strings.TrimSpace(transition.To) == "" {
			errs = append(errs, fmt.Errorf("%s missing to", transitionPrefix))
		}
		if strings.TrimSpace(transition.Event) == "" {
			errs = append(errs, fmt.Errorf("%s missing event", transitionPrefix))
		}
		if len(nonEmptyStrings(transition.TestRefs)) == 0 {
			errs = append(errs, fmt.Errorf("%s missing test_refs", transitionPrefix))
		}
	}

	return errs
}

func validateBehavioralMutationCoverage(mutation BehavioralMutationCoverage) []error {
	var errs []error

	requirement := normalizeBehavioralMutationRequirement(mutation.Requirement)
	if requirement == "" {
		requirement = BehavioralMutationRequirementNotRequired
	}

	switch requirement {
	case BehavioralMutationRequirementRequired:
		if mutation.MinScore <= 0 {
			errs = append(errs, errors.New("behavioral coverage mutation required min_score must be positive"))
		}
		if mutation.Score == nil {
			errs = append(errs, errors.New("behavioral coverage mutation required score is missing"))
		}
	case BehavioralMutationRequirementWaived:
		if strings.TrimSpace(mutation.WaiverReason) == "" {
			errs = append(errs, errors.New("behavioral coverage mutation waiver_reason is required when waived"))
		}
	case BehavioralMutationRequirementNotRequired:
	default:
		errs = append(errs, fmt.Errorf("behavioral coverage mutation invalid requirement %q", mutation.Requirement))
	}

	if mutation.Score != nil && (*mutation.Score < 0 || *mutation.Score > 100) {
		errs = append(errs, fmt.Errorf("behavioral coverage mutation score must be 0..100, got %.1f", *mutation.Score))
	}

	return errs
}

func projectMutationCoverage(mutation BehavioralMutationCoverage, coverage *TDDCoverage) {
	if coverage == nil {
		return
	}

	switch normalizeBehavioralMutationRequirement(mutation.Requirement) {
	case BehavioralMutationRequirementRequired:
		coverage.MutationMinScore = mutation.MinScore
		coverage.MutationScore = mutation.Score
		if mutation.Score == nil || *mutation.Score < float64(mutation.MinScore) {
			coverage.GatesBlocked = appendUniqueString(coverage.GatesBlocked, "mutation")
		}
	case BehavioralMutationRequirementWaived:
		coverage.MutationMinScore = mutation.MinScore
		coverage.MutationScore = mutation.Score
		coverage.GatesBlocked = appendUniqueString(coverage.GatesBlocked, "mutation_unavailable_tool")
	case BehavioralMutationRequirementNotRequired, "":
		coverage.MutationScore = mutation.Score
	}
}

func stateMachineCoverageComplete(machine StateMachineCoverage) bool {
	if len(nonEmptyStrings(machine.States)) == 0 || len(machine.Transitions) == 0 {
		return false
	}

	for _, transition := range machine.Transitions {
		if strings.TrimSpace(transition.ID) == "" ||
			strings.TrimSpace(transition.From) == "" ||
			strings.TrimSpace(transition.To) == "" ||
			strings.TrimSpace(transition.Event) == "" ||
			len(nonEmptyStrings(transition.TestRefs)) == 0 {

			return false
		}
	}

	return true
}

func BehavioralCoverageStatusIsAccepted(status string) bool {
	return normalizeBehavioralCoverageStatus(status) == BehavioralCoverageStatusAccepted
}

func BehavioralCoverageSurfaceStatusIsAccepted(status string) bool {
	return NormalizeBehavioralSurfaceStatus(status) == BehavioralCoverageStatusAccepted
}

func BehavioralCoverageSurfaceStatusIsClosedDecision(status string) bool {
	switch NormalizeBehavioralSurfaceStatus(status) {
	case BehavioralCoverageStatusAccepted, BehavioralCoverageStatusWaived, BehavioralCoverageStatusNotRequired:
		return true
	default:
		return false
	}
}

func NormalizeBehavioralSurfaceStatus(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "accepted", "covered", "tested", "in_scope", "required":
		return BehavioralCoverageStatusAccepted
	case "":
		return ""
	case BehavioralCoverageStatusWaived:
		return BehavioralCoverageStatusWaived
	case BehavioralCoverageStatusNotRequired, "out_of_scope":
		return BehavioralCoverageStatusNotRequired
	case "open_question", "question_open", "unknown":
		return "open_question"
	default:
		return strings.TrimSpace(strings.ToLower(status))
	}
}

func normalizeBehavioralCoverageStatus(status string) string {
	trimmed := strings.TrimSpace(strings.ToLower(status))
	if trimmed == "" {
		return BehavioralCoverageStatusAccepted
	}

	return trimmed
}

func validBehavioralCoverageStatus(status string) bool {
	switch status {
	case BehavioralCoverageStatusAccepted, BehavioralCoverageStatusWaived, BehavioralCoverageStatusNotRequired:
		return true
	default:
		return false
	}
}

func normalizeBehavioralMutationRequirement(requirement string) string {
	return strings.TrimSpace(strings.ToLower(requirement))
}

func sameOptionalFloat64(left *float64, right *float64) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}
