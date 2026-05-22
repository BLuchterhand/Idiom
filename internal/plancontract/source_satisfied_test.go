package plancontract

import (
	"reflect"
	"testing"
)

func TestSourceSatisfiedAnswerCoversPathsRequiresExplicitDecision(t *testing.T) {
	answer := "Decision: source-satisfied\n- a.txt: already present.\n- b.txt: current-state evidence."
	if !SourceSatisfiedAnswerCoversPaths(answer, []string{"a.txt", "b.txt"}) {
		t.Fatalf("expected explicit source-satisfied answer to cover both paths")
	}
	if SourceSatisfiedAnswerCoversPaths("a.txt and b.txt already present", []string{"a.txt", "b.txt"}) {
		t.Fatalf("non-decision prose must not cover source-satisfied paths")
	}
	if SourceSatisfiedAnswerCoversPaths(answer, []string{"a.txt", "c.txt"}) {
		t.Fatalf("missing path mention must not cover required paths")
	}
}

func TestSourceSatisfiedEvidencePathsRequiresExplicitDecision(t *testing.T) {
	answer := "Decision: source-satisfied\n- ./b.txt: already present.\n- a.txt: current-state evidence.\n- b.txt: duplicate."
	got := SourceSatisfiedEvidencePaths(answer)
	want := []string{"a.txt", "b.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SourceSatisfiedEvidencePaths() = %#v, want %#v", got, want)
	}
	blocker := "<<QUESTION>>: use Decision: source-satisfied if pkg/parser.go is already implemented"
	if paths := SourceSatisfiedEvidencePaths(blocker); len(paths) != 0 {
		t.Fatalf("blocker remedy text must not produce evidence paths, got %#v", paths)
	}
}
