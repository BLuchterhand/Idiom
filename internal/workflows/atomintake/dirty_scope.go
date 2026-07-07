package atomintake

import (
	"context"

	"idiom/internal/platform/gitstate"
)

func dirtyAtomScopePaths(projectRoot string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err := gitstate.Output(context.Background(), projectRoot, args...)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var dirty []string

	for _, line := range gitstate.PorcelainLines(out) {
		path := gitstate.PathFromPorcelainLine(line)
		if path == "" || seen[path] {
			continue
		}

		seen[path] = true
		dirty = append(dirty, path)
	}

	return dirty
}
