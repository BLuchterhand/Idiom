package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
)

func writeSessionLog(t *testing.T, dir string, name string, lines []string) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := filepath.Join(dir, name)
	body := strings.Join(lines, "\n")

	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	return path
}

func TestResolveSessionLogPathFromID(t *testing.T) {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: "/tmp/conv"}}
	got := ResolveSessionLogPath(cfg, "20260418-055543.504")
	want := filepath.Join("/tmp/conv", "20260418-055543.504.jsonl")

	if got != want {
		t.Errorf("ResolveSessionLogPath id-form = %q, want %q", got, want)
	}
}

func TestResolveSessionLogPathFromPath(t *testing.T) {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: "/tmp/conv"}}
	if got := ResolveSessionLogPath(cfg, "logs/conversations/foo.jsonl"); got != "logs/conversations/foo.jsonl" {
		t.Errorf("path-form unchanged but got %q", got)
	}

	if got := ResolveSessionLogPath(cfg, "/abs/foo"); got != "/abs/foo.jsonl" {
		t.Errorf("abs-form should append .jsonl: %q", got)
	}
}

func TestListRecordedSessionsSummarizesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Chat: config.Chat{DebugLogDir: dir}}
	writeSessionLog(t, dir, "20260418-055543.504.jsonl", []string{
		`{"type":"message","role":"user","content":"hello world\nsecond line"}`,
		`{"type":"message","role":"assistant","content":"hi"}`,
		`{"type":"tool_request","tool":"read_file"}`,
	})
	writeSessionLog(t, dir, "20260419-100000.000.jsonl", []string{
		`{"type":"message","role":"user","content":"second session prompt"}`,
	})
	writeSessionLog(t, dir, "skipme.txt", []string{"not a session"})

	got, err := ListRecordedSessions(cfg)
	if err != nil {
		t.Fatalf("ListRecordedSessions: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d (%v)", len(got), got)
	}

	if got[0].ID != "20260419-100000.000" {
		t.Errorf("expected newest first, got %q", got[0].ID)
	}

	older := got[1]
	if older.UserMessages != 1 || older.AssistMessage != 1 {
		t.Errorf("older session counts = %d/%d, want 1/1", older.UserMessages, older.AssistMessage)
	}

	if older.FirstPrompt != "hello world" {
		t.Errorf("older first prompt = %q, want %q", older.FirstPrompt, "hello world")
	}
}

func TestListRecordedSessionsMissingDir(t *testing.T) {
	cfg := config.Config{Chat: config.Chat{DebugLogDir: filepath.Join(t.TempDir(), "does-not-exist")}}

	got, err := ListRecordedSessions(cfg)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("expected zero sessions for missing dir, got %d", len(got))
	}
}

func TestLoadHistoryFromSessionReplaysUserAssistant(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "session.jsonl", []string{
		`{"type":"message","role":"user","content":"first prompt"}`,
		`{"type":"message","role":"assistant","content":"first reply"}`,
		`{"type":"message","role":"system","content":"ignored"}`,
		`{"type":"tool_request","tool":"read_file"}`,
		`{"type":"message","role":"user","content":"second prompt"}`,
	})

	model := &Model{}
	if err := model.LoadHistoryFromSession(path); err != nil {
		t.Fatalf("LoadHistoryFromSession: %v", err)
	}

	if len(model.messages) != 3 {
		t.Fatalf("expected 3 messages, got %d (%v)", len(model.messages), model.messages)
	}

	want := []llama.ChatMessage{
		{Role: "user", Content: "first prompt"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second prompt"},
	}
	for i, w := range want {
		got := model.messages[i]
		if got.Role != w.Role || got.Content != w.Content {
			t.Errorf("messages[%d] = %#v, want %#v", i, got, w)
		}
	}
}

func TestLoadHistoryFromSessionRestoresGovernanceHistory(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "governed.jsonl", []string{
		`{"type":"session_start","app_name":"idiom"}`,
		`{"type":"message","role":"user","content":"inspect the control path"}`,
		`{"type":"message","role":"route_plan","content":"initial=explore followup=implement contract=implementation_result"}`,
		`{"type":"tool_request","step":1,"tool":"context_pack","action":"rank repository evidence","discovery_class":"context","discovery_scope":"repo","discovery_cost":"low"}`,
		`{"type":"tool_result","step":1,"tool":"context_pack","summary":"ranked 8 files","output_lines":12,"output_chars":900,"context_cache_state":"miss_recompute","context_cache_source":"recompute","context_cache_reason":"cache_miss","context_cache_pack_id":"pack-123","context_auto_refresh_attempted":true,"context_auto_refresh_files_indexed":12,"context_auto_refresh_reason":"stale_unbounded_graph"}`,
		`{"type":"run_event","role":"autonomy_event","content":"{\"type\":\"contract_compiled\",\"contract_id\":\"contract_123\",\"correlation\":{\"lane\":\"implement\",\"phase\":\"execute\",\"runtime_contract\":\"implementation_result\"}}"}`,
		`{"type":"message","role":"assistant","content":"The control path is inspectable."}`,
	})

	model := newHistoryPanelModel()

	model.history.viewport = viewport.New(96, 24)
	model.history.expanded = true

	if err := model.LoadHistoryFromSession(path); err != nil {
		t.Fatalf("LoadHistoryFromSession: %v", err)
	}

	if len(model.messages) != 2 {
		t.Fatalf("expected 2 model messages, got %d (%#v)", len(model.messages), model.messages)
	}

	if len(model.history.entries) != 2 {
		t.Fatalf("expected user and agent history entries, got %d: %#v", len(model.history.entries), model.history.entries)
	}

	agentEntry := model.history.entries[1]
	if agentEntry.inProgress {
		t.Fatal("replayed agent history should be finalized")
	}

	if len(agentEntry.toolCalls) != 1 {
		t.Fatalf("expected replayed tool call, got %#v", agentEntry.toolCalls)
	}

	if len(agentEntry.auditEvents) < 3 {
		t.Fatalf("expected route/cache/contract audit events, got %#v", agentEntry.auditEvents)
	}

	view := collapseWhitespace(model.history.viewport.View())
	for _, want := range []string{
		"Route: explore -> implement (implementation_result)",
		"Tools: 1 call(s) (1 discovery)",
		"Latest: context_pack ok - rank repository evidence",
		"Contracts: contract compiled",
		"Signals: cache miss_recompute, cache refreshed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("history viewport missing %q:\n%s", want, view)
		}
	}
}

func TestLoadHistoryFromSessionRejectsEmptyLog(t *testing.T) {
	dir := t.TempDir()
	path := writeSessionLog(t, dir, "empty.jsonl", []string{
		`{"type":"tool_request","tool":"read_file"}`,
	})
	model := &Model{}

	if err := model.LoadHistoryFromSession(path); err == nil {
		t.Fatal("expected error for log with no user/assistant messages")
	}
}
