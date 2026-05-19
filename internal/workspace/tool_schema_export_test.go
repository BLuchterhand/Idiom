package workspace

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"idiom/internal/autonomy"
)

func TestBuildToolSchemaExportDerivesFromManifestAndSchemas(t *testing.T) {
	export, err := BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport returned error: %v", err)
	}
	if export.SchemaVersion != ToolSchemaExportVersion {
		t.Fatalf("unexpected schema version %q", export.SchemaVersion)
	}
	if len(export.Metadata.ClientClassifications) != len(autonomy.DefaultClientClassifications()) {
		t.Fatalf("export should carry all generic client classifications: %#v", export.Metadata.ClientClassifications)
	}
	for _, want := range autonomy.DefaultClientClassifications() {
		if !slices.Contains(export.Metadata.ClientClassifications, want) {
			t.Fatalf("export missing client classification %q: %#v", want, export.Metadata.ClientClassifications)
		}
	}
	if len(export.Metadata.ProductSignals) != len(autonomy.DefaultProductSignals()) {
		t.Fatalf("export should carry all generic product signals: %#v", export.Metadata.ProductSignals)
	}
	for _, want := range autonomy.DefaultProductSignals() {
		if !slices.Contains(export.Metadata.ProductSignals, want) {
			t.Fatalf("export missing product signal %q: %#v", want, export.Metadata.ProductSignals)
		}
	}

	exported := make(map[string]ExportedToolSchema, len(export.Tools))
	for _, tool := range export.Tools {
		if tool.Name == "" {
			t.Fatalf("export contains tool with empty name: %#v", tool)
		}
		if _, exists := exported[tool.Name]; exists {
			t.Fatalf("duplicate exported tool %q", tool.Name)
		}
		if !json.Valid(tool.InputSchema) {
			t.Fatalf("exported schema for %s is invalid JSON: %s", tool.Name, string(tool.InputSchema))
		}
		if len(tool.Profiles) == 0 {
			t.Fatalf("exported tool %s missing profile metadata", tool.Name)
		}
		if len(tool.Policy.SideEffects) == 0 {
			t.Fatalf("exported tool %s missing side-effect metadata", tool.Name)
		}
		for _, want := range []string{"invalid_input", "unsupported_tool"} {
			if !slices.Contains(tool.Policy.ErrorClasses, want) {
				t.Fatalf("exported tool %s missing error class %q: %#v", tool.Name, want, tool.Policy.ErrorClasses)
			}
		}
		exported[tool.Name] = tool
	}

	manifestNames := make(map[string]struct{})
	for _, entry := range BuildToolManifest(nil) {
		manifestNames[entry.Tool] = struct{}{}
	}
	if len(exported) != len(manifestNames) {
		t.Fatalf("exported tool count %d does not match manifest count %d", len(exported), len(manifestNames))
	}
	for name := range manifestNames {
		if _, ok := exported[name]; !ok {
			t.Fatalf("manifest tool %q missing from export", name)
		}
	}

	applyPatch := exported["apply_patch"]
	if !slices.Contains(applyPatch.Policy.SideEffects, autonomy.SideEffectAdditive) ||
		!slices.Contains(applyPatch.Policy.SideEffects, autonomy.SideEffectDestructive) {
		t.Fatalf("apply_patch should expose additive and destructive side effects: %#v", applyPatch.Policy.SideEffects)
	}
	if !slices.Contains(applyPatch.Policy.ErrorClasses, "required_edit_scope") {
		t.Fatalf("apply_patch should expose write policy error metadata: %#v", applyPatch.Policy.ErrorClasses)
	}
	if !slices.Contains(exported["read_file"].Policy.SideEffects, autonomy.SideEffectReadOnly) {
		t.Fatalf("read_file should expose read-only side effect: %#v", exported["read_file"].Policy.SideEffects)
	}
	if !slices.Contains(exported["context_status"].Profiles, ToolProfileDiscovery) {
		t.Fatalf("context_status should expose discovery profile: %#v", exported["context_status"].Profiles)
	}
	if slices.Contains(exported["write_file"].Profiles, ToolProfileDiscovery) {
		t.Fatalf("write_file must not expose discovery profile: %#v", exported["write_file"].Profiles)
	}
	if !slices.Contains(exported["submit_plan"].Profiles, "plan_then_execute") {
		t.Fatalf("submit_plan should expose plan-then-execute profile: %#v", exported["submit_plan"].Profiles)
	}
	if !slices.Contains(exported["go_move_declarations"].Profiles, "go_write") {
		t.Fatalf("go_move_declarations should expose Go write profile: %#v", exported["go_move_declarations"].Profiles)
	}

	data, err := json.Marshal(export)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if strings.Contains(string(data), "Inspect one focused architecture/review question") {
		t.Fatalf("schema export should not expose raw manifest prompt examples")
	}
}

func TestBuildToolSchemaExportNormalizesAllowedToolNames(t *testing.T) {
	export, err := BuildToolSchemaExport([]string{" read_file ", "", "\tcontext_pack\n"})
	if err != nil {
		t.Fatalf("BuildToolSchemaExport returned error for padded allowed tools: %v", err)
	}
	var names []string
	for _, tool := range export.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{"context_pack", "read_file"}
	if !slices.Equal(names, want) {
		t.Fatalf("expected normalized allowed tool export %v, got %v", want, names)
	}
}

func TestBuildMCPDiscoveryToolSchemasDerivesFromDiscoveryProfileAndSemanticCapability(t *testing.T) {
	tools, err := BuildMCPDiscoveryToolSchemas(ToolProfileDiscovery)
	if err != nil {
		t.Fatalf("BuildMCPDiscoveryToolSchemas returned error: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected MCP discovery schema surface to contain tools")
	}
	var sawContextStatus bool
	var sawRecoveryInspect bool
	var sawRecoveryStatus bool
	for _, tool := range tools {
		if !slices.Contains(tool.Profiles, ToolProfileDiscovery) {
			t.Fatalf("MCP discovery tool %s missing discovery profile: %#v", tool.Name, tool.Profiles)
		}
		if !slices.Contains(tool.Policy.Capabilities, autonomy.CapabilitySemanticDiscovery) {
			t.Fatalf("MCP discovery tool %s is not schema-classified as semantic discovery: %#v", tool.Name, tool.Policy.Capabilities)
		}
		if !ToolAllowedForProfile(tool.Name, ToolProfileDiscovery).Allowed {
			t.Fatalf("MCP discovery tool %s is not allowed by discovery profile", tool.Name)
		}
		if tool.Name == "context_status" {
			sawContextStatus = true
		}
		if tool.Name == "recovery_inspect" {
			sawRecoveryInspect = true
		}
		if tool.Name == "recovery_status" {
			sawRecoveryStatus = true
		}
		if !mcpDiscoveryProfileTool(tool.Name) {
			t.Fatalf("MCP discovery tool %s should be context-backed or recovery read-only", tool.Name)
		}
		for _, forbidden := range []string{"read_file", "write_file", "shell", "context_pack", "recovery_decompose", "go_read_symbol", "history_range", "blame_range"} {
			if tool.Name == forbidden {
				t.Fatalf("MCP discovery schema surface should not expose %s in P8.1", forbidden)
			}
		}
	}
	if !sawContextStatus {
		t.Fatalf("MCP discovery schema surface should expose context_status: %#v", tools)
	}
	if !sawRecoveryInspect || !sawRecoveryStatus {
		t.Fatalf("MCP discovery schema surface should expose read-only recovery tools: inspect=%v status=%v tools=%#v", sawRecoveryInspect, sawRecoveryStatus, tools)
	}
}

func TestBuildMCPDiscoveryToolSchemasMCPDiscoveryProfileExposesExpandedSurface(t *testing.T) {
	tools, err := BuildMCPDiscoveryToolSchemas(ToolProfileMCPDiscovery)
	if err != nil {
		t.Fatalf("BuildMCPDiscoveryToolSchemas returned error: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected mcp_discovery schema surface to contain tools")
	}
	toolNames := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if !slices.Contains(tool.Profiles, ToolProfileMCPDiscovery) {
			t.Fatalf("mcp_discovery tool %s missing mcp_discovery profile: %#v", tool.Name, tool.Profiles)
		}
		if !slices.Contains(tool.Policy.Capabilities, autonomy.CapabilitySemanticDiscovery) {
			t.Fatalf("mcp_discovery tool %s is not schema-classified as semantic discovery: %#v", tool.Name, tool.Policy.Capabilities)
		}
		if !ToolAllowedForProfile(tool.Name, ToolProfileMCPDiscovery).Allowed {
			t.Fatalf("mcp_discovery tool %s is not allowed by mcp_discovery profile", tool.Name)
		}
		toolNames[tool.Name] = true
	}
	for _, required := range []string{
		"context_status", "context_query", "context_zoom",
		"context_pack", "context_impact", "discovery_query",
		"structural_search", "dependency_graph",
		"history_range", "history_symbol", "blame_range",
		"ctags_symbols", "code_navigate", "harness_audit",
		"recovery_inspect", "recovery_status",
	} {
		if !toolNames[required] {
			names := make([]string, 0, len(toolNames))
			for n := range toolNames {
				names = append(names, n)
			}
			slices.Sort(names)
			t.Fatalf("mcp_discovery schema surface should expose %s, got: %v", required, names)
		}
	}
	for _, forbidden := range []string{"read_file", "write_file", "shell", "edit_file", "apply_patch", "context_trace_pack", "recovery_decompose"} {
		if toolNames[forbidden] {
			t.Fatalf("mcp_discovery schema surface should not expose %s", forbidden)
		}
	}
	discoveryTools, _ := BuildMCPDiscoveryToolSchemas(ToolProfileDiscovery)
	if len(tools) <= len(discoveryTools) {
		t.Fatalf("mcp_discovery surface (%d tools) should be strictly larger than discovery surface (%d tools)", len(tools), len(discoveryTools))
	}
}

func TestBuildToolSchemaExportFailsOnManifestSchemaDrift(t *testing.T) {
	manifest := BuildToolManifest(nil)
	defs := BuildToolDefinitions(nil)
	profiles := toolSchemaExportProfiles()

	_, err := buildToolSchemaExportFromSources(
		append(append([]ToolManifestEntry(nil), manifest...), ToolManifestEntry{Tool: "missing_schema", Purpose: "drift fixture"}),
		defs,
		profiles,
	)
	if err == nil || !strings.Contains(err.Error(), `manifest tool "missing_schema" has no schema definition`) {
		t.Fatalf("expected missing schema drift error, got %v", err)
	}

	_, err = buildToolSchemaExportFromSources(
		manifest,
		append(append([]ToolDefinition(nil), defs...), ToolDefinition{Name: "stale_schema", ParametersJSON: `{"type":"object","properties":{}}`}),
		profiles,
	)
	if err == nil || !strings.Contains(err.Error(), `schema definition "stale_schema" is not in the manifest`) {
		t.Fatalf("expected stale schema drift error, got %v", err)
	}

	badDefs := append([]ToolDefinition(nil), defs...)
	badDefs[0].ParametersJSON = `{`
	_, err = buildToolSchemaExportFromSources(manifest, badDefs, profiles)
	if err == nil || !strings.Contains(err.Error(), "is not valid JSON") {
		t.Fatalf("expected invalid schema error, got %v", err)
	}

	noProfiles := make(map[string][]string, len(profiles))
	for tool, values := range profiles {
		noProfiles[tool] = append([]string(nil), values...)
	}
	delete(noProfiles, manifest[0].Tool)
	_, err = buildToolSchemaExportFromSources(manifest, defs, noProfiles)
	if err == nil || !strings.Contains(err.Error(), "has no exported profile") {
		t.Fatalf("expected missing profile error, got %v", err)
	}
}

func TestHarnessAuditToolSurfaceDetectsExportSuggestionAndStaleBlockerDrift(t *testing.T) {
	manifest := append(toolManifestToolsAcrossBuiltInSurfaces(), "manifest_only")
	defs := append(BuildToolDefinitions(nil), ToolDefinition{
		Name:           "schema_only",
		Description:    "drift fixture",
		ParametersJSON: `{"type":"object","properties":{}}`,
	})
	suggestions := append(builtinToolNamesForSuggest(), "suggestion_only")
	export, err := BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport: %v", err)
	}
	for i := range export.Tools {
		if export.Tools[i].Name != "context_query" {
			continue
		}
		filtered := export.Tools[i].Policy.ErrorClasses[:0]
		for _, class := range export.Tools[i].Policy.ErrorClasses {
			if class != "stale_context" {
				filtered = append(filtered, class)
			}
		}
		export.Tools[i].Policy.ErrorClasses = filtered
		break
	}

	surface := auditToolSurfaceFromSources(manifest, defs, suggestions, export, nil)
	if surface.DriftFindingCount == 0 {
		t.Fatalf("expected drift findings, got %#v", surface)
	}
	if !slices.Contains(surface.ManifestWithoutSchema, "manifest_only") ||
		!slices.Contains(surface.ManifestWithoutExportedSchema, "manifest_only") {
		t.Fatalf("expected manifest drift, got %#v", surface)
	}
	if !slices.Contains(surface.SchemaWithoutManifest, "schema_only") ||
		!slices.Contains(surface.SchemaWithoutExportedSchema, "schema_only") {
		t.Fatalf("expected schema drift, got %#v", surface)
	}
	if !slices.Contains(surface.SuggestionWithoutManifest, "suggestion_only") {
		t.Fatalf("expected stale suggestion drift, got %#v", surface)
	}
	if !slices.Contains(surface.StaleUseBlockerMissingTools, "context_query") {
		t.Fatalf("expected stale-use blocker coverage drift, got %#v", surface.StaleUseBlockerCoverage)
	}
	findings := toolSurfaceFindings(surface)
	for _, want := range []string{"exported_schema_drift", "stale_tool_suggestion", "stale_use_blocker_gap"} {
		if !slices.ContainsFunc(findings, func(finding harnessAuditFinding) bool { return finding.ID == want }) {
			t.Fatalf("expected finding %q in %#v", want, findings)
		}
	}
}
