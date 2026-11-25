package eval

import (
	"math"
	"slices"

	"idiom/internal/repository/comprehension"
)

// Localization eval metrics. The ranked list is the files an agent sees, in
// order and expanded (a collapsed header/implementation display pair counts as
// two files, each scored on its own); k counts files shown.

const (
	// LocalizationEvalGatedK is the k the phase gates are judged at.
	LocalizationEvalGatedK = 8
	// LocalizationEvalSecondaryK is the other reported cutoff.
	LocalizationEvalSecondaryK = 5
	// LocalizationEvalAlpha is the alpha-nDCG redundancy discount.
	LocalizationEvalAlpha = 0.5
)

// LocalizationEvalCaseMetrics are the per-case metrics at one k.
type LocalizationEvalCaseMetrics struct {
	K               int     `json:"k"`
	RecallRaw       float64 `json:"recall_raw"`
	RecallCapped    float64 `json:"recall_capped"`
	Hit             float64 `json:"hit"`
	MRR             float64 `json:"mrr"`
	NDCG            float64 `json:"ndcg"`
	SubtopicRecall  float64 `json:"subtopic_recall"`
	AlphaNDCG       float64 `json:"alpha_ndcg"`
	DistinctDirs    int     `json:"distinct_dirs"`
	GoldSubtopics   int     `json:"gold_subtopics"`
	GoldFiles       int     `json:"gold_files"`
	FilesShown      int     `json:"files_shown"`
	GoldFilesInTopK int     `json:"gold_files_in_top_k"`
}

// ComputeLocalizationEvalMetrics scores one ranked file list against gold at k.
// Duplicate ranked paths keep their first position. Returns ok=false when the
// gold set is empty (the case is not scoreable).
func ComputeLocalizationEvalMetrics(ranked []string, gold []string, k int) (LocalizationEvalCaseMetrics, bool) {
	goldSet := make(map[string]struct{}, len(gold))
	for _, g := range gold {
		goldSet[g] = struct{}{}
	}

	if len(goldSet) == 0 || k <= 0 {
		return LocalizationEvalCaseMetrics{}, false
	}

	top := uniqueFirstN(ranked, k)
	m := LocalizationEvalCaseMetrics{K: k, GoldFiles: len(goldSet), FilesShown: len(top)}

	dirs := map[string]struct{}{}
	for _, p := range top {
		dirs[comprehension.PathDir2(p)] = struct{}{}
	}

	m.DistinctDirs = len(dirs)

	subtopics := map[string]struct{}{}
	for g := range goldSet {
		subtopics[comprehension.PathDir2(g)] = struct{}{}
	}

	m.GoldSubtopics = len(subtopics)

	covered := map[string]struct{}{}
	dcg := 0.0

	for i, p := range top {
		if _, ok := goldSet[p]; !ok {
			continue
		}

		m.GoldFilesInTopK++
		if m.MRR == 0 {
			m.MRR = 1 / float64(i+1)
		}

		dcg += 1 / math.Log2(float64(i+2))
		covered[comprehension.PathDir2(p)] = struct{}{}
	}

	m.RecallRaw = float64(m.GoldFilesInTopK) / float64(len(goldSet))
	m.RecallCapped = float64(m.GoldFilesInTopK) / float64(min(k, len(goldSet)))

	if m.GoldFilesInTopK > 0 {
		m.Hit = 1
	}

	m.NDCG = dcg / idealBinaryDCG(min(k, len(goldSet)))
	m.SubtopicRecall = float64(len(covered)) / float64(len(subtopics))
	m.AlphaNDCG = alphaNDCG(top, goldSet, k)

	return m, true
}

func uniqueFirstN(ranked []string, k int) []string {
	seen := make(map[string]struct{}, k)
	out := make([]string, 0, k)

	for _, p := range ranked {
		if _, ok := seen[p]; ok {
			continue
		}

		seen[p] = struct{}{}

		out = append(out, p)
		if len(out) == k {
			break
		}
	}

	return out
}

func idealBinaryDCG(n int) float64 {
	sum := 0.0
	for i := range n {
		sum += 1 / math.Log2(float64(i+2))
	}

	return sum
}

// alphaNDCG scores subtopic coverage. Each gold file is one document with one
// subtopic (its Dir2); a gold file whose subtopic was already seen n times
// gains (1-alpha)^n. The ideal list is built greedily: at each position pick
// the unused gold file with the highest marginal gain given the subtopics
// already covered, ties by path. It has min(k, |gold|) positions.
func alphaNDCG(top []string, goldSet map[string]struct{}, k int) float64 {
	gain := func(seen map[string]int, path string) float64 {
		return math.Pow(1-LocalizationEvalAlpha, float64(seen[comprehension.PathDir2(path)]))
	}

	seen := map[string]int{}
	dcg := 0.0

	for i, p := range top {
		if _, ok := goldSet[p]; !ok {
			continue
		}

		dcg += gain(seen, p) / math.Log2(float64(i+2))
		seen[comprehension.PathDir2(p)]++
	}

	remaining := make([]string, 0, len(goldSet))
	for g := range goldSet {
		remaining = append(remaining, g)
	}

	slices.Sort(remaining)

	ideal := 0.0
	idealSeen := map[string]int{}

	for pos := 0; pos < k && len(remaining) > 0; pos++ {
		best := 0

		for j := 1; j < len(remaining); j++ {
			if gain(idealSeen, remaining[j]) > gain(idealSeen, remaining[best]) {
				best = j
			}
		}

		ideal += gain(idealSeen, remaining[best]) / math.Log2(float64(pos+2))
		idealSeen[comprehension.PathDir2(remaining[best])]++
		remaining = slices.Delete(remaining, best, best+1)
	}

	if ideal == 0 {
		return 0
	}

	return dcg / ideal
}

// LocalizationEvalMetricValue picks one named metric from a case result; the
// names are the JSON field names.
func LocalizationEvalMetricValue(m LocalizationEvalCaseMetrics, name string) float64 {
	switch name {
	case "recall_capped":
		return m.RecallCapped
	case "recall_raw":
		return m.RecallRaw
	case "hit":
		return m.Hit
	case "mrr":
		return m.MRR
	case "ndcg":
		return m.NDCG
	case "subtopic_recall":
		return m.SubtopicRecall
	case "alpha_ndcg":
		return m.AlphaNDCG
	default:
		return math.NaN()
	}
}
