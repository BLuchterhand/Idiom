package workspace

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/textutil"
)

const ToolNamePrefetch = "prefetch"

const (
	DefaultPrefetchMaxFiles        = 6
	DefaultPrefetchMaxBytes        = 160 * 1024
	DefaultPrefetchMaxBytesPerFile = 64 * 1024
	DefaultPrefetchMaxExcerptBytes = 8 * 1024
)

type PrefetchReadOptions struct {
	MaxFiles        int
	MaxBytes        int
	MaxBytesPerFile int
	MaxExcerptBytes int
}

type PrefetchReadReport struct {
	Results                  []ToolResult
	SelectedPaths            []string
	SkippedPaths             []PrefetchSkippedPath
	RequestedPaths           int
	DuplicatePaths           int
	Bytes                    int
	Lines                    int
	TruncatedFiles           int
	EstimatedRoundTripsSaved int
}

type PrefetchSkippedPath struct {
	Path   string
	Reason string
}

func (m *Manager) PrefetchReadFiles(ctx context.Context, paths []string, opts PrefetchReadOptions) PrefetchReadReport {
	report := PrefetchReadReport{RequestedPaths: len(paths)}
	if m == nil || !m.Enabled() {
		for _, path := range paths {
			report.SkippedPaths = append(report.SkippedPaths, PrefetchSkippedPath{Path: path, Reason: "workspace_unavailable"})
		}

		return report
	}

	opts = m.normalizePrefetchReadOptions(opts)
	ctx = m.snapshotExecutionSession(ctx)

	seen := map[string]struct{}{}

	for _, requested := range paths {
		requested = strings.TrimSpace(requested)
		if requested == "" {
			continue
		}

		if len(report.Results) >= opts.MaxFiles {
			report.SkippedPaths = append(report.SkippedPaths, PrefetchSkippedPath{Path: requested, Reason: "max_files_exceeded"})
			continue
		}

		if report.Bytes >= opts.MaxBytes {
			report.SkippedPaths = append(report.SkippedPaths, PrefetchSkippedPath{Path: requested, Reason: "max_bytes_exceeded"})
			continue
		}

		result, selectedPath, bytesRead, linesRead, truncated, skip := m.prefetchReadFile(ctx, requested, opts, opts.MaxBytes-report.Bytes)
		if skip.Reason != "" {
			report.SkippedPaths = append(report.SkippedPaths, skip)
			continue
		}

		if _, ok := seen[selectedPath]; ok {
			report.DuplicatePaths++
			continue
		}

		seen[selectedPath] = struct{}{}

		report.Results = append(report.Results, result)
		report.SelectedPaths = append(report.SelectedPaths, selectedPath)

		report.Bytes += bytesRead
		report.Lines += linesRead

		if truncated {
			report.TruncatedFiles++
		}
	}

	if len(report.Results) > 0 {
		// Conservative lower bound: N prefetched files replace the serial
		// "read file 1 ... read file N" tool-acquisition sequence with one
		// initial evidence packet. Actual savings may be N when the model can
		// answer immediately from prefetch, but N-1 avoids overstating cases
		// where one synthesis turn is still required.
		report.EstimatedRoundTripsSaved = len(report.Results) - 1
		if report.EstimatedRoundTripsSaved < 0 {
			report.EstimatedRoundTripsSaved = 0
		}
	}

	return report
}

func (m *Manager) normalizePrefetchReadOptions(opts PrefetchReadOptions) PrefetchReadOptions {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = DefaultPrefetchMaxFiles
	}

	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultPrefetchMaxBytes
	}

	if opts.MaxBytesPerFile <= 0 {
		opts.MaxBytesPerFile = DefaultPrefetchMaxBytesPerFile
	}

	if opts.MaxExcerptBytes <= 0 {
		opts.MaxExcerptBytes = DefaultPrefetchMaxExcerptBytes
	}

	if m != nil && m.cfg.MaxFileBytes > 0 && opts.MaxBytesPerFile > m.cfg.MaxFileBytes {
		opts.MaxBytesPerFile = m.cfg.MaxFileBytes
	}

	if opts.MaxBytesPerFile > opts.MaxBytes {
		opts.MaxBytesPerFile = opts.MaxBytes
	}

	if opts.MaxExcerptBytes > opts.MaxBytesPerFile {
		opts.MaxExcerptBytes = opts.MaxBytesPerFile
	}

	return opts
}

func (m *Manager) prefetchReadFile(ctx context.Context, requested string, opts PrefetchReadOptions, remainingBytes int) (ToolResult, string, int, int, bool, PrefetchSkippedPath) {
	mutationEpoch := m.MutationEpoch()
	snapshot, err := m.SnapshotWorkspace(ctx, WorkspaceSnapshotRequest{
		Purpose: WorkspaceSnapshotSourceWindow,
		Paths:   []string{requested},
	})
	if err != nil || len(snapshot.Entries) != 1 {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: requested, Reason: "resolve_failed"}
	}
	entry := snapshot.Entries[0]
	displayPath := entry.Identity.CanonicalID
	description := m.handleDisplay(entry.Identity.AbsolutePath)
	kind := classifySearchFilePath(displayPath)

	switch kind {
	case searchFileKindLog, searchFileKindGenerated:
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "excluded_" + string(kind)}
	}

	if entry.Kind != WorkspaceSnapshotFile {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "read_failed"}
	}

	if opts.MaxBytesPerFile <= 0 || opts.MaxExcerptBytes <= 0 || remainingBytes <= 0 {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "max_bytes_exceeded"}
	}

	sampleLimit := opts.MaxBytesPerFile
	if remainingBytes < sampleLimit {
		sampleLimit = remainingBytes
	}

	fullBody := entry.Body()
	data := fullBody
	if len(data) > sampleLimit {
		data = data[:sampleLimit]
	}

	if bytes.IndexByte(data, 0) >= 0 {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "binary"}
	}

	originalLines := len(textutil.SplitWorkspaceFileLines(fullBody))

	excerptLimit := prefetchExcerptLimit(int64(len(fullBody)), opts, remainingBytes, len(data))

	excerpt := data
	if len(excerpt) > excerptLimit {
		excerpt = excerpt[:excerptLimit]
	}

	outputBytes := len(excerpt)
	truncated := outputBytes < len(fullBody)
	lines := textutil.SplitWorkspaceFileLines(excerpt)

	var b strings.Builder
	lineContentTruncated := false
	hasCarriageReturn := false
	exposedLines := make([]string, 0, len(lines))

	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		hasCarriageReturn = hasCarriageReturn || strings.HasSuffix(line, "\r")
		if truncated {
			line = textutil.TruncateBytes(trimmed, defaultLogLineCharCap, lineTruncationMarker)
			lineContentTruncated = lineContentTruncated || line != trimmed
		} else {
			line = trimmed
		}
		exposedLines = append(exposedLines, line)

		fmt.Fprintf(&b, "%d:%s\n", i+1, line)
	}

	startLine := 1
	endLine := len(lines)
	if originalLines == 0 {
		startLine = 0
	}
	summary := fmt.Sprintf("Read %s lines %d-%d (total lines: %d; source: prefetch)", description, startLine, endLine, originalLines)
	if truncated {
		summary = fmt.Sprintf("Read %s lines %d-%d (excerpt lines: %d; total lines: %d; bytes: %d/%d; source: prefetch; truncated=true)", description, startLine, endLine, len(lines), originalLines, outputBytes, len(fullBody))
		if originalLines > len(lines) {
			fmt.Fprintf(&b, "\n[prefetch excerpt truncated: omitted lines %d-%d; full file not included in prefetch]", len(lines)+1, originalLines)
		} else {
			fmt.Fprintf(&b, "\n[prefetch excerpt truncated: omitted content after line %d; full file not included in prefetch]", len(lines))
		}
	}

	m.MarkPathObserved(displayPath)

	if mutationEpoch != m.MutationEpoch() {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "stale_generation"}
	}
	result := ToolResult{
		Tool:    ToolNamePrefetch,
		Summary: summary,
		Output:  strings.TrimRight(b.String(), "\n"),
		Paths:   []string{displayPath},
	}
	evidence := toolfacts.SourceWindowEvidence{
		CanonicalID: entry.Identity.CanonicalID, StartLine: startLine, EndLine: endLine, TotalLines: originalLines,
		Truncated: truncated, RevisionSHA256: entry.Evidence.DigestSHA256,
		WindowSHA256: toolfacts.SourceWindowSHA256([]byte(strings.Join(exposedLines, "\n"))), MutationEpoch: mutationEpoch,
	}
	evidence.Authority = toolfacts.SourceWindowAuthorityEvidence{
		PolicySubject:  entry.Metadata.PolicySubject,
		PolicyDecision: entry.Metadata.PolicyDecision,
	}
	cutInsideLine := truncated && len(excerpt) > 0 && excerpt[len(excerpt)-1] != '\n'
	if !lineContentTruncated && !hasCarriageReturn && !cutInsideLine {
		window, windowErr := NewWorkspaceSourceWindow(evidence, strings.Join(exposedLines, "\n"))
		if windowErr != nil {
			return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "invalid_source_window"}
		}
		attachWorkspaceSourceWindows(&result, window)
	}
	return result, displayPath, outputBytes, len(lines), truncated, PrefetchSkippedPath{}
}

func prefetchExcerptLimit(fileSize int64, opts PrefetchReadOptions, remainingBytes int, bytesRead int) int {
	limit := opts.MaxExcerptBytes
	if fileSize <= int64(opts.MaxBytesPerFile) && fileSize <= int64(remainingBytes) {
		limit = opts.MaxBytesPerFile
	}

	if remainingBytes < limit {
		limit = remainingBytes
	}

	if bytesRead < limit {
		limit = bytesRead
	}

	return limit
}
