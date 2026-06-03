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
	for _, entry := range BuildToolManifest([]string{"submit_plan"}) {
		if entry.Tool != "submit_plan" {
			continue
		}
		for _, want := range []string{"JSON array, not a quoted string", "complete final content", "not placeholders or prose"} {
			if !strings.Contains(entry.Purpose, want) {
				t.Fatalf("submit_plan manifest purpose missing %q:\n%s", want, entry.Purpose)
			}
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

func TestToolManifestAndExportExposeEvidenceCapabilityMetadata(t *testing.T) {
	manifest := BuildToolManifest([]string{
		"code_navigate", "go_definition", "read_file", "source_sanity", "context_trace_pack",
		"apply_patch", "harness_audit", "history_range", "semgrep_scan", "ctags_symbols",
	})
	byTool := make(map[string]ToolManifestEntry, len(manifest))
	for _, entry := range manifest {
		byTool[entry.Tool] = entry
	}
	cases := []struct {
		tool           string
		capability     string
		classification string
	}{
		{tool: "code_navigate", capability: "inspect_symbol", classification: CapabilityClassRouted},
		{tool: "go_definition", capability: "find_definition", classification: CapabilityClassCompatibilityAdapter},
		{tool: "read_file", capability: "inspect_file", classification: CapabilityClassFallback},
		{tool: "source_sanity", capability: "verify_edit", classification: CapabilityClassDeferred},
		{tool: "context_trace_pack", capability: "verify_edit", classification: CapabilityClassDeferred},
		{tool: "apply_patch", capability: "", classification: CapabilityClassWriteSideEffect},
		{tool: "harness_audit", capability: "audit_behavior", classification: CapabilityClassRouted},
		{tool: "history_range", capability: "history_context", classification: CapabilityClassFallback},
		{tool: "semgrep_scan", capability: "security_scan", classification: CapabilityClassFallback},
		{tool: "ctags_symbols", capability: "inspect_symbol", classification: CapabilityClassFallback},
	}
	for _, tc := range cases {
		entry := byTool[tc.tool]
		if entry.Tool == "" {
			t.Fatalf("manifest missing %s: %#v", tc.tool, manifest)
		}
		if tc.capability != "" && !slices.Contains(entry.EvidenceCapabilities, tc.capability) {
			t.Fatalf("%s manifest capabilities = %#v, want %s", tc.tool, entry.EvidenceCapabilities, tc.capability)
		}
		if entry.CapabilityClassification != tc.classification {
			t.Fatalf("%s manifest classification = %q, want %q", tc.tool, entry.CapabilityClassification, tc.classification)
		}
	}

	export, err := BuildToolSchemaExport([]string{"code_navigate", "go_definition", "source_sanity", "history_symbol", "joern_scan", "ctags_symbols"})
	if err != nil {
		t.Fatalf("BuildToolSchemaExport returned error: %v", err)
	}
	exported := make(map[string]ExportedToolSchema, len(export.Tools))
	for _, tool := range export.Tools {
		exported[tool.Name] = tool
	}
	if !slices.Contains(exported["code_navigate"].EvidenceCapabilities, "find_references") {
		t.Fatalf("code_navigate export capabilities = %#v", exported["code_navigate"].EvidenceCapabilities)
	}
	if exported["go_definition"].CapabilityClassification != CapabilityClassCompatibilityAdapter {
		t.Fatalf("go_definition export classification = %q", exported["go_definition"].CapabilityClassification)
	}
	if exported["source_sanity"].CapabilityClassification != CapabilityClassDeferred ||
		!slices.Contains(exported["source_sanity"].EvidenceCapabilities, "verify_edit") {
		t.Fatalf("source_sanity export metadata = %#v", exported["source_sanity"])
	}
	if exported["history_symbol"].CapabilityClassification != CapabilityClassFallback ||
		!slices.Contains(exported["history_symbol"].EvidenceCapabilities, "history_context") {
		t.Fatalf("history_symbol export metadata = %#v", exported["history_symbol"])
	}
	if exported["joern_scan"].CapabilityClassification != CapabilityClassFallback ||
		!slices.Contains(exported["joern_scan"].EvidenceCapabilities, "security_scan") {
		t.Fatalf("joern_scan export metadata = %#v", exported["joern_scan"])
	}
	if exported["ctags_symbols"].CapabilityClassification != CapabilityClassFallback ||
		!slices.Contains(exported["ctags_symbols"].EvidenceCapabilities, "find_references") ||
		!slices.Contains(exported["ctags_symbols"].EvidenceCapabilities, "inspect_symbol") {
		t.Fatalf("ctags_symbols export metadata = %#v", exported["ctags_symbols"])
	}
}

func TestToolSchemaPolicyUsesToolSurfaceMetadata(t *testing.T) {
	cases := []struct {
		tool         string
		capabilities []autonomy.Capability
		sideEffects  []autonomy.SideEffectClass
	}{
		{
			tool:         "read_file",
			capabilities: []autonomy.Capability{autonomy.CapabilityBoundedRead},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		},
		{
			tool:         "search_content",
			capabilities: []autonomy.Capability{autonomy.CapabilityLiteralSearch},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		},
		{
			tool:         "context_index",
			capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectAdditive},
		},
		{
			tool:         "context_trace_pack",
			capabilities: []autonomy.Capability{autonomy.CapabilityShellVerification},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectUnknown},
		},
		{
			tool:         "submit_plan",
			capabilities: []autonomy.Capability{autonomy.CapabilityPlanSubmission},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectAdditive},
		},
		{
			tool:         "recovery_decompose",
			capabilities: []autonomy.Capability{autonomy.CapabilityExternalWrite},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectAdditive},
		},
		{
			tool:         "go_definition",
			capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		},
		{
			tool:         "go_find_type",
			capabilities: []autonomy.Capability{autonomy.CapabilitySemanticDiscovery},
			sideEffects:  []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		},
	}
	for _, tc := range cases {
		policy := toolSchemaPolicyForTool(tc.tool)
		if !slices.Equal(policy.Capabilities, tc.capabilities) {
			t.Fatalf("%s capabilities = %#v, want %#v", tc.tool, policy.Capabilities, tc.capabilities)
		}
		if !slices.Equal(policy.SideEffects, tc.sideEffects) {
			t.Fatalf("%s side effects = %#v, want %#v", tc.tool, policy.SideEffects, tc.sideEffects)
		}
	}
}

func TestDiscoveryManifestToolsHaveEvidenceCapabilitiesOrExplicitExclusion(t *testing.T) {
	excluded := map[string]string{
		"context_index": "refreshes local context artifacts; freshness and index state are reported through context_status",
	}
	for tool, reason := range excluded {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("discovery evidence capability exclusion for %s must name the reason", tool)
		}
	}

	seenExclusions := make(map[string]bool, len(excluded))
	for _, entry := range BuildToolManifest(nil) {
		if ClassifyTool(entry.Tool) != CategoryDiscovery {
			continue
		}
		if len(EvidenceCapabilitiesForTool(entry.Tool)) > 0 {
			continue
		}
		if _, ok := excluded[entry.Tool]; ok {
			seenExclusions[entry.Tool] = true
			continue
		}
		t.Fatalf("discovery tool %s has no evidence capabilities and no explicit exclusion", entry.Tool)
	}
	for tool := range excluded {
		if !seenExclusions[tool] {
			t.Fatalf("discovery evidence capability exclusion for %s no longer matches a manifest discovery tool", tool)
		}
	}
}

func TestWriteToolManifestGuidanceIncludesRequiredDescription(t *testing.T) {
	writeTools := map[string]bool{
		"edit_file":   true,
		"write_file":  true,
		"append_file": true,
		"apply_patch": true,
	}
	seen := make(map[string]int, len(writeTools))
	for _, entry := range BuildToolManifest(nil) {
		if !writeTools[entry.Tool] {
			continue
		}
		seen[entry.Tool]++
		if strings.Contains(entry.Request, "relative/file") || strings.Contains(entry.Request, "replacement text") {
			t.Fatalf("%s manifest request should not expose placeholder JSON: %s", entry.Tool, entry.Request)
		}
		if !strings.Contains(strings.ToLower(entry.Purpose), "description") {
			t.Fatalf("%s manifest purpose must mention description because the write guard requires it: %s", entry.Tool, entry.Purpose)
		}
	}
	for tool := range writeTools {
		if seen[tool] == 0 {
			t.Fatalf("missing manifest example for %s", tool)
		}
	}
}

func TestWriteToolDescriptionsDistinguishBoundedAndWholeFileEdits(t *testing.T) {
	manifest := BuildToolManifest(nil)
	var editPurpose, writePurpose string
	for _, entry := range manifest {
		switch entry.Tool {
		case "edit_file":
			editPurpose += entry.Purpose + "\n"
		case "write_file":
			writePurpose = entry.Purpose
		}
	}
	for _, want := range []string{"bounded edit", "existing source/test"} {
		if !strings.Contains(editPurpose, want) {
			t.Fatalf("edit_file manifest should advertise bounded source/test edits and contain %q:\n%s", want, editPurpose)
		}
	}
	for _, want := range []string{"whole-file replacement", "new files", "prefer edit_file/apply_patch for existing source/test files"} {
		if !strings.Contains(writePurpose, want) {
			t.Fatalf("write_file manifest should scope whole-file replacement and contain %q:\n%s", want, writePurpose)
		}
	}

	export, err := BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport returned error: %v", err)
	}
	tools := make(map[string]ExportedToolSchema)
	for _, tool := range export.Tools {
		tools[tool.Name] = tool
	}
	editDescription := tools["edit_file"].Description
	for _, want := range []string{"Bounded edit", "Do not pass content without start_line/end_line", "not use this for whole-file replacement"} {
		if !strings.Contains(editDescription, want) {
			t.Fatalf("edit_file schema description should contain %q:\n%s", want, editDescription)
		}
	}
	writeDescription := tools["write_file"].Description
	for _, want := range []string{"Whole-file replacement", "new files", "prefer edit_file or apply_patch for existing source/test edits"} {
		if !strings.Contains(writeDescription, want) {
			t.Fatalf("write_file schema description should contain %q:\n%s", want, writeDescription)
		}
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
		if !slices.Contains(tool.Policy.Capabilities, autonomy.CapabilitySemanticDiscovery) &&
			!slices.Contains(tool.Policy.Capabilities, autonomy.CapabilityBoundedRead) {
			t.Fatalf("mcp_discovery tool %s is not schema-classified as semantic discovery or bounded read: %#v", tool.Name, tool.Policy.Capabilities)
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
		"product_efforts", "product_status",
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
