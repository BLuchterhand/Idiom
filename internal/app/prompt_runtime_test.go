package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestNewInstructionAttacherCanBeDisabledByConfig(t *testing.T) {
	disabled := false
	if got := newInstructionAttacher(config.Chat{AttachInstructionFiles: &disabled}, t.TempDir()); got != nil {
		t.Fatalf("expected nil instruction attacher when disabled, got %#v", got)
	}
}

func TestNewInstructionAttacherUsesCustomInstructionFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "IDIOM.md", "Project-specific guidance")

	attacher := newInstructionAttacher(config.Chat{InstructionFiles: []string{"IDIOM.md"}}, root)
	if attacher == nil {
		t.Fatal("expected instruction attacher")
	}
	attachment := attacher.OnFileTouch(filepath.Join(root, "src", "file.go"))
	if attachment == nil || !strings.Contains(attachment.Content, "Project-specific guidance") {
		t.Fatalf("expected custom instruction attachment, got %#v", attachment)
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

func TestBuildPromptExecutionPlanHonorsForcedTaskFamily(t *testing.T) {
	model := &Model{}
	model.SetForcedTaskFamily("implementation")

	plan := model.buildPromptExecutionPlan(context.Background(), "Do not run tests; just inspect this.")
	if plan.route.Family != "implementation" {
		t.Fatalf("family = %q, want implementation", plan.route.Family)
	}
	if plan.route.InitialLane != "implement" || plan.runtimeBrief.Contract != "implementation_result" {
		t.Fatalf("unexpected forced implementation plan: %#v brief=%#v", plan.route, plan.runtimeBrief)
	}
}

func TestBuildPromptExecutionPlanSkipsClassifierWhenTaskFamilyForced(t *testing.T) {
	var classifierCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&classifierCalls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"family\":\"architecture\"}"}}]}`))
	}))
	defer server.Close()

	model := &Model{
		cfg:    config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		client: llama.NewClient(server.URL, time.Second),
	}
	model.SetForcedTaskFamily("implementation")

	plan := model.buildPromptExecutionPlan(context.Background(), "Run tests and only report results.")
	if plan.route.Family != "implementation" || plan.route.InitialLane != "implement" {
		t.Fatalf("unexpected forced implementation plan: %#v", plan.route)
	}
	if got := atomic.LoadInt32(&classifierCalls); got != 0 {
		t.Fatalf("forced task family should bypass classifier, got %d classifier calls", got)
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
