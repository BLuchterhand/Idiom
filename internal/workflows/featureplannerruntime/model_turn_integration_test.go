package featureplannerruntime

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
)

type plannerDiagnosticRecord struct {
	role    string
	content string
}

var _ = ginkgo.It("retries an empty structured planner turn without leaking raw development context", ginkgo.Label("development-scenario", "integration", "contract"), func() {
	t := ginkgo.GinkgoT()
	client := llamamocks.NewMockLLMClient(gomock.NewController(t))
	prompt := "Plan the private migration for customer-ember in internal/session/store.go."
	answer := `{"tool":"submit_feature_plan","feature_id":"FEAT-MIGRATION-1"}`

	first := client.EXPECT().
		CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
			gomega.Expect(messages).To(gomega.Equal([]llama.ChatMessage{{Role: "user", Content: prompt}}))
			gomega.Expect(opts.ResponseFormatJSON).To(gomega.BeTrue())
			gomega.Expect(opts.EnableThinking).NotTo(gomega.BeNil())
			gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse())
			gomega.Expect(opts.MaxTokens).To(gomega.Equal(16384))

			return llama.ChatMessage{FinishReason: "length"}, nil
		})
	second := client.EXPECT().
		CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
			gomega.Expect(messages).To(gomega.HaveLen(1))
			gomega.Expect(messages[0].Role).To(gomega.Equal("user"))
			gomega.Expect(messages[0].Content).To(gomega.And(
				gomega.ContainSubstring("prior feature-planner response had empty visible content"),
				gomega.ContainSubstring("finish_reason=length"),
				gomega.ContainSubstring(prompt),
			))
			gomega.Expect(opts.ResponseFormatJSON).To(gomega.BeFalse())
			gomega.Expect(opts.EnableThinking).NotTo(gomega.BeNil())
			gomega.Expect(*opts.EnableThinking).To(gomega.BeFalse())

			return llama.ChatMessage{Content: answer, FinishReason: "stop"}, nil
		})
	gomock.InOrder(first, second)

	var diagnostics []plannerDiagnosticRecord
	got, err := CompleteModelTurn(
		context.Background(),
		client,
		prompt,
		llama.ChatOptions{MaxTokens: 64},
		string(autonomy.TaskFamilyFeaturePlannerPlanner),
		func(role string, content string) {
			diagnostics = append(diagnostics, plannerDiagnosticRecord{role: role, content: content})
		},
	)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(got).To(gomega.Equal(answer))
	gomega.Expect(diagnostics).To(gomega.HaveLen(3))
	gomega.Expect(diagnostics[0].role).To(gomega.Equal("feature_planner_prompt"))
	gomega.Expect(diagnostics[1].role).To(gomega.Equal("feature_planner_model_empty"))
	gomega.Expect(diagnostics[2].role).To(gomega.Equal("feature_planner_model"))
	gomega.Expect(diagnostics[2].content).To(gomega.ContainSubstring(`"tool":"submit_feature_plan"`))

	for _, diagnostic := range diagnostics {
		gomega.Expect(diagnostic.content).NotTo(gomega.ContainSubstring("customer-ember"))
		gomega.Expect(diagnostic.content).NotTo(gomega.ContainSubstring("FEAT-MIGRATION-1"))
	}
	gomega.Expect(diagnostics[0].content).To(gomega.And(
		gomega.ContainSubstring(`"raw_omitted":true`),
		gomega.ContainSubstring(`"sha256":"sha256:`),
	))
})
