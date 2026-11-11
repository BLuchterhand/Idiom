package safepath

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WalkDirUnderRoot visits path and its descendants using opened directory
// handles. Each child directory is opened relative to its already-open parent
// and checked against the identity observed through that parent.
func WalkDirUnderRoot(root string, path string, label string, fn fs.WalkDirFunc) error {
	if fn == nil {
		return fmt.Errorf("%s walk callback is nil", label)
	}

	canonicalRoot, expectedRootInfo, err := canonicalExistingRoot(root)
	if err != nil {
		return fmt.Errorf("resolve %s root: %w", label, err)
	}
	path, err = CleanPathChecked(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", label, err)
	}
	if path == "" {
		return fmt.Errorf("%s path is empty", label)
	}

	targetPath := path
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(canonicalRoot, targetPath)
	}
	targetPath = CleanPath(filepath.Clean(targetPath))
	targetPath = rebasePathOntoCanonicalRoot(root, canonicalRoot, targetPath)

	rootRelative := "."
	if !samePath(canonicalRoot, targetPath) {
		var ok bool
		rootRelative, ok = Rel(canonicalRoot, targetPath)
		if !ok {
			return fmt.Errorf("%s must stay under workspace root", label)
		}
	}

	walkRoot, walkRootInfo, err := openWalkRoot(canonicalRoot, expectedRootInfo, rootRelative, label)
	if err != nil {
		return err
	}
	defer walkRoot.Close()

	displayRoot := filepath.Clean(path)
	rootEntry := walkDirEntry{name: filepath.Base(displayRoot), info: walkRootInfo}
	if err := fn(displayRoot, rootEntry, nil); err != nil {
		if err == filepath.SkipAll || err == filepath.SkipDir {
			return nil
		}
		return err
	}

	err = walkOpenedDirectory(walkRoot, canonicalRoot, targetPath, displayRoot, label, fn)
	if err == filepath.SkipAll {
		return nil
	}
	return err
}

func openWalkRoot(canonicalRoot string, expectedRootInfo os.FileInfo, relative string, label string) (*os.Root, os.FileInfo, error) {
	root, err := OpenRootPath(canonicalRoot, label)
	if err != nil {
		return nil, nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, nil, fmt.Errorf("inspect opened %s root: %w", label, err)
	}
	if !os.SameFile(expectedRootInfo, rootInfo) {
		_ = root.Close()
		return nil, nil, fmt.Errorf("%s root changed while opening", label)
	}
	if relative == "." {
		return root, rootInfo, nil
	}

	current := root
	currentPath := canonicalRoot
	for _, component := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			_ = current.Close()
			return nil, nil, fmt.Errorf("invalid %s path component", label)
		}

		info, err := current.Lstat(component)
		if err != nil {
			_ = current.Close()
			return nil, nil, fmt.Errorf("inspect %s directory: %w", label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			_ = current.Close()
			return nil, nil, fmt.Errorf("%s path must contain only real directories", label)
		}

		next, err := current.OpenRoot(component)
		if err != nil {
			_ = current.Close()
			return nil, nil, fmt.Errorf("open %s directory: %w", label, err)
		}
		nextPath := filepath.Join(currentPath, component)
		target := openTarget{
			rootPath:   canonicalRoot,
			targetPath: nextPath,
			label:      label,
			targetInfo: info,
		}
		if err := validateOpenedRoot(next, target); err != nil {
			_ = next.Close()
			_ = current.Close()
			return nil, nil, err
		}

		_ = current.Close()
		current = next
		currentPath = nextPath
	}

	info, err := current.Stat(".")
	if err != nil {
		_ = current.Close()
		return nil, nil, fmt.Errorf("inspect opened %s directory: %w", label, err)
	}
	return current, info, nil
}

func walkOpenedDirectory(parent *os.Root, canonicalRoot string, canonicalPath string, displayPath string, label string, fn fs.WalkDirFunc) error {
	entries, err := readDirectoryEntries(parent, canonicalRoot, canonicalPath, label)
	if err != nil {
		return err
	}

	for _, listed := range entries {
		name := listed.Name()
		childDisplayPath := filepath.Join(displayPath, name)
		info, err := parent.Lstat(name)
		if err != nil {
			callbackErr := fn(childDisplayPath, nil, err)
			if callbackErr == filepath.SkipAll {
				return filepath.SkipAll
			}
			if callbackErr == filepath.SkipDir {
				continue
			}
			if callbackErr != nil {
				return callbackErr
			}
			continue
		}

		entry := walkDirEntry{name: name, info: info}
		if callbackErr := fn(childDisplayPath, entry, nil); callbackErr != nil {
			if callbackErr == filepath.SkipAll {
				return filepath.SkipAll
			}
			if callbackErr == filepath.SkipDir {
				continue
			}
			return callbackErr
		}

		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		child, err := parent.OpenRoot(name)
		if err != nil {
			return fmt.Errorf("open %s child directory %q: %w", label, name, err)
		}
		childCanonicalPath := filepath.Join(canonicalPath, name)
		target := openTarget{
			rootPath:   canonicalRoot,
			targetPath: childCanonicalPath,
			label:      label,
			targetInfo: info,
		}
		if err := validateOpenedRoot(child, target); err != nil {
			_ = child.Close()
			return err
		}

		walkErr := walkOpenedDirectory(child, canonicalRoot, childCanonicalPath, childDisplayPath, label, fn)
		_ = child.Close()
		if walkErr != nil {
			return walkErr
		}
	}

	return nil
}

type walkDirEntry struct {
	name string
	info os.FileInfo
}

func (entry walkDirEntry) Name() string { return entry.name }

func (entry walkDirEntry) IsDir() bool { return entry.info.IsDir() }

func (entry walkDirEntry) Type() os.FileMode { return entry.info.Mode().Type() }

func (entry walkDirEntry) Info() (os.FileInfo, error) { return entry.info, nil }

var _ fs.DirEntry = walkDirEntry{}
