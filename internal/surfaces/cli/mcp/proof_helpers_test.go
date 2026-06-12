package mcp

import "strings"

const mcpStdioTransport = StdioTransport

func jsonValueAtDottedPath(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		value, ok := object[part]
		if !ok {
			return nil, false
		}

		current = value
	}

	return current, true
}
