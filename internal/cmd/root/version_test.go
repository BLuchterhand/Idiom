package root
import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// TestRunVersionMainPrintsVersion verifies the default unset-ldflags
// build prints "version: dev" — the development sentinel — plus the
// runtime version line. A go-install or go-build user without
// goreleaser's -ldflags should still get a recognizable answer.
func TestRunVersionMainPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := runVersionMain(nil, &stdout, &stderr); got != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%s)", got, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "version: ") {
		t.Fatalf("output missing 'version: ' line; got: %s", out)
	}
	if !strings.Contains(out, "go: ") {
		t.Fatalf("output missing 'go: ' line; got: %s", out)
	}
	if !strings.Contains(out, runtime.Version()) {
		t.Fatalf("output missing runtime.Version() %q; got: %s", runtime.Version(), out)
	}
	if !strings.Contains(out, "os/arch: ") {
		t.Fatalf("output missing 'os/arch: ' line; got: %s", out)
	}
}

// TestRunVersionMainHonorsLdflagsInjection verifies that the package-
// level `version` variable is what gets printed. goreleaser injects
// this at build time via -ldflags '-X main.version=...', so the
// build-time override path is the contract this test guards.
func TestRunVersionMainHonorsLdflagsInjection(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	version = "v9.9.9-test"

	var stdout, stderr bytes.Buffer
	if got := runVersionMain(nil, &stdout, &stderr); got != 0 {
		t.Fatalf("expected exit 0, got %d", got)
	}
	if !strings.Contains(stdout.String(), "version: v9.9.9-test") {
		t.Fatalf("expected version override to surface in output; got: %s", stdout.String())
	}
}
