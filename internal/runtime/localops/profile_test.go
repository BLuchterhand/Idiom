package localops

import (
	"idiom/internal/runtime/readiness"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Local profile reports", func() {
	It("summarizes readiness gates and capacity blockers", func() {
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

		report := BuildProfileReport(profile)
		Expect(report.Smoke.Status).To(Equal("blocked"))
		Expect(report.Smoke.Passed).To(Equal(1))
		Expect(report.Smoke.Total).To(Equal(2))
		Expect(report.Capacity.Status).To(Equal("blocked"))
		Expect(report.Capacity.Blockers).NotTo(BeEmpty())
		Expect(report.Readiness.HardFailures).To(Equal(1))
		Expect(report.Readiness.ContextBudgetState).To(Equal("blocked"))
	})

	It("counts benchmark profile outcomes", func() {
		report := BenchmarkReport{
			Profiles: []ProfileReport{
				{Smoke: SmokeReport{Passed: 1, Total: 2, Degradations: []Blocker{{Scope: "a"}}}, Capacity: CapacityReport{Status: "blocked"}},
				{Smoke: SmokeReport{Passed: 2, Total: 2}, Capacity: CapacityReport{Status: "pass"}},
			},
			Replay:   ReplayReport{LogCount: 3},
			Blockers: []Blocker{{Scope: "b"}},
		}

		summary := SummarizeBenchmark(report)
		Expect(summary.ProfileCount).To(Equal(2))
		Expect(summary.SmokePassed).To(Equal(3))
		Expect(summary.SmokeTotal).To(Equal(4))
		Expect(summary.CapacityBlocked).To(Equal(1))
		Expect(summary.ReplayLogCount).To(Equal(3))
		Expect(summary.BlockerCount).To(Equal(1))
		Expect(summary.SmokePassRate).To(Equal(0.75))
	})
})
