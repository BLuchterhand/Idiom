package gdscript

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
)

func (idx *Index) FindReferences(pattern string, targetPath string, limit int) []Reference {
	if idx == nil {
		return nil
	}
	targetName, targetClass := splitPattern(pattern)
	resourcePath := ""
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(pattern)), ".gd") {
		resourcePath = normalizeTargetPath(idx.Root, pattern)
		targetName = filepath.Base(resourcePath)
	}
	targetPath = normalizeTargetPath(idx.Root, targetPath)
	if targetName == "" {
		return nil
	}
	if resourcePath != "" && !idx.hasPath(resourcePath) {
		return nil
	}
	if resourcePath == "" && !idx.hasTarget(targetName, targetClass, targetPath) {
		return nil
	}

	type referenceSite struct {
		path string
		line int
		kind string
	}
	// A resource load is indexed under both its file name and its class name; a path query reports each load once.
	seenSites := map[referenceSite]bool{}
	results := make([]Reference, 0, 16)
	for _, file := range idx.Files {
		for _, reference := range file.References {
			nameMatch := reference.Name == targetName
			if !nameMatch && targetClass != "" {
				nameMatch = reference.Name == targetClass && targetName == targetClass
			}
			pathMatch := resourcePath != "" && strings.EqualFold(filepath.ToSlash(filepath.Clean(reference.TargetPath)), resourcePath)
			if !nameMatch && !pathMatch {
				continue
			}
			if resourcePath != "" {
				site := referenceSite{path: reference.Path, line: reference.Line, kind: reference.Kind}
				if seenSites[site] {
					continue
				}
				seenSites[site] = true
			}
			results = append(results, reference)
		}
	}

	slices.SortFunc(results, func(a, b Reference) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
	results = uniqueReferences(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

func (idx *Index) hasPath(path string) bool {
	for _, file := range idx.Files {
		if strings.EqualFold(filepath.ToSlash(filepath.Clean(file.Path)), path) {
			return true
		}
	}
	return false
}

func (idx *Index) FindCallsInto(pattern string, targetPath string, limit int) CallsResult {
	if idx == nil {
		return CallsResult{}
	}
	targetName, targetClass := splitPattern(pattern)
	targetPath = normalizeTargetPath(idx.Root, targetPath)
	if targetName == "" {
		return CallsResult{}
	}

	classes := make(map[string]struct{})
	for _, file := range idx.Files {
		if targetPath != "" && !pathMatches(file.Path, targetPath) {
			continue
		}
		if targetClass != "" && file.ClassName != targetClass {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Name == targetName {
				classes[file.ClassName] = struct{}{}
			}
		}
	}
	if len(classes) == 0 {
		return CallsResult{}
	}

	results := CallsResult{Resolved: make([]Call, 0, 16), UnresolvedCandidates: make([]Call, 0, 4)}
	for _, file := range idx.Files {
		for _, call := range file.Calls {
			if call.Target != targetName {
				continue
			}
			if call.TargetClass == "" {
				results.UnresolvedCandidates = append(results.UnresolvedCandidates, call)
				continue
			}
			if _, ok := classes[call.TargetClass]; ok {
				results.Resolved = append(results.Resolved, call)
			}
		}
	}

	sortCalls := func(calls []Call) {
		slices.SortFunc(calls, func(a, b Call) int {
			if c := cmp.Compare(a.Caller.Path, b.Caller.Path); c != 0 {
				return c
			}
			if c := cmp.Compare(a.Line, b.Line); c != 0 {
				return c
			}
			return cmp.Compare(a.Target, b.Target)
		})
	}
	sortCalls(results.Resolved)
	sortCalls(results.UnresolvedCandidates)
	results.Resolved = uniqueCalls(results.Resolved)
	results.UnresolvedCandidates = uniqueCalls(results.UnresolvedCandidates)
	if limit > 0 {
		if len(results.Resolved) > limit {
			results.Resolved = results.Resolved[:limit]
		}
		if len(results.UnresolvedCandidates) > limit {
			results.UnresolvedCandidates = results.UnresolvedCandidates[:limit]
		}
	}
	return results
}

func (idx *Index) hasTarget(name string, className string, path string) bool {
	for _, file := range idx.Files {
		if path != "" && !pathMatches(file.Path, path) {
			continue
		}
		if className != "" && file.ClassName != className {
			continue
		}
		for _, symbol := range file.Symbols {
			if symbol.Name == name {
				return true
			}
		}
		if file.ClassName == name {
			return true
		}
	}
	return false
}

func splitPattern(pattern string) (string, string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", ""
	}
	if strings.HasSuffix(strings.ToLower(pattern), ".gd") {
		return filepath.Base(pattern), ""
	}
	if dot := strings.LastIndex(pattern, "."); dot >= 0 {
		return strings.TrimSpace(pattern[dot+1:]), strings.TrimSpace(pattern[:dot])
	}
	return pattern, ""
}

func normalizeTargetPath(root string, target string) string {
	target = strings.TrimSpace(target)
	if target == "" || target == "." {
		return ""
	}
	if filepath.IsAbs(target) {
		if rel, err := filepath.Rel(root, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			target = rel
		}
	}
	target = filepath.ToSlash(filepath.Clean(target))
	return strings.TrimPrefix(target, "./")
}

func pathMatches(path string, filter string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	return path == filter || strings.HasPrefix(path, strings.TrimSuffix(filter, "/")+"/")
}

func uniqueReferences(values []Reference) []Reference {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	last := Reference{}
	for i, value := range values {
		if i > 0 && value.Path == last.Path && value.Line == last.Line && value.Name == last.Name && value.Kind == last.Kind && value.TargetPath == last.TargetPath {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}

func uniqueCalls(values []Call) []Call {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	last := Call{}
	for i, value := range values {
		if i > 0 && value.Caller.Path == last.Caller.Path && value.Caller.Name == last.Caller.Name && value.Line == last.Line && value.Target == last.Target && value.Kind == last.Kind {
			continue
		}
		out = append(out, value)
		last = value
	}
	return out
}
