package agent

import (
	"context"
	"fmt"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/agent/agentutil"
	"idiom/internal/runtime/agent/stringset"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func appendUniqueRunnerStrings(values []string, additions ...string) []string {
	return stringset.AppendUnique(values, additions...)
}

func changedPathsFromToolResults(results []workspace.ToolResult) map[string]struct{} {
	paths := make(map[string]struct{})

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, path := range changedPathsFromToolResult(result) {
			paths[normalizeRunnerPath(path)] = struct{}{}
		}
	}

	if len(paths) == 0 {
		return nil
	}

	return paths
}

func unresolvedRejectedMutationPaths(results []workspace.ToolResult, requiredPaths []string) []string {
	changed := changedPathsFromToolResults(results)
	required := readOnlyPathSet(requiredPaths)

	var rejected []string

	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" || !workspace.IsWorkspaceWriteTool(result.Tool) {
			continue
		}

		paths := readOnlySourceLikePaths(agentutil.ReadPathsFromToolResult(result), 8)

		paths = mergeRunnerStateStrings(paths, readOnlySourceLikePaths(sourceLikePathsFromText(result.Summary+"\n"+result.Output+"\n"+result.Error, 8), 8))

		for _, path := range paths {
			path = normalizeRunnerPath(path)
			if path == "" {
				continue
			}

			if len(required) > 0 {
				if _, ok := required[path]; !ok {
					continue
				}
			}

			if _, ok := changed[path]; ok {
				continue
			}

			rejected = appendUniqueRunnerStrings(rejected, path)
		}
	}

	return uniqueSortedRunnerPaths(rejected)
}

func unsupportedClaimedChangedPaths(reply string, results []workspace.ToolResult) []string {
	claimed := claimedChangedPathsFromAnswer(reply)
	if len(claimed) == 0 {
		return nil
	}

	actual := changedPathsFromToolResults(results)
	if len(actual) == 0 {
		return claimed
	}

	var unsupported []string

	for _, path := range claimed {
		if _, ok := actual[path]; !ok {
			if claimedPathMatchesActualBasename(path, actual) || !looksLikeStrictChangedPathClaim(path) {
				continue
			}

			unsupported = append(unsupported, path)
		}
	}

	return unsupported
}

func claimedPathMatchesActualBasename(claimed string, actual map[string]struct{}) bool {
	claimed = normalizeRunnerPath(claimed)
	if claimed == "" || strings.Contains(claimed, "/") {
		return false
	}

	matches := 0

	for path := range actual {
		if filepath.Base(path) == claimed {
			matches++
		}
	}

	return matches == 1
}

func looksLikeStrictChangedPathClaim(path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" {
		return false
	}

	if pathparse.HasWorkspaceFileExtension(path) {
		return true
	}

	first, _, hasSlash := strings.Cut(path, "/")
	if !hasSlash {
		return false
	}

	switch first {
	case ".codex", ".github", ".idiom", ".vscode", "app", "apps", "bin", "cmd", "config", "configs", "docs", "examples", "internal", "lib", "native", "pkg", "prompts", "scripts", "src", "test", "tests", "tools", "web":
		return true
	default:
		return false
	}
}

func missingRequiredEditPaths(required []string, actual map[string]struct{}) []string {
	if len(required) == 0 {
		return nil
	}

	var missing []string

	seen := make(map[string]struct{}, len(required))

	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		if _, ok := actual[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}

	return missing
}

func (r *Runner) missingRequiredEditSnippets(results []workspace.ToolResult) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}

	changed := changedPathsFromToolResults(results)
	if len(changed) == 0 {
		return nil
	}

	contentCache := make(map[string]string)

	var missing []string

	for i, snippet := range brief.RequiredEditSnippets {
		candidates := requiredEditSnippetCandidatePaths(snippet, brief.RequiredEditPaths, changed)
		if len(candidates) == 0 {
			candidates = mapKeys(changed)
		}

		if requiredEditSnippetPresent(root, candidates, snippet.Content, contentCache) {
			continue
		}

		missing = append(missing, requiredEditSnippetName(snippet, i))
	}

	return missing
}

func (r *Runner) unreportedRemovedTestDeclarations(reply string, results []workspace.ToolResult) []string {
	removed := r.removedTestDeclarations(results)
	if len(removed) == 0 || replyReportsRemovedTests(reply, removed) {
		return nil
	}

	return removed
}

func (r *Runner) removedTestDeclarations(results []workspace.ToolResult) []string {
	if r == nil || r.Workspace == nil {
		return nil
	}

	root := strings.TrimSpace(r.Workspace.Root())
	if root == "" {
		return nil
	}

	var paths []string

	for _, path := range mapKeys(changedPathsFromToolResults(results)) {
		if testSourcePathCanRemoveDeclarations(path) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"diff", "--unified=0", "--"}, paths...)
	diff, err := gitCombinedOutput(context.Background(), root, args...)

	if err != nil || strings.TrimSpace(diff) == "" {
		return nil
	}

	return removedTestDeclarationsFromDiff(diff)
}

func testSourcePathCanRemoveDeclarations(path string) bool {
	if !sourcePathNeedsSemanticVerification(path) {
		return false
	}

	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	base := filepath.Base(lower)

	return strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(base, "test") ||
		strings.Contains(base, "spec")
}

func removedTestDeclarationsFromDiff(diff string) []string {
	type testDecl struct {
		display string
		count   int
	}

	removed := make(map[string]testDecl)
	added := make(map[string]int)

	for _, rawLine := range strings.Split(diff, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		switch {
		case strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+++"):
			continue
		case strings.HasPrefix(line, "-"):
			key, display, ok := testDeclarationFromLine(strings.TrimSpace(strings.TrimPrefix(line, "-")))
			if !ok {
				continue
			}

			entry := removed[key]

			entry.display = display
			entry.count++

			removed[key] = entry
		case strings.HasPrefix(line, "+"):
			key, _, ok := testDeclarationFromLine(strings.TrimSpace(strings.TrimPrefix(line, "+")))
			if ok {
				added[key]++
			}
		}
	}

	var missing []string

	for key, entry := range removed {
		if added[key] >= entry.count {
			continue
		}

		missing = append(missing, entry.display)
	}

	slices.Sort(missing)

	return missing
}

var (
	cppCatchTestCasePattern = regexp.MustCompile(`\b(TEST_CASE|SCENARIO)\s*\(\s*"([^"]+)"`)
	cppGTestPattern         = regexp.MustCompile(`\b(?:TEST|TEST_F|TEST_P)\s*\(\s*([A-Za-z_][\w:]*|"[^"]+")\s*,\s*([A-Za-z_][\w:]*|"[^"]+")`)
	goTestFuncPattern       = regexp.MustCompile(`^func\s+(Test[A-Za-z0-9_]+)\s*\(`)
	pythonTestFuncPattern   = regexp.MustCompile(`^def\s+(test_[A-Za-z0-9_]+)\s*\(`)
	jsTestCallPattern       = regexp.MustCompile(`\b(?:describe|it|test)\s*\(\s*['"]([^'"]+)['"]`)
	rustTestFuncPattern     = regexp.MustCompile(`^fn\s+(test_[A-Za-z0-9_]+)\s*\(`)
)

func testDeclarationFromLine(line string) (key string, display string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	if match := cppCatchTestCasePattern.FindStringSubmatch(line); len(match) == 3 {
		return "cpp:" + strings.ToLower(match[1]+":"+match[2]), match[1] + ` "` + match[2] + `"`, true
	}

	if match := cppGTestPattern.FindStringSubmatch(line); len(match) == 3 {
		suite := strings.Trim(match[1], `"`)
		name := strings.Trim(match[2], `"`)

		return "gtest:" + strings.ToLower(suite+"."+name), "TEST " + suite + "." + name, true
	}

	if match := goTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "go:" + strings.ToLower(match[1]), "func " + match[1], true
	}

	if match := pythonTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "python:" + strings.ToLower(match[1]), "def " + match[1], true
	}

	if match := jsTestCallPattern.FindStringSubmatch(line); len(match) == 2 {
		return "js:" + strings.ToLower(match[1]), `test "` + match[1] + `"`, true
	}

	if match := rustTestFuncPattern.FindStringSubmatch(line); len(match) == 2 {
		return "rust:" + strings.ToLower(match[1]), "fn " + match[1], true
	}

	return "", "", false
}

func replyReportsRemovedTests(reply string, removed []string) bool {
	lower := strings.ToLower(reply)
	if !containsAny(lower, "removed test", "deleted test", "dropped test", "removed existing test", "test removal", "removed tests", "deleted tests", "dropped tests") {
		return false
	}

	for _, declaration := range removed {
		if !replyMentionsRemovedTestDeclaration(lower, declaration) {
			return false
		}
	}

	return true
}

func replyMentionsRemovedTestDeclaration(lowerReply string, declaration string) bool {
	for _, term := range removedTestDeclarationTerms(declaration) {
		if strings.Contains(lowerReply, strings.ToLower(term)) {
			return true
		}
	}

	return false
}

func removedTestDeclarationTerms(declaration string) []string {
	declaration = strings.TrimSpace(declaration)
	if declaration == "" {
		return nil
	}

	terms := []string{declaration}
	if start := strings.Index(declaration, `"`); start >= 0 {
		if end := strings.LastIndex(declaration, `"`); end > start {
			terms = append(terms, declaration[start+1:end])
		}
	}

	fields := strings.Fields(declaration)
	if len(fields) > 1 {
		terms = append(terms, fields[len(fields)-1])
	}

	return terms
}

func requiredEditSnippetCandidatePaths(snippet workerruntime.RequiredEditSnippet, required []string, changed map[string]struct{}) []string {
	if path := normalizeRunnerPath(snippet.Path); path != "" {
		return []string{path}
	}

	var candidates []string

	seen := make(map[string]struct{})

	for _, path := range required {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if len(changed) > 0 {
			if _, ok := changed[normalized]; !ok {
				continue
			}
		}

		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}

	slices.Sort(candidates)

	return candidates
}

func requiredEditSnippetPresent(root string, candidates []string, snippet string, contentCache map[string]string) bool {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return true
	}

	for _, candidate := range candidates {
		path := normalizeRunnerPath(candidate)
		if path == "" || filepath.IsAbs(path) {
			continue
		}

		content, ok := contentCache[path]
		if !ok {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				continue
			}

			content = string(data)
			contentCache[path] = content
		}

		if editSnippetMatchesContent(content, snippet) {
			return true
		}
	}

	return false
}

func editSnippetMatchesContent(content string, snippet string) bool {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	snippet = strings.ReplaceAll(strings.TrimSpace(snippet), "\r\n", "\n")

	if snippet == "" {
		return true
	}

	if strings.Contains(content, snippet) {
		return true
	}

	snippetLines := nonEmptyTrimmedLines(snippet)
	if len(snippetLines) == 0 {
		return true
	}

	contentLines := nonEmptyTrimmedLines(content)
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

func nonEmptyTrimmedLines(text string) []string {
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

func requiredEditSnippetName(snippet workerruntime.RequiredEditSnippet, index int) string {
	if id := strings.TrimSpace(snippet.ID); id != "" {
		return id
	}

	if path := strings.TrimSpace(snippet.Path); path != "" {
		return fmt.Sprintf("required_edit_%d (%s)", index+1, path)
	}

	return fmt.Sprintf("required_edit_%d", index+1)
}

func claimedChangedPathsFromAnswer(reply string) []string {
	var paths []string

	seen := make(map[string]struct{})
	inChanges := false
	linesRemaining := 0
	pathsSeenInSection := false

	for _, rawLine := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)

		if changedPathsSectionHeader(lower) {
			inChanges = true
			linesRemaining = 14
			pathsSeenInSection = false
		} else if inChanges {
			if linesRemaining <= 0 || changedPathsSectionStop(lower, pathsSeenInSection) {
				inChanges = false
				linesRemaining = 0
				pathsSeenInSection = false
			} else {
				linesRemaining--
			}
		}

		if !inChanges {
			continue
		}

		lineCandidates := pathparse.MentionedWorkspacePaths(normalizeWorkspacePathScanText(line))
		for _, fragment := range pathparse.QuotedFragments(line) {
			if normalized, ok := pathparse.NormalizeWorkspacePathToken(fragment); ok {
				lineCandidates = append(lineCandidates, normalized)
			}
		}

		for _, path := range lineCandidates {
			path = normalizeRunnerPath(path)
			if path == "" {
				continue
			}

			pathsSeenInSection = true

			if _, ok := seen[path]; ok {
				continue
			}

			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	slices.Sort(paths)

	return paths
}

func changedPathsSectionHeader(lower string) bool {
	lower = strings.Trim(strings.TrimSpace(lower), "*#:")
	lower = strings.TrimSpace(lower)

	return strings.Contains(lower, "changed paths") ||
		strings.Contains(lower, "changed files") ||
		strings.Contains(lower, "files changed") ||
		strings.Contains(lower, "modified files") ||
		strings.Contains(lower, "changes made")
}

func changedPathsSectionStop(lower string, pathsSeen bool) bool {
	if lower == "" {
		return false
	}

	trimmed := strings.TrimSpace(lower)
	label := strings.TrimSpace(strings.TrimLeft(trimmed, "-* "))

	if pathsSeen && changedPathsSectionStatusLabel(label) {
		return true
	}

	headingLike := strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "**") ||
		strings.HasSuffix(trimmed, ":")
	if !headingLike {
		return false
	}

	if !pathsSeen && changedPathsSectionHeader(lower) {
		return false
	}

	return containsAny(lower, "verification", "test", "planner", "blocker", "validation", "lane sanity")
}

func changedPathsSectionStatusLabel(label string) bool {
	label = strings.Trim(strings.TrimSpace(label), "`")

	return strings.HasPrefix(label, "verification:") ||
		strings.HasPrefix(label, "validation:") ||
		strings.HasPrefix(label, "planner-owned ") ||
		strings.HasPrefix(label, "planner owned ") ||
		strings.HasPrefix(label, "blocker:") ||
		strings.HasPrefix(label, "lane sanity")
}

func changedPathsFromToolResult(result workspace.ToolResult) []string {
	switch strings.TrimSpace(result.Tool) {
	case workspace.ToolNameEditFile, workspace.ToolNameWriteFile, workspace.ToolNameAppendFile, workspace.ToolNameMkdir:
		return changedPathFromSummary(result.Summary)
	case workspace.ToolNameGoMoveDeclarations:
		return changedGoMovePathsFromSummary(result.Summary)
	case workspace.ToolNameApplyPatch:
		return changedPathsFromPatchOutput(result.Output)
	case workspace.ToolNameDelegateEdit:
		// wrapDelegateEditResult preserves the underlying edit_file /
		// apply_patch summary so we can parse it the same way. Without
		// this case the planner's required_edit_paths_progress would
		// never tick paths off after a delegate_edit success.
		if paths := changedPathFromSummary(result.Summary); len(paths) > 0 {
			return paths
		}

		return changedPathsFromPatchOutput(result.Output)
	default:
		return nil
	}
}

func changedPathsFromPatchOutput(output string) []string {
	var paths []string

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "add "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "add ")))
		case strings.HasPrefix(line, "delete "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "delete ")))
		case strings.HasPrefix(line, "update+move "):
			rest := strings.TrimPrefix(line, "update+move ")
			source, destination, moved := strings.Cut(rest, " -> ")

			if moved {
				paths = append(paths, patchOutputPath(source), patchOutputPath(destination))
			} else {
				paths = append(paths, patchOutputPath(rest))
			}
		case strings.HasPrefix(line, "update "):
			paths = append(paths, patchOutputPath(strings.TrimPrefix(line, "update ")))
		}
	}

	return paths
}

func patchOutputPath(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, " ("); ok {
		value = before
	}

	return strings.TrimSpace(value)
}

func changedPathFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) < 2 {
		return nil
	}

	switch fields[0] {
	case "Edited", "Wrote", "Appended":
		return []string{fields[1]}
	case "Created":
		if len(fields) >= 3 && fields[1] == "directory" {
			return []string{fields[2]}
		}
	}

	return nil
}

func changedGoMovePathsFromSummary(summary string) []string {
	fields := strings.Fields(strings.TrimSpace(summary))
	if len(fields) == 0 || fields[0] != "Moved" {
		return nil
	}

	var paths []string

	for i, field := range fields {
		switch field {
		case "from", "to":
			if i+1 < len(fields) {
				paths = append(paths, fields[i+1])
			}
		}
	}

	return paths
}

func normalizeRunnerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")

	return path
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func isWriteTool(req workspace.ToolRequest) bool {
	return workspace.IsWorkspaceWriteTool(req.Tool)
}

func isVerificationRequest(req workspace.ToolRequest) bool {
	tool := strings.TrimSpace(req.Tool)
	if tool == workspace.ToolNameSourceSanity {
		return true
	}

	if isDiagnosticsVerificationTool(tool) {
		return true
	}

	switch tool {
	case workspace.ToolNameShell:
		return isVerificationCommandText(req.Command)
	}

	return false
}

func hasPendingVerificationAfterWrites(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	if verificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	docLane := briefIsDocDeliverableLane(brief)
	pending := false

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) {
			// Doc-deliverable lanes (refine drafts/reviews/patches, design
			// notes, etc.) have RequiredEditPaths populated with .md/.txt
			// targets. For those there's no source-aware sanity to run, so
			// non-source writes don't set the pending flag. Implementation
			// lanes that touch a markdown file still verify — patch hygiene
			// (`git diff --check`) IS available and satisfies the gate. A
			// SUBSEQUENT source write still sets pending — mixed atoms
			// require source-aware sanity regardless of lane shape.
			if writeTouchesOnlyNonSourcePaths(result) || (docLane && mkdirTouchesOnlyRequiredPathParents(result, brief.RequiredEditPaths)) {
				continue
			}

			pending = true

			continue
		}

		if pending && resultCountsAsSuccessfulVerification(result) {
			pending = false
		}
	}

	return pending
}

// briefIsDocDeliverableLane reports whether the brief specs a doc-only
// deliverable (refine draft/review/patch, design note write-up, etc.).
// The signal is "every RequiredEditPath is a non-source file". Empty
// RequiredEditPaths is treated as not-doc-only so implementation lanes
// with no specced allowlist still trip the verification gate when they
// happen to touch a markdown file.
