package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// laneHistoryRelPath is the project-rooted location of the lane
// dispatch log. Append-only JSONL, one event per line.
const laneHistoryRelPath = ".idiom/lane-history.jsonl"

// laneHistoryEvent is one dispatch record. Fields are intentionally
// flat so jq / shell tools can summarize without a parser.
//
// Token fields (PromptTokens/CompletionTokens/TotalTokens) are
// populated only when the underlying subcommand was invoked with
// --json (the wrapper extracts them from the session_summary stdout
// event). When --json is off the fields stay zero and are omitted
// from the on-disk record; lane history aggregations skip zero
// values so the breakdown still works for mixed history.
type laneHistoryEvent struct {
	Timestamp        string `json:"ts"`
	Mode             string `json:"mode"`
	Brief            string `json:"brief"`
	BriefMtime       string `json:"brief_mtime,omitempty"`
	BriefProvenance  string `json:"brief_provenance,omitempty"`
	Title            string `json:"title,omitempty"`
	ExitCode         int    `json:"exit_code"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
}

// refineDraftedMarker is the HTML comment finalizeRefine prepends to
// every refine-produced brief. detectBriefProvenance greps for this
// in the first 1024 bytes; cheap and robust to file renames.
const refineDraftedMarker = "<!-- idiom:refine-drafted"

// detectBriefProvenance returns "refine-drafted" if the brief at
// briefPath starts with the refine marker (in its first 1KB),
// "claude-direct" if the file is readable but the marker is absent,
// and "unknown" if the path is empty or unreadable.
func detectBriefProvenance(briefPath string) string {
	if strings.TrimSpace(briefPath) == "" {
		return "unknown"
	}
	f, err := os.Open(briefPath)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return "claude-direct"
	}
	if strings.Contains(string(buf[:n]), refineDraftedMarker) {
		return "refine-drafted"
	}
	return "claude-direct"
}

// appendLaneHistory appends one event to the history log under
// projectRoot. Concurrent appenders are safe because os.O_APPEND
// guarantees atomic single-write behavior for sub-PIPE_BUF lines
// (a JSONL line is well under that). Errors are returned but
// callers should log-and-continue — a missing history log must
// never abort a real dispatch.
func appendLaneHistory(projectRoot string, evt laneHistoryEvent) error {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	if strings.TrimSpace(evt.Timestamp) == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	path := filepath.Join(projectRoot, laneHistoryRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open history: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write history: %w", err)
	}
	return nil
}

// laneHistorySummary aggregates the on-disk history into a
// human-readable report.
type laneHistorySummary struct {
	Total                 int
	ByMode                map[string]int
	ByExitCode            map[int]int
	BriefsByPath          map[string]int
	ByProvenance          map[string]int
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalTokens           int
	EventsWithTokens      int
}

// laneHistoryFilter constrains which events a summary includes.
// Zero values mean "no filter" — every event is included.
type laneHistoryFilter struct {
	Since      time.Time // include events with ts >= Since; zero time = no filter
	FailedOnly bool      // include only events with non-zero exit_code
	ModeFilter string    // include only events whose Mode equals this (empty = no filter)
}

// summarizeLaneHistoryWithFilter reads the history file and aggregates,
// applying the given filter. Reuses the same JSONL parsing as
// summarizeLaneHistory but skips events that don't match the filter.
func summarizeLaneHistoryWithFilter(projectRoot string, filter laneHistoryFilter) (laneHistorySummary, error) {
	path := filepath.Join(projectRoot, laneHistoryRelPath)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return laneHistorySummary{
				ByMode:       map[string]int{},
				ByExitCode:   map[int]int{},
				BriefsByPath: map[string]int{},
				ByProvenance: map[string]int{},
			}, nil
		}
		return laneHistorySummary{}, err
	}
	out := laneHistorySummary{
		ByMode:       map[string]int{},
		ByExitCode:   map[int]int{},
		BriefsByPath: map[string]int{},
		ByProvenance: map[string]int{},
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt laneHistoryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if !filter.Since.IsZero() {
			evtTs, err := time.Parse(time.RFC3339Nano, evt.Timestamp)
			if err != nil {
				continue
			}
			if evtTs.Before(filter.Since) {
				continue
			}
		}
		if filter.FailedOnly && evt.ExitCode == 0 {
			continue
		}
		if filter.ModeFilter != "" && evt.Mode != filter.ModeFilter {
			continue
		}
		out.Total++
		out.ByMode[evt.Mode]++
		out.ByExitCode[evt.ExitCode]++
		if evt.Brief != "" {
			out.BriefsByPath[evt.Brief]++
		}
		if evt.BriefProvenance != "" {
			out.ByProvenance[evt.BriefProvenance]++
		}
		accumulateLaneHistoryTokens(&out, evt)
	}
	return out, nil
}

// accumulateLaneHistoryTokens folds one event's token totals into
// the running summary. Zero-token events (subcommand ran without
// --json) are skipped so EventsWithTokens reflects only events
// that actually contributed numbers — the display layer uses this
// to print the per-event-with-tokens average, not the per-total-
// dispatch average.
func accumulateLaneHistoryTokens(out *laneHistorySummary, evt laneHistoryEvent) {
	if evt.PromptTokens == 0 && evt.CompletionTokens == 0 && evt.TotalTokens == 0 {
		return
	}
	out.TotalPromptTokens += evt.PromptTokens
	out.TotalCompletionTokens += evt.CompletionTokens
	out.TotalTokens += evt.TotalTokens
	out.EventsWithTokens++
}

// summarizeLaneHistory reads the history file and aggregates.
// Malformed lines are skipped. A missing file returns an empty
// summary with nil error.
func summarizeLaneHistory(projectRoot string) (laneHistorySummary, error) {
	path := filepath.Join(projectRoot, laneHistoryRelPath)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return laneHistorySummary{
				ByMode:       map[string]int{},
				ByExitCode:   map[int]int{},
				BriefsByPath: map[string]int{},
				ByProvenance: map[string]int{},
			}, nil
		}
		return laneHistorySummary{}, err
	}
	out := laneHistorySummary{
		ByMode:       map[string]int{},
		ByExitCode:   map[int]int{},
		BriefsByPath: map[string]int{},
		ByProvenance: map[string]int{},
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt laneHistoryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		out.Total++
		out.ByMode[evt.Mode]++
		out.ByExitCode[evt.ExitCode]++
		if evt.Brief != "" {
			out.BriefsByPath[evt.Brief]++
		}
		if evt.BriefProvenance != "" {
			out.ByProvenance[evt.BriefProvenance]++
		}
		accumulateLaneHistoryTokens(&out, evt)
	}
	return out, nil
}

func summarizeLaneHistoryByMode(projectRoot string, mode string) (laneHistorySummary, error) {
	path := filepath.Join(projectRoot, laneHistoryRelPath)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return laneHistorySummary{
				ByMode:       map[string]int{},
				ByExitCode:   map[int]int{},
				BriefsByPath: map[string]int{},
				ByProvenance: map[string]int{},
			}, nil
		}
		return laneHistorySummary{}, err
	}
	out := laneHistorySummary{
		ByMode:       map[string]int{},
		ByExitCode:   map[int]int{},
		BriefsByPath: map[string]int{},
		ByProvenance: map[string]int{},
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt laneHistoryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Mode != mode {
			continue
		}
		out.Total++
		out.ByMode[evt.Mode]++
		out.ByExitCode[evt.ExitCode]++
		if evt.Brief != "" {
			out.BriefsByPath[evt.Brief]++
		}
		if evt.BriefProvenance != "" {
			out.ByProvenance[evt.BriefProvenance]++
		}
		accumulateLaneHistoryTokens(&out, evt)
	}
	return out, nil
}

// printLaneHistorySummary writes a human-readable breakdown to w.
func printLaneHistorySummary(projectRoot string, w io.Writer, filter laneHistoryFilter) error {
	s, err := summarizeLaneHistoryWithFilter(projectRoot, filter)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "lane history: %d dispatch(es) recorded\n", s.Total)
	if s.Total == 0 {
		return nil
	}
	fmt.Fprintln(w, "by mode:")
	for mode, n := range s.ByMode {
		fmt.Fprintf(w, "  %s: %d\n", mode, n)
	}
	if len(s.ByProvenance) > 0 {
		fmt.Fprintln(w, "by provenance:")
		for prov, n := range s.ByProvenance {
			fmt.Fprintf(w, "  %s: %d\n", prov, n)
		}
	}
	fmt.Fprintln(w, "by exit code:")
	for code, n := range s.ByExitCode {
		fmt.Fprintf(w, "  %d: %d\n", code, n)
	}
	rerun := 0
	for _, n := range s.BriefsByPath {
		if n > 1 {
			rerun++
		}
	}
	if rerun > 0 {
		fmt.Fprintf(w, "re-dispatched briefs: %d (paths dispatched ≥2 times)\n", rerun)
	}
	if s.EventsWithTokens > 0 {
		fmt.Fprintf(w, "token usage (across %d dispatch(es) run with --json):\n", s.EventsWithTokens)
		fmt.Fprintf(w, "  prompt: %d\n", s.TotalPromptTokens)
		fmt.Fprintf(w, "  completion: %d\n", s.TotalCompletionTokens)
		fmt.Fprintf(w, "  total: %d\n", s.TotalTokens)
		if s.EventsWithTokens > 1 {
			fmt.Fprintf(w, "  avg total/dispatch: %d\n", s.TotalTokens/s.EventsWithTokens)
		}
	}
	return nil
}
