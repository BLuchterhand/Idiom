package eval

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("BM25 field scoring", func() {
	ginkgo.It("prefers the rare term match", func() {
		query := []string{"slot", "queue"}
		set := map[string]struct{}{"slot": {}, "queue": {}}
		f := newBM25Field(3)
		f.add(0, []string{"slot", "admission"}, set)
		f.add(1, []string{"queue", "queue", "other"}, set)
		f.add(2, []string{"queue", "unrelated"}, set)

		s := f.scores(query)
		gomega.Expect(s).To(gomega.HaveLen(3))
		gomega.Expect(s[0]).To(gomega.BeNumerically(">", s[1]), "scores = %v", s)
		gomega.Expect(s[1]).To(gomega.BeNumerically(">", s[2]), "scores = %v", s)
		gomega.Expect(s[2]).To(gomega.BeNumerically(">", 0), "scores = %v", s)
	})
})
