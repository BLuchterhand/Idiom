package agent

import "idiom/internal/contracts/toolfacts"

func contextPackCandidateFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return contextPackFactsForTest(toolfacts.DiscoveryStrengthCandidateOnly, paths...)
}

func contextPackSourceOwnerFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return contextPackFactsForTest(toolfacts.DiscoveryStrengthSourceOwner, paths...)
}

func contextPackFactsForTest(strength toolfacts.DiscoveryStrength, paths ...string) *toolfacts.ToolResultFacts {
	return &toolfacts.ToolResultFacts{
		CandidatePaths:      append([]string(nil), paths...),
		HasDiscoveryTargets: len(paths) > 0,
		DiscoveryStrength:   strength,
	}
}
