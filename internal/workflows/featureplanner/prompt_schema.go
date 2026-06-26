package featureplanner

import "fmt"

const standaloneBehavioralCoverageDraftShape = `,"behavioral_coverage_draft":{"authority":"standalone_provisional","status":"draft","tier":"contract_high","requirements":[{"id":"REQ-1","pattern":"event-driven","statement":"When ... the system shall ...","refs":["SDD-ACCEPTANCE-1"],"tdd_obligations":["TDD-1"]}],"example_rules":[{"id":"RULE-1","requirement_id":"REQ-1","rule":"...","examples":[{"id":"EX-1","description":"normal case with concrete data","concrete_data":{"input":"...","expected":"..."},"concrete_data_refs":["fixtures/example-normal.json"]},{"id":"EX-2","description":"boundary case with concrete data","concrete_data":{"input":"...","expected":"..."},"concrete_data_refs":["fixtures/example-boundary.json"]}]}],"scenarios":[{"id":"BDD-1","requirement_id":"REQ-1","rule_id":"RULE-1","given":["concrete precondition"],"when":["specific action"],"then":["observable outcome"],"concrete_data":{"input":"...","expected":"..."},"concrete_data_refs":["fixtures/scenario.json"],"obligations":["TDD-1"],"tags":["mvp"]}],"invariants":[{"id":"INV-1","statement":"property that must always hold","property_based_test_refs":["TestPropertyAlwaysHolds"],"obligations":["TDD-1"]}],"failure_modes":[{"id":"FM-1","mode":"invalid or boundary condition","mitigation":"expected recovery","rpn":1,"mitigation_test_refs":["TestInvalidConditionRecovery"],"obligations":["TDD-1"]}],"state_machines":[{"id":"SM-1","states":["initial","handled"],"transitions":[{"id":"TR-1","from":"initial","event":"valid input","to":"handled","test_refs":["TestValidInputTransition"]}],"obligations":["TDD-1"]}],"surface_inventory":[{"id":"SURF-1","surface":"domain_logic","status":"in_scope","rationale":"core requested behavior","tdd_obligations":["TDD-1"]},{"id":"SURF-2","surface":"api_transport","status":"not_required","rationale":"no external transport requested for this increment"}],"test_obligations":[{"id":"TDD-1","type":"unit","behavior":"...","owner":"standalone feature planner","command":"project-specific-test-command","required_before":"implementation"}],"verification":[{"id":"VERIFY-1","gate":"required test command passes","command":"project-specific-test-command","obligations":["TDD-1"]}]}`

type submitFeaturePlanShapeOptions struct {
	CoverageTier     string
	PRDRefs          string
	ADRRefs          string
	Acceptance       string
	Command          string
	RequiredPath     string
	AllowedPath      string
	TestArtifactPath string
	SDDRef           string
	ExtraFields      string
}

func submitFeaturePlanShape(opts submitFeaturePlanShapeOptions) string {
	return fmt.Sprintf("{\"tool\":\"submit_feature_plan\",\"schema_version\":\"idiom.feature_plan.v2\",\"feature_id\":\"feature-id\",\"source_task\":{\"kind\":\"operator_prompt\",\"summary\":\"...\"},\"summary\":\"...\",\"top_level_scope\":{\"objective\":\"...\",\"success_criteria\":[\"...\"]},\"sdd_contract\":{\"prd_requirement_refs\":%s,\"adr_refs\":%s,\"acceptance\":%s},\"tdd_contract\":{\"coverage_tier\":\"%s\",\"required_before_implementation\":true,\"obligations\":[{\"id\":\"TDD-1\",\"behavior\":\"...\",\"command\":\"%s\",\"required_before\":\"implementation\"}],\"coverage_gate_event_id\":\"evt-cov-1\"}%s,\"atoms\":[{\"schema_version\":\"idiom.feature_atom.v1\",\"id\":\"atom-1\",\"title\":\"...\",\"summary\":\"...\",\"required_edit_paths\":[\"%s\"],\"allowed_edit_paths\":[\"%s\"],\"sdd_refs\":[\"%s\"],\"tdd_obligations\":[\"TDD-1\"],\"required_tests\":[\"%s\"],\"test_first\":{\"obligation_refs\":[\"TDD-1\"],\"commands\":[\"%s\"],\"baseline_required\":true,\"test_artifact_paths\":[\"%s\"],\"pre_execution_event_ids\":[\"evt-tdd-1\"],\"expected_pre_execution_result\":\"red-confirmed\",\"coverage_gate_event_id\":\"evt-tdd-1\"},\"planned_tdd_snapshot\":{\"obligation_ids\":[\"TDD-1\"],\"test_commands\":[\"%s\"],\"test_artifact_paths\":[\"%s\"],\"expected_pre_execution_results\":[\"red-confirmed\"],\"created_before_dispatch_event_id\":\"evt-tdd-1\"},\"prompt\":\"...\"}]}\n",
		opts.PRDRefs,
		opts.ADRRefs,
		opts.Acceptance,
		opts.CoverageTier,
		opts.Command,
		opts.ExtraFields,
		opts.RequiredPath,
		opts.AllowedPath,
		opts.SDDRef,
		opts.Command,
		opts.Command,
		opts.TestArtifactPath,
		opts.Command,
		opts.TestArtifactPath,
	)
}

func productSubmitFeaturePlanShape(coverageTier string) string {
	return submitFeaturePlanShape(submitFeaturePlanShapeOptions{
		CoverageTier:     coverageTier,
		PRDRefs:          "[\"REQ-001\"]",
		ADRRefs:          "[\"ADR-001\"]",
		Acceptance:       "[\"...\"]",
		Command:          "use-only-product-grounded-test-command",
		RequiredPath:     "use-only-product-grounded-source-or-required-tdd-test-paths",
		AllowedPath:      "use-only-product-grounded-optional-edit-paths",
		TestArtifactPath: "use-only-product-grounded-test-artifact-paths",
		SDDRef:           "REQ-001",
	})
}

func standaloneSubmitFeaturePlanShape() string {
	return submitFeaturePlanShape(submitFeaturePlanShapeOptions{
		CoverageTier:     "contract_high",
		PRDRefs:          "[]",
		ADRRefs:          "[]",
		Acceptance:       "[\"SDD-ACCEPTANCE-1: your acceptance criterion here\"]",
		Command:          "project-specific-test-command",
		RequiredPath:     "project/source-file.ext",
		AllowedPath:      "project/test-file.ext",
		TestArtifactPath: "project/test-file.ext",
		SDDRef:           "SDD-ACCEPTANCE-1",
		ExtraFields:      standaloneBehavioralCoverageDraftShape,
	})
}
