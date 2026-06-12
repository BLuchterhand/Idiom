package cmdroute

import (
	"testing"

	"idiom/internal/contracts/writerroute"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

func TestBuildDecisionEffectiveModelPrefersDirectConfigOverRoleAlias(t *testing.T) {
	cfg := config.Default()

	cfg.LlamaServer.Model = "deepseek-v4-flash"

	decision := BuildDecision(cfg, Options{
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

	decision := BuildDecision(cfg, Options{
		Mode:        writerroute.ModeReadOnly,
		Lane:        "main",
		RoleAliases: map[string]string{backends.RoleClassifier: "deepseek-v4-pro"},
	})

	if decision.EffectiveModel != "deepseek-v4-pro" {
		t.Fatalf("effective model = %q, want alias fallback", decision.EffectiveModel)
	}
}
