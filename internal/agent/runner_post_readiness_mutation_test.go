package agent

import (
	"testing"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

// TestPostReadinessGapScopeForBriefSuppressesAnswerReadyOnMutation
// covers #101: when a child:explorer reports `answer_readiness_reached`
// to a mutation-required parent (refine draft, plan-execute, etc.),
// the parent's post-readiness gate must NOT fire. The parent's
// deliverable is a WRITE; the child's read-readiness only means
// the parent has enough evidence to commit the write, not that
// the parent is done. Without this guard, the parent's next
// targeted read trips post_readiness_no_gap and the lane wedges
// at 39s with no draft produced.
func TestPostReadinessGapScopeForBriefSuppressesAnswerReadyOnMutation(t *testing.T) {
	mutationBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		RequiredEditPaths: []string{"some/output.md"},
	}
	readOnlyBrief := workerruntime.WorkerRuntimeBrief{
		TaskFamily: "investigation",
	}
	req := workspace.ToolRequest{Tool: "read_file", Path: "x.go"}

	// Mutation parent + delegated read-only answer ready → suppressed.
	if scope := postReadinessGapScopeForBrief("main", true, false, req, mutationBrief); scope != "" {
		t.Fatalf("mutation parent should suppress answer_readiness_reached scope; got %q", scope)
	}

	// Read-only parent + delegated read-only answer ready → still fires.
	if scope := postReadinessGapScopeForBrief("main", true, false, req, readOnlyBrief); scope != "answer_readiness_reached" {
		t.Fatalf("read-only parent should still trigger answer_readiness_reached; got %q", scope)
	}

	// Child:* agent + read-only answer ready → delegated path still
	// fires regardless of parent brief shape (child's own gate).
	if scope := postReadinessGapScopeForBrief("child:explorer", true, false, req, mutationBrief); scope != "delegated_read_only_answer_ready" {
		t.Fatalf("child:* agent should still trigger delegated path; got %q", scope)
	}

	// child_packet_available scope ALSO suppressed on mutation
	// parents — same reasoning as answer_readiness_reached: the
	// parent's deliverable is a write, not an answer.
	if scope := postReadinessGapScopeForBrief("main", false, true, req, mutationBrief); scope != "" {
		t.Fatalf("child_packet_available should also be suppressed on mutation parent; got %q", scope)
	}

	// But on a READ-ONLY parent, child_packet_available STILL fires.
	if scope := postReadinessGapScopeForBrief("main", false, true, req, readOnlyBrief); scope != "child_packet_available" {
		t.Fatalf("child_packet_available should fire on read-only parent; got %q", scope)
	}

	// Write-class tool — neither scope fires regardless of brief.
	writeReq := workspace.ToolRequest{Tool: "write_file", Path: "x.md"}
	if scope := postReadinessGapScopeForBrief("main", true, false, writeReq, mutationBrief); scope != "" {
		t.Fatalf("write-class tool should never trigger post-readiness scope; got %q", scope)
	}
}
