package featureplanner

import "testing"

func TestPlanExecuteCompletionTerminalClass(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		want   PlanExecuteTerminalClass
	}{
		{
			name:   "completed status",
			fields: map[string]any{"status": "completed"},
			want:   PlanExecuteTerminalCompleted,
		},
		{
			name:   "skipped review repair",
			fields: map[string]any{"status": "skipped", "review_repair_skipped": true},
			want:   PlanExecuteTerminalCompleted,
		},
		{
			name:   "failure details override degraded status",
			fields: map[string]any{"status": "degraded", "failure_kind": "review_model_error"},
			want:   PlanExecuteTerminalBlocked,
		},
		{
			name:   "ambiguous status",
			fields: map[string]any{"status": "ambiguous"},
			want:   PlanExecuteTerminalBlocked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanExecuteCompletionTerminalClass(tt.fields); got != tt.want {
				t.Fatalf("PlanExecuteCompletionTerminalClass = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanExecuteCompletionTerminalDecisionReasons(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]any
		wantClass  PlanExecuteTerminalClass
		wantReason string
	}{
		{
			name:       "missing status",
			fields:     map[string]any{},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "missing required nested plan-execute status",
		},
		{
			name:       "skipped without explicit marker",
			fields:     map[string]any{"status": "skipped"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute skipped status is not explicitly marked as review-repair skipped by design",
		},
		{
			name:       "running phase blocks completed status",
			fields:     map[string]any{"status": "completed", "phase": "running"},
			wantClass:  PlanExecuteTerminalBlocked,
			wantReason: "nested plan-execute status is not success-shaped",
		},
		{
			name:      "skipped review with explicit marker succeeds",
			fields:    map[string]any{"status": "completed", "review_status": "skipped", "review_repair_skipped": true},
			wantClass: PlanExecuteTerminalCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanExecuteCompletionTerminalDecision(tt.fields)
			if got.Class != tt.wantClass || got.Reason != tt.wantReason {
				t.Fatalf("decision = %#v, want class=%q reason=%q", got, tt.wantClass, tt.wantReason)
			}
		})
	}
}
