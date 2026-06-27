package readapprovalcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"idiom/internal/contracts/readapproval"
	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/sessionid"
)

const fileRecordSchemaVersion = "idiom.readapproval.file_cache.v1"

// FileCache stores read-approval decisions in the workspace-local Idiom state
// directory and keeps an in-memory copy per session for repeated lookups.
type FileCache struct {
	root string
	mu   sync.Mutex
	mem  map[string]readapproval.SessionDecisions
}

// NewFileCache returns a filesystem-backed read-approval cache rooted at the
// workspace. Session IDs are treated as single path components.
func NewFileCache(root string) *FileCache {
	return &FileCache{
		root: root,
		mem:  make(map[string]readapproval.SessionDecisions),
	}
}

func (c *FileCache) sessionDir(sessionID string) string {
	return filepath.Join(c.root, ".idiom", "read-approvals", "sessions", sessionID)
}

func (c *FileCache) sessionFile(sessionID string) string {
	return filepath.Join(c.sessionDir(sessionID), "read-approvals.json")
}

type fileRecord struct {
	Schema   string                  `json:"schema_version"`
	Session  string                  `json:"session_id"`
	Updated  time.Time               `json:"updated_at"`
	Approved map[string]fileDecision `json:"approved"`
	Denied   map[string]fileDecision `json:"denied"`
}

type fileDecision struct {
	Outcome string    `json:"outcome"`
	Reason  string    `json:"reason,omitempty"`
	Source  string    `json:"source,omitempty"`
	At      time.Time `json:"at,omitempty"`
	Path    string    `json:"path"`
}

func (c *FileCache) load(sessionID string) (readapproval.SessionDecisions, error) {
	if err := validateSessionID(sessionID); err != nil {
		return readapproval.NewSessionDecisions(), err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.mem[sessionID]; ok {
		return cloneSessionDecisions(cached), nil
	}

	decisions := readapproval.NewSessionDecisions()

	path := c.sessionFile(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.mem[sessionID] = cloneSessionDecisions(decisions)
			return cloneSessionDecisions(decisions), nil
		}
		return decisions, err
	}

	var record fileRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return decisions, fmt.Errorf("readapproval: parse %s: %w", path, err)
	}
	if strings.TrimSpace(record.Schema) != "" && strings.TrimSpace(record.Schema) != fileRecordSchemaVersion {
		return decisions, fmt.Errorf("readapproval: unsupported schema %q in %s", record.Schema, path)
	}
	if strings.TrimSpace(record.Session) != "" && strings.TrimSpace(record.Session) != sessionID {
		return decisions, fmt.Errorf("readapproval: cache session %q does not match requested session %q in %s", record.Session, sessionID, path)
	}

	for path, d := range record.Approved {
		decision, err := readApprovalDecisionFromFile(path, d, readapproval.OutcomeApproved)
		if err != nil {
			return decisions, fmt.Errorf("readapproval: invalid approved decision in %s: %w", path, err)
		}
		decisions.Record(decision)
	}
	for path, d := range record.Denied {
		decision, err := readApprovalDecisionFromFile(path, d, readapproval.OutcomeDenied)
		if err != nil {
			return decisions, fmt.Errorf("readapproval: invalid denied decision in %s: %w", path, err)
		}
		decisions.Record(decision)
	}

	c.mem[sessionID] = cloneSessionDecisions(decisions)
	return cloneSessionDecisions(decisions), nil
}

func (c *FileCache) persist(sessionID string, decisions readapproval.SessionDecisions) error {
	dir := c.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("readapproval: mkdir %s: %w", dir, err)
	}

	record := fileRecord{
		Schema:   fileRecordSchemaVersion,
		Session:  sessionID,
		Updated:  time.Now().UTC(),
		Approved: readApprovalFileDecisions(decisions.Approved),
		Denied:   readApprovalFileDecisions(decisions.Denied),
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("readapproval: marshal: %w", err)
	}

	if err := atomicfile.WriteFile(c.sessionFile(sessionID), data, 0o600); err != nil {
		return fmt.Errorf("readapproval: write cache: %w", err)
	}
	return nil
}

func (c *FileCache) Lookup(sessionID, path string) (readapproval.Decision, bool, error) {
	decisions, err := c.load(sessionID)
	if err != nil {
		return readapproval.Decision{}, false, err
	}
	decision, ok := decisions.Lookup(path)
	return decision, ok, nil
}

func (c *FileCache) Record(sessionID string, decision readapproval.Decision) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
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
		if current, currentOK := c.mem[sessionID]; currentOK {
			decisions = current
		} else {
			decisions = loaded
		}
	}
	updated := cloneSessionDecisions(decisions)
	updated.Record(decision)
	persisted := cloneSessionDecisions(updated)
	if err := c.persist(sessionID, persisted); err != nil {
		c.mu.Unlock()
		return err
	}
	c.mem[sessionID] = cloneSessionDecisions(updated)
	c.mu.Unlock()

	return nil
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

func sortedPathKeys(m map[string]readapproval.Decision) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readApprovalFileDecisions(decisions map[string]readapproval.Decision) map[string]fileDecision {
	out := make(map[string]fileDecision, len(decisions))
	for path, decision := range decisions {
		if decision.Path == "" {
			decision.Path = path
		}
		out[decision.Path] = fileDecision{
			Outcome: decision.Outcome.String(),
			Reason:  decision.Reason,
			Source:  decision.Source,
			At:      decision.At,
			Path:    decision.Path,
		}
	}
	return out
}

func readApprovalDecisionFromFile(mapPath string, stored fileDecision, expected readapproval.Outcome) (readapproval.Decision, error) {
	path := strings.TrimSpace(stored.Path)
	if path == "" {
		return readapproval.Decision{}, errors.New("missing path")
	}
	if path != mapPath {
		return readapproval.Decision{}, fmt.Errorf("path %q does not match map key %q", path, mapPath)
	}

	outcome, ok := readApprovalOutcomeFromString(stored.Outcome)
	if !ok {
		return readapproval.Decision{}, fmt.Errorf("unknown outcome %q", stored.Outcome)
	}
	if outcome != expected {
		return readapproval.Decision{}, fmt.Errorf("outcome %q does not match %s bucket", stored.Outcome, expected.String())
	}

	return readapproval.Decision{
		Outcome: outcome,
		Reason:  strings.TrimSpace(stored.Reason),
		Source:  strings.TrimSpace(stored.Source),
		At:      stored.At,
		Path:    path,
	}, nil
}

func readApprovalOutcomeFromString(value string) (readapproval.Outcome, bool) {
	switch strings.TrimSpace(value) {
	case readapproval.OutcomeApproved.String():
		return readapproval.OutcomeApproved, true
	case readapproval.OutcomeDenied.String():
		return readapproval.OutcomeDenied, true
	case readapproval.OutcomePromptRequired.String():
		return readapproval.OutcomePromptRequired, true
	default:
		return readapproval.OutcomeDenied, false
	}
}

func cloneSessionDecisions(in readapproval.SessionDecisions) readapproval.SessionDecisions {
	out := readapproval.NewSessionDecisions()
	for path, decision := range in.Approved {
		out.Approved[path] = decision
	}
	for path, decision := range in.Denied {
		out.Denied[path] = decision
	}
	return out
}

func validateSessionID(sessionID string) error {
	if err := sessionid.Validate(sessionID); err != nil {
		return fmt.Errorf("readapproval: %w", err)
	}
	return nil
}
