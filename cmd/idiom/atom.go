package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/telemetry"
)

type atomIntakeState string

const (
	atomStateDispatchable     atomIntakeState = "dispatchable"
	atomStateComplete         atomIntakeState = "complete"
	atomStateSourceSatisfied  atomIntakeState = "source-satisfied"
	atomStateStaleNeedsRebase atomIntakeState = "stale-needs-rebase"
	atomStateFailedRefine     atomIntakeState = "failed-refine-draft"
	atomStateBlockedDirty     atomIntakeState = "blocked-dirty-files"
	atomStateMissingScope     atomIntakeState = "missing-scope"
	atomStateInvalidScope     atomIntakeState = "invalid-scope"
)

const (
	atomInvalidScopeOutsideProjectRoot     = "outside_project_root"
	atomInvalidScopeAmbiguousBareShorthand = "ambiguous_bare_shorthand"
)

type atomIntakeOptions struct {
	SpecPath    string
	ProjectRoot string
	JSON        bool
	AllowDirty  bool
}

type atomIntakeReport struct {
	Type           string            `json:"type"`
	SpecPath       string            `json:"spec_path"`
	ProjectRoot    string            `json:"project_root"`
	Status         string            `json:"status,omitempty"`
	State          atomIntakeState   `json:"state"`
	Dispatchable   bool              `json:"dispatchable"`
	ScopePaths     []string          `json:"scope_paths,omitempty"`
	Existing       []string          `json:"existing_paths,omitempty"`
	Missing        []string          `json:"missing_paths,omitempty"`
	Invalid        []string          `json:"invalid_paths,omitempty"`
	InvalidReasons map[string]string `json:"invalid_path_reasons,omitempty"`
	Dirty          []string          `json:"dirty_paths,omitempty"`
	Reasons        []string          `json:"reasons,omitempty"`
	NextActions    []string          `json:"next_actions,omitempty"`
}

func runAtomMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}
	code := runAtomMain(args, stdout, stderr)
	logCLIPhase(rec, "atom", map[string]any{"subcommand": subcommand, "exit_code": code})
	return code
}

func runAtomMain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		printAtomUsage(stderr)
		return 2
	}
	switch args[0] {
	case "intake":
		return runAtomIntakeMain(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printAtomUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "idiom atom: unknown subcommand %q\n", args[0])
		printAtomUsage(stderr)
		return 2
	}
}

func printAtomUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  idiom atom intake --spec <atom.md> [--project-root <root>] [--json] [--allow-dirty]")
}

func runAtomIntakeMain(args []string, stdout io.Writer, stderr io.Writer) int {
	opts := atomIntakeOptions{}
	fs := flag.NewFlagSet("idiom atom intake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.SpecPath, "spec", "", "atom/spec markdown file to classify before dispatch")
	fs.StringVar(&opts.ProjectRoot, "project-root", "", "workspace root for scoped path and dirty checks; default current directory")
	fs.BoolVar(&opts.JSON, "json", false, "emit machine-readable intake report")
	fs.BoolVar(&opts.AllowDirty, "allow-dirty", false, "allow dispatch even when scoped existing paths are dirty")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(opts.SpecPath) == "" {
		fmt.Fprintln(stderr, "atom intake: --spec is required")
		return 2
	}
	report, err := runAtomIntake(opts)
	if err != nil {
		fmt.Fprintf(stderr, "atom intake: %v\n", err)
		return 1
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "atom intake: cannot encode JSON: %v\n", err)
			return 1
		}
	} else {
		printAtomIntakeReport(report, stdout)
	}
	if report.Dispatchable {
		return 0
	}
	return 1
}

func runAtomIntake(opts atomIntakeOptions) (atomIntakeReport, error) {
	root := strings.TrimSpace(opts.ProjectRoot)
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return atomIntakeReport{}, err
		}
		root = wd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return atomIntakeReport{}, err
	}
	specPath := strings.TrimSpace(opts.SpecPath)
	if !filepath.IsAbs(specPath) {
		specPath = filepath.Join(absRoot, specPath)
	}
	body, err := os.ReadFile(specPath)
	if err != nil {
		return atomIntakeReport{}, err
	}
	return evaluateAtomIntake(string(body), specPath, absRoot, opts.AllowDirty), nil
}

func evaluateAtomIntake(specBody, specPath, projectRoot string, allowDirty bool) atomIntakeReport {
	mode := refineDetectMode(specBody)
	scopePaths := extractDeliverableTargets(specBody, mode)
	report := atomIntakeReport{
		Type:        "idiom.atom.intake",
		SpecPath:    specPath,
		ProjectRoot: projectRoot,
		ScopePaths:  scopePaths,
	}
	if status, ok := atomSpecStatus(specBody); ok {
		report.Status = status
	}
	if state, reason := atomStatusTerminalState(report.Status); state != "" {
		report.State = state
		report.Reasons = append(report.Reasons, reason)
		report.NextActions = append(report.NextActions, "do not dispatch this atom; update the tracker or choose another atom")
		return report
	}
	if state, reason := atomFailedRefineState(specBody); state != "" {
		report.State = state
		report.Reasons = append(report.Reasons, reason)
		report.NextActions = append(report.NextActions, "inspect the refine review, rerun refine from the source spec, or write a corrected dispatchable brief")
		return report
	}
	report.Existing, report.Missing, report.Invalid, report.InvalidReasons = classifyAtomScopePaths(projectRoot, scopePaths)
	if len(scopePaths) == 0 {
		report.State = atomStateMissingScope
		report.Reasons = append(report.Reasons, "spec does not expose any scoped file paths")
		report.NextActions = append(report.NextActions, "rebase the atom/spec with an explicit Scope or Files To Modify section")
		return report
	}
	if len(report.Invalid) > 0 {
		report.State = atomStateInvalidScope
		report.Reasons = append(report.Reasons, atomInvalidScopeSummary(report.InvalidReasons))
		report.NextActions = append(report.NextActions, atomInvalidScopeNextAction(report.InvalidReasons))
		return report
	}
	if atomSpecHasAcceptedResult(specBody) {
		report.State = atomStateStaleNeedsRebase
		report.Reasons = append(report.Reasons, "spec has a dispatchable status but also records an accepted Result")
		report.NextActions = append(report.NextActions, "refresh the atom status/tracker before dispatch")
		return report
	}
	editEval := atomEvaluateRequiredEditSnippets(specBody, projectRoot, report.Existing, scopePaths)
	if editEval.SourceSatisfied() {
		report.State = atomStateSourceSatisfied
		report.Reasons = append(report.Reasons, "all required edit snippets already appear in scoped source files")
		report.Reasons = append(report.Reasons, editEval.Evidence...)
		report.NextActions = append(report.NextActions, "do not dispatch this atom; mark it source-satisfied/accepted or choose another atom")
		return report
	}
	if editEval.StaleNeedsRebase() {
		report.State = atomStateStaleNeedsRebase
		report.Reasons = append(report.Reasons, "required edit snippets partially match or appear superseded in scoped source files")
		report.Reasons = append(report.Reasons, editEval.Evidence...)
		report.NextActions = append(report.NextActions, "refresh/rebase the atom so Required Edits reflect current source before dispatch")
		return report
	}
	if !allowDirty {
		report.Dirty = dirtyAtomScopePaths(projectRoot, scopePaths)
		if len(report.Dirty) > 0 {
			report.State = atomStateBlockedDirty
			report.Reasons = append(report.Reasons, "existing scoped paths have uncommitted changes")
			report.NextActions = append(report.NextActions, "finish, commit, or explicitly allow the dirty scoped paths before dispatch")
			return report
		}
	}
	report.State = atomStateDispatchable
	report.Dispatchable = true
	report.Reasons = append(report.Reasons, "status and scoped paths are dispatchable")
	return report
}

func printAtomIntakeReport(report atomIntakeReport, w io.Writer) {
	fmt.Fprintf(w, "state: %s\n", report.State)
	if report.Status != "" {
		fmt.Fprintf(w, "status: %s\n", report.Status)
	}
	fmt.Fprintf(w, "dispatchable: %t\n", report.Dispatchable)
	if len(report.ScopePaths) > 0 {
		fmt.Fprintf(w, "scope: %s\n", strings.Join(report.ScopePaths, ", "))
	}
	if len(report.Dirty) > 0 {
		fmt.Fprintf(w, "dirty: %s\n", strings.Join(report.Dirty, ", "))
	}
	if len(report.Invalid) > 0 {
		fmt.Fprintf(w, "invalid: %s\n", strings.Join(report.Invalid, ", "))
	}
	for _, reason := range report.Reasons {
		fmt.Fprintf(w, "reason: %s\n", reason)
	}
	for _, action := range report.NextActions {
		fmt.Fprintf(w, "next: %s\n", action)
	}
}

func atomSpecDispatchBlocker(specBody string) string {
	state, reason := atomStatusTerminalStateFromBody(specBody)
	if state != "" {
		return reason
	}
	if _, reason := atomFailedRefineState(specBody); reason != "" {
		return reason
	}
	return ""
}

func atomStatusTerminalStateFromBody(specBody string) (atomIntakeState, string) {
	status, ok := atomSpecStatus(specBody)
	if !ok {
		return "", ""
	}
	return atomStatusTerminalState(status)
}

func atomStatusTerminalState(status string) (atomIntakeState, string) {
	if strings.TrimSpace(status) == "" {
		return "", ""
	}
	lower := strings.ToLower(status)
	if strings.Contains(lower, "do not dispatch") || strings.Contains(lower, "source-satisfied") || strings.Contains(lower, "source satisfied") {
		return atomStateSourceSatisfied, fmt.Sprintf("status %q marks the atom as already satisfied or non-dispatchable", status)
	}
	switch firstAtomStatusToken(lower) {
	case "accepted", "complete", "completed", "closed":
		return atomStateComplete, fmt.Sprintf("status %q marks the atom as already complete", status)
	default:
		return "", ""
	}
}

func atomFailedRefineState(specBody string) (atomIntakeState, string) {
	reason, ok := atomRefineDraftReason(specBody)
	if !ok || reason == "" || reason == "ship-as-is" {
		return "", ""
	}
	return atomStateFailedRefine, fmt.Sprintf("refine draft reason %q is non-dispatchable until review findings are resolved", reason)
}

func atomRefineDraftReason(specBody string) (string, bool) {
	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "> WARNING: refine exited non-zero") {
			if idx := strings.Index(trimmed, "reason="); idx >= 0 {
				return atomRefineReasonValue(trimmed[idx+len("reason="):]), true
			}
			return "non-zero", true
		}
		if strings.HasPrefix(trimmed, "<!-- idiom:refine-drafted") {
			fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(trimmed, "<!--"), "-->"))
			for _, field := range fields {
				if strings.HasPrefix(field, "reason=") {
					return atomRefineReasonValue(strings.TrimPrefix(field, "reason=")), true
				}
			}
			return "", true
		}
		break
	}
	return "", false
}

func atomRefineReasonValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for i, r := range raw {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '`' || r == '>' {
			raw = raw[:i]
			break
		}
	}
	raw = strings.Trim(raw, ".,;:)")
	return strings.ToLower(raw)
}

func atomSpecStatus(specBody string) (string, bool) {
	for _, line := range strings.Split(specBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		normalized := strings.Trim(trimmed, "* ")
		if !strings.HasPrefix(strings.ToLower(normalized), "status") {
			continue
		}
		parts := strings.SplitN(normalized, ":", 2)
		if len(parts) != 2 {
			continue
		}
		label := strings.Trim(strings.TrimSpace(parts[0]), "* ")
		if !strings.EqualFold(label, "status") {
			continue
		}
		status := strings.Trim(strings.TrimSpace(parts[1]), "* ")
		if status == "" {
			continue
		}
		return status, true
	}
	return "", false
}

func firstAtomStatusToken(status string) string {
	tokens := strings.FieldsFunc(status, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '-' || r == '_' || r == '.' || r == ',' || r == ':' || r == ';'
	})
	if len(tokens) == 0 {
		return ""
	}
	return strings.TrimSpace(tokens[0])
}

func atomSpecHasAcceptedResult(specBody string) bool {
	lower := strings.ToLower(specBody)
	hasResultHeading := strings.HasPrefix(lower, "result:") ||
		strings.HasPrefix(lower, "## result") ||
		strings.HasPrefix(lower, "### result") ||
		strings.Contains(lower, "\nresult:") ||
		strings.Contains(lower, "\n## result") ||
		strings.Contains(lower, "\n### result")
	return hasResultHeading && strings.Contains(lower, "accepted")
}

type atomRequiredEditSnippet struct {
	Path    string
	Content string
	Anchor  string
}

type atomRequiredEditBlock struct {
	Paths   []string
	Content string
	Anchor  string
}

type atomRequiredEditEvaluation struct {
	SnippetCount int
	ExactMatches int
	NearMatches  int
	Missing      int
	Evidence     []string
}

func (e atomRequiredEditEvaluation) SourceSatisfied() bool {
	return e.SnippetCount > 0 && e.ExactMatches == e.SnippetCount
}

func (e atomRequiredEditEvaluation) StaleNeedsRebase() bool {
	if e.SnippetCount == 0 {
		return false
	}
	if e.SourceSatisfied() {
		return false
	}
	return e.ExactMatches > 0 || e.NearMatches > 0
}

func atomRequiredEditsSourceSatisfied(specBody, projectRoot string, existingPaths, scopePaths []string) (bool, []string) {
	eval := atomEvaluateRequiredEditSnippets(specBody, projectRoot, existingPaths, scopePaths)
	return eval.SourceSatisfied(), eval.Evidence
}

func atomEvaluateRequiredEditSnippets(specBody, projectRoot string, existingPaths, scopePaths []string) atomRequiredEditEvaluation {
	snippets := atomRequiredEditSnippetsFromSpec(specBody, scopePaths)
	eval := atomRequiredEditEvaluation{SnippetCount: len(snippets)}
	if len(snippets) == 0 || len(existingPaths) == 0 {
		return eval
	}
	existing := make(map[string]bool, len(existingPaths))
	for _, path := range existingPaths {
		existing[path] = true
	}
	contentCache := make(map[string]string, len(existingPaths))
	for i, snippet := range snippets {
		content := strings.TrimSpace(snippet.Content)
		if content == "" {
			eval.SnippetCount--
			continue
		}
		candidates := existingPaths
		if snippet.Path != "" {
			if !existing[snippet.Path] {
				eval.Missing++
				eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s is missing for required_edit_%d", snippet.Path, i+1))
				continue
			}
			candidates = []string{snippet.Path}
		}
		best := atomRequiredEditMatch{}
		for _, candidate := range candidates {
			body, ok := contentCache[candidate]
			if !ok {
				fullPath, valid := atomScopeFullPath(projectRoot, candidate)
				if !valid {
					continue
				}
				data, err := os.ReadFile(fullPath)
				if err != nil {
					continue
				}
				body = string(data)
				contentCache[candidate] = body
			}
			best = best.Better(atomRequiredEditSnippetMatch(body, content, snippet.Anchor, candidate))
		}
		switch best.Kind {
		case atomSnippetMatchExact:
			eval.ExactMatches++
			eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s contains required_edit_%d", best.Path, i+1))
		case atomSnippetMatchNear:
			eval.NearMatches++
			eval.Evidence = append(eval.Evidence, fmt.Sprintf("%s near-matches required_edit_%d (%d/%d non-empty lines); rebase before dispatch", best.Path, i+1, best.MatchedLines, best.TotalLines))
		default:
			eval.Missing++
		}
	}
	return eval
}

type atomSnippetMatchKind int

const (
	atomSnippetMatchMissing atomSnippetMatchKind = iota
	atomSnippetMatchNear
	atomSnippetMatchExact
)

type atomRequiredEditMatch struct {
	Kind         atomSnippetMatchKind
	Path         string
	MatchedLines int
	TotalLines   int
}

func (m atomRequiredEditMatch) Better(other atomRequiredEditMatch) atomRequiredEditMatch {
	if other.Kind > m.Kind {
		return other
	}
	if other.Kind == m.Kind && other.MatchedLines > m.MatchedLines {
		return other
	}
	return m
}

func atomRequiredEditSnippetMatch(content, snippet, anchor, path string) atomRequiredEditMatch {
	if atomEditSnippetContiguousMatchesContent(content, snippet) {
		lines := atomNonEmptyTrimmedLines(snippet)
		return atomRequiredEditMatch{
			Kind:         atomSnippetMatchExact,
			Path:         path,
			MatchedLines: len(lines),
			TotalLines:   len(lines),
		}
	}
	contentLines := atomNonEmptyTrimmedLines(content)
	snippetLines := atomNonEmptyTrimmedLines(snippet)
	matchedLines := atomLineMultisetMatchCount(contentLines, snippetLines)
	if atomIsNearRequiredEditMatch(matchedLines, len(snippetLines)) {
		if strings.TrimSpace(anchor) != "" && atomEditSnippetMatchesContent(content, anchor) {
			return atomRequiredEditMatch{
				Kind:         atomSnippetMatchMissing,
				Path:         path,
				MatchedLines: matchedLines,
				TotalLines:   len(snippetLines),
			}
		}
		return atomRequiredEditMatch{
			Kind:         atomSnippetMatchNear,
			Path:         path,
			MatchedLines: matchedLines,
			TotalLines:   len(snippetLines),
		}
	}
	return atomRequiredEditMatch{
		Kind:         atomSnippetMatchMissing,
		Path:         path,
		MatchedLines: matchedLines,
		TotalLines:   len(snippetLines),
	}
}

func atomOrderedLineMatchCount(contentLines, snippetLines []string) int {
	if len(contentLines) == 0 || len(snippetLines) == 0 {
		return 0
	}
	next := 0
	for _, line := range contentLines {
		if line != snippetLines[next] {
			continue
		}
		next++
		if next == len(snippetLines) {
			return next
		}
	}
	return next
}

func atomLineMultisetMatchCount(contentLines, snippetLines []string) int {
	if len(contentLines) == 0 || len(snippetLines) == 0 {
		return 0
	}
	counts := make(map[string]int, len(contentLines))
	for _, line := range contentLines {
		counts[line]++
	}
	matched := 0
	for _, line := range snippetLines {
		if counts[line] <= 0 {
			continue
		}
		counts[line]--
		matched++
	}
	return matched
}

func atomIsNearRequiredEditMatch(matchedLines, totalLines int) bool {
	if totalLines < 3 {
		return false
	}
	required := (totalLines*3 + 3) / 4
	if required < 3 {
		required = 3
	}
	return matchedLines >= required
}

func atomRequiredEditSnippetsFromSpec(specBody string, scopePaths []string) []atomRequiredEditSnippet {
	headings := []string{
		"required edits",
		"edits to apply",
		"edits",
		"implementation shape",
		"implementation",
	}
	for _, heading := range headings {
		section := atomMarkdownSectionByHeading(specBody, heading)
		if strings.TrimSpace(section) == "" {
			continue
		}
		if snippets := atomRequiredEditCodeBlocks(section, scopePaths); len(snippets) > 0 {
			return snippets
		}
	}
	return nil
}

func atomRequiredEditCodeBlocks(section string, scopePaths []string) []atomRequiredEditSnippet {
	var blocks []atomRequiredEditBlock
	var current []string
	previousNonEmpty := ""
	fence := ""
	var currentPaths []string
	var blockPaths []string
	skipCurrent := false
	pendingAnchor := atomRequiredEditBlock{}
	for _, rawLine := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(rawLine)
		if fence == "" {
			if marker, info, ok := atomMarkdownFenceOpen(trimmed); ok {
				fence = marker
				current = nil
				blockPaths = append([]string(nil), currentPaths...)
				skipCurrent = atomSkipRequiredEditCodeBlock(previousNonEmpty, info)
				continue
			}
			if trimmed != "" {
				if paths := atomRequiredEditPathsFromLabel(trimmed, scopePaths); len(paths) > 0 {
					currentPaths = paths
				}
				previousNonEmpty = trimmed
			}
			continue
		}
		if strings.HasPrefix(trimmed, fence) {
			if block := strings.TrimSpace(strings.Join(current, "\n")); block != "" {
				if skipCurrent {
					pendingAnchor = atomRequiredEditBlock{Paths: append([]string(nil), blockPaths...), Content: block}
				} else {
					anchor := ""
					if pendingAnchor.Content != "" && atomRequiredEditPathsCompatible(pendingAnchor.Paths, blockPaths) {
						anchor = pendingAnchor.Content
					}
					blocks = append(blocks, atomRequiredEditBlock{Paths: append([]string(nil), blockPaths...), Content: block, Anchor: anchor})
					pendingAnchor = atomRequiredEditBlock{}
				}
			}
			fence = ""
			current = nil
			blockPaths = nil
			skipCurrent = false
			previousNonEmpty = ""
			continue
		}
		current = append(current, rawLine)
	}
	var snippets []atomRequiredEditSnippet
	for _, block := range blocks {
		paths := block.Paths
		if len(paths) == 0 && len(scopePaths) == 1 {
			paths = []string{strings.TrimSpace(scopePaths[0])}
		}
		if len(paths) == 0 {
			paths = []string{""}
		}
		for _, path := range paths {
			snippets = append(snippets, atomRequiredEditSnippet{
				Path:    strings.TrimSpace(path),
				Content: block.Content,
				Anchor:  block.Anchor,
			})
		}
	}
	if len(scopePaths) == 1 {
		for i := range snippets {
			if snippets[i].Path == "" {
				snippets[i].Path = strings.TrimSpace(scopePaths[0])
			}
		}
	}
	return snippets
}

func atomRequiredEditPathsCompatible(anchorPaths, blockPaths []string) bool {
	if len(anchorPaths) == 0 || len(blockPaths) == 0 {
		return true
	}
	if len(anchorPaths) != len(blockPaths) {
		return false
	}
	for i := range anchorPaths {
		if strings.TrimSpace(anchorPaths[i]) != strings.TrimSpace(blockPaths[i]) {
			return false
		}
	}
	return true
}

func atomRequiredEditPathFromLabel(label string, scopePaths []string) string {
	paths := atomRequiredEditPathsFromLabel(label, scopePaths)
	if len(paths) == 1 {
		return paths[0]
	}
	return ""
}

func atomRequiredEditPathsFromLabel(label string, scopePaths []string) []string {
	label = strings.ReplaceAll(strings.TrimSpace(label), "\\", "/")
	if label == "" {
		return nil
	}
	baseCounts := make(map[string]int, len(scopePaths))
	for _, path := range scopePaths {
		baseCounts[filepath.Base(filepath.FromSlash(path))]++
	}
	matched := make(map[string]struct{}, len(scopePaths))
	for _, path := range scopePaths {
		normalized := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
		if normalized == "" {
			continue
		}
		if strings.Contains(label, "`"+normalized+"`") || strings.Contains(label, normalized) {
			matched[path] = struct{}{}
		}
		base := filepath.Base(filepath.FromSlash(normalized))
		if base != "" && baseCounts[base] == 1 && strings.Contains(label, "`"+base+"`") {
			matched[path] = struct{}{}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	out := make([]string, 0, len(matched))
	for _, path := range scopePaths {
		if _, ok := matched[path]; ok {
			out = append(out, path)
		}
	}
	return out
}

func atomMarkdownSectionByHeading(markdown string, headingName string) string {
	want := atomNormalizeMarkdownHeadingText(headingName)
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	inFence := false
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, text, ok := atomMarkdownHeading(trimmed)
		if !ok || !atomMarkdownHeadingMatches(atomNormalizeMarkdownHeadingText(text), want) {
			continue
		}
		start = i + 1
		startLevel = level
		break
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	inFence = false
	fence = ""
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if marker, _, ok := atomMarkdownFenceOpen(trimmed); ok {
			if !inFence {
				inFence = true
				fence = marker
			} else if strings.HasPrefix(trimmed, fence) {
				inFence = false
				fence = ""
			}
			continue
		}
		if inFence {
			continue
		}
		level, _, ok := atomMarkdownHeading(trimmed)
		if ok && level <= startLevel {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func atomMarkdownHeading(line string) (int, string, bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	text := strings.TrimSpace(line[level+1:])
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

func atomNormalizeMarkdownHeadingText(text string) string {
	text = strings.Trim(strings.ToLower(strings.TrimSpace(text)), ": ")
	return strings.Join(strings.Fields(text), " ")
}

func atomMarkdownHeadingMatches(candidate string, want string) bool {
	candidate = atomTrimHeadingQualifier(candidate)
	want = atomTrimHeadingQualifier(want)
	return candidate == want
}

func atomTrimHeadingQualifier(heading string) string {
	heading = strings.TrimSpace(heading)
	for _, marker := range []string{" (", " [", " - ", " -- "} {
		if idx := strings.Index(heading, marker); idx > 0 {
			return strings.TrimSpace(heading[:idx])
		}
	}
	return heading
}

func atomMarkdownFenceOpen(line string) (marker string, info string, ok bool) {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```", strings.TrimSpace(strings.TrimPrefix(line, "```")), true
	case strings.HasPrefix(line, "~~~"):
		return "~~~", strings.TrimSpace(strings.TrimPrefix(line, "~~~")), true
	default:
		return "", "", false
	}
}

func atomSkipRequiredEditCodeBlock(label string, info string) bool {
	lowerInfo := strings.ToLower(strings.TrimSpace(info))
	if strings.Contains(lowerInfo, "diff") || strings.Contains(lowerInfo, "patch") {
		return true
	}
	lowerLabel := strings.Trim(strings.ToLower(strings.TrimSpace(label)), ":. ")
	return strings.HasPrefix(lowerLabel, "old") ||
		strings.HasPrefix(lowerLabel, "before") ||
		strings.HasPrefix(lowerLabel, "existing") ||
		strings.HasPrefix(lowerLabel, "current") ||
		strings.HasPrefix(lowerLabel, "replace this")
}

func atomEditSnippetMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")
	if snippet == "" {
		return true
	}
	if strings.Contains(content, snippet) {
		return true
	}
	snippetLines := atomNonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}
	contentLines := atomNonEmptyTrimmedLines(content)
	next := 0
	for _, line := range contentLines {
		if line != snippetLines[next] {
			continue
		}
		next++
		if next == len(snippetLines) {
			return true
		}
	}
	return false
}

func atomEditSnippetContiguousMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")
	if snippet == "" {
		return true
	}
	if strings.Contains(content, snippet) {
		return true
	}
	snippetLines := atomNonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}
	contentLines := atomNonEmptyTrimmedLines(content)
	if len(snippetLines) > len(contentLines) {
		return false
	}
	for start := 0; start <= len(contentLines)-len(snippetLines); start++ {
		matched := true
		for offset, line := range snippetLines {
			if contentLines[start+offset] != line {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func atomNonEmptyTrimmedLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func classifyAtomScopePaths(projectRoot string, paths []string) (existing []string, missing []string, invalid []string, invalidReasons map[string]string) {
	for _, path := range paths {
		fullPath, ok := atomScopeFullPath(projectRoot, path)
		if !ok {
			invalidReasons = atomRecordInvalidScopeReason(invalidReasons, path, atomInvalidScopeOutsideProjectRoot)
			invalid = append(invalid, path)
			continue
		}
		if _, err := os.Stat(fullPath); err == nil {
			existing = append(existing, path)
		} else if os.IsNotExist(err) {
			if atomScopeMissingPathLooksLikeBareShorthand(path) {
				invalidReasons = atomRecordInvalidScopeReason(invalidReasons, path, atomInvalidScopeAmbiguousBareShorthand)
				invalid = append(invalid, path)
				continue
			}
			missing = append(missing, path)
		}
	}
	return existing, missing, invalid, invalidReasons
}

func atomRecordInvalidScopeReason(reasons map[string]string, path, reason string) map[string]string {
	if reasons == nil {
		reasons = map[string]string{}
	}
	reasons[path] = reason
	return reasons
}

func atomInvalidScopeSummary(invalidReasons map[string]string) string {
	hasOutsideRoot := atomInvalidScopeReasonsInclude(invalidReasons, atomInvalidScopeOutsideProjectRoot)
	hasBareShorthand := atomInvalidScopeReasonsInclude(invalidReasons, atomInvalidScopeAmbiguousBareShorthand)
	switch {
	case hasOutsideRoot && hasBareShorthand:
		return "spec scope includes paths outside the project root and ambiguous bare shorthand paths that could imply repository-root writes"
	case hasBareShorthand:
		return "spec scope includes ambiguous bare shorthand paths that could imply repository-root writes"
	default:
		return "spec scope includes paths outside the project root"
	}
}

func atomInvalidScopeNextAction(invalidReasons map[string]string) string {
	hasOutsideRoot := atomInvalidScopeReasonsInclude(invalidReasons, atomInvalidScopeOutsideProjectRoot)
	hasBareShorthand := atomInvalidScopeReasonsInclude(invalidReasons, atomInvalidScopeAmbiguousBareShorthand)
	switch {
	case hasOutsideRoot && hasBareShorthand:
		return "rebase the atom/spec so every scoped path is explicit, repository-relative, and inside the workspace"
	case hasBareShorthand:
		return "rebase the atom/spec so new scoped files use explicit repository-relative paths instead of bare basenames"
	default:
		return "rebase the atom/spec so every scoped path is repository-relative and inside the workspace"
	}
}

func atomInvalidScopeReasonsInclude(invalidReasons map[string]string, reason string) bool {
	for _, got := range invalidReasons {
		if got == reason {
			return true
		}
	}
	return false
}

func atomScopeMissingPathLooksLikeBareShorthand(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	normalized := filepath.ToSlash(path)
	if strings.Contains(normalized, "/") {
		return false
	}
	return !refineKnownFileBasenames[normalized]
}

func dirtyAtomScopePaths(projectRoot string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-C", projectRoot, "status", "--porcelain", "--"}, paths...)
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var dirty []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path := parseGitPorcelainPath(line)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		dirty = append(dirty, path)
	}
	return dirty
}

func parseGitPorcelainPath(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return ""
	}
	path := ""
	if len(line) >= 4 && line[2] == ' ' {
		path = strings.TrimSpace(line[3:])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ""
		}
		path = fields[len(fields)-1]
	}
	if idx := strings.LastIndex(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}
	path = strings.Trim(path, `"`)
	return path
}

func atomScopeFullPath(projectRoot, path string) (string, bool) {
	if filepath.IsAbs(path) {
		if !pathWithinRoot(projectRoot, path) {
			return "", false
		}
		return path, true
	}
	fullPath := filepath.Join(projectRoot, filepath.FromSlash(path))
	if !pathWithinRoot(projectRoot, fullPath) {
		return "", false
	}
	return fullPath, true
}

func pathWithinRoot(projectRoot, path string) bool {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return false
	}
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if !pathWithinRootLexical(root, fullPath) {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realPath, err := resolveExistingPathPrefix(fullPath)
	if err != nil {
		return false
	}
	return pathWithinRootLexical(realRoot, realPath)
}

func pathWithinRootLexical(projectRoot, path string) bool {
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

func resolveExistingPathPrefix(path string) (string, error) {
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := fullPath
	var missingSuffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missingSuffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missingSuffix[i])
			}
			return filepath.Abs(resolved)
		}
		if linkTarget, ok := unresolvedSymlinkTarget(current); ok {
			for i := len(missingSuffix) - 1; i >= 0; i-- {
				linkTarget = filepath.Join(linkTarget, missingSuffix[i])
			}
			return filepath.Abs(linkTarget)
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missingSuffix = append(missingSuffix, filepath.Base(current))
		current = parent
	}
}

func unresolvedSymlinkTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return target, true
}
