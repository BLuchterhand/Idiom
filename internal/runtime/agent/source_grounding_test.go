package agent

import "testing"

func TestSourceGroundingEscapingMentionedPaths(t *testing.T) {
	answer := "The bug is in `/tmp/outside/labels.go:6`, and a related path is ../other/config.yaml."

	paths := sourceGroundingEscapingMentionedPaths(answer)
	if len(paths) != 2 {
		t.Fatalf("escaping paths = %#v, want two", paths)
	}
	if paths[0] != "../other/config.yaml" || paths[1] != "/tmp/outside/labels.go" {
		t.Fatalf("escaping paths = %#v", paths)
	}
}

func TestSourceGroundingEscapingUnsupportedPathsAllowUnverifiedQualification(t *testing.T) {
	answer := "The outside path `/tmp/outside/labels.go:6` is unverified and outside the workspace."

	if paths := sourceGroundingEscapingMentionedPaths(answer); len(paths) != 1 || paths[0] != "/tmp/outside/labels.go" {
		t.Fatalf("escaping path detection changed: %#v", paths)
	}

	if unsupported := sourceGroundingUnsupportedEscapingMentionedPaths(answer, nil); len(unsupported) != 0 {
		t.Fatalf("unverified outside-workspace citation should not block source grounding: %#v", unsupported)
	}
}

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
