package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"idiom/internal/autonomy"
)

const ToolSchemaExportVersion = "idiom.tool_schema_export.v1"

// ToolSchemaExport is the sidecar-safe contract view of the built-in workspace
// tool surface. It intentionally derives names, descriptions, and JSON schemas
// from BuildToolManifest and BuildToolDefinitions instead of declaring another
// schema registry.
type ToolSchemaExport struct {
	SchemaVersion string                    `json:"schema_version"`
	Source        string                    `json:"source"`
	Metadata      autonomy.ContractMetadata `json:"metadata,omitempty"`
	Tools         []ExportedToolSchema      `json:"tools"`
}

type ExportedToolSchema struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description,omitempty"`
	InputSchema      json.RawMessage        `json:"input_schema"`
	Profiles         []string               `json:"profiles,omitempty"`
	ManifestPurposes []string               `json:"manifest_purposes,omitempty"`
	Policy           ToolSchemaPolicyExport `json:"policy"`
}

type ToolSchemaPolicyExport struct {
	Source            string                     `json:"source"`
	Capabilities      []autonomy.Capability      `json:"capabilities,omitempty"`
	SideEffects       []autonomy.SideEffectClass `json:"side_effects"`
	DecisionAuthority string                     `json:"decision_authority"`
	Enforcement       []string                   `json:"enforcement,omitempty"`
	ErrorClasses      []string                   `json:"error_classes,omitempty"`
}

// BuildToolSchemaExport returns a JSON-serializable schema export for the
// requested built-in tools. An empty allowedTools slice exports every tool in
// the union of built-in manifest surfaces, including conditional planner tools.
func BuildToolSchemaExport(allowedTools []string) (ToolSchemaExport, error) {
	return buildToolSchemaExportFromSources(
		BuildToolManifest(allowedTools),
		BuildToolDefinitions(allowedTools),
		toolSchemaExportProfiles(),
	)
}

// BuildMCPDiscoveryToolSchemas returns the schema-derived tool surface exposed
// by the P8.1 MCP stdio server. The selection is intentionally derived from
// the same exported schema/profile/policy metadata as `idiom tool schemas`:
// tools must be allowed by the discovery profile, must be semantic discovery
// tools, and in P8.1 must be graph-backed context tools. Raw file, language
// source, history/blame, scan, and resource surfaces are deferred to later MCP
// containment/parity work.
func BuildMCPDiscoveryToolSchemas() ([]ExportedToolSchema, error) {
	export, err := BuildToolSchemaExport(nil)
	if err != nil {
		return nil, err
	}
	var tools []ExportedToolSchema
	for _, tool := range export.Tools {
		if !exportedToolSchemaHasProfile(tool, ToolProfileDiscovery) {
			continue
		}
		if !exportedToolSchemaHasCapability(tool, autonomy.CapabilitySemanticDiscovery) {
			continue
		}
		if !strings.HasPrefix(tool.Name, "context_") {
			continue
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func buildToolSchemaExportFromSources(manifest []ToolManifestEntry, defs []ToolDefinition, profiles map[string][]string) (ToolSchemaExport, error) {
	purposesByTool := make(map[string][]string)
	for _, entry := range manifest {
		tool := strings.TrimSpace(entry.Tool)
		if tool == "" {
			continue
		}
		if purpose := strings.TrimSpace(entry.Purpose); purpose != "" {
			purposesByTool[tool] = append(purposesByTool[tool], purpose)
		} else if _, ok := purposesByTool[tool]; !ok {
			purposesByTool[tool] = nil
		}
	}

	defsByTool := make(map[string]ToolDefinition, len(defs))
	var problems []string
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			problems = append(problems, "schema definition has empty name")
			continue
		}
		if _, exists := defsByTool[name]; exists {
			problems = append(problems, fmt.Sprintf("duplicate schema definition for %q", name))
			continue
		}
		if !json.Valid([]byte(def.ParametersJSON)) {
			problems = append(problems, fmt.Sprintf("schema for %q is not valid JSON", name))
			continue
		}
		def.Name = name
		defsByTool[name] = def
	}

	for tool := range purposesByTool {
		if _, ok := defsByTool[tool]; !ok {
			problems = append(problems, fmt.Sprintf("manifest tool %q has no schema definition", tool))
		}
		if len(profiles[tool]) == 0 {
			problems = append(problems, fmt.Sprintf("manifest tool %q has no exported profile", tool))
		}
	}
	for name := range defsByTool {
		if _, ok := purposesByTool[name]; !ok {
			problems = append(problems, fmt.Sprintf("schema definition %q is not in the manifest", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return ToolSchemaExport{}, fmt.Errorf("tool schema export drift: %s", strings.Join(problems, "; "))
	}

	names := make([]string, 0, len(purposesByTool))
	for name := range purposesByTool {
		names = append(names, name)
	}
	sort.Strings(names)

	out := ToolSchemaExport{
		SchemaVersion: ToolSchemaExportVersion,
		Source:        "internal/workspace.BuildToolManifest + internal/workspace.BuildToolDefinitions",
		Metadata:      autonomy.DefaultContractMetadata("p2.1", "tool_schema_export"),
		Tools:         make([]ExportedToolSchema, 0, len(names)),
	}
	for _, name := range names {
		def := defsByTool[name]
		out.Tools = append(out.Tools, ExportedToolSchema{
			Name:             name,
			Description:      strings.TrimSpace(def.Description),
			InputSchema:      json.RawMessage(def.ParametersJSON),
			Profiles:         append([]string(nil), profiles[name]...),
			ManifestPurposes: compactToolExportStrings(purposesByTool[name]),
			Policy:           toolSchemaPolicyForTool(name),
		})
	}
	return out, nil
}

type toolSchemaExportProfile struct {
	name string
	opts ToolManifestToolOptions
}

func toolSchemaExportProfiles() map[string][]string {
	surfaces := []toolSchemaExportProfile{
		{name: "core", opts: ToolManifestToolOptions{}},
		{name: "task_ledger", opts: ToolManifestToolOptions{IncludeTaskLedger: true}},
		{name: "delegation", opts: ToolManifestToolOptions{IncludeTask: true}},
		{name: "write", opts: ToolManifestToolOptions{IncludeWriteTools: true}},
		{name: "plan_then_execute", opts: ToolManifestToolOptions{IncludePlanThenExecute: true}},
		{name: "go", opts: ToolManifestToolOptions{IncludeGo: true}},
		{name: "go_write", opts: ToolManifestToolOptions{IncludeGo: true, IncludeWriteTools: true}},
		{name: "cpp", opts: ToolManifestToolOptions{IncludeCpp: true}},
		{name: "python", opts: ToolManifestToolOptions{IncludePython: true}},
		{name: "typescript", opts: ToolManifestToolOptions{IncludeTypescript: true}},
		{name: "rust", opts: ToolManifestToolOptions{IncludeRust: true}},
		{name: "java", opts: ToolManifestToolOptions{IncludeJava: true}},
	}
	profiles := make(map[string][]string)
	for _, surface := range surfaces {
		for _, tool := range ToolManifestTools(surface.opts) {
			profiles[tool] = append(profiles[tool], surface.name)
		}
	}
	for _, tool := range ToolManifestTools(ToolManifestToolOptions{
		IncludeTaskLedger: true,
		IncludeTask:       true,
		IncludeWriteTools: true,
		IncludeGo:         true,
		IncludeCpp:        true,
		IncludePython:     true,
		IncludeTypescript: true,
		IncludeRust:       true,
		IncludeJava:       true,
	}) {
		if ToolAllowedForProfile(tool, ToolProfileDiscovery).Allowed {
			profiles[tool] = append(profiles[tool], ToolProfileDiscovery)
		}
	}
	for tool, names := range profiles {
		profiles[tool] = compactToolExportStrings(names)
	}
	return profiles
}

func toolSchemaPolicyForTool(tool string) ToolSchemaPolicyExport {
	sideEffects := toolSchemaSideEffects(tool)
	return ToolSchemaPolicyExport{
		Source:            "derived_from_manifest_and_autonomy_contract_vocabulary",
		Capabilities:      toolSchemaCapabilities(tool),
		SideEffects:       sideEffects,
		DecisionAuthority: "runtime_contract",
		Enforcement:       toolSchemaEnforcement(tool, sideEffects),
		ErrorClasses:      toolSchemaErrorClasses(tool, sideEffects),
	}
}

func toolSchemaCapabilities(tool string) []autonomy.Capability {
	switch {
	case tool == "read_file" || tool == "pwd" || tool == "list_files" || tool == "file_stats" || tool == "git_status" || tool == "git_diff":
		return []autonomy.Capability{autonomy.CapabilityBoundedRead}
	case tool == "search_files" || tool == "search_content":
		return []autonomy.Capability{autonomy.CapabilityLiteralSearch}
	case tool == "edit_file" || tool == "write_file" || tool == "append_file" || tool == "mkdir" || tool == "go_move_declarations":
		return []autonomy.Capability{autonomy.CapabilitySourceEdit}
	case tool == "apply_patch":
		return []autonomy.Capability{autonomy.CapabilityApplyPatch}
	case tool == "delegate_edit":
		return []autonomy.Capability{autonomy.CapabilityDelegation, autonomy.CapabilitySourceEdit}
	case tool == "task":
		return []autonomy.Capability{autonomy.CapabilityDelegation}
	case tool == "submit_plan":
		return []autonomy.Capability{autonomy.CapabilityPlanSubmission}
	case tool == "shell":
		return []autonomy.Capability{autonomy.CapabilityShellVerification, autonomy.CapabilityMutatingShell}
	case isToolSchemaDiagnosticTool(tool) || tool == "source_sanity" || tool == "context_trace_pack":
		return []autonomy.Capability{autonomy.CapabilityShellVerification}
	case isToolSchemaSemanticDiscoveryTool(tool):
		return []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}
	default:
		return nil
	}
}

func toolSchemaSideEffects(tool string) []autonomy.SideEffectClass {
	switch {
	case tool == "task_ledger" || tool == "harness_feedback" || tool == "context_index" || tool == "submit_plan":
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive}
	case tool == "task":
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
	case tool == "edit_file" || tool == "write_file" || tool == "append_file" || tool == "mkdir" || tool == "delegate_edit" || tool == "go_move_declarations":
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive}
	case tool == "apply_patch":
		return []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive}
	case tool == "shell" || tool == "context_trace_pack":
		return []autonomy.SideEffectClass{autonomy.SideEffectUnknown}
	case isToolSchemaDiagnosticTool(tool) || tool == "source_sanity":
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectUnknown}
	default:
		return []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
	}
}

func toolSchemaEnforcement(tool string, sideEffects []autonomy.SideEffectClass) []string {
	enforcement := []string{"allowed_tools"}
	if hasToolSchemaSideEffect(sideEffects, autonomy.SideEffectUnknown) || tool == "shell" {
		enforcement = append(enforcement, "command_classification", "approval_guard")
	}
	if tool == "task" || tool == "delegate_edit" {
		enforcement = append(enforcement, "delegation_scope")
	}
	if tool == "submit_plan" {
		enforcement = append(enforcement, "planner_phase_contract")
	}
	if IsWorkspaceWriteTool(tool) || hasToolSchemaSideEffect(sideEffects, autonomy.SideEffectDestructive) {
		enforcement = append(enforcement, "write_policy", "path_roles", "protected_paths", "approval_guard")
	} else {
		enforcement = append(enforcement, "path_roles_when_configured")
	}
	return compactToolExportStrings(enforcement)
}

func toolSchemaErrorClasses(tool string, sideEffects []autonomy.SideEffectClass) []string {
	classes := []string{"invalid_input", "unsupported_tool"}
	if IsWorkspaceWriteTool(tool) || hasToolSchemaSideEffect(sideEffects, autonomy.SideEffectDestructive) {
		classes = append(classes, "permission_policy", "required_edit_scope", "approval_guard", "protected_path", "write_policy")
	}
	if hasToolSchemaSideEffect(sideEffects, autonomy.SideEffectUnknown) || tool == "shell" {
		classes = append(classes, "permission_policy", "approval_guard", "command_failed", "timeout")
	}
	if isToolSchemaDiagnosticTool(tool) || isToolSchemaLanguageTool(tool) || tool == "source_sanity" {
		classes = append(classes, "unsupported_language", "symbol_not_found", "diagnostics_failed")
	}
	if tool == readOnlyCapabilityToolName {
		classes = append(classes, "capability_deferred", "capability_unmapped", "adapter_unavailable", "resolver_disabled", "route_disabled")
	}
	if strings.HasPrefix(tool, "context_") || tool == "discovery_query" || tool == "dependency_graph" || tool == "structural_search" {
		classes = append(classes, "backend_unavailable", "stale_context")
	}
	return compactToolExportStrings(classes)
}

func isToolSchemaSemanticDiscoveryTool(tool string) bool {
	switch tool {
	case "code_navigate", "read_only_capability", "harness_audit", "context_status", "context_query", "context_zoom",
		"context_pack", "context_impact", "discovery_query", "structural_search", "dependency_graph",
		"history_range", "history_symbol", "blame_range", "ctags_symbols", "semgrep_scan", "joern_scan":
		return true
	default:
		return isToolSchemaLanguageTool(tool)
	}
}

func isToolSchemaLanguageTool(tool string) bool {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			return true
		}
	}
	return false
}

func isToolSchemaDiagnosticTool(tool string) bool {
	return strings.HasSuffix(tool, "_diagnostics")
}

func hasToolSchemaSideEffect(values []autonomy.SideEffectClass, want autonomy.SideEffectClass) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func exportedToolSchemaHasProfile(tool ExportedToolSchema, want string) bool {
	for _, profile := range tool.Profiles {
		if profile == want {
			return true
		}
	}
	return false
}

func exportedToolSchemaHasCapability(tool ExportedToolSchema, want autonomy.Capability) bool {
	for _, capability := range tool.Policy.Capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func compactToolExportStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
