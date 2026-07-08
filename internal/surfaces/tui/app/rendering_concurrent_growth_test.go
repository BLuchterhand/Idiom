//go:build !race

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"idiom/internal/runtime/llama"
)

func TestRenderedMessageBodyDoesNotPanicWhenTranscriptGrowsDuringRender(t *testing.T) {
	content := strings.Repeat("large render body that keeps the render pass active long enough for a follow-up\n", 10000)
	messages := make([]llama.ChatMessage, 4)
	for i := range messages {
		messages[i] = llama.ChatMessage{
			Role:    "assistant",
			Content: content,
		}
	}

	m := &Model{
		ready:              true,
		viewport:           viewport.New(48, 12),
		messages:           messages[:4:4],
		perMsgRenderCache:  make([]string, 4),
		perMsgRenderHashes: make([]uint64, 4),
		perMsgRenderLen:    4,
		messageRenderWidth: 48,
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Microsecond)
		m.messages = append(m.messages, llama.ChatMessage{Role: "user", Content: "follow-up that arrived during render"})
		close(done)
	}()

	body := m.renderedMessageBody()
	<-done

	if !strings.Contains(body, "large render body") {
		t.Fatalf("rendered body missing original transcript content")
	}
}
