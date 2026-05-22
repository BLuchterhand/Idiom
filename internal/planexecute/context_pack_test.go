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

func TestPromptContextPackNeedsFreshnessRecovery(t *testing.T) {
	cases := []workspace.ToolResult{
		{Tool: "context_pack", Error: "freshness_blocker"},
		{Tool: "context_pack", Summary: "context_pack blocked by context freshness (stale_unbounded_graph)"},
		{Tool: "context_pack", Output: `{"error":"freshness_blocker"}`},
	}
	for _, result := range cases {
		if !promptContextPackNeedsFreshnessRecovery(result) {
			t.Fatalf("expected freshness recovery for %#v", result)
		}
	}
	if promptContextPackNeedsFreshnessRecovery(workspace.ToolResult{Tool: "context_pack", Summary: "Context pack wrote view"}) {
		t.Fatal("successful context_pack summary should not trigger freshness recovery")
	}
}

func TestContextDiscoveryFallbackUsesLaneNeutralEditBearingLanguage(t *testing.T) {
	got := RenderPromptContextDiscoveryFallbackSection(PromptContextPackOptions{
		Title:             "Update target behavior",
		RequiredEditPaths: []string{"pkg/target.go"},
	}, "cache unavailable")

	for _, want := range []string{
		"before the first edit-bearing action",
		"before the first `submit_plan` or write action",
		"Required edit paths from brief: pkg/target.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, got)
		}
	}
	for _, notWant := range []string{"before writing", "before the first write"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("fallback prompt should not use implementation-only phrase %q:\n%s", notWant, got)
		}
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

func TestRequiredEditPathsFromPromptCapturesInlineColonList(t *testing.T) {
	prompt := "Implement the change. Required edit paths: cmd/idiom/product_workflow.go, cmd/idiom/product_workflow_test.go"
	got := RequiredEditPathsFromPrompt(prompt)
	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing required path %q, got %#v", want, got)
		}
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
	got := RequiredEditPathsFromPrompt(prompt)
	if !slices.Equal(got, want) {
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

func TestPlanExecuteAllowedEditPathsIncludesInlineNaturalLanguageScope(t *testing.T) {
	prompt := "Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go. Use pkg/evidence.go as read-only evidence."

	required := planExecuteRequiredEditPaths(Options{}, prompt)
	if len(required) != 0 {
		t.Fatalf("inline candidate scope must not define required coverage, got %#v", required)
	}
	allowed := planExecuteAllowedEditPaths(Options{}, prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing inline allowed path %q, got %#v", want, allowed)
		}
	}
	if slices.Contains(allowed, "pkg/evidence.go") {
		t.Fatalf("read-only evidence sentence must not become allowed edit scope, got %#v", allowed)
	}
}

func TestPlanExecuteEditableFilesHeadingIsCandidateScopeOnly(t *testing.T) {
	prompt := `Editable Files:
- pkg/feature.go
- pkg/feature_test.go
`
	if required := planExecuteRequiredEditPaths(Options{}, prompt); len(required) != 0 {
		t.Fatalf("editable files must not define required coverage, got %#v", required)
	}
	allowed := planExecuteAllowedEditPaths(Options{}, prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("editable files heading missing allowed path %q, got %#v", want, allowed)
		}
	}
}

func TestPlanScopeContractIDTracksNormalizedContent(t *testing.T) {
	contract := NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil)
	originalID := contract.ContractID
	contract.ContractID = "stale"
	contract.RequiredCoveragePaths = []string{"pkg/b.go"}
	contract = contract.Normalized()

	if contract.ContractID == "" || contract.ContractID == "stale" || contract.ContractID == originalID {
		t.Fatalf("contract id should be regenerated from normalized content, got %q original=%q", contract.ContractID, originalID)
	}
}

func TestPlanExecuteEditSetObligationsDoNotPromoteAllowedPathsToRequiredPaths(t *testing.T) {
	prompt := `Implement the feature and add tests.

	allowed_edit_paths:
- pkg/feature.go
- pkg/feature_test.go
`
	required := planExecuteRequiredEditPaths(Options{}, prompt)
	if len(required) != 0 {
		t.Fatalf("edit-set obligations and allowed scope must not define required paths, got %#v", required)
	}
	allowed := planExecuteAllowedEditPaths(Options{}, prompt)
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		if !slices.Contains(allowed, want) {
			t.Fatalf("missing allowed path %q, got %#v", want, allowed)
		}
	}
	obligations := planExecuteEditSetObligations(Options{}, prompt)
	if !obligations.Has(EditSetObligationKindTestEdit) {
		t.Fatalf("expected test edit obligation, got %#v", obligations)
	}
	if !obligations.Has(EditSetObligationKindImplementationEdit) {
		t.Fatalf("expected implementation edit obligation, got %#v", obligations)
	}
}

func TestPlanExecuteSourceSatisfiedUsesNamedAllowedEvidenceOnly(t *testing.T) {
	answer := strings.Join([]string{
		"Decision: source-satisfied",
		"- pkg/feature.go: requested final state is already present.",
	}, "\n")
	paths := planExecuteSourceSatisfiedEvidencePaths(answer, nil, []string{"pkg/feature.go", "pkg/feature_test.go"})
	if len(paths) != 1 || paths[0] != "pkg/feature.go" {
		t.Fatalf("allowed scope must not become source-satisfied coverage obligations, got %#v", paths)
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

func TestRenderPromptContextPackSectionPrefersSiblingTestsOverUnrelatedTestCandidates(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "internal", "productworkflow", "migration_apply_test.go")
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatalf("mkdir test dir: %v", err)
	}
	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		t.Fatalf("write sibling test: %v", err)
	}
	output := `{
  "pack_id": "abc123",
  "fresh": true,
  "candidate_files": [
    {"path":"internal/productworkflow/migration_apply.go","role":"editable","reason":"matched task terms","score":5.0},
    {"path":"cmd/idiom/lane_test.go","role":"test","reason":"matched task terms","score":4.6}
  ],
  "candidate_symbols": [
    {"qualified_name":"TestRunLaneMainEmbedsCanonicalPlanExecutePreamble","path":"cmd/idiom/lane_test.go","kind":"test","start_line":908,"reason":"matched task terms"}
  ],
  "tests": [
    {"command":"go test ./cmd/idiom","path":"cmd/idiom/lane_test.go"}
  ]
}`
	section, err := RenderPromptContextPackSectionWithOptions(workspace.ToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 2 candidate file(s)",
		Output:  output,
	}, PromptContextPackOptions{ProjectRoot: root})
	if err != nil {
		t.Fatalf("render context pack: %v", err)
	}
	for _, want := range []string{
		"`internal/productworkflow/migration_apply.go`",
		"`go test ./internal/productworkflow` - internal/productworkflow/migration_apply_test.go",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("expected %q in context section:\n%s", want, section)
		}
	}
	for _, forbidden := range []string{"cmd/idiom/lane_test.go", "`go test ./cmd/idiom`", "TestRunLaneMainEmbedsCanonicalPlanExecutePreamble"} {
		if strings.Contains(section, forbidden) {
			t.Fatalf("context section should omit unrelated test candidate %q:\n%s", forbidden, section)
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
