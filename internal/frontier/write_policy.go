package frontier

import (
	"fmt"
	"path/filepath"
	"strings"
)

func PathExplicitlyAuthorized(path string, normalizedPrompt string, explicitPaths map[string]struct{}) bool {
	path = NormalizePath(path)
	if path == "" {
		return false
	}
	if _, ok := explicitPaths[path]; ok {
		return true
	}
	if _, ok := explicitPaths[filepath.Base(path)]; ok {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(normalizedPrompt)), strings.ToLower(path))
}

func ExplicitCreateRequested(normalizedPrompt string, path string, pathAuthorized bool, fileExists bool) bool {
	if !ContainsAny(strings.ToLower(strings.TrimSpace(normalizedPrompt)), "create", "new file", "add file", "add a file", "make a file", "create directory", "new directory", "mkdir") {
		return false
	}
	return NormalizePath(path) == "" || pathAuthorized || !fileExists
}

func ExplicitRewriteRequested(normalizedPrompt string, path string, pathAuthorized bool) bool {
	if !ContainsAny(strings.ToLower(strings.TrimSpace(normalizedPrompt)), "rewrite", "full rewrite", "rewrite the file", "replace the entire", "replace the whole file", "overwrite") {
		return false
	}
	return NormalizePath(path) == "" || pathAuthorized
}

func ExplicitAppendRequested(normalizedPrompt string, path string, pathAuthorized bool) bool {
	normalized := strings.ToLower(strings.TrimSpace(normalizedPrompt))
	if !ContainsAny(normalized,
		"append",
		"append to",
		"add to the end",
		"adding exactly this sentence",
		"add exactly this sentence",
		"adding exactly this troubleshooting sentence",
		"add exactly this troubleshooting sentence",
	) {
		return false
	}
	return NormalizePath(path) == "" || pathAuthorized
}

func ExplicitMultiFileRequested(normalizedPrompt string) bool {
	return ContainsAny(strings.ToLower(strings.TrimSpace(normalizedPrompt)), "multiple files", "several files", "across files", "multi-file", "broad refactor", "whole repo", "codebase")
}

func IsProtectedWritePath(path string) bool {
	path = NormalizePath(path)
	switch {
	case path == "", path == ".":
		return false
	case path == "config.json", path == "config.example.json":
		return true
	case strings.HasPrefix(path, "prompts/"), strings.HasPrefix(path, "scripts/"):
		return true
	case strings.HasPrefix(path, ".git/"), strings.HasPrefix(path, "logs/"), strings.HasPrefix(path, "models/"), strings.HasPrefix(path, "bin/"):
		return true
	default:
		return false
	}
}

func WriteApprovalPathRoleLines(paths []string) []string {
	lines := make([]string, 0, len(paths)+2)
	if len(paths) > 1 {
		lines = append(lines, fmt.Sprintf("requested scope spans %d files", len(paths)))
	}
	for _, path := range paths {
		path = NormalizePath(path)
		if path == "" {
			continue
		}
		switch {
		case path == "config.json" || path == "config.example.json":
			lines = append(lines, fmt.Sprintf("%s controls runtime defaults and launch behavior for future sessions", path))
		case strings.HasPrefix(path, "prompts/"):
			lines = append(lines, fmt.Sprintf("%s changes baseline assistant behavior and can affect every future session", path))
		case strings.HasPrefix(path, "scripts/"):
			lines = append(lines, fmt.Sprintf("%s affects launcher or developer workflow entrypoints", path))
		case strings.HasPrefix(path, "internal/"):
			lines = append(lines, fmt.Sprintf("%s is core application code under guarded scope", path))
		}
	}
	return lines
}
