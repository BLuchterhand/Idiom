package textutil

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		marker   string
		want     string
	}{
		{"shorter than limit", "hello", 10, "...", "hello"},
		{"exactly at limit", "hello", 5, "...", "hello"},
		{"longer than limit", "hello world", 8, "...", "hello..."},
		{"empty string", "", 5, "...", ""},
		{"zero limit", "hello", 0, "...", "hello"},
		{"negative limit", "hello", -1, "...", "hello"},
		{"limit smaller than marker", "hello", 2, "...", "he"},
		{"limit equals marker length", "hello", 3, "...", "hel"},
		{"custom marker", "hello world", 10, "…", "hello w…"},
		{"empty marker", "hello world", 5, "", "hello"},
		{"single byte limit", "hello", 1, "...", "h"},
		{"does not split emoji with marker", "hi😀there", 6, "...", "hi..."},
		{"does not split CJK with custom marker", "hello世界", 10, "…", "hello…"},
		{"limit smaller than first rune", "😀hello", 2, "...", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateBytes(tt.input, tt.maxBytes, tt.marker)
			if got != tt.want {
				t.Errorf("TruncateBytes(%q, %d, %q) = %q, want %q",
					tt.input, tt.maxBytes, tt.marker, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("TruncateBytes(%q, %d, %q) returned invalid UTF-8: %q",
					tt.input, tt.maxBytes, tt.marker, got)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		marker   string
		want     string
	}{
		{"shorter than limit", "hello", 10, "...", "hello"},
		{"exactly at limit", "hello", 5, "...", "hello"},
		{"longer than limit", "hello world", 8, "...", "hello..."},
		{"empty string", "", 5, "...", ""},
		{"zero limit", "hello", 0, "...", "hello"},
		{"negative limit", "hello", -1, "...", "hello"},
		{"emoji truncation", "😀😃😄😁😆", 3, "...", "😀😃😄"},
		{"CJK truncation", "你好世界测试", 3, "...", "你好世"},
		{"limit smaller than marker", "hello", 2, "...", "he"},
		{"limit equals marker length", "hello", 3, "...", "hel"},
		{"custom marker with emoji", "😀😃😄😁", 3, "…", "😀😃…"},
		{"empty marker", "hello world", 5, "", "hello"},
		{"mixed ascii and emoji", "hi😀😃", 3, "...", "hi😀"},
		{"marker longer than limit", "hello", 2, "....", "he"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateRunes(tt.input, tt.maxRunes, tt.marker)
			if got != tt.want {
				t.Errorf("TruncateRunes(%q, %d, %q) = %q, want %q",
					tt.input, tt.maxRunes, tt.marker, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"shorter than limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"longer than limit", "hello world", 8, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "hello"},
		{"negative limit", "hello", -1, "hello"},
		{"delegates to TruncateBytes", "abcdefghij", 5, "ab..."},
		{"does not split multibyte text", "ab😀cd", 6, "ab..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q",
					tt.input, tt.maxBytes, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Truncate(%q, %d) returned invalid UTF-8: %q",
					tt.input, tt.maxBytes, got)
			}
		})
	}
}

func TestTruncateWithByteCount(t *testing.T) {
	got := TruncateWithByteCount("hello world", 5)
	want := "hello\n\n[truncated 6 bytes]\n"
	if got != want {
		t.Fatalf("TruncateWithByteCount = %q, want %q", got, want)
	}

	if got := TruncateWithByteCount("short", 10); got != "short" {
		t.Fatalf("short text should not be truncated, got %q", got)
	}

	if got := TruncateWithByteCount("😀hello", 2); got != "\n\n[truncated 7 bytes]\n" {
		t.Fatalf("multibyte truncation should stay UTF-8 safe, got %q", got)
	}
}
