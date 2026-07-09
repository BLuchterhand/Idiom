package app

import (
	"fmt"
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
	mode     string
	response chan approvalResponse
}

type approvalResponse struct {
	allow bool
	scope workspace.SystemSearchAccessScope
}

// summary returns a one-line description the UI can show to the user so
// they know what they're approving.
func (p *pendingApproval) summary() string {
	if p == nil {
		return ""
	}

	req := p.request
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

// requestApproval is called from the runner goroutine when a tool hits
// an Ask rule. It registers the pending request, signals the UI, and
// blocks until the UI resolves it via /approve or /deny.
//
// Only one approval can be outstanding at a time. If another tool call
// hits Ask while a prior one is still unresolved, the caller blocks on
// approvalSerial until the UI unblocks the slot. This preserves the
// invariant that the banner always matches a real request the user is
// being asked about — no silent clobbers.
func (m *Model) requestApproval(req workspace.ToolRequest, modeName string) bool {
	return m.requestApprovalResponse(req, modeName).allow
}

func (m *Model) requestApprovalResponse(req workspace.ToolRequest, modeName string) approvalResponse {
	if m == nil {
		return approvalResponse{}
	}

	// In headless mode (idiom run --prompt --json, idiom artifact, idiom
	// implement) there is no TUI thread to call resolveApproval, so
	// blocking on the response channel hangs the lane forever. Auto-deny
	// instead so the runner's doom-loop / approval-required path closes
	// cleanly with the rejected-blocker shape callers expect.
	if m.eventStream != nil || m.suppressPromptTranscript || m.nonInteractive {
		reason := "headless mode (json event stream active); no TUI available to approve"
		if m.eventStream == nil {
			reason = "headless prompt runner; no TUI available to approve"
		}

		if m.eventStream == nil {
			return approvalResponse{}
		}

		m.emitJSONEvent(map[string]any{
			"event":   "approval_auto_denied",
			"tool":    req.Tool,
			"mode":    modeName,
			"reason":  reason,
			"session": m.eventStream.sessionID,
		})

		return approvalResponse{}
	}

	m.approvalSerial.Lock()
	defer m.approvalSerial.Unlock()

	resp := make(chan approvalResponse, 1)
	pending := &pendingApproval{
		request:  req,
		mode:     modeName,
		response: resp,
	}

	m.approvalMu.Lock()

	m.approvalPending = pending
	m.approvalMu.Unlock()

	m.events <- infoMsg{text: fmt.Sprintf("Awaiting approval (%s mode): %s", modeName, pending.approveHint())}

	return <-resp
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
