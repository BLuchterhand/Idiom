package workspace

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

func languageToolDefinitions() []ToolDefinition {
	catalog := languagetools.ToolCatalog()
	defs := make([]ToolDefinition, 0, len(catalog))

	for _, spec := range catalog {
		defs = append(defs, ToolDefinition{
			Name:           strings.TrimSpace(spec.Tool),
			Description:    strings.TrimSpace(spec.Description),
			ParametersJSON: strings.TrimSpace(spec.ParametersJSON),
		})
	}

	return defs
}

func languageToolManifestEntries() []ToolManifestEntry {
	catalog := languagetools.ToolCatalog()
	entries := make([]ToolManifestEntry, 0, len(catalog))

	for _, spec := range catalog {
		entries = append(entries, attachToolManifestCapabilityMetadata(ToolManifestEntry{
			Tool:    strings.TrimSpace(spec.Tool),
			Request: strings.TrimSpace(spec.ManifestRequest),
			Purpose: strings.TrimSpace(spec.ManifestPurpose),
		}))
	}

	return entries
}

func languageToolEvidenceCapabilities(tool string) []string {
	if spec, ok := languagetools.ToolSpecForTool(tool); ok {
		return append([]string(nil), spec.EvidenceCapabilities...)
	}

	return nil
}

func CodeNavigateCapabilityPredicate(capability semanticlanguage.Capability) func(string) bool {
	want := string(capability)

	return func(candidate string) bool {
		return textutil.EqualTrimFold(candidate, want)
	}
}

func CodeNavigateSourceBodyCapabilityNames() []string {
	return []string{
		string(semanticlanguage.CapReadSymbol),
		string(semanticlanguage.CapExtractBody),
	}
}

func CodeNavigateSourceBodyCapabilityPhrase() string {
	names := CodeNavigateSourceBodyCapabilityNames()
	parts := make([]string, 0, len(names))

	for _, name := range names {
		parts = append(parts, fmt.Sprintf("`capability:\"%s\"`", name))
	}

	return strings.Join(parts, " / ")
}

func CodeNavigateCapabilityInstruction(capability semanticlanguage.Capability) string {
	return fmt.Sprintf("`%s` with `capability:\"%s\"`", ToolNameCodeNavigate, string(capability))
}

func CodeNavigateRequestExample(capability semanticlanguage.Capability, path, pattern string) string {
	req := map[string]string{
		"tool":       ToolNameCodeNavigate,
		"capability": string(capability),
	}
	if strings.TrimSpace(path) != "" {
		req["path"] = path
	}
	if strings.TrimSpace(pattern) != "" {
		req["pattern"] = pattern
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Sprintf(`{"tool":"%s","capability":"%s"}`, ToolNameCodeNavigate, capability)
	}

	return string(body)
}
