package app

import (
	"testing"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/backends"
)

func TestWriterRouteModelForRolePrefersDirectConfigOverAlias(t *testing.T) {
	m := &Model{cfg: config.Config{LlamaServer: config.LlamaServer{Model: "deepseek-v4-flash"}}}
	aliases := map[string]string{backends.RoleClassifier: "deepseek-v4-pro"}

	if got := m.writerRouteModelForRole(aliases, backends.RoleClassifier); got != "deepseek-v4-flash" {
		t.Fatalf("classifier model = %q, want direct configured model", got)
	}
}

func TestWriterRouteModelForRoleUsesAliasWhenNoDirectModelConfigured(t *testing.T) {
	m := &Model{}
	aliases := map[string]string{backends.RoleClassifier: "deepseek-v4-pro"}

	if got := m.writerRouteModelForRole(aliases, backends.RoleClassifier); got != "deepseek-v4-pro" {
		t.Fatalf("classifier model = %q, want alias fallback", got)
	}
}
