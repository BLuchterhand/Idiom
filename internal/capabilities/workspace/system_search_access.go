package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"idiom/internal/platform/textutil"
)

//go:generate go tool mockgen -source=system_search_access.go -destination=mock_system_search_access_approver_test.go -package=workspace -typed

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

type systemSearchAccessApproverContextKey struct{}

type boundSystemSearchAccessApprover struct {
	approver SystemSearchAccessApprover
}

// WithSystemSearchAccessApprover binds operator routing to one invocation.
// Binding a nil approver explicitly disables fallback to Manager state.
func WithSystemSearchAccessApprover(ctx context.Context, approver SystemSearchAccessApprover) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, systemSearchAccessApproverContextKey{}, boundSystemSearchAccessApprover{approver: approver})
}

func systemSearchAccessApproverFromContext(ctx context.Context) (SystemSearchAccessApprover, bool) {
	if ctx == nil {
		return nil, false
	}

	bound, ok := ctx.Value(systemSearchAccessApproverContextKey{}).(boundSystemSearchAccessApprover)
	if !ok {
		return nil, false
	}

	return bound.approver, true
}

func (m *Manager) snapshotSystemSearchAccessApprover(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bound := systemSearchAccessApproverFromContext(ctx); bound {
		return ctx
	}

	var approver SystemSearchAccessApprover
	if m != nil {
		approver = m.pathAccess.SystemSearchAccessApprover()
	}

	return WithSystemSearchAccessApprover(ctx, approver)
}

func (m *Manager) SetSystemSearchAccessApprover(approver SystemSearchAccessApprover) {
	if m == nil {
		return
	}

	m.pathAccess.SetSystemSearchAccessApprover(approver)
}

func (m *Manager) requestSystemSearchAccess(ctx context.Context, req ToolRequest) ToolResult {
	identity, _ := executionSessionIdentityFromContext(ctx)
	approver, approverBound := systemSearchAccessApproverFromContext(ctx)
	if !approverBound {
		approver = m.pathAccess.SystemSearchAccessApprover()
	}
	if identity.explicit && identity.sessionID == "" {
		return toolInvalidArgumentResult(ToolNameSystemSearchAccess, "System search access rejected", "execution session id is required")
	}

	target, info, err := m.pathAccess.inspectSystemSearchCandidate(req.Path, req.Workdir)
	if err != nil {
		return toolInvalidArgumentResult(ToolNameSystemSearchAccess, "System search access rejected", err.Error())
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

	if scope, ok := m.pathAccess.ApprovedSystemSearchScope(identity, target); ok && (scope == SystemSearchAccessScopeContents || !info.IsDir()) {
		return ToolResult{
			Tool:    ToolNameSystemSearchAccess,
			Summary: "System search access already approved",
			Output:  display,
		}
	}

	if approver == nil {
		return toolPermissionPolicyResult(ToolNameSystemSearchAccess, "System search access requires approval", "no interactive approver is available for broad system search")
	}

	decision, err := approver.ApproveSystemSearchAccess(ctx, SystemSearchAccessRequest{
		SessionID:     identity.sessionID,
		Path:          target,
		DisplayPath:   display,
		TargetKind:    targetKind,
		RequestTool:   strings.TrimSpace(req.Tool),
		RequestReason: strings.TrimSpace(textutil.FirstNonEmpty(req.Purpose, req.NamedGap, req.WhyNotAnswering, req.Query, req.Pattern)),
	})
	if ctx != nil && ctx.Err() != nil {
		return toolPermissionPolicyResult(ToolNameSystemSearchAccess, "System search access canceled", "the request ended before approval could be accepted")
	}
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

	summary := "System search access approved for selected path"
	if scope == SystemSearchAccessScopeContents {
		summary = "System search access approved for folder contents"
	}

	return ToolResult{
		Tool:                     ToolNameSystemSearchAccess,
		Summary:                  summary,
		Output:                   fmt.Sprintf("%s scope=%s", display, scope),
		pendingSystemSearchGrant: &systemSearchAccessGrant{Path: target, Scope: scope, Identity: info},
	}
}

func (m *Manager) commitAcceptedSystemSearchGrant(identity executionSessionIdentity, result *ToolResult) {
	if m == nil || result == nil || result.pendingSystemSearchGrant == nil {
		return
	}

	grant := *result.pendingSystemSearchGrant
	result.pendingSystemSearchGrant = nil
	if err := m.pathAccess.recordApprovedSystemSearchGrant(identity, grant); err != nil {
		*result = toolPermissionPolicyResult(
			ToolNameSystemSearchAccess,
			"System search access approval became stale",
			err.Error()+"; request approval again for the current filesystem target",
		)
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
