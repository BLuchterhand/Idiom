package sensitivity

import (
	"strings"
	"testing"
)

func TestContainsSecretMarkerCoversSharedPolicy(t *testing.T) {
	awsAccessKeyLike := strings.Join([]string{"AKIA", "1234567890ABCD"}, "")
	githubTokenLike := strings.Join([]string{"ghp", "_", "1234567890abcdef"}, "")
	openAIKeyLike := strings.Join([]string{"sk", "-", "1234567890abcdef"}, "")

	for _, input := range []string{
		"Authorization: Bearer abc123",
		awsAccessKeyLike,
		githubTokenLike,
		"token=abc123",
		"secret abc123",
		openAIKeyLike,
	} {
		if !ContainsSecretMarker(input) {
			t.Fatalf("ContainsSecretMarker(%q) = false", input)
		}
	}
}

func TestContainsSecretMarkerAllowsNonSecretTokenProse(t *testing.T) {
	for _, input := range []string{
		"optional total budget tokens for the run",
		"authorization boundary documentation",
		"Authorization: https://example.invalid/spec",
		"task-specific workaround",
	} {
		if ContainsSecretMarker(input) {
			t.Fatalf("ContainsSecretMarker(%q) = true", input)
		}
	}
}

func TestContainsSensitiveTextCoversSharedPrivacyPolicy(t *testing.T) {
	for _, input := range []string{
		"raw_prompt: summarize",
		"file_body includes source",
		"customer-acme-session",
		"token abc123",
		"/Users/example/private/repo",
		"~/workspace/customer",
	} {
		if !ContainsSensitiveText(input) {
			t.Fatalf("ContainsSensitiveText(%q) = false", input)
		}
	}
}

func TestContainsSensitiveTextAllowsBenignSecretProse(t *testing.T) {
	for _, input := range []string{
		"optional total budget tokens for the run",
		"task-specific workaround",
		"authorization boundary documentation",
	} {
		if ContainsSecretMarker(input) {
			t.Fatalf("ContainsSecretMarker(%q) = true", input)
		}
	}
}
