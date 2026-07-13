package root

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/platform/config"
	"idiom/internal/surfaces/cli/runrecord"
)

const (
	headlessBinarySmokeAPIKey = "idiom-smoke-key"
	headlessBinarySmokeMarker = "IDIOM_BINARY_SMOKE_OK"
)

var _ = ginkgo.Describe("Headless binary", ginkgo.Ordered, ginkgo.Label("e2e", "integration", "smoke"), func() {
	var bin string

	ginkgo.BeforeAll(func() {
		bin = buildIdiomBinary(ginkgo.GinkgoT())
	})

	ginkgo.It("completes a real Responses tool round trip and writes a private completion record", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		peer := &responsesAPISmokePeer{}
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)

		configPath := writeHeadlessBinarySmokeConfig(t, workspaceRoot, server.URL)
		prompt := "Read SMOKE.md with read_file and report its exact marker with a file:line citation. Do not modify files."
		t.Setenv("IDIOM_SMOKE_API_KEY", headlessBinarySmokeAPIKey)

		result := runIdiomBinary(t, bin,
			"run",
			"--config", configPath,
			"--prompt", prompt,
			"--session", "binary-smoke",
			"--output", "json",
			"--permission-profile", "read-only",
			"--approval-policy", "never",
		)

		requests, auth, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s\nrequests=%#v", result.stdout, result.stderr, requests)
		gomega.Expect(result.stderr).To(gomega.BeEmpty())
		gomega.Expect(requests).To(gomega.HaveLen(2), "the real agent should read once and then synthesize")
		gomega.Expect(auth).To(gomega.ConsistOf("Bearer "+headlessBinarySmokeAPIKey, "Bearer "+headlessBinarySmokeAPIKey))
		gomega.Expect(peer.snapshotPaths()).To(gomega.Equal([]string{"/v1/responses", "/v1/responses"}))
		gomega.Expect(requestContainsNamedTool(requests[0], "read_file")).To(gomega.BeTrue())
		gomega.Expect(requestContainsFunctionOutput(requests[1], "call_smoke_read", headlessBinarySmokeMarker)).To(gomega.BeTrue())

		var output runrecord.RunOutputRecord
		gomega.Expect(json.Unmarshal([]byte(result.stdout), &output)).To(gomega.Succeed(), result.stdout)
		gomega.Expect(output.TerminalClass).To(gomega.Equal(runrecord.TerminalClassCompleted))
		gomega.Expect(output.ExitCode).To(gomega.BeZero())
		gomega.Expect(output.Answer).To(gomega.And(
			gomega.ContainSubstring(headlessBinarySmokeMarker),
			gomega.ContainSubstring("SMOKE.md:1"),
		))
		gomega.Expect(output.PromptSHA256).NotTo(gomega.BeEmpty())
		gomega.Expect(output.AnswerSHA256).NotTo(gomega.BeEmpty())

		completionPath := filepath.Join(workspaceRoot, filepath.FromSlash(runrecord.RunCompletionRelPath("binary-smoke")))
		completionBody, err := os.ReadFile(completionPath)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(completionBody)).NotTo(gomega.ContainSubstring(prompt))
		gomega.Expect(string(completionBody)).NotTo(gomega.ContainSubstring(headlessBinarySmokeMarker))
	})

	ginkgo.It("streams ordered JSONL lifecycle and tool events from the compiled process", func() {
		t := ginkgo.GinkgoT()
		workspaceRoot := t.TempDir()
		peer := &responsesAPISmokePeer{}
		server := httptest.NewServer(http.HandlerFunc(peer.serveHTTP))
		ginkgo.DeferCleanup(server.Close)

		configPath := writeHeadlessBinarySmokeConfig(t, workspaceRoot, server.URL)
		t.Setenv("IDIOM_SMOKE_API_KEY", headlessBinarySmokeAPIKey)
		result := runIdiomBinary(t, bin,
			"run",
			"--config", configPath,
			"--prompt", "Read SMOKE.md and cite its marker.",
			"--session", "binary-jsonl-smoke",
			"--json",
			"--permission-profile", "read-only",
		)

		_, _, peerErrors := peer.snapshot()
		gomega.Expect(peerErrors).To(gomega.BeEmpty())
		gomega.Expect(result.code).To(gomega.BeZero(), "stdout=%s\nstderr=%s", result.stdout, result.stderr)
		gomega.Expect(result.stderr).To(gomega.BeEmpty())

		events := decodeHeadlessBinarySmokeEvents(result.stdout)
		start := eventTypeIndex(events, "start")
		request := eventTypeIndex(events, "tool_request")
		toolResult := eventTypeToolIndexAfter(events, "tool_result", "read_file", request)
		final := eventTypeIndex(events, "final")
		gomega.Expect([]int{start, request, toolResult, final}).To(gomega.HaveEach(gomega.BeNumerically(">=", 0)))
		gomega.Expect(start).To(gomega.BeNumerically("<", request), "events=%#v", events)
		gomega.Expect(request).To(gomega.BeNumerically("<", toolResult), "events=%#v", events)
		gomega.Expect(toolResult).To(gomega.BeNumerically("<", final), "events=%#v", events)
		gomega.Expect(events[request]).To(gomega.HaveKeyWithValue("tool", "read_file"))
		gomega.Expect(events[toolResult]).To(gomega.HaveKeyWithValue("tool", "read_file"))
		gomega.Expect(events[final]).To(gomega.HaveKeyWithValue("content", gomega.ContainSubstring(headlessBinarySmokeMarker)))
	})

})

// test-double: scripted-protocol-peer
type responsesAPISmokePeer struct {
	mu       sync.Mutex
	requests []map[string]any
	auth     []string
	paths    []string
	errors   []error
}

func (p *responsesAPISmokePeer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		p.recordError(fmt.Errorf("decode Responses request: %w", err))
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.auth = append(p.auth, r.Header.Get("Authorization"))
	p.paths = append(p.paths, r.URL.Path)
	requestNumber := len(p.requests)
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch requestNumber {
	case 1:
		_, _ = w.Write([]byte(`{
			"id":"resp-smoke-read",
			"status":"completed",
			"output":[{"type":"function_call","call_id":"call_smoke_read","name":"read_file","arguments":"{\"path\":\"SMOKE.md\"}"}],
			"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16}
		}`))
	case 2:
		_, _ = w.Write([]byte(`{
			"id":"resp-smoke-final",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The exact marker is IDIOM_BINARY_SMOKE_OK (` + "`SMOKE.md:1`" + `)."}]}],
			"usage":{"input_tokens":23,"output_tokens":10,"total_tokens":33}
		}`))
	default:
		err := fmt.Errorf("unexpected Responses request %d", requestNumber)
		p.recordError(err)
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	}
}

func (p *responsesAPISmokePeer) snapshotPaths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

func (p *responsesAPISmokePeer) recordError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors = append(p.errors, err)
}

func (p *responsesAPISmokePeer) snapshot() ([]map[string]any, []string, []error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	requests := append([]map[string]any(nil), p.requests...)
	auth := append([]string(nil), p.auth...)
	errors := append([]error(nil), p.errors...)
	return requests, auth, errors
}

func writeHeadlessBinarySmokeConfig(t rootTestContext, workspaceRoot string, baseURL string) string {
	t.Helper()

	gomega.Expect(os.WriteFile(filepath.Join(workspaceRoot, "SMOKE.md"), []byte(headlessBinarySmokeMarker+"\n"), 0o644)).To(gomega.Succeed())

	cfg := config.Default()
	cfg.LlamaServer.Kind = config.BackendCodex
	cfg.LlamaServer.BaseURL = baseURL
	cfg.LlamaServer.APIKeyEnv = "IDIOM_SMOKE_API_KEY"
	cfg.LlamaServer.Model = "idiom-smoke-model"
	cfg.LlamaServer.CtxSize = 32768
	cfg.Chat.AutoStartServer = false
	cfg.Chat.SystemPrompt = "Use the available workspace tools to answer from source evidence. Cite path:line evidence."
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
	cfg.Workspace.EnableShell = false
	cfg.Workspace.EnableWrites = false
	cfg.RouteClassifier.Enabled = false
	cfg.MCP = nil

	data, err := json.Marshal(cfg)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	configPath := filepath.Join(workspaceRoot, "config.json")
	gomega.Expect(os.WriteFile(configPath, append(data, '\n'), 0o600)).To(gomega.Succeed())
	return configPath
}

func requestContainsNamedTool(request map[string]any, name string) bool {
	tools, ok := request["tools"].([]any)
	if !ok {
		return false
	}

	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if ok && tool["name"] == name {
			return true
		}
	}
	return false
}

func requestContainsFunctionOutput(request map[string]any, callID string, marker string) bool {
	input, ok := request["input"].([]any)
	if !ok {
		return false
	}

	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "function_call_output" || item["call_id"] != callID {
			continue
		}
		output, _ := item["output"].(string)
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func decodeHeadlessBinarySmokeEvents(raw string) []map[string]any {
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil
		}
		events = append(events, event)
	}
	return events
}

func eventTypeIndex(events []map[string]any, eventType string) int {
	for index, event := range events {
		if event["type"] == eventType {
			return index
		}
	}
	return -1
}

func eventTypeToolIndexAfter(events []map[string]any, eventType string, tool string, after int) int {
	for index := after + 1; index < len(events); index++ {
		if events[index]["type"] == eventType && events[index]["tool"] == tool {
			return index
		}
	}
	return -1
}
