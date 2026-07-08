package workspace

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
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

func (m *Manager) PrefetchReadFiles(paths []string, opts PrefetchReadOptions) PrefetchReadReport {
	report := PrefetchReadReport{RequestedPaths: len(paths)}
	if m == nil || !m.Enabled() {
		for _, path := range paths {
			report.SkippedPaths = append(report.SkippedPaths, PrefetchSkippedPath{Path: path, Reason: "workspace_unavailable"})
		}

		return report
	}

	opts = m.normalizePrefetchReadOptions(opts)

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

		result, selectedPath, bytesRead, linesRead, truncated, skip := m.prefetchReadFile(requested, opts, opts.MaxBytes-report.Bytes)
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

func (m *Manager) prefetchReadFile(requested string, opts PrefetchReadOptions, remainingBytes int) (ToolResult, string, int, int, bool, PrefetchSkippedPath) {
	path, description, err := m.resolveReadTarget(ToolRequest{Path: requested})
	if err != nil {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: requested, Reason: "resolve_failed"}
	}

	displayPath := m.pathAccess.DisplayPath(path)
	kind := classifySearchFilePath(displayPath)

	switch kind {
	case searchFileKindLog, searchFileKindGenerated:
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "excluded_" + string(kind)}
	}

	info, err := os.Stat(path)
	if err != nil {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "stat_failed"}
	}

	if info.IsDir() {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "directory"}
	}

	if opts.MaxBytesPerFile <= 0 || opts.MaxExcerptBytes <= 0 || remainingBytes <= 0 {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "max_bytes_exceeded"}
	}

	sampleLimit := opts.MaxBytesPerFile
	if remainingBytes < sampleLimit {
		sampleLimit = remainingBytes
	}

	file, err := os.Open(path)
	if err != nil {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "read_failed"}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(sampleLimit)))
	if err != nil {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "read_failed"}
	}

	if bytes.IndexByte(data, 0) >= 0 {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "binary"}
	}

	originalLines, err := countWorkspaceFileLines(path)
	if err != nil {
		return ToolResult{}, "", 0, 0, false, PrefetchSkippedPath{Path: displayPath, Reason: "read_failed"}
	}

	excerptLimit := prefetchExcerptLimit(info.Size(), opts, remainingBytes, len(data))

	excerpt := data
	if len(excerpt) > excerptLimit {
		excerpt = excerpt[:excerptLimit]
	}

	outputBytes := len(excerpt)
	truncated := int64(outputBytes) < info.Size()
	lines := textutil.SplitWorkspaceFileLines(excerpt)

	if len(lines) == 0 {
		lines = []string{""}
	}

	var b strings.Builder

	for i, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		if truncated {
			line = textutil.TruncateBytes(line, defaultLogLineCharCap, lineTruncationMarker)
		}

		fmt.Fprintf(&b, "%d:%s\n", i+1, line)
	}

	summary := fmt.Sprintf("Read %s lines 1-%d (total lines: %d; source: prefetch)", description, len(lines), len(lines))
	if truncated {
		summary = fmt.Sprintf("Read %s lines 1-%d (excerpt lines: %d; total lines: %d; bytes: %d/%d; source: prefetch; truncated=true)", description, len(lines), len(lines), originalLines, outputBytes, info.Size())
		if originalLines > len(lines) {
			fmt.Fprintf(&b, "\n[prefetch excerpt truncated: omitted lines %d-%d; full file not included in prefetch]", len(lines)+1, originalLines)
		} else {
			fmt.Fprintf(&b, "\n[prefetch excerpt truncated: omitted content after line %d; full file not included in prefetch]", len(lines))
		}
	}

	m.MarkPathObserved(displayPath)

	return ToolResult{
		Tool:    ToolNamePrefetch,
		Summary: summary,
		Output:  strings.TrimRight(b.String(), "\n"),
		Paths:   []string{displayPath},
		Facts: toolfacts.NewSourceBodyFacts(toolfacts.SourceBodyEvidence{
			Path:       displayPath,
			StartLine:  1,
			EndLine:    len(lines),
			TotalLines: originalLines,
			Truncated:  truncated,
		}),
	}, displayPath, outputBytes, len(lines), truncated, PrefetchSkippedPath{}
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

func countWorkspaceFileLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	lines := 0

	for {
		_, err := reader.ReadString('\n')
		lines++

		if err == nil {
			continue
		}

		if err == io.EOF {
			break
		}

		return 0, err
	}

	return lines, nil
}
