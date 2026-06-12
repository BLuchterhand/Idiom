package util

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "hello", "world"); got != "hello" {
		t.Errorf("FirstNonEmpty = %q, want %q", got, "hello")
	}

	if got := FirstNonEmpty("", "  "); got != "" {
		t.Errorf("FirstNonEmpty all empty = %q, want empty", got)
	}

	if got := FirstNonEmpty("first", "second"); got != "first" {
		t.Errorf("FirstNonEmpty = %q, want %q", got, "first")
	}
}

func TestContainsAny(t *testing.T) {
	if !ContainsAny("hello world", "world", "foo") {
		t.Error("ContainsAny should find 'world'")
	}

	if ContainsAny("hello", "world", "foo") {
		t.Error("ContainsAny should not find any needle")
	}

	if !ContainsAny("hello", "") {
		t.Error("ContainsAny with empty needle should match")
	}
}

func TestUniqueNonEmpty(t *testing.T) {
	result := UniqueNonEmpty([]string{"a", "  b  ", "a", "", "b", "c"})
	if len(result) != 3 {
		t.Fatalf("UniqueNonEmpty len = %d, want 3: %v", len(result), result)
	}

	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("UniqueNonEmpty = %v, want [a b c]", result)
	}

	if len(UniqueNonEmpty(nil)) != 0 {
		t.Error("UniqueNonEmpty(nil) should return empty")
	}
}

func TestEqualTrimFold(t *testing.T) {
	if !EqualTrimFold("  Hello  ", "hello") {
		t.Error("EqualTrimFold should match case-insensitive trimmed")
	}

	if EqualTrimFold("hello", "world") {
		t.Error("EqualTrimFold should not match different strings")
	}
}

func TestNonEmptyTrimmedLines(t *testing.T) {
	result := NonEmptyTrimmedLines("  a  \n\n  b  \n  c  ")
	if len(result) != 3 {
		t.Fatalf("NonEmptyTrimmedLines len = %d, want 3", len(result))
	}

	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("NonEmptyTrimmedLines = %v", result)
	}

	if len(NonEmptyTrimmedLines("")) != 0 {
		t.Error("NonEmptyTrimmedLines empty should return empty")
	}
}

func TestFallbackNonEmpty(t *testing.T) {
	if got := Fallback("hello", "alt"); got != "hello" {
		t.Errorf("Fallback = %q, want %q", got, "hello")
	}
}

func TestFallbackEmpty(t *testing.T) {
	if got := Fallback("", "alt"); got != "alt" {
		t.Errorf("Fallback = %q, want %q", got, "alt")
	}
}

func TestFallbackWhitespaceOnly(t *testing.T) {
	if got := Fallback("   ", "alt"); got != "alt" {
		t.Errorf("Fallback = %q, want %q", got, "alt")
	}
}

func TestFallbackBothEmpty(t *testing.T) {
	if got := Fallback("", ""); got != "" {
		t.Errorf("Fallback = %q, want empty", got)
	}
}

func TestContainsFold(t *testing.T) {
	tests := []struct {
		haystack, needle string
		want             bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "foo", false},
		{"Hello World", "", false},
		{"Hello World", "   ", false},
	}
	for _, tt := range tests {
		got := ContainsFold(tt.haystack, tt.needle)
		if got != tt.want {
			t.Errorf("ContainsFold(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
		}
	}
}

func TestTrimHeadingQualifier(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello (world)", "Hello"},
		{"Hello [ref]", "Hello"},
		{"Hello - suffix", "Hello"},
		{"Hello -- long suffix", "Hello"},
		{"Hello", "Hello"},
		{"  Hello (world)  ", "Hello"},
		{"(starts with paren)", "(starts with paren)"},
	}
	for _, tt := range tests {
		got := TrimHeadingQualifier(tt.in)
		if got != tt.want {
			t.Errorf("TrimHeadingQualifier(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
