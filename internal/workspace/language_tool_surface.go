package workspace

import "strings"

type supplementalLanguageToolSpec struct {
	Language             string
	Tool                 string
	EvidenceCapabilities []string
}

var supplementalLanguageToolSpecs = []supplementalLanguageToolSpec{
	{Language: "go", Tool: "go_find_function", EvidenceCapabilities: []string{"find_symbol"}},
	{Language: "go", Tool: "go_find_type", EvidenceCapabilities: []string{"find_symbol"}},
	{Language: "go", Tool: "go_find_const", EvidenceCapabilities: []string{"find_symbol"}},
	{Language: "go", Tool: "go_find_package", EvidenceCapabilities: []string{"related_files"}},
	{Language: "go", Tool: "go_extract_body", EvidenceCapabilities: []string{"inspect_symbol"}},
	{Language: "go", Tool: "go_importers", EvidenceCapabilities: []string{"related_files"}},
	{Language: "cpp", Tool: "cpp_declaration", EvidenceCapabilities: []string{"find_definition"}},
	{Language: "cpp", Tool: "cpp_extract_body", EvidenceCapabilities: []string{"inspect_symbol"}},
	{Language: "cpp", Tool: "cpp_includes", EvidenceCapabilities: []string{"related_files"}},
	{Language: "cpp", Tool: "cpp_who_includes", EvidenceCapabilities: []string{"related_files"}},
	{Language: "cpp", Tool: "cpp_blast_radius", EvidenceCapabilities: []string{"related_files"}},
	{Language: "cpp", Tool: "cpp_dead_symbol", EvidenceCapabilities: []string{"find_references"}},
}

func supplementalLanguageToolsForManifest(languageName string) []string {
	languageName = strings.ToLower(strings.TrimSpace(languageName))
	if languageName == "" {
		return nil
	}
	var tools []string
	for _, spec := range supplementalLanguageToolSpecs {
		if spec.Language == languageName {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func supplementalLanguageEvidenceCapabilities(tool string) []string {
	tool = strings.ToLower(strings.TrimSpace(tool))
	for _, spec := range supplementalLanguageToolSpecs {
		if spec.Tool == tool {
			return append([]string(nil), spec.EvidenceCapabilities...)
		}
	}
	return nil
}

func isSupplementalLanguageToolName(tool string) bool {
	return len(supplementalLanguageEvidenceCapabilities(tool)) > 0
}
