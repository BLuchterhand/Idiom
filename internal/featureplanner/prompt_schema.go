package featureplanner

import "fmt"

type submitFeaturePlanShapeOptions struct {
	CoverageTier     string
	PRDRefs          string
	ADRRefs          string
	Acceptance       string
	Command          string
	RequiredPath     string
	AllowedPath      string
	TestArtifactPath string
}

func submitFeaturePlanShape(opts submitFeaturePlanShapeOptions) string {
	return fmt.Sprintf("{\"tool\":\"submit_feature_plan\",\"schema_version\":\"idiom.feature_plan.v2\",\"feature_id\":\"feature-id\",\"source_task\":{\"kind\":\"operator_prompt\",\"summary\":\"...\"},\"summary\":\"...\",\"top_level_scope\":{\"objective\":\"...\",\"success_criteria\":[\"...\"]},\"sdd_contract\":{\"prd_requirement_refs\":%s,\"adr_refs\":%s,\"acceptance\":%s},\"tdd_contract\":{\"coverage_tier\":\"%s\",\"required_before_implementation\":true,\"obligations\":[{\"id\":\"TDD-1\",\"behavior\":\"...\",\"command\":\"%s\",\"required_before\":\"implementation\"}],\"coverage_gate_event_id\":\"evt-cov-1\"},\"atoms\":[{\"schema_version\":\"idiom.feature_atom.v1\",\"id\":\"atom-1\",\"title\":\"...\",\"summary\":\"...\",\"required_edit_paths\":[\"%s\"],\"allowed_edit_paths\":[\"%s\"],\"sdd_refs\":[\"REQ-001\"],\"tdd_obligations\":[\"TDD-1\"],\"required_tests\":[\"%s\"],\"test_first\":{\"obligation_refs\":[\"TDD-1\"],\"commands\":[\"%s\"],\"baseline_required\":true,\"test_artifact_paths\":[\"%s\"],\"pre_execution_event_ids\":[\"evt-tdd-1\"],\"expected_pre_execution_result\":\"red-confirmed\",\"coverage_gate_event_id\":\"evt-tdd-1\"},\"planned_tdd_snapshot\":{\"snapshot_id\":\"snap-tdd-1\",\"snapshot_digest\":\"sha256:...\",\"obligation_ids\":[\"TDD-1\"],\"test_commands\":[\"%s\"],\"test_artifact_paths\":[\"%s\"],\"expected_pre_execution_results\":[\"red-confirmed\"],\"created_before_dispatch_event_id\":\"evt-tdd-1\"},\"prompt\":\"...\"}]}\n",
		opts.PRDRefs,
		opts.ADRRefs,
		opts.Acceptance,
		opts.CoverageTier,
		opts.Command,
		opts.RequiredPath,
		opts.AllowedPath,
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
	})
}

func standaloneSubmitFeaturePlanShape() string {
	return submitFeaturePlanShape(submitFeaturePlanShapeOptions{
		CoverageTier:     "contract_high",
		PRDRefs:          "[]",
		ADRRefs:          "[]",
		Acceptance:       "[\"your-acceptance-criteria-here\"]",
		Command:          "project-specific-test-command",
		RequiredPath:     "project/source-file.ext",
		AllowedPath:      "project/test-file.ext",
		TestArtifactPath: "project/test-file.ext",
	})
}
