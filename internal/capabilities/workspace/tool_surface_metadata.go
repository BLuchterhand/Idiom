package workspace

import (
	"slices"
	"strings"
	"time"

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
	Timeout                  time.Duration
	PlannerAllowed           bool
	ActionClass              string
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
	{Tool: ToolNamePwd, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, ActionClass: "read"},
	{Tool: ToolNameListFiles, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, ActionClass: "search"},
	{Tool: ToolNameSearchFiles, Category: CategorySearch, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameSearchContent, Category: CategorySearch, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameRg, Category: CategorySearch, Narrowing: true, ActionClass: "search"},
	{Tool: ToolNameGrep, Category: CategorySearch, ActionClass: "search"},
	{Tool: ToolNameReadFile, Category: CategoryRead, DefaultManifest: true, EvidenceCapabilities: []string{"inspect_file"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true, PlannerAllowed: true, ActionClass: "read"},
	{Tool: ToolNameFileStats, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true, PlannerAllowed: true, ActionClass: "read"},
	{Tool: ToolNameCodeNavigate, Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: CodeNavigationEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true, PlannerAllowed: true, ActionClass: "read"},
	{Tool: readOnlyCapabilityToolName, Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: ReadOnlyCapabilityEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, ActionClass: "read"},
	{Tool: ToolNameSourceSanity, Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"source_diagnostics", "verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnlyUnknown, RuntimeSourceInspection: true, ActionClass: "verify"},
	{Tool: ToolNameHarnessAudit, Category: CategoryRead, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"audit_behavior"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameHarnessFeedback, Category: CategoryVerification, DefaultManifest: true, Narrowing: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, PlannerAllowed: true},
	{Tool: ToolNameIdiomFeedback, Category: CategoryVerification, Narrowing: true},
	{Tool: ToolNameRecoveryInspect, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, PlannerAllowed: true},
	{Tool: ToolNameRecoveryStatus, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, PlannerAllowed: true},
	{Tool: ToolNameRecoveryDecompose, Category: CategoryVerification, DefaultManifest: true, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: ToolNameProductEfforts, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true, PlannerAllowed: true},
	{Tool: ToolNameProductStatus, Category: CategoryRead, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true, PlannerAllowed: true},
	{Tool: ToolNameContextIndex, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, StaleContextErrors: true, Timeout: 5 * time.Minute},
	{Tool: ToolNameContextStatus, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameContextQuery, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameContextZoom, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change", "query_context"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameContextPack, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameContextImpact, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameContextTracePack, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, StaleContextErrors: true, Timeout: 5 * time.Minute, ActionClass: "verify"},
	{Tool: ToolNameDiscoveryQuery, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"localize_change"}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameStructuralSearch, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true, RuntimeContextDiscovery: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameDependencyGraph, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"related_files"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true},
	{Tool: ToolNameHistoryRange, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true, PlannerAllowed: true, ActionClass: "read"},
	{Tool: ToolNameHistorySymbol, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true, PlannerAllowed: true, ActionClass: "read"},
	{Tool: ToolNameBlameRange, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"history_context"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true, PlannerAllowed: true, ActionClass: "search"},
	{Tool: ToolNameCtagsSymbols, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"find_references", "inspect_symbol"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 90 * time.Second},
	{Tool: ToolNameSemgrepScan, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"security_scan"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
	{Tool: ToolNameJoernScan, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{"security_scan"}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
	{Tool: ToolNameGitStatus, Category: CategoryShell, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, ActionClass: "verify"},
	{Tool: ToolNameGitDiff, Category: CategoryShell, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, ActionClass: "verify"},
	{Tool: ToolNameShell, Category: CategoryShell, DefaultManifest: true, EvidenceCapabilities: []string{"verify_edit"}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyMutatingShell, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNameTaskLedger, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: ToolNameTask, PolicyCapabilities: policyDelegation, SideEffects: sideEffectReadOnlyAdditive, ActionClass: "generic_probe"},
	{Tool: ToolNameEditFile, Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameWriteFile, Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameAppendFile, Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameApplyPatch, Category: CategoryWrite, PolicyCapabilities: policyApplyPatch, SideEffects: sideEffectAdditiveDestructive, ActionClass: "per_edit_write"},
	{Tool: ToolNameMkdir, Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameDelegateEdit, Category: CategoryWrite, PolicyCapabilities: policyDelegatingEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameGoMoveDeclarations, Language: "go", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive, ActionClass: "per_edit_write"},
	{Tool: ToolNameSubmitPlan, Category: CategoryVerification, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyPlanSubmission, SideEffects: sideEffectAdditive, PlannerAllowed: true, ActionClass: "submit_plan"},
	{Tool: ToolNameGoDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNameCppDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNamePythonDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNameRustDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNameJavaDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
	{Tool: ToolNameTsDiagnostics, Category: CategoryRead, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, ActionClass: "verify"},
}

func builtInToolSurfaceMetadata(tool string) (builtInToolSurfaceSpec, bool) {
	return BuiltInToolSurfaceMetadata(tool)
}

// ToolActionClass returns the action class for a tool, or empty string if unknown.
func ToolActionClass(tool string) string {
	spec, ok := BuiltInToolSurfaceMetadata(tool)
	if !ok || spec.ActionClass == "" {
		return ""
	}

	return spec.ActionClass
}

func BuiltInToolSurfaceMetadata(tool string) (builtInToolSurfaceSpec, bool) {
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
