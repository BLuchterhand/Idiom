package workspace

import (
	"encoding/json"
	"regexp"
	"strings"

	"idiom/internal/contracts/toolfacts"
)

var (
	webMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
	webURLPattern          = regexp.MustCompile(`https?://[^\s<>"'\]\[(){}]+`)
)

// externalWebSourceEvidenceForMCP extracts only explicit source identities
// from successful web-search/fetch content. Search and fetch are intentionally
// different evidence kinds; the caller must not collapse them.
func externalWebSourceEvidenceForMCP(tool string, requestedURL string, parts []json.RawMessage) []toolfacts.ExternalWebSourceEvidence {
	kind, ok := externalWebSourceKindForMCPTool(tool)
	if !ok {
		return nil
	}

	var evidence []toolfacts.ExternalWebSourceEvidence
	for _, raw := range parts {
		var value any
		if err := json.Unmarshal(raw, &value); err == nil {
			evidence = collectExternalWebSources(value, kind, evidence)
			continue
		}
	}

	normalized := toolfacts.NormalizeExternalWebSources(evidence)
	if kind != toolfacts.ExternalWebSourceBody {
		return normalized
	}

	// A fetch response may contain outbound links. Those links are not fetched
	// bodies. Keep body evidence anchored to the URL supplied to the fetch call;
	// if the provider omitted the URL in its response, the successful request is
	// still sufficient to establish that requested identity.
	target := toolfacts.NormalizeExternalWebSources([]toolfacts.ExternalWebSourceEvidence{{
		Kind: toolfacts.ExternalWebSourceBody,
		URL:  requestedURL,
	}})
	if len(target) == 0 {
		return nil
	}
	targetURL := target[0].CanonicalURL
	for _, source := range normalized {
		if source.CanonicalURL == targetURL {
			return []toolfacts.ExternalWebSourceEvidence{source}
		}
	}

	return target
}

func externalWebSourceKindForMCPTool(tool string) (toolfacts.ExternalWebSourceKind, bool) {
	tool = strings.TrimSpace(tool)
	switch {
	case strings.HasSuffix(tool, "/web_search_exa"), strings.HasSuffix(tool, "/web_search"):
		return toolfacts.ExternalWebSourceCandidate, true
	case strings.HasSuffix(tool, "/web_fetch_exa"), strings.HasSuffix(tool, "/web_fetch"):
		return toolfacts.ExternalWebSourceBody, true
	default:
		return "", false
	}
}

func collectExternalWebSources(value any, kind toolfacts.ExternalWebSourceKind, evidence []toolfacts.ExternalWebSourceEvidence) []toolfacts.ExternalWebSourceEvidence {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			evidence = collectExternalWebSources(item, kind, evidence)
		}
	case map[string]any:
		urlValue := externalWebStringField(typed, "url", "source_url", "link", "canonical_url", "canonicalUrl")
		canonicalURL := externalWebStringField(typed, "canonical_url", "canonicalUrl")
		if urlValue != "" || canonicalURL != "" {
			evidence = append(evidence, toolfacts.ExternalWebSourceEvidence{
				Kind:         kind,
				URL:          urlValue,
				CanonicalURL: canonicalURL,
				Title:        externalWebStringField(typed, "title", "name"),
				Publisher:    externalWebStringField(typed, "publisher", "source", "site_name", "siteName"),
			})
		}
		for _, item := range typed {
			evidence = collectExternalWebSources(item, kind, evidence)
		}
	case string:
		evidence = collectExternalWebText(typed, kind, evidence)
	}

	return evidence
}

func collectExternalWebText(text string, kind toolfacts.ExternalWebSourceKind, evidence []toolfacts.ExternalWebSourceEvidence) []toolfacts.ExternalWebSourceEvidence {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return evidence
	}

	// MCP text blocks sometimes contain a JSON-encoded result envelope. Walk
	// that envelope first so title/publisher metadata stays associated with its
	// URL instead of being reduced to a bare regex match.
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var nested any
		if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
			evidence = collectExternalWebSources(nested, kind, evidence)
		}
	}

	for _, match := range webMarkdownLinkPattern.FindAllStringSubmatch(trimmed, -1) {
		evidence = append(evidence, toolfacts.ExternalWebSourceEvidence{
			Kind:  kind,
			URL:   strings.TrimRight(match[2], ".,;:!?"),
			Title: strings.TrimSpace(match[1]),
		})
	}
	for _, match := range webURLPattern.FindAllString(trimmed, -1) {
		evidence = append(evidence, toolfacts.ExternalWebSourceEvidence{
			Kind: kind,
			URL:  strings.TrimRight(match, ".,;:!?"),
		})
	}

	return evidence
}

func externalWebStringField(values map[string]any, names ...string) string {
	for _, name := range names {
		for key, value := range values {
			if !strings.EqualFold(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""), strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", "")) {
				continue
			}
			if text, ok := value.(string); ok {
				if text = strings.TrimSpace(text); text != "" {
					return text
				}
			}
		}
	}
	return ""
}
