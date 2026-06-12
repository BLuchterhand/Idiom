package eval

import (
	"regexp"
	"strings"
)

var groundednessStopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "in": {}, "on": {}, "at": {}, "to": {}, "of": {},
	"for": {}, "and": {}, "or": {}, "but": {}, "is": {}, "are": {}, "was": {},
	"were": {}, "be": {}, "been": {}, "being": {}, "have": {}, "has": {}, "had": {},
	"do": {}, "does": {}, "did": {}, "will": {}, "would": {}, "could": {}, "should": {},
	"may": {}, "might": {}, "shall": {}, "can": {}, "not": {}, "with": {}, "from": {},
	"by": {}, "as": {}, "this": {}, "that": {}, "it": {}, "if": {}, "then": {},
	"so": {}, "we": {}, "you": {}, "i": {}, "they": {}, "them": {}, "our": {},
	"your": {}, "their": {}, "there": {}, "here": {}, "which": {}, "what": {},
	"when": {}, "where": {}, "how": {}, "why": {},
}

var groundednessTokenPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_]{2,}`)

// tokenizeForGroundedness lowercases and splits on non-alphanumeric runs,
// keeping tokens of length >= 3 to avoid noise from short stop-words slipping
// past the explicit list.
func tokenizeForGroundedness(text string) []string {
	if text == "" {
		return nil
	}

	matches := groundednessTokenPattern.FindAllString(strings.ToLower(text), -1)
	out := make([]string, 0, len(matches))

	for _, m := range matches {
		if _, stop := groundednessStopWords[m]; stop {
			continue
		}

		out = append(out, m)
	}

	return out
}

// GroundednessScore returns the fraction of response tokens that also appear
// in the context bundle. Mirrors sigmap's judge.groundedness primitive: cheap,
// gameable on its own, but useful as a band-based learning trigger and as a
// sanity oracle that catches "the model bullshitted instead of citing the
// context". Score is in [0, 1].
func GroundednessScore(response, context string) float64 {
	respTokens := tokenizeForGroundedness(response)
	if len(respTokens) == 0 {
		return 0
	}

	ctxTokenSet := make(map[string]struct{})
	for _, t := range tokenizeForGroundedness(context) {
		ctxTokenSet[t] = struct{}{}
	}

	if len(ctxTokenSet) == 0 {
		return 0
	}

	matched := 0

	for _, t := range respTokens {
		if _, ok := ctxTokenSet[t]; ok {
			matched++
		}
	}

	return float64(matched) / float64(len(respTokens))
}

// GenericPhraseBand classifies a groundedness score into one of three bands
// used by the learning loop: "boost" when the response is well-grounded,
// "penalize" when it isn't, and "no_op" in the noisy middle.
func GenericPhraseBand(score float64, boostAt, penalizeAt float64) string {
	switch {
	case score >= boostAt:
		return "boost"
	case score < penalizeAt:
		return "penalize"
	default:
		return "no_op"
	}
}

var genericPhrasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)however[, ]+based on my knowledge`),
	regexp.MustCompile(`(?i)\bbased on (my|general) knowledge\b`),
	regexp.MustCompile(`(?i)\bgenerally speaking\b`),
	regexp.MustCompile(`(?i)\bin general\b`),
	regexp.MustCompile(`(?i)\btypically,\b`),
	regexp.MustCompile(`(?i)\busually,\b`),
	regexp.MustCompile(`(?i)\bas a general rule\b`),
	regexp.MustCompile(`(?i)\bin most cases\b`),
	regexp.MustCompile(`(?i)\bcommonly,\b`),
	regexp.MustCompile(`(?i)\boften,\b`),
}

// DetectGenericPhrases returns the list of generic-knowledge phrases the
// response leaks. The phrases signal that the model fell back on training
// data rather than grounding in the supplied context. Empty list is the
// expected case for a passing run.
func DetectGenericPhrases(response string) []string {
	if response == "" {
		return nil
	}

	var hits []string

	seen := make(map[string]struct{})

	for _, pat := range genericPhrasePatterns {
		match := pat.FindString(response)
		if match == "" {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(match))
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		hits = append(hits, key)
	}

	return hits
}
