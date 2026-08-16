package app

import (
	"fmt"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
	runtimesession "idiom/internal/runtime/session"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("backends model to llama server carries hosted fields", func() {
	defaults := config.Default().LlamaServer
	got := runtimesession.ServerSpecForBackendModel(backends.Model{
		Alias:           "glm",
		Kind:            "zai",
		Model:           "glm-5.1",
		BaseURL:         "https://api.z.ai/api/paas/v4",
		APIKeyEnv:       "ZAI_API_KEY",
		ReasoningEffort: "high",
		Verbosity:       "low",
		Roles:           []string{backends.RolePlanner},
	}, defaults)

	gomega.Expect(got.IsHosted()).To(gomega.BeTrue(), fmt.Sprintf("expected hosted ZAI spec, got %#v", got))
	gomega.Expect(got.IsZAI()).To(gomega.BeTrue(), fmt.Sprintf("expected hosted ZAI spec, got %#v", got))

	gomega.Expect(got.Model).To(gomega.Equal("glm-5.1"), fmt.Sprintf("hosted fields not carried: %#v", got))
	gomega.Expect(got.EndpointBaseURL()).To(gomega.Equal("https://api.z.ai/api/paas/v4"), fmt.Sprintf("hosted fields not carried: %#v", got))
	gomega.Expect(got.APIKeyEnvName()).To(gomega.Equal("ZAI_API_KEY"), fmt.Sprintf("hosted fields not carried: %#v", got))

	gomega.Expect(got.DisplayName).To(gomega.Equal("glm"), fmt.Sprintf("expected display name to preserve alias, got %q", got.DisplayName))

	gomega.Expect(got.ReasoningEffort).To(gomega.Equal("high"), fmt.Sprintf("provider options not carried: %#v", got))
	gomega.Expect(got.Verbosity).To(gomega.Equal("low"), fmt.Sprintf("provider options not carried: %#v", got))
})

var _ = ginkgo.It("backends model to llama server carries glm defaults", func() {
	defaults := config.Default().LlamaServer
	got := runtimesession.ServerSpecForBackendModel(backends.Model{
		Alias:           "glm-5.2",
		Kind:            config.BackendGLM,
		Model:           "glm-5.2",
		ReasoningEffort: "max",
		Roles:           []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier},
	}, defaults)

	gomega.Expect(got.IsHosted()).To(gomega.BeTrue(), fmt.Sprintf("expected hosted GLM spec, got %#v", got))
	gomega.Expect(got.IsGLM()).To(gomega.BeTrue(), fmt.Sprintf("expected hosted GLM spec, got %#v", got))

	gomega.Expect(got.Model).To(gomega.Equal("glm-5.2"), fmt.Sprintf("GLM defaults not applied: %#v", got))
	gomega.Expect(got.EndpointBaseURL()).To(gomega.Equal("https://api.z.ai/api/coding/paas/v4"), fmt.Sprintf("GLM defaults not applied: %#v", got))
	gomega.Expect(got.APIKeyEnvName()).To(gomega.Equal("ZAI_API_KEY"), fmt.Sprintf("GLM defaults not applied: %#v", got))

	gomega.Expect(got.DisplayName).To(gomega.Equal("glm-5.2"), fmt.Sprintf("GLM alias/provider options not carried: %#v", got))
	gomega.Expect(got.ReasoningEffort).To(gomega.Equal("max"), fmt.Sprintf("GLM alias/provider options not carried: %#v", got))
})

var _ = ginkgo.It("backends model to llama server does not carry local base url into hosted role", func() {
	defaults := config.Default().RouteClassifier.LlamaServerSpec()

	defaults.Kind = config.BackendMLX
	defaults.BaseURL = "http://127.0.0.1:11437"
	defaults.Host = "127.0.0.1"
	defaults.Port = 11437
	defaults.Model = "/models/local-classifier"

	got := runtimesession.ServerSpecForBackendModel(backends.Model{
		Alias:     "deepseek-v4-pro",
		Kind:      config.BackendDeepSeek,
		Model:     "deepseek-chat",
		APIKeyEnv: "DEEPSEEK_API_KEY",
		Roles:     []string{backends.RolePlanner, backends.RoleClassifier},
	}, defaults)

	gomega.Expect(got.IsDeepSeek()).To(gomega.BeTrue(), fmt.Sprintf("expected hosted DeepSeek classifier, got %#v", got))

	gomega.Expect(got.BaseURL).To(gomega.Equal(""), fmt.Sprintf("hosted role without explicit base_url must not inherit local classifier base_url, got %q", got.BaseURL))

	gomega.Expect(got.EndpointBaseURL()).To(gomega.Equal("https://api.deepseek.com"), fmt.Sprintf("expected DeepSeek default endpoint, got %q from %#v", got.EndpointBaseURL(), got))
})

var _ = ginkgo.It("backends model to llama server keeps custom base url for same hosted kind", func() {
	defaults := config.Default().LlamaServer

	defaults.Kind = config.BackendDeepSeek
	defaults.BaseURL = "https://deepseek.internal/v1"
	defaults.APIKeyEnv = "DEEPSEEK_API_KEY"

	got := runtimesession.ServerSpecForBackendModel(backends.Model{
		Alias: "deepseek-v4-pro",
		Kind:  config.BackendDeepSeek,
		Model: "deepseek-chat",
		Roles: []string{backends.RolePlanner},
	}, defaults)

	gomega.Expect(got.EndpointBaseURL()).To(gomega.Equal("https://deepseek.internal/v1"), fmt.Sprintf("same hosted kind should inherit custom base_url, got %q", got.EndpointBaseURL()))
})

var _ = ginkgo.It("role spec resolves explicit hosted alias across profiles", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	backendsJSON := `{
  "default_profile": "local",
  "profiles": {
    "local": {
      "models": [{"alias": "local-planner", "kind": "mlx", "model_path": "Qwen/Qwen", "roles": ["planner"]}]
    },
    "hosted": {
      "models": [{
        "alias": "glm-5.1",
        "kind": "zai",
        "model": "glm-5.1",
        "base_url": "https://api.z.ai/api/paas/v4",
        "api_key_env": "ZAI_API_KEY",
        "roles": ["planner", "executor"]
      }]
    }
  }
}`
	defaults := config.Default().LlamaServer

	defaults.Model = "glm-5.1"

	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.LlamaServer = defaults
	cfg.Backends = []byte(backendsJSON)
	got := runtimesession.ResolveBackendResources(cfg).PlannerSpec

	gomega.Expect(got.IsZAI()).To(gomega.BeTrue(), fmt.Sprintf("unexpected resolved spec: %#v", got))
	gomega.Expect(got.Model).To(gomega.Equal("glm-5.1"), fmt.Sprintf("unexpected resolved spec: %#v", got))
	gomega.Expect(got.EndpointBaseURL()).To(gomega.Equal("https://api.z.ai/api/paas/v4"), fmt.Sprintf("unexpected resolved spec: %#v", got))
})

var _ = ginkgo.It("new reuses planner client when classifier role matches planner", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	backendsJSON := `{
  "default_profile": "local",
  "profiles": {
    "local": {
      "models": [{
        "alias": "planner",
        "kind": "mlx",
        "binary": "/bin/false",
        "model_path": "/models/planner",
        "host": "127.0.0.1",
        "port": 47780,
        "roles": ["planner", "classifier"]
      }]
    }
  }
}`
	cfg := config.Default()

	cfg.Backends = []byte(backendsJSON)
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.RouteClassifier.Enabled = true
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.BaseURL = "http://127.0.0.1:11437"
	cfg.RouteClassifier.Binary = "/bin/false"
	cfg.RouteClassifier.Host = "127.0.0.1"
	cfg.RouteClassifier.Port = 11437
	cfg.RouteClassifier.ServerModel = "/models/classifier"

	model := New(cfg)
	defer model.Close()

	gomega.Expect(model.summaryClient).To(gomega.Equal(model.client), fmt.Sprint("classifier role shares the planner target; expected summary client to reuse planner client"))

	gomega.Expect(model.summaryController).To(gomega.BeNil(), fmt.Sprint("classifier role shares the planner target; must not create a second server controller"))
})

var _ = ginkgo.It("new hosted classifier role does not use stale local classifier base url", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	backendsJSON := `{
  "default_profile": "deepseek",
  "profiles": {
    "deepseek": {
      "models": [{
        "alias": "deepseek-v4-pro",
        "kind": "deepseek",
        "model": "deepseek-chat",
        "api_key_env": "DEEPSEEK_API_KEY",
        "roles": ["planner", "executor", "classifier"]
      }],
      "proxy": {
        "default_planner": "deepseek-v4-pro",
        "default_executor": "deepseek-v4-pro",
        "default_classifier": "deepseek-v4-pro"
      }
    }
  }
}`
	cfg := config.Default()

	cfg.Backends = []byte(backendsJSON)
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root
	cfg.RouteClassifier.Enabled = true
	cfg.RouteClassifier.AutoStartServer = true
	cfg.RouteClassifier.Kind = config.BackendMLX
	cfg.RouteClassifier.BaseURL = "http://127.0.0.1:11437"
	cfg.RouteClassifier.Host = "127.0.0.1"
	cfg.RouteClassifier.Port = 11437
	cfg.RouteClassifier.ServerModel = "/models/local-classifier"

	model := New(cfg)
	defer model.Close()

	gomega.Expect(model.plannerSpec.EndpointBaseURL()).To(gomega.Equal("https://api.deepseek.com"), fmt.Sprintf("expected hosted planner endpoint, got %#v", model.plannerSpec))

	gomega.Expect(model.summaryClient).To(gomega.Equal(model.client), fmt.Sprint("classifier role shares the hosted planner target; expected summary client to reuse planner client"))

	gomega.Expect(model.summaryController).To(gomega.BeNil(), fmt.Sprint("hosted classifier role must not create a local summary controller"))
})
