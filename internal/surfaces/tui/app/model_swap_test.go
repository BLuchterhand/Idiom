package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

const testBackendsJSON = `{
  "default_profile": "default",
  "profiles": {
    "default": {
      "models": [
        {"alias": "glm-5.1", "kind": "zai", "model": "glm-5.1", "base_url": "https://api.z.ai/api/paas/v4", "api_key_env": "ZAI_API_KEY", "max_tokens": 8192, "roles": ["planner"]},
        {"alias": "local-qwen", "kind": "mlx", "model_path": "Qwen/Qwen3.6-35B", "max_tokens": 8192, "roles": ["planner"]},
        {"alias": "minimax-m2.7", "kind": "minimax", "model": "MiniMax-M2.7", "base_url": "https://api.minimax.chat/v1", "api_key_env": "MINIMAX_API_KEY", "max_tokens": 4096, "roles": ["planner"]},
        {"alias": "local-no-max", "kind": "llama", "model_path": "Qwen/Qwen3-0.6B"}
      ]
    }
  }
}`

func writeTestBackendsJSON(t *testing.T, root string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(testBackendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}
}

func newTestModelWithWorkspace(t *testing.T) *Model {
	t.Helper()

	root := t.TempDir()
	writeTestBackendsJSON(t, root)

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	thinkingOn := true
	stub := &stubLLMClient{}

	return &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
			LlamaServer: config.Default().LlamaServer,
		},
		workspace:   ws,
		plannerSpec: config.Default().LlamaServer,
		client:      stub,
		runner:      &agent.Runner{Client: stub},
	}
}

func TestSlashModelShowsCurrentModel(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.modelID = "qwen3.6-35b"

	m.slashModel("/model", []string{"/model"})

	if !m.modelPopup.open {
		t.Fatal("expected model popup to be open")
	}

	if len(m.modelPopup.roles) == 0 {
		t.Fatal("expected popup roles to be populated")
	}
}

func TestSlashModelListShowsModels(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.modelID = "qwen3.6-35b"
	m.client = &stubLLMClient{}

	m.slashModel("/model list", []string{"/model", "list"})

	if m.status == "" {
		t.Fatal("expected non-empty status for /model list")
	}

	for _, name := range []string{"glm-5.1", "local-qwen", "minimax-m2.7", "local-no-max"} {
		if !containsSubstring(m.status, name) {
			t.Fatalf("expected /model list to include %q, got %q", name, m.status)
		}
	}
}

type stubLLMClient struct{}

type failingListModelsClient struct {
	stubLLMClient
	err error
}

func (f *failingListModelsClient) ListModels(_ context.Context) ([]llama.ModelInfo, error) {
	return nil, f.err
}

type reachableListModelsClient struct {
	stubLLMClient
	models []llama.ModelInfo
}

func (r *reachableListModelsClient) ListModels(_ context.Context) ([]llama.ModelInfo, error) {
	return r.models, nil
}

func (s *stubLLMClient) CompleteChat(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (string, error) {
	return "", nil
}
func (s *stubLLMClient) CompleteChatMessage(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions) (llama.ChatMessage, error) {
	return llama.ChatMessage{}, nil
}
func (s *stubLLMClient) StreamChat(_ context.Context, _ []llama.ChatMessage, _ llama.ChatOptions, _ func(string) error) error {
	return nil
}
func (s *stubLLMClient) ListModels(_ context.Context) ([]llama.ModelInfo, error) {
	return nil, nil
}
func (s *stubLLMClient) Backend() string           { return "stub" }
func (s *stubLLMClient) SupportsNativeTools() bool { return false }

func TestApplyModelSwapHostedSetsThinkingFloor(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("glm-5.1")

	if m.modelID != "glm-5.1" {
		t.Fatalf("expected modelID glm-5.1, got %q", m.modelID)
	}

	if !m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=true for hosted model")
	}

	if m.maxTokensOverride != 8192 {
		t.Fatalf("expected maxTokensOverride=8192, got %d", m.maxTokensOverride)
	}
}

func TestApplyModelSwapMinimaxSetsThinkingFloor(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("minimax-m2.7")

	if m.modelID != "MiniMax-M2.7" {
		t.Fatalf("expected request modelID MiniMax-M2.7, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "minimax-m2.7" {
		t.Fatalf("expected display model minimax-m2.7, got %q", m.activeModelDisplayID())
	}

	if !m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=true for minimax hosted model")
	}

	if m.maxTokensOverride != 4096 {
		t.Fatalf("expected maxTokensOverride=4096, got %d", m.maxTokensOverride)
	}
}

func TestApplyModelSwapMLXSetsThinkingFloor(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-qwen")

	if m.modelID != "Qwen/Qwen3.6-35B" {
		t.Fatalf("expected request modelID Qwen/Qwen3.6-35B, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "local-qwen" {
		t.Fatalf("expected display model local-qwen, got %q", m.activeModelDisplayID())
	}

	if !m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=true for MLX local model")
	}

	if m.maxTokensOverride != 8192 {
		t.Fatalf("expected maxTokensOverride=8192 from backends.json, got %d", m.maxTokensOverride)
	}
}

func TestApplyModelSwapLocalWithNoMaxTokens(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-no-max")

	if m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=false for local model")
	}

	if m.maxTokensOverride != 0 {
		t.Fatalf("expected maxTokensOverride=0 when backends.json omits max_tokens, got %d", m.maxTokensOverride)
	}
}

func TestApplyModelSwapHostedToLocalResetsThinkingFloor(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("glm-5.1")

	if !m.modelThinkingFloor {
		t.Fatal("setup: expected thinking floor after hosted swap")
	}

	m.applyModelSwap("local-no-max")

	if m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=false after swapping from hosted to non-MLX local")
	}
}

func TestApplyModelSwapLocalToHostedSetsThinkingFloor(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-no-max")

	if m.modelThinkingFloor {
		t.Fatal("setup: expected no thinking floor after non-MLX local swap")
	}

	m.applyModelSwap("glm-5.1")

	if !m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=true after swapping from local to hosted")
	}
}

func TestApplyModelSwapWithoutBackendsSetsModelIDOnly(t *testing.T) {
	root := t.TempDir()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	m := &Model{
		cfg:         config.Config{LlamaServer: config.Default().LlamaServer},
		workspace:   ws,
		plannerSpec: config.Default().LlamaServer,
	}

	m.applyModelSwap("some-model")

	if m.modelID != "some-model" {
		t.Fatalf("expected modelID some-model, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "some-model" {
		t.Fatalf("expected display model some-model, got %q", m.activeModelDisplayID())
	}

	if m.modelThinkingFloor {
		t.Fatal("expected modelThinkingFloor=false when no backends.json")
	}

	if m.maxTokensOverride != 0 {
		t.Fatalf("expected maxTokensOverride=0 when no backends.json, got %d", m.maxTokensOverride)
	}
}

func TestApplyModelSwapEmptyNameShowsUsage(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("")

	if m.status != "Usage: /model [list|name]" {
		t.Fatalf("expected usage message, got %q", m.status)
	}
}

func TestRoutingHostedThinkingFloorPreservesThinking(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		MaxTokens:          8192,
		ModelThinkingFloor: true,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for hosted model with read-only profile (thinking floor)")
	}

	if tuned.PreserveThinking != true {
		t.Fatal("expected PreserveThinking=true for hosted model")
	}

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected direct_answer floor of 4096 (8192 exceeds, should stay), got %d", tuned.MaxTokens)
	}
}

func TestRoutingHostedThinkingFloorWithMaxTokensOverride(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		MaxTokens:          8192,
		ModelThinkingFloor: true,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 8192)

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected maxTokensOverride to win over contract clamp, got %d", tuned.MaxTokens)
	}

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for hosted model")
	}
}

func TestRoutingLocalModelDisablesThinkingForReadOnly(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		MaxTokens:          8192,
		ModelThinkingFloor: false,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for local model with direct_answer read-only (conversation lane preserves thinking)")
	}

	if !tuned.PreserveThinking {
		t.Fatal("expected PreserveThinking=true for direct_answer read-only route")
	}

	if !tuned.ChatTemplateKwargs {
		t.Fatal("expected ChatTemplateKwargs=true for read-only route")
	}
}

func TestRoutingLocalModelMaxTokensOverrideWinsOverClamp(t *testing.T) {
	thinkingOn := true
	opts := llama.ChatOptions{
		EnableThinking:     &thinkingOn,
		PreserveThinking:   true,
		MaxTokens:          8192,
		ModelThinkingFloor: false,
	}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 8192)

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected maxTokensOverride=8192 to win over 4096 clamp, got %d", tuned.MaxTokens)
	}

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for direct_answer read-only (conversation lane preserves thinking)")
	}
}

func TestDisableThinkingOverrideWinsOverThinkingFloor(t *testing.T) {
	thinkingOn := true
	m := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		modelID: "glm-5.1",
	}

	m.modelThinkingFloor = true
	m.SetDisableThinkingOverride(true)

	opts := m.chatOptions()

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatal("expected thinking OFF when disableThinkingOverride=true even with thinking floor")
	}

	if opts.ModelThinkingFloor != true {
		t.Fatal("expected ModelThinkingFloor still true in chat options (routing should see it)")
	}
}

func TestDisableThinkingOverrideAndRoutingStillDisablesThinkingForHosted(t *testing.T) {
	m := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   boolPtr(true),
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		modelID: "glm-5.1",
	}

	m.modelThinkingFloor = true
	m.SetDisableThinkingOverride(true)

	opts := m.chatOptions()

	if opts.EnableThinking == nil || *opts.EnableThinking {
		t.Fatal("expected thinking OFF from disable override before routing")
	}

	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	if tuned.EnableThinking == nil || *tuned.EnableThinking {
		t.Fatal("expected thinking to stay OFF after routing (already off, floor irrelevant)")
	}
}

func TestFullFlowHostedModelSwapPreservesThinkingAndTokens(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	thinkingOn := true

	m.cfg.Chat.EnableThinking = &thinkingOn
	m.cfg.Chat.PreserveThinking = true
	m.cfg.Chat.MaxTokens = 8192
	m.modelID = "qwen3.6-35b"

	m.applyModelSwap("glm-5.1")

	opts := m.chatOptions()
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, m.maxTokensOverride)

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for hosted glm-5.1 on read-only route")
	}

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected maxTokens=8192 (override wins over 4096 clamp), got %d", tuned.MaxTokens)
	}
}

func TestFullFlowLocalModelSwapDisablesThinkingButOverrideWinsOnTokens(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	thinkingOn := true

	m.cfg.Chat.EnableThinking = &thinkingOn
	m.cfg.Chat.PreserveThinking = true
	m.cfg.Chat.MaxTokens = 8192
	m.modelID = "qwen3.6-35b"

	m.applyModelSwap("local-qwen")

	opts := m.chatOptions()
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, m.maxTokensOverride)

	if tuned.EnableThinking == nil || !*tuned.EnableThinking {
		t.Fatal("expected thinking ON for direct_answer conversation lane (preserves thinking)")
	}

	if tuned.MaxTokens != 8192 {
		t.Fatalf("expected maxTokens=8192 (override wins over 4096 clamp), got %d", tuned.MaxTokens)
	}
}

func TestBackendsModelByAlias(t *testing.T) {
	root := t.TempDir()
	writeTestBackendsJSON(t, root)

	file, found, err := backends.LoadFromProjectRoot(root)
	if err != nil {
		t.Fatalf("load backends: %v", err)
	}

	if !found {
		t.Fatal("expected backends.json to be found")
	}

	model, ok := file.ModelByAlias("glm-5.1")
	if !ok {
		t.Fatal("expected to find glm-5.1 alias")
	}

	if model.Kind != "zai" || model.MaxTokens != 8192 {
		t.Fatalf("unexpected model: %#v", model)
	}

	model, ok = file.ModelByAlias("local-qwen")
	if !ok {
		t.Fatal("expected to find local-qwen alias")
	}

	if model.Kind != "mlx" {
		t.Fatalf("expected kind=mlx, got %q", model.Kind)
	}

	_, ok = file.ModelByAlias("nonexistent")
	if ok {
		t.Fatal("expected nonexistent alias to not be found")
	}
}

func TestApplyModelSwapSetsSwappedFlag(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	if m.modelSwapped {
		t.Fatal("expected modelSwapped=false before swap")
	}

	m.applyModelSwap("glm-5.1")

	if !m.modelSwapped {
		t.Fatal("expected modelSwapped=true after /model swap")
	}
}

func TestEnsureServerReadyDoesNotRevertSwappedModelWhenUnreachable(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	m.applyModelSwap("local-qwen")

	if m.modelID != "Qwen/Qwen3.6-35B" {
		t.Fatalf("setup: expected request modelID Qwen/Qwen3.6-35B, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "local-qwen" {
		t.Fatalf("setup: expected display model local-qwen, got %q", m.activeModelDisplayID())
	}

	origSpec := m.plannerSpec

	m.client = &failingListModelsClient{err: fmt.Errorf("connection refused")}
	m.controller = nil

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := m.ensureServerReady(ctx)
	if err == nil {
		t.Fatal("expected error when swapped model server is unreachable")
	}

	if !strings.Contains(err.Error(), "swapped model") {
		t.Fatalf("expected swapped-model error, got %q", err.Error())
	}

	if m.modelID != "Qwen/Qwen3.6-35B" {
		t.Fatalf("request modelID should NOT be reverted, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "local-qwen" {
		t.Fatalf("display model should NOT be reverted, got %q", m.activeModelDisplayID())
	}

	if m.plannerSpec.EndpointBaseURL() != origSpec.EndpointBaseURL() {
		t.Fatal("plannerSpec should not be mutated on failure")
	}
}

func TestEnsureServerReadySucceedsForReachableSwappedModel(t *testing.T) {
	m := newTestModelWithWorkspace(t)
	m.applyModelSwap("local-qwen")

	m.client = &reachableListModelsClient{
		models: []llama.ModelInfo{{ID: "local-qwen"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := m.ensureServerReady(ctx)
	if err != nil {
		t.Fatalf("expected no error for reachable swapped server, got %v", err)
	}
}

func TestEnsureServerReadyDisplaysAliasWithoutChangingRequestModel(t *testing.T) {
	m := newTestModelWithWorkspace(t)

	m.plannerSpec = config.LlamaServer{
		Kind:        config.BackendMLX,
		Model:       "/models/qwen3.6-35b-a3b-jang-6m",
		DisplayName: "qwen3.6-35b",
		Host:        "127.0.0.1",
		Port:        11434,
	}
	m.client = &reachableListModelsClient{
		models: []llama.ModelInfo{{ID: "default-reported-model"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.ensureServerReady(ctx); err != nil {
		t.Fatalf("ensureServerReady: %v", err)
	}

	if m.modelID != "/models/qwen3.6-35b-a3b-jang-6m" {
		t.Fatalf("request modelID should remain backend path, got %q", m.modelID)
	}

	if m.activeModelDisplayID() != "qwen3.6-35b" {
		t.Fatalf("display model should use alias, got %q", m.activeModelDisplayID())
	}

	if opts := m.chatOptions(); opts.Model != "/models/qwen3.6-35b-a3b-jang-6m" {
		t.Fatalf("chat options should use request model path, got %q", opts.Model)
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsSubstrHelper(s, sub))
}

func containsSubstrHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}

const multiProfileBackendsJSON = `{
  "default_profile": "qwen-pair",
  "profiles": {
    "qwen-pair": {
      "models": [
        {"alias": "qwen3.6-35b", "kind": "mlx", "model_path": "/models/qwen3.6-35b", "port": 47780, "roles": ["planner", "classifier"]},
        {"alias": "qwen3-coder-30b", "kind": "mlx", "model_path": "/models/qwen3-coder-30b", "port": 47781, "roles": ["executor"]}
      ]
    },
    "minimax-m2.7": {
      "models": [
        {"alias": "MiniMax-M2.7", "kind": "minimax", "model": "MiniMax-M2.7", "base_url": "https://api.minimax.io/v1", "api_key_env": "MINIMAX_API_KEY", "roles": ["planner", "executor", "classifier"]}
      ]
    },
    "glm-5.1": {
      "models": [
        {"alias": "glm-5.1", "kind": "zai", "model": "glm-5.1", "base_url": "https://api.z.ai/api/coding/paas/v4", "api_key_env": "ZAI_API_KEY", "roles": ["planner", "executor", "classifier"]}
      ]
    }
  }
}`

func newTestModelWithMultiProfile(t *testing.T) *Model {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(multiProfileBackendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	thinkingOn := true
	stub := &stubLLMClient{}

	return &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
			LlamaServer: config.Default().LlamaServer,
		},
		workspace:   ws,
		plannerSpec: config.Default().LlamaServer,
		client:      stub,
		runner:      &agent.Runner{Client: stub},
	}
}

func TestModelPopupCollectsAllProfileModels(t *testing.T) {
	m := newTestModelWithMultiProfile(t)

	m.modelID = "qwen3.6-35b"

	m.openModelPopup()

	if !m.modelPopup.open {
		t.Fatal("expected popup open")
	}

	aliases := m.collectAllAliases()

	for _, want := range []string{"qwen3.6-35b", "qwen3-coder-30b", "MiniMax-M2.7", "glm-5.1"} {
		found := false

		for _, a := range aliases {
			if a == want {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("expected alias %q in collected aliases, got %v", want, aliases)
		}
	}

	for _, role := range m.modelPopup.roles {
		hasMiniMax := false

		for _, a := range role.aliases {
			if a == "MiniMax-M2.7" {
				hasMiniMax = true
				break
			}
		}

		if !hasMiniMax {
			t.Errorf("role %q missing MiniMax-M2.7 in aliases: %v", role.name, role.aliases)
		}
	}
}

func TestModelPopupTabCyclesModels(t *testing.T) {
	m := newTestModelWithMultiProfile(t)

	m.modelID = "qwen3.6-35b"
	m.openModelPopup()

	plannerIdx := m.modelPopup.modelIdx["planner"]
	initialAlias := m.modelPopup.roles[0].aliases[plannerIdx]

	_, handled := m.handleModelPopupKey(tea.KeyMsg{Type: tea.KeyTab})
	if !handled {
		t.Fatal("expected Tab to be handled")
	}

	newIdx := m.modelPopup.modelIdx["planner"]
	newAlias := m.modelPopup.roles[0].aliases[newIdx]

	if newAlias == initialAlias {
		t.Fatal("expected Tab to change the selected model")
	}
}

func TestModelPopupEscCloses(t *testing.T) {
	m := newTestModelWithMultiProfile(t)

	m.modelID = "qwen3.6-35b"
	m.openModelPopup()

	_, handled := m.handleModelPopupKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("expected Esc to be handled")
	}

	if m.modelPopup.open {
		t.Fatal("expected popup to be closed after Esc")
	}
}
