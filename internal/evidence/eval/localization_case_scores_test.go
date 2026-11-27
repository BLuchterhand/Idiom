package eval

import (
	"os"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("localization per-case score comparison", func() {
	write := func(dir, name, body string) string {
		p := filepath.Join(dir, name)
		gomega.Expect(os.WriteFile(p, []byte(body), 0o600)).To(gomega.Succeed())

		return p
	}
	header := "id,split,recall@8,hit@8,mrr@8,timestamp\n"

	ginkgo.It("joins machines, pairs by id and counts wins, losses and ties per metric", func() {
		dir := ginkgo.GinkgoT().TempDir()
		pcBefore := write(dir, "pc0.csv", header+"a,holdout,0.5,1,0.5,t\nb,holdout,0,0,0,t\nd,dev,1,1,1,t\n")
		macBefore := write(dir, "mac0.csv", header+"c,holdout,1,1,1,t\n")
		pcAfter := write(dir, "pc1.csv", header+"a,holdout,1,1,1,t\nb,holdout,0,0,0,t\nd,dev,0,0,0,t\n")
		macAfter := write(dir, "mac1.csv", header+"c,holdout,0.5,1,0.25,t\n")

		before, err := ReadLocalizationCaseScores([]string{pcBefore, macBefore})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		after, err := ReadLocalizationCaseScores([]string{pcAfter, macAfter})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		cmp, err := CompareLocalizationCaseScores(before, after, "holdout")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cmp.Cases).To(gomega.Equal(3))

		recall := cmp.Metrics[0]
		gomega.Expect([]int{recall.Wins, recall.Losses, recall.Ties}).To(gomega.Equal([]int{1, 1, 1}))
		gomega.Expect(recall.BeforeMean).To(gomega.BeNumerically("~", 0.5, metricTolerance))
		gomega.Expect(recall.AfterMean).To(gomega.BeNumerically("~", 0.5, metricTolerance))
		gomega.Expect(recall.SignPTwoSided).To(gomega.Equal(1.0))

		hit := cmp.Metrics[1]
		gomega.Expect([]int{hit.Wins, hit.Losses, hit.Ties}).To(gomega.Equal([]int{0, 0, 3}))
	})

	ginkgo.It("refuses an id on two machines and a pairing with missing cases", func() {
		dir := ginkgo.GinkgoT().TempDir()
		one := write(dir, "one.csv", header+"a,holdout,1,1,1,t\n")
		two := write(dir, "two.csv", header+"a,holdout,0,0,0,t\n")
		_, err := ReadLocalizationCaseScores([]string{one, two})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("more than one score file")))

		before, _ := ReadLocalizationCaseScores([]string{one})
		after, _ := ReadLocalizationCaseScores([]string{write(dir, "b.csv", header+"b,holdout,1,1,1,t\n")})
		_, err = CompareLocalizationCaseScores(before, after, "holdout")
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("gives exact two-sided sign test p-values", func() {
		gomega.Expect(LocalizationEvalSignTestTwoSidedP(0, 0)).To(gomega.Equal(1.0))
		// 9 wins, 1 loss: 2 * P(X >= 9 | n=10) = 2 * 11/1024.
		gomega.Expect(LocalizationEvalSignTestTwoSidedP(9, 1)).To(gomega.BeNumerically("~", 22.0/1024, metricTolerance))
		gomega.Expect(LocalizationEvalSignTestTwoSidedP(1, 9)).To(gomega.BeNumerically("~", 22.0/1024, metricTolerance))
		gomega.Expect(LocalizationEvalSignTestTwoSidedP(5, 5)).To(gomega.Equal(1.0))
	})
})
