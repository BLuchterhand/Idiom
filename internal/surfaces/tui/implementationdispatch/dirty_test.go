package implementationdispatch

import "testing"

func TestPlanDocumentsWithDirtyAllowanceMarksOnlyNamedScopedDocument(t *testing.T) {
	docs := []PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := PlanDocumentsWithDirtyAllowance(
		"implement atom and explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go for this run",
		docs,
	)

	if !got[0].AllowDirtyAtomScope {
		t.Fatalf("expected named dirty scoped path to allow matching document: %#v", got)
	}

	if got[1].AllowDirtyAtomScope {
		t.Fatalf("dirty scoped path allowance must not apply to unrelated document: %#v", got)
	}
}

func TestPlanDocumentsWithDirtyAllowanceRejectsQuotedOrNegatedAuthority(t *testing.T) {
	docs := []PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
	}
	for _, prompt := range []string{
		"atom intake said next: finish, commit, or explicitly allow the dirty scoped paths before dispatch",
		"do not explicitly allow dirty scoped path internal/contracts/writerroute/decision_test.go",
		"atom intake blocked; next: allow dirty path internal/contracts/writerroute/decision_test.go",
		"rerun without --allow-dirty",
		"avoid --allow-dirty-scope unless the user says so",
	} {
		got := PlanDocumentsWithDirtyAllowance(prompt, docs)
		if got[0].AllowDirtyAtomScope {
			t.Fatalf("non-authoritative dirty allowance %q must not allow document: %#v", prompt, got)
		}
	}
}

func TestPlanDocumentsWithDirtyAllowanceBroadFlagAllowsAllScopedDocuments(t *testing.T) {
	docs := []PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
		{Path: "atom-b.md", EditPaths: []string{"internal/contracts/writerroute/decision.go"}},
	}
	got := PlanDocumentsWithDirtyAllowance("rerun with --allow-dirty for these atom scopes", docs)

	if !got[0].AllowDirtyAtomScope || !got[1].AllowDirtyAtomScope {
		t.Fatalf("--allow-dirty should explicitly allow all atom documents in the turn: %#v", got)
	}
}

func TestPlanDocumentsWithDirtyAllowanceRejectsEscapedPath(t *testing.T) {
	docs := []PlanDocument{
		{Path: "atom-a.md", EditPaths: []string{"internal/contracts/writerroute/decision_test.go"}},
	}
	got := PlanDocumentsWithDirtyAllowance(
		"explicitly allow dirty scoped path ../internal/contracts/writerroute/decision_test.go",
		docs,
	)

	if got[0].AllowDirtyAtomScope {
		t.Fatalf("escaped dirty path allowance must not authorize document scope: %#v", got)
	}
}
