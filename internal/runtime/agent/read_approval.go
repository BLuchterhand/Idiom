package agent

import (
	"context"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/runtime/workerruntime"
)

const planExecuteReadScopeRejectionSummary = "Plan-execute read outside allowed candidate scope"

type ReadApprovalGate struct {
	Decider   readapproval.Decider
	Cache     readapproval.Cache
	OnEvent   func(readapproval.Request, readapproval.Decision)
}

func (g *ReadApprovalGate) active() bool {
	return g != nil && g.Decider != nil
}

func (g *ReadApprovalGate) MaybeOverrideRejection(
	ctx context.Context,
	sessionID string,
	req workspace.ToolRequest,
	brief workerruntime.WorkerRuntimeBrief,
	summary string,
	errorText string,
) (override bool, denialContext string) {
	if !g.active() {
		return false, ""
	}

	if summary != planExecuteReadScopeRejectionSummary {
		return false, ""
	}

	path := strings.TrimSpace(req.Path)
	if path == "" {
		return false, ""
	}

	request := readapproval.Request{
		SessionID: sessionID,
		Path:      path,
		Tool:      strings.TrimSpace(req.Tool),
		Lane:      brief.TaskFamily,
	}

	if g.Cache != nil {
		if decision, ok := g.Cache.Lookup(sessionID, path); ok {
			g.emit(request, decision)
			switch decision.Outcome {
			case readapproval.OutcomeApproved:
				return true, ""
			case readapproval.OutcomeDenied:
				return false, " Operator previously denied this read for the current session; do not retry."
			}
		}
	}

	decision, err := g.Decider.DecideRead(ctx, request)
	if err != nil {
		decision = readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonNoDeciderWired,
			Source:  readapproval.SourceNoDecider,
			At:      time.Now(),
			Path:    path,
		}
	}
	if decision.At.IsZero() {
		decision.At = time.Now()
	}
	if decision.Path == "" {
		decision.Path = path
	}

	if g.Cache != nil {
		_ = g.Cache.Record(sessionID, decision)
	}

	g.emit(request, decision)

	switch decision.Outcome {
	case readapproval.OutcomeApproved:
		return true, ""
	case readapproval.OutcomeDenied:
		return false, " Operator denied this read for the current session; do not retry."
	default:
		return false, ""
	}
}

func (g *ReadApprovalGate) emit(req readapproval.Request, decision readapproval.Decision) {
	if g == nil || g.OnEvent == nil {
		return
	}
	g.OnEvent(req, decision)
}
