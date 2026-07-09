package languagetools

import (
	"fmt"
	"strings"

	"idiom/internal/platform/tooljsonschema"
	"idiom/internal/repository/semantic/language"
)

type ToolSpec struct {
	Language             string
	Tool                 string
	Capability           language.Capability
	Supplemental         bool
	Description          string
	ParametersJSON       string
	ManifestRequest      string
	ManifestPurpose      string
	EvidenceCapabilities []string
}

type CodeNavigationSpec struct {
	Name            string
	Capability      language.Capability
	RequiresPattern bool
	RequiresPath    bool
	Cost            string
	Class           string
	Aliases         []string
}

type languageManifestToolToken struct {
	Capability   language.Capability
	Supplemental string
}

var codeNavigationSpecs = []CodeNavigationSpec{
	{Name: "file_outline", Capability: language.CapFileOutline, RequiresPath: true, Class: "language_symbol", Cost: "low", Aliases: []string{"file_outline", "outline"}},
	{Name: "find_symbol", Capability: language.CapFindSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low", Aliases: []string{"find_symbol", "symbol", "symbol_lookup", "lookup"}},
	{Name: "workspace_symbol", Capability: language.CapWorkspaceSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low", Aliases: []string{"workspace_symbol", "workspace_symbols", "find_symbol_workspace"}},
	{Name: "read_symbol", Capability: language.CapReadSymbol, RequiresPattern: true, Class: "language_symbol", Cost: "low", Aliases: []string{"read_symbol", "inspect_symbol", "symbol_body"}},
	{Name: "extract_body", Capability: language.CapExtractBody, RequiresPattern: true, Class: "language_symbol", Cost: "low", Aliases: []string{"extract_body", "full_body", "symbol_full_body"}},
	{Name: "definition", Capability: language.CapDefinition, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"definition", "definitions", "declaration", "declarations", "find_definition", "find_definitions", "find_declaration", "find_declarations", "goto_definition", "go_to_definition"}},
	{Name: "references", Capability: language.CapReferences, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"references", "reference", "usages", "find_references", "find_reference"}},
	{Name: "calls_from", Capability: language.CapCallsFrom, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"calls_from", "callees"}},
	{Name: "calls_into", Capability: language.CapCallsInto, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"calls_into", "callers"}},
	{Name: "implementations", Capability: language.CapImplementations, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"implementations", "implementation", "impls"}},
	{Name: "related_files", Capability: language.CapRelatedFiles, RequiresPattern: true, Class: "language_relationship", Cost: "medium", Aliases: []string{"related_files", "related"}},
}

var supplementalLanguageToolSpecs = map[string][]ToolSpec{
	"go": {
		{
			Language:             "go",
			Tool:                 "go_find_function",
			Capability:           language.CapFindSymbol,
			Description:          "Find Go functions matching a pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("function name or regex")),
			ManifestRequest:      `{"tool":"go_find_function","pattern":"FuncName"}`,
			ManifestPurpose:      "find Go functions",
			EvidenceCapabilities: []string{"find_symbol"},
		},
		{
			Language:             "go",
			Tool:                 "go_find_type",
			Capability:           language.CapFindSymbol,
			Description:          "Find Go types matching a pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("type name or regex")),
			ManifestRequest:      `{"tool":"go_find_type","pattern":"TypeName"}`,
			ManifestPurpose:      "find Go types",
			EvidenceCapabilities: []string{"find_symbol"},
		},
		{
			Language:             "go",
			Tool:                 "go_find_const",
			Capability:           language.CapFindSymbol,
			Description:          "Find Go constants matching a pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("constant name or regex")),
			ManifestRequest:      `{"tool":"go_find_const","pattern":"ConstName"}`,
			ManifestPurpose:      "find Go constants",
			EvidenceCapabilities: []string{"find_symbol"},
		},
		{
			Language:             "go",
			Tool:                 "go_find_package",
			Capability:           language.CapRelatedFiles,
			Description:          "Find Go packages matching a pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("package import path or regex")),
			ManifestRequest:      `{"tool":"go_find_package","pattern":"internal/surfaces/tui/app"}`,
			ManifestPurpose:      "find Go packages",
			EvidenceCapabilities: []string{"related_files"},
		},
		{
			Language:             "go",
			Tool:                 "go_importers",
			Capability:           language.CapRelatedFiles,
			Description:          "Find packages that import a given Go package.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("package path")),
			ManifestRequest:      `{"tool":"go_importers","pattern":"internal/surfaces/tui/app"}`,
			ManifestPurpose:      "find Go importer packages",
			EvidenceCapabilities: []string{"related_files"},
		},
	},
	"cpp": {
		{
			Language:             "cpp",
			Tool:                 "cpp_declaration",
			Capability:           language.CapDefinition,
			Description:          "Find a C/C++ symbol declaration.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("symbol")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_declaration","pattern":"Name","path":"."}`,
			ManifestPurpose:      "find C/C++ declarations",
			EvidenceCapabilities: []string{"find_definition"},
		},
		{
			Language:             "cpp",
			Tool:                 "cpp_includes",
			Capability:           language.CapRelatedFiles,
			Description:          "Find direct and transitive C/C++ include dependencies for a file or include pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("file path, header name, or include pattern")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_includes","path":"src/file.cpp"}`,
			ManifestPurpose:      "inspect includes",
			EvidenceCapabilities: []string{"related_files"},
		},
		{
			Language:             "cpp",
			Tool:                 "cpp_who_includes",
			Capability:           language.CapRelatedFiles,
			Description:          "Find C/C++ files that directly or transitively include a file or include pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("file path, header name, or include pattern")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_who_includes","path":"include/file.h"}`,
			ManifestPurpose:      "find include users",
			EvidenceCapabilities: []string{"related_files"},
		},
		{
			Language:             "cpp",
			Tool:                 "cpp_blast_radius",
			Capability:           language.CapRelatedFiles,
			Description:          "Estimate C/C++ translation units affected by a file or include pattern.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("file path, header name, symbol, or include pattern")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_blast_radius","pattern":"Name"}`,
			ManifestPurpose:      "estimate C/C++ blast radius",
			EvidenceCapabilities: []string{"related_files"},
		},
		{
			Language:             "cpp",
			Tool:                 "cpp_dead_symbol",
			Capability:           language.CapReferences,
			Description:          "Check whether a C/C++ symbol appears unused.",
			ParametersJSON:       tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("symbol name")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")),
			ManifestRequest:      `{"tool":"cpp_dead_symbol","pattern":"Name","path":"."}`,
			ManifestPurpose:      "check for dead C/C++ symbols",
			EvidenceCapabilities: []string{"find_references"},
		},
	},
}

var defaultLanguageManifestToolOrder = []languageManifestToolToken{
	{Capability: language.CapOverview},
	{Capability: language.CapEntrypoints},
	{Capability: language.CapFindSymbol},
	{Capability: language.CapReadSymbol},
	{Capability: language.CapRelatedFiles},
	{Capability: language.CapDefinition},
	{Capability: language.CapImplementations},
	{Capability: language.CapReferences},
	{Capability: language.CapCallsFrom},
	{Capability: language.CapCallsInto},
	{Capability: language.CapWorkspaceSymbol},
	{Capability: language.CapDiagnostics},
	{Capability: language.CapFileOutline},
}

var languageManifestToolOrders = map[string][]languageManifestToolToken{
	"go": {
		{Capability: language.CapOverview},
		{Capability: language.CapEntrypoints},
		{Capability: language.CapFindSymbol},
		{Supplemental: "go_find_function"},
		{Supplemental: "go_find_type"},
		{Supplemental: "go_find_const"},
		{Supplemental: "go_find_package"},
		{Capability: language.CapRelatedFiles},
		{Capability: language.CapWorkspaceSymbol},
		{Capability: language.CapReferences},
		{Capability: language.CapDefinition},
		{Capability: language.CapImplementations},
		{Capability: language.CapCallsFrom},
		{Capability: language.CapCallsInto},
		{Capability: language.CapReadSymbol},
		{Capability: language.CapExtractBody},
		{Supplemental: "go_importers"},
		{Capability: language.CapDiagnostics},
		{Capability: language.CapFileOutline},
	},
	"cpp": {
		{Capability: language.CapOverview},
		{Capability: language.CapEntrypoints},
		{Capability: language.CapFindSymbol},
		{Capability: language.CapReadSymbol},
		{Capability: language.CapRelatedFiles},
		{Capability: language.CapReferences},
		{Capability: language.CapDefinition},
		{Supplemental: "cpp_declaration"},
		{Capability: language.CapImplementations},
		{Capability: language.CapCallsFrom},
		{Capability: language.CapCallsInto},
		{Capability: language.CapExtractBody},
		{Supplemental: "cpp_includes"},
		{Supplemental: "cpp_who_includes"},
		{Supplemental: "cpp_blast_radius"},
		{Supplemental: "cpp_dead_symbol"},
		{Capability: language.CapWorkspaceSymbol},
		{Capability: language.CapDiagnostics},
		{Capability: language.CapFileOutline},
	},
	"rust": {
		{Capability: language.CapOverview},
		{Capability: language.CapEntrypoints},
		{Capability: language.CapFindSymbol},
		{Capability: language.CapWorkspaceSymbol},
		{Capability: language.CapReadSymbol},
		{Capability: language.CapRelatedFiles},
		{Capability: language.CapFileOutline},
		{Capability: language.CapDefinition},
		{Capability: language.CapReferences},
		{Capability: language.CapCallsFrom},
		{Capability: language.CapCallsInto},
		{Capability: language.CapImplementations},
		{Capability: language.CapDiagnostics},
	},
}

func ToolCatalog() []ToolSpec {
	var out []ToolSpec

	for _, lang := range language.LanguageRegistry() {
		for _, token := range languageToolOrderFor(lang.Name) {
			switch {
			case token.Capability != "":
				tool := strings.TrimSpace(lang.Tools[token.Capability])
				if tool == "" {
					continue
				}
				out = append(out, toolSpecForCapability(lang.Name, tool, token.Capability))
			case token.Supplemental != "":
				if spec, ok := supplementalLanguageToolSpecFor(lang.Name, token.Supplemental); ok {
					out = append(out, spec)
				}
			}
		}
	}

	return cloneToolSpecs(out)
}

func ToolSpecForTool(tool string) (ToolSpec, bool) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ToolSpec{}, false
	}

	for _, spec := range ToolCatalog() {
		if strings.EqualFold(spec.Tool, tool) {
			return spec, true
		}
	}

	return ToolSpec{}, false
}

func IsToolName(tool string) bool {
	_, ok := ToolSpecForTool(tool)
	return ok
}

func ToolParts(tool string) (languageName string, suffix string, ok bool) {
	spec, ok := ToolSpecForTool(tool)
	if !ok {
		return "", "", false
	}

	tool = strings.TrimSpace(spec.Tool)
	_, suffix, ok = strings.Cut(tool, "_")
	if !ok || suffix == "" {
		return strings.TrimSpace(spec.Language), "", false
	}

	return strings.TrimSpace(spec.Language), suffix, true
}

func ToolHasAnyCapability(tool string, capabilities ...language.Capability) bool {
	spec, ok := ToolSpecForTool(tool)
	if !ok || spec.Capability == "" {
		return false
	}
	for _, capability := range capabilities {
		if spec.Capability == capability {
			return true
		}
	}
	return false
}

func languageToolOrderFor(languageName string) []languageManifestToolToken {
	languageName = strings.ToLower(strings.TrimSpace(languageName))
	if order := languageManifestToolOrders[languageName]; len(order) > 0 {
		return order
	}
	return defaultLanguageManifestToolOrder
}

func toolSpecForCapability(languageName, tool string, capability language.Capability) ToolSpec {
	return ToolSpec{
		Language:             strings.ToLower(strings.TrimSpace(languageName)),
		Tool:                 strings.TrimSpace(tool),
		Capability:           capability,
		Description:          languageToolDescription(languageName, capability),
		ParametersJSON:       languageToolParametersJSON(capability),
		ManifestRequest:      languageToolManifestRequest(tool, languageName, capability),
		ManifestPurpose:      languageToolManifestPurpose(languageName, capability),
		EvidenceCapabilities: languageCapabilityEvidenceCapabilities(capability),
	}
}

func supplementalLanguageToolSpecFor(languageName, tool string) (ToolSpec, bool) {
	languageName = strings.ToLower(strings.TrimSpace(languageName))
	tool = strings.TrimSpace(tool)
	for _, spec := range supplementalLanguageToolSpecs[languageName] {
		if spec.Tool == tool {
			spec.Language = strings.ToLower(strings.TrimSpace(spec.Language))
			spec.Tool = strings.TrimSpace(spec.Tool)
			spec.Supplemental = true
			spec.Description = strings.TrimSpace(spec.Description)
			spec.ParametersJSON = strings.TrimSpace(spec.ParametersJSON)
			spec.ManifestRequest = strings.TrimSpace(spec.ManifestRequest)
			spec.ManifestPurpose = strings.TrimSpace(spec.ManifestPurpose)
			return spec, true
		}
	}
	return ToolSpec{}, false
}

func cloneToolSpecs(specs []ToolSpec) []ToolSpec {
	out := make([]ToolSpec, 0, len(specs))
	for _, spec := range specs {
		cloned := spec
		cloned.EvidenceCapabilities = append([]string(nil), spec.EvidenceCapabilities...)
		out = append(out, cloned)
	}
	return out
}

func languageCapabilityEvidenceCapabilities(capability language.Capability) []string {
	if id := evidenceCapabilityForLanguageCapability(capability); id != "" {
		return []string{id}
	}
	return nil
}

func evidenceCapabilityForLanguageCapability(capability language.Capability) string {
	switch capability {
	case language.CapFileOutline, language.CapReadSymbol, language.CapExtractBody:
		return "inspect_symbol"
	case language.CapFindSymbol, language.CapWorkspaceSymbol:
		return "find_symbol"
	case language.CapDefinition:
		return "find_definition"
	case language.CapReferences:
		return "find_references"
	case language.CapImplementations:
		return "find_implementations"
	case language.CapCallsFrom:
		return "calls_from"
	case language.CapCallsInto:
		return "calls_into"
	case language.CapRelatedFiles, language.CapOverview, language.CapEntrypoints:
		return "related_files"
	case language.CapDiagnostics:
		return "source_diagnostics"
	default:
		return ""
	}
}

func languageToolDescription(languageName string, capability language.Capability) string {
	label := languageDisplayName(languageName)
	switch capability {
	case language.CapOverview:
		return fmt.Sprintf("Summarize the %s workspace structure.", label)
	case language.CapEntrypoints:
		return fmt.Sprintf("List likely %s entrypoints.", label)
	case language.CapFindSymbol:
		return fmt.Sprintf("Find %s symbols matching a pattern.", label)
	case language.CapWorkspaceSymbol:
		return fmt.Sprintf("Workspace-wide %s symbol lookup.", label)
	case language.CapDefinition:
		return fmt.Sprintf("Find a %s symbol definition.", label)
	case language.CapReferences:
		return fmt.Sprintf("Find references to a %s symbol.", label)
	case language.CapCallsFrom:
		return fmt.Sprintf("Find outgoing calls from a %s function or method.", label)
	case language.CapCallsInto:
		return fmt.Sprintf("Find incoming calls to a %s function or method.", label)
	case language.CapReadSymbol:
		return fmt.Sprintf("Read a %s symbol body. Anchor first with a symbol lookup or prior tool result; use search_content for literal text inside files.", label)
	case language.CapExtractBody:
		return fmt.Sprintf("Extract the full body of a %s function or method.", label)
	case language.CapRelatedFiles:
		return fmt.Sprintf("Find %s files related to a pattern or symbol.", label)
	case language.CapImplementations:
		return fmt.Sprintf("Find %s implementations for an interface, trait, base type, or method.", label)
	case language.CapDiagnostics:
		return fmt.Sprintf("Run %s diagnostics when the language backend is available.", label)
	case language.CapFileOutline:
		return fmt.Sprintf("List declarations with line ranges in a %s file.", label)
	default:
		return fmt.Sprintf("Run %s capability %s.", label, capability)
	}
}

func languageToolParametersJSON(capability language.Capability) string {
	pathDesc := "optional file or directory scope"
	if capability == language.CapFileOutline || capability == language.CapExtractBody {
		pathDesc = "file path"
	}

	switch capability {
	case language.CapOverview:
		return `{"type":"object","properties":{}}`
	case language.CapEntrypoints:
		return tooljsonschema.Object(nil, `"path":`+tooljsonschema.StringProperty("optional workspace scope")+`,"max_depth":`+tooljsonschema.IntegerProperty("optional result limit"))
	case language.CapFileOutline, language.CapDiagnostics:
		return tooljsonschema.Object([]string{"path"}, `"path":`+tooljsonschema.StringProperty(pathDesc))
	case language.CapReadSymbol:
		return tooljsonschema.Object([]string{"pattern", "path"}, `"pattern":`+tooljsonschema.StringProperty("symbol name or regex")+`,"path":`+tooljsonschema.StringProperty("file path")+`,"start_line":`+tooljsonschema.IntegerProperty("optional 1-based start")+`,"end_line":`+tooljsonschema.IntegerProperty("optional 1-based end"))
	case language.CapExtractBody:
		return tooljsonschema.Object([]string{"pattern", "path"}, `"pattern":`+tooljsonschema.StringProperty("symbol name or regex")+`,"path":`+tooljsonschema.StringProperty(pathDesc)+`,"start_line":`+tooljsonschema.IntegerProperty("optional 1-based start")+`,"end_line":`+tooljsonschema.IntegerProperty("optional 1-based end"))
	case language.CapFindSymbol, language.CapWorkspaceSymbol:
		return tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("symbol name or regex")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")+`,"max_depth":`+tooljsonschema.IntegerProperty("optional result limit"))
	case language.CapRelatedFiles:
		return tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("term or symbol")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope")+`,"max_depth":`+tooljsonschema.IntegerProperty("optional result limit"))
	case language.CapDefinition, language.CapReferences, language.CapCallsFrom, language.CapCallsInto, language.CapImplementations:
		return tooljsonschema.Object([]string{"pattern"}, `"pattern":`+tooljsonschema.StringProperty("symbol name or regex")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope"))
	default:
		return tooljsonschema.Object(nil, `"pattern":`+tooljsonschema.StringProperty("optional symbol or query")+`,"path":`+tooljsonschema.StringProperty("optional file or directory scope"))
	}
}

func languageToolManifestRequest(tool, languageName string, capability language.Capability) string {
	switch capability {
	case language.CapOverview, language.CapEntrypoints:
		return fmt.Sprintf(`{"tool":%q}`, tool)
	case language.CapFileOutline, language.CapDiagnostics:
		return fmt.Sprintf(`{"tool":%q,"path":%q}`, tool, sampleLanguagePath(languageName))
	case language.CapReadSymbol, language.CapExtractBody:
		return fmt.Sprintf(`{"tool":%q,"pattern":"Name","path":%q}`, tool, sampleLanguagePath(languageName))
	default:
		return fmt.Sprintf(`{"tool":%q,"pattern":"Name","path":"."}`, tool)
	}
}

func languageToolManifestPurpose(languageName string, capability language.Capability) string {
	label := languageDisplayName(languageName)
	switch capability {
	case language.CapOverview:
		return fmt.Sprintf("summarize %s workspace", label)
	case language.CapEntrypoints:
		return fmt.Sprintf("find %s entrypoints", label)
	case language.CapFileOutline:
		return fmt.Sprintf("list %s declaration boundaries", label)
	case language.CapDiagnostics:
		return fmt.Sprintf("run %s diagnostics", label)
	case language.CapReadSymbol:
		return fmt.Sprintf("read a %s symbol body", label)
	case language.CapExtractBody:
		return fmt.Sprintf("extract a full %s body", label)
	case language.CapFindSymbol, language.CapWorkspaceSymbol:
		return fmt.Sprintf("find %s symbols", label)
	case language.CapDefinition:
		return fmt.Sprintf("find %s definitions", label)
	case language.CapReferences:
		return fmt.Sprintf("find %s references", label)
	case language.CapCallsFrom:
		return fmt.Sprintf("find outgoing %s calls", label)
	case language.CapCallsInto:
		return fmt.Sprintf("find incoming %s calls", label)
	case language.CapImplementations:
		return fmt.Sprintf("find %s implementations", label)
	case language.CapRelatedFiles:
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
