package plan

import (
	"idiom/internal/contracts/workflowstate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Prompt packets", func() {
	It("detects a dispatchable structured context packet", func() {
		packet := PromptPacketFromPrompt(`{"candidate_scope":[{"path":"a/b.go"}],"evidence":[{"source":"a/c.go"}]}`)
		Expect(packet.Context.StructuredPacket).To(BeTrue())
		Expect(packet.Context.Dispatchable).To(BeTrue())
		Expect(packet.Context.LegacyHeadingFallback).To(BeFalse())
		Expect(packet.TargetPacketMarkers).To(ContainElement("prompt_context:structured_packet"))
		Expect(packet.Context.Reason).To(Equal("structured_packet"))
	})

	DescribeTable("rejects incidental structured-packet mentions",
		func(prompt string) {
			Expect(PromptHasStructuredPacketMarker(prompt)).To(BeFalse())
		},
		Entry("incidental evidence field", `{"evidence":"one incidental field"}`),
		Entry("packet name prose", `Document how planning_packet_v2 is named in prompts.`),
		Entry("schema prose", `Document the "schema" for planning_packet_v2 prompts.`),
	)

	It("requires content after a legacy context heading", func() {
		packet := PromptPacketFromPrompt(`
Context Packet:

Required Behavior:
- internal/surfaces/tui/app/prompt_routing.go
`)
		Expect(packet.Context.StructuredPacket).To(BeFalse())
		Expect(packet.Context.LegacyHeadingFallback).To(BeFalse())
		Expect(packet.Context.Dispatchable).To(BeFalse())
		Expect(packet.Context.State).To(Equal(workflowstate.StateTerminal))
		Expect(packet.Context.TerminalState).To(Equal(workflowstate.TerminalStateBlocked))
		Expect(packet.TargetPacketMarkers).NotTo(ContainElement("prompt_context:context_packet"))
		Expect(packet.EvidencePaths).To(BeEmpty())
	})

	It("captures a trailing-colon path under a context heading", func() {
		packet := PromptPacketFromPrompt(`
Context Packet:
- internal/surfaces/tui/app/prompt_routing.go:
`)
		Expect(packet.Context.LegacyHeadingFallback).To(BeTrue())
		Expect(packet.Context.Dispatchable).To(BeTrue())
		Expect(packet.TargetPacketMarkers).To(ContainElement("prompt_context:context_packet"))
		Expect(packet.EvidencePaths).To(ContainElement("internal/surfaces/tui/app/prompt_routing.go"))
	})

	It("accepts a nested evidence section under a context heading", func() {
		packet := PromptPacketFromPrompt(`
Context Packet:
Evidence:
- internal/surfaces/tui/app/prompt_routing.go
`)
		Expect(packet.Context.LegacyHeadingFallback).To(BeTrue())
		Expect(packet.Context.Dispatchable).To(BeTrue())
		Expect(packet.TargetPacketMarkers).To(ContainElement("prompt_context:context_packet"))
	})

	It("includes scope and exact-anchor facts", func() {
		packet := PromptPacketFromPrompt(`
Allowed Edit Paths:
- pkg/optional.go

Required edit paths: pkg/target.go

{
  "old_text": "before();",
  "new_text": "after();"
}
`)
		Expect(packet.RequiredEditPaths).To(ContainElement("pkg/target.go"))
		Expect(packet.AllowedEditPaths).To(ContainElement("pkg/optional.go"))
		Expect(packet.HasExactEditAnchor).To(BeTrue())
		Expect(packet.HasRequiredEditPayload).To(BeTrue())
	})

	It("counts a required-edits marker as required payload", func() {
		packet := PromptPacketFromPrompt(`
## Context Packet

Evidence:
- pkg/a.go

## Required Edits

Add the helper:
` + "```go" + `
func helper() {}
` + "```" + `
`)
		Expect(packet.TargetPacketMarkers).To(ContainElement("prompt_context:required_edits"))
		Expect(packet.HasRequiredEditPayload).To(BeTrue())
	})
})
