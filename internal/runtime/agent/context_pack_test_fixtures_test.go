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
