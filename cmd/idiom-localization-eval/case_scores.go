package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

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

	rows := [][]string{eval.LocalizationCaseScoreColumns}

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

// compareScores judges a candidate against a baseline on per-case score
// files: paired per case on one split, with wins, losses, ties and an exact
// two-sided sign test per metric. Each side may join several machines' files.
func compareScores(args []string) error {
	fs := flag.NewFlagSet("compare-scores", flag.ExitOnError)
	before := fs.String("before", "", "baseline score CSVs, comma-separated")
	after := fs.String("after", "", "candidate score CSVs, comma-separated")
	split := fs.String("split", "holdout", "split to judge")
	_ = fs.Parse(args)

	if *before == "" || *after == "" {
		return fmt.Errorf("-before and -after are required")
	}

	b, err := eval.ReadLocalizationCaseScores(strings.Split(*before, ","))
	if err != nil {
		return err
	}

	a, err := eval.ReadLocalizationCaseScores(strings.Split(*after, ","))
	if err != nil {
		return err
	}

	cmp, err := eval.CompareLocalizationCaseScores(b, a, *split)
	if err != nil {
		return err
	}

	fmt.Printf("split %s, %d paired cases\n", cmp.Split, cmp.Cases)

	for _, m := range cmp.Metrics {
		fmt.Printf("%-9s before %.3f after %.3f  wins %d losses %d ties %d  sign p (two-sided) %.4f\n",
			m.Name, m.BeforeMean, m.AfterMean, m.Wins, m.Losses, m.Ties, m.SignPTwoSided)
	}

	return nil
}
