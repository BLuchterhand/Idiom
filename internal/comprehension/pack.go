package comprehension

import (
	"encoding/json"
	"fmt"
	"idiom/internal/util"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	defaultPackLimit       = 8
	maxPackFileHandles     = 12
	maxPackRelationships   = 32
	relationshipsPerTarget = 4
	packAlgorithmVersion   = "query-term-coverage-v5-packet-quality"
)

type PackOptions struct {
	Root          string
	Task          string
	Path          string
	Scope         string
	Mode          string
	Limit         int
	BudgetTokens  int
	RequiredTests []string
	// RequiredEditPaths are caller-declared write targets. They are
	// scope evidence, not an edit instruction: Pack should surface and
	// prioritize them so downstream agents do not rediscover already
	// declared targets.
	RequiredEditPaths []string
	ForbiddenPaths    []string
	Now               time.Time
	// Intent overrides automatic intent detection from Task. Empty means
	// detect from Task.
	Intent Intent
	// LearnedWeights overrides the on-disk persisted weights. Empty means
	// load from .idiom/ranker/weights.json.
	LearnedWeights map[string]float64
	Freshness      *FreshnessDecision
	// IncludeStaleRuntime is a debug/audit escape hatch. Normal packet
	// generation leaves stale runtime traces in risks/gaps instead of
	// evidence because old command output cannot be made fresh by a
	// static graph refresh.
	IncludeStaleRuntime bool
}

type PackResult struct {
	PackID               string                  `json:"pack_id"`
	IndexID              string                  `json:"index_id,omitempty"`
	ArtifactPath         string                  `json:"artifact_path,omitempty"`
	StoreKind            string                  `json:"store_kind,omitempty"`
	Root                 string                  `json:"root"`
	Task                 string                  `json:"task"`
	Mode                 string                  `json:"mode"`
	Fresh                bool                    `json:"fresh"`
	Freshness            *FreshnessDecision      `json:"freshness,omitempty"`
	Confidence           GraphResultConfidence   `json:"confidence"`
	Provenance           GraphResultProvenance   `json:"provenance"`
	StaleEvidence        StaleEvidenceSafety     `json:"stale_evidence"`
	Cache                GraphViewCacheState     `json:"cache"`
	Contract             GraphViewResultContract `json:"contract"`
	GeneratedAt          string                  `json:"generated_at"`
	AutoRefresh          *RefreshReport          `json:"auto_refresh,omitempty"`
	BudgetTokens         int                     `json:"budget_tokens,omitempty"`
	Quality              PackQuality             `json:"quality,omitempty"`
	RequiredEditPaths    []string                `json:"required_edit_paths,omitempty"`
	CandidateFiles       []PackFile              `json:"candidate_files,omitempty"`
	CandidateSymbols     []PackSymbol            `json:"candidate_symbols,omitempty"`
	Tests                []PackTest              `json:"tests,omitempty"`
	Relationships        []EdgeSummary           `json:"relationships,omitempty"`
	Evidence             []PackEvidence          `json:"evidence,omitempty"`
	Risks                []string                `json:"risks,omitempty"`
	Unknowns             []string                `json:"unknowns,omitempty"`
	UnresolvedGaps       []string                `json:"unresolved_gaps,omitempty"`
	UnresolvedGapDetails []GraphUnresolvedGap    `json:"unresolved_gap_details,omitempty"`
}

type PackQuality struct {
	LocalizationConfidence string   `json:"localization_confidence,omitempty"`
	SourceOwnerFound       bool     `json:"source_owner_found"`
	EvidencePaths          []string `json:"evidence_paths,omitempty"`
	Reasons                []string `json:"reasons,omitempty"`
}

type PackFile struct {
	Path           string         `json:"path"`
	Role           string         `json:"role"`
	Reason         string         `json:"reason,omitempty"`
	Language       string         `json:"language,omitempty"`
	Confidence     string         `json:"confidence,omitempty"`
	Stale          bool           `json:"stale,omitempty"`
	Handles        []string       `json:"handles,omitempty"`
	OmittedHandles int            `json:"omitted_handles,omitempty"`
	Score          float64        `json:"score,omitempty"`
	Hub            bool           `json:"hub,omitempty"`
	RankerReasons  []RankerReason `json:"ranker_reasons,omitempty"`
}

type PackSymbol struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Path          string `json:"path,omitempty"`
	Language      string `json:"language,omitempty"`
	QualifiedName string `json:"qualified_name,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	EndLine       int    `json:"end_line,omitempty"`
	Confidence    string `json:"confidence"`
	Stale         bool   `json:"stale,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type PackTest struct {
	Command  string `json:"command,omitempty"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type PackEvidence struct {
	Kind       string `json:"kind"`
	Label      string `json:"label,omitempty"`
	Path       string `json:"path,omitempty"`
	Handle     string `json:"handle,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Content    string `json:"content,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
}

func Pack(opts PackOptions) (PackResult, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return PackResult{}, err
	}
	task := strings.TrimSpace(opts.Task)
	if task == "" {
		return PackResult{}, fmt.Errorf("task is required")
	}
	graph, err := LoadGraph(root)
	if err != nil {
		return PackResult{}, err
	}
	status, _ := Status(root)
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = "plan"
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultPackLimit
	}

	intent := opts.Intent
	if intent == "" {
		intent = DetectIntent(opts.Task)
	}
	workspaceStateFingerprints := packWorkspaceStateFingerprints(root, opts.LearnedWeights)
	cacheKey := viewKeyFromOptions(PackOptions{
		Task:                task,
		Path:                opts.Path,
		Scope:               opts.Scope,
		Limit:               limit,
		BudgetTokens:        opts.BudgetTokens,
		RequiredTests:       opts.RequiredTests,
		RequiredEditPaths:   opts.RequiredEditPaths,
		ForbiddenPaths:      opts.ForbiddenPaths,
		IncludeStaleRuntime: opts.IncludeStaleRuntime,
	}, graph.ID, mode, intent, workspaceStateFingerprints)
	cacheState := GraphViewCacheStateForKey(cacheKey, graph.StoreKind, "miss_recompute", "cache_miss")

	// Try the packet cache even when unrelated workspace paths are stale.
	// LoadView validates the cached row against its referenced paths,
	// source fingerprints, workspace-state fingerprint, and current
	// index_id before returning a hit. That scoped freshness check lets
	// local models reuse task packets instead of paying for re-discovery
	// just because another file elsewhere in the repo changed.
	if cached, hit, err := LoadView(root, cacheKey); err != nil {
		// I/O errors on the cache should not block the pack — drop the error and recompute.
		_ = err
		cacheState = GraphViewCacheStateForKey(cacheKey, graph.StoreKind, "fallback_recompute", "cache_load_error")
	} else if hit {
		cached.Cache = GraphViewCacheStateForKey(cacheKey, util.FirstNonEmpty(cached.StoreKind, graph.StoreKind), "hit", "fresh_cached_row")
		cached.Provenance.Operation = "context_pack"
		AttachPackResultContract(&cached)
		_ = savePack(root, cached)
		return cached, nil
	}

	nodesByID := packNodesByID(graph.Nodes)
	stale := packStalePaths(status)
	forbidden := cleanPackPaths(opts.ForbiddenPaths)
	requiredEditPaths := cleanPackPaths(opts.RequiredEditPaths)
	requiredPathSet := packPathSet(requiredEditPaths)
	terms := queryTerms(QueryOptions{Query: task, Path: opts.Path, Scope: opts.Scope})
	if len(terms) == 0 {
		terms = []string{"."}
	}
	matched := matchNodes(graph.Nodes, terms, opts.Path, "any", limit*4)
	matchedIDs := make(map[string]struct{}, len(matched))
	for _, node := range matched {
		matchedIDs[node.ID] = struct{}{}
	}
	requiredNodes := packNodesForPaths(graph.Nodes, requiredPathSet)
	for _, node := range requiredNodes {
		matchedIDs[node.ID] = struct{}{}
	}
	relationships := relatedEdges(graph.Edges, matchedIDs, packRelationshipLimit(limit))
	fileBuilder := newPackFileBuilder(stale, forbidden)
	symbolBuilder := newPackSymbolBuilder(stale)
	testBuilder := newPackTestBuilder()

	for _, command := range opts.RequiredTests {
		testBuilder.add(PackTest{Command: strings.TrimSpace(command), Required: true, Reason: "required by context_pack input"})
	}
	for _, path := range requiredEditPaths {
		fileBuilder.addPackFile(PackFile{
			Path:       path,
			Role:       "editable",
			Reason:     "required edit path",
			Confidence: "exact_input",
			Stale:      packPathIsStale(stale, path),
		})
	}
	for _, node := range requiredNodes {
		addPackNode(node, true, "required edit path", fileBuilder, symbolBuilder)
	}
	for _, node := range matched {
		addPackNode(node, true, "matched task terms", fileBuilder, symbolBuilder)
		if node.Kind == "package" {
			addPackageContainedFiles(graph, node.ID, nodesByID, fileBuilder, limit)
		}
	}
	for _, edge := range relationships {
		source := nodesByID[edge.SourceID]
		target := nodesByID[edge.TargetID]
		if edge.Kind == "tests" {
			addPackTestFromNode(source, target, testBuilder)
		}
		addPackRelatedNode(source, edge, fileBuilder, symbolBuilder)
		addPackRelatedNode(target, edge, fileBuilder, symbolBuilder)
	}
	for _, file := range fileBuilder.files {
		addPackTestCommandForFile(file, testBuilder)
	}
	runtimeOverlay := runtimeOverlayForPack(root, task, opts.Path, limit, opts.IncludeStaleRuntime)
	for _, file := range runtimeOverlay.Files {
		fileBuilder.addPackFile(file)
	}
	for _, test := range runtimeOverlay.Tests {
		testBuilder.add(test)
	}

	learnedWeights := opts.LearnedWeights
	if learnedWeights == nil {
		loaded, _ := LoadLearnedWeights(root)
		learnedWeights = loaded
	}
	rankInputs := RankInputs{
		Graph:          &graph,
		MatchedIDs:     matchedIDs,
		Intent:         intent,
		LearnedWeights: learnedWeights,
		QueryTokens:    terms,
		RequiredPaths:  requiredPathSet,
	}
	candidateFiles := finalizePackFiles(fileBuilder.all(), rankInputs, limit)
	quality := assessPackQuality(task, candidateFiles, symbolBuilder.list(limit*2), testBuilder.list(limit))

	result := PackResult{
		IndexID:           graph.ID,
		StoreKind:         graph.StoreKind,
		Root:              root,
		Task:              task,
		Mode:              mode,
		Fresh:             status.Fresh,
		Freshness:         opts.Freshness,
		Confidence:        graphResultConfidenceForStatus(status, opts.Freshness),
		Provenance:        graphResultProvenanceFromStatus("context_pack", status, ""),
		StaleEvidence:     StaleEvidenceSafetyForStatus(status, opts.Freshness),
		Cache:             cacheState,
		GeneratedAt:       now.Format(time.RFC3339),
		BudgetTokens:      opts.BudgetTokens,
		Quality:           quality,
		RequiredEditPaths: requiredEditPaths,
		CandidateFiles:    candidateFiles,
		CandidateSymbols:  symbolBuilder.list(limit * 2),
		Tests:             testBuilder.list(limit),
		Relationships:     summarizeEdges(relationships, true),
	}
	if !status.Fresh {
		if gap := GraphGapForStatus(status); gap != "" {
			result.UnresolvedGaps = append(result.UnresolvedGaps, gap)
		}
		if gap, ok := GraphGapDetailForStatus(status); ok {
			result.UnresolvedGapDetails = append(result.UnresolvedGapDetails, gap)
		}
		result.Risks = append(result.Risks, "Stale graph facts may point at outdated symbols or tests.")
	}
	if len(result.CandidateFiles) == 0 {
		result.Unknowns = append(result.Unknowns, "No candidate files matched the task terms in the context graph.")
		result.UnresolvedGaps = append(result.UnresolvedGaps, "reason=no_graph_matches candidate_file_count=0")
		result.UnresolvedGapDetails = append(result.UnresolvedGapDetails, GraphUnresolvedGap{
			Reason:             "no_graph_matches",
			State:              "empty",
			CandidateFileCount: 0,
		})
	}
	if len(result.CandidateSymbols) == 0 {
		result.Unknowns = append(result.Unknowns, "No candidate symbols matched the task terms in the context graph.")
	}
	if len(result.Tests) == 0 {
		result.Unknowns = append(result.Unknowns, "No exact validation command was inferred from the context graph.")
	}
	if result.Quality.LocalizationConfidence == "low" && !result.Quality.SourceOwnerFound {
		result.Unknowns = append(result.Unknowns, "Context packet localized related evidence but did not identify a source-owner file.")
		result.UnresolvedGaps = append(result.UnresolvedGaps, "reason=weak_source_owner localization_confidence=low")
		result.UnresolvedGapDetails = append(result.UnresolvedGapDetails, GraphUnresolvedGap{
			Reason:             "weak_source_owner",
			State:              "low_quality",
			CandidateFileCount: len(result.CandidateFiles),
		})
	}
	for _, path := range forbidden {
		if fileBuilder.omittedForbidden[path] {
			result.Risks = append(result.Risks, fmt.Sprintf("Forbidden path %q was omitted from candidate files.", path))
		}
	}
	result.Evidence = append(result.Evidence, runtimeOverlay.Evidence...)
	result.Risks = append(result.Risks, runtimeOverlay.Risks...)
	result.Unknowns = append(result.Unknowns, runtimeOverlay.Unknowns...)
	result.UnresolvedGaps = append(result.UnresolvedGaps, runtimeOverlay.UnresolvedGaps...)
	result.UnresolvedGapDetails = append(result.UnresolvedGapDetails, runtimeOverlay.UnresolvedGapDetails...)

	result.PackID = cacheKey.ID()
	result.ArtifactPath = filepath.ToSlash(filepath.Join(".idiom", "comprehension", "views", result.PackID+".json"))
	result.Provenance.ArtifactPath = result.ArtifactPath
	result.Cache.PackID = result.PackID
	result.Cache.StoreKind = util.FirstNonEmpty(result.Cache.StoreKind, result.StoreKind)
	result.Evidence = append(result.Evidence, PackEvidence{
		Kind:    "context_view",
		Label:   "context_pack",
		Path:    result.ArtifactPath,
		Handle:  "context_pack://" + result.PackID,
		Content: fmt.Sprintf("%d candidate file(s), %d candidate symbol(s), %d test command(s)", len(result.CandidateFiles), len(result.CandidateSymbols), len(result.Tests)),
		Stale:   !result.Fresh,
	})
	for _, file := range result.CandidateFiles {
		result.Evidence = append(result.Evidence, PackEvidence{
			Kind:       "context_file",
			Label:      file.Role,
			Path:       file.Path,
			Confidence: file.Confidence,
			Content:    file.Reason,
			Stale:      file.Stale,
		})
	}
	for _, symbol := range result.CandidateSymbols {
		result.Evidence = append(result.Evidence, PackEvidence{
			Kind:       "context_symbol",
			Label:      symbol.QualifiedName,
			Path:       symbol.Path,
			Handle:     symbol.ID,
			Confidence: symbol.Confidence,
			Content:    symbol.Reason,
			Stale:      symbol.Stale,
		})
	}
	AttachPackResultContract(&result)
	if err := SaveView(root, cacheKey, result); err != nil {
		return PackResult{}, fmt.Errorf("save graph_views row: %w", err)
	}
	if err := savePack(root, result); err != nil {
		return PackResult{}, err
	}
	return result, nil
}

type packFileBuilder struct {
	files            map[string]PackFile
	stale            map[string]struct{}
	forbidden        []string
	omittedForbidden map[string]bool
}

func newPackFileBuilder(stale map[string]struct{}, forbidden []string) *packFileBuilder {
	return &packFileBuilder{
		files:            make(map[string]PackFile),
		stale:            stale,
		forbidden:        forbidden,
		omittedForbidden: make(map[string]bool),
	}
}

func (b *packFileBuilder) add(node Node, role string, reason string, handle string) {
	if node.Path == "" {
		return
	}
	for _, forbidden := range b.forbidden {
		if pathInScope(node.Path, forbidden) {
			b.omittedForbidden[forbidden] = true
			return
		}
	}
	existing := b.files[node.Path]
	if existing.Path == "" || packRoleRank(role) > packRoleRank(existing.Role) {
		existing.Path = node.Path
		existing.Role = role
		existing.Language = node.Language
		existing.Confidence = node.Confidence
	}
	if existing.Reason == "" {
		existing.Reason = reason
	}
	if _, ok := b.stale[node.Path]; ok {
		existing.Stale = true
	}
	if handle != "" && !slices.Contains(existing.Handles, handle) {
		existing.Handles = append(existing.Handles, handle)
		slices.Sort(existing.Handles)
	}
	b.files[node.Path] = existing
}

func (b *packFileBuilder) addPackFile(file PackFile) {
	if file.Path == "" {
		return
	}
	for _, forbidden := range b.forbidden {
		if pathInScope(file.Path, forbidden) {
			b.omittedForbidden[forbidden] = true
			return
		}
	}
	existing := b.files[file.Path]
	if existing.Path == "" || packRoleRank(file.Role) > packRoleRank(existing.Role) {
		existing.Path = file.Path
		existing.Role = file.Role
		existing.Language = file.Language
		existing.Confidence = file.Confidence
	}
	if existing.Reason == "" {
		existing.Reason = file.Reason
	}
	existing.Stale = existing.Stale || file.Stale
	for _, handle := range file.Handles {
		if handle != "" && !slices.Contains(existing.Handles, handle) {
			existing.Handles = append(existing.Handles, handle)
		}
	}
	slices.Sort(existing.Handles)
	b.files[file.Path] = existing
}

// all returns every accumulated candidate file in role-stable order, before
// any score-based ranking, limit, or handle bounding is applied.
func (b *packFileBuilder) all() []PackFile {
	out := make([]PackFile, 0, len(b.files))
	for _, file := range b.files {
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool {
		if packRoleRank(out[i].Role) != packRoleRank(out[j].Role) {
			return packRoleRank(out[i].Role) > packRoleRank(out[j].Role)
		}
		if out[i].Stale != out[j].Stale {
			return !out[i].Stale
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// finalizePackFiles applies score-based ranking via RankPackFiles, then the
// candidate limit and per-file handle bounding. The Hub flag is set inside
// RankPackFiles so callers that bypass finalizePackFiles still see it.
func finalizePackFiles(files []PackFile, in RankInputs, limit int) []PackFile {
	ranked := RankPackFiles(RankInputs{
		Files:          files,
		Graph:          in.Graph,
		MatchedIDs:     in.MatchedIDs,
		Intent:         in.Intent,
		LearnedWeights: in.LearnedWeights,
		QueryTokens:    in.QueryTokens,
		RequiredPaths:  in.RequiredPaths,
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	for i := range ranked {
		ranked[i] = boundedPackFileHandles(ranked[i])
	}
	return ranked
}

type packQualityCandidate struct {
	path   string
	text   string
	source bool
	score  int
}

func assessPackQuality(task string, files []PackFile, symbols []PackSymbol, tests []PackTest) PackQuality {
	tokens := packQualityTokens(task)
	if len(tokens) == 0 {
		return PackQuality{LocalizationConfidence: "unknown", Reasons: []string{"empty_query_tokens"}}
	}
	candidates := packQualityCandidates(files, symbols, tests)
	for i := range candidates {
		candidates[i].score = packQualityOverlap(tokens, candidates[i].text)
	}
	sourceOwnerFound := false
	bestScore := 0
	bestSourceScore := 0
	for _, candidate := range candidates {
		if candidate.score <= 0 {
			continue
		}
		if candidate.score > bestScore {
			bestScore = candidate.score
		}
		if candidate.source && candidate.score > bestSourceScore {
			bestSourceScore = candidate.score
		}
		if candidate.source && candidate.score >= 3 {
			sourceOwnerFound = true
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if sourceOwnerFound && candidates[i].source != candidates[j].source {
			return candidates[i].source
		}
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].source != candidates[j].source {
			return candidates[i].source
		}
		return candidates[i].path < candidates[j].path
	})
	evidencePaths := make([]string, 0, 6)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.path == "" || candidate.score <= 0 {
			continue
		}
		if !sourceOwnerFound && candidate.source {
			continue
		}
		if _, ok := seen[candidate.path]; !ok && len(evidencePaths) < 6 {
			seen[candidate.path] = struct{}{}
			evidencePaths = append(evidencePaths, candidate.path)
		}
	}
	confidence := "low"
	reasons := []string{}
	switch {
	case sourceOwnerFound && bestSourceScore >= 3:
		confidence = "high"
		reasons = append(reasons, "source_owner_token_overlap_high")
	case sourceOwnerFound:
		confidence = "medium"
		reasons = append(reasons, "source_owner_token_overlap")
	case bestScore > 0:
		reasons = append(reasons, "related_evidence_without_source_owner")
	default:
		reasons = append(reasons, "no_query_token_overlap_in_packet")
	}
	return PackQuality{
		LocalizationConfidence: confidence,
		SourceOwnerFound:       sourceOwnerFound,
		EvidencePaths:          evidencePaths,
		Reasons:                reasons,
	}
}

func packQualityCandidates(files []PackFile, symbols []PackSymbol, tests []PackTest) []packQualityCandidate {
	out := make([]packQualityCandidate, 0, len(files)+len(symbols)+len(tests))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		out = append(out, packQualityCandidate{
			path:   path,
			text:   strings.Join(append([]string{path, file.Role, file.Reason}, file.Handles...), " "),
			source: !packQualityPathIsTest(path),
		})
	}
	for _, symbol := range symbols {
		path := strings.TrimSpace(symbol.Path)
		if path == "" {
			continue
		}
		out = append(out, packQualityCandidate{
			path:   path,
			text:   strings.Join([]string{path, symbol.Kind, symbol.QualifiedName, symbol.Reason}, " "),
			source: !packQualityPathIsTest(path) && symbol.Kind != "test",
		})
	}
	for _, test := range tests {
		path := strings.TrimSpace(test.Path)
		if path == "" {
			continue
		}
		out = append(out, packQualityCandidate{
			path: path,
			text: strings.Join([]string{path, test.Symbol, test.Command, test.Reason}, " "),
		})
	}
	return out
}

func packQualityTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range splitPackQualityText(text) {
		if len(token) < 3 || packQualityStopToken(token) {
			continue
		}
		out[packQualityStem(token)] = struct{}{}
	}
	return out
}

func packQualityOverlap(tokens map[string]struct{}, text string) int {
	seen := map[string]struct{}{}
	for _, token := range splitPackQualityText(text) {
		stem := packQualityStem(token)
		if _, ok := tokens[stem]; ok {
			seen[stem] = struct{}{}
		}
	}
	return len(seen)
}

func splitPackQualityText(text string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(b.String()))
		b.Reset()
	}
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			if b.Len() > 0 {
				flush()
			}
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

func packQualityStem(token string) string {
	token = strings.TrimSpace(strings.ToLower(token))
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+3 && strings.HasSuffix(token, suffix) {
			return packQualityTrimDoubledFinalConsonant(strings.TrimSuffix(token, suffix))
		}
	}
	return token
}

func packQualityTrimDoubledFinalConsonant(token string) string {
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

func packQualityStopToken(token string) bool {
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

func packQualityPathIsTest(path string) bool {
	path = filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	return strings.HasSuffix(path, "_test.go") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/fixtures/") ||
		strings.Contains(path, "/tests/")
}

func boundedPackFileHandles(file PackFile) PackFile {
	if len(file.Handles) <= maxPackFileHandles {
		return file
	}
	file.OmittedHandles = len(file.Handles) - maxPackFileHandles
	file.Handles = append([]string(nil), file.Handles[:maxPackFileHandles]...)
	return file
}

func packRelationshipLimit(limit int) int {
	if limit <= 0 {
		return maxPackRelationships
	}
	relationshipLimit := limit * relationshipsPerTarget
	if relationshipLimit > maxPackRelationships {
		return maxPackRelationships
	}
	if relationshipLimit < limit {
		return limit
	}
	return relationshipLimit
}

func packWorkspaceStateFingerprints(root string, learnedWeights map[string]float64) ComprehensionStateFingerprintSet {
	state := ComprehensionStateFingerprints(root)
	if learnedWeights == nil {
		return state
	}
	data, _ := json.Marshal(learnedWeights)
	state.RankerWeightsFingerprint = shortSHA256Hex(append([]byte("learned_weights_override\x00"), data...))
	state.WorkspaceStateFingerprint = composeWorkspaceStateFingerprint(state.RankerWeightsFingerprint, state.RuntimeTracesFingerprint)
	return state
}

type packSymbolBuilder struct {
	symbols map[string]PackSymbol
	stale   map[string]struct{}
}

func newPackSymbolBuilder(stale map[string]struct{}) *packSymbolBuilder {
	return &packSymbolBuilder{symbols: make(map[string]PackSymbol), stale: stale}
}

func (b *packSymbolBuilder) add(node Node, reason string) {
	if !isSymbolKind(node.Kind) || strings.HasPrefix(node.ID, "external_symbol://") {
		return
	}
	symbol := PackSymbol{
		ID:            node.ID,
		Kind:          node.Kind,
		Path:          node.Path,
		Language:      node.Language,
		QualifiedName: node.QualifiedName,
		StartLine:     node.StartLine,
		EndLine:       node.EndLine,
		Confidence:    node.Confidence,
		Reason:        reason,
	}
	if _, ok := b.stale[node.Path]; ok {
		symbol.Stale = true
	}
	if existing := b.symbols[node.ID]; existing.ID != "" && packReasonRank(existing.Reason) >= packReasonRank(reason) {
		return
	}
	b.symbols[node.ID] = symbol
}

func (b *packSymbolBuilder) list(limit int) []PackSymbol {
	out := make([]PackSymbol, 0, len(b.symbols))
	for _, symbol := range b.symbols {
		out = append(out, symbol)
	}
	sort.Slice(out, func(i, j int) bool {
		if packReasonRank(out[i].Reason) != packReasonRank(out[j].Reason) {
			return packReasonRank(out[i].Reason) > packReasonRank(out[j].Reason)
		}
		if out[i].Stale != out[j].Stale {
			return !out[i].Stale
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].QualifiedName < out[j].QualifiedName
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

type packTestBuilder struct {
	tests map[string]PackTest
}

func newPackTestBuilder() *packTestBuilder {
	return &packTestBuilder{tests: make(map[string]PackTest)}
}

func (b *packTestBuilder) add(test PackTest) {
	test.Command = strings.TrimSpace(test.Command)
	test.Path = strings.TrimSpace(test.Path)
	test.Symbol = strings.TrimSpace(test.Symbol)
	test.Reason = strings.TrimSpace(test.Reason)
	if test.Command == "" && test.Path == "" && test.Symbol == "" {
		return
	}
	key := util.FirstNonEmpty(test.Command, test.Path+"\x00"+test.Symbol)
	if existing := b.tests[key]; existing.Command != "" || existing.Path != "" || existing.Symbol != "" {
		if !existing.Required && test.Required {
			existing.Required = true
			b.tests[key] = existing
		}
		return
	}
	b.tests[key] = test
}

func (b *packTestBuilder) list(limit int) []PackTest {
	out := make([]PackTest, 0, len(b.tests))
	for _, test := range b.tests {
		out = append(out, test)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		if out[i].Command != out[j].Command {
			return out[i].Command < out[j].Command
		}
		return out[i].Path < out[j].Path
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func addPackNode(node Node, direct bool, reason string, files *packFileBuilder, symbols *packSymbolBuilder) {
	if node.ID == "" {
		return
	}
	role := "read_only_evidence"
	if direct {
		role = "editable"
	}
	if node.Kind == "test" || strings.HasSuffix(node.Path, "_test.go") {
		role = "test"
	}
	if node.Kind == "file" || isSymbolKind(node.Kind) {
		files.add(node, role, reason, node.ID)
	}
	if isSymbolKind(node.Kind) {
		symbols.add(node, reason)
	}
}

func addPackRelatedNode(node Node, edge Edge, files *packFileBuilder, symbols *packSymbolBuilder) {
	if node.ID == "" || strings.HasPrefix(node.ID, "external://") || strings.HasPrefix(node.ID, "external_symbol://") {
		return
	}
	reason := "related by " + edge.Kind + " relationship"
	addPackNode(node, false, reason, files, symbols)
}

func addPackageContainedFiles(graph Graph, packageID string, nodesByID map[string]Node, files *packFileBuilder, limit int) {
	added := 0
	for _, edge := range graph.Edges {
		if edge.Kind != "contains" || edge.SourceID != packageID {
			continue
		}
		node := nodesByID[edge.TargetID]
		if node.Kind != "file" {
			continue
		}
		files.add(node, "editable", "package matched task terms", node.ID)
		added++
		if limit > 0 && added >= limit {
			return
		}
	}
}

func addPackTestFromNode(source Node, target Node, tests *packTestBuilder) {
	if source.Kind != "test" {
		return
	}
	test := PackTest{
		Path:   source.Path,
		Symbol: source.QualifiedName,
		Reason: "context graph test edge covers " + util.FirstNonEmpty(target.QualifiedName, target.Path, target.ID),
	}
	if source.Language == "go" {
		test.Command = goTestCommandForPath(source.Path)
	}
	tests.add(test)
}

func addPackTestCommandForFile(file PackFile, tests *packTestBuilder) {
	if file.Language != "go" {
		return
	}
	command := goTestCommandForPath(file.Path)
	if command == "" {
		return
	}
	tests.add(PackTest{Command: command, Path: file.Path, Reason: "inferred from Go candidate file"})
}

func goTestCommandForPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	dir := pathDir(path)
	if dir == "." || dir == "" {
		return "go test ."
	}
	return "go test ./" + dir
}

func packNodesByID(nodes []Node) map[string]Node {
	out := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		out[node.ID] = node
	}
	return out
}

func packStalePaths(status StatusReport) map[string]struct{} {
	out := make(map[string]struct{}, len(status.StaleFiles)+len(status.MissingFiles))
	for _, path := range status.StaleFiles {
		out[path] = struct{}{}
	}
	for _, path := range status.MissingFiles {
		out[path] = struct{}{}
	}
	return out
}

func cleanPackPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = cleanScope(path)
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func packPathSet(paths []string) map[string]struct{} {
	if len(paths) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = cleanScope(path)
		if path == "" || path == "." {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func packNodesForPaths(nodes []Node, paths map[string]struct{}) []Node {
	if len(paths) == 0 {
		return nil
	}
	out := make([]Node, 0)
	for _, node := range nodes {
		path := cleanScope(node.Path)
		if _, ok := paths[path]; ok {
			out = append(out, node)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func packPathIsStale(stale map[string]struct{}, path string) bool {
	if len(stale) == 0 {
		return false
	}
	_, ok := stale[cleanScope(path)]
	return ok
}

func packRoleRank(role string) int {
	switch role {
	case "editable":
		return 3
	case "test":
		return 2
	case "read_only_evidence":
		return 1
	default:
		return 0
	}
}

func packReasonRank(reason string) int {
	switch reason {
	case "required edit path":
		return 4
	case "matched task terms":
		return 3
	case "package matched task terms":
		return 2
	default:
		return 1
	}
}

// viewKeyFromOptions builds the content-addressed cache key from
// PackOptions. Identical option sets produce identical keys across
// processes; the active graph index_id makes the key invalidate when
// the codebase content shifts under the harness.
func viewKeyFromOptions(opts PackOptions, indexID, mode string, intent Intent, workspaceStateFingerprints ComprehensionStateFingerprintSet) ViewKey {
	return ViewKey{
		IndexID: indexID,
		Task:    strings.TrimSpace(opts.Task),
		Mode:    mode,
		Query: ViewQueryInputs{
			Path:                      strings.TrimSpace(opts.Path),
			Scope:                     strings.TrimSpace(opts.Scope),
			Limit:                     opts.Limit,
			BudgetTokens:              opts.BudgetTokens,
			RequiredTests:             opts.RequiredTests,
			RequiredEditPaths:         opts.RequiredEditPaths,
			ForbiddenPaths:            opts.ForbiddenPaths,
			Intent:                    string(intent),
			IncludeStaleRuntime:       opts.IncludeStaleRuntime,
			AlgorithmVersion:          packAlgorithmVersion,
			WorkspaceStateFingerprint: workspaceStateFingerprints.WorkspaceStateFingerprint,
			RankerWeightsFingerprint:  workspaceStateFingerprints.RankerWeightsFingerprint,
			RuntimeTracesFingerprint:  workspaceStateFingerprints.RuntimeTracesFingerprint,
		},
	}
}

// savePack writes the human-readable JSON export under
// .idiom/comprehension/views/<pack_id>.json. The SQLite graph_views
// row is the source of truth; this file mirrors it for inspection.
func savePack(root string, result PackResult) error {
	path := filepath.Join(root, ".idiom", "comprehension", "views", result.PackID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
