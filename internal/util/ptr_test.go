package util

import "testing"

func TestPtr(t *testing.T) {
	val := 42
	p := Ptr(val)
	if p == nil {
		t.Fatal("Ptr returned nil")
	}
	if *p != val {
		t.Errorf("Ptr(%d) = %d, want %d", val, *p, val)
	}
}

func TestPtrReturnsNewAllocation(t *testing.T) {
	val := 10
	p := Ptr(val)
	*p = 20
	if val != 10 {
		t.Errorf("modifying Ptr result changed original: original=%d, want 10", val)
	}
}

func TestPtrString(t *testing.T) {
	s := "hello"
	p := Ptr(s)
	if *p != s {
		t.Errorf("Ptr(%q) = %q", s, *p)
	}
}

func TestPtrEmptyStruct(t *testing.T) {
	type empty struct{}
	e := empty{}
	p := Ptr(e)
	if p == nil {
		t.Fatal("Ptr(empty) returned nil")
	}
}

func TestPtrBool(t *testing.T) {
	for _, v := range []bool{true, false} {
		p := Ptr(v)
		if *p != v {
			t.Errorf("Ptr(%v) = %v", v, *p)
		}
	}
}

func TestClonePtr(t *testing.T) {
	original := 42
	p := &original
	cloned := ClonePtr(p)
	if cloned == nil {
		t.Fatal("ClonePtr returned nil")
	}
	if *cloned != *p {
		t.Errorf("ClonePtr = %d, want %d", *cloned, *p)
	}
}

func TestClonePtrNil(t *testing.T) {
	var p *int
	cloned := ClonePtr(p)
	if cloned != nil {
		t.Errorf("ClonePtr(nil) = %v, want nil", cloned)
	}
}

func TestClonePtrIndependence(t *testing.T) {
	val := 10
	p := &val
	cloned := ClonePtr(p)

	*cloned = 20
	if *p != 10 {
		t.Errorf("modifying ClonePtr result changed original: original=%d, want 10", *p)
	}

	*p = 30
	if *cloned != 20 {
		t.Errorf("modifying original changed ClonePtr result: clone=%d, want 20", *cloned)
	}
}

func TestClonePtrString(t *testing.T) {
	s := "hello"
	p := &s
	cloned := ClonePtr(p)
	if *cloned != s {
		t.Errorf("ClonePtr string = %q, want %q", *cloned, s)
	}
}

func TestClonePtrNilTyped(t *testing.T) {
	var s *string
	cloned := ClonePtr(s)
	if cloned != nil {
		t.Error("expected nil for nil *string input")
	}
}

func BenchmarkPtr(b *testing.B) {
	for b.Loop() {
		_ = Ptr(42)
	}
}

func BenchmarkClonePtr(b *testing.B) {
	v := 42
	p := &v
	for b.Loop() {
		_ = ClonePtr(p)
	}
}

func BenchmarkClonePtrNil(b *testing.B) {
	var p *int
	for b.Loop() {
		_ = ClonePtr(p)
	}
}
