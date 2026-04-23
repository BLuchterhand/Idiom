package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// InstructionAttachment models a "nearest AGENTS.md / CLAUDE.md"
// lookup. OpenCode's `session/instruction.ts` does the same: when the
// model reads a file, walk up from that file's directory to the repo
// root and, on the way, find the nearest `AGENTS.md` (or `CLAUDE.md`)
// to attach as a one-shot system message.
//
// Rationale: project-specific conventions belong with the code, not in
// a global system prompt. Attaching them only when the model actually
// touches the relevant directory keeps the prompt small and the
// guidance contextual.
type InstructionAttachment struct {
	Path    string
	Content string
}

// InstructionAttacher walks the workspace and emits at-most-once
// attachments per file path per session. Safe for concurrent use.
type InstructionAttacher struct {
	root      string
	candidate []string

	mu        sync.Mutex
	attached  map[string]struct{}
}

// NewInstructionAttacher constructs an attacher rooted at the given
// workspace root. Candidates default to AGENTS.md and CLAUDE.md (in
// that order). Pass custom candidates if a project uses a different
// filename.
func NewInstructionAttacher(root string, candidates ...string) *InstructionAttacher {
	if len(candidates) == 0 {
		candidates = []string{"AGENTS.md", "CLAUDE.md"}
	}
	return &InstructionAttacher{
		root:      strings.TrimSpace(root),
		candidate: candidates,
		attached:  make(map[string]struct{}),
	}
}

// OnFileTouch is called whenever a tool result references a file path
// (e.g. read_file, edit_file). It walks upward from the file's
// directory to the workspace root, looking for the nearest candidate.
// Returns the attachment to surface once — or nil if there's nothing
// new to attach (either no candidate found, or already attached for
// this path this session).
func (a *InstructionAttacher) OnFileTouch(touchedPath string) *InstructionAttachment {
	if a == nil {
		return nil
	}
	touchedPath = strings.TrimSpace(touchedPath)
	if touchedPath == "" {
		return nil
	}
	root := a.root
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	absTouched := touchedPath
	if !filepath.IsAbs(absTouched) {
		absTouched = filepath.Join(root, absTouched)
	}
	absTouched = filepath.Clean(absTouched)
	absRoot := filepath.Clean(root)

	dir := filepath.Dir(absTouched)
	for {
		for _, name := range a.candidate {
			candidatePath := filepath.Join(dir, name)
			if _, err := os.Stat(candidatePath); err != nil {
				continue
			}
			a.mu.Lock()
			if _, seen := a.attached[candidatePath]; seen {
				a.mu.Unlock()
				return nil
			}
			data, err := os.ReadFile(candidatePath)
			if err != nil {
				a.mu.Unlock()
				return nil
			}
			a.attached[candidatePath] = struct{}{}
			a.mu.Unlock()
			return &InstructionAttachment{
				Path:    candidatePath,
				Content: strings.TrimSpace(string(data)),
			}
		}
		if dir == absRoot || dir == "." || dir == "/" {
			return nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// Render produces the system-message content for an attachment. Mirrors
// OpenCode's format so the model sees a consistent shape.
func (a *InstructionAttachment) Render() string {
	if a == nil || strings.TrimSpace(a.Content) == "" {
		return ""
	}
	return fmt.Sprintf("Instructions from %s:\n\n%s", a.Path, a.Content)
}
