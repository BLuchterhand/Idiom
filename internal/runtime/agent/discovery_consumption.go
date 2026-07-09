package agent

import (
	"regexp"
	"strconv"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/toolfacts"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
	"idiom/internal/runtime/workerruntime"
)

const discoveryBroadSearchMatchThreshold = 50

var textSearchFoundMatchingLinePattern = regexp.MustCompile(`found\s+(\d+)\s+matching\s+line`)

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

	if len(record.SourceBodyPaths()) > 0 {
		return outcome
	}

	hasDiscoveryFacts := result.Facts != nil && result.Facts.HasDiscoveryTargets
	if strings.TrimSpace(record.output) == "" && strings.TrimSpace(record.summary) == "" && !hasDiscoveryFacts {
		return outcome
	}

	outcome.Candidates = record.CandidatePaths(maxCandidates)

	if hasDiscoveryFacts {
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
	case workspace.IsTextSearchToolName(tool):
		if len(outcome.Candidates) == 0 {
			return outcome
		}

		broad, count, countOK := textSearchDiscoveryResultBreadth(result)
		if countOK {
			outcome.SearchHitCount = count
			outcome.HighCardinalitySearch = count > discoveryBroadSearchMatchThreshold
		}

		if broad {
			outcome.Strength = toolfacts.DiscoveryStrengthCandidateOnly
		} else {
			outcome.Strength = toolfacts.DiscoveryStrengthSourceOwner
		}
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

func textSearchDiscoveryResultBreadth(result workspace.ToolResult) (broad bool, count int, countOK bool) {
	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))
	if text == "" {
		return false, 0, false
	}

	if strings.Contains(text, "showing first") ||
		strings.Contains(text, "truncated") ||
		strings.Contains(text, "too many matches") ||
		strings.Contains(text, "more matches") {

		broad = true
	}

	matches := textSearchFoundMatchingLinePattern.FindStringSubmatch(text)
	if len(matches) != 2 {
		return broad, 0, false
	}

	count, err := strconv.Atoi(matches[1])
	if err != nil {
		return broad, 0, false
	}

	return broad || count >= discoveryBroadSearchMatchThreshold, count, true
}
