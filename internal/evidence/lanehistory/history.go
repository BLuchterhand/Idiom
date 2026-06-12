package lanehistory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const RelPath = ".idiom/lane-history.jsonl"

type Event struct {
	Timestamp        string   `json:"ts"`
	Mode             string   `json:"mode"`
	Brief            string   `json:"brief"`
	BriefMtime       string   `json:"brief_mtime,omitempty"`
	BriefProvenance  string   `json:"brief_provenance,omitempty"`
	Title            string   `json:"title,omitempty"`
	ExitCode         int      `json:"exit_code"`
	FinalState       string   `json:"final_state,omitempty"`
	SessionID        string   `json:"session_id,omitempty"`
	QueuePool        string   `json:"queue_pool,omitempty"`
	ModelCalls       int      `json:"model_calls,omitempty"`
	ToolCalls        int      `json:"tool_calls,omitempty"`
	PromptTokens     int      `json:"prompt_tokens,omitempty"`
	CompletionTokens int      `json:"completion_tokens,omitempty"`
	TotalTokens      int      `json:"total_tokens,omitempty"`
	PeakPromptTokens int      `json:"peak_prompt_tokens,omitempty"`
	ContextSize      int      `json:"ctx_size,omitempty"`
	DurationMS       int      `json:"duration_ms,omitempty"`
	StopReason       string   `json:"stop_reason,omitempty"`
	Degradations     []string `json:"degradations,omitempty"`
}

const refineDraftedMarker = "<!-- idiom:refine-drafted"

func DetectBriefProvenance(briefPath string) string {
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

func Append(projectRoot string, evt Event) error {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}

	if strings.TrimSpace(evt.Timestamp) == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	path := filepath.Join(projectRoot, RelPath)
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

func EventFields(evt Event) map[string]any {
	body, err := json.Marshal(evt)
	if err != nil {
		return map[string]any{}
	}

	var fields map[string]any

	if err := json.Unmarshal(body, &fields); err != nil {
		return map[string]any{}
	}

	delete(fields, "ts")

	return fields
}

type Summary struct {
	Total                 int
	ByMode                map[string]int
	ByExitCode            map[int]int
	ByFinalState          map[string]int
	BriefsByPath          map[string]int
	ByProvenance          map[string]int
	TotalModelCalls       int
	TotalToolCalls        int
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalTokens           int
	TotalDurationMS       int
	EventsWithTokens      int
	EventsWithRunMetrics  int
}

type Filter struct {
	Since      time.Time
	FailedOnly bool
	ModeFilter string
}

func SummarizeWithFilter(projectRoot string, filter Filter) (Summary, error) {
	path := filepath.Join(projectRoot, RelPath)

	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newSummary(), nil
		}

		return Summary{}, err
	}

	out := newSummary()

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var evt Event

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

		if evt.FinalState != "" {
			out.ByFinalState[evt.FinalState]++
		}

		if evt.Brief != "" {
			out.BriefsByPath[evt.Brief]++
		}

		if evt.BriefProvenance != "" {
			out.ByProvenance[evt.BriefProvenance]++
		}

		accumulateRunMetrics(&out, evt)
	}

	return out, nil
}

func newSummary() Summary {
	return Summary{
		ByMode:       map[string]int{},
		ByExitCode:   map[int]int{},
		ByFinalState: map[string]int{},
		BriefsByPath: map[string]int{},
		ByProvenance: map[string]int{},
	}
}

func accumulateRunMetrics(out *Summary, evt Event) {
	if evt.ModelCalls != 0 || evt.ToolCalls != 0 || evt.DurationMS != 0 {
		out.TotalModelCalls += evt.ModelCalls
		out.TotalToolCalls += evt.ToolCalls
		out.TotalDurationMS += evt.DurationMS
		out.EventsWithRunMetrics++
	}

	if evt.PromptTokens != 0 || evt.CompletionTokens != 0 || evt.TotalTokens != 0 {
		out.TotalPromptTokens += evt.PromptTokens
		out.TotalCompletionTokens += evt.CompletionTokens
		out.TotalTokens += evt.TotalTokens
		out.EventsWithTokens++
	}
}

func Summarize(projectRoot string) (Summary, error) {
	return SummarizeWithFilter(projectRoot, Filter{})
}

func SummarizeByMode(projectRoot string, mode string) (Summary, error) {
	return SummarizeWithFilter(projectRoot, Filter{ModeFilter: mode})
}

func PrintSummary(projectRoot string, w io.Writer, filter Filter) error {
	s, err := SummarizeWithFilter(projectRoot, filter)
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

	if len(s.ByFinalState) > 0 {
		fmt.Fprintln(w, "by final state:")

		for state, n := range s.ByFinalState {
			fmt.Fprintf(w, "  %s: %d\n", state, n)
		}
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

	if s.EventsWithRunMetrics > 0 {
		fmt.Fprintf(w, "run metrics (across %d dispatch(es) with summaries):\n", s.EventsWithRunMetrics)
		fmt.Fprintf(w, "  model calls: %d\n", s.TotalModelCalls)
		fmt.Fprintf(w, "  tool calls: %d\n", s.TotalToolCalls)
		fmt.Fprintf(w, "  duration_ms: %d\n", s.TotalDurationMS)
	}

	return nil
}
