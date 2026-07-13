package agent

import (
	"errors"
	"fmt"
	"reflect"

	"idiom/internal/capabilities/workspace"
	recoverycontract "idiom/internal/contracts/recovery"
	"idiom/internal/platform/textutil"
	"idiom/internal/runtime/workerruntime"
)

var (
	ErrRecoveredMutationStateInvalid  = errors.New("recovered mutation state is invalid")
	ErrRecoveredMutationStateMismatch = errors.New("recovered mutation state does not match live authority")
)

func IsRecoveredMutationStateInvalid(err error) bool {
	return errors.Is(err, ErrRecoveredMutationStateInvalid)
}

func IsRecoveredMutationStateMismatch(err error) bool {
	return errors.Is(err, ErrRecoveredMutationStateMismatch)
}

type runnerMutationState struct {
	canonicalScope           workspace.CanonicalMutationScope
	canonicalScopeProjection workspace.CanonicalMutationScopeProjection
	commitReceipts           []workspace.MutationCommitProjection
	newFileEffectIDs         []string
	restoredRejectedPaths    []string
	restoredReceiptCount     int
	bound                    bool
}

func runnerBriefCarriesMutationScope(brief workerruntime.WorkerRuntimeBrief) bool {
	return len(brief.RequiredEditPaths) > 0 ||
		len(brief.AllowedEditPaths) > 0 ||
		len(brief.AuthorizedNewFiles) > 0
}

func (state *runnerRunState) bindMutationScope(manager *workspace.Manager) error {
	if state == nil || !runnerBriefCarriesMutationScope(state.runtimeBrief) {
		return nil
	}
	if manager == nil {
		return fmt.Errorf("workspace manager is required to bind mutation scope")
	}

	boundContext, canonicalScope, err := manager.BindMutationScope(state.ctx, workspace.MutationScopePaths{
		RequiredPaths:      state.runtimeBrief.RequiredEditPaths,
		AllowedPaths:       state.runtimeBrief.AllowedEditPaths,
		AuthorizedNewFiles: state.runtimeBrief.AuthorizedNewFiles,
	})
	if err != nil {
		return fmt.Errorf("bind canonical mutation scope: %w", err)
	}

	state.ctx = boundContext
	state.mutation = runnerMutationState{
		canonicalScope:           canonicalScope,
		canonicalScopeProjection: canonicalScope.DurableProjection(),
		newFileEffectIDs:         authorizedNewFileIDs(canonicalScope.DurableProjection()),
		bound:                    true,
	}
	return nil
}

func (state *runnerRunState) recordMutationCommitReceipt(result workspace.ToolResult) {
	if state == nil {
		return
	}
	receipt := result.CommitReceipt()
	if receipt == nil {
		return
	}
	state.mutation.commitReceipts = append(state.mutation.commitReceipts, receipt.DurableProjection())
	for _, commit := range receipt.Commits {
		if commit.Preimage.Class != workspace.MutationPreimageAbsent {
			continue
		}
		switch commit.Kind {
		case workspace.MutationCommitReplacedFile, workspace.MutationCommitAppendedFile:
			state.mutation.newFileEffectIDs = textutil.Merge(state.mutation.newFileEffectIDs, []string{commit.Identity.CanonicalID})
		}
	}
}

func (state *runnerRunState) restoreDurableMutationState(manager *workspace.Manager, recovered *recoverycontract.ImplementationState) error {
	if state == nil || recovered == nil {
		return nil
	}
	if err := recoverycontract.ValidateImplementationState(recovered); err != nil {
		return fmt.Errorf("%w: %v", ErrRecoveredMutationStateInvalid, err)
	}

	var reboundScope *recoverycontract.CanonicalMutationScopeProjection
	if state.mutation.bound {
		projection := recoveryCanonicalMutationScopeProjection(state.mutation.canonicalScopeProjection)
		reboundScope = &projection
	}
	if !reflect.DeepEqual(reboundScope, recovered.CanonicalMutationScope) {
		return fmt.Errorf("%w: canonical scope projection differs", ErrRecoveredMutationStateMismatch)
	}

	restoredReceipts := make([]workspace.MutationCommitProjection, 0, len(recovered.CommitReceipts))
	for index, receipt := range recovered.CommitReceipts {
		if !reflect.DeepEqual(reboundScope, receipt.Scope) {
			return fmt.Errorf("%w: commit receipt %d scope differs", ErrRecoveredMutationStateMismatch, index)
		}
		projection := workspaceMutationCommitProjection(receipt)
		if manager == nil {
			return fmt.Errorf("%w: workspace manager is required to validate commit receipt %d", ErrRecoveredMutationStateInvalid, index)
		}
		if err := manager.ValidateMutationCommitProjection(state.ctx, projection); err != nil {
			return fmt.Errorf("%w: commit receipt %d: %v", ErrRecoveredMutationStateInvalid, index, err)
		}
		restoredReceipts = append(restoredReceipts, projection)
	}
	restoredRejectedPaths := uniqueSortedRunnerPaths(recovered.UnresolvedRejectedMutationPaths)
	if state.mutation.bound && len(restoredRejectedPaths) > 0 {
		claims := state.mutation.canonicalScopeProjection.ProjectClaims(restoredRejectedPaths)
		if len(claims.UnsupportedPaths) > 0 {
			return fmt.Errorf("%w: unresolved rejected mutation paths fall outside canonical scope", ErrRecoveredMutationStateInvalid)
		}
		restoredRejectedPaths = uniqueSortedRunnerPaths(claims.CanonicalIDs)
	}
	state.mutation.commitReceipts = restoredReceipts
	state.mutation.newFileEffectIDs = authorizedNewFileIDs(state.mutation.canonicalScopeProjection)
	state.mutation.restoredRejectedPaths = restoredRejectedPaths
	state.mutation.restoredReceiptCount = len(restoredReceipts)
	return nil
}

func authorizedNewFileIDs(scope workspace.CanonicalMutationScopeProjection) []string {
	var ids []string
	for _, rule := range scope.Rules {
		if rule.Kind == workspace.MutationScopeExactFile && rule.Source == workspace.MutationScopeAuthorizedNewFile {
			ids = append(ids, rule.CanonicalID)
		}
	}
	return uniqueSortedRunnerPaths(ids)
}

func (state *runnerRunState) canonicalMutationScopeProjection() *workspace.CanonicalMutationScopeProjection {
	if state == nil || !state.mutation.bound {
		return nil
	}
	projection := state.mutation.canonicalScopeProjection
	return &projection
}

func (state *runnerRunState) committedMutationEffectIDs() []string {
	if state == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, receipt := range state.mutation.commitReceipts {
		for _, effect := range receipt.Effects {
			if effect.CanonicalID == "" {
				continue
			}
			if _, duplicate := seen[effect.CanonicalID]; duplicate {
				continue
			}
			seen[effect.CanonicalID] = struct{}{}
			ids = append(ids, effect.CanonicalID)
		}
	}
	return ids
}

func committedMutationEffectIDsForResult(result workspace.ToolResult) []string {
	receipt := result.CommitReceipt()
	if receipt == nil {
		return nil
	}
	return committedMutationEffectIDsForProjection(receipt.DurableProjection())
}

func committedMutationEffectIDsForProjection(projection workspace.MutationCommitProjection) []string {
	failed := make(map[string]struct{}, len(projection.FailedTargets)+len(projection.UnattemptedTargets))
	for _, target := range projection.FailedTargets {
		if target.CanonicalID != "" {
			failed[target.CanonicalID] = struct{}{}
		}
	}
	for _, target := range projection.UnattemptedTargets {
		if target.CanonicalID != "" {
			failed[target.CanonicalID] = struct{}{}
		}
	}
	var ids []string
	for _, effect := range projection.Effects {
		if effect.CanonicalID == "" {
			continue
		}
		if _, incomplete := failed[effect.CanonicalID]; incomplete {
			continue
		}
		ids = append(ids, effect.CanonicalID)
	}
	return uniqueSortedRunnerPaths(ids)
}

func committedMutationEffectSetForResults(results []workspace.ToolResult) map[string]struct{} {
	committed := make(map[string]struct{})
	for _, result := range results {
		receipt := result.CommitReceipt()
		if receipt == nil {
			continue
		}
		for _, effect := range receipt.DurableProjection().Effects {
			if effect.CanonicalID != "" {
				committed[effect.CanonicalID] = struct{}{}
			}
		}
	}
	return committed
}

func (r *runExecution) completionChangedPaths(results []workspace.ToolResult) []string {
	if r != nil {
		return uniqueSortedRunnerPaths(r.state.committedMutationEffectIDs())
	}
	return mapKeys(newToolResultEvidenceProjection(results).ChangedPathSet())
}

func (state *runnerRunState) committedPostimageFileIDs() []string {
	if state == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, receipt := range state.mutation.commitReceipts {
		for _, id := range receipt.PostimageFileIDs {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}

func (state *runnerRunState) committedAffectedFileIDs() []string {
	if state == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, receipt := range state.mutation.commitReceipts {
		for _, id := range receipt.AffectedFileIDs {
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return uniqueSortedRunnerPaths(ids)
}

func (state *runnerRunState) mutationTargetsAlreadyRemoved(canonicalIDs []string) bool {
	if state == nil || len(canonicalIDs) == 0 {
		return false
	}

	latestKinds := make(map[string]workspace.MutationCommitKind)
	for _, receipt := range state.mutation.commitReceipts {
		for _, effect := range receipt.Effects {
			if effect.CanonicalID != "" {
				latestKinds[effect.CanonicalID] = effect.Kind
			}
		}
	}
	for _, canonicalID := range canonicalIDs {
		if latestKinds[canonicalID] != workspace.MutationCommitRemovedFile {
			return false
		}
	}
	return true
}

func (state *runnerRunState) missingRequiredMutationCoverageIDs() []string {
	if state == nil || !state.mutation.bound {
		return nil
	}
	committed := make(map[string]struct{})
	for _, id := range state.committedMutationEffectIDs() {
		committed[id] = struct{}{}
	}
	var missing []string
	for _, required := range state.mutation.canonicalScopeProjection.RequiredCoverageIDs {
		if _, covered := committed[required]; covered {
			continue
		}
		missing = append(missing, required)
	}
	return missing
}

func (state *runnerRunState) unresolvedMutationTargetIDs() []string {
	if state == nil {
		return nil
	}
	receiptPending := make(map[string]struct{})
	for _, receipt := range state.mutation.commitReceipts {
		applyMutationReceiptToPendingTargets(receiptPending, receipt)
	}

	restoredPending := make(map[string]struct{}, len(state.mutation.restoredRejectedPaths))
	for _, path := range state.mutation.restoredRejectedPaths {
		restoredPending[path] = struct{}{}
	}
	for _, receipt := range state.mutation.commitReceipts[state.mutation.restoredReceiptCount:] {
		applyMutationReceiptToPendingTargets(restoredPending, receipt)
	}
	for path := range restoredPending {
		receiptPending[path] = struct{}{}
	}
	return uniqueSortedRunnerPaths(mapKeys(receiptPending))
}

func applyMutationReceiptToPendingTargets(pending map[string]struct{}, receipt workspace.MutationCommitProjection) {
	for _, effect := range receipt.Effects {
		delete(pending, effect.CanonicalID)
	}
	for _, target := range receipt.FailedTargets {
		if target.CanonicalID != "" {
			pending[target.CanonicalID] = struct{}{}
		}
	}
	for _, target := range receipt.UnattemptedTargets {
		if target.CanonicalID != "" {
			pending[target.CanonicalID] = struct{}{}
		}
	}
}

func (state *runnerRunState) projectMutationClaims(paths []string) workspace.MutationScopeClaimProjection {
	if state == nil || !state.mutation.bound {
		return workspace.MutationScopeClaimProjection{UnsupportedPaths: append([]string(nil), paths...)}
	}
	return state.mutation.canonicalScopeProjection.ProjectClaims(paths)
}

func (state *runnerRunState) durableMutationState() (*recoverycontract.CanonicalMutationScopeProjection, []recoverycontract.MutationCommitProjection) {
	if state == nil {
		return nil, nil
	}
	var scope *recoverycontract.CanonicalMutationScopeProjection
	if state.mutation.bound {
		converted := recoveryCanonicalMutationScopeProjection(state.mutation.canonicalScopeProjection)
		scope = &converted
	}
	receipts := make([]recoverycontract.MutationCommitProjection, 0, len(state.mutation.commitReceipts))
	for _, receipt := range state.mutation.commitReceipts {
		receipts = append(receipts, recoveryMutationCommitProjection(receipt))
	}
	return scope, receipts
}

func recoveryCanonicalMutationScopeProjection(scope workspace.CanonicalMutationScopeProjection) recoverycontract.CanonicalMutationScopeProjection {
	projection := recoverycontract.CanonicalMutationScopeProjection{
		RequiredCoverageIDs: append([]string(nil), scope.RequiredCoverageIDs...),
		Rules:               make([]recoverycontract.CanonicalMutationScopeRuleProjection, 0, len(scope.Rules)),
		Presentations:       make([]recoverycontract.CanonicalMutationScopePresentationProjection, 0, len(scope.Presentations)),
	}
	for _, rule := range scope.Rules {
		projection.Rules = append(projection.Rules, recoverycontract.CanonicalMutationScopeRuleProjection{
			Kind:        string(rule.Kind),
			Source:      string(rule.Source),
			CanonicalID: rule.CanonicalID,
		})
	}
	for _, presentation := range scope.Presentations {
		projection.Presentations = append(projection.Presentations, recoverycontract.CanonicalMutationScopePresentationProjection{
			PresentedPath: presentation.PresentedPath,
			CanonicalID:   presentation.CanonicalID,
			Source:        string(presentation.Source),
		})
	}
	return projection
}

func recoveryMutationCommitProjection(receipt workspace.MutationCommitProjection) recoverycontract.MutationCommitProjection {
	projection := recoverycontract.MutationCommitProjection{
		PostimageFileIDs: append([]string(nil), receipt.PostimageFileIDs...),
		AffectedFileIDs:  append([]string(nil), receipt.AffectedFileIDs...),
		Effects:          make([]recoverycontract.MutationCommitEffectProjection, 0, len(receipt.Effects)),
		FailedTargets:    make([]recoverycontract.MutationTargetProjection, 0, len(receipt.FailedTargets)),
		UnattemptedTargets: make(
			[]recoverycontract.MutationTargetProjection,
			0,
			len(receipt.UnattemptedTargets),
		),
	}
	if receipt.Scope != nil {
		scope := recoveryCanonicalMutationScopeProjection(*receipt.Scope)
		projection.Scope = &scope
	}
	for _, effect := range receipt.Effects {
		projection.Effects = append(projection.Effects, recoverycontract.MutationCommitEffectProjection{
			CanonicalID: effect.CanonicalID,
			Kind:        string(effect.Kind),
			Role:        string(effect.Role),
		})
	}
	for _, target := range receipt.FailedTargets {
		projection.FailedTargets = append(projection.FailedTargets, recoverycontract.MutationTargetProjection{
			CanonicalID:   target.CanonicalID,
			Role:          string(target.Role),
			PreimageClass: string(target.PreimageClass),
		})
	}
	for _, target := range receipt.UnattemptedTargets {
		projection.UnattemptedTargets = append(projection.UnattemptedTargets, recoverycontract.MutationTargetProjection{
			CanonicalID:   target.CanonicalID,
			Role:          string(target.Role),
			PreimageClass: string(target.PreimageClass),
		})
	}
	return projection
}

func workspaceMutationCommitProjection(receipt recoverycontract.MutationCommitProjection) workspace.MutationCommitProjection {
	projection := workspace.MutationCommitProjection{
		PostimageFileIDs: append([]string(nil), receipt.PostimageFileIDs...),
		AffectedFileIDs:  append([]string(nil), receipt.AffectedFileIDs...),
		Effects:          make([]workspace.MutationCommitEffectProjection, 0, len(receipt.Effects)),
		FailedTargets:    make([]workspace.MutationTargetProjection, 0, len(receipt.FailedTargets)),
		UnattemptedTargets: make(
			[]workspace.MutationTargetProjection,
			0,
			len(receipt.UnattemptedTargets),
		),
	}
	if receipt.Scope != nil {
		scope := workspaceCanonicalMutationScopeProjection(*receipt.Scope)
		projection.Scope = &scope
	}
	for _, effect := range receipt.Effects {
		projection.Effects = append(projection.Effects, workspace.MutationCommitEffectProjection{
			CanonicalID: effect.CanonicalID,
			Kind:        workspace.MutationCommitKind(effect.Kind),
			Role:        workspace.MutationTargetRole(effect.Role),
		})
	}
	for _, target := range receipt.FailedTargets {
		projection.FailedTargets = append(projection.FailedTargets, workspace.MutationTargetProjection{
			CanonicalID:   target.CanonicalID,
			Role:          workspace.MutationTargetRole(target.Role),
			PreimageClass: workspace.MutationPreimageClass(target.PreimageClass),
		})
	}
	for _, target := range receipt.UnattemptedTargets {
		projection.UnattemptedTargets = append(projection.UnattemptedTargets, workspace.MutationTargetProjection{
			CanonicalID:   target.CanonicalID,
			Role:          workspace.MutationTargetRole(target.Role),
			PreimageClass: workspace.MutationPreimageClass(target.PreimageClass),
		})
	}
	return projection
}

func workspaceCanonicalMutationScopeProjection(scope recoverycontract.CanonicalMutationScopeProjection) workspace.CanonicalMutationScopeProjection {
	projection := workspace.CanonicalMutationScopeProjection{
		RequiredCoverageIDs: append([]string(nil), scope.RequiredCoverageIDs...),
		Rules:               make([]workspace.CanonicalMutationScopeRuleProjection, 0, len(scope.Rules)),
		Presentations:       make([]workspace.CanonicalMutationScopePresentationProjection, 0, len(scope.Presentations)),
	}
	for _, rule := range scope.Rules {
		projection.Rules = append(projection.Rules, workspace.CanonicalMutationScopeRuleProjection{
			Kind:        workspace.MutationScopeRuleKind(rule.Kind),
			Source:      workspace.MutationScopeRuleSource(rule.Source),
			CanonicalID: rule.CanonicalID,
		})
	}
	for _, presentation := range scope.Presentations {
		projection.Presentations = append(projection.Presentations, workspace.CanonicalMutationScopePresentationProjection{
			PresentedPath: presentation.PresentedPath,
			CanonicalID:   presentation.CanonicalID,
			Source:        workspace.MutationScopeRuleSource(presentation.Source),
		})
	}
	return projection
}
