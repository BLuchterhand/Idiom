package routeprobe

import (
	"strings"
	"testing"

	"idiom/internal/contracts/autonomy"
	lanecontract "idiom/internal/contracts/lane"
	"idiom/internal/runtime/workerruntime"
)

func TestLaneImplementationFamilyMatchesContract(t *testing.T) {
	res, err := Lane(LaneOptions{
		TaskFamily:   string(autonomy.TaskFamilyImplementation),
		AllowedTools: []string{"read_file", "write_file"},
	})
	if err != nil {
		t.Fatalf("Lane: %v", err)
	}

	if res.InitialLane != string(lanecontract.LaneImplement) {
		t.Fatalf("initial_lane should be 'implement', got %q", res.InitialLane)
	}

	if res.Contract != string(workerruntime.ContractImplementationResult) {
		t.Fatalf("contract should be implementation_result, got %q", res.Contract)
	}

	if res.BriefTaskFam != string(autonomy.TaskFamilyImplementation) {
		t.Fatalf("brief.task_family should be implementation, got %q", res.BriefTaskFam)
	}

	if res.Reason != explicitTaskFamilyReason {
		t.Fatalf("reason should be explicit_task_family, got %q", res.Reason)
	}

	if res.AllowedToolsN == 0 {
		t.Fatalf("allowed_tools should not be empty")
	}
}

func TestLanePlanThenExecutePlannerMatchesContract(t *testing.T) {
	res, err := Lane(LaneOptions{
		TaskFamily:   string(autonomy.TaskFamilyPlanThenExecutePlanner),
		AllowedTools: []string{"read_file", "submit_plan"},
	})
	if err != nil {
		t.Fatalf("Lane: %v", err)
	}

	if res.InitialLane != string(lanecontract.LaneImplement) {
		t.Fatalf("planner phase should run on the implement lane shape, got initial_lane=%q", res.InitialLane)
	}

	if res.Contract != string(workerruntime.ContractImplementationResult) {
		t.Fatalf("planner contract should be implementation_result, got %q", res.Contract)
	}

	if res.BriefTaskFam != string(autonomy.TaskFamilyPlanThenExecutePlanner) {
		t.Fatalf("brief.task_family should be plan_then_execute_planner, got %q", res.BriefTaskFam)
	}
}

func TestLaneRejectsEmptyTaskFamily(t *testing.T) {
	_, err := Lane(LaneOptions{})
	if err == nil || !strings.Contains(err.Error(), "TaskFamily") {
		t.Fatalf("empty TaskFamily must error; got %v", err)
	}
}

func TestLaneDoesNotRequireMode(t *testing.T) {
	res, err := Lane(LaneOptions{
		TaskFamily:   string(autonomy.TaskFamilyImplementation),
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("Lane: %v", err)
	}

	if res.Contract != string(workerruntime.ContractImplementationResult) {
		t.Fatalf("default mode should still resolve to implementation_result; got %q", res.Contract)
	}
}

func TestFormatResultIsSingleLineHumanReadable(t *testing.T) {
	r := LaneResult{
		InitialLane:   string(lanecontract.LaneImplement),
		Reason:        explicitTaskFamilyReason,
		Contract:      string(workerruntime.ContractImplementationResult),
		BriefTaskFam:  string(autonomy.TaskFamilyImplementation),
		AllowedToolsN: 12,
	}
	got := FormatResult("PASS implement", r)

	if strings.Count(got, "\n") > 0 {
		t.Fatalf("FormatResult must be single-line: %q", got)
	}

	for _, want := range []string{"initial=implement", "contract=implementation_result", "tools=12", "followup=-"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q; got %q", want, got)
		}
	}
}
