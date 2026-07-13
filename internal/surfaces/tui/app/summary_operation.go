package app

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/evidence/transcript"
	"idiom/internal/runtime/llama"
)

// summaryServerLaunchState is session-local and shared only with detached
// snapshots of that Model. It replaces the former package-global once state,
// which incorrectly coupled independent TUI sessions.
type summaryServerLaunchState struct {
	mu      sync.Mutex
	started bool
	ready   chan struct{}
}

func newSummaryServerLaunchState() *summaryServerLaunchState {
	return &summaryServerLaunchState{}
}

func (state *summaryServerLaunchState) begin() (<-chan struct{}, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()

	if !state.started {
		state.started = true
		state.ready = make(chan struct{})

		return state.ready, true
	}

	return state.ready, false
}

func (state *summaryServerLaunchState) complete() {
	if state == nil {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.ready == nil {
		return
	}

	select {
	case <-state.ready:
	default:
		close(state.ready)
	}
}

type summaryServerOperation struct {
	client       llama.LLMClient
	controller   *llama.ServerController
	autoStart    bool
	startTimeout time.Duration
	state        *summaryServerLaunchState
	debugLogger  *transcript.DebugLogger
}

type historySummaryExecution struct {
	server         summaryServerOperation
	fallbackClient llama.LLMClient
	timeout        time.Duration
	maxTokens      int
	events         chan<- tea.Msg
}

func snapshotHistorySummaryExecution(model *Model) historySummaryExecution {
	if model.summaryServerState == nil {
		model.summaryServerState = newSummaryServerLaunchState()
	}
	startTimeout := time.Duration(model.cfg.RouteClassifier.StartTimeoutSeconds) * time.Second
	if startTimeout <= 0 {
		startTimeout = 120 * time.Second
	}
	timeout := time.Duration(model.cfg.RouteClassifier.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxTokens := model.cfg.RouteClassifier.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64
	}

	return historySummaryExecution{
		server: summaryServerOperation{
			client:       model.summaryClient,
			controller:   model.summaryController,
			autoStart:    model.cfg.RouteClassifier.AutoStartServer,
			startTimeout: startTimeout,
			state:        model.summaryServerState,
			debugLogger:  model.debugLogger,
		},
		fallbackClient: model.client,
		timeout:        timeout,
		maxTokens:      maxTokens,
		events:         model.events,
	}
}

func (operation summaryServerOperation) ensure(ctx context.Context) error {
	if operation.client == nil {
		return errSummaryDisabled
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	_, err := operation.client.ListModels(pingCtx)
	pingCancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !operation.autoStart {
		return err
	}

	ready, shouldLaunch := operation.state.begin()
	if shouldLaunch {
		operation.launch(ctx)
	}

	timer := time.NewTimer(operation.startTimeout)
	defer timer.Stop()
	select {
	case <-ready:
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errSummaryStartTimeout
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer verifyCancel()
	_, err = operation.client.ListModels(verifyCtx)

	return err
}

func (operation summaryServerOperation) launch(ownerCtx context.Context) {
	defer operation.state.complete()
	if operation.controller == nil || ownerCtx.Err() != nil {
		return
	}

	ctx, cancel := context.WithTimeout(ownerCtx, operation.startTimeout)
	defer cancel()
	if err := operation.controller.Start(ctx); err != nil {
		operation.logError("start failed: " + err.Error())

		return
	}
	if err := operation.controller.WaitUntilReady(ctx); err != nil {
		operation.logError("ready wait failed: " + err.Error())
	}
}

func (operation summaryServerOperation) logError(message string) {
	if operation.debugLogger != nil {
		_ = operation.debugLogger.LogError("route_classifier", message)
	}
}
