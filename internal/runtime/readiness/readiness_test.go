package readiness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProfilesRejectsUnknownProfile(t *testing.T) {
	_, err := BuildProfiles(filepath.Join(t.TempDir(), "missing-config.json"), "unknown", nil)
	if err == nil {
		t.Fatal("expected unknown profile to fail")
	}

	if !IsProfileSelectionError(err) {
		t.Fatalf("expected profile selection error, got %T %v", err, err)
	}
}

func TestWorkspaceRootFromConfigResolvesRelativeRoot(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".idiom")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"workspace":{"root":".."}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := WorkspaceRootFromConfig(configPath)
	if got != filepath.Clean(dir) {
		t.Fatalf("WorkspaceRootFromConfig() = %q, want %q", got, filepath.Clean(dir))
	}
}

func TestRuntimeReadinessSchemaMapsMissingBackendRole(t *testing.T) {
	report := runtimeProfileForCheck("implement", Check{
		Name:   "backend role config",
		State:  "blocked",
		Detail: "missing role aliases: executor",
		Remedy: "add role tags or proxy.default_executor aliases in .idiom/backends.json",
	})

	gate := findReadinessGateForTest(t, report, "backend_roles_resolved")
	if gate.InvariantID != "runtime.backend_roles_resolved" ||
		gate.RuntimeDecision != RuntimeReadinessLaunchBlocked ||
		gate.ReasonClass != ReasonMissingBackendRole ||
		gate.Fallback != FallbackLegacyConfig {

		t.Fatalf("backend gate did not expose typed role failure metadata: %#v", gate)
	}

	invariant := findRuntimeInvariantForTest(t, report.RuntimeReadiness, "runtime.backend_roles_resolved")
	if invariant.Decision != RuntimeReadinessLaunchBlocked ||
		invariant.ReasonClass != ReasonMissingBackendRole ||
		invariant.Fallback != FallbackLegacyConfig {

		t.Fatalf("runtime readiness invariant did not mirror backend gate: %#v", invariant)
	}

	if report.RuntimeReadiness.Decision != RuntimeReadinessLaunchBlocked || !report.RuntimeReadiness.LaunchBlocked {
		t.Fatalf("profile runtime decision = %#v, want launch_blocked", report.RuntimeReadiness)
	}
}

func TestRuntimeReadinessSchemaMapsInvalidContextBudget(t *testing.T) {
	report := runtimeProfileForCheck("plan", Check{
		Name:   "context budget",
		State:  "degraded",
		Detail: "ctx_size=1024 max_output=2048 leaves no prompt budget",
		Remedy: "lower chat.max_tokens or raise the configured backend context",
	})

	gate := findReadinessGateForTest(t, report, "context_budget_valid")
	if gate.Status != "blocked" ||
		gate.RuntimeDecision != RuntimeReadinessLaunchBlocked ||
		gate.ReasonClass != ReasonInvalidContextBudget ||
		gate.Fallback != "" {

		t.Fatalf("context gate did not expose typed invalid-budget metadata: %#v", gate)
	}

	if report.RuntimeReadiness.Decision != RuntimeReadinessLaunchBlocked {
		t.Fatalf("profile runtime decision = %#v, want launch_blocked", report.RuntimeReadiness)
	}
}

func TestRuntimeReadinessSchemaMapsQueueUnavailableAndSoftDegraded(t *testing.T) {
	for _, tc := range []struct {
		name        string
		checkState  string
		reasonClass RuntimeReadinessReasonClass
		fallback    RuntimeReadinessFallback
	}{
		{
			name:        "unavailable",
			checkState:  "unavailable",
			reasonClass: ReasonQueueUnavailable,
			fallback:    FallbackQueueOpportunistic,
		},
		{
			name:        "soft degraded",
			checkState:  "degraded",
			reasonClass: ReasonQueueSoftDegraded,
			fallback:    FallbackQueueOperatorRepair,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := runtimeProfileForCheck("implement", Check{
				Name:   "queue admission",
				State:  tc.checkState,
				Detail: "queue daemon not reachable",
				Remedy: "start the daemon with `idiom queue serve` or inspect queue status",
			})

			gate := findReadinessGateForTest(t, report, "queue_admission_known")
			if gate.Status != "degraded" ||
				gate.RuntimeDecision != RuntimeReadinessAllowedDegraded ||
				gate.ReasonClass != tc.reasonClass ||
				gate.Fallback != tc.fallback {

				t.Fatalf("queue gate did not expose typed degraded metadata: %#v", gate)
			}

			if report.RuntimeReadiness.Decision != RuntimeReadinessAllowedDegraded ||
				report.RuntimeReadiness.LaunchBlocked ||
				report.RuntimeReadiness.LaunchBlockingEnforced {

				t.Fatalf("queue profile should remain allowed_degraded and non-blocking, got %#v", report.RuntimeReadiness)
			}

			if len(report.RuntimeReadiness.Fallbacks) != 1 ||
				report.RuntimeReadiness.Fallbacks[0].Fallback != tc.fallback {

				t.Fatalf("queue fallback not aggregated in runtime readiness: %#v", report.RuntimeReadiness.Fallbacks)
			}
		})
	}
}

func TestRuntimeReadinessSchemaPreservesReportOnlyEnforcement(t *testing.T) {
	report := runtimeProfileForCheck("eval", Check{
		Name:   "queue admission",
		State:  "unavailable",
		Detail: "queue daemon not reachable",
		Remedy: "start the daemon with `idiom queue serve`",
	})

	gate := findReadinessGateForTest(t, report, "queue_admission_known")
	if gate.Status != "blocked" || gate.RuntimeDecision != RuntimeReadinessLaunchBlocked {
		t.Fatalf("eval queue gate should record launch_blocked, got %#v", gate)
	}

	if report.Enforcement != string(EnforcementReportOnly) ||
		gate.Enforcement != string(EnforcementReportOnly) ||
		report.RuntimeReadiness.Enforcement != EnforcementReportOnly {

		t.Fatalf("readiness enforcement must remain report_only, report=%#v gate=%#v", report.RuntimeReadiness, gate)
	}

	if !report.RuntimeReadiness.LaunchBlocked || report.RuntimeReadiness.LaunchBlockingEnforced {
		t.Fatalf("launch_blocked should be report-only metadata, got %#v", report.RuntimeReadiness)
	}
}

func runtimeProfileForCheck(profileName string, check Check) ProfileReport {
	report := ProfileReport{
		Name:              profileName,
		Enforcement:       string(EnforcementReportOnly),
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

func findReadinessGateForTest(t *testing.T, report ProfileReport, name string) GateDecision {
	t.Helper()

	for _, gate := range report.Gates {
		if gate.Name == name {
			return gate
		}
	}

	t.Fatalf("missing gate %q in %#v", name, report.Gates)

	return GateDecision{}
}

func findRuntimeInvariantForTest(t *testing.T, report RuntimeReadinessReport, invariantID string) RuntimeReadinessInvariant {
	t.Helper()

	for _, invariant := range report.Invariants {
		if invariant.InvariantID == invariantID {
			return invariant
		}
	}

	t.Fatalf("missing runtime invariant %q in %#v", invariantID, report.Invariants)

	return RuntimeReadinessInvariant{}
}
