package agent

import (
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/workerruntime"
)

type discoveryResultOutcome struct {
	Strength              toolfacts.DiscoveryStrength
	Candidates            []string
	SearchHitCount        int
	HighCardinalitySearch bool
}

func discoveryResultOutcomeFromResult(req workspace.ToolRequest, result workspace.ToolResult, maxCandidates int) discoveryResultOutcome {
	var outcome discoveryResultOutcome

	record := newToolResultEvidenceRecord(result)
	if record.failed || workerruntime.IsZeroHitSearchResult(result) {
		return outcome
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == "" {
		tool = strings.TrimSpace(result.Tool)
	}

	if len(record.SourceWindowPaths()) > 0 {
		return outcome
	}

	hasDiscoveryFacts := result.Facts != nil && result.Facts.HasDiscoveryTargets
	if strings.TrimSpace(record.output) == "" && strings.TrimSpace(record.summary) == "" && !hasDiscoveryFacts {
		return outcome
	}

	outcome.Candidates = record.CandidatePaths(maxCandidates)

	if hasDiscoveryFacts {
		outcome.SearchHitCount = result.Facts.SearchHitCount
		outcome.HighCardinalitySearch = result.Facts.SearchHighCardinality

		switch result.Facts.DiscoveryStrength {
		case toolfacts.DiscoveryStrengthCandidateOnly:
			outcome.Strength = toolfacts.DiscoveryStrengthCandidateOnly
		case toolfacts.DiscoveryStrengthSourceOwner:
			outcome.Strength = toolfacts.DiscoveryStrengthSourceOwner
		}
		if outcome.Strength == "" {
			if result.Facts.SourceOwnerFound {
				outcome.Strength = toolfacts.DiscoveryStrengthSourceOwner
			} else if len(outcome.Candidates) > 0 || len(result.Facts.CandidateSymbols) > 0 {
				outcome.Strength = toolfacts.DiscoveryStrengthCandidateOnly
			}
		}
		return outcome
	}

	switch {
	case tool == workspace.ToolNameContextQuery:
		if len(outcome.Candidates) == 0 {
			return outcome
		}

		outcome.Strength = toolfacts.DiscoveryStrengthCandidateOnly
	case tool == workspace.ToolNameSearchFiles ||
		languagetools.ToolHasAnyCapability(tool, semanticlanguage.CapWorkspaceSymbol) ||
		tool == workspace.ToolNameStructuralSearch ||
		tool == workspace.ToolNameDiscoveryQuery ||
		tool == workspace.ToolNameContextZoom ||
		tool == workspace.ToolNameContextImpact:
		if len(outcome.Candidates) == 0 {
			return outcome
		}

		outcome.Strength = toolfacts.DiscoveryStrengthSourceOwner
	}

	return outcome
}
