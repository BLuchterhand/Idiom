package app

import (
	"context"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

// newLaneSwitchReplyModel wires a Model whose route classifier is disabled
// (so the route falls back to read-only conversation) and whose LLM client
// is an in-process server that returns a fixed refusal reply. This is the
// production shape that triggers the lane-switch affordance: the classifier
// did not upgrade the route, the model recognised it cannot act, and its
// final reply contains lane-refusal language.
func newLaneSwitchReplyModel(t ginkgo.FullGinkgoTInterface, refusalReply string) (*Model, *ghttp.Server) {
	ginkgo.GinkgoHelper()

	server := ghttp.NewServer()
	server.Writer = ginkgo.GinkgoWriter
	server.RouteToHandler(http.MethodPost, "/v1/chat/completions", ghttp.CombineHandlers(
		ghttp.VerifyRequest(http.MethodPost, "/v1/chat/completions"),
		ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": refusalReply},
			}},
		}),
	))
	server.RouteToHandler(http.MethodGet, "/v1/models", ghttp.RespondWithJSONEncoded(http.StatusOK, map[string]any{
		"data": []map[string]any{{"id": "planner-model"}},
	}))
	t.Cleanup(server.Close)

	cfg := config.Default()
	cfg.RouteClassifier.Enabled = false // force read-only conversation fallback
	cfg.Chat.AutoStartServer = false

	m := &Model{
		cfg:    cfg,
		client: llama.NewClient(server.URL(), time.Second),
		events: make(chan tea.Msg, promptWorkerEventBuffer),
	}

	return m, server
}

var _ = ginkgo.Describe("Lane-switch reply integration", ginkgo.Label("integration"), func() {
	// TestRunStandardPromptOnceOpensPopupOnRefusalReply is the end-to-end wire
	// proof for the reply-scan detection path: when the assistant's final
	// reply contains lane-refusal language and the TUI is interactive,
	// runStandardPromptOnce must open the lane-switch popup and preserve the
	// original refusal in the returned reply.
	ginkgo.It("opens the lane-switch popup for an interactive refusal", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		refusal := "I'm currently in read-only mode and cannot edit files. Use /implement to switch to the write-capable lane."

		m, server := newLaneSwitchReplyModel(t, refusal)

		m.startPromptExecution(func(workerCtx context.Context, session *promptExecutionSession) (string, error) {
			return session.runStandardPromptOnce(workerCtx, "update the README to document the new flag")
		})
		m, result := drivePromptWorkerResult(ctx, m)
		reply, err := result.answer, result.err
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reply: %s", reply)
		gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeTrue())
		gomega.Expect(reply).To(gomega.ContainSubstring("read-only mode"))
		gomega.Expect(m.laneSwitchPopup.objective).To(gomega.Equal("update the README to document the new flag"))
		gomega.Expect(m.laneSwitchPopup.options[0].recommended).To(gomega.BeTrue())
		gomega.Expect(server.ReceivedRequests()).To(gomega.HaveLen(1))
	})

	// TestRunStandardPromptOnceAppendsOptionsOnRefusalReplyHeadless proves the
	// headless degradation path: when a JSON event stream / suppress flag is
	// active, the same refusal reply causes the numbered options block to be
	// appended to the returned reply instead of opening an interactive popup.
	ginkgo.It("appends lane-switch options to a headless refusal", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		refusal := "I'm in read-only mode; please switch to the plan-execute lane and re-run."

		m, server := newLaneSwitchReplyModel(t, refusal)
		m.suppressPromptTranscript = true // headless: no TUI thread

		m.startPromptExecution(func(workerCtx context.Context, session *promptExecutionSession) (string, error) {
			return session.runStandardPromptOnce(workerCtx, "fix the off-by-one in pkg/auth")
		})
		m, result := drivePromptWorkerResult(ctx, m)
		reply, err := result.answer, result.err
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reply: %s", reply)
		gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse())
		gomega.Expect(reply).To(gomega.ContainSubstring("read-only mode"))

		required := []string{
			"/plan-execute fix the off-by-one in pkg/auth",
			"Recommended: /plan-execute",
		}
		for _, want := range required {
			gomega.Expect(reply).To(gomega.ContainSubstring(want))
		}
		gomega.Expect(server.ReceivedRequests()).To(gomega.HaveLen(1))
	})

	// TestRunStandardPromptOnceNoPopupOnNonRefusalReply is the negative
	// regression: a normal read-only answer that does not contain lane-refusal
	// language must NOT trigger the popup or append any options block.
	ginkgo.It("leaves ordinary read-only answers unchanged", ginkgo.SpecTimeout(asyncOwnershipSpecTimeout), func(ctx ginkgo.SpecContext) {
		t := ginkgo.GinkgoT()
		normal := "The auth middleware validates tokens before delegating to the session store. No file changes are needed."

		m, server := newLaneSwitchReplyModel(t, normal)

		m.startPromptExecution(func(workerCtx context.Context, session *promptExecutionSession) (string, error) {
			return session.runStandardPromptOnce(workerCtx, "explain how auth works")
		})
		m, result := drivePromptWorkerResult(ctx, m)
		reply, err := result.answer, result.err
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "reply: %s", reply)
		gomega.Expect(m.laneSwitchPopup.open).To(gomega.BeFalse())
		gomega.Expect(reply).NotTo(gomega.ContainSubstring("Would you like to switch"))
		gomega.Expect(server.ReceivedRequests()).To(gomega.HaveLen(1))
	})
})
