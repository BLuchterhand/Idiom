package eval

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("build failing scenario suite copies failed tasks with records", func() {
	source := SWESuite{
		Name:    "source suite",
		Version: 2,
		Tasks: []SWETask{
			{
				ID:      "failed",
				Family:  "behavior",
				Prompt:  "original prompt",
				Turns:   []string{"turn one", "turn two"},
				Tags:    []string{"existing"},
				Fixture: "fixture-repo",
				Oracle:  SWEOracle{RequireNoShell: true},
			},
			{ID: "passed", Family: "behavior", Prompt: "passed prompt"},
		},
	}
	report := SWESuiteReport{
		Tasks: []SWETaskReport{
			{
				TaskID:   "failed",
				Family:   "fallback-family",
				Passed:   false,
				Findings: []string{"missing grounded answer"},
				Metrics:  Metrics{LastUser: "latest user prompt"},
			},
			{TaskID: "passed", Passed: true},
			{TaskID: "no-records", Passed: false},
		},
	}
	recordsByTask := map[string][]Record{
		"failed": {
			{Type: "message", Data: map[string]any{"role": "user", "content": "latest user prompt"}},
			{Type: "tool_request", Data: map[string]any{"step": float64(1), "tool": "read_file", "path": "internal/evidence/eval/regression.go"}},
			{Type: "tool_result", Data: map[string]any{"step": float64(1), "tool": "read_file", "path": "internal/evidence/eval/regression.go", "summary": "Read regression.go"}},
		},
	}

	got := BuildFailingScenarioSuite("", source, report, recordsByTask)
	gomega.Expect(got.Name).To(gomega.BeEquivalentTo("source suite generated regressions"), fmt.Sprintf("unexpected suite identity: %#v", got))
	gomega.Expect(got.Version).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected suite identity: %#v", got))

	gomega.Expect(got.Tasks).To(gomega.HaveLen(1), fmt.Sprintf("expected one generated regression task, got %#v", got.Tasks))

	task := got.Tasks[0]
	gomega.Expect(task.ID).To(gomega.BeEquivalentTo("failed-regression"), fmt.Sprintf("unexpected generated task identity: %#v", task))
	gomega.Expect(task.Family).To(gomega.BeEquivalentTo("behavior"), fmt.Sprintf("unexpected generated task identity: %#v", task))
	gomega.Expect(task.Prompt).To(gomega.BeEquivalentTo("latest user prompt"), fmt.Sprintf("unexpected generated task identity: %#v", task))

	gomega.Expect(task.Turns).To(gomega.HaveLen(2), fmt.Sprintf("expected source task fields to be preserved: %#v", task))
	gomega.Expect(task.Fixture).To(gomega.BeEquivalentTo("fixture-repo"), fmt.Sprintf("expected source task fields to be preserved: %#v", task))
	gomega.Expect(task.Oracle.RequireNoShell).To(gomega.BeTrue(), fmt.Sprintf("expected source task fields to be preserved: %#v", task))

	gomega.Expect(containsStringFold(task.Tags, "existing")).To(gomega.BeTrue(), fmt.Sprintf("expected source and generated tags, got %#v", task.Tags))
	gomega.Expect(containsStringFold(task.Tags, "generated-regression")).To(gomega.BeTrue(), fmt.Sprintf("expected source and generated tags, got %#v", task.Tags))

	gomega.Expect(task.Scenario.Name).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected generated scenario from records, got %#v", task.Scenario))
	gomega.Expect(containsStringFold(task.Scenario.RequireTools, "read_file")).To(gomega.BeTrue(), fmt.Sprintf("expected generated scenario from records, got %#v", task.Scenario))
})
var _ = ginkgo.It("non empty strings drops whitespace", func() {
	got := nonEmptyStrings([]string{"", " keep ", "\t", "also-keep"})
	gomega.Expect(got).To(gomega.HaveLen(2), fmt.Sprintf("nonEmptyStrings = %#v, want non-empty original values", got))
	gomega.Expect(got[0]).To(gomega.BeEquivalentTo(" keep "), fmt.Sprintf("nonEmptyStrings = %#v, want non-empty original values", got))
	gomega.Expect(got[1]).To(gomega.BeEquivalentTo("also-keep"), fmt.Sprintf("nonEmptyStrings = %#v, want non-empty original values", got))
})
