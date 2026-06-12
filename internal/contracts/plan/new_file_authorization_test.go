package plancontract

import "testing"

func TestNewFileAuthorizedForPathIsPathAware(t *testing.T) {
	if !NewFileAuthorizedForPath("Create the new helper with the following content.", "pkg/helper.go") {
		t.Fatal("explicit create/full-body language should authorize a new implementation file")
	}

	if !NewFileAuthorizedForPath("I explicitly authorize creating these NEW repo-relative files.", "pkg/helper.go") {
		t.Fatal("operator authorization to create new repo-relative files should authorize missing paths")
	}

	if !NewFileAuthorizedForPath("Plan and deliver a tiny source package plus test.", "internal/greeting/greeting.go") {
		t.Fatal("source-package language should authorize a missing implementation path")
	}

	if !NewFileAuthorizedForPath("Add an internal/greeting package with Hello function.", "internal/greeting/greeting.go") {
		t.Fatal("add-package language should authorize a missing implementation path")
	}

	if !NewFileAuthorizedForPath("Add regression tests for invalid routes.", "pkg/route_test.go") {
		t.Fatal("add-test language should authorize a missing test-like path")
	}

	if NewFileAuthorizedForPath("Add score breakdown to the existing tooltip.", "project/scripts/economy/score_breakdown.gd") {
		t.Fatal("generic add language must not authorize an invented implementation path")
	}
}
