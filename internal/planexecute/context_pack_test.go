package planexecute

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
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
