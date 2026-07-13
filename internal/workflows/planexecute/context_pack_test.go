package planexecute

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/pathparse"
	plancontract "idiom/internal/contracts/plan"
	"idiom/internal/platform/config"
)

var _ = ginkgo.It("compose planner prompt with context pack scopes brief body", func() {
	brief := "## Files To Modify\n\n- pkg/target.go\n"
	contextPack := "## Context Pack\n\n- `pkg/noise.go` (candidate): related file\n"
	prompt := ComposePlannerPromptWithContextPack(brief, contextPack)
	gomega.Expect(strings.Contains(prompt, "## Context Pack")).To(gomega.BeTrue(), fmt.Sprintf("prompt missing context pack:\n%s", prompt))
	gomega.Expect(prompt).To(gomega.ContainSubstring("<!-- IDIOM:BRIEF-BODY-START -->"), fmt.Sprintf("prompt missing brief body markers:\n%s", prompt))
	gomega.Expect(prompt).To(gomega.ContainSubstring("<!-- IDIOM:BRIEF-BODY-END -->"), fmt.Sprintf("prompt missing brief body markers:\n%s", prompt))

	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("required paths = %#v, want only brief-scoped target path", got))
	gomega.Expect(got[0]).To(gomega.Equal("pkg/target.go"), fmt.Sprintf("required paths = %#v, want only brief-scoped target path", got))
})

var _ = ginkgo.It("prompt context pack needs freshness recovery", func() {
	cases := []PromptContextToolResult{
		{Tool: "context_pack", Error: "freshness_blocker"},
		{Tool: "context_pack", Summary: "context_pack blocked by context freshness (stale_unbounded_graph)"},
		{Tool: "context_pack", Output: `{"error":"freshness_blocker"}`},
	}
	for _, result := range cases {
		gomega.Expect(promptContextPackNeedsFreshnessRecovery(result)).To(gomega.BeTrue(), fmt.Sprintf("expected freshness recovery for %#v", result))

	}
	gomega.Expect(promptContextPackNeedsFreshnessRecovery(PromptContextToolResult{Tool: "context_pack", Summary: "Context pack wrote view"})).To(gomega.BeFalse(), fmt.Sprint("successful context_pack summary should not trigger freshness recovery"))
})

var _ = ginkgo.It("context discovery fallback uses lane neutral edit bearing language", func() {
	got := RenderPromptContextDiscoveryFallbackSection(PromptContextPackOptions{
		Title:             "Update target behavior",
		RequiredEditPaths: []string{"pkg/target.go"},
	}, "cache unavailable")

	for _, want := range []string{
		"before the first edit-bearing action",
		"before the first `submit_plan` or write action",
		"Required edit paths from brief: pkg/target.go",
	} {
		gomega.Expect(strings.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("fallback prompt missing %q:\n%s", want, got))

	}

	for _, notWant := range []string{"before writing", "before the first write"} {
		gomega.Expect(strings.Contains(got, notWant)).To(gomega.BeFalse(), fmt.Sprintf("fallback prompt should not use implementation-only phrase %q:\n%s", notWant, got))

	}
})

var _ = ginkgo.It("required edit paths from prompt ignores allowed edit paths", func() {
	prompt := `
## Allowed Edit Paths

- pkg/target.go
- pkg/optional.go

## Evidence

- pkg/evidence.go
`
	if got := plancontract.RequiredEditPathsFromPrompt(prompt); len(got) != 0 {
		ginkgo.Fail(fmt.Sprintf("allowed edit paths must not define required coverage, got %#v", got))
	}

	allowed := plancontract.AllowedEditPathsFromPrompt(prompt)
	for _, want := range []string{"pkg/target.go", "pkg/optional.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed path %q, got %#v", want, allowed))

	}
	gomega.Expect(slices.Contains(allowed, "pkg/evidence.go")).To(gomega.BeFalse(), fmt.Sprintf("evidence path must not bleed into allowed edit paths, got %#v", allowed))
})

var _ = ginkgo.It("required edit paths from prompt captures inline colon list", func() {
	prompt := "Implement the change. Required edit paths: cmd/idiom/product_workflow.go, cmd/idiom/product_workflow_test.go"
	got := plancontract.RequiredEditPathsFromPrompt(prompt)

	for _, want := range []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"} {
		gomega.Expect(slices.Contains(got, want)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got %#v", want, got))

	}
})

var _ = ginkgo.It("required edit paths from prompt captures inline only colon list", func() {
	prompt := "Implement a bounded harness fix. Required edit paths only: internal/workflows/planexecute/prompt.go, internal/workflows/planexecute/context_pack_test.go, internal/surfaces/tui/app/update_handlers.go, internal/surfaces/tui/app/lifecycle.go."
	want := []string{
		"internal/workflows/planexecute/prompt.go",
		"internal/workflows/planexecute/context_pack_test.go",
		"internal/surfaces/tui/app/update_handlers.go",
		"internal/surfaces/tui/app/lifecycle.go",
	}
	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("required paths = %#v, want %#v", got, want))
})

var _ = ginkgo.It("required edit paths from prompt ignores trailing slash prose", func() {
	prompt := "Implement the change. Required edit paths: `cmd/idiom/product_workflow.go`, `cmd/idiom/product_workflow_test.go`. Add focused tests for missing `--effort-root` and mutually exclusive/missing mode flags under JSON output."
	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "cmd/idiom/product_workflow_test.go"}
	gomega.Expect(len(got) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("required paths = %#v, want %#v", got, want))

	for _, path := range want {
		gomega.Expect(slices.Contains(got, path)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got %#v", path, got))

	}

	for _, forbidden := range []string{"exclusive/missing", "cmd/idiom"} {
		gomega.Expect(slices.Contains(got, forbidden)).To(gomega.BeFalse(), fmt.Sprintf("slash prose or truncated directory leaked into required paths: %#v", got))

	}
})

var _ = ginkgo.It("required edit paths from prompt rejects directory like obligations", func() {
	prompt := strings.Join([]string{
		"## Files To Modify",
		"",
		"- cmd/idiom",
		"- cmd/idiom/product_workflow.go",
		"- Makefile",
	}, "\n")
	got := plancontract.RequiredEditPathsFromPrompt(prompt)
	want := []string{"cmd/idiom/product_workflow.go", "Makefile"}
	gomega.Expect(len(got) != len(want)).To(gomega.BeFalse(), fmt.Sprintf("expected file-only required paths %#v, got %#v", want, got))

	for _, path := range want {
		gomega.Expect(slices.Contains(got, path)).To(gomega.BeTrue(), fmt.Sprintf("missing required path %q, got %#v", path, got))

	}
	gomega.Expect(slices.Contains(got, "cmd/idiom")).To(gomega.BeFalse(), fmt.Sprintf("directory-like target must not become an exact required edit path: %#v", got))
})

var _ = ginkgo.It("plan execute allowed edit paths includes prompt allowed scope", func() {
	prompt := `{
  "allowed_edit_paths": ["pkg/target.go"],
  "evidence": ["pkg/evidence.go"]
}`
	required := planExecuteScopeContract(Options{}, prompt).RequiredCoveragePaths
	gomega.Expect(len(required) != 0).To(gomega.BeFalse(), fmt.Sprintf("allowed_edit_paths must not define required coverage, got %#v", required))

	allowed := planExecuteScopeContract(Options{
		AllowedEditPaths: []string{"pkg/explicit.go"},
	}, prompt).AllowedCandidatePaths
	for _, want := range []string{"pkg/explicit.go", "pkg/target.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed path %q, got %#v", want, allowed))

	}
	gomega.Expect(slices.Contains(allowed, "pkg/evidence.go")).To(gomega.BeFalse(), fmt.Sprintf("evidence path must not bleed into allowed edit paths, got %#v", allowed))
})

var _ = ginkgo.It("plan execute allowed edit paths includes inline natural language scope", func() {
	prompt := "Implement the behavior, but only edit pkg/feature.go and pkg/feature_test.go. Use pkg/evidence.go as read-only evidence."

	required := planExecuteScopeContract(Options{}, prompt).RequiredCoveragePaths
	gomega.Expect(len(required) != 0).To(gomega.BeFalse(), fmt.Sprintf("inline candidate scope must not define required coverage, got %#v", required))

	allowed := planExecuteScopeContract(Options{}, prompt).AllowedCandidatePaths
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing inline allowed path %q, got %#v", want, allowed))

	}
	gomega.Expect(slices.Contains(allowed, "pkg/evidence.go")).To(gomega.BeFalse(), fmt.Sprintf("read-only evidence sentence must not become allowed edit scope, got %#v", allowed))
})

var _ = ginkgo.It("plan execute scope contract does not reinfer prompt obligations when typed scope present", func() {
	prompt := strings.Join([]string{
		"## Context Pack",
		"",
		"Generated context mentions feature fallback and add tests.",
		"",
		pathparse.WrapBriefBody("# Objective\n\nImplement the feature and add tests."),
	}, "\n")
	opts := Options{
		ScopeContract: plancontract.NewPlanScopeContract(nil, []string{"pkg/feature.go", "pkg/feature_test.go"}, nil),
	}

	contract := planExecuteScopeContract(opts, prompt)
	gomega.Expect(len(contract.EditSetObligations) != 0).To(gomega.BeFalse(), fmt.Sprintf("typed scope owns edit-set obligations; prompt prose must not re-infer them: %#v", contract.EditSetObligations))
})

var _ = ginkgo.It("plan execute scope contract preserves typed obligation sources", func() {
	opts := Options{
		ScopeContract: plancontract.PlanScopeContract{
			EditSetObligations: plancontract.EditSetObligations{{Kind: plancontract.EditSetObligationKindTestEdit}},
		},
		EditSetObligations: plancontract.EditSetObligations{{Kind: plancontract.EditSetObligationKindImplementationEdit}},
	}

	contract := planExecuteScopeContract(opts, "")
	got := map[plancontract.EditSetObligationKind]string{}

	for _, obligation := range contract.EditSetObligations {
		got[obligation.Kind] = obligation.Source
	}
	gomega.Expect(got[plancontract.EditSetObligationKindTestEdit] != "scope_contract").To(gomega.BeFalse(), fmt.Sprintf("scope contract obligation source = %q, want scope_contract; obligations=%#v", got[plancontract.EditSetObligationKindTestEdit], contract.EditSetObligations))
	gomega.Expect(got[plancontract.EditSetObligationKindImplementationEdit] != "options").To(gomega.BeFalse(), fmt.Sprintf("options obligation source = %q, want options; obligations=%#v", got[plancontract.EditSetObligationKindImplementationEdit], contract.EditSetObligations))
})

var _ = ginkgo.It("plan execute editable files heading is candidate scope only", func() {
	prompt := `Editable Files:
- pkg/feature.go
- pkg/feature_test.go
`
	if required := planExecuteScopeContract(Options{}, prompt).RequiredCoveragePaths; len(required) != 0 {
		ginkgo.Fail(fmt.Sprintf("editable files must not define required coverage, got %#v", required))
	}

	allowed := planExecuteScopeContract(Options{}, prompt).AllowedCandidatePaths
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("editable files heading missing allowed path %q, got %#v", want, allowed))

	}
})

var _ = ginkgo.It("plan execute unscoped objective mentioned paths become candidate scope", func() {
	prompt := "Add the atom_spec lane to cmd/idiom/lane.go and the registry in internal/runtime/agent/runner_plan_then_execute_guard.go."

	if required := planExecuteScopeContract(Options{}, prompt).RequiredCoveragePaths; len(required) != 0 {
		ginkgo.Fail(fmt.Sprintf("plain objective path mentions must not define required coverage, got %#v", required))
	}

	allowed := planExecuteScopeContract(Options{}, prompt).AllowedCandidatePaths
	for _, want := range []string{"cmd/idiom/lane.go", "internal/runtime/agent/runner_plan_then_execute_guard.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing objective-mentioned candidate path %q, got %#v", want, allowed))

	}
})

var _ = ginkgo.It("repair missing allowed candidate paths uses unique code basename", func() {
	root := ginkgo.GinkgoT().TempDir()
	mustWritePlanExecuteTestFile(root, "internal/surfaces/cli/lane/lane.go", "package lane\n")

	got, repairs := repairMissingAllowedCandidatePaths(root, []string{"cmd/idiom/lane.go"})
	gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("repaired paths = %#v, want unique lane.go target", got))
	gomega.Expect(got[0]).To(gomega.Equal("internal/surfaces/cli/lane/lane.go"), fmt.Sprintf("repaired paths = %#v, want unique lane.go target", got))
	gomega.Expect(repairs).To(gomega.HaveLen(1), fmt.Sprintf("repair metadata = %#v", repairs))
	gomega.Expect(repairs[0].Original).To(gomega.Equal("cmd/idiom/lane.go"), fmt.Sprintf("repair metadata = %#v", repairs))
	gomega.Expect(repairs[0].Resolved).To(gomega.Equal("internal/surfaces/cli/lane/lane.go"), fmt.Sprintf("repair metadata = %#v", repairs))
})

var _ = ginkgo.It("repair missing allowed candidate paths keeps ambiguous basename", func() {
	root := ginkgo.GinkgoT().TempDir()
	mustWritePlanExecuteTestFile(root, "internal/a/lane.go", "package a\n")
	mustWritePlanExecuteTestFile(root, "internal/b/lane.go", "package b\n")

	got, repairs := repairMissingAllowedCandidatePaths(root, []string{"cmd/idiom/lane.go"})
	gomega.Expect(got).To(gomega.HaveLen(1), fmt.Sprintf("ambiguous path should remain unchanged, got %#v", got))
	gomega.Expect(got[0]).To(gomega.Equal("cmd/idiom/lane.go"), fmt.Sprintf("ambiguous path should remain unchanged, got %#v", got))
	gomega.Expect(len(repairs) != 0).To(gomega.BeFalse(), fmt.Sprintf("ambiguous path should not produce repair metadata, got %#v", repairs))
})

var _ = ginkgo.It("apply candidate path repairs to prompt removes stale alias", func() {
	prompt := "Add the atom_spec lane to cmd/idiom/lane.go and update docs mentioning cmd/idiom/lane.go."
	got := applyCandidatePathRepairsToPrompt(prompt, []candidatePathRepair{{
		Original: "cmd/idiom/lane.go",
		Resolved: "internal/surfaces/cli/lane/lane.go",
		Reason:   "missing_candidate_unique_basename_match",
	}})

	body, metadata, ok := strings.Cut(got, "Path repair metadata:")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("repair metadata missing from prompt:\n%s", got))
	gomega.Expect(strings.Contains(body, "cmd/idiom/lane.go")).To(gomega.BeFalse(), fmt.Sprintf("stale alias leaked into repaired prompt body:\n%s", got))
	gomega.Expect(strings.Contains(metadata, "cmd/idiom/lane.go -> internal/surfaces/cli/lane/lane.go")).To(gomega.BeTrue(), fmt.Sprintf("repair metadata should preserve original-to-resolved mapping:\n%s", got))
	gomega.Expect(strings.Count(body, "internal/surfaces/cli/lane/lane.go") != 2).To(gomega.BeFalse(), fmt.Sprintf("resolved path not substituted everywhere:\n%s", got))
})

func mustWritePlanExecuteTestFile(root string, rel string, body string) {
	ginkgo.GinkgoHelper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("create parent dirs for %s: %v", rel, err))
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write %s: %v", rel, err))
	}
}

var _ = ginkgo.It("plan scope contract id tracks normalized content", func() {
	contract := plancontract.NewPlanScopeContract([]string{"pkg/a.go"}, nil, nil)
	originalID := contract.ContractID

	contract.ContractID = "stale"
	contract.RequiredCoveragePaths = []string{"pkg/b.go"}
	contract = contract.Normalized()
	gomega.Expect(contract.ContractID).NotTo(gomega.Equal(""), fmt.Sprintf("contract id should be regenerated from normalized content, got %q original=%q", contract.ContractID, originalID))
	gomega.Expect(contract.ContractID).NotTo(gomega.Equal("stale"), fmt.Sprintf("contract id should be regenerated from normalized content, got %q original=%q", contract.ContractID, originalID))
	gomega.Expect(contract.ContractID).NotTo(gomega.Equal(originalID), fmt.Sprintf("contract id should be regenerated from normalized content, got %q original=%q", contract.ContractID, originalID))
})

var _ = ginkgo.It("plan execute edit set obligations do not promote allowed paths to required paths", func() {
	prompt := `Implement the feature and add tests.

	allowed_edit_paths:
- pkg/feature.go
- pkg/feature_test.go
`
	required := planExecuteScopeContract(Options{}, prompt).RequiredCoveragePaths
	gomega.Expect(len(required) != 0).To(gomega.BeFalse(), fmt.Sprintf("edit-set obligations and allowed scope must not define required paths, got %#v", required))

	allowed := planExecuteScopeContract(Options{}, prompt).AllowedCandidatePaths
	for _, want := range []string{"pkg/feature.go", "pkg/feature_test.go"} {
		gomega.Expect(slices.Contains(allowed, want)).To(gomega.BeTrue(), fmt.Sprintf("missing allowed path %q, got %#v", want, allowed))

	}

	obligations := planExecuteScopeContract(Options{}, prompt).EditSetObligations
	gomega.Expect(len(obligations) != 0).To(gomega.BeFalse(), fmt.Sprintf("prompt prose must not create edit-set obligations, got %#v", obligations))
})

var _ = ginkgo.It("plan execute source satisfied uses named allowed evidence only", func() {
	answer := strings.Join([]string{
		"Decision: source-satisfied",
		"- pkg/feature.go: requested final state is already present.",
	}, "\n")
	paths := planExecuteSourceSatisfiedEvidencePaths(answer, nil, []string{"pkg/feature.go", "pkg/feature_test.go"})
	gomega.Expect(paths).To(gomega.HaveLen(1), fmt.Sprintf("allowed scope must not become source-satisfied coverage obligations, got %#v", paths))
	gomega.Expect(paths[0]).To(gomega.Equal("pkg/feature.go"), fmt.Sprintf("allowed scope must not become source-satisfied coverage obligations, got %#v", paths))
})

var _ = ginkgo.It("plan execute missing source satisfied coverage paths uses exact coverage", func() {
	answer := "Decision: source-satisfied\n- pkg/parser.go.bak: already present.\n- pkg/parser_test.go: already present."
	got := plancontract.SourceSatisfiedMissingCoveragePaths(answer, []string{"pkg/parser.go", "pkg/parser_test.go"})
	want := []string{"pkg/parser.go"}
	gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("missing source-satisfied coverage paths = %#v, want %#v", got, want))
})

var _ = ginkgo.It("plan execute auto source satisfied answer covers required paths", func() {
	answer := planExecuteAutoSourceSatisfiedAnswer([]string{"pkg/parser.go", "pkg/parser_test.go"})
	got := plancontract.SourceSatisfiedMissingCoveragePaths(answer, []string{"pkg/parser.go", "pkg/parser_test.go"})
	gomega.Expect(len(got) != 0).To(gomega.BeFalse(), fmt.Sprintf("auto source-satisfied answer should cover required paths, missing %#v in:\n%s", got, answer))
})

var _ = ginkgo.It("append plan execute scope to brief body makes allowed paths parseable", func() {
	prompt := ComposePlannerPromptWithContextPack("# Objective\n\nUpdate banner.", "## Context Pack\n\n- `noise.go`")
	got := appendPlanExecuteScopeToBriefBody(prompt, nil, []string{"internal/surfaces/tui/app/rendering.go"})
	scoped := pathparse.ScopeToBriefBody(got)
	gomega.Expect(scoped).To(gomega.ContainSubstring("allowed_edit_paths:"), fmt.Sprintf("allowed scope was not inserted inside the brief body:\n%s", got))
	gomega.Expect(scoped).To(gomega.ContainSubstring("internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("allowed scope was not inserted inside the brief body:\n%s", got))

	allowed := plancontract.AllowedEditPathsFromPrompt(got)
	gomega.Expect(allowed).To(gomega.HaveLen(1), fmt.Sprintf("allowed paths not parseable from composed prompt: %#v\n%s", allowed, got))
	gomega.Expect(allowed[0]).To(gomega.Equal("internal/surfaces/tui/app/rendering.go"), fmt.Sprintf("allowed paths not parseable from composed prompt: %#v\n%s", allowed, got))
})

var _ = ginkgo.It("append plan execute scope to brief body does not repeat required as allowed", func() {
	got := appendPlanExecuteScopeToBriefBody("Implement it.", []string{"pkg/feature.go", "pkg/feature_test.go"}, []string{"pkg/feature_test.go", "pkg/optional.go"})
	gomega.Expect(strings.Count(got, "pkg/feature_test.go") != 1).To(gomega.BeFalse(), fmt.Sprintf("required path should appear once in canonical scope:\n%s", got))

	allowed := plancontract.AllowedEditPathsFromPrompt(got)
	gomega.Expect(slices.Contains(allowed, "pkg/feature_test.go")).To(gomega.BeFalse(), fmt.Sprintf("required path must not remain allowed candidate scope: %#v\n%s", allowed, got))
	gomega.Expect(slices.Contains(allowed, "pkg/optional.go")).To(gomega.BeTrue(), fmt.Sprintf("optional allowed path missing from prompt: %#v\n%s", allowed, got))
})

var _ = ginkgo.It("render prompt context pack section omits cache artifact path", func() {
	output := `{
  "pack_id": "abc123",
  "artifact_path": ".idiom/comprehension/views/abc123.json",
  "fresh": true,
  "freshness": {"policy":"read_through","operation":"context_pack","fresh_for_scope":true,"global_fresh":true},
  "candidate_files": [
    {"path":"internal/surfaces/tui/app/prompt_routing.go","role":"editable","reason":"matched task terms","score":0.91}
  ],
  "candidate_symbols": [
    {"qualified_name":"runtimeBriefForRoute","path":"internal/surfaces/tui/app/prompt_routing.go","kind":"func","start_line":91,"reason":"matched task terms"}
  ],
  "tests": [
    {"command":"go test ./internal/surfaces/tui/app","path":"internal/surfaces/tui/app/prompt_runtime_test.go"}
  ]
}`

	section, err := RenderPromptContextPackSectionWithOptions(PromptContextToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 1 candidate file(s)",
		Output:  output,
	}, PromptContextPackOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("render context pack: %v", err))
	gomega.Expect(strings.Contains(section, ".idiom/comprehension/views/abc123.json")).To(gomega.BeFalse(), fmt.Sprintf("context pack section should not expose cache artifact as a workspace evidence path:\n%s", section))

	for _, want := range []string{
		"context view artifact",
		"context_pack_summary:",
		"pack_id: abc123",
		"packet_quality:",
		"localization_confidence",
		"source_owner_found",
		"candidate_files:",
		"internal/surfaces/tui/app/prompt_routing.go",
		"candidate_symbols:",
		"runtimeBriefForRoute",
		"tests:",
		"go test ./internal/surfaces/tui/app",
		"evidence_note:",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in context section:\n%s", want, section))

	}
	// Verify quality fields are present and substantive.
	gomega.Expect(strings.Contains(section, "localization_confidence:")).To(gomega.BeTrue(), fmt.Sprintf("context section missing localization_confidence:\n%s", section))
	gomega.Expect(strings.Contains(section, "source_owner_found:")).To(gomega.BeTrue(), fmt.Sprintf("context section missing source_owner_found:\n%s", section))
})

var _ = ginkgo.It("render prompt context pack section includes quality in preload", func() {
	root := ginkgo.GinkgoT().TempDir()
	testPath := filepath.Join(root, "internal", "workflows", "productworkflow", "migration_apply_test.go")

	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprintf("mkdir test dir: %v", err))
	}

	if err := os.WriteFile(testPath, []byte("package productworkflow\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprintf("write sibling test: %v", err))
	}

	output := `{
  "pack_id": "abc123",
  "fresh": true,
  "quality": {"localization_confidence":"medium","source_owner_found":true,"evidence_paths":["internal/workflows/productworkflow/migration_apply.go"],"reasons":["matched task terms"]},
  "candidate_files": [
    {"path":"internal/workflows/productworkflow/migration_apply.go","role":"editable","reason":"matched task terms","score":5.0},
    {"path":"internal/surfaces/cli/lane/lane_test.go","role":"test","reason":"matched task terms","score":4.6}
  ],
  "candidate_symbols": [
    {"qualified_name":"TestRunLaneMainEmbedsCanonicalPlanExecutePreamble","path":"internal/surfaces/cli/lane/lane_test.go","kind":"test","start_line":908,"reason":"matched task terms"}
  ],
  "tests": [
    {"command":"go test ./internal/surfaces/cli/lane","path":"internal/surfaces/cli/lane/lane_test.go"}
  ]
}`

	section, err := RenderPromptContextPackSectionWithOptions(PromptContextToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/abc123.json with 2 candidate file(s)",
		Output:  output,
	}, PromptContextPackOptions{ProjectRoot: root})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("render context pack: %v", err))

	// Verify the compact projection format with quality is present.
	for _, want := range []string{
		"context_pack_summary:",
		"pack_id: abc123",
		"packet_quality:",
		"localization_confidence: medium",
		"source_owner_found: true",
		"evidence_paths:",
		"internal/workflows/productworkflow/migration_apply.go",
		"evidence_note:",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in context section:\n%s", want, section))

	}

	// The old test verified sibling-test filtering; with the shared compact
	// projection, all candidates are rendered. Verify both candidates appear.
	for _, want := range []string{
		"internal/workflows/productworkflow/migration_apply.go",
		"internal/surfaces/cli/lane/lane_test.go",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("expected %q in context section:\n%s", want, section))

	}
})

var _ = ginkgo.It("render prompt context pack section adds fallback when no targets", func() {
	section, err := RenderPromptContextPackSectionWithOptions(PromptContextToolResult{
		Tool:    "context_pack",
		Summary: "Context pack wrote .idiom/comprehension/views/empty.json with 0 candidate file(s)",
		Output:  `{"pack_id":"empty","candidate_files":[],"candidate_symbols":[],"tests":[]}`,
	}, PromptContextPackOptions{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("render context pack: %v", err))

	for _, want := range []string{
		"## Context Discovery Fallback",
		"context_pack returned no candidate files",
		"`search_files`",
		"`search_content`",
		"`read_file`",
		"If a literal phrase misses, broaden",
		"<<QUESTION>>",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("fallback section missing %q:\n%s", want, section))

	}
})

var _ = ginkgo.It("render prompt context discovery fallback section includes brief scope", func() {
	section := RenderPromptContextDiscoveryFallbackSection(PromptContextPackOptions{
		Title:             "Replace banner path with alias",
		RequiredEditPaths: []string{"internal/surfaces/tui/app/rendering.go", "internal/surfaces/tui/app/rendering.go"},
	}, "context index unavailable\nretry timed out")
	for _, want := range []string{
		"## Context Discovery Fallback",
		"Replace banner path with alias",
		"internal/surfaces/tui/app/rendering.go",
		"context index unavailable retry timed out",
		"`search_files`",
		"`search_content`",
		"`read_file`",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("fallback section missing %q:\n%s", want, section))

	}
})

var _ = ginkgo.It("render prompt context discovery fallback section includes typed payload", func() {
	result := PromptContextToolResult{
		Tool:  "context_pack",
		Error: "context_pack did not return before the active context deadline",
		Output: `{
  "schema": "idiom.context_pack.fallback.v1",
  "status": "unavailable",
  "reason": "timeout",
  "tool": "context_pack",
  "retry_same_request": false,
  "uncertainty": "The context packet was not produced.",
  "fallback_capabilities": ["context_status", "search_files", "read_file"],
  "required_edit_paths": ["internal/runtime/agent/runner_core.go"]
}`,
	}
	section := RenderPromptContextDiscoveryFallbackSectionForResult(PromptContextPackOptions{
		Title: "Investigate routing failure",
	}, result)

	for _, want := range []string{
		"Context Pack Fallback Evidence",
		"Fallback reason: timeout",
		"Retry same request: false",
		"context_status, search_files, read_file",
		"internal/runtime/agent/runner_core.go",
	} {
		gomega.Expect(strings.Contains(section, want)).To(gomega.BeTrue(), fmt.Sprintf("typed fallback section missing %q:\n%s", want, section))

	}
})

var _ = ginkgo.It("build prompt context pack section seeds required edit paths", func() {
	root := ginkgo.GinkgoT().TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/planexecutecontext\n\ngo 1.22\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "target.go"), []byte("package pkg\n\nfunc Target() string { return \"target\" }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	if err := os.WriteFile(filepath.Join(root, "pkg", "noise.go"), []byte("package pkg\n\nfunc Noise() string { return \"noise\" }\n"), 0o644); err != nil {
		ginkgo.Fail(fmt.Sprint(err))
	}

	brief := "# Plan-execute atom\n\n## Task\n\nPlan a change to Noise.\n\n## Files To Modify\n\n- pkg/target.go\n"

	var stderr bytes.Buffer

	section := buildPromptContextPackSectionForTest(config.Config{Workspace: config.Workspace{Root: root, CWD: root}}, PromptContextPackOptions{
		Mode:              "plan-execute",
		Title:             "Plan-execute atom",
		Brief:             brief,
		ProjectRoot:       root,
		RequiredEditPaths: []string{"pkg/target.go"},
	}, &stderr)
	gomega.Expect(strings.Contains(section, "pkg/target.go")).To(gomega.BeTrue(), fmt.Sprintf("automatic context pack should include required edit path, stderr=%s\nsection:\n%s", stderr.String(), section))
	// Verify quality is present in the plan-execute preload.
	gomega.Expect(strings.Contains(section, "packet_quality:")).To(gomega.BeTrue(), fmt.Sprintf("context pack section should include packet_quality, stderr=%s\nsection:\n%s", stderr.String(), section))
	gomega.Expect(strings.Contains(section, "localization_confidence")).To(gomega.BeTrue(), fmt.Sprintf("context pack section should include localization_confidence, stderr=%s\nsection:\n%s", stderr.String(), section))
})
