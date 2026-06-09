package comprehension

import (
	"fmt"
	"idiom/internal/platform/util"
	"path/filepath"
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
	// CompactProjection is the budget-enforced model-facing projection
	// produced by RenderCompactPackProjection. It carries omitted-item
	// counts reflecting the budget cap applied at pack creation time.
	// Zero-value when budget is unbounded or when the pack was created
	// before Atom 5 budget enforcement wiring.
	CompactProjection CompactPackProjection `json:"compact_projection,omitempty"`
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
	ctx, err := preparePackContext(opts)
	if err != nil {
		return PackResult{}, err
	}
	if cached, hit := loadCachedPack(ctx); hit {
		return cached, nil
	}

	candidates := collectPackCandidates(ctx)
	result := buildPackResult(ctx, candidates)
	// Compute the budget-enforced compact projection while the full
	// PackResult is still in hand. Store it on the result so all
	// downstream consumers — direct tool output, plan-execute preload,
	// executor handoff, phase evidence, sidecar, telemetry, and audit —
	// share one canonical projection with consistent omitted-item counts.
	proj := NewCompactPackProjection(result)
	_, compactProj := RenderCompactPackProjection(proj, result.BudgetTokens)
	result.CompactProjection = compactProj
	addPackResultGaps(ctx, candidates, &result)
	addPackResultEvidence(&result)

	AttachPackResultContract(&result)
	if err := SaveView(ctx.root, ctx.cacheKey, result); err != nil {
		return PackResult{}, fmt.Errorf("save graph_views row: %w", err)
	}
	if err := savePack(ctx.root, result); err != nil {
		return PackResult{}, err
	}
	return result, nil
}

type packContext struct {
	opts PackOptions

	root   string
	task   string
	mode   string
	now    time.Time
	limit  int
	intent Intent

	graph      Graph
	status     StatusReport
	cacheKey   ViewKey
	cacheState GraphViewCacheState

	nodesByID         map[string]Node
	stale             map[string]struct{}
	forbidden         []string
	requiredEditPaths []string
	requiredPathSet   map[string]struct{}
	terms             []string
	matched           []Node
	matchedIDs        map[string]struct{}
	requiredNodes     []Node
	relationships     []Edge
}

type packCandidates struct {
	files          *packFileBuilder
	symbols        *packSymbolBuilder
	tests          *packTestBuilder
	candidateFiles []PackFile
	runtimeOverlay runtimePackOverlay
}

func preparePackContext(opts PackOptions) (*packContext, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	task := strings.TrimSpace(opts.Task)
	if task == "" {
		return nil, fmt.Errorf("task is required")
	}
	graph, err := LoadGraph(root)
	if err != nil {
		return nil, err
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

	return &packContext{
		opts:       opts,
		root:       root,
		task:       task,
		mode:       mode,
		now:        now,
		limit:      limit,
		intent:     intent,
		graph:      graph,
		status:     status,
		cacheKey:   cacheKey,
		cacheState: cacheState,
	}, nil
}

func loadCachedPack(ctx *packContext) (PackResult, bool) {
	// Try the packet cache even when unrelated workspace paths are stale.
	// LoadView validates the cached row against its referenced paths,
	// source fingerprints, workspace-state fingerprint, and current
	// index_id before returning a hit. That scoped freshness check lets
	// local models reuse task packets instead of paying for re-discovery
	// just because another file elsewhere in the repo changed.
	if cached, hit, err := LoadView(ctx.root, ctx.cacheKey); err != nil {
		// I/O errors on the cache should not block the pack — drop the error and recompute.
		_ = err
		ctx.cacheState = GraphViewCacheStateForKey(ctx.cacheKey, ctx.graph.StoreKind, "fallback_recompute", "cache_load_error")
	} else if hit {
		cached.Cache = GraphViewCacheStateForKey(ctx.cacheKey, util.FirstNonEmpty(cached.StoreKind, ctx.graph.StoreKind), "hit", "fresh_cached_row")
		cached.Provenance.Operation = "context_pack"
		// Recompute the compact projection if the cached pack was
		// created before Atom 5 budget enforcement wiring.
		if cached.CompactProjection.PackID == "" {
			cp := NewCompactPackProjection(cached)
			_, compactProj := RenderCompactPackProjection(cp, cached.BudgetTokens)
			cached.CompactProjection = compactProj
		}
		AttachPackResultContract(&cached)
		_ = savePack(ctx.root, cached)
		return cached, true
	}
	return PackResult{}, false
}

func collectPackCandidates(ctx *packContext) packCandidates {
	preparePackDiscovery(ctx)

	files := newPackFileBuilder(ctx.stale, ctx.forbidden)
	symbols := newPackSymbolBuilder(ctx.stale)
	tests := newPackTestBuilder()

	addRequiredPackInputs(ctx, files, symbols, tests)
	addMatchedPackNodes(ctx, files, symbols)
	addPackRelationshipEvidence(ctx, files, symbols, tests)
	addInferredPackTests(files, tests)
	runtimeOverlay := addRuntimeOverlayToPack(ctx, files, tests)

	rankInputs := packRankInputs(ctx)
	candidateFiles := finalizePackFiles(files.all(), rankInputs, ctx.limit)

	return packCandidates{
		files:          files,
		symbols:        symbols,
		tests:          tests,
		candidateFiles: candidateFiles,
		runtimeOverlay: runtimeOverlay,
	}
}

func preparePackDiscovery(ctx *packContext) {
	ctx.nodesByID = packNodesByID(ctx.graph.Nodes)
	ctx.stale = packStalePaths(ctx.status)
	ctx.forbidden = cleanPackPaths(ctx.opts.ForbiddenPaths)
	ctx.requiredEditPaths = cleanPackPaths(ctx.opts.RequiredEditPaths)
	ctx.requiredPathSet = packPathSet(ctx.requiredEditPaths)
	ctx.terms = queryTerms(QueryOptions{Query: ctx.task, Path: ctx.opts.Path, Scope: ctx.opts.Scope})
	if len(ctx.terms) == 0 {
		ctx.terms = []string{"."}
	}

	ctx.matched = matchNodes(ctx.graph.Nodes, ctx.terms, ctx.opts.Path, "any", ctx.limit*4)
	ctx.matchedIDs = make(map[string]struct{}, len(ctx.matched))
	for _, node := range ctx.matched {
		ctx.matchedIDs[node.ID] = struct{}{}
	}

	ctx.requiredNodes = packNodesForPaths(ctx.graph.Nodes, ctx.requiredPathSet)
	for _, node := range ctx.requiredNodes {
		ctx.matchedIDs[node.ID] = struct{}{}
	}
	ctx.relationships = relatedEdges(ctx.graph.Edges, ctx.matchedIDs, packRelationshipLimit(ctx.limit))
}

func addRequiredPackInputs(ctx *packContext, files *packFileBuilder, symbols *packSymbolBuilder, tests *packTestBuilder) {
	for _, command := range ctx.opts.RequiredTests {
		tests.add(PackTest{Command: strings.TrimSpace(command), Required: true, Reason: "required by context_pack input"})
	}

	for _, path := range ctx.requiredEditPaths {
		files.addPackFile(PackFile{
			Path:       path,
			Role:       "editable",
			Reason:     "required edit path",
			Confidence: "exact_input",
			Stale:      packPathIsStale(ctx.stale, path),
		})
	}

	for _, node := range ctx.requiredNodes {
		addPackNode(node, true, "required edit path", files, symbols)
	}
}

func addMatchedPackNodes(ctx *packContext, files *packFileBuilder, symbols *packSymbolBuilder) {
	for _, node := range ctx.matched {
		addPackNode(node, true, "matched task terms", files, symbols)
		if node.Kind == "package" {
			addPackageContainedFiles(ctx.graph, node.ID, ctx.nodesByID, files, ctx.limit)
		}
	}
}

func addPackRelationshipEvidence(ctx *packContext, files *packFileBuilder, symbols *packSymbolBuilder, tests *packTestBuilder) {
	for _, edge := range ctx.relationships {
		source := ctx.nodesByID[edge.SourceID]
		target := ctx.nodesByID[edge.TargetID]
		if edge.Kind == "tests" {
			addPackTestFromNode(source, target, tests)
		}
		addPackRelatedNode(source, edge, files, symbols)
		addPackRelatedNode(target, edge, files, symbols)
	}
}

func addInferredPackTests(files *packFileBuilder, tests *packTestBuilder) {
	for _, file := range files.files {
		addPackTestCommandForFile(file, tests)
	}
}

func addRuntimeOverlayToPack(ctx *packContext, files *packFileBuilder, tests *packTestBuilder) runtimePackOverlay {
	runtimeOverlay := runtimeOverlayForPack(ctx.root, ctx.task, ctx.opts.Path, ctx.limit, ctx.opts.IncludeStaleRuntime)
	for _, file := range runtimeOverlay.Files {
		files.addPackFile(file)
	}
	for _, test := range runtimeOverlay.Tests {
		tests.add(test)
	}
	return runtimeOverlay
}

func packRankInputs(ctx *packContext) RankInputs {
	learnedWeights := ctx.opts.LearnedWeights
	if learnedWeights == nil {
		loaded, _ := LoadLearnedWeights(ctx.root)
		learnedWeights = loaded
	}
	return RankInputs{
		Graph:          &ctx.graph,
		MatchedIDs:     ctx.matchedIDs,
		Intent:         ctx.intent,
		LearnedWeights: learnedWeights,
		QueryTokens:    ctx.terms,
		RequiredPaths:  ctx.requiredPathSet,
	}
}

func buildPackResult(ctx *packContext, candidates packCandidates) PackResult {
	candidateSymbols := candidates.symbols.list(ctx.limit * 2)
	tests := candidates.tests.list(ctx.limit)
	quality := assessPackQuality(ctx.task, candidates.candidateFiles, candidateSymbols, tests)
	result := PackResult{
		IndexID:           ctx.graph.ID,
		StoreKind:         ctx.graph.StoreKind,
		Root:              ctx.root,
		Task:              ctx.task,
		Mode:              ctx.mode,
		Fresh:             ctx.status.Fresh,
		Freshness:         ctx.opts.Freshness,
		Confidence:        graphResultConfidenceForStatus(ctx.status, ctx.opts.Freshness),
		Provenance:        graphResultProvenanceFromStatus("context_pack", ctx.status, ""),
		StaleEvidence:     StaleEvidenceSafetyForStatus(ctx.status, ctx.opts.Freshness),
		Cache:             ctx.cacheState,
		GeneratedAt:       ctx.now.Format(time.RFC3339),
		BudgetTokens:      ctx.opts.BudgetTokens,
		Quality:           quality,
		RequiredEditPaths: ctx.requiredEditPaths,
		CandidateFiles:    candidates.candidateFiles,
		CandidateSymbols:  candidateSymbols,
		Tests:             tests,
		Relationships:     summarizeEdges(ctx.relationships, true),
	}
	result.PackID = ctx.cacheKey.ID()
	result.ArtifactPath = filepath.ToSlash(filepath.Join(".idiom", "comprehension", "views", result.PackID+".json"))
	result.Provenance.ArtifactPath = result.ArtifactPath
	result.Cache.PackID = result.PackID
	result.Cache.StoreKind = util.FirstNonEmpty(result.Cache.StoreKind, result.StoreKind)
	return result
}

func addPackResultGaps(ctx *packContext, candidates packCandidates, result *PackResult) {
	if !ctx.status.Fresh {
		if gap := GraphGapForStatus(ctx.status); gap != "" {
			result.UnresolvedGaps = append(result.UnresolvedGaps, gap)
		}
		if gap, ok := GraphGapDetailForStatus(ctx.status); ok {
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
	for _, path := range ctx.forbidden {
		if candidates.files.omittedForbidden[path] {
			result.Risks = append(result.Risks, fmt.Sprintf("Forbidden path %q was omitted from candidate files.", path))
		}
	}

	result.Evidence = append(result.Evidence, candidates.runtimeOverlay.Evidence...)
	result.Risks = append(result.Risks, candidates.runtimeOverlay.Risks...)
	result.Unknowns = append(result.Unknowns, candidates.runtimeOverlay.Unknowns...)
	result.UnresolvedGaps = append(result.UnresolvedGaps, candidates.runtimeOverlay.UnresolvedGaps...)
	result.UnresolvedGapDetails = append(result.UnresolvedGapDetails, candidates.runtimeOverlay.UnresolvedGapDetails...)
}

func addPackResultEvidence(result *PackResult) {
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
}
