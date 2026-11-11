package safepath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type openTarget struct {
	rootPath     string
	targetPath   string
	rootRelative string
	label        string
	options      Options
	rootInfo     os.FileInfo
	targetInfo   os.FileInfo
}

// OpenFileUnderRoot opens path relative to a root handle and returns the file
// only after checking that the handle still names the validated object.
func OpenFileUnderRoot(root string, path string, label string, opts Options) (*os.File, error) {
	target, err := prepareOpenTarget(root, path, label, opts)
	if err != nil {
		return nil, err
	}

	rootHandle, err := openTargetRoot(target)
	if err != nil {
		return nil, err
	}
	defer rootHandle.Close()

	return openPreparedFile(rootHandle, target)
}

// OpenRootUnderRoot opens a directory relative to a root handle and validates
// the opened directory before returning its root handle.
func OpenRootUnderRoot(root string, path string, label string, opts Options) (*os.Root, error) {
	rootPath, expectedRootInfo, err := canonicalExistingRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve %s root: %w", label, err)
	}
	path, err = CleanPathChecked(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootPath, path)
	}
	path = rebasePathOntoCanonicalRoot(root, rootPath, CleanPath(filepath.Clean(path)))
	if !samePath(rootPath, path) {
		if _, ok := Rel(rootPath, path); !ok {
			return nil, fmt.Errorf("%s must stay under workspace root", label)
		}
		if opts.RejectSymlinkAncestors {
			if err := RejectSymlinkAncestors(rootPath, path, label); err != nil {
				return nil, err
			}
		}
	}
	if opts.RejectSymlinkTarget {
		if err := RejectSymlinkTarget(path, label); err != nil {
			return nil, err
		}
	}
	targetPath, err := CanonicalExistingPath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve real %s: %w", label, err)
	}
	targetPath = CleanPath(targetPath)
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !targetInfo.IsDir() {
		return nil, fmt.Errorf("%s must be a directory", label)
	}
	if samePath(rootPath, targetPath) {
		openedRoot, err := OpenRootPath(rootPath, label)
		if err != nil {
			return nil, err
		}
		openedInfo, err := openedRoot.Stat(".")
		if err != nil || !os.SameFile(expectedRootInfo, openedInfo) {
			_ = openedRoot.Close()
			if err != nil {
				return nil, fmt.Errorf("inspect opened %s root: %w", label, err)
			}
			return nil, fmt.Errorf("%s root changed while opening", label)
		}
		return openedRoot, nil
	}

	target, err := prepareOpenTarget(rootPath, path, label, opts)
	if err != nil {
		return nil, err
	}
	baseRoot, err := openTargetRoot(target)
	if err != nil {
		return nil, err
	}
	defer baseRoot.Close()
	openedBaseInfo, err := baseRoot.Stat(".")
	if err != nil || !os.SameFile(expectedRootInfo, openedBaseInfo) {
		if err != nil {
			return nil, fmt.Errorf("inspect opened %s root: %w", label, err)
		}
		return nil, fmt.Errorf("%s root changed while opening", label)
	}

	openedRoot, err := baseRoot.OpenRoot(target.rootRelative)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	if err := validateOpenedRoot(openedRoot, target); err != nil {
		_ = openedRoot.Close()
		return nil, err
	}
	return openedRoot, nil
}

// OpenRootPath opens a directory and binds its canonical path to the opened
// directory handle. Use it when the directory itself is the authority root.
func OpenRootPath(path string, label string) (*os.Root, error) {
	path, err := CleanPathChecked(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	if path == "" {
		return nil, fmt.Errorf("%s is empty", label)
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	path = CleanPath(path)
	before, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("%s must be a directory", label)
	}
	canonical, err := CanonicalExistingPath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve real %s: %w", label, err)
	}
	canonical = CleanPath(canonical)
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("inspect real %s: %w", label, err)
	}
	if !os.SameFile(before, canonicalInfo) {
		return nil, fmt.Errorf("%s changed while opening", label)
	}

	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	target := openTarget{
		rootPath:   canonical,
		targetPath: canonical,
		label:      label,
		rootInfo:   canonicalInfo,
		targetInfo: canonicalInfo,
	}
	if err := validateOpenedRoot(root, target); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// OpenFileFromRoot opens a file relative to an already-open root and validates
// the opened handle against the root and file identities.
func OpenFileFromRoot(root *os.Root, canonicalRoot string, relative string, label string, opts Options) (*os.File, error) {
	if root == nil {
		return nil, fmt.Errorf("%s root is not open", label)
	}
	canonicalRoot, err := CanonicalExistingPath(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve %s root: %w", label, err)
	}
	canonicalRoot = CleanPath(canonicalRoot)
	relative = filepath.Clean(filepath.FromSlash(strings.TrimSpace(relative)))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("%s must name a file under its root", label)
	}
	targetPath := CleanPath(filepath.Join(canonicalRoot, relative))
	if _, ok := Rel(canonicalRoot, targetPath); !ok {
		return nil, fmt.Errorf("%s must stay under workspace root", label)
	}
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect %s root: %w", label, err)
	}
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	target := openTarget{
		rootPath:     canonicalRoot,
		targetPath:   targetPath,
		rootRelative: relative,
		label:        label,
		options:      opts,
		rootInfo:     rootInfo,
		targetInfo:   targetInfo,
	}
	return openPreparedFile(root, target)
}

// ReadFileUnderRoot opens and reads one file without reopening its pathname.
func ReadFileUnderRoot(root string, path string, label string, opts Options) ([]byte, error) {
	file, err := OpenFileUnderRoot(root, path, label, opts)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

// ReadDirUnderRoot reads directory entries from the validated directory
// handle, without reopening the directory by pathname.
func ReadDirUnderRoot(root string, path string, label string, opts Options) ([]os.DirEntry, error) {
	if isRootDirectoryPath(root, path) {
		if opts.RejectSymlinkTarget {
			rootPath, err := CleanPathChecked(strings.TrimSpace(root))
			if err != nil {
				return nil, fmt.Errorf("resolve %s root: %w", label, err)
			}
			if err := RejectSymlinkTarget(rootPath, label); err != nil {
				return nil, err
			}
		}
		canonicalRoot, expectedRootInfo, err := canonicalExistingRoot(root)
		if err != nil {
			return nil, fmt.Errorf("resolve %s root: %w", label, err)
		}
		rootHandle, err := OpenRootPath(canonicalRoot, label)
		if err != nil {
			return nil, err
		}
		defer rootHandle.Close()
		openedRootInfo, err := rootHandle.Stat(".")
		if err != nil || !os.SameFile(expectedRootInfo, openedRootInfo) {
			if err != nil {
				return nil, fmt.Errorf("inspect opened %s root: %w", label, err)
			}
			return nil, fmt.Errorf("%s root changed while opening", label)
		}
		return readDirectoryEntries(rootHandle, canonicalRoot, canonicalRoot, label)
	}

	target, err := prepareOpenTarget(root, path, label, opts)
	if err != nil {
		return nil, err
	}
	if !target.targetInfo.IsDir() {
		return nil, fmt.Errorf("%s must be a directory", label)
	}

	rootHandle, err := openTargetRoot(target)
	if err != nil {
		return nil, err
	}
	defer rootHandle.Close()

	directory, err := openPreparedFile(rootHandle, target)
	if err != nil {
		return nil, err
	}
	defer directory.Close()

	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(left int, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	return entries, nil
}

func isRootDirectoryPath(root string, path string) bool {
	root, err := CleanPathChecked(strings.TrimSpace(root))
	if err != nil || root == "" {
		return false
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	path, err = CleanPathChecked(strings.TrimSpace(path))
	if err != nil || path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	return samePath(CleanPath(root), CleanPath(path))
}

func readDirectoryEntries(root *os.Root, canonicalRoot string, canonicalPath string, label string) ([]os.DirEntry, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open %s directory handle: %w", label, err)
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("inspect opened %s directory: %w", label, err)
	}
	if err := validateOpenedFile(directory, canonicalRoot, canonicalPath, rootInfo, label); err != nil {
		_ = directory.Close()
		return nil, err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read %s directory: %w", label, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s directory: %w", label, closeErr)
	}
	sort.Slice(entries, func(left int, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	return entries, nil
}

func prepareOpenTarget(root string, path string, label string, opts Options) (openTarget, error) {
	root, err := CleanPathChecked(strings.TrimSpace(root))
	if err != nil {
		return openTarget{}, fmt.Errorf("resolve %s root: %w", label, err)
	}
	if root == "" {
		return openTarget{}, fmt.Errorf("%s root is empty", label)
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return openTarget{}, fmt.Errorf("resolve %s root: %w", label, err)
	}
	root = CleanPath(root)
	rootBefore, err := os.Stat(root)
	if err != nil {
		return openTarget{}, fmt.Errorf("inspect %s root: %w", label, err)
	}
	if !rootBefore.IsDir() {
		return openTarget{}, fmt.Errorf("%s root must be a directory", label)
	}

	safePath, _, err := UnderRoot(root, path, label, opts)
	if err != nil {
		return openTarget{}, err
	}
	canonicalRoot, err := CanonicalExistingPath(root)
	if err != nil {
		return openTarget{}, fmt.Errorf("resolve real %s root: %w", label, err)
	}
	canonicalRoot = CleanPath(canonicalRoot)
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return openTarget{}, fmt.Errorf("inspect real %s root: %w", label, err)
	}
	if !os.SameFile(rootBefore, rootInfo) {
		return openTarget{}, fmt.Errorf("%s root changed while resolving", label)
	}

	canonicalTarget, err := CanonicalExistingPath(safePath)
	if err != nil {
		return openTarget{}, fmt.Errorf("resolve real %s: %w", label, err)
	}
	canonicalTarget = CleanPath(canonicalTarget)
	targetInfo, err := os.Stat(canonicalTarget)
	if err != nil {
		return openTarget{}, fmt.Errorf("inspect %s: %w", label, err)
	}
	relative, ok := Rel(canonicalRoot, canonicalTarget)
	if !ok {
		return openTarget{}, fmt.Errorf("%s symlink target escapes workspace root", label)
	}

	opts.AllowMissing = false
	return openTarget{
		rootPath:     canonicalRoot,
		targetPath:   canonicalTarget,
		rootRelative: filepath.FromSlash(relative),
		label:        label,
		options:      opts,
		rootInfo:     rootInfo,
		targetInfo:   targetInfo,
	}, nil
}

func openTargetRoot(target openTarget) (*os.Root, error) {
	root, err := os.OpenRoot(target.rootPath)
	if err != nil {
		return nil, fmt.Errorf("open %s root: %w", target.label, err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(target.rootInfo, opened) {
		_ = root.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect opened %s root: %w", target.label, err)
		}
		return nil, fmt.Errorf("%s root changed while opening", target.label)
	}
	rootFile, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("open %s root handle: %w", target.label, err)
	}
	defer rootFile.Close()
	if err := validateOpenedFile(rootFile, target.rootPath, target.rootPath, target.rootInfo, target.label); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func openPreparedFile(root *os.Root, target openTarget) (*os.File, error) {
	rootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(target.rootInfo, rootInfo) {
		if err != nil {
			return nil, fmt.Errorf("inspect opened %s root: %w", target.label, err)
		}
		return nil, fmt.Errorf("%s root changed while opening", target.label)
	}

	file, err := root.Open(target.rootRelative)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", target.label, err)
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(target.targetInfo, opened) {
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("inspect opened %s: %w", target.label, err)
		}
		return nil, fmt.Errorf("%s changed while opening", target.label)
	}
	if err := validateOpenedFile(file, target.rootPath, target.targetPath, target.targetInfo, target.label); err != nil {
		_ = file.Close()
		return nil, err
	}
	if target.options.RejectSymlinkAncestors {
		if err := RejectSymlinkAncestors(target.rootPath, target.targetPath, target.label); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	if target.options.RejectSymlinkTarget {
		if err := RejectSymlinkTarget(target.targetPath, target.label); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return file, nil
}

func validateOpenedRoot(root *os.Root, target openTarget) error {
	opened, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", target.label, err)
	}
	if !os.SameFile(target.targetInfo, opened) {
		return fmt.Errorf("%s changed while opening", target.label)
	}
	file, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open %s handle: %w", target.label, err)
	}
	defer file.Close()
	return validateOpenedFile(file, target.rootPath, target.targetPath, target.targetInfo, target.label)
}

func canonicalExistingRoot(root string) (string, os.FileInfo, error) {
	root, err := CleanPathChecked(strings.TrimSpace(root))
	if err != nil {
		return "", nil, err
	}
	if root == "" {
		return "", nil, fmt.Errorf("workspace root is empty")
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", nil, err
	}
	root = CleanPath(root)
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", nil, err
	}
	if !rootInfo.IsDir() {
		return "", nil, fmt.Errorf("workspace root must be a directory")
	}
	canonical, err := CanonicalExistingPath(root)
	if err != nil {
		return "", nil, err
	}
	canonical = CleanPath(canonical)
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		return "", nil, err
	}
	if !os.SameFile(rootInfo, canonicalInfo) {
		return "", nil, fmt.Errorf("workspace root changed while resolving")
	}
	return canonical, canonicalInfo, nil
}

// rebasePathOntoCanonicalRoot maps an absolute path spelled under the caller's
// root (for example through macOS /var, a symlink to /private/var, or a Windows
// junction) onto the canonical root. Only the root prefix is rewritten: every
// later component stays as written, so symlinks inside the root are still seen
// by the containment and symlink checks that follow.
func rebasePathOntoCanonicalRoot(root string, canonicalRoot string, path string) string {
	rawRoot, err := filepath.Abs(filepath.Clean(strings.TrimSpace(root)))
	if err != nil {
		return path
	}

	rawRoot = CleanPath(rawRoot)
	if samePath(rawRoot, canonicalRoot) {
		return path
	}

	if samePath(rawRoot, path) {
		return canonicalRoot
	}

	if rel, ok := Rel(rawRoot, path); ok {
		return CleanPath(filepath.Join(canonicalRoot, filepath.FromSlash(rel)))
	}

	return path
}
