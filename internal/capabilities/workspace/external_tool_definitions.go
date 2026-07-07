package workspace

import (
	"cmp"
	"slices"
)

// appendExternalToolDefinitions appends ToolDefinitions for every entry in the
// registry, filtered by allowedSet when non-empty. Results are sorted for
// deterministic tool-spec order.
func appendExternalToolDefinitions(defs []ToolDefinition, extReg *ExternalToolRegistry, allowedSet map[string]struct{}, baseDir string) []ToolDefinition {
	if extReg == nil {
		return defs
	}

	extDefs := extReg.DefinitionsForBaseDir(baseDir)
	slices.SortFunc(extDefs, func(a, b ToolDefinition) int { return cmp.Compare(a.Name, b.Name) })

	for _, ed := range extDefs {
		if allowedSet != nil {
			if _, ok := allowedSet[ed.Name]; !ok {
				continue
			}
		}

		defs = append(defs, ed)
	}

	return defs
}
