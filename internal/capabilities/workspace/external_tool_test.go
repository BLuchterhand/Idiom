package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"idiom/internal/capabilities/workspace/externaltool"
	"idiom/internal/platform/commandexec"
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

	reg, err := externaltool.NewRegistry([]externaltool.Tool{
		{Name: "ok_tool", Command: "./scripts/ok.sh"},
		{Name: "stale_tool", Command: filepath.Join(root, "does-not-exist.sh")},
	})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

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

func TestManagerExecutesExternalToolsFromRootWhenCWDEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "scripts", "ok.sh"), []byte("#!/bin/sh\necho external-ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "ok_tool", Command: "./scripts/ok.sh"}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "ok_tool"})
	if result.Error != "" {
		t.Fatalf("external tool failed: %#v", result)
	}
	if strings.TrimSpace(result.Output) != "external-ok" {
		t.Fatalf("external tool should run from project root when cwd is empty, got %#v", result.Output)
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

	reg, err := externaltool.NewRegistry([]externaltool.Tool{
		{Name: "lookup_tool", Command: "./scripts/lookup.sh", ReadOnly: true, Discovery: true},
		{Name: "mutating_tool", Command: "./scripts/mutate.sh", ReadOnly: false, Discovery: true},
		{Name: "stale_lookup", Command: "./scripts/missing.sh", ReadOnly: true, Discovery: true},
	})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

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

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "chatty_tool", Command: "./scripts/chatty.sh"}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 8})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

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

func TestWorkspaceCommandRunnerHonorsExplicitTimeoutOverride(t *testing.T) {
	exe := workspaceExternalHelperBinary(t)
	mgr := NewManager(config.Workspace{Root: t.TempDir(), CommandTimeoutSeconds: 5, MaxOutputBytes: 1024})

	result := mgr.runCommandResult(context.Background(), commandexec.Spec{
		Name:    exe,
		Args:    workspaceExternalHelperArgs("sleep"),
		Timeout: 20 * time.Millisecond,
	})

	if !result.TimedOut {
		t.Fatalf("runCommandResult should preserve explicit timeout, result=%#v", result)
	}
}

func TestWorkspaceCommandRunnerCapturesBeyondDisplayLimit(t *testing.T) {
	exe := workspaceExternalHelperBinary(t)
	mgr := NewManager(config.Workspace{Root: t.TempDir(), MaxOutputBytes: 8})

	result := mgr.runCommandResult(context.Background(), commandexec.Spec{
		Name: exe,
		Args: workspaceExternalHelperArgs("large-output"),
	})

	if result.Err != nil {
		t.Fatalf("runCommandResult failed: %v", result.Err)
	}
	if result.StdoutTruncated {
		t.Fatalf("workspace runner should keep diagnostic capture separate from display limit, result=%#v", result)
	}
	if len(strings.TrimSpace(result.Stdout)) <= 8 {
		t.Fatalf("raw capture should exceed display limit, got %q", result.Stdout)
	}

	display := mgr.commandDisplayOutput(result)
	if !display.Truncated || !strings.Contains(display.Text, "...[truncated]") {
		t.Fatalf("display projection should still truncate model-facing output, got %#v", display)
	}
}

func TestManagerEnforcesExternalToolDeclaredTimeout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}

	script := filepath.Join(root, "scripts", "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "slow_tool", Command: "./scripts/slow.sh", TimeoutSeconds: 1}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

	start := time.Now()
	result := mgr.executeExternal(context.Background(), ToolRequest{Tool: "slow_tool"})
	elapsed := time.Since(start)

	if result.Error == "" || !strings.Contains(result.Error, `timed out after 1s`) {
		t.Fatalf("external tool timeout error = %#v", result)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("external tool did not use declared timeout promptly; elapsed=%v", elapsed)
	}
}

func TestManagerExternalToolUsesCanonicalPWDOverlay(t *testing.T) {
	root := t.TempDir()
	exe := workspaceExternalHelperBinary(t)

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{
		Name:    "pwd_tool",
		Command: exe,
		Args:    workspaceExternalHelperArgs("print-pwd"),
	}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "pwd_tool"})
	if result.Error != "" {
		t.Fatalf("external tool failed: %#v", result)
	}
	if got, want := strings.TrimSpace(result.Output), config.CanonicalizeExistingPath(root); got != want {
		t.Fatalf("external tool PWD = %q, want canonical workspace cwd %q", got, want)
	}
}

func TestManagerExternalToolMergesStdoutAndStderr(t *testing.T) {
	root := t.TempDir()
	exe := workspaceExternalHelperBinary(t)

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{
		Name:    "mixed_tool",
		Command: exe,
		Args:    workspaceExternalHelperArgs("mixed-output"),
	}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

	result := mgr.Execute(context.Background(), ToolRequest{Tool: "mixed_tool"})
	if result.Error != "" {
		t.Fatalf("external tool failed: %#v", result)
	}
	if !strings.Contains(result.Output, "stdout text") || !strings.Contains(result.Output, "stderr text") {
		t.Fatalf("external tool output should include stdout and stderr, got %#v", result.Output)
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

	reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "stdin_tool", Command: "./scripts/stdin.py"}})
	if err != nil {
		t.Fatalf("externaltool.NewRegistry: %v", err)
	}

	mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
	mgr.toolSurface.SetExternalToolsRegistry(reg)

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

func workspaceExternalHelperBinary(t *testing.T) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	return exe
}

func workspaceExternalHelperArgs(action string) []string {
	return []string{"-test.run=TestWorkspaceExternalToolHelperProcess", "--", action}
}

func TestWorkspaceExternalToolHelperProcess(t *testing.T) {
	sep := -1
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return
	}

	args := os.Args[sep+1:]
	if len(args) == 0 {
		os.Exit(2)
	}

	switch args[0] {
	case "print-pwd":
		fmt.Fprint(os.Stdout, filepath.Clean(os.Getenv("PWD")))
		os.Exit(0)
	case "sleep":
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	case "large-output":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 64))
		os.Exit(0)
	case "mixed-output":
		fmt.Fprint(os.Stdout, "stdout text\n")
		fmt.Fprint(os.Stderr, "stderr text\n")
		os.Exit(0)
	}

	os.Exit(2)
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
