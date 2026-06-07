package workspace

import (
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
)

type builtInToolSurfaceSpec struct {
	Tool                     string
	Language                 string
	Category                 ToolCategory
	DefaultManifest          bool
	Narrowing                bool
	EvidenceCapabilities     []string
	CapabilityClassification string
	PolicyCapabilities       []autonomy.Capability
	SideEffects              []autonomy.SideEffectClass
	MCPDiscoveryProfile      bool
	DiscoveryLocalArtifacts  bool
	StaleContextErrors       bool
	MCPDiscoveryReadModel    bool
	RuntimeSourceInspection  bool
	RuntimeContextDiscovery  bool
	RuntimeHistoryInspection bool
}

var (
	policyBoundedRead       = []autonomy.Capability{autonomy.CapabilityBoundedRead}
	policyLiteralSearch     = []autonomy.Capability{autonomy.CapabilityLiteralSearch}
	policySemanticDiscovery = []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}
	policySourceEdit        = []autonomy.Capability{autonomy.CapabilitySourceEdit}
	policyApplyPatch        = []autonomy.Capability{autonomy.CapabilityApplyPatch}
	policyShellVerification = []autonomy.Capability{autonomy.CapabilityShellVerification}
	policyMutatingShell     = []autonomy.Capability{autonomy.CapabilityShellVerification, autonomy.CapabilityMutatingShell}
	policyDelegation        = []autonomy.Capability{autonomy.CapabilityDelegation}
	policyDelegatingEdit    = []autonomy.Capability{autonomy.CapabilityDelegation, autonomy.CapabilitySourceEdit}
	policyPlanSubmission    = []autonomy.Capability{autonomy.CapabilityPlanSubmission}
	policyExternalWrite     = []autonomy.Capability{autonomy.CapabilityExternalWrite}

	sideEffectReadOnly            = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
	sideEffectAdditive            = []autonomy.SideEffectClass{autonomy.SideEffectAdditive}
	sideEffectUnknown             = []autonomy.SideEffectClass{autonomy.SideEffectUnknown}
	sideEffectReadOnlyUnknown     = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectUnknown}
	sideEffectReadOnlyAdditive    = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
	sideEffectAdditiveDestructive = []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive}
)

var builtInToolSurfaceSpecs = []builtInToolSurfaceSpec{
	{Tool: "pwd", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: "list_files", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: "search_files", Category: CategorySearch, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: "search_content", Category: CategorySearch, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: "rg", Category: CategorySearch, Narrowing: true},
	{Tool: "grep", Category: CategorySearch},
	{Tool: "read_file", Category: CategoryRead, DefaultManifest: true, EvidenceCapabilities: []string{"inspect_file"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: "file_stats", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: "code_navigate", Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: CodeNavigationEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: readOnlyCapabilityToolName, Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: ReadOnlyCapabilityEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: "source_sanity", Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"source_diagnostics", "verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnlyUnknown, RuntimeSourceInspection: true},
	{Tool: "harness_audit", Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"audit_behavior"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: "harness_feedback", Category: CategoryVerification, DefaultManifest: true, Narrowing: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive},
	{Tool: "idiom_feedback", Category: CategoryVerification, Narrowing: true},
	{Tool: "recovery_inspect", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true},
	{Tool: "recovery_status", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true},
	{Tool: "recovery_decompose", Category: CategoryVerification, DefaultManifest: true, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: "product_efforts", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true},
	{Tool: "product_status", Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true},
	{Tool: "context_index", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, StaleContextErrors: true},
	{Tool: "context_status", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "context_query", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "context_zoom", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "context_pack", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "context_impact", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "context_trace_pack", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, StaleContextErrors: true},
	{Tool: "discovery_query", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "structural_search", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: "dependency_graph", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"related_files"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true},
	{Tool: "history_range", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: "history_symbol", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: "blame_range", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: "ctags_symbols", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references", "inspect_symbol"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: "semgrep_scan", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"security_scan"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: "joern_scan", Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"security_scan"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: "git_status", Category: CategoryShell, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: "git_diff", Category: CategoryShell, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: "shell", Category: CategoryShell, DefaultManifest: true, EvidenceCapabilities: []string{"verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyMutatingShell, SideEffects: sideEffectUnknown},
	{Tool: "task_ledger", PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: "task", PolicyCapabilities: policyDelegation, SideEffects: sideEffectReadOnlyAdditive},
	{Tool: "edit_file", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: "write_file", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: "append_file", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: "apply_patch", Category: CategoryWrite, PolicyCapabilities: policyApplyPatch, SideEffects: sideEffectAdditiveDestructive},
	{Tool: "mkdir", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: "delegate_edit", Category: CategoryWrite, PolicyCapabilities: policyDelegatingEdit, SideEffects: sideEffectAdditive},
	{Tool: "go_move_declarations", Language: "go", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: "submit_plan", Category: CategoryVerification, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyPlanSubmission, SideEffects: sideEffectAdditive},
}

func builtInToolSurfaceMetadata(tool string) (builtInToolSurfaceSpec, bool) {
	tool = strings.TrimSpace(tool)
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.Tool == tool {
			return spec, true
		}
	}
	return builtInToolSurfaceSpec{}, false
}

func defaultToolManifestTools() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.DefaultManifest {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func builtInToolSurfaceToolsByCategory(category ToolCategory) []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.Category == category {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func builtInToolSurfaceToolsByCategoryWithoutLanguageSpecific(category ToolCategory) []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.Category == category && strings.TrimSpace(spec.Language) == "" {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func builtInToolSurfaceEvidenceCapabilities(tool string) []string {
	spec, ok := builtInToolSurfaceMetadata(tool)
	if !ok || len(spec.EvidenceCapabilities) == 0 {
		return nil
	}
	return slices.Clone(spec.EvidenceCapabilities)
}

func builtInToolSurfacePolicyCapabilities(tool string) []autonomy.Capability {
	spec, ok := builtInToolSurfaceMetadata(tool)
	if !ok || len(spec.PolicyCapabilities) == 0 {
		return nil
	}
	return slices.Clone(spec.PolicyCapabilities)
}

func builtInToolSurfaceSideEffects(tool string) []autonomy.SideEffectClass {
	spec, ok := builtInToolSurfaceMetadata(tool)
	if !ok || len(spec.SideEffects) == 0 {
		return nil
	}
	return slices.Clone(spec.SideEffects)
}

func builtInToolSurfaceMCPDiscoveryProfileTool(tool string) bool {
	spec, ok := builtInToolSurfaceMetadata(tool)
	return ok && spec.MCPDiscoveryProfile
}

func builtInToolSurfaceDiscoveryLocalArtifacts(tool string) bool {
	spec, ok := builtInToolSurfaceMetadata(tool)
	return ok && spec.DiscoveryLocalArtifacts
}

func builtInToolSurfaceDiscoveryLocalArtifactTools() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.DiscoveryLocalArtifacts {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func builtInToolSurfaceStaleContextErrors(tool string) bool {
	spec, ok := builtInToolSurfaceMetadata(tool)
	return ok && spec.StaleContextErrors
}

func builtInToolSurfaceMCPDiscoveryReadModelTool(tool string) bool {
	spec, ok := builtInToolSurfaceMetadata(tool)
	return ok && spec.MCPDiscoveryReadModel
}

func RuntimeSourceInspectionToolNames() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.RuntimeSourceInspection {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func RuntimeContextDiscoveryToolNames() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.RuntimeContextDiscovery {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}

func RuntimeHistoryInspectionToolNames() []string {
	var tools []string
	for _, spec := range builtInToolSurfaceSpecs {
		if spec.RuntimeHistoryInspection {
			tools = append(tools, spec.Tool)
		}
	}
	return tools
}
