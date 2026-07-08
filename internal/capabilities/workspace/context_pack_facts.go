package workspace

import (
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/comprehension"
)

func contextPackToolResultFacts(result comprehension.PackResult) *toolfacts.ToolResultFacts {
	facts := &toolfacts.ToolResultFacts{
		CandidatePaths:         contextPackFactCandidatePaths(comprehension.RankedPackCandidatePaths(result)),
		CandidateSymbols:       comprehension.RankedPackCandidateSymbols(result),
		HasDiscoveryTargets:    len(result.CandidateFiles) > 0 || len(result.CandidateSymbols) > 0 || len(result.Tests) > 0,
		SourceOwnerFound:       result.Quality.SourceOwnerFound,
		LocalizationConfidence: strings.TrimSpace(result.Quality.LocalizationConfidence),
	}

	if len(facts.CandidatePaths) == 0 && len(facts.CandidateSymbols) == 0 && !facts.HasDiscoveryTargets && !facts.SourceOwnerFound && facts.LocalizationConfidence == "" {
		return nil
	}

	return facts
}

func contextPackFactCandidatePaths(candidates []string) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, candidate := range candidates {
		path := strings.TrimPrefix(strings.TrimSpace(candidate), "./")
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return paths
}
