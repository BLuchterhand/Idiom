package eval

import "testing"

func TestBuildFailingScenarioSuiteCopiesFailedTasksWithRecords(t *testing.T) {
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
	if got.Name != "source suite generated regressions" || got.Version != 2 {
		t.Fatalf("unexpected suite identity: %#v", got)
	}

	if len(got.Tasks) != 1 {
		t.Fatalf("expected one generated regression task, got %#v", got.Tasks)
	}

	task := got.Tasks[0]
	if task.ID != "failed-regression" || task.Family != "behavior" || task.Prompt != "latest user prompt" {
		t.Fatalf("unexpected generated task identity: %#v", task)
	}

	if len(task.Turns) != 2 || task.Fixture != "fixture-repo" || !task.Oracle.RequireNoShell {
		t.Fatalf("expected source task fields to be preserved: %#v", task)
	}

	if !containsStringFold(task.Tags, "existing") || !containsStringFold(task.Tags, "generated-regression") {
		t.Fatalf("expected source and generated tags, got %#v", task.Tags)
	}

	if task.Scenario.Name == "" || !containsStringFold(task.Scenario.RequireTools, "read_file") {
		t.Fatalf("expected generated scenario from records, got %#v", task.Scenario)
	}
}

func TestNonEmptyStringsDropsWhitespace(t *testing.T) {
	got := nonEmptyStrings([]string{"", " keep ", "\t", "also-keep"})
	if len(got) != 2 || got[0] != " keep " || got[1] != "also-keep" {
		t.Fatalf("nonEmptyStrings = %#v, want non-empty original values", got)
	}
}
