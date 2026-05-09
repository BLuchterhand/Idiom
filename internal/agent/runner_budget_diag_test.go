package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
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

func TestEmitReadBudgetDiagLabelsClampReason(t *testing.T) {
	var events []RunEvent
	runner := &Runner{
		OnEvent: collectRunnerEvents(&events),
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			Constraints:       []string{"exact_anchor_implementation"},
			RequiredEditPaths: []string{"native/src/foo.cpp"},
		},
	}
	requestBudget := newRunnerRequestBudgetState(2)
	requestBudget.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "native/src/foo.cpp"})
	policy := RunPolicy{ReadBudgetHard: 1}

	runner.emitReadBudgetDiag(3, workspace.ToolRequest{Tool: "read_file", Path: "native/src/foo.cpp"}, policy, nil, requestBudget)

	event := findEvent(events, "read_budget_diag")
	if event == nil {
		t.Fatal("expected read_budget_diag event")
	}
	var payload readBudgetDiagEvent
	if err := json.Unmarshal([]byte(event.Content), &payload); err != nil {
		t.Fatalf("read_budget_diag payload not valid JSON: %v", err)
	}
	if payload.ClampReason != "writeReadyPacketPrewrite" {
		t.Fatalf("clamp_reason: want writeReadyPacketPrewrite, got %q", payload.ClampReason)
	}
	if !payload.ExactAnchorActive {
		t.Fatal("exact_anchor_active should be true with constraint set")
	}
	if payload.EffectiveReadBudget != 1 {
		t.Fatalf("effective_read_budget: want 1, got %d", payload.EffectiveReadBudget)
	}
	if payload.ReadCallsSinceWrite != 1 || payload.ReadCallCount != 1 {
		t.Fatalf("read counters not surfaced: %+v", payload)
	}
}

func TestReadBudgetClampReasonBranches(t *testing.T) {
	implementationBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"a.go"},
	}
	if got := readBudgetClampReason(implementationBrief, nil); got != "abstractImplementationDefault" {
		t.Fatalf("brief without anchors: want abstractImplementationDefault, got %q", got)
	}

	withSnippets := implementationBrief
	withSnippets.RequiredEditSnippets = []workerruntime.RequiredEditSnippet{{Path: "a.go", Content: "anchor"}}
	if got := readBudgetClampReason(withSnippets, nil); got != "requiredEditPathPrewrite" {
		t.Fatalf("brief with snippets: want requiredEditPathPrewrite, got %q", got)
	}

	exactBrief := implementationBrief
	exactBrief.Constraints = []string{"exact_anchor_implementation"}
	if got := readBudgetClampReason(exactBrief, nil); got != "writeReadyPacketPrewrite" {
		t.Fatalf("prewrite with exact anchor: want writeReadyPacketPrewrite, got %q", got)
	}

	results := []workspace.ToolResult{{Tool: "edit_file", Summary: "Edited a.go"}}
	if got := readBudgetClampReason(implementationBrief, results); got != "mutationDefault" {
		t.Fatalf("all required paths changed: want mutationDefault, got %q", got)
	}

	twoPathBrief := implementationBrief
	twoPathBrief.RequiredEditPaths = []string{"a.go", "b.go"}
	if got := readBudgetClampReason(twoPathBrief, results); got != "abstractImplementationRemainder" {
		t.Fatalf("partial change without exact anchor: want abstractImplementationRemainder, got %q", got)
	}

	exactRemainder := twoPathBrief
	exactRemainder.Constraints = []string{"exact_anchor_implementation"}
	if got := readBudgetClampReason(exactRemainder, results); got != "requiredEditPathRemainder" {
		t.Fatalf("partial change with exact anchor: want requiredEditPathRemainder, got %q", got)
	}

	readOnly := workerruntime.WorkerRuntimeBrief{TaskFamily: "architecture"}
	if got := readBudgetClampReason(readOnly, nil); got != "modeDefault" {
		t.Fatalf("read-only brief: want modeDefault, got %q", got)
	}
}

func TestReadBudgetHardBlockPayloadCarriesGapAndRemaining(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"native/src/foo.h", "native/src/foo.cpp"},
		},
	}
	requestBudget := newRunnerRequestBudgetState(2)
	requestBudget.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "native/src/foo.h"})
	requestBudget.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "native/src/foo.h"})

	req := workspace.ToolRequest{
		Tool:            "read_file",
		Path:            "native/src/foo.h",
		StartLine:       120,
		EndLine:         250,
		NamedGap:        "find private section anchor",
		WhyNotAnswering: "need surrounding code to insert decl",
	}
	policy := RunPolicy{ReadBudgetHard: 1}

	rendered := runner.readBudgetHardBlockPayload(req, nil, policy, requestBudget, "fallback message")
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("expected non-empty payload")
	}
	var payload readBudgetHardBlockEvent
	if err := json.Unmarshal([]byte(rendered), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload.NamedGap != req.NamedGap {
		t.Fatalf("named_gap: want %q, got %q", req.NamedGap, payload.NamedGap)
	}
	if payload.WhyNotAnswering != req.WhyNotAnswering {
		t.Fatalf("why_not_answering: want %q, got %q", req.WhyNotAnswering, payload.WhyNotAnswering)
	}
	if !payload.RequestPathInRequiredPaths {
		t.Fatal("request path overlaps required edit paths but flag was false")
	}
	if len(payload.RemainingRequiredEditPaths) != 2 {
		t.Fatalf("remaining required paths: want 2, got %v", payload.RemainingRequiredEditPaths)
	}
	if payload.EffectiveReadBudget != 1 {
		t.Fatalf("effective_read_budget: want 1, got %d", payload.EffectiveReadBudget)
	}
	if payload.ReadCallsSinceWrite != 2 {
		t.Fatalf("read_calls_since_write: want 2, got %d", payload.ReadCallsSinceWrite)
	}
	if payload.Message != "fallback message" {
		t.Fatalf("message: want fallback string, got %q", payload.Message)
	}
}

func TestReadBudgetHardBlockPayloadIgnoresPathOutsideRequired(t *testing.T) {
	runner := &Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			RequiredEditPaths: []string{"native/src/foo.cpp"},
		},
	}
	requestBudget := newRunnerRequestBudgetState(2)
	requestBudget.noteReadClassRequest(workspace.ToolRequest{Tool: "read_file", Path: "native/src/bar.cpp"})

	rendered := runner.readBudgetHardBlockPayload(
		workspace.ToolRequest{Tool: "read_file", Path: "native/src/bar.cpp"},
		nil,
		RunPolicy{ReadBudgetHard: 1},
		requestBudget,
		"msg",
	)
	var payload readBudgetHardBlockEvent
	if err := json.Unmarshal([]byte(rendered), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload.RequestPathInRequiredPaths {
		t.Fatal("path outside required paths should leave flag false")
	}
}
