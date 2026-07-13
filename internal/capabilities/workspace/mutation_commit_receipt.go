package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

type MutationCommitKind string

const (
	MutationCommitCreatedDirectory MutationCommitKind = "created_directory"
	MutationCommitReplacedFile     MutationCommitKind = "replaced_file"
	MutationCommitAppendedFile     MutationCommitKind = "appended_file"
	MutationCommitRemovedFile      MutationCommitKind = "removed_file"
)

type MutationPreimageReceipt struct {
	Class  MutationPreimageClass
	Exists bool
	SHA256 string
}

type MutationCommit struct {
	Identity CanonicalWorkspacePath
	Role     MutationTargetRole
	Kind     MutationCommitKind
	Preimage MutationPreimageReceipt
}

// MutationCommitReceipt is workspace-authored evidence of effects that crossed
// the rooted commit boundary. Planned paths never appear as commits.
type MutationCommitReceipt struct {
	Tool               string
	Root               string
	Scope              *CanonicalMutationScope
	Targets            []MutationRequestTarget
	Commits            []MutationCommit
	FailedTargets      []MutationRequestTarget
	UnattemptedTargets []MutationRequestTarget

	rollback    *mutationRollbackState
	rollbackErr error
	fileChanges []MutationFileChange
}

// MutationFileChange is invocation-scoped evidence for a file effect that
// crossed the rooted commit boundary. Bodies remain private and are available
// only through defensive-copy accessors; JSON exposes metadata only.
type MutationFileChange struct {
	canonicalID  string
	kind         MutationCommitKind
	before       []byte
	beforeExists bool
	after        []byte
	afterExists  bool
}

func (c MutationFileChange) CanonicalID() string {
	return c.canonicalID
}

func (c MutationFileChange) Kind() MutationCommitKind {
	return c.kind
}

func (c MutationFileChange) BeforeBody() ([]byte, bool) {
	return append([]byte(nil), c.before...), c.beforeExists
}

func (c MutationFileChange) AfterBody() ([]byte, bool) {
	return append([]byte(nil), c.after...), c.afterExists
}

func (c MutationFileChange) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		CanonicalID string             `json:"canonical_id"`
		Kind        MutationCommitKind `json:"kind"`
	}{CanonicalID: c.canonicalID, Kind: c.kind})
}

// MutationCommitEffectProjection is one durable effect that crossed the
// workspace commit boundary. It intentionally retains only the relative
// canonical identity and semantic effect classification.
type MutationCommitEffectProjection struct {
	CanonicalID string             `json:"canonical_id"`
	Kind        MutationCommitKind `json:"kind"`
	Role        MutationTargetRole `json:"role"`
}

// MutationTargetProjection is the non-sensitive target shape retained for a
// failed or unattempted request target.
type MutationTargetProjection struct {
	CanonicalID   string                `json:"canonical_id"`
	Role          MutationTargetRole    `json:"role"`
	PreimageClass MutationPreimageClass `json:"preimage_class"`
}

// MutationCommitProjection is the durable, non-sensitive receipt shape for
// runtime resume and audit. It contains relative canonical workspace IDs and
// typed classifications only; roots, absolute paths, and preimage bodies or
// digests never cross this boundary.
type MutationCommitProjection struct {
	Scope              *CanonicalMutationScopeProjection `json:"scope,omitempty"`
	Effects            []MutationCommitEffectProjection  `json:"effects,omitempty"`
	PostimageFileIDs   []string                          `json:"postimage_file_ids,omitempty"`
	AffectedFileIDs    []string                          `json:"affected_file_ids,omitempty"`
	FailedTargets      []MutationTargetProjection        `json:"failed_targets,omitempty"`
	UnattemptedTargets []MutationTargetProjection        `json:"unattempted_targets,omitempty"`
}

func (r *MutationCommitReceipt) ChangedPaths() []string {
	if r == nil || len(r.Commits) == 0 {
		return nil
	}

	committed := make(map[string]struct{}, len(r.Commits))
	for _, commit := range r.Commits {
		committed[commit.Identity.CanonicalID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(committed))
	paths := make([]string, 0, len(committed))
	for _, target := range r.Targets {
		id := target.Identity.CanonicalID
		if _, ok := committed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		paths = append(paths, id)
	}
	// Implicit parent directory commits are real effects even though they are
	// not independent request targets.
	for _, commit := range r.Commits {
		id := commit.Identity.CanonicalID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		paths = append(paths, id)
	}

	return paths
}

// VerifiablePaths returns committed file identities whose post-write state can
// participate in verification. Directory effects remain in Commits and
// ChangedPaths for audit, but never masquerade as file-verification work.
func (r *MutationCommitReceipt) VerifiablePaths() []string {
	if r == nil {
		return nil
	}
	paths := make([]string, 0, len(r.Commits))
	seen := make(map[string]struct{}, len(r.Commits))
	for _, commit := range r.Commits {
		switch commit.Kind {
		case MutationCommitReplacedFile, MutationCommitAppendedFile:
		default:
			continue
		}
		id := commit.Identity.CanonicalID
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		paths = append(paths, id)
	}
	return paths
}

// FileChanges returns private, exact pre/post file evidence produced by the
// rooted transaction. It is absent when a commit's exact file body could not
// be established and never reconstructs bodies from a later host read.
func (r *MutationCommitReceipt) FileChanges() []MutationFileChange {
	if r == nil {
		return nil
	}
	return cloneMutationFileChanges(r.fileChanges)
}

func (r *MutationCommitReceipt) DurableProjection() MutationCommitProjection {
	projection := MutationCommitProjection{}
	if r == nil {
		return projection
	}
	if r.Scope != nil {
		scope := r.Scope.DurableProjection()
		projection.Scope = &scope
	}
	for _, commit := range r.Commits {
		projection.Effects = append(projection.Effects, MutationCommitEffectProjection{
			CanonicalID: commit.Identity.CanonicalID,
			Kind:        commit.Kind,
			Role:        commit.Role,
		})
		switch commit.Kind {
		case MutationCommitReplacedFile, MutationCommitAppendedFile:
			projection.PostimageFileIDs = appendUniqueCanonicalID(projection.PostimageFileIDs, commit.Identity.CanonicalID)
			projection.AffectedFileIDs = appendUniqueCanonicalID(projection.AffectedFileIDs, commit.Identity.CanonicalID)
		case MutationCommitRemovedFile:
			projection.AffectedFileIDs = appendUniqueCanonicalID(projection.AffectedFileIDs, commit.Identity.CanonicalID)
		}
	}
	for _, target := range r.FailedTargets {
		projection.FailedTargets = appendUniqueMutationTargetProjection(projection.FailedTargets, target)
	}
	for _, target := range r.UnattemptedTargets {
		projection.UnattemptedTargets = appendUniqueMutationTargetProjection(projection.UnattemptedTargets, target)
	}
	return projection
}

// MarshalJSON prevents the invocation-bound receipt from accidentally
// crossing a durable boundary with workspace roots, absolute paths, or
// preimage digests. Callers that serialize a receipt get its durable shape.
func (r MutationCommitReceipt) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.DurableProjection())
}

// ValidateMutationCommitProjection validates one loaded durable receipt at the
// workspace authority boundary. A bound mutation scope is taken only from ctx;
// callers cannot reconstruct or substitute live authorization through the DTO.
func (m *Manager) ValidateMutationCommitProjection(ctx context.Context, projection MutationCommitProjection) error {
	if err := validateMutationCommitProjectionShape(projection); err != nil {
		return err
	}

	binding, bound := mutationScopeFromContext(ctx)
	if !bound {
		return nil
	}
	if m == nil || !m.Enabled() {
		return fmt.Errorf("workspace manager is required to validate a bound mutation receipt")
	}

	rootPath := filepath.Clean(strings.TrimSpace(m.cfg.Root))
	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || binding.rootIdentity == nil || !os.SameFile(binding.rootIdentity, currentRoot) {
		if err == nil {
			err = fmt.Errorf("workspace mutation root identity changed after scope binding")
		}
		return err
	}
	if binding.rootPath != rootPath {
		return fmt.Errorf("workspace mutation root does not match the bound scope")
	}

	expectedScope := binding.canonical.DurableProjection()
	if projection.Scope == nil || !reflect.DeepEqual(*projection.Scope, expectedScope) {
		return fmt.Errorf("durable mutation receipt scope does not match the bound workspace scope")
	}
	for index, effect := range projection.Effects {
		if !binding.canonical.authorizesDurableTarget(effect.CanonicalID, effect.Role) {
			return fmt.Errorf("durable mutation receipt effects[%d] target %q is outside the bound workspace scope", index, effect.CanonicalID)
		}
	}
	for index, target := range projection.FailedTargets {
		if !binding.canonical.authorizesDurableTarget(target.CanonicalID, target.Role) {
			return fmt.Errorf("durable mutation receipt failed_targets[%d] target %q is outside the bound workspace scope", index, target.CanonicalID)
		}
	}
	for index, target := range projection.UnattemptedTargets {
		if !binding.canonical.authorizesDurableTarget(target.CanonicalID, target.Role) {
			return fmt.Errorf("durable mutation receipt unattempted_targets[%d] target %q is outside the bound workspace scope", index, target.CanonicalID)
		}
	}
	return nil
}

func validateMutationCommitProjectionShape(projection MutationCommitProjection) error {
	if err := validateCanonicalMutationScopeProjectionShape(projection.Scope); err != nil {
		return fmt.Errorf("durable mutation receipt scope: %w", err)
	}
	if err := validateUniqueDurableMutationPaths("postimage_file_ids", projection.PostimageFileIDs); err != nil {
		return err
	}
	if err := validateUniqueDurableMutationPaths("affected_file_ids", projection.AffectedFileIDs); err != nil {
		return err
	}

	expectedPostimages := make([]string, 0, len(projection.Effects))
	expectedAffected := make([]string, 0, len(projection.Effects))
	seenEffects := make(map[string]struct{}, len(projection.Effects))
	for index, effect := range projection.Effects {
		field := fmt.Sprintf("effects[%d]", index)
		if err := validateDurableMutationPath(field+".canonical_id", effect.CanonicalID); err != nil {
			return err
		}
		if _, duplicate := seenEffects[effect.CanonicalID]; duplicate {
			return fmt.Errorf("%s duplicates an earlier canonical identity", field)
		}
		seenEffects[effect.CanonicalID] = struct{}{}
		if !validMutationCommitKind(effect.Kind) {
			return fmt.Errorf("%s.kind is invalid: %q", field, effect.Kind)
		}
		if !validMutationTargetRole(effect.Role) {
			return fmt.Errorf("%s.role is invalid: %q", field, effect.Role)
		}
		if effect.Kind == MutationCommitCreatedDirectory && effect.Role != MutationTargetDirectory {
			return fmt.Errorf("%s.role must be %q for %q", field, MutationTargetDirectory, effect.Kind)
		}
		if effect.Kind != MutationCommitCreatedDirectory && effect.Role == MutationTargetDirectory {
			return fmt.Errorf("%s.role %q is invalid for %q", field, effect.Role, effect.Kind)
		}
		switch effect.Kind {
		case MutationCommitReplacedFile, MutationCommitAppendedFile:
			expectedPostimages = appendUniqueCanonicalID(expectedPostimages, effect.CanonicalID)
			expectedAffected = appendUniqueCanonicalID(expectedAffected, effect.CanonicalID)
		case MutationCommitRemovedFile:
			expectedAffected = appendUniqueCanonicalID(expectedAffected, effect.CanonicalID)
		}
	}
	if !slices.Equal(projection.PostimageFileIDs, expectedPostimages) {
		return fmt.Errorf("postimage_file_ids do not match durable mutation effects")
	}
	if !slices.Equal(projection.AffectedFileIDs, expectedAffected) {
		return fmt.Errorf("affected_file_ids do not match durable mutation effects")
	}
	if err := validateMutationTargetProjections("failed_targets", projection.FailedTargets); err != nil {
		return err
	}
	if err := validateMutationTargetProjections("unattempted_targets", projection.UnattemptedTargets); err != nil {
		return err
	}
	return nil
}

type mutationScopeProjectionAuthority struct {
	source      MutationScopeRuleSource
	canonicalID string
}

type mutationScopeProjectionRule struct {
	kind        MutationScopeRuleKind
	source      MutationScopeRuleSource
	canonicalID string
}

func validateCanonicalMutationScopeProjectionShape(scope *CanonicalMutationScopeProjection) error {
	if scope == nil {
		return nil
	}
	if err := validateUniqueDurableMutationPaths("required_coverage_ids", scope.RequiredCoverageIDs); err != nil {
		return err
	}

	seenRules := make(map[mutationScopeProjectionRule]struct{}, len(scope.Rules))
	primaryRules := make(map[mutationScopeProjectionAuthority]MutationScopeRuleKind)
	requiredExactRules := make(map[string]struct{})
	for index, rule := range scope.Rules {
		field := fmt.Sprintf("rules[%d]", index)
		if !validMutationScopeRuleKind(rule.Kind) {
			return fmt.Errorf("%s.kind is invalid: %q", field, rule.Kind)
		}
		if !validMutationScopeRuleSource(rule.Source) {
			return fmt.Errorf("%s.source is invalid: %q", field, rule.Source)
		}
		if err := validateDurableMutationPath(field+".canonical_id", rule.CanonicalID); err != nil {
			return err
		}
		key := mutationScopeProjectionRule{kind: rule.Kind, source: rule.Source, canonicalID: rule.CanonicalID}
		if _, duplicate := seenRules[key]; duplicate {
			return fmt.Errorf("%s duplicates an earlier durable mutation scope rule", field)
		}
		seenRules[key] = struct{}{}

		if rule.Kind == MutationScopeDirectorySubtree && rule.Source == MutationScopeAuthorizedNewFile {
			return fmt.Errorf("%s cannot grant directory authority from %q", field, rule.Source)
		}
		if rule.Kind == MutationScopeExactFile || rule.Kind == MutationScopeDirectorySubtree {
			authority := mutationScopeProjectionAuthority{source: rule.Source, canonicalID: rule.CanonicalID}
			if existing, conflict := primaryRules[authority]; conflict && existing != rule.Kind {
				return fmt.Errorf("%s conflicts with another primary rule for the same source and canonical identity", field)
			}
			primaryRules[authority] = rule.Kind
		}
		if rule.Kind == MutationScopeExactFile && rule.Source == MutationScopeRequiredPath {
			requiredExactRules[rule.CanonicalID] = struct{}{}
		}
	}

	coverage := make(map[string]struct{}, len(scope.RequiredCoverageIDs))
	for _, canonicalID := range scope.RequiredCoverageIDs {
		if _, required := requiredExactRules[canonicalID]; !required {
			return fmt.Errorf("required coverage identity %q has no matching required exact-file rule", canonicalID)
		}
		coverage[canonicalID] = struct{}{}
	}
	for canonicalID := range requiredExactRules {
		if _, covered := coverage[canonicalID]; !covered {
			return fmt.Errorf("required exact-file rule %q is missing its coverage identity", canonicalID)
		}
	}

	seenPresentations := make(map[string]struct{}, len(scope.Presentations))
	presentedCanonical := make(map[string]string, len(scope.Presentations))
	presentedAuthorities := make(map[mutationScopeProjectionAuthority]struct{}, len(scope.Presentations))
	for index, presentation := range scope.Presentations {
		field := fmt.Sprintf("presentations[%d]", index)
		if !validMutationScopeRuleSource(presentation.Source) {
			return fmt.Errorf("%s.source is invalid: %q", field, presentation.Source)
		}
		if err := validateDurableMutationPath(field+".presented_path", presentation.PresentedPath); err != nil {
			return err
		}
		if err := validateDurableMutationPath(field+".canonical_id", presentation.CanonicalID); err != nil {
			return err
		}
		authority := mutationScopeProjectionAuthority{source: presentation.Source, canonicalID: presentation.CanonicalID}
		if _, matched := primaryRules[authority]; !matched {
			return fmt.Errorf("%s has no matching exact-file or directory-subtree rule", field)
		}
		key := presentation.PresentedPath + "\x00" + presentation.CanonicalID + "\x00" + string(presentation.Source)
		if _, duplicate := seenPresentations[key]; duplicate {
			return fmt.Errorf("%s duplicates an earlier durable mutation scope presentation", field)
		}
		seenPresentations[key] = struct{}{}
		if existing, seen := presentedCanonical[presentation.PresentedPath]; seen && existing != presentation.CanonicalID {
			return fmt.Errorf("%s ambiguously maps one presented path to multiple canonical identities", field)
		}
		presentedCanonical[presentation.PresentedPath] = presentation.CanonicalID
		presentedAuthorities[authority] = struct{}{}
	}
	for authority := range primaryRules {
		if _, presented := presentedAuthorities[authority]; !presented {
			return fmt.Errorf("primary rule %q from %q has no durable presentation", authority.canonicalID, authority.source)
		}
	}

	for index, rule := range scope.Rules {
		if rule.Kind != MutationScopeParentDirectoryCreation {
			continue
		}
		companion := false
		for authority, kind := range primaryRules {
			if kind == MutationScopeExactFile && authority.source == rule.Source && authority.canonicalID != rule.CanonicalID && canonicalIDWithinDirectory(authority.canonicalID, rule.CanonicalID) {
				companion = true
				break
			}
		}
		if !companion {
			return fmt.Errorf("rules[%d] parent-directory authority has no matching exact-file rule", index)
		}
	}
	return nil
}

func validateMutationTargetProjections(field string, targets []MutationTargetProjection) error {
	seen := make(map[string]struct{}, len(targets))
	for index, target := range targets {
		targetField := fmt.Sprintf("%s[%d]", field, index)
		if err := validateDurableMutationPath(targetField+".canonical_id", target.CanonicalID); err != nil {
			return err
		}
		if _, duplicate := seen[target.CanonicalID]; duplicate {
			return fmt.Errorf("%s duplicates an earlier canonical identity", targetField)
		}
		seen[target.CanonicalID] = struct{}{}
		if !validMutationTargetRole(target.Role) {
			return fmt.Errorf("%s.role is invalid: %q", targetField, target.Role)
		}
		if !validMutationPreimageClass(target.PreimageClass) {
			return fmt.Errorf("%s.preimage_class is invalid: %q", targetField, target.PreimageClass)
		}
		if (target.Role == MutationTargetDirectory) != (target.PreimageClass == MutationPreimageDirectory) {
			return fmt.Errorf("%s directory role and preimage class do not agree", targetField)
		}
	}
	return nil
}

func validateUniqueDurableMutationPaths(field string, paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	for index, path := range paths {
		if err := validateDurableMutationPath(fmt.Sprintf("%s[%d]", field, index), path); err != nil {
			return err
		}
		if _, duplicate := seen[path]; duplicate {
			return fmt.Errorf("%s[%d] duplicates an earlier canonical identity", field, index)
		}
		seen[path] = struct{}{}
	}
	return nil
}

func validateDurableMutationPath(field string, path string) error {
	if normalized := normalizeMutationScopeClaim(path); normalized == "" || normalized != path {
		return fmt.Errorf("%s must be a normalized relative workspace path: %q", field, path)
	}
	return nil
}

func validMutationScopeRuleKind(kind MutationScopeRuleKind) bool {
	switch kind {
	case MutationScopeExactFile, MutationScopeDirectorySubtree, MutationScopeParentDirectoryCreation:
		return true
	default:
		return false
	}
}

func validMutationScopeRuleSource(source MutationScopeRuleSource) bool {
	switch source {
	case MutationScopeRequiredPath, MutationScopeAllowedPath, MutationScopeAuthorizedNewFile:
		return true
	default:
		return false
	}
}

func validMutationCommitKind(kind MutationCommitKind) bool {
	switch kind {
	case MutationCommitCreatedDirectory, MutationCommitReplacedFile, MutationCommitAppendedFile, MutationCommitRemovedFile:
		return true
	default:
		return false
	}
}

func validMutationTargetRole(role MutationTargetRole) bool {
	switch role {
	case MutationTargetSource, MutationTargetDestination, MutationTargetFile, MutationTargetDirectory:
		return true
	default:
		return false
	}
}

func validMutationPreimageClass(class MutationPreimageClass) bool {
	switch class {
	case MutationPreimageRequired, MutationPreimageOptional, MutationPreimageAbsent, MutationPreimageDirectory:
		return true
	default:
		return false
	}
}

func appendUniqueMutationTargetProjection(values []MutationTargetProjection, target MutationRequestTarget) []MutationTargetProjection {
	for _, existing := range values {
		if existing.CanonicalID == target.Identity.CanonicalID && existing.Role == target.Role && existing.PreimageClass == target.Preimage {
			return values
		}
	}
	if target.Identity.CanonicalID != "" {
		values = append(values, MutationTargetProjection{
			CanonicalID:   target.Identity.CanonicalID,
			Role:          target.Role,
			PreimageClass: target.Preimage,
		})
	}
	return values
}

func appendUniqueCanonicalID(values []string, id string) []string {
	for _, existing := range values {
		if existing == id {
			return values
		}
	}
	if id != "" {
		values = append(values, id)
	}
	return values
}

func (m *workspaceMutation) commitReceipt(tool string) *MutationCommitReceipt {
	if m == nil {
		return nil
	}
	receipt := &MutationCommitReceipt{Tool: tool, Root: m.rootPath}
	if m.authorization != nil {
		receipt.Targets = cloneMutationRequestTargets(m.authorization.orderedTargets)
		if m.authorization.scopeBound {
			scope := cloneCanonicalMutationScope(m.authorization.scope)
			receipt.Scope = &scope
		}
	}
	receipt.Commits = make([]MutationCommit, 0, len(m.commits))
	for _, commit := range m.commits {
		canonicalID := filepath.ToSlash(filepath.Clean(commit.path))
		role := MutationTargetDirectory
		class := MutationPreimageDirectory
		if m.authorization != nil {
			if target, ok := m.authorization.targets[canonicalID]; ok {
				role = target.Role
				class = target.Preimage
			}
		}
		preimage := m.initialByPath[canonicalID]
		preimageReceipt := MutationPreimageReceipt{Class: class, Exists: preimage.exists}
		if preimage.exists {
			preimageReceipt.SHA256 = fmt.Sprintf("%x", preimage.digest[:])
		}
		receipt.Commits = append(receipt.Commits, MutationCommit{
			Identity: CanonicalWorkspacePath{
				CanonicalID:  canonicalID,
				AbsolutePath: filepath.Join(m.rootPath, filepath.FromSlash(canonicalID)),
				DisplayPath:  canonicalID,
			},
			Role:     role,
			Kind:     commit.kind,
			Preimage: preimageReceipt,
		})
	}
	if m.authorization != nil {
		if m.failedTargetID != "" {
			if target, ok := m.authorization.targets[m.failedTargetID]; ok {
				receipt.FailedTargets = append(receipt.FailedTargets, target)
			}
		}
		for _, id := range m.unattemptedIDs {
			if target, ok := m.authorization.targets[id]; ok {
				receipt.UnattemptedTargets = append(receipt.UnattemptedTargets, target)
			}
		}
	}
	receipt.rollback, receipt.rollbackErr = m.buildMutationRollbackState(receipt.Commits)
	receipt.fileChanges = m.buildMutationFileChanges(receipt.Commits)
	return receipt
}

func (m *workspaceMutation) buildMutationFileChanges(commits []MutationCommit) []MutationFileChange {
	if m == nil || len(commits) == 0 || len(m.commits) != len(commits) {
		return nil
	}

	changes := make([]MutationFileChange, 0, len(commits))
	for index, commit := range commits {
		privateCommit := m.commits[index]
		if commit.Kind == MutationCommitCreatedDirectory || !privateCommit.fileBodyExact {
			continue
		}
		preimage := m.initialByPath[commit.Identity.CanonicalID]
		change := MutationFileChange{
			canonicalID:  commit.Identity.CanonicalID,
			kind:         commit.Kind,
			before:       append([]byte(nil), preimage.body...),
			beforeExists: preimage.exists,
			after:        append([]byte(nil), privateCommit.fileBody...),
			afterExists:  privateCommit.fileExists,
		}
		if commit.Kind == MutationCommitAppendedFile && privateCommit.appendExact {
			if privateCommit.appendStart < 0 || privateCommit.appendStart > int64(len(privateCommit.fileBody)) {
				continue
			}
			change.before = append([]byte(nil), privateCommit.fileBody[:privateCommit.appendStart]...)
			change.beforeExists = preimage.exists
		}
		changes = append(changes, change)
	}
	return changes
}

func cloneMutationFileChanges(changes []MutationFileChange) []MutationFileChange {
	if changes == nil {
		return nil
	}
	cloned := make([]MutationFileChange, len(changes))
	for index, change := range changes {
		change.before = append([]byte(nil), change.before...)
		change.after = append([]byte(nil), change.after...)
		cloned[index] = change
	}
	return cloned
}

func (m *workspaceMutation) attachCommitReceipt(tool string, result *ToolResult) {
	if m == nil || result == nil {
		return
	}
	if m.checkpoint != nil && len(m.commits) != 0 {
		last := m.commits[len(m.commits)-1]
		m.checkpoint(workspaceMutationCheckpoint{
			Stage:          workspaceMutationBeforeReceipt,
			Path:           last.path,
			OperationIndex: len(m.commits) - 1,
		})
	}
	result.mutationCommitReceipt = m.commitReceipt(tool)
	result.ChangedPaths = result.mutationCommitReceipt.ChangedPaths()
}

func cloneMutationCommitReceipt(receipt *MutationCommitReceipt) *MutationCommitReceipt {
	if receipt == nil {
		return nil
	}
	cloned := *receipt
	cloned.Targets = cloneMutationRequestTargets(receipt.Targets)
	if receipt.Scope != nil {
		scope := cloneCanonicalMutationScope(*receipt.Scope)
		cloned.Scope = &scope
	}
	cloned.Commits = append([]MutationCommit(nil), receipt.Commits...)
	cloned.FailedTargets = cloneMutationRequestTargets(receipt.FailedTargets)
	cloned.UnattemptedTargets = cloneMutationRequestTargets(receipt.UnattemptedTargets)
	cloned.rollback = receipt.rollback
	cloned.rollbackErr = receipt.rollbackErr
	cloned.fileChanges = cloneMutationFileChanges(receipt.fileChanges)
	return &cloned
}
