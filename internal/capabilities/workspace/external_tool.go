package workspace

import (
	"context"
	"encoding/json"

	"idiom/internal/capabilities/workspace/externaltool"
)

// ExternalTool registers an arbitrary shell-invokable tool that the model can
// call by name, just like a built-in or MCP tool.
type ExternalTool = externaltool.Tool

// ExternalToolRegistry holds loaded ExternalTool definitions. The execution
// implementation lives in the externaltool subpackage; this wrapper preserves
// the workspace package API.
type ExternalToolRegistry struct {
	registry *externaltool.Registry
}

// NewExternalToolRegistry returns a registry pre-loaded with the provided
// tools. Returns an error if any tool name collides or is empty.
func NewExternalToolRegistry(tools []ExternalTool) (*ExternalToolRegistry, error) {
	registry, err := externaltool.NewRegistry(tools)
	if err != nil {
		return nil, err
	}

	return &ExternalToolRegistry{registry: registry}, nil
}

func (r *ExternalToolRegistry) inner() *externaltool.Registry {
	if r == nil {
		return nil
	}

	return r.registry
}

// Names returns the registered tool names in undefined order.
func (r *ExternalToolRegistry) Names() []string {
	return r.inner().Names()
}

// AvailableNames returns registered tool names whose command can be resolved.
func (r *ExternalToolRegistry) AvailableNames(baseDir string) []string {
	return r.inner().AvailableNames(baseDir)
}

// Has reports whether name is registered.
func (r *ExternalToolRegistry) Has(name string) bool {
	return r.inner().Has(name)
}

// Tool returns the registered external tool definition.
func (r *ExternalToolRegistry) Tool(name string) (ExternalTool, bool) {
	return r.inner().Tool(name)
}

// Availability reports whether a registered external tool can be invoked.
func (r *ExternalToolRegistry) Availability(name string, baseDir string) (bool, string) {
	return r.inner().Availability(name, baseDir)
}

// IsReadOnlyDiscovery reports whether the host project declared an external
// tool as a read-only discovery wrapper.
func (r *ExternalToolRegistry) IsReadOnlyDiscovery(name string) bool {
	return r.inner().IsReadOnlyDiscovery(name)
}

// Definitions converts registered tools into workspace ToolDefinition entries.
func (r *ExternalToolRegistry) Definitions() []ToolDefinition {
	return externalToolDefinitions(r.inner().Definitions())
}

// DefinitionsForBaseDir converts available registered tools into workspace
// ToolDefinition entries.
func (r *ExternalToolRegistry) DefinitionsForBaseDir(baseDir string) []ToolDefinition {
	return externalToolDefinitions(r.inner().DefinitionsForBaseDir(baseDir))
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

// Invoke runs the registered tool with the provided JSON-encoded input.
func (r *ExternalToolRegistry) Invoke(ctx context.Context, name string, input json.RawMessage, baseDir string, maxOutputBytes int) (string, bool, error) {
	return r.inner().Invoke(ctx, name, input, baseDir, maxOutputBytes)
}
