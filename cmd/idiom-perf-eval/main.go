// idiom-perf-eval reads a conversation JSONL log (or many via glob) and
// emits efficiency metrics: per-turn tool counts, token/duration cost,
// and a short list of anti-patterns observed. With -aggregate it rolls
// up across all matched logs to surface the highest-frequency slack.
//
// The point is to make optimization work evidence-driven: run the
// behavior suite, pipe the logs through this tool, and the next harness
// change targets whichever anti-pattern tops the hit-rate list.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/eval"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var (
		logPath   string
		logsGlob  string
		jsonOut   bool
		aggregate bool
		topN      int
	)
	fs := flag.NewFlagSet("idiom-perf-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&logPath, "log", "", "path to a single JSONL conversation log")
	fs.StringVar(&logsGlob, "logs-glob", "", "glob pattern matching multiple logs (e.g. logs/conversations/*.jsonl)")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	fs.BoolVar(&aggregate, "aggregate", false, "with --logs-glob: also print an aggregate summary across all logs")
	fs.IntVar(&topN, "top", 10, "how many worst-offender logs / top tools / top patterns to print")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if logPath == "" && logsGlob == "" {
		fmt.Fprintln(stderr, "pass --log PATH or --logs-glob PATTERN")
		return 2
	}

	var reports []eval.PerfReport
	if logPath != "" {
		r, err := eval.AnalyzeLog(logPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		r.SessionID = sessionIDFromPath(logPath)
		reports = append(reports, r)
	} else {
		paths, err := filepath.Glob(logsGlob)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(paths) == 0 {
			fmt.Fprintf(stderr, "no files matched %q\n", logsGlob)
			return 1
		}
		sort.Strings(paths)
		for _, p := range paths {
			r, err := eval.AnalyzeLog(p)
			if err != nil {
				fmt.Fprintf(stderr, "analyze %s: %v\n", p, err)
				continue
			}
			r.SessionID = sessionIDFromPath(p)
			reports = append(reports, r)
		}
	}

	if jsonOut {
		return emitJSON(reports, aggregate, stdout)
	}
	return emitText(reports, aggregate, topN, stdout)
}

func sessionIDFromPath(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func emitJSON(reports []eval.PerfReport, aggregate bool, w io.Writer) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if aggregate {
		payload := struct {
			Reports   []eval.PerfReport  `json:"reports"`
			Aggregate eval.PerfAggregate `json:"aggregate"`
		}{reports, eval.Aggregate(reports)}
		if err := enc.Encode(payload); err != nil {
			return 1
		}
		return 0
	}
	if len(reports) == 1 {
		if err := enc.Encode(reports[0]); err != nil {
			return 1
		}
		return 0
	}
	if err := enc.Encode(reports); err != nil {
		return 1
	}
	return 0
}

func emitText(reports []eval.PerfReport, aggregate bool, topN int, w io.Writer) int {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(w)
		}
		printReport(r, w)
	}
	if aggregate && len(reports) > 1 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "=============== AGGREGATE ===============")
		printAggregate(eval.Aggregate(reports), topN, w)
	}
	return 0
}

func printReport(r eval.PerfReport, w io.Writer) {
	fmt.Fprintf(w, "── %s\n", firstNonEmpty(r.SessionID, r.LogPath))
	if r.UserPrompt != "" {
		fmt.Fprintf(w, "   user:         %s\n", trunc(r.UserPrompt, 120))
	}
	fmt.Fprintf(w, "   tool results: %d  (rejected %d)\n", r.ToolResults, r.RejectedActions)
	if r.ModelCalls > 0 {
		fmt.Fprintf(w, "   model calls:  %d   prompt_tok=%d  completion_tok=%d  duration=%dms   stop=%s\n",
			r.ModelCalls, r.PromptTokens, r.CompletionTokens, r.DurationMillis, r.StopReason)
	}
	fmt.Fprintf(w, "   tool mix:     list=%d read=%d search_content=%d search_files=%d go=%d cpp=%d edit=%d shell=%d other=%d\n",
		r.Tools.ListFiles, r.Tools.ReadFile, r.Tools.SearchContent, r.Tools.SearchFiles,
		r.Tools.GoSemantic, r.Tools.CppSemantic, r.Tools.Edit, r.Tools.Shell, r.Tools.Other)
	if r.FinalAnswerChars > 0 {
		fmt.Fprintf(w, "   final answer: %d chars\n", r.FinalAnswerChars)
	}
	if len(r.AntiPatterns) == 0 {
		fmt.Fprintln(w, "   anti-patterns: none detected")
		return
	}
	fmt.Fprintf(w, "   anti-patterns (score=%d):\n", r.AntiPatternScore)
	for _, ap := range r.AntiPatterns {
		fmt.Fprintf(w, "     • %s ×%d — %s\n", ap.Name, ap.Count, ap.Description)
		for _, ex := range ap.Examples {
			fmt.Fprintf(w, "         %s\n", ex)
		}
	}
}

func printAggregate(a eval.PerfAggregate, topN int, w io.Writer) {
	fmt.Fprintf(w, "logs analyzed:      %d\n", a.LogCount)
	fmt.Fprintf(w, "tool results:       median=%d  max=%d\n", a.MedianToolResults, a.MaxToolResults)
	fmt.Fprintf(w, "prompt tokens:      median=%d  max=%d\n", a.MedianPromptTokens, a.MaxPromptTokens)
	fmt.Fprintf(w, "duration (ms):      median=%d\n", a.MedianDurationMillis)
	fmt.Fprintf(w, "total rejections:   %d across %d logs\n", a.TotalRejectedActions, a.LogCount)
	if len(a.StopReasonCounts) > 0 {
		fmt.Fprintf(w, "stop reasons:       ")
		// stable sort by count desc
		keys := make([]string, 0, len(a.StopReasonCounts))
		for k := range a.StopReasonCounts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return a.StopReasonCounts[keys[i]] > a.StopReasonCounts[keys[j]] })
		for i, k := range keys {
			if i > 0 {
				fmt.Fprint(w, ", ")
			}
			fmt.Fprintf(w, "%s=%d", k, a.StopReasonCounts[k])
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "anti-pattern hit rate (top %d):\n", topN)
	if len(a.PatternHitRate) == 0 {
		fmt.Fprintln(w, "  none detected")
	}
	limit := topN
	if limit > len(a.PatternHitRate) {
		limit = len(a.PatternHitRate)
	}
	for _, p := range a.PatternHitRate[:limit] {
		fmt.Fprintf(w, "  %-28s logs=%d/%d (%.0f%%)   total_count=%d\n",
			p.Name, p.LogsHit, a.LogCount, p.HitRate*100, p.TotalCount)
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "tool mix (top %d):\n", topN)
	limit = topN
	if limit > len(a.ToolMix) {
		limit = len(a.ToolMix)
	}
	for _, t := range a.ToolMix[:limit] {
		fmt.Fprintf(w, "  %-24s %5d calls  (%.1f%% share, avg %.2f per log)\n",
			t.Tool, t.Calls, t.Share*100, t.AvgPerLog)
	}

	if len(a.WorstOffenders) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "worst offenders (by anti-pattern score, top %d):\n", topN)
		for _, off := range a.WorstOffenders {
			line := fmt.Sprintf("  score=%d  tools=%d  %s", off.AntiPatternScore, off.ToolResults, firstNonEmpty(off.SessionID, off.LogPath))
			if off.UserPrompt != "" {
				line += "   — " + trunc(off.UserPrompt, 80)
			}
			fmt.Fprintln(w, line)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
