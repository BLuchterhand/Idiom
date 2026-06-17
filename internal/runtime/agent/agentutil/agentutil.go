// Package agentutil provides shared helpers used across the agent package
// and its sub-packages. Consolidating these here avoids circular dependencies
// when agent types are split into sub-packages.
package agentutil

import (
	"strings"

	"idiom/internal/capabilities/workspace"
)

// NormalizeRunnerPath cleans a workspace-relative path for consistent comparison.
func NormalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	return path
}



// ContainsAny reports whether text contains any of the given needles.
func ContainsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// ReadPathsFromToolResult extracts workspace-relative paths from a tool result's
// summary and output fields.
func ReadPathsFromToolResult(r workspace.ToolResult) []string {
	if CandidateAnchorOnlyResult(r) {
		return nil
	}

	tool := strings.TrimSpace(r.Tool)
	if (tool == "code_navigate" || tool == "read_only_capability") && !RoutedSourceReadOutputHasBody(r.Output) {
		return nil
	}

	seen := map[string]struct{}{}
	var paths []string

	for _, candidate := range []string{
		ExtractReadPathFromSummary(r.Summary),
		ExtractReadPathFromSummary(r.Output),
		ExtractRoutedReadPathFromOutput(r.Output),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		normalized := NormalizeRunnerPath(candidate)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		paths = append(paths, candidate)
	}

	return paths
}

// CandidateAnchorOnlyResult reports whether a tool result only produced a
// candidate anchor without reading source body content.
func CandidateAnchorOnlyResult(r workspace.ToolResult) bool {
	return CandidateAnchorOnlyText(r.Summary + "\n" + r.Output)
}

// ReadOnlySourceLikePaths filters paths to only include source-code-like paths
// with a limit on count.
func ReadOnlySourceLikePaths(paths []string, max int) []string {
	if max <= 0 {
		max = 8
	}

	var filtered []string

	for _, path := range paths {
		path = NormalizeRunnerPath(path)
		if path == "" || strings.HasPrefix(path, "../") || path == ".." || strings.HasPrefix(path, "/") {
			continue
		}

		if !looksLikeWorkspaceSourcePath(strings.ToLower(path)) {
			continue
		}

		filtered = append(filtered, path)
		if len(filtered) >= max {
			break
		}
	}

	return filtered
}

func looksLikeWorkspaceSourcePath(lower string) bool {
	// Skip non-source paths that commonly appear in tool output.
	if strings.HasPrefix(lower, ".idiom/") || strings.HasPrefix(lower, ".git/") {
		return false
	}

	if strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonl") ||
		strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".toml") ||
		strings.HasSuffix(lower, ".lock") || strings.HasSuffix(lower, ".sum") || strings.HasSuffix(lower, ".mod") ||
		strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".env") ||
		strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".tmp") ||
		strings.HasSuffix(lower, "go.sum") || strings.HasSuffix(lower, "go.mod") {
		return true // config/doc files are valid source-like paths in workspace context
	}

	// Skip paths that look like URLs or binary names.
	if strings.Contains(lower, "://") || !strings.Contains(lower, "/") {
		return false
	}

	return true
}

// ---------------------------------------------------------------------------
// Internal helpers extracted from runner_plan_then_execute_guard.go
// ---------------------------------------------------------------------------

// RoutedSourceReadOutputHasBody reports whether a routed tool result's output
// contains source body content (not just headers, anchors, or route summaries).
func RoutedSourceReadOutputHasBody(output string) bool {
	output = strings.TrimSpace(output)
	if output == "" {
		return false
	}

	if CandidateAnchorOnlyText(output) {
		return false
	}

	if !strings.Contains(output, "route_summary:") {
		return true
	}

	seenRouteSummary := false

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			seenRouteSummary = true
			continue
		}

		if !seenRouteSummary || line == "" {
			continue
		}

		if routedSourceReadOutputHeaderLine(line) {
			continue
		}

		return true
	}

	return false
}

func routedSourceReadOutputHeaderLine(line string) bool {
	return strings.Contains(line, "capability:") ||
		strings.Contains(line, "language:") ||
		strings.Contains(line, "routed_tool:")
}

// ExtractReadPathFromSummary parses a canonical source-body read summary
// and returns the path.
func ExtractReadPathFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}

	lower := strings.ToLower(summary)
	fields := strings.Fields(summary)
	if len(fields) < 2 {
		return ""
	}

	// "Read <symbol> in <path> ..." — language-symbol shape.
	// "Extracted <symbol> from <path> lines A-B" — extract_body shape.
	if len(fields) >= 4 && (strings.EqualFold(fields[0], "Read") || strings.EqualFold(fields[0], "Extracted")) {
		// "Read <symbol> in <path>" or "Extracted <symbol> from <path>"
		preposition := fields[2]
		if preposition == "in" || preposition == "from" {
			return fields[3]
		}
	}

	if strings.EqualFold(fields[0], "Read") && len(fields) >= 3 {
		// "Read fXX path lines A-B (total lines: N)" — read_file shape.
		// fields[1] is a file handle ("f1"), fields[3] is "lines".
		if strings.HasPrefix(fields[1], "f") && len(fields) >= 4 && strings.EqualFold(fields[3], "lines") {
			return fields[2]
		}

		// "Read path lines A-B" — fields[1] is the path, fields[2] is "lines".
		if len(fields) >= 3 && strings.EqualFold(fields[2], "lines") {
			return fields[1]
		}

		// "Read <N> lines from <path>" — path is at index 4.
		if len(fields) >= 5 && strings.EqualFold(fields[2], "lines") && strings.EqualFold(fields[3], "from") {
			return fields[4]
		}

		// "Read <symbol> in <path>" or "Extracted <symbol> from <path>"
		if len(fields) >= 4 && strings.EqualFold(fields[2], "in") && strings.EqualFold(fields[0], "Read") {
			return fields[3]
		}

		// "Read file: path" or "Read path" — fields[2] is the path (original fallback).
		return fields[2]
	}

	// Pattern: "Redirected grep for X to read_file: <path>"
	if idx := strings.Index(lower, " to read_file: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to read_file: "):])
	}

	// Pattern: "Redirected search to code_navigate: <path>"
	if idx := strings.Index(lower, " to code_navigate: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to code_navigate: "):])
	}

	// Pattern: "Redirected search to read_only_capability: <path>"
	if idx := strings.Index(lower, " to read_only_capability: "); idx >= 0 {
		return strings.TrimSpace(summary[idx+len(" to read_only_capability: "):])
	}

	// Pattern: "<path> — <summary>" (used by file_outline)
	parts := strings.SplitN(summary, " — ", 2)
	if len(parts) == 2 && hasSourceSuffix(parts[0]) {
		return strings.TrimSpace(parts[0])
	}

	return ""
}

func hasSourceSuffix(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, ".rs") ||
		strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".py") ||
		strings.HasSuffix(lower, ".java") || strings.HasSuffix(lower, ".c") ||
		strings.HasSuffix(lower, ".h") || strings.HasSuffix(lower, ".cpp") ||
		strings.HasSuffix(lower, ".hpp")
}

// ---------------------------------------------------------------------------
// Objective text helpers
// ---------------------------------------------------------------------------

// ReadOnlyObjectiveSupportTerm normalizes a term for comparison (lowercase,
// singular).
func ReadOnlyObjectiveSupportTerm(term string) string {
	term = strings.TrimSpace(strings.ToLower(term))
	if len(term) > 3 && strings.HasSuffix(term, "s") {
		term = strings.TrimSuffix(term, "s")
	}
	return term
}

// SingularObjectiveWord trims a trailing 's' from a word to produce a
// singular form, unless the word ends with "ss".
func SingularObjectiveWord(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return strings.TrimSuffix(word, "s")
	}
	return word
}

// RecoverySearchStopWords returns the set of stop words used when extracting
// search terms from an objective.
func RecoverySearchStopWords() map[string]struct{} {
	return map[string]struct{}{
		"actual": {}, "please": {}, "replace": {}, "with": {}, "from": {}, "that": {}, "this": {}, "the": {}, "and": {}, "for": {}, "you": {}, "can": {}, "top": {}, "right": {}, "left": {}, "path": {}, "alias": {},
		"continuing": {}, "continue": {}, "completion": {}, "complete": {}, "work": {}, "docs": {}, "document": {}, "design": {}, "implement": {}, "implementation": {}, "bounded": {}, "topic": {}, "only": {}, "must": {},
		"against": {}, "existing": {}, "focus": {}, "focused": {}, "add": {}, "regression": {}, "tests": {}, "test": {}, "proving": {}, "without": {}, "partial": {}, "durable": {}, "mutation": {},
		"repo": {}, "repository": {}, "discovery": {}, "first": {}, "make": {}, "minimal": {}, "code": {}, "changes": {}, "change": {}, "requested": {}, "request": {},
		"tdd": {}, "prd": {}, "adr": {}, "brief": {}, "research": {}, "workflow": {}, "workflows": {}, "markdown": {},
		// "model" intentionally kept — it carries domain signal for path→objective matching.
		"send": {}, "through": {}, "plan": {}, "execute": {}, "expose": {}, "read": {}, "external": {}, "agents": {},
	}
}

// RecoveryObjectiveSearchWords extracts content-bearing search terms from an
// objective string, filtered by stop words and minimum length.
func RecoveryObjectiveSearchWords(objective string, max int) []string {
	if max <= 0 {
		max = 8
	}

	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return nil
	}

	replacer := strings.NewReplacer("'", " ", "\"", " ", "`", " ", "’", " ", "-", " ", "_", " ", "/", " ")

	lower = replacer.Replace(lower)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	stop := RecoverySearchStopWords()
	terms := make([]string, 0, max)
	seen := make(map[string]struct{})

	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 4 {
			continue
		}

		if _, skip := stop[word]; skip {
			continue
		}

		if _, ok := seen[word]; ok {
			continue
		}

		seen[word] = struct{}{}
		terms = append(terms, word)

		if len(terms) >= max {
			break
		}
	}

	return terms
}


// ExtractRoutedReadPathFromOutput extracts the path from a route_summary line.
func ExtractRoutedReadPathFromOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route_summary:") {
			return ExtractReadPathFromSummary(strings.TrimSpace(strings.TrimPrefix(line, "route_summary:")))
		}
	}
	return ""
}

// CandidateAnchorOnlyText reports whether the given text contains only anchor
// metadata with no source body content.
func CandidateAnchorOnlyText(text string) bool {
	lower := strings.ToLower(text)

	return strings.Contains(lower, "path-filter miss") &&
		strings.Contains(lower, "candidate_anchor_status: anchors only")
}

// ObjectiveAsksImplementationShape reports whether the objective asks about
// implementation shape ("what would it take", "what needs to change", etc.)
func ObjectiveAsksImplementationShape(objective string) bool {
	lower := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "

	return ContainsAny(lower,
		" what would it take ",
		" what needs to change ",
		" what would need to change ",
		" how would ",
		" how should ",
	)
}

// ReadStyleSourceBodyTool reports whether the given tool name reads source
// body content (read_file, prefetch).
func ReadStyleSourceBodyTool(tool string) bool {
	switch strings.TrimSpace(tool) {
	case workspace.ToolNameReadFile, workspace.ToolNamePrefetch:
		return true
	default:
		return false
	}
}

// IsDiagnosticLogArtifactPath reports whether the path points to a known
// diagnostic log artifact that should be excluded from source evidence.
func IsDiagnosticLogArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case lower == "logs" || strings.HasPrefix(lower, "logs/"):
		return true
	case lower == ".idiom/logs" || strings.HasPrefix(lower, ".idiom/logs/"):
		return true
	case lower == ".idiom/comprehension/traces" || strings.HasPrefix(lower, ".idiom/comprehension/traces/"):
		return true
	case lower == "traces" || strings.HasPrefix(lower, "traces/"):
		return true
	case strings.Contains(lower, "/.idiom/logs/") || strings.Contains(lower, "/.idiom/comprehension/traces/"):
		return true
	default:
		return false
	}
}

// IsNonSourceEvidenceArtifactPath reports whether the path points to a
// non-source artifact that should be excluded from source evidence.
func IsNonSourceEvidenceArtifactPath(path string) bool {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	lower = strings.TrimPrefix(lower, "./")

	switch {
	case lower == "":
		return false
	case IsDiagnosticLogArtifactPath(lower):
		return true
	case lower == "transcripts" || strings.HasPrefix(lower, "transcripts/"):
		return true
	case lower == ".idiom" || strings.HasPrefix(lower, ".idiom/"):
		return true
	case lower == "idiom" || strings.HasPrefix(lower, "idiom/"):
		return true
	case lower == ".codex/discovery" || strings.HasPrefix(lower, ".codex/discovery/"):
		return true
	case lower == "testdata/swe/generated-logs" || strings.HasPrefix(lower, "testdata/swe/generated-logs/"):
		return true
	default:
		return false
	}
}
