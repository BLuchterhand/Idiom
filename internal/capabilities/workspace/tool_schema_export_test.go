package workspace

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("tool schema export", func() {
	ginkgo.It("build tool schema export derives from manifest and schemas", func() {
		export, err := BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error: %v", err))
		gomega.Expect(export.SchemaVersion).To(gomega.Equal(ToolSchemaExportVersion), fmt.Sprintf("unexpected schema version %q", export.SchemaVersion))
		gomega.Expect(export.Metadata.ClientClassifications).To(gomega.HaveLen(len(autonomy.DefaultClientClassifications())), fmt.Sprintf("export should carry all generic client classifications: %#v", export.Metadata.ClientClassifications))

		for _, want := range autonomy.DefaultClientClassifications() {
			gomega.Expect(slices.Contains(export.Metadata.ClientClassifications, want)).To(gomega.BeTrue(), fmt.Sprintf("export missing client classification %q: %#v", want, export.Metadata.ClientClassifications))

		}
		gomega.Expect(export.Metadata.ProductSignals).To(gomega.HaveLen(len(autonomy.DefaultProductSignals())), fmt.Sprintf("export should carry all generic product signals: %#v", export.Metadata.ProductSignals))

		for _, want := range autonomy.DefaultProductSignals() {
			gomega.Expect(slices.Contains(export.Metadata.ProductSignals, want)).To(gomega.BeTrue(), fmt.Sprintf("export missing product signal %q: %#v", want, export.Metadata.ProductSignals))

		}

		exported := make(map[string]ExportedToolSchema, len(export.Tools))
		for _, tool := range export.Tools {
			gomega.Expect(tool.Name).NotTo(gomega.Equal(""), fmt.Sprintf("export contains tool with empty name: %#v", tool))

			if _, exists := exported[tool.Name]; exists {
				ginkgo.Fail(fmt.Sprintf("duplicate exported tool %q", tool.Name))
			}
			gomega.Expect(json.Valid(tool.InputSchema)).To(gomega.BeTrue(), fmt.Sprintf("exported schema for %s is invalid JSON: %s", tool.Name, string(tool.InputSchema)))
			gomega.Expect(tool.Profiles).NotTo(gomega.BeEmpty(), fmt.Sprintf("exported tool %s missing profile metadata", tool.Name))
			gomega.Expect(tool.Policy.SideEffects).NotTo(gomega.BeEmpty(), fmt.Sprintf("exported tool %s missing side-effect metadata", tool.Name))

			for _, want := range []string{"invalid_input", "unsupported_tool"} {
				gomega.Expect(slices.Contains(tool.Policy.ErrorClasses, want)).To(gomega.BeTrue(), fmt.Sprintf("exported tool %s missing error class %q: %#v", tool.Name, want, tool.Policy.ErrorClasses))

			}

			exported[tool.Name] = tool
		}

		manifestNames := make(map[string]struct{})
		for _, entry := range BuildToolManifest(nil) {
			manifestNames[entry.Tool] = struct{}{}
		}
		gomega.Expect(exported).To(gomega.HaveLen(len(manifestNames)), fmt.Sprintf("exported tool count %d does not match manifest count %d", len(exported), len(manifestNames)))

		for name := range manifestNames {
			if _, ok := exported[name]; !ok {
				ginkgo.Fail(fmt.Sprintf("manifest tool %q missing from export", name))
			}
		}

		for _, entry := range BuildToolManifest([]string{"submit_plan"}) {
			if entry.Tool != "submit_plan" {
				continue
			}

			for _, want := range []string{"JSON array, not a quoted string", "complete final content", "not placeholders or prose"} {
				gomega.Expect(entry.Purpose).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan manifest purpose missing %q:\n%s", want, entry.Purpose))

			}
		}

		applyPatch := exported["apply_patch"]
		gomega.Expect(applyPatch.Policy.SideEffects).To(gomega.ContainElement(autonomy.SideEffectAdditive), fmt.Sprintf("apply_patch should expose additive and destructive side effects: %#v", applyPatch.Policy.SideEffects))
		gomega.Expect(applyPatch.Policy.SideEffects).To(gomega.ContainElement(autonomy.SideEffectDestructive), fmt.Sprintf("apply_patch should expose additive and destructive side effects: %#v", applyPatch.Policy.SideEffects))
		gomega.Expect(slices.Contains(applyPatch.Policy.ErrorClasses, "required_edit_scope")).To(gomega.BeTrue(), fmt.Sprintf("apply_patch should expose write policy error metadata: %#v", applyPatch.Policy.ErrorClasses))
		gomega.Expect(slices.Contains(exported["read_file"].Policy.SideEffects, autonomy.SideEffectReadOnly)).To(gomega.BeTrue(), fmt.Sprintf("read_file should expose read-only side effect: %#v", exported["read_file"].Policy.SideEffects))
		gomega.Expect(slices.Contains(exported["context_status"].Profiles, ToolProfileDiscovery)).To(gomega.BeTrue(), fmt.Sprintf("context_status should expose discovery profile: %#v", exported["context_status"].Profiles))
		gomega.Expect(slices.Contains(exported["write_file"].Profiles, ToolProfileDiscovery)).To(gomega.BeFalse(), fmt.Sprintf("write_file must not expose discovery profile: %#v", exported["write_file"].Profiles))
		gomega.Expect(slices.Contains(exported["submit_plan"].Profiles, "plan_then_execute")).To(gomega.BeTrue(), fmt.Sprintf("submit_plan should expose plan-then-execute profile: %#v", exported["submit_plan"].Profiles))
		gomega.Expect(slices.Contains(exported["go_move_declarations"].Profiles, "go_write")).To(gomega.BeTrue(), fmt.Sprintf("go_move_declarations should expose Go write profile: %#v", exported["go_move_declarations"].Profiles))

		data, err := json.Marshal(export)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal export: %v", err))
		gomega.Expect(string(data)).NotTo(gomega.ContainSubstring("Inspect one focused architecture/review question"), fmt.Sprintf("schema export should not expose raw manifest prompt examples"))

	})
	ginkgo.It("tool manifest and export expose evidence capability metadata", func() {
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
			{tool: "ctags_symbols", capability: "find_symbol", classification: CapabilityClassFallback},
		}
		for _, tc := range cases {
			entry := byTool[tc.tool]
			gomega.Expect(entry.Tool).NotTo(gomega.Equal(""), fmt.Sprintf("manifest missing %s: %#v", tc.tool, manifest))
			if tc.capability != "" {
				gomega.Expect(entry.EvidenceCapabilities).To(gomega.ContainElement(tc.capability), fmt.Sprintf("%s manifest capabilities = %#v, want %s", tc.tool, entry.EvidenceCapabilities, tc.capability))
			}
			gomega.Expect(entry.CapabilityClassification).To(gomega.Equal(tc.classification), fmt.Sprintf("%s manifest classification = %q, want %q", tc.tool, entry.CapabilityClassification, tc.classification))

		}

		export, err := BuildToolSchemaExport([]string{"code_navigate", "go_definition", "source_sanity", "history_symbol", "joern_scan", "ctags_symbols"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error: %v", err))

		exported := make(map[string]ExportedToolSchema, len(export.Tools))
		for _, tool := range export.Tools {
			exported[tool.Name] = tool
		}

		for _, want := range []string{"find_references", "find_implementations", "inspect_symbol"} {
			gomega.Expect(slices.Contains(exported["code_navigate"].EvidenceCapabilities, want)).To(gomega.BeTrue(), fmt.Sprintf("code_navigate export capabilities = %#v, want %s", exported["code_navigate"].EvidenceCapabilities, want))

		}
		gomega.Expect(slices.Equal(exported["code_navigate"].EvidenceCapabilities, ReadOnlyCapabilityEvidenceCapabilities())).To(gomega.BeFalse(), fmt.Sprintf("code_navigate export capabilities = %#v", exported["code_navigate"].EvidenceCapabilities))
		gomega.Expect(exported["go_definition"].CapabilityClassification).To(gomega.Equal(CapabilityClassCompatibilityAdapter), fmt.Sprintf("go_definition export classification = %q", exported["go_definition"].CapabilityClassification))
		gomega.Expect(exported["source_sanity"].CapabilityClassification).To(gomega.Equal(CapabilityClassDeferred), fmt.Sprintf("source_sanity export metadata = %#v", exported["source_sanity"]))
		gomega.Expect(exported["source_sanity"].EvidenceCapabilities).To(gomega.ContainElement("verify_edit"), fmt.Sprintf("source_sanity export metadata = %#v", exported["source_sanity"]))
		gomega.Expect(exported["history_symbol"].CapabilityClassification).To(gomega.Equal(CapabilityClassFallback), fmt.Sprintf("history_symbol export metadata = %#v", exported["history_symbol"]))
		gomega.Expect(exported["history_symbol"].EvidenceCapabilities).To(gomega.ContainElement("history_context"), fmt.Sprintf("history_symbol export metadata = %#v", exported["history_symbol"]))
		gomega.Expect(exported["joern_scan"].CapabilityClassification).To(gomega.Equal(CapabilityClassFallback), fmt.Sprintf("joern_scan export metadata = %#v", exported["joern_scan"]))
		gomega.Expect(exported["joern_scan"].EvidenceCapabilities).To(gomega.ContainElement("security_scan"), fmt.Sprintf("joern_scan export metadata = %#v", exported["joern_scan"]))
		gomega.Expect(exported["ctags_symbols"].CapabilityClassification).To(gomega.Equal(CapabilityClassFallback), fmt.Sprintf("ctags_symbols export metadata = %#v", exported["ctags_symbols"]))
		gomega.Expect(exported["ctags_symbols"].EvidenceCapabilities).To(gomega.Equal([]string{"find_symbol"}), fmt.Sprintf("ctags_symbols export metadata = %#v", exported["ctags_symbols"]))

	})
	ginkgo.It("tool schema enums derive from capability registries", func() {
		codeNavigate := toolDefinitionForTest("code_navigate")
		if got := stringEnumForToolProperty(codeNavigate, "capability"); !slices.Equal(got, languagetools.CodeNavigationCapabilityNames()) {
			ginkgo.Fail(fmt.Sprintf("code_navigate schema enum drifted from runtime capabilities:\ngot  %#v\nwant %#v", got, languagetools.CodeNavigationCapabilityNames()))
		}

		readOnly := toolDefinitionForTest(ToolNameReadOnlyCapability)
		registry := BuildReadOnlyCapabilityRegistry()
		wantReadOnly := make([]string, 0, len(registry.Capabilities))

		for _, capability := range registry.Capabilities {
			wantReadOnly = append(wantReadOnly, capability.ID)
		}

		if got := stringEnumForToolProperty(readOnly, "capability"); !slices.Equal(got, wantReadOnly) {
			ginkgo.Fail(fmt.Sprintf("read_only_capability schema enum drifted from registry:\ngot  %#v\nwant %#v", got, wantReadOnly))
		}

		export, err := BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error: %v", err))

		exported := make(map[string]ExportedToolSchema, len(export.Tools))
		for _, tool := range export.Tools {
			exported[tool.Name] = tool
		}
		readOnlyExport := exported[ToolNameReadOnlyCapability]
		for _, want := range []string{"capability_unmapped", "adapter_unavailable", "resolver_disabled", "route_disabled"} {
			gomega.Expect(slices.Contains(readOnlyExport.Policy.ErrorClasses, want)).To(gomega.BeTrue(), fmt.Sprintf("read_only_capability error classes = %#v, want %q", readOnlyExport.Policy.ErrorClasses, want))

		}
		gomega.Expect(slices.Contains(readOnlyExport.Policy.ErrorClasses, "capability_deferred")).To(gomega.BeFalse(), fmt.Sprintf("read_only_capability should not advertise deleted deferred facade error class: %#v", readOnlyExport.Policy.ErrorClasses))

	})
	ginkgo.It("language tool catalog drives schema manifest and metadata", func() {
		catalog := languagetools.ToolCatalog()
		gomega.Expect(catalog).NotTo(gomega.BeEmpty(), "language tool catalog should not be empty")

		names := make([]string, 0, len(catalog))
		for _, spec := range catalog {
			names = append(names, spec.Tool)
		}
		gomega.Expect(names).To(gomega.HaveLen(len(catalog)), fmt.Sprintf("catalog names = %d, entries = %d", len(names), len(catalog)))

		defs := BuildToolDefinitions(names)
		manifest := BuildToolManifest(names)
		gomega.Expect(defs).To(gomega.HaveLen(len(catalog)), fmt.Sprintf("catalog tool definition count = %d, want %d", len(defs), len(catalog)))
		gomega.Expect(manifest).To(gomega.HaveLen(len(catalog)), fmt.Sprintf("catalog manifest count = %d, want %d", len(manifest), len(catalog)))

		defByTool := make(map[string]ToolDefinition, len(defs))
		for _, def := range defs {
			defByTool[def.Name] = def
		}

		manifestByTool := make(map[string]ToolManifestEntry, len(manifest))
		for _, entry := range manifest {
			manifestByTool[entry.Tool] = entry
		}

		for _, spec := range catalog {
			def, ok := defByTool[spec.Tool]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("catalog tool %s missing schema definition", spec.Tool))
			gomega.Expect(def.Description).To(gomega.Equal(spec.Description), fmt.Sprintf("%s schema drifted from catalog:\ndef  %#v\nspec %#v", spec.Tool, def, spec))
			gomega.Expect(def.ParametersJSON).To(gomega.Equal(spec.ParametersJSON), fmt.Sprintf("%s schema drifted from catalog:\ndef  %#v\nspec %#v", spec.Tool, def, spec))
			gomega.Expect(json.Valid([]byte(def.ParametersJSON))).To(gomega.BeTrue(), fmt.Sprintf("%s catalog schema is invalid JSON: %s", spec.Tool, def.ParametersJSON))

			manifestEntry, ok := manifestByTool[spec.Tool]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("catalog tool %s missing manifest entry", spec.Tool))
			gomega.Expect(manifestEntry.Purpose).To(gomega.Equal(spec.ManifestPurpose), fmt.Sprintf("%s manifest purpose = %q, want %q", spec.Tool, manifestEntry.Purpose, spec.ManifestPurpose))
			gomega.Expect(slices.Equal(manifestEntry.EvidenceCapabilities, spec.EvidenceCapabilities)).To(gomega.BeTrue(), fmt.Sprintf("%s evidence metadata = %#v, want %#v", spec.Tool, manifestEntry.EvidenceCapabilities, spec.EvidenceCapabilities))

			wantClassification := CapabilityClassCompatibilityAdapter
			if spec.Capability == semanticlanguage.CapDiagnostics {
				wantClassification = CapabilityClassDeferred
			}
			gomega.Expect(manifestEntry.CapabilityClassification).To(gomega.Equal(wantClassification), fmt.Sprintf("%s classification = %q, want %q", spec.Tool, manifestEntry.CapabilityClassification, wantClassification))
			gomega.Expect(languagetools.IsToolName(spec.Tool)).To(gomega.BeTrue(), fmt.Sprintf("%s should be accepted as a catalog language tool", spec.Tool))

		}
	})
	ginkgo.It("extract body is registry backed language capability", func() {
		for _, tool := range []string{"go_extract_body", "cpp_extract_body"} {
			spec, ok := languagetools.ToolSpecForTool(tool)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing from language tool catalog", tool))
			gomega.Expect(spec.Supplemental).To(gomega.BeFalse(), fmt.Sprintf("%s should be registry-backed, not supplemental: %#v", tool, spec))
			gomega.Expect(spec.Capability).To(gomega.Equal(semanticlanguage.CapExtractBody), fmt.Sprintf("%s capability = %q, want %q", tool, spec.Capability, semanticlanguage.CapExtractBody))
			gomega.Expect(slices.Contains(spec.EvidenceCapabilities, "inspect_symbol")).To(gomega.BeTrue(), fmt.Sprintf("%s evidence capabilities = %#v", tool, spec.EvidenceCapabilities))

		}
	})
})

func toolDefinitionForTest(tool string) ToolDefinition {
	ginkgo.GinkgoHelper()

	defs := BuildToolDefinitions([]string{tool})
	gomega.Expect(defs).To(gomega.HaveLen(1), fmt.Sprintf("BuildToolDefinitions(%q) returned %d definition(s)", tool, len(defs)))

	return defs[0]
}

func stringEnumForToolProperty(def ToolDefinition, property string) []string {
	ginkgo.GinkgoHelper()

	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	gomega.Expect(json.Unmarshal([]byte(def.ParametersJSON), &schema)).To(gomega.Succeed())

	prop, ok := schema.Properties[property]
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("schema for %s missing property %q: %s", def.Name, property, def.ParametersJSON))

	return prop.Enum
}

var _ = ginkgo.Describe("tool schema export", func() {
	ginkgo.It("tool schema policy uses tool surface metadata", func() {
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
			gomega.Expect(slices.Equal(policy.Capabilities, tc.capabilities)).To(gomega.BeTrue(), fmt.Sprintf("%s capabilities = %#v, want %#v", tc.tool, policy.Capabilities, tc.capabilities))
			gomega.Expect(slices.Equal(policy.SideEffects, tc.sideEffects)).To(gomega.BeTrue(), fmt.Sprintf("%s side effects = %#v, want %#v", tc.tool, policy.SideEffects, tc.sideEffects))

		}
	})
	ginkgo.It("tool schema policy keeps registry diagnostics deferred", func() {
		var diagnosticTools []string
		for _, spec := range languagetools.ToolCatalog() {
			if spec.Capability == semanticlanguage.CapDiagnostics {
				diagnosticTools = append(diagnosticTools, spec.Tool)
			}
		}
		gomega.Expect(diagnosticTools).NotTo(gomega.BeEmpty(), "language tool catalog should declare diagnostic tools")

		for _, tool := range diagnosticTools {
			policy := toolSchemaPolicyForTool(tool)
			gomega.Expect(slices.Contains(policy.Capabilities, autonomy.CapabilityShellVerification)).To(gomega.BeTrue(), fmt.Sprintf("%s capabilities = %#v, want shell verification", tool, policy.Capabilities))
			gomega.Expect(slices.Contains(policy.SideEffects, autonomy.SideEffectUnknown)).To(gomega.BeTrue(), fmt.Sprintf("%s side effects = %#v, want unknown", tool, policy.SideEffects))

			if got := CapabilityClassificationForTool(tool); got != CapabilityClassDeferred {
				ginkgo.Fail(fmt.Sprintf("%s classification = %q, want %q", tool, got, CapabilityClassDeferred))
			}
			for _, want := range []string{"unsupported_language", "symbol_not_found", "diagnostics_failed"} {
				gomega.Expect(slices.Contains(policy.ErrorClasses, want)).To(gomega.BeTrue(), fmt.Sprintf("%s error classes = %#v, want %q", tool, policy.ErrorClasses, want))

			}
		}
	})
	ginkgo.It("discovery manifest tools have evidence capabilities or explicit exclusion", func() {
		excluded := map[string]string{
			"context_index": "refreshes local context artifacts; freshness and index state are reported through context_status",
		}
		for tool, reason := range excluded {
			gomega.Expect(strings.TrimSpace(reason)).NotTo(gomega.Equal(""), fmt.Sprintf("discovery evidence capability exclusion for %s must name the reason", tool))

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
			ginkgo.Fail(fmt.Sprintf("discovery tool %s has no evidence capabilities and no explicit exclusion", entry.Tool))
		}

		for tool := range excluded {
			gomega.Expect(seenExclusions[tool]).To(gomega.BeTrue(), fmt.Sprintf("discovery evidence capability exclusion for %s no longer matches a manifest discovery tool", tool))

		}
	})
	ginkgo.It("write tool manifest guidance includes required description", func() {
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
			gomega.Expect(entry.Request).NotTo(gomega.ContainSubstring("relative/file"), fmt.Sprintf("%s manifest request should not expose placeholder JSON: %s", entry.Tool, entry.Request))
			gomega.Expect(entry.Request).NotTo(gomega.ContainSubstring("replacement text"), fmt.Sprintf("%s manifest request should not expose placeholder JSON: %s", entry.Tool, entry.Request))
			gomega.Expect(strings.ToLower(entry.Purpose)).To(gomega.ContainSubstring("description"), fmt.Sprintf("%s manifest purpose must mention description because the write guard requires it: %s", entry.Tool, entry.Purpose))

		}

		for tool := range writeTools {
			gomega.Expect(seen[tool]).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("missing manifest example for %s", tool))

		}
	})
	ginkgo.It("write tool descriptions distinguish bounded and whole file edits", func() {
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
			gomega.Expect(editPurpose).To(gomega.ContainSubstring(want), fmt.Sprintf("edit_file manifest should advertise bounded source/test edits and contain %q:\n%s", want, editPurpose))

		}

		for _, want := range []string{"full-body write", "missing/new files", "prefer edit_file/apply_patch for existing source/test files"} {
			gomega.Expect(writePurpose).To(gomega.ContainSubstring(want), fmt.Sprintf("write_file manifest should scope full-body writes and contain %q:\n%s", want, writePurpose))

		}

		export, err := BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error: %v", err))

		tools := make(map[string]ExportedToolSchema)
		for _, tool := range export.Tools {
			tools[tool.Name] = tool
		}

		editDescription := tools["edit_file"].Description
		for _, want := range []string{"Bounded edit", "Do not pass content without start_line/end_line", "not use this for whole-file replacement"} {
			gomega.Expect(editDescription).To(gomega.ContainSubstring(want), fmt.Sprintf("edit_file schema description should contain %q:\n%s", want, editDescription))

		}

		writeDescription := tools["write_file"].Description
		for _, want := range []string{"Full-body write", "missing/new files", "prefer edit_file or apply_patch for existing source/test edits"} {
			gomega.Expect(writeDescription).To(gomega.ContainSubstring(want), fmt.Sprintf("write_file schema description should contain %q:\n%s", want, writeDescription))

		}

		submitPlanSchema := string(tools["submit_plan"].InputSchema)
		for _, want := range []string{
			"complete missing/new-file body",
			"missing/new-file body",
			"missing/new files only",
			"existing source/test files require bounded anchors",
		} {
			gomega.Expect(submitPlanSchema).To(gomega.ContainSubstring(want), fmt.Sprintf("submit_plan property schema should contain %q:\n%s", want, submitPlanSchema))

		}
		for _, stale := range []string{
			"replacement/whole-file body",
			"write new_text as the entire file body (creates the file if missing)",
		} {
			gomega.Expect(submitPlanSchema).NotTo(gomega.ContainSubstring(stale), fmt.Sprintf("submit_plan property schema retained stale wording %q:\n%s", stale, submitPlanSchema))

		}
	})
	ginkgo.It("build tool schema export normalizes allowed tool names", func() {
		export, err := BuildToolSchemaExport([]string{" read_file ", "", "\tcontext_pack\n"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error for padded allowed tools: %v", err))

		var names []string

		for _, tool := range export.Tools {
			names = append(names, tool.Name)
		}

		slices.Sort(names)

		want := []string{"context_pack", "read_file"}
		gomega.Expect(slices.Equal(names, want)).To(gomega.BeTrue(), fmt.Sprintf("expected normalized allowed tool export %v, got %v", want, names))

	})
	ginkgo.It("build mcp discovery tool schemas derives from discovery profile and semantic capability", func() {
		tools, err := BuildMCPDiscoveryToolSchemas(ToolProfileDiscovery)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildMCPDiscoveryToolSchemas returned error: %v", err))
		gomega.Expect(tools).NotTo(gomega.BeEmpty(), "expected MCP discovery schema surface to contain tools")

		var (
			sawContextStatus   bool
			sawRecoveryInspect bool
			sawRecoveryStatus  bool
		)

		for _, tool := range tools {
			gomega.Expect(slices.Contains(tool.Profiles, ToolProfileDiscovery)).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery tool %s missing discovery profile: %#v", tool.Name, tool.Profiles))
			gomega.Expect(slices.Contains(tool.Policy.Capabilities, autonomy.CapabilitySemanticDiscovery)).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery tool %s is not schema-classified as semantic discovery: %#v", tool.Name, tool.Policy.Capabilities))
			gomega.Expect(ToolAllowedForProfile(tool.Name, ToolProfileDiscovery).Allowed).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery tool %s is not allowed by discovery profile", tool.Name))

			if tool.Name == "context_status" {
				sawContextStatus = true
			}

			if tool.Name == "recovery_inspect" {
				sawRecoveryInspect = true
			}

			if tool.Name == "recovery_status" {
				sawRecoveryStatus = true
			}
			gomega.Expect(builtInToolSurfaceMCPDiscoveryProfileTool(tool.Name)).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery tool %s should be context-backed or recovery read-only", tool.Name))

			for _, forbidden := range []string{"read_file", "write_file", "shell", "context_pack", "recovery_decompose", "go_read_symbol", "history_range", "blame_range"} {
				gomega.Expect(tool.Name).NotTo(gomega.Equal(forbidden), fmt.Sprintf("MCP discovery schema surface should not expose %s in P8.1", forbidden))

			}
		}
		gomega.Expect(sawContextStatus).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery schema surface should expose context_status: %#v", tools))
		gomega.Expect(sawRecoveryInspect).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery schema surface should expose read-only recovery tools: inspect=%v status=%v tools=%#v", sawRecoveryInspect, sawRecoveryStatus, tools))
		gomega.Expect(sawRecoveryStatus).To(gomega.BeTrue(), fmt.Sprintf("MCP discovery schema surface should expose read-only recovery tools: inspect=%v status=%v tools=%#v", sawRecoveryInspect, sawRecoveryStatus, tools))

	})
	ginkgo.It("build mcp discovery tool schemas mcp discovery profile exposes expanded surface", func() {
		tools, err := BuildMCPDiscoveryToolSchemas(ToolProfileMCPDiscovery)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildMCPDiscoveryToolSchemas returned error: %v", err))
		gomega.Expect(tools).NotTo(gomega.BeEmpty(), "expected mcp_discovery schema surface to contain tools")

		toolNames := make(map[string]bool, len(tools))
		for _, tool := range tools {
			gomega.Expect(slices.Contains(tool.Profiles, ToolProfileMCPDiscovery)).To(gomega.BeTrue(), fmt.Sprintf("mcp_discovery tool %s missing mcp_discovery profile: %#v", tool.Name, tool.Profiles))
			gomega.Expect(tool.Policy.Capabilities).To(gomega.Or(
				gomega.ContainElement(autonomy.CapabilitySemanticDiscovery),
				gomega.ContainElement(autonomy.CapabilityBoundedRead),
			), fmt.Sprintf("mcp_discovery tool %s is not schema-classified as semantic discovery or bounded read: %#v", tool.Name, tool.Policy.Capabilities))
			gomega.Expect(ToolAllowedForProfile(tool.Name, ToolProfileMCPDiscovery).Allowed).To(gomega.BeTrue(), fmt.Sprintf("mcp_discovery tool %s is not allowed by mcp_discovery profile", tool.Name))

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
				ginkgo.Fail(fmt.Sprintf("mcp_discovery schema surface should expose %s, got: %v", required, names))
			}
		}

		for _, forbidden := range []string{"read_file", "write_file", "shell", "edit_file", "apply_patch", "context_trace_pack", "recovery_decompose"} {
			gomega.Expect(toolNames[forbidden]).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery schema surface should not expose %s", forbidden))

		}

		discoveryTools, _ := BuildMCPDiscoveryToolSchemas(ToolProfileDiscovery)
		gomega.Expect(len(tools) <= len(discoveryTools)).To(gomega.BeFalse(), fmt.Sprintf("mcp_discovery surface (%d tools) should be strictly larger than discovery surface (%d tools)", len(tools), len(discoveryTools)))

	})
	ginkgo.It("build tool schema export fails on manifest schema drift", func() {
		manifest := BuildToolManifest(nil)
		defs := BuildToolDefinitions(nil)
		profiles := toolSchemaExportProfiles()

		_, err := buildToolSchemaExportFromSources(
			append(append([]ToolManifestEntry(nil), manifest...), ToolManifestEntry{Tool: "missing_schema", Purpose: "drift fixture"}),
			defs,
			profiles,
		)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing schema drift error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`manifest tool "missing_schema" has no schema definition`), fmt.Sprintf("expected missing schema drift error, got %v", err))

		_, err = buildToolSchemaExportFromSources(
			manifest,
			append(append([]ToolDefinition(nil), defs...), ToolDefinition{Name: "stale_schema", ParametersJSON: `{"type":"object","properties":{}}`}),
			profiles,
		)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected stale schema drift error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring(`schema definition "stale_schema" is not in the manifest`), fmt.Sprintf("expected stale schema drift error, got %v", err))

		badDefs := append([]ToolDefinition(nil), defs...)

		badDefs[0].ParametersJSON = `{`
		_, err = buildToolSchemaExportFromSources(manifest, badDefs, profiles)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected invalid schema error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("is not valid JSON"), fmt.Sprintf("expected invalid schema error, got %v", err))

		noProfiles := make(map[string][]string, len(profiles))
		for tool, values := range profiles {
			noProfiles[tool] = append([]string(nil), values...)
		}

		delete(noProfiles, manifest[0].Tool)

		_, err = buildToolSchemaExportFromSources(manifest, defs, noProfiles)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("expected missing profile error, got %v", err))
		gomega.Expect(err.Error()).To(gomega.ContainSubstring("has no exported profile"), fmt.Sprintf("expected missing profile error, got %v", err))

	})
	ginkgo.It("harness audit tool surface detects export suggestion and stale blocker drift", func() {
		manifest := append(toolManifestToolsAcrossBuiltInSurfaces(), "manifest_only")
		defs := append(BuildToolDefinitions(nil), ToolDefinition{
			Name:           "schema_only",
			Description:    "drift fixture",
			ParametersJSON: `{"type":"object","properties":{}}`,
		})
		suggestions := append(toolManifestToolsAcrossBuiltInSurfaces(), "suggestion_only")

		export, err := BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport: %v", err))

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
		gomega.Expect(surface.DriftFindingCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected drift findings, got %#v", surface))
		gomega.Expect(surface.ManifestWithoutSchema).To(gomega.ContainElement("manifest_only"), fmt.Sprintf("expected manifest drift, got %#v", surface))
		gomega.Expect(surface.ManifestWithoutExportedSchema).To(gomega.ContainElement("manifest_only"), fmt.Sprintf("expected manifest drift, got %#v", surface))
		gomega.Expect(surface.SchemaWithoutManifest).To(gomega.ContainElement("schema_only"), fmt.Sprintf("expected schema drift, got %#v", surface))
		gomega.Expect(surface.SchemaWithoutExportedSchema).To(gomega.ContainElement("schema_only"), fmt.Sprintf("expected schema drift, got %#v", surface))
		gomega.Expect(slices.Contains(surface.SuggestionWithoutManifest, "suggestion_only")).To(gomega.BeTrue(), fmt.Sprintf("expected stale suggestion drift, got %#v", surface))
		gomega.Expect(slices.Contains(surface.StaleUseBlockerMissingTools, "context_query")).To(gomega.BeTrue(), fmt.Sprintf("expected stale-use blocker coverage drift, got %#v", surface.StaleUseBlockerCoverage))

		findings := toolSurfaceFindings(surface)
		for _, want := range []string{"exported_schema_drift", "stale_tool_suggestion", "stale_use_blocker_gap"} {
			gomega.Expect(slices.ContainsFunc(findings, func(finding harnessAuditFinding) bool { return finding.ID == want })).To(gomega.BeTrue(), fmt.Sprintf("expected finding %q in %#v", want, findings))

		}
	})
})
