package app

import (
	"context"
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
	model *Model
	cache agent.ReadApprovalCache
}

func newTuiReadApprovalDecider(m *Model) *tuiReadApprovalDecider {
	d := &tuiReadApprovalDecider{
		model: m,
	}
	if m != nil && m.workspace != nil {
		d.cache = readapprovalcache.NewFileCache(m.workspace.Root())
	}
	return d
}

func (d *tuiReadApprovalDecider) DecideRead(ctx context.Context, req readapproval.Request) (readapproval.Decision, error) {
	if d == nil || d.model == nil {
		return readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonNoDeciderWired,
			Source:  readapproval.SourceNoDecider,
			At:      time.Now(),
			Path:    req.Path,
		}, readapproval.ErrNoDecider
	}

	d.emitRequested(req)

	approved := d.model.requestApproval(workspace.ToolRequest{
		Tool: req.Tool,
		Path: req.Path,
	}, readApprovalModeName)

	decision := readapproval.Decision{
		At:   time.Now(),
		Path: req.Path,
	}
	if approved {
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
	if d == nil || d.model == nil || d.model.eventStream == nil {
		return
	}
	d.model.emitJSONEvent(map[string]any{
		"event":      readapproval.EventReadApprovalRequested,
		"tool":       req.Tool,
		"path":       req.Path,
		"lane":       req.Lane,
		"session_id": req.SessionID,
		"mode":       readApprovalModeName,
	})
}

func (m *Model) newReadApprovalGate() *agent.ReadApprovalGate {
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
			m.emitJSONEvent(map[string]any{
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
	model *Model
}

func newTuiSystemSearchAccessApprover(m *Model) *tuiSystemSearchAccessApprover {
	return &tuiSystemSearchAccessApprover{model: m}
}

func (a *tuiSystemSearchAccessApprover) ApproveSystemSearchAccess(_ context.Context, req workspace.SystemSearchAccessRequest) (workspace.SystemSearchAccessDecision, error) {
	if a == nil || a.model == nil {
		return workspace.SystemSearchAccessDecision{}, nil
	}

	if a.model.eventStream != nil {
		a.model.emitJSONEvent(map[string]any{
			"event":       systemSearchAccessRequestedEvent,
			"path":        req.Path,
			"session_id":  req.SessionID,
			"reason":      req.RequestReason,
			"target_kind": req.TargetKind,
			"mode":        systemSearchApprovalModeName,
		})
	}

	response := a.model.requestApprovalResponse(workspace.ToolRequest{
		Tool:    workspace.ToolNameSystemSearchAccess,
		Path:    req.Path,
		Purpose: req.RequestReason,
	}, systemSearchApprovalModeName)

	if a.model.eventStream != nil {
		outcome := systemSearchAccessOutcomeDenied
		if response.allow {
			outcome = systemSearchAccessOutcomeApproved
		}
		a.model.emitJSONEvent(map[string]any{
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

func (m *Model) wireSystemSearchAccessApprover() {
	if m == nil || m.workspace == nil {
		return
	}

	m.workspace.SetSystemSearchAccessApprover(newTuiSystemSearchAccessApprover(m))
}
