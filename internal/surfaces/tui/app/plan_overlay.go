package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// planItemMeta describes one clickable plan entry. DiffID is the id the
// user clicks through to; for ledger items that haven't been attached
// yet DiffID is empty and the item is inert.
type planItemMeta struct {
	LineIdx            int
	RawText            string
	DiffID             string
	TaskID             string
	PlanEditID         string
	PlanApprovalAction planApprovalAction
	Checked            bool
}

var (
	planDiffAnchorRe     = regexp.MustCompile(`<!--\s*id:\s*([^ \t<]+?)\s*-->`)
	planTaskAnchorRe     = regexp.MustCompile(`<!--\s*tid:\s*([^ \t<]+?)\s*-->`)
	planPEAnchorRe       = regexp.MustCompile(`<!--\s*pid:\s*([^ \t<]+?)\s*-->`)
	planApprovalAnchorRe = regexp.MustCompile(`<!--\s*plan-approval:\s*([^ \t<]+?)\s*-->`)
)

// parsePlanItems walks the plan markdown and returns one entry per
// checkbox line, recording whichever anchors it carries. Non-checkbox
// lines are ignored so headings and prose don't inflate the cursor
// stops when the user navigates with j/k.
func parsePlanItems(plan string) []planItemMeta {
	var items []planItemMeta

	for i, raw := range strings.Split(plan, "\n") {
		meta, ok := parsePlanItemLine(i, raw)
		if !ok {
			continue
		}

		items = append(items, meta)
	}

	return items
}

func parsePlanItemLine(lineIdx int, raw string) (planItemMeta, bool) {
	trimmed := strings.TrimLeft(raw, " \t")
	if !strings.HasPrefix(trimmed, "- [") && !strings.HasPrefix(trimmed, "* [") {
		return planItemMeta{}, false
	}

	if len(trimmed) < 5 {
		return planItemMeta{}, false
	}

	meta := planItemMeta{LineIdx: lineIdx, RawText: raw}
	box := trimmed[3]

	meta.Checked = box == 'x' || box == 'X'

	if m := planDiffAnchorRe.FindStringSubmatch(raw); len(m) == 2 {
		meta.DiffID = m[1]
	}

	if m := planTaskAnchorRe.FindStringSubmatch(raw); len(m) == 2 {
		meta.TaskID = m[1]
	}

	if m := planPEAnchorRe.FindStringSubmatch(raw); len(m) == 2 {
		meta.PlanEditID = m[1]
	}

	if m := planApprovalAnchorRe.FindStringSubmatch(raw); len(m) == 2 {
		meta.PlanApprovalAction, _ = parsePlanApprovalAction(m[1])
	}

	return meta, true
}

// loadPlanDiffEntry scans the session's diffs JSONL file for the
// record with the given id. Linear scan because we expect per-session
// record counts in the dozens; a per-file index would add complexity
// that doesn't pay off at this scale.
func (m *Model) loadPlanDiffEntry(id string) (*planDiffEntry, error) {
	if m == nil {
		return nil, fmt.Errorf("nil model")
	}

	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("empty diff id")
	}

	path := m.diffsPath()
	if path == "" {
		return nil, fmt.Errorf("no session diffs path")
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024*4) // diffs can be large

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec planDiffEntry

		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		if rec.ID == id {
			return &rec, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("diff id %q not found in %s", id, path)
}

// Styles for diff lines. Kept private to this file so the palette stays
// cohesive with the rest of the plan panel's lipgloss usage.
var (
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#38BB71"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5484D"))
	diffHunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E97A7")).Bold(true)
	diffHeaderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8E97A7"))
)

// renderColoredDiff applies ANSI colors to a unified-diff payload so
// the overlay is scannable at a glance. Safe to call with an empty
// diff (e.g. mkdir), in which case a short placeholder is returned.
func renderColoredDiff(diff string) string {
	if strings.TrimSpace(diff) == "" {
		return diffHeaderStyle.Render("(no diff — tool produced no file change)")
	}

	var out strings.Builder

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			out.WriteString(diffHeaderStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			out.WriteString(diffHunkStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			out.WriteString(diffAddStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			out.WriteString(diffRemoveStyle.Render(line))
		case strings.HasPrefix(line, `\ `):
			out.WriteString(diffHeaderStyle.Render(line))
		default:
			out.WriteString(line)
		}

		out.WriteString("\n")
	}

	return strings.TrimRight(out.String(), "\n")
}

// renderPlanDiffOverlay builds the full overlay body for a given diff
// entry: a short header (tool / path / description) followed by the
// colored diff. Callers place this inside the plan viewport.
func renderPlanDiffOverlay(entry *planDiffEntry) string {
	if entry == nil {
		return diffHeaderStyle.Render("(no diff selected)")
	}

	var b strings.Builder

	b.WriteString(diffHunkStyle.Render(fmt.Sprintf("◆ %s %s", entry.Tool, entry.Path)))
	b.WriteString("\n")

	if desc := strings.TrimSpace(entry.Description); desc != "" {
		b.WriteString(diffHeaderStyle.Render("  " + desc))
		b.WriteString("\n")
	}

	if summary := strings.TrimSpace(entry.Summary); summary != "" && summary != entry.Description {
		b.WriteString(diffHeaderStyle.Render("  " + summary))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(renderColoredDiff(entry.Diff))

	return b.String()
}
