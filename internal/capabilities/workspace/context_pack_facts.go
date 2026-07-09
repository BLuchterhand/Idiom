package workspace

import (
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/repository/comprehension"
)

func contextPackToolResultFacts(result comprehension.PackResult) *toolfacts.ToolResultFacts {
	var discoveryStrength toolfacts.DiscoveryStrength
	switch result.DiscoveryStrength() {
	case comprehension.PackDiscoveryStrengthCandidateOnly:
		discoveryStrength = toolfacts.DiscoveryStrengthCandidateOnly
	case comprehension.PackDiscoveryStrengthSourceOwner:
		discoveryStrength = toolfacts.DiscoveryStrengthSourceOwner
	}

	facts := &toolfacts.ToolResultFacts{
		CandidatePaths:         discoveryFactCandidatePaths(comprehension.RankedPackCandidatePaths(result)),
		CandidateSymbols:       comprehension.RankedPackCandidateSymbols(result),
		HasDiscoveryTargets:    len(result.CandidateFiles) > 0 || len(result.CandidateSymbols) > 0 || len(result.Tests) > 0,
		DiscoveryStrength:      discoveryStrength,
		SourceOwnerFound:       result.Quality.SourceOwnerFound,
		LocalizationConfidence: strings.TrimSpace(result.Quality.LocalizationConfidence),
	}

	if len(facts.CandidatePaths) == 0 && len(facts.CandidateSymbols) == 0 && !facts.HasDiscoveryTargets && !facts.SourceOwnerFound && facts.LocalizationConfidence == "" {
		return nil
	}

	return facts
}

func contextQueryToolResultFacts(result comprehension.QueryResult) *toolfacts.ToolResultFacts {
	paths := discoveryFactCandidatePaths(extractPathsFromNodeSummaries(result.Nodes))
	symbols := contextQueryCandidateSymbols(result.Nodes)
	if len(paths) == 0 && len(symbols) == 0 {
		return nil
	}

	facts := &toolfacts.ToolResultFacts{
		CandidatePaths:      paths,
		CandidateSymbols:    symbols,
		HasDiscoveryTargets: true,
		DiscoveryStrength:   toolfacts.DiscoveryStrengthCandidateOnly,
		SourceOwnerFound:    false,
	}

	return facts
}

func contextQueryCandidateSymbols(nodes []comprehension.NodeSummary) []string {
	seen := map[string]struct{}{}
	var symbols []string
	for _, node := range nodes {
		name := strings.TrimSpace(node.QualifiedName)
		if name == "" {
			continue
		}

		value := name
		if path := strings.TrimPrefix(strings.TrimSpace(node.Path), "./"); path != "" {
			value = path + "#" + name
		}

		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		symbols = append(symbols, value)
	}

	return symbols
}

func discoveryFactCandidatePaths(candidates []string) []string {
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
