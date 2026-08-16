//go:build !darwin && !linux

package commandexec

func sandboxSupported() bool { return false }

func platformSandboxCommand(Spec, string, []string) (string, []string, bool, error) {
	return "", nil, false, nil
}
