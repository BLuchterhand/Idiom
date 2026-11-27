package eval

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
)

// LocalizationCaseScore is one row of a per-case score CSV (case-scores):
// id, split, recall@8, hit@8, mrr@8, timestamp. Several machines' files
// combine by id, so a usage corpus can be scored where its transcripts live.
type LocalizationCaseScore struct {
	ID     string
	Split  string
	Recall float64
	Hit    float64
	MRR    float64
	Time   string
}

// LocalizationCaseScoreColumns is the CSV header every score file carries.
var LocalizationCaseScoreColumns = []string{"id", "split", "recall@8", "hit@8", "mrr@8", "timestamp"}

// ReadLocalizationCaseScores reads and joins per-case score files. An id in
// two files is an error: the machines' case sets must not overlap.
func ReadLocalizationCaseScores(paths []string) (map[string]LocalizationCaseScore, error) {
	out := map[string]LocalizationCaseScore{}

	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}

		rows, err := readCaseScoreRows(f)
		f.Close()

		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}

		for _, s := range rows {
			if _, dup := out[s.ID]; dup {
				return nil, fmt.Errorf("%s: case %s appears in more than one score file", p, s.ID)
			}

			out[s.ID] = s
		}
	}

	return out, nil
}

func readCaseScoreRows(r io.Reader) ([]LocalizationCaseScore, error) {
	records, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) == 0 || !slices.Equal(records[0], LocalizationCaseScoreColumns) {
		return nil, fmt.Errorf("header is not %v", LocalizationCaseScoreColumns)
	}

	rows := make([]LocalizationCaseScore, 0, len(records)-1)

	for _, rec := range records[1:] {
		s := LocalizationCaseScore{ID: rec[0], Split: rec[1], Time: rec[5]}

		for i, dst := range []*float64{&s.Recall, &s.Hit, &s.MRR} {
			if *dst, err = strconv.ParseFloat(rec[2+i], 64); err != nil {
				return nil, fmt.Errorf("case %s column %s: %w", s.ID, LocalizationCaseScoreColumns[2+i], err)
			}
		}

		rows = append(rows, s)
	}

	return rows, nil
}

// LocalizationPairedMetric is one metric's paired result over a split.
type LocalizationPairedMetric struct {
	Name               string
	BeforeMean         float64
	AfterMean          float64
	Wins, Losses, Ties int
	SignPTwoSided      float64
}

// LocalizationPairedComparison is the paired per-case judgement of one split.
type LocalizationPairedComparison struct {
	Split   string
	Cases   int
	Metrics []LocalizationPairedMetric
}

// CompareLocalizationCaseScores pairs before and after by case id over one
// split. Both sides must hold exactly the same cases of that split: a case
// scored on one side only would make the pairing silently partial.
func CompareLocalizationCaseScores(before, after map[string]LocalizationCaseScore, split string) (LocalizationPairedComparison, error) {
	ids := caseIDsOfSplit(before, split)
	if afterIDs := caseIDsOfSplit(after, split); !slices.Equal(ids, afterIDs) {
		return LocalizationPairedComparison{}, fmt.Errorf("split %s: before has %d cases, after %d, and the ids differ", split, len(ids), len(afterIDs))
	}

	cmp := LocalizationPairedComparison{Split: split, Cases: len(ids)}
	metrics := []struct {
		name string
		get  func(LocalizationCaseScore) float64
	}{
		{"recall@8", func(s LocalizationCaseScore) float64 { return s.Recall }},
		{"hit@8", func(s LocalizationCaseScore) float64 { return s.Hit }},
		{"mrr@8", func(s LocalizationCaseScore) float64 { return s.MRR }},
	}

	for _, m := range metrics {
		pm := LocalizationPairedMetric{Name: m.name}

		for _, id := range ids {
			b, a := m.get(before[id]), m.get(after[id])
			pm.BeforeMean += b
			pm.AfterMean += a

			switch {
			case a > b:
				pm.Wins++
			case a < b:
				pm.Losses++
			default:
				pm.Ties++
			}
		}

		if len(ids) > 0 {
			pm.BeforeMean /= float64(len(ids))
			pm.AfterMean /= float64(len(ids))
		}

		pm.SignPTwoSided = LocalizationEvalSignTestTwoSidedP(pm.Wins, pm.Losses)
		cmp.Metrics = append(cmp.Metrics, pm)
	}

	return cmp, nil
}

func caseIDsOfSplit(scores map[string]LocalizationCaseScore, split string) []string {
	var ids []string

	for id, s := range scores {
		if s.Split == split {
			ids = append(ids, id)
		}
	}

	slices.Sort(ids)

	return ids
}
