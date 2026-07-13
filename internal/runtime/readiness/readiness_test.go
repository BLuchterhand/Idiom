package readiness

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Runtime readiness", func() {
	It("rejects an unknown profile as a selection error", func() {
		_, err := BuildProfiles(filepath.Join(GinkgoT().TempDir(), "missing-config.json"), "unknown", nil)
		Expect(err).To(HaveOccurred())
		Expect(IsProfileSelectionError(err)).To(BeTrue())
	})

	It("resolves a workspace root relative to the config directory", func() {
		dir := GinkgoT().TempDir()
		configDir := filepath.Join(dir, ".idiom")
		Expect(os.MkdirAll(configDir, 0o755)).To(Succeed())

		configPath := filepath.Join(configDir, "config.json")
		Expect(os.WriteFile(configPath, []byte(`{"workspace":{"root":".."}}`), 0o644)).To(Succeed())
		Expect(WorkspaceRootFromConfig(configPath)).To(Equal(filepath.Clean(dir)))
	})

	It("blocks unknown host check statuses", func() {
		checks := checksFromHost([]HostCheck{{
			Name:   "doctor typo",
			Status: HostCheckStatus("failed"),
			Detail: "raw host status drifted",
		}})

		Expect(checks).To(HaveLen(1))
		Expect(checks[0].State).To(Equal("blocked"))
		Expect(checks[0].Detail).To(ContainSubstring("unknown host check status"))
	})

	It("maps a missing backend role to typed launch-blocking metadata", func() {
		report := runtimeProfileForCheck("implement", Check{
			Name:   "backend role config",
			State:  "blocked",
			Detail: "missing role aliases: executor",
			Remedy: "add role tags or proxy.default_executor aliases in .idiom/backends.json",
		})

		gate := findReadinessGate(report, "backend_roles_resolved")
		Expect(gate.InvariantID).To(Equal("runtime.backend_roles_resolved"))
		Expect(gate.RuntimeDecision).To(Equal(RuntimeReadinessLaunchBlocked))
		Expect(gate.ReasonClass).To(Equal(ReasonMissingBackendRole))
		Expect(gate.Fallback).To(Equal(FallbackLegacyConfig))

		invariant := findRuntimeInvariant(report.RuntimeReadiness, "runtime.backend_roles_resolved")
		Expect(invariant.Decision).To(Equal(RuntimeReadinessLaunchBlocked))
		Expect(invariant.ReasonClass).To(Equal(ReasonMissingBackendRole))
		Expect(invariant.Fallback).To(Equal(FallbackLegacyConfig))
		Expect(report.RuntimeReadiness.Decision).To(Equal(RuntimeReadinessLaunchBlocked))
		Expect(report.RuntimeReadiness.LaunchBlocked).To(BeTrue())
	})

	It("maps an invalid context budget to a launch-blocking gate", func() {
		report := runtimeProfileForCheck("plan", Check{
			Name:   "context budget",
			State:  "degraded",
			Detail: "ctx_size=1024 max_output=2048 leaves no prompt budget",
			Remedy: "lower chat.max_tokens or raise the configured backend context",
		})

		gate := findReadinessGate(report, "context_budget_valid")
		Expect(gate.Status).To(Equal("blocked"))
		Expect(gate.RuntimeDecision).To(Equal(RuntimeReadinessLaunchBlocked))
		Expect(gate.ReasonClass).To(Equal(ReasonInvalidContextBudget))
		Expect(gate.Fallback).To(BeEmpty())
		Expect(report.RuntimeReadiness.Decision).To(Equal(RuntimeReadinessLaunchBlocked))
	})

	DescribeTable("maps degraded queue states without blocking launch",
		func(checkState string, reasonClass RuntimeReadinessReasonClass, fallback RuntimeReadinessFallback) {
			report := runtimeProfileForCheck("implement", Check{
				Name:   "queue admission",
				State:  checkState,
				Detail: "queue daemon not reachable",
				Remedy: "start the daemon with `idiom queue serve` or inspect queue status",
			})

			gate := findReadinessGate(report, "queue_admission_known")
			Expect(gate.Status).To(Equal("degraded"))
			Expect(gate.RuntimeDecision).To(Equal(RuntimeReadinessAllowedDegraded))
			Expect(gate.ReasonClass).To(Equal(reasonClass))
			Expect(gate.Fallback).To(Equal(fallback))
			Expect(report.RuntimeReadiness.Decision).To(Equal(RuntimeReadinessAllowedDegraded))
			Expect(report.RuntimeReadiness.LaunchBlocked).To(BeFalse())
			Expect(report.RuntimeReadiness.LaunchBlockingEnforced).To(BeFalse())
			Expect(report.RuntimeReadiness.Fallbacks).To(HaveLen(1))
			Expect(report.RuntimeReadiness.Fallbacks[0].Fallback).To(Equal(fallback))
		},
		Entry("queue unavailable", "unavailable", ReasonQueueUnavailable, FallbackQueueOpportunistic),
		Entry("queue softly degraded", "degraded", ReasonQueueSoftDegraded, FallbackQueueOperatorRepair),
	)

	It("preserves report-only enforcement for launch-blocking metadata", func() {
		report := runtimeProfileForCheck("eval", Check{
			Name:   "queue admission",
			State:  "unavailable",
			Detail: "queue daemon not reachable",
			Remedy: "start the daemon with `idiom queue serve`",
		})

		gate := findReadinessGate(report, "queue_admission_known")
		Expect(gate.Status).To(Equal("blocked"))
		Expect(gate.RuntimeDecision).To(Equal(RuntimeReadinessLaunchBlocked))
		Expect(report.Enforcement).To(Equal(string(ReadinessEnforcementReportOnly)))
		Expect(gate.Enforcement).To(Equal(string(ReadinessEnforcementReportOnly)))
		Expect(report.RuntimeReadiness.Enforcement).To(Equal(ReadinessEnforcementReportOnly))
		Expect(report.RuntimeReadiness.LaunchBlocked).To(BeTrue())
		Expect(report.RuntimeReadiness.LaunchBlockingEnforced).To(BeFalse())
	})
})

func runtimeProfileForCheck(profileName string, check Check) ProfileReport {
	report := ProfileReport{
		Name:              profileName,
		Enforcement:       string(ReadinessEnforcementReportOnly),
		RequiredForLaunch: true,
		Checks:            []Check{check},
	}

	report = applyGatePolicy(report)
	report.State = profileState(report.Checks, report.Gates)
	report.Detail = profileDetail(report.State)
	report.Metrics = metricsFor(report.Checks, report.Gates, Metrics{})
	report.RuntimeReadiness = BuildRuntimeReadiness(report)

	return report
}

func findReadinessGate(report ProfileReport, name string) GateDecision {
	for _, gate := range report.Gates {
		if gate.Name == name {
			return gate
		}
	}

	Fail("missing readiness gate " + name)
	return GateDecision{}
}

func findRuntimeInvariant(report RuntimeReadinessReport, invariantID string) RuntimeReadinessInvariant {
	for _, invariant := range report.Invariants {
		if invariant.InvariantID == invariantID {
			return invariant
		}
	}

	Fail("missing runtime readiness invariant " + invariantID)
	return RuntimeReadinessInvariant{}
}
