package comprehension

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/util"
	"idiom/internal/repository/discovery"
	"idiom/internal/repository/semantic"
)

func Build(ctx context.Context, opts BuildOptions) (BuildReport, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return BuildReport{}, err
	}

	unlock, err := lockGraphBuild(ctx, root)
	if err != nil {
		return BuildReport{}, err
	}
	defer unlock()

	statusBefore, _ := Status(root)
	now := opts.Now

	if now.IsZero() {
		now = time.Now().UTC()
	}

	gitHead, gitDirty := gitState(ctx, root)

	scope := cleanScope(opts.Path)
	scopedBuild := strings.TrimSpace(opts.Path) != "" && scope != "."
	fullRebuildReason := ""
	partialRefreshReason := ""
	language := normalizeLanguage(opts.Language)

	if language == "" {
		language = "auto"
	}

	var goIndex *semantic.GoIndex

	repoProps := map[string]any{}

	var previousGraph Graph

	previousGraphLoaded := false
	incremental := false
	expandedScope := ""
	preserveCollectorMismatch := false

	var dependencyRefreshFiles []string

	activeKind := activeGraphStoreKind(root, "")
	patchActiveStore := opts.PatchActiveStore && scopedBuild && statusBefore.Indexed && activeKind == sqliteStoreKind

	if patchActiveStore && statusBefore.Indexed && !opts.ExactScope {
		if statusBefore.CollectorVersionMismatch {
			patchActiveStore = false
		} else if paths, err := dependencyRefreshPathsFromSQLite(root, scope); err == nil {
			dependencyRefreshFiles = paths
			if nextScope := commonRefreshScope(scope, dependencyRefreshFiles); nextScope != scope {
				scope = nextScope
				expandedScope = nextScope

				if scope == "." {
					scopedBuild = false
					patchActiveStore = false
					fullRebuildReason = "dependency_scope_expanded_to_root"
				}
			}
		} else {
			patchActiveStore = false
		}
	}

	if statusBefore.Indexed && !patchActiveStore {
		if graph, err := LoadGraph(root); err == nil {
			previousGraph = graph
			previousGraphLoaded = true
		}
	}

	if scopedBuild && previousGraphLoaded {
		incremental = true

		if opts.ExactScope {
			partialRefreshReason = "exact_scope_refresh"
		} else if statusBefore.CollectorVersionMismatch {
			preserveCollectorMismatch = true
			partialRefreshReason = "collector_version_mismatch_preserved_for_scoped_refresh"
		} else {
			dependencyRefreshFiles = dependencyRefreshPaths(previousGraph, scope)
			if nextScope := commonRefreshScope(scope, dependencyRefreshFiles); nextScope != scope {
				scope = nextScope
				expandedScope = nextScope

				if scope == "." {
					incremental = false
					fullRebuildReason = "dependency_scope_expanded_to_root"
				}
			}
		}
	}

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	indexOpts := semantic.IndexOptions{Scope: scope, MaxFiles: opts.MaxFiles, Context: ctx}
	if shouldUseGoCollector(language) {
		goIndex, err = semantic.BuildGoIndexWithOptions(root, indexOpts)
		if err != nil {
			return BuildReport{}, fmt.Errorf("build go index: %w", err)
		}

		if goIndex != nil && goIndex.Module != "" {
			repoProps["module"] = goIndex.Module
		}

		if language == "go" && goIndex == nil && (!patchActiveStore || !scopePathMissing(root, scope)) {
			return BuildReport{}, fmt.Errorf("no Go files found under %s", root)
		}
	}

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	var cppIndex *semantic.CppIndex

	if shouldUseCppCollector(language) {
		cppIndex, err = semantic.BuildCppIndexWithOptions(root, indexOpts)
		if err != nil {
			return BuildReport{}, fmt.Errorf("build C++ index: %w", err)
		}

		if language == "cpp" && cppIndex == nil && (!patchActiveStore || !scopePathMissing(root, scope)) {
			return BuildReport{}, fmt.Errorf("no C/C++ files found under %s", root)
		}
	}

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	allowMissingPatchScope := patchActiveStore && scopePathMissing(root, scope)

	nativeIndexes, err := buildNativeLanguageIndexes(ctx, root, language, scope, opts.MaxFiles, allowMissingPatchScope)
	if err != nil {
		return BuildReport{}, err
	}

	collectorVersion := currentCollectorVersion()
	collectorVersions := currentCollectorVersionMap()

	if preserveCollectorMismatch {
		collectorVersion = previousGraph.CollectorVersion
		collectorVersions = previousGraph.CollectorVersions
	}

	graph := Graph{
		SchemaVersion:     SchemaVersion,
		Root:              root,
		GeneratedAt:       now.Format(time.RFC3339),
		GitHead:           gitHead,
		GitDirty:          gitDirty,
		StoreKind:         storeKind,
		CollectorVersion:  collectorVersion,
		CollectorVersions: collectorVersions,
	}
	builder := graphBuilder{
		root:    root,
		now:     graph.GeneratedAt,
		gitHead: gitHead,
		nodes:   make(map[string]Node),
		edges:   make(map[string]Edge),
	}
	repoID := "repo://" + rootFingerprint(root)
	builder.addNode(Node{
		ID:         repoID,
		Kind:       "repo",
		Path:       ".",
		Collector:  "context_index",
		Confidence: "exact",
		Properties: repoProps,
	})

	filesIndexed := 0
	symbolsIndexed := 0
	indexedFiles := make(map[string]struct{})
	languagesSeen := make(map[string]struct{})
	collectors := map[string]struct{}{
		"file_fingerprint": {},
		"git_state":        {},
	}

	if goIndex != nil {
		addedFiles, addedSymbols := builder.addGoIndex(root, repoID, scope, goIndex, opts.MaxFiles, indexedFiles)

		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols

		if addedFiles > 0 {
			languagesSeen["go"] = struct{}{}
			collectors["go_index"] = struct{}{}
		}
	}

	if cppIndex != nil && (opts.MaxFiles <= 0 || filesIndexed < opts.MaxFiles) {
		remaining := 0
		if opts.MaxFiles > 0 {
			remaining = opts.MaxFiles - filesIndexed
		}

		addedFiles, addedSymbols := builder.addCppIndex(root, repoID, scope, cppIndex, remaining, indexedFiles)

		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols

		if addedFiles > 0 {
			languagesSeen["cpp"] = struct{}{}
			collectors["cpp_index"] = struct{}{}
		}
	}

	if opts.MaxFiles <= 0 || filesIndexed < opts.MaxFiles {
		remaining := 0
		if opts.MaxFiles > 0 {
			remaining = opts.MaxFiles - filesIndexed
		}

		addedFiles, addedSymbols, addedLanguages, addedCollectors := nativeIndexes.addToGraph(root, repoID, scope, &builder, remaining, indexedFiles)

		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols

		for _, language := range addedLanguages {
			languagesSeen[language] = struct{}{}
		}

		for _, collector := range addedCollectors {
			collectors[collector] = struct{}{}
		}
	}

	if shouldUseGDScriptCollector(language) && (opts.MaxFiles <= 0 || filesIndexed < opts.MaxFiles) {
		if err := checkBuildContext(ctx); err != nil {
			return BuildReport{}, err
		}

		remaining := 0
		if opts.MaxFiles > 0 {
			remaining = opts.MaxFiles - filesIndexed
		}

		addedFiles, addedSymbols, err := builder.addGDScriptIndex(ctx, root, repoID, scope, remaining, indexedFiles)
		if err != nil {
			return BuildReport{}, err
		}

		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols

		if addedFiles > 0 {
			languagesSeen["gdscript"] = struct{}{}
			collectors["gdscript_index"] = struct{}{}
		}

		if language == "gdscript" && addedFiles == 0 && (!patchActiveStore || !scopePathMissing(root, scope)) {
			return BuildReport{}, fmt.Errorf("no GDScript files found under %s", root)
		}
	}

	if shouldUseGenericCollector(language) && (opts.MaxFiles <= 0 || filesIndexed < opts.MaxFiles) {
		if err := checkBuildContext(ctx); err != nil {
			return BuildReport{}, err
		}

		remaining := 0
		if opts.MaxFiles > 0 {
			remaining = opts.MaxFiles - filesIndexed
		}

		addedFiles, addedSymbols, addedLanguages, err := builder.addGenericSourceIndex(ctx, root, repoID, scope, language, remaining, indexedFiles)
		if err != nil {
			return BuildReport{}, err
		}

		filesIndexed += addedFiles
		symbolsIndexed += addedSymbols

		if addedFiles > 0 {
			collectors["generic_source_index"] = struct{}{}
		}

		for _, language := range addedLanguages {
			languagesSeen[language] = struct{}{}
		}
	}

	reusedIndexedFiles := 0
	if incremental {
		reusedIndexedFiles = builder.mergePreviousGraph(previousGraph, scope)
		for _, language := range builderLanguageSet(builder.nodes) {
			languagesSeen[language] = struct{}{}
		}

		for _, collector := range builderCollectorSet(builder.nodes, builder.edges) {
			collectors[collector] = struct{}{}
		}
	}

	if filesIndexed == 0 && countNodesFromMap(builder.nodes, "file") == 0 && (!patchActiveStore || !scopePathMissing(root, scope)) {
		return BuildReport{}, fmt.Errorf("no source files found under %s for language %q", root, language)
	}

	languages := builderLanguageSet(builder.nodes)
	if len(languages) == 0 {
		languages = sortedStringSet(languagesSeen)
	}

	coverage := sortedStringSet(collectors)

	graph.Languages = languages
	graph.Nodes = sortedNodes(builder.nodes)
	graph.Edges = sortedEdges(builder.edges)

	if patchActiveStore {
		if partialRefreshReason == "" {
			if opts.ExactScope {
				partialRefreshReason = "exact_scope_refresh"
			} else {
				partialRefreshReason = "sqlite_scope_patch"
			}
		}

		graph.StoreKind = sqliteStoreKind
		graph.ID = util.FirstNonEmpty(statusBefore.IndexID, graphID(graph))
		graph.CollectorReports = collectorReportsExcept(statusBefore.CollectorReports, "summary_index")

		counts, err := patchSQLiteGraphScope(root, graph, scope, statusBefore)
		if err != nil {
			return BuildReport{}, err
		}

		// Scoped SQLite patches delete derived summary nodes instead of
		// leaving stale package/repository summaries behind.
		_, _ = MarkAllViewsStale(root)

		backends := storageBackends(root, sqliteStoreKind)

		if err := saveManifest(root, Manifest{
			SchemaVersion:     SchemaVersion,
			Root:              root,
			IndexID:           counts.IndexID,
			StoreKind:         sqliteStoreKind,
			GraphPath:         filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite")),
			GitHead:           gitHead,
			GitDirty:          gitDirty,
			GeneratedAt:       graph.GeneratedAt,
			CollectorVersion:  graph.CollectorVersion,
			CollectorVersions: graph.CollectorVersions,
			Languages:         counts.Languages,
			FilesIndexed:      counts.FilesIndexed,
			SymbolsIndexed:    counts.SymbolsIndexed,
			EdgesIndexed:      counts.EdgesIndexed,
			StorageBackends:   backends,
			CollectorReports:  graph.CollectorReports,
		}); err != nil {
			return BuildReport{}, err
		}

		_ = ensureIgnored(root)

		return BuildReport{
			IndexID:                counts.IndexID,
			ArtifactPath:           filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite")),
			ManifestPath:           filepath.ToSlash(filepath.Join(".idiom", "comprehension", "manifest.json")),
			StoreKind:              sqliteStoreKind,
			Root:                   root,
			GitHead:                gitHead,
			GitDirty:               gitDirty,
			CollectorVersion:       graph.CollectorVersion,
			CollectorVersions:      graph.CollectorVersions,
			Incremental:            true,
			ReusedIndexedFiles:     max(counts.FilesIndexed-filesIndexed, 0),
			ExpandedScope:          expandedScope,
			DependencyRefreshFiles: dependencyRefreshFiles,
			PartialRefreshReason:   partialRefreshReason,
			Languages:              counts.Languages,
			FilesIndexed:           filesIndexed,
			SymbolsIndexed:         counts.SymbolsIndexed,
			EdgesIndexed:           counts.EdgesIndexed,
			StorageBackends:        backends,
			RefreshedStaleFiles:    filterPathsInScope(append(append([]string(nil), statusBefore.StaleFiles...), statusBefore.MissingFiles...), scope),
			RefreshedStaleSymbols:  statusBefore.StaleSymbols,
			CollectorCoverage:      coverage,
			CollectorReports:       graph.CollectorReports,
		}, nil
	}

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	enrichmentReports, err := applyGraphEnrichers(ctx, opts, graph, repoID, &builder, coverage)
	if err != nil {
		return BuildReport{}, err
	}

	collectorReports := append([]CollectorReport(nil), enrichmentReports...)
	for _, report := range enrichmentReports {
		if strings.TrimSpace(report.Name) != "" {
			coverage = append(coverage, report.Name)
		}
	}

	summaryReport := builder.addSummaryIndex(repoID, languages)
	if summaryReport.NodesAdded > 0 {
		collectorReports = append(collectorReports, summaryReport)
		coverage = append(coverage, summaryReport.Name)
	}

	if len(collectorReports) > 0 {
		graph.CollectorReports = collectorReports
	}

	coverage = uniqueSortedStrings(coverage)
	graph.Nodes = sortedNodes(builder.nodes)
	graph.Edges = sortedEdges(builder.edges)
	graph.StoreKind = sqliteStoreKind
	graph.ID = graphID(graph)
	symbolsIndexed = countSymbolNodes(graph.Nodes)

	jsonGraph := graph

	jsonGraph.StoreKind = storeKind

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	if err := saveGraph(root, jsonGraph); err != nil {
		return BuildReport{}, err
	}

	if err := checkBuildContext(ctx); err != nil {
		return BuildReport{}, err
	}

	if err := saveSQLiteGraph(root, graph); err != nil {
		return BuildReport{}, err
	}

	// graph_views invalidation runs after the new graph is in place. Full
	// rebuilds blanket-stale the cache; scoped refresh marks only the
	// rows whose referenced paths changed, so the cache survives partial
	// updates instead of starting cold every refresh.
	if scopedBuild {
		changedPaths := ChangedPathsBetween(previousGraph, graph)
		if len(changedPaths) > 0 {
			_, _ = MarkViewsStale(root, changedPaths)
		}
	} else if previousGraphLoaded {
		changedPaths := ChangedPathsBetween(previousGraph, graph)
		if len(changedPaths) > 0 {
			_, _ = MarkViewsStale(root, changedPaths)
		}
	} else {
		_, _ = MarkAllViewsStale(root)
	}

	backends := storageBackends(root, sqliteStoreKind)
	refreshedStaleFiles := statusBefore.StaleFiles
	refreshedStaleSymbols := statusBefore.StaleSymbols

	if scopedBuild {
		refreshedStaleFiles = filterPathsInScope(statusBefore.StaleFiles, scope)
		refreshedStaleSymbols = countSymbolsForPaths(previousGraph.Nodes, refreshedStaleFiles)
	}

	if err := saveManifest(root, Manifest{
		SchemaVersion:     SchemaVersion,
		Root:              root,
		IndexID:           graph.ID,
		StoreKind:         sqliteStoreKind,
		GraphPath:         filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite")),
		GitHead:           gitHead,
		GitDirty:          gitDirty,
		GeneratedAt:       graph.GeneratedAt,
		CollectorVersion:  graph.CollectorVersion,
		CollectorVersions: graph.CollectorVersions,
		Languages:         languages,
		FilesIndexed:      filesIndexed,
		SymbolsIndexed:    symbolsIndexed,
		EdgesIndexed:      len(graph.Edges),
		StorageBackends:   backends,
		CollectorReports:  collectorReports,
	}); err != nil {
		return BuildReport{}, err
	}

	_ = ensureIgnored(root)

	return BuildReport{
		IndexID:                graph.ID,
		ArtifactPath:           filepath.ToSlash(filepath.Join(".idiom", "comprehension", "graph.sqlite")),
		ManifestPath:           filepath.ToSlash(filepath.Join(".idiom", "comprehension", "manifest.json")),
		StoreKind:              sqliteStoreKind,
		Root:                   root,
		GitHead:                gitHead,
		GitDirty:               gitDirty,
		CollectorVersion:       graph.CollectorVersion,
		CollectorVersions:      graph.CollectorVersions,
		Incremental:            incremental,
		ReusedIndexedFiles:     reusedIndexedFiles,
		ExpandedScope:          expandedScope,
		DependencyRefreshFiles: dependencyRefreshFiles,
		FullRebuildReason:      fullRebuildReason,
		PartialRefreshReason:   partialRefreshReason,
		Languages:              languages,
		FilesIndexed:           filesIndexed,
		SymbolsIndexed:         symbolsIndexed,
		EdgesIndexed:           len(graph.Edges),
		StorageBackends:        backends,
		RefreshedStaleFiles:    refreshedStaleFiles,
		RefreshedStaleSymbols:  refreshedStaleSymbols,
		CollectorCoverage:      coverage,
		CollectorReports:       collectorReports,
	}, nil
}

func checkBuildContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context index build canceled: %w", err)
	}

	return nil
}

func scopePathMissing(root string, scope string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(cleanScope(scope))))
	return os.IsNotExist(err)
}

func collectorReportsExcept(reports []CollectorReport, names ...string) []CollectorReport {
	if len(reports) == 0 {
		return nil
	}

	omit := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			omit[name] = struct{}{}
		}
	}

	out := make([]CollectorReport, 0, len(reports))
	for _, report := range reports {
		if _, skip := omit[strings.TrimSpace(report.Name)]; skip {
			continue
		}

		out = append(out, report)
	}

	return out
}

func Status(root string) (StatusReport, error) {
	return status(root, "")
}

func StatusForScope(root string, scope string) (StatusReport, error) {
	return status(root, cleanScope(scope))
}

func status(root string, scope string) (StatusReport, error) {
	root, err := normalizeRoot(root)
	if err != nil {
		return StatusReport{}, err
	}

	activeKind := activeGraphStoreKind(root, "")
	if activeKind == sqliteStoreKind {
		status, ok, err := statusFromSQLite(root, activeKind, scope)
		if ok || err == nil {
			return status, err
		}

		if !os.IsNotExist(err) {
			gitHead, gitDirty := gitState(context.Background(), root)
			return StatusReportWithMetadata(StatusReport{Indexed: false, Root: root, GitHead: gitHead, GitDirty: gitDirty, CurrentCollectorVersion: currentCollectorVersion(), StorageBackends: storageBackends(root, activeKind), Fresh: false}, "context_status", nil), nil
		}
	}

	graph, err := loadGraphForStatus(root)
	if err != nil {
		if os.IsNotExist(err) || activeKind != "" {
			gitHead, gitDirty := gitState(context.Background(), root)
			return StatusReportWithMetadata(StatusReport{Indexed: false, Root: root, GitHead: gitHead, GitDirty: gitDirty, CurrentCollectorVersion: currentCollectorVersion(), StorageBackends: storageBackends(root, activeKind), Fresh: false}, "context_status", nil), nil
		}

		return StatusReport{}, err
	}

	if activeKind == "" {
		activeKind = graph.StoreKind
	}

	currentVersion := currentCollectorVersion()
	collectorMismatch := strings.TrimSpace(graph.CollectorVersion) == "" || graph.CollectorVersion != currentVersion
	staleFiles, missingFiles := staleFilePaths(root, graph)

	if scope != "" && scope != "." {
		staleFiles = filterPathsInScope(staleFiles, scope)
		missingFiles = filterPathsInScope(missingFiles, scope)
	}

	staleSet := make(map[string]struct{}, len(staleFiles)+len(missingFiles))
	for _, path := range staleFiles {
		staleSet[path] = struct{}{}
	}

	for _, path := range missingFiles {
		staleSet[path] = struct{}{}
	}

	staleSymbols := 0

	var staleSymbolSamples []Node

	for _, node := range graph.Nodes {
		if isSymbolKind(node.Kind) {
			if _, ok := staleSet[node.Path]; ok {
				staleSymbols++

				if len(staleSymbolSamples) < 12 {
					staleSymbolSamples = append(staleSymbolSamples, node)
				}
			}
		}
	}

	gitHead, gitDirty := gitState(context.Background(), root)

	return StatusReportWithMetadata(StatusReport{
		Indexed:                  true,
		IndexID:                  graph.ID,
		StoreKind:                activeKind,
		Root:                     root,
		GitHead:                  gitHead,
		GitDirty:                 gitDirty,
		GeneratedAt:              graph.GeneratedAt,
		CollectorVersion:         graph.CollectorVersion,
		CurrentCollectorVersion:  currentVersion,
		CollectorVersionMismatch: collectorMismatch,
		Languages:                graph.Languages,
		FilesIndexed:             countNodes(graph.Nodes, "file"),
		SymbolsIndexed:           countSymbolNodes(graph.Nodes),
		EdgesIndexed:             len(graph.Edges),
		StorageBackends:          storageBackends(root, activeKind),
		CollectorReports:         graph.CollectorReports,
		StaleFiles:               staleFiles,
		StaleSymbols:             staleSymbols,
		StaleSymbolSamples:       summarizeNodes(staleSymbolSamples, StatusReport{StaleFiles: staleFiles, MissingFiles: missingFiles}),
		MissingFiles:             missingFiles,
		Fresh:                    !collectorMismatch && len(staleFiles) == 0 && len(missingFiles) == 0,
	}, "context_status", nil), nil
}

func Query(opts QueryOptions) (QueryResult, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return QueryResult{}, err
	}

	graph, err := LoadGraph(root)
	if err != nil {
		return QueryResult{}, err
	}

	status, _ := Status(root)
	limit := opts.Limit

	if limit <= 0 {
		limit = 12
	}

	resolution := strings.TrimSpace(opts.Resolution)
	if resolution == "" {
		resolution = "symbol"
	}

	terms := queryTerms(opts)
	if len(terms) == 0 {
		terms = []string{"."}
	}

	matched := matchNodes(graph.Nodes, terms, opts.Path, resolution, limit)
	result := QueryResult{
		IndexID:       graph.ID,
		StoreKind:     graph.StoreKind,
		Fresh:         status.Fresh,
		Resolution:    resolution,
		GeneratedAt:   graph.GeneratedAt,
		Confidence:    graphResultConfidenceForStatus(status, nil),
		Provenance:    graphResultProvenanceFromGraph("context_query", graph, ""),
		StaleEvidence: StaleEvidenceSafetyForStatus(status, nil),
		Nodes:         summarizeNodes(matched, status),
	}

	if len(matched) == 0 {
		result.UnresolvedGaps = append(result.UnresolvedGaps, "no graph nodes matched the query")
		return result, nil
	}

	ids := make(map[string]struct{}, len(matched))
	for _, node := range matched {
		ids[node.ID] = struct{}{}
		result.Claims = append(result.Claims, claimForNode(node))
	}

	edges := relatedEdges(graph.Edges, ids, limit*2)

	result.Edges = summarizeEdges(edges, opts.IncludeEvidence)

	if gap := GraphGapForStatus(status); gap != "" {
		result.UnresolvedGaps = append(result.UnresolvedGaps, gap)
	}

	return result, nil
}

func Zoom(opts ZoomOptions) (ZoomResult, error) {
	root, err := normalizeRoot(opts.Root)
	if err != nil {
		return ZoomResult{}, err
	}

	graph, err := LoadGraph(root)
	if err != nil {
		return ZoomResult{}, err
	}

	status, _ := Status(root)
	limit := opts.Limit

	if limit <= 0 {
		limit = 20
	}

	handle := strings.TrimSpace(opts.Handle)
	if handle == "" {
		return ZoomResult{}, fmt.Errorf("handle is required")
	}

	node, ok := findNode(graph.Nodes, handle)
	if !ok {
		return ZoomResult{
			IndexID:        graph.ID,
			StoreKind:      graph.StoreKind,
			Fresh:          status.Fresh,
			GeneratedAt:    graph.GeneratedAt,
			Confidence:     graphResultConfidenceForStatus(status, nil),
			Provenance:     graphResultProvenanceFromGraph("context_zoom", graph, ""),
			StaleEvidence:  StaleEvidenceSafetyForStatus(status, nil),
			Resolution:     defaultString(opts.Resolution, "symbol"),
			UnresolvedGaps: []string{"handle was not found in the context index"},
		}, nil
	}

	ids := map[string]struct{}{node.ID: {}}
	edges := relatedEdges(graph.Edges, ids, limit)
	relatedIDs := make(map[string]struct{})

	for _, edge := range edges {
		if edge.SourceID != node.ID {
			relatedIDs[edge.SourceID] = struct{}{}
		}

		if edge.TargetID != node.ID {
			relatedIDs[edge.TargetID] = struct{}{}
		}
	}

	related := make([]Node, 0, len(relatedIDs))
	for _, candidate := range graph.Nodes {
		if _, ok := relatedIDs[candidate.ID]; ok {
			related = append(related, candidate)
		}

		if len(related) >= limit {
			break
		}
	}

	summary := summarizeNodes([]Node{node}, status)
	result := ZoomResult{
		IndexID:       graph.ID,
		StoreKind:     graph.StoreKind,
		Fresh:         status.Fresh,
		Resolution:    defaultString(opts.Resolution, node.Kind),
		GeneratedAt:   graph.GeneratedAt,
		Confidence:    graphResultConfidenceForStatus(status, nil),
		Provenance:    graphResultProvenanceFromGraph("context_zoom", graph, ""),
		StaleEvidence: StaleEvidenceSafetyForStatus(status, nil),
		RelatedNodes:  summarizeNodes(related, status),
		Edges:         summarizeEdges(edges, true),
	}

	if len(summary) > 0 {
		result.Node = &summary[0]
	}

	if gap := GraphGapForStatus(status); gap != "" {
		result.UnresolvedGaps = append(result.UnresolvedGaps, gap)
	}

	return result, nil
}

func LoadGraph(root string) (Graph, error) {
	return loadGraph(root, false)
}

func loadGraphForStatus(root string) (Graph, error) {
	return loadGraph(root, true)
}

func loadGraph(root string, allowExportFallback bool) (Graph, error) {
	root, err := normalizeRoot(root)
	if err != nil {
		return Graph{}, err
	}

	manifest, manifestErr := readManifest(root)
	if manifestErr == nil && manifest.StoreKind == sqliteStoreKind {
		graph, err := loadSQLiteGraph(root)
		if err == nil {
			return graph, nil
		}

		if allowExportFallback {
			if fallback, fallbackErr := loadJSONGraph(root); fallbackErr == nil {
				return fallback, nil
			}
		}

		return Graph{}, err
	}

	if manifestErr != nil && os.IsNotExist(manifestErr) {
		graph, err := loadSQLiteGraph(root)
		if err == nil {
			return graph, nil
		}
	}

	return loadJSONGraph(root)
}

func loadJSONGraph(root string) (Graph, error) {
	data, err := os.ReadFile(graphPath(root))
	if err != nil {
		return Graph{}, err
	}

	var graph Graph

	if err := json.Unmarshal(data, &graph); err != nil {
		return Graph{}, fmt.Errorf("decode context graph: %w", err)
	}

	return graph, nil
}

func saveGraph(root string, graph Graph) error {
	return util.WriteJSON(graphPath(root), graph)
}

func saveManifest(root string, manifest Manifest) error {
	return util.WriteJSON(manifestPath(root), manifest)
}

func readManifest(root string) (Manifest, error) {
	data, err := os.ReadFile(manifestPath(root))
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest

	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode context graph manifest: %w", err)
	}

	return manifest, nil
}

func activeGraphStoreKind(root string, fallback string) string {
	manifest, err := readManifest(root)
	if err == nil && manifest.StoreKind != "" {
		return manifest.StoreKind
	}

	return fallback
}

func graphPath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "graph.json")
}

func manifestPath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "manifest.json")
}

func normalizeRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		var err error

		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}

	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}

	return filepath.Clean(abs), nil
}

func cleanScope(path string) string {
	path = cleanScopePath(path)
	if path == "" {
		return "."
	}

	return path
}

func cleanScopePath(path string) string {
	normalized := strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(path)), "\\", "/")
	if normalized == "" || filepath.IsAbs(path) || strings.HasPrefix(normalized, "/") || looksLikeWindowsAbsPath(normalized) {
		return ""
	}

	path = filepath.ToSlash(filepath.Clean(normalized))
	if path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}

	return strings.TrimSuffix(path, "/")
}

func pathInScope(path string, scope string) bool {
	scope = cleanScope(scope)
	if scope == "." {
		return true
	}

	path = cleanScopePath(path)
	if path == "" {
		return false
	}

	return path == scope || strings.HasPrefix(path, scope+"/")
}

func shouldVisitScopeDir(root string, path string, scope string) bool {
	scope = cleanScope(scope)
	if scope == "." {
		return true
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}

	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return true
	}

	return pathInScope(rel, scope) || strings.HasPrefix(scope, rel+"/")
}

func collectGenericSourceFiles(ctx context.Context, root string, scope string, language string, maxFiles int, indexedFiles map[string]struct{}) ([]genericSourceFile, error) {
	files := make([]genericSourceFile, 0)
	errStop := fmt.Errorf("stop generic source walk")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if err := checkBuildContext(ctx); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if !shouldVisitScopeDir(root, path, scope) {
				return filepath.SkipDir
			}

			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) {
			return nil
		}

		if !pathInScope(rel, scope) {
			return nil
		}

		if _, ok := indexedFiles[rel]; ok {
			return nil
		}

		fileLanguage := sourceLanguageForPath(rel)
		if fileLanguage == "" || !languageMatches(language, fileLanguage) {
			return nil
		}

		files = append(files, genericSourceFile{Path: rel, Language: fileLanguage})
		if maxFiles > 0 && len(files) >= maxFiles {
			return errStop
		}

		return nil
	})

	if err == errStop {
		err = nil
	}

	if err != nil {
		return nil, err
	}

	slices.SortFunc(files, func(a, b genericSourceFile) int { return cmp.Compare(a.Path, b.Path) })

	return files, nil
}

func collectGDScriptProjectFiles(ctx context.Context, root string, scope string, maxFiles int, indexedFiles map[string]struct{}) ([]genericSourceFile, error) {
	files := make([]genericSourceFile, 0)
	errStop := fmt.Errorf("stop gdscript project walk")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if err := checkBuildContext(ctx); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if !shouldVisitScopeDir(root, path, scope) {
				return filepath.SkipDir
			}

			if discovery.ShouldSkipDirName(d.Name()) && path != root {
				return filepath.SkipDir
			}

			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		rel = filepath.ToSlash(filepath.Clean(rel))
		if discovery.ShouldSkipRelFile(rel) || !pathInScope(rel, scope) {
			return nil
		}

		if _, ok := indexedFiles[rel]; ok {
			return nil
		}

		if !isGDScriptProjectPath(rel) {
			return nil
		}

		files = append(files, genericSourceFile{Path: rel, Language: "gdscript"})
		if maxFiles > 0 && len(files) >= maxFiles {
			return errStop
		}

		return nil
	})

	if err == errStop {
		err = nil
	}

	if err != nil {
		return nil, err
	}

	slices.SortFunc(files, func(a, b genericSourceFile) int { return cmp.Compare(a.Path, b.Path) })

	return files, nil
}

func isGDScriptProjectPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gd", ".tscn", ".tres":
		return true
	default:
		return filepath.Base(path) == "project.godot"
	}
}

func gdscriptFileRole(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gd":
		return "script"
	case ".tscn":
		return "scene"
	case ".tres":
		return "resource"
	default:
		if filepath.Base(path) == "project.godot" {
			return "project_settings"
		}

		return "file"
	}
}

func sourceLanguageForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".gd":
		return "gdscript"
	case ".py", ".pyi":
		return "python"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".lua":
		return "lua"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func languageMatches(requested string, actual string) bool {
	requested = normalizeLanguage(requested)
	actual = normalizeLanguage(actual)

	if requested == "" || requested == "auto" {
		return true
	}

	if requested == "typescript" {
		return actual == "typescript" || actual == "javascript"
	}

	return requested == actual
}

func fileFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

func rootFingerprint(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:8])
}

func packageFingerprint(path string, pkg semantic.GoPackage) string {
	payload := fmt.Sprintf("%s:%s:%d:%d:%d:%d", path, pkg.Name, pkg.Files, pkg.Funcs, pkg.Methods, pkg.Types)
	sum := sha256.Sum256([]byte(payload))

	return hex.EncodeToString(sum[:])
}

func genericPackageFingerprint(path string, language string) string {
	payload := fmt.Sprintf("%s:%s", path, language)
	sum := sha256.Sum256([]byte(payload))

	return hex.EncodeToString(sum[:])
}

// graphID is content-addressed: identical (root, git head, file
// path/fingerprint set) inputs produce identical IDs across builds,
// regardless of when the build ran. This is the load-bearing property
// for the graph_views packet cache — without it, every refresh would
// invalidate the cache by minting a new index_id.
//
// We hash file nodes' (path, fingerprint) pairs in sorted order rather
// than every node, because file fingerprints transitively cover symbol
// nodes (a symbol inherits its containing file's fingerprint). Edge
// count and collector versions are included so structural collector
// changes that add edges or reinterpret unchanged source facts still
// produce a new ID.
func graphID(graph Graph) string {
	type filePair struct {
		path        string
		fingerprint string
	}

	type collectorPair struct {
		name    string
		version string
	}

	files := make([]filePair, 0, len(graph.Nodes))

	for _, node := range graph.Nodes {
		if node.Kind != "file" || node.Path == "" {
			continue
		}

		files = append(files, filePair{path: node.Path, fingerprint: node.Fingerprint})
	}

	slices.SortFunc(files, func(a, b filePair) int { return cmp.Compare(a.path, b.path) })

	collectors := make([]collectorPair, 0, len(graph.CollectorVersions))
	for name, version := range graph.CollectorVersions {
		name = strings.TrimSpace(name)
		version = strings.TrimSpace(version)

		if name == "" || version == "" {
			continue
		}

		collectors = append(collectors, collectorPair{name: name, version: version})
	}

	slices.SortFunc(collectors, func(a, b collectorPair) int { return cmp.Compare(a.name, b.name) })

	h := sha256.New()
	h.Write([]byte(graph.Root))
	h.Write([]byte("\x00"))
	h.Write([]byte(graph.GitHead))
	h.Write([]byte("\x00"))
	h.Write([]byte(strings.TrimSpace(graph.CollectorVersion)))
	h.Write([]byte("\x00"))

	for _, collector := range collectors {
		h.Write([]byte(collector.name))
		h.Write([]byte("\x00"))
		h.Write([]byte(collector.version))
		h.Write([]byte("\x00"))
	}

	h.Write([]byte(strconv.Itoa(len(graph.Edges))))
	h.Write([]byte("\x00"))

	for _, f := range files {
		h.Write([]byte(f.path))
		h.Write([]byte("\x00"))
		h.Write([]byte(f.fingerprint))
		h.Write([]byte("\x00"))
	}

	sum := h.Sum(nil)

	return hex.EncodeToString(sum[:8])
}

func fileNodeID(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func packageNodeID(path string) string {
	return "package://" + filepath.ToSlash(path)
}

func symbolNodeID(path string, qualifiedName string) string {
	return "symbol://" + filepath.ToSlash(path) + "#" + qualifiedName
}

func externalDependencyNodeID(path string) string {
	return "external://" + path
}

func externalSymbolNodeID(language string, qualifiedName string) string {
	return "external_symbol://" + language + "/" + strings.TrimSpace(qualifiedName)
}

func pathDir(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		return "."
	}

	return dir
}

func packageNameForDir(dir string) string {
	if dir == "." || dir == "" {
		return "."
	}

	return filepath.Base(filepath.FromSlash(dir))
}

func defaultInt(value int, fallback int) int {
	if value == 0 {
		return fallback
	}

	return value
}

func edgeID(kind, sourceID, targetID string, evidence map[string]any) string {
	data, _ := json.Marshal(evidence)
	sum := sha256.Sum256([]byte(kind + "\x00" + sourceID + "\x00" + targetID + "\x00" + string(data)))

	return "edge://" + hex.EncodeToString(sum[:8])
}

func qualifiedGoSymbolName(symbol semantic.GoSymbol) string {
	if strings.TrimSpace(symbol.Receiver) == "" {
		return symbol.Name
	}

	return symbol.Receiver + "." + symbol.Name
}

func qualifiedCallName(call semantic.GoCall) string {
	if strings.TrimSpace(call.Qualifier) == "" {
		return call.Name
	}

	return call.Qualifier + "." + call.Name
}

func goImportAliases(path string) map[string]string {
	out := make(map[string]string)

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return out
	}

	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if importPath == "" {
			continue
		}

		if imp.Name != nil && imp.Name.Name != "" && imp.Name.Name != "_" && imp.Name.Name != "." {
			out[imp.Name.Name] = importPath
			continue
		}

		base := filepath.Base(importPath)
		if base != "." && base != "" {
			out[base] = importPath
		}
	}

	return out
}

func goSymbolIDsByName(index *semantic.GoIndex, packageDir string) map[string]string {
	out := make(map[string]string)
	if index == nil {
		return out
	}

	for _, file := range index.Files {
		if file.PathDir() != packageDir {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Kind != "func" && symbol.Kind != "method" && symbol.Kind != "type" {
				continue
			}

			out[symbol.Name] = symbolNodeID(file.Path, qualifiedGoSymbolName(symbol))
		}
	}

	return out
}

func isGoTestSymbol(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") || strings.HasPrefix(name, "Fuzz")
}

func goTestTargetID(testName string, symbolIDsByName map[string]string) string {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz"} {
		if strings.HasPrefix(testName, prefix) {
			target := strings.TrimPrefix(testName, prefix)
			if id := symbolIDsByName[target]; id != "" {
				return id
			}
		}
	}

	return ""
}

func goCallTargetID(index *semantic.GoIndex, call semantic.GoCall, scope string, packageDir string, importAliases map[string]string) (string, *Node) {
	if index == nil || strings.TrimSpace(call.Name) == "" {
		return "", nil
	}

	if strings.TrimSpace(call.Qualifier) == "" {
		return goPackageSymbolTargetID(index, packageDir, call.Name, "", scope), nil
	}

	if importPath := importAliases[call.Qualifier]; importPath != "" {
		if targetID := goLocalImportTargetID(index, importPath, call.Name, scope); targetID != "" {
			return targetID, nil
		}

		id := externalSymbolNodeID("go", importPath+"."+call.Name)
		node := goExternalCallNode(id, call, importPath)

		return id, &node
	}

	return goPackageSymbolTargetID(index, packageDir, call.Name, qualifiedCallName(call), scope), nil
}

func goLocalImportTargetID(index *semantic.GoIndex, importPath string, name string, scope string) string {
	dir := goLocalImportDir(index, importPath)
	if dir == "" {
		return ""
	}

	return goPackageSymbolTargetID(index, dir, name, "", scope)
}

func goLocalImportDir(index *semantic.GoIndex, importPath string) string {
	if index == nil || strings.TrimSpace(index.Module) == "" || strings.TrimSpace(importPath) == "" {
		return ""
	}

	module := strings.TrimSuffix(index.Module, "/")

	importPath = strings.TrimSuffix(importPath, "/")

	switch {
	case importPath == module:
		return "."
	case strings.HasPrefix(importPath, module+"/"):
		return filepath.ToSlash(strings.TrimPrefix(importPath, module+"/"))
	default:
		return ""
	}
}

func goPackageSymbolTargetID(index *semantic.GoIndex, packageDir string, name string, qualified string, scope string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	qualified = strings.ToLower(strings.TrimSpace(qualified))

	if name == "" {
		return ""
	}

	for _, file := range index.Files {
		if file.PathDir() != packageDir || !pathInScope(file.Path, scope) {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Kind != "func" && symbol.Kind != "method" {
				continue
			}

			full := strings.ToLower(qualifiedGoSymbolName(symbol))
			if qualified != "" {
				if full == qualified {
					return symbolNodeID(file.Path, qualifiedGoSymbolName(symbol))
				}

				continue
			}

			if strings.ToLower(symbol.Name) == name {
				return symbolNodeID(file.Path, qualifiedGoSymbolName(symbol))
			}
		}
	}

	return ""
}

func goExternalCallNode(id string, call semantic.GoCall, importPath string) Node {
	return Node{
		ID:            id,
		Kind:          "external_symbol",
		Language:      "go",
		QualifiedName: importPath + "." + call.Name,
		Collector:     "go_index",
		Confidence:    "syntax",
		Properties: map[string]any{
			"import":    importPath,
			"qualifier": call.Qualifier,
			"name":      call.Name,
		},
	}
}

func qualifiedCppSymbolName(symbol semantic.CppSymbol) string {
	if strings.TrimSpace(symbol.Qualified) != "" {
		return symbol.Qualified
	}

	if strings.TrimSpace(symbol.Scope) != "" {
		return symbol.Scope + "::" + symbol.Name
	}

	return symbol.Name
}

func cppSymbolKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "class", "struct", "enum":
		return "type"
	case "namespace":
		return "namespace"
	default:
		return kind
	}
}

func cppIncludeTargetID(index *semantic.CppIndex, include string) string {
	include = filepath.ToSlash(strings.TrimSpace(include))
	if include == "" {
		return ""
	}

	if index != nil && index.FileByPath(include) != nil {
		return fileNodeID(include)
	}

	return externalDependencyNodeID("cpp:" + include)
}

type cppCallTargetResolver struct {
	byName map[string]cppCallTarget
}

type cppCallTarget struct {
	id    string
	score int
}

func newCppCallTargetResolver(index *semantic.CppIndex, scope string) cppCallTargetResolver {
	resolver := cppCallTargetResolver{byName: make(map[string]cppCallTarget)}
	if index == nil {
		return resolver
	}

	for _, file := range index.Files {
		if !pathInScope(file.Path, scope) {
			continue
		}

		for _, symbol := range file.Symbols {
			if symbol.Kind != "func" && symbol.Kind != "method" {
				continue
			}

			name := strings.ToLower(symbol.Name)
			qualified := strings.ToLower(qualifiedCppSymbolName(symbol))
			id := symbolNodeID(file.Path, qualifiedCppSymbolName(symbol))
			resolver.add(qualified, id, cppCallTargetScore(symbol, 40))
			resolver.add(name, id, cppCallTargetScore(symbol, 30))
		}
	}

	return resolver
}

func (r cppCallTargetResolver) targetID(call semantic.CppCall) string {
	callName := strings.ToLower(strings.TrimSpace(call.Name))
	if callName == "" || len(r.byName) == 0 {
		return ""
	}

	return r.byName[callName].id
}

func (r cppCallTargetResolver) add(name string, id string, score int) {
	name = strings.TrimSpace(name)
	if name == "" || id == "" {
		return
	}

	if current, ok := r.byName[name]; ok && current.score >= score {
		return
	}

	r.byName[name] = cppCallTarget{id: id, score: score}
}

func cppCallTargetScore(symbol semantic.CppSymbol, base int) int {
	score := base
	if symbol.HasBody {
		score += 20
	}

	if symbol.Kind == "method" {
		score += 4
	}

	return score
}

var (
	pythonSymbolRe        = regexp.MustCompile(`^\s*(?:async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsFunctionRe          = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsDeclarationRe       = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(class|interface|type|enum)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	tsArrowFunctionRe     = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*(?:async\s*)?\(?[^=]*\)?\s*=>`)
	tsMethodRe            = regexp.MustCompile(`^\s*(?:public|private|protected|static|override|readonly|async|get|set|\s)*([A-Za-z_$][A-Za-z0-9_$]*)\s*\([^)]*\)\s*(?::\s*[^={]+)?\s*\{?`)
	rustSymbolRe          = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(fn|struct|enum|trait|impl)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	javaMethodRe          = regexp.MustCompile(`^\s*(?:public|private|protected|static|final|synchronized|native|abstract|default|\s)+[A-Za-z_$][A-Za-z0-9_$<>\[\], ?&.]*\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	classLikeSymbolRe     = regexp.MustCompile(`\b(class|interface|struct|enum|trait)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	functionLikeSymbolRe  = regexp.MustCompile(`\b(?:func|function|def|fn)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	genericCallRe         = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	pythonImportRe        = regexp.MustCompile(`^\s*(?:from\s+([A-Za-z_\.][A-Za-z0-9_\.]*)\s+import|import\s+([A-Za-z_][A-Za-z0-9_\.]*))`)
	tsImportFromRe        = regexp.MustCompile(`^\s*(?:import|export)(?:.+?\sfrom\s+)?["']([^"']+)["']`)
	tsRequireRe           = regexp.MustCompile(`\brequire\(["']([^"']+)["']\)`)
	rustUseRe             = regexp.MustCompile(`^\s*use\s+([^;]+);`)
	javaImportRe          = regexp.MustCompile(`^\s*import\s+([^;]+);`)
	cppIncludeRe          = regexp.MustCompile(`^\s*#\s*include\s+[<"]([^>"]+)`)
	gdClassNameRe         = regexp.MustCompile(`^\s*class_name\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdExtendsRe           = regexp.MustCompile(`^\s*extends\s+(.+)`)
	gdFunctionRe          = regexp.MustCompile(`^\s*(?:@[A-Za-z_][A-Za-z0-9_]*(?:\([^)]*\))?\s+)*(?:static\s+)?func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	gdSignalRe            = regexp.MustCompile(`^\s*signal\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdVarRe               = regexp.MustCompile(`^\s*(?:(?:@[A-Za-z_][A-Za-z0-9_]*(?:\([^)]*\))?|export(?:\([^)]*\))?|onready)\s+)*(?:var|const)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdInnerClassRe        = regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdEnumRe              = regexp.MustCompile(`^\s*enum\s+([A-Za-z_][A-Za-z0-9_]*)`)
	gdResourcePathRe      = regexp.MustCompile(`(?:preload|load)\(\s*["']([^"']+)["']\s*\)|extends\s+["']([^"']+)["']`)
	gdSceneResourcePathRe = regexp.MustCompile(`path=["']([^"']+\.(?:gd|tscn|tres))["']`)
	gdAutoloadRe          = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*["']\*?([^"']+\.gd)["']`)
)

func genericSymbols(path string, language string, content string) []genericSymbol {
	switch normalizeLanguage(language) {
	case "python":
		return genericPythonSymbols(content)
	case "typescript", "javascript":
		return genericBraceLanguageSymbols(content, "typescript")
	case "java":
		return genericBraceLanguageSymbols(content, "java")
	default:
		return genericFlatSymbols(path, language, content)
	}
}

func genericFlatSymbols(path string, language string, content string) []genericSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]genericSymbol, 0)

	for i, line := range lines {
		lineNo := i + 1

		switch normalizeLanguage(language) {
		case "rust":
			if match := rustSymbolRe.FindStringSubmatch(line); len(match) == 3 {
				symbols = append(symbols, genericSymbol{Name: match[2], Kind: genericKind(match[1]), Line: lineNo})
			}
		default:
			if match := classLikeSymbolRe.FindStringSubmatch(line); len(match) == 3 {
				symbols = append(symbols, genericSymbol{Name: match[2], Kind: genericKind(match[1]), Line: lineNo})
				continue
			}

			if match := functionLikeSymbolRe.FindStringSubmatch(line); len(match) == 2 {
				symbols = append(symbols, genericSymbol{Name: match[1], Kind: "func", Line: lineNo})
			}
		}
	}

	for i := range symbols {
		end := len(lines)
		if i+1 < len(symbols) {
			end = symbols[i+1].Line - 1
		}

		if symbols[i].End == 0 || symbols[i].End > end {
			symbols[i].End = end
		}

		if genericSymbolCanHaveCalls(symbols[i].Kind) {
			symbols[i].Calls = genericCallsInRange(lines, symbols[i].Line, symbols[i].End)
		}
	}

	return symbols
}

func genericPythonSymbols(content string) []genericSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]genericSymbol, 0)

	type pythonScope struct {
		name   string
		indent int
	}

	var scopes []pythonScope

	for i, line := range lines {
		lineNo := i + 1
		match := pythonSymbolRe.FindStringSubmatch(line)

		if len(match) != 3 {
			continue
		}

		indent := leadingWhitespaceColumns(line)
		for len(scopes) > 0 && indent <= scopes[len(scopes)-1].indent {
			scopes = scopes[:len(scopes)-1]
		}

		kind := genericKind(match[1])
		symbol := genericSymbol{Name: match[2], Kind: kind, Line: lineNo}

		if kind == "func" && len(scopes) > 0 {
			symbol.Kind = "method"
			symbol.Parent = scopes[len(scopes)-1].name
		}

		symbols = append(symbols, symbol)
		if kind == "type" {
			scopes = append(scopes, pythonScope{name: match[2], indent: indent})
		}
	}

	return finalizeGenericSymbols(symbols, lines)
}

type braceScope struct {
	name  string
	depth int
}

func genericBraceLanguageSymbols(content string, language string) []genericSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]genericSymbol, 0)

	var scopes []braceScope

	depth := 0

	for i, line := range lines {
		lineNo := i + 1
		clean := stripGenericComment(line)

		for len(scopes) > 0 && depth < scopes[len(scopes)-1].depth {
			scopes = scopes[:len(scopes)-1]
		}

		if symbol, ok := genericBraceSymbol(clean, language, scopes, lineNo); ok {
			symbols = append(symbols, symbol)
			if isTypeLikeSymbolKind(symbol.Kind) {
				scopes = append(scopes, braceScope{name: symbol.Name, depth: depth + braceDelta(clean)})
				if scopes[len(scopes)-1].depth <= depth {
					scopes[len(scopes)-1].depth = depth + 1
				}
			}
		}

		depth += braceDelta(clean)
		if depth < 0 {
			depth = 0
		}

		for len(scopes) > 0 && depth < scopes[len(scopes)-1].depth {
			scopes = scopes[:len(scopes)-1]
		}
	}

	return finalizeGenericSymbols(symbols, lines)
}

func genericBraceSymbol(line string, language string, scopes []braceScope, lineNo int) (genericSymbol, bool) {
	switch normalizeLanguage(language) {
	case "typescript", "javascript":
		if match := tsFunctionRe.FindStringSubmatch(line); len(match) == 2 {
			return genericSymbol{Name: match[1], Kind: "func", Line: lineNo}, true
		}

		if match := tsArrowFunctionRe.FindStringSubmatch(line); len(match) == 2 {
			return genericSymbol{Name: match[1], Kind: "func", Line: lineNo}, true
		}

		if match := tsDeclarationRe.FindStringSubmatch(line); len(match) == 3 {
			return genericSymbol{Name: match[2], Kind: genericKind(match[1]), Line: lineNo}, true
		}

		if len(scopes) > 0 {
			if match := tsMethodRe.FindStringSubmatch(line); len(match) == 2 && !isGenericCallKeyword(match[1]) {
				return genericSymbol{Name: match[1], Kind: "method", Line: lineNo, Parent: scopes[len(scopes)-1].name}, true
			}
		}
	case "java":
		if match := classLikeSymbolRe.FindStringSubmatch(line); len(match) == 3 {
			return genericSymbol{Name: match[2], Kind: genericKind(match[1]), Line: lineNo}, true
		}

		if len(scopes) > 0 && strings.Contains(line, "{") {
			if match := javaMethodRe.FindStringSubmatch(line); len(match) == 2 && !isGenericCallKeyword(match[1]) {
				return genericSymbol{Name: match[1], Kind: "method", Line: lineNo, Parent: scopes[len(scopes)-1].name}, true
			}
		}
	}

	return genericSymbol{}, false
}

func finalizeGenericSymbols(symbols []genericSymbol, lines []string) []genericSymbol {
	for i := range symbols {
		end := len(lines)
		if i+1 < len(symbols) {
			end = symbols[i+1].Line - 1
		}

		if symbols[i].End == 0 || symbols[i].End > end {
			symbols[i].End = end
		}

		if genericSymbolCanHaveCalls(symbols[i].Kind) {
			symbols[i].Calls = genericCallsInRange(lines, symbols[i].Line, symbols[i].End)
		}
	}

	return symbols
}

func isTypeLikeSymbolKind(kind string) bool {
	switch kind {
	case "type", "interface":
		return true
	default:
		return false
	}
}

func leadingWhitespaceColumns(line string) int {
	columns := 0

	for _, r := range line {
		switch r {
		case ' ':
			columns++
		case '\t':
			columns += 4
		default:
			return columns
		}
	}

	return columns
}

func braceDelta(line string) int {
	delta := 0
	inString := rune(0)
	escaped := false

	for _, r := range line {
		if inString != 0 {
			if escaped {
				escaped = false
				continue
			}

			if r == '\\' {
				escaped = true
				continue
			}

			if r == inString {
				inString = 0
			}

			continue
		}

		switch r {
		case '\'', '"', '`':
			inString = r
		case '{':
			delta++
		case '}':
			delta--
		}
	}

	return delta
}

func genericKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "class", "struct", "enum":
		return "type"
	case "interface", "trait":
		return "interface"
	case "def", "fn", "func", "function":
		return "func"
	case "impl":
		return "type"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func genericSymbolCanHaveCalls(kind string) bool {
	switch kind {
	case "func", "method", "test":
		return true
	default:
		return false
	}
}

func genericCallsInRange(lines []string, startLine int, endLine int) []genericCall {
	if startLine <= 0 || endLine < startLine {
		return nil
	}

	seen := make(map[string]struct{})

	var calls []genericCall

	for lineNo := startLine; lineNo <= endLine && lineNo <= len(lines); lineNo++ {
		line := stripGenericComment(lines[lineNo-1])
		for _, match := range genericCallRe.FindAllStringSubmatch(line, -1) {
			name := strings.TrimSpace(match[1])
			if isGenericCallKeyword(name) {
				continue
			}

			key := fmt.Sprintf("%s:%d", name, lineNo)
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}

			calls = append(calls, genericCall{Name: name, Line: lineNo})
		}
	}

	return calls
}

func stripGenericComment(line string) string {
	cut := len(line)
	for _, marker := range []string{"//", "#"} {
		if idx := strings.Index(line, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}

	return line[:cut]
}

func isGenericCallKeyword(name string) bool {
	switch strings.ToLower(name) {
	case "if", "for", "while", "switch", "catch", "return", "new", "class", "function", "def", "fn", "func", "sizeof", "typeof", "print", "println":
		return true
	default:
		return false
	}
}

func genericDependencies(language string, content string) []genericDependency {
	lines := strings.Split(content, "\n")
	deps := make([]genericDependency, 0)
	seen := make(map[string]struct{})
	add := func(name string, line int) {
		name = strings.TrimSpace(strings.Trim(name, `"'`))
		if name == "" {
			return
		}

		if _, ok := seen[name]; ok {
			return
		}

		seen[name] = struct{}{}
		deps = append(deps, genericDependency{Name: name, Line: line})
	}

	for i, line := range lines {
		lineNo := i + 1

		switch normalizeLanguage(language) {
		case "python":
			if match := pythonImportRe.FindStringSubmatch(line); len(match) == 3 {
				add(util.FirstNonEmpty(match[1], match[2]), lineNo)
			}
		case "typescript", "javascript":
			if match := tsImportFromRe.FindStringSubmatch(line); len(match) == 2 {
				add(match[1], lineNo)
			}

			if match := tsRequireRe.FindStringSubmatch(line); len(match) == 2 {
				add(match[1], lineNo)
			}
		case "rust":
			if match := rustUseRe.FindStringSubmatch(line); len(match) == 2 {
				add(match[1], lineNo)
			}
		case "java":
			if match := javaImportRe.FindStringSubmatch(line); len(match) == 2 {
				add(match[1], lineNo)
			}
		case "cpp":
			if match := cppIncludeRe.FindStringSubmatch(line); len(match) == 2 {
				add(match[1], lineNo)
			}
		}
	}

	return deps
}

func genericDependencyTargetID(sourcePath string, language string, dep string, existingFiles map[string]struct{}) string {
	dep = strings.TrimSpace(dep)

	switch normalizeLanguage(language) {
	case "typescript", "javascript":
		if strings.HasPrefix(dep, ".") {
			return localFileNodeID(genericRelativeCandidates(sourcePath, dep, []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}), existingFiles)
		}
	case "python":
		if strings.HasPrefix(dep, ".") {
			return localFileNodeID(genericRelativeCandidates(sourcePath, strings.TrimLeft(dep, "."), []string{".py"}), existingFiles)
		}

		modulePath := strings.ReplaceAll(dep, ".", "/")

		return localFileNodeID([]string{modulePath + ".py", modulePath + "/__init__.py", filepath.ToSlash(filepath.Join(pathDir(sourcePath), modulePath+".py"))}, existingFiles)
	case "rust":
		module := rustUseModule(dep)
		if module != "" {
			return localFileNodeID([]string{"src/" + module + ".rs", "src/" + module + "/mod.rs", filepath.ToSlash(filepath.Join(pathDir(sourcePath), module+".rs"))}, existingFiles)
		}
	case "java":
		javaPath := strings.ReplaceAll(dep, ".", "/") + ".java"
		if id := localFileNodeID([]string{"src/main/java/" + javaPath, "src/test/java/" + javaPath, javaPath}, existingFiles); id != "" {
			return id
		}

		if id := localFileNodeIDBySuffix(filepath.Base(javaPath), existingFiles); id != "" {
			return id
		}
	}

	return ""
}

func genericRelativeCandidates(sourcePath string, dep string, extensions []string) []string {
	base := filepath.ToSlash(filepath.Join(pathDir(sourcePath), filepath.FromSlash(dep)))

	var candidates []string

	candidates = append(candidates, base)

	for _, ext := range extensions {
		candidates = append(candidates, base+ext)
		candidates = append(candidates, filepath.ToSlash(filepath.Join(base, "index"+ext)))
	}

	return candidates
}

func localFileNodeID(candidates []string, existingFiles map[string]struct{}) string {
	for _, candidate := range candidates {
		candidate = filepath.ToSlash(filepath.Clean(candidate))
		if _, ok := existingFiles[candidate]; ok {
			return fileNodeID(candidate)
		}
	}

	return ""
}

func localFileNodeIDBySuffix(base string, existingFiles map[string]struct{}) string {
	var matches []string

	for path := range existingFiles {
		if filepath.Base(path) == base {
			matches = append(matches, path)
		}
	}

	if len(matches) != 1 {
		return ""
	}

	return fileNodeID(matches[0])
}

func rustUseModule(dep string) string {
	dep = strings.TrimSpace(dep)
	dep = strings.TrimPrefix(dep, "crate::")
	dep = strings.TrimPrefix(dep, "self::")
	dep = strings.TrimPrefix(dep, "super::")
	dep = strings.Trim(dep, "{}")

	parts := strings.Split(dep, "::")

	if len(parts) == 0 {
		return ""
	}

	part := strings.Trim(parts[0], "{} ")
	if part == "" || part == "*" {
		return ""
	}

	return part
}

func genericCallTargetID(sourcePath string, callName string, symbolIDsByName map[string][]string, importedFileIDs map[string]struct{}) string {
	matches := symbolIDsByName[strings.ToLower(strings.TrimSpace(callName))]
	if len(matches) == 0 {
		return ""
	}

	sourcePrefix := "symbol://" + filepath.ToSlash(sourcePath) + "#"
	for _, id := range matches {
		if strings.HasPrefix(id, sourcePrefix) {
			return id
		}
	}

	sourceDirPrefix := "symbol://" + pathDir(sourcePath) + "/"
	for _, id := range matches {
		if strings.HasPrefix(id, sourceDirPrefix) {
			return id
		}
	}

	for _, id := range matches {
		symbolFile, _, ok := strings.Cut(id, "#")
		if !ok {
			continue
		}

		fileID := "file://" + strings.TrimPrefix(symbolFile, "symbol://")
		if _, imported := importedFileIDs[fileID]; imported {
			return id
		}
	}

	if len(matches) == 1 {
		return matches[0]
	}

	return ""
}

func isGenericTestPath(path string, language string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))

	switch normalizeLanguage(language) {
	case "python":
		base := filepath.Base(lower)
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	case "typescript", "javascript":
		return strings.Contains(lower, "/__tests__/") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.")
	case "rust":
		return strings.Contains(lower, "/tests/") || strings.HasSuffix(lower, "_test.rs")
	case "java":
		return strings.HasSuffix(lower, "test.java") || strings.Contains(lower, "/test/")
	default:
		return strings.Contains(lower, "test")
	}
}

func isGenericTestSymbol(language string, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))

	switch normalizeLanguage(language) {
	case "python":
		return strings.HasPrefix(lower, "test_")
	case "typescript", "javascript":
		return lower == "test" || lower == "it" || strings.HasPrefix(lower, "test")
	case "rust", "java":
		return strings.HasPrefix(lower, "test")
	default:
		return strings.Contains(lower, "test")
	}
}

func gdscriptFileFacts(path string, content string, existing map[string]struct{}) ([]genericSymbol, []genericDependency) {
	switch gdscriptFileRole(path) {
	case "script":
		return gdscriptSymbols(path, content), gdscriptDependencies(path, content, existing)
	case "scene", "resource":
		return nil, gdscriptSceneDependencies(path, content, existing)
	case "project_settings":
		return gdscriptAutoloadSymbols(content), gdscriptProjectDependencies(path, content, existing)
	default:
		return nil, nil
	}
}

func gdscriptSymbols(path string, content string) []genericSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]genericSymbol, 0)

	for i, line := range lines {
		lineNo := i + 1

		switch {
		case gdClassNameRe.MatchString(line):
			match := gdClassNameRe.FindStringSubmatch(line)

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "type", Line: lineNo, Extra: map[string]any{"declaration": "class_name"}})
		case gdFunctionRe.MatchString(line):
			match := gdFunctionRe.FindStringSubmatch(line)
			kind := "func"

			if strings.Contains(strings.TrimSpace(line), "static func") {
				kind = "method"
			}

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: kind, Line: lineNo})
		case gdSignalRe.MatchString(line):
			match := gdSignalRe.FindStringSubmatch(line)

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "signal", Line: lineNo})
		case gdInnerClassRe.MatchString(line):
			match := gdInnerClassRe.FindStringSubmatch(line)

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "type", Line: lineNo, Extra: map[string]any{"declaration": "class"}})
		case gdEnumRe.MatchString(line):
			match := gdEnumRe.FindStringSubmatch(line)

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "type", Line: lineNo, Extra: map[string]any{"declaration": "enum"}})
		case gdVarRe.MatchString(line):
			match := gdVarRe.FindStringSubmatch(line)

			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "var", Line: lineNo})
		case gdExtendsRe.MatchString(line):
			match := gdExtendsRe.FindStringSubmatch(line)
			base := strings.Trim(strings.TrimSpace(match[1]), `"'`)

			if base != "" {
				symbols = append(symbols, genericSymbol{Name: "extends:" + base, Kind: "extends", Line: lineNo, Extra: map[string]any{"target": base}})
			}
		}
	}

	return symbols
}

func gdscriptAutoloadSymbols(content string) []genericSymbol {
	lines := strings.Split(content, "\n")
	symbols := make([]genericSymbol, 0)
	inAutoload := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inAutoload = trimmed == "[autoload]"
			continue
		}

		if !inAutoload {
			continue
		}

		if match := gdAutoloadRe.FindStringSubmatch(line); len(match) == 3 {
			symbols = append(symbols, genericSymbol{Name: match[1], Kind: "autoload", Line: i + 1, Extra: map[string]any{"target": strings.TrimPrefix(match[2], "res://")}})
		}
	}

	return symbols
}

func gdscriptDependencies(path string, content string, existing map[string]struct{}) []genericDependency {
	lines := strings.Split(content, "\n")
	deps := make([]genericDependency, 0)
	seen := make(map[string]struct{})
	add := func(raw string, line int) {
		target := normalizeGDScriptResource(path, raw, existing)
		if target == "" {
			target = strings.TrimSpace(raw)
		}

		if target == "" {
			return
		}

		if _, ok := seen[target]; ok {
			return
		}

		seen[target] = struct{}{}
		deps = append(deps, genericDependency{Name: target, Line: line})
	}

	for i, line := range lines {
		for _, match := range gdResourcePathRe.FindAllStringSubmatch(line, -1) {
			if len(match) != 3 {
				continue
			}

			add(util.FirstNonEmpty(match[1], match[2]), i+1)
		}
	}

	return deps
}

func gdscriptSceneDependencies(path string, content string, existing map[string]struct{}) []genericDependency {
	lines := strings.Split(content, "\n")
	deps := make([]genericDependency, 0)
	seen := make(map[string]struct{})

	for i, line := range lines {
		for _, match := range gdSceneResourcePathRe.FindAllStringSubmatch(line, -1) {
			if len(match) != 2 {
				continue
			}

			target := normalizeGDScriptResource(path, match[1], existing)
			if target == "" {
				continue
			}

			if _, ok := seen[target]; ok {
				continue
			}

			seen[target] = struct{}{}
			deps = append(deps, genericDependency{Name: target, Line: i + 1})
		}
	}

	return deps
}

func gdscriptProjectDependencies(path string, content string, existing map[string]struct{}) []genericDependency {
	lines := strings.Split(content, "\n")
	deps := make([]genericDependency, 0)
	seen := make(map[string]struct{})
	inAutoload := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inAutoload = trimmed == "[autoload]"
			continue
		}

		if !inAutoload {
			continue
		}

		if match := gdAutoloadRe.FindStringSubmatch(line); len(match) == 3 {
			target := normalizeGDScriptResource(path, match[2], existing)
			if target == "" {
				continue
			}

			if _, ok := seen[target]; ok {
				continue
			}

			seen[target] = struct{}{}
			deps = append(deps, genericDependency{Name: target, Line: i + 1})
		}
	}

	return deps
}

func normalizeGDScriptResource(sourcePath string, raw string, existing map[string]struct{}) string {
	raw = strings.Trim(strings.TrimSpace(raw), `"'`)
	if raw == "" {
		return ""
	}

	candidate := ""

	switch {
	case strings.HasPrefix(raw, "res://"):
		candidate = strings.TrimPrefix(raw, "res://")
	case strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"):
		candidate = filepath.ToSlash(filepath.Clean(filepath.Join(pathDir(sourcePath), raw)))
	default:
		candidate = raw
	}

	candidate = filepath.ToSlash(filepath.Clean(candidate))
	if _, ok := existing[candidate]; ok {
		return candidate
	}

	return raw
}

func dependencyNames(deps []genericDependency) []string {
	names := make([]string, 0, len(deps))
	for _, dep := range deps {
		names = append(names, dep.Name)
	}

	return names
}

func sortedNodes(nodes map[string]Node) []Node {
	out := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}

	slices.SortFunc(out, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })

	return out
}

func sortedEdges(edges map[string]Edge) []Edge {
	out := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, edge)
	}

	slices.SortFunc(out, func(a, b Edge) int { return cmp.Compare(a.ID, b.ID) })

	return out
}

func staleFilePaths(root string, graph Graph) ([]string, []string) {
	var (
		stale   []string
		missing []string
	)

	indexedFiles := make(map[string]struct{})

	for _, node := range graph.Nodes {
		if node.Kind != "file" || node.Path == "" {
			continue
		}

		indexedFiles[node.Path] = struct{}{}

		current, err := fileFingerprint(filepath.Join(root, filepath.FromSlash(node.Path)))

		if os.IsNotExist(err) {
			missing = append(missing, node.Path)
			continue
		}

		if err != nil {
			continue
		}

		if current != node.Fingerprint {
			stale = append(stale, node.Path)
		}
	}

	stale = append(stale, unindexedSourceFilePaths(root, ".", graph.Languages, indexedFiles)...)

	return uniqueSortedStrings(stale), uniqueSortedStrings(missing)
}

func unindexedSourceFilePaths(root string, scope string, indexedLanguages []string, indexedFiles map[string]struct{}) []string {
	files, err := collectGenericSourceFiles(context.Background(), root, scope, "auto", 0, indexedFiles)
	if err != nil {
		return nil
	}

	var paths []string

	for _, file := range files {
		if indexedLanguageIncludesSource(indexedLanguages, file.Language) {
			paths = append(paths, file.Path)
		}
	}

	return uniqueSortedStrings(paths)
}

func indexedLanguageIncludesSource(indexedLanguages []string, sourceLanguage string) bool {
	sourceLanguage = normalizeLanguage(sourceLanguage)
	if sourceLanguage == "" {
		return false
	}

	seenIndexedLanguage := false

	for _, indexedLanguage := range indexedLanguages {
		indexedLanguage = normalizeLanguage(indexedLanguage)
		if indexedLanguage == "" {
			continue
		}

		seenIndexedLanguage = true

		if languageMatches(indexedLanguage, sourceLanguage) {
			return true
		}
	}

	return !seenIndexedLanguage
}

func countNodes(nodes []Node, kind string) int {
	count := 0

	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
	}

	return count
}

func countSymbolNodes(nodes []Node) int {
	count := 0

	for _, node := range nodes {
		if isSymbolKind(node.Kind) {
			count++
		}
	}

	return count
}

func isSymbolKind(kind string) bool {
	switch kind {
	case "symbol", "type", "class", "struct", "enum", "namespace", "interface", "method", "function", "func", "test", "bench", "constructor", "field", "const", "var", "signal", "extends", "autoload", "macro", "mod", "impl", "external_symbol":
		return true
	default:
		return false
	}
}

func queryTerms(opts QueryOptions) []string {
	var terms []string

	for _, value := range []string{opts.Symbol, opts.Query, opts.Path, opts.Scope} {
		for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '/' || r == '.')
		}) {
			field = strings.Trim(strings.TrimSpace(field), ".,;:!?\"'()[]{}<>`")
			if isQueryStopword(field) {
				continue
			}

			if field != "" {
				terms = append(terms, field)
				terms = append(terms, queryTermVariants(field)...)
			}
		}
	}

	return uniqueStrings(terms)
}

func isQueryStopword(value string) bool {
	switch value {
	case "", "a", "agentic", "an", "and", "another", "answer", "are", "as", "assistant", "be", "by", "can", "cause", "caused", "causes", "conversation", "debug", "do", "does", "evidence", "file", "files", "finding", "findings", "for", "form", "from", "get", "how", "i", "in", "index", "indexed", "is", "issue", "it", "line", "lines", "me", "mid", "non", "number", "numbers", "of", "on", "or", "our", "please", "present", "process", "provide", "relevant", "research", "show", "structured", "symbol", "symbols", "that", "the", "this", "to", "try", "trying", "we", "what", "where", "which", "with", "you", "your":
		return true
	default:
		return false
	}
}

func singularQueryTerm(value string) string {
	if strings.HasSuffix(value, "ies") && len(value) > 3 {
		return strings.TrimSuffix(value, "ies") + "y"
	}

	if strings.HasSuffix(value, "ses") && len(value) > 3 {
		return strings.TrimSuffix(value, "s")
	}

	if strings.HasSuffix(value, "s") && len(value) > 3 {
		return strings.TrimSuffix(value, "s")
	}

	return value
}

func queryTermVariants(value string) []string {
	var terms []string

	if singular := singularQueryTerm(value); singular != "" && singular != value {
		terms = append(terms, singular)
	}

	if strings.HasSuffix(value, "ing") && len(value) > 5 {
		stem := strings.TrimSuffix(value, "ing")
		if len(stem) >= 2 && stem[len(stem)-1] == stem[len(stem)-2] {
			stem = stem[:len(stem)-1]
		}

		if stem != "" && stem != value {
			terms = append(terms, stem)
		}

		if withE := stem + "e"; withE != value {
			terms = append(terms, withE)
		}
	}

	return terms
}

func matchNodes(nodes []Node, terms []string, path string, resolution string, limit int) []Node {
	var matches []Node

	path = strings.TrimSpace(filepath.ToSlash(path))

	for _, node := range nodes {
		if path != "" && node.Path != "" && !strings.Contains(strings.ToLower(node.Path), strings.ToLower(path)) {
			continue
		}

		if resolution != "" && !nodeMatchesResolution(node, resolution) {
			continue
		}

		score := nodeMatchScore(node, terms)
		if score <= 0 {
			continue
		}

		node.Properties = cloneProperties(node.Properties)
		node.Properties["match_score"] = score
		matches = append(matches, node)
	}

	sort.Slice(matches, func(i, j int) bool {
		left, _ := matches[i].Properties["match_score"].(int)
		right, _ := matches[j].Properties["match_score"].(int)

		if left != right {
			return left > right
		}

		return matches[i].ID < matches[j].ID
	})

	if limit > 0 && len(matches) > limit {
		matches = diversifyNodeMatchesByQueryTerms(matches, terms, limit)
	}

	return matches
}

type nodeTermCoverage struct {
	term  string
	count int
}

func diversifyNodeMatchesByQueryTerms(matches []Node, terms []string, limit int) []Node {
	if limit <= 0 || len(matches) <= limit {
		return matches
	}

	coverage := nodeTermCoverageList(matches, terms)
	if len(coverage) == 0 {
		return matches[:limit]
	}

	sort.Slice(coverage, func(i, j int) bool {
		if coverage[i].count != coverage[j].count {
			return coverage[i].count < coverage[j].count
		}

		return coverage[i].term < coverage[j].term
	})

	coverageSlots := limit / 4
	if coverageSlots < 4 {
		coverageSlots = 4
	}

	if coverageSlots > limit {
		coverageSlots = limit
	}

	if coverageSlots > len(coverage) {
		coverageSlots = len(coverage)
	}

	selected := make(map[string]struct{}, limit)
	selectedPaths := make(map[string]struct{}, limit)
	out := make([]Node, 0, limit)

	for _, candidate := range coverage[:coverageSlots] {
		node, ok := bestNodeForQueryTerm(matches, candidate.term, selected, selectedPaths)
		if !ok {
			continue
		}

		selected[node.ID] = struct{}{}
		if node.Path != "" {
			selectedPaths[node.Path] = struct{}{}
		}

		out = append(out, node)
	}

	for _, node := range matches {
		if len(out) >= limit {
			break
		}

		if _, ok := selected[node.ID]; ok {
			continue
		}

		selected[node.ID] = struct{}{}
		out = append(out, node)
	}

	return out
}

func nodeTermCoverageList(matches []Node, terms []string) []nodeTermCoverage {
	var coverage []nodeTermCoverage

	for _, term := range terms {
		if len(term) < 3 || term == "." {
			continue
		}

		count := 0

		for _, node := range matches {
			if nodeTermMatchScore(node, term) > 0 {
				count++
			}
		}

		if count > 0 {
			coverage = append(coverage, nodeTermCoverage{term: term, count: count})
		}
	}

	return coverage
}

func bestNodeForQueryTerm(matches []Node, term string, selected map[string]struct{}, selectedPaths map[string]struct{}) (Node, bool) {
	var best Node

	bestScore := -1
	bestTotal := -1
	bestKind := -1
	bestHasNewPath := false

	for _, node := range matches {
		if _, ok := selected[node.ID]; ok {
			continue
		}

		score := nodeTermMatchScore(node, term)
		if score <= 0 {
			continue
		}

		total, _ := node.Properties["match_score"].(int)
		kindScore := queryMatchKindScore(node.Kind)
		hasNewPath := node.Path == ""

		if node.Path != "" {
			_, alreadyCovered := selectedPaths[node.Path]

			hasNewPath = !alreadyCovered
		}

		if bestScore < 0 ||
			(hasNewPath && !bestHasNewPath) ||
			(hasNewPath == bestHasNewPath && score > bestScore) ||
			(hasNewPath == bestHasNewPath && score == bestScore && kindScore > bestKind) ||
			(hasNewPath == bestHasNewPath && score == bestScore && kindScore == bestKind && total > bestTotal) ||
			(hasNewPath == bestHasNewPath && score == bestScore && kindScore == bestKind && total == bestTotal && node.ID < best.ID) {

			best = node
			bestScore = score
			bestTotal = total
			bestKind = kindScore
			bestHasNewPath = hasNewPath
		}
	}

	return best, bestScore >= 0
}

func queryMatchKindScore(kind string) int {
	if isSymbolKind(kind) {
		return 4
	}

	switch kind {
	case "file":
		return 3
	case "package", "module":
		return 2
	case "summary":
		return 1
	default:
		return 0
	}
}

func nodeMatchesResolution(node Node, resolution string) bool {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "", "any":
		return true
	case "repo", "repository":
		return node.Kind == "repo"
	case "package", "module":
		return node.Kind == "package" || node.Kind == "module"
	case "file":
		return node.Kind == "file"
	case "symbol":
		return isSymbolKind(node.Kind)
	case "summary":
		return node.Kind == "summary"
	default:
		return node.Kind == resolution
	}
}

func nodeMatchScore(node Node, terms []string) int {
	if len(terms) == 1 && terms[0] == "." {
		if node.Kind == "repo" {
			return 100
		}

		if node.Kind == "summary" {
			return 30
		}

		if node.Kind == "package" || node.Kind == "file" {
			return 20
		}

		return 0
	}

	score := 0
	for _, term := range terms {
		score += nodeTermMatchScore(node, term)
	}

	return score
}

func nodeTermMatchScore(node Node, term string) int {
	if term == "" {
		return 0
	}

	score := 0
	path := strings.ToLower(node.Path)
	name := strings.ToLower(node.QualifiedName)
	kind := strings.ToLower(node.Kind)

	switch {
	case name == term:
		score += 80
	case strings.Contains(name, term):
		score += 40
	case strings.Contains(path, term):
		score += 24
	case strings.Contains(kind, term):
		score += 10
	}

	if value, ok := node.Properties["name"].(string); ok && strings.Contains(strings.ToLower(value), term) {
		score += 30
	}

	return score
}

func summarizeNodes(nodes []Node, status StatusReport) []NodeSummary {
	stale := make(map[string]struct{})
	for _, path := range status.StaleFiles {
		stale[path] = struct{}{}
	}

	for _, path := range status.MissingFiles {
		stale[path] = struct{}{}
	}

	out := make([]NodeSummary, 0, len(nodes))
	for _, node := range nodes {
		_, isStale := stale[node.Path]

		out = append(out, NodeSummary{
			ID:            node.ID,
			Kind:          node.Kind,
			Path:          node.Path,
			Language:      node.Language,
			QualifiedName: node.QualifiedName,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			Confidence:    node.Confidence,
			Stale:         isStale,
			Properties:    node.Properties,
		})
	}

	return out
}

func summarizeEdges(edges []Edge, includeEvidence bool) []EdgeSummary {
	out := make([]EdgeSummary, 0, len(edges))
	for _, edge := range edges {
		item := EdgeSummary{
			ID:         edge.ID,
			Kind:       edge.Kind,
			SourceID:   edge.SourceID,
			TargetID:   edge.TargetID,
			Confidence: edge.Confidence,
		}
		if includeEvidence {
			item.Evidence = edge.Evidence
		}

		out = append(out, item)
	}

	return out
}

func relatedEdges(edges []Edge, ids map[string]struct{}, limit int) []Edge {
	out := make([]Edge, 0)

	for _, edge := range edges {
		_, source := ids[edge.SourceID]
		_, target := ids[edge.TargetID]

		if !source && !target {
			continue
		}

		out = append(out, edge)
		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out
}

func findNode(nodes []Node, handle string) (Node, bool) {
	for _, node := range nodes {
		if node.ID == handle || node.Path == handle || node.QualifiedName == handle {
			return node, true
		}
	}

	return Node{}, false
}

func claimForNode(node Node) string {
	label := node.ID
	if node.QualifiedName != "" {
		label = node.QualifiedName
	}

	if node.Path != "" && node.StartLine > 0 {
		return fmt.Sprintf("%s %q is in %s lines %d-%d (%s confidence)", node.Kind, label, node.Path, node.StartLine, node.EndLine, node.Confidence)
	}

	if node.Path != "" {
		return fmt.Sprintf("%s %q maps to %s (%s confidence)", node.Kind, label, node.Path, node.Confidence)
	}

	return fmt.Sprintf("%s %q is present in the context index (%s confidence)", node.Kind, label, node.Confidence)
}

func cloneProperties(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}

	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}

		seen[value] = struct{}{}
		out = append(out, value)
	}

	return out
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}

	slices.Sort(out)

	return out
}

func uniqueSortedStrings(values []string) []string {
	out := util.UniqueNonEmpty(values)
	slices.Sort(out)

	return out
}

func normalizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, ".")

	switch value {
	case "", "auto", "all", "*":
		return "auto"
	case "go", "golang":
		return "go"
	case "gd", "gdscript", "godot":
		return "gdscript"
	case "ts", "tsx", "mts", "cts", "d.ts", "d.mts", "d.cts":
		return "typescript"
	case "js", "jsx", "mjs", "cjs", "node":
		return "javascript"
	case "py":
		return "python"
	case "c++", "cc", "cxx":
		return "cpp"
	case "c#", "cs":
		return "csharp"
	default:
		return value
	}
}

func shouldUseGoCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "go"
}

func shouldUseCppCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "cpp"
}

func shouldUseGDScriptCollector(language string) bool {
	language = normalizeLanguage(language)
	return language == "auto" || language == "gdscript"
}

func shouldUseGenericCollector(language string) bool {
	switch normalizeLanguage(language) {
	case "go", "cpp", "gdscript", "python", "typescript", "javascript", "rust", "java":
		return false
	default:
		return true
	}
}

func gitState(ctx context.Context, root string) (string, bool) {
	head := strings.TrimSpace(execOutput(ctx, root, "git", "-C", root, "rev-parse", "HEAD"))
	status := strings.TrimSpace(execOutput(ctx, root, "git", "-C", root, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=all"))

	return head, status != ""
}

func execOutput(ctx context.Context, dir string, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return string(out)
}

func ensureIgnored(root string) error {
	exclude := execOutput(context.Background(), root, "git", "-C", root, "rev-parse", "--git-path", "info/exclude")

	exclude = strings.TrimSpace(exclude)

	if exclude == "" {
		return nil
	}

	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(root, exclude)
	}

	data, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".idiom/", "/.idiom/":
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}

	addition := "# Idiom generated artifacts\n.idiom/\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		addition = "\n" + addition
	}

	file, err := os.OpenFile(exclude, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(addition)

	return err
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}
