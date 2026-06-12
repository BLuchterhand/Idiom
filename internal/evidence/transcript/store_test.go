package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/runtime/llama"
)

func TestSaveMarkdownWritesTranscriptWithRoleHeadings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "transcripts")

	path, err := SaveMarkdown(dir, []llama.ChatMessage{
		{Role: "user", Content: "Please inspect evidence."},
		{Role: "assistant", Content: "The evidence is grounded."},
		{Content: "Untitled message."},
	})
	if err != nil {
		t.Fatalf("SaveMarkdown: %v", err)
	}

	if !strings.HasPrefix(path, dir+string(os.PathSeparator)) || !strings.HasSuffix(path, ".md") {
		t.Fatalf("unexpected transcript path: %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}

	body := string(data)
	for _, want := range []string{
		"# Idiom Transcript",
		"## User\n\nPlease inspect evidence.",
		"## Assistant\n\nThe evidence is grounded.",
		"## Message\n\nUntitled message.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("transcript missing %q in:\n%s", want, body)
		}
	}
}

func TestTranscriptTitleNormalizesRoles(t *testing.T) {
	if got := transcriptTitle(" assistant"); got != "Assistant" {
		t.Fatalf("transcriptTitle trimmed role = %q, want Assistant", got)
	}

	if got := transcriptTitle(""); got != "" {
		t.Fatalf("transcriptTitle empty role = %q, want empty", got)
	}
}
