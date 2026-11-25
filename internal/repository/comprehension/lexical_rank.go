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
}

// fuseRankedLists is the seam where later phases combine retrievers (exact
// symbol matches, graph neighbours) by reciprocal rank fusion. Phase 1 has
// one retriever, so its list passes through unchanged.
func fuseRankedLists(lists ...rankedList) []LexicalHit {
	if len(lists) == 0 {
		return nil
	}

	return lists[0].Hits
}
