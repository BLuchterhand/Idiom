package workspace

import (
	"slices"
	"strings"
	"time"

	"idiom/internal/capabilities/toolcatalog"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
)

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

var builtInToolSurfaceCatalog = toolcatalog.MustNewToolSurfaceCatalog([]toolcatalog.ToolSurfaceSpec{
	{Tool: ToolNamePwd, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameListFiles, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameSearchFiles, Category: int(CategorySearch), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: ToolNameSearchContent, Category: int(CategorySearch), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: ToolNameRg, Category: int(CategorySearch), Narrowing: true},
	{Tool: ToolNameReadFile, Category: int(CategoryRead), DefaultManifest: true, EvidenceCapabilities: []string{EvidenceCapabilityInspectFile}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: ToolNameFileStats, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: ToolNameCodeNavigate, Category: int(CategoryRead), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: CodeNavigationEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: readOnlyCapabilityToolName, Category: int(CategoryRead), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: ReadOnlyCapabilityEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameSourceSanity, Category: int(CategoryRead), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilitySourceDiagnostics, EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnlyUnknown, RuntimeSourceInspection: true},
	{Tool: ToolNameCodingRulesCheck, Category: int(CategoryRead), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilitySourceDiagnostics, EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
	{Tool: ToolNameHarnessAudit, Category: int(CategoryRead), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityAuditBehavior}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameHarnessFeedback, Category: int(CategoryVerification), DefaultManifest: true, Narrowing: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive},
	{Tool: ToolNameRecoveryInspect, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true},
	{Tool: ToolNameRecoveryStatus, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true},
	{Tool: ToolNameRecoveryDecompose, Category: int(CategoryVerification), DefaultManifest: true, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: ToolNameProductEfforts, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true},
	{Tool: ToolNameProductStatus, Category: int(CategoryRead), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, MCPDiscoveryReadModel: true},
	{Tool: ToolNameContextIndex, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, StaleContextErrors: true, Timeout: 5 * time.Minute},
	{Tool: ToolNameContextStatus, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: ToolNameContextQuery, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true, Timeout: 90 * time.Second},
	{Tool: ToolNameContextZoom, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: ToolNameContextPack, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: ToolNameContextImpact, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: ToolNameContextTracePack, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, StaleContextErrors: true, Timeout: 5 * time.Minute},
	{Tool: ToolNameDiscoveryQuery, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true, Timeout: 90 * time.Second},
	{Tool: ToolNameStructuralSearch, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true, RuntimeContextDiscovery: true},
	{Tool: ToolNameDependencyGraph, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true},
	{Tool: ToolNameHistoryRange, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: ToolNameHistorySymbol, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: ToolNameBlameRange, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, RuntimeHistoryInspection: true},
	{Tool: ToolNameCtagsSymbols, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityFindReferences, EvidenceCapabilityInspectSymbol}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 90 * time.Second},
	{Tool: ToolNameSemgrepScan, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilitySecurityScan}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
	{Tool: ToolNameJoernScan, Category: int(CategoryDiscovery), DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilitySecurityScan}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
	{Tool: ToolNameGitStatus, Category: int(CategoryShell), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameGitDiff, Category: int(CategoryShell), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
	{Tool: ToolNameShell, Category: int(CategoryShell), DefaultManifest: true, EvidenceCapabilities: []string{EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyMutatingShell, SideEffects: sideEffectUnknown},
	{Tool: ToolNameSystemSearchAccess, Category: int(CategoryVerification), DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnlyAdditive},
	{Tool: ToolNameTaskLedger, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
	{Tool: ToolNameTask, PolicyCapabilities: policyDelegation, SideEffects: sideEffectReadOnlyAdditive},
	{Tool: ToolNameEditFile, Category: int(CategoryWrite), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameWriteFile, Category: int(CategoryWrite), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameAppendFile, Category: int(CategoryWrite), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameApplyPatch, Category: int(CategoryWrite), PolicyCapabilities: policyApplyPatch, SideEffects: sideEffectAdditiveDestructive},
	{Tool: ToolNameMkdir, Category: int(CategoryWrite), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameDelegateEdit, Category: int(CategoryWrite), PolicyCapabilities: policyDelegatingEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameGoMoveDeclarations, Language: "go", Category: int(CategoryWrite), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
	{Tool: ToolNameSubmitPlan, Category: int(CategoryVerification), CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyPlanSubmission, SideEffects: sideEffectAdditive},
	{Tool: ToolNameGoDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	{Tool: ToolNameCppDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	{Tool: ToolNamePythonDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	{Tool: ToolNameRustDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	{Tool: ToolNameJavaDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	{Tool: ToolNameTsDiagnostics, Category: int(CategoryRead), DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
})

func defaultToolManifestTools() []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.DefaultManifest
	})
}

func builtInToolSurfaceToolsByCategory(category ToolCategory) []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return ToolCategory(spec.Category) == category
	})
}

func builtInToolSurfaceToolsByCategoryWithoutLanguageSpecific(category ToolCategory) []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return ToolCategory(spec.Category) == category && strings.TrimSpace(spec.Language) == ""
	})
}

func builtInToolSurfaceEvidenceCapabilities(tool string) []string {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	if !ok || len(spec.EvidenceCapabilities) == 0 {
		return nil
	}

	return slices.Clone(spec.EvidenceCapabilities)
}

func builtInToolSurfacePolicyCapabilities(tool string) []autonomy.Capability {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	if !ok || len(spec.PolicyCapabilities) == 0 {
		return nil
	}

	return slices.Clone(spec.PolicyCapabilities)
}

func builtInToolSurfaceSideEffects(tool string) []autonomy.SideEffectClass {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	if !ok || len(spec.SideEffects) == 0 {
		return nil
	}

	return slices.Clone(spec.SideEffects)
}

func builtInToolSurfaceMCPDiscoveryProfileTool(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	return ok && spec.MCPDiscoveryProfile
}

func builtInToolSurfaceDiscoveryLocalArtifacts(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	return ok && spec.DiscoveryLocalArtifacts
}

func builtInToolSurfaceStaleContextErrors(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	return ok && spec.StaleContextErrors
}

func builtInToolSurfaceMCPDiscoveryReadModelTool(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog.Lookup(tool)
	return ok && spec.MCPDiscoveryReadModel
}

func RuntimeSourceInspectionToolNames() []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeSourceInspection
	})
}

func RuntimeContextDiscoveryToolNames() []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeContextDiscovery
	})
}

func RuntimeHistoryInspectionToolNames() []string {
	return builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		return spec.RuntimeHistoryInspection
	})
}

func RuntimeReadOnlyInvestigationToolNames() []string {
	tools := builtInToolSurfaceCatalog.ToolsWhere(func(spec toolcatalog.ToolSurfaceSpec) bool {
		if spec.Tool == ToolNameTask {
			return true
		}
		if spec.Tool == ToolNameSystemSearchAccess {
			return true
		}

		if spec.DiscoveryLocalArtifacts || !slices.Equal(spec.SideEffects, sideEffectReadOnly) {
			return false
		}

		actionClass := autonomy.CanonicalActionClass(autonomy.TurnPhaseActionClassForToolClass(spec.Tool))
		switch actionClass {
		case autonomy.ActionClassRead, autonomy.ActionClassSearch, autonomy.ActionClassGenericProbe:
		default:
			return false
		}

		return spec.RuntimeSourceInspection ||
			spec.RuntimeContextDiscovery ||
			spec.RuntimeHistoryInspection ||
			spec.Tool == ToolNameReadOnlyCapability ||
			spec.Tool == ToolNameHarnessAudit ||
			spec.Tool == ToolNameTask
	})

	tools = append(tools, ToolNameMCPWebFetchExa, ToolNameMCPWebSearchExa)

	return textutil.UniqueNonEmpty(tools)
}
