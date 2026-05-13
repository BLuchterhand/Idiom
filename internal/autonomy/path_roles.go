package autonomy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type PathInvariantID string

const (
	InvariantRequiredEditPath   PathInvariantID = "path.required_edit"
	InvariantEvidenceReadonly   PathInvariantID = "path.evidence_readonly"
	InvariantProtectedPath      PathInvariantID = "path.protected"
	InvariantForbiddenPath      PathInvariantID = "path.forbidden"
	InvariantExternalEffectPath PathInvariantID = "path.external_side_effect"
)

// PathInvariant is the named host-owned contract attached to a path role.
// Higher Precedence wins when one path matches multiple roles. The losing roles
// are still retained on PathRoleResolution so audit and future obligation
// migration can see all evidence, not just the effective policy result.
type PathInvariant struct {
	InvariantID PathInvariantID    `json:"invariant_id"`
	Role        PathRoleKind       `json:"role"`
	Precedence  int                `json:"precedence"`
	Decision    ToolPolicyDecision `json:"decision,omitempty"`
	Obligation  ObligationKind     `json:"obligation,omitempty"`
	SideEffect  SideEffectClass    `json:"side_effect,omitempty"`
	ReasonCode  string             `json:"reason_code,omitempty"`
	Description string             `json:"description,omitempty"`
}

// PathRoleResolverInput keeps resolution passive: callers provide contract path
// roles, evidence references, and metadata, and the resolver returns typed
// evidence without enforcing any lane/gate decision itself.
type PathRoleResolverInput struct {
	WorkspaceRoot string
	PathRoles     []PathRole
	Evidence      []EvidenceRef
	Metadata      ContractMetadata
	Source        string
}

// PathRoleResolutionSet is shaped for future policy events: each path has
// typed role matches, an effective invariant, role counts, evidence refs, and
// metadata. No field prescribes a next model strategy.
type PathRoleResolutionSet struct {
	WorkspaceRoot string                   `json:"workspace_root,omitempty"`
	Source        string                   `json:"source,omitempty"`
	Resolutions   []PathRoleResolution     `json:"resolutions,omitempty"`
	RoleCounts    map[string]int           `json:"role_counts,omitempty"`
	EvidenceRefs  []string                 `json:"evidence_refs,omitempty"`
	Metadata      ContractMetadata         `json:"metadata,omitempty"`
	Invariants    map[string]PathInvariant `json:"invariants,omitempty"`
}

// PathRoleResolution records both the canonical path identity and every matched
// role. With WorkspaceRoot set, paths inside the workspace resolve to
// RelativePath/Path and paths outside keep an absolute Path with
// OutsideWorkspace true. Without WorkspaceRoot, relative and absolute inputs are
// normalized independently and matched against the supplied role patterns.
type PathRoleResolution struct {
	OriginalPath         string             `json:"original_path,omitempty"`
	Path                 string             `json:"path,omitempty"`
	RelativePath         string             `json:"relative_path,omitempty"`
	AbsolutePath         string             `json:"absolute_path,omitempty"`
	OutsideWorkspace     bool               `json:"outside_workspace,omitempty"`
	Roles                []ResolvedPathRole `json:"roles,omitempty"`
	Matches              []PathRoleMatch    `json:"matches,omitempty"`
	EffectiveRole        PathRoleKind       `json:"effective_role,omitempty"`
	EffectiveInvariantID PathInvariantID    `json:"effective_invariant_id,omitempty"`
	Decision             ToolPolicyDecision `json:"decision,omitempty"`
	ReasonCode           string             `json:"reason_code,omitempty"`
	Conflicts            []PathRoleConflict `json:"conflicts,omitempty"`
	EvidenceRefs         []string           `json:"evidence_refs,omitempty"`
}

// ResolvedPathRole aggregates all matching patterns for one role on one path,
// so repeated patterns never create duplicate role entries.
type ResolvedPathRole struct {
	Role            PathRoleKind       `json:"role"`
	InvariantID     PathInvariantID    `json:"invariant_id"`
	Precedence      int                `json:"precedence"`
	Decision        ToolPolicyDecision `json:"decision,omitempty"`
	Obligation      ObligationKind     `json:"obligation,omitempty"`
	SideEffect      SideEffectClass    `json:"side_effect,omitempty"`
	ReasonCode      string             `json:"reason_code,omitempty"`
	MatchedPatterns []string           `json:"matched_patterns,omitempty"`
	PacketIDs       []string           `json:"packet_ids,omitempty"`
	Freshness       Freshness          `json:"freshness,omitempty"`
	Reasons         []string           `json:"reasons,omitempty"`
	EvidenceRefs    []string           `json:"evidence_refs,omitempty"`
}

// PathRoleConflict explains precedence without deleting suppressed role
// evidence. The precedence order is forbidden, protected, external side effect,
// required edit, then read-only evidence.
type PathRoleConflict struct {
	Path                string          `json:"path,omitempty"`
	EffectiveRole       PathRoleKind    `json:"effective_role,omitempty"`
	SuppressedRole      PathRoleKind    `json:"suppressed_role,omitempty"`
	EffectiveInvariant  PathInvariantID `json:"effective_invariant_id,omitempty"`
	SuppressedInvariant PathInvariantID `json:"suppressed_invariant_id,omitempty"`
	Reason              string          `json:"reason,omitempty"`
}

// NamedPathInvariants returns a copy of the invariant table used by the shared
// resolver. Callers cannot mutate the package-level contract map.
func NamedPathInvariants() map[PathInvariantID]PathInvariant {
	out := make(map[PathInvariantID]PathInvariant, len(pathInvariantMap))
	for id, invariant := range pathInvariantMap {
		out[id] = invariant
	}
	return out
}

// PathInvariantForRole returns the named invariant for resolver-supported path
// roles: required edit, read-only evidence, protected, forbidden, and external
// side-effect paths.
func PathInvariantForRole(role PathRoleKind) (PathInvariant, bool) {
	invariantID, ok := pathInvariantByRole[role]
	if !ok {
		return PathInvariant{}, false
	}
	invariant, ok := pathInvariantMap[invariantID]
	return invariant, ok
}

// ResolveContractPathRoles resolves paths against an AutonomyContract's path
// roles, evidence refs, and metadata without changing the contract or emitting
// policy decisions.
func ResolveContractPathRoles(contract AutonomyContract, workspaceRoot string, paths ...string) (PathRoleResolutionSet, error) {
	return ResolvePathRoles(PathRoleResolverInput{
		WorkspaceRoot: workspaceRoot,
		PathRoles:     contract.PathRoles,
		Evidence:      contract.Evidence,
		Metadata:      contract.Metadata,
		Source:        "autonomy_contract:" + contract.ContractID,
	}, paths...)
}

// ResolvePathRoles normalizes paths and role patterns, supports exact and glob
// matching including ** across path separators, deduplicates repeated roles, and
// selects the highest-precedence matched invariant as the effective role.
func ResolvePathRoles(input PathRoleResolverInput, paths ...string) (PathRoleResolutionSet, error) {
	workspaceRoot, err := normalizeWorkspaceRoot(input.WorkspaceRoot)
	if err != nil {
		return PathRoleResolutionSet{}, err
	}
	roles := normalizeResolverPathRoles(input.PathRoles, workspaceRoot)
	evidenceRefs := evidenceRefIndex(input.Evidence)
	result := PathRoleResolutionSet{
		WorkspaceRoot: workspaceRoot,
		Source:        strings.TrimSpace(input.Source),
		Metadata:      copyMetadata(input.Metadata),
		Invariants:    namedPathInvariantsByString(),
	}
	result.Metadata.normalize()
	for _, rawPath := range paths {
		candidate, err := normalizeResolverPath(rawPath, workspaceRoot)
		if err != nil {
			return PathRoleResolutionSet{}, err
		}
		resolution := resolveOnePath(candidate, roles, evidenceRefs)
		result.Resolutions = append(result.Resolutions, resolution)
		for _, role := range resolution.Roles {
			if result.RoleCounts == nil {
				result.RoleCounts = make(map[string]int)
			}
			result.RoleCounts[string(role.Role)]++
			result.EvidenceRefs = append(result.EvidenceRefs, role.EvidenceRefs...)
		}
		result.EvidenceRefs = append(result.EvidenceRefs, resolution.EvidenceRefs...)
	}
	result.EvidenceRefs = compactStrings(result.EvidenceRefs)
	sort.Slice(result.Resolutions, func(i, j int) bool {
		return result.Resolutions[i].Path < result.Resolutions[j].Path
	})
	return result, nil
}

// PolicyPathMatches flattens path matches into the event payload type already
// used by autonomy policy events.
func (s PathRoleResolutionSet) PolicyPathMatches() []PathRoleMatch {
	var out []PathRoleMatch
	for _, resolution := range s.Resolutions {
		out = append(out, resolution.Matches...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sortKey() < out[j].sortKey() })
	return out
}

var pathInvariantMap = map[PathInvariantID]PathInvariant{
	InvariantRequiredEditPath: {
		InvariantID: InvariantRequiredEditPath,
		Role:        PathRoleRequiredEdit,
		Precedence:  200,
		Decision:    PolicyAllow,
		Obligation:  ObligationWrite,
		SideEffect:  SideEffectAdditive,
		ReasonCode:  "required_edit_path",
		Description: "path is inside the required write contract",
	},
	InvariantEvidenceReadonly: {
		InvariantID: InvariantEvidenceReadonly,
		Role:        PathRoleEvidenceReadonly,
		Precedence:  100,
		Decision:    PolicyObserve,
		Obligation:  ObligationEvidence,
		SideEffect:  SideEffectReadOnly,
		ReasonCode:  "evidence_readonly_path",
		Description: "path may support evidence but is not a write target",
	},
	InvariantProtectedPath: {
		InvariantID: InvariantProtectedPath,
		Role:        PathRoleProtected,
		Precedence:  400,
		Decision:    PolicyDeny,
		SideEffect:  SideEffectDestructive,
		ReasonCode:  "protected_path",
		Description: "path is protected by a host-owned safety boundary",
	},
	InvariantForbiddenPath: {
		InvariantID: InvariantForbiddenPath,
		Role:        PathRoleForbidden,
		Precedence:  500,
		Decision:    PolicyDeny,
		SideEffect:  SideEffectDestructive,
		ReasonCode:  "forbidden_path",
		Description: "path is outside the current contract",
	},
	InvariantExternalEffectPath: {
		InvariantID: InvariantExternalEffectPath,
		Role:        PathRoleExternalEffect,
		Precedence:  300,
		Decision:    PolicyAsk,
		Obligation:  ObligationApproval,
		SideEffect:  SideEffectOpenWorld,
		ReasonCode:  "external_side_effect_path",
		Description: "path implies an external or non-local side effect",
	},
}

var pathInvariantByRole = map[PathRoleKind]PathInvariantID{
	PathRoleRequiredEdit:     InvariantRequiredEditPath,
	PathRoleEvidenceReadonly: InvariantEvidenceReadonly,
	PathRoleProtected:        InvariantProtectedPath,
	PathRoleForbidden:        InvariantForbiddenPath,
	PathRoleExternalEffect:   InvariantExternalEffectPath,
}

type normalizedResolverPathRole struct {
	role            PathRole
	pathPattern     string
	absolutePattern string
}

type normalizedResolverPath struct {
	original         string
	path             string
	relativePath     string
	absolutePath     string
	outsideWorkspace bool
	matchCandidates  []string
}

func resolveOnePath(candidate normalizedResolverPath, roles []normalizedResolverPathRole, evidenceRefs map[string][]string) PathRoleResolution {
	byRole := make(map[PathRoleKind]*ResolvedPathRole)
	var matches []PathRoleMatch
	for _, role := range roles {
		if !pathRoleMatches(role, candidate) {
			continue
		}
		invariant, ok := PathInvariantForRole(role.role.Role)
		if !ok {
			continue
		}
		resolved := byRole[role.role.Role]
		if resolved == nil {
			resolved = &ResolvedPathRole{
				Role:        role.role.Role,
				InvariantID: invariant.InvariantID,
				Precedence:  invariant.Precedence,
				Decision:    invariant.Decision,
				Obligation:  invariant.Obligation,
				SideEffect:  invariant.SideEffect,
				ReasonCode:  invariant.ReasonCode,
				Freshness:   role.role.Freshness,
			}
			byRole[role.role.Role] = resolved
		}
		resolved.MatchedPatterns = append(resolved.MatchedPatterns, role.pathPattern)
		resolved.PacketIDs = append(resolved.PacketIDs, role.role.PacketID)
		resolved.Reasons = append(resolved.Reasons, role.role.Reason)
		resolved.EvidenceRefs = append(resolved.EvidenceRefs, evidenceRefsForRole(role.role, evidenceRefs)...)
		resolved.Freshness = mergeFreshness(resolved.Freshness, role.role.Freshness)
		matches = append(matches, PathRoleMatch{
			Path:             candidate.path,
			Role:             role.role.Role,
			MatchedPattern:   role.pathPattern,
			PacketID:         role.role.PacketID,
			Freshness:        role.role.Freshness,
			OutsideWorkspace: candidate.outsideWorkspace,
		})
	}
	resolution := PathRoleResolution{
		OriginalPath:     candidate.original,
		Path:             candidate.path,
		RelativePath:     candidate.relativePath,
		AbsolutePath:     candidate.absolutePath,
		OutsideWorkspace: candidate.outsideWorkspace,
		Matches:          compactPathRoleMatches(matches),
	}
	for _, role := range byRole {
		role.MatchedPatterns = compactStrings(role.MatchedPatterns)
		role.PacketIDs = compactStrings(role.PacketIDs)
		role.Reasons = compactStrings(role.Reasons)
		role.EvidenceRefs = compactStrings(role.EvidenceRefs)
		resolution.Roles = append(resolution.Roles, *role)
		resolution.EvidenceRefs = append(resolution.EvidenceRefs, role.EvidenceRefs...)
	}
	sort.Slice(resolution.Roles, func(i, j int) bool {
		if resolution.Roles[i].Precedence == resolution.Roles[j].Precedence {
			return resolution.Roles[i].Role < resolution.Roles[j].Role
		}
		return resolution.Roles[i].Precedence > resolution.Roles[j].Precedence
	})
	if len(resolution.Roles) > 0 {
		effective := resolution.Roles[0]
		resolution.EffectiveRole = effective.Role
		resolution.EffectiveInvariantID = effective.InvariantID
		resolution.Decision = effective.Decision
		resolution.ReasonCode = effective.ReasonCode
		for _, suppressed := range resolution.Roles[1:] {
			resolution.Conflicts = append(resolution.Conflicts, PathRoleConflict{
				Path:                resolution.Path,
				EffectiveRole:       effective.Role,
				SuppressedRole:      suppressed.Role,
				EffectiveInvariant:  effective.InvariantID,
				SuppressedInvariant: suppressed.InvariantID,
				Reason:              "higher-precedence path invariant wins; suppressed role remains recorded as audit evidence",
			})
		}
	}
	resolution.EvidenceRefs = compactStrings(resolution.EvidenceRefs)
	sort.Slice(resolution.Conflicts, func(i, j int) bool {
		return string(resolution.Conflicts[i].SuppressedRole) < string(resolution.Conflicts[j].SuppressedRole)
	})
	return resolution
}

func normalizeWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("normalize workspace root: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func normalizeResolverPathRoles(roles []PathRole, workspaceRoot string) []normalizedResolverPathRole {
	out := make([]normalizedResolverPathRole, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role.normalize()
		if _, ok := PathInvariantForRole(role.Role); !ok {
			continue
		}
		if role.PathPattern == "" {
			continue
		}
		normalizedPattern, absolutePattern := normalizeRolePattern(role.PathPattern, workspaceRoot)
		if normalizedPattern == "" && absolutePattern == "" {
			continue
		}
		key := strings.Join([]string{string(role.Role), normalizedPattern, absolutePattern, role.PacketID, role.Reason}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalizedResolverPathRole{role: role, pathPattern: normalizedPattern, absolutePattern: absolutePattern})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join([]string{string(out[i].role.Role), out[i].pathPattern, out[i].absolutePattern}, "\x00") <
			strings.Join([]string{string(out[j].role.Role), out[j].pathPattern, out[j].absolutePattern}, "\x00")
	})
	return out
}

func normalizeRolePattern(pattern string, workspaceRoot string) (string, string) {
	pattern = normalizeSlashPath(pattern)
	if pattern == "" {
		return "", ""
	}
	if filepath.IsAbs(filepath.FromSlash(pattern)) {
		cleanPattern := filepath.Clean(filepath.FromSlash(pattern))
		absolutePattern := filepath.ToSlash(cleanPattern)
		if workspaceRoot != "" {
			if rel, ok := relativeToWorkspace(cleanPattern, workspaceRoot); ok {
				return rel, absolutePattern
			}
		}
		return "", absolutePattern
	}
	return strings.TrimPrefix(pattern, "./"), ""
}

func normalizeResolverPath(path string, workspaceRoot string) (normalizedResolverPath, error) {
	original := strings.TrimSpace(path)
	if original == "" {
		return normalizedResolverPath{}, fmt.Errorf("path role resolver path is empty")
	}
	cleanPath := filepath.Clean(filepath.FromSlash(original))
	candidate := normalizedResolverPath{original: original}
	if workspaceRoot != "" {
		if filepath.IsAbs(cleanPath) {
			candidate.absolutePath = filepath.Clean(cleanPath)
		} else {
			candidate.absolutePath = filepath.Clean(filepath.Join(workspaceRoot, cleanPath))
		}
		if rel, ok := relativeToWorkspace(candidate.absolutePath, workspaceRoot); ok {
			candidate.relativePath = rel
			candidate.path = rel
		} else {
			candidate.outsideWorkspace = true
			candidate.path = filepath.ToSlash(candidate.absolutePath)
		}
	} else if filepath.IsAbs(cleanPath) {
		candidate.absolutePath = filepath.Clean(cleanPath)
		candidate.path = filepath.ToSlash(candidate.absolutePath)
	} else {
		candidate.relativePath = normalizeSlashPath(original)
		candidate.path = candidate.relativePath
	}
	candidate.absolutePath = filepath.ToSlash(candidate.absolutePath)
	candidate.matchCandidates = compactStrings([]string{candidate.path, candidate.relativePath, candidate.absolutePath})
	return candidate, nil
}

func relativeToWorkspace(path string, workspaceRoot string) (string, bool) {
	if workspaceRoot == "" {
		return "", false
	}
	rel, err := filepath.Rel(workspaceRoot, path)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func pathRoleMatches(role normalizedResolverPathRole, candidate normalizedResolverPath) bool {
	for _, value := range candidate.matchCandidates {
		if value == "" {
			continue
		}
		if role.pathPattern != "" && pathPatternMatches(role.pathPattern, value) {
			return true
		}
		if role.absolutePattern != "" && pathPatternMatches(role.absolutePattern, value) {
			return true
		}
	}
	return false
}

func pathPatternMatches(pattern string, candidate string) bool {
	pattern = normalizeSlashPath(pattern)
	candidate = normalizeSlashPath(candidate)
	if pattern == "" || candidate == "" {
		return false
	}
	if pattern == candidate {
		return true
	}
	matched, err := regexp.MatchString("^"+globPatternToRegex(pattern)+"$", candidate)
	return err == nil && matched
}

func globPatternToRegex(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	return b.String()
}

func normalizeSlashPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "." {
		return ""
	}
	return strings.TrimPrefix(path, "./")
}

func evidenceRefIndex(evidence []EvidenceRef) map[string][]string {
	out := make(map[string][]string)
	for _, ref := range evidence {
		ref.normalize()
		if ref.EvidenceID == "" {
			continue
		}
		for _, key := range []string{ref.EvidenceID, ref.Handle, normalizeSlashPath(ref.Path)} {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			out[key] = append(out[key], ref.EvidenceID)
		}
	}
	for key := range out {
		out[key] = compactStrings(out[key])
	}
	return out
}

func evidenceRefsForRole(role PathRole, evidenceRefs map[string][]string) []string {
	var refs []string
	for _, key := range []string{role.PacketID, "context_pack://" + role.PacketID, role.PathPattern} {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		refs = append(refs, evidenceRefs[key]...)
		refs = append(refs, evidenceRefs[normalizeSlashPath(key)]...)
	}
	return compactStrings(refs)
}

func mergeFreshness(current Freshness, next Freshness) Freshness {
	if current == "" {
		return next
	}
	if next == "" || next == current {
		return current
	}
	if current == FreshnessStale || next == FreshnessStale {
		return FreshnessStale
	}
	return FreshnessUnknown
}

func compactPathRoleMatches(matches []PathRoleMatch) []PathRoleMatch {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]PathRoleMatch, 0, len(matches))
	for _, match := range matches {
		match.normalize()
		key := match.sortKey()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, match)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sortKey() < out[j].sortKey() })
	return out
}

func namedPathInvariantsByString() map[string]PathInvariant {
	invariants := NamedPathInvariants()
	out := make(map[string]PathInvariant, len(invariants))
	for id, invariant := range invariants {
		out[string(id)] = invariant
	}
	return out
}
