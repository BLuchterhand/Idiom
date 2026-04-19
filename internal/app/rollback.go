package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"qwen-bubbles/internal/workspace"
)

type rollbackSnapshot struct {
	Path    string
	Exists  bool
	IsDir   bool
	Mode    os.FileMode
	Content []byte
}

type executionJournal struct {
	Prompt      string
	Paths       []string
	Snapshots   map[string]rollbackSnapshot
	Summary     string
	Refactor    *structuralRefactorStage
	CreatedAt   time.Time
	CommittedAt time.Time
}

type executionJournalRecorder struct {
	mgr       *workspace.Manager
	prompt    string
	snapshots map[string]rollbackSnapshot
	order     []string
}

func newExecutionJournalRecorder(mgr *workspace.Manager, prompt string) *executionJournalRecorder {
	return &executionJournalRecorder{
		mgr:       mgr,
		prompt:    prompt,
		snapshots: make(map[string]rollbackSnapshot),
		order:     make([]string, 0, 4),
	}
}

func (r *executionJournalRecorder) CaptureRequest(req workspace.ToolRequest) error {
	if r == nil {
		return nil
	}
	switch req.Tool {
	case "edit_file", "write_file", "append_file", "mkdir":
	default:
		return nil
	}
	return r.capturePath(req.Path, req.Tool == "mkdir")
}

func (r *executionJournalRecorder) capturePath(path string, treatMissingAsDir bool) error {
	if r == nil || r.mgr == nil {
		return nil
	}
	path = normalizeGuardedPath(path)
	if path == "" {
		return nil
	}
	if _, ok := r.snapshots[path]; ok {
		return nil
	}
	fullPath, err := preflightResolvePath(r.mgr, path, ".")
	if err != nil {
		return fmt.Errorf("resolve rollback snapshot path %s: %w", path, err)
	}
	info, statErr := os.Stat(fullPath)
	switch {
	case statErr == nil:
		snapshot := rollbackSnapshot{
			Path:   path,
			Exists: true,
			IsDir:  info.IsDir(),
			Mode:   info.Mode(),
		}
		if !info.IsDir() {
			content, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				return fmt.Errorf("read rollback snapshot %s: %w", path, readErr)
			}
			snapshot.Content = content
		}
		r.snapshots[path] = snapshot
	case os.IsNotExist(statErr):
		r.snapshots[path] = rollbackSnapshot{
			Path:   path,
			Exists: false,
			IsDir:  treatMissingAsDir,
		}
	default:
		return fmt.Errorf("stat rollback snapshot %s: %w", path, statErr)
	}
	r.order = append(r.order, path)
	return nil
}

func (r *executionJournalRecorder) Finalize(changedPaths []string, summary string) *executionJournal {
	if r == nil {
		return nil
	}
	changedPaths = uniqueNormalizedPaths(changedPaths)
	if len(changedPaths) == 0 {
		return nil
	}
	for _, path := range changedPaths {
		if _, ok := r.snapshots[path]; ok {
			continue
		}
		_ = r.capturePath(path, false)
	}
	snapshots := make(map[string]rollbackSnapshot, len(changedPaths))
	for _, path := range changedPaths {
		if snapshot, ok := r.snapshots[path]; ok {
			snapshots[path] = snapshot
		}
	}
	if len(snapshots) == 0 {
		return nil
	}
	return &executionJournal{
		Prompt:      strings.TrimSpace(r.prompt),
		Paths:       changedPaths,
		Snapshots:   snapshots,
		Summary:     strings.TrimSpace(summary),
		CreatedAt:   time.Now(),
		CommittedAt: time.Now(),
	}
}

func applyExecutionRollback(mgr *workspace.Manager, journal *executionJournal) error {
	if mgr == nil || journal == nil {
		return nil
	}
	paths := append([]string(nil), journal.Paths...)
	slices.SortFunc(paths, func(a, b string) int {
		if len(a) == len(b) {
			return strings.Compare(a, b)
		}
		return len(b) - len(a)
	})
	for _, path := range paths {
		snapshot, ok := journal.Snapshots[path]
		if !ok {
			continue
		}
		fullPath, err := preflightResolvePath(mgr, path, ".")
		if err != nil {
			return fmt.Errorf("resolve rollback path %s: %w", path, err)
		}
		if !snapshot.Exists {
			if removeErr := os.RemoveAll(fullPath); removeErr != nil && !os.IsNotExist(removeErr) {
				return fmt.Errorf("remove rollback target %s: %w", path, removeErr)
			}
			continue
		}
		if snapshot.IsDir {
			if err := os.MkdirAll(fullPath, snapshot.Mode.Perm()); err != nil {
				return fmt.Errorf("restore directory %s: %w", path, err)
			}
			if err := os.Chmod(fullPath, snapshot.Mode); err != nil {
				return fmt.Errorf("restore directory mode %s: %w", path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("restore parent dir for %s: %w", path, err)
		}
		if err := os.WriteFile(fullPath, snapshot.Content, snapshot.Mode.Perm()); err != nil {
			return fmt.Errorf("restore file %s: %w", path, err)
		}
		if err := os.Chmod(fullPath, snapshot.Mode); err != nil {
			return fmt.Errorf("restore file mode %s: %w", path, err)
		}
	}
	return nil
}

func composeRollbackSummary(journal *executionJournal, verify implementationVerification) string {
	if journal == nil {
		return "No rollback journal was available."
	}
	sections := make([]string, 0, 3)
	sections = append(sections, "Rolled back the last bounded change.")
	if len(journal.Paths) > 0 {
		var b strings.Builder
		b.WriteString("Restored paths:\n")
		for _, path := range journal.Paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	if len(verify.results) > 0 {
		var b strings.Builder
		b.WriteString("Verification:\n")
		for i, result := range verify.results {
			command := fmt.Sprintf("check %d", i+1)
			if i < len(verify.commands) && strings.TrimSpace(verify.commands[i]) != "" {
				command = "`" + verify.commands[i] + "`"
			}
			status := "passed"
			if result.Error != "" {
				status = "failed"
			}
			b.WriteString("- ")
			b.WriteString(command)
			b.WriteString(": ")
			b.WriteString(status)
			b.WriteString("\n")
		}
		sections = append(sections, strings.TrimSpace(b.String()))
	}
	return strings.Join(sections, "\n\n")
}
