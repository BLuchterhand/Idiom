package comprehension

import (
	"path/filepath"
	"sort"
	"strings"
)

type packQualityCandidate struct {
	path   string
	text   string
	source bool
	score  int
}

func assessPackQuality(task string, files []PackFile, symbols []PackSymbol, tests []PackTest) PackQuality {
	tokens := packQualityTokens(task)
	if len(tokens) == 0 {
		return PackQuality{LocalizationConfidence: "unknown", Reasons: []string{"empty_query_tokens"}}
	}

	candidates := packQualityCandidates(files, symbols, tests)
	for i := range candidates {
		candidates[i].score = packQualityOverlap(tokens, candidates[i].text)
	}

	sourceOwnerFound := false
	bestScore := 0
	bestSourceScore := 0

	for _, candidate := range candidates {
		if candidate.score <= 0 {
			continue
		}

		if candidate.score > bestScore {
			bestScore = candidate.score
		}

		if candidate.source && candidate.score > bestSourceScore {
			bestSourceScore = candidate.score
		}

		if candidate.source && candidate.score >= 3 {
			sourceOwnerFound = true
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if sourceOwnerFound && candidates[i].source != candidates[j].source {
			return candidates[i].source
		}

		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}

		if candidates[i].source != candidates[j].source {
			return candidates[i].source
		}

		return candidates[i].path < candidates[j].path
	})

	evidencePaths := make([]string, 0, 6)
	seen := map[string]struct{}{}

	for _, candidate := range candidates {
		if candidate.path == "" || candidate.score <= 0 {
			continue
		}

		if !sourceOwnerFound && candidate.source {
			continue
		}

		if _, ok := seen[candidate.path]; !ok && len(evidencePaths) < 6 {
			seen[candidate.path] = struct{}{}
			evidencePaths = append(evidencePaths, candidate.path)
		}
	}

	confidence := "low"
	reasons := []string{}

	switch {
	case sourceOwnerFound && bestSourceScore >= 3:
		confidence = "high"

		reasons = append(reasons, "source_owner_token_overlap_high")
	case sourceOwnerFound:
		confidence = "medium"

		reasons = append(reasons, "source_owner_token_overlap")
	case bestScore > 0:
		reasons = append(reasons, "related_evidence_without_source_owner")
	default:
		reasons = append(reasons, "no_query_token_overlap_in_packet")
	}

	return PackQuality{
		LocalizationConfidence: confidence,
		SourceOwnerFound:       sourceOwnerFound,
		EvidencePaths:          evidencePaths,
		Reasons:                reasons,
	}
}

func packQualityCandidates(files []PackFile, symbols []PackSymbol, tests []PackTest) []packQualityCandidate {
	out := make([]packQualityCandidate, 0, len(files)+len(symbols)+len(tests))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}

		out = append(out, packQualityCandidate{
			path:   path,
			text:   strings.Join(append([]string{path, file.Role, file.Reason}, file.Handles...), " "),
			source: !packQualityPathIsTest(path),
		})
	}

	for _, symbol := range symbols {
		path := strings.TrimSpace(symbol.Path)
		if path == "" {
			continue
		}

		out = append(out, packQualityCandidate{
			path:   path,
			text:   strings.Join([]string{path, symbol.Kind, symbol.QualifiedName, symbol.Reason}, " "),
			source: !packQualityPathIsTest(path) && symbol.Kind != "test",
		})
	}

	for _, test := range tests {
		path := strings.TrimSpace(test.Path)
		if path == "" {
			continue
		}

		out = append(out, packQualityCandidate{
			path: path,
			text: strings.Join([]string{path, test.Symbol, test.Command, test.Reason}, " "),
		})
	}

	return out
}

func packQualityTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, token := range splitPackQualityText(text) {
		if len(token) < 3 || packQualityStopToken(token) {
			continue
		}

		out[packQualityStem(token)] = struct{}{}
	}

	return out
}

func packQualityOverlap(tokens map[string]struct{}, text string) int {
	seen := map[string]struct{}{}

	for _, token := range splitPackQualityText(text) {
		stem := packQualityStem(token)
		if _, ok := tokens[stem]; ok {
			seen[stem] = struct{}{}
		}
	}

	return len(seen)
}

func splitPackQualityText(text string) []string {
	var (
		tokens []string
		b      strings.Builder
	)

	flush := func() {
		if b.Len() == 0 {
			return
		}

		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}

	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			if b.Len() > 0 {
				flush()
			}

			b.WriteRune(r + ('a' - 'A'))

			continue
		}

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			continue
		}

		flush()
	}

	flush()

	return tokens
}

func packQualityStem(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return packQualityTrimDoubledFinalConsonant(strings.TrimSuffix(token, suffix))
		}
	}

	return token
}

func packQualityTrimDoubledFinalConsonant(token string) string {
	if len(token) < 2 {
		return token
	}

	last := token[len(token)-1]
	prev := token[len(token)-2]

	if last == prev {
		switch last {
		case 'b', 'd', 'g', 'm', 'n', 'p', 'r', 't':
			return token[:len(token)-1]
		}
	}

	return token
}

func packQualityStopToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "where", "when", "that", "this", "from", "into", "over",
		"do", "not", "no", "only", "same", "way", "longer", "first", "later", "time",
		"bug", "fix", "issue", "fail", "fails", "failure", "source", "file", "files", "symbol", "symbols",
		"research", "repo", "repository", "edit", "edits", "editing", "investigate", "identify", "name",
		"likely", "smallest", "proper", "propose", "return", "answer", "evidence", "codebase", "exact",
		"missing", "insufficient", "sufficient", "claim":
		return true
	default:
		return false
	}
}

func packQualityPathIsTest(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))

	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/fixtures/") ||
		strings.Contains(path, "/tests/")
}
