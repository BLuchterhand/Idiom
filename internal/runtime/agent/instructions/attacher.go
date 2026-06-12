package instructions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Attachment models a nearest project-instruction file lookup.
type Attachment struct {
	Path    string
	Content string
}

// Attacher walks the workspace and emits at-most-once attachments per file
// path per session. Safe for concurrent use.
type Attacher struct {
	root      string
	candidate []string

	mu       sync.Mutex
	attached map[string]struct{}
}

func NewAttacher(root string, candidates ...string) *Attacher {
	if len(candidates) == 0 {
		candidates = []string{"AGENTS.md"}
	}

	return &Attacher{
		root:      strings.TrimSpace(root),
		candidate: candidates,
		attached:  make(map[string]struct{}),
	}
}

func (a *Attacher) OnFileTouch(touchedPath string) *Attachment {
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

			return &Attachment{
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

func (a *Attachment) Render() string {
	if a == nil || strings.TrimSpace(a.Content) == "" {
		return ""
	}

	return fmt.Sprintf("Instructions from %s:\n\n%s", a.Path, a.Content)
}
