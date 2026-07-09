package mcpidentity

import (
	"fmt"
	"strings"
)

const qualifiedToolPrefix = "mcp:"

// NormalizeServerName returns the canonical registry identity for an MCP
// server. Slashes are reserved for the qualified tool-name separator.
func NormalizeServerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("mcp server name is required")
	}
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("mcp server name %q cannot contain '/'", name)
	}

	return name, nil
}

// QualifiedToolName produces the stable model-facing identity for an MCP tool.
func QualifiedToolName(server string, tool string) string {
	return qualifiedToolPrefix + strings.TrimSpace(server) + "/" + strings.TrimSpace(tool)
}

// ParseQualifiedToolName splits a model-facing MCP tool identity.
func ParseQualifiedToolName(name string) (server string, tool string, ok bool) {
	trimmed := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmed, qualifiedToolPrefix) {
		return "", "", false
	}

	trimmed = strings.TrimPrefix(trimmed, qualifiedToolPrefix)
	idx := strings.IndexByte(trimmed, '/')
	if idx <= 0 || idx >= len(trimmed)-1 {
		return "", "", false
	}

	return trimmed[:idx], trimmed[idx+1:], true
}
