package featureplanner

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Feature planner vocabulary", func() {
	It("round-trips every declared event type without duplicates", func() {
		seen := map[FeaturePlannerEventType]struct{}{}
		for _, eventType := range EventTypes() {
			_, duplicate := seen[eventType]
			Expect(duplicate).To(BeFalse(), "duplicate EventTypes entry %q", eventType)
			seen[eventType] = struct{}{}

			Expect(KnownEventType(eventType)).To(BeTrue())
			parsed, ok := ParseEventName(EventName(eventType))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventType))
		}
	})

	It("keeps the event registry synchronized with typed constants", func() {
		_, filename, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "runtime.Caller should locate this test")

		sourcePath := filepath.Join(filepath.Dir(filename), "event_types.go")
		body, err := os.ReadFile(sourcePath)
		Expect(err).NotTo(HaveOccurred())

		declared := map[FeaturePlannerEventType]string{}
		declRE := regexp.MustCompile(`(?m)(Event[A-Za-z0-9]+)\s+FeaturePlannerEventType\s*=\s*"([^"]+)"`)
		for _, match := range declRE.FindAllStringSubmatch(string(body), -1) {
			declared[FeaturePlannerEventType(match[2])] = match[1]
		}
		Expect(declared).NotTo(BeEmpty(), "no FeaturePlannerEventType constants found")

		known := map[FeaturePlannerEventType]struct{}{}
		for _, eventType := range EventTypes() {
			known[eventType] = struct{}{}
		}
		for eventType, name := range declared {
			Expect(known).To(HaveKey(eventType), "EventTypes missing %s=%s", name, eventType)
		}
	})

	DescribeTable("rejects malformed event names",
		func(name string) {
			_, ok := ParseEventName(name)
			Expect(ok).To(BeFalse())
		},
		Entry("empty", ""),
		Entry("unprefixed", string(EventStart)),
		Entry("bare prefix", EventPrefix),
		Entry("another event family", "idiom.plan_execute.start"),
		Entry("unknown feature planner event", "idiom.feature_planner.not_real"),
	)

	It("recognizes current and legacy durable event names", func() {
		for _, name := range EventNames() {
			Expect(KnownDurableEventName(name)).To(BeTrue(), "current event %q should be durable", name)
		}
		for _, name := range LegacyEventNames() {
			Expect(KnownDurableEventName(string(name))).To(BeTrue(), "legacy event %q should be durable", name)
			Expect(KnownLegacyEventName(string(name))).To(BeTrue(), "legacy event %q should be recognized", name)
		}
		for _, name := range []string{"", "idiom.feature.created", "idiom.feature_planner.not_real"} {
			Expect(KnownDurableEventName(name)).To(BeFalse(), "unknown event %q should be rejected", name)
		}
	})

	It("parses every nested plan-execute completion event", func() {
		for _, eventName := range []NestedPlanExecuteCompletionEventName{
			NestedPlanExecuteCompletionEventCompleted,
			NestedPlanExecuteCompletionEventBlocked,
		} {
			parsed, ok := ParseNestedPlanExecuteCompletionEventName(string(eventName))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(eventName))
			Expect(KnownNestedPlanExecuteCompletionEventName(string(eventName))).To(BeTrue())
		}

		_, ok := ParseNestedPlanExecuteCompletionEventName("internal_idiom.plan_execute.unknown")
		Expect(ok).To(BeFalse())
	})

	It("trusts only declared completion sources and distinguishes durable evidence", func() {
		for _, source := range PlanExecuteCompletionSources() {
			parsed, ok := ParsePlanExecuteCompletionSource(string(source))
			Expect(ok).To(BeTrue())
			Expect(parsed).To(Equal(source))
			Expect(CompletionSourceTrustsAcceptanceEvidence(string(source))).To(BeTrue())
		}

		for _, source := range []PlanExecuteCompletionSource{
			PlanExecuteCompletionSourceDurableTerminal,
			PlanExecuteCompletionSourceDurableProgress,
		} {
			Expect(CompletionSourceCarriesDurableAcceptanceEvidence(string(source))).To(BeTrue())
		}
		Expect(CompletionSourceCarriesDurableAcceptanceEvidence(string(PlanExecuteCompletionSourceHeadlessReturned))).To(BeFalse())
		Expect(CompletionSourceTrustsAcceptanceEvidence("test_plan_execute_adapter")).To(BeFalse())
	})

	It("trims string inputs before applying feature planner predicates", func() {
		eventType, ok := ParseEventName(" \t" + EventName(EventStart) + "\n")
		Expect(ok).To(BeTrue())
		Expect(eventType).To(Equal(EventStart))
		Expect(KnownLegacyEventName(" \t" + string(LegacyEventAtomStarted) + "\n")).To(BeTrue())

		source, ok := ParsePlanExecuteCompletionSource(" \t" + string(PlanExecuteCompletionSourceDurableTerminal) + "\n")
		Expect(ok).To(BeTrue())
		Expect(source).To(Equal(PlanExecuteCompletionSourceDurableTerminal))
		Expect(CompletionSourceCarriesDurableAcceptanceEvidence(" \t" + string(PlanExecuteCompletionSourceDurableProgress) + "\n")).To(BeTrue())
		Expect(CompletionSourceTrustsAcceptanceEvidence(" \t" + string(PlanExecuteCompletionSourceTUIReturned) + "\n")).To(BeTrue())

		eventName, ok := ParseNestedPlanExecuteCompletionEventName(" \t" + string(NestedPlanExecuteCompletionEventBlocked) + "\n")
		Expect(ok).To(BeTrue())
		Expect(eventName).To(Equal(NestedPlanExecuteCompletionEventBlocked))

		for _, value := range []string{
			"idiom.feature_planner.not_real",
			"idiom.feature.atom_unknown",
			"test_plan_execute_adapter",
			"internal_idiom.plan_execute.unknown",
		} {
			Expect(KnownDurableEventName(value)).To(BeFalse(), "unknown value %q should be rejected", value)
		}
	})
})
