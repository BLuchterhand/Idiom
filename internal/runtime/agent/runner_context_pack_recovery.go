package agent

import (
	"context"
	"encoding/json"
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/util"
	"idiom/internal/runtime/workerruntime"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func contextPackUnavailableForRecovery(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack {
			continue
		}

		if strings.TrimSpace(result.Error) != "" {
			return true
		}

		if !contextPackResultHasTargets(result) {
			return true
		}
	}

	return false
}

func (r *Runner) recoverContextPackFreshness(ctx context.Context, req workspace.ToolRequest, result workspace.ToolResult) workspace.ToolResult {
	if r == nil || r.Workspace == nil || strings.TrimSpace(req.Tool) != workspace.ToolNameContextPack || !contextPackResultNeedsFreshnessRecovery(result) {
		return result
	}

	r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_pack freshness blocked; refreshing context status before retry"})

	refresh := workspace.ToolRequest{
		Tool:              workspace.ToolNameContextStatus,
		Refresh:           true,
		Limit:             50,
		FreshnessMaxFiles: 2000,
		RequiredEditPaths: append([]string(nil), req.RequiredEditPaths...),
	}
	if strings.TrimSpace(req.Language) != "" {
		refresh.Language = req.Language
	}

	refreshResult := r.Workspace.Execute(ctx, refresh)
	if summary := strings.TrimSpace(util.FirstNonEmpty(refreshResult.Summary, refreshResult.Error)); summary != "" {
		r.emitEvent(RunEvent{Role: "context_pack_freshness_recovery", Content: "context_status refresh result: " + util.Truncate(strings.TrimSpace(summary), 260)})
	}

	retry := r.Workspace.Execute(ctx, req)
	if strings.TrimSpace(retry.Error) == "" || strings.TrimSpace(refreshResult.Error) == "" {
		return retry
	}

	return result
}

func contextPackResultNeedsFreshnessRecovery(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Error) == "freshness_blocker" {
		return true
	}

	text := strings.ToLower(strings.TrimSpace(result.Summary + "\n" + result.Output))

	return strings.Contains(text, "freshness_blocker") ||
		strings.Contains(text, "stale_unbounded_graph") ||
		strings.Contains(text, "context freshness")
}

func contextPackSeen(results []workspace.ToolResult) bool {
	for _, result := range results {
		if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
			return true
		}
	}

	return false
}

func contextPackResultHasTargets(result workspace.ToolResult) bool {
	output := strings.TrimSpace(result.Output)
	if output == "" {
		return false
	}

	var view struct {
		CandidateFiles   []json.RawMessage `json:"candidate_files"`
		CandidateSymbols []json.RawMessage `json:"candidate_symbols"`
		Tests            []json.RawMessage `json:"tests"`
	}

	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return false
	}

	return len(view.CandidateFiles) > 0 || len(view.CandidateSymbols) > 0 || len(view.Tests) > 0
}

// contextPackCandidateSymbols extracts the top-ranked symbol references from
// a context_pack view JSON. It prefers top-level candidate_symbols and packet
// quality evidence, then falls back to candidate_files.handles from older views.
func contextPackCandidateSymbols(output string, max int) []string {
	output = strings.TrimSpace(output)
	if output == "" || max <= 0 {
		return nil
	}

	var view struct {
		Task    string `json:"task"`
		Quality struct {
			EvidencePaths []string `json:"evidence_paths"`
		} `json:"quality"`
		CandidateFiles []struct {
			Path    string   `json:"path"`
			Role    string   `json:"role"`
			Reason  string   `json:"reason"`
			Handles []string `json:"handles"`
		} `json:"candidate_files"`
		CandidateSymbols []struct {
			Path          string `json:"path"`
			Kind          string `json:"kind"`
			QualifiedName string `json:"qualified_name"`
			Reason        string `json:"reason"`
		} `json:"candidate_symbols"`
	}

	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return nil
	}

	evidenceRank := map[string]int{}

	for i, path := range view.Quality.EvidencePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if _, ok := evidenceRank[path]; !ok {
			evidenceRank[path] = i
		}
	}

	tokens := runnerEvidenceTokens(view.Task)
	candidates := make([]contextPackSymbolCandidate, 0, len(view.CandidateSymbols))

	for _, symbol := range view.CandidateSymbols {
		path := strings.TrimSpace(symbol.Path)
		name := strings.TrimSpace(symbol.QualifiedName)

		if path == "" || name == "" {
			continue
		}

		candidates = append(candidates, contextPackSymbolCandidate{
			value:        path + "#" + name,
			path:         path,
			evidenceRank: contextPackEvidenceRank(evidenceRank, path),
			score:        runnerEvidenceOverlap(tokens, strings.Join([]string{path, symbol.Kind, name, symbol.Reason}, " ")),
		})
	}

	sortContextPackSymbolCandidates(candidates)

	var symbols []string

	seen := map[string]struct{}{}
	addSymbol := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(symbols) >= max {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}
		symbols = append(symbols, value)
	}

	for _, candidate := range candidates {
		addSymbol(candidate.value)
	}

	for _, f := range view.CandidateFiles {
		if len(symbols) >= max {
			break
		}

		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}

		if strings.TrimSpace(f.Role) == "test" {
			continue
		}

		for _, handle := range f.Handles {
			if len(symbols) >= max {
				break
			}

			handle = strings.TrimPrefix(strings.TrimSpace(handle), "symbol://")
			if handle == "" || handle == path {
				continue
			}

			addSymbol(handle)
		}
	}

	if len(symbols) == 0 {
		return nil
	}

	return symbols
}

type contextPackSymbolCandidate struct {
	value        string
	path         string
	evidenceRank int
	score        int
}

func contextPackEvidenceRank(ranks map[string]int, path string) int {
	if len(ranks) == 0 {
		return -1
	}

	if rank, ok := ranks[strings.TrimSpace(path)]; ok {
		return rank
	}

	return -1
}

func sortContextPackSymbolCandidates(candidates []contextPackSymbolCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].evidenceRank >= 0 || candidates[j].evidenceRank >= 0 {
			if candidates[i].evidenceRank < 0 {
				return false
			}

			if candidates[j].evidenceRank < 0 {
				return true
			}

			if candidates[i].evidenceRank != candidates[j].evidenceRank {
				return candidates[i].evidenceRank < candidates[j].evidenceRank
			}
		}

		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}

		return candidates[i].value < candidates[j].value
	})
}

func concreteRecoveryEvidencePaths(results []workspace.ToolResult, max int) []string {
	filtered := make([]workspace.ToolResult, 0, len(results))
	for _, result := range results {
		switch strings.TrimSpace(result.Tool) {
		case workspace.ToolNameContextPack, "completion_gate":
			continue
		default:
			filtered = append(filtered, result)
		}
	}

	return promptEvidencePathsFromToolResults(filtered, max)
}

func discoveryConsumptionCandidatesFromResult(result workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	if strings.TrimSpace(result.Error) != "" {
		return nil
	}

	if strings.TrimSpace(result.Tool) == workspace.ToolNameContextPack {
		return contextPackCandidatePathsFromResults([]workspace.ToolResult{result}, max)
	}

	return promptEvidencePathsFromToolResults([]workspace.ToolResult{result}, max)
}

func recoveryEvidencePathsForObjective(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	searchRanked := rankedSearchContentEvidencePaths(objective, results, max)
	if len(searchRanked) >= max {
		return searchRanked[:max]
	}

	seen := make(map[string]struct{}, len(searchRanked))
	paths := make([]string, 0, max)

	for _, path := range searchRanked {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range concreteRecoveryEvidencePaths(results, max*2) {
		if _, ok := seen[path]; ok {
			continue
		}

		seen[path] = struct{}{}
		paths = append(paths, path)

		if len(paths) >= max {
			break
		}
	}

	return paths
}

func candidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := make(map[string]struct{}, max)
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" {
			return
		}

		if !planThenExecuteRecoveryScopePathAllowed(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	for _, path := range brief.AllowedEditPaths {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range recoveryEvidencePathsForObjective(brief.Objective, results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	for _, path := range contextPackCandidatePathsFromResults(results, max) {
		add(path)

		if len(paths) >= max {
			return paths
		}
	}

	return paths
}

func contextPackCandidatePathsFromResults(results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	seen := map[string]struct{}{}
	paths := make([]string, 0, max)
	add := func(path string) {
		path = normalizeRunnerPath(path)
		if path == "" || !pathparse.HasWorkspaceFileExtension(path) {
			return
		}

		if _, ok := seen[path]; ok {
			return
		}

		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	addRanked := func(candidates []contextPackEvidenceCandidate) bool {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}

			if candidates[i].source != candidates[j].source {
				return candidates[i].source
			}

			return candidates[i].path < candidates[j].path
		})

		for _, candidate := range candidates {
			if candidate.score <= 0 {
				continue
			}

			add(candidate.path)

			if len(paths) >= max {
				return true
			}
		}

		return false
	}

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(result.Output) == "" {
			continue
		}

		var view struct {
			Task    string `json:"task"`
			Quality struct {
				LocalizationConfidence string   `json:"localization_confidence"`
				SourceOwnerFound       bool     `json:"source_owner_found"`
				EvidencePaths          []string `json:"evidence_paths"`
			} `json:"quality"`
			CandidateFiles []struct {
				Path    string   `json:"path"`
				Role    string   `json:"role"`
				Reason  string   `json:"reason"`
				Handles []string `json:"handles"`
			} `json:"candidate_files"`
			CandidateSymbols []struct {
				Path          string `json:"path"`
				Kind          string `json:"kind"`
				QualifiedName string `json:"qualified_name"`
				Reason        string `json:"reason"`
			} `json:"candidate_symbols"`
			Tests []struct {
				Path    string `json:"path"`
				Symbol  string `json:"symbol"`
				Command string `json:"command"`
				Reason  string `json:"reason"`
			} `json:"tests"`
		}

		if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
			continue
		}

		weakQuality := strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
		for _, path := range view.Quality.EvidencePaths {
			if weakQuality && !runnerEvidencePathIsTest(path) {
				continue
			}

			add(path)

			if len(paths) >= max {
				return paths
			}
		}

		if tokens := runnerEvidenceTokens(view.Task); len(tokens) > 0 {
			candidates := make([]contextPackEvidenceCandidate, 0, len(view.CandidateFiles)+len(view.CandidateSymbols)+len(view.Tests))
			for _, file := range view.CandidateFiles {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   file.Path,
					source: !runnerEvidencePathIsTest(file.Path),
					score:  runnerEvidenceOverlap(tokens, strings.Join(append([]string{file.Path, file.Role, file.Reason}, file.Handles...), " ")),
				})
			}

			for _, symbol := range view.CandidateSymbols {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:   symbol.Path,
					source: !runnerEvidencePathIsTest(symbol.Path) && symbol.Kind != "test",
					score:  runnerEvidenceOverlap(tokens, strings.Join([]string{symbol.Path, symbol.Kind, symbol.QualifiedName, symbol.Reason}, " ")),
				})
			}

			for _, test := range view.Tests {
				candidates = append(candidates, contextPackEvidenceCandidate{
					path:  test.Path,
					score: runnerEvidenceOverlap(tokens, strings.Join([]string{test.Path, test.Symbol, test.Command, test.Reason}, " ")),
				})
			}

			if addRanked(candidates) {
				return paths
			}
		}

		for _, file := range view.CandidateFiles {
			add(file.Path)

			if len(paths) >= max {
				return paths
			}
		}

		for _, symbol := range view.CandidateSymbols {
			add(symbol.Path)

			if len(paths) >= max {
				return paths
			}
		}

		for _, test := range view.Tests {
			add(test.Path)

			if len(paths) >= max {
				return paths
			}
		}
	}

	return paths
}

type contextPackEvidenceCandidate struct {
	path   string
	source bool
	score  int
}

func contextPackResultWeakSourceOwner(result workspace.ToolResult) bool {
	if strings.TrimSpace(result.Tool) != workspace.ToolNameContextPack || strings.TrimSpace(result.Output) == "" || strings.TrimSpace(result.Error) != "" {
		return false
	}

	var view struct {
		Quality struct {
			LocalizationConfidence string `json:"localization_confidence"`
			SourceOwnerFound       bool   `json:"source_owner_found"`
		} `json:"quality"`
	}

	if err := json.Unmarshal([]byte(result.Output), &view); err != nil {
		return false
	}

	return strings.TrimSpace(view.Quality.LocalizationConfidence) == "low" && !view.Quality.SourceOwnerFound
}

func runnerEvidenceTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, token := range splitRunnerEvidenceText(text) {
		if len(token) < 3 || runnerEvidenceStopToken(token) {
			continue
		}

		out[runnerEvidenceStem(token)] = struct{}{}
	}

	return out
}

func runnerEvidenceOverlap(tokens map[string]struct{}, text string) int {
	seen := map[string]struct{}{}

	for _, token := range splitRunnerEvidenceText(text) {
		stem := runnerEvidenceStem(token)
		if _, ok := tokens[stem]; ok {
			seen[stem] = struct{}{}
		}
	}

	return len(seen)
}

func splitRunnerEvidenceText(text string) []string {
	var (
		tokens []string
		b      strings.Builder
	)

	flush := func() {
		if b.Len() == 0 {
			return
		}

		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}

	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			flush()
			b.WriteRune(r + ('a' - 'A'))

			continue
		}

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			continue
		}

		flush()
	}

	flush()

	return tokens
}

func runnerEvidenceStem(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return runnerEvidenceTrimDoubledFinalConsonant(strings.TrimSuffix(token, suffix))
		}
	}

	return token
}

func runnerEvidenceTrimDoubledFinalConsonant(token string) string {
	if len(token) < 2 {
		return token
	}

	last := token[len(token)-1]
	prev := token[len(token)-2]

	if last == prev {
		switch last {
		case 'b', 'd', 'g', 'm', 'n', 'p', 'r', 't':
			return token[:len(token)-1]
		}
	}

	return token
}

func runnerEvidenceStopToken(token string) bool {
	switch token {
	case "the", "and", "for", "with", "where", "when", "that", "this", "from", "into", "over",
		"do", "not", "no", "only", "same", "way", "longer", "first", "later", "time",
		"bug", "fix", "issue", "fail", "fails", "failure", "source", "file", "files", "symbol", "symbols",
		"research", "repo", "repository", "edit", "edits", "editing", "investigate", "identify", "name",
		"likely", "smallest", "proper", "propose", "return", "answer", "evidence", "codebase", "exact",
		"missing", "insufficient", "sufficient", "claim":
		return true
	default:
		return false
	}
}

func runnerEvidencePathIsTest(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))

	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/fixtures/") ||
		strings.Contains(path, "/tests/")
}

func unreadCandidateRecoveryScopePaths(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := readPathsFromResults(results)
	for _, path := range candidates {
		if _, ok := read[normalizeRunnerPath(path)]; ok {
			return nil
		}
	}

	unread := make([]string, 0, len(candidates))

	unread = append(unread, candidates...)

	return unread
}

func unreadCandidateRecoveryScopePathsForRecovery(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult, max int) []string {
	candidates := candidateRecoveryScopePaths(brief, results, max)
	if len(candidates) == 0 {
		return nil
	}

	read := readPathsFromResults(results)
	unread := make([]string, 0, len(candidates))

	for _, path := range candidates {
		normalized := normalizeRunnerPath(path)
		if normalized == "" {
			continue
		}

		if candidateRecoveryScopePathHasMissingFileEvidence(normalized, results) {
			continue
		}

		if _, ok := read[normalized]; ok {
			continue
		}

		unread = append(unread, normalized)
	}

	return unread
}

func candidateRecoveryScopePathHasMissingFileEvidence(path string, results []workspace.ToolResult) bool {
	normalized := normalizeRunnerPath(path)
	if normalized == "" {
		return false
	}

	lowerPath := strings.ToLower(normalized)
	for _, result := range results {
		text := strings.ToLower(strings.Join([]string{result.Summary, result.Error, result.Output}, "\n"))
		if !strings.Contains(text, lowerPath) {
			continue
		}

		if strings.Contains(text, "file not found") ||
			strings.Contains(text, "no such file") ||
			strings.Contains(text, "cannot find the file") ||
			strings.Contains(text, "does not exist") {

			return true
		}
	}

	return false
}

func rankedSearchContentEvidencePaths(objective string, results []workspace.ToolResult, max int) []string {
	if max <= 0 {
		max = 4
	}

	terms := recoveryObjectiveSignalTerms(objective)
	lowerObjective := strings.ToLower(objective)
	uiObjective := objectiveMentionsUI(lowerObjective)

	type scoredPath struct {
		path  string
		score int
		order int
	}

	scores := map[string]*scoredPath{}
	order := 0

	for _, result := range results {
		if strings.TrimSpace(result.Tool) != workspace.ToolNameSearchContent || strings.TrimSpace(result.Error) != "" {
			continue
		}

		for _, line := range strings.Split(result.Output, "\n") {
			path, text, ok := searchContentPathAndText(line)
			if !ok {
				continue
			}

			entry, exists := scores[path]
			if !exists {
				entry = &scoredPath{path: path, order: order}
				scores[path] = entry
				order++
			}

			entry.score += recoverySearchLineScore(path, text, terms, uiObjective)
		}
	}

	if len(scores) == 0 {
		return nil
	}

	paths := make([]scoredPath, 0, len(scores))
	for _, entry := range scores {
		paths = append(paths, *entry)
	}

	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].score != paths[j].score {
			return paths[i].score > paths[j].score
		}

		return paths[i].order < paths[j].order
	})

	out := make([]string, 0, min(max, len(paths)))
	for _, entry := range paths {
		if entry.score <= -50 {
			continue
		}

		out = append(out, entry.path)
		if len(out) >= max {
			break
		}
	}

	return out
}

func searchContentPathAndText(line string) (path string, text string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}

	firstColon := strings.Index(line, ":")
	if firstColon < 0 {
		return "", "", false
	}

	before := strings.TrimSpace(line[:firstColon])
	fields := strings.Fields(before)

	if len(fields) == 0 {
		return "", "", false
	}

	path = fields[len(fields)-1]
	if !recoverySearchPathLooksEditableSource(path) || !strings.Contains(path, "/") {
		return "", "", false
	}

	rest := line[firstColon+1:]
	secondColon := strings.Index(rest, ":")

	if secondColon >= 0 {
		text = strings.TrimSpace(rest[secondColon+1:])
	}

	return filepath.ToSlash(path), text, true
}

func recoverySearchLineScore(path string, text string, terms []string, uiObjective bool) int {
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	lowerText := strings.ToLower(text)
	score := 1

	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			score += 10
		}

		if strings.Contains(lowerText, term) {
			score += 12
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		score -= 100
	}

	if strings.HasPrefix(strings.TrimSpace(lowerText), "//") {
		score -= 8
	}

	if strings.Contains(lowerPath, "tool_schemas.go") || strings.Contains(lowerPath, "/agent/") || strings.Contains(lowerPath, "/eval/") {
		score -= 30
	}

	if strings.HasPrefix(lowerPath, "cmd/") && !containsAny(lowerText, "implementation", "core", "internal") {
		score -= 20
	}

	if slices.Contains(terms, "apply") && containsAny(lowerPath, "migration_apply", "_apply.", "/apply") {
		score += 60
	}

	if uiObjective {
		if strings.HasPrefix(lowerPath, "internal/surfaces/tui/app/") {
			score += 45
		}

		if containsAny(lowerPath, "render", "view", "model_state", "update_handlers") {
			score += 25
		}

		if containsAny(lowerPath, "frontier/", "comprehension/", "workspace/") {
			score -= 25
		}
	}

	return score
}

func recoveryObjectiveSignalTerms(objective string) []string {
	pattern := literalRecoverySearchPattern(objective)
	if pattern == "" {
		return nil
	}

	parts := strings.Split(pattern, "|")
	terms := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}

		terms = append(terms, strings.ReplaceAll(part, `\`, ""))
	}

	return terms
}

func objectiveMentionsUI(lowerObjective string) bool {
	return containsAny(lowerObjective, "tui", "ui", "banner", "header", "footer", "top-right", "top right", "window", "screen", "view", "render")
}
