package hosttools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFirstAvailableUsesPathOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	firstDir := t.TempDir()
	secondDir := t.TempDir()
	writeExecutableHostTool(t, filepath.Join(secondDir, "tool-a"), "#!/bin/sh\nexit 0\n")
	writeExecutableHostTool(t, filepath.Join(firstDir, "tool-b"), "#!/bin/sh\nexit 0\n")
	writeExecutableHostTool(t, filepath.Join(secondDir, "tool-b"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", firstDir+string(os.PathListSeparator)+secondDir)

	got := FirstAvailable("missing-tool", "tool-b", "tool-a")
	want := filepath.Join(firstDir, "tool-b")
	if got != want {
		t.Fatalf("FirstAvailable = %q, want %q", got, want)
	}
}

func TestVersionOutputCapturesMergedOutputAndRejectsFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	stdoutTool := writeExecutableHostTool(t, filepath.Join(t.TempDir(), "stdout-tool"), "#!/bin/sh\nprintf '%s\\n' 'tool 1.0'\n")
	got, err := VersionOutput(t.Context(), stdoutTool, 1024)
	if err != nil {
		t.Fatalf("VersionOutput stdout: %v", err)
	}
	if strings.TrimSpace(got) != "tool 1.0" {
		t.Fatalf("VersionOutput stdout = %q", got)
	}

	stderrTool := writeExecutableHostTool(t, filepath.Join(t.TempDir(), "stderr-tool"), "#!/bin/sh\nprintf '%s\\n' 'tool 2.0' >&2\n")
	got, err = VersionOutput(t.Context(), stderrTool, 1024)
	if err != nil {
		t.Fatalf("VersionOutput stderr: %v", err)
	}
	if strings.TrimSpace(got) != "tool 2.0" {
		t.Fatalf("VersionOutput stderr = %q", got)
	}

	failingTool := writeExecutableHostTool(t, filepath.Join(t.TempDir(), "failing-tool"), "#!/bin/sh\nprintf '%s\\n' 'tool 3.0'\nexit 2\n")
	if _, err := VersionOutput(t.Context(), failingTool, 1024); err == nil {
		t.Fatalf("VersionOutput failing tool returned nil error")
	}
}

func TestVersionOutputContainsIsCaseInsensitive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses POSIX mode bits")
	}

	tool := writeExecutableHostTool(t, filepath.Join(t.TempDir(), "ctags"), "#!/bin/sh\nprintf '%s\\n' 'Universal Ctags 6.0'\n")
	if !VersionOutputContains(t.Context(), tool, "universal ctags") {
		t.Fatalf("VersionOutputContains did not match marker")
	}
	if VersionOutputContains(t.Context(), tool, "gnu grep") {
		t.Fatalf("VersionOutputContains matched absent marker")
	}
}

func writeExecutableHostTool(t *testing.T, path string, body string) string {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}

	return path
}
