// idiom-perf-eval reads a conversation JSONL log (or many via glob) and
// emits efficiency metrics: per-turn tool counts, token/duration cost,
// and a short list of anti-patterns observed. With -aggregate it rolls
// up across all matched logs to surface the highest-frequency slack.
//
// The point is to make optimization work evidence-driven: run the
// behavior suite, pipe the logs through this tool, and the next harness
// change targets whichever anti-pattern tops the hit-rate list.
package perfeval

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"idiom/internal/eval"
)

func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr)
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
		slices.Sort(paths)
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
		fmt.Fprintln(w, "   user:         [redacted]")
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
	if r.Context != nil {
		printContextStats(r.Context, "   ", w)
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

	if a.Context != nil {
		fmt.Fprintln(w)
		printContextStats(a.Context, "", w)
	}
	if a.Feedback != nil {
		fmt.Fprintln(w)
		printFeedbackStats(a.Feedback, "", w)
	}

	if len(a.WorstOffenders) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "worst offenders (by anti-pattern score, top %d):\n", topN)
		for _, off := range a.WorstOffenders {
			line := fmt.Sprintf("  score=%d  tools=%d  %s", off.AntiPatternScore, off.ToolResults, firstNonEmpty(off.SessionID, off.LogPath))
			if off.LogExcerptRedacted {
				line += " -- prompt excerpt redacted"
			}
			fmt.Fprintln(w, line)
		}
	}
}

func printFeedbackStats(feedback *eval.FeedbackStats, indent string, w io.Writer) {
	if feedback == nil {
		return
	}
	fmt.Fprintf(w, "%sfeedback:         total=%d verified=%d inferred=%d evidence_items=%d severity(high=%d medium=%d low=%d)\n",
		indent, feedback.Total, feedback.Verified, feedback.Inferred, feedback.EvidenceItems, feedback.HighSeverity, feedback.MediumSeverity, feedback.LowSeverity)
	printCountMap(w, indent+"feedback cats:   ", feedback.ByCategory)
	printCountMap(w, indent+"feedback comps:  ", feedback.ByComponent)
}

func printContextStats(ctx *eval.ContextStats, indent string, w io.Writer) {
	if ctx == nil {
		return
	}
	fmt.Fprintf(w, "%scontext graph:    results=%d indexed=%d fresh=%d stale=%d incompatible=%d trace_failures=%d\n",
		indent, ctx.ToolResults, ctx.IndexedResults, ctx.FreshResults, ctx.StaleResults, ctx.IncompatibleBackendResults, ctx.TraceFailures)
	fmt.Fprintf(w, "%scontext size:     max_files=%d max_symbols=%d max_edges=%d stale_symbols=%d refreshed_stale_symbols=%d stale_samples=%d\n",
		indent, ctx.MaxFilesIndexed, ctx.MaxSymbolsIndexed, ctx.MaxEdgesIndexed, ctx.MaxStaleSymbols, ctx.RefreshedStaleSymbols, ctx.StaleSymbolSamples)
	fmt.Fprintf(w, "%scontext refresh:  incremental=%d reused_files=%d dependency_refresh_files=%d auto=%d skipped=%d failed=%d fresh_after=%d files_indexed=%d refreshed_files=%d\n",
		indent, ctx.IncrementalRefreshes, ctx.ReusedIndexedFiles, ctx.DependencyRefreshFiles, ctx.AutoRefreshAttempts, ctx.AutoRefreshSkips, ctx.AutoRefreshFailures, ctx.AutoRefreshFreshAfter, ctx.AutoRefreshFilesIndexed, ctx.AutoRefreshRefreshedFiles)
	fmt.Fprintf(w, "%scontext query:    nodes=%d edges=%d summary_nodes=%d model_summary_nodes=%d candidates=%d/%d tests=%d gaps=%d\n",
		indent, ctx.NodesReturned, ctx.EdgesReturned, ctx.SummaryNodesReturned, ctx.ModelSummaryNodesReturned, ctx.CandidateFiles, ctx.CandidateSymbols, ctx.TestsSuggested, ctx.UnresolvedGaps)
	fmt.Fprintf(w, "%scontext collect:  reports=%d coverage=%d unavailable=%d warnings=%d version_mismatches=%d diagnostics=%d normalized_relationship_edges=%d\n",
		indent, ctx.CollectorReports, ctx.CollectorCoverage, ctx.CollectorUnavailable, ctx.CollectorWarnings, ctx.CollectorVersionMismatches, ctx.EnrichmentDiagnostics, ctx.NormalizedRelationshipEdges)
	printCountMap(w, indent+"context stores:   ", ctx.StoreKindCounts)
	printCountMap(w, indent+"context active:   ", ctx.ActiveBackendCounts)
	printCountMap(w, indent+"context reports:  ", ctx.CollectorReportNameCounts)
}

func printCountMap(w io.Writer, label string, values map[string]int) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if values[keys[i]] != values[keys[j]] {
			return values[keys[i]] > values[keys[j]]
		}
		return keys[i] < keys[j]
	})
	fmt.Fprint(w, label)
	for i, key := range keys {
		if i > 0 {
			fmt.Fprint(w, ", ")
		}
		fmt.Fprintf(w, "%s=%d", key, values[key])
	}
	fmt.Fprintln(w)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
