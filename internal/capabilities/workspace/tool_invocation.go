package workspace

import (
	"context"
	"strings"
	"time"

	"idiom/internal/contracts/toolpolicy"
)

const (
	ToolInvocationTimeoutOwnerInternal            = "tool_internal_timeout"
	ToolInvocationTimeoutOwnerWorkspace           = "workspace_tool_timeout"
	ToolInvocationTimeoutOwnerWorkspaceWithMargin = "workspace_tool_timeout_with_cli_margin"
)

type ToolInvocationPolicyMode string

const (
	ToolInvocationPolicyWorkspaceProfile ToolInvocationPolicyMode = "workspace_profile"
	ToolInvocationPolicyProfile          ToolInvocationPolicyMode = "profile"
)

type ToolInvocationExposure struct {
	ToolNames    []string
	DeniedReason string
}

type ToolInvocationOptions struct {
	Profile                 string
	Agent                   string
	Transport               string
	Caller                  ToolSidecarCaller
	RawInputBytes           int
	RequestFields           ToolTelemetryRequestFieldMode
	PolicyMode              ToolInvocationPolicyMode
	Exposure                ToolInvocationExposure
	IncludeTimeoutOwner     bool
	IncludeAuditEventIDs    bool
	TimeoutMargin           time.Duration
	RequestTelemetryEventID string
	ResultTelemetryEventID  string
	SetupFailureSummary     string
	ResultFilter            func(ToolResult) ToolResult
	Telemetry               func(event string, fields map[string]any)
}

type ToolInvocationOutcome struct {
	Result       ToolResult
	Payload      ToolInvocationPayload
	SetupFailure string
}

type ToolInvocationPayload struct {
	ToolResult
	Sidecar      ToolSidecarEnvelope `json:"sidecar"`
	TimeoutOwner string              `json:"timeout_owner,omitempty"`
}

func (s *ConfiguredToolSurface) InvokeTool(ctx context.Context, req ToolRequest, opts ToolInvocationOptions) ToolInvocationOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = normalizeToolInvocationOptions(opts)

	timeout := ToolTimeoutForRequest(req)
	if s != nil && s.Manager != nil {
		timeout = s.Manager.ToolTimeoutForRequest(req)
	}

	timeoutOwner := ""
	if opts.IncludeTimeoutOwner {
		timeoutOwner = ToolInvocationTimeoutOwner(timeout, opts.TimeoutMargin)
	}
	if opts.Telemetry != nil {
		opts.Telemetry("tool_request", ToolRequestTelemetryFields(req, ToolTelemetryRequestOptions{
			Agent:            opts.Agent,
			Transport:        opts.Transport,
			Profile:          opts.Profile,
			Caller:           opts.Caller,
			RawInputBytes:    opts.RawInputBytes,
			TimeoutOwner:     timeoutOwner,
			TelemetryEventID: opts.RequestTelemetryEventID,
			RequestFields:    opts.RequestFields,
		}))
	}

	var result ToolResult
	outcome := ToolInvocationOutcome{}
	durationMillis := int64(0)
	exposureDecision := opts.Exposure.decision(req.Tool)
	policyDecision := ToolProfileDecision{Allowed: true}
	if exposureDecision.Allowed {
		policyDecision = toolInvocationPolicyDecision(s, req, opts)
	}
	switch {
	case s != nil && len(s.MCPFailures) > 0 && strings.HasPrefix(strings.TrimSpace(req.Tool), "mcp:") &&
		(s.MCPRegistry == nil || !s.MCPRegistry.Has(req.Tool)):
		outcome.SetupFailure = "mcp setup: " + strings.Join(s.MCPFailures, "; ")
		result = ToolResult{Tool: strings.TrimSpace(req.Tool), Summary: opts.SetupFailureSummary, Error: outcome.SetupFailure}
	case !exposureDecision.Allowed:
		if s != nil && s.Manager != nil {
			envelope := NewToolExecutionEnvelope(opts.Transport, req)
			envelope.PreflightDecision = toolpolicy.Deny
			envelope.Status = ToolExecutionDenied
			s.Manager.RecordToolExecutionEnvelope(envelope)
		}
		result = ToolProfileDeniedResult(req.Tool, opts.Profile, exposureDecision)
	case !policyDecision.Allowed:
		if s != nil && s.Manager != nil {
			envelope := NewToolExecutionEnvelope(opts.Transport, req)
			envelope.PreflightDecision = toolpolicy.Deny
			envelope.Status = ToolExecutionDenied
			s.Manager.RecordToolExecutionEnvelope(envelope)
		}
		result = ToolProfileDeniedResult(req.Tool, opts.Profile, policyDecision)
	case s == nil || s.Manager == nil:
		outcome.SetupFailure = "tool setup: manager is not configured"
		result = ToolResult{Tool: strings.TrimSpace(req.Tool), Summary: opts.SetupFailureSummary, Error: outcome.SetupFailure}
	default:
		toolCtx, cancel := toolInvocationContext(ctx, timeout, opts.TimeoutMargin)
		toolCtx = WithToolTransport(toolCtx, opts.Transport)
		startedAt := time.Now()
		result = s.Manager.Execute(toolCtx, req)
		cancel()
		durationMillis = time.Since(startedAt).Milliseconds()
	}

	if opts.ResultFilter != nil {
		result = opts.ResultFilter(result)
	}
	outcome.Result = result
	outcome.Payload = BuildToolInvocationPayload(req, result, opts.Profile, ToolSidecarOptions{
		Caller:    opts.Caller,
		Transport: opts.Transport,
	}, timeoutOwner)
	if opts.Telemetry != nil {
		opts.Telemetry("tool_result", ToolResultTelemetryFields(req, result, ToolTelemetryResultOptions{
			Agent:                opts.Agent,
			Transport:            opts.Transport,
			Profile:              opts.Profile,
			Caller:               opts.Caller,
			DurationMillis:       durationMillis,
			TimeoutOwner:         timeoutOwner,
			TelemetryEventID:     opts.ResultTelemetryEventID,
			IncludeAuditEventIDs: opts.IncludeAuditEventIDs,
		}))
	}

	return outcome
}

func BuildToolInvocationPayload(req ToolRequest, result ToolResult, profile string, sidecarOptions ToolSidecarOptions, timeoutOwner string) ToolInvocationPayload {
	return ToolInvocationPayload{
		ToolResult:   result,
		Sidecar:      BuildToolSidecarEnvelopeWithOptions(req, result, profile, sidecarOptions),
		TimeoutOwner: strings.TrimSpace(timeoutOwner),
	}
}

func ToolInvocationTimeoutOwner(timeout time.Duration, margin time.Duration) string {
	if timeout <= 0 {
		return ToolInvocationTimeoutOwnerInternal
	}
	if margin > 0 {
		return ToolInvocationTimeoutOwnerWorkspaceWithMargin
	}

	return ToolInvocationTimeoutOwnerWorkspace
}

func normalizeToolInvocationOptions(opts ToolInvocationOptions) ToolInvocationOptions {
	opts.Profile = strings.TrimSpace(opts.Profile)
	if opts.Profile == "" {
		opts.Profile = ToolProfileDefault
	}
	if opts.RequestFields == "" {
		opts.RequestFields = ToolTelemetryRequestValues
	}
	if opts.PolicyMode == "" {
		opts.PolicyMode = ToolInvocationPolicyWorkspaceProfile
	}
	if strings.TrimSpace(opts.SetupFailureSummary) == "" {
		opts.SetupFailureSummary = "Tool setup failed"
	}

	return opts
}

func (e ToolInvocationExposure) decision(tool string) ToolProfileDecision {
	if len(e.ToolNames) == 0 {
		return ToolProfileDecision{Allowed: true}
	}

	tool = strings.TrimSpace(tool)
	for _, allowed := range e.ToolNames {
		if strings.TrimSpace(allowed) == tool {
			return ToolProfileDecision{Allowed: true}
		}
	}

	reason := strings.TrimSpace(e.DeniedReason)
	if reason == "" {
		reason = "tool is not exposed by this tool surface"
	}

	return ToolProfileDecision{Allowed: false, Code: "profile_denied", Reason: reason}
}

func toolInvocationPolicyDecision(s *ConfiguredToolSurface, req ToolRequest, opts ToolInvocationOptions) ToolProfileDecision {
	if opts.PolicyMode == ToolInvocationPolicyProfile || s == nil {
		return ToolRequestAllowedForProfile(req, opts.Profile)
	}

	return ToolRequestAllowedForWorkspaceProfile(req, opts.Profile, s.PolicyContext)
}

func toolInvocationContext(parent context.Context, timeout time.Duration, margin time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 || margin <= 0 {
		return context.WithCancel(parent)
	}

	return context.WithTimeout(parent, timeout+margin)
}
