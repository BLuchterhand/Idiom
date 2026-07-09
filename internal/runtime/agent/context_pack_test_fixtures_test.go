package agent

import "idiom/internal/contracts/toolfacts"

func contextPackCandidateFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return discoveryCandidateFactsForTest(paths...)
}

func contextPackSourceOwnerFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return discoverySourceOwnerFactsForTest(paths...)
}

func discoveryCandidateFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return discoveryFactsForTest(toolfacts.DiscoveryStrengthCandidateOnly, paths...)
}

func discoverySourceOwnerFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return discoveryFactsForTest(toolfacts.DiscoveryStrengthSourceOwner, paths...)
}

func discoveryFactsForTest(strength toolfacts.DiscoveryStrength, paths ...string) *toolfacts.ToolResultFacts {
	return &toolfacts.ToolResultFacts{
		CandidatePaths:      append([]string(nil), paths...),
		HasDiscoveryTargets: len(paths) > 0,
		DiscoveryStrength:   strength,
		SourceOwnerFound:    strength == toolfacts.DiscoveryStrengthSourceOwner,
	}
}

func discoveryQueryFactsForTest(candidates ...toolfacts.DiscoveryCandidateEvidence) *toolfacts.ToolResultFacts {
	paths := make([]string, 0, len(candidates))
	ranked := make([]toolfacts.DiscoveryCandidateEvidence, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Path == "" {
			continue
		}
		ranked = append(ranked, candidate)
		if _, ok := seen[candidate.Path]; ok {
			continue
		}
		seen[candidate.Path] = struct{}{}
		paths = append(paths, candidate.Path)
	}

	return &toolfacts.ToolResultFacts{
		CandidatePaths:            paths,
		RankedDiscoveryCandidates: ranked,
		HasDiscoveryTargets:       len(paths) > 0,
		DiscoveryStrength:         toolfacts.DiscoveryStrengthSourceOwner,
		SourceOwnerFound:          len(paths) > 0,
	}
}
