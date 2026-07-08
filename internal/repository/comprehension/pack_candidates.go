package comprehension

import (
	"sort"
	"strings"
)

// RankedPackCandidatePaths returns the pack's candidate paths in the same
// evidence order used by pack quality assessment.
func RankedPackCandidatePaths(result PackResult) []string {
	seen := map[string]struct{}{}
	var paths []string
	add := func(path string) bool {
		path = strings.TrimPrefix(strings.TrimSpace(path), "./")
		if path == "" {
			return false
		}

		if _, ok := seen[path]; ok {
			return false
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		return true
	}

	weakQuality := strings.TrimSpace(result.Quality.LocalizationConfidence) == "low" && !result.Quality.SourceOwnerFound
	for _, path := range result.Quality.EvidencePaths {
		if weakQuality && !packQualityPathIsTest(path) {
			continue
		}

		add(path)
	}

	if tokens := packQualityTokens(result.Task); len(tokens) > 0 {
		candidates := packQualityCandidates(result.CandidateFiles, result.CandidateSymbols, result.Tests)
		for i := range candidates {
			candidates[i].score = packQualityOverlap(tokens, candidates[i].text)
		}

		if addRankedPackCandidates(candidates, add) {
			return paths
		}
	}

	for _, file := range result.CandidateFiles {
		add(file.Path)
	}

	for _, symbol := range result.CandidateSymbols {
		add(symbol.Path)
	}

	for _, test := range result.Tests {
		add(test.Path)
	}

	return paths
}

func addRankedPackCandidates(candidates []packQualityCandidate, add func(string) bool) bool {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}

		if candidates[i].source != candidates[j].source {
			return candidates[i].source
		}

		return candidates[i].path < candidates[j].path
	})

	added := false
	for _, candidate := range candidates {
		if candidate.score <= 0 {
			continue
		}

		added = add(candidate.path) || added
	}

	return added
}

// RankedPackCandidateSymbols returns candidate symbols and file handles in the
// pack's evidence order.
func RankedPackCandidateSymbols(result PackResult) []string {
	evidenceRank := map[string]int{}
	for i, path := range result.Quality.EvidencePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if _, ok := evidenceRank[path]; !ok {
			evidenceRank[path] = i
		}
	}

	tokens := packQualityTokens(result.Task)
	candidates := make([]packSymbolCandidate, 0, len(result.CandidateSymbols))
	for _, symbol := range result.CandidateSymbols {
		path := strings.TrimSpace(symbol.Path)
		name := strings.TrimSpace(symbol.QualifiedName)

		if path == "" || name == "" {
			continue
		}

		candidates = append(candidates, packSymbolCandidate{
			value:        path + "#" + name,
			path:         path,
			evidenceRank: packCandidateEvidenceRank(evidenceRank, path),
			score:        packQualityOverlap(tokens, strings.Join([]string{path, symbol.Kind, name, symbol.Reason}, " ")),
		})
	}

	sortPackSymbolCandidates(candidates)

	var symbols []string
	seen := map[string]struct{}{}
	addSymbol := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		symbols = append(symbols, value)
	}

	for _, candidate := range candidates {
		addSymbol(candidate.value)
	}

	for _, f := range result.CandidateFiles {
		path := strings.TrimSpace(f.Path)
		if path == "" || strings.TrimSpace(f.Role) == "test" {
			continue
		}

		for _, handle := range f.Handles {
			handle = strings.TrimPrefix(strings.TrimSpace(handle), "symbol://")
			if handle == "" || handle == path {
				continue
			}

			addSymbol(handle)
		}
	}

	return symbols
}

type packSymbolCandidate struct {
	value        string
	path         string
	evidenceRank int
	score        int
}

func packCandidateEvidenceRank(ranks map[string]int, path string) int {
	if len(ranks) == 0 {
		return -1
	}

	if rank, ok := ranks[strings.TrimSpace(path)]; ok {
		return rank
	}

	return -1
}

func sortPackSymbolCandidates(candidates []packSymbolCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].evidenceRank >= 0 || candidates[j].evidenceRank >= 0 {
			if candidates[i].evidenceRank < 0 {
				return false
			}

			if candidates[j].evidenceRank < 0 {
				return true
			}

			if candidates[i].evidenceRank != candidates[j].evidenceRank {
				return candidates[i].evidenceRank < candidates[j].evidenceRank
			}
		}

		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}

		return candidates[i].value < candidates[j].value
	})
}
