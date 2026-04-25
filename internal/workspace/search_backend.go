package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
			if path := firstAvailableBinary("rg"); path != "" {
				return contentSearchBackend{bin: path, kind: contentSearchRipgrep}, nil
			}
		case "ggrep":
			if path := firstAvailableBinary("ggrep"); path != "" {
				return contentSearchBackend{bin: path, kind: contentSearchGrep}, nil
			}
		case "grep":
			if path, ok := findGNUStyleGrep("grep"); ok {
				return contentSearchBackend{bin: path, kind: contentSearchGrep}, nil
			}
		}
	}
	return contentSearchBackend{}, fmt.Errorf("no supported content search backend found; install ripgrep (`rg`) or GNU grep (`ggrep`)")
}

func hasContentSearchBackend() bool {
	_, err := resolveContentSearchBackend()
	return err == nil
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
	path, err := exec.LookPath(name)
	if err != nil || path == "" {
		return "", false
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", false
	}
	if !strings.Contains(strings.ToLower(string(output)), "gnu grep") {
		return "", false
	}
	return path, true
}

func (b contentSearchBackend) args(req ToolRequest, excludes []string) []string {
	switch b.kind {
	case contentSearchGrep:
		return grepContentSearchArgs(req, excludes)
	default:
		return ripgrepContentSearchArgs(req, excludes)
	}
}

func ripgrepContentSearchArgs(req ToolRequest, excludes []string) []string {
	args := []string{"-n", "-H", "--hidden", "--no-heading", "--color", "never"}
	if req.Glob != "" {
		args = append(args, "-g", req.Glob)
	}
	for _, glob := range excludes {
		args = append(args, "-g", glob)
	}
	args = append(args, strings.TrimSpace(req.Pattern), defaultString(req.Path, "."))
	return args
}

func grepContentSearchArgs(req ToolRequest, excludes []string) []string {
	args := []string{"-R", "-n", "-H", "-I", "--binary-files=without-match", "--color=never"}
	if glob := strings.TrimSpace(req.Glob); glob != "" {
		args = append(args, "--include="+glob)
	}
	args = append(args, grepExcludeArgs(excludes)...)
	args = append(args, "-E", strings.TrimSpace(req.Pattern), defaultString(req.Path, "."))
	return args
}

func grepExcludeArgs(excludes []string) []string {
	seenDir := map[string]struct{}{}
	seenFile := map[string]struct{}{}
	args := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		pattern := strings.TrimPrefix(strings.TrimSpace(exclude), "!")
		pattern = filepath.ToSlash(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		switch {
		case strings.HasSuffix(pattern, "/**"):
			dir := strings.TrimSuffix(pattern, "/**")
			base := filepath.Base(dir)
			if base == "" || base == "." || base == "/" {
				continue
			}
			if _, ok := seenDir[base]; ok {
				continue
			}
			seenDir[base] = struct{}{}
			args = append(args, "--exclude-dir="+base)
		default:
			base := filepath.Base(pattern)
			if base == "" || base == "." || base == "/" {
				continue
			}
			if _, ok := seenFile[base]; ok {
				continue
			}
			seenFile[base] = struct{}{}
			args = append(args, "--exclude="+base)
		}
	}
	return args
}
