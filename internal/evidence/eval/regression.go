package eval

import (
	"strings"

	"idiom/internal/platform/textutil"
)

func BuildFailingScenarioSuite(name string, source SWESuite, report SWESuiteReport, recordsByTask map[string][]Record) SWESuite {
	name = strings.TrimSpace(name)
	if name == "" {
		name = source.Name + " generated regressions"
	}

	out := SWESuite{
		Name:    name,
		Version: source.Version,
		Tasks:   make([]SWETask, 0),
	}
	sourceByID := make(map[string]SWETask, len(source.Tasks))

	for _, task := range source.Tasks {
		sourceByID[strings.TrimSpace(task.ID)] = task
	}

	for _, taskReport := range report.Tasks {
		if taskReport.Passed {
			continue
		}

		id := strings.TrimSpace(taskReport.TaskID)
		records := recordsByTask[id]

		if len(records) == 0 {
			continue
		}

		sourceTask := sourceByID[id]
		scenario := BuildSuggestedScenario(id+"-regression", records)
		prompt := strings.TrimSpace(taskReport.Metrics.LastUser)

		if prompt == "" {
			prompt = strings.TrimSpace(sourceTask.Prompt)
		}

		if prompt == "" && len(sourceTask.Turns) > 0 {
			prompt = strings.TrimSpace(sourceTask.Turns[len(sourceTask.Turns)-1])
		}

		tags := textutil.UniqueNonEmpty(append(append([]string(nil), sourceTask.Tags...), "generated-regression"))

		out.Tasks = append(out.Tasks, SWETask{
			ID:       id + "-regression",
			Family:   textutil.FirstNonEmpty(sourceTask.Family, taskReport.Family, "regression"),
			Prompt:   prompt,
			Turns:    append([]string(nil), sourceTask.Turns...),
			Tags:     tags,
			Fixture:  sourceTask.Fixture,
			Scenario: scenario,
			Oracle:   sourceTask.Oracle,
		})
	}

	return out
}
