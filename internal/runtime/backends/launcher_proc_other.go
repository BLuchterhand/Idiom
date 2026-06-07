//go:build !linux && !windows

package backends

import "idiom/internal/platform/failures"

// errProcUnavailable is the sentinel pidForPortViaProc returns on
// platforms where /proc isn't a kernel-managed surface. macOS and
// the BSDs fall through to the lsof-only path.
var errProcUnavailable = failures.New(failures.Unavailable, reasonBackendProcUnavailable, "backends: /proc not available on this platform")

// pidForPortViaProc is the no-op fallback for non-Linux Unixes.
// Returns errProcUnavailable so the lsof-first chain can either
// surface the right diagnostic or stop trying. The port is named
// `_` so vet doesn't flag it as unused; the signature has to match
// the Linux variant.
func pidForPortViaProc(_ int) (int, error) {
	return 0, errProcUnavailable
}
