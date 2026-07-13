package app

import (
	"context"
	"sync"
)

// backgroundOperationGroup owns asynchronous Model operations that use shared
// loggers, event channels, clients, or controllers. Admission is synchronous:
// once shutdown closes the group, no goroutine can be added after Wait begins.
type backgroundOperationGroup struct {
	mu     sync.Mutex
	closed bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newBackgroundOperationGroup() *backgroundOperationGroup {
	ctx, cancel := context.WithCancel(context.Background())

	return &backgroundOperationGroup{ctx: ctx, cancel: cancel}
}

func (group *backgroundOperationGroup) start(run func(context.Context)) bool {
	if group == nil || run == nil {
		return false
	}

	group.mu.Lock()
	if group.closed {
		group.mu.Unlock()

		return false
	}
	group.wg.Add(1)
	ctx := group.ctx
	group.mu.Unlock()

	go func() {
		defer group.wg.Done()
		run(ctx)
	}()

	return true
}

func (group *backgroundOperationGroup) shutdown() {
	if group == nil {
		return
	}

	group.mu.Lock()
	if !group.closed {
		group.closed = true
		group.cancel()
	}
	group.mu.Unlock()

	group.wg.Wait()
}
