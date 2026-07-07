package implementationdispatch

import (
	"path/filepath"
	"strings"

	"idiom/internal/contracts/pathparse"
	"idiom/internal/platform/safepath"
)

func WorkspaceRoot(root string, cwd string) string {
	if root := strings.TrimSpace(root); root != "" {
		return root
	}

	return strings.TrimSpace(cwd)
}

func WorkspaceFile(root string, rel string) (string, bool) {
	if strings.TrimSpace(rel) == "" || filepath.IsAbs(rel) {
		return "", false
	}

	abs, _, err := safepath.UnderRoot(root, rel, "implementation dispatch workspace file", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
	})
	if err != nil {
		return "", false
	}

	return abs, true
}

func LooksLikePlanDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".mdx", ".markdown":
		return true
	default:
		return false
	}
}

func IsRefineScratchpadPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if normalized == "" || normalized == "." {
		return false
	}

	if !strings.HasPrefix(normalized, ".idiom/refine/") && !strings.Contains(normalized, "/.idiom/refine/") {
		return false
	}

	if strings.Contains(normalized, "/iter-") {
		return true
	}

	base := strings.ToLower(filepath.Base(normalized))

	return strings.HasPrefix(base, "meta-") || base == "draft.md" || base == "review.md" || base == "patch.md"
}

func IsAtomizedPlanDocumentPath(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.HasPrefix(normalized, ".idiom/atoms/")
}

func LooksLikeConcreteFile(path string) bool {
	if pathparse.HasWorkspaceFileExtension(path) {
		return true
	}

	switch filepath.Base(path) {
	case "Makefile", "go.mod", "go.sum", "README.md":
		return true
	default:
		return false
	}
}
