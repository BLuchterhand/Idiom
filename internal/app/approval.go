package app

import (
	"fmt"
	"strings"
	"sync"

	"idiom/internal/workspace"
)

// pendingApproval holds a tool request that hit a ToolRuleAsk decision and
// is blocked waiting for a user response via /approve or /deny. The
// PreExecute hook blocks on response until the UI thread resolves it.
type pendingApproval struct {
	request  workspace.ToolRequest
	mode     string
	response chan bool
}

// summary returns a one-line description the UI can show to the user so
// they know what they're approving.
func (p *pendingApproval) summary() string {
	if p == nil {
		return ""
	}
	req := p.request
	switch strings.TrimSpace(req.Tool) {
	case "shell", "bash":
		return fmt.Sprintf("%s: %s", req.Tool, truncateText(req.Command, 120))
	case "edit_file", "write_file", "append_file":
		return fmt.Sprintf("%s: %s", req.Tool, req.Path)
	case "apply_patch":
		return fmt.Sprintf("apply_patch (%d bytes)", len(req.Content))
	default:
		target := firstNonEmptyString(req.Path, req.Command)
		if target == "" {
			return req.Tool
		}
		return fmt.Sprintf("%s: %s", req.Tool, truncateText(target, 120))
	}
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
	if m == nil {
		return false
	}
	// In headless mode (idiom --prompt --json, idiom artifact, idiom
	// implement) there is no TUI thread to call resolveApproval, so
	// blocking on the response channel hangs the lane forever. Auto-deny
	// instead so the runner's doom-loop / approval-required path closes
	// cleanly with the rejected-blocker shape callers expect.
	if m.eventStream != nil {
		m.emitJSONEvent(map[string]any{
			"event":   "approval_auto_denied",
			"tool":    req.Tool,
			"mode":    modeName,
			"reason":  "headless mode (json event stream active); no TUI available to approve",
			"session": m.eventStream.sessionID,
		})
		return false
	}
	m.approvalSerial.Lock()
	defer m.approvalSerial.Unlock()

	resp := make(chan bool, 1)
	pending := &pendingApproval{
		request:  req,
		mode:     modeName,
		response: resp,
	}
	m.approvalMu.Lock()
	m.approvalPending = pending
	m.approvalMu.Unlock()
	m.events <- infoMsg{text: fmt.Sprintf("Awaiting approval (%s mode): /approve or /deny", modeName)}
	return <-resp
}

// resolveApproval is called from the UI thread when the user types
// /approve or /deny. Returns the request summary for the status line,
// and a bool indicating whether there was a pending request at all.
func (m *Model) resolveApproval(allow bool) (string, bool) {
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
	pending.response <- allow
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
