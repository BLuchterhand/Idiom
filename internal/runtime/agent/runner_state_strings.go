package agent

import (
	"idiom/internal/platform/util"
	"idiom/internal/runtime/agent/stringset"
	"strings"
)

func mergeRunnerStateStrings(groups ...[]string) []string {
	return stringset.Merge(groups...)
}

func runnerCurrentAllowedOrBriefTools(currentAllowedTools []string, briefAllowedTools []string) []string {
	if len(uniquePhaseStrings(currentAllowedTools)) > 0 {
		return append([]string(nil), currentAllowedTools...)
	}

	return append([]string(nil), briefAllowedTools...)
}

func runnerStateSummary(state runnerNextActionState) string {
	parts := []string{state.Reason}
	if state.LastTool != "" {
		if state.LastTarget != "" {
			parts = append(parts, state.LastTool+" "+state.LastTarget)
		} else {
			parts = append(parts, state.LastTool)
		}
	}

	if state.ResultError != "" {
		parts = append(parts, "blocked: "+state.ResultError)
	} else if state.ResultSummary != "" {
		parts = append(parts, state.ResultSummary)
	}

	return util.Truncate(strings.TrimSpace(strings.Join(parts, " | ")), 320)
}

func prependRunnerStateStrings(preferred []string, existing []string) []string {
	out := make([]string, 0, len(preferred)+len(existing))
	for _, value := range preferred {
		out = util.AppendUnique(out, value)
	}

	for _, value := range existing {
		out = util.AppendUnique(out, value)
	}

	return out
}

func filterRunnerStateStrings(values []string, drop func(string) bool) []string {
	if len(values) == 0 {
		return values
	}

	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || drop(trimmed) {
			continue
		}

		filtered = append(filtered, trimmed)
	}

	return filtered
}
