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
	"testing"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/config"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

func TestReadOnlyCapabilityRegistryMapsStableVocabularyToAdapters(t *testing.T) {
	registry := BuildReadOnlyCapabilityRegistry()
	if registry.SchemaVersion != readOnlyCapabilitySchemaVersion {
		t.Fatalf("schema version = %q", registry.SchemaVersion)
	}

	if !registry.ResolverRuntimeEnabled {
		t.Fatal("P3.2 resolver should be enabled by default")
	}

	if !registry.PublicStatusEnabled {
		t.Fatal("public capability status should be enabled by default")
	}

	if !registry.DirectToolFallback {
		t.Fatal("direct tool names must remain available as fallback")
	}

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
		if !capability.ReadOnly {
			t.Fatalf("%s should be read-only", capability.ID)
		}

		if !slices.Equal(capability.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
			t.Fatalf("%s side effects = %#v", capability.ID, capability.SideEffects)
		}

		if len(capability.Adapters) == 0 {
			t.Fatalf("%s has no adapter mapping", capability.ID)
		}

		for _, adapter := range capability.Adapters {
			if adapter.Tool == "" {
				t.Fatalf("%s has empty adapter: %#v", capability.ID, adapter)
			}

			if IsWorkspaceWriteTool(adapter.Tool) {
				t.Fatalf("%s maps to write adapter %s", capability.ID, adapter.Tool)
			}

			if !slices.Equal(adapter.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
				t.Fatalf("%s adapter %s side effects = %#v", capability.ID, adapter.Tool, adapter.SideEffects)
			}
		}
	}

	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("capability IDs = %#v, want %#v", gotIDs, wantIDs)
	}

	definition, ok := ResolveReadOnlyCapability("definition", nil)
	if !ok {
		t.Fatal("definition capability should resolve")
	}

	if !slices.ContainsFunc(definition.Capability.Adapters, func(adapter ReadOnlyCapabilityAdapterSpec) bool {
		return adapter.Tool == "code_navigate" && adapter.Capability == string(semanticlanguage.CapDefinition)
	}) {

		t.Fatalf("definition should map to code_navigate definition route: %#v", definition.Capability.Adapters)
	}

	if !slices.ContainsFunc(definition.Capability.Adapters, func(adapter ReadOnlyCapabilityAdapterSpec) bool {
		return adapter.Tool == "go_definition" && adapter.Language == "go"
	}) {

		t.Fatalf("definition should include direct Go adapter fallback: %#v", definition.Capability.Adapters)
	}

	if _, ok := ResolveReadOnlyCapability("source-sanity", nil); ok {
		t.Fatal("source-sanity must remain deferred from the initial read-only capability vocabulary")
	}
}

func TestReadOnlyCapabilityResolverDoesNotFacadeWriteCapabilities(t *testing.T) {
	for _, capability := range []string{
		"edit",
		"write",
		"apply-patch",
		"submit-plan",
		"delegate-edit",
	} {
		if resolution, ok := ResolveReadOnlyCapability(capability, nil); ok {
			t.Fatalf("write capability %q must not resolve through read-only facade: %#v", capability, resolution)
		}
		if deferred, ok := deferredReadOnlyCapabilityAdapter(capability); ok {
			t.Fatalf("write capability %q must not be deferred as read-only evidence: %#v", capability, deferred)
		}
	}

	for _, tool := range []string{
		ToolNameEditFile,
		ToolNameWriteFile,
		ToolNameApplyPatch,
		ToolNameSubmitPlan,
		ToolNameDelegateEdit,
	} {
		if capabilities := EvidenceCapabilitiesForTool(tool); len(capabilities) != 0 {
			t.Fatalf("write tool %s should not expose read-only evidence capabilities: %#v", tool, capabilities)
		}
		if got := CapabilityClassificationForTool(tool); got != CapabilityClassWriteSideEffect {
			t.Fatalf("write tool %s classification = %q, want write side effect", tool, got)
		}
	}
}

func TestReadOnlyCapabilityRegistryPromotesStableSubsetOfCodeNavigate(t *testing.T) {
	if implementations, ok := ResolveReadOnlyCapability(string(semanticlanguage.CapImplementations), nil); !ok ||
		implementations.Capability.ID != string(ReadOnlyCapabilityImplementations) {

		t.Fatalf("implementations should be promoted as a stable product capability, got ok=%v resolution=%#v", ok, implementations)
	}

	for _, capability := range []string{
		string(semanticlanguage.CapExtractBody),
	} {
		if _, ok := ResolveReadOnlyCapability(capability, nil); ok {
			t.Fatalf("%s should remain outside read_only_capability until promoted as a stable product capability", capability)
		}
	}

	codeNavigateCapabilities := CodeNavigationEvidenceCapabilities()
	if !slices.Contains(codeNavigateCapabilities, "find_implementations") {
		t.Fatalf("code_navigate should expose implementation lookup evidence capability: %#v", codeNavigateCapabilities)
	}

	if !slices.Contains(codeNavigateCapabilities, "inspect_symbol") {
		t.Fatalf("code_navigate should expose full-body/source-body evidence capability: %#v", codeNavigateCapabilities)
	}

	if slices.Equal(ReadOnlyCapabilityEvidenceCapabilities(), codeNavigateCapabilities) {
		t.Fatalf("read_only_capability projection should remain a smaller stable product vocabulary, got same as code_navigate: %#v", codeNavigateCapabilities)
	}
}

func TestReadOnlyCapabilityEvidenceCapabilitiesProjectResolverVocabulary(t *testing.T) {
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
		"source_sanity",
		"trace_runtime_failure",
		"verify_edit",
		"audit_behavior",
	}
	got := ReadOnlyCapabilityEvidenceCapabilities()

	if !slices.Equal(got, want) {
		t.Fatalf("read-only evidence capability projection = %#v, want %#v", got, want)
	}

	got[0] = "mutated"

	if next := ReadOnlyCapabilityEvidenceCapabilities(); !slices.Equal(next, want) {
		t.Fatalf("projection should return an isolated slice, got %#v", next)
	}

	if got := EvidenceCapabilitiesForTool(ToolNameReadOnlyCapability); !slices.Equal(got, want) {
		t.Fatalf("%s evidence capabilities = %#v, want resolver projection %#v", ToolNameReadOnlyCapability, got, want)
	}

	codeNavigateWant := CodeNavigationEvidenceCapabilities()
	if got := EvidenceCapabilitiesForTool("code_navigate"); !slices.Equal(got, codeNavigateWant) {
		t.Fatalf("code_navigate evidence capabilities = %#v, want code navigation projection %#v", got, codeNavigateWant)
	}

	if !slices.Contains(codeNavigateWant, "find_implementations") {
		t.Fatalf("code_navigate projection should include runtime-only implementation lookup capability, got %#v", codeNavigateWant)
	}
}

func TestReadOnlyCapabilityResolverCanBeDisabled(t *testing.T) {
	t.Setenv(readOnlyCapabilityResolverEnv, "0")

	registry := BuildReadOnlyCapabilityRegistry()
	if registry.ResolverRuntimeEnabled {
		t.Fatal("resolver should honor disable env")
	}

	root := t.TempDir()
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

	if result.Error == "" {
		t.Fatalf("disabled resolver should return a structured fallback error, got %#v", result)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability fallback payload: %v\n%s", err, result.Output)
	}

	if payload.FallbackReason != "resolver_runtime_disabled" || payload.Routed {
		t.Fatalf("disabled fallback payload = %#v", payload)
	}

	if !payload.DirectToolFallback {
		t.Fatalf("direct tool fallback should remain visible: %#v", payload)
	}

	if payload.Status.State != "fallback" ||
		payload.Status.Fallback.State != "disabled" ||
		payload.Status.Fallback.Reason != "resolver_runtime_disabled" ||
		payload.Status.SelectedAdapter != nil ||
		len(payload.Status.UnresolvedGaps) == 0 {

		t.Fatalf("disabled status should be machine-readable, got %#v", payload.Status)
	}
}

func TestReadOnlyCapabilityRouteCanBeDisabledIndividually(t *testing.T) {
	t.Setenv(readOnlyCapabilityDisabledRoutesEnv, "definition, related_files")

	registry := BuildReadOnlyCapabilityRegistry()
	if !registry.ResolverRuntimeEnabled {
		t.Fatal("per-route disable should not disable the resolver globally")
	}

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

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
	if result.Error == "" {
		t.Fatalf("disabled route should return a structured fallback error, got %#v", result)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode route-disabled payload: %v\n%s", err, result.Output)
	}

	if payload.FallbackReason != "capability_route_disabled" ||
		payload.Routed ||
		!payload.DirectToolFallback ||
		payload.Status.Fallback.State != "disabled" ||
		payload.Status.Fallback.Reason != "capability_route_disabled" {

		t.Fatalf("route-disabled fallback payload = %#v", payload)
	}

	coverage := ReadOnlyCapabilityCoverageForTools(nil)
	if coverage.DisabledCapabilityCount != 2 ||
		!slices.Contains(coverage.DisabledCapabilities, string(ReadOnlyCapabilityDefinition)) ||
		!slices.Contains(coverage.DisabledCapabilities, string(ReadOnlyCapabilityRelatedFiles)) {

		t.Fatalf("coverage should report individually disabled routes, got %#v", coverage)
	}

	if slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityDefinition)) {
		t.Fatalf("disabled definition route should not be counted as routed: %#v", coverage.RoutedCapabilities)
	}

	if coverage.RouteStateCounts["disabled"] != 2 || coverage.RouteStateCounts["deferred"] == 0 {
		t.Fatalf("route state counts should include disabled and deferred states, got %#v", coverage.RouteStateCounts)
	}
}

func TestReadOnlyCapabilityPublicStatusCanBeSuppressedForRoutedResults(t *testing.T) {
	t.Setenv(readOnlyCapabilityPublicStatusEnv, "0")

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

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
	if result.Error != "" {
		t.Fatalf("read_only_capability returned error: %v\n%s", result.Error, result.Output)
	}

	var raw map[string]any

	if err := json.Unmarshal([]byte(result.Output), &raw); err != nil {
		t.Fatalf("decode capability payload: %v\n%s", err, result.Output)
	}

	if _, ok := raw["status"]; ok {
		t.Fatalf("public status should be suppressed, got %s", result.Output)
	}

	if raw["schema_version"] != readOnlyCapabilityResultVersion || raw["routed"] != true || raw["routed_tool"] != "code_navigate" {
		t.Fatalf("suppressed payload should remain structured, got %#v", raw)
	}

	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["capability_status_state"] != "routed" ||
		fields["capability_fallback_state"] != "none" ||
		fields["capability_selected_adapter"] != "code_navigate" ||
		fields["capability_precision_tier"] == "" {

		t.Fatalf("suppressed status telemetry fields = %#v", fields)
	}
}

func TestReadOnlyCapabilityPublicStatusCanBeSuppressedForDisabledFallback(t *testing.T) {
	t.Setenv(readOnlyCapabilityResolverEnv, "0")
	t.Setenv(readOnlyCapabilityPublicStatusEnv, "0")

	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool:       ToolNameReadOnlyCapability,
		Capability: string(ReadOnlyCapabilityDefinition),
		Path:       ".",
		Pattern:    "Run",
	})
	if result.Error == "" {
		t.Fatalf("disabled resolver should return a structured fallback error, got %#v", result)
	}

	var raw map[string]any

	if err := json.Unmarshal([]byte(result.Output), &raw); err != nil {
		t.Fatalf("decode capability fallback payload: %v\n%s", err, result.Output)
	}

	if _, ok := raw["status"]; ok {
		t.Fatalf("public status should be suppressed for disabled fallback, got %s", result.Output)
	}

	if raw["fallback_reason"] != "resolver_runtime_disabled" ||
		raw["resolver_runtime_enabled"] != false ||
		raw["direct_tool_fallback"] != true ||
		raw["routed"] != false {

		t.Fatalf("suppressed disabled fallback should remain structured, got %#v", raw)
	}

	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["capability_status_state"] != "fallback" ||
		fields["capability_fallback_state"] != "disabled" ||
		fields["capability_fallback_reason"] != "resolver_runtime_disabled" ||
		fields["capability_fallback"] != true {

		t.Fatalf("suppressed disabled fallback telemetry fields = %#v", fields)
	}
}

func TestReadOnlyCapabilityAdaptersMatchSchemaSideEffectTruth(t *testing.T) {
	export, err := BuildToolSchemaExport(nil)
	if err != nil {
		t.Fatalf("BuildToolSchemaExport returned error: %v", err)
	}

	exported := make(map[string]ExportedToolSchema, len(export.Tools))
	for _, tool := range export.Tools {
		exported[tool.Name] = tool
	}

	registry := BuildReadOnlyCapabilityRegistry()
	for _, capability := range registry.Capabilities {
		for _, adapter := range capability.Adapters {
			schemaTool, ok := exported[adapter.Tool]
			if !ok {
				t.Fatalf("%s adapter %s is not present in schema export", capability.ID, adapter.Tool)
			}

			if !slices.Equal(adapter.SideEffects, schemaTool.Policy.SideEffects) {
				t.Fatalf("%s adapter %s declared side effects %#v, schema truth %#v", capability.ID, adapter.Tool, adapter.SideEffects, schemaTool.Policy.SideEffects)
			}

			if !slices.Equal(schemaTool.Policy.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}) {
				t.Fatalf("%s adapter %s schema side effects are not read-only-only: %#v", capability.ID, adapter.Tool, schemaTool.Policy.SideEffects)
			}
		}
	}

	sourceSanity, ok := exported["source_sanity"]
	if !ok {
		t.Fatal("source_sanity should remain present as a direct tool schema")
	}

	if !slices.Contains(sourceSanity.Policy.SideEffects, autonomy.SideEffectUnknown) {
		t.Fatalf("source_sanity schema truth should expose unknown side effects while it remains deferred: %#v", sourceSanity.Policy.SideEffects)
	}
}

func TestResolveReadOnlyCapabilityFiltersAvailableAdaptersAndAliases(t *testing.T) {
	resolution, ok := ResolveReadOnlyCapability("reference", []string{"code_navigate", "go_references"})
	if !ok {
		t.Fatal("reference alias should resolve")
	}

	if resolution.Capability.ID != string(ReadOnlyCapabilityReferences) {
		t.Fatalf("capability ID = %q", resolution.Capability.ID)
	}

	gotTools := capabilityAdapterTools(resolution.AvailableAdapters)
	if !slices.Equal(gotTools, []string{"code_navigate", "go_references"}) {
		t.Fatalf("available adapters = %#v", gotTools)
	}

	if len(resolution.UnavailableAdapters) == 0 {
		t.Fatal("expected unavailable adapters when filtering to a narrow tool set")
	}

	outline, ok := ResolveReadOnlyCapability("file_outline", []string{"code_navigate"})
	if !ok {
		t.Fatal("underscore alias should resolve")
	}

	if outline.Capability.ID != string(ReadOnlyCapabilityFileOutline) {
		t.Fatalf("outline capability ID = %q", outline.Capability.ID)
	}

	if got := capabilityAdapterTools(outline.AvailableAdapters); !slices.Equal(got, []string{"code_navigate"}) {
		t.Fatalf("outline available adapters = %#v", got)
	}

	inspect, ok := ResolveReadOnlyCapability("inspect_symbol", []string{"code_navigate", "go_read_symbol"})
	if !ok {
		t.Fatal("design vocabulary alias should resolve")
	}

	if inspect.Capability.ID != string(ReadOnlyCapabilityInspectSymbol) {
		t.Fatalf("inspect capability ID = %q", inspect.Capability.ID)
	}

	if got := capabilityAdapterTools(inspect.AvailableAdapters); !slices.Equal(got, []string{"code_navigate", "go_read_symbol"}) {
		t.Fatalf("inspect-symbol available adapters = %#v", got)
	}
}

func TestReadOnlyCapabilityRuntimeResolutionDoesNotUseWorkspaceManifestDetection(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "notes.txt"), "Run is documented here.\n")
	writeTestFile(t, filepath.Join(root, "src", "main.cpp"), "void run() {}\n")
	writeTestFile(t, filepath.Join(root, "scripts", "tool.py"), "def run():\n    return None\n")
	writeTestFile(t, filepath.Join(root, "web", "main.ts"), "export function run() {}\n")
	writeTestFile(t, filepath.Join(root, "crates", "lib.rs"), "fn run() {}\n")
	writeTestFile(t, filepath.Join(root, "java", "App.java"), "class App { void run() {} }\n")

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

	if result.Error != "" {
		t.Fatalf("read_only_capability returned error: %v\n%s", result.Error, result.Output)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability payload: %v\n%s", err, result.Output)
	}

	if !payload.Routed || payload.RoutedTool != "code_navigate" {
		t.Fatalf("expected bounded code_navigate route, got %#v", payload)
	}

	if got := capabilityAdapterTools(payload.AvailableAdapters); !slices.Equal(got, []string{"code_navigate"}) {
		t.Fatalf("path-specific runtime resolver should not expose workspace-detected language adapters, got %#v", got)
	}

	pyRequest := ToolRequest{Path: filepath.Join("scripts", "tool.py")}
	pyResolution, ok := ResolveReadOnlyCapability("definition", readOnlyCapabilityResolverTools(pyRequest))
	if !ok {
		t.Fatal("python path should resolve definition capability")
	}

	pyTools := capabilityAdapterTools(pyResolution.AvailableAdapters)
	if !slices.Equal(pyTools, []string{"code_navigate", "python_definition"}) {
		t.Fatalf("python path should keep only code_navigate plus direct python fallback, got %#v", pyTools)
	}
}

func TestReadOnlyCapabilityPathLanguageBeatsConflictingOverride(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn \"ok\"\n}\n")

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
	if result.Error != "" {
		t.Fatalf("read_only_capability returned error: %v\n%s", result.Error, result.Output)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability payload: %v\n%s", err, result.Output)
	}

	if payload.UnderlyingResult == nil || !strings.Contains(payload.UnderlyingResult.Output, "routed_tool: go_definition") {
		t.Fatalf("expected path-derived Go definition route, got %#v", payload.UnderlyingResult)
	}

	if got := capabilityAdapterTools(payload.AvailableAdapters); !slices.Equal(got, []string{"code_navigate", "go_definition"}) {
		t.Fatalf("expected path-derived Go adapters only, got %#v", got)
	}

	if payload.ResolvedTarget.Language != "python" || payload.ResolvedTarget.Path != "pkg/runner.go" {
		t.Fatalf("resolved target should preserve request target, got %#v", payload.ResolvedTarget)
	}

	if payload.FreshnessState == "" || payload.TruncationState.State == "" || payload.ResultCounts.OutputLineCount == 0 || len(payload.FollowUpCapabilities) == 0 {
		t.Fatalf("expected result contract metadata, got freshness=%q truncation=%#v counts=%#v followups=%#v", payload.FreshnessState, payload.TruncationState, payload.ResultCounts, payload.FollowUpCapabilities)
	}
}

func TestReadOnlyCapabilityRoutesNamedSliceThroughExistingAdapters(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.com/capability\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string {\n\treturn helper()\n}\n\nfunc helper() string {\n\treturn \"ok\"\n}\n")
	searchBackend, err := resolveContentSearchBackend()
	if err != nil {
		t.Skipf("no supported content search backend installed on this machine: %v", err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("create test tool bin: %v", err)
	}
	searchTool := filepath.Join(bin, filepath.Base(searchBackend.bin))
	writeTestFile(t, searchTool, fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", searchBackend.bin))
	if err := os.Chmod(searchTool, 0o755); err != nil {
		t.Fatalf("chmod test search backend: %v", err)
	}
	t.Setenv("PATH", bin)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		CommandTimeoutSeconds: 30,
		MaxOutputBytes:        65536,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   1024,
		EnableShell:           true,
	})
	mgr.languageTools = languagetools.NewManager(languageToolHost{manager: mgr})

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
		t.Run(tc.name, func(t *testing.T) {
			result := mgr.Execute(context.Background(), tc.req)
			if result.Error != "" {
				t.Fatalf("read_only_capability returned error: %v\n%s", result.Error, result.Output)
			}

			var payload ReadOnlyCapabilityExecutionResult

			if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
				t.Fatalf("decode capability payload: %v\n%s", err, result.Output)
			}

			if !payload.Routed || payload.Capability != tc.wantCapability {
				t.Fatalf("payload routing = %#v", payload)
			}

			if payload.RoutedTool != "code_navigate" || payload.RoutedAdapter == nil || payload.RoutedAdapter.Tool != "code_navigate" {
				t.Fatalf("expected route through code_navigate adapter, got %#v", payload)
			}

			if payload.UnderlyingResult == nil || payload.UnderlyingResult.Tool != "code_navigate" {
				t.Fatalf("expected underlying code_navigate result, got %#v", payload.UnderlyingResult)
			}

			if payload.Status.State != "routed" ||
				payload.Status.SelectedAdapter == nil ||
				payload.Status.SelectedAdapter.Tool != "code_navigate" ||
				payload.Status.SelectedAdapter.Source != "capability_router" ||
				payload.Status.SelectedAdapter.Provenance == "" ||
				payload.Status.PrecisionTier == "" ||
				payload.Status.Confidence == "" ||
				payload.Status.Fallback.State != "none" {

				t.Fatalf("expected successful route status with adapter provenance and precision, got %#v", payload.Status)
			}

			if !strings.Contains(payload.UnderlyingResult.Output, "routed_tool: "+tc.wantAdapter) {
				t.Fatalf("expected underlying adapter %s, got:\n%s", tc.wantAdapter, payload.UnderlyingResult.Output)
			}
		})
	}
}

func TestReadOnlyCapabilityReportsStructuredDeferredSourceSanityFallback(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadOnlyCapability, Capability: "source-sanity", Path: "."})

	if result.Error == "" {
		t.Fatalf("deferred capability should return an error with structured fallback, got %#v", result)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability deferred payload: %v\n%s", err, result.Output)
	}

	if payload.FallbackReason != "capability_deferred" || payload.Routed || payload.Capability != "source-sanity" {
		t.Fatalf("unexpected deferred fallback payload: %#v", payload)
	}

	if payload.UnderlyingResult == nil ||
		payload.UnderlyingResult.Tool != "source_sanity" ||
		!strings.Contains(payload.UnderlyingResult.Error, "schema_side_effects_not_read_only_only") {

		t.Fatalf("deferred fallback should preserve direct source_sanity schema truth, got %#v", payload.UnderlyingResult)
	}

	if payload.Status.State != "fallback" ||
		payload.Status.Fallback.State != "deferred" ||
		payload.Status.Fallback.Reason != "capability_deferred" ||
		len(payload.Status.UnresolvedGaps) == 0 {

		t.Fatalf("deferred status should expose fallback details, got %#v", payload.Status)
	}

	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["capability_fallback_state"] != "deferred" ||
		fields["capability_fallback_reason"] != "capability_deferred" ||
		fields["capability_deferred"] != true {

		t.Fatalf("deferred telemetry fields = %#v", fields)
	}
}

func TestReadOnlyCapabilityReportsStructuredUnmappedFallback(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadOnlyCapability, Capability: "unknown-capability", Path: "."})

	if result.Error == "" {
		t.Fatalf("unmapped capability should return an error with structured fallback, got %#v", result)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability fallback payload: %v\n%s", err, result.Output)
	}

	if payload.FallbackReason != "capability_unmapped" || payload.Routed {
		t.Fatalf("unexpected unmapped fallback payload: %#v", payload)
	}

	if payload.Status.State != "fallback" ||
		payload.Status.Fallback.State != "unmapped" ||
		payload.Status.Fallback.Reason != "capability_unmapped" ||
		len(payload.Status.UnresolvedGaps) == 0 {

		t.Fatalf("unmapped status should expose fallback details, got %#v", payload.Status)
	}

	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["capability_fallback_state"] != "unmapped" ||
		fields["capability_fallback_reason"] != "capability_unmapped" ||
		fields["capability_unmapped"] != true {

		t.Fatalf("unmapped telemetry fields = %#v", fields)
	}
}

func TestReadOnlyCapabilityReportsUnsupportedAbsentConfidence(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "scripts", "player.gd"), "extends Node\n\nfunc _ready():\n\tpass\n")

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
	if result.Error != "" {
		t.Fatalf("unsupported language fallback should report absence without tool error: %v\n%s", result.Error, result.Output)
	}

	var payload ReadOnlyCapabilityExecutionResult

	if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
		t.Fatalf("decode capability payload: %v\n%s", err, result.Output)
	}

	if !payload.Routed || payload.Status.State != "absent" {
		t.Fatalf("expected routed absent status, got %#v", payload)
	}

	if payload.Status.Fallback.State != "unsupported" ||
		payload.Status.Fallback.Reason != "unsupported_language" ||
		!strings.Contains(payload.Status.Fallback.SourceReason, "no registered language route") {

		t.Fatalf("expected unsupported-language fallback status, got %#v", payload.Status.Fallback)
	}

	if payload.Status.AbsenceConfidence == nil ||
		payload.Status.AbsenceConfidence.Level != "low" ||
		payload.Status.AbsenceConfidence.Reason != "generic_fallback_no_matches" ||
		payload.Status.AbsenceConfidence.PrecisionTier != PrecisionTierRGFallback {

		t.Fatalf("expected absence confidence from generic fallback, got %#v", payload.Status.AbsenceConfidence)
	}

	if payload.Status.PrecisionTier != PrecisionTierRGFallback || payload.Status.Confidence != "low" {
		t.Fatalf("expected low-confidence rg fallback status, got %#v", payload.Status)
	}

	statusJSON, err := json.Marshal(payload.Status)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(strings.ToLower(string(statusJSON)), "pass a path") {
		t.Fatalf("status must not prescribe next actions: %s", statusJSON)
	}

	fields := ToolTelemetryFields(result.Tool, result.Output)
	if fields["capability_status_state"] != "absent" ||
		fields["capability_fallback_state"] != "unsupported" ||
		fields["capability_fallback_reason"] != "unsupported_language" ||
		fields["capability_absence_confidence"] != "low" ||
		fields["capability_precision_tier"] != PrecisionTierRGFallback {

		t.Fatalf("absence telemetry fields = %#v", fields)
	}
}

func TestReadOnlyCapabilityCoverageReportsUnmappedAdapters(t *testing.T) {
	coverage := ReadOnlyCapabilityCoverageForTools(nil)
	if coverage.CapabilityCount != len(BuildReadOnlyCapabilityRegistry().Capabilities) {
		t.Fatalf("capability count = %d", coverage.CapabilityCount)
	}

	if coverage.StatusFieldCount != jsonTaggedFieldCount(reflect.TypeOf(ReadOnlyCapabilityStatus{})) {
		t.Fatalf("status field count = %d, want current JSON field count", coverage.StatusFieldCount)
	}

	if !coverage.PublicStatusEnabled {
		t.Fatal("coverage should report public status enabled by default")
	}

	if coverage.MappedAdapterCount == 0 || coverage.ReadOnlyAdapterCount == 0 {
		t.Fatalf("expected non-zero mapped/read-only counts: %#v", coverage)
	}

	if coverage.MappedReadOnlyAdapterCount == 0 || coverage.Coverage <= 0 {
		t.Fatalf("expected measurable positive coverage: %#v", coverage)
	}

	if coverage.StatusFieldCount == 0 ||
		coverage.FallbackStateCount == 0 ||
		!slices.Contains(coverage.FallbackStates, "unmapped") ||
		!slices.Contains(coverage.FallbackStates, "unsupported") {

		t.Fatalf("expected status/fallback state metrics in coverage: %#v", coverage)
	}

	if coverage.RoutedCapabilityCount != 12 ||
		!slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityLocalizeChange)) ||
		!slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityDefinition)) ||
		!slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityRelatedFiles)) ||
		!slices.Contains(coverage.RoutedCapabilities, string(ReadOnlyCapabilityAuditBehavior)) {

		t.Fatalf("expected routed read-only capabilities in coverage: %#v", coverage)
	}

	if coverage.FallbackReasonCount == 0 || !slices.Contains(coverage.FallbackReasons, "capability_unmapped") {
		t.Fatalf("expected fallback reason metrics in coverage: %#v", coverage)
	}

	if !slices.Contains(coverage.FallbackReasons, "capability_deferred") ||
		!slices.Contains(coverage.FallbackStates, "deferred") {

		t.Fatalf("expected deferred fallback metrics in coverage: %#v", coverage)
	}

	if coverage.RouteStateCounts["routed"] != 12 || coverage.RouteStateCounts["deferred"] != 3 || len(coverage.RouteStates) == 0 {
		t.Fatalf("expected routed/deferred route state metrics in coverage: %#v", coverage)
	}

	if coverage.DisabledCapabilityCount != 0 || len(coverage.DisabledCapabilities) != 0 {
		t.Fatalf("unexpected disabled routes in default coverage: %#v", coverage)
	}

	if coverage.UnmappedAdapterCount != len(coverage.UnmappedAdapters) {
		t.Fatalf("unmapped count drift: %#v", coverage)
	}

	if coverage.UnsafeMappedAdapterCount != 0 || len(coverage.UnsafeMappedAdapters) != 0 {
		t.Fatalf("read-only capability registry should have zero unsafe schema mappings: %#v", coverage.UnsafeMappedAdapters)
	}

	if coverage.DeferredAdapterCount != len(coverage.DeferredAdapters) {
		t.Fatalf("deferred count drift: %#v", coverage)
	}

	if !slices.ContainsFunc(coverage.DeferredAdapters, func(adapter ReadOnlyCapabilityDeferredAdapterSpec) bool {
		return adapter.Capability == "source-sanity" &&
			adapter.Tool == "source_sanity" &&
			slices.Contains(adapter.SchemaSideEffects, autonomy.SideEffectUnknown)
	}) {

		t.Fatalf("source_sanity should be named as deferred mixed/unknown adapter: %#v", coverage.DeferredAdapters)
	}

	if slices.Contains(coverage.UnmappedAdapters, "context_pack") {
		t.Fatalf("context_pack should be covered by localize-change in Phase 5: %#v", coverage.UnmappedAdapters)
	}

	if slices.Contains(coverage.UnmappedAdapters, "go_definition") {
		t.Fatalf("go_definition should be covered by the definition capability: %#v", coverage.UnmappedAdapters)
	}
}

func TestReadOnlyCapabilityCoverageKeepsStatusMetricsWhenPublicStatusSuppressed(t *testing.T) {
	t.Setenv(readOnlyCapabilityPublicStatusEnv, "0")

	coverage := ReadOnlyCapabilityCoverageForTools(nil)
	if coverage.PublicStatusEnabled {
		t.Fatal("coverage should report public status suppression")
	}

	if coverage.StatusFieldCount != jsonTaggedFieldCount(reflect.TypeOf(ReadOnlyCapabilityStatus{})) {
		t.Fatalf("status field count = %d, want current JSON field count", coverage.StatusFieldCount)
	}

	if coverage.FallbackStateCount == 0 || !slices.Contains(coverage.FallbackStates, "disabled") {
		t.Fatalf("suppressed public status must retain fallback state metrics: %#v", coverage)
	}

	if coverage.FallbackReasonCount == 0 || !slices.Contains(coverage.FallbackReasons, "resolver_runtime_disabled") {
		t.Fatalf("suppressed public status must retain fallback reason metrics: %#v", coverage)
	}

	if coverage.RoutedCapabilityCount == 0 {
		t.Fatalf("public status suppression should not disable routing metrics: %#v", coverage)
	}
}

func TestHarnessAuditReportsCapabilityRegistryMetrics(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "harness_audit", Purpose: "tool_surface"})
	if result.Error != "" {
		t.Fatalf("harness_audit returned error: %v\n%s", result.Error, result.Output)
	}

	if !strings.Contains(result.Summary, "capability_coverage=") {
		t.Fatalf("summary should include capability coverage metric, got %q", result.Summary)
	}

	var report harnessAuditReport

	if err := json.Unmarshal([]byte(result.Output), &report); err != nil {
		t.Fatalf("decode audit report: %v\n%s", err, result.Output)
	}

	if report.ToolSurface == nil || report.ToolSurface.CapabilityRegistry == nil {
		t.Fatalf("expected capability registry metrics, got %#v", report.ToolSurface)
	}

	if report.ToolSurface.CapabilityToolCounts["find_definition"] == 0 ||
		report.ToolSurface.CapabilityToolCounts["audit_behavior"] == 0 ||
		report.ToolSurface.CapabilityClassCounts[CapabilityClassCompatibilityAdapter] == 0 ||
		report.ToolSurface.CapabilityClassCounts[CapabilityClassDeferred] == 0 {

		t.Fatalf("expected capability grouping metrics, got tool_counts=%#v class_counts=%#v", report.ToolSurface.CapabilityToolCounts, report.ToolSurface.CapabilityClassCounts)
	}

	got := report.ToolSurface.CapabilityRegistry
	want := ReadOnlyCapabilityCoverageForTools(toolManifestToolsAcrossBuiltInSurfaces())

	if got.CapabilityCount != want.CapabilityCount ||
		got.StatusFieldCount != want.StatusFieldCount ||
		got.PublicStatusEnabled != want.PublicStatusEnabled ||
		got.FallbackStateCount != want.FallbackStateCount ||
		got.RoutedCapabilityCount != want.RoutedCapabilityCount ||
		got.DisabledCapabilityCount != want.DisabledCapabilityCount ||
		got.FallbackReasonCount != want.FallbackReasonCount ||
		got.MappedReadOnlyAdapterCount != want.MappedReadOnlyAdapterCount ||
		got.ReadOnlyAdapterCount != want.ReadOnlyAdapterCount ||
		got.UnmappedAdapterCount != want.UnmappedAdapterCount {

		t.Fatalf("capability registry metrics = %#v, want %#v", got, want)
	}

	if !got.ResolverRuntimeEnabled {
		t.Fatal("harness audit should report enabled runtime resolver for P3.2")
	}

	if got.RoutedCapabilityCount < 3 || !slices.Contains(got.RoutedCapabilities, string(ReadOnlyCapabilityFileOutline)) {
		t.Fatalf("harness audit should report routed capabilities, got %#v", got)
	}

	if got.FallbackReasonCount == 0 || !slices.Contains(got.FallbackReasons, "resolver_runtime_disabled") {
		t.Fatalf("harness audit should report fallback reason metrics, got %#v", got)
	}

	if got.FallbackStateCount == 0 || !slices.Contains(got.FallbackStates, "disabled") {
		t.Fatalf("harness audit should report fallback state metrics, got %#v", got)
	}

	if got.RouteStateCounts["routed"] != 12 || got.RouteStateCounts["deferred"] != 3 {
		t.Fatalf("harness audit should report routed/deferred route states, got %#v", got.RouteStateCounts)
	}

	if got.UnsafeMappedAdapterCount != 0 || len(got.UnsafeMappedAdapters) != 0 {
		t.Fatalf("harness audit should report zero unsafe read-only mappings, got %#v", got.UnsafeMappedAdapters)
	}

	allFindings := append(append([]harnessAuditFinding(nil), report.Findings...), report.HistoricalFindings...)
	if got.UnmappedAdapterCount > 0 && !slices.ContainsFunc(allFindings, func(finding harnessAuditFinding) bool {
		return finding.ID == "unmapped_capability_adapter" &&
			finding.Severity == "info" &&
			len(finding.Tools) == got.UnmappedAdapterCount
	}) {
		t.Fatalf("harness audit should surface unmapped adapters as an informational finding, findings=%#v historical=%#v unmapped=%#v", report.Findings, report.HistoricalFindings, got.UnmappedAdapters)
	}

	if !slices.ContainsFunc(got.DeferredAdapters, func(adapter ReadOnlyCapabilityDeferredAdapterSpec) bool {
		return adapter.Capability == "source-sanity" && adapter.Tool == "source_sanity"
	}) {

		t.Fatalf("harness audit should name deferred source_sanity adapter, got %#v", got.DeferredAdapters)
	}
}

func TestToolCapabilityMetadataUsesOneStaticSource(t *testing.T) {
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
			wantCapabilities:    []string{"localize_change", "query_context", "inspect_file", "inspect_symbol", "find_symbol", "find_definition", "find_references", "find_implementations", "calls_from", "calls_into", "related_files", "source_sanity", "trace_runtime_failure", "verify_edit", "audit_behavior"},
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
			wantCapabilities:   []string{"find_references", "inspect_symbol"},
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
			if len(gotCapabilities) != 0 {
				t.Fatalf("%s capabilities = %#v, want none", tc.tool, gotCapabilities)
			}
		} else if !slices.Equal(gotCapabilities, tc.wantCapabilities) {
			t.Fatalf("%s capabilities = %#v, want %#v", tc.tool, gotCapabilities, tc.wantCapabilities)
		}

		if got := CapabilityClassificationForTool(tc.tool); got != tc.wantClassification {
			t.Fatalf("%s classification = %q, want %q", tc.tool, got, tc.wantClassification)
		}

		if tc.mutateFirstCall {
			gotCapabilities[0] = "mutated"

			next := EvidenceCapabilitiesForTool(tc.tool)

			if len(next) == 0 || next[0] != tc.wantFirstCapability {
				t.Fatalf("%s capabilities should be returned as a copy, got %#v", tc.tool, next)
			}
		}
	}

	for _, lang := range semanticlanguage.LanguageRegistry() {
		for capability, tool := range lang.Tools {
			if got := EvidenceCapabilitiesForTool(tool); len(got) == 0 {
				t.Fatalf("%s tool %s for %s should expose evidence capabilities", lang.Name, tool, capability)
			}

			if got := CapabilityClassificationForTool(tool); got == "" {
				t.Fatalf("%s tool %s for %s should expose capability classification", lang.Name, tool, capability)
			}
		}
	}

	for _, specs := range supplementalLanguageToolSpecs {
		for _, spec := range specs {
			if got := EvidenceCapabilitiesForTool(spec.Tool); !slices.Equal(got, spec.EvidenceCapabilities) {
				t.Fatalf("supplemental language tool %s capabilities = %#v, want %#v", spec.Tool, got, spec.EvidenceCapabilities)
			}

			if got := CapabilityClassificationForTool(spec.Tool); got == "" {
				t.Fatalf("supplemental language tool %s should expose capability classification", spec.Tool)
			}
		}
	}
}

func TestReadOnlyCapabilityEvidenceCapabilityDrivesAuditNormalization(t *testing.T) {
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
			t.Fatalf("ReadOnlyCapabilityEvidenceCapability(%q) = %q, want %q", tc.capability, got, tc.want)
		}

		if got := auditObservedCapabilityForToolCapability(ToolNameReadOnlyCapability, tc.capability); got != tc.want {
			t.Fatalf("auditObservedCapabilityForToolCapability(%q) = %q, want %q", tc.capability, got, tc.want)
		}
	}
}

func TestUnsafeReadOnlyCapabilityMappingsNameSchemaTruthViolations(t *testing.T) {
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
	if len(unsafe) != 1 {
		t.Fatalf("unsafe mapping count = %d, got %#v", len(unsafe), unsafe)
	}

	got := unsafe[0]
	if got.Capability != "source-sanity" || got.Tool != "source_sanity" {
		t.Fatalf("unsafe mapping should identify capability and tool, got %#v", got)
	}

	if got.Reason != "schema_side_effects_not_read_only_only" {
		t.Fatalf("unsafe reason = %q", got.Reason)
	}

	if !slices.Contains(got.SchemaSideEffects, autonomy.SideEffectUnknown) {
		t.Fatalf("unsafe mapping should carry schema truth, got %#v", got.SchemaSideEffects)
	}
}

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
