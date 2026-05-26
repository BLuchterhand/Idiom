package localops

import (
	"strings"

	"idiom/internal/readiness"
)

func BuildProfileReport(profile readiness.ProfileReport) ProfileReport {
	out := ProfileReport{
		Name:  profile.Name,
		State: profile.State,
		Readiness: ReadinessProfileEvidence{
			Enforcement:          profile.Enforcement,
			RequiredForLaunch:    profile.RequiredForLaunch,
			TaskFamily:           profile.TaskFamily,
			Contract:             profile.Contract,
			PermissionProfile:    profile.PermissionProfile,
			RoleAliases:          profile.RoleAliases,
			HardFailures:         len(profile.HardFailures),
			SoftDegradations:     len(profile.SoftDegradations),
			UnavailableChecks:    len(profile.Unavailable),
			BackendState:         profile.Metrics.BackendState,
			QueueState:           profile.Metrics.QueueState,
			ContextBudgetState:   profile.Metrics.ContextBudgetState,
			RouteClassifierState: profile.Metrics.ClassifierState,
		},
	}
	out.Smoke = BuildSmokeReport(profile)
	out.Capacity = BuildCapacityReport(profile)
	out.Blockers = append(out.Blockers, out.Smoke.Unsupported...)
	out.Blockers = append(out.Blockers, out.Capacity.Blockers...)
	return out
}

func BuildSmokeReport(profile readiness.ProfileReport) SmokeReport {
	total := len(profile.Gates)
	passed := 0
	smoke := SmokeReport{
		Status:  profile.State,
		Total:   total,
		Signals: map[string]string{},
	}
	for _, gate := range profile.Gates {
		smoke.Signals[gate.Name] = gate.Status
		if gate.Status == "pass" {
			passed++
			continue
		}
		blocker := Blocker{Scope: profile.Name + "/" + gate.Name, Reason: SafeText(gate.Reason), Remedy: SafeText(gate.Remedy)}
		if gate.Status == "blocked" {
			smoke.Unsupported = append(smoke.Unsupported, blocker)
		} else {
			smoke.Degradations = append(smoke.Degradations, blocker)
		}
	}
	smoke.Passed = passed
	if total > 0 {
		smoke.PassRate = float64(passed) / float64(total)
	}
	if len(smoke.Unsupported) > 0 {
		smoke.Status = "blocked"
	} else if len(smoke.Degradations) > 0 {
		smoke.Status = "degraded"
	} else {
		smoke.Status = "pass"
	}
	return smoke
}

func BuildCapacityReport(profile readiness.ProfileReport) CapacityReport {
	capacity := CapacityReport{
		Status:             "pass",
		ContextTokens:      profile.Metrics.ContextTokens,
		MaxOutputTokens:    profile.Metrics.MaxOutputTokens,
		PromptBudgetTokens: profile.Metrics.ContextTokens - profile.Metrics.MaxOutputTokens,
		QueuePools:         profile.Metrics.QueuePools,
		QueueInFlight:      profile.Metrics.QueueInFlight,
		QueueWaiting:       profile.Metrics.QueueWaiting,
		QueueIdle:          profile.Metrics.QueueIdle,
	}
	capacity.Limits = append(capacity.Limits,
		Limit{Name: "context_tokens", State: profile.Metrics.ContextBudgetState, Value: profile.Metrics.ContextTokens},
		Limit{Name: "max_output_tokens", State: profile.Metrics.ContextBudgetState, Value: profile.Metrics.MaxOutputTokens},
		Limit{Name: "queue_pools", State: profile.Metrics.QueueState, Value: profile.Metrics.QueuePools},
	)
	if capacity.PromptBudgetTokens < 0 {
		capacity.PromptBudgetTokens = 0
	}
	if profile.Metrics.ContextBudgetState != "pass" {
		capacity.Status = "blocked"
		capacity.Blockers = append(capacity.Blockers, Blocker{
			Scope:  profile.Name + "/context_budget",
			Reason: SafeText("context budget is " + firstNonEmpty(profile.Metrics.ContextBudgetState, "unavailable")),
			Remedy: SafeText(remedyForCheck(profile.Checks, "context budget")),
		})
	}
	if profile.Metrics.QueueState != "pass" {
		if capacity.Status != "blocked" {
			capacity.Status = "degraded"
		}
		capacity.Blockers = append(capacity.Blockers, Blocker{
			Scope:  profile.Name + "/queue_capacity",
			Reason: SafeText("queue capacity evidence is " + firstNonEmpty(profile.Metrics.QueueState, "unavailable")),
			Remedy: SafeText(remedyForCheck(profile.Checks, "queue admission")),
		})
	}
	if profile.Metrics.ContextTokens <= 0 {
		capacity.Limits = append(capacity.Limits, Limit{Name: "context_sweep", State: "unsupported", Reason: "configured context window unavailable"})
	} else {
		capacity.Limits = append(capacity.Limits, Limit{Name: "context_sweep", State: "bounded", Value: profile.Metrics.ContextTokens, Reason: "default harness smoke records configured limit; live sweeps require explicit backend runs"})
	}
	return capacity
}

func CollectBlockers(profiles []ProfileReport, replay ReplayReport) []Blocker {
	var blockers []Blocker
	for _, profile := range profiles {
		blockers = append(blockers, profile.Blockers...)
	}
	blockers = append(blockers, replay.Blockers...)
	return blockers
}

func SummarizeBenchmark(report BenchmarkReport) BenchmarkSummary {
	summary := BenchmarkSummary{ProfileCount: len(report.Profiles), ReplayLogCount: report.Replay.LogCount, BlockerCount: len(report.Blockers)}
	for _, profile := range report.Profiles {
		summary.SmokePassed += profile.Smoke.Passed
		summary.SmokeTotal += profile.Smoke.Total
		summary.DegradationCount += len(profile.Smoke.Degradations)
		if profile.Capacity.Status == "blocked" {
			summary.CapacityBlocked++
		}
	}
	if summary.SmokeTotal > 0 {
		summary.SmokePassRate = float64(summary.SmokePassed) / float64(summary.SmokeTotal)
	}
	return summary
}

func remedyForCheck(checks []readiness.Check, name string) string {
	for _, check := range checks {
		if check.Name == name {
			return check.Remedy
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
