package productworkflow

type CoverageGateEvaluation struct {
	EffortID     string                  `json:"effort_id"`
	TaskID       string                  `json:"task_id"`
	CoverageTier string                  `json:"coverage_tier"`
	Status       ProductWorkflowStatus   `json:"status"`
	Criteria     []CoverageGateCriterion `json:"criteria"`
	Findings     []CoverageGateFinding   `json:"findings"`
	Blockers     []CoverageGateBlocker   `json:"blockers"`
}

type CoverageGateEvaluatedEvent struct {
	Event          string                    `json:"event"`
	EffortID       string                    `json:"effort_id"`
	TaskID         string                    `json:"task_id"`
	Coverage       CoverageGateEventCoverage `json:"coverage"`
	GateResult     string                    `json:"gate_result"`
	BlockedReasons []string                  `json:"blocked_reasons"`
	Waivers        []string                  `json:"waivers"`
}

type CoverageGateEventCoverage struct {
	EARSCoveragePct       *float64 `json:"ears_coverage_pct"`
	BDDCoveragePct        *float64 `json:"bdd_coverage_pct"`
	FMEAMitigationPct     *float64 `json:"fmea_mitigation_pct"`
	HAZOPTestedPct        *float64 `json:"hazop_tested_pct"`
	InvariantPBTPct       *float64 `json:"invariant_pbt_pct"`
	StateMachinePct       *float64 `json:"state_machine_pct"`
	DecisionTablePct      *float64 `json:"decision_table_pct"`
	BoundaryValuePct      *float64 `json:"boundary_value_pct"`
	FaultTreeCoveragePct  *float64 `json:"fault_tree_coverage_pct"`
	PostMortemCoveragePct *float64 `json:"post_mortem_coverage_pct"`
	FormalVerificationPct *float64 `json:"formal_verification_pct"`
	FuzzCoveragePct       *float64 `json:"fuzz_coverage_pct"`
	RAGEvalCoveragePct    *float64 `json:"rag_eval_coverage_pct"`
	SecurityCoveragePct   *float64 `json:"security_coverage_pct"`
	PrivacyCoveragePct    *float64 `json:"privacy_coverage_pct"`
	ProviderContractPct   *float64 `json:"provider_contract_pct"`
	ObservabilityPct      *float64 `json:"observability_pct"`
	PerformancePct        *float64 `json:"performance_pct"`
	ScriptedE2EPct        *float64 `json:"scripted_e2e_pct"`
	MutationScore         *float64 `json:"mutation_score"`
}

type CoverageGateCriterion struct {
	Key      string                `json:"key"`
	Required bool                  `json:"required"`
	Status   ProductWorkflowStatus `json:"status"`
	Details  string                `json:"details,omitempty"`
}

type CoverageGateFinding struct {
	Kind      string `json:"kind"`
	Criterion string `json:"criterion"`
	Message   string `json:"message"`
	File      string `json:"file,omitempty"`
	Severity  string `json:"severity"`
}

type CoverageGateBlocker struct {
	Kind      string `json:"kind"`
	Criterion string `json:"criterion"`
	Message   string `json:"message"`
	File      string `json:"file,omitempty"`
}
