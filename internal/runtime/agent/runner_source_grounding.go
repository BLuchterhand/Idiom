package agent

import (
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/runtime/workerruntime"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func (r *Runner) readOnlyExplanationCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil || !readOnlyExplanationGateApplies(r.RuntimeBrief) {
		return workspace.ToolResult{}, false
	}

	if reason := readOnlyExplanationMetaRejectionReason(reply); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected meta final answer",
			Error:   reason,
		}, true
	}

	if reason := readOnlyExplanationObjectiveRejectionReason(reply, r.RuntimeBrief); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected off-objective final answer",
			Error:   reason,
		}, true
	}

	if reason := readOnlyBinaryVerdictContradictionReason(reply, r.RuntimeBrief.Objective); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected contradictory verdict",
			Error:   reason,
		}, true
	}

	if unsupported := architectureUnsupportedExactDetail(reply, results); len(unsupported) > 0 {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Architecture completion gate rejected unsupported detail",
			Error:   "the answer names additional implementation detail " + quotedRunnerStrings(unsupported) + " without an exact inspected probe or explicit uncertainty. Either anchor that detail with one exact read, or qualify it as unverified.",
		}, true
	}

	if reason := r.sourceGroundingRejectionReason(reply, results); reason != "" {
		return workspace.ToolResult{
			Tool:    "completion_gate",
			Summary: "Read-only explanation gate rejected ungrounded source citation",
			Error:   reason,
		}, true
	}

	return workspace.ToolResult{}, false
}

func readOnlyBinaryVerdictContradictionReason(answer string, objective string) string {
	if !readOnlyObjectiveLooksBinaryQuestion(objective) {
		return ""
	}

	polarities := map[string]struct{}{}
	for _, line := range strings.Split(answer, "\n") {
		if polarity := readOnlyLabeledVerdictPolarity(line); polarity != "" {
			polarities[polarity] = struct{}{}
		}
	}

	if _, hasYes := polarities["yes"]; !hasYes {
		return ""
	}

	if _, hasNo := polarities["no"]; !hasNo {
		return ""
	}

	return "the final answer gives contradictory yes/no conclusions for the requested question. Reconcile the conclusion against the inspected evidence, qualify the uncertainty, or state the exact blocker."
}

func readOnlyObjectiveLooksBinaryQuestion(objective string) bool {
	lower := strings.ToLower(strings.Join(strings.Fields(objective), " "))
	if lower == "" {
		return false
	}

	for _, marker := range []string{
		" whether ",
		" can ",
		" could ",
		" does ",
		" do ",
		" is ",
		" are ",
		" was ",
		" were ",
		" did ",
	} {
		if strings.Contains(" "+lower+" ", marker) {
			return true
		}
	}

	return strings.HasPrefix(lower, "can ") ||
		strings.HasPrefix(lower, "could ") ||
		strings.HasPrefix(lower, "does ") ||
		strings.HasPrefix(lower, "do ") ||
		strings.HasPrefix(lower, "is ") ||
		strings.HasPrefix(lower, "are ")
}

func readOnlyLabeledVerdictPolarity(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = strings.Trim(line, "*_#` \t")
	if line == "" {
		return ""
	}

	label := ""
	for _, candidate := range []string{"finding:", "verdict:", "conclusion:", "answer:"} {
		if idx := strings.Index(line, candidate); idx >= 0 {
			label = strings.TrimSpace(line[idx+len(candidate):])
			break
		}
	}

	if label == "" {
		return ""
	}

	label = strings.Trim(label, "*_` \t")
	switch {
	case strings.HasPrefix(label, "yes") || strings.HasPrefix(label, "it can") || strings.HasPrefix(label, "can "):
		return "yes"
	case strings.HasPrefix(label, "no") || strings.HasPrefix(label, "it cannot") || strings.HasPrefix(label, "it can't") || strings.HasPrefix(label, "cannot"):
		return "no"
	default:
		return ""
	}
}

func (r *Runner) sourceGroundingRejectionReason(answer string, results []workspace.ToolResult) string {
	if r == nil {
		return ""
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return ""
	}

	if !sourceGroundingGateApplies(brief) {
		return ""
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return ""
	}

	if readOnlyScaffoldFinalAnswer(answer) {
		return "the answer is only a retained-context or evidence-scaffold summary. Final answers must state the concrete behavior supported by relevant source-body or diagnostic evidence, not only list evidence surfaces."
	}

	paths := sourceGroundingMentionedPaths(answer)
	if len(paths) == 0 {
		return ""
	}

	var (
		missing     []string
		unsupported []string
	)

	for _, rawPath := range paths {
		path := normalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		citationPath := path
		if resolved, ok := sourceCitationCanonicalPath(r, path, results); ok {
			citationPath = resolved
		}

		if sourceEvidenceBodyReadForPath(citationPath, results) || slices.Contains(delegatedReadOnlySourceBodyEvidencePaths(results), citationPath) {
			continue
		}

		if !sourceCitationExists(r, citationPath) {
			if !strings.Contains(path, "/") &&
				!sourceCitationLooksLikeRootPath(path) &&
				!sourceCitationHasLineSuffix(answer, path) &&
				!sourceCitationClaimsSourceDetail(answer, path) {

				continue
			}

			if !sourceCitationExplicitlyNonCurrent(answer, path) {
				missing = append(missing, path)
			}

			continue
		}

		if !sourceGroundingPathRequiresBody(path) {
			continue
		}

		if !sourceCitationClaimsSourceDetail(answer, path) {
			continue
		}

		if sourceCitationExplicitlyNonCurrent(answer, path) || sourceCitationExplicitlyUnverified(answer, path) {
			continue
		}

		unsupported = append(unsupported, citationPath)
	}

	if len(missing) > 0 {
		return "the answer cites current-workspace path(s) that do not exist: " + quotedRunnerStrings(uniqueSortedRunnerPaths(missing)) + ". Correct the citation, label it as historical/log-only evidence, or answer from inspected current source."
	}

	if len(unsupported) > 0 {
		return "the answer cites source path/line/symbol detail not backed by a source-body read: " + quotedRunnerStrings(uniqueSortedRunnerPaths(unsupported)) + ". Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."
	}

	return ""
}

func sourceCitationCanonicalPath(r *Runner, path string, results []workspace.ToolResult) (string, bool) {
	path = normalizeRunnerPath(path)
	if path == "" {
		return "", false
	}

	if sourceCitationExists(r, path) {
		return path, true
	}

	if strings.Contains(path, "/") || sourceCitationLooksLikeRootPath(path) {
		return path, false
	}

	base := strings.ToLower(filepath.Base(path))

	var matches []string

	for _, candidate := range mergeRunnerStateStrings(sourceEvidenceBodyPaths(results), delegatedReadOnlySourceBodyEvidencePaths(results)) {
		candidate = normalizeRunnerPath(candidate)
		if candidate == "" || strings.EqualFold(candidate, path) {
			continue
		}

		if strings.ToLower(filepath.Base(candidate)) != base {
			continue
		}

		if !sourceCitationExists(r, candidate) {
			continue
		}

		matches = append(matches, candidate)
	}

	matches = uniqueSortedRunnerPaths(matches)
	if len(matches) != 1 {
		return path, false
	}

	return matches[0], true
}

func sourceGroundingMentionedPaths(answer string) []string {
	paths := append([]string(nil), pathparse.MentionedWorkspaceFiles(answer)...)
	lineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])([a-z0-9_.-]+\\.(?:go|py|ts|tsx|js|jsx|rs|java|c|cc|cpp|h|hpp|sh|zsh|gd)):\\d+")

	for _, match := range lineRE.FindAllStringSubmatch(answer, -1) {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}

	rootLineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])((?:go\\.mod|go\\.sum|makefile|dockerfile|readme\\.md|agents\\.md|package\\.json|package-lock\\.json|pnpm-lock\\.yaml|yarn\\.lock|pyproject\\.toml|poetry\\.lock|cargo\\.toml|cargo\\.lock)):\\d+")
	for _, match := range rootLineRE.FindAllStringSubmatch(answer, -1) {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceGroundingGateApplies(brief workerruntime.WorkerRuntimeBrief) bool {
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return false
	}

	if readOnlyDirectAnswerLane(brief) {
		return true
	}

	lowerObjective := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if strings.Contains(lowerObjective, "root cause") ||
		strings.Contains(lowerObjective, "debug") ||
		strings.Contains(lowerObjective, "failure") {

		return true
	}

	return false
}

func sourceCitationExists(r *Runner, path string) bool {
	path = normalizeRunnerPath(path)
	if path == "" || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return false
	}

	root := "."
	if r != nil && r.Workspace != nil && strings.TrimSpace(r.Workspace.Root()) != "" {
		root = r.Workspace.Root()
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))

	return err == nil && !info.IsDir()
}

func sourceGroundingPathRequiresBody(path string) bool {
	switch strings.ToLower(filepath.Ext(filepath.ToSlash(path))) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".py", ".pyi", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
		".kt", ".kts", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".gd":
		return true
	default:
		return false
	}
}

func sourceCitationLooksLikeRootPath(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	switch base {
	case "go.mod", "go.sum", "makefile", "dockerfile", "readme.md", "agents.md",
		"package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock":
		return true
	default:
		return false
	}
}

func sourceCitationHasLineSuffix(answer string, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}

	return regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer)
}

func sourceCitationClaimsSourceDetail(answer string, path string) bool {
	window := sourceCitationWindow(answer, path)
	lower := strings.ToLower(window)

	if strings.Contains(lower, "line ") || strings.Contains(lower, "lines ") ||
		strings.Contains(lower, "symbol") || strings.Contains(lower, "function") ||
		strings.Contains(lower, "method") || strings.Contains(lower, "handler") ||
		strings.Contains(lower, "root cause") {

		return true
	}

	if regexp.MustCompile(regexp.QuoteMeta(path) + `:\d+`).MatchString(answer) {
		return true
	}

	for _, token := range backtickedCodeLikeTokens(window) {
		if sourceGroundingTokenLooksLikeSymbol(token) {
			return true
		}
	}

	return false
}

func sourceCitationExplicitlyNonCurrent(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"does not exist", "do not exist", "doesn't exist",
		"not in the current checkout", "not current",
		"historical", "log-only", "from the log", "in the log",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationExplicitlyUnverified(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"unverified", "not yet read", "not source-read", "not source read", "not backed",
		"not enough evidence", "not enough to name", "not enough to identify",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationWindow(answer string, path string) string {
	if strings.TrimSpace(answer) == "" || strings.TrimSpace(path) == "" {
		return ""
	}

	idx := strings.Index(answer, path)
	if idx < 0 {
		return answer
	}

	start := idx - 180
	if start < 0 {
		start = 0
	}

	end := idx + len(path) + 180
	if end > len(answer) {
		end = len(answer)
	}

	return answer[start:end]
}

func backtickedCodeLikeTokens(text string) []string {
	matches := backtickedTokenPattern.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))

	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, strings.TrimSpace(match[1]))
		}
	}

	return out
}

func sourceGroundingTokenLooksLikeSymbol(token string) bool {
	if token == "" || strings.Contains(token, "/") || strings.Contains(token, " ") {
		return false
	}

	return codeShapedTokenPattern.MatchString(token) ||
		codeShapedSnakeCasePattern.MatchString(token) ||
		qualifiedIdentifierPattern.MatchString(token)
}

func planThenExecuteAnswerDeclaresTypedBlocker(answer string) bool {
	trimmed := strings.TrimSpace(answer)
	if strings.HasPrefix(trimmed, "<<QUESTION>>") ||
		strings.Contains(trimmed, "\n<<QUESTION>>") {

		return true
	}

	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "decision: block") {
			return true
		}
	}

	return false
}
