package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/failures"
)

var errWorkspaceMutationUnauthorized = errors.New("workspace mutation authorization rejected")

//go:generate go tool mockgen -source=mutation_authorization.go -destination=mock_mutation_approver_test.go -package=workspace -typed

// MutationApprover is the host-owned approval boundary for requests whose
// effective workspace policy is Ask. ToolRequest fields cannot install or
// impersonate an approver.
type MutationApprover interface {
	ApproveWorkspaceMutation(context.Context, MutationApprovalRequest) (MutationApprovalDecision, error)
}

type MutationApprovalRequest struct {
	SessionID   string
	Tool        string
	Description string
	Targets     []MutationRequestTarget
}

type MutationApprovalDecision struct {
	Approved bool
}

type evaluatedMutationAdmission struct {
	assessment   MutationAdmissionAssessment
	failure      *ToolResult
	rootPath     string
	rootIdentity os.FileInfo
	scopeBound   bool
	scope        CanonicalMutationScope
}

type mutationApproverContextKey struct{}

type boundMutationApprover struct {
	approver MutationApprover
}

// WithMutationApprover binds operator routing to one invocation. Binding nil
// explicitly disables fallback to Manager state.
func WithMutationApprover(ctx context.Context, approver MutationApprover) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	return context.WithValue(ctx, mutationApproverContextKey{}, boundMutationApprover{approver: approver})
}

func mutationApproverFromContext(ctx context.Context) (MutationApprover, bool) {
	if ctx == nil {
		return nil, false
	}

	bound, ok := ctx.Value(mutationApproverContextKey{}).(boundMutationApprover)
	if !ok {
		return nil, false
	}

	return bound.approver, true
}

func (m *Manager) snapshotMutationApprover(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bound := mutationApproverFromContext(ctx); bound {
		return ctx
	}

	return WithMutationApprover(ctx, nil)
}

type mutationAuthorizationContextKey struct{}

type mutationAuthorizationReceipt struct {
	mu             sync.Mutex
	consumed       bool
	requestHash    [sha256.Size]byte
	rootPath       string
	rootIdentity   os.FileInfo
	invocation     executionSessionIdentity
	targets        map[string]MutationRequestTarget
	orderedTargets []MutationRequestTarget
	tool           string
	scopeBound     bool
	scope          CanonicalMutationScope
}

func withMutationAuthorization(ctx context.Context, receipt *mutationAuthorizationReceipt) context.Context {
	return context.WithValue(ctx, mutationAuthorizationContextKey{}, receipt)
}

func mutationAuthorizationFromContext(ctx context.Context) (*mutationAuthorizationReceipt, bool) {
	if ctx == nil {
		return nil, false
	}
	receipt, ok := ctx.Value(mutationAuthorizationContextKey{}).(*mutationAuthorizationReceipt)
	return receipt, ok && receipt != nil
}

// AssessMutationAdmission evaluates live request shape, canonical target,
// bound-scope, root-identity, write-mode, and permission authority without
// executing the request or consulting an approver. Only
// MutationAdmissionAdmittedWithoutApproval is safe for retaining prerequisite
// intent; every later execution must still pass authorizeMutationRequest.
func (m *Manager) AssessMutationAdmission(ctx context.Context, req ToolRequest) MutationAdmissionAssessment {
	return m.assessMutationAdmission(ctx, req).assessment
}

func (m *Manager) assessMutationAdmission(ctx context.Context, req ToolRequest) evaluatedMutationAdmission {
	evaluated := evaluatedMutationAdmission{
		assessment: MutationAdmissionAssessment{Disposition: MutationAdmissionInvalid},
	}
	if m == nil {
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", fmt.Errorf("workspace manager is not configured"), failures.FailedPrecondition, reasonPlanSubmissionRejected)
		evaluated.failure = &result
		return evaluated
	}
	if ctx == nil {
		ctx = context.Background()
	}

	analysis, err := m.AnalyzeMutationRequest(req)
	evaluated.assessment.Analysis = analysis
	if err != nil {
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", err, failures.InvalidArgument, reasonToolInvalidArgument)
		evaluated.failure = &result
		return evaluated
	}
	if !analysis.Mutation {
		evaluated.assessment.Disposition = MutationAdmissionNotMutation
		return evaluated
	}
	if len(analysis.Targets) == 0 {
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", fmt.Errorf("mutation request does not identify a target"), failures.InvalidArgument, reasonToolInvalidArgument)
		evaluated.failure = &result
		return evaluated
	}

	rootPath := filepath.Clean(strings.TrimSpace(m.cfg.Root))
	rootIdentity, err := os.Lstat(rootPath)
	if err != nil || rootIdentity.Mode()&os.ModeSymlink != 0 || !rootIdentity.IsDir() {
		if err == nil {
			err = fmt.Errorf("workspace mutation root must be a directory and must not be a symlink")
		}
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", err, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		evaluated.failure = &result
		return evaluated
	}
	evaluated.rootPath = rootPath
	evaluated.rootIdentity = rootIdentity

	analysis, err = m.classifyMutationAdmissionPreimages(req, analysis, rootPath, rootIdentity)
	evaluated.assessment.Analysis = analysis
	if err != nil {
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", err, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		evaluated.failure = &result
		return evaluated
	}

	scopeBinding, scopeBound := mutationScopeFromContext(ctx)
	evaluated.scopeBound = scopeBound
	if scopeBound {
		if scopeBinding.rootPath != rootPath || !os.SameFile(scopeBinding.rootIdentity, rootIdentity) {
			result := toolPermissionPolicyResult(req.Tool, "Mutation scope rejected", "workspace root identity changed after mutation scope binding")
			evaluated.assessment.Disposition = MutationAdmissionDenied
			evaluated.failure = &result
			return evaluated
		}
		for _, target := range analysis.Targets {
			if !mutationAdmissionScopeAuthorizes(scopeBinding.canonical, req.Tool, target) {
				result := toolPermissionPolicyResult(req.Tool, "Mutation target outside bound scope", fmt.Sprintf("canonical target %s is outside the invocation's typed RequiredPaths, AllowedPaths, and AuthorizedNewFiles rules", target.Identity.CanonicalID))
				evaluated.assessment.Disposition = MutationAdmissionDenied
				evaluated.failure = &result
				return evaluated
			}
		}
		evaluated.scope = cloneCanonicalMutationScope(scopeBinding.canonical)
	}

	policyDecision := m.EvaluateToolRule(req)
	if policyDecision == toolpolicy.Deny {
		result := toolPermissionPolicyResult(req.Tool, "Tool request denied by workspace policy", "workspace policy denies this mutation request")
		evaluated.assessment.Disposition = MutationAdmissionDenied
		evaluated.failure = &result
		return evaluated
	}
	if !m.cfg.EnableWrites {
		result := toolWriteDisabledResult(req.Tool, "Mutation request rejected", "workspace writes are disabled")
		evaluated.assessment.Disposition = MutationAdmissionDenied
		evaluated.failure = &result
		return evaluated
	}
	if reason, blocked := exploreModeWriteBlocked(req.Tool, m.Mode()); blocked {
		result := toolPermissionPolicyResult(req.Tool, "Tool request rejected", reason)
		evaluated.assessment.Disposition = MutationAdmissionDenied
		evaluated.failure = &result
		return evaluated
	}
	if policyDecision == toolpolicy.Ask {
		evaluated.assessment.Disposition = MutationAdmissionApprovalRequired
		return evaluated
	}

	evaluated.assessment.Disposition = MutationAdmissionAdmittedWithoutApproval
	return evaluated
}

// classifyMutationAdmissionPreimages refines write_file's static Optional
// capability into the live rooted fact consumed by admission and authorization.
func (m *Manager) classifyMutationAdmissionPreimages(req ToolRequest, analysis MutationRequestAnalysis, rootPath string, rootIdentity os.FileInfo) (MutationRequestAnalysis, error) {
	if analysis.Tool != ToolNameWriteFile {
		return analysis, nil
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return analysis, fmt.Errorf("open workspace root for mutation admission: %w", err)
	}
	defer root.Close()

	openedRoot, err := root.Stat(".")
	if err != nil {
		return analysis, fmt.Errorf("inspect opened workspace root for mutation admission: %w", err)
	}
	if !os.SameFile(rootIdentity, openedRoot) {
		return analysis, fmt.Errorf("workspace mutation root changed while assessing target preimage")
	}

	for index := range analysis.Targets {
		target := &analysis.Targets[index]
		if target.Preimage != MutationPreimageOptional {
			continue
		}

		before, exists, observeErr := observeRootedMutationAdmissionTarget(root, target.Identity.CanonicalID)
		if observeErr != nil {
			return analysis, observeErr
		}
		currentIdentity, resolveErr := m.CanonicalizeWorkspacePath(target.RequestedPath, req.Workdir)
		if resolveErr != nil {
			return analysis, fmt.Errorf("re-resolve mutation target %q while assessing preimage: %w", target.RequestedPath, resolveErr)
		}
		if currentIdentity.CanonicalID != target.Identity.CanonicalID {
			return analysis, fmt.Errorf("mutation target %q changed canonical identity while assessing preimage", target.RequestedPath)
		}
		after, stillExists, observeErr := observeRootedMutationAdmissionTarget(root, target.Identity.CanonicalID)
		if observeErr != nil {
			return analysis, observeErr
		}
		if exists != stillExists || (exists && !os.SameFile(before, after)) {
			return analysis, fmt.Errorf("mutation target %q changed while assessing preimage", target.RequestedPath)
		}
		if !exists {
			target.Preimage = MutationPreimageAbsent
		} else {
			target.Preimage = MutationPreimageRequired
		}
	}

	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(rootIdentity, currentRoot) {
		if err != nil {
			return analysis, fmt.Errorf("reinspect workspace root after mutation admission: %w", err)
		}
		return analysis, fmt.Errorf("workspace mutation root changed while assessing target preimage")
	}
	openedRoot, err = root.Stat(".")
	if err != nil || !os.SameFile(rootIdentity, openedRoot) {
		if err != nil {
			return analysis, fmt.Errorf("reinspect opened workspace root after mutation admission: %w", err)
		}
		return analysis, fmt.Errorf("opened workspace mutation root changed while assessing target preimage")
	}

	return analysis, nil
}

func observeRootedMutationAdmissionTarget(root *os.Root, canonicalID string) (os.FileInfo, bool, error) {
	info, err := root.Lstat(filepath.FromSlash(canonicalID))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect mutation admission target %q: %w", canonicalID, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("mutation admission target %q changed to a symlink", canonicalID)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("mutation admission target %q must be a regular file", canonicalID)
	}
	return info, true, nil
}

func mutationAdmissionScopeAuthorizes(scope CanonicalMutationScope, tool string, target MutationRequestTarget) bool {
	// AuthorizedNewFiles must consume the same rooted absence fact returned by
	// admission instead of making a second path-based existence observation.
	if strings.TrimSpace(tool) == ToolNameWriteFile && scope.requiresAbsent(target.Identity.CanonicalID) {
		return target.Role != MutationTargetDirectory && target.Preimage == MutationPreimageAbsent
	}
	return scope.authorizes(tool, target)
}

func (m *Manager) authorizeMutationRequest(ctx context.Context, req ToolRequest) (context.Context, *ToolResult) {
	evaluated := m.assessMutationAdmission(ctx, req)
	switch evaluated.assessment.Disposition {
	case MutationAdmissionNotMutation:
		return ctx, nil
	case MutationAdmissionInvalid, MutationAdmissionDenied:
		if evaluated.failure != nil {
			return ctx, evaluated.failure
		}
		result := toolPermissionPolicyResult(req.Tool, "Mutation request rejected", "workspace mutation admission failed closed")
		return ctx, &result
	case MutationAdmissionApprovalRequired:
		analysis := evaluated.assessment.Analysis
		approver, bound := mutationApproverFromContext(ctx)
		if !bound || approver == nil {
			result := toolPermissionPolicyResult(req.Tool, "Mutation approval required", "workspace policy requires an invocation-bound mutation approver")
			return ctx, &result
		}
		identity, _ := executionSessionIdentityFromContext(ctx)
		decision, approveErr := approver.ApproveWorkspaceMutation(ctx, MutationApprovalRequest{
			SessionID:   identity.sessionID,
			Tool:        analysis.Tool,
			Description: analysis.Description,
			Targets:     cloneMutationRequestTargets(analysis.Targets),
		})
		if approveErr != nil {
			result := toolFailureFromErrorResult(req.Tool, "Mutation approval failed", approveErr, failures.PermissionDenied, reasonToolPermissionPolicy)
			return ctx, &result
		}
		if !decision.Approved {
			result := toolPermissionPolicyResult(req.Tool, "Mutation approval denied", "the operator did not approve this mutation request")
			return ctx, &result
		}
	case MutationAdmissionAdmittedWithoutApproval:
		// Continue to the second live root check and one-use receipt.
	default:
		result := toolPermissionPolicyResult(req.Tool, "Mutation request rejected", "workspace mutation admission returned an unknown disposition")
		return ctx, &result
	}

	analysis := evaluated.assessment.Analysis
	rootPath := evaluated.rootPath
	rootIdentity := evaluated.rootIdentity
	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(rootIdentity, currentRoot) {
		if err == nil {
			err = fmt.Errorf("workspace mutation root changed during authorization")
		}
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", err, failures.FailedPrecondition, reasonPlanSubmissionRejected)
		return ctx, &result
	}

	requestHash, err := mutationRequestHash(req)
	if err != nil {
		result := toolFailureFromErrorResult(req.Tool, "Mutation request rejected", err, failures.Internal, reasonToolInternalFailure)
		return ctx, &result
	}
	identity, _ := executionSessionIdentityFromContext(ctx)
	targets := make(map[string]MutationRequestTarget, len(analysis.Targets))
	for _, target := range analysis.Targets {
		targets[target.Identity.CanonicalID] = target
	}
	receipt := &mutationAuthorizationReceipt{
		requestHash:    requestHash,
		rootPath:       rootPath,
		rootIdentity:   rootIdentity,
		invocation:     identity,
		targets:        targets,
		orderedTargets: cloneMutationRequestTargets(analysis.Targets),
		tool:           analysis.Tool,
		scopeBound:     evaluated.scopeBound,
	}
	if evaluated.scopeBound {
		receipt.scope = cloneCanonicalMutationScope(evaluated.scope)
	}

	return withMutationAuthorization(ctx, receipt), nil
}

func (r *mutationAuthorizationReceipt) consume(ctx context.Context, req ToolRequest, rootPath string) error {
	if r == nil {
		return fmt.Errorf("%w: authorization receipt is absent", errWorkspaceMutationUnauthorized)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.consumed {
		return fmt.Errorf("%w: authorization receipt was already consumed", errWorkspaceMutationUnauthorized)
	}
	// A receipt is one-use even when a later binding check fails.
	r.consumed = true

	hash, err := mutationRequestHash(req)
	if err != nil {
		return fmt.Errorf("%w: hash mutation request: %v", errWorkspaceMutationUnauthorized, err)
	}
	if hash != r.requestHash {
		return fmt.Errorf("%w: request does not match authorization receipt", errWorkspaceMutationUnauthorized)
	}
	identity, bound := executionSessionIdentityFromContext(ctx)
	if !bound || identity != r.invocation {
		return fmt.Errorf("%w: invocation identity does not match authorization receipt", errWorkspaceMutationUnauthorized)
	}
	currentScope, currentScopeBound := mutationScopeFromContext(ctx)
	if currentScopeBound != r.scopeBound {
		return fmt.Errorf("%w: mutation scope binding presence changed after authorization", errWorkspaceMutationUnauthorized)
	}
	if r.scopeBound && (!slices.Equal(currentScope.canonical.RequiredCoverage, r.scope.RequiredCoverage) ||
		!slices.Equal(currentScope.canonical.Rules, r.scope.Rules) ||
		!slices.Equal(currentScope.canonical.Presentations, r.scope.Presentations)) {
		return fmt.Errorf("%w: mutation scope does not match authorization receipt", errWorkspaceMutationUnauthorized)
	}
	if filepath.Clean(rootPath) != r.rootPath {
		return fmt.Errorf("%w: workspace root does not match authorization receipt", errWorkspaceMutationUnauthorized)
	}
	rootIdentity, err := os.Lstat(r.rootPath)
	if err != nil || rootIdentity.Mode()&os.ModeSymlink != 0 || !rootIdentity.IsDir() || !os.SameFile(rootIdentity, r.rootIdentity) {
		return fmt.Errorf("%w: workspace root identity changed after authorization", errWorkspaceMutationUnauthorized)
	}

	return nil
}

func mutationRequestHash(req ToolRequest) ([sha256.Size]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(body), nil
}

func cloneMutationRequestTargets(targets []MutationRequestTarget) []MutationRequestTarget {
	return append([]MutationRequestTarget(nil), targets...)
}
