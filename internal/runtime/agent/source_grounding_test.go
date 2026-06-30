package agent

import "testing"

func TestSourceCitationLocalWindowReturnsEmptyWhenPathAbsent(t *testing.T) {
	answer := "The current source `internal/app/current.go` defines the active behavior."

	if got := sourceCitationLocalWindow(answer, "internal/app/missing.go", 80, 140); got != "" {
		t.Fatalf("absent citation local window = %q, want empty", got)
	}
}

func TestSourceCitationClaimsDoNotBorrowTextFromAnotherCitation(t *testing.T) {
	answer := "The current source `internal/app/current.go` defines the active behavior. Future work could add `internal/app/future.go`."

	if !sourceCitationClaimsCurrentSourcePath(answer, "internal/app/current.go") {
		t.Fatal("current-source claim next to cited path should be detected")
	}

	if sourceCitationClaimsCurrentSourcePath(answer, "internal/app/missing.go") {
		t.Fatal("absent path must not inherit current-source wording from another citation")
	}
}
