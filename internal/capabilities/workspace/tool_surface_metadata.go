package workspace

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/semantic/languagetools"
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
	policyPlanSubmission    = []autonomy.Capability{autonomy.CapabilityPlanSubmission}
	policyExternalWrite     = []autonomy.Capability{autonomy.CapabilityExternalWrite}

	sideEffectReadOnly            = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly}
	sideEffectAdditive            = []autonomy.SideEffectClass{autonomy.SideEffectAdditive}
	sideEffectUnknown             = []autonomy.SideEffectClass{autonomy.SideEffectUnknown}
	sideEffectReadOnlyUnknown     = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectUnknown}
	sideEffectReadOnlyAdditive    = []autonomy.SideEffectClass{autonomy.SideEffectReadOnly, autonomy.SideEffectAdditive}
	sideEffectAdditiveDestructive = []autonomy.SideEffectClass{autonomy.SideEffectAdditive, autonomy.SideEffectDestructive}
)

const (
	directCapabilityWorkspaceOrientation = "workspace orientation is a direct filesystem read, not an exact stable capability route"
	directCapabilityPrimitiveSearch      = "primitive search returns raw path or text matches without the semantics promised by a stable capability route"
	directCapabilityFileInspection       = "file inspection is a direct filesystem contract, not symbol or file-outline capability evidence"
	directCapabilityVerification         = "verification policy is intentionally outside the read-only discovery resolver"
	directCapabilityDomainReadModel      = "domain status is an owner-specific read model, not a generic read-only capability"
	directCapabilityHistory              = "version-control history has no exact stable read-only capability route"
	directCapabilitySecurityScan         = "security scanning has no exact stable read-only capability route"
	directCapabilityVersionControl       = "version-control workspace state has no exact stable read-only capability route"
)

type toolSurfaceFacet string

const (
	toolSurfaceFacetReadClass                  toolSurfaceFacet = "read_class"
	toolSurfaceFacetRuntimeContentInspection   toolSurfaceFacet = "runtime_content_inspection"
	toolSurfaceFacetRuntimeSearchGuidance      toolSurfaceFacet = "runtime_search_guidance"
	toolSurfaceFacetRuntimeOrientationGuidance toolSurfaceFacet = "runtime_orientation_guidance"
	toolSurfaceFacetSourceBodyInspection       toolSurfaceFacet = "source_body_inspection"
	toolSurfaceFacetTaskLedgerReadEvidence     toolSurfaceFacet = "task_ledger_read_evidence"
	toolSurfaceFacetPathAttachesInstructions   toolSurfaceFacet = "path_attaches_instructions"
	toolSurfaceFacetBroadDiscovery             toolSurfaceFacet = "broad_discovery"
	toolSurfaceFacetDirectEditAdapter          toolSurfaceFacet = "direct_edit_adapter"
)

type toolSurfaceHandler func(*Manager, context.Context, ToolRequest) ToolResult

type toolSurfaceSpec struct {
	Tool                           string
	Handler                        toolSurfaceHandler
	Language                       string
	Category                       ToolCategory
	ActionClass                    autonomy.ActionClass
	DefaultManifest                bool
	Narrowing                      bool
	Facets                         []toolSurfaceFacet
	RuntimeNarrowFamily            string
	EvidenceCapabilities           []string
	CapabilityClassification       string
	DirectReadOnlyCapabilityReason string
	PolicyCapabilities             []autonomy.Capability
	SideEffects                    []autonomy.SideEffectClass
	PlanExecutePlanner             bool
	MCPDiscoveryProfile            bool
	DiscoveryLocalArtifacts        bool
	StaleContextErrors             bool
	MCPDiscoveryReadModel          bool
	RuntimeSourceInspection        bool
	RuntimeContextDiscovery        bool
	RuntimeHistoryInspection       bool
	Timeout                        time.Duration
}

type toolSurfaceCatalog struct {
	specs  []toolSurfaceSpec
	byTool map[string]int
}

func mustNewToolSurfaceCatalog(specs []toolSurfaceSpec) toolSurfaceCatalog {
	out := make([]toolSurfaceSpec, 0, len(specs))
	byTool := make(map[string]int, len(specs))

	for _, spec := range specs {
		tool := strings.TrimSpace(spec.Tool)
		if tool == "" {
			panic("invalid built-in tool surface catalog: tool name is required")
		}
		if _, exists := byTool[tool]; exists {
			panic("invalid built-in tool surface catalog: duplicate tool " + tool)
		}

		cloned := cloneToolSurfaceSpec(spec)
		cloned.Tool = tool
		byTool[tool] = len(out)
		out = append(out, cloned)
	}

	return toolSurfaceCatalog{specs: out, byTool: byTool}
}

func (c toolSurfaceCatalog) specsSnapshot() []toolSurfaceSpec {
	out := make([]toolSurfaceSpec, len(c.specs))
	for i, spec := range c.specs {
		out[i] = cloneToolSurfaceSpec(spec)
	}

	return out
}

func (c toolSurfaceCatalog) lookup(tool string) (toolSurfaceSpec, bool) {
	index, ok := c.byTool[strings.TrimSpace(tool)]
	if !ok {
		return toolSurfaceSpec{}, false
	}

	return cloneToolSurfaceSpec(c.specs[index]), true
}

func (c toolSurfaceCatalog) handler(tool string) (toolSurfaceHandler, bool) {
	index, ok := c.byTool[strings.TrimSpace(tool)]
	if !ok {
		return nil, false
	}

	handler := c.specs[index].Handler
	if handler == nil {
		return nil, false
	}

	return handler, true
}

func (c toolSurfaceCatalog) toolsWhere(match func(toolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range c.specs {
		if match == nil || match(cloneToolSurfaceSpec(spec)) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}

func (spec toolSurfaceSpec) hasFacet(facet toolSurfaceFacet) bool {
	return slices.Contains(spec.Facets, facet)
}

func facets(values ...toolSurfaceFacet) []toolSurfaceFacet {
	return values
}

func cloneToolSurfaceSpec(spec toolSurfaceSpec) toolSurfaceSpec {
	spec.Facets = slices.Clone(spec.Facets)
	spec.EvidenceCapabilities = slices.Clone(spec.EvidenceCapabilities)
	spec.PolicyCapabilities = slices.Clone(spec.PolicyCapabilities)
	spec.SideEffects = slices.Clone(spec.SideEffects)

	return spec
}

var (
	builtInToolSurfaceCatalogOnce  sync.Once
	builtInToolSurfaceCatalogValue toolSurfaceCatalog
)

func builtInToolSurfaceCatalog() toolSurfaceCatalog {
	builtInToolSurfaceCatalogOnce.Do(func() {
		builtInToolSurfaceCatalogValue = newBuiltInToolSurfaceCatalog()
	})

	return builtInToolSurfaceCatalogValue
}

func newBuiltInToolSurfaceCatalog() toolSurfaceCatalog {
	return mustNewToolSurfaceCatalog([]toolSurfaceSpec{
		{Tool: ToolNamePwd, Handler: (*Manager).pwd, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityWorkspaceOrientation, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameListFiles, Handler: (*Manager).listFiles, Category: CategoryRead, ActionClass: autonomy.ActionClassSearch, DefaultManifest: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetRuntimeOrientationGuidance, toolSurfaceFacetBroadDiscovery), CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityWorkspaceOrientation, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameSearchFiles, Handler: (*Manager).searchFiles, Category: CategorySearch, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetRuntimeSearchGuidance, toolSurfaceFacetRuntimeOrientationGuidance), RuntimeNarrowFamily: "search", EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityPrimitiveSearch, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeSourceInspection: true},
		{Tool: ToolNameSearchContent, Handler: (*Manager).searchContent, Category: CategorySearch, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetRuntimeSearchGuidance, toolSurfaceFacetRuntimeOrientationGuidance, toolSurfaceFacetTaskLedgerReadEvidence), RuntimeNarrowFamily: "search", EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityPrimitiveSearch, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeSourceInspection: true},
		{Tool: ToolNameRg, Handler: (*Manager).searchContent, Category: CategorySearch, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetRuntimeSearchGuidance, toolSurfaceFacetRuntimeOrientationGuidance), RuntimeNarrowFamily: "search", EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityPrimitiveSearch, PolicyCapabilities: policyLiteralSearch, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
		{Tool: ToolNameReadFile, Handler: (*Manager).readFile, Category: CategoryRead, DefaultManifest: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetRuntimeOrientationGuidance, toolSurfaceFacetSourceBodyInspection, toolSurfaceFacetTaskLedgerReadEvidence, toolSurfaceFacetPathAttachesInstructions), RuntimeNarrowFamily: "read_file", EvidenceCapabilities: []string{EvidenceCapabilityInspectFile}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityFileInspection, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeSourceInspection: true},
		{Tool: ToolNameFileStats, Handler: (*Manager).fileStats, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityFileInspection, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeSourceInspection: true},
		{Tool: ToolNameCodeNavigate, Handler: (*Manager).executeLanguageTool, Category: CategoryRead, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetTaskLedgerReadEvidence, toolSurfaceFacetPathAttachesInstructions), RuntimeNarrowFamily: "code_navigate", EvidenceCapabilities: CodeNavigationEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeSourceInspection: true},
		{Tool: ToolNameReadOnlyCapability, Handler: (*Manager).readOnlyCapability, Category: CategoryRead, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetRuntimeContentInspection, toolSurfaceFacetPathAttachesInstructions), EvidenceCapabilities: ReadOnlyCapabilityEvidenceCapabilities(), CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameSourceSanity, Handler: (*Manager).sourceSanity, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetPathAttachesInstructions), EvidenceCapabilities: []string{EvidenceCapabilitySourceDiagnostics, EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnlyUnknown, RuntimeSourceInspection: true},
		{Tool: ToolNameCodingRulesCheck, Handler: (*Manager).codingRulesCheck, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilitySourceDiagnostics, EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, DirectReadOnlyCapabilityReason: directCapabilityVerification, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectReadOnly, RuntimeSourceInspection: true},
		{Tool: ToolNameHarnessAudit, Handler: (*Manager).harnessAudit, Category: CategoryRead, ActionClass: autonomy.ActionClassSearch, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass), EvidenceCapabilities: []string{EvidenceCapabilityAuditBehavior}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameHarnessFeedback, Handler: (*Manager).harnessFeedback, Category: CategoryVerification, DefaultManifest: true, Narrowing: true, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, PlanExecutePlanner: true},
		{Tool: ToolNameRecoveryInspect, Handler: (*Manager).recoveryInspect, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityDomainReadModel, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryProfile: true},
		{Tool: ToolNameRecoveryStatus, Handler: (*Manager).recoveryStatus, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityDomainReadModel, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryProfile: true},
		{Tool: ToolNameRecoveryDecompose, Handler: (*Manager).recoveryDecompose, Category: CategoryVerification, DefaultManifest: true, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
		{Tool: ToolNameProductEfforts, Handler: (*Manager).productEfforts, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityDomainReadModel, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryReadModel: true},
		{Tool: ToolNameProductStatus, Handler: (*Manager).productStatus, Category: CategoryRead, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityDomainReadModel, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryReadModel: true},
		{Tool: ToolNameContextIndex, Handler: (*Manager).contextIndex, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectAdditive, StaleContextErrors: true, Timeout: 5 * time.Minute},
		{Tool: ToolNameContextStatus, Handler: (*Manager).contextStatus, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
		{Tool: ToolNameContextQuery, Handler: (*Manager).contextQuery, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetTaskLedgerReadEvidence), EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true, Timeout: 90 * time.Second},
		{Tool: ToolNameContextZoom, Handler: (*Manager).contextZoom, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetTaskLedgerReadEvidence), EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange, EvidenceCapabilityQueryContext}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, MCPDiscoveryProfile: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
		{Tool: ToolNameContextPack, Handler: (*Manager).contextPack, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetTaskLedgerReadEvidence), EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
		{Tool: ToolNameContextImpact, Handler: (*Manager).contextImpact, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
		{Tool: ToolNameContextTracePack, Handler: (*Manager).contextTracePack, Category: CategoryDiscovery, ActionClass: autonomy.ActionClassVerify, DefaultManifest: true, Narrowing: true, EvidenceCapabilities: []string{EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown, StaleContextErrors: true, Timeout: 5 * time.Minute},
		{Tool: ToolNameDiscoveryQuery, Handler: (*Manager).discoveryQuery, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetTaskLedgerReadEvidence, toolSurfaceFacetBroadDiscovery), EvidenceCapabilities: []string{EvidenceCapabilityLocalizeChange}, CapabilityClassification: CapabilityClassRouted, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, DiscoveryLocalArtifacts: true, StaleContextErrors: true, RuntimeContextDiscovery: true, Timeout: 90 * time.Second},
		{Tool: ToolNameStructuralSearch, Handler: (*Manager).structuralSearch, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass), EvidenceCapabilities: []string{EvidenceCapabilityFindReferences}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityPrimitiveSearch, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, StaleContextErrors: true, RuntimeContextDiscovery: true},
		{Tool: ToolNameDependencyGraph, Handler: (*Manager).dependencyGraph, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetBroadDiscovery), EvidenceCapabilities: []string{EvidenceCapabilityRelatedFiles}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, StaleContextErrors: true},
		{Tool: ToolNameHistoryRange, Handler: (*Manager).historyRange, Category: CategoryDiscovery, ActionClass: autonomy.ActionClassRead, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass), EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityHistory, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeHistoryInspection: true},
		{Tool: ToolNameHistorySymbol, Handler: (*Manager).historySymbol, Category: CategoryDiscovery, ActionClass: autonomy.ActionClassRead, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass), EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityHistory, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeHistoryInspection: true},
		{Tool: ToolNameBlameRange, Handler: (*Manager).blameRange, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass), EvidenceCapabilities: []string{EvidenceCapabilityHistoryContext}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilityHistory, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, PlanExecutePlanner: true, RuntimeHistoryInspection: true},
		{Tool: ToolNameCtagsSymbols, Handler: (*Manager).ctagsSymbols, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetBroadDiscovery), EvidenceCapabilities: []string{EvidenceCapabilityFindSymbol}, CapabilityClassification: CapabilityClassFallback, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 90 * time.Second},
		{Tool: ToolNameSemgrepScan, Handler: (*Manager).semgrepScan, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetBroadDiscovery), EvidenceCapabilities: []string{EvidenceCapabilitySecurityScan}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilitySecurityScan, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
		{Tool: ToolNameJoernScan, Handler: (*Manager).joernScan, Category: CategoryDiscovery, DefaultManifest: true, Narrowing: true, Facets: facets(toolSurfaceFacetReadClass, toolSurfaceFacetBroadDiscovery), EvidenceCapabilities: []string{EvidenceCapabilitySecurityScan}, CapabilityClassification: CapabilityClassFallback, DirectReadOnlyCapabilityReason: directCapabilitySecurityScan, PolicyCapabilities: policySemanticDiscovery, SideEffects: sideEffectReadOnly, Timeout: 5 * time.Minute},
		{Tool: ToolNameGitStatus, Handler: (*Manager).gitStatus, Category: CategoryShell, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityVersionControl, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameGitDiff, Handler: (*Manager).gitDiff, Category: CategoryShell, DefaultManifest: true, CapabilityClassification: CapabilityClassDirect, DirectReadOnlyCapabilityReason: directCapabilityVersionControl, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnly},
		{Tool: ToolNameShell, Handler: (*Manager).runShell, Category: CategoryShell, DefaultManifest: true, RuntimeNarrowFamily: "shell", EvidenceCapabilities: []string{EvidenceCapabilityVerifyEdit}, CapabilityClassification: CapabilityClassDeferred, PolicyCapabilities: policyMutatingShell, SideEffects: sideEffectUnknown},
		{Tool: ToolNameSystemSearchAccess, Handler: (*Manager).requestSystemSearchAccess, Category: CategoryVerification, ActionClass: autonomy.ActionClassGenericProbe, DefaultManifest: true, PolicyCapabilities: policyBoundedRead, SideEffects: sideEffectReadOnlyAdditive, PlanExecutePlanner: true},
		{Tool: ToolNameTaskLedger, PolicyCapabilities: policyExternalWrite, SideEffects: sideEffectAdditive},
		{Tool: ToolNameTask, ActionClass: autonomy.ActionClassGenericProbe, PolicyCapabilities: policyDelegation, SideEffects: sideEffectReadOnlyAdditive},
		{Tool: ToolNameEditFile, Handler: (*Manager).editFile, Category: CategoryWrite, Facets: facets(toolSurfaceFacetPathAttachesInstructions, toolSurfaceFacetDirectEditAdapter), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
		{Tool: ToolNameWriteFile, Handler: (*Manager).writeFile, Category: CategoryWrite, Facets: facets(toolSurfaceFacetPathAttachesInstructions, toolSurfaceFacetDirectEditAdapter), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
		{Tool: ToolNameAppendFile, Handler: (*Manager).writeFile, Category: CategoryWrite, Facets: facets(toolSurfaceFacetPathAttachesInstructions, toolSurfaceFacetDirectEditAdapter), PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
		{Tool: ToolNameApplyPatch, Handler: (*Manager).applyPatch, Category: CategoryWrite, Facets: facets(toolSurfaceFacetDirectEditAdapter), PolicyCapabilities: policyApplyPatch, SideEffects: sideEffectAdditiveDestructive},
		{Tool: ToolNameMkdir, Handler: (*Manager).makeDir, Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
		{Tool: ToolNameGoMoveDeclarations, Handler: (*Manager).executeLanguageTool, Language: "go", Category: CategoryWrite, PolicyCapabilities: policySourceEdit, SideEffects: sideEffectAdditive},
		{Tool: ToolNameSubmitPlan, Handler: (*Manager).submitPlan, Category: CategoryVerification, ActionClass: autonomy.ActionClassSubmitPlan, CapabilityClassification: CapabilityClassWriteSideEffect, PolicyCapabilities: policyPlanSubmission, SideEffects: sideEffectAdditive, PlanExecutePlanner: true},
		{Tool: ToolNameGoDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
		{Tool: ToolNameCppDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
		{Tool: ToolNamePythonDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
		{Tool: ToolNameRustDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
		{Tool: ToolNameJavaDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
		{Tool: ToolNameTsDiagnostics, Category: CategoryRead, ActionClass: autonomy.ActionClassVerify, DefaultManifest: false, PolicyCapabilities: policyShellVerification, SideEffects: sideEffectUnknown},
	})
}

func builtInToolSurfaceToolsByCategory(category ToolCategory) []string {
	return builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.Category == category
	})
}

func builtInToolSurfaceEvidenceCapabilities(tool string) []string {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok || len(spec.EvidenceCapabilities) == 0 {
		return nil
	}

	return slices.Clone(spec.EvidenceCapabilities)
}

func builtInToolSurfaceHasFacet(tool string, facet toolSurfaceFacet) bool {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	return ok && spec.hasFacet(facet)
}

func builtInToolSurfaceToolsByFacet(facet toolSurfaceFacet) []string {
	return builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.hasFacet(facet)
	})
}

func builtInToolSurfaceRuntimeNarrowFamily(tool string) string {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok {
		return ""
	}

	return strings.TrimSpace(spec.RuntimeNarrowFamily)
}

func builtInToolSurfacePolicyCapabilities(tool string) []autonomy.Capability {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok || len(spec.PolicyCapabilities) == 0 {
		return nil
	}

	return slices.Clone(spec.PolicyCapabilities)
}

func ToolPolicyCapabilities(tool string) []autonomy.Capability {
	if capabilities := builtInToolSurfacePolicyCapabilities(tool); len(capabilities) > 0 {
		return capabilities
	}

	if IsSearchToolName(tool) {
		return []autonomy.Capability{autonomy.CapabilityLiteralSearch}
	}

	if languagetools.IsToolName(tool) {
		return []autonomy.Capability{autonomy.CapabilitySemanticDiscovery}
	}

	return nil
}

// PlanExecutePlannerToolMetadata describes planner-facing capabilities for a
// built-in workspace tool as projected into the plan-execute contract compiler.
type PlanExecutePlannerToolMetadata struct {
	Tool           string
	Capabilities   []autonomy.Capability
	SideEffect     autonomy.SideEffectClass
	PlannerAllowed bool
}

func PlanExecutePlannerToolMetadataEntries() []PlanExecutePlannerToolMetadata {
	specs := builtInToolSurfaceCatalog().specsSnapshot()
	entries := make([]PlanExecutePlannerToolMetadata, 0, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Tool) == "" {
			continue
		}
		entries = append(entries, planExecutePlannerToolMetadataFromSpec(spec))
	}

	slices.SortFunc(entries, func(a, b PlanExecutePlannerToolMetadata) int {
		return strings.Compare(a.Tool, b.Tool)
	})

	return entries
}

func PlanExecutePlannerToolNames() []string {
	var tools []string
	for _, entry := range PlanExecutePlannerToolMetadataEntries() {
		if entry.PlannerAllowed {
			tools = append(tools, entry.Tool)
		}
	}

	return tools
}

func PlanExecutePlannerProjectionTools(groups ...[]string) []string {
	var tools []string

	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" || !IsPlanExecutePlannerToolAllowed(value) {
				continue
			}

			tools = append(tools, value)
		}
	}

	return textutil.Unique(tools)
}

func PlanExecuteKnownPlannerToolNames(values []string) []string {
	known := make(map[string]struct{})
	for _, entry := range PlanExecutePlannerToolMetadataEntries() {
		known[strings.TrimSpace(entry.Tool)] = struct{}{}
	}

	var tools []string

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		if _, ok := known[value]; !ok {
			continue
		}

		tools = append(tools, value)
	}

	return textutil.Unique(tools)
}

func LookupPlanExecutePlannerToolMetadata(tool string) (PlanExecutePlannerToolMetadata, bool) {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok {
		return PlanExecutePlannerToolMetadata{}, false
	}

	return planExecutePlannerToolMetadataFromSpec(spec), true
}

func IsPlanExecutePlannerToolAllowed(tool string) bool {
	entry, ok := LookupPlanExecutePlannerToolMetadata(tool)
	return ok && entry.PlannerAllowed
}

func planExecutePlannerToolMetadataFromSpec(spec toolSurfaceSpec) PlanExecutePlannerToolMetadata {
	return PlanExecutePlannerToolMetadata{
		Tool:           strings.TrimSpace(spec.Tool),
		Capabilities:   primaryPlanExecuteCapabilities(spec.PolicyCapabilities),
		SideEffect:     primaryPlanExecuteSideEffect(spec.SideEffects),
		PlannerAllowed: spec.PlanExecutePlanner,
	}
}

func primaryPlanExecuteCapabilities(capabilities []autonomy.Capability) []autonomy.Capability {
	if len(capabilities) == 0 {
		return nil
	}

	return []autonomy.Capability{capabilities[0]}
}

func primaryPlanExecuteSideEffect(sideEffects []autonomy.SideEffectClass) autonomy.SideEffectClass {
	for _, candidate := range []autonomy.SideEffectClass{
		autonomy.SideEffectAdditive,
		autonomy.SideEffectUnknown,
		autonomy.SideEffectDestructive,
		autonomy.SideEffectOpenWorld,
		autonomy.SideEffectReadOnly,
	} {
		if slices.Contains(sideEffects, candidate) {
			return candidate
		}
	}

	return autonomy.SideEffectUnknown
}

func builtInToolSurfaceSideEffects(tool string) []autonomy.SideEffectClass {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok || len(spec.SideEffects) == 0 {
		return nil
	}

	return slices.Clone(spec.SideEffects)
}

func builtInToolSurfaceMCPDiscoveryProfileTool(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	return ok && spec.MCPDiscoveryProfile
}

func builtInToolSurfaceDiscoveryLocalArtifacts(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	return ok && spec.DiscoveryLocalArtifacts
}

func builtInToolSurfaceStaleContextErrors(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	return ok && spec.StaleContextErrors
}

func builtInToolSurfaceMCPDiscoveryReadModelTool(tool string) bool {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	return ok && spec.MCPDiscoveryReadModel
}

func RuntimeSourceInspectionToolNames() []string {
	return builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeSourceInspection
	})
}

func RuntimeContextDiscoveryToolNames() []string {
	return builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeContextDiscovery
	})
}

func RuntimeHistoryInspectionToolNames() []string {
	return builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		return spec.RuntimeHistoryInspection
	})
}

func RuntimeReadOnlyInvestigationToolNames() []string {
	tools := builtInToolSurfaceCatalog().toolsWhere(func(spec toolSurfaceSpec) bool {
		if spec.Tool == ToolNameTask {
			return true
		}
		if spec.Tool == ToolNameSystemSearchAccess {
			return true
		}

		if spec.DiscoveryLocalArtifacts || !slices.Equal(spec.SideEffects, sideEffectReadOnly) {
			return false
		}

		actionClass := ToolActionClass(spec.Tool)
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
