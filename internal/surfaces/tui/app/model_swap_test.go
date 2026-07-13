package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
	"idiom/internal/runtime/workerruntime"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
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

func writeTestBackendsJSON(t ginkgo.FullGinkgoTInterface, root string) {
	ginkgo.GinkgoHelper()

	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(testBackendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}
}

func newTestModelWithWorkspace(t ginkgo.FullGinkgoTInterface) *Model {
	ginkgo.GinkgoHelper()

	root := t.TempDir()
	writeTestBackendsJSON(t, root)

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	thinkingOn := true
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))

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
		client:      client,
	}
}

var _ = ginkgo.It("use config planner model keeps client controller and reused classifier in lockstep", ginkgo.Label("development-scenario", "integration", "contract", "race-contract"), func() {
	oldSpec := config.LlamaServer{Kind: config.BackendLlama, Model: "old-role-model", BaseURL: "http://old.invalid"}
	oldClient := llama.NewClient(oldSpec.BaseURL, time.Second)
	oldController := llama.NewServerController(oldSpec, oldClient)
	configuredSpec := config.LlamaServer{Kind: config.BackendLlama, Model: "configured-model", BaseURL: "http://configured.invalid"}
	m := &Model{
		cfg:                  config.Config{LlamaServer: configuredSpec},
		plannerSpec:          oldSpec,
		client:               oldClient,
		controller:           oldController,
		summaryClient:        oldClient,
		summaryController:    oldController,
		backendRuntimeState:  backendRuntimeState{managedServer: true},
		managedServers:       newManagedServerRegistry(),
		backgroundOperations: newBackgroundOperationGroup(),
	}

	m.UseConfigPlannerModel()

	gomega.Expect(m.plannerSpec).To(gomega.Equal(configuredSpec))
	gomega.Expect(m.client).NotTo(gomega.BeIdenticalTo(oldClient))
	gomega.Expect(m.controller).NotTo(gomega.BeNil())
	gomega.Expect(m.controller).NotTo(gomega.BeIdenticalTo(oldController))
	gomega.Expect(m.summaryClient).To(gomega.BeIdenticalTo(m.client), "classifier reuse should follow the explicit planner client")
	gomega.Expect(m.summaryController).To(gomega.BeNil(), "classifier reuse must not create a second owner for the planner controller")
	gomega.Expect(m.managedServers.snapshot()).To(gomega.ContainElement(oldController))
	gomega.Expect(m.managedServer).To(gomega.BeFalse())

	distinctClassifierSpec := config.LlamaServer{Kind: config.BackendLlama, Model: "classifier-model", BaseURL: "http://classifier.invalid"}
	distinctClassifier := llama.NewClient(distinctClassifierSpec.BaseURL, time.Second)
	distinctClassifierController := llama.NewServerController(distinctClassifierSpec, distinctClassifier)
	m.summaryClient = distinctClassifier
	m.summaryController = distinctClassifierController
	m.cfg.LlamaServer = config.LlamaServer{Kind: config.BackendDeepSeek, Model: "hosted-configured-model"}
	m.UseConfigPlannerModel()

	gomega.Expect(m.controller).To(gomega.BeNil(), "hosted planner selection must not retain a launchable controller")
	gomega.Expect(m.summaryClient).To(gomega.BeIdenticalTo(distinctClassifier), "distinct classifier client must remain independent")
	gomega.Expect(m.summaryController).To(gomega.BeIdenticalTo(distinctClassifierController))
})

var _ = ginkgo.It("slash model shows current model", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.modelID = "qwen3.6-35b"
	client := m.client.(*llamamocks.MockLLMClient)
	client.EXPECT().ListModels(gomock.Any()).Return(nil, nil).Times(1)

	m.slashModel("/model", []string{"/model"})

	gomega.Expect(m.status).NotTo(gomega.Equal(""), fmt.Sprint("expected non-empty status for /model"))

	for _, name := range []string{"glm-5.1", "local-qwen", "minimax-m2.7", "local-no-max"} {
		gomega.Expect(m.status).To(gomega.ContainSubstring(name), fmt.Sprintf("expected /model to include %q, got %q", name, m.status))
	}
})

var _ = ginkgo.It("slash model list shows models", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.modelID = "qwen3.6-35b"
	client := m.client.(*llamamocks.MockLLMClient)
	client.EXPECT().ListModels(gomock.Any()).Return(nil, nil).Times(1)
	m.slashModel("/model list", []string{"/model", "list"})

	gomega.Expect(m.status).NotTo(gomega.Equal(""), fmt.Sprint("expected non-empty status for /model list"))

	for _, name := range []string{"glm-5.1", "local-qwen", "minimax-m2.7", "local-no-max"} {
		gomega.Expect(m.status).To(gomega.ContainSubstring(name), fmt.Sprintf("expected /model list to include %q, got %q", name, m.status))
	}
})

var _ = ginkgo.It("apply model swap hosted sets thinking floor", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("glm-5.1")

	gomega.Expect(m.modelID).To(gomega.Equal("glm-5.1"), fmt.Sprintf("expected modelID glm-5.1, got %q", m.modelID))

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprint("expected modelThinkingFloor=true for hosted model"))

	gomega.Expect(m.maxTokensOverride).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokensOverride=8192, got %d", m.maxTokensOverride))
})

var _ = ginkgo.It("apply model swap minimax sets thinking floor", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("minimax-m2.7")

	gomega.Expect(m.modelID).To(gomega.Equal("MiniMax-M2.7"), fmt.Sprintf("expected request modelID MiniMax-M2.7, got %q", m.modelID))

	gomega.Expect(activeModelDisplayID(m.modelDisplayID, m.modelID)).To(gomega.Equal("minimax-m2.7"), fmt.Sprintf("expected display model minimax-m2.7, got %q", activeModelDisplayID(m.modelDisplayID, m.modelID)))

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprint("expected modelThinkingFloor=true for minimax hosted model"))

	gomega.Expect(m.maxTokensOverride).To(gomega.Equal(4096), fmt.Sprintf("expected maxTokensOverride=4096, got %d", m.maxTokensOverride))
})

var _ = ginkgo.It("apply model swap mlx sets thinking floor", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-qwen")

	gomega.Expect(m.modelID).To(gomega.Equal("Qwen/Qwen3.6-35B"), fmt.Sprintf("expected request modelID Qwen/Qwen3.6-35B, got %q", m.modelID))

	gomega.Expect(activeModelDisplayID(m.modelDisplayID, m.modelID)).To(gomega.Equal("local-qwen"), fmt.Sprintf("expected display model local-qwen, got %q", activeModelDisplayID(m.modelDisplayID, m.modelID)))

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprint("expected modelThinkingFloor=true for MLX local model"))

	gomega.Expect(m.maxTokensOverride).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokensOverride=8192 from backends.json, got %d", m.maxTokensOverride))
})

var _ = ginkgo.It("apply model swap local with no max tokens", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-no-max")

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprint("expected modelThinkingFloor=false for local model"))

	gomega.Expect(m.maxTokensOverride).To(gomega.Equal(0), fmt.Sprintf("expected maxTokensOverride=0 when backends.json omits max_tokens, got %d", m.maxTokensOverride))
})

var _ = ginkgo.It("apply model swap hosted to local resets thinking floor", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("glm-5.1")

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprint("setup: expected thinking floor after hosted swap"))

	m.applyModelSwap("local-no-max")

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprint("expected modelThinkingFloor=false after swapping from hosted to non-MLX local"))
})

var _ = ginkgo.It("apply model swap local to hosted sets thinking floor", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("local-no-max")

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprint("setup: expected no thinking floor after non-MLX local swap"))

	m.applyModelSwap("glm-5.1")

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeTrue(), fmt.Sprint("expected modelThinkingFloor=true after swapping from local to hosted"))
})

var _ = ginkgo.It("apply model swap without backends sets model id only", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	m := &Model{
		cfg:         config.Config{LlamaServer: config.Default().LlamaServer},
		workspace:   ws,
		plannerSpec: config.Default().LlamaServer,
	}

	m.applyModelSwap("some-model")

	gomega.Expect(m.modelID).To(gomega.Equal("some-model"), fmt.Sprintf("expected modelID some-model, got %q", m.modelID))

	gomega.Expect(activeModelDisplayID(m.modelDisplayID, m.modelID)).To(gomega.Equal("some-model"), fmt.Sprintf("expected display model some-model, got %q", activeModelDisplayID(m.modelDisplayID, m.modelID)))

	gomega.Expect(m.modelThinkingFloor).To(gomega.BeFalse(), fmt.Sprint("expected modelThinkingFloor=false when no backends.json"))

	gomega.Expect(m.maxTokensOverride).To(gomega.Equal(0), fmt.Sprintf("expected maxTokensOverride=0 when no backends.json, got %d", m.maxTokensOverride))
})

var _ = ginkgo.It("apply model swap empty name shows usage", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.applyModelSwap("")

	gomega.Expect(m.status).To(gomega.Equal("Usage: /model [list|name]"), fmt.Sprintf("expected usage message, got %q", m.status))
})

var _ = ginkgo.It("routing hosted thinking floor preserves thinking", func() {
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

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for hosted model with read-only profile (thinking floor)"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for hosted model with read-only profile (thinking floor)"))

	gomega.Expect(tuned.PreserveThinking).To(gomega.Equal(true), fmt.Sprint("expected PreserveThinking=true for hosted model"))

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected direct_answer floor of 4096 (8192 exceeds, should stay), got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("routing hosted thinking floor with max tokens override", func() {
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

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokensOverride to win over contract clamp, got %d", tuned.MaxTokens))

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for hosted model"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for hosted model"))
})

var _ = ginkgo.It("routing local model disables thinking for read only", func() {
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

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for local model with direct_answer read-only (conversation lane preserves thinking)"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for local model with direct_answer read-only (conversation lane preserves thinking)"))

	gomega.Expect(tuned.PreserveThinking).To(gomega.BeTrue(), fmt.Sprint("expected PreserveThinking=true for direct_answer read-only route"))

	gomega.Expect(tuned.ChatTemplateKwargs).To(gomega.BeTrue(), fmt.Sprint("expected ChatTemplateKwargs=true for read-only route"))
})

var _ = ginkgo.It("routing local model max tokens override wins over clamp", func() {
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

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokensOverride=8192 to win over 4096 clamp, got %d", tuned.MaxTokens))

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for direct_answer read-only (conversation lane preserves thinking)"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for direct_answer read-only (conversation lane preserves thinking)"))
})

var _ = ginkgo.It("disable thinking override wins over thinking floor", func() {
	thinkingOn := true
	m := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		backendRuntimeState: backendRuntimeState{modelID: "glm-5.1"},
	}

	m.modelThinkingFloor = true
	m.SetDisableThinkingOverride(true)

	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

	gomega.Expect(opts.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking OFF when disableThinkingOverride=true even with thinking floor"))
	gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse(), fmt.Sprint("expected thinking OFF when disableThinkingOverride=true even with thinking floor"))

	gomega.Expect(opts.ModelThinkingFloor).To(gomega.Equal(true), fmt.Sprint("expected ModelThinkingFloor still true in chat options (routing should see it)"))
})

var _ = ginkgo.It("disable thinking override and routing still disables thinking for hosted", func() {
	m := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   boolPtr(true),
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		backendRuntimeState: backendRuntimeState{modelID: "glm-5.1"},
	}

	m.modelThinkingFloor = true
	m.SetDisableThinkingOverride(true)

	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)

	gomega.Expect(opts.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking OFF from disable override before routing"))
	gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse(), fmt.Sprint("expected thinking OFF from disable override before routing"))

	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking to stay OFF after routing (already off, floor irrelevant)"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeFalse(), fmt.Sprint("expected thinking to stay OFF after routing (already off, floor irrelevant)"))
})

var _ = ginkgo.It("full flow hosted model swap preserves thinking and tokens", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	thinkingOn := true

	m.cfg.Chat.EnableThinking = &thinkingOn
	m.cfg.Chat.PreserveThinking = true
	m.cfg.Chat.MaxTokens = 8192
	m.modelID = "qwen3.6-35b"

	m.applyModelSwap("glm-5.1")

	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, m.maxTokensOverride)

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for hosted glm-5.1 on read-only route"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for hosted glm-5.1 on read-only route"))

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokens=8192 (override wins over 4096 clamp), got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("full flow local model swap disables thinking but override wins on tokens", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	thinkingOn := true

	m.cfg.Chat.EnableThinking = &thinkingOn
	m.cfg.Chat.PreserveThinking = true
	m.cfg.Chat.MaxTokens = 8192
	m.modelID = "qwen3.6-35b"

	m.applyModelSwap("local-qwen")

	opts := chatOptions(m.cfg.Chat, m.modelID, m.modelThinkingFloor, m.disableThinkingOverride)
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "direct_answer",
		},
	}
	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, m.maxTokensOverride)

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprint("expected thinking ON for direct_answer conversation lane (preserves thinking)"))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprint("expected thinking ON for direct_answer conversation lane (preserves thinking)"))

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected maxTokens=8192 (override wins over 4096 clamp), got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("backends model by alias", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	writeTestBackendsJSON(t, root)

	file, found, err := backends.LoadFromProjectRoot(root)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("load backends: %v", err))

	gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprint("expected backends.json to be found"))

	model, ok := file.ModelByAlias("glm-5.1")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected to find glm-5.1 alias"))

	gomega.Expect(model.Kind).To(gomega.Equal("zai"), fmt.Sprintf("unexpected model: %#v", model))
	gomega.Expect(model.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("unexpected model: %#v", model))

	model, ok = file.ModelByAlias("local-qwen")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprint("expected to find local-qwen alias"))

	gomega.Expect(model.Kind).To(gomega.Equal("mlx"), fmt.Sprintf("expected kind=mlx, got %q", model.Kind))

	_, ok = file.ModelByAlias("nonexistent")
	gomega.Expect(ok).To(gomega.BeFalse(), fmt.Sprint("expected nonexistent alias to not be found"))
})

var _ = ginkgo.It("apply model swap sets swapped flag", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	gomega.Expect(m.modelSwapped).To(gomega.BeFalse(), fmt.Sprint("expected modelSwapped=false before swap"))

	m.applyModelSwap("glm-5.1")

	gomega.Expect(m.modelSwapped).To(gomega.BeTrue(), fmt.Sprint("expected modelSwapped=true after /model swap"))
})

var _ = ginkgo.It("ensure server ready does not revert swapped model when unreachable", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	client := m.client.(*llamamocks.MockLLMClient)
	m.applyModelSwap("local-qwen")

	gomega.Expect(m.modelID).To(gomega.Equal("Qwen/Qwen3.6-35B"), fmt.Sprintf("setup: expected request modelID Qwen/Qwen3.6-35B, got %q", m.modelID))

	gomega.Expect(activeModelDisplayID(m.modelDisplayID, m.modelID)).To(gomega.Equal("local-qwen"), fmt.Sprintf("setup: expected display model local-qwen, got %q", activeModelDisplayID(m.modelDisplayID, m.modelID)))

	origSpec := m.plannerSpec

	client.EXPECT().ListModels(gomock.Any()).Return(nil, fmt.Errorf("connection refused")).Times(1)
	m.client = client
	m.controller = nil

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	session := newDetachedPromptExecutionSession(ctx, m)
	err := session.ensureServerReady(ctx)
	gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprint("expected error when swapped model server is unreachable"))

	gomega.Expect(strings.Contains(err.Error(), "swapped model")).To(gomega.BeTrue(), fmt.Sprintf("expected swapped-model error, got %q", err.Error()))

	gomega.Expect(session.modelID).To(gomega.Equal("Qwen/Qwen3.6-35B"), fmt.Sprintf("request modelID should NOT be reverted, got %q", session.modelID))

	gomega.Expect(activeModelDisplayID(session.modelDisplayID, session.modelID)).To(gomega.Equal("local-qwen"), fmt.Sprintf("display model should NOT be reverted, got %q", activeModelDisplayID(session.modelDisplayID, session.modelID)))

	gomega.Expect(session.plannerSpec.EndpointBaseURL()).To(gomega.Equal(origSpec.EndpointBaseURL()), fmt.Sprint("plannerSpec should not be mutated on failure"))
})

var _ = ginkgo.It("ensure server ready succeeds for reachable swapped model", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)
	client := m.client.(*llamamocks.MockLLMClient)
	m.applyModelSwap("local-qwen")

	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "local-qwen"}}, nil).Times(1)
	m.client = client

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	session := newDetachedPromptExecutionSession(ctx, m)
	err := session.ensureServerReady(ctx)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected no error for reachable swapped server, got %v", err))
})

var _ = ginkgo.It("ensure server ready displays alias without changing request model", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithWorkspace(t)

	m.plannerSpec = config.LlamaServer{
		Kind:        config.BackendMLX,
		Model:       "/models/local-coder-mlx",
		DisplayName: "local-coder",
		Host:        "127.0.0.1",
		Port:        11434,
	}
	client := m.client.(*llamamocks.MockLLMClient)
	client.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "default-reported-model"}}, nil).Times(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session := newDetachedPromptExecutionSession(ctx, m)

	{
		err := session.ensureServerReady(ctx)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ensureServerReady: %v", err))
	}

	gomega.Expect(session.modelID).To(gomega.Equal("/models/local-coder-mlx"), fmt.Sprintf("request modelID should remain backend path, got %q", session.modelID))

	gomega.Expect(activeModelDisplayID(session.modelDisplayID, session.modelID)).To(gomega.Equal("local-coder"), fmt.Sprintf("display model should use alias, got %q", activeModelDisplayID(session.modelDisplayID, session.modelID)))

	{
		opts := chatOptions(session.cfg.Chat, session.modelID, session.modelThinkingFloor, session.disableThinkingOverride)
		gomega.Expect(opts.Model).To(gomega.Equal("/models/local-coder-mlx"), fmt.Sprintf("chat options should use request model path, got %q", opts.Model))
	}
})

func boolPtr(b bool) *bool {
	return &b
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
    },
    "glm-5.2": {
      "models": [
        {"alias": "glm-5.2", "kind": "glm", "model": "glm-5.2", "api_key_env": "ZAI_API_KEY", "reasoning_effort": "max", "roles": ["planner", "executor", "classifier"]}
      ]
    }
  }
}`

func newTestModelWithMultiProfile(t ginkgo.FullGinkgoTInterface) *Model {
	ginkgo.GinkgoHelper()

	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(multiProfileBackendsJSON), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	ws := workspace.NewManager(config.Workspace{Root: root, CWD: root})
	thinkingOn := true
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))

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
		client:      client,
	}
}

var _ = ginkgo.It("model alias discovery collects all profile models", func() {
	t := ginkgo.GinkgoT()
	m := newTestModelWithMultiProfile(t)

	m.modelID = "qwen3.6-35b"

	client := m.client.(*llamamocks.MockLLMClient)
	client.EXPECT().ListModels(gomock.Any()).Return(nil, nil).Times(1)
	aliases := m.collectAllAliases()

	for _, want := range []string{"qwen3.6-35b", "qwen3-coder-30b", "MiniMax-M2.7", "glm-5.1", "glm-5.2"} {
		found := false

		for _, a := range aliases {
			if a == want {
				found = true
				break
			}
		}

		gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("expected alias %q in collected aliases, got %v", want, aliases))
	}
})
