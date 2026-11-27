package comprehension

import (
	"math"
	"slices"
	"strings"
)

const (
	pastTopItems       = 30   // matching work items that contribute files
	pastHalfLifeDays   = 30.0 // recency decay of an item's or commit's weight
	pastSecondsPerDay  = 86400.0
	coChangeSeedCount  = 8 // head of the other channels that seeds the graph
	coChangeMinSupport = 2 // commits two files must share before they count as coupled
)

func ageDecay(asOf, ts int64) float64 {
	return math.Pow(0.5, math.Max(0, float64(asOf-ts))/pastSecondsPerDay/pastHalfLifeDays)
}

func sortHits(hits []LexicalHit) {
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
}

// rankSimilarWork is BM25 over work items, each item's score decayed by age.
// The best pastTopItems give each of their files the item's score divided by
// log2(1+files), so a sweeping item says little about any one file.
func rankSimilarWork(h ChangeHistory, terms []string, include func(string) bool) []LexicalHit {
	terms = UniqueLexicalTerms(terms)
	if len(h.Items) == 0 || len(terms) == 0 {
		return nil
	}

	total := 0
	for _, it := range h.Items {
		total += it.Length
	}

	avg := BM25AvgLength(total, len(h.Items))
	df := make(map[string]int, len(terms))

	for _, it := range h.Items {
		for _, t := range terms {
			if it.Terms[t] > 0 {
				df[t]++
			}
		}
	}

	type scored struct {
		score float64
		files []string
	}

	var matched []scored

	for _, it := range h.Items {
		s := 0.0

		for _, t := range terms {
			if tf := it.Terms[t]; tf > 0 {
				s += BM25IDF(len(h.Items), df[t]) * BM25TermScore(tf, it.Length, avg)
			}
		}

		if s > 0 {
			matched = append(matched, scored{s * ageDecay(h.AsOf, it.Time), it.Files})
		}
	}

	slices.SortStableFunc(matched, func(a, b scored) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		default:
			return 0
		}
	})

	byFile := map[string]float64{}

	for _, m := range matched[:min(pastTopItems, len(matched))] {
		share := m.score / math.Log2(1+float64(len(m.files)))

		for _, f := range m.files {
			if include(f) {
				byFile[f] += share
			}
		}
	}

	return pastHits(byFile, pastWorkSimilar)
}

func pastHits(byFile map[string]float64, source string) []LexicalHit {
	hits := make([]LexicalHit, 0, len(byFile))
	for f, s := range byFile {
		hits = append(hits, LexicalHit{Path: f, Score: s, PastWork: source})
	}

	sortHits(hits)

	return hits
}

type couplePair struct{ seed, file string }

// rankCoChange ranks files by logical coupling with the seeds: the cosine of
// their age-decayed commit sets, so a file that changes in every commit (a
// registry, a CMake list) is discounted by its own change count. A pair needs
// coChangeMinSupport shared commits. Seeds weigh 1/(rrfK+rank), as in RRF.
func rankCoChange(h ChangeHistory, seeds []LexicalHit, include func(string) bool) []LexicalHit {
	seedWeight := map[string]float64{}

	for rank, s := range seeds[:min(coChangeSeedCount, len(seeds))] {
		seedWeight[s.Path] = 1 / float64(rrfK+rank+1)
	}

	if len(seedWeight) == 0 || len(h.Commits) == 0 {
		return nil
	}

	changes := map[string]float64{} // decayed commits per file
	shared := map[couplePair]float64{}
	support := map[couplePair]int{}

	for _, c := range h.Commits {
		d := ageDecay(h.AsOf, c.Time)

		var inCommit []string

		for _, f := range c.Files {
			changes[f] += d

			if _, ok := seedWeight[f]; ok {
				inCommit = append(inCommit, f)
			}
		}

		for _, s := range inCommit {
			for _, f := range c.Files {
				if f != s {
					p := couplePair{s, f}
					shared[p] += d
					support[p]++
				}
			}
		}
	}

	byFile := map[string]float64{}

	for p, n := range shared {
		if support[p] < coChangeMinSupport {
			continue
		}

		if _, isSeed := seedWeight[p.file]; isSeed || !include(p.file) {
			continue
		}

		byFile[p.file] += seedWeight[p.seed] * n / math.Sqrt(changes[p.seed]*changes[p.file])
	}

	return pastHits(byFile, pastWorkCoChange)
}
