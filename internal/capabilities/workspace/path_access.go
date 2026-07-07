package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"idiom/internal/platform/config"
	"idiom/internal/platform/safepath"
)

// PathAccess owns workspace path resolution, display projection, and
// session-scoped read approval for paths outside the configured workspace.
type PathAccess struct {
	cfg config.Workspace

	mu                        sync.Mutex
	systemSearchApprover      SystemSearchAccessApprover
	approvedSystemSearchRoots map[string]SystemSearchAccessScope
}

type systemSearchAccessGrant struct {
	Path  string
	Scope SystemSearchAccessScope
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

	return candidate, nil
}

func (a *PathAccess) ResolveWritePath(path string, workdir string) (string, error) {
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

	fullPath, _, err := safepath.UnderRoot(a.cfg.Root, candidate, "workspace write path", safepath.Options{
		AllowMissing:           true,
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return "", err
	}

	return fullPath, nil
}

func (a *PathAccess) ResolveReadPath(path string, workdir string) (string, error) {
	candidate, err := a.candidatePath(path, workdir, true)
	if err != nil {
		return "", err
	}

	if a.PathWithinWorkspace(candidate) {
		return candidate, nil
	}

	if _, ok := a.ApprovedSystemSearchScope(candidate); ok {
		return candidate, nil
	}

	return "", fmt.Errorf("path %q escapes workspace root and has not been approved for system search", path)
}

func (a *PathAccess) ResolveReadWorkdir(workdir string) (string, error) {
	if !a.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	workdir = strings.TrimSpace(workdir)
	if workdir == "" || workdir == "." {
		return a.cfg.CWD, nil
	}

	return a.ResolveReadPath(workdir, "")
}

func (a *PathAccess) SystemSearchCandidate(path string, workdir string) (string, error) {
	return a.candidatePath(path, workdir, false)
}

func (a *PathAccess) candidatePath(path string, workdir string, allowApprovedWorkdir bool) (string, error) {
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
			base, err = a.ResolveReadWorkdir(workdir)
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

	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`) {
		return rel, true
	}

	evaluatedRoot, rootErr := filepath.EvalSymlinks(root)
	evaluatedPath, pathErr := filepath.EvalSymlinks(path)

	if rootErr != nil || pathErr != nil {
		return "", false
	}

	rel, err := filepath.Rel(evaluatedRoot, evaluatedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`) {
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

	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
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

func (a *PathAccess) ClearSystemSearchApprovals() {
	if a == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.approvedSystemSearchRoots = nil
}

func (a *PathAccess) ApprovedSystemSearchGrantsSnapshot() []systemSearchAccessGrant {
	if a == nil {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	grants := make([]systemSearchAccessGrant, 0, len(a.approvedSystemSearchRoots))
	for root, scope := range a.approvedSystemSearchRoots {
		grants = append(grants, systemSearchAccessGrant{Path: root, Scope: normalizeSystemSearchAccessScope(scope)})
	}
	slices.SortFunc(grants, func(a, b systemSearchAccessGrant) int {
		if a.Path == b.Path {
			return strings.Compare(string(a.Scope), string(b.Scope))
		}

		return strings.Compare(a.Path, b.Path)
	})

	return grants
}

func (a *PathAccess) RecordApprovedSystemSearchRoot(root string, scope SystemSearchAccessScope) {
	if a == nil {
		return
	}

	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.approvedSystemSearchRoots == nil {
		a.approvedSystemSearchRoots = make(map[string]SystemSearchAccessScope)
	}

	scope = normalizeSystemSearchAccessScope(scope)
	if existing := normalizeSystemSearchAccessScope(a.approvedSystemSearchRoots[root]); existing == SystemSearchAccessScopeContents {
		return
	}

	a.approvedSystemSearchRoots[root] = scope
}

func (a *PathAccess) ApprovedSystemSearchScope(path string) (SystemSearchAccessScope, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", false
	}

	found := false
	best := SystemSearchAccessScopeSelected

	for _, grant := range a.ApprovedSystemSearchGrantsSnapshot() {
		root := filepath.Clean(grant.Path)
		scope := normalizeSystemSearchAccessScope(grant.Scope)
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

func (a *PathAccess) SelectedSystemSearchDirectoryAccess(path string, info os.FileInfo) bool {
	if a == nil || info == nil || !info.IsDir() || a.PathWithinWorkspace(path) {
		return false
	}

	scope, ok := a.ApprovedSystemSearchScope(path)
	return ok && scope == SystemSearchAccessScopeSelected
}
