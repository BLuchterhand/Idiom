package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// File-of-interest reference pinning. The idea: every file the session has
// read or edited lives in a small in-memory pin set. When
// requestMessages() builds the chat payload, it can inject a compact
// snapshot of the pinned files' current on-disk content as a single system
// message, so the model always has the relevant source in context even
// after the raw `read_file` tool result has been compacted out of history.
//
// This is a direct response to the failure mode where a model reads a
// file early, the history gets compacted, and the next turn the model
// re-requests the same file (burning a step) or hallucinates from memory.

type pinnedFile struct {
	path     string
	lastSeen time.Time
}

type pinnedFileRegistry struct {
	mu    sync.Mutex
	pins  map[string]*pinnedFile
	order []string
}

func newPinnedFileRegistry() *pinnedFileRegistry {
	return &pinnedFileRegistry{pins: map[string]*pinnedFile{}}
}

// Pin records that a path is in active use. Repeated pins refresh the
// last-seen timestamp so LRU eviction can drop stale entries.
func (r *pinnedFileRegistry) Pin(path string) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return
	}
	normalized = filepath.ToSlash(filepath.Clean(normalized))
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.pins[normalized]; ok {
		existing.lastSeen = time.Now()
		return
	}
	r.pins[normalized] = &pinnedFile{path: normalized, lastSeen: time.Now()}
	r.order = append(r.order, normalized)
	r.evictLRU(pinnedFileMaxEntries)
}

// Pins returns the currently pinned paths in most-recent-first order.
func (r *pinnedFileRegistry) Pins() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.pins))
	for _, path := range r.order {
		if _, ok := r.pins[path]; ok {
			out = append(out, path)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return r.pins[out[i]].lastSeen.After(r.pins[out[j]].lastSeen)
	})
	return out
}

// Forget drops a path from the pin set. Used when the model asks the
// workspace to delete or move a file so we don't keep staging its
// contents.
func (r *pinnedFileRegistry) Forget(path string) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		return
	}
	normalized = filepath.ToSlash(filepath.Clean(normalized))
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pins, normalized)
}

func (r *pinnedFileRegistry) evictLRU(cap int) {
	if cap <= 0 || len(r.pins) <= cap {
		return
	}
	// Find LRU by scanning. Small N, no need for a heap.
	type entry struct {
		path string
		t    time.Time
	}
	entries := make([]entry, 0, len(r.pins))
	for path, pin := range r.pins {
		entries = append(entries, entry{path: path, t: pin.lastSeen})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].t.Before(entries[j].t)
	})
	toDrop := len(entries) - cap
	for i := 0; i < toDrop; i++ {
		delete(r.pins, entries[i].path)
	}
	kept := make([]string, 0, len(r.pins))
	for _, path := range r.order {
		if _, ok := r.pins[path]; ok {
			kept = append(kept, path)
		}
	}
	r.order = kept
}

const (
	pinnedFileMaxEntries        = 6
	pinnedFileSnapshotMaxBytes  = 6000
	pinnedFileMaxBytesPerFile   = 2400
	pinnedFileSnapshotHeaderTag = "Pinned files snapshot (recent reads / edits; contents may have changed since the last tool result):"
)

// BuildPinnedFilesSnapshot returns a system-message-friendly string listing
// each pinned file with its current on-disk contents. Truncated to a hard
// byte cap so repeated injection stays bounded. Returns the empty string
// when no pins exist or all reads fail.
func (r *pinnedFileRegistry) BuildSnapshot(root string) string {
	if r == nil {
		return ""
	}
	pins := r.Pins()
	if len(pins) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(pinnedFileSnapshotHeaderTag)
	b.WriteByte('\n')
	budget := pinnedFileSnapshotMaxBytes - b.Len()
	for _, rel := range pins {
		if budget <= 0 {
			break
		}
		abs := rel
		if !filepath.IsAbs(abs) && strings.TrimSpace(root) != "" {
			abs = filepath.Join(root, rel)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		snippet := string(data)
		if len(snippet) > pinnedFileMaxBytesPerFile {
			snippet = snippet[:pinnedFileMaxBytesPerFile] + "\n… (truncated)"
		}
		entry := fmt.Sprintf("--- %s ---\n%s\n", rel, snippet)
		if len(entry) > budget {
			if budget < 128 {
				break
			}
			entry = entry[:budget] + "\n… (truncated for pin-snapshot budget)"
		}
		b.WriteString(entry)
		budget -= len(entry)
	}
	out := strings.TrimRight(b.String(), "\n")
	// If the header is all we wrote, emit nothing so the caller doesn't
	// inject a useless empty block.
	if out == pinnedFileSnapshotHeaderTag {
		return ""
	}
	return out
}
