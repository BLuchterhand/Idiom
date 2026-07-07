package atomintake

import (
	"context"
	"strings"

	"idiom/internal/platform/gitstate"
)

func dirtyAtomScopePaths(projectRoot string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err := gitstate.Output(context.Background(), projectRoot, args...)

	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}

	seen := map[string]bool{}

	var dirty []string

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		path := ParseGitPorcelainPath(line)
		if path == "" || seen[path] {
			continue
		}

		seen[path] = true
		dirty = append(dirty, path)
	}

	return dirty
}

func ParseGitPorcelainPath(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return ""
	}

	path := ""
	if len(line) >= 4 && line[2] == ' ' {
		path = strings.TrimSpace(line[3:])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ""
		}

		path = fields[len(fields)-1]
	}

	if idx := strings.LastIndex(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}

	path = strings.Trim(path, `"`)

	return path
}
