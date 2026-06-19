package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

// newLaneSwitchReplyModel wires a Model whose route classifier is disabled
// (so the route falls back to read-only conversation) and whose LLM client
// is an in-process server that returns a fixed refusal reply. This is the
// production shape that triggers the lane-switch affordance: the classifier
// did not upgrade the route, the model recognised it cannot act, and its
// final reply contains lane-refusal language.
func newLaneSwitchReplyModel(t *testing.T, refusalReply string) (*Model, *httptest.Server, *int) {
	t.Helper()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "planner-model"}},
			})
			return
		}

		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{"content": refusalReply},
			}},
		})
	}))

	cfg := config.Default()
	cfg.RouteClassifier.Enabled = false // force read-only conversation fallback
	cfg.Chat.AutoStartServer = false

	m := &Model{
		cfg:    cfg,
		client: llama.NewClient(server.URL, time.Second),
	}

	return m, server, &requests
}

// TestRunStandardPromptOnceOpensPopupOnRefusalReply is the end-to-end wire
// proof for the reply-scan detection path: when the assistant's final
// reply contains lane-refusal language and the TUI is interactive,
// runStandardPromptOnce must open the lane-switch popup and preserve the
// original refusal in the returned reply.
func TestRunStandardPromptOnceOpensPopupOnRefusalReply(t *testing.T) {
	refusal := "I'm currently in read-only mode and cannot edit files. Use /implement to switch to the write-capable lane."

	m, server, _ := newLaneSwitchReplyModel(t, refusal)
	defer server.Close()

	reply, err := m.runStandardPromptOnce(context.Background(), "update the README to document the new flag")
	if err != nil {
		t.Fatalf("runStandardPromptOnce: %v\nreply: %s", err, reply)
	}

	if !m.laneSwitchPopup.open {
		t.Fatal("expected lane-switch popup to open after refusal reply in TUI mode")
	}

	if !strings.Contains(reply, "read-only mode") {
		t.Errorf("expected original refusal preserved in reply, got %q", reply)
	}

	if m.laneSwitchPopup.objective != "update the README to document the new flag" {
		t.Errorf("expected popup to carry original objective, got %q", m.laneSwitchPopup.objective)
	}

	if !m.laneSwitchPopup.options[0].recommended {
		t.Error("expected implement option flagged recommended (reply mentions /implement)")
	}
}

// TestRunStandardPromptOnceAppendsOptionsOnRefusalReplyHeadless proves the
// headless degradation path: when a JSON event stream / suppress flag is
// active, the same refusal reply causes the numbered options block to be
// appended to the returned reply instead of opening an interactive popup.
func TestRunStandardPromptOnceAppendsOptionsOnRefusalReplyHeadless(t *testing.T) {
	refusal := "I'm in read-only mode; please switch to the plan-execute lane and re-run."

	m, server, _ := newLaneSwitchReplyModel(t, refusal)
	m.suppressPromptTranscript = true // headless: no TUI thread
	defer server.Close()

	reply, err := m.runStandardPromptOnce(context.Background(), "fix the off-by-one in pkg/auth")
	if err != nil {
		t.Fatalf("runStandardPromptOnce: %v\nreply: %s", err, reply)
	}

	if m.laneSwitchPopup.open {
		t.Error("headless path must NOT open the interactive popup")
	}

	if !strings.Contains(reply, "read-only mode") {
		t.Errorf("expected original refusal preserved, got %q", reply)
	}

	required := []string{
		"/plan-execute fix the off-by-one in pkg/auth",
		"Recommended: /plan-execute",
	}
	for _, want := range required {
		if !strings.Contains(reply, want) {
			t.Errorf("headless reply missing %q\n----\n%s", want, reply)
		}
	}
}

// TestRunStandardPromptOnceNoPopupOnNonRefusalReply is the negative
// regression: a normal read-only answer that does not contain lane-refusal
// language must NOT trigger the popup or append any options block.
func TestRunStandardPromptOnceNoPopupOnNonRefusalReply(t *testing.T) {
	normal := "The auth middleware validates tokens before delegating to the session store. No file changes are needed."

	m, server, _ := newLaneSwitchReplyModel(t, normal)
	defer server.Close()

	reply, err := m.runStandardPromptOnce(context.Background(), "explain how auth works")
	if err != nil {
		t.Fatalf("runStandardPromptOnce: %v\nreply: %s", err, reply)
	}

	if m.laneSwitchPopup.open {
		t.Error("popup must NOT open for a non-refusal read-only answer")
	}

	if strings.Contains(reply, "Would you like to switch") {
		t.Errorf("non-refusal reply must not contain lane-switch options block, got %q", reply)
	}
}
