package productworkflow

import "testing"

func TestProductWorkflowStatusPredicates(t *testing.T) {
	if !ProductWorkflowStatusIsPass(string(ProductWorkflowStatusPass)) {
		t.Fatalf("ProductWorkflowStatusIsPass(%q) = false", ProductWorkflowStatusPass)
	}
	if ProductWorkflowStatusIsPass(string(ProductWorkflowStatusAccepted)) {
		t.Fatalf("accepted status must not satisfy pass-only predicate")
	}
	for _, status := range []ProductWorkflowStatus{ProductWorkflowStatusAccepted, ProductWorkflowStatusPass} {
		if !ProductWorkflowStatusIsAccepted(string(status)) {
			t.Fatalf("ProductWorkflowStatusIsAccepted(%q) = false", status)
		}
	}
	if ProductWorkflowStatusIsAccepted(string(ProductWorkflowStatusBlocked)) {
		t.Fatalf("blocked status must not satisfy accepted predicate")
	}
}
