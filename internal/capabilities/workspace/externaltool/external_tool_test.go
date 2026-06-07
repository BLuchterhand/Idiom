package externaltool

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSubstituteArgsDropsMissingWholeToken(t *testing.T) {
	got := SubstituteArgs("{optional}", map[string]any{})
	if len(got) != 0 {
		t.Fatalf("expected missing whole-token placeholder to drop, got %#v", got)
	}
}

func TestSubstituteArgsExpandsWholeTokenArray(t *testing.T) {
	input := map[string]any{
		"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := SubstituteArgs("{files}", input)
	want := []string{"native/src/a.cpp", "native/src/b.cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("array expansion mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsExpandsStringSlice(t *testing.T) {
	input := map[string]any{
		"files": []string{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := SubstituteArgs("{files}", input)
	want := []string{"native/src/a.cpp", "native/src/b.cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("string slice mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsDoesNotSplitEmbeddedArray(t *testing.T) {
	input := map[string]any{
		"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
	}
	got := SubstituteArgs("--files={files}", input)
	want := []string{`--files=["native/src/a.cpp","native/src/b.cpp"]`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded array mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestSubstituteArgsConditionals(t *testing.T) {
	input := map[string]any{"terse": true}
	got := SubstituteArgs("{?terse:--terse}", input)
	want := []string{"--terse"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("truthy conditional mismatch\nwant: %#v\n got: %#v", want, got)
	}

	got = SubstituteArgs("{?json:--json}", input)
	if len(got) != 0 {
		t.Fatalf("expected false conditional to drop, got %#v", got)
	}
}

func TestRegistryReadOnlyDiscoveryContract(t *testing.T) {
	reg, err := NewRegistry([]Tool{
		{Name: "lookup", Command: "true", ReadOnly: true, Discovery: true},
		{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: true},
		{Name: "format", Command: "true", ReadOnly: true, Discovery: false},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if !reg.IsReadOnlyDiscovery("lookup") {
		t.Fatalf("lookup should be classified as read-only discovery")
	}
	for _, name := range []string{"queue_run", "format", "missing"} {
		if reg.IsReadOnlyDiscovery(name) {
			t.Fatalf("%s should not be classified as read-only discovery", name)
		}
	}
	tool, ok := reg.Tool("lookup")
	if !ok || !tool.ReadOnly || !tool.Discovery {
		t.Fatalf("Tool lookup returned %#v, %v", tool, ok)
	}
}

func TestRegistryAvailabilityFiltersMissingCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	scriptPath := filepath.Join(root, "scripts", "ok.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	reg, err := NewRegistry([]Tool{
		{Name: "ok_relative", Command: "./scripts/ok.sh"},
		{Name: "missing_absolute", Command: filepath.Join(root, "missing.sh")},
		{Name: "missing_relative", Command: "./scripts/missing.sh"},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	names := reg.AvailableNames(root)
	if !hasString(names, "ok_relative") {
		t.Fatalf("expected available relative command, got %#v", names)
	}
	for _, missing := range []string{"missing_absolute", "missing_relative"} {
		if hasString(names, missing) {
			t.Fatalf("missing command %q should not be available: %#v", missing, names)
		}
		ok, reason := reg.Availability(missing, root)
		if ok || reason == "" {
			t.Fatalf("expected unavailable reason for %s, ok=%v reason=%q", missing, ok, reason)
		}
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
