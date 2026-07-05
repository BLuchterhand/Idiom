package intentloop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CleanupReport struct {
	CompactedSessions []string                `json:"compacted_sessions,omitempty"`
	DeletedPaths      []string                `json:"deleted_paths,omitempty"`
	PreservedPaths    []string                `json:"preserved_paths,omitempty"`
	ChildSnapshots    []ChildArtifactSnapshot `json:"child_snapshots,omitempty"`
}

type ChildArtifactSnapshot struct {
	CycleNumber              int    `json:"cycle_number"`
	PlanExecuteSessionID     string `json:"plan_execute_session_id,omitempty"`
	TerminalCompletionPath   string `json:"terminal_completion_path,omitempty"`
	TerminalCompletionSHA256 string `json:"terminal_completion_sha256,omitempty"`
	ResumeStatePath          string `json:"resume_state_path,omitempty"`
	ResumeStateSHA256        string `json:"resume_state_sha256,omitempty"`
}

func CompactStaleSessions(root string, olderThan time.Duration, now time.Time) (CleanupReport, error) {
	if olderThan <= 0 {
		return CleanupReport{}, fmt.Errorf("olderThan must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	sessionsDir := filepath.Join(normalizeCleanupRoot(root), ".idiom", "intent-loop", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return CleanupReport{}, nil
	}
	if err != nil {
		return CleanupReport{}, fmt.Errorf("read intent-loop sessions dir: %w", err)
	}

	var report CleanupReport
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		state, _, err := LoadState(root, sessionID)
		if err != nil {
			return report, err
		}
		if !stateIsOlderThan(state, filepath.Join(sessionsDir, entry.Name()), olderThan, now) {
			continue
		}

		childReport, err := CompactChildArtifacts(root, &state)
		if err != nil {
			return report, err
		}
		report.CompactedSessions = append(report.CompactedSessions, sessionID)
		report.DeletedPaths = append(report.DeletedPaths, childReport.DeletedPaths...)
		report.PreservedPaths = append(report.PreservedPaths, childReport.PreservedPaths...)
		report.ChildSnapshots = append(report.ChildSnapshots, childReport.ChildSnapshots...)
		if _, err := SaveState(root, &state); err != nil {
			return report, err
		}
	}

	sort.Strings(report.CompactedSessions)
	sort.Strings(report.DeletedPaths)
	sort.Strings(report.PreservedPaths)
	return report, nil
}

func CompactChildArtifacts(root string, state *IntentLoopState) (CleanupReport, error) {
	if state == nil {
		return CleanupReport{}, fmt.Errorf("intent loop state is nil")
	}

	var report CleanupReport
	for _, cycle := range state.Cycles {
		snapshot, preserved, err := childArtifactSnapshot(root, cycle)
		if err != nil {
			return report, err
		}
		if snapshot != (ChildArtifactSnapshot{}) {
			report.ChildSnapshots = append(report.ChildSnapshots, snapshot)
		}
		report.PreservedPaths = append(report.PreservedPaths, preserved...)
	}

	deleted, err := removeSessionTransientArtifacts(root, state.SessionID)
	if err != nil {
		return report, err
	}
	report.DeletedPaths = append(report.DeletedPaths, deleted...)

	sort.Strings(report.DeletedPaths)
	sort.Strings(report.PreservedPaths)
	return report, nil
}

func childArtifactSnapshot(root string, cycle CycleRecord) (ChildArtifactSnapshot, []string, error) {
	terminalPath := strings.TrimSpace(cycle.TerminalCompletionPath)
	snapshot := ChildArtifactSnapshot{
		CycleNumber:            cycle.CycleNumber,
		PlanExecuteSessionID:   strings.TrimSpace(cycle.PlanExecuteSessionID),
		TerminalCompletionPath: terminalPath,
		ResumeStatePath:        strings.TrimSpace(cycle.ResumeStatePath),
	}

	var preserved []string
	if snapshot.TerminalCompletionPath != "" {
		hash, found, err := hashRelativeFile(root, snapshot.TerminalCompletionPath)
		if err != nil {
			return ChildArtifactSnapshot{}, nil, err
		}
		if found {
			snapshot.TerminalCompletionSHA256 = hash
			preserved = append(preserved, snapshot.TerminalCompletionPath)
		}
	}
	if snapshot.ResumeStatePath != "" {
		hash, found, err := hashRelativeFile(root, snapshot.ResumeStatePath)
		if err != nil {
			return ChildArtifactSnapshot{}, nil, err
		}
		if found {
			snapshot.ResumeStateSHA256 = hash
			preserved = append(preserved, snapshot.ResumeStatePath)
		}
	}

	if snapshot.PlanExecuteSessionID == "" && snapshot.TerminalCompletionSHA256 == "" && snapshot.ResumeStateSHA256 == "" {
		return ChildArtifactSnapshot{}, preserved, nil
	}
	return snapshot, preserved, nil
}

func removeSessionTransientArtifacts(root string, sessionID string) ([]string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !validIntentLoopSessionID.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid intent loop session id %q", sessionID)
	}

	sessionDir := filepath.Join(normalizeCleanupRoot(root), ".idiom", "intent-loop", "sessions", sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat intent-loop session dir: %w", err)
	}

	var deleted []string
	err := filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == sessionDir {
			return nil
		}

		rel, err := filepath.Rel(normalizeCleanupRoot(root), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if entry.IsDir() {
			if isTransientArtifactName(entry.Name()) {
				if err := os.RemoveAll(path); err != nil {
					return err
				}
				deleted = append(deleted, rel)
				return filepath.SkipDir
			}
			return nil
		}

		if !isTransientArtifactName(entry.Name()) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		deleted = append(deleted, rel)
		return nil
	})
	if err != nil {
		return deleted, fmt.Errorf("remove transient intent-loop artifacts: %w", err)
	}
	sort.Strings(deleted)
	return deleted, nil
}

func stateIsOlderThan(state IntentLoopState, statePath string, olderThan time.Duration, now time.Time) bool {
	for _, raw := range []string{state.UpdatedAt, state.CreatedAt} {
		if stamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); err == nil {
			return now.Sub(stamp) > olderThan
		}
	}
	info, err := os.Stat(statePath)
	if err != nil {
		return false
	}
	return now.Sub(info.ModTime()) > olderThan
}

func hashRelativeFile(root string, relPath string) (string, bool, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || filepath.IsAbs(relPath) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(relPath)), "../") {
		return "", false, fmt.Errorf("invalid relative artifact path %q", relPath)
	}

	body, err := os.ReadFile(filepath.Join(normalizeCleanupRoot(root), filepath.FromSlash(relPath)))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read artifact %s: %w", relPath, err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), true, nil
}

func isTransientArtifactName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "spill", "spills", "tmp", "temp", "transient", "cache":
		return true
	}
	for _, suffix := range []string{".tmp", ".spill", ".cache", ".stdout", ".stderr"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func normalizeCleanupRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return "."
	}
	return root
}
