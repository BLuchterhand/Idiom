package semantic

import (
	"cmp"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

type hasPath interface{ GetPath() string }

type ScoredEntry[T any] struct {
	File  T
	Score int
}

func RankFiles[T any](files []T, scoreFn func(T) int, limit int) []T {
	scored := make([]ScoredEntry[T], 0, len(files))
	for _, file := range files {
		score := scoreFn(file)
		if score <= 0 {
			continue
		}
		scored = append(scored, ScoredEntry[T]{File: file, Score: score})
	}
	slices.SortFunc(scored, func(a, b ScoredEntry[T]) int {
		if c := -cmp.Compare(a.Score, b.Score); c != 0 {
			return c
		}
		return cmp.Compare(AnyPath(a.File), AnyPath(b.File))
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]T, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.File)
	}
	return out
}

func UniqueRankedPaths[T any](files []T, limit int) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(files))
	for _, file := range files {
		p := AnyPath(file)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func FindFilesExactPartial[T any](files []T, pattern string) []string {
	pattern = normalizeSearchPath(pattern)
	if pattern == "" {
		return nil
	}
	exact := make([]string, 0)
	partial := make([]string, 0)
	for _, file := range files {
		p := AnyPath(file)
		full := normalizeSearchPath(p)
		base := path.Base(full)
		switch {
		case base == pattern || full == pattern:
			exact = append(exact, p)
		case strings.Contains(base, pattern) || strings.Contains(full, pattern):
			partial = append(partial, p)
		}
	}
	if len(exact) > 0 {
		slices.Sort(exact)
		return exact
	}
	slices.Sort(partial)
	return partial
}

func normalizeSearchPath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "/")
	value = filepath.ToSlash(value)
	return strings.ToLower(value)
}

func AnyPath(v any) string {
	switch f := v.(type) {
	case GoFile:
		return f.Path
	case CppFile:
		return f.Path
	case JavaFile:
		return f.Path
	case RustFile:
		return f.Path
	case TsFile:
		return f.Path
	case PythonFile:
		return f.Path
	case string:
		return f
	default:
		return ""
	}
}
