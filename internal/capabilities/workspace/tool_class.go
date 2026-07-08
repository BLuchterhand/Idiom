package workspace

import (
	"path/filepath"
	"strings"

	"idiom/internal/contracts/autonomy"
	semanticlanguage "idiom/internal/repository/semantic/language"
)

const legacyGrepToolName = "grep"

// ToolCategory classifies workspace tools into broad capability groups
// for gating, recovery-card rendering, and autonomy recording.
type ToolCategory int

const (
	CategoryUnknown ToolCategory = iota
	CategoryRead
	CategoryWrite
	CategorySearch
	CategoryDiscovery
	CategoryVerification
	CategoryShell
	CategoryMCP
)

// ClassifyTool returns the ToolCategory for a given tool name.
func ClassifyTool(name string) ToolCategory {
	if metadata, ok := builtInToolSurfaceCatalog.lookup(name); ok && ToolCategory(metadata.Category) != CategoryUnknown {
		return ToolCategory(metadata.Category)
	}

	if IsLanguageToolName(name) {
		return CategoryRead
	}

	if len(name) > 4 && name[:4] == "mcp:" {
		return CategoryMCP
	}

	return CategoryUnknown
}

// IsReadTool reports whether name is a read, search, or discovery tool.
func IsReadTool(name string) bool {
	c := ClassifyTool(name)
	return c == CategoryRead || c == CategorySearch || c == CategoryDiscovery
}

// IsSearchToolName reports whether name is a search tool result. It includes
// the legacy "grep" result name because old transcripts and evidence records
// can still contain it even though current workspace requests use rg/search_content.
func IsSearchToolName(name string) bool {
	name = strings.TrimSpace(name)
	return ClassifyTool(name) == CategorySearch || name == legacyGrepToolName
}

// IsTextSearchToolName reports whether search output contains text matches
// that can be broad or truncated. File-name searches are intentionally excluded.
func IsTextSearchToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameSearchContent, ToolNameRg, legacyGrepToolName:
		return true
	default:
		return false
	}
}

// ToolActionClass returns the canonical autonomy action class for a workspace
// tool name. The workspace catalog owns this projection because it owns tool
// categories, language-tool registration, and built-in tool metadata.
func ToolActionClass(tool string) autonomy.ActionClass {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ""
	}

	if metadata, ok := builtInToolSurfaceCatalog.lookup(tool); ok {
		if metadata.ActionClass != "" {
			return autonomy.CanonicalActionClass(metadata.ActionClass)
		}

		switch ToolCategory(metadata.Category) {
		case CategoryRead:
			return autonomy.ActionClassRead
		case CategorySearch, CategoryDiscovery:
			return autonomy.ActionClassSearch
		case CategoryWrite:
			return autonomy.ActionClassPerEditWrite
		case CategoryVerification, CategoryShell:
			return autonomy.ActionClassVerify
		case CategoryMCP:
			return autonomy.ActionClassGenericProbe
		}
	}

	if IsLanguageToolName(tool) {
		return autonomy.ActionClassRead
	}

	if strings.HasPrefix(tool, "mcp:") {
		return autonomy.ActionClassGenericProbe
	}

	return ""
}

// IsImplementationAttemptTool reports whether a tool-call name is enough
// evidence that an implementation lane attempted a write-capable action.
func IsImplementationAttemptTool(tool string) bool {
	switch ToolActionClass(tool) {
	case autonomy.ActionClassPerEditWrite, autonomy.ActionClassSubmitPlan:
		return true
	default:
		return false
	}
}

// ToolRequestActionClass classifies a concrete tool request. It refines the
// catalog-level class with request shape for shell verification, scoped
// list-files reads, and deliverable writes.
func ToolRequestActionClass(req ToolRequest) autonomy.ActionClass {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case ToolNameShell:
		if AnalyzeShellCommand(req.Command).SideEffect == autonomy.SideEffectReadOnly {
			return autonomy.ActionClassVerify
		}

		return autonomy.ActionClassGenericProbe
	case ToolNameTask:
		return autonomy.ActionClassDelegate
	case ToolNameListFiles:
		if IsScopedInspectionRequest(req) {
			return autonomy.ActionClassRead
		}
	case ToolNameWriteFile:
		if strings.TrimSpace(req.Path) != "" && !looksLikeSourcePath(req.Path) {
			return autonomy.ActionClassDeliverableWrite
		}
	}

	return ToolActionClass(tool)
}

func looksLikeSourcePath(path string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".c", ".cc", ".cpp", ".cs", ".go", ".h", ".hpp", ".java", ".js", ".jsx", ".kt", ".kts", ".php", ".py", ".rb", ".rs", ".swift", ".ts", ".tsx":
		return true
	default:
		return false
	}
}

// IsReadClassToolName reports whether name is a read-class tool that narrows
// workspace evidence after readiness. This is intentionally narrower than
// IsReadTool: broad context-pack/status tools are discovery setup, while this
// class covers follow-up reads/searches that must explain the exact remaining
// evidence gap.
func IsReadClassToolName(name string) bool {
	name = strings.TrimSpace(name)
	if builtInToolSurfaceHasFacet(name, toolSurfaceFacetReadClass) {
		return true
	}

	return IsLanguageReadClassToolName(name)
}

func IsLanguageReadClassToolName(name string) bool {
	return LanguageToolHasAnyCapability(name,
		semanticlanguage.CapReadSymbol,
		semanticlanguage.CapFileOutline,
		semanticlanguage.CapExtractBody,
		semanticlanguage.CapWorkspaceSymbol,
		semanticlanguage.CapReferences,
		semanticlanguage.CapDefinition,
		semanticlanguage.CapCallsFrom,
		semanticlanguage.CapCallsInto,
		semanticlanguage.CapRelatedFiles,
	)
}

// IsRuntimeContentInspectionTool reports whether a tool actually inspects or
// narrows source content. Runtime uses this to distinguish content inspection
// from broad directory listing loops.
func IsRuntimeContentInspectionTool(name string) bool {
	name = strings.TrimSpace(name)
	if builtInToolSurfaceHasFacet(name, toolSurfaceFacetRuntimeContentInspection) {
		return true
	}

	return languageToolHasAnyEvidenceCapability(name,
		EvidenceCapabilityFindSymbol,
		EvidenceCapabilityInspectSymbol,
		EvidenceCapabilityFindDefinition,
		EvidenceCapabilityFindReferences,
		EvidenceCapabilityFindImplementations,
		EvidenceCapabilityCallsFrom,
		EvidenceCapabilityCallsInto,
		EvidenceCapabilityRelatedFiles,
		EvidenceCapabilitySourceDiagnostics,
	)
}

// IsRuntimeSearchGuidanceTool reports whether a successful result is a
// search-like narrowing step whose next action should usually read or inspect
// the highest-confidence hit.
func IsRuntimeSearchGuidanceTool(name string) bool {
	name = strings.TrimSpace(name)
	if builtInToolSurfaceHasFacet(name, toolSurfaceFacetRuntimeSearchGuidance) {
		return true
	}

	return languageToolHasAnyEvidenceCapability(name,
		EvidenceCapabilityFindSymbol,
		EvidenceCapabilityFindDefinition,
		EvidenceCapabilityFindReferences,
		EvidenceCapabilityFindImplementations,
		EvidenceCapabilityCallsFrom,
		EvidenceCapabilityCallsInto,
		EvidenceCapabilityRelatedFiles,
	)
}

// IsRuntimeOrientationGuidanceTool reports whether a just-finished tool was a
// broad orientation/search step for runtime next-action guidance. Structured
// symbol tools are intentionally excluded because they already return the
// evidence that relational queries usually need.
func IsRuntimeOrientationGuidanceTool(name string) bool {
	return builtInToolSurfaceHasFacet(name, toolSurfaceFacetRuntimeOrientationGuidance)
}

// IsSourceBodyInspectionTool reports whether a result from tool can provide a
// concrete source body or declaration boundary, as opposed to only search or
// directory evidence.
func IsSourceBodyInspectionTool(name string) bool {
	name = strings.TrimSpace(name)
	if builtInToolSurfaceHasFacet(name, toolSurfaceFacetSourceBodyInspection) {
		return true
	}

	return LanguageToolHasAnyCapability(name,
		semanticlanguage.CapReadSymbol,
		semanticlanguage.CapFileOutline,
		semanticlanguage.CapExtractBody,
	)
}

func IsLanguageSourceBodyTool(name string) bool {
	return LanguageToolHasAnyCapability(name, semanticlanguage.CapReadSymbol, semanticlanguage.CapExtractBody)
}

// TaskLedgerReadEvidenceTool reports whether a successful tool result can
// satisfy a task-ledger read evidence reference.
func TaskLedgerReadEvidenceTool(name string) bool {
	name = strings.TrimSpace(name)
	if builtInToolSurfaceHasFacet(name, toolSurfaceFacetTaskLedgerReadEvidence) {
		return true
	}

	return LanguageToolHasAnyCapability(name, semanticlanguage.CapReadSymbol, semanticlanguage.CapExtractBody, semanticlanguage.CapFileOutline)
}

// ToolRequestPathAttachesInstructions reports whether a request's Path should
// be used to surface nearby instruction files after the tool result.
func ToolRequestPathAttachesInstructions(tool string) bool {
	tool = strings.TrimSpace(tool)
	if builtInToolSurfaceHasFacet(tool, toolSurfaceFacetPathAttachesInstructions) {
		return true
	}

	return LanguageToolHasAnyCapability(tool,
		semanticlanguage.CapReadSymbol,
		semanticlanguage.CapFileOutline,
		semanticlanguage.CapExtractBody,
		semanticlanguage.CapDiagnostics,
	)
}

// BroadDiscoveryToolNames returns tools that orient broadly rather than inspect
// one concrete source location. Runtime uses this to hide broad probes once a
// child packet or readiness gate has supplied enough context.
func BroadDiscoveryToolNames() []string {
	tools := builtInToolSurfaceToolsByFacet(toolSurfaceFacetBroadDiscovery)

	for _, entry := range languageToolCatalog() {
		if entry.Capability == semanticlanguage.CapOverview || entry.Capability == semanticlanguage.CapEntrypoints {
			tools = append(tools, entry.Tool)
		}
	}

	return uniqueToolNames(tools)
}

func uniqueToolNames(tools []string) []string {
	seen := make(map[string]struct{}, len(tools))
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		out = append(out, tool)
	}

	return out
}

// RuntimeNarrowToolFamily returns the repeated-tool family used by runtime
// read-loop nudges. Broad discovery tools intentionally return an empty family.
func RuntimeNarrowToolFamily(name string) string {
	name = strings.TrimSpace(name)
	if family := builtInToolSurfaceRuntimeNarrowFamily(name); family != "" {
		return family
	}

	if languageToolHasAnyEvidenceCapability(name,
		EvidenceCapabilityFindSymbol,
		EvidenceCapabilityInspectSymbol,
		EvidenceCapabilityFindDefinition,
		EvidenceCapabilityFindReferences,
		EvidenceCapabilityFindImplementations,
		EvidenceCapabilityCallsFrom,
		EvidenceCapabilityCallsInto,
		EvidenceCapabilityRelatedFiles,
		EvidenceCapabilitySourceDiagnostics,
	) {
		return "code_navigate"
	}

	return ""
}

func languageToolHasAnyEvidenceCapability(tool string, capabilities ...string) bool {
	evidenceCapabilities := languageToolEvidenceCapabilities(tool)
	if len(evidenceCapabilities) == 0 {
		return false
	}

	for _, found := range evidenceCapabilities {
		for _, capability := range capabilities {
			if found == capability {
				return true
			}
		}
	}

	return false
}

// IsRuntimeInspectionRequest reports whether a request gathers or narrows
// workspace evidence rather than verifying, mutating, or updating harness state.
func IsRuntimeInspectionRequest(req ToolRequest) bool {
	if IsBroadInspectionRequest(req) {
		return true
	}

	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameShell || tool == ToolNameListFiles {
		return IsNarrowingRequest(req)
	}

	metadata, ok := builtInToolSurfaceCatalog.lookup(tool)
	if !ok {
		return IsLanguageToolName(tool)
	}

	actionClass := ToolActionClass(tool)
	if ToolCategory(metadata.Category) == CategoryWrite ||
		ToolCategory(metadata.Category) == CategoryVerification ||
		actionClass == autonomy.ActionClassVerify ||
		actionClass == autonomy.ActionClassPerEditWrite ||
		actionClass == autonomy.ActionClassSubmitPlan {

		return false
	}

	if metadata.RuntimeSourceInspection ||
		metadata.RuntimeContextDiscovery ||
		metadata.RuntimeHistoryInspection {

		return true
	}

	switch actionClass {
	case autonomy.ActionClassRead,
		autonomy.ActionClassSearch,
		autonomy.ActionClassGenericProbe:
		return true
	default:
		return false
	}
}
