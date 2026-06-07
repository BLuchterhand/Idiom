package indexsupport

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

func CleanIndexScope(scope string) string {
	scope = filepath.ToSlash(filepath.Clean(strings.TrimSpace(scope)))
	if scope == "" || scope == "." {
		return "."
	}
	return strings.TrimSuffix(scope, "/")
}

func PathInScope(path string, scope string) bool {
	scope = CleanIndexScope(scope)
	if scope == "." {
		return true
	}
	path = filepath.ToSlash(filepath.Clean(path))
	return path == scope || strings.HasPrefix(path, scope+"/")
}

func ShouldVisitDir(root string, path string, scope string) bool {
	scope = CleanIndexScope(scope)
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
	return PathInScope(rel, scope) || strings.HasPrefix(scope, rel+"/")
}

func ContextErr(opts IndexOptions) error {
	if opts.Context == nil {
		return nil
	}
	return opts.Context.Err()
}
