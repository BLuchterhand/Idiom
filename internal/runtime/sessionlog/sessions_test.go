package sessionlog

import (
	"os"
	"path/filepath"
	"testing"

	"idiom/internal/platform/config"
)

func TestListRecordedSessionsUsesSafePromptSummary(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}

	writeSessionLog(t, dir, "20260419-090000.000", []string{
		`{"type":"message","role":"user","content":"summarize the repository"}`,
		`{"type":"message","role":"assistant","content":"ok"}`,
	})
	writeSessionLog(t, dir, "20260419-100000.000", []string{
		`{"type":"message","role":"user","content":"raw_prompt: customer-acme token=abc123 /Users/alice/private/repo"}`,
		`{"type":"message","role":"assistant","content":"ok"}`,
	})

	sessions, err := ListRecordedSessions(cfg)
	if err != nil {
		t.Fatalf("ListRecordedSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}

	if sessions[0].FirstPrompt != "[redacted prompt summary]" {
		t.Fatalf("sensitive prompt summary = %q, want redacted marker", sessions[0].FirstPrompt)
	}
	if sessions[1].FirstPrompt != "summarize the repository" {
		t.Fatalf("benign prompt summary = %q", sessions[1].FirstPrompt)
	}
}

func TestSafePromptSummaryRedactsSensitivityMarkers(t *testing.T) {
	for _, input := range []string{
		"raw_prompt: summarize the private repo",
		"token=abc123",
		"customer-acme onboarding",
		"open /Users/alice/private/repo/main.go",
	} {
		if got := SafePromptSummary(input, 80); got != "[redacted prompt summary]" {
			t.Fatalf("SafePromptSummary(%q) = %q, want redacted marker", input, got)
		}
	}
}

func writeSessionLog(t *testing.T, dir string, id string, lines []string) {
	t.Helper()

	path := filepath.Join(dir, id+".jsonl")
	data := []byte{}
	for _, line := range lines {
		data = append(data, line...)
		data = append(data, '\n')
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
