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
