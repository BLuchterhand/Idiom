package patching

import (
	"fmt"
	"slices"
	"strings"
)

type OpKind string

const (
	OpAdd    OpKind = "add"
	OpUpdate OpKind = "update"
	OpDelete OpKind = "delete"
)

type Op struct {
	Kind    OpKind
	Path    string
	MoveTo  string
	NewBody string
	Hunks   []Hunk
}

type Hunk struct {
	ContextHeader string
	Lines         []Line
}

type Line struct {
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
	beginMarker            = "*** Begin Patch"
	endMarker              = "*** End Patch"
	addPrefix              = "*** Add File: "
	updatePrefix           = "*** Update File: "
	deletePrefix           = "*** Delete File: "
	movePrefix             = "*** Move to: "
	hunkMarker             = "@@"
	unknownDirectivePrefix = "*** "
)

func Parse(text string) ([]Op, error) {
	text = strings.TrimRight(text, "\n")

	lines := strings.Split(text, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != beginMarker {
		return nil, fmt.Errorf("patch must start with %q", beginMarker)
	}

	if strings.TrimSpace(lines[len(lines)-1]) != endMarker {
		return nil, fmt.Errorf("patch must end with %q", endMarker)
	}

	var (
		ops     []Op
		current *Op
		hunk    *Hunk
	)

	addBody := []string{}

	flushAdd := func() {
		if current != nil && current.Kind == OpAdd {
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
		case strings.HasPrefix(line, addPrefix):
			closeOp()

			current = &Op{Kind: OpAdd, Path: strings.TrimSpace(strings.TrimPrefix(line, addPrefix))}
		case strings.HasPrefix(line, updatePrefix):
			closeOp()

			current = &Op{Kind: OpUpdate, Path: strings.TrimSpace(strings.TrimPrefix(line, updatePrefix))}
		case strings.HasPrefix(line, deletePrefix):
			closeOp()

			current = &Op{Kind: OpDelete, Path: strings.TrimSpace(strings.TrimPrefix(line, deletePrefix))}
		case strings.HasPrefix(line, movePrefix):
			if current == nil || current.Kind != OpUpdate {
				return nil, fmt.Errorf("%q is only valid inside an Update File section", movePrefix)
			}

			current.MoveTo = strings.TrimSpace(strings.TrimPrefix(line, movePrefix))
		case strings.HasPrefix(line, unknownDirectivePrefix):
			continue
		case current == nil:
			if strings.TrimSpace(line) == "" {
				continue
			}

			return nil, fmt.Errorf("unexpected line outside of any file section: %q", line)
		case current.Kind == OpAdd:
			if !strings.HasPrefix(line, "+") {
				return nil, fmt.Errorf("add file body lines must start with '+': %q", line)
			}

			addBody = append(addBody, strings.TrimPrefix(line, "+"))
		case current.Kind == OpUpdate:
			if strings.HasPrefix(line, hunkMarker) {
				flushHunk()

				header := strings.TrimSpace(strings.TrimPrefix(line, hunkMarker))

				hunk = &Hunk{ContextHeader: header}

				continue
			}

			if hunk == nil {
				return nil, fmt.Errorf("update file body must contain a %q hunk header before content", hunkMarker)
			}

			switch {
			case strings.HasPrefix(line, "+"):
				hunk.Lines = append(hunk.Lines, Line{Kind: LineInsert, Text: strings.TrimPrefix(line, "+")})
			case strings.HasPrefix(line, "-"):
				hunk.Lines = append(hunk.Lines, Line{Kind: LineDelete, Text: strings.TrimPrefix(line, "-")})
			case strings.HasPrefix(line, " "):
				hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Text: strings.TrimPrefix(line, " ")})
			case line == "":
				hunk.Lines = append(hunk.Lines, Line{Kind: LineContext, Text: ""})
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

// Serialize reverses Parse: it turns a slice of Op into the canonical
// envelope text.
func Serialize(ops []Op) string {
	var b strings.Builder

	b.WriteString(beginMarker)
	b.WriteString("\n")

	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			b.WriteString(addPrefix)
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
		case OpUpdate:
			b.WriteString(updatePrefix)
			b.WriteString(strings.TrimSpace(op.Path))
			b.WriteString("\n")

			if mv := strings.TrimSpace(op.MoveTo); mv != "" {
				b.WriteString(movePrefix)
				b.WriteString(mv)
				b.WriteString("\n")
			}

			for _, hunk := range op.Hunks {
				b.WriteString(hunkMarker)

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
		case OpDelete:
			b.WriteString(deletePrefix)
			b.WriteString(strings.TrimSpace(op.Path))
			b.WriteString("\n")
		}
	}

	b.WriteString(endMarker)
	b.WriteString("\n")

	return b.String()
}

func OpPaths(ops []Op) []string {
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

	slices.Sort(out)

	return out
}

func NormalizeEnvelope(text string) string {
	body := strings.TrimSpace(text)
	if body == "" || strings.HasPrefix(body, beginMarker) {
		return text
	}

	if !startsWithFileSection(body) {
		return text
	}

	if strings.Contains(body, "\n"+beginMarker) || strings.Contains(body, "\n"+endMarker) {
		return text
	}

	return beginMarker + "\n" + strings.TrimRight(body, "\n") + "\n" + endMarker
}

func startsWithFileSection(body string) bool {
	first, _, _ := strings.Cut(body, "\n")

	first = strings.TrimSpace(first)

	return strings.HasPrefix(first, addPrefix) ||
		strings.HasPrefix(first, updatePrefix) ||
		strings.HasPrefix(first, deletePrefix)
}

func ApplyUpdateHunks(current string, hunks []Hunk) (string, error) {
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

func hunkBlocks(hunk Hunk) (string, string) {
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
