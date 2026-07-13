package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("capabilities", func() {
	ginkgo.It("read only capability registry maps stable vocabulary to adapters", func() {
		registry := BuildReadOnlyCapabilityRegistry()
		gomega.Expect(registry.SchemaVersion).To(gomega.Equal(readOnlyCapabilitySchemaVersion), fmt.Sprintf("schema version = %q", registry.SchemaVersion))
		gomega.Expect(registry.ResolverRuntimeEnabled).To(gomega.BeTrue(), "P3.2 resolver should be enabled by default")
		gomega.Expect(registry.PublicStatusEnabled).To(gomega.BeTrue(), "public capability status should be enabled by default")
		gomega.Expect(registry.DirectToolFallback).To(gomega.BeTrue(), "direct tool names must remain available as fallback")

		wantIDs := []string{
			string(ReadOnlyCapabilityLocalizeChange),
			string(ReadOnlyCapabilityQueryContext),
			string(ReadOnlyCapabilityFileOutline),
			string(ReadOnlyCapabilityInspectSymbol),
			string(ReadOnlyCapabilitySymbolLookup),
			string(ReadOnlyCapabilityDefinition),
			string(ReadOnlyCapabilityReferences),
			string(ReadOnlyCapabilityImplementations),
			string(ReadOnlyCapabilityCallsFrom),
			string(ReadOnlyCapabilityCallsInto),
			string(ReadOnlyCapabilityRelatedFiles),
			string(ReadOnlyCapabilityAuditBehavior),
		}
		gotIDs := make([]string, 0, len(registry.Capabilities))

		for _, capability := range registry.Capabilities {
			gotIDs = append(gotIDs, capability.ID)
			gomega.Expect(capability.ReadOnly).To(gomega.BeTrue(), fmt.Sprintf("%s should be read-only", capability.ID))
			gomega.Expect(slices.Equal(capability.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly})).To(gomega.BeTrue(), fmt.Sprintf("%s side effects = %#v", capability.ID, capability.SideEffects))
			gomega.Expect(capability.Adapters).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s has no adapter mapping", capability.ID))

			for _, adapter := range capability.Adapters {
				gomega.Expect(adapter.Tool).NotTo(gomega.Equal(""), fmt.Sprintf("%s has empty adapter: %#v", capability.ID, adapter))
				gomega.Expect(IsWorkspaceWriteTool(adapter.Tool)).To(gomega.BeFalse(), fmt.Sprintf("%s maps to write adapter %s", capability.ID, adapter.Tool))
				gomega.Expect(slices.Equal(adapter.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly})).To(gomega.BeTrue(), fmt.Sprintf("%s adapter %s side effects = %#v", capability.ID, adapter.Tool, adapter.SideEffects))

			}
		}
		gomega.Expect(slices.Equal(gotIDs, wantIDs)).To(gomega.BeTrue(), fmt.Sprintf("capability IDs = %#v, want %#v", gotIDs, wantIDs))

		definition, ok := ResolveReadOnlyCapability("definition", nil)
		gomega.Expect(ok).To(gomega.BeTrue(), "definition capability should resolve")
		gomega.Expect(slices.ContainsFunc(definition.Capability.Adapters, func(adapter ReadOnlyCapabilityAdapterSpec) bool {
			return adapter.Tool == "code_navigate" && adapter.Capability == string(semanticlanguage.CapDefinition)
		})).To(gomega.BeTrue(), fmt.Sprintf("definition should map to code_navigate definition route: %#v", definition.Capability.Adapters))
		gomega.Expect(slices.ContainsFunc(definition.Capability.Adapters, func(adapter ReadOnlyCapabilityAdapterSpec) bool {
			return adapter.Tool == "go_definition" && adapter.Language == "go"
		})).To(gomega.BeTrue(), fmt.Sprintf("definition should include direct Go adapter fallback: %#v", definition.Capability.Adapters))

		if _, ok := ResolveReadOnlyCapability("source-sanity", nil); ok {
			ginkgo.Fail("source-sanity must remain unmapped from the read-only capability vocabulary")
		}
	})
	ginkgo.It("read only capability resolver does not facade write capabilities", func() {
		for _, capability := range []string{
			"edit",
			"write",
			"apply-patch",
			"submit-plan",
		} {
			if resolution, ok := ResolveReadOnlyCapability(capability, nil); ok {
				ginkgo.Fail(fmt.Sprintf("write capability %q must not resolve through read-only facade: %#v", capability, resolution))
			}
		}

		for _, tool := range []string{
			ToolNameEditFile,
			ToolNameWriteFile,
			ToolNameApplyPatch,
			ToolNameSubmitPlan,
		} {
			if capabilities := EvidenceCapabilitiesForTool(tool); len(capabilities) != 0 {
				ginkgo.Fail(fmt.Sprintf("write tool %s should not expose read-only evidence capabilities: %#v", tool, capabilities))
			}
			if got := CapabilityClassificationForTool(tool); got != CapabilityClassWriteSideEffect {
				ginkgo.Fail(fmt.Sprintf("write tool %s classification = %q, want write side effect", tool, got))
			}
		}
	})
	ginkgo.It("read only capability registry promotes stable subset of code navigate", func() {
		if implementations, ok := ResolveReadOnlyCapability(string(semanticlanguage.CapImplementations), nil); !ok ||
			implementations.Capability.ID != string(ReadOnlyCapabilityImplementations) {
			ginkgo.Fail(fmt.Sprintf("implementations should be promoted as a stable product capability, got ok=%v resolution=%#v", ok, implementations))
		}

		for _, capability := range []string{
			string(semanticlanguage.CapExtractBody),
		} {
			if _, ok := ResolveReadOnlyCapability(capability, nil); ok {
				ginkgo.Fail(fmt.Sprintf("%s should remain outside read_only_capability until promoted as a stable product capability", capability))
			}
		}

		codeNavigateCapabilities := CodeNavigationEvidenceCapabilities()
		gomega.Expect(slices.Contains(codeNavigateCapabilities, "find_implementations")).To(gomega.BeTrue(), fmt.Sprintf("code_navigate should expose implementation lookup evidence capability: %#v", codeNavigateCapabilities))
		gomega.Expect(slices.Contains(codeNavigateCapabilities, "inspect_symbol")).To(gomega.BeTrue(), fmt.Sprintf("code_navigate should expose full-body/source-body evidence capability: %#v", codeNavigateCapabilities))
		gomega.Expect(slices.Equal(ReadOnlyCapabilityEvidenceCapabilities(), codeNavigateCapabilities)).To(gomega.BeFalse(), fmt.Sprintf("read_only_capability projection should remain a smaller stable product vocabulary, got same as code_navigate: %#v", codeNavigateCapabilities))

	})
	ginkgo.It("read only capability evidence capabilities project resolver vocabulary", func() {
		want := []string{
			"localize_change",
			"query_context",
			"inspect_file",
			"inspect_symbol",
			"find_symbol",
			"find_definition",
			"find_references",
			"find_implementations",
			"calls_from",
			"calls_into",
			"related_files",
			"audit_behavior",
		}
		got := ReadOnlyCapabilityEvidenceCapabilities()
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("read-only evidence capability projection = %#v, want %#v", got, want))

		got[0] = "mutated"

		if next := ReadOnlyCapabilityEvidenceCapabilities(); !slices.Equal(next, want) {
			ginkgo.Fail(fmt.Sprintf("projection should return an isolated slice, got %#v", next))
		}

		if got := EvidenceCapabilitiesForTool(ToolNameReadOnlyCapability); !slices.Equal(got, want) {
			ginkgo.Fail(fmt.Sprintf("%s evidence capabilities = %#v, want resolver projection %#v", ToolNameReadOnlyCapability, got, want))
		}

		codeNavigateWant := CodeNavigationEvidenceCapabilities()
		if got := EvidenceCapabilitiesForTool("code_navigate"); !slices.Equal(got, codeNavigateWant) {
			ginkgo.Fail(fmt.Sprintf("code_navigate evidence capabilities = %#v, want code navigation projection %#v", got, codeNavigateWant))
		}
		gomega.Expect(slices.Contains(codeNavigateWant, "find_implementations")).To(gomega.BeTrue(), fmt.Sprintf("code_navigate projection should include runtime-only implementation lookup capability, got %#v", codeNavigateWant))

	})
	ginkgo.It("read only capability resolver can be disabled", func() {
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityResolverEnv, "0")

		registry := BuildReadOnlyCapabilityRegistry()
		gomega.Expect(registry.ResolverRuntimeEnabled).To(gomega.BeFalse(), "resolver should honor disable env")

		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadOnlyCapability, Capability: "definition", Pattern: "Run", Path: "."})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("disabled resolver should return a structured fallback error, got %#v", result))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.FallbackReason).To(gomega.Equal("resolver_runtime_disabled"), fmt.Sprintf("disabled fallback payload = %#v", payload))
		gomega.Expect(payload.Routed).To(gomega.BeFalse(), fmt.Sprintf("disabled fallback payload = %#v", payload))
		gomega.Expect(payload.DirectToolFallback).To(gomega.BeTrue(), fmt.Sprintf("direct tool fallback should remain visible: %#v", payload))
		gomega.Expect(payload.Status.State).To(gomega.Equal("fallback"), fmt.Sprintf("disabled status should be machine-readable, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("disabled"), fmt.Sprintf("disabled status should be machine-readable, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.Reason).To(gomega.Equal("resolver_runtime_disabled"), fmt.Sprintf("disabled status should be machine-readable, got %#v", payload.Status))
		gomega.Expect(payload.Status.SelectedAdapter).To(gomega.BeNil(), fmt.Sprintf("disabled status should be machine-readable, got %#v", payload.Status))
		gomega.Expect(payload.Status.UnresolvedGaps).NotTo(gomega.BeEmpty(), fmt.Sprintf("disabled status should be machine-readable, got %#v", payload.Status))

	})
	ginkgo.It("read only capability route can be disabled individually", func() {
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityDisabledRoutesEnv, "definition, related_files")

		registry := BuildReadOnlyCapabilityRegistry()
		gomega.Expect(registry.ResolverRuntimeEnabled).To(gomega.BeTrue(), "per-route disable should not disable the resolver globally")

		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       filepath.Join("pkg", "runner.go"),
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("disabled route should return a structured fallback error, got %#v", result))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.FallbackReason).To(gomega.Equal("capability_route_disabled"), fmt.Sprintf("route-disabled fallback payload = %#v", payload))
		gomega.Expect(payload.Routed).To(gomega.BeFalse(), fmt.Sprintf("route-disabled fallback payload = %#v", payload))
		gomega.Expect(payload.DirectToolFallback).To(gomega.BeTrue(), fmt.Sprintf("route-disabled fallback payload = %#v", payload))
		gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("disabled"), fmt.Sprintf("route-disabled fallback payload = %#v", payload))
		gomega.Expect(payload.Status.Fallback.Reason).To(gomega.Equal("capability_route_disabled"), fmt.Sprintf("route-disabled fallback payload = %#v", payload))

		coverage := ReadOnlyCapabilityCoverageForTools(nil)
		gomega.Expect(coverage.DisabledCapabilityCount).To(gomega.BeNumerically("==", 2), fmt.Sprintf("coverage should report individually disabled routes, got %#v", coverage))
		gomega.Expect(coverage.DisabledCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityDefinition)), fmt.Sprintf("coverage should report individually disabled routes, got %#v", coverage))
		gomega.Expect(coverage.DisabledCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityRelatedFiles)), fmt.Sprintf("coverage should report individually disabled routes, got %#v", coverage))
		gomega.Expect(slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityDefinition))).To(gomega.BeFalse(), fmt.Sprintf("disabled definition route should not be counted as routed: %#v", coverage.RoutedCapabilities))
		gomega.Expect(coverage.RouteStateCounts["disabled"]).To(gomega.BeNumerically("==", 2), fmt.Sprintf("route state counts should include only disabled routed-capability states, got %#v", coverage.RouteStateCounts))
		gomega.Expect(coverage.RouteStateCounts["deferred"]).To(gomega.BeNumerically("==", 0), fmt.Sprintf("route state counts should include only disabled routed-capability states, got %#v", coverage.RouteStateCounts))

	})
	ginkgo.It("read only capability public status can be suppressed for routed results", func() {
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityPublicStatusEnv, "0")

		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       filepath.Join("pkg", "runner.go"),
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_only_capability returned error: %v\n%s", result.Error, result.Output))

		var raw map[string]any
		gomega.Expect(json.Unmarshal([]byte(result.Output), &raw)).To(gomega.Succeed())

		if _, ok := raw["status"]; ok {
			ginkgo.Fail(fmt.Sprintf("public status should be suppressed, got %s", result.Output))
		}
		gomega.Expect(raw["schema_version"]).To(gomega.Equal(readOnlyCapabilityResultVersion), fmt.Sprintf("suppressed payload should remain structured, got %#v", raw))
		gomega.Expect(raw["routed"]).To(gomega.BeTrue(), fmt.Sprintf("suppressed payload should remain structured, got %#v", raw))
		gomega.Expect(raw["routed_tool"]).To(gomega.Equal("code_navigate"), fmt.Sprintf("suppressed payload should remain structured, got %#v", raw))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["capability_status_state"]).To(gomega.Equal("routed"), fmt.Sprintf("suppressed status telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_state"]).To(gomega.Equal("none"), fmt.Sprintf("suppressed status telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_selected_adapter"]).To(gomega.Equal("code_navigate"), fmt.Sprintf("suppressed status telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_precision_tier"]).NotTo(gomega.BeEmpty(), fmt.Sprintf("suppressed status telemetry fields = %#v", fields))

	})
	ginkgo.It("read only capability public status can be suppressed for disabled fallback", func() {
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityResolverEnv, "0")
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityPublicStatusEnv, "0")

		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       ".",
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("disabled resolver should return a structured fallback error, got %#v", result))

		var raw map[string]any
		gomega.Expect(json.Unmarshal([]byte(result.Output), &raw)).To(gomega.Succeed())

		if _, ok := raw["status"]; ok {
			ginkgo.Fail(fmt.Sprintf("public status should be suppressed for disabled fallback, got %s", result.Output))
		}
		gomega.Expect(raw["fallback_reason"]).To(gomega.Equal("resolver_runtime_disabled"), fmt.Sprintf("suppressed disabled fallback should remain structured, got %#v", raw))
		gomega.Expect(raw["resolver_runtime_enabled"]).To(gomega.BeFalse(), fmt.Sprintf("suppressed disabled fallback should remain structured, got %#v", raw))
		gomega.Expect(raw["direct_tool_fallback"]).To(gomega.BeTrue(), fmt.Sprintf("suppressed disabled fallback should remain structured, got %#v", raw))
		gomega.Expect(raw["routed"]).To(gomega.BeFalse(), fmt.Sprintf("suppressed disabled fallback should remain structured, got %#v", raw))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["capability_status_state"]).To(gomega.Equal("fallback"), fmt.Sprintf("suppressed disabled fallback telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_state"]).To(gomega.Equal("disabled"), fmt.Sprintf("suppressed disabled fallback telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_reason"]).To(gomega.Equal("resolver_runtime_disabled"), fmt.Sprintf("suppressed disabled fallback telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback"]).To(gomega.BeTrue(), fmt.Sprintf("suppressed disabled fallback telemetry fields = %#v", fields))

	})
	ginkgo.It("read only capability adapters match schema side effect truth", func() {
		export, err := BuildToolSchemaExport(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("BuildToolSchemaExport returned error: %v", err))

		exported := make(map[string]ExportedToolSchema, len(export.Tools))
		for _, tool := range export.Tools {
			exported[tool.Name] = tool
		}

		registry := BuildReadOnlyCapabilityRegistry()
		for _, capability := range registry.Capabilities {
			for _, adapter := range capability.Adapters {
				schemaTool, ok := exported[adapter.Tool]
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s adapter %s is not present in schema export", capability.ID, adapter.Tool))
				gomega.Expect(slices.Equal(adapter.SideEffects, schemaTool.Policy.SideEffects)).To(gomega.BeTrue(), fmt.Sprintf("%s adapter %s declared side effects %#v, schema truth %#v", capability.ID, adapter.Tool, adapter.SideEffects, schemaTool.Policy.SideEffects))
				gomega.Expect(slices.Equal(schemaTool.Policy.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly})).To(gomega.BeTrue(), fmt.Sprintf("%s adapter %s schema side effects are not read-only-only: %#v", capability.ID, adapter.Tool, schemaTool.Policy.SideEffects))

			}
		}

		sourceSanity, ok := exported["source_sanity"]
		gomega.Expect(ok).To(gomega.BeTrue(), "source_sanity should remain present as a direct tool schema")
		gomega.Expect(slices.Contains(sourceSanity.Policy.SideEffects, autonomy.SideEffectUnknown)).To(gomega.BeTrue(), fmt.Sprintf("source_sanity schema truth should expose unknown side effects as a direct tool: %#v", sourceSanity.Policy.SideEffects))

	})
	ginkgo.It("resolve read only capability filters available adapters and aliases", func() {
		resolution, ok := ResolveReadOnlyCapability("reference", []string{"code_navigate", "go_references"})
		gomega.Expect(ok).To(gomega.BeTrue(), "reference alias should resolve")
		gomega.Expect(resolution.Capability.ID).To(gomega.Equal(string(ReadOnlyCapabilityReferences)), fmt.Sprintf("capability ID = %q", resolution.Capability.ID))

		gotTools := capabilityAdapterTools(resolution.AvailableAdapters)
		gomega.Expect(slices.Equal(gotTools, []string{"code_navigate", "go_references"})).To(gomega.BeTrue(), fmt.Sprintf("available adapters = %#v", gotTools))
		gomega.Expect(resolution.UnavailableAdapters).NotTo(gomega.BeEmpty(), "expected unavailable adapters when filtering to a narrow tool set")

		outline, ok := ResolveReadOnlyCapability("file_outline", []string{"code_navigate"})
		gomega.Expect(ok).To(gomega.BeTrue(), "underscore alias should resolve")
		gomega.Expect(outline.Capability.ID).To(gomega.Equal(string(ReadOnlyCapabilityFileOutline)), fmt.Sprintf("outline capability ID = %q", outline.Capability.ID))

		if got := capabilityAdapterTools(outline.AvailableAdapters); !slices.Equal(got, []string{"code_navigate"}) {
			ginkgo.Fail(fmt.Sprintf("outline available adapters = %#v", got))
		}

		inspect, ok := ResolveReadOnlyCapability("inspect_symbol", []string{"code_navigate", "go_read_symbol"})
		gomega.Expect(ok).To(gomega.BeTrue(), "design vocabulary alias should resolve")
		gomega.Expect(inspect.Capability.ID).To(gomega.Equal(string(ReadOnlyCapabilityInspectSymbol)), fmt.Sprintf("inspect capability ID = %q", inspect.Capability.ID))

		routes := make([]string, 0, len(inspect.AvailableAdapters))
		for _, adapter := range inspect.AvailableAdapters {
			routes = append(routes, adapter.Tool+":"+adapter.Capability)
		}
		if !slices.Equal(routes, []string{
			"code_navigate:read_symbol",
			"code_navigate:extract_body",
			"go_read_symbol:read_symbol",
		}) {
			ginkgo.Fail(fmt.Sprintf("inspect-symbol available adapter routes = %#v", routes))
		}
	})
	ginkgo.It("read only capability runtime resolution does not use workspace manifest detection", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "notes.txt"), "Run is documented here.\n")
		writeTestFile(filepath.Join(root, "src", "main.cpp"), "void run() {}\n")
		writeTestFile(filepath.Join(root, "scripts", "tool.py"), "def run():\n    return None\n")
		writeTestFile(filepath.Join(root, "web", "main.ts"), "export function run() {}\n")
		writeTestFile(filepath.Join(root, "crates", "lib.rs"), "fn run() {}\n")
		writeTestFile(filepath.Join(root, "java", "App.java"), "class App { void run() {} }\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})
		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       "notes.txt",
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_only_capability returned error: %v\n%s", result.Error, result.Output))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.Routed).To(gomega.BeTrue(), fmt.Sprintf("expected bounded code_navigate route, got %#v", payload))
		gomega.Expect(payload.RoutedTool).To(gomega.Equal("code_navigate"), fmt.Sprintf("expected bounded code_navigate route, got %#v", payload))

		if got := capabilityAdapterTools(payload.AvailableAdapters); !slices.Equal(got, []string{"code_navigate"}) {
			ginkgo.Fail(fmt.Sprintf("path-specific runtime resolver should not expose workspace-detected language adapters, got %#v", got))
		}

		pyRequest := ToolRequest{Path: filepath.Join("scripts", "tool.py")}
		pyResolution, ok := ResolveReadOnlyCapability("definition", readOnlyCapabilityResolverTools(pyRequest))
		gomega.Expect(ok).To(gomega.BeTrue(), "python path should resolve definition capability")

		pyTools := capabilityAdapterTools(pyResolution.AvailableAdapters)
		gomega.Expect(slices.Equal(pyTools, []string{"code_navigate", "python_definition"})).To(gomega.BeTrue(), fmt.Sprintf("python path should keep only code_navigate plus direct python fallback, got %#v", pyTools))

	})
	ginkgo.It("read only capability path language beats conflicting override", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
		writeTestFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       filepath.Join("pkg", "runner.go"),
			Language:   "python",
			Pattern:    "Run",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_only_capability returned error: %v\n%s", result.Error, result.Output))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.UnderlyingResult).NotTo(gomega.BeNil(), fmt.Sprintf("expected path-derived Go definition route, got %#v", payload.UnderlyingResult))
		gomega.Expect(payload.UnderlyingResult.Output).To(gomega.ContainSubstring("routed_tool: go_definition"), fmt.Sprintf("expected path-derived Go definition route, got %#v", payload.UnderlyingResult))

		if got := capabilityAdapterTools(payload.AvailableAdapters); !slices.Equal(got, []string{"code_navigate", "go_definition"}) {
			ginkgo.Fail(fmt.Sprintf("expected path-derived Go adapters only, got %#v", got))
		}
		gomega.Expect(payload.ResolvedTarget.Language).To(gomega.Equal("python"), fmt.Sprintf("resolved target should preserve request target, got %#v", payload.ResolvedTarget))
		gomega.Expect(payload.ResolvedTarget.Path).To(gomega.Equal("pkg/runner.go"), fmt.Sprintf("resolved target should preserve request target, got %#v", payload.ResolvedTarget))
		gomega.Expect(payload.FreshnessState).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected result contract metadata, got freshness=%q truncation=%#v counts=%#v followups=%#v", payload.FreshnessState, payload.TruncationState, payload.ResultCounts, payload.FollowUpCapabilities))
		gomega.Expect(payload.TruncationState.State).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected result contract metadata, got freshness=%q truncation=%#v counts=%#v followups=%#v", payload.FreshnessState, payload.TruncationState, payload.ResultCounts, payload.FollowUpCapabilities))
		gomega.Expect(payload.ResultCounts.OutputLineCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected result contract metadata, got freshness=%q truncation=%#v counts=%#v followups=%#v", payload.FreshnessState, payload.TruncationState, payload.ResultCounts, payload.FollowUpCapabilities))
		gomega.Expect(payload.FollowUpCapabilities).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected result contract metadata, got freshness=%q truncation=%#v counts=%#v followups=%#v", payload.FreshnessState, payload.TruncationState, payload.ResultCounts, payload.FollowUpCapabilities))

	})
	ginkgo.Describe("read only capability routes named slice through existing adapters", func() {
		tests := []struct {
			name           string
			req            ToolRequest
			wantCapability string
			wantAdapter    string
		}{
			{
				name: "file outline",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityFileOutline),
					Path:       filepath.Join("pkg", "runner.go"),
				},
				wantCapability: string(ReadOnlyCapabilityFileOutline),
				wantAdapter:    "go_file_outline",
			},
			{
				name: "inspect symbol",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: "inspect_symbol",
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "Run",
				},
				wantCapability: string(ReadOnlyCapabilityInspectSymbol),
				wantAdapter:    "go_read_symbol",
			},
			{
				name: "symbol lookup",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilitySymbolLookup),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "Run",
				},
				wantCapability: string(ReadOnlyCapabilitySymbolLookup),
				wantAdapter:    "go_find_symbol",
			},
			{
				name: "references",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityReferences),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "Run",
				},
				wantCapability: string(ReadOnlyCapabilityReferences),
				wantAdapter:    "go_references",
			},
			{
				name: "definition",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityDefinition),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "Run",
				},
				wantCapability: string(ReadOnlyCapabilityDefinition),
				wantAdapter:    "go_definition",
			},
			{
				name: "calls from",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityCallsFrom),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "Run",
				},
				wantCapability: string(ReadOnlyCapabilityCallsFrom),
				wantAdapter:    "go_calls_from",
			},
			{
				name: "calls into",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityCallsInto),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "helper",
				},
				wantCapability: string(ReadOnlyCapabilityCallsInto),
				wantAdapter:    "go_calls_into",
			},
			{
				name: "related files",
				req: ToolRequest{
					Tool:       ToolNameReadOnlyCapability,
					Capability: string(ReadOnlyCapabilityRelatedFiles),
					Path:       filepath.Join("pkg", "runner.go"),
					Pattern:    "runner",
				},
				wantCapability: string(ReadOnlyCapabilityRelatedFiles),
				wantAdapter:    "go_related_files",
			},
		}

		for _, tc := range tests {
			ginkgo.It(tc.name, func() {
				root := ginkgo.GinkgoT().TempDir()
				writeTestFile(filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
				writeTestFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn helper()\n}\n\nfunc helper() string {\n\treturn \"ok\"\n}\n")
				searchBackend, err := resolveContentSearchBackend()
				if err != nil {
					ginkgo.Skip(fmt.Sprintf("no supported content search backend installed on this machine: %v", err))
				}
				bin := filepath.Join(root, "bin")
				gomega.Expect(os.MkdirAll(bin, 0o755)).To(gomega.Succeed())

				searchTool := filepath.Join(bin, filepath.Base(searchBackend.bin))
				writeTestFile(searchTool, fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", searchBackend.bin))
				gomega.Expect(os.Chmod(searchTool, 0o755)).To(gomega.Succeed())
				ginkgo.GinkgoT().Setenv("PATH", bin)

				mgr := NewManager(config.Workspace{
					Root:                  root,
					CWD:                   root,
					CommandTimeoutSeconds: 30,
					MaxOutputBytes:        65536,
					MaxFileBytes:          65536,
					MaxDirectoryEntries:   1024,
					EnableShell:           true,
				})

				result := mgr.Execute(context.Background(), tc.req)
				gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("read_only_capability returned error: %v\n%s", result.Error, result.Output))

				var payload ReadOnlyCapabilityExecutionResult
				gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
				gomega.Expect(payload.Routed).To(gomega.BeTrue(), fmt.Sprintf("payload routing = %#v", payload))
				gomega.Expect(payload.Capability).To(gomega.Equal(tc.wantCapability), fmt.Sprintf("payload routing = %#v", payload))
				gomega.Expect(payload.RoutedTool).To(gomega.Equal("code_navigate"), fmt.Sprintf("expected route through code_navigate adapter, got %#v", payload))
				gomega.Expect(payload.RoutedAdapter).NotTo(gomega.BeNil(), fmt.Sprintf("expected route through code_navigate adapter, got %#v", payload))
				gomega.Expect(payload.RoutedAdapter.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("expected route through code_navigate adapter, got %#v", payload))
				gomega.Expect(payload.UnderlyingResult).NotTo(gomega.BeNil(), fmt.Sprintf("expected underlying code_navigate result, got %#v", payload.UnderlyingResult))
				gomega.Expect(payload.UnderlyingResult.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("expected underlying code_navigate result, got %#v", payload.UnderlyingResult))
				gomega.Expect(payload.Status.State).To(gomega.Equal("routed"), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.SelectedAdapter).NotTo(gomega.BeNil(), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.SelectedAdapter.Tool).To(gomega.Equal("code_navigate"), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.SelectedAdapter.Source).To(gomega.Equal("capability_router"), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.SelectedAdapter.Provenance).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.PrecisionTier).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.Confidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("none"), fmt.Sprintf("expected successful route status with adapter provenance and precision, got %#v", payload.Status))
				gomega.Expect(payload.UnderlyingResult.Output).To(gomega.ContainSubstring("routed_tool: "+tc.wantAdapter), fmt.Sprintf("expected underlying adapter %s, got:\n%s", tc.wantAdapter, payload.UnderlyingResult.Output))

			})
		}
	})
	ginkgo.It("read only capability reports unmapped source sanity facade", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadOnlyCapability, Capability: "source-sanity", Path: "."})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("unmapped capability should return an error with structured fallback, got %#v", result))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.FallbackReason).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.Routed).To(gomega.BeFalse(), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.Capability).To(gomega.BeEmpty(), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.RequestedCapability).To(gomega.Equal("source-sanity"), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.UnderlyingResult).To(gomega.BeNil(), fmt.Sprintf("unmapped read-only facade should not impersonate the direct source_sanity tool, got %#v", payload.UnderlyingResult))
		gomega.Expect(payload.Status.State).To(gomega.Equal("fallback"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("unmapped"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.Reason).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.UnresolvedGaps).NotTo(gomega.BeEmpty(), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["capability_fallback_state"]).To(gomega.Equal("unmapped"), fmt.Sprintf("unmapped telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_reason"]).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unmapped telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_unmapped"]).To(gomega.BeTrue(), fmt.Sprintf("unmapped telemetry fields = %#v", fields))

	})
	ginkgo.It("read only capability reports structured unmapped fallback", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadOnlyCapability, Capability: "unknown-capability", Path: "."})
		gomega.Expect(result.Error).NotTo(gomega.Equal(""), fmt.Sprintf("unmapped capability should return an error with structured fallback, got %#v", result))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.FallbackReason).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.Routed).To(gomega.BeFalse(), fmt.Sprintf("unexpected unmapped fallback payload: %#v", payload))
		gomega.Expect(payload.Status.State).To(gomega.Equal("fallback"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("unmapped"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.Fallback.Reason).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))
		gomega.Expect(payload.Status.UnresolvedGaps).NotTo(gomega.BeEmpty(), fmt.Sprintf("unmapped status should expose fallback details, got %#v", payload.Status))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["capability_fallback_state"]).To(gomega.Equal("unmapped"), fmt.Sprintf("unmapped telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_reason"]).To(gomega.Equal("capability_unmapped"), fmt.Sprintf("unmapped telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_unmapped"]).To(gomega.BeTrue(), fmt.Sprintf("unmapped telemetry fields = %#v", fields))

	})
	ginkgo.It("read only capability reports unsupported absent confidence", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "scripts", "player.gd"), "extends Node\n\nfunc _ready():\n\tpass\n")

		mgr := NewManager(config.Workspace{
			Root:                  root,
			CWD:                   root,
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        65536,
			MaxFileBytes:          65536,
			MaxDirectoryEntries:   1024,
			EnableShell:           true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameReadOnlyCapability,
			Capability: string(ReadOnlyCapabilityDefinition),
			Path:       filepath.Join("scripts", "player.gd"),
			Language:   "elixir",
			Pattern:    "MissingSymbol",
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("unsupported language fallback should report absence without tool error: %v\n%s", result.Error, result.Output))

		var payload ReadOnlyCapabilityExecutionResult
		gomega.Expect(json.Unmarshal([]byte(result.Output), &payload)).To(gomega.Succeed())
		gomega.Expect(payload.Routed).To(gomega.BeTrue(), fmt.Sprintf("expected routed absent status, got %#v", payload))
		gomega.Expect(payload.Status.State).To(gomega.Equal("absent"), fmt.Sprintf("expected routed absent status, got %#v", payload))
		gomega.Expect(payload.Status.Fallback.State).To(gomega.Equal("unsupported"), fmt.Sprintf("expected unsupported-language fallback status, got %#v", payload.Status.Fallback))
		gomega.Expect(payload.Status.Fallback.Reason).To(gomega.Equal("unsupported_language"), fmt.Sprintf("expected unsupported-language fallback status, got %#v", payload.Status.Fallback))
		gomega.Expect(payload.Status.Fallback.SourceReason).To(gomega.ContainSubstring("no registered language route"), fmt.Sprintf("expected unsupported-language fallback status, got %#v", payload.Status.Fallback))
		gomega.Expect(payload.Status.AbsenceConfidence).NotTo(gomega.BeNil(), fmt.Sprintf("expected absence confidence from generic fallback, got %#v", payload.Status.AbsenceConfidence))
		gomega.Expect(payload.Status.AbsenceConfidence.Level).To(gomega.Equal("low"), fmt.Sprintf("expected absence confidence from generic fallback, got %#v", payload.Status.AbsenceConfidence))
		gomega.Expect(payload.Status.AbsenceConfidence.Reason).To(gomega.Equal("generic_fallback_no_matches"), fmt.Sprintf("expected absence confidence from generic fallback, got %#v", payload.Status.AbsenceConfidence))
		gomega.Expect(payload.Status.AbsenceConfidence.PrecisionTier).To(gomega.Equal(PrecisionTierRGFallback), fmt.Sprintf("expected absence confidence from generic fallback, got %#v", payload.Status.AbsenceConfidence))
		gomega.Expect(payload.Status.PrecisionTier).To(gomega.Equal(PrecisionTierRGFallback), fmt.Sprintf("expected low-confidence rg fallback status, got %#v", payload.Status))
		gomega.Expect(payload.Status.Confidence).To(gomega.Equal("low"), fmt.Sprintf("expected low-confidence rg fallback status, got %#v", payload.Status))

		statusJSON, err := json.Marshal(payload.Status)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), err)
		gomega.Expect(strings.ToLower(string(statusJSON))).NotTo(gomega.ContainSubstring("pass a path"), fmt.Sprintf("status must not prescribe next actions: %s", statusJSON))

		fields := ToolTelemetryFields(result.Tool, result.Output)
		gomega.Expect(fields["capability_status_state"]).To(gomega.Equal("absent"), fmt.Sprintf("absence telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_state"]).To(gomega.Equal("unsupported"), fmt.Sprintf("absence telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_fallback_reason"]).To(gomega.Equal("unsupported_language"), fmt.Sprintf("absence telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_absence_confidence"]).To(gomega.Equal("low"), fmt.Sprintf("absence telemetry fields = %#v", fields))
		gomega.Expect(fields["capability_precision_tier"]).To(gomega.Equal(PrecisionTierRGFallback), fmt.Sprintf("absence telemetry fields = %#v", fields))

	})
	ginkgo.It("read only capability coverage reports unmapped adapters", func() {
		coverage := ReadOnlyCapabilityCoverageForTools(nil)
		gomega.Expect(coverage.CapabilityCount).To(gomega.Equal(len(BuildReadOnlyCapabilityRegistry().Capabilities)), fmt.Sprintf("capability count = %d", coverage.CapabilityCount))
		gomega.Expect(coverage.StatusFieldCount).To(gomega.Equal(jsonTaggedFieldCount(reflect.TypeOf(ReadOnlyCapabilityStatus{}))), fmt.Sprintf("status field count = %d, want current JSON field count", coverage.StatusFieldCount))
		gomega.Expect(coverage.PublicStatusEnabled).To(gomega.BeTrue(), "coverage should report public status enabled by default")
		gomega.Expect(coverage.MappedAdapterCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected non-zero mapped/read-only counts: %#v", coverage))
		gomega.Expect(coverage.ReadOnlyAdapterCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected non-zero mapped/read-only counts: %#v", coverage))
		gomega.Expect(coverage.MappedReadOnlyAdapterCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected measurable positive coverage: %#v", coverage))
		gomega.Expect(coverage.Coverage).NotTo(gomega.BeNumerically("<=", 0), fmt.Sprintf("expected measurable positive coverage: %#v", coverage))
		gomega.Expect(coverage.StatusFieldCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected status/fallback state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackStateCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected status/fallback state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackStates).To(gomega.ContainElement("unmapped"), fmt.Sprintf("expected status/fallback state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackStates).To(gomega.ContainElement("unsupported"), fmt.Sprintf("expected status/fallback state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilityCount).To(gomega.BeNumerically("==", 12), fmt.Sprintf("expected routed read-only capabilities in coverage: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityLocalizeChange)), fmt.Sprintf("expected routed read-only capabilities in coverage: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityDefinition)), fmt.Sprintf("expected routed read-only capabilities in coverage: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityRelatedFiles)), fmt.Sprintf("expected routed read-only capabilities in coverage: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityAuditBehavior)), fmt.Sprintf("expected routed read-only capabilities in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackReasonCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected fallback reason metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackReasons).To(gomega.ContainElement("capability_unmapped"), fmt.Sprintf("expected fallback reason metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.FallbackReasons).NotTo(gomega.ContainElement("capability_deferred"), fmt.Sprintf("read-only capability coverage should not preserve deferred facade metrics: %#v", coverage))
		gomega.Expect(coverage.FallbackStates).NotTo(gomega.ContainElement("deferred"), fmt.Sprintf("read-only capability coverage should not preserve deferred facade metrics: %#v", coverage))
		gomega.Expect(coverage.RouteStateCounts["routed"]).To(gomega.BeNumerically("==", 12), fmt.Sprintf("expected routed-only route state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.RouteStateCounts["deferred"]).To(gomega.BeNumerically("==", 0), fmt.Sprintf("expected routed-only route state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.RouteStates).NotTo(gomega.BeEmpty(), fmt.Sprintf("expected routed-only route state metrics in coverage: %#v", coverage))
		gomega.Expect(coverage.DisabledCapabilityCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("unexpected disabled routes in default coverage: %#v", coverage))
		gomega.Expect(coverage.DisabledCapabilities).To(gomega.BeEmpty(), fmt.Sprintf("unexpected disabled routes in default coverage: %#v", coverage))
		gomega.Expect(coverage.UnmappedAdapterCount).To(gomega.Equal(len(coverage.UnmappedAdapters)), fmt.Sprintf("unmapped count drift: %#v", coverage))
		gomega.Expect(coverage.UnsafeMappedAdapterCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("read-only capability registry should have zero unsafe schema mappings: %#v", coverage.UnsafeMappedAdapters))
		gomega.Expect(coverage.UnsafeMappedAdapters).To(gomega.BeEmpty(), fmt.Sprintf("read-only capability registry should have zero unsafe schema mappings: %#v", coverage.UnsafeMappedAdapters))
		gomega.Expect(slices.Contains(coverage.UnmappedAdapters, "context_pack")).To(gomega.BeFalse(), fmt.Sprintf("context_pack should be covered by localize-change in Phase 5: %#v", coverage.UnmappedAdapters))
		gomega.Expect(slices.Contains(coverage.UnmappedAdapters, "go_definition")).To(gomega.BeFalse(), fmt.Sprintf("go_definition should be covered by the definition capability: %#v", coverage.UnmappedAdapters))

	})
	ginkgo.It("read only capability coverage keeps status metrics when public status suppressed", func() {
		ginkgo.GinkgoT().Setenv(readOnlyCapabilityPublicStatusEnv, "0")

		coverage := ReadOnlyCapabilityCoverageForTools(nil)
		gomega.Expect(coverage.PublicStatusEnabled).To(gomega.BeFalse(), "coverage should report public status suppression")
		gomega.Expect(coverage.StatusFieldCount).To(gomega.Equal(jsonTaggedFieldCount(reflect.TypeOf(ReadOnlyCapabilityStatus{}))), fmt.Sprintf("status field count = %d, want current JSON field count", coverage.StatusFieldCount))
		gomega.Expect(coverage.FallbackStateCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("suppressed public status must retain fallback state metrics: %#v", coverage))
		gomega.Expect(coverage.FallbackStates).To(gomega.ContainElement("disabled"), fmt.Sprintf("suppressed public status must retain fallback state metrics: %#v", coverage))
		gomega.Expect(coverage.FallbackReasonCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("suppressed public status must retain fallback reason metrics: %#v", coverage))
		gomega.Expect(coverage.FallbackReasons).To(gomega.ContainElement("resolver_runtime_disabled"), fmt.Sprintf("suppressed public status must retain fallback reason metrics: %#v", coverage))
		gomega.Expect(coverage.RoutedCapabilityCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("public status suppression should not disable routing metrics: %#v", coverage))

	})
	ginkgo.It("read only capability route states treat non runtime registry entries as unsupported", func() {
		registry := ReadOnlyCapabilityRegistry{
			ResolverRuntimeEnabled: true,
			Capabilities: []ReadOnlyCapabilitySpec{
				readOnlyCapability(
					ReadOnlyCapabilityID("fixture-non-runtime"),
					"Fixture capability outside the runtime-routed resolver set.",
					[]ReadOnlyCapabilityAdapterSpec{
						readOnlyAdapter(ToolNameReadFile, EvidenceCapabilityInspectFile, "", "fixture"),
					},
				),
			},
		}

		states := readOnlyCapabilityRouteStates(registry, []string{ToolNameReadFile})
		gomega.Expect(states).To(gomega.HaveLen(1), fmt.Sprintf("route states = %#v, want one synthetic state", states))
		gomega.Expect(states[0].State).To(gomega.Equal("unsupported"), fmt.Sprintf("non-runtime capability state = %#v, want unsupported/not-runtime", states[0]))
		gomega.Expect(states[0].Reason).To(gomega.Equal("capability_not_runtime_routed"), fmt.Sprintf("non-runtime capability state = %#v, want unsupported/not-runtime", states[0]))

	})
	ginkgo.It("harness audit reports capability registry metrics", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{Root: root, CWD: root})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "tool_surface"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("harness_audit returned error: %v\n%s", result.Error, result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("capability_coverage="), fmt.Sprintf("summary should include capability coverage metric, got %q", result.Summary))

		var report harnessAuditReport
		gomega.Expect(json.Unmarshal([]byte(result.Output), &report)).To(gomega.Succeed())
		gomega.Expect(report.ToolSurface).NotTo(gomega.BeNil(), fmt.Sprintf("expected capability registry metrics, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.CapabilityRegistry).NotTo(gomega.BeNil(), fmt.Sprintf("expected capability registry metrics, got %#v", report.ToolSurface))
		gomega.Expect(report.ToolSurface.CapabilityToolCounts["find_definition"]).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected capability grouping metrics, got tool_counts=%#v class_counts=%#v", report.ToolSurface.CapabilityToolCounts, report.ToolSurface.CapabilityClassCounts))
		gomega.Expect(report.ToolSurface.CapabilityToolCounts["audit_behavior"]).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected capability grouping metrics, got tool_counts=%#v class_counts=%#v", report.ToolSurface.CapabilityToolCounts, report.ToolSurface.CapabilityClassCounts))
		gomega.Expect(report.ToolSurface.CapabilityClassCounts[CapabilityClassCompatibilityAdapter]).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected capability grouping metrics, got tool_counts=%#v class_counts=%#v", report.ToolSurface.CapabilityToolCounts, report.ToolSurface.CapabilityClassCounts))
		gomega.Expect(report.ToolSurface.CapabilityClassCounts[CapabilityClassDeferred]).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("expected capability grouping metrics, got tool_counts=%#v class_counts=%#v", report.ToolSurface.CapabilityToolCounts, report.ToolSurface.CapabilityClassCounts))

		got := report.ToolSurface.CapabilityRegistry
		want := ReadOnlyCapabilityCoverageForTools(toolManifestToolsAcrossBuiltInSurfaces())
		gomega.Expect(got.CapabilityCount).To(gomega.Equal(want.CapabilityCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.StatusFieldCount).To(gomega.Equal(want.StatusFieldCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.PublicStatusEnabled).To(gomega.Equal(want.PublicStatusEnabled), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.FallbackStateCount).To(gomega.Equal(want.FallbackStateCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.RoutedCapabilityCount).To(gomega.Equal(want.RoutedCapabilityCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.DisabledCapabilityCount).To(gomega.Equal(want.DisabledCapabilityCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.FallbackReasonCount).To(gomega.Equal(want.FallbackReasonCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.MappedReadOnlyAdapterCount).To(gomega.Equal(want.MappedReadOnlyAdapterCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.ReadOnlyAdapterCount).To(gomega.Equal(want.ReadOnlyAdapterCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.UnmappedAdapterCount).To(gomega.Equal(want.UnmappedAdapterCount), fmt.Sprintf("capability registry metrics = %#v, want %#v", got, want))
		gomega.Expect(got.ResolverRuntimeEnabled).To(gomega.BeTrue(), "harness audit should report enabled runtime resolver for P3.2")
		gomega.Expect(got.RoutedCapabilityCount).NotTo(gomega.BeNumerically("<", 3), fmt.Sprintf("harness audit should report routed capabilities, got %#v", got))
		gomega.Expect(got.RoutedCapabilities).To(gomega.ContainElement(string(ReadOnlyCapabilityFileOutline)), fmt.Sprintf("harness audit should report routed capabilities, got %#v", got))
		gomega.Expect(got.FallbackReasonCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("harness audit should report fallback reason metrics, got %#v", got))
		gomega.Expect(got.FallbackReasons).To(gomega.ContainElement("resolver_runtime_disabled"), fmt.Sprintf("harness audit should report fallback reason metrics, got %#v", got))
		gomega.Expect(got.FallbackStateCount).NotTo(gomega.BeNumerically("==", 0), fmt.Sprintf("harness audit should report fallback state metrics, got %#v", got))
		gomega.Expect(got.FallbackStates).To(gomega.ContainElement("disabled"), fmt.Sprintf("harness audit should report fallback state metrics, got %#v", got))
		gomega.Expect(got.RouteStateCounts["routed"]).To(gomega.BeNumerically("==", 12), fmt.Sprintf("harness audit should report routed route states without deferred facades, got %#v", got.RouteStateCounts))
		gomega.Expect(got.RouteStateCounts["deferred"]).To(gomega.BeNumerically("==", 0), fmt.Sprintf("harness audit should report routed route states without deferred facades, got %#v", got.RouteStateCounts))
		gomega.Expect(got.UnsafeMappedAdapterCount).To(gomega.BeNumerically("==", 0), fmt.Sprintf("harness audit should report zero unsafe read-only mappings, got %#v", got.UnsafeMappedAdapters))
		gomega.Expect(got.UnsafeMappedAdapters).To(gomega.BeEmpty(), fmt.Sprintf("harness audit should report zero unsafe read-only mappings, got %#v", got.UnsafeMappedAdapters))

		allFindings := append(append([]harnessAuditFinding(nil), report.Findings...), report.HistoricalFindings...)
		if got.UnmappedAdapterCount > 0 {
			gomega.Expect(allFindings).To(gomega.ContainElement(gomega.And(
				gomega.HaveField("ID", "unmapped_capability_adapter"),
				gomega.HaveField("Severity", "info"),
				gomega.HaveField("Tools", gomega.HaveLen(got.UnmappedAdapterCount)),
			)), fmt.Sprintf("harness audit should surface unmapped adapters as an informational finding, findings=%#v historical=%#v unmapped=%#v", report.Findings, report.HistoricalFindings, got.UnmappedAdapters))
		}

	})
	ginkgo.It("tool capability metadata uses one static source", func() {
		cases := []struct {
			tool                string
			wantCapabilities    []string
			wantClassification  string
			wantNoCapabilities  bool
			mutateFirstCall     bool
			wantFirstCapability string
		}{
			{
				tool:                ToolNameReadOnlyCapability,
				wantCapabilities:    []string{"localize_change", "query_context", "inspect_file", "inspect_symbol", "find_symbol", "find_definition", "find_references", "find_implementations", "calls_from", "calls_into", "related_files", "audit_behavior"},
				wantClassification:  CapabilityClassRouted,
				mutateFirstCall:     true,
				wantFirstCapability: "localize_change",
			},
			{
				tool:               "context_query",
				wantCapabilities:   []string{"localize_change", "query_context"},
				wantClassification: CapabilityClassRouted,
			},
			{
				tool:               "context_trace_pack",
				wantCapabilities:   []string{"verify_edit"},
				wantClassification: CapabilityClassDeferred,
			},
			{
				tool:               "read_file",
				wantCapabilities:   []string{"inspect_file"},
				wantClassification: CapabilityClassFallback,
			},
			{
				tool:               "ctags_symbols",
				wantCapabilities:   []string{"find_symbol"},
				wantClassification: CapabilityClassFallback,
			},
			{
				tool:               "harness_audit",
				wantCapabilities:   []string{"audit_behavior"},
				wantClassification: CapabilityClassRouted,
			},
			{
				tool:               "go_definition",
				wantCapabilities:   []string{"find_definition"},
				wantClassification: CapabilityClassCompatibilityAdapter,
			},
			{
				tool:               "go_workspace_symbol",
				wantCapabilities:   []string{"find_symbol"},
				wantClassification: CapabilityClassCompatibilityAdapter,
			},
			{
				tool:               "apply_patch",
				wantClassification: CapabilityClassWriteSideEffect,
				wantNoCapabilities: true,
			},
			{
				tool:               "submit_plan",
				wantClassification: CapabilityClassWriteSideEffect,
				wantNoCapabilities: true,
			},
			{
				tool:               "recovery_decompose",
				wantClassification: CapabilityClassWriteSideEffect,
				wantNoCapabilities: true,
			},
			{
				tool:               "context_index",
				wantClassification: CapabilityClassRouted,
				wantNoCapabilities: true,
			},
			{
				tool:               "context_unregistered",
				wantClassification: "",
				wantNoCapabilities: true,
			},
		}
		for _, tc := range cases {
			gotCapabilities := EvidenceCapabilitiesForTool(tc.tool)
			if tc.wantNoCapabilities {
				gomega.Expect(gotCapabilities).To(gomega.BeEmpty(), fmt.Sprintf("%s capabilities = %#v, want none", tc.tool, gotCapabilities))

			} else if !slices.Equal(gotCapabilities, tc.wantCapabilities) {
				ginkgo.Fail(fmt.Sprintf("%s capabilities = %#v, want %#v", tc.tool, gotCapabilities, tc.wantCapabilities))
			}

			if got := CapabilityClassificationForTool(tc.tool); got != tc.wantClassification {
				ginkgo.Fail(fmt.Sprintf("%s classification = %q, want %q", tc.tool, got, tc.wantClassification))
			}

			if tc.mutateFirstCall {
				gotCapabilities[0] = "mutated"

				next := EvidenceCapabilitiesForTool(tc.tool)
				gomega.Expect(next).NotTo(gomega.BeEmpty(), fmt.Sprintf("%s capabilities should be returned as a copy, got %#v", tc.tool, next))
				gomega.Expect(next[0]).To(gomega.Equal(tc.wantFirstCapability), fmt.Sprintf("%s capabilities should be returned as a copy, got %#v", tc.tool, next))

			}
		}

		for _, lang := range semanticlanguage.LanguageRegistry() {
			for capability, tool := range lang.Tools {
				if got := EvidenceCapabilitiesForTool(tool); len(got) == 0 {
					ginkgo.Fail(fmt.Sprintf("%s tool %s for %s should expose evidence capabilities", lang.Name, tool, capability))
				}

				if got := CapabilityClassificationForTool(tool); got == "" {
					ginkgo.Fail(fmt.Sprintf("%s tool %s for %s should expose capability classification", lang.Name, tool, capability))
				}
			}
		}

		for _, spec := range languagetools.ToolCatalog() {
			if !spec.Supplemental {
				continue
			}

			if got := EvidenceCapabilitiesForTool(spec.Tool); !slices.Equal(got, spec.EvidenceCapabilities) {
				ginkgo.Fail(fmt.Sprintf("supplemental language tool %s capabilities = %#v, want %#v", spec.Tool, got, spec.EvidenceCapabilities))
			}

			if got := CapabilityClassificationForTool(spec.Tool); got == "" {
				ginkgo.Fail(fmt.Sprintf("supplemental language tool %s should expose capability classification", spec.Tool))
			}
		}
	})
	ginkgo.It("read only capability evidence capability drives audit normalization", func() {
		cases := []struct {
			capability string
			want       string
		}{
			{capability: string(ReadOnlyCapabilityFileOutline), want: "inspect_file"},
			{capability: string(ReadOnlyCapabilityInspectSymbol), want: "inspect_symbol"},
			{capability: string(ReadOnlyCapabilitySymbolLookup), want: "find_symbol"},
			{capability: "inspect_symbol", want: "inspect_symbol"},
			{capability: string(ReadOnlyCapabilityDefinition), want: "find_definition"},
			{capability: string(ReadOnlyCapabilityReferences), want: "find_references"},
			{capability: string(ReadOnlyCapabilityCallsFrom), want: "calls_from"},
			{capability: string(ReadOnlyCapabilityCallsInto), want: "calls_into"},
			{capability: string(ReadOnlyCapabilityRelatedFiles), want: "related_files"},
		}
		for _, tc := range cases {
			if got := ReadOnlyCapabilityEvidenceCapability(tc.capability); got != tc.want {
				ginkgo.Fail(fmt.Sprintf("ReadOnlyCapabilityEvidenceCapability(%q) = %q, want %q", tc.capability, got, tc.want))
			}

			if got := auditObservedCapabilityForToolCapability(ToolNameReadOnlyCapability, tc.capability); got != tc.want {
				ginkgo.Fail(fmt.Sprintf("auditObservedCapabilityForToolCapability(%q) = %q, want %q", tc.capability, got, tc.want))
			}
		}

		for _, capability := range []string{"source-sanity", "source-diagnostics", "verify-edit"} {
			if got := auditObservedCapabilityForToolCapability(ToolNameReadOnlyCapability, capability); got != "" {
				ginkgo.Fail(fmt.Sprintf("read_only_capability audit normalization for deleted facade %q = %q, want empty", capability, got))
			}
		}

		if got := auditObservedCapabilityForToolCapability(ToolNameSourceSanity, "source_diagnostics"); got != EvidenceCapabilitySourceDiagnostics {
			ginkgo.Fail(fmt.Sprintf("direct source_sanity audit normalization = %q, want %q", got, EvidenceCapabilitySourceDiagnostics))
		}
	})
	ginkgo.It("unsafe read only capability mappings name schema truth violations", func() {
		registry := ReadOnlyCapabilityRegistry{
			Capabilities: []ReadOnlyCapabilitySpec{
				readOnlyCapability(
					ReadOnlyCapabilityID("source-sanity"),
					"fixture",
					[]ReadOnlyCapabilityAdapterSpec{readOnlyAdapter("source_sanity", "", "", "fixture")},
				),
			},
		}

		unsafe := unsafeReadOnlyCapabilityMappingsForTools(registry, nil)
		gomega.Expect(unsafe).To(gomega.HaveLen(1), fmt.Sprintf("unsafe mapping count = %d, got %#v", len(unsafe), unsafe))

		got := unsafe[0]
		gomega.Expect(got.Capability).To(gomega.Equal("source-sanity"), fmt.Sprintf("unsafe mapping should identify capability and tool, got %#v", got))
		gomega.Expect(got.Tool).To(gomega.Equal("source_sanity"), fmt.Sprintf("unsafe mapping should identify capability and tool, got %#v", got))
		gomega.Expect(got.Reason).To(gomega.Equal("schema_side_effects_not_read_only_only"), fmt.Sprintf("unsafe reason = %q", got.Reason))
		gomega.Expect(slices.Contains(got.SchemaSideEffects, autonomy.SideEffectUnknown)).To(gomega.BeTrue(), fmt.Sprintf("unsafe mapping should carry schema truth, got %#v", got.SchemaSideEffects))

	})
})

func capabilityAdapterTools(adapters []ReadOnlyCapabilityAdapterSpec) []string {
	out := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		out = append(out, adapter.Tool)
	}

	return out
}

func jsonTaggedFieldCount(t reflect.Type) int {
	count := 0

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}

		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}

		count++
	}

	return count
}
