package localops

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForbiddenEvidenceLeakAllowsPrivacyFieldNamesButRejectsTrueCapture(t *testing.T) {
	allowed := `{"privacy_redaction_status":{"secret_captured":false,"private_path_captured":false},"redacted_fields":["secret_value"]}`
	if finding := ForbiddenEvidenceLeak(allowed); finding != "" {
		t.Fatalf("allowed privacy field names should not fail forbidden scan: %s", finding)
	}

	blocked := `{"privacy_redaction_status":{"secret_captured":true}}`
	if finding := ForbiddenEvidenceLeak(blocked); finding != "secret_captured" {
		t.Fatalf("expected true secret capture flag, got %q", finding)
	}
}

func TestContainsAbsolutePathIgnoresURLPath(t *testing.T) {
	if !ContainsAbsolutePath("workspace=/Users/ben/Documents/GitHub/idiom") {
		t.Fatal("expected absolute filesystem path")
	}
	if ContainsAbsolutePath("docs=https://example.com/idiom/path") {
		t.Fatal("URL paths should not be treated as local absolute paths")
	}
}

func TestSafeTokenNormalizesBoundedASCIIIdentifier(t *testing.T) {
	if got := SafeToken(" No-Go Client Class "); got != "no_go_client_class" {
		t.Fatalf("safe token = %q, want no_go_client_class", got)
	}
	long := strings.Repeat("a", 90)
	if got := SafeToken(long); len(got) != 80 {
		t.Fatalf("safe token should be capped at 80 chars, got len=%d", len(got))
	}
}

func TestContentHashUsesStableShortHexDigest(t *testing.T) {
	if got := ContentHash([]byte("same")); got != ContentHash([]byte("same")) || len(got) != 16 {
		t.Fatalf("content hash should be stable 16-char digest, got %q", got)
	}
	if ContentHash([]byte("same")) == ContentHash([]byte("different")) {
		t.Fatalf("content hash should change with input")
	}
}

func TestCompactStringsTrimsDropsEmptyAndDedupes(t *testing.T) {
	got := CompactStrings(" alpha ", "", "beta", "alpha", " beta ")
	want := []string{"alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("compact strings length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compact strings[%d] = %q, want %q in %#v", i, got[i], want[i], got)
		}
	}
}

func TestSafeCommandRedactsSensitiveRelativePathTokens(t *testing.T) {
	for _, command := range []string{
		"go test ./customer-private-repo",
		"go test ../secrets/token.txt",
	} {
		got := SafeCommand(command)
		if strings.Contains(got, "customer") || strings.Contains(got, "secret") || strings.Contains(got, "token") {
			t.Fatalf("expected sensitive relative command token redacted, got %q", got)
		}
	}
	if got := SafeCommand("go test ./..."); got != "go test ./..." {
		t.Fatalf("safe relative command token should be preserved, got %q", got)
	}
}

func TestForbiddenEvidenceLeakRejectsRelativeTelemetryLogPaths(t *testing.T) {
	for _, body := range []string{
		`{"path":"logs/telemetry/session.jsonl"}`,
		`{"path":"logs/conversations/session.jsonl"}`,
		`{"path":".idiom/logs/conversations/session.jsonl"}`,
	} {
		if finding := ForbiddenEvidenceLeak(body); finding != "log_path" {
			t.Fatalf("expected log_path finding for %s, got %q", body, finding)
		}
	}
}

func TestRedactForbiddenStringsRedactsMapStringAnyValuesAndKeys(t *testing.T) {
	report := struct {
		Metadata map[string]any `json:"metadata"`
	}{
		Metadata: map[string]any{
			"debug_path":       "/Users/ben/private-repo",
			"private_customer": "safe",
			"nested":           map[string]any{"token_value": "token=abc123"},
			"safe_description": "metadata only",
		},
	}
	RedactForbiddenStrings(&report)
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	rendered := string(body)
	for _, forbidden := range []string{"/Users/ben/private-repo", "private_customer", "token=abc123"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("expected forbidden map content %q to be redacted: %s", forbidden, rendered)
		}
	}
	if finding := ForbiddenEvidenceLeak(rendered); finding != "" {
		t.Fatalf("redacted map should pass forbidden scan, got %q in %s", finding, rendered)
	}
}

func TestTaskEvidenceClassifiesSensitiveTaskWithoutRawCapture(t *testing.T) {
	got := TaskEvidence("CustomerX-private-repo secret abc123 /Users/ben/project")
	if got.RawTaskCaptured {
		t.Fatalf("task evidence must not capture raw task: %#v", got)
	}
	if got.RedactionStatus != "redacted" || got.TaskID == "" {
		t.Fatalf("expected redacted hashed task evidence, got %#v", got)
	}
	for _, want := range []string{"absolute_path", "secret_marker", "sensitive_identifier"} {
		if !stringSliceContains(got.ContentClasses, want) {
			t.Fatalf("missing class %q in %#v", want, got.ContentClasses)
		}
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
