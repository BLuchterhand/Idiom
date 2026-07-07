package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// PathTracker owns the session-local relationship between paths, observed
// read/search evidence, and compact file handles returned by workspace tools.
type PathTracker struct {
	mu            sync.Mutex
	nextHandle    int
	pathToHandle  map[string]string
	handleToPath  map[string]string
	observedPaths map[string]struct{}
}

func NewPathTracker() PathTracker {
	return PathTracker{
		nextHandle:    1,
		pathToHandle:  make(map[string]string),
		handleToPath:  make(map[string]string),
		observedPaths: make(map[string]struct{}),
	}
}

func (t *PathTracker) MarkObserved(path string) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureMapsLocked()
	t.observedPaths[normalizePathKey(normalized)] = struct{}{}
}

func (t *PathTracker) HasObserved(path string) bool {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.observedPaths == nil {
		return false
	}

	_, ok := t.observedPaths[normalizePathKey(normalized)]
	return ok
}

func (t *PathTracker) Remember(path string) (string, bool) {
	cleaned := filepath.Clean(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureMapsLocked()
	if handle, ok := t.pathToHandle[cleaned]; ok {
		return handle, false
	}

	handle := fmt.Sprintf("f%d", t.nextHandle)
	t.nextHandle++
	t.pathToHandle[cleaned] = handle
	t.handleToPath[handle] = cleaned

	return handle, true
}

func (t *PathTracker) HasDiscovered(path string) bool {
	cleaned := filepath.Clean(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	_, ok := t.pathToHandle[cleaned]
	return ok
}

func (t *PathTracker) PathForHandle(handle string) (string, error) {
	trimmed := strings.TrimSpace(handle)

	t.mu.Lock()
	defer t.mu.Unlock()

	path, ok := t.handleToPath[trimmed]
	if !ok {
		return "", fmt.Errorf("unknown file handle %q", handle)
	}

	return path, nil
}

func (t *PathTracker) ensureMapsLocked() {
	if t.nextHandle <= 0 {
		t.nextHandle = 1
	}
	if t.pathToHandle == nil {
		t.pathToHandle = make(map[string]string)
	}
	if t.handleToPath == nil {
		t.handleToPath = make(map[string]string)
	}
	if t.observedPaths == nil {
		t.observedPaths = make(map[string]struct{})
	}
}

func normalizePathKey(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.ToLower(cleaned)
}
