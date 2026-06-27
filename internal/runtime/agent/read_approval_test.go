package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
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
	err       error
	recordErr error
}

func newFakeCache() *fakeCache {
	return &fakeCache{decisions: map[string]readapproval.Decision{}}
}

func (c *fakeCache) Lookup(sessionID, path string) (readapproval.Decision, bool, error) {
	if c.err != nil {
		return readapproval.Decision{}, false, c.err
	}
	d, ok := c.decisions[path]
	return d, ok, nil
}

func (c *fakeCache) Record(sessionID string, decision readapproval.Decision) error {
	if c.recordErr != nil {
		return c.recordErr
	}
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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
		plancontract.PlanExecuteRejectionAnchorRefreshCapabilityRequired,
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
	var origins []string
	gate := &ReadApprovalGate{
		Decider: decider,
		Cache:   cache,
		OnEvent: func(_ readapproval.Request, _ readapproval.Decision, origin string) {
			origins = append(origins, origin)
		},
	}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/design-intent-loop-mutation-cycle.md"},
		workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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
	if len(origins) != 1 || origins[0] != readapproval.EventOriginLiveDecision {
		t.Fatalf("expected live decision origin, got %#v", origins)
	}
}

func TestReadApprovalGateAutoApprovesConversationLogReads(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeDenied,
		Reason:  readapproval.ReasonOperatorDeniedLive,
		Source:  readapproval.SourceOperator,
	}}
	cache := newFakeCache()
	var gotDecision readapproval.Decision
	var gotOrigin string
	gate := &ReadApprovalGate{
		Decider: decider,
		Cache:   cache,
		OnEvent: func(_ readapproval.Request, decision readapproval.Decision, origin string) {
			gotDecision = decision
			gotOrigin = origin
		},
	}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: ".idiom/logs/conversations/session.jsonl"},
		workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		".idiom/logs/conversations/session.jsonl is not an explicit allowed candidate path.",
	)

	if !override {
		t.Fatal("conversation log read must be approved by default policy")
	}
	if denialContext != "" {
		t.Fatalf("default-approved conversation log must not append denial context, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called for default-readable conversation log; got %d calls", decider.calls)
	}
	if len(cache.recorded) != 0 {
		t.Fatalf("default policy approval should not write operator cache records, got %#v", cache.recorded)
	}
	if gotDecision.Outcome != readapproval.OutcomeApproved ||
		gotDecision.Reason != readapproval.ReasonConversationLogDefault ||
		gotDecision.Source != readapproval.SourceHarnessDefault ||
		gotOrigin != readapproval.EventOriginPolicyDefault {

		t.Fatalf("unexpected default approval event: decision=%+v origin=%q", gotDecision, gotOrigin)
	}
}

func TestReadApprovalGateFailsClosedWhenLiveDecisionCannotBeRecorded(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
	}}
	cache := newFakeCache()
	cache.recordErr = errors.New("disk full")
	var gotOrigin string
	var gotDecision readapproval.Decision
	gate := &ReadApprovalGate{
		Decider: decider,
		Cache:   cache,
		OnEvent: func(_ readapproval.Request, decision readapproval.Decision, origin string) {
			gotDecision = decision
			gotOrigin = origin
		},
	}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/not-durable.md"},
		workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		"docs/not-durable.md is not an explicit allowed candidate path.",
	)

	if override {
		t.Fatal("read must remain denied when live approval cannot be durably recorded")
	}
	if !strings.Contains(denialContext, "could not be recorded") {
		t.Fatalf("denial context = %q", denialContext)
	}
	if gotOrigin != readapproval.EventOriginCacheRecord {
		t.Fatalf("origin = %q, want %q", gotOrigin, readapproval.EventOriginCacheRecord)
	}
	if gotDecision.Outcome != readapproval.OutcomeDenied || gotDecision.Reason != readapproval.ReasonCacheUnavailable {
		t.Fatalf("decision = %+v, want cache unavailable denial", gotDecision)
	}
	if _, ok, err := cache.Lookup("session-1", "docs/not-durable.md"); err != nil || ok {
		t.Fatalf("failed record must not publish cached approval, ok=%v err=%v", ok, err)
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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

func TestReadApprovalGateCacheShortCircuitsApprovedEquivalentPath(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{Outcome: readapproval.OutcomeDenied}}
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
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "./docs/cached-approved.md"},
		workerruntime.WorkerRuntimeBrief{},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		"./docs/cached-approved.md is not an explicit allowed candidate path.",
	)
	if !override {
		t.Fatal("cached-approved equivalent path must override rejection without calling decider")
	}
	if denialContext != "" {
		t.Fatalf("cached-approved equivalent path must not append denial context, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called when normalized cache has approved decision; got %d calls", decider.calls)
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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

func TestReadApprovalGateCacheShortCircuitsDeniedEquivalentPath(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{Outcome: readapproval.OutcomeApproved}}
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
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "./docs/cached-denied.md"},
		workerruntime.WorkerRuntimeBrief{},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		"./docs/cached-denied.md is not an explicit allowed candidate path.",
	)
	if override {
		t.Fatal("cached-denied equivalent path must not override rejection")
	}
	if !strings.Contains(denialContext, "previously denied") {
		t.Fatalf("cached-denied equivalent context must mention previous denial, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called when normalized cache has denied decision; got %d calls", decider.calls)
	}
}

func TestReadApprovalGateFailsClosedWhenCacheLookupErrors(t *testing.T) {
	decider := &fakeDecider{decision: readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
	}}
	cache := newFakeCache()
	cache.err = errors.New("corrupt read approval cache")
	var events []readapproval.Decision
	var origins []string
	gate := &ReadApprovalGate{
		Decider: decider,
		Cache:   cache,
		OnEvent: func(_ readapproval.Request, decision readapproval.Decision, origin string) {
			events = append(events, decision)
			origins = append(origins, origin)
		},
	}

	override, denialContext := gate.MaybeOverrideRejection(
		context.Background(),
		"session-1",
		workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: "docs/corrupt.md"},
		workerruntime.WorkerRuntimeBrief{},
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		"docs/corrupt.md is not an explicit allowed candidate path.",
	)
	if override {
		t.Fatal("cache error must fail closed")
	}
	if !strings.Contains(denialContext, "cache could not be loaded") {
		t.Fatalf("cache-error context must mention cache load failure, got %q", denialContext)
	}
	if decider.calls != 0 {
		t.Fatalf("decider must not be called when cache lookup errors; got %d calls", decider.calls)
	}
	if len(events) != 1 || events[0].Reason != readapproval.ReasonCacheUnavailable || events[0].Source != readapproval.SourceCache {
		t.Fatalf("cache-error event mismatch: %#v", events)
	}
	if len(origins) != 1 || origins[0] != readapproval.EventOriginCacheLookup {
		t.Fatalf("cache-error origin mismatch: %#v", origins)
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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
		plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
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
