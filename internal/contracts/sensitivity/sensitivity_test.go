package sensitivity

import "testing"

func TestContainsSecretMarkerCoversSharedPolicy(t *testing.T) {
	for _, input := range []string{
		"Authorization: Bearer abc123",
		"AKIA1234567890ABCD",
		"ghp_1234567890abcdef",
		"token=abc123",
		"secret abc123",
		"sk-1234567890abcdef",
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
