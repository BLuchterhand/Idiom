package atomintake

import (
	"os"
	"path/filepath"
	"strings"

	atomintakecontract "idiom/internal/contracts/atomintake"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
)

func classifyAtomScopePaths(projectRoot string, paths []string) (existing []string, missing []string, invalid []string, invalidReasons map[string]string) {
	for _, path := range paths {
		scopePath, fullPath, ok := atomScopePath(projectRoot, path)
		if !ok {
			invalidReasons = atomRecordInvalidScopeReason(invalidReasons, path, atomintakecontract.InvalidScopeOutsideProjectRoot)
			invalid = append(invalid, path)

			continue
		}

		if _, err := os.Stat(fullPath); err == nil {
			existing = append(existing, scopePath)
		} else if os.IsNotExist(err) {
			if atomScopeMissingPathLooksLikeBareShorthand(scopePath) {
				invalidReasons = atomRecordInvalidScopeReason(invalidReasons, path, atomintakecontract.InvalidScopeAmbiguousBareShorthand)
				invalid = append(invalid, path)

				continue
			}

			missing = append(missing, scopePath)
		}
	}

	return existing, missing, invalid, invalidReasons
}

func atomRecordInvalidScopeReason(reasons map[string]string, path, reason string) map[string]string {
	if reasons == nil {
		reasons = map[string]string{}
	}

	reasons[path] = reason

	return reasons
}

func atomInvalidScopeSummary(invalidReasons map[string]string) string {
	hasOutsideRoot := atomInvalidScopeReasonsInclude(invalidReasons, atomintakecontract.InvalidScopeOutsideProjectRoot)
	hasBareShorthand := atomInvalidScopeReasonsInclude(invalidReasons, atomintakecontract.InvalidScopeAmbiguousBareShorthand)

	switch {
	case hasOutsideRoot && hasBareShorthand:
		return "spec scope includes paths outside the project root and ambiguous bare shorthand paths that could imply repository-root writes"
	case hasBareShorthand:
		return "spec scope includes ambiguous bare shorthand paths that could imply repository-root writes"
	default:
		return "spec scope includes paths outside the project root"
	}
}

func atomInvalidScopeNextAction(invalidReasons map[string]string) string {
	hasOutsideRoot := atomInvalidScopeReasonsInclude(invalidReasons, atomintakecontract.InvalidScopeOutsideProjectRoot)
	hasBareShorthand := atomInvalidScopeReasonsInclude(invalidReasons, atomintakecontract.InvalidScopeAmbiguousBareShorthand)

	switch {
	case hasOutsideRoot && hasBareShorthand:
		return "rebase the atom/spec so every scoped path is explicit, repository-relative, and inside the workspace"
	case hasBareShorthand:
		return "rebase the atom/spec so new scoped files use explicit repository-relative paths instead of bare basenames"
	default:
		return "rebase the atom/spec so every scoped path is repository-relative and inside the workspace"
	}
}

func atomInvalidScopeReasonsInclude(invalidReasons map[string]string, reason string) bool {
	for _, got := range invalidReasons {
		if got == reason {
			return true
		}
	}

	return false
}

func atomScopeMissingPathLooksLikeBareShorthand(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return false
	}

	normalized := filepath.ToSlash(path)
	if strings.Contains(normalized, "/") {
		return false
	}

	return !pathparse.KnownFileBasename(normalized)
}
func atomScopeFullPath(projectRoot, path string) (string, bool) {
	_, fullPath, ok := atomScopePath(projectRoot, path)
	return fullPath, ok
}

func atomScopePath(projectRoot, path string) (string, string, bool) {
	if filepath.IsAbs(path) {
		if !pathWithinRoot(projectRoot, path) {
			return "", "", false
		}

		root, err := filepath.Abs(projectRoot)
		if err != nil {
			return "", "", false
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", "", false
		}

		scopePath := plancontract.NormalizeScopePath(filepath.ToSlash(rel))
		if scopePath == "" {
			return "", "", false
		}

		return scopePath, filepath.Join(projectRoot, filepath.FromSlash(scopePath)), true
	}

	scopePath := plancontract.NormalizeScopePath(path)
	if scopePath == "" {
		return "", "", false
	}

	fullPath := filepath.Join(projectRoot, filepath.FromSlash(scopePath))
	if !pathWithinRoot(projectRoot, fullPath) {
		return "", "", false
	}

	return scopePath, fullPath, true
}

func pathWithinRoot(projectRoot, path string) bool {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return false
	}

	fullPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	if !pathWithinRootLexical(root, fullPath) {
		return false
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}

	realPath, err := resolveExistingPathPrefix(fullPath)
	if err != nil {
		return false
	}

	return pathWithinRootLexical(realRoot, realPath)
}

func pathWithinRootLexical(projectRoot, path string) bool {
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}

	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

func resolveExistingPathPrefix(path string) (string, error) {
	fullPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	current := fullPath

	var missingSuffix []string

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missingSuffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missingSuffix[i])
			}

			return filepath.Abs(resolved)
		}

		if linkTarget, ok := unresolvedSymlinkTarget(current); ok {
			for i := len(missingSuffix) - 1; i >= 0; i-- {
				linkTarget = filepath.Join(linkTarget, missingSuffix[i])
			}

			return filepath.Abs(linkTarget)
		}

		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}

		missingSuffix = append(missingSuffix, filepath.Base(current))
		current = parent
	}
}

func unresolvedSymlinkTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}

	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}

	return target, true
}
