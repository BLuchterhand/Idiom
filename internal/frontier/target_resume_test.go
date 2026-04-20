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

func TestRenderDurablePlanArtifactOwnsApprovalScope(t *testing.T) {
	rendered := RenderDurablePlanArtifact(
		"Refactor internal/app/app.go.",
		LaneArchitecture,
		"Split internal/app/app.go into internal/app/routing.go and keep README.md as evidence.",
		[]string{"internal/app/app.go", "README.md"},
		"run go test ./internal/app",
	)

	for _, want := range []string{"Approval scope:", "Plan artifact schema:", "\"approval_scope\"", "allowed_destinations"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered artifact missing %q:\n%s", want, rendered)
		}
	}
	if allowed := DurablePlanAllowedDestinations(rendered); !slices.Contains(allowed, "internal/app/routing.go") {
		t.Fatalf("DurablePlanAllowedDestinations = %#v, want rendered destination", allowed)
	}
}

func TestBuildTaskMemoryResumeContextOwnsDurablePlanPolicy(t *testing.T) {
	summary := strings.Join([]string{
		"Durable plan artifact",
		"- Target: internal/app/implementation.go",
		"```json",
		`{"approval_scope":{"allowed_destinations":["internal/app/implementation_plan.go"]}}`,
		"```",
	}, "\n")

	resume := BuildTaskMemoryResumeContext(TaskMemoryResumeInput{
		Active:              true,
		Kind:                "durable_plan",
		Prompt:              "Refactor internal/app/prompt_routing.go",
		Lane:                string(LaneArchitecture),
		Summary:             summary,
		UserPrompt:          "implement this plan",
		AttemptedSearches:   []string{"validated target internal/app/analysis_agents.go"},
		ChosenTargets:       []string{"internal/app/task_memory.go"},
		VerificationHistory: []string{"go test ./internal/app -> passed"},
		NextActions:         []string{"verify with go test ./internal/app"},
	})

	if !resume.Active {
		t.Fatal("resume.Active = false, want true")
	}
	if !slices.Equal(resume.ApprovedEditTargets, []string{"internal/app/implementation.go"}) {
		t.Fatalf("ApprovedEditTargets = %#v, want durable artifact target", resume.ApprovedEditTargets)
	}
	if !slices.Equal(resume.ReadOnlyEvidencePaths, []string{"internal/app/analysis_agents.go", "internal/app/task_memory.go"}) {
		t.Fatalf("ReadOnlyEvidencePaths = %#v, want attempted and fallback targets as evidence", resume.ReadOnlyEvidencePaths)
	}
	if !slices.Equal(resume.AllowedDestinations, []string{"internal/app/implementation_plan.go"}) {
		t.Fatalf("AllowedDestinations = %#v, want durable artifact destination", resume.AllowedDestinations)
	}
	if !slices.Contains(resume.VerificationPlan, "verify with go test ./internal/app") {
		t.Fatalf("VerificationPlan = %#v, want verification-oriented next action", resume.VerificationPlan)
	}
}

func TestBuildWorkflowResumeContextOwnsTargetPolicy(t *testing.T) {
	resume := BuildWorkflowResumeContext(WorkflowResumeInput{
		Resumable:           true,
		TargetRefs:          []string{"internal/app/fallback.go"},
		EditableTargets:     []string{"internal/app/workflow_state.go"},
		ReadOnlyEvidence:    []string{"internal/app/prompt_routing.go", "internal/app/workflow_state.go"},
		EvidencePaths:       []string{"internal/app/task_memory.go"},
		AllowedDestinations: []string{"internal/frontier/workflow_resume.go"},
		ForbiddenPaths:      []string{"internal/app/forbidden.go"},
		Verification:        []string{"go test ./internal/app"},
		NextActions:         []string{"confirm handoff"},
	})

	if resume.Kind != "workflow_plan" || resume.SourceLane != LaneArchitecture {
		t.Fatalf("resume identity = (%q, %q), want workflow architecture source", resume.Kind, resume.SourceLane)
	}
	if !slices.Equal(resume.ApprovedEditTargets, []string{"internal/app/workflow_state.go"}) {
		t.Fatalf("ApprovedEditTargets = %#v, want editable targets before target refs", resume.ApprovedEditTargets)
	}
	if slices.Contains(resume.ReadOnlyEvidencePaths, "internal/app/workflow_state.go") {
		t.Fatalf("ReadOnlyEvidencePaths = %#v, must exclude editable target", resume.ReadOnlyEvidencePaths)
	}
	if !slices.Contains(resume.AllowedDestinations, "internal/frontier/workflow_resume.go") {
		t.Fatalf("AllowedDestinations = %#v, want workflow-approved destination", resume.AllowedDestinations)
	}
	if !slices.Contains(resume.VerificationPlan, "go test ./internal/app") {
		t.Fatalf("VerificationPlan = %#v, want workflow verification", resume.VerificationPlan)
	}
}
