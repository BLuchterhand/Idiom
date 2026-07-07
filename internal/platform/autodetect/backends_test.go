package autodetect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDetectBackendsFindsStubBinary stages a fake mlx_lm.server binary
// in a temp dir, prepends it to PATH, and verifies DetectBackends finds
// it. The stub doesn't need to do anything; host tool lookup only needs
// a regular file with the executable bit set on Unix, so we set it
// explicitly. On Windows we skip because PATH probing has different
// extension-resolution rules and idiom isn't shipping windows-host
// support for backends.
func TestDetectBackendsFindsStubBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backend autodetect is unix-host today; windows hosts run idiom against a remote backend")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "mlx_lm.server")

	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("stage stub: %v", err)
	}

	t.Setenv("PATH", dir)

	backends := DetectBackends()
	if len(backends) == 0 {
		t.Fatalf("expected DetectBackends to find the staged stub; got none")
	}

	var got *Backend

	for i := range backends {
		if backends[i].BinaryName == "mlx_lm.server" {
			got = &backends[i]
			break
		}
	}

	if got == nil {
		t.Fatalf("expected mlx_lm.server in detected list; got %+v", backends)
	}

	if got.Kind != "mlx" {
		t.Fatalf("expected kind=mlx for mlx_lm.server, got %q", got.Kind)
	}

	if got.BinaryPath != stub {
		t.Fatalf("expected BinaryPath=%q, got %q", stub, got.BinaryPath)
	}
}

// TestDetectBackendsEmptyPath verifies the function returns an empty
// (non-nil) slice when nothing matches, so callers can range without
// nil-checks.
func TestDetectBackendsEmptyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see TestDetectBackendsFindsStubBinary")
	}

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	backends := DetectBackends()
	if backends == nil {
		t.Fatalf("expected non-nil empty slice from DetectBackends, got nil")
	}

	if len(backends) != 0 {
		t.Fatalf("expected zero backends in empty PATH, got %+v", backends)
	}
}

// TestKnownBackendNamesContractStable guards against drift in the
// candidate list — adding a new backend type should be a deliberate
// change, not silent. Doctor output and project README both reference
// these names.
func TestKnownBackendNamesContractStable(t *testing.T) {
	got := KnownBackendNames()
	want := map[string]bool{
		"mlx_lm.server": true,
		"llama-server":  true,
		"ollama":        true,
	}

	if len(got) != len(want) {
		t.Fatalf("KnownBackendNames count drift: got %d, want %d (%v)", len(got), len(want), got)
	}

	for _, name := range got {
		if !want[name] {
			t.Fatalf("KnownBackendNames returned unexpected %q; want one of %v", name, want)
		}
	}
}

// TestDetectBackendsPreservesPreferenceOrder verifies multi-backend
// installs surface in MLX → llamacpp → ollama order. Project init
// picks "the first detected" as default executor; the order is the
// contract.
func TestDetectBackendsPreservesPreferenceOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see TestDetectBackendsFindsStubBinary")
	}

	dir := t.TempDir()
	for _, name := range []string{"mlx_lm.server", "llama-server", "ollama"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}

	t.Setenv("PATH", dir)

	backends := DetectBackends()
	if len(backends) != 3 {
		t.Fatalf("expected all three backends detected; got %d (%+v)", len(backends), backends)
	}

	wantOrder := []string{"mlx_lm.server", "llama-server", "ollama"}
	for i, b := range backends {
		if b.BinaryName != wantOrder[i] {
			t.Fatalf("preference order violated at index %d: got %q, want %q", i, b.BinaryName, wantOrder[i])
		}
	}
}
