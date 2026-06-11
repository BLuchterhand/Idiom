package agent

import (
	"testing"

	"idiom/internal/runtime/workerruntime"
)

// TestLanePolicyForBrief_ReadOnlyInvestigationLanes verifies that ALL
// read-only workspace lanes that do not require mutation receive
// ConversationLanePolicy, so the model owns readiness and is not
// force-completed after incidental tool rejections. Previously only
// conversation/direct_answer lanes got this treatment; architecture,
// review, and locate lanes fell through to DefaultLanePolicy, causing
// the LLM classifier's non-determinism (temp 0.1) to produce
// inconsistent force-answer behavior for similar evidence-based
// questions.
func TestLanePolicyForBrief_ReadOnlyInvestigationLanes(t *testing.T) {
	tests := []struct {
		name   string
		brief  workerruntime.WorkerRuntimeBrief
		wantFn func(LanePolicy) bool // returns true if policy is acceptable
	}{
		{
			name: "conversation gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ConversationLanePolicy); return ok },
		},
		{
			name: "direct_answer + read_only gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "direct_answer",
				Contract:          "direct_answer",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ConversationLanePolicy); return ok },
		},
		{
			name: "architecture + read_only gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "architecture",
				Contract:          "architecture_assessment",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ConversationLanePolicy); return ok },
		},
		{
			name: "review + read_only gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "security_review",
				Contract:          "review_findings",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ConversationLanePolicy); return ok },
		},
		{
			name: "locate + read_only gets conversation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "locate",
				Contract:          "locate_targets",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ConversationLanePolicy); return ok },
		},
		{
			name: "implementation gets default policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "implementation",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(DefaultLanePolicy); return ok },
		},
		{
			name: "plan-execute planner gets default policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "plan_execute_planner",
				Contract:          "implementation_result",
				PermissionProfile: "guarded_workspace_write",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(DefaultLanePolicy); return ok },
		},
		{
			name: "delegated read-only gets delegated policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "delegated_read_only",
				Contract:          "grounded_evidence_summary",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(DelegatedReadOnlyLanePolicy); return ok },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LanePolicyForBrief(tt.brief)
			if !tt.wantFn(got) {
				t.Errorf("LanePolicyForBrief(%+v) = %T (%#v), want type satisfying wantFn", tt.brief, got, got)
			}
		})
	}
}
