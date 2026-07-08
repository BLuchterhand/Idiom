package agent

import "idiom/internal/contracts/toolfacts"

func contextPackCandidateFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return contextPackFactsForTest("low", false, paths...)
}

func contextPackSourceOwnerFactsForTest(paths ...string) *toolfacts.ToolResultFacts {
	return contextPackFactsForTest("high", true, paths...)
}

func contextPackFactsForTest(confidence string, sourceOwner bool, paths ...string) *toolfacts.ToolResultFacts {
	return &toolfacts.ToolResultFacts{
		CandidatePaths:         append([]string(nil), paths...),
		HasDiscoveryTargets:    len(paths) > 0,
		SourceOwnerFound:       sourceOwner,
		LocalizationConfidence: confidence,
	}
}
