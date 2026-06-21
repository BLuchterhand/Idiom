package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/runtime/workerruntime"
)

type fakeDecider struct {
	decision readapproval.Decision
	err      error
	calls    int
	lastReq  readapproval.Request
}

func (f *fakeDecider) DecideRead(ctx context.Context, req readapproval.Request) (readapproval.Decision, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return readapproval.Decision{}, f.err
	}
	d := f.decision
	if d.Path == "" {
		d.Path = req.Path
	}
	if d.At.IsZero() {
		d.At = time.Now()
	}
	return d, nil
}

type fakeCache struct {
	decisions map[string]readapproval.Decision
	recorded  []readapproval.Decision
}

func newFakeCache() *fakeCache {
	return &fakeCache{decisions: map[string]readapproval.Decision{}}
}

func (c *fakeCache) Lookup(sessionID, path string) (readapproval.Decision, bool) {
	d, ok := c.decisions[path]
	return d, ok
}

func (c *fakeCache) Record(sessionID string, decision readapproval.Decision) error {
	c.recorded = append(c.recorded, decision)
	c.decisions[decision.Path] = decision
	return nil
}

func (c *fakeCache) ApprovedPaths(sessionID string) []string {
	var out []string
	for p, d := range c.decisions {
		if d.Outcome == readapproval.OutcomeApproved {
			out = append(out, p)
		}
	}
	return out
}

func (c *fakeCache) DeniedPaths(sessionID string) []string {
	var out []string
	for p, d := range c.decisions {
		if d.Outcome == readapproval.OutcomeDenied {
			out = append(out, p)
		}
	}
	return out
}

func TestReadApprovalGateInactiveWhenDeciderMissing(t *testing.T) {
	gate := &ReadApprovalGate{}
	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/foo.md"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"original error",
	)
	if override {
		t.Fatal("gate with no decider must never override rejection")
	}
	if denialContext != "" {
		t.Fatalf("gate with no decider must not append denial context, got %q", denialContext)
	}
}

func TestReadApprovalGateIgnoresNonScopeRejection(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{Outcome: readapproval.OutcomeApproved}}
	gate := &ReadApprovalGate{Decider: decider}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/foo.md"},
		workerruntime.WorkerRuntimeBrief{},
		"Anchor refresh capability required",
		"some anchor refresh text",
	)
	if override {
		t.Fatal("non-scope rejection must not be overridable")
	}
	if denialContext != "" {
		t.Fatalf("non-scope rejection must not append denial context, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called for non-scope rejection; got %d calls", decider.calls)
	}
}

func TestReadApprovalGateOverridesWhenOperatorApproves(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
	}}
	cache := newFakeCache()
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/design-intent-loop-mutation-cycle.md"},
		workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
		planExecuteReadScopeRejectionSummary,
		"docs/design-intent-loop-mutation-cycle.md is not an explicit allowed candidate path.",
	)
	if !override {
		t.Fatal("operator-approved read must override rejection")
	}
	if denialContext != "" {
		t.Fatalf("approved override must not append denial context, got %q", denialContext)
	}
	if decider.calls != 1 {
		t.Fatalf("expected exactly one decider call, got %d", decider.calls)
	}
	if len(cache.recorded) != 1 || cache.recorded[0].Outcome != readapproval.OutcomeApproved {
		t.Fatalf("approved decision must be recorded once, got %+v", cache.recorded)
	}
}

func TestReadApprovalGateAppendsOperatorDenialContext(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Reason:  readapproval.ReasonOperatorDeniedLive,
		Source:  readapproval.SourceOperator,
	}}
	cache := newFakeCache()
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/foo.md"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"docs/foo.md is not an explicit allowed candidate path.",
	)
	if override {
		t.Fatal("operator-denied read must not override rejection")
	}
	if !strings.Contains(denialContext, "Operator denied") {
		t.Fatalf("denial context must mention operator denial, got %q", denialContext)
	}
	if !strings.Contains(denialContext, "do not retry") {
		t.Fatalf("denial context must instruct model not to retry, got %q", denialContext)
	}
	if len(cache.recorded) != 1 || cache.recorded[0].Outcome != readapproval.OutcomeDenied {
		t.Fatalf("denied decision must be recorded once, got %+v", cache.recorded)
	}
}

func TestReadApprovalGateCacheShortCircuitsApproved(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
	}}
	cache := newFakeCache()
	cache.decisions["docs/cached-approved.md"] = readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		Path:    "docs/cached-approved.md",
	}
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/cached-approved.md"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"docs/cached-approved.md is not an explicit allowed candidate path.",
	)
	if !override {
		t.Fatal("cached-approved path must override rejection without calling decider")
	}
	if denialContext != "" {
		t.Fatalf("cached-approved override must not append denial context, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called when cache has approved decision; got %d calls", decider.calls)
	}
}

func TestReadApprovalGateCacheShortCircuitsDenied(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
	}}
	cache := newFakeCache()
	cache.decisions["docs/cached-denied.md"] = readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Reason:  readapproval.ReasonOperatorDeniedLive,
		Source:  readapproval.SourceOperator,
		Path:    "docs/cached-denied.md",
	}
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/cached-denied.md"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"docs/cached-denied.md is not an explicit allowed candidate path.",
	)
	if override {
		t.Fatal("cached-denied path must not override rejection")
	}
	if !strings.Contains(denialContext, "previously denied") {
		t.Fatalf("cached-denied context must mention previous denial, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called when cache has denied decision; got %d calls", decider.calls)
	}
}

func TestReadApprovalGateHandlesDeciderErrorAsDenied(t *testing.T) {
	decider := &fakeDecider{err: errors.New("transport failed")}
	cache := newFakeCache()
	gate := &ReadApprovalGate{Decider: decider, Cache: cache}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/foo.md"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"docs/foo.md is not an explicit allowed candidate path.",
	)
	if override {
		t.Fatal("decider-error must not override rejection")
	}
	if denialContext == "" {
		t.Fatal("decider-error should append default operator-denial context")
	}
	if len(cache.recorded) != 1 {
		t.Fatalf("error-path decision must still be recorded for audit, got %d records", len(cache.recorded))
	}
	if cache.recorded[0].Reason != readapproval.ReasonNoDeciderWired {
		t.Fatalf("error-path decision reason must be %q, got %q", readapproval.ReasonNoDeciderWired, cache.recorded[0].Reason)
	}
}

func TestReadApprovalGateIgnoresEmptyPath(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{Outcome: readapproval.OutcomeApproved}}
	gate := &ReadApprovalGate{Decider: decider}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameSearchContent, Pattern: "foo"},
		workerruntime.WorkerRuntimeBrief{},
		planExecuteReadScopeRejectionSummary,
		"some text",
	)
	if override {
		t.Fatal("empty path must not be overridable")
	}
	if denialContext != "" {
		t.Fatalf("empty path must not append denial context, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called for empty path; got %d calls", decider.calls)
	}
}
