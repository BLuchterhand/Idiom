package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

// MutationScopePaths is the runner-owned implementation contract before
// physical workspace identity binding. Required and allowed paths may name
// exact files or directory scopes inferred from the complete contract.
// AuthorizedNewFiles are exact absent files whose missing parent directories
// may be created.
type MutationScopePaths struct {
	RequiredPaths      []string
	AllowedPaths       []string
	AuthorizedNewFiles []string
	Workdir            string
}

type MutationScopeRuleKind string

const (
	MutationScopeExactFile               MutationScopeRuleKind = "exact_file"
	MutationScopeDirectorySubtree        MutationScopeRuleKind = "directory_subtree"
	MutationScopeParentDirectoryCreation MutationScopeRuleKind = "parent_directory_creation"
)

type MutationScopeRuleSource string

const (
	MutationScopeRequiredPath      MutationScopeRuleSource = "required_path"
	MutationScopeAllowedPath       MutationScopeRuleSource = "allowed_path"
	MutationScopeAuthorizedNewFile MutationScopeRuleSource = "authorized_new_file"
)

type MutationScopeRule struct {
	Kind     MutationScopeRuleKind
	Source   MutationScopeRuleSource
	Identity CanonicalWorkspacePath
}

type MutationScopePresentation struct {
	PresentedPath string
	CanonicalID   string
	Source        MutationScopeRuleSource
}

type MutationScopeClaimProjection struct {
	CanonicalIDs     []string
	UnsupportedPaths []string
}

// CanonicalMutationScope is the workspace-owned scope contract. RequiredCoverage
// is exact-file coverage only. Rules own mutation authorization; directory
// subtree rules never become coverage obligations.
type CanonicalMutationScope struct {
	RequiredCoverage []CanonicalWorkspacePath
	Rules            []MutationScopeRule
	Presentations    []MutationScopePresentation
}

type CanonicalMutationScopeRuleProjection struct {
	Kind        MutationScopeRuleKind   `json:"kind"`
	Source      MutationScopeRuleSource `json:"source"`
	CanonicalID string                  `json:"canonical_id"`
}

type CanonicalMutationScopePresentationProjection struct {
	PresentedPath string                  `json:"presented_path"`
	CanonicalID   string                  `json:"canonical_id"`
	Source        MutationScopeRuleSource `json:"source"`
}

type CanonicalMutationScopeProjection struct {
	RequiredCoverageIDs []string                                       `json:"required_coverage_ids,omitempty"`
	Rules               []CanonicalMutationScopeRuleProjection         `json:"rules,omitempty"`
	Presentations       []CanonicalMutationScopePresentationProjection `json:"presentations,omitempty"`
}

// DurableProjection strips roots and absolute paths while retaining only
// normalized relative presentation mappings needed for alias-safe resume.
func (scope CanonicalMutationScope) DurableProjection() CanonicalMutationScopeProjection {
	projection := CanonicalMutationScopeProjection{}
	for _, required := range scope.RequiredCoverage {
		if canonicalID := normalizeMutationScopeClaim(required.CanonicalID); canonicalID != "" {
			projection.RequiredCoverageIDs = appendUniqueCanonicalID(projection.RequiredCoverageIDs, canonicalID)
		}
	}
	for _, rule := range scope.Rules {
		canonicalID := normalizeMutationScopeClaim(rule.Identity.CanonicalID)
		if canonicalID == "" {
			continue
		}
		projection.Rules = append(projection.Rules, CanonicalMutationScopeRuleProjection{
			Kind: rule.Kind, Source: rule.Source, CanonicalID: canonicalID,
		})
	}
	seenPresentations := make(map[string]struct{}, len(scope.Presentations))
	for _, presentation := range scope.Presentations {
		presented := normalizeMutationScopeClaim(presentation.PresentedPath)
		canonicalID := normalizeMutationScopeClaim(presentation.CanonicalID)
		if presented == "" || canonicalID == "" {
			continue
		}
		key := presented + "\x00" + canonicalID + "\x00" + string(presentation.Source)
		if _, duplicate := seenPresentations[key]; duplicate {
			continue
		}
		seenPresentations[key] = struct{}{}
		projection.Presentations = append(projection.Presentations, CanonicalMutationScopePresentationProjection{
			PresentedPath: presented, CanonicalID: canonicalID, Source: presentation.Source,
		})
	}
	return projection
}

func (scope CanonicalMutationScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(scope.DurableProjection())
}

type mutationScopeContextKey struct{}

type mutationScopeBinding struct {
	canonical    CanonicalMutationScope
	rootPath     string
	rootIdentity os.FileInfo
}

// BindMutationScope canonicalizes and binds one invocation's implementation
// scope. Calling it with all three path sets empty deliberately binds an empty
// scope that denies every mutation. Omitting the binding keeps the existing
// workspace-root authorization behavior.
func (m *Manager) BindMutationScope(ctx context.Context, paths MutationScopePaths) (context.Context, CanonicalMutationScope, error) {
	if m == nil || !m.Enabled() {
		return ctx, CanonicalMutationScope{}, fmt.Errorf("workspace root is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rootPath := filepath.Clean(strings.TrimSpace(m.cfg.Root))
	rootIdentity, err := os.Lstat(rootPath)
	if err != nil || rootIdentity.Mode()&os.ModeSymlink != 0 || !rootIdentity.IsDir() {
		if err == nil {
			err = fmt.Errorf("workspace mutation root must be a directory and must not be a symlink")
		}
		return ctx, CanonicalMutationScope{}, err
	}

	canonical := CanonicalMutationScope{}
	isDirectory := func(path string) bool {
		identity, resolveErr := m.CanonicalizeWorkspacePath(path, paths.Workdir)
		if resolveErr != nil {
			return false
		}
		info, statErr := os.Lstat(identity.AbsolutePath)
		return statErr == nil && info.IsDir()
	}
	allContractPaths := make([]string, 0, len(paths.RequiredPaths)+len(paths.AllowedPaths)+len(paths.AuthorizedNewFiles))
	allContractPaths = append(allContractPaths, paths.RequiredPaths...)
	allContractPaths = append(allContractPaths, paths.AllowedPaths...)
	allContractPaths = append(allContractPaths, paths.AuthorizedNewFiles...)
	directoryContract := plancontract.RequiredScope(allContractPaths, nil, isDirectory)
	contractDirectories := mutationScopePathSet(directoryContract.DirectoryScopes)
	seenCoverage := make(map[string]struct{}, len(paths.RequiredPaths))
	seenRules := make(map[string]struct{}, len(paths.RequiredPaths)+len(paths.AllowedPaths)+len(paths.AuthorizedNewFiles)*2)
	seenPresentations := make(map[string]struct{}, len(paths.RequiredPaths)+len(paths.AllowedPaths)+len(paths.AuthorizedNewFiles))
	for _, path := range paths.RequiredPaths {
		identity, resolveErr := m.CanonicalizeWorkspacePath(path, paths.Workdir)
		if resolveErr != nil {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("canonicalize required mutation path %q: %w", path, resolveErr)
		}
		if identity.CanonicalID == "." {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("required mutation path %q: workspace root cannot be a mutation scope", path)
		}
		kind := MutationScopeExactFile
		if _, directory := contractDirectories[normalizeMutationScopeContractPath(path)]; directory {
			kind = MutationScopeDirectorySubtree
		}
		if kind == MutationScopeExactFile {
			if err := requireCanonicalFileScope(identity, false); err != nil {
				return ctx, CanonicalMutationScope{}, fmt.Errorf("required mutation path %q: %w", path, err)
			}
			if _, ok := seenCoverage[identity.CanonicalID]; !ok {
				seenCoverage[identity.CanonicalID] = struct{}{}
				canonical.RequiredCoverage = append(canonical.RequiredCoverage, identity)
			}
		}
		canonical.Rules = appendUniqueMutationScopeRule(canonical.Rules, seenRules, MutationScopeRule{
			Kind: kind, Source: MutationScopeRequiredPath, Identity: identity,
		})
		if kind == MutationScopeExactFile {
			if _, statErr := os.Lstat(identity.AbsolutePath); os.IsNotExist(statErr) {
				canonical.Rules = appendMutationScopeParentRules(canonical.Rules, seenRules, rootPath, identity, MutationScopeRequiredPath)
			}
		}
		canonical.Presentations = appendUniqueMutationScopePresentation(canonical.Presentations, seenPresentations, path, identity, MutationScopeRequiredPath)
	}

	for _, path := range paths.AllowedPaths {
		identity, resolveErr := m.CanonicalizeWorkspacePath(path, paths.Workdir)
		if resolveErr != nil {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("canonicalize allowed mutation path %q: %w", path, resolveErr)
		}
		if identity.CanonicalID == "." {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("allowed mutation path %q: workspace root cannot be a mutation scope", path)
		}
		kind := MutationScopeExactFile
		if _, directory := contractDirectories[normalizeMutationScopeContractPath(path)]; directory {
			kind = MutationScopeDirectorySubtree
		}
		if kind == MutationScopeExactFile {
			if err := requireCanonicalFileScope(identity, false); err != nil {
				return ctx, CanonicalMutationScope{}, fmt.Errorf("allowed mutation path %q: %w", path, err)
			}
		}
		canonical.Rules = appendUniqueMutationScopeRule(canonical.Rules, seenRules, MutationScopeRule{
			Kind: kind, Source: MutationScopeAllowedPath, Identity: identity,
		})
		if kind == MutationScopeExactFile {
			if _, statErr := os.Lstat(identity.AbsolutePath); os.IsNotExist(statErr) {
				canonical.Rules = appendMutationScopeParentRules(canonical.Rules, seenRules, rootPath, identity, MutationScopeAllowedPath)
			}
		}
		canonical.Presentations = appendUniqueMutationScopePresentation(canonical.Presentations, seenPresentations, path, identity, MutationScopeAllowedPath)
	}

	for _, path := range paths.AuthorizedNewFiles {
		identity, resolveErr := m.CanonicalizeWorkspacePath(path, paths.Workdir)
		if resolveErr != nil {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("canonicalize authorized new file %q: %w", path, resolveErr)
		}
		if identity.CanonicalID == "." {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("authorized new file %q: workspace root cannot be a mutation scope", path)
		}
		if err := requireCanonicalFileScope(identity, true); err != nil {
			return ctx, CanonicalMutationScope{}, fmt.Errorf("authorized new file %q: %w", path, err)
		}
		canonical.Rules = appendUniqueMutationScopeRule(canonical.Rules, seenRules, MutationScopeRule{
			Kind: MutationScopeExactFile, Source: MutationScopeAuthorizedNewFile, Identity: identity,
		})
		canonical.Presentations = appendUniqueMutationScopePresentation(canonical.Presentations, seenPresentations, path, identity, MutationScopeAuthorizedNewFile)
		canonical.Rules = appendMutationScopeParentRules(canonical.Rules, seenRules, rootPath, identity, MutationScopeAuthorizedNewFile)
	}

	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(rootIdentity, currentRoot) {
		if err == nil {
			err = fmt.Errorf("workspace mutation root changed while binding scope")
		}
		return ctx, CanonicalMutationScope{}, err
	}

	binding := mutationScopeBinding{
		canonical:    cloneCanonicalMutationScope(canonical),
		rootPath:     rootPath,
		rootIdentity: rootIdentity,
	}
	return context.WithValue(ctx, mutationScopeContextKey{}, binding), canonical, nil
}

func requireCanonicalFileScope(identity CanonicalWorkspacePath, mustBeAbsent bool) error {
	info, err := os.Lstat(identity.AbsolutePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if mustBeAbsent {
		return fmt.Errorf("path already exists")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("exact file scope must name a regular file or an absent file")
	}
	return nil
}

func mutationScopePathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if normalized := normalizeMutationScopeContractPath(path); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

func normalizeMutationScopeContractPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	return strings.Trim(path, "/")
}

func appendUniqueMutationScopeRule(rules []MutationScopeRule, seen map[string]struct{}, rule MutationScopeRule) []MutationScopeRule {
	key := string(rule.Kind) + "\x00" + string(rule.Source) + "\x00" + rule.Identity.CanonicalID
	if _, ok := seen[key]; ok {
		return rules
	}
	seen[key] = struct{}{}
	return append(rules, rule)
}

func appendMutationScopeParentRules(rules []MutationScopeRule, seen map[string]struct{}, rootPath string, identity CanonicalWorkspacePath, source MutationScopeRuleSource) []MutationScopeRule {
	for parent := filepath.Dir(filepath.FromSlash(identity.CanonicalID)); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
		parentID := filepath.ToSlash(filepath.Clean(parent))
		rules = appendUniqueMutationScopeRule(rules, seen, MutationScopeRule{
			Kind:   MutationScopeParentDirectoryCreation,
			Source: source,
			Identity: CanonicalWorkspacePath{
				CanonicalID:  parentID,
				AbsolutePath: filepath.Join(rootPath, filepath.FromSlash(parentID)),
				DisplayPath:  parentID,
			},
		})
	}
	return rules
}

func appendUniqueMutationScopePresentation(values []MutationScopePresentation, seen map[string]struct{}, presented string, identity CanonicalWorkspacePath, source MutationScopeRuleSource) []MutationScopePresentation {
	presented = normalizeMutationScopeClaim(presented)
	key := presented + "\x00" + identity.CanonicalID + "\x00" + string(source)
	if _, ok := seen[key]; ok {
		return values
	}
	seen[key] = struct{}{}
	return append(values, MutationScopePresentation{PresentedPath: presented, CanonicalID: identity.CanonicalID, Source: source})
}

func mutationScopeFromContext(ctx context.Context) (mutationScopeBinding, bool) {
	if ctx == nil {
		return mutationScopeBinding{}, false
	}
	binding, ok := ctx.Value(mutationScopeContextKey{}).(mutationScopeBinding)
	return binding, ok
}

func (scope CanonicalMutationScope) authorizes(tool string, target MutationRequestTarget) bool {
	if scope.requiresAbsent(target.Identity.CanonicalID) {
		if target.Role == MutationTargetDirectory {
			return false
		}
		_, err := os.Lstat(target.Identity.AbsolutePath)
		return os.IsNotExist(err)
	}
	for _, rule := range scope.Rules {
		switch rule.Kind {
		case MutationScopeExactFile:
			if target.Role == MutationTargetDirectory || target.Identity.CanonicalID != rule.Identity.CanonicalID {
				continue
			}
			return true
		case MutationScopeDirectorySubtree:
			if canonicalIDWithinDirectory(target.Identity.CanonicalID, rule.Identity.CanonicalID) {
				return true
			}
		case MutationScopeParentDirectoryCreation:
			if strings.TrimSpace(tool) == ToolNameMkdir && target.Role == MutationTargetDirectory && target.Identity.CanonicalID == rule.Identity.CanonicalID {
				return true
			}
		}
	}
	return false
}

// authorizesDurableTarget checks whether a canonical receipt target is covered
// by the live bound rule set. It intentionally does not repeat transient
// preimage checks: a successfully created authorized-new file exists on resume,
// and implicit parent-directory effects have no standalone tool request.
func (scope CanonicalMutationScope) authorizesDurableTarget(canonicalID string, role MutationTargetRole) bool {
	for _, rule := range scope.Rules {
		switch rule.Kind {
		case MutationScopeExactFile:
			if role != MutationTargetDirectory && canonicalID == rule.Identity.CanonicalID {
				return true
			}
		case MutationScopeDirectorySubtree:
			if canonicalIDWithinDirectory(canonicalID, rule.Identity.CanonicalID) {
				return true
			}
		case MutationScopeParentDirectoryCreation:
			if role == MutationTargetDirectory && canonicalID == rule.Identity.CanonicalID {
				return true
			}
		}
	}
	return false
}

func (scope CanonicalMutationScope) requiresAbsent(canonicalID string) bool {
	for _, rule := range scope.Rules {
		if rule.Kind == MutationScopeExactFile && rule.Source == MutationScopeAuthorizedNewFile && rule.Identity.CanonicalID == canonicalID {
			return true
		}
	}
	return false
}

func (scope CanonicalMutationScope) allowsParentCreation(canonicalID string) bool {
	for _, rule := range scope.Rules {
		switch rule.Kind {
		case MutationScopeDirectorySubtree:
			if canonicalIDWithinDirectory(canonicalID, rule.Identity.CanonicalID) {
				return true
			}
		case MutationScopeParentDirectoryCreation:
			if rule.Identity.CanonicalID == canonicalID {
				return true
			}
		}
	}
	return false
}

func canonicalIDWithinDirectory(id string, directory string) bool {
	id = filepath.ToSlash(filepath.Clean(filepath.FromSlash(id)))
	directory = filepath.ToSlash(filepath.Clean(filepath.FromSlash(directory)))
	return id == directory || strings.HasPrefix(id, directory+"/")
}

// ProjectClaims maps final-answer or completion claims through the immutable
// presented-to-canonical identities captured when scope was bound. It never
// consults the live filesystem, so alias retargeting cannot rewrite history.
func (scope CanonicalMutationScopeProjection) ProjectClaims(paths []string) MutationScopeClaimProjection {
	type exactPresentation struct {
		canonicalID string
		ambiguous   bool
	}

	presented := make(map[string]exactPresentation, len(scope.Presentations))
	canonical := make(map[string]struct{}, len(scope.Rules)+len(scope.RequiredCoverageIDs))
	directoryRules := make(map[string]map[MutationScopeRuleSource]struct{})
	directoryCanonical := make([]mutationScopeClaimPrefix, 0, len(scope.Rules))
	for _, entry := range scope.Presentations {
		normalized := normalizeMutationScopeClaim(entry.PresentedPath)
		canonicalID := normalizeMutationScopeClaim(entry.CanonicalID)
		if normalized == "" || canonicalID == "" {
			continue
		}
		if existing, ok := presented[normalized]; ok {
			existing.ambiguous = existing.ambiguous || existing.canonicalID != canonicalID
			presented[normalized] = existing
		} else {
			presented[normalized] = exactPresentation{canonicalID: canonicalID}
		}
		canonical[canonicalID] = struct{}{}
	}
	for _, rule := range scope.Rules {
		canonicalID := normalizeMutationScopeClaim(rule.CanonicalID)
		if canonicalID == "" {
			continue
		}
		canonical[canonicalID] = struct{}{}
		if rule.Kind == MutationScopeDirectorySubtree {
			if directoryRules[canonicalID] == nil {
				directoryRules[canonicalID] = make(map[MutationScopeRuleSource]struct{})
				directoryCanonical = append(directoryCanonical, newMutationScopeClaimPrefix(canonicalID, canonicalID))
			}
			directoryRules[canonicalID][rule.Source] = struct{}{}
		}
	}
	for _, required := range scope.RequiredCoverageIDs {
		if canonicalID := normalizeMutationScopeClaim(required); canonicalID != "" {
			canonical[canonicalID] = struct{}{}
		}
	}

	directoryPresentations := make([]mutationScopeClaimPrefix, 0, len(scope.Presentations))
	for _, entry := range scope.Presentations {
		presentedPath := normalizeMutationScopeClaim(entry.PresentedPath)
		canonicalID := normalizeMutationScopeClaim(entry.CanonicalID)
		if presentedPath == "" || canonicalID == "" {
			continue
		}
		sources := directoryRules[canonicalID]
		if _, directory := sources[entry.Source]; !directory {
			continue
		}
		directoryPresentations = append(directoryPresentations, newMutationScopeClaimPrefix(presentedPath, canonicalID))
	}
	sortMutationScopeClaimPrefixes(directoryPresentations)
	sortMutationScopeClaimPrefixes(directoryCanonical)

	projection := MutationScopeClaimProjection{}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		claim := normalizeMutationScopeClaim(path)
		id := ""
		ok := false
		if exact, exactMatch := presented[claim]; exactMatch {
			if exact.ambiguous {
				projection.UnsupportedPaths = append(projection.UnsupportedPaths, strings.TrimSpace(path))
				continue
			}
			id, ok = exact.canonicalID, true
		} else {
			if _, canonicalClaim := canonical[claim]; canonicalClaim {
				id, ok = claim, true
			}
		}
		if !ok && claim != "" {
			var ambiguous bool
			id, ok, ambiguous = projectMutationScopeClaimThroughPrefixes(claim, directoryPresentations)
			if ambiguous {
				projection.UnsupportedPaths = append(projection.UnsupportedPaths, strings.TrimSpace(path))
				continue
			}
		}
		if !ok && claim != "" {
			var ambiguous bool
			id, ok, ambiguous = projectMutationScopeClaimThroughPrefixes(claim, directoryCanonical)
			if ambiguous {
				projection.UnsupportedPaths = append(projection.UnsupportedPaths, strings.TrimSpace(path))
				continue
			}
		}
		if !ok {
			projection.UnsupportedPaths = append(projection.UnsupportedPaths, strings.TrimSpace(path))
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		projection.CanonicalIDs = append(projection.CanonicalIDs, id)
	}
	return projection
}

type mutationScopeClaimPrefix struct {
	presentedPath string
	canonicalID   string
	segmentDepth  int
}

func newMutationScopeClaimPrefix(presentedPath string, canonicalID string) mutationScopeClaimPrefix {
	return mutationScopeClaimPrefix{
		presentedPath: presentedPath,
		canonicalID:   canonicalID,
		segmentDepth:  strings.Count(presentedPath, "/") + 1,
	}
}

func sortMutationScopeClaimPrefixes(prefixes []mutationScopeClaimPrefix) {
	sort.Slice(prefixes, func(i int, j int) bool {
		left := prefixes[i]
		right := prefixes[j]
		if left.segmentDepth != right.segmentDepth {
			return left.segmentDepth > right.segmentDepth
		}
		if len(left.presentedPath) != len(right.presentedPath) {
			return len(left.presentedPath) > len(right.presentedPath)
		}
		if left.presentedPath != right.presentedPath {
			return left.presentedPath < right.presentedPath
		}
		return left.canonicalID < right.canonicalID
	})
}

func projectMutationScopeClaimThroughPrefixes(claim string, prefixes []mutationScopeClaimPrefix) (string, bool, bool) {
	projectedID := ""
	matchedDepth := -1
	matchedLength := -1
	for _, prefix := range prefixes {
		if matchedDepth >= 0 && (prefix.segmentDepth < matchedDepth || len(prefix.presentedPath) < matchedLength) {
			break
		}
		suffix, descendant := mutationScopeDescendantSuffix(claim, prefix.presentedPath)
		if !descendant {
			continue
		}
		candidateID := pathpkg.Join(prefix.canonicalID, suffix)
		if matchedDepth < 0 {
			projectedID = candidateID
			matchedDepth = prefix.segmentDepth
			matchedLength = len(prefix.presentedPath)
			continue
		}
		if candidateID != projectedID {
			return "", false, true
		}
	}
	return projectedID, matchedDepth >= 0, false
}

func normalizeMutationScopeClaim(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\", "/")
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return ""
		}
	}
	cleaned := pathpkg.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func mutationScopeDescendantSuffix(claim string, directory string) (string, bool) {
	if claim == "" || directory == "" || !strings.HasPrefix(claim, directory+"/") {
		return "", false
	}
	suffix := strings.TrimPrefix(claim, directory+"/")
	if suffix == "" || normalizeMutationScopeClaim(suffix) != suffix {
		return "", false
	}
	return suffix, true
}

func cloneCanonicalMutationScope(scope CanonicalMutationScope) CanonicalMutationScope {
	return CanonicalMutationScope{
		RequiredCoverage: append([]CanonicalWorkspacePath(nil), scope.RequiredCoverage...),
		Rules:            append([]MutationScopeRule(nil), scope.Rules...),
		Presentations:    append([]MutationScopePresentation(nil), scope.Presentations...),
	}
}
