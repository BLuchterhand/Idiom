package recovery

import (
	"strings"
	"testing"
)

func TestRedactTextRemovesTranscriptAndFileBodyShapes(t *testing.T) {
	redacted := RedactText("raw_prompt: operator text\nfile_body:\n```text\ncustomer source\npassword=secret\n```\ntranscript: hidden")

	for _, forbidden := range []string{"operator text", "customer source", "password=secret", "hidden"} {
		if strings.Contains(redacted, forbidden) {
			t.Fatalf("redaction leaked %q in %q", forbidden, redacted)
		}
	}

	if !strings.Contains(redacted, "[redacted sensitive recovery detail]") || !strings.Contains(redacted, "[redacted fenced recovery detail]") {
		t.Fatalf("expected redaction markers, got %q", redacted)
	}
}

func TestRedactTextRedactsSharedSecretMarkers(t *testing.T) {
	for _, input := range []string{
		"Authorization: Bearer abc123",
		"aws=AKIA1234567890ABCD",
		"github=ghp_1234567890abcdef",
		"api_key=secret-value",
	} {
		redacted := RedactText(input)
		if strings.Contains(redacted, input) || !strings.Contains(redacted, "[redacted sensitive recovery detail]") {
			t.Fatalf("RedactText(%q) = %q, want marker redacted", input, redacted)
		}
	}
}
