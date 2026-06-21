package readapproval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type FileCache struct {
	root string
	mu   sync.Mutex
	mem  map[string]SessionDecisions
}

func NewFileCache(root string) *FileCache {
	return &FileCache{
		root: root,
		mem:  make(map[string]SessionDecisions),
	}
}

func (c *FileCache) sessionDir(sessionID string) string {
	return filepath.Join(c.root, "sessions", sessionID)
}

func (c *FileCache) sessionFile(sessionID string) string {
	return filepath.Join(c.sessionDir(sessionID), "read-approvals.json")
}

type fileRecord struct {
	Schema   string               `json:"schema_version"`
	Session  string               `json:"session_id"`
	Updated  time.Time            `json:"updated_at"`
	Approved map[string]Decision  `json:"approved"`
	Denied   map[string]Decision  `json:"denied"`
}

func (c *FileCache) load(sessionID string) (SessionDecisions, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.mem[sessionID]; ok {
		return cached, nil
	}

	decisions := NewSessionDecisions()

	path := c.sessionFile(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.mem[sessionID] = decisions
			return decisions, nil
		}
		return decisions, err
	}

	var record fileRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return decisions, fmt.Errorf("readapproval: parse %s: %w", path, err)
	}

	for path, d := range record.Approved {
		if d.Path == "" {
			d.Path = path
		}
		decisions.Approved[d.Path] = d
	}
	for path, d := range record.Denied {
		if d.Path == "" {
			d.Path = path
		}
		decisions.Denied[d.Path] = d
	}

	c.mem[sessionID] = decisions
	return decisions, nil
}

func (c *FileCache) persist(sessionID string, decisions SessionDecisions) error {
	dir := c.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("readapproval: mkdir %s: %w", dir, err)
	}

	record := fileRecord{
		Schema:   "idiom.readapproval.file_cache.v1",
		Session:  sessionID,
		Updated:  time.Now().UTC(),
		Approved: decisions.Approved,
		Denied:   decisions.Denied,
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("readapproval: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "read-approvals.*.tmp")
	if err != nil {
		return fmt.Errorf("readapproval: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("readapproval: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("readapproval: close temp: %w", err)
	}
	if err := os.Rename(tmpName, c.sessionFile(sessionID)); err != nil {
		return fmt.Errorf("readapproval: rename: %w", err)
	}
	return nil
}

func (c *FileCache) Lookup(sessionID, path string) (Decision, bool) {
	decisions, err := c.load(sessionID)
	if err != nil {
		return Decision{}, false
	}
	return decisions.Lookup(path)
}

func (c *FileCache) Record(sessionID string, decision Decision) error {
	if sessionID == "" {
		return errors.New("readapproval: empty session id")
	}
	if decision.Path == "" {
		return errors.New("readapproval: empty decision path")
	}

	c.mu.Lock()
	decisions, ok := c.mem[sessionID]
	if !ok {
		c.mu.Unlock()
		loaded, err := c.load(sessionID)
		if err != nil {
			return err
		}
		c.mu.Lock()
		decisions = loaded
	}
	decisions.Record(decision)
	c.mem[sessionID] = decisions
	c.mu.Unlock()

	return c.persist(sessionID, decisions)
}

func (c *FileCache) ApprovedPaths(sessionID string) []string {
	decisions, err := c.load(sessionID)
	if err != nil {
		return nil
	}
	return sortedPathKeys(decisions.Approved)
}

func (c *FileCache) DeniedPaths(sessionID string) []string {
	decisions, err := c.load(sessionID)
	if err != nil {
		return nil
	}
	return sortedPathKeys(decisions.Denied)
}

func sortedPathKeys(m map[string]Decision) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
