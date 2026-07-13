package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Scope expansion contracts", func() {
	It("normalizes path, role, and metadata", func() {
		path := ScopeExpansionPath{
			Path:   ` ./internal\contracts/plan/scope.go `,
			Role:   " MUTATION_TARGET ",
			Reason: " because ",
			Source: " prompt ",
		}.Normalized()
		Expect(path.Path).To(Equal("internal/contracts/plan/scope.go"))
		Expect(path.Role).To(Equal(ScopePathRoleMutationTarget))
		Expect(path.Reason).To(Equal("because"))
		Expect(path.Source).To(Equal("prompt"))
	})

	It("drops invalid paths and deduplicates by path and role", func() {
		paths := NormalizeScopeExpansionPaths([]ScopeExpansionPath{
			{Path: "pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "first"},
			{Path: "./pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "duplicate"},
			{Path: "pkg/a.go", Role: ScopePathRoleReadOnlyEvidence},
			{Path: "../escape.go", Role: ScopePathRoleReferenceDocument},
			{Path: "pkg/b.go", Role: "unknown"},
		})
		Expect(paths).To(Equal([]ScopeExpansionPath{
			{Path: "pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "first"},
			{Path: "pkg/a.go", Role: ScopePathRoleReadOnlyEvidence},
		}))
	})

	It("normalizes event policy, paths, and metadata", func() {
		event := ScopeExpansionEvent{
			Kind:       " PROPOSAL ",
			ProposalID: " proposal-1 ",
			Policy:     ScopeExpansionPolicyRequiresUserApproval,
			Paths: []ScopeExpansionPath{
				{Path: "docs/ref.md", Role: ScopePathRoleReferenceDocument},
			},
			Reason: " needs source ",
			Source: " planner ",
		}.Normalized()
		Expect(event.Kind).To(Equal(ScopeExpansionEventProposalCreated))
		Expect(event.ProposalID).To(Equal("proposal-1"))
		Expect(event.Policy).To(Equal(ScopeExpansionPolicyRequiresUserApproval))
		Expect(event.Paths).To(Equal([]ScopeExpansionPath{{Path: "docs/ref.md", Role: ScopePathRoleReferenceDocument}}))
		Expect(event.Reason).To(Equal("needs source"))
		Expect(event.Source).To(Equal("planner"))
	})

	DescribeTable("normalizes event-kind aliases",
		func(input string, expected ScopeExpansionEventKind) {
			Expect(NormalizeScopeExpansionEventKind(ScopeExpansionEventKind(input))).To(Equal(expected))
		},
		Entry("proposal constant", " PROPOSAL_CREATED ", ScopeExpansionEventProposalCreated),
		Entry("proposal alias", "proposal", ScopeExpansionEventProposalCreated),
		Entry("approved constant", "proposal_approved", ScopeExpansionEventProposalApproved),
		Entry("approval alias", "approval", ScopeExpansionEventProposalApproved),
		Entry("rejected constant", "proposal_rejected", ScopeExpansionEventProposalRejected),
		Entry("rejection alias", "rejection", ScopeExpansionEventProposalRejected),
		Entry("blocked constant", "scope_expansion_blocked", ScopeExpansionEventExpansionBlocked),
		Entry("block alias", "block", ScopeExpansionEventExpansionBlocked),
		Entry("unknown", "unknown", ScopeExpansionEventKind("")),
	)
})
