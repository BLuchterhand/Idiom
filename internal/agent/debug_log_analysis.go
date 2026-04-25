package agent

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/eval"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

func structuredDebugLogAnswer(mgr *workspace.Manager, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (string, bool) {
	if strings.TrimSpace(brief.TaskFamily) != "debug_logs" {
		return "", false
	}
	displayPath, absPath, ok := selectedConversationLogPath(mgr, results)
	if !ok {
		return "", false
	}
	report, err := eval.AnalyzeLog(absPath)
	if err != nil {
		return "", false
	}
	if report.ModelCalls == 0 && report.ToolResults == 0 && strings.TrimSpace(report.UserPrompt) == "" {
		return "", false
	}
	records, err := eval.LoadRecords(absPath)
	if err != nil {
		return "", false
	}
	return buildStructuredDebugLogAnswer(displayPath, report, records), true
}

func selectedConversationLogPath(mgr *workspace.Manager, results []workspace.ToolResult) (string, string, bool) {
	selection, ok := latestDebugLogSelection(results)
	if !ok {
		return "", "", false
	}
	displayPath := strings.TrimSpace(selection.Path)
	if displayPath == "" {
		return "", "", false
	}
	lower := strings.ToLower(filepath.ToSlash(displayPath))
	if !strings.HasSuffix(lower, ".jsonl") || !strings.Contains(lower, "/conversations/") {
		return "", "", false
	}
	if filepath.IsAbs(displayPath) {
		return displayPath, displayPath, true
	}
	if mgr == nil || strings.TrimSpace(mgr.Root()) == "" {
		return "", "", false
	}
	return displayPath, filepath.Join(strings.TrimSpace(mgr.Root()), filepath.FromSlash(displayPath)), true
}

func buildStructuredDebugLogAnswer(displayPath string, report eval.PerfReport, records []eval.Record) string {
	findings := debugLogFindings(report, records)
	if len(findings) == 0 {
		findings = append(findings, "the selected conversation log is structured and analyzable, but the runner still fell back to raw log walking instead of synthesizing from that structure.")
	}
	recommendations := debugLogRecommendations(report, records)

	var b strings.Builder
	b.WriteString("Yes. The logs show the harness can be made smarter.\n\n")
	b.WriteString("Most recent log checked: `")
	b.WriteString(displayPath)
	b.WriteString("`.\n\n")
	b.WriteString("What looks off:\n")
	for _, finding := range findings {
		b.WriteString("- ")
		b.WriteString(finding)
		b.WriteString("\n")
	}
	if len(recommendations) > 0 {
		b.WriteString("\nWhat to change next:\n")
		for _, recommendation := range recommendations {
			b.WriteString("- ")
			b.WriteString(recommendation)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func debugLogFindings(report eval.PerfReport, records []eval.Record) []string {
	findings := make([]string, 0, 4)
	if report.ToolResults > 0 && report.Tools.ReadFile >= 2 && report.Tools.ReadFile*2 >= report.ToolResults {
		findings = append(findings, fmt.Sprintf("raw `read_file` inspection dominated the turn (%d of %d tool steps), so the agent kept walking the log instead of narrowing or synthesizing once the newest log was selected.", report.Tools.ReadFile, report.ToolResults))
	}
	if summary, count := topDebugLogError(records); count >= 2 {
		findings = append(findings, fmt.Sprintf("it repeated the same failing tool shape %d times (`%s`), which means malformed or low-value requests were allowed to keep spending budget.", count, summary))
	}
	if taskCalls := report.Tools.ByName["task"]; taskCalls > 0 {
		findings = append(findings, "it delegated to a child task during a read-only log investigation, then still had to recover locally; that orchestration cost is too high for this lane.")
	}
	if chosen, durationMs := longestDebugLogModelCall(records); durationMs >= 30000 {
		if chosen == "<answer>" {
			findings = append(findings, fmt.Sprintf("the forced final answer itself took about %d ms, so the harness paid a large latency tax after it was already stuck.", durationMs))
		} else {
			findings = append(findings, fmt.Sprintf("one `%s` model turn alone took about %d ms, which is a sign the loop stayed open longer than it should have.", chosen, durationMs))
		}
	}
	if stopReason := debugLogStopReason(report, records); stopReason != "" && stopReason != "model_final" {
		findings = append(findings, fmt.Sprintf("the turn ended at `%s`, so the harness only cut over after the loop had already exhausted meaningful budget.", stopReason))
	}
	if len(findings) > 5 {
		findings = findings[:5]
	}
	return findings
}

func debugLogRecommendations(report eval.PerfReport, records []eval.Record) []string {
	recommendations := []string{
		"Once the newest conversation log is selected, prefer structured log analysis over repeated raw `read_file` windows of that same file.",
		"After the first selected-log tail, allow a targeted `search_content` inside that log or answer directly; block more blind rereads until there is a concrete hit.",
	}
	if summary, count := topDebugLogError(records); count >= 2 || (debugLogStopReason(report, records) != "" && debugLogStopReason(report, records) != "model_final") {
		recommendations = append(recommendations, fmt.Sprintf("Escalate repeated tool-shape failures like `%s` and repeated lane-budget stops directly to synthesis instead of paying for another long completion attempt.", summary))
	}
	return recommendations
}

func debugLogStopReason(report eval.PerfReport, records []eval.Record) string {
	if stopReason := strings.TrimSpace(report.StopReason); stopReason != "" {
		return stopReason
	}
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Type != "turn_summary" {
			continue
		}
		stopReason, _ := rec.Data["stop_reason"].(string)
		if strings.TrimSpace(stopReason) != "" {
			return strings.TrimSpace(stopReason)
		}
	}
	return ""
}

func topDebugLogError(records []eval.Record) (string, int) {
	counts := make(map[string]int)
	for _, rec := range records {
		if rec.Type != "tool_result" {
			continue
		}
		errText, _ := rec.Data["error"].(string)
		if strings.TrimSpace(errText) == "" {
			continue
		}
		summary, _ := rec.Data["summary"].(string)
		summary = strings.TrimSpace(summary)
		if summary == "" {
			summary = firstDebugLogLine(errText)
		}
		counts[summary]++
	}
	type pair struct {
		Summary string
		Count   int
	}
	pairs := make([]pair, 0, len(counts))
	for summary, count := range counts {
		pairs = append(pairs, pair{Summary: summary, Count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count == pairs[j].Count {
			return pairs[i].Summary < pairs[j].Summary
		}
		return pairs[i].Count > pairs[j].Count
	})
	if len(pairs) == 0 {
		return "", 0
	}
	return pairs[0].Summary, pairs[0].Count
}

func longestDebugLogModelCall(records []eval.Record) (string, int) {
	chosen := ""
	longest := 0
	for _, rec := range records {
		if rec.Type != "model_call" {
			continue
		}
		currentChosen, _ := rec.Data["chosen_tool"].(string)
		duration := evalRecordInt(rec.Data["duration_ms"])
		if duration > longest {
			longest = duration
			chosen = strings.TrimSpace(currentChosen)
		}
	}
	return chosen, longest
}

func evalRecordInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func firstDebugLogLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		value = value[:idx]
	}
	if len(value) > 160 {
		value = value[:157] + "..."
	}
	return value
}
