package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/workspace"
)

// rollbackEntry captures file content before an edit for the /undo command.
// Unlike planWriteSnapshot (which powers the plan diff overlay), this type
// feeds undoLast so the user can step backwards through write-tool mutations.
//
// Each write-class tool produces one entry; apply_patch produces one per
// affected path. Entries are ordered oldest-first; /undo pops from the tail.
type rollbackEntry struct {
	path     string
	contents []byte
	existed  bool
}

// recordRollbackSnapshot captures the pre-edit state of files a
// write-class tool is about to modify. Called from PreExecute after the
// tool-rule check returns Allow (or user-approved Ask). Read-only tools
// produce no snapshots. Errors reading the file are swallowed — /undo
// will just be missing that entry rather than blocking the tool call.
func (m *Model) recordRollbackSnapshot(req workspace.ToolRequest) {
	if m == nil || m.workspace == nil {
		return
	}
	paths := rollbackPathsForRequest(req)
	if len(paths) == 0 {
		return
	}
	root := m.workspace.CWD()
	for _, path := range paths {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				m.rollbackHistory = append(m.rollbackHistory, rollbackEntry{path: abs, existed: false})
			}
			continue
		}
		m.rollbackHistory = append(m.rollbackHistory, rollbackEntry{
			path:     abs,
			contents: append([]byte(nil), data...),
			existed:  true,
		})
	}
}

// rollbackPathsForRequest returns the file paths a write-class tool
// will touch. Non-write tools (read_file, search_*, shell) return nil.
func rollbackPathsForRequest(req workspace.ToolRequest) []string {
	switch strings.TrimSpace(req.Tool) {
	case "edit_file", "write_file", "append_file":
		if p := strings.TrimSpace(req.Path); p != "" {
			return []string{p}
		}
	case "apply_patch":
		ops, err := workspace.ParsePatch(req.Content)
		if err != nil {
			return nil
		}
		return workspace.PatchOpPaths(ops)
	}
	return nil
}

// undoLast restores the most recent rollback entry. Returns a summary
// suitable for the status line, or an error if there's nothing to undo
// or the file can't be restored.
func (m *Model) undoLast() (string, error) {
	if m == nil {
		return "", fmt.Errorf("no active session")
	}
	if len(m.rollbackHistory) == 0 {
		return "", fmt.Errorf("no rollback history — nothing to undo")
	}
	entry := m.rollbackHistory[len(m.rollbackHistory)-1]
	m.rollbackHistory = m.rollbackHistory[:len(m.rollbackHistory)-1]
	if !entry.existed {
		if err := os.Remove(entry.path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove %s: %w", entry.path, err)
		}
		return "Undone: removed " + m.displayPath(entry.path), nil
	}
	if err := os.WriteFile(entry.path, entry.contents, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", entry.path, err)
	}
	return "Undone: restored " + m.displayPath(entry.path), nil
}
