package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

const metricTolerance = 1e-9

var _ = ginkgo.Describe("localization eval metrics", func() {
	gold := []string{"a/b/x.go", "a/b/y.go", "c/d/z.go"}

	ginkgo.It("scores a fixed ranking by hand-computed values", func() {
		ranked := []string{"a/b/x.go", "n/one.go", "c/d/z.go", "a/b/y.go"}
		m, ok := ComputeLocalizationEvalMetrics(ranked, gold, 8)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(m.RecallRaw).To(gomega.Equal(1.0))
		gomega.Expect(m.RecallCapped).To(gomega.Equal(1.0))
		gomega.Expect(m.MRR).To(gomega.Equal(1.0))
		gomega.Expect(m.NDCG).To(gomega.BeNumerically("~", 0.9060254355346823, metricTolerance))
		gomega.Expect(m.AlphaNDCG).To(gomega.BeNumerically("~", 0.9119629671334931, metricTolerance))
		gomega.Expect(m.SubtopicRecall).To(gomega.Equal(1.0))
		gomega.Expect(m.DistinctDirs).To(gomega.Equal(3))
	})

	ginkgo.It("caps recall at min(k, gold) and keeps raw recall", func() {
		var big, ranked []string
		for i := range 20 {
			big = append(big, fmt.Sprintf("d/f%02d.go", i))
		}

		ranked = append(ranked, big[:8]...)
		m, _ := ComputeLocalizationEvalMetrics(ranked, big, 8)
		gomega.Expect(m.RecallRaw).To(gomega.BeNumerically("~", 0.4, metricTolerance))
		gomega.Expect(m.RecallCapped).To(gomega.Equal(1.0))
	})

	ginkgo.It("counts each file of a collapsed pair as one shown file and ignores duplicates", func() {
		ranked := []string{"core/foo.h", "core/foo.cpp", "core/foo.cpp", "x/y.go"}
		m, _ := ComputeLocalizationEvalMetrics(ranked, []string{"core/foo.cpp"}, 2)
		gomega.Expect(m.FilesShown).To(gomega.Equal(2))
		gomega.Expect(m.MRR).To(gomega.Equal(0.5))
		gomega.Expect(m.RecallCapped).To(gomega.Equal(1.0))
	})

	ginkgo.It("scores zero for a miss and reports unscoreable empty gold", func() {
		m, ok := ComputeLocalizationEvalMetrics([]string{"n/a.go"}, gold, 8)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(m.RecallCapped + m.MRR + m.NDCG + m.AlphaNDCG + m.SubtopicRecall).To(gomega.Equal(0.0))

		_, ok = ComputeLocalizationEvalMetrics([]string{"n/a.go"}, nil, 8)
		gomega.Expect(ok).To(gomega.BeFalse())
	})

	ginkgo.It("rewards subtopic coverage over redundant same-directory hits in alpha-nDCG", func() {
		spread, _ := ComputeLocalizationEvalMetrics([]string{"a/b/x.go", "c/d/z.go"}, gold, 8)
		redundant, _ := ComputeLocalizationEvalMetrics([]string{"a/b/x.go", "a/b/y.go"}, gold, 8)
		gomega.Expect(spread.AlphaNDCG).To(gomega.BeNumerically(">", redundant.AlphaNDCG))
		gomega.Expect(spread.SubtopicRecall).To(gomega.BeNumerically(">", redundant.SubtopicRecall))
	})

	ginkgo.It("builds the greedy ideal over fewer gold files than positions", func() {
		m, _ := ComputeLocalizationEvalMetrics([]string{"a/x.go"}, []string{"a/x.go"}, 8)
		gomega.Expect(m.AlphaNDCG).To(gomega.Equal(1.0))
		gomega.Expect(m.NDCG).To(gomega.Equal(1.0))
	})

	ginkgo.It("maps files to two-level subtopics", func() {
		gomega.Expect(LocalizationEvalDir2("core/world/day_clock.cpp")).To(gomega.Equal("core/world"))
		gomega.Expect(LocalizationEvalDir2("core/world/constants/x.h")).To(gomega.Equal("core/world"))
		gomega.Expect(LocalizationEvalDir2("client/session.cpp")).To(gomega.Equal("client"))
		gomega.Expect(LocalizationEvalDir2("go.mod")).To(gomega.Equal("."))
	})
})

var _ = ginkgo.Describe("localization eval split and gates", func() {
	ginkgo.It("keeps the hash split stable and about one third hold-out", func() {
		gomega.Expect(LocalizationEvalSplit("a")).To(gomega.Equal("holdout"))
		gomega.Expect(LocalizationEvalSplit("e")).To(gomega.Equal("dev"))
		gomega.Expect(LocalizationEvalSplit("f")).To(gomega.Equal("dev"))

		holdout := 0

		for i := range 3000 {
			id := fmt.Sprintf("merge-%d", i)
			got := LocalizationEvalSplit(id)
			gomega.Expect(LocalizationEvalSplit(id)).To(gomega.Equal(got))

			if got == "holdout" {
				holdout++
			}
		}

		gomega.Expect(holdout).To(gomega.BeNumerically("~", 1000, 100))
	})

	ginkgo.It("computes the exact one-sided sign test", func() {
		gomega.Expect(LocalizationEvalSignTestP(5, 0)).To(gomega.BeNumerically("~", 1.0/32, metricTolerance))
		gomega.Expect(LocalizationEvalSignTestP(4, 0)).To(gomega.BeNumerically("~", 1.0/16, metricTolerance))
		gomega.Expect(LocalizationEvalSignTestP(10, 5)).To(gomega.BeNumerically("~", 0.15087890625, metricTolerance))
		gomega.Expect(LocalizationEvalSignTestP(0, 0)).To(gomega.Equal(1.0))
	})

	ginkgo.It("gives a deterministic bootstrap bound that tracks the sample", func() {
		positive := repeatDeltas(30, 0.1)
		a := LocalizationEvalBootstrapLowerBound(positive, 0.95)
		gomega.Expect(LocalizationEvalBootstrapLowerBound(positive, 0.95)).To(gomega.Equal(a))
		gomega.Expect(a).To(gomega.BeNumerically("~", 0.1, metricTolerance))

		mixed := append(repeatDeltas(15, 0.2), repeatDeltas(15, -0.2)...)
		gomega.Expect(LocalizationEvalBootstrapLowerBound(mixed, 0.95)).To(gomega.BeNumerically("<", 0))
		gomega.Expect(LocalizationEvalBootstrapLowerBound(mixed, 0.992)).To(gomega.BeNumerically("<=", LocalizationEvalBootstrapLowerBound(mixed, 0.95)))
	})

	spec := LocalizationEvalGateSpec{Metric: "recall_capped", Confidence: LocalizationEvalDevConfidence}

	ginkgo.It("passes a clear gain and fails a no-gain set on the lower bound", func() {
		gain := wrapDeltas(append(repeatDeltas(20, 0.15), repeatDeltas(10, 0)...))
		v := EvaluateLocalizationEvalGate(spec, gain)
		gomega.Expect(v.Pass).To(gomega.BeTrue(), "%v", v.Reasons)
		gomega.Expect(v.Method).To(gomega.Equal("bootstrap"))
		gomega.Expect([]int{v.Wins, v.Losses, v.Ties}).To(gomega.Equal([]int{20, 0, 10}))

		flat := EvaluateLocalizationEvalGate(spec, wrapDeltas(append(repeatDeltas(15, 0.1), repeatDeltas(15, -0.1)...)))
		gomega.Expect(flat.Pass).To(gomega.BeFalse())
	})

	ginkgo.It("fails when losses exceed wins or one case drops past the limit", func() {
		losses := wrapDeltas(append(repeatDeltas(10, 0.3), repeatDeltas(12, -0.05)...))
		gomega.Expect(EvaluateLocalizationEvalGate(spec, losses).Pass).To(gomega.BeFalse())

		drop := wrapDeltas(append(repeatDeltas(29, 0.1), -0.3))
		v := EvaluateLocalizationEvalGate(spec, drop)
		gomega.Expect(v.Pass).To(gomega.BeFalse())
		gomega.Expect(v.WorstDrop).To(gomega.BeNumerically("~", 0.3, metricTolerance))
		gomega.Expect(strings.Join(v.Reasons, ";")).To(gomega.ContainSubstring("dropped"))
	})

	ginkgo.It("uses the sign test below the minimum subset size", func() {
		v := EvaluateLocalizationEvalGate(spec, wrapDeltas(repeatDeltas(6, 0.2)))
		gomega.Expect(v.Method).To(gomega.Equal("sign_test"))
		gomega.Expect(v.Pass).To(gomega.BeTrue(), "%v", v.Reasons)

		small := EvaluateLocalizationEvalGate(spec, wrapDeltas(repeatDeltas(3, 0.2)))
		gomega.Expect(small.Pass).To(gomega.BeFalse())
		gomega.Expect(EvaluateLocalizationEvalGate(spec, nil).Pass).To(gomega.BeFalse())
	})

	ginkgo.It("pairs two runs by case id and skips errored cases", func() {
		mk := func(id, split string, recall float64, errText string) LocalizationEvalCaseResult {
			return LocalizationEvalCaseResult{ID: id, Split: split, Error: errText, Metrics8: LocalizationEvalCaseMetrics{RecallCapped: recall}}
		}
		before := LocalizationEvalRunResult{Cases: []LocalizationEvalCaseResult{mk("a", "dev", 0.2, ""), mk("b", "dev", 0.5, ""), mk("c", "holdout", 0.1, "")}}
		after := LocalizationEvalRunResult{Cases: []LocalizationEvalCaseResult{mk("a", "dev", 0.6, ""), mk("b", "dev", 0.5, "boom"), mk("c", "holdout", 0.0, "")}}

		dev := PairLocalizationEvalDeltas(before, after, "recall_capped", "dev")
		gomega.Expect(dev).To(gomega.HaveLen(1))
		gomega.Expect(dev[0].Delta).To(gomega.BeNumerically("~", 0.4, metricTolerance))
		gomega.Expect(PairLocalizationEvalDeltas(before, after, "recall_capped", "all")).To(gomega.HaveLen(2))
	})

	ginkgo.It("summarizes by split", func() {
		cases := []LocalizationEvalCaseResult{
			{ID: "a", Split: "dev", Deterministic: true, Metrics8: LocalizationEvalCaseMetrics{RecallCapped: 1}},
			{ID: "b", Split: "dev", Deterministic: true, Metrics8: LocalizationEvalCaseMetrics{RecallCapped: 0.5}},
			{ID: "c", Split: "holdout", Error: "x"},
		}
		sums := SummarizeLocalizationEval(cases)
		gomega.Expect(sums[0].Split).To(gomega.Equal("dev"))
		gomega.Expect(sums[0].RecallCapped8).To(gomega.BeNumerically("~", 0.75, metricTolerance))
		gomega.Expect(sums[1].Errors).To(gomega.Equal(1))
	})
})

func repeatDeltas(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}

	return out
}

func wrapDeltas(values []float64) []LocalizationEvalDelta {
	out := make([]LocalizationEvalDelta, len(values))
	for i, v := range values {
		out[i] = LocalizationEvalDelta{ID: fmt.Sprintf("c%02d", i), Delta: v}
	}

	return out
}

var _ = ginkgo.Describe("localization eval case builder", func() {
	runGit := func(dir string, args ...string) string {
		ginkgo.GinkgoHelper()

		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), string(out))

		return string(out)
	}
	write := func(dir, rel, content string) {
		ginkgo.GinkgoHelper()
		gomega.Expect(os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644)).To(gomega.Succeed())
	}

	ginkgo.It("takes the query from the ledger at the base commit and separates gold kinds", func() {
		repo := ginkgo.GinkgoT().TempDir()
		runGit(repo, "init", "-q", "-b", "main")
		write(repo, "docs/ledger.md", "| ID | Work |\n| G9 | Wobble the widget at dusk | x |\n")
		write(repo, "src/a.cpp", "a\n")
		write(repo, "src/gone.cpp", "g\n")
		write(repo, "tests/a_test.cpp", "t\n")
		runGit(repo, "add", "-A")
		runGit(repo, "commit", "-q", "-m", "base")
		runGit(repo, "checkout", "-q", "-b", "work")
		write(repo, "src/a.cpp", "a2\n")
		write(repo, "src/new.cpp", "n\n")
		write(repo, "tests/a_test.cpp", "t2\n")
		write(repo, "docs/ledger.md", "| ID | Work |\n| G9 | CHANGED AFTER BASE | x |\n")
		write(repo, "docs/note.md", "n\n")
		runGit(repo, "rm", "-q", "src/gone.cpp")
		runGit(repo, "add", "-A")
		runGit(repo, "commit", "-q", "-m", "work")
		runGit(repo, "checkout", "-q", "main")
		write(repo, "other.txt", "o\n")
		runGit(repo, "add", "-A")
		runGit(repo, "commit", "-q", "-m", "diverge")
		runGit(repo, "merge", "-q", "--no-ff", "-m", "merge: G9 wobble", "work")
		runGit(repo, "checkout", "-q", "-b", "norow", "HEAD~1")
		write(repo, "src/b.cpp", "b\n")
		runGit(repo, "add", "-A")
		runGit(repo, "commit", "-q", "-m", "x")
		runGit(repo, "checkout", "-q", "main")
		runGit(repo, "merge", "-q", "--no-ff", "-m", "merge: G77 no such row", "norow")

		cfg := LocalizationEvalBuilderConfig{
			Commits: "merges", QueryFrom: "ledger", LedgerPaths: []string{"docs/ledger.md"},
			SubjectIDRegex: `^merge: ([A-Z]+[0-9]+)\b`, LedgerRowRegex: `(?m)^\| %s \| ([^|]+) \|`,
			SourceRegex: `\.cpp$`, TestRegex: `(^|/)tests?/`, DocRegex: `\.md$`, MinQueryLen: 5,
		}
		cases, err := BuildLocalizationEvalCases(repo, cfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cases).To(gomega.HaveLen(1), "the merge without a ledger row is dropped")

		c := cases[0]
		gomega.Expect(c.Query).To(gomega.Equal("Wobble the widget at dusk"), "query is the base text, not the later edit")
		gomega.Expect(c.Gold).To(gomega.Equal([]string{"src/a.cpp", "src/gone.cpp"}))
		gomega.Expect(c.GoldNew).To(gomega.Equal([]string{"src/new.cpp"}))
		gomega.Expect(c.GoldTests).To(gomega.Equal([]string{"tests/a_test.cpp"}))
		gomega.Expect(c.GoldDocs).To(gomega.ConsistOf("docs/ledger.md", "docs/note.md"))
		gomega.Expect(c.Split).To(gomega.Equal(LocalizationEvalSplit(c.Merge)))
	})

	ginkgo.It("rejects a case file whose split does not match the hash", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "cases.json")
		bad := `{"schema":1,"corpus":"x","cases":[{"id":"a","merge":"a","base":"b","query":"q","gold":["f"],"split":"` + otherSplit(LocalizationEvalSplit("a")) + `"}]}`
		gomega.Expect(os.WriteFile(path, []byte(bad), 0o644)).To(gomega.Succeed())
		_, err := ReadLocalizationEvalCaseFile(path)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("split")))
	})

	ginkgo.It("ships case files that validate and meet the size floors", func() {
		bentech, err := ReadLocalizationEvalCaseFile(evalTestdataPath("localization", "eval", "bentech-cases.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(len(bentech.Cases)).To(gomega.BeNumerically(">=", 60))

		idiom, err := ReadLocalizationEvalCaseFile(evalTestdataPath("localization", "eval", "idiom-cases.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(len(idiom.Cases)).To(gomega.BeNumerically(">=", 30))
	})
})

func otherSplit(s string) string {
	if s == "dev" {
		return "holdout"
	}

	return "dev"
}
