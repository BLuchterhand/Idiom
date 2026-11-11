package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/capabilities/workspace/patching"
	"idiom/internal/platform/safepath"
)

// CanonicalWorkspacePath is the workspace-owned identity used for mutation
// scope, authorization, commit evidence, and runtime path-set algebra.
// CanonicalID is a slash-separated path relative to the physical workspace
// root. AbsolutePath is retained for rooted filesystem operations only.
type CanonicalWorkspacePath struct {
	CanonicalID  string `json:"canonical_id"`
	AbsolutePath string `json:"-"`
	DisplayPath  string `json:"display_path,omitempty"`
}

type MutationTargetRole string

const (
	MutationTargetSource      MutationTargetRole = "source"
	MutationTargetDestination MutationTargetRole = "destination"
	MutationTargetFile        MutationTargetRole = "file"
	MutationTargetDirectory   MutationTargetRole = "directory"
)

type MutationPreimageClass string

const (
	MutationPreimageRequired  MutationPreimageClass = "required"
	MutationPreimageOptional  MutationPreimageClass = "optional"
	MutationPreimageAbsent    MutationPreimageClass = "absent"
	MutationPreimageDirectory MutationPreimageClass = "directory"
)

type MutationRequestTarget struct {
	RequestedPath string
	Identity      CanonicalWorkspacePath
	Role          MutationTargetRole
	Description   string
	Preimage      MutationPreimageClass
}

type MutationRequestAnalysis struct {
	Tool        string
	Description string
	Mutation    bool
	Targets     []MutationRequestTarget
}

// MutationAdmissionDisposition is the workspace-owned, non-executing answer
// to whether a mutation request may be retained while the caller satisfies a
// prerequisite. It is not an execution authorization and cannot be consumed
// as a mutation receipt.
type MutationAdmissionDisposition string

const (
	MutationAdmissionAdmittedWithoutApproval MutationAdmissionDisposition = "admitted_without_approval"
	MutationAdmissionApprovalRequired        MutationAdmissionDisposition = "approval_required"
	MutationAdmissionDenied                  MutationAdmissionDisposition = "denied"
	MutationAdmissionInvalid                 MutationAdmissionDisposition = "invalid"
	MutationAdmissionNotMutation             MutationAdmissionDisposition = "not_mutation"
)

// MutationAdmissionAssessment contains canonical request facts from the same
// live workspace authority check used by execution authorization. Assessment
// performs no mutation, never invokes an approver, and never creates or
// consumes a one-use authorization receipt. Manager.Execute must still
// re-assess and authorize the request immediately before dispatch.
type MutationAdmissionAssessment struct {
	Disposition MutationAdmissionDisposition
	Analysis    MutationRequestAnalysis
}

// CanonicalizeWorkspacePath returns the physical, workspace-relative identity
// for one requested path. Existing symlink aliases that resolve inside the
// workspace collapse to the same identity as their physical target. Aliases
// that resolve outside the workspace are rejected.
func (m *Manager) CanonicalizeWorkspacePath(path string, workdir string) (CanonicalWorkspacePath, error) {
	if m == nil {
		return CanonicalWorkspacePath{}, fmt.Errorf("workspace manager is not configured")
	}

	return m.pathAccess.canonicalWorkspacePath(path, workdir)
}

func (a *PathAccess) canonicalWorkspacePath(path string, workdir string) (CanonicalWorkspacePath, error) {
	if a == nil || !a.Enabled() {
		return CanonicalWorkspacePath{}, fmt.Errorf("workspace root is not configured")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return CanonicalWorkspacePath{}, fmt.Errorf("path is required")
	}

	base, err := workspaceMutationBasePath(a.cfg.Root, a.cfg.CWD, workdir)
	if err != nil {
		return CanonicalWorkspacePath{}, err
	}
	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}

	physical, ok := resolvePathForWorkspaceContainment(candidate)
	if !ok {
		return CanonicalWorkspacePath{}, fmt.Errorf("path %q cannot be resolved safely", path)
	}
	physicalRoot, err := safepath.CanonicalExistingPath(safepath.CleanPath(filepath.Clean(a.cfg.Root)))
	if err != nil {
		return CanonicalWorkspacePath{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	relative, within := workspaceRelativePath(physicalRoot, physical)
	if !within {
		return CanonicalWorkspacePath{}, fmt.Errorf("path %q escapes workspace root through a symlink", path)
	}

	canonicalID := filepath.ToSlash(filepath.Clean(relative))
	absolute := filepath.Clean(filepath.Join(physicalRoot, relative))
	return CanonicalWorkspacePath{
		CanonicalID:  canonicalID,
		AbsolutePath: absolute,
		DisplayPath:  canonicalID,
	}, nil
}

// AnalyzeMutationRequest derives every semantic target of a mutation request
// and binds it to the canonical workspace identity used by authorization and
// commit accounting. It is the production owner for mutation request shape.
func (m *Manager) AnalyzeMutationRequest(req ToolRequest) (MutationRequestAnalysis, error) {
	analysis, err := analyzeMutationRequestShape(req)
	if err != nil || !analysis.Mutation {
		return analysis, err
	}

	for index := range analysis.Targets {
		identity, resolveErr := m.CanonicalizeWorkspacePath(analysis.Targets[index].RequestedPath, req.Workdir)
		if resolveErr != nil {
			return MutationRequestAnalysis{}, fmt.Errorf("resolve mutation target %q: %w", analysis.Targets[index].RequestedPath, resolveErr)
		}
		if identity.CanonicalID == "." {
			return MutationRequestAnalysis{}, fmt.Errorf("workspace root cannot be a mutation target")
		}
		analysis.Targets[index].Identity = identity
	}
	if strings.TrimSpace(req.Tool) == ToolNameGoMoveDeclarations && len(req.Moves) > 0 {
		slices.SortFunc(analysis.Targets, func(a, b MutationRequestTarget) int {
			return strings.Compare(a.Identity.CanonicalID, b.Identity.CanonicalID)
		})
	}

	return analysis, nil
}

func analyzeMutationRequestShape(req ToolRequest) (MutationRequestAnalysis, error) {
	tool := strings.TrimSpace(req.Tool)
	analysis := MutationRequestAnalysis{
		Tool:        tool,
		Description: strings.TrimSpace(req.Description),
		Mutation:    IsWorkspaceWriteTool(tool),
	}
	if !analysis.Mutation {
		return analysis, nil
	}

	add := func(path string, role MutationTargetRole, preimage MutationPreimageClass, description string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		analysis.Targets = append(analysis.Targets, MutationRequestTarget{
			RequestedPath: path,
			Role:          role,
			Preimage:      preimage,
			Description:   strings.TrimSpace(description),
		})
	}

	switch tool {
	case ToolNameEditFile:
		add(req.Path, MutationTargetFile, MutationPreimageRequired, "edit file")
	case ToolNameWriteFile:
		add(req.Path, MutationTargetFile, MutationPreimageOptional, "write file")
	case ToolNameAppendFile:
		add(req.Path, MutationTargetFile, MutationPreimageOptional, "append file")
	case ToolNameMkdir:
		add(req.Path, MutationTargetDirectory, MutationPreimageDirectory, "create directory")
	case ToolNameApplyPatch:
		ops, err := patching.Parse(patching.NormalizeEnvelope(req.Content))
		if err != nil {
			return analysis, err
		}
		for _, op := range ops {
			switch op.Kind {
			case patching.OpAdd:
				add(op.Path, MutationTargetDestination, MutationPreimageAbsent, "add patch destination")
			case patching.OpDelete:
				add(op.Path, MutationTargetSource, MutationPreimageRequired, "delete patch source")
			case patching.OpUpdate:
				add(op.Path, MutationTargetSource, MutationPreimageRequired, "update patch source")
				if strings.TrimSpace(op.MoveTo) != "" {
					add(op.MoveTo, MutationTargetDestination, MutationPreimageAbsent, "move patch destination")
				}
			}
		}
	case ToolNameGoMoveDeclarations:
		add(req.Path, MutationTargetSource, MutationPreimageRequired, "Go declaration move source")
		if len(req.Moves) == 0 {
			add(req.Destination, MutationTargetDestination, MutationPreimageOptional, "Go declaration move destination")
		} else {
			for _, move := range req.Moves {
				add(move.Destination, MutationTargetDestination, MutationPreimageOptional, "Go declaration move destination")
			}
			start := 0
			if len(analysis.Targets) > 0 && analysis.Targets[0].Role == MutationTargetSource {
				start = 1
			}
			slices.SortFunc(analysis.Targets[start:], func(a, b MutationRequestTarget) int {
				return strings.Compare(a.RequestedPath, b.RequestedPath)
			})
		}
	}

	return analysis, nil
}
