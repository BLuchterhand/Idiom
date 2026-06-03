package agent

import (
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// TestPostReadinessGapScopeForBriefDoesNotBlockModelReads covers the core
// discovery invariant: read-only readiness can nudge the model toward an
// answer, but it cannot deny a read request. If the model asks for more source
// evidence, the normal lane/tool permissions decide whether that read is legal.
func TestPostReadinessGapScopeForBriefDoesNotBlockModelReads(t *testing.T) {
	mutationBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"some/output.md"},
	}
	readOnlyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "investigation",
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "x.go"}

	// Mutation parent + delegated read-only answer ready -> suppressed.
	if scope := postReadinessGapScopeForBrief("main", true, false, req, mutationBrief); scope != "" {
		t.Fatalf("mutation parent should suppress answer_readiness_reached scope; got %q", scope)
	}

	// Read-only parent + delegated read-only answer ready -> advisory only.
	if scope := postReadinessGapScopeForBrief("main", true, false, req, readOnlyBrief); scope != "" {
		t.Fatalf("read-only parent should not block post-readiness reads; got %q", scope)
	}

	// Child:* agent + read-only answer ready -> also advisory.
	if scope := postReadinessGapScopeForBrief("child:explorer", true, false, req, mutationBrief); scope != "" {
		t.Fatalf("child:* agent should not block post-readiness reads; got %q", scope)
	}

	// child_packet_available scope ALSO suppressed on mutation
	// parents — same reasoning as answer_readiness_reached: the
	// parent's deliverable is a write, not an answer.
	if scope := postReadinessGapScopeForBrief("main", false, true, req, mutationBrief); scope != "" {
		t.Fatalf("child_packet_available should also be suppressed on mutation parent; got %q", scope)
	}

	// Read-only parent + child packet -> advisory only.
	if scope := postReadinessGapScopeForBrief("main", false, true, req, readOnlyBrief); scope != "" {
		t.Fatalf("read-only child_packet_available should not block reads; got %q", scope)
	}

	// Write-class tool: neither scope fires regardless of brief.
	writeReq := workspace.ToolRequest{Tool: "write_file", Path: "x.md"}
	if scope := postReadinessGapScopeForBrief("main", true, false, writeReq, mutationBrief); scope != "" {
		t.Fatalf("write-class tool should never trigger post-readiness scope; got %q", scope)
	}
}

func TestPostReadinessReadRequestWithoutNamedGapRemainsDispatchable(t *testing.T) {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "architecture",
		Contract:          "architecture_assessment",
		PermissionProfile: "read_only_workspace",
		Objective:         "What does the debug log redacted mode do?",
	}
	req := workspace.ToolRequest{
		Tool:   "code_navigate",
		Path:   "internal/transcript/debuglog.go",
		Symbol: "DebugLogger.sanitizeValue",
	}

	if postReadinessExactProbeContractPresent(req) {
		t.Fatal("test setup should omit named_gap and why_not_answering")
	}
	if scope := postReadinessGapScopeForBrief("main", true, false, req, brief); scope != "" {
		t.Fatalf("post-readiness source read should remain dispatchable, got blocking scope %q", scope)
	}
}
