package workspace

import (
	"path/filepath"
	"strings"
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
	case "pwd":
		return discoveryProfile("workspace_context", "targeted", "low", false)
	case "list_files":
		return discoveryProfile("workspace_listing", scopeFromPath(req.Path), "low", false)
	case "search_files":
		return discoveryProfile("file_search", "targeted", "low", false)
	case "search_content", "rg":
		return discoveryProfile("text_search", "targeted", "low", false)
	case "read_file":
		return discoveryProfile("file_read", "targeted", "low", false)
	case "discovery_query":
		cost := "medium"
		if normalizeDiscoveryQueryPurpose(req.Purpose) == "security_review" {
			cost = "high"
		}
		return discoveryProfile("composite_query", scopeFromPathAndPattern(req.Path, req.Pattern), cost, true)
	case "structural_search":
		return discoveryProfile("ast_search", "targeted", "medium", true)
	case "dependency_graph":
		return discoveryProfile("dependency_graph", scopeFromPath(req.Path), "medium", true)
	case "history_range", "history_symbol", "blame_range":
		return discoveryProfile("history", "targeted", "medium", true)
	case "ctags_symbols":
		return discoveryProfile("symbol_index", scopeFromPathAndPattern(req.Path, req.Pattern), "medium", true)
	case "semgrep_scan", "joern_scan":
		return discoveryProfile("security_scan", scopeFromPath(req.Path), "high", true)
	case "task":
		return discoveryProfile("delegated_discovery", "broad", "high", false)
	case "shell":
		return shellDiscoveryProfile(req.Command)
	default:
		return languageDiscoveryProfile(tool)
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
	case "overview", "entrypoints":
		return discoveryProfile("language_overview", "broad", "medium", false)
	case "find_symbol", "find_function", "find_type", "find_const", "find_package", "workspace_symbol", "file_outline", "read_symbol":
		return discoveryProfile("language_symbol", "targeted", "low", false)
	case "definition", "declaration", "references", "implementations", "calls_from", "calls_into", "extract_body", "related_files", "importers", "includes", "who_includes", "blast_radius", "dead_symbol":
		return discoveryProfile("language_relationship", "targeted", "medium", false)
	case "diagnostics":
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
