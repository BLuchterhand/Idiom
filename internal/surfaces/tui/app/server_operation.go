package app

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

// plannerServerOperation is an immutable snapshot for one asynchronous server
// operation. Goroutines use only this value and never read the live Model,
// whose backend selection may change through /model while they are running.
type plannerServerOperation struct {
	source         serverStatusSource
	spec           config.LlamaServer
	client         llama.LLMClient
	controller     *llama.ServerController
	events         chan<- tea.Msg
	managedServers *managedServerRegistry
	autoStart      bool
	startTimeout   time.Duration
}

type implementerServerOperation struct {
	spec         config.LlamaServer
	client       llama.LLMClient
	controller   *llama.ServerController
	events       chan<- tea.Msg
	debugLogger  *transcript.DebugLogger
	autoStart    bool
	startTimeout time.Duration
}

func snapshotPlannerServerOperation(model *Model) plannerServerOperation {
	if model == nil {
		return plannerServerOperation{}
	}

	managedServers := model.managedServers
	if managedServers == nil {
		managedServers = newManagedServerRegistry()
		model.managedServers = managedServers
	}

	return plannerServerOperation{
		source:         currentServerStatusSource(model.backendRevision, model.controller, model.plannerSpec),
		spec:           cloneLlamaServerSpec(model.plannerSpec),
		client:         model.client,
		controller:     model.controller,
		events:         model.events,
		managedServers: managedServers,
		autoStart:      model.cfg.Chat.AutoStartServer,
		startTimeout:   time.Duration(model.cfg.Chat.ServerStartTimeoutSecs) * time.Second,
	}
}

func snapshotImplementerServerOperation(model *Model) (implementerServerOperation, bool) {
	if model == nil || model.implementerController == nil {
		return implementerServerOperation{}, false
	}

	return implementerServerOperation{
		spec:         cloneLlamaServerSpec(model.executorSpec),
		client:       model.implementerClient,
		controller:   model.implementerController,
		events:       model.events,
		debugLogger:  model.debugLogger,
		autoStart:    model.cfg.Chat.AutoStartServer,
		startTimeout: time.Duration(model.cfg.Chat.ServerStartTimeoutSecs) * time.Second,
	}, true
}

func (operation plannerServerOperation) status(msg serverStatusMsg) serverStatusMsg {
	source := operation.source
	msg.source = &source

	return msg
}

func (operation plannerServerOperation) sendStatus(ctx context.Context, msg serverStatusMsg) {
	select {
	case operation.events <- operation.status(msg):
	case <-ctx.Done():
	}
}

func (operation plannerServerOperation) start(ownerCtx context.Context) {
	ctx, cancel := context.WithTimeout(ownerCtx, operation.startTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return
	}

	if err := operation.controller.Start(ctx); err != nil {
		operation.sendStatus(ctx, serverStatusMsg{err: err})

		return
	}
	operation.managedServers.add(operation.controller)

	if err := operation.controller.WaitUntilReady(ctx); err != nil {
		operation.sendStatus(ctx, serverStatusMsg{err: err, owned: true})

		return
	}

	operation.sendReadyStatus(ctx)
}

func (operation plannerServerOperation) stop(ownerCtx context.Context) {
	ctx, cancel := context.WithTimeout(ownerCtx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return
	}

	err := operation.controller.Stop(ctx)
	operation.sendStatus(ctx, serverStatusMsg{running: false, err: err})
}

func (operation plannerServerOperation) restart(ownerCtx context.Context) {
	ctx, cancel := context.WithTimeout(ownerCtx, operation.startTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return
	}

	if err := operation.controller.Restart(ctx); err != nil {
		operation.sendStatus(ctx, serverStatusMsg{err: err})

		return
	}
	operation.managedServers.add(operation.controller)

	if err := operation.controller.WaitUntilReady(ctx); err != nil {
		operation.sendStatus(ctx, serverStatusMsg{err: err, owned: true})

		return
	}

	operation.sendReadyStatus(ctx)
}

func (operation plannerServerOperation) sendReadyStatus(ctx context.Context) {
	models, err := operation.client.ListModels(ctx)
	if err != nil || len(models) == 0 {
		modelID := resolveModelID(operation.spec, "local-model")
		operation.sendStatus(ctx, serverStatusMsg{
			running:   true,
			modelID:   modelID,
			displayID: resolveModelDisplayID(operation.spec, "local-model"),
			owned:     true,
		})

		return
	}

	modelID := resolveModelID(operation.spec, models[0].ID)
	operation.sendStatus(ctx, serverStatusMsg{
		running:   true,
		modelID:   modelID,
		displayID: resolveModelDisplayID(operation.spec, models[0].ID),
		owned:     true,
	})
}

func (operation implementerServerOperation) start(ownerCtx context.Context) {
	ctx, cancel := context.WithTimeout(ownerCtx, operation.startTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return
	}

	if models, err := operation.client.ListModels(ctx); err == nil && len(models) > 0 {
		operation.sendInfo(ctx, "Implementer ready: "+resolveModelDisplayID(operation.spec, models[0].ID))

		return
	}

	if !operation.autoStart {
		return
	}

	if err := operation.controller.Start(ctx); err != nil {
		operation.logError("start failed: " + err.Error())

		return
	}

	if err := operation.controller.WaitUntilReady(ctx); err != nil {
		operation.logError("ready wait failed: " + err.Error())

		return
	}

	models, err := operation.client.ListModels(ctx)
	id := "local-model"
	if err == nil && len(models) > 0 {
		id = models[0].ID
	}
	operation.sendInfo(ctx, "Implementer ready: "+resolveModelDisplayID(operation.spec, id))
}

func (operation implementerServerOperation) sendInfo(ctx context.Context, text string) {
	select {
	case operation.events <- infoMsg{text: text}:
	case <-ctx.Done():
	}
}

func (operation implementerServerOperation) logError(message string) {
	if operation.debugLogger != nil {
		_ = operation.debugLogger.LogError("implementer", message)
	}
}
