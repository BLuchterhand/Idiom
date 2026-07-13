package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan-execute failure kinds", func() {
	It("parses every known value exactly once", func() {
		seen := map[PlanExecuteFailureKind]struct{}{}
		for _, kind := range PlanExecuteFailureKinds() {
			Expect(seen).NotTo(HaveKey(kind))
			seen[kind] = struct{}{}
			parsed, ok := ParsePlanExecuteFailureKind(string(kind))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(kind))
		}
	})

	It("accepts legacy hyphenation while rejecting unknown kinds", func() {
		parsed, ok := ParsePlanExecuteFailureKind("missing-requested-test-edit")
		Expect(ok).To(BeTrue())
		Expect(parsed).To(Equal(PlanExecuteFailureMissingRequestedTestEdit))
		parsed, ok = ParsePlanExecuteFailureKind("review_repair_blocked")
		Expect(ok).To(BeTrue())
		Expect(parsed).To(Equal(PlanExecuteFailureReviewRepairBlocked))
		_, ok = ParsePlanExecuteFailureKind("review-repair-typo")
		Expect(ok).To(BeFalse())
	})

	It("covers every declared constant", func() {
		_, filename, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue())
		body, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "failure_kinds.go"))
		Expect(err).NotTo(HaveOccurred())

		declared := map[PlanExecuteFailureKind]string{}
		declarationPattern := regexp.MustCompile(`(?m)(PlanExecuteFailure[A-Za-z0-9]+)\s+PlanExecuteFailureKind\s*=\s*"([^"]+)"`)
		for _, match := range declarationPattern.FindAllStringSubmatch(string(body), -1) {
			declared[PlanExecuteFailureKind(match[2])] = match[1]
		}
		Expect(declared).NotTo(BeEmpty())

		known := map[PlanExecuteFailureKind]struct{}{}
		for _, kind := range PlanExecuteFailureKinds() {
			known[kind] = struct{}{}
		}
		for kind := range declared {
			Expect(known).To(HaveKey(kind))
		}
	})
})
