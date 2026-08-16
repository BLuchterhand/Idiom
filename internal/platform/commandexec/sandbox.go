package commandexec

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type SandboxEnforcement string

const (
	SandboxDisabled SandboxEnforcement = "disabled"
	SandboxAdvisory SandboxEnforcement = "advisory"
	SandboxEnforced SandboxEnforcement = "enforced"
)

type SandboxNetworkMode string

const (
	SandboxNetworkDeny  SandboxNetworkMode = "deny"
	SandboxNetworkAllow SandboxNetworkMode = "allow"
)

// SandboxProfile describes OS-level restrictions for one admitted process.
// Logical workspace policy remains authoritative for whether the command may
// run at all.
type SandboxProfile struct {
	Enforcement SandboxEnforcement
	ReadRoots   []string
	WriteRoots  []string
	Network     SandboxNetworkMode
	TempDir     string
}

func (p SandboxProfile) Validate() error {
	switch p.Enforcement {
	case "", SandboxDisabled:
		return nil
	case SandboxAdvisory, SandboxEnforced:
	default:
		return fmt.Errorf("unsupported sandbox enforcement %q", p.Enforcement)
	}
	if p.Network != "" && p.Network != SandboxNetworkDeny && p.Network != SandboxNetworkAllow {
		return fmt.Errorf("unsupported sandbox network mode %q", p.Network)
	}
	for _, root := range append(append([]string(nil), p.ReadRoots...), p.WriteRoots...) {
		if strings.TrimSpace(root) == "" {
			return errors.New("sandbox roots must not be empty")
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("sandbox root must be absolute: %q", root)
		}
	}
	return nil
}

func (p SandboxProfile) networkMode() SandboxNetworkMode {
	if p.Network == "" {
		return SandboxNetworkDeny
	}
	return p.Network
}

// SandboxSupported reports whether the current platform has the adapter used
// for enforced profiles and whether its launcher is available.
func SandboxSupported() bool { return sandboxSupported() }

func sandboxCommandArgv(spec Spec, name string, args []string) (string, []string, error) {
	profile := spec.Sandbox
	if profile == nil || profile.Enforcement == "" || profile.Enforcement == SandboxDisabled {
		return name, args, nil
	}
	if err := profile.Validate(); err != nil {
		return "", nil, err
	}
	wrappedName, wrappedArgs, supported, err := platformSandboxCommand(spec, name, args)
	if err != nil {
		return "", nil, err
	}
	if supported {
		return wrappedName, wrappedArgs, nil
	}
	if profile.Enforcement == SandboxAdvisory {
		return name, args, nil
	}
	return "", nil, fmt.Errorf("enforced sandbox is unavailable on this platform")
}
