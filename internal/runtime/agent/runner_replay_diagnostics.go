package agent

import (
	"idiom/internal/capabilities/workspace"
	"sort"
	"strconv"
	"strings"
)

func replayRepeatedToolRequest(req workspace.ToolRequest, source workspace.ToolResult) workspace.ToolResult {
	summary := strings.TrimSpace(source.Summary)
	if summary == "" {
		if strings.TrimSpace(source.Error) != "" {
			summary = "Repeated tool request replayed the same blocker"
		} else {
			summary = "Repeated tool request reused prior evidence"
		}
	} else if strings.TrimSpace(source.Error) != "" {
		summary += " (replayed same blocker)"
	} else {
		summary += " (replayed prior evidence)"
	}

	return workspace.ToolResult{
		Tool:    req.Tool,
		Summary: summary,
		Output:  source.Output,
		Error:   source.Error,
	}
}

func repeatedDiagnosticLogReadFileBackfill(req workspace.ToolRequest, results []workspace.ToolResult) (workspace.ToolRequest, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return workspace.ToolRequest{}, "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return workspace.ToolRequest{}, "", false
	}

	windows := diagnosticLogReadFileWindows(path, results)
	nextStart, nextEnd, ok := diagnosticLogNextUnreadPreviousWindow(windows)

	if !ok {
		return workspace.ToolRequest{}, "", false
	}

	rewritten := req

	rewritten.StartLine = nextStart
	rewritten.EndLine = nextEnd

	raw := marshalToolRequestRaw(rewritten)

	if raw == "" {
		return workspace.ToolRequest{}, "", false
	}

	return rewritten, raw, true
}

type diagnosticLogReadWindow struct {
	start int
	end   int
	total int
}

func diagnosticLogReadFileWindows(path string, results []workspace.ToolResult) []diagnosticLogReadWindow {
	path = normalizeRunnerPath(path)
	if path == "" {
		return nil
	}

	var windows []diagnosticLogReadWindow

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if !runnerToolResultReadPathMatches(result, path) {
			continue
		}

		start, end, total, ok := readFileSummaryLineWindow(result.Summary)
		if !ok || start <= 0 || end < start || total <= 0 {
			continue
		}

		windows = append(windows, diagnosticLogReadWindow{start: start, end: end, total: total})
	}

	return windows
}

func diagnosticLogUnboundedReadCoverageRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine > 0 || req.EndLine > 0 {
		return "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}

	if !diagnosticLogWindowsCoverContiguousLog(diagnosticLogReadFileWindows(path, results)) {
		return "", "", false
	}

	return "Diagnostic log read coverage exhausted",
		"this diagnostic log has already been read back to line 1 and includes the current tail window. Do not replay the unbounded read. Answer from the gathered log evidence, or request one different bounded `start_line`/`end_line` window only if you can name the exact missing event range.",
		true
}

func diagnosticLogNextUnreadPreviousWindow(windows []diagnosticLogReadWindow) (int, int, bool) {
	merged, total := mergeDiagnosticLogReadWindows(windows)
	if len(merged) == 0 || total <= 0 {
		return 0, 0, false
	}

	tailIndex := -1

	for i := len(merged) - 1; i >= 0; i-- {
		if merged[i].end >= total {
			tailIndex = i
			break
		}
	}

	if tailIndex < 0 {
		return 0, 0, false
	}

	tail := merged[tailIndex]
	gapEnd := tail.start - 1

	if gapEnd < 1 {
		return 0, 0, false
	}

	windowSize := tail.end - tail.start + 1
	if windowSize <= 0 {
		return 0, 0, false
	}

	lowerBound := 1
	if tailIndex > 0 {
		lowerBound = merged[tailIndex-1].end + 1
	}

	gapStart := gapEnd - windowSize + 1
	if gapStart < lowerBound {
		gapStart = lowerBound
	}

	if gapStart < 1 {
		gapStart = 1
	}

	if gapStart > gapEnd {
		return 0, 0, false
	}

	return gapStart, gapEnd, true
}

func diagnosticLogWindowsCoverContiguousLog(windows []diagnosticLogReadWindow) bool {
	merged, total := mergeDiagnosticLogReadWindows(windows)
	if total <= 0 {
		return false
	}

	for _, window := range merged {
		if window.start <= 1 && window.end >= total {
			return true
		}
	}

	return false
}

func mergeDiagnosticLogReadWindows(windows []diagnosticLogReadWindow) ([]diagnosticLogReadWindow, int) {
	if len(windows) == 0 {
		return nil, 0
	}

	total := 0
	filtered := make([]diagnosticLogReadWindow, 0, len(windows))

	for _, window := range windows {
		if window.start <= 0 || window.end < window.start || window.total <= 0 {
			continue
		}

		if window.total > total {
			total = window.total
		}

		filtered = append(filtered, window)
	}

	if len(filtered) == 0 || total <= 0 {
		return nil, total
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].start == filtered[j].start {
			return filtered[i].end < filtered[j].end
		}

		return filtered[i].start < filtered[j].start
	})

	merged := make([]diagnosticLogReadWindow, 0, len(filtered))
	for _, window := range filtered {
		if window.end > total {
			window.end = total
		}

		if len(merged) == 0 || window.start > merged[len(merged)-1].end+1 {
			merged = append(merged, diagnosticLogReadWindow{start: window.start, end: window.end, total: total})
			continue
		}

		if window.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = window.end
		}

		merged[len(merged)-1].total = total
	}

	return merged, total
}

func runnerToolResultReadPathMatches(result workspace.ToolResult, path string) bool {
	for _, readPath := range readPathsFromToolResult(result) {
		if normalizeRunnerPath(readPath) == path {
			return true
		}
	}

	return false
}

func readFileSummaryLineWindow(summary string) (int, int, int, bool) {
	match := readFileSummaryLineWindowPattern.FindStringSubmatch(summary)
	if len(match) != 3 {
		return 0, 0, 0, false
	}

	totalMatch := readFileSummaryTotalLinesPattern.FindStringSubmatch(summary)
	if len(totalMatch) != 3 {
		return 0, 0, 0, false
	}

	start, errStart := strconv.Atoi(match[1])
	end, errEnd := strconv.Atoi(match[2])
	totalText := totalMatch[1]

	if totalText == "" {
		totalText = totalMatch[2]
	}

	total, errTotal := strconv.Atoi(totalText)
	if errStart != nil || errEnd != nil || errTotal != nil {
		return 0, 0, 0, false
	}

	return start, end, total, true
}

func repeatedDiagnosticLogReadFileExactRejection(req workspace.ToolRequest, last workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile {
		return "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}

	if strings.TrimSpace(last.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(last.Error) != "" {
		return "", "", false
	}

	for _, readPath := range readPathsFromToolResult(last) {
		if normalizeRunnerPath(readPath) != path {
			continue
		}

		return "Repeated diagnostic log read rejected",
			"this exact diagnostic log read already returned evidence. Do not request the same log window again. If a missing fact remains, request a different bounded `start_line`/`end_line` window tied to that missing timestamp, event, or stop reason; inspect a named source path only if the log evidence points to it; otherwise answer from the gathered evidence.",
			true
	}

	return "", "", false
}

func repeatedDiagnosticLogReadFilePreviouslyReadRejection(req workspace.ToolRequest, results []workspace.ToolResult) (string, string, bool) {
	if strings.TrimSpace(req.Tool) != workspace.ToolNameReadFile || req.StartLine <= 0 || req.EndLine < req.StartLine {
		return "", "", false
	}

	path := normalizeRunnerPath(req.Path)
	if path == "" || !isDiagnosticLogArtifactPath(path) {
		return "", "", false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile || strings.TrimSpace(result.Error) != "" {
			continue
		}

		if !runnerToolResultReadPathMatches(result, path) {
			continue
		}

		start, end, _, ok := readFileSummaryLineWindow(result.Summary)
		if !ok || start != req.StartLine || end != req.EndLine {
			continue
		}

		return "Repeated diagnostic log window rejected",
			"this bounded diagnostic log window already returned evidence earlier in the run. Do not request the same `start_line`/`end_line` window again. If a missing fact remains, request a different bounded window tied to the exact missing event, inspect a named source path pointed to by the log, or answer from the gathered evidence.",
			true
	}

	return "", "", false
}
