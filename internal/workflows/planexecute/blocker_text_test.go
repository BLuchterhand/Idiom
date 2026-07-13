package planexecute

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"strings"
)

var _ = ginkgo.It("plan execute blocker summary prefers explicit question over preamble", func() {
	answer := strings.Join([]string{
		"I now have all the evidence I need. Let me analyze what's happening.",
		"",
		"The implementation needs a header declaration that is not in repair scope.",
		"<<QUESTION>>: required header changes are outside the allowed repair scope.",
	}, "\n")

	got := planExecuteBlockerSummary(answer)
	want := "<<QUESTION>>: required header changes are outside the allowed repair scope."
	gomega.Expect(got != want).To(gomega.BeFalse(), fmt.Sprintf("summary = %q, want %q", got, want))
})

var _ = ginkgo.It("plan execute blocker summary prefers blocker line over preamble", func() {
	answer := "Analysis complete.\nBlocker: source refresh still required for native/src/core/colony_sim_tick.cpp"

	got := planExecuteBlockerSummary(answer)
	want := "Blocker: source refresh still required for native/src/core/colony_sim_tick.cpp"
	gomega.Expect(got != want).To(gomega.BeFalse(), fmt.Sprintf("summary = %q, want %q", got, want))
})
