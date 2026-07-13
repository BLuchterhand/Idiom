package jsoneventstest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JSON event test helpers", func() {
	It("decodes JSON lines and finds events by type", func() {
		events := DecodeLines(GinkgoT(), "noise\n{\"type\":\"idiom.test.one\",\"value\":1}\n{\"type\":\"idiom.plan_execute.executor_start\"}\n")

		Expect(events).To(HaveLen(2))
		_, found := FindByType(events, "idiom.test.one")
		Expect(found).To(BeTrue())
		_, found = FindByType(events, "idiom.test.missing")
		Expect(found).To(BeFalse())
	})
})
