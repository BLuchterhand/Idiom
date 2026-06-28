package workspace

import (
	"path/filepath"
	"testing"
)

func TestIsDefaultReadableConversationLogPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	globalLog := filepath.Join(home, ".idiom", "logs", "conversations", "session.jsonl")
	codexSession := filepath.Join(home, ".codex", "sessions", "2026", "06", "28", "rollout-2026-06-28T12-00-00-session.jsonl")
	codexArchivedSession := filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-06-28T12-00-00-session.jsonl")

	tests := []struct {
		path string
		want bool
	}{
		{path: "logs/conversations/session.jsonl", want: true},
		{path: "./logs/conversations/session.jsonl", want: true},
		{path: ".idiom/logs/conversations/session.jsonl", want: true},
		{path: globalLog, want: true},
		{path: codexSession, want: true},
		{path: codexArchivedSession, want: true},
		{path: "logs/telemetry/session.jsonl", want: false},
		{path: ".idiom/config.json", want: false},
		{path: filepath.Join(home, ".idiom", "config.json"), want: false},
		{path: filepath.Join(home, ".codex", "config.toml"), want: false},
		{path: filepath.Join(home, ".codex", "sessions", "not-a-session.txt"), want: false},
	}

	for _, tt := range tests {
		if got := IsDefaultReadableConversationLogPath(tt.path); got != tt.want {
			t.Fatalf("IsDefaultReadableConversationLogPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
