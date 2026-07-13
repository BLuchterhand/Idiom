package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/capabilities/workspace"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/readapproval"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("ReadApprovalGate", func() {
	const sessionID = "session-1"

	var (
		decider *MockReadApprovalDecider
		cache   *MockReadApprovalCache
	)

	ginkgo.BeforeEach(func() {
		controller := gomock.NewController(ginkgo.GinkgoT())
		decider = NewMockReadApprovalDecider(controller)
		cache = NewMockReadApprovalCache(controller)
	})

	invoke := func(gate *ReadApprovalGate, path string, code plancontract.PlanExecuteRejectionCode) (bool, string) {
		ginkgo.GinkgoHelper()
		return gate.MaybeOverrideRejection(
			context.Background(),
			sessionID,
			workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: path},
			workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
			code,
			path+" is not an explicit allowed candidate path.",
		)
	}

	expectedRequest := func(path string) readapproval.Request {
		return readapproval.Request{
			SessionID: sessionID,
			Path:      path,
			Tool:      workspace.ToolNameReadFile,
			Lane:      "plan-then-execute-planner",
		}
	}

	decisionMatcher := func(outcome readapproval.Outcome, reason string, source string, path string) gomock.Matcher {
		return gomock.Cond(func(decision readapproval.Decision) bool {
			return decision.Outcome == outcome &&
				decision.Reason == reason &&
				decision.Source == source &&
				decision.Path == path &&
				!decision.At.IsZero()
		})
	}

	ginkgo.It("is inactive without a decider", func() {
		override, denialContext := invoke(
			&ReadApprovalGate{},
			"docs/foo.md",
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.BeEmpty())
	})

	ginkgo.It("does not consult the decider for a non-scope rejection", func() {
		override, denialContext := invoke(
			&ReadApprovalGate{Decider: decider},
			"docs/foo.md",
			plancontract.PlanExecuteRejectionAnchorRefreshCapabilityRequired,
		)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.BeEmpty())
	})

	ginkgo.It("records and emits an operator-approved read", func() {
		path := "docs/design-intent-loop-mutation-cycle.md"
		approved := readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Reason:  readapproval.ReasonOperatorApprovedLive,
			Source:  readapproval.SourceOperator,
		}
		cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, nil)
		decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).Return(approved, nil)
		cache.EXPECT().Record(sessionID, decisionMatcher(
			readapproval.OutcomeApproved,
			readapproval.ReasonOperatorApprovedLive,
			readapproval.SourceOperator,
			path,
		)).Return(nil)

		var origins []string
		gate := &ReadApprovalGate{
			Decider: decider,
			Cache:   cache,
			OnEvent: func(_ readapproval.Request, _ readapproval.Decision, origin string) {
				origins = append(origins, origin)
			},
		}

		override, denialContext := invoke(gate, path, plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope)

		gomega.Expect(override).To(gomega.BeTrue())
		gomega.Expect(denialContext).To(gomega.BeEmpty())
		gomega.Expect(origins).To(gomega.Equal([]string{readapproval.EventOriginLiveDecision}))
	})

	ginkgo.DescribeTable("fails closed without recording a live result returned after cancellation",
		func(liveDecision readapproval.Decision, liveErr error) {
			path := "docs/canceled.md"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, nil)
			decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).DoAndReturn(
				func(callCtx context.Context, _ readapproval.Request) (readapproval.Decision, error) {
					cancel()
					<-callCtx.Done()
					return liveDecision, liveErr
				},
			)

			var events []readapproval.Decision
			gate := &ReadApprovalGate{
				Decider: decider,
				Cache:   cache,
				OnEvent: func(_ readapproval.Request, decision readapproval.Decision, _ string) {
					events = append(events, decision)
				},
			}

			override, denialContext := gate.MaybeOverrideRejection(
				ctx,
				sessionID,
				workspace.ToolRequest{Tool: workspace.ToolNameReadFile, Path: path},
				workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
				plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
				path+" is not an explicit allowed candidate path.",
			)

			gomega.Expect(override).To(gomega.BeFalse())
			gomega.Expect(denialContext).To(gomega.ContainSubstring("canceled"))
			gomega.Expect(events).To(gomega.BeEmpty())
		},
		ginkgo.Entry("late approval", readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Reason:  readapproval.ReasonOperatorApprovedLive,
			Source:  readapproval.SourceOperator,
		}, nil),
		ginkgo.Entry("decider error", readapproval.Decision{}, errors.New("approval UI stopped")),
	)

	ginkgo.It("auto-approves conversation logs without consulting operator state", func() {
		type emittedEvent struct {
			decision readapproval.Decision
			origin   string
		}
		var events []emittedEvent
		gate := &ReadApprovalGate{
			Decider: decider,
			Cache:   cache,
			OnEvent: func(_ readapproval.Request, decision readapproval.Decision, origin string) {
				events = append(events, emittedEvent{decision: decision, origin: origin})
			},
		}

		override, denialContext := invoke(
			gate,
			".idiom/logs/conversations/session.jsonl",
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		)
		gomega.Expect(override).To(gomega.BeTrue())
		gomega.Expect(denialContext).To(gomega.BeEmpty())

		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)
		codexSession := filepath.Join(home, ".codex", "sessions", "2026", "06", "28", "rollout-2026-06-28T12-00-00-session.jsonl")
		override, denialContext = invoke(gate, codexSession, plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope)

		gomega.Expect(override).To(gomega.BeTrue())
		gomega.Expect(denialContext).To(gomega.BeEmpty())
		gomega.Expect(events).To(gomega.HaveLen(2))
		for _, event := range events {
			gomega.Expect(event.decision.Outcome).To(gomega.Equal(readapproval.OutcomeApproved))
			gomega.Expect(event.decision.Reason).To(gomega.Equal(readapproval.ReasonConversationLogDefault))
			gomega.Expect(event.decision.Source).To(gomega.Equal(readapproval.SourceHarnessDefault))
			gomega.Expect(event.decision.At).NotTo(gomega.BeZero())
			gomega.Expect(event.origin).To(gomega.Equal(readapproval.EventOriginPolicyDefault))
		}
	})

	ginkgo.It("fails closed when an approval cannot be recorded", func() {
		path := "docs/not-durable.md"
		approved := readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Reason:  readapproval.ReasonOperatorApprovedLive,
			Source:  readapproval.SourceOperator,
		}
		cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, nil)
		decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).Return(approved, nil)
		cache.EXPECT().Record(sessionID, gomock.Any()).Return(errors.New("disk full"))

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

		override, denialContext := invoke(gate, path, plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.ContainSubstring("could not be recorded"))
		gomega.Expect(gotOrigin).To(gomega.Equal(readapproval.EventOriginCacheRecord))
		gomega.Expect(gotDecision.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
		gomega.Expect(gotDecision.Reason).To(gomega.Equal(readapproval.ReasonCacheUnavailable))
	})

	ginkgo.It("records an operator denial and appends retry guidance", func() {
		path := "docs/foo.md"
		denied := readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Reason:  readapproval.ReasonOperatorDeniedLive,
			Source:  readapproval.SourceOperator,
		}
		cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, nil)
		decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).Return(denied, nil)
		cache.EXPECT().Record(sessionID, decisionMatcher(
			readapproval.OutcomeDenied,
			readapproval.ReasonOperatorDeniedLive,
			readapproval.SourceOperator,
			path,
		)).Return(nil)

		override, denialContext := invoke(
			&ReadApprovalGate{Decider: decider, Cache: cache},
			path,
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.And(
			gomega.ContainSubstring("Operator denied"),
			gomega.ContainSubstring("do not retry"),
		))
	})

	ginkgo.DescribeTable("uses a cached approval for normalized equivalent paths",
		func(requestPath string) {
			cached := readapproval.Decision{
				Outcome: readapproval.OutcomeApproved,
				Reason:  readapproval.ReasonOperatorApprovedLive,
				Source:  readapproval.SourceOperator,
				Path:    "docs/cached-approved.md",
			}
			cache.EXPECT().Lookup(sessionID, "docs/cached-approved.md").Return(cached, true, nil)

			override, denialContext := invoke(
				&ReadApprovalGate{Decider: decider, Cache: cache},
				requestPath,
				plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
			)

			gomega.Expect(override).To(gomega.BeTrue())
			gomega.Expect(denialContext).To(gomega.BeEmpty())
		},
		ginkgo.Entry("canonical path", "docs/cached-approved.md"),
		ginkgo.Entry("dot-relative path", "./docs/cached-approved.md"),
	)

	ginkgo.DescribeTable("uses a cached denial for normalized equivalent paths",
		func(requestPath string) {
			cached := readapproval.Decision{
				Outcome: readapproval.OutcomeDenied,
				Reason:  readapproval.ReasonOperatorDeniedLive,
				Source:  readapproval.SourceOperator,
				Path:    "docs/cached-denied.md",
			}
			cache.EXPECT().Lookup(sessionID, "docs/cached-denied.md").Return(cached, true, nil)

			override, denialContext := invoke(
				&ReadApprovalGate{Decider: decider, Cache: cache},
				requestPath,
				plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
			)

			gomega.Expect(override).To(gomega.BeFalse())
			gomega.Expect(denialContext).To(gomega.ContainSubstring("previously denied"))
		},
		ginkgo.Entry("canonical path", "docs/cached-denied.md"),
		ginkgo.Entry("dot-relative path", "./docs/cached-denied.md"),
	)

	ginkgo.It("fails closed when the cache lookup fails", func() {
		path := "docs/corrupt.md"
		cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, errors.New("corrupt read approval cache"))

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

		override, denialContext := invoke(gate, path, plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.ContainSubstring("cache could not be loaded"))
		gomega.Expect(events).To(gomega.HaveLen(1))
		gomega.Expect(events[0].Reason).To(gomega.Equal(readapproval.ReasonCacheUnavailable))
		gomega.Expect(events[0].Source).To(gomega.Equal(readapproval.SourceCache))
		gomega.Expect(origins).To(gomega.Equal([]string{readapproval.EventOriginCacheLookup}))
	})

	ginkgo.It("records a fail-closed denial when the decider errors", func() {
		path := "docs/foo.md"
		cache.EXPECT().Lookup(sessionID, path).Return(readapproval.Decision{}, false, nil)
		decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).Return(readapproval.Decision{}, errors.New("transport failed"))
		cache.EXPECT().Record(sessionID, decisionMatcher(
			readapproval.OutcomeDenied,
			readapproval.ReasonNoDeciderWired,
			readapproval.SourceNoDecider,
			path,
		)).Return(nil)

		override, denialContext := invoke(
			&ReadApprovalGate{Decider: decider, Cache: cache},
			path,
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
		)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.ContainSubstring("Operator denied"))
	})

	ginkgo.It("does not consult the decider for an empty request path", func() {
		override, denialContext := (&ReadApprovalGate{Decider: decider}).MaybeOverrideRejection(
			context.Background(),
			sessionID,
			workspace.ToolRequest{Tool: workspace.ToolNameSearchContent, Pattern: "foo"},
			workerruntime.WorkerRuntimeBrief{},
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
			"some text",
		)

		gomega.Expect(override).To(gomega.BeFalse())
		gomega.Expect(denialContext).To(gomega.BeEmpty())
	})

	ginkgo.It("normalizes whitespace in the read request before deciding", func() {
		path := "docs/normalized.md"
		decider.EXPECT().DecideRead(gomock.Any(), expectedRequest(path)).Return(readapproval.Decision{
			Outcome: readapproval.OutcomeApproved,
			Reason:  readapproval.ReasonOperatorApprovedLive,
			Source:  readapproval.SourceOperator,
		}, nil)

		override, denialContext := (&ReadApprovalGate{Decider: decider}).MaybeOverrideRejection(
			context.Background(),
			sessionID,
			workspace.ToolRequest{Tool: "  " + workspace.ToolNameReadFile + "  ", Path: " ./" + path + " "},
			workerruntime.WorkerRuntimeBrief{TaskFamily: "plan-then-execute-planner"},
			plancontract.PlanExecuteRejectionReadOutsideAllowedCandidateScope,
			"outside scope",
		)

		gomega.Expect(override).To(gomega.BeTrue())
		gomega.Expect(strings.TrimSpace(denialContext)).To(gomega.BeEmpty())
	})
})
