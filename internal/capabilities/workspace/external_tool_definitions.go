package workspace

import (
	"cmp"
	"slices"

	"idiom/internal/capabilities/workspace/externaltool"
)

// appendExternalToolDefinitions appends ToolDefinitions for every entry in the
// registry, filtered by allowedSet when non-empty. Results are sorted for
// deterministic tool-spec order.
func appendExternalToolDefinitions(defs []ToolDefinition, extReg *externaltool.Registry, allowedSet map[string]struct{}, baseDir string) []ToolDefinition {
	if extReg == nil {
		return defs
	}

	extDefs := externalToolDefinitions(extReg.DefinitionsForBaseDir(baseDir))
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

func externalToolDefinitions(defs []externaltool.Definition) []ToolDefinition {
	if len(defs) == 0 {
		return nil
	}

	out := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		out = append(out, ToolDefinition{
			Name:           def.Name,
			Description:    def.Description,
			ParametersJSON: def.ParametersJSON,
		})
	}

	return out
}
