package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/promptstack"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/contracts/plan"
	"idiom/internal/workflows/planexecute"
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

func TestSystemPromptAutoAppendsQwenCompatibilityOverlay(t *testing.T) {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "auto"},
		},
		promptStack: promptstack.Stack{
			Text: "Base behavior",
			Sources: []promptstack.Source{{
				Kind:        "baseline",
				DisplayName: "base.md",
			}},
		},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	if !strings.Contains(prompt, "[model compatibility: model:qwen]") {
		t.Fatalf("expected qwen compatibility overlay, got:\n%s", prompt)
	}

	if !slices.Contains(model.contextSourceNames(), "model:qwen") {
		t.Fatalf("expected qwen overlay in context sources, got %#v", model.contextSourceNames())
	}
}

func TestSystemPromptSkipsQwenOverlayForGenericFamily(t *testing.T) {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "generic"},
		},
		promptStack:   promptstack.Stack{Text: "Base behavior"},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	if strings.Contains(prompt, "model:qwen") {
		t.Fatalf("expected generic model family to suppress qwen overlay, got:\n%s", prompt)
	}
}

func TestSystemPromptUsesResolvedPlannerSpecForModelOverlay(t *testing.T) {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "auto"},
		},
		plannerSpec:   config.LlamaServer{Kind: "zai", Model: "glm-5.1"},
		promptStack:   promptstack.Stack{Text: "Base behavior"},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	if strings.Contains(prompt, "model:qwen") {
		t.Fatalf("resolved non-Qwen planner should suppress inherited config overlay, got:\n%s", prompt)
	}
}

func TestSystemPromptCanForceQwenOverlayForModelAlias(t *testing.T) {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "local-coder"},
			Chat:        config.Chat{ModelFamily: "qwen"},
		},
		promptStack:   promptstack.Stack{Text: "Base behavior"},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	if !strings.Contains(prompt, "model:qwen") {
		t.Fatalf("expected explicit qwen family to append overlay, got:\n%s", prompt)
	}
}

func TestContextSourcesIncludeModePromptFiles(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "prompts", "base.md")
	planPath := filepath.Join(dir, "prompts", "plan-mode.md")

	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}

	if err := os.WriteFile(basePath, []byte("Base behavior\n"), 0o644); err != nil {
		t.Fatalf("write base prompt: %v", err)
	}

	if err := os.WriteFile(planPath, []byte("Plan behavior\n"), 0o644); err != nil {
		t.Fatalf("write plan prompt: %v", err)
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
		agentModeName: "plan",
	}

	prompt := model.systemPrompt()
	if strings.Count(prompt, "Base behavior") != 1 || !strings.Contains(prompt, "Plan behavior") {
		t.Fatalf("expected deduped base and loaded plan prompt, got:\n%s", prompt)
	}

	if !slices.Contains(model.contextSourceNames(), "mode:plan:plan-mode.md") {
		t.Fatalf("expected mode prompt in context sources, got %#v", model.contextSourceNames())
	}
}

func TestSystemPromptSourcesUseCachedModePromptUntilInvalidated(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "prompts", "plan-mode.md")

	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}

	if err := os.WriteFile(planPath, []byte("Plan behavior\n"), 0o644); err != nil {
		t.Fatalf("write plan prompt: %v", err)
	}

	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: dir}},
		promptStack:   promptstack.Stack{Text: "Base behavior"},
		agentModeName: "plan",
	}

	first := model.systemPrompt()
	if !strings.Contains(first, "Plan behavior") {
		t.Fatalf("expected initial mode prompt, got:\n%s", first)
	}

	if err := os.Remove(planPath); err != nil {
		t.Fatalf("remove plan prompt: %v", err)
	}

	if got := model.contextSummary(); !strings.Contains(got, "mode:plan:plan-mode.md") {
		t.Fatalf("expected context summary to use cached prompt sources, got %q", got)
	}

	second := model.systemPrompt()
	if second != first {
		t.Fatalf("expected cached system prompt after mode file removal\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	model.invalidateComposedPrompt()

	if prompt := model.systemPrompt(); strings.Contains(prompt, "Plan behavior") {
		t.Fatalf("expected invalidation to observe missing mode prompt, got:\n%s", prompt)
	}
}

func TestRoleSpecExplicitAliasCanTargetNonDefaultProfile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	body := `{
		"default_profile": "qwen-pair",
		"profiles": {
			"qwen-pair": {
				"models": [
					{"alias": "qwen3.6-35b", "model_path": "/models/qwen3.6-35b", "port": 47780, "roles": ["planner"]},
					{"alias": "qwen3-coder-30b", "model_path": "/models/qwen3-coder-30b", "port": 47781, "roles": ["executor"]}
				]
			},
			"qwen-27b-pair": {
				"models": [
					{"alias": "qwen3.6-27b", "model_path": "/models/qwen3.6-27b", "port": 47783, "roles": ["planner"]},
					{"alias": "qwen3-coder-30b", "model_path": "/models/qwen3-coder-30b", "port": 47781, "roles": ["executor"]}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	spec, ok := roleSpec(root, backends.RolePlanner, config.LlamaServer{
		Kind:  "mlx",
		Host:  "127.0.0.1",
		Port:  47680,
		Model: "qwen3.6-27b",
	})

	if !ok {
		t.Fatalf("expected explicit 27B alias to resolve")
	}

	if spec.Port != 47783 || spec.Model != "/models/qwen3.6-27b" {
		t.Fatalf("resolved spec = %+v, want 27B profile target", spec)
	}
}

func TestRoleSpecExecutorIgnoresExplicitPlannerAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	body := `{
		"default_profile": "qwen-pair",
		"profiles": {
			"qwen-pair": {
				"models": [
					{"alias": "qwen3.6-35b", "model_path": "/models/qwen3.6-35b", "port": 47780, "roles": ["planner"]},
					{"alias": "qwen3-coder-30b", "model_path": "/models/qwen3-coder-30b", "port": 47781, "roles": ["executor"]}
				]
			},
			"qwen-27b-pair": {
				"models": [
					{"alias": "qwen3.6-27b", "model_path": "/models/qwen3.6-27b", "port": 47783, "roles": ["planner"]},
					{"alias": "qwen3-coder-30b", "model_path": "/models/qwen3-coder-30b", "port": 47781, "roles": ["executor"]}
				]
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	spec, ok := roleSpec(root, backends.RoleExecutor, config.LlamaServer{
		Kind:  "mlx",
		Host:  "127.0.0.1",
		Port:  47680,
		Model: "qwen3.6-27b",
	})

	if !ok {
		t.Fatalf("expected executor role to resolve")
	}

	if spec.Port != 47781 || spec.Model != "/models/qwen3-coder-30b" {
		t.Fatalf("resolved spec = %+v, want coder executor", spec)
	}
}

func TestTuneChatOptionsForReadOnlyPlanDisablesThinkingAndCapsTokens(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		Temperature:      0.95,
		TopP:             0.95,
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

	if !tuned.ChatTemplateKwargs {
		t.Fatalf("expected read-only lane to send explicit chat template kwargs")
	}

	if tuned.Temperature != 0.2 || tuned.TopP != 1 {
		t.Fatalf("read-only sampling = temp %.2f top_p %.2f, want deterministic cap", tuned.Temperature, tuned.TopP)
	}

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected grounded explanation token floor of 1536 (8192 exceeds floor, should stay 8192), got %d", tuned.MaxTokens)
	}
}

func TestTuneChatOptionsForReadOnlyPlanPreservesLowerConfiguredTemperature(t *testing.T) {
	thinkingOn := true
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "conversation_only",
			Contract:          "direct_answer",
		},
	}

	opts := llama.ChatOptions{
		Temperature:      0.1,
		TopP:             0.8,
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	if tuned.Temperature != 0.1 || tuned.TopP != 1 {
		t.Fatalf("read-only low sampling = temp %.2f top_p %.2f, want low temp preserved with deterministic top_p", tuned.Temperature, tuned.TopP)
	}

	opts.Temperature = 0
	tuned = tuneChatOptionsForPlan(opts, plan)

	if tuned.Temperature != 0 || tuned.TopP != 1 {
		t.Fatalf("read-only zero sampling = temp %.2f top_p %.2f, want explicit zero temp preserved", tuned.Temperature, tuned.TopP)
	}
}

func TestTuneChatOptionsForPlanExecutePlannerUsesStrictLocalToolMode(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		Temperature:      0.6,
		TopP:             0.95,
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        agent.PlanThenExecutePlannerTaskFamily,
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)
	if tuned.Temperature != 0 || tuned.TopP != 1 {
		t.Fatalf("plan-execute planner sampling = temp %.2f top_p %.2f, want deterministic tool sampling", tuned.Temperature, tuned.TopP)
	}

	if tuned.EnableThinking == nil || *tuned.EnableThinking {
		t.Fatalf("plan-execute planner should disable local thinking for strict tool emission, got %#v", tuned.EnableThinking)
	}

	if tuned.PreserveThinking {
		t.Fatalf("plan-execute planner should strip hidden reasoning from follow-up history")
	}

	if !tuned.ChatTemplateKwargs {
		t.Fatalf("plan-execute planner should send explicit thinking kwargs")
	}

	if tuned.MaxTokens != 8192 {
		t.Fatalf("plan-execute planner should not inherit read-only token clamps, got %d", tuned.MaxTokens)
	}
}

func TestTuneChatOptionsForImplementationLaneKeepsConfiguredSampling(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		Temperature:      0.6,
		TopP:             0.95,
		EnableThinking:   &thinkingOn,
		PreserveThinking: true,
		MaxTokens:        8192,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "implementation",
			Contract:          "implementation_result",
			PermissionProfile: "guarded_workspace_write",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)
	if tuned.Temperature != 0.6 || tuned.TopP != 0.95 {
		t.Fatalf("implementation sampling changed unexpectedly: temp %.2f top_p %.2f", tuned.Temperature, tuned.TopP)
	}

	if tuned.EnableThinking == nil || !*tuned.EnableThinking || !tuned.PreserveThinking {
		t.Fatalf("implementation thinking config should stay as configured, got %#v preserve=%t", tuned.EnableThinking, tuned.PreserveThinking)
	}
}

func TestChatOptionsCanForceThinkingOffForRun(t *testing.T) {
	thinkingOn := true
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		modelID: "qwen3.6-35b",
	}

	model.SetDisableThinkingOverride(true)

	opts := model.chatOptions()

	if opts.Model != "qwen3.6-35b" {
		t.Fatalf("model = %q", opts.Model)
	}

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("expected thinking disabled, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking {
		t.Fatalf("expected preserve thinking off")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("expected explicit chat template kwargs for thinking override")
	}
}

func TestChatOptionsSendsTemplateKwargsForConfigThinkingOff(t *testing.T) {
	thinkingOff := false
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOff,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		modelID: "qwen3.6-35b",
	}

	opts := model.chatOptions()

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatalf("expected config thinking disabled, got %#v", opts.EnableThinking)
	}

	if opts.PreserveThinking != true {
		t.Fatalf("expected preserve thinking to follow config")
	}

	if !opts.ChatTemplateKwargs {
		t.Fatalf("expected explicit chat template kwargs for config thinking off")
	}
}

func TestChatOptionsOmitsTemplateKwargsForConfigThinkingOn(t *testing.T) {
	thinkingOn := true
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		modelID: "qwen3.6-35b",
	}

	opts := model.chatOptions()

	if opts.EnableThinking == nil || !*opts.EnableThinking {
		t.Fatalf("expected config thinking enabled, got %#v", opts.EnableThinking)
	}

	if opts.ChatTemplateKwargs {
		t.Fatalf("did not expect template kwargs for config thinking on")
	}
}

func TestApplyMaxTokensOverrideLeavesRouteClampWhenUnset(t *testing.T) {
	opts := llama.ChatOptions{MaxTokens: 8192}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "grounded_explanation",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 0)

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected grounded explanation token floor of 1536 (8192 exceeds floor, should stay 8192), got %d", tuned.MaxTokens)
	}
}

func TestApplyMaxTokensOverrideWinsOverRouteClamp(t *testing.T) {
	opts := llama.ChatOptions{MaxTokens: 8192}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "grounded_explanation",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 4096)

	if tuned.MaxTokens != 4096 {
		t.Fatalf("expected max-token override, got %d", tuned.MaxTokens)
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

func TestBuildPromptExecutionPlanCapturesMentionedImplementationRefsAsEvidencePaths(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("review_then_implement")

	plan := model.buildPromptExecutionPlan(context.Background(), "Investigate the routing flow in this codebase and then update docs/frontier-routing-implementation-plan.md with what you find.")

	if plan.route.Family != "review_then_implement" {
		t.Fatalf("unexpected family: %q", plan.route.Family)
	}

	if len(plan.runtimeBrief.TargetRefs) != 0 {
		t.Fatalf("natural implementation prose should not make mentioned paths target refs, got %#v", plan.runtimeBrief.TargetRefs)
	}

	if !slices.Contains(plan.runtimeBrief.EvidencePaths, "docs/frontier-routing-implementation-plan.md") {
		t.Fatalf("expected mentioned implementation path as evidence, got %#v", plan.runtimeBrief.EvidencePaths)
	}

	if slices.Contains(plan.runtimeBrief.RequiredNext, "gather source evidence before large reads of named target refs") {
		t.Fatalf("mentioned implementation evidence paths must not trigger target-read gate, got %#v", plan.runtimeBrief.RequiredNext)
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

func TestPlanExecutePromptRuntimeBriefDoesNotAdvertiseImplementationActions(t *testing.T) {
	model := &Model{}
	model.SetForcedTaskFamily(agent.PlanThenExecutePlannerTaskFamily)
	model.SetPlannerOwnedVerification(true)

	prompt := `
# IMPLEMENTATION REQUEST

## Files To Modify

| File | Purpose |
| --- | --- |
| internal/surfaces/tui/app/prompt_routing.go | Align runtime brief guidance |

Old text:
oldRuntimeBrief()

New text:
newRuntimeBrief()
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)
	joinedNext := strings.Join(plan.runtimeBrief.RequiredNext, "\n")
	joinedPolicy := strings.Join(plan.runtimeBrief.SearchPolicy, "\n")
	joinedInvalid := strings.Join(plan.runtimeBrief.InvalidActions, "\n")

	for _, want := range []string{
		"source-body obligations before submit_plan",
		"submit_plan scope",
		"packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
		"do not run build, test, shell, git, or source_sanity in the plan-execute planner phase",
	} {
		if !strings.Contains(joinedNext+"\n"+joinedPolicy, want) {
			t.Fatalf("plan-execute runtime brief missing %q\nnext=%#v\npolicy=%#v", want, plan.runtimeBrief.RequiredNext, plan.runtimeBrief.SearchPolicy)
		}
	}

	for _, forbidden := range []string{
		"edit an in-scope required path",
		"after source edits run source_sanity",
	} {
		if strings.Contains(joinedNext, forbidden) {
			t.Fatalf("plan-execute runtime brief retained implementation action %q\nnext=%#v", forbidden, plan.runtimeBrief.RequiredNext)
		}
	}

	if !strings.Contains(joinedInvalid, "source_sanity_in_planner_phase") {
		t.Fatalf("plan-execute runtime brief should reject planner-phase source_sanity, got %#v", plan.runtimeBrief.InvalidActions)
	}
}

func TestPlanExecuteRuntimeBriefUsesTypedCandidateScopeInsteadOfPromptSchema(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	restore := model.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
		ScopeContract: planexecute.NewPlanScopeContract(nil, []string{
			"internal/surfaces/tui/app/rendering.go",
			"internal/workflows/productworkflow/status.go",
		}, nil),
	}, io.Discard)

	defer restore()

	prompt := `
# Plan-then-execute planner phase

allowed_edit_paths:
- internal/surfaces/tui/app/rendering.go
- internal/workflows/productworkflow/status.go

The planner schema supports edit entries with old_text and new_text fields.
Each submit_plan edit may include old_text/new_text copied from source evidence.
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if len(plan.runtimeBrief.RequiredEditPaths) != 0 {
		t.Fatalf("typed plan-execute candidate scope must not become required coverage, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	for _, want := range []string{"internal/surfaces/tui/app/rendering.go", "internal/workflows/productworkflow/status.go"} {
		if !slices.Contains(plan.runtimeBrief.AllowedEditPaths, want) {
			t.Fatalf("typed plan-execute allowed paths missing %q, got %#v", want, plan.runtimeBrief.AllowedEditPaths)
		}

		if !slices.Contains(plan.runtimeBrief.TargetRefs, want) {
			t.Fatalf("typed plan-execute target refs missing %q, got %#v", want, plan.runtimeBrief.TargetRefs)
		}
	}

	if slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation") {
		t.Fatalf("generated schema text must not add exact-anchor constraints in typed plan-execute scope, got %#v", plan.runtimeBrief.Constraints)
	}
}

func TestPlanExecuteReviewRepairRuntimeBriefCarriesFailedPhaseObligations(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	restore := model.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily:                  agent.PlanThenExecuteReviewRepairTaskFamily,
		ReviewRepairOpenObligations: []string{"verification_failed:go test ./..."},
	}, io.Discard)

	defer restore()

	plan := model.buildPromptExecutionPlan(context.Background(), "review repair")
	if plan.runtimeBrief.Readiness == nil || !slices.Contains(plan.runtimeBrief.Readiness.OpenObligations, "verification_failed:go test ./...") {
		t.Fatalf("review-repair runtime brief should carry failed phase obligations, got %#v", plan.runtimeBrief.Readiness)
	}
}

func TestPlanExecuteRuntimeBriefKeepsRealCandidateScopeExactSnippets(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	restore := model.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
		ScopeContract: planexecute.NewPlanScopeContract(nil, []string{
			"native/src/feature.cpp",
		}, nil),
	}, io.Discard)

	defer restore()

	prompt := `
# Plan-then-execute planner phase

allowed_edit_paths:
- native/src/feature.cpp

## Required Edits

In native/src/feature.cpp, preserve this exact replacement body:
` + "```cpp" + `
bool Hardened() {
	return true;
}
` + "```" + `
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if len(plan.runtimeBrief.RequiredEditPaths) != 0 {
		t.Fatalf("candidate scope snippets must not promote required paths, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	if got := len(plan.runtimeBrief.RequiredEditSnippets); got != 1 {
		t.Fatalf("expected one candidate-scope exact snippet, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets)
	}

	if plan.runtimeBrief.RequiredEditSnippets[0].Path != "native/src/feature.cpp" {
		t.Fatalf("snippet path = %q, want candidate source path", plan.runtimeBrief.RequiredEditSnippets[0].Path)
	}

	if !slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation") {
		t.Fatalf("real candidate-scope snippets should keep exact-anchor constraint, got %#v", plan.runtimeBrief.Constraints)
	}

	if !slices.Contains(plan.runtimeBrief.SearchPolicy, "preserve_exact_anchor_snippets_in_submit_plan_before_broad_discovery") {
		t.Fatalf("plan-execute exact snippet should use submit_plan search policy, got %#v", plan.runtimeBrief.SearchPolicy)
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
	if plan.route.Family != "conversation" {
		t.Fatalf("expected conservative fallback route when classifier disabled, got %#v", plan.route)
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

	if !slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope") {
		t.Fatalf("expected required-path write-scope constraint, got %#v", plan.runtimeBrief.Constraints)
	}

	if !slices.Contains(plan.runtimeBrief.InvalidActions, "broad_read_before_first_edit_attempt") {
		t.Fatalf("expected broad-read invalid action, got %#v", plan.runtimeBrief.InvalidActions)
	}

	if !slices.Contains(plan.runtimeBrief.SearchPolicy, "treat required_edit_paths plus packet evidence as edit scope; prefer semantic/context/declared project discovery before narrow required-path reads for named edit-safety facts") {
		t.Fatalf("expected required-path scoped search policy, got %#v", plan.runtimeBrief.SearchPolicy)
	}
}

func TestBuildPromptExecutionPlanCarriesObjectiveIntoRuntimeBrief(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily(agent.PlanThenExecutePlannerTaskFamily)

	prompt := "Smoke test without changing files and answer source-satisfied with evidence. Required edit paths: internal/surfaces/tui/app/app.go, internal/runtime/agent/runner.go."
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if plan.runtimeBrief.Objective != prompt {
		t.Fatalf("runtime brief objective = %q, want original prompt", plan.runtimeBrief.Objective)
	}

	if plan.runtimeBrief.TaskFamily != agent.PlanThenExecutePlannerTaskFamily {
		t.Fatalf("expected plan-execute planner family, got %q", plan.runtimeBrief.TaskFamily)
	}
}

func TestPlanExecutePromptRunnerDirectQuestionUsesConversationOnlyBrief(t *testing.T) {
	root := t.TempDir()
	model := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: root},
			Workspace: config.Workspace{Root: root, CWD: root},
		},
		agentModeName: "build",
	}
	restore := model.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: "direct_question",
	}, io.Discard)

	defer restore()

	plan := model.buildPromptExecutionPlan(context.Background(), "answer directly")
	if plan.runtimeBrief.PermissionProfile != "conversation_only" {
		t.Fatalf("permission profile = %q, want conversation_only", plan.runtimeBrief.PermissionProfile)
	}

	if len(plan.runtimeBrief.AllowedTools) != 0 || len(plan.runtimeBrief.RequiredEvidence) != 0 {
		t.Fatalf("direct-question prompt runner should not advertise workspace evidence gates, brief=%#v", plan.runtimeBrief)
	}
}

func TestBuildPromptExecutionPlanScopesWrappedPlanExecuteObjective(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily(agent.PlanThenExecutePlannerTaskFamily)

	wrapped := strings.Repeat("planner addendum text ", 80) + `
<!-- IDIOM:BRIEF-BODY-START -->
# Objective

Smoke test without changing files and answer source-satisfied with evidence.

## Files to Modify

required_edit_paths:
- internal/surfaces/tui/app/app.go
- internal/runtime/agent/runner.go
<!-- IDIOM:BRIEF-BODY-END -->`
	plan := model.buildPromptExecutionPlan(context.Background(), wrapped)

	if strings.Contains(plan.runtimeBrief.Objective, "planner addendum text") {
		t.Fatalf("runtime brief objective leaked planner addendum: %q", plan.runtimeBrief.Objective)
	}

	if !strings.Contains(plan.runtimeBrief.Objective, "source-satisfied") || !strings.Contains(plan.runtimeBrief.Objective, "without changing files") {
		t.Fatalf("runtime brief objective lost source-satisfied no-write intent: %q", plan.runtimeBrief.Objective)
	}
}

func TestRequiredEditPathsTableDoesNotCaptureProseSlashTokens(t *testing.T) {
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
| native/tests/test_funeral.cpp | Add STRANGER, BEST_FRIEND, and RIVAL/NEMESIS unburied penalty coverage |
| native/src/simulation/creatures/social_welfare.cpp | Scale the emitted mood value |
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{"native/tests/test_funeral.cpp", "native/src/simulation/creatures/social_welfare.cpp"} {
		if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, want) {
			t.Fatalf("expected required edit path %q, got %#v", want, plan.runtimeBrief.RequiredEditPaths)
		}
	}

	if slices.Contains(plan.runtimeBrief.RequiredEditPaths, "RIVAL/NEMESIS") {
		t.Fatalf("slash-shaped prose leaked into required edit paths: %#v", plan.runtimeBrief.RequiredEditPaths)
	}
}

func TestBuildPromptExecutionPlanDoesNotTreatGenericAnchorGuidanceAsExactAnchor(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
Modify only the allowlisted files named in the brief. Exact anchors, exact
old/new snippets, Current-State Anchors, Required Edits, or unified patches in
the brief are sufficient source evidence. For edit_file line-range edits,
content must be only the final replacement; prefer an exact old_text/new_text
edit when available.

## Files To Modify

| File | Action |
|---|---|
| native/tests/test_track_system.cpp | Add live integration coverage. |

## Verified Current State

` + "```cpp" + `
TEST_CASE("existing pattern") {
    ColonySim sim;
}
` + "```" + `

## Required Behavior

Add a new test that wires the existing systems together using local APIs.
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_track_system.cpp") {
		t.Fatalf("expected required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	if slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation") {
		t.Fatalf("generic launcher guidance and verified-state snippets should not force exact-anchor write-first: %#v", plan.runtimeBrief.Constraints)
	}

	if slices.Contains(plan.runtimeBrief.InvalidActions, "broad_read_before_first_edit_attempt") {
		t.Fatalf("non-exact add-test brief should allow bounded prewrite reads, got %#v", plan.runtimeBrief.InvalidActions)
	}

	if !slices.Contains(plan.runtimeBrief.RequiredNext, "use semantic/context/declared project discovery or narrow reads only for one named edit-safety fact; after that fact is resolved, edit an in-scope required path or report <<QUESTION>>") {
		t.Fatalf("expected non-exact required-path packet to bias toward discovery/read/edit/block after bounded probes, got %#v", plan.runtimeBrief.RequiredNext)
	}
}

func TestBuildPromptExecutionPlanCapturesRichContextPacketEvidence(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files You May Edit

- native/tests/test_transport_system.cpp

## Context Packet

{
  "schema": "planning_packet_v2",
  "candidate_scope": [
    {"path": "native/src/simulation/transport/route_graph.cpp", "confidence": "high"}
  ],
  "evidence": [
    {"source": "native/src/simulation/transport/route_graph.h", "symbol": "RouteGraph"}
  ],
  "anchors": [
    {"file": "native/tests/test_transport_system.cpp", "line": 42}
  ]
}

## Verified Current State

` + "```cpp" + `
TEST_CASE("routes connect depots") {}
` + "```" + `

## Required Edits

` + "```cpp" + `
TEST_CASE("routes reject disconnected depots") {}
` + "```" + `
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		"prompt_context:context_packet",
		"prompt_context:verified_current_state",
		"prompt_context:required_edits",
		"prompt_context:structured_packet",
	} {
		if !slices.Contains(plan.runtimeBrief.TargetPacket, want) {
			t.Fatalf("expected target packet marker %q, got %#v", want, plan.runtimeBrief.TargetPacket)
		}
	}

	for _, want := range []string{
		"native/src/simulation/transport/route_graph.cpp",
		"native/src/simulation/transport/route_graph.h",
		"native/tests/test_transport_system.cpp",
	} {
		if !slices.Contains(plan.runtimeBrief.EvidencePaths, want) {
			t.Fatalf("expected context evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths)
		}
	}

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp") {
		t.Fatalf("expected required edit path from rich packet prompt, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}
}

func TestBuildPromptExecutionPlanCapturesColonStyleContextPacketEvidence(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
Context Packet:
- path: native/src/simulation/transport/route_graph.cpp
- source: native/src/simulation/transport/route_graph.h

Files To Modify:
- native/tests/test_transport_system.cpp
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if !slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:context_packet") {
		t.Fatalf("expected colon-style context packet marker, got %#v", plan.runtimeBrief.TargetPacket)
	}

	for _, want := range []string{
		"native/src/simulation/transport/route_graph.cpp",
		"native/src/simulation/transport/route_graph.h",
	} {
		if !slices.Contains(plan.runtimeBrief.EvidencePaths, want) {
			t.Fatalf("expected colon-style context evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths)
		}
	}

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp") {
		t.Fatalf("expected colon-style required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}
}

func TestBuildPromptExecutionPlanCapturesRequiredReadsAsEvidence(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := strings.Join([]string{
		"## Required reads (exactly 2)",
		"",
		"1. Previous draft: `.idiom/refine/example/iter-1/draft.md`",
		"2. Review with bug list: `.idiom/refine/example/iter-1/review.md`",
		"",
		"## Primary evidence paths (read-only)",
		"",
		"- `native/src/simulation/ai/appraisal.cpp`",
		"",
		"## Files To Modify",
		"",
		"| File | Change |",
		"|------|--------|",
		"| `.idiom/refine/example/iter-2/draft.md` | NEW FILE |",
	}, "\n")
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		".idiom/refine/example/iter-1/draft.md",
		".idiom/refine/example/iter-1/review.md",
		"native/src/simulation/ai/appraisal.cpp",
	} {
		if !slices.Contains(plan.runtimeBrief.EvidencePaths, want) {
			t.Fatalf("expected required-read evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths)
		}
	}

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, ".idiom/refine/example/iter-2/draft.md") {
		t.Fatalf("expected output draft as required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}
}

func TestPromptStructuredPacketMarkerRequiresPacketShape(t *testing.T) {
	if promptHasStructuredPacketMarker(`{"evidence":"one incidental field"}`) {
		t.Fatal("single generic evidence field should not be enough to mark a structured packet")
	}

	if promptHasStructuredPacketMarker(`Document how planning_packet_v2 is named in prompts.`) {
		t.Fatal("bare planning_packet prose should not mark a structured packet")
	}

	if promptHasStructuredPacketMarker(`Document the "schema" for planning_packet_v2 prompts.`) {
		t.Fatal("planning_packet prose plus quoted schema should not mark a structured packet")
	}

	if !promptHasStructuredPacketMarker(`{"candidate_scope":[{"path":"a/b.go"}],"evidence":[{"source":"a/c.go"}]}`) {
		t.Fatal("candidate scope plus evidence should mark a structured packet")
	}
}

func TestPromptContextPacketMarkerRequiresSectionContent(t *testing.T) {
	prompt := `
Context Packet:

Required Behavior:
- Explain the route.
`
	if markers := promptContextPacketMarkers(prompt); slices.Contains(markers, "prompt_context:context_packet") {
		t.Fatalf("empty context packet heading should not create marker, got %#v", markers)
	}
}

func TestPromptContextPacketMarkerAcceptsTrailingColonPathContent(t *testing.T) {
	prompt := `
Context Packet:
- internal/surfaces/tui/app/prompt_routing.go:
`
	if markers := promptContextPacketMarkers(prompt); !slices.Contains(markers, "prompt_context:context_packet") {
		t.Fatalf("path content ending in colon should create context packet marker, got %#v", markers)
	}
}

func TestPromptContextPacketMarkerAcceptsNestedEvidenceSection(t *testing.T) {
	prompt := `
Context Packet:
Evidence:
- internal/surfaces/tui/app/prompt_routing.go
`
	if markers := promptContextPacketMarkers(prompt); !slices.Contains(markers, "prompt_context:context_packet") {
		t.Fatalf("nested evidence should create context packet marker, got %#v", markers)
	}
}

func TestBuildPromptExecutionPlanCapturesStructuredRequiredEditPaths(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `{
  "schema": "planning_packet_v2",
  "files_to_modify": ["native/tests/test_transport_system.cpp"],
  "candidate_scope": [{"path": "native/src/simulation/transport/route_graph.cpp"}],
  "evidence": [{"source": "native/src/simulation/transport/route_graph.h"}]
}`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if !slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:structured_packet") {
		t.Fatalf("expected structured packet marker, got %#v", plan.runtimeBrief.TargetPacket)
	}

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp") {
		t.Fatalf("expected structured required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}
}

func TestStructuredRequiredEditPathsIgnoresNegativeAllowlistKeys(t *testing.T) {
	prompt := `{
  "not_allowed_files": ["native/src/secret.cpp"],
  "candidate_scope": [{"path": "native/src/simulation/transport/route_graph.cpp"}],
  "evidence": [{"source": "native/src/simulation/transport/route_graph.h"}]
}`
	if got := plancontract.RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("negative allowlist-like keys should not define required edit paths, got %#v", got)
	}
}

func TestStructuredAllowedEditPathsCapturesYAMLStyleWriteScope(t *testing.T) {
	prompt := `
Write Scope:
- native/tests/test_transport_system.cpp

Evidence:
- native/src/simulation/transport/route_graph.cpp
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	if len(got) != 0 {
		t.Fatalf("write_scope is candidate scope, not required edit coverage, got %#v", got)
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	if len(allowed) != 1 || allowed[0] != "native/tests/test_transport_system.cpp" {
		t.Fatalf("expected YAML-style write_scope candidate path only, got %#v", allowed)
	}
}

func TestEditableFilesHeadingIsCandidateScopeOnly(t *testing.T) {
	prompt := `
Editable Files:
- native/tests/test_transport_system.cpp
- native/src/simulation/transport/route_graph.cpp
`
	if got := RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("editable files must not define required edit coverage, got %#v", got)
	}

	allowed := AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"native/tests/test_transport_system.cpp", "native/src/simulation/transport/route_graph.cpp"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("editable files missing allowed path %q, got %#v", want, allowed)
		}
	}
}

func TestStructuredRequiredEditPathsInlineScalarDoesNotBleed(t *testing.T) {
	prompt := `
write_scope: native/tests/test_transport_system.cpp
evidence: native/src/simulation/transport/route_graph.cpp
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	if len(got) != 0 {
		t.Fatalf("write_scope is candidate scope, not required edit coverage, got %#v", got)
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	if len(allowed) != 1 || allowed[0] != "native/tests/test_transport_system.cpp" {
		t.Fatalf("expected inline write_scope candidate path only, got %#v", allowed)
	}
}

func TestRequiredEditPathsIgnoreAllowedEditPaths(t *testing.T) {
	prompt := `
## Allowed Edit Paths

- native/tests/test_transport_system.cpp
- native/src/simulation/transport/route_graph.cpp

## Evidence

- docs/design.md
`
	if got := requiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("allowed edit paths must not define required coverage, got %#v", got)
	}

	allowed := allowedEditPathsFromPrompt(prompt)
	for _, want := range []string{
		"native/tests/test_transport_system.cpp",
		"native/src/simulation/transport/route_graph.cpp",
	} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}

	if slices.Contains(allowed, "docs/design.md") {
		t.Fatalf("evidence path must not bleed into allowed edit paths, got %#v", allowed)
	}
}

func TestRequiredEditPathsCaptureInlineColonList(t *testing.T) {
	prompt := "Implement the change. Required edit paths: cmd/idiom/product_workflow.go, cmd/idiom/product_workflow_test.go"
	got := requiredEditPathsFromPrompt(prompt)

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing required path %q, got %#v", want, got)
		}
	}
}

func TestImplementationAtomPromptPreservesRequiredEditPathsAfterDesignDocReference(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `Implement one bounded TDD capability atom from docs/design-tdd-coverage.md, Phase 1: EARS and BDD Coverage Checks.

Goal: extend the product workflow TDD coverage gate so behavior_standard-or-higher coverage fails closed when EARS requirements are unmapped or BDD scenarios lack concrete data, using the existing tdd.json coverage metadata fields.

Context evidence: context_pack pack_id=059dc319e5447ef9 identified internal/workflows/productworkflow/coverage_gate.go, internal/workflows/productworkflow/coverage_gate_test.go, and cmd/idiom/product_workflow_test.go as the relevant implementation/test surfaces.

Required edit paths:
- internal/workflows/productworkflow/coverage_gate.go
- internal/workflows/productworkflow/coverage_gate_test.go
- cmd/idiom/product_workflow_test.go

Acceptance:
- Add or update focused tests that fail before the change.
- Preserve the current durable product workflow fixture pass path.`

	plan := model.buildPromptExecutionPlan(context.Background(), prompt)
	want := []string{
		"internal/workflows/productworkflow/coverage_gate.go",
		"internal/workflows/productworkflow/coverage_gate_test.go",
		"cmd/idiom/product_workflow_test.go",
	}

	if !slices.Equal(plan.runtimeBrief.RequiredEditPaths, want) {
		t.Fatalf("required edit paths = %#v, want %#v", plan.runtimeBrief.RequiredEditPaths, want)
	}

	if slices.Contains(plan.runtimeBrief.RequiredEditPaths, "docs/design-tdd-coverage.md") {
		t.Fatalf("design doc evidence must not become required edit scope: %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	if !slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope") {
		t.Fatalf("expected required-path write scope constraint, got %#v", plan.runtimeBrief.Constraints)
	}
}

func TestRequiredEditPathsRejectDirectoryLikeObligations(t *testing.T) {
	prompt := `
## Files To Modify

- cmd/idiom
- cmd/idiom/product_workflow.go
- Makefile
`
	got := requiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "Makefile"}

	if len(got) != len(want) {
		t.Fatalf("expected file-only required paths %#v, got %#v", want, got)
	}

	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Fatalf("missing required path %q, got %#v", path, got)
		}
	}

	if slices.Contains(got, "cmd/idiom") {
		t.Fatalf("directory-like target must not become an exact required edit path: %#v", got)
	}
}

func TestStructuredRequiredEditPathsIgnoreAllowedKeys(t *testing.T) {
	prompt := `{
  "allowed_edit_paths": ["native/src/a.cpp"],
  "allowed_files": ["native/src/b.cpp"],
  "evidence": ["native/src/c.cpp"]
}`
	if got := plancontract.RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("allowed structured keys must not define required coverage, got %#v", got)
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"native/src/a.cpp", "native/src/b.cpp"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}

	if slices.Contains(allowed, "native/src/c.cpp") {
		t.Fatalf("evidence path must not bleed into allowed edit paths, got %#v", allowed)
	}
}

func TestStructuredRequiredEditPathsSingleLineJSONDoesNotBleed(t *testing.T) {
	prompt := `{"required_edit_paths":["native/src/a.cpp"],"evidence":[".idiom/config.json","native/src/b.cpp"]}`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	if len(got) != 1 || got[0] != "native/src/a.cpp" {
		t.Fatalf("expected single-line JSON required paths only, got %#v", got)
	}
}

func TestStructuredRequiredEditPathsIgnoreInlineEvidenceCommand(t *testing.T) {
	prompt := `
Freshness:
- idiom tool context_status --config .idiom/config.json --input '{"refresh":true,"required_edit_paths":["native/src/a.cpp"],"limit":20}'
`
	if got := plancontract.RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("inline evidence commands must not define required edit paths, got %#v", got)
	}
}

func TestRequiredEditPathsIgnoreStructuredKeysInsideEvidenceCommands(t *testing.T) {
	prompt := `
# Atom

## Files To Modify

| File | Change |
|---|---|
| ` + "`native/src/a.cpp`" + ` | MODIFY source |
| ` + "`native/tests/test_a.cpp`" + ` | ADD coverage |

## Freshness / Staleness Gate

- idiom tool context_status --config .idiom/config.json --input '{"refresh":true,"required_edit_paths":["native/src/a.cpp","native/tests/test_a.cpp"],"limit":20}'
`
	got := requiredEditPathsFromPrompt(prompt)

	for _, want := range []string{"native/src/a.cpp", "native/tests/test_a.cpp"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing required edit path %q, got %#v", want, got)
		}
	}

	if slices.Contains(got, ".idiom/config.json") {
		t.Fatalf("evidence command config path must not become required edit path, got %#v", got)
	}

	if len(got) != 2 {
		t.Fatalf("expected only Files To Modify paths, got %#v", got)
	}
}

func TestRequiredEditPathsCapturesAllowlistTableAfterDescriptorLine(t *testing.T) {
	prompt := `
# Atom

## Files to Modify

Exhaustive allowlist for this atom:

| Path | Reason |
|---|---|
| ` + "`tests/e2e/suites/binding_surface_suite.gd`" + ` | Add consumer-contract case. |
| ` + "`.planning/campaigns/testing-coverage-expansion.md`" + ` | Update tracker. |
| ` + "`.planning/campaigns/testing-coverage-expansion-atom-6.3b-spec.md`" + ` | Update atom status. |

## Files to Read First

| Path | Why |
|---|---|
| ` + "`scripts/ui/shaft_panel.gd`" + ` | Evidence only. |
`
	got := requiredEditPathsFromPrompt(prompt)
	want := []string{
		"tests/e2e/suites/binding_surface_suite.gd",
		".planning/campaigns/testing-coverage-expansion.md",
		".planning/campaigns/testing-coverage-expansion-atom-6.3b-spec.md",
	}

	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Fatalf("missing required edit path %q, got %#v", path, got)
		}
	}

	if slices.Contains(got, "scripts/ui/shaft_panel.gd") {
		t.Fatalf("following evidence table must not become required edit paths, got %#v", got)
	}

	if len(got) != len(want) {
		t.Fatalf("expected only allowlist paths %#v, got %#v", want, got)
	}
}

func TestRequiredEditPathsDoNotBleedFromFollowingEvidenceSection(t *testing.T) {
	prompt := `
Files To Modify:

Evidence:
- native/src/simulation/transport/route_graph.cpp
`
	if got := requiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("empty edit section should not borrow evidence paths, got %#v", got)
	}
}

func TestRequiredEditPathsCapturesTrailingColonPathLine(t *testing.T) {
	prompt := `
Files To Modify:
- internal/surfaces/tui/app/prompt_routing.go:
`
	got := requiredEditPathsFromPrompt(prompt)

	if !slices.Contains(got, "internal/surfaces/tui/app/prompt_routing.go") {
		t.Fatalf("expected trailing-colon path to count as required edit path, got %#v", got)
	}
}

func TestContextEvidenceDoesNotBleedFromEmptyPacketHeading(t *testing.T) {
	prompt := `
Context Packet:

Required Behavior:
- internal/surfaces/tui/app/prompt_routing.go
`
	if got := promptContextEvidencePaths(prompt); len(got) != 0 {
		t.Fatalf("empty context packet should not borrow later behavior paths, got %#v", got)
	}
}

func TestRuntimeBriefCapturesHostCurrentStateAnchorEvidence(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetPlanExecuteScopeContract(planexecute.NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil))

	prompt := `
# Atom

## Files To Modify

- pkg/a.go

## Verified Current State

- Host current-state anchors captured before dispatch:
  - ` + "`pkg/a.go:3`: `const A = \"old\"`" + `
`

	brief := model.runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, agent.BuildMode(), prompt)
	if !slices.Contains(brief.EvidencePaths, "pkg/a.go") {
		t.Fatalf("expected host anchor path in EvidencePaths, got %#v", brief.EvidencePaths)
	}

	if !slices.Contains(brief.TargetPacket, "prompt_context:verified_current_state") {
		t.Fatalf("expected verified-current-state marker, got %#v", brief.TargetPacket)
	}
}

func TestPlanExecuteRuntimeBriefHistoryRequirementFollowsGitMetadata(t *testing.T) {
	nonGitRoot := t.TempDir()
	nonGit := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: nonGitRoot}},
		workspace:     workspace.NewManager(config.Workspace{Root: nonGitRoot, CWD: nonGitRoot}),
		agentModeName: "build",
	}
	prompt := "Use plan-execute to update pkg/a.go.\n\nrequired_edit_paths: pkg/a.go"
	brief := nonGit.runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, agent.BuildMode(), prompt)

	if slices.Contains(brief.RequiredEvidence, runtimeBriefGitHistoryEvidence) {
		t.Fatalf("non-git workspace must not require git history evidence: %#v", brief.RequiredEvidence)
	}

	if slices.Contains(brief.RequiredTools, runtimeBriefGitHistoryTool) {
		t.Fatalf("non-git workspace must not require git history tools: %#v", brief.RequiredTools)
	}

	gitRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(gitRoot, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	gitModel := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: gitRoot}},
		workspace:     workspace.NewManager(config.Workspace{Root: gitRoot, CWD: gitRoot}),
		agentModeName: "build",
	}
	gitBrief := gitModel.runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, agent.BuildMode(), prompt)

	if !slices.Contains(gitBrief.RequiredEvidence, runtimeBriefGitHistoryEvidence) {
		t.Fatalf("git workspace should retain git history evidence: %#v", gitBrief.RequiredEvidence)
	}

	if !slices.Contains(gitBrief.RequiredTools, runtimeBriefGitHistoryTool) {
		t.Fatalf("git workspace should retain git history tool requirement: %#v", gitBrief.RequiredTools)
	}
}

func TestPromptContainsExactEditAnchorRecognizesStructuredJSONFields(t *testing.T) {
	prompt := `Apply this exact edit:
{
  "old_text": "before();",
  "new_text": "after();"
}`
	if !promptContainsExactEditAnchor(prompt) {
		t.Fatal("expected structured old_text/new_text fields to count as an exact edit anchor")
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

func TestBuildPromptExecutionPlanAssignsMultiFileRequiredEditSnippetPaths(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files To Modify

| File | Change |
|---|---|
| ` + "`native/src/a.cpp`" + ` | Update source |
| ` + "`native/tests/test_a.cpp`" + ` | Update test |

## Required Edits

In ` + "`native/src/a.cpp`" + `, replace the source line.

old_text:

` + "```text" + `
int before();
` + "```" + `

new_text:

` + "```text" + `
int after();
` + "```" + `

In ` + "`native/tests/test_a.cpp`" + `, replace the test case.

old_text:

` + "```text" + `
TEST_CASE("before") {}
` + "```" + `

new_text:

` + "```text" + `
TEST_CASE("after") {}
` + "```" + `
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if got := len(plan.runtimeBrief.RequiredEditSnippets); got != 2 {
		t.Fatalf("expected two new snippets, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets)
	}

	got := map[string]string{}
	for _, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		got[snippet.Path] = snippet.Content
	}

	if !strings.Contains(got["native/src/a.cpp"], "int after();") {
		t.Fatalf("source snippet path/content not captured correctly: %#v", plan.runtimeBrief.RequiredEditSnippets)
	}

	if !strings.Contains(got["native/tests/test_a.cpp"], `TEST_CASE("after")`) {
		t.Fatalf("test snippet path/content not captured correctly: %#v", plan.runtimeBrief.RequiredEditSnippets)
	}
}

func TestBuildPromptExecutionPlanDuplicatesSharedRequiredEditSnippetPaths(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files To Modify

| File | Change |
|---|---|
| ` + "`native/src/a.h`" + ` | Update header comment |
| ` + "`native/src/a.cpp`" + ` | Update implementation comment |

## Required Edits

Update the ` + "`a.h`" + ` and ` + "`a.cpp`" + ` comments.

old_text:

` + "```text" + `
// old shared comment
` + "```" + `

new_text:

` + "```text" + `
// new shared comment
` + "```" + `
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	got := map[string]string{}
	for _, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		got[snippet.Path] = snippet.Content
	}

	for _, path := range []string{"native/src/a.h", "native/src/a.cpp"} {
		if !strings.Contains(got[path], "// new shared comment") {
			t.Fatalf("shared snippet was not duplicated for %s: %#v", path, plan.runtimeBrief.RequiredEditSnippets)
		}
	}
}

func TestBuildPromptExecutionPlanCapturesImplementationShapeSnippets(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files To Modify

- native/src/foo.cpp

## Implementation Shape

Add the helper:
` + "```cpp" + `
int helper(int x) { return x + 1; }
` + "```" + `

## Verification

Run the tests.
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if got := len(plan.runtimeBrief.RequiredEditSnippets); got != 1 {
		t.Fatalf("expected one snippet from Implementation Shape heading, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets)
	}

	if !strings.Contains(plan.runtimeBrief.RequiredEditSnippets[0].Content, "int helper(int x)") {
		t.Fatalf("snippet content missing helper body: %#v", plan.runtimeBrief.RequiredEditSnippets[0])
	}
}

func TestBuildPromptExecutionPlanCapturesFilesYouMayEditAsCandidateScope(t *testing.T) {
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
		if slices.Contains(plan.runtimeBrief.RequiredEditPaths, want) {
			t.Fatalf("files-you-may-edit path %q must not become required coverage, got %#v", want, plan.runtimeBrief.RequiredEditPaths)
		}

		if !slices.Contains(plan.runtimeBrief.TargetRefs, want) {
			t.Fatalf("expected candidate target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs)
		}

		if !slices.Contains(plan.runtimeBrief.AllowedEditPaths, want) {
			t.Fatalf("expected allowed edit scope %q, got %#v", want, plan.runtimeBrief.AllowedEditPaths)
		}
	}
}

func TestAllowedEditPathsFromPromptKeepsInlineScopeSentenceBounded(t *testing.T) {
	prompt := "Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go. Use pkg/evidence.go as read-only evidence."

	allowed := AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing inline allowed path %q, got %#v", want, allowed)
		}
	}

	if slices.Contains(allowed, "pkg/evidence.go") {
		t.Fatalf("read-only evidence sentence must not become allowed edit scope, got %#v", allowed)
	}
}

func TestBuildPromptExecutionPlanPromotesOnlyPayloadPathFromCandidateScope(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("implementation")

	prompt := `
# IMPLEMENTATION REQUEST

## Files You May Edit

- native/src/a.cpp
- native/src/b.cpp

## Required Edits

In ` + "`native/src/a.cpp`" + `, replace the source line.

old_text:

` + "```text" + `
int before();
` + "```" + `

new_text:

` + "```text" + `
int after();
` + "```" + `
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if !slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/src/a.cpp") {
		t.Fatalf("payload path should become required edit coverage, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	if slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/src/b.cpp") {
		t.Fatalf("candidate-only path must not become required edit coverage, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	for _, want := range []string{"native/src/a.cpp", "native/src/b.cpp"} {
		if !slices.Contains(plan.runtimeBrief.TargetRefs, want) {
			t.Fatalf("expected candidate target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs)
		}
	}

	if got := len(plan.runtimeBrief.RequiredEditSnippets); got != 1 {
		t.Fatalf("expected one payload snippet, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets)
	}

	if plan.runtimeBrief.RequiredEditSnippets[0].Path != "native/src/a.cpp" {
		t.Fatalf("snippet should bind to explicit payload path, got %#v", plan.runtimeBrief.RequiredEditSnippets[0])
	}
}

func TestReviewRepairPromptDoesNotPromoteEditScopeToRequiredPaths(t *testing.T) {
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("plan_then_execute_review_repair")

	prompt := `
# Plan-then-execute review-repair phase

## Allowed Repair Paths

- widget_config.json
- widget_notes.md

## Phase Evidence Bundle

` + "```text" + `
required_paths: (none)
allowed_repair_paths: widget_config.json, widget_notes.md
submitted_plan_state:
- ` + "`widget_config.json`" + ` status=applied
- ` + "`widget_notes.md`" + ` status=applied
` + "```" + `

## Diff Evidence

` + "```diff" + `
diff --git a/widget_config.json b/widget_config.json
--- a/widget_config.json
+++ b/widget_config.json
@@ -1 +1 @@
-{"labels":["alpha"]}
+{"labels":["alpha","beta","live-tui-e2e"],"version":2}
` + "```" + `

## Original Task / Spec

> <!-- IDIOM:BRIEF-BODY-START -->
> # Objective
>
> Please update widget_config.json and widget_notes.md. Only edit widget_config.json and widget_notes.md.
>
> ## Candidate Edit Scope From Target References
>
> - widget_config.json
> - widget_notes.md
>
> <!-- IDIOM:BRIEF-BODY-END -->
`
	plan := model.buildPromptExecutionPlan(context.Background(), prompt)

	if len(plan.runtimeBrief.RequiredEditPaths) != 0 {
		t.Fatalf("review-repair allowed/original edit scope must not become required coverage, got %#v", plan.runtimeBrief.RequiredEditPaths)
	}

	if slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope") {
		t.Fatalf("review-repair must not get mutation required-path constraints, got %#v", plan.runtimeBrief.Constraints)
	}

	for _, want := range []string{"widget_config.json", "widget_notes.md"} {
		if !slices.Contains(plan.runtimeBrief.TargetRefs, want) {
			t.Fatalf("expected review target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs)
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

func TestNewUsesProjectPromptCandidatesForDynamicInstructionAttachment(t *testing.T) {
	candidates, explicit := instructionAttachmentCandidates(config.Chat{ProjectPromptCandidates: []string{"AGENTS.md"}})
	if !explicit || !slices.Equal(candidates, []string{"AGENTS.md"}) {
		t.Fatalf("expected explicit AGENTS.md candidate only, got explicit=%t candidates=%#v", explicit, candidates)
	}

	candidates, explicit = instructionAttachmentCandidates(config.Chat{ProjectPromptCandidates: []string{"AGENTS.md", "CLAUDE.md"}})
	if !explicit || !slices.Equal(candidates, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("expected explicit candidates to preserve configured list, got explicit=%t candidates=%#v", explicit, candidates)
	}

	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Chat.AttachInstructionFiles = true
	cfg.Chat.ProjectPromptCandidates = []string{}

	model := New(cfg)

	if model.instructionAttacher != nil {
		t.Fatal("expected empty project_prompt_candidates to suppress dynamic instruction attachment")
	}

	cfg.Chat.ProjectPromptCandidates = []string{"AGENTS.md"}
	model = New(cfg)

	if model.instructionAttacher == nil {
		t.Fatal("expected explicit AGENTS.md candidate to enable dynamic instruction attachment")
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

	if strings.Contains(model.promptStack.Text, "[project behavior: .idiom/discovery/context.md]") ||
		strings.Contains(model.promptStack.Text, "Generated Project Context") {

		t.Fatalf("generated context should not be auto-loaded as prompt behavior, got:\n%s", model.promptStack.Text)
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
