package analyzecmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSessionAcceptsDirectDebugLogRecords(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "session.jsonl")
	data := strings.Join([]string{
		`{"type":"message","role":"user","content":"find the redirect path"}`,
		`{"type":"tool_request","turn":1,"step":2,"tool":"search_content","raw":"{\"tool\":\"search_content\",\"pattern\":\"redirect\"}"}`,
		`{"type":"run_event","role":"discovery_redirect","content":"search_content -> go_find_symbol"}`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var summary sessionSummary

	calls, err := parseSession(logPath, "session", &summary)
	if err != nil {
		t.Fatalf("parseSession: %v", err)
	}

	if len(calls) != 1 || calls[0].Tool != "search_content" || calls[0].Step != 2 {
		t.Fatalf("unexpected parsed calls: %#v", calls)
	}
	if summary.TotalCalls != 1 || summary.Generic != 1 || summary.Redirects != 1 {
		t.Fatalf("unexpected summary counts: %#v", summary)
	}
	if summary.FirstPrompt != "find the redirect path" {
		t.Fatalf("unexpected first prompt: %q", summary.FirstPrompt)
	}
}
