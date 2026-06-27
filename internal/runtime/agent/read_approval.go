package agent

import (
	"context"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/runtime/workerruntime"
)

type ReadApprovalGate struct {
	Decider ReadApprovalDecider
	Cache   ReadApprovalCache
	OnEvent func(readapproval.Request, readapproval.Decision, string)
}

type ReadApprovalDecider interface {
	DecideRead(ctx context.Context, req readapproval.Request) (readapproval.Decision, error)
}

type ReadApprovalCache interface {
	Lookup(sessionID, path string) (readapproval.Decision, bool, error)
	Record(sessionID string, decision readapproval.Decision) error
	ApprovedPaths(sessionID string) []string
	DeniedPaths(sessionID string) []string
}

func (g *ReadApprovalGate) active() bool {
	return g != nil && g.Decider != nil
}

func (g *ReadApprovalGate) MaybeOverrideRejection(
	ctx context.Context,
	sessionID string,
	req workspace.ToolRequest,
	brief workerruntime.WorkerRuntimeBrief,
	rejectionCode plancontract.PlanExecuteRejectionCode,
	errorText string,
) (override bool, denialContext string) {
	if !g.active() {
		return false, ""
	}

	if rejectionCode != plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope {
		return false, ""
	}

	path := NormalizeRunnerPath(req.Path)
	if path == "" {
		return false, ""
	}

	request := readapproval.Request{
		SessionID: sessionID,
		Path:      path,
		Tool:      strings.TrimSpace(req.Tool),
		Lane:      brief.TaskFamily,
	}

	if workspace.IsDefaultReadableConversationLogPath(path) {
		decision := readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Reason:  readapproval.ReasonConversationLogDefault,
			Source:  readapproval.SourceHarnessDefault,
			At:      time.Now(),
			Path:    path,
		}
		g.emit(request, decision, readapproval.EventOriginPolicyDefault)
		return true, ""
	}

	if g.Cache != nil {
		decision, ok, err := g.Cache.Lookup(sessionID, path)
		if err != nil {
			decision = readapproval.Decision{
				Outcome: readapproval.OutcomeDenied,
				Reason:  readapproval.ReasonCacheUnavailable,
				Source:  readapproval.SourceCache,
				At:      time.Now(),
				Path:    path,
			}
			g.emit(request, decision, readapproval.EventOriginCacheLookup)
			return false, " Read approval cache could not be loaded for the current session; fail closed and do not retry this read."
		}
		if ok {
			g.emit(request, decision, readapproval.EventOriginCacheLookup)
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
	decision.Path = path

	if g.Cache != nil {
		if err := g.Cache.Record(sessionID, decision); err != nil {
			denial := readapproval.Decision{
				Outcome: readapproval.OutcomeDenied,
				Reason:  readapproval.ReasonCacheUnavailable,
				Source:  readapproval.SourceCache,
				At:      time.Now(),
				Path:    path,
			}
			g.emit(request, denial, readapproval.EventOriginCacheRecord)
			return false, " Read approval could not be recorded for the current session; fail closed and do not retry this read."
		}
	}

	g.emit(request, decision, readapproval.EventOriginLiveDecision)

	switch decision.Outcome {
	case readapproval.OutcomeApproved:
		return true, ""
	case readapproval.OutcomeDenied:
		return false, " Operator denied this read for the current session; do not retry."
	default:
		return false, ""
	}
}

func (g *ReadApprovalGate) emit(req readapproval.Request, decision readapproval.Decision, origin string) {
	if g == nil || g.OnEvent == nil {
		return
	}
	g.OnEvent(req, decision, origin)
}
