package writerrouting

import (
	"testing"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

func TestBuildDecisionEffectiveModelPrefersDirectConfigOverRoleAlias(t *testing.T) {
	cfg := config.Default()

	cfg.LlamaServer.Model = "deepseek-v4-flash"

	decision := BuildDecision(cfg, DecisionOptions{
		Mode:        writerroute.ModeReadOnly,
		Lane:        "main",
		RoleAliases: map[string]string{backends.RoleClassifier: "deepseek-v4-pro"},
	})

	if decision.EffectiveModel != "deepseek-v4-flash" {
		t.Fatalf("effective model = %q, want direct configured model", decision.EffectiveModel)
	}

	if decision.RoleAliases[backends.RoleClassifier] != "deepseek-v4-pro" {
		t.Fatalf("role aliases should remain visible as routing context: %#v", decision.RoleAliases)
	}
}

func TestBuildDecisionEffectiveModelUsesAliasWhenNoDirectModelConfigured(t *testing.T) {
	cfg := config.Default()

	cfg.LlamaServer.Model = ""

	decision := BuildDecision(cfg, DecisionOptions{
		Mode:        writerroute.ModeReadOnly,
		Lane:        "main",
		RoleAliases: map[string]string{backends.RoleClassifier: "deepseek-v4-pro"},
	})

	if decision.EffectiveModel != "deepseek-v4-pro" {
		t.Fatalf("effective model = %q, want alias fallback", decision.EffectiveModel)
	}
}

func TestBuildDecisionUsesDisplayModelAndClassifierBaseFallback(t *testing.T) {
	cfg := config.Default()
	cfg.LlamaServer.Model = "/models/qwen/qwen3.gguf"
	cfg.LlamaServer.DisplayName = "qwen-display"

	decision := BuildDecision(cfg, DecisionOptions{
		Mode: writerroute.ModeReadOnly,
		Lane: "main",
	})

	if decision.PlannerModel != "qwen-display" || decision.ExecutorModel != "qwen-display" || decision.ClassifierModel != "qwen-display" {
		t.Fatalf("models = planner %q executor %q classifier %q, want display fallback", decision.PlannerModel, decision.ExecutorModel, decision.ClassifierModel)
	}
	if decision.EffectiveModel != "qwen-display" {
		t.Fatalf("effective model = %q, want display fallback", decision.EffectiveModel)
	}
}

func TestBuildDecisionKeepsExplicitCLIOverridesAuthoritative(t *testing.T) {
	cfg := config.Default()
	cfg.LlamaServer.Model = "base-model"
	cfg.Implementer = &config.LlamaServer{Model: "implementer-model"}
	cfg.RouteClassifier.Model = "classifier-config"

	decision := BuildDecision(cfg, DecisionOptions{
		Mode:            writerroute.ModeAgenticGuarded,
		Lane:            "implement",
		PlannerModel:    "planner-override",
		ExecutorModel:   "executor-override",
		ClassifierModel: "classifier-override",
	})

	if decision.PlannerModel != "planner-override" || decision.ExecutorModel != "executor-override" || decision.ClassifierModel != "classifier-override" {
		t.Fatalf("explicit overrides not preserved: %#v", decision)
	}
	if decision.EffectiveModel != "executor-override" {
		t.Fatalf("effective model = %q, want executor override", decision.EffectiveModel)
	}
}
