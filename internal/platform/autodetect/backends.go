// Package autodetect discovers local-LLM backends and model files
// already installed on the host. It is the foundation for `idiom
// doctor` (Phase 11 — preflight diagnostic) and `idiom init --auto`
// (Phase 10 — zero-config init), so a fresh user runs neither command
// against an empty config and gets actionable output instead of
// silence.
//
// The package is read-only and side-effect-free: it scans $PATH and
// well-known directories without modifying state. Callers compose the
// results into config files, doctor reports, or init scaffolding.
package autodetect

import (
	"os/exec"
	"slices"
)

// Backend describes one local-LLM runner that's installed on this host.
type Backend struct {
	// Kind is a short identifier: "mlx", "llamacpp", "ollama".
	Kind string
	// BinaryPath is the absolute path to the runner binary as resolved
	// via $PATH.
	BinaryPath string
	// BinaryName is the basename ("mlx_lm.server", etc.) — convenient
	// for display without re-deriving from BinaryPath.
	BinaryName string
}

// backendCandidates lists every runner autodetect knows about, in
// preference order: MLX (Apple Silicon native, fastest on darwin/arm64),
// llama.cpp (portable, works everywhere), Ollama (turnkey but adds a
// model-management layer). Order is stable so callers who pick "the
// first detected" get the same answer across runs.
var backendCandidates = []struct {
	kind string
	bin  string
}{
	{"mlx", "mlx_lm.server"},
	{"llamacpp", "llama-server"},
	{"ollama", "ollama"},
}

// DetectBackends returns every known backend runner present on $PATH,
// in preference order. Returns an empty slice (not nil) when nothing
// is installed — callers can range over it without nil-checks.
func DetectBackends() []Backend {
	found := make([]Backend, 0, len(backendCandidates))
	for _, c := range backendCandidates {
		path, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}

		found = append(found, Backend{Kind: c.kind, BinaryPath: path, BinaryName: c.bin})
	}

	return found
}

// KnownBackendNames returns every backend binary name autodetect
// considers, regardless of whether it's installed. Useful for doctor
// output ("checked for: mlx_lm.server, llama-server, ollama") and for
// tests asserting the candidate list hasn't drifted.
func KnownBackendNames() []string {
	out := make([]string, 0, len(backendCandidates))
	for _, c := range backendCandidates {
		out = append(out, c.bin)
	}

	slices.Sort(out)

	return out
}
