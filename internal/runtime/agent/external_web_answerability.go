package agent

import (
	"regexp"
	"strings"

	"idiom/internal/contracts/toolfacts"
)

var externalWebURLPattern = regexp.MustCompile(`https?://[^\s<>"'\]\[(){}]+`)

func normalizedExternalWebAnswerURLs(answer string) []string {
	matches := externalWebURLPattern.FindAllString(answer, -1)
	if len(matches) == 0 {
		return nil
	}

	evidence := make([]toolfacts.ExternalWebSourceEvidence, 0, len(matches))
	for _, raw := range matches {
		raw = strings.TrimRight(raw, ".,;:!?`\")")
		if raw == "" {
			continue
		}
		evidence = append(evidence, toolfacts.ExternalWebSourceEvidence{
			Kind: toolfacts.ExternalWebSourceBody,
			URL:  raw,
		})
	}

	normalized := toolfacts.NormalizeExternalWebSources(evidence)
	urls := make([]string, 0, len(normalized))
	for _, source := range normalized {
		urls = append(urls, source.CanonicalURL)
	}
	return urls
}

func answerCitesRelevantExternalWebSource(answer string, ledger readOnlyAnswerabilityLedger) bool {
	mentioned := canonicalExternalWebURLSet(normalizedExternalWebAnswerURLs(answer))
	if len(mentioned) == 0 {
		return false
	}

	for _, source := range ledger.RelevantExternalWebSources {
		if _, ok := mentioned[source]; ok {
			return true
		}
	}
	return false
}

func uniqueCanonicalExternalWebURLs(urls []string) []string {
	return intersectCanonicalExternalWebURLs(urls, urls)
}

func intersectCanonicalExternalWebURLs(preferred []string, allowed []string) []string {
	allowedSet := canonicalExternalWebURLSet(allowed)
	seen := make(map[string]struct{}, len(preferred))
	result := make([]string, 0, len(preferred))
	for _, raw := range preferred {
		canonical := canonicalExternalWebURL(raw)
		if canonical == "" {
			continue
		}
		if _, ok := allowedSet[canonical]; !ok {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result
}

func canonicalExternalWebURLSet(urls []string) map[string]struct{} {
	set := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		canonical := canonicalExternalWebURL(raw)
		if canonical != "" {
			set[canonical] = struct{}{}
		}
	}
	return set
}

func canonicalExternalWebURL(raw string) string {
	normalized := toolfacts.NormalizeExternalWebSources([]toolfacts.ExternalWebSourceEvidence{{
		Kind: toolfacts.ExternalWebSourceBody,
		URL:  raw,
	}})
	if len(normalized) == 0 {
		return ""
	}
	return normalized[0].CanonicalURL
}
