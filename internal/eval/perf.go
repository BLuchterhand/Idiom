package eval

// perf.go scans a conversation JSONL log for efficiency signals. It is
// intentionally descriptive, not prescriptive: every anti-pattern named
// here is something we've seen the agent actually do in live logs.
// Adding a new pattern is cheap; the payoff is that aggregate runs over
// a behavior suite surface the highest-frequency slack so we know which
// harness change will actually move the needle.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolCounts breaks down the tool mix used in a single turn. The
// categories are coarse: total per tool name plus a few "kinds" rolled
// up (go_* and cpp_* semantic tools, doc-class reads). Everything else
// lands in Other — the goal is to notice skew, not bucket perfectly.
type ToolCounts struct {
	ListFiles         int            `json:"list_files,omitempty"`
	ReadFile          int            `json:"read_file,omitempty"`
	SearchContent     int            `json:"search_content,omitempty"`
	SearchFiles       int            `json:"search_files,omitempty"`
	GoSemantic        int            `json:"go_semantic,omitempty"`
	CppSemantic       int            `json:"cpp_semantic,omitempty"`
	Edit              int            `json:"edit,omitempty"`
	Shell             int            `json:"shell,omitempty"`
	Other             int            `json:"other,omitempty"`
	Total             int            `json:"total,omitempty"`
	ByName            map[string]int `json:"by_name,omitempty"`
	DocClassReads     int            `json:"doc_class_reads,omitempty"`
	SourceClassReads  int            `json:"source_class_reads,omitempty"`
	DistinctReadPaths int            `json:"distinct_read_paths,omitempty"`
}

// AntiPattern is one kind of inefficiency observed in a log. Count is
// how many times it fired; Examples are short, human-readable pointers
// so you can click back to the moment it happened.
type AntiPattern struct {
	Name        string   `json:"name"`
	Count       int      `json:"count"`
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
}

// PerfReport is the per-log rollup. Intentionally flat so it marshals
// to a single line in aggregate output; ByName on ToolCounts is the
// one place detail lives.
type PerfReport struct {
	LogPath          string        `json:"log_path"`
	SessionID        string        `json:"session_id,omitempty"`
	UserPrompt       string        `json:"user_prompt,omitempty"`
	ModelCalls       int           `json:"model_calls"`
	ToolResults      int           `json:"tool_results"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	DurationMillis   int           `json:"duration_millis"`
	RejectedActions  int           `json:"rejected_actions"`
	StopReason       string        `json:"stop_reason,omitempty"`
	Tools            ToolCounts    `json:"tools"`
	AntiPatterns     []AntiPattern `json:"anti_patterns,omitempty"`
	AntiPatternScore int           `json:"anti_pattern_score"`
	FinalAnswerChars int           `json:"final_answer_chars,omitempty"`
}

// AnalyzeLog reads a JSONL conversation log from disk and produces one
// PerfReport. Parse errors propagate; missing optional events do not.
func AnalyzeLog(path string) (PerfReport, error) {
	records, err := LoadRecords(path)
	if err != nil {
		return PerfReport{}, err
	}
	return analyzeRecords(path, records), nil
}

// toolStep is the analyzer's view of one tool_request (+ its paired
// tool_result). Kept inside the eval package so detectors can share
// the shape without re-declaring it.
type toolStep struct {
	index     int
	tool      string
	action    string
	rawArgs   string
	target    string
	isReject  bool
	rejectWhy string
	hasError  bool
	errText   string
}

func analyzeRecords(path string, records []Record) PerfReport {
	rep := PerfReport{
		LogPath: path,
		Tools:   ToolCounts{ByName: make(map[string]int)},
	}

	var steps []toolStep
	// Walk records once, collecting what every pattern needs.
	for _, rec := range records {
		switch rec.Type {
		case "session_start":
			if v, ok := rec.Data["workspace_root"].(string); ok && v != "" {
				_ = v
			}
			// The log file name is the session id surrogate.
		case "message":
			role, _ := rec.Data["role"].(string)
			content, _ := rec.Data["content"].(string)
			switch role {
			case "user":
				if rep.UserPrompt == "" {
					rep.UserPrompt = firstLine(content)
				}
			case "assistant":
				// Keep only the last assistant message as the final.
				rep.FinalAnswerChars = len(content)
			case "budget_ledger":
				parseBudgetLedgerIntoPerfReport(content, &rep)
			}
		case "tool_request":
			tool, _ := rec.Data["tool"].(string)
			action, _ := rec.Data["action"].(string)
			raw, _ := rec.Data["raw"].(string)
			steps = append(steps, toolStep{
				index:   len(steps) + 1,
				tool:    tool,
				action:  action,
				rawArgs: raw,
				target:  targetFromAction(action),
			})
		case "tool_result":
			// Pair to the most recent request (same order).
			tool, _ := rec.Data["tool"].(string)
			errMsg, _ := rec.Data["error"].(string)
			summary, _ := rec.Data["summary"].(string)
			if len(steps) > 0 && steps[len(steps)-1].tool == tool {
				idx := len(steps) - 1
				if errMsg != "" {
					steps[idx].hasError = true
					steps[idx].errText = errMsg
					// A "rejection" specifically means a pre-execute
					// guard fired — the summary shape is well-known
					// ("... rejected" or "retry blocked"). Other
					// errors are tool-execution failures (invalid
					// args, missing paths, workspace errors) which
					// we track separately so detectors can target them.
					lowered := strings.ToLower(summary)
					if strings.Contains(lowered, "rejected") || strings.Contains(lowered, "retry blocked") || strings.Contains(lowered, "preempted") {
						steps[idx].isReject = true
						steps[idx].rejectWhy = summary
						rep.RejectedActions++
					}
				}
			}
			countTool(&rep.Tools, tool, summary)
		}
	}

	// Prefer the budget_ledger's tool-result count when it was present;
	// fall back to the number of tool_request events otherwise. Older
	// logs predate budget_ledger.
	if rep.ToolResults == 0 {
		rep.ToolResults = len(steps)
	}

	// Anti-pattern detectors. Each is independent; all run on the same
	// step list so adding a new one is local.
	detectDirWalk(&rep, steps)
	detectZeroHitSearch(&rep, records)
	detectTruncatedSearch(&rep, records)
	detectRedirectStorm(&rep, records)
	detectSameToolRepeat(&rep, steps)
	detectDocWander(&rep, steps)
	detectRejectionRetry(&rep, steps)
	detectMalformedEditArgs(&rep, records)
	detectToolErrorStreak(&rep, steps, records)

	score := 0
	for _, ap := range rep.AntiPatterns {
		score += ap.Count
	}
	rep.AntiPatternScore = score

	// If a budget_ledger record wasn't present, infer from messages.
	if rep.ModelCalls == 0 {
		for _, rec := range records {
			if rec.Type == "message" {
				role, _ := rec.Data["role"].(string)
				if role == "budget_ledger" {
					if content, ok := rec.Data["content"].(string); ok {
						parseBudgetLedgerIntoPerfReport(content, &rep)
					}
				}
			}
		}
	}

	return rep
}

func parseBudgetLedgerIntoPerfReport(content string, rep *PerfReport) {
	var payload struct {
		UsedModelCalls   int    `json:"used_model_calls"`
		UsedToolResults  int    `json:"used_tool_results"`
		PromptTokens     int    `json:"prompt_tokens"`
		CompletionTokens int    `json:"completion_tokens"`
		DurationMillis   int    `json:"duration_millis"`
		RejectedActions  int    `json:"rejected_actions"`
		StopReason       string `json:"stop_reason"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return
	}
	rep.ModelCalls = payload.UsedModelCalls
	rep.ToolResults = payload.UsedToolResults
	rep.PromptTokens = payload.PromptTokens
	rep.CompletionTokens = payload.CompletionTokens
	rep.DurationMillis = payload.DurationMillis
	if payload.RejectedActions > 0 {
		rep.RejectedActions = payload.RejectedActions
	}
	rep.StopReason = payload.StopReason
}

func countTool(t *ToolCounts, tool, summary string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	t.Total++
	t.ByName[tool]++
	switch tool {
	case "list_files":
		t.ListFiles++
	case "read_file":
		t.ReadFile++
		if strings.Contains(strings.ToLower(summary), ".md") ||
			strings.Contains(strings.ToLower(summary), "readme") ||
			strings.Contains(strings.ToLower(summary), ".txt") {
			t.DocClassReads++
		} else {
			t.SourceClassReads++
		}
	case "search_content", "rg", "grep":
		t.SearchContent++
	case "search_files":
		t.SearchFiles++
	case "shell":
		t.Shell++
	case "edit_file", "write_file", "append_file", "apply_patch":
		t.Edit++
	default:
		switch {
		case strings.HasPrefix(tool, "go_"):
			t.GoSemantic++
		case strings.HasPrefix(tool, "cpp_"):
			t.CppSemantic++
		default:
			t.Other++
		}
	}
}

func targetFromAction(action string) string {
	// actions are shaped like "read_file:path/to/file" or
	// "search_content:.:pattern:_" — the second colon-delimited field
	// is the most useful "target" for dedupe / examples.
	if action == "" {
		return ""
	}
	parts := strings.SplitN(action, ":", 3)
	if len(parts) < 2 {
		return parts[0]
	}
	return parts[1]
}

// ---- detectors ----

func detectDirWalk(rep *PerfReport, steps []toolStep) {
	// Any run of 3+ consecutive list_files without a content tool in
	// between counts as a dir-walk streak. One streak per walk, even
	// if it's 6 long — the metric is "how often did this happen",
	// not "how many calls did it cost".
	streaks := 0
	var examples []string
	run := 0
	runStart := 0
	for i, s := range steps {
		if s.tool == "list_files" {
			if run == 0 {
				runStart = s.index
			}
			run++
			continue
		}
		if run >= 3 {
			streaks++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("steps %d-%d (%d list_files)", runStart, steps[i-1].index, run))
			}
		}
		run = 0
	}
	if run >= 3 {
		streaks++
		if len(examples) < 3 {
			examples = append(examples, fmt.Sprintf("steps %d-%d (%d list_files)", runStart, steps[len(steps)-1].index, run))
		}
	}
	if streaks > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "dir_walk_streak",
			Count:       streaks,
			Description: "≥3 consecutive list_files calls without a content tool between them",
			Examples:    examples,
		})
	}
}

func detectZeroHitSearch(rep *PerfReport, records []Record) {
	// A search_files or search_content result with "no matches" shape.
	// These are often invented filenames — the interesting signal is
	// that the model guessed instead of searching content.
	needles := []string{"no file matches", "no matches", "no content matches", "no go symbols matched", "no c++ symbols matched"}
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		tool, _ := rec.Data["tool"].(string)
		summary, _ := rec.Data["summary"].(string)
		lowered := strings.ToLower(summary)
		for _, n := range needles {
			if strings.Contains(lowered, n) {
				count++
				if len(examples) < 3 {
					examples = append(examples, fmt.Sprintf("%s: %s", tool, summary))
				}
				break
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "zero_hit_search",
			Count:       count,
			Description: "search_files/search_content returned no results — model guessed an identifier or filename",
			Examples:    examples,
		})
	}
}

func detectTruncatedSearch(rep *PerfReport, records []Record) {
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		summary, _ := rec.Data["summary"].(string)
		if strings.Contains(strings.ToLower(summary), "showing first") {
			count++
			if len(examples) < 3 {
				examples = append(examples, summary)
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "search_content_truncated",
			Count:       count,
			Description: "search_content returned more hits than the display cap — pattern was too broad",
			Examples:    examples,
		})
	}
}

func detectRedirectStorm(rep *PerfReport, records []Record) {
	redirects := 0
	for _, rec := range records {
		role, _ := rec.Data["role"].(string)
		if role == "discovery_redirect" {
			redirects++
		}
	}
	if redirects >= 2 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "redirect_storm",
			Count:       redirects,
			Description: "multiple discovery_redirect events — model kept reaching for raw read_file on code files",
		})
	}
}

func detectSameToolRepeat(rep *PerfReport, steps []toolStep) {
	counts := make(map[string]int)
	for _, s := range steps {
		key := s.tool + "|" + s.rawArgs
		counts[key]++
	}
	worst := 0
	var worstKey string
	for k, c := range counts {
		if c > worst {
			worst = c
			worstKey = k
		}
	}
	if worst >= 3 {
		display := worstKey
		if len(display) > 80 {
			display = display[:80] + "..."
		}
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "same_tool_repeat",
			Count:       worst,
			Description: "one tool-call fingerprint was submitted ≥3 times in the turn",
			Examples:    []string{display},
		})
	}
}

func detectDocWander(rep *PerfReport, steps []toolStep) {
	// Heuristic: 3+ read_file calls on .md / README-ish paths with
	// <=1 read on source. Indicates the model answered from docs
	// instead of looking at the code.
	docs := 0
	src := 0
	var docExamples []string
	for _, s := range steps {
		if s.tool != "read_file" {
			continue
		}
		t := strings.ToLower(s.target)
		if strings.HasSuffix(t, ".md") || strings.Contains(t, "readme") || strings.HasSuffix(t, ".txt") {
			docs++
			if len(docExamples) < 3 {
				docExamples = append(docExamples, fmt.Sprintf("step %d: %s", s.index, s.target))
			}
		} else {
			src++
		}
	}
	if docs >= 3 && src <= 1 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "doc_wander",
			Count:       docs,
			Description: "3+ doc reads with ≤1 source read — the answer is coming from docs, not the code",
			Examples:    docExamples,
		})
	}
}

// detectMalformedEditArgs flags the specific failure shape that live
// logs surface: edit_file called with invalid line ranges (start > end)
// or missing old_text. Neither fails pre-execute guards (the request
// parses fine), but workspace execution errors out. Observed in a live
// run as a 60+ tool-call stall on a single doc-edit task.
func detectMalformedEditArgs(rep *PerfReport, records []Record) {
	needles := []string{
		"start_line must be",
		"old_text is required",
		"start_line/end_line",
	}
	count := 0
	var examples []string
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		tool, _ := rec.Data["tool"].(string)
		if tool != "edit_file" && tool != "write_file" && tool != "append_file" {
			continue
		}
		errMsg, _ := rec.Data["error"].(string)
		if errMsg == "" {
			continue
		}
		lowered := strings.ToLower(errMsg)
		matched := false
		for _, n := range needles {
			if strings.Contains(lowered, n) {
				matched = true
				break
			}
		}
		if matched {
			count++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("%s: %s", tool, firstLine(errMsg)))
			}
		}
	}
	if count > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "malformed_edit_args",
			Count:       count,
			Description: "edit_file failed with invalid line ranges or missing required fields — model's tool call shape was wrong, not a guard rejection",
			Examples:    examples,
		})
	}
}

// detectToolErrorStreak flags ≥3 consecutive tool_results with
// non-empty errors on the same (tool, path-stem). Distinct from
// rejection_retry: these are tool-execution failures, not guard
// rejections, and the model's args vary between attempts. The signal
// is "model is stuck trying to do something to X and failing each
// time with different broken inputs."
func detectToolErrorStreak(rep *PerfReport, steps []toolStep, records []Record) {
	// We need the path stem per step; derive from action target.
	var examples []string
	streaks := 0
	run := 0
	runStart := 0
	lastKey := ""
	for i, s := range steps {
		if !s.hasError || s.isReject {
			if run >= 3 {
				streaks++
				if len(examples) < 3 {
					examples = append(examples, fmt.Sprintf("steps %d-%d (%d failures on %s)", runStart, steps[i-1].index, run, lastKey))
				}
			}
			run = 0
			lastKey = ""
			continue
		}
		key := s.tool + "|" + pathStemOf(s.target)
		if key == lastKey && key != "" {
			run++
		} else {
			run = 1
			runStart = s.index
			lastKey = key
		}
	}
	if run >= 3 {
		streaks++
		if len(examples) < 3 {
			examples = append(examples, fmt.Sprintf("steps %d-%d (%d failures on %s)", runStart, steps[len(steps)-1].index, run, lastKey))
		}
	}
	if streaks > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "tool_error_streak",
			Count:       streaks,
			Description: "≥3 consecutive tool failures on the same tool+target — model is churning on invalid args or a stuck state",
			Examples:    examples,
		})
	}
}

func pathStemOf(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func detectRejectionRetry(rep *PerfReport, steps []toolStep) {
	// How often did a rejected tool get retried with identical args
	// immediately after? That's a pure waste signal: the model
	// ignored the rejection message.
	retries := 0
	var examples []string
	for i := 1; i < len(steps); i++ {
		if steps[i-1].isReject && steps[i].rawArgs == steps[i-1].rawArgs {
			retries++
			if len(examples) < 3 {
				examples = append(examples, fmt.Sprintf("step %d retried %s after rejection", steps[i].index, steps[i].tool))
			}
		}
	}
	if retries > 0 {
		rep.AntiPatterns = append(rep.AntiPatterns, AntiPattern{
			Name:        "rejection_retry",
			Count:       retries,
			Description: "rejected tool call was retried with identical arguments on the next step",
			Examples:    examples,
		})
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

// ---- aggregation ----

// PerfAggregate summarizes a slice of PerfReports. It's the lens used
// to find the next optimization target: which anti-pattern shows up in
// the most logs? Which tool dominates the mix? Median/max numbers
// surface tails without anecdotal cherry-picking.
type PerfAggregate struct {
	LogCount             int                    `json:"log_count"`
	MedianToolResults    int                    `json:"median_tool_results"`
	MaxToolResults       int                    `json:"max_tool_results"`
	MedianPromptTokens   int                    `json:"median_prompt_tokens"`
	MaxPromptTokens      int                    `json:"max_prompt_tokens"`
	MedianDurationMillis int                    `json:"median_duration_millis"`
	TotalRejectedActions int                    `json:"total_rejected_actions"`
	PatternHitRate       []PerfPatternHit       `json:"pattern_hit_rate"`
	ToolMix              []PerfToolMix          `json:"tool_mix"`
	StopReasonCounts     map[string]int         `json:"stop_reason_counts,omitempty"`
	WorstOffenders       []PerfWorstOffender    `json:"worst_offenders,omitempty"`
	Extras               map[string]interface{} `json:"extras,omitempty"`
}

type PerfPatternHit struct {
	Name       string  `json:"name"`
	LogsHit    int     `json:"logs_hit"`
	TotalCount int     `json:"total_count"`
	HitRate    float64 `json:"hit_rate"`
}

type PerfToolMix struct {
	Tool    string  `json:"tool"`
	Calls   int     `json:"calls"`
	Share   float64 `json:"share"`
	AvgPerLog float64 `json:"avg_per_log"`
}

type PerfWorstOffender struct {
	LogPath          string `json:"log_path"`
	SessionID        string `json:"session_id,omitempty"`
	AntiPatternScore int    `json:"anti_pattern_score"`
	ToolResults      int    `json:"tool_results"`
	UserPrompt       string `json:"user_prompt,omitempty"`
}

// Aggregate combines per-log reports into one summary.
func Aggregate(reports []PerfReport) PerfAggregate {
	agg := PerfAggregate{
		LogCount:         len(reports),
		StopReasonCounts: make(map[string]int),
	}
	if len(reports) == 0 {
		return agg
	}

	toolResults := make([]int, 0, len(reports))
	// Many older logs predate the budget_ledger event — they have no
	// token or duration data. Feeding zeros into the medians would
	// pull them toward zero and mask the actual distribution, so we
	// skip zeros for those two channels.
	promptTokens := make([]int, 0, len(reports))
	durations := make([]int, 0, len(reports))
	patternLogs := make(map[string]int)
	patternCounts := make(map[string]int)
	toolCalls := make(map[string]int)
	totalCalls := 0
	for _, r := range reports {
		toolResults = append(toolResults, r.ToolResults)
		if r.PromptTokens > 0 {
			promptTokens = append(promptTokens, r.PromptTokens)
		}
		if r.DurationMillis > 0 {
			durations = append(durations, r.DurationMillis)
		}
		agg.TotalRejectedActions += r.RejectedActions
		if r.StopReason != "" {
			agg.StopReasonCounts[r.StopReason]++
		}
		seen := make(map[string]bool)
		for _, ap := range r.AntiPatterns {
			patternCounts[ap.Name] += ap.Count
			if !seen[ap.Name] {
				seen[ap.Name] = true
				patternLogs[ap.Name]++
			}
		}
		for tool, calls := range r.Tools.ByName {
			toolCalls[tool] += calls
			totalCalls += calls
		}
	}
	agg.MedianToolResults = median(toolResults)
	agg.MaxToolResults = maxOf(toolResults)
	agg.MedianPromptTokens = median(promptTokens)
	agg.MaxPromptTokens = maxOf(promptTokens)
	agg.MedianDurationMillis = median(durations)

	for name, logsHit := range patternLogs {
		agg.PatternHitRate = append(agg.PatternHitRate, PerfPatternHit{
			Name:       name,
			LogsHit:    logsHit,
			TotalCount: patternCounts[name],
			HitRate:    float64(logsHit) / float64(len(reports)),
		})
	}
	sort.Slice(agg.PatternHitRate, func(i, j int) bool {
		return agg.PatternHitRate[i].LogsHit > agg.PatternHitRate[j].LogsHit
	})

	for tool, calls := range toolCalls {
		share := 0.0
		if totalCalls > 0 {
			share = float64(calls) / float64(totalCalls)
		}
		agg.ToolMix = append(agg.ToolMix, PerfToolMix{
			Tool:      tool,
			Calls:     calls,
			Share:     share,
			AvgPerLog: float64(calls) / float64(len(reports)),
		})
	}
	sort.Slice(agg.ToolMix, func(i, j int) bool {
		return agg.ToolMix[i].Calls > agg.ToolMix[j].Calls
	})

	// Worst-offender list: top 5 logs by anti-pattern score.
	offenders := append([]PerfReport(nil), reports...)
	sort.Slice(offenders, func(i, j int) bool {
		return offenders[i].AntiPatternScore > offenders[j].AntiPatternScore
	})
	keep := 5
	if len(offenders) < keep {
		keep = len(offenders)
	}
	for _, r := range offenders[:keep] {
		if r.AntiPatternScore == 0 {
			break
		}
		agg.WorstOffenders = append(agg.WorstOffenders, PerfWorstOffender{
			LogPath:          r.LogPath,
			SessionID:        r.SessionID,
			AntiPatternScore: r.AntiPatternScore,
			ToolResults:      r.ToolResults,
			UserPrompt:       r.UserPrompt,
		})
	}

	return agg
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func maxOf(values []int) int {
	best := 0
	for _, v := range values {
		if v > best {
			best = v
		}
	}
	return best
}
