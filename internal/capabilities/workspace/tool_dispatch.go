package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"idiom/internal/platform/failures"
)

type toolSurfaceHandlerID string

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
	defer func() {
		result = annotateToolFailure(result)
		result = annotateLanguagePrecisionTier(result)
		result = annotateLanguageRelationships(result)
	}()

	if m.EvaluateConfiguredToolRule(req) == ToolRuleDeny {
		return m.toolRuleDeniedResult(req)
	}

	timeout := ToolTimeoutForRequest(req)
	if timeout <= 0 {
		return m.executeDispatch(ctx, req)
	}

	return m.executeWithTimeout(ctx, req, timeout)
}

func (m *Manager) toolRuleDeniedResult(req ToolRequest) ToolResult {
	tool := strings.TrimSpace(req.Tool)
	target := extractToolRuleTarget(tool, req)

	if target == "" {
		target = "<request>"
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

func toolTimeoutFor(tool string) time.Duration {
	spec, ok := builtInToolSurfaceCatalog.lookup(tool)
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

func (m *Manager) executeBuiltInTool(ctx context.Context, req ToolRequest, id toolSurfaceHandlerID) ToolResult {
	switch strings.TrimSpace(string(id)) {
	case ToolNamePwd:
		tool := strings.TrimSpace(req.Tool)
		return ToolResult{
			Tool:    tool,
			Summary: "Printed current working directory",
			Output:  m.cfg.CWD,
		}
	case ToolNameListFiles:
		return m.listFiles(req)
	case ToolNameSearchFiles:
		return m.searchFiles(req)
	case ToolNameSearchContent, ToolNameRg:
		return m.searchContent(ctx, req, strings.TrimSpace(req.Tool))
	case ToolNameReadFile:
		return m.readFile(req)
	case ToolNameFileStats:
		return m.fileStats(req)
	case ToolNameSystemSearchAccess:
		return m.requestSystemSearchAccess(ctx, req)
	case ToolNameCodeNavigate, ToolNameGoMoveDeclarations:
		return m.executeLanguageTool(ctx, req)
	case ToolNameReadOnlyCapability:
		return m.readOnlyCapability(ctx, req)
	case ToolNameSourceSanity:
		return m.sourceSanity(ctx, req)
	case ToolNameCodingRulesCheck:
		return m.codingRulesCheck(ctx, req)
	case ToolNameHarnessAudit:
		return m.harnessAudit(req)
	case ToolNameHarnessFeedback:
		return m.harnessFeedback(req)
	case ToolNameRecoveryInspect:
		return m.recoveryInspect(ctx, req)
	case ToolNameRecoveryStatus:
		return m.recoveryStatus(ctx, req)
	case ToolNameRecoveryDecompose:
		return m.recoveryDecompose(ctx, req)
	case ToolNameProductEfforts:
		return m.productEfforts(req)
	case ToolNameProductStatus:
		return m.productStatus(req)
	case ToolNameSubmitPlan:
		return m.submitPlan(req)
	case ToolNameContextIndex:
		return m.contextIndex(ctx, req)
	case ToolNameContextStatus:
		return m.contextStatus(ctx, req)
	case ToolNameContextQuery:
		return m.contextQuery(ctx, req)
	case ToolNameContextZoom:
		return m.contextZoom(ctx, req)
	case ToolNameContextPack:
		return m.contextPack(ctx, req)
	case ToolNameContextImpact:
		return m.contextImpact(ctx, req)
	case ToolNameContextTracePack:
		return m.contextTracePack(ctx, req)
	case ToolNameDiscoveryQuery:
		return m.discoveryQuery(ctx, req)
	case ToolNameStructuralSearch:
		return m.structuralSearch(ctx, req)
	case ToolNameDependencyGraph:
		return m.dependencyGraph(ctx, req)
	case ToolNameHistoryRange:
		return m.historyRange(ctx, req)
	case ToolNameHistorySymbol:
		return m.historySymbol(ctx, req)
	case ToolNameBlameRange:
		return m.blameRange(ctx, req)
	case ToolNameCtagsSymbols:
		return m.ctagsSymbols(ctx, req)
	case ToolNameSemgrepScan:
		return m.semgrepScan(ctx, req)
	case ToolNameJoernScan:
		return m.joernScan(ctx, req)
	case ToolNameMkdir:
		return m.makeDir(req)
	case ToolNameEditFile:
		return m.editFile(req)
	case ToolNameWriteFile:
		return m.writeFile(req, false)
	case ToolNameAppendFile:
		return m.writeFile(req, true)
	case ToolNameApplyPatch:
		return m.applyPatch(req)
	case ToolNameShell:
		return m.runShell(ctx, req)
	case ToolNameGitStatus:
		return m.gitStatus(ctx, req)
	case ToolNameGitDiff:
		return m.gitDiff(ctx, req)
	default:
		return toolUnsupportedResult(strings.TrimSpace(req.Tool), "Tool request rejected", m.unsupportedToolError(strings.TrimSpace(req.Tool)))
	}
}

func (m *Manager) executeDispatch(ctx context.Context, req ToolRequest) (result ToolResult) {
	tool := strings.TrimSpace(req.Tool)
	mode := m.Mode()

	if reason, ok := exploreModeWriteBlocked(tool, mode); ok {
		return toolPermissionPolicyResult(tool, "Tool request rejected", reason)
	}

	if IsLanguageToolName(tool) {
		return m.executeLanguageTool(ctx, req)
	}

	if handlerID, ok := builtInToolSurfaceCatalog.handlerID(tool); ok {
		return m.executeBuiltInTool(ctx, req, handlerID)
	}

	if strings.HasPrefix(tool, "mcp:") {
		return m.executeMCP(ctx, req)
	}

	if extReg := m.toolSurface.ExternalToolsRegistry(); extReg != nil && extReg.Has(tool) {
		return m.executeExternal(ctx, req)
	}

	return toolUnsupportedResult(tool, "Tool request rejected", m.unsupportedToolError(tool))
}
