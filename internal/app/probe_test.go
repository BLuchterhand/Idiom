package app

import (
	"strings"
	"testing"

	"idiom/internal/agent"
)

// TestProbeLaneImplementationFamilyMatchesContract is the canonical
// happy path: a synthetic implementation brief should normalize to
// initial=implement, contract=implementation_result, brief
// task_family="implementation", and reason="explicit_task_family".
// Failing this test means the lane wrapper would route the same
// brief somewhere read-only, which is the exact regression the
// Colony probe used to catch.
func TestProbeLaneImplementationFamilyMatchesContract(t *testing.T) {
	res, err := ProbeLane(ProbeLaneOptions{
		Mode:         agent.BuildMode(),
		TaskFamily:   "implementation",
		AllowedTools: []string{"read_file", "write_file"},
	})
	if err != nil {
		t.Fatalf("ProbeLane: %v", err)
	}
	if res.InitialLane != "implement" {
		t.Fatalf("initial_lane should be 'implement', got %q", res.InitialLane)
	}
	if res.Contract != "implementation_result" {
		t.Fatalf("contract should be implementation_result, got %q", res.Contract)
	}
	if res.BriefTaskFam != "implementation" {
		t.Fatalf("brief.task_family should be implementation, got %q", res.BriefTaskFam)
	}
	if res.Reason != "explicit_task_family" {
		t.Fatalf("reason should be explicit_task_family, got %q", res.Reason)
	}
	if res.AllowedToolsN == 0 {
		t.Fatalf("allowed_tools should not be empty")
	}
}

// TestProbeLanePlanThenExecutePlannerMatchesContract guards the
// plan-execute planner phase: same lane shape (write-capable) but
// brief.task_family is the planner-specific value so the runtime
// guard surfaces submit_plan as the only write tool. Without this
// case, the planner falls through to fallbackTurnRoute and ends up
// with zero tools — the literal "lane has no submit_plan" bug
// that motivated the planner-specific case in normalizeTurnRoutePlan.
func TestProbeLanePlanThenExecutePlannerMatchesContract(t *testing.T) {
	res, err := ProbeLane(ProbeLaneOptions{
		Mode:         agent.BuildMode(),
		TaskFamily:   "plan_then_execute_planner",
		AllowedTools: []string{"read_file", "submit_plan"},
	})
	if err != nil {
		t.Fatalf("ProbeLane: %v", err)
	}
	if res.InitialLane != "implement" {
		t.Fatalf("planner phase should run on the implement lane shape, got initial_lane=%q", res.InitialLane)
	}
	if res.Contract != "implementation_result" {
		t.Fatalf("planner contract should be implementation_result, got %q", res.Contract)
	}
	if res.BriefTaskFam != "plan_then_execute_planner" {
		t.Fatalf("brief.task_family should be plan_then_execute_planner, got %q", res.BriefTaskFam)
	}
}

// TestProbeLaneRejectsEmptyTaskFamily verifies the input contract:
// an empty TaskFamily can't be probed because the lane wrapper
// would never set explicit_task_family without a value. The probe
// surfaces this as an input error rather than silently returning
// fallback shape.
func TestProbeLaneRejectsEmptyTaskFamily(t *testing.T) {
	_, err := ProbeLane(ProbeLaneOptions{Mode: agent.BuildMode()})
	if err == nil || !strings.Contains(err.Error(), "TaskFamily") {
		t.Fatalf("empty TaskFamily must error; got %v", err)
	}
}

// TestProbeLaneDefaultsModeToBuild verifies the convenience: a
// caller that doesn't pass a Mode gets BuildMode() as the default.
// Important because most external probe callers won't have an
// agent.Mode handy and the routing should still work.
func TestProbeLaneDefaultsModeToBuild(t *testing.T) {
	res, err := ProbeLane(ProbeLaneOptions{
		TaskFamily:   "implementation",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("ProbeLane: %v", err)
	}
	if res.Contract != "implementation_result" {
		t.Fatalf("default mode should still resolve to implementation_result; got %q", res.Contract)
	}
}

// TestFormatProbeResultIsSingleLineHumanReadable verifies the
// pretty-printer keeps the output operators are used to grepping
// for. One line with key=value pairs; empty fields collapse to "-"
// rather than leaving holes.
func TestFormatProbeResultIsSingleLineHumanReadable(t *testing.T) {
	r := ProbeLaneResult{
		InitialLane:   "implement",
		Reason:        "explicit_task_family",
		Contract:      "implementation_result",
		BriefTaskFam:  "implementation",
		AllowedToolsN: 12,
	}
	got := FormatProbeResult("PASS implement", r)
	if strings.Count(got, "\n") > 0 {
		t.Fatalf("FormatProbeResult must be single-line: %q", got)
	}
	for _, want := range []string{"initial=implement", "contract=implementation_result", "tools=12", "followup=-"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q; got %q", want, got)
		}
	}
}
