package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/contracts/taskledger"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/shortid"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/planexecute"
)

func mutatePlanLines(path string, fn func(lines []string) []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")

	lines = fn(lines)

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

func (m *promptExecutionSession) mutatePlanLines(path string, fn func(lines []string) []string) error {
	err := mutatePlanLines(path, fn)
	if err == nil {
		m.invalidatePlanCache()
	}

	return err
}

func (m *Model) mutatePlanLines(path string, fn func(lines []string) []string) error {
	err := mutatePlanLines(path, fn)
	if err == nil {
		m.invalidatePlanCache()
	}

	return err
}

// maxFailureArgsLen caps how much of the serialized ToolRequest is stored
// in the failures log. A bad write_file with a megabyte of content would
// otherwise make the log file unusable for a human scroll. 8 KiB is more
// than enough to reproduce any reasonable call while keeping each line
// navigable; past the cap we append a marker so the truncation is obvious.
const maxFailureArgsLen = 8192

// truncateForLog clips s to at most max bytes, appending a clear marker
// when it does so. Preserves valid JSON-ish readability by keeping the
// prefix intact rather than trying to reshape the payload.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return s[:max] + "…[truncated]"
}

// failureRecord is one entry in <session-id>.failures.jsonl. Fields are
// JSON-tagged lowercase to match the streaming event format; unknown or
// empty values are omitted so records stay scannable in a terminal.
type failureRecord struct {
	Timestamp   string `json:"timestamp"`
	Step        int    `json:"step,omitempty"`
	Tool        string `json:"tool"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
	Args        string `json:"args,omitempty"`
}

// planDiffEntry is one record in <session-id>.diffs.jsonl. The id is
// the stable handle the plan markdown line points to; Diff carries
// the unified-diff text used by the clickable overlay in the UI.
type planDiffEntry struct {
	ID          string `json:"id"`
	Timestamp   string `json:"timestamp"`
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Summary     string `json:"summary"`
	Diff        string `json:"diff"`
}

// failuresPath returns the absolute path of the session's failures log.
// It mirrors ensurePlanPath: both files live under .planning/<session-id>.*
// so a resumed session finds both attached artifacts without bookkeeping.
func (m *promptExecutionSession) failuresPath() string {
	id := sessionlog.SessionIDFromPath(m.debugLogPath)
	if id == "" {
		return ""
	}

	dir := planDirName
	if root := m.workspace.Root(); root != "" {
		dir = filepath.Join(root, planDirName)
	}

	return filepath.Join(dir, id+".failures.jsonl")
}

// recordWriteFailure appends a failure entry for any write-tool call
// that returned an error. Only write tools are recorded — read/search
// failures are already visible in the transcript and would drown the
// log in noise.
//
// The function is intentionally permissive: it creates the directory if
// missing and silently drops the write if the session id isn't resolvable
// yet (very first turn edge case). A failed write-of-a-failure must not
// break the agent loop.
func (m *promptExecutionSession) recordWriteFailure(step int, req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil {
		return
	}

	if strings.TrimSpace(result.Error) == "" {
		return
	}

	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return
	}

	path := m.failuresPath()
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	argsJSON, _ := json.Marshal(req)
	rec := failureRecord{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Step:        step,
		Tool:        req.Tool,
		Path:        req.Path,
		Description: req.Description,
		Summary:     result.Summary,
		Error:       result.Error,
		Args:        truncateForLog(string(argsJSON), maxFailureArgsLen),
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	defer m.invalidatePlanCache()

	if _, err := fmt.Fprintln(f, string(line)); err != nil {
		return
	}
}

// recordWriteSuccess lands a successful write into the plan: for each
// exact file change attached by the rooted workspace transaction, it computes
// a unified diff, appends a record to <id>.diffs.jsonl, and adds a checked-off
// line to the plan markdown. It never reconstructs commit evidence with a
// later host filesystem read.
func (m *promptExecutionSession) recordWriteSuccess(step int, req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil || m.workspace == nil {
		return
	}

	// Only land success cases; failures are captured by recordWriteFailure.
	if strings.TrimSpace(result.Error) != "" {
		return
	}

	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return
	}

	if sessionlog.SessionIDFromPath(m.debugLogPath) == "" {
		return
	}

	// Write the session heading once per live session.
	m.ensurePlanSessionHeading()

	var changes []workspace.MutationFileChange
	if receipt := result.CommitReceipt(); receipt != nil {
		changes = receipt.FileChanges()
	}

	// Directory-only mutations and results without exact private commit bodies
	// still receive a plan entry, but never a fabricated file diff.
	if len(changes) == 0 {
		id := newPlanEntryID()
		m.appendPlanDiffEntry(planDiffEntry{
			ID:          id,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
			Step:        step,
			Tool:        req.Tool,
			Path:        req.Path,
			Description: req.Description,
			Summary:     result.Summary,
			Diff:        "",
		})

		if !m.attachWriteToPlannedItem(req, strings.TrimSpace(req.Path), id) &&
			!m.attachWriteToSubmittedEdit(strings.TrimSpace(req.Path), id) {

			m.appendUnplannedCheckedLine(planLineLabel(req, ""), id)
		}

		return
	}

	for _, change := range changes {
		before, _ := change.BeforeBody()
		after, _ := change.AfterBody()
		path := change.CanonicalID()
		diff := planexecute.UnifiedDiff(path, string(before), string(after))
		id := newPlanEntryID()
		m.appendPlanDiffEntry(planDiffEntry{
			ID:          id,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
			Step:        step,
			Tool:        req.Tool,
			Path:        path,
			Description: req.Description,
			Summary:     result.Summary,
			Diff:        diff,
		})

		if !m.attachWriteToPlannedItem(req, path, id) &&
			!m.attachWriteToSubmittedEdit(path, id) {

			m.appendUnplannedCheckedLine(planLineLabel(req, path), id)
		}
	}
}

// appendUnplannedCheckedLine writes a `- [x] ... <!-- id:... -->` line
// under a `### Unplanned` subsection. The subsection header is added
// once per session — subsequent unplanned writes append to it. Using a
// dedicated subsection keeps ledger-covered work visually separated
// from drive-by edits, so a reviewer can see at a glance which writes
// exceeded the triage plan.
func (m *promptExecutionSession) appendUnplannedCheckedLine(label, id string) {
	path := m.ensurePlanPath()
	if path == "" {
		return
	}

	existing, _ := os.ReadFile(path)
	needsHeader := !strings.Contains(string(existing), "### Unplanned")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	defer m.invalidatePlanCache()

	if needsHeader {
		if _, err := f.WriteString("### Unplanned\n"); err != nil {
			return
		}
	}

	line := fmt.Sprintf("- [x] %s <!-- id:%s -->\n", label, id)

	_, _ = f.WriteString(line)
}

// planLineLabel builds the human-readable text that appears after the
// checkbox. Prefers the model-supplied Description; falls back to the
// path or tool name so every line is at least scannable.
func planLineLabel(req workspace.ToolRequest, path string) string {
	desc := strings.TrimSpace(req.Description)
	if path == "" {
		path = strings.TrimSpace(req.Path)
	}

	if desc != "" && path != "" {
		return fmt.Sprintf("%s (%s)", desc, path)
	}

	if desc != "" {
		return desc
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) {
		if s := toolIntent(req.Tool, path); s != "" {
			return s
		}

		if path != "" {
			return fmt.Sprintf("%s %s", req.Tool, path)
		}

		return req.Tool
	}

	if path != "" {
		return fmt.Sprintf("%s %s", req.Tool, path)
	}

	return req.Tool
}

// ensurePlanSessionHeading appends a `## Session <id>` heading to the
// plan file the first time the autodoc lands a record in this live
// session. If the plan file is empty or missing the heading also
// includes a top-level title so fresh sessions get a usable document.
func (m *promptExecutionSession) ensurePlanSessionHeading() {
	if m == nil || m.plan.sessionHeadingWritten {
		return
	}

	path := m.ensurePlanPath()
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	// Read existing content so we don't duplicate the heading across
	// process restarts that resumed into the same session id.
	existing, _ := os.ReadFile(path)
	heading := fmt.Sprintf("## Session %s — %s\n\n", sessionlog.SessionIDFromPath(m.debugLogPath), time.Now().UTC().Format("2006-01-02 15:04"))

	if strings.Contains(string(existing), "## Session "+sessionlog.SessionIDFromPath(m.debugLogPath)) {
		m.plan.sessionHeadingWritten = true
		return
	}

	var prelude string

	if len(existing) == 0 {
		prelude = fmt.Sprintf("# Plan — %s\n\n", sessionlog.SessionIDFromPath(m.debugLogPath))
	} else if !strings.HasSuffix(string(existing), "\n") {
		prelude = "\n"
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	if _, err := f.WriteString(prelude + heading); err != nil {
		return
	}

	m.plan.sessionHeadingWritten = true
}

// appendPlanDiffEntry writes one JSONL record to <id>.diffs.jsonl.
// Silently drops on any I/O error — a failed diff-log write must not
// break the agent loop, the same invariant recordWriteFailure honors.
func (m *promptExecutionSession) appendPlanDiffEntry(entry planDiffEntry) {
	root := ""
	if m.workspace != nil {
		root = m.workspace.Root()
	}
	path := planningArtifactPath(root, sessionlog.SessionIDFromPath(m.debugLogPath), ".diffs.jsonl")
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = fmt.Fprintln(f, string(line))
}

// newPlanEntryID returns a short, URL-safe hex id used to tie a plan
// markdown line to its diff record. 8 bytes is collision-safe across
// a session's worth of writes while staying visually compact inside
// the HTML comment marker.
func newPlanEntryID() string {
	return shortid.New()
}

// recordTaskLedger merges a task_ledger tool result into the in-memory
// ledger snapshot, and appends any newly-introduced items to the plan
// markdown under a "### Planned" subsection. Subsequent calls update
// the snapshot but do not re-append items already rendered: items
// already in the plan get checked off in place by recordWriteSuccess
// when a matching write lands.
//
// Called from the onToolResult handler for any task_ledger event with
// no error. Safe to call with a nil receiver or missing session id.
func (m *promptExecutionSession) recordTaskLedger(req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil {
		return
	}

	if strings.TrimSpace(req.Tool) != "task_ledger" {
		return
	}

	if strings.TrimSpace(result.Error) != "" {
		return
	}

	if sessionlog.SessionIDFromPath(m.debugLogPath) == "" {
		return
	}

	items := req.Todos
	if len(items) == 0 {
		items = req.Items
	}

	if len(items) == 0 {
		return
	}

	m.ensurePlanSessionHeading()

	if m.plan.ledgerItems == nil {
		m.plan.ledgerItems = map[string]workspace.TaskLedgerItem{}
	}

	if m.plan.ledgerRenderedIDs == nil {
		m.plan.ledgerRenderedIDs = map[string]bool{}
	}

	renderedSnapshot := m.renderedLedgerSnapshot()

	var newItems []workspace.TaskLedgerItem

	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}

		m.plan.ledgerItems[item.ID] = item
		if !m.plan.ledgerRenderedIDs[item.ID] {
			if m.findDuplicateLedgerItemID(item.Content, renderedSnapshot) != "" {
				m.plan.ledgerRenderedIDs[item.ID] = true
				continue
			}

			newItems = append(newItems, item)
			m.plan.ledgerRenderedIDs[item.ID] = true
		} else {
			m.updatePlanItemCheckbox(item)
		}
	}

	if len(newItems) == 0 {
		return
	}

	m.appendPlannedItems(newItems)
}

// appendPlannedItems writes a `### Planned` subsection (once) and then
// appends each new item as a checkbox line with its ledger-id anchor.
// The subsection header is added only if the plan doesn't already
// contain one under the current session, so resumed sessions stay
// coherent.
func (m *promptExecutionSession) appendPlannedItems(items []workspace.TaskLedgerItem) {
	path := m.ensurePlanPath()
	if path == "" {
		return
	}

	existing, _ := os.ReadFile(path)
	needsHeader := !strings.Contains(string(existing), "### Planned")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	if needsHeader {
		if _, err := f.WriteString("### Planned\n"); err != nil {
			return
		}
	}

	for _, item := range items {
		_, _ = f.WriteString(plannedItemLine(item))
	}
}

// renderedLedgerSnapshot returns a copy of the currently-rendered ledger items
// for use as a dedup baseline within a single recordTaskLedger call.
func (m *promptExecutionSession) renderedLedgerSnapshot() map[string]workspace.TaskLedgerItem {
	if len(m.plan.ledgerRenderedIDs) == 0 {
		return nil
	}

	out := make(map[string]workspace.TaskLedgerItem, len(m.plan.ledgerRenderedIDs))
	for id := range m.plan.ledgerRenderedIDs {
		if item, ok := m.plan.ledgerItems[id]; ok {
			out[id] = item
		}
	}

	return out
}

// normalizeLedgerContent folds case and whitespace for cheap content comparison.
func normalizeLedgerContent(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

const ledgerDedupTimeout = 10 * time.Second

// findDuplicateLedgerItemID returns the ID of an already-rendered item that is
// semantically equivalent to newContent, or "" if none matches. It tries a
// cheap normalized-content comparison first; on a miss it asks the model via
// completeChatWithFallback with a short timeout so the synchronous call never
// blocks a turn for more than ledgerDedupTimeout.
func (m *promptExecutionSession) findDuplicateLedgerItemID(newContent string, rendered map[string]workspace.TaskLedgerItem) string {
	if len(rendered) == 0 {
		return ""
	}

	norm := normalizeLedgerContent(newContent)
	for id, item := range rendered {
		if normalizeLedgerContent(item.Content) == norm {
			return id
		}
	}

	// Semantic check via model.
	ids := make([]string, 0, len(rendered))
	for id := range rendered {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	var sb strings.Builder

	sb.WriteString("Given a new task and a list of existing tasks, reply with the ID of the semantically equivalent existing task, or \"none\". Reply with the ID only — no explanation.\n\nNew task: ")
	sb.WriteString(newContent)
	sb.WriteString("\n\nExisting tasks:\n")

	for _, id := range ids {
		sb.WriteString(id)
		sb.WriteString(": ")
		sb.WriteString(rendered[id].Content)
		sb.WriteString("\n")
	}

	ctx, cancel := context.WithTimeout(context.Background(), ledgerDedupTimeout)
	defer cancel()

	thinkingOff := false

	reply, err := m.completeChatWithFallback(ctx, []llama.ChatMessage{
		{Role: "user", Content: sb.String()},
	}, llama.ChatOptions{
		MaxTokens:          16,
		Temperature:        0.0,
		EnableThinking:     &thinkingOff,
		ChatTemplateKwargs: true,
	})
	if err != nil {
		return ""
	}

	reply = strings.TrimSpace(reply)
	if reply == "" || strings.EqualFold(reply, "none") {
		return ""
	}

	if _, ok := rendered[reply]; ok {
		return reply
	}

	return ""
}

// plannedItemLine formats one task-ledger item as a plan markdown line.
// completed/cancelled items render with a filled box so resumed plans
// show the already-done portion of prior work accurately.
func plannedItemLine(item workspace.TaskLedgerItem) string {
	box := "[ ]"

	status, _ := taskledger.Parse(string(item.Status))
	switch {
	case status.Finished():
		box = "[x]"
	case status == taskledger.StatusInProgress:
		box = "[~]"
	}

	content := strings.TrimSpace(item.Content)
	if content == "" {
		content = item.ID
	}

	var pathsNote string

	if len(item.Paths) > 0 {
		pathsNote = fmt.Sprintf(" (paths: %s)", strings.Join(item.Paths, ", "))
	}

	return fmt.Sprintf("- %s %s%s <!-- tid:%s -->\n", box, content, pathsNote, item.ID)
}

// attachWriteToPlannedItem checks the plan file for a line whose tid
// anchor matches a ledger item covering the written path, and
// rewrites that line in place to use `[x]` and append the diff-id
// anchor. Returns true on a successful attach so recordWriteSuccess
// knows not to also append an "Unplanned" entry.
//
// Matching strategy, in priority order:
//  1. Explicit req.TaskID — the model declared this write fulfills
//     a specific planned item. Most reliable; works even when the
//     ledger entry didn't list paths or the write touches a
//     different path than planned.
//  2. Path match — a pending item lists this write's path in its
//     Paths slice.
//  3. Heuristic fallback — exactly one pending item has no paths
//     at all. We take that as "the model forgot to attach paths
//     but there's only one candidate," which is the common shape
//     of a single-task plan where the triage was terse.
//
// The replacement preserves everything on the existing line (content,
// paths annotation, indent) so a user hand-editing the plan doesn't
// lose their notes.
func (m *promptExecutionSession) attachWriteToPlannedItem(req workspace.ToolRequest, path, diffID string) bool {
	if m == nil || len(m.plan.ledgerItems) == 0 {
		return false
	}

	planPath := m.ensurePlanPath()
	if planPath == "" {
		return false
	}

	// Iterate in sorted id order so that when multiple items are
	// candidates (e.g. share a path, or multiple paths-less items
	// exist for the heuristic) the choice is deterministic.
	ids := make([]string, 0, len(m.plan.ledgerItems))
	for id := range m.plan.ledgerItems {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	pending := func(item workspace.TaskLedgerItem) bool {
		status, _ := taskledger.Parse(string(item.Status))
		return !status.Finished()
	}

	var (
		matchID   string
		matchItem workspace.TaskLedgerItem
	)

	// 1) Explicit task_id on the write request wins outright.
	if tid := strings.TrimSpace(req.TaskID); tid != "" {
		if item, ok := m.plan.ledgerItems[tid]; ok && pending(item) {
			matchID = tid
			matchItem = item
		}
	}

	// 2) Path match against a pending item's Paths.
	if matchID == "" {
		for _, id := range ids {
			item := m.plan.ledgerItems[id]
			if !pending(item) {
				continue
			}

			hits := m.plan.ledgerHits[id]

			for _, p := range item.Paths {
				trimmed := strings.TrimSpace(p)
				if trimmed != path {
					continue
				}

				if hits[trimmed] {
					// This path already attached on a previous write; skip
					// so a second write to the same path lands as Unplanned
					// rather than silently overwriting.
					continue
				}

				matchID = id
				matchItem = item

				break
			}

			if matchID != "" {
				break
			}
		}
	}

	// 3) Heuristic fallback: exactly one pending item has no paths.
	//    This covers the common miss where the model omitted paths
	//    from its task_ledger call. Ambiguous cases (multiple
	//    paths-less pending items) fall through to Unplanned so the
	//    user isn't silently mis-attributed.
	if matchID == "" {
		var (
			candidateID   string
			candidateItem workspace.TaskLedgerItem
			candidates    int
		)

		for _, id := range ids {
			item := m.plan.ledgerItems[id]
			if !pending(item) {
				continue
			}

			if len(item.Paths) != 0 {
				continue
			}

			candidateID = id
			candidateItem = item
			candidates++

			if candidates > 1 {
				break
			}
		}

		if candidates == 1 {
			matchID = candidateID
			matchItem = candidateItem
		}
	}

	if matchID == "" {
		return false
	}

	anchor := "<!-- tid:" + matchID + " -->"

	raw, err := os.ReadFile(planPath)
	if err != nil {
		return false
	}

	if !strings.Contains(string(raw), anchor) {
		return false
	}

	// Record the hit before we decide whether the item is fully
	// fulfilled — the current write counts toward completion.
	if m.plan.ledgerHits == nil {
		m.plan.ledgerHits = map[string]map[string]bool{}
	}

	if m.plan.ledgerHits[matchID] == nil {
		m.plan.ledgerHits[matchID] = map[string]bool{}
	}

	m.plan.ledgerHits[matchID][path] = true

	// Only flip to [x] once every listed path for the item has been
	// written. Multi-path items stay [ ] until the final path lands.
	allHit := true

	for _, p := range matchItem.Paths {
		if !m.plan.ledgerHits[matchID][strings.TrimSpace(p)] {
			allHit = false
			break
		}
	}

	var changed bool

	if err := m.mutatePlanLines(planPath, func(lines []string) []string {
		for i, line := range lines {
			if !strings.Contains(line, anchor) {
				continue
			}

			newLine := line
			if allHit {
				newLine = strings.Replace(newLine, "- [ ]", "- [x]", 1)
				newLine = strings.Replace(newLine, "* [ ]", "* [x]", 1)
			}

			newLine = strings.TrimRight(newLine, " ") + " <!-- id:" + diffID + " -->"
			lines[i] = newLine
			changed = true

			break
		}

		return lines
	}); err != nil || !changed {
		return false
	}

	if allHit {
		item := m.plan.ledgerItems[matchID]

		item.Status = taskledger.StatusCompleted
		m.plan.ledgerItems[matchID] = item
	}

	return true
}

// attachWriteToSubmittedEdit matches a successful write against a
// submit_plan checklist entry by path, flipping [ ] → [x] and
// appending the diff anchor. Returns true on a successful match.
func (m *promptExecutionSession) attachWriteToSubmittedEdit(path, diffID string) bool {
	if m == nil || len(m.plan.submittedEdits) == 0 {
		return false
	}

	planPath := m.ensurePlanPath()
	if planPath == "" {
		return false
	}

	ids := make([]string, 0, len(m.plan.submittedEdits))
	for id := range m.plan.submittedEdits {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	var matchID string

	for _, id := range ids {
		edit := m.plan.submittedEdits[id]
		if strings.TrimSpace(edit.Path) != path {
			continue
		}

		anchor := "<!-- pid:" + id + " -->"
		data, err := os.ReadFile(planPath)

		if err != nil || !strings.Contains(string(data), anchor) {
			continue
		}

		// Check it isn't already checked.
		if strings.Contains(string(data), "- [x]") {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, anchor) && strings.Contains(line, "- [x]") {
					continue
				}
			}
		}

		matchID = id

		break
	}

	if matchID == "" {
		return false
	}

	anchor := "<!-- pid:" + matchID + " -->"

	var changed bool

	if err := m.mutatePlanLines(planPath, func(lines []string) []string {
		for i, line := range lines {
			if !strings.Contains(line, anchor) {
				continue
			}

			newLine := strings.Replace(line, "- [ ]", "- [x]", 1)

			newLine = strings.Replace(newLine, "- [~]", "- [x]", 1)
			newLine = strings.TrimRight(newLine, " ") + " <!-- id:" + diffID + " -->"
			lines[i] = newLine
			changed = true

			break
		}

		return lines
	}); err != nil || !changed {
		return false
	}

	return true
}

// updatePlanItemCheckbox rewrites the checkbox on an already-rendered
// ledger item when its status changes (e.g. pending → in_progress).
// The rewrite is in-place in the plan file, preserving all anchors and
// the item text. No-op when the item has not been rendered yet.
func (m *promptExecutionSession) updatePlanItemCheckbox(item workspace.TaskLedgerItem) {
	if m == nil {
		return
	}

	planPath := m.ensurePlanPath()
	if planPath == "" {
		return
	}

	anchor := "<!-- tid:" + item.ID + " -->"
	status, _ := taskledger.Parse(string(item.Status))

	if status == taskledger.StatusPending || status == "" {
		return
	}

	var targetBox string

	switch {
	case status.Finished():
		targetBox = "[x]"
	case status == taskledger.StatusInProgress:
		targetBox = "[~]"
	default:
		return
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		return
	}

	if !strings.Contains(string(data), anchor) {
		return
	}

	_ = m.mutatePlanLines(planPath, func(lines []string) []string {
		for i, line := range lines {
			if !strings.Contains(line, anchor) {
				continue
			}

			newLine := line
			for _, oldBox := range []string{"[ ]", "[~]", "[x]", "[X]"} {
				newLine = strings.Replace(newLine, oldBox, targetBox, 1)
			}

			if newLine != line {
				lines[i] = newLine
			}

			break
		}

		return lines
	})
}

// recordSubmittedPlan intercepts a successful submit_plan tool result
// and populates the plan panel with unchecked checklist items — one per
// edit in the submitted plan. Each line carries a synthetic "pid:"
// anchor so executor-phase writes can be matched back and checked off.
//
// On a second submit_plan (correction round), the existing "### Plan"
// section is replaced entirely with the latest artifact.
func (m *promptExecutionSession) recordSubmittedPlan(req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil {
		return
	}

	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return
	}

	if strings.TrimSpace(result.Tool) != "submit_plan" {
		return
	}

	if strings.TrimSpace(result.Error) != "" {
		return
	}

	if sessionlog.SessionIDFromPath(m.debugLogPath) == "" {
		return
	}

	var edits []planartifact.PlanEdit

	if req.PlanArtifact != nil {
		edits = req.PlanArtifact.Edits
	}

	if len(edits) == 0 {
		edits = req.Edits
	}

	if len(edits) == 0 {
		return
	}

	m.ensurePlanSessionHeading()

	if m.plan.submittedEdits == nil {
		m.plan.submittedEdits = map[string]planartifact.PlanEdit{}
	}
	for id := range m.plan.submittedEdits {
		if strings.HasPrefix(id, "pe-") {
			delete(m.plan.submittedEdits, id)
		}
	}

	path := m.ensurePlanPath()
	if path == "" {
		return
	}

	existing, _ := os.ReadFile(path)
	content := string(existing)

	var b strings.Builder

	rest, _ := cutPlanSection(content, "### Plan")
	rest, _ = cutPlanSection(rest, "### Approval")
	b.WriteString(rest)

	if !strings.HasSuffix(rest, "\n") {
		b.WriteString("\n")
	}

	b.WriteString("### Plan\n")

	for i, edit := range edits {
		peID := fmt.Sprintf("pe-%d", i)
		label := planEditDetailedLabel(edit)
		b.WriteString(fmt.Sprintf("- [ ] %s <!-- pid:%s -->\n", label, peID))

		m.plan.submittedEdits[peID] = edit
	}

	b.WriteString("\n### Approval\n")
	for _, row := range planApprovalRows(planSubmittedEditPathCount(edits)) {
		b.WriteString(row)
		b.WriteString("\n")
	}

	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	m.invalidatePlanCache()
}

// cutPlanSection splits content at the "### Plan" heading, returning
// everything before it (plus anything after the next ### heading or
// end-of-file). Returns (before, true) when the section exists.
func cutPlanSection(content, heading string) (string, bool) {
	targetLevel, targetTitle, ok := parsePlanHeading(heading)
	if !ok {
		return content, false
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	start := -1

	for i, line := range lines {
		level, title, ok := parsePlanHeading(line)
		if ok && level == targetLevel && strings.EqualFold(title, targetTitle) {
			start = i
			break
		}
	}

	if start < 0 {
		return content, false
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		level, _, ok := parsePlanHeading(lines[i])
		if ok && level <= targetLevel {
			end = i
			break
		}
	}

	kept := append([]string{}, lines[:start]...)
	kept = append(kept, lines[end:]...)

	return strings.Join(kept, "\n"), true
}

// planEditLabel builds a human-readable label for a PlanEdit, using its
// Purpose field with a path fallback.
func planEditLabel(edit planartifact.PlanEdit) string {
	desc := strings.TrimSpace(edit.Purpose)
	p := strings.TrimSpace(edit.Path)

	switch {
	case desc != "" && p != "":
		return fmt.Sprintf("%s (%s)", desc, p)
	case desc != "":
		return desc
	case p != "":
		return p
	default:
		return fmt.Sprintf("edit %d", 0)
	}
}

func planEditDetailedLabel(edit planartifact.PlanEdit) string {
	label := planEditLabel(edit)
	details := planEditDetails(edit)
	if details == "" {
		return label
	}

	return label + " - " + details
}

func planEditDetails(edit planartifact.PlanEdit) string {
	var parts []string

	switch {
	case edit.ReplaceAll:
		parts = append(parts, "whole-file replacement")
	case edit.StartLine > 0 && edit.EndLine > 0 && edit.StartLine != edit.EndLine:
		parts = append(parts, fmt.Sprintf("lines %d-%d", edit.StartLine, edit.EndLine))
	case edit.StartLine > 0:
		parts = append(parts, fmt.Sprintf("line %d", edit.StartLine))
	case edit.InsertAfterLine > 0:
		parts = append(parts, fmt.Sprintf("insert after line %d", edit.InsertAfterLine))
	case strings.TrimSpace(edit.OldText) != "":
		parts = append(parts, "anchored replacement")
	default:
		parts = append(parts, "file edit")
	}

	if chars, ok := planEditReplacementSize(edit); ok {
		parts = append(parts, fmt.Sprintf("%d replacement char(s)", chars))
	} else if strings.TrimSpace(edit.NewTextRef) != "" || strings.TrimSpace(edit.ContentRef) != "" {
		parts = append(parts, "replacement from artifact ref")
	}

	return strings.Join(parts, ", ")
}

func planEditReplacementSize(edit planartifact.PlanEdit) (int, bool) {
	switch {
	case planartifact.PlanEditHasExplicitReplacementText(edit):
		return len(edit.NewText), true
	case strings.TrimSpace(edit.Content) != "":
		return len(edit.Content), true
	default:
		return 0, false
	}
}

func planSubmittedEditPathCount(edits []planartifact.PlanEdit) int {
	seen := map[string]bool{}
	for _, edit := range edits {
		p := strings.TrimSpace(edit.Path)
		if p != "" {
			seen[p] = true
		}
	}

	return len(seen)
}
