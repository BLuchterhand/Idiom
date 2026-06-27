package plan

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

func TestSourceSatisfiedAnswerRejectsStaleAnchorIntent(t *testing.T) {
	answer := "Decision: source-satisfied\n- pkg/x.go: stale anchor; old_text no longer applies."
	if SourceSatisfiedAnswerIntent(answer) {
		t.Fatalf("stale-anchor prose must not satisfy source-satisfied intent")
	}
	if SourceSatisfiedAnswerCoversPaths(answer, []string{"pkg/x.go"}) {
		t.Fatalf("stale-anchor prose must not cover required paths")
	}
}

func TestSourceSatisfiedAnswerCoversPathsRequiresExactPathMentions(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{
			name:   "sibling prefix",
			answer: "Decision: source-satisfied\n- ba.txt: already present.",
		},
		{
			name:   "backup suffix",
			answer: "Decision: source-satisfied\n- a.txt.bak: already present.",
		},
		{
			name:   "old suffix in directory",
			answer: "Decision: source-satisfied\n- dir/a.txt.old: already present.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if SourceSatisfiedAnswerCoversPaths(tc.answer, []string{"a.txt"}) {
				t.Fatalf("nearby path mention must not cover required path: %q", tc.answer)
			}
		})
	}
}

func TestSourceSatisfiedMissingCoveragePathsUsesExactCoverage(t *testing.T) {
	answer := "Decision: source-satisfied\n- a.txt.bak: already present.\n- b.txt: already present.\n- ./Dockerfile: already present."
	got := SourceSatisfiedMissingCoveragePaths(answer, []string{"a.txt", "b.txt", "Dockerfile"})
	want := []string{"a.txt"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SourceSatisfiedMissingCoveragePaths() = %#v, want %#v", got, want)
	}
}

func TestSourceSatisfiedCoverageUsesScopePathNormalization(t *testing.T) {
	answer := "Decision: source-satisfied\n- pkg/feature.go: already present."
	if !SourceSatisfiedAnswerCoversPaths(answer, []string{`pkg\feature.go`}) {
		t.Fatalf("expected source-satisfied coverage to normalize backslash required paths")
	}

	if got := SourceSatisfiedMissingCoveragePaths(answer, []string{"../secret.go", "/tmp/abs.go", "pkg/./bad.go"}); len(got) != 0 {
		t.Fatalf("invalid required paths should be dropped by scope normalization, got %#v", got)
	}
}

func TestSourceSatisfiedMissingCoveragePathsRequiresDecisionIntent(t *testing.T) {
	got := SourceSatisfiedMissingCoveragePaths("a.txt and b.txt are already present", []string{"b.txt", "a.txt"})
	want := []string{"a.txt", "b.txt"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SourceSatisfiedMissingCoveragePaths() = %#v, want %#v", got, want)
	}
}

func TestSourceSatisfiedAnswerCoversExtensionlessRequiredPaths(t *testing.T) {
	cases := []struct {
		name          string
		answer        string
		requiredPaths []string
	}{
		{
			name:          "dockerfile",
			answer:        "Decision: source-satisfied\n- Dockerfile: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "build file",
			answer:        "Decision: source-satisfied\n- BUILD: already present.",
			requiredPaths: []string{"BUILD"},
		},
		{
			name:          "nested makefile",
			answer:        "Decision: source-satisfied\n- cmd/foo/Makefile: already present.",
			requiredPaths: []string{"cmd/foo/Makefile"},
		},
		{
			name:          "dot slash dockerfile",
			answer:        "Decision: source-satisfied\n- ./Dockerfile: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "backticked dockerfile",
			answer:        "Decision: source-satisfied\n- `Dockerfile`: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "bold dockerfile",
			answer:        "Decision: source-satisfied\n- **Dockerfile**: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !SourceSatisfiedAnswerCoversPaths(tc.answer, tc.requiredPaths) {
				t.Fatalf("expected exact extensionless path mention to cover required path: %q", tc.answer)
			}
		})
	}
}

func TestSourceSatisfiedAnswerCoversExtensionlessRequiredPathsRejectsNearMatches(t *testing.T) {
	cases := []struct {
		name          string
		answer        string
		requiredPaths []string
	}{
		{
			name:          "backup suffix",
			answer:        "Decision: source-satisfied\n- Dockerfile.bak: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "sibling prefix",
			answer:        "Decision: source-satisfied\n- OldDockerfile: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "nested path is not root file",
			answer:        "Decision: source-satisfied\n- deploy/Dockerfile: already present.",
			requiredPaths: []string{"Dockerfile"},
		},
		{
			name:          "root path is not nested file",
			answer:        "Decision: source-satisfied\n- Dockerfile: already present.",
			requiredPaths: []string{"deploy/Dockerfile"},
		},
		{
			name:          "prose mention after nested path is not root file",
			answer:        "Decision: source-satisfied\n- deploy/Dockerfile: already present; root Dockerfile is not covered.",
			requiredPaths: []string{"Dockerfile"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if SourceSatisfiedAnswerCoversPaths(tc.answer, tc.requiredPaths) {
				t.Fatalf("nearby extensionless path mention must not cover required path: %q", tc.answer)
			}
		})
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
