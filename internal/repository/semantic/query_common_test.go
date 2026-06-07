package semantic

import (
	"regexp"
	"testing"
)

func TestNormalizeSymbolPattern_ExactMatch(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	}
	result := NormalizeSymbolPattern("func Hello", extractors)
	if result != "Hello" {
		t.Errorf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPattern_NoMatch(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^func\s+([A-Za-z_]\w*)`),
	}
	result := NormalizeSymbolPattern("Hello", extractors)
	if result != "Hello" {
		t.Errorf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPattern_TrimsSpace(t *testing.T) {
	result := NormalizeSymbolPattern("  Hello  ", nil)
	if result != "Hello" {
		t.Errorf("expected Hello, got %s", result)
	}
}

func TestNormalizeSymbolPattern_MultipleExtractors(t *testing.T) {
	extractors := []*regexp.Regexp{
		regexp.MustCompile(`^class\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^struct\s+([A-Za-z_]\w*)`),
	}
	result := NormalizeSymbolPattern("struct Foo", extractors)
	if result != "Foo" {
		t.Errorf("expected Foo, got %s", result)
	}
}

func TestStripLineComment_WithComment(t *testing.T) {
	result := StripLineComment("int x = 5; // comment")
	if result != "int x = 5; " {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineComment_NoComment(t *testing.T) {
	result := StripLineComment("int x = 5;")
	if result != "int x = 5;" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineComment_Empty(t *testing.T) {
	result := StripLineComment("")
	if result != "" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineComment_OnlyComment(t *testing.T) {
	result := StripLineComment("// just a comment")
	if result != "" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineCommentStringAware_BasicComment(t *testing.T) {
	result := StripLineCommentStringAware("int x = 5; // comment")
	if result != "int x = 5; " {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineCommentStringAware_InString(t *testing.T) {
	result := StripLineCommentStringAware(`String s = "http://example.com";`)
	if result != `String s = "http://example.com";` {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestStripLineCommentStringAware_EscapedQuote(t *testing.T) {
	result := StripLineCommentStringAware(`String s = "say \"hi\""; // done`)
	if result != `String s = "say \"hi\""; ` {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestFirstQualified_WithScope(t *testing.T) {
	result := FirstQualified("MyClass", "myMethod", ".")
	if result != "MyClass.myMethod" {
		t.Errorf("expected MyClass.myMethod, got %s", result)
	}
}

func TestFirstQualified_EmptyScope(t *testing.T) {
	result := FirstQualified("", "myMethod", ".")
	if result != "myMethod" {
		t.Errorf("expected myMethod, got %s", result)
	}
}

func TestFirstQualified_WhitespaceScope(t *testing.T) {
	result := FirstQualified("  ", "myMethod", ".")
	if result != "myMethod" {
		t.Errorf("expected myMethod, got %s", result)
	}
}

func TestFirstQualified_CppSeparator(t *testing.T) {
	result := FirstQualified("ns", "func", "::")
	if result != "ns::func" {
		t.Errorf("expected ns::func, got %s", result)
	}
}
