package cmdruntime

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/evidence/telemetry"
	"idiom/internal/platform/config"
	runtimesession "idiom/internal/runtime/session"
	"idiom/internal/runtime/session/sessionmocks"
)

var _ = ginkgo.Describe("CLI runtime composition", func() {
	ginkgo.It("rejects TUI startup when stdout and stderr are not terminals", func() {
		err := NewTUIRunner(nil)(config.Default(), RunOptions{})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("requires a terminal")))
	})

	ginkgo.It("applies identical session options to single and multi-prompt runners", func() {
		controller := gomock.NewController(ginkgo.GinkgoT())
		first := sessionmocks.NewMockEngine(controller)
		second := sessionmocks.NewMockEngine(controller)
		engines := []runtimesession.Engine{first, second}
		factoryCalls := 0
		factory := func(config.Config) runtimesession.Engine {
			engine := engines[factoryCalls]
			factoryCalls++
			return engine
		}
		promptRun, promptsRun := NewHeadlessRunners(factory)
		opts := RunOptions{
			TaskFamily:                  "plan_then_execute_planner",
			PlannerOwnedVerification:    true,
			MaxTokensOverride:           2048,
			DisableThinking:             true,
			ReviewRepairOpenObligations: []string{"tests"},
			UseActiveModel:              true,
			ScopeContract:               plancontract.NewPlanScopeContract([]string{"a.go"}, []string{"b.go"}, nil),
			AuthorizedNewFiles:          []string{"new.go"},
		}
		want := runtimesession.Options{
			TaskFamily:                  opts.TaskFamily,
			PlannerOwnedVerification:    opts.PlannerOwnedVerification,
			MaxTokensOverride:           opts.MaxTokensOverride,
			DisableThinking:             opts.DisableThinking,
			ReviewRepairOpenObligations: []string{"tests"},
			UseActiveModel:              true,
			ScopeContract:               opts.ScopeContract,
			AuthorizedNewFiles:          []string{"new.go"},
		}

		first.EXPECT().ConfigureHeadlessSession(want).Times(1)
		first.EXPECT().DebugLogPath().Return("").Times(1)
		first.EXPECT().RunPromptOnce(gomock.Any(), "one").Return("one", nil).Times(1)
		first.EXPECT().Close().Return(nil).Times(1)
		second.EXPECT().ConfigureHeadlessSession(want).Times(1)
		second.EXPECT().DebugLogPath().Return("").Times(1)
		gomock.InOrder(
			second.EXPECT().RunPromptOnce(gomock.Any(), "one").Return("one", nil).Times(1),
			second.EXPECT().RunPromptOnce(gomock.Any(), "two").Return("two", nil).Times(1),
		)
		second.EXPECT().Close().Return(nil).Times(1)

		answer, err := promptRun(config.Default(), "one", opts, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal("one"))
		answers, err := promptsRun(config.Default(), []string{"one", "two"}, opts, io.Discard)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answers).To(gomega.Equal([]string{"one", "two"}))
		gomega.Expect(factoryCalls).To(gomega.Equal(2))
	})

	ginkgo.It("serializes signal closeout after an active Bubble Tea update", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
		recorder, err := telemetry.Open(telemetry.Options{
			Dir:           ginkgo.GinkgoT().TempDir(),
			Command:       "tui-signal-race-contract",
			WorkspaceRoot: "/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = recorder.CloseWithFields(0, nil) })

		programContext, cancelProgram := context.WithCancel(ctx)
		ginkgo.DeferCleanup(cancelProgram)

		model := newBlockingInteractiveModel(true, false)
		ginkgo.DeferCleanup(model.releaseBlockedOperations)
		programDone := make(chan error, 1)
		go func() {
			programDone <- runInteractiveModel(
				programContext,
				model,
				RunOptions{Telemetry: recorder},
				tea.WithInput(nil),
				tea.WithOutput(io.Discard),
				tea.WithoutRenderer(),
			)
		}()

		gomega.Eventually(model.updateStarted).Should(gomega.BeClosed())
		closeoutDone := make(chan error, 1)
		go func() {
			closeoutDone <- recorder.CloseWithFields(130, map[string]any{"reason": "signal"})
		}()

		gomega.Consistently(model.closeStarted, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(gomega.BeClosed())
		model.releaseBlockedUpdate()

		var programErr error
		gomega.Eventually(programDone).Should(gomega.Receive(&programErr))
		gomega.Expect(errors.Is(programErr, tea.ErrProgramKilled)).To(gomega.BeTrue(), "program error: %v", programErr)
		var closeoutErr error
		gomega.Eventually(closeoutDone).Should(gomega.Receive(&closeoutErr))
		gomega.Expect(closeoutErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(model.closeCount.Load()).To(gomega.Equal(int32(1)))
		gomega.Expect(model.closeDuringUpdate.Load()).To(gomega.BeFalse())
	}, ginkgo.SpecTimeout(5*time.Second))

	ginkgo.It("cancels signal closeout safely before Bubble Tea starts", ginkgo.Label("race-contract"), func(ctx ginkgo.SpecContext) {
		recorder, err := telemetry.Open(telemetry.Options{
			Dir:           ginkgo.GinkgoT().TempDir(),
			Command:       "tui-prestart-signal-race-contract",
			WorkspaceRoot: "/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(func() { _ = recorder.CloseWithFields(0, nil) })

		programContext, cancelProgram := context.WithCancel(ctx)
		ginkgo.DeferCleanup(cancelProgram)

		model := newBlockingInteractiveModel(false, true)
		ginkgo.DeferCleanup(model.releaseBlockedOperations)
		programDone := make(chan error, 1)
		go func() {
			programDone <- runInteractiveModel(
				programContext,
				model,
				RunOptions{Telemetry: recorder, Resume: "resume-before-start"},
				tea.WithInput(nil),
				tea.WithOutput(io.Discard),
				tea.WithoutRenderer(),
			)
		}()

		gomega.Eventually(model.resumeStarted).Should(gomega.BeClosed())
		closeoutDone := make(chan error, 1)
		go func() {
			closeoutDone <- recorder.CloseWithFields(143, map[string]any{"reason": "signal"})
		}()

		gomega.Consistently(model.closeStarted, 100*time.Millisecond, 10*time.Millisecond).ShouldNot(gomega.BeClosed())
		model.releaseBlockedResume()

		var programErr error
		gomega.Eventually(programDone).Should(gomega.Receive(&programErr))
		gomega.Expect(errors.Is(programErr, tea.ErrProgramKilled)).To(gomega.BeTrue(), "program error: %v", programErr)
		var closeoutErr error
		gomega.Eventually(closeoutDone).Should(gomega.Receive(&closeoutErr))
		gomega.Expect(closeoutErr).NotTo(gomega.HaveOccurred())
		gomega.Expect(model.closeCount.Load()).To(gomega.Equal(int32(1)))
		gomega.Expect(model.closeDuringUpdate.Load()).To(gomega.BeFalse())
	}, ginkgo.SpecTimeout(5*time.Second))
})

// test-double: observation-probe
type blockingInteractiveModel struct {
	emitUpdate        bool
	blockResume       bool
	updateStarted     chan struct{}
	releaseUpdate     chan struct{}
	resumeStarted     chan struct{}
	releaseResume     chan struct{}
	closeStarted      chan struct{}
	closeStartedOnce  sync.Once
	releaseUpdateOnce sync.Once
	releaseResumeOnce sync.Once
	updateActive      atomic.Bool
	closeDuringUpdate atomic.Bool
	closeCount        atomic.Int32
}

type blockingInteractiveUpdateMsg struct{}

func newBlockingInteractiveModel(emitUpdate bool, blockResume bool) *blockingInteractiveModel {
	return &blockingInteractiveModel{
		emitUpdate:    emitUpdate,
		blockResume:   blockResume,
		updateStarted: make(chan struct{}),
		releaseUpdate: make(chan struct{}),
		resumeStarted: make(chan struct{}),
		releaseResume: make(chan struct{}),
		closeStarted:  make(chan struct{}),
	}
}

func (model *blockingInteractiveModel) Init() tea.Cmd {
	if !model.emitUpdate {
		return nil
	}

	return func() tea.Msg { return blockingInteractiveUpdateMsg{} }
}

func (model *blockingInteractiveModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := message.(blockingInteractiveUpdateMsg); !ok {
		return model, nil
	}

	model.updateActive.Store(true)
	close(model.updateStarted)
	<-model.releaseUpdate
	model.updateActive.Store(false)

	return model, nil
}

func (model *blockingInteractiveModel) View() string { return "" }

func (model *blockingInteractiveModel) DebugLogPath() string { return "" }

func (model *blockingInteractiveModel) ResumeSession(string) error {
	if !model.blockResume {
		return nil
	}

	close(model.resumeStarted)
	<-model.releaseResume

	return nil
}

func (model *blockingInteractiveModel) Close() error {
	model.closeCount.Add(1)
	if model.updateActive.Load() {
		model.closeDuringUpdate.Store(true)
	}
	model.closeStartedOnce.Do(func() { close(model.closeStarted) })

	return nil
}

func (model *blockingInteractiveModel) releaseBlockedUpdate() {
	model.releaseUpdateOnce.Do(func() { close(model.releaseUpdate) })
}

func (model *blockingInteractiveModel) releaseBlockedResume() {
	model.releaseResumeOnce.Do(func() { close(model.releaseResume) })
}

func (model *blockingInteractiveModel) releaseBlockedOperations() {
	model.releaseBlockedUpdate()
	model.releaseBlockedResume()
}
