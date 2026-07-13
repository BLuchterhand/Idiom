package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"idiom/internal/platform/config"
)

// PathAccess owns workspace path resolution, mutation serialization, display
// projection, and session-scoped read approval outside the configured root.
type PathAccess struct {
	cfg config.Workspace

	mu                        sync.Mutex
	mutationGate              workspaceMutationGate
	systemSearchApprover      SystemSearchAccessApprover
	approvedSystemSearchRoots map[executionSessionGrantKey]map[string]systemSearchAccessGrant
	mutationEpoch             *atomic.Uint64
}

type systemSearchAccessGrant struct {
	Path     string
	Scope    SystemSearchAccessScope
	Identity os.FileInfo
}

func NewPathAccess(cfg config.Workspace) PathAccess {
	return PathAccess{cfg: cfg}
}

func (a *PathAccess) Enabled() bool {
	return a != nil && a.cfg.Root != ""
}

func (a *PathAccess) ResolveWorkdir(workdir string) (string, error) {
	if !a.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	base := a.cfg.CWD
	if strings.TrimSpace(workdir) == "" || workdir == "." {
		return base, nil
	}

	return a.ResolvePath(workdir, "")
}

func (a *PathAccess) ResolvePath(path string, workdir string) (string, error) {
	if !a.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}

	var candidate string

	switch {
	case filepath.IsAbs(path):
		candidate = filepath.Clean(path)
	case workdir != "":
		base, err := a.ResolveWorkdir(workdir)
		if err != nil {
			return "", err
		}

		candidate = filepath.Clean(filepath.Join(base, path))
	default:
		candidate = filepath.Clean(filepath.Join(a.cfg.CWD, path))
	}

	rel, err := filepath.Rel(a.cfg.Root, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace root", path)
	}
	if !a.PathWithinWorkspace(candidate) {
		return "", fmt.Errorf("path %q escapes workspace root through a symlink", path)
	}

	return candidate, nil
}

func (a *PathAccess) ResolveReadPath(identity executionSessionIdentity, path string, workdir string) (string, error) {
	candidate, err := a.candidatePath(identity, path, workdir, true)
	if err != nil {
		return "", err
	}

	if a.PathWithinWorkspace(candidate) {
		return candidate, nil
	}

	if _, ok := a.ApprovedSystemSearchScope(identity, candidate); ok {
		return candidate, nil
	}

	return "", fmt.Errorf("path %q escapes workspace root and has not been approved for system search", path)
}

func (a *PathAccess) ResolveReadWorkdir(identity executionSessionIdentity, workdir string) (string, error) {
	if !a.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	workdir = strings.TrimSpace(workdir)
	if workdir == "" || workdir == "." {
		return a.cfg.CWD, nil
	}

	return a.ResolveReadPath(identity, workdir, "")
}

func (a *PathAccess) inspectSystemSearchCandidate(path string, workdir string) (string, os.FileInfo, error) {
	candidate, err := a.candidatePath(executionSessionIdentity{}, path, workdir, false)
	if err != nil {
		return "", nil, err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", nil, err
	}
	resolved = filepath.Clean(resolved)
	identity, err := inspectFilesystemPathIdentity(resolved)
	if err != nil {
		return "", nil, err
	}

	return resolved, identity, nil
}

func (a *PathAccess) candidatePath(identity executionSessionIdentity, path string, workdir string, allowApprovedWorkdir bool) (string, error) {
	if !a.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	switch {
	case filepath.IsAbs(path):
		return filepath.Clean(path), nil
	case strings.TrimSpace(workdir) != "":
		var base string
		var err error
		if allowApprovedWorkdir {
			base, err = a.ResolveReadWorkdir(identity, workdir)
		} else {
			base, err = a.ResolveWorkdir(workdir)
		}
		if err != nil {
			return "", err
		}

		return filepath.Clean(filepath.Join(base, path)), nil
	default:
		return filepath.Clean(filepath.Join(a.cfg.CWD, path)), nil
	}
}

func (a *PathAccess) DisplayPath(path string) string {
	if path == "" {
		return ""
	}

	if rel, ok := a.RelativeWorkspacePath(path); ok && rel != "." {
		return rel
	}

	return path
}

func (a *PathAccess) PathWithinWorkspace(path string) bool {
	_, ok := a.RelativeWorkspacePath(path)
	return ok
}

func (a *PathAccess) RelativeWorkspacePath(path string) (string, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	root := filepath.Clean(strings.TrimSpace(a.cfg.Root))

	if path == "" || root == "" {
		return "", false
	}

	lexicalRel, lexicalWithin := workspaceRelativePath(root, path)

	evaluatedRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		return "", false
	}
	evaluatedPath, pathResolved := resolvePathForWorkspaceContainment(path)
	if !pathResolved {
		return "", false
	}

	evaluatedRel, evaluatedWithin := workspaceRelativePath(evaluatedRoot, evaluatedPath)
	if !evaluatedWithin {
		return "", false
	}

	if lexicalWithin {
		return lexicalRel, true
	}

	return evaluatedRel, true
}

func resolvePathForWorkspaceContainment(path string) (string, bool) {
	current := filepath.Clean(path)
	var missingComponents []string

	for {
		if evaluated, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{evaluated}, missingComponents...)
			return filepath.Join(parts...), true
		} else if !os.IsNotExist(err) {
			return "", false
		}

		if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", false
		} else if err != nil && !os.IsNotExist(err) {
			return "", false
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}

		missingComponents = append([]string{filepath.Base(current)}, missingComponents...)
		current = parent
	}
}

func workspaceRelativePath(root string, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}

	return rel, true
}

func (a *PathAccess) ProtectedPathKeys(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	cleaned := filepath.Clean(path)
	keys := []string{normalizePathKey(cleaned)}

	if filepath.IsAbs(cleaned) {
		if root := strings.TrimSpace(a.cfg.Root); root != "" {
			if rel, err := filepath.Rel(root, cleaned); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				keys = append(keys, normalizePathKey(rel))
			}
		}
	} else if root := strings.TrimSpace(a.cfg.Root); root != "" {
		keys = append(keys, normalizePathKey(filepath.Join(root, cleaned)))
	}

	canonicalCandidate := cleaned
	if !filepath.IsAbs(canonicalCandidate) && strings.TrimSpace(a.cfg.Root) != "" {
		canonicalCandidate = filepath.Join(a.cfg.Root, canonicalCandidate)
	}
	keys = append(keys, a.canonicalWorkspacePathKeys(canonicalCandidate, "")...)

	return uniquePathKeys(keys)
}

// canonicalWorkspacePathKeys returns canonical relative and absolute matcher
// keys for a path whose resolved target remains inside the workspace. This is
// intentionally separate from display projection: policy evaluates aliases
// against their real workspace target without exposing that target to callers.
func (a *PathAccess) canonicalWorkspacePathKeys(path string, workdir string) []string {
	if !a.Enabled() || strings.TrimSpace(path) == "" {
		return nil
	}

	base := strings.TrimSpace(a.cfg.CWD)
	if base == "" {
		base = strings.TrimSpace(a.cfg.Root)
	}

	cleanedWorkdir := strings.TrimSpace(workdir)
	if cleanedWorkdir != "" {
		if filepath.IsAbs(cleanedWorkdir) {
			base = filepath.Clean(cleanedWorkdir)
		} else {
			base = filepath.Join(base, cleanedWorkdir)
		}
	}

	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}

	evaluatedRoot, err := filepath.EvalSymlinks(filepath.Clean(a.cfg.Root))
	if err != nil {
		return nil
	}

	evaluatedCandidate, ok := resolvePathForWorkspaceContainment(candidate)
	if !ok {
		return nil
	}

	rel, within := workspaceRelativePath(evaluatedRoot, evaluatedCandidate)
	if !within {
		return nil
	}

	return uniquePathKeys([]string{rel, evaluatedCandidate})
}

func uniquePathKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = normalizePathKey(key)
		if key == "" {
			continue
		}

		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		out = append(out, key)
	}

	return out
}

func (a *PathAccess) SetSystemSearchAccessApprover(approver SystemSearchAccessApprover) {
	if a == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.systemSearchApprover = approver
}

func (a *PathAccess) SystemSearchAccessApprover() SystemSearchAccessApprover {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	return a.systemSearchApprover
}

func (a *PathAccess) ApprovedSystemSearchGrantsSnapshot(identity executionSessionIdentity) []systemSearchAccessGrant {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	sessionGrants := a.approvedSystemSearchRoots[identity.grantKey()]
	grants := make([]systemSearchAccessGrant, 0, len(sessionGrants))
	for _, grant := range sessionGrants {
		grant.Scope = normalizeSystemSearchAccessScope(grant.Scope)
		grants = append(grants, grant)
	}
	slices.SortFunc(grants, func(a, b systemSearchAccessGrant) int {
		if a.Path == b.Path {
			return strings.Compare(string(a.Scope), string(b.Scope))
		}

		return strings.Compare(a.Path, b.Path)
	})

	return grants
}

func (a *PathAccess) recordApprovedSystemSearchGrant(identity executionSessionIdentity, grant systemSearchAccessGrant) error {
	if a == nil {
		return fmt.Errorf("workspace path access is not configured")
	}

	grant.Path = filepath.Clean(strings.TrimSpace(grant.Path))
	if grant.Path == "" || grant.Identity == nil {
		return fmt.Errorf("system search grant identity is incomplete")
	}
	current, err := inspectFilesystemPathIdentity(grant.Path)
	if err != nil {
		return fmt.Errorf("revalidate approved system search target: %w", err)
	}
	if !os.SameFile(grant.Identity, current) {
		return fmt.Errorf("approved system search target changed before the grant was recorded")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.approvedSystemSearchRoots == nil {
		a.approvedSystemSearchRoots = make(map[executionSessionGrantKey]map[string]systemSearchAccessGrant)
	}
	grantKey := identity.grantKey()
	if a.approvedSystemSearchRoots[grantKey] == nil {
		a.approvedSystemSearchRoots[grantKey] = make(map[string]systemSearchAccessGrant)
	}

	sessionGrants := a.approvedSystemSearchRoots[grantKey]
	grant.Scope = normalizeSystemSearchAccessScope(grant.Scope)
	if existing, ok := sessionGrants[grant.Path]; ok &&
		existing.Identity != nil && os.SameFile(existing.Identity, grant.Identity) &&
		normalizeSystemSearchAccessScope(existing.Scope) == SystemSearchAccessScopeContents {

		return nil
	}

	sessionGrants[grant.Path] = grant

	return nil
}

func (a *PathAccess) ApprovedSystemSearchScope(identity executionSessionIdentity, path string) (SystemSearchAccessScope, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", false
	}

	found := false
	best := SystemSearchAccessScopeSelected

	for _, grant := range a.ApprovedSystemSearchGrantsSnapshot(identity) {
		root := filepath.Clean(grant.Path)
		scope := normalizeSystemSearchAccessScope(grant.Scope)
		currentRoot, rootErr := inspectFilesystemPathIdentity(root)
		if rootErr != nil || grant.Identity == nil || !os.SameFile(grant.Identity, currentRoot) {
			continue
		}
		evaluatedRoot, rootErr := filepath.EvalSymlinks(root)
		evaluatedPath, pathErr := filepath.EvalSymlinks(path)
		if rootErr == nil && pathErr == nil {
			if systemSearchGrantCoversPath(evaluatedRoot, evaluatedPath, scope) {
				if scope == SystemSearchAccessScopeContents {
					return scope, true
				}
				found = true
			}

			continue
		}

		if !systemSearchGrantCoversPath(root, path, scope) {
			continue
		}

		if pathErr != nil && os.IsNotExist(pathErr) && scope != SystemSearchAccessScopeContents {
			continue
		}

		if scope == SystemSearchAccessScopeContents {
			return scope, true
		}

		found = true
	}

	return best, found
}

func inspectFilesystemPathIdentity(path string) (os.FileInfo, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil, fmt.Errorf("filesystem identity path is empty")
	}

	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("filesystem identity path must not be a symlink")
	}
	if !before.IsDir() && !before.Mode().IsRegular() {
		return nil, fmt.Errorf("filesystem identity path must be a regular file or directory")
	}

	var opened os.FileInfo
	if before.IsDir() {
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		opened, err = root.Stat(".")
		if err != nil {
			return nil, err
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		opened, err = file.Stat()
		if err != nil {
			return nil, err
		}
	}

	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("filesystem identity path changed while opening")
	}

	return opened, nil
}

func (a *PathAccess) SelectedSystemSearchDirectoryAccess(identity executionSessionIdentity, path string, info os.FileInfo) bool {
	if a == nil || info == nil || !info.IsDir() || a.PathWithinWorkspace(path) {
		return false
	}

	scope, ok := a.ApprovedSystemSearchScope(identity, path)
	return ok && scope == SystemSearchAccessScopeSelected
}
