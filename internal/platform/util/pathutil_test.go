package util

import "testing"

func TestNormalizePath(t *testing.T) {
	tests := []struct{ input, want string }{
		{"", "."},
		{".", "."},
		{"  foo/bar  ", "foo/bar"},
		{"foo\\bar\\baz", "foo/bar/baz"},
		{"foo//bar", "foo/bar"},
		{"foo/bar/..", "foo"},
		{"./foo/bar", "foo/bar"},
		{"foo/./bar", "foo/bar"},
	}
	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeRelPath(t *testing.T) {
	got, err := NormalizeRelPath("/root", "foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/root/foo/bar" {
		t.Errorf("got %q, want %q", got, "/root/foo/bar")
	}

	_, err = NormalizeRelPath("/root", "/absolute")
	if err == nil {
		t.Error("expected error for absolute path")
	}
}
