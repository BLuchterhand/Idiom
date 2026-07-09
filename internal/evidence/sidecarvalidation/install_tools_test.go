package sidecarvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallToolsScriptsExposeSidecarOnlyLaunchers(t *testing.T) {
	root := repoRootForInstallToolTest(t)

	unixScript := readInstallToolFile(t, root, "install-tools.sh")
	for _, want := range []string{
		"idiom-tool-bin",
		"exec \"$DIR/idiom-tool-bin\" tool \"$@\"",
		"exec \"$DIR/idiom-tool-bin\" mcp serve --stdio --profile discovery \"$@\"",
		"use the Windows installer",
		"list --profile discovery",
	} {
		if !strings.Contains(unixScript, want) {
			t.Fatalf("install-tools.sh missing %q", want)
		}
	}

	if strings.Contains(unixScript, "${INSTALL_DIR}/idiom\"") {
		t.Fatal("tools installer must not expose the full idiom command name")
	}

	powerShellScript := readInstallToolFile(t, root, "install-tools.ps1")
	for _, want := range []string{
		"install.ps1",
		`"-Profile", "Tools"`,
		"Programs\\idiom-tools\\bin",
		"ReleaseUrlBase",
		"InstallerArgs",
	} {
		if !strings.Contains(powerShellScript, want) {
			t.Fatalf("install-tools.ps1 missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"Get-FileHash -Algorithm SHA256",
		"Expand-Archive",
		"windows_amd64.zip",
	} {
		if strings.Contains(powerShellScript, forbidden) {
			t.Fatalf("install-tools.ps1 should delegate install mechanics, still contains %q", forbidden)
		}
	}
}

func repoRootForInstallToolTest(t *testing.T) string {
	t.Helper()

	return sidecarValidationRepoRoot(t)
}

func readInstallToolFile(t *testing.T, root string, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	return string(data)
}
