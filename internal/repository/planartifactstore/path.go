package planartifactstore

import (
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/safepath"
)

const storeRelativeDir = ".idiom/plans"

func StoreDir(root string) (string, string, error) {
	return safepath.UnderRoot(root, storeRelativeDir, "plan artifact directory", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
}

func StoreFilePath(root string, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("plan artifact filename is empty")
	}
	if filepath.Base(name) != name || strings.ContainsRune(name, 0) {
		return "", "", fmt.Errorf("plan artifact filename must be a base name")
	}

	return safepath.UnderRoot(root, filepath.ToSlash(filepath.Join(storeRelativeDir, name)), "plan artifact path", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
}

func StoreAssetPath(root string, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("plan artifact asset filename is empty")
	}
	if filepath.Base(name) != name || strings.ContainsRune(name, 0) {
		return "", "", fmt.Errorf("plan artifact asset filename must be a base name")
	}

	return safepath.UnderRoot(root, filepath.ToSlash(filepath.Join(storeRelativeDir, "assets", name)), "plan artifact asset path", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
}
