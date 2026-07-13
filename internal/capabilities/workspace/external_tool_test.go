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

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/capabilities/workspace/externaltool"
	"idiom/internal/platform/commandexec"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("external tool", func() {
	ginkgo.It("manager does not advertise unavailable external tools", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "scripts", "ok.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755)).To(gomega.Succeed())

		reg, err := externaltool.NewRegistry([]externaltool.Tool{
			{Name: "ok_tool", Command: "./scripts/ok.sh"},
			{Name: "stale_tool", Command: filepath.Join(root, "does-not-exist.sh")},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		mgr.toolSurface.SetExternalToolsRegistry(reg)
		gomega.Expect(hasString(mgr.ToolSurfaceSnapshot().AllowedTools(), "ok_tool")).To(gomega.BeTrue(), fmt.Sprintf("available external tool missing from allowed tools"))
		gomega.Expect(hasString(mgr.ToolSurfaceSnapshot().AllowedTools(), "stale_tool")).To(gomega.BeFalse(), fmt.Sprintf("unavailable external tool should not be advertised in allowed tools"))

		for _, def := range mgr.ToolSurfaceSnapshot().BuildToolDefinitions(nil) {
			gomega.Expect(def.Name).NotTo(gomega.Equal("stale_tool"), fmt.Sprintf("unavailable external tool should not be advertised in tool definitions"))

		}

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "stale_tool"})
		gomega.Expect(result.Error).To(gomega.ContainSubstring("unavailable"), fmt.Sprintf("direct stale tool invocation should return typed unavailable error, got %#v", result))

	})
	ginkgo.It("manager executes external tools from root when cwd empty", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(root, "scripts", "ok.sh"), []byte("#!/bin/sh\necho external-ok\n"), 0o755)).To(gomega.Succeed())

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "ok_tool", Command: "./scripts/ok.sh"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "ok_tool"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("external tool failed: %#v", result))
		gomega.Expect(strings.TrimSpace(result.Output)).To(gomega.Equal("external-ok"), fmt.Sprintf("external tool should run from project root when cwd is empty, got %#v", result.Output))

	})
	ginkgo.It("tool surface snapshot copies external tool view", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())

		for _, name := range []string{"lookup.sh", "mutate.sh"} {
			gomega.Expect(os.WriteFile(filepath.Join(root, "scripts", name), []byte("#!/bin/sh\necho ok\n"), 0o755)).To(gomega.Succeed())

		}

		reg, err := externaltool.NewRegistry([]externaltool.Tool{
			{Name: "lookup_tool", Command: "./scripts/lookup.sh", ReadOnly: true, Discovery: true},
			{Name: "mutating_tool", Command: "./scripts/mutate.sh", ReadOnly: false, Discovery: true},
			{Name: "stale_lookup", Command: "./scripts/missing.sh", ReadOnly: true, Discovery: true},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		snapshot := mgr.ToolSurfaceSnapshot()
		gomega.Expect(hasString(snapshot.AllowedTools(), "lookup_tool")).To(gomega.BeTrue(), fmt.Sprintf("snapshot should advertise available external tools, got %#v", snapshot.AllowedTools()))
		gomega.Expect(hasString(snapshot.AllowedTools(), "mutating_tool")).To(gomega.BeTrue(), fmt.Sprintf("snapshot should advertise available external tools, got %#v", snapshot.AllowedTools()))
		gomega.Expect(hasString(snapshot.AllowedTools(), "stale_lookup")).To(gomega.BeFalse(), fmt.Sprintf("snapshot should not advertise unavailable external tools"))
		gomega.Expect(hasString(snapshot.AvailableExternalToolNames(), "lookup_tool")).To(gomega.BeTrue(), fmt.Sprintf("snapshot available external names = %#v", snapshot.AvailableExternalToolNames()))
		gomega.Expect(hasString(snapshot.AvailableExternalToolNames(), "stale_lookup")).To(gomega.BeFalse(), fmt.Sprintf("snapshot available external names = %#v", snapshot.AvailableExternalToolNames()))
		gomega.Expect(snapshot.HasExternalTool("lookup_tool")).To(gomega.BeTrue(), fmt.Sprintf("snapshot should retain registered external tool membership"))
		gomega.Expect(snapshot.HasExternalTool("stale_lookup")).To(gomega.BeTrue(), fmt.Sprintf("snapshot should retain registered external tool membership"))
		gomega.Expect(snapshot.ExternalToolIsReadOnlyDiscovery("lookup_tool")).To(gomega.BeTrue(), fmt.Sprintf("snapshot should classify read-only discovery external tools"))
		gomega.Expect(snapshot.ExternalToolIsReadOnlyDiscovery("mutating_tool")).To(gomega.BeFalse(), fmt.Sprintf("snapshot should not classify mutating external tools as read-only discovery"))

		projection := snapshot.PermissionAuthorizedInspection([]string{ToolNameReadFile, ToolNameWriteFile, ToolNameShell, "lookup_tool", "mutating_tool"})
		gomega.Expect(projection.ToolNames()).To(gomega.ContainElements(ToolNameReadFile, ToolNameShell, "lookup_tool"))
		gomega.Expect(projection.ToolNames()).NotTo(gomega.ContainElements(ToolNameWriteFile, "mutating_tool"))
		gomega.Expect(projection.AllowsRequest(ToolRequest{Tool: ToolNameShell, Command: "pwd"})).To(gomega.BeTrue())
		gomega.Expect(projection.AllowsRequest(ToolRequest{Tool: ToolNameShell, Command: "touch changed.txt"})).To(gomega.BeFalse())
		gomega.Expect(projection.AllowsRequest(ToolRequest{Tool: "lookup_tool"})).To(gomega.BeTrue())

		allowed := snapshot.AllowedTools()
		gomega.Expect(allowed).NotTo(gomega.BeEmpty(), fmt.Sprintf("snapshot returned no allowed tools"))

		allowed[0] = "mutated_allowed"
		gomega.Expect(hasString(snapshot.AllowedTools(), "mutated_allowed")).To(gomega.BeFalse(), fmt.Sprintf("AllowedTools should return a copy"))

		externalNames := snapshot.AvailableExternalToolNames()
		gomega.Expect(externalNames).NotTo(gomega.BeEmpty(), fmt.Sprintf("snapshot returned no available external tool names"))

		externalNames[0] = "mutated_external"
		gomega.Expect(hasString(snapshot.AvailableExternalToolNames(), "mutated_external")).To(gomega.BeFalse(), fmt.Sprintf("AvailableExternalToolNames should return a copy"))

		defs := snapshot.BuildToolDefinitions(nil)
		gomega.Expect(defs).NotTo(gomega.BeEmpty(), fmt.Sprintf("snapshot returned no tool definitions"))

		defs[0].Name = "mutated_definition"
		for _, def := range snapshot.BuildToolDefinitions(nil) {
			gomega.Expect(def.Name).NotTo(gomega.Equal("mutated_definition"), fmt.Sprintf("BuildToolDefinitions should return a copy"))

		}
	})
	ginkgo.It("manager bounds external tool output", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())

		script := filepath.Join(root, "scripts", "chatty.sh")
		gomega.Expect(os.WriteFile(script, []byte("#!/bin/sh\nprintf 'abcdefghijklmnopqrstuvwxyz'\n"), 0o755)).To(gomega.Succeed())

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "chatty_tool", Command: "./scripts/chatty.sh"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 8})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "chatty_tool"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("external tool failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.HavePrefix("abcdefgh"), fmt.Sprintf("external tool output was not bounded: %#v", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("...[truncated]"), fmt.Sprintf("external tool output was not bounded: %#v", result.Output))
		gomega.Expect(result.Summary).To(gomega.ContainSubstring("output truncated"), fmt.Sprintf("external tool summary missing truncation signal: %#v", result.Summary))

	})
	ginkgo.It("workspace command runner honors explicit timeout override", func() {
		exe := workspaceExternalHelperBinary()
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir(), CommandTimeoutSeconds: 5, MaxOutputBytes: 1024})

		result := mgr.runCommandResult(context.Background(), commandexec.Spec{
			Name:    exe,
			Args:    workspaceExternalHelperArgs("sleep"),
			Timeout: 20 * time.Millisecond,
		})
		gomega.Expect(result.TimedOut).To(gomega.BeTrue(), fmt.Sprintf("runCommandResult should preserve explicit timeout, result=%#v", result))

	})
	ginkgo.It("workspace command runner captures beyond display limit", func() {
		exe := workspaceExternalHelperBinary()
		mgr := NewManager(config.Workspace{Root: ginkgo.GinkgoT().TempDir(), MaxOutputBytes: 8})

		result := mgr.runCommandResult(context.Background(), commandexec.Spec{
			Name: exe,
			Args: workspaceExternalHelperArgs("large-output"),
		})
		gomega.Expect(result.Err).To(gomega.BeNil(), fmt.Sprintf("runCommandResult failed: %v", result.Err))
		gomega.Expect(result.StdoutTruncated).To(gomega.BeFalse(), fmt.Sprintf("workspace runner should keep diagnostic capture separate from display limit, result=%#v", result))
		gomega.Expect(len(strings.TrimSpace(result.Stdout)) <= 8).To(gomega.BeFalse(), fmt.Sprintf("raw capture should exceed display limit, got %q", result.Stdout))

		display := mgr.commandDisplayOutput(result)
		gomega.Expect(display.Truncated).To(gomega.BeTrue(), fmt.Sprintf("display projection should still truncate model-facing output, got %#v", display))
		gomega.Expect(display.Text).To(gomega.ContainSubstring("...[truncated]"), fmt.Sprintf("display projection should still truncate model-facing output, got %#v", display))

	})
	ginkgo.It("manager enforces external tool declared timeout", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())

		script := filepath.Join(root, "scripts", "slow.sh")
		gomega.Expect(os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755)).To(gomega.Succeed())

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "slow_tool", Command: "./scripts/slow.sh", TimeoutSeconds: 1}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		start := time.Now()
		result := mgr.executeExternal(context.Background(), ToolRequest{Tool: "slow_tool"})
		elapsed := time.Since(start)
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), fmt.Sprintf("external tool timeout error = %#v", result))
		gomega.Expect(result.Error).To(gomega.ContainSubstring(`timed out after 1s`), fmt.Sprintf("external tool timeout error = %#v", result))
		gomega.Expect(elapsed > 3*time.Second).To(gomega.BeFalse(), fmt.Sprintf("external tool did not use declared timeout promptly; elapsed=%v", elapsed))

	})
	ginkgo.It("manager external tool uses canonical pwd overlay", func() {
		root := ginkgo.GinkgoT().TempDir()
		exe := workspaceExternalHelperBinary()

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{
			Name:    "pwd_tool",
			Command: exe,
			Args:    workspaceExternalHelperArgs("print-pwd"),
		}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "pwd_tool"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("external tool failed: %#v", result))

		if got, want := strings.TrimSpace(result.Output), config.CanonicalizeExistingPath(root); got != want {
			ginkgo.Fail(fmt.Sprintf("external tool PWD = %q, want canonical workspace cwd %q", got, want))
		}
	})
	ginkgo.It("manager external tool merges stdout and stderr", func() {
		root := ginkgo.GinkgoT().TempDir()
		exe := workspaceExternalHelperBinary()

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{
			Name:    "mixed_tool",
			Command: exe,
			Args:    workspaceExternalHelperArgs("mixed-output"),
		}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		result := mgr.Execute(context.Background(), ToolRequest{Tool: "mixed_tool"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("external tool failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("stdout text"), fmt.Sprintf("external tool output should include stdout and stderr, got %#v", result.Output))
		gomega.Expect(result.Output).To(gomega.ContainSubstring("stderr text"), fmt.Sprintf("external tool output should include stdout and stderr, got %#v", result.Output))

	})
	ginkgo.It("manager passes external tool input on stdin", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())

		script := filepath.Join(root, "scripts", "stdin.py")
		gomega.Expect(os.WriteFile(script, []byte("#!/usr/bin/env python3\nimport sys\nprint(sys.stdin.read())\n"), 0o755)).To(gomega.Succeed())

		reg, err := externaltool.NewRegistry([]externaltool.Tool{{Name: "stdin_tool", Command: "./scripts/stdin.py"}})
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("externaltool.NewRegistry: %v", err))

		mgr := NewManager(config.Workspace{Root: root, CWD: root, MaxOutputBytes: 1024})
		mgr.toolSurface.SetExternalToolsRegistry(reg)

		result := mgr.Execute(context.Background(), ToolRequest{
			Tool: "stdin_tool",
			Args: json.RawMessage(`{"message":"hello"}`),
		})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("external tool failed: %#v", result))
		gomega.Expect(result.Output).To(gomega.ContainSubstring(`"message":"hello"`), fmt.Sprintf("external tool did not receive JSON input on stdin: %#v", result.Output))

	})
})

func workspaceExternalHelperBinary() string {
	ginkgo.GinkgoHelper()

	exe, err := os.Executable()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("os.Executable: %v", err))

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
