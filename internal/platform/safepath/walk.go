package safepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WalkCursor marks where a stopped walk resumes: list Dir (root-relative,
// slash-separated, "." for the root) and report only the entries named after
// After. An empty After lists the whole directory. Entries and directories a
// walk finished before it stopped are not visited again.
type WalkCursor struct {
	Dir   string `json:"dir"`
	After string `json:"after,omitempty"`
}

// WalkDirUnderRoot visits path and its descendants using opened directory
// handles. Each child directory is opened relative to its already-open parent
// and checked against the identity observed through that parent.
func WalkDirUnderRoot(root string, path string, label string, fn fs.WalkDirFunc) error {
	walker, err := newDirWalker(root, path, label, nil, fn)
	if err != nil {
		return err
	}
	_, err = walker.run(nil)
	return err
}

// WalkDirUnderRootResumable walks root like WalkDirUnderRoot, in the same
// order, but asks stop before descending into each directory. When stop
// returns true it returns the cursors a later call passes as pending to carry
// on; a finished walk returns no cursors. A nil pending starts at root. Every
// resumed directory is reopened from root through validated handles, so a
// cursor grants no access a fresh walk would not.
func WalkDirUnderRootResumable(root string, pending []WalkCursor, label string, stop func() bool, fn fs.WalkDirFunc) ([]WalkCursor, error) {
	walker, err := newDirWalker(root, root, label, stop, fn)
	if err != nil {
		return nil, err
	}
	return walker.run(pending)
}

type dirWalker struct {
	canonicalRoot string
	rootInfo      os.FileInfo
	startRelative string
	displayStart  string
	label         string
	stop          func() bool
	fn            fs.WalkDirFunc
}

func newDirWalker(root string, path string, label string, stop func() bool, fn fs.WalkDirFunc) (*dirWalker, error) {
	if fn == nil {
		return nil, fmt.Errorf("%s walk callback is nil", label)
	}

	canonicalRoot, expectedRootInfo, err := canonicalExistingRoot(root)
	if err != nil {
		return nil, fmt.Errorf("resolve %s root: %w", label, err)
	}
	path, err = CleanPathChecked(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	if path == "" {
		return nil, fmt.Errorf("%s path is empty", label)
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
			return nil, fmt.Errorf("%s must stay under workspace root", label)
		}
	}

	return &dirWalker{
		canonicalRoot: canonicalRoot,
		rootInfo:      expectedRootInfo,
		startRelative: filepath.FromSlash(rootRelative),
		displayStart:  filepath.Clean(path),
		label:         label,
		stop:          stop,
		fn:            fn,
	}, nil
}

// run walks the pending cursors in order, or the whole tree from its start
// when pending is nil, and returns the cursors left when stop fired.
func (w *dirWalker) run(pending []WalkCursor) ([]WalkCursor, error) {
	if pending == nil {
		start, startInfo, err := openWalkRoot(w.canonicalRoot, w.rootInfo, w.startRelative, w.label)
		if err != nil {
			return nil, err
		}
		rootEntry := walkDirEntry{name: filepath.Base(w.displayStart), info: startInfo}
		if err := w.fn(w.displayStart, rootEntry, nil); err != nil {
			_ = start.Close()
			if err == filepath.SkipAll || err == filepath.SkipDir {
				return nil, nil
			}
			return nil, err
		}
		left, err := w.walkDirectory(start, startInfo, ".", "")
		_ = start.Close()
		return finishWalk(left, err)
	}

	for index, cursor := range pending {
		if index > 0 && w.stop != nil && w.stop() {
			return pending[index:], nil
		}
		relative, err := cursorDirectory(cursor, w.label)
		if err != nil {
			return nil, err
		}
		dir, dirInfo, err := openWalkRoot(w.canonicalRoot, w.rootInfo, filepath.Join(w.startRelative, relative), w.label)
		if errors.Is(err, errWalkDirVanished) {
			// Deleted since the checkpoint: nothing under it is left to walk.
			continue
		}
		if err != nil {
			return nil, err
		}
		left, err := w.walkDirectory(dir, dirInfo, filepath.ToSlash(relative), cursor.After)
		_ = dir.Close()
		if err == filepath.SkipAll {
			// The caller is done with the whole walk, as in a fresh walk: no
			// cursor is kept, so a resumed walk never visits past this point.
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if left != nil {
			return append(left, pending[index+1:]...), nil
		}
	}
	return nil, nil
}

// errWalkDirVanished marks a resume cursor whose directory, or one of its
// ancestors, no longer exists. Only a not-exist answer counts: a component that
// is now a link, a file or another object still fails the walk closed.
var errWalkDirVanished = errors.New("walk directory no longer exists")

func finishWalk(left []WalkCursor, err error) ([]WalkCursor, error) {
	if err == filepath.SkipAll {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return left, nil
}

// cursorDirectory checks a resume cursor names a directory under the root;
// openWalkRoot then refuses any component that is not a real directory.
func cursorDirectory(cursor WalkCursor, label string) (string, error) {
	relative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(cursor.Dir)))
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s resume cursor %q must stay under workspace root", label, cursor.Dir)
	}
	if strings.ContainsAny(cursor.After, `/\`) {
		return "", fmt.Errorf("%s resume cursor entry %q must be a single name", label, cursor.After)
	}
	return relative, nil
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
		if errors.Is(err, fs.ErrNotExist) {
			_ = current.Close()
			return nil, nil, fmt.Errorf("inspect %s directory: %w: %w", label, errWalkDirVanished, err)
		}
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

// walkDirectory lists one opened directory and descends into its real
// subdirectories in name order. It returns the cursors left when stop fired,
// deepest first, so walking them in order continues the same pre-order walk.
func (w *dirWalker) walkDirectory(dir *os.Root, dirInfo os.FileInfo, relative string, after string) ([]WalkCursor, error) {
	canonicalPath := filepath.Join(w.canonicalRoot, w.startRelative, filepath.FromSlash(relative))
	entries, err := readValidatedDirectory(dir, dirInfo, w.canonicalRoot, canonicalPath, w.label)
	if err != nil {
		return nil, err
	}
	displayPath := filepath.Join(w.displayStart, filepath.FromSlash(relative))

	for _, listed := range entries {
		name := listed.Name()
		if after != "" && name <= after {
			continue
		}
		childDisplayPath := filepath.Join(displayPath, name)
		entry, childInfo, err := walkEntryFor(dir, listed)
		if err != nil {
			callbackErr := w.fn(childDisplayPath, nil, err)
			if callbackErr != nil && callbackErr != filepath.SkipDir {
				return nil, callbackErr
			}
			continue
		}

		if callbackErr := w.fn(childDisplayPath, entry, nil); callbackErr != nil {
			if callbackErr == filepath.SkipDir {
				continue
			}
			return nil, callbackErr
		}

		if childInfo == nil || !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}

		childRelative := pathJoinSlash(relative, name)
		if w.stop != nil && w.stop() {
			return []WalkCursor{{Dir: childRelative}, {Dir: relative, After: name}}, nil
		}

		child, err := dir.OpenRoot(name)
		if err != nil {
			return nil, fmt.Errorf("open %s child directory %q: %w", w.label, name, err)
		}
		left, walkErr := w.walkDirectory(child, childInfo, childRelative, "")
		_ = child.Close()
		if walkErr != nil {
			return nil, walkErr
		}
		if left != nil {
			return append(left, WalkCursor{Dir: relative, After: name}), nil
		}
	}

	return nil, nil
}

// walkEntryFor builds the entry reported for one listed name. Only a listed
// directory is inspected now, through the parent handle without following
// reparse points, because its identity is what the opened child handle must
// match before the walk descends. Every other entry reports the type the
// directory listing gave and inspects itself only if the caller asks for Info.
func walkEntryFor(parent *os.Root, listed os.DirEntry) (fs.DirEntry, os.FileInfo, error) {
	if !listed.IsDir() {
		return &lazyWalkEntry{parent: parent, listed: listed}, nil, nil
	}
	info, err := parent.Lstat(listed.Name())
	if err != nil {
		return nil, nil, err
	}
	return walkDirEntry{name: listed.Name(), info: info}, info, nil
}

func pathJoinSlash(dir string, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// readValidatedDirectory opens dir's own handle, checks it names the expected
// object inside the root and is not a reparse point, then lists it from that
// same handle. One validation covers both the directory handle and dir, since
// "." opened relative to a directory handle is that directory.
func readValidatedDirectory(dir *os.Root, expected os.FileInfo, canonicalRoot string, canonicalPath string, label string) ([]os.DirEntry, error) {
	directory, err := dir.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open %s directory handle: %w", label, err)
	}
	if err := validateOpenedFile(directory, canonicalRoot, canonicalPath, expected, label); err != nil {
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

type walkDirEntry struct {
	name string
	info os.FileInfo
}

func (entry walkDirEntry) Name() string { return entry.name }

func (entry walkDirEntry) IsDir() bool { return entry.info.IsDir() }

func (entry walkDirEntry) Type() os.FileMode { return entry.info.Mode().Type() }

func (entry walkDirEntry) Info() (os.FileInfo, error) { return entry.info, nil }

var _ fs.DirEntry = walkDirEntry{}

// lazyWalkEntry reports a non-directory entry with the type from the directory
// listing, as fs.DirEntry allows. Info inspects the entry through its parent
// handle on first use, so sizes and times are as exact as an Lstat; the parent
// handle is open only while the walk callback for this entry runs.
type lazyWalkEntry struct {
	parent *os.Root
	listed os.DirEntry
	info   os.FileInfo
	err    error
}

func (entry *lazyWalkEntry) Name() string { return entry.listed.Name() }

func (entry *lazyWalkEntry) IsDir() bool { return entry.listed.IsDir() }

func (entry *lazyWalkEntry) Type() os.FileMode { return entry.listed.Type() }

func (entry *lazyWalkEntry) Info() (os.FileInfo, error) {
	if entry.info == nil && entry.err == nil {
		entry.info, entry.err = entry.parent.Lstat(entry.listed.Name())
	}
	return entry.info, entry.err
}

var _ fs.DirEntry = (*lazyWalkEntry)(nil)
