package productworkflow

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/safepath"
)

type effortArtifactPathRequirement uint8

type effortArtifactWriteBoundary uint8

const (
	effortArtifactMustExist effortArtifactPathRequirement = iota
	effortArtifactMayBeMissing
)

const (
	effortArtifactWriteBeforeParentPreparation effortArtifactWriteBoundary = iota
	effortArtifactWriteBeforeAtomicReplacement
)

var effortArtifactWriteBoundaryHook func(effortArtifactWriteBoundary)

// writeEffortArtifactAtomic is the shared write path for generated artifacts
// beneath an effort root. It acquires the product-workflow mutation lease so
// ordinary artifact writes cannot race a lease-held generation check or bypass
// a pending status-migration fence. Callers that already hold the lease use
// writeEffortArtifactAtomicRoot directly.
//
// Go 1.26 os.Root prevents escape but intentionally follows in-root symlinks and
// exposes no portable no-follow open. The checks below reject observable
// symlinks before and after I/O. A component swapped to a symlink and back
// between identity checks remains a bounded race. GOOS=js also retains
// os.Root's documented TOCTOU limitation, while plan9 and js roots are
// path-based across directory renames.
func writeEffortArtifactAtomic(effortRoot string, relPath string, body []byte, perm os.FileMode) error {
	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}
	lease, err := openProductWorkflowMutationLease(effortRoot)
	if err != nil {
		return fmt.Errorf("acquire product workflow artifact write lease: %w", err)
	}
	defer lease.Close()

	return lease.writeEffortArtifactAtomic(cleanRel, body, perm)
}

func (lease *productWorkflowMutationLease) writeEffortArtifactAtomic(
	cleanRel string,
	body []byte,
	perm os.FileMode,
) error {
	if lease == nil || lease.root == nil {
		return errors.New("product workflow artifact write lease is not open")
	}
	normalizedRel, ok := cleanEffortRelativePath(cleanRel)
	if !ok {
		return fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", cleanRel)
	}
	cleanRel = normalizedRel
	if effortArtifactWriteBoundaryHook != nil {
		effortArtifactWriteBoundaryHook(effortArtifactWriteBeforeParentPreparation)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate product workflow artifact write effort root before parent preparation: %w", err)
	}
	if err := ensureEffortArtifactParents(lease.root, cleanRel); err != nil {
		return err
	}
	if err := validateEffortArtifactPath(lease.root, cleanRel, effortArtifactMayBeMissing); err != nil {
		return err
	}
	if effortArtifactWriteBoundaryHook != nil {
		effortArtifactWriteBoundaryHook(effortArtifactWriteBeforeAtomicReplacement)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("revalidate product workflow artifact write effort root before atomic replacement: %w", err)
	}

	return replaceEffortArtifactAtomicRoot(lease.root, cleanRel, body, perm)
}

// writeEffortArtifactAtomicRoot owns an atomic artifact write relative to an
// already-open effort root. Transactional callers must use this form so the
// inode whose authority they inspected is also the inode they mutate.
func writeEffortArtifactAtomicRoot(root *os.Root, relPath string, body []byte, perm os.FileMode) error {
	if root == nil {
		return errors.New("effort artifact root is not open")
	}
	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}

	if err := ensureEffortArtifactParents(root, cleanRel); err != nil {
		return err
	}
	if err := validateEffortArtifactPath(root, cleanRel, effortArtifactMayBeMissing); err != nil {
		return err
	}

	return replaceEffortArtifactAtomicRoot(root, cleanRel, body, perm)
}

func replaceEffortArtifactAtomicRoot(root *os.Root, cleanRel string, body []byte, perm os.FileMode) error {
	if err := atomicfile.WriteFileRoot(root, cleanRel, body, perm); err != nil {
		return fmt.Errorf("atomically write effort artifact: %w", err)
	}
	if err := validateEffortArtifactPath(root, cleanRel, effortArtifactMustExist); err != nil {
		return &atomicfile.PostCommitError{Op: "effort artifact committed but path validation failed", Err: err}
	}

	return nil
}

func readEffortArtifact(effortRoot string, relPath string) ([]byte, error) {
	root, cleanRel, err := openEffortArtifactRoot(effortRoot, relPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	return readEffortArtifactRoot(root, cleanRel)
}

// readEffortArtifactRoot owns an identity-checked artifact read relative to an
// already-open effort root. Lease-held snapshots use it to avoid pairing
// authority from one root inode with documents reopened from another.
func readEffortArtifactRoot(root *os.Root, relPath string) ([]byte, error) {
	if root == nil {
		return nil, errors.New("effort artifact root is not open")
	}
	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return nil, fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}

	if err := validateEffortArtifactPath(root, cleanRel, effortArtifactMustExist); err != nil {
		return nil, err
	}
	before, err := root.Lstat(cleanRel)
	if err != nil {
		return nil, fmt.Errorf("inspect effort artifact: %w", err)
	}

	file, err := root.Open(cleanRel)
	if err != nil {
		return nil, fmt.Errorf("open effort artifact: %w", err)
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened effort artifact: %w", err)
	}
	after, err := root.Lstat(cleanRel)
	if err != nil {
		return nil, fmt.Errorf("reinspect effort artifact: %w", err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("effort artifact path changed while opening")
	}
	if err := validateEffortArtifactParents(root, cleanRel); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read effort artifact: %w", err)
	}
	finalInfo, err := root.Lstat(cleanRel)
	if err != nil || finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.Mode().IsRegular() || !os.SameFile(opened, finalInfo) {
		return nil, fmt.Errorf("effort artifact path changed while reading")
	}
	return body, nil
}

// readEffortArtifactDirectoryRoot reads a directory through the root inode
// already owned by the surrounding transaction.
func readEffortArtifactDirectoryRoot(root *os.Root, relPath string) ([]os.DirEntry, error) {
	if root == nil {
		return nil, errors.New("effort artifact root is not open")
	}
	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return nil, fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}

	if err := validateEffortArtifactParents(root, cleanRel); err != nil {
		return nil, err
	}
	before, err := root.Lstat(cleanRel)
	if err != nil {
		return nil, fmt.Errorf("inspect effort artifact directory: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("effort artifact directory %q must be a directory and must not be a symlink", filepath.ToSlash(cleanRel))
	}

	directory, err := root.Open(cleanRel)
	if err != nil {
		return nil, fmt.Errorf("open effort artifact directory: %w", err)
	}
	defer directory.Close()

	opened, openErr := directory.Stat()
	after, afterErr := root.Lstat(cleanRel)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("effort artifact directory changed while opening")
	}

	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read effort artifact directory: %w", err)
	}
	finalInfo, err := root.Lstat(cleanRel)
	if err != nil || !os.SameFile(opened, finalInfo) {
		return nil, fmt.Errorf("effort artifact directory changed while reading")
	}

	return entries, nil
}

func openEffortArtifactRoot(effortRoot string, relPath string) (*os.Root, string, error) {
	if strings.TrimSpace(effortRoot) == "" {
		return nil, "", fmt.Errorf("effort root is empty")
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		return nil, "", fmt.Errorf("resolve effort root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)

	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return nil, "", fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}

	before, err := os.Lstat(absRoot)
	if err != nil {
		return nil, "", fmt.Errorf("inspect effort root: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("effort root must not be a symlink")
	}
	if !before.IsDir() {
		return nil, "", errors.New("effort root must be a directory")
	}

	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, "", fmt.Errorf("open effort root: %w", err)
	}

	opened, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, "", fmt.Errorf("inspect opened effort root: %w", err)
	}
	after, err := os.Lstat(absRoot)
	if err != nil {
		root.Close()
		return nil, "", fmt.Errorf("reinspect effort root: %w", err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		root.Close()
		return nil, "", errors.New("effort root changed while opening")
	}

	return root, cleanRel, nil
}

func ensureEffortArtifactParents(root *os.Root, cleanRel string) error {
	parent := filepath.Dir(cleanRel)
	if parent == "." {
		return nil
	}

	current := ""
	for _, component := range strings.Split(parent, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)

		info, err := root.Lstat(current)
		observedMissing := os.IsNotExist(err)
		if observedMissing {
			if err := root.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create effort artifact directory %q: %w", filepath.ToSlash(current), err)
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("inspect effort artifact directory %q: %w", filepath.ToSlash(current), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("effort artifact path must not traverse symlinked directories: %q", filepath.ToSlash(current))
		}
		if !info.IsDir() {
			return fmt.Errorf("effort artifact parent %q must be a directory", filepath.ToSlash(current))
		}
		if observedMissing {
			if err := atomicfile.SyncDirRoot(root, filepath.Dir(current)); err != nil {
				return fmt.Errorf("sync effort artifact directory entry %q: %w", filepath.ToSlash(current), err)
			}
		}
	}

	return validateEffortArtifactParents(root, cleanRel)
}

func validateEffortArtifactParents(root *os.Root, cleanRel string) error {
	parent := filepath.Dir(cleanRel)
	if parent == "." {
		return nil
	}

	current := ""
	for _, component := range strings.Split(parent, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)

		info, err := root.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect effort artifact directory %q: %w", filepath.ToSlash(current), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("effort artifact path must not traverse symlinked directories: %q", filepath.ToSlash(current))
		}
		if !info.IsDir() {
			return fmt.Errorf("effort artifact parent %q must be a directory", filepath.ToSlash(current))
		}
	}

	return nil
}

func validateEffortArtifactPath(root *os.Root, cleanRel string, requirement effortArtifactPathRequirement) error {
	if err := validateEffortArtifactParents(root, cleanRel); err != nil {
		return err
	}

	info, err := root.Lstat(cleanRel)
	if os.IsNotExist(err) && requirement == effortArtifactMayBeMissing {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect effort artifact %q: %w", filepath.ToSlash(cleanRel), err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("effort artifact %q must not be a symlink", filepath.ToSlash(cleanRel))
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("effort artifact %q must be a regular file", filepath.ToSlash(cleanRel))
	}

	return nil
}

func resolveEffortArtifactPath(effortRoot string, relPath string, requirement effortArtifactPathRequirement) (string, error) {
	if strings.TrimSpace(effortRoot) == "" {
		return "", fmt.Errorf("effort root is empty")
	}
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		return "", fmt.Errorf("resolve effort root: %w", err)
	}

	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return "", fmt.Errorf("inspect effort root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("effort root must not be a symlink")
	}
	if !rootInfo.IsDir() {
		return "", errors.New("effort root must be a directory")
	}

	cleanRel, ok := cleanEffortRelativePath(relPath)
	if !ok {
		return "", fmt.Errorf("effort artifact path %q must be effort-relative and must not contain path traversal", relPath)
	}

	absPath, _, err := safepath.UnderRoot(absRoot, cleanRel, "effort artifact path", safepath.Options{
		AllowMissing:           requirement == effortArtifactMayBeMissing,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return "", err
	}

	return absPath, nil
}
