package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
	"idiom/internal/repository/discovery"
)

const (
	harnessAuditDefaultLogLimit = 80
	harnessAuditMaxLogLimit     = 5000
)

var harnessAuditLifecycleCloseoutIntroducedAt = time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

type harnessAuditLaneHistory struct {
	Path            string                       `json:"path"`
	Exists          bool                         `json:"exists"`
	Runs            int                          `json:"runs"`
	ByMode          map[string]int               `json:"by_mode,omitempty"`
	ByFinalState    map[string]int               `json:"by_final_state,omitempty"`
	ByExitCode      map[string]int               `json:"by_exit_code,omitempty"`
	TotalModelCalls int                          `json:"total_model_calls,omitempty"`
	TotalToolCalls  int                          `json:"total_tool_calls,omitempty"`
	TotalTokens     int                          `json:"total_tokens,omitempty"`
	TotalDurationMS int                          `json:"total_duration_ms,omitempty"`
	Recent          []harnessAuditLaneHistoryRun `json:"recent,omitempty"`
	Warnings        []string                     `json:"warnings,omitempty"`
}

type harnessAuditLaneHistoryRun struct {
	Timestamp    string   `json:"ts,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Brief        string   `json:"brief,omitempty"`
	ExitCode     int      `json:"exit_code"`
	FinalState   string   `json:"final_state,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	QueuePool    string   `json:"queue_pool,omitempty"`
	ModelCalls   int      `json:"model_calls,omitempty"`
	ToolCalls    int      `json:"tool_calls,omitempty"`
	TotalTokens  int      `json:"total_tokens,omitempty"`
	DurationMS   int      `json:"duration_ms,omitempty"`
	StopReason   string   `json:"stop_reason,omitempty"`
	Degradations []string `json:"degradations,omitempty"`
}

type harnessAuditLogSource struct {
	Pattern  string `json:"pattern"`
	Matched  int    `json:"matched"`
	Selected int    `json:"selected"`
	Capped   bool   `json:"capped,omitempty"`
}

type harnessAuditInProgressSession struct {
	TelemetryLog    string `json:"telemetry_log,omitempty"`
	ConversationLog string `json:"conversation_log,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Command         string `json:"command,omitempty"`
	PID             int    `json:"pid,omitempty"`
}

type harnessAuditIncompleteSession struct {
	TelemetryLog    string   `json:"telemetry_log,omitempty"`
	ConversationLog string   `json:"conversation_log,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	Command         string   `json:"command,omitempty"`
	Missing         []string `json:"missing"`
}

func buildHarnessAuditLaneHistory(root string, recentLimit int) *harnessAuditLaneHistory {
	if strings.TrimSpace(root) == "" {
		root = "."
	}

	out := &harnessAuditLaneHistory{
		Path:         ".idiom/lane-history.jsonl",
		ByMode:       map[string]int{},
		ByFinalState: map[string]int{},
		ByExitCode:   map[string]int{},
	}
	path := filepath.Join(root, ".idiom", "lane-history.jsonl")

	body, err := safepath.ReadFileUnderRoot(root, path, "lane history", safepath.Options{})
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			out.Warnings = append(out.Warnings, fmt.Sprintf("read lane history: %v", err))
		}

		return out
	}

	out.Exists = true

	var recent []harnessAuditLaneHistoryRun

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var run harnessAuditLaneHistoryRun

		if err := json.Unmarshal([]byte(line), &run); err != nil {
			out.Warnings = append(out.Warnings, "skipped malformed lane-history row")
			continue
		}

		out.Runs++
		if run.Mode != "" {
			out.ByMode[run.Mode]++
		}

		if run.FinalState != "" {
			out.ByFinalState[run.FinalState]++
		}

		out.ByExitCode[fmt.Sprintf("%d", run.ExitCode)]++

		out.TotalModelCalls += run.ModelCalls
		out.TotalToolCalls += run.ToolCalls
		out.TotalTokens += run.TotalTokens
		out.TotalDurationMS += run.DurationMS

		run.Brief = harnessAuditDisplayPath(root, run.Brief)
		recent = append(recent, run)
	}

	if recentLimit <= 0 {
		recentLimit = 8
	}

	if len(recent) > recentLimit {
		recent = recent[len(recent)-recentLimit:]
	}

	out.Recent = recent

	return out
}

func harnessAuditDisplayPath(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	if !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}

	if rel, ok := harnessAuditRelativePath(root, path); ok {
		return rel
	}

	realRoot, rootErr := safepath.CanonicalExistingPath(root)
	realPath, pathErr := safepath.CanonicalExistingPath(path)

	if rootErr == nil && pathErr == nil {
		if rel, ok := harnessAuditRelativePath(realRoot, realPath); ok {
			return rel
		}
	}

	return filepath.ToSlash(path)
}

func harnessAuditRelativePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

type harnessAuditLogFacts struct {
	Command                 string
	WorkspaceRoot           string
	SessionID               string
	DebugLogPath            string
	PID                     int
	HasProcessStart         bool
	HasProcessExit          bool
	HasModelSession         bool
	HasModelSessionCloseout bool
	HasSessionSummary       bool
	IsImplementConversation bool
}

func (m *Manager) auditIncompleteImplementSessions(paths []string) ([]harnessAuditIncompleteSession, []harnessAuditInProgressSession, []string) {
	var (
		incomplete []harnessAuditIncompleteSession
		inProgress []harnessAuditInProgressSession
		warnings   []string
	)

	type auditedLog struct {
		path        string
		displayPath string
		facts       harnessAuditLogFacts
	}

	logs := make([]auditedLog, 0, len(paths))
	activeConversationLogs := make(map[string]harnessAuditInProgressSession)
	telemetryByConversationLog := make(map[string]auditedLog)

	for _, path := range paths {
		facts, err := readHarnessAuditLogFacts(m.cfg.Root, path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", m.pathAccess.DisplayPath(path), err))
			continue
		}

		displayPath := m.pathAccess.DisplayPath(path)
		log := auditedLog{path: path, displayPath: displayPath, facts: facts}

		logs = append(logs, log)

		conversationPath := ""

		if facts.HasModelSession {
			conversationPath = m.auditConversationPathFromTelemetry(facts)
			if conversationPath != "" {
				if _, ok := telemetryByConversationLog[filepath.Clean(conversationPath)]; !ok {
					telemetryByConversationLog[filepath.Clean(conversationPath)] = log
				}

				if displayConversation := m.pathAccess.DisplayPath(conversationPath); displayConversation != "" {
					if _, ok := telemetryByConversationLog[displayConversation]; !ok {
						telemetryByConversationLog[displayConversation] = log
					}
				}
			}
		}

		if facts.HasProcessStart && facts.HasModelSession && !facts.HasProcessExit && processIDAppearsRunning(facts.PID) {
			if conversationPath != "" {
				session := harnessAuditInProgressSession{
					TelemetryLog:    displayPath,
					ConversationLog: m.pathAccess.DisplayPath(conversationPath),
					SessionID:       strings.TrimSpace(facts.SessionID),
					Command:         strings.TrimSpace(facts.Command),
					PID:             facts.PID,
				}

				activeConversationLogs[filepath.Clean(conversationPath)] = session
				activeConversationLogs[session.ConversationLog] = session
			}
		}
	}

	seenIncomplete := make(map[string]struct{})
	seenInProgress := make(map[string]struct{})
	appendInProgress := func(session harnessAuditInProgressSession) {
		key := session.TelemetryLog + "|" + session.ConversationLog
		if _, ok := seenInProgress[key]; ok {
			return
		}

		seenInProgress[key] = struct{}{}

		inProgress = append(inProgress, session)
	}
	lookupTelemetryForConversation := func(conversationPath string, conversationFacts harnessAuditLogFacts) (auditedLog, bool) {
		prefix := harnessAuditSessionTimestampPrefix(conversationFacts.SessionID)
		if prefix == "" {
			prefix = harnessAuditSessionTimestampPrefix(strings.TrimSuffix(filepath.Base(conversationPath), filepath.Ext(conversationPath)))
		}

		if prefix == "" {
			return auditedLog{}, false
		}

		prefixes := []string{prefix}
		if len(prefix) > len("20060102-1504") {
			prefixes = append(prefixes, prefix[:len("20060102-1504")])
		}

		prefixes = textutil.UniqueNonEmpty(prefixes)

		displayConversationPath := m.pathAccess.DisplayPath(conversationPath)

		var candidates []string

		for _, base := range textutil.UniqueNonEmpty([]string{m.cfg.CWD, m.cfg.Root}) {
			for _, dir := range []string{
				filepath.Join(base, "logs", "telemetry"),
				filepath.Join(base, ".idiom", "logs", "telemetry"),
			} {
				for _, candidatePrefix := range prefixes {
					matches, err := filepath.Glob(filepath.Join(dir, candidatePrefix+"*.jsonl"))
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: telemetry lookup failed: %v", displayConversationPath, err))
						continue
					}

					candidates = append(candidates, matches...)
				}
			}
		}

		candidates = textutil.UniqueNonEmpty(candidates)
		sortAuditLogPaths(candidates)

		cleanConversationPath := filepath.Clean(conversationPath)

		for _, candidate := range candidates {
			candidateFacts, err := readHarnessAuditLogFacts(m.cfg.Root, candidate)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", m.pathAccess.DisplayPath(candidate), err))
				continue
			}

			if !candidateFacts.HasModelSession {
				continue
			}

			candidateConversationPath := m.auditConversationPathFromTelemetry(candidateFacts)
			sameConversation := candidateConversationPath != "" &&
				(filepath.Clean(candidateConversationPath) == cleanConversationPath ||
					m.pathAccess.DisplayPath(candidateConversationPath) == displayConversationPath)
			sameSession := strings.TrimSpace(conversationFacts.SessionID) != "" &&
				strings.TrimSpace(conversationFacts.SessionID) == strings.TrimSpace(candidateFacts.SessionID)

			if !sameConversation && !sameSession {
				continue
			}

			return auditedLog{path: candidate, displayPath: m.pathAccess.DisplayPath(candidate), facts: candidateFacts}, true
		}

		return auditedLog{}, false
	}

	for _, log := range logs {
		facts := log.facts
		displayPath := log.displayPath

		if facts.HasProcessStart && textutil.EqualTrimFold(facts.Command, "implement") {
			if !facts.HasProcessExit && processIDAppearsRunning(facts.PID) {
				session := harnessAuditInProgressSession{
					TelemetryLog: displayPath,
					SessionID:    strings.TrimSpace(facts.SessionID),
					Command:      "implement",
					PID:          facts.PID,
				}
				if facts.HasModelSession {
					if conversationPath := m.auditConversationPathFromTelemetry(facts); conversationPath != "" {
						session.ConversationLog = m.pathAccess.DisplayPath(conversationPath)
					}
				}

				appendInProgress(session)

				continue
			}

			session := harnessAuditIncompleteSession{
				TelemetryLog: displayPath,
				SessionID:    strings.TrimSpace(facts.SessionID),
				Command:      "implement",
			}
			if !facts.HasProcessExit {
				session.Missing = append(session.Missing, "process_exit")
			}

			if facts.HasModelSession {
				if conversationPath := m.auditConversationPathFromTelemetry(facts); conversationPath != "" {
					session.ConversationLog = m.pathAccess.DisplayPath(conversationPath)

					conversationFacts, err := readHarnessAuditLogFacts(m.cfg.Root, conversationPath)
					if err != nil {
						warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: %v", session.ConversationLog, err))
					} else if !conversationFacts.HasSessionSummary && !facts.HasModelSessionCloseout {
						session.Missing = append(session.Missing, "session_summary")
					}
				} else if strings.TrimSpace(facts.DebugLogPath) != "" {
					warnings = append(warnings, fmt.Sprintf("%s lifecycle audit: debug log path %q is outside the audited workspace root or does not exist", displayPath, facts.DebugLogPath))
				} else if !facts.HasModelSessionCloseout {
					session.Missing = append(session.Missing, "session_summary")
				}
			}

			if len(session.Missing) > 0 {
				key := session.TelemetryLog + "|" + session.ConversationLog
				if _, ok := seenIncomplete[key]; !ok {
					seenIncomplete[key] = struct{}{}

					incomplete = append(incomplete, session)
				}
			}

			continue
		}

		if facts.IsImplementConversation && !facts.HasSessionSummary {
			activeSession, ok := activeConversationLogs[filepath.Clean(log.path)]
			if !ok {
				activeSession, ok = activeConversationLogs[displayPath]
			}

			if ok {
				if strings.TrimSpace(activeSession.Command) == "" {
					activeSession.Command = "tui"
				}

				appendInProgress(activeSession)

				continue
			}

			telemetryLog, ok := telemetryByConversationLog[filepath.Clean(log.path)]
			if !ok {
				telemetryLog, ok = telemetryByConversationLog[displayPath]
			}

			if !ok {
				telemetryLog, ok = lookupTelemetryForConversation(log.path, facts)
			}

			if ok {
				telemetryFacts := telemetryLog.facts
				if telemetryFacts.HasProcessStart && !telemetryFacts.HasProcessExit && processIDAppearsRunning(telemetryFacts.PID) {
					appendInProgress(harnessAuditInProgressSession{
						TelemetryLog:    telemetryLog.displayPath,
						ConversationLog: displayPath,
						SessionID:       textutil.FirstNonEmpty(strings.TrimSpace(facts.SessionID), strings.TrimSpace(telemetryFacts.SessionID)),
						Command:         textutil.FirstNonEmpty(strings.TrimSpace(telemetryFacts.Command), "tui"),
						PID:             telemetryFacts.PID,
					})

					continue
				}

				missing := make([]string, 0, 2)
				if telemetryFacts.HasProcessStart && !telemetryFacts.HasProcessExit {
					missing = append(missing, "process_exit")
				}

				if !telemetryFacts.HasModelSessionCloseout {
					missing = append(missing, "session_summary")
				}

				if len(missing) == 0 {
					continue
				}

				session := harnessAuditIncompleteSession{
					TelemetryLog:    telemetryLog.displayPath,
					ConversationLog: displayPath,
					SessionID:       textutil.FirstNonEmpty(strings.TrimSpace(facts.SessionID), strings.TrimSpace(telemetryFacts.SessionID)),
					Command:         textutil.FirstNonEmpty(strings.TrimSpace(telemetryFacts.Command), "tui"),
					Missing:         missing,
				}
				key := session.TelemetryLog + "|" + session.ConversationLog

				if _, ok := seenIncomplete[key]; !ok {
					seenIncomplete[key] = struct{}{}

					incomplete = append(incomplete, session)
				}

				continue
			}

			session := harnessAuditIncompleteSession{
				ConversationLog: displayPath,
				SessionID:       strings.TrimSpace(facts.SessionID),
				Command:         "implement",
				Missing:         []string{"session_summary"},
			}
			key := session.TelemetryLog + "|" + session.ConversationLog

			if _, ok := seenIncomplete[key]; !ok {
				seenIncomplete[key] = struct{}{}

				incomplete = append(incomplete, session)
			}
		}
	}

	sort.Slice(incomplete, func(i, j int) bool {
		left := firstNonEmptyAuditSessionPath(incomplete[i])
		right := firstNonEmptyAuditSessionPath(incomplete[j])

		return left < right
	})
	sort.Slice(inProgress, func(i, j int) bool {
		left := firstNonEmptyAuditInProgressSessionPath(inProgress[i])
		right := firstNonEmptyAuditInProgressSessionPath(inProgress[j])

		return left < right
	})

	return incomplete, inProgress, warnings
}

func (m *Manager) auditConversationPathFromTelemetry(facts harnessAuditLogFacts) string {
	path := strings.TrimSpace(facts.DebugLogPath)
	if path == "" {
		return ""
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(m.cfg.CWD, path)
	}

	path = filepath.Clean(path)
	if _, err := os.Stat(path); err != nil {
		return ""
	}

	if m.pathAccess.PathWithinWorkspace(path) || auditPathWithinRoot(path, facts.WorkspaceRoot) {
		return path
	}

	return ""
}

func harnessAuditSessionTimestampPrefix(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if idx := strings.Index(sessionID, "."); idx > 0 {
		sessionID = sessionID[:idx]
	}

	if len(sessionID) != len("20060102-150405") || sessionID[8] != '-' {
		return ""
	}

	for i, r := range sessionID {
		if i == 8 {
			continue
		}

		if r < '0' || r > '9' {
			return ""
		}
	}

	return sessionID
}

func readHarnessAuditLogFacts(root string, path string) (harnessAuditLogFacts, error) {
	data, err := safepath.ReadFileUnderRoot(root, path, "audit log", safepath.Options{})
	if err != nil {
		return harnessAuditLogFacts{}, err
	}

	var facts harnessAuditLogFacts

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var record map[string]any

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		eventType := auditStringField(record, "type")
		role := auditStringField(record, "role")

		switch eventType {
		case "process_start":
			facts.HasProcessStart = true
			facts.Command = textutil.FirstNonEmpty(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = textutil.FirstNonEmpty(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
			facts.PID = auditIntField(record, "pid")
		case "session_start":
			facts.Command = textutil.FirstNonEmpty(facts.Command, auditStringField(record, "command"))
			facts.WorkspaceRoot = textutil.FirstNonEmpty(facts.WorkspaceRoot, auditStringField(record, "workspace_root"))
		case "process_exit":
			facts.HasProcessExit = true
		case "model_session":
			facts.HasModelSession = true
			facts.SessionID = textutil.FirstNonEmpty(facts.SessionID, auditStringField(record, "session_id"))
			facts.DebugLogPath = textutil.FirstNonEmpty(facts.DebugLogPath, auditStringField(record, "debug_log_path"))
		case "session_summary":
			facts.HasSessionSummary = true
		case "model_session_closeout", "implement_session_closeout":
			facts.HasModelSessionCloseout = true
			facts.SessionID = textutil.FirstNonEmpty(facts.SessionID, auditStringField(record, "session_id"))
			facts.DebugLogPath = textutil.FirstNonEmpty(facts.DebugLogPath, auditStringField(record, "debug_log_path"))
		}

		if role == "control_rails" || eventType == "control_rails" {
			if auditControlRailsIndicateImplementation(record) {
				facts.IsImplementConversation = true
			}
		}

		if eventType == "session_start" && strings.EqualFold(auditStringField(record, "command"), "implement") {
			facts.IsImplementConversation = true
		}
	}

	if facts.SessionID == "" {
		facts.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return facts, nil
}

func processIDAppearsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return process.Signal(syscall.Signal(0)) == nil
}

func auditControlRailsIndicateImplementation(record map[string]any) bool {
	if strings.Contains(strings.ToLower(auditStringField(record, "content")), "implementation_result") {
		return true
	}

	for _, key := range []string{"contract", "task_family", "lane", "permission_profile"} {
		value := strings.ToLower(auditStringField(record, key))
		if strings.Contains(value, "implementation") || strings.Contains(value, "implement") {
			return true
		}
	}

	return false
}

func auditPathWithinRoot(path string, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))

	if path == "" || root == "" {
		return false
	}

	rel, err := filepath.Rel(root, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`)
}

func auditStringField(record map[string]any, key string) string {
	if value, ok := record[key]; ok {
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}

	return ""
}

func auditIntField(record map[string]any, key string) int {
	value, ok := record[key]
	if !ok {
		return 0
	}

	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	}

	return 0
}

func firstNonEmptyAuditInProgressSessionPath(session harnessAuditInProgressSession) string {
	if strings.TrimSpace(session.TelemetryLog) != "" {
		return strings.TrimSpace(session.TelemetryLog)
	}

	return strings.TrimSpace(session.ConversationLog)
}

func firstNonEmptyAuditSessionPath(session harnessAuditIncompleteSession) string {
	if strings.TrimSpace(session.TelemetryLog) != "" {
		return strings.TrimSpace(session.TelemetryLog)
	}

	return strings.TrimSpace(session.ConversationLog)
}

func (m *Manager) auditLogPaths(req ToolRequest, limit int) ([]string, []harnessAuditLogSource, []string, error) {
	var (
		warnings []string
		sources  []harnessAuditLogSource
		paths    []string
	)

	defaultScope := false

	switch {
	case strings.TrimSpace(req.Glob) != "":
		pattern := strings.TrimSpace(req.Glob)

		matches, err := m.auditGlob(pattern)
		if err != nil {
			return nil, nil, nil, err
		}

		sources = append(sources, harnessAuditLogSource{Pattern: pattern, Matched: len(matches), Selected: len(matches)})
		paths = append(paths, matches...)
	case strings.TrimSpace(req.Path) != "":
		path := strings.TrimSpace(req.Path)
		if hasAuditGlobMagic(path) {
			matches, err := m.auditGlob(path)
			if err != nil {
				return nil, nil, nil, err
			}

			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: len(matches), Selected: len(matches)})
			paths = append(paths, matches...)

			break
		}

		target, err := m.pathAccess.ResolvePath(path, req.Workdir)
		if err != nil {
			return nil, nil, nil, err
		}

		info, err := os.Stat(target)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("audit log path %q: %w", path, err)
		}

		if info.IsDir() {
			matches, err := auditJSONLFilesUnder(target)
			if err != nil {
				return nil, nil, nil, err
			}

			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: len(matches), Selected: len(matches)})
			paths = append(paths, matches...)
		} else {
			sources = append(sources, harnessAuditLogSource{Pattern: path, Matched: 1, Selected: 1})
			paths = append(paths, target)
		}
	default:
		defaultScope = true

		for _, pattern := range []string{
			".idiom/logs/conversations/*.jsonl",
			"logs/conversations/*.jsonl",
			".idiom/logs/e2e/*.jsonl",
			"logs/e2e/*.jsonl",
			"logs/telemetry/*.jsonl",
		} {
			matches, err := m.auditDefaultJSONLGlob(pattern)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}

			matches = auditUniqueExistingFiles(matches)
			sortAuditLogPaths(matches)

			source := harnessAuditLogSource{Pattern: pattern, Matched: len(matches), Selected: len(matches)}
			if len(matches) > limit {
				warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched for %s", limit, len(matches), pattern))
				matches = matches[:limit]
				source.Selected = len(matches)
				source.Capped = true
			}

			sources = append(sources, source)
			paths = append(paths, matches...)
		}
	}

	paths = auditUniqueExistingFiles(paths)
	sortAuditLogPaths(paths)

	if len(paths) == 0 {
		warnings = append(warnings, "no JSONL behavior logs matched the audit scope")
		return paths, sources, warnings, nil
	}

	if len(paths) > limit && !defaultScope {
		warnings = append(warnings, fmt.Sprintf("log scan capped at %d newest file(s) out of %d matched", limit, len(paths)))
		paths = paths[:limit]

		if len(sources) > 0 {
			sources[len(sources)-1].Selected = len(paths)
			sources[len(sources)-1].Capped = true
		}
	}

	return paths, sources, warnings, nil
}

func (m *Manager) auditDefaultJSONLGlob(pattern string) ([]string, error) {
	const suffix = "/*.jsonl"

	if !strings.HasSuffix(strings.ToLower(pattern), suffix) {
		return m.auditGlob(pattern)
	}

	dirPattern := pattern[:len(pattern)-len(suffix)]
	if strings.TrimSpace(dirPattern) == "" {
		return m.auditGlob(pattern)
	}

	if hasAuditGlobMagic(dirPattern) {
		return m.auditGlob(pattern)
	}

	dir, err := m.pathAccess.ResolvePath(dirPattern, "")
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}

	entries, err := safepath.ReadDirUnderRoot(m.cfg.Root, dir, "read audit directory", safepath.Options{})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			continue
		}

		paths = append(paths, filepath.Join(dir, entry.Name()))
	}

	return paths, nil
}

func (m *Manager) auditGlob(pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil
	}

	var absPattern string

	if filepath.IsAbs(pattern) {
		absPattern = filepath.Clean(pattern)
	} else {
		absPattern = filepath.Clean(filepath.Join(m.cfg.CWD, pattern))
	}

	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("audit glob %q: %w", pattern, err)
	}

	for _, match := range matches {
		if err := m.ensureAuditPathInWorkspace(match); err != nil {
			return nil, err
		}
	}

	return matches, nil
}

func (m *Manager) ensureAuditPathInWorkspace(path string) error {
	rel, err := filepath.Rel(m.cfg.Root, filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve audit path: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("audit path %q escapes workspace root", path)
	}

	return nil
}

func auditJSONLFilesUnder(root string) ([]string, error) {
	var paths []string

	err := safepath.WalkDirUnderRoot(root, root, "audit log discovery", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if path != root && discovery.ShouldSkipDirName(d.Name()) {
				return filepath.SkipDir
			}

			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			paths = append(paths, path)
		}

		return nil
	})

	return paths, err
}

func harnessAuditLogLimit(limit int) int {
	if limit <= 0 {
		return harnessAuditDefaultLogLimit
	}

	if limit > harnessAuditMaxLogLimit {
		return harnessAuditMaxLogLimit
	}

	return limit
}

func hasAuditGlobMagic(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func auditUniqueExistingFiles(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))

	for _, path := range paths {
		path = filepath.Clean(path)
		if _, dup := seen[path]; dup {
			continue
		}

		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	return out
}

func sortAuditLogPaths(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		infoI, errI := os.Stat(paths[i])
		infoJ, errJ := os.Stat(paths[j])

		if errI == nil && errJ == nil && !infoI.ModTime().Equal(infoJ.ModTime()) {
			return infoI.ModTime().After(infoJ.ModTime())
		}

		return paths[i] < paths[j]
	})
}

func currentHarnessAuditIncompleteSessions(sessions []harnessAuditIncompleteSession) []harnessAuditIncompleteSession {
	var current []harnessAuditIncompleteSession

	for _, session := range sessions {
		occurredAt, ok := harnessAuditIncompleteSessionTime(session)
		if !ok || !occurredAt.Before(harnessAuditLifecycleCloseoutIntroducedAt) {
			current = append(current, session)
		}
	}

	return current
}

func harnessAuditIncompleteSessionTime(session harnessAuditIncompleteSession) (time.Time, bool) {
	for _, value := range []string{
		session.SessionID,
		strings.TrimSuffix(filepath.Base(session.TelemetryLog), filepath.Ext(session.TelemetryLog)),
		strings.TrimSuffix(filepath.Base(session.ConversationLog), filepath.Ext(session.ConversationLog)),
	} {
		if parsed, ok := harnessAuditTimestampFromLabel(value); ok {
			return parsed, true
		}
	}

	return time.Time{}, false
}

func harnessAuditTimestampFromLabel(value string) (time.Time, bool) {
	if prefix := harnessAuditSessionTimestampPrefix(value); prefix != "" {
		if parsed, err := time.Parse("20060102-150405", prefix); err == nil {
			return parsed.UTC(), true
		}
	}

	for i := 0; i+len("20060102") <= len(value); i++ {
		candidate := value[i : i+len("20060102")]
		if !harnessAuditAllDigits(candidate) {
			continue
		}

		parsed, err := time.Parse("20060102", candidate)
		if err == nil {
			return parsed.UTC(), true
		}
	}

	return time.Time{}, false
}

func harnessAuditAllDigits(value string) bool {
	if value == "" {
		return false
	}

	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}
