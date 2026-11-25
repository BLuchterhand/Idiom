package comprehension

import (
	"strings"
)

// lexicalPackFiles builds the pack's candidate files from the BM25 index:
// declared edit paths and runtime-overlay files first, then every file the
// task's terms retrieve, best first, cut at the pack limit. It reports false
// when the index cannot answer (an index built before the lexical tables, or
// a task with no indexed term), and the caller falls back to graph-only
// ranking so coverage never drops.
//
// Scope (PackOptions.Path and Scope) restricts which files may appear; it is
// never a query term and never changes a score.
func lexicalPackFiles(ctx *packContext, files *packFileBuilder, overlay runtimePackOverlay) ([]PackFile, bool) {
	terms := UniqueLexicalTerms(LexicalTokens(ctx.task))
	if len(terms) == 0 {
		return nil, false
	}

	corpus, err := LoadLexicalCorpus(ctx.root, terms)
	if err != nil {
		return nil, false
	}

	hits := fuseRankedLists(rankedList{
		Source: "bm25",
		Hits:   RankLexical(corpus, terms, packCandidateFilter(ctx, files)),
	})
	if len(hits) == 0 {
		return nil, false
	}

	selected := pinnedPackFiles(ctx, files, overlay)
	chosen := make(map[string]struct{}, len(selected))

	for _, file := range selected {
		chosen[file.Path] = struct{}{}
	}

	top := hits[0].Score

	for _, hit := range hits {
		if ctx.limit > 0 && len(selected) >= ctx.limit {
			break
		}

		if _, dup := chosen[hit.Path]; dup {
			continue
		}

		selected = append(selected, lexicalPackFile(ctx, files, hit, top))
		chosen[hit.Path] = struct{}{}
	}

	if ctx.limit > 0 && len(selected) > ctx.limit {
		selected = selected[:ctx.limit]
	}

	return selected, true
}

// packCandidateFilter admits a file when it is inside the requested scope and
// not forbidden. Forbidden files are recorded as omitted for the pack's risks.
func packCandidateFilter(ctx *packContext, files *packFileBuilder) func(string) bool {
	return func(path string) bool {
		if !pathInScope(path, ctx.opts.Path) || !pathInScope(path, ctx.opts.Scope) {
			return false
		}

		for _, forbidden := range ctx.forbidden {
			if pathInScope(path, forbidden) {
				files.omittedForbidden[forbidden] = true

				return false
			}
		}

		return true
	}
}

// pinnedPackFiles are the files the caller named (required edit paths) and
// the runtime overlay surfaced; they lead the pack regardless of score.
func pinnedPackFiles(ctx *packContext, files *packFileBuilder, overlay runtimePackOverlay) []PackFile {
	var pinned []PackFile

	seen := map[string]struct{}{}
	add := func(path, reason string) {
		file, ok := files.files[path]
		if _, dup := seen[path]; dup || !ok {
			return
		}

		seen[path] = struct{}{}
		file.Score = 1
		file.RankerReasons = []RankerReason{{Reason: reason, State: "declared", ScoreDelta: 1}}

		pinned = append(pinned, file)
	}

	for _, path := range ctx.requiredEditPaths {
		add(path, "required_edit_path")
	}

	for _, file := range overlay.Files {
		add(file.Path, "runtime_overlay")
	}

	return pinned
}

func lexicalPackFile(ctx *packContext, files *packFileBuilder, hit LexicalHit, top float64) PackFile {
	file := files.files[hit.Path]
	if file.Path == "" {
		node := ctx.nodesByID[fileNodeID(hit.Path)]
		file = PackFile{Path: hit.Path, Language: node.Language, Confidence: node.Confidence}

		if node.ID != "" {
			file.Handles = []string{node.ID}
		}
	}

	file.Role = "editable"
	if IsLexicalTestPath(hit.Path) {
		file.Role = "test"
	}

	file.Reason = "BM25 match: " + strings.Join(hit.Terms, ", ")
	file.Stale = file.Stale || packPathIsStale(ctx.stale, hit.Path)
	file.Score = hit.Score / top
	file.matched = hit.Terms
	file.RankerReasons = lexicalRankerReasons(hit, file.Role)

	return file
}

func lexicalRankerReasons(hit LexicalHit, role string) []RankerReason {
	// role_floor stays in the list, at zero, because the pack's ranker_reasons
	// field has always led with it and consumers read it.
	reasons := []RankerReason{
		{Reason: "role_floor", State: role},
		{Reason: "bm25_match", State: strings.Join(hit.Terms, ","), ScoreDelta: roundScore(hit.Score)},
	}
	if role == "test" {
		reasons = append(reasons, RankerReason{Reason: "test_demotion", State: "test_path", Multiplier: lexicalTestWeight})
	}

	return reasons
}
