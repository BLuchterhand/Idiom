package app

import (
	"context"
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/llama/llamamocks"
)

type scriptedLLMConfig struct {
	responses          []string
	reasoningResponses []string
	finishReasons      []string
	expectedCallCount  int
	assertCallCount    bool
}

// scriptedLLMRecorder programs the generated LLM mock with deterministic
// replies and records the requests that exercised it. It deliberately does
// not implement llama.LLMClient; tests must inject the generated mock.
type scriptedLLMRecorder struct {
	mock               *llamamocks.MockLLMClient
	responses          []string
	reasoningResponses []string
	finishReasons      []string
	messages           [][]llama.ChatMessage
	options            []llama.ChatOptions
	callCount          int
	attemptCount       int
	expectedCallCount  int
	assertCallCount    bool
}

func newScriptedLLMRecorder(t gomock.TestReporter, cfg scriptedLLMConfig) *scriptedLLMRecorder {
	recorder := &scriptedLLMRecorder{
		responses:          append([]string(nil), cfg.responses...),
		reasoningResponses: append([]string(nil), cfg.reasoningResponses...),
		finishReasons:      append([]string(nil), cfg.finishReasons...),
		expectedCallCount:  cfg.expectedCallCount,
		assertCallCount:    cfg.assertCallCount,
	}
	recorder.Mock(t)

	return recorder
}

func (r *scriptedLLMRecorder) Mock(t gomock.TestReporter) *llamamocks.MockLLMClient {
	if helper, ok := t.(interface{ Helper() }); ok {
		helper.Helper()
	}
	if r.mock != nil {
		return r.mock
	}

	ctrl := gomock.NewController(t)
	r.mock = llamamocks.NewMockLLMClient(ctrl)
	expectedCalls := len(r.responses)
	if r.assertCallCount {
		expectedCalls = r.expectedCallCount
	}
	r.mock.EXPECT().
		CompleteChat(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(r.completeChat).
		MaxTimes(expectedCalls)
	r.mock.EXPECT().
		CompleteChatMessage(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(r.completeChatMessage).
		MaxTimes(expectedCalls)
	// Readiness and capability probes are incidental to these scripted-response
	// fixtures. Behavioral model interactions remain bounded above and are
	// checked for exact aggregate consumption below.
	r.mock.EXPECT().ListModels(gomock.Any()).Return([]llama.ModelInfo{{ID: "mock-model"}}, nil).AnyTimes()
	r.mock.EXPECT().Backend().Return("mock").AnyTimes()
	r.mock.EXPECT().SupportsNativeTools().Return(false).AnyTimes()
	ginkgo.DeferCleanup(func() {
		gomega.Expect(r.attemptCount).To(gomega.Equal(expectedCalls), "scripted LLM interaction count")
	})

	return r.mock
}

func (r *scriptedLLMRecorder) completeChat(_ context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (string, error) {
	r.attemptCount++
	r.messages = append(r.messages, append([]llama.ChatMessage(nil), messages...))
	r.options = append(r.options, opts)

	if r.callCount >= len(r.responses) {
		return "", fmt.Errorf("unexpected LLM request %d: only %d scripted response(s)", r.callCount+1, len(r.responses))
	}

	response := r.responses[r.callCount]
	r.callCount++

	return response, nil
}

func (r *scriptedLLMRecorder) completeChatMessage(ctx context.Context, messages []llama.ChatMessage, opts llama.ChatOptions) (llama.ChatMessage, error) {
	callIndex := r.callCount
	response, err := r.completeChat(ctx, messages, opts)
	if err != nil {
		return llama.ChatMessage{}, err
	}

	finishReason := ""
	if callIndex < len(r.finishReasons) {
		finishReason = r.finishReasons[callIndex]
	}

	reasoning := ""
	if callIndex < len(r.reasoningResponses) {
		reasoning = r.reasoningResponses[callIndex]
	}

	return llama.ChatMessage{
		Role:             "assistant",
		Content:          response,
		ReasoningContent: reasoning,
		FinishReason:     finishReason,
	}, nil
}
