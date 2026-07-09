package indexsupport

import (
	"regexp"
	"testing"
)

func TestNormalizeSymbolPatternExactMatch(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	}

	if result := NormalizeSymbolPattern("func Hello", extractors); result != "Hello" {
		t.Fatalf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPatternNoMatch(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	}

	if result := NormalizeSymbolPattern("Hello", extractors); result != "Hello" {
		t.Fatalf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPatternTrimsSpace(t *testing.T) {
	if result := NormalizeSymbolPattern("  Hello  ", nil); result != "Hello" {
		t.Fatalf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPatternMultipleExtractors(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
	}

	if result := NormalizeSymbolPattern("struct Foo", extractors); result != "Foo" {
		t.Fatalf("expected Foo, got %s", result)
	}
}

func TestStripLineCommentWithComment(t *testing.T) {
	if result := StripLineComment("int x = 5; // comment"); result != "int x = 5; " {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestStripLineCommentNoComment(t *testing.T) {
	if result := StripLineComment("int x = 5;"); result != "int x = 5;" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestStripLineCommentEmpty(t *testing.T) {
	if result := StripLineComment(""); result != "" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestStripLineCommentOnlyComment(t *testing.T) {
	if result := StripLineComment("// just a comment"); result != "" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestFirstQualifiedWithScope(t *testing.T) {
	if result := FirstQualified("MyClass", "myMethod", "."); result != "MyClass.myMethod" {
		t.Fatalf("expected MyClass.myMethod, got %s", result)
	}
}

func TestFirstQualifiedEmptyScope(t *testing.T) {
	if result := FirstQualified("", "myMethod", "."); result != "myMethod" {
		t.Fatalf("expected myMethod, got %s", result)
	}
}

func TestFirstQualifiedWhitespaceScope(t *testing.T) {
	if result := FirstQualified("  ", "myMethod", "."); result != "myMethod" {
		t.Fatalf("expected myMethod, got %s", result)
	}
}

func TestFirstQualifiedCppSeparator(t *testing.T) {
	if result := FirstQualified("ns", "func", "::"); result != "ns::func" {
		t.Fatalf("expected ns::func, got %s", result)
	}
}
