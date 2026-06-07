package plancontract

import "testing"

func TestNewFileAuthorizedForPathIsPathAware(t *testing.T) {
	if !NewFileAuthorizedForPath("Create the new helper with the following content.", "pkg/helper.go") {
		t.Fatal("explicit create/full-body language should authorize a new implementation file")
	}
	if !NewFileAuthorizedForPath("Add regression tests for invalid routes.", "pkg/route_test.go") {
		t.Fatal("add-test language should authorize a missing test-like path")
	}
	if NewFileAuthorizedForPath("Add score breakdown to the existing tooltip.", "project/scripts/economy/score_breakdown.gd") {
		t.Fatal("generic add language must not authorize an invented implementation path")
	}
}
