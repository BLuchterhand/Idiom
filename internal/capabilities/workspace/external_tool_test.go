package workspace

import (
	"context"
	"encoding/json"
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

	if !hasString(mgr.ToolSurfaceSnapshot().AllowedTools(), "ok_tool") {
		t.Fatalf("available external tool missing from allowed tools")
	}

	if hasString(mgr.ToolSurfaceSnapshot().AllowedTools(), "stale_tool") {
		t.Fatalf("unavailable external tool should not be advertised in allowed tools")
	}

	for _, def := range mgr.ToolSurfaceSnapshot().BuildToolDefinitions(nil) {
		if def.Name == "stale_tool" {
			t.Fatalf("unavailable external tool should not be advertised in tool definitions")
		}
	}

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "stale_tool"})
	if !strings.Contains(result.Error, "unavailable") {
		t.Fatalf("direct stale tool invocation should return typed unavailable error, got %#v", result)
	}
}

func TestToolSurfaceSnapshotCopiesExternalToolView(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	for _, name := range []string{"lookup.sh", "mutate.sh"} {
		if err := os.WriteFile(filepath.Join(root, "scripts", name), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatalf("write script %s: %v", name, err)
		}
	}

	reg, err := NewExternalToolRegistry([]ExternalTool{
		{Name: "lookup_tool", Command: "./scripts/lookup.sh", ReadOnly: true, Discovery: true},
		{Name: "mutating_tool", Command: "./scripts/mutate.sh", ReadOnly: false, Discovery: true},
		{Name: "stale_lookup", Command: "./scripts/missing.sh", ReadOnly: true, Discovery: true},
	})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	mgr.SetExternalToolsRegistry(reg)

	snapshot := mgr.ToolSurfaceSnapshot()

	if !hasString(snapshot.AllowedTools(), "lookup_tool") || !hasString(snapshot.AllowedTools(), "mutating_tool") {
		t.Fatalf("snapshot should advertise available external tools, got %#v", snapshot.AllowedTools())
	}
	if hasString(snapshot.AllowedTools(), "stale_lookup") {
		t.Fatalf("snapshot should not advertise unavailable external tools")
	}

	if !hasString(snapshot.AvailableExternalToolNames(), "lookup_tool") || hasString(snapshot.AvailableExternalToolNames(), "stale_lookup") {
		t.Fatalf("snapshot available external names = %#v", snapshot.AvailableExternalToolNames())
	}

	if !snapshot.HasExternalTool("lookup_tool") || !snapshot.HasExternalTool("stale_lookup") {
		t.Fatalf("snapshot should retain registered external tool membership")
	}
	if !snapshot.ExternalToolIsReadOnlyDiscovery("lookup_tool") {
		t.Fatalf("snapshot should classify read-only discovery external tools")
	}
	if snapshot.ExternalToolIsReadOnlyDiscovery("mutating_tool") {
		t.Fatalf("snapshot should not classify mutating external tools as read-only discovery")
	}

	allowed := snapshot.AllowedTools()
	if len(allowed) == 0 {
		t.Fatalf("snapshot returned no allowed tools")
	}
	allowed[0] = "mutated_allowed"
	if hasString(snapshot.AllowedTools(), "mutated_allowed") {
		t.Fatalf("AllowedTools should return a copy")
	}

	externalNames := snapshot.AvailableExternalToolNames()
	if len(externalNames) == 0 {
		t.Fatalf("snapshot returned no available external tool names")
	}
	externalNames[0] = "mutated_external"
	if hasString(snapshot.AvailableExternalToolNames(), "mutated_external") {
		t.Fatalf("AvailableExternalToolNames should return a copy")
	}

	defs := snapshot.BuildToolDefinitions(nil)
	if len(defs) == 0 {
		t.Fatalf("snapshot returned no tool definitions")
	}
	defs[0].Name = "mutated_definition"
	for _, def := range snapshot.BuildToolDefinitions(nil) {
		if def.Name == "mutated_definition" {
			t.Fatalf("BuildToolDefinitions should return a copy")
		}
	}
}

func TestManagerBoundsExternalToolOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	script := filepath.Join(root, "scripts", "chatty.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'abcdefghijklmnopqrstuvwxyz'\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	reg, err := NewExternalToolRegistry([]ExternalTool{{Name: "chatty_tool", Command: "./scripts/chatty.sh"}})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 8})
	mgr.SetExternalToolsRegistry(reg)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "chatty_tool"})
	if result.Error != "" {
		t.Fatalf("external tool failed: %#v", result)
	}
	if !strings.HasPrefix(result.Output, "abcdefgh") || !strings.Contains(result.Output, "...[truncated]") {
		t.Fatalf("external tool output was not bounded: %#v", result.Output)
	}
	if !strings.Contains(result.Summary, "output truncated") {
		t.Fatalf("external tool summary missing truncation signal: %#v", result.Summary)
	}
}

func TestManagerPassesExternalToolInputOnStdin(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	script := filepath.Join(root, "scripts", "stdin.py")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env python3\nimport sys\nprint(sys.stdin.read())\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	reg, err := NewExternalToolRegistry([]ExternalTool{{Name: "stdin_tool", Command: "./scripts/stdin.py"}})
	if err != nil {
		t.Fatalf("NewExternalToolRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
	mgr.SetExternalToolsRegistry(reg)

	result := mgr.Execute(context.Background(), ToolRequest{
		Tool: "stdin_tool",
		Args: json.RawMessage(`{"message":"hello"}`),
	})
	if result.Error != "" {
		t.Fatalf("external tool failed: %#v", result)
	}
	if !strings.Contains(result.Output, `"message":"hello"`) {
		t.Fatalf("external tool did not receive JSON input on stdin: %#v", result.Output)
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
