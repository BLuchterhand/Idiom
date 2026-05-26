package localops

import (
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/eval"
)

func BuildReplayReport(logPath string, logsGlob string) (ReplayReport, error) {
	paths, err := replayPaths(logPath, logsGlob)
	if err != nil {
		return ReplayReport{}, err
	}
	if len(paths) == 0 {
		return ReplayReport{
			Status: "unsupported",
			Blockers: []Blocker{{
				Scope:  "replay",
				Reason: "no replay logs supplied",
				Remedy: "pass --replay-log <metadata-only conversation log> or --replay-glob for metadata-only replay signals",
			}},
		}, nil
	}
	reports := make([]eval.PerfReport, 0, len(paths))
	replay := ReplayReport{Status: "pass"}
	for _, path := range paths {
		report, err := eval.AnalyzeLog(path)
		if err != nil {
			replay.Status = "degraded"
			replay.Blockers = append(replay.Blockers, Blocker{
				Scope:  "replay/log_" + StableID(path),
				Reason: replayFailureReason(err),
				Remedy: SafeText("provide a readable, parseable JSONL conversation or telemetry log"),
			})
			continue
		}
		report.SessionID = StableID(path)
		reports = append(reports, report)
		replay.LogIDs = append(replay.LogIDs, StableID(path))
		replay.Signals.ToolCalls += report.Tools.Total
		replay.Signals.RepeatedToolSignals += antiPatternCount(report, "repeated")
		replay.Signals.MalformedToolSignals += antiPatternCount(report, "malformed")
		if strings.Contains(strings.ToLower(report.StopReason), "budget") {
			replay.Signals.BudgetExhausted++
		}
		if report.LocalOps != nil {
			replay.Signals.QueueDegraded += report.LocalOps.QueueDegraded
			replay.Signals.BackendEvents += report.LocalOps.BackendEvents
		}
	}
	if len(reports) == 0 {
		replay.Status = "blocked"
		replay.Blockers = append(replay.Blockers, Blocker{Scope: "replay", Reason: "no replay logs could be analyzed"})
		return replay, nil
	}
	aggregate := eval.Aggregate(reports)
	replay.Aggregate = SanitizeReplayAggregate(aggregate)
	replay.LogCount = len(reports)
	slices.Sort(replay.LogIDs)
	return replay, nil
}

func SanitizeReplayAggregate(aggregate eval.PerfAggregate) *ReplayAggregateReport {
	safe := &ReplayAggregateReport{
		LogCount:             aggregate.LogCount,
		MedianToolResults:    aggregate.MedianToolResults,
		MaxToolResults:       aggregate.MaxToolResults,
		MedianPromptTokens:   aggregate.MedianPromptTokens,
		MaxPromptTokens:      aggregate.MaxPromptTokens,
		MedianDurationMillis: aggregate.MedianDurationMillis,
		TotalRejectedActions: aggregate.TotalRejectedActions,
		PatternHitRate:       aggregate.PatternHitRate,
		ToolMix:              aggregate.ToolMix,
		Context:              aggregate.Context,
		Feedback:             aggregate.Feedback,
		LocalOps:             aggregate.LocalOps,
		StopReasonCounts:     aggregate.StopReasonCounts,
	}
	for _, offender := range aggregate.WorstOffenders {
		logID := strings.TrimSpace(offender.SessionID)
		if logID == "" {
			logID = StableID(offender.LogPath)
		}
		safe.WorstOffenders = append(safe.WorstOffenders, WorstOffender{
			LogID:              logID,
			AntiPatternScore:   offender.AntiPatternScore,
			ToolResults:        offender.ToolResults,
			LogExcerptRedacted: offender.LogExcerptRedacted,
		})
	}
	return safe
}

func replayFailureReason(err error) string {
	if err == nil {
		return "replay_log_unavailable"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "open log"), strings.Contains(message, "permission"), strings.Contains(message, "no such file"):
		return "replay_log_unreadable"
	case strings.Contains(message, "decode log record"), strings.Contains(message, "scan log"):
		return "replay_log_unparseable"
	default:
		return "replay_log_unavailable"
	}
}

func replayPaths(logPath string, logsGlob string) ([]string, error) {
	if strings.TrimSpace(logPath) != "" {
		return []string{filepath.Clean(logPath)}, nil
	}
	if strings.TrimSpace(logsGlob) == "" {
		return nil, nil
	}
	paths, err := filepath.Glob(logsGlob)
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	return paths, nil
}

func antiPatternCount(report eval.PerfReport, needle string) int {
	needle = strings.ToLower(strings.TrimSpace(needle))
	total := 0
	for _, pattern := range report.AntiPatterns {
		if strings.Contains(strings.ToLower(pattern.Name), needle) {
			total += pattern.Count
		}
	}
	return total
}
