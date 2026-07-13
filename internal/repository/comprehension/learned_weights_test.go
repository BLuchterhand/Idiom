package comprehension

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = It("learned weights round trip", func() {
	root := GinkgoT().TempDir()
	{
		_, _, err := ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"internal/runtime/agent/runner.go", "internal/helper.go"}, "", "")
		Expect(err).NotTo(HaveOccurred(), "update: %v", err)
	}

	got, err := LoadLearnedWeights(GinkgoT().Context(), root)
	Expect(err).NotTo(HaveOccurred(), "load: %v", err)
	Expect(math.Abs(got["internal/runtime/agent/runner.go"]-1.15) > 1e-6).To(BeFalse(), "boost result = %v, want 1.15", got["internal/runtime/agent/runner.go"])
	Expect(math.Abs(got["internal/helper.go"]-1.15) > 1e-6).To(BeFalse(), "boost result = %v, want 1.15", got["internal/helper.go"])
})
var _ = It("learned weights clamp to bounds", func() {
	root := GinkgoT().TempDir()
	for i := 0; i < 100; i++ {
		_, _, _ = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"a.go"}, "", fmt.Sprintf("clamp-boost-%d", i))
	}

	weights, _ := LoadLearnedWeights(GinkgoT().Context(), root)
	Expect(weights["a.go"]).To(BeEquivalentTo(LearnedWeightMax), "excessive boost = %v, want clamp to %v", weights["a.go"], LearnedWeightMax)

	for i := 0; i < 200; i++ {
		_, _, _ = ApplyManualRankerNudge(GinkgoT().Context(), root, "penalize", []string{"a.go"}, "", fmt.Sprintf("clamp-penalty-%d", i))
	}

	weights, _ = LoadLearnedWeights(GinkgoT().Context(), root)
	Expect(weights["a.go"]).To(BeEquivalentTo(LearnedWeightMin), "excessive penalty = %v, want clamp to %v", weights["a.go"], LearnedWeightMin)
})
var _ = It("learned weights decay toward baseline", func() {
	first := applyLearnedWeightUpdate(nil, learnedWeightUpdate{boost: []string{"a.go"}, boostAmount: 1.0})
	final := first
	// Subsequent updates without any boost/penalty should decay the weight
	// back toward 1.0. (1.0 - baseline) * 0.95^80 ≈ 1.7e-2, well below the
	// 0.05 threshold.
	for i := 0; i < 80; i++ {
		final = applyLearnedWeightUpdate(final, learnedWeightUpdate{})
	}

	{
		val, ok := final["a.go"]
		if ok {
			Expect(val).To(BeNumerically("~", LearnedWeightBaseline, 0.05), "decayed weight = %v, want close to baseline; before decay = %v", val, first["a.go"])
		}
	}
})
var _ = It("learned weights rejects traversal keys", func() {
	got := applyLearnedWeightUpdate(nil, learnedWeightUpdate{boost: []string{"../escape.go", "/abs.go", " ", "safe/../escape.go", "safe/./escape.go", "safe//escape.go"}})
	Expect(len(got)).To(BeEquivalentTo(0), "traversal keys should have been dropped, got %v", got)
})
var _ = It("learned weights file persistence", func() {
	root := GinkgoT().TempDir()

	_, _, _ = ApplyManualRankerNudge(GinkgoT().Context(), root, "boost", []string{"a.go"}, "", "")

	target := filepath.Join(root, ".idiom", "ranker", "weights.json")
	{
		_, err := os.Stat(target)
		Expect(err).NotTo(HaveOccurred(), "weights file missing: %v", err)
	}

	_, err := ResetLearnedRankerWeights(GinkgoT().Context(), root, "")
	Expect(err).NotTo(HaveOccurred())
	_, err = os.Stat(target)
	Expect(os.IsNotExist(err)).To(BeTrue(), "baseline reset should durably remove weights.json")
})
