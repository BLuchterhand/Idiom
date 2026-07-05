package plan

import (
	"slices"
	"testing"

	"idiom/internal/contracts/workflowstate"
)

func TestPromptPacketDetectsStructuredContextPacket(t *testing.T) {
	packet := PromptPacketFromPrompt(`{"candidate_scope":[{"path":"a/b.go"}],"evidence":[{"source":"a/c.go"}]}`)

	if !packet.Context.StructuredPacket || !packet.Context.Dispatchable || packet.Context.LegacyHeadingFallback {
		t.Fatalf("structured packet should be dispatchable without heading fallback: %#v", packet.Context)
	}
	if !slices.Contains(packet.TargetPacketMarkers, "prompt_context:structured_packet") {
		t.Fatalf("structured packet marker missing: %#v", packet.TargetPacketMarkers)
	}
	if packet.Context.Reason != "structured_packet" {
		t.Fatalf("reason = %q, want structured_packet", packet.Context.Reason)
	}
}

func TestPromptPacketRejectsIncidentalStructuredPacketMentions(t *testing.T) {
	for _, prompt := range []string{
		`{"evidence":"one incidental field"}`,
		`Document how planning_packet_v2 is named in prompts.`,
		`Document the "schema" for planning_packet_v2 prompts.`,
	} {
		t.Run(prompt, func(t *testing.T) {
			if PromptHasStructuredPacketMarker(prompt) {
				t.Fatalf("incidental prose should not mark a structured packet: %q", prompt)
			}
		})
	}
}

func TestPromptPacketContextHeadingFallbackRequiresContent(t *testing.T) {
	packet := PromptPacketFromPrompt(`
Context Packet:

Required Behavior:
- internal/surfaces/tui/app/prompt_routing.go
`)
	if packet.Context.StructuredPacket || packet.Context.LegacyHeadingFallback || packet.Context.Dispatchable {
		t.Fatalf("empty context packet heading should not dispatch: %#v", packet.Context)
	}
	if packet.Context.State != workflowstate.StateTerminal || packet.Context.TerminalState != workflowstate.TerminalStateBlocked {
		t.Fatalf("missing context should be terminal blocked: %#v", packet.Context)
	}
	if slices.Contains(packet.TargetPacketMarkers, "prompt_context:context_packet") {
		t.Fatalf("empty heading should not create context packet marker: %#v", packet.TargetPacketMarkers)
	}
	if len(packet.EvidencePaths) != 0 {
		t.Fatalf("empty heading should not borrow later behavior paths: %#v", packet.EvidencePaths)
	}
}

func TestPromptPacketContextHeadingCapturesTrailingColonPath(t *testing.T) {
	packet := PromptPacketFromPrompt(`
Context Packet:
- internal/surfaces/tui/app/prompt_routing.go:
`)
	if !packet.Context.LegacyHeadingFallback || !packet.Context.Dispatchable {
		t.Fatalf("path content should dispatch legacy context packet: %#v", packet.Context)
	}
	if !slices.Contains(packet.TargetPacketMarkers, "prompt_context:context_packet") {
		t.Fatalf("context packet marker missing: %#v", packet.TargetPacketMarkers)
	}
	if !slices.Contains(packet.EvidencePaths, "internal/surfaces/tui/app/prompt_routing.go") {
		t.Fatalf("context evidence path missing: %#v", packet.EvidencePaths)
	}
}

func TestPromptPacketContextHeadingAcceptsNestedEvidenceSection(t *testing.T) {
	packet := PromptPacketFromPrompt(`
Context Packet:
Evidence:
- internal/surfaces/tui/app/prompt_routing.go
`)
	if !packet.Context.LegacyHeadingFallback || !packet.Context.Dispatchable {
		t.Fatalf("nested evidence should dispatch legacy context packet: %#v", packet.Context)
	}
	if !slices.Contains(packet.TargetPacketMarkers, "prompt_context:context_packet") {
		t.Fatalf("context packet marker missing: %#v", packet.TargetPacketMarkers)
	}
}

func TestPromptPacketIncludesScopeAndExactAnchorFacts(t *testing.T) {
	packet := PromptPacketFromPrompt(`
Allowed Edit Paths:
- pkg/optional.go

Required edit paths: pkg/target.go

{
  "old_text": "before();",
  "new_text": "after();"
}
`)
	if !slices.Contains(packet.RequiredEditPaths, "pkg/target.go") {
		t.Fatalf("required edit path missing: %#v", packet.RequiredEditPaths)
	}
	if !slices.Contains(packet.AllowedEditPaths, "pkg/optional.go") {
		t.Fatalf("allowed edit path missing: %#v", packet.AllowedEditPaths)
	}
	if !packet.HasExactEditAnchor {
		t.Fatal("structured old_text/new_text pair should count as exact anchor")
	}
	if !packet.HasRequiredEditPayload {
		t.Fatal("exact edit anchor should count as required edit payload")
	}
}

func TestPromptPacketRequiredEditsMarkerCountsAsRequiredPayload(t *testing.T) {
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
	if !slices.Contains(packet.TargetPacketMarkers, "prompt_context:required_edits") {
		t.Fatalf("required edits marker missing: %#v", packet.TargetPacketMarkers)
	}
	if !packet.HasRequiredEditPayload {
		t.Fatal("required edits marker should count as required edit payload")
	}
}
