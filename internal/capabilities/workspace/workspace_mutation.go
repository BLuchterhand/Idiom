package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/failures"
	"idiom/internal/platform/safepath"
)

var (
	errWorkspaceMutationParentMissing               = errors.New("workspace mutation parent is missing")
	errWorkspaceMutationStale                       = errors.New("workspace mutation preimage changed")
	errWorkspaceMutationDirectoryPublishUnsupported = errors.New("atomic no-replace directory publication is unsupported on this platform")
)

type workspaceMutationCheckpointStage string

const (
	workspaceMutationAfterPreflight         workspaceMutationCheckpointStage = "after_preflight"
	workspaceMutationAfterParentBind        workspaceMutationCheckpointStage = "after_parent_bound"
	workspaceMutationBeforeCommit           workspaceMutationCheckpointStage = "before_commit"
	workspaceMutationWaitingForGate         workspaceMutationCheckpointStage = "waiting_for_authority"
	workspaceMutationBeforeReceipt          workspaceMutationCheckpointStage = "before_receipt"
	workspaceMutationBeforeDirectoryPublish workspaceMutationCheckpointStage = "before_directory_publish"
)

type workspaceMutationCheckpoint struct {
	Stage          workspaceMutationCheckpointStage
	Path           string
	OperationIndex int
}

type workspaceMutationCheckpointFunc func(workspaceMutationCheckpoint)

type workspaceMutationCheckpointContextKey struct{}

func workspaceMutationCheckpointFromContext(ctx context.Context) workspaceMutationCheckpointFunc {
	if ctx == nil {
		return nil
	}

	checkpoint, _ := ctx.Value(workspaceMutationCheckpointContextKey{}).(workspaceMutationCheckpointFunc)
	return checkpoint
}

type workspaceMutationTarget struct {
	canonicalID string
	relative    string
	absolute    string
	display     string
}

type workspaceMutationPreimage struct {
	exists bool
	info   os.FileInfo
	digest [sha256.Size]byte
	body   []byte
}

type workspaceMutationCommit struct {
	path           string
	kind           MutationCommitKind
	postimage      mutationRollbackPostimage
	postimageExact bool
	fileBody       []byte
	fileBodyExact  bool
	fileExists     bool
	appendStart    int64
	appendLength   int64
	appendDigest   [sha256.Size]byte
	appendExact    bool
}

// workspaceMutationGate serializes workspace mutations while allowing a
// canceled invocation to stop waiting for authority. The zero value is ready
// for use so PathAccess keeps its zero-value construction contract.
type workspaceMutationGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *workspaceMutationGate) init() {
	g.once.Do(func() {
		g.token = make(chan struct{}, 1)
		g.token <- struct{}{}
	})
}

func (g *workspaceMutationGate) Lock(ctx context.Context, onWait func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.init()
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case <-g.token:
		if err := ctx.Err(); err != nil {
			g.token <- struct{}{}
			return err
		}
		return nil
	default:
	}
	if onWait != nil {
		onWait()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
		if err := ctx.Err(); err != nil {
			g.token <- struct{}{}
			return err
		}
		return nil
	}
}

func (g *workspaceMutationGate) Unlock() {
	g.init()
	g.token <- struct{}{}
}

// workspaceMutation owns the filesystem authority for one workspace mutation
// invocation. Its opened root, transaction serialization lock, path
// resolution, preimages, and commits have the same lifetime. os.Root provides
// containment on supported platforms; GOOS=js retains its documented TOCTOU
// limitation, and plan9/js roots remain path-based across directory renames.
type workspaceMutation struct {
	access          *PathAccess
	root            *os.Root
	rootPath        string
	rootIdentity    os.FileInfo
	basePath        string
	workdir         string
	ctx             context.Context
	authorization   *mutationAuthorizationReceipt
	checkpoint      workspaceMutationCheckpointFunc
	commits         []workspaceMutationCommit
	committedByPath map[string]struct{}
	observedByPath  map[string]workspaceMutationPreimage
	initialByPath   map[string]workspaceMutationPreimage
	failedTargetID  string
	unattemptedIDs  []string
	rollbackErr     error
	closeOnce       sync.Once
}

func (a *PathAccess) beginWorkspaceMutation(ctx context.Context, req ToolRequest) (*workspaceMutation, error) {
	if !a.Enabled() {
		return nil, fmt.Errorf("workspace root is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	receipt, ok := mutationAuthorizationFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("%w: authorization receipt is absent", errWorkspaceMutationUnauthorized)
	}
	rootPath := filepath.Clean(strings.TrimSpace(a.cfg.Root))
	if err := receipt.consume(ctx, req, rootPath); err != nil {
		return nil, err
	}

	checkpoint := workspaceMutationCheckpointFromContext(ctx)
	if err := a.mutationGate.Lock(ctx, func() {
		if checkpoint != nil {
			checkpoint(workspaceMutationCheckpoint{Stage: workspaceMutationWaitingForGate, Path: filepath.ToSlash(filepath.Clean(req.Workdir))})
		}
	}); err != nil {
		return nil, fmt.Errorf("acquire workspace mutation authority: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			a.mutationGate.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	before, err := os.Lstat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace mutation root: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("workspace mutation root must be a directory and must not be a symlink")
	}

	root, err := safepath.OpenRootPath(rootPath, "workspace mutation root")
	if err != nil {
		return nil, fmt.Errorf("open workspace mutation root: %w", err)
	}
	opened, openErr := root.Stat(".")
	after, afterErr := os.Lstat(rootPath)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = root.Close()
		if openErr != nil {
			return nil, fmt.Errorf("inspect opened workspace mutation root: %w", openErr)
		}
		if afterErr != nil {
			return nil, fmt.Errorf("reinspect workspace mutation root: %w", afterErr)
		}

		return nil, fmt.Errorf("workspace mutation root changed while opening")
	}

	basePath, err := workspaceMutationBasePath(a.cfg.Root, a.cfg.CWD, req.Workdir)
	if err != nil {
		_ = root.Close()
		return nil, err
	}

	mutation := &workspaceMutation{
		access:          a,
		root:            root,
		rootPath:        rootPath,
		rootIdentity:    opened,
		basePath:        basePath,
		workdir:         req.Workdir,
		ctx:             ctx,
		authorization:   receipt,
		checkpoint:      checkpoint,
		committedByPath: make(map[string]struct{}),
		observedByPath:  make(map[string]workspaceMutationPreimage),
		initialByPath:   make(map[string]workspaceMutationPreimage),
	}
	locked = false

	return mutation, nil
}

func workspaceMutationBasePath(root string, cwd string, workdir string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	base := filepath.Clean(strings.TrimSpace(cwd))
	if base == "." || base == "" {
		base = root
	}

	workdir = strings.TrimSpace(workdir)
	if workdir != "" && workdir != "." {
		if filepath.IsAbs(workdir) {
			base = filepath.Clean(workdir)
		} else {
			base = filepath.Clean(filepath.Join(base, workdir))
		}
	}

	if _, ok := workspaceRelativePath(root, base); !ok {
		return "", fmt.Errorf("workdir %q escapes workspace root", workdir)
	}

	return base, nil
}

func (m *workspaceMutation) Close() {
	if m == nil {
		return
	}

	m.closeOnce.Do(func() {
		if m.root != nil {
			_ = m.root.Close()
		}
		if m.access != nil {
			m.access.mutationGate.Unlock()
		}
	})
}

func (m *workspaceMutation) checkContext() error {
	if m == nil || m.ctx == nil {
		return nil
	}
	return m.ctx.Err()
}

func (m *workspaceMutation) Resolve(path string) (workspaceMutationTarget, error) {
	if m == nil || m.root == nil {
		return workspaceMutationTarget{}, fmt.Errorf("workspace mutation is not open")
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return workspaceMutationTarget{}, fmt.Errorf("path is required")
	}

	identity, err := m.access.canonicalWorkspacePath(path, m.workdir)
	if err != nil {
		return workspaceMutationTarget{}, err
	}
	relative := filepath.FromSlash(identity.CanonicalID)
	if relative == "." {
		return workspaceMutationTarget{}, fmt.Errorf("workspace write path must stay under workspace root")
	}
	if m.authorization == nil {
		return workspaceMutationTarget{}, fmt.Errorf("%w: authorization receipt is absent", errWorkspaceMutationUnauthorized)
	}
	if _, authorized := m.authorization.targets[identity.CanonicalID]; !authorized {
		return workspaceMutationTarget{}, fmt.Errorf("%w: target %q was not authorized for this request", errWorkspaceMutationUnauthorized, identity.CanonicalID)
	}

	if err := m.validateResolvedTarget(relative); err != nil {
		return workspaceMutationTarget{}, err
	}

	return workspaceMutationTarget{
		canonicalID: identity.CanonicalID,
		relative:    relative,
		absolute:    identity.AbsolutePath,
		display:     identity.DisplayPath,
	}, nil
}

func (m *workspaceMutation) validateResolvedTarget(relative string) error {
	if err := m.validateRootPathIdentity(); err != nil {
		return err
	}

	components := workspaceMutationPathComponents(relative)
	current := ""
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := m.root.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect workspace mutation path %q: %w", filepath.ToSlash(current), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if index == len(components)-1 {
				return fmt.Errorf("workspace write path %q must not be a symlink", filepath.ToSlash(current))
			}

			return fmt.Errorf("workspace write path must not traverse symlinked directories: %q", filepath.ToSlash(current))
		}
		if index < len(components)-1 && !info.IsDir() {
			return fmt.Errorf("workspace write parent %q must be a directory", filepath.ToSlash(current))
		}
	}

	return nil
}

func workspaceMutationPathComponents(relative string) []string {
	relative = filepath.Clean(relative)
	if relative == "." || relative == "" {
		return nil
	}

	parts := strings.Split(relative, string(filepath.Separator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}

	return out
}

func (m *workspaceMutation) Observe(target workspaceMutationTarget) (workspaceMutationPreimage, error) {
	if err := m.validateRootPathIdentity(); err != nil {
		return workspaceMutationPreimage{}, err
	}

	parent, err := m.openParent(target, false)
	if errors.Is(err, errWorkspaceMutationParentMissing) {
		preimage := workspaceMutationPreimage{}
		if err := m.validateScopePreimage(target, preimage); err != nil {
			return workspaceMutationPreimage{}, err
		}
		m.recordObservedPreimage(target.canonicalID, preimage)
		return preimage, nil
	}
	if err != nil {
		return workspaceMutationPreimage{}, err
	}
	defer parent.Close()

	preimage, err := readWorkspaceMutationPreimage(parent.root, filepath.Base(target.relative))
	if err != nil {
		return workspaceMutationPreimage{}, err
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return workspaceMutationPreimage{}, err
	}
	if err := m.validateScopePreimage(target, preimage); err != nil {
		return workspaceMutationPreimage{}, err
	}
	m.recordObservedPreimage(target.canonicalID, preimage)

	return preimage, nil
}

func (m *workspaceMutation) recordObservedPreimage(canonicalID string, preimage workspaceMutationPreimage) {
	if m == nil {
		return
	}
	preimage = cloneWorkspaceMutationPreimage(preimage)
	if _, recorded := m.initialByPath[canonicalID]; !recorded {
		m.initialByPath[canonicalID] = preimage
	}
	m.observedByPath[canonicalID] = preimage
}

func (m *workspaceMutation) validateScopePreimage(target workspaceMutationTarget, preimage workspaceMutationPreimage) error {
	if m == nil || m.authorization == nil || !m.authorization.scopeBound {
		return nil
	}
	if m.authorization.scope.requiresAbsent(target.canonicalID) && preimage.exists {
		return fmt.Errorf("%w: authorized new file %s appeared after scope binding", errWorkspaceMutationStale, target.display)
	}
	return nil
}

func (m *workspaceMutation) ValidatePreimage(target workspaceMutationTarget, expected workspaceMutationPreimage) error {
	if err := m.checkContext(); err != nil {
		return err
	}
	if err := m.validateRootPathIdentity(); err != nil {
		return err
	}

	parent, err := m.openParent(target, false)
	if errors.Is(err, errWorkspaceMutationParentMissing) {
		if expected.exists {
			return fmt.Errorf("%w: target parent disappeared since preflight", errWorkspaceMutationStale)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := validateWorkspaceMutationPreimage(parent.root, filepath.Base(target.relative), expected); err != nil {
		return err
	}
	return parent.ValidatePathIdentity()
}

type workspaceMutationParent struct {
	mutation *workspaceMutation
	root     *os.Root
	relative string
	identity os.FileInfo
}

func (p *workspaceMutationParent) Close() {
	if p != nil && p.root != nil {
		_ = p.root.Close()
		p.root = nil
	}
}

func (p *workspaceMutationParent) ValidatePathIdentity() error {
	if p == nil || p.mutation == nil || p.root == nil {
		return fmt.Errorf("workspace mutation parent is not open")
	}
	if err := p.mutation.validateRootPathIdentity(); err != nil {
		return err
	}
	if err := p.mutation.validateDirectoryPathIdentity(p.relative, p.identity); err != nil {
		return err
	}
	opened, err := p.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect opened workspace mutation parent: %w", err)
	}
	if !os.SameFile(opened, p.identity) {
		return fmt.Errorf("workspace mutation parent handle identity changed")
	}

	return nil
}

func (m *workspaceMutation) openParent(target workspaceMutationTarget, create bool) (*workspaceMutationParent, error) {
	parentRelative := filepath.Dir(target.relative)
	currentRoot, err := m.root.OpenRoot(".")
	if err != nil {
		return nil, fmt.Errorf("open workspace mutation root handle: %w", err)
	}
	currentIdentity, err := currentRoot.Stat(".")
	if err != nil {
		_ = currentRoot.Close()
		return nil, fmt.Errorf("inspect workspace mutation root handle: %w", err)
	}
	currentRelative := "."

	for _, component := range workspaceMutationPathComponents(parentRelative) {
		info, lstatErr := currentRoot.Lstat(component)
		if os.IsNotExist(lstatErr) {
			if !create {
				_ = currentRoot.Close()
				return nil, fmt.Errorf("%w: %s", errWorkspaceMutationParentMissing, filepath.ToSlash(filepath.Join(currentRelative, component)))
			}
			if err := m.checkContext(); err != nil {
				_ = currentRoot.Close()
				return nil, err
			}
			createdRelative := filepath.Clean(filepath.Join(currentRelative, component))
			createdCanonicalID := filepath.ToSlash(createdRelative)
			if m.authorization != nil && m.authorization.scopeBound &&
				strings.TrimSpace(m.authorization.tool) != ToolNameMkdir &&
				!m.authorization.scope.allowsParentCreation(createdCanonicalID) {
				_ = currentRoot.Close()
				return nil, fmt.Errorf("workspace mutation scope does not authorize implicit parent directory creation %q", createdCanonicalID)
			}
			var exact bool
			info, exact, err = m.createDirectoryCommit(currentRoot, component, createdRelative, -1)
			if err != nil {
				_ = currentRoot.Close()
				return nil, fmt.Errorf("create workspace mutation directory %q: %w", filepath.ToSlash(filepath.Join(currentRelative, component)), err)
			}
			lstatErr = nil
			if exact {
				m.recordCommitPostimage(createdRelative, MutationCommitCreatedDirectory, mutationRollbackPostimage{
					exists: true, directory: true, identity: info, parentIdentity: currentIdentity,
					mode: info.Mode(), digest: sha256.Sum256(nil),
				})
			} else {
				m.recordCommit(createdRelative, MutationCommitCreatedDirectory)
				m.rollbackErr = errors.Join(m.rollbackErr, fmt.Errorf("exact directory publication is unavailable for %q", createdCanonicalID))
			}
			if err := atomicfile.SyncDirRoot(currentRoot, "."); err != nil {
				_ = currentRoot.Close()
				return nil, &atomicfile.PostCommitError{Op: "workspace directory created but parent sync failed", Err: err}
			}
		}
		if lstatErr != nil {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("inspect workspace mutation directory %q: %w", filepath.ToSlash(filepath.Join(currentRelative, component)), lstatErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("workspace write path must not traverse symlinked or non-directory parent %q", filepath.ToSlash(filepath.Join(currentRelative, component)))
		}

		nextRoot, err := currentRoot.OpenRoot(component)
		if err != nil {
			_ = currentRoot.Close()
			return nil, fmt.Errorf("open workspace mutation directory %q: %w", filepath.ToSlash(filepath.Join(currentRelative, component)), err)
		}
		opened, openErr := nextRoot.Stat(".")
		after, afterErr := currentRoot.Lstat(component)
		if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(info, opened) || !os.SameFile(opened, after) {
			_ = nextRoot.Close()
			_ = currentRoot.Close()
			if openErr != nil {
				return nil, fmt.Errorf("inspect opened workspace mutation directory: %w", openErr)
			}
			if afterErr != nil {
				return nil, fmt.Errorf("reinspect workspace mutation directory: %w", afterErr)
			}

			return nil, fmt.Errorf("workspace mutation directory changed while opening")
		}

		_ = currentRoot.Close()
		currentRoot = nextRoot
		currentIdentity = opened
		currentRelative = filepath.Clean(filepath.Join(currentRelative, component))
	}

	parent := &workspaceMutationParent{
		mutation: m,
		root:     currentRoot,
		relative: currentRelative,
		identity: currentIdentity,
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		parent.Close()
		return nil, err
	}

	return parent, nil
}

func (m *workspaceMutation) validateRootPathIdentity() error {
	if m == nil || m.root == nil {
		return fmt.Errorf("workspace mutation is not open")
	}

	pathInfo, err := os.Lstat(m.rootPath)
	if err != nil {
		return fmt.Errorf("inspect workspace mutation root path: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return fmt.Errorf("workspace mutation root path must remain a directory and must not be a symlink")
	}
	opened, err := m.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect open workspace mutation root: %w", err)
	}
	if !os.SameFile(pathInfo, m.rootIdentity) || !os.SameFile(opened, m.rootIdentity) {
		return fmt.Errorf("workspace mutation root path no longer names the opened workspace root")
	}

	return nil
}

func (m *workspaceMutation) validateDirectoryPathIdentity(relative string, identity os.FileInfo) error {
	if relative == "." {
		opened, err := m.root.Stat(".")
		if err != nil {
			return fmt.Errorf("inspect workspace mutation root directory: %w", err)
		}
		if !os.SameFile(opened, identity) {
			return fmt.Errorf("workspace mutation root directory identity changed")
		}

		return nil
	}

	current := ""
	for _, component := range workspaceMutationPathComponents(relative) {
		current = filepath.Join(current, component)
		info, err := m.root.Lstat(current)
		if err != nil {
			return fmt.Errorf("reinspect workspace mutation directory %q: %w", filepath.ToSlash(current), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("workspace mutation directory %q changed to a symlink or non-directory", filepath.ToSlash(current))
		}
	}
	info, err := m.root.Lstat(relative)
	if err != nil {
		return fmt.Errorf("reinspect workspace mutation parent %q: %w", filepath.ToSlash(relative), err)
	}
	if !os.SameFile(info, identity) {
		return fmt.Errorf("workspace mutation parent %q no longer names the opened directory", filepath.ToSlash(relative))
	}

	return nil
}

func readWorkspaceMutationPreimage(root *os.Root, base string) (workspaceMutationPreimage, error) {
	before, err := root.Lstat(base)
	if os.IsNotExist(err) {
		return workspaceMutationPreimage{}, nil
	}
	if err != nil {
		return workspaceMutationPreimage{}, fmt.Errorf("inspect workspace mutation target %q: %w", filepath.ToSlash(base), err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return workspaceMutationPreimage{}, fmt.Errorf("workspace mutation target %q must be a regular file and must not be a symlink", filepath.ToSlash(base))
	}

	file, err := root.Open(base)
	if err != nil {
		return workspaceMutationPreimage{}, fmt.Errorf("open workspace mutation target %q: %w", filepath.ToSlash(base), err)
	}
	defer file.Close()

	opened, openErr := file.Stat()
	after, afterErr := root.Lstat(base)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		if openErr != nil {
			return workspaceMutationPreimage{}, fmt.Errorf("inspect opened workspace mutation target: %w", openErr)
		}
		if afterErr != nil {
			return workspaceMutationPreimage{}, fmt.Errorf("reinspect workspace mutation target: %w", afterErr)
		}

		return workspaceMutationPreimage{}, fmt.Errorf("workspace mutation target changed while opening")
	}

	body, err := io.ReadAll(file)
	if err != nil {
		return workspaceMutationPreimage{}, fmt.Errorf("read workspace mutation target: %w", err)
	}
	finalInfo, err := root.Lstat(base)
	if err != nil || finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.Mode().IsRegular() || !os.SameFile(opened, finalInfo) {
		return workspaceMutationPreimage{}, fmt.Errorf("workspace mutation target changed while reading")
	}

	return workspaceMutationPreimage{
		exists: true,
		info:   opened,
		digest: sha256.Sum256(body),
		body:   body,
	}, nil
}

func validateWorkspaceMutationPreimage(root *os.Root, base string, expected workspaceMutationPreimage) error {
	current, err := readWorkspaceMutationPreimage(root, base)
	if err != nil {
		return err
	}
	if expected.exists != current.exists {
		return fmt.Errorf("%w: target existence changed since preflight", errWorkspaceMutationStale)
	}
	if !expected.exists {
		return nil
	}
	if !os.SameFile(expected.info, current.info) {
		return fmt.Errorf("%w: target identity changed since preflight", errWorkspaceMutationStale)
	}
	if expected.digest != current.digest {
		return fmt.Errorf("%w: target contents changed since preflight", errWorkspaceMutationStale)
	}

	return nil
}

// validateWorkspaceMutationAppendIdentity deliberately ignores the content
// digest. Appends to the same regular-file identity commute with append records
// from other processes; replacement or path appearance still invalidates the
// authority.
func validateWorkspaceMutationAppendIdentity(root *os.Root, base string, expected workspaceMutationPreimage) error {
	current, err := root.Lstat(base)
	if os.IsNotExist(err) {
		if expected.exists {
			return fmt.Errorf("%w: append target disappeared since preflight", errWorkspaceMutationStale)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return fmt.Errorf("workspace append target %q must be a regular file and must not be a symlink", filepath.ToSlash(base))
	}
	if !expected.exists {
		return fmt.Errorf("%w: append target appeared since preflight", errWorkspaceMutationStale)
	}
	if !os.SameFile(expected.info, current) {
		return fmt.Errorf("%w: append target identity changed since preflight", errWorkspaceMutationStale)
	}

	return nil
}

func (m *workspaceMutation) Replace(target workspaceMutationTarget, expected workspaceMutationPreimage, body []byte, perm os.FileMode, operationIndex int) error {
	parent, err := m.openParent(target, true)
	if err != nil {
		return err
	}
	defer parent.Close()

	base := filepath.Base(target.relative)
	if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
		return err
	}
	m.reachCheckpoint(workspaceMutationAfterParentBind, target, operationIndex)
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
		return err
	}

	m.reachCheckpoint(workspaceMutationBeforeCommit, target, operationIndex)
	var committedInfo os.FileInfo
	writeErr := atomicfile.WriteFileRootCheckedObserved(parent.root, base, body, perm, func() error {
		if err := m.checkContext(); err != nil {
			return err
		}
		if err := parent.ValidatePathIdentity(); err != nil {
			return err
		}
		if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
			return err
		}
		return m.checkContext()
	}, func(info os.FileInfo) {
		committedInfo = info
	})
	if committedInfo != nil {
		m.recordFileCommitPostimage(target.relative, MutationCommitReplacedFile, mutationRollbackPostimage{
			exists: true, identity: committedInfo, parentIdentity: parent.identity,
			mode: committedInfo.Mode(), digest: sha256.Sum256(body),
		}, body, true)
	}
	if writeErr != nil {
		if committedMutationError(writeErr) && committedInfo == nil {
			m.recordCommit(target.relative, MutationCommitReplacedFile)
		}

		return writeErr
	}
	if committedInfo == nil {
		m.recordCommit(target.relative, MutationCommitReplacedFile)
	}

	if err := parent.ValidatePathIdentity(); err != nil {
		return &atomicfile.PostCommitError{Op: "workspace file committed but parent identity changed", Err: err}
	}

	return nil
}

// Append binds an O_APPEND file descriptor to the preflight identity before
// writing. Existing files retain their inode, hardlinks, permissions, and
// unrelated concurrent append records. A newly created target is recorded as
// committed as soon as O_EXCL claims its directory entry.
func (m *workspaceMutation) Append(target workspaceMutationTarget, expected workspaceMutationPreimage, body []byte, perm os.FileMode, operationIndex int) error {
	parent, err := m.openParent(target, true)
	if err != nil {
		return err
	}
	defer parent.Close()

	base := filepath.Base(target.relative)
	if err := validateWorkspaceMutationAppendIdentity(parent.root, base, expected); err != nil {
		return err
	}
	m.reachCheckpoint(workspaceMutationAfterParentBind, target, operationIndex)
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateWorkspaceMutationAppendIdentity(parent.root, base, expected); err != nil {
		return err
	}

	m.reachCheckpoint(workspaceMutationBeforeCommit, target, operationIndex)
	if err := m.checkContext(); err != nil {
		return err
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateWorkspaceMutationAppendIdentity(parent.root, base, expected); err != nil {
		return err
	}
	if err := m.checkContext(); err != nil {
		return err
	}

	flags := os.O_RDWR | os.O_APPEND
	if !expected.exists {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := parent.root.OpenFile(base, flags, perm)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	opened, statErr := file.Stat()
	pathInfo, pathErr := parent.root.Lstat(base)
	if !expected.exists {
		m.recordCommit(target.relative, MutationCommitAppendedFile)
	}
	if statErr != nil || pathErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		err := errors.Join(statErr, pathErr)
		if err == nil {
			err = errors.New("append target identity changed while opening")
		}
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created but identity validation failed", Err: err}
		}
		return err
	}
	if !expected.exists {
		m.recordFileCommitPostimage(target.relative, MutationCommitAppendedFile, mutationRollbackPostimage{
			exists: true, identity: opened, parentIdentity: parent.identity,
			mode: opened.Mode(), digest: sha256.Sum256(nil),
		}, nil, true)
	}
	if expected.exists && !os.SameFile(expected.info, opened) {
		return fmt.Errorf("%w: append target identity changed since preflight", errWorkspaceMutationStale)
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created but parent identity changed", Err: err}
		}
		return err
	}

	if err := m.checkContext(); err != nil {
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created before cancellation", Err: err}
		}
		return err
	}
	pathInfo, err = parent.root.Lstat(base)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		if err == nil {
			err = errors.New("append target path no longer names the opened file")
		}
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created but identity changed before write", Err: err}
		}
		return err
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created but parent identity changed before write", Err: err}
		}
		return err
	}
	if err := m.checkContext(); err != nil {
		if !expected.exists {
			return &atomicfile.PostCommitError{Op: "append target created before cancellation", Err: err}
		}
		return err
	}

	appendEndOffset := int64(-1)
	written, writeErr := file.Write(body)
	if written > 0 {
		endOffset, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil || endOffset < int64(written) {
			m.recordCommit(target.relative, MutationCommitAppendedFile)
			if seekErr == nil {
				seekErr = fmt.Errorf("append offset is shorter than the committed segment")
			}
			m.rollbackErr = errors.Join(m.rollbackErr, fmt.Errorf("capture append rollback segment for %q: %w", target.display, seekErr))
		} else {
			appendEndOffset = endOffset
			m.recordAppendCommit(target.relative, endOffset-int64(written), body[:written])
		}
	}
	if writeErr == nil && written != len(body) {
		writeErr = io.ErrShortWrite
	}
	postimageCaptured := false
	if written > 0 || !expected.exists {
		committedPostimage, captureErr := readWorkspaceMutationPreimage(parent.root, base)
		expectedEnd := int64(0)
		if written > 0 {
			expectedEnd = appendEndOffset
		}
		if captureErr == nil && committedPostimage.exists && os.SameFile(opened, committedPostimage.info) && expectedEnd >= 0 && committedPostimage.info.Size() == expectedEnd {
			m.recordFileCommitPostimage(target.relative, MutationCommitAppendedFile, mutationRollbackPostimage{
				exists: true, identity: committedPostimage.info, parentIdentity: parent.identity,
				mode: committedPostimage.info.Mode(), digest: committedPostimage.digest,
			}, committedPostimage.body, true)
			postimageCaptured = true
		} else {
			if captureErr == nil {
				captureErr = fmt.Errorf("append postimage does not match the committed segment boundary")
			}
			m.rollbackErr = errors.Join(m.rollbackErr, fmt.Errorf("capture append rollback postimage for %q: %w", target.display, captureErr))
		}
	}
	if writeErr != nil {
		if written > 0 || !expected.exists {
			return &atomicfile.PostCommitError{Op: "append partially committed", Err: writeErr}
		}
		return writeErr
	}
	if err := file.Sync(); err != nil {
		if written > 0 || !expected.exists {
			return &atomicfile.PostCommitError{Op: "append committed but file sync failed", Err: err}
		}
		return err
	}
	if err := file.Close(); err != nil {
		closed = true
		if written > 0 || !expected.exists {
			return &atomicfile.PostCommitError{Op: "append committed but close failed", Err: err}
		}
		return err
	}
	closed = true

	if err := parent.ValidatePathIdentity(); err != nil {
		if written > 0 || !expected.exists {
			return &atomicfile.PostCommitError{Op: "append committed but parent identity changed", Err: err}
		}
		return err
	}
	pathInfo, err = parent.root.Lstat(base)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		if err == nil {
			err = errors.New("append target path changed after write")
		}
		if written > 0 || !expected.exists {
			return &atomicfile.PostCommitError{Op: "append committed but target identity changed", Err: err}
		}
		return err
	}
	if (written > 0 || !expected.exists) && !postimageCaptured {
		return &atomicfile.PostCommitError{Op: "append committed but rollback evidence capture failed", Err: m.rollbackErr}
	}
	if !expected.exists {
		if err := atomicfile.SyncDirRoot(parent.root, "."); err != nil {
			return &atomicfile.PostCommitError{Op: "append target created but parent sync failed", Err: err}
		}
	}

	return nil
}

func (m *workspaceMutation) Remove(target workspaceMutationTarget, expected workspaceMutationPreimage, operationIndex int) error {
	if !expected.exists {
		return fmt.Errorf("%w: delete target was missing during preflight", errWorkspaceMutationStale)
	}
	parent, err := m.openParent(target, false)
	if err != nil {
		return err
	}
	defer parent.Close()

	base := filepath.Base(target.relative)
	if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
		return err
	}
	m.reachCheckpoint(workspaceMutationAfterParentBind, target, operationIndex)
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
		return err
	}

	m.reachCheckpoint(workspaceMutationBeforeCommit, target, operationIndex)
	if err := m.checkContext(); err != nil {
		return err
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if err := validateWorkspaceMutationPreimage(parent.root, base, expected); err != nil {
		return err
	}
	if err := m.checkContext(); err != nil {
		return err
	}
	if err := parent.root.Remove(base); err != nil {
		return err
	}
	m.recordFileCommitPostimage(target.relative, MutationCommitRemovedFile, mutationRollbackPostimage{parentIdentity: parent.identity}, nil, false)
	if err := atomicfile.SyncDirRoot(parent.root, "."); err != nil {
		return &atomicfile.PostCommitError{Op: "workspace file removed but parent sync failed", Err: err}
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return &atomicfile.PostCommitError{Op: "workspace file removed but parent identity changed", Err: err}
	}

	return nil
}

func (m *workspaceMutation) MakeDir(target workspaceMutationTarget, operationIndex int) error {
	parent, err := m.openParent(target, true)
	if err != nil {
		return err
	}
	defer parent.Close()

	base := filepath.Base(target.relative)
	info, err := parent.root.Lstat(base)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("workspace directory target %q must be a directory and must not be a symlink", target.display)
		}

		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	m.reachCheckpoint(workspaceMutationAfterParentBind, target, operationIndex)
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if _, err := parent.root.Lstat(base); err == nil {
		return fmt.Errorf("%w: directory target appeared since preflight", errWorkspaceMutationStale)
	} else if !os.IsNotExist(err) {
		return err
	}

	m.reachCheckpoint(workspaceMutationBeforeCommit, target, operationIndex)
	if err := m.checkContext(); err != nil {
		return err
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return err
	}
	if _, err := parent.root.Lstat(base); err == nil {
		return fmt.Errorf("%w: directory target appeared since preflight", errWorkspaceMutationStale)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := m.checkContext(); err != nil {
		return err
	}
	created, exact, err := m.createDirectoryCommit(parent.root, base, target.relative, operationIndex)
	if err != nil {
		return err
	}
	if exact {
		m.recordCommitPostimage(target.relative, MutationCommitCreatedDirectory, mutationRollbackPostimage{
			exists: true, directory: true, identity: created, parentIdentity: parent.identity,
			mode: created.Mode(), digest: sha256.Sum256(nil),
		})
	} else {
		m.recordCommit(target.relative, MutationCommitCreatedDirectory)
		m.rollbackErr = errors.Join(m.rollbackErr, fmt.Errorf("exact directory publication is unavailable for %q", target.canonicalID))
	}
	if err := atomicfile.SyncDirRoot(parent.root, "."); err != nil {
		return &atomicfile.PostCommitError{Op: "workspace directory created but parent sync failed", Err: err}
	}
	if err := parent.ValidatePathIdentity(); err != nil {
		return &atomicfile.PostCommitError{Op: "workspace directory created but parent identity changed", Err: err}
	}

	return nil
}

func (m *workspaceMutation) createDirectoryCommit(parent *os.Root, target string, relative string, operationIndex int) (os.FileInfo, bool, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, false, err
	}
	temporary := fmt.Sprintf(".idiom-directory-%x", nonce[:])
	if err := parent.Mkdir(temporary, 0o755); err != nil {
		return nil, false, err
	}
	published := false
	defer func() {
		if !published {
			_ = parent.Remove(temporary)
		}
	}()
	created, err := parent.Lstat(temporary)
	if err != nil || created.Mode()&os.ModeSymlink != 0 || !created.IsDir() {
		return nil, false, fmt.Errorf("inspect private workspace mutation directory")
	}
	if m.checkpoint != nil {
		m.checkpoint(workspaceMutationCheckpoint{Stage: workspaceMutationBeforeDirectoryPublish, Path: filepath.ToSlash(relative), OperationIndex: operationIndex})
	}
	if err := m.checkContext(); err != nil {
		return nil, false, err
	}
	if err := publishWorkspaceMutationPath(parent, temporary, target); err != nil {
		if errors.Is(err, errWorkspaceMutationDirectoryPublishUnsupported) {
			_ = parent.Remove(temporary)
			if err := parent.Mkdir(target, 0o755); err != nil {
				return nil, false, err
			}
			published = true
			fallback, statErr := parent.Lstat(target)
			return fallback, false, statErr
		}
		return nil, false, err
	}
	published = true
	final, err := parent.Lstat(target)
	if err != nil || final.Mode()&os.ModeSymlink != 0 || !final.IsDir() || !os.SameFile(created, final) {
		if err == nil {
			err = fmt.Errorf("published directory identity changed")
		}
		return nil, true, &atomicfile.PostCommitError{Op: "workspace directory published but identity validation failed", Err: err}
	}
	return final, true, nil
}

func (m *workspaceMutation) reachCheckpoint(stage workspaceMutationCheckpointStage, target workspaceMutationTarget, operationIndex int) {
	if m == nil || m.checkpoint == nil {
		return
	}

	m.checkpoint(workspaceMutationCheckpoint{
		Stage:          stage,
		Path:           filepath.ToSlash(target.relative),
		OperationIndex: operationIndex,
	})
}

func (m *workspaceMutation) recordCommit(relative string, kind MutationCommitKind) {
	if m == nil {
		return
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == "" {
		return
	}
	path := filepath.ToSlash(relative)
	if _, ok := m.committedByPath[path]; ok {
		return
	}

	m.committedByPath[path] = struct{}{}
	if len(m.commits) == 0 && m.access != nil && m.access.mutationEpoch != nil {
		m.access.mutationEpoch.Add(1)
	}
	m.commits = append(m.commits, workspaceMutationCommit{path: path, kind: kind})
}

func (m *workspaceMutation) recordFileCommitPostimage(relative string, kind MutationCommitKind, postimage mutationRollbackPostimage, body []byte, exists bool) {
	m.recordCommitPostimage(relative, kind, postimage)
	path := filepath.ToSlash(filepath.Clean(relative))
	for index := range m.commits {
		if m.commits[index].path != path {
			continue
		}
		m.commits[index].fileBody = append([]byte(nil), body...)
		m.commits[index].fileBodyExact = true
		m.commits[index].fileExists = exists
		return
	}
}

func (m *workspaceMutation) recordCommitPostimage(relative string, kind MutationCommitKind, postimage mutationRollbackPostimage) {
	m.recordCommit(relative, kind)
	path := filepath.ToSlash(filepath.Clean(relative))
	for index := range m.commits {
		if m.commits[index].path != path {
			continue
		}
		m.commits[index].kind = kind
		m.commits[index].postimage = postimage
		m.commits[index].postimageExact = true
		return
	}
}

func (m *workspaceMutation) recordAppendCommit(relative string, start int64, body []byte) {
	if m == nil || start < 0 || len(body) == 0 {
		return
	}
	path := filepath.ToSlash(filepath.Clean(relative))
	for index := range m.commits {
		if m.commits[index].path != path {
			continue
		}
		m.commits[index].kind = MutationCommitAppendedFile
		m.commits[index].appendStart = start
		m.commits[index].appendLength = int64(len(body))
		m.commits[index].appendDigest = sha256.Sum256(body)
		m.commits[index].appendExact = true
		return
	}

	m.recordCommit(relative, MutationCommitAppendedFile)
	if len(m.commits) == 0 || m.commits[len(m.commits)-1].path != path {
		return
	}
	m.commits[len(m.commits)-1].appendStart = start
	m.commits[len(m.commits)-1].appendLength = int64(len(body))
	m.commits[len(m.commits)-1].appendDigest = sha256.Sum256(body)
	m.commits[len(m.commits)-1].appendExact = true
}

func (m *workspaceMutation) setFailureIntent(failed workspaceMutationTarget, unattempted []workspaceMutationTarget) {
	if m == nil {
		return
	}
	m.failedTargetID = failed.canonicalID
	m.unattemptedIDs = m.unattemptedIDs[:0]
	seen := make(map[string]struct{}, len(unattempted))
	for _, target := range unattempted {
		if target.canonicalID == "" {
			continue
		}
		if _, ok := seen[target.canonicalID]; ok {
			continue
		}
		seen[target.canonicalID] = struct{}{}
		m.unattemptedIDs = append(m.unattemptedIDs, target.canonicalID)
	}
}

func (m *workspaceMutation) CommittedPaths() []string {
	if m == nil {
		return nil
	}

	paths := make([]string, 0, len(m.commits))
	for _, commit := range m.commits {
		paths = append(paths, commit.path)
	}

	return paths
}

func (m *workspaceMutation) CommittedOutput() string {
	if m == nil || len(m.commits) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("committed before failure:\n")
	for _, commit := range m.commits {
		fmt.Fprintf(&b, "- %s: %s\n", commit.kind, commit.path)
	}

	return strings.TrimSpace(b.String())
}

func (m *Manager) workspaceMutationFailureResult(
	tool string,
	summary string,
	partialSummary string,
	err error,
	mutation *workspaceMutation,
	code failures.Code,
	reason failures.Reason,
	unattempted []string,
) ToolResult {
	if errors.Is(err, errWorkspaceMutationStale) {
		code = failures.FailedPrecondition
		reason = reasonPlanSubmissionRejected
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = failures.Canceled
		reason = reasonToolInternalFailure
	}

	result := toolFailureFromErrorResult(tool, summary, err, code, reason)
	committedPaths := mutation.CommittedPaths()
	if len(committedPaths) > 0 {
		result.Summary = partialSummary
		result.ChangedPaths = committedPaths
		if m != nil {
			m.resetLanguageTools()
		}
	}

	var output strings.Builder
	if committed := mutation.CommittedOutput(); committed != "" {
		output.WriteString(committed)
		output.WriteString("\n")
	}
	if err != nil {
		output.WriteString("failed operation: ")
		output.WriteString(err.Error())
		output.WriteString("\n")
	}
	if len(unattempted) > 0 {
		output.WriteString("unattempted after failure:\n")
		for _, item := range unattempted {
			if item = strings.TrimSpace(item); item != "" {
				fmt.Fprintf(&output, "- %s\n", item)
			}
		}
	}
	result.Output = strings.TrimSpace(output.String())
	mutation.attachCommitReceipt(tool, &result)

	return result
}

func committedMutationError(err error) bool {
	if err == nil {
		return false
	}

	var committed interface{ Committed() bool }
	return errors.As(err, &committed) && committed.Committed()
}
