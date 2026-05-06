package workspace

import (
	"reflect"
	"testing"
)

func TestSubstituteArgsDropsMissingWholeToken(t *testing.T) {
	got := substituteArgs("{optional}", map[string]any{})
	if len(got) != 0 {
		t.Fatalf("expected missing whole-token placeholder to drop, got %#v", got)
	}
}

func TestSubstituteArgsExpandsWholeTokenArray(t *testing.T) {
	input := map[string]any{
		"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := substituteArgs("{files}", input)
	want := []string{"native/src/a.cpp", "native/src/b.cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("array expansion mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsExpandsStringSlice(t *testing.T) {
	input := map[string]any{
		"files": []string{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := substituteArgs("{files}", input)
	want := []string{"native/src/a.cpp", "native/src/b.cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("string slice expansion mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsDoesNotSplitEmbeddedArray(t *testing.T) {
	input := map[string]any{
		"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := substituteArgs("--files={files}", input)
	want := []string{`--files=["native/src/a.cpp","native/src/b.cpp"]`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded array expansion mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsConditionals(t *testing.T) {
	input := map[string]any{"terse": true}
	got := substituteArgs("{?terse:--terse}", input)
	want := []string{"--terse"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("truthy conditional mismatch\nwant: %#v\n got: %#v", want, got)
	}

	got = substituteArgs("{?json:--json}", input)
	if len(got) != 0 {
		t.Fatalf("expected false conditional to drop, got %#v", got)
	}
}
