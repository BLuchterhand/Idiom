# Prefetch Excerpt Implementation Review Packet

## Files

- internal/capabilities/workspace/read_prefetch.go
- internal/capabilities/workspace/read_prefetch_test.go
- internal/runtime/agent/read_only_prefetch_test.go
- docs/evidence/read-only-prefetch-proposal/prefetch-model-facing-excerpt-intent.md

## Verification

- go test ./internal/capabilities/workspace -run 'Prefetch|ReadFile' -count=1
- go test ./internal/runtime/agent -run 'ReadOnlyNamedPathPrefetch|TruncatedPrefetch|NamedPathPrefetch|ReadOnly.*Prefetch|Prefetch' -count=1
- go test ./internal/runtime/agent ./internal/capabilities/workspace -count=1
- go build ./cmd/idiom
- git diff --check scoped files

## Implementation

```go
package workspace

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"idiom/internal/platform/util"
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
	displayPath := m.displayPath(path)
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
	excerptLimit := opts.MaxExcerptBytes
	if remainingBytes < excerptLimit {
		excerptLimit = remainingBytes
	}
	if excerptLimit > len(data) {
		excerptLimit = len(data)
	}
	excerpt := data
	if len(excerpt) > excerptLimit {
		excerpt = excerpt[:excerptLimit]
	}
	outputBytes := len(excerpt)
	truncated := int64(outputBytes) < info.Size()
	lines := splitWorkspaceFileLines(excerpt)
	if len(lines) == 0 {
		lines = []string{""}
	}
	var b strings.Builder
	for i, line := range lines {
		line = util.TruncateBytes(strings.TrimRight(line, "\r\n"), defaultLogLineCharCap, "...[line truncated]")
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
	}, displayPath, outputBytes, len(lines), truncated, PrefetchSkippedPath{}
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

```

## Workspace Tests

```go
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestPrefetchReadFilesReadsNamedSourceAndSkipsUnsafeKinds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("Idiom keeps source evidence fresh.\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "logs", "session.log"), []byte("private log\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("x", 128)), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxFileBytes: 128})
	report := mgr.PrefetchReadFiles([]string{
		"README.md",
		"README.md",
		"logs/session.log",
		"binary.dat",
		"large.md",
	}, PrefetchReadOptions{MaxFiles: 6, MaxBytes: 256, MaxBytesPerFile: 64})

	if len(report.Results) != 2 {
		t.Fatalf("expected two prefetched results, got %#v", report)
	}
	if report.Results[0].Tool != ToolNamePrefetch {
		t.Fatalf("tool = %q", report.Results[0].Tool)
	}
	if report.Results[0].Summary != "Read f1 README.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("prefetch summary should preserve read_file shape, got %q", report.Results[0].Summary)
	}
	if !strings.Contains(report.Results[0].Output, "1:Idiom keeps source evidence fresh.") {
		t.Fatalf("prefetch output missing line-numbered source body:\n%s", report.Results[0].Output)
	}
	if !strings.Contains(report.Results[1].Summary, "large.md lines 1-1 (excerpt lines: 1; total lines: 1; bytes: 64/128; source: prefetch; truncated=true)") {
		t.Fatalf("large file should return truncated excerpt summary, got %q", report.Results[1].Summary)
	}
	if !strings.Contains(report.Results[1].Output, "[prefetch excerpt truncated: omitted content after line 1; full file not included in prefetch]") {
		t.Fatalf("large file output missing neutral truncation footer:\n%s", report.Results[1].Output)
	}
	if report.DuplicatePaths != 1 {
		t.Fatalf("duplicate paths = %d, want 1", report.DuplicatePaths)
	}
	if report.TruncatedFiles != 1 {
		t.Fatalf("truncated files = %d, want 1", report.TruncatedFiles)
	}
	reasons := map[string]bool{}
	for _, skipped := range report.SkippedPaths {
		reasons[skipped.Reason] = true
	}
	for _, want := range []string{"excluded_logs", "binary"} {
		if !reasons[want] {
			t.Fatalf("missing skip reason %q in %#v", want, report.SkippedPaths)
		}
	}
}

func TestPrefetchReadFilesUsesWorkspacePathPolicy(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})

	report := mgr.PrefetchReadFiles([]string{filepath.Join(outside, "secret.md"), "README.md"}, PrefetchReadOptions{})
	if len(report.Results) != 1 || report.SelectedPaths[0] != "README.md" {
		t.Fatalf("expected only workspace-local README prefetch, got %#v", report)
	}
	if len(report.SkippedPaths) != 1 || report.SkippedPaths[0].Reason != "resolve_failed" {
		t.Fatalf("expected outside path resolve failure, got %#v", report.SkippedPaths)
	}
}

func TestPrefetchReadFilesTruncatesMultipleLargeFilesAndKeepsSmallFilesStable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "small.md"), []byte("small\n"), 0o644); err != nil {
		t.Fatalf("write small: %v", err)
	}
	largeA := strings.Join([]string{"alpha", strings.Repeat("a", 80), "omega"}, "\n")
	largeB := strings.Join([]string{"bravo", strings.Repeat("b", 80), "done"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "large-a.md"), []byte(largeA), 0o644); err != nil {
		t.Fatalf("write large-a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large-b.md"), []byte(largeB), 0o644); err != nil {
		t.Fatalf("write large-b: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"small.md", "large-a.md", "large-b.md"}, PrefetchReadOptions{
		MaxFiles:        3,
		MaxBytes:        256,
		MaxBytesPerFile: 128,
		MaxExcerptBytes: 16,
	})

	if len(report.Results) != 3 {
		t.Fatalf("expected three prefetched results, got %#v", report)
	}
	if report.Results[0].Summary != "Read f1 small.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("small file summary changed: %q", report.Results[0].Summary)
	}
	for i, result := range report.Results[1:] {
		if !strings.Contains(result.Summary, "truncated=true") {
			t.Fatalf("large result %d was not truncated: %q", i, result.Summary)
		}
		if !strings.Contains(result.Output, "[prefetch excerpt truncated: omitted lines 3-3; full file not included in prefetch]") {
			t.Fatalf("large result %d missing neutral footer:\n%s", i, result.Output)
		}
	}
	if report.TruncatedFiles != 2 {
		t.Fatalf("truncated files = %d, want 2", report.TruncatedFiles)
	}
}

func TestPrefetchReadFilesAllowsReadFileFollowupAfterTruncatedExcerpt(t *testing.T) {
	root := t.TempDir()
	body := strings.Join([]string{"first", "middle", "last"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write large: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"large.md"}, PrefetchReadOptions{
		MaxFiles:        1,
		MaxBytes:        128,
		MaxBytesPerFile: 128,
		MaxExcerptBytes: 8,
	})
	if len(report.Results) != 1 || report.TruncatedFiles != 1 {
		t.Fatalf("expected truncated prefetch, got %#v", report)
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "large.md", StartLine: 2, EndLine: 3})
	if result.Error != "" {
		t.Fatalf("read_file follow-up failed: %#v", result)
	}
	if !strings.Contains(result.Output, "2:middle") || !strings.Contains(result.Output, "3:last") {
		t.Fatalf("read_file follow-up missing requested lines:\n%s", result.Output)
	}
}

func TestPrefetchReadFilesHandlesExcerptEdges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.md"), nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "exact.md"), []byte("12345678"), 0o644); err != nil {
		t.Fatalf("write exact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "long-line.md"), []byte("123456789"), 0o644); err != nil {
		t.Fatalf("write long-line: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "large-binary.bin"), append([]byte("123456789"), 0x00, 'x'), 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	report := mgr.PrefetchReadFiles([]string{"empty.md", "exact.md", "long-line.md", "large-binary.bin"}, PrefetchReadOptions{
		MaxFiles:        4,
		MaxBytes:        128,
		MaxBytesPerFile: 32,
		MaxExcerptBytes: 8,
	})

	if len(report.Results) != 3 {
		t.Fatalf("expected three text results, got %#v", report)
	}
	if report.Results[0].Summary != "Read f1 empty.md lines 1-1 (total lines: 1; source: prefetch)" {
		t.Fatalf("empty summary = %q", report.Results[0].Summary)
	}
	if strings.Contains(report.Results[1].Summary, "truncated=true") {
		t.Fatalf("exact-budget file should not be truncated: %q", report.Results[1].Summary)
	}
	if !strings.Contains(report.Results[2].Summary, "truncated=true") {
		t.Fatalf("long single-line file should be truncated: %q", report.Results[2].Summary)
	}
	if report.TruncatedFiles != 1 {
		t.Fatalf("truncated files = %d, want 1", report.TruncatedFiles)
	}
	if len(report.SkippedPaths) != 1 || report.SkippedPaths[0].Reason != "binary" {
		t.Fatalf("binary should be rejected before excerpt construction, got %#v", report.SkippedPaths)
	}
}

```

## Runner Tests

```go
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

func TestReadOnlyNamedPathPrefetchEventGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("prefetch fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	base := func() *Runner {
		return &Runner{
			Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
				Contract:          string(workerruntime.ContractArchitecture),
				Objective:         "Read README.md and summarize it.",
				PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			},
		}
	}

	tests := []struct {
		name             string
		mutate           func(*Runner)
		collectedResults []workspace.ToolResult
		envDisabled      bool
		wantEligible     bool
		wantMissReason   string
	}{
		{
			name:         "eligible top-level read-only",
			wantEligible: true,
		},
		{
			name: "delegated child",
			mutate: func(r *Runner) {
				r.AgentName = "child:explorer"
			},
			wantMissReason: "delegated_child_agent",
		},
		{
			name:           "disabled by env",
			envDisabled:    true,
			wantMissReason: "disabled_by_env",
		},
		{
			name: "existing evidence",
			collectedResults: []workspace.ToolResult{
				{Tool: "read_file", Summary: "Read README.md"},
			},
			wantMissReason: "existing_evidence_present",
		},
		{
			name: "write permission",
			mutate: func(r *Runner) {
				r.RuntimeBrief.PermissionProfile = string(workerruntime.PermissionGuardedWorkspaceWrite)
			},
			wantMissReason: "not_read_only_workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := base()
			if tt.mutate != nil {
				tt.mutate(runner)
			}
			if tt.envDisabled {
				t.Setenv("IDIOM_DISABLE_READONLY_PREFETCH", "1")
			}

			event := runner.readOnlyNamedPathPrefetchEvent(tt.collectedResults)
			if event.Eligible != tt.wantEligible || event.MissReason != tt.wantMissReason {
				t.Fatalf("event = %#v, want eligible=%v miss=%q", event, tt.wantEligible, tt.wantMissReason)
			}
		})
	}
}

func TestRunnerTruncatedPrefetchCanAnswerWithoutReadFileFollowup(t *testing.T) {
	root := t.TempDir()
	body := "The large fixture uses prefetch excerpts to expose answer token.\n" + strings.Repeat("padding\n", 2000)
	if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"content": "The large fixture uses prefetch excerpts to expose answer token. Source: large.md.",
				},
			}},
		})
	}))
	defer server.Close()

	runner := &Runner{
		Client:    llama.NewClient(server.URL, 2*time.Second),
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 3},
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Read large.md and explain what the large fixture uses.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			AllowedTools:      []string{"read_file", "search_content"},
		},
	}

	var prefetchEvent *readOnlyPrefetchEvent
	var readFileResults int
	runner.OnEvent = func(event RunEvent) {
		if event.Role != readOnlyPrefetchEventRole {
			return
		}
		var payload readOnlyPrefetchEvent
		if err := json.Unmarshal([]byte(event.Content), &payload); err == nil {
			prefetchEvent = &payload
		}
	}

	answer, err := runner.Run(
		context.Background(),
		[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
		llama.ChatOptions{},
		nil,
		nil,
		func(event ToolResultEvent) {
			if event.Result.Tool == workspace.ToolNameReadFile {
				readFileResults++
			}
		},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(answer, "Source: large.md") {
		t.Fatalf("expected grounded answer from prefetched excerpt, got %q", answer)
	}
	if prefetchEvent == nil || !prefetchEvent.Eligible || prefetchEvent.TruncatedFiles != 1 {
		t.Fatalf("expected eligible truncated prefetch event, got %#v", prefetchEvent)
	}
	if readFileResults != 0 {
		t.Fatalf("expected no model-initiated read_file follow-up, got %d", readFileResults)
	}
	if requests != 1 {
		t.Fatalf("expected one model request, got %d", requests)
	}
}

func TestReadOnlyNamedPathPrefetchNoPathMissIsReported(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{
		Workspace: workspace.NewManager(config.Workspace{Root: root, CWD: root}),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        string(workerruntime.TaskFamilyArchitecture),
			Contract:          string(workerruntime.ContractArchitecture),
			Objective:         "Summarize the relevant behavior.",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
		},
	}

	var events []RunEvent
	runner.OnEvent = func(event RunEvent) {
		if event.Role == readOnlyPrefetchEventRole {
			events = append(events, event)
		}
	}
	collected := []workspace.ToolResult{}
	turnMessages := []llama.ChatMessage{}
	recorder := runnerTurnRecorder{
		runner:           runner,
		collectedResults: &collected,
		turnMessages:     &turnMessages,
	}

	got := runner.maybeApplyReadOnlyNamedPathPrefetch(recorder, collected)
	if len(got) != 0 {
		t.Fatalf("expected no prefetched results, got %#v", got)
	}
	if len(events) != 1 {
		t.Fatalf("expected one prefetch event, got %#v", events)
	}
	var payload readOnlyPrefetchEvent
	if err := json.Unmarshal([]byte(events[0].Content), &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if payload.Eligible || payload.MissReason != "no_explicit_named_paths" {
		t.Fatalf("unexpected no-path event: %#v", payload)
	}
}

```
