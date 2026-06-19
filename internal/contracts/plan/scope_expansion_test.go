package plancontract

import (
	"slices"
	"testing"
)

func TestScopeExpansionPathNormalizesPathRoleAndMetadata(t *testing.T) {
	path := ScopeExpansionPath{
		Path:   ` ./internal\contracts/plan/scope.go `,
		Role:   " MUTATION_TARGET ",
		Reason: " because ",
		Source: " prompt ",
	}.Normalized()

	if path.Path != "internal/contracts/plan/scope.go" {
		t.Fatalf("path = %q, want normalized repo-relative path", path.Path)
	}
	if path.Role != ScopePathRoleMutationTarget {
		t.Fatalf("role = %q, want %q", path.Role, ScopePathRoleMutationTarget)
	}
	if path.Reason != "because" || path.Source != "prompt" {
		t.Fatalf("metadata not trimmed: %#v", path)
	}
}

func TestScopeExpansionPathsDropInvalidEntriesAndDeduplicateByPathRole(t *testing.T) {
	paths := NormalizeScopeExpansionPaths([]ScopeExpansionPath{
		{Path: "pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "first"},
		{Path: "./pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "duplicate"},
		{Path: "pkg/a.go", Role: ScopePathRoleReadOnlyEvidence},
		{Path: "../escape.go", Role: ScopePathRoleReferenceDocument},
		{Path: "pkg/b.go", Role: "unknown"},
	})

	want := []ScopeExpansionPath{
		{Path: "pkg/a.go", Role: ScopePathRoleMutationTarget, Reason: "first"},
		{Path: "pkg/a.go", Role: ScopePathRoleReadOnlyEvidence},
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestScopeExpansionProposalNormalizesPolicyPathsAndEvents(t *testing.T) {
	proposal := ScopeExpansionProposal{
		ID:     " proposal-1 ",
		Policy: " BOUNDED_DISCOVERY ",
		Paths: []ScopeExpansionPath{
			{Path: "pkg/a.go", Role: ScopePathRoleCandidateExpansion},
			{Path: "/tmp/outside.go", Role: ScopePathRoleReadOnlyEvidence},
		},
		Reason: " inspect nearby implementation ",
		Source: " planner ",
		Events: []ScopeExpansionEvent{
			{
				Kind:       " PROPOSAL ",
				ProposalID: " proposal-1 ",
				Policy:     ScopeExpansionPolicyRequiresUserApproval,
				Paths: []ScopeExpansionPath{
					{Path: "docs/ref.md", Role: ScopePathRoleReferenceDocument},
				},
				Reason: " needs source ",
				Source: " planner ",
			},
			{Kind: "ignored"},
		},
	}.Normalized()

	if proposal.ID != "proposal-1" {
		t.Fatalf("id = %q, want trimmed id", proposal.ID)
	}
	if proposal.Policy != ScopeExpansionPolicyBoundedDiscovery {
		t.Fatalf("policy = %q, want %q", proposal.Policy, ScopeExpansionPolicyBoundedDiscovery)
	}
	if want := []ScopeExpansionPath{{Path: "pkg/a.go", Role: ScopePathRoleCandidateExpansion}}; !slices.Equal(proposal.Paths, want) {
		t.Fatalf("paths = %#v, want %#v", proposal.Paths, want)
	}
	if proposal.Reason != "inspect nearby implementation" || proposal.Source != "planner" {
		t.Fatalf("proposal metadata not trimmed: %#v", proposal)
	}
	if len(proposal.Events) != 1 {
		t.Fatalf("events = %#v, want one valid event", proposal.Events)
	}

	event := proposal.Events[0]
	if event.Kind != ScopeExpansionEventProposalCreated {
		t.Fatalf("event kind = %q, want %q", event.Kind, ScopeExpansionEventProposalCreated)
	}
	if event.ProposalID != "proposal-1" || event.Policy != ScopeExpansionPolicyRequiresUserApproval {
		t.Fatalf("event identifiers not normalized: %#v", event)
	}
	if want := []ScopeExpansionPath{{Path: "docs/ref.md", Role: ScopePathRoleReferenceDocument}}; !slices.Equal(event.Paths, want) {
		t.Fatalf("event paths = %#v, want %#v", event.Paths, want)
	}
}

func TestScopeExpansionEventKindsCoverApprovalRejectionAndBlock(t *testing.T) {
	cases := map[ScopeExpansionEventKind]ScopeExpansionEventKind{
		" PROPOSAL_CREATED ":      ScopeExpansionEventProposalCreated,
		"proposal":                ScopeExpansionEventProposalCreated,
		"proposal_approved":       ScopeExpansionEventProposalApproved,
		"approval":                ScopeExpansionEventProposalApproved,
		"proposal_rejected":       ScopeExpansionEventProposalRejected,
		"rejection":               ScopeExpansionEventProposalRejected,
		"scope_expansion_blocked": ScopeExpansionEventExpansionBlocked,
		"block":                   ScopeExpansionEventExpansionBlocked,
		"unknown":                 "",
	}

	for input, want := range cases {
		if got := NormalizeScopeExpansionEventKind(input); got != want {
			t.Fatalf("NormalizeScopeExpansionEventKind(%q) = %q, want %q", input, got, want)
		}
	}
}
