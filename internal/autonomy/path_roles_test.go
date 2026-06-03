package autonomy

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNamedPathInvariantsCoverCorePathRoles(t *testing.T) {
	tests := []struct {
		role      PathRoleKind
		invariant PathInvariantID
		decision  ToolPolicyDecision
	}{
		{role: PathRoleRequiredEdit, invariant: InvariantRequiredEditPath, decision: PolicyAllow},
		{role: PathRoleAllowedEdit, invariant: InvariantAllowedCandidate, decision: PolicyAllow},
		{role: PathRoleEvidenceReadonly, invariant: InvariantEvidenceReadonly, decision: PolicyObserve},
		{role: PathRoleProtected, invariant: InvariantProtectedPath, decision: PolicyDeny},
		{role: PathRoleForbidden, invariant: InvariantForbiddenPath, decision: PolicyDeny},
		{role: PathRoleExternalEffect, invariant: InvariantExternalEffectPath, decision: PolicyAsk},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			invariant, ok := PathInvariantForRole(tt.role)
			if !ok {
				t.Fatalf("missing invariant for role %q", tt.role)
			}
			if invariant.InvariantID != tt.invariant || invariant.Decision != tt.decision {
				t.Fatalf("unexpected invariant: %#v", invariant)
			}
			if invariant.Precedence <= 0 || invariant.ReasonCode == "" {
				t.Fatalf("invariant should carry precedence and reason code: %#v", invariant)
			}
		})
	}

	forbidden, _ := PathInvariantForRole(PathRoleForbidden)
	protected, _ := PathInvariantForRole(PathRoleProtected)
	sideEffect, _ := PathInvariantForRole(PathRoleExternalEffect)
	required, _ := PathInvariantForRole(PathRoleRequiredEdit)
	allowed, _ := PathInvariantForRole(PathRoleAllowedEdit)
	evidence, _ := PathInvariantForRole(PathRoleEvidenceReadonly)
	if !(forbidden.Precedence > protected.Precedence &&
		protected.Precedence > sideEffect.Precedence &&
		sideEffect.Precedence > required.Precedence &&
		required.Precedence > allowed.Precedence &&
		allowed.Precedence > evidence.Precedence) {
		t.Fatalf("unexpected precedence order: forbidden=%d protected=%d side-effect=%d required=%d allowed=%d evidence=%d",
			forbidden.Precedence, protected.Precedence, sideEffect.Precedence, required.Precedence, allowed.Precedence, evidence.Precedence)
	}

	invariants := NamedPathInvariants()
	invariants[InvariantForbiddenPath] = PathInvariant{}
	invariant, ok := PathInvariantForRole(PathRoleForbidden)
	if !ok || invariant.InvariantID != InvariantForbiddenPath {
		t.Fatalf("NamedPathInvariants returned aliased mutable state: %#v", invariant)
	}
}

func TestResolvePlanScopePathRoleObligationsSeparatesRequiredAndAllowedCandidates(t *testing.T) {
	coverage, err := ResolvePlanScopePathRoleObligations(PlanScopePathRoleObligationInput{
		RequiredEditPaths:      []string{" ./pkg/required.go ", "pkg/required.go", "../escape.go"},
		AllowedCandidatePaths:  []string{"pkg/candidate.go", "/tmp/outside.go"},
		CandidateCoveragePaths: []string{"pkg/candidate.go"},
		Source:                 "test",
	})
	if err != nil {
		t.Fatalf("ResolvePlanScopePathRoleObligations returned error: %v", err)
	}

	if want := []string{"pkg/required.go"}; !slices.Equal(coverage.RequiredCoveragePaths, want) {
		t.Fatalf("required coverage paths = %#v, want %#v", coverage.RequiredCoveragePaths, want)
	}
	if want := []string{"pkg/candidate.go"}; !slices.Equal(coverage.AllowedCandidatePaths, want) {
		t.Fatalf("allowed candidate paths = %#v, want %#v", coverage.AllowedCandidatePaths, want)
	}
	if want := []string{"pkg/required.go"}; !slices.Equal(coverage.MissingRequiredCoveragePaths, want) {
		t.Fatalf("candidate path must not satisfy required coverage, missing=%#v want %#v", coverage.MissingRequiredCoveragePaths, want)
	}
	if want := []string{"pkg/candidate.go"}; !slices.Equal(coverage.CandidatePathsOutsideRequired, want) {
		t.Fatalf("candidate outside required = %#v, want %#v", coverage.CandidatePathsOutsideRequired, want)
	}
	if len(coverage.CandidateCoverage) != 1 {
		t.Fatalf("candidate coverage = %#v", coverage.CandidateCoverage)
	}
	candidate := coverage.CandidateCoverage[0]
	if candidate.SatisfiesRequiredCoverage || !candidate.WithinAllowedCandidatePath {
		t.Fatalf("allowed candidate must remain scope-only, got %#v", candidate)
	}
	if !slices.Contains(candidate.Roles, PathRoleAllowedEdit) || slices.Contains(candidate.Roles, PathRoleRequiredEdit) {
		t.Fatalf("candidate roles should be allowed-only, got %#v", candidate.Roles)
	}
}

func TestResolvePathRolesCoversRequiredEvidenceProtectedForbiddenAndSideEffect(t *testing.T) {
	root := t.TempDir()
	resolved, err := ResolvePathRoles(PathRoleResolverInput{
		WorkspaceRoot: root,
		PathRoles: []PathRole{
			{Role: PathRoleRequiredEdit, PathPattern: "internal/autonomy/path_roles.go", PacketID: "packet-required", Freshness: FreshnessFresh, Reason: "required implementation path"},
			{Role: PathRoleEvidenceReadonly, PathPattern: "docs/**", PacketID: "packet-docs", Freshness: FreshnessFresh, Reason: "decision evidence"},
			{Role: PathRoleProtected, PathPattern: ".env", Freshness: FreshnessUnknown, Reason: "secret-bearing path"},
			{Role: PathRoleForbidden, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata"},
			{Role: PathRoleExternalEffect, PathPattern: "deploy/prod/**", Freshness: FreshnessFresh, Reason: "external deployment surface"},
		},
		Evidence: []EvidenceRef{
			{EvidenceID: "ev-required", Handle: "context_pack://packet-required", Freshness: FreshnessFresh},
			{EvidenceID: "ev-docs", Path: "docs/decisioning-north-star.md", Freshness: FreshnessFresh},
		},
		Source: "p1.4-test",
	},
		"internal/autonomy/path_roles.go",
		"docs/decisioning-north-star.md",
		".env",
		".git/config",
		"deploy/prod/release.json",
	)
	if err != nil {
		t.Fatalf("ResolvePathRoles returned error: %v", err)
	}

	assertEffectivePathRole(t, resolved, "internal/autonomy/path_roles.go", PathRoleRequiredEdit, PolicyAllow, InvariantRequiredEditPath)
	assertEffectivePathRole(t, resolved, "docs/decisioning-north-star.md", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)
	assertEffectivePathRole(t, resolved, ".env", PathRoleProtected, PolicyDeny, InvariantProtectedPath)
	assertEffectivePathRole(t, resolved, ".git/config", PathRoleForbidden, PolicyDeny, InvariantForbiddenPath)
	assertEffectivePathRole(t, resolved, "deploy/prod/release.json", PathRoleExternalEffect, PolicyAsk, InvariantExternalEffectPath)

	required := findPathResolution(t, resolved, "internal/autonomy/path_roles.go")
	if !slices.Contains(required.EvidenceRefs, "ev-required") {
		t.Fatalf("required role should carry evidence ref, got %#v", required.EvidenceRefs)
	}
	if resolved.RoleCounts[string(PathRoleRequiredEdit)] != 1 ||
		resolved.RoleCounts[string(PathRoleEvidenceReadonly)] != 1 ||
		resolved.RoleCounts[string(PathRoleProtected)] != 1 ||
		resolved.RoleCounts[string(PathRoleForbidden)] != 1 ||
		resolved.RoleCounts[string(PathRoleExternalEffect)] != 1 {
		t.Fatalf("unexpected role counts: %#v", resolved.RoleCounts)
	}
}

func TestResolvePathRolesAppliesConflictPrecedenceAndDeduplicatesRoles(t *testing.T) {
	resolved, err := ResolvePathRoles(PathRoleResolverInput{
		PathRoles: []PathRole{
			{Role: PathRoleRequiredEdit, PathPattern: "internal/autonomy/path_roles.go", PacketID: "packet-required", Reason: "required exact"},
			{Role: PathRoleRequiredEdit, PathPattern: "internal/autonomy/**", PacketID: "packet-required-glob", Reason: "required glob"},
			{Role: PathRoleRequiredEdit, PathPattern: "internal/autonomy/**", PacketID: "packet-required-glob", Reason: "required glob"},
			{Role: PathRoleEvidenceReadonly, PathPattern: "internal/autonomy/**", Reason: "read-only evidence"},
			{Role: PathRoleProtected, PathPattern: "internal/**", Reason: "protected source tree"},
			{Role: PathRoleForbidden, PathPattern: "internal/autonomy/path_roles.go", Reason: "explicit forbidden override"},
		},
	}, "internal/autonomy/path_roles.go")
	if err != nil {
		t.Fatalf("ResolvePathRoles returned error: %v", err)
	}

	resolution := findPathResolution(t, resolved, "internal/autonomy/path_roles.go")
	if resolution.EffectiveRole != PathRoleForbidden || resolution.EffectiveInvariantID != InvariantForbiddenPath {
		t.Fatalf("forbidden role should win conflict, got %#v", resolution)
	}
	if got := countResolvedRole(resolution.Roles, PathRoleRequiredEdit); got != 1 {
		t.Fatalf("required role should be deduplicated, got %d role entries in %#v", got, resolution.Roles)
	}
	required := resolvedRole(t, resolution.Roles, PathRoleRequiredEdit)
	if len(required.MatchedPatterns) != 2 {
		t.Fatalf("required role should aggregate exact and glob matches once, got %#v", required.MatchedPatterns)
	}
	if len(resolution.Conflicts) != 3 {
		t.Fatalf("expected conflicts for protected, required, and evidence roles, got %#v", resolution.Conflicts)
	}
}

func TestResolvePathRolesNormalizesRelativeAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "outside", "secrets.env")
	resolved, err := ResolvePathRoles(PathRoleResolverInput{
		WorkspaceRoot: root,
		PathRoles: []PathRole{
			{Role: PathRoleRequiredEdit, PathPattern: "./internal/autonomy/path_roles.go", Reason: "relative required path"},
			{Role: PathRoleEvidenceReadonly, PathPattern: filepath.Join(root, "docs", "**"), Reason: "absolute evidence glob inside workspace"},
			{Role: PathRoleProtected, PathPattern: outside, Reason: "absolute protected path outside workspace"},
		},
	},
		filepath.Join(root, "internal", "autonomy", "..", "autonomy", "path_roles.go"),
		"./docs/decisioning-north-star.md",
		outside,
	)
	if err != nil {
		t.Fatalf("ResolvePathRoles returned error: %v", err)
	}

	required := findPathResolution(t, resolved, "internal/autonomy/path_roles.go")
	if required.RelativePath != "internal/autonomy/path_roles.go" || required.OutsideWorkspace {
		t.Fatalf("required path not canonicalized as workspace-relative: %#v", required)
	}
	if required.AbsolutePath != filepath.ToSlash(filepath.Join(root, "internal", "autonomy", "path_roles.go")) {
		t.Fatalf("absolute path mismatch: got %q", required.AbsolutePath)
	}
	assertEffectivePathRole(t, resolved, "docs/decisioning-north-star.md", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)

	protected := findPathResolution(t, resolved, filepath.ToSlash(filepath.Clean(outside)))
	if !protected.OutsideWorkspace || protected.EffectiveRole != PathRoleProtected {
		t.Fatalf("outside protected path should retain absolute identity and protected role: %#v", protected)
	}
}

func TestResolveContractPathRolesSupportsReadOnlySidecarExample(t *testing.T) {
	contract, err := NewContract(ContractInput{
		Scope: ContractScope{
			InvocationKind:    InvocationSidecarCall,
			TaskFamily:        "sidecar_discovery",
			RuntimeContract:   "grounded_evidence_summary",
			PermissionProfile: "read_only_workspace",
			Sidecar: &SidecarContext{
				Transport: "cli",
				Caller:    "codex",
				Profile:   "discovery",
				RequestID: "req-p14",
				Tool:      "context_pack",
			},
		},
		PathRoles: []PathRole{
			{Role: PathRoleEvidenceReadonly, PathPattern: "internal/autonomy/**", PacketID: "packet-autonomy", Freshness: FreshnessFresh, Reason: "sidecar source evidence"},
			{Role: PathRoleProtected, PathPattern: ".env", Freshness: FreshnessUnknown, Reason: "secret-bearing path"},
			{Role: PathRoleForbidden, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata hidden from sidecar"},
			{Role: PathRoleExternalEffect, PathPattern: "publish/**", Freshness: FreshnessFresh, Reason: "publishing is an external side effect"},
		},
		Capabilities: CapabilityPolicy{
			Allowed:          []Capability{CapabilitySemanticDiscovery, CapabilityBoundedRead},
			Forbidden:        []Capability{CapabilityApplyPatch, CapabilityExternalWrite},
			AllowedEffects:   []SideEffectClass{SideEffectReadOnly},
			ForbiddenEffects: []SideEffectClass{SideEffectAdditive, SideEffectDestructive, SideEffectOpenWorld},
		},
		Evidence: []EvidenceRef{
			{EvidenceID: "ev-autonomy-packet", Handle: "context_pack://packet-autonomy", Freshness: FreshnessFresh},
		},
		Metadata: ContractMetadata{
			ClientClassifications: []ClientClassification{ClientToolingVendor, ClientLocalModel, ClientOpenSource},
			ProductSignals:        []ProductSignal{ProductIntegrationProof, ProductSecurityPrivacy, ProductLocalEvidenceExport},
			Tags:                  []string{"p1.4", "read-only-sidecar"},
		},
	})
	if err != nil {
		t.Fatalf("NewContract returned error: %v", err)
	}

	resolved, err := ResolveContractPathRoles(contract, "", "internal/autonomy/contract.go", ".git/config", "publish/release.json")
	if err != nil {
		t.Fatalf("ResolveContractPathRoles returned error: %v", err)
	}
	if !strings.Contains(resolved.Source, contract.ContractID) {
		t.Fatalf("expected source to reference contract id, got %q", resolved.Source)
	}
	assertEffectivePathRole(t, resolved, "internal/autonomy/contract.go", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)
	assertEffectivePathRole(t, resolved, ".git/config", PathRoleForbidden, PolicyDeny, InvariantForbiddenPath)
	assertEffectivePathRole(t, resolved, "publish/release.json", PathRoleExternalEffect, PolicyAsk, InvariantExternalEffectPath)
	if len(resolved.PolicyPathMatches()) != 3 {
		t.Fatalf("expected policy-ready path matches, got %#v", resolved.PolicyPathMatches())
	}

	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("marshal resolver output: %v", err)
	}
	for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("passive resolver output should not prescribe strategy field %q: %s", forbidden, encoded)
		}
	}
}

func TestResolverCarriesClientClassificationAndProductMetadataWithoutBranches(t *testing.T) {
	var roles []PathRole
	var paths []string
	for _, classification := range allClientClassifications() {
		path := "clients/" + string(classification) + "/evidence.json"
		paths = append(paths, path)
		roles = append(roles, PathRole{
			Role:        PathRoleEvidenceReadonly,
			PathPattern: path,
			Reason:      "client classification fixture",
		})
	}
	for _, signal := range allProductSignals() {
		path := "signals/" + string(signal) + "/proof.json"
		paths = append(paths, path)
		roles = append(roles, PathRole{
			Role:        PathRoleEvidenceReadonly,
			PathPattern: path,
			Reason:      "product success fixture",
		})
	}

	resolved, err := ResolvePathRoles(PathRoleResolverInput{
		PathRoles: roles,
		Metadata: ContractMetadata{
			ClientClassifications: allClientClassifications(),
			ProductSignals:        allProductSignals(),
			Tags:                  []string{"p1.4", "metadata-path-role-fixture"},
		},
	}, paths...)
	if err != nil {
		t.Fatalf("ResolvePathRoles returned error: %v", err)
	}

	if len(resolved.Resolutions) != len(paths) {
		t.Fatalf("expected one resolution per metadata fixture path, got %d want %d", len(resolved.Resolutions), len(paths))
	}
	for _, resolution := range resolved.Resolutions {
		if resolution.EffectiveRole != PathRoleEvidenceReadonly || resolution.Decision != PolicyObserve {
			t.Fatalf("metadata fixture path should resolve generically as evidence, got %#v", resolution)
		}
	}
	for _, want := range allClientClassifications() {
		if !containsClientClassification(resolved.Metadata.ClientClassifications, want) {
			t.Fatalf("missing client classification %q in %#v", want, resolved.Metadata.ClientClassifications)
		}
	}
	for _, want := range allProductSignals() {
		if !containsProductSignal(resolved.Metadata.ProductSignals, want) {
			t.Fatalf("missing product signal %q in %#v", want, resolved.Metadata.ProductSignals)
		}
	}
}

func assertEffectivePathRole(t *testing.T, set PathRoleResolutionSet, path string, role PathRoleKind, decision ToolPolicyDecision, invariant PathInvariantID) {
	t.Helper()
	resolution := findPathResolution(t, set, path)
	if resolution.EffectiveRole != role || resolution.Decision != decision || resolution.EffectiveInvariantID != invariant {
		t.Fatalf("resolution for %q = role %q decision %q invariant %q, want role %q decision %q invariant %q: %#v",
			path, resolution.EffectiveRole, resolution.Decision, resolution.EffectiveInvariantID, role, decision, invariant, resolution)
	}
}

func findPathResolution(t *testing.T, set PathRoleResolutionSet, path string) PathRoleResolution {
	t.Helper()
	normalized := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))), "./")
	for _, resolution := range set.Resolutions {
		if resolution.Path == normalized || resolution.RelativePath == normalized || resolution.AbsolutePath == normalized {
			return resolution
		}
	}
	t.Fatalf("missing resolution for %q in %#v", normalized, set.Resolutions)
	return PathRoleResolution{}
}

func countResolvedRole(roles []ResolvedPathRole, role PathRoleKind) int {
	count := 0
	for _, resolved := range roles {
		if resolved.Role == role {
			count++
		}
	}
	return count
}

func resolvedRole(t *testing.T, roles []ResolvedPathRole, role PathRoleKind) ResolvedPathRole {
	t.Helper()
	for _, resolved := range roles {
		if resolved.Role == role {
			return resolved
		}
	}
	t.Fatalf("missing role %q in %#v", role, roles)
	return ResolvedPathRole{}
}
