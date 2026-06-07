package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestManagerDoesNotAdvertiseUnavailableExternalTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "ok.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	reg, err := NewExternalToolRegistry([]ExternalTool{
		{Name: "ok_tool", Command: "./scripts/ok.sh"},
		{Name: "stale_tool", Command: filepath.Join(root, "does-not-exist.sh")},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}
	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	mgr.SetExternalToolsRegistry(reg)

	if !hasString(mgr.AllowedTools(), "ok_tool") {
		t.Fatalf("available external tool missing from allowed tools")
	}
	if hasString(mgr.AllowedTools(), "stale_tool") {
		t.Fatalf("unavailable external tool should not be advertised in allowed tools")
	}
	for _, def := range mgr.BuildToolDefinitions(nil) {
		if def.Name == "stale_tool" {
			t.Fatalf("unavailable external tool should not be advertised in tool definitions")
		}
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "stale_tool"})
	if !strings.Contains(result.Error, "unavailable") {
		t.Fatalf("direct stale tool invocation should return typed unavailable error, got %#v", result)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
