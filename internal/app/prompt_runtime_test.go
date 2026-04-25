package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/workerruntime"
	"idiom/internal/llama"
	"idiom/internal/promptstack"
)

func TestSystemPromptSkipsModePromptFilesAlreadyInPromptStack(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "prompts", "base.md")
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(basePath, []byte("Base behavior\n"), 0o644); err != nil {
		t.Fatalf("write base prompt: %v", err)
	}

	model := &Model{
		cfg: config.Config{Chat: config.Chat{ProjectRoot: dir}},
		promptStack: promptstack.Stack{
			Text: "[baseline behavior: base.md]\nBase behavior",
			Sources: []promptstack.Source{{
				Kind:        "baseline",
				DisplayName: "base.md",
				Path:        filepath.Join("prompts", "base.md"),
			}},
		},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	if strings.Count(prompt, "Base behavior") != 1 {
		t.Fatalf("expected mode/system prompt dedupe, got:\n%s", prompt)
	}
}

func TestTuneChatOptionsForReadOnlyPlanDisablesThinkingAndCapsTokens(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "grounded_explanation",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)
	if tuned.EnableThinking == nil || *tuned.EnableThinking {
		t.Fatalf("expected read-only lane to disable thinking, got %#v", tuned.EnableThinking)
	}
	if tuned.PreserveThinking {
		t.Fatalf("expected preserve thinking off for read-only lane")
	}
	if tuned.MaxTokens != 1536 {
		t.Fatalf("expected grounded explanation token cap, got %d", tuned.MaxTokens)
	}
}

func TestDefaultVerificationPlanPrefersGoTestWhenGoModExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	plan := defaultVerificationPlan(dir)
	if len(plan) == 0 || plan[0] != "go test ./..." {
		t.Fatalf("expected go test verification plan, got %#v", plan)
	}
}

func TestBuildPromptExecutionPlanCapturesEvidenceFirstTargetRefs(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	plan := model.buildPromptExecutionPlan(context.Background(), "Investigate the routing flow in this codebase and then update docs/frontier-routing-implementation-plan.md with what you find.")

	if plan.route.Family != "review_then_implement" {
		t.Fatalf("unexpected family: %q", plan.route.Family)
	}
	if len(plan.runtimeBrief.TargetRefs) != 1 || plan.runtimeBrief.TargetRefs[0] != "docs/frontier-routing-implementation-plan.md" {
		t.Fatalf("expected target ref to be captured, got %#v", plan.runtimeBrief.TargetRefs)
	}
	if !slices.Contains(plan.runtimeBrief.RequiredNext, "gather source evidence before large reads of named target refs") {
		t.Fatalf("expected evidence-first requirement in runtime brief, got %#v", plan.runtimeBrief.RequiredNext)
	}
	if !slices.Contains(plan.runtimeBrief.InvalidActions, "large read_file on named target refs before source evidence") {
		t.Fatalf("expected target-read invalid action in runtime brief, got %#v", plan.runtimeBrief.InvalidActions)
	}
}

func TestReloadPromptStackGeneratesProjectArtifacts(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# Demo\n\nAlways verify focused changes.\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("Prefer semantic tools.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS: %v", err)
	}

	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{ProjectRoot: project},
		},
	}

	if err := model.reloadPromptStackWithProfiles(nil); err != nil {
		t.Fatalf("reloadPromptStackWithProfiles: %v", err)
	}
	contextPath := filepath.Join(project, ".idiom", "discovery", "context.md")
	if _, err := os.Stat(contextPath); err != nil {
		t.Fatalf("expected generated context at %s: %v", contextPath, err)
	}
	if !strings.Contains(model.promptStack.Text, "[project behavior: .idiom/discovery/context.md]") {
		t.Fatalf("expected prompt stack to include generated context, got:\n%s", model.promptStack.Text)
	}
	if !strings.Contains(model.promptStack.Text, "Generated Project Context") {
		t.Fatalf("expected generated context body in prompt stack, got:\n%s", model.promptStack.Text)
	}
}
