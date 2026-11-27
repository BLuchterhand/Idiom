package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"

	"idiom/internal/evidence/eval"
)

// caseScores writes one CSV row per scored case (id, split, recall@8, hit@8,
// mrr@8, query time) so runs from several machines combine by summing per
// case. It carries no query text: the rows can leave the machine that holds
// the transcripts.
func caseScores(args []string) error {
	fs := flag.NewFlagSet("case-scores", flag.ExitOnError)
	runPath := fs.String("run", "", "run result written by run")
	casesPath := fs.String("cases", "", "case file the run used (for query times)")
	out := fs.String("out", "", "CSV file to write")
	_ = fs.Parse(args)

	if *runPath == "" || *casesPath == "" || *out == "" {
		return fmt.Errorf("-run, -cases and -out are required")
	}

	result, err := readResult(*runPath)
	if err != nil {
		return err
	}

	file, err := eval.ReadLocalizationEvalCaseFile(*casesPath)
	if err != nil {
		return err
	}

	times := map[string]string{}
	for _, c := range file.Cases {
		times[c.ID] = c.Time
	}

	rows := [][]string{{"id", "split", "recall@8", "hit@8", "mrr@8", "timestamp"}}

	for _, c := range result.Cases {
		if c.Error != "" {
			return fmt.Errorf("case %s did not score: %s", c.ID, c.Error)
		}

		m := c.Metrics8
		rows = append(rows, []string{c.ID, c.Split, formatScore(m.RecallCapped), formatScore(m.Hit), formatScore(m.MRR), times[c.ID]})
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}

	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		f.Close()

		return err
	}

	return f.Close()
}

func formatScore(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
