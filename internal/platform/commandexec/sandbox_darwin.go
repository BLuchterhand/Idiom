//go:build darwin

package commandexec

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func sandboxSupported() bool {
	_, err := exec.LookPath("sandbox-exec")
	return err == nil
}

func platformSandboxCommand(spec Spec, name string, args []string) (string, []string, bool, error) {
	if !sandboxSupported() {
		return "", nil, false, nil
	}
	profile, err := seatbeltProfile(spec.Sandbox, name)
	if err != nil {
		return "", nil, false, err
	}
	wrapped := []string{"-p", profile, name}
	return "sandbox-exec", append(wrapped, args...), true, nil
}

func seatbeltProfile(profile *SandboxProfile, name string) (string, error) {
	if profile == nil {
		return "", fmt.Errorf("sandbox profile is required")
	}
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-fork)\n(allow process-exec)\n(allow signal (target self))\n")
	b.WriteString("(allow sysctl-read)\n(allow file-read-metadata)\n")
	// macOS sandbox-exec aborts rather than returning a normal process error
	// when a dynamically linked tool lacks one of its transitive runtime reads.
	// Keep the runtime read baseline broad and enforce declared write roots and
	// network policy here; ReadRoots remain metadata for the caller until a
	// platform-native profile can express a complete runtime closure safely.
	b.WriteString("(allow file-read* (subpath \"/\"))\n")
	if filepath.IsAbs(name) {
		b.WriteString("(allow file-read* (literal ")
		b.WriteString(seatbeltString(name))
		b.WriteString(") )\n")
	}
	for _, root := range profile.ReadRoots {
		b.WriteString("(allow file-read* (subpath ")
		b.WriteString(seatbeltString(root))
		b.WriteString(") )\n")
	}
	for _, root := range profile.WriteRoots {
		b.WriteString("(allow file-write* (subpath ")
		b.WriteString(seatbeltString(root))
		b.WriteString(") )\n")
	}
	if profile.networkMode() == SandboxNetworkAllow {
		b.WriteString("(allow network*)\n")
	}
	return b.String(), nil
}

func seatbeltString(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
