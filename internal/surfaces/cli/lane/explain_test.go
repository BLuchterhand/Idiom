package lane

import (
	"bytes"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

var _ = ginkgo.Describe("explain", func() {
	ginkgo.It("build lane explain summary parses synthetic log", func() {
		log := strings.Join([]string{
			`{"type":"session_start","session_id":"abc123","ts":"2026-05-11T10:00:00Z"}`,
			`{"type":"tool_result","tool":"read_file","ts":"2026-05-11T10:00:05Z"}`,
			`{"type":"tool_result","tool":"read_file","error":"hidden by contract","ts":"2026-05-11T10:00:06Z"}`,
			`{"type":"tool_result","tool":"completion_gate","error":"required paths remain unchanged","ts":"2026-05-11T10:00:10Z"}`,
			`{"type":"completion_status","status":"blocked","blocker_kind":"required_paths_remaining"}`,
			`{"type":"turn_summary","stop_reason":"completion_gate_required_paths_remaining_repeated","budget_exhausted":true}`,
			`{"type":"session_summary","model_calls":3,"tool_calls":3,"turns":1,"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500,"prompt_cache_hit_tokens":700,"prompt_cache_miss_tokens":300,"prompt_cache_accounting_known":true,"reasoning_tokens":25,"peak_prompt_tokens":800,"ctx_size":8192,"duration_ms":10000,"ts":"2026-05-11T10:00:10Z"}`,
			`{"type":"final","content":"<<QUESTION>>: missing exact anchor"}`,
		}, "\n")

		s := buildLaneExplainSummary([]byte(log))
		gomega.Expect(s.SessionID).To(gomega.Equal("abc123"), fmt.Sprintf("SessionID = %q, want abc123", s.SessionID))
		gomega.Expect(s.ModelCalls).To(gomega.Equal(3), fmt.Sprintf("call counts = %d/%d/%d, want 3/3/1", s.ModelCalls, s.ToolCalls, s.Turns))
		gomega.Expect(s.ToolCalls).To(gomega.Equal(3), fmt.Sprintf("call counts = %d/%d/%d, want 3/3/1", s.ModelCalls, s.ToolCalls, s.Turns))
		gomega.Expect(s.Turns).To(gomega.Equal(1), fmt.Sprintf("call counts = %d/%d/%d, want 3/3/1", s.ModelCalls, s.ToolCalls, s.Turns))
		gomega.Expect(s.TotalTokens).To(gomega.Equal(1500), fmt.Sprintf("tokens = %d/%d/%d, want 1500/1000/500", s.TotalTokens, s.PromptTokens, s.CompletionTokens))
		gomega.Expect(s.PromptTokens).To(gomega.Equal(1000), fmt.Sprintf("tokens = %d/%d/%d, want 1500/1000/500", s.TotalTokens, s.PromptTokens, s.CompletionTokens))
		gomega.Expect(s.CompletionTokens).To(gomega.Equal(500), fmt.Sprintf("tokens = %d/%d/%d, want 1500/1000/500", s.TotalTokens, s.PromptTokens, s.CompletionTokens))
		gomega.Expect(s.CtxSize).To(gomega.Equal(8192), fmt.Sprintf("ctx/peak = %d/%d, want 8192/800", s.CtxSize, s.PeakPromptTokens))
		gomega.Expect(s.PeakPromptTokens).To(gomega.Equal(800), fmt.Sprintf("ctx/peak = %d/%d, want 8192/800", s.CtxSize, s.PeakPromptTokens))
		gomega.Expect(s.PromptCacheAccountingKnown).To(gomega.BeTrue(), fmt.Sprintf("cache usage = known:%v hit:%d miss:%d reasoning:%d", s.PromptCacheAccountingKnown, s.PromptCacheHitTokens, s.PromptCacheMissTokens, s.ReasoningTokens))
		gomega.Expect(s.PromptCacheHitTokens).To(gomega.Equal(700), fmt.Sprintf("cache usage = known:%v hit:%d miss:%d reasoning:%d", s.PromptCacheAccountingKnown, s.PromptCacheHitTokens, s.PromptCacheMissTokens, s.ReasoningTokens))
		gomega.Expect(s.PromptCacheMissTokens).To(gomega.Equal(300), fmt.Sprintf("cache usage = known:%v hit:%d miss:%d reasoning:%d", s.PromptCacheAccountingKnown, s.PromptCacheHitTokens, s.PromptCacheMissTokens, s.ReasoningTokens))
		gomega.Expect(s.ReasoningTokens).To(gomega.Equal(25), fmt.Sprintf("cache usage = known:%v hit:%d miss:%d reasoning:%d", s.PromptCacheAccountingKnown, s.PromptCacheHitTokens, s.PromptCacheMissTokens, s.ReasoningTokens))
		gomega.Expect(s.StopReason).To(gomega.Equal("completion_gate_required_paths_remaining_repeated"), fmt.Sprintf("stop_reason=%q budget_exhausted=%v", s.StopReason, s.BudgetExhausted))
		gomega.Expect(s.BudgetExhausted).To(gomega.BeTrue(), fmt.Sprintf("stop_reason=%q budget_exhausted=%v", s.StopReason, s.BudgetExhausted))
		gomega.Expect(s.ToolRejections).To(gomega.HaveLen(2), fmt.Sprintf("ToolRejections len = %d, want 2: %v", len(s.ToolRejections), s.ToolRejections))
		gomega.Expect(s.ToolsByCount).NotTo(gomega.BeEmpty(), fmt.Sprintf("ToolsByCount[0] = %+v, want read_file=2", s.ToolsByCount[0]))
		gomega.Expect(s.ToolsByCount[0].Tool).To(gomega.Equal("read_file"), fmt.Sprintf("ToolsByCount[0] = %+v, want read_file=2", s.ToolsByCount[0]))
		gomega.Expect(s.ToolsByCount[0].Count).To(gomega.Equal(2), fmt.Sprintf("ToolsByCount[0] = %+v, want read_file=2", s.ToolsByCount[0]))
		gomega.Expect(s.Question).To(gomega.ContainSubstring("<<QUESTION>>:"), fmt.Sprintf("Question not captured: %q", s.Question))
		gomega.Expect(s.CompletionStatus["status"]).To(gomega.Equal("blocked"), fmt.Sprintf("CompletionStatus[status] = %v, want blocked", s.CompletionStatus["status"]))

	})
	ginkgo.It("build lane explain summary ignores malformed and unknown types", func() {
		log := strings.Join([]string{
			`not json`,
			`{"type":"some_future_event","field":"value"}`,
			`{"type":"session_summary","total_tokens":42}`,
			``,
			`{"type":"session_summary"`,
		}, "\n")

		s := buildLaneExplainSummary([]byte(log))
		gomega.Expect(s.TotalTokens).To(gomega.Equal(42), fmt.Sprintf("malformed lines should be ignored; TotalTokens=%d, want 42", s.TotalTokens))

	})
	ginkgo.It("select lane explain session picks newest by default", func() {
		dir := ginkgo.GinkgoT().TempDir()
		logsDir := filepath.Join(dir, ".idiom", "logs", "conversations")
		gomega.Expect(os.MkdirAll(logsDir, 0o755)).To(gomega.Succeed())

		for _, name := range []string{
			"20260510-100000.000-aaaa.jsonl",
			"20260511-080000.000-bbbb.jsonl",
			"20260511-100000.000-cccc.jsonl",
		} {
			gomega.Expect(os.WriteFile(filepath.Join(logsDir, name), []byte("{}"), 0o644)).To(gomega.Succeed())

		}

		got, err := selectLaneExplainSession(logsDir, "", "", dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("select: %v", err))
		gomega.Expect(strings.HasSuffix(got, "20260511-100000.000-cccc.jsonl")).To(gomega.BeTrue(), fmt.Sprintf("got %q, want suffix cccc", got))

	})
	ginkgo.It("select lane explain session filters by session id", func() {
		dir := ginkgo.GinkgoT().TempDir()
		logsDir := filepath.Join(dir, ".idiom", "logs", "conversations")
		gomega.Expect(os.MkdirAll(logsDir, 0o755)).To(gomega.Succeed())

		for _, name := range []string{
			"20260510-100000.000-aaaa.jsonl",
			"20260511-080000.000-bbbb.jsonl",
		} {
			gomega.Expect(os.WriteFile(filepath.Join(logsDir, name), []byte("{}"), 0o644)).To(gomega.Succeed())

		}

		got, err := selectLaneExplainSession(logsDir, "aaaa", "", dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("select: %v", err))
		gomega.Expect(strings.HasSuffix(got, "-aaaa.jsonl")).To(gomega.BeTrue(), fmt.Sprintf("got %q, want suffix aaaa.jsonl", got))

	})
	ginkgo.It("brief timestamp to filename prefix handles common formats", func() {
		cases := map[string]string{
			"2026-05-11T08:20:22.789-04:00":    "20260511-082022",
			"2026-05-11T08:20:22Z":             "20260511-082022",
			"2026-05-11T08:20:22.194349-04:00": "20260511-082022",
			"":                                 "",
			"not a timestamp":                  "",
		}
		for in, want := range cases {
			got := briefTimestampToFilenamePrefix(in)
			gomega.Expect(got).To(gomega.Equal(want), fmt.Sprintf("briefTimestampToFilenamePrefix(%q) = %q, want %q", in, got, want))

		}
	})
	ginkgo.It("render lane explain omits empty sections", func() {
		s := laneExplainSummary{
			SessionID:    "minimal",
			PromptTokens: 100,
			TotalTokens:  100,
			ToolsByCount: nil,
		}

		var buf bytes.Buffer

		renderLaneExplain(s, &buf)

		out := buf.String()
		gomega.Expect(out).To(gomega.ContainSubstring("session=minimal"), fmt.Sprintf("missing session header: %s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("tools called"), fmt.Sprintf("empty tools section should be omitted: %s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("tool rejections"), fmt.Sprintf("empty rejections section should be omitted: %s", out))
		gomega.Expect(out).NotTo(gomega.ContainSubstring("errors:"), fmt.Sprintf("empty errors section should be omitted: %s", out))

	})
	ginkgo.It("run lane explain no logs returns error exit", func() {
		dir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(dir, ".idiom", "logs", "conversations"), 0o755)).To(gomega.Succeed())

		opts := laneOptions{Mode: "explain", ProjectRoot: dir}

		var stdout, stderr bytes.Buffer

		exit := runLaneExplain(opts, &stdout, &stderr)
		gomega.Expect(exit).To(gomega.Equal(1), fmt.Sprintf("expected exit=1 when no logs present, got %d", exit))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("no matching session log found"), fmt.Sprintf("expected 'no matching session' message, got: %s", stderr.String()))

	})
})
