package workspace

import (
	"slices"
	"strings"
	"sync"

	"idiom/internal/capabilities/mcp"
)

type ToolSurface struct {
	mu                    sync.Mutex
	mcpRegistry           *mcp.Registry
	externalToolsRegistry *ExternalToolRegistry
}

type ToolSurfaceManifestOptions struct {
	Profile           ToolSurfaceProfile
	ReadOnly          bool
	IncludeGo         bool
	IncludeCpp        bool
	IncludePython     bool
	IncludeTypescript bool
	IncludeRust       bool
	IncludeJava       bool
}

type ToolSurfaceSnapshot struct {
	allowedTools              []string
	toolDefinitions           []ToolDefinition
	externalToolNames         []string
	externalToolNameSet       map[string]struct{}
	externalReadOnlyDiscovery map[string]struct{}
}

type AdvertisedToolSurfaceInput struct {
	BaseTools        []string
	RuntimeTools     []string
	ConversationOnly bool
	SuppressAll      bool
	DisableTaskTool  bool
}

func (s ToolSurfaceSnapshot) AllowedTools() []string {
	return append([]string(nil), s.allowedTools...)
}

func (s ToolSurfaceSnapshot) AdvertisedAllowedTools(input AdvertisedToolSurfaceInput) []string {
	if input.SuppressAll {
		return nil
	}

	if input.ConversationOnly {
		return s.conversationOnlyAdvertisedTools(input.BaseTools)
	}

	allowed := append([]string(nil), input.BaseTools...)
	if len(input.RuntimeTools) > 0 {
		allowed = filterAdvertisedToolsByRuntimeSurface(input.BaseTools, input.RuntimeTools)
	}

	if input.DisableTaskTool {
		allowed = filterAdvertisedTools(allowed, func(tool string) bool {
			return strings.TrimSpace(tool) != ToolNameTask
		})
	}

	return allowed
}

func (s ToolSurfaceSnapshot) conversationOnlyAdvertisedTools(base []string) []string {
	names := s.AvailableExternalToolNames()
	if len(names) == 0 {
		return nil
	}

	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		known[strings.TrimSpace(name)] = struct{}{}
	}

	out := filterAdvertisedTools(base, func(tool string) bool {
		_, ok := known[strings.TrimSpace(tool)]
		return ok
	})
	if len(out) > 0 {
		return out
	}

	return names
}

func filterAdvertisedToolsByRuntimeSurface(base []string, runtimeTools []string) []string {
	available := make(map[string]string, len(base))
	for _, tool := range base {
		trimmed := strings.TrimSpace(tool)
		if trimmed != "" {
			available[trimmed] = tool
		}
	}

	filtered := make([]string, 0, len(runtimeTools))
	seen := make(map[string]struct{}, len(runtimeTools))
	for _, tool := range runtimeTools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		if original, ok := available[tool]; ok {
			filtered = append(filtered, original)
			seen[tool] = struct{}{}
		}
	}

	return filtered
}

func filterAdvertisedTools(tools []string, keep func(string) bool) []string {
	if len(tools) == 0 || keep == nil {
		return append([]string(nil), tools...)
	}

	filtered := make([]string, 0, len(tools))
	for _, tool := range tools {
		if keep(tool) {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func (s ToolSurfaceSnapshot) BuildToolDefinitions(allowed []string) []ToolDefinition {
	if len(allowed) == 0 {
		return cloneToolDefinitions(s.toolDefinitions)
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		name = strings.TrimSpace(name)
		if name != "" {
			allowedSet[name] = struct{}{}
		}
	}

	out := make([]ToolDefinition, 0, len(allowedSet))
	for _, def := range s.toolDefinitions {
		if _, ok := allowedSet[strings.TrimSpace(def.Name)]; ok {
			out = append(out, def)
		}
	}

	return out
}

func (s ToolSurfaceSnapshot) AvailableExternalToolNames() []string {
	return append([]string(nil), s.externalToolNames...)
}

func (s ToolSurfaceSnapshot) HasExternalTool(name string) bool {
	_, ok := s.externalToolNameSet[strings.TrimSpace(name)]
	return ok
}

func (s ToolSurfaceSnapshot) ExternalToolIsReadOnlyDiscovery(name string) bool {
	_, ok := s.externalReadOnlyDiscovery[strings.TrimSpace(name)]
	return ok
}

func cloneToolDefinitions(defs []ToolDefinition) []ToolDefinition {
	if len(defs) == 0 {
		return nil
	}

	return append([]ToolDefinition(nil), defs...)
}

func (m *Manager) SetMCPRegistry(registry *mcp.Registry) {
	if m == nil {
		return
	}

	m.toolSurface.SetMCPRegistry(registry)
}

func (m *Manager) SetExternalToolsRegistry(registry *ExternalToolRegistry) {
	if m == nil {
		return
	}

	m.toolSurface.SetExternalToolsRegistry(registry)
}

func (m *Manager) ToolSurfaceSnapshot() ToolSurfaceSnapshot {
	if m == nil {
		return ToolSurfaceSnapshot{toolDefinitions: BuildToolDefinitions(nil)}
	}

	readOnly := m.Mode() == ModeExplore
	return m.toolSurface.Snapshot(m.toolSurfaceManifestOptions(readOnly), m.externalToolBaseDir())
}

func (m *Manager) ToolSurfaceSnapshotForProfile(profile ToolSurfaceProfile) ToolSurfaceSnapshot {
	if m == nil {
		return ToolSurfaceSnapshot{toolDefinitions: BuildToolDefinitions(nil)}
	}

	readOnly := m.Mode() == ModeExplore
	opts := m.toolSurfaceManifestOptions(readOnly)
	opts.Profile = profile

	return m.toolSurface.Snapshot(opts, m.externalToolBaseDir())
}

func (s *ToolSurface) SetMCPRegistry(registry *mcp.Registry) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.mcpRegistry = registry
}

func (s *ToolSurface) MCPRegistry() *mcp.Registry {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mcpRegistry
}

func (s *ToolSurface) SetExternalToolsRegistry(registry *ExternalToolRegistry) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.externalToolsRegistry = registry
}

func (s *ToolSurface) ExternalToolsRegistry() *ExternalToolRegistry {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.externalToolsRegistry
}

func (s *ToolSurface) ToolManifestTools(opts ToolSurfaceManifestOptions) []string {
	return ToolManifestTools(toolManifestOptionsForSurface(opts))
}

func toolManifestOptionsForSurface(opts ToolSurfaceManifestOptions) ToolManifestToolOptions {
	if opts.Profile != "" {
		manifestOpts := toolManifestOptionsForSurfaceProfile(opts.Profile)
		manifestOpts.IncludeGo = opts.IncludeGo
		manifestOpts.IncludeCpp = opts.IncludeCpp
		manifestOpts.IncludePython = opts.IncludePython
		manifestOpts.IncludeTypescript = opts.IncludeTypescript
		manifestOpts.IncludeRust = opts.IncludeRust
		manifestOpts.IncludeJava = opts.IncludeJava

		return manifestOpts
	}

	return ToolManifestToolOptions{
		IncludeTaskLedger: !opts.ReadOnly,
		IncludeTask:       true,
		IncludeWriteTools: !opts.ReadOnly,
		IncludeGo:         opts.IncludeGo,
		IncludeCpp:        opts.IncludeCpp,
		IncludePython:     opts.IncludePython,
		IncludeTypescript: opts.IncludeTypescript,
		IncludeRust:       opts.IncludeRust,
		IncludeJava:       opts.IncludeJava,
	}
}

func (s *ToolSurface) AllowedTools(opts ToolSurfaceManifestOptions, externalBaseDir string) []string {
	tools := s.ToolManifestTools(opts)

	if registry := s.MCPRegistry(); registry != nil {
		names := make([]string, 0)
		for qualified := range registry.Tools() {
			names = append(names, qualified)
		}

		slices.Sort(names)
		tools = append(tools, names...)
	}

	if registry := s.ExternalToolsRegistry(); registry != nil {
		names := registry.AvailableNames(externalBaseDir)
		slices.Sort(names)
		tools = append(tools, names...)
	}

	if opts.Profile != "" {
		return AllowedToolsForSurfaceProfile(tools, opts.Profile)
	}

	return tools
}

func (s *ToolSurface) Snapshot(opts ToolSurfaceManifestOptions, externalBaseDir string) ToolSurfaceSnapshot {
	if s == nil {
		return ToolSurfaceSnapshot{toolDefinitions: BuildToolDefinitions(nil)}
	}

	snapshot := ToolSurfaceSnapshot{
		allowedTools:    s.AllowedTools(opts, externalBaseDir),
		toolDefinitions: s.BuildToolDefinitions(nil, externalBaseDir),
	}

	if registry := s.ExternalToolsRegistry(); registry != nil {
		snapshot.externalToolNames = registry.AvailableNames(externalBaseDir)
		snapshot.externalToolNameSet = make(map[string]struct{})
		for _, name := range registry.Names() {
			name = strings.TrimSpace(name)
			if name != "" {
				snapshot.externalToolNameSet[name] = struct{}{}
			}
		}

		snapshot.externalReadOnlyDiscovery = make(map[string]struct{})
		for _, name := range registry.Names() {
			name = strings.TrimSpace(name)
			if name != "" && registry.IsReadOnlyDiscovery(name) {
				snapshot.externalReadOnlyDiscovery[name] = struct{}{}
			}
		}
	}

	return snapshot
}

func (s *ToolSurface) BuildToolDefinitions(allowed []string, externalBaseDir string) []ToolDefinition {
	defs := BuildToolDefinitions(allowed)

	var allowedSet map[string]struct{}
	if len(allowed) > 0 {
		allowedSet = make(map[string]struct{}, len(allowed))
		for _, name := range allowed {
			if name != "" {
				allowedSet[name] = struct{}{}
			}
		}
	}

	defs = appendExternalToolDefinitions(defs, s.ExternalToolsRegistry(), allowedSet, externalBaseDir)

	registry := s.MCPRegistry()
	if registry == nil {
		return defs
	}

	tools := registry.Tools()
	names := make([]string, 0, len(tools))
	for qualified := range tools {
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[qualified]; !ok {
				continue
			}
		}

		names = append(names, qualified)
	}

	slices.Sort(names)

	for _, qualified := range names {
		spec := tools[qualified]
		server, _, _ := mcp.ParseQualifiedToolName(qualified)
		description := spec.Description
		if description == "" {
			description = "MCP tool from " + server
		}

		if server != "" {
			description = "[mcp:" + server + "] " + description
		}

		params := string(spec.InputSchema)
		if params == "" {
			params = `{"type":"object","properties":{}}`
		}

		defs = append(defs, ToolDefinition{
			Name:           qualified,
			Description:    description,
			ParametersJSON: params,
		})
	}

	return defs
}

func (s *ToolSurface) KnownToolNames(externalBaseDir string) []string {
	names := append([]string(nil), toolManifestToolsAcrossBuiltInSurfaces()...)
	if reg := s.ExternalToolsRegistry(); reg != nil {
		names = append(names, reg.AvailableNames(externalBaseDir)...)
	}

	if mcpReg := s.MCPRegistry(); mcpReg != nil {
		for name := range mcpReg.Tools() {
			names = append(names, name)
		}
	}

	return names
}
