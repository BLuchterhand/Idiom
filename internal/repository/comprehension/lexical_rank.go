package comprehension

import (
	"slices"
	"strings"
)

const maxHitTerms = 3 // matched terms named in a hit's explanation

// LexicalHit is one ranked file: its BM25 score (test files demoted) and the
// query terms that contributed most to it, strongest first.
type LexicalHit struct {
	Path  string
	Score float64
	Terms []string
	// Via names the matched file a graph hop reached this one from; empty
	// for files that matched the task themselves.
	Via string
	// Sources names the retrievers whose lists held this file after fusion
	// (empty before it).
	Sources []string
}

// RankLexical scores every file that shares a query term with BM25 and
// returns them best first, ties by path. include restricts which files may
// appear (scope, forbidden paths); it never changes a score, so corpus
// statistics stay those of the whole index. A nil include admits every file.
func RankLexical(corpus LexicalCorpus, terms []string, include func(path string) bool) []LexicalHit {
	if corpus.Docs == 0 {
		return nil
	}

	avg := BM25AvgLength(corpus.TotalLen, corpus.Docs)

	type acc struct {
		score float64
		best  []termShare
	}

	byPath := map[string]*acc{}

	for _, term := range UniqueLexicalTerms(terms) {
		postings := corpus.Postings[term]
		if len(postings) == 0 {
			continue
		}

		idf := BM25IDF(corpus.Docs, len(postings))

		for _, p := range postings {
			a := byPath[p.Path]
			if a == nil {
				a = &acc{}
				byPath[p.Path] = a
			}

			share := idf * BM25TermScore(p.TF, p.Length, avg)
			a.score += share
			a.best = append(a.best, termShare{term, share})
		}
	}

	hits := make([]LexicalHit, 0, len(byPath))

	for path, a := range byPath {
		if include != nil && !include(path) {
			continue
		}

		score := a.score
		if IsLexicalTestPath(path) {
			score *= lexicalTestWeight
		}

		hits = append(hits, LexicalHit{Path: path, Score: score, Terms: topTerms(a.best)})
	}

	slices.SortFunc(hits, func(a, b LexicalHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return strings.Compare(a.Path, b.Path)
		}
	})

	return hits
}

type termShare struct {
	term  string
	share float64
}

func topTerms(shares []termShare) []string {
	slices.SortStableFunc(shares, func(a, b termShare) int {
		switch {
		case a.share > b.share:
			return -1
		case a.share < b.share:
			return 1
		default:
			return strings.Compare(a.term, b.term)
		}
	})

	out := make([]string, 0, maxHitTerms)
	for _, s := range shares[:min(maxHitTerms, len(shares))] {
		out = append(out, s.term)
	}

	return out
}

// rankedList is one retriever's ordered candidates, named for diagnostics.
type rankedList struct {
	Source string
	Hits   []LexicalHit
	// Weight scales a hit's reciprocal-rank share by its path (per-language
	// channel weights); nil means 1 for every file, and a weight of 0 or less
	// drops the hit.
	Weight func(path string) float64
}

const (
	// rrfK is the reciprocal rank fusion constant of Cormack, Clarke and
	// Buettcher (SIGIR 2009); 60 is their published default, not fitted.
	rrfK = 60
	// fusionDepth cuts each list before fusing, so membership in a long tail
	// does not count as agreement.
	fusionDepth = 24
)

// fuseRankedLists combines retrievers by weighted reciprocal rank fusion: a
// file scores the sum of weight/(rrfK+rank) over the lists that hold it in
// their first fusionDepth entries (weight 1 unless the list sets Weight). Ties break by path. Matched terms come
// from whichever list carried them.
func fuseRankedLists(lists ...rankedList) []LexicalHit {
	byPath := map[string]*LexicalHit{}

	for _, list := range lists {
		for rank, hit := range list.Hits[:min(fusionDepth, len(list.Hits))] {
			weight := 1.0
			if list.Weight != nil {
				if weight = list.Weight(hit.Path); weight <= 0 {
					continue
				}
			}

			fused := byPath[hit.Path]
			if fused == nil {
				fused = &LexicalHit{Path: hit.Path}
				byPath[hit.Path] = fused
			}

			fused.Score += weight / float64(rrfK+rank+1)
			fused.Sources = append(fused.Sources, list.Source)

			if len(fused.Terms) == 0 {
				fused.Terms = hit.Terms
			}

			if fused.Via == "" {
				fused.Via = hit.Via
			}
		}
	}

	out := make([]LexicalHit, 0, len(byPath))
	for _, hit := range byPath {
		out = append(out, *hit)
	}

	slices.SortFunc(out, func(a, b LexicalHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return strings.Compare(a.Path, b.Path)
		}
	})

	return out
}
