package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/platform/textutil"
)

// SystemSearchAccessApprover asks the host/operator whether a session may
// inspect a filesystem target outside the configured workspace. Approval is
// session-scoped and read-only; write path resolution never consults it.
type SystemSearchAccessApprover interface {
	ApproveSystemSearchAccess(ctx context.Context, req SystemSearchAccessRequest) (SystemSearchAccessDecision, error)
}

type SystemSearchAccessRequest struct {
	SessionID     string
	Path          string
	DisplayPath   string
	TargetKind    string
	RequestTool   string
	RequestReason string
}

type SystemSearchAccessScope string

const (
	SystemSearchAccessScopeSelected SystemSearchAccessScope = "selected"
	SystemSearchAccessScopeContents SystemSearchAccessScope = "contents"
)

type SystemSearchAccessDecision struct {
	Approved bool
	Scope    SystemSearchAccessScope
}

type systemSearchAccessGrant struct {
	Path  string
	Scope SystemSearchAccessScope
}

func (m *Manager) SetSystemSearchAccessApprover(approver SystemSearchAccessApprover) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.systemSearchApprover = approver
}

func (m *Manager) approvedSystemSearchGrantsSnapshot() []systemSearchAccessGrant {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	grants := make([]systemSearchAccessGrant, 0, len(m.approvedSystemSearchRoots))
	for root, scope := range m.approvedSystemSearchRoots {
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

func (m *Manager) requestSystemSearchAccess(ctx context.Context, req ToolRequest) ToolResult {
	target, err := m.systemSearchCandidate(req.Path, req.Workdir)
	if err != nil {
		return toolInvalidArgumentResult(ToolNameSystemSearchAccess, "System search access rejected", err.Error())
	}

	info, err := os.Stat(target)
	if err != nil {
		return toolPathResolutionFailureResult(ToolNameSystemSearchAccess, "System search access rejected", err)
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}

	target = filepath.Clean(target)
	display := m.displayPath(target)
	if m.pathWithinWorkspace(target) {
		return ToolResult{
			Tool:    ToolNameSystemSearchAccess,
			Summary: "System search access already available",
			Output:  display,
		}
	}
	targetKind := "file"
	if info.IsDir() {
		targetKind = "directory"
	}

	if scope, ok := m.approvedSystemSearchScope(target); ok && (scope == SystemSearchAccessScopeContents || !info.IsDir()) {
		return ToolResult{
			Tool:    ToolNameSystemSearchAccess,
			Summary: "System search access already approved",
			Output:  display,
		}
	}

	m.mu.Lock()
	approver := m.systemSearchApprover
	m.mu.Unlock()
	if approver == nil {
		return toolPermissionPolicyResult(ToolNameSystemSearchAccess, "System search access requires approval", "no interactive approver is available for broad system search")
	}

	decision, err := approver.ApproveSystemSearchAccess(ctx, SystemSearchAccessRequest{
		SessionID:     m.SessionID(),
		Path:          target,
		DisplayPath:   display,
		TargetKind:    targetKind,
		RequestTool:   strings.TrimSpace(req.Tool),
		RequestReason: strings.TrimSpace(textutil.FirstNonEmpty(req.Purpose, req.NamedGap, req.WhyNotAnswering, req.Query, req.Pattern)),
	})
	if err != nil {
		return toolPermissionPolicyResult(ToolNameSystemSearchAccess, "System search access denied", err.Error())
	}
	if !decision.Approved {
		return toolPermissionPolicyResult(ToolNameSystemSearchAccess, "System search access denied", "operator denied broad system search for "+display)
	}

	scope := normalizeSystemSearchAccessScope(decision.Scope)
	if !info.IsDir() && scope == SystemSearchAccessScopeContents {
		scope = SystemSearchAccessScopeSelected
	}

	m.recordApprovedSystemSearchRoot(target, scope)

	summary := "System search access approved for selected path"
	if scope == SystemSearchAccessScopeContents {
		summary = "System search access approved for folder contents"
	}

	return ToolResult{
		Tool:    ToolNameSystemSearchAccess,
		Summary: summary,
		Output:  fmt.Sprintf("%s scope=%s", display, scope),
	}
}

func (m *Manager) recordApprovedSystemSearchRoot(root string, scope SystemSearchAccessScope) {
	if m == nil {
		return
	}

	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.approvedSystemSearchRoots == nil {
		m.approvedSystemSearchRoots = make(map[string]SystemSearchAccessScope)
	}

	scope = normalizeSystemSearchAccessScope(scope)
	if existing := normalizeSystemSearchAccessScope(m.approvedSystemSearchRoots[root]); existing == SystemSearchAccessScopeContents {
		return
	}

	m.approvedSystemSearchRoots[root] = scope
}

func (m *Manager) resolveReadPath(path string, workdir string) (string, error) {
	candidate, err := m.candidatePath(path, workdir, true)
	if err != nil {
		return "", err
	}

	if m.pathWithinWorkspace(candidate) {
		return candidate, nil
	}

	if _, ok := m.approvedSystemSearchScope(candidate); ok {
		return candidate, nil
	}

	return "", fmt.Errorf("path %q escapes workspace root and has not been approved for system search", path)
}

func (m *Manager) resolveReadWorkdir(workdir string) (string, error) {
	if !m.Enabled() {
		return "", fmt.Errorf("workspace root is not configured")
	}

	workdir = strings.TrimSpace(workdir)
	if workdir == "" || workdir == "." {
		return m.cfg.CWD, nil
	}

	return m.resolveReadPath(workdir, "")
}

func (m *Manager) systemSearchCandidate(path string, workdir string) (string, error) {
	return m.candidatePath(path, workdir, false)
}

func (m *Manager) candidatePath(path string, workdir string, allowApprovedWorkdir bool) (string, error) {
	if !m.Enabled() {
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
			base, err = m.resolveReadWorkdir(workdir)
		} else {
			base, err = m.resolveWorkdir(workdir)
		}
		if err != nil {
			return "", err
		}

		return filepath.Clean(filepath.Join(base, path)), nil
	default:
		return filepath.Clean(filepath.Join(m.cfg.CWD, path)), nil
	}
}

func (m *Manager) approvedSystemSearchScope(path string) (SystemSearchAccessScope, bool) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return "", false
	}

	found := false
	best := SystemSearchAccessScopeSelected

	for _, grant := range m.approvedSystemSearchGrantsSnapshot() {
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

func normalizeSystemSearchAccessScope(scope SystemSearchAccessScope) SystemSearchAccessScope {
	switch scope {
	case SystemSearchAccessScopeContents:
		return SystemSearchAccessScopeContents
	default:
		return SystemSearchAccessScopeSelected
	}
}

func systemSearchGrantCoversPath(root string, path string, scope SystemSearchAccessScope) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	if root == "" || path == "" {
		return false
	}
	if scope == SystemSearchAccessScopeSelected {
		return root == path
	}

	return pathLexicallyUnderRoot(root, path)
}

func (m *Manager) selectedSystemSearchDirectoryAccess(path string, info os.FileInfo) bool {
	if m == nil || info == nil || !info.IsDir() || m.pathWithinWorkspace(path) {
		return false
	}

	scope, ok := m.approvedSystemSearchScope(path)
	return ok && scope == SystemSearchAccessScopeSelected
}

func selectedSystemSearchDirectoryDeniedResult(tool string, path string) ToolResult {
	return toolPermissionPolicyResult(
		tool,
		"Selected path access does not include folder contents",
		fmt.Sprintf("operator approved only the selected directory %s; request blanket folder-content access before recursively inspecting descendants", path),
	)
}

func pathLexicallyUnderRoot(root string, path string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	path = filepath.Clean(strings.TrimSpace(path))
	if root == "" || path == "" {
		return false
	}
	if path == root {
		return true
	}

	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !strings.HasPrefix(rel, `..\`)
}
