package workspace

import (
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/platform/textutil"
)

const CodeNavigateToolName = ToolNameCodeNavigate

type languageToolCatalogEntry struct {
	Language             string
	Tool                 string
	Capability           Capability
	Supplemental         bool
	Description          string
	ParametersJSON       string
	ManifestRequest      string
	ManifestPurpose      string
	EvidenceCapabilities []string
}

type supplementalLanguageToolSpec struct {
	Tool                 string
	Description          string
	ParametersJSON       string
	ManifestRequest      string
	ManifestPurpose      string
	EvidenceCapabilities []string
}

var supplementalLanguageToolSpecs = map[string][]supplementalLanguageToolSpec{
	"go": {
		{
			Tool:                 ToolNameGoFindFunction,
			Description:          "Find Go functions matching a pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("function name or regex")),
			ManifestRequest:      `{"tool":"go_find_function","pattern":"FuncName"}`,
			ManifestPurpose:      "find Go functions",
			EvidenceCapabilities: []string{EvidenceCapabilityFindSymbol},
		},
		{
			Tool:                 ToolNameGoFindType,
			Description:          "Find Go types matching a pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("type name or regex")),
			ManifestRequest:      `{"tool":"go_find_type","pattern":"TypeName"}`,
			ManifestPurpose:      "find Go types",
			EvidenceCapabilities: []string{EvidenceCapabilityFindSymbol},
		},
		{
			Tool:                 ToolNameGoFindConst,
			Description:          "Find Go constants matching a pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("constant name or regex")),
			ManifestRequest:      `{"tool":"go_find_const","pattern":"ConstName"}`,
			ManifestPurpose:      "find Go constants",
			EvidenceCapabilities: []string{EvidenceCapabilityFindSymbol},
		},
		{
			Tool:                 ToolNameGoFindPackage,
			Description:          "Find Go packages matching a pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("package import path or regex")),
			ManifestRequest:      `{"tool":"go_find_package","pattern":"internal/surfaces/tui/app"}`,
			ManifestPurpose:      "find Go packages",
			EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles},
		},
		{
			Tool:                 ToolNameGoImporters,
			Description:          "Find packages that import a given Go package.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("package path")),
			ManifestRequest:      `{"tool":"go_importers","pattern":"internal/surfaces/tui/app"}`,
			ManifestPurpose:      "find Go importer packages",
			EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles},
		},
	},
	"cpp": {
		{
			Tool:                 ToolNameCppDeclaration,
			Description:          "Find a C/C++ symbol declaration.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("symbol")+`,"path":`+strProp("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_declaration","pattern":"Name","path":"."}`,
			ManifestPurpose:      "find C/C++ declarations",
			EvidenceCapabilities: []string{EvidenceCapabilityFindDefinition},
		},
		{
			Tool:                 ToolNameCppIncludes,
			Description:          "Find direct and transitive C/C++ include dependencies for a file or include pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("file path, header name, or include pattern")+`,"path":`+strProp("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_includes","path":"src/file.cpp"}`,
			ManifestPurpose:      "inspect includes",
			EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles},
		},
		{
			Tool:                 ToolNameCppWhoIncludes,
			Description:          "Find C/C++ files that directly or transitively include a file or include pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("file path, header name, or include pattern")+`,"path":`+strProp("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_who_includes","path":"include/file.h"}`,
			ManifestPurpose:      "find include users",
			EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles},
		},
		{
			Tool:                 ToolNameCppBlastRadius,
			Description:          "Estimate C/C++ translation units affected by a file or include pattern.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("file path, header name, symbol, or include pattern")+`,"path":`+strProp("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_blast_radius","pattern":"Name"}`,
			ManifestPurpose:      "estimate C/C++ blast radius",
			EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles},
		},
		{
			Tool:                 ToolNameCppDeadSymbol,
			Description:          "Check whether a C/C++ symbol appears unused.",
			ParametersJSON:       schemaObj([]string{"pattern"}, `"pattern":`+strProp("symbol name")+`,"path":`+strProp("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_dead_symbol","pattern":"Name","path":"."}`,
			ManifestPurpose:      "check for dead C/C++ symbols",
			EvidenceCapabilities: []string{EvidenceCapabilityFindReferences},
		},
	},
}

type languageManifestToolToken struct {
	Capability   Capability
	Supplemental string
}

var defaultLanguageManifestToolOrder = []languageManifestToolToken{
	{Capability: CapOverview},
	{Capability: CapEntrypoints},
	{Capability: CapFindSymbol},
	{Capability: CapReadSymbol},
	{Capability: CapRelatedFiles},
	{Capability: CapDefinition},
	{Capability: CapImplementations},
	{Capability: CapReferences},
	{Capability: CapCallsFrom},
	{Capability: CapCallsInto},
	{Capability: CapWorkspaceSymbol},
	{Capability: CapDiagnostics},
	{Capability: CapFileOutline},
}

var languageManifestToolOrders = map[string][]languageManifestToolToken{
	"go": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Supplemental: ToolNameGoFindFunction},
		{Supplemental: ToolNameGoFindType},
		{Supplemental: ToolNameGoFindConst},
		{Supplemental: ToolNameGoFindPackage},
		{Capability: CapRelatedFiles},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapReadSymbol},
		{Capability: CapExtractBody},
		{Supplemental: ToolNameGoImporters},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"cpp": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Supplemental: ToolNameCppDeclaration},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapExtractBody},
		{Supplemental: ToolNameCppIncludes},
		{Supplemental: ToolNameCppWhoIncludes},
		{Supplemental: ToolNameCppBlastRadius},
		{Supplemental: ToolNameCppDeadSymbol},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"python": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapReferences},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"typescript": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapDefinition},
		{Capability: CapImplementations},
		{Capability: CapReferences},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
	"rust": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapFileOutline},
		{Capability: CapDefinition},
		{Capability: CapReferences},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapImplementations},
		{Capability: CapDiagnostics},
	},
	"java": {
		{Capability: CapOverview},
		{Capability: CapEntrypoints},
		{Capability: CapFindSymbol},
		{Capability: CapReadSymbol},
		{Capability: CapRelatedFiles},
		{Capability: CapWorkspaceSymbol},
		{Capability: CapDefinition},
		{Capability: CapReferences},
		{Capability: CapImplementations},
		{Capability: CapCallsFrom},
		{Capability: CapCallsInto},
		{Capability: CapDiagnostics},
		{Capability: CapFileOutline},
	},
}

func languageToolCatalog() []languageToolCatalogEntry {
	var out []languageToolCatalogEntry

	for _, lang := range LanguageRegistry() {
		for _, token := range languageToolOrderFor(lang.Name) {
			switch {
			case token.Capability != "":
				tool := strings.TrimSpace(lang.Tools[token.Capability])
				if tool == "" {
					continue
				}

				out = append(out, languageToolEntryForCapability(lang.Name, tool, token.Capability))
			case token.Supplemental != "":
				if spec, ok := supplementalLanguageToolSpecFor(lang.Name, token.Supplemental); ok {
					out = append(out, languageToolCatalogEntry{
						Language:             lang.Name,
						Tool:                 spec.Tool,
						Supplemental:         true,
						Description:          spec.Description,
						ParametersJSON:       spec.ParametersJSON,
						ManifestRequest:      spec.ManifestRequest,
						ManifestPurpose:      spec.ManifestPurpose,
						EvidenceCapabilities: append([]string(nil), spec.EvidenceCapabilities...),
					})
				}
			}
		}
	}

	return out
}

func languageToolOrderFor(languageName string) []languageManifestToolToken {
	languageName = strings.ToLower(strings.TrimSpace(languageName))
	if order := languageManifestToolOrders[languageName]; len(order) > 0 {
		return order
	}

	return defaultLanguageManifestToolOrder
}

func languageToolEntryForCapability(languageName, tool string, capability Capability) languageToolCatalogEntry {
	return languageToolCatalogEntry{
		Language:             strings.ToLower(strings.TrimSpace(languageName)),
		Tool:                 strings.TrimSpace(tool),
		Capability:           capability,
		Description:          languageToolDescription(languageName, capability),
		ParametersJSON:       languageToolParametersJSON(languageName, capability),
		ManifestRequest:      languageToolManifestRequest(tool, languageName, capability),
		ManifestPurpose:      languageToolManifestPurpose(languageName, capability),
		EvidenceCapabilities: languageCapabilityEvidenceCapabilities(capability),
	}
}

func supplementalLanguageToolSpecFor(languageName, tool string) (supplementalLanguageToolSpec, bool) {
	languageName = strings.ToLower(strings.TrimSpace(languageName))
	tool = strings.TrimSpace(tool)

	specs, ok := supplementalLanguageToolSpecs[languageName]

	if !ok {
		return supplementalLanguageToolSpec{}, false
	}

	for _, spec := range specs {
		if spec.Tool == tool {
			return spec, true
		}
	}

	return supplementalLanguageToolSpec{}, false
}

func languageToolCatalogEntryForTool(tool string) (languageToolCatalogEntry, bool) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return languageToolCatalogEntry{}, false
	}

	for _, entry := range languageToolCatalog() {
		if strings.EqualFold(entry.Tool, tool) {
			return entry, true
		}
	}

	return languageToolCatalogEntry{}, false
}

func languageToolDefinitions() []ToolDefinition {
	catalog := languageToolCatalog()
	defs := make([]ToolDefinition, 0, len(catalog))

	for _, entry := range catalog {
		defs = append(defs, ToolDefinition{
			Name:           entry.Tool,
			Description:    entry.Description,
			ParametersJSON: entry.ParametersJSON,
		})
	}

	return defs
}

func languageToolManifestEntries() []ToolManifestEntry {
	catalog := languageToolCatalog()
	entries := make([]ToolManifestEntry, 0, len(catalog))

	for _, entry := range catalog {
		entries = append(entries, attachToolManifestCapabilityMetadata(ToolManifestEntry{
			Tool:    entry.Tool,
			Request: entry.ManifestRequest,
			Purpose: entry.ManifestPurpose,
		}))
	}

	return entries
}

func languageToolCatalogToolNames() []string {
	catalog := languageToolCatalog()
	out := make([]string, 0, len(catalog))

	for _, entry := range catalog {
		out = append(out, entry.Tool)
	}

	return out
}

func languageToolEvidenceCapabilities(tool string) []string {
	if entry, ok := languageToolCatalogEntryForTool(tool); ok {
		return append([]string(nil), entry.EvidenceCapabilities...)
	}

	return nil
}

func LanguageToolCapability(tool string) (Capability, bool) {
	entry, ok := languageToolCatalogEntryForTool(tool)
	if !ok || entry.Capability == "" {
		return "", false
	}

	return entry.Capability, true
}

func LanguageToolHasCapability(tool string, capability Capability) bool {
	found, ok := LanguageToolCapability(tool)
	return ok && found == capability
}

func LanguageToolHasAnyCapability(tool string, capabilities ...Capability) bool {
	found, ok := LanguageToolCapability(tool)
	if !ok {
		return false
	}

	for _, capability := range capabilities {
		if found == capability {
			return true
		}
	}

	return false
}

func LanguageToolForCapability(language string, capability Capability) string {
	lang, ok := LanguageForName(language)
	if !ok {
		return ""
	}

	return strings.TrimSpace(lang.Tool(capability))
}

func isLanguageToolName(tool string) bool {
	_, ok := languageToolCatalogEntryForTool(tool)
	return ok
}

func languageCapabilityEvidenceCapabilities(capability Capability) []string {
	if id := evidenceCapabilityForLanguageCapability(capability); id != "" {
		return []string{id}
	}

	return nil
}

func languageToolDescription(languageName string, capability Capability) string {
	label := languageDisplayName(languageName)

	switch capability {
	case CapOverview:
		return fmt.Sprintf("Summarize the %s workspace structure.", label)
	case CapEntrypoints:
		return fmt.Sprintf("List likely %s entrypoints.", label)
	case CapFindSymbol:
		return fmt.Sprintf("Find %s symbols matching a pattern.", label)
	case CapWorkspaceSymbol:
		return fmt.Sprintf("Workspace-wide %s symbol lookup.", label)
	case CapDefinition:
		return fmt.Sprintf("Find a %s symbol definition.", label)
	case CapReferences:
		return fmt.Sprintf("Find references to a %s symbol.", label)
	case CapCallsFrom:
		return fmt.Sprintf("Find outgoing calls from a %s function or method.", label)
	case CapCallsInto:
		return fmt.Sprintf("Find incoming calls to a %s function or method.", label)
	case CapReadSymbol:
		return fmt.Sprintf("Read a %s symbol body. Anchor first with a symbol lookup or prior tool result; use search_content for literal text inside files.", label)
	case CapExtractBody:
		return fmt.Sprintf("Extract the full body of a %s function or method.", label)
	case CapRelatedFiles:
		return fmt.Sprintf("Find %s files related to a pattern or symbol.", label)
	case CapImplementations:
		return fmt.Sprintf("Find %s implementations for an interface, trait, base type, or method.", label)
	case CapDiagnostics:
		return fmt.Sprintf("Run %s diagnostics when the language backend is available.", label)
	case CapFileOutline:
		return fmt.Sprintf("List declarations with line ranges in a %s file.", label)
	default:
		return fmt.Sprintf("Run %s capability %s.", label, capability)
	}
}

func languageToolParametersJSON(languageName string, capability Capability) string {
	pathDesc := "optional file or directory scope"
	if capability == CapFileOutline || capability == CapExtractBody {
		pathDesc = "file path"
	}

	switch capability {
	case CapOverview:
		return `{"type":"object","properties":{}}`
	case CapEntrypoints:
		return schemaObj(nil, `"path":`+strProp("optional workspace scope")+`,"max_depth":`+intProp("optional result limit"))
	case CapFileOutline, CapDiagnostics:
		return schemaObj([]string{"path"}, `"path":`+strProp(pathDesc))
	case CapReadSymbol:
		return schemaObj([]string{"pattern", "path"}, `"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("file path")+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"))
	case CapExtractBody:
		return schemaObj([]string{"pattern", "path"}, `"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp(pathDesc)+`,"start_line":`+intProp("optional 1-based start")+`,"end_line":`+intProp("optional 1-based end"))
	case CapFindSymbol, CapWorkspaceSymbol:
		return schemaObj([]string{"pattern"}, `"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("optional file or directory scope")+`,"max_depth":`+intProp("optional result limit"))
	case CapRelatedFiles:
		return schemaObj([]string{"pattern"}, `"pattern":`+strProp("term or symbol")+`,"path":`+strProp("optional file or directory scope")+`,"max_depth":`+intProp("optional result limit"))
	case CapDefinition, CapReferences, CapCallsFrom, CapCallsInto, CapImplementations:
		return schemaObj([]string{"pattern"}, `"pattern":`+strProp("symbol name or regex")+`,"path":`+strProp("optional file or directory scope"))
	default:
		_ = languageName
		return schemaObj(nil, `"pattern":`+strProp("optional symbol or query")+`,"path":`+strProp("optional file or directory scope"))
	}
}

func languageToolManifestRequest(tool, languageName string, capability Capability) string {
	switch capability {
	case CapOverview:
		return fmt.Sprintf(`{"tool":%q}`, tool)
	case CapEntrypoints:
		return fmt.Sprintf(`{"tool":%q}`, tool)
	case CapFileOutline:
		return fmt.Sprintf(`{"tool":%q,"path":%q}`, tool, sampleLanguagePath(languageName))
	case CapDiagnostics:
		return fmt.Sprintf(`{"tool":%q,"path":%q}`, tool, sampleLanguagePath(languageName))
	case CapReadSymbol, CapExtractBody:
		return fmt.Sprintf(`{"tool":%q,"pattern":"Name","path":%q}`, tool, sampleLanguagePath(languageName))
	default:
		return fmt.Sprintf(`{"tool":%q,"pattern":"Name","path":"."}`, tool)
	}
}

func languageToolManifestPurpose(languageName string, capability Capability) string {
	label := languageDisplayName(languageName)

	switch capability {
	case CapOverview:
		return fmt.Sprintf("summarize %s workspace", label)
	case CapEntrypoints:
		return fmt.Sprintf("find %s entrypoints", label)
	case CapFileOutline:
		return fmt.Sprintf("list %s declaration boundaries", label)
	case CapDiagnostics:
		return fmt.Sprintf("run %s diagnostics", label)
	case CapReadSymbol:
		return fmt.Sprintf("read a %s symbol body", label)
	case CapExtractBody:
		return fmt.Sprintf("extract a full %s body", label)
	case CapFindSymbol, CapWorkspaceSymbol:
		return fmt.Sprintf("find %s symbols", label)
	case CapDefinition:
		return fmt.Sprintf("find %s definitions", label)
	case CapReferences:
		return fmt.Sprintf("find %s references", label)
	case CapCallsFrom:
		return fmt.Sprintf("find outgoing %s calls", label)
	case CapCallsInto:
		return fmt.Sprintf("find incoming %s calls", label)
	case CapImplementations:
		return fmt.Sprintf("find %s implementations", label)
	case CapRelatedFiles:
		return fmt.Sprintf("find related %s files", label)
	default:
		return fmt.Sprintf("run %s capability %s", label, capability)
	}
}

func languageDisplayName(languageName string) string {
	switch strings.ToLower(strings.TrimSpace(languageName)) {
	case "go":
		return "Go"
	case "cpp":
		return "C/C++"
	case "python":
		return "Python"
	case "typescript":
		return "TypeScript/JavaScript"
	case "rust":
		return "Rust"
	case "java":
		return "Java"
	default:
		return strings.TrimSpace(languageName)
	}
}

func sampleLanguagePath(languageName string) string {
	switch strings.ToLower(strings.TrimSpace(languageName)) {
	case "go":
		return "relative/file.go"
	case "cpp":
		return "src/file.cpp"
	case "python":
		return "src/pkg/file.py"
	case "typescript":
		return "src/file.ts"
	case "rust":
		return "src/file.rs"
	case "java":
		return "src/main/java/com/example/File.java"
	default:
		return "relative/file"
	}
}

func CodeNavigateCapabilityName(capability Capability) string {
	return string(capability)
}

func CodeNavigateCapabilityPredicate(capability Capability) func(string) bool {
	want := CodeNavigateCapabilityName(capability)

	return func(candidate string) bool {
		return textutil.EqualTrimFold(candidate, want)
	}
}

func CodeNavigateSourceBodyCapabilityNames() []string {
	return []string{
		CodeNavigateCapabilityName(CapReadSymbol),
		CodeNavigateCapabilityName(CapExtractBody),
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

func CodeNavigateCapabilityInstruction(capability Capability) string {
	return fmt.Sprintf("`%s` with `capability:\"%s\"`", CodeNavigateToolName, CodeNavigateCapabilityName(capability))
}

func CodeNavigateRequestExample(capability Capability, path, pattern string) string {
	if strings.TrimSpace(pattern) == "" {
		pattern = "<symbol name>"
	}

	request := map[string]string{
		"tool":       CodeNavigateToolName,
		"capability": CodeNavigateCapabilityName(capability),
		"path":       path,
		"pattern":    pattern,
	}

	encoded, err := json.Marshal(request)
	if err != nil {
		return ""
	}

	return string(encoded)
}
