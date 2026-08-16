//go:build linux

package commandexec

import (
	"os/exec"
	"path/filepath"
)

func sandboxSupported() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func platformSandboxCommand(spec Spec, name string, args []string) (string, []string, bool, error) {
	if !sandboxSupported() {
		return "", nil, false, nil
	}
	wrapped := []string{"--die-with-parent", "--new-session", "--proc", "/proc", "--dev", "/dev"}
	if spec.Sandbox.networkMode() == SandboxNetworkDeny {
		wrapped = append(wrapped, "--unshare-net")
	}
	for _, root := range spec.Sandbox.ReadRoots {
		wrapped = append(wrapped, "--ro-bind", root, root)
	}
	for _, root := range spec.Sandbox.WriteRoots {
		wrapped = append(wrapped, "--bind", root, root)
	}
	if spec.Dir != "" {
		wrapped = append(wrapped, "--chdir", spec.Dir)
	}
	for _, root := range []string{"/bin", "/usr", "/lib", "/lib64"} {
		if _, err := filepath.Abs(root); err == nil {
			wrapped = append(wrapped, "--ro-bind", root, root)
		}
	}
	wrapped = append(wrapped, "--", name)
	return "bwrap", append(wrapped, args...), true, nil
}
