package workspace

import (
	"fmt"
	"strings"

	"idiom/internal/capabilities/workspace/externaltool"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/semantic/languagetools"
)

const ToolProfileDefault = "default"
const ToolProfileDiscovery = "discovery"
const ToolProfileMCPDiscovery = "mcp_discovery"

type ToolProfileDecision struct {
	Allowed bool
	Code    string
	Reason  string
}

type ToolProfilePolicyContext struct {
	Workspace     config.Workspace
	ExternalTools *externaltool.Registry
}

func NormalizeToolProfile(profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return ToolProfileDefault, nil
	}

	switch profile {
	case ToolProfileDefault, ToolProfileDiscovery, ToolProfileMCPDiscovery:
		return profile, nil
	default:
		return "", fmt.Errorf("unknown tool profile %q (expected default or discovery)", profile)
	}
}

func ToolAllowedForProfile(tool string, profile string) ToolProfileDecision {
	profile, err := NormalizeToolProfile(profile)
	if err != nil {
		return ToolProfileDecision{Allowed: false, Code: "invalid_profile", Reason: err.Error()}
	}

	tool = strings.TrimSpace(tool)

	if profile == ToolProfileDefault {
		return ToolProfileDecision{Allowed: true}
	}

	if tool == "" {
		return ToolProfileDecision{Allowed: false, Code: "invalid_input", Reason: "tool is required"}
	}

	if !IsBuiltInToolName(tool) {
		return discoveryProfileDenied("tool is not declared in the built-in tool surface")
	}

	policy := toolSchemaPolicyForTool(tool)
	if tool == ToolNameShell || hasToolSchemaSideEffect(policy.SideEffects, autonomy.SideEffectUnknown) {
		return discoveryProfileDenied("unknown or shell side effects are not allowed")
	}

	if IsWorkspaceWriteTool(tool) ||
		hasToolSchemaSideEffect(policy.SideEffects, autonomy.SideEffectAdditive) ||
		hasToolSchemaSideEffect(policy.SideEffects, autonomy.SideEffectDestructive) {

		return discoveryProfileDenied("mutation side effects are not allowed")
	}

	if profile == ToolProfileDiscovery && builtInToolSurfaceDiscoveryLocalArtifacts(tool) {
		return discoveryProfileDenied("tool writes local context artifacts")
	}

	if !hasToolSchemaSideEffect(policy.SideEffects, autonomy.SideEffectReadOnly) {
		return discoveryProfileDenied("tool is not declared read-only")
	}

	return ToolProfileDecision{Allowed: true}
}

func ToolRequestAllowedForProfile(req ToolRequest, profile string) ToolProfileDecision {
	decision := ToolAllowedForProfile(req.Tool, profile)
	if !decision.Allowed {
		return decision
	}

	profile, err := NormalizeToolProfile(profile)
	if err != nil {
		return ToolProfileDecision{Allowed: false, Code: "invalid_profile", Reason: err.Error()}
	}

	if profile != ToolProfileDiscovery {
		return ToolProfileDecision{Allowed: true}
	}

	return discoveryProfileRequestAllowed(req)
}

func ToolRequestAllowedForWorkspaceProfile(req ToolRequest, profile string, policy ToolProfilePolicyContext) ToolProfileDecision {
	decision := ToolRequestAllowedForProfile(req, profile)
	if decision.Allowed {
		return decision
	}

	profile, err := NormalizeToolProfile(profile)
	if err != nil {
		return ToolProfileDecision{Allowed: false, Code: "invalid_profile", Reason: err.Error()}
	}
	if profile != ToolProfileDiscovery || IsBuiltInToolName(req.Tool) {
		return decision
	}

	if policy.ExternalTools != nil && policy.ExternalTools.IsReadOnlyDiscovery(req.Tool) {
		return ToolProfileDecision{Allowed: true}
	}

	if strings.HasPrefix(strings.TrimSpace(req.Tool), "mcp:") &&
		toolpolicy.Match(req.Tool, toolPolicyRequest(req), config.ToolPolicyRules(policy.Workspace.ToolRules)) == toolpolicy.Allow {

		return ToolProfileDecision{Allowed: true}
	}

	return decision
}

func ToolProfileDeniedResult(tool string, profile string, decision ToolProfileDecision) ToolResult {
	profile = textutil.FirstNonEmpty(strings.TrimSpace(profile), ToolProfileDefault)

	code := textutil.FirstNonEmpty(strings.TrimSpace(decision.Code), "profile_denied")
	reason := textutil.FirstNonEmpty(strings.TrimSpace(decision.Reason), "tool is not allowed by profile")

	return ToolResult{
		Tool:    strings.TrimSpace(tool),
		Code:    code,
		Summary: "Tool request denied by " + profile + " profile",
		Error:   fmt.Sprintf("%s: read_only_profile: %s", code, reason),
		Failure: failures.New(failures.PermissionDenied, failures.NoReason, reason),
	}
}

func discoveryProfileDenied(reason string) ToolProfileDecision {
	return ToolProfileDecision{Allowed: false, Code: "profile_denied", Reason: reason}
}

func discoveryProfileRequestAllowed(req ToolRequest) ToolProfileDecision {
	tool := strings.TrimSpace(req.Tool)
	if req.Refresh {
		return discoveryProfileDenied("refresh requests are not allowed")
	}

	switch tool {
	case ToolNameContextStatus:
		if discoveryProfileFreshnessPolicyCanRefresh(req.FreshnessPolicy) {
			return discoveryProfileDenied("freshness_policy requests that can refresh local context are not allowed")
		}
	case ToolNameContextQuery, ToolNameContextZoom:
		if !discoveryProfileFreshnessPolicyIsReportOnly(req.FreshnessPolicy) {
			return discoveryProfileDenied("graph-backed discovery requests must use freshness_policy report_only or allow_stale")
		}
	}

	return ToolProfileDecision{Allowed: true}
}

func discoveryProfileFreshnessPolicyCanRefresh(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "read_through", "require_fresh":
		return true
	default:
		return false
	}
}

func discoveryProfileFreshnessPolicyIsReportOnly(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "report_only", "allow_stale":
		return true
	default:
		return false
	}
}

func IsBuiltInToolName(tool string) bool {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return false
	}

	if _, ok := builtInToolSurfaceCatalog.lookup(tool); ok {
		return true
	}

	return languagetools.IsToolName(tool)
}
