package app

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/backends"
	"idiom/internal/config"
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
	if got.ReasoningEffort != "high" || got.Verbosity != "low" {
		t.Fatalf("provider options not carried: %#v", got)
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
