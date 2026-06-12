package agent

import (
	"testing"

	"idiom/internal/runtime/workerruntime"
)

// TestLanePolicyForBrief_ReadOnlyInvestigationLanes verifies that ALL
// read-only workspace lanes that do not require mutation receive
// ReadOnlyInvestigationLanePolicy, so budget enforcement and loop detection
// remain active (the model cannot read forever) while force-answer-after-
// rejection is suppressed. Conversation and direct_answer lanes still get
// ConversationLanePolicy (full north-star trust model).
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
			name: "architecture + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "architecture",
				Contract:          "architecture_assessment",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ReadOnlyInvestigationLanePolicy); return ok },
		},
		{
			name: "review + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "security_review",
				Contract:          "review_findings",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ReadOnlyInvestigationLanePolicy); return ok },
		},
		{
			name: "locate + read_only gets read-only investigation policy",
			brief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "locate",
				Contract:          "locate_targets",
				PermissionProfile: "read_only_workspace",
			},
			wantFn: func(p LanePolicy) bool { _, ok := p.(ReadOnlyInvestigationLanePolicy); return ok },
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
