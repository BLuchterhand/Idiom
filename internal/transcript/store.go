package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"qwen-bubbles/internal/llama"
)

func SaveMarkdown(dir string, messages []llama.ChatMessage) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create transcript dir: %w", err)
	}

	filename := filepath.Join(dir, time.Now().Format("20060102-150405")+".md")
	var b strings.Builder
	b.WriteString("# Qwen Bubbles Transcript\n\n")
	b.WriteString("Saved: " + time.Now().Format(time.RFC3339) + "\n\n")

	for _, msg := range messages {
		role := strings.Title(msg.Role)
		if role == "" {
			role = "Message"
		}
		b.WriteString("## " + role + "\n\n")
		b.WriteString(msg.Content)
		b.WriteString("\n\n")
	}

	if err := os.WriteFile(filename, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("write transcript: %w", err)
	}
	return filename, nil
}
