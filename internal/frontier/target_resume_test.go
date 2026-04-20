package frontier

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"qwen-bubbles/internal/llama"
)

func TestBuildTargetResumeContextFromPlanOwnsRecentPlanPolicy(t *testing.T) {
	root := t.TempDir()
	targetPath := filepath.Join(root, "internal", "app", "implementation.go")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	history := []llama.ChatMessage{{
		Role: "assistant",
		Content: "**Phase 1**\n" +
			"- Target: internal/app/implementation.go\n" +
			"- Constraint: edit only this existing Go file; do not create files or move declarations\n\n" +
			"**Phase 2**\n" +
			"- Target: internal/app/prompt_routing.go\n",
	}}

	resume := BuildTargetResumeContextFromPlan(TargetResumePlanInput{
		Root:    root,
		Prompt:  "begin phase 1",
		History: history,
	})

	if !resume.Active {
		t.Fatal("resume.Active = false, want true")
	}
	if !slices.Equal(resume.ApprovedEditTargets, []string{"internal/app/implementation.go"}) {
		t.Fatalf("ApprovedEditTargets = %#v, want phase 1 target only", resume.ApprovedEditTargets)
	}
	if len(resume.AllowedDestinations) != 0 {
		t.Fatalf("AllowedDestinations = %#v, want none for existing target", resume.AllowedDestinations)
	}
	if len(resume.VerificationPlan) == 0 || !strings.Contains(resume.VerificationPlan[0], "do not create files or move declarations") {
		t.Fatalf("VerificationPlan = %#v, want no-move constraint", resume.VerificationPlan)
	}
}

func TestDurablePlanScopeExtractionIsFrontierOwned(t *testing.T) {
	summary := strings.Join([]string{
		"Plan:",
		"- Target: internal/app/app.go",
		"- Split renderer into internal/app/rendering.go",
		"- create new file internal/app/session_state.go",
		"```json",
		`{"approval_scope":{"allowed_destinations":["internal/app/artifact_scope.go"]}}`,
		"```",
	}, "\n")

	if got := DurablePlanAllowedDestinations(summary); !slices.Equal(got, []string{"internal/app/artifact_scope.go"}) {
		t.Fatalf("DurablePlanAllowedDestinations = %#v, want artifact destination", got)
	}

	destinations := DurablePlanCandidateDestinations(summary, []string{"internal/app/app.go"}, nil)
	if !slices.Contains(destinations, "internal/app/rendering.go") || !slices.Contains(destinations, "internal/app/session_state.go") {
		t.Fatalf("DurablePlanCandidateDestinations = %#v, want summary-derived destinations", destinations)
	}

	evidence := DurablePlanReadOnlyEvidence([]string{"internal/app/app.go", "internal/app/analysis_agents.go"}, []string{"internal/app/app.go"})
	if !slices.Equal(evidence, []string{"internal/app/analysis_agents.go"}) {
		t.Fatalf("DurablePlanReadOnlyEvidence = %#v, want editable target filtered out", evidence)
	}
}
