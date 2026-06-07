package comprehension

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"idiom/internal/platform/util"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTraceEventLimit     = 64
	traceExcerptLimit          = 4000
	runtimeTraceRecentLimit    = 12
	runtimeTraceManifestLimit  = 64
	runtimeTraceManifestFile   = "manifest.json"
	runtimeTraceManifestSchema = "idiom.runtime_traces.v1"
)

type TraceOptions struct {
	Root         string
	Command      string
	Path         string
	Language     string
	Stdout       string
	Stderr       string
	ExitCode     int
	ExecError    string
	Limit        int
	IncludeFiles bool
	Now          time.Time
}

type TraceResult struct {
	TraceID        string          `json:"trace_id"`
	ArtifactPath   string          `json:"artifact_path,omitempty"`
	IndexID        string          `json:"index_id,omitempty"`
	StoreKind      string          `json:"store_kind,omitempty"`
	Root           string          `json:"root"`
	Command        string          `json:"command"`
	Path           string          `json:"path,omitempty"`
	Language       string          `json:"language,omitempty"`
	GeneratedAt    string          `json:"generated_at"`
	Success        bool            `json:"success"`
	ExitCode       int             `json:"exit_code"`
	Fresh          bool            `json:"fresh"`
	StaleFiles     []string        `json:"stale_files,omitempty"`
	StdoutExcerpt  string          `json:"stdout_excerpt,omitempty"`
	StderrExcerpt  string          `json:"stderr_excerpt,omitempty"`
	Events         []TraceEvent    `json:"events,omitempty"`
	ExecutedFiles  []TraceFile     `json:"executed_files,omitempty"`
	FailingAnchors []TraceAnchor   `json:"failing_anchors,omitempty"`
	Coverage       []TraceCoverage `json:"coverage,omitempty"`
	RuntimeNodes   []NodeSummary   `json:"runtime_nodes,omitempty"`
	RuntimeEdges   []EdgeSummary   `json:"runtime_edges,omitempty"`
	Evidence       []PackEvidence  `json:"evidence,omitempty"`
	Risks          []string        `json:"risks,omitempty"`
	Unknowns       []string        `json:"unknowns,omitempty"`
	UnresolvedGaps []string        `json:"unresolved_gaps,omitempty"`
}

type TraceEvent struct {
	Kind       string  `json:"kind"`
	Action     string  `json:"action,omitempty"`
	Package    string  `json:"package,omitempty"`
	Test       string  `json:"test,omitempty"`
	Path       string  `json:"path,omitempty"`
	Line       int     `json:"line,omitempty"`
	Function   string  `json:"function,omitempty"`
	Message    string  `json:"message,omitempty"`
	Elapsed    float64 `json:"elapsed,omitempty"`
	Language   string  `json:"language,omitempty"`
	Confidence string  `json:"confidence"`
}

type TraceFile struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Language    string `json:"language,omitempty"`
	Confidence  string `json:"confidence"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Stale       bool   `json:"stale,omitempty"`
}

type TraceAnchor struct {
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Function   string `json:"function,omitempty"`
	Label      string `json:"label,omitempty"`
	Message    string `json:"message,omitempty"`
	Language   string `json:"language,omitempty"`
	Confidence string `json:"confidence"`
	Stale      bool   `json:"stale,omitempty"`
}

type TraceCoverage struct {
	Path          string  `json:"path"`
	StartLine     int     `json:"start_line,omitempty"`
	EndLine       int     `json:"end_line,omitempty"`
	CoveredBlocks int     `json:"covered_blocks,omitempty"`
	TotalBlocks   int     `json:"total_blocks,omitempty"`
	CoveredLines  int     `json:"covered_lines,omitempty"`
	TotalLines    int     `json:"total_lines,omitempty"`
	Percent       float64 `json:"percent,omitempty"`
	Format        string  `json:"format,omitempty"`
	Confidence    string  `json:"confidence"`
}

type traceManifest struct {
	SchemaVersion string               `json:"schema_version"`
	UpdatedAt     string               `json:"updated_at,omitempty"`
	Traces        []traceManifestEntry `json:"traces,omitempty"`
}

type traceManifestEntry struct {
	TraceID      string `json:"trace_id"`
	ArtifactPath string `json:"artifact_path"`
	GeneratedAt  string `json:"generated_at,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	ModTime      string `json:"mod_time,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type runtimePackOverlay struct {
	Files                []PackFile
	Tests                []PackTest
	Evidence             []PackEvidence
	Risks                []string
	Unknowns             []string
	UnresolvedGaps       []string
	UnresolvedGapDetails []GraphUnresolvedGap
}

type goTestJSONEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func Trace(opts TraceOptions) (TraceResult, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return TraceResult{}, err
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		return TraceResult{}, fmt.Errorf("command is required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultTraceEventLimit
	}
	language := normalizeLanguage(opts.Language)
	if language == "" {
		language = "auto"
	}

	traceID := tracePackID(command, opts.Stdout, opts.Stderr, now.Format(time.RFC3339))
	result := TraceResult{
		TraceID:       traceID,
		ArtifactPath:  filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json")),
		Root:          root,
		Command:       command,
		Path:          cleanTraceScope(opts.Path),
		Language:      language,
		GeneratedAt:   now.Format(time.RFC3339),
		Success:       opts.ExitCode == 0 && strings.TrimSpace(opts.ExecError) == "",
		ExitCode:      opts.ExitCode,
		Fresh:         true,
		StdoutExcerpt: traceExcerpt(opts.Stdout),
		StderrExcerpt: traceExcerpt(opts.Stderr),
	}

	graph, graphErr := LoadGraph(root)
	if graphErr == nil {
		result.IndexID = graph.ID
		result.StoreKind = graph.StoreKind
	} else {
		result.UnresolvedGaps = append(result.UnresolvedGaps, "no context index was available to link runtime evidence to static graph nodes")
	}

	combined := strings.TrimSpace(opts.Stdout + "\n" + opts.Stderr)
	result.Events = append(result.Events, TraceEvent{
		Kind:       "command",
		Action:     commandAction(result.Success),
		Message:    strings.TrimSpace(opts.ExecError),
		Language:   language,
		Confidence: "exact",
	})
	result.Events = append(result.Events, parseGoTestJSONEvents(combined, limit)...)
	anchors := parseTraceAnchors(root, combined, language)
	for _, anchor := range anchors {
		result.Events = append(result.Events, TraceEvent{
			Kind:       "stack_frame",
			Action:     "observed",
			Path:       anchor.Path,
			Line:       anchor.Line,
			Function:   anchor.Function,
			Message:    anchor.Message,
			Language:   anchor.Language,
			Confidence: anchor.Confidence,
		})
	}
	result.FailingAnchors = failingAnchorsFromTrace(result.Events, anchors, limit)

	coverage := parseCoverageFromCommand(root, command, language, limit)
	result.Coverage = coverage
	for _, item := range coverage {
		result.Events = append(result.Events, TraceEvent{
			Kind:       "coverage",
			Action:     "observed",
			Path:       item.Path,
			Line:       item.StartLine,
			Message:    fmt.Sprintf("%s coverage %.1f%%", item.Format, item.Percent),
			Language:   sourceLanguageForPath(item.Path),
			Confidence: item.Confidence,
		})
	}
	if len(coverage) == 0 {
		result.UnresolvedGaps = append(result.UnresolvedGaps, "no coverage profile was found in the command arguments")
	}

	result.ExecutedFiles = traceFilesFromEvidence(root, anchors, coverage, limit)
	stale := markTraceFreshness(root, &result)
	result.StaleFiles = stale
	result.Fresh = len(stale) == 0
	if !result.Success {
		result.Risks = append(result.Risks, "Runtime command exited unsuccessfully; trace evidence may be partial but is still useful for failing-test localization.")
	}
	if len(result.FailingAnchors) == 0 && !result.Success {
		result.Unknowns = append(result.Unknowns, "Command failed but no file/line anchor was parsed from stdout or stderr.")
	}
	if len(result.ExecutedFiles) == 0 {
		result.Unknowns = append(result.Unknowns, "No executed files or stack-frame paths were parsed from the runtime output.")
	}
	if len(result.Events) > limit {
		result.Events = result.Events[:limit]
	}

	result.RuntimeNodes, result.RuntimeEdges = runtimeGraphEvidence(graph, result)
	result.Evidence = tracePackEvidence(result)
	if err := saveTrace(root, result); err != nil {
		return TraceResult{}, err
	}
	return result, nil
}

func runtimeOverlayForPack(root string, task string, scope string, limit int, includeStale bool) runtimePackOverlay {
	if limit <= 0 {
		limit = defaultPackLimit
	}
	traces, err := loadRecentTraces(root, runtimeTraceRecentLimit)
	if err != nil || len(traces) == 0 {
		return runtimePackOverlay{}
	}
	terms := queryTerms(QueryOptions{Query: task, Path: scope})
	runtimeTask := looksRuntimeTask(task)
	type scoredTrace struct {
		trace TraceResult
		score int
	}
	var scored []scoredTrace
	for _, trace := range traces {
		score := traceMatchScore(trace, terms)
		if score == 0 && !runtimeTask {
			continue
		}
		scored = append(scored, scoredTrace{trace: trace, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].trace.GeneratedAt > scored[j].trace.GeneratedAt
	})
	overlay := runtimePackOverlay{}
	added := 0
	for _, item := range scored {
		trace := item.trace
		stale := markTraceFreshness(root, &trace)
		trace.Fresh = len(stale) == 0
		trace.StaleFiles = stale
		if !trace.Fresh {
			if !trace.Success {
				overlay.Risks = append(overlay.Risks, "Stale runtime trace "+trace.TraceID+" captured a failing command but references changed files.")
			}
			overlay.UnresolvedGaps = append(overlay.UnresolvedGaps, staleRuntimeTraceGap(trace))
			overlay.UnresolvedGapDetails = append(overlay.UnresolvedGapDetails, staleRuntimeTraceGapDetail(trace))
			if includeStale {
				overlay.Evidence = append(overlay.Evidence, staleRuntimeEvidence(trace)...)
			}
			continue
		}
		overlay.Evidence = append(overlay.Evidence, PackEvidence{
			Kind:    "runtime_trace",
			Label:   "context_trace_pack",
			Path:    trace.ArtifactPath,
			Handle:  "context_trace://" + trace.TraceID,
			Content: tracePackSummary(trace),
		})
		for _, anchor := range trace.FailingAnchors {
			overlay.Files = append(overlay.Files, PackFile{
				Path:       anchor.Path,
				Role:       packRoleForRuntimePath(anchor.Path),
				Reason:     "observed failing runtime anchor in " + trace.TraceID,
				Language:   anchor.Language,
				Confidence: anchor.Confidence,
				Stale:      anchor.Stale,
			})
			overlay.Evidence = append(overlay.Evidence, PackEvidence{
				Kind:       "runtime_anchor",
				Label:      anchor.Label,
				Path:       anchor.Path,
				Confidence: anchor.Confidence,
				Content:    runtimeAnchorContent(anchor),
			})
		}
		for _, file := range trace.ExecutedFiles {
			overlay.Files = append(overlay.Files, PackFile{
				Path:       file.Path,
				Role:       packRoleForRuntimePath(file.Path),
				Reason:     "observed in runtime trace " + trace.TraceID,
				Language:   file.Language,
				Confidence: file.Confidence,
				Stale:      file.Stale,
			})
		}
		for _, event := range trace.Events {
			if event.Kind == "test_event" && event.Action == "fail" {
				overlay.Tests = append(overlay.Tests, PackTest{
					Command: trace.Command,
					Path:    event.Path,
					Symbol:  event.Test,
					Reason:  "runtime trace observed failing test",
				})
			}
		}
		if !trace.Success {
			overlay.Risks = append(overlay.Risks, "Runtime trace "+trace.TraceID+" captured a failing command.")
		}
		if !trace.Fresh {
			overlay.UnresolvedGaps = append(overlay.UnresolvedGaps, staleRuntimeTraceGap(trace))
			overlay.UnresolvedGapDetails = append(overlay.UnresolvedGapDetails, staleRuntimeTraceGapDetail(trace))
		}
		added++
		if added >= 3 || len(overlay.Files) >= limit {
			break
		}
	}
	return overlay
}

func staleRuntimeTraceGap(trace TraceResult) string {
	return "reason=stale_runtime_trace trace_id=" + strings.TrimSpace(trace.TraceID) + " stale_path_count=" + fmt.Sprint(len(trace.StaleFiles)) + " stale_paths=" + limitedCSV(trace.StaleFiles, 5)
}

func staleRuntimeTraceGapDetail(trace TraceResult) GraphUnresolvedGap {
	return GraphUnresolvedGap{
		Reason:         "stale_runtime_trace",
		State:          "stale",
		TraceID:        strings.TrimSpace(trace.TraceID),
		StalePathCount: len(trace.StaleFiles),
		StalePaths:     limitedStrings(trace.StaleFiles, 5),
	}
}

func staleRuntimeEvidence(trace TraceResult) []PackEvidence {
	evidence := []PackEvidence{{
		Kind:    "runtime_trace",
		Label:   "context_trace_pack",
		Path:    trace.ArtifactPath,
		Handle:  "context_trace://" + trace.TraceID,
		Content: tracePackSummary(trace),
		Stale:   true,
	}}
	for _, anchor := range trace.FailingAnchors {
		evidence = append(evidence, PackEvidence{
			Kind:       "runtime_anchor",
			Label:      anchor.Label,
			Path:       anchor.Path,
			Confidence: anchor.Confidence,
			Content:    runtimeAnchorContent(anchor),
			Stale:      true,
		})
	}
	return evidence
}

func parseGoTestJSONEvents(text string, limit int) []TraceEvent {
	var out []TraceEvent
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"Action"`) {
			continue
		}
		var event goTestJSONEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Action == "" {
			continue
		}
		message := strings.TrimSpace(event.Output)
		if event.Action == "output" && !isImportantRuntimeOutput(message) {
			continue
		}
		out = append(out, TraceEvent{
			Kind:       "test_event",
			Action:     strings.ToLower(event.Action),
			Package:    event.Package,
			Test:       event.Test,
			Message:    message,
			Elapsed:    event.Elapsed,
			Language:   "go",
			Confidence: "exact",
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func parseTraceAnchors(root string, text string, language string) []TraceAnchor {
	var anchors []TraceAnchor
	for _, match := range regexp.MustCompile(`(?m)File "([^"]+)", line ([0-9]+), in ([^\n]+)`).FindAllStringSubmatch(text, -1) {
		anchors = appendTraceAnchor(root, anchors, match[1], match[2], strings.TrimSpace(match[3]), "file traceback")
	}
	for _, match := range regexp.MustCompile(`(?m)\bat\s+(?:[^\s()]+\s+)?\(?([^()\s]+):([0-9]+):[0-9]+\)?`).FindAllStringSubmatch(text, -1) {
		anchors = appendTraceAnchor(root, anchors, match[1], match[2], "", "stack trace")
	}
	for _, match := range regexp.MustCompile(`(?m)\(([^():]+\.(?:java|kt|scala)):(\d+)\)`).FindAllStringSubmatch(text, -1) {
		anchors = appendTraceAnchor(root, anchors, match[1], match[2], "", "jvm stack trace")
	}
	for _, match := range regexp.MustCompile(`(?m)((?:res://)?[A-Za-z0-9_./\\-]+\.(?:go|rs|py|ts|tsx|js|jsx|java|c|cc|cpp|cxx|h|hpp|gd)):(\d+)`).FindAllStringSubmatch(text, -1) {
		anchors = appendTraceAnchor(root, anchors, match[1], match[2], "", "file:line output")
	}
	anchors = dedupeTraceAnchors(anchors)
	for i := range anchors {
		if anchors[i].Language == "" {
			anchors[i].Language = defaultString(sourceLanguageForPath(anchors[i].Path), normalizeLanguage(language))
		}
		if anchors[i].Message == "" {
			anchors[i].Message = traceLineNearAnchor(text, anchors[i])
		}
	}
	return anchors
}

func appendTraceAnchor(root string, anchors []TraceAnchor, rawPath string, rawLine string, function string, label string) []TraceAnchor {
	path, ok := normalizeTracePath(root, rawPath)
	if !ok {
		return anchors
	}
	line, err := strconv.Atoi(rawLine)
	if err != nil || line <= 0 {
		return anchors
	}
	anchors = append(anchors, TraceAnchor{
		Path:       path,
		Line:       line,
		Function:   function,
		Label:      label,
		Language:   sourceLanguageForPath(path),
		Confidence: "exact",
	})
	return anchors
}

func parseCoverageFromCommand(root string, command string, language string, limit int) []TraceCoverage {
	var out []TraceCoverage
	for _, path := range coverageProfilePathsFromCommand(command) {
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, filepath.FromSlash(path))
		}
		items := parseCoverageFile(root, abs, language, limit-len(out))
		out = append(out, items...)
		if limit > 0 && len(out) >= limit {
			return out[:limit]
		}
	}
	return out
}

func parseCoverageFile(root string, path string, language string, limit int) []TraceCoverage {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(data)
	if strings.HasPrefix(strings.TrimSpace(text), "mode:") {
		return parseGoCoverageProfile(root, text, limit)
	}
	if strings.Contains(text, "\nSF:") || strings.HasPrefix(text, "SF:") {
		return parseLCOVProfile(root, text, limit)
	}
	return nil
}

func parseGoCoverageProfile(root string, text string, limit int) []TraceCoverage {
	type aggregate struct {
		path          string
		startLine     int
		endLine       int
		coveredBlocks int
		totalBlocks   int
	}
	items := map[string]*aggregate{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		rawRange := fields[0]
		count, _ := strconv.Atoi(fields[2])
		pathPart, rangePart, ok := strings.Cut(rawRange, ":")
		if !ok {
			continue
		}
		path, ok := normalizeTracePath(root, pathPart)
		if !ok {
			continue
		}
		startLine, endLine := parseGoCoverageRange(rangePart)
		if startLine == 0 {
			continue
		}
		item := items[path]
		if item == nil {
			item = &aggregate{path: path, startLine: startLine, endLine: endLine}
			items[path] = item
		}
		if startLine < item.startLine {
			item.startLine = startLine
		}
		if endLine > item.endLine {
			item.endLine = endLine
		}
		item.totalBlocks++
		if count > 0 {
			item.coveredBlocks++
		}
	}
	out := make([]TraceCoverage, 0, len(items))
	for _, item := range items {
		percent := 0.0
		if item.totalBlocks > 0 {
			percent = float64(item.coveredBlocks) / float64(item.totalBlocks) * 100
		}
		out = append(out, TraceCoverage{
			Path:          item.path,
			StartLine:     item.startLine,
			EndLine:       item.endLine,
			CoveredBlocks: item.coveredBlocks,
			TotalBlocks:   item.totalBlocks,
			Percent:       percent,
			Format:        "go-coverprofile",
			Confidence:    "exact",
		})
	}
	slices.SortFunc(out, func(a, b TraceCoverage) int { return cmp.Compare(a.Path, b.Path) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func parseLCOVProfile(root string, text string, limit int) []TraceCoverage {
	type aggregate struct {
		path         string
		coveredLines int
		totalLines   int
		minLine      int
		maxLine      int
	}
	var current string
	items := map[string]*aggregate{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "SF:") {
			path, ok := normalizeTracePath(root, strings.TrimPrefix(line, "SF:"))
			if !ok {
				current = ""
				continue
			}
			current = path
			if items[current] == nil {
				items[current] = &aggregate{path: current}
			}
			continue
		}
		if current == "" || !strings.HasPrefix(line, "DA:") {
			continue
		}
		payload := strings.TrimPrefix(line, "DA:")
		lineText, countText, ok := strings.Cut(payload, ",")
		if !ok {
			continue
		}
		lineNo, err := strconv.Atoi(lineText)
		if err != nil {
			continue
		}
		count, _ := strconv.Atoi(countText)
		item := items[current]
		item.totalLines++
		if count > 0 {
			item.coveredLines++
		}
		if item.minLine == 0 || lineNo < item.minLine {
			item.minLine = lineNo
		}
		if lineNo > item.maxLine {
			item.maxLine = lineNo
		}
	}
	out := make([]TraceCoverage, 0, len(items))
	for _, item := range items {
		percent := 0.0
		if item.totalLines > 0 {
			percent = float64(item.coveredLines) / float64(item.totalLines) * 100
		}
		out = append(out, TraceCoverage{
			Path:         item.path,
			StartLine:    item.minLine,
			EndLine:      item.maxLine,
			CoveredLines: item.coveredLines,
			TotalLines:   item.totalLines,
			Percent:      percent,
			Format:       "lcov",
			Confidence:   "exact",
		})
	}
	slices.SortFunc(out, func(a, b TraceCoverage) int { return cmp.Compare(a.Path, b.Path) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func traceFilesFromEvidence(root string, anchors []TraceAnchor, coverage []TraceCoverage, limit int) []TraceFile {
	files := map[string]TraceFile{}
	for _, anchor := range anchors {
		if anchor.Path == "" {
			continue
		}
		item := files[anchor.Path]
		if item.Path == "" {
			item = TraceFile{
				Path:       anchor.Path,
				StartLine:  anchor.Line,
				EndLine:    anchor.Line,
				Reason:     "stack frame",
				Language:   anchor.Language,
				Confidence: anchor.Confidence,
			}
		}
		if anchor.Line > 0 && (item.StartLine == 0 || anchor.Line < item.StartLine) {
			item.StartLine = anchor.Line
		}
		if anchor.Line > item.EndLine {
			item.EndLine = anchor.Line
		}
		files[anchor.Path] = item
	}
	for _, cov := range coverage {
		item := files[cov.Path]
		if item.Path == "" {
			item = TraceFile{
				Path:       cov.Path,
				StartLine:  cov.StartLine,
				EndLine:    cov.EndLine,
				Reason:     "coverage profile",
				Language:   sourceLanguageForPath(cov.Path),
				Confidence: cov.Confidence,
			}
		}
		if item.Reason != "coverage profile" {
			item.Reason += "; coverage profile"
		}
		files[cov.Path] = item
	}
	out := make([]TraceFile, 0, len(files))
	for _, item := range files {
		item.Fingerprint, _ = fileFingerprint(filepath.Join(root, filepath.FromSlash(item.Path)))
		out = append(out, item)
	}
	slices.SortFunc(out, func(a, b TraceFile) int { return cmp.Compare(a.Path, b.Path) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func runtimeGraphEvidence(graph Graph, trace TraceResult) ([]NodeSummary, []EdgeSummary) {
	traceID := "runtime_trace://" + trace.TraceID
	nodes := []NodeSummary{{
		ID:         traceID,
		Kind:       "runtime_trace",
		Path:       trace.ArtifactPath,
		Language:   trace.Language,
		Confidence: "exact",
		Properties: map[string]any{
			"command":   trace.Command,
			"success":   trace.Success,
			"exit_code": trace.ExitCode,
		},
	}}
	var edges []EdgeSummary
	seenNodes := map[string]struct{}{traceID: {}}
	addNode := func(node NodeSummary) {
		if node.ID == "" {
			return
		}
		if _, ok := seenNodes[node.ID]; ok {
			return
		}
		seenNodes[node.ID] = struct{}{}
		nodes = append(nodes, node)
	}
	addEdge := func(kind string, targetID string, evidence map[string]any) {
		if targetID == "" {
			return
		}
		edges = append(edges, EdgeSummary{
			ID:         edgeID(kind, traceID, targetID, evidence),
			Kind:       kind,
			SourceID:   traceID,
			TargetID:   targetID,
			Confidence: "exact",
			Evidence:   evidence,
		})
	}
	for _, file := range trace.ExecutedFiles {
		id := fileNodeID(file.Path)
		node := NodeSummary{ID: id, Kind: "file", Path: file.Path, Language: file.Language, Confidence: file.Confidence, Stale: file.Stale}
		if graph.Root != "" {
			if graphNode, ok := findNode(graph.Nodes, id); ok {
				summaries := summarizeNodes([]Node{graphNode}, StatusReport{})
				if len(summaries) > 0 {
					node = summaries[0]
				}
			}
		}
		addNode(node)
		addEdge("runtime_reads", id, map[string]any{"path": file.Path, "start_line": file.StartLine, "end_line": file.EndLine, "reason": file.Reason})
	}
	for _, event := range trace.Events {
		if event.Kind != "test_event" || event.Test == "" {
			continue
		}
		if id := findTestSymbolID(graph.Nodes, event.Test, event.Package); id != "" {
			if node, ok := findNode(graph.Nodes, id); ok {
				summaries := summarizeNodes([]Node{node}, StatusReport{})
				if len(summaries) > 0 {
					addNode(summaries[0])
				}
			}
			addEdge("runtime_calls", id, map[string]any{"package": event.Package, "test": event.Test, "action": event.Action})
		}
	}
	return nodes, edges
}

func tracePackEvidence(result TraceResult) []PackEvidence {
	evidence := []PackEvidence{{
		Kind:    "runtime_trace",
		Label:   "context_trace_pack",
		Path:    result.ArtifactPath,
		Handle:  "context_trace://" + result.TraceID,
		Content: tracePackSummary(result),
		Stale:   !result.Fresh,
	}}
	for _, anchor := range result.FailingAnchors {
		evidence = append(evidence, PackEvidence{
			Kind:       "runtime_anchor",
			Label:      anchor.Label,
			Path:       anchor.Path,
			Confidence: anchor.Confidence,
			Content:    runtimeAnchorContent(anchor),
			Stale:      anchor.Stale,
		})
	}
	for _, file := range result.ExecutedFiles {
		evidence = append(evidence, PackEvidence{
			Kind:       "runtime_file",
			Label:      file.Reason,
			Path:       file.Path,
			Confidence: file.Confidence,
			Content:    traceFileContent(file),
			Stale:      file.Stale,
		})
	}
	return evidence
}

func markTraceFreshness(root string, result *TraceResult) []string {
	staleSet := make(map[string]struct{})
	for i := range result.ExecutedFiles {
		file := &result.ExecutedFiles[i]
		if file.Path == "" || file.Fingerprint == "" {
			continue
		}
		current, err := fileFingerprint(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil || current != file.Fingerprint {
			file.Stale = true
			staleSet[file.Path] = struct{}{}
		}
	}
	for i := range result.FailingAnchors {
		anchor := &result.FailingAnchors[i]
		if _, ok := staleSet[anchor.Path]; ok {
			anchor.Stale = true
		}
	}
	return sortedStringSet(staleSet)
}

func loadRecentTraces(root string, limit int) ([]TraceResult, error) {
	dir := filepath.Join(root, ".idiom", "comprehension", "traces")
	if manifest, ok := loadTraceManifest(root); ok {
		return loadRecentTracesFromManifest(root, manifest, limit)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var traces []TraceResult
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == runtimeTraceManifestFile || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var trace TraceResult
		if err := json.Unmarshal(data, &trace); err != nil {
			continue
		}
		traces = append(traces, trace)
	}
	slices.SortFunc(traces, func(a, b TraceResult) int { return -cmp.Compare(a.GeneratedAt, b.GeneratedAt) })
	if limit > 0 && len(traces) > limit {
		traces = traces[:limit]
	}
	return traces, nil
}

func saveTrace(root string, result TraceResult) error {
	path := filepath.Join(root, ".idiom", "comprehension", "traces", result.TraceID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_ = ensureIgnored(root)
	written := append(data, '\n')
	if err := os.WriteFile(path, written, 0o644); err != nil {
		return err
	}
	return updateTraceManifest(root, result, written)
}

type runtimeTraceFingerprintState struct {
	Fingerprint string
	Source      string
	Considered  int
	Total       int
}

func runtimeTracesStateFingerprint(root string) string {
	return runtimeTraceStateFingerprint(root, runtimeTraceRecentLimit).Fingerprint
}

func runtimeTraceStateFingerprint(root string, limit int) runtimeTraceFingerprintState {
	if limit <= 0 {
		limit = runtimeTraceRecentLimit
	}
	if manifest, ok := loadTraceManifest(root); ok {
		entries := boundedTraceManifestEntries(manifest.Traces, limit)
		if len(entries) == 0 {
			return runtimeTraceFingerprintState{Source: "trace_manifest", Total: len(manifest.Traces)}
		}
		h := sha256.New()
		h.Write([]byte(runtimeTraceManifestSchema))
		h.Write([]byte("\x00"))
		for _, entry := range entries {
			h.Write([]byte(entry.TraceID))
			h.Write([]byte("\x00"))
			h.Write([]byte(entry.ArtifactPath))
			h.Write([]byte("\x00"))
			h.Write([]byte(entry.GeneratedAt))
			h.Write([]byte("\x00"))
			h.Write([]byte(entry.Fingerprint))
			h.Write([]byte("\x00"))
			h.Write([]byte(entry.ModTime))
			h.Write([]byte("\x00"))
			h.Write([]byte(strconv.FormatInt(entry.Size, 10)))
			h.Write([]byte("\x00"))
		}
		sum := h.Sum(nil)
		return runtimeTraceFingerprintState{
			Fingerprint: hex.EncodeToString(sum[:8]),
			Source:      "trace_manifest",
			Considered:  len(entries),
			Total:       len(manifest.Traces),
		}
	}
	info, err := os.Stat(filepath.Join(root, ".idiom", "comprehension", "traces"))
	if err != nil {
		return runtimeTraceFingerprintState{}
	}
	h := sha256.New()
	h.Write([]byte("runtime_trace_directory\x00"))
	h.Write([]byte(info.ModTime().UTC().Format(time.RFC3339Nano)))
	h.Write([]byte("\x00"))
	sum := h.Sum(nil)
	return runtimeTraceFingerprintState{
		Fingerprint: hex.EncodeToString(sum[:8]),
		Source:      "trace_directory",
		Considered:  1,
	}
}

func loadRecentTracesFromManifest(root string, manifest traceManifest, limit int) ([]TraceResult, error) {
	entries := boundedTraceManifestEntries(manifest.Traces, limit)
	traces := make([]TraceResult, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(entry.ArtifactPath)))
		if strings.TrimSpace(entry.ArtifactPath) == "" {
			path = filepath.Join(root, ".idiom", "comprehension", "traces", entry.TraceID+".json")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var trace TraceResult
		if err := json.Unmarshal(data, &trace); err != nil || strings.TrimSpace(trace.TraceID) == "" {
			continue
		}
		traces = append(traces, trace)
	}
	slices.SortFunc(traces, func(a, b TraceResult) int { return -cmp.Compare(a.GeneratedAt, b.GeneratedAt) })
	if limit > 0 && len(traces) > limit {
		traces = traces[:limit]
	}
	return traces, nil
}

func updateTraceManifest(root string, result TraceResult, written []byte) error {
	traceID := strings.TrimSpace(result.TraceID)
	if traceID == "" {
		return nil
	}
	manifest, _ := loadTraceManifest(root)
	path := filepath.Join(root, ".idiom", "comprehension", "traces", traceID+".json")
	info, _ := os.Stat(path)
	entry := traceManifestEntry{
		TraceID:      traceID,
		ArtifactPath: util.FirstNonEmpty(strings.TrimSpace(result.ArtifactPath), filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", traceID+".json"))),
		GeneratedAt:  strings.TrimSpace(result.GeneratedAt),
		Fingerprint:  shortSHA256Hex(written),
	}
	if info != nil {
		entry.ModTime = info.ModTime().UTC().Format(time.RFC3339Nano)
		entry.Size = info.Size()
	}
	merged := make([]traceManifestEntry, 0, len(manifest.Traces)+1)
	merged = append(merged, entry)
	for _, existing := range manifest.Traces {
		if strings.TrimSpace(existing.TraceID) == traceID || strings.TrimSpace(existing.ArtifactPath) == entry.ArtifactPath {
			continue
		}
		merged = append(merged, existing)
	}
	manifest = traceManifest{
		SchemaVersion: runtimeTraceManifestSchema,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		Traces:        boundedTraceManifestEntries(merged, runtimeTraceManifestLimit),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	path = traceManifestPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func loadTraceManifest(root string) (traceManifest, bool) {
	data, err := os.ReadFile(traceManifestPath(root))
	if err != nil {
		return traceManifest{}, false
	}
	var manifest traceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return traceManifest{}, false
	}
	manifest.Traces = boundedTraceManifestEntries(manifest.Traces, runtimeTraceManifestLimit)
	if manifest.SchemaVersion == "" {
		manifest.SchemaVersion = runtimeTraceManifestSchema
	}
	return manifest, true
}

func boundedTraceManifestEntries(entries []traceManifestEntry, limit int) []traceManifestEntry {
	cleaned := make([]traceManifestEntry, 0, len(entries))
	for _, entry := range entries {
		entry.TraceID = strings.TrimSpace(entry.TraceID)
		entry.ArtifactPath = filepath.ToSlash(strings.TrimSpace(entry.ArtifactPath))
		entry.GeneratedAt = strings.TrimSpace(entry.GeneratedAt)
		entry.Fingerprint = strings.TrimSpace(entry.Fingerprint)
		entry.ModTime = strings.TrimSpace(entry.ModTime)
		if entry.TraceID == "" {
			continue
		}
		if entry.ArtifactPath == "" {
			entry.ArtifactPath = filepath.ToSlash(filepath.Join(".idiom", "comprehension", "traces", entry.TraceID+".json"))
		}
		cleaned = append(cleaned, entry)
	}
	sort.Slice(cleaned, func(i, j int) bool {
		if cleaned[i].GeneratedAt != cleaned[j].GeneratedAt {
			return cleaned[i].GeneratedAt > cleaned[j].GeneratedAt
		}
		if cleaned[i].ModTime != cleaned[j].ModTime {
			return cleaned[i].ModTime > cleaned[j].ModTime
		}
		return cleaned[i].TraceID > cleaned[j].TraceID
	})
	if limit > 0 && len(cleaned) > limit {
		cleaned = cleaned[:limit]
	}
	return cleaned
}

func traceManifestPath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "traces", runtimeTraceManifestFile)
}

func normalizeTracePath(root string, raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, `"'()[]`)
	path = strings.TrimPrefix(path, "file://")
	path = strings.TrimPrefix(path, "res://")
	path = strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if module := goModulePath(root); module != "" && strings.HasPrefix(path, module+"/") {
		path = strings.TrimPrefix(path, module+"/")
	}
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(root, filepath.Clean(path))
		if err != nil || traceRelEscapesWorkspace(rel) {
			evalRoot, rootErr := filepath.EvalSymlinks(root)
			evalPath, pathErr := filepath.EvalSymlinks(filepath.Clean(path))
			if rootErr != nil || pathErr != nil {
				return "", false
			}
			rel, err = filepath.Rel(evalRoot, evalPath)
			if err != nil || traceRelEscapesWorkspace(rel) {
				return "", false
			}
		}
		path = filepath.ToSlash(rel)
	}
	path = pathpkg.Clean(strings.ReplaceAll(filepath.ToSlash(path), "\\", "/"))
	if path == "." || path == "" || traceRelEscapesWorkspace(path) {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
		return path, true
	}
	if !strings.Contains(path, "/") {
		return resolveTraceBasename(root, path)
	}
	return "", false
}

func traceRelEscapesWorkspace(rel string) bool {
	rel = strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(rel)), "\\", "/")
	return rel == ".." || strings.HasPrefix(rel, "../")
}

func resolveTraceBasename(root string, base string) (string, bool) {
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && discoverySkipDirForTrace(root, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != base {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		matches = append(matches, filepath.ToSlash(rel))
		return nil
	})
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

func discoverySkipDirForTrace(root string, path string, name string) bool {
	if path == root {
		return false
	}
	switch name {
	case ".git", ".idiom", "node_modules", "vendor", "target", "dist", "build", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}

func coverageProfilePathsFromCommand(command string) []string {
	seen := make(map[string]struct{})
	var out []string
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:^|\s)-{1,2}coverprofile(?:=|\s+)([^\s]+)`),
		regexp.MustCompile(`(?:^|\s)-{1,2}(?:coverage|cov|lcov)(?:=|\s+)([^\s]+)`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(command, -1) {
			path := strings.Trim(match[1], `"'`)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			out = append(out, path)
		}
	}
	return out
}

func parseGoCoverageRange(value string) (int, int) {
	start, end, ok := strings.Cut(value, ",")
	if !ok {
		return 0, 0
	}
	startLine := coverageLineNumber(start)
	endLine := coverageLineNumber(end)
	return startLine, endLine
}

func coverageLineNumber(value string) int {
	line, _, _ := strings.Cut(value, ".")
	out, _ := strconv.Atoi(line)
	return out
}

func tracePackID(command string, stdout string, stderr string, generatedAt string) string {
	sum := sha256.Sum256([]byte(command + "\x00" + stdout + "\x00" + stderr + "\x00" + generatedAt))
	return hex.EncodeToString(sum[:8])
}

func traceExcerpt(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= traceExcerptLimit {
		return value
	}
	return value[:traceExcerptLimit] + "\n...[truncated]"
}

func commandAction(success bool) string {
	if success {
		return "completed"
	}
	return "failed"
}

func cleanTraceScope(path string) string {
	path = cleanScope(path)
	if path == "." {
		return ""
	}
	return path
}

func failingAnchorsFromTrace(events []TraceEvent, anchors []TraceAnchor, limit int) []TraceAnchor {
	if len(anchors) == 0 {
		return nil
	}
	failing := false
	for _, event := range events {
		if event.Action == "fail" || event.Action == "failed" || strings.Contains(strings.ToLower(event.Message), "panic") || strings.Contains(strings.ToLower(event.Message), "error") {
			failing = true
			break
		}
	}
	if !failing {
		return nil
	}
	out := append([]TraceAnchor(nil), anchors...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func dedupeTraceAnchors(anchors []TraceAnchor) []TraceAnchor {
	seen := make(map[string]struct{}, len(anchors))
	var out []TraceAnchor
	for _, anchor := range anchors {
		key := fmt.Sprintf("%s:%d:%s", anchor.Path, anchor.Line, anchor.Function)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, anchor)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func traceLineNearAnchor(text string, anchor TraceAnchor) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, anchor.Path) && strings.Contains(line, strconv.Itoa(anchor.Line)) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func isImportantRuntimeOutput(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "fail") || strings.Contains(value, "panic") || strings.Contains(value, "error") || strings.Contains(value, "traceback")
}

func findTestSymbolID(nodes []Node, test string, pkg string) string {
	for _, node := range nodes {
		if node.Kind != "test" {
			continue
		}
		if node.QualifiedName != test {
			continue
		}
		if pkg == "" || strings.Contains(node.Path, packagePathFromImport(pkg)) {
			return node.ID
		}
	}
	return ""
}

func packagePathFromImport(value string) string {
	if value == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Base(value))
}

func tracePackSummary(trace TraceResult) string {
	status := "passed"
	if !trace.Success {
		status = "failed"
	}
	return fmt.Sprintf("%s command with %d event(s), %d executed file(s), %d failing anchor(s), and %d coverage item(s)", status, len(trace.Events), len(trace.ExecutedFiles), len(trace.FailingAnchors), len(trace.Coverage))
}

func runtimeAnchorContent(anchor TraceAnchor) string {
	parts := []string{}
	if anchor.Line > 0 {
		parts = append(parts, fmt.Sprintf("line %d", anchor.Line))
	}
	if anchor.Function != "" {
		parts = append(parts, "function "+anchor.Function)
	}
	if anchor.Message != "" {
		parts = append(parts, anchor.Message)
	}
	return strings.Join(parts, "; ")
}

func traceFileContent(file TraceFile) string {
	if file.StartLine > 0 && file.EndLine > 0 && file.StartLine != file.EndLine {
		return fmt.Sprintf("%s lines %d-%d", file.Reason, file.StartLine, file.EndLine)
	}
	if file.StartLine > 0 {
		return fmt.Sprintf("%s line %d", file.Reason, file.StartLine)
	}
	return file.Reason
}

func packRoleForRuntimePath(path string) string {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "test") || strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, ".spec.ts") || strings.HasSuffix(lower, ".test.ts") {
		return "test"
	}
	return "read_only_evidence"
}

func looksRuntimeTask(task string) bool {
	task = strings.ToLower(task)
	for _, term := range []string{"fail", "failing", "panic", "trace", "stack", "coverage", "runtime", "test", "crash", "error"} {
		if strings.Contains(task, term) {
			return true
		}
	}
	return false
}

func traceMatchScore(trace TraceResult, terms []string) int {
	if len(terms) == 0 {
		if !trace.Success {
			return 1
		}
		return 0
	}
	haystack := strings.ToLower(trace.Command + " " + trace.Path + " " + trace.Language)
	for _, event := range trace.Events {
		haystack += " " + strings.ToLower(event.Package+" "+event.Test+" "+event.Path+" "+event.Message)
	}
	for _, anchor := range trace.FailingAnchors {
		haystack += " " + strings.ToLower(anchor.Path+" "+anchor.Function+" "+anchor.Message)
	}
	for _, file := range trace.ExecutedFiles {
		haystack += " " + strings.ToLower(file.Path+" "+file.Reason)
	}
	score := 0
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			score++
		}
	}
	if !trace.Success {
		score++
	}
	return score
}

func goModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}
