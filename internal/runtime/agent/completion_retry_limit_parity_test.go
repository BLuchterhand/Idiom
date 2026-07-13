package agent

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.It("keeps every completion retry context at its literal contract limit", func() {
	implementationBrief := func() workerruntime.WorkerRuntimeBrief {
		return workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		}
	}

	unscoped := newRunnerRunFixture(&Runner{RuntimeBrief: implementationBrief()})

	submitCorrectionBrief := implementationBrief()
	submitCorrectionBrief.TaskFamily = PlanThenExecutePlannerTaskFamily
	submitCorrectionBrief.RequiredEditPaths = []string{"a.go", "b.go"}
	submitCorrectionResults := []workspace.ToolResult{
		readFileSourceWindowResult("a.go", 1, 10, 10, "1:package a\n"),
		readFileSourceWindowResult("b.go", 1, 10, 10, "1:package b\n"),
		{Tool: workspace.ToolNameSubmitPlan, Summary: "submit_plan rejected: required edit path coverage mismatch", Error: "missing required edit path(s): `b.go`."},
	}
	submitCorrection := newRunnerRunFixture(&Runner{RuntimeBrief: submitCorrectionBrief})

	plannerBrief := submitCorrectionBrief
	plannerResults := []workspace.ToolResult{
		codeNavigateSourceWindowResult("a.go", 1, 10, 0, "route_summary: Read A in a.go lines 1-10\n\n1:func A() {}\n"),
		codeNavigateSourceWindowResult("b.go", 1, 10, 0, "route_summary: Read B in b.go lines 1-10\n\n1:func B() {}\n"),
	}
	planner := newRunnerRunFixture(&Runner{RuntimeBrief: plannerBrief})

	textualBrief := implementationBrief()
	textualBrief.RequiredEditPaths = []string{"a.go"}
	textual := newRunnerRunFixture(&Runner{
		Client:       llama.NewClient("http://example.invalid", time.Second).WithBackend(config.BackendMLX),
		RuntimeBrief: textualBrief,
	})

	mutationBrief := implementationBrief()
	mutationBrief.RequiredEditPaths = []string{"a.go"}
	mutation := newRunnerRunFixture(&Runner{RuntimeBrief: mutationBrief})

	readOnlyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
		Contract:          string(workerruntime.ContractDirectAnswer),
		PermissionProfile: "read_only_workspace",
		AllowedTools:      []string{workspace.ToolNameReadFile, workspace.ToolNameSearchContent},
	}
	readOnly := newRunnerRunFixture(&Runner{RuntimeBrief: readOnlyBrief})
	readOnlyWithoutBodyBrief := readOnlyBrief
	readOnlyWithoutBodyBrief.AllowedTools = []string{workspace.ToolNameSearchContent}
	readOnlyWithoutBody := newRunnerRunFixture(&Runner{RuntimeBrief: readOnlyWithoutBodyBrief})
	defaultRun := newRunnerRunFixture(&Runner{})

	groundingAssessment := sourceGroundingUnsupportedDetailReferenceAssessment(sourceReferencesForPaths([]string{"internal/runtime/workerruntime/catalog.go"}))
	groundingResult := annotateSourceGroundingAssessment(workspace.ToolResult{
		Tool:  completionGateToolName,
		Error: groundingAssessment.Message(),
	}, groundingAssessment)

	tests := []struct {
		name    string
		runner  *runExecution
		results []workspace.ToolResult
		active  *workspace.ToolResult
		tools   []string
		want    int
	}{
		{name: "unscoped no-write", runner: unscoped, results: []workspace.ToolResult{{Tool: workspace.ToolNameSearchContent, Summary: "No content matches"}}, want: 2},
		{name: "submit correction", runner: submitCorrection, results: submitCorrectionResults, want: 2},
		{name: "planner required paths without submit", runner: planner, results: plannerResults, want: 1},
		{name: "textual required path", runner: textual, want: 2},
		{name: "mutation recovery", runner: mutation, want: 1},
		{name: "read-only non-mutation", runner: readOnly, want: 3},
		{name: "default", runner: defaultRun, want: 2},
		{name: "read-only source grounding without body reads", runner: readOnlyWithoutBody, active: &groundingResult, tools: []string{workspace.ToolNameSearchContent}, want: 3},
		{name: "read-only source grounding with body reads", runner: readOnly, active: &groundingResult, tools: []string{workspace.ToolNameReadFile, workspace.ToolNameSearchContent}, want: 5},
	}

	for _, test := range tests {
		ginkgo.By(test.name)
		got := test.runner.completionGateRetryLimit(test.results)
		if test.active != nil {
			got = test.runner.completionGateActiveRetryLimit(*test.active, test.results, test.tools)
		}
		gomega.Expect(got).To(gomega.Equal(test.want), test.name)
	}
})
