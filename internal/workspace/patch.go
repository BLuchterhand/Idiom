package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PatchOpKind string

const (
	PatchOpAdd    PatchOpKind = "add"
	PatchOpUpdate PatchOpKind = "update"
	PatchOpDelete PatchOpKind = "delete"
)

type PatchOp struct {
	Kind    PatchOpKind
	Path    string
	MoveTo  string
	NewBody string
	Hunks   []PatchHunk
}

type PatchHunk struct {
	ContextHeader string
	Lines         []PatchLine
}

type PatchLine struct {
	Kind LineKind
	Text string
}

type LineKind int

const (
	LineContext LineKind = iota
	LineDelete
	LineInsert
)

const (
	patchBeginMarker            = "*** Begin Patch"
	patchEndMarker              = "*** End Patch"
	patchAddPrefix              = "*** Add File: "
	patchUpdatePrefix           = "*** Update File: "
	patchDeletePrefix           = "*** Delete File: "
	patchMovePrefix             = "*** Move to: "
	patchHunkMarker             = "@@"
	patchUnknownDirectivePrefix = "*** "
)

func ParsePatch(text string) ([]PatchOp, error) {
	text = strings.TrimRight(text, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != patchBeginMarker {
		return nil, fmt.Errorf("patch must start with %q", patchBeginMarker)
	}
	if strings.TrimSpace(lines[len(lines)-1]) != patchEndMarker {
		return nil, fmt.Errorf("patch must end with %q", patchEndMarker)
	}

	var ops []PatchOp
	var current *PatchOp
	var hunk *PatchHunk
	addBody := []string{}

	flushAdd := func() {
		if current != nil && current.Kind == PatchOpAdd {
			current.NewBody = strings.Join(addBody, "\n")
		}
		addBody = nil
	}
	flushHunk := func() {
		if hunk != nil && current != nil {
			current.Hunks = append(current.Hunks, *hunk)
		}
		hunk = nil
	}
	closeOp := func() {
		flushAdd()
		flushHunk()
		if current != nil {
			ops = append(ops, *current)
		}
		current = nil
	}

	for i := 1; i < len(lines)-1; i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, patchAddPrefix):
			closeOp()
			current = &PatchOp{Kind: PatchOpAdd, Path: strings.TrimSpace(strings.TrimPrefix(line, patchAddPrefix))}
		case strings.HasPrefix(line, patchUpdatePrefix):
			closeOp()
			current = &PatchOp{Kind: PatchOpUpdate, Path: strings.TrimSpace(strings.TrimPrefix(line, patchUpdatePrefix))}
		case strings.HasPrefix(line, patchDeletePrefix):
			closeOp()
			current = &PatchOp{Kind: PatchOpDelete, Path: strings.TrimSpace(strings.TrimPrefix(line, patchDeletePrefix))}
		case strings.HasPrefix(line, patchMovePrefix):
			if current == nil || current.Kind != PatchOpUpdate {
				return nil, fmt.Errorf("%q is only valid inside an Update File section", patchMovePrefix)
			}
			current.MoveTo = strings.TrimSpace(strings.TrimPrefix(line, patchMovePrefix))
		case strings.HasPrefix(line, patchUnknownDirectivePrefix):
			// Unknown `*** Foo` directive — likely model commentary or a
			// non-standard envelope marker (e.g. `*** Sanity Check`). The
			// documented directives are matched earlier in this switch;
			// real hunk content always starts with `+`, `-`, ` `, or is
			// empty, so skipping here cannot swallow legitimate edits.
			// Silent-tolerate rather than hard-failing the entire patch.
			continue
		case current == nil:
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("unexpected line outside of any file section: %q", line)
		case current.Kind == PatchOpAdd:
			if !strings.HasPrefix(line, "+") {
				return nil, fmt.Errorf("Add File body lines must start with '+': %q", line)
			}
			addBody = append(addBody, strings.TrimPrefix(line, "+"))
		case current.Kind == PatchOpUpdate:
			if strings.HasPrefix(line, patchHunkMarker) {
				flushHunk()
				header := strings.TrimSpace(strings.TrimPrefix(line, patchHunkMarker))
				hunk = &PatchHunk{ContextHeader: header}
				continue
			}
			if hunk == nil {
				return nil, fmt.Errorf("Update File body must contain a %q hunk header before content", patchHunkMarker)
			}
			switch {
			case strings.HasPrefix(line, "+"):
				hunk.Lines = append(hunk.Lines, PatchLine{Kind: LineInsert, Text: strings.TrimPrefix(line, "+")})
			case strings.HasPrefix(line, "-"):
				hunk.Lines = append(hunk.Lines, PatchLine{Kind: LineDelete, Text: strings.TrimPrefix(line, "-")})
			case strings.HasPrefix(line, " "):
				hunk.Lines = append(hunk.Lines, PatchLine{Kind: LineContext, Text: strings.TrimPrefix(line, " ")})
			case line == "":
				hunk.Lines = append(hunk.Lines, PatchLine{Kind: LineContext, Text: ""})
			default:
				return nil, fmt.Errorf("hunk lines must start with ' ', '+', '-', or be empty: %q", line)
			}
		}
	}
	closeOp()

	if len(ops) == 0 {
		return nil, fmt.Errorf("patch contained no file sections")
	}
	for i := range ops {
		if strings.TrimSpace(ops[i].Path) == "" {
			return nil, fmt.Errorf("patch op %d has an empty path", i+1)
		}
	}
	return ops, nil
}

// SerializePatch reverses ParsePatch: it turns a slice of PatchOp into the
// canonical *** Begin Patch / *** End Patch envelope text. Used by the
// runner-side pre-flight when it needs to drop redundant sections from a
// model-issued patch and re-submit the trimmed envelope to applyPatch
// without changing applyPatch's semantics.
func SerializePatch(ops []PatchOp) string {
	var b strings.Builder
	b.WriteString(patchBeginMarker)
	b.WriteString("\n")
	for _, op := range ops {
		switch op.Kind {
		case PatchOpAdd:
			b.WriteString(patchAddPrefix)
			b.WriteString(strings.TrimSpace(op.Path))
			b.WriteString("\n")
			body := op.NewBody
			if body != "" {
				for _, line := range strings.Split(body, "\n") {
					b.WriteString("+")
					b.WriteString(line)
					b.WriteString("\n")
				}
			}
		case PatchOpUpdate:
			b.WriteString(patchUpdatePrefix)
			b.WriteString(strings.TrimSpace(op.Path))
			b.WriteString("\n")
			if mv := strings.TrimSpace(op.MoveTo); mv != "" {
				b.WriteString(patchMovePrefix)
				b.WriteString(mv)
				b.WriteString("\n")
			}
			for _, hunk := range op.Hunks {
				b.WriteString(patchHunkMarker)
				if header := strings.TrimSpace(hunk.ContextHeader); header != "" {
					b.WriteString(" ")
					b.WriteString(header)
				}
				b.WriteString("\n")
				for _, line := range hunk.Lines {
					switch line.Kind {
					case LineContext:
						b.WriteString(" ")
						b.WriteString(line.Text)
					case LineInsert:
						b.WriteString("+")
						b.WriteString(line.Text)
					case LineDelete:
						b.WriteString("-")
						b.WriteString(line.Text)
					}
					b.WriteString("\n")
				}
			}
		case PatchOpDelete:
			b.WriteString(patchDeletePrefix)
			b.WriteString(strings.TrimSpace(op.Path))
			b.WriteString("\n")
		}
	}
	b.WriteString(patchEndMarker)
	b.WriteString("\n")
	return b.String()
}

func PatchOpPaths(ops []PatchOp) []string {
	seen := make(map[string]struct{}, len(ops)*2)
	out := make([]string, 0, len(ops)*2)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, op := range ops {
		add(op.Path)
		if op.MoveTo != "" {
			add(op.MoveTo)
		}
	}
	sort.Strings(out)
	return out
}

func (m *Manager) applyPatch(req ToolRequest) ToolResult {
	if !m.cfg.EnableWrites {
		return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: "workspace writes are disabled"}
	}
	body := normalizePatchEnvelope(req.Content)
	if strings.TrimSpace(body) == "" {
		return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: "apply_patch requires the patch body in content"}
	}
	ops, err := ParsePatch(body)
	if err != nil {
		return ToolResult{Tool: "apply_patch", Summary: "Patch parse failed", Error: err.Error()}
	}

	type plannedWrite struct {
		path     string
		contents []byte
		remove   bool
	}
	planned := make([]plannedWrite, 0, len(ops))
	summaries := make([]string, 0, len(ops))

	for opIdx, op := range ops {
		path, err := m.resolvePath(op.Path, req.Workdir)
		if err != nil {
			return ToolResult{Tool: "apply_patch", Summary: "Failed to resolve path", Error: fmt.Sprintf("op %d (%s %s): %v", opIdx+1, op.Kind, op.Path, err)}
		}

		switch op.Kind {
		case PatchOpAdd:
			if _, statErr := os.Stat(path); statErr == nil {
				return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: fmt.Sprintf("Add File target %s already exists", m.displayPath(path))}
			}
			body := op.NewBody
			if body != "" && !strings.HasSuffix(body, "\n") {
				body += "\n"
			}
			planned = append(planned, plannedWrite{path: path, contents: []byte(body)})
			summaries = append(summaries, fmt.Sprintf("add %s (%d bytes)", m.displayPath(path), len(body)))
		case PatchOpDelete:
			info, statErr := os.Stat(path)
			if statErr != nil {
				return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: fmt.Sprintf("Delete File target %s not found", m.displayPath(path))}
			}
			if info.IsDir() {
				return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: fmt.Sprintf("Delete File target %s is a directory", m.displayPath(path))}
			}
			planned = append(planned, plannedWrite{path: path, remove: true})
			summaries = append(summaries, fmt.Sprintf("delete %s", m.displayPath(path)))
		case PatchOpUpdate:
			current, readErr := os.ReadFile(path)
			if readErr != nil {
				return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: fmt.Sprintf("Update File target %s: %v", m.displayPath(path), readErr)}
			}
			updated, applyErr := applyUpdateHunks(string(current), op.Hunks)
			if applyErr != nil {
				return ToolResult{Tool: "apply_patch", Summary: "Patch hunk did not apply", Error: fmt.Sprintf("Update File %s: %v", m.displayPath(path), applyErr)}
			}
			destPath := path
			summary := fmt.Sprintf("update %s", m.displayPath(path))
			if op.MoveTo != "" {
				destPath, err = m.resolvePath(op.MoveTo, req.Workdir)
				if err != nil {
					return ToolResult{Tool: "apply_patch", Summary: "Failed to resolve move destination", Error: fmt.Sprintf("Move to %s: %v", op.MoveTo, err)}
				}
				if _, statErr := os.Stat(destPath); statErr == nil && filepath.Clean(destPath) != filepath.Clean(path) {
					return ToolResult{Tool: "apply_patch", Summary: "Patch request rejected", Error: fmt.Sprintf("Move to destination %s already exists", m.displayPath(destPath))}
				}
				planned = append(planned, plannedWrite{path: path, remove: true})
				summary = fmt.Sprintf("update+move %s -> %s", m.displayPath(path), m.displayPath(destPath))
			}
			planned = append(planned, plannedWrite{path: destPath, contents: []byte(updated)})
			summaries = append(summaries, summary)
		}
	}

	for _, write := range planned {
		if write.remove {
			continue
		}
		if rejected := m.rejectInvalidPlannedWrite("apply_patch", "Patch request rejected", write.path, write.contents); rejected != nil {
			return *rejected
		}
	}

	for _, write := range planned {
		if write.remove {
			if err := os.Remove(write.path); err != nil && !os.IsNotExist(err) {
				return ToolResult{Tool: "apply_patch", Summary: "Patch apply failed mid-stream", Error: fmt.Sprintf("delete %s: %v", m.displayPath(write.path), err)}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(write.path), 0o755); err != nil {
			return ToolResult{Tool: "apply_patch", Summary: "Patch apply failed", Error: fmt.Sprintf("mkdir %s: %v", m.displayPath(filepath.Dir(write.path)), err)}
		}
		if err := os.WriteFile(write.path, write.contents, 0o644); err != nil {
			return ToolResult{Tool: "apply_patch", Summary: "Patch apply failed", Error: fmt.Sprintf("write %s: %v", m.displayPath(write.path), err)}
		}
	}

	m.invalidateGoIndex()
	m.invalidateCppIndex()
	return ToolResult{
		Tool:    "apply_patch",
		Summary: fmt.Sprintf("Applied patch with %d op(s)", len(ops)),
		Output:  strings.Join(summaries, "\n"),
	}
}

func normalizePatchEnvelope(text string) string {
	body := strings.TrimSpace(text)
	if body == "" || strings.HasPrefix(body, patchBeginMarker) {
		return text
	}
	if !patchStartsWithFileSection(body) {
		return text
	}
	if strings.Contains(body, "\n"+patchBeginMarker) || strings.Contains(body, "\n"+patchEndMarker) {
		return text
	}
	return patchBeginMarker + "\n" + strings.TrimRight(body, "\n") + "\n" + patchEndMarker
}

func patchStartsWithFileSection(body string) bool {
	first, _, _ := strings.Cut(body, "\n")
	first = strings.TrimSpace(first)
	return strings.HasPrefix(first, patchAddPrefix) ||
		strings.HasPrefix(first, patchUpdatePrefix) ||
		strings.HasPrefix(first, patchDeletePrefix)
}

func applyUpdateHunks(current string, hunks []PatchHunk) (string, error) {
	if len(hunks) == 0 {
		return "", fmt.Errorf("update has no hunks")
	}
	updated := current
	for hunkIdx, hunk := range hunks {
		oldBlock, newBlock := hunkBlocks(hunk)
		if oldBlock == "" && newBlock == "" {
			return "", fmt.Errorf("hunk %d is empty", hunkIdx+1)
		}
		matches := strings.Count(updated, oldBlock)
		if matches == 0 {
			return "", fmt.Errorf("hunk %d context did not match the file", hunkIdx+1)
		}
		if matches > 1 {
			return "", fmt.Errorf("hunk %d context matched %d locations; add more context to disambiguate", hunkIdx+1, matches)
		}
		updated = strings.Replace(updated, oldBlock, newBlock, 1)
	}
	return updated, nil
}

func hunkBlocks(hunk PatchHunk) (string, string) {
	var oldLines, newLines []string
	for _, l := range hunk.Lines {
		switch l.Kind {
		case LineContext:
			oldLines = append(oldLines, l.Text)
			newLines = append(newLines, l.Text)
		case LineDelete:
			oldLines = append(oldLines, l.Text)
		case LineInsert:
			newLines = append(newLines, l.Text)
		}
	}
	return joinHunkLines(oldLines), joinHunkLines(newLines)
}

func joinHunkLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
