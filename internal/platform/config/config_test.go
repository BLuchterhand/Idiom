package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/runprofile"
)

var _ = ginkgo.Describe("Configuration", func() {
	ginkgo.It("uses the native shell by default and preserves explicit shell settings", func() {
		want := "/bin/sh"
		if runtime.GOOS == "windows" {
			want = "powershell.exe"
		}
		gomega.Expect(Default().Workspace.Shell).To(gomega.Equal(want))
		for _, body := range []string{`{}`, `{"workspace":{"shell":""}}`, `{"workspace":{"shell":"  "}}`} {
			gomega.Expect(loadTestConfig(body).Workspace.Shell).To(gomega.Equal(want))
		}
		gomega.Expect(loadTestConfig(`{"workspace":{"shell":"/bin/zsh"}}`).Workspace.Shell).To(gomega.Equal("/bin/zsh"))
		gomega.Expect(loadTestConfig(`{"workspace":{"shell":"pwsh"}}`).Workspace.Shell).To(gomega.Equal("pwsh"))
	})
	ginkgo.It("applies defaults and resolves workspace-relative paths", func() {
		dir := ginkgo.GinkgoT().TempDir()
		projectRoot := filepath.Join(dir, "project")
		gomega.Expect(os.MkdirAll(projectRoot, 0o755)).To(gomega.Succeed())
		cfg := loadTestConfigAt(dir, `{
  "llama_server": {"binary":"llama-server","host":"127.0.0.1","port":8080,"model":"model.gguf"},
  "chat": {"project_root":"project","project_prompt_files":["AGENTS.md"],"preset":"custom","max_tokens":16},
  "workspace": {"root":"project","cwd":".","enable_shell":true,"enable_writes":true}
}`)

		canonicalProjectRoot, err := filepath.EvalSymlinks(projectRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cfg.Workspace.Root).To(gomega.Equal(canonicalProjectRoot))
		gomega.Expect(cfg.Workspace.CWD).To(gomega.Equal(canonicalProjectRoot))
		gomega.Expect(cfg.Chat.ProjectPromptFiles).To(gomega.HaveExactElements(filepath.Join(canonicalProjectRoot, "AGENTS.md")))
		gomega.Expect(cfg.Chat.DebugLogMode).To(gomega.Equal(runprofile.DebugLogModeRedacted))
		gomega.Expect(cfg.Chat.MaxToolSteps).To(gomega.BeZero())
		gomega.Expect(cfg.Chat.MaxOperationalModelCalls).To(gomega.BeZero())
		gomega.Expect(cfg.Chat.AttachInstructionFiles).To(gomega.BeFalse())
	})

	ginkgo.It("loads the consolidated backend profile document without duplicating its schema", func() {
		cfg := loadTestConfig(`{
  "llama_server": {"kind":"mlx","binary":"mlx_lm.server","model":"Qwen/Qwen3.8-27B"},
  "backends": {"default_profile":"qwen","profiles":{"qwen":{"models":[{"alias":"qwen","kind":"mlx","model_path":"Qwen/Qwen3.8-27B","roles":["planner"]}]}}},
  "chat": {"preset":"custom","max_tokens":16},
  "workspace": {"root":".","cwd":"."}
}`)
		gomega.Expect(cfg.Backends).NotTo(gomega.BeEmpty())

		var document map[string]any
		gomega.Expect(json.Unmarshal(cfg.Backends, &document)).To(gomega.Succeed())
		gomega.Expect(document).To(gomega.HaveKeyWithValue("default_profile", "qwen"))
	})

	ginkgo.It("makes the global fallback policy explicit", func() {
		home := ginkgo.GinkgoT().TempDir()
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{})).To(gomega.BeTrue())
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{DisableGlobalConfigFallback: true})).To(gomega.BeFalse())
		ginkgo.GinkgoT().Setenv("IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK", "1")
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{})).To(gomega.BeFalse())
	})

	ginkgo.It("rejects process-home fallback during tests unless explicitly authorized", func() {
		home := ginkgo.GinkgoT().TempDir()
		restoreProcessHomeFallback(filepath.Clean(home))
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{})).To(gomega.BeFalse())
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{AllowProcessHomeGlobalConfigFallback: true})).To(gomega.BeTrue())
		ginkgo.GinkgoT().Setenv("IDIOM_ALLOW_PROCESS_HOME_GLOBAL_CONFIG_FALLBACK", "1")
		gomega.Expect(shouldUseGlobalConfigFallback(home, LoadOptions{})).To(gomega.BeTrue())
	})

	ginkgo.It("does not load process-home global configuration during tests", func() {
		home := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(home, ".idiom", "config.json"), `{"app_name":"process-home-fixture"}`)
		restoreProcessHomeFallback(filepath.Clean(home))
		ginkgo.GinkgoT().Setenv("HOME", home)

		cfg, err := Load(filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-config.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cfg.AppName).NotTo(gomega.Equal("process-home-fixture"))
	})

	ginkgo.It("allows a synthetic HOME fallback during tests", func() {
		restoreProcessHomeFallback(filepath.Clean(ginkgo.GinkgoT().TempDir()))
		home := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(home, ".idiom", "config.json"), `{"app_name":"synthetic-home-fixture"}`)
		ginkgo.GinkgoT().Setenv("HOME", home)

		cfg, err := Load(filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-config.json"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cfg.AppName).To(gomega.Equal("synthetic-home-fixture"))
	})

	ginkgo.It("can disable global fallback through LoadOptions", func() {
		home := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(home, ".idiom", "config.json"), `{"app_name":"global-fixture"}`)
		ginkgo.GinkgoT().Setenv("HOME", home)

		cfg, err := LoadWithOptions(filepath.Join(ginkgo.GinkgoT().TempDir(), "missing-config.json"), LoadOptions{DisableGlobalConfigFallback: true})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(cfg.AppName).NotTo(gomega.Equal("global-fixture"))
	})

	ginkgo.DescribeTable("loading instruction attachment policy",
		func(value bool) {
			cfg := loadTestConfig(`{"chat":{"attach_instruction_files":` + boolJSON(value) + `},"workspace":{"root":".","cwd":"."}}`)
			gomega.Expect(cfg.Chat.AttachInstructionFiles).To(gomega.Equal(value))
		},
		ginkgo.Entry("disabled", false),
		ginkgo.Entry("enabled", true),
	)

	ginkgo.It("loads external-tool discovery contract flags", func() {
		cfg := loadTestConfig(`{
  "workspace": {"root":".","cwd":".","external_tools":[{
    "name":"trace_code","description":"Trace source relationships","command":"true","read_only":true,"discovery":true
  }]}
}`)
		gomega.Expect(cfg.Workspace.ExternalTools).To(gomega.HaveLen(1))
		gomega.Expect(cfg.Workspace.ExternalTools[0].ReadOnly).To(gomega.BeTrue())
		gomega.Expect(cfg.Workspace.ExternalTools[0].Discovery).To(gomega.BeTrue())
	})

	ginkgo.It("rejects unknown configuration keys", func() {
		path, _ := writeTestConfig(`{"chat":{"preset":"custom","made_up":true},"workspace":{"root":".","cwd":"."}}`)
		_, err := Load(path)
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("chat.made_up")))
	})

	ginkgo.It("accepts modeled aliases and comment keys", func() {
		cfg := loadTestConfig(`{
  "_comment":"allowed JSON comment field",
  "chat":{"preset":"custom","max_tokens":16},
  "route_classifier":{"enabled":true,"always_on":true},
  "_mcp":[{"name":"disabled","url":"https://example.invalid"}],
  "workspace":{"root":".","cwd":"."}
}`)
		gomega.Expect(cfg.RouteClassifier.AlwaysOn).To(gomega.BeTrue())
	})

	ginkgo.It("canonicalizes an existing path through symlinks", func() {
		dir := ginkgo.GinkgoT().TempDir()
		projectRoot := filepath.Join(dir, "project")
		gomega.Expect(os.MkdirAll(projectRoot, 0o755)).To(gomega.Succeed())
		linkRoot := filepath.Join(dir, "project-link")
		if err := os.Symlink(projectRoot, linkRoot); err != nil {
			ginkgo.Skip("symlinks unavailable: " + err.Error())
		}
		want, err := filepath.EvalSymlinks(projectRoot)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(CanonicalizeExistingPath(linkRoot)).To(gomega.Equal(want))
	})

	ginkgo.DescribeTable("rejecting invalid configuration",
		func(mutate func(*Config), want string) {
			cfg := Default()
			mutate(&cfg)
			gomega.Expect(cfg.Validate()).To(gomega.MatchError(gomega.ContainSubstring(want)))
		},
		ginkgo.Entry("unknown preset", func(cfg *Config) { cfg.Chat.Preset = "not-a-preset" }, "not a known built-in preset"),
		ginkgo.Entry("cwd outside root", func(cfg *Config) {
			cfg.Workspace.Root = "/tmp/root"
			cfg.Workspace.CWD = "/tmp/elsewhere"
		}, "workspace.cwd must be inside workspace.root"),
		ginkgo.Entry("unknown model family", func(cfg *Config) { cfg.Chat.ModelFamily = "qwen-ish" }, "chat.model_family"),
		ginkgo.Entry("unknown implementation dispatch", func(cfg *Config) { cfg.Chat.ImplementationDispatch = "planner" }, "chat.implementation_dispatch"),
	)

	ginkgo.DescribeTable("recognizing plan-execute dispatch",
		func(dispatch ImplementationDispatch, want bool) {
			gomega.Expect(dispatch.IsPlanExecute()).To(gomega.Equal(want))
		},
		ginkgo.Entry("disabled", ImplementationDispatch(""), false),
		ginkgo.Entry("plan execute", ImplementationDispatchPlanExecute, true),
		ginkgo.Entry("surrounding whitespace", ImplementationDispatch(" plan_execute "), true),
		ginkgo.Entry("unknown", ImplementationDispatch("planner"), false),
	)

	ginkgo.It("clones the thinking pointer when applying built-in presets", func() {
		var chat Chat
		gomega.Expect(ApplyBuiltinChatPreset(&chat, "qwen3-direct")).To(gomega.Succeed())
		gomega.Expect(chat.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*chat.EnableThinking).To(gomega.BeFalse())
		original := chat.EnableThinking

		gomega.Expect(ApplyBuiltinChatPreset(&chat, "qwen3-coding")).To(gomega.Succeed())
		gomega.Expect(chat.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*chat.EnableThinking).To(gomega.BeTrue())
		gomega.Expect(chat.EnableThinking).NotTo(gomega.BeIdenticalTo(original))
	})

	ginkgo.It("loads an implementer server block with inherited defaults", func() {
		cfg := loadTestConfig(`{
  "llama_server":{"binary":"llama-server","host":"127.0.0.1","port":8080,"model":"planner.gguf"},
  "implementer":{"host":"127.0.0.1","port":8081,"model":"implementer.gguf","ctx_size":131072},
  "chat":{"preset":"qwen3-coding-preserve","max_tokens":16}
}`)
		gomega.Expect(cfg.Implementer).NotTo(gomega.BeNil())
		gomega.Expect(cfg.Implementer.Binary).To(gomega.Equal("llama-server"))
		gomega.Expect(cfg.Implementer.Reasoning).To(gomega.Equal("auto"))
		gomega.Expect(cfg.Implementer.ReasoningFormat).To(gomega.Equal("deepseek"))
		gomega.Expect(cfg.Implementer.LogFile).NotTo(gomega.BeEmpty())
		gomega.Expect(cfg.ImplementerBaseURL()).To(gomega.Equal("http://127.0.0.1:8081"))
	})

	ginkgo.It("rejects an implementer port collision", func() {
		cfg := Default()
		impl := cfg.LlamaServer
		impl.Model = "implementer.gguf"
		cfg.Implementer = &impl
		gomega.Expect(cfg.Validate()).To(gomega.MatchError(gomega.ContainSubstring("implementer.port must differ")))
	})

	ginkgo.It("omits an implementer by default", func() {
		cfg := Default()
		gomega.Expect(cfg.Implementer).To(gomega.BeNil())
		gomega.Expect(cfg.ImplementerBaseURL()).To(gomega.BeEmpty())
	})

	ginkgo.It("loads an MLX backend without path-resolving its model ID", func() {
		cfg := loadTestConfig(`{
  "llama_server":{"kind":"mlx","binary":"mlx_lm.server","host":"127.0.0.1","port":8080,"model":"Qwen/Qwen3.6-35B-A3B"},
  "chat":{"preset":"qwen3-coding-preserve","max_tokens":16}
}`)
		gomega.Expect(cfg.LlamaServer.Kind).To(gomega.Equal("mlx"))
		gomega.Expect(cfg.LlamaServer.Model).To(gomega.Equal("Qwen/Qwen3.6-35B-A3B"))
		gomega.Expect(cfg.LlamaServer.IsMLX()).To(gomega.BeTrue())
	})

	ginkgo.It("loads hosted backends without local launch fields", func() {
		cfg := loadTestConfig(`{
  "llama_server":{"kind":"codex","model":"gpt-5-codex","reasoning_effort":"high"},
  "implementer":{"kind":"zai","model":"glm-5.1","base_url":"https://api.z.ai/api/paas/v4","api_key_env":"ZAI_API_KEY"},
  "chat":{"preset":"custom","max_tokens":16}
}`)
		gomega.Expect(cfg.LlamaServer.IsHosted()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.IsOpenAIResponses()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.Model).To(gomega.Equal("gpt-5-codex"))
		gomega.Expect(cfg.LlamaServer.EndpointBaseURL()).To(gomega.Equal("https://api.openai.com"))
		gomega.Expect(cfg.LlamaServer.APIKeyEnvName()).To(gomega.Equal("OPENAI_API_KEY"))
		gomega.Expect(cfg.Implementer).NotTo(gomega.BeNil())
		gomega.Expect(cfg.Implementer.IsZAI()).To(gomega.BeTrue())
		gomega.Expect(cfg.Implementer.EndpointBaseURL()).To(gomega.Equal("https://api.z.ai/api/paas/v4"))
		gomega.Expect(cfg.Implementer.APIKeyEnvName()).To(gomega.Equal("ZAI_API_KEY"))
		gomega.Expect(cfg.ImplementerBaseURL()).To(gomega.Equal("https://api.z.ai/api/paas/v4"))
	})

	ginkgo.It("applies hosted GLM defaults", func() {
		cfg := loadTestConfig(`{"llama_server":{"kind":"glm","model":"glm-5.2","reasoning_effort":"max"},"chat":{"preset":"custom","max_tokens":16}}`)
		gomega.Expect(cfg.LlamaServer.IsHosted()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.IsGLM()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.Model).To(gomega.Equal("glm-5.2"))
		gomega.Expect(cfg.LlamaServer.EndpointBaseURL()).To(gomega.Equal("https://api.z.ai/api/coding/paas/v4"))
		gomega.Expect(cfg.LlamaServer.APIKeyEnvName()).To(gomega.Equal("ZAI_API_KEY"))
	})

	ginkgo.It("applies hosted Anthropic defaults", func() {
		cfg := loadTestConfig(`{"llama_server":{"kind":"anthropic","model":"claude-sonnet-4-6"},"chat":{"preset":"custom","max_tokens":16}}`)
		gomega.Expect(cfg.LlamaServer.IsHosted()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.IsAnthropic()).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.Model).To(gomega.Equal("claude-sonnet-4-6"))
		gomega.Expect(cfg.LlamaServer.EndpointBaseURL()).To(gomega.Equal("https://api.anthropic.com"))
		gomega.Expect(cfg.LlamaServer.APIKeyEnvName()).To(gomega.Equal("ANTHROPIC_API_KEY"))
	})

	ginkgo.It("rejects an unknown backend kind", func() {
		cfg := Default()
		cfg.LlamaServer.Kind = "nope"
		gomega.Expect(cfg.Validate()).To(gomega.MatchError(gomega.ContainSubstring("llama_server.kind must be one of")))
	})

	ginkgo.DescribeTable("validating MCP servers",
		func(server MCPServer, wantErr string) {
			err := server.Validate()
			if wantErr == "" {
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				return
			}
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(wantErr)))
		},
		ginkgo.Entry("stdio", MCPServer{Name: "exa", Command: "npx", Args: []string{"-y", "x"}}, ""),
		ginkgo.Entry("http", MCPServer{Name: "exa", URL: "https://mcp.exa.ai/mcp"}, ""),
		ginkgo.Entry("http headers and timeout", MCPServer{Name: "exa", URL: "https://mcp.exa.ai/mcp", Headers: map[string]string{"X-Api-Key": "x"}, TimeoutSeconds: 25}, ""),
		ginkgo.Entry("missing name", MCPServer{URL: "https://example.com/"}, "name is required"),
		ginkgo.Entry("reserved separator", MCPServer{Name: "team/docs", URL: "https://example.com/"}, "cannot contain '/'"),
		ginkgo.Entry("both transports", MCPServer{Name: "x", Command: "foo", URL: "https://example.com/"}, "both command and url"),
		ginkgo.Entry("neither transport", MCPServer{Name: "x"}, "either command (stdio) or url (http)"),
		ginkgo.Entry("negative timeout", MCPServer{Name: "x", URL: "https://example.com/", TimeoutSeconds: -1}, "timeout_seconds"),
		ginkgo.Entry("stdio headers", MCPServer{Name: "x", Command: "foo", Headers: map[string]string{"k": "v"}}, "headers only apply to http"),
	)

	ginkgo.It("propagates an MCP validation error with its index", func() {
		cfg := Default()
		cfg.Chat.SaveDir = ginkgo.GinkgoT().TempDir()
		cfg.MCP = []MCPServer{{Name: "broken"}}
		gomega.Expect(cfg.Validate()).To(gomega.MatchError(gomega.ContainSubstring("mcp[0]")))
	})

	ginkgo.It("rejects normalized duplicate MCP server names", func() {
		cfg := Default()
		cfg.Chat.SaveDir = ginkgo.GinkgoT().TempDir()
		cfg.MCP = []MCPServer{{Name: "docs", Command: "first"}, {Name: " docs ", Command: "second"}}
		gomega.Expect(cfg.Validate()).To(gomega.MatchError(gomega.ContainSubstring(`server name "docs" duplicates mcp[0]`)))
	})

	ginkgo.It("classifies MCP transports", func() {
		stdio := MCPServer{Name: "x", Command: "foo"}
		http := MCPServer{Name: "y", URL: "https://example.com/"}
		gomega.Expect(stdio.IsStdio()).To(gomega.BeTrue())
		gomega.Expect(stdio.IsHTTP()).To(gomega.BeFalse())
		gomega.Expect(http.IsStdio()).To(gomega.BeFalse())
		gomega.Expect(http.IsHTTP()).To(gomega.BeTrue())
	})

	ginkgo.It("preserves Qwen thinking in the default configuration", func() {
		cfg := Default()
		gomega.Expect(cfg.Chat.Preset).To(gomega.Equal("qwen3-coding-preserve"))
		gomega.Expect(cfg.Chat.EnableThinking).NotTo(gomega.BeNil())
		gomega.Expect(*cfg.Chat.EnableThinking).To(gomega.BeTrue())
		gomega.Expect(cfg.Chat.PreserveThinking).To(gomega.BeTrue())
		gomega.Expect(cfg.LlamaServer.CtxSize).To(gomega.Equal(262144))
	})
})

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func loadTestConfig(body string) Config {
	path, _ := writeTestConfig(body)
	cfg, err := Load(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return cfg
}

func loadTestConfigAt(dir string, body string) Config {
	path := filepath.Join(dir, "config.json")
	writeTestFile(path, body)
	cfg, err := Load(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return cfg
}

func writeTestConfig(body string) (string, string) {
	dir := ginkgo.GinkgoT().TempDir()
	path := filepath.Join(dir, "config.json")
	writeTestFile(path, body)
	return path, dir
}

func writeTestFile(path string, body string) {
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(body), 0o644)).To(gomega.Succeed())
}

func restoreProcessHomeFallback(value string) {
	original := processHomeForGlobalConfigFallback
	processHomeForGlobalConfigFallback = value
	ginkgo.DeferCleanup(func() { processHomeForGlobalConfigFallback = original })
}
