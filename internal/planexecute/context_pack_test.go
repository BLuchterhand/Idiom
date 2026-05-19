package planexecute

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/config"
	"idiom/internal/frontier/pathparse"
	"idiom/internal/workspace"
)

func TestComposePlannerPromptWithContextPackScopesBriefBody(t *testing.T) {
	brief := "## Files To Modify\n\n- pkg/target.go\n"
	contextPack := "## Context Pack\n\n- `pkg/noise.go` (candidate): related file\n"
	prompt := ComposePlannerPromptWithContextPack(brief, contextPack)

	if !strings.Contains(prompt, "## Context Pack") {
		t.Fatalf("prompt missing context pack:\n%s", prompt)
	}
	if !strings.Contains(prompt, "<!-- IDIOM:BRIEF-BODY-START -->") || !strings.Contains(prompt, "<!-- IDIOM:BRIEF-BODY-END -->") {
		t.Fatalf("prompt missing brief body markers:\n%s", prompt)
	}
	got := RequiredEditPathsFromPrompt(prompt)
	if len(got) != 1 || got[0] != "pkg/target.go" {
		t.Fatalf("required paths = %#v, want only brief-scoped target path", got)
	}
}

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

func TestPlanExecuteAllowedEditPathsIncludesPromptAllowedScope(t *testing.T) {
	prompt := `{
  "allowed_edit_paths": ["pkg/target.go"],
  "evidence": ["pkg/evidence.go"]
}`
	required := planExecuteRequiredEditPaths(Options{}, prompt)
	if len(required) != 0 {
		t.Fatalf("allowed_edit_paths must not define required coverage, got %#v", required)
	}
	allowed := planExecuteAllowedEditPaths(Options{
		AllowedEditPaths: []string{"pkg/explicit.go"},
	}, prompt)
	for _, want := range []string{"pkg/explicit.go", "pkg/target.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}
	if slices.Contains(allowed, "pkg/evidence.go") {
		t.Fatalf("evidence path must not bleed into allowed edit paths, got %#v", allowed)
	}
}

func TestAppendPlanExecuteScopeToBriefBodyMakesAllowedPathsParseable(t *testing.T) {
	prompt := ComposePlannerPromptWithContextPack("# Objective\n\nUpdate banner.", "## Context Pack\n\n- `noise.go`")
	got := appendPlanExecuteScopeToBriefBody(prompt, nil, []string{"internal/app/rendering.go"})
	scoped := pathparse.ScopeToBriefBody(got)

	if !strings.Contains(scoped, "allowed_edit_paths:") || !strings.Contains(scoped, "internal/app/rendering.go") {
		t.Fatalf("allowed scope was not inserted inside the brief body:\n%s", got)
	}
	allowed := AllowedEditPathsFromPrompt(got)
	if len(allowed) != 1 || allowed[0] != "internal/app/rendering.go" {
		t.Fatalf("allowed paths not parseable from composed prompt: %#v\n%s", allowed, got)
	}
}

func TestRenderPromptContextPackSectionOmitsCacheArtifactPath(t *testing.T) {
	output := `{
  "pack_id": "abc123",
  "artifact_path": ".idiom/comprehension/views/abc123.json",
  "fresh": true,
  "freshness": {"policy":"read_through","operation":"context_pack","fresh_for_scope":true,"global_fresh":true},
  "candidate_files": [
    {"path":"internal/app/prompt_routing.go","role":"editable","reason":"matched task terms","score":0.91}
  ],
  "candidate_symbols": [
    {"qualified_name":"runtimeBriefForRoute","path":"internal/app/prompt_routing.go","kind":"func","start_line":91,"reason":"matched task terms"}
  ],
  "tests": [
    {"command":"go test ./internal/app","path":"internal/app/prompt_runtime_test.go"}
  ]
}`
	section, err := RenderPromptContextPackSection(workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 1 candidate file(s)",
		Output:  output,
	})
	if err != nil {
		t.Fatalf("render context pack: %v", err)
	}
	if strings.Contains(section, ".idiom/comprehension/views/abc123.json") {
		t.Fatalf("context pack section should not expose cache artifact as a workspace evidence path:\n%s", section)
	}
	for _, want := range []string{"context view artifact", "`internal/app/prompt_routing.go`", "`runtimeBriefForRoute`", "`go test ./internal/app`"} {
		if !strings.Contains(section, want) {
			t.Fatalf("expected %q in context section:\n%s", want, section)
		}
	}
}

func TestRenderPromptContextPackSectionAddsFallbackWhenNoTargets(t *testing.T) {
	section, err := RenderPromptContextPackSection(workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/empty.json with 0 candidate file(s)",
		Output:  `{"pack_id":"empty","candidate_files":[],"candidate_symbols":[],"tests":[]}`,
	})
	if err != nil {
		t.Fatalf("render context pack: %v", err)
	}
	for _, want := range []string{
		"## Context Discovery Fallback",
		"context_pack returned no candidate files",
		"`search_files`",
		"`search_content`",
		"`read_file`",
		"If a literal phrase misses, broaden",
		"<<QUESTION>>",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("fallback section missing %q:\n%s", want, section)
		}
	}
}

func TestRenderPromptContextDiscoveryFallbackSectionIncludesBriefScope(t *testing.T) {
	section := RenderPromptContextDiscoveryFallbackSection(PromptContextPackOptions{
		Title:             "Replace banner path with alias",
		RequiredEditPaths: []string{"internal/app/rendering.go", "internal/app/rendering.go"},
	}, "context index unavailable\nretry timed out")
	for _, want := range []string{
		"## Context Discovery Fallback",
		"Replace banner path with alias",
		"internal/app/rendering.go",
		"context index unavailable retry timed out",
		"`search_files`",
		"`search_content`",
		"`read_file`",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("fallback section missing %q:\n%s", want, section)
		}
	}
}

func TestBuildPromptContextPackSectionSeedsRequiredEditPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexecutecontext\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "noise.go"), []byte("package pkg\n\nfunc Noise() string { return \"noise\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	brief := "# Plan-execute atom\n\n## Task\n\nPlan a change to Noise.\n\n## Files To Modify\n\n- pkg/target.go\n"
	var stderr bytes.Buffer
	section := BuildPromptContextPackSection(config.Config{Workspace: config.Workspace{Root: root, CWD: root}}, PromptContextPackOptions{
		Mode:              "plan-execute",
		Title:             "Plan-execute atom",
		Brief:             brief,
		ProjectRoot:       root,
		RequiredEditPaths: []string{"pkg/target.go"},
	}, &stderr)
	if !strings.Contains(section, "`pkg/target.go`") {
		t.Fatalf("automatic context pack should include required edit path, stderr=%s\nsection:\n%s", stderr.String(), section)
	}
}
