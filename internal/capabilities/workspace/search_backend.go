package workspace

import (
	"context"
	"fmt"
	"runtime"

	"idiom/internal/platform/hosttools"
)

type contentSearchBackend struct {
	bin  string
	kind string
}

const (
	contentSearchRipgrep = "ripgrep"
	contentSearchGrep    = "grep"
)

func resolveContentSearchBackend() (contentSearchBackend, error) {
	for _, candidate := range contentSearchBackendCandidates() {
		switch candidate {
		case "rg":
			if path := hosttools.FirstAvailable("rg"); path != "" {
				return contentSearchBackend{bin: path, kind: contentSearchRipgrep}, nil
			}
		case "ggrep":
			if path := hosttools.FirstAvailable("ggrep"); path != "" {
				return contentSearchBackend{bin: path, kind: contentSearchGrep}, nil
			}
		case "grep":
			if path, ok := findGNUStyleGrep("grep"); ok {
				return contentSearchBackend{bin: path, kind: contentSearchGrep}, nil
			}
		}
	}

	return contentSearchBackend{}, fmt.Errorf("no supported content search backend found (rg or ggrep)")
}

func contentSearchBackendCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		// Homebrew installs GNU grep as `ggrep` on macOS; prefer that
		// before the BSD `/usr/bin/grep`, whose flags differ materially.
		return []string{"rg", "ggrep", "grep"}
	default:
		return []string{"rg", "grep", "ggrep"}
	}
}

func findGNUStyleGrep(name string) (string, bool) {
	path := hosttools.FirstAvailable(name)
	if path == "" {
		return "", false
	}

	if !hosttools.VersionOutputContains(context.Background(), path, "gnu grep") {
		return "", false
	}

	return path, true
}
