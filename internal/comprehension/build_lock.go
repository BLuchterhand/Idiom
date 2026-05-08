package comprehension

import (
	"context"
	"sync"
)

var graphBuildLocks sync.Map

func lockGraphBuild(ctx context.Context, root string) (func(), error) {
	if err := checkBuildContext(ctx); err != nil {
		return nil, err
	}
	semaphore := graphBuildSemaphore(root)
	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	unlockFile, err := lockGraphBuildFile(ctx, root)
	if err != nil {
		<-semaphore
		return nil, err
	}
	return func() {
		unlockFile()
		<-semaphore
	}, nil
}

func graphBuildSemaphore(root string) chan struct{} {
	value, _ := graphBuildLocks.LoadOrStore(root, make(chan struct{}, 1))
	return value.(chan struct{})
}
