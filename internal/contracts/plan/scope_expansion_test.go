package plan

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

func TestScopeExpansionEventNormalizesPolicyPathsAndMetadata(t *testing.T) {
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

	if event.Kind != ScopeExpansionEventProposalCreated {
		t.Fatalf("event kind = %q, want %q", event.Kind, ScopeExpansionEventProposalCreated)
	}
	if event.ProposalID != "proposal-1" || event.Policy != ScopeExpansionPolicyRequiresUserApproval {
		t.Fatalf("event identifiers not normalized: %#v", event)
	}
	if want := []ScopeExpansionPath{{Path: "docs/ref.md", Role: ScopePathRoleReferenceDocument}}; !slices.Equal(event.Paths, want) {
		t.Fatalf("event paths = %#v, want %#v", event.Paths, want)
	}
	if event.Reason != "needs source" || event.Source != "planner" {
		t.Fatalf("event metadata not trimmed: %#v", event)
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
