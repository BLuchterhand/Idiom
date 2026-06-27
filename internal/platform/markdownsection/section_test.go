package markdownsection

import (
	"strings"
	"testing"
)

func TestByHeadingReturnsRequestedSection(t *testing.T) {
	markdown := strings.Join([]string{
		"# Title",
		"intro",
		"## Required Edits",
		"- one",
		"### Nested",
		"- nested",
		"## Next",
		"done",
	}, "\n")

	got := ByHeading(markdown, "required edits")
	want := "- one\n### Nested\n- nested"
	if got != want {
		t.Fatalf("ByHeading() = %q, want %q", got, want)
	}
}

func TestByHeadingIgnoresHeadingsInsideFences(t *testing.T) {
	markdown := strings.Join([]string{
		"```md",
		"## Required Edits",
		"ignored",
		"```",
		"## Required Edits",
		"kept",
	}, "\n")

	if got := ByHeading(markdown, "required edits"); got != "kept" {
		t.Fatalf("ByHeading() = %q, want kept", got)
	}
}

func TestByHeadingMatchesQualifiedHeadings(t *testing.T) {
	markdown := "## Required Edits (phase 1)\nbody\n## Other\nnext"
	if got := ByHeading(markdown, "required edits"); got != "body" {
		t.Fatalf("ByHeading() = %q, want body", got)
	}
}
