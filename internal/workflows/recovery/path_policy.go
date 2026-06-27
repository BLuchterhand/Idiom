package recovery

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"idiom/internal/platform/safepath"
)

// SafeSiblingReconciliationPath returns the default reconciliation.json path
// next to packetPath when that path stays inside root and does not cross
// protected or symlinked workspace locations.
func SafeSiblingReconciliationPath(root string, packetPath string) (string, string, bool) {
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", "", false
	}
	absRoot = filepath.Clean(absRoot)

	absPacketPath, err := filepath.Abs(strings.TrimSpace(packetPath))
	if err != nil {
		return "", "", false
	}
	absPacketPath = filepath.Clean(absPacketPath)

	if _, ok := safepath.Rel(absRoot, absPacketPath); !ok {
		return "", "", false
	}

	fullPath := filepath.Join(filepath.Dir(absPacketPath), "reconciliation.json")
	relPath, ok := safepath.Rel(absRoot, fullPath)
	if !ok || ProtectedReconciliationOutputPath(relPath) || safepath.RejectSymlinkAncestors(absRoot, fullPath, "recovery reconciliation path") != nil {
		return "", "", false
	}
	if err := safepath.RejectSymlinkTarget(fullPath, "recovery reconciliation path"); err != nil {
		return "", "", false
	}

	return fullPath, relPath, true
}

func SafePacketPath(root string, packetPath string) (string, string, error) {
	fullPath, relPath, err := safeWorkspacePath(root, packetPath, "recovery packet path")
	if err != nil {
		return "", "", err
	}
	return fullPath, relPath, nil
}

func SafeDefaultReconciliationPath(root string, failureID string) (string, string, error) {
	fullPath, relPath, err := ReconciliationPath(root, failureID, "")
	if err != nil {
		return "", "", err
	}
	fullPath, safeRel, err := safeWorkspacePath(root, fullPath, "recovery reconciliation path")
	if err != nil {
		return "", "", err
	}
	if filepath.ToSlash(filepath.Clean(relPath)) != safeRel {
		relPath = safeRel
	}
	return fullPath, relPath, nil
}

func SafeReconciliationOutputPath(root string, packetPath string, failureID string, outputPath string) (string, string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		if fullPath, relPath, ok := SafeSiblingReconciliationPath(root, packetPath); ok {
			return fullPath, relPath, nil
		}
		return SafeDefaultReconciliationPath(root, failureID)
	}

	fullPath, relPath, err := safeWorkspacePath(root, outputPath, "recovery reconciliation output path")
	if err != nil {
		return "", "", err
	}
	if ProtectedReconciliationOutputPath(relPath) {
		return "", "", fmt.Errorf("recovery reconciliation output path is protected: %s", relPath)
	}
	return fullPath, relPath, nil
}

func SafeDecompositionOutputPath(root string, failureID string, outputPath string) (string, string, error) {
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		fullPath, relPath, err := DecompositionPacketPath(root, failureID, "")
		if err != nil {
			return "", "", err
		}
		fullPath, safeRel, err := safeWorkspacePath(root, fullPath, "recovery decomposition output path")
		if err != nil {
			return "", "", err
		}
		if filepath.ToSlash(filepath.Clean(relPath)) != safeRel {
			relPath = safeRel
		}
		return fullPath, relPath, nil
	}

	fullPath, relPath, err := safeWorkspacePath(root, outputPath, "recovery decomposition output path")
	if err != nil {
		return "", "", err
	}
	if ProtectedReconciliationOutputPath(relPath) {
		return "", "", fmt.Errorf("recovery decomposition output path is protected: %s", relPath)
	}
	if RecoveryControlArtifactPath(relPath) {
		return "", "", fmt.Errorf("recovery decomposition output path would overwrite a recovery control artifact: %s", relPath)
	}
	return fullPath, relPath, nil
}

func SafePromotionOutputPath(root string, outputPath string) (string, string, error) {
	fullPath, relPath, err := safeWorkspacePath(root, outputPath, "promotion output path")
	if err != nil {
		return "", "", err
	}
	if ProtectedPromotionOutputPath(relPath) {
		return "", "", fmt.Errorf("promotion output path is protected: %s", relPath)
	}
	return fullPath, relPath, nil
}

func ProtectedPromotionOutputPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." {
		return true
	}

	canonical := strings.ToLower(rel)
	if canonical == ".idiom" || strings.HasPrefix(canonical, ".idiom/") {
		return true
	}

	for _, prefix := range []string{".git/", "logs/"} {
		if canonical == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(canonical, prefix) {
			return true
		}
	}

	switch canonical {
	case ".env", "config.json":
		return true
	default:
		return false
	}
}

func RecoveryControlArtifactPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))

	canonical := strings.ToLower(rel)
	if canonical == "" || canonical == "." {
		return true
	}
	if canonical != ".idiom/recovery" && !strings.HasPrefix(canonical, ".idiom/recovery/") {
		return false
	}

	switch path.Base(canonical) {
	case "failure.json", "reconciliation.json":
		return true
	default:
		return false
	}
}

func ProtectedReconciliationOutputPath(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "" || rel == "." {
		return true
	}

	canonical := strings.ToLower(rel)
	if canonical == ".idiom" || strings.HasPrefix(canonical, ".idiom/") {
		return canonical != ".idiom/recovery" && !strings.HasPrefix(canonical, ".idiom/recovery/")
	}

	for _, prefix := range []string{".git/", "logs/"} {
		if canonical == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(canonical, prefix) {
			return true
		}
	}

	switch canonical {
	case ".env", "config.json":
		return true
	default:
		return false
	}
}

func safeWorkspacePath(root string, path string, label string) (string, string, error) {
	return safepath.UnderRoot(root, path, label, safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
}
