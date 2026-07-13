package intentloop

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Intent loop vocabulary", func() {
	It("round-trips every declared event type without duplicates", func() {
		seen := map[EventType]struct{}{}
		for _, eventType := range EventTypes() {
			_, duplicate := seen[eventType]
			Expect(duplicate).To(BeFalse(), "duplicate EventTypes entry %q", eventType)
			seen[eventType] = struct{}{}

			Expect(EventName(eventType)).To(Equal(EventPrefix + string(eventType)))
			Expect(KnownEventType(eventType)).To(BeTrue())
		}
	})

	DescribeTable("classifies terminal statuses and their exit codes",
		func(status Status, terminal bool, exitCode int) {
			Expect(StatusTerminal(status)).To(Equal(terminal))
			Expect(TerminalExitCode(status)).To(Equal(exitCode))
		},
		Entry("running", StatusRunning, false, 1),
		Entry("completed", StatusCompleted, true, 0),
		Entry("blocked", StatusBlocked, true, 1),
		Entry("no progress", StatusNoProgress, true, 1),
		Entry("budget exhausted", StatusBudgetExhausted, true, 1),
		Entry("maximum cycles exceeded", StatusMaxCyclesExceeded, true, 1),
		Entry("failed", StatusFailed, true, 1),
	)

	It("parses every declared intent-loop status and rejects unknown values", func() {
		for _, status := range Statuses() {
			parsed, ok := ParseStatus(string(status))
			Expect(ok).To(BeTrue(), "status %q should parse", status)
			Expect(parsed).To(Equal(status))
		}

		for _, raw := range []string{"", "completed with notes", "running-ish", "not_real"} {
			_, ok := ParseStatus(raw)
			Expect(ok).To(BeFalse(), "status %q should be rejected", raw)
		}
	})

	It("parses every declared cycle status and rejects unknown values", func() {
		for _, status := range CycleStatuses() {
			parsed, ok := ParseCycleStatus(string(status))
			Expect(ok).To(BeTrue(), "cycle status %q should parse", status)
			Expect(parsed).To(Equal(status))
		}

		for _, raw := range []string{"", "completed with notes", "dispatching", "not_real"} {
			_, ok := ParseCycleStatus(raw)
			Expect(ok).To(BeFalse(), "cycle status %q should be rejected", raw)
		}
	})

	It("trims vocabulary strings while keeping typed event values exact", func() {
		status, ok := ParseStatus(" \t" + string(StatusCompleted) + "\n")
		Expect(ok).To(BeTrue())
		Expect(status).To(Equal(StatusCompleted))

		cycleStatus, ok := ParseCycleStatus(" \t" + string(CycleStatusFailed) + "\n")
		Expect(ok).To(BeTrue())
		Expect(cycleStatus).To(Equal(CycleStatusFailed))

		Expect(KnownEventType(EventType(EventName(EventStart)))).To(BeFalse())
		Expect(KnownEventType(EventType(" " + string(EventStart)))).To(BeFalse())
	})
})
