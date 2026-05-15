package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/llama"
	"idiom/internal/workspace"
)

// planWriteSnapshot captures the pre-execute state of a single file so
// the post-execute callback can compute a diff. existed is tracked so
// create-new-file writes render as all-add hunks rather than a diff
// against an empty baseline that would be indistinguishable from a
// truly-empty file.
type planWriteSnapshot struct {
	Path    string
	Before  string
	Existed bool
	Request workspace.ToolRequest
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

// diffsPath returns the absolute path of the session's diff log, a
// JSONL file whose records carry the per-write unified diff alongside
// its plan-entry id. Mirrors failuresPath's layout under .planning/.
func (m *Model) diffsPath() string {
	id := m.sessionID()
	if id == "" {
		return ""
	}
	dir := planDirName
	if root := m.workspace.Root(); root != "" {
		dir = filepath.Join(root, planDirName)
	}
	return filepath.Join(dir, id+".diffs.jsonl")
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
func (m *Model) failuresPath() string {
	id := m.sessionID()
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
func (m *Model) recordWriteFailure(step int, req workspace.ToolRequest, result workspace.ToolResult) {
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
	if _, err := fmt.Fprintln(f, string(line)); err != nil {
		return
	}
}

// capturePlanSnapshot records the pre-write state of every file a
// write-class tool is about to mutate. Called from PreExecute after
// the permission decision resolves to Allow. Snapshots are keyed by
// step number so the matching ToolResult can pair them up.
//
// Non-write tools and read-only failures produce no snapshots. Errors
// reading a file (most commonly "does not exist yet" for a brand-new
// file) are captured as an empty baseline with Existed=false so the
// diff renders as pure additions.
func (m *Model) capturePlanSnapshot(step int, req workspace.ToolRequest) {
	if m == nil || m.workspace == nil {
		return
	}
	paths := rollbackPathsForRequest(req)
	if len(paths) == 0 {
		return
	}
	root := m.workspace.CWD()
	var snaps []planWriteSnapshot
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		snap := planWriteSnapshot{Path: p, Request: req}
		if data, err := os.ReadFile(abs); err == nil {
			snap.Before = string(data)
			snap.Existed = true
		} else if os.IsNotExist(err) {
			snap.Existed = false
		} else {
			// Any other read error (permissions, I/O) means we cannot
			// reliably diff this call; skip it rather than claim empty
			// baseline.
			continue
		}
		snaps = append(snaps, snap)
	}
	if len(snaps) == 0 {
		return
	}
	if m.planWriteSnapshots == nil {
		m.planWriteSnapshots = map[int][]planWriteSnapshot{}
	}
	m.planWriteSnapshots[step] = snaps
}

// recordWriteSuccess lands a successful write into the plan: for each
// snapshot from the matching PreExecute, it reads the new content from
// disk, computes a unified diff, appends a record to <id>.diffs.jsonl,
// and adds a checked-off line to the plan markdown. The snapshot map
// entry for the step is always cleared so a later failed write doesn't
// re-bind to stale baselines.
func (m *Model) recordWriteSuccess(step int, req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil || m.workspace == nil {
		return
	}
	snaps, ok := m.planWriteSnapshots[step]
	if ok {
		delete(m.planWriteSnapshots, step)
	}
	// Only land success cases; failures are captured by recordWriteFailure.
	if strings.TrimSpace(result.Error) != "" {
		return
	}
	if !workspace.IsWorkspaceWriteTool(req.Tool) {
		return
	}
	if m.sessionID() == "" {
		return
	}
	// Write the session heading once per live session.
	m.ensurePlanSessionHeading()

	// mkdir and go_move_declarations don't produce meaningful file-level
	// diffs; record the plan-entry without a diff payload so the user
	// still sees the action in the plan.
	if len(snaps) == 0 {
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
	root := m.workspace.CWD()
	for _, snap := range snaps {
		abs := snap.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		var after string
		if data, err := os.ReadFile(abs); err == nil {
			after = string(data)
		}
		diff := unifiedDiff(snap.Path, snap.Before, after)
		id := newPlanEntryID()
		m.appendPlanDiffEntry(planDiffEntry{
			ID:          id,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
			Step:        step,
			Tool:        req.Tool,
			Path:        snap.Path,
			Description: req.Description,
			Summary:     result.Summary,
			Diff:        diff,
		})
		if !m.attachWriteToPlannedItem(req, snap.Path, id) &&
			!m.attachWriteToSubmittedEdit(snap.Path, id) {
			m.appendUnplannedCheckedLine(planLineLabel(req, snap.Path), id)
		}
	}
}

// appendUnplannedCheckedLine writes a `- [x] ... <!-- id:... -->` line
// under a `### Unplanned` subsection. The subsection header is added
// once per session — subsequent unplanned writes append to it. Using a
// dedicated subsection keeps ledger-covered work visually separated
// from drive-by edits, so a reviewer can see at a glance which writes
// exceeded the triage plan.
func (m *Model) appendUnplannedCheckedLine(label, id string) {
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
		return writeToolIntentSynth(req.Tool, path)
	}
	if path != "" {
		return fmt.Sprintf("%s %s", req.Tool, path)
	}
	return req.Tool
}

// writeToolIntentSynth produces a human-readable intent string for a
// write tool when the model didn't supply a Description. Mirrors the
// patterns from toolRequestIntent but is self-contained so plan_autodoc
// doesn't depend on lifecycle.
func writeToolIntentSynth(tool, path string) string {
	tool = strings.TrimSpace(tool)
	path = strings.TrimSpace(path)
	switch tool {
	case "edit_file":
		if path == "" {
			return "apply the planned code change"
		}
		return "apply the planned change in " + path
	case "write_file":
		if path == "" {
			return "create or replace a file"
		}
		return "create or replace " + path
	case "append_file":
		if path == "" {
			return "append content to a file"
		}
		return "append content to " + path
	case "apply_patch":
		if path == "" {
			return "apply a patch"
		}
		return "apply a patch to " + path
	case "mkdir":
		if path == "" {
			return "create a directory"
		}
		return "create directory " + path
	case "go_move_declarations":
		if path == "" {
			return "move declarations"
		}
		return "move declarations in " + path
	default:
		if path != "" {
			return tool + " " + path
		}
		return tool
	}
}

// ensurePlanSessionHeading appends a `## Session <id>` heading to the
// plan file the first time the autodoc lands a record in this live
// session. If the plan file is empty or missing the heading also
// includes a top-level title so fresh sessions get a usable document.
func (m *Model) ensurePlanSessionHeading() {
	if m == nil || m.planSessionHeadingWritten {
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
	heading := fmt.Sprintf("## Session %s — %s\n\n", m.sessionID(), time.Now().UTC().Format("2006-01-02 15:04"))
	if strings.Contains(string(existing), "## Session "+m.sessionID()) {
		m.planSessionHeadingWritten = true
		return
	}
	var prelude string
	if len(existing) == 0 {
		prelude = fmt.Sprintf("# Plan — %s\n\n", m.sessionID())
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
	m.planSessionHeadingWritten = true
}

// appendPlanDiffEntry writes one JSONL record to <id>.diffs.jsonl.
// Silently drops on any I/O error — a failed diff-log write must not
// break the agent loop, the same invariant recordWriteFailure honors.
func (m *Model) appendPlanDiffEntry(entry planDiffEntry) {
	path := m.diffsPath()
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
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ts%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
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
func (m *Model) recordTaskLedger(req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil {
		return
	}
	if strings.TrimSpace(req.Tool) != "task_ledger" {
		return
	}
	if strings.TrimSpace(result.Error) != "" {
		return
	}
	if m.sessionID() == "" {
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

	if m.planLedgerItems == nil {
		m.planLedgerItems = map[string]workspace.TaskLedgerItem{}
	}
	if m.planLedgerRenderedIDs == nil {
		m.planLedgerRenderedIDs = map[string]bool{}
	}

	renderedSnapshot := m.renderedLedgerSnapshot()

	var newItems []workspace.TaskLedgerItem
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		m.planLedgerItems[item.ID] = item
		if !m.planLedgerRenderedIDs[item.ID] {
			if m.findDuplicateLedgerItemID(item.Content, renderedSnapshot) != "" {
				m.planLedgerRenderedIDs[item.ID] = true
				continue
			}
			newItems = append(newItems, item)
			m.planLedgerRenderedIDs[item.ID] = true
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
func (m *Model) appendPlannedItems(items []workspace.TaskLedgerItem) {
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
func (m *Model) renderedLedgerSnapshot() map[string]workspace.TaskLedgerItem {
	if len(m.planLedgerRenderedIDs) == 0 {
		return nil
	}
	out := make(map[string]workspace.TaskLedgerItem, len(m.planLedgerRenderedIDs))
	for id := range m.planLedgerRenderedIDs {
		if item, ok := m.planLedgerItems[id]; ok {
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
func (m *Model) findDuplicateLedgerItemID(newContent string, rendered map[string]workspace.TaskLedgerItem) string {
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
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "completed", "cancelled":
		box = "[x]"
	case "in_progress":
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
func (m *Model) attachWriteToPlannedItem(req workspace.ToolRequest, path, diffID string) bool {
	if m == nil || len(m.planLedgerItems) == 0 {
		return false
	}
	planPath := m.ensurePlanPath()
	if planPath == "" {
		return false
	}
	// Iterate in sorted id order so that when multiple items are
	// candidates (e.g. share a path, or multiple paths-less items
	// exist for the heuristic) the choice is deterministic.
	ids := make([]string, 0, len(m.planLedgerItems))
	for id := range m.planLedgerItems {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	pending := func(item workspace.TaskLedgerItem) bool {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		return status != "completed" && status != "cancelled"
	}

	var (
		matchID   string
		matchItem workspace.TaskLedgerItem
	)

	// 1) Explicit task_id on the write request wins outright.
	if tid := strings.TrimSpace(req.TaskID); tid != "" {
		if item, ok := m.planLedgerItems[tid]; ok && pending(item) {
			matchID = tid
			matchItem = item
		}
	}

	// 2) Path match against a pending item's Paths.
	if matchID == "" {
		for _, id := range ids {
			item := m.planLedgerItems[id]
			if !pending(item) {
				continue
			}
			hits := m.planLedgerHits[id]
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
			item := m.planLedgerItems[id]
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
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false
	}
	anchor := "<!-- tid:" + matchID + " -->"
	if !strings.Contains(string(data), anchor) {
		return false
	}
	// Record the hit before we decide whether the item is fully
	// fulfilled — the current write counts toward completion.
	if m.planLedgerHits == nil {
		m.planLedgerHits = map[string]map[string]bool{}
	}
	if m.planLedgerHits[matchID] == nil {
		m.planLedgerHits[matchID] = map[string]bool{}
	}
	m.planLedgerHits[matchID][path] = true

	// Only flip to [x] once every listed path for the item has been
	// written. Multi-path items stay [ ] until the final path lands.
	allHit := true
	for _, p := range matchItem.Paths {
		if !m.planLedgerHits[matchID][strings.TrimSpace(p)] {
			allHit = false
			break
		}
	}

	// Rewrite the matching line: optionally flip [ ] → [x], and append
	// this write's diff id. Multiple writes append multiple id anchors
	// rather than replacing, so no attached diff is lost.
	lines := strings.Split(string(data), "\n")
	var changed bool
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
	if !changed {
		return false
	}
	if err := os.WriteFile(planPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false
	}
	if allHit {
		item := m.planLedgerItems[matchID]
		item.Status = "completed"
		m.planLedgerItems[matchID] = item
	}
	return true
}

// attachWriteToSubmittedEdit matches a successful write against a
// submit_plan checklist entry by path, flipping [ ] → [x] and
// appending the diff anchor. Returns true on a successful match.
func (m *Model) attachWriteToSubmittedEdit(path, diffID string) bool {
	if m == nil || len(m.planSubmittedEdits) == 0 {
		return false
	}
	planPath := m.ensurePlanPath()
	if planPath == "" {
		return false
	}

	ids := make([]string, 0, len(m.planSubmittedEdits))
	for id := range m.planSubmittedEdits {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	var matchID string
	for _, id := range ids {
		edit := m.planSubmittedEdits[id]
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
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false
	}
	lines := strings.Split(string(data), "\n")
	var changed bool
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
	if !changed {
		return false
	}
	if err := os.WriteFile(planPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false
	}
	return true
}

// updatePlanItemCheckbox rewrites the checkbox on an already-rendered
// ledger item when its status changes (e.g. pending → in_progress).
// The rewrite is in-place in the plan file, preserving all anchors and
// the item text. No-op when the item has not been rendered yet.
func (m *Model) updatePlanItemCheckbox(item workspace.TaskLedgerItem) {
	if m == nil {
		return
	}
	planPath := m.ensurePlanPath()
	if planPath == "" {
		return
	}
	anchor := "<!-- tid:" + item.ID + " -->"
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == "pending" || status == "" {
		return
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		return
	}
	if !strings.Contains(string(data), anchor) {
		return
	}
	var targetBox string
	switch status {
	case "completed", "cancelled":
		targetBox = "[x]"
	case "in_progress":
		targetBox = "[~]"
	default:
		return
	}
	lines := strings.Split(string(data), "\n")
	var changed bool
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
			changed = true
		}
		break
	}
	if changed {
		_ = os.WriteFile(planPath, []byte(strings.Join(lines, "\n")), 0o644)
	}
}

// recordSubmittedPlan intercepts a successful submit_plan tool result
// and populates the plan panel with unchecked checklist items — one per
// edit in the submitted plan. Each line carries a synthetic "pid:"
// anchor so executor-phase writes can be matched back and checked off.
//
// On a second submit_plan (correction round), the existing "### Plan"
// section is replaced entirely with the latest artifact.
func (m *Model) recordSubmittedPlan(req workspace.ToolRequest, result workspace.ToolResult) {
	if m == nil {
		return
	}
	if strings.TrimSpace(req.Tool) != "submit_plan" {
		return
	}
	if strings.TrimSpace(result.Error) != "" {
		return
	}
	if m.sessionID() == "" {
		return
	}
	var edits []workspace.PlanEdit
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

	if m.planSubmittedEdits == nil {
		m.planSubmittedEdits = map[string]workspace.PlanEdit{}
	}

	path := m.ensurePlanPath()
	if path == "" {
		return
	}

	existing, _ := os.ReadFile(path)
	content := string(existing)

	var b strings.Builder
	rest, hasSection := cutPlanSection(content, "### Plan")
	if hasSection {
		// Drop the old ### Plan section; we'll rewrite it.
	}
	b.WriteString(rest)
	if !strings.HasSuffix(rest, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("### Plan\n")
	for i, edit := range edits {
		peID := fmt.Sprintf("pe-%d", i)
		label := planEditLabel(edit)
		b.WriteString(fmt.Sprintf("- [ ] %s <!-- pid:%s -->\n", label, peID))
		m.planSubmittedEdits[peID] = edit
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// cutPlanSection splits content at the "### Plan" heading, returning
// everything before it (plus anything after the next ### heading or
// end-of-file). Returns (before, true) when the section exists.
func cutPlanSection(content, heading string) (string, bool) {
	idx := strings.Index(content, heading+"\n")
	if idx < 0 {
		return content, false
	}
	before := content[:idx]
	after := content[idx:]
	nextHeading := strings.Index(after[len(heading)+1:], "\n### ")
	if nextHeading >= 0 {
		cut := nextHeading + len(heading) + 1
		return before + after[cut+1:], true
	}
	return before, true
}

// planEditLabel builds a human-readable label for a PlanEdit, using its
// Purpose field with a path fallback.
func planEditLabel(edit workspace.PlanEdit) string {
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
