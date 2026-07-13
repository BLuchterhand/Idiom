package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/runprofile"
	"idiom/internal/contracts/toolfacts"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Debug logs", func() {
	ginkgo.It("persists privacy-safe typed rejection facts", func() {
		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:  ginkgo.GinkgoT().TempDir(),
			Mode: runprofile.DebugLogModeRedacted,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogToolResultDetail(ToolResultDetail{
			Step:    2,
			Agent:   "main",
			Tool:    "edit_file",
			Summary: "Mutation blocked",
			Error:   "outside authorized scope",
			Facts: &toolfacts.ToolResultFacts{
				RejectionDisposition:   toolfacts.ToolRejectionPolicyDenied,
				RejectedWritePaths:     []string{"internal/feature/target.go", "/tmp/private.go"},
				PendingSourceBodyPaths: []string{"internal/feature/source.go"},
			},
		})).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		gomega.Expect(records).To(gomega.HaveLen(1))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("rejection_disposition", "policy_denied"))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("rejected_write_paths", []any{"internal/feature/target.go"}))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("pending_source_body_paths", []any{"internal/feature/source.go"}))
		encoded, err := json.Marshal(records[0])
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(encoded)).NotTo(gomega.ContainSubstring("/tmp/private.go"))
	})

	ginkgo.It("masks prompts and tool payloads in redacted mode", func() {
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:           dir,
			Mode:          runprofile.DebugLogModeRedacted,
			WorkspaceRoot: "/tmp/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogSessionStart(SessionStart{
			AppName: "Idiom",
			PromptSources: []PromptSource{{
				Kind:        "baseline",
				DisplayName: "base.md",
				Path:        "/tmp/workspace/prompts/base.md",
			}},
			SystemPrompt:  "hidden system prompt body",
			WorkspaceRoot: "/tmp/workspace",
			WorkspaceCWD:  "/tmp/workspace/internal/surfaces/tui/app",
		})).To(gomega.Succeed())

		raw := `{"tool":"edit_file","path":"/tmp/workspace/config.json","content":"top secret patch","command":"TOKEN=abc123 go test ./..."}`
		gomega.Expect(logger.LogToolRequest(1, "implement", raw, "edit_file", "normal", false, true, "edit_file:config.json")).To(gomega.Succeed())
		gomega.Expect(logger.LogMessage("user", "api_key=secret123 path=/tmp/workspace/config.json")).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		gomega.Expect(records[0]["system_prompt"]).To(gomega.BeNil())

		digest, ok := records[0]["system_prompt_digest"].(string)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(digest).To(gomega.HavePrefix("sha256:"))

		gomega.Expect(records[0]["workspace_root"]).To(gomega.Equal("."))

		sourceRecords, ok := records[0]["prompt_sources"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(sourceRecords).To(gomega.HaveLen(1))

		sourceRecord, _ := sourceRecords[0].(map[string]any)
		gomega.Expect(sourceRecord).To(gomega.HaveKeyWithValue("path", "./prompts/base.md"))

		rawField, _ := records[1]["raw"].(string)
		gomega.Expect(rawField).NotTo(gomega.ContainSubstring("top secret patch"))
		gomega.Expect(rawField).NotTo(gomega.ContainSubstring("go test ./..."))
		gomega.Expect(rawField).To(gomega.ContainSubstring("redacted 16 chars"))
		gomega.Expect(rawField).To(gomega.ContainSubstring("redacted shell command"))

		msgContent, _ := records[2]["content"].(string)
		gomega.Expect(msgContent).NotTo(gomega.ContainSubstring("secret123"))
		gomega.Expect(msgContent).NotTo(gomega.ContainSubstring("/tmp/workspace"))
	})

	ginkgo.It("preserves prompts and tool payloads in full mode", func() {
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:           dir,
			Mode:          runprofile.DebugLogModeFull,
			WorkspaceRoot: "/tmp/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogSessionStart(SessionStart{
			AppName:       "Idiom",
			SystemPrompt:  "hidden system prompt body",
			WorkspaceRoot: "/tmp/workspace",
			WorkspaceCWD:  "/tmp/workspace/internal/surfaces/tui/app",
		})).To(gomega.Succeed())

		raw := `{"tool":"shell","command":"go test ./...","path":"/tmp/workspace/config.json"}`
		gomega.Expect(logger.LogToolRequest(1, "implement", raw, "shell", "normal", false, true, "shell:go")).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		gomega.Expect(records[0]["system_prompt"]).To(gomega.Equal("hidden system prompt body"))

		rawField, _ := records[1]["raw"].(string)
		gomega.Expect(rawField).To(gomega.ContainSubstring("go test ./..."))
		gomega.Expect(rawField).To(gomega.ContainSubstring("/tmp/workspace/config.json"))
	})

	ginkgo.It("normalizes modes and writes wrapper records", func() {
		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:           ginkgo.GinkgoT().TempDir(),
			Mode:          runprofile.DebugLogModeFull,
			WorkspaceRoot: "/tmp/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.Mode()).To(gomega.Equal(runprofile.DebugLogModeFull))

		logger.SetMode("")

		gomega.Expect(logger.Mode()).To(gomega.Equal(runprofile.DebugLogModeRedacted))

		logger.SetMode(runprofile.DebugLogModeFull)

		gomega.Expect(logger.LogContext([]string{"base.md"}, "system prompt", "normal", "medium")).To(gomega.Succeed())
		gomega.Expect(logger.LogToolResult(2, "main", "read_file", "Read /tmp/workspace/main.go", "")).To(gomega.Succeed())
		gomega.Expect(logger.LogInputSubmission("hello")).To(gomega.Succeed())
		gomega.Expect(logger.LogError("runner", "failed")).To(gomega.Succeed())
		gomega.Expect(logger.LogServer(true, "model", true, "")).To(gomega.Succeed())
		gomega.Expect(logger.LogArtifact("report", "/tmp/workspace/report.json")).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		wantTypes := []string{"context_update", "tool_result", "input_submission", "error", "server", "artifact"}

		gomega.Expect(records).To(gomega.HaveLen(len(wantTypes)))

		for i, want := range wantTypes {
			gomega.Expect(records[i]["type"]).To(gomega.Equal(want))
		}
		gomega.Expect(records[0]["system_prompt"]).To(gomega.Equal("system prompt"))
	})

	ginkgo.It("logs redacted phase events", func() {
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:           dir,
			Mode:          runprofile.DebugLogModeRedacted,
			WorkspaceRoot: "/tmp/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogPhase("implement", "verify", "active", "Running check 1/2: go build ./... inside /tmp/workspace")).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		gomega.Expect(records).To(gomega.HaveLen(1))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("type", "phase"))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("lane", "implement"))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("phase", "verify"))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("state", "active"))

		detail, _ := records[0]["detail"].(string)
		gomega.Expect(detail).NotTo(gomega.ContainSubstring("/tmp/workspace"))
	})

	ginkgo.It("records discovery-balance metadata", func() {
		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:  ginkgo.GinkgoT().TempDir(),
			Mode: runprofile.DebugLogModeFull,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogToolRequestDetail(ToolRequestDetail{
			Step:           1,
			Agent:          "main",
			Raw:            `{"tool":"structural_search","pattern":"if $X { $Y }"}`,
			Tool:           "structural_search",
			WorkspaceMode:  "normal",
			Narrowing:      true,
			Action:         "structural_search:.:if $x { $y }:go",
			DiscoveryClass: "ast_search",
			DiscoveryScope: "targeted",
			DiscoveryCost:  "medium",
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogToolResultDetail(ToolResultDetail{
			Step:           1,
			Agent:          "main",
			Tool:           "structural_search",
			Summary:        "Ran structural search",
			DiscoveryClass: "ast_search",
			DiscoveryScope: "targeted",
			DiscoveryCost:  "medium",
			ExtraFields: map[string]any{
				"context_store_kind":     "sqlite-graph-v1",
				"context_files_indexed":  3,
				"non_context_debug_data": "ignored",
			},
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogSessionSummary(SessionSummary{
			Turns:            1,
			ModelCalls:       1,
			ToolCalls:        1,
			DiscoveryByClass: []DiscoveryAggregate{{Name: "ast_search", Calls: 1}},
			DiscoveryByCost:  []DiscoveryAggregate{{Name: "medium", Calls: 1}},
		})).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		for _, index := range []int{0, 1} {
			gomega.Expect(records[index]).To(gomega.HaveKeyWithValue("discovery_class", "ast_search"))
			gomega.Expect(records[index]).To(gomega.HaveKeyWithValue("discovery_scope", "targeted"))
			gomega.Expect(records[index]).To(gomega.HaveKeyWithValue("discovery_cost", "medium"))
		}
		gomega.Expect(records[1]).To(gomega.HaveKeyWithValue("context_store_kind", "sqlite-graph-v1"))
		gomega.Expect(records[1]).To(gomega.HaveKeyWithValue("context_files_indexed", float64(3)))
		gomega.Expect(records[1]["non_context_debug_data"]).To(gomega.BeNil())
		gomega.Expect(records[2]["discovery_by_class"]).NotTo(gomega.BeNil())
		gomega.Expect(records[2]["discovery_by_cost"]).NotTo(gomega.BeNil())
	})

	ginkgo.It("suppresses duplicate assistant summary messages", func() {
		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:  ginkgo.GinkgoT().TempDir(),
			Mode: runprofile.DebugLogModeFull,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogMessage("architecture_summary", "same grounded answer")).To(gomega.Succeed())
		gomega.Expect(logger.LogPhase("architecture", "complete", "done", "phase records must not break message dedupe")).To(gomega.Succeed())
		gomega.Expect(logger.LogMessage("control_rails_enforcement", `{"passed":true}`)).To(gomega.Succeed())
		gomega.Expect(logger.LogMessage("assistant", "same grounded answer")).To(gomega.Succeed())
		gomega.Expect(logger.LogMessage("assistant", "different final answer")).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		assistantMessages := 0

		for _, record := range records {
			if record["type"] == "message" && record["role"] == "assistant" {
				assistantMessages++
				gomega.Expect(record["content"]).To(gomega.Equal("different final answer"))
			}
		}
		gomega.Expect(assistantMessages).To(gomega.Equal(1))
	})

	ginkgo.It("writes run events without a message record", func() {
		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:  ginkgo.GinkgoT().TempDir(),
			Mode: runprofile.DebugLogModeFull,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogRunEvent("completion_gate_recovery", "internal recovery prompt")).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		gomega.Expect(records).To(gomega.HaveLen(1))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("type", "run_event"))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("role", "completion_gate_recovery"))
	})

	ginkgo.It("logs context-fill telemetry", func() {
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:           dir,
			Mode:          runprofile.DebugLogModeRedacted,
			WorkspaceRoot: "/tmp/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		defer logger.Close()

		gomega.Expect(logger.LogSessionStart(SessionStart{
			AppName:       "Idiom",
			WorkspaceRoot: "/tmp/workspace",
			CtxSize:       196608,
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogToolResultDetail(ToolResultDetail{
			Step:        2,
			Agent:       "main",
			Tool:        "read_file",
			Summary:     "Read file",
			OutputChars: 8400,
			OutputLines: 200,
			Path:        "/tmp/workspace/internal/surfaces/tui/app/app.go",
			StartLine:   1,
			EndLine:     200,
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogModelCall(ModelCall{
			Step:                       3,
			Agent:                      "main",
			PromptTokens:               12000,
			CompletionTokens:           350,
			TotalTokens:                12350,
			PromptCacheHitTokens:       9000,
			PromptCacheMissTokens:      3000,
			PromptCacheAccountingKnown: true,
			ReasoningTokens:            40,
			CtxSize:                    196608,
			MessageCount:               14,
			RoleChars:                  map[string]int{"system": 4500, "user": 800, "assistant": 1200, "tool": 9000},
			ChosenTool:                 "edit_file",
			DurationMillis:             2400,
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogTurnSummary(TurnSummary{
			Lane:                       "main",
			UsedModelCalls:             3,
			UsedToolResults:            6,
			PromptTokens:               20000,
			CompletionTokens:           600,
			TotalTokens:                20600,
			PromptCacheHitTokens:       15000,
			PromptCacheMissTokens:      5000,
			PromptCacheAccountingKnown: true,
			ReasoningTokens:            70,
			DurationMillis:             8000,
			StopReason:                 "completed",
		})).To(gomega.Succeed())

		gomega.Expect(logger.LogSessionSummary(SessionSummary{
			Turns:                      2,
			ModelCalls:                 6,
			ToolCalls:                  12,
			PromptTokens:               40000,
			CompletionTokens:           1200,
			TotalTokens:                41200,
			PromptCacheHitTokens:       30000,
			PromptCacheMissTokens:      10000,
			PromptCacheAccountingKnown: true,
			PromptCacheHitRate:         0.75,
			ReasoningTokens:            120,
			PeakPromptTokens:           22000,
			CtxSize:                    196608,
			PeakCtxPct:                 float64(22000) / float64(196608),
			Compactions:                1,
			TopToolsByOutput:           []ToolOutputAggregate{{Tool: "read_file", Calls: 8, OutputChars: 60000}},
			DurationMillis:             30000,
		})).To(gomega.Succeed())
		gomega.Expect(logger.Close()).To(gomega.Succeed())

		records := readDebugLogRecords(logger.Path())
		byType := map[string]map[string]any{}

		for _, rec := range records {
			recordType, _ := rec["type"].(string)
			byType[recordType] = rec
		}

		gomega.Expect(byType["session_start"]).To(gomega.HaveKeyWithValue("ctx_size", float64(196608)))

		tr := byType["tool_result"]
		gomega.Expect(tr["output_chars"]).NotTo(gomega.BeNil())
		gomega.Expect(tr["output_lines"]).NotTo(gomega.BeNil())
		gomega.Expect(tr["path"]).NotTo(gomega.BeNil())
		gomega.Expect(tr["start_line"]).NotTo(gomega.BeNil())

		path, _ := tr["path"].(string)
		gomega.Expect(path).NotTo(gomega.ContainSubstring("/tmp/workspace"))

		mc := byType["model_call"]
		gomega.Expect(mc["ctx_pct"]).NotTo(gomega.BeNil())

		pct, _ := mc["ctx_pct"].(float64)
		gomega.Expect(pct).To(gomega.BeNumerically(">", 0))
		gomega.Expect(pct).To(gomega.BeNumerically("<", 1))

		rc, ok := mc["role_chars"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(rc["tool"]).NotTo(gomega.BeNil())
		gomega.Expect(mc).To(gomega.HaveKeyWithValue("chosen_tool", "edit_file"))
		gomega.Expect(mc).To(gomega.HaveKeyWithValue("prompt_cache_accounting_known", true))
		gomega.Expect(mc).To(gomega.HaveKeyWithValue("prompt_cache_hit_tokens", float64(9000)))
		gomega.Expect(mc).To(gomega.HaveKeyWithValue("prompt_cache_miss_tokens", float64(3000)))
		gomega.Expect(mc).To(gomega.HaveKeyWithValue("reasoning_tokens", float64(40)))

		ts := byType["turn_summary"]
		gomega.Expect(ts).NotTo(gomega.BeNil())
		gomega.Expect(ts["used_model_calls"]).NotTo(gomega.BeNil())
		gomega.Expect(ts).To(gomega.HaveKeyWithValue("prompt_cache_accounting_known", true))
		gomega.Expect(ts).To(gomega.HaveKeyWithValue("prompt_cache_hit_tokens", float64(15000)))
		gomega.Expect(ts).To(gomega.HaveKeyWithValue("prompt_cache_miss_tokens", float64(5000)))
		gomega.Expect(ts).To(gomega.HaveKeyWithValue("reasoning_tokens", float64(70)))

		ss := byType["session_summary"]
		gomega.Expect(ss).NotTo(gomega.BeNil())
		gomega.Expect(ss["peak_ctx_pct"]).NotTo(gomega.BeNil())
		gomega.Expect(ss["top_tools_by_output"]).NotTo(gomega.BeNil())
		gomega.Expect(ss).To(gomega.HaveKeyWithValue("prompt_cache_accounting_known", true))
		gomega.Expect(ss).To(gomega.HaveKeyWithValue("prompt_cache_hit_tokens", float64(30000)))
		gomega.Expect(ss).To(gomega.HaveKeyWithValue("prompt_cache_miss_tokens", float64(10000)))
		gomega.Expect(ss).To(gomega.HaveKeyWithValue("prompt_cache_hit_rate", float64(0.75)))
		gomega.Expect(ss).To(gomega.HaveKeyWithValue("reasoning_tokens", float64(120)))
	})

	ginkgo.It("includes a session ID in the filename when configured", func() {
		// SessionID in the filename lets us attribute logs to a specific
		// idiom run when concurrent sessions write logs in the same
		// directory at near-identical timestamps.
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:       dir,
			Mode:      runprofile.DebugLogModeRedacted,
			SessionID: "abc123def456",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		path := logger.Path()
		gomega.Expect(path).To(gomega.HaveSuffix("-abc123def456.jsonl"))
	})

	ginkgo.It("keeps the legacy filename when the session ID is empty", func() {
		// Backward compatibility: existing callers that don't pass
		// SessionID get the legacy timestamp-only filename.
		dir := ginkgo.GinkgoT().TempDir()

		logger, err := NewDebugLogger(DebugLoggerOptions{
			Dir:  dir,
			Mode: runprofile.DebugLogModeRedacted,
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		name := filepath.Base(logger.Path())
		// Format is YYYYMMDD-HHMMSS.mmm.jsonl — exactly one hyphen,
		// not two. A session-stamped filename would have two hyphens.
		gomega.Expect(strings.Count(name, "-")).To(gomega.Equal(1))
	})
})

func readDebugLogRecords(path string) []map[string]any {
	ginkgo.GinkgoHelper()
	data, err := os.ReadFile(filepath.Clean(path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		record := make(map[string]any)
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())
		records = append(records, record)
	}

	return records
}
