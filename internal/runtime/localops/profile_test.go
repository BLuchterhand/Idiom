package localops

import (
	"testing"

	"idiom/internal/runtime/readiness"
)

func TestBuildProfileReportSummarizesReadinessGates(t *testing.T) {
	profile := readiness.ProfileReport{
		Name:              "implement",
		State:             "blocked",
		Enforcement:       "report_only",
		RequiredForLaunch: true,
		TaskFamily:        "implementation",
		Contract:          "mutation",
		PermissionProfile: "workspace",
		Gates: []readiness.GateDecision{
			{Name: "backend_roles_resolved", Status: "pass"},
			{Name: "context_budget_valid", Status: "blocked", Reason: "ctx_size=0", Remedy: "set ctx"},
		},
		HardFailures: []readiness.GateDecision{{Name: "context_budget_valid"}},
		Metrics: readiness.Metrics{
			ContextBudgetState: "blocked",
			QueueState:         "pass",
			ContextTokens:      0,
			MaxOutputTokens:    512,
		},
		Checks: []readiness.Check{{Name: "context budget", Remedy: "set ctx"}},
	}

	got := BuildProfileReport(profile)
	if got.Smoke.Status != "blocked" || got.Smoke.Passed != 1 || got.Smoke.Total != 2 {
		t.Fatalf("unexpected smoke report: %#v", got.Smoke)
	}
	if got.Capacity.Status != "blocked" || len(got.Capacity.Blockers) == 0 {
		t.Fatalf("expected capacity blocker, got %#v", got.Capacity)
	}
	if got.Readiness.HardFailures != 1 || got.Readiness.ContextBudgetState != "blocked" {
		t.Fatalf("readiness evidence missing: %#v", got.Readiness)
	}
}

func TestSummarizeBenchmarkCountsProfileOutcomes(t *testing.T) {
	report := BenchmarkReport{
		Profiles: []ProfileReport{
			{Smoke: SmokeReport{Passed: 1, Total: 2, Degradations: []Blocker{{Scope: "a"}}}, Capacity: CapacityReport{Status: "blocked"}},
			{Smoke: SmokeReport{Passed: 2, Total: 2}, Capacity: CapacityReport{Status: "pass"}},
		},
		Replay:   ReplayReport{LogCount: 3},
		Blockers: []Blocker{{Scope: "b"}},
	}

	got := SummarizeBenchmark(report)
	if got.ProfileCount != 2 || got.SmokePassed != 3 || got.SmokeTotal != 4 || got.CapacityBlocked != 1 || got.ReplayLogCount != 3 || got.BlockerCount != 1 {
		t.Fatalf("unexpected summary: %#v", got)
	}
	if got.SmokePassRate != 0.75 {
		t.Fatalf("unexpected pass rate: %v", got.SmokePassRate)
	}
}
