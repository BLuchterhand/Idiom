package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/contracts/workflowstate"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/modepolicy"
	"idiom/internal/runtime/promptstack"
	runtimesession "idiom/internal/runtime/session"
	"idiom/internal/runtime/workerruntime"
	"idiom/internal/workflows/planexecute"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("mode run policy separates advisory and operational model call limits", func() {
	cfg := config.Default().Chat
	cfg.MaxModelCalls = 7
	cfg.MaxOperationalModelCalls = 11

	policy := modeRunPolicy(modepolicy.Mode{}, cfg)
	gomega.Expect(policy.MaxModelCalls).To(gomega.Equal(7), fmt.Sprintf("MaxModelCalls should remain advisory config threshold, got %d", policy.MaxModelCalls))
	gomega.Expect(policy.MaxOperationalModelCalls).To(gomega.Equal(11), fmt.Sprintf("MaxOperationalModelCalls should carry hard operational fuse, got %d", policy.MaxOperationalModelCalls))
})

var _ = ginkgo.It("system prompt skips mode prompt files already in prompt stack", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "prompts", "base.md")

	{
		err := os.MkdirAll(filepath.Dir(basePath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir prompts: %v", err))
	}

	{
		err := os.WriteFile(basePath, []byte("Base behavior\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write base prompt: %v", err))
	}

	model := &Model{
		cfg: config.Config{Chat: config.Chat{ProjectRoot: dir}},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{promptStack: promptstack.Stack{
				Text: "[baseline behavior: base.md]\nBase behavior",
				Sources: []promptstack.Source{{
					Kind:        "baseline",
					DisplayName: "base.md",
					Path:        filepath.Join("prompts", "base.md"),
				}},
			}},
		},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Count(prompt, "Base behavior")).To(gomega.Equal(1), fmt.Sprintf("expected mode/system prompt dedupe, got:\n%s", prompt))
})

var _ = ginkgo.It("system prompt auto appends qwen compatibility overlay", func() {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "auto"},
		},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{promptStack: promptstack.Stack{
				Text: "Base behavior",
				Sources: []promptstack.Source{{
					Kind:        "baseline",
					DisplayName: "base.md",
				}},
			}},
		},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Contains(prompt, "[model compatibility: model:qwen]")).To(gomega.BeTrue(), fmt.Sprintf("expected qwen compatibility overlay, got:\n%s", prompt))

	gomega.Expect(slices.Contains(model.contextSourceNames(), "model:qwen")).To(gomega.BeTrue(), fmt.Sprintf("expected qwen overlay in context sources, got %#v", model.contextSourceNames()))
})

var _ = ginkgo.It("system prompt skips qwen overlay for generic family", func() {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "generic"},
		},
		promptRuntimeState: promptRuntimeState{promptRuntimeCompletionState: promptRuntimeCompletionState{
			promptStack: promptstack.Stack{Text: "Base behavior"},
		}},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Contains(prompt, "model:qwen")).To(gomega.BeFalse(), fmt.Sprintf("expected generic model family to suppress qwen overlay, got:\n%s", prompt))
})

var _ = ginkgo.It("system prompt uses resolved planner spec for model overlay", func() {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "/models/Qwen3-Coder-30B"},
			Chat:        config.Chat{ModelFamily: "auto"},
		},
		plannerSpec: config.LlamaServer{Kind: "zai", Model: "glm-5.1"},
		promptRuntimeState: promptRuntimeState{promptRuntimeCompletionState: promptRuntimeCompletionState{
			promptStack: promptstack.Stack{Text: "Base behavior"},
		}},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Contains(prompt, "model:qwen")).To(gomega.BeFalse(), fmt.Sprintf("resolved non-Qwen planner should suppress inherited config overlay, got:\n%s", prompt))
})

var _ = ginkgo.It("system prompt can force qwen overlay for model alias", func() {
	model := &Model{
		cfg: config.Config{
			LlamaServer: config.LlamaServer{Model: "local-coder"},
			Chat:        config.Chat{ModelFamily: "qwen"},
		},
		promptRuntimeState: promptRuntimeState{promptRuntimeCompletionState: promptRuntimeCompletionState{
			promptStack: promptstack.Stack{Text: "Base behavior"},
		}},
		agentModeName: "build",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Contains(prompt, "model:qwen")).To(gomega.BeTrue(), fmt.Sprintf("expected explicit qwen family to append overlay, got:\n%s", prompt))
})

var _ = ginkgo.It("context sources include mode prompt files", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "prompts", "base.md")
	planPath := filepath.Join(dir, "prompts", "plan-mode.md")

	{
		err := os.MkdirAll(filepath.Dir(basePath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir prompts: %v", err))
	}

	{
		err := os.WriteFile(basePath, []byte("Base behavior\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write base prompt: %v", err))
	}

	{
		err := os.WriteFile(planPath, []byte("Plan behavior\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan prompt: %v", err))
	}

	model := &Model{
		cfg: config.Config{Chat: config.Chat{ProjectRoot: dir}},
		promptRuntimeState: promptRuntimeState{
			promptRuntimeCompletionState: promptRuntimeCompletionState{promptStack: promptstack.Stack{
				Text: "[baseline behavior: base.md]\nBase behavior",
				Sources: []promptstack.Source{{
					Kind:        "baseline",
					DisplayName: "base.md",
					Path:        filepath.Join("prompts", "base.md"),
				}},
			}},
		},
		agentModeName: "plan",
	}

	prompt := model.systemPrompt()
	gomega.Expect(strings.Count(prompt, "Base behavior")).To(gomega.Equal(1), fmt.Sprintf("expected deduped base and loaded plan prompt, got:\n%s", prompt))
	gomega.Expect(prompt).To(gomega.ContainSubstring("Plan behavior"), fmt.Sprintf("expected deduped base and loaded plan prompt, got:\n%s", prompt))

	gomega.Expect(slices.Contains(model.contextSourceNames(), "mode:plan:plan-mode.md")).To(gomega.BeTrue(), fmt.Sprintf("expected mode prompt in context sources, got %#v", model.contextSourceNames()))
})

var _ = ginkgo.It("system prompt sources use cached mode prompt until invalidated", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	planPath := filepath.Join(dir, "prompts", "plan-mode.md")

	{
		err := os.MkdirAll(filepath.Dir(planPath), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir prompts: %v", err))
	}

	{
		err := os.WriteFile(planPath, []byte("Plan behavior\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write plan prompt: %v", err))
	}

	model := &Model{
		cfg: config.Config{Chat: config.Chat{ProjectRoot: dir}},
		promptRuntimeState: promptRuntimeState{promptRuntimeCompletionState: promptRuntimeCompletionState{
			promptStack: promptstack.Stack{Text: "Base behavior"},
		}},
		agentModeName: "plan",
	}

	first := model.systemPrompt()
	gomega.Expect(strings.Contains(first, "Plan behavior")).To(gomega.BeTrue(), fmt.Sprintf("expected initial mode prompt, got:\n%s", first))

	{
		err := os.Remove(planPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("remove plan prompt: %v", err))
	}

	{
		got := model.contextSummary()
		gomega.Expect(strings.Contains(got, "mode:plan:plan-mode.md")).To(gomega.BeTrue(), fmt.Sprintf("expected context summary to use cached prompt sources, got %q", got))
	}

	second := model.systemPrompt()
	gomega.Expect(second).To(gomega.Equal(first), fmt.Sprintf("expected cached system prompt after mode file removal\nfirst:\n%s\nsecond:\n%s", first, second))

	model.invalidateComposedPrompt()

	{
		prompt := model.systemPrompt()
		gomega.Expect(strings.Contains(prompt, "Plan behavior")).To(gomega.BeFalse(), fmt.Sprintf("expected invalidation to observe missing mode prompt, got:\n%s", prompt))
	}
})

var _ = ginkgo.It("role spec explicit alias can target non default profile", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
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
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	defaults := config.LlamaServer{
		Kind:  "mlx",
		Host:  "127.0.0.1",
		Port:  47680,
		Model: "qwen3.6-27b",
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.LlamaServer = defaults
	spec := runtimesession.ResolveBackendResources(cfg).PlannerSpec

	gomega.Expect(spec.Port).To(gomega.Equal(47783), fmt.Sprintf("resolved spec = %+v, want 27B profile target", spec))
	gomega.Expect(spec.Model).To(gomega.Equal("/models/qwen3.6-27b"), fmt.Sprintf("resolved spec = %+v, want 27B profile target", spec))
})

var _ = ginkgo.It("role spec executor ignores explicit planner alias", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .idiom: %v", err))
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
	{
		err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(body), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write backends.json: %v", err))
	}

	defaults := config.LlamaServer{
		Kind:  "mlx",
		Host:  "127.0.0.1",
		Port:  47680,
		Model: "qwen3.6-27b",
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.Implementer = &defaults
	spec := runtimesession.ResolveBackendResources(cfg).ExecutorSpec

	gomega.Expect(spec.Port).To(gomega.Equal(47781), fmt.Sprintf("resolved spec = %+v, want coder executor", spec))
	gomega.Expect(spec.Model).To(gomega.Equal("/models/qwen3-coder-30b"), fmt.Sprintf("resolved spec = %+v, want coder executor", spec))
})

var _ = ginkgo.It("tune chat options for read only plan disables thinking and caps tokens", func() {
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
	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("expected read-only lane to disable thinking, got %#v", tuned.EnableThinking))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeFalse(), fmt.Sprintf("expected read-only lane to disable thinking, got %#v", tuned.EnableThinking))

	gomega.Expect(tuned.PreserveThinking).To(gomega.BeFalse(), fmt.Sprintf("expected preserve thinking off for read-only lane"))

	gomega.Expect(tuned.ChatTemplateKwargs).To(gomega.BeTrue(), fmt.Sprintf("expected read-only lane to send explicit chat template kwargs"))

	gomega.Expect(tuned.Temperature).To(gomega.Equal(0.2), fmt.Sprintf("read-only sampling = temp %.2f top_p %.2f, want deterministic cap", tuned.Temperature, tuned.TopP))
	gomega.Expect(tuned.TopP).To(gomega.BeNumerically("==", 1), fmt.Sprintf("read-only sampling = temp %.2f top_p %.2f, want deterministic cap", tuned.Temperature, tuned.TopP))

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected grounded explanation token floor of 1536 (8192 exceeds floor, should stay 8192), got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("tune chat options for read only plan raises low max tokens to route floor", func() {
	tests := []struct {
		name               string
		contract           string
		initialMaxTokens   int
		modelThinkingFloor bool
		wantMaxTokens      int
	}{
		{
			name:               "direct answer with thinking floor",
			contract:           string(workerruntime.ContractDirectAnswer),
			modelThinkingFloor: true,
			wantMaxTokens:      4096,
		},
		{
			name:             "direct answer without thinking floor",
			contract:         string(workerruntime.ContractDirectAnswer),
			initialMaxTokens: 128,
			wantMaxTokens:    3072,
		},
		{
			name:          "grounded explanation",
			contract:      "grounded_explanation",
			wantMaxTokens: 1536,
		},
		{
			name:             "locate targets",
			contract:         "locate_targets",
			initialMaxTokens: 128,
			wantMaxTokens:    1536,
		},
		{
			name:          "review findings",
			contract:      "review_findings",
			wantMaxTokens: 2048,
		},
		{
			name:             "architecture assessment",
			contract:         "architecture_assessment",
			initialMaxTokens: 256,
			wantMaxTokens:    2048,
		},
	}

	for _, tt := range tests {
		ginkgo.By(fmt.Sprint(tt.name), func() {
			opts := llama.ChatOptions{
				MaxTokens:          tt.initialMaxTokens,
				ModelThinkingFloor: tt.modelThinkingFloor,
			}
			plan := promptExecutionPlan{
				runtimeBrief: workerruntime.WorkerRuntimeBrief{
					PermissionProfile: "read_only_workspace",
					Contract:          tt.contract,
				},
			}

			tuned := tuneChatOptionsForPlan(opts, plan)
			gomega.Expect(tuned.MaxTokens).To(gomega.Equal(tt.wantMaxTokens), fmt.Sprintf("MaxTokens = %d, want route floor %d", tuned.MaxTokens, tt.wantMaxTokens))
		})
	}
})

var _ = ginkgo.It("tune chat options for read only plan preserves lower configured temperature", func() {
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

	gomega.Expect(tuned.Temperature).To(gomega.Equal(0.1), fmt.Sprintf("read-only low sampling = temp %.2f top_p %.2f, want low temp preserved with deterministic top_p", tuned.Temperature, tuned.TopP))
	gomega.Expect(tuned.TopP).To(gomega.BeNumerically("==", 1), fmt.Sprintf("read-only low sampling = temp %.2f top_p %.2f, want low temp preserved with deterministic top_p", tuned.Temperature, tuned.TopP))

	opts.Temperature = 0
	tuned = tuneChatOptionsForPlan(opts, plan)

	gomega.Expect(tuned.Temperature).To(gomega.BeNumerically("==", 0), fmt.Sprintf("read-only zero sampling = temp %.2f top_p %.2f, want explicit zero temp preserved", tuned.Temperature, tuned.TopP))
	gomega.Expect(tuned.TopP).To(gomega.BeNumerically("==", 1), fmt.Sprintf("read-only zero sampling = temp %.2f top_p %.2f, want explicit zero temp preserved", tuned.Temperature, tuned.TopP))
})

var _ = ginkgo.It("tune chat options for plan execute planner uses strict local tool mode", func() {
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
	gomega.Expect(tuned.Temperature).To(gomega.BeNumerically("==", 0), fmt.Sprintf("plan-execute planner sampling = temp %.2f top_p %.2f, want deterministic tool sampling", tuned.Temperature, tuned.TopP))
	gomega.Expect(tuned.TopP).To(gomega.BeNumerically("==", 1), fmt.Sprintf("plan-execute planner sampling = temp %.2f top_p %.2f, want deterministic tool sampling", tuned.Temperature, tuned.TopP))

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("plan-execute planner should disable local thinking for strict tool emission, got %#v", tuned.EnableThinking))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeFalse(), fmt.Sprintf("plan-execute planner should disable local thinking for strict tool emission, got %#v", tuned.EnableThinking))

	gomega.Expect(tuned.PreserveThinking).To(gomega.BeFalse(), fmt.Sprintf("plan-execute planner should strip hidden reasoning from follow-up history"))

	gomega.Expect(tuned.ChatTemplateKwargs).To(gomega.BeTrue(), fmt.Sprintf("plan-execute planner should send explicit thinking kwargs"))

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("plan-execute planner should not inherit read-only token clamps, got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("tune chat options for implementation lane keeps configured sampling", func() {
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
	gomega.Expect(tuned.Temperature).To(gomega.Equal(0.6), fmt.Sprintf("implementation sampling changed unexpectedly: temp %.2f top_p %.2f", tuned.Temperature, tuned.TopP))
	gomega.Expect(tuned.TopP).To(gomega.Equal(0.95), fmt.Sprintf("implementation sampling changed unexpectedly: temp %.2f top_p %.2f", tuned.Temperature, tuned.TopP))

	gomega.Expect(tuned.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("implementation thinking config should stay as configured, got %#v preserve=%t", tuned.EnableThinking, tuned.PreserveThinking))
	gomega.Expect(*tuned.EnableThinking).To(gomega.BeTrue(), fmt.Sprintf("implementation thinking config should stay as configured, got %#v preserve=%t", tuned.EnableThinking, tuned.PreserveThinking))
	gomega.Expect(tuned.PreserveThinking).To(gomega.BeTrue(), fmt.Sprintf("implementation thinking config should stay as configured, got %#v preserve=%t", tuned.EnableThinking, tuned.PreserveThinking))
})

var _ = ginkgo.It("chat options can force thinking off for run", func() {
	thinkingOn := true
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		backendRuntimeState: backendRuntimeState{modelID: "qwen3.6-35b"},
	}

	model.SetDisableThinkingOverride(true)

	opts := chatOptions(model.cfg.Chat, model.modelID, model.modelThinkingFloor, model.disableThinkingOverride)

	gomega.Expect(opts.Model).To(gomega.Equal("qwen3.6-35b"), fmt.Sprintf("model = %q", opts.Model))

	gomega.Expect(opts.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("expected thinking disabled, got %#v", opts.EnableThinking))
	gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse(), fmt.Sprintf("expected thinking disabled, got %#v", opts.EnableThinking))

	gomega.Expect(opts.PreserveThinking).To(gomega.BeFalse(), fmt.Sprintf("expected preserve thinking off"))

	gomega.Expect(opts.ChatTemplateKwargs).To(gomega.BeTrue(), fmt.Sprintf("expected explicit chat template kwargs for thinking override"))
})

var _ = ginkgo.It("chat options sends template kwargs for config thinking off", func() {
	thinkingOff := false
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOff,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		backendRuntimeState: backendRuntimeState{modelID: "qwen3.6-35b"},
	}

	opts := chatOptions(model.cfg.Chat, model.modelID, model.modelThinkingFloor, model.disableThinkingOverride)

	gomega.Expect(opts.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("expected config thinking disabled, got %#v", opts.EnableThinking))
	gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse(), fmt.Sprintf("expected config thinking disabled, got %#v", opts.EnableThinking))

	gomega.Expect(opts.PreserveThinking).To(gomega.Equal(true), fmt.Sprintf("expected preserve thinking to follow config"))

	gomega.Expect(opts.ChatTemplateKwargs).To(gomega.BeTrue(), fmt.Sprintf("expected explicit chat template kwargs for config thinking off"))
})

var _ = ginkgo.It("chat options omits template kwargs for config thinking on", func() {
	thinkingOn := true
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				EnableThinking:   &thinkingOn,
				PreserveThinking: true,
				MaxTokens:        8192,
			},
		},
		backendRuntimeState: backendRuntimeState{modelID: "qwen3.6-35b"},
	}

	opts := chatOptions(model.cfg.Chat, model.modelID, model.modelThinkingFloor, model.disableThinkingOverride)

	gomega.Expect(opts.EnableThinking).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("expected config thinking enabled, got %#v", opts.EnableThinking))
	gomega.Expect(*opts.EnableThinking).To(gomega.BeTrue(), fmt.Sprintf("expected config thinking enabled, got %#v", opts.EnableThinking))

	gomega.Expect(opts.ChatTemplateKwargs).To(gomega.BeFalse(), fmt.Sprintf("did not expect template kwargs for config thinking on"))
})

var _ = ginkgo.It("apply max tokens override leaves route clamp when unset", func() {
	opts := llama.ChatOptions{MaxTokens: 8192}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "grounded_explanation",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 0)

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(8192), fmt.Sprintf("expected grounded explanation token floor of 1536 (8192 exceeds floor, should stay 8192), got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("apply max tokens override wins over route clamp", func() {
	opts := llama.ChatOptions{MaxTokens: 8192}
	plan := promptExecutionPlan{
		runtimeBrief: workerruntime.WorkerRuntimeBrief{
			PermissionProfile: "read_only_workspace",
			Contract:          "grounded_explanation",
		},
	}

	tuned := tuneChatOptionsForPlan(opts, plan)

	tuned = applyMaxTokensOverride(tuned, 4096)

	gomega.Expect(tuned.MaxTokens).To(gomega.Equal(4096), fmt.Sprintf("expected max-token override, got %d", tuned.MaxTokens))
})

var _ = ginkgo.It("default verification plan prefers go test when go mod exists", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.22\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write go.mod: %v", err))
	}

	plan := defaultVerificationPlan(dir)
	gomega.Expect(len(plan)).To(gomega.Not(gomega.Equal(0)), fmt.Sprintf("expected go test verification plan, got %#v", plan))
	gomega.Expect(plan[0]).To(gomega.Equal("go test ./..."), fmt.Sprintf("expected go test verification plan, got %#v", plan))
})

var _ = ginkgo.It("build prompt execution plan captures mentioned implementation refs as evidence paths", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily("review_then_implement")

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), "Investigate the routing flow in this codebase and then update docs/frontier-routing-implementation-plan.md with what you find.")

	gomega.Expect(plan.route.Family).To(gomega.Equal("review_then_implement"), fmt.Sprintf("unexpected family: %q", plan.route.Family))

	gomega.Expect(len(plan.runtimeBrief.TargetRefs)).To(gomega.Equal(0), fmt.Sprintf("natural implementation prose should not make mentioned paths target refs, got %#v", plan.runtimeBrief.TargetRefs))

	gomega.Expect(slices.Contains(plan.runtimeBrief.EvidencePaths, "docs/frontier-routing-implementation-plan.md")).To(gomega.BeTrue(), fmt.Sprintf("expected mentioned implementation path as evidence, got %#v", plan.runtimeBrief.EvidencePaths))

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredNext, "gather source evidence before large reads of named target refs")).To(gomega.BeFalse(), fmt.Sprintf("mentioned implementation evidence paths must not trigger target-read gate, got %#v", plan.runtimeBrief.RequiredNext))
})

var _ = ginkgo.It("build prompt execution plan honors forced task family", func() {
	model := &Model{}
	model.SetForcedTaskFamily("implementation")

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), "Do not run tests; just inspect this.")
	gomega.Expect(plan.route.Family).To(gomega.Equal("implementation"), fmt.Sprintf("family = %q, want implementation", plan.route.Family))

	gomega.Expect(plan.route.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected forced implementation plan: %#v brief=%#v", plan.route, plan.runtimeBrief))
	gomega.Expect(plan.runtimeBrief.Contract).To(gomega.Equal("implementation_result"), fmt.Sprintf("unexpected forced implementation plan: %#v brief=%#v", plan.route, plan.runtimeBrief))
})

var _ = ginkgo.It("planner owned verification marks implementation brief", func() {
	model := &Model{}
	model.SetForcedTaskFamily("implementation")
	model.SetPlannerOwnedVerification(true)

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), "Modify the file and leave tests to the caller.")

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "planner_owned_verification")).To(gomega.BeTrue(), fmt.Sprintf("expected planner-owned verification constraint, got %#v", plan.runtimeBrief.Constraints))

	gomega.Expect(slices.Contains(plan.runtimeBrief.InvalidActions, "build_or_test_shell")).To(gomega.BeTrue(), fmt.Sprintf("expected build/test shell invalid action, got %#v", plan.runtimeBrief.InvalidActions))
})

var _ = ginkgo.It("plan execute prompt runtime brief does not advertise implementation actions", func() {
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)
	joinedNext := strings.Join(plan.runtimeBrief.RequiredNext, "\n")
	joinedPolicy := strings.Join(plan.runtimeBrief.SearchPolicy, "\n")
	joinedInvalid := strings.Join(plan.runtimeBrief.InvalidActions, "\n")

	for _, want := range []string{
		"source-body obligations before submit_plan",
		"submit_plan scope",
		"packet anchors establish scope but existing paths still need source-body obligations satisfied before submit_plan",
		"do not run build, test, shell, git, or source_sanity in the plan-execute planner phase",
	} {
		gomega.Expect(strings.Contains(joinedNext+"\n"+joinedPolicy, want)).To(gomega.BeTrue(), fmt.Sprintf("plan-execute runtime brief missing %q\nnext=%#v\npolicy=%#v", want, plan.runtimeBrief.RequiredNext, plan.runtimeBrief.SearchPolicy))
	}

	for _, forbidden := range []string{
		"edit an in-scope required path",
		"after source edits run source_sanity",
	} {
		gomega.Expect(strings.Contains(joinedNext, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("plan-execute runtime brief retained implementation action %q\nnext=%#v", forbidden, plan.runtimeBrief.RequiredNext))
	}

	gomega.Expect(strings.Contains(joinedInvalid, "source_sanity_in_planner_phase")).To(gomega.BeTrue(), fmt.Sprintf("plan-execute runtime brief should reject planner-phase source_sanity, got %#v", plan.runtimeBrief.InvalidActions))
})

var _ = ginkgo.It("plan execute runtime brief uses typed candidate scope instead of prompt schema", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	restore := execution.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
		ScopeContract: plancontract.NewPlanScopeContract(nil, []string{
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
	plan := execution.buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(len(plan.runtimeBrief.RequiredEditPaths)).To(gomega.Equal(0), fmt.Sprintf("typed plan-execute candidate scope must not become required coverage, got %#v", plan.runtimeBrief.RequiredEditPaths))

	for _, want := range []string{"internal/surfaces/tui/app/rendering.go", "internal/workflows/productworkflow/status.go"} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("typed plan-execute allowed paths missing %q, got %#v", want, plan.runtimeBrief.AllowedEditPaths))

		gomega.Expect(slices.Contains(plan.runtimeBrief.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("typed plan-execute target refs missing %q, got %#v", want, plan.runtimeBrief.TargetRefs))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation")).To(gomega.BeFalse(), fmt.Sprintf("generated schema text must not add exact-anchor constraints in typed plan-execute scope, got %#v", plan.runtimeBrief.Constraints))
})

var _ = ginkgo.It("plan execute review repair runtime brief carries failed phase obligations", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	restore := execution.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily:                  agent.PlanThenExecuteReviewRepairTaskFamily,
		ReviewRepairOpenObligations: []string{"verification_failed:go test ./..."},
	}, io.Discard)

	defer restore()

	plan := execution.buildPromptExecutionPlan(context.Background(), "review repair")
	gomega.Expect(plan.runtimeBrief.Readiness).To(gomega.Not(gomega.BeNil()), fmt.Sprintf("review-repair runtime brief should carry failed phase obligations, got %#v", plan.runtimeBrief.Readiness))
	gomega.Expect(plan.runtimeBrief.Readiness.OpenObligations).To(gomega.ContainElement("verification_failed:go test ./..."), fmt.Sprintf("review-repair runtime brief should carry failed phase obligations, got %#v", plan.runtimeBrief.Readiness))
})

var _ = ginkgo.It("plan execute runtime brief keeps real candidate scope exact snippets", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	restore := execution.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: agent.PlanThenExecutePlannerTaskFamily,
		ScopeContract: plancontract.NewPlanScopeContract(nil, []string{
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
	plan := execution.buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(len(plan.runtimeBrief.RequiredEditPaths)).To(gomega.Equal(0), fmt.Sprintf("candidate scope snippets must not promote required paths, got %#v", plan.runtimeBrief.RequiredEditPaths))

	{
		got := len(plan.runtimeBrief.RequiredEditSnippets)
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("expected one candidate-scope exact snippet, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets))
	}

	gomega.Expect(plan.runtimeBrief.RequiredEditSnippets[0].Path).To(gomega.Equal("native/src/feature.cpp"), fmt.Sprintf("snippet path = %q, want candidate source path", plan.runtimeBrief.RequiredEditSnippets[0].Path))

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation")).To(gomega.BeTrue(), fmt.Sprintf("real candidate-scope snippets should keep exact-anchor constraint, got %#v", plan.runtimeBrief.Constraints))

	gomega.Expect(slices.Contains(plan.runtimeBrief.SearchPolicy, "preserve_exact_anchor_snippets_in_submit_plan_before_broad_discovery")).To(gomega.BeTrue(), fmt.Sprintf("plan-execute exact snippet should use submit_plan search policy, got %#v", plan.runtimeBrief.SearchPolicy))
})

var _ = ginkgo.It("build prompt execution plan skips classifier when task family forced", func() {
	t := ginkgo.GinkgoT()
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

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), "Run tests and only report results.")
	gomega.Expect(plan.route.Family).To(gomega.Equal("implementation"), fmt.Sprintf("unexpected forced implementation plan: %#v", plan.route))
	gomega.Expect(plan.route.InitialLane).To(gomega.Equal("implement"), fmt.Sprintf("unexpected forced implementation plan: %#v", plan.route))

	{
		got := atomic.LoadInt32(&classifierCalls)
		gomega.Expect(got).To(gomega.BeNumerically("==", 0), fmt.Sprintf("forced task family should bypass classifier, got %d classifier calls", got))
	}
})

var _ = ginkgo.It("build prompt execution plan skips classifier when disabled", func() {
	t := ginkgo.GinkgoT()
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

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), "Run the tests and report failures.")
	gomega.Expect(plan.route.Family).To(gomega.Equal("conversation"), fmt.Sprintf("expected conservative fallback route when classifier disabled, got %#v", plan.route))

	{
		got := atomic.LoadInt32(&classifierCalls)
		gomega.Expect(got).To(gomega.BeNumerically("==", 0), fmt.Sprintf("disabled route classifier should not call model, got %d classifier calls", got))
	}
})

var _ = ginkgo.It("build prompt execution plan captures required edit paths and exact anchor", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{"native/src/simulation/job_queue.cpp", "scripts/ui/status_panel.gd"} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected required edit path %q, got %#v", want, plan.runtimeBrief.RequiredEditPaths))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation")).To(gomega.BeTrue(), fmt.Sprintf("expected exact-anchor constraint, got %#v", plan.runtimeBrief.Constraints))

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope")).To(gomega.BeTrue(), fmt.Sprintf("expected required-path write-scope constraint, got %#v", plan.runtimeBrief.Constraints))

	gomega.Expect(slices.Contains(plan.runtimeBrief.InvalidActions, "broad_read_before_first_edit_attempt")).To(gomega.BeTrue(), fmt.Sprintf("expected broad-read invalid action, got %#v", plan.runtimeBrief.InvalidActions))

	gomega.Expect(slices.Contains(plan.runtimeBrief.SearchPolicy, "treat required_edit_paths plus packet evidence as edit scope; prefer semantic/context/declared project discovery before narrow required-path reads for named edit-safety facts")).To(gomega.BeTrue(), fmt.Sprintf("expected required-path scoped search policy, got %#v", plan.runtimeBrief.SearchPolicy))
})

var _ = ginkgo.It("build prompt execution plan carries objective into runtime brief", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetForcedTaskFamily(agent.PlanThenExecutePlannerTaskFamily)

	prompt := "Smoke test without changing files and answer source-satisfied with evidence. Required edit paths: internal/surfaces/tui/app/app.go, internal/runtime/agent/runner.go."
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(plan.runtimeBrief.Objective).To(gomega.Equal(prompt), fmt.Sprintf("runtime brief objective = %q, want original prompt", plan.runtimeBrief.Objective))

	gomega.Expect(plan.runtimeBrief.TaskFamily).To(gomega.Equal(agent.PlanThenExecutePlannerTaskFamily), fmt.Sprintf("expected plan-execute planner family, got %q", plan.runtimeBrief.TaskFamily))
})

var _ = ginkgo.It("implementation followup runtime brief carries previous recommendation", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:           "implementation",
		Contract:             "implementation_result",
		PermissionProfile:    string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:         []string{"read_file", "search_content", "apply_patch"},
		ForbiddenTools:       []string{"shell"},
		RequiredTools:        []string{workspace.RequiredToolEditFileOrApplyPatch},
		ToolManifest:         []workspace.ToolManifestEntry{{Tool: "read_file", Request: "bounded source read"}},
		EvidenceCapabilities: []string{"source_body"},
		SearchPolicy:         []string{"consume_target_packet_first"},
		TargetPacket:         []string{"prompt_context:structured_packet"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which literal strings in internal/runtime/workerruntime/catalog.go should become enums?"},
		{Role: "assistant", Content: "Move FinalCriticID values and SearchPolicyID values to internal/runtime/workerruntime/vocabulary.go, then replace the corresponding catalog.go literals."},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(slices.Contains(got.TargetPacket, "conversation_context:previous_assistant_recommendation")).To(gomega.BeTrue(), fmt.Sprintf("expected previous recommendation marker, got %#v", got.TargetPacket))

	packet := strings.Join(got.TargetPacket, "\n")
	for _, want := range []string{"FinalCriticID", "SearchPolicyID", "vocabulary.go", "catalog.go"} {
		gomega.Expect(strings.Contains(packet, want)).To(gomega.BeTrue(), fmt.Sprintf("target packet missing %q:\n%s", want, packet))
	}

	gomega.Expect(got.AllowedTools).To(gomega.Equal(baseline.AllowedTools), fmt.Sprintf("follow-up context must not mutate tool-control fields:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.ForbiddenTools).To(gomega.Equal(baseline.ForbiddenTools), fmt.Sprintf("follow-up context must not mutate tool-control fields:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.RequiredTools).To(gomega.Equal(baseline.RequiredTools), fmt.Sprintf("follow-up context must not mutate tool-control fields:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.ToolManifest).To(gomega.Equal(baseline.ToolManifest), fmt.Sprintf("follow-up context must not mutate tool-control fields:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.EvidenceCapabilities).To(gomega.Equal(baseline.EvidenceCapabilities), fmt.Sprintf("follow-up context must not mutate tool-control fields:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(slices.Contains(got.TargetPacket, "prompt_context:structured_packet")).To(gomega.BeTrue(), fmt.Sprintf("existing target packet entries should be preserved, got %#v", got.TargetPacket))
	for _, want := range []string{"internal/runtime/workerruntime/vocabulary.go", "internal/runtime/workerruntime/catalog.go"} {
		gomega.Expect(slices.Contains(got.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected prior recommendation edit path %q in allowed edit paths, got %#v", want, got.AllowedEditPaths))
		gomega.Expect(slices.Contains(got.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected prior recommendation edit path %q in target refs, got %#v", want, got.TargetRefs))
		gomega.Expect(strings.Contains(packet, "Editable candidate file from previous assistant recommendation: "+want)).To(gomega.BeTrue(), fmt.Sprintf("target packet should surface editable candidate file %q:\n%s", want, packet))
	}

	rendered := workerruntime.RenderInitialRuntimeBrief(got)
	gomega.Expect(strings.Contains(rendered, "conversation_context:previous_assistant_recommendation")).To(gomega.BeTrue(), fmt.Sprintf("rendered runtime brief should carry previous recommendation context:\n%s", rendered))
	gomega.Expect(rendered).To(gomega.ContainSubstring("internal/runtime/workerruntime/vocabulary.go"), fmt.Sprintf("rendered runtime brief should carry prior recommendation edit scope:\n%s", rendered))
	gomega.Expect(rendered).To(gomega.ContainSubstring("internal/runtime/workerruntime/catalog.go"), fmt.Sprintf("rendered runtime brief should carry prior recommendation edit scope:\n%s", rendered))
})

var _ = ginkgo.It("implementation followup runtime brief promotes recommendation backed source surfaces", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "internal/runtime/workerruntime/catalog.go contains some literal strings that should be enums or constants. Can you identify where those should be going?"},
		{Role: "assistant", Content: strings.Join([]string{
			"## Literal strings in `catalog.go` that should be typed constants",
			"`vocabulary.go` defines the type `FinalCriticID`, but no constants are defined. Every `FinalCriticID` in `catalog.go` is a raw string literal.",
			"**Where they should go**: a new `const` block in `vocabulary.go`.",
			"Then `catalog.go` lines 63, 75, and 87 would reference these constants instead of raw strings.",
			"",
			"### 2. `SearchPolicyID` values",
			"`vocabulary.go` defines the type `SearchPolicyID`, but no constants are defined. Every `SearchPolicyID` in `catalog.go` and `roles.go` is a raw string literal.",
			"Both should get a typed `const` block in `vocabulary.go`, and `catalog.go`/`roles.go` should reference those constants instead of raw strings.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	for _, want := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
		"internal/runtime/workerruntime/roles.go",
	} {
		gomega.Expect(slices.Contains(got.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected recommendation-backed edit path %q in allowed edit paths, got %#v", want, got.AllowedEditPaths))
		gomega.Expect(slices.Contains(got.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected recommendation-backed target ref %q, got %#v", want, got.TargetRefs))
	}
})

var _ = ginkgo.It("implementation followup runtime brief promotes recommendation section paths", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
		TargetPacket:      []string{"prompt_context:structured_packet"},
		SearchPolicy:      []string{"consume_target_packet_first"},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "internal/runtime/workerruntime/catalog.go contains some literal strings that should be enums or constants. Can you identify where those should be going?"},
		{Role: "assistant", Content: strings.Join([]string{
			"Here is my analysis of `internal/runtime/workerruntime/catalog.go`:",
			"`requiredToolGitHistoryLookup` should be a typed constant in `vocabulary.go`.",
			"Other inline tool and policy strings follow the same pattern.",
			"",
			"**Summary of where they should go:**",
			"All of these should be defined as typed constants in **`internal/runtime/workerruntime/vocabulary.go`**, which already defines the surrounding ID types.",
			"The `SearchPolicyID` inline strings should become constants in `vocabulary.go`.",
			"Then replace the corresponding `catalog.go` literals with those constants.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	packet := strings.Join(got.TargetPacket, "\n")
	for _, want := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		gomega.Expect(slices.Contains(got.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected recommendation-section edit path %q in allowed edit paths, got %#v", want, got.AllowedEditPaths))
		gomega.Expect(slices.Contains(got.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected recommendation-section target ref %q, got %#v", want, got.TargetRefs))
		gomega.Expect(strings.Contains(packet, "Editable candidate file from previous assistant recommendation: "+want)).To(gomega.BeTrue(), fmt.Sprintf("target packet should surface editable candidate file %q:\n%s", want, packet))
	}

	rendered := workerruntime.RenderInitialRuntimeBrief(got)
	for _, want := range []string{
		"\"allowed_edit_paths\"",
		"\"target_refs\"",
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		gomega.Expect(strings.Contains(rendered, want)).To(gomega.BeTrue(), fmt.Sprintf("rendered runtime brief missing %q:\n%s", want, rendered))
	}
})

var _ = ginkgo.It("implementation followup runtime brief promotes path only recommendation section", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "internal/runtime/workerruntime/catalog.go contains literal strings that should be constants."},
		{Role: "assistant", Content: strings.Join([]string{
			"Summary of where they should go:",
			"`internal/runtime/workerruntime/vocabulary.go`",
			"`internal/runtime/workerruntime/catalog.go`",
			"",
			"Evidence inspected:",
			"`internal/runtime/workerruntime/roles.go` has related raw strings, but no recommendation is available for that file yet.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	for _, want := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		gomega.Expect(slices.Contains(got.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected path-only recommendation section to promote %q, got %#v", want, got.AllowedEditPaths))
	}
	gomega.Expect(slices.Contains(got.AllowedEditPaths, "internal/runtime/workerruntime/roles.go")).To(gomega.BeFalse(), fmt.Sprintf("recommendation section should stop before later evidence-only path, got %#v", got.AllowedEditPaths))
})

var _ = ginkgo.It("implementation followup runtime brief does not promote missing recommendation path", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	{
		err := os.MkdirAll(filepath.Join(root, "internal/runtime/workerruntime"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir fixture: %v", err))
	}
	for _, rel := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		{
			err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("package workerruntime\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write fixture %s: %v", rel, err))
		}
	}

	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which strings in internal/runtime/workerruntime/catalog.go should become constants?"},
		{Role: "assistant", Content: strings.Join([]string{
			"Summary of where they should go:",
			"`internal/runtime/workerruntime/vocabulary.go` for the constants.",
			"`internal/runtime/workerruntime/analysis_agents.go` for the analysis profile.",
			"Then update `internal/runtime/workerruntime/catalog.go` references.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
		root,
	)

	for _, want := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		gomega.Expect(slices.Contains(got.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected existing recommendation path %q to become editable, got %#v", want, got.AllowedEditPaths))
	}
	gomega.Expect(got.AllowedEditPaths).To(gomega.Not(gomega.ContainElement("internal/runtime/workerruntime/analysis_agents.go")), fmt.Sprintf("missing prior recommendation path must not become edit authority:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(got.TargetRefs).To(gomega.Not(gomega.ContainElement("internal/runtime/workerruntime/analysis_agents.go")), fmt.Sprintf("missing prior recommendation path must not become edit authority:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(strings.Contains(strings.Join(got.TargetPacket, "\n"), "Unresolved prior recommendation path; not granted as edit scope until verified or explicitly authorized as a new file: internal/runtime/workerruntime/analysis_agents.go")).To(gomega.BeTrue(), fmt.Sprintf("missing prior recommendation should remain visible as unresolved context, got %#v", got.TargetPacket))
})

var _ = ginkgo.It("implementation followup runtime brief does not promote evidence only paths", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which strings in internal/runtime/workerruntime/catalog.go should become constants?"},
		{Role: "assistant", Content: strings.Join([]string{
			"Evidence inspected:",
			"- `internal/runtime/workerruntime/catalog.go` defines the current catalog entries.",
			"- `internal/runtime/workerruntime/roles.go` references related role metadata.",
			"",
			"No implementation recommendation is available from that evidence yet.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(len(got.AllowedEditPaths)).To(gomega.Equal(0), fmt.Sprintf("evidence-only prior paths must not become editable candidates:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(len(got.TargetRefs)).To(gomega.Equal(0), fmt.Sprintf("evidence-only prior paths must not become editable candidates:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(strings.Contains(strings.Join(got.TargetPacket, "\n"), "Editable candidate file from previous assistant recommendation")).To(gomega.BeFalse(), fmt.Sprintf("evidence-only prior answer should not mark editable candidate files: %#v", got.TargetPacket))
})

var _ = ginkgo.It("implementation followup runtime brief does not promote raw literal evidence as edit scope", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which strings in internal/runtime/workerruntime/catalog.go should become constants?"},
		{Role: "assistant", Content: strings.Join([]string{
			"`internal/runtime/workerruntime/vocabulary.go` defines the type `FinalCriticID`, but no constants are defined.",
			"Every `FinalCriticID` in `internal/runtime/workerruntime/catalog.go` is a raw string literal.",
			"No destination recommendation is available yet.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(len(got.AllowedEditPaths)).To(gomega.Equal(0), fmt.Sprintf("raw-literal evidence must not become editable candidate scope without recommendation language:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(len(got.TargetRefs)).To(gomega.Equal(0), fmt.Sprintf("raw-literal evidence must not become editable candidate scope without recommendation language:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(strings.Contains(strings.Join(got.TargetPacket, "\n"), "Editable candidate file from previous assistant recommendation")).To(gomega.BeFalse(), fmt.Sprintf("raw-literal evidence should not mark editable candidate files: %#v", got.TargetPacket))
})

var _ = ginkgo.It("implementation followup runtime brief does not open section from negative destination prose", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which strings in internal/runtime/workerruntime/catalog.go should become constants?"},
		{Role: "assistant", Content: strings.Join([]string{
			"No destination recommendation is available yet.",
			"`internal/runtime/workerruntime/roles.go` was inspected only as related evidence.",
		}, "\n")},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(len(got.AllowedEditPaths)).To(gomega.Equal(0), fmt.Sprintf("negative destination prose must not open editable candidate scope:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(len(got.TargetRefs)).To(gomega.Equal(0), fmt.Sprintf("negative destination prose must not open editable candidate scope:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
})

var _ = ginkgo.It("implementation followup runtime brief does not promote same line negative destination path", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "search_content", "apply_patch"},
		RequiredTools:     []string{workspace.RequiredToolEditFileOrApplyPatch},
	}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which strings in internal/runtime/workerruntime/catalog.go should become constants?"},
		{Role: "assistant", Content: "No destination recommendation is available for `internal/runtime/workerruntime/roles.go`."},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(len(got.AllowedEditPaths)).To(gomega.Equal(0), fmt.Sprintf("same-line negative destination path must not become editable candidate scope:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
	gomega.Expect(len(got.TargetRefs)).To(gomega.Equal(0), fmt.Sprintf("same-line negative destination path must not become editable candidate scope:\nallowed=%#v\ntarget_refs=%#v", got.AllowedEditPaths, got.TargetRefs))
})

var _ = ginkgo.It("implementation followup runtime brief leaves standalone implementation alone", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       "implementation",
		Contract:         "implementation_result",
		AllowedTools:     []string{"read_file", "search_content", "apply_patch"},
		SearchPolicy:     []string{"consume_target_packet_first"},
		TargetPacket:     []string{"prompt_context:structured_packet"},
		AllowedEditPaths: []string{"internal/runtime/workerruntime/catalog.go"},
		TargetRefs:       []string{"internal/runtime/workerruntime/catalog.go"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "assistant", Content: "Prior answer that should not be carried."},
		{Role: "user", Content: "rename the qwen model label constant"},
	}

	got := implementationFollowupRuntimeBrief(
		"rename the qwen model label constant",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(reflect.DeepEqual(got, baseline)).To(gomega.BeTrue(), fmt.Sprintf("standalone implementation should preserve the normalized baseline:\ngot=%#v\nbaseline=%#v", got, baseline))
})

var _ = ginkgo.It("implementation followup runtime brief does not widen explicit scope", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedEditPaths:  []string{"pkg/a_test.go"},
		TargetRefs:        []string{"pkg/a_test.go"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which literals should move from internal/runtime/workerruntime/catalog.go?"},
		{Role: "assistant", Content: "Editable candidate file: internal/runtime/workerruntime/vocabulary.go\nMove the constants there and update internal/runtime/workerruntime/catalog.go."},
		{Role: "user", Content: "move these values to the locations you recommended, but only edit pkg/a_test.go"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended, but only edit pkg/a_test.go",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(slices.Contains(got.TargetPacket, "conversation_context:previous_assistant_recommendation")).To(gomega.BeTrue(), fmt.Sprintf("explicit-scope follow-up should still carry prior recommendation packet, got %#v", got.TargetPacket))
	gomega.Expect(got.AllowedEditPaths).To(gomega.Equal(baseline.AllowedEditPaths), fmt.Sprintf("current-turn explicit scope must not be widened by prior answer candidates:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.TargetRefs).To(gomega.Equal(baseline.TargetRefs), fmt.Sprintf("current-turn explicit scope must not be widened by prior answer candidates:\ngot=%#v\nbaseline=%#v", got, baseline))
	packet := strings.Join(got.TargetPacket, "\n")
	gomega.Expect(strings.Contains(packet, "Editable candidate file from previous assistant recommendation")).To(gomega.BeFalse(), fmt.Sprintf("explicit-scope follow-up should not mark prior paths as editable candidate files: %#v", got.TargetPacket))
	if strings.Contains(packet, "Editable candidate file: internal/runtime/workerruntime/vocabulary.go") {
		gomega.Expect(packet).To(gomega.ContainSubstring("read_only_evidence"), fmt.Sprintf("explicit-scope follow-up must neutralize writable prior-answer target-packet phrasing: %#v", got.TargetPacket))
	}
})

var _ = ginkgo.It("implementation followup runtime brief ignores recommended defaults false positive", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:        "implementation",
		Contract:          "implementation_result",
		PermissionProfile: string(workerruntime.PermissionGuardedWorkspaceWrite),
		AllowedTools:      []string{"read_file", "apply_patch"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "assistant", Content: "Move values to internal/runtime/workerruntime/vocabulary.go."},
		{Role: "user", Content: "use the recommended defaults in internal/runtime/workerruntime/catalog.go"},
	}

	got := implementationFollowupRuntimeBrief(
		"use the recommended defaults in internal/runtime/workerruntime/catalog.go",
		history,
		turnRoutePlan{Family: "implementation"},
		brief,
	)

	gomega.Expect(reflect.DeepEqual(got, baseline)).To(gomega.BeTrue(), fmt.Sprintf("bare recommended wording should not carry unrelated prior answer:\ngot=%#v\nbaseline=%#v", got, baseline))
})

var _ = ginkgo.It("implementation followup runtime brief ignores non implementation anaphora", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:   "conversation",
		Contract:     "direct_answer",
		AllowedTools: []string{"read_file", "search_content"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "assistant", Content: "Move values to internal/runtime/workerruntime/vocabulary.go."},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: "conversation"},
		brief,
	)

	gomega.Expect(reflect.DeepEqual(got, baseline)).To(gomega.BeTrue(), fmt.Sprintf("non-implementation route should not carry prior recommendation context:\ngot=%#v\nbaseline=%#v", got, baseline))
})

var _ = ginkgo.It("implementation followup runtime brief does not promote plan execute scope", func() {
	brief := workerruntime.WorkerRuntimeBrief{
		TaskFamily:       agent.PlanThenExecutePlannerTaskFamily,
		Contract:         "implementation_result",
		AllowedTools:     []string{"read_file", "submit_plan"},
		AllowedEditPaths: []string{"internal/runtime/workerruntime/existing.go"},
		TargetRefs:       []string{"internal/runtime/workerruntime/existing.go"},
	}
	baseline := workerruntime.NormalizeRuntimeBrief(brief)
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which values should move from internal/runtime/workerruntime/catalog.go?"},
		{Role: "assistant", Content: "Move values to internal/runtime/workerruntime/vocabulary.go, then replace catalog.go literals."},
		{Role: "user", Content: "move these values to the locations you recommended"},
	}

	got := implementationFollowupRuntimeBrief(
		"move these values to the locations you recommended",
		history,
		turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily},
		brief,
	)

	gomega.Expect(slices.Contains(got.TargetPacket, "conversation_context:previous_assistant_recommendation")).To(gomega.BeTrue(), fmt.Sprintf("plan-execute follow-up should still carry prior recommendation packet, got %#v", got.TargetPacket))
	gomega.Expect(got.AllowedEditPaths).To(gomega.Equal(baseline.AllowedEditPaths), fmt.Sprintf("plan-execute follow-up must not promote prior answer paths into planner scope:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(got.TargetRefs).To(gomega.Equal(baseline.TargetRefs), fmt.Sprintf("plan-execute follow-up must not promote prior answer paths into planner scope:\ngot=%#v\nbaseline=%#v", got, baseline))
	gomega.Expect(strings.Contains(strings.Join(got.TargetPacket, "\n"), "Editable candidate file from previous assistant recommendation")).To(gomega.BeFalse(), fmt.Sprintf("plan-execute follow-up should not mark prior paths as editable candidate files: %#v", got.TargetPacket))
})

var _ = ginkgo.It("plan execute command followup carries context without promoting scope", func() {
	t := ginkgo.GinkgoT()
	model := &Model{cfg: config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}}, agentModeName: "build"}
	objective := "move these values to the locations you recommended"
	plan := newDetachedPromptExecutionSession(context.Background(), model).planExecuteCommandExecutionPlan(objective)
	plan.runtimeBrief.AllowedEditPaths = []string{"internal/runtime/workerruntime/existing.go"}
	plan.runtimeBrief.TargetRefs = []string{"internal/runtime/workerruntime/existing.go"}
	history := []llama.ChatMessage{
		{Role: "user", Content: "Which values should move from internal/runtime/workerruntime/catalog.go?"},
		{Role: "assistant", Content: "Move values to internal/runtime/workerruntime/vocabulary.go, then replace catalog.go literals."},
		{Role: "user", Content: objective},
	}

	got := promptExecutionPlanWithConversationContext(objective, history, plan)

	gomega.Expect(slices.Contains(got.runtimeBrief.TargetPacket, implementationFollowupPriorAssistantMarker)).To(gomega.BeTrue(), fmt.Sprintf("plan-execute follow-up should carry prior recommendation context, got %#v", got.runtimeBrief.TargetPacket))
	gomega.Expect(got.runtimeBrief.AllowedEditPaths).To(gomega.Equal([]string{"internal/runtime/workerruntime/existing.go"}), fmt.Sprintf("plan-execute follow-up must not promote prior answer paths into planner scope: %#v", got.runtimeBrief))
	gomega.Expect(got.runtimeBrief.TargetRefs).To(gomega.Equal([]string{"internal/runtime/workerruntime/existing.go"}), fmt.Sprintf("plan-execute follow-up must not promote prior answer paths into planner scope: %#v", got.runtimeBrief))
})

var _ = ginkgo.It("implement command followup uses raw conversation when payload history is compacted", func() {
	t := ginkgo.GinkgoT()
	model := newTestModelWithWorkspace(t)
	root := model.workspace.Root()
	{
		err := os.MkdirAll(filepath.Join(root, "internal/runtime/workerruntime"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir fixture: %v", err))
	}
	{
		err := os.WriteFile(filepath.Join(root, "internal/runtime/workerruntime/vocabulary.go"), []byte("package workerruntime\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write vocabulary fixture: %v", err))
	}

	objective := "move these values to the locations you recommended"
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	plan := execution.implementCommandExecutionPlan(objective)
	execution.messages = []llama.ChatMessage{
		{Role: "user", Content: "Where should the catalog constants go?"},
		{Role: "assistant", Content: "Move them to internal/runtime/workerruntime/vocabulary.go."},
		{Role: "user", Content: objective},
	}
	payloadHistory := []llama.ChatMessage{
		{Role: "system", Content: execution.systemPrompt()},
		{Role: "user", Content: objective},
	}

	got := promptExecutionPlanWithConversationContext(objective, execution.conversationFollowupHistory(objective, false), plan, root)
	lost := promptExecutionPlanWithConversationContext(objective, payloadHistory, plan, root)

	gomega.Expect(slices.Contains(got.runtimeBrief.AllowedEditPaths, "internal/runtime/workerruntime/vocabulary.go")).To(gomega.BeTrue(), fmt.Sprintf("raw conversation follow-up should recover prior recommendation edit path, got %#v", got.runtimeBrief.AllowedEditPaths))
	gomega.Expect(slices.Contains(lost.runtimeBrief.AllowedEditPaths, "internal/runtime/workerruntime/vocabulary.go")).To(gomega.BeFalse(), fmt.Sprintf("compacted payload-only history should not be the source of prior recommendation recovery: %#v", lost.runtimeBrief.AllowedEditPaths))
})

var _ = ginkgo.It("implement command conversation followup projects recommendation into runner prompt", func() {
	t := ginkgo.GinkgoT()
	model := newTestModelWithWorkspace(t)
	root := model.workspace.Root()
	{
		err := os.MkdirAll(filepath.Join(root, "internal/runtime/workerruntime"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir fixture: %v", err))
	}
	for _, rel := range []string{
		"internal/runtime/workerruntime/vocabulary.go",
		"internal/runtime/workerruntime/catalog.go",
	} {
		{
			err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("package workerruntime\n"), 0o644)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write fixture %s: %v", rel, err))
		}
	}
	model.SetNonInteractive(true)
	client := &scriptedLLMRecorder{
		responses:         []string{"I will apply the recommended edit scope."},
		assertCallCount:   true,
		expectedCallCount: 2,
	}
	model.client = client.Mock(t)
	model.messages = []llama.ChatMessage{
		{Role: "user", Content: "Which literal strings in internal/runtime/workerruntime/catalog.go should become enums?"},
		{Role: "assistant", Content: "The fix: add FinalCriticID and SearchPolicyID constants in internal/runtime/workerruntime/vocabulary.go, then replace catalog.go literals."},
	}

	_, _ = model.RunPromptOnce(context.Background(), "/implement move these values to the locations you recommended")

	gomega.Expect(len(client.messages)).NotTo(gomega.Equal(0), fmt.Sprint("expected implement command to reach the runner model call"))

	firstRequest := renderMessagesForRuntimeBriefAssertion(client.messages[0])
	for _, want := range []string{
		"conversation_context:previous_assistant_recommendation",
		"FinalCriticID",
		"SearchPolicyID",
		"Editable candidate file from previous assistant recommendation: internal/runtime/workerruntime/vocabulary.go",
		"Editable candidate file from previous assistant recommendation: internal/runtime/workerruntime/catalog.go",
		"target_refs",
		"allowed_edit_paths",
	} {
		gomega.Expect(strings.Contains(firstRequest, want)).To(gomega.BeTrue(), fmt.Sprintf("runner prompt missing %q:\n%s", want, firstRequest))
	}
})

func renderMessagesForRuntimeBriefAssertion(messages []llama.ChatMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Role)
		b.WriteString(":\n")
		b.WriteString(msg.Content)
		b.WriteString("\n")
	}

	return b.String()
}

var _ = ginkgo.It("plan execute prompt runner direct question uses conversation only brief", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	model := &Model{
		cfg: config.Config{
			Chat:      config.Chat{ProjectRoot: root},
			Workspace: config.Workspace{Root: root, CWD: root},
		},
		agentModeName: "build",
	}
	execution := newDetachedPromptExecutionSession(context.Background(), model)
	restore := execution.applyPlanExecutePromptRunnerState(config.Default(), planexecute.RunOptions{
		TaskFamily: "direct_question",
	}, io.Discard)

	defer restore()

	plan := execution.buildPromptExecutionPlan(context.Background(), "answer directly")
	gomega.Expect(plan.runtimeBrief.PermissionProfile).To(gomega.Equal("conversation_only"), fmt.Sprintf("permission profile = %q, want conversation_only", plan.runtimeBrief.PermissionProfile))

	gomega.Expect(len(plan.runtimeBrief.AllowedTools)).To(gomega.Equal(0), fmt.Sprintf("direct-question prompt runner should not advertise workspace evidence gates, brief=%#v", plan.runtimeBrief))
	gomega.Expect(len(plan.runtimeBrief.RequiredEvidence)).To(gomega.Equal(0), fmt.Sprintf("direct-question prompt runner should not advertise workspace evidence gates, brief=%#v", plan.runtimeBrief))
})

var _ = ginkgo.It("build prompt execution plan scopes wrapped plan execute objective", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), wrapped)

	gomega.Expect(strings.Contains(plan.runtimeBrief.Objective, "planner addendum text")).To(gomega.BeFalse(), fmt.Sprintf("runtime brief objective leaked planner addendum: %q", plan.runtimeBrief.Objective))

	gomega.Expect(plan.runtimeBrief.Objective).To(gomega.ContainSubstring("source-satisfied"), fmt.Sprintf("runtime brief objective lost source-satisfied no-write intent: %q", plan.runtimeBrief.Objective))
	gomega.Expect(plan.runtimeBrief.Objective).To(gomega.ContainSubstring("without changing files"), fmt.Sprintf("runtime brief objective lost source-satisfied no-write intent: %q", plan.runtimeBrief.Objective))
})

var _ = ginkgo.It("required edit paths table does not capture prose slash tokens", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{"native/tests/test_funeral.cpp", "native/src/simulation/creatures/social_welfare.cpp"} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected required edit path %q, got %#v", want, plan.runtimeBrief.RequiredEditPaths))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "RIVAL/NEMESIS")).To(gomega.BeFalse(), fmt.Sprintf("slash-shaped prose leaked into required edit paths: %#v", plan.runtimeBrief.RequiredEditPaths))
})

var _ = ginkgo.It("build prompt execution plan does not treat generic anchor guidance as exact anchor", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_track_system.cpp")).To(gomega.BeTrue(), fmt.Sprintf("expected required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths))

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "exact_anchor_implementation")).To(gomega.BeFalse(), fmt.Sprintf("generic launcher guidance and verified-state snippets should not force exact-anchor write-first: %#v", plan.runtimeBrief.Constraints))

	gomega.Expect(slices.Contains(plan.runtimeBrief.InvalidActions, "broad_read_before_first_edit_attempt")).To(gomega.BeFalse(), fmt.Sprintf("non-exact add-test brief should allow bounded prewrite reads, got %#v", plan.runtimeBrief.InvalidActions))

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredNext, "use semantic/context/declared project discovery or narrow reads only for one named edit-safety fact; after that fact is resolved, edit an in-scope required path or report <<QUESTION>>")).To(gomega.BeTrue(), fmt.Sprintf("expected non-exact required-path packet to bias toward discovery/read/edit/block after bounded probes, got %#v", plan.runtimeBrief.RequiredNext))
})

var _ = ginkgo.It("build prompt execution plan captures rich context packet evidence", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		"prompt_context:context_packet",
		"prompt_context:verified_current_state",
		"prompt_context:required_edits",
		"prompt_context:structured_packet",
	} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.TargetPacket, want)).To(gomega.BeTrue(), fmt.Sprintf("expected target packet marker %q, got %#v", want, plan.runtimeBrief.TargetPacket))
	}

	for _, want := range []string{
		"native/src/simulation/transport/route_graph.cpp",
		"native/src/simulation/transport/route_graph.h",
		"native/tests/test_transport_system.cpp",
	} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.EvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected context evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp")).To(gomega.BeTrue(), fmt.Sprintf("expected required edit path from rich packet prompt, got %#v", plan.runtimeBrief.RequiredEditPaths))
})

var _ = ginkgo.It("build prompt execution plan captures colon style context packet evidence", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:context_packet")).To(gomega.BeTrue(), fmt.Sprintf("expected colon-style context packet marker, got %#v", plan.runtimeBrief.TargetPacket))

	for _, want := range []string{
		"native/src/simulation/transport/route_graph.cpp",
		"native/src/simulation/transport/route_graph.h",
	} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.EvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected colon-style context evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp")).To(gomega.BeTrue(), fmt.Sprintf("expected colon-style required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths))
})

var _ = ginkgo.It("build prompt execution plan captures required reads as evidence", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		".idiom/refine/example/iter-1/draft.md",
		".idiom/refine/example/iter-1/review.md",
		"native/src/simulation/ai/appraisal.cpp",
	} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.EvidencePaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected required-read evidence path %q, got %#v", want, plan.runtimeBrief.EvidencePaths))
	}

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, ".idiom/refine/example/iter-2/draft.md")).To(gomega.BeTrue(), fmt.Sprintf("expected output draft as required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths))
})

var _ = ginkgo.It("prompt structured packet marker requires packet shape", func() {
	gomega.Expect(plancontract.PromptHasStructuredPacketMarker(`{"evidence":"one incidental field"}`)).To(gomega.BeFalse(), fmt.Sprint("single generic evidence field should not be enough to mark a structured packet"))

	gomega.Expect(plancontract.PromptHasStructuredPacketMarker(`Document how planning_packet_v2 is named in prompts.`)).To(gomega.BeFalse(), fmt.Sprint("bare planning_packet prose should not mark a structured packet"))

	gomega.Expect(plancontract.PromptHasStructuredPacketMarker(`Document the "schema" for planning_packet_v2 prompts.`)).To(gomega.BeFalse(), fmt.Sprint("planning_packet prose plus quoted schema should not mark a structured packet"))

	gomega.Expect(plancontract.PromptHasStructuredPacketMarker(`{"candidate_scope":[{"path":"a/b.go"}],"evidence":[{"source":"a/c.go"}]}`)).To(gomega.BeTrue(), fmt.Sprint("candidate scope plus evidence should mark a structured packet"))
})

var _ = ginkgo.It("prompt context packet projection prefers structured packet", func() {
	projection := plancontract.PromptContextPacketProjectionFromPrompt(`{"candidate_scope":[{"path":"a/b.go"}],"evidence":[{"source":"a/c.go"}]}`)
	gomega.Expect(projection.StructuredPacket).To(gomega.BeTrue(), fmt.Sprintf("structured packet should be typed dispatchable without heading fallback: %#v", projection))
	gomega.Expect(projection.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("structured packet should be typed dispatchable without heading fallback: %#v", projection))
	gomega.Expect(projection.LegacyHeadingFallback).To(gomega.BeFalse(), fmt.Sprintf("structured packet should be typed dispatchable without heading fallback: %#v", projection))
	gomega.Expect(slices.Contains(projection.Markers, "prompt_context:structured_packet")).To(gomega.BeTrue(), fmt.Sprintf("structured projection missing marker: %#v", projection))
})

var _ = ginkgo.It("prompt context packet projection marks heading scan as compatibility fallback", func() {
	projection := plancontract.PromptContextPacketProjectionFromPrompt("## Context Pack\n\n- internal/surfaces/tui/app/prompt_routing.go\n")
	gomega.Expect(projection.StructuredPacket).To(gomega.BeFalse(), fmt.Sprintf("heading context should be compatibility fallback, got %#v", projection))
	gomega.Expect(projection.LegacyHeadingFallback).To(gomega.BeTrue(), fmt.Sprintf("heading context should be compatibility fallback, got %#v", projection))
	gomega.Expect(projection.State).To(gomega.Equal(workflowstate.StateDispatchable), fmt.Sprintf("heading context should be compatibility fallback, got %#v", projection))
	gomega.Expect(projection.Dispatchable).To(gomega.BeTrue(), fmt.Sprintf("heading context should be compatibility fallback, got %#v", projection))
})

var _ = ginkgo.It("prompt context packet projection requires structured or legacy evidence", func() {
	projection := plancontract.PromptContextPacketProjectionFromPrompt("Explain the prompt router without any attached packet.")
	gomega.Expect(projection.StructuredPacket).To(gomega.BeFalse(), fmt.Sprintf("plain prompt should not be context-packet dispatchable: %#v", projection))
	gomega.Expect(projection.LegacyHeadingFallback).To(gomega.BeFalse(), fmt.Sprintf("plain prompt should not be context-packet dispatchable: %#v", projection))
	gomega.Expect(projection.Dispatchable).To(gomega.BeFalse(), fmt.Sprintf("plain prompt should not be context-packet dispatchable: %#v", projection))
	gomega.Expect(projection.State).To(gomega.Equal(workflowstate.StateTerminal), fmt.Sprintf("plain prompt should not be context-packet dispatchable: %#v", projection))
	gomega.Expect(projection.Reason).To(gomega.Equal("missing_structured_packet_or_legacy_heading"), fmt.Sprintf("unexpected projection reason: %#v", projection))
})

var _ = ginkgo.It("prompt context packet marker requires section content", func() {
	prompt := `
Context Packet:

Required Behavior:
- Explain the route.
`
	{
		markers := plancontract.PromptContextPacketMarkersFromPrompt(prompt)
		gomega.Expect(slices.Contains(markers, "prompt_context:context_packet")).To(gomega.BeFalse(), fmt.Sprintf("empty context packet heading should not create marker, got %#v", markers))
	}
})

var _ = ginkgo.It("prompt context packet marker accepts trailing colon path content", func() {
	prompt := `
Context Packet:
- internal/surfaces/tui/app/prompt_routing.go:
`
	{
		markers := plancontract.PromptContextPacketMarkersFromPrompt(prompt)
		gomega.Expect(slices.Contains(markers, "prompt_context:context_packet")).To(gomega.BeTrue(), fmt.Sprintf("path content ending in colon should create context packet marker, got %#v", markers))
	}
})

var _ = ginkgo.It("prompt context packet marker accepts nested evidence section", func() {
	prompt := `
Context Packet:
Evidence:
- internal/surfaces/tui/app/prompt_routing.go
`
	{
		markers := plancontract.PromptContextPacketMarkersFromPrompt(prompt)
		gomega.Expect(slices.Contains(markers, "prompt_context:context_packet")).To(gomega.BeTrue(), fmt.Sprintf("nested evidence should create context packet marker, got %#v", markers))
	}
})

var _ = ginkgo.It("build prompt execution plan captures structured required edit paths", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(slices.Contains(plan.runtimeBrief.TargetPacket, "prompt_context:structured_packet")).To(gomega.BeTrue(), fmt.Sprintf("expected structured packet marker, got %#v", plan.runtimeBrief.TargetPacket))

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/tests/test_transport_system.cpp")).To(gomega.BeTrue(), fmt.Sprintf("expected structured required edit path, got %#v", plan.runtimeBrief.RequiredEditPaths))
})

var _ = ginkgo.It("structured required edit paths ignores negative allowlist keys", func() {
	prompt := `{
  "not_allowed_files": ["native/src/secret.cpp"],
  "candidate_scope": [{"path": "native/src/simulation/transport/route_graph.cpp"}],
  "evidence": [{"source": "native/src/simulation/transport/route_graph.h"}]
}`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("negative allowlist-like keys should not define required edit paths, got %#v", got))
	}
})

var _ = ginkgo.It("structured allowed edit paths captures yaml style write scope", func() {
	prompt := `
Write Scope:
- native/tests/test_transport_system.cpp

Evidence:
- native/src/simulation/transport/route_graph.cpp
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("write_scope is candidate scope, not required edit coverage, got %#v", got))

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	gomega.Expect(len(allowed)).To(gomega.Equal(1), fmt.Sprintf("expected YAML-style write_scope candidate path only, got %#v", allowed))
	gomega.Expect(allowed[0]).To(gomega.Equal("native/tests/test_transport_system.cpp"), fmt.Sprintf("expected YAML-style write_scope candidate path only, got %#v", allowed))
})

var _ = ginkgo.It("editable files heading is candidate scope only", func() {
	prompt := `
Editable Files:
- native/tests/test_transport_system.cpp
- native/src/simulation/transport/route_graph.cpp
`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("editable files must not define required edit coverage, got %#v", got))
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"native/tests/test_transport_system.cpp", "native/src/simulation/transport/route_graph.cpp"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("editable files missing allowed path %q, got %#v", want, allowed))
	}
})

var _ = ginkgo.It("structured required edit paths inline scalar does not bleed", func() {
	prompt := `
write_scope: native/tests/test_transport_system.cpp
evidence: native/src/simulation/transport/route_graph.cpp
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("write_scope is candidate scope, not required edit coverage, got %#v", got))

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	gomega.Expect(len(allowed)).To(gomega.Equal(1), fmt.Sprintf("expected inline write_scope candidate path only, got %#v", allowed))
	gomega.Expect(allowed[0]).To(gomega.Equal("native/tests/test_transport_system.cpp"), fmt.Sprintf("expected inline write_scope candidate path only, got %#v", allowed))
})

var _ = ginkgo.It("required edit paths ignore allowed edit paths", func() {
	prompt := `
## Allowed Edit Paths

- native/tests/test_transport_system.cpp
- native/src/simulation/transport/route_graph.cpp

## Evidence

- docs/design.md
`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("allowed edit paths must not define required coverage, got %#v", got))
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{
		"native/tests/test_transport_system.cpp",
		"native/src/simulation/transport/route_graph.cpp",
	} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed path %q, got %#v", want, allowed))
	}

	gomega.Expect(slices.Contains(allowed, "docs/design.md")).To(gomega.BeFalse(), fmt.Sprintf("evidence path must not bleed into allowed edit paths, got %#v", allowed))
})

var _ = ginkgo.It("required edit paths capture inline colon list", func() {
	prompt := "Implement the change. Required edit paths: cmd/idiom/product_workflow.go, cmd/idiom/product_workflow_test.go"
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got %#v", want, got))
	}
})

var _ = ginkgo.It("implementation atom prompt preserves required edit paths after design doc reference", func() {
	t := ginkgo.GinkgoT()
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

	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)
	want := []string{
		"internal/workflows/productworkflow/coverage_gate.go",
		"internal/workflows/productworkflow/coverage_gate_test.go",
		"cmd/idiom/product_workflow_test.go",
	}

	gomega.Expect(slices.Equal(plan.runtimeBrief.RequiredEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("required edit paths = %#v, want %#v", plan.runtimeBrief.RequiredEditPaths, want))

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "docs/design-tdd-coverage.md")).To(gomega.BeFalse(), fmt.Sprintf("design doc evidence must not become required edit scope: %#v", plan.runtimeBrief.RequiredEditPaths))

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope")).To(gomega.BeTrue(), fmt.Sprintf("expected required-path write scope constraint, got %#v", plan.runtimeBrief.Constraints))
})

var _ = ginkgo.It("required edit paths reject directory like obligations", func() {
	prompt := `
## Files To Modify

- cmd/idiom
- cmd/idiom/product_workflow.go
- Makefile
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "Makefile"}

	gomega.Expect(len(got)).To(gomega.Equal(len(want)), fmt.Sprintf("expected file-only required paths %#v, got %#v", want, got))

	for _, path := range want {
		gomega.Expect(slices.Contains(got, path)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got %#v", path, got))
	}

	gomega.Expect(slices.Contains(got, "cmd/idiom")).To(gomega.BeFalse(), fmt.Sprintf("directory-like target must not become an exact required edit path: %#v", got))
})

var _ = ginkgo.It("structured required edit paths ignore allowed keys", func() {
	prompt := `{
  "allowed_edit_paths": ["native/src/a.cpp"],
  "allowed_files": ["native/src/b.cpp"],
  "evidence": ["native/src/c.cpp"]
}`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("allowed structured keys must not define required coverage, got %#v", got))
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"native/src/a.cpp", "native/src/b.cpp"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed path %q, got %#v", want, allowed))
	}

	gomega.Expect(slices.Contains(allowed, "native/src/c.cpp")).To(gomega.BeFalse(), fmt.Sprintf("evidence path must not bleed into allowed edit paths, got %#v", allowed))
})

var _ = ginkgo.It("structured required edit paths single line json does not bleed", func() {
	prompt := `{"required_edit_paths":["native/src/a.cpp"],"evidence":[".idiom/config.json","native/src/b.cpp"]}`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	gomega.Expect(len(got)).To(gomega.Equal(1), fmt.Sprintf("expected single-line JSON required paths only, got %#v", got))
	gomega.Expect(got[0]).To(gomega.Equal("native/src/a.cpp"), fmt.Sprintf("expected single-line JSON required paths only, got %#v", got))
})

var _ = ginkgo.It("structured required edit paths ignore inline evidence command", func() {
	prompt := `
Freshness:
- idiom tool context_status --config .idiom/config.json --input '{"refresh":true,"required_edit_paths":["native/src/a.cpp"],"limit":20}'
`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("inline evidence commands must not define required edit paths, got %#v", got))
	}
})

var _ = ginkgo.It("required edit paths ignore structured keys inside evidence commands", func() {
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
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	for _, want := range []string{"native/src/a.cpp", "native/tests/test_a.cpp"} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("missing required edit path %q, got %#v", want, got))
	}

	gomega.Expect(slices.Contains(got, ".idiom/config.json")).To(gomega.BeFalse(), fmt.Sprintf("evidence command config path must not become required edit path, got %#v", got))

	gomega.Expect(len(got)).To(gomega.Equal(2), fmt.Sprintf("expected only Files To Modify paths, got %#v", got))
})

var _ = ginkgo.It("required edit paths captures allowlist table after descriptor line", func() {
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
	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	want := []string{
		"tests/e2e/suites/binding_surface_suite.gd",
		".planning/campaigns/testing-coverage-expansion.md",
		".planning/campaigns/testing-coverage-expansion-atom-6.3b-spec.md",
	}

	for _, path := range want {
		gomega.Expect(slices.Contains(got, path)).To(gomega.BeTrue(), fmt.Sprintf("missing required edit path %q, got %#v", path, got))
	}

	gomega.Expect(slices.Contains(got, "scripts/ui/shaft_panel.gd")).To(gomega.BeFalse(), fmt.Sprintf("following evidence table must not become required edit paths, got %#v", got))

	gomega.Expect(len(got)).To(gomega.Equal(len(want)), fmt.Sprintf("expected only allowlist paths %#v, got %#v", want, got))
})

var _ = ginkgo.It("required edit paths do not bleed from following evidence section", func() {
	prompt := `
Files To Modify:

Evidence:
- native/src/simulation/transport/route_graph.cpp
`
	{
		got := plancontract.RequiredEditPathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("empty edit section should not borrow evidence paths, got %#v", got))
	}
})

var _ = ginkgo.It("required edit paths captures trailing colon path line", func() {
	prompt := `
Files To Modify:
- internal/surfaces/tui/app/prompt_routing.go:
`
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	gomega.Expect(slices.Contains(got, "internal/surfaces/tui/app/prompt_routing.go")).To(gomega.BeTrue(), fmt.Sprintf("expected trailing-colon path to count as required edit path, got %#v", got))
})

var _ = ginkgo.It("context evidence does not bleed from empty packet heading", func() {
	prompt := `
Context Packet:

Required Behavior:
- internal/surfaces/tui/app/prompt_routing.go
`
	{
		got := plancontract.PromptContextEvidencePathsFromPrompt(prompt)
		gomega.Expect(len(got)).To(gomega.Equal(0), fmt.Sprintf("empty context packet should not borrow later behavior paths, got %#v", got))
	}
})

var _ = ginkgo.It("runtime brief captures host current state anchor evidence", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetPlanExecuteScope(plancontract.NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil), nil)

	prompt := `
# Atom

## Files To Modify

- pkg/a.go

## Verified Current State

- Host current-state anchors captured before dispatch:
  - ` + "`pkg/a.go:3`: `const A = \"old\"`" + `
`

	brief := newDetachedPromptExecutionSession(context.Background(), model).runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, modepolicy.BuildMode(), prompt)
	gomega.Expect(slices.Contains(brief.EvidencePaths, "pkg/a.go")).To(gomega.BeTrue(), fmt.Sprintf("expected host anchor path in EvidencePaths, got %#v", brief.EvidencePaths))

	gomega.Expect(slices.Contains(brief.TargetPacket, "prompt_context:verified_current_state")).To(gomega.BeTrue(), fmt.Sprintf("expected verified-current-state marker, got %#v", brief.TargetPacket))
})

var _ = ginkgo.It("runtime brief scope contract resolves tui dispatch path roles", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetPlanExecuteScope(plancontract.NewPlanScopeContract(
		[]string{"pkg/feature.go"},
		[]string{"pkg/feature_test.go"},
		nil,
	), nil)

	brief := newDetachedPromptExecutionSession(context.Background(), model).runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, modepolicy.BuildMode(), "Implement the feature")
	coverage, err := autonomy.ResolvePlanScopePathRoleObligations(autonomy.PlanScopePathRoleObligationInput{
		RequiredEditPaths:      brief.RequiredEditPaths,
		AllowedCandidatePaths:  brief.AllowedEditPaths,
		CandidateCoveragePaths: []string{"pkg/feature.go", "pkg/feature_test.go"},
		Source:                 "tui_dispatch_test",
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve path roles: %v", err))

	gomega.Expect(slices.Equal(coverage.RequiredCoveragePaths, []string{"pkg/feature.go"})).To(gomega.BeTrue(), fmt.Sprintf("required coverage paths = %#v", coverage.RequiredCoveragePaths))
	gomega.Expect(slices.Equal(coverage.AllowedCandidatePaths, []string{"pkg/feature_test.go"})).To(gomega.BeTrue(), fmt.Sprintf("allowed candidate paths = %#v", coverage.AllowedCandidatePaths))
	gomega.Expect(len(coverage.MissingRequiredCoveragePaths)).To(gomega.Equal(0), fmt.Sprintf("required path should be covered by required candidate, missing=%#v", coverage.MissingRequiredCoveragePaths))

	var allowedCandidate autonomy.PlanScopePathRoleCoverage
	for _, candidate := range coverage.CandidateCoverage {
		if candidate.Path == "pkg/feature_test.go" {
			allowedCandidate = candidate
			break
		}
	}
	gomega.Expect(allowedCandidate.EffectiveInvariantID).To(gomega.Equal(autonomy.InvariantAllowedCandidate), fmt.Sprintf("TUI allowed path must remain candidate scope only, got %#v", allowedCandidate))
	gomega.Expect(allowedCandidate.SatisfiesRequiredCoverage).To(gomega.BeFalse(), fmt.Sprintf("TUI allowed path must remain candidate scope only, got %#v", allowedCandidate))
	gomega.Expect(allowedCandidate.WithinAllowedCandidatePath).To(gomega.BeTrue(), fmt.Sprintf("TUI allowed path must remain candidate scope only, got %#v", allowedCandidate))
})

var _ = ginkgo.It("runtime brief plan execute scope includes typed authorized new files", func() {
	t := ginkgo.GinkgoT()
	model := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: t.TempDir()}},
		agentModeName: "build",
	}
	model.SetPlanExecuteScope(
		plancontract.NewPlanScopeContract([]string{"pkg/new_helper.go"}, nil, nil),
		[]string{"pkg/new_helper.go"},
	)

	brief := newDetachedPromptExecutionSession(context.Background(), model).runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, modepolicy.BuildMode(), "Create pkg/new_helper.go")

	gomega.Expect(slices.Equal(brief.AuthorizedNewFiles, []string{"pkg/new_helper.go"})).To(gomega.BeTrue(), fmt.Sprintf("authorized new files = %#v, want pkg/new_helper.go", brief.AuthorizedNewFiles))
})

var _ = ginkgo.It("plan execute runtime brief history requirement follows git metadata", func() {
	t := ginkgo.GinkgoT()
	nonGitRoot := t.TempDir()
	nonGit := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: nonGitRoot}},
		workspace:     workspace.NewManager(config.Workspace{Root: nonGitRoot, CWD: nonGitRoot}),
		agentModeName: "build",
	}
	prompt := "Use plan-execute to update pkg/a.go.\n\nrequired_edit_paths: pkg/a.go"
	brief := newDetachedPromptExecutionSession(context.Background(), nonGit).runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, modepolicy.BuildMode(), prompt)

	gomega.Expect(slices.Contains(brief.RequiredEvidence, string(workerruntime.RequiredEvidenceGitHistoryRationale))).To(gomega.BeFalse(), fmt.Sprintf("non-git workspace must not require git history evidence: %#v", brief.RequiredEvidence))

	gomega.Expect(slices.Contains(brief.RequiredTools, workspace.RequiredToolGitHistoryLookup)).To(gomega.BeFalse(), fmt.Sprintf("non-git workspace must not require git history tools: %#v", brief.RequiredTools))

	gitRoot := t.TempDir()
	{
		err := os.Mkdir(filepath.Join(gitRoot, ".git"), 0o755)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("mkdir .git: %v", err))
	}

	gitModel := &Model{
		cfg:           config.Config{Chat: config.Chat{ProjectRoot: gitRoot}},
		workspace:     workspace.NewManager(config.Workspace{Root: gitRoot, CWD: gitRoot}),
		agentModeName: "build",
	}
	gitBrief := newDetachedPromptExecutionSession(context.Background(), gitModel).runtimeBriefForRouteMode(turnRoutePlan{Family: agent.PlanThenExecutePlannerTaskFamily}, modepolicy.BuildMode(), prompt)

	gomega.Expect(slices.Contains(gitBrief.RequiredEvidence, string(workerruntime.RequiredEvidenceGitHistoryRationale))).To(gomega.BeTrue(), fmt.Sprintf("git workspace should retain git history evidence: %#v", gitBrief.RequiredEvidence))

	gomega.Expect(slices.Contains(gitBrief.RequiredTools, workspace.RequiredToolGitHistoryLookup)).To(gomega.BeTrue(), fmt.Sprintf("git workspace should retain git history tool requirement: %#v", gitBrief.RequiredTools))
})

var _ = ginkgo.It("prompt contains exact edit anchor recognizes structured json fields", func() {
	prompt := `Apply this exact edit:
{
  "old_text": "before();",
  "new_text": "after();"
}`
	gomega.Expect(plancontract.PromptContainsExactEditAnchor(prompt)).To(gomega.BeTrue(), fmt.Sprint("expected structured old_text/new_text fields to count as an exact edit anchor"))
})

var _ = ginkgo.It("build prompt execution plan captures required edit snippets", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	{
		got := len(plan.runtimeBrief.RequiredEditSnippets)
		gomega.Expect(got).To(gomega.Equal(2), fmt.Sprintf("expected two required edit snippets, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets))
	}

	for i, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		gomega.Expect(snippet.ID).NotTo(gomega.Equal(""), fmt.Sprintf("snippet %d missing id: %#v", i, snippet))

		gomega.Expect(snippet.Path).To(gomega.Equal("native/tests/test_production_graph.cpp"), fmt.Sprintf("snippet %d path = %q, want test file", i, snippet.Path))

		gomega.Expect(strings.Contains(snippet.Content, "old behavior")).To(gomega.BeFalse(), fmt.Sprintf("verified-current-state snippet should not be captured: %#v", snippet))
	}

	gomega.Expect(strings.Contains(plan.runtimeBrief.RequiredEditSnippets[0].Content, `#include "core/constants/logistics.h"`)).To(gomega.BeTrue(), fmt.Sprintf("missing include snippet: %#v", plan.runtimeBrief.RequiredEditSnippets))

	gomega.Expect(strings.Contains(plan.runtimeBrief.RequiredEditSnippets[1].Content, "demand pressure caps production")).To(gomega.BeTrue(), fmt.Sprintf("missing regression snippet: %#v", plan.runtimeBrief.RequiredEditSnippets))
})

var _ = ginkgo.It("build prompt execution plan assigns multi file required edit snippet paths", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	{
		got := len(plan.runtimeBrief.RequiredEditSnippets)
		gomega.Expect(got).To(gomega.Equal(2), fmt.Sprintf("expected two new snippets, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets))
	}

	got := map[string]string{}
	for _, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		got[snippet.Path] = snippet.Content
	}

	gomega.Expect(strings.Contains(got["native/src/a.cpp"], "int after();")).To(gomega.BeTrue(), fmt.Sprintf("source snippet path/content not captured correctly: %#v", plan.runtimeBrief.RequiredEditSnippets))

	gomega.Expect(strings.Contains(got["native/tests/test_a.cpp"], `TEST_CASE("after")`)).To(gomega.BeTrue(), fmt.Sprintf("test snippet path/content not captured correctly: %#v", plan.runtimeBrief.RequiredEditSnippets))
})

var _ = ginkgo.It("build prompt execution plan duplicates shared required edit snippet paths", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	got := map[string]string{}
	for _, snippet := range plan.runtimeBrief.RequiredEditSnippets {
		got[snippet.Path] = snippet.Content
	}

	for _, path := range []string{"native/src/a.h", "native/src/a.cpp"} {
		gomega.Expect(strings.Contains(got[path], "// new shared comment")).To(gomega.BeTrue(), fmt.Sprintf("shared snippet was not duplicated for %s: %#v", path, plan.runtimeBrief.RequiredEditSnippets))
	}
})

var _ = ginkgo.It("build prompt execution plan captures implementation shape snippets", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	{
		got := len(plan.runtimeBrief.RequiredEditSnippets)
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("expected one snippet from Implementation Shape heading, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets))
	}

	gomega.Expect(strings.Contains(plan.runtimeBrief.RequiredEditSnippets[0].Content, "int helper(int x)")).To(gomega.BeTrue(), fmt.Sprintf("snippet content missing helper body: %#v", plan.runtimeBrief.RequiredEditSnippets[0]))
})

var _ = ginkgo.It("build prompt execution plan captures files you may edit as candidate scope", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	for _, want := range []string{
		"native/src/simulation/world/megaflora_stamper.h",
		"native/src/simulation/world/megaflora_stamper.cpp",
		"scripts/world/megaflora_stamper.gd",
	} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, want)).To(gomega.BeFalse(), fmt.Sprintf("files-you-may-edit path %q must not become required coverage, got %#v", want, plan.runtimeBrief.RequiredEditPaths))

		gomega.Expect(slices.Contains(plan.runtimeBrief.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected candidate target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs))

		gomega.Expect(slices.Contains(plan.runtimeBrief.AllowedEditPaths, want)).To(gomega.BeTrue(), fmt.Sprintf("expected allowed edit scope %q, got %#v", want, plan.runtimeBrief.AllowedEditPaths))
	}
})

var _ = ginkgo.It("allowed edit paths from prompt keeps inline scope sentence bounded", func() {
	prompt := "Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go. Use pkg/evidence.go as read-only evidence."

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing inline allowed path %q, got %#v", want, allowed))
	}

	gomega.Expect(slices.Contains(allowed, "pkg/evidence.go")).To(gomega.BeFalse(), fmt.Sprintf("read-only evidence sentence must not become allowed edit scope, got %#v", allowed))
})

var _ = ginkgo.It("build prompt execution plan promotes only payload path from candidate scope", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/src/a.cpp")).To(gomega.BeTrue(), fmt.Sprintf("payload path should become required edit coverage, got %#v", plan.runtimeBrief.RequiredEditPaths))

	gomega.Expect(slices.Contains(plan.runtimeBrief.RequiredEditPaths, "native/src/b.cpp")).To(gomega.BeFalse(), fmt.Sprintf("candidate-only path must not become required edit coverage, got %#v", plan.runtimeBrief.RequiredEditPaths))

	for _, want := range []string{"native/src/a.cpp", "native/src/b.cpp"} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected candidate target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs))
	}

	{
		got := len(plan.runtimeBrief.RequiredEditSnippets)
		gomega.Expect(got).To(gomega.Equal(1), fmt.Sprintf("expected one payload snippet, got %d: %#v", got, plan.runtimeBrief.RequiredEditSnippets))
	}

	gomega.Expect(plan.runtimeBrief.RequiredEditSnippets[0].Path).To(gomega.Equal("native/src/a.cpp"), fmt.Sprintf("snippet should bind to explicit payload path, got %#v", plan.runtimeBrief.RequiredEditSnippets[0]))
})

var _ = ginkgo.It("review repair prompt does not promote edit scope to required paths", func() {
	t := ginkgo.GinkgoT()
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
	plan := newDetachedPromptExecutionSession(context.Background(), model).buildPromptExecutionPlan(context.Background(), prompt)

	gomega.Expect(len(plan.runtimeBrief.RequiredEditPaths)).To(gomega.Equal(0), fmt.Sprintf("review-repair allowed/original edit scope must not become required coverage, got %#v", plan.runtimeBrief.RequiredEditPaths))

	gomega.Expect(slices.Contains(plan.runtimeBrief.Constraints, "required_edit_paths_define_write_scope")).To(gomega.BeFalse(), fmt.Sprintf("review-repair must not get mutation required-path constraints, got %#v", plan.runtimeBrief.Constraints))

	for _, want := range []string{"widget_config.json", "widget_notes.md"} {
		gomega.Expect(slices.Contains(plan.runtimeBrief.TargetRefs, want)).To(gomega.BeTrue(), fmt.Sprintf("expected review target ref %q, got %#v", want, plan.runtimeBrief.TargetRefs))
	}
})

var _ = ginkgo.It("new honors attach instruction files config", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Chat.AttachInstructionFiles = false

	model := New(cfg)

	gomega.Expect(model.instructionAttacher).To(gomega.BeNil(), fmt.Sprint("expected dynamic instruction attachment to be disabled"))

	cfg.Chat.AttachInstructionFiles = true
	model = New(cfg)

	gomega.Expect(model.instructionAttacher).NotTo(gomega.BeNil(), fmt.Sprint("expected dynamic instruction attachment to be enabled"))
})

var _ = ginkgo.It("new uses project prompt candidates for dynamic instruction attachment", func() {
	t := ginkgo.GinkgoT()
	candidates, explicit := instructionAttachmentCandidates(config.Chat{ProjectPromptCandidates: []string{"AGENTS.md"}})
	gomega.Expect(explicit).To(gomega.BeTrue(), fmt.Sprintf("expected explicit AGENTS.md candidate only, got explicit=%t candidates=%#v", explicit, candidates))
	gomega.Expect(slices.Equal(candidates, []string{"AGENTS.md"})).To(gomega.BeTrue(), fmt.Sprintf("expected explicit AGENTS.md candidate only, got explicit=%t candidates=%#v", explicit, candidates))

	candidates, explicit = instructionAttachmentCandidates(config.Chat{ProjectPromptCandidates: []string{"AGENTS.md", "CLAUDE.md"}})
	gomega.Expect(explicit).To(gomega.BeTrue(), fmt.Sprintf("expected explicit candidates to preserve configured list, got explicit=%t candidates=%#v", explicit, candidates))
	gomega.Expect(slices.Equal(candidates, []string{"AGENTS.md", "CLAUDE.md"})).To(gomega.BeTrue(), fmt.Sprintf("expected explicit candidates to preserve configured list, got explicit=%t candidates=%#v", explicit, candidates))

	cfg := config.Default()

	cfg.Workspace.Root = t.TempDir()
	cfg.Workspace.CWD = cfg.Workspace.Root
	cfg.Chat.AttachInstructionFiles = true
	cfg.Chat.ProjectPromptCandidates = []string{}

	model := New(cfg)

	gomega.Expect(model.instructionAttacher).To(gomega.BeNil(), fmt.Sprint("expected empty project_prompt_candidates to suppress dynamic instruction attachment"))

	cfg.Chat.ProjectPromptCandidates = []string{"AGENTS.md"}
	model = New(cfg)

	gomega.Expect(model.instructionAttacher).NotTo(gomega.BeNil(), fmt.Sprint("expected explicit AGENTS.md candidate to enable dynamic instruction attachment"))
})

var _ = ginkgo.It("reload prompt stack generates project artifacts", func() {
	t := ginkgo.GinkgoT()
	project := t.TempDir()
	{
		err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# Demo\n\nAlways verify focused changes.\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write README: %v", err))
	}

	{
		err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("Prefer semantic tools.\n"), 0o644)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("write AGENTS: %v", err))
	}

	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{ProjectRoot: project},
		},
	}

	{
		err := model.reloadPromptStackWithProfiles(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("reloadPromptStackWithProfiles: %v", err))
	}

	contextPath := filepath.Join(project, ".idiom", "discovery", "context.md")
	{
		_, err := os.Stat(contextPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("expected generated context at %s: %v", contextPath, err))
	}

	gomega.Expect(model.promptStack.Text).To(gomega.Not(gomega.ContainSubstring("[project behavior: .idiom/discovery/context.md]")), fmt.Sprintf("generated context should not be auto-loaded as prompt behavior, got:\n%s", model.promptStack.Text))
	gomega.Expect(model.promptStack.Text).To(gomega.Not(gomega.ContainSubstring("Generated Project Context")), fmt.Sprintf("generated context should not be auto-loaded as prompt behavior, got:\n%s", model.promptStack.Text))
})

var _ = ginkgo.It("reload prompt stack rejects missing explicit prompt by default", func() {
	t := ginkgo.GinkgoT()
	missing := filepath.Join(t.TempDir(), "missing.md")
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{SystemPromptFiles: []string{missing}},
		},
	}

	{
		err := model.reloadPromptStackWithProfiles(nil)
		gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("reloadPromptStackWithProfiles should reject missing explicit prompts by default"))
	}
})

var _ = ginkgo.It("reload prompt stack can degrade missing explicit prompt when configured", func() {
	t := ginkgo.GinkgoT()
	missing := filepath.Join(t.TempDir(), "missing.md")
	model := &Model{
		cfg: config.Config{
			Chat: config.Chat{
				SystemPromptFiles:   []string{missing},
				AllowMissingPrompts: true,
			},
		},
	}

	{
		err := model.reloadPromptStackWithProfiles(nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("reloadPromptStackWithProfiles should degrade missing prompts when configured: %v", err))
	}

	gomega.Expect(strings.Contains(model.contextError, "prompt context degraded")).To(gomega.BeTrue(), fmt.Sprintf("expected context warning, got %q", model.contextError))

	gomega.Expect(len(model.promptStack.Warnings)).NotTo(gomega.Equal(0), fmt.Sprintf("expected prompt stack warnings"))
})
