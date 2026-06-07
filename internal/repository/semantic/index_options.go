package semantic

import (
	"context"
	"path/filepath"
	"strings"
)

type IndexOptions struct {
	Scope    string
	MaxFiles int
	Context  context.Context
}

func cleanIndexScope(scope string) string {
	scope = filepath.ToSlash(filepath.Clean(strings.TrimSpace(scope)))
	if scope == "" || scope == "." {
		return "."
	}
	return strings.TrimSuffix(scope, "/")
}

func pathInIndexScope(path string, scope string) bool {
	scope = cleanIndexScope(scope)
	if scope == "." {
		return true
	}
	path = filepath.ToSlash(filepath.Clean(path))
	return path == scope || strings.HasPrefix(path, scope+"/")
}

func shouldVisitIndexDir(root string, path string, scope string) bool {
	scope = cleanIndexScope(scope)
	if scope == "." {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return true
	}
	return pathInIndexScope(rel, scope) || strings.HasPrefix(scope, rel+"/")
}

func indexContextErr(opts IndexOptions) error {
	if opts.Context == nil {
		return nil
	}
	return opts.Context.Err()
}
