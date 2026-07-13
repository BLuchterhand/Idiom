package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/sourcekind"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/gitstate"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const completionGateToolName = "completion_gate"

func requestPathInRequiredEditPaths(path string, required []string) bool {
	cleaned := NormalizeRunnerPath(path)
	if cleaned == "" {
		return false
	}

	for _, p := range required {
		if NormalizeRunnerPath(p) == cleaned {
			return true
		}
	}

	return false
}

func unresolvedRejectedMutationPaths(results []workspace.ToolResult, requiredPaths []string) []string {
	required := make(map[string]struct{}, len(requiredPaths))
	for _, raw := range requiredPaths {
		if path := cleanRelativeRunnerPath(raw); path != "" {
			required[path] = struct{}{}
		}
	}
	requiredScopeActive := len(requiredPaths) > 0
	pending := make(map[string]struct{})

	for _, result := range results {
		if strings.TrimSpace(result.Error) != "" && workspace.IsWorkspaceWriteTool(result.Tool) {
			var paths []string
			if result.Facts != nil && result.Facts.RejectionDisposition.CarriesRejectedWriteIntent() {
				paths = canonicalPersistedRunnerPaths(result.Facts.RejectedWritePaths)
			}

			for _, path := range canonicalPersistedRunnerPaths(paths) {
				if requiredScopeActive {
					if _, ok := required[path]; !ok {
						continue
					}
				}

				pending[path] = struct{}{}
			}
		}

		for _, path := range committedMutationEffectIDsForResult(result) {
			delete(pending, path)
		}
	}

	return uniqueSortedRunnerPaths(mapKeys(pending))
}

func canonicalPersistedRunnerPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := cleanRelativeRunnerPath(raw)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}

	return uniqueSortedRunnerPaths(out)
}

func unsupportedClaimedChangedPaths(reply string, results []workspace.ToolResult) []string {
	claimed := claimedChangedPathsFromAnswer(reply)
	if len(claimed) == 0 {
		return nil
	}

	actual := committedMutationEffectSetForResults(results)
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

func (r *runExecution) unsupportedClaimedChangedPaths(reply string, results []workspace.ToolResult) []string {
	if r == nil {
		return unsupportedClaimedChangedPaths(reply, results)
	}
	claimed := claimedChangedPathsFromAnswer(reply)
	if len(claimed) == 0 {
		return nil
	}
	committed := make(map[string]struct{})
	for _, id := range r.state.committedMutationEffectIDs() {
		committed[id] = struct{}{}
	}
	var unsupported []string
	for _, claim := range claimed {
		if !r.state.mutation.bound {
			if _, ok := committed[claim]; !ok && looksLikeStrictChangedPathClaim(claim) {
				unsupported = append(unsupported, claim)
			}
			continue
		}
		projection := r.state.projectMutationClaims([]string{claim})
		if len(projection.UnsupportedPaths) > 0 || len(projection.CanonicalIDs) != 1 {
			if looksLikeStrictChangedPathClaim(claim) {
				unsupported = append(unsupported, claim)
			}
			continue
		}
		if _, ok := committed[projection.CanonicalIDs[0]]; !ok && looksLikeStrictChangedPathClaim(claim) {
			unsupported = append(unsupported, claim)
		}
	}
	return uniqueSortedRunnerPaths(unsupported)
}

func claimedPathMatchesActualBasename(claimed string, actual map[string]struct{}) bool {
	claimed = NormalizeRunnerPath(claimed)
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
	path = NormalizeRunnerPath(path)
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
		normalized := NormalizeRunnerPath(path)
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

func (r *runExecution) missingRequiredEditSnippets(results []workspace.ToolResult) []string {
	if r == nil {
		return nil
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if len(brief.RequiredEditSnippets) == 0 {
		return nil
	}
	allMissing := make([]string, 0, len(brief.RequiredEditSnippets))
	for i, snippet := range brief.RequiredEditSnippets {
		allMissing = append(allMissing, requiredEditSnippetName(snippet, i))
	}
	if r.runner == nil || r.runner.Workspace == nil {
		return allMissing
	}

	postimages := make(map[string]struct{})
	for _, path := range r.state.committedPostimageFileIDs() {
		postimages[path] = struct{}{}
	}
	if len(postimages) == 0 {
		return allMissing
	}

	var missing []string

	for i, snippet := range brief.RequiredEditSnippets {
		candidates := r.requiredEditSnippetSnapshotCandidates(snippet, brief.RequiredEditPaths, postimages)
		satisfied := false
		for _, candidate := range candidates {
			snapshot, err := r.runner.Workspace.SnapshotWorkspace(r.state.ctx, workspace.WorkspaceSnapshotRequest{
				Purpose: workspace.WorkspaceSnapshotCompletionSnippet,
				Paths:   []string{candidate.PresentedPath},
			})
			if err != nil || len(snapshot.Entries) != 1 {
				continue
			}
			entry := snapshot.Entries[0]
			if entry.Kind != workspace.WorkspaceSnapshotFile || entry.Identity.CanonicalID != candidate.CanonicalID {
				continue
			}
			if editSnippetMatchesContent(string(entry.Body()), snippet.Content) {
				satisfied = true
				break
			}
		}
		if !satisfied {
			missing = append(missing, requiredEditSnippetName(snippet, i))
		}
	}

	return missing
}

type requiredEditSnippetSnapshotCandidate struct {
	PresentedPath string
	CanonicalID   string
}

func (r *runExecution) requiredEditSnippetSnapshotCandidates(
	snippet plancontract.RequiredEditSnippet,
	requiredPaths []string,
	postimages map[string]struct{},
) []requiredEditSnippetSnapshotCandidate {
	if r == nil || r.runner == nil || r.runner.Workspace == nil || len(postimages) == 0 {
		return nil
	}

	presentedPaths := requiredPaths
	pathSpecific := strings.TrimSpace(snippet.Path) != ""
	if pathSpecific {
		presentedPaths = []string{snippet.Path}
	}

	seen := make(map[string]struct{}, len(presentedPaths))
	candidates := make([]requiredEditSnippetSnapshotCandidate, 0, len(presentedPaths))
	for _, rawPath := range presentedPaths {
		presentedPath := NormalizeRunnerPath(rawPath)
		if presentedPath == "" {
			continue
		}
		canonicalID := r.requiredEditSnippetCanonicalID(presentedPath)
		if canonicalID == "" {
			continue
		}
		if _, committed := postimages[canonicalID]; !committed {
			continue
		}
		if _, duplicate := seen[canonicalID]; duplicate {
			continue
		}
		seen[canonicalID] = struct{}{}
		candidates = append(candidates, requiredEditSnippetSnapshotCandidate{
			PresentedPath: presentedPath,
			CanonicalID:   canonicalID,
		})
	}

	if pathSpecific || len(candidates) > 0 {
		return candidates
	}

	for _, canonicalID := range uniqueSortedRunnerPaths(mapKeys(postimages)) {
		if _, duplicate := seen[canonicalID]; duplicate {
			continue
		}
		seen[canonicalID] = struct{}{}
		candidates = append(candidates, requiredEditSnippetSnapshotCandidate{
			PresentedPath: canonicalID,
			CanonicalID:   canonicalID,
		})
	}
	return candidates
}

func (r *runExecution) requiredEditSnippetCanonicalID(path string) string {
	if r == nil || r.runner == nil || r.runner.Workspace == nil {
		return ""
	}
	if r.state.mutation.bound {
		claims := r.state.projectMutationClaims([]string{path})
		if len(claims.UnsupportedPaths) != 0 || len(claims.CanonicalIDs) != 1 {
			return ""
		}
		return claims.CanonicalIDs[0]
	}
	identity, err := r.runner.Workspace.CanonicalizeWorkspacePath(path, "")
	if err != nil {
		return ""
	}
	return identity.CanonicalID
}

func (r *runExecution) unreportedRemovedTestDeclarations(reply string, results []workspace.ToolResult) []string {
	removed := r.removedTestDeclarations(results)
	if len(removed) == 0 || replyReportsRemovedTests(reply, removed) {
		return nil
	}

	return removed
}

func (r *runExecution) removedTestDeclarations(results []workspace.ToolResult) []string {
	if r == nil || r.runner.Workspace == nil {
		return nil
	}

	root := strings.TrimSpace(r.runner.Workspace.Root())
	if root == "" {
		return nil
	}

	var paths []string

	for _, path := range r.state.committedAffectedFileIDs() {
		if testSourcePathCanRemoveDeclarations(path) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		return nil
	}

	diff, err := gitstate.WorktreeUnifiedDiff(context.Background(), root, 0, paths)

	if err != nil || strings.TrimSpace(diff) == "" {
		return nil
	}

	return removedTestDeclarationsFromDiff(diff)
}

func changedTestSourcePaths(results []workspace.ToolResult) []string {
	var paths []string
	for _, result := range results {
		receipt := result.CommitReceipt()
		if receipt == nil {
			continue
		}
		for _, path := range receipt.DurableProjection().AffectedFileIDs {
			if testSourcePathCanRemoveDeclarations(path) {
				paths = append(paths, path)
			}
		}
	}
	return uniqueSortedRunnerPaths(paths)
}

func testSourcePathCanRemoveDeclarations(path string) bool {
	if !sourcekind.CoversSourceSanity(path) {
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
	if !ContainsAny(lower, "removed test", "deleted test", "dropped test", "removed existing test", "test removal", "removed tests", "deleted tests", "dropped tests") {
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

func requiredEditSnippetCandidatePaths(snippet plancontract.RequiredEditSnippet, required []string, changed map[string]struct{}) []string {
	if path := NormalizeRunnerPath(snippet.Path); path != "" {
		return []string{path}
	}

	var candidates []string

	seen := make(map[string]struct{})

	for _, path := range required {
		normalized := NormalizeRunnerPath(path)
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

func requiredEditSnippetName(snippet plancontract.RequiredEditSnippet, index int) string {
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
			path = NormalizeRunnerPath(path)
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

	return ContainsAny(lower, "verification", "test", "planner", "blocker", "validation", "lane sanity")
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

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func hasPendingVerificationAfterWrites(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) bool {
	return postWriteVerificationLedgerForResults(results, brief).Pending()
}

func (r *runExecution) hasPendingVerificationAfterWrites(results []workspace.ToolResult) bool {
	return r.postWriteVerificationLedger(results).Pending()
}

type postWriteVerificationLedger struct {
	PendingPaths          []string
	PendingPostimagePaths []string
	PendingProjectPaths   []string
	LatestFailed          workspace.ToolResult
	HasFailure            bool
}

func (ledger postWriteVerificationLedger) Pending() bool {
	return len(ledger.PendingPaths) > 0
}

func postWriteVerificationLedgerForResults(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) postWriteVerificationLedger {
	return postWriteVerificationLedgerForReceipts(results, brief, nil)
}

func (r *runExecution) postWriteVerificationLedger(results []workspace.ToolResult) postWriteVerificationLedger {
	if r == nil {
		return postWriteVerificationLedger{}
	}
	return postWriteVerificationLedgerForReceipts(results, r.state.runtimeBrief, r.state.mutation.commitReceipts)
}

func postWriteVerificationLedgerForReceipts(
	results []workspace.ToolResult,
	brief workerruntime.WorkerRuntimeBrief,
	receipts []workspace.MutationCommitProjection,
) postWriteVerificationLedger {
	if workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return postWriteVerificationLedger{}
	}

	ledger := postWriteVerificationLedger{}
	liveReceiptCount := 0
	for _, result := range results {
		if result.CommitReceipt() != nil {
			liveReceiptCount++
		}
	}
	restoredReceiptCount := len(receipts) - liveReceiptCount
	if restoredReceiptCount < 0 {
		restoredReceiptCount = 0
	}
	for _, receipt := range receipts[:restoredReceiptCount] {
		ledger.addCommittedEffects(receipt)
	}

	for _, result := range results {
		if receipt := result.CommitReceipt(); receipt != nil {
			projection := receipt.DurableProjection()
			ledger.addCommittedEffects(projection)
			ledger.LatestFailed = workspace.ToolResult{}
			ledger.HasFailure = false

			continue
		}

		if !ledger.Pending() {
			continue
		}

		evidence := completionGateVerificationEvidenceForResult(result, ledger.PendingPaths, brief.VerificationPlan)
		covered := intersectRunnerPaths(evidence.CoveredPaths, ledger.PendingPostimagePaths)
		observation := workspace.AnalyzePostWriteVerificationResult(result, ledger.PendingProjectPaths)
		if evidence.ClearsAll && (len(ledger.PendingProjectPaths) == 0 || observation.BuildOrTest) {
			covered = append([]string(nil), ledger.PendingPaths...)
		} else {
			if observation.Successful && observation.BuildOrTest {
				covered = textutil.Merge(covered, observation.CoveredPendingPaths)
			}
		}
		covered = uniqueSortedRunnerPaths(covered)
		if evidence.Successful && len(covered) > 0 {
			ledger.clearVerifiedPaths(covered)
			ledger.LatestFailed = workspace.ToolResult{}
			ledger.HasFailure = false
			continue
		}
		if evidence.Attempted && evidence.Relevant && strings.TrimSpace(result.Error) != "" {
			ledger.LatestFailed = result
			ledger.HasFailure = true
		}
	}

	return ledger
}

func (ledger *postWriteVerificationLedger) addCommittedEffects(receipt workspace.MutationCommitProjection) {
	if ledger == nil {
		return
	}
	ledger.PendingPostimagePaths = textutil.AppendUniqueMulti(ledger.PendingPostimagePaths, receipt.PostimageFileIDs...)
	postimages := make(map[string]struct{}, len(receipt.PostimageFileIDs))
	for _, id := range receipt.PostimageFileIDs {
		postimages[id] = struct{}{}
	}
	for _, id := range receipt.AffectedFileIDs {
		if _, hasPostimage := postimages[id]; hasPostimage {
			continue
		}
		ledger.PendingProjectPaths = textutil.AppendUnique(ledger.PendingProjectPaths, id)
	}
	ledger.syncPendingPaths()
}

func (ledger *postWriteVerificationLedger) clearVerifiedPaths(paths []string) {
	if ledger == nil {
		return
	}
	ledger.PendingPostimagePaths = completionGateRemainingPaths(ledger.PendingPostimagePaths, paths)
	ledger.PendingProjectPaths = completionGateRemainingPaths(ledger.PendingProjectPaths, paths)
	ledger.syncPendingPaths()
}

func (ledger *postWriteVerificationLedger) syncPendingPaths() {
	if ledger == nil {
		return
	}
	ledger.PendingPaths = uniqueSortedRunnerPaths(textutil.Merge(ledger.PendingPostimagePaths, ledger.PendingProjectPaths))
}

// briefIsDocDeliverableLane reports whether the brief specs a doc-only
// deliverable (refine draft/review/patch, design note write-up, etc.).
// The signal is "every RequiredEditPath is a non-source file". Empty
// RequiredEditPaths is treated as not-doc-only so implementation lanes
// with no specced allowlist still trip the verification gate when they
// happen to touch a markdown file.

func runnerGuidanceConstraints(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, authority planThenExecutePathAuthority) []string {
	constraints := append([]string(nil), brief.Constraints...)
	if planThenExecuteSubmitPlanOnlyReady(brief, results, authority) {
		constraints = textutil.Merge(constraints, []string{"plan_then_execute_submit_plan_ready"})
	}

	if strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		strings.TrimSpace(brief.TaskFamily) == "delegated_read_only" &&
		delegatedReadOnlyClosureReady(agent, brief, results) {

		constraints = textutil.Merge(constraints, []string{"answer_readiness_reached"})
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") &&
		newToolResultEvidenceProjection(results).postChildPacketClosureReady(brief) {

		constraints = textutil.Merge(constraints, []string{"child_packet_available"})
	}

	return constraints
}

func applyDelegatedReadOnlyClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") ||
		strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" ||
		!delegatedReadOnlyClosureReady(agent, brief, results) {

		return state
	}

	state.Constraints = textutil.Merge(state.Constraints, []string{"answer_readiness_reached"})
	state.InvalidNextActions = filterRunnerStateStrings(state.InvalidNextActions, func(value string) bool {
		return value == "final_answer_without_satisfying_completion_gate"
	})
	state.NextValidActions = filterRunnerStateStrings(state.NextValidActions, func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "gate") || strings.Contains(lower, "gather or edit the missing evidence")
	})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the evidence already gathered is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	return state
}

func delegatedReadOnlyClosureReady(agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) bool {
	if slices.Contains(brief.Constraints, "answer_readiness_reached") {
		return true
	}

	if !strings.HasPrefix(strings.TrimSpace(agent), "child:") || strings.TrimSpace(brief.TaskFamily) != "delegated_read_only" {
		return false
	}

	if runnerBriefRequiresMutation(brief) || runnerVerificationRequired(brief) {
		return false
	}

	return readOnlyClosureDecisionForResults(brief, results).Answerable()
}

func applyPostChildClosureBias(state runnerNextActionState, agent string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) runnerNextActionState {
	if strings.HasPrefix(strings.TrimSpace(agent), "child:") ||
		!newToolResultEvidenceProjection(results).postChildPacketClosureReady(brief) {

		return state
	}

	state.Constraints = textutil.Merge(state.Constraints, []string{"child_packet_available"})
	state.NextValidActions = prependRunnerStateStrings([]string{
		"final answer from the child packet evidence is available",
		"if one exact missing claim remains, use one bounded probe against a named path, symbol, discovered handle, or narrow range",
	}, state.NextValidActions)
	return state
}

func (r *runExecution) completionGateRejection(reply string, results []workspace.ToolResult, step int, taskLedger *taskLedgerState, declarationMovesForbidden bool) (workspace.ToolResult, bool) {
	if result, rejected := r.completionGatePipeline(step, taskLedger, declarationMovesForbidden).Evaluate(reply, results); rejected {
		return result, true
	}

	if r.runner.CompletionGate == nil {
		return workspace.ToolResult{}, false
	}

	gate := r.runner.CompletionGate(CompletionGateInput{
		AgentName: r.agentName(),
		Reply:     reply,
		Results:   append([]workspace.ToolResult(nil), results...),
		Steps:     step,
	})
	if gate.Accept {
		return workspace.ToolResult{}, false
	}

	summary := strings.TrimSpace(gate.Summary)
	if summary == "" {
		summary = "Final answer rejected"
	}

	errorText := strings.TrimSpace(gate.Error)
	if errorText == "" {
		errorText = "the answer does not satisfy the task completion gate; continue with the next tool call or state a concrete blocker"
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: summary,
		Error:   errorText,
	}, true
}

func (r *runExecution) finalAnswerAcceptanceCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}
	if !r.replyMayProjectFinalAnswerAcceptanceThroughCompletionGate(reply) {
		return workspace.ToolResult{}, false
	}

	acceptance := r.evaluateFinalAnswerAcceptance(reply, results)
	if acceptance.BlockerKind == "" {
		return workspace.ToolResult{}, false
	}
	if !r.finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance) {
		return workspace.ToolResult{}, false
	}

	summary := "Final answer rejected by blocker check"
	if violation, ok := turnContractAssessmentPrimaryViolation(acceptance.ContractAssessment); ok &&
		violation.Kind == turnViolationSourceGroundingUnmet {

		summary = "Read-only completion gate rejected final answer"
	}

	return finalAnswerCompletionGateProjection(
		acceptance,
		summary,
		r.finalAnswerBlockerErrorText(acceptance.BlockerKind, acceptance.Reason, reply, results),
	)
}

func (r *runExecution) replyMayProjectFinalAnswerAcceptanceThroughCompletionGate(_ string) bool {
	return r != nil &&
		!runnerBriefRequiresMutation(r.state.runtimeBrief) &&
		workerruntime.PermissionProfileIsReadOnlyWorkspace(r.state.runtimeBrief.PermissionProfile)
}

func (r *runExecution) finalAnswerAcceptanceProjectsThroughCompletionGate(acceptance finalAnswerAcceptance) bool {
	if r == nil ||
		runnerBriefRequiresMutation(r.state.runtimeBrief) ||
		!workerruntime.PermissionProfileIsReadOnlyWorkspace(r.state.runtimeBrief.PermissionProfile) {

		return false
	}

	switch acceptance.BlockerKind {
	case string(turnViolationProtocolMetaFinalAnswer):
		return true
	case string(turnViolationSourceGroundingUnmet):
		return r != nil &&
			workerruntime.PermissionProfileIsReadOnlyWorkspace(r.state.runtimeBrief.PermissionProfile) &&
			!runnerBriefRequiresMutation(r.state.runtimeBrief)
	default:
		return false
	}
}

func rawToolResultTranscriptCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeRawToolResultTranscript(reply) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Raw tool result transcript rejected as final answer",
		Error:   "the reply is an internal tool-result transcript, not a user-facing answer. Synthesize the answer from the gathered evidence, or state the concrete blocker/no-op reason; do not copy tool/status/output fields.",
	}, true
}

func (r *runExecution) readOnlyNonSubstantiveCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}

	if !genericReadOnlyAnswerEvidenceReady(brief, results) {
		return workspace.ToolResult{}, false
	}

	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	if len(strings.Fields(trimmed)) > 2 {
		return workspace.ToolResult{}, false
	}

	if len(pathparse.MentionedWorkspacePaths(trimmed)) > 0 {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Read-only completion gate rejected non-substantive final answer",
		Error:   "read-only workspace evidence has been gathered, but the reply is not a substantive user-facing answer. A substantive final answer should carry forward the inspected path and the concrete value or fact needed by the question.",
	}, true
}

func unresolvedProcessIntentCompletionRejection(reply string) (workspace.ToolResult, bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return workspace.ToolResult{}, false
	}

	inspect := stripQuotedEvidenceForProcessIntent(trimmed)
	if strings.TrimSpace(inspect) == "" {
		return workspace.ToolResult{}, false
	}

	lower := " " + strings.ToLower(strings.Join(strings.Fields(inspect), " ")) + " "
	if ContainsAny(lower,
		" blocked ",
		" blocker ",
		" cannot ",
		" can't ",
		" could not ",
		" unable ",
		" no access ",
		" permission ",
		" not available ",
	) {

		return workspace.ToolResult{}, false
	}

	if !ContainsAny(lower,
		" let me search ", " let me inspect ", " let me read ", " let me refresh ", " let me run ", " let me check ", " let me audit ", " let me investigate ", " let me explore ", " let me look ",
		" let me call ", " let me use ",
		" i need to search ", " i need to inspect ", " i need to find ", " i need to read ", " i need to refresh ", " i need to run ", " i need to check ", " i need to audit ", " i need to investigate ", " i need to explore ", " i need to look ",
		" i need to call ", " i need to use ",
		" i should search ", " i should inspect ", " i should find ", " i should read ", " i should refresh ", " i should run ", " i should check ", " i should audit ", " i should investigate ", " i should explore ", " i should look ",
		" i should call ", " i should use ",
		" i'll search ", " i'll inspect ", " i'll read ", " i'll refresh ", " i'll run ", " i'll check ", " i'll audit ", " i'll investigate ", " i'll explore ", " i'll look ",
		" i'll call ", " i'll use ",
		" i will search ", " i will inspect ", " i will read ", " i will refresh ", " i will run ", " i will check ", " i will audit ", " i will investigate ", " i will explore ", " i will look ",
		" i will call ", " i will use ",
		" i'm going to search ", " i'm going to inspect ", " i'm going to read ", " i'm going to run ", " i'm going to check ", " i'm going to audit ", " i'm going to investigate ", " i'm going to explore ", " i'm going to look ",
		" i'm going to call ", " i'm going to use ",
		" i'll start by ", " i will start by ",
		" i'll ",
		" i will ",
	) {

		return workspace.ToolResult{}, false
	}

	if ContainsAny(lower, " i'll ", " i will ") && !ContainsAny(lower,
		" search ", " inspect ", " read ", " refresh ", " run ", " check ", " verify ",
	) {

		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Completion gate rejected unresolved process narration",
		Error:   "the reply describes an intended future search/read/inspection instead of completing the answer. Issue the needed tool call now, answer from gathered evidence, or state a concrete blocker.",
	}, true
}

func stripQuotedEvidenceForProcessIntent(text string) string {
	var out []string

	inFence := false

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}

		if inFence {
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

// looksLikeToolResultTranscript returns true when the reply looks like
// a raw tool result transcript (e.g. line-numbered source fragments or
// "contains `func Foo`"). Broader low-value source inventories are
// handled by final-answer acceptance; this completion gate is only for
// raw transcript/search scaffolding that should never be user-visible.
func looksLikeToolResultTranscript(lower string) bool {
	scanText := strings.ToLower(stripQuotedAndFencedSourceExamples(lower))
	hasLead := replyHasDirectSynthesisLead(scanText)

	if looksLikeLowValueSourceInventoryNonAnswer(scanText) && !hasLead {
		return true
	}

	if looksLikeLineOrientedSourceSnippetAnswer(scanText) && !hasLead {
		return true
	}

	// " contains `f" where f is any function/symbol name
	if !hasLead && (strings.Contains(scanText, " contains `func ") ||
		strings.Contains(scanText, " contains `type ") ||
		strings.Contains(scanText, " contains `var ") ||
		strings.Contains(scanText, " contains `const ")) {

		return true
	}

	return false
}

func replyHasDirectSynthesisLead(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	trimmed = strings.TrimPrefix(trimmed, "**")
	lower := strings.ToLower(strings.TrimSpace(trimmed))
	for _, prefix := range []string{
		"i have sufficient evidence to answer.",
		"i now have sufficient evidence to answer.",
		"i now have enough evidence to answer.",
		"i now have enough evidence to answer both questions.",
		"i now have all the evidence needed for a grounded answer.",
		"now i have sufficient evidence to answer.",
		"now i have enough evidence to answer.",
		"now i have enough evidence to answer both questions.",
		"now i have all the evidence needed for a grounded answer.",
		"now i have the full picture.",
		"let me synthesize.",
	} {
		lower = strings.TrimSpace(strings.TrimPrefix(lower, prefix))
	}

	first := lower
	if idx := strings.Index(first, "\n\n"); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(strings.Trim(first, "* "))
	if first == "" {
		return false
	}

	if strings.HasPrefix(first, "for `") ||
		strings.HasPrefix(first, "`") ||
		strings.HasPrefix(first, "the inspected source") ||
		strings.Contains(first, "clearest inspected implementation anchor") ||
		strings.Contains(first, " inspected body shows ") {

		return false
	}

	return strings.Contains(first, "confirmed:") ||
		strings.HasPrefix(first, "confirmed.") ||
		strings.Contains(first, "the answer is:") ||
		strings.HasPrefix(first, "answer:") ||
		strings.HasPrefix(first, "## answer:") ||
		strings.HasPrefix(first, "yes,") ||
		strings.HasPrefix(first, "no,") ||
		strings.HasPrefix(first, "no —") ||
		strings.HasPrefix(first, "yes —") ||
		(strings.Contains(first, " is ") && len(strings.Fields(first)) >= 8)
}

func (r *runExecution) readOnlySearchHitInventoryCompletionRejection(reply string, results []workspace.ToolResult) (workspace.ToolResult, bool) {
	if r == nil {
		return workspace.ToolResult{}, false
	}

	brief := workerruntime.NormalizeRuntimeBrief(r.state.runtimeBrief)
	if !workerruntime.PermissionProfileIsReadOnlyWorkspace(brief.PermissionProfile) {
		return workspace.ToolResult{}, false
	}

	// A tool-result transcript answer is garbage regardless of
	// whether the evidence gate declares evidence ready. Check format
	// first, evidence status second.
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" || strings.HasPrefix(trimmed, "<<QUESTION>>") {
		return workspace.ToolResult{}, false
	}

	lower := strings.ToLower(trimmed)
	unquotedReply := stripQuotedAndFencedSourceExamples(trimmed)
	hasTranscript := looksLikeToolResultTranscript(lower)
	hasSearchHandle := toolLocalSearchHandlePattern.MatchString(unquotedReply)
	if !hasTranscript && !(hasSearchHandle && !replyHasDirectSynthesisLead(trimmed)) {
		return workspace.ToolResult{}, false
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Read-only completion gate rejected search-hit inventory",
		Error:   "the reply inventories search handles or raw hit lines instead of answering the user's question. Synthesize the answer from the inspected evidence; do not expose tool-local handles or search-result scaffolding.",
	}, true
}

func (r *runExecution) unexecutedToolRequestCompletionRejection(reply string) (workspace.ToolResult, bool) {
	if !looksLikeUnexecutedToolRequestAnswer(reply) {
		return workspace.ToolResult{}, false
	}
	transport := PlanExecutePlanningTransport{}
	if r != nil {
		transport = r.state.planningTransport()
	}

	return workspace.ToolResult{
		Tool:    completionGateToolName,
		Summary: "Unexecuted tool request rejected as final answer",
		Error: strings.Join([]string{
			"the reply is a workspace-tool request payload, not a final answer.",
			workspaceLookupTransportInstruction(recoveryToolTransportMode(transport)),
			"If no lookup is needed, answer the user in normal prose.",
			"Do not stop with raw JSON.",
		}, " "),
	}, true
}

func looksLikeUnexecutedToolRequestAnswer(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	start, end, ok := firstToolRequestShapedJSONObjectSpan(trimmed)
	if !ok {
		return false
	}

	if markdownFenceOpenBefore(trimmed, start) {
		return false
	}

	outside := strings.TrimSpace(trimmed[:start] + "\n" + trimmed[end:])

	outside = strings.Trim(outside, "` \t\r\n:")

	if outside == "" {
		return true
	}

	if toolRequestJSONObjectAppearsAsStandaloneBlock(trimmed, start, end) {
		return true
	}

	fields := strings.Fields(outside)
	if len(fields) > 24 {
		return false
	}

	lower := " " + strings.ToLower(outside) + " "

	return textutil.ContainsAny(lower,
		" let me ",
		" i need ",
		" i will ",
		" i'll ",
		" start by ",
		" first ",
		" understanding ",
		" locating ",
		" inspect ",
		" checking ",
		" call ",
		" calling ",
		" use ",
	)
}

func toolRequestJSONObjectAppearsAsStandaloneBlock(text string, start int, end int) bool {
	if markdownFenceOpenBefore(text, start) {
		return false
	}

	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	if strings.TrimSpace(text[lineStart:start]) != "" {
		return false
	}

	lineEnd := len(text)
	if nextNewline := strings.IndexByte(text[end:], '\n'); nextNewline >= 0 {
		lineEnd = end + nextNewline
	}

	return strings.TrimSpace(text[end:lineEnd]) == ""
}

func markdownFenceOpenBefore(text string, pos int) bool {
	open := false

	for _, line := range strings.Split(text[:pos], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			open = !open
		}
	}

	return open
}

func firstToolRequestShapedJSONObjectSpan(text string) (int, int, bool) {
	for searchFrom := 0; searchFrom < len(text); {
		relativeStart := strings.IndexByte(text[searchFrom:], '{')
		if relativeStart < 0 {
			return 0, 0, false
		}

		start := searchFrom + relativeStart
		object, end, ok := extractJSONObjectAt(text, start)

		if !ok {
			return 0, 0, false
		}

		if markdownFenceOpenBefore(text, start) {
			searchFrom = end
			continue
		}

		var fields map[string]json.RawMessage

		if err := json.Unmarshal([]byte(object), &fields); err == nil && toolRequestShapedJSONFields(fields) {
			return start, end, true
		}

		searchFrom = start + 1
	}

	return 0, 0, false
}

func extractJSONObjectAt(text string, start int) (string, int, bool) {
	if start < 0 || start >= len(text) || text[start] != '{' {
		return "", 0, false
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '{':
			depth++
		case !inString && c == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(text[start : i+1]), i + 1, true
			}
		}
	}

	return "", 0, false
}

func toolRequestShapedJSONFields(fields map[string]json.RawMessage) bool {
	has := func(keys ...string) bool {
		for _, key := range keys {
			if _, ok := fields[key]; ok {
				return true
			}
		}

		return false
	}
	if has("tool") {
		return true
	}

	if has("edits") && has("rationale") {
		return true
	}

	if has("plan_artifact") {
		return true
	}

	if has("task") && has("mode", "limit", "budget_tokens", "required_tests", "required_edit_paths", "forbidden_paths", "scope", "path") {
		return true
	}

	if has("query") && has("resolution", "limit", "mode", "path", "language") {
		return true
	}

	if has("command") && has("workdir", "timeout_ms", "yield_time_ms", "sandbox_permissions", "justification") {
		return true
	}

	if has("agent") && has("message", "task", "items") {
		return true
	}

	if has("todos") && has("task_id", "items") {
		return true
	}

	if has("path") && has("old_text", "new_text", "replace_all") {
		return true
	}

	if has("capability") && has("path", "pattern", "query", "language") {
		return true
	}

	if has("path") && has("start_line", "end_line", "pattern", "glob", "language", "content", "recursive", "include_source", "include_files", "refresh", "freshness_policy", "handle", "symbol") {
		return true
	}

	if has("pattern") && has("path", "glob") {
		return true
	}

	return false
}

func syntheticLegacyToolCall(step int, req workspace.ToolRequest, raw string) llama.ToolCall {
	name := strings.TrimSpace(req.Tool)
	if name == "" {
		name = "workspace_tool"
	}

	args := strings.TrimSpace(raw)
	if args == "" {
		args = marshalToolRequestRaw(req)
	}

	return llama.ToolCall{
		ID:   fmt.Sprintf("idiom_legacy_tool_%d", step),
		Type: "function",
		Function: llama.ToolCallFunc{
			Name:      name,
			Arguments: args,
		},
	}
}

func legacyToolCallHistoryMessage(reply llama.ChatMessage, call llama.ToolCall) llama.ChatMessage {
	return llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		ReasoningContent: reply.ReasoningContent,
		ToolCalls:        []llama.ToolCall{call},
	}
}

func textualToolRequestHistoryMessage(reply llama.ChatMessage, req workspace.ToolRequest, raw string) llama.ChatMessage {
	content := strings.TrimSpace(raw)
	if content == "" || !strings.Contains(content, `"tool"`) {
		content = marshalToolRequestRaw(req)
	}

	content = strings.TrimSpace(content)

	return llama.ChatMessage{
		Role:             textutil.FirstNonEmpty(strings.TrimSpace(reply.Role), "assistant"),
		Content:          content,
		ReasoningContent: reply.ReasoningContent,
	}
}

type runnerControlRails struct {
	Lane                 string   `json:"lane,omitempty"`
	FollowupLane         string   `json:"followup_lane,omitempty"`
	Contract             string   `json:"contract,omitempty"`
	AgentMode            string   `json:"agent_mode,omitempty"`
	WorkflowKind         string   `json:"workflow_kind,omitempty"`
	WorkflowPhase        string   `json:"workflow_phase,omitempty"`
	TaskFamily           string   `json:"task_family,omitempty"`
	PermissionProfile    string   `json:"permission_profile,omitempty"`
	TargetRefs           []string `json:"target_refs,omitempty"`
	RequiredEditPaths    []string `json:"required_edit_paths,omitempty"`
	AllowedEditPaths     []string `json:"allowed_edit_paths,omitempty"`
	RequiredEvidence     []string `json:"required_evidence,omitempty"`
	RequiredTools        []string `json:"required_tools,omitempty"`
	AllowedTools         []string `json:"allowed_tools,omitempty"`
	ForbiddenTools       []string `json:"forbidden_tools,omitempty"`
	ReadinessGate        []string `json:"readiness_gate,omitempty"`
	SearchPolicy         []string `json:"search_policy,omitempty"`
	VerificationContract string   `json:"verification_contract,omitempty"`
	StopConditions       []string `json:"stop_conditions,omitempty"`
	FinalCritic          []string `json:"final_critic,omitempty"`
	DynamicInstructions  bool     `json:"dynamic_instruction_attachment,omitempty"`
	FailureToEval        bool     `json:"failure_to_eval"`
}

func (r *runExecution) emitControlRailsForBrief(messages []llama.ChatMessage, brief workerruntime.WorkerRuntimeBrief) {
	if r == nil || r.runner.OnEvent == nil {
		return
	}

	rails := r.controlRailsForBrief(brief)

	data, err := json.Marshal(rails)
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "control_rails", Content: string(data)})
}

func (r *runExecution) controlRailsForBrief(brief workerruntime.WorkerRuntimeBrief) runnerControlRails {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	completion := r.implementationCompletionState(nil)

	allowedTools := []string(nil)

	if workerruntime.PermissionProfileIsConversationOnly(brief.PermissionProfile) {
		allowedTools = nil
	} else if len(brief.AllowedTools) > 0 {
		allowedTools = append([]string(nil), brief.AllowedTools...)
	} else {
		allowedTools = r.workspaceAllowedTools()
	}

	if exactAnchorPreWriteSurfaceActive(brief, nil, completion.HasChanges()) {
		allowedTools = workspace.ToolSurfacePhaseAllowedTools(allowedTools, workspace.ToolSurfacePhaseImplementationPreWrite)
	} else if implementationScopedEditSurfaceActive(brief, nil, completion.MissingRequiredPaths, completion.HasChanges()) {
		allowedTools = workspace.ImplementationScopedEditAllowedToolsWithExternalReadOnlyDiscovery(allowedTools, r.externalReadOnlyDiscoveryTool)
	}
	allowedTools = textutil.Merge(
		allowedTools,
		r.permissionAuthorizedInspection(r.workspaceAllowedTools()).ToolNames(),
	)

	profile := strings.TrimSpace(brief.PermissionProfile)
	if profile == "" {
		if len(allowedTools) == 0 {
			profile = string(workerruntime.PermissionConversationOnly)
		} else {
			profile = string(workerruntime.PermissionReadOnlyWorkspace)
		}
	}

	rails := runnerControlRails{
		Lane:                 textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), r.agentName()),
		Contract:             textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractDirectAnswer)),
		TaskFamily:           textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyDirectAnswer)),
		PermissionProfile:    profile,
		TargetRefs:           append([]string(nil), brief.TargetRefs...),
		RequiredEditPaths:    append([]string(nil), brief.RequiredEditPaths...),
		AllowedEditPaths:     append([]string(nil), brief.AllowedEditPaths...),
		RequiredEvidence:     append([]string(nil), brief.RequiredEvidence...),
		RequiredTools:        append([]string(nil), brief.RequiredTools...),
		AllowedTools:         allowedTools,
		ForbiddenTools:       append([]string(nil), brief.ForbiddenTools...),
		ReadinessGate:        append([]string(nil), brief.ReadinessGate...),
		SearchPolicy:         append([]string(nil), brief.SearchPolicy...),
		VerificationContract: textutil.FirstNonEmpty(strings.TrimSpace(brief.VerificationContract), string(workerruntime.VerificationNotRequiredConversation)),
		StopConditions:       append([]string(nil), brief.StopConditions...),
		FinalCritic:          append([]string(nil), brief.FinalCritic...),
		DynamicInstructions:  r.runner.InstructionAttacher != nil,
	}
	if workerruntime.PermissionProfileIsReadOnlyWorkspace(rails.PermissionProfile) {
		tools := workerruntime.NormalizeReadOnlyWorkspaceToolSet(workerruntime.RuntimeToolSet{
			AllowedTools:   rails.AllowedTools,
			RequiredTools:  rails.RequiredTools,
			ForbiddenTools: rails.ForbiddenTools,
		})
		rails.AllowedTools = tools.AllowedTools
		rails.RequiredTools = tools.RequiredTools
		rails.ForbiddenTools = tools.ForbiddenTools
	}
	if len(rails.RequiredEvidence) == 0 {
		rails.RequiredEvidence = []string{string(workerruntime.RequiredEvidenceAnswerAddressesUserRequest)}
	}

	if len(rails.StopConditions) == 0 {
		rails.StopConditions = []string{string(workerruntime.StopAnsweredUserRequest), string(workerruntime.StopBlockedByMissingInformation)}
	}

	if runnerBriefRequiresMutation(brief) {
		rails.Lane = textutil.FirstNonEmpty(strings.TrimSpace(brief.Lane), "main")
		rails.Contract = textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractImplementationResult))
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyImplementation))
		rails.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionGuardedWorkspaceWrite))

		if planThenExecutePlannerBrief(brief) {
			rails.RequiredEvidence = textutil.Merge([]string{string(workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied)}, rails.RequiredEvidence)
			rails.RequiredTools = textutil.Merge(workspace.ToolSurfacePhaseToolNames(workspace.ToolSurfacePhasePlanExecuteSubmitPlanOnly), rails.RequiredTools)
			rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, "unbounded_write")
			rails.ReadinessGate = textutil.Merge([]string{string(workerruntime.ReadinessSubmitPlanOrSourceSatisfiedOrBlocker)}, rails.ReadinessGate)
			rails.SearchPolicy = textutil.Merge([]string{"consume_target_packet_first", "trust_preloaded_current_state_anchors_for_scope_not_source_body", "required_existing_paths_need_source_body_before_submit_plan", "submit_plan_when_edit_set_is_supported", "source_satisfied_when_current_state_already_matches", "do_not_call_per_edit_writes"}, rails.SearchPolicy)
			rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredTextMutation))
			rails.StopConditions = textutil.Merge([]string{string(workerruntime.StopSubmitPlanReturnedOrSourceSatisfiedOrBlocker), string(workerruntime.StopBlockedByMissingPermissionOrInformation)}, rails.StopConditions)
			rails.FinalCritic = []string{"answers_actual_user_request", "submit_plan_called", "source_satisfied_or_blocker_only_with_per_path_evidence"}

			return rails
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceChangedPathsOrNoopReason), string(workerruntime.RequiredEvidenceVerificationStatus)})
		if exactAnchorPreWriteSurfaceActive(brief, nil, completion.HasChanges()) {
			rails.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch}
		} else if implementationScopedEditSurfaceActive(brief, nil, completion.MissingRequiredPaths, completion.HasChanges()) {
			rails.RequiredTools = []string{workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSemanticDiscoveryOrNarrowReadForNamedGap}
		} else {
			rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{workspace.RequiredToolEditFileOrApplyPatch, workspace.RequiredToolSourceSanityOrVerificationShell})
		}

		rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, "unbounded_write")
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessChangedPathsReported), string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"consume_target_packet_first", "prefer_small_bounded_edits", "verify_changed_surface"})
		rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationTestsPassedFailedSkipped))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopChangeVerified), string(workerruntime.StopBlockedWithFailedVerificationOrScope)})
		rails.FinalCritic = []string{"answers_actual_user_request", "changed_paths_reported", "verification_status_reported"}

		return rails
	}

	if slices.Contains(rails.AllowedTools, workspace.ToolNameShell) && !runnerHasWorkspaceWriteCapability(brief, rails.AllowedTools) && runnerVerificationRequired(brief) {
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), "verification")
		rails.Contract = textutil.FirstNonEmpty(strings.TrimSpace(brief.Contract), string(workerruntime.ContractGroundedExplanation))
		rails.PermissionProfile = textutil.FirstNonEmpty(strings.TrimSpace(brief.PermissionProfile), string(workerruntime.PermissionReadOnlyVerificationShell))
		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceVerificationCommand), string(workerruntime.RequiredEvidencePassFailStatus)})
		rails.RequiredTools = defaultRunnerStateSlice(rails.RequiredTools, []string{workspace.ToolNameShell})
		rails.ForbiddenTools = appendRunnerTools(rails.ForbiddenTools, append(workspace.WorkspaceWriteToolNames(), "mutating_shell")...)
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{string(workerruntime.ReadinessVerificationStatusReported)})
		rails.SearchPolicy = defaultRunnerStateSlice(rails.SearchPolicy, []string{"run_requested_check", "capture_failure_output", "do_not_edit"})
		rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}

		return rails
	}

	if rails.Contract == string(workerruntime.ContractArchitecture) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "architecture_boundary_evidence_supported_or_uncertainty_declared"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}

	if rails.Contract == string(workerruntime.ContractReviewFindings) {
		rails.ReadinessGate = defaultRunnerStateSlice(rails.ReadinessGate, []string{"inspected_paths_reported", "bounded_findings_or_no_finding_supported"})
		if len(rails.FinalCritic) == 0 {
			rails.FinalCritic = []string{"answers_actual_user_request", "required_evidence_cited"}
		}
	}

	if rails.Contract == string(workerruntime.ContractDirectAnswer) {
		rails.TaskFamily = textutil.FirstNonEmpty(strings.TrimSpace(brief.TaskFamily), string(autonomy.TaskFamilyDirectAnswer))
		if len(rails.AllowedTools) == 0 {
			rails.PermissionProfile = string(workerruntime.PermissionConversationOnly)
			rails.ForbiddenTools = textutil.AppendUnique(rails.ForbiddenTools, workspace.ToolNameShell)
		}

		rails.RequiredEvidence = defaultRunnerStateSlice(rails.RequiredEvidence, []string{string(workerruntime.RequiredEvidenceAnswerAddressesPrompt)})
		rails.VerificationContract = textutil.FirstNonEmpty(strings.TrimSpace(rails.VerificationContract), string(workerruntime.VerificationNotRequiredReadOnlyLane))
		rails.StopConditions = defaultRunnerStateSlice(rails.StopConditions, []string{string(workerruntime.StopRequiredEvidenceSatisfied)})

		if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
			rails.ReadinessGate = nil
			rails.FinalCritic = nil
		}
	}

	if runnerUsesSoftReadOnlyShapingOnly(brief, rails) {
		rails.ReadinessGate = nil
		rails.FinalCritic = nil
	}

	return rails
}

func runnerUsesSoftReadOnlyShapingOnly(brief workerruntime.WorkerRuntimeBrief, rails runnerControlRails) bool {
	if runnerBriefRequiresMutation(brief) {
		return false
	}

	switch strings.TrimSpace(rails.Contract) {
	case string(workerruntime.ContractArchitecture), string(workerruntime.ContractReviewFindings):
		return false
	}

	if runnerVerificationRequired(brief) {
		return false
	}

	switch strings.TrimSpace(rails.PermissionProfile) {
	case string(workerruntime.PermissionReadOnlyWorkspace):
		return true
	default:
		return false
	}
}

func successfulToolSeen(results []workspace.ToolResult, tool string) bool {
	tool = strings.TrimSpace(tool)
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == tool && strings.TrimSpace(result.Error) == "" {
			return true
		}
	}

	return false
}

func completionGateResultSeen(results []workspace.ToolResult) bool {
	return completionGateResultCount(results) > 0
}

func completionGateResultCount(results []workspace.ToolResult) int {
	count := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) == completionGateToolName {
			count++
		}
	}

	return count
}

func nonEmptyRunnerPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

func lastNonGateTool(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == completionGateToolName {
			continue
		}

		if strings.TrimSpace(results[i].Error) != "" {
			continue
		}

		return tool
	}

	return ""
}

func lastNonGateAttemptWasUnavailableContextPack(results []workspace.ToolResult) bool {
	for i := len(results) - 1; i >= 0; i-- {
		tool := strings.TrimSpace(results[i].Tool)
		if tool == "" || tool == completionGateToolName {
			continue
		}

		if tool != workspace.ToolNameContextPack {
			return false
		}

		return strings.TrimSpace(results[i].Error) != "" ||
			results[i].Facts == nil ||
			!results[i].Facts.HasDiscoveryTargets
	}

	return false
}

func alreadyReadPath(results []workspace.ToolResult, path string) bool {
	path = NormalizeRunnerPath(path)
	if path == "" {
		return false
	}

	_, ok := newToolResultEvidenceProjection(results).ReadPaths()[path]
	return ok
}

type runnerExecutionState struct {
	verificationAttempted         bool
	needsNarrowingAfterBroad      bool
	broadInspectionCount          int
	consecutiveListFiles          int
	pendingDiscoveryConsumption   bool
	pendingDiscoveryTool          string
	pendingDiscoverySummary       string
	pendingDiscoveryOutput        string
	pendingDiscoveryCandidates    []string
	pendingDiscoveryStrength      toolfacts.DiscoveryStrength
	pendingDiscoveryFailedAttempt bool
	checkpointCreated             bool
	writesSinceVerification       int
	consecutiveToolFailures       int
	lastFailedPathStem            string
	softFailureNudgeUsed          bool
}

type runnerExecutionUpdate struct {
	ResetVerificationGuards bool
	VerificationState       string
	VerificationDetail      string
}

func newRunnerExecutionState(initialNeedsNarrowing bool, initialBroadInspects int) runnerExecutionState {
	return runnerExecutionState{
		needsNarrowingAfterBroad: initialNeedsNarrowing,
		broadInspectionCount:     initialBroadInspects,
	}
}

func (s *runnerExecutionState) shouldRequireVerification(req workspace.ToolRequest, brief workerruntime.WorkerRuntimeBrief) bool {
	if s == nil {
		return false
	}

	if plannerOwnsVerification(brief) || workerruntime.VerificationContractSkipsGate(brief.VerificationContract) {
		return false
	}

	if workspace.IsWorkspaceWriteTool(req.Tool) {
		return false
	}

	return s.writesSinceVerification >= 2 && !workspace.AnalyzeVerificationRequest(req).Attempted && strings.TrimSpace(req.Tool) != workspace.ToolNameTaskLedger
}

func (s *runnerExecutionState) needsCheckpoint(policy CheckpointPolicy, req workspace.ToolRequest) bool {
	if s == nil {
		return false
	}

	return policy.Enabled && !s.checkpointCreated && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool))
}

func (s *runnerExecutionState) markCheckpointCreated() {
	if s == nil {
		return
	}

	s.checkpointCreated = true
}

func (s *runnerExecutionState) applyResult(req workspace.ToolRequest, result workspace.ToolResult, currentFingerprint string, mgr *workspace.Manager, requestBudget *runnerRequestBudgetState) runnerExecutionUpdate {
	if s == nil {
		return runnerExecutionUpdate{}
	}

	update := runnerExecutionUpdate{}

	if workspace.AnalyzeVerificationRequest(req).Attempted {
		s.verificationAttempted = true
		// Any verification attempt closes the "run verification before more
		// inspection" guard. A failed verifier opens a repair path instead;
		// completion readiness is still enforced from ToolResult evidence.
		s.writesSinceVerification = 0

		if requestBudget != nil {
			requestBudget.noteVerification()
		}

		update.ResetVerificationGuards = true
		update.VerificationState, update.VerificationDetail = verificationPhaseState(result)
	} else if toolResultMadeProgress(req, result) && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool)) {
		s.writesSinceVerification++

		if requestBudget != nil {
			requestBudget.noteWriteProgress()
		}
	}

	switch {
	case workspace.IsBroadInspectionRequest(req):
		s.needsNarrowingAfterBroad = true
		s.broadInspectionCount++
	case mgr != nil && mgr.ShouldClearBroadInspectionLock(req, result):
		s.needsNarrowingAfterBroad = false
	}

	if textutil.EqualTrimFold(req.Tool, workspace.ToolNameListFiles) {
		s.consecutiveListFiles++
	} else if workspace.IsRuntimeContentInspectionTool(req.Tool) {
		s.consecutiveListFiles = 0
	}

	sourceBodyConsumed := len(newToolResultEvidenceRecord(result).SourceWindowPaths()) > 0 ||
		(toolResultMadeProgress(req, result) && workspace.IsWorkspaceWriteTool(strings.TrimSpace(req.Tool)))
	discoveryOutcome := discoveryResultOutcomeFromResult(req, result, 6)

	if discoveryOutcome.Strength != "" {
		if s.shouldReplacePendingDiscovery(discoveryOutcome.Strength) {
			s.pendingDiscoveryConsumption = true
			s.pendingDiscoveryTool = strings.TrimSpace(req.Tool)
			s.pendingDiscoverySummary = truncForRetryReason(result.Summary)
			s.pendingDiscoveryOutput = truncForRetryReason(result.Output)
			s.pendingDiscoveryCandidates = append([]string(nil), discoveryOutcome.Candidates...)
			s.pendingDiscoveryStrength = discoveryOutcome.Strength
			s.pendingDiscoveryFailedAttempt = false
		}
	} else if sourceBodyConsumed {
		s.clearPendingDiscoveryConsumption()
	} else if s.pendingDiscoveryConsumption && workspace.IsRuntimeContentInspectionTool(req.Tool) && strings.TrimSpace(result.Error) != "" {
		s.pendingDiscoveryFailedAttempt = true
	}

	if requestBudget != nil {
		if currentFingerprint != "" && strings.TrimSpace(result.Error) == "" && workerruntime.IsZeroHitSearchResult(result) {
			requestBudget.noteZeroHitSearch(req, currentFingerprint, result)
		}

		if currentFingerprint != "" && strings.TrimSpace(result.Error) != "" && result.Facts != nil && result.Facts.RejectionDisposition != "" {
			requestBudget.recordRejectedFingerprint(currentFingerprint, req, result)
		}
	}

	return update
}

func (s *runnerExecutionState) shouldReplacePendingDiscovery(strength toolfacts.DiscoveryStrength) bool {
	return s.pendingDiscoveryStrength != toolfacts.DiscoveryStrengthSourceOwner || strength == toolfacts.DiscoveryStrengthSourceOwner
}

func (s *runnerExecutionState) clearPendingDiscoveryConsumption() {
	if s == nil {
		return
	}

	s.pendingDiscoveryConsumption = false
	s.pendingDiscoveryTool = ""
	s.pendingDiscoverySummary = ""
	s.pendingDiscoveryOutput = ""
	s.pendingDiscoveryCandidates = nil
	s.pendingDiscoveryStrength = ""
	s.pendingDiscoveryFailedAttempt = false
}

func verificationPhaseState(result workspace.ToolResult) (state string, detail string) {
	detail = strings.TrimSpace(result.Summary)
	if strings.TrimSpace(result.Error) != "" {
		state = "failed"
		detail = strings.TrimSpace(result.Error)
	} else if workspace.AnalyzeVerificationResult(result).PatchHygieneOnly {
		state = "hygiene"

		if detail == "" {
			detail = "patch hygiene completed"
		} else {
			detail += "; patch hygiene completed"
		}
	} else if workspace.AnalyzeVerificationResult(result).Successful {
		state = "done"
	} else {
		state = "insufficient"

		if detail == "" {
			detail = "verification evidence was insufficient"
		} else {
			detail += "; verification evidence was insufficient"
		}
	}

	if strings.TrimSpace(detail) == "" {
		detail = "verification completed"
	}

	return state, detail
}

func (s *runnerExecutionState) noteToolFailure(req workspace.ToolRequest, result workspace.ToolResult) int {
	if s == nil {
		return 0
	}

	if strings.TrimSpace(result.Error) == "" {
		s.consecutiveToolFailures = 0
		s.lastFailedPathStem = ""

		return 0
	}

	stem := toolFailurePathStem(req)
	if stem != "" && stem == s.lastFailedPathStem {
		s.consecutiveToolFailures++
	} else {
		s.consecutiveToolFailures = 1
		s.lastFailedPathStem = stem
	}

	return s.consecutiveToolFailures
}

func (r *runExecution) emitVerificationPhaseEvent(state string, detail string) {
	if r == nil || r.runner.OnEvent == nil || strings.TrimSpace(state) == "" {
		return
	}

	data, err := json.Marshal(map[string]string{
		"lane":   "implement",
		"phase":  "verify",
		"state":  state,
		"detail": detail,
	})
	if err != nil {
		return
	}

	r.emitEvent(RunEvent{Role: "phase", Content: string(data)})
}

func (r *runExecution) attachInstructionsForTouchedPaths(turnMessages *[]llama.ChatMessage, req workspace.ToolRequest, result workspace.ToolResult) {
	if r == nil || r.runner.InstructionAttacher == nil || turnMessages == nil || strings.TrimSpace(result.Error) != "" {
		return
	}

	for _, touched := range toolResultTouchedPaths(req, result) {
		if attachment := r.runner.InstructionAttacher.OnFileTouch(touched); attachment != nil {
			content := attachment.Render()
			if content != "" {
				*turnMessages = append(*turnMessages, llama.ChatMessage{Role: "system", Content: content})
				r.emitEvent(RunEvent{Role: "instructions_attached", Content: content})
			}
		}
	}
}

func budgetDeterministicBlocker(reason string, brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	if runnerBriefRequiresMutation(brief) {
		return synthesizeMutationFallbackAnswer(brief, results)
	}

	budgetKind := "Budget"
	lowerReason := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lowerReason, "operational model-call"):
		budgetKind = "Operational model-call limit"
	case strings.Contains(lowerReason, "model-call budget"):
		budgetKind = "Model-call budget"
	case strings.Contains(lowerReason, "duration budget") || strings.Contains(lowerReason, "run duration"):
		budgetKind = "Run duration budget"
	case strings.Contains(lowerReason, "tool budget") || strings.Contains(lowerReason, "tool-step"):
		budgetKind = "Tool budget"
	}

	var b strings.Builder
	b.WriteString(budgetKind)
	b.WriteString(" was exhausted before a final answer could be synthesized from gathered evidence.")

	if len(results) == 0 {
		b.WriteString(" No tool evidence was recorded before exhaustion.")
		return b.String()
	}

	if paths := newToolResultEvidenceProjection(results).promptEvidencePaths(6); len(paths) > 0 {
		b.WriteString("\n\nEvidence paths:\n")
		for _, path := range paths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}
	}

	if latest := latestSuccessfulToolSummary(results); latest != "" {
		b.WriteString("\nLatest recorded evidence:\n- ")
		fmt.Fprintf(&b, "%q", latest)
	}

	return strings.TrimSpace(b.String())
}

func (r *runExecution) budgetDeterministicBlocker(reason string, results []workspace.ToolResult) string {
	if r != nil && runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return r.synthesizeMutationFallbackAnswer(results)
	}
	if r == nil {
		return budgetDeterministicBlocker(reason, workerruntime.WorkerRuntimeBrief{}, results)
	}
	return budgetDeterministicBlocker(reason, r.state.runtimeBrief, results)
}

func latestSuccessfulToolSummary(results []workspace.ToolResult) string {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			return summary
		}
	}

	return ""
}

func synthesizeMutationFallbackAnswer(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) string {
	latestSourceVerification, hasLatestSourceVerification := latestVerificationAfterLatestSourceWrite(results)
	return synthesizeMutationFallbackAnswerFromState(
		brief,
		results,
		deriveImplementationProgressState(brief, results),
		postWriteVerificationLedgerForResults(results, brief),
		sourceEditsNeedSemanticVerification(brief, results),
		latestSourceVerification,
		hasLatestSourceVerification,
	)
}

func (r *runExecution) synthesizeMutationFallbackAnswer(results []workspace.ToolResult) string {
	if r == nil {
		return synthesizeMutationFallbackAnswer(workerruntime.WorkerRuntimeBrief{}, results)
	}
	latestSourceVerification, hasLatestSourceVerification := r.latestVerificationAfterLatestSourceWrite(results)
	return synthesizeMutationFallbackAnswerFromState(
		r.state.runtimeBrief,
		results,
		r.implementationCompletionState(results),
		r.postWriteVerificationLedger(results),
		r.sourceEditsNeedSemanticVerification(results),
		latestSourceVerification,
		hasLatestSourceVerification,
	)
}

func synthesizeMutationFallbackAnswerFromState(
	brief workerruntime.WorkerRuntimeBrief,
	results []workspace.ToolResult,
	completion implementationCompletionState,
	verification postWriteVerificationLedger,
	semanticVerificationPending bool,
	latestSourceVerification workspace.ToolResult,
	hasLatestSourceVerification bool,
) string {
	changedPaths := completion.ChangedPaths
	missingRequiredPaths := completion.MissingRequiredPaths
	rejectedMutationPaths := completion.UnresolvedRejectedMutationPaths

	var lastError *workspace.ToolResult

	latestProgressIndex := -1

	for i := range results {
		result := results[i]
		if strings.TrimSpace(result.Error) != "" {
			continue
		}

		if workspace.IsWorkspaceWriteTool(result.Tool) || workspace.AnalyzeVerificationResult(result).Successful {
			latestProgressIndex = i
		}
	}

	if verification.Pending() && verification.HasFailure {
		copy := verification.LatestFailed
		lastError = &copy
	} else {
		for i := range results {
			if i <= latestProgressIndex {
				continue
			}

			result := normalizeFallbackResult(results[i])
			if strings.TrimSpace(result.Error) == "" {
				continue
			}

			if isHarnessInternalRejectionResult(result) {
				continue
			}

			copy := result

			lastError = &copy
		}
	}

	var b strings.Builder

	if len(missingRequiredPaths) > 0 || len(rejectedMutationPaths) > 0 {
		b.WriteString("<<QUESTION>>: Implementation incomplete; required mutation path(s) remain unresolved.\n\n")
	} else if len(changedPaths) > 0 {
		b.WriteString(mutationFallbackStatusLeadForLedger(results, verification))
		b.WriteString("\n\n")
	}

	if len(changedPaths) > 0 {
		b.WriteString("Changed paths:\n")

		for _, path := range changedPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	} else {
		b.WriteString("Changed paths: none\n\n")
	}

	if len(missingRequiredPaths) > 0 {
		b.WriteString("Required edit paths not changed:\n")

		for _, path := range missingRequiredPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if len(rejectedMutationPaths) > 0 {
		b.WriteString("Rejected mutation paths still unresolved:\n")

		for _, path := range rejectedMutationPaths {
			b.WriteString("- ")
			b.WriteString(path)
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	if verification.Pending() && verification.HasFailure {
		b.WriteString("Verification: failed\n")
		fmt.Fprintf(&b, "Verification result: summary=%q; error=%q\n", verification.LatestFailed.Summary, verification.LatestFailed.Error)
	} else if semanticVerificationPending && hasLatestSourceVerification && workspace.AnalyzeVerificationResult(latestSourceVerification).PatchHygieneOnly {
		b.WriteString("Verification: pending\nVerification result: ")

		if summary, ok := latestSuccessfulVerificationSummary(results); ok {
			fmt.Fprintf(&b, "%q", summary)
			b.WriteString(" (patch hygiene only); ")
		}

		if plannerOwnsVerification(brief) {
			b.WriteString("source-aware lane sanity still pending; planner-owned build/test verification not run by lane.\n")
		} else {
			b.WriteString("source-aware verification pending for source edits.\n")
		}
	} else if summary, ok := latestSuccessfulVerificationSummary(results); ok {
		b.WriteString("Verification: passed\n")
		fmt.Fprintf(&b, "Verification result: %q\n", summary)
	} else if verification.Pending() {
		if plannerOwnsVerification(brief) {
			b.WriteString("Verification: pending lane sanity; build/test verification is planner-owned.\n")
		} else {
			b.WriteString("Verification: blocked; workspace writes are still pending verification.\n")
		}
	} else {
		b.WriteString("Verification: not run or not detected.\n")
	}

	if lastError != nil {
		fmt.Fprintf(&b, "\nBlocker:\n- summary=%q; error=%q\n", lastError.Summary, lastError.Error)
	} else if len(changedPaths) == 0 {
		b.WriteString("\nBlocker:\n- The implementation lane ended before a successful workspace edit was recorded.\n")
	}

	return strings.TrimSpace(b.String())
}

func mutationFallbackStatusLead(results []workspace.ToolResult, brief workerruntime.WorkerRuntimeBrief) string {
	return mutationFallbackStatusLeadForLedger(results, postWriteVerificationLedgerForResults(results, brief))
}

func mutationFallbackStatusLeadForLedger(results []workspace.ToolResult, ledger postWriteVerificationLedger) string {
	if ledger.Pending() {
		return "Workspace changes were recorded, but post-write verification is still pending."
	}

	if _, ok := latestSuccessfulVerificationSummary(results); ok {
		return "Workspace changes were recorded and verification evidence was found."
	}

	return "Workspace changes were recorded."
}

func (r *runExecution) modelErrorAfterPartialEditBlockerAnswer(modelErr error, results []workspace.ToolResult) (string, bool) {
	if r == nil || modelErr == nil || !runnerBriefRequiresMutation(r.state.runtimeBrief) {
		return "", false
	}

	if len(r.completionChangedPaths(results)) == 0 {
		return "", false
	}

	detail := strings.TrimSpace(modelErr.Error())
	if detail == "" {
		detail = "model transport failed"
	}

	fallback := strings.TrimSpace(r.synthesizeMutationFallbackAnswer(results))
	if strings.HasPrefix(fallback, "<<QUESTION>>:") {
		if _, remainder, ok := strings.Cut(fallback, "\n\n"); ok {
			fallback = strings.TrimSpace(remainder)
		} else {
			fallback = ""
		}
	}

	var b strings.Builder

	b.WriteString("<<QUESTION>>: Local model endpoint failed after workspace edits before implementation closeout: ")
	b.WriteString(detail)
	b.WriteString("\n\n")

	if fallback != "" {
		b.WriteString(fallback)
	}

	return strings.TrimSpace(b.String()), true
}

func latestSuccessfulVerificationSummary(results []workspace.ToolResult) (string, bool) {
	for i := len(results) - 1; i >= 0; i-- {
		result := results[i]
		observed := workspace.AnalyzeVerificationResult(result)
		if !observed.Successful {
			continue
		}

		if command := strings.TrimSpace(observed.Command); command != "" {
			return command + " passed", true
		}

		if summary := strings.TrimSpace(result.Summary); summary != "" {
			if strings.Contains(strings.ToLower(summary), "passed") {
				return summary, true
			}

			return summary + " passed", true
		}

		return "verification passed", true
	}

	return "", false
}

// extractReadFilePath pulls the path out of a read_file result's summary
// (the canonical format is "Read <display> lines N-M"). Returns empty on
// any parse failure so the caller just skips the observation.
func extractReadFilePath(summary string) string {
	trimmed := strings.TrimSpace(summary)
	if !strings.HasPrefix(trimmed, "Read ") {
		return ""
	}

	rest := strings.TrimPrefix(trimmed, "Read ")
	idx := strings.Index(rest, " lines ")

	if idx <= 0 {
		return ""
	}

	return strings.TrimSpace(rest[:idx])
}

// normalizeFallbackResult prepares a tool result for fallback synthesis by
// removing nested fallback wrappers from a delegated child's Output. When a
// child lane force-completes, its answer is already a "Partial findings from
// this turn..." template; that wrapper bleeds into the parent's view of the
// task result and produces nested "Partial findings" headers in the final
// answer. We strip the outer template so the substance (Verified context
// bullets, observations) folds cleanly into the parent's own template.
func normalizeFallbackResult(result workspace.ToolResult) workspace.ToolResult {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameTask {
		return result
	}

	stripped := stripNestedFallbackTemplate(result.Output)
	if stripped != result.Output {
		result.Output = stripped
	}

	return result
}

// firstBulletText returns the text of the first bullet ("- ") in s, with the
// leading dash trimmed. Returns the empty string if no bullet is found.

// isHarnessRejectionSummary returns true for tool-result Summary strings that
// originate from harness contracts (post-readiness gap requirement, packet
// anchoring, etc.) rather than from real tool failures. These are not useful
// "Final blocker" lines -- the user did nothing wrong and the message is
// internal to the harness's own enforcement loop.

// isHarnessRejectionErrorText returns true when the error text reads like a
// harness contract message rather than a real tool error. Used as a fallback
// when the Summary itself isn't enough to classify the rejection.

func conversationLaneWithoutSemanticEval(brief workerruntime.WorkerRuntimeBrief) bool {
	brief = workerruntime.NormalizeRuntimeBrief(brief)
	if !readOnlyDirectAnswerTaskFamily(brief.TaskFamily) ||
		!workerruntime.RuntimeContractIsDirectAnswer(brief.Contract) {

		return false
	}

	return !readOnlyDirectAnswerLane(brief) || strings.TrimSpace(brief.Objective) == ""
}

// emptyAnswerRetryPrompt builds the retry system message when the model
// produces an empty answer. The prompt re-anchors to the user's original
// objective so the model reorients rather than repeating its mistake.
func emptyAnswerRetryPrompt(brief workerruntime.WorkerRuntimeBrief) string {
	base := "Your previous response was empty. Tools are disabled now — do NOT emit a tool block. "
	if obj := strings.TrimSpace(brief.Objective); obj != "" {
		base += fmt.Sprintf("The user asked: %q. ", obj)
	}

	base += "Reply with a direct, non-empty answer based on the evidence already gathered. Keep it short " +
		"(a few sentences or a brief list). No preamble like \"Based on...\" or \"Here is...\" — just the answer. " +
		"If one exact claim remains unsupported, state that uncertainty after the supported answer in one sentence."

	return base
}

func finishShouldOverrideSourceEcho(answer string) bool {
	if !looksLikeSourceEchoOnlyAnswer(answer) {
		return false
	}

	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "no files were changed") {
		return false
	}

	for _, path := range pathparse.MentionedWorkspacePaths(answer) {
		path = NormalizeRunnerPath(path)
		if strings.HasPrefix(path, "internal/") || strings.HasPrefix(path, "logs/") {
			return true
		}
	}

	return false
}

// cleanToolRequestPathDisplay returns a short display value for a tool
// request path suitable for progress nudges and event logs.
func cleanToolRequestPathDisplay(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return "."
	}

	return p
}

// looksLikeNonAnswerProse checks whether the answer text is clearly a
// non-answer (tool-call markup, echoed user question, tool-failure
// language without substantive findings) rather than an actual answer.
// These patterns indicate the model emitted transport-level noise
// instead of addressing the user's request.

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}

	return false
}

// finalEvidenceProjection separates producer-observed facts from claims made in
// the final answer. Terminal acceptance may enforce only a claim that maps to
// producer-owned evidence; semantic answer quality remains model guidance.
type finalEvidenceProjection struct {
	ChangedPaths          []string
	ObservedPaths         []string
	InspectedPaths        []string
	ClaimedPaths          []string
	CanonicalClaimedPaths []string
	ArtifactIDs           []string
	Verification          finalVerificationEvidence
	ReportedVerification  string
	TerminalDisposition   implementationTerminalDisposition
	GitHistorySatisfied   bool
}

type finalVerificationEvidence struct {
	Attempted       bool
	Outcome         string
	Kind            string
	Command         string
	EvidenceEpoch   int
	CommandReported bool
}

func (r *runExecution) finalEvidenceProjection(answer string, results []workspace.ToolResult) finalEvidenceProjection {
	completion := implementationCompletionState{}
	if r != nil {
		completion = r.implementationCompletionStateForTerminalAnswer(answer, results)
		if !completion.CloseoutReady() {
			completion.TerminalDisposition = implementationTerminalDispositionNone
		}
	}
	projection := newFinalEvidenceProjection(answer, results, completion)
	if r == nil {
		return projection
	}
	projection.ChangedPaths = uniqueSortedRunnerPaths(r.state.committedMutationEffectIDs())
	projection.ObservedPaths = uniqueSortedRunnerPaths(textutil.Merge(projection.ObservedPaths, projection.ChangedPaths))
	if r.state.mutation.bound {
		claims := r.state.projectMutationClaims(projection.ClaimedPaths)
		projection.CanonicalClaimedPaths = uniqueSortedRunnerPaths(textutil.Merge(projection.CanonicalClaimedPaths, claims.CanonicalIDs))
	}
	projection.GitHistorySatisfied = finalGitHistorySatisfied(
		results,
		completion,
		changedPathsRequiringGitHistoryForReceipts(r.state.mutation.commitReceipts, r.state.mutation.newFileEffectIDs),
		len(projection.ChangedPaths) > 0,
	)
	return projection
}

func newFinalEvidenceProjection(answer string, results []workspace.ToolResult, completion implementationCompletionState) finalEvidenceProjection {
	evidence := newToolResultEvidenceProjection(results)
	var changedPaths, observed, inspected, artifactIDs []string
	for index, result := range results {
		record := evidence.records[index]
		if receipt := result.CommitReceipt(); receipt != nil {
			for _, effect := range receipt.DurableProjection().Effects {
				changedPaths = textutil.AppendUnique(changedPaths, effect.CanonicalID)
				observed = textutil.AppendUnique(observed, effect.CanonicalID)
			}
		}
		if result.Facts != nil {
			artifactIDs = textutil.Merge(artifactIDs, result.Facts.ArtifactIDs)
			inspected = textutil.Merge(inspected, result.Facts.DiagnosticEvidencePaths)
			observed = textutil.Merge(observed, result.Facts.DiagnosticEvidencePaths)
			for _, window := range record.sourceWindowFacts {
				inspected = textutil.Merge(inspected, []string{window.CanonicalID})
				observed = textutil.Merge(observed, []string{window.CanonicalID})
			}
			for _, logRead := range record.diagnosticLogReads {
				inspected = textutil.Merge(inspected, []string{logRead.CanonicalID})
				observed = textutil.Merge(observed, []string{logRead.CanonicalID})
			}
		}
		for _, relationship := range result.Relationships {
			observed = textutil.Merge(observed, []string{relationship.SourcePath, relationship.TargetPath})
			inspected = textutil.Merge(inspected, []string{relationship.SourcePath, relationship.TargetPath})
		}
	}

	projection := finalEvidenceProjection{
		ChangedPaths:          uniqueSortedRunnerPaths(changedPaths),
		ObservedPaths:         uniqueSortedRunnerPaths(observed),
		InspectedPaths:        uniqueSortedRunnerPaths(inspected),
		ClaimedPaths:          uniqueSortedRunnerPaths(pathparse.MentionedWorkspacePaths(answer)),
		CanonicalClaimedPaths: uniqueSortedRunnerPaths(evidence.ClaimedDiagnosticLogPaths(answer, 0)),
		ArtifactIDs:           textutil.Unique(artifactIDs),
		ReportedVerification:  finalReportedVerificationOutcome(answer),
		TerminalDisposition:   completion.TerminalDisposition,
	}
	for index, result := range results {
		verification := workspace.AnalyzeVerificationResult(result)
		if !verification.Attempted {
			continue
		}

		outcome := "failed"
		switch failures.CodeOf(result.Failure) {
		case failures.DeadlineExceeded:
			outcome = "timed_out"
		case failures.Canceled:
			outcome = "cancelled"
		default:
			if verification.Successful {
				outcome = "passed"
			}
		}
		command := strings.TrimSpace(verification.Command)
		projection.Verification = finalVerificationEvidence{
			Attempted:       true,
			Outcome:         outcome,
			Kind:            strings.TrimSpace(result.Tool),
			Command:         command,
			EvidenceEpoch:   index + 1,
			CommandReported: command != "" && strings.Contains(answer, command),
		}
	}
	projection.GitHistorySatisfied = finalGitHistorySatisfied(results, completion, changedPathsRequiringGitHistory(results), len(projection.ChangedPaths) > 0)
	return projection
}

func finalGitHistorySatisfied(results []workspace.ToolResult, completion implementationCompletionState, requiredPaths []string, hasChanges bool) bool {
	if len(requiredPaths) == 0 {
		return successfulGitHistoryEvidenceSeen(results) ||
			completion.TerminalDisposition == implementationTerminalDispositionSourceSatisfied ||
			completion.TerminalDisposition == implementationTerminalDispositionExactBlocker ||
			hasChanges
	}
	for _, path := range requiredPaths {
		if !successfulGitHistoryResultForPath(path, results) {
			return false
		}
	}
	return true
}

func finalReportedVerificationOutcome(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if !strings.HasPrefix(line, "verification:") {
			continue
		}
		status := strings.TrimSpace(strings.TrimPrefix(line, "verification:"))
		if ContainsAny(status, "cancelled", "canceled") {
			return "cancelled"
		}
		if ContainsAny(status, "timed out", "timeout", "deadline exceeded") {
			return "timed_out"
		}
		for _, field := range strings.FieldsFunc(status, func(r rune) bool { return !unicode.IsLetter(r) }) {
			switch field {
			case "failed", "failure", "error":
				return "failed"
			case "passed", "pass", "successful", "success":
				return "passed"
			}
		}
	}
	return ""
}

func (projection finalEvidenceProjection) claimedObservedPaths() bool {
	return projection.claimedPathsCover(projection.ObservedPaths)
}

func (projection finalEvidenceProjection) claimedPathsCover(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	claimed := make(map[string]struct{}, len(projection.ClaimedPaths)+len(projection.CanonicalClaimedPaths))
	for _, path := range projection.ClaimedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			claimed[normalized] = struct{}{}
		}
	}
	for _, path := range projection.CanonicalClaimedPaths {
		if normalized := NormalizeRunnerPath(path); normalized != "" {
			claimed[normalized] = struct{}{}
		}
	}
	for _, path := range paths {
		if _, ok := claimed[NormalizeRunnerPath(path)]; !ok {
			return false
		}
	}
	return true
}

func finalEvidenceRequirementSatisfied(requirement workerruntime.EvidenceRequirementID, projection finalEvidenceProjection) (bool, bool) {
	switch requirement {
	case workerruntime.RequiredEvidenceTargetLocation:
		return projection.claimedObservedPaths(), true
	case workerruntime.RequiredEvidenceChangedPaths:
		return projection.claimedPathsCover(projection.ChangedPaths), true
	case workerruntime.RequiredEvidenceChangedPathsOrNoopReason,
		workerruntime.RequiredEvidenceChangedFilesOrNoopReason,
		workerruntime.RequiredEvidenceChangedPathsOrNoopReasonRepair:
		return projection.claimedPathsCover(projection.ChangedPaths) ||
			projection.TerminalDisposition == implementationTerminalDispositionSourceSatisfied ||
			projection.TerminalDisposition == implementationTerminalDispositionExactBlocker, true
	case workerruntime.RequiredEvidenceVerificationStatus,
		workerruntime.RequiredEvidenceVerificationResult,
		workerruntime.RequiredEvidencePassFailStatus:
		return projection.Verification.Attempted && projection.Verification.Outcome != "" &&
			projection.ReportedVerification == projection.Verification.Outcome, true
	case workerruntime.RequiredEvidenceVerificationCommand:
		return projection.Verification.Command != "" && projection.Verification.CommandReported, true
	case workerruntime.RequiredEvidencePlanSubmittedOrSourceSatisfied:
		return projection.TerminalDisposition == implementationTerminalDispositionPlanSubmitted ||
			projection.TerminalDisposition == implementationTerminalDispositionSourceSatisfied ||
			projection.TerminalDisposition == implementationTerminalDispositionExactBlocker, true
	case workerruntime.RequiredEvidenceGitHistoryRationale:
		return projection.GitHistorySatisfied, true
	case workerruntime.RequiredEvidenceInspectedPaths:
		return projection.claimedPathsCover(projection.InspectedPaths), true
	case workerruntime.RequiredEvidenceExactWorkspacePath:
		// ExactWorkspacePath awaits producer-owned ProjectClaims. Path-shaped
		// answer text is not authority and remains guidance for now.
		return false, false
	case workerruntime.RequiredEvidenceArtifactBodyOrTypedBlocker:
		// ArtifactIDs establish artifact identity, not body validity. Until the
		// producer exposes a body-validation receipt, keep this as guidance.
		return false, false
	default:
		return false, false
	}
}

// missingRequiredEvidence returns only structurally verifiable requirements
// that are not satisfied by producer-owned facts. Semantic requirements remain
// model guidance and are intentionally absent from this gate.
func missingRequiredEvidence(evidence []string, projection finalEvidenceProjection) []string {
	var missing []string
	for _, label := range evidence {
		trimmed := strings.TrimSpace(label)
		satisfied, verifiable := finalEvidenceRequirementSatisfied(workerruntime.EvidenceRequirementID(trimmed), projection)
		if verifiable && !satisfied {
			missing = append(missing, trimmed)
		}
	}
	return missing
}

func changedPathsRequiringGitHistory(results []workspace.ToolResult) []string {
	var receipts []workspace.MutationCommitProjection
	var newFileEffectIDs []string
	for _, result := range results {
		if receipt := result.CommitReceipt(); receipt != nil {
			projection := receipt.DurableProjection()
			receipts = append(receipts, projection)
			if projection.Scope != nil {
				newFileEffectIDs = textutil.Merge(newFileEffectIDs, authorizedNewFileIDs(*projection.Scope))
			}
			for _, commit := range receipt.Commits {
				if commit.Preimage.Class != workspace.MutationPreimageAbsent {
					continue
				}
				switch commit.Kind {
				case workspace.MutationCommitReplacedFile, workspace.MutationCommitAppendedFile:
					newFileEffectIDs = textutil.Merge(newFileEffectIDs, []string{commit.Identity.CanonicalID})
				}
			}
		}
	}
	return changedPathsRequiringGitHistoryForReceipts(receipts, newFileEffectIDs)
}

func changedPathsRequiringGitHistoryForReceipts(receipts []workspace.MutationCommitProjection, newFileEffectIDs []string) []string {
	newFiles := runnerArtifactPathSet(newFileEffectIDs)
	var paths []string

	for _, receipt := range receipts {
		for _, path := range receipt.PostimageFileIDs {
			normalized := NormalizeRunnerPath(path)
			if normalized == "" || writePathIsNonSource(normalized) {
				continue
			}
			if _, newFile := newFiles[normalized]; newFile {
				continue
			}

			paths = append(paths, normalized)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func stripFallbackJSONArtifacts(output string) string {
	if !strings.Contains(output, "{") {
		return output
	}

	lines := strings.Split(output, "\n")
	out := make([]string, 0, len(lines))
	skipUntilBaseline := false
	skipDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipUntilBaseline {
			// Track nested braces. We exit the skip when depth returns
			// to zero, when we see a blank line, or when a new bullet
			// starts (lines beginning with "- " mark the next section).
			skipDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if skipDepth <= 0 {
				skipUntilBaseline = false
				skipDepth = 0
			}

			if trimmed == "" || strings.HasPrefix(trimmed, "- ") {
				skipUntilBaseline = false
				skipDepth = 0

				if strings.HasPrefix(trimmed, "- ") {
					out = append(out, line)
				}
			}

			continue
		}

		// A bare "{" line, or a "{...}" JSON one-liner whose first
		// inner content is a quoted key, marks the start of a metadata
		// dump. Drop the block.
		if trimmed == "{" || (strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "\":")) {
			skipUntilBaseline = true
			skipDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")

			if skipDepth <= 0 {
				// One-liner JSON; consume just this line.
				skipUntilBaseline = false
				skipDepth = 0
			}

			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

type runnerFinalAnswerRetryInput struct {
	stepNumber       int
	activeToolCall   *llama.ToolCall
	assistantMessage llama.ChatMessage
	acceptance       finalAnswerAcceptance
	result           workspace.ToolResult
	controller       *runnerTurnController
}

func (r *runExecution) retryRejectedFinalAnswerForRun(input runnerFinalAnswerRetryInput) bool {
	retry := r.retryRejectedFinalAnswer(input.controller, input.stepNumber, input.activeToolCall, input.assistantMessage, input.acceptance, input.result)
	if !retry {
		return false
	}
	r.rebuildRunTools()

	return true
}
