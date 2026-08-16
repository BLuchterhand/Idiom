package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/failures"
	"idiom/internal/repository/semantic/languagetools"
)

func exploreModeWriteBlocked(tool string, mode Mode) (string, bool) {
	if mode != ModeExplore || !IsWorkspaceWriteTool(tool) {
		return "", false
	}

	return "explore mode is read-only; leave explore mode before using mutating workspace tools", true
}

// Execute dispatches a tool request and wraps the dispatch in a per-tool
// wall-clock timeout so a misbehaving backend cannot stall the lane
// indefinitely.
func (m *Manager) Execute(ctx context.Context, req ToolRequest) (result ToolResult) {
	envelope := NewToolExecutionEnvelope(toolTransportFromContext(ctx), req)
	defer func() {
		envelope.applyResult(result, ctx != nil && ctx.Err() != nil)
		m.observeToolExecution(envelope)
		if observer := toolExecutionObserverFromContext(ctx); observer != nil {
			observer(envelope)
		}
	}()

	ctx = m.snapshotExecutionSession(ctx)
	ctx = m.snapshotSystemSearchAccessApprover(ctx)
	ctx = m.snapshotMutationApprover(ctx)
	req.executionSessionIdentity, _ = executionSessionIdentityFromContext(ctx)

	defer func() {
		result = annotateToolFailure(result)
		result = annotateLanguagePrecisionTier(result)
		result = annotateLanguageRelationships(result)
	}()

	if decision := m.EvaluateConfiguredToolRule(req); decision.Decision == toolpolicy.Deny {
		envelope.PreflightDecision = decision.Decision
		envelope.Status = ToolExecutionDenied
		return m.toolRuleDeniedResult(req, decision.Target)
	}
	var authorizationFailure *ToolResult
	ctx, authorizationFailure = m.authorizeMutationRequest(ctx, req)
	if authorizationFailure != nil {
		envelope.PreflightDecision = toolpolicy.Ask
		envelope.ApprovalRequired = strings.Contains(strings.ToLower(authorizationFailure.Error), "approval")
		if envelope.ApprovalRequired {
			envelope.Status = ToolExecutionApproval
		} else {
			envelope.Status = ToolExecutionDenied
		}
		return *authorizationFailure
	}
	envelope.PreflightDecision = m.EvaluateToolRule(req)
	if envelope.PreflightDecision == toolpolicy.None {
		envelope.PreflightDecision = toolpolicy.Allow
	}
	envelope.Status = ToolExecutionAdmitted

	timeout := m.ToolTimeoutForRequest(req)
	if timeout <= 0 {
		result = m.executeDispatch(ctx, req)
	} else if identityBoundMutationTool(req.Tool) {
		// These handlers own commit accounting and context checks. Running
		// them with the same deadline in the caller goroutine ensures a
		// cancellation cannot replace an exact partial-commit result with
		// the generic timeout wrapper's synthetic response.
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		result = m.executeDispatch(timeoutCtx, req)
		cancel()
	} else {
		result = m.executeWithTimeout(ctx, req, timeout)
	}
	m.commitAcceptedSystemSearchGrant(req.executionSessionIdentity, &result)

	return result
}

func identityBoundMutationTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case ToolNameMkdir, ToolNameWriteFile, ToolNameAppendFile, ToolNameEditFile, ToolNameApplyPatch, ToolNameGoMoveDeclarations:
		return true
	default:
		return false
	}
}

func (m *Manager) toolRuleDeniedResult(req ToolRequest, target string) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	target = strings.TrimSpace(target)
	if target == "" {
		target = toolPolicyDisplayTarget(req)
	}

	return toolPermissionPolicyResult(tool, "Tool request denied by workspace tool_rules", fmt.Sprintf("workspace tool_rules deny %s on %s", tool, target))
}

// executeWithTimeout runs the tool dispatch in a goroutine and races it
// against a context deadline. If the dispatch returns first, its result is
// used; if the deadline trips first, a synthesized timeout result is returned
// and the goroutine is left to drain.
func (m *Manager) executeWithTimeout(ctx context.Context, req ToolRequest, timeout time.Duration) ToolResult {
	return m.executeWithTimeoutFn(ctx, req, timeout, func(timeoutCtx context.Context) ToolResult {
		return m.executeDispatch(timeoutCtx, req)
	})
}

// executeWithTimeoutFn is the testable core of executeWithTimeout.
func (m *Manager) executeWithTimeoutFn(ctx context.Context, req ToolRequest, timeout time.Duration, fn func(context.Context) ToolResult) ToolResult {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan ToolResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- ToolResult{
					Tool:    req.Tool,
					Summary: "Tool panicked",
					Error:   fmt.Sprintf("%s panicked while executing: %v", strings.TrimSpace(req.Tool), r),
					Failure: failures.New(failures.Internal, failures.NoReason, fmt.Sprintf("%s panicked while executing", strings.TrimSpace(req.Tool))),
				}
			}
		}()

		done <- fn(timeoutCtx)
	}()

	select {
	case res := <-done:
		return res
	case <-timeoutCtx.Done():
		if strings.TrimSpace(req.Tool) == ToolNameContextPack {
			return contextPackFallbackResult(req, timeout, timeoutCtx.Err())
		}

		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return ToolResult{
				Tool:    req.Tool,
				Summary: "Tool timed out",
				Error:   fmt.Sprintf("%s did not return within %s; the runner is moving on. Treat this as a tool stall and pick a different approach (e.g., a different tool, narrower input, or a per-path blocker).", strings.TrimSpace(req.Tool), timeout),
				Failure: failures.New(failures.DeadlineExceeded, failures.NoReason, fmt.Sprintf("%s did not return within %s", strings.TrimSpace(req.Tool), timeout)),
			}
		}

		return ToolResult{
			Tool:    req.Tool,
			Summary: "Tool canceled",
			Error:   "the run was canceled before this tool returned",
			Failure: failures.New(failures.Canceled, failures.NoReason, "the run was canceled before this tool returned"),
		}
	}
}

// ToolTimeoutForRequest returns the per-tool wall-clock budget used by
// executeWithTimeout.
func ToolTimeoutForRequest(req ToolRequest) time.Duration {
	tool := strings.TrimSpace(req.Tool)
	if tool == ToolNameContextStatus && req.Refresh {
		return toolTimeoutFor(ToolNameContextIndex)
	}

	return toolTimeoutFor(tool)
}

// ToolTimeoutForRequest returns the effective wall-clock budget for this
// manager, including registered external tool timeout declarations.
func (m *Manager) ToolTimeoutForRequest(req ToolRequest) time.Duration {
	toolName := strings.TrimSpace(req.Tool)
	if m != nil {
		if registry := m.toolSurface.ExternalToolsRegistry(); registry != nil {
			if tool, ok := registry.Tool(toolName); ok {
				if tool.TimeoutSeconds > 0 {
					return time.Duration(tool.TimeoutSeconds) * time.Second
				}

				return 30 * time.Second
			}
		}
	}

	return ToolTimeoutForRequest(req)
}

func toolTimeoutFor(tool string) time.Duration {
	spec, ok := builtInToolSurfaceCatalog().lookup(tool)
	if !ok {
		return 30 * time.Second
	}

	// shell has no outer timeout; the shell handler enforces its own
	// CommandTimeoutSeconds. Skip the outer timeout to avoid double-bounding.
	if tool == ToolNameShell {
		return 0
	}

	if spec.Timeout > 0 {
		return spec.Timeout
	}

	return 30 * time.Second
}

func (m *Manager) pwd(_ context.Context, req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	return ToolResult{
		Tool:    tool,
		Summary: "Printed current working directory",
		Output:  m.cfg.CWD,
	}
}

func (m *Manager) executeDispatch(ctx context.Context, req ToolRequest) (result ToolResult) {
	tool := strings.TrimSpace(req.Tool)
	mode := m.Mode()

	if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
		return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
	}

	if languagetools.IsToolName(tool) {
		return m.executeLanguageTool(ctx, req)
	}

	if handler, ok := builtInToolSurfaceCatalog().handler(tool); ok {
		return handler(m, ctx, req)
	}

	if strings.HasPrefix(tool, "mcp:") {
		return m.executeMCP(ctx, req)
	}

	if extReg := m.toolSurface.ExternalToolsRegistry(); extReg != nil && extReg.Has(tool) {
		return m.executeExternal(ctx, req)
	}

	return toolUnsupportedResult(tool, "Tool request rejected", m.unsupportedToolError(tool))
}
