package workspace

import (
	"path/filepath"
	"strings"

	"idiom/internal/platform/textutil"
)

type ToolDiscoveryProfile struct {
	Class     string `json:"class,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Cost      string `json:"cost,omitempty"`
	Discovery bool   `json:"discovery,omitempty"`
	Generic   bool   `json:"generic,omitempty"`
}

func DiscoveryProfileForRequest(req ToolRequest) ToolDiscoveryProfile {
	tool := strings.TrimSpace(req.Tool)
	switch tool {
	case ToolNamePwd:
		return discoveryProfile("workspace_context", "targeted", "low", false)
	case ToolNameListFiles:
		return discoveryProfile("workspace_listing", scopeFromPath(req.Path), "low", false)
	case ToolNameSearchFiles:
		return discoveryProfile("file_search", "targeted", "low", false)
	case ToolNameSearchContent, ToolNameRg:
		return discoveryProfile("text_search", "targeted", "low", false)
	case ToolNameReadFile:
		return discoveryProfile("file_read", "targeted", "low", false)
	case ToolNameCodeNavigate:
		return codeNavigationDiscoveryProfile(req)
	case ToolNameReadOnlyCapability:
		return readOnlyCapabilityDiscoveryProfile(req)
	case ToolNameSourceSanity:
		return discoveryProfile("diagnostics", "targeted", "medium", false)
	case ToolNameCodingRulesCheck:
		return discoveryProfile("coding_rules", scopeFromPath(req.Path), "medium", false)
	case ToolNameSystemSearchAccess:
		return discoveryProfile("system_search_access", scopeFromPath(req.Path), "low", false)
	case ToolNameHarnessAudit:
		return discoveryProfile("harness_audit", scopeFromPath(req.Path), "medium", true)
	case ToolNameHarnessFeedback:
		return discoveryProfile("harness_feedback", "targeted", "low", true)
	case ToolNameContextIndex:
		return discoveryProfile(ToolNameContextIndex, scopeFromPath(req.Path), "medium", true)
	case ToolNameContextStatus:
		return discoveryProfile(ToolNameContextStatus, "targeted", "low", true)
	case ToolNameContextQuery:
		return discoveryProfile(ToolNameContextQuery, scopeFromPathAndPattern(req.Path, textutil.FirstNonEmpty(req.Pattern, req.Query)), "low", true)
	case ToolNameContextZoom:
		return discoveryProfile(ToolNameContextZoom, "targeted", "low", true)
	case ToolNameContextPack:
		return discoveryProfile(ToolNameContextPack, scopeFromPathAndPattern(req.Path, textutil.FirstNonEmpty(req.Task, req.Query, req.Pattern)), "medium", true)
	case ToolNameContextImpact:
		return discoveryProfile(ToolNameContextImpact, scopeFromPathAndPattern(req.Path, textutil.FirstNonEmpty(req.Task, req.Query, req.Pattern)), "medium", true)
	case ToolNameContextTracePack:
		return discoveryProfile("runtime_trace", scopeFromPathAndPattern(req.Path, req.Command), "medium", true)
	case ToolNameDiscoveryQuery:
		cost := "medium"
		if NormalizeDiscoveryQueryPurpose(req.Purpose) == DiscoveryQueryPurposeSecurityReview {
			cost = "high"
		}

		return discoveryProfile("composite_query", scopeFromPathAndPattern(req.Path, req.Pattern), cost, true)
	case ToolNameStructuralSearch:
		return discoveryProfile("ast_search", "targeted", "medium", true)
	case ToolNameDependencyGraph:
		return discoveryProfile(ToolNameDependencyGraph, scopeFromPath(req.Path), "medium", true)
	case ToolNameHistoryRange, ToolNameHistorySymbol, ToolNameBlameRange:
		return discoveryProfile("history", "targeted", "medium", true)
	case ToolNameCtagsSymbols:
		return discoveryProfile("symbol_index", scopeFromPathAndPattern(req.Path, req.Pattern), "medium", true)
	case ToolNameSemgrepScan, ToolNameJoernScan:
		return discoveryProfile(EvidenceCapabilitySecurityScan, scopeFromPath(req.Path), "high", true)
	case ToolNameTask:
		return discoveryProfile("delegated_discovery", "broad", "high", false)
	case ToolNameShell:
		return shellDiscoveryProfile(req.Command)
	default:
		return languageDiscoveryProfile(tool)
	}
}

func readOnlyCapabilityDiscoveryProfile(req ToolRequest) ToolDiscoveryProfile {
	routed := req

	routed.Tool = ToolNameCodeNavigate

	switch normalizeReadOnlyCapabilityID(req.Capability) {
	case string(ReadOnlyCapabilityFileOutline):
		routed.Capability = string(CapFileOutline)
	case string(ReadOnlyCapabilityInspectSymbol):
		routed.Capability = string(CapReadSymbol)
	case string(ReadOnlyCapabilitySymbolLookup):
		routed.Capability = string(CapFindSymbol)
	case string(ReadOnlyCapabilityDefinition):
		routed.Capability = string(CapDefinition)
	case string(ReadOnlyCapabilityReferences):
		routed.Capability = string(CapReferences)
	case string(ReadOnlyCapabilityCallsFrom):
		routed.Capability = string(CapCallsFrom)
	case string(ReadOnlyCapabilityCallsInto):
		routed.Capability = string(CapCallsInto)
	case string(ReadOnlyCapabilityRelatedFiles):
		routed.Capability = string(CapRelatedFiles)
	default:
		return discoveryProfile(ToolNameReadOnlyCapability, scopeFromPathAndPattern(req.Path, textutil.FirstNonEmpty(req.Pattern, req.Query)), "medium", true)
	}

	return codeNavigationDiscoveryProfile(routed)
}

func codeNavigationDiscoveryProfile(req ToolRequest) ToolDiscoveryProfile {
	class := "language_symbol"
	cost := "low"

	switch normalizeCodeNavigationCapability(req.Capability) {
	case string(CapDefinition), string(CapReferences), string(CapCallsFrom), string(CapCallsInto), string(CapImplementations), string(CapRelatedFiles):
		class = "language_relationship"
		cost = "medium"
	}

	return discoveryProfile(class, scopeFromPathAndPattern(req.Path, textutil.FirstNonEmpty(req.Pattern, req.Query)), cost, true)
}

func normalizeCodeNavigationCapability(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")

	switch value {
	case "outline":
		return string(CapFileOutline)
	case "symbol", "symbol_lookup", "lookup":
		return string(CapFindSymbol)
	case "workspace_symbols", "find_symbol_workspace":
		return string(CapWorkspaceSymbol)
	case "inspect_symbol", "symbol_body":
		return string(CapReadSymbol)
	case "definitions", "declaration", "declarations", "find_definition", "find_definitions", "find_declaration", "find_declarations", "goto_definition", "go_to_definition":
		return string(CapDefinition)
	case "reference", "usages", "find_references", "find_reference":
		return string(CapReferences)
	case "callees":
		return string(CapCallsFrom)
	case "callers":
		return string(CapCallsInto)
	case "implementation", "impls":
		return string(CapImplementations)
	case "related":
		return string(CapRelatedFiles)
	default:
		return value
	}
}

func discoveryProfile(class string, scope string, cost string, generic bool) ToolDiscoveryProfile {
	return ToolDiscoveryProfile{
		Class:     class,
		Scope:     scope,
		Cost:      cost,
		Discovery: true,
		Generic:   generic,
	}
}

func scopeFromPath(path string) string {
	path = filepath.Clean(defaultString(strings.TrimSpace(path), "."))
	if path == "." || path == string(filepath.Separator) {
		return "broad"
	}

	return "targeted"
}

func scopeFromPathAndPattern(path string, pattern string) string {
	if strings.TrimSpace(pattern) != "" {
		return "targeted"
	}

	return scopeFromPath(path)
}

func shellDiscoveryProfile(command string) ToolDiscoveryProfile {
	switch {
	case isBroadInspectionShell(command):
		return discoveryProfile("shell_discovery", "broad", "high", false)
	case isNarrowingShell(command):
		return discoveryProfile("shell_discovery", "targeted", "medium", false)
	default:
		return ToolDiscoveryProfile{Class: "other"}
	}
}

func languageDiscoveryProfile(tool string) ToolDiscoveryProfile {
	lang, suffix, ok := splitLanguageTool(tool)
	if !ok {
		return ToolDiscoveryProfile{Class: "other"}
	}

	_ = lang

	switch suffix {
	case string(CapOverview), string(CapEntrypoints):
		return discoveryProfile("language_overview", "broad", "medium", false)
	case string(CapFindSymbol), "find_function", "find_type", "find_const", "find_package", string(CapWorkspaceSymbol), string(CapFileOutline), string(CapReadSymbol):
		return discoveryProfile("language_symbol", "targeted", "low", false)
	case string(CapDefinition), "declaration", string(CapReferences), string(CapImplementations), string(CapCallsFrom), string(CapCallsInto), string(CapExtractBody), string(CapRelatedFiles), "importers", "includes", "who_includes", "blast_radius", "dead_symbol":
		return discoveryProfile("language_relationship", "targeted", "medium", false)
	case string(CapDiagnostics):
		return discoveryProfile("diagnostics", "targeted", "medium", false)
	default:
		return ToolDiscoveryProfile{Class: "other"}
	}
}

func splitLanguageTool(tool string) (string, string, bool) {
	for _, prefix := range []string{"go_", "cpp_", "python_", "ts_", "rust_", "java_"} {
		if strings.HasPrefix(tool, prefix) {
			return strings.TrimSuffix(prefix, "_"), strings.TrimPrefix(tool, prefix), true
		}
	}

	return "", "", false
}
