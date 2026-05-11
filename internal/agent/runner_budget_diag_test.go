package agent

import (
	"encoding/json"
	"testing"

	"idiom/internal/frontier/workerruntime"
)

func collectRunnerEvents(events *[]RunEvent) func(RunEvent) {
	return func(event RunEvent) {
		*events = append(*events, event)
	}
}

func findEvent(events []RunEvent, role string) *RunEvent {
	for i := range events {
		if events[i].Role == role {
			return &events[i]
		}
	}
	return nil
}

func TestEmitBriefAuditCapturesContractShape(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		OnEvent: collectRunnerEvents(&events),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			Lane:              "implement",
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"a.go", "b.go"},
			RequiredEditSnippets: []workerruntime.RequiredEditSnippet{
				{Path: "a.go", Content: "old"},
				{Path: "a.go", Content: "second snippet"},
				{Path: "b.go", Content: "new"},
			},
			TargetPacket: []string{"line one", "line two"},
			Readiness: &workerruntime.ReadinessState{
				ReadyToWrite:        true,
				ClosedClaims:        []string{"required_edit_paths_define_write_scope"},
				FurtherReadsRequire: "named_gap",
			},
		},
	}

	runner.emitBriefAudit(runner.RuntimeBrief, nil)

	event := findEvent(events, "brief_audit")
	if event == nil {
		t.Fatal("expected brief_audit event")
	}
	var payload briefAuditEvent
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("brief_audit payload not valid JSON: %v", err)
	}
	if payload.RequiredEditSnippetsCount != 3 {
		t.Fatalf("snippet count: want 3, got %d", payload.RequiredEditSnippetsCount)
	}
	if got := payload.RequiredEditSnippetPaths; len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("snippet paths should dedupe: %v", got)
	}
	if !payload.ExactAnchorImplActive {
		t.Fatal("exact_anchor_implementation_active should be true when constraint is set")
	}
	if !payload.ReadinessReadyToWrite {
		t.Fatal("readiness_ready_to_write should mirror brief readiness")
	}
	if payload.TargetPacketLines != 2 {
		t.Fatalf("target_packet_lines: want 2, got %d", payload.TargetPacketLines)
	}
	if payload.TargetPacketBytes == 0 {
		t.Fatal("target_packet_bytes should be non-zero for a populated packet")
	}
}
