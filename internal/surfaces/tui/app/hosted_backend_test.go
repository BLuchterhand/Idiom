package app

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

func TestBackendsModelToLlamaServerCarriesHostedFields(t *testing.T) {
	defaults := config.Default().LlamaServer
	got := backendsModelToLlamaServer(backends.Model{
		Alias:           "glm",
		Kind:            "zai",
		Model:           "glm-5.1",
		BaseURL:         "https://api.z.ai/api/paas/v4",
		APIKeyEnv:       "ZAI_API_KEY",
		ReasoningEffort: "high",
		Verbosity:       "low",
		Roles:           []string{backends.RolePlanner},
	}, defaults)

	if !got.IsHosted() || !got.IsZAI() {
		t.Fatalf("expected hosted ZAI spec, got %#v", got)
	}

	if got.Model != "glm-5.1" || got.EndpointBaseURL() != "https://api.z.ai/api/paas/v4" || got.APIKeyEnvName() != "ZAI_API_KEY" {
		t.Fatalf("hosted fields not carried: %#v", got)
	}

	if got.DisplayName != "glm" {
		t.Fatalf("expected display name to preserve alias, got %q", got.DisplayName)
	}

	if got.ReasoningEffort != "high" || got.Verbosity != "low" {
		t.Fatalf("provider options not carried: %#v", got)
	}
}

func TestBackendsModelToLlamaServerCarriesGLMDefaults(t *testing.T) {
	defaults := config.Default().LlamaServer
	got := backendsModelToLlamaServer(backends.Model{
		Alias:           "glm-5.2",
		Kind:            config.BackendGLM,
		Model:           "glm-5.2",
		ReasoningEffort: "max",
		Roles:           []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier},
	}, defaults)

	if !got.IsHosted() || !got.IsGLM() {
		t.Fatalf("expected hosted GLM spec, got %#v", got)
	}

	if got.Model != "glm-5.2" || got.EndpointBaseURL() != "https://api.z.ai/api/coding/paas/v4" || got.APIKeyEnvName() != "ZAI_API_KEY" {
		t.Fatalf("GLM defaults not applied: %#v", got)
	}

	if got.DisplayName != "glm-5.2" || got.ReasoningEffort != "max" {
		t.Fatalf("GLM alias/provider options not carried: %#v", got)
	}
}

func TestBackendsModelToLlamaServerDoesNotCarryLocalBaseURLIntoHostedRole(t *testing.T) {
	defaults := config.Default().RouteClassifier.LlamaServerSpec()

	defaults.Kind = config.BackendMLX
	defaults.BaseURL = "http://127.0.0.1:11437"
	defaults.Host = "127.0.0.1"
	defaults.Port = 11437
	defaults.Model = "/models/local-classifier"

	got := backendsModelToLlamaServer(backends.Model{
		Alias:     "deepseek-v4-pro",
		Kind:      config.BackendDeepSeek,
		Model:     "deepseek-chat",
		APIKeyEnv: "DEEPSEEK_API_KEY",
		Roles:     []string{backends.RolePlanner, backends.RoleClassifier},
	}, defaults)

	if !got.IsDeepSeek() {
		t.Fatalf("expected hosted DeepSeek classifier, got %#v", got)
	}

	if got.BaseURL != "" {
		t.Fatalf("hosted role without explicit base_url must not inherit local classifier base_url, got %q", got.BaseURL)
	}

	if got.EndpointBaseURL() != "https://api.deepseek.com" {
		t.Fatalf("expected DeepSeek default endpoint, got %q from %#v", got.EndpointBaseURL(), got)
	}
}

func TestBackendsModelToLlamaServerKeepsCustomBaseURLForSameHostedKind(t *testing.T) {
	defaults := config.Default().LlamaServer

	defaults.Kind = config.BackendDeepSeek
	defaults.BaseURL = "https://deepseek.internal/v1"
	defaults.APIKeyEnv = "DEEPSEEK_API_KEY"

	got := backendsModelToLlamaServer(backends.Model{
		Alias: "deepseek-v4-pro",
		Kind:  config.BackendDeepSeek,
		Model: "deepseek-chat",
		Roles: []string{backends.RolePlanner},
	}, defaults)

	if got.EndpointBaseURL() != "https://deepseek.internal/v1" {
		t.Fatalf("same hosted kind should inherit custom base_url, got %q", got.EndpointBaseURL())
	}
}

func TestRoleSpecResolvesExplicitHostedAliasAcrossProfiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	defaults := config.Default().LlamaServer

	defaults.Model = "glm-5.1"

	got, ok := roleSpec(root, backends.RolePlanner, defaults)

	if !ok {
		t.Fatal("expected explicit alias to resolve")
	}

	if !got.IsZAI() || got.Model != "glm-5.1" || got.EndpointBaseURL() != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("unexpected resolved spec: %#v", got)
	}
}

func TestNewReusesPlannerClientWhenClassifierRoleMatchesPlanner(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	cfg := config.Default()

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

	if model.summaryClient != model.client {
		t.Fatal("classifier role shares the planner target; expected summary client to reuse planner client")
	}

	if model.summaryController != nil {
		t.Fatal("classifier role shares the planner target; must not create a second server controller")
	}
}

func TestNewHostedClassifierRoleDoesNotUseStaleLocalClassifierBaseURL(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".idiom"), 0o755); err != nil {
		t.Fatalf("mkdir .idiom: %v", err)
	}

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
	if err := os.WriteFile(filepath.Join(root, ".idiom", "backends.json"), []byte(backendsJSON), 0o644); err != nil {
		t.Fatalf("write backends.json: %v", err)
	}

	cfg := config.Default()

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

	if model.plannerSpec.EndpointBaseURL() != "https://api.deepseek.com" {
		t.Fatalf("expected hosted planner endpoint, got %#v", model.plannerSpec)
	}

	if model.summaryClient != model.client {
		t.Fatal("classifier role shares the hosted planner target; expected summary client to reuse planner client")
	}

	if model.summaryController != nil {
		t.Fatal("hosted classifier role must not create a local summary controller")
	}
}
