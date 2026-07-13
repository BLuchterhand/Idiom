package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"idiom/internal/platform/hosttools"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/comprehension"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

type harnessAuditToolSurface struct {
	ManifestToolCount             int                            `json:"manifest_tool_count"`
	SchemaToolCount               int                            `json:"schema_tool_count"`
	ExportedSchemaToolCount       int                            `json:"exported_schema_tool_count"`
	DriftFindingCount             int                            `json:"drift_finding_count"`
	CapabilityToolCounts          map[string]int                 `json:"capability_tool_counts,omitempty"`
	CapabilityClassCounts         map[string]int                 `json:"capability_class_counts,omitempty"`
	DuplicateManifestTools        []string                       `json:"duplicate_manifest_tools,omitempty"`
	ManifestWithoutSchema         []string                       `json:"manifest_without_schema,omitempty"`
	SchemaWithoutManifest         []string                       `json:"schema_without_manifest,omitempty"`
	ManifestWithoutExportedSchema []string                       `json:"manifest_without_exported_schema,omitempty"`
	ExportedSchemaWithoutManifest []string                       `json:"exported_schema_without_manifest,omitempty"`
	SchemaWithoutExportedSchema   []string                       `json:"schema_without_exported_schema,omitempty"`
	ExportedSchemaWithoutSchema   []string                       `json:"exported_schema_without_schema,omitempty"`
	InvalidSchemas                []string                       `json:"invalid_schemas,omitempty"`
	ExportError                   string                         `json:"export_error,omitempty"`
	SuggestionToolCount           int                            `json:"suggestion_tool_count"`
	SuggestionMissingTools        []string                       `json:"suggestion_missing_tools,omitempty"`
	SuggestionWithoutManifest     []string                       `json:"suggestion_without_manifest,omitempty"`
	StaleUseBlockerCoverage       []harnessAuditStaleUseCoverage `json:"stale_use_blocker_coverage,omitempty"`
	StaleUseBlockerMissingTools   []string                       `json:"stale_use_blocker_missing_tools,omitempty"`
	StaleUseReportOnlyTools       []string                       `json:"stale_use_report_only_tools,omitempty"`
	CapabilityRegistry            *ReadOnlyCapabilityCoverage    `json:"capability_registry,omitempty"`
}

type harnessAuditStaleUseCoverage struct {
	Tool                string `json:"tool"`
	State               string `json:"state"`
	DefaultPolicy       string `json:"default_policy"`
	SchemaErrorClass    bool   `json:"schema_error_class"`
	RuntimeBlocker      bool   `json:"runtime_blocker"`
	ReportOnly          bool   `json:"report_only,omitempty"`
	LiveFallbackOnStale bool   `json:"live_fallback_on_stale,omitempty"`
}

type harnessAuditLanguage struct {
	Language     string                     `json:"language"`
	Active       bool                       `json:"active"`
	Extensions   []string                   `json:"extensions,omitempty"`
	Capabilities []harnessAuditLanguageTool `json:"capabilities"`
	Gaps         []string                   `json:"gaps,omitempty"`
}

type harnessAuditLanguageTool struct {
	Capability string `json:"capability"`
	Tool       string `json:"tool,omitempty"`
	InManifest bool   `json:"in_manifest"`
	HasSchema  bool   `json:"has_schema"`
}

type harnessAuditBackendAvailabilityExpectation string
type harnessAuditBackendStatus string

type harnessAuditBackend struct {
	Name                    string                                     `json:"name"`
	Binaries                []string                                   `json:"binaries"`
	AvailabilityExpectation harnessAuditBackendAvailabilityExpectation `json:"availability_expectation"`
	Status                  harnessAuditBackendStatus                  `json:"status"`
	Available               bool                                       `json:"available"`
	ActionRequired          bool                                       `json:"action_required"`
	Path                    string                                     `json:"path,omitempty"`
	Tools                   []string                                   `json:"tools,omitempty"`
	Impact                  string                                     `json:"impact,omitempty"`
	Active                  bool                                       `json:"active,omitempty"`
}

const (
	backendAvailabilityRequired harnessAuditBackendAvailabilityExpectation = "required"
	backendAvailabilityOptional harnessAuditBackendAvailabilityExpectation = "optional"

	backendStatusAvailable           harnessAuditBackendStatus = "available"
	backendStatusInactive            harnessAuditBackendStatus = "inactive"
	backendStatusRequiredUnavailable harnessAuditBackendStatus = "required_unavailable"
	backendStatusOptionalUnavailable harnessAuditBackendStatus = "optional_unavailable"
)

var harnessAuditStandardCapabilities = []semanticlanguage.Capability{
	semanticlanguage.CapOverview,
	semanticlanguage.CapEntrypoints,
	semanticlanguage.CapFindSymbol,
	semanticlanguage.CapWorkspaceSymbol,
	semanticlanguage.CapDefinition,
	semanticlanguage.CapReferences,
	semanticlanguage.CapCallsFrom,
	semanticlanguage.CapCallsInto,
	semanticlanguage.CapReadSymbol,
	semanticlanguage.CapRelatedFiles,
	semanticlanguage.CapImplementations,
	semanticlanguage.CapDiagnostics,
	semanticlanguage.CapFileOutline,
}

func toolSurfaceFindings(surface harnessAuditToolSurface) []harnessAuditFinding {
	var findings []harnessAuditFinding

	if len(surface.ManifestWithoutSchema) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "manifest_without_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d exported tool(s) have no function schema", len(surface.ManifestWithoutSchema)),
			Recommendation: "Add schemas or remove the tools from the exported manifest.",
			Tools:          surface.ManifestWithoutSchema,
		})
	}

	if len(surface.SchemaWithoutManifest) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "schema_without_manifest",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d schema tool(s) are not listed in the exported manifest", len(surface.SchemaWithoutManifest)),
			Recommendation: "Add them to the manifest or delete stale schemas.",
			Tools:          surface.SchemaWithoutManifest,
		})
	}

	if len(surface.ManifestWithoutExportedSchema) > 0 || len(surface.ExportedSchemaWithoutManifest) > 0 || len(surface.SchemaWithoutExportedSchema) > 0 || len(surface.ExportedSchemaWithoutSchema) > 0 || surface.ExportError != "" {
		tools := append(append([]string(nil), surface.ManifestWithoutExportedSchema...), surface.ExportedSchemaWithoutManifest...)

		tools = append(tools, surface.SchemaWithoutExportedSchema...)
		tools = append(tools, surface.ExportedSchemaWithoutSchema...)
		tools = textutil.UniqueNonEmpty(tools)

		evidence := fmt.Sprintf("%d exported-schema drift item(s) detected", len(tools))

		if surface.ExportError != "" {
			evidence = "schema export failed: " + surface.ExportError
		}

		findings = append(findings, harnessAuditFinding{
			ID:             "exported_schema_drift",
			Severity:       "high",
			Evidence:       evidence,
			Recommendation: "reason=exported_schema_drift source=manifest_schema_export",
			Tools:          tools,
		})
	}

	if len(surface.InvalidSchemas) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "invalid_tool_schema",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d tool schema(s) are not valid JSON", len(surface.InvalidSchemas)),
			Recommendation: "Fix the hand-authored JSON schema strings before advertising these tools.",
			Tools:          surface.InvalidSchemas,
		})
	}

	if len(surface.SuggestionMissingTools) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "unsupported_tool_suggestion_gap",
			Severity:       "low",
			Evidence:       fmt.Sprintf("%d manifest tool(s) are absent from typo suggestions", len(surface.SuggestionMissingTools)),
			Recommendation: "Generate typo suggestions from the manifest inventory.",
			Tools:          surface.SuggestionMissingTools,
		})
	}

	if len(surface.SuggestionWithoutManifest) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_tool_suggestion",
			Severity:       "medium",
			Evidence:       fmt.Sprintf("%d suggested tool(s) are absent from the manifest", len(surface.SuggestionWithoutManifest)),
			Recommendation: "reason=stale_tool_suggestion source=unsupported_tool_recovery",
			Tools:          surface.SuggestionWithoutManifest,
		})
	}

	if len(surface.StaleUseBlockerMissingTools) > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "stale_use_blocker_gap",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d graph-backed tool(s) lack stale-context blocker/report coverage", len(surface.StaleUseBlockerMissingTools)),
			Recommendation: "reason=stale_use_blocker_gap source=tool_surface",
			Tools:          surface.StaleUseBlockerMissingTools,
		})
	}

	if surface.CapabilityRegistry != nil && surface.CapabilityRegistry.UnsafeMappedAdapterCount > 0 {
		tools := unsafeCapabilityAdapterTools(surface.CapabilityRegistry.UnsafeMappedAdapters)

		findings = append(findings, harnessAuditFinding{
			ID:             "unsafe_read_only_capability_mapping",
			Severity:       "high",
			Evidence:       fmt.Sprintf("%d read-only capability adapter mapping(s) have non-read-only schema side effects", surface.CapabilityRegistry.UnsafeMappedAdapterCount),
			Recommendation: "Defer mixed/unknown adapters from the read-only capability vocabulary or model them outside the read-only-only registry.",
			Tools:          tools,
		})
	}

	if surface.CapabilityRegistry != nil && surface.CapabilityRegistry.UnmappedAdapterCount > 0 {
		findings = append(findings, harnessAuditFinding{
			ID:             "unmapped_capability_adapter",
			Severity:       "info",
			Evidence:       fmt.Sprintf("%d read-only adapter(s) have neither an exact stable capability mapping nor a documented direct-only classification", surface.CapabilityRegistry.UnmappedAdapterCount),
			Recommendation: "Add an exact existing capability mapping, or document why the tool's direct contract is intentionally outside the resolver.",
			Tools:          append([]string(nil), surface.CapabilityRegistry.UnmappedAdapters...),
		})
	}

	return findings
}

func unsafeCapabilityAdapterTools(adapters []ReadOnlyCapabilityUnsafeAdapterSpec) []string {
	tools := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		if tool := strings.TrimSpace(adapter.Tool); tool != "" {
			tools = append(tools, tool)
		}
	}

	return textutil.UniqueNonEmpty(tools)
}

func backendFindings(backends []harnessAuditBackend) []harnessAuditFinding {
	var findings []harnessAuditFinding

	for _, backend := range backends {
		if backend.Available || !backend.Active || backend.AvailabilityExpectation == backendAvailabilityOptional {
			continue
		}

		severity := "medium"
		if backend.Name == "content_search" || backend.Name == "git" {
			severity = "high"
		}

		findings = append(findings, harnessAuditFinding{
			ID:             "backend_unavailable_" + backend.Name,
			Severity:       severity,
			Evidence:       fmt.Sprintf("%s not found on PATH; checked %s", backend.Name, strings.Join(backend.Binaries, ", ")),
			Recommendation: backend.Impact,
			Tools:          backend.Tools,
		})
	}

	return findings
}

func auditSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}

	return out
}

func auditSetDiff(left map[string]struct{}, right map[string]struct{}) []string {
	var out []string

	for value := range left {
		if _, ok := right[value]; !ok {
			out = append(out, value)
		}
	}

	slices.Sort(out)

	return out
}

func auditDuplicates(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	dups := make(map[string]struct{})

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := seen[value]; ok {
			dups[value] = struct{}{}
			continue
		}

		seen[value] = struct{}{}
	}

	out := make([]string, 0, len(dups))
	for value := range dups {
		out = append(out, value)
	}

	slices.Sort(out)

	return out
}
func (m *Manager) auditToolSurface() harnessAuditToolSurface {
	manifest := toolManifestToolsAcrossBuiltInSurfaces()
	schemaDefs := BuildToolDefinitions(nil)
	export, exportErr := BuildToolSchemaExport(nil)

	return auditToolSurfaceFromSources(manifest, schemaDefs, toolManifestToolsAcrossBuiltInSurfaces(), export, exportErr)
}

func auditToolSurfaceFromSources(manifest []string, schemaDefs []ToolDefinition, suggestions []string, export ToolSchemaExport, exportErr error) harnessAuditToolSurface {
	manifestSet := auditSet(manifest)
	schemaNames, invalidSchemas := auditSchemaDefinitionNames(schemaDefs)
	schemaSet := auditSet(schemaNames)
	suggestSet := auditSet(suggestions)
	exportNames := auditExportedToolNames(export)
	exportSet := auditSet(exportNames)
	coverage, missingBlockers, reportOnlyTools := auditStaleUseBlockerCoverage(export)

	surface := harnessAuditToolSurface{
		ManifestToolCount:             len(manifestSet),
		SchemaToolCount:               len(schemaSet),
		ExportedSchemaToolCount:       len(exportSet),
		DuplicateManifestTools:        auditDuplicates(manifest),
		ManifestWithoutSchema:         auditSetDiff(manifestSet, schemaSet),
		SchemaWithoutManifest:         auditSetDiff(schemaSet, manifestSet),
		ManifestWithoutExportedSchema: auditSetDiff(manifestSet, exportSet),
		ExportedSchemaWithoutManifest: auditSetDiff(exportSet, manifestSet),
		SchemaWithoutExportedSchema:   auditSetDiff(schemaSet, exportSet),
		ExportedSchemaWithoutSchema:   auditSetDiff(exportSet, schemaSet),
		InvalidSchemas:                invalidSchemas,
		SuggestionToolCount:           len(suggestSet),
		SuggestionMissingTools:        auditSetDiff(manifestSet, suggestSet),
		SuggestionWithoutManifest:     auditSetDiff(suggestSet, manifestSet),
		StaleUseBlockerCoverage:       coverage,
		StaleUseBlockerMissingTools:   missingBlockers,
		StaleUseReportOnlyTools:       reportOnlyTools,
		CapabilityToolCounts:          auditCapabilityToolCounts(export),
		CapabilityClassCounts:         auditCapabilityClassCounts(export),
	}
	capabilityCoverage := ReadOnlyCapabilityCoverageForTools(manifest)

	surface.CapabilityRegistry = &capabilityCoverage

	if exportErr != nil {
		surface.ExportError = exportErr.Error()
	}

	surface.DriftFindingCount = toolSurfaceDriftFindingCount(surface)

	return surface
}

func auditSchemaDefinitionNames(defs []ToolDefinition) ([]string, []string) {
	names := make([]string, 0, len(defs))
	invalid := make([]string, 0)

	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		names = append(names, name)
		if !json.Valid([]byte(def.ParametersJSON)) {
			invalid = append(invalid, name)
		}
	}

	slices.Sort(invalid)

	return names, invalid
}

func auditExportedToolNames(export ToolSchemaExport) []string {
	names := make([]string, 0, len(export.Tools))
	for _, tool := range export.Tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			names = append(names, name)
		}
	}

	return names
}

func auditCapabilityToolCounts(export ToolSchemaExport) map[string]int {
	counts := make(map[string]int)

	for _, tool := range export.Tools {
		for _, capability := range tool.EvidenceCapabilities {
			capability = strings.TrimSpace(capability)
			if capability == "" {
				continue
			}

			counts[capability]++
		}
	}

	if len(counts) == 0 {
		return nil
	}

	return counts
}

func auditCapabilityClassCounts(export ToolSchemaExport) map[string]int {
	counts := make(map[string]int)

	for _, tool := range export.Tools {
		class := strings.TrimSpace(tool.CapabilityClassification)
		if class == "" {
			continue
		}

		counts[class]++
	}

	if len(counts) == 0 {
		return nil
	}

	return counts
}

func auditStaleUseBlockerCoverage(export ToolSchemaExport) ([]harnessAuditStaleUseCoverage, []string, []string) {
	exported := make(map[string]ExportedToolSchema, len(export.Tools))
	for _, tool := range export.Tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			exported[name] = tool
		}
	}

	var (
		coverage   []harnessAuditStaleUseCoverage
		missing    []string
		reportOnly []string
	)

	for _, expectation := range staleUseCoverageExpectations() {
		tool, ok := exported[expectation.Tool]
		row := expectation

		if ok && slices.Contains(tool.Policy.ErrorClasses, "stale_context") {
			row.SchemaErrorClass = true
		}

		if !ok || !row.SchemaErrorClass || (!row.RuntimeBlocker && !row.ReportOnly && !row.LiveFallbackOnStale) {
			missing = append(missing, row.Tool)
		}

		if row.ReportOnly {
			reportOnly = append(reportOnly, row.Tool)
		}

		coverage = append(coverage, row)
	}

	slices.Sort(missing)
	slices.Sort(reportOnly)

	return coverage, missing, reportOnly
}

func staleUseCoverageExpectations() []harnessAuditStaleUseCoverage {
	return []harnessAuditStaleUseCoverage{
		{Tool: ToolNameContextStatus, State: "reported", DefaultPolicy: string(comprehension.FreshnessReportOnly), ReportOnly: true},
		{Tool: ToolNameContextQuery, State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: ToolNameContextZoom, State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: ToolNameContextPack, State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: ToolNameContextImpact, State: "blocked", DefaultPolicy: string(comprehension.FreshnessReadThrough), RuntimeBlocker: true},
		{Tool: ToolNameDiscoveryQuery, State: "reported", DefaultPolicy: string(comprehension.FreshnessReadThrough), LiveFallbackOnStale: true},
	}
}

func toolSurfaceDriftFindingCount(surface harnessAuditToolSurface) int {
	capabilityUnsafeCount := 0
	if surface.CapabilityRegistry != nil {
		capabilityUnsafeCount = surface.CapabilityRegistry.UnsafeMappedAdapterCount
	}

	return len(surface.DuplicateManifestTools) +
		len(surface.ManifestWithoutSchema) +
		len(surface.SchemaWithoutManifest) +
		len(surface.ManifestWithoutExportedSchema) +
		len(surface.ExportedSchemaWithoutManifest) +
		len(surface.SchemaWithoutExportedSchema) +
		len(surface.ExportedSchemaWithoutSchema) +
		len(surface.InvalidSchemas) +
		len(surface.SuggestionMissingTools) +
		len(surface.SuggestionWithoutManifest) +
		len(surface.StaleUseBlockerMissingTools) +
		capabilityUnsafeCount +
		boolToAuditCount(surface.ExportError != "")
}

func boolToAuditCount(value bool) int {
	if value {
		return 1
	}

	return 0
}

func (m *Manager) auditLanguageTooling() []harnessAuditLanguage {
	manifest := auditSet(ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}))
	defs := BuildToolDefinitions(nil)
	schemas := make(map[string]struct{}, len(defs))

	for _, def := range defs {
		schemas[def.Name] = struct{}{}
	}

	active := make(map[string]bool)
	for _, lang := range m.ActiveLanguages() {
		active[lang.Name] = true
	}

	languages := semanticlanguage.LanguageRegistry()
	out := make([]harnessAuditLanguage, 0, len(languages))
	for _, lang := range languages {
		row := harnessAuditLanguage{
			Language:   lang.Name,
			Active:     active[lang.Name],
			Extensions: append([]string(nil), lang.Extensions...),
		}
		for _, cap := range harnessAuditStandardCapabilities {
			tool := lang.Tool(cap)
			item := harnessAuditLanguageTool{
				Capability: string(cap),
				Tool:       tool,
			}

			if tool == "" {
				row.Gaps = append(row.Gaps, "missing tool for capability "+string(cap))
			} else {
				_, item.InManifest = manifest[tool]
				_, item.HasSchema = schemas[tool]

				if !item.InManifest {
					row.Gaps = append(row.Gaps, tool+" missing from manifest")
				}

				if !item.HasSchema {
					row.Gaps = append(row.Gaps, tool+" missing schema")
				}
			}

			row.Capabilities = append(row.Capabilities, item)
		}

		out = append(out, row)
	}

	return out
}

func (m *Manager) auditBackendAvailability(languages []harnessAuditLanguage) []harnessAuditBackend {
	active := make(map[string]bool)
	for _, lang := range languages {
		active[lang.Language] = lang.Active
	}

	backends := []harnessAuditBackend{
		{
			Name:                    "content_search",
			Binaries:                contentSearchBackendCandidates(),
			AvailabilityExpectation: backendAvailabilityRequired,
			Tools:                   []string{ToolNameSearchContent},
			Impact:                  "search_content needs ripgrep or GNU grep; without it, generic text search is unavailable.",
			Active:                  true,
		},
		{
			Name:                    "git",
			Binaries:                []string{"git"},
			AvailabilityExpectation: backendAvailabilityRequired,
			Tools:                   []string{ToolNameGitStatus, ToolNameGitDiff, ToolNameHistoryRange, ToolNameHistorySymbol, ToolNameBlameRange},
			Impact:                  "history and blame tools require git.",
			Active:                  true,
		},
		{
			Name:                    "ast_grep",
			Binaries:                []string{"ast-grep", "sg"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameStructuralSearch},
			Impact:                  "structural_search is unavailable without ast-grep.",
			Active:                  true,
		},
		{
			Name:                    "universal_ctags",
			Binaries:                []string{"ctags"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameCtagsSymbols},
			Impact:                  "ctags_symbols is the fallback symbol index for uncovered file types.",
			Active:                  true,
		},
		{
			Name:                    "semgrep",
			Binaries:                []string{"semgrep"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameSemgrepScan},
			Impact:                  "security_review discovery packets degrade when semgrep is absent.",
			Active:                  true,
		},
		{
			Name:                    "joern",
			Binaries:                []string{"joern-scan"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameJoernScan},
			Impact:                  "heavy code-property-graph scanning is unavailable without joern-scan.",
			Active:                  true,
		},
		{
			Name:                    "gopls",
			Binaries:                []string{"gopls"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"go_definition", "go_references", "go_calls_from", "go_calls_into", "go_implementations"},
			Impact:                  "Go tools still have syntax-index fallbacks, but implementation queries and highest precision require gopls.",
			Active:                  active["go"],
		},
		{
			Name:                    "clangd",
			Binaries:                []string{"clangd"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"cpp_definition", "cpp_references", "cpp_calls_from", "cpp_calls_into"},
			Impact:                  "C/C++ precision improves when clangd is available.",
			Active:                  active["cpp"],
		},
		{
			Name:                    "python_lsp",
			Binaries:                []string{"pyright-langserver", "pylsp"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"python_definition", "python_references", "python_diagnostics"},
			Impact:                  "Python location and diagnostics precision improves with pyright or pylsp.",
			Active:                  active["python"],
		},
		{
			Name:                    "typescript_language_server",
			Binaries:                []string{"typescript-language-server"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"ts_definition", "ts_references", "ts_diagnostics"},
			Impact:                  "TypeScript/JavaScript precision improves with typescript-language-server.",
			Active:                  active["typescript"],
		},
		{
			Name:                    "rust_analyzer",
			Binaries:                []string{"rust-analyzer"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"rust_definition", "rust_references", "rust_diagnostics"},
			Impact:                  "Rust precision improves with rust-analyzer; fallbacks use the syntactic index or rg.",
			Active:                  active["rust"],
		},
		{
			Name:                    "jdtls",
			Binaries:                []string{"jdtls"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{"java_definition", "java_references", "java_diagnostics"},
			Impact:                  "Java precision improves with Eclipse JDT-LS.",
			Active:                  active["java"],
		},
		{
			Name:                    "madge",
			Binaries:                []string{"madge"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameDependencyGraph},
			Impact:                  "TypeScript dependency_graph falls back to local import extraction when madge is absent.",
			Active:                  active["typescript"],
		},
		{
			Name:                    "pydeps",
			Binaries:                []string{"pydeps"},
			AvailabilityExpectation: backendAvailabilityOptional,
			Tools:                   []string{ToolNameDependencyGraph},
			Impact:                  "Python dependency_graph falls back to local import extraction when pydeps is absent.",
			Active:                  active["python"],
		},
	}
	for i := range backends {
		if backends[i].Name == "content_search" {
			if backend, err := resolveContentSearchBackend(); err == nil {
				backends[i].Available = true
				backends[i].Path = backend.bin
			}

			continue
		}

		if backends[i].Name == "universal_ctags" {
			if path := auditUniversalCtagsPath(); path != "" {
				backends[i].Available = true
				backends[i].Path = path
			}

			continue
		}

		if path := hosttools.FirstAvailable(backends[i].Binaries...); path != "" {
			backends[i].Available = true
			backends[i].Path = path
		}
	}

	backends = append(backends, m.auditContextGraphBackend())
	for i := range backends {
		classifyHarnessAuditBackend(&backends[i])
	}

	return backends
}

func classifyHarnessAuditBackend(backend *harnessAuditBackend) {
	if backend == nil {
		return
	}
	if backend.AvailabilityExpectation == "" {
		backend.AvailabilityExpectation = backendAvailabilityRequired
	}

	switch {
	case !backend.Active:
		backend.Status = backendStatusInactive
		backend.ActionRequired = false
	case backend.Available:
		backend.Status = backendStatusAvailable
		backend.ActionRequired = false
	case backend.AvailabilityExpectation == backendAvailabilityOptional:
		backend.Status = backendStatusOptionalUnavailable
		backend.ActionRequired = false
	default:
		backend.Status = backendStatusRequiredUnavailable
		backend.ActionRequired = true
	}
}

func (m *Manager) auditContextGraphBackend() harnessAuditBackend {
	backend := harnessAuditBackend{
		Name:                    "context_graph_sqlite",
		Tools:                   []string{ToolNameContextIndex, ToolNameContextStatus, ToolNameContextQuery, ToolNameContextZoom, ToolNameContextPack, ToolNameContextImpact, ToolNameContextTracePack, ToolNameDiscoveryQuery},
		Impact:                  "Context graph tools use the embedded SQLite graph when present; discovery_query falls back to live probes when the graph is stale, collector-version-mismatched, missing, or incompatible.",
		Active:                  true,
		Binaries:                []string{"embedded:modernc.org/sqlite"},
		AvailabilityExpectation: backendAvailabilityRequired,
	}

	status, err := comprehension.Status(m.cfg.Root)
	if err != nil {
		backend.Impact = backend.Impact + " context_status currently errors: " + err.Error()
		return backend
	}

	for _, storage := range status.StorageBackends {
		if storage.Active {
			backend.Path = storage.Path
			backend.Available = status.Indexed && storage.Compatible
			backend.Impact = fmt.Sprintf("%s Active backend %s is %s.", backend.Impact, storage.Kind, storage.Status)

			return backend
		}
	}

	backend.Available = false
	if !status.Indexed {
		backend.Impact = backend.Impact + " No context index is currently present."
	}

	return backend
}

func auditUniversalCtagsPath() string {
	path := hosttools.FirstAvailable("ctags")
	if path == "" {
		return ""
	}

	if !hosttools.VersionOutputContains(context.Background(), path, "universal ctags") {
		return ""
	}

	return path
}
