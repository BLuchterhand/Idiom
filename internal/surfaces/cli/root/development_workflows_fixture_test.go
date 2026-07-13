package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runrecord"
)

func runDevelopmentE2EBinary(t rootTestContext, bin, workingDirectory string, environment map[string]string, args ...string) binaryE2EResult {
	t.Helper()

	overrides := map[string]string{
		"HOME":                                 t.TempDir(),
		"IDIOM_DISABLE_GLOBAL_CONFIG_FALLBACK": "1",
		"IDIOM_TELEMETRY":                      "0",
	}
	for key, value := range environment {
		overrides[key] = value
	}

	command := exec.Command(bin, args...)
	command.Dir = workingDirectory
	command.Env = rootTestEnvironmentWithOverrides(os.Environ(), overrides)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := binaryE2EResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.code = exitErr.ExitCode()
		return result
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return result
}

func writeDevelopmentE2EConfig(t rootTestContext, workspaceRoot, baseURL string, enableWrites, enableShell bool) string {
	t.Helper()

	cfg := config.Default()
	cfg.LlamaServer.Kind = config.BackendCodex
	cfg.LlamaServer.BaseURL = baseURL
	cfg.LlamaServer.APIKeyEnv = developmentWorkflowAPIKeyEnv
	cfg.LlamaServer.Model = "idiom-development-e2e"
	cfg.LlamaServer.CtxSize = 32768
	cfg.Chat.AutoStartServer = false
	cfg.Chat.SystemPrompt = "Use workspace tools for repository evidence and verification. Report completed work with path:line citations."
	cfg.Chat.SystemPromptFiles = nil
	cfg.Chat.DefaultProfiles = nil
	cfg.Chat.ProjectPromptCandidates = nil
	cfg.Chat.ProjectPromptFiles = nil
	cfg.Chat.AttachInstructionFiles = false
	cfg.Chat.ProjectRoot = workspaceRoot
	cfg.Chat.SaveDir = filepath.Join(workspaceRoot, ".idiom", "transcripts")
	cfg.Chat.DebugLogDir = filepath.Join(workspaceRoot, ".idiom", "logs")
	cfg.Chat.RequestTimeoutSeconds = 10
	cfg.Chat.ServerStartTimeoutSecs = 0
	cfg.Workspace.Root = workspaceRoot
	cfg.Workspace.CWD = workspaceRoot
	cfg.Workspace.EnableWrites = enableWrites
	cfg.Workspace.EnableShell = enableShell
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	data, err := json.Marshal(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	configPath := filepath.Join(workspaceRoot, "config.json")
	gomega.Expect(os.WriteFile(configPath, append(data, '\n'), 0o600)).To(gomega.Succeed())
	return configPath
}

func writeDevelopmentE2EFile(t rootTestContext, root, relativePath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed())
	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed())
}

func readDevelopmentE2ECompletion(t rootTestContext, workspaceRoot, sessionID string) string {
	t.Helper()
	path := filepath.Join(workspaceRoot, filepath.FromSlash(runrecord.RunCompletionRelPath(sessionID)))
	body, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return string(body)
}

func decodeDevelopmentE2ERunOutput(raw string) runrecord.RunOutputRecord {
	ginkgo.GinkgoHelper()
	var output runrecord.RunOutputRecord
	gomega.Expect(json.Unmarshal([]byte(raw), &output)).To(gomega.Succeed(), raw)
	return output
}
