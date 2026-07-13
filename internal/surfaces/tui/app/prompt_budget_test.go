package app

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("direct prompt budgets remain bounded in non interactive mode", func() {
	route := turnRoutePlan{Family: "conversation", Contract: "direct_answer"}

	gomega.Expect(shouldUnboundDirectPromptBudgets(true, route)).To(gomega.BeFalse(), fmt.Sprint("non-interactive prompt mode must honor configured budgets"))

	gomega.Expect(shouldUnboundDirectPromptBudgets(false, route)).To(gomega.BeTrue(), fmt.Sprint("interactive TUI conversation mode should retain unbounded investigation budgets"))
})
