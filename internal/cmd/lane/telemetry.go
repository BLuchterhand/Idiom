package lane
import (
	"strings"

	"idiom/internal/cmdtelemetry"
	"idiom/internal/config"
)

func laneStartTelemetryFields(opts laneOptions, cfg config.Config) map[string]any {
	fields := map[string]any{
		"mode":                    strings.TrimSpace(opts.Mode),
		"readiness_profile":       laneReadinessProfileName(opts.Mode),
		"backend_profile":         strings.TrimSpace(opts.BackendProfile),
		"metadata_only_contract":  true,
		"contains_prompt_content": false,
		"local_ops":               cmdtelemetry.LocalOpsFields(cfg, opts.Mode),
	}
	profile, ok := laneDoctorReadinessProfile(opts)
	if !ok {
		fields["readiness_state"] = "unavailable"
		fields["backend_state"] = "unavailable"
		fields["queue_state"] = "unavailable"
		fields["context_budget_state"] = "unavailable"
		fields["route_classifier_state"] = "unavailable"
		return fields
	}
	fields["readiness_state"] = profile.State
	fields["enforcement"] = profile.Enforcement
	fields["required_for_launch"] = profile.RequiredForLaunch
	fields["task_family"] = profile.TaskFamily
	fields["contract"] = profile.Contract
	fields["required_roles"] = append([]string(nil), profile.RequiredRoles...)
	fields["role_aliases"] = cloneStringMap(profile.RoleAliases)
	fields["backend_state"] = profile.Metrics.BackendState
	fields["queue_state"] = profile.Metrics.QueueState
	fields["queue_pools"] = profile.Metrics.QueuePools
	fields["queue_in_flight"] = profile.Metrics.QueueInFlight
	fields["queue_waiting"] = profile.Metrics.QueueWaiting
	fields["queue_idle"] = profile.Metrics.QueueIdle
	fields["context_budget_state"] = profile.Metrics.ContextBudgetState
	fields["context_tokens"] = profile.Metrics.ContextTokens
	fields["max_output_tokens"] = profile.Metrics.MaxOutputTokens
	fields["route_classifier_state"] = profile.Metrics.ClassifierState
	fields["hard_failures"] = profile.Metrics.HardFailures
	fields["soft_degradations"] = profile.Metrics.SoftDegradations
	fields["degraded"] = profile.State == "degraded"
	fields["blocked"] = profile.State == "blocked"
	return fields
}

func laneDoctorReadinessProfile(opts laneOptions) (readinessProfileReport, bool) {
	profileName := laneReadinessProfileName(opts.Mode)
	if profileName == "" {
		return readinessProfileReport{}, false
	}
	profiles, err := buildDoctorReadinessProfiles(opts.ConfigPath, profileName, nil)
	if err != nil || len(profiles) != 1 {
		return readinessProfileReport{}, false
	}
	return profiles[0], true
}

func laneReadinessProfileName(mode string) string {
	switch strings.TrimSpace(mode) {
	case "plan-execute":
		return "plan"
	case "implement":
		return "implement"
	default:
		return ""
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
