package util

import (
	"testing"
)

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"empty args", nil, ""},
		{"all empty", []string{"", "", ""}, ""},
		{"first wins", []string{"alpha", "beta", "gamma"}, "alpha"},
		{"skip leading empties", []string{"", "", "found"}, "found"},
		{"trailing empties ignored", []string{"first", "", ""}, "first"},
		{"whitespace-only counts as empty", []string{"  ", "\t", "\n", "actual"}, "actual"},
		{"returns trimmed value", []string{"  hello  "}, "hello"},
		{"picks first non-empty among whitespace", []string{"  ", "  middle  "}, "middle"},
		{"single empty string", []string{""}, ""},
		{"single non-empty string", []string{"only"}, "only"},
		{"zero-width spaces are not whitespace per TrimSpace", []string{"\u200b", "\u200b", "visible"}, "\u200b"},
		{"unicode whitespace", []string{"\u00a0", "\u3000", "content"}, "content"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FirstNonEmpty(tc.values...)
			if got != tc.want {
				t.Errorf("FirstNonEmpty(%v) = %q, want %q", tc.values, got, tc.want)
			}
		})
	}
}

func TestFirstNonEmptyDoesNotMutate(t *testing.T) {
	orig := []string{"  ", "  value  ", ""}
	_ = FirstNonEmpty(orig...)
	if orig[1] != "  value  " {
		t.Errorf("FirstNonEmpty mutated input slice element: got %q", orig[1])
	}
}

func TestCombineOutput(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{"both empty", "", "", ""},
		{"stdout only", "hello", "", "hello"},
		{"stderr only", "", "error", "error"},
		{"both present", "out", "err", "out\nerr"},
		{"stdout with trailing newline", "out\n", "", "out"},
		{"stderr with trailing newline", "", "err\n", "err"},
		{"both have surrounding whitespace", " out ", " err ", "out\nerr"},
		{"stdout is whitespace-only", "  \t ", "err", "err"},
		{"stderr is whitespace-only", "out", "  \t ", "out"},
		{"both whitespace-only", "  ", "  ", ""},
		{"multiline stdout", "line1\nline2", "", "line1\nline2"},
		{"multiline both", "a\nb", "c\nd", "a\nb\nc\nd"},
		{"leading newlines in stdout", "\nout", "", "out"},
		{"tab in stderr", "", "\twarn", "warn"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CombineOutput(tc.stdout, tc.stderr)
			if got != tc.want {
				t.Errorf("CombineOutput(%q, %q) = %q, want %q", tc.stdout, tc.stderr, got, tc.want)
			}
		})
	}
}

func TestEqualTrimFold(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"exact match", "hello", "hello", true},
		{"case insensitive", "Hello", "HELLO", true},
		{"leading whitespace on a", "  hello", "hello", true},
		{"trailing whitespace on b", "hello", "hello  ", true},
		{"both have whitespace", " hello ", " hello ", true},
		{"mixed case and whitespace", "  HeLLo  ", "  hElLo  ", true},
		{"empty strings", "", "", true},
		{"whitespace vs empty", "  ", "", true},
		{"different strings", "hello", "world", false},
		{"prefix not equal", "hello", "hello world", false},
		{"unicode case (ß not folded by Go EqualFold)", "straße", "STRASSE", false},
		{"tab vs space", "\thello", " hello", true},
		{"numbers differ", "v1", "v2", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EqualTrimFold(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("EqualTrimFold(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func BenchmarkFirstNonEmptyHit(b *testing.B) {
	vals := []string{"", "", "found", "also"}
	for b.Loop() {
		FirstNonEmpty(vals...)
	}
}

func BenchmarkFirstNonEmptyMiss(b *testing.B) {
	vals := []string{"", "", "", ""}
	for b.Loop() {
		FirstNonEmpty(vals...)
	}
}

func BenchmarkCombineOutput(b *testing.B) {
	for b.Loop() {
		CombineOutput("some output line\n", "some error line\n")
	}
}

func BenchmarkEqualTrimFold(b *testing.B) {
	for b.Loop() {
		EqualTrimFold("  Hello World  ", "  hello world  ")
	}
}
