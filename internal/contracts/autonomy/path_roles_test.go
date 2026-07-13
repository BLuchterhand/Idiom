package autonomy

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Path-role resolution", func() {
	Describe("named path invariants", func() {
		tests := []struct {
			role      PathRoleKind
			invariant PathInvariantID
			decision  ToolPolicyDecision
		}{
			{role: PathRoleRequiredEdit, invariant: InvariantRequiredEditPath, decision: PolicyAllow},
			{role: PathRoleAllowedEdit, invariant: InvariantAllowedCandidate, decision: PolicyAllow},
			{role: PathRoleEvidenceReadonly, invariant: InvariantEvidenceReadonly, decision: PolicyObserve},
			{role: PathRoleGeneratedArtifact, invariant: InvariantGeneratedArtifact, decision: PolicyObserve},
			{role: PathRoleProtected, invariant: InvariantProtectedPath, decision: PolicyDeny},
			{role: PathRoleForbidden, invariant: InvariantForbiddenPath, decision: PolicyDeny},
			{role: PathRoleVerificationOut, invariant: InvariantVerificationOutput, decision: PolicyObserve},
			{role: PathRoleExternalEffect, invariant: InvariantExternalEffectPath, decision: PolicyAsk},
		}

		for _, tt := range tests {
			tt := tt
			It("covers "+string(tt.role), func() {
				invariant, ok := PathInvariantForRole(tt.role)
				Expect(ok).To(BeTrue())
				Expect(invariant.InvariantID).To(Equal(tt.invariant))
				Expect(invariant.Decision).To(Equal(tt.decision))
				Expect(invariant.Precedence).To(BeNumerically(">", 0))
				Expect(invariant.ReasonCode).NotTo(BeEmpty())

				if tt.role == PathRoleVerificationOut {
					Expect(invariant.Obligation).To(BeEmpty())
				}

				if tt.role == PathRoleExternalEffect {
					Expect(invariant.SideEffect).To(Equal(SideEffectOpenWorld))
				}
			})
		}

		It("orders restrictive roles ahead of permissive and passive roles", func() {
			forbidden, _ := PathInvariantForRole(PathRoleForbidden)
			protected, _ := PathInvariantForRole(PathRoleProtected)
			sideEffect, _ := PathInvariantForRole(PathRoleExternalEffect)
			required, _ := PathInvariantForRole(PathRoleRequiredEdit)
			allowed, _ := PathInvariantForRole(PathRoleAllowedEdit)
			generated, _ := PathInvariantForRole(PathRoleGeneratedArtifact)
			evidence, _ := PathInvariantForRole(PathRoleEvidenceReadonly)
			verification, _ := PathInvariantForRole(PathRoleVerificationOut)

			Expect(forbidden.Precedence).To(BeNumerically(">", protected.Precedence))
			Expect(protected.Precedence).To(BeNumerically(">", sideEffect.Precedence))
			Expect(sideEffect.Precedence).To(BeNumerically(">", required.Precedence))
			Expect(required.Precedence).To(BeNumerically(">", allowed.Precedence))
			Expect(allowed.Precedence).To(BeNumerically(">", generated.Precedence))
			Expect(generated.Precedence).To(BeNumerically(">", evidence.Precedence))
			Expect(evidence.Precedence).To(BeNumerically(">", verification.Precedence))

			invariants := NamedPathInvariants()
			invariants[InvariantForbiddenPath] = PathInvariant{}
			invariant, ok := PathInvariantForRole(PathRoleForbidden)
			Expect(ok).To(BeTrue())
			Expect(invariant.InvariantID).To(Equal(InvariantForbiddenPath))
		})
	})

	It("separates required coverage from allowed candidate scope", func() {
		coverage, err := ResolvePlanScopePathRoleObligations(PlanScopePathRoleObligationInput{
			RequiredEditPaths:      []string{" ./pkg/required.go ", "pkg/required.go", "../escape.go"},
			AllowedCandidatePaths:  []string{"pkg/candidate.go", "/tmp/outside.go"},
			CandidateCoveragePaths: []string{"pkg/candidate.go"},
			Source:                 "test",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(coverage.RequiredCoveragePaths).To(Equal([]string{"pkg/required.go"}))
		Expect(coverage.AllowedCandidatePaths).To(Equal([]string{"pkg/candidate.go"}))
		Expect(coverage.MissingRequiredCoveragePaths).To(Equal([]string{"pkg/required.go"}))
		Expect(coverage.CandidatePathsOutsideRequired).To(Equal([]string{"pkg/candidate.go"}))
		Expect(coverage.CandidateCoverage).To(HaveLen(1))

		candidate := coverage.CandidateCoverage[0]
		Expect(candidate.SatisfiesRequiredCoverage).To(BeFalse())
		Expect(candidate.WithinAllowedCandidatePath).To(BeTrue())
		Expect(candidate.Roles).To(ContainElement(PathRoleAllowedEdit))
		Expect(candidate.Roles).NotTo(ContainElement(PathRoleRequiredEdit))
	})

	It("covers every supported first-class path role", func() {
		root := GinkgoT().TempDir()

		resolved, err := ResolvePathRoles(PathRoleResolverInput{
			WorkspaceRoot: root,
			PathRoles: []PathRole{
				{Role: PathRoleRequiredEdit, PathPattern: "internal/contracts/autonomy/path_roles.go", PacketID: "packet-required", Freshness: FreshnessFresh, Reason: "required implementation path"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "docs/**", PacketID: "packet-docs", Freshness: FreshnessFresh, Reason: "decision evidence"},
				{Role: PathRoleGeneratedArtifact, PathPattern: "dist/**", PacketID: "packet-generated", Freshness: FreshnessFresh, Reason: "generated output"},
				{Role: PathRoleProtected, PathPattern: ".env", Freshness: FreshnessUnknown, Reason: "secret-bearing path"},
				{Role: PathRoleProtected, PathPattern: ".idiom/**", Freshness: FreshnessUnknown, Reason: "harness artifact boundary"},
				{Role: PathRoleForbidden, PathPattern: ".git/**", Freshness: FreshnessUnknown, Reason: "repository metadata"},
				{Role: PathRoleVerificationOut, PathPattern: "logs/verification/**", PacketID: "packet-verification", Freshness: FreshnessFresh, Reason: "verification trace"},
				{Role: PathRoleExternalEffect, PathPattern: "deploy/prod/**", Freshness: FreshnessFresh, Reason: "external deployment surface"},
			},
			Evidence: []EvidenceRef{
				{EvidenceID: "ev-required", Handle: "context_pack://packet-required", Freshness: FreshnessFresh},
				{EvidenceID: "ev-docs", Path: "docs/decisioning-north-star.md", Freshness: FreshnessFresh},
				{EvidenceID: "ev-generated", Handle: "context_pack://packet-generated", Freshness: FreshnessFresh},
				{EvidenceID: "ev-verification", Handle: "context_pack://packet-verification", Freshness: FreshnessFresh},
			},
			Source: "p1.4-test",
		},
			"internal/contracts/autonomy/path_roles.go",
			"docs/decisioning-north-star.md",
			"dist/protocol-card.json",
			".env",
			".idiom/logs/conversations/session.jsonl",
			".git/config",
			"logs/verification/go-test.json",
			"deploy/prod/release.json",
		)
		Expect(err).NotTo(HaveOccurred())
		assertEffectivePathRole(resolved, "internal/contracts/autonomy/path_roles.go", PathRoleRequiredEdit, PolicyAllow, InvariantRequiredEditPath)
		assertEffectivePathRole(resolved, "docs/decisioning-north-star.md", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)
		assertEffectivePathRole(resolved, "dist/protocol-card.json", PathRoleGeneratedArtifact, PolicyObserve, InvariantGeneratedArtifact)
		assertEffectivePathRole(resolved, ".env", PathRoleProtected, PolicyDeny, InvariantProtectedPath)
		assertEffectivePathRole(resolved, ".idiom/logs/conversations/session.jsonl", PathRoleProtected, PolicyDeny, InvariantProtectedPath)
		assertEffectivePathRole(resolved, ".git/config", PathRoleForbidden, PolicyDeny, InvariantForbiddenPath)
		assertEffectivePathRole(resolved, "logs/verification/go-test.json", PathRoleVerificationOut, PolicyObserve, InvariantVerificationOutput)
		assertEffectivePathRole(resolved, "deploy/prod/release.json", PathRoleExternalEffect, PolicyAsk, InvariantExternalEffectPath)

		required := findPathResolution(resolved, "internal/contracts/autonomy/path_roles.go")
		Expect(required.EvidenceRefs).To(ContainElement("ev-required"))
		verification := findPathResolution(resolved, "logs/verification/go-test.json")
		Expect(verification.EvidenceRefs).To(ContainElement("ev-verification"))
		Expect(resolvedRole(verification.Roles, PathRoleVerificationOut).Obligation).To(BeEmpty())
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleRequiredEdit), 1))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleEvidenceReadonly), 1))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleGeneratedArtifact), 1))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleProtected), 2))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleForbidden), 1))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleVerificationOut), 1))
		Expect(resolved.RoleCounts).To(HaveKeyWithValue(string(PathRoleExternalEffect), 1))
	})

	It("keeps generated and verification output passive under edit scope", func() {
		resolved, err := ResolvePathRoles(PathRoleResolverInput{
			PathRoles: []PathRole{
				{Role: PathRoleAllowedEdit, PathPattern: "out/report.json", Reason: "allowed source contract"},
				{Role: PathRoleGeneratedArtifact, PathPattern: "out/**", Reason: "generated output"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "out/**", Reason: "supporting evidence"},
				{Role: PathRoleVerificationOut, PathPattern: "out/**", Reason: "verification trace"},
			},
		}, "out/report.json")
		Expect(err).NotTo(HaveOccurred())

		resolution := findPathResolution(resolved, "out/report.json")
		Expect(resolution.EffectiveRole).To(Equal(PathRoleAllowedEdit))
		Expect(resolution.Decision).To(Equal(PolicyAllow))

		for _, role := range []PathRoleKind{PathRoleGeneratedArtifact, PathRoleEvidenceReadonly, PathRoleVerificationOut} {
			Expect(countResolvedRole(resolution.Roles, role)).To(Equal(1))
		}
		Expect(resolution.Conflicts).To(HaveLen(3))
	})

	It("applies conflict precedence and deduplicates roles", func() {
		resolved, err := ResolvePathRoles(PathRoleResolverInput{
			PathRoles: []PathRole{
				{Role: PathRoleRequiredEdit, PathPattern: "internal/contracts/autonomy/path_roles.go", PacketID: "packet-required", Reason: "required exact"},
				{Role: PathRoleRequiredEdit, PathPattern: "internal/contracts/autonomy/**", PacketID: "packet-required-glob", Reason: "required glob"},
				{Role: PathRoleRequiredEdit, PathPattern: "internal/contracts/autonomy/**", PacketID: "packet-required-glob", Reason: "required glob"},
				{Role: PathRoleEvidenceReadonly, PathPattern: "internal/contracts/autonomy/**", Reason: "read-only evidence"},
				{Role: PathRoleProtected, PathPattern: "internal/**", Reason: "protected source tree"},
				{Role: PathRoleForbidden, PathPattern: "internal/contracts/autonomy/path_roles.go", Reason: "explicit forbidden override"},
			},
		}, "internal/contracts/autonomy/path_roles.go")
		Expect(err).NotTo(HaveOccurred())

		resolution := findPathResolution(resolved, "internal/contracts/autonomy/path_roles.go")
		Expect(resolution.EffectiveRole).To(Equal(PathRoleForbidden))
		Expect(resolution.EffectiveInvariantID).To(Equal(InvariantForbiddenPath))
		Expect(countResolvedRole(resolution.Roles, PathRoleRequiredEdit)).To(Equal(1))

		required := resolvedRole(resolution.Roles, PathRoleRequiredEdit)
		Expect(required.MatchedPatterns).To(HaveLen(2))
		Expect(resolution.Conflicts).To(HaveLen(3))
	})

	It("normalizes relative and absolute paths", func() {
		root := GinkgoT().TempDir()
		outside := filepath.Join(root, "..", "outside", "secrets.env")

		resolved, err := ResolvePathRoles(PathRoleResolverInput{
			WorkspaceRoot: root,
			PathRoles: []PathRole{
				{Role: PathRoleRequiredEdit, PathPattern: "./internal/contracts/autonomy/path_roles.go", Reason: "relative required path"},
				{Role: PathRoleEvidenceReadonly, PathPattern: filepath.Join(root, "docs", "**"), Reason: "absolute evidence glob inside workspace"},
				{Role: PathRoleProtected, PathPattern: outside, Reason: "absolute protected path outside workspace"},
			},
		},
			filepath.Join(root, "internal", "contracts", "autonomy", "..", "autonomy", "path_roles.go"),
			"./docs/decisioning-north-star.md",
			outside,
		)
		Expect(err).NotTo(HaveOccurred())

		required := findPathResolution(resolved, "internal/contracts/autonomy/path_roles.go")
		Expect(required.RelativePath).To(Equal("internal/contracts/autonomy/path_roles.go"))
		Expect(required.OutsideWorkspace).To(BeFalse())
		Expect(required.AbsolutePath).To(Equal(filepath.ToSlash(filepath.Join(root, "internal", "contracts", "autonomy", "path_roles.go"))))
		assertEffectivePathRole(resolved, "docs/decisioning-north-star.md", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)

		protected := findPathResolution(resolved, filepath.ToSlash(filepath.Clean(outside)))
		Expect(protected.OutsideWorkspace).To(BeTrue())
		Expect(protected.EffectiveRole).To(Equal(PathRoleProtected))
	})

	It("resolves the read-only sidecar path-role example", func() {
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
				{Role: PathRoleEvidenceReadonly, PathPattern: "internal/contracts/autonomy/**", PacketID: "packet-autonomy", Freshness: FreshnessFresh, Reason: "sidecar source evidence"},
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
		Expect(err).NotTo(HaveOccurred())

		resolved, err := ResolveContractPathRoles(contract, "", "internal/contracts/autonomy/contract.go", ".git/config", "publish/release.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Source).To(ContainSubstring(contract.ContractID))
		assertEffectivePathRole(resolved, "internal/contracts/autonomy/contract.go", PathRoleEvidenceReadonly, PolicyObserve, InvariantEvidenceReadonly)
		assertEffectivePathRole(resolved, ".git/config", PathRoleForbidden, PolicyDeny, InvariantForbiddenPath)
		assertEffectivePathRole(resolved, "publish/release.json", PathRoleExternalEffect, PolicyAsk, InvariantExternalEffectPath)
		Expect(resolved.PolicyPathMatches()).To(HaveLen(3))

		encoded, err := json.Marshal(resolved)
		Expect(err).NotTo(HaveOccurred())

		for _, forbidden := range []string{"next_action", "safe_alternatives", "must_call_tool"} {
			Expect(string(encoded)).NotTo(ContainSubstring(forbidden))
		}
	})

	It("carries classification and product metadata without client-specific branches", func() {
		var (
			roles []PathRole
			paths []string
		)

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
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Resolutions).To(HaveLen(len(paths)))

		for _, resolution := range resolved.Resolutions {
			Expect(resolution.EffectiveRole).To(Equal(PathRoleEvidenceReadonly))
			Expect(resolution.Decision).To(Equal(PolicyObserve))
		}

		for _, want := range allClientClassifications() {
			Expect(resolved.Metadata.ClientClassifications).To(ContainElement(want))
		}

		for _, want := range allProductSignals() {
			Expect(resolved.Metadata.ProductSignals).To(ContainElement(want))
		}
	})
})

func assertEffectivePathRole(set PathRoleResolutionSet, path string, role PathRoleKind, decision ToolPolicyDecision, invariant PathInvariantID) {
	GinkgoHelper()
	resolution := findPathResolution(set, path)
	Expect(resolution.EffectiveRole).To(Equal(role), "path %q", path)
	Expect(resolution.Decision).To(Equal(decision), "path %q", path)
	Expect(resolution.EffectiveInvariantID).To(Equal(invariant), "path %q", path)
}

func findPathResolution(set PathRoleResolutionSet, path string) PathRoleResolution {
	GinkgoHelper()
	normalized := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))), "./")
	for _, resolution := range set.Resolutions {
		if resolution.Path == normalized || resolution.RelativePath == normalized || resolution.AbsolutePath == normalized {
			return resolution
		}
	}

	Fail(fmt.Sprintf("missing resolution for %q in %#v", normalized, set.Resolutions))
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

func resolvedRole(roles []ResolvedPathRole, role PathRoleKind) ResolvedPathRole {
	GinkgoHelper()
	for _, resolved := range roles {
		if resolved.Role == role {
			return resolved
		}
	}

	Fail(fmt.Sprintf("missing role %q in %#v", role, roles))
	return ResolvedPathRole{}
}
