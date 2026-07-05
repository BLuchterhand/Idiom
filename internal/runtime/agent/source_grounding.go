package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

type sourceGroundingRejectionKind string

const (
	sourceGroundingRejectionUnknown           sourceGroundingRejectionKind = ""
	sourceGroundingRejectionCandidateOnly     sourceGroundingRejectionKind = "candidate_only"
	sourceGroundingRejectionMissingCandidate  sourceGroundingRejectionKind = "missing_candidate"
	sourceGroundingRejectionUnsupportedDetail sourceGroundingRejectionKind = "unsupported_detail"
	sourceGroundingRejectionExactDetail       sourceGroundingRejectionKind = "exact_detail"
	sourceGroundingRejectionScaffold          sourceGroundingRejectionKind = "scaffold"
)

const (
	sourceGroundingCandidateOnlyMarker     = "Candidate/search/context paths are not source-body evidence"
	sourceGroundingCandidateOnlyPathMarker = "Candidate path(s):"
	sourceGroundingMissingCandidateMarker  = "not read as source-body evidence"
	sourceGroundingUnsupportedDetailMarker = "not backed by a source-body read"
)

type sourceGroundingAssessment struct {
	Kind  sourceGroundingRejectionKind
	Paths []string
	Text  string
}

func classifySourceGroundingRejectionText(text string) (sourceGroundingAssessment, bool) {
	text = strings.TrimSpace(sourceGroundingStripGateRecoveryPrefix(text))
	if text == "" {
		return sourceGroundingAssessment{}, false
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, sourceGroundingCandidateOnlyMarker):
		return sourceGroundingAssessment{
			Kind:  sourceGroundingRejectionCandidateOnly,
			Paths: sourceGroundingPathsAfterMarker(text, sourceGroundingCandidateOnlyPathMarker),
			Text:  text,
		}, true
	case strings.Contains(text, sourceGroundingMissingCandidateMarker):
		return sourceGroundingAssessment{
			Kind:  sourceGroundingRejectionMissingCandidate,
			Paths: sourceGroundingPathsAfterMarker(text, sourceGroundingMissingCandidateMarker+":"),
			Text:  text,
		}, true
	case strings.Contains(text, sourceGroundingUnsupportedDetailMarker):
		return sourceGroundingAssessment{
			Kind:  sourceGroundingRejectionUnsupportedDetail,
			Paths: sourceGroundingPathsAfterMarker(text, sourceGroundingUnsupportedDetailMarker+":"),
			Text:  text,
		}, true
	case strings.Contains(lower, "source-body evidence") && strings.Contains(lower, "answer cites path"):
		return sourceGroundingAssessment{
			Kind:  sourceGroundingRejectionMissingCandidate,
			Paths: sourceGroundingPathsAfterMarker(text, ":"),
			Text:  text,
		}, true
	case strings.Contains(lower, "without an exact inspected probe"):
		return sourceGroundingAssessment{
			Kind:  sourceGroundingRejectionExactDetail,
			Paths: sourceGroundingPathsAfterMarker(text, ":"),
			Text:  text,
		}, true
	default:
		return sourceGroundingAssessment{}, false
	}
}

func sourceGroundingCandidateOnlyAssessment(paths []string) sourceGroundingAssessment {
	return sourceGroundingAssessment{
		Kind:  sourceGroundingRejectionCandidateOnly,
		Paths: uniqueSortedRunnerPaths(nonEmptyRunnerStrings(paths, 8)),
	}
}

func sourceGroundingMissingCandidateAssessment(paths []string) sourceGroundingAssessment {
	return sourceGroundingAssessment{
		Kind:  sourceGroundingRejectionMissingCandidate,
		Paths: uniqueSortedRunnerPaths(paths),
	}
}

func sourceGroundingUnsupportedDetailAssessment(paths []string) sourceGroundingAssessment {
	return sourceGroundingAssessment{
		Kind:  sourceGroundingRejectionUnsupportedDetail,
		Paths: uniqueSortedRunnerPaths(paths),
	}
}

func sourceGroundingScaffoldAssessment(text string) sourceGroundingAssessment {
	return sourceGroundingAssessment{
		Kind: sourceGroundingRejectionScaffold,
		Text: strings.TrimSpace(text),
	}
}

func sourceGroundingCandidateOnlyCompletionAssessment(brief workerruntime.WorkerRuntimeBrief, answer string, results []workspace.ToolResult) (sourceGroundingAssessment, bool) {
	ledger, candidateOnly := sourceGroundingCandidateOnlyLedger(brief, results)
	if !candidateOnly {
		return sourceGroundingAssessment{}, false
	}
	if sourceGroundingDiagnosticArtifactCitationSupportsObjective(brief, answer, results) {
		return sourceGroundingAssessment{}, true
	}

	candidates := closurePromptEvidencePaths(ledger.CandidatePaths, 4)
	if len(candidates) == 0 {
		return sourceGroundingAssessment{}, true
	}

	return sourceGroundingCandidateOnlyAssessment(candidates), true
}

func sourceGroundingCandidateOnlyLedger(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) (readOnlyAnswerabilityLedger, bool) {
	if !readOnlyDirectAnswerLane(brief) {
		return readOnlyAnswerabilityLedger{}, false
	}

	ledger := readOnlyAnswerabilityForResults(readOnlyDirectAnswerAcceptanceLedgerBrief(brief), results)

	return ledger, len(ledger.CandidatePaths) > 0 && len(ledger.SourceBodyPaths) == 0 && !ledger.DiagnosticEvidence
}

func sourceGroundingCandidateOnlyContextPaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	ledger, candidateOnly := sourceGroundingCandidateOnlyLedger(brief, results)
	if !candidateOnly {
		return nil
	}

	return closurePromptEvidencePaths(ledger.CandidatePaths, max)
}

func (a sourceGroundingAssessment) BlocksFinalAnswer() bool {
	return strings.TrimSpace(a.Message()) != ""
}

func (a sourceGroundingAssessment) Message() string {
	switch a.Kind {
	case sourceGroundingRejectionCandidateOnly:
		paths := nonEmptyRunnerStrings(a.Paths, 8)
		pathSummary := strings.Join(paths, ", ")
		if pathSummary == "" {
			pathSummary = "(candidate path unavailable)"
		}
		return "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. " + sourceGroundingCandidateOnlyMarker + ". " + sourceGroundingCandidateOnlyPathMarker + " " + pathSummary + ". Inspect one candidate with source-body evidence, use one materially different source-owner probe if these candidates do not establish the claim, or answer `<<QUESTION>>:` with the exact unavailable evidence source."
	case sourceGroundingRejectionMissingCandidate:
		return "the answer cites path(s) that were discovered as index candidates but " + sourceGroundingMissingCandidateMarker + ": " + quotedRunnerStrings(uniqueSortedRunnerPaths(a.Paths)) + ". Read the first unread path with one bounded `read_file` or `code_navigate` call and cite the actual evidence from the file body. If the path was from search or graph results and does not need a full read, qualify the claim as `unverified` next to the citation."
	case sourceGroundingRejectionUnsupportedDetail:
		return "the answer cites source path/line/symbol detail " + sourceGroundingUnsupportedDetailMarker + ": " + quotedRunnerStrings(uniqueSortedRunnerPaths(a.Paths)) + ". Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."
	case sourceGroundingRejectionExactDetail, sourceGroundingRejectionScaffold:
		return strings.TrimSpace(a.Text)
	default:
		return ""
	}
}

func (a sourceGroundingAssessment) RecoveryPrompt(ledgerGuidance string) string {
	if !a.BlocksFinalAnswer() {
		return ""
	}

	lines := []string{
		"The completion gate rejected the answer because source paths were cited without body evidence: " + formatInlinePathList(a.Paths, 4) + ".",
		"Valid next strategies: read one unsupported path with a bounded `read_file` or `code_navigate` body request; rewrite the answer so it cites only body-backed evidence; qualify candidate-only paths as `unverified`; or answer `<<QUESTION>>:` with the exact blocker if the evidence source is unavailable.",
		"Do not answer with only an evidence-path inventory, readiness summary, or gate-satisfaction statement; answer the user's actual question using only supported evidence.",
	}
	if strings.TrimSpace(ledgerGuidance) != "" {
		lines = append(lines, strings.TrimSpace(ledgerGuidance))
	}
	lines = append(lines, "Do not broaden into new search/discovery unless you can name the exact missing claim that the current evidence ledger cannot answer.")

	return strings.Join(lines, "\n\n")
}

func (a sourceGroundingAssessment) PresentableDetail() string {
	pathSuffix := ""
	if len(a.Paths) > 0 {
		pathSuffix = ": " + strings.Join(uniqueSortedRunnerPaths(a.Paths), ", ")
	}

	switch a.Kind {
	case sourceGroundingRejectionCandidateOnly, sourceGroundingRejectionMissingCandidate:
		return "the answer cited path(s) that were not supported by inspected evidence" + pathSuffix
	case sourceGroundingRejectionUnsupportedDetail:
		return "the answer cited source path, line, or symbol detail that was not supported by inspected evidence" + pathSuffix
	case sourceGroundingRejectionExactDetail:
		detail := firstSentenceBeforeAny(a.Text,
			" Either anchor that detail",
			" Either issue ONE concrete tool call",
			" Answer with the exact",
			" Use one exact read",
			"\n\nEvidence paths observed before rejection:",
		)
		detail = strings.TrimSpace(sourceGroundingStripGateRecoveryPrefix(detail))
		if detail != "" {
			return lowercaseFirstSentence(detail)
		}
		return "the answer cited source path, line, or symbol detail that was not supported by inspected evidence" + pathSuffix
	default:
		return ""
	}
}

func (r *Runner) assessSourceGrounding(answer string, results []workspace.ToolResult) sourceGroundingAssessment {
	if r == nil {
		return sourceGroundingAssessment{}
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return sourceGroundingAssessment{}
	}

	if !sourceGroundingGateApplies(brief) {
		return sourceGroundingAssessment{}
	}

	if strings.HasPrefix(strings.TrimSpace(answer), "<<QUESTION>>") {
		return sourceGroundingAssessment{}
	}

	if readOnlyScaffoldFinalAnswer(answer) {
		return sourceGroundingScaffoldAssessment("the answer is only a retained-context or evidence-scaffold summary. Final answers must state the concrete behavior supported by relevant source-body or diagnostic evidence, not only list evidence surfaces.")
	}

	validationLedgerEvidence := r.validationStatusAnswerCanUseLedgerEvidence(answer, results)

	if escaping := sourceGroundingUnsupportedEscapingMentionedPaths(answer, results); len(escaping) > 0 {
		return sourceGroundingUnsupportedDetailAssessment(escaping)
	}

	paths := sourceGroundingMentionedPaths(answer)
	if len(paths) == 0 {
		return sourceGroundingAssessment{}
	}

	var (
		missing     []string
		unsupported []string
	)

	for _, rawPath := range paths {
		path := agentutil.NormalizeRunnerPath(rawPath)
		if path == "" {
			continue
		}

		citationPath := path
		if resolved, ok := sourceCitationCanonicalPath(r, path, results); ok {
			citationPath = resolved
		}

		if validationLedgerEvidence {
			if validationStatusPathBackedByLedger(path, citationPath, results) &&
				!sourceCitationClaimsSourceDetail(answer, path) {

				continue
			}
		}

		if sourceEvidenceHasBody(r.evidenceEngine().EvidenceForPath(results, citationPath)) ||
			sourceEvidenceBodyReadForPathWithRegistry(citationPath, results, r.PathEvidenceRegistry) ||
			slices.Contains(delegatedReadOnlySourceBodyEvidencePaths(results), citationPath) ||
			sourceGroundingDiagnosticArtifactReadForPath(path, results) ||
			sourceGroundingDiagnosticArtifactReadForPath(citationPath, results) {
			continue
		}

		citationExists := sourceCitationExists(r, citationPath)
		if !citationExists && sourceCitationIsSchemaPlaceholder(answer, path) {
			continue
		}

		if !citationExists {
			if !strings.Contains(path, "/") &&
				!sourceCitationLooksLikeRootPath(path) &&
				!sourceCitationHasLineSuffix(answer, path) &&
				!sourceCitationClaimsSourceDetail(answer, path) {

				continue
			}

			if !sourceCitationExplicitlyNonCurrent(answer, path) &&
				!sourceCitationBackedByProposedPlanArtifact(r, answer, path, results) &&
				!sourceCitationBackedByFuturePlanRecommendation(r, answer, path, results) {
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

		if sourceCitationExplicitlyNonCurrent(answer, path) ||
			sourceCitationBackedByProposedPlanArtifact(r, answer, path, results) ||
			sourceCitationExplicitlyUnverified(answer, path) {
			continue
		}

		unsupported = append(unsupported, citationPath)
	}

	if len(missing) > 0 {
		return sourceGroundingMissingCandidateAssessment(missing)
	}

	if len(unsupported) > 0 {
		return sourceGroundingUnsupportedDetailAssessment(unsupported)
	}

	return sourceGroundingAssessment{}
}

func (r *Runner) sourceGroundingRejectionReason(answer string, results []workspace.ToolResult) string {
	return r.assessSourceGrounding(answer, results).Message()
}

func sourceGroundingPathsAfterMarker(text string, marker string) []string {
	text = strings.TrimSpace(sourceGroundingStripGateRecoveryPrefix(text))
	marker = strings.TrimSpace(marker)
	if text == "" || marker == "" {
		return nil
	}

	lower := strings.ToLower(text)
	markerLower := strings.ToLower(marker)
	idx := strings.Index(lower, markerLower)
	if idx < 0 {
		return nil
	}

	pathText := strings.TrimSpace(text[idx+len(marker):])
	if pathText == "" {
		return nil
	}

	var paths []string
	for _, token := range strings.Split(pathText, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if idx := strings.Index(token, ". "); idx > 0 {
			token = token[:idx]
		} else if strings.HasSuffix(token, ".") {
			token = strings.TrimSuffix(token, ".")
		}
		if idx := strings.Index(token, " — "); idx > 0 {
			token = token[:idx]
		}
		token = strings.Trim(token, "`'\" ")
		if token != "" {
			paths = append(paths, token)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceGroundingStripGateRecoveryPrefix(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[gate retry") &&
		!strings.HasPrefix(text, "[source evidence recovery") {

		return text
	}

	if idx := strings.Index(text, "]"); idx >= 0 {
		return strings.TrimSpace(text[idx+1:])
	}

	return text
}

func sourceCitationCanonicalPath(r *Runner, path string, results []workspace.ToolResult) (string, bool) {
	path = agentutil.NormalizeRunnerPath(path)
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

	for _, candidate := range textutil.Merge(sourceEvidenceBodyPaths(results), delegatedReadOnlySourceBodyEvidencePaths(results)) {
		candidate = agentutil.NormalizeRunnerPath(candidate)
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
	if len(matches) == 1 {
		return matches[0], true
	}

	if r != nil && r.PathEvidenceRegistry != nil {
		if resolved, ok := r.PathEvidenceRegistry.ResolveShortPath(path); ok {
			return resolved, true
		}
	}

	return path, false
}

func sourceGroundingMentionedPaths(answer string) []string {
	scanText := stripQuotedAndFencedSourceExamples(answer)
	paths := filterSourceGroundingMentionedPaths(pathparse.MentionedWorkspaceFiles(scanText))
	lineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])([a-z0-9_.-]+\\.(?:go|py|ts|tsx|js|jsx|rs|java|c|cc|cpp|h|hpp|sh|zsh|gd)):\\d+")

	for _, match := range lineRE.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 && sourceGroundingMentionedPathLooksPlausible(match[1]) {
			paths = append(paths, match[1])
		}
	}

	rootLineRE := regexp.MustCompile("(?i)(?:^|[\\s`'\"(])((?:go\\.mod|go\\.sum|makefile|dockerfile|readme\\.md|agents\\.md|package\\.json|package-lock\\.json|pnpm-lock\\.yaml|yarn\\.lock|pyproject\\.toml|poetry\\.lock|cargo\\.toml|cargo\\.lock)):\\d+")
	for _, match := range rootLineRE.FindAllStringSubmatch(scanText, -1) {
		if len(match) > 1 && sourceGroundingMentionedPathLooksPlausible(match[1]) {
			paths = append(paths, match[1])
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceGroundingEscapingMentionedPaths(answer string) []string {
	scanText := stripQuotedAndFencedSourceExamples(answer)
	seen := map[string]struct{}{}
	var paths []string

	for _, raw := range strings.Fields(scanText) {
		raw = strings.Trim(raw, "`'\"()[]{}<>,;")
		if !readOnlyPathEscapesWorkspace(raw) {
			continue
		}

		path := sourceGroundingEscapingPathDisplay(raw)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			continue
		}

		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	return textutil.Sorted(paths)
}

func sourceGroundingUnsupportedEscapingMentionedPaths(answer string, results []workspace.ToolResult) []string {
	paths := sourceGroundingEscapingMentionedPaths(answer)
	if len(paths) == 0 {
		return nil
	}

	var unsupported []string
	for _, path := range paths {
		if sourceGroundingDiagnosticArtifactReadForPath(path, results) {
			continue
		}
		if sourceCitationExplicitlyUnverified(answer, path) || sourceCitationExplicitlyNonCurrent(answer, path) {
			continue
		}
		unsupported = append(unsupported, path)
	}

	return unsupported
}

func sourceGroundingEscapingPathDisplay(raw string) string {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, "`'\"()[]{}<>,;")
	if path == "" {
		return ""
	}

	for _, ext := range []string{".jsonl", ".json", ".yaml", ".toml", ".tsx", ".jsx", ".cpp", ".hpp", ".zsh", ".go", ".py", ".ts", ".js", ".rs", ".java", ".cc", ".c", ".h", ".sh", ".gd", ".md", ".yml", ".txt"} {
		if idx := indexFold(path, ext); idx >= 0 {
			return path[:idx+len(ext)]
		}
	}

	return path
}

func stripQuotedAndFencedSourceExamples(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	var out []string
	inFence := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}

		if inFence || strings.HasPrefix(trimmed, ">") {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func filterSourceGroundingMentionedPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if sourceGroundingMentionedPathLooksPlausible(path) {
			out = append(out, path)
		}
	}

	return out
}

func sourceGroundingMentionedPathLooksPlausible(path string) bool {
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	base := filepath.Base(filepath.ToSlash(path))
	if base == "." || base == "/" || base == "" {
		return false
	}

	ext := filepath.Ext(base)
	if ext != "" && base == ext {
		return false
	}

	return true
}

func sourceGroundingMentionedPathIsSchemaPlaceholder(path string) bool {
	path = strings.ToLower(strings.Trim(filepath.ToSlash(agentutil.NormalizeRunnerPath(path)), "/"))
	if path == "" {
		return false
	}

	switch path {
	case "path/file.go", "path/to/file.go", "path/to/source.go",
		"file/path.go", "some/path.go", "example/file.go",
		"example/path.go", "src/path/file.go":
		return true
	default:
		return false
	}
}

func sourceCitationIsSchemaPlaceholder(answer string, path string) bool {
	if !sourceGroundingMentionedPathIsSchemaPlaceholder(path) {
		return false
	}

	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return agentutil.ContainsAny(lower,
		"schema placeholder",
		"schema example",
		"example placeholder",
		"placeholder path",
		"placeholder source",
		"tool schema",
		"recovery prompt",
		"not a repository citation",
		"not a missing source file",
	)
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
	path = agentutil.NormalizeRunnerPath(path)
	if path == "" || strings.HasPrefix(path, "../") || path == ".." || filepath.IsAbs(path) {
		return false
	}

	if r != nil && r.PathEvidenceRegistry != nil {
		return r.PathEvidenceRegistry.EnsurePathExists(path)
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
		"not in the workspace", "outside the workspace", "outside workspace",
		"historical", "log-only", "from the log", "in the log",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationBackedByProposedPlanArtifact(r *Runner, answer string, path string, results []workspace.ToolResult) bool {
	return featurePlannerPlanPathCitationAuthority(r, answer, path, results).Kind == featurePlannerPlanCitationArtifactProposed
}

func sourceCitationBackedByFuturePlanRecommendation(r *Runner, answer string, path string, results []workspace.ToolResult) bool {
	return featurePlannerPlanPathCitationAuthority(r, answer, path, results).Kind == featurePlannerPlanCitationFutureRecommendation
}

type featurePlannerPlanCitationKind string

const (
	featurePlannerPlanCitationNone                 featurePlannerPlanCitationKind = ""
	featurePlannerPlanCitationArtifactProposed     featurePlannerPlanCitationKind = "artifact_proposed"
	featurePlannerPlanCitationFutureRecommendation featurePlannerPlanCitationKind = "future_recommendation"
)

type featurePlannerPlanCitationAuthority struct {
	Kind                 featurePlannerPlanCitationKind
	ResolvedPlanPath     string
	BackedByPlanArtifact bool
}

type featurePlannerPlanArtifactCitationContext struct {
	Objective            string
	ProposedPaths        []string
	BackedByPlanArtifact bool
	AnswerHasPlanContext bool
}

func featurePlannerPlanArtifactCitationContextForAnswer(answer string, objective string, proposedPaths []string, backedByPlanArtifact bool) featurePlannerPlanArtifactCitationContext {
	return featurePlannerPlanArtifactCitationContext{
		Objective:            strings.TrimSpace(objective),
		ProposedPaths:        proposedPaths,
		BackedByPlanArtifact: backedByPlanArtifact,
		AnswerHasPlanContext: answerHasFeaturePlannerPlanArtifactContext(answer, objective),
	}
}

func featurePlannerPlanPathCitationAuthority(r *Runner, answer string, path string, results []workspace.ToolResult) featurePlannerPlanCitationAuthority {
	if r == nil {
		return featurePlannerPlanCitationAuthority{}
	}

	path = agentutil.NormalizeRunnerPath(path)
	if path == "" {
		return featurePlannerPlanCitationAuthority{}
	}

	for _, result := range results {
		if !featurePlannerPlanArtifactResult(result) {
			continue
		}

		brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
		ctx := featurePlannerPlanArtifactCitationContextForAnswer(answer, brief.Objective, featurePlannerPlanArtifactProposedPaths(result), true)
		authority := featurePlannerPlanArtifactPathCitationAuthority(answer, path, ctx)
		if authority.Kind != featurePlannerPlanCitationArtifactProposed {
			continue
		}

		resolved := authority.ResolvedPlanPath
		if sourceCitationClaimsPlanResult(answer, path) || sourceCitationClaimsPlanResult(answer, resolved) {
			return featurePlannerPlanCitationAuthority{}
		}
		if (sourceCitationExists(r, path) || sourceCitationExists(r, resolved)) &&
			(sourceCitationClaimsSourceDetail(answer, path) || sourceCitationClaimsSourceDetail(answer, resolved)) {

			return featurePlannerPlanCitationAuthority{}
		}

		return authority
	}

	if sourceCitationClaimsPlanResult(answer, path) ||
		sourceCitationClaimsSourceDetail(answer, path) ||
		sourceCitationClaimsCurrentOrPlannedPath(answer, path) ||
		!sourceCitationExplicitlyFutureRecommendation(answer, path) {

		return featurePlannerPlanCitationAuthority{}
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.RuntimeBrief)
	objectiveText := strings.ToLower(strings.Join(strings.Fields(brief.Objective), " "))
	if !objectiveAsksAboutActivePlan(objectiveText) || !objectiveAsksAboutPlanExpansion(objectiveText) {
		return featurePlannerPlanCitationAuthority{}
	}

	if path == "" || !strings.Contains(path, "/") || featurePlannerPlanMetadataPath(path) {
		return featurePlannerPlanCitationAuthority{}
	}

	for _, result := range results {
		if featurePlannerPlanArtifactMatchesObjective(brief.Objective, result) {
			return featurePlannerPlanCitationAuthority{
				Kind:                 featurePlannerPlanCitationFutureRecommendation,
				BackedByPlanArtifact: true,
			}
		}
	}

	return featurePlannerPlanCitationAuthority{}
}

func featurePlannerPlanArtifactPathCitationAuthority(answer string, path string, ctx featurePlannerPlanArtifactCitationContext) featurePlannerPlanCitationAuthority {
	resolved, ok := featurePlannerPlanProposedPathForCitation(path, ctx.ProposedPaths)
	if !ok {
		return featurePlannerPlanCitationAuthority{}
	}

	hasLocalPlanContext := sourceCitationExplicitlyProposedPlanPath(answer, path) ||
		sourceCitationExplicitlyProposedPlanPath(answer, resolved)
	if !hasLocalPlanContext && !ctx.AnswerHasPlanContext {
		return featurePlannerPlanCitationAuthority{}
	}

	if sourceCitationClaimsPlanResult(answer, path) ||
		sourceCitationClaimsPlanResult(answer, resolved) ||
		sourceCitationClaimsCurrentSourcePath(answer, path) ||
		sourceCitationClaimsCurrentSourcePath(answer, resolved) {

		return featurePlannerPlanCitationAuthority{}
	}

	return featurePlannerPlanCitationAuthority{
		Kind:                 featurePlannerPlanCitationArtifactProposed,
		ResolvedPlanPath:     resolved,
		BackedByPlanArtifact: ctx.BackedByPlanArtifact,
	}
}

func featurePlannerPlanProposedPathForCitation(citation string, proposedPaths []string) (string, bool) {
	citation = agentutil.NormalizeRunnerPath(citation)
	if citation == "" || len(proposedPaths) == 0 {
		return "", false
	}

	for _, proposed := range proposedPaths {
		proposed = agentutil.NormalizeRunnerPath(proposed)
		if proposed == "" {
			continue
		}
		if proposed == citation {
			return proposed, true
		}
	}

	if strings.Contains(citation, "/") || sourceCitationLooksLikeRootPath(citation) {
		return "", false
	}

	base := strings.ToLower(filepath.Base(filepath.ToSlash(citation)))
	var matches []string
	for _, proposed := range proposedPaths {
		proposed = agentutil.NormalizeRunnerPath(proposed)
		if proposed == "" {
			continue
		}
		if strings.ToLower(filepath.Base(filepath.ToSlash(proposed))) == base {
			matches = append(matches, proposed)
		}
	}

	matches = uniqueSortedRunnerPaths(matches)
	if len(matches) != 1 {
		return "", false
	}

	return matches[0], true
}

func answerHasFeaturePlannerPlanArtifactContext(answer string, objective string) bool {
	lowerAnswer := " " + strings.ToLower(strings.Join(strings.Fields(answer), " ")) + " "
	if agentutil.ContainsAny(lowerAnswer,
		" feature-planner artifact ", " feature planner artifact ",
		" active plan ", " proposed plan ", " current plan ",
		" planned tests ", " planned test files ", " planned files ",
		" proposed tests ", " proposed test files ", " proposed files ",
		" plan proposes ", " plan defines ", " plan includes ",
		" from the plan ", " in the plan ",
		" what tests are planned ", " what tests are currently planned ",
	) {
		return true
	}

	objectiveText := normalizedPlanObjectiveText(objective)
	return objectiveAsksAboutActivePlan(objectiveText)
}

func proposedPlanPathCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if len(ledger.PlanArtifactPaths) == 0 {
		return false
	}

	ctx := featurePlannerPlanArtifactCitationContextForAnswer(answer, ledger.Objective, ledger.PlanArtifactPaths, true)
	for _, path := range mentioned {
		if featurePlannerPlanArtifactPathCitationAuthority(answer, path, ctx).Kind == featurePlannerPlanCitationArtifactProposed {
			return true
		}
	}

	return false
}

func featurePlannerPlanPathCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if proposedPlanPathCitationsAreRelevant(answer, mentioned, ledger) {
		return true
	}

	return futurePlanRecommendationCitationsAreRelevant(answer, mentioned, ledger)
}

func futurePlanRecommendationCitationsAreRelevant(answer string, mentioned []string, ledger readOnlyAnswerabilityLedger) bool {
	if len(ledger.PlanArtifactPaths) == 0 {
		return false
	}

	objectiveText := strings.ToLower(strings.Join(strings.Fields(ledger.Objective), " "))
	if !objectiveAsksAboutActivePlan(objectiveText) || !objectiveAsksAboutPlanExpansion(objectiveText) {
		return false
	}

	for _, path := range mentioned {
		path = agentutil.NormalizeRunnerPath(path)
		if path == "" || !strings.Contains(path, "/") || featurePlannerPlanMetadataPath(path) {
			continue
		}
		if sourceCitationClaimsPlanResult(answer, path) ||
			sourceCitationClaimsSourceDetail(answer, path) ||
			sourceCitationClaimsCurrentOrPlannedPath(answer, path) ||
			!sourceCitationExplicitlyFutureRecommendation(answer, path) {

			continue
		}

		return true
	}

	return false
}

func sourceCitationExplicitlyFutureRecommendation(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return agentutil.ContainsAny(lower,
		"i would add", "i'd add", "would add", "should add", "also add",
		"recommend adding", "recommend add", "recommended addition", "recommended additions",
		"future test", "future tests", "future file", "future files",
		"new test file", "new test files", "new file to add", "new files to add",
		"additional test", "additional tests", "additional file", "additional files",
		"expand with", "expand the", "expansion test", "expansion tests",
		"files to add", "tests to add",
	)
}

func sourceCitationClaimsCurrentOrPlannedPath(answer string, path string) bool {
	if sourceCitationClaimsCurrentSourcePath(answer, path) {
		return true
	}

	lower := strings.ToLower(sourceCitationLocalWindow(answer, path, 80, 140))
	return agentutil.ContainsAny(lower,
		"planned tests include", "planned test includes", "planned files include", "planned file includes",
		"the planned tests are", "the planned files are", "the plan includes", "plan proposes",
		"from the plan", "in the plan", "already planned",
	)
}

func sourceCitationClaimsPlanResult(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	return agentutil.ContainsAny(lower,
		" passed", " passes", " passing",
		" succeeded", " succeeds", " successful",
		" failed", " failing",
		" proves", " prove ", " proof",
		" verified", " verifies", " confirms", " validates",
	)
}

func sourceCitationExplicitlyProposedPlanPath(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationWindow(answer, path))
	for _, phrase := range []string{
		"proposed path", "proposed paths", "proposed file", "proposed files",
		"planned path", "planned paths", "planned file", "planned files",
		"proposed test", "proposed tests", "planned test", "planned tests", "intended test", "intended tests",
		"planned test plan", "proposed test plan", "intended test plan",
		"planned test suite", "proposed test suite", "intended test suite",
		"planned verification", "proposed verification", "verification command", "verification commands",
		"planned coverage", "proposed coverage", "intended coverage", "planned test coverage",
		"proposed test coverage", "intended test coverage",
		"would create", "will create", "to create", "to be created",
		"files to create", "paths to create",
		"not existing source", "not current source",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func sourceCitationClaimsCurrentSourcePath(answer string, path string) bool {
	lower := strings.ToLower(sourceCitationLocalWindow(answer, path, 80, 140))
	if agentutil.ContainsAny(lower,
		"not existing source", "not existing file",
		"not current source", "not current file",
		"not yet existing source", "not yet existing file",
		"not an existing source", "not an existing file",
		"not a current source", "not a current file",
	) {
		return false
	}

	return agentutil.ContainsAny(lower,
		"already exists", "existing source", "existing file",
		"current source", "current file", "currently exists",
		"exists on disk", "exists in the workspace",
		"defines ", "contains ", "implements ",
	)
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
	return sourceCitationLocalWindow(answer, path, 180, 180)
}

func sourceCitationLocalWindow(answer string, path string, before int, after int) string {
	if strings.TrimSpace(answer) == "" || strings.TrimSpace(path) == "" {
		return ""
	}

	idx := strings.Index(answer, path)
	if idx < 0 {
		return ""
	}

	start := idx - before
	if start < 0 {
		start = 0
	}

	end := idx + len(path) + after
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

func sourceGroundingAnswerCitesReadDiagnosticArtifact(answer string, results []workspace.ToolResult) bool {
	cited := sourceGroundingDiagnosticArtifactPathsFromText(answer)
	if len(cited) == 0 {
		return false
	}

	for _, path := range cited {
		if sourceGroundingDiagnosticArtifactReadForPath(path, results) {
			return true
		}
	}

	return false
}

func sourceGroundingDiagnosticArtifactCitationSupportsObjective(brief workerruntime.WorkerRuntimeBrief, answer string, results []workspace.ToolResult) bool {
	if !sourceGroundingAnswerCitesReadDiagnosticArtifact(answer, results) {
		return false
	}

	objective := strings.TrimSpace(brief.Objective)
	if objective == "" {
		return false
	}

	if objectiveRequestsDiagnosticLogInspection(objective) && !objectiveTreatsDiagnosticLogsAsOptionalEvidence(objective) {
		return true
	}

	words := " " + strings.ToLower(strings.Join(strings.Fields(objective), " ")) + " "
	if !agentutil.ContainsAny(words,
		" harness ", " session ", " turn ", " conversation ", " completion gate ",
		" final answer ", " runner gate ", " gate rejection ", " gate rejected ",
	) {
		return false
	}

	return agentutil.ContainsAny(words,
		" rca ", " failure ", " failed ", " issue ", " regression ", " rejected ",
		" blocker ", " latest ", " recent ", " most recent ", " previous ",
	)
}

func sourceGroundingDiagnosticArtifactPathsFromText(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	paths := pathparse.MentionedWorkspacePaths(text)

	logRE := regexp.MustCompile(`(?i)(?:^|[\s` + "`" + `'"(])((?:/[^` + "`" + `'"()\s]+)?(?:\.idiom/logs|logs)/(?:conversations|telemetry)/[^` + "`" + `'"()\s,;]+\.jsonl)`)
	for _, match := range logRE.FindAllStringSubmatch(filepath.ToSlash(text), -1) {
		if len(match) > 1 {
			paths = append(paths, match[1])
		}
	}

	var out []string
	for _, path := range paths {
		path = sourceGroundingNormalizeDiagnosticArtifactPath(path)
		if path != "" && agentutil.IsDiagnosticLogArtifactPath(path) {
			out = append(out, path)
		}
	}

	return uniqueSortedRunnerPaths(out)
}

func sourceGroundingDiagnosticArtifactReadForPath(path string, results []workspace.ToolResult) bool {
	needle := sourceGroundingNormalizeDiagnosticArtifactPath(path)
	if needle == "" || !agentutil.IsDiagnosticLogArtifactPath(needle) {
		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" ||
			strings.TrimSpace(result.Tool) != workspace.ToolNameReadFile ||
			!readFileResultOnlyMentionsDiagnosticLogArtifacts(result) {

			continue
		}

		for _, readPath := range agentutil.ReadPathsFromToolResult(result) {
			if sourceGroundingNormalizeDiagnosticArtifactPath(readPath) == needle {
				return true
			}
		}
	}

	return false
}

func sourceGroundingNormalizeDiagnosticArtifactPath(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "`'\" "))
	if path == "" {
		return ""
	}

	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")

	return path
}
