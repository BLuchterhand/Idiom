package session_test

import (
	"context"
	"io"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/session"
	"idiom/internal/runtime/session/sessionmocks"
)

var _ = ginkgo.Describe("Headless prompt sessions", func() {
	ginkgo.It("uses one configured engine for a sequence of prompts", func() {
		controller := gomock.NewController(ginkgo.GinkgoT())
		engine := sessionmocks.NewMockEngine(controller)
		cfg := config.Default()
		cfg.Chat.RequestTimeoutSeconds = 1
		cfg.Chat.ServerStartTimeoutSecs = 1
		opts := session.Options{
			TaskFamily:         "plan_then_execute_planner",
			UseActiveModel:     true,
			AuthorizedNewFiles: []string{"new.go"},
		}

		gomock.InOrder(
			engine.EXPECT().ConfigureHeadlessSession(opts).Times(1),
			engine.EXPECT().DebugLogPath().Return("").Times(1),
			engine.EXPECT().RunPromptOnce(gomock.Any(), "one").Return("answer:one", nil).Times(1),
			engine.EXPECT().RunPromptOnce(gomock.Any(), "two").Return("answer:two", nil).Times(1),
			engine.EXPECT().Close().Return(nil).Times(1),
		)

		promptSession, err := session.Open(cfg, func(config.Config) session.Engine { return engine }, opts, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		answers, err := promptSession.RunPrompts(context.Background(), []string{"one", "two"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answers).To(gomega.Equal([]string{"answer:one", "answer:two"}))
		gomega.Expect(promptSession.Close()).To(gomega.Succeed())
	})

	ginkgo.It("closes its engine exactly once", func() {
		controller := gomock.NewController(ginkgo.GinkgoT())
		engine := sessionmocks.NewMockEngine(controller)
		engine.EXPECT().ConfigureHeadlessSession(session.Options{}).Times(1)
		engine.EXPECT().DebugLogPath().Return("").Times(1)
		engine.EXPECT().Close().Return(nil).Times(1)

		promptSession, err := session.Open(config.Default(), func(config.Config) session.Engine { return engine }, session.Options{}, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(promptSession.Close()).To(gomega.Succeed())
		gomega.Expect(promptSession.Close()).To(gomega.Succeed())
	})

	ginkgo.It("cancels and joins its active turn before closing the engine", ginkgo.Label("race-contract"), ginkgo.SpecTimeout(10*time.Second), func(ctx ginkgo.SpecContext) {
		controller := gomock.NewController(ginkgo.GinkgoT())
		engine := sessionmocks.NewMockEngine(controller)
		started := make(chan struct{})
		returned := make(chan struct{})
		orderingViolation := make(chan struct{}, 1)
		gomock.InOrder(
			engine.EXPECT().ConfigureHeadlessSession(session.Options{}).Times(1),
			engine.EXPECT().DebugLogPath().Return("/tmp/session-debug.jsonl").Times(1),
			engine.EXPECT().RunPromptOnce(gomock.Any(), "active").DoAndReturn(func(turnCtx context.Context, _ string) (string, error) {
				close(started)
				<-turnCtx.Done()
				close(returned)

				return "", turnCtx.Err()
			}).Times(1),
			engine.EXPECT().Close().DoAndReturn(func() error {
				select {
				case <-returned:
				default:
					orderingViolation <- struct{}{}
				}

				return nil
			}).Times(1),
		)

		promptSession, err := session.Open(config.Default(), func(config.Config) session.Engine { return engine }, session.Options{}, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = promptSession.Close() })
		activeResult := make(chan error, 1)
		go func() {
			_, runErr := promptSession.RunPrompt(context.Background(), "active")
			activeResult <- runErr
		}()
		gomega.Eventually(ctx, started).Should(gomega.BeClosed())
		gomega.Expect(promptSession.DebugLogPath()).To(gomega.Equal("/tmp/session-debug.jsonl"))

		queuedResult := make(chan error, 1)
		go func() {
			_, runErr := promptSession.RunPrompt(context.Background(), "queued")
			queuedResult <- runErr
		}()
		closeResult := make(chan error, 1)
		go func() { closeResult <- promptSession.Close() }()

		var activeErr error
		gomega.Eventually(ctx, activeResult).Should(gomega.Receive(&activeErr))
		gomega.Expect(activeErr).To(gomega.MatchError(context.Canceled))
		var queuedErr error
		gomega.Eventually(ctx, queuedResult).Should(gomega.Receive(&queuedErr))
		gomega.Expect(queuedErr).To(gomega.MatchError(session.ErrSessionClosed))
		gomega.Eventually(ctx, closeResult).Should(gomega.Receive(gomega.Succeed()))
		gomega.Expect(orderingViolation).NotTo(gomega.Receive())
	})

	ginkgo.It("rejects prompt admission after Close", ginkgo.Label("race-contract"), func() {
		controller := gomock.NewController(ginkgo.GinkgoT())
		engine := sessionmocks.NewMockEngine(controller)
		gomock.InOrder(
			engine.EXPECT().ConfigureHeadlessSession(session.Options{}).Times(1),
			engine.EXPECT().DebugLogPath().Return("").Times(1),
			engine.EXPECT().Close().Return(nil).Times(1),
		)

		promptSession, err := session.Open(config.Default(), func(config.Config) session.Engine { return engine }, session.Options{}, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(promptSession.Close()).To(gomega.Succeed())
		_, err = promptSession.RunPrompt(context.Background(), "too late")
		gomega.Expect(err).To(gomega.MatchError(session.ErrSessionClosed))
	})
})
