package app

import (
	"sync"

	"idiom/internal/runtime/llama"
)

// managedServerRegistry is shared infrastructure between the live model and
// detached prompt workers. A worker registers a controller immediately after
// it starts the process, so Close can stop that process even when the event
// loop quits before consuming the corresponding status projection.
type managedServerRegistry struct {
	mu          sync.Mutex
	controllers map[*llama.ServerController]struct{}
}

func newManagedServerRegistry() *managedServerRegistry {
	return &managedServerRegistry{controllers: make(map[*llama.ServerController]struct{})}
}

func (r *managedServerRegistry) add(controller *llama.ServerController) {
	if r == nil || controller == nil {
		return
	}

	r.mu.Lock()
	if r.controllers == nil {
		r.controllers = make(map[*llama.ServerController]struct{})
	}
	r.controllers[controller] = struct{}{}
	r.mu.Unlock()
}

func (r *managedServerRegistry) snapshot() []*llama.ServerController {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	controllers := make([]*llama.ServerController, 0, len(r.controllers))
	for controller := range r.controllers {
		controllers = append(controllers, controller)
	}

	return controllers
}
