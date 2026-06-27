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

	tests := []struct {
		path string
		want bool
	}{
		{path: "logs/conversations/session.jsonl", want: true},
		{path: "./logs/conversations/session.jsonl", want: true},
		{path: ".idiom/logs/conversations/session.jsonl", want: true},
		{path: globalLog, want: true},
		{path: "logs/telemetry/session.jsonl", want: false},
		{path: ".idiom/config.json", want: false},
		{path: filepath.Join(home, ".idiom", "config.json"), want: false},
	}

	for _, tt := range tests {
		if got := IsDefaultReadableConversationLogPath(tt.path); got != tt.want {
			t.Fatalf("IsDefaultReadableConversationLogPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
