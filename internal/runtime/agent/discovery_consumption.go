package agent

import (
	"regexp"
	"strconv"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/runtime/workerruntime"
)

type discoveryConsumptionStrength int

const (
	discoveryConsumptionNone discoveryConsumptionStrength = iota
	discoveryConsumptionCandidateOnly
	discoveryConsumptionSourceOwner
)

const broadTextSearchMatchThreshold = 50

var textSearchFoundMatchingLinePattern = regexp.MustCompile(`found\s+(\d+)\s+matching\s+line`)

func successfulSearchDiscoveryResult(req workspace.ToolRequest, result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" || workerruntime.IsZeroHitSearchResult(result) {
		return false
	}

	switch strings.TrimSpace(req.Tool) {
	case "search_files", "search_content", "rg", "grep",
		"go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol":
		return true
	default:
		return false
	}
}

func discoveryConsumptionResultStrength(req workspace.ToolRequest, result workspace.ToolResult) discoveryConsumptionStrength {
	return discoveryConsumptionResultStrengthFromLegacySemantics(req, result)
}

func discoveryConsumptionResultStrengthFromLegacySemantics(req workspace.ToolRequest, result workspace.ToolResult) discoveryConsumptionStrength {
	if strings.TrimSpace(result.Error) != "" || workerruntime.IsZeroHitSearchResult(result) {
		return discoveryConsumptionNone
	}

	if concreteSourceEvidenceResult(result) {
		return discoveryConsumptionNone
	}

	if strings.TrimSpace(result.Output) == "" && strings.TrimSpace(result.Summary) == "" {
		return discoveryConsumptionNone
	}

	switch strings.TrimSpace(req.Tool) {
	case "context_pack":
		if !contextPackResultHasTargets(result) {
			return discoveryConsumptionNone
		}

		if contextPackResultWeakSourceOwner(result) {
			return discoveryConsumptionCandidateOnly
		}

		return discoveryConsumptionSourceOwner
	case "context_query":
		if len(discoveryConsumptionCandidatesFromResult(result, 1)) == 0 {
			return discoveryConsumptionNone
		}

		if discoveryConsumptionResultHasReadableSourceOwner(result) {
			return discoveryConsumptionSourceOwner
		}

		return discoveryConsumptionCandidateOnly
	case "search_content", "rg", "grep":
		if len(discoveryConsumptionCandidatesFromResult(result, 1)) == 0 {
			return discoveryConsumptionNone
		}

		if textSearchDiscoveryResultIsBroad(result) {
			return discoveryConsumptionCandidateOnly
		}

		return discoveryConsumptionSourceOwner
	case "search_files",
		"go_workspace_symbol", "cpp_workspace_symbol", "python_workspace_symbol", "ts_workspace_symbol", "rust_workspace_symbol", "java_workspace_symbol",
		"structural_search", "discovery_query", "context_zoom", "context_impact":
		if len(discoveryConsumptionCandidatesFromResult(result, 1)) == 0 {
			return discoveryConsumptionNone
		}

		return discoveryConsumptionSourceOwner
	default:
		return discoveryConsumptionNone
	}
}

func textSearchDiscoveryResultIsBroad(result workspace.ToolResult) bool {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false
	}

	if strings.Contains(text, "showing first") ||
		strings.Contains(text, "truncated") ||
		strings.Contains(text, "too many matches") ||
		strings.Contains(text, "more matches") {

		return true
	}

	matches := textSearchFoundMatchingLinePattern.FindStringSubmatch(text)
	if len(matches) != 2 {
		return false
	}

	count, err := strconv.Atoi(matches[1])
	return err == nil && count >= broadTextSearchMatchThreshold
}

func discoveryConsumptionResultHasReadableSourceOwner(result workspace.ToolResult) bool {
	text := strings.TrimSpace(result.Summary + "\n" + result.Output)
	if text == "" {
		return false
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, `"source_owner_found":false`) ||
		strings.Contains(lower, `"source_owner_found": false`) {

		return false
	}

	if !strings.Contains(lower, `"source_owner_found":true`) &&
		!strings.Contains(lower, `"source_owner_found": true`) &&
		!strings.Contains(lower, "source owner found") &&
		!strings.Contains(lower, "source-owner found") &&
		!strings.Contains(lower, "readable source owner") &&
		!strings.Contains(lower, "readable source-owner") {

		return false
	}

	return len(readOnlySourceLikePaths(sourceLikePathsFromText(text, 4), 1)) > 0
}
