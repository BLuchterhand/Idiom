package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/textutil"
)

// pendingApproval holds a tool request that hit an ask permission decision and
// is blocked waiting for a user response via /approve or /deny. The
// PreExecute hook blocks on response until the UI thread resolves it.
type pendingApproval struct {
	request  workspace.ToolRequest
	targets  []string
	mode     string
	response chan approvalResponse
}

type approvalResponse struct {
	allow bool
	scope workspace.SystemSearchAccessScope
}

// approvalAwaitingMsg carries the identity of the request that owns an
// awaiting-approval status. Update ignores the message after that request is
// resolved, so delayed UI delivery cannot replace a newer terminal status.
type approvalAwaitingMsg struct {
	pending *pendingApproval
}

// summary returns a one-line description the UI can show to the user so
// they know what they're approving.
func (p *pendingApproval) summary() string {
	if p == nil {
		return ""
	}

	req := p.request
	if len(p.targets) > 0 {
		return fmt.Sprintf("%s: %s", req.Tool, textutil.Truncate(strings.Join(p.targets, ", "), 120))
	}
	switch strings.TrimSpace(req.Tool) {
	case workspace.ToolNameShell, "bash":
		return fmt.Sprintf("%s: %s", req.Tool, textutil.Truncate(strings.TrimSpace(req.Command), 120))
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile:
		return fmt.Sprintf("%s: %s", req.Tool, req.Path)
	case workspace.ToolNameApplyPatch:
		return fmt.Sprintf("apply_patch (%d bytes)", len(req.Content))
	default:
		target := textutil.FirstNonEmpty(req.Path, req.Command)
		if target == "" {
			return req.Tool
		}

		return fmt.Sprintf("%s: %s", req.Tool, textutil.Truncate(strings.TrimSpace(target), 120))
	}
}

func (p *pendingApproval) approveHint() string {
	if p == nil {
		return "/approve or /deny"
	}
	if p.mode == systemSearchApprovalModeName {
		return "/approve for selected path, /approve all for folder contents, or /deny"
	}

	return "/approve or /deny"
}

// requestApproval is called from the prompt execution goroutine when a tool
// hits an Ask rule. It transfers pending-request ownership to Update and blocks
// until the UI resolves it via /approve or /deny. Serializing requests keeps
// the visible banner bound to the request that is actually waiting.
func (m *promptExecutionSession) requestApproval(req workspace.ToolRequest, modeName string) bool {
	return m.requestApprovalResponse(m.promptExecutionContext, req, modeName, nil).allow
}

func (m *promptExecutionSession) requestApprovalResponse(ctx context.Context, req workspace.ToolRequest, modeName string, targets []string) approvalResponse {
	if m == nil {
		return approvalResponse{}
	}
	if ctx == nil {
		ctx = m.promptExecutionContext
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return approvalResponse{}
	default:
	}
	if m.eventStream != nil || m.nonInteractive {
		if m.eventStream != nil {
			m.eventStream.emit(map[string]any{
				"event":   "approval_auto_denied",
				"tool":    req.Tool,
				"mode":    modeName,
				"reason":  "headless mode (json event stream active); no TUI available to approve",
				"session": m.eventStream.sessionID,
			})
		}

		return approvalResponse{}
	}

	m.approvalSerial.Lock()
	defer m.approvalSerial.Unlock()

	response := make(chan approvalResponse, 1)
	pending := &pendingApproval{
		request:  req,
		targets:  append([]string(nil), targets...),
		mode:     modeName,
		response: response,
	}
	m.queueUIEvent(approvalPendingMsg{pending: pending})
	m.queueUIEvent(approvalAwaitingMsg{pending: pending})

	select {
	case decision := <-response:
		select {
		case <-ctx.Done():
			return approvalResponse{}
		default:
			return decision
		}
	case <-ctx.Done():
		m.queueUIEvent(approvalClearedMsg{pending: pending})

		return approvalResponse{}
	}
}

// tuiMutationApprover is the trusted UI adapter for workspace-owned mutation
// authorization. It can only answer for the immutable execution session that
// created it; workspace remains responsible for canonical scope checks and
// the one-use authorization receipt.
type tuiMutationApprover struct {
	session   *promptExecutionSession
	sessionID string
}

func (a *tuiMutationApprover) ApproveWorkspaceMutation(ctx context.Context, req workspace.MutationApprovalRequest) (workspace.MutationApprovalDecision, error) {
	if a == nil || a.session == nil {
		return workspace.MutationApprovalDecision{}, fmt.Errorf("TUI mutation approver is not configured")
	}
	expectedSessionID := strings.TrimSpace(a.sessionID)
	contextSessionID, bound := workspace.ExecutionSessionIDFromContext(ctx)
	if expectedSessionID == "" || !bound || strings.TrimSpace(contextSessionID) != expectedSessionID || strings.TrimSpace(req.SessionID) != expectedSessionID {
		return workspace.MutationApprovalDecision{}, fmt.Errorf("mutation approval execution session does not match the prompt session")
	}
	if err := ctx.Err(); err != nil {
		return workspace.MutationApprovalDecision{}, err
	}

	displayTargets := make([]string, 0, len(req.Targets))
	for _, target := range req.Targets {
		displayPath := textutil.FirstNonEmpty(strings.TrimSpace(target.RequestedPath), strings.TrimSpace(target.Identity.CanonicalID))
		if displayPath != "" && !slices.Contains(displayTargets, displayPath) {
			displayTargets = append(displayTargets, displayPath)
		}
	}
	request := workspace.ToolRequest{
		Tool:        strings.TrimSpace(req.Tool),
		Description: strings.TrimSpace(req.Description),
	}
	if len(displayTargets) == 1 {
		request.Path = displayTargets[0]
	}
	modeName := string(currentMode(a.session.agentModeName, a.session.cfg.Chat.AgentMode).Name)
	response := a.session.requestApprovalResponse(ctx, request, modeName, displayTargets)
	if err := ctx.Err(); err != nil {
		return workspace.MutationApprovalDecision{}, err
	}
	return workspace.MutationApprovalDecision{Approved: response.allow}, nil
}

func (m *Model) handleApprovalAwaiting(msg approvalAwaitingMsg) {
	if m == nil || msg.pending == nil {
		return
	}

	if m.currentApproval() != msg.pending {
		return
	}

	m.status = fmt.Sprintf("Awaiting approval (%s mode): %s", msg.pending.mode, msg.pending.approveHint())
}

// resolveApproval is called from the UI thread when the user types
// /approve or /deny. Returns the request summary for the status line,
// and a bool indicating whether there was a pending request at all.
func (m *Model) resolveApproval(allow bool) (string, bool) {
	scope := workspace.SystemSearchAccessScopeSelected
	if !allow {
		scope = ""
	}

	return m.resolveApprovalWithScope(allow, scope)
}

func (m *Model) resolveApprovalWithScope(allow bool, scope workspace.SystemSearchAccessScope) (string, bool) {
	if m == nil {
		return "", false
	}

	m.approvalMu.Lock()

	pending := m.approvalPending

	m.approvalPending = nil
	m.approvalMu.Unlock()

	if pending == nil {
		return "", false
	}

	pending.response <- approvalResponse{allow: allow, scope: scope}

	return pending.summary(), true
}

// currentApproval snapshots the pending request for rendering, without
// holding the lock across render calls.
func (m *Model) currentApproval() *pendingApproval {
	if m == nil {
		return nil
	}

	m.approvalMu.Lock()
	defer m.approvalMu.Unlock()

	return m.approvalPending
}

// approvalMutex exposure is via a struct field, not this helper, but
// keeping the accessor documented here clarifies the ownership story.
var _ = (*sync.Mutex)(nil)
