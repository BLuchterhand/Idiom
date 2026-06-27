package app

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/evidence/transcript"
	"idiom/internal/platform/jsonl"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/sessionlog"
	"idiom/internal/workflows/featureplanner"
)

// resumeLinePathsRe captures the optional `(paths: a.go, b.go)` note the
// autodoc writes into each ledger-item line. Used during rehydration to
// recover the per-item paths the writer recorded, so the hit tracker
// can match subsequent writes to the right item.
var resumeLinePathsRe = regexp.MustCompile(`\(paths:\s*([^)]+)\)`)
var featurePlannerStatePathRe = regexp.MustCompile(`\.idiom/feature-planner/([^/\s"']+)/state\.json`)

// ResumeSession swaps the model into a prior session identified by id:
// the debug logger is re-pointed at that session's JSONL (append mode),
// chat history is replayed, and plan-panel state is rehydrated from the
// session's `.planning/<id>.md` plan + `.planning/<id>.diffs.jsonl`
// diff log. Safe to call at any time; if resume fails, prior state is
// preserved.
//
// Callers: --resume CLI flag plumbing in cmd/idiom and the /resume
// slash command. The TUI ResumeSessionCmd wrapper re-renders panels.
func (m *Model) ResumeSession(id string) error {
	if m == nil {
		return fmt.Errorf("nil model")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("session id is required")
	}

	path := sessionlog.ResolveSessionLogPath(m.cfg, id)
	if path == "" {
		return fmt.Errorf("could not resolve session log path for %q", id)
	}

	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("session log %q: %w", path, err)
	}

	// Replay messages and history-panel metadata first so a parse failure
	// aborts cleanly before we touch the logger.
	replayed, err := replaySessionLog(path)
	if err != nil {
		return err
	}

	if len(replayed.messages) == 0 {
		return fmt.Errorf("session log %q contained no user/assistant messages", path)
	}

	inputHistory, err := inputHistoryFromSessionLog(path, maxInputHistoryEntries)
	if err != nil {
		return fmt.Errorf("restore input history: %w", err)
	}

	// Swap the debug logger to append into the resumed file. Close the
	// previous one first so we don't leak a file handle on the empty
	// new-session log that initDebugLogger just created.
	newLogger, err := transcript.NewDebugLogger(transcript.DebugLoggerOptions{
		Dir:           m.cfg.Chat.DebugLogDir,
		Mode:          transcript.DebugLogMode(textutil.FirstNonEmpty(m.cfg.Chat.DebugLogMode, "redacted")),
		WorkspaceRoot: m.workspace.Root(),
		ResumePath:    path,
	})
	if err != nil {
		return fmt.Errorf("open resumed log: %w", err)
	}

	if m.debugLogger != nil {
		prevPath := m.debugLogger.Path()

		_ = m.debugLogger.Close()
		// If the previous log was a brand-new startup file with no
		// activity yet (typical resume-at-boot), remove it so the
		// conversations dir isn't littered with empties.
		if prevPath != "" && prevPath != path {
			if info, statErr := os.Stat(prevPath); statErr == nil && info.Size() < 512 {
				_ = os.Remove(prevPath)
			}
		}
	}

	m.debugLogger = newLogger
	m.debugLogPath = newLogger.Path()

	// Reset plan caches so ensurePlanPath re-resolves against the new
	// session id and the rehydrator can build fresh maps.
	m.planPath = ""
	m.plan.ledgerItems = map[string]workspace.TaskLedgerItem{}
	m.plan.ledgerRenderedIDs = map[string]bool{}
	m.plan.ledgerHits = map[string]map[string]bool{}
	m.plan.sessionHeadingWritten = false
	m.plan.cursor = 0
	m.plan.overlayID = ""
	m.plan.overlayBody = ""

	// Replace chat history with the replayed messages. We replace
	// wholesale rather than append — on /resume mid-session, leftover
	// messages from the current session would confuse the model.
	m.messages = replayed.messages
	m.inputHistory = inputHistory
	m.resetInputHistoryNavigation()
	m.history.entries = replayed.history

	if len(m.history.entries) > 0 {
		m.history.selected = len(m.history.entries) - 1
	} else {
		m.history.selected = 0
	}

	m.currentTurnHistoryActive = false
	m.currentTurnHistoryIndex = 0
	m.currentTurnTools = nil
	m.directFeaturePlannerSessionID = ""
	m.restoreDirectFeaturePlannerSessionFromSessionLog(path)
	m.renderMessages(false)
	m.requestHistoryPanelRender(true, true)

	rehydratePlanState(m)
	m.renderPlanPanelContent()

	return nil
}

func (m *Model) restoreDirectFeaturePlannerSessionFromSessionLog(path string) bool {
	if m == nil {
		return false
	}

	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		return false
	}

	ids := featurePlannerSessionIDsFromSessionLog(path)
	for i := len(ids) - 1; i >= 0; i-- {
		state, _, err := featureplanner.LoadState(root, ids[i])
		if err != nil {
			m.directFeaturePlannerSessionID = ""
			return false
		}

		if featurePlannerStateAcceptsDirectOperatorInput(state) {
			m.directFeaturePlannerSessionID = strings.TrimSpace(state.SessionID)
			if effortRoot := strings.TrimSpace(state.EffortRoot); effortRoot != "" {
				m.featurePlannerEffortRoot = m.resolveFeaturePlannerEffortRoot(effortRoot)
			}

			return true
		}

		m.directFeaturePlannerSessionID = ""

		return false
	}

	return false
}

func featurePlannerSessionIDsFromSessionLog(path string) []string {
	var ids []string
	seen := map[string]bool{}

	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}

		if seen[id] {
			for i, existing := range ids {
				if existing == id {
					ids = append(ids[:i], ids[i+1:]...)
					break
				}
			}
		}
		seen[id] = true
		ids = append(ids, id)
	}

	_ = jsonl.ScanFile(path, func(raw json.RawMessage) error {
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil
		}

		role := sessionStringField(record, "role")
		content := sessionStringField(record, "content")
		if content == "" {
			return nil
		}

		featurePlannerRole := strings.Contains(role, "feature_planner")

		for _, match := range featurePlannerStatePathRe.FindAllStringSubmatchIndex(content, -1) {
			if !featurePlannerStatePathMatchIsRelative(content, match[0]) {
				continue
			}
			if len(match) > 3 {
				appendID(content[match[2]:match[3]])
			}
		}

		if featurePlannerRole {
			var event struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(content), &event); err == nil {
				appendID(event.SessionID)
			}
		}

		return nil
	})

	return ids
}

func featurePlannerStatePathMatchIsRelative(content string, start int) bool {
	if start <= 0 || start > len(content) {
		return true
	}

	switch content[start-1] {
	case '/', '\\':
		return false
	default:
		return true
	}
}

// replaySessionMessages reads one JSONL log and returns the
// user/assistant messages in order. Mirrors LoadHistoryFromSession but
// returns the slice instead of appending so ResumeSession can swap
// history atomically.
func replaySessionMessages(path string) ([]llama.ChatMessage, error) {
	var out []llama.ChatMessage

	err := jsonl.ScanFile(path, func(raw json.RawMessage) error {
		var rec map[string]any

		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil
		}

		if rec["type"] != "message" {
			return nil
		}

		role, _ := rec["role"].(string)
		content, _ := rec["content"].(string)

		if role != "user" && role != "assistant" {
			return nil
		}

		if strings.TrimSpace(content) == "" {
			return nil
		}

		out = append(out, llama.ChatMessage{Role: role, Content: content})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read session log: %w", err)
	}

	return out, nil
}

// rehydratePlanState reads the session's plan markdown and diffs log
// (if they exist) and repopulates the in-memory ledger bookkeeping:
// planLedgerItems, planLedgerRenderedIDs, planLedgerHits, and
// planSessionHeadingWritten. Without this, a resumed session would
// duplicate every prior ledger item on the next task_ledger call and
// would route attached writes to Unplanned.
//
// Not a method on Model only so tests can drive it with a minimally
// constructed Model.
func rehydratePlanState(m *Model) {
	if m == nil {
		return
	}

	// Defensive init so callers can wipe maps to nil to simulate a
	// fresh process and still exercise the rehydrate path.
	if m.plan.ledgerItems == nil {
		m.plan.ledgerItems = map[string]workspace.TaskLedgerItem{}
	}

	if m.plan.ledgerRenderedIDs == nil {
		m.plan.ledgerRenderedIDs = map[string]bool{}
	}

	if m.plan.ledgerHits == nil {
		m.plan.ledgerHits = map[string]map[string]bool{}
	}

	planPath := m.ensurePlanPath()
	if planPath == "" {
		return
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		// Nothing to rehydrate — a brand-new session id or a session
		// that never triggered the autodoc. Leave the maps empty.
		return
	}

	plan := string(data)

	// Session heading — if the resumed session's own heading already
	// exists, suppress re-emission when writes resume.
	if strings.Contains(plan, "## Session "+m.sessionID()) {
		m.plan.sessionHeadingWritten = true
	}

	// Walk each plan line. We look for lines carrying a tid anchor
	// (ledger items) and optionally id anchors (attached writes).
	// Parse the paths annotation so hit tracking can be reconstructed.
	type lineMeta struct {
		TID     string
		DiffIDs []string
		Content string
		Paths   []string
		Checked bool
	}

	var lines []lineMeta

	for _, raw := range strings.Split(plan, "\n") {
		trimmed := strings.TrimLeft(raw, " \t")
		if !strings.HasPrefix(trimmed, "- [") && !strings.HasPrefix(trimmed, "* [") {
			continue
		}

		if len(trimmed) < 5 {
			continue
		}

		tid := ""
		if m := planTaskAnchorRe.FindStringSubmatch(raw); len(m) == 2 {
			tid = m[1]
		}

		if tid == "" {
			continue
		}

		var diffIDs []string

		for _, sub := range planDiffAnchorRe.FindAllStringSubmatch(raw, -1) {
			if len(sub) == 2 {
				diffIDs = append(diffIDs, sub[1])
			}
		}

		box := trimmed[3]
		checked := box == 'x' || box == 'X'

		// Extract `(paths: a, b)` if present.
		var paths []string

		if match := resumeLinePathsRe.FindStringSubmatch(raw); len(match) == 2 {
			for _, p := range strings.Split(match[1], ",") {
				if v := strings.TrimSpace(p); v != "" {
					paths = append(paths, v)
				}
			}
		}

		// The content is everything between the checkbox and the
		// paths / tid anchor. Approximate it: strip anchors + paths
		// note from the trimmed line after the checkbox.
		content := trimmed[5:]

		content = planCommentRe.ReplaceAllString(content, "")
		content = resumeLinePathsRe.ReplaceAllString(content, "")
		content = strings.TrimSpace(content)

		lines = append(lines, lineMeta{
			TID:     tid,
			DiffIDs: diffIDs,
			Content: content,
			Paths:   paths,
			Checked: checked,
		})
	}

	// Resolve diff ids → paths by reading diffs.jsonl so we know which
	// path each attached diff filled. Falling back to an empty map is
	// fine: an item stays "partially hit" and the user can see it's
	// unchecked.
	diffPaths := loadDiffPathIndex(m.diffsPath())

	for _, lm := range lines {
		status := "pending"
		if lm.Checked {
			status = "completed"
		}

		m.plan.ledgerItems[lm.TID] = workspace.TaskLedgerItem{
			ID:      lm.TID,
			Content: lm.Content,
			Status:  status,
			Paths:   lm.Paths,
		}
		m.plan.ledgerRenderedIDs[lm.TID] = true

		if m.plan.ledgerHits[lm.TID] == nil {
			m.plan.ledgerHits[lm.TID] = map[string]bool{}
		}

		for _, did := range lm.DiffIDs {
			if p, ok := diffPaths[did]; ok && p != "" {
				m.plan.ledgerHits[lm.TID][p] = true
			}
		}

		// If the item rendered checked but we don't know any hit
		// paths (e.g. mkdir), mark every declared path as hit so
		// state matches what the box claims.
		if lm.Checked && len(lm.Paths) > 0 && len(lm.DiffIDs) == 0 {
			for _, p := range lm.Paths {
				m.plan.ledgerHits[lm.TID][p] = true
			}
		}
	}
}

// loadDiffPathIndex reads `.planning/<id>.diffs.jsonl` and returns a
// diff-id → path map. Used only during rehydration; errors are silent
// because a missing/corrupt diffs file should degrade gracefully
// rather than fail resume outright.
func loadDiffPathIndex(path string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(path) == "" {
		return out
	}

	_ = jsonl.ScanFile(path, func(raw json.RawMessage) error {
		var rec planDiffEntry

		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil
		}

		if rec.ID != "" {
			out[rec.ID] = rec.Path
		}

		return nil
	})

	return out
}

// AvailableSessionIDs returns the most recent session ids under the
// debug-log directory, newest first. Exposed so the TUI `/resume`
// handler can surface a short list when the user doesn't pass an id.
func (m *Model) AvailableSessionIDs(limit int) []string {
	sessions, err := sessionlog.ListRecordedSessions(m.cfg)
	if err != nil {
		return nil
	}

	if limit <= 0 || limit > len(sessions) {
		limit = len(sessions)
	}

	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, sessions[i].ID)
	}

	return out
}
