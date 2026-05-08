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

func TestPlannerOwnedVerificationMarksImplementationBrief(t *testing.T) {
	model := &Model{}
	model.SetForcedTaskFamily("implementation")
	model.SetPlannerOwnedVerification(true)

	plan := model.buildPromptExecutionPlan(context.Background(), "Modify the file and leave tests to the caller.")

	if !slices.Contains(plan.runtimeBrief.Constraints, "planner_owned_verification") {
		t.Fatalf("expected planner-owned verification constraint, got %#v", plan.runtimeBrief.Constraints)
	}
	if !slices.Contains(plan.runtimeBrief.InvalidActions, "build_or_test_shell") {
		t.Fatalf("expected build/test shell invalid action, got %#v", plan.runtimeBrief.InvalidActions)
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

func TestBuildPromptExecutionPlanSkipsClassifierWhenDisabled(t *testing.T) {
	var classifierCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&classifierCalls, 1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"family\":\"architecture\"}"}}]}`))
	}))
	defer server.Close()

	model := &Model{
		cfg: config.Config{
			Chat:            config.Chat{ProjectRoot: t.TempDir()},
			RouteClassifier: config.RouteClassifier{Enabled: false},
		},
		client: llama.NewClient(server.URL, time.Second),
	}

	plan := model.buildPromptExecutionPlan(context.Background(), "Run the tests and report failures.")
	if plan.route.Family != "verification_only" {
		t.Fatalf("expected fallback route when classifier disabled, got %#v", plan.route)
	}
	if got := atomic.LoadInt32(&classifierCalls); got != 0 {
		t.Fatalf("disabled route classifier should not call model, got %d classifier calls", got)
	}
}

func TestBuildPromptExecutionPlanCapturesRequiredEditPathsAndExactAnchor(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files To Modify

| File | Purpose |
| --- | --- |
| native/src/simulation/job_queue.cpp | Update queue logic |
| scripts/ui/status_panel.gd | Update UI wiring |

Old text:
enqueue_loader_job();

New text:
enqueue_loader_job_once();
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{"native/src/simulation/job_queue.cpp", "scripts/ui/status_panel.gd"} {
		if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, want) {
			t.Fatalf("expected required edit path %q, got %#v", want, plan.runtimeBrief.RequiredEditPaths)
		}
	}
	if !slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation") {
		t.Fatalf("expected exact-anchor constraint, got %#v", plan.runtimeBrief.Constraints)
	}
	if !slices.Contains(plan.runtimeBrief.InvalidActions, "broad_read_before_first_edit_attempt") {
		t.Fatalf("expected broad-read invalid action, got %#v", plan.runtimeBrief.InvalidActions)
	}
}

func TestBuildPromptExecutionPlanCapturesRequiredEditSnippets(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files You May Edit

- native/tests/test_production_graph.cpp

## Verified Current State

` + "```cpp" + `
TEST_CASE("old behavior") {}
` + "```" + `

## Required Edits

Add the logistics include:
` + "```cpp" + `
#include "core/constants/logistics.h"
` + "```" + `

Insert the demand-pressure regression:
` + "```cpp" + `
TEST_CASE("demand pressure caps production") {
	CHECK(DEMAND_PRESSURE_CAP > 0.0);
}
` + "```" + `

## Planner-Owned Validation

The planner will run the test slice.
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if got := len(plan.runtimeBrief.RequiredEditSnippets); got != 2 {
		t.Fatalf("expected two required edit snippets, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets)
	}
	for i, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		if snippet.ID == "" {
			t.Fatalf("snippet %d missing id: %#v", i, snippet)
		}
		if snippet.Path != "native/tests/test_production_graph.cpp" {
			t.Fatalf("snippet %d path = %q, want test file", i, snippet.Path)
		}
		if strings.Contains(snippet.Content, "old behavior") {
			t.Fatalf("verified-current-state snippet should not be captured: %#v", snippet)
		}
	}
	if !strings.Contains(plan.runtimeBrief.RequiredEditSnippets[0].Content, `#include "core/constants/logistics.h"`) {
		t.Fatalf("missing include snippet: %#v", plan.runtimeBrief.RequiredEditSnippets)
	}
	if !strings.Contains(plan.runtimeBrief.RequiredEditSnippets[1].Content, "demand pressure caps production") {
		t.Fatalf("missing regression snippet: %#v", plan.runtimeBrief.RequiredEditSnippets)
	}
}

func TestBuildPromptExecutionPlanCapturesFilesYouMayEditAllowlist(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files You May Edit

- native/src/simulation/world/megaflora_stamper.h
- native/src/simulation/world/megaflora_stamper.cpp
- scripts/world/megaflora_stamper.gd

Implement the patch in the named files only.
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		"native/src/simulation/world/megaflora_stamper.h",
		"native/src/simulation/world/megaflora_stamper.cpp",
		"scripts/world/megaflora_stamper.gd",
	} {
		if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, want) {
			t.Fatalf("expected required edit path %q, got %#v", want, plan.runtimeBrief.RequiredEditPaths)
		}
	}
}

func TestNewHonorsAttachInstructionFilesConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Chat.AttachInstructionFiles = false
	model := New(cfg)
	if model.instructionAttacher != nil {
		t.Fatal("expected dynamic instruction attachment to be disabled")
	}

	cfg.Chat.AttachInstructionFiles = true
	model = New(cfg)
	if model.instructionAttacher == nil {
		t.Fatal("expected dynamic instruction attachment to be enabled")
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

func TestReloadPromptStackRejectsMissingExplicitPromptByDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{SystemPromptFiles: []string{missing}},
		},
	}

	if err := model.reloadPromptStackWithProfiles(nil); err == nil {
		t.Fatalf("reloadPromptStackWithProfiles should reject missing explicit prompts by default")
	}
}

func TestReloadPromptStackCanDegradeMissingExplicitPromptWhenConfigured(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				SystemPromptFiles:   []string{missing},
				AllowMissingPrompts: true,
			},
		},
	}

	if err := model.reloadPromptStackWithProfiles(nil); err != nil {
		t.Fatalf("reloadPromptStackWithProfiles should degrade missing prompts when configured: %v", err)
	}
	if !strings.Contains(model.contextError, "prompt context degraded") {
		t.Fatalf("expected context warning, got %q", model.contextError)
	}
	if len(model.promptStack.Warnings) == 0 {
		t.Fatalf("expected prompt stack warnings")
	}
}
