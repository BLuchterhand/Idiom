package eval

import (
	"slices"
)

// LocalizationEvalSummary aggregates one split's cases: gated metrics are
// means over scored cases; diagnostics are reported and never gated.
type LocalizationEvalSummary struct {
	Split          string  `json:"split"`
	Cases          int     `json:"cases"`
	Errors         int     `json:"errors"`
	Deterministic  int     `json:"deterministic"`
	RecallCapped8  float64 `json:"recall_capped_k8"`
	RecallRaw8     float64 `json:"recall_raw_k8"`
	RecallCapped5  float64 `json:"recall_capped_k5"`
	Hit8           float64 `json:"hit_k8"`
	MRR8           float64 `json:"mrr_k8"`
	NDCG8          float64 `json:"ndcg_k8"`
	SubtopicRecall float64 `json:"subtopic_recall_k8"`
	AlphaNDCG8     float64 `json:"alpha_ndcg_k8"`
	MeanDirs8      float64 `json:"mean_distinct_dirs_k8"`
	MeanBytes      float64 `json:"mean_output_bytes"`
	LatencyP50MS   int64   `json:"latency_p50_ms,omitempty"`
	LatencyP95MS   int64   `json:"latency_p95_ms,omitempty"`
}

// SummarizeLocalizationEval summarizes by split ("dev", "holdout") and "all".
func SummarizeLocalizationEval(cases []LocalizationEvalCaseResult) []LocalizationEvalSummary {
	var out []LocalizationEvalSummary

	for _, split := range []string{"dev", "holdout", "all"} {
		s := LocalizationEvalSummary{Split: split}

		var latencies []int64

		for _, c := range cases {
			if split != "all" && c.Split != split {
				continue
			}

			if c.Error != "" {
				s.Errors++
				continue
			}

			s.Cases++

			if c.Deterministic {
				s.Deterministic++
			}

			s.RecallCapped8 += c.Metrics8.RecallCapped
			s.RecallRaw8 += c.Metrics8.RecallRaw
			s.RecallCapped5 += c.Metrics5.RecallCapped
			s.Hit8 += c.Metrics8.Hit
			s.MRR8 += c.Metrics8.MRR
			s.NDCG8 += c.Metrics8.NDCG
			s.SubtopicRecall += c.Metrics8.SubtopicRecall
			s.AlphaNDCG8 += c.Metrics8.AlphaNDCG
			s.MeanDirs8 += float64(c.Metrics8.DistinctDirs)
			s.MeanBytes += float64(c.OutputBytes)
			latencies = append(latencies, c.LatencyMS...)
		}

		if n := float64(s.Cases); n > 0 {
			for _, p := range []*float64{&s.RecallCapped8, &s.RecallRaw8, &s.RecallCapped5, &s.Hit8, &s.MRR8, &s.NDCG8, &s.SubtopicRecall, &s.AlphaNDCG8, &s.MeanDirs8, &s.MeanBytes} {
				*p /= n
			}
		}

		slices.Sort(latencies)

		if len(latencies) > 0 {
			s.LatencyP50MS = latencies[len(latencies)/2]
			s.LatencyP95MS = latencies[min(len(latencies)-1, len(latencies)*95/100)]
		}

		out = append(out, s)
	}

	return out
}

// PairLocalizationEvalDeltas pairs two runs by case id (cases scored in both,
// without error) and returns after-minus-before deltas of metric at k=8 for
// the split ("all" for every case).
func PairLocalizationEvalDeltas(before, after LocalizationEvalRunResult, metric, split string) []LocalizationEvalDelta {
	byID := map[string]LocalizationEvalCaseResult{}
	for _, c := range before.Cases {
		byID[c.ID] = c
	}

	var out []LocalizationEvalDelta

	for _, a := range after.Cases {
		b, ok := byID[a.ID]
		if !ok || a.Error != "" || b.Error != "" || (split != "all" && a.Split != split) {
			continue
		}

		out = append(out, LocalizationEvalDelta{ID: a.ID, Delta: LocalizationEvalMetricValue(a.Metrics8, metric) - LocalizationEvalMetricValue(b.Metrics8, metric)})
	}

	slices.SortFunc(out, func(a, b LocalizationEvalDelta) int {
		if a.ID < b.ID {
			return -1
		}

		return 1
	})

	return out
}
