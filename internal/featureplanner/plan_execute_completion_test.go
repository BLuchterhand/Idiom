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
