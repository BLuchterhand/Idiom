package workspace

import (
	"fmt"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/repository/semantic/languagetools"
)

var _ = ginkgo.Describe("tool surface metadata", func() {
	ginkgo.It("built in tool surface catalog lookup returns cloned metadata", func() {
		first, ok := builtInToolSurfaceCatalog().lookup(ToolNameReadFile)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("catalog lookup missed %s", ToolNameReadFile))

		first.EvidenceCapabilities = append(first.EvidenceCapabilities, "mutated")
		first.PolicyCapabilities = append(first.PolicyCapabilities, autonomy.CapabilitySourceEdit)
		first.SideEffects = append(first.SideEffects, autonomy.SideEffectDestructive)
		first.Facets = append(first.Facets, toolSurfaceFacetBroadDiscovery)

		second, ok := builtInToolSurfaceCatalog().lookup(ToolNameReadFile)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("second catalog lookup missed %s", ToolNameReadFile))
		gomega.Expect(second.EvidenceCapabilities).NotTo(gomega.ContainElement("mutated"), fmt.Sprintf("catalog lookup leaked mutable slices: %#v", second))
		gomega.Expect(second.PolicyCapabilities).NotTo(gomega.ContainElement(autonomy.CapabilitySourceEdit), fmt.Sprintf("catalog lookup leaked mutable slices: %#v", second))
		gomega.Expect(second.SideEffects).NotTo(gomega.ContainElement(autonomy.SideEffectDestructive), fmt.Sprintf("catalog lookup leaked mutable slices: %#v", second))
		gomega.Expect(second.Facets).NotTo(gomega.ContainElement(toolSurfaceFacetBroadDiscovery), fmt.Sprintf("catalog lookup leaked mutable slices: %#v", second))

	})
	ginkgo.It("workspace tool surface lists project catalog", func() {
		gomega.Expect(ToolManifestTools(ToolManifestToolOptions{})).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.DefaultManifest
		})), "default manifest tools no longer project catalog metadata")
		gomega.Expect(WorkspaceWriteToolNames()).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.Category == CategoryWrite
		})), "workspace write tools no longer project catalog metadata")
		gomega.Expect(WorkspacePerEditWriteToolNames()).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.hasFacet(toolSurfaceFacetDirectEditAdapter)
		})), "workspace per-edit write tools no longer project direct edit-adapter metadata")
		gomega.Expect(RuntimeSourceInspectionToolNames()).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.RuntimeSourceInspection
		})), "runtime source inspection tools no longer project catalog metadata")
		gomega.Expect(RuntimeContextDiscoveryToolNames()).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.RuntimeContextDiscovery
		})), "runtime context discovery tools no longer project catalog metadata")
		gomega.Expect(RuntimeHistoryInspectionToolNames()).To(gomega.Equal(projectedCatalogTools(func(spec toolSurfaceSpec) bool {
			return spec.RuntimeHistoryInspection
		})), "runtime history inspection tools no longer project catalog metadata")

		broadDiscoveryBuiltIns := builtInToolSurfaceToolsByFacet(toolSurfaceFacetBroadDiscovery)
		for _, tool := range broadDiscoveryBuiltIns {
			gomega.Expect(slices.Contains(BroadDiscoveryToolNames(), tool)).To(gomega.BeTrue(), fmt.Sprintf("broad discovery built-ins no longer project catalog facet metadata: missing %s", tool))

		}
	})
	ginkgo.It("built in tool surface catalog owns manager dispatch handlers", func() {
		runtimeOwned := map[string]struct{}{
			ToolNameTaskLedger: {},
			ToolNameTask:       {},
		}

		for _, spec := range builtInToolSurfaceCatalog().specsSnapshot() {
			if spec.Handler != nil {
				continue
			}
			if _, ok := runtimeOwned[spec.Tool]; ok {
				continue
			}
			if languagetools.IsToolName(spec.Tool) {
				continue
			}
			ginkgo.Fail(fmt.Sprintf("built-in manager-dispatched tool %q has no catalog handler", spec.Tool))
		}

		for tool := range runtimeOwned {
			spec, ok := builtInToolSurfaceCatalog().lookup(tool)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("runtime-owned tool %q missing from catalog", tool))
			gomega.Expect(spec.Handler).To(gomega.BeNil(), fmt.Sprintf("runtime-owned tool %q must not be manager-dispatched", tool))

		}
	})
	ginkgo.It("workspace per edit write tool names owns direct write adapters", func() {
		tools := WorkspacePerEditWriteToolNames()
		want := []string{ToolNameEditFile, ToolNameWriteFile, ToolNameAppendFile, ToolNameApplyPatch}
		gomega.Expect(slices.Equal(tools, want)).To(gomega.BeTrue(), fmt.Sprintf("per-edit write tools = %#v, want %#v", tools, want))

		for _, tool := range tools {
			gomega.Expect(IsWorkspacePerEditWriteTool(tool)).To(gomega.BeTrue(), fmt.Sprintf("per-edit write predicate rejected %q", tool))
			gomega.Expect(IsWorkspaceWriteTool(tool)).To(gomega.BeTrue(), fmt.Sprintf("per-edit write tool %q is not in the workspace write catalog", tool))

		}

		for _, broaderWrite := range []string{ToolNameMkdir, ToolNameGoMoveDeclarations} {
			gomega.Expect(IsWorkspacePerEditWriteTool(broaderWrite)).To(gomega.BeFalse(), fmt.Sprintf("broader write tool %q must not satisfy per-edit write predicate", broaderWrite))

		}
	})
	ginkgo.It("explore mode write block uses workspace write catalog", func() {
		writeTools := WorkspaceWriteToolNames()
		gomega.Expect(writeTools).NotTo(gomega.BeEmpty(), "workspace write tool catalog is empty")

		for _, tool := range writeTools {
			reason, blocked := exploreModeWriteBlocked(tool, ModeExplore)
			gomega.Expect(blocked).To(gomega.BeTrue(), fmt.Sprintf("explore mode did not block cataloged write tool %q", tool))
			gomega.Expect(reason).To(gomega.ContainSubstring("explore mode is read-only"), fmt.Sprintf("explore mode rejection for %q lost read-only reason: %q", tool, reason))

		}

		for _, tool := range []string{ToolNameReadFile, ToolNameSearchContent, ToolNameSubmitPlan} {
			if reason, blocked := exploreModeWriteBlocked(tool, ModeExplore); blocked {
				ginkgo.Fail(fmt.Sprintf("explore mode should not block non-workspace-write tool %q at dispatch gate: %q", tool, reason))
			}
		}
	})
	ginkgo.It("runtime read only investigation tool names keeps general lane diagnostics", func() {
		tools := RuntimeReadOnlyInvestigationToolNames()
		for _, want := range []string{
			ToolNameReadFile,
			ToolNameSearchFiles,
			ToolNameSearchContent,
			ToolNameReadOnlyCapability,
			ToolNameHarnessAudit,
			ToolNameContextStatus,
			ToolNameContextQuery,
			ToolNameContextZoom,
			ToolNameStructuralSearch,
			ToolNameHistoryRange,
			ToolNameHistorySymbol,
			ToolNameBlameRange,
			ToolNameSystemSearchAccess,
			ToolNameTask,
			ToolNameMCPWebFetchExa,
			ToolNameMCPWebSearchExa,
		} {
			gomega.Expect(slices.Contains(tools, want)).To(gomega.BeTrue(), fmt.Sprintf("read-only investigation projection missing %s: %#v", want, tools))

		}

		for _, forbidden := range []string{
			ToolNameSourceSanity,
			ToolNameContextPack,
			ToolNameContextImpact,
			ToolNameContextTracePack,
			ToolNameDiscoveryQuery,
			ToolNameSubmitPlan,
			ToolNameShell,
			ToolNameEditFile,
			ToolNameWriteFile,
			ToolNameApplyPatch,
		} {
			gomega.Expect(slices.Contains(tools, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("read-only investigation projection exposed non-investigation tool %s: %#v", forbidden, tools))

		}
	})
	ginkgo.It("runtime read only investigation tool action classes use workspace projection", func() {
		for _, tool := range RuntimeReadOnlyInvestigationToolNames() {
			if strings.HasPrefix(tool, "mcp:") {
				continue
			}

			switch got := ToolActionClass(tool); got {
			case autonomy.ActionClassRead, autonomy.ActionClassSearch, autonomy.ActionClassGenericProbe, autonomy.ActionClassDelegate:
			default:
				ginkgo.Fail(fmt.Sprintf("%s is in read-only investigation projection with non-investigation action class %q", tool, got))
			}
		}
	})
	ginkgo.It("runtime read only investigation tool names allows only read approval additive effect", func() {
		for _, tool := range RuntimeReadOnlyInvestigationToolNames() {
			if strings.HasPrefix(tool, "mcp:") || tool == ToolNameTask {
				continue
			}

			spec, ok := builtInToolSurfaceCatalog().lookup(tool)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing metadata for %s", tool))

			if slices.Equal(spec.SideEffects, sideEffectReadOnly) {
				continue
			}
			if tool == ToolNameSystemSearchAccess && slices.Equal(spec.SideEffects, sideEffectReadOnlyAdditive) {
				continue
			}
			ginkgo.Fail(fmt.Sprintf("read-only investigation projection exposed unexpected side effect for %s: %#v", tool, spec.SideEffects))
		}
	})
	ginkgo.It("tool policy capabilities uses workspace catalog", func() {
		readFile := ToolPolicyCapabilities(ToolNameReadFile)
		gomega.Expect(slices.Equal(readFile, []autonomy.Capability{autonomy.CapabilityBoundedRead})).To(gomega.BeTrue(), fmt.Sprintf("read_file capabilities = %#v, want bounded_read", readFile))

		rg := ToolPolicyCapabilities(ToolNameRg)
		gomega.Expect(slices.Equal(rg, []autonomy.Capability{autonomy.CapabilityLiteralSearch})).To(gomega.BeTrue(), fmt.Sprintf("rg capabilities = %#v, want literal_search", rg))

		grep := ToolPolicyCapabilities("grep")
		gomega.Expect(slices.Equal(grep, []autonomy.Capability{autonomy.CapabilityLiteralSearch})).To(gomega.BeTrue(), fmt.Sprintf("legacy grep capabilities = %#v, want literal_search", grep))

		languageTool := ToolPolicyCapabilities("go_read_symbol")
		gomega.Expect(slices.Equal(languageTool, []autonomy.Capability{autonomy.CapabilitySemanticDiscovery})).To(gomega.BeTrue(), fmt.Sprintf("language tool capabilities = %#v, want semantic_discovery", languageTool))

		readFile[0] = autonomy.CapabilitySourceEdit
		if again := ToolPolicyCapabilities(ToolNameReadFile); slices.Contains(again, autonomy.CapabilitySourceEdit) {
			ginkgo.Fail(fmt.Sprintf("ToolPolicyCapabilities leaked mutable catalog slice: %#v", again))
		}
	})
	ginkgo.It("plan execute planner tool metadata names allowed surface", func() {
		allowed := PlanExecutePlannerToolNames()
		for _, tc := range []struct {
			tool       string
			capability autonomy.Capability
			sideEffect autonomy.SideEffectClass
		}{
			{tool: ToolNameSearchFiles, capability: autonomy.CapabilityLiteralSearch, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameSearchContent, capability: autonomy.CapabilityLiteralSearch, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameReadFile, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameFileStats, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameCodeNavigate, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameHarnessFeedback, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectAdditive},
			{tool: ToolNameRecoveryInspect, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameRecoveryStatus, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameProductEfforts, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameProductStatus, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameContextStatus, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameContextQuery, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameContextZoom, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameContextPack, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameContextImpact, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameDiscoveryQuery, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameStructuralSearch, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameHistoryRange, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameHistorySymbol, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameBlameRange, capability: autonomy.CapabilitySemanticDiscovery, sideEffect: autonomy.SideEffectReadOnly},
			{tool: ToolNameSystemSearchAccess, capability: autonomy.CapabilityBoundedRead, sideEffect: autonomy.SideEffectAdditive},
			{tool: ToolNameSubmitPlan, capability: autonomy.CapabilityPlanSubmission, sideEffect: autonomy.SideEffectAdditive},
		} {
			gomega.Expect(slices.Contains(allowed, tc.tool)).To(gomega.BeTrue(), fmt.Sprintf("planner allowed tool names missing %q: %#v", tc.tool, allowed))

			entry, ok := LookupPlanExecutePlannerToolMetadata(tc.tool)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("planner metadata missing allowed tool %q", tc.tool))
			gomega.Expect(entry.PlannerAllowed).To(gomega.BeTrue(), fmt.Sprintf("planner metadata marks allowed tool %q as forbidden: %#v", tc.tool, entry))
			gomega.Expect(slices.Equal(entry.Capabilities, []autonomy.Capability{tc.capability})).To(gomega.BeTrue(), fmt.Sprintf("planner allowed tool %q capabilities = %#v, want %q", tc.tool, entry.Capabilities, tc.capability))
			gomega.Expect(entry.SideEffect).To(gomega.Equal(tc.sideEffect), fmt.Sprintf("planner allowed tool %q side effect = %q, want %q", tc.tool, entry.SideEffect, tc.sideEffect))

		}
	})
	ginkgo.It("plan execute planner tool metadata marks rejected planner tools", func() {
		for _, tool := range []string{
			ToolNameApplyPatch,
			ToolNameEditFile,
			ToolNameWriteFile,
			ToolNameAppendFile,
			ToolNameMkdir,
			ToolNameGoMoveDeclarations,
			ToolNameShell,
			ToolNameSourceSanity,
			ToolNameContextTracePack,
			ToolNameTask,
			ToolNameTaskLedger,
			ToolNameRecoveryDecompose,
			ToolNameGitDiff,
			ToolNameHarnessAudit,
		} {
			entry, ok := LookupPlanExecutePlannerToolMetadata(tool)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("planner metadata missing rejected tool %q", tool))
			gomega.Expect(entry.PlannerAllowed).To(gomega.BeFalse(), fmt.Sprintf("planner metadata allows rejected tool %q: %#v", tool, entry))
			gomega.Expect(slices.Contains(PlanExecutePlannerToolNames(), tool)).To(gomega.BeFalse(), fmt.Sprintf("planner allowed tool names contain rejected tool %q", tool))

		}
	})
	ginkgo.It("plan execute planner projection tools owns allowed filter", func() {
		got := PlanExecutePlannerProjectionTools(
			[]string{ToolNameReadFile, ToolNameEditFile, "", ToolNameSubmitPlan},
			[]string{ToolNameReadFile, ToolNameShell, ToolNameHistoryRange},
		)
		want := []string{ToolNameReadFile, ToolNameSubmitPlan, ToolNameHistoryRange}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("planner projection tools = %#v, want %#v", got, want))

	})
	ginkgo.It("plan execute known planner tool names preserves known rejected tools", func() {
		got := PlanExecuteKnownPlannerToolNames([]string{
			ToolNameEditFile,
			"not_a_tool",
			ToolNameSubmitPlan,
			ToolNameEditFile,
		})
		want := []string{ToolNameEditFile, ToolNameSubmitPlan}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("known planner tools = %#v, want %#v", got, want))

	})
	ginkgo.It("plan execute planner tool metadata returns defensive copies", func() {
		entries := PlanExecutePlannerToolMetadataEntries()
		gomega.Expect(entries).NotTo(gomega.BeEmpty(), fmt.Sprintf("planner metadata fixture unexpectedly empty: %#v", entries))
		gomega.Expect(entries[0].Capabilities).NotTo(gomega.BeEmpty(), fmt.Sprintf("planner metadata fixture unexpectedly empty: %#v", entries))

		submitPlanIndex := slices.IndexFunc(entries, func(entry PlanExecutePlannerToolMetadata) bool {
			return entry.Tool == ToolNameSubmitPlan
		})
		gomega.Expect(submitPlanIndex < 0).To(gomega.BeFalse(), fmt.Sprintf("planner metadata missing submit_plan: %#v", entries))

		entries[submitPlanIndex].Tool = "mutated"
		entries[submitPlanIndex].Capabilities[0] = autonomy.CapabilitySourceEdit

		freshEntryFromEntries := PlanExecutePlannerToolMetadataEntries()[submitPlanIndex]
		gomega.Expect(freshEntryFromEntries.Tool).NotTo(gomega.Equal("mutated"), fmt.Sprintf("planner metadata entries were mutated through caller-owned slice: %#v", freshEntryFromEntries))
		gomega.Expect(freshEntryFromEntries.Capabilities[0]).To(gomega.Equal(autonomy.CapabilityPlanSubmission), fmt.Sprintf("planner metadata entries were mutated through caller-owned slice: %#v", freshEntryFromEntries))

		entry, ok := LookupPlanExecutePlannerToolMetadata(ToolNameSubmitPlan)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("submit_plan metadata missing: %#v", entry))
		gomega.Expect(entry.Capabilities).NotTo(gomega.BeEmpty(), fmt.Sprintf("submit_plan metadata missing: %#v", entry))

		entry.Capabilities[0] = autonomy.CapabilitySourceEdit

		freshEntry, ok := LookupPlanExecutePlannerToolMetadata(ToolNameSubmitPlan)
		gomega.Expect(ok).To(gomega.BeTrue(), "submit_plan metadata missing after mutation")
		gomega.Expect(freshEntry.Capabilities[0]).To(gomega.Equal(autonomy.CapabilityPlanSubmission), fmt.Sprintf("lookup metadata was mutated through caller-owned slice: %#v", freshEntry))

	})
	ginkgo.It("workspace tool surface metadata keeps key semantic flags", func() {
		readFile, ok := builtInToolSurfaceCatalog().lookup(ToolNameReadFile)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing metadata for %s", ToolNameReadFile))
		gomega.Expect(readFile.Category).To(gomega.Equal(CategoryRead), fmt.Sprintf("read_file semantic flags drifted: %#v", readFile))
		gomega.Expect(readFile.DefaultManifest).To(gomega.BeTrue(), fmt.Sprintf("read_file semantic flags drifted: %#v", readFile))
		gomega.Expect(readFile.RuntimeSourceInspection).To(gomega.BeTrue(), fmt.Sprintf("read_file semantic flags drifted: %#v", readFile))
		gomega.Expect(slices.Equal(readFile.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityBoundedRead})).To(gomega.BeTrue(), fmt.Sprintf("read_file policy capabilities drifted: %#v", readFile.PolicyCapabilities))
		gomega.Expect(slices.Equal(readFile.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly})).To(gomega.BeTrue(), fmt.Sprintf("read_file side effects drifted: %#v", readFile.SideEffects))

		submitPlan, ok := builtInToolSurfaceCatalog().lookup(ToolNameSubmitPlan)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing metadata for %s", ToolNameSubmitPlan))
		gomega.Expect(submitPlan.Category).To(gomega.Equal(CategoryVerification), fmt.Sprintf("submit_plan semantic flags drifted: %#v", submitPlan))
		gomega.Expect(submitPlan.CapabilityClassification).To(gomega.Equal(CapabilityClassWriteSideEffect), fmt.Sprintf("submit_plan semantic flags drifted: %#v", submitPlan))
		gomega.Expect(slices.Equal(submitPlan.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilityPlanSubmission})).To(gomega.BeTrue(), fmt.Sprintf("submit_plan policy capabilities drifted: %#v", submitPlan.PolicyCapabilities))
		gomega.Expect(slices.Equal(submitPlan.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectAdditive})).To(gomega.BeTrue(), fmt.Sprintf("submit_plan side effects drifted: %#v", submitPlan.SideEffects))

		harnessAudit, ok := builtInToolSurfaceCatalog().lookup(ToolNameHarnessAudit)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing metadata for %s", ToolNameHarnessAudit))
		gomega.Expect(harnessAudit.Category).To(gomega.Equal(CategoryRead), fmt.Sprintf("harness_audit semantic flags drifted: %#v", harnessAudit))
		gomega.Expect(harnessAudit.CapabilityClassification).To(gomega.Equal(CapabilityClassRouted), fmt.Sprintf("harness_audit semantic flags drifted: %#v", harnessAudit))
		gomega.Expect(slices.Equal(harnessAudit.PolicyCapabilities, []autonomy.Capability{autonomy.CapabilitySemanticDiscovery})).To(gomega.BeTrue(), fmt.Sprintf("harness_audit policy capabilities drifted: %#v", harnessAudit.PolicyCapabilities))
		gomega.Expect(slices.Equal(harnessAudit.SideEffects, []autonomy.SideEffectClass{autonomy.SideEffectReadOnly})).To(gomega.BeTrue(), fmt.Sprintf("harness_audit side effects drifted: %#v", harnessAudit.SideEffects))

	})
})

func projectedCatalogTools(match func(toolSurfaceSpec) bool) []string {
	var tools []string

	for _, spec := range builtInToolSurfaceCatalog().specsSnapshot() {
		if match(spec) {
			tools = append(tools, spec.Tool)
		}
	}

	return tools
}
