package util

import "testing"

func TestNormalizePath_AlreadyClean(t *testing.T) {
	got := NormalizePath("foo/bar/baz")
	if got != "foo/bar/baz" {
		t.Errorf("got %q, want %q", got, "foo/bar/baz")
	}
}

func TestNormalizePath_Backslashes(t *testing.T) {
	got := NormalizePath(`foo\bar\baz`)
	if got != "foo/bar/baz" {
		t.Errorf("got %q, want %q", got, "foo/bar/baz")
	}
}

func TestNormalizePath_DotAndDotDot(t *testing.T) {
	got := NormalizePath("foo/./bar/../baz")
	if got != "foo/baz" {
		t.Errorf("got %q, want %q", got, "foo/baz")
	}
}

func TestNormalizePath_TrailingSlash(t *testing.T) {
	got := NormalizePath("foo/bar/")
	if got != "foo/bar" {
		t.Errorf("got %q, want %q", got, "foo/bar")
	}
}

func TestNormalizePath_Whitespace(t *testing.T) {
	got := NormalizePath("  foo/bar  ")
	if got != "foo/bar" {
		t.Errorf("got %q, want %q", got, "foo/bar")
	}
}

func TestNormalizePath_Empty(t *testing.T) {
	got := NormalizePath("")
	if got != "." {
		t.Errorf("got %q, want %q", got, ".")
	}
}

func TestNormalizePath_MixedSeparators(t *testing.T) {
	got := NormalizePath(`foo/bar\baz`)
	if got != "foo/bar/baz" {
		t.Errorf("got %q, want %q", got, "foo/bar/baz")
	}
}

func TestNormalizePath_RelativeDotSlash(t *testing.T) {
	got := NormalizePath("./foo/bar")
	if got != "foo/bar" {
		t.Errorf("got %q, want %q", got, "foo/bar")
	}
}

func TestNormalizePath_Absolute(t *testing.T) {
	got := NormalizePath("/tmp/idiom/foo")
	if got != "/tmp/idiom/foo" {
		t.Errorf("got %q, want %q", got, "/tmp/idiom/foo")
	}
}

func TestNormalizePathKey_Lowercases(t *testing.T) {
	got := NormalizePathKey("Foo/Bar/Baz")
	if got != "foo/bar/baz" {
		t.Errorf("got %q, want %q", got, "foo/bar/baz")
	}
}

func TestNormalizePathKey_NormalizesThenLowercases(t *testing.T) {
	got := NormalizePathKey(`  Foo\Bar/../Baz  `)
	if got != "foo/baz" {
		t.Errorf("got %q, want %q", got, "foo/baz")
	}
}
