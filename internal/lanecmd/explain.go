package lanecmd

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/util"
)

// laneExplainSummary is the diagnostic payload runLaneExplain renders.
// Fields stay zero/empty when the source log lacks the record;
// printers must tolerate partial data.
type laneExplainSummary struct {
	SessionID        string
	SessionLogPath   string
	StartedAt        string
	EndedAt          string
	DurationMS       int64
	ModelCalls       int
	ToolCalls        int
	Turns            int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	PeakPromptTokens int
	CtxSize          int
	StopReason       string
	BudgetExhausted  bool
	ToolsByCount     []toolCount
	ToolRejections   []string
	Errors           []string
	Question         string
	CompletionStatus map[string]any
}

type toolCount struct {
	Tool  string
	Count int
}

// runLaneExplain renders a one-screen diagnostic for the matching
// session. Selection precedence: --session > --brief > most-recent.
// Returns 0 on success, 1 if no matching session is found, 2 on
// CLI/usage errors caught upstream.
func runLaneExplain(opts laneOptions, stdout io.Writer, stderr io.Writer) int {
	logsDir := filepath.Join(opts.ProjectRoot, ".idiom", "logs", "conversations")
	sessionPath, err := selectLaneExplainSession(logsDir, opts.ExplainSessionID, opts.ExplainBriefFilter, opts.ProjectRoot)
	if err != nil {
		fmt.Fprintf(stderr, "lane: explain: %v\n", err)
		return 1
	}
	if sessionPath == "" {
		fmt.Fprintln(stderr, "lane: explain: no matching session log found")
		return 1
	}
	body, err := os.ReadFile(sessionPath)
	if err != nil {
		fmt.Fprintf(stderr, "lane: explain: read %q: %v\n", sessionPath, err)
		return 1
	}
	summary := buildLaneExplainSummary(body)
	summary.SessionLogPath = sessionPath
	if summary.SessionID == "" {
		summary.SessionID = sessionIDFromLogFilename(filepath.Base(sessionPath))
	}
	renderLaneExplain(summary, stdout)
	return 0
}

// selectLaneExplainSession picks the session log to diagnose.
// Returns absolute path or "" if no match. --session pins to one
// id (substring match against filenames). --brief filters
// lane-history.jsonl to the most-recent dispatch of that brief and
// uses its session id. When both are empty, returns the newest log.
func selectLaneExplainSession(logsDir, sessionIDFlag, briefFlag, projectRoot string) (string, error) {
	if strings.TrimSpace(sessionIDFlag) != "" {
		return findSessionLogByID(logsDir, strings.TrimSpace(sessionIDFlag))
	}
	if strings.TrimSpace(briefFlag) != "" {
		sid, err := sessionIDForLatestBriefDispatch(projectRoot, strings.TrimSpace(briefFlag))
		if err != nil {
			return "", err
		}
		if sid == "" {
			return "", fmt.Errorf("no lane-history entry for brief %q", briefFlag)
		}
		return findSessionLogByID(logsDir, sid)
	}
	return findNewestSessionLog(logsDir)
}

func findSessionLogByID(logsDir, sessionID string) (string, error) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return "", fmt.Errorf("read logs dir %q: %w", logsDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if strings.Contains(name, sessionID) {
			return filepath.Join(logsDir, name), nil
		}
	}
	return "", nil
}

func findNewestSessionLog(logsDir string) (string, error) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return "", fmt.Errorf("read logs dir %q: %w", logsDir, err)
	}
	var newest string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		// Filenames follow YYYYMMDD-HHMMSS.MS-<id>.jsonl, so a
		// lexicographic max is the chronological newest. Avoids
		// stat'ing every file and works the same on all platforms.
		if name > newest {
			newest = name
		}
	}
	if newest == "" {
		return "", nil
	}
	return filepath.Join(logsDir, newest), nil
}

// sessionIDForLatestBriefDispatch scans lane-history.jsonl for the
// latest entry whose brief field matches briefPath, and returns the
// session id from its log filename pattern. Returns "" when the
// brief isn't in history. The session id is the trailing 16 hex
// chars of the log filename (matches the suffix lane writers emit).
func sessionIDForLatestBriefDispatch(projectRoot, briefPath string) (string, error) {
	path := filepath.Join(projectRoot, laneHistoryRelPath)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var latestTs string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt laneHistoryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Brief != briefPath {
			continue
		}
		if evt.Timestamp > latestTs {
			latestTs = evt.Timestamp
		}
	}
	if latestTs == "" {
		return "", nil
	}
	// lane-history doesn't store the session id directly; the log
	// filename is the only ground-truth link. Caller passes our
	// timestamp prefix to the log scanner which compares filenames.
	// Filename shape: YYYYMMDD-HHMMSS.MS-<sessionid>.jsonl
	// Timestamp shape: 2026-05-11T08:20:22.789-04:00
	// Match by reformatting: 20260511-082022 prefix.
	return briefTimestampToFilenamePrefix(latestTs), nil
}

// briefTimestampToFilenamePrefix converts an RFC3339Nano timestamp
// to the filename-prefix shape session logs use (YYYYMMDD-HHMMSS).
// Used to bridge lane-history.jsonl timestamps to the on-disk log
// files. Returns "" when parsing fails.
func briefTimestampToFilenamePrefix(ts string) string {
	// Find the date and time components by splitting on T.
	t := strings.SplitN(ts, "T", 2)
	if len(t) != 2 {
		return ""
	}
	date := strings.ReplaceAll(t[0], "-", "")
	rest := t[1]
	// Strip the timezone suffix.
	for _, sep := range []string{"+", "-", "Z"} {
		if idx := strings.IndexAny(rest, sep); idx > 0 {
			rest = rest[:idx]
			break
		}
	}
	parts := strings.Split(rest, ":")
	if len(parts) < 3 {
		return ""
	}
	// Drop fractional seconds from the seconds component.
	sec := parts[2]
	if dot := strings.Index(sec, "."); dot >= 0 {
		sec = sec[:dot]
	}
	return date + "-" + parts[0] + parts[1] + sec
}

func sessionIDFromLogFilename(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if idx := strings.LastIndex(name, "-"); idx >= 0 && idx < len(name)-1 {
		return name[idx+1:]
	}
	return name
}

// buildLaneExplainSummary walks the session log JSONL and extracts
// the diagnostic signals worth surfacing. Each event type that
// contributes is recognized by its `type` field; unrecognized
// events are ignored so future schema additions don't break the
// explain output.
func buildLaneExplainSummary(body []byte) laneExplainSummary {
	out := laneExplainSummary{CompletionStatus: map[string]any{}}
	toolCounts := map[string]int{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev["type"] {
		case "session_start":
			if v, ok := ev["session_id"].(string); ok {
				out.SessionID = v
			}
			if v, ok := ev["ts"].(string); ok {
				out.StartedAt = v
			}
		case "session_summary":
			out.EndedAt = jsonString(ev["ts"])
			out.DurationMS = int64(jsonInt(ev["duration_ms"]))
			out.ModelCalls = jsonInt(ev["model_calls"])
			out.ToolCalls = jsonInt(ev["tool_calls"])
			out.Turns = jsonInt(ev["turns"])
			out.PromptTokens = jsonInt(ev["prompt_tokens"])
			out.CompletionTokens = jsonInt(ev["completion_tokens"])
			out.TotalTokens = jsonInt(ev["total_tokens"])
			out.PeakPromptTokens = jsonInt(ev["peak_prompt_tokens"])
			out.CtxSize = jsonInt(ev["ctx_size"])
		case "tool_result":
			tool := jsonString(ev["tool"])
			if tool != "" {
				toolCounts[tool]++
			}
			if errStr := jsonString(ev["error"]); errStr != "" {
				out.ToolRejections = append(out.ToolRejections, fmt.Sprintf("%s: %s", tool, errStr))
			}
		case "completion_status":
			// Last one wins — final completion_status is the
			// authoritative lane-end state.
			out.CompletionStatus = ev
		case "final":
			content := jsonString(ev["content"])
			if strings.Contains(content, "<<QUESTION>>:") {
				out.Question = content
			}
		case "error":
			msg := jsonString(ev["message"])
			kind := jsonString(ev["error_kind"])
			if msg != "" {
				out.Errors = append(out.Errors, fmt.Sprintf("%s: %s", kind, msg))
			}
		case "turn_summary":
			out.StopReason = jsonString(ev["stop_reason"])
			out.BudgetExhausted = jsonBool(ev["budget_exhausted"])
		}
	}
	for tool, n := range toolCounts {
		out.ToolsByCount = append(out.ToolsByCount, toolCount{Tool: tool, Count: n})
	}
	slices.SortFunc(out.ToolsByCount, func(a, b toolCount) int {
		return cmp.Or(
			-cmp.Compare(a.Count, b.Count),
			cmp.Compare(a.Tool, b.Tool),
		)
	})
	return out
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func jsonBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// renderLaneExplain prints a one-screen diagnostic. Sections are
// emitted only when there's something to say — a clean run shows
// only header + tokens; a wedge shows the rejection chain.
func renderLaneExplain(s laneExplainSummary, w io.Writer) {
	fmt.Fprintf(w, "lane explain: session=%s\n", s.SessionID)
	if s.SessionLogPath != "" {
		fmt.Fprintf(w, "  log: %s\n", s.SessionLogPath)
	}
	if s.StartedAt != "" || s.EndedAt != "" {
		fmt.Fprintf(w, "  span: %s → %s (%d ms)\n", s.StartedAt, s.EndedAt, s.DurationMS)
	}
	if s.ModelCalls > 0 || s.ToolCalls > 0 {
		fmt.Fprintf(w, "  calls: %d model, %d tool, %d turn(s)\n", s.ModelCalls, s.ToolCalls, s.Turns)
	}
	if s.TotalTokens > 0 {
		pct := 0.0
		if s.CtxSize > 0 {
			pct = 100.0 * float64(s.PeakPromptTokens) / float64(s.CtxSize)
		}
		fmt.Fprintf(w, "  tokens: prompt=%d completion=%d total=%d peak_prompt=%d (%.1f%% of ctx=%d)\n",
			s.PromptTokens, s.CompletionTokens, s.TotalTokens, s.PeakPromptTokens, pct, s.CtxSize)
	}
	if len(s.ToolsByCount) > 0 {
		fmt.Fprintln(w, "tools called:")
		for _, tc := range s.ToolsByCount {
			fmt.Fprintf(w, "  %s: %d\n", tc.Tool, tc.Count)
		}
	}
	if len(s.ToolRejections) > 0 {
		fmt.Fprintln(w, "tool rejections:")
		for _, r := range s.ToolRejections {
			fmt.Fprintf(w, "  - %s\n", truncateForExplain(r, 200))
		}
	}
	if len(s.Errors) > 0 {
		fmt.Fprintln(w, "errors:")
		for _, e := range s.Errors {
			fmt.Fprintf(w, "  - %s\n", truncateForExplain(e, 200))
		}
	}
	if s.StopReason != "" {
		fmt.Fprintf(w, "stop_reason: %s", s.StopReason)
		if s.BudgetExhausted {
			fmt.Fprintf(w, " (budget exhausted)")
		}
		fmt.Fprintln(w)
	}
	if len(s.CompletionStatus) > 0 {
		// Print only the typed-blocker hint fields, not the whole
		// payload — full dump is the log file.
		if status := jsonString(s.CompletionStatus["status"]); status != "" {
			fmt.Fprintf(w, "completion_status: %s", status)
			if kind := jsonString(s.CompletionStatus["blocker_kind"]); kind != "" {
				fmt.Fprintf(w, " (%s)", kind)
			}
			fmt.Fprintln(w)
		}
	}
	if s.Question != "" {
		fmt.Fprintln(w, "final <<QUESTION>>:")
		fmt.Fprintln(w, "  "+truncateForExplain(s.Question, 400))
	}
}

func truncateForExplain(s string, n int) string {
	return util.TruncateBytes(strings.ReplaceAll(s, "\n", " "), n, "…")
}
