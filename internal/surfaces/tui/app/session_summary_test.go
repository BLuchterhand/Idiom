package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/agent"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("model close writes session summary for empty session", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir:           filepath.Join(root, "logs", "conversations"),
			RequestTimeoutSeconds: 1,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	}

	model := New(cfg)
	logPath := model.DebugLogPath()
	model.Close()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	gomega.Expect(len(records)).To(gomega.Equal(1), fmt.Sprintf("expected exactly one session_summary, got %d in %#v", len(records), records))

	summary := records[0]
	gomega.Expect(summary["model_calls"]).To(gomega.Equal(float64(0)), fmt.Sprintf("empty session_summary should preserve zero counters, got %#v", summary))
	gomega.Expect(summary["tool_calls"]).To(gomega.Equal(float64(0)), fmt.Sprintf("empty session_summary should preserve zero counters, got %#v", summary))
	gomega.Expect(summary["turns"]).To(gomega.Equal(float64(0)), fmt.Sprintf("empty session_summary should preserve zero counters, got %#v", summary))
})

var _ = ginkgo.It("model close session summary includes provider cache usage", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir:           filepath.Join(root, "logs", "conversations"),
			RequestTimeoutSeconds: 1,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	}

	model := New(cfg)
	model.startPromptExecution(func(_ context.Context, session *promptExecutionSession) (string, error) {
		session.logRunEvent(agent.RunEvent{
			Role:    "model_call",
			Content: `{"step":1,"label":"main_loop","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":5,"prompt_cache_accounting_known":true,"reasoning_tokens":2}`,
		})

		return "", nil
	})
	model = waitForPromptWorkerResult(ctx, model)

	logPath := model.DebugLogPath()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	gomega.Expect(len(records)).To(gomega.Equal(1), fmt.Sprintf("expected one session_summary, got %d in %#v", len(records), records))

	summary := records[0]
	gomega.Expect(summary["prompt_cache_accounting_known"]).To(gomega.Equal(true), fmt.Sprintf("session_summary missing provider cache usage: %#v", summary))
	gomega.Expect(summary["prompt_cache_hit_tokens"]).To(gomega.Equal(float64(8)), fmt.Sprintf("session_summary missing provider cache usage: %#v", summary))
	gomega.Expect(summary["prompt_cache_miss_tokens"]).To(gomega.Equal(float64(5)), fmt.Sprintf("session_summary missing provider cache usage: %#v", summary))
	gomega.Expect(summary["reasoning_tokens"]).To(gomega.Equal(float64(2)), fmt.Sprintf("session_summary missing provider cache usage: %#v", summary))

	{
		got, _ := summary["prompt_cache_hit_rate"].(float64)
		gomega.Expect(got).To(gomega.BeNumerically(">=", 0.61), fmt.Sprintf("prompt_cache_hit_rate = %v, want about 0.615: %#v", got, summary))
		gomega.Expect(got).To(gomega.BeNumerically("<=", 0.62), fmt.Sprintf("prompt_cache_hit_rate = %v, want about 0.615: %#v", got, summary))
	}
})

var _ = ginkgo.It("model close session summary emits zero cache hit rate", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	cfg := config.Config{
		AppName: "Idiom",
		Chat: config.Chat{
			DebugLogDir:           filepath.Join(root, "logs", "conversations"),
			RequestTimeoutSeconds: 1,
		},
		Workspace: config.Workspace{
			Root: root,
			CWD:  root,
		},
	}

	model := New(cfg)
	model.startPromptExecution(func(_ context.Context, session *promptExecutionSession) (string, error) {
		session.logRunEvent(agent.RunEvent{
			Role:    "model_call",
			Content: `{"step":1,"label":"main_loop","prompt_tokens":13,"completion_tokens":3,"total_tokens":16,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":13,"prompt_cache_accounting_known":true}`,
		})

		return "", nil
	})
	model = waitForPromptWorkerResult(ctx, model)

	logPath := model.DebugLogPath()
	model.Close()

	records := readAppSessionSummaryRecords(t, logPath)
	gomega.Expect(len(records)).To(gomega.Equal(1), fmt.Sprintf("expected one session_summary, got %d in %#v", len(records), records))

	summary := records[0]
	gomega.Expect(summary["prompt_cache_accounting_known"]).To(gomega.Equal(true), fmt.Sprintf("session_summary missing known cache miss accounting: %#v", summary))
	gomega.Expect(summary["prompt_cache_hit_tokens"]).To(gomega.Equal(float64(0)), fmt.Sprintf("session_summary missing known cache miss accounting: %#v", summary))
	gomega.Expect(summary["prompt_cache_miss_tokens"]).To(gomega.Equal(float64(13)), fmt.Sprintf("session_summary missing known cache miss accounting: %#v", summary))

	{
		got, ok := summary["prompt_cache_hit_rate"].(float64)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("prompt_cache_hit_rate = %#v, want emitted zero", summary["prompt_cache_hit_rate"]))
		gomega.Expect(got).To(gomega.BeNumerically("==", 0), fmt.Sprintf("prompt_cache_hit_rate = %#v, want emitted zero", summary["prompt_cache_hit_rate"]))
	}
})

func readAppSessionSummaryRecords(t ginkgo.FullGinkgoTInterface, path string) []map[string]any {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(filepath.Clean(path))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read debug log: %v", err))

	var summaries []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record map[string]any

		{
			err := json.Unmarshal([]byte(line), &record)
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("unmarshal %q: %v", line, err))
		}

		if record["type"] == "session_summary" {
			summaries = append(summaries, record)
		}
	}

	return summaries
}
