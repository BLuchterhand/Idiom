package app

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("classify tui request does not use keyword mutation preflight", func() {
	got := classifyTUIRequest("Implement persistent input history.")
	gomega.Expect(got).To(gomega.Equal(tuiRequestClassConversation), fmt.Sprintf("class = %q, want conversation", got))
})

var _ = ginkgo.It("classify tui request honors read only constraint", func() {
	got := classifyTUIRequest("Just explain how we would implement persistent input history; do not edit files.")
	gomega.Expect(got).To(gomega.Equal(tuiRequestClassConversation), fmt.Sprintf("class = %q, want conversation", got))
})

var _ = ginkgo.It("classify tui request does not use plan execute keyword preflight", func() {
	got := classifyTUIRequest("Send this through plan-execute and implement the atom.")
	gomega.Expect(got).To(gomega.Equal(tuiRequestClassConversation), fmt.Sprintf("class = %q, want conversation", got))
})

var _ = ginkgo.It("update tui request status does not confirm keyword mutation", func() {
	m := &Model{}
	m.updateTUIRequestStatus("Implement persistent input history.")

	gomega.Expect(len(m.messages)).To(gomega.Equal(0), fmt.Sprintf("messages = %d, want no local confirmation messages", len(m.messages)))

	gomega.Expect(m.status).To(gomega.Equal("Request classified: conversation."), fmt.Sprintf("status = %q", m.status))
})
