package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func (m *Manager) SetSystemSearchAccessApprover(approver SystemSearchAccessApprover) {
	if m == nil {
		return
	}

	m.pathAccess.SetSystemSearchAccessApprover(approver)
}

func (m *Manager) requestSystemSearchAccess(ctx context.Context, req ToolRequest) ToolResult {
	target, err := m.pathAccess.SystemSearchCandidate(req.Path, req.Workdir)
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
	display := m.pathAccess.DisplayPath(target)
	if m.pathAccess.PathWithinWorkspace(target) {
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

	if scope, ok := m.pathAccess.ApprovedSystemSearchScope(target); ok && (scope == SystemSearchAccessScopeContents || !info.IsDir()) {
		return ToolResult{
			Tool:    ToolNameSystemSearchAccess,
			Summary: "System search access already approved",
			Output:  display,
		}
	}

	approver := m.pathAccess.SystemSearchAccessApprover()
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

	m.pathAccess.RecordApprovedSystemSearchRoot(target, scope)

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
