package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"idiom/internal/contracts/writerroute"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plan-execute event vocabulary", func() {
	It("round-trips a qualified event name", func() {
		name := PlanExecuteEventName(PlanExecuteEventVerification)
		Expect(name).To(Equal("idiom.plan_execute.verification"))
		eventType, ok := ParsePlanExecuteEventName(name)
		Expect(ok).To(BeTrue())
		Expect(eventType).To(Equal(PlanExecuteEventVerification))
	})

	It("round-trips every declared event through every parser", func() {
		seen := map[PlanExecuteEventType]struct{}{}
		for _, eventType := range PlanExecuteEventTypes() {
			Expect(seen).NotTo(HaveKey(eventType))
			seen[eventType] = struct{}{}
			Expect(KnownPlanExecuteEventType(eventType)).To(BeTrue())

			parsed, ok := ParsePlanExecuteEventName(PlanExecuteEventName(eventType))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventType))
			parsed, ok = ParsePlanExecuteEventType(string(eventType))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventType))
			parsed, ok = ParsePlanExecuteInvalidEventName(PlanExecuteInvalidEventName(eventType))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventType))
		}
	})

	It("covers every declared event constant", func() {
		_, filename, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue())
		body, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "event_types.go"))
		Expect(err).NotTo(HaveOccurred())
		declared := map[PlanExecuteEventType]string{}
		pattern := regexp.MustCompile(`(?m)(PlanExecuteEvent[A-Za-z0-9]+)\s+PlanExecuteEventType\s*=\s*"([^"]+)"`)
		for _, match := range pattern.FindAllStringSubmatch(string(body), -1) {
			declared[PlanExecuteEventType(match[2])] = match[1]
		}
		Expect(declared).NotTo(BeEmpty())
		known := map[PlanExecuteEventType]struct{}{}
		for _, eventType := range PlanExecuteEventTypes() {
			known[eventType] = struct{}{}
		}
		for eventType := range declared {
			Expect(known).To(HaveKey(eventType))
		}
	})

	DescribeTable("round-trips terminal outcome events",
		func(eventType PlanExecuteEventType) {
			parsed, ok := ParsePlanExecuteEventName(PlanExecuteEventName(eventType))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventType))
		},
		Entry("completed", PlanExecuteEventCompleted),
		Entry("blocked", PlanExecuteEventBlocked),
	)

	DescribeTable("rejects malformed event names",
		func(name string) {
			_, ok := ParsePlanExecuteEventName(name)
			Expect(ok).To(BeFalse())
		},
		Entry("empty", ""),
		Entry("bare", "verification"),
		Entry("wrong prefix", "idiom.feature_planner.verification"),
		Entry("prefix only", PlanExecuteEventPrefix),
		Entry("unknown event", "idiom.plan_execute.typo_event"),
	)

	DescribeTable("accepts qualified and bare event types",
		func(name string) {
			eventType, ok := ParsePlanExecuteEventType(name)
			Expect(ok).To(BeTrue())
			Expect(eventType).To(Equal(PlanExecuteEventExecutorEdit))
		},
		Entry("qualified", PlanExecuteEventName(PlanExecuteEventExecutorEdit)),
		Entry("bare", string(PlanExecuteEventExecutorEdit)),
	)

	DescribeTable("rejects unknown event types",
		func(name string) {
			_, ok := ParsePlanExecuteEventType(name)
			Expect(ok).To(BeFalse())
		},
		Entry("qualified", "idiom.plan_execute.not_real"),
		Entry("bare", "not_real"),
	)

	It("round-trips invalid-event names", func() {
		name := PlanExecuteInvalidEventName(PlanExecuteEventChildTerminal)
		Expect(name).To(Equal("idiom.plan_execute.child_terminal_invalid"))
		eventType, ok := ParsePlanExecuteInvalidEventName(name)
		Expect(ok).To(BeTrue())
		Expect(eventType).To(Equal(PlanExecuteEventChildTerminal))
	})

	DescribeTable("rejects non-invalid event names",
		func(name string) {
			_, ok := ParsePlanExecuteInvalidEventName(name)
			Expect(ok).To(BeFalse())
		},
		Entry("normal event", PlanExecuteEventName(PlanExecuteEventChildTerminal)),
		Entry("missing event", "idiom.plan_execute._invalid"),
		Entry("unknown event", "idiom.plan_execute.typo_event_invalid"),
		Entry("wrong prefix", "idiom.feature_planner.child_terminal_invalid"),
	)

	It("matches the writer-route contract vocabulary", func() {
		Expect(string(PlanExecuteEventWriterRouteDecision)).To(Equal(string(writerroute.EventWriterRouteDecision)))
	})
})
