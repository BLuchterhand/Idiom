package workspace

import (
	"context"
	"strings"
	"time"

	"idiom/internal/capabilities/mcp"
	"idiom/internal/platform/config"
)

type ConfiguredToolSurfaceOptions struct {
	Manager *Manager
	Profile string
	Request ToolRequest
	Runtime bool
	List    bool
}

type ConfiguredToolSurface struct {
	Manager            *Manager
	PolicyContext      ToolProfilePolicyContext
	ExternalToolsError error
	MCPFailures        []string
	MCPRegistry        *mcp.Registry
}

func ConfigureToolSurface(ctx context.Context, cfg config.Config, opts ConfiguredToolSurfaceOptions) *ConfiguredToolSurface {
	mgr := opts.Manager
	if mgr == nil {
		mgr = NewManager(cfg.Workspace)
	}

	setup := &ConfiguredToolSurface{
		Manager:       mgr,
		PolicyContext: ToolProfilePolicyContext{Workspace: cfg.Workspace},
	}

	registry, err := LoadExternalToolsRegistry(cfg.Workspace)
	if err != nil {
		setup.ExternalToolsError = err
	} else if registry != nil {
		setup.PolicyContext.ExternalTools = registry
		mgr.toolSurface.SetExternalToolsRegistry(registry)
	}

	needsMCP := opts.Runtime
	if opts.List {
		needsMCP = opts.Profile != ToolProfileDiscovery || configuredToolSurfaceHasMCPAllowRule(cfg.Workspace.ToolRules)
	} else if !opts.Runtime && strings.HasPrefix(strings.TrimSpace(opts.Request.Tool), "mcp:") {
		needsMCP = ToolRequestAllowedForWorkspaceProfile(opts.Request, opts.Profile, setup.PolicyContext).Allowed
	}
	if needsMCP {
		mcpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		registry, failures := mcp.SetupRegistry(mcpCtx, cfg.MCP, cfg.Workspace.AllowPrivateNetwork)
		cancel()

		setup.MCPFailures = failures
		if registry != nil {
			setup.MCPRegistry = registry
			mgr.toolSurface.SetMCPRegistry(registry)
		}
	}

	return setup
}

func (s *ConfiguredToolSurface) Close() {
	if s.MCPRegistry != nil {
		_ = s.MCPRegistry.Close()
		s.MCPRegistry = nil
	}
}

func (s *ConfiguredToolSurface) ListToolNamesForProfile(profile string) []string {
	opts := ToolSurfaceManifestOptions{IncludeGo: true, IncludeCpp: true, IncludePython: true, IncludeTypescript: true, IncludeRust: true, IncludeJava: true}
	snapshot := s.Manager.toolSurface.Snapshot(opts, s.Manager.externalToolBaseDir())

	tools := snapshot.AllowedTools()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if toolListProfileAllows(tool, profile, s.PolicyContext) {
			out = append(out, tool)
		}
	}

	return out
}

func toolListProfileAllows(tool string, profile string, policyContext ToolProfilePolicyContext) bool {
	if IsBuiltInToolName(tool) {
		return ToolAllowedForProfile(tool, profile).Allowed
	}

	return profile != ToolProfileDiscovery ||
		ToolRequestAllowedForWorkspaceProfile(ToolRequest{Tool: tool}, profile, policyContext).Allowed
}

func configuredToolSurfaceHasMCPAllowRule(rules map[string][]config.ToolRule) bool {
	for tool, list := range rules {
		if !strings.HasPrefix(strings.TrimSpace(tool), "mcp:") {
			continue
		}
		for _, rule := range list {
			if strings.EqualFold(strings.TrimSpace(rule.Decision), "allow") {
				return true
			}
		}
	}

	return false
}
