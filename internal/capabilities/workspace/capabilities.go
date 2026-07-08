package workspace

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
	semanticlanguage "idiom/internal/repository/semantic/language"
	"idiom/internal/repository/semantic/languagetools"
)

const (
	readOnlyCapabilitySchemaVersion     = "idiom.read_only_capabilities.v1"
	readOnlyCapabilityResultVersion     = "idiom.read_only_capability_result.v1"
	readOnlyCapabilityResolverEnv       = "IDIOM_READ_ONLY_CAPABILITY_RESOLVER"
	readOnlyCapabilityDisabledRoutesEnv = "IDIOM_READ_ONLY_CAPABILITY_DISABLED_ROUTES"
	readOnlyCapabilityPublicStatusEnv   = "IDIOM_READ_ONLY_CAPABILITY_PUBLIC_STATUS"
)

const (
	CapabilityClassRouted               = "routed"
	CapabilityClassCompatibilityAdapter = "compatibility_adapter"
	CapabilityClassDeferred             = "deferred"
	CapabilityClassFallback             = "fallback"
	CapabilityClassWriteSideEffect      = "write_side_effect"
)

const (
	EvidenceCapabilityAuditBehavior       = "audit_behavior"
	EvidenceCapabilityCallsFrom           = "calls_from"
	EvidenceCapabilityCallsInto           = "calls_into"
	EvidenceCapabilityFindDefinition      = "find_definition"
	EvidenceCapabilityFindImplementations = "find_implementations"
	EvidenceCapabilityFindReferences      = "find_references"
	EvidenceCapabilityFindSymbol          = "find_symbol"
	EvidenceCapabilityHistoryContext      = "history_context"
	EvidenceCapabilityInspectFile         = "inspect_file"
	EvidenceCapabilityInspectSymbol       = "inspect_symbol"
	EvidenceCapabilityLocalizeChange      = "localize_change"
	EvidenceCapabilityQueryContext        = "query_context"
	EvidenceCapabilityRelatedFiles        = "related_files"
	EvidenceCapabilitySecurityScan        = "security_scan"
	EvidenceCapabilitySourceDiagnostics   = "source_diagnostics"
	EvidenceCapabilitySourceSanity        = "source_sanity"
	EvidenceCapabilityTraceRuntimeFailure = "trace_runtime_failure"
	EvidenceCapabilityVerifyEdit          = "verify_edit"
)

type ReadOnlyCapabilityID string

const (
	ReadOnlyCapabilityLocalizeChange  ReadOnlyCapabilityID = "localize-change"
	ReadOnlyCapabilityQueryContext    ReadOnlyCapabilityID = "query-context"
	ReadOnlyCapabilityFileOutline     ReadOnlyCapabilityID = "file-outline"
	ReadOnlyCapabilityInspectSymbol   ReadOnlyCapabilityID = "inspect-symbol"
	ReadOnlyCapabilitySymbolLookup    ReadOnlyCapabilityID = "symbol-lookup"
	ReadOnlyCapabilityDefinition      ReadOnlyCapabilityID = "definition"
	ReadOnlyCapabilityReferences      ReadOnlyCapabilityID = "references"
	ReadOnlyCapabilityImplementations ReadOnlyCapabilityID = "find-implementations"
	ReadOnlyCapabilityCallsFrom       ReadOnlyCapabilityID = "calls-from"
	ReadOnlyCapabilityCallsInto       ReadOnlyCapabilityID = "calls-into"
	ReadOnlyCapabilityRelatedFiles    ReadOnlyCapabilityID = "related-files"
	ReadOnlyCapabilityAuditBehavior   ReadOnlyCapabilityID = "audit-behavior"
	ReadOnlyCapabilitySourceSanity    ReadOnlyCapabilityID = "source-sanity"
	ReadOnlyCapabilityTraceRuntime    ReadOnlyCapabilityID = "trace-runtime-failure"
	ReadOnlyCapabilityVerifyEdit      ReadOnlyCapabilityID = "verify-edit"
)

type ReadOnlyCapabilityRegistry struct {
	SchemaVersion          string                   `json:"schema_version"`
	ResolverRuntimeEnabled bool                     `json:"resolver_runtime_enabled"`
	PublicStatusEnabled    bool                     `json:"public_status_enabled"`
	DirectToolFallback     bool                     `json:"direct_tool_fallback"`
	Capabilities           []ReadOnlyCapabilitySpec `json:"capabilities"`
}

type ReadOnlyCapabilitySpec struct {
	ID          string                          `json:"id"`
	Description string                          `json:"description,omitempty"`
	ReadOnly    bool                            `json:"read_only"`
	SideEffects []autonomy.SideEffectClass      `json:"side_effects"`
	Adapters    []ReadOnlyCapabilityAdapterSpec `json:"adapters"`
}

type ReadOnlyCapabilityAdapterSpec struct {
	Tool        string                     `json:"tool"`
	Capability  string                     `json:"capability,omitempty"`
	Language    string                     `json:"language,omitempty"`
	Source      string                     `json:"source,omitempty"`
	SideEffects []autonomy.SideEffectClass `json:"side_effects"`
}

type ReadOnlyCapabilityUnsafeAdapterSpec struct {
	Capability          string                     `json:"capability"`
	Tool                string                     `json:"tool"`
	Reason              string                     `json:"reason"`
	DeclaredSideEffects []autonomy.SideEffectClass `json:"declared_side_effects,omitempty"`
	SchemaSideEffects   []autonomy.SideEffectClass `json:"schema_side_effects,omitempty"`
}

type ReadOnlyCapabilityDeferredAdapterSpec struct {
	Capability        string                     `json:"capability"`
	Tool              string                     `json:"tool"`
	Reason            string                     `json:"reason"`
	SchemaSideEffects []autonomy.SideEffectClass `json:"schema_side_effects,omitempty"`
}

type ReadOnlyCapabilityRouteState struct {
	Capability   string `json:"capability"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	AdapterCount int    `json:"adapter_count,omitempty"`
}

type ReadOnlyCapabilityResolution struct {
	Capability             ReadOnlyCapabilitySpec          `json:"capability"`
	ResolverRuntimeEnabled bool                            `json:"resolver_runtime_enabled"`
	DirectToolFallback     bool                            `json:"direct_tool_fallback"`
	AvailableAdapters      []ReadOnlyCapabilityAdapterSpec `json:"available_adapters"`
	UnavailableAdapters    []ReadOnlyCapabilityAdapterSpec `json:"unavailable_adapters,omitempty"`
}

type ReadOnlyCapabilityExecutionResult struct {
	SchemaVersion          string                              `json:"schema_version"`
	RequestedCapability    string                              `json:"requested_capability"`
	Capability             string                              `json:"capability,omitempty"`
	ResolvedTarget         ReadOnlyCapabilityResolvedTarget    `json:"resolved_target,omitempty"`
	ResolverRuntimeEnabled bool                                `json:"resolver_runtime_enabled"`
	DirectToolFallback     bool                                `json:"direct_tool_fallback"`
	Routed                 bool                                `json:"routed"`
	RoutedTool             string                              `json:"routed_tool,omitempty"`
	RoutedAdapter          *ReadOnlyCapabilityAdapterSpec      `json:"routed_adapter,omitempty"`
	FallbackReason         string                              `json:"fallback_reason,omitempty"`
	FreshnessState         string                              `json:"freshness_state,omitempty"`
	ResultCounts           ReadOnlyCapabilityResultCounts      `json:"result_counts,omitempty"`
	TruncationState        ReadOnlyCapabilityTruncationState   `json:"truncation_state,omitempty"`
	FollowUpCapabilities   []string                            `json:"follow_up_capabilities,omitempty"`
	Status                 ReadOnlyCapabilityStatus            `json:"status"`
	AvailableAdapters      []ReadOnlyCapabilityAdapterSpec     `json:"available_adapters,omitempty"`
	UnavailableAdapters    []ReadOnlyCapabilityAdapterSpec     `json:"unavailable_adapters,omitempty"`
	UnderlyingResult       *ReadOnlyCapabilityUnderlyingResult `json:"underlying_result,omitempty"`
}

type ReadOnlyCapabilityResolvedTarget struct {
	Path      string `json:"path,omitempty"`
	Handle    string `json:"handle,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	Query     string `json:"query,omitempty"`
	Language  string `json:"language,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

type ReadOnlyCapabilityResultCounts struct {
	RelationshipCount int `json:"relationship_count,omitempty"`
	OutputLineCount   int `json:"output_line_count,omitempty"`
	OutputByteCount   int `json:"output_byte_count,omitempty"`
}

type ReadOnlyCapabilityTruncationState struct {
	State           string `json:"state,omitempty"`
	OutputByteCount int    `json:"output_byte_count,omitempty"`
}

type ReadOnlyCapabilityStatus struct {
	State             string                               `json:"state"`
	SelectedAdapter   *ReadOnlyCapabilitySelectedAdapter   `json:"selected_adapter,omitempty"`
	PrecisionTier     string                               `json:"precision_tier,omitempty"`
	Confidence        string                               `json:"confidence,omitempty"`
	Fallback          ReadOnlyCapabilityFallbackStatus     `json:"fallback"`
	AbsenceConfidence *ReadOnlyCapabilityAbsenceConfidence `json:"absence_confidence,omitempty"`
	UnresolvedGaps    []ReadOnlyCapabilityUnresolvedGap    `json:"unresolved_gaps,omitempty"`
}

type ReadOnlyCapabilitySelectedAdapter struct {
	Tool        string                     `json:"tool"`
	Capability  string                     `json:"capability,omitempty"`
	Language    string                     `json:"language,omitempty"`
	Source      string                     `json:"source,omitempty"`
	Provenance  string                     `json:"provenance,omitempty"`
	SideEffects []autonomy.SideEffectClass `json:"side_effects,omitempty"`
}

type ReadOnlyCapabilityFallbackStatus struct {
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	SourceReason string `json:"source_reason,omitempty"`
}

type ReadOnlyCapabilityAbsenceConfidence struct {
	Level         string `json:"level"`
	Reason        string `json:"reason"`
	PrecisionTier string `json:"precision_tier,omitempty"`
	Source        string `json:"source,omitempty"`
}

type ReadOnlyCapabilityUnresolvedGap struct {
	State        string `json:"state"`
	Reason       string `json:"reason"`
	Tool         string `json:"tool,omitempty"`
	Capability   string `json:"capability,omitempty"`
	Language     string `json:"language,omitempty"`
	AdapterCount int    `json:"adapter_count,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

type ReadOnlyCapabilityUnderlyingResult struct {
	Tool          string             `json:"tool"`
	Summary       string             `json:"summary,omitempty"`
	Output        string             `json:"output,omitempty"`
	Error         string             `json:"error,omitempty"`
	PrecisionTier string             `json:"precision_tier,omitempty"`
	Relationships []ToolRelationship `json:"relationships,omitempty"`
}

type ReadOnlyCapabilityCoverage struct {
	SchemaVersion              string                                  `json:"schema_version"`
	ResolverRuntimeEnabled     bool                                    `json:"resolver_runtime_enabled"`
	PublicStatusEnabled        bool                                    `json:"public_status_enabled"`
	DirectToolFallback         bool                                    `json:"direct_tool_fallback"`
	StatusFieldCount           int                                     `json:"status_field_count"`
	FallbackStateCount         int                                     `json:"fallback_state_count"`
	FallbackStates             []string                                `json:"fallback_states,omitempty"`
	CapabilityCount            int                                     `json:"capability_count"`
	RoutedCapabilityCount      int                                     `json:"routed_capability_count"`
	RoutedCapabilities         []string                                `json:"routed_capabilities,omitempty"`
	FallbackReasonCount        int                                     `json:"fallback_reason_count"`
	FallbackReasons            []string                                `json:"fallback_reasons,omitempty"`
	RouteStateCounts           map[string]int                          `json:"route_state_counts,omitempty"`
	RouteStates                []ReadOnlyCapabilityRouteState          `json:"route_states,omitempty"`
	DisabledCapabilityCount    int                                     `json:"disabled_capability_count"`
	DisabledCapabilities       []string                                `json:"disabled_capabilities,omitempty"`
	MappedAdapterCount         int                                     `json:"mapped_adapter_count"`
	ReadOnlyAdapterCount       int                                     `json:"read_only_adapter_count"`
	MappedReadOnlyAdapterCount int                                     `json:"mapped_read_only_adapter_count"`
	Coverage                   float64                                 `json:"coverage"`
	UnmappedAdapterCount       int                                     `json:"unmapped_adapter_count"`
	UnmappedAdapters           []string                                `json:"unmapped_adapters,omitempty"`
	UnsafeMappedAdapterCount   int                                     `json:"unsafe_mapped_adapter_count"`
	UnsafeMappedAdapters       []ReadOnlyCapabilityUnsafeAdapterSpec   `json:"unsafe_mapped_adapters,omitempty"`
	DeferredAdapterCount       int                                     `json:"deferred_adapter_count"`
	DeferredAdapters           []ReadOnlyCapabilityDeferredAdapterSpec `json:"deferred_adapters,omitempty"`
}

func ReadOnlyCapabilityEvidenceCapabilities() []string {
	return []string{
		EvidenceCapabilityLocalizeChange,
		EvidenceCapabilityQueryContext,
		EvidenceCapabilityInspectFile,
		EvidenceCapabilityInspectSymbol,
		EvidenceCapabilityFindSymbol,
		EvidenceCapabilityFindDefinition,
		EvidenceCapabilityFindReferences,
		EvidenceCapabilityFindImplementations,
		EvidenceCapabilityCallsFrom,
		EvidenceCapabilityCallsInto,
		EvidenceCapabilityRelatedFiles,
		EvidenceCapabilitySourceSanity,
		EvidenceCapabilityTraceRuntimeFailure,
		EvidenceCapabilityVerifyEdit,
		EvidenceCapabilityAuditBehavior,
	}
}

func CodeNavigationEvidenceCapabilities() []string {
	var out []string

	for _, capability := range languagetools.CodeNavigationCapabilities() {
		if evidence := evidenceCapabilityForLanguageCapability(semanticlanguage.Capability(capability)); evidence != "" {
			out = append(out, evidence)
		}
	}

	return textutil.UniqueNonEmpty(out)
}

func ReadOnlyCapabilityEvidenceCapability(capability string) string {
	switch normalizeReadOnlyCapabilityID(capability) {
	case string(ReadOnlyCapabilityLocalizeChange):
		return EvidenceCapabilityLocalizeChange
	case string(ReadOnlyCapabilityQueryContext):
		return EvidenceCapabilityQueryContext
	case string(ReadOnlyCapabilityFileOutline):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapFileOutline)
	case string(ReadOnlyCapabilityInspectSymbol):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapReadSymbol)
	case string(ReadOnlyCapabilitySymbolLookup):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapFindSymbol)
	case string(ReadOnlyCapabilityDefinition):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapDefinition)
	case string(ReadOnlyCapabilityReferences):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapReferences)
	case string(ReadOnlyCapabilityImplementations):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapImplementations)
	case string(ReadOnlyCapabilityCallsFrom):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapCallsFrom)
	case string(ReadOnlyCapabilityCallsInto):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapCallsInto)
	case string(ReadOnlyCapabilityRelatedFiles):
		return evidenceCapabilityForLanguageCapability(semanticlanguage.CapRelatedFiles)
	case string(ReadOnlyCapabilitySourceSanity):
		return EvidenceCapabilitySourceSanity
	case string(ReadOnlyCapabilityTraceRuntime):
		return EvidenceCapabilityTraceRuntimeFailure
	case string(ReadOnlyCapabilityVerifyEdit):
		return EvidenceCapabilityVerifyEdit
	case string(ReadOnlyCapabilityAuditBehavior):
		return EvidenceCapabilityAuditBehavior
	default:
		return ""
	}
}

func BuildReadOnlyCapabilityRegistry() ReadOnlyCapabilityRegistry {
	return ReadOnlyCapabilityRegistry{
		SchemaVersion:          readOnlyCapabilitySchemaVersion,
		ResolverRuntimeEnabled: readOnlyCapabilityResolverRuntimeEnabled(),
		PublicStatusEnabled:    readOnlyCapabilityPublicStatusEnabled(),
		DirectToolFallback:     true,
		Capabilities: []ReadOnlyCapabilitySpec{
			readOnlyCapability(
				ReadOnlyCapabilityLocalizeChange,
				"Localize candidate files, symbols, and tests for a change without choosing the model's strategy.",
				[]ReadOnlyCapabilityAdapterSpec{
					readOnlyAdapter(ToolNameContextPack, EvidenceCapabilityLocalizeChange, "", "context_graph"),
					readOnlyAdapter(ToolNameContextQuery, EvidenceCapabilityLocalizeChange, "", "context_graph"),
					readOnlyAdapter(ToolNameContextZoom, EvidenceCapabilityLocalizeChange, "", "context_graph"),
					readOnlyAdapter(ToolNameContextStatus, EvidenceCapabilityLocalizeChange, "", "context_graph"),
					readOnlyAdapter(ToolNameContextImpact, EvidenceCapabilityLocalizeChange, "", "context_graph"),
					readOnlyAdapter(ToolNameDiscoveryQuery, EvidenceCapabilityLocalizeChange, "", "discovery_orchestrator"),
				},
			),
			readOnlyCapability(
				ReadOnlyCapabilityQueryContext,
				"Ask a graph-shaped question over indexed context without directly inspecting source.",
				[]ReadOnlyCapabilityAdapterSpec{
					readOnlyAdapter(ToolNameContextQuery, EvidenceCapabilityQueryContext, "", "context_graph"),
					readOnlyAdapter(ToolNameContextZoom, EvidenceCapabilityQueryContext, "", "context_graph"),
					readOnlyAdapter(ToolNameContextStatus, EvidenceCapabilityQueryContext, "", "context_graph"),
				},
			),
			readOnlyCapability(
				ReadOnlyCapabilityFileOutline,
				"Return declaration or structural outline evidence for one source file.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapFileOutline)},
					languageCapabilityAdapters(semanticlanguage.CapFileOutline)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityInspectSymbol,
				"Read a symbol body or exact symbol range after anchoring.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapReadSymbol)},
					languageCapabilityAdapters(semanticlanguage.CapReadSymbol)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilitySymbolLookup,
				"Find candidate symbols by name or pattern without reading or changing source.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{
						codeNavigateCapabilityAdapter(semanticlanguage.CapFindSymbol),
						codeNavigateCapabilityAdapter(semanticlanguage.CapWorkspaceSymbol),
					},
					append(languageCapabilityAdapters(semanticlanguage.CapFindSymbol), languageCapabilityAdapters(semanticlanguage.CapWorkspaceSymbol)...)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityDefinition,
				"Locate definition evidence for a named symbol.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapDefinition)},
					languageCapabilityAdapters(semanticlanguage.CapDefinition)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityReferences,
				"Locate reference or usage evidence for a named symbol.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapReferences)},
					languageCapabilityAdapters(semanticlanguage.CapReferences)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityImplementations,
				"Locate implementation evidence for an interface, trait, method, or abstract symbol.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapImplementations)},
					languageCapabilityAdapters(semanticlanguage.CapImplementations)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityCallsFrom,
				"Locate outgoing call evidence from a named symbol.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapCallsFrom)},
					languageCapabilityAdapters(semanticlanguage.CapCallsFrom)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityCallsInto,
				"Locate incoming call evidence for a named symbol.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapCallsInto)},
					languageCapabilityAdapters(semanticlanguage.CapCallsInto)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityRelatedFiles,
				"Find files related to a symbol, file, or implementation topic.",
				append(
					[]ReadOnlyCapabilityAdapterSpec{codeNavigateCapabilityAdapter(semanticlanguage.CapRelatedFiles)},
					languageCapabilityAdapters(semanticlanguage.CapRelatedFiles)...,
				),
			),
			readOnlyCapability(
				ReadOnlyCapabilityAuditBehavior,
				"Inspect harness behavior, tool surface, backend, and cache status without mutating source.",
				[]ReadOnlyCapabilityAdapterSpec{
					readOnlyAdapter(ToolNameHarnessAudit, EvidenceCapabilityAuditBehavior, "", ToolNameHarnessAudit),
				},
			),
		},
	}
}

func attachToolManifestCapabilityMetadata(entry ToolManifestEntry) ToolManifestEntry {
	entry.EvidenceCapabilities = EvidenceCapabilitiesForTool(entry.Tool)
	entry.CapabilityClassification = CapabilityClassificationForTool(entry.Tool)

	return entry
}

func EvidenceCapabilitiesForTool(tool string) []string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil
	}

	if capabilities := languageToolEvidenceCapabilities(tool); len(capabilities) > 0 {
		return capabilities
	}

	if capabilities := builtInToolSurfaceEvidenceCapabilities(tool); len(capabilities) > 0 {
		return capabilities
	}

	return nil
}

func CapabilityClassificationForTool(tool string) string {
	tool = strings.TrimSpace(tool)
	switch {
	case tool == "":
		return ""
	case IsWorkspaceWriteTool(tool):
		return CapabilityClassWriteSideEffect
	case LanguageToolHasCapability(tool, semanticlanguage.CapDiagnostics):
		return CapabilityClassDeferred
	}

	if metadata, ok := builtInToolSurfaceCatalog.lookup(tool); ok && metadata.CapabilityClassification != "" {
		return metadata.CapabilityClassification
	}

	if len(languageToolEvidenceCapabilities(tool)) > 0 {
		return CapabilityClassCompatibilityAdapter
	}

	return ""
}

func evidenceCapabilityForLanguageCapability(capability semanticlanguage.Capability) string {
	switch capability {
	case semanticlanguage.CapFileOutline:
		return EvidenceCapabilityInspectFile
	case semanticlanguage.CapFindSymbol, semanticlanguage.CapWorkspaceSymbol:
		return EvidenceCapabilityFindSymbol
	case semanticlanguage.CapReadSymbol, semanticlanguage.CapExtractBody:
		return EvidenceCapabilityInspectSymbol
	case semanticlanguage.CapDefinition:
		return EvidenceCapabilityFindDefinition
	case semanticlanguage.CapReferences:
		return EvidenceCapabilityFindReferences
	case semanticlanguage.CapCallsFrom:
		return EvidenceCapabilityCallsFrom
	case semanticlanguage.CapCallsInto:
		return EvidenceCapabilityCallsInto
	case semanticlanguage.CapRelatedFiles:
		return EvidenceCapabilityRelatedFiles
	case semanticlanguage.CapImplementations:
		return EvidenceCapabilityFindImplementations
	case semanticlanguage.CapDiagnostics:
		return EvidenceCapabilitySourceDiagnostics
	case semanticlanguage.CapOverview, semanticlanguage.CapEntrypoints:
		return EvidenceCapabilityLocalizeChange
	default:
		return ""
	}
}

func ResolveReadOnlyCapability(id string, availableTools []string) (ReadOnlyCapabilityResolution, bool) {
	registry := BuildReadOnlyCapabilityRegistry()
	want := normalizeReadOnlyCapabilityID(id)

	for _, capability := range registry.Capabilities {
		if capability.ID != want {
			continue
		}

		available, unavailable := filterCapabilityAdapters(capability.Adapters, availableTools)

		return ReadOnlyCapabilityResolution{
			Capability:             capability,
			ResolverRuntimeEnabled: registry.ResolverRuntimeEnabled,
			DirectToolFallback:     registry.DirectToolFallback,
			AvailableAdapters:      available,
			UnavailableAdapters:    unavailable,
		}, true
	}

	return ReadOnlyCapabilityResolution{}, false
}

func ReadOnlyCapabilityCoverageForTools(tools []string) ReadOnlyCapabilityCoverage {
	if len(tools) == 0 {
		tools = toolManifestToolsAcrossBuiltInSurfaces()
	}

	registry := BuildReadOnlyCapabilityRegistry()
	readOnlyAdapters := readOnlyCapabilityMetricAdapters(tools)
	readOnlySet := stringSet(readOnlyAdapters)
	toolSet := stringSet(tools)
	mappedSet := make(map[string]struct{})

	for _, capability := range registry.Capabilities {
		for _, adapter := range capability.Adapters {
			tool := strings.TrimSpace(adapter.Tool)
			if tool != "" {
				mappedSet[tool] = struct{}{}
			}
		}
	}

	mappedReadOnly := 0

	for tool := range mappedSet {
		if _, ok := readOnlySet[tool]; ok {
			mappedReadOnly++
		}
	}

	unmapped := make([]string, 0)

	for _, tool := range readOnlyAdapters {
		if _, ok := mappedSet[tool]; !ok {
			unmapped = append(unmapped, tool)
		}
	}

	unsafe := unsafeReadOnlyCapabilityMappingsForTools(registry, tools)
	deferred := deferredReadOnlyCapabilityAdaptersForTools(toolSet)
	routeStates := readOnlyCapabilityRouteStates(registry, tools)
	routedCapabilities := readOnlyCapabilityRoutedIDsFromStates(routeStates)
	disabledCapabilities := readOnlyCapabilityDisabledIDsFromStates(routeStates)
	fallbackReasons := readOnlyCapabilityFallbackReasonCodes()
	fallbackStates := readOnlyCapabilityFallbackStates()

	coverage := 0.0
	if len(readOnlyAdapters) > 0 {
		coverage = float64(mappedReadOnly) / float64(len(readOnlyAdapters))
	}

	return ReadOnlyCapabilityCoverage{
		SchemaVersion:              registry.SchemaVersion,
		ResolverRuntimeEnabled:     registry.ResolverRuntimeEnabled,
		PublicStatusEnabled:        registry.PublicStatusEnabled,
		DirectToolFallback:         registry.DirectToolFallback,
		StatusFieldCount:           readOnlyCapabilityStatusFieldCount(),
		FallbackStateCount:         len(fallbackStates),
		FallbackStates:             fallbackStates,
		CapabilityCount:            len(registry.Capabilities),
		RoutedCapabilityCount:      len(routedCapabilities),
		RoutedCapabilities:         routedCapabilities,
		FallbackReasonCount:        len(fallbackReasons),
		FallbackReasons:            fallbackReasons,
		RouteStateCounts:           readOnlyCapabilityRouteStateCounts(routeStates),
		RouteStates:                routeStates,
		DisabledCapabilityCount:    len(disabledCapabilities),
		DisabledCapabilities:       disabledCapabilities,
		MappedAdapterCount:         len(mappedSet),
		ReadOnlyAdapterCount:       len(readOnlyAdapters),
		MappedReadOnlyAdapterCount: mappedReadOnly,
		Coverage:                   coverage,
		UnmappedAdapterCount:       len(unmapped),
		UnmappedAdapters:           unmapped,
		UnsafeMappedAdapterCount:   len(unsafe),
		UnsafeMappedAdapters:       unsafe,
		DeferredAdapterCount:       len(deferred),
		DeferredAdapters:           deferred,
	}
}

func (m *Manager) readOnlyCapability(ctx context.Context, req ToolRequest) ToolResult {
	requested := strings.TrimSpace(textutil.FirstNonEmpty(req.Capability, req.ID))
	target := readOnlyCapabilityResolvedTarget(req)

	if requested == "" {
		return readOnlyCapabilityFallbackResult(requested, target, ReadOnlyCapabilityResolution{}, "invalid_input", "capability is required")
	}

	resolution, ok := resolveReadOnlyCapabilityForRequest(requested, req)
	if !ok {
		if deferred, ok := deferredReadOnlyCapabilityAdapter(requested); ok {
			return readOnlyCapabilityDeferredResult(requested, target, deferred)
		}

		return readOnlyCapabilityFallbackResult(requested, target, ReadOnlyCapabilityResolution{
			ResolverRuntimeEnabled: readOnlyCapabilityResolverRuntimeEnabled(),
			DirectToolFallback:     true,
		}, "capability_unmapped", "")
	}

	if !resolution.ResolverRuntimeEnabled {
		return readOnlyCapabilityFallbackResult(requested, target, resolution, "resolver_runtime_disabled", "")
	}

	if isReadOnlyCapabilityRouteDisabled(resolution.Capability.ID) {
		return readOnlyCapabilityFallbackResult(requested, target, resolution, "capability_route_disabled", "")
	}

	if !isReadOnlyCapabilityRuntimeRouted(resolution.Capability.ID) {
		return readOnlyCapabilityFallbackResult(requested, target, resolution, "capability_not_runtime_routed", "")
	}

	adapter, fallbackReason, ok := selectReadOnlyCapabilityAdapter(resolution.AvailableAdapters)
	if !ok {
		return readOnlyCapabilityFallbackResult(requested, target, resolution, fallbackReason, "")
	}

	routedReq := req

	routedReq.Tool = adapter.Tool
	routedReq.Capability = adapter.Capability

	result := m.Execute(ctx, routedReq)

	return readOnlyCapabilityWrapResult(requested, target, resolution, adapter, result)
}

func resolveReadOnlyCapabilityForRequest(id string, req ToolRequest) (ReadOnlyCapabilityResolution, bool) {
	return ResolveReadOnlyCapability(id, readOnlyCapabilityResolverTools(req))
}

func readOnlyCapabilityResolverTools(req ToolRequest) []string {
	tools := []string{ToolNameCodeNavigate}

	if lang, ok := readOnlyCapabilityRequestLanguage(req); ok {
		for _, tool := range lang.Tools {
			tools = append(tools, tool)
		}
	}

	return textutil.UniqueNonEmpty(tools)
}

func readOnlyCapabilityRequestLanguage(req ToolRequest) (semanticlanguage.LanguageSupport, bool) {
	if path := strings.TrimSpace(req.Path); path != "" {
		if lang, ok := semanticlanguage.LanguageForFile(path); ok {
			return lang, true
		}
	}

	if language := strings.TrimSpace(req.Language); language != "" {
		return semanticlanguage.LanguageForName(language)
	}

	return semanticlanguage.LanguageSupport{}, false
}

func readOnlyCapabilityResolvedTarget(req ToolRequest) ReadOnlyCapabilityResolvedTarget {
	return ReadOnlyCapabilityResolvedTarget{
		Path:      filepath.ToSlash(strings.TrimSpace(req.Path)),
		Handle:    strings.TrimSpace(req.Handle),
		Pattern:   strings.TrimSpace(req.Pattern),
		Query:     strings.TrimSpace(req.Query),
		Language:  strings.TrimSpace(req.Language),
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
	}
}

func readOnlyCapabilityFallbackResult(requested string, target ReadOnlyCapabilityResolvedTarget, resolution ReadOnlyCapabilityResolution, reason string, detail string) ToolResult {
	if reason == "" {
		reason = "no_available_read_only_adapter"
	}

	payload := readOnlyCapabilityExecutionPayload(requested, target, resolution)

	payload.FallbackReason = reason

	if detail != "" {
		payload.UnderlyingResult = &ReadOnlyCapabilityUnderlyingResult{
			Tool:  ToolNameReadOnlyCapability,
			Error: detail,
		}
	}

	readOnlyCapabilityAttachStatus(&payload, nil, nil)

	return ToolResult{
		Tool:    ToolNameReadOnlyCapability,
		Summary: "Read-only capability not routed",
		Output:  marshalReadOnlyCapabilityExecutionResult(payload),
		Error:   strings.TrimSpace(textutil.FirstNonEmpty(detail, reason)),
	}
}

func readOnlyCapabilityDeferredResult(requested string, target ReadOnlyCapabilityResolvedTarget, adapter ReadOnlyCapabilityDeferredAdapterSpec) ToolResult {
	payload := readOnlyCapabilityExecutionPayload(requested, target, ReadOnlyCapabilityResolution{
		Capability: ReadOnlyCapabilitySpec{
			ID:          strings.TrimSpace(adapter.Capability),
			Description: "Deferred from read-only capability execution because the direct tool schema is not read-only-only.",
			ReadOnly:    false,
			SideEffects: append([]autonomy.SideEffectClass(nil), adapter.SchemaSideEffects...),
		},
		ResolverRuntimeEnabled: readOnlyCapabilityResolverRuntimeEnabled(),
		DirectToolFallback:     true,
	})

	payload.FallbackReason = "capability_deferred"
	payload.UnderlyingResult = &ReadOnlyCapabilityUnderlyingResult{
		Tool:    strings.TrimSpace(adapter.Tool),
		Summary: "Capability deferred from read-only routing",
		Error:   strings.TrimSpace(adapter.Reason),
	}
	readOnlyCapabilityAttachStatus(&payload, nil, nil)

	return ToolResult{
		Tool:    ToolNameReadOnlyCapability,
		Summary: "Read-only capability deferred",
		Output:  marshalReadOnlyCapabilityExecutionResult(payload),
		Error:   "capability_deferred",
	}
}

func readOnlyCapabilityWrapResult(requested string, target ReadOnlyCapabilityResolvedTarget, resolution ReadOnlyCapabilityResolution, adapter ReadOnlyCapabilityAdapterSpec, result ToolResult) ToolResult {
	payload := readOnlyCapabilityExecutionPayload(requested, target, resolution)

	payload.Routed = true
	payload.RoutedTool = strings.TrimSpace(adapter.Tool)

	adapterCopy := adapter

	adapterCopy.SideEffects = append([]autonomy.SideEffectClass(nil), adapter.SideEffects...)
	payload.RoutedAdapter = &adapterCopy
	payload.FallbackReason = readOnlyCapabilityUnderlyingFallbackReason(result)
	payload.UnderlyingResult = &ReadOnlyCapabilityUnderlyingResult{
		Tool:          strings.TrimSpace(result.Tool),
		Summary:       strings.TrimSpace(result.Summary),
		Output:        strings.TrimSpace(result.Output),
		Error:         strings.TrimSpace(result.Error),
		PrecisionTier: strings.TrimSpace(result.PrecisionTier),
		Relationships: append([]ToolRelationship(nil), result.Relationships...),
	}
	readOnlyCapabilityAttachStatus(&payload, &adapter, &result)

	summary := "Read-only capability routed " + payload.Capability + " via " + payload.RoutedTool
	if payload.FallbackReason != "" {
		summary += " with fallback"
	}

	return ToolResult{
		Tool:          ToolNameReadOnlyCapability,
		Summary:       summary,
		Output:        marshalReadOnlyCapabilityExecutionResult(payload),
		Error:         strings.TrimSpace(result.Error),
		PrecisionTier: result.PrecisionTier,
		Relationships: append([]ToolRelationship(nil), result.Relationships...),
	}
}

func readOnlyCapabilityExecutionPayload(requested string, target ReadOnlyCapabilityResolvedTarget, resolution ReadOnlyCapabilityResolution) ReadOnlyCapabilityExecutionResult {
	return ReadOnlyCapabilityExecutionResult{
		SchemaVersion:          readOnlyCapabilityResultVersion,
		RequestedCapability:    strings.TrimSpace(requested),
		Capability:             strings.TrimSpace(resolution.Capability.ID),
		ResolvedTarget:         target,
		ResolverRuntimeEnabled: resolution.ResolverRuntimeEnabled,
		DirectToolFallback:     resolution.DirectToolFallback,
		AvailableAdapters:      cloneCapabilityAdapters(resolution.AvailableAdapters),
		UnavailableAdapters:    cloneCapabilityAdapters(resolution.UnavailableAdapters),
		FollowUpCapabilities:   readOnlyCapabilityFollowUps(resolution.Capability.ID),
	}
}

func readOnlyCapabilityFollowUps(current string) []string {
	current = normalizeReadOnlyCapabilityID(current)

	registry := BuildReadOnlyCapabilityRegistry()

	var out []string

	for _, capability := range registry.Capabilities {
		id := strings.TrimSpace(capability.ID)
		if id == "" || id == current || !isReadOnlyCapabilityRuntimeRouted(id) || isReadOnlyCapabilityRouteDisabled(id) {
			continue
		}

		out = append(out, id)
	}

	slices.Sort(out)

	return out
}

func marshalReadOnlyCapabilityExecutionResult(payload ReadOnlyCapabilityExecutionResult) string {
	data, err := json.MarshalIndent(readOnlyCapabilityPublicExecutionResult(payload), "", "  ")
	if err != nil {
		return `{"schema_version":"` + readOnlyCapabilityResultVersion + `","fallback_reason":"marshal_failed"}`
	}

	return string(data)
}

type readOnlyCapabilityPublicResult struct {
	SchemaVersion          string                              `json:"schema_version"`
	RequestedCapability    string                              `json:"requested_capability"`
	Capability             string                              `json:"capability,omitempty"`
	ResolvedTarget         ReadOnlyCapabilityResolvedTarget    `json:"resolved_target,omitempty"`
	ResolverRuntimeEnabled bool                                `json:"resolver_runtime_enabled"`
	DirectToolFallback     bool                                `json:"direct_tool_fallback"`
	Routed                 bool                                `json:"routed"`
	RoutedTool             string                              `json:"routed_tool,omitempty"`
	RoutedAdapter          *ReadOnlyCapabilityAdapterSpec      `json:"routed_adapter,omitempty"`
	FallbackReason         string                              `json:"fallback_reason,omitempty"`
	FreshnessState         string                              `json:"freshness_state,omitempty"`
	ResultCounts           ReadOnlyCapabilityResultCounts      `json:"result_counts,omitempty"`
	TruncationState        ReadOnlyCapabilityTruncationState   `json:"truncation_state,omitempty"`
	FollowUpCapabilities   []string                            `json:"follow_up_capabilities,omitempty"`
	Status                 *ReadOnlyCapabilityStatus           `json:"status,omitempty"`
	AvailableAdapters      []ReadOnlyCapabilityAdapterSpec     `json:"available_adapters,omitempty"`
	UnavailableAdapters    []ReadOnlyCapabilityAdapterSpec     `json:"unavailable_adapters,omitempty"`
	UnderlyingResult       *ReadOnlyCapabilityUnderlyingResult `json:"underlying_result,omitempty"`
}

func readOnlyCapabilityPublicExecutionResult(payload ReadOnlyCapabilityExecutionResult) readOnlyCapabilityPublicResult {
	result := readOnlyCapabilityPublicResult{
		SchemaVersion:          payload.SchemaVersion,
		RequestedCapability:    payload.RequestedCapability,
		Capability:             payload.Capability,
		ResolvedTarget:         payload.ResolvedTarget,
		ResolverRuntimeEnabled: payload.ResolverRuntimeEnabled,
		DirectToolFallback:     payload.DirectToolFallback,
		Routed:                 payload.Routed,
		RoutedTool:             payload.RoutedTool,
		RoutedAdapter:          payload.RoutedAdapter,
		FallbackReason:         payload.FallbackReason,
		FreshnessState:         payload.FreshnessState,
		ResultCounts:           payload.ResultCounts,
		TruncationState:        payload.TruncationState,
		FollowUpCapabilities:   append([]string(nil), payload.FollowUpCapabilities...),
		AvailableAdapters:      payload.AvailableAdapters,
		UnavailableAdapters:    payload.UnavailableAdapters,
		UnderlyingResult:       payload.UnderlyingResult,
	}
	if readOnlyCapabilityPublicStatusEnabled() {
		status := payload.Status

		result.Status = &status
	}

	return result
}

func readOnlyCapabilityAttachStatus(payload *ReadOnlyCapabilityExecutionResult, adapter *ReadOnlyCapabilityAdapterSpec, result *ToolResult) {
	if payload == nil {
		return
	}

	fallback := readOnlyCapabilityFallbackStatusFor(payload.FallbackReason)
	absence := readOnlyCapabilityAbsenceConfidenceFor(result)
	precisionTier := readOnlyCapabilityStatusPrecisionTier(result, payload.UnderlyingResult)

	payload.FreshnessState = readOnlyCapabilityFreshnessState(payload, result)
	payload.ResultCounts = readOnlyCapabilityResultCounts(result, payload.UnderlyingResult)
	payload.TruncationState = readOnlyCapabilityTruncationState(result, payload.UnderlyingResult)
	payload.Status = ReadOnlyCapabilityStatus{
		State:             readOnlyCapabilityStatusState(payload.Routed, fallback, absence),
		SelectedAdapter:   readOnlyCapabilitySelectedAdapterStatus(adapter),
		PrecisionTier:     precisionTier,
		Confidence:        readOnlyCapabilityRouteConfidence(precisionTier, fallback, absence, result),
		Fallback:          fallback,
		AbsenceConfidence: absence,
		UnresolvedGaps:    readOnlyCapabilityStatusGaps(*payload, fallback, absence),
	}
}

func readOnlyCapabilitySelectedAdapterStatus(adapter *ReadOnlyCapabilityAdapterSpec) *ReadOnlyCapabilitySelectedAdapter {
	if adapter == nil {
		return nil
	}

	source := strings.TrimSpace(adapter.Source)

	return &ReadOnlyCapabilitySelectedAdapter{
		Tool:        strings.TrimSpace(adapter.Tool),
		Capability:  strings.TrimSpace(adapter.Capability),
		Language:    strings.TrimSpace(adapter.Language),
		Source:      source,
		Provenance:  readOnlyCapabilityAdapterProvenance(source),
		SideEffects: append([]autonomy.SideEffectClass(nil), adapter.SideEffects...),
	}
}

func readOnlyCapabilityAdapterProvenance(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "read_only_capability_registry"
	}

	return "read_only_capability_registry:" + source
}

func readOnlyCapabilityStatusPrecisionTier(result *ToolResult, underlying *ReadOnlyCapabilityUnderlyingResult) string {
	if result != nil {
		if tier := strings.TrimSpace(result.PrecisionTier); tier != "" {
			return tier
		}
	}

	if underlying != nil {
		return strings.TrimSpace(underlying.PrecisionTier)
	}

	return ""
}

func readOnlyCapabilityRouteConfidence(precisionTier string, fallback ReadOnlyCapabilityFallbackStatus, absence *ReadOnlyCapabilityAbsenceConfidence, result *ToolResult) string {
	if absence != nil {
		return absence.Level
	}

	if result != nil && strings.TrimSpace(result.Error) != "" {
		return "low"
	}

	switch strings.TrimSpace(precisionTier) {
	case PrecisionTierLSP:
		return "high"
	case PrecisionTierSyntaxIndex:
		return "medium"
	case PrecisionTierRGFallback, PrecisionTierUnavailable:
		return "low"
	}

	if fallback.State != "" && fallback.State != "none" {
		return "low"
	}

	return "observed"
}

func readOnlyCapabilityFreshnessState(payload *ReadOnlyCapabilityExecutionResult, result *ToolResult) string {
	if result != nil {
		lower := strings.ToLower(strings.TrimSpace(result.Output + "\n" + result.Summary))
		switch {
		case strings.Contains(lower, "stale"):
			return "stale_reported"
		case strings.Contains(lower, "fresh"):
			return "fresh_reported"
		}
	}

	if payload != nil && payload.Routed {
		return "not_reported"
	}

	return "unavailable"
}

func readOnlyCapabilityResultCounts(result *ToolResult, underlying *ReadOnlyCapabilityUnderlyingResult) ReadOnlyCapabilityResultCounts {
	var counts ReadOnlyCapabilityResultCounts

	if result != nil {
		counts.RelationshipCount = len(result.Relationships)
		counts.OutputLineCount = nonEmptyLineCount(result.Output)
		counts.OutputByteCount = len(result.Output)

		return counts
	}

	if underlying != nil {
		counts.RelationshipCount = len(underlying.Relationships)
		counts.OutputLineCount = nonEmptyLineCount(underlying.Output)
		counts.OutputByteCount = len(underlying.Output)
	}

	return counts
}

func readOnlyCapabilityTruncationState(result *ToolResult, underlying *ReadOnlyCapabilityUnderlyingResult) ReadOnlyCapabilityTruncationState {
	output := ""
	if result != nil {
		output = result.Output
	} else if underlying != nil {
		output = underlying.Output
	}

	state := "not_reported"
	if strings.Contains(strings.ToLower(output), "truncated") {
		state = "truncated_reported"
	}

	return ReadOnlyCapabilityTruncationState{State: state, OutputByteCount: len(output)}
}

func nonEmptyLineCount(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	count := 0

	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}

	return count
}

func readOnlyCapabilityStatusState(routed bool, fallback ReadOnlyCapabilityFallbackStatus, absence *ReadOnlyCapabilityAbsenceConfidence) string {
	if absence != nil {
		return "absent"
	}

	if fallback.State != "" && fallback.State != "none" {
		return "fallback"
	}

	if routed {
		return "routed"
	}

	return "fallback"
}

func readOnlyCapabilityFallbackStatusFor(sourceReason string) ReadOnlyCapabilityFallbackStatus {
	sourceReason = strings.TrimSpace(sourceReason)
	if sourceReason == "" {
		return ReadOnlyCapabilityFallbackStatus{State: "none"}
	}

	reason := readOnlyCapabilityFallbackReasonCode(sourceReason)
	status := ReadOnlyCapabilityFallbackStatus{
		State:  readOnlyCapabilityFallbackState(reason),
		Reason: reason,
	}

	if sourceReason != reason {
		status.SourceReason = limitCapabilityStatusDetail(sourceReason)
	}

	return status
}

func readOnlyCapabilityFallbackReasonCode(sourceReason string) string {
	sourceReason = strings.TrimSpace(sourceReason)
	switch sourceReason {
	case "adapter_schema_side_effects_not_read_only_only",
		"capability_deferred",
		"capability_route_disabled",
		"capability_not_runtime_routed",
		"capability_unmapped",
		"invalid_input",
		"no_available_read_only_adapter",
		"resolver_runtime_disabled",
		"underlying_adapter_error",
		"underlying_adapter_fallback":
		return sourceReason
	}

	lower := strings.ToLower(sourceReason)
	switch {
	case strings.Contains(lower, "unsupported language"):
		return "unsupported_language"
	case strings.Contains(lower, "no registered language route"),
		strings.Contains(lower, "no language route matched"):
		return "unsupported_language"
	case strings.Contains(lower, " has no ") && strings.Contains(lower, " route"):
		return "unsupported_language_capability"
	case strings.Contains(lower, "unavailable"),
		strings.Contains(lower, "not installed"),
		strings.Contains(lower, "language server"):
		return "adapter_unavailable"
	case strings.Contains(lower, "no generic code navigation matches"):
		return "absent_result"
	default:
		return "degraded_path"
	}
}

func readOnlyCapabilityFallbackState(reason string) string {
	switch reason {
	case "resolver_runtime_disabled", "capability_route_disabled":
		return "disabled"
	case "capability_deferred":
		return "deferred"
	case "capability_unmapped":
		return "unmapped"
	case "no_available_read_only_adapter", "adapter_unavailable":
		return "unavailable"
	case "unsupported_language", "unsupported_language_capability", "capability_not_runtime_routed":
		return "unsupported"
	case "invalid_input":
		return "rejected"
	case "underlying_adapter_error", "adapter_schema_side_effects_not_read_only_only":
		return "error"
	case "absent_result":
		return "absent"
	default:
		return "degraded"
	}
}

func readOnlyCapabilityAbsenceConfidenceFor(result *ToolResult) *ReadOnlyCapabilityAbsenceConfidence {
	if result == nil || strings.TrimSpace(result.Error) != "" {
		return nil
	}

	summary := strings.ToLower(strings.TrimSpace(result.Summary))
	output := strings.ToLower(strings.TrimSpace(result.Output))

	if !readOnlyCapabilityReportsAbsence(summary, output) {
		return nil
	}

	tier := strings.TrimSpace(result.PrecisionTier)

	return &ReadOnlyCapabilityAbsenceConfidence{
		Level:         readOnlyCapabilityAbsenceConfidenceLevel(tier),
		Reason:        readOnlyCapabilityAbsenceReason(summary, output),
		PrecisionTier: tier,
		Source:        strings.TrimSpace(result.Tool),
	}
}

func readOnlyCapabilityReportsAbsence(summary string, output string) bool {
	if strings.Contains(summary, "no warnings or errors") || strings.Contains(output, "no warnings or errors") ||
		strings.Contains(summary, "no issues found") || strings.Contains(output, "no issues found") {

		return false
	}

	if readOnlyCapabilityLineReportsAbsence(summary) {
		return true
	}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary: ") {
			if readOnlyCapabilityLineReportsAbsence(strings.TrimSpace(strings.TrimPrefix(line, "route_summary: "))) {
				return true
			}
		}
	}

	return false
}

func readOnlyCapabilityLineReportsAbsence(line string) bool {
	line = strings.TrimSpace(line)

	return strings.HasPrefix(line, "no ") &&
		(strings.Contains(line, " matched") ||
			strings.Contains(line, " matches") ||
			strings.Contains(line, " found"))
}

func readOnlyCapabilityAbsenceReason(summary string, output string) string {
	if strings.Contains(summary, "no generic code navigation matches") || strings.Contains(output, "no generic code navigation matches") {
		return "generic_fallback_no_matches"
	}

	if strings.Contains(summary, " matched") || strings.Contains(output, "route_summary: no ") {
		return "adapter_reported_no_matches"
	}

	if strings.Contains(summary, " found") {
		return "adapter_reported_not_found"
	}

	return "adapter_reported_absent_result"
}

func readOnlyCapabilityAbsenceConfidenceLevel(precisionTier string) string {
	switch strings.TrimSpace(precisionTier) {
	case PrecisionTierLSP:
		return "high"
	case PrecisionTierSyntaxIndex:
		return "medium"
	case PrecisionTierRGFallback, PrecisionTierUnavailable:
		return "low"
	default:
		return "observed"
	}
}

func readOnlyCapabilityStatusGaps(payload ReadOnlyCapabilityExecutionResult, fallback ReadOnlyCapabilityFallbackStatus, absence *ReadOnlyCapabilityAbsenceConfidence) []ReadOnlyCapabilityUnresolvedGap {
	var gaps []ReadOnlyCapabilityUnresolvedGap

	if fallback.State != "" && fallback.State != "none" {
		gaps = append(gaps, ReadOnlyCapabilityUnresolvedGap{
			State:      fallback.State,
			Reason:     fallback.Reason,
			Tool:       payload.RoutedTool,
			Capability: payload.Capability,
			Detail:     fallback.SourceReason,
		})
	}

	if !payload.Routed && len(payload.AvailableAdapters) == 0 && len(payload.UnavailableAdapters) > 0 {
		gaps = append(gaps, ReadOnlyCapabilityUnresolvedGap{
			State:        "unavailable",
			Reason:       "adapter_unavailable",
			Capability:   payload.Capability,
			AdapterCount: len(payload.UnavailableAdapters),
		})
	}

	if absence != nil {
		gaps = append(gaps, ReadOnlyCapabilityUnresolvedGap{
			State:      "empty",
			Reason:     absence.Reason,
			Tool:       payload.RoutedTool,
			Capability: payload.Capability,
		})
	}

	return readOnlyCapabilityUniqueGaps(gaps)
}

func readOnlyCapabilityUniqueGaps(gaps []ReadOnlyCapabilityUnresolvedGap) []ReadOnlyCapabilityUnresolvedGap {
	if len(gaps) <= 1 {
		return gaps
	}

	seen := make(map[string]struct{}, len(gaps))
	out := make([]ReadOnlyCapabilityUnresolvedGap, 0, len(gaps))

	for _, gap := range gaps {
		key := gap.State + "\x00" + gap.Reason + "\x00" + gap.Tool + "\x00" + gap.Capability + "\x00" + gap.Detail
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, gap)
	}

	return out
}

func limitCapabilityStatusDetail(value string) string {
	value = strings.TrimSpace(value)

	const limit = 240

	if len(value) <= limit {
		return value
	}

	return value[:limit]
}

func selectReadOnlyCapabilityAdapter(adapters []ReadOnlyCapabilityAdapterSpec) (ReadOnlyCapabilityAdapterSpec, string, bool) {
	for _, adapter := range adapters {
		if adapter.Tool != ToolNameCodeNavigate {
			continue
		}

		if !isReadOnlyOnlySideEffects(toolSchemaSideEffects(adapter.Tool)) {
			return ReadOnlyCapabilityAdapterSpec{}, "adapter_schema_side_effects_not_read_only_only", false
		}

		return adapter, "", true
	}

	for _, adapter := range adapters {
		tool := strings.TrimSpace(adapter.Tool)
		if tool == "" {
			continue
		}

		if !isReadOnlyOnlySideEffects(toolSchemaSideEffects(tool)) {
			continue
		}

		return adapter, "", true
	}

	if len(adapters) > 0 {
		return ReadOnlyCapabilityAdapterSpec{}, "adapter_schema_side_effects_not_read_only_only", false
	}

	return ReadOnlyCapabilityAdapterSpec{}, "no_available_read_only_adapter", false
}

func readOnlyCapabilityUnderlyingFallbackReason(result ToolResult) string {
	if strings.TrimSpace(result.Error) != "" {
		return "underlying_adapter_error"
	}

	if reason := toolOutputMetadataValue(result.Output, "fallback_reason"); reason != "" {
		return reason
	}

	if strings.Contains(strings.ToLower(result.Summary), "fallback") {
		return "underlying_adapter_fallback"
	}

	return ""
}

func toolOutputMetadataValue(output string, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}

	prefix := strings.ToLower(key) + ":"

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}

	return ""
}

func readOnlyCapabilityResolverRuntimeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(readOnlyCapabilityResolverEnv))) {
	case "0", "false", "off", "disabled", "disable", "no":
		return false
	default:
		return true
	}
}

func isReadOnlyCapabilityRouteDisabled(id string) bool {
	disabled := readOnlyCapabilityDisabledRouteSet()
	if len(disabled) == 0 {
		return false
	}

	_, ok := disabled[normalizeReadOnlyCapabilityID(id)]

	return ok
}

func readOnlyCapabilityDisabledRouteSet() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(readOnlyCapabilityDisabledRoutesEnv))
	if raw == "" {
		return nil
	}

	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	out := make(map[string]struct{}, len(fields))

	for _, field := range fields {
		id := normalizeReadOnlyCapabilityID(field)
		if id == "" {
			continue
		}

		out[id] = struct{}{}
	}

	return out
}

func readOnlyCapabilityPublicStatusEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(readOnlyCapabilityPublicStatusEnv))) {
	case "0", "false", "off", "disabled", "disable", "no", "hidden", "hide", "suppressed", "suppress":
		return false
	default:
		return true
	}
}

func readOnlyCapabilityRouteStates(registry ReadOnlyCapabilityRegistry, tools []string) []ReadOnlyCapabilityRouteState {
	var states []ReadOnlyCapabilityRouteState

	for _, capability := range registry.Capabilities {
		state := ReadOnlyCapabilityRouteState{Capability: strings.TrimSpace(capability.ID)}
		switch {
		case !registry.ResolverRuntimeEnabled:
			state.State = "disabled"
			state.Reason = "resolver_runtime_disabled"
		case isReadOnlyCapabilityRouteDisabled(capability.ID):
			state.State = "disabled"
			state.Reason = "capability_route_disabled"
		case !isReadOnlyCapabilityRuntimeRouted(capability.ID):
			state.State = "deferred"
			state.Reason = "capability_not_runtime_routed"
		default:
			available, unavailable := filterCapabilityAdapters(capability.Adapters, tools)

			state.AdapterCount = len(available)

			if len(available) == 0 && len(unavailable) > 0 {
				state.State = "unavailable"
				state.Reason = "adapter_unavailable"

				break
			}

			if _, reason, ok := selectReadOnlyCapabilityAdapter(available); ok {
				state.State = "routed"
			} else {
				state.State = readOnlyCapabilityFallbackState(reason)
				state.Reason = readOnlyCapabilityFallbackReasonCode(reason)
			}
		}

		states = append(states, state)
	}

	deferredStates := make(map[string]ReadOnlyCapabilityRouteState)
	for _, adapter := range deferredReadOnlyCapabilityAdaptersForTools(stringSet(tools)) {
		capability := strings.TrimSpace(adapter.Capability)
		if capability == "" {
			continue
		}
		state := deferredStates[capability]
		state.Capability = capability
		state.State = "deferred"
		state.Reason = strings.TrimSpace(adapter.Reason)
		state.AdapterCount++
		deferredStates[capability] = state
	}
	for _, state := range deferredStates {
		states = append(states, state)
	}

	slices.SortFunc(states, func(a, b ReadOnlyCapabilityRouteState) int {
		return cmp.Compare(a.Capability, b.Capability)
	})

	return states
}

func readOnlyCapabilityRoutedIDsFromStates(states []ReadOnlyCapabilityRouteState) []string {
	var routed []string

	for _, state := range states {
		if state.State == "routed" {
			routed = append(routed, state.Capability)
		}
	}

	slices.Sort(routed)

	return routed
}

func readOnlyCapabilityDisabledIDsFromStates(states []ReadOnlyCapabilityRouteState) []string {
	var disabled []string

	for _, state := range states {
		if state.State == "disabled" {
			disabled = append(disabled, state.Capability)
		}
	}

	slices.Sort(disabled)

	return disabled
}

func readOnlyCapabilityRouteStateCounts(states []ReadOnlyCapabilityRouteState) map[string]int {
	if len(states) == 0 {
		return nil
	}

	counts := make(map[string]int)

	for _, state := range states {
		if state.State != "" {
			counts[state.State]++
		}
	}

	return counts
}

func isReadOnlyCapabilityRuntimeRouted(id string) bool {
	switch normalizeReadOnlyCapabilityID(id) {
	case string(ReadOnlyCapabilityLocalizeChange),
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
		string(ReadOnlyCapabilityAuditBehavior):
		return true
	default:
		return false
	}
}

func readOnlyCapabilityFallbackReasonCodes() []string {
	return []string{
		"absent_result",
		"adapter_unavailable",
		"adapter_schema_side_effects_not_read_only_only",
		"capability_deferred",
		"capability_not_runtime_routed",
		"capability_route_disabled",
		"capability_unmapped",
		"degraded_path",
		"invalid_input",
		"no_available_read_only_adapter",
		"resolver_runtime_disabled",
		"underlying_adapter_error",
		"underlying_adapter_fallback",
		"unsupported_language",
		"unsupported_language_capability",
	}
}

func readOnlyCapabilityFallbackStates() []string {
	return []string{
		"absent",
		"degraded",
		"deferred",
		"disabled",
		"error",
		"rejected",
		"unavailable",
		"unmapped",
		"unsupported",
	}
}

func readOnlyCapabilityStatusFieldCount() int {
	return jsonFieldCount(reflect.TypeOf(ReadOnlyCapabilityStatus{}))
}

func jsonFieldCount(t reflect.Type) int {
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

func readOnlyCapability(id ReadOnlyCapabilityID, description string, adapters []ReadOnlyCapabilityAdapterSpec) ReadOnlyCapabilitySpec {
	return ReadOnlyCapabilitySpec{
		ID:          string(id),
		Description: description,
		ReadOnly:    true,
		SideEffects: []autonomy.SideEffectClass{autonomy.SideEffectReadOnly},
		Adapters:    adapters,
	}
}

func codeNavigateCapabilityAdapter(cap semanticlanguage.Capability) ReadOnlyCapabilityAdapterSpec {
	return readOnlyAdapter(ToolNameCodeNavigate, string(cap), "", "capability_router")
}

func languageCapabilityAdapters(cap semanticlanguage.Capability) []ReadOnlyCapabilityAdapterSpec {
	var adapters []ReadOnlyCapabilityAdapterSpec

	for _, lang := range semanticlanguage.LanguageRegistry() {
		tool := strings.TrimSpace(lang.Tool(cap))
		if tool == "" {
			continue
		}

		adapters = append(adapters, readOnlyAdapter(tool, string(cap), lang.Name, "language_registry"))
	}

	return adapters
}

func readOnlyAdapter(tool string, capability string, language string, source string) ReadOnlyCapabilityAdapterSpec {
	return ReadOnlyCapabilityAdapterSpec{
		Tool:       strings.TrimSpace(tool),
		Capability: strings.TrimSpace(capability),
		Language:   strings.TrimSpace(language),
		Source:     strings.TrimSpace(source),
		SideEffects: []autonomy.SideEffectClass{
			autonomy.SideEffectReadOnly,
		},
	}
}

func filterCapabilityAdapters(adapters []ReadOnlyCapabilityAdapterSpec, availableTools []string) ([]ReadOnlyCapabilityAdapterSpec, []ReadOnlyCapabilityAdapterSpec) {
	availableSet := stringSet(availableTools)
	if len(availableSet) == 0 {
		return cloneCapabilityAdapters(adapters), nil
	}

	var (
		available   []ReadOnlyCapabilityAdapterSpec
		unavailable []ReadOnlyCapabilityAdapterSpec
	)

	for _, adapter := range adapters {
		if _, ok := availableSet[adapter.Tool]; ok {
			available = append(available, adapter)
		} else {
			unavailable = append(unavailable, adapter)
		}
	}

	return available, unavailable
}

func readOnlyCapabilityMetricAdapters(tools []string) []string {
	if len(tools) == 0 {
		tools = toolManifestToolsAcrossBuiltInSurfaces()
	}

	unique := textutil.UniqueNonEmpty(tools)
	out := make([]string, 0, len(unique))

	for _, tool := range unique {
		if isReadOnlyCapabilityMetricAdapter(tool) {
			out = append(out, tool)
		}
	}

	slices.Sort(out)

	return out
}

func isReadOnlyCapabilityMetricAdapter(tool string) bool {
	if strings.TrimSpace(tool) == ToolNameReadOnlyCapability {
		return false
	}

	return isReadOnlyOnlySideEffects(toolSchemaSideEffects(tool))
}

func normalizeReadOnlyCapabilityID(id string) string {
	key := strings.ToLower(strings.TrimSpace(id))

	key = strings.ReplaceAll(key, "_", "-")
	key = strings.ReplaceAll(key, " ", "-")

	for strings.Contains(key, "--") {
		key = strings.ReplaceAll(key, "--", "-")
	}

	switch key {
	case "localize", "localize-change", "localizechange", "localization", "localise-change":
		return string(ReadOnlyCapabilityLocalizeChange)
	case "query-context", "querycontext", "context-query", "contextquery":
		return string(ReadOnlyCapabilityQueryContext)
	case "outline", "fileoutline", "inspect-file", "inspectfile", "file-outline":
		return string(ReadOnlyCapabilityFileOutline)
	case "read-symbol", "read-symbols", "readsymbol", "inspect-symbol", "inspectsymbol", "symbol-body", "symbolbody":
		return string(ReadOnlyCapabilityInspectSymbol)
	case "symbol", "lookup", "symbol-search", "symbollookup", "workspace-symbol", "workspace-symbols":
		return string(ReadOnlyCapabilitySymbolLookup)
	case "definition", "declaration", "find-definition", "go-to-definition", "goto-definition":
		return string(ReadOnlyCapabilityDefinition)
	case "reference", "references", "refs", "usages", "find-reference", "find-references":
		return string(ReadOnlyCapabilityReferences)
	case "implementation", "implementations", "find-implementation", "find-implementations":
		return string(ReadOnlyCapabilityImplementations)
	case "calls-from", "callees":
		return string(ReadOnlyCapabilityCallsFrom)
	case "calls-into", "callers":
		return string(ReadOnlyCapabilityCallsInto)
	case "related", "related-file", "related-files":
		return string(ReadOnlyCapabilityRelatedFiles)
	case "audit", "audit-behavior", "auditbehaviour", "audit-behaviour", "harness-audit":
		return string(ReadOnlyCapabilityAuditBehavior)
	case string(ReadOnlyCapabilitySourceSanity), "sourcesanity", "source-diagnostics":
		return string(ReadOnlyCapabilitySourceSanity)
	case string(ReadOnlyCapabilityTraceRuntime), "traceruntimefailure", "runtime-trace", "trace-failure":
		return string(ReadOnlyCapabilityTraceRuntime)
	case string(ReadOnlyCapabilityVerifyEdit), "verifyedit", "verification", "source-verification":
		return string(ReadOnlyCapabilityVerifyEdit)
	default:
		return key
	}
}

func unsafeReadOnlyCapabilityMappingsForTools(registry ReadOnlyCapabilityRegistry, tools []string) []ReadOnlyCapabilityUnsafeAdapterSpec {
	toolSet := stringSet(tools)
	filterTools := len(toolSet) > 0

	var unsafe []ReadOnlyCapabilityUnsafeAdapterSpec

	for _, capability := range registry.Capabilities {
		for _, adapter := range capability.Adapters {
			tool := strings.TrimSpace(adapter.Tool)
			if tool == "" {
				continue
			}

			if filterTools {
				if _, ok := toolSet[tool]; !ok {
					continue
				}
			}

			schemaSideEffects := toolSchemaSideEffects(tool)
			reason := unsafeReadOnlyCapabilityReason(adapter.SideEffects, schemaSideEffects)

			if reason == "" {
				continue
			}

			unsafe = append(unsafe, ReadOnlyCapabilityUnsafeAdapterSpec{
				Capability:          strings.TrimSpace(capability.ID),
				Tool:                tool,
				Reason:              reason,
				DeclaredSideEffects: append([]autonomy.SideEffectClass(nil), adapter.SideEffects...),
				SchemaSideEffects:   append([]autonomy.SideEffectClass(nil), schemaSideEffects...),
			})
		}
	}

	slices.SortFunc(unsafe, func(a, b ReadOnlyCapabilityUnsafeAdapterSpec) int {
		if c := cmp.Compare(a.Capability, b.Capability); c != 0 {
			return c
		}

		return cmp.Compare(a.Tool, b.Tool)
	})

	return unsafe
}

func unsafeReadOnlyCapabilityReason(declaredSideEffects []autonomy.SideEffectClass, schemaSideEffects []autonomy.SideEffectClass) string {
	if !isReadOnlyOnlySideEffects(schemaSideEffects) {
		return "schema_side_effects_not_read_only_only"
	}

	if !sameSideEffects(declaredSideEffects, schemaSideEffects) {
		return "declared_side_effects_mismatch_schema"
	}

	return ""
}

func deferredReadOnlyCapabilityAdaptersForTools(toolSet map[string]struct{}) []ReadOnlyCapabilityDeferredAdapterSpec {
	deferred := []ReadOnlyCapabilityDeferredAdapterSpec{
		{
			Capability:        string(ReadOnlyCapabilitySourceSanity),
			Tool:              ToolNameSourceSanity,
			Reason:            "schema_side_effects_not_read_only_only",
			SchemaSideEffects: append([]autonomy.SideEffectClass(nil), toolSchemaSideEffects(ToolNameSourceSanity)...),
		},
		{
			Capability:        string(ReadOnlyCapabilityTraceRuntime),
			Tool:              ToolNameContextTracePack,
			Reason:            "schema_side_effects_not_read_only_only",
			SchemaSideEffects: append([]autonomy.SideEffectClass(nil), toolSchemaSideEffects(ToolNameContextTracePack)...),
		},
		{
			Capability:        string(ReadOnlyCapabilityVerifyEdit),
			Tool:              ToolNameContextTracePack,
			Reason:            "schema_side_effects_not_read_only_only",
			SchemaSideEffects: append([]autonomy.SideEffectClass(nil), toolSchemaSideEffects(ToolNameContextTracePack)...),
		},
		{
			Capability:        string(ReadOnlyCapabilityVerifyEdit),
			Tool:              ToolNameSourceSanity,
			Reason:            "schema_side_effects_not_read_only_only",
			SchemaSideEffects: append([]autonomy.SideEffectClass(nil), toolSchemaSideEffects(ToolNameSourceSanity)...),
		},
		{
			Capability:        string(ReadOnlyCapabilityVerifyEdit),
			Tool:              ToolNameShell,
			Reason:            "schema_side_effects_not_read_only_only",
			SchemaSideEffects: append([]autonomy.SideEffectClass(nil), toolSchemaSideEffects(ToolNameShell)...),
		},
	}
	out := deferred[:0]

	for _, adapter := range deferred {
		if len(toolSet) > 0 {
			if _, ok := toolSet[adapter.Tool]; !ok {
				continue
			}
		}

		out = append(out, adapter)
	}

	slices.SortFunc(out, func(a, b ReadOnlyCapabilityDeferredAdapterSpec) int {
		if c := cmp.Compare(a.Capability, b.Capability); c != 0 {
			return c
		}

		return cmp.Compare(a.Tool, b.Tool)
	})

	return append([]ReadOnlyCapabilityDeferredAdapterSpec(nil), out...)
}

func deferredReadOnlyCapabilityAdapter(id string) (ReadOnlyCapabilityDeferredAdapterSpec, bool) {
	want := normalizeReadOnlyCapabilityID(id)
	for _, adapter := range deferredReadOnlyCapabilityAdaptersForTools(nil) {
		if normalizeReadOnlyCapabilityID(adapter.Capability) == want {
			return adapter, true
		}
	}

	return ReadOnlyCapabilityDeferredAdapterSpec{}, false
}

func isReadOnlyOnlySideEffects(sideEffects []autonomy.SideEffectClass) bool {
	return len(sideEffects) == 1 && sideEffects[0] == autonomy.SideEffectReadOnly
}

func sameSideEffects(left []autonomy.SideEffectClass, right []autonomy.SideEffectClass) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func cloneCapabilityAdapters(adapters []ReadOnlyCapabilityAdapterSpec) []ReadOnlyCapabilityAdapterSpec {
	out := make([]ReadOnlyCapabilityAdapterSpec, len(adapters))
	copy(out, adapters)

	for i := range out {
		out[i].SideEffects = append([]autonomy.SideEffectClass(nil), out[i].SideEffects...)
	}

	return out
}

func stringSet(values []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		seen[value] = struct{}{}
	}

	return seen
}
