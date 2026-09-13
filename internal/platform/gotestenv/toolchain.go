// Package gotestenv keeps subprocesses in integration-test fixtures aligned
// with the Go toolchain running their parent suite.
package gotestenv

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// UseToolchain pins nested Go commands to the test process's compiler. Fixture
// modules often target an older Go version, and workspace login shells may
// replace the toolchain PATH injected by go test while retaining its GOROOT.
// Explicit selection prevents an older system compiler using a newer GOROOT.
func UseToolchain(t testing.TB) {
	t.Helper()
	t.Setenv("GOTOOLCHAIN", runtime.Version())

	// CLI fixtures replace HOME. Preserve the original module cache so Go
	// can reuse this toolchain instead of downloading a readonly SDK into a
	// temporary fixture directory that testing cannot remove afterwards.
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cache, err := exec.Command(goBinary, "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("resolve fixture Go module cache: %v", err)
	}
	t.Setenv("GOMODCACHE", strings.TrimSpace(string(cache)))
}
