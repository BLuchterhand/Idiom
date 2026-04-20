package frontier

import (
	"strings"
	"testing"
)

func TestFrontierPromptCompilerIncludesLaneContracts(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "implement",
			prompt: BuildImplementTaskPrompt("change routing"),
			want:   "Answer contract: implementation_result",
		},
		{
			name:   "review",
			prompt: BuildReviewTaskPrompt("review routing"),
			want:   "Answer contract: review_finding",
		},
		{
			name:   "architecture",
			prompt: BuildArchitectureTaskPrompt("assess architecture"),
			want:   "Answer contract: architecture_assessment",
		},
		{
			name:   "explore",
			prompt: BuildExploreTaskPrompt("find the files"),
			want:   "Answer contract: grounded_explanation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.prompt, tc.want) {
				t.Fatalf("expected %q in prompt:\n%s", tc.want, tc.prompt)
			}
		})
	}
}

func TestStructuredExecutionPlanRulesUseTargetPolicy(t *testing.T) {
	packet := TargetPacket{
		SourceLane:   string(LaneArchitecture),
		PrimaryPaths: []string{"internal/app/app.go"},
	}
	rules := StructuredExecutionPlanRules(packet)
	for _, needle := range []string{
		"At most 2 operations",
		"structural refactor request",
		"Stay within the files and structural hints",
	} {
		if !strings.Contains(rules, needle) {
			t.Fatalf("expected %q in structural rules:\n%s", needle, rules)
		}
	}

	focused := FocusedStructuredExecutionPlanRules(packet, "internal/app/app.go", 10, 20)
	for _, needle := range []string{
		"Use only path internal/app/app.go.",
		"lines 10-20",
		"responsibility or boundary",
	} {
		if !strings.Contains(focused, needle) {
			t.Fatalf("expected %q in focused structural rules:\n%s", needle, focused)
		}
	}
}
