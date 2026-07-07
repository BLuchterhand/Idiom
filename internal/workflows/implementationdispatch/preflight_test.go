package implementationdispatch

import (
	"context"
	"reflect"
	"testing"
)

func TestPreflightPlanDocumentsSkipsEmptyAndReferenceOnlyDocuments(t *testing.T) {
	var visited []string
	docs := []PlanDocument{
		{Path: ""},
		{Path: "docs/reference.md", ReferenceOnly: true},
		{Path: "docs/dispatch.md"},
	}

	blocked := PreflightPlanDocuments(context.Background(), docs, func(_ context.Context, doc PlanDocument) PlanDocumentPreflightResult {
		visited = append(visited, doc.Path)
		return PlanDocumentPreflightResult{Path: doc.Path, OK: true}
	})

	if len(blocked) != 0 {
		t.Fatalf("dispatchable documents should not block: %#v", blocked)
	}

	if !reflect.DeepEqual(visited, []string{"docs/dispatch.md"}) {
		t.Fatalf("visited documents = %#v", visited)
	}
}

func TestPreflightPlanDocumentsReportsBlockedResultsAndBackfillsPath(t *testing.T) {
	docs := []PlanDocument{{Path: "docs/dispatch.md"}}

	blocked := PreflightPlanDocuments(context.Background(), docs, func(_ context.Context, doc PlanDocument) PlanDocumentPreflightResult {
		return PlanDocumentPreflightResult{
			OK:       true,
			Blockers: []string{"brief lint: missing exact anchor"},
		}
	})

	if len(blocked) != 1 {
		t.Fatalf("blocked results = %#v, want one blocker", blocked)
	}

	if blocked[0].Path != "docs/dispatch.md" {
		t.Fatalf("blocked path = %q", blocked[0].Path)
	}

	if !reflect.DeepEqual(blocked[0].Blockers, []string{"brief lint: missing exact anchor"}) {
		t.Fatalf("blockers = %#v", blocked[0].Blockers)
	}
}
