package util

import "testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateBytes(tt.input, tt.maxBytes, tt.marker)
			if got != tt.want {
				t.Errorf("TruncateBytes(%q, %d, %q) = %q, want %q",
					tt.input, tt.maxBytes, tt.marker, got, tt.want)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q",
					tt.input, tt.maxBytes, got, tt.want)
			}
		})
	}
}
