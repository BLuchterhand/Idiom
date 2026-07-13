package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/repository/readapprovalcache"
	"idiom/internal/runtime/agent"
)

const readApprovalModeName = "read-scope"
const (
	systemSearchApprovalModeName      = "system-search"
	systemSearchAccessRequestedEvent  = "system_search_access_requested"
	systemSearchAccessDecisionEvent   = "system_search_access_decision"
	systemSearchAccessDecisionSource  = "operator"
	systemSearchAccessOutcomeApproved = "approved"
	systemSearchAccessOutcomeDenied   = "denied"
)

type tuiReadApprovalDecider struct {
	session   *promptExecutionSession
	sessionID string
	cache     agent.ReadApprovalCache
}

func newTuiReadApprovalDecider(session *promptExecutionSession) *tuiReadApprovalDecider {
	d := &tuiReadApprovalDecider{
		session: session,
	}
	if session != nil {
		d.sessionID, _ = workspace.ExecutionSessionIDFromContext(session.promptExecutionContext)
	}
	if session != nil && session.workspace != nil {
		d.cache = readapprovalcache.NewFileCache(session.workspace.Root())
	}

	return d
}

func (d *tuiReadApprovalDecider) DecideRead(ctx context.Context, req readapproval.Request) (readapproval.Decision, error) {
	if d == nil || d.session == nil {
		return readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonNoDeciderWired,
			Source:  readapproval.SourceNoDecider,
			At:      time.Now(),
			Path:    req.Path,
		}, readapproval.ErrNoDecider
	}
	expectedSessionID := strings.TrimSpace(d.sessionID)
	contextSessionID, bound := workspace.ExecutionSessionIDFromContext(ctx)
	if expectedSessionID == "" || !bound || strings.TrimSpace(contextSessionID) != expectedSessionID || strings.TrimSpace(req.SessionID) != expectedSessionID {
		return readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonNoDeciderWired,
			Source:  readapproval.SourceNoDecider,
			At:      time.Now(),
			Path:    req.Path,
		}, fmt.Errorf("read approval execution session does not match the prompt session")
	}
	if err := ctx.Err(); err != nil {
		return readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonOperatorDeniedLive,
			Source:  readapproval.SourceOperator,
			At:      time.Now(),
			Path:    req.Path,
		}, err
	}

	d.emitRequested(req)

	response := d.session.requestApprovalResponse(ctx, workspace.ToolRequest{
		Tool: req.Tool,
		Path: req.Path,
	}, readApprovalModeName, nil)
	if err := ctx.Err(); err != nil {
		return readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonOperatorDeniedLive,
			Source:  readapproval.SourceOperator,
			At:      time.Now(),
			Path:    req.Path,
		}, err
	}

	decision := readapproval.Decision{
		At:   time.Now(),
		Path: req.Path,
	}
	if response.allow {
		decision.Outcome = readapproval.OutcomeApproved
		decision.Reason = readapproval.ReasonOperatorApprovedLive
		decision.Source = readapproval.SourceOperator
	} else {
		decision.Outcome = readapproval.OutcomeDenied
		decision.Reason = readapproval.ReasonOperatorDeniedLive
		decision.Source = readapproval.SourceOperator
	}

	return decision, nil
}

func (d *tuiReadApprovalDecider) emitRequested(req readapproval.Request) {
	if d == nil || d.session == nil || d.session.eventStream == nil {
		return
	}
	d.session.eventStream.emit(map[string]any{
		"event":      readapproval.EventReadApprovalRequested,
		"tool":       req.Tool,
		"path":       req.Path,
		"lane":       req.Lane,
		"session_id": req.SessionID,
		"mode":       readApprovalModeName,
	})
}

func (m *promptExecutionSession) newReadApprovalGate() *agent.ReadApprovalGate {
	if m == nil {
		return nil
	}

	decider := newTuiReadApprovalDecider(m)
	return &agent.ReadApprovalGate{
		Decider: decider,
		Cache:   decider.cache,
		OnEvent: func(req readapproval.Request, decision readapproval.Decision, origin string) {
			if m.eventStream == nil {
				return
			}
			m.eventStream.emit(map[string]any{
				"event":      readapproval.EventReadApprovalDecision,
				"tool":       req.Tool,
				"path":       req.Path,
				"lane":       req.Lane,
				"session_id": req.SessionID,
				"outcome":    decision.Outcome.String(),
				"reason":     decision.Reason,
				"source":     decision.Source,
				"origin":     origin,
			})
		},
	}
}

type tuiSystemSearchAccessApprover struct {
	session *promptExecutionSession
}

func newTuiSystemSearchAccessApprover(session *promptExecutionSession) *tuiSystemSearchAccessApprover {
	return &tuiSystemSearchAccessApprover{session: session}
}

func (a *tuiSystemSearchAccessApprover) ApproveSystemSearchAccess(ctx context.Context, req workspace.SystemSearchAccessRequest) (workspace.SystemSearchAccessDecision, error) {
	if a == nil || a.session == nil {
		return workspace.SystemSearchAccessDecision{}, nil
	}
	if ctx == nil {
		ctx = a.session.promptExecutionContext
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return workspace.SystemSearchAccessDecision{}, ctx.Err()
		default:
		}
	}

	if a.session.eventStream != nil {
		a.session.eventStream.emit(map[string]any{
			"event":       systemSearchAccessRequestedEvent,
			"path":        req.Path,
			"session_id":  req.SessionID,
			"reason":      req.RequestReason,
			"target_kind": req.TargetKind,
			"mode":        systemSearchApprovalModeName,
		})
	}

	response := a.session.requestApprovalResponse(ctx, workspace.ToolRequest{
		Tool:    workspace.ToolNameSystemSearchAccess,
		Path:    req.Path,
		Purpose: req.RequestReason,
	}, systemSearchApprovalModeName, nil)

	if a.session.eventStream != nil {
		outcome := systemSearchAccessOutcomeDenied
		if response.allow {
			outcome = systemSearchAccessOutcomeApproved
		}
		a.session.eventStream.emit(map[string]any{
			"event":      systemSearchAccessDecisionEvent,
			"path":       req.Path,
			"session_id": req.SessionID,
			"outcome":    outcome,
			"source":     systemSearchAccessDecisionSource,
			"scope":      response.scope,
			"mode":       systemSearchApprovalModeName,
		})
	}

	return workspace.SystemSearchAccessDecision{
		Approved: response.allow,
		Scope:    response.scope,
	}, nil
}
