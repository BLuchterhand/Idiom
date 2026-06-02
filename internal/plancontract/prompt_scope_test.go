package plancontract

import (
	"slices"
	"strings"
	"testing"
)

func TestRequiredEditPathsFromPromptIgnoresAllowedEditPaths(t *testing.T) {
	prompt := `
## Allowed Edit Paths

- pkg/target.go
- pkg/optional.go

## Evidence

- pkg/evidence.go
`
	if got := RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		t.Fatalf("allowed edit paths must not define required coverage, got %#v", got)
	}
	allowed := AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"pkg/target.go", "pkg/optional.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}
	if slices.Contains(allowed, "pkg/evidence.go") {
		t.Fatalf("evidence path must not bleed into allowed edit paths, got %#v", allowed)
	}
}

func TestRequiredEditPathsFromPromptCapturesInlineOnlyColonList(t *testing.T) {
	prompt := "Implement a bounded harness fix. Required edit paths only: internal/planexecute/prompt.go, internal/planexecute/context_pack_test.go, internal/app/update_handlers.go, internal/app/lifecycle.go."
	want := []string{
		"internal/planexecute/prompt.go",
		"internal/planexecute/context_pack_test.go",
		"internal/app/update_handlers.go",
		"internal/app/lifecycle.go",
	}
	if got := RequiredEditPathsFromPrompt(prompt); !slices.Equal(got, want) {
		t.Fatalf("required paths = %#v, want %#v", got, want)
	}
}

func TestRequiredEditPathsFromPromptIgnoresTrailingSlashProse(t *testing.T) {
	prompt := "Implement the change. Required edit paths: `cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`. Add focused tests for missing `--effort-root` and mutually exclusive/missing mode flags under JSON output."
	got := RequiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"}
	if len(got) != len(want) {
		t.Fatalf("required paths = %#v, want %#v", got, want)
	}
	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Fatalf("missing required path %q, got %#v", path, got)
		}
	}
	for _, forbidden := range []string{"exclusive/missing", "cmd/idiom"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("slash prose or truncated directory leaked into required paths: %#v", got)
		}
	}
}

func TestRequiredEditPathsFromPromptRejectsDirectoryLikeObligations(t *testing.T) {
	prompt := strings.Join([]string{
		"## Files To Modify",
		"",
		"- cmd/idiom",
		"- cmd/idiom/product_workflow.go",
		"- Makefile",
	}, "\n")
	got := RequiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "Makefile"}
	if len(got) != len(want) {
		t.Fatalf("expected file-only required paths %#v, got %#v", want, got)
	}
	for _, path := range want {
		if !slices.Contains(got, path) {
			t.Fatalf("missing required path %q, got %#v", path, got)
		}
	}
	if slices.Contains(got, "cmd/idiom") {
		t.Fatalf("directory-like target must not become an exact required edit path: %#v", got)
	}
}

func TestAllowedEditPathsFromPromptIncludesStructuredAndInlineScope(t *testing.T) {
	prompt := strings.Join([]string{
		`{"allowed_edit_paths": ["pkg/target.go"]}`,
		"Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go.",
		"Use pkg/evidence.go as read-only evidence.",
	}, "\n")
	if required := RequiredEditPathsFromPrompt(prompt); len(required) != 0 {
		t.Fatalf("allowed scope must not define required coverage, got %#v", required)
	}
	allowed := AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"pkg/target.go", "pkg/feature.go", "pkg/feature_test.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}
	if slices.Contains(allowed, "pkg/evidence.go") {
		t.Fatalf("read-only evidence sentence must not become allowed edit scope, got %#v", allowed)
	}
}
