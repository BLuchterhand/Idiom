package redaction

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no secrets", "hello world", "hello world"},
		{"empty string", "", ""},
		{"api_key assignment", `api_key=mysecret123`, `api_key=<redacted>`},
		{"apiKey colon with openai key value", `"apiKey": "sk-abc123def456"`, `"apiKey": "<redacted-secret>"`},
		{"token assignment", `token=abcdef`, `token=<redacted>`},
		{"secret assignment", `secret: value123`, `secret=<redacted>`},
		{"password assignment", `password=hunter2`, `password=<redacted>`},
		{"case insensitive api_key", `API_KEY=SECRET`, `API_KEY=<redacted>`},
		{"case insensitive token", `Token: MyToken`, `Token=<redacted>`},
		{"case insensitive password", `PASSWORD=topsecret`, `PASSWORD=<redacted>`},
		{"bearer token", `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload`, `Authorization: Bearer <redacted>`},
		{"bearer case insensitive", `bearer abc123DEF`, `Bearer <redacted>`},
		{"openai key", `key=sk-abcdefghijklmnopqrstuv`, `key=<redacted-secret>`},
		{"openai key too short", `key=sk-short`, `key=sk-short`},
		{"multiple secrets", `api_key=secret1 token=secret2`, `api_key=<redacted> token=<redacted>`},
		{"api-key with dash", `api-key=mykey`, `api-key=<redacted>`},
		{"surrounded by text", `the api_key=secret123 here`, `the api_key=<redacted> here`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecrets(tt.input)
			if got != tt.want {
				t.Errorf("RedactSecrets(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactedValue(t *testing.T) {
	t.Run("empty string returns empty placeholder", func(t *testing.T) {
		got := RedactedValue("")
		if got != "<empty>" {
			t.Errorf("RedactedValue(\"\") = %q, want %q", got, "<empty>")
		}
	})

	t.Run("whitespace-only returns empty placeholder", func(t *testing.T) {
		got := RedactedValue("   ")
		if got != "<empty>" {
			got2 := RedactedValue("   ")
			if got2 != "<empty>" {
				t.Errorf("RedactedValue(\"   \") = %q, want %q", got2, "<empty>")
			}
		}
	})

	t.Run("consistent output for same input", func(t *testing.T) {
		a := RedactedValue("my-secret-key")
		b := RedactedValue("my-secret-key")

		if a != b {
			t.Errorf("RedactedValue not consistent: %q != %q", a, b)
		}
	})

	t.Run("different inputs produce different outputs", func(t *testing.T) {
		a := RedactedValue("secret-a")
		b := RedactedValue("secret-b")

		if a == b {
			t.Errorf("different inputs should produce different outputs, both got %q", a)
		}
	})

	t.Run("output contains length", func(t *testing.T) {
		got := RedactedValue("hello")
		if !strings.Contains(got, "len=5") {
			t.Errorf("RedactedValue(\"hello\") = %q, want len=5", got)
		}
	})

	t.Run("output contains sha256 prefix", func(t *testing.T) {
		got := RedactedValue("test")
		if !strings.HasPrefix(got, "<redacted ") || !strings.Contains(got, "sha256:") {
			t.Errorf("RedactedValue(\"test\") = %q, want sha256 prefix", got)
		}
	})

	t.Run("trims input before hashing", func(t *testing.T) {
		a := RedactedValue("secret")
		b := RedactedValue("  secret  ")

		if a != b {
			t.Errorf("RedactedValue should trim whitespace: %q != %q", a, b)
		}
	})
}
